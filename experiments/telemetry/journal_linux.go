//go:build linux

// Package telemetry contains an experimental journal for events already delivered
// to this process. It does not establish source completeness or production storage.
package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"hoopa/internal/config"
)

const (
	lockName     = "journal.lock"
	dataName     = "events.frames"
	payloadLimit = 64 * 1024
	frameLimit   = 96 * 1024
)

var (
	ErrConflict         = errors.New("journal event content conflict")
	ErrUncertain        = errors.New("journal append outcome uncertain; reopen required")
	ErrRecoveryRequired = errors.New("journal recovery required")
	ErrLocked           = errors.New("journal already locked")
	ErrUnsafe           = errors.New("journal unsafe file or directory")
	ErrInvalid          = errors.New("journal invalid event")
	ErrClosed           = errors.New("journal closed")
	ErrIO               = errors.New("journal I/O failure")
)

type Record struct {
	Seq     uint64
	Epoch   string
	ID      string
	Payload json.RawMessage
}
type eventKey struct{ epoch, id string }
type journalFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}
type Journal struct {
	mu               sync.Mutex
	root             *os.Root
	lock             *os.File
	file             journalFile
	records          []Record
	index            map[eventKey]int
	poisoned, closed bool
}
type envelope struct {
	FormatVersion uint64 `json:"format_version"`
	Seq           uint64 `json:"seq"`
	Epoch         string `json:"epoch"`
	ID            string `json:"id"`
	// Base64 preserves the original payload bytes, including surrounding whitespace.
	Payload []byte `json:"payload"`
}

