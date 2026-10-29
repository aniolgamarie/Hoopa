//go:build linux

package config

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These are opaque Store records. No control import or new fault seam is used;
// Journal.Get's decoded projection of ErrUncertain is not injected here.
func TestOperationJournalPoisonObservationAndReopen(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	id := operationKey("poison-observation")
	first := []byte(`{"observation":"before"}`)
	second := []byte(`{"observation":"after","synthetic_error":{"code":"FIXTURE"},"warnings":["kept"]}`)
	oldSHA, err := s.CompareAndSwapOperation(id, "", first)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("operation parent sync fixture failure")
	hits := 0
	s.fault = func(point string) error {
		if point == "operation.parent-sync" {
			hits++
			return injected
		}
		return nil
	}
	sha, err := s.CompareAndSwapOperation(id, oldSHA, second)
	if sha != "" || !errors.Is(err, ErrUncertain) || !errors.Is(err, injected) || hits != 1 {
		t.Fatal("did not reach actual post-rename uncertainty", sha, err, hits)
	}
	before := tree(t, p.State)
	touches := 0
	s.fault = func(point string) error {
		touches++
		t.Errorf("observation/poison reuse called write or sync: %s", point)
		return injected
	}
	raw, observedSHA, err := s.ReadOperationFile(id)
	if !errors.Is(err, ErrUncertain) || !bytes.Equal(raw, second) || observedSHA != digest(second) {
		t.Fatal("poison observation lost bytes/hash or hid uncertainty", err)
	}
	raw[0] = '['
	again, gotSHA, err := s.ReadOperationFile(id)
	if !errors.Is(err, ErrUncertain) || !bytes.Equal(again, second) || gotSHA != observedSHA {
		t.Fatal("observation alias changed authority", err)
	}
	ids, err := s.ListOperationIDs()
	if err != nil || !reflect.DeepEqual(ids, []ID{id}) {
		t.Fatal("enumeration changed visible names", err)
	}
	if _, err = s.CompareAndSwapOperation(id, observedSHA, first); !errors.Is(err, ErrUncertain) {
		t.Fatal("poisoned writer reused", err)
	}
	if _, err = s.CompareAndSwapOperation(operationKey("must-not-appear"), "", first); !errors.Is(err, ErrUncertain) {
		t.Fatal("poisoned writer admitted new ID", err)
	}
	if touches != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("poison observation/rejected writes mutated state")
	}
	// A separately opened reader reports current bytes only. Its nil error does
	// not establish the writer's missing durability barrier or grant effects.
	reader, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	reader.fault = func(point string) error { t.Errorf("readonly sync/write: %s", point); return injected }
	got, h, err := reader.ReadOperationFile(id)
	if err != nil || h != observedSHA || !bytes.Equal(got, second) {
		t.Fatal("readonly current observation unavailable", err)
	}
	if _, err = reader.CompareAndSwapOperation(operationKey("reader-new"), "", first); !errors.Is(err, ErrReadOnly) {
		t.Fatal("reader created record", err)
	}
	reader.Close()
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("reader repaired/synced storage")
	}
	s.fault = nil
	s.Close()
	reopened := openWriter(t, p)
	got, h, err = reopened.ReadOperationFile(id)
	if err != nil || h != observedSHA || !bytes.Equal(got, second) {
		t.Fatal("explicit writer reopen did not retain observed post-rename record", err)
	}
}

func TestOperationJournalOpaqueRecordSizeZeroWrite(t *testing.T) {
	if MaxOperationBytes != 1<<20 {
		t.Fatal("record byte budget changed")
	}
	p := storePaths(t)
	s := openWriter(t, p)
	id := operationKey("exact-size")
	const prefix = `{"payload":"`
	const suffix = `"}`
	exact := []byte(prefix + strings.Repeat("x", MaxOperationBytes-len(prefix)-len(suffix)) + suffix)
	sha, err := s.CompareAndSwapOperation(id, "", exact)
	if err != nil {
		t.Fatal("exact opaque record bound rejected", err)
	}
	before := tree(t, p.State)
	calls := 0
	s.fault = func(string) error { calls++; return errors.New("unexpected publication") }
	over := []byte(prefix + strings.Repeat("x", MaxOperationBytes+1-len(prefix)-len(suffix)) + suffix)
	for _, key := range []ID{id, operationKey("new-oversize")} {
		expected := ""
		if key == id {
			expected = sha
		}
		if _, err = s.CompareAndSwapOperation(key, expected, over); !errors.Is(err, ErrIntegrity) {
			t.Fatal("oversized record reached publication", err)
		}
	}
	raw, h, err := s.ReadOperationFile(id)
	if err != nil || h != sha || !bytes.Equal(raw, exact) || calls != 0 {
		t.Fatal("size rejection modified old record", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("size rejection changed storage")
	}
}
