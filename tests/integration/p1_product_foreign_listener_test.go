//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/platform"
)

const p1ForeignMarker = "HOOPA_P1_FOREIGN_LISTENER_DRIVER"
const p1ForeignEcho = "HOOPA_FOREIGN_ECHO_V1"

var errP1ForeignListener = errors.New("P1_FOREIGN_LISTENER_UNVERIFIED")

type p1ForeignBuffer struct {
	mu       sync.Mutex
	raw      []byte
	total    int
	overflow bool
	ready    chan struct{}
	once     sync.Once
}

func (b *p1ForeignBuffer) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += len(raw)
	remaining := 4096 - len(b.raw)
	if remaining > len(raw) {
		remaining = len(raw)
	}
	if remaining > 0 {
		b.raw = append(b.raw, raw[:remaining]...)
	}
	if remaining < len(raw) {
		b.overflow = true
	}
	if b.ready != nil && (bytes.Contains(b.raw, []byte{'\n'}) || b.overflow) {
		b.once.Do(func() { close(b.ready) })
	}
	return len(raw), nil
}
func (b *p1ForeignBuffer) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.raw), b.overflow
}

type p1ForeignListener struct {
	mu                                                                sync.Mutex
	cmd                                                               *exec.Cmd
	stdin                                                             io.WriteCloser
	stdout, stderr                                                    *p1ForeignBuffer
	waitDone                                                          chan struct{}
	waitErr                                                           error
	emergencyFD                                                       int
	handle                                                            *platform.ProcessHandle
	identity                                                          platform.ProcessIdentity
	address, workdir, captureDir, executable, executableSHA, nonceSHA string
	serial                                                            int
	begin, end                                                        originalRef
	closed                                                            bool
	emergencySignal                                                   bool
}

func (p *p1ForeignListener) Address() string    { return p.address }
func (p *p1ForeignListener) Begin() originalRef { return p.begin }
func (p *p1ForeignListener) ExitOriginal() originalRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.end
}

