//go:build linux

package config

import (
	"errors"
	"sort"
	"strings"
)

var ErrRunStaging = errors.New("RUN_STAGING_REQUIRES_RECONCILIATION")

func runTemporary(name string) bool {
	const prefix = ".run-"
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+48 {
		return false
	}
	for _, c := range name[len(prefix):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ListRunIDs discovers bounded immutable launch intentions without writing or
// restoring a launch ticket. It verifies each private intent and its directory
// binding; callers must still ReadRunIntent to verify referenced bundle material
// and freshly prove process/controller identity. Missing birth is not absence.
// Staging, damage or unknown names return no partial list. A readonly handle can
// observe visible records, but cannot establish their publication durability.
func (s *Store) ListRunIDs() ([]RunID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.poisoned {
		return nil, ErrUncertain
	}
	runs, err := openPrivateDir(s.root, "runs", false)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	defer runs.Close()
	entries, err := boundedEntries(runs, MaxRuns)
	if err != nil {
		return nil, err
	}
	ids := make([]RunID, 0, len(entries))
	for _, entry := range entries {
		staging := runTemporary(entry.Name())
		if !staging && !isDigest(entry.Name()) {
			return nil, ErrIntegrity
		}
		dir, err := openPrivateDir(runs, entry.Name(), false)
		if err != nil {
			return nil, errors.Join(ErrReadUnavailable, err)
		}
		if staging {
			err := dir.Close()
			return nil, errors.Join(ErrRunStaging, err)
		}
		data, readErr := readBounded(dir, "intent.json", MaxRunRecordBytes)
		if err := errors.Join(readErr, dir.Close()); err != nil {
			return nil, errors.Join(ErrReadUnavailable, err)
		}
		var intent RunIntent
		if err := decode(data, &intent); err != nil {
			return nil, err
		}
		if err := intent.Validate(); err != nil {
			return nil, err
		}
		name, _ := runDirectory(intent.RunID)
		if name != entry.Name() {
			return nil, ErrIntegrity
		}
		ids = append(ids, intent.RunID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
