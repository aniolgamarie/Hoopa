package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrSandboxOwnership = errors.New("experiment sandbox ownership conflict")

// Sandbox owns only a newly created experiment directory and its reservations.
// It does not adopt, stop, or change any pre-existing proxy process.
type Sandbox struct {
	dir     string
	runID   string
	root    *os.Root
	dirInfo os.FileInfo
	mu      sync.Mutex
	ports   []net.Listener
	removed bool
}

func NewSandbox(base string) (*Sandbox, error) {
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(base, "hoopa-p0-")
	if err != nil {
		return nil, fmt.Errorf("create private experiment directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	cleanup := func() { _ = root.Close(); _ = os.RemoveAll(dir) }
	info, err := os.Lstat(dir)
	if err != nil {
		cleanup()
		return nil, err
	}
	runID, err := NewSecret()
	if err != nil {
		cleanup()
		return nil, err
	}
	f, err := root.OpenFile("owner.token", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return nil, err
	}
	_, writeErr := f.WriteString(runID + "\n")
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		cleanup()
		return nil, err
	}
	return &Sandbox{dir: dir, runID: runID, root: root, dirInfo: info}, nil
}

func NewSecret() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Sandbox) Path() string  { return s.dir }
func (s *Sandbox) RunID() string { return s.runID }

// ReserveTCP holds a loopback port until ReleasePorts. An external process must
// still verify its bind after release: a reservation is not process ownership.
func (s *Sandbox) ReserveTCP() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removed {
		return "", os.ErrClosed
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	s.ports = append(s.ports, listener)
	return listener.Addr().String(), nil
}

func (s *Sandbox) ReleasePorts() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releasePorts()
}

func (s *Sandbox) releasePorts() error {
	var result error
	for _, listener := range s.ports {
		result = errors.Join(result, listener.Close())
	}
	s.ports = nil
	return result
}

// RemoveFiles requires the original directory and nonce. The harness must stop
// and verify its owned processes first. Descendant removal is anchored to an
// open directory handle; symlinks cannot redirect it to user configuration.
func (s *Sandbox) RemoveFiles() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.removed {
		return nil
	}
	info, err := os.Lstat(s.dir)
	if err != nil || !info.IsDir() || !os.SameFile(s.dirInfo, info) {
		return ErrSandboxOwnership
	}
	token, err := s.root.ReadFile("owner.token")
	if err != nil || strings.TrimSpace(string(token)) != s.runID {
		return ErrSandboxOwnership
	}
	if err := s.releasePorts(); err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	names, readErr := dir.Readdirnames(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, name := range names {
		if err := s.root.RemoveAll(name); err != nil {
			return err
		}
	}
	if err := s.root.Close(); err != nil {
		return err
	}
	// Remove only an empty directory at the path, never a replacement's contents.
	if err := os.Remove(s.dir); err != nil {
		return err
	}
	s.removed = true
	return nil
}
