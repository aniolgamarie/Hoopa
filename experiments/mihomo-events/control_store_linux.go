//go:build linux

package mihomoevents

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type controlStore struct {
	directory string
	lock      *os.File
	fault     func(string) error
}

func controlCheckPath(path string, dir bool) error {
	v, e := os.Lstat(path)
	if e != nil {
		return e
	}
	st, ok := v.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0600)
	if dir {
		mode = 0700
	}
	if !ok || int(st.Uid) != os.Geteuid() || v.Mode().Perm() != mode || v.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || v.Mode()&os.ModeSymlink != 0 || v.IsDir() != dir || (!dir && !v.Mode().IsRegular()) {
		return fmt.Errorf("unsafe control path %s", path)
	}
	return nil
}
func controlSyncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	e = f.Sync()
	c := f.Close()
	if e != nil {
		return e
	}
	return c
}
func InitControlProbe(directory string) error {
	directory, e := filepath.Abs(directory)
	if len(directory)+42 > 4096 {
		return errors.New("control directory too long")
	}
	if e != nil {
		return e
	}
	if e = os.Mkdir(directory, 0700); e != nil {
		return e
	}
	if e = controlCheckPath(directory, true); e != nil {
		return e
	}
	for _, name := range []string{"operations", "bundles", "launches"} {
		if e = os.Mkdir(filepath.Join(directory, name), 0700); e != nil {
			return e
		}
	}
	f, e := os.OpenFile(filepath.Join(directory, "lock"), os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	s := &controlStore{directory: directory}
	if e = s.write("active.json", ControlActive{Version: 1, Desired: "stopped"}, 64<<10); e != nil {
		return e
	}
	return controlSyncDirectory(filepath.Dir(directory))
}
func (s *controlStore) write(name string, v any, limit int) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b)+1 > limit {
		return errors.New("control record exceeds bound")
	}
	b = append(b, '\n')
	dir := filepath.Dir(filepath.Join(s.directory, name))
	var n [16]byte
	if _, e = rand.Read(n[:]); e != nil {
		return e
	}
	tmp := filepath.Join(dir, ".write-"+hex.EncodeToString(n[:]))
	f, e := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
	}
	// An uncertain temporary file is retained. Reopen never guesses that rename failed.
	fail := func(e error) error { _ = f.Close(); return fmt.Errorf("%w: %v", ErrControlPoisoned, e) }
	if s.fault != nil {
		if e = s.fault("write"); e != nil {
			return fail(e)
		}
	}
	for len(b) > 0 {
		n, e := f.Write(b)
		if e != nil {
			return fail(e)
		}
		if n == 0 {
			return fail(io.ErrShortWrite)
		}
		b = b[n:]
	}
	if e = f.Sync(); e != nil {
		return fail(e)
	}
	if s.fault != nil {
		if e = s.fault("file-sync"); e != nil {
			return fail(e)
		}
	}
	if e = f.Close(); e != nil {
		return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
	}
	if e = os.Rename(tmp, filepath.Join(s.directory, name)); e != nil {
		return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
	}
	if s.fault != nil {
		if e = s.fault("rename"); e != nil {
			return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
		}
	}
	if e = controlSyncDirectory(dir); e != nil {
		return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
	}
	if s.fault != nil {
		if e = s.fault("directory-sync"); e != nil {
			return fmt.Errorf("%w: %v", ErrControlPoisoned, e)
		}
	}
	return nil
}
func (s *controlStore) read(name string, out any, limit int) error {
	path := filepath.Join(s.directory, name)
	if e := controlCheckPath(path, false); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	c := f.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return c
	}
	if len(b) > limit {
		return errors.New("control record exceeds bound")
	}
	return controlDecode(b, out)
}
func (s *controlStore) close() error {
	if s.lock == nil {
		return nil
	}
	e := syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	c := s.lock.Close()
	s.lock = nil
	if e != nil {
		return e
	}
	return c
}
func (s *controlStore) bundle(b ControlBundle) error {
	name := "bundles/" + controlDigest(b) + ".json"
	var old ControlBundle
	e := s.read(name, &old, 256<<10)
	if e == nil {
		if controlDigest(old) != controlDigest(b) {
			return errors.New("bundle collision")
		}
		return nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return s.write(name, b, 256<<10)
}
func (s *controlStore) launchDirectory(nonce string) error {
	if e := os.Mkdir(filepath.Join(s.directory, "launches", nonce), 0700); e != nil {
		return e
	}
	return controlSyncDirectory(filepath.Join(s.directory, "launches"))
}
func controlValidateActive(a ControlActive, directory string) error {
	if a.Version != 1 {
		return errors.New("unknown active version")
	}
	switch a.Desired {
	case "stopped":
		if a.Bundle != nil || a.Launch != nil {
			return errors.New("invalid stopped active")
		}
	case "running":
		if a.Bundle == nil || a.Launch == nil || a.Launch.Instance == nil || controlDigest(*a.Bundle) != a.Launch.BundleDigest {
			return errors.New("invalid running active")
		}
		return controlValidateLaunch(*a.Launch, directory)
	default:
		return errors.New("unknown active intent")
	}
	return nil
}
func controlValidateOperation(o ControlOperation, directory string) error {
	if o.Version != 1 || o.ID != o.Request.RequestID || !controlToken.MatchString(o.ID) || o.RequestDigest != controlDigest(o.Request) || o.Base.Revision != o.Request.ExpectedRevision || o.Base.Revision == ^uint64(0) || o.TargetRevision != o.Base.Revision+1 {
		return errors.New("invalid operation identity")
	}
	if e := controlValidateRequest(o.Request); e != nil {
		return e
	}
	if e := controlValidateActive(o.Base, directory); e != nil {
		return e
	}
	switch o.Phase {
	case "applying", "verifying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
	default:
		return errors.New("unknown operation phase")
	}
	switch o.Step {
	case "recorded", "stop-old", "old-stopped", "new-spawn", "new-verified", "publishing", "committed", "rolling-back", "rollback-verified", "rollback-publishing", "rolled-back":
	default:
		return errors.New("unknown operation step")
	}
	if o.Request.Action == "stop" && (o.Target != nil || o.Rollback != nil) {
		return errors.New("stop contains spawn intent")
	}
	if o.Request.Action == "apply" && (o.Phase == "committed" || o.Phase == "verifying" || o.Phase == "publishing" || o.Phase == "rolling_back" || o.Phase == "rolled_back") && o.Target == nil {
		return errors.New("missing target launch")
	}
	if o.Phase == "committed" && o.Request.Action == "apply" && o.Target.Instance == nil {
		return errors.New("missing committed instance")
	}
	if o.Phase == "rolled_back" && o.Base.Desired == "running" && (o.Rollback == nil || o.Rollback.Instance == nil) {
		return errors.New("missing rollback instance")
	}
	if o.Target != nil {
		if o.Request.Bundle == nil || o.Target.BundleDigest != controlDigest(*o.Request.Bundle) {
			return errors.New("target bundle mismatch")
		}
		if e := controlValidateLaunch(*o.Target, directory); e != nil {
			return e
		}
	}
	if o.Rollback != nil {
		if o.Base.Bundle == nil || o.Rollback.BundleDigest != controlDigest(*o.Base.Bundle) {
			return errors.New("rollback bundle mismatch")
		}
		if e := controlValidateLaunch(*o.Rollback, directory); e != nil {
			return e
		}
		if o.Target != nil && o.Rollback.Nonce == o.Target.Nonce {
			return errors.New("rollback reused launch")
		}
	}
	return nil
}
func OpenControlProbe(directory string, backend ControlBackend) (*ControlProbe, error) {
	if backend == nil {
		return nil, errors.New("missing control backend")
	}
	directory, e := filepath.Abs(directory)
	if len(directory)+42 > 4096 {
		return nil, errors.New("control directory too long")
	}
	if e != nil {
		return nil, e
	}
	if e = controlCheckPath(directory, true); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(directory, "lock"), os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	s := &controlStore{directory: directory, lock: f}
	if e = controlCheckPath(filepath.Join(directory, "lock"), false); e != nil {
		f.Close()
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	// Validate bounded material without repairing incomplete writes or invoking backend.
	count := 0
	e = filepath.WalkDir(directory, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 1024 {
			return errors.New("too many control files")
		}
		if e = controlCheckPath(path, d.IsDir()); e != nil {
			return e
		}
		// Runtime material belongs to the core/backend, not to the control
		// record format. In particular, an active source journal can exceed
		// the per-record limit and keeps changing while this reader opens.
		// Validate the private boundary here; Observe verifies its own bounded
		// claim/config files before granting process control.
		if d.IsDir() && filepath.Dir(path) == filepath.Join(directory, "launches") && controlNonce.MatchString(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			v, e := d.Info()
			if e != nil {
				return e
			}
			if v.Size() > 256<<10 {
				return errors.New("control file too large")
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	for _, name := range []string{"operations", "bundles", "launches"} {
		if e = controlCheckPath(filepath.Join(directory, name), true); e != nil {
			return nil, e
		}
	}
	p := &ControlProbe{store: s, backend: backend, operations: map[string]ControlOperation{}}
	if e = s.read("active.json", &p.active, 64<<10); e != nil {
		return nil, e
	}
	if e = controlValidateActive(p.active, directory); e != nil {
		return nil, e
	}
	entries, e := os.ReadDir(filepath.Join(directory, "operations"))
	if e != nil {
		return nil, e
	}
	for _, v := range entries {
		if strings.HasPrefix(v.Name(), ".write-") {
			continue
		}
		if v.IsDir() || !strings.HasSuffix(v.Name(), ".json") {
			return nil, errors.New("invalid operation file")
		}
		var o ControlOperation
		if e = s.read("operations/"+v.Name(), &o, 256<<10); e != nil {
			return nil, e
		}
		if v.Name() != o.ID+".json" {
			return nil, errors.New("operation filename mismatch")
		}
		if e = controlValidateOperation(o, directory); e != nil {
			return nil, e
		}
		p.operations[o.ID] = o
	}
	if len(p.operations) > 32 {
		return nil, errors.New("too many control operations")
	}
	roots, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, v := range roots {
		switch v.Name() {
		case "lock", "active.json", "operations", "bundles", "launches":
		default:
			if !strings.HasPrefix(v.Name(), ".write-") {
				return nil, errors.New("unknown control material")
			}
		}
	}
	bundles, err := os.ReadDir(filepath.Join(directory, "bundles"))
	if err != nil {
		return nil, err
	}
	for _, v := range bundles {
		if strings.HasPrefix(v.Name(), ".write-") {
			continue
		}
		var b ControlBundle
		if v.IsDir() {
			return nil, errors.New("invalid bundle file")
		}
		if e = s.read("bundles/"+v.Name(), &b, 256<<10); e != nil {
			return nil, e
		}
		if e = controlValidateBundle(b); e != nil {
			return nil, e
		}
		if v.Name() != controlDigest(b)+".json" {
			return nil, errors.New("bundle filename mismatch")
		}
	}
	launches, err := os.ReadDir(filepath.Join(directory, "launches"))
	if err != nil {
		return nil, err
	}
	for _, v := range launches {
		if strings.HasPrefix(v.Name(), ".write-") {
			continue
		}
		if v.IsDir() {
			if !controlNonce.MatchString(v.Name()) {
				return nil, errors.New("invalid launch directory")
			}
			if _, e = os.Lstat(filepath.Join(directory, "launches", v.Name()+".json")); e != nil {
				return nil, e
			}
			continue
		}
		var l ControlLaunch
		if e = s.read("launches/"+v.Name(), &l, 256<<10); e != nil {
			return nil, e
		}
		if e = controlValidateLaunch(l, directory); e != nil {
			return nil, e
		}
		if l.Instance != nil || v.Name() != l.Nonce+".json" {
			return nil, errors.New("invalid immutable launch intent")
		}
	}
	// Every referenced snapshot and launch intent must match its durable original.
	validateLaunch := func(l *ControlLaunch) error {
		if l == nil {
			return nil
		}
		var stored ControlLaunch
		if e := s.read("launches/"+l.Nonce+".json", &stored, 256<<10); e != nil {
			return e
		}
		copy := controlClone(*l)
		copy.Instance = nil
		if controlDigest(stored) != controlDigest(copy) {
			return errors.New("launch intent mismatch")
		}
		return nil
	}
	validateBundle := func(b *ControlBundle) error {
		if b == nil {
			return nil
		}
		var stored ControlBundle
		if e := s.read("bundles/"+controlDigest(*b)+".json", &stored, 256<<10); e != nil {
			return e
		}
		if controlDigest(stored) != controlDigest(*b) {
			return errors.New("bundle snapshot mismatch")
		}
		return nil
	}
	if e = validateLaunch(p.active.Launch); e != nil {
		return nil, e
	}
	if e = validateBundle(p.active.Bundle); e != nil {
		return nil, e
	}
	unfinished := 0
	for _, o := range p.operations {
		if !controlTerminal(o.Phase) {
			unfinished++
			if !controlPendingActiveMatches(p.active, o) {
				return nil, errors.New("active record is unrelated to unfinished operation")
			}
		}
		for _, l := range []*ControlLaunch{o.Base.Launch, o.Target, o.Rollback} {
			if e = validateLaunch(l); e != nil {
				return nil, e
			}
		}
		for _, b := range []*ControlBundle{o.Base.Bundle, o.Request.Bundle} {
			if e = validateBundle(b); e != nil {
				return nil, e
			}
		}
	}
	if unfinished > 1 {
		return nil, errors.New("multiple unfinished operations")
	}
	ok = true
	return p, nil
}

// A two-file publication can leave either the base or a published outcome.
// Reopening must not permit recovery to overwrite an unrelated active state.
// This check intentionally does not infer a total historical operation order.
func controlPendingActiveMatches(a ControlActive, o ControlOperation) bool {
	if controlDigest(a) == controlDigest(o.Base) {
		return true
	}
	if o.Request.Action == "stop" {
		return controlDigest(a) == controlDigest(ControlActive{Version: 1, Revision: o.TargetRevision, Desired: "stopped"})
	}
	if o.Target != nil && o.Target.Instance != nil {
		target := ControlActive{Version: 1, Revision: o.TargetRevision, Desired: "running", Bundle: o.Request.Bundle, Launch: o.Target}
		if controlDigest(a) == controlDigest(target) {
			return true
		}
	}
	if o.Rollback != nil && o.Rollback.Instance != nil {
		base := controlClone(o.Base)
		base.Launch = o.Rollback
		return controlDigest(a) == controlDigest(base)
	}
	return false
}
