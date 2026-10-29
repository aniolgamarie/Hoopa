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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	"hoopa/internal/service"
	"hoopa/internal/testsupport/p1session"
	"hoopa/internal/testsupport/p1trace"
)

var errP1SessionTrace = errors.New("P1_SESSION_TRACE_UNVERIFIED")

type p1SessionBroker struct {
	mu                                                              sync.Mutex
	listener                                                        *net.UnixListener
	path, stream, dir, probe, probeSHA, workdir, nonceSHA, captures string
	probeInfo                                                       os.FileInfo
	paths                                                           config.Paths
	decide                                                          func(p1TraceRecord) p1TraceDecision
	used, closed                                                    bool
}
type p1SessionTrace struct {
	mu                            sync.RWMutex
	exitMu                        sync.Mutex
	lifecycle                     sync.Mutex
	paths                         config.Paths
	handle                        *platform.ProcessHandle
	collector                     *p1trace.Collector
	socket                        *os.File
	captures                      string
	identity                      platform.ProcessIdentity
	parentPID, sessionID, groupID int
	birth, exit                   originalRef
	signals                       []originalRef
	records                       []p1TraceRecord
	traceErr                      error
	traceEOF                      bool
	done                          chan struct{}
	cancel                        context.CancelFunc
	decide                        func(p1TraceRecord) p1TraceDecision
	closed                        bool
}

