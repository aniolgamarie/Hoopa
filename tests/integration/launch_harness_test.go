//go:build linux && amd64 && us4integration && onboardingintegration && us2integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

//go:embed testdata/launch-pty.py
var launchPTYScript []byte

const launchEnvSecret = "T053_PRIVATE_ENV_486feab3"

type launchHarness struct {
	t         *testing.T
	r         *onboardingHarness
	child     string
	processes []*launchProcess
	finished  bool
}

func newLaunchHarness(t *testing.T) *launchHarness {
	t.Helper()
	h := &launchHarness{t: t, r: newOnboardingHarness(t)}
	t.Cleanup(h.cleanup)
	source, want := os.Getenv("HOOPA_T053_CHILD"), os.Getenv("HOOPA_T053_CHILD_SHA256")
	if !filepath.IsAbs(source) || len(want) != 64 {
		t.Fatal("explicit pinned independent application fixture required, cannot skip")
	}
	raw, err := os.ReadFile(source)
	appMust(t, err)
	sum := sha256.Sum256(raw)
	info, err := buildinfo.ReadFile(source)
	if err != nil || info.Path != "hoopa/tests/integration/testdata/launch-child" || hex.EncodeToString(sum[:]) != want {
		t.Fatal("wrong application fixture binary/hash")
	}
	h.child = filepath.Join(h.r.h.root, "launch-child")
	file, err := os.OpenFile(h.child, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	appMust(t, err)
	_, err = file.Write(raw)
	appMust(t, err)
	appMust(t, file.Close())
	c := h.r.h.open()
	appMust(t, c.InitializeLaunchHistory(appCtx(t), h.r.h.activeWith(c).Revision))
	appMust(t, c.Close())
	input := onboardingWriteIntent(t, h.r.h, "launch-prerequisite.json")
	output := filepath.Join(h.r.h.root, "launch-prerequisite.plan.json")
	plan := onboardingCLIPlan(t, h.r, input, output)
	h.r.start()
	reply := onboardingCLIApply(t, h.r, output, plan, "launch-setup-apply")
	if !reply.OK {
		t.Fatal("actual CLI prerequisite Apply failed")
	}
	return h
}
func (h *launchHarness) target() string {
	_, port, err := net.SplitHostPort(h.r.h.originAddress)
	appMust(h.t, err)
	return "http://localhost:" + port
}

func (h *launchHarness) env() []string {
	return append(h.r.cli.env(), "HOOPA_T053_PRIVATE_ENV="+launchEnvSecret, "http_proxy=parent-private-lower", "HTTP_PROXY=parent-private-upper")
}
func (h *launchHarness) start(mode string, meter bool, fixture string, args ...string) *launchProcess {
	return h.startApp("app-a", mode, meter, fixture, args...)
}
func (h *launchHarness) startApp(app, mode string, meter bool, fixture string, args ...string) *launchProcess {
	argv := []string{"run", "--app", app, "--mode", mode}
	if meter {
		argv = append(argv, "--meter")
	}
	argv = append(argv, "--", h.child, fixture)
	argv = append(argv, args...)
	return h.process(h.r.cli.path, argv, 0)
}
func (h *launchHarness) sentinel(group int) *launchProcess {
	return h.process(h.child, []string{"sentinel"}, group)
}
func (h *launchHarness) views() []control.LaunchView {
	before := h.r.h.tree()
	values, err := control.ReadLaunches(appCtx(h.t), h.r.h.paths)
	appMust(h.t, err)
	if !reflect.DeepEqual(before, h.r.h.tree()) {
		h.t.Fatal("ReadLaunches mutated protected state")
	}
	return values
}
func (h *launchHarness) newView(before []control.LaunchView) control.LaunchView {
	old := map[config.ID]bool{}
	for _, view := range before {
		old[view.LaunchID] = true
	}
	after := h.views()
	if len(after) != len(before)+1 {
		h.t.Fatal("launch inventory not complete/exact")
	}
	for _, view := range after {
		if !old[view.LaunchID] {
			return view
		}
	}
	h.t.Fatal("new launch identity missing")
	return control.LaunchView{}
}
func (h *launchHarness) view(id config.ID) control.LaunchView {
	for _, view := range h.views() {
		if view.LaunchID == id {
			return view
		}
	}
	h.t.Fatal("retained launch vanished")
	return control.LaunchView{}
}
func (h *launchHarness) base(view control.LaunchView, mode, dispatch string) {
	active := h.r.h.active()
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	appMust(h.t, err)
	if view.Root != nil && view.Root.BootID != strings.TrimSpace(string(boot)) {
		h.t.Fatal("root boot identity differs from actual guest boot")
	}
	if view.LaunchID == "" || view.AppID != "app-a" || view.Mode != mode || view.Metering || view.DispatchState != dispatch || view.ProfileRevision != string(active.Revision) || view.ProfileGeneration == nil || active.Generation == nil || string(*view.ProfileGeneration) != string(*active.Generation) || view.Root == nil || view.Root.PID <= 0 || view.Root.BootID == "" || view.Root.StartTicks == 0 {
		h.t.Fatal("root/profile identity metadata incomplete")
	}
	if view.DescendantsState != "unknown" || view.DescendantsReason != "not_enumerated" {
		h.t.Fatal("direct root completion fabricated descendant census")
	}
	if mode == "managed" {
		raw, err := os.ReadFile(h.r.h.bindingPath("app-a"))
		appMust(h.t, err)
		sum := sha256.Sum256(raw)
		if view.EntryBindingSHA256 == nil || *view.EntryBindingSHA256 != hex.EncodeToString(sum[:]) || view.EntryLeaseState != "unknown" {
			h.t.Fatal("managed terminal lease lost original identity/unknown state")
		}
	} else if view.EntryBindingSHA256 != nil || view.EntryLeaseState != "none" {
		h.t.Fatal("unmanaged invocation allocated entry lease")
	}
}
func (h *launchHarness) normal(view control.LaunchView, code int, mode string) {
	h.base(view, mode, "authorization_recorded")
	if view.ExecFailureReason != nil {
		h.t.Fatal("normal application terminal result carries exec-failure reason")
	}
	if view.Root.ExitCode == nil || *view.Root.ExitCode != code || view.Root.Signal != nil || view.Root.WaitStatus == nil || *view.Root.WaitStatus != uint32(code<<8) {
		h.t.Fatal("normal exit was not retained exactly")
	}
}
func (h *launchHarness) signaled(view control.LaunchView, sig int, mode string) {
	h.base(view, mode, "authorization_recorded")
	if view.ExecFailureReason != nil {
		h.t.Fatal("normal application terminal result carries exec-failure reason")
	}
	if view.Root.Signal == nil || *view.Root.Signal != sig || view.Root.ExitCode != nil || view.Root.WaitStatus == nil || *view.Root.WaitStatus != uint32(sig) {
		h.t.Fatal("signal exit conflated numeric128+signal")
	}
}
func (h *launchHarness) execFormat(view control.LaunchView) {
	h.base(view, "direct", "exec_failure_reported")
	if view.ExecFailureReason == nil || *view.ExecFailureReason != "exec_format" || view.Root.ExitCode == nil || *view.Root.ExitCode != 126 || view.Root.Signal != nil || view.Root.WaitStatus == nil || *view.Root.WaitStatus != uint32(126<<8) {
		h.t.Fatal("fixed launcher exec-format channel/terminal metadata missing")
	}
}
func (h *launchHarness) stable() map[string]string {
	result := map[string]string{}
	state := h.r.h.paths.State
	for path, value := range h.r.h.tree() {
		for _, name := range []string{"active.json", "operations", "bundles", "source-assets", "credentials", "runs"} {
			prefix := filepath.Join(state, name)
			if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
				result[path] = value
			}
		}
	}
	return result
}
func (h *launchHarness) noPrivatePersisted(markers ...string) {
	for path := range h.r.h.tree() {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 64<<20 {
			h.t.Fatal("privacy snapshot file exceeds known bounded profile")
		}
		raw, err := os.ReadFile(path)
		appMust(h.t, err)
		for _, marker := range markers {
			if launchPrivateBytes(raw, marker, 0) {
				h.t.Fatal("product persisted full private argv/environment marker")
			}
		}
	}
	raw, err := json.Marshal(h.views())
	appMust(h.t, err)
	for _, marker := range markers {
		if bytes.Contains(raw, []byte(marker)) {
			h.t.Fatal("metadata exposed private argv/environment")
		}
	}
}

