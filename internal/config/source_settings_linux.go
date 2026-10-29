//go:build linux

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
)

const MaxSourceSettingsBytes = 128 << 10

var ErrSourceSettingsConflict = errors.New("SOURCE_SETTINGS_ALREADY_INITIALIZED")

const sourceSettingsName = "source-settings-v1.json"

// ReadSourceSettings reads private initialization input, not runtime authority.
// It never generates an identity key, creates a file, or supplies missing values.
// The control layer owns the typed schema and validation of these opaque bytes.
func (s *Store) ReadSourceSettings() ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, "", ErrClosed
	}
	dir, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return nil, "", errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	raw, err := readBounded(dir, sourceSettingsName, MaxSourceSettingsBytes)
	if err != nil {
		return nil, "", errors.Join(ErrReadUnavailable, err)
	}
	if !json.Valid(raw) {
		return nil, "", ErrIntegrity
	}
	if s.poisoned {
		return nil, "", ErrUncertain
	}
	return raw, digest(raw), nil
}

// PublishSourceSettings installs one already validated private initialization
// record. This cannot replace an existing setting or change active.json. The
// control initializer must reject missing settings when previous source history
// exists; this opaque primitive alone cannot distinguish loss from first install.
// A different existing record needs an explicit migration contract; an identical
// retry establishes fresh durability barriers.
func (s *Store) PublishSourceSettings(raw []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > MaxSourceSettingsBytes || !json.Valid(raw) {
		return "", ErrIntegrity
	}
	raw = bytes.Clone(raw)
	dir, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	old, err := readBounded(dir, sourceSettingsName, MaxSourceSettingsBytes)
	if err == nil {
		if !json.Valid(old) {
			return "", ErrIntegrity
		}
		if !bytes.Equal(raw, old) {
			return "", ErrSourceSettingsConflict
		}
		f, err := openPrivateFile(dir, sourceSettingsName, os.O_RDONLY)
		if err != nil {
			return "", err
		}
		err = s.checkpoint("source.settings-existing-file-sync")
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return "", s.uncertain(err)
		}
		if err = s.syncDir(dir, "source.settings-existing-parent-sync"); err != nil {
			return "", s.uncertain(err)
		}
		return digest(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".source-settings-" + token
	defer dir.Remove(tmp)
	if err = s.writeNew(dir, tmp, raw, "source.settings-file"); err != nil {
		return "", err
	}
	if err = s.checkpoint("source.settings-rename"); err != nil {
		return "", err
	}
	if _, err = dir.Lstat(sourceSettingsName); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(ErrSourceSettingsConflict, err)
	}
	if err = dir.Rename(tmp, sourceSettingsName); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(dir, "source.settings-parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return digest(raw), nil
}
