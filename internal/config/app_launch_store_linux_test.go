//go:build linux && amd64

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Metadata-only fixture: these numbers are not live process evidence. No test
// in this file signals, adopts or waits on the synthetic child PID.
func appLaunchStoreFixture(t *testing.T) (Paths, *Store, AppLaunchInput) {
	t.Helper()
	p := storePaths(t)
	s := openWriter(t, p)
	if err := s.InitializeLaunchHistory(); err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := appLaunchTypesFixture()
	r.Wrapper.PID = os.Getpid()
	r.Wrapper.UID = uint32(os.Geteuid())
	r.Wrapper.PPID = os.Getppid()
	return p, s, AppLaunchInput{Profile: r.Profile, Mode: r.Mode, EntryBindingSHA256: r.EntryBindingSHA256, Wrapper: r.Wrapper}
}
func appLaunchStoreReserve(t *testing.T, p Paths, in AppLaunchInput) *AppLaunchWriter {
	t.Helper()
	w, err := ReserveAppLaunch(context.Background(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}
func appLaunchStoreBirth(t *testing.T, w *AppLaunchWriter) AppLaunchReservation {
	t.Helper()
	r, err := w.TakeSpawn()
	if err != nil {
		t.Fatal(err)
	}
	b := r.Wrapper
	b.PID += 10000
	b.PPID = r.Wrapper.PID
	b.ProcessGroup = b.PID
	b.StartTicks++
	if err = w.RecordBirth(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAppLaunchStoreSeparateAuthorityAndImmutablePrefix(t *testing.T) {
	p, store, input := appLaunchStoreFixture(t)
	active, _ := store.ReadActive()
	// The original configuration writer remains held throughout allocation,
	// append and read; the independently scoped launch namespace must still work.
	w := appLaunchStoreReserve(t, p, input)
	*input.Profile.Generation = "caller-change"
	*input.Profile.ManifestSHA256 = strings.Repeat("f", 64)
	copy := *w
	r := appLaunchStoreBirth(t, w)
	if r.Profile.Generation == nil || *r.Profile.Generation != "generation-a" || *r.Profile.ManifestSHA256 != strings.Repeat("d", 64) {
		t.Fatal("reservation retained caller pointers")
	}
	if _, err := copy.TakeSpawn(); !errors.Is(err, ErrAppLaunchConflict) {
		t.Fatal("copied writer renewed spawn ticket", err)
	}
	r.Profile.App.Bypass = append(r.Profile.App.Bypass, "not-original")
	if err := w.Authorize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Authorize(context.Background()); !errors.Is(err, ErrAppLaunchConflict) {
		t.Fatal("authorization replay", err)
	}
	before := tree(t, p.State)
	rows, err := ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 1 || rows[0].Birth == nil || rows[0].Authorization == nil || rows[0].Terminal != nil || len(rows[0].Reservation.Profile.App.Bypass) != 0 {
		t.Fatal("prefix", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("read mutated store")
	}
	if err := w.RecordTerminal(context.Background(), AppLaunchResult{ExitCode: appLaunchInt(130), WaitStatus: 130 << 8}); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordTerminal(context.Background(), AppLaunchResult{Signal: appLaunchInt(2), WaitStatus: 2}); !errors.Is(err, ErrAppLaunchConflict) {
		t.Fatal("terminal replaced", err)
	}
	rows, err = ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 1 || rows[0].Terminal.Result.ExitCode == nil || *rows[0].Terminal.Result.ExitCode != 130 || rows[0].Terminal.Result.Signal != nil {
		t.Fatal(err)
	}
	rows[0].Reservation.Profile.App.ID = "changed"
	rows[0].Terminal.Result.ExitCode = appLaunchInt(99)
	rows, err = ReadAppLaunchRecords(context.Background(), p)
	if err != nil || rows[0].Reservation.Profile.App.ID != "app-a" || *rows[0].Terminal.Result.ExitCode != 130 {
		t.Fatal("reader alias", err)
	}
	settingsActiveSame(t, store, active)
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordBirth(context.Background(), input.Wrapper); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := store.InitializeLaunchHistory(); err != nil {
		t.Fatal("init contended with launch owner", err)
	}
	rows, err = ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
}

func TestAppLaunchStoreRestrictiveUmaskOwnNamesOnly(t *testing.T) {
	const marker = "HOOPA_T056_LAUNCH_UMASK_CHILD"
	if os.Getenv(marker) != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAppLaunchStoreRestrictiveUmaskOwnNamesOnly$", "-test.v")
		cmd.Env = append(os.Environ(), marker+"=1")
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask: %v %s", err, raw)
		}
		return
	}
	p, _, input := appLaunchStoreFixture(t)
	identity, err := os.Stat(filepath.Join(p.State, launchHistoryDirectory, launchHistoryIdentity))
	if err != nil {
		t.Fatal(err)
	}
	prior := syscall.Umask(0777)
	defer syscall.Umask(prior)
	w := appLaunchStoreReserve(t, p, input)
	appLaunchStoreBirth(t, w)
	if err = w.Authorize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = w.RecordTerminal(context.Background(), AppLaunchResult{ExitCode: appLaunchInt(0)}); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 1 || rows[0].Terminal == nil {
		t.Fatal("new owned names not private/readable", err)
	}
	current, err := os.Stat(filepath.Join(p.State, launchHistoryDirectory, launchHistoryIdentity))
	if err != nil || !os.SameFile(identity, current) || identity.Mode() != current.Mode() || identity.ModTime() != current.ModTime() {
		t.Fatal("existing namespace metadata modified", err)
	}
}

func TestAppLaunchStoreReadAggregateBound(t *testing.T) {
	p, _, input := appLaunchStoreFixture(t)
	w := appLaunchStoreReserve(t, p, input)
	r := appLaunchStoreBirth(t, w)
	dir, _, err := openAppLaunchNamespace(p)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	child, err := openPrivateDir(dir, string(r.LaunchID), false)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	remaining := 1
	if _, err = readAppLaunchRecord(context.Background(), child, r.HistoryID, r.LaunchID, &remaining); !errors.Is(err, ErrAppLaunchCapacity) {
		t.Fatal("aggregate read bound ignored", err)
	}
}

func TestAppLaunchStoreMissingAndCancelledNeverInitialize(t *testing.T) {
	p := storePaths(t)
	before := tree(t, filepath.Dir(filepath.Dir(p.State)))
	if _, err := ReadAppLaunchRecords(context.Background(), p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, filepath.Dir(filepath.Dir(p.State)))) {
		t.Fatal("read created state")
	}
	p, _, input := appLaunchStoreFixture(t)
	before = tree(t, p.State)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReserveAppLaunch(ctx, p, input); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := ReadAppLaunchRecords(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	input.Wrapper.PID++
	if _, err := ReserveAppLaunch(context.Background(), p, input); !errors.Is(err, ErrAppLaunch) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("invalid/cancelled allocated")
	}
}

func TestAppLaunchStoreLockCancellationAndNoOwnerLockReads(t *testing.T) {
	p, _, input := appLaunchStoreFixture(t)
	w := appLaunchStoreReserve(t, p, input)
	dir, _, err := openAppLaunchNamespace(p)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	lock, err := appLaunchLock(context.Background(), dir, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		start := time.Now()
		if read {
			_, err = ReadAppLaunchRecords(ctx, p)
		} else {
			_, err = ReserveAppLaunch(ctx, p, input)
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
			t.Fatal("lock wait was not bounded/cancelled", err)
		}
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if late, err := appLaunchLockUntil(context.Background(), dir, false, time.Now().Add(-time.Second)); late != nil || !errors.Is(err, ErrAppLaunchBusy) {
		if late != nil {
			late.Close()
		}
		t.Fatal("expired lock attempt succeeded", err)
	}
	// The wrapper's original owner flock stays held. Inventory must not take it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rows, err := ReadAppLaunchRecords(ctx, p)
	if err != nil || len(rows) != 1 {
		t.Fatal("reader waited on owner", err)
	}
	if w.state.closed {
		t.Fatal("reader closed owner")
	}
}

func TestAppLaunchStoreLockHasDefaultBound(t *testing.T) {
	p, _, _ := appLaunchStoreFixture(t)
	dir, _, err := openAppLaunchNamespace(p)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	lock, err := appLaunchLock(context.Background(), dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	start := time.Now()
	late, err := appLaunchLock(context.Background(), dir, false)
	if late != nil {
		late.Close()
		t.Fatal("contended lock granted")
	}
	if !errors.Is(err, ErrAppLaunchBusy) || time.Since(start) < 2*time.Second || time.Since(start) > 5*time.Second {
		t.Fatal("default wait not bounded", err, time.Since(start))
	}
}

func TestAppLaunchStoreConcurrentFreshIDsAndReads(t *testing.T) {
	p, store, input := appLaunchStoreFixture(t)
	active, _ := store.ReadActive()
	var wg sync.WaitGroup
	errs := make(chan error, 13)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := ReserveAppLaunch(context.Background(), p, input)
			if err != nil {
				errs <- err
				return
			}
			defer w.Close()
			r, err := w.TakeSpawn()
			if err != nil {
				errs <- err
				return
			}
			b := r.Wrapper
			b.PID += 10000
			b.PPID = r.Wrapper.PID
			b.ProcessGroup = b.PID
			b.StartTicks++
			if err = w.RecordBirth(context.Background(), b); err == nil {
				err = w.Authorize(context.Background())
			}
			if err == nil {
				err = w.RecordTerminal(context.Background(), AppLaunchResult{ExitCode: appLaunchInt(0), WaitStatus: 0})
			}
			errs <- err
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			rows, err := ReadAppLaunchRecords(context.Background(), p)
			if err != nil {
				errs <- err
				return
			}
			for _, r := range rows {
				if r.Terminal != nil && (r.Birth == nil || r.Authorization == nil) {
					errs <- ErrAppLaunch
					return
				}
			}
		}
		errs <- nil
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 12 {
		t.Fatal("lost concurrent launch", len(rows), err)
	}
	seen := map[ID]bool{}
	for _, r := range rows {
		if seen[r.Reservation.LaunchID] || r.Terminal == nil {
			t.Fatal("reused/incomplete launch")
		}
		seen[r.Reservation.LaunchID] = true
	}
	settingsActiveSame(t, store, active)
}

func TestAppLaunchStoreAllocationFaultsNeverGrantSpawn(t *testing.T) {
	points := []string{"app.launch-owner-sync", "app.launch-reservation.write", "app.launch-reservation.sync", "app.launch-reservation.closed", "app.launch-stage-sync", "app.launch-publish", "app.launch-namespace-sync"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			p, _, input := appLaunchStoreFixture(t)
			injected := errors.New("allocation boundary")
			hits := 0
			writes := &Store{fault: func(at string) error {
				if at == point {
					hits++
					return injected
				}
				return nil
			}}
			w, err := reserveAppLaunch(context.Background(), p, input, writes)
			if w != nil || !errors.Is(err, injected) || hits != 1 {
				t.Fatal("fault returned a spawn ticket", w, err, hits)
			}
			entries, err := os.ReadDir(filepath.Join(p.State, launchHistoryDirectory))
			if err != nil {
				t.Fatal(err)
			}
			staging, final := 0, 0
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".stage-") {
					staging++
				}
				if strings.HasPrefix(e.Name(), "launch-") {
					final++
				}
			}
			if point == "app.launch-namespace-sync" {
				if final != 1 || staging != 0 {
					t.Fatal("uncertain final evidence lost")
				}
			} else if staging != 1 || final != 0 {
				t.Fatal("failed staging evidence discarded")
			}
			// Explicit new allocation has a new ID; it never resumes the retained one.
			w = appLaunchStoreReserve(t, p, input)
			if _, err = w.TakeSpawn(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppLaunchStoreAppendFailurePoisonsAndRetainsEvidence(t *testing.T) {
	for _, stage := range []string{"birth.json", "authorization.json", "terminal.json"} {
		for _, suffix := range []string{".write", ".sync", ".closed", "-publish", "-parent-sync"} {
			t.Run(stage+suffix, func(t *testing.T) {
				p, _, input := appLaunchStoreFixture(t)
				w := appLaunchStoreReserve(t, p, input)
				r, err := w.TakeSpawn()
				if err != nil {
					t.Fatal(err)
				}
				child := r.Wrapper
				child.PID += 10000
				child.PPID = r.Wrapper.PID
				child.ProcessGroup = child.PID
				child.StartTicks++
				if stage != "birth.json" {
					if err = w.RecordBirth(context.Background(), child); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "terminal.json" {
					if err = w.Authorize(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				point := "app.launch-" + stage + suffix
				injected := errors.New("append boundary")
				hits := 0
				w.state.writes.fault = func(at string) error {
					if at == point {
						hits++
						return injected
					}
					return nil
				}
				switch stage {
				case "birth.json":
					err = w.RecordBirth(context.Background(), child)
				case "authorization.json":
					err = w.Authorize(context.Background())
				case "terminal.json":
					err = w.RecordTerminal(context.Background(), AppLaunchResult{Signal: appLaunchInt(2), WaitStatus: 2})
				}
				if !errors.Is(err, injected) || !errors.Is(err, ErrUncertain) || hits != 1 {
					t.Fatal("failure not uncertain", err, hits)
				}
				if err = w.Authorize(context.Background()); !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned authorization reused", err)
				}
				w.Close()
				rows, err := ReadAppLaunchRecords(context.Background(), p)
				if err != nil || len(rows) != 1 {
					t.Fatal("valid retained prefix unreadable", err)
				}
				has := false
				switch stage {
				case "birth.json":
					has = rows[0].Birth != nil
				case "authorization.json":
					has = rows[0].Authorization != nil
				case "terminal.json":
					has = rows[0].Terminal != nil
				}
				if has != (suffix == "-parent-sync") {
					t.Fatal("reader fabricated or hid visible original")
				}
			})
		}
	}
}

func TestAppLaunchStoreCorruptionFailsWholeInventory(t *testing.T) {
	for _, kind := range []string{"unknown-namespace", "unsafe-directory", "missing-owner", "corrupt-reservation", "missing-birth", "bad-terminal-ref", "unknown-record", "unsafe-temp", "large-temp", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			p, _, input := appLaunchStoreFixture(t)
			w := appLaunchStoreReserve(t, p, input)
			r := appLaunchStoreBirth(t, w)
			if err := w.Authorize(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := w.RecordTerminal(context.Background(), AppLaunchResult{ExitCode: appLaunchInt(17), WaitStatus: 17 << 8}); err != nil {
				t.Fatal(err)
			}
			w.Close()
			// Another valid row cannot make a partial result an acceptable success.
			other := appLaunchStoreReserve(t, p, input)
			other.Close()
			dir := filepath.Join(p.State, launchHistoryDirectory, string(r.LaunchID))
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "unknown-namespace":
				must(os.WriteFile(filepath.Join(p.State, launchHistoryDirectory, "unknown"), nil, 0600))
			case "unsafe-directory":
				must(os.Chmod(dir, 0755))
			case "missing-owner":
				must(os.Remove(filepath.Join(dir, ".owner.lock")))
			case "corrupt-reservation":
				must(os.WriteFile(filepath.Join(dir, "reservation.json"), []byte("{}\n"), 0600))
			case "missing-birth":
				must(os.Remove(filepath.Join(dir, "birth.json")))
			case "bad-terminal-ref":
				raw, e := os.ReadFile(filepath.Join(dir, "terminal.json"))
				must(e)
				var v AppLaunchTerminal
				must(decode(raw, &v))
				v.BirthSHA256 = strings.Repeat("f", 64)
				raw, e = encode(v)
				must(e)
				must(os.WriteFile(filepath.Join(dir, "terminal.json"), raw, 0600))
			case "unknown-record":
				must(os.WriteFile(filepath.Join(dir, "surprise"), nil, 0600))
			case "unsafe-temp":
				must(os.WriteFile(filepath.Join(dir, ".tmp-"+strings.Repeat("a", 48)), nil, 0644))
			case "large-temp":
				must(os.WriteFile(filepath.Join(dir, ".tmp-"+strings.Repeat("b", 48)), make([]byte, MaxAppLaunchRecordBytes+1), 0600))
			case "hardlink":
				must(os.Link(filepath.Join(dir, "birth.json"), filepath.Join(dir, ".tmp-"+strings.Repeat("c", 48))))
			}
			before := tree(t, p.State)
			rows, err := ReadAppLaunchRecords(context.Background(), p)
			if err == nil || rows != nil {
				t.Fatal("corrupt inventory returned partial rows", len(rows), err)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("reader repaired corruption")
			}
		})
	}
}

func TestAppLaunchStoreStagingCapacityAndNoReplace(t *testing.T) {
	p, _, input := appLaunchStoreFixture(t)
	namespace := filepath.Join(p.State, launchHistoryDirectory)
	for i := 0; i < MaxAppLaunches; i++ {
		if err := os.Mkdir(filepath.Join(namespace, fmt.Sprintf(".stage-%048x", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Interiors of unpublished staging remain uninterpreted evidence. They
	// cannot appear as a launch, authorize execution, or be followed/removed.
	stagedExtra := filepath.Join(namespace, fmt.Sprintf(".stage-%048x", 0), "unaccepted-internal")
	if err := os.WriteFile(stagedExtra, []byte("not a launch original"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveAppLaunch(context.Background(), p, input); !errors.Is(err, ErrAppLaunchCapacity) {
		t.Fatal("retained staging not counted", err)
	}
	rows, err := ReadAppLaunchRecords(context.Background(), p)
	if err != nil || len(rows) != 0 {
		t.Fatal("staging treated as launched", err)
	}
	dir, _, err := openAppLaunchNamespace(p)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := appLaunchRename(dir, fmt.Sprintf(".stage-%048x", 0), fmt.Sprintf(".stage-%048x", 1)); !errors.Is(err, syscall.EEXIST) {
		t.Fatal("rename replaced retained identity", err)
	}
	entries, err := os.ReadDir(namespace)
	if err != nil || len(entries) != MaxAppLaunches+2 {
		t.Fatal("capacity silently pruned", len(entries), err)
	}
	if raw, err := os.ReadFile(stagedExtra); err != nil || string(raw) != "not a launch original" {
		t.Fatal("staging evidence changed", err)
	}
}
