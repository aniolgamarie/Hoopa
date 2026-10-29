//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/service"
)

var errP1Frontend = errors.New("P1_FRONTEND_LIFECYCLE_FAILED")

type p1FrontendBuffer struct {
	mu           sync.Mutex
	raw          []byte
	limit, total int
	overflow     bool
	cancel       context.CancelFunc
}

func (b *p1FrontendBuffer) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.raw)
	if remaining > len(raw) {
		remaining = len(raw)
	}
	b.raw = append(b.raw, raw[:remaining]...)
	b.total += len(raw)
	if remaining < len(raw) {
		b.overflow = true
		b.cancel()
	}
	return len(raw), nil
}
func (b *p1FrontendBuffer) snapshot() ([]byte, int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.raw...), b.total, b.overflow
}

type p1Frontend struct {
	mu                                    sync.Mutex
	cmd                                   *exec.Cmd
	stdin                                 io.WriteCloser
	stdinClosed                           bool
	cancel                                context.CancelFunc
	done                                  chan struct{}
	waitErr                               error
	stdout, stderr                        *p1FrontendBuffer
	directory                             *os.File
	artifactDir, requestID, executableSHA string
	request, begin                        originalRef
	finished, closed                      bool
}

func (*p1Frontend) String() string       { return "p1Frontend{private direct child}" }
func (*p1Frontend) GoString() string     { return "p1Frontend{private direct child}" }
func (p *p1Frontend) PID() int           { return p.cmd.Process.Pid }
func (p *p1Frontend) Begin() originalRef { return p.begin }
func (p *p1Frontend) original(name string, raw []byte) (originalRef, error) {
	file, err := p1TraceCreate(p.directory, name)
	if err != nil {
		return originalRef{}, errP1Frontend
	}
	n, err := file.Write(raw)
	if n != len(raw) {
		err = errors.Join(err, io.ErrShortWrite)
	}
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil || p.directory.Sync() != nil {
		return originalRef{}, errP1Frontend
	}
	return originalRef{Path: filepath.Join(p.artifactDir, name), Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, nil
}
func (p *p1Frontend) closeStdin() {
	if !p.stdinClosed {
		_ = p.stdin.Close()
		p.stdinClosed = true
	}
}

// The supplied transport must be the fixed actual built CLI, never a driver.
// exec.Cmd.Start's successful exec handshake precedes writing the complete valid
// request. The private stdin writer remains open, so CLI ReadAll waits for EOF.
// Only this Cmd's original direct child is a signal/cancellation target.
func startP1Frontend(cli *p1CLITransport, artifactDir, requestID string) (*p1Frontend, error) {
	if cli == nil || cli.driverMode != "" {
		return nil, errP1Frontend
	}
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": requestID, "operation": "status.get", "params": map[string]any{}})
	if err != nil {
		return nil, errP1Frontend
	}
	parsed, err := control.ParseRequest(raw)
	if err != nil || parsed.Version() != "v1" || parsed.Operation() != "status.get" {
		return nil, errP1Frontend
	}
	info, digest, err := p1CLIExecutable(cli.executable)
	if err != nil || !os.SameFile(info, cli.executableInfo) || digest != cli.executableSHA {
		return nil, errP1Frontend
	}
	directory, err := p1OpenObserveCapture(artifactDir)
	if err != nil {
		return nil, errP1Frontend
	}
	lifetime, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	p := &p1Frontend{directory: directory, artifactDir: artifactDir, requestID: parsed.ID(), executableSHA: digest, cancel: cancel, done: make(chan struct{})}
	p.stdout = &p1FrontendBuffer{limit: p1CLIStdoutLimit, cancel: cancel}
	p.stderr = &p1FrontendBuffer{limit: p1CLIStderrLimit, cancel: cancel}
	p.request, err = p.original("request.json", raw)
	if err != nil {
		cancel()
		directory.Close()
		return nil, errP1Frontend
	}
	cmd := exec.CommandContext(lifetime, cli.executable, "api", "v1", "status.get")
	cmd.Dir = cli.home
	cmd.Env = cli.environment()
	cmd.Stdout = p.stdout
	cmd.Stderr = p.stderr
	cmd.WaitDelay = 250 * time.Millisecond
	p.stdin, err = cmd.StdinPipe()
	if err != nil {
		cancel()
		directory.Close()
		return nil, errP1Frontend
	}
	p.cmd = cmd
	if err = cmd.Start(); err != nil {
		p.stdin.Close()
		cancel()
		directory.Close()
		return nil, errP1Frontend
	}
	go func() { p.waitErr = cmd.Wait(); close(p.done) }()
	n, err := p.stdin.Write(raw)
	if err != nil || n != len(raw) {
		cancel()
		p.closeStdin()
		<-p.done
		directory.Close()
		return nil, errP1Frontend
	}
	select {
	case <-p.done:
		cancel()
		p.closeStdin()
		directory.Close()
		return nil, errP1Frontend
	case <-time.After(25 * time.Millisecond):
	}
	stdout, _, outOverflow := p.stdout.snapshot()
	stderr, _, errOverflow := p.stderr.snapshot()
	if len(stdout) != 0 || len(stderr) != 0 || outOverflow || errOverflow {
		cancel()
		p.closeStdin()
		<-p.done
		directory.Close()
		return nil, errP1Frontend
	}
	begin, _ := json.Marshal(map[string]any{"version": 1, "kind": "actual-cli-frontend-began", "pid": cmd.Process.Pid, "executable": cli.executable, "executable_sha256": digest, "exec_evidence": "successful-exec.Cmd.Start-handshake", "request_id": p.requestID, "request_original": p.request, "stdin_complete_request_written": true, "stdin_eof_sent": false, "live_at_begin": true, "observed_at": time.Now().UTC(), "environment_provenance": "p1CLITransport.environment"})
	p.begin, err = p.original("begin.json", begin)
	if err != nil {
		cancel()
		p.closeStdin()
		<-p.done
		directory.Close()
		return nil, errP1Frontend
	}
	return p, nil
}
func (p *p1Frontend) Finish(ctx context.Context, mode string) (originalRef, error) {
	if p == nil || ctx == nil || mode != "normal" && mode != "abnormal" {
		return originalRef{}, errP1Frontend
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || p.closed || ctx.Err() != nil {
		return originalRef{}, errP1Frontend
	}
	select {
	case <-p.done:
		return originalRef{}, errP1Frontend
	default:
	}
	p.finished = true
	if mode == "normal" {
		p.closeStdin()
	} else {
		if err := p.cmd.Process.Kill(); err != nil {
			return originalRef{}, errP1Frontend
		}
		p.closeStdin()
	}
	bounded, cancel := context.WithTimeout(ctx, p1CLIRuntimeLimit)
	defer cancel()
	select {
	case <-p.done:
	case <-bounded.Done():
		p.cancel()
		p.closeStdin()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
		}
		return originalRef{}, errors.Join(errP1Frontend, bounded.Err())
	}
	p.cancel()
	stdout, stdoutTotal, stdoutOverflow := p.stdout.snapshot()
	stderr, stderrTotal, stderrOverflow := p.stderr.snapshot()
	outRef, err := p.original("stdout.raw", stdout)
	if err != nil {
		return originalRef{}, errP1Frontend
	}
	errRef, err := p.original("stderr.raw", stderr)
	if err != nil {
		return originalRef{}, errP1Frontend
	}
	state := p.cmd.ProcessState
	if state == nil {
		return originalRef{}, errP1Frontend
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return originalRef{}, errP1Frontend
	}
	valid := !stdoutOverflow && !stderrOverflow
	responseValidated := false
	businessCode := ""
	if mode == "normal" {
		body, code, rejected, e := validateP1CLIResponse(stdout, p.requestID)
		businessCode = code
		responseValidated = e == nil
		valid = valid && e == nil && !rejected && p.waitErr == nil && status.Exited() && state.ExitCode() == 0 && service.ExitCode(body) == 0
	} else {
		var exit *exec.ExitError
		valid = valid && errors.As(p.waitErr, &exit) && status.Signaled() && status.Signal() == syscall.SIGKILL
	}
	signal := ""
	if status.Signaled() {
		signal = status.Signal().String()
		if status.Signal() == syscall.SIGKILL {
			signal = "SIGKILL"
		}
	}
	receipt, _ := json.Marshal(map[string]any{"version": 1, "kind": "actual-cli-frontend-ended", "mode": mode, "pid": p.cmd.Process.Pid, "executable_sha256": p.executableSHA, "request_id": p.requestID, "request_original": p.request, "begin_original": p.begin, "stdout_original": outRef, "stderr_original": errRef, "stdout_observed_bytes": stdoutTotal, "stderr_observed_bytes": stderrTotal, "stdout_truncated": stdoutOverflow, "stderr_truncated": stderrOverflow, "wait_completed": true, "wait_status": int(status), "wait_exited": status.Exited(), "wait_signaled": status.Signaled(), "wait_signal": signal, "exit_code": state.ExitCode(), "response_validated": responseValidated, "business_error_code": businessCode, "lifecycle_validated": valid, "observed_at": time.Now().UTC()})
	ref, err := p.original("end.json", receipt)
	if err != nil {
		return originalRef{}, errP1Frontend
	}
	if !valid {
		return ref, errP1Frontend
	}
	return ref, nil
}
func (p *p1Frontend) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return errP1Frontend
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.cancel()
	p.closeStdin()
	select {
	case <-p.done:
	case <-ctx.Done():
		return errors.Join(errP1Frontend, ctx.Err())
	}
	p.closed = true
	return p.directory.Close()
}