func (b *p1SessionBroker) Path() string   { return b.path }
func (b *p1SessionBroker) Stream() string { return b.stream }
func startP1SessionBroker(paths config.Paths, probePath, workdir, nonceSHA, captureDir string, decide func(p1TraceRecord) p1TraceDecision) (*p1SessionBroker, error) {
	if paths.Validate() != nil || !operationDigest(nonceSHA) {
		return nil, errP1SessionTrace
	}
	for _, path := range []string{workdir, captureDir} {
		dir, err := p1OpenObserveCapture(path)
		if err != nil {
			return nil, errP1SessionTrace
		}
		if path == captureDir {
			names, e := dir.Readdirnames(1)
			if len(names) != 0 || e != io.EOF {
				dir.Close()
				return nil, errP1SessionTrace
			}
		}
		if dir.Close() != nil {
			return nil, errP1SessionTrace
		}
	}
	info, sha, err := p1CLIExecutable(probePath)
	if err != nil {
		return nil, errP1SessionTrace
	}
	dir, err := os.MkdirTemp(workdir, "sb-")
	if err != nil {
		return nil, errP1SessionTrace
	}
	path := filepath.Join(dir, "b")
	if len(path) > 107 {
		os.Remove(dir)
		return nil, errP1SessionTrace
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		os.Remove(dir)
		return nil, errP1SessionTrace
	}
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		os.Remove(dir)
		return nil, errP1SessionTrace
	}
	if os.Chmod(path, 0600) != nil {
		listener.Close()
		os.Remove(dir)
		return nil, errP1SessionTrace
	}
	return &p1SessionBroker{listener: listener, path: path, dir: dir, stream: hex.EncodeToString(token), probe: probePath, probeSHA: sha, probeInfo: info, workdir: workdir, nonceSHA: nonceSHA, captures: captureDir, paths: paths, decide: decide}, nil
}
func (b *p1SessionBroker) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	return errors.Join(b.listener.Close(), os.Remove(b.dir))
}
func (b *p1SessionBroker) Accept(ctx context.Context) (result *p1SessionTrace, resultErr error) {
	if b == nil || ctx == nil || ctx.Err() != nil {
		return nil, errP1SessionTrace
	}
	b.mu.Lock()
	if b.used || b.closed {
		b.mu.Unlock()
		return nil, errP1SessionTrace
	}
	b.used = true
	b.mu.Unlock()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	deadline, _ := bounded.Deadline()
	b.listener.SetDeadline(deadline)
	stop := context.AfterFunc(bounded, func() { b.listener.SetDeadline(time.Now()) })
	defer stop()
	conn, err := b.listener.AcceptUnix()
	if err != nil {
		return nil, errP1SessionTrace
	}
	defer conn.Close()
	conn.SetDeadline(deadline)
	stopConnection := context.AfterFunc(bounded, func() { conn.SetDeadline(time.Now()) })
	defer stopConnection()
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, errP1SessionTrace
	}
	var peer *syscall.Ucred
	var peerErr error
	if raw.Control(func(fd uintptr) {
		peer, peerErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}) != nil || peerErr != nil || peer == nil || peer.Pid <= 0 || int(peer.Uid) != os.Geteuid() {
		return nil, errP1SessionTrace
	}
	data, oob := make([]byte, p1session.MaxPacket+1), make([]byte, syscall.CmsgSpace(16*4))
	n, on, flags, _, err := conn.ReadMsgUnix(data, oob)
	messages, parseErr := syscall.ParseSocketControlMessage(oob[:on])
	for _, message := range messages {
		if message.Header.Level == syscall.SOL_SOCKET && message.Header.Type == syscall.SCM_RIGHTS {
			fds, _ := syscall.ParseUnixRights(&message)
			for _, fd := range fds {
				syscall.Close(fd)
			}
		}
	}
	if err != nil || parseErr != nil || on != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 || n == 0 || n > p1session.MaxPacket {
		return nil, errP1SessionTrace
	}
	var hello p1session.Request
	if !p1HistoryJSON(data[:n], &hello, "version", "stream", "manager_pid", "manager_parent_pid") || hello.Version != 1 || hello.Stream != b.stream || hello.ManagerPID != int(peer.Pid) || hello.ManagerParentPID <= 0 {
		return nil, errP1SessionTrace
	}
	info, sha, err := p1CLIExecutable(b.probe)
	if err != nil || sha != b.probeSHA || !os.SameFile(b.probeInfo, info) {
		return nil, errP1SessionTrace
	}
	handle, err := platform.ObserveProcess(bounded, int(peer.Pid), platform.ProcessSpec{BinaryPath: b.probe, BinarySHA256: b.probeSHA, Workdir: b.workdir, NonceSHA256: b.nonceSHA})
	if err != nil {
		return nil, errP1SessionTrace
	}
	owned := true
	defer func() {
		if owned {
			handle.Close()
		}
	}()
	args := []string{b.probe, "session-trace", b.path, b.stream, strconv.Itoa(os.Getpid()), "service"}
	if handle.VerifyArguments(bounded, args) != nil {
		return nil, errP1SessionTrace
	}
	statFile, err := os.Open(fmt.Sprintf("/proc/%d/stat", peer.Pid))
	if err != nil {
		return nil, errP1SessionTrace
	}
	statRaw, readErr := io.ReadAll(io.LimitReader(statFile, 4097))
	closeErr := statFile.Close()
	if readErr != nil || closeErr != nil || len(statRaw) > 4096 {
		return nil, errP1SessionTrace
	}
	parent, pgrp, session, ticks, err := p1SessionStat(statRaw, int(peer.Pid))
	identity := handle.Identity()
	if err != nil || parent != hello.ManagerParentPID || ticks != identity.StartTicks || handle.Verify(bounded) != nil {
		return nil, errP1SessionTrace
	}
	statRef, err := p1WriteOriginal(filepath.Join(b.captures, "birth.proc-stat.raw"), statRaw)
	if err != nil {
		return nil, err
	}
	requestRef, err := p1WriteOriginal(filepath.Join(b.captures, "bootstrap-request.json"), data[:n])
	if err != nil {
		return nil, err
	}
	birth, err := p1WriteOriginal(filepath.Join(b.captures, "birth.json"), p1SessionJSON(map[string]any{"version": 1, "kind": "actual-session-manager-peer-proc-and-original-pidfd", "identity": identity, "actual_peer_pid": peer.Pid, "actual_peer_uid": peer.Uid, "manager_parent_pid": parent, "process_group_id": pgrp, "session_id": session, "actual_stat_original": statRef, "request_original": requestRef, "argv": args, "wait_status_observed": false, "scope": "actual process facts; SSH/session ancestry belongs to root supervisor"}))
	if err != nil {
		return nil, err
	}
	traceDir := filepath.Join(b.captures, "trace")
	if err = os.Mkdir(traceDir, 0700); err != nil {
		return nil, err
	}
	a, c, err := p1trace.SocketPair()
	if err != nil {
		return nil, errP1SessionTrace
	}
	defer c.Close()
	collector, err := p1trace.NewCollector(a, int(peer.Pid), int(peer.Uid), b.stream, traceDir)
	if err != nil {
		a.Close()
		return nil, errP1SessionTrace
	}
	success := false
	defer func() {
		if !success {
			collector.Close()
			a.Close()
		}
	}()
	response, _ := json.Marshal(p1session.Response{Version: 1, Stream: b.stream})
	if count, _, err := conn.WriteMsgUnix(response, syscall.UnixRights(int(c.Fd())), nil); err != nil || count != len(response) {
		return nil, errP1SessionTrace
	}
	conn.Close()
	c.Close()
	collectContext, collectCancel := context.WithCancel(context.Background())
	result = &p1SessionTrace{paths: b.paths, handle: handle, collector: collector, socket: a, captures: b.captures, identity: identity, parentPID: parent, sessionID: session, groupID: pgrp, birth: birth, done: make(chan struct{}), cancel: collectCancel, decide: b.decide}
	owned = false
	success = true
	go result.collect(collectContext)
	return result, nil
}
func p1SessionJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return append(raw, '\n')
}
func p1SessionStat(raw []byte, pid int) (parent, group, session int, ticks uint64, resultErr error) {
	fail := func() (int, int, int, uint64, error) { return 0, 0, 0, 0, errP1SessionTrace }
	opening, closing := bytes.IndexByte(raw, '('), bytes.LastIndexByte(raw, ')')
	if opening <= 0 || closing <= opening {
		return fail()
	}
	actual, err := strconv.Atoi(strings.TrimSpace(string(raw[:opening])))
	if err != nil || actual != pid {
		return fail()
	}
	fields := strings.Fields(string(raw[closing+1:]))
	if len(fields) < 20 {
		return fail()
	}
	parent, e1 := strconv.Atoi(fields[1])
	group, e2 := strconv.Atoi(fields[2])
	session, e3 := strconv.Atoi(fields[3])
	ticks, e4 := strconv.ParseUint(fields[19], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || parent <= 0 || group <= 0 || session <= 0 || ticks == 0 {
		return fail()
	}
	return parent, group, session, ticks, nil
}
func (p *p1SessionTrace) PID() int                           { return p.identity.PID }
func (p *p1SessionTrace) Identity() platform.ProcessIdentity { return p.identity }
func (p *p1SessionTrace) BirthOriginal() originalRef         { return p.birth }
func (p *p1SessionTrace) ExitOriginal() originalRef {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exit
}
func (p *p1SessionTrace) SignalOriginals() []originalRef {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]originalRef(nil), p.signals...)
}
func (p *p1SessionTrace) Records() []p1TraceRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]p1TraceRecord, len(p.records))
	for i, record := range p.records {
		out[i] = copyP1TraceRecord(record)
	}
	return out
}
func (p *p1SessionTrace) TraceError() error { p.mu.RLock(); defer p.mu.RUnlock(); return p.traceErr }
func (p *p1SessionTrace) fail(err error) {
	p.mu.Lock()
	p.traceErr = errors.Join(p.traceErr, err)
	p.mu.Unlock()
}
func (p *p1SessionTrace) collect(ctx context.Context) {
	defer close(p.done)
	defer p.collector.Close()
	defer p.socket.Close()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, eof, err := p1TraceReady(p.socket)
		if err != nil {
			p.fail(errP1SessionTrace)
			return
		}
		if ready {
			if err = p.collectOne(ctx); err != nil {
				p.fail(err)
				return
			}
			continue
		}
		if eof {
			p.mu.Lock()
			p.traceEOF = true
			p.mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			p.fail(errP1SessionTrace)
			return
		case <-ticker.C:
		}
	}
}
func (p *p1SessionTrace) collectOne(ctx context.Context) error {
	pending, err := p.collector.Next(ctx)
	if err != nil {
		return err
	}
	record := p1TraceRecord{Event: pending.Event(), Originals: traceOriginals(pending), Sequence: pending.Sequence(), PeerPID: pending.PeerPID(), PeerUID: pending.PeerUID()}
	wrapper, err := p1TraceUnwrap(record.Event.Data)
	p.mu.Lock()
	index := len(p.records)
	p.records = append(p.records, copyP1TraceRecord(record))
	p.mu.Unlock()
	defer func() {
		record.Originals = traceOriginals(pending)
		if record.OperationOriginal != nil {
			record.Originals = append(record.Originals, *record.OperationOriginal)
		}
		p.mu.Lock()
		p.records[index] = copyP1TraceRecord(record)
		p.mu.Unlock()
	}()
	if err != nil {
		return err
	}
	if record.Event.Component == "journal" && record.Event.Point == "published" {
		var detail struct {
			SHA       string `json:"record_sha256"`
			Phase     string `json:"phase"`
			Body      string `json:"body_sha256"`
			Operation string `json:"operation"`
		}
		if wrapper.Injectable || !p1HistoryJSON(wrapper.Detail, &detail, "record_sha256", "phase", "body_sha256", "operation") || !operationDigest(detail.SHA) {
			return errP1SessionTrace
		}
		store, err := config.OpenStore(p.paths, false)
		if err != nil {
			return errP1SessionTrace
		}
		raw, sha, err := store.ReadOperationFile(config.ID(record.Event.OperationID))
		closeErr := store.Close()
		if err != nil || closeErr != nil || sha != detail.SHA || p1SHA(raw) != sha {
			return errP1SessionTrace
		}
		ref, err := p1WriteOriginal(filepath.Join(p.captures, "trace", fmt.Sprintf("%016d.operation.json", record.Sequence)), raw)
		if err != nil {
			return err
		}
		record.OperationOriginal = &ref
		record.Originals = append(record.Originals, ref)
	}
	decision := p1TraceContinue
	if p.decide != nil {
		decision = p.decide(copyP1TraceRecord(record))
	}
	if decision != p1TraceContinue {
		return errP1SessionTrace
	}
	if err = pending.Decide(ctx, p1trace.Continue); err != nil {
		return err
	}
	record.Decision = p1TraceContinue
	return nil
}

