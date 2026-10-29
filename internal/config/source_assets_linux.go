//go:build linux

package config

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

const (
	MaxSourceAssetBytes      = 64 << 20
	MaxSourceAssetTotalBytes = 512 << 20
	MaxSourceAssets          = 1024
)

var (
	ErrSourceAssetCapacity = errors.New("SOURCE_ASSET_CAPACITY_EXHAUSTED")
	ErrSourceAssetStaging  = errors.New("SOURCE_ASSET_STAGING_REQUIRES_MAINTENANCE")
)

func sourceAssetName(sha string) (string, error) {
	if !isDigest(sha) {
		return "", ErrIntegrity
	}
	return sha + ".json", nil
}

func sourceAssetTemporary(name string) bool {
	if !strings.HasPrefix(name, ".source-") {
		return false
	}
	token := strings.TrimPrefix(name, ".source-")
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 24 && hex.EncodeToString(b) == token
}

// Inventory checks bounded metadata, not the contents of unrelated assets.
// Temporary files are never exposed as candidates or removed by a read.
func sourceAssetInventory(dir *os.Root) (int, int64, bool, error) {
	entries, err := boundedEntries(dir, 2*MaxSourceAssets)
	if err != nil {
		return 0, 0, false, err
	}
	count, total, staging := 0, int64(0), false
	for _, entry := range entries {
		name := entry.Name()
		temporary := sourceAssetTemporary(name)
		if temporary {
			staging = true
		} else {
			want, err := sourceAssetName(strings.TrimSuffix(name, ".json"))
			if err != nil || name != want {
				return 0, 0, false, ErrIntegrity
			}
			count++
		}
		f, err := openPrivateFile(dir, name, os.O_RDONLY)
		if err != nil {
			return 0, 0, false, err
		}
		info, statErr := f.Stat()
		if err = errors.Join(statErr, f.Close()); err != nil {
			return 0, 0, false, err
		}
		if info.Size() == 0 && !temporary {
			return 0, 0, false, ErrIntegrity
		}
		if info.Size() < 0 || info.Size() > MaxSourceAssetBytes || info.Size() > MaxSourceAssetTotalBytes-total {
			return 0, 0, false, ErrSourceAssetCapacity
		}
		total += info.Size()
	}
	if count > MaxSourceAssets {
		return 0, 0, false, ErrSourceAssetCapacity
	}
	return count, total, staging, nil
}

// ReadSourceAsset returns exact private bytes after structural filesystem and
// digest checks. Only subscription.RestoreAsset can recreate parser authority.
// A file's existence is not evidence that an update committed or is pending.
func (s *Store) ReadSourceAsset(sha string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	name, err := sourceAssetName(sha)
	if err != nil {
		return nil, err
	}
	dir, err := openPrivateDir(s.root, "source-assets", false)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	raw, err := readBounded(dir, name, MaxSourceAssetBytes)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	if digest(raw) != sha || !json.Valid(raw) {
		return nil, ErrIntegrity
	}
	if s.poisoned {
		return nil, ErrUncertain
	}
	return raw, nil
}

// PublishSourceAsset writes one immutable, content-addressed private JSON asset.
// It does not advance active.json, assign a source ID, or commit an update. The
// sole control writer must bind it to a validated operation receipt afterwards.
// Existing exact bytes may be re-synced even when capacity is full; they are not
// a new candidate. An unknown staging file blocks new publication until explicit
// maintenance, and is never silently removed here.
func (s *Store) PublishSourceAsset(raw []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > MaxSourceAssetBytes || !json.Valid(raw) {
		return "", ErrIntegrity
	}
	raw = bytes.Clone(raw)
	sha := digest(raw)
	name, _ := sourceAssetName(sha)
	// Only this explicit writer may add the optional directory. OpenStore and
	// all readers retain their original no-migration/no-repair behavior.
	_, err := s.root.Lstat("source-assets")
	if errors.Is(err, os.ErrNotExist) {
		if err = s.checkpoint("source.assets-mkdir"); err != nil {
			return "", err
		}
		if err = s.root.Mkdir("source-assets", 0700); err != nil {
			return "", s.uncertain(err)
		}
	} else if err != nil {
		return "", err
	}
	dir, err := openPrivateDir(s.root, "source-assets", false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	// A preceding writer may have died after a visible rename. Reopening is
	// not itself a durability barrier; establish both directory barriers here.
	if err = s.syncDir(dir, "source.reopen-sync"); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(s.root, "source.root-sync"); err != nil {
		return "", s.uncertain(err)
	}
	old, err := readBounded(dir, name, MaxSourceAssetBytes)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return "", ErrIntegrity
		}
		f, err := openPrivateFile(dir, name, os.O_RDONLY)
		if err != nil {
			return "", err
		}
		err = s.checkpoint("source.existing-file-sync")
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return "", s.uncertain(err)
		}
		if err = s.syncDir(dir, "source.existing-parent-sync"); err != nil {
			return "", s.uncertain(err)
		}
		return sha, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	count, total, staging, err := sourceAssetInventory(dir)
	if err != nil {
		return "", err
	}
	if staging {
		return "", ErrSourceAssetStaging
	}
	if count >= MaxSourceAssets || int64(len(raw)) > MaxSourceAssetTotalBytes-total {
		return "", ErrSourceAssetCapacity
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".source-" + token
	defer dir.Remove(tmp) // Only this call's unpublished temporary.
	if err = s.writeNew(dir, tmp, raw, "source.file"); err != nil {
		return "", err
	}
	if err = s.checkpoint("source.rename"); err != nil {
		return "", err
	}
	if _, err = dir.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(ErrIntegrity, err)
	}
	if err = dir.Rename(tmp, name); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(dir, "source.parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return sha, nil
}