func startP1ForeignListener(ctx context.Context, address, workdir, captureDir string) (result *p1ForeignListener, resultErr error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, errP1ForeignListener
	}
	host, _, splitErr := net.SplitHostPort(address)
	if splitErr != nil || host != "127.0.0.1" {
		return nil, errP1ForeignListener
	}
	endpoint, err := net.ResolveTCPAddr("tcp4", address)
	if err != nil || endpoint.IP == nil || !endpoint.IP.Equal(net.IPv4(127, 0, 0, 1)) || endpoint.Port < 0 || endpoint.Port > 65535 {
		return nil, errP1ForeignListener
	}
	for _, path := range []string{workdir, captureDir} {
		dir, err := p1OpenObserveCapture(path)
		if err != nil {
			return nil, errP1ForeignListener
		}
		if path == captureDir {
			names, e := dir.Readdirnames(1)
			if len(names) != 0 || e != io.EOF {
				dir.Close()
				return nil, errP1ForeignListener
			}
		}
		if dir.Close() != nil {
			return nil, errP1ForeignListener
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, errP1ForeignListener
	}
	_, pin, err := p1CLIExecutable(executable)
	if err != nil {
		return nil, errP1ForeignListener
	}
	listener, err := net.ListenTCP("tcp4", endpoint)
	if err != nil {
		return nil, errP1ForeignListener
	}
	defer listener.Close()
	inherited, err := listener.File()
	if err != nil {
		return nil, errP1ForeignListener
	}
	defer inherited.Close()
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return nil, errP1ForeignListener
	}
	nonce := hex.EncodeToString(token)
	p := &p1ForeignListener{address: listener.Addr().String(), workdir: workdir, captureDir: captureDir, executable: executable, executableSHA: pin, nonceSHA: p1SHA([]byte(nonce)), emergencyFD: -1, stdout: &p1ForeignBuffer{ready: make(chan struct{})}, stderr: &p1ForeignBuffer{}, waitDone: make(chan struct{})}
	cmd := exec.Command(executable, "-test.run=^TestP1ForeignListenerDriver$", "-test.timeout=130s")
	p.cmd = cmd
	cmd.Dir = workdir
	cmd.Env = []string{p1ForeignMarker + "=1", "HOOPA_RUN_NONCE=" + nonce, "HOME=" + workdir, "TMPDIR=" + workdir, "LANG=C"}
	cmd.ExtraFiles = []*os.File{inherited}
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	p.stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, errP1ForeignListener
	}
	if ctx.Err() != nil {
		p.stdin.Close()
		return nil, ctx.Err()
	}
	if err = cmd.Start(); err != nil {
		p.stdin.Close()
		return nil, errP1ForeignListener
	}
	// Pin only this original direct child for failure cleanup even if the full
	// independent ownership observer cannot establish a verified handle.
	fd, _, errno := syscall.Syscall(434, uintptr(cmd.Process.Pid), 0, 0)
	if errno == 0 {
		p.emergencyFD = int(fd)
		syscall.CloseOnExec(p.emergencyFD)
	}
	go func() { p.waitErr = cmd.Wait(); close(p.waitDone) }()
	listener.Close()
	inherited.Close() // parent must not retain listener ownership
	defer func() {
		if resultErr != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, p.Close(cleanup))
		}
	}()
	if errno != 0 {
		return nil, errP1ForeignListener
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.handle, err = platform.ObserveProcess(bounded, cmd.Process.Pid, platform.ProcessSpec{BinaryPath: executable, BinarySHA256: pin, Workdir: workdir, NonceSHA256: p.nonceSHA})
	if err != nil {
		return nil, errP1ForeignListener
	}
	p.identity = p.handle.Identity()
	select {
	case <-p.stdout.ready:
	case <-p.waitDone:
		return nil, errP1ForeignListener
	case <-bounded.Done():
		return nil, bounded.Err()
	}
	raw, overflow := p.stdout.snapshot()
	line, _, found := bytes.Cut(raw, []byte{'\n'})
	var ready struct {
		Version int    `json:"version"`
		Address string `json:"address"`
	}
	if overflow || !found || !p1HistoryJSON(line, &ready, "version", "address") || ready.Version != 1 || ready.Address != p.address {
		return nil, errP1ForeignListener
	}
	p.begin, err = p.original("begin.json", map[string]any{"version": 1, "kind": "original-direct-foreign-listener-start", "pid": cmd.Process.Pid, "identity": p.identity, "address": p.address, "executable_path": executable, "executable_sha256": pin, "workdir": workdir, "nonce_sha256": p.nonceSHA, "ready_sha256": p1SHA(line), "parent_listener_copies_closed": true, "scope": "test-only foreign process, no product RunID or activation proof"})
	if err != nil {
		return nil, err
	}
	return p, nil
}
func (p *p1ForeignListener) original(name string, value any) (originalRef, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return originalRef{}, errP1ForeignListener
	}
	return p1WriteOriginal(filepath.Join(p.captureDir, name), append(raw, '\n'))
}
func (p *p1ForeignListener) Observe(ctx context.Context) (platform.ProcessIdentity, originalRef, error) {
	if p == nil || ctx == nil {
		return platform.ProcessIdentity{}, originalRef{}, errP1ForeignListener
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fail := func() (platform.ProcessIdentity, originalRef, error) {
		if ctx.Err() != nil {
			return platform.ProcessIdentity{}, originalRef{}, errors.Join(errP1ForeignListener, ctx.Err())
		}
		return platform.ProcessIdentity{}, originalRef{}, errP1ForeignListener
	}
	if p.closed || p.handle == nil || ctx.Err() != nil {
		return fail()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if p.handle.Verify(bounded) != nil {
		return fail()
	}
	conn, err := p.handle.DialOwnedTCP(bounded, p.address)
	if err != nil {
		return fail()
	}
	defer conn.Close()
	deadline, _ := bounded.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return fail()
	}
	stop := context.AfterFunc(bounded, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	if n, err := io.WriteString(conn, p1ForeignEcho); err != nil || n != len(p1ForeignEcho) {
		return fail()
	}
	echo := make([]byte, len(p1ForeignEcho))
	if _, err = io.ReadFull(conn, echo); err != nil || string(echo) != p1ForeignEcho {
		return fail()
	}
	if err = p.handle.Verify(bounded); err != nil || bounded.Err() != nil {
		return fail()
	}
	p.serial++
	ref, err := p.original(fmt.Sprintf("observation-%06d.json", p.serial), map[string]any{"version": 1, "kind": "fresh-original-pidfd-connected-foreign-listener-observation", "identity": p.identity, "address": p.address, "begin_original": p.begin, "owned_connection_proved": true, "payload_bytes": len(p1ForeignEcho), "payload_sha256": p1SHA([]byte(p1ForeignEcho)), "echo_sha256": p1SHA(echo), "observed_at": time.Now().UTC(), "scope": "actual test child accepted socket, no product RunID"})
	if err != nil {
		return fail()
	}
	return p.identity, ref, nil
}
func (p *p1ForeignListener) Close(ctx context.Context) (resultErr error) {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return errP1ForeignListener
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	_ = p.stdin.Close()
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case <-p.waitDone:
	case <-bounded.Done():
		if p.emergencyFD >= 0 {
			_, _, errno := syscall.Syscall6(424, uintptr(p.emergencyFD), uintptr(syscall.SIGKILL), 0, 0, 0, 0)
			if errno != 0 && errno != syscall.ESRCH {
				return errP1ForeignListener
			}
			p.emergencySignal = errno == 0
		}
		cleanup, finish := context.WithTimeout(context.Background(), 3*time.Second)
		defer finish()
		select {
		case <-p.waitDone:
		case <-cleanup.Done():
			return errP1ForeignListener
		}
	}
	defer func() {
		if p.handle != nil {
			resultErr = errors.Join(resultErr, p.handle.Close())
		}
		if p.emergencyFD >= 0 {
			resultErr = errors.Join(resultErr, syscall.Close(p.emergencyFD))
			p.emergencyFD = -1
		}
		p.closed = true
	}()
	cleanup, finish := context.WithTimeout(context.Background(), 2*time.Second)
	defer finish()
	exited := p.handle != nil && p.handle.WaitExited(cleanup) == nil
	out, outOverflow := p.stdout.snapshot()
	stderr, errOverflow := p.stderr.snapshot()
	outRef, err := p1WriteOriginal(filepath.Join(p.captureDir, "stdout.raw"), out)
	if err != nil {
		return err
	}
	stderrRef, err := p1WriteOriginal(filepath.Join(p.captureDir, "stderr.raw"), stderr)
	if err != nil {
		return err
	}
	state := p.cmd.ProcessState
	if state == nil {
		return errP1ForeignListener
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return errP1ForeignListener
	}
	p.end, err = p.original("end.json", map[string]any{"version": 1, "kind": "original-direct-foreign-listener-wait-and-pidfd-exit", "pid": p.cmd.Process.Pid, "identity": p.identity, "begin_original": p.begin, "wait_status": int(status), "exit_code": state.ExitCode(), "wait_exited": status.Exited(), "wait_signaled": status.Signaled(), "pidfd_exit_observed": exited, "emergency_original_pidfd_signal": p.emergencySignal, "stdout_original": outRef, "stderr_original": stderrRef, "stdout_truncated": outOverflow, "stderr_truncated": errOverflow})
	if err != nil {
		return err
	}
	if err != nil || !exited || p.emergencySignal || p.waitErr != nil || !status.Exited() || status.ExitStatus() != 0 || outOverflow || errOverflow {
		return errP1ForeignListener
	}
	return nil
}

// Only this explicit disposable child does work. An ordinary test invocation
// returns without acting and is not counted as actual listener coverage.
func TestP1ForeignListenerDriver(t *testing.T) {
	if os.Getenv(p1ForeignMarker) != "1" {
		return
	}
	nonce := os.Getenv("HOOPA_RUN_NONCE")
	decoded, err := hex.DecodeString(nonce)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != nonce {
		t.Fatal("invalid private driver setup")
	}
	inherited := os.NewFile(3, "foreign-listener")
	listener, err := net.FileListener(inherited)
	inherited.Close()
	if err != nil {
		t.Fatal("inherited listener unavailable")
	}
	defer listener.Close()
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	stop := make(chan struct{})
	go func() { defer close(stop); _, _ = io.Copy(io.Discard, os.Stdin) }()
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				raw := make([]byte, len(p1ForeignEcho))
				if _, err := io.ReadFull(conn, raw); err == nil && string(raw) == p1ForeignEcho {
					_, _ = conn.Write(raw)
				}
			}()
		}
	}()
	raw, _ := json.Marshal(map[string]any{"version": 1, "address": listener.Addr().String()})
	if _, err = os.Stdout.Write(append(raw, '\n')); err != nil {
		t.Fatal("ready write failed")
	}
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-stop:
	case <-timer.C:
	}
	listener.Close()
	<-accepted
	mu.Lock()
	for conn := range connections {
		conn.Close()
	}
	mu.Unlock()
	workers.Wait()
}