// Verify repeats full original-pidfd identity verification; Identity and
// BirthOriginal remain historical observations, not trigger-time live proof.
func (p *p1SessionTrace) Verify(ctx context.Context) error {
	if p == nil || ctx == nil {
		return errP1SessionTrace
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	if p.closed {
		return errP1SessionTrace
	}
	return p.handle.Verify(ctx)
}
func (p *p1SessionTrace) WaitExited(ctx context.Context) error {
	if p == nil || ctx == nil {
		return errP1SessionTrace
	}
	return p.waitExited(ctx)
}

// recordedExit preserves both independent requirements: pidfd exit and a
// complete authenticated trace EOF. A receipt never fabricates child Wait.
func (p *p1SessionTrace) recordedExit() (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.exit.Path == "" {
		return false, nil
	}
	if !p.traceEOF || p.traceErr != nil {
		return true, errP1SessionTrace
	}
	return true, nil
}
func (p *p1SessionTrace) waitExited(ctx context.Context) error {
	if found, result := p.recordedExit(); found {
		return result
	}
	p.mu.RLock()
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		if found, result := p.recordedExit(); found {
			return result
		}
		return errP1SessionTrace
	}
	if err := p.handle.WaitExited(ctx); err != nil {
		// A concurrent Close publishes the actual receipt BEFORE closing the handle.
		if found, result := p.recordedExit(); found {
			return result
		}
		return err
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.exitMu.Lock()
	defer p.exitMu.Unlock()
	if found, result := p.recordedExit(); found {
		return result
	}
	p.mu.RLock()
	eof, traceErr := p.traceEOF, p.traceErr
	p.mu.RUnlock()
	ref, err := p1WriteOriginal(filepath.Join(p.captures, "exit.json"), p1SessionJSON(map[string]any{"version": 1, "kind": "original-pidfd-session-manager-exit-observation", "identity": p.identity, "birth_original": p.birth, "pidfd_exit_observed": true, "trace_eof_observed": eof, "trace_completed_without_error": traceErr == nil, "wait_status_observed": false, "exit_code_observed": false, "scope": "not a child Wait; neither SSH client nor shell status is manager exit status"}))
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.exit = ref
	p.mu.Unlock()
	_, result := p.recordedExit()
	return result
}
func (p *p1SessionTrace) Signal(ctx context.Context, signal syscall.Signal) error {
	if p == nil || ctx == nil || (signal != syscall.SIGTERM && signal != syscall.SIGKILL) {
		return errP1SessionTrace
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	return p.signal(ctx, signal)
}
func (p *p1SessionTrace) signal(ctx context.Context, signal syscall.Signal) error {
	if p.closed {
		return errP1SessionTrace
	}
	if err := p.handle.SignalVerified(ctx, signal); err != nil {
		return err
	}
	p.mu.RLock()
	number := len(p.signals) + 1
	p.mu.RUnlock()
	ref, err := p1WriteOriginal(filepath.Join(p.captures, fmt.Sprintf("signal-%04d.json", number)), p1SessionJSON(map[string]any{"version": 1, "kind": "actual-original-pidfd-session-manager-signal", "signal": int(signal), "identity": p.identity, "birth_original": p.birth, "source": "explicit session-trace supervisor action", "wait_status_observed": false}))
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.signals = append(p.signals, ref)
	p.mu.Unlock()
	return nil
}
func (p *p1SessionTrace) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		return errP1SessionTrace
	}
	p.lifecycle.Lock()
	defer p.lifecycle.Unlock()
	if p.closed {
		return nil
	}
	hasExit := func() bool { p.mu.RLock(); defer p.mu.RUnlock(); return p.exit.Path != "" }
	var result error
	select {
	case <-p.done:
		wait, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		result = p.waitExited(wait)
		cancel()
	default:
	}
	if !hasExit() {
		grace, cancel := context.WithTimeout(ctx, 3*time.Second)
		signalErr := p.signal(grace, syscall.SIGTERM)
		cancel()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		result = p.waitExited(wait)
		cancel()
		if !hasExit() {
			kill, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			killErr := p.signal(kill, syscall.SIGKILL)
			cancel()
			wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			result = p.waitExited(wait)
			cancel()
			if !hasExit() {
				return errors.Join(result, signalErr, killErr)
			}
		}
	}
	p.cancel()
	handleErr := p.handle.Close()
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return errors.Join(result, handleErr)
}

