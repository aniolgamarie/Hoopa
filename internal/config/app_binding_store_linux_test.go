//go:build linux

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	profile "hoopa/protocol/sourceprofile"
)

func storeAppBinding(t *testing.T, id ID, password string) AppBinding {
	t.Helper()
	b, err := NewAppBinding(id, strings.Repeat("a", 64), profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: 17001, Users: []profile.User{{Username: "app-user", Password: strings.Repeat(password, 32)}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func putAppBinding(t *testing.T, s *Store, b AppBinding) {
	t.Helper()
	if sha, err := s.PublishAppBinding(b); err != nil || sha != b.Digest() {
		t.Fatal("publish binding", sha, err)
	}
}
func bindingBundle(t *testing.T, s *Store, g Generation, b AppBinding) {
	t.Helper()
	m, files := fixtureBundle()
	m.Generation = g
	name, err := AppBindingFilename(b.AppID())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b.SensitiveBytes()
	if err != nil {
		t.Fatal(err)
	}
	files[name] = raw
	m.Files = append(m.Files, BundleFile{Kind: "app_binding", Name: name, Bytes: int64(len(raw)), SHA256: b.Digest()})
	if _, err = s.PublishBundle(m, files); err != nil {
		t.Fatal(err)
	}
}

func TestAppBindingStoreReadOnlyScopeConflict(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	active, err := s.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	b := storeAppBinding(t, "app-a", "x")
	name, _ := AppBindingFilename(b.AppID())
	before := tree(t, p.State)
	if got, err := s.ReadAppBinding(b.AppID()); got.Digest() != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing fabricated binding", err)
	}
	if got, err := s.ListAppBindings(); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if got, err := s.ReadAppBindingHistory(); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read initialized input")
	}
	putAppBinding(t, s, b)
	settingsActiveSame(t, s, active)
	if info, err := os.Stat(filepath.Join(p.State, "credentials", name)); err != nil || privateInfo(info, false) != nil {
		t.Fatal("private binding", err)
	}
	before = tree(t, p.State)
	s.fault = func(string) error { t.Error("conflict wrote"); return errors.New("write") }
	if sha, err := s.PublishAppBinding(storeAppBinding(t, "app-a", "y")); sha != "" || !errors.Is(err, ErrAppBindingConflict) {
		t.Fatal("replaced immutable binding", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("conflict altered state")
	}
	s.fault = nil
	putAppBinding(t, s, storeAppBinding(t, "app-b", "z"))
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before = tree(t, p.State)
	r.fault = func(string) error { t.Error("read checkpoint"); return errors.New("write") }
	got, err := r.ReadAppBinding("app-a")
	if err != nil || got.Digest() != b.Digest() {
		t.Fatal("read original", err)
	}
	all, err := r.ListAppBindings()
	if err != nil || len(all) != 2 || all[0].AppID() != "app-a" || all[1].AppID() != "app-b" {
		t.Fatal("complete sorted inputs", err)
	}
	if sha, err := r.PublishAppBinding(b); sha != "" || !errors.Is(err, ErrReadOnly) {
		t.Fatal("readonly publish", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("readonly changed state")
	}
	r.Close()
	if _, err = r.ReadAppBinding("app-a"); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if all, err = r.ListAppBindings(); all != nil || !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestAppBindingStoreDurabilityAndReopen(t *testing.T) {
	for _, point := range []string{"app.binding-file.write", "app.binding-file.sync", "app.binding-file.closed", "app.binding-rename", "app.binding-parent-sync", "app.binding-existing-file-sync", "app.binding-existing-parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			b := storeAppBinding(t, "app-a", "x")
			existing := strings.Contains(point, "existing-")
			if existing {
				putAppBinding(t, s, b)
			}
			active, _ := s.ReadActive()
			sentinel := errors.New("injected binding durability failure")
			hits := 0
			s.fault = func(at string) error {
				if at == point {
					hits++
					return sentinel
				}
				return nil
			}
			sha, err := s.PublishAppBinding(b)
			if sha != "" || !errors.Is(err, sentinel) || hits != 1 {
				t.Fatal("missed fault", sha, err, hits)
			}
			uncertain := existing || point == "app.binding-parent-sync"
			if errors.Is(err, ErrUncertain) != uncertain {
				t.Fatal("uncertainty", err)
			}
			name, _ := AppBindingFilename(b.AppID())
			_, statErr := os.Stat(filepath.Join(p.State, "credentials", name))
			if uncertain && statErr != nil || !uncertain && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("publication visibility", statErr)
			}
			settingsActiveSame(t, s, active)
			s.fault = nil
			if uncertain {
				if _, err = s.ReadAppBinding(b.AppID()); !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned read", err)
				}
				if _, err = s.PublishAppBinding(b); !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned write", err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openWriter(t, p)
			var barriers []string
			s.fault = func(at string) error { barriers = append(barriers, at); return nil }
			putAppBinding(t, s, b)
			want := []string{"app.binding-file.write", "app.binding-file.sync", "app.binding-file.closed", "app.binding-rename", "app.binding-parent-sync"}
			if uncertain {
				want = []string{"app.binding-existing-file-sync", "app.binding-existing-parent-sync"}
			}
			if !reflect.DeepEqual(barriers, want) {
				t.Fatal("retry barriers", barriers)
			}
			settingsActiveSame(t, s, active)
		})
	}
}

func TestAppBindingStoreRejectsUnsafeAndMismatchedRecords(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "nonprivate", "wrong-id", "corrupt", "reserved-bad-name"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			b := storeAppBinding(t, "app-a", "x")
			putAppBinding(t, s, b)
			name, _ := AppBindingFilename(b.AppID())
			path := filepath.Join(p.State, "credentials", name)
			switch kind {
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			case "nonprivate":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "wrong-id":
				raw, _ := storeAppBinding(t, "app-b", "x").SensitiveBytes()
				settingsWrite(t, path, raw)
			case "corrupt":
				settingsWrite(t, path, []byte(`{"private":"broken"}`))
			case "reserved-bad-name":
				settingsWrite(t, filepath.Join(p.State, "credentials", "app-entry-garbage.json"), []byte(`{}`))
			}
			before := tree(t, p.State)
			if kind != "reserved-bad-name" {
				if got, err := s.ReadAppBinding(b.AppID()); err == nil || got.Digest() != "" {
					t.Fatal("unsafe direct record read", err)
				}
			}
			got, err := s.ListAppBindings()
			if err == nil || got != nil {
				t.Fatal("partial/unsafe inventory", got, err)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("inventory repaired")
			}
		})
	}
}

