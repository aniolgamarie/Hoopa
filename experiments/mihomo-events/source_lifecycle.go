package mihomoevents

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"

	"hoopa/internal/config"
	"hoopa/protocol/telemetrywire"
)

var ErrInvalidSourceLifecycle = errors.New("experimental source lifecycle requires recovery")

type SourceFlow struct {
	SourceEpoch    config.CounterEpoch `json:"source_epoch"`
	FlowID         config.ID           `json:"flow_id"`
	OpenedEventID  config.ID           `json:"opened_event_id"`
	State          string              `json:"state"`
	Reason         string              `json:"reason"`
	LastCheckpoint *CounterEvent       `json:"last_checkpoint"`
	Final          *CounterEvent       `json:"final"`
	// TailBytes is zero only when a precise closed frame is present. Unknown
	// unobserved tails stay null, even if the last checkpoint happened to be zero.
	TailBytes                   *config.Bytes `json:"tail_bytes"`
	GapDimensions               []string      `json:"gap_dimensions"`
	terminal, shutdown, gapSeen bool
}

type SourceLifecycle struct {
	SourceEpoch      config.CounterEpoch `json:"source_epoch"`
	SourceInstanceID config.ID           `json:"source_instance_id"`
	ConfigGeneration config.Generation   `json:"config_generation"`
	LastSourceSeq    uint64              `json:"last_source_seq,string"`
	EpochFinality    string              `json:"epoch_finality"`
	Flows            []SourceFlow        `json:"flows"`
}

// ReadSourceLifecycle inspects one frozen source-frame snapshot (at most 8MiB).
// It is read-only, emits no ACK, and never appends a guessed final to an old
// epoch. Replaying the same durable census reproduces the same pending/gap;
// absence of a final or epoch marker cannot prove zero missing bytes. A caller
// must obtain a stable snapshot rather than race a live writer. This does not
// qualify source identity, disk durability, production reclamation or SQL.
func ReadSourceLifecycle(reader io.Reader, expectedEpoch string) (SourceLifecycle, error) {
	snapshot, err := readWireLifecycle(reader, expectedEpoch)
	if err != nil {
		return SourceLifecycle{}, err
	}
	return lifecycleFromWire(snapshot, config.CounterEpoch(expectedEpoch)), nil
}

func readWireLifecycle(reader io.Reader, expectedEpoch string, manifests ...telemetrywire.Manifest) (telemetrywire.Lifecycle, error) {
	invalid := func() (telemetrywire.Lifecycle, error) { return telemetrywire.Lifecycle{}, ErrInvalidSourceLifecycle }
	epoch, err := config.ParseID(expectedEpoch)
	if err != nil {
		return invalid()
	}
	var state telemetrywire.State
	limited := &io.LimitedReader{R: reader, N: 8*1024*1024 + 1}
	for {
		var header [4]byte
		n, err := io.ReadFull(limited, header[:])
		if err == io.EOF && n == 0 {
			break
		}
		if err != nil {
			return invalid()
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > sourceInputLimit {
			return invalid()
		}
		frame := make([]byte, int(size)+sha256.Size)
		if _, err := io.ReadFull(limited, frame); err != nil || limited.N < 1 {
			return invalid()
		}
		sum := sha256.Sum256(frame[:size])
		if !bytes.Equal(sum[:], frame[size:]) {
			return invalid()
		}
		e, err := telemetrywire.DecodeSourceEvent(frame[:size])
		if err != nil || e.SourceEpoch != string(epoch) || len(manifests) > 0 && telemetrywire.ValidateManifestEvent(manifests[0], e) != nil || state.Commit(e) != nil {
			return invalid()
		}
	}
	return state.Snapshot(), nil
}

func lifecycleFromWire(snapshot telemetrywire.Lifecycle, expected config.CounterEpoch) SourceLifecycle {
	out := SourceLifecycle{SourceEpoch: expected, SourceInstanceID: config.ID(snapshot.SourceInstanceID), ConfigGeneration: config.Generation(snapshot.ConfigGeneration), LastSourceSeq: snapshot.LastSourceSeq, EpochFinality: snapshot.EpochFinality, Flows: []SourceFlow{}}
	for _, f := range snapshot.Flows {
		flow := SourceFlow{SourceEpoch: config.CounterEpoch(f.SourceEpoch), FlowID: config.ID(f.FlowID), OpenedEventID: config.ID(f.OpenedEventID), State: f.State, Reason: f.Reason, GapDimensions: append([]string{}, f.GapDimensions...)}
		if f.LastCheckpoint != nil {
			v := counterFromWire(*f.LastCheckpoint)
			flow.LastCheckpoint = &v
		}
		if f.Final != nil {
			v := counterFromWire(*f.Final)
			flow.Final = &v
		}
		if f.TailBytes != nil {
			v := config.Bytes(*f.TailBytes)
			flow.TailBytes = &v
		}
		out.Flows = append(out.Flows, flow)
	}
	return out
}
