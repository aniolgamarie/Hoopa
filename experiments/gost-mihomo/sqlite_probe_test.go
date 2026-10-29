//go:build sqlite_modernc || sqlite_mattn

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sqliteFixture(t *testing.T) (string, *sqliteProbe) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "probe.sqlite")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := openSQLiteProbe(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.close() })
	if err = p.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return path, p
}

func sqliteSample(n int) sqliteProbeSample {
	if n == 1 {
		return sqliteProbeSample{Series: "entry-a", Epoch: "epoch-a", ID: "sample-1", Sequence: 1, Upload: 8, Download: 16}
	}
	return sqliteProbeSample{Series: "entry-a", Epoch: "epoch-a", ID: "sample-2", Sequence: 2, Upload: 18, Download: 36, GapReason: "checkpoint_window_unknown"}
}

func sqliteWant(n int) sqliteProbeState {
	if n == 0 {
		return sqliteProbeState{}
	}
	if n == 1 {
		return sqliteProbeState{Samples: 1, Facts: 1, Checkpoints: 1, Version: 1, Upload: 8, Download: 16, Sequence: 1, CheckpointUpload: 8, CheckpointDownload: 16}
	}
	return sqliteProbeState{Samples: 2, Facts: 2, Checkpoints: 1, Gaps: 1, Version: 2, Upload: 18, Download: 36, Sequence: 2, CheckpointUpload: 18, CheckpointDownload: 36}
}

