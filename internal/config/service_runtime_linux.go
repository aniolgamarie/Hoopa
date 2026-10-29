//go:build linux

package config

import "os"

// OpenRuntimeDirectory opens an existing, safely traversed private directory.
// Discovery/query callers never create paths or obtain the instance writer.
func OpenRuntimeDirectory(paths Paths) (*os.Root, error) {
	if err := paths.Validate(); err != nil {
		return nil, err
	}
	return privateRoot(paths.Runtime, false)
}

// OpenServiceRuntime lends a separate runtime root to the management service.
// Its caller must retain the writer lease until all service resources close.
func (s *Store) OpenServiceRuntime() (*os.Root, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return nil, err
	}
	return privateRoot(s.runtimePath, false)
}
