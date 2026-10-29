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

func maintenanceTemp(n int) string { return fmt.Sprintf(".operation-%048x", n) }
func maintenancePut(t *testing.T, dir, name string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func maintenanceFixture(t *testing.T) (Paths, *Store, ID, string) {
	t.Helper()
	p := storePaths(t)
	s := openWriter(t, p)
	id := operationKey("maintenance-authority")
	sha, e := s.CompareAndSwapOperation(id, "", []byte(`{"authority":"原件\\u2028"}`))
	if e != nil {
		t.Fatal(e)
	}
	return p, s, id, sha
}
func maintenanceAuthority(t *testing.T, s *Store, p Paths, id ID, sha, metadata string) {
	t.Helper()
	raw, got, e := s.ReadOperationFile(id)
	if e != nil || got != sha || digest(raw) != sha {
		t.Fatal("authority bytes changed", e)
	}
	path := filepath.Join(p.State, "operations", string(id)+".json")
	if tree(t, filepath.Dir(path))[path] != metadata {
		t.Fatal("authority metadata changed")
	}
}
func TestOperationMaintenanceExplicitOriginals(t *testing.T) {
	p, s, id, sha := maintenanceFixture(t)
	dir := filepath.Join(p.State, "operations")
	meta := tree(t, dir)[filepath.Join(dir, string(id)+".json")]
	maintenancePut(t, dir, maintenanceTemp(1), []byte("unpublished non-JSON original"))
	maintenancePut(t, dir, maintenanceTemp(2), nil)
	before := tree(t, p.State)
	ids, e := s.ListOperationIDs()
	if e != nil || !reflect.DeepEqual(ids, []ID{id}) {
		t.Fatal(e)
	}
	if e := s.CheckOperationAdmission(); !errors.Is(e, ErrOperationStaging) {
		t.Fatal("staging not reported", e)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read/check removed staging")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenStore(p, true)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("OpenStore maintained staging automatically")
	}
	var points []string
	reopened.fault = func(at string) error { points = append(points, at); return nil }
	n, e := reopened.MaintainOperationStaging()
	reopened.fault = nil
	if e != nil || n != 2 || !reflect.DeepEqual(points, []string{"operation.maintenance.remove", "operation.maintenance.remove", "operation.maintenance.sync"}) {
		t.Fatal("explicit maintenance order", n, e, points)
	}
	maintenanceAuthority(t, reopened, p, id, sha, meta)
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 || entries[0].Name() != string(id)+".json" {
		t.Fatal("unexpected remaining namespace", e)
	}
	points = nil
	reopened.fault = func(at string) error { points = append(points, at); return nil }
	n, e = reopened.MaintainOperationStaging()
	reopened.fault = nil
	if e != nil || n != 0 || !reflect.DeepEqual(points, []string{"operation.maintenance.sync"}) {
		t.Fatal("empty maintenance did not sync", n, e, points)
	}
}
func TestOperationMaintenanceWriterQualification(t *testing.T) {
	for _, kind := range []string{"readonly", "closed", "poison"} {
		t.Run(kind, func(t *testing.T) {
			p, s, _, _ := maintenanceFixture(t)
			dir := filepath.Join(p.State, "operations")
			maintenancePut(t, dir, maintenanceTemp(1), []byte("x"))
			want := ErrReadOnly
			if kind == "readonly" {
				r, e := OpenStore(p, false)
				if e != nil {
					t.Fatal(e)
				}
				defer r.Close()
				s = r
			}
			if kind == "closed" {
				if e := s.Close(); e != nil {
					t.Fatal(e)
				}
				want = ErrClosed
			}
			if kind == "poison" {
				s.fault = func(at string) error {
					if at == "operation.maintenance.sync" {
						return errors.New("synthetic sync failure")
					}
					return nil
				}
				if _, e := s.MaintainOperationStaging(); !errors.Is(e, ErrUncertain) {
					t.Fatal(e)
				}
				s.fault = nil
				maintenancePut(t, dir, maintenanceTemp(2), []byte("not reusable"))
				want = ErrUncertain
			}
			before := tree(t, p.State)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			n, e := s.MaintainOperationStaging()
			s.fault = nil
			if n != 0 || !errors.Is(e, want) || hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("unqualified writer mutated", n, e, hits)
			}
		})
	}
}
func TestOperationMaintenancePrevalidateAll(t *testing.T) {
	for _, kind := range []string{"unknown", "temp-suffix", "temp-uppercase", "symlink", "hardlink", "mode", "authority-mode", "authority-oversize", "temporary-oversize", "directory"} {
		t.Run(kind, func(t *testing.T) {
			p, s, id, _ := maintenanceFixture(t)
			dir := filepath.Join(p.State, "operations")
			maintenancePut(t, dir, maintenanceTemp(1), []byte("keep until ALL validated"))
			path := filepath.Join(dir, maintenanceTemp(2))
			var e error
			switch kind {
			case "unknown":
				maintenancePut(t, dir, "foreign", []byte("unknown"))
			case "temp-suffix":
				maintenancePut(t, dir, maintenanceTemp(2)+".json", nil)
			case "temp-uppercase":
				maintenancePut(t, dir, ".operation-"+strings.Repeat("A", 48), nil)
			case "symlink":
				e = os.Symlink(maintenanceTemp(1), path)
			case "hardlink":
				e = os.Link(filepath.Join(dir, maintenanceTemp(1)), path)
			case "mode":
				maintenancePut(t, dir, maintenanceTemp(2), nil)
				e = os.Chmod(path, 0644)
			case "authority-mode":
				e = os.Chmod(filepath.Join(dir, string(id)+".json"), 0644)
			case "authority-oversize":
				e = os.Truncate(filepath.Join(dir, string(id)+".json"), MaxOperationBytes+1)
			case "temporary-oversize":
				maintenancePut(t, dir, maintenanceTemp(2), nil)
				e = os.Truncate(path, MaxOperationBytes+1)
			case "directory":
				e = os.Mkdir(path, 0700)
			}
			if e != nil {
				t.Fatal(e)
			}
			before := tree(t, p.State)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			n, e := s.MaintainOperationStaging()
			s.fault = nil
			if n != 0 || e == nil || errors.Is(e, ErrUncertain) || s.poisoned || hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("invalid inventory partially deleted", n, e, hits)
			}
		})
	}
}
func TestOperationMaintenanceFaultReconciliation(t *testing.T) {
	for _, kind := range []string{"before-first", "between", "sync-after-delete", "empty-sync"} {
		t.Run(kind, func(t *testing.T) {
			p, s, id, sha := maintenanceFixture(t)
			dir := filepath.Join(p.State, "operations")
			meta := tree(t, dir)[filepath.Join(dir, string(id)+".json")]
			if kind != "empty-sync" {
				maintenancePut(t, dir, maintenanceTemp(1), []byte("one"))
				maintenancePut(t, dir, maintenanceTemp(2), []byte("two"))
			}
			injected := errors.New("synthetic maintenance checkpoint")
			removes := 0
			var points []string
			s.fault = func(at string) error {
				points = append(points, at)
				if at == "operation.maintenance.remove" {
					removes++
					if kind == "before-first" || kind == "between" && removes == 2 {
						return injected
					}
				}
				if at == "operation.maintenance.sync" && strings.Contains(kind, "sync") {
					return injected
				}
				return nil
			}
			n, e := s.MaintainOperationStaging()
			s.fault = nil
			expected := 0
			if kind == "between" {
				expected = 1
			}
			if kind == "sync-after-delete" {
				expected = 2
			}
			uncertain := kind != "before-first"
			if n != expected || !errors.Is(e, injected) || errors.Is(e, ErrUncertain) != uncertain || s.poisoned != uncertain {
				t.Fatal("wrong uncertainty/count", n, e, points)
			}
			expectedPoints := []string{"operation.maintenance.remove"}
			if kind == "between" {
				expectedPoints = append(expectedPoints, "operation.maintenance.remove")
			}
			if kind == "sync-after-delete" {
				expectedPoints = []string{"operation.maintenance.remove", "operation.maintenance.remove", "operation.maintenance.sync"}
			}
			if kind == "empty-sync" {
				expectedPoints = []string{"operation.maintenance.sync"}
			}
			if !reflect.DeepEqual(points, expectedPoints) {
				t.Fatal("fault order", points)
			}
			// Sorted deletion leaves the second original intact after one remove.
			if kind == "between" {
				raw, e := os.ReadFile(filepath.Join(dir, maintenanceTemp(2)))
				if e != nil || !bytes.Equal(raw, []byte("two")) {
					t.Fatal("remaining original changed", e)
				}
			}
			if uncertain {
				before := tree(t, p.State)
				hits := 0
				s.fault = func(string) error { hits++; return nil }
				if n, e := s.MaintainOperationStaging(); n != 0 || !errors.Is(e, ErrUncertain) || hits != 0 {
					t.Fatal("poison reused", n, e)
				}
				s.fault = nil
				if !reflect.DeepEqual(before, tree(t, p.State)) {
					t.Fatal("poison changed tree")
				}
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			r, e := OpenStore(p, true)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			var syncs int
			r.fault = func(at string) error {
				if at == "operation.maintenance.sync" {
					syncs++
				}
				return nil
			}
			n, e = r.MaintainOperationStaging()
			r.fault = nil
			remaining := 2 - expected
			if kind == "empty-sync" {
				remaining = 0
			}
			if e != nil || n != remaining || syncs != 1 {
				t.Fatal("explicit reopened reconciliation", n, e, syncs)
			}
			maintenanceAuthority(t, r, p, id, sha, meta)
		})
	}
}
func TestOperationMaintenanceRecheckOriginalInode(t *testing.T) {
	p, s, _, _ := maintenanceFixture(t)
	dir := filepath.Join(p.State, "operations")
	name := maintenanceTemp(1)
	maintenancePut(t, dir, name, []byte("original"))
	// Controlled fault seam replaces an inventoried name; this checks the guard,
	// not a claim to defend hostile same-UID concurrent unlink races.
	s.fault = func(at string) error {
		if at == "operation.maintenance.remove" {
			if e := os.Rename(filepath.Join(dir, name), filepath.Join(dir, "saved-original")); e != nil {
				t.Fatal(e)
			}
			maintenancePut(t, dir, name, []byte("replacement"))
		}
		return nil
	}
	n, e := s.MaintainOperationStaging()
	s.fault = nil
	if n != 0 || !errors.Is(e, ErrIntegrity) || s.poisoned {
		t.Fatal("replacement inode deleted", n, e)
	}
	for _, x := range []struct{ name, body string }{{name, "replacement"}, {"saved-original", "original"}} {
		b, e := os.ReadFile(filepath.Join(dir, x.name))
		if e != nil || string(b) != x.body {
			t.Fatal("guard removed original/replacement", e)
		}
	}
}
func TestOperationMaintenanceBudgets(t *testing.T) {
	t.Run("single-file-inclusive", func(t *testing.T) {
		p := storePaths(t)
		s := openWriter(t, p)
		dir := filepath.Join(p.State, "operations")
		name := maintenanceTemp(1)
		maintenancePut(t, dir, name, nil)
		if e := os.Truncate(filepath.Join(dir, name), MaxOperationBytes); e != nil {
			t.Fatal(e)
		}
		if n, e := s.MaintainOperationStaging(); e != nil || n != 1 {
			t.Fatal("maximum opaque temporary rejected", n, e)
		}
	})
	t.Run("authority-count", func(t *testing.T) {
		p := storePaths(t)
		s := openWriter(t, p)
		dir := filepath.Join(p.State, "operations")
		maintenancePut(t, dir, maintenanceTemp(1), nil)
		// Names/metadata are the storage-layer authority boundary here, not Journal
		// records; no loop of fsync or fictional accepted operations is performed.
		for i := 0; i <= MaxOperationFiles; i++ {
			maintenancePut(t, dir, string(operationKey(fmt.Sprint(i)))+".json", []byte(`{}`))
		}
		before := tree(t, p.State)
		if n, e := s.MaintainOperationStaging(); n != 0 || !errors.Is(e, ErrOperationCapacity) || s.poisoned {
			t.Fatal("authority capacity", n, e)
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("capacity deleted staging")
		}
	})
	t.Run("all-entry-count-inclusive", func(t *testing.T) {
		p := storePaths(t)
		s := openWriter(t, p)
		dir := filepath.Join(p.State, "operations")
		for i := 0; i <= 2*MaxOperationFiles; i++ {
			maintenancePut(t, dir, maintenanceTemp(i), nil)
		}
		before := tree(t, p.State)
		if n, e := s.MaintainOperationStaging(); n != 0 || e == nil || s.poisoned {
			t.Fatal("total capacity", n, e)
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("entry budget partially deleted")
		}
		if e := os.Remove(filepath.Join(dir, maintenanceTemp(2*MaxOperationFiles))); e != nil {
			t.Fatal(e)
		}
		if n, e := s.MaintainOperationStaging(); e != nil || n != 2*MaxOperationFiles {
			t.Fatal("inclusive entry boundary", n, e)
		}
	})
}
