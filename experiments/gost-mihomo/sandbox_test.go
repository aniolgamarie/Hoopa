package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxPrivateAndIndependent(t *testing.T) {
	base := t.TempDir()
	a, err := NewSandbox(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSandbox(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.RemoveFiles(); _ = b.RemoveFiles() })
	if a.Path() == b.Path() || a.RunID() == b.RunID() {
		t.Fatal("runs share a directory or identity")
	}
	info, err := os.Stat(a.Path())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("sandbox permissions: %v, %v", info, err)
	}
	info, err = os.Stat(filepath.Join(a.Path(), "owner.token"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("owner permissions: %v, %v", info, err)
	}
	first, err := a.ReserveTCP()
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.ReserveTCP()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("runs share a reserved port")
	}
	if ln, err := net.Listen("tcp4", first); err == nil {
		ln.Close()
		t.Fatal("reservation did not retain the port")
	}
	if err := a.ReleasePorts(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp4", first)
	if err != nil {
		t.Fatal("port not released:", err)
	}
	ln.Close()
}

func TestSandboxRejectsForeignIdentity(t *testing.T) {
	s, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.root.Close()
	if err := os.WriteFile(filepath.Join(s.Path(), "owner.token"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFiles(); !errors.Is(err, ErrSandboxOwnership) {
		t.Fatalf("want ownership conflict, got %v", err)
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatal("foreign directory removed:", err)
	}
}

func TestSandboxRejectsReplacedDirectory(t *testing.T) {
	s, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.root.Close()
	if err := os.Rename(s.Path(), s.Path()+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Path(), "keep"), []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFiles(); !errors.Is(err, ErrSandboxOwnership) {
		t.Fatalf("want ownership conflict, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Path(), "keep")); err != nil {
		t.Fatal("replacement was modified:", err)
	}
}

func TestSandboxCleanupDoesNotFollowSymlink(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSandbox(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Path(), "external")); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveFiles(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("symlink target was removed:", err)
	}
	if err := s.RemoveFiles(); err != nil {
		t.Fatal("cleanup not idempotent:", err)
	}
}
