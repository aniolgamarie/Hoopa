//go:build linux

package config

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxLaunchWorkdirs = 1024

// PrepareLaunchWorkdir creates a fresh, durable private working directory under
// this Store's configured runtime root. The coordinator must durably record the
// random name before calling. This is neither a launch ticket nor permission to
// reuse an existing directory. Failed partial creations remain as evidence.
func (s *Store) PrepareLaunchWorkdir(name string, raw []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	token := strings.TrimPrefix(name, "launch-")
	b, err := hex.DecodeString(token)
	if !strings.HasPrefix(name, "launch-") || err != nil || len(b) != 24 || hex.EncodeToString(b) != token || len(raw) == 0 || len(raw) > 65536 {
		return "", ErrIntegrity
	}
	root, err := privateRoot(s.runtimePath, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return "", err
	}
	entries, err := f.ReadDir(MaxLaunchWorkdirs + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", err
	}
	if len(entries) >= MaxLaunchWorkdirs {
		return "", ErrIntegrity
	}
	if err := s.checkpoint("workdir.mkdir"); err != nil {
		return "", err
	}
	if err := root.Mkdir(name, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.Join(ErrRunConflict, err)
		}
		return "", err
	}
	// From the first successful namespace mutation onward, any failure needs
	// explicit reconciliation. Never erase a directory and retry its name.
	child, err := openPrivateDir(root, name, false)
	if err != nil {
		return "", s.uncertain(err)
	}
	err = s.writeNew(child, "config.json", raw, "workdir.config")
	if err == nil {
		err = s.syncDir(child, "workdir.directory-sync")
	}
	err = errors.Join(err, child.Close())
	if err == nil {
		err = s.syncDir(root, "workdir.parent-sync")
	}
	if err != nil {
		return "", s.uncertain(err)
	}
	return filepath.Join(s.runtimePath, name), nil
}
