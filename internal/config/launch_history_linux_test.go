//go:build linux

package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestLaunchHistoryExplicitInstallationAndReadOnly(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	active, _ := s.ReadActive()
	before := tree(t, p.State)
	if _, err := s.ReadLaunchHistoryID(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := s.CheckLaunchHistoryInitialization(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read created optional namespace")
	}
	if err := s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	id, err := s.ReadLaunchHistoryID()
	if err != nil || !strings.HasPrefix(id, "apphist-") {
		t.Fatal(id, err)
	}
	settingsActiveSame(t, s, active)
	before = tree(t, p.State)
	if err = s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("retry changed bytes/names")
	}
	s.Close()
	r, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.fault = func(string) error { t.Error("readonly sync"); return errors.New("sync") }
	if got, err := r.ReadLaunchHistoryID(); err != nil || got != id {
		t.Fatal(got, err)
	}
	if err := r.CheckLaunchHistoryInitialization(); err != nil {
		t.Fatal(err)
	}
	if err := r.InitializeLaunchHistory(); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("readonly mutated")
	}
}

func TestLaunchHistoryPartialInstallationReusesIdentity(t *testing.T) {
	for _, withLock := range []bool{false, true} {
		t.Run(map[bool]string{false: "identity-only", true: "identity-lock"}[withLock], func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			if err := s.InitializeLaunchHistory(); err != nil {
				t.Fatal(err)
			}
			id, _ := s.ReadLaunchHistoryID()
			if err := os.Remove(filepath.Join(p.State, "credentials", launchHistoryMarker)); err != nil {
				t.Fatal(err)
			}
			if !withLock {
				if err := os.Remove(filepath.Join(p.State, launchHistoryDirectory, launchAllocationLock)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ReadLaunchHistoryID(); err == nil {
				t.Fatal("unpublished identity accepted")
			}
			if err := s.InitializeLaunchHistory(); err != nil {
				t.Fatal(err)
			}
			if got, err := s.ReadLaunchHistoryID(); err != nil || got != id {
				t.Fatal("identity replaced", got, err)
			}
		})
	}
}

func TestLaunchHistoryLostUnsafeOrAmbiguousNeverReset(t *testing.T) {
	for _, kind := range []string{"missing-directory", "missing-identity", "missing-lock", "mismatch", "identity-mode", "hardlink", "symlink", "fifo", "directory-mode", "unmarked-launch", "unmarked-stage", "unmarked-extra", "unmarked-empty"} {
		t.Run(kind, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			if err := s.InitializeLaunchHistory(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(p.State, launchHistoryDirectory)
			identity := filepath.Join(dir, launchHistoryIdentity)
			marker := filepath.Join(p.State, "credentials", launchHistoryMarker)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "missing-directory":
				must(os.RemoveAll(dir))
			case "missing-identity":
				must(os.Remove(identity))
			case "missing-lock":
				must(os.Remove(filepath.Join(dir, launchAllocationLock)))
			case "mismatch":
				raw, _ := encode(launchHistoryIdentityRecord{1, "apphist-" + strings.Repeat("f", 48)})
				must(os.WriteFile(identity, raw, 0600))
			case "identity-mode":
				must(os.Chmod(identity, 0644))
			case "hardlink":
				must(os.Link(identity, filepath.Join(dir, "linked")))
			case "symlink":
				must(os.Remove(identity))
				must(os.Symlink(marker, identity))
			case "fifo":
				must(os.Remove(identity))
				must(syscall.Mkfifo(identity, 0600))
			case "directory-mode":
				must(os.Chmod(dir, 0755))
			default:
				must(os.Remove(marker))
				switch kind {
				case "unmarked-launch":
					must(os.Mkdir(filepath.Join(dir, "launch-"+strings.Repeat("a", 48)), 0700))
				case "unmarked-stage":
					must(os.Mkdir(filepath.Join(dir, ".stage-"+strings.Repeat("b", 48)), 0700))
				case "unmarked-extra":
					must(os.WriteFile(filepath.Join(dir, "unknown"), nil, 0600))
				case "unmarked-empty":
					must(os.Remove(identity))
					must(os.Remove(filepath.Join(dir, launchAllocationLock)))
				}
			}
			// tree does not read special files; it preserves names/types/modes.
			before := tree(t, p.State)
			calls := 0
			s.fault = func(string) error { calls++; return nil }
			if err := s.InitializeLaunchHistory(); err == nil {
				t.Fatal("lost/unsafe/ambiguous namespace reset")
			}
			if calls != 0 || !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("failed validation wrote")
			}
		})
	}
}

func TestLaunchHistoryPublicationOrderAndFaultRecovery(t *testing.T) {
	points := []string{"launch.history-identity.write", "launch.history-identity.sync", "launch.history-identity.closed", "launch.history-lock.write", "launch.history-lock.sync", "launch.history-lock.closed", "launch.history-identity-sync", "launch.history-lock-sync", "launch.history-directory-sync", "launch.history-state-sync", "launch.history-marker.write", "launch.history-marker.sync", "launch.history-marker.closed", "launch.history-marker-rename", "launch.history-credentials-sync"}
	p := storePaths(t)
	s := openWriter(t, p)
	var seen []string
	s.fault = func(point string) error { seen = append(seen, point); return nil }
	if err := s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, points) {
		t.Fatal("publication ordering", seen)
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			injected := errors.New("boundary")
			hits := 0
			s.fault = func(at string) error {
				if at == point {
					hits++
					return injected
				}
				return nil
			}
			err := s.InitializeLaunchHistory()
			if !errors.Is(err, injected) || hits != 1 {
				t.Fatal("fault not hit", err, hits)
			}
			if point != "launch.history-marker-rename" && !errors.Is(err, ErrUncertain) {
				t.Fatal("partial sync/write not uncertain", err)
			}
			if errors.Is(err, ErrUncertain) {
				s.fault = nil
				if err = s.InitializeLaunchHistory(); !errors.Is(err, ErrUncertain) {
					t.Fatal("poison reused", err)
				}
			}
			s.Close()
			s = openWriter(t, p)
			err = s.InitializeLaunchHistory()
			if point == "launch.history-identity.write" {
				if err == nil {
					t.Fatal("malformed partial identity repaired")
				}
				return
			}
			if err != nil {
				t.Fatal("recognized preparation could not finish", err)
			}
			if _, err = s.ReadLaunchHistoryID(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLaunchHistoryRetryDoesNotInspectOrLockOwners(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	if err := s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	// Once installed, metadata-only init neither repairs nor interprets stages.
	path := filepath.Join(p.State, launchHistoryDirectory, "launch-"+strings.Repeat("c", 48))
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(path, ".owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.State)
	if err = s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("init changed launch")
	}
}
