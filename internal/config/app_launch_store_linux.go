//go:build linux && amd64

package config

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type AppLaunchInput struct {
	Profile            AppLaunchProfile
	Mode               string
	Metering           bool
	EntryBindingSHA256 *string
	Wrapper            AppLaunchProcess
}

// AppLaunchWriter has authority over only its one fresh directory. Copies share
// the mutex, one-use ticket and lifetime; no configuration Store is exposed.
type AppLaunchWriter struct{ state *appLaunchWriterState }
type appLaunchWriterState struct {
	mu               sync.Mutex
	dir              *os.Root
	owner            *os.File
	writes           *Store
	closed           bool
	taken            bool
	reservation      AppLaunchReservation
	reservationSHA   string
	birth            *AppLaunchBirth
	birthSHA         string
	authorization    *AppLaunchAuthorization
	authorizationSHA string
	terminal         bool
}

func openAppLaunchNamespace(paths Paths) (*os.Root, string, error) {
	if err := paths.Validate(); err != nil {
		return nil, "", err
	}
	state, err := privateRoot(paths.State, false)
	if err != nil {
		return nil, "", errors.Join(ErrReadUnavailable, err)
	}
	reader := &Store{root: state}
	id, err := reader.readLaunchHistory()
	if err != nil {
		state.Close()
		return nil, "", err
	}
	dir, err := openPrivateDir(state, launchHistoryDirectory, false)
	err = errors.Join(err, state.Close())
	if err != nil {
		if dir != nil {
			dir.Close()
		}
		return nil, "", err
	}
	return dir, id, nil
}

func appLaunchLock(ctx context.Context, dir *os.Root, exclusive bool) (*os.File, error) {
	return appLaunchLockUntil(ctx, dir, exclusive, time.Now().Add(2*time.Second))
}