func TestP1ForeignListenerActual(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	work, captures := t.TempDir(), t.TempDir()
	if retained := os.Getenv("HOOPA_P1_FOREIGN_LISTENER_ARTIFACTS"); retained != "" {
		if !filepath.IsAbs(retained) {
			t.Fatal("absolute foreign fixture artifact directory required")
		}
		captures = retained
	}
	must(t, os.Chmod(work, 0700))
	must(t, os.Chmod(captures, 0700))
	p, err := startP1ForeignListener(ctx, "127.0.0.1:0", work, captures)
	must(t, err)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	id, ref, err := p.Observe(ctx)
	must(t, err)
	if id.PID != p.cmd.Process.Pid || id.BinarySHA256 != p.executableSHA || id.NonceSHA256 != p.nonceSHA || ref.Path == "" {
		t.Fatal("foreign identity/connection proof missing")
	}
	_, _, err = p.Observe(ctx)
	must(t, err)
	must(t, p.Close(ctx))
	if p.emergencySignal || p.end.Path == "" {
		t.Fatal("normal close lacked natural EOF/pidfd exit")
	}
	if _, err = os.Stat(fmt.Sprintf("/proc/%d", id.PID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original foreign process not reaped")
	}
	if got, _, err := p.Observe(ctx); err == nil || got.PID != 0 {
		t.Fatal("closed child fabricated live observation")
	}
	t.Logf("actual foreign child: original-pidfd verified, owned TCP echo twice, stdin EOF exit0, no emergency signal")
}
func TestP1ForeignListenerRejects(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "localhost:0", "127.0.0.2:0", "[::1]:0", "127.0.0.1:99999"} {
		t.Run(strings.ReplaceAll(address, ":", "-"), func(t *testing.T) {
			if p, err := startP1ForeignListener(context.Background(), address, t.TempDir(), t.TempDir()); err == nil || p != nil {
				t.Fatal("nonfixed-loopback address accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := startP1ForeignListener(ctx, "127.0.0.1:0", t.TempDir(), t.TempDir()); err == nil || p != nil {
		t.Fatal("canceled start accepted")
	}
}
