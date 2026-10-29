//go:build linux

package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSourceInventoryMissingEmptyAndHistoryReadOnly(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "final", "opaque-corrupt-final", "staging-empty", "staging-nonempty"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			dir := filepath.Join(p.State, "source-assets")
			if kind != "missing" {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "final":
				if err := os.WriteFile(filepath.Join(dir, digest([]byte(`{}`))+".json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "opaque-corrupt-final":
				if err := os.WriteFile(filepath.Join(dir, strings.Repeat("a", 64)+".json"), []byte("not JSON or matching digest"), 0600); err != nil {
					t.Fatal(err)
				}
			case "staging-empty", "staging-nonempty":
				var raw []byte
				if kind == "staging-nonempty" {
					raw = []byte("unpublished")
				}
				if err := os.WriteFile(filepath.Join(dir, ".source-"+strings.Repeat("ab", 24)), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, p.State)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			want := kind != "missing" && kind != "empty"
			got, err := s.HasSourceAssets()
			if err != nil || got != want {
				t.Fatalf("history %v %v", got, err)
			}
			if hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("inventory mutated")
			}
			s.Close()
			r, err := OpenStore(p, false)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			before = tree(t, p.State)
			got, err = r.HasSourceAssets()
			if err != nil || got != want || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("readonly inventory", err)
			}
		})
	}
}
func TestSourceInventoryUnsafeAndMalformedFailWithoutRepair(t *testing.T) {
	for _, kind := range []string{"directory-mode", "directory-symlink", "file-mode", "hardlink", "symlink", "empty-final", "unknown-name", "bad-staging", "oversize", "count-overlimit"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			dir := filepath.Join(p.State, "source-assets")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, strings.Repeat("a", 64)+".json")
			if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory-mode":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Rename(dir, dir+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-moved", dir); err != nil {
					t.Fatal(err)
				}
			case "file-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(dir, strings.Repeat("b", 64)+".json")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(p.State, "active.json"), path); err != nil {
					t.Fatal(err)
				}
			case "empty-final":
				if err := os.Truncate(path, 0); err != nil {
					t.Fatal(err)
				}
			case "unknown-name", "bad-staging":
				name := "foreign"
				if kind == "bad-staging" {
					name = ".source-nothex"
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.Truncate(path, MaxSourceAssetBytes+1); err != nil {
					t.Fatal(err)
				}
			case "count-overlimit":
				for i := 0; i < MaxSourceAssets; i++ {
					assetSparse(t, dir, i, 1)
				}
			}
			var before map[string]string
			if kind != "oversize" {
				before = tree(t, p.State)
			}
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			if got, err := s.HasSourceAssets(); got || err == nil {
				t.Fatal("malformed inventory succeeded")
			}
			if hits != 0 || before != nil && !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("inventory repaired state")
			}
			if kind == "oversize" {
				info, err := os.Stat(path)
				if err != nil || info.Size() != MaxSourceAssetBytes+1 {
					t.Fatal("oversize file altered", err)
				}
			}
		})
	}
}
func TestSourceInventoryPoisonAndClosed(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	injected := errors.New("inventory poison boundary")
	s.fault = func(at string) error {
		if at == "source.parent-sync" {
			return injected
		}
		return nil
	}
	if _, err := s.PublishSourceAsset([]byte(`{}`)); !errors.Is(err, ErrUncertain) {
		t.Fatal("failed to create genuinely poisoned Store", err)
	}
	before := tree(t, p.State)
	s.fault = func(string) error { t.Error("inventory wrote"); return injected }
	if got, err := s.HasSourceAssets(); got || !errors.Is(err, ErrUncertain) {
		t.Fatal("poison accepted", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("poison read changed tree")
	}
	s.Close()
	if got, err := s.HasSourceAssets(); got || err != ErrClosed {
		t.Fatal("closed accepted", err)
	}
}
