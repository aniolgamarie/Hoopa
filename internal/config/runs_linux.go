//go:build linux

package config

import (
	"errors"
	"io"
	"os"
	"strings"
)

const (
	MaxRunRecordBytes   = 64 << 10
	MaxRuns             = 1024
	MaxRunRegistrations = 1024
)

var ErrRunConflict = errors.New("RUN_RECORD_CONFLICT")

// PreparedRun exists only after FIRST durable creation of this launch intent.
// Reading an old intent cannot reconstruct a spawn ticket. The ticket and its
// inherited directory FD do not carry or prolong the manager's writer lock.
type PreparedRun struct {
	store *Store
	id    RunID
	sha   string
	taken bool // protected by store.mu
}

func (p *PreparedRun) Digest() string { return p.sha }

// TakeBirthDirectory transfers one directory FD for a launcher ExtraFiles entry.
// Failure consumes the ticket too: never retry a possibly initiated launch.
func (p *PreparedRun) TakeBirthDirectory() (*os.File, error) {
	if p == nil || p.store == nil {
		return nil, ErrRunConflict
	}
	s := p.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.taken {
		return nil, ErrRunConflict
	}
	p.taken = true
	if err := s.writable(); err != nil {
		return nil, err
	}
	dir, err := s.openRun(p.id)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if _, sha, err := readRunIntent(dir, p.id); err != nil || sha != p.sha {
		return nil, errors.Join(ErrRunConflict, err)
	}
	if _, err := dir.Lstat("birth.json"); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(ErrRunConflict, err)
	}
	return dir.Open(".")
}

func runDirectory(id RunID) (string, error) {
	if _, err := ParseID(string(id)); err != nil {
		return "", errors.Join(ErrIntegrity, err)
	}
	return digest([]byte(id)), nil
}

func registrationName(op ID) (string, error) {
	if _, err := ParseID(string(op)); err != nil {
		return "", errors.Join(ErrIntegrity, err)
	}
	return "registration-" + digest([]byte(op)) + ".json", nil
}

func boundedEntries(root *os.Root, limit int) ([]os.DirEntry, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := f.ReadDir(limit + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	if len(entries) > limit {
		return nil, ErrRunConflict
	}
	return entries, nil
}

func (s *Store) openRun(id RunID) (*os.Root, error) {
	name, err := runDirectory(id)
	if err != nil {
		return nil, err
	}
	runs, err := openPrivateDir(s.root, "runs", false)
	if err != nil {
		return nil, err
	}
	defer runs.Close()
	return openPrivateDir(runs, name, false)
}

func readRunIntent(dir *os.Root, id RunID) (RunIntent, string, error) {
	var intent RunIntent
	data, err := readBounded(dir, "intent.json", MaxRunRecordBytes)
	if err == nil {
		err = decode(data, &intent)
	}
	if err == nil {
		err = intent.Validate()
	}
	if err == nil && intent.RunID != id {
		err = ErrIntegrity
	}
	if err != nil {
		return RunIntent{}, "", err
	}
	return intent, digest(data), nil
}

func (s *Store) validateRunBundle(intent RunIntent) error {
	bundle, err := s.readBundle(intent.Generation)
	if err != nil || bundle.ManifestSHA256 != intent.ManifestSHA256 {
		return errors.Join(ErrIntegrity, err)
	}
	binary, config := false, false
	for _, b := range bundle.Manifest.Binaries {
		binary = binary || b == intent.Binary
	}
	for _, f := range bundle.Manifest.Files {
		config = config || f.Kind == "generated_config" && f.SHA256 == intent.ConfigSHA256
	}
	if !binary || !config {
		return ErrIntegrity
	}
	return nil
}

// PrepareRun publishes an immutable launch intention before any process spawn.
// It does not materialize the core's workdir/config, spawn, or grant ownership.
func (s *Store) PrepareRun(intent RunIntent) (*PreparedRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return nil, err
	}
	if err := intent.Validate(); err != nil {
		return nil, err
	}
	if err := s.validateRunBundle(intent); err != nil {
		return nil, err
	}
	data, err := encode(intent)
	if err != nil || len(data) > MaxRunRecordBytes {
		return nil, errors.Join(ErrIntegrity, err)
	}
	runs, err := openPrivateDir(s.root, "runs", false)
	if err != nil {
		return nil, err
	}
	defer runs.Close()
	entries, err := boundedEntries(runs, MaxRuns)
	if err != nil || len(entries) >= MaxRuns {
		return nil, errors.Join(ErrRunConflict, err)
	}
	name, _ := runDirectory(intent.RunID)
	if _, err = runs.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(ErrRunConflict, err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	tmp := ".run-" + token
	if err = runs.Mkdir(tmp, 0700); err != nil {
		return nil, err
	}
	defer runs.RemoveAll(tmp) // only this invocation's unpublished directory
	dir, err := openPrivateDir(runs, tmp, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	if err = s.writeNew(dir, "intent.json", data, "run.intent"); err != nil {
		return nil, err
	}
	if err = s.syncDir(dir, "run.temp-sync"); err != nil {
		return nil, err
	}
	if err = s.checkpoint("run.rename"); err != nil {
		return nil, err
	}
	if _, err = runs.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.Join(ErrRunConflict, err)
	}
	if err = runs.Rename(tmp, name); err != nil {
		return nil, s.uncertain(err)
	}
	if err = s.syncDir(runs, "run.parent-sync"); err != nil {
		return nil, s.uncertain(err)
	}
	return &PreparedRun{store: s, id: intent.RunID, sha: digest(data)}, nil
}

func (s *Store) ReadRunIntent(id RunID) (RunIntent, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RunIntent{}, "", ErrClosed
	}
	dir, err := s.openRun(id)
	if err != nil {
		return RunIntent{}, "", err
	}
	defer dir.Close()
	intent, sha, err := readRunIntent(dir, id)
	if err == nil {
		err = s.validateRunBundle(intent)
	}
	if err != nil {
		return RunIntent{}, "", err
	}
	return intent, sha, nil
}

