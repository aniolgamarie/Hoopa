//go:build linux

package config

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// MaintainOperationStaging is an explicit writer operation, never a side effect
// of OpenStore or an authority reader. The caller must first validate recovery
// admission and the complete authoritative journal. Temporary bytes are opaque.
// The persistent writer lock excludes controlled concurrent namespace writers;
// same-UID hostile replacement remains outside the Store's isolation boundary.
func (s *Store) MaintainOperationStaging() (removed int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.writable(); err != nil {
		return 0, err
	}
	dir, err := openPrivateDir(s.root, "operations", false)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := dir.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			if removed > 0 {
				err = s.uncertain(err)
			}
		}
	}()
	entries, err := boundedEntries(dir, 2*MaxOperationFiles)
	if err != nil {
		return 0, err
	}
	type temporary struct {
		name string
		info os.FileInfo
	}
	temps := []temporary{}
	authorities := 0
	// Validate ALL names and metadata before the first deletion, including files
	// that will never be removed. These are operationInventory's exact budgets.
	for _, entry := range entries {
		name := entry.Name()
		isTemp := operationTemporary(name)
		if !isTemp {
			id := ID(strings.TrimSuffix(name, ".json"))
			want, e := operationName(id)
			if e != nil || name != want {
				return 0, ErrIntegrity
			}
			authorities++
		}
		f, e := openPrivateFile(dir, name, os.O_RDONLY)
		if e != nil {
			return 0, e
		}
		info, e := f.Stat()
		e = errors.Join(e, f.Close())
		if e != nil {
			return 0, e
		}
		if info.Size() < 0 || info.Size() > MaxOperationBytes {
			return 0, ErrIntegrity
		}
		if isTemp {
			temps = append(temps, temporary{name, info})
		}
	}
	if authorities > MaxOperationFiles {
		return 0, ErrOperationCapacity
	}
	sort.Slice(temps, func(i, j int) bool { return temps[i].name < temps[j].name })
	fail := func(e error) (int, error) {
		if removed > 0 {
			return removed, s.uncertain(e)
		}
		return removed, e
	}
	for _, temp := range temps {
		if e := s.checkpoint("operation.maintenance.remove"); e != nil {
			return fail(e)
		}
		// Reopen under the anchored directory, reject symlink/hardlink/metadata
		// changes, and retain the originally inventoried inode identity. Linux does
		// not provide conditional unlink; this is not an adversarial same-UID lock.
		f, e := openPrivateFile(dir, temp.name, os.O_RDONLY)
		if e != nil {
			return fail(e)
		}
		info, e := f.Stat()
		e = errors.Join(e, f.Close())
		if e != nil {
			return fail(e)
		}
		if !os.SameFile(temp.info, info) || info.Size() < 0 || info.Size() > MaxOperationBytes {
			return fail(ErrIntegrity)
		}
		if e := dir.Remove(temp.name); e != nil {
			return fail(e)
		}
		removed++
	}
	// Even an empty inventory needs an explicit successful parent sync: a prior
	// writer may have deleted everything but failed to establish durability.
	if e := s.syncDir(dir, "operation.maintenance.sync"); e != nil {
		return removed, s.uncertain(e)
	}
	return removed, nil
}
