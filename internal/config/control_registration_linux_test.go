//go:build linux

package config

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestControlRegistrationWriterRead(t *testing.T) {
	p, s, intent, nonce := runStoreFixture(t)
	birth, bsha, _ := runStoreBirth(t, s, intent, nonce)
	reg := runStoreRegistration(intent, birth, bsha)
	sha, err := s.PublishRunRegistration(reg)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.State)
	hits := 0
	s.fault = func(string) error { hits++; return errors.New("unexpected write") }
	got, digest, err := s.ReadControlRegistration(intent.RunID, reg.OperationID)
	if err != nil || got != reg || digest != sha || hits != 0 {
		t.Fatal("writer read changed binding or wrote", err, hits)
	}
	if _, _, err = s.ReadControlRegistration(intent.RunID, "missing-operation"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing record authorized", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("control read changed bytes/mtime")
	}
	s.fault = nil
	// Poison the writer through a real post-publication durability error.
	other := reg
	other.OperationID = "uncertain-operation"
	s.fault = func(point string) error {
		if point == "registration.parent-sync" {
			return errors.New("injected directory sync")
		}
		return nil
	}
	if _, err = s.PublishRunRegistration(other); !errors.Is(err, ErrUncertain) {
		t.Fatal("did not reach uncertain boundary", err)
	}
	s.fault = nil
	before = tree(t, p.State)
	if _, _, err = s.ReadControlRegistration(intent.RunID, reg.OperationID); !errors.Is(err, ErrUncertain) {
		t.Fatal("poisoned writer granted control", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("poisoned read wrote")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReadControlRegistration(intent.RunID, reg.OperationID); !errors.Is(err, ErrClosed) {
		t.Fatal("closed writer granted control", err)
	}
	reader, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	before = tree(t, p.State)
	if _, _, err = reader.ReadControlRegistration(intent.RunID, reg.OperationID); !errors.Is(err, ErrReadOnly) {
		t.Fatal("reader granted control", err)
	}
	if g, h, e := reader.ReadRunRegistration(intent.RunID, reg.OperationID); e != nil || g != reg || h != sha {
		t.Fatal("ordinary readonly observation unavailable", e)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("reader changed bytes/mtime")
	}
}
