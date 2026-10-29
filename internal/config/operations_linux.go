//go:build linux

package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
)

const (
	MaxOperationFiles = 1024
	MaxOperationBytes = 1 << 20
)

var (
	ErrOperationConflict = errors.New("OPERATION_RECORD_CONFLICT")
	ErrOperationCapacity = errors.New("OPERATION_CAPACITY_EXHAUSTED")
	ErrOperationStaging  = errors.New("OPERATION_STAGING_REQUIRES_MAINTENANCE")
)

func operationName(id ID) (string, error) {
	if !strings.HasPrefix(string(id), "op-") || !isDigest(strings.TrimPrefix(string(id), "op-")) {
		return "", ErrIntegrity
	}
	return string(id) + ".json", nil
}

func operationTemporary(name string) bool {
	if !strings.HasPrefix(name, ".operation-") {
		return false
	}
	token := strings.TrimPrefix(name, ".operation-")
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 24 && hex.EncodeToString(b) == token
}

// operationInventory validates names/metadata without interpreting authority.
// Valid unpublished temporaries are never returned as accepted operation IDs.
func operationInventory(dir *os.Root) ([]ID, bool, error) {
	entries, err := boundedEntries(dir, 2*MaxOperationFiles)
	if err != nil {
		return nil, false, err
	}
	ids := []ID{}
	staging := false
	for _, entry := range entries {
		name := entry.Name()
		if operationTemporary(name) {
			staging = true
		} else {
			id := ID(strings.TrimSuffix(name, ".json"))
			want, err := operationName(id)
			if err != nil || name != want {
				return nil, false, ErrIntegrity
			}
			ids = append(ids, id)
		}
		f, err := openPrivateFile(dir, name, os.O_RDONLY)
		if err != nil {
			return nil, false, err
		}
		info, statErr := f.Stat()
		err = errors.Join(statErr, f.Close())
		if err != nil {
			return nil, false, err
		}
		if info.Size() < 0 || info.Size() > MaxOperationBytes {
			return nil, false, ErrIntegrity
		}
	}
	if len(ids) > MaxOperationFiles {
		return nil, false, ErrOperationCapacity
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, staging, nil
}

// ReadOperationFile and ListOperationIDs never create, sync or repair state.
// The control layer must validate the record schema and key/request binding.
func (s *Store) ReadOperationFile(id ID) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, "", ErrClosed
	}
	name, err := operationName(id)
	if err != nil {
		return nil, "", err
	}
	dir, err := openPrivateDir(s.root, "operations", false)
	if err != nil {
		return nil, "", errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	raw, err := readBounded(dir, name, MaxOperationBytes)
	if err != nil {
		return nil, "", err
	}
	if s.poisoned {
		return raw, digest(raw), ErrUncertain
	}
	return raw, digest(raw), nil
}

func (s *Store) ListOperationIDs() ([]ID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	dir, err := openPrivateDir(s.root, "operations", false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	ids, _, err := operationInventory(dir)
	return ids, err
}

// CompareAndSwapOperation publishes one opaque, bounded JSON record under the
// persistent writer lock. Empty expectedSHA means first creation only. Existing
// records require an exact prior digest, including an identical-byte retry.
// This is not a transaction with active.json or a proof of any core effect.
func (s *Store) CompareAndSwapOperation(id ID, expectedSHA string, data []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	name, err := operationName(id)
	if err != nil || expectedSHA != "" && !isDigest(expectedSHA) || len(data) == 0 || len(data) > MaxOperationBytes || !json.Valid(data) {
		return "", ErrIntegrity
	}
	dir, err := openPrivateDir(s.root, "operations", false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	old, err := readBounded(dir, name, MaxOperationBytes)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if exists && (expectedSHA == "" || digest(old) != expectedSHA) || !exists && expectedSHA != "" {
		return "", ErrOperationConflict
	}
	if exists && bytes.Equal(old, data) {
		return digest(old), nil
	}
	ids, staging, err := operationInventory(dir)
	if err != nil {
		return "", err
	}
	if staging {
		return "", ErrOperationStaging
	}
	if !exists && len(ids) >= MaxOperationFiles {
		return "", ErrOperationCapacity
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".operation-" + token
	defer dir.Remove(tmp) // only this call's unpublished temporary, never history
	if err = s.writeNew(dir, tmp, data, "operation.file"); err != nil {
		return "", err
	}
	if err = s.checkpoint("operation.rename"); err != nil {
		return "", err
	}
	if err = dir.Rename(tmp, name); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(dir, "operation.parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return digest(data), nil
}
