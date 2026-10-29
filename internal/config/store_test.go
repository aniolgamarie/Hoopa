//go:build linux

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func storePaths(t *testing.T) Paths {
	t.Helper()
	p, err := ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func openWriter(t *testing.T, p Paths) *Store {
	t.Helper()
	s, err := OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func fixtureBundle() (BundleManifest, map[string][]byte) {
	files := map[string][]byte{"intent.json": []byte("{}\n"), "core.yaml": []byte("mode: rule\n")}
	m := BundleManifest{Version: 1, Generation: "fixture-g1", CompilerVersion: "fixture-v1", CapabilitiesDigest: digest([]byte("capabilities")), Binaries: []BinaryReference{{Path: "/fixture/mihomo", Version: "fixture-core", SHA256: digest([]byte("binary"))}}}
	for _, pair := range []struct{ kind, name string }{{"intent", "intent.json"}, {"generated_config", "core.yaml"}} {
		m.Files = append(m.Files, BundleFile{Kind: pair.kind, Name: pair.name, Bytes: int64(len(files[pair.name])), SHA256: digest(files[pair.name])})
	}
	return m, files
}

func pointerFor(g Generation, sha string) ActivePointer {
	op := ID("operation-1")
	return ActivePointer{Version: 1, Revision: "revision-2", Generation: &g, ManifestSHA256: sha, OperationID: &op, DesiredRuntime: "running"}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			value += ":" + digest(b)
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStoreReadOnlyDoesNotInitializeOrMutate(t *testing.T) {
	p := storePaths(t)
	before := tree(t, filepath.Dir(p.State))
	if _, err := OpenStore(p, false); !errors.Is(err, ErrReadUnavailable) {
		t.Fatalf("missing: %v", err)
	}
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(p.State))) {
		t.Fatal("read-only created state")
	}
	w := openWriter(t, p)
	m, files := fixtureBundle()
	sha, err := w.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.PublishActive(pointerFor(m.Generation, sha)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	// A reader must not need a writer lock inode at all.
	if err = os.Remove(filepath.Join(p.State, ".writer.lock")); err != nil {
		t.Fatal(err)
	}
	before = tree(t, p.State)
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadActive(); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadBundle(m.Generation); err != nil {
		t.Fatal(err)
	}
	if err = r.PublishActive(pointerFor(m.Generation, sha)); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("reader write: %v", err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read path changed durable tree")
	}
}

func TestStoreInitializationAndWriterLock(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	a, err := s.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != 1 || a.Revision == "" || a.Generation != nil || a.OperationID != nil || a.DesiredRuntime != "stopped" {
		t.Fatalf("bad initial: %+v", a)
	}
	for _, path := range []string{p.Config, p.State, p.Cache, p.Runtime, filepath.Join(p.State, "bundles"), filepath.Join(p.State, "operations"), filepath.Join(p.State, "runs"), filepath.Join(p.State, "credentials")} {
		info, err := os.Stat(path)
		if err != nil || privateInfo(info, true) != nil {
			t.Fatalf("private dir %s: %v", path, err)
		}
	}
	if err := os.RemoveAll(p.Runtime); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenStore(p, true); !errors.Is(err, ErrWriterBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other := openWriter(t, p)
	b, err := other.ReadActive()
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("reopen reset authority: %+v %v", b, err)
	}
}

func TestStoreMissingAuthorityNotReinitialized(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	s.Close()
	if err := os.Remove(filepath.Join(p.State, "active.json")); err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.State)
	if w, err := OpenStore(p, true); !errors.Is(err, ErrReadUnavailable) {
		if w != nil {
			w.Close()
		}
		t.Fatalf("lost pointer: %v", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("missing authority was repaired")
	}
}

func TestStoreBundleIntegrityAndPointer(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	m, files := fixtureBundle()
	sha, err := s.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.State)
	if again, err := s.PublishBundle(m, files); err != nil || again != sha {
		t.Fatalf("idempotency %s %v", again, err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("idempotent publish changed state")
	}
	got, err := s.ReadBundle(m.Generation)
	if err != nil || !reflect.DeepEqual(got.Files, files) || got.ManifestSHA256 != sha {
		t.Fatalf("read bundle %v", err)
	}
	pointer := pointerFor(m.Generation, sha)
	if err = s.PublishActive(pointer); err != nil {
		t.Fatal(err)
	}
	a, err := s.ReadActive()
	if err != nil || !reflect.DeepEqual(a, pointer) {
		t.Fatalf("pointer %+v %v", a, err)
	}
	m.CompilerVersion = "different"
	if _, err = s.PublishBundle(m, files); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("collision %v", err)
	}
	for _, bad := range []ActivePointer{pointerFor("absent", sha), pointerFor(m.Generation, strings.Repeat("a", 64)), {Version: 1, Revision: "r", DesiredRuntime: "running"}} {
		before := tree(t, p.State)
		if err := s.PublishActive(bad); err == nil {
			t.Fatal("accepted invalid pointer")
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("invalid pointer mutated state")
		}
	}
	dir, _ := generationDirectory(m.Generation)
	if err := os.WriteFile(filepath.Join(p.State, "bundles", dir, "core.yaml"), []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	before = tree(t, p.State)
	if _, err = s.ReadBundle(m.Generation); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corruption %v", err)
	}
	if _, err = s.ReadActive(); !errors.Is(err, ErrReadUnavailable) {
		t.Fatalf("corrupt reference %v", err)
	}
	if _, err = s.PublishBundle(m, files); err == nil {
		t.Fatal("repaired corrupted generation")
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("corruption refusal mutated state")
	}
}

func TestStoreRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "hardlink", "fifo", "oversize", "duplicate-json", "extra-bundle-file", "bundle-directory-permissions"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			m, files := fixtureBundle()
			sha, err := s.PublishBundle(m, files)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.PublishActive(pointerFor(m.Generation, sha)); err != nil {
				t.Fatal(err)
			}
			s.Close()
			active := filepath.Join(p.State, "active.json")
			raw, err := os.ReadFile(active)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "permissions":
				err = os.Chmod(active, 0644)
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				err = os.WriteFile(target, raw, 0600)
				if err == nil {
					err = os.Remove(active)
				}
				if err == nil {
					err = os.Symlink(target, active)
				}
			case "hardlink":
				err = os.Link(active, filepath.Join(p.State, "alias"))
			case "fifo":
				err = os.Remove(active)
				if err == nil {
					err = syscall.Mkfifo(active, 0600)
				}
			case "oversize":
				err = os.WriteFile(active, bytes.Repeat([]byte("x"), MaxActiveBytes+1), 0600)
			case "duplicate-json":
				err = os.WriteFile(active, bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), 0600)
			case "extra-bundle-file":
				dir, _ := generationDirectory(m.Generation)
				err = os.WriteFile(filepath.Join(p.State, "bundles", dir, "extra"), nil, 0600)
			case "bundle-directory-permissions":
				dir, _ := generationDirectory(m.Generation)
				err = os.Chmod(filepath.Join(p.State, "bundles", dir), 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := tree(t, p.State)
			if r, err := OpenStore(p, false); err == nil {
				r.Close()
				t.Fatal("accepted unsafe authority")
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("unsafe file was modified")
			}
		})
	}
}

