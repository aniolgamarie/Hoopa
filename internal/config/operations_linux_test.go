//go:build linux

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func operationKey(v string) ID { return ID("op-" + digest([]byte(v))) }

func TestOperationFileMissingPersistentDirectoryIsNotNewHistory(t *testing.T) {
	for _, name := range []string{"operations", "runs", "bundles", "credentials"} {
		t.Run(name, func(t *testing.T) {
			p := storePaths(t)
			w := openWriter(t, p)
			w.Close()
			path := filepath.Join(p.State, name)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			before := tree(t, p.State)
			if reopened, err := OpenStore(p, true); !errors.Is(err, ErrReadUnavailable) {
				if reopened != nil {
					reopened.Close()
				}
				t.Fatal("missing persistent directory was accepted", err)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("missing authority was recreated")
			}
		})
	}
}

func TestOperationFileCASAndReadonly(t *testing.T) {
	p := storePaths(t)
	w := openWriter(t, p)
	id := operationKey("原始/请求")
	first, second := []byte(`{"step":1}`), []byte(`{"step":2}`)
	sha, err := w.CompareAndSwapOperation(id, "", first)
	if err != nil || sha != digest(first) {
		t.Fatal(sha, err)
	}
	before := tree(t, p.State)
	if _, err = w.CompareAndSwapOperation(id, "", second); !errors.Is(err, ErrOperationConflict) {
		t.Fatal(err)
	}
	if _, err = w.CompareAndSwapOperation(id, sha, first); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("CAS reject or identical bytes changed state")
	}
	sha, err = w.CompareAndSwapOperation(id, sha, second)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before = tree(t, p.State)
	raw, gotSHA, err := r.ReadOperationFile(id)
	if err != nil || gotSHA != sha || !bytes.Equal(raw, second) {
		t.Fatal("reopen lost snapshot", err)
	}
	ids, err := r.ListOperationIDs()
	if err != nil || !reflect.DeepEqual(ids, []ID{id}) {
		t.Fatal(ids, err)
	}
	if _, err = r.CompareAndSwapOperation(id, sha, first); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("readonly changed state")
	}
}

func TestOperationFilePublicationBoundaries(t *testing.T) {
	for _, point := range []string{"operation.file.write", "operation.file.sync", "operation.file.closed", "operation.rename", "operation.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			w := openWriter(t, p)
			id := operationKey("failure")
			first, second := []byte(`{"step":1}`), []byte(`{"step":2}`)
			sha, err := w.CompareAndSwapOperation(id, "", first)
			if err != nil {
				t.Fatal(err)
			}
			var calls []string
			w.fault = func(at string) error {
				calls = append(calls, at)
				if at == point {
					return errors.New("injected boundary")
				}
				return nil
			}
			_, err = w.CompareAndSwapOperation(id, sha, second)
			if err == nil {
				t.Fatal("injection not reached")
			}
			want := first
			if point == "operation.parent-sync" {
				want = second
				if !errors.Is(err, ErrUncertain) {
					t.Fatal("published state falsely definite", err)
				}
				if _, err = w.CompareAndSwapOperation(id, digest(second), first); !errors.Is(err, ErrUncertain) {
					t.Fatal("poison reused", err)
				}
			} else if errors.Is(err, ErrUncertain) {
				t.Fatal("prepublication reported uncertain")
			}
			raw, _, err := w.ReadOperationFile(id)
			uncertain := point == "operation.parent-sync"
			if (!uncertain && err != nil) || (uncertain && !errors.Is(err, ErrUncertain)) || !bytes.Equal(raw, want) {
				t.Fatal("wrong visible authority", err)
			}
			if len(calls) == 0 || calls[0] != "operation.file.write" {
				t.Fatal(calls)
			}
			w.fault = nil
			w.Close()
			w = openWriter(t, p)
			raw, gotSHA, err := w.ReadOperationFile(id)
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatal("reopen changed authority", err)
			}
			if _, err = w.CompareAndSwapOperation(id, gotSHA, second); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOperationFileCapacityRetainsOldIDs(t *testing.T) {
	p := storePaths(t)
	w := openWriter(t, p)
	// Populate the actual private directory with bounded retained records. There
	// is no injected capacity return or reduced production limit.
	data := []byte(`{"retained":true}`)
	for i := 0; i < MaxOperationFiles; i++ {
		id := operationKey(fmt.Sprint(i))
		if err := os.WriteFile(filepath.Join(p.State, "operations", string(id)+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, p.State)
	if _, err := w.CompareAndSwapOperation(operationKey("new"), "", data); !errors.Is(err, ErrOperationCapacity) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("full rejection mutated state")
	}
	id := operationKey("0")
	if raw, sha, err := w.ReadOperationFile(id); err != nil || !bytes.Equal(raw, data) || sha != digest(data) {
		t.Fatal(err)
	}
	if _, err := w.CompareAndSwapOperation(id, digest(data), []byte(`{"retained":"updated"}`)); err != nil {
		t.Fatal("capacity prevented existing progress", err)
	}
}

func TestOperationFileOrphansCorruptionAndBounds(t *testing.T) {
	p := storePaths(t)
	w := openWriter(t, p)
	id := operationKey("one")
	data := []byte(`{"step":1}`)
	sha, err := w.CompareAndSwapOperation(id, "", data)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(p.State, "operations", ".operation-"+strings.Repeat("a", 48))
	if err = os.WriteFile(tmp, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.State)
	ids, err := w.ListOperationIDs()
	if err != nil || !reflect.DeepEqual(ids, []ID{id}) {
		t.Fatal(ids, err)
	}
	if _, _, err = w.ReadOperationFile(id); err != nil {
		t.Fatal(err)
	}
	if _, err = w.CompareAndSwapOperation(operationKey("next"), "", data); !errors.Is(err, ErrOperationStaging) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("orphan was adopted or cleaned")
	}
	if err = os.Remove(tmp); err != nil {
		t.Fatal(err)
	} // Test fixture maintenance only.
	for _, bad := range []ID{"", "../outside", ID("op-" + strings.Repeat("A", 64)), ID("op-" + strings.Repeat("a", 63))} {
		if _, _, err = w.ReadOperationFile(bad); err == nil {
			t.Fatal("unsafe key")
		}
	}
	for _, bad := range [][]byte{nil, []byte("not-json"), bytes.Repeat([]byte(" "), MaxOperationBytes+1)} {
		if _, err = w.CompareAndSwapOperation(id, sha, bad); !errors.Is(err, ErrIntegrity) {
			t.Fatal(err)
		}
	}
	file := filepath.Join(p.State, "operations", string(id)+".json")
	if err = os.Chmod(file, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = w.ReadOperationFile(id); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if _, err = w.CompareAndSwapOperation(id, sha, data); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}
