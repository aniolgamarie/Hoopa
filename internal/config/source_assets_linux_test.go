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

func assetPut(t *testing.T, s *Store, raw []byte) string {
	t.Helper()
	sha, e := s.PublishSourceAsset(raw)
	if e != nil || sha != digest(raw) {
		t.Fatalf("publish %q %v", sha, e)
	}
	return sha
}
func assetDir(t *testing.T, p Paths) string {
	t.Helper()
	dir := filepath.Join(p.State, "source-assets")
	if e := os.Mkdir(dir, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		t.Fatal(e)
	}
	return dir
}
func assetFile(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func assetSparse(t *testing.T, dir string, i int, size int64) {
	t.Helper()
	f, e := os.OpenFile(filepath.Join(dir, digest([]byte(fmt.Sprint("sparse-", i)))+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	e = errors.Join(f.Truncate(size), f.Close())
	if e != nil {
		t.Fatal(e)
	}
}
func assetActiveSame(t *testing.T, s *Store, want ActivePointer) {
	t.Helper()
	got, e := s.ReadActive()
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("asset moved active/revision: %+v %v", got, e)
	}
}

func TestSourceAssetExplicitPublishAndReadOnly(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	initial, e := s.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(p.State, "source-assets")
	if _, e = os.Stat(dir); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("open created optional assets", e)
	}
	before := tree(t, p.State)
	for _, sha := range []string{digest([]byte("missing")), "", strings.Repeat("A", 64), "../active.json", strings.Repeat("a", 63)} {
		got, e := s.ReadSourceAsset(sha)
		if e == nil || got != nil {
			t.Fatal("invalid/missing read accepted")
		}
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read created optional directory")
	}
	raw := []byte(`{"opaque":"not provider authority"}`)
	sha := assetPut(t, s, raw)
	info, e := os.Stat(filepath.Join(dir, sha+".json"))
	if e != nil || privateInfo(info, false) != nil {
		t.Fatal("asset permissions", e)
	}
	got, e := s.ReadSourceAsset(sha)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("read failed", e)
	}
	got[0] = '!'
	again, e := s.ReadSourceAsset(sha)
	if e != nil || !bytes.Equal(again, raw) {
		t.Fatal("read aliases")
	}
	assetActiveSame(t, s, initial)
	if other, e := OpenStore(p, true); !errors.Is(e, ErrWriterBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("asset writer bypassed flock", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	before = tree(t, p.State)
	r, e := OpenStore(p, false)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	r.fault = func(string) error { t.Error("reader wrote checkpoint"); return errors.New("unexpected") }
	got, e = r.ReadSourceAsset(sha)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal(e)
	}
	if sha, e = r.PublishSourceAsset(raw); sha != "" || !errors.Is(e, ErrReadOnly) {
		t.Fatal("reader published", e)
	}
	assetActiveSame(t, r, initial)
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read changed filesystem")
	}
	r.Close()
	if got, e = r.ReadSourceAsset(digest(raw)); got != nil || !errors.Is(e, ErrClosed) {
		t.Fatal("closed read", e)
	}
}
func TestSourceAssetDurableOrderAndExactRepeat(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	var seen []string
	s.fault = func(at string) error { seen = append(seen, at); return nil }
	raw := []byte(`{"x":1}`)
	sha := assetPut(t, s, raw)
	want := []string{"source.assets-mkdir", "source.reopen-sync", "source.root-sync", "source.file.write", "source.file.sync", "source.file.closed", "source.rename", "source.parent-sync"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("order got %v", seen)
	}
	seen = nil
	if assetPut(t, s, raw) != sha {
		t.Fatal("repeat changed address")
	}
	want = []string{"source.reopen-sync", "source.root-sync", "source.existing-file-sync", "source.existing-parent-sync"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("repeat skipped barriers %v", seen)
	}
}

func TestSourceAssetOpaqueJSONDoesNotConferAuthority(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	initial, err := s.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	// This layer stores exact opaque JSON, including values that are not valid
	// provider assets. RestoreAsset and a committed update must establish authority.
	for _, text := range []string{`null`, `[]`, `false`, `"opaque"`, `{"schema_version":99}`, `{"duplicate":1,"duplicate":2}`} {
		raw := []byte(text)
		sha := assetPut(t, s, raw)
		got, err := s.ReadSourceAsset(sha)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("opaque JSON changed", err)
		}
	}
	assetActiveSame(t, s, initial)
}