// Existing private JSON receipts may encode byte fields as base64. Check those
// bounded representations too, without choosing a future launch record schema.
func launchPrivateBytes(raw []byte, marker string, depth int) bool {
	if bytes.Contains(raw, []byte(marker)) {
		return true
	}
	if depth >= 6 {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		var visit func(any) bool
		visit = func(v any) bool {
			switch v := v.(type) {
			case string:
				if launchPrivateBytes([]byte(v), marker, depth+1) {
					return true
				}
				if decoded, err := base64.StdEncoding.DecodeString(v); err == nil && launchPrivateBytes(decoded, marker, depth+1) {
					return true
				}
			case []any:
				for _, child := range v {
					if visit(child) {
						return true
					}
				}
			case map[string]any:
				for _, child := range v {
					if visit(child) {
						return true
					}
				}
			}
			return false
		}
		return visit(value)
	}
	return false
}
func (h *launchHarness) reject(app, mode string, meter bool, code int, diagnostic, marker string) {
	stable := h.stable()
	old := h.views()
	p := h.startApp(app, mode, meter, "sentinel-file", marker)
	p.closeInput()
	p.wait(code)
	p.drain()
	if len(p.stdout()) != 0 || !bytes.Equal(p.stderr(), []byte(diagnostic)) {
		h.t.Fatal("launch refusal diagnostic differs from exact finite stderr line")
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		h.t.Fatal("refused invocation dispatched application")
	}
	for _, private := range []string{marker, h.child, launchEnvSecret, "parent-private-lower"} {
		if bytes.Contains(p.stderr(), []byte(private)) {
			h.t.Fatal("launch diagnostic leaked private input")
		}
	}
	if !reflect.DeepEqual(stable, h.stable()) {
		h.t.Fatal("refused run altered configuration/runtime")
	}
	if !reflect.DeepEqual(old, h.views()) {
		h.t.Fatal("pre-dispatch refusal allocated a launch or changed prior history")
	}
}
func (h *launchHarness) finish() {
	if h.finished {
		return
	}
	if !h.r.started {
		h.r.h.trace.verify(h.t)
		h.finished = true
		return
	}
	if h.r.h.active().DesiredRuntime == "running" {
		h.r.finish()
	} else {
		h.r.h.trace.verify(h.t)
		h.r.stopService()
	}
	h.finished = true
}
func (h *launchHarness) cleanup() {
	for _, p := range h.processes {
		p.cleanup()
	}
}