func TestAppBindingStoreHistorySurvivesLostInputAndRejectsCorruption(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	b := storeAppBinding(t, "app-a", "x")
	putAppBinding(t, s, b)
	bindingBundle(t, s, "bundle-one", b)
	bindingBundle(t, s, "bundle-two", b)
	name, _ := AppBindingFilename(b.AppID())
	if err := os.Remove(filepath.Join(p.State, "credentials", name)); err != nil {
		t.Fatal(err)
	}
	history, err := s.ReadAppBindingHistory()
	if err != nil || len(history) != 1 || history[0].Digest() != b.Digest() {
		t.Fatal("lost independent retained history", err)
	}
	// Every bundle is integrity checked, even once this app has already been found.
	dir, _ := generationDirectory("bundle-two")
	settingsWrite(t, filepath.Join(p.State, "bundles", dir, "core.yaml"), []byte("corrupt"))
	if got, err := s.ReadAppBindingHistory(); err == nil || got != nil {
		t.Fatal("partial history after unrelated asset damage", err)
	}
}

func TestAppBindingStoreHistoryRejectsRebinding(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	bindingBundle(t, s, "bundle-one", storeAppBinding(t, "app-a", "x"))
	bindingBundle(t, s, "bundle-two", storeAppBinding(t, "app-a", "y"))
	if got, err := s.ReadAppBindingHistory(); err == nil || got != nil {
		t.Fatal("conflicting immutable history", err)
	}
}

func TestAppBindingStoreInventoryBound(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	for i := 0; i < MaxAppBindings+1; i++ {
		b := storeAppBinding(t, ID(fmt.Sprintf("app-%04d", i)), "x")
		raw, _ := b.SensitiveBytes()
		name, _ := AppBindingFilename(b.AppID())
		settingsWrite(t, filepath.Join(p.State, "credentials", name), raw)
	}
	if got, err := s.ListAppBindings(); got != nil || !errors.Is(err, ErrAppBindingInventoryBound) {
		t.Fatal("unbounded/partial inventory", len(got), err)
	}
}
