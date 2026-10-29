//go:build linux && amd64

package launch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Reserved argv is test-binary-only application fixture dispatch. It never
// substitutes for the product runner/CLI, and adds no no-op helper test PASS.
func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "__hoopa_t056_child":
		time.AfterFunc(12*time.Second, func() { os.Exit(88) })
		name := []byte("proc) (fixture\x00")
		_, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, 15, uintptr(unsafe.Pointer(&name[0])), 0, 0, 0, 0)
		fg, _ := launchForeground(0)
		ttys := []bool{}
		for _, fd := range []uintptr{0, 1, 2} {
			var term syscall.Termios
			_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
			ttys = append(ttys, err == 0)
		}
		componentEvent("ready", map[string]any{"pid": os.Getpid(), "pgrp": syscall.Getpgrp(), "foreground": fg, "ttys": ttys})
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			os.Exit(89)
		}
		componentEvent("input", map[string]any{"line": line})
		os.Exit(17)
	case "__hoopa_t056_sentinel":
		componentEvent("ready", map[string]any{"pid": os.Getpid(), "pgrp": syscall.Getpgrp()})
		time.Sleep(3 * time.Second)
		componentEvent("sentinel_exit", nil)
		os.Exit(0)
	case "__hoopa_t056_terminal":
		code := componentTerminalWrapper(os.Args[2])
		os.Exit(code)
	}
}
func componentEvent(event string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = event
	raw, _ := json.Marshal(fields)
	_, _ = os.Stdout.Write(append(raw, '\n'))
}

type componentChild struct {
	t      *testing.T
	cmd    *exec.Cmd
	fd     int
	input  *os.File
	reader *bufio.Reader
	output *os.File
	waited bool
}

