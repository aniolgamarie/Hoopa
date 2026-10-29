//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"hoopa/internal/control"
)

func TestPlanOutputExclusiveCompletePrivateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")
	raw := []byte(`{"plan_digest":"complete-private-output"}`)
	if err := writePlanOutput(context.Background(), path, raw); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("partial or changed output", err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatal("unsafe output", err)
	}
	if err = writePlanOutput(context.Background(), path, []byte("changed")); !errors.Is(err, control.ErrConflict) {
		t.Fatal("existing output overwritten", err)
	}
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, raw) {
		t.Fatal("existing output changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("temporary output leaked")
	}
}

func TestPlanOutputExplicitModeSurvivesRestrictiveUmask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	old := syscall.Umask(0777)
	defer syscall.Umask(old)
	if err := writePlanOutput(context.Background(), path, []byte("complete")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("explicit output mode lost to umask", err)
	}
}

func TestPlanOutputRefusesUnsafeOrNonRegularDestination(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory", "missing-parent", "writable-parent", "nil-context", "cancelled", "empty", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "plan.json")
			raw := []byte("complete")
			ctx := context.Background()
			want := control.ErrConflict
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "absent"), path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "missing-parent":
				path = filepath.Join(dir, "absent", "plan.json")
				want = control.ErrRequest
			case "writable-parent":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
				want = control.ErrRequest
			case "nil-context":
				ctx = nil
				want = control.ErrRequest
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "empty":
				raw = nil
				want = control.ErrRequest
			case "oversize":
				raw = make([]byte, control.MaxRequestBytes+1)
				want = control.ErrRequest
			}
			if err := writePlanOutput(ctx, path, raw); !errors.Is(err, want) {
				t.Fatal("wrong refusal", err, want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if len(entry.Name()) > 0 && entry.Name()[0] == '.' {
					t.Fatal("refusal left temp file")
				}
			}
			if kind == "missing-parent" {
				if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatal("created output parent", err)
				}
			}
		})
	}
}

func TestPlanOutputPublicationFaultBoundaries(t *testing.T) {
	for _, point := range []string{"write", "file-sync", "publish", "directory-sync"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "plan.json")
			raw := bytes.Repeat([]byte("complete"), 100)
			err := writePlanOutputChecked(context.Background(), path, raw, func(stage string) error {
				if stage == point {
					return errors.New("injected")
				}
				return nil
			})
			if err == nil {
				t.Fatal("fault hidden")
			}
			got, readErr := os.ReadFile(path)
			if point == "directory-sync" {
				if readErr != nil || !bytes.Equal(got, raw) {
					t.Fatal("published original removed/partial", readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatal("pre-publication failure exposed output", readErr)
			}
			entries, _ := os.ReadDir(dir)
			want := 0
			if point == "directory-sync" {
				want = 1
			}
			if len(entries) != want {
				t.Fatal("temporary file leaked")
			}
		})
	}
}

func TestPlanOutputConcurrentWritersNeverReplaceWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	var wg sync.WaitGroup
	results := make(chan []byte, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(value byte) {
			defer wg.Done()
			raw := bytes.Repeat([]byte{value}, 4096)
			err := writePlanOutput(context.Background(), path, raw)
			if err == nil {
				results <- raw
			} else {
				errs <- err
			}
		}(byte('a' + i))
	}
	wg.Wait()
	close(results)
	close(errs)
	if len(results) != 1 {
		t.Fatal("multiple/no publishers", len(results))
	}
	want := <-results
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("winner incomplete/replaced", err)
	}
	for err := range errs {
		if !errors.Is(err, control.ErrConflict) {
			t.Fatal("loser error", err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("concurrent temps leaked")
	}
}