type launchBuffer struct {
	mu   sync.Mutex
	data []byte
	err  error
	file *os.File
	done chan struct{}
}

func (b *launchBuffer) Write(raw []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(raw) > 4<<20 {
		return 0, errors.New("bounded fixture output")
	}
	b.data = append(b.data, raw...)
	return b.file.Write(raw)
}
func (b *launchBuffer) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.data)
}

type launchProcess struct {
	h           *launchHarness
	cmd         *exec.Cmd
	input       *os.File
	out, errout *launchBuffer
	handle      *launchHandle
	done        chan error
	finished    bool
	closed      bool
}

func (h *launchHarness) process(executable string, args []string, group int) *launchProcess {
	p := &launchProcess{h: h, cmd: exec.Command(executable, args...), done: make(chan error, 1)}
	inputRead, inputWrite, err := os.Pipe()
	appMust(h.t, err)
	p.input = inputWrite
	p.cmd.Stdin = inputRead
	p.cmd.Env = h.env()
	rawfd := -1
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: group, PidFD: &rawfd}
	readers := []*os.File{}
	writers := []*os.File{}
	for _, which := range []string{"stdout", "stderr"} {
		reader, writer, err := os.Pipe()
		appMust(h.t, err)
		file, err := os.OpenFile(filepath.Join(h.r.h.root, fmt.Sprintf("launch-%03d.%s", len(h.processes)+1, which)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		appMust(h.t, err)
		buffer := &launchBuffer{file: file, done: make(chan struct{})}
		if which == "stdout" {
			p.out = buffer
			p.cmd.Stdout = writer
		} else {
			p.errout = buffer
			p.cmd.Stderr = writer
		}
		readers = append(readers, reader)
		writers = append(writers, writer)
	}
	appMust(h.t, p.cmd.Start())
	inputRead.Close()
	for _, writer := range writers {
		writer.Close()
	}
	if rawfd < 0 {
		h.t.Fatal("original wrapper clone pidfd unavailable")
	}
	pid, start, _, _, err := launchStat(p.cmd.Process.Pid)
	appMust(h.t, err)
	p.handle = &launchHandle{t: h.t, fd: os.NewFile(uintptr(rawfd), "original-launch-process"), pid: pid, start: start}
	h.processes = append(h.processes, p)
	for i, b := range []*launchBuffer{p.out, p.errout} {
		reader := readers[i]
		go func(b *launchBuffer) {
			_, err := io.Copy(b, reader)
			reader.Close()
			b.mu.Lock()
			b.err = err
			b.mu.Unlock()
			b.file.Close()
			close(b.done)
		}(b)
	}
	go func() { p.done <- p.cmd.Wait() }()
	return p
}
func (p *launchProcess) write(raw []byte) {
	p.h.t.Helper()
	_, err := p.input.Write(raw)
	appMust(p.h.t, err)
}
func (p *launchProcess) closeInput() { p.input.Close() }
func (p *launchProcess) signal(sig syscall.Signal) {
	_, _, errno := syscall.Syscall6(424, p.handle.fd.Fd(), uintptr(sig), 0, 0, 0, 0)
	if errno != 0 {
		p.h.t.Fatal("original wrapper pidfd signal refused", errno)
	}
}
func (p *launchProcess) wait(want int) {
	p.h.t.Helper()
	if p.finished {
		p.h.t.Fatal("duplicate wrapper wait")
	}
	select {
	case <-p.done:
		p.finished = true
	case <-time.After(40 * time.Second):
		p.h.t.Fatal("wrapper timeout is not terminal success")
	}
	state := p.cmd.ProcessState
	if state == nil || !state.Exited() || state.ExitCode() != want {
		p.h.t.Fatal("actual wrapper exit differs from required child result/refusal")
	}
}
func (p *launchProcess) event(name string) map[string]json.RawMessage {
	return p.until(func(value map[string]json.RawMessage) bool {
		var event string
		_ = json.Unmarshal(value["event"], &event)
		return event == name
	})
}
func (p *launchProcess) until(match func(map[string]json.RawMessage) bool) map[string]json.RawMessage {
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range bytes.Split(p.out.snapshot(), []byte{'\n'}) {
			var event map[string]json.RawMessage
			if json.Unmarshal(line, &event) == nil && match(event) {
				return event
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.h.t.Fatal("application barrier missing, not proof of rejection")
	return nil
}
func (p *launchProcess) traffic(want string) {
	p.until(func(value map[string]json.RawMessage) bool {
		var event, body string
		_ = json.Unmarshal(value["event"], &event)
		_ = json.Unmarshal(value["body"], &body)
		return event == "traffic" && body == want
	})
}
func (p *launchProcess) stdoutEnded() bool {
	select {
	case <-p.out.done:
		return true
	default:
		return false
	}
}
func (p *launchProcess) drain() {
	for _, b := range []*launchBuffer{p.out, p.errout} {
		select {
		case <-b.done:
		case <-time.After(35 * time.Second):
			p.h.t.Fatal("bounded fixture stream never closed")
		}
		b.mu.Lock()
		err := b.err
		b.mu.Unlock()
		appMust(p.h.t, err)
	}
}
func (p *launchProcess) stdout() []byte { p.drain(); return p.out.snapshot() }
func (p *launchProcess) stderr() []byte { p.drain(); return p.errout.snapshot() }
func (p *launchProcess) cleanup() {
	if p.closed {
		return
	}
	p.closed = true
	if !p.finished {
		p.h.t.Error("launch needed cleanup; cannot count as pass")
		p.signal(syscall.SIGTERM)
		select {
		case <-p.done:
			p.finished = true
		case <-time.After(35 * time.Second):
			p.h.t.Error("original launch not terminal after bounded cleanup")
		}
	}
	p.closeInput()
	if p.handle != nil {
		p.handle.fd.Close()
	}
}
func launchInt(t *testing.T, value map[string]json.RawMessage, key string) int {
	t.Helper()
	var result int
	appMust(t, json.Unmarshal(value[key], &result))
	if result <= 0 {
		t.Fatal("invalid fixture original PID")
	}
	return result
}
func launchStat(pid int) (int, uint64, int, string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, 0, 0, "", err
	}
	open, close := strings.IndexByte(string(raw), '('), strings.LastIndexByte(string(raw), ')')
	if open < 1 || close < open {
		return 0, 0, 0, "", errors.New("invalid fixture stat")
	}
	actual, err := strconv.Atoi(strings.TrimSpace(string(raw[:open])))
	fields := strings.Fields(string(raw[close+1:]))
	if err != nil || actual != pid || len(fields) < 20 {
		return 0, 0, 0, "", errors.New("fixture identity mismatch")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, 0, 0, "", errors.New("fixture start unavailable")
	}
	ppid, err := strconv.Atoi(fields[1])
	return actual, ticks, ppid, fields[0], err
}

type launchHandle struct {
	t     *testing.T
	fd    *os.File
	pid   int
	start uint64
}

func launchObserve(t *testing.T, pid int) *launchHandle {
	t.Helper()
	_, start, _, _, err := launchStat(pid)
	appMust(t, err)
	raw, _, errno := syscall.Syscall(434, uintptr(pid), 0, 0)
	if errno != 0 {
		t.Fatal("original fixture pidfd unavailable", errno)
	}
	fd := os.NewFile(raw, "observed-fixture-original")
	_, after, _, _, err := launchStat(pid)
	if err != nil || start != after {
		fd.Close()
		t.Fatal("fixture identity changed around pidfd")
	}
	t.Cleanup(func() { fd.Close() })
	return &launchHandle{t: t, fd: fd, pid: pid, start: start}
}
func (h *launchHandle) ready() bool {
	type poll struct {
		FD               int32
		Events, Returned int16
	}
	p := poll{FD: int32(h.fd.Fd()), Events: 1}
	n, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&p)), 1, 0)
	if errno != 0 || p.Returned&0x20 != 0 {
		h.t.Fatal("invalid original pidfd observation")
	}
	return n > 0 && p.Returned&1 != 0
}
func (h *launchHandle) exited(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("original pidfd did not reach terminal before bound")
}
func (h *launchHandle) reapAdopted(t *testing.T) {
	t.Helper()
	pid, ticks, parent, state, err := launchStat(h.pid)
	if os.IsNotExist(err) {
		return
	}
	appMust(t, err)
	if pid != h.pid || ticks != h.start || parent != os.Getpid() || state != "Z" {
		t.Fatal("adopted fixture not original owned zombie")
	}
	var status syscall.WaitStatus
	got, err := syscall.Wait4(h.pid, &status, syscall.WNOHANG, nil)
	if err != nil || got != h.pid || !status.Exited() && !status.Signaled() {
		t.Fatal("exact adopted fixture reap refused")
	}
}