// Explicit disposable intermediate parent, not SSH. It owns the probe child
// Wait; the outer broker sees a NON-child manager and never borrows that status.
func TestP1SessionTraceLauncher(t *testing.T) {
	if os.Getenv("HOOPA_P1_SESSION_TRACE_LAUNCHER") != "1" {
		return
	}
	var args []string
	if json.Unmarshal([]byte(os.Getenv("HOOPA_P1_SESSION_TRACE_ARGS")), &args) != nil {
		t.Fatal("invalid private launcher setup")
	}
	probe, work := os.Getenv("HOOPA_P1_SESSION_TRACE_PROBE"), os.Getenv("HOOPA_P1_SESSION_TRACE_WORK")
	cmd := exec.Command(probe, args...)
	cmd.Dir = work
	cmd.Env = []string{"HOME=" + work, "TMPDIR=" + work, "LANG=C", "HOOPA_RUN_NONCE=" + os.Getenv("HOOPA_P1_SESSION_TRACE_NONCE")}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal("manager fixture start failed")
	}
	// This actual Wait belongs only to this independent launcher fixture. Its
	// status is not exported as any broker/remote-manager exit observation.
	_ = cmd.Wait()
}
func p1SessionTestFixture(t *testing.T, decide func(p1TraceRecord) p1TraceDecision) (*p1SessionBroker, *p1SessionTrace, config.Paths, func()) {
	t.Helper()
	probe := os.Getenv("HOOPA_P1_SESSION_TRACE_PROBE")
	if probe == "" {
		t.Fatal("explicit actual tagged probe executable required")
	}
	work, err := os.MkdirTemp("/tmp", "hps-")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(work) })
	captures := t.TempDir()
	must(t, os.Chmod(captures, 0700))
	if root := os.Getenv("HOOPA_P1_SESSION_TRACE_ARTIFACTS"); root != "" {
		captures = filepath.Join(root, strings.ReplaceAll(t.Name(), "/", "-"))
		must(t, os.Mkdir(captures, 0700))
	}
	paths, err := config.ResolvePaths(work, nil)
	must(t, err)
	nonce := make([]byte, 32)
	_, err = rand.Read(nonce)
	must(t, err)
	token := hex.EncodeToString(nonce)
	broker, err := startP1SessionBroker(paths, probe, work, p1SHA([]byte(token)), captures, decide)
	must(t, err)
	t.Cleanup(func() { _ = broker.Close() })
	args := []string{"session-trace", broker.Path(), broker.Stream(), strconv.Itoa(os.Getpid()), "service"}
	exe, err := os.Executable()
	must(t, err)
	command := exec.Command(exe, "-test.run=^TestP1SessionTraceLauncher$", "-test.timeout=20s")
	command.Dir = work
	command.Env = []string{"HOOPA_P1_SESSION_TRACE_LAUNCHER=1", "HOOPA_P1_SESSION_TRACE_PROBE=" + probe, "HOOPA_P1_SESSION_TRACE_WORK=" + work, "HOOPA_P1_SESSION_TRACE_NONCE=" + token, "HOOPA_P1_SESSION_TRACE_ARGS=" + string(p1SessionJSON(args))}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	must(t, command.Start())
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var trace *p1SessionTrace
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if trace != nil {
			_ = trace.Close(ctx)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	}
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trace, err = broker.Accept(ctx)
	must(t, err)
	if trace.PID() == command.Process.Pid || trace.parentPID != command.Process.Pid || trace.PID() == os.Getpid() {
		t.Fatal("fixture did not produce a verified non-child manager")
	}
	return broker, trace, paths, func() {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("intermediate fixture parent failed")
			}
			done <- nil
		case <-time.After(3 * time.Second):
			t.Fatal("intermediate fixture parent did not reap its own child")
		}
	}
}
func p1SessionTestReady(t *testing.T, paths config.Paths) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		conn, err := service.DialEndpoint(ctx, paths)
		if err == nil {
			must(t, conn.Close())
			return
		}
		if !errors.Is(err, service.ErrServiceUnavailable) {
			t.Fatal("management endpoint failed", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("management endpoint unavailable")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func TestP1SessionTraceActualNonChild(t *testing.T) {
	_, trace, paths, launcherWait := p1SessionTestFixture(t, nil)
	p1SessionTestReady(t, paths)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := config.OpenStore(paths, false)
	must(t, err)
	active, err := store.ReadActive()
	must(t, err)
	must(t, store.Close())
	response, err := service.Call(ctx, paths, p1SessionJSON(map[string]any{"api_version": "v1", "request_id": "session-component-stop", "operation": "service.stop", "params": map[string]any{"expected_revision": active.Revision}}))
	must(t, err)
	if service.ExitCode(response) != 0 {
		t.Fatal("actual management-only service stop failed")
	}
	must(t, trace.WaitExited(ctx))
	launcherWait()
	must(t, trace.TraceError())
	records := trace.Records()
	must(t, p1ValidateTraceRecords(records))
	journals := 0
	for _, record := range records {
		if record.Event.Component == "journal" && record.Event.Point == "published" {
			journals++
			if record.OperationOriginal == nil {
				t.Fatal("actual journal original missing")
			}
		}
	}
	if journals == 0 || trace.BirthOriginal().Path == "" || trace.ExitOriginal().Path == "" {
		t.Fatal("actual non-child evidence incomplete")
	}
	raw, err := p1HistoryOriginal(trace.ExitOriginal(), 4096)
	must(t, err)
	var receipt map[string]json.RawMessage
	must(t, json.Unmarshal(raw, &receipt))
	if string(receipt["wait_status_observed"]) != "false" || string(receipt["pidfd_exit_observed"]) != "true" || string(receipt["trace_eof_observed"]) != "true" {
		t.Fatal("pidfd/EOF evidence fabricated child Wait")
	}
	must(t, trace.Close(ctx))
	t.Logf("actual non-child management service: records=%d journal-originals=%d; original-pidfd exit+authenticated EOF, no manager Wait status", len(records), journals)
}
func TestP1SessionTraceActualSignal(t *testing.T) {
	_, trace, paths, launcherWait := p1SessionTestFixture(t, nil)
	p1SessionTestReady(t, paths)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	waiting := make(chan error, 1)
	go func() { waiting <- trace.WaitExited(ctx) }()
	must(t, trace.Verify(ctx))
	if trace.Signal(ctx, syscall.SIGUSR1) == nil {
		t.Fatal("unsupported signal accepted")
	}
	must(t, trace.Signal(ctx, syscall.SIGTERM))
	must(t, <-waiting)
	if trace.Verify(ctx) == nil {
		t.Fatal("exited pidfd fabricated fresh live proof")
	}
	must(t, trace.WaitExited(ctx))
	launcherWait()
	if len(trace.SignalOriginals()) != 1 {
		t.Fatal("actual original-pidfd signal evidence missing")
	}
	must(t, p1ValidateTraceRecords(trace.Records()))
	must(t, trace.Close(ctx))
}
func TestP1SessionBrokerRejectsHello(t *testing.T) {
	probe := os.Getenv("HOOPA_P1_SESSION_TRACE_PROBE")
	if probe == "" {
		t.Fatal("explicit actual probe required")
	}
	for _, mode := range []string{"claim-pid", "unknown", "stream", "rights", "truncated", "wrong-process"} {
		t.Run(mode, func(t *testing.T) {
			work, err := os.MkdirTemp("/tmp", "hps-")
			must(t, err)
			defer os.RemoveAll(work)
			capture := t.TempDir()
			must(t, os.Chmod(capture, 0700))
			paths, err := config.ResolvePaths(work, nil)
			must(t, err)
			broker, err := startP1SessionBroker(paths, probe, work, strings.Repeat("a", 64), capture, nil)
			must(t, err)
			defer broker.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: broker.Path(), Net: "unixpacket"})
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				hello := map[string]any{"version": 1, "stream": broker.Stream(), "manager_pid": os.Getpid(), "manager_parent_pid": os.Getppid()}
				switch mode {
				case "claim-pid":
					hello["manager_pid"] = os.Getpid() + 1
				case "unknown":
					hello["extra"] = true
				case "stream":
					hello["stream"] = strings.Repeat("f", 32)
				}
				raw := p1SessionJSON(hello)
				var rights []byte
				if mode == "rights" {
					rights = syscall.UnixRights(0)
				}
				if mode == "truncated" {
					raw = make([]byte, p1session.MaxPacket+1)
				}
				conn.WriteMsgUnix(raw, rights, nil)
				conn.Read(make([]byte, 1024))
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			trace, err := broker.Accept(ctx)
			<-finished
			if trace != nil || err == nil {
				t.Fatal("invalid actual peer/hello granted trace FD")
			}
		})
	}
}
func TestP1SessionStatActual(t *testing.T) {
	raw, err := os.ReadFile("/proc/self/stat")
	must(t, err)
	parent, group, session, ticks, err := p1SessionStat(raw, os.Getpid())
	must(t, err)
	if parent != os.Getppid() || group != syscall.Getpgrp() || session <= 0 || ticks == 0 {
		t.Fatal("actual proc stat identity mismatch")
	}
	for _, bad := range [][]byte{nil, []byte("1 (x) R 0 0 0"), raw} {
		pid := os.Getpid()
		if bytes.Equal(bad, raw) {
			pid++
		}
		if _, _, _, _, err := p1SessionStat(bad, pid); err == nil {
			t.Fatal("invalid stat accepted")
		}
	}
}

// Synthetic cached-result policy only; these markers are not actual process or
// receipt evidence. Missing EOF must remain an error on every repeat call.
func TestP1SessionTraceRecordedExitResult(t *testing.T) {
	for _, test := range []struct {
		name                         string
		eof, traceFailure, wantError bool
	}{{"complete", true, false, false}, {"missing-EOF", false, false, true}, {"trace-failed", true, true, true}} {
		t.Run(test.name, func(t *testing.T) {
			p := &p1SessionTrace{exit: originalRef{Path: "synthetic-cached-result-only"}, traceEOF: test.eof, closed: true}
			if test.traceFailure {
				p.traceErr = errP1SessionTrace
			}
			for n := 0; n < 2; n++ {
				err := p.WaitExited(context.Background())
				if (err != nil) != test.wantError {
					t.Fatal("cached exit result lost EOF/trace requirement")
				}
			}
		})
	}
}
