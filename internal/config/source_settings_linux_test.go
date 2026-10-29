//go:build linux

package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func settingsPut(t *testing.T, s *Store, raw []byte) string {
	t.Helper()
	sha, err := s.PublishSourceSettings(raw)
	if err != nil || sha != digest(raw) {
		t.Fatalf("settings publish %q %v", sha, err)
	}
	return sha
}
func settingsActiveSame(t *testing.T, s *Store, want ActivePointer) {
	t.Helper()
	got, err := s.ReadActive()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("settings moved active/revision: %+v %v", got, err)
	}
}
func settingsWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSourceSettingsMissingReadOnlyAndClosed(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	before := tree(t, p.State)
	calls := 0
	s.fault = func(string) error { calls++; return nil }
	b, sha, err := s.ReadSourceSettings()
	if b != nil || sha != "" || !errors.Is(err, ErrReadUnavailable) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing supplied default", err)
	}
	if calls != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("missing read mutated state")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before = tree(t, p.State)
	b, sha, err = r.ReadSourceSettings()
	if b != nil || sha != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatal("readonly missing read", err)
	}
	if sha, err = r.PublishSourceSettings([]byte(`{}`)); sha != "" || !errors.Is(err, ErrReadOnly) {
		t.Fatal("readonly publish", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("readonly created settings")
	}
	r.Close()
	if b, sha, err = r.ReadSourceSettings(); b != nil || sha != "" || !errors.Is(err, ErrClosed) {
		t.Fatal("closed read", err)
	}
	if sha, err = r.PublishSourceSettings([]byte(`{}`)); sha != "" || !errors.Is(err, ErrClosed) {
		t.Fatal("closed publish", err)
	}
}
func TestSourceSettingsExactScopeAndDetachedRead(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	initial, err := s.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(p.State, "credentials")
	unrelated := filepath.Join(credentials, "unrelated-private-record")
	settingsWrite(t, unrelated, []byte("untouched"))
	// A prior unrelated temporary is never recognized as settings or cleaned up.
	stale := filepath.Join(credentials, ".source-settings-"+strings.Repeat("ab", 24))
	settingsWrite(t, stale, nil)
	before := tree(t, p.State)
	raw := []byte(`{"opaque_input":"not initialized business state"}`)
	sha := settingsPut(t, s, raw)
	path := filepath.Join(credentials, sourceSettingsName)
	info, err := os.Stat(path)
	if err != nil || privateInfo(info, false) != nil {
		t.Fatal("nonprivate settings file", err)
	}
	after := tree(t, p.State)
	delete(after, path)
	delete(after, credentials)
	delete(before, credentials)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("settings touched active/journal/unrelated tree")
	}
	b, got, err := s.ReadSourceSettings()
	if err != nil || got != sha || !bytes.Equal(b, raw) {
		t.Fatal("read changed exact bytes/hash", err)
	}
	b[0] = '!'
	again, _, err := s.ReadSourceSettings()
	if err != nil || !bytes.Equal(again, raw) {
		t.Fatal("getter shares bytes", err)
	}
	settingsActiveSame(t, s, initial)
	s.Close()
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before = tree(t, p.State)
	r.fault = func(string) error { t.Error("read invoked write checkpoint"); return errors.New("unexpected write") }
	b, got, err = r.ReadSourceSettings()
	if err != nil || got != sha || !bytes.Equal(b, raw) {
		t.Fatal("readonly roundtrip", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("successful read changed tree")
	}
	settingsActiveSame(t, r, initial)
}
func TestSourceSettingsPublicationAndReplayBarriers(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	var seen []string
	s.fault = func(at string) error { seen = append(seen, at); return nil }
	raw := []byte(`{"identity_key":"opaque-test-value"}`)
	sha := settingsPut(t, s, raw)
	want := []string{"source.settings-file.write", "source.settings-file.sync", "source.settings-file.closed", "source.settings-rename", "source.settings-parent-sync"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("first publication order %v", seen)
	}
	seen = nil
	if settingsPut(t, s, raw) != sha {
		t.Fatal("replay changed hash")
	}
	want = []string{"source.settings-existing-file-sync", "source.settings-existing-parent-sync"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("replay skipped barriers %v", seen)
	}
}
func TestSourceSettingsConflictIsExactAndZeroWrite(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	raw := []byte(`{"x":1}`)
	sha := settingsPut(t, s, raw)
	before := tree(t, p.State)
	hits := 0
	s.fault = func(string) error { hits++; return errors.New("must not write") }
	for _, changed := range []string{`{"x":2}`, `{ "x":1 }`, `null`, `{"identity_key":"replacement"}`} {
		if got, err := s.PublishSourceSettings([]byte(changed)); got != "" || err != ErrSourceSettingsConflict {
			t.Fatal("conflict not rejected", err)
		}
	}
	if hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("conflict wrote")
	}
	b, got, err := s.ReadSourceSettings()
	if err != nil || got != sha || !bytes.Equal(b, raw) {
		t.Fatal("conflict changed original", err)
	}
	// A conflict does not poison the writer; identical replay remains possible.
	s.fault = nil
	settingsPut(t, s, raw)
}
func TestSourceSettingsOpaqueJSONAndSizeBound(t *testing.T) {
	for _, text := range []string{`null`, `[]`, `false`, `"opaque"`, `{"schema_version":99}`, `{"duplicate":1,"duplicate":2}`} {
		t.Run(text, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(text)
			sha := settingsPut(t, s, raw)
			b, got, err := s.ReadSourceSettings()
			if err != nil || got != sha || !bytes.Equal(b, raw) {
				t.Fatal("opaque JSON rejected", err)
			}
		})
	}
	p := storePaths(t)
	s := openWriter(t, p)
	before := tree(t, p.State)
	hits := 0
	s.fault = func(string) error { hits++; return nil }
	large := bytes.Repeat([]byte{' '}, MaxSourceSettingsBytes+1)
	copy(large, []byte(`{}`))
	for _, raw := range [][]byte{nil, {}, []byte("{"), []byte(`{} {}`), large} {
		if sha, err := s.PublishSourceSettings(raw); sha != "" || err != ErrIntegrity {
			t.Fatal("invalid/oversized settings accepted", err)
		}
	}
	if hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("invalid input wrote")
	}
	s.fault = nil
	exact := large[:MaxSourceSettingsBytes]
	sha := settingsPut(t, s, exact)
	b, got, err := s.ReadSourceSettings()
	if err != nil || got != sha || !bytes.Equal(b, exact) {
		t.Fatal("inclusive size limit failed", err)
	}
}
func TestSourceSettingsFaultBoundariesPoisonAndReopen(t *testing.T) {
	for _, point := range []string{"source.settings-file.write", "source.settings-file.sync", "source.settings-file.closed", "source.settings-rename", "source.settings-parent-sync", "source.settings-existing-file-sync", "source.settings-existing-parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(`{"candidate":"settings"}`)
			initial, err := s.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			existing := strings.Contains(point, "existing-")
			if existing {
				settingsPut(t, s, raw)
			}
			injected := errors.New("settings durability boundary")
			hits := 0
			s.fault = func(at string) error {
				if at == point {
					hits++
					return injected
				}
				return nil
			}
			sha, err := s.PublishSourceSettings(raw)
			if sha != "" || !errors.Is(err, injected) || hits != 1 {
				t.Fatalf("boundary unhit %q %v hits%d", sha, err, hits)
			}
			uncertain := point == "source.settings-parent-sync" || existing
			if errors.Is(err, ErrUncertain) != uncertain {
				t.Fatal("wrong uncertainty", err)
			}
			path := filepath.Join(p.State, "credentials", sourceSettingsName)
			_, statErr := os.Stat(path)
			if uncertain && statErr != nil || !uncertain && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("wrong visibility", statErr)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".source-settings-") {
					t.Fatal("own temporary leaked")
				}
			}
			settingsActiveSame(t, s, initial)
			s.fault = nil
			if uncertain {
				if sha, err = s.PublishSourceSettings(raw); sha != "" || !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned writer reused", err)
				}
				if b, got, err := s.ReadSourceSettings(); b != nil || got != "" || !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned reader returned candidate", err)
				}
			}
			// Every case is closed and reopened, including failures before publication.
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openWriter(t, p)
			if uncertain {
				b, got, err := s.ReadSourceSettings()
				if err != nil || got != digest(raw) || !bytes.Equal(b, raw) {
					t.Fatal("reopen lost visible settings", err)
				}
				if got, err := s.PublishSourceSettings([]byte(`{"replacement":true}`)); got != "" || err != ErrSourceSettingsConflict {
					t.Fatal("reopen replaced original", err)
				}
			} else {
				if b, got, err := s.ReadSourceSettings(); b != nil || got != "" || !errors.Is(err, os.ErrNotExist) {
					t.Fatal("prepublication generated settings", err)
				}
			}
			var seen []string
			s.fault = func(at string) error { seen = append(seen, at); return nil }
			settingsPut(t, s, raw)
			want := []string{"source.settings-file.write", "source.settings-file.sync", "source.settings-file.closed", "source.settings-rename", "source.settings-parent-sync"}
			if uncertain {
				want = []string{"source.settings-existing-file-sync", "source.settings-existing-parent-sync"}
			}
			if !reflect.DeepEqual(seen, want) {
				t.Fatalf("retry barriers %v", seen)
			}
			settingsActiveSame(t, s, initial)
		})
	}
}
func TestSourceSettingsUnsafeOrCorruptNeverRepaired(t *testing.T) {
	for _, kind := range []string{"file-mode", "hardlink", "symlink", "directory-mode", "directory-symlink", "missing-directory", "empty", "invalid-json", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			raw := []byte(`{"original":true}`)
			settingsPut(t, s, raw)
			dir := filepath.Join(p.State, "credentials")
			path := filepath.Join(dir, sourceSettingsName)
			switch kind {
			case "file-mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(dir, "linked")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(p.State, "target")
				settingsWrite(t, target, raw)
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
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
			case "missing-directory":
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			case "empty":
				settingsWrite(t, path, nil)
			case "invalid-json":
				settingsWrite(t, path, []byte("not JSON"))
			case "oversize":
				if err := os.Truncate(path, MaxSourceSettingsBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, p.State)
			hits := 0
			s.fault = func(string) error { hits++; return nil }
			if b, sha, err := s.ReadSourceSettings(); b != nil || sha != "" || err == nil {
				t.Fatal("unsafe/corrupt read accepted")
			}
			if sha, err := s.PublishSourceSettings(raw); sha != "" || err == nil || errors.Is(err, ErrSourceSettingsConflict) {
				t.Fatal("unsafe/corrupt treated as ordinary conflict", err)
			}
			if hits != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("unsafe/corrupt state repaired")
			}
		})
	}
}
