//go:build linux

package mihomoevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"

	"hoopa/experiments/telemetry"
	"hoopa/protocol/telemetrywire"
)

var (
	ErrReceiverOrder       = errors.New("source receiver sequence or epoch metadata invalid")
	ErrReceiverConflict    = errors.New("source receiver event content conflict")
	ErrReceiverUncertain   = errors.New("source receiver outcome uncertain; reopen required")
	ErrReceiverRecovery    = errors.New("source receiver recovery required")
	ErrReceiverUnavailable = errors.New("source receiver unavailable")
	ErrReceiverClosed      = errors.New("source receiver closed")
)

type receiverJournal interface {
	Append(string, string, json.RawMessage) (uint64, error)
	Close() error
}
type receiverKey struct{ epoch, id string }
type receiverReceipt struct {
	raw json.RawMessage
	seq uint64
}

// SourceReceiver is a Linux experiment for already delivered source events.
// It validates delivered flow lifecycles, not source completeness. Source
// positions are independent per epoch; receipt positions belong to the journal.
type SourceReceiver struct {
	mu               sync.Mutex
	journal          receiverJournal
	manifests        map[string]telemetrywire.Manifest
	epochs           map[string]*telemetrywire.State
	events           map[receiverKey]receiverReceipt
	poisoned, closed bool
}

func newSourceReceiver(j receiverJournal) *SourceReceiver {
	return &SourceReceiver{journal: j, epochs: map[string]*telemetrywire.State{}, events: map[receiverKey]receiverReceipt{}}
}

// OpenSourceReceiver requires an existing private directory. Journal reopen
// scans and syncs all frames first; every opaque payload is then strictly
// decoded and checked against its outer identity and preceding source position.
// An invalid record is never repaired, truncated or treated as an empty source.
func OpenSourceReceiver(privateExistingDir string) (*SourceReceiver, error) {
	return openSourceReceiver(privateExistingDir, nil)
}

// OpenSourceReceiverWithManifests binds known epochs before journal replay or receipt writes.
func OpenSourceReceiverWithManifests(dir string, rawManifests [][]byte) (*SourceReceiver, error) {
	manifests := map[string]telemetrywire.Manifest{}
	for _, raw := range rawManifests {
		m, err := telemetrywire.DecodeManifest(raw)
		if err != nil {
			return nil, ErrReceiverUnavailable
		}
		if _, exists := manifests[m.SourceEpoch]; exists {
			return nil, ErrReceiverUnavailable
		}
		manifests[m.SourceEpoch] = m
	}
	return openSourceReceiver(dir, manifests)
}
func openSourceReceiver(privateExistingDir string, manifests map[string]telemetrywire.Manifest) (*SourceReceiver, error) {
	j, err := telemetry.OpenJournal(privateExistingDir)
	if err != nil {
		if errors.Is(err, telemetry.ErrRecoveryRequired) {
			return nil, ErrReceiverRecovery
		}
		return nil, ErrReceiverUnavailable
	}
	r := newSourceReceiver(j)
	r.manifests = manifests
	for i, record := range j.Replay() {
		e, err := DecodeSourceEvent(record.Payload)
		if err != nil || record.Epoch != string(e.SourceEpoch) || record.ID != string(e.EventID) || record.Seq != uint64(i)+1 {
			_ = j.Close()
			return nil, ErrReceiverRecovery
		}
		if _, duplicate, err := r.check(e); err != nil || duplicate {
			_ = j.Close()
			return nil, ErrReceiverRecovery
		}
		if r.commit(e, record.Seq) != nil {
			_ = j.Close()
			return nil, ErrReceiverRecovery
		}
	}
	return r, nil
}

// check is read-only and requires the receiver mutex (or exclusive construction).
// Same-event retries precede the epoch_closed check so a lost marker ACK is safe.
func (r *SourceReceiver) check(e SourceEvent) (uint64, bool, error) {
	key := receiverKey{string(e.SourceEpoch), string(e.EventID)}
	if previous, ok := r.events[key]; ok {
		if !bytes.Equal(previous.raw, e.Raw) {
			return 0, false, ErrReceiverConflict
		}
		return previous.seq, true, nil
	}
	if m, ok := r.manifests[string(e.SourceEpoch)]; ok {
		if telemetrywire.ValidateManifestEvent(m, e.wire) != nil {
			return 0, false, ErrReceiverOrder
		}
	} else if e.wire.SchemaVersion == 5 {
		return 0, false, ErrReceiverOrder
	}
	state := r.epochs[key.epoch]
	if state == nil {
		state = &telemetrywire.State{}
	}
	if state.Validate(e.wire) != nil {
		return 0, false, ErrReceiverOrder
	}
	return 0, false, nil
}
func (r *SourceReceiver) commit(e SourceEvent, receiptSeq uint64) error {
	epoch := string(e.SourceEpoch)
	state := r.epochs[epoch]
	if state == nil {
		state = &telemetrywire.State{}
	}
	if err := state.Commit(e.wire); err != nil {
		return err
	}
	r.epochs[epoch] = state
	r.events[receiverKey{epoch, string(e.EventID)}] = receiverReceipt{append(json.RawMessage(nil), e.Raw...), receiptSeq}
	return nil
}

// Accept returns a receipt only after Journal.Append's complete Write+Sync.
// Shape/order errors never reach the journal. Any append failure conservatively
// poisons this receiver until a full reopen disambiguates the durable records.
func (r *SourceReceiver) Accept(raw []byte) (uint64, error) {
	e, err := DecodeSourceEvent(raw)
	if err != nil {
		return 0, ErrInvalidSourceEvent
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, ErrReceiverClosed
	}
	if r.poisoned {
		return 0, ErrReceiverUncertain
	}
	if seq, duplicate, err := r.check(e); err != nil || duplicate {
		return seq, err
	}
	seq, err := r.journal.Append(string(e.SourceEpoch), string(e.EventID), e.Raw)
	if err != nil || seq == 0 {
		r.poisoned = true
		return 0, ErrReceiverUncertain
	}
	if r.commit(e, seq) != nil {
		r.poisoned = true
		return 0, ErrReceiverUncertain
	}
	return seq, nil
}

func (r *SourceReceiver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.journal.Close() != nil {
		return ErrReceiverUnavailable
	}
	return nil
}