func newComponentChild(t *testing.T, mode string, group int) *componentChild {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	cmd := exec.Command(self, mode)
	cmd.Stdin = inputRead
	cmd.Stdout = outputWrite
	cmd.Stderr = outputWrite
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group, PidFD: &fd}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	inputRead.Close()
	outputWrite.Close()
	if fd < 0 {
		t.Fatal("required original clone pidfd unavailable")
	}
	child := &componentChild{t: t, cmd: cmd, fd: fd, input: inputWrite, reader: bufio.NewReader(outputRead), output: outputRead}
	t.Cleanup(child.cleanup)
	var ready map[string]any
	raw, err := child.reader.ReadBytes('\n')
	if err != nil || json.Unmarshal(raw, &ready) != nil || ready["event"] != "ready" {
		t.Fatal("actual owned fixture ready barrier missing")
	}
	return child
}
func (c *componentChild) wait(want uint32) {
	c.t.Helper()
	if c.waited {
		c.t.Fatal("duplicate fixture Wait")
	}
	err := c.cmd.Wait()
	c.waited = true
	if err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			c.t.Fatal("nonterminal fixture wait", err)
		}
	}
	status, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || uint32(status) != want {
		c.t.Fatal("actual fixture terminal mismatch", status, want)
	}
}
func (c *componentChild) cleanup() {
	if !c.waited {
		// This child is solely a fixed owned test helper, never an authorized user
		// application. Exact raw-pidfd TERM/TTL cleanup is not a success witness.
		_ = signalOriginalChild(c.fd, syscall.SIGTERM)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := observeChildExit(ctx, c.fd)
		cancel()
		if err != nil {
			c.t.Error("fixture cleanup failed to observe original terminal", err)
		}
		_ = c.cmd.Wait()
		c.waited = true
	}
	c.input.Close()
	c.output.Close()
	syscall.Close(c.fd)
}
func TestLaunchProcessCurrentAndBoundedIdentityParsing(t *testing.T) {
	current, err := currentIdentity()
	if err != nil || current.PID != os.Getpid() || current.UID != uint32(os.Geteuid()) || current.PPID != os.Getppid() || current.ProcessGroup != syscall.Getpgrp() || !launchBootID(current.BootID) || current.StartTicks == 0 {
		t.Fatal("current kernel identity incomplete", err)
	}
	boot, readErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if readErr != nil || strings.TrimSpace(string(boot)) != current.BootID {
		t.Fatal("boot identity differs from kernel original")
	}
	for _, pid := range []int{-1, 0, 2147483648} {
		if _, err := childIdentity(pid); !errors.Is(err, ErrLaunchProcess) {
			t.Fatal("invalid PID accepted")
		}
	}
	fields := []string{"Z", "22", "33", "44"}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}
	fields = append(fields, "567")
	raw := []byte("123 (private) (comm) " + strings.Join(fields, " ") + "\n")
	parsed, err := parseLaunchStat(raw, 123)
	if err != nil || parsed.identity.PPID != 22 || parsed.identity.ProcessGroup != 33 || parsed.identity.Session != 44 || parsed.identity.StartTicks != 567 || parsed.state != "Z" {
		t.Fatal("bounded stat did not handle parentheses/zombie", err)
	}
	for _, bad := range [][]byte{nil, []byte("private broken stat"), []byte("123 (x) Z"), []byte(strings.Repeat("x", 8193)), []byte(strings.Replace(string(raw), "567", "0", 1)), []byte(strings.Replace(string(raw), "567", "18446744073709551616", 1))} {
		if _, err := parseLaunchStat(bad, 123); !errors.Is(err, ErrLaunchProcess) {
			t.Fatal("malformed stat accepted")
		}
	}
	uid, err := launchEffectiveUID([]byte("Name:\tprivate\nUid:\t1\t2\t3\t4\n"))
	if err != nil || uid != 2 {
		t.Fatal("effective UID not selected", err)
	}
	for _, raw := range []string{"", "Uid: 1 2 3\n", "Uid: 1 4294967296 3 4\n", "Uid: 1 2 3 4\nUid: 1 2 3 4\n"} {
		if _, err := launchEffectiveUID([]byte(raw)); !errors.Is(err, ErrLaunchProcess) {
			t.Fatal("malformed UID accepted")
		}
	}
}
func TestLaunchProcessOriginalSignalAndObservationDoNotReapOrBroadcast(t *testing.T) {
	child := newComponentChild(t, "__hoopa_t056_child", 0)
	birth, err := childIdentity(child.cmd.Process.Pid)
	if err != nil || birth.PID != child.cmd.Process.Pid || birth.PPID != os.Getpid() || birth.ProcessGroup != birth.PID {
		t.Fatal("original child identity", err)
	}
	sentinel := newComponentChild(t, "__hoopa_t056_sentinel", birth.PID)
	sibling, err := childIdentity(sentinel.cmd.Process.Pid)
	if err != nil || sibling.ProcessGroup != birth.ProcessGroup {
		t.Fatal("same-group sentinel not established", err)
	}
	if err = signalOriginalChild(child.fd, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = observeChildExit(ctx, child.fd); err != nil {
		t.Fatal(err)
	}
	held, err := readLaunchProcess(birth.PID)
	if err != nil || held.state != "Z" || held.identity != birth {
		t.Fatal("observe reaped/substituted original child", err)
	}
	liveCtx, liveCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err = observeChildExit(liveCtx, sentinel.fd)
	liveCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("original-child signal affected same-group sentinel", err)
	}
	child.wait(uint32(syscall.SIGTERM))
	err = signalOriginalChild(child.fd, syscall.SIGTERM)
	if !errors.Is(err, ErrLaunchProcess) || !errors.Is(err, syscall.ESRCH) || err.Error() != ErrLaunchProcess.Error() {
		t.Fatal("terminal race did not retain opaque ESRCH class", err)
	}
	if err = observeChildExit(ctx, sentinel.fd); err != nil {
		t.Fatal("sentinel failed own TTL", err)
	}
	sentinel.wait(0)
	t.Logf("raw-pidfd TERM: child PID=%d pgrp=%d status=%d remained Z before Wait; same-group sentinel PID=%d own-TTL status0", birth.PID, birth.ProcessGroup, syscall.SIGTERM, sibling.PID)
}
func TestLaunchProcessCancellationAndBadDescriptors(t *testing.T) {
	child := newComponentChild(t, "__hoopa_t056_child", 0)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := observeChildExit(canceled, child.fd); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancel not returned", err)
	}
	ctx, done := context.WithTimeout(context.Background(), 20*time.Millisecond)
	start := time.Now()
	err := observeChildExit(ctx, child.fd)
	done()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("poll cancellation not bounded", err)
	}
	if snapshot, err := readLaunchProcess(child.cmd.Process.Pid); err != nil || snapshot.state == "Z" {
		t.Fatal("cancellation signaled or reaped child", err)
	}
	for _, fd := range []int{-1, 2147483648} {
		if err := observeChildExit(context.Background(), fd); !errors.Is(err, ErrLaunchProcess) {
			t.Fatal("bad poll fd accepted")
		}
	}
	if err := observeChildExit(nil, child.fd); !errors.Is(err, ErrLaunchProcess) {
		t.Fatal("nil context accepted")
	}
	for _, sig := range []syscall.Signal{0, -1, 65} {
		if err := signalOriginalChild(child.fd, sig); !errors.Is(err, ErrLaunchProcess) {
			t.Fatal("invalid signal accepted")
		}
	}
	err = signalOriginalChild(-1, syscall.SIGTERM)
	if !errors.Is(err, ErrLaunchProcess) {
		t.Fatal("bad signal fd accepted")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	err = signalOriginalChild(int(null.Fd()), syscall.SIGTERM)
	if !errors.Is(err, ErrLaunchProcess) || err == nil || strings.Contains(fmt.Sprintf("%+v %#v", err, err), "/dev/") {
		t.Fatal("non-pidfd signal/error privacy boundary", err)
	}
	if _, err := child.input.Write([]byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	live, cancelLive := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelLive()
	if err := observeChildExit(live, child.fd); err != nil {
		t.Fatal(err)
	}
	child.wait(17 << 8)
	// Cancellation wins even if the original pidfd is already ready.
	if err := observeChildExit(canceled, child.fd); !errors.Is(err, context.Canceled) {
		t.Fatal("ready raced past canceled context", err)
	}
}

// Kept as a simple bounded event value reader for the actual PTY helper.
func componentInteger(value map[string]json.RawMessage, key string) (int, error) {
	var result int
	err := json.Unmarshal(value[key], &result)
	if err != nil || result <= 0 {
		return 0, ErrLaunchProcess
	}
	return result, nil
}