func validPayload(p []byte) bool {
	if len(p) == 0 || len(p) > payloadLimit {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var object map[string]json.RawMessage
	if err := dec.Decode(&object); err != nil || object == nil {
		return false
	}
	var extra json.RawMessage
	return dec.Decode(&extra) == io.EOF
}
func validEvent(epoch, id string, p []byte) bool {
	if _, err := config.ParseID(epoch); err != nil {
		return false
	}
	if _, err := config.ParseID(id); err != nil {
		return false
	}
	return validPayload(p)
}
func encodeFrame(r Record) ([]byte, error) {
	body, err := json.Marshal(envelope{1, r.Seq, r.Epoch, r.ID, []byte(r.Payload)})
	if err != nil {
		return nil, err
	}
	if len(body) > frameLimit {
		return nil, ErrInvalid
	}
	frame := make([]byte, 4+len(body)+sha256.Size)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	sum := sha256.Sum256(body)
	copy(frame[4+len(body):], sum[:])
	return frame, nil
}

// The outer envelope is deliberately strict, including duplicate JSON key
// rejection. Inner JSON objects are opaque validated bytes, not normalized data.
func decodeFrame(body []byte) (Record, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return Record{}, ErrRecoveryRequired
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return Record{}, ErrRecoveryRequired
		}
		name, ok := token.(string)
		if !ok {
			return Record{}, ErrRecoveryRequired
		}
		switch name {
		case "format_version", "seq", "epoch", "id", "payload":
		default:
			return Record{}, ErrRecoveryRequired
		}
		if _, exists := fields[name]; exists {
			return Record{}, ErrRecoveryRequired
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return Record{}, ErrRecoveryRequired
		}
		fields[name] = raw
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') || len(fields) != 5 {
		return Record{}, ErrRecoveryRequired
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Record{}, ErrRecoveryRequired
	}
	var e envelope
	if json.Unmarshal(fields["format_version"], &e.FormatVersion) != nil || e.FormatVersion != 1 || json.Unmarshal(fields["seq"], &e.Seq) != nil || json.Unmarshal(fields["epoch"], &e.Epoch) != nil || json.Unmarshal(fields["id"], &e.ID) != nil || json.Unmarshal(fields["payload"], &e.Payload) != nil || !validEvent(e.Epoch, e.ID, e.Payload) {
		return Record{}, ErrRecoveryRequired
	}
	return Record{e.Seq, e.Epoch, e.ID, json.RawMessage(e.Payload)}, nil
}
func privateFile(root *os.Root, name string, flags int) (*os.File, error) {
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return nil, ErrUnsafe
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	f, err := root.OpenFile(name, flags|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EISDIR) {
			return nil, ErrUnsafe
		}
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		f.Close()
		return nil, ErrUnsafe
	}
	return f, nil
}
func syncDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// OpenJournal requires an existing 0700 directory. No damaged or partial frame
// is repaired or discarded. The advisory lock is held until Close.
func OpenJournal(privateExistingDir string) (*Journal, error) {
	info, err := os.Lstat(privateExistingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, ErrUnsafe
	}
	root, err := os.OpenRoot(privateExistingDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, ErrUnsafe
	}
	j := &Journal{root: root, index: map[eventKey]int{}, records: []Record{}}
	success := false
	defer func() {
		if !success {
			j.Close()
		}
	}()
	j.lock, err = privateFile(root, lockName, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(j.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	// O_EXCL detects first creation; O_NOFOLLOW prevents symlink substitution.
	file, err := privateFile(root, dataName, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_EXCL)
	if errors.Is(err, os.ErrExist) {
		file, err = privateFile(root, dataName, os.O_RDWR|os.O_APPEND)
	}
	if err != nil {
		return nil, err
	}
	j.file = file

	for {
		var header [4]byte
		n, err := io.ReadFull(file, header[:])
		if err == io.EOF && n == 0 {
			break
		}
		if err != nil {
			return nil, ErrRecoveryRequired
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > frameLimit {
			return nil, ErrRecoveryRequired
		}
		data := make([]byte, int(size)+sha256.Size)
		if _, err = io.ReadFull(file, data); err != nil {
			return nil, ErrRecoveryRequired
		}
		sum := sha256.Sum256(data[:size])
		if !bytes.Equal(sum[:], data[size:]) {
			return nil, ErrRecoveryRequired
		}
		r, err := decodeFrame(data[:size])
		if err != nil {
			return nil, ErrRecoveryRequired
		}
		if r.Seq != uint64(len(j.records))+1 {
			return nil, ErrRecoveryRequired
		}
		key := eventKey{r.Epoch, r.ID}
		if _, exists := j.index[key]; exists {
			return nil, ErrRecoveryRequired
		}
		j.index[key] = len(j.records)
		j.records = append(j.records, r)
	}
	// Re-sync even on reopen: a complete frame from an uncertain append may
	// still only be in page cache. No deduplication acknowledgement before this.
	if err = file.Sync(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	if err = syncDirectory(root); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIO, err)
	}
	success = true
	return j, nil
}

// Append confirms only after one complete frame write and Sync. Any ambiguous
// write poisons this instance: do not retry until a full reopen scan succeeds.
func (j *Journal) Append(epoch, eventID string, payload json.RawMessage) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return 0, ErrClosed
	}
	if j.poisoned {
		return 0, ErrUncertain
	}
	if !validEvent(epoch, eventID, payload) {
		return 0, ErrInvalid
	}
	key := eventKey{epoch, eventID}
	if at, exists := j.index[key]; exists {
		r := j.records[at]
		if !bytes.Equal(r.Payload, payload) {
			return 0, ErrConflict
		}
		return r.Seq, nil
	}
	seq := uint64(len(j.records)) + 1
	r := Record{seq, epoch, eventID, append(json.RawMessage(nil), payload...)}
	frame, err := encodeFrame(r)
	if err != nil {
		return 0, err
	}
	n, err := j.file.Write(frame)
	if err != nil || n != len(frame) {
		j.poisoned = true
		return 0, ErrUncertain
	}
	if err = j.file.Sync(); err != nil {
		j.poisoned = true
		return 0, ErrUncertain
	}
	j.index[key] = len(j.records)
	j.records = append(j.records, r)
	return seq, nil
}

// Replay returns only this instance's confirmed records, with copied payloads.
func (j *Journal) Replay() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	records := make([]Record, len(j.records))
	for i, r := range j.records {
		records[i] = r
		records[i].Payload = append(json.RawMessage(nil), r.Payload...)
	}
	return records
}
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	var errs []error
	if j.file != nil {
		if err := j.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if j.lock != nil {
		if err := syscall.Flock(int(j.lock.Fd()), syscall.LOCK_UN); err != nil {
			errs = append(errs, err)
		}
		if err := j.lock.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if j.root != nil {
		if err := j.root.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrIO, errors.Join(errs...))
	}
	return nil
}
