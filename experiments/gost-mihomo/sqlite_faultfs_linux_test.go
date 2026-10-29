//go:build sqlite_faultfs && (sqlite_modernc || sqlite_mattn)

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Run only on a dedicated, empty 4 MiB tmpfs in a private mount namespace.
// The caller mounts and unmounts it; this test never mounts a host filesystem.
func TestSQLiteProbeFilesystemFull(t *testing.T) {
	dir := os.Getenv("HOOPA_SQLITE_FULL_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("explicit absolute isolated tmpfs path required")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		t.Fatal(err)
	}
	if fs.Type != 0x01021994 || fs.Blocks*uint64(fs.Bsize) != 4<<20 {
		t.Fatal("requires exactly 4 MiB tmpfs; refuse to fill another filesystem")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("fixture must be empty: entries=%d err=%v", len(entries), err)
	}
	path := filepath.Join(dir, "probe.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := openSQLiteProbe(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err = p.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = p.apply(ctx, sqliteSample(1), nil); err != nil {
		t.Fatal(err)
	}
	filler := filepath.Join(dir, "filler")
	f, err = os.OpenFile(filler, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	block := make([]byte, 64<<10)
	written := 0
	for written <= 4<<20 {
		n, e := f.Write(block)
		written += n
		if e != nil {
			err = e
			break
		}
	}
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("filler did not observe physical ENOSPC: bytes=%d err=%v", written, err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Statfs(dir, &fs); err != nil || fs.Bavail != 0 {
		t.Fatalf("filesystem not full: available=%d err=%v", fs.Bavail, err)
	}
	_, err = p.apply(ctx, sqliteSample(2), nil)
	if err == nil {
		t.Fatal("sample committed despite full fixture")
	}
	code := sqliteProbeErrorCode(err)
	if code != 13 && code != 10 {
		t.Fatalf("expected native FULL or IOERR, code=%d err=%v", code, err)
	}
	t.Logf("physical ENOSPC observed: filler_bytes=%d native_code=%d uncertain=%v err=%v", written, code, errors.Is(err, errSQLiteProbeUncertain), err)
	if err = os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	p = sqliteReopen(t, path, p)
	sqliteAssert(t, p, sqliteWant(1))
	if replay, err := p.apply(ctx, sqliteSample(2), nil); err != nil || replay {
		t.Fatalf("retry after freeing space: replay=%v err=%v", replay, err)
	}
	sqliteAssert(t, sqliteReopen(t, path, p), sqliteWant(2))
}
