//go:build linux

package config

import (
	"errors"
	"os"
)

// HasSourceAssets is a read-only history guard, not a list of valid candidates.
// An unpublished temporary counts as history too. Missing storage means no
// observable assets; callers must separately check the original operation log.
func (s *Store) HasSourceAssets() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false, ErrClosed
	}
	if s.poisoned {
		return false, ErrUncertain
	}
	dir, err := openPrivateDir(s.root, "source-assets", false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	count, _, staging, err := sourceAssetInventory(dir)
	if err != nil {
		return false, err
	}
	return count != 0 || staging, nil
}
