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

func workdirName(n int) string { return fmt.Sprintf("launch-%048x", n) }
func workdirTreeUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("rejected preparation changed bytes, permissions, size or mtime")
	}
}
func TestPrepareLaunchWorkdirDurableOriginals(t *testing.T) {
	for _, size := range []int{1, 65536} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			p, s, intent, _ := runStoreFixture(t)
			raw := bytes.Repeat([]byte{'x'}, size)
			var points []string
			s.fault = func(p string) error { points = append(points, p); return nil }
			path, e := s.PrepareLaunchWorkdir(workdirName(1), raw)
			s.fault = nil
			if e != nil || path != filepath.Join(p.Runtime, workdirName(1)) {
				t.Fatal("prepare", path, e)
			}
			want := []string{"workdir.mkdir", "workdir.config.write", "workdir.config.sync", "workdir.config.closed", "workdir.directory-sync", "workdir.parent-sync"}
			if !reflect.DeepEqual(points, want) {
				t.Fatal("durability order", points)
			}
			for _, x := range []struct {
				path string
				mode os.FileMode
			}{{path, 0700}, {filepath.Join(path, "config.json"), 0600}} {
				st, e := os.Lstat(x.path)
				if e != nil || st.Mode().Perm() != x.mode || st.Mode()&os.ModeSymlink != 0 {
					t.Fatal("private original mode", e)
				}
			}
			intent.Workdir = path
			intent.ConfigSHA256 = digest(raw)
			r, e := OpenRunWorkdir(intent)
			if e != nil {
				t.Fatal("prepared original not readable", e)
			}
			b, e := r.ReadFile("config.json")
			r.Close()
			if e != nil || !bytes.Equal(b, raw) {
				t.Fatal("raw config changed", e)
			}
			log := filepath.Join(path, "launcher.log")
			if e := os.WriteFile(log, []byte("old launcher original\n"), 0600); e != nil {
				t.Fatal(e)
			}
			before := tree(t, p.Runtime)
			if _, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("replace")); !errors.Is(e, ErrRunConflict) {
				t.Fatal("existing original reused", e)
			}
			workdirTreeUnchanged(t, p.Runtime, before)
			if _, e := s.PrepareLaunchWorkdir(workdirName(2), []byte("next")); e != nil {
				t.Fatal("fresh name", e)
			}
			got, e := os.ReadFile(log)
			if e != nil || string(got) != "old launcher original\n" {
				t.Fatal("old log changed", e)
			}
		})
	}
}
func TestPrepareLaunchWorkdirRejectShape(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"", []byte("x")}, {"launch-" + strings.Repeat("a", 47), []byte("x")}, {"launch-" + strings.Repeat("A", 48), []byte("x")}, {"launch-" + strings.Repeat("g", 48), []byte("x")}, {workdirName(1) + "/child", []byte("x")}, {"../" + workdirName(1), []byte("x")}, {workdirName(1) + "\x00", []byte("x")}, {workdirName(1), nil}, {workdirName(1), bytes.Repeat([]byte("x"), 65537)},
	} {
		t.Run(fmt.Sprintf("case-%d", len(tc.name)+len(tc.raw)), func(t *testing.T) {
			before := tree(t, p.Runtime)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			path, e := s.PrepareLaunchWorkdir(tc.name, tc.raw)
			s.fault = nil
			if path != "" || !errors.Is(e, ErrIntegrity) || hits != 0 || s.poisoned {
				t.Fatal("invalid input mutated or wrong error", e, hits)
			}
			workdirTreeUnchanged(t, p.Runtime, before)
		})
	}
}
func TestPrepareLaunchWorkdirExistingEntries(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "regular"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			path := filepath.Join(p.Runtime, workdirName(1))
			target := filepath.Join(p.Runtime, "original")
			if e := os.Mkdir(target, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(target, "launcher.log"), []byte("do not repair"), 0600); e != nil {
				t.Fatal(e)
			}
			var e error
			switch kind {
			case "directory":
				e = os.Mkdir(path, 0700)
			case "symlink":
				e = os.Symlink(target, path)
			case "regular":
				e = os.WriteFile(path, []byte("occupied"), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			before := tree(t, p.Runtime)
			if path, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("x")); path != "" || !errors.Is(e, ErrRunConflict) || s.poisoned {
				t.Fatal("collision", e)
			}
			workdirTreeUnchanged(t, p.Runtime, before)
		})
	}
}
func TestPrepareLaunchWorkdirUnsafeRuntime(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			switch kind {
			case "mode":
				if e := os.Chmod(p.Runtime, 0755); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				saved := p.Runtime + "-saved"
				if e := os.Rename(p.Runtime, saved); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(saved, p.Runtime); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := os.Remove(p.Runtime); e != nil {
					t.Fatal(e)
				}
			}
			before := tree(t, filepath.Dir(p.State))
			path, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("x"))
			if e == nil || path != "" || s.poisoned {
				t.Fatal("unsafe root accepted/repaired", e)
			}
			workdirTreeUnchanged(t, filepath.Dir(p.State), before)
		})
	}
}
func TestPrepareLaunchWorkdirWriterQualification(t *testing.T) {
	for _, kind := range []string{"readonly", "closed", "poison"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			expected := ErrReadOnly
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
				expected = ErrClosed
			}
			if kind == "poison" {
				s.fault = func(at string) error {
					if at == "workdir.config.sync" {
						return errors.New("synthetic sync boundary")
					}
					return nil
				}
				if _, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("partial")); !errors.Is(e, ErrUncertain) {
					t.Fatal(e)
				}
				s.fault = nil
				expected = ErrUncertain
			}
			before := tree(t, filepath.Dir(p.State))
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			path, e := s.PrepareLaunchWorkdir(workdirName(2), []byte("x"))
			s.fault = nil
			if path != "" || !errors.Is(e, expected) || hits != 0 {
				t.Fatal("unqualified writer", e, hits)
			}
			workdirTreeUnchanged(t, filepath.Dir(p.State), before)
		})
	}
}
func TestPrepareLaunchWorkdirCapacity(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	// Count every existing entry, not only recognized launch directories. Filling
	// uses no fsync loop: only the one boundary preparation exercises durability.
	for i := 0; i < MaxLaunchWorkdirs-3; i++ {
		if e := os.WriteFile(filepath.Join(p.Runtime, fmt.Sprintf("opaque-%04d", i)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Mkdir(filepath.Join(p.Runtime, "opaque-directory"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("opaque-directory", filepath.Join(p.Runtime, "opaque-link")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("last available")); e != nil {
		t.Fatal("1023 existing entries must allow one creation", e)
	}
	before := tree(t, p.Runtime)
	if path, e := s.PrepareLaunchWorkdir(workdirName(2), []byte("overflow")); path != "" || !errors.Is(e, ErrIntegrity) || s.poisoned {
		t.Fatal("actual full capacity", e)
	}
	workdirTreeUnchanged(t, p.Runtime, before)
}
func TestPrepareLaunchWorkdirFaultBoundaries(t *testing.T) {
	points := []string{"workdir.mkdir", "workdir.config.write", "workdir.config.sync", "workdir.config.closed", "workdir.directory-sync", "workdir.parent-sync"}
	for i, point := range points {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			injected := errors.New("synthetic checkpoint failure")
			var seen []string
			s.fault = func(at string) error {
				seen = append(seen, at)
				if at == point {
					return injected
				}
				return nil
			}
			path, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("original bytes"))
			s.fault = nil
			if path != "" || !errors.Is(e, injected) || errors.Is(e, ErrUncertain) != (i > 0) || s.poisoned != (i > 0) || !reflect.DeepEqual(seen, points[:i+1]) {
				t.Fatal("failure authority/order", e, seen)
			}
			dir := filepath.Join(p.Runtime, workdirName(1))
			raw, readErr := os.ReadFile(filepath.Join(dir, "config.json"))
			if i == 0 {
				if _, e := os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("pre-mkdir failure created original", e)
				}
				if _, e := s.PrepareLaunchWorkdir(workdirName(1), []byte("retry safe")); e != nil {
					t.Fatal("pre-mutation failure poisoned", e)
				}
				return
			}
			if readErr != nil {
				t.Fatal("partial original deleted", readErr)
			}
			want := []byte("original bytes")
			if i == 1 {
				want = nil
			}
			if !bytes.Equal(raw, want) {
				t.Fatal("wrong retained partial bytes")
			}
			before := tree(t, p.Runtime)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			if _, e := s.PrepareLaunchWorkdir(workdirName(2), []byte("forbidden")); !errors.Is(e, ErrUncertain) || hits != 0 {
				t.Fatal("poison did not reject before write", e, hits)
			}
			s.fault = nil
			workdirTreeUnchanged(t, p.Runtime, before)
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			reopened, e := OpenStore(p, true)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			if _, e := reopened.PrepareLaunchWorkdir(workdirName(1), []byte("overwrite")); !errors.Is(e, ErrRunConflict) {
				t.Fatal("reopen reused failed original", e)
			}
			workdirTreeUnchanged(t, p.Runtime, before)
		})
	}
}
