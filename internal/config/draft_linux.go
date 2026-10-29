//go:build linux

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
)

const draftName = "draft-input-v1.json"

type draftRecord struct {
	SchemaVersion int             `json:"schema_version"`
	IntentSHA256  string          `json:"intent_sha256"`
	Intent        json.RawMessage `json:"intent"`
}

// ValidateDraft checks the complete protected-record bound before an explicit
// initializer opens or creates state. It has no filesystem effects.
func ValidateDraft(intent []byte) error {
	_, _, err := draftBytes(intent)
	return err
}

func draftBytes(intent []byte) ([]byte, string, error) {
	canonical, err := CanonicalJSON(intent)
	if err != nil {
		return nil, "", ErrIntent
	}
	if _, err = ParseIntent(canonical, IntentDraft); err != nil {
		return nil, "", ErrIntent
	}
	sha := digest(canonical)
	raw, err := json.Marshal(draftRecord{1, sha, canonical})
	if err != nil {
		return nil, "", ErrIntent
	}
	raw, err = CanonicalJSON(raw)
	if err != nil || len(raw) >= MaxCanonicalBytes {
		return nil, "", ErrIntent
	}
	return append(raw, '\n'), sha, nil
}

func (s *Store) readDraft() ([]byte, string, error) {
	raw, err := readBounded(s.root, draftName, MaxCanonicalBytes)
	if err != nil {
		return nil, "", errors.Join(ErrReadUnavailable, err)
	}
	var record draftRecord
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != 1 {
		return nil, "", ErrIntegrity
	}
	want, sha, err := draftBytes(record.Intent)
	if err != nil || sha != record.IntentSHA256 || !bytes.Equal(raw, want) {
		return nil, "", ErrIntegrity
	}
	canonical, _ := CanonicalJSON(record.Intent)
	return canonical, sha, nil
}

// ReadDraft reads declared local input only, never managed runtime authority.
func (s *Store) ReadDraft() ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, "", ErrClosed
	}
	if s.poisoned {
		return nil, "", ErrUncertain
	}
	return s.readDraft()
}

// PublishDraft validates old evidence before atomic replacement. It leaves the
// active revision/generation and all launch originals untouched.
func (s *Store) PublishDraft(intent []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	raw, sha, err := draftBytes(intent)
	if err != nil {
		return "", err
	}
	_, oldSHA, err := s.readDraft()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && oldSHA == sha {
		if err = s.syncPrivateFile(s.root, draftName, "draft.existing-file-sync"); err != nil {
			return "", s.uncertain(err)
		}
		if err = s.syncDir(s.root, "draft.existing-parent-sync"); err != nil {
			return "", s.uncertain(err)
		}
		return sha, nil
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".draft-" + token
	defer s.root.Remove(tmp)
	if err = s.writeNew(s.root, tmp, raw, "draft.file"); err != nil {
		return "", s.uncertain(err)
	}
	if err = checkPrivateInputFile(s.root, tmp); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.checkpoint("draft.rename"); err != nil {
		return "", err
	}
	if err = s.root.Rename(tmp, draftName); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(s.root, "draft.parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return sha, nil
}

func (s *Store) syncPrivateFile(root *os.Root, name, point string) error {
	f, err := openPrivateFile(root, name, os.O_RDONLY)
	if err != nil {
		return err
	}
	if err = s.checkpoint(point); err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}

// Creation modes are filtered by umask. Never publish an input whose actual
// permissions/type cannot be consumed by the exact private reader. This does
// not chmod an existing inode or silently relax an authority boundary.
func checkPrivateInputFile(root *os.Root, name string) error {
	f, err := openPrivateFile(root, name, os.O_RDONLY)
	if err != nil {
		return err
	}
	return f.Close()
}
