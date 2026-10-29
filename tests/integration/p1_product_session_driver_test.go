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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/platform"
	"hoopa/internal/service"
)

var errP1SessionDriver = errors.New("P1_ISOLATED_SESSION_DRIVER_UNVERIFIED")
var p1SessionSSHArgs = []string{"/bin/ssh", "-F", "/dev/null", "-tt", "-p", "2222", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=/work/ssh/known_hosts", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "ConnectTimeout=3", "-o", "PasswordAuthentication=no", "-o", "PreferredAuthentications=publickey", "-i", "/work/ssh/client", "root@127.0.0.1"}

type p1SessionLog struct {
	mu              sync.Mutex
	file            *os.File
	path            string
	retained, total int
	overflow        bool
	closed          bool
}

func (p *p1SessionLog) Write(raw []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total += len(raw)
	n := len(raw)
	if n > 65536-p.retained {
		n = 65536 - p.retained
		p.overflow = true
	}
	if n > 0 {
		written, err := p.file.Write(raw[:n])
		p.retained += written
		if err != nil || written != n {
			return 0, errP1SessionDriver
		}
	}
	return len(raw), nil
}
func (p *p1SessionLog) finish() (originalRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		if err := errors.Join(p.file.Sync(), p.file.Close()); err != nil {
			return originalRef{}, err
		}
		p.closed = true
	}
	raw, err := os.ReadFile(p.path)
	if err != nil || len(raw) != p.retained {
		return originalRef{}, errP1SessionDriver
	}
	return originalRef{Path: p.path, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, nil
}

type p1SessionChild struct {
	cmd             *exec.Cmd
	handle          *platform.ProcessHandle
	emergencyFD     int
	done            chan struct{}
	waitErr         error
	stdin           io.WriteCloser
	stdout, stderr  *p1SessionLog
	identity        platform.ProcessIdentity
	role            string
	receipts        []originalRef
	waited          bool
	started, closed bool
	waitResult      error
}
type p1SessionProc struct {
	PID          int         `json:"pid"`
	Parent       int         `json:"ppid"`
	Group        int         `json:"pgrp"`
	Session      int         `json:"sid"`
	Start        uint64      `json:"start_ticks,string"`
	TTY          int64       `json:"tty_nr"`
	Foreground   int         `json:"tpgid"`
	State        string      `json:"state"`
	Exe          string      `json:"exe"`
	ExeSHA       string      `json:"exe_sha256"`
	Arguments    []string    `json:"arguments"`
	StatOriginal originalRef `json:"stat_original"`
}
type p1SessionPreexec struct {
	Version            int         `json:"version"`
	Kind               string      `json:"kind"`
	PID                int         `json:"pid"`
	Start              uint64      `json:"start_ticks,string"`
	Parent             int         `json:"ppid"`
	Group              int         `json:"pgrp"`
	Session            int         `json:"sid"`
	TTY                int64       `json:"tty_nr"`
	Foreground         int         `json:"tpgid"`
	TTYName            string      `json:"tty_name"`
	Connection         string      `json:"ssh_connection"`
	BootID             string      `json:"boot_id"`
	StatOriginal       originalRef `json:"stat_original"`
	ParentStatOriginal originalRef `json:"parent_stat_original"`
	ParentExe          string      `json:"parent_exe"`
	ParentArguments    []string    `json:"parent_arguments"`
	ShellOriginal      string      `json:"shell_original"`
}
type p1SessionDriver struct {
	mu                                                 sync.Mutex
	Trace                                              *p1SessionTrace
	h                                                  *p1ProductHarness
	kind, dir, work, helper, fifo, shellProof, preexec string
	binding                                            p1GuestBinding
	broker                                             *p1SessionBroker
	server, client                                     *p1SessionChild
	fifoFile                                           *os.File
	refs                                               []originalRef
	began, ended                                       originalRef
	pre                                                p1SessionPreexec
	ancestors                                          []p1SessionProc
	closeResult                                        error
	closed, triggered                                  bool
}

func (d *p1SessionDriver) Originals() []originalRef {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]originalRef(nil), d.refs...)
	for _, child := range []*p1SessionChild{d.server, d.client} {
		if child != nil {
			out = append(out, child.receipts...)
		}
	}
	if d.Trace != nil {
		out = append(out, d.Trace.BirthOriginal())
		if ref := d.Trace.ExitOriginal(); ref.Path != "" {
			out = append(out, ref)
		}
		out = append(out, d.Trace.SignalOriginals()...)
	}
	return out
}
func (d *p1SessionDriver) original(label string, value any) (originalRef, error) {
	ref, err := p1WriteOriginal(filepath.Join(d.dir, label+".json"), p1SessionJSON(value))
	if err == nil {
		d.refs = append(d.refs, ref)
	}
	return ref, err
}
func p1SessionQuote(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'" }
func p1SessionLoginLine(helper, fifo, shell string) string {
	return `umask 077; shopt -s huponexit; case $- in *i*) p1_i=true;; *) p1_i=false;; esac; if shopt -q login_shell; then p1_l=true; else p1_l=false; fi; if shopt -q huponexit; then p1_h=true; else p1_h=false; fi; printf '{"pid":%s,"interactive":%s,"login":%s,"huponexit":%s}\n' "$$" "$p1_i" "$p1_l" "$p1_h" > ` + p1SessionQuote(shell) + `; PYTHONHOME=/usr ` + p1SessionQuote("/bin/python3") + ` -S -B ` + p1SessionQuote(helper) + ` ` + p1SessionQuote(fifo) + ` &` + "\n"
}
func p1SessionNonce() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	return token, p1SHA([]byte(token)), nil
}
func (d *p1SessionDriver) startChild(ctx context.Context, role string, args []string) (*p1SessionChild, error) {
	nonce, nonceSHA, err := p1SessionNonce()
	if err != nil {
		return nil, errP1SessionDriver
	}
	_, sha, err := p1CLIExecutable(args[0])
	if err != nil {
		return nil, err
	}
	child := &p1SessionChild{role: role, emergencyFD: -1, done: make(chan struct{})}
	command := exec.Command(args[0], args[1:]...)
	child.cmd = command
	command.Dir = d.work
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Env = []string{"HOME=/root", "TMPDIR=" + d.work, "PATH=/bin:/sbin", "LANG=C", "PYTHONHOME=/usr", "PYTHONDONTWRITEBYTECODE=1", "HOOPA_RUN_NONCE=" + nonce}
	for label, target := range map[string]**p1SessionLog{"stdout": &child.stdout, "stderr": &child.stderr} {
		path := filepath.Join(d.dir, role+"."+label)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return child, err
		}
		*target = &p1SessionLog{file: file, path: path}
	}
	command.Stdout = child.stdout
	command.Stderr = child.stderr
	child.stdin, err = command.StdinPipe()
	if err != nil {
		return child, err
	}
	if ctx.Err() != nil {
		return child, ctx.Err()
	}
	if err = command.Start(); err != nil {
		return child, err
	}
	child.started = true
	fd, _, errno := syscall.Syscall(434, uintptr(command.Process.Pid), 0, 0)
	if errno == 0 {
		child.emergencyFD = int(fd)
		syscall.CloseOnExec(child.emergencyFD)
	}
	go func() { child.waitErr = command.Wait(); close(child.done) }()
	if errno != 0 {
		return child, errP1SessionDriver
	}
	if role == "sshd" {
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		ready := false
		for !ready {
			raw, err := os.ReadFile("/run/sshd.pid")
			if err == nil {
				pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
				if e != nil || pid != command.Process.Pid {
					return child, errP1SessionDriver
				}
				ready = true
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return child, errP1SessionDriver
			}
			select {
			case <-child.done:
				return child, errP1SessionDriver
			case <-ctx.Done():
				return child, ctx.Err()
			case <-deadline.C:
				return child, errP1SessionDriver
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", command.Process.Pid))
	if err != nil || (cwd != d.work && !(role == "sshd" && cwd == "/")) {
		return child, errP1SessionDriver
	}
	child.handle, err = platform.ObserveProcess(ctx, command.Process.Pid, platform.ProcessSpec{BinaryPath: args[0], BinarySHA256: sha, Workdir: cwd, NonceSHA256: nonceSHA})
	if err != nil {
		return child, err
	}
	child.identity = child.handle.Identity()
	row, err := d.proc(command.Process.Pid, role+"-birth")
	if err != nil || row.Parent != os.Getpid() || row.Session != row.PID || row.Group != row.PID || row.Exe != args[0] || row.ExeSHA != sha || row.Start != child.identity.StartTicks {
		return child, errP1SessionDriver
	}
	ref, err := d.original(role+"-original-direct-child", map[string]any{"kind": "actual-setsid-direct-session-child", "role": role, "identity": child.identity, "proc": row, "arguments": args, "created_by_test_pid": os.Getpid()})
	if err != nil {
		return child, err
	}
	child.receipts = append(child.receipts, ref)
	return child, nil
}
func (d *p1SessionDriver) proc(pid int, label string) (p1SessionProc, error) {
	fail := func() (p1SessionProc, error) { return p1SessionProc{}, errP1SessionDriver }
	path := fmt.Sprintf("/proc/%d", pid)
	file, err := os.Open(path + "/stat")
	if err != nil {
		return fail()
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 4096 {
		return fail()
	}
	parent, group, session, start, err := p1SessionStat(raw, pid)
	if err != nil {
		return fail()
	}
	fields := strings.Fields(string(raw[bytes.LastIndexByte(raw, ')')+1:]))
	tty, e1 := strconv.ParseInt(fields[4], 10, 64)
	foreground, e2 := strconv.Atoi(fields[5])
	if e1 != nil || e2 != nil {
		return fail()
	}
	exe, err := os.Readlink(path + "/exe")
	if err != nil {
		return fail()
	}
	_, sha, err := p1CLIExecutable(exe)
	if err != nil {
		return fail()
	}
	arguments, err := os.ReadFile(path + "/cmdline")
	if err != nil || len(arguments) > 32768 || len(arguments) == 0 || arguments[len(arguments)-1] != 0 {
		return fail()
	}
	again, err := os.ReadFile(path + "/stat")
	if err != nil {
		return fail()
	}
	_, _, _, againStart, err := p1SessionStat(again, pid)
	if err != nil || againStart != start {
		return fail()
	}
	ref, err := p1WriteOriginal(filepath.Join(d.dir, label+".stat.raw"), raw)
	if err != nil {
		return fail()
	}
	d.refs = append(d.refs, ref)
	return p1SessionProc{PID: pid, Parent: parent, Group: group, Session: session, Start: start, TTY: tty, Foreground: foreground, State: fields[0], Exe: exe, ExeSHA: sha, Arguments: strings.Split(string(arguments[:len(arguments)-1]), "\x00"), StatOriginal: ref}, nil
}
func (d *p1SessionDriver) ancestry(ctx context.Context, label string) ([]p1SessionProc, error) {
	if d.Trace.Verify(ctx) != nil || d.server.handle.Verify(ctx) != nil {
		return nil, errP1SessionDriver
	}
	pid := d.Trace.PID()
	rows := []p1SessionProc{}
	seen := map[int]bool{}
	for index := 0; index < 16; index++ {
		if seen[pid] || pid <= 1 {
			return nil, errP1SessionDriver
		}
		seen[pid] = true
		row, err := d.proc(pid, fmt.Sprintf("%s-%02d", label, index))
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if index == 0 && (row.Start != d.Trace.Identity().StartTicks || row.Session != d.pre.Session || row.Group != d.pre.Group || row.TTY != d.pre.TTY) {
			return nil, errP1SessionDriver
		}
		if pid == d.server.cmd.Process.Pid {
			if row.Start != d.server.identity.StartTicks || row.Parent != os.Getpid() {
				return nil, errP1SessionDriver
			}
			if d.Trace.Verify(ctx) != nil || d.server.handle.Verify(ctx) != nil {
				return nil, errP1SessionDriver
			}
			return rows, nil
		}
		pid = row.Parent
	}
	return nil, errP1SessionDriver
}
func startP1SessionDriver(ctx context.Context, h *p1ProductHarness, kind string) (driver *p1SessionDriver, resultErr error) {
	binding, err := p1SessionGuest()
	if err != nil {
		return nil, err
	}
	if ctx == nil || ctx.Err() != nil || h == nil || h.f == nil || (kind != "ssh-disconnect" && kind != "login-shell-logout") {
		return nil, errP1SessionDriver
	}
	d := &p1SessionDriver{h: h, kind: kind, work: h.f.cli.home, binding: binding}
	d.dir = h.directory("session-driver")
	d.helper = filepath.Join(d.dir, "remote.py")
	d.fifo = filepath.Join(d.work, "session-control-"+filepath.Base(d.dir))
	d.shellProof = filepath.Join(d.dir, "shell-options.json")
	d.preexec = filepath.Join(d.dir, "remote-preexec.json")
	defer func() {
		if resultErr != nil {
			if d.Trace != nil {
				driver = d
			} else {
				cleanup, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				cleanupErr := d.Close(cleanup)
				resultErr = errors.Join(resultErr, cleanupErr)
				if cleanupErr != nil {
					driver = d
				}
			}
		}
	}()
	for _, name := range []string{"sshd_config", "host", "client", "known_hosts", "authorized_keys"} {
		info, err := os.Lstat(filepath.Join("/work/ssh", name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
			return nil, errP1SessionDriver
		}
	}
	if _, err = os.Lstat("/run/sshd.pid"); !errors.Is(err, os.ErrNotExist) {
		return nil, errP1SessionDriver
	}
	d.server, err = d.startChild(ctx, "sshd", []string{"/sbin/sshd", "-D", "-e", "-f", "/work/ssh/sshd_config"})
	if err != nil {
		return nil, err
	}
	rawNonce, nonceSHA, err := p1SessionNonce()
	if err != nil {
		return nil, err
	}
	captures := filepath.Join(d.dir, "manager-trace")
	if err = os.Mkdir(captures, 0700); err != nil {
		return nil, err
	}
	d.broker, err = startP1SessionBroker(h.f.cli.Paths(), h.f.probe, d.work, nonceSHA, captures, h.decide)
	if err != nil {
		return nil, err
	}
	helperRef, err := p1WriteOriginal(d.helper, []byte(p1SessionRemotePython))
	if err != nil {
		return nil, err
	}
	d.refs = append(d.refs, helperRef)
	if err = syscall.Mkfifo(d.fifo, 0600); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(d.fifo, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	d.fifoFile = os.NewFile(uintptr(fd), "private-session-control")
	environment := h.f.cli.environment()
	environment = append(environment, "HOOPA_RUN_NONCE="+rawNonce)
	args := []string{h.f.probe, "session-trace", d.broker.Path(), d.broker.Stream(), strconv.Itoa(os.Getpid()), "service"}
	payload := p1SessionJSON(map[string]any{"version": 1, "kind": kind, "probe": h.f.probe, "arguments": args, "workdir": d.work, "environment": environment, "preexec": d.preexec, "shell_original": d.shellProof})
	if len(payload) > 4096 {
		return nil, errP1SessionDriver
	}
	expectedCount := len(payload)
	count, err := d.fifoFile.Write(payload)
	for i := range payload {
		payload[i] = 0
	}
	if err != nil || count != expectedCount {
		return nil, errP1SessionDriver
	}
	command := "PYTHONHOME=/usr exec " + p1SessionQuote("/bin/python3") + " -S -B " + p1SessionQuote(d.helper) + " " + p1SessionQuote(d.fifo)
	if kind == "login-shell-logout" {
		command = "/bin/bash -li"
	}
	ssh := append(append([]string(nil), p1SessionSSHArgs...), command)
	d.client, err = d.startChild(ctx, "ssh-client", ssh)
	if err != nil {
		return nil, err
	}
	if kind == "login-shell-logout" {
		line := p1SessionLoginLine(d.helper, d.fifo, d.shellProof)
		if _, err = io.WriteString(d.client.stdin, line); err != nil {
			return nil, err
		}
	}
	d.Trace, err = d.broker.Accept(ctx)
	if err != nil {
		return nil, err
	}
	if err = d.fifoFile.Close(); err != nil {
		return d, err
	}
	d.fifoFile = nil
	if err = os.Remove(d.fifo); err != nil {
		return d, err
	}
	if err = d.broker.Close(); err != nil {
		return d, err
	}
	preRaw, err := os.ReadFile(d.preexec)
	if err != nil || len(preRaw) > 65536 {
		return d, errP1SessionDriver
	}
	preRef := originalRef{Path: d.preexec, Bytes: int64(len(preRaw)), SHA256: p1SHA(preRaw)}
	if _, err = p1HistoryOriginal(preRef, 65536); err != nil {
		return d, err
	}
	if json.Unmarshal(preRaw, &d.pre) != nil {
		return d, errP1SessionDriver
	}
	d.refs = append(d.refs, preRef, d.Trace.BirthOriginal())
	for _, ref := range []originalRef{d.pre.StatOriginal, d.pre.ParentStatOriginal} {
		if _, err = p1HistoryOriginal(ref, 4096); err != nil {
			return d, err
		}
		d.refs = append(d.refs, ref)
	}
	if err = d.validatePreexec(); err != nil {
		return d, err
	}
	d.ancestors, err = d.ancestry(ctx, "ancestry-start")
	if err != nil {
		return d, err
	}
	if kind == "login-shell-logout" {
		if err = d.validateShell(); err != nil {
			return d, err
		}
	}
	if err = d.ready(ctx); err != nil {
		return d, err
	}
	d.began, err = d.original("session-begin", map[string]any{"kind": kind, "phase": "actual-remote-management-ready-before-core-start", "guest_binding": binding, "server_identity": d.server.identity, "client_identity": d.client.identity, "remote_identity": d.Trace.Identity(), "remote_birth_original": d.Trace.BirthOriginal(), "preexec_original": preRef, "ancestry": d.ancestors, "remote_manager_is_not_ssh_client": d.Trace.PID() != d.client.cmd.Process.Pid, "scope": "actual dedicated guest SSH/PTY topology; core state belongs to subsequent product observations"})
	if err != nil {
		return d, err
	}
	return d, nil
}
func (d *p1SessionDriver) validatePreexec() error {
	p := d.pre
	identity := d.Trace.Identity()
	parts := strings.Fields(p.Connection)
	if p.Version != 1 || p.Kind != "actual-remote-python-before-probe-exec" || p.PID != identity.PID || p.Start != identity.StartTicks || p.BootID != identity.BootID || p.Parent != d.Trace.parentPID || p.Session != d.Trace.sessionID || p.Group != d.Trace.groupID || p.TTY == 0 || !strings.HasPrefix(p.TTYName, "/dev/pts/") || len(parts) != 4 || parts[0] != "127.0.0.1" || parts[2] != "127.0.0.1" || parts[3] != "2222" || p.PID == d.client.cmd.Process.Pid || p.PID == d.server.cmd.Process.Pid {
		return errP1SessionDriver
	}
	raw, err := p1HistoryOriginal(p.StatOriginal, 4096)
	if err != nil {
		return err
	}
	parent, group, session, start, err := p1SessionStat(raw, p.PID)
	if err != nil || parent != p.Parent || group != p.Group || session != p.Session || start != p.Start {
		return errP1SessionDriver
	}
	return nil
}
func (d *p1SessionDriver) validateShell() error {
	var row struct {
		PID         int  `json:"pid"`
		Interactive bool `json:"interactive"`
		Login       bool `json:"login"`
		Hup         bool `json:"huponexit"`
	}
	raw, err := os.ReadFile(d.shellProof)
	if err != nil || !p1HistoryJSON(raw, &row, "pid", "interactive", "login", "huponexit") || row.PID != d.pre.Parent || !row.Interactive || !row.Login || !row.Hup || d.pre.ParentExe != "/bin/bash" || len(d.pre.ParentArguments) != 2 || d.pre.ParentArguments[0] != "/bin/bash" || d.pre.ParentArguments[1] != "-li" {
		return errP1SessionDriver
	}
	ref := originalRef{Path: d.shellProof, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}
	if _, err = p1HistoryOriginal(ref, 4096); err != nil {
		return err
	}
	d.refs = append(d.refs, ref)
	parent, err := d.proc(row.PID, "login-shell-start")
	if err != nil || parent.Session != d.pre.Session || parent.TTY != d.pre.TTY || parent.Exe != "/bin/bash" {
		return errP1SessionDriver
	}
	return nil
}
func (d *p1SessionDriver) ready(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		conn, err := service.DialEndpoint(bounded, d.h.f.cli.Paths())
		if err == nil {
			raw, e := conn.SyscallConn()
			var peer *syscall.Ucred
			var pe error
			if e == nil {
				e = raw.Control(func(fd uintptr) { peer, pe = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
			}
			closeErr := conn.Close()
			if e != nil || pe != nil || closeErr != nil || peer == nil || int(peer.Pid) != d.Trace.PID() {
				return errP1SessionDriver
			}
			return d.Trace.Verify(bounded)
		}
		if !errors.Is(err, service.ErrServiceUnavailable) {
			return err
		}
		select {
		case <-bounded.Done():
			return bounded.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (d *p1SessionDriver) waitChild(ctx context.Context, child *p1SessionChild) error {
	if child == nil {
		return nil
	}
	if child.waited {
		return child.waitResult
	}
	if !child.started {
		return errP1SessionDriver
	}
	select {
	case <-child.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	state := child.cmd.ProcessState
	if state == nil {
		return errP1SessionDriver
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || (!status.Exited() && !status.Signaled()) {
		return errP1SessionDriver
	}
	stdout, err := child.stdout.finish()
	if err != nil {
		return err
	}
	stderr, err := child.stderr.finish()
	if err != nil {
		return err
	}
	child.receipts = append(child.receipts, stdout, stderr)
	child.stdout.mu.Lock()
	outOverflow, outTotal := child.stdout.overflow, child.stdout.total
	child.stdout.mu.Unlock()
	child.stderr.mu.Lock()
	errOverflow, errTotal := child.stderr.overflow, child.stderr.total
	child.stderr.mu.Unlock()
	ref, err := d.original(child.role+"-wait", map[string]any{"kind": "actual-original-direct-child-wait", "role": child.role, "pid": child.cmd.Process.Pid, "identity": child.identity, "wait_status": int(status), "wait_exited": status.Exited(), "wait_signaled": status.Signaled(), "exit_code": state.ExitCode(), "stdout_original": stdout, "stderr_original": stderr, "stdout_total": outTotal, "stderr_total": errTotal, "stdout_truncated": outOverflow, "stderr_truncated": errOverflow, "scope": "this status belongs only to this direct SSH client/server, never remote manager"})
	if err != nil {
		return err
	}
	child.receipts = append(child.receipts, ref)
	child.waited = true
	if outOverflow || errOverflow {
		child.waitResult = errP1SessionDriver
	}
	return child.waitResult
}
func (d *p1SessionDriver) childSignal(ctx context.Context, child *p1SessionChild, signal syscall.Signal, label string) error {
	if child == nil || !child.started {
		return nil
	}
	var err error
	if child.handle != nil {
		err = child.handle.SignalVerified(ctx, signal)
	} else if child.emergencyFD >= 0 {
		_, _, errno := syscall.Syscall6(424, uintptr(child.emergencyFD), uintptr(signal), 0, 0, 0, 0)
		if errno != 0 {
			err = errno
		}
	} else {
		return errP1SessionDriver
	}
	if err != nil {
		return err
	}
	ref, err := d.original(label, map[string]any{"kind": "actual-original-owned-session-child-signal", "role": child.role, "pid": child.cmd.Process.Pid, "identity": child.identity, "signal": int(signal), "source": "explicit isolated session driver action", "emergency_original_pidfd": child.handle == nil})
	if err == nil {
		child.receipts = append(child.receipts, ref)
	}
	return err
}
func (d *p1SessionDriver) End(ctx context.Context) (sessionRecord, error) {
	if d == nil || ctx == nil {
		return sessionRecord{}, errP1SessionDriver
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	record := sessionRecord{Kind: d.kind, Began: d.began, Ended: d.ended}
	if d.closed || d.triggered || d.Trace == nil || d.began.Path == "" {
		return record, errP1SessionDriver
	}
	binding, err := p1SessionGuest()
	if err != nil || binding != d.binding {
		return record, errP1SessionDriver
	}
	if err = d.Trace.Verify(ctx); err != nil {
		return record, err
	}
	if len(d.Trace.SignalOriginals()) != 0 {
		return record, errP1SessionDriver
	}
	ancestry, err := d.ancestry(ctx, "ancestry-before-trigger")
	if err != nil {
		return record, err
	}
	if err = d.client.handle.Verify(ctx); err != nil {
		return record, err
	}
	if d.kind == "login-shell-logout" {
		shell, err := d.proc(d.pre.Parent, "login-shell-before-logout")
		if err != nil {
			return record, err
		}
		raw, err := p1HistoryOriginal(d.pre.ParentStatOriginal, 4096)
		if err != nil {
			return record, err
		}
		_, _, _, start, err := p1SessionStat(raw, d.pre.Parent)
		if err != nil || shell.Start != start || shell.Exe != "/bin/bash" || shell.Session != d.pre.Session || shell.TTY != d.pre.TTY {
			return record, errP1SessionDriver
		}
	}
	before, err := d.original("session-before-trigger", map[string]any{"kind": d.kind, "phase": "fresh-native-manager-and-SSH-ancestry-verified", "remote_identity": d.Trace.Identity(), "remote_birth_original": d.Trace.BirthOriginal(), "ancestry": ancestry, "client_identity": d.client.identity, "core_observation_scope": "factory performs separate fresh product Observe before invoking End", "manager_signal_count": len(d.Trace.SignalOriginals())})
	if err != nil {
		return record, err
	}
	d.triggered = true
	if d.kind == "ssh-disconnect" {
		err = d.childSignal(ctx, d.client, syscall.SIGTERM, "ssh-client-disconnect-signal")
	} else {
		line := "logout 23\n"
		n, e := io.WriteString(d.client.stdin, line)
		if e != nil || n != len(line) {
			err = errP1SessionDriver
		} else {
			_, err = d.original("login-shell-logout-input", map[string]any{"kind": "actual-login-shell-logout-input-written", "client_pid": d.client.cmd.Process.Pid, "input": "logout 23", "bytes": n, "shell_pid": d.pre.Parent})
		}
	}
	if err != nil {
		return record, err
	}
	clientErr := d.waitChild(ctx, d.client)
	var shellExit originalRef
	if clientErr == nil && d.kind == "login-shell-logout" {
		state := d.client.cmd.ProcessState
		status, ok := state.Sys().(syscall.WaitStatus)
		if !ok || !status.Exited() || state.ExitCode() != 23 {
			clientErr = errP1SessionDriver
		} else {
			raw, e := p1HistoryOriginal(d.pre.ParentStatOriginal, 4096)
			if e != nil {
				clientErr = e
			} else {
				_, _, _, start, e := p1SessionStat(raw, d.pre.Parent)
				if e != nil {
					clientErr = e
				} else {
					shellExit, clientErr = d.waitKnownGone(ctx, d.pre.Parent, start, "login-shell-exit")
				}
			}
		}
	}
	managerErr := d.Trace.WaitExited(ctx)
	if len(d.Trace.SignalOriginals()) != 0 {
		managerErr = errors.Join(managerErr, errP1SessionDriver)
	}
	end, writeErr := d.original("session-end", map[string]any{"kind": d.kind, "phase": "actual-session-trigger-and-independent-exit-observations", "began_original": d.began, "before_trigger_original": before, "server_identity": d.server.identity, "client_identity": d.client.identity, "client_wait_observed": d.client.waited, "client_wait_originals": d.client.receipts, "login_shell_exit_original": shellExit, "remote_identity": d.Trace.Identity(), "remote_exit_original": d.Trace.ExitOriginal(), "remote_wait_status_observed": false, "manager_exit_and_trace_eof_validated": managerErr == nil, "client_termination_validated": clientErr == nil, "manager_cleanup_signal_used": len(d.Trace.SignalOriginals()) != 0, "scope": "SSH client/shell statuses never substitute remote manager original-pidfd exit"})
	if writeErr == nil {
		d.ended = end
		record.Ended = end
	}
	return record, errors.Join(clientErr, managerErr, writeErr)
}
func (d *p1SessionDriver) waitKnownGone(ctx context.Context, pid int, start uint64, label string) (originalRef, error) {
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	samples := []originalRef{}
	waits := []map[string]any{}
	for index := 0; index < 160; index++ {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if errors.Is(err, os.ErrNotExist) {
			return d.original(label, map[string]any{"kind": "exact-known-session-descendant-gone", "pid": pid, "start_ticks": strconv.FormatUint(start, 10), "samples": samples, "actual_adopted_waits": waits, "proc_absent": true})
		}
		if err != nil || len(raw) > 4096 {
			return originalRef{}, errP1SessionDriver
		}
		parent, _, _, actual, err := p1SessionStat(raw, pid)
		if err != nil || actual != start {
			return originalRef{}, errP1SessionDriver
		}
		ref, err := p1WriteOriginal(filepath.Join(d.dir, fmt.Sprintf("%s-%03d.stat.raw", label, index)), raw)
		if err != nil {
			return originalRef{}, err
		}
		samples = append(samples, ref)
		d.refs = append(d.refs, ref)
		if parent == os.Getpid() {
			var status syscall.WaitStatus
			got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
			if err != nil && err != syscall.ECHILD {
				return originalRef{}, err
			}
			waits = append(waits, map[string]any{"returned_pid": got, "status": int(status), "ECHILD": err == syscall.ECHILD})
			if got != 0 && got != pid {
				return originalRef{}, errP1SessionDriver
			}
		}
		select {
		case <-bounded.Done():
			return originalRef{}, bounded.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return originalRef{}, errP1SessionDriver
}
func (d *p1SessionDriver) closeChild(ctx context.Context, child *p1SessionChild) error {
	if child == nil || child.closed {
		return nil
	}
	if child.stdin != nil {
		_ = child.stdin.Close()
	}
	if !child.started {
		for _, log := range []*p1SessionLog{child.stdout, child.stderr} {
			if log != nil {
				ref, err := log.finish()
				if err != nil {
					return err
				}
				child.receipts = append(child.receipts, ref)
			}
		}
		child.closed = true
		return nil
	}
	select {
	case <-child.done:
	default:
		signalCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		signalErr := d.childSignal(signalCtx, child, syscall.SIGTERM, child.role+"-cleanup-term")
		cancel()
		select {
		case <-child.done:
		case <-time.After(2 * time.Second):
			killCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			killErr := d.childSignal(killCtx, child, syscall.SIGKILL, child.role+"-cleanup-kill")
			cancel()
			if killErr != nil {
				return errors.Join(signalErr, killErr)
			}
			select {
			case <-child.done:
			case <-time.After(2 * time.Second):
				return errP1SessionDriver
			}
		}
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	result := d.waitChild(waitCtx, child)
	cancel()
	if child.handle != nil {
		if !child.waited {
			return errors.Join(result, errP1SessionDriver)
		}
		exitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		exitErr := child.handle.WaitExited(exitCtx)
		cancel()
		result = errors.Join(result, exitErr, child.handle.Close())
	}
	if child.emergencyFD >= 0 {
		result = errors.Join(result, syscall.Close(child.emergencyFD))
		child.emergencyFD = -1
	}
	child.closed = true
	return result
}
func (d *p1SessionDriver) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil {
		return errP1SessionDriver
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return d.closeResult
	}
	var result error
	if d.Trace != nil {
		result = errors.Join(result, d.Trace.Close(ctx))
	}
	result = errors.Join(result, d.closeChild(ctx, d.client), d.closeChild(ctx, d.server))
	if d.fifoFile != nil {
		result = errors.Join(result, d.fifoFile.Close())
		d.fifoFile = nil
	}
	if d.fifo != "" {
		if err := os.Remove(d.fifo); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	if d.broker != nil {
		result = errors.Join(result, d.broker.Close())
	}
	if d.Trace != nil {
		_, err := d.waitKnownGone(ctx, d.Trace.PID(), d.Trace.Identity().StartTicks, "cleanup-manager-gone")
		result = errors.Join(result, err)
	}
	for index, row := range d.ancestors {
		if d.Trace == nil || d.server == nil || row.PID == d.Trace.PID() || row.PID == d.server.cmd.Process.Pid {
			continue
		}
		_, err := d.waitKnownGone(ctx, row.PID, row.Start, fmt.Sprintf("cleanup-ancestor-%02d", index))
		result = errors.Join(result, err)
	}
	d.closeResult = result
	d.closed = true
	return result
}

const p1SessionRemotePython = `"""Image-guarded SSH/PTY FIFO exec bridge. Never run on the host."""
import hashlib,json,os,re,signal,stat,sys,termios
from pathlib import Path

def unique(pairs):
 result={}
 for key,value in pairs:
  if key in result:raise ValueError('duplicate field')
  result[key]=value
 return result

def read_json(path,limit,mode):
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
 with os.fdopen(fd,'rb') as stream:
  info=os.fstat(stream.fileno());assert info.st_uid==0 and stat.S_IMODE(info.st_mode)==mode
  raw=stream.read(limit+1);assert len(raw)<=limit
 return json.loads(raw,object_pairs_hook=unique)

def save(path,value):
 raw=(json.dumps(value,sort_keys=True)+'\n').encode();fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as stream:stream.write(raw);stream.flush();os.fsync(stream.fileno())
 fd=os.open(str(Path(path).parent),os.O_RDONLY|os.O_DIRECTORY)
 try:os.fsync(fd)
 finally:os.close(fd)

def original(path,raw):
 fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
 with os.fdopen(fd,'wb') as stream:stream.write(raw);stream.flush();os.fsync(stream.fileno())
 return dict(path=path,bytes=len(raw),sha256=hashlib.sha256(raw).hexdigest())

def proc(pid):
 raw=Path('/proc/%d/stat'%pid).read_bytes();text=raw.decode();assert int(text.split(' ',1)[0])==pid
 fields=text[text.rfind(')')+1:].split();assert len(fields)>=20
 return raw,dict(pid=pid,start_ticks=fields[19],ppid=int(fields[1]),pgrp=int(fields[2]),sid=int(fields[3]),tty_nr=int(fields[4]),tpgid=int(fields[5]))

def main():
 assert os.geteuid()==0 and len(sys.argv)==2
 binding=read_json('/work/p1-guest-binding.json',4096,0o600)
 assert set(binding)=={'kind','token','boot_id','test_sha256'} and binding['kind']=='hoopa-p1-isolated-session-guest'
 assert re.fullmatch('[0-9a-f]{64}',binding['token']) and re.fullmatch('[0-9a-f]{64}',binding['test_sha256'])
 markers=[x for x in Path('/proc/cmdline').read_text().split() if x.startswith('hoopa_p1_session_vm=')]
 assert markers==['hoopa_p1_session_vm='+binding['token']]
 assert Path('/proc/sys/kernel/random/boot_id').read_text().strip()==binding['boot_id']
 assert hashlib.sha256(Path('/work/product/integration.test').read_bytes()).hexdigest()==binding['test_sha256']
 connection=os.environ.get('SSH_CONNECTION','').split();assert len(connection)==4 and connection[0]==connection[2]=='127.0.0.1' and connection[3]=='2222'
 assert connection[1].isascii() and connection[1].isdecimal() and 0<int(connection[1])<65536
 assert os.isatty(0);tty=os.ttyname(0);assert tty.startswith('/dev/pts/')
 flags=termios.tcgetattr(0);flags[3]&=~termios.ECHO
 old_ttou=signal.signal(signal.SIGTTOU,signal.SIG_IGN)
 try:termios.tcsetattr(0,termios.TCSANOW,flags)
 finally:signal.signal(signal.SIGTTOU,old_ttou)
 fd=os.open(sys.argv[1],os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
 with os.fdopen(fd,'rb',buffering=0) as stream:
  info=os.fstat(stream.fileno());assert stat.S_ISFIFO(info.st_mode) and info.st_uid==0 and stat.S_IMODE(info.st_mode)==0o600
  raw=stream.readline(4097);assert len(raw)<=4096 and raw.endswith(b'\n')
 data=json.loads(raw,object_pairs_hook=unique);assert set(data)=={'version','kind','probe','arguments','workdir','environment','preexec','shell_original'} and data['version']==1 and data['kind'] in ('ssh-disconnect','login-shell-logout')
 assert data['arguments'][0]==data['probe'] and data['arguments'][1]=='session-trace' and data['arguments'][-1]=='service' and len(data['arguments'])==6
 environment={}
 for item in data['environment']:
  key,value=item.split('=',1);assert key not in environment;environment[key]=value
 assert set(environment)=={'HOME','XDG_CONFIG_HOME','XDG_STATE_HOME','XDG_CACHE_HOME','XDG_RUNTIME_DIR','TMPDIR','LANG','HOOPA_RUN_NONCE'}
 assert re.fullmatch('[0-9a-f]{64}',environment['HOOPA_RUN_NONCE'])
 rawstat,row=proc(os.getpid());parent_raw,parent=proc(row['ppid']);assert row['tty_nr']!=0 and row['sid']==os.getsid(0)
 if data['kind']=='login-shell-logout':
  shell=read_json(data['shell_original'],4096,0o600)
  assert set(shell)=={'pid','interactive','login','huponexit'} and shell['pid']==row['ppid'] and shell['interactive'] is True and shell['login'] is True and shell['huponexit'] is True
  fd=os.open(data['shell_original'],os.O_RDONLY|os.O_NOFOLLOW)
  try:os.fsync(fd)
  finally:os.close(fd)
 prefix=str(Path(data['preexec']).parent);row.update(version=1,kind='actual-remote-python-before-probe-exec',tty_name=tty,ssh_connection=' '.join(connection),boot_id=binding['boot_id'],stat_original=original(prefix+'/remote-preexec.stat.raw',rawstat),parent_stat_original=original(prefix+'/remote-parent.stat.raw',parent_raw),parent_exe=os.readlink('/proc/%d/exe'%row['ppid']),parent_arguments=Path('/proc/%d/cmdline'%row['ppid']).read_bytes().rstrip(b'\0').decode().split('\0'),shell_original=data['shell_original'])
 save(data['preexec'],row)
 os.chdir(data['workdir']);os.execve(data['probe'],data['arguments'],environment)

if __name__=='__main__':main()
`

func TestP1SessionDriverHostGuard(t *testing.T) {
	driver, err := startP1SessionDriver(context.Background(), nil, "ssh-disconnect")
	if err == nil || driver != nil {
		t.Fatal("missing isolated fixture started SSH")
	}
	for _, kind := range []string{"", "system-suspend-resume", "other"} {
		driver, err := startP1SessionDriver(context.Background(), nil, kind)
		if err == nil || driver != nil {
			t.Fatal("unsupported session kind accepted")
		}
	}
}
func TestP1SessionDriverQuote(t *testing.T) {
	for value, want := range map[string]string{"/private/path": "'/private/path'", "a'b": "'a'\"'\"'b'", "with spaces": "'with spaces'"} {
		if p1SessionQuote(value) != want {
			t.Fatal("private path shell quoting changed")
		}
	}
}
func TestP1SessionDriverPythonSyntax(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-S", "-B", "-c", "import sys; compile(sys.stdin.read(), 'remote.py', 'exec')")
	command.Stdin = strings.NewReader(p1SessionRemotePython)
	if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatal("remote helper static syntax invalid")
	}
}

func TestP1SessionDriverLoginLine(t *testing.T) {
	proof := filepath.Join(t.TempDir(), "shell proof.json")
	line := p1SessionLoginLine("/private/helper", "/private/fifo", proof)
	if !strings.HasSuffix(line, "\n") || strings.HasSuffix(line, `\n`) {
		t.Fatal("interactive input lacks real Enter")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/bash", "-n")
	command.Stdin = strings.NewReader(line)
	if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatal("login input shell syntax invalid")
	}
	// Execute only the safe shell-option/printf prefix in a disposable local
	// noninteractive bash. Never execute SSH, the Python helper, or the FIFO path.
	prefix, _, ok := strings.Cut(line, "; PYTHONHOME=/usr ")
	if !ok {
		t.Fatal("bootstrap command boundary missing")
	}
	command = exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", prefix)
	if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatal("safe shell proof formatter failed")
	}
	raw, err := os.ReadFile(proof)
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		PID         int  `json:"pid"`
		Interactive bool `json:"interactive"`
		Login       bool `json:"login"`
		Hup         bool `json:"huponexit"`
	}
	if !p1HistoryJSON(raw, &row, "pid", "interactive", "login", "huponexit") || row.PID <= 0 || row.Interactive || row.Login || !row.Hup {
		t.Fatal("shell proof JSON does not reflect actual option states")
	}
}
func TestP1SessionDriverPythonHelpers(t *testing.T) {
	// Load only pure helper definitions, never main or its guarded SSH actions.
	script := `import ast,json,os,sys
source=sys.stdin.read();tree=ast.parse(source)
selected=[n for n in tree.body if isinstance(n,(ast.Import,ast.ImportFrom)) or isinstance(n,ast.FunctionDef) and n.name!='main']
ns={};exec(compile(ast.Module(body=selected,type_ignores=[]),'helper-definitions','exec'),ns)
path=os.path.join(sys.argv[1],'record.json');ns['save'](path,{'ok':True})
raw=open(path,'rb').read();assert raw.endswith(b'\n') and json.loads(raw)=={'ok':True}
ref=ns['original'](os.path.join(sys.argv[1],'raw'),b'x\0\n');assert ref['bytes']==3
values=[n.value for n in ast.walk(tree) if isinstance(n,ast.Constant)]
assert b'\0' in values and b'\n' in values
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-S", "-B", "-c", script, t.TempDir())
	command.Stdin = strings.NewReader(p1SessionRemotePython)
	if output, err := command.CombinedOutput(); err != nil || len(output) != 0 {
		t.Fatal("remote helper serialization semantics invalid")
	}
}