func TestSourceAssetFailureBoundariesAndReopen(t *testing.T) {
	for _, point := range []string{"source.assets-mkdir", "source.reopen-sync", "source.root-sync", "source.file.write", "source.file.sync", "source.file.closed", "source.rename", "source.parent-sync", "source.existing-file-sync", "source.existing-parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(`{"candidate":true}`)
			sha := digest(raw)
			initial, e := s.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			existing := strings.Contains(point, "existing-")
			if existing {
				assetPut(t, s, raw)
			}
			injected := errors.New("source boundary fault")
			hits := 0
			s.fault = func(at string) error {
				if at == point {
					hits++
					return injected
				}
				return nil
			}
			got, e := s.PublishSourceAsset(raw)
			if got != "" || !errors.Is(e, injected) || hits != 1 {
				t.Fatalf("unhit boundary %q %v hits%d", got, e, hits)
			}
			uncertain := point == "source.reopen-sync" || point == "source.root-sync" || point == "source.parent-sync" || existing
			if errors.Is(e, ErrUncertain) != uncertain {
				t.Fatalf("uncertainty %v", e)
			}
			assetActiveSame(t, s, initial)
			s.fault = nil
			visible := point == "source.parent-sync" || existing
			info, statErr := os.Stat(filepath.Join(p.State, "source-assets", sha+".json"))
			_ = info
			if visible && statErr != nil || !visible && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("wrong publication visibility", statErr)
			}
			if entries, e := os.ReadDir(filepath.Join(p.State, "source-assets")); e == nil {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".source-") {
						t.Fatal("own staging leaked")
					}
				}
			}
			if uncertain {
				if got, e = s.PublishSourceAsset(raw); got != "" || !errors.Is(e, ErrUncertain) {
					t.Fatal("poisoned writer reused", e)
				}
				if visible {
					if b, e := s.ReadSourceAsset(sha); b != nil || !errors.Is(e, ErrUncertain) {
						t.Fatal("poisoned read accepted", e)
					}
				}
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				s = openWriter(t, p)
			}
			var retry []string
			s.fault = func(at string) error { retry = append(retry, at); return nil }
			assetPut(t, s, raw)
			if visible {
				want := []string{"source.reopen-sync", "source.root-sync", "source.existing-file-sync", "source.existing-parent-sync"}
				if !reflect.DeepEqual(retry, want) {
					t.Fatalf("reopen failed exact retry barriers %v", retry)
				}
			}
			assetActiveSame(t, s, initial)
		})
	}
}
func TestSourceAssetInvalidJSONAndSingleLimitNoWrites(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	before := tree(t, p.State)
	hits := 0
	s.fault = func(string) error { hits++; return nil }
	tooLarge := bytes.Repeat([]byte{' '}, MaxSourceAssetBytes+1)
	copy(tooLarge, []byte(`{}`))
	for _, raw := range [][]byte{nil, {}, []byte("{"), []byte(`{} {}`), tooLarge} {
		if sha, e := s.PublishSourceAsset(raw); sha != "" || !errors.Is(e, ErrIntegrity) {
			t.Fatal("invalid accepted", e)
		}
	}
	if hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("invalid input wrote")
	}
	// Real 64MiB valid JSON at the inclusive single-file boundary (not sparse).
	s.fault = nil
	raw := tooLarge[:MaxSourceAssetBytes]
	sha := assetPut(t, s, raw)
	info, e := os.Stat(filepath.Join(p.State, "source-assets", sha+".json"))
	if e != nil || info.Size() != MaxSourceAssetBytes {
		t.Fatal("exact limit failed", e)
	}
}
func TestSourceAssetUnsafeOrCorruptPathsRemainUnrepaired(t *testing.T) {
	for _, kind := range []string{"file-mode", "hardlink", "symlink", "directory-mode", "directory-symlink", "corrupt-digest", "bad-json", "oversize", "empty", "unknown-inventory", "invalid-staging"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(`{"ok":true}`)
			sha := assetPut(t, s, raw)
			dir := filepath.Join(p.State, "source-assets")
			path := filepath.Join(dir, sha+".json")
			switch kind {
			case "file-mode":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(path, filepath.Join(dir, "linked")); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				os.Remove(path)
				target := filepath.Join(p.State, "outside")
				assetFile(t, target, raw)
				if e := os.Symlink(target, path); e != nil {
					t.Fatal(e)
				}
			case "directory-mode":
				if e := os.Chmod(dir, 0755); e != nil {
					t.Fatal(e)
				}
			case "directory-symlink":
				moved := dir + "-moved"
				if e := os.Rename(dir, moved); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(moved, dir); e != nil {
					t.Fatal(e)
				}
			case "corrupt-digest":
				assetFile(t, path, []byte(`{"forged":true}`))
			case "bad-json":
				raw = []byte("not JSON")
				sha = digest(raw)
				path = filepath.Join(dir, sha+".json")
				assetFile(t, path, raw)
			case "oversize":
				if e := os.Truncate(path, MaxSourceAssetBytes+1); e != nil {
					t.Fatal(e)
				}
			case "empty":
				assetFile(t, path, nil)
			case "unknown-inventory":
				assetFile(t, filepath.Join(dir, "unknown"), []byte("{}"))
			case "invalid-staging":
				assetFile(t, filepath.Join(dir, ".source-nothex"), []byte("{}"))
			}
			// Oversize uses sparse metadata: avoid tree helper reading 64MiB just to hash it.
			var before map[string]string
			if kind != "oversize" {
				before = tree(t, p.State)
			}
			if kind != "unknown-inventory" && kind != "invalid-staging" {
				if b, e := s.ReadSourceAsset(sha); b != nil || e == nil {
					t.Fatal("unsafe/corrupt read accepted")
				}
			}
			publish := []byte(`{"new":true}`)
			if kind == "corrupt-digest" || kind == "bad-json" {
				publish = raw
			}
			if got, e := s.PublishSourceAsset(publish); got != "" || e == nil {
				t.Fatal("unsafe inventory accepted", e)
			}
			if before != nil && !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("silently repaired filesystem")
			}
		})
	}
}
func TestSourceAssetStagingAndOrphanNeverAdvanceActive(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	initial, e := s.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte(`{"known":true}`)
	sha := assetPut(t, s, raw)
	dir := assetDir(t, p)
	stage := filepath.Join(dir, ".source-"+strings.Repeat("ab", 24))
	assetFile(t, stage, nil)
	before := tree(t, p.State)
	if got, e := s.PublishSourceAsset([]byte(`{"unknown":true}`)); got != "" || !errors.Is(e, ErrSourceAssetStaging) {
		t.Fatal("staging did not block new publish", e)
	}
	assetPut(t, s, raw)
	got, e := s.ReadSourceAsset(sha)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("unrelated empty staging altered")
	}
	assetActiveSame(t, s, initial)
	s.Close()
	s = openWriter(t, p)
	assetActiveSame(t, s, initial)
	if _, e := s.ReadSourceAsset(sha); e != nil {
		t.Fatal("orphan exact asset lost", e)
	}
	// The asset remains readable but no active generation or revision was assigned.
}
func TestSourceAssetCountAndAggregateCapacity(t *testing.T) {
	for _, kind := range []string{"count", "total", "over-count", "oversize-member"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(`{"known":true}`)
			assetPut(t, s, raw)
			dir := assetDir(t, p)
			switch kind {
			case "count", "over-count":
				count := MaxSourceAssets - 1
				if kind == "over-count" {
					count++
				}
				for i := 0; i < count; i++ {
					assetSparse(t, dir, i, 1)
				}
			case "total":
				for i := 0; i < 8; i++ {
					size := int64(MaxSourceAssetBytes)
					if i == 7 {
						size -= int64(len(raw))
					}
					assetSparse(t, dir, i, size)
				}
			case "oversize-member":
				assetSparse(t, dir, 0, MaxSourceAssetBytes+1)
			}
			// Count/total fixtures are metadata-only sparse files, not genuine assets or
			// evidence of 512MiB of allocated data. Inventory deliberately checks metadata.
			before, e := os.ReadDir(dir)
			if e != nil {
				t.Fatal(e)
			}
			if sha, e := s.PublishSourceAsset([]byte(`{"new":true}`)); sha != "" || !errors.Is(e, ErrSourceAssetCapacity) {
				t.Fatal("capacity accepted", e)
			}
			after, e := os.ReadDir(dir)
			if e != nil || len(after) != len(before) {
				t.Fatal("capacity failure created files", e)
			}
			// Already known exact bytes require no new capacity, even with unrelated
			// excess inventory; their durability barriers still execute.
			assetPut(t, s, raw)
		})
	}
}