func sqliteState(t *testing.T, p *sqliteProbe) sqliteProbeState {
	t.Helper()
	s, err := p.state(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sqliteAssert(t *testing.T, p *sqliteProbe, want sqliteProbeState) {
	t.Helper()
	if got := sqliteState(t, p); got != want {
		t.Fatalf("atomic tuple: got %+v, want %+v", got, want)
	}
}

func sqliteReopen(t *testing.T, path string, p *sqliteProbe) *sqliteProbe {
	t.Helper()
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	next, err := openSQLiteProbe(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.close() })
	return next
}

func TestSQLiteProbeEngine(t *testing.T) {
	_, p := sqliteFixture(t)
	var version, source string
	if err := p.conn.QueryRowContext(context.Background(), "SELECT sqlite_version(),sqlite_source_id()").Scan(&version, &source); err != nil {
		t.Fatal(err)
	}
	rows, err := p.conn.QueryContext(context.Background(), "PRAGMA compile_options")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var options []string
	for rows.Next() {
		var option string
		if err = rows.Scan(&option); err != nil {
			t.Fatal(err)
		}
		options = append(options, option)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("driver=%s sqlite=%s source=%s compile_options=%q", sqliteProbeDriverName, version, source, options)
}

func TestSQLiteProbeAtomicReplayAndEpochs(t *testing.T) {
	path, p := sqliteFixture(t)
	ctx := context.Background()
	for _, n := range []int{1, 2} {
		if replay, err := p.apply(ctx, sqliteSample(n), nil); replay || err != nil {
			t.Fatalf("apply: replay=%v err=%v", replay, err)
		}
	}
	p = sqliteReopen(t, path, p)
	sqliteAssert(t, p, sqliteWant(2))
	for _, n := range []int{1, 2} {
		if replay, err := p.apply(ctx, sqliteSample(n), nil); !replay || err != nil {
			t.Fatalf("replay: %v %v", replay, err)
		}
	}
	conflict := sqliteSample(2)
	conflict.Download++
	if _, err := p.apply(ctx, conflict, nil); !errors.Is(err, errSQLiteProbeConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	for _, s := range []sqliteProbeSample{
		{Series: "entry-a", Epoch: "epoch-a", ID: "stale", Sequence: 2, Upload: 18, Download: 36},
		{Series: "entry-a", Epoch: "epoch-a", ID: "lower", Sequence: 3, Upload: 1, Download: 36},
		{Series: "entry-a", Epoch: "epoch-a", ID: "negative", Sequence: 3, Upload: -1, Download: 36},
	} {
		if _, err := p.apply(ctx, s, nil); !errors.Is(err, errSQLiteProbeIntegrity) {
			t.Fatalf("invalid sample: %v", err)
		}
	}
	sqliteAssert(t, p, sqliteWant(2))
	s := sqliteProbeSample{Series: "entry-a", Epoch: "epoch-b", ID: "sample-1", Sequence: 1, Upload: 3, Download: 4}
	if _, err := p.apply(ctx, s, nil); err != nil {
		t.Fatal(err)
	}
	want := sqliteWant(2)
	want.Samples++
	want.Facts++
	want.Checkpoints++
	want.Version++
	want.Upload += 3
	want.Download += 4
	want.CheckpointUpload += 3
	want.CheckpointDownload += 4
	sqliteAssert(t, sqliteReopen(t, path, p), want)
}

func TestSQLiteProbeExactIntegers(t *testing.T) {
	path, p := sqliteFixture(t)
	s := sqliteSample(1)
	s.Upload = 9007199254740993
	s.Download = math.MaxInt64
	if _, err := p.apply(context.Background(), s, nil); err != nil {
		t.Fatal(err)
	}
	want := sqliteWant(1)
	want.Upload = s.Upload
	want.Download = s.Download
	want.CheckpointUpload = s.Upload
	want.CheckpointDownload = s.Download
	sqliteAssert(t, sqliteReopen(t, path, p), want)
}

func TestSQLiteProbeRollbackAndLostCommitResponse(t *testing.T) {
	for _, phase := range []string{"after_fact", "before_commit", "after_commit"} {
		t.Run(phase, func(t *testing.T) {
			path, p := sqliteFixture(t)
			if _, err := p.apply(context.Background(), sqliteSample(1), nil); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected application boundary")
			_, err := p.apply(context.Background(), sqliteSample(2), func(stage string) error {
				if stage == phase {
					return injected
				}
				return nil
			})
			want := 1
			if phase == "after_commit" {
				want = 2
				if !errors.Is(err, errSQLiteProbeUncertain) {
					t.Fatalf("expected uncertain response: %v", err)
				}
				if _, e := p.apply(context.Background(), sqliteSample(2), nil); !errors.Is(e, errSQLiteProbeUncertain) {
					t.Fatalf("uncertain connection reused before reopen: %v", e)
				}
			} else if !errors.Is(err, injected) {
				t.Fatalf("missing injected abort: %v", err)
			}
			p = sqliteReopen(t, path, p)
			sqliteAssert(t, p, sqliteWant(want))
			replay, err := p.apply(context.Background(), sqliteSample(2), nil)
			if err != nil || replay != (want == 2) {
				t.Fatalf("reconcile: replay=%v err=%v", replay, err)
			}
			sqliteAssert(t, sqliteReopen(t, path, p), sqliteWant(2))
		})
	}
}

func TestSQLiteProbeCancellation(t *testing.T) {
	path, p := sqliteFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := p.apply(ctx, sqliteSample(1), func(stage string) error {
		if stage == "before_commit" {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("cancelled transaction succeeded")
	}
	sqliteAssert(t, sqliteReopen(t, path, p), sqliteWant(0))
}

func TestSQLiteProbeBusy(t *testing.T) {
	path, p := sqliteFixture(t)
	other, err := openSQLiteProbe(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	tx, err := p.conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(context.Background(), "UPDATE probe_meta SET version=version WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = other.apply(context.Background(), sqliteSample(1), nil)
	if code := sqliteProbeErrorCode(err); code != 5 {
		t.Fatalf("wanted real SQLITE_BUSY, code=%d err=%v", code, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("busy wait exceeded bounded test budget")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	sqliteAssert(t, other, sqliteWant(0))
}

func TestSQLiteProbePageLimitFull(t *testing.T) {
	path, p := sqliteFixture(t)
	if _, err := p.apply(context.Background(), sqliteSample(1), nil); err != nil {
		t.Fatal(err)
	}
	var pages, limit int
	if err := p.conn.QueryRowContext(context.Background(), "PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := p.conn.QueryRowContext(context.Background(), fmt.Sprintf("PRAGMA max_page_count=%d", pages)).Scan(&limit); err != nil || limit != pages {
		t.Fatalf("page bound: %d %v", limit, err)
	}
	s := sqliteSample(2)
	s.GapReason = strings.Repeat("x", 1<<20)
	_, err := p.apply(context.Background(), s, nil)
	if code := sqliteProbeErrorCode(err); code != 13 {
		t.Fatalf("wanted real SQLITE_FULL, code=%d err=%v", code, err)
	}
	sqliteAssert(t, sqliteReopen(t, path, p), sqliteWant(1))
	t.Log("SQLITE_FULL from actual max_page_count; physical filesystem ENOSPC not tested by this case")
}

type sqliteFileSnapshot struct {
	Mode     os.FileMode
	Size     int64
	Modified int64
	Hash     [32]byte
}

func sqliteTree(t *testing.T, dir string) map[string]sqliteFileSnapshot {
	t.Helper()
	out := map[string]sqliteFileSnapshot{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("nonregular fixture %s", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = sqliteFileSnapshot{info.Mode(), info.Size(), info.ModTime().UnixNano(), sha256.Sum256(raw)}
	}
	return out
}

// This fixture is deliberately quiescent and uses rollback journaling. The
// explicit writer prepares it before the read-only boundary being measured.
// It does not demonstrate safe direct reads of a live WAL ledger.
func TestSQLiteProbeReadOnly(t *testing.T) {
	path, p := sqliteFixture(t)
	ctx := context.Background()
	if _, err := p.apply(ctx, sqliteSample(1), nil); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := p.conn.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("prepare quiescent rollback fixture: mode=%q err=%v", mode, err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	before := sqliteTree(t, filepath.Dir(path))
	ro, err := openSQLiteProbe(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	sqliteAssert(t, ro, sqliteWant(1))
	if _, err = ro.conn.ExecContext(ctx, "UPDATE probe_meta SET version=2"); sqliteProbeErrorCode(err) != 8 {
		t.Fatalf("readonly write was not rejected: %v", err)
	}
	if err = ro.close(); err != nil {
		t.Fatal(err)
	}
	if after := sqliteTree(t, filepath.Dir(path)); !reflect.DeepEqual(before, after) {
		t.Fatalf("readonly path changed files: before=%+v after=%+v", before, after)
	}
}

func sqliteAssertReadRefused(t *testing.T, path string) {
	t.Helper()
	before := sqliteTree(t, filepath.Dir(path))
	ro, err := openSQLiteProbe(context.Background(), path, true)
	if ro != nil {
		_ = ro.close()
	}
	if ro != nil || !errors.Is(err, errSQLiteProbeReadUnavailable) {
		t.Fatalf("unsafe direct-file read not refused: ro=%v err=%v", ro != nil, err)
	}
	if after := sqliteTree(t, filepath.Dir(path)); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused read changed files: before=%+v after=%+v", before, after)
	}
}

// These are fallback policy checks, not native SQLite permission failures.
func TestSQLiteProbeWALReadRefusal(t *testing.T) {
	path, p := sqliteFixture(t)
	if _, err := p.apply(context.Background(), sqliteSample(1), nil); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("live_with_sidecars", func(t *testing.T) { sqliteAssertReadRefused(t, path) })
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	t.Run("closed", func(t *testing.T) { sqliteAssertReadRefused(t, path) })
	t.Run("missing", func(t *testing.T) {
		sqliteAssertReadRefused(t, filepath.Join(filepath.Dir(path), "missing.sqlite"))
	})
}

func TestSQLiteProbeRollbackJournalReadRefusal(t *testing.T) {
	path, p := sqliteFixture(t)
	var mode string
	if err := p.conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("prepare rollback fixture: mode=%q err=%v", mode, err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			// Even an empty sidecar is refused; the reader does not attempt to
			// decide whether it is stale, hot, or belongs to another writer.
			if err := os.WriteFile(path+suffix, nil, 0600); err != nil {
				t.Fatal(err)
			}
			sqliteAssertReadRefused(t, path)
			if err := os.Remove(path + suffix); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// This helper only runs in subprocesses launched below, on this test's private DB.
func TestSQLiteProbeCrashChild(t *testing.T) {
	path := os.Getenv("HOOPA_SQLITE_PROBE_CHILD_PATH")
	if path == "" {
		return
	}
	p, err := openSQLiteProbe(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	phase := os.Getenv("HOOPA_SQLITE_PROBE_CHILD_PHASE")
	_, err = p.apply(context.Background(), sqliteSample(2), func(stage string) error {
		if stage == phase || (phase == "race_commit" && stage == "before_commit") {
			if _, err := fmt.Fprintln(os.Stdout, "HOOPA_SQLITE_BARRIER"); err != nil {
				return err
			}
			if phase != "race_commit" {
				var b [1]byte
				_, err := io.ReadFull(os.Stdin, b[:])
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteProbeProcessInterruption(t *testing.T) {
	for _, phase := range []string{"after_fact", "before_commit", "after_commit", "race_commit"} {
		t.Run(phase, func(t *testing.T) {
			path, p := sqliteFixture(t)
			if _, err := p.apply(context.Background(), sqliteSample(1), nil); err != nil {
				t.Fatal(err)
			}
			if err := p.close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSQLiteProbeCrashChild$")
			cmd.Env = append(os.Environ(), "HOOPA_SQLITE_PROBE_CHILD_PATH="+path, "HOOPA_SQLITE_PROBE_CHILD_PHASE="+phase)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			found := false
			scanner := bufio.NewScanner(out)
			for scanner.Scan() {
				if scanner.Text() == "HOOPA_SQLITE_BARRIER" {
					found = true
					break
				}
			}
			killErr := cmd.Process.Kill()
			waitErr := cmd.Wait()
			if !found {
				t.Fatalf("child did not reach barrier: wait=%v stderr=%s", waitErr, stderr.String())
			}
			if phase != "race_commit" && (killErr != nil || waitErr == nil) {
				t.Fatalf("held child did not die: kill=%v wait=%v", killErr, waitErr)
			}
			next, err := openSQLiteProbe(context.Background(), path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer next.close()
			got := sqliteState(t, next)
			if phase == "race_commit" {
				if got != sqliteWant(1) && got != sqliteWant(2) {
					t.Fatalf("torn commit tuple: %+v", got)
				}
				t.Logf("race commit resolved whole tuple version=%d; exact fsync timing unobserved", got.Version)
			} else {
				want := 1
				if phase == "after_commit" {
					want = 2
				}
				if got != sqliteWant(want) {
					t.Fatalf("post-kill tuple: %+v", got)
				}
			}
			if _, err = next.apply(context.Background(), sqliteSample(2), nil); err != nil {
				t.Fatal(err)
			}
			sqliteAssert(t, next, sqliteWant(2))
		})
	}
}
