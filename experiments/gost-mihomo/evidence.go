package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"hoopa/internal/config"
)

type EvidenceStatus string

const (
	Passed     EvidenceStatus = "passed"
	Failed     EvidenceStatus = "failed"
	Unverified EvidenceStatus = "unverified"
)

var (
	ErrEvidenceInvalid = errors.New("invalid experiment evidence fields")
	ErrEvidenceInit    = errors.New("experiment evidence initialization incomplete; inspect original sandbox")
	ErrEvidenceWrite   = errors.New("experiment evidence write uncertain; writer cannot be retried")
	caseForm           = regexp.MustCompile(`^(G0[1-7]|F(0[1-9]|[12][0-9]|3[0-3])|C0[12]|CALIBRATION|REAL_AGENT|SECURITY)$`)
	refForm            = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)
	shaForm            = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// BinaryRecord records supplied observations, not a claim that a source was verified.
// Version is the extracted version field, never raw command output or arguments.
type BinaryRecord struct {
	Role            string  `json:"role"`
	Path            string  `json:"path"`
	ReportedVersion string  `json:"reported_version"`
	SHA256          string  `json:"sha256"`
	SourceRevision  *string `json:"source_revision"`
	SourceVerified  bool    `json:"source_verified"`
}

type Manifest struct {
	SchemaVersion int            `json:"schema_version"`
	RunID         string         `json:"run_id"`
	ObservedAt    string         `json:"observed_at"`
	OS            string         `json:"os"`
	Arch          string         `json:"arch"`
	Binaries      []BinaryRecord `json:"binaries"`
	ConfigSHA256  string         `json:"config_sha256"`
	Status        EvidenceStatus `json:"status"`
}

type EvidenceEvent struct {
	Seq        uint64         `json:"seq"`
	ObservedAt string         `json:"observed_at"`
	ElapsedMS  int64          `json:"elapsed_ms"`
	CaseID     string         `json:"case_id"`
	Kind       string         `json:"kind"`
	Status     EvidenceStatus `json:"status"`
	ReasonCode string         `json:"reason_code"`
	Refs       []string       `json:"refs"`
}

type evidenceFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// Evidence is a single append-only P0 writer. It is not a recovery journal and
// does not promise source replay or atomicity across manifest and event files.
type Evidence struct {
	mu             sync.Mutex
	file           evidenceFile
	started        time.Time
	seq            uint64
	failed, closed bool
}

func field(s string, limit int) bool {
	if s == "" || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func validBinaries(binaries []BinaryRecord) bool {
	if len(binaries) == 0 {
		return false
	}
	for _, b := range binaries {
		switch b.Role {
		case "mihomo", "gost", "agent", "calibrator":
		default:
			return false
		}
		if !filepath.IsAbs(b.Path) || !field(b.Path, 4096) || !field(b.ReportedVersion, 256) || strings.EqualFold(b.ReportedVersion, "latest") || !shaForm.MatchString(b.SHA256) {
			return false
		}
		if b.SourceVerified && b.SourceRevision == nil {
			return false
		}
		if b.SourceRevision != nil {
			if _, err := config.ParseID(*b.SourceRevision); err != nil {
				return false
			}
		}
	}
	return true
}

func writeSynced(f evidenceFile, data []byte) error {
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return f.Sync()
}

// NewEvidence exclusively creates files in the original sandbox. Partial files
// remain on failure as evidence of incomplete initialization, never reused.
func NewEvidence(s *Sandbox, binaries []BinaryRecord, configSHA256 string) (*Evidence, error) {
	return newEvidence(s, binaries, configSHA256, syncEvidenceDirectory)
}

func syncEvidenceDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func newEvidence(s *Sandbox, binaries []BinaryRecord, configSHA256 string, syncDir func(*os.Root) error) (*Evidence, error) {
	if s == nil || !validBinaries(binaries) || !shaForm.MatchString(configSHA256) {
		return nil, ErrEvidenceInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removed {
		return nil, ErrEvidenceInit
	}
	now := time.Now()
	manifest := Manifest{1, s.RunID(), now.UTC().Format(time.RFC3339Nano), runtime.GOOS, runtime.GOARCH, binaries, configSHA256, Unverified}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, ErrEvidenceInvalid
	}
	f, err := s.root.OpenFile("manifest.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, ErrEvidenceInit
	}
	writeErr := writeSynced(f, append(data, '\n'))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return nil, ErrEvidenceInit
	}
	events, err := s.root.OpenFile("events.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return nil, ErrEvidenceInit
	}
	if err := syncDir(s.root); err != nil {
		_ = events.Close()
		return nil, ErrEvidenceInit
	}
	return &Evidence{file: events, started: now}, nil
}

func validEvent(caseID, kind string, status EvidenceStatus, reason string, refs []string) bool {
	if !caseForm.MatchString(caseID) {
		return false
	}
	switch kind {
	case "startup", "sample", "probe", "fault", "shutdown", "restart", "takeover", "recovery", "assertion":
	default:
		return false
	}
	if status != Passed && status != Failed && status != Unverified {
		return false
	}
	// The caller supplies a non-secret ASCII reason token, never a raw error.
	if _, err := config.ParseID(reason); err != nil {
		return false
	}
	for _, ref := range refs {
		if !fs.ValidPath(ref) || !refForm.MatchString(ref) {
			return false
		}
	}
	return true
}

func (e *Evidence) Append(caseID, kind string, status EvidenceStatus, reasonCode string, refs []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failed {
		return ErrEvidenceWrite
	}
	if e.closed {
		return os.ErrClosed
	}
	if !validEvent(caseID, kind, status, reasonCode, refs) || e.seq == ^uint64(0) {
		return ErrEvidenceInvalid
	}
	now := time.Now()
	elapsed, err := config.DurationBetween(e.started, now)
	if err != nil {
		return ErrEvidenceInvalid
	}
	event := EvidenceEvent{e.seq + 1, now.UTC().Format(time.RFC3339Nano), int64(elapsed), caseID, kind, status, reasonCode, append([]string{}, refs...)}
	data, err := json.Marshal(event)
	if err != nil {
		return ErrEvidenceInvalid
	}
	if err := writeSynced(e.file, append(data, '\n')); err != nil {
		e.failed = true // The tail may exist; never retry or advance the sequence.
		return ErrEvidenceWrite
	}
	e.seq = event.Seq
	return nil
}

func (e *Evidence) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	if err := e.file.Close(); err != nil {
		e.failed = true
		return ErrEvidenceWrite
	}
	return nil
}