func TestP1ProductFrontendActualNoCore(t *testing.T) {
	executable := os.Getenv("HOOPA_FRONTEND_CLI")
	artifacts := os.Getenv("HOOPA_FRONTEND_ARTIFACTS")
	if executable == "" || artifacts == "" {
		t.Fatal("explicit actual frontend CLI/artifacts required")
	}
	t.Setenv("HTTP_PROXY", "http://private-proxy-secret.invalid")
	t.Setenv("FRONTEND_PRIVATE_SENTINEL", "private-ambient-secret")
	for _, mode := range []string{"normal", "abnormal"} {
		t.Run(mode, func(t *testing.T) {
			home, err := os.MkdirTemp("/tmp", "hpf-")
			must(t, err)
			t.Cleanup(func() { must(t, os.RemoveAll(home)) })
			capture := filepath.Join(artifacts, mode)
			must(t, os.Mkdir(capture, 0700))
			cliCapture := filepath.Join(capture, "cli")
			must(t, os.Mkdir(cliCapture, 0700))
			cli, err := newP1CLITransport(executable, home, cliCapture)
			must(t, err)
			t.Cleanup(func() { must(t, cli.Close()) })
			store, err := config.OpenStore(cli.Paths(), true)
			must(t, err)
			must(t, store.Close())
			before := p1FrontendTree(t, cli.Paths())
			childDir := filepath.Join(capture, "frontend")
			must(t, os.Mkdir(childDir, 0700))
			requestID := "frontend-input-" + mode
			frontend, err := startP1Frontend(cli, childDir, requestID)
			must(t, err)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				must(t, frontend.Close(ctx))
			})
			verifyOriginal(t, frontend.Begin())
			select {
			case <-frontend.done:
				t.Fatal("actual CLI did not remain alive on non-EOF stdin")
			case <-time.After(50 * time.Millisecond):
			}
			if !reflect.DeepEqual(before, p1FrontendTree(t, cli.Paths())) {
				t.Fatal("pending frontend wrote Store before EOF")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ended, err := frontend.Finish(ctx, mode)
			must(t, err)
			verifyOriginal(t, ended)
			raw, err := os.ReadFile(ended.Path)
			must(t, err)
			var receipt struct {
				Mode      string      `json:"mode"`
				PID       int         `json:"pid"`
				RequestID string      `json:"request_id"`
				Signaled  bool        `json:"wait_signaled"`
				Signal    string      `json:"wait_signal"`
				ExitCode  int         `json:"exit_code"`
				Validated bool        `json:"response_validated"`
				Stdout    originalRef `json:"stdout_original"`
				Valid     bool        `json:"lifecycle_validated"`
			}
			must(t, json.Unmarshal(raw, &receipt))
			if receipt.Mode != mode || receipt.PID != frontend.PID() || receipt.RequestID != requestID || !receipt.Valid {
				t.Fatal("actual frontend wait/input provenance mismatch")
			}
			if mode == "normal" {
				if receipt.Signaled || receipt.ExitCode != 0 || !receipt.Validated {
					t.Fatal("normal frontend did not complete actual status")
				}
			} else {
				if !receipt.Signaled || receipt.Signal != "SIGKILL" || receipt.ExitCode != -1 || receipt.Validated || receipt.Stdout.Bytes != 0 {
					t.Fatal("abnormal frontend fabricated normal JSON/exit")
				}
			}
			if !reflect.DeepEqual(before, p1FrontendTree(t, cli.Paths())) {
				t.Fatal("actual normal/killed status frontend wrote Store or started proxy")
			}
			if _, err = frontend.Finish(ctx, mode); err == nil {
				t.Fatal("frontend lifecycle reused")
			}
			t.Logf("actual CLI frontend %s pid=%d exit=%d signal=%s response_validated=%v; immutable initialized Store, no core", mode, receipt.PID, receipt.ExitCode, receipt.Signal, receipt.Validated)
		})
	}
}
func p1FrontendTree(t *testing.T, paths config.Paths) map[string]fileSnapshot {
	t.Helper()
	out := map[string]fileSnapshot{}
	for _, root := range []string{paths.Config, paths.State, paths.Cache, paths.Runtime} {
		must(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			item := fileSnapshot{Kind: "directory", Mode: uint32(info.Mode()), ModifiedNS: info.ModTime().UnixNano()}
			if info.Mode().IsRegular() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				item.Kind = "regular"
				item.Bytes = int64(len(raw))
				item.SHA256 = p1SHA(raw)
			}
			out[path] = item
			return nil
		}))
	}
	return out
}
