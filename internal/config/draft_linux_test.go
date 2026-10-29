//go:build linux

package config

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestInitPrivateInputRestrictiveUmask(t *testing.T) {
	const marker = "HOOPA_T054_UMASK_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInitPrivateInputRestrictiveUmask$", "-test.v")
		cmd.Env = append(os.Environ(), marker+"=1")
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask check: %v %s", err, raw)
		}
		return
	}
	p := storePaths(t)
	s := openWriter(t, p)
	m := intentFixture(t)
	raw := intentEncode(t, m)
	oldSHA, err := s.PublishDraft(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	id, err := s.ReadLaunchHistoryID()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(p.State, "credentials", launchHistoryMarker)); err != nil {
		t.Fatal(err)
	}
	m["default_target"] = map[string]any{"kind": "reject"}
	next := intentEncode(t, m)
	prior := syscall.Umask(0777)
	_, err = s.PublishDraft(next)
	syscall.Umask(prior)
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrUncertain) {
		t.Fatal("unreadable draft reported prepared", err)
	}
	s.Close()
	s = openWriter(t, p)
	if _, sha, err := s.ReadDraft(); err != nil || sha != oldSHA {
		t.Fatal("umask refusal replaced old draft", err)
	}
	prior = syscall.Umask(0777)
	err = s.InitializeLaunchHistory()
	syscall.Umask(prior)
	if !errors.Is(err, ErrUnsafePath) || !errors.Is(err, ErrUncertain) {
		t.Fatal("unreadable marker reported prepared", err)
	}
	if _, err = os.Lstat(filepath.Join(p.State, "credentials", launchHistoryMarker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unreadable marker published", err)
	}
	s.Close()
	s = openWriter(t, p)
	if err = s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ReadLaunchHistoryID(); err != nil || got != id {
		t.Fatal("retry replaced identity", err)
	}
}

func TestDraftCanonicalReplacementAndReadOnly(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	active, _ := s.ReadActive()
	before := tree(t, p.State)
	if _, _, err := s.ReadDraft(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read wrote")
	}
	m := intentFixture(t)
	m["apps"] = []any{map[string]any{"id": "app", "mode": "direct", "metering": false, "bypass": []any{"a&b"}}}
	raw := intentEncode(t, m)
	canonical, _ := CanonicalJSON(raw)
	sha, err := s.PublishDraft(raw)
	if err != nil || sha != digest(canonical) {
		t.Fatal(sha, err)
	}
	b, got, err := s.ReadDraft()
	if err != nil || got != sha || !bytes.Equal(b, canonical) {
		t.Fatal("canonical roundtrip", err)
	}
	b[0] = '!'
	b, _, err = s.ReadDraft()
	if err != nil || !bytes.Equal(b, canonical) {
		t.Fatal("read shares bytes", err)
	}
	before = tree(t, p.State)
	if _, err = s.PublishDraft(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("idempotent changed bytes")
	}
	m["default_target"] = map[string]any{"kind": "reject"}
	next := intentEncode(t, m)
	if _, err = s.PublishDraft(next); err != nil {
		t.Fatal(err)
	}
	settingsActiveSame(t, s, active)
	s.Close()
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before = tree(t, p.State)
	if _, _, err = r.ReadDraft(); err != nil {
		t.Fatal(err)
	}
	if _, err = r.PublishDraft(raw); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("readonly changed tree")
	}
}

func TestDraftInvalidAndCorruptNotRepaired(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	before := tree(t, p.State)
	for _, raw := range [][]byte{nil, []byte(`{}`), []byte(`{"schema_version":1,"schema_version":1}`)} {
		if _, err := s.PublishDraft(raw); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("invalid wrote")
	}
	raw := intentEncode(t, intentFixture(t))
	if _, err := s.PublishDraft(raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.State, draftName), []byte(`{"schema_version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	before = tree(t, p.State)
	if _, _, err := s.ReadDraft(); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if _, err := s.PublishDraft(raw); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("corrupt replaced")
	}
}

func TestDraftFailureKeepsOldOrCompleteNew(t *testing.T) {
	for _, point := range []string{"draft.file.write", "draft.file.sync", "draft.file.closed", "draft.rename", "draft.parent-sync", "draft.existing-file-sync", "draft.existing-parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			m := intentFixture(t)
			old := intentEncode(t, m)
			oldSHA, err := s.PublishDraft(old)
			if err != nil {
				t.Fatal(err)
			}
			m["default_target"] = map[string]any{"kind": "reject"}
			next := intentEncode(t, m)
			if point == "draft.existing-file-sync" || point == "draft.existing-parent-sync" {
				next = old
			}
			injected := errors.New("draft boundary")
			s.fault = func(at string) error {
				if at == point {
					return injected
				}
				return nil
			}
			if _, err = s.PublishDraft(next); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			if point != "draft.rename" && !errors.Is(err, ErrUncertain) {
				t.Fatal("uncertainty lost", err)
			}
			s.Close()
			s = openWriter(t, p)
			_, sha, err := s.ReadDraft()
			if err != nil {
				t.Fatal(err)
			}
			if point == "draft.parent-sync" {
				canonical, _ := CanonicalJSON(next)
				if sha != digest(canonical) {
					t.Fatal("new complete draft lost")
				}
			} else if sha != oldSHA {
				t.Fatal("old draft damaged")
			}
			if _, err = s.PublishDraft(next); err != nil {
				t.Fatal(err)
			}
		})
	}
}