func TestStoreRejectsUnsafeDirectories(t *testing.T) {
	for _, kind := range []string{"leaf-mode", "ancestor-mode", "leaf-symlink", "ancestor-symlink"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			parent := filepath.Join(base, "parent")
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(parent, "leaf")
			if err := os.Mkdir(leaf, 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "leaf-mode":
				err = os.Chmod(leaf, 0755)
			case "ancestor-mode":
				err = os.Chmod(parent, 0777)
			case "leaf-symlink":
				err = os.Remove(leaf)
				if err == nil {
					err = os.Symlink(t.TempDir(), leaf)
				}
			case "ancestor-symlink":
				err = os.Rename(parent, parent+"-old")
				if err == nil {
					err = os.Symlink(parent+"-old", parent)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if r, err := privateRoot(leaf, false); err == nil {
				r.Close()
				t.Fatal("unsafe directory accepted")
			}
		})
	}
}

func TestStoreRejectsManifestBoundsBeforeMutation(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	for _, kind := range []string{"generation", "filename", "reserved", "negative", "total", "count", "digest", "missing-data", "unknown-kind", "huge-version"} {
		t.Run(kind, func(t *testing.T) {
			m, files := fixtureBundle()
			switch kind {
			case "generation":
				m.Generation = "../outside"
			case "filename":
				m.Files[0].Name = "../outside"
			case "reserved":
				m.Files[0].Name = "manifest.json"
			case "negative":
				m.Files[0].Bytes = -1
			case "total":
				m.Files[0].Bytes = MaxBundleBytes
				m.Files[1].Bytes = 1
			case "count":
				m.Files = make([]BundleFile, MaxBundleFiles+1)
			case "digest":
				m.Files[0].SHA256 = strings.Repeat("A", 64)
			case "missing-data":
				delete(files, "core.yaml")
			case "unknown-kind":
				m.Files[0].Kind = "script"
			case "huge-version":
				m.CompilerVersion = strings.Repeat("x", 129)
			}
			before := tree(t, p.State)
			if _, err := s.PublishBundle(m, files); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("bound refusal: %v", err)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("invalid bundle mutated state")
			}
		})
	}
}

func TestStoreWriterLockSurvivesRuntimeDeletionAndProcessDeath(t *testing.T) {
	if os.Getenv("HOOPA_STORE_LOCK_CHILD") == "1" {
		p, err := ResolvePaths(os.Getenv("HOOPA_STORE_LOCK_HOME"), nil)
		if err != nil {
			t.Fatal(err)
		}
		s, err := OpenStore(p, true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		fmt.Println("STORE_LOCK_HELD")
		for {
			time.Sleep(time.Hour)
		}
	}
	home := t.TempDir()
	p, err := ResolvePaths(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreWriterLockSurvivesRuntimeDeletionAndProcessDeath$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), "HOOPA_STORE_LOCK_CHILD=1", "HOOPA_STORE_LOCK_HOME="+home)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, len("STORE_LOCK_HELD\n"))
		_, e := io.ReadFull(stdout, b)
		if e == nil && string(b) != "STORE_LOCK_HELD\n" {
			e = fmt.Errorf("unexpected child output %q", b)
		}
		ready <- e
	}()
	select {
	case err = <-ready:
		if err != nil {
			t.Fatalf("child readiness: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child lock timeout")
	}
	if err = os.RemoveAll(p.Runtime); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenStore(p, true); !errors.Is(err, ErrWriterBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("process lock %v", err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected killed child: %v", err)
	}
	s := openWriter(t, p)
	if _, err = s.ReadActive(); err != nil {
		t.Fatal(err)
	}
}
