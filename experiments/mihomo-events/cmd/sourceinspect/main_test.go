package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotPrivateRegularFileOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot")
	if err := os.WriteFile(path, []byte("frozen"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil || !unchanged(f, info) {
		t.Fatal("stable file rejected", err)
	}
	_ = f.Close()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if f, err := openSnapshot(path); err == nil {
		_ = f.Close()
		t.Fatal("public snapshot accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openSnapshot(link); err == nil {
		_ = f.Close()
		t.Fatal("symlink snapshot accepted")
	}
	if f, err := openSnapshot(dir); err == nil {
		_ = f.Close()
		t.Fatal("directory accepted")
	}
}

func TestSnapshotModificationIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(path, []byte("frozen"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed length"), 0600); err != nil {
		t.Fatal(err)
	}
	if unchanged(f, before) {
		t.Fatal("changing snapshot accepted")
	}
}