func appLaunchLockUntil(ctx context.Context, dir *os.Root, exclusive bool, deadline time.Time) (*os.File, error) {
	if ctx == nil {
		return nil, ErrAppLaunch
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !time.Now().Before(deadline) {
		return nil, ErrAppLaunchBusy
	}
	flags := os.O_RDONLY
	if exclusive {
		flags = os.O_RDWR
	}
	f, err := openPrivateFile(dir, launchAllocationLock, flags)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		f.Close()
		return nil, errors.Join(ErrIntegrity, err)
	}
	mode := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		mode = syscall.LOCK_EX | syscall.LOCK_NB
	}
	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, ErrAppLaunchBusy
		}
		err = syscall.Flock(int(f.Fd()), mode)
		if err == nil {
			if err = ctx.Err(); err != nil {
				f.Close()
				return nil, err
			}
			if !time.Now().Before(deadline) {
				f.Close()
				return nil, ErrAppLaunchBusy
			}
			return f, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, ErrAppLaunchBusy
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func appLaunchInventory(ctx context.Context, dir *os.Root) ([]ID, int, error) {
	entries, err := boundedEntries(dir, 8192)
	if err != nil {
		return nil, 0, errors.Join(ErrAppLaunch, err)
	}
	ids := []ID{}
	retained := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		name := entry.Name()
		if name == launchHistoryIdentity || name == launchAllocationLock {
			limit := int64(1024)
			if name == launchAllocationLock {
				limit = 0
			}
			if _, err := readBounded(dir, name, limit); err != nil {
				return nil, 0, err
			}
			continue
		}
		final := appLaunchToken(name, "launch-")
		if !final && !appLaunchToken(name, ".stage-") {
			return nil, 0, ErrAppLaunch
		}
		child, err := openPrivateDir(dir, name, false)
		if err != nil {
			return nil, 0, err
		}
		if err = child.Close(); err != nil {
			return nil, 0, err
		}
		retained++
		if retained > MaxAppLaunches {
			return nil, 0, ErrAppLaunchCapacity
		}
		if final {
			ids = append(ids, ID(name))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, retained, nil
}

// appLaunchRename uses the existing rooted directory FD and Linux
// RENAME_NOREPLACE. No caller-controlled absolute path or replacing rename is
// used for append/publication. Linux/amd64 syscall number is fixed by this file.
func appLaunchRename(dir *os.Root, old, new string) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	a, err := syscall.BytePtrFromString(old)
	if err != nil {
		return ErrAppLaunch
	}
	b, err := syscall.BytePtrFromString(new)
	if err != nil {
		return ErrAppLaunch
	}
	_, _, errno := syscall.Syscall6(316, f.Fd(), uintptr(unsafe.Pointer(a)), f.Fd(), uintptr(unsafe.Pointer(b)), 1, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// ReserveAppLaunch never acquires the configuration writer and never creates a
// missing namespace. Only successful final publication returns a spawn ticket.
func ReserveAppLaunch(ctx context.Context, paths Paths, input AppLaunchInput) (_ *AppLaunchWriter, err error) {
	return reserveAppLaunch(ctx, paths, input, &Store{})
}

func reserveAppLaunch(ctx context.Context, paths Paths, input AppLaunchInput, writes *Store) (_ *AppLaunchWriter, err error) {
	if ctx == nil {
		return nil, ErrAppLaunch
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if input.Wrapper.PID != os.Getpid() || input.Wrapper.UID != uint32(os.Geteuid()) {
		return nil, ErrAppLaunch
	}
	dir, history, err := openAppLaunchNamespace(paths)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	lock, err := appLaunchLock(ctx, dir, true)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	_, retained, err := appLaunchInventory(ctx, dir)
	if err != nil {
		return nil, err
	}
	if retained >= MaxAppLaunches {
		return nil, ErrAppLaunchCapacity
	}
	var record AppLaunchReservation
	var raw []byte
	var stage string
	created := false
	for attempt := 0; attempt < 8; attempt++ {
		token, e := randomToken()
		if e != nil {
			return nil, e
		}
		record = AppLaunchReservation{SchemaVersion: 1, HistoryID: history, LaunchID: ID("launch-" + token), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Profile: input.Profile, Mode: input.Mode, Metering: input.Metering, EntryBindingSHA256: input.EntryBindingSHA256, Wrapper: input.Wrapper}
		if record.Validate() != nil {
			return nil, ErrAppLaunch
		}
		raw, e = encode(record)
		if e != nil || len(raw) > MaxAppLaunchRecordBytes {
			return nil, ErrAppLaunch
		}
		// The stored value owns every pointer/slice independently of the caller.
		var owned AppLaunchReservation
		if decode(raw, &owned) != nil {
			return nil, ErrAppLaunch
		}
		record = owned
		if _, e = dir.Lstat(string(record.LaunchID)); e == nil {
			continue
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		stage = ".stage-" + token
		if e = dir.Mkdir(stage, 0700); errors.Is(e, os.ErrExist) {
			continue
		} else if e != nil {
			return nil, e
		}
		created = true
		break
	}
	if !created {
		return nil, ErrAppLaunchConflict
	}
	// Only this freshly/exclusively created name is adjusted. A restrictive
	// umask must not leave an unsafe retained directory that poisons inventory.
	if err = dir.Chmod(stage, 0700); err != nil {
		return nil, errors.Join(ErrUncertain, err)
	}
	child, err := openPrivateDir(dir, stage, false)
	if err != nil {
		return nil, errors.Join(ErrUncertain, err)
	}
	var owner *os.File
	success := false
	defer func() {
		if !success {
			if owner != nil {
				owner.Close()
			}
			child.Close()
		}
	}()
	owner, err = child.OpenFile(".owner.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, writes.uncertain(err)
	}
	if err = owner.Chmod(0600); err != nil {
		return nil, writes.uncertain(err)
	}
	info, err := owner.Stat()
	if err != nil {
		return nil, writes.uncertain(err)
	}
	if err = privateInfo(info, false); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = writes.checkpoint("app.launch-owner-sync"); err == nil {
		err = owner.Sync()
	}
	if err != nil {
		return nil, writes.uncertain(err)
	}
	if err = writeAppLaunchFile(writes, child, "reservation.json", raw, "app.launch-reservation"); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = checkPrivateInputFile(child, "reservation.json"); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = writes.syncDir(child, "app.launch-stage-sync"); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = writes.checkpoint("app.launch-publish"); err != nil {
		return nil, err
	}
	if err = appLaunchRename(dir, stage, string(record.LaunchID)); err != nil {
		return nil, writes.uncertain(err)
	}
	if err = writes.syncDir(dir, "app.launch-namespace-sync"); err != nil {
		return nil, writes.uncertain(err)
	}
	// Explicitly release allocation before returning any per-launch authority.
	// Close is idempotent at the OS-file wrapper; deferred Close is harmless.
	if err = lock.Close(); err != nil {
		return nil, writes.uncertain(err)
	}
	success = true
	return &AppLaunchWriter{state: &appLaunchWriterState{dir: child, owner: owner, writes: writes, reservation: record, reservationSHA: digest(raw)}}, nil
}

func (w *AppLaunchWriter) Close() error {
	if w == nil || w.state == nil {
		return nil
	}
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.owner.Close(), s.dir.Close())
}
func (w *AppLaunchWriter) TakeSpawn() (AppLaunchReservation, error) {
	if w == nil || w.state == nil {
		return AppLaunchReservation{}, ErrAppLaunch
	}
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return AppLaunchReservation{}, err
	}
	if s.taken {
		return AppLaunchReservation{}, ErrAppLaunchConflict
	}
	s.taken = true
	raw, err := encode(s.reservation)
	if err != nil {
		return AppLaunchReservation{}, ErrAppLaunch
	}
	var out AppLaunchReservation
	if err = decode(raw, &out); err != nil {
		return AppLaunchReservation{}, err
	}
	return out, nil
}
func (s *appLaunchWriterState) check() error {
	if s.closed {
		return ErrClosed
	}
	if s.writes.poisoned {
		return ErrUncertain
	}
	return nil
}
func (s *appLaunchWriterState) append(ctx context.Context, name string, value any) (string, error) {
	if ctx == nil {
		return "", ErrAppLaunch
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.check(); err != nil {
		return "", err
	}
	raw, err := encode(value)
	if err != nil || len(raw) > MaxAppLaunchRecordBytes {
		return "", ErrAppLaunch
	}
	if _, err = s.dir.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(ErrAppLaunchConflict, err)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".tmp-" + token
	if err = writeAppLaunchFile(s.writes, s.dir, tmp, raw, "app.launch-"+name); err != nil {
		return "", s.writes.uncertain(err)
	}
	if err = checkPrivateInputFile(s.dir, tmp); err != nil {
		return "", s.writes.uncertain(err)
	}
	if err = s.writes.checkpoint("app.launch-" + name + "-publish"); err != nil {
		return "", s.writes.uncertain(err)
	}
	if err = appLaunchRename(s.dir, tmp, name); err != nil {
		return "", s.writes.uncertain(err)
	}
	if err = s.writes.syncDir(s.dir, "app.launch-"+name+"-parent-sync"); err != nil {
		return "", s.writes.uncertain(err)
	}
	return digest(raw), nil
}
func (w *AppLaunchWriter) RecordBirth(ctx context.Context, child AppLaunchProcess) error {
	if w == nil || w.state == nil {
		return ErrAppLaunch
	}
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if !s.taken || s.birth != nil || s.terminal {
		return ErrAppLaunchConflict
	}
	r := s.reservation
	b := AppLaunchBirth{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ReservationSHA256: s.reservationSHA, Child: child}
	if b.Match(r, s.reservationSHA) != nil {
		return ErrAppLaunch
	}
	sha, err := s.append(ctx, "birth.json", b)
	if err != nil {
		return err
	}
	s.birth, s.birthSHA = &b, sha
	return nil
}
func (w *AppLaunchWriter) Authorize(ctx context.Context) error {
	if w == nil || w.state == nil {
		return ErrAppLaunch
	}
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.birth == nil || s.authorization != nil || s.terminal {
		return ErrAppLaunchConflict
	}
	r := s.reservation
	a := AppLaunchAuthorization{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ReservationSHA256: s.reservationSHA, BirthSHA256: s.birthSHA}
	if a.Match(r, s.reservationSHA, *s.birth, s.birthSHA) != nil {
		return ErrAppLaunch
	}
	sha, err := s.append(ctx, "authorization.json", a)
	if err != nil {
		return err
	}
	s.authorization, s.authorizationSHA = &a, sha
	return nil
}
func (w *AppLaunchWriter) RecordTerminal(ctx context.Context, result AppLaunchResult) error {
	if w == nil || w.state == nil {
		return ErrAppLaunch
	}
	s := w.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.birth == nil || s.terminal {
		return ErrAppLaunchConflict
	}
	r := s.reservation
	var auth *string
	if s.authorization != nil {
		sha := s.authorizationSHA
		auth = &sha
	}
	t := AppLaunchTerminal{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ReservationSHA256: s.reservationSHA, BirthSHA256: s.birthSHA, AuthorizationSHA256: auth, Result: result}
	if t.Match(r, s.reservationSHA, *s.birth, s.birthSHA, s.authorization, auth) != nil {
		return ErrAppLaunch
	}
	if _, err := s.append(ctx, "terminal.json", t); err != nil {
		return err
	}
	s.terminal = true
	return nil
}

// Recognized private append temporaries are never accepted stage evidence.
func appLaunchTemporary(name string) bool {
	return strings.HasPrefix(name, ".tmp-") && appLaunchToken(name, ".tmp-")
}

// This narrowly scoped creator adjusts only its new O_EXCL inode, before any
// publication. It never chmods an existing original or uses a path as repair.
func writeAppLaunchFile(writes *Store, root *os.Root, name string, raw []byte, point string) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil {
		err = writes.checkpoint(point + ".write")
	}
	if err == nil {
		var n int
		n, err = f.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = writes.checkpoint(point + ".sync")
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = writes.checkpoint(point + ".closed")
	}
	return err
}
