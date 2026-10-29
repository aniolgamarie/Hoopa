//go:build linux

package config

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

const launchHistoryDirectory = "app-launches"
const launchHistoryMarker = "app-launch-history-v1.json"
const launchHistoryIdentity = ".identity.json"
const launchAllocationLock = ".allocation.lock"

type launchHistoryIdentityRecord struct {
	SchemaVersion int    `json:"schema_version"`
	HistoryID     string `json:"history_id"`
}

func decodeLaunchHistory(raw []byte) (string, error) {
	var record launchHistoryIdentityRecord
	if decode(raw, &record) != nil || record.SchemaVersion != 1 || !strings.HasPrefix(record.HistoryID, "apphist-") {
		return "", ErrIntegrity
	}
	token := strings.TrimPrefix(record.HistoryID, "apphist-")
	b, err := hex.DecodeString(token)
	if err != nil || len(b) != 24 || hex.EncodeToString(b) != token {
		return "", ErrIntegrity
	}
	return record.HistoryID, nil
}

func (s *Store) readLaunchHistory() (string, error) {
	credentials, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return "", errors.Join(ErrReadUnavailable, err)
	}
	defer credentials.Close()
	marker, err := readBounded(credentials, launchHistoryMarker, 1024)
	if err != nil {
		return "", errors.Join(ErrReadUnavailable, err)
	}
	id, err := decodeLaunchHistory(marker)
	if err != nil {
		return "", err
	}
	dir, err := openPrivateDir(s.root, launchHistoryDirectory, false)
	if err != nil {
		return "", errors.Join(ErrIntegrity, err)
	}
	defer dir.Close()
	raw, err := readBounded(dir, launchHistoryIdentity, 1024)
	if err != nil || !bytes.Equal(raw, marker) {
		return "", errors.Join(ErrIntegrity, err)
	}
	lock, err := readBounded(dir, launchAllocationLock, 0)
	if err != nil || len(lock) != 0 {
		return "", errors.Join(ErrIntegrity, err)
	}
	return id, nil
}

// ReadLaunchHistoryID performs no creation, synchronization or lock acquisition.
func (s *Store) ReadLaunchHistoryID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	if s.poisoned {
		return "", ErrUncertain
	}
	return s.readLaunchHistory()
}

// CheckLaunchHistoryInitialization is a read-only preflight, not an install or
// readiness claim. Init uses it before preparing other immutable credentials.
func (s *Store) CheckLaunchHistoryInitialization() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.poisoned {
		return ErrUncertain
	}
	return s.checkLaunchHistoryInitialization()
}

func (s *Store) checkLaunchHistoryInitialization() error {
	credentials, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return err
	}
	defer credentials.Close()
	_, err = readBounded(credentials, launchHistoryMarker, 1024)
	if err == nil {
		_, err = s.readLaunchHistory()
		return err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := openPrivateDir(s.root, launchHistoryDirectory, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	raw, err := readBounded(dir, launchHistoryIdentity, 1024)
	if err != nil {
		return errors.Join(ErrIntegrity, err)
	}
	if _, err = decodeLaunchHistory(raw); err != nil {
		return err
	}
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(4)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != launchHistoryIdentity && entry.Name() != launchAllocationLock {
			return ErrIntegrity
		}
	}
	_, err = readBounded(dir, launchAllocationLock, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// InitializeLaunchHistory installs optional metadata under the Store writer.
// A marked but lost namespace is never recreated. Existing launch directories
// are never opened, repaired or locked by initialization.
func (s *Store) InitializeLaunchHistory() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	if err := s.checkLaunchHistoryInitialization(); err != nil {
		return err
	}
	credentials, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return err
	}
	defer credentials.Close()
	marker, markerErr := readBounded(credentials, launchHistoryMarker, 1024)
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}
	marked := markerErr == nil
	if marked {
		if _, err = decodeLaunchHistory(marker); err != nil {
			return err
		}
	}
	dir, err := openPrivateDir(s.root, launchHistoryDirectory, false)
	if err != nil {
		if marked || !errors.Is(err, os.ErrNotExist) {
			return errors.Join(ErrIntegrity, err)
		}
		token, e := randomToken()
		if e != nil {
			return e
		}
		marker, e = encode(launchHistoryIdentityRecord{1, "apphist-" + token})
		if e != nil {
			return e
		}
		if e = s.root.Mkdir(launchHistoryDirectory, 0700); e != nil {
			return e
		}
		dir, e = openPrivateDir(s.root, launchHistoryDirectory, false)
		if e != nil {
			return s.uncertain(e)
		}
		if e = s.writeNew(dir, launchHistoryIdentity, marker, "launch.history-identity"); e != nil {
			dir.Close()
			return s.uncertain(e)
		}
	}
	defer dir.Close()
	raw, err := readBounded(dir, launchHistoryIdentity, 1024)
	if err != nil {
		return errors.Join(ErrIntegrity, err)
	}
	if _, err = decodeLaunchHistory(raw); err != nil {
		return err
	}
	if marked && !bytes.Equal(raw, marker) {
		return ErrIntegrity
	}
	marker = raw
	if !marked {
		// Only a recognizably interrupted first installation can be finished.
		f, e := dir.Open(".")
		if e != nil {
			return e
		}
		entries, e := f.ReadDir(4)
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if entry.Name() != launchHistoryIdentity && entry.Name() != launchAllocationLock {
				return ErrIntegrity
			}
		}
	}
	lock, err := readBounded(dir, launchAllocationLock, 0)
	if err != nil {
		if marked || !errors.Is(err, os.ErrNotExist) {
			return errors.Join(ErrIntegrity, err)
		}
		if err = s.writeNew(dir, launchAllocationLock, nil, "launch.history-lock"); err != nil {
			return s.uncertain(err)
		}
	} else if len(lock) != 0 {
		return ErrIntegrity
	}
	for _, item := range []struct{ name, point string }{{launchHistoryIdentity, "launch.history-identity-sync"}, {launchAllocationLock, "launch.history-lock-sync"}} {
		if err = s.syncPrivateFile(dir, item.name, item.point); err != nil {
			return s.uncertain(err)
		}
	}
	if err = s.syncDir(dir, "launch.history-directory-sync"); err != nil {
		return s.uncertain(err)
	}
	if err = s.syncDir(s.root, "launch.history-state-sync"); err != nil {
		return s.uncertain(err)
	}
	if !marked {
		token, e := randomToken()
		if e != nil {
			return e
		}
		tmp := ".app-launch-history-" + token
		defer credentials.Remove(tmp)
		if err = s.writeNew(credentials, tmp, marker, "launch.history-marker"); err != nil {
			return s.uncertain(err)
		}
		if err = checkPrivateInputFile(credentials, tmp); err != nil {
			return s.uncertain(err)
		}
		if err = s.checkpoint("launch.history-marker-rename"); err != nil {
			return err
		}
		if err = credentials.Rename(tmp, launchHistoryMarker); err != nil {
			return s.uncertain(err)
		}
	} else if err = s.syncPrivateFile(credentials, launchHistoryMarker, "launch.history-marker-sync"); err != nil {
		return s.uncertain(err)
	}
	if err = s.syncDir(credentials, "launch.history-credentials-sync"); err != nil {
		return s.uncertain(err)
	}
	return nil
}
