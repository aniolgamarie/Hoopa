//go:build linux && amd64

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Parent invocations do no work; only explicit disposable fixture subprocesses
// execute this entry. Do not count the empty parent helper as native coverage.
func TestProcessFixture(t *testing.T) {
	if os.Getenv("HOOPA_PROCESS_FIXTURE") != "1" {
		return
	}
	fmt.Print("READY\n")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

type processFixture struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	spec   ProcessSpec
	waited bool
}

func startProcessFixture(t *testing.T) *processFixture {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("ab", 32)
	dir := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestProcessFixture$", "-test.timeout=20s")
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, processNonceEnvironment+"=") && !strings.HasPrefix(v, "HOOPA_PROCESS_FIXTURE=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, processNonceEnvironment+"="+nonce, "HOOPA_PROCESS_FIXTURE=1")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	f := &processFixture{cmd: cmd, stdin: in, spec: ProcessSpec{BinaryPath: exe, BinarySHA256: processDigest(raw), Workdir: dir, NonceSHA256: processDigest([]byte(nonce))}}
	t.Cleanup(func() {
		in.Close()
		if !f.waited {
			cmd.Process.Kill()
			cmd.Wait()
			f.waited = true
		}
	})
	ready := make(chan error, 1)
	go func() {
		var b [6]byte
		_, err := io.ReadFull(out, b[:])
		if err == nil && string(b[:]) != "READY\n" {
			err = fmt.Errorf("unexpected fixture readiness")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fixture startup timed out")
	}
	return f
}

func processTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestProcessStatParsing(t *testing.T) {
	stat := func(name, start, state string) []byte {
		return []byte("123 (" + name + ") " + state + " " + strings.Repeat("0 ", 18) + start + " 0\n")
	}
	for _, name := range []string{"simple", "contains ) parentheses (", "space\nand newline"} {
		pid, start, state, err := parseProcessStat(stat(name, "18446744073709551615", "S"))
		if err != nil || pid != 123 || start != ^uint64(0) || state != "S" {
			t.Fatalf("valid stat %q: %d/%d/%s/%v", name, pid, start, state, err)
		}
	}
	for _, raw := range [][]byte{nil, []byte("123 (x) S"), stat("x", "0", "S"), stat("x", "-1", "S"), stat("x", "18446744073709551616", "S"), stat("x", "1", "?"), []byte("-1 (x) S " + strings.Repeat("0 ", 18) + "1")} {
		if _, _, _, err := parseProcessStat(raw); err == nil {
			t.Fatalf("invalid stat accepted %q", raw)
		}
	}
}

func TestProcessPidfdTargetBinding(t *testing.T) {
	for _, raw := range []string{"Pid:\t123\nNSpid:\t123 7\n", "pos:\t0\nPid:\t123\nflags:\t02000002\n"} {
		if err := pidfdTargetMatches([]byte(raw), 123); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"", "Pid:\t124\n", "Pid:\t0\n", "Pid:\t-1\n", "Pid:\t0123\n", "Pid:\t123 124\n", "Pid:\t123\nPid:\t123\n"} {
		if err := pidfdTargetMatches([]byte(raw), 123); !errors.Is(err, ErrProcessUnverified) {
			t.Fatalf("ambiguous/unavailable proc PID accepted: %q %v", raw, err)
		}
	}
	f := startProcessFixture(t)
	h, err := ObserveProcess(processTestContext(t), f.cmd.Process.Pid, f.spec)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err = verifyPidfdTarget(h.fd, f.cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err = verifyPidfdTarget(h.fd, os.Getpid()); !errors.Is(err, ErrProcessUnverified) {
		t.Fatalf("original child pidfd accepted another actual PID: %v", err)
	}
}

func TestProcessObserveRevalidateAndCloseKeepsChild(t *testing.T) {
	f := startProcessFixture(t)
	ctx := processTestContext(t)
	h, err := ObserveProcess(ctx, f.cmd.Process.Pid, f.spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	id := h.Identity()
	if id.PID != f.cmd.Process.Pid || id.UID != uint32(os.Geteuid()) || id.StartTicks == 0 || len(id.BootID) != 36 || id.BinaryPath != f.spec.BinaryPath || id.BinarySHA256 != f.spec.BinarySHA256 || id.Workdir != f.spec.Workdir || id.NonceSHA256 != f.spec.NonceSHA256 {
		t.Fatalf("incomplete observed identity %+v", id)
	}
	if err = h.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 15*time.Millisecond)
	defer cancel()
	if err = h.WaitExited(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live child falsely exited %v", err)
	}
	if err = h.Close(); err != nil {
		t.Fatal(err)
	}
	if err = h.Verify(ctx); !errors.Is(err, ErrProcessUnverified) {
		t.Fatalf("closed handle reusable %v", err)
	}
	if err = h.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := ObserveProcess(ctx, id.PID, f.spec)
	if err != nil {
		t.Fatal("Close killed child", err)
	}
	defer again.Close()
	if again.Identity() != id {
		t.Fatal("same child identity changed after handle Close")
	}
}

func TestProcessWrongEvidenceDoesNotSignal(t *testing.T) {
	f := startProcessFixture(t)
	ctx := processTestContext(t)
	for _, field := range []string{"binary-path", "binary-hash", "cwd", "nonce", "invalid-hash"} {
		t.Run(field, func(t *testing.T) {
			s := f.spec
			switch field {
			case "binary-path":
				s.BinaryPath += "-wrong"
			case "binary-hash":
				s.BinarySHA256 = strings.Repeat("0", 64)
			case "cwd":
				s.Workdir += "-wrong"
			case "nonce":
				s.NonceSHA256 = strings.Repeat("0", 64)
			case "invalid-hash":
				s.NonceSHA256 = "invalid"
			}
			if h, err := ObserveProcess(ctx, f.cmd.Process.Pid, s); !errors.Is(err, ErrProcessUnverified) {
				if h != nil {
					h.Close()
				}
				t.Fatalf("unverified evidence accepted %v", err)
			}
		})
	}
	h, err := ObserveProcess(ctx, f.cmd.Process.Pid, f.spec)
	if err != nil {
		t.Fatal("evidence rejection harmed fixture", err)
	}
	defer h.Close()
	if err = h.SignalVerified(ctx, syscall.SIGSTOP); !errors.Is(err, ErrProcessUnverified) {
		t.Fatalf("unsupported signal accepted: %v", err)
	}
	if err = h.Verify(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProcessOriginalPidfdSignalAndReap(t *testing.T) {
	f := startProcessFixture(t)
	ctx := processTestContext(t)
	h, err := ObserveProcess(ctx, f.cmd.Process.Pid, f.spec)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err = h.SignalVerified(ctx, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err = h.WaitExited(ctx); err != nil {
		t.Fatal(err)
	}
	// pidfd readiness is separate from this actual parent Wait.
	err = f.cmd.Wait()
	f.waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("wrong fixture terminal status %v", err)
	}
	if err = h.Verify(ctx); !errors.Is(err, ErrProcessUnverified) {
		t.Fatalf("exited process verified %v", err)
	}
	if err = h.SignalVerified(ctx, syscall.SIGKILL); !errors.Is(err, ErrProcessUnverified) {
		t.Fatalf("exited process signaled %v", err)
	}
}

func TestProcessCancellationAndMissingPID(t *testing.T) {
	f := startProcessFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h, err := ObserveProcess(ctx, f.cmd.Process.Pid, f.spec); !errors.Is(err, context.Canceled) {
		if h != nil {
			h.Close()
		}
		t.Fatalf("cancel ignored %v", err)
	}
	for _, pid := range []int{0, -1} {
		if h, err := ObserveProcess(context.Background(), pid, f.spec); !errors.Is(err, ErrProcessUnverified) {
			if h != nil {
				h.Close()
			}
			t.Fatalf("bad PID accepted %d %v", pid, err)
		}
	}
	if err := f.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	f.cmd.Wait()
	f.waited = true
	if h, err := ObserveProcess(context.Background(), f.cmd.Process.Pid, f.spec); !errors.Is(err, ErrProcessUnverified) {
		if h != nil {
			h.Close()
		}
		t.Fatalf("missing PID became verified/absent claim %s %v", strconv.Itoa(f.cmd.Process.Pid), err)
	}
}
