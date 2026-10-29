//go:build sqlite_syncfault && (sqlite_modernc || sqlite_mattn)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A fault fixture in a disposable child, not a product sandbox. The additive
// filter returns EIO for fsync/fdatasync in that process only. TSYNC is required
// because SQLite/Go may execute the commit on another thread. No filter escapes
// into the parent, and no privilege or global kernel setting is changed.
func sqliteInjectSyncFailure() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, 38, 1, 0, 0, 0, 0); errno != 0 { // PR_SET_NO_NEW_PRIVS
		return errno
	}
	filter := []syscall.SockFilter{
		{Code: 0x20, K: 4},                 // load seccomp_data.arch
		{Code: 0x15, K: 0xc000003e, Jt: 1}, // AUDIT_ARCH_X86_64
		{Code: 0x06, K: 0x80000000},        // KILL_PROCESS for unexpected ABI
		{Code: 0x20, K: 0},                 // load syscall number
		{Code: 0x15, K: syscall.SYS_FSYNC, Jt: 2},
		{Code: 0x15, K: syscall.SYS_FDATASYNC, Jt: 1},
		{Code: 0x06, K: 0x7fff0000},                       // ALLOW
		{Code: 0x06, K: 0x00050000 | uint32(syscall.EIO)}, // ERRNO(EIO)
	}
	program := syscall.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	// Linux amd64 __NR_seccomp=317, SET_MODE_FILTER=1, FILTER_FLAG_TSYNC=1.
	r, _, errno := syscall.RawSyscall(317, 1, 1, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(program)
	if errno != 0 || r != 0 {
		return fmt.Errorf("seccomp TSYNC: result=%d errno=%v", r, errno)
	}
	return nil
}

type sqliteSyncFaultResult struct {
	Driver       string   `json:"driver"`
	Phases       []string `json:"phases"`
	FilterActive bool     `json:"filter_active"`
	NativeCode   int      `json:"native_code"`
	ExtendedCode int      `json:"extended_code"`
	Uncertain    bool     `json:"uncertain"`
	ReuseRefused bool     `json:"reuse_refused"`
	CloseError   string   `json:"close_error"`
}

func TestSQLiteProbeSyncFaultChild(t *testing.T) {
	path := os.Getenv("HOOPA_SQLITE_SYNCFAULT_CHILD_PATH")
	if path == "" {
		return // parent helper, never counted as independent coverage
	}
	ctx := context.Background()
	p, err := openSQLiteProbe(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	control, err := os.OpenFile(filepath.Join(filepath.Dir(path), "sync-control"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if err = control.Sync(); err != nil {
		t.Fatal(err)
	}
	result := sqliteSyncFaultResult{Driver: sqliteProbeDriverName}
	_, err = p.apply(ctx, sqliteSample(2), func(phase string) error {
		result.Phases = append(result.Phases, phase)
		if phase == "before_commit" {
			if e := sqliteInjectSyncFailure(); e != nil {
				return e
			}
			if e := syscall.Fsync(int(control.Fd())); e != syscall.EIO {
				return fmt.Errorf("injected fsync returned %v", e)
			}
			if e := syscall.Fdatasync(int(control.Fd())); e != syscall.EIO {
				return fmt.Errorf("injected fdatasync returned %v", e)
			}
			result.FilterActive = true
		}
		return nil // the error must come from the real SQLite Commit
	})
	result.NativeCode = sqliteProbeErrorCode(err)
	result.ExtendedCode = sqliteSyncFaultExtendedCode(err)
	result.Uncertain = errors.Is(err, errSQLiteProbeUncertain)
	_, reuseErr := p.apply(ctx, sqliteSample(2), nil)
	result.ReuseRefused = errors.Is(reuseErr, errSQLiteProbeUncertain)
	result.CloseError = fmt.Sprint(p.close())
	raw, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	fmt.Printf("HOOPA_SYNCFAULT %s\n", raw)
	if !result.FilterActive || result.NativeCode != 10 || result.ExtendedCode != 1034 || !result.Uncertain || !result.ReuseRefused {
		t.Fatalf("native Commit sync fault not established: result=%+v err=%v", result, err)
	}
}

func TestSQLiteProbeNativeSyncFailure(t *testing.T) {
	path, p := sqliteFixture(t)
	ctx := context.Background()
	if _, err := p.apply(ctx, sqliteSample(1), nil); err != nil {
		t.Fatal(err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestSQLiteProbeSyncFaultChild$", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "HOOPA_SQLITE_SYNCFAULT_CHILD_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil || childCtx.Err() != nil {
		t.Fatalf("sync fault child: err=%v output=%s", err, out)
	}
	var observation sqliteSyncFaultResult
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "HOOPA_SYNCFAULT ") {
			count++
			if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "HOOPA_SYNCFAULT ")), &observation); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 || observation.Driver != sqliteProbeDriverName || !observation.FilterActive || observation.ExtendedCode != 1034 || !observation.Uncertain || !observation.ReuseRefused {
		t.Fatalf("invalid child observation: %s", out)
	}
	t.Logf("actual native syscall-injected EIO: %s", out)
	next, err := openSQLiteProbe(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	got := sqliteState(t, next)
	if got != sqliteWant(1) && got != sqliteWant(2) {
		t.Fatalf("torn tuple after native sync error: %+v", got)
	}
	t.Logf("fresh unfiltered process resolved tuple version=%d; not a power-loss test", got.Version)
	replay, err := next.apply(ctx, sqliteSample(2), nil)
	if err != nil || replay != (got == sqliteWant(2)) {
		t.Fatalf("reconcile same sample: replay=%v err=%v", replay, err)
	}
	sqliteAssert(t, sqliteReopen(t, path, next), sqliteWant(2))
}