type launchPTYReport struct {
	WrapperPID       int    `json:"wrapper_pid"`
	RootPID          int    `json:"root_pid"`
	RootStartTicks   uint64 `json:"root_start_ticks"`
	ChildForeground  int    `json:"child_foreground"`
	AfterForeground  int    `json:"after_foreground"`
	WrapperExit      int    `json:"wrapper_exit"`
	TTY              []bool `json:"tty"`
	Raw              []byte `json:"raw"`
	TermiosUnchanged bool   `json:"termios_unchanged"`
	WrapperUnreaped  bool   `json:"wrapper_unreaped"`
}

func (h *launchHarness) pty(mode string) launchPTYReport {
	script := filepath.Join(h.r.h.root, "launch-pty.py")
	if _, err := os.Lstat(script); os.IsNotExist(err) {
		appMust(h.t, os.WriteFile(script, launchPTYScript, 0600))
	}
	cmd := exec.Command("/usr/bin/python3", script, h.r.cli.path, h.child, mode)
	cmd.Env = h.env()
	raw, err := cmd.Output()
	// Preserve private transport diagnostics before reporting the original
	// failure. These bytes are fixture evidence, never a public CLI diagnostic.
	var driverStderr []byte
	errorClass := "none"
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		driverStderr = append([]byte{}, exitError.Stderr...)
		errorClass = "exit_error"
	} else if err != nil {
		errorClass = "execution_error"
	}
	var exitCode *int
	var rawWaitStatus *uint32
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		exitCode = &code
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			rawStatus := uint32(status)
			rawWaitStatus = &rawStatus
		}
	}
	prefix := filepath.Join(h.r.h.root, "launch-"+mode+".driver.")
	appMust(h.t, os.WriteFile(prefix+"stdout", raw, 0600))
	appMust(h.t, os.WriteFile(prefix+"stderr", driverStderr, 0600))
	statusRaw, statusErr := json.Marshal(map[string]any{"schema_version": 1, "exit_code": exitCode, "raw_wait_status": rawWaitStatus, "error_class": errorClass})
	appMust(h.t, statusErr)
	appMust(h.t, os.WriteFile(prefix+"status.json", append(statusRaw, '\n'), 0600))
	if err != nil {
		h.t.Fatal("controlled PTY driver failed; not a product terminal witness")
	}
	var report launchPTYReport
	appMust(h.t, json.Unmarshal(raw, &report))
	if len(report.TTY) != 3 {
		h.t.Fatal("PTY report incomplete")
	}
	appMust(h.t, os.WriteFile(filepath.Join(h.r.h.root, "launch-"+mode+".pty.json"), raw, 0600))
	return report
}