func readRunBirth(dir *os.Root, intent RunIntent, intentSHA string) (RunBirth, string, error) {
	var birth RunBirth
	data, err := readBounded(dir, "birth.json", MaxRunRecordBytes)
	if err == nil {
		err = decode(data, &birth)
	}
	if err == nil {
		err = birth.Match(intent, intentSHA)
	}
	if err != nil {
		return RunBirth{}, "", err
	}
	return birth, digest(data), nil
}

func (s *Store) ReadRunBirth(id RunID) (RunBirth, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RunBirth{}, "", ErrClosed
	}
	dir, err := s.openRun(id)
	if err != nil {
		return RunBirth{}, "", err
	}
	defer dir.Close()
	intent, sha, err := readRunIntent(dir, id)
	if err == nil {
		err = s.validateRunBundle(intent)
	}
	if err != nil {
		return RunBirth{}, "", err
	}
	return readRunBirth(dir, intent, sha)
}

func readRegistration(dir *os.Root, intent RunIntent, intentSHA string, op ID) (RunRegistration, string, error) {
	var reg RunRegistration
	name, err := registrationName(op)
	if err != nil {
		return reg, "", err
	}
	birth, birthSHA, err := readRunBirth(dir, intent, intentSHA)
	if err != nil {
		return reg, "", err
	}
	data, err := readBounded(dir, name, MaxRunRecordBytes)
	if err == nil {
		err = decode(data, &reg)
	}
	if err == nil {
		err = reg.Match(intent, intentSHA, birth, birthSHA)
	}
	if err == nil && reg.OperationID != op {
		err = ErrIntegrity
	}
	if err != nil {
		return RunRegistration{}, "", err
	}
	return reg, digest(data), nil
}

// PublishRunRegistration persists a caller's FRESH proof. Stored records are not
// authorization; the runtime must verify ownership again for every later action.
func (s *Store) PublishRunRegistration(reg RunRegistration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	if err := reg.Validate(); err != nil {
		return "", err
	}
	dir, err := s.openRun(reg.RunID)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	intent, intentSHA, err := readRunIntent(dir, reg.RunID)
	if err == nil {
		err = s.validateRunBundle(intent)
	}
	if err != nil {
		return "", err
	}
	birth, birthSHA, err := readRunBirth(dir, intent, intentSHA)
	if err != nil {
		return "", err
	}
	if err = reg.Match(intent, intentSHA, birth, birthSHA); err != nil {
		return "", err
	}
	data, err := encode(reg)
	if err != nil || len(data) > MaxRunRecordBytes {
		return "", errors.Join(ErrIntegrity, err)
	}
	sha := digest(data)
	name, _ := registrationName(reg.OperationID)
	if _, err = dir.Lstat(name); err == nil {
		_, oldSHA, err := readRegistration(dir, intent, intentSHA, reg.OperationID)
		if err != nil || oldSHA != sha {
			return "", errors.Join(ErrRunConflict, err)
		}
		return sha, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	entries, err := boundedEntries(dir, MaxRunRegistrations+2)
	if err != nil {
		return "", err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "registration-") {
			count++
		}
	}
	if count >= MaxRunRegistrations || len(entries) >= MaxRunRegistrations+2 {
		return "", ErrRunConflict
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".registration-" + token
	defer dir.Remove(tmp)
	if err = s.writeNew(dir, tmp, data, "registration.file"); err != nil {
		return "", err
	}
	if err = s.checkpoint("registration.rename"); err != nil {
		return "", err
	}
	if err = dir.Rename(tmp, name); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(dir, "registration.parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return sha, nil
}

func (s *Store) ReadRunRegistration(id RunID, op ID) (RunRegistration, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return RunRegistration{}, "", ErrClosed
	}
	return s.readRunRegistration(id, op)
}

// ReadControlRegistration requires an open, non-poisoned writer but only reads
// an EXISTING record. It cannot repair a missing registration. The coordinator
// must keep its writer open and serialize actions throughout subsequent IO.
func (s *Store) ReadControlRegistration(id RunID, op ID) (RunRegistration, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return RunRegistration{}, "", err
	}
	return s.readRunRegistration(id, op)
}

func (s *Store) readRunRegistration(id RunID, op ID) (RunRegistration, string, error) {
	dir, err := s.openRun(id)
	if err != nil {
		return RunRegistration{}, "", err
	}
	defer dir.Close()
	intent, intentSHA, err := readRunIntent(dir, id)
	if err == nil {
		err = s.validateRunBundle(intent)
	}
	if err != nil {
		return RunRegistration{}, "", err
	}
	return readRegistration(dir, intent, intentSHA, op)
}

// syncRunDirectories is an explicit writer-open barrier. It never repairs or
// deletes partial attempts and does not interpret missing births as absence.
func (s *Store) syncRunDirectories() error {
	runs, err := openPrivateDir(s.root, "runs", false)
	if err != nil {
		return err
	}
	defer runs.Close()
	entries, err := boundedEntries(runs, MaxRuns)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		dir, err := openPrivateDir(runs, entry.Name(), false)
		if err != nil {
			return err
		}
		err = errors.Join(s.syncDir(dir, "open.run.sync"), dir.Close())
		if err != nil {
			return err
		}
	}
	return nil
}
