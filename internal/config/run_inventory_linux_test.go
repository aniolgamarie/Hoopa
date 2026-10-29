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

func TestRunInventoryDiscoveryIsReadOnly(t *testing.T) {
	p, s, intent, _ := runStoreFixture(t)
	if ids, err := s.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatal("empty inventory", ids, err)
	}
	for _, id := range []RunID{"z-run", "a-run"} {
		intent.RunID = id
		if _, err := s.PrepareRun(intent); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, p.State)
	ids, err := s.ListRunIDs()
	if err != nil || !reflect.DeepEqual(ids, []RunID{"a-run", "z-run"}) {
		t.Fatal("discover unpublished-birth intents", ids, err)
	}
	ids[0] = "caller-mutation"
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := reader.ListRunIDs()
	if err != nil || !reflect.DeepEqual(got, []RunID{"a-run", "z-run"}) {
		t.Fatal("readonly detached discovery", got, err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("discovery changed records")
	}
	if _, err := s.ListRunIDs(); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store", err)
	}
}

func TestRunInventoryRefusesIncompleteAuthority(t *testing.T) {
	for _, scenario := range []string{"lost-root", "unknown-name", "staging", "staging-symlink", "run-symlink", "run-permissions", "missing-intent", "intent-permissions", "intent-symlink", "intent-hardlink", "intent-oversize", "intent-corrupt", "directory-binding"} {
		t.Run(scenario, func(t *testing.T) {
			p, s, intent, _ := runStoreFixture(t)
			if _, err := s.PrepareRun(intent); err != nil {
				t.Fatal(err)
			}
			runs := filepath.Join(p.State, "runs")
			name, _ := runDirectory(intent.RunID)
			dir := filepath.Join(runs, name)
			file := filepath.Join(dir, "intent.json")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "lost-root":
				must(os.RemoveAll(runs))
			case "unknown-name":
				must(os.Mkdir(filepath.Join(runs, "unknown"), 0700))
			case "staging":
				must(os.Mkdir(filepath.Join(runs, ".run-"+strings.Repeat("a", 48)), 0700))
			case "staging-symlink":
				must(os.Symlink(dir, filepath.Join(runs, ".run-"+strings.Repeat("a", 48))))
			case "run-symlink":
				saved := filepath.Join(p.State, "saved-run")
				must(os.Rename(dir, saved))
				must(os.Symlink(saved, dir))
			case "run-permissions":
				must(os.Chmod(dir, 0755))
			case "missing-intent":
				must(os.Remove(file))
			case "intent-permissions":
				must(os.Chmod(file, 0644))
			case "intent-symlink":
				must(os.Rename(file, file+"-saved"))
				must(os.Symlink(file+"-saved", file))
			case "intent-hardlink":
				must(os.Link(file, filepath.Join(p.State, "intent-link")))
			case "intent-oversize":
				must(os.WriteFile(file, []byte(strings.Repeat(" ", MaxRunRecordBytes+1)), 0600))
			case "intent-corrupt":
				must(os.WriteFile(file, []byte("{}\n"), 0600))
			case "directory-binding":
				intent.RunID = "different-run"
				raw, err := encode(intent)
				must(err)
				must(os.WriteFile(file, raw, 0600))
			}
			before := tree(t, p.State)
			ids, err := s.ListRunIDs()
			if err == nil || ids != nil {
				t.Fatal("partial or incomplete inventory accepted", ids, err)
			}
			if scenario == "staging" && !errors.Is(err, ErrRunStaging) {
				t.Fatal("staging not distinguished", err)
			}
			if scenario == "lost-root" && !errors.Is(err, ErrReadUnavailable) {
				t.Fatal("lost root not unavailable", err)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("inventory repaired or removed evidence")
			}
		})
	}
}

func TestRunInventoryBoundAndUncertainWriter(t *testing.T) {
	t.Run("exact-bound", func(t *testing.T) {
		p, s, intent, _ := runStoreFixture(t)
		// Actual bounded directories with valid synthetic intent originals; no
		// launch/birth/runtime facts are inferred from these storage fixtures.
		for i := 0; i < MaxRuns; i++ {
			intent.RunID = RunID("run-" + digest([]byte{byte(i >> 8), byte(i)}))
			name, _ := runDirectory(intent.RunID)
			dir := filepath.Join(p.State, "runs", name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			raw, err := encode(intent)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "intent.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		before := tree(t, p.State)
		ids, err := s.ListRunIDs()
		if err != nil || len(ids) != MaxRuns {
			t.Fatal("exact bound", len(ids), err)
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("exact read mutated")
		}
		if err := os.Mkdir(filepath.Join(p.State, "runs", "overflow"), 0700); err != nil {
			t.Fatal(err)
		}
		before = tree(t, p.State)
		if ids, err := s.ListRunIDs(); err == nil || ids != nil {
			t.Fatal("over bound", ids, err)
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("over read mutated")
		}
	})
	t.Run("uncertain-publication", func(t *testing.T) {
		p, s, intent, _ := runStoreFixture(t)
		s.fault = func(point string) error {
			if point == "run.parent-sync" {
				return errors.New("injected sync failure")
			}
			return nil
		}
		if ticket, err := s.PrepareRun(intent); ticket != nil || !errors.Is(err, ErrUncertain) {
			t.Fatal("fault did not publish uncertain intent", err)
		}
		s.fault = nil
		before := tree(t, p.State)
		if ids, err := s.ListRunIDs(); ids != nil || !errors.Is(err, ErrUncertain) {
			t.Fatal("poison ignored", ids, err)
		}
		r, err := OpenStore(p, false)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		ids, err := r.ListRunIDs()
		if err != nil || !reflect.DeepEqual(ids, []RunID{intent.RunID}) {
			t.Fatal("readonly observation", ids, err)
		}
		if !reflect.DeepEqual(before, tree(t, p.State)) {
			t.Fatal("uncertain observation wrote")
		}
	})
}
