package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func evidenceSandbox(t *testing.T) *Sandbox {
	t.Helper()
	s, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.RemoveFiles(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func testBinary() []BinaryRecord {
	return []BinaryRecord{{Role: "calibrator", Path: "/synthetic/calibrator", ReportedVersion: "test-v1", SHA256: strings.Repeat("a", 64)}}
}

func TestEvidencePrivateOrderedAndExclusive(t *testing.T) {
	s := evidenceSandbox(t)
	e, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	for _, name := range []string{"manifest.json", "events.jsonl"} {
		info, err := os.Stat(filepath.Join(s.Path(), name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private %s: %v %v", name, info, err)
		}
	}
	var manifest Manifest
	data, err := os.ReadFile(filepath.Join(s.Path(), "manifest.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatal("invalid manifest", err)
	}
	if manifest.RunID != s.RunID() || manifest.Status != Unverified || manifest.Binaries[0].SourceVerified {
		t.Fatal("manifest fabricated verification")
	}
	for _, status := range []EvidenceStatus{Passed, Failed, Unverified} {
		if err := e.Append("G01", "assertion", status, "recorded", []string{"result.md"}); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(filepath.Join(s.Path(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var previous int64
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event EvidenceEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Seq != uint64(i+1) || event.ElapsedMS < previous || !strings.HasSuffix(event.ObservedAt, "Z") {
			t.Fatalf("event ordering/time: %+v", event)
		}
		previous = event.ElapsedMS
	}
	if _, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64)); !errors.Is(err, ErrEvidenceInit) {
		t.Fatalf("overwrote run: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.Path(), "events.jsonl"))
	if string(after) != string(data) {
		t.Fatal("duplicate initialization changed events")
	}
}

func TestEvidenceRejectsUnsafeFieldsBeforeWrite(t *testing.T) {
	s := evidenceSandbox(t)
	bad := testBinary()
	bad[0].ReportedVersion = "v1\nPRIVATE_ERROR"
	if _, err := NewEvidence(s, bad, strings.Repeat("b", 64)); !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Path(), "manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid manifest created state")
	}
	e, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	for _, tc := range []struct {
		caseID, kind string
		status       EvidenceStatus
		reason       string
		refs         []string
	}{
		{"F34", "fault", Failed, "injected", nil}, {"F11", "anything", Failed, "injected", nil},
		{"F11", "fault", "unknown", "injected", nil}, {"F11", "fault", Failed, "PRIVATE ERROR", nil},
		{"F11", "fault", Failed, "injected", []string{"../outside"}}, {"F11", "fault", Failed, "injected", []string{"/outside"}},
		{"F11", "fault", Failed, "injected", []string{"x\nPRIVATE"}},
	} {
		if err := e.Append(tc.caseID, tc.kind, tc.status, tc.reason, tc.refs); !errors.Is(err, ErrEvidenceInvalid) {
			t.Fatalf("unsafe event accepted: %v", err)
		}
	}
	if e.seq != 0 {
		t.Fatal("invalid events advanced sequence")
	}
	info, _ := os.Stat(filepath.Join(s.Path(), "events.jsonl"))
	if info.Size() != 0 {
		t.Fatal("invalid event wrote bytes")
	}
	if err := e.Append("CALIBRATION", "sample", Unverified, "input_missing", nil); err != nil {
		t.Fatal(err)
	}
}

type failingEvidenceFile struct {
	writes, syncs int
	failSync      bool
}

func (f *failingEvidenceFile) Write(p []byte) (int, error) {
	f.writes++
	if f.failSync {
		return len(p), nil
	}
	return len(p) - 1, io.ErrShortWrite
}
func (f *failingEvidenceFile) Sync() error {
	f.syncs++
	return errors.New("PRIVATE_ERROR_MUST_NOT_BE_RECORDED")
}
func (*failingEvidenceFile) Close() error { return nil }

func TestEvidenceWriteOrSyncFailurePoisonsWriter(t *testing.T) {
	for _, failSync := range []bool{false, true} {
		e, err := NewEvidence(evidenceSandbox(t), testBinary(), strings.Repeat("b", 64))
		if err != nil {
			t.Fatal(err)
		}
		_ = e.file.Close()
		f := &failingEvidenceFile{failSync: failSync}
		e.file = f
		err = e.Append("F11", "fault", Failed, "injected", nil)
		if !errors.Is(err, ErrEvidenceWrite) || strings.Contains(err.Error(), "PRIVATE_ERROR") || e.seq != 0 {
			t.Fatalf("failure handling: %v seq=%d", err, e.seq)
		}
		if err := e.Append("F11", "fault", Failed, "injected", nil); !errors.Is(err, ErrEvidenceWrite) || f.writes != 1 {
			t.Fatalf("blind retry: %v writes=%d", err, f.writes)
		}
		_ = e.Close()
	}
}

func TestEvidenceExistingEventsAndFailedInitializationAreNotReused(t *testing.T) {
	s := evidenceSandbox(t)
	if err := os.WriteFile(filepath.Join(s.Path(), "events.jsonl"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64)); !errors.Is(err, ErrEvidenceInit) {
		t.Fatal(err)
	}
	if _, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64)); !errors.Is(err, ErrEvidenceInit) {
		t.Fatal("partial initialization reused", err)
	}
	data, _ := os.ReadFile(filepath.Join(s.Path(), "events.jsonl"))
	if string(data) != "keep" {
		t.Fatal("existing events overwritten")
	}
}

func TestEvidenceDirectorySyncFailureLeavesIncompleteRun(t *testing.T) {
	s := evidenceSandbox(t)
	called := false
	e, err := newEvidence(s, testBinary(), strings.Repeat("b", 64), func(root *os.Root) error {
		called = true
		for _, name := range []string{"manifest.json", "events.jsonl"} {
			if _, err := root.Stat(name); err != nil {
				t.Fatal("directory sync attempted before files existed", err)
			}
		}
		return io.ErrUnexpectedEOF
	})
	if !called || e != nil || !errors.Is(err, ErrEvidenceInit) {
		t.Fatalf("directory sync failure: %v %v", e, err)
	}
	if _, err := NewEvidence(s, testBinary(), strings.Repeat("b", 64)); !errors.Is(err, ErrEvidenceInit) {
		t.Fatal("incomplete run reused", err)
	}
}
