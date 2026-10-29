//go:build linux && amd64

package mihomoevents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hoopa/experiments/mihomo-events/capabilityprofile"
	"hoopa/protocol/identityprofile"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type linuxControlSpawn struct {
	Path                      string
	Args, Environment         []string
	Directory, Stdout, Stderr string
}
type linuxControlOps struct {
	guard             func() error
	readFile          func(string, int) ([]byte, error)
	privatePath       func(string, bool) error
	hashFile          func(string, string, int64) error
	hashExecutable    func(int, string) error
	executable        func(string) error
	readlink          func(string) (string, error)
	pidfdOpen         func(int) (int, error)
	signal            func(int, int) error
	exited            func(int) (bool, error)
	waitExit          func(context.Context, int, time.Duration) (bool, error)
	closeFD           func(int) error
	status            func(context.Context, capabilityprofile.Config) (linuxControlStatus, error)
	mkdir             func(string) error
	writeExclusive    func(string, []byte) error
	syncDir           func(string) error
	spawn             func(linuxControlSpawn, func(LinuxControlExitRecord)) error
	exec              func(string, []string, []string) error
	chdir             func(string) error
	pid               func() int
	netnsCookie       func() (uint64, error)
	verifyLaunchFiles func(ControlLaunch) error
	now               func() time.Time
	sleep             func(time.Duration)
}

func newLinuxControlOps() *linuxControlOps {
	o := &linuxControlOps{guard: linuxControlGuestGuard, readFile: linuxControlReadFile, privatePath: controlCheckPath, hashFile: linuxControlHashFile, hashExecutable: linuxControlHashExe, executable: linuxControlExecutable, readlink: os.Readlink, pidfdOpen: linuxControlPidfdOpen, signal: linuxControlSignal, exited: linuxControlExited, waitExit: linuxControlWaitExit, closeFD: syscall.Close, status: linuxControlHTTPStatus, mkdir: func(path string) error { return os.Mkdir(path, 0700) }, writeExclusive: linuxControlWriteExclusive, syncDir: controlSyncDirectory, spawn: linuxControlSpawnProcess, exec: syscall.Exec, chdir: os.Chdir, pid: os.Getpid, netnsCookie: linuxControlNetnsCookie, verifyLaunchFiles: linuxControlVerifyLaunchFiles, now: time.Now, sleep: time.Sleep}
	return o
}
func linuxControlReadFile(path string, limit int) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	c := f.Close()
	if e != nil {
		return nil, e
	}
	if c != nil {
		return nil, c
	}
	if len(b) > limit {
		return nil, ErrLinuxControlRuntime
	}
	return b, nil
}
func linuxControlHashReader(f *os.File, expected string, limit int64) error {
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > limit || st.Mode().Perm()&0022 != 0 {
		return ErrLinuxControlRuntime
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return ErrLinuxControlRuntime
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, limit+1))
	if e != nil || n != st.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
		return ErrLinuxControlRuntime
	}
	return nil
}
func linuxControlHashFile(path, expected string, limit int64) error {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	e = linuxControlHashReader(f, expected, limit)
	c := f.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return ErrLinuxControlRuntime
	}
	return nil
}
func linuxControlExecutable(path string) error {
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 || st.Mode().Perm()&0022 != 0 {
		return ErrLinuxControlRuntime
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 {
		return ErrLinuxControlRuntime
	}
	return nil
}
func linuxControlHashExe(pid int, expected string) error {
	f, e := os.Open("/proc/" + strconv.Itoa(pid) + "/exe")
	if e != nil {
		return ErrLinuxControlRuntime
	}
	e = linuxControlHashReader(f, expected, 128<<20)
	c := f.Close()
	if e != nil || c != nil {
		return ErrLinuxControlRuntime
	}
	return nil
}
func linuxControlGuestGuard() error {
	if os.Geteuid() != 0 {
		return ErrLinuxControlGuest
	}
	raw, e := linuxControlReadFile("/proc/cmdline", 8192)
	if e != nil {
		return ErrLinuxControlGuest
	}
	if linuxControlGuestMarkers(raw) != nil {
		return ErrLinuxControlGuest
	}
	var uts syscall.Utsname
	if syscall.Uname(&uts) != nil {
		return ErrLinuxControlGuest
	}
	release := []byte{}
	for _, c := range uts.Release {
		if c == 0 {
			break
		}
		release = append(release, byte(c))
	}
	if string(release) != linuxControlKernel {
		return ErrLinuxControlGuest
	}
	raw, e = linuxControlReadFile("/sys/kernel/btf/vmlinux", 8<<20)
	if e != nil || len(raw) != 6018902 || linuxControlSHA(raw) != linuxControlBTF {
		return ErrLinuxControlGuest
	}
	raw, e = linuxControlReadFile("/proc/self/cgroup", 4096)
	if e != nil || strings.TrimSpace(string(raw)) != "0::/" {
		return ErrLinuxControlGuest
	}
	for _, ns := range []string{"pid", "net", "user", "mnt", "time", "time_for_children"} {
		self, e := os.Stat("/proc/self/ns/" + ns)
		if e != nil {
			return ErrLinuxControlGuest
		}
		initial, e := os.Stat("/proc/1/ns/" + ns)
		if e != nil || !os.SameFile(self, initial) {
			return ErrLinuxControlGuest
		}
	}
	raw, e = linuxControlReadFile("/proc/self/mountinfo", 128<<10)
	if e != nil {
		return ErrLinuxControlGuest
	}
	matches := 0
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.Split(line, " - ")
		if len(parts) != 2 {
			continue
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) >= 5 && left[4] == "/sys/fs/cgroup" {
			if left[3] != "/" || len(right) < 1 || right[0] != "cgroup2" {
				return ErrLinuxControlGuest
			}
			matches++
		}
	}
	if matches != 1 {
		return ErrLinuxControlGuest
	}
	return nil
}
func linuxControlGuestMarkers(raw []byte) error {
	counts := map[string]int{}
	for _, x := range strings.Fields(string(raw)) {
		counts[x]++
	}
	if counts["hoopa_lab=t132"] != 1 || counts["hoopa_control_lab=t136"] != 1 {
		return ErrLinuxControlGuest
	}
	for x := range counts {
		if strings.HasPrefix(x, "hoopa_lab=") && x != "hoopa_lab=t132" || strings.HasPrefix(x, "hoopa_control_lab=") && x != "hoopa_control_lab=t136" {
			return ErrLinuxControlGuest
		}
	}
	return nil
}
func linuxControlWriteExclusive(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	for len(b) > 0 {
		n, e := f.Write(b)
		if e != nil {
			f.Close()
			return e
		}
		if n == 0 {
			f.Close()
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	e = f.Sync()
	c := f.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return c
	}
	return controlSyncDirectory(filepath.Dir(path))
}
func linuxControlSpawnProcess(s linuxControlSpawn, done func(LinuxControlExitRecord)) error {
	out, e := os.OpenFile(s.Stdout, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	errout, e := os.OpenFile(s.Stderr, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer errout.Close()
	in, e := os.OpenFile("/dev/null", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer in.Close()
	if e = out.Sync(); e != nil {
		return e
	}
	if e = errout.Sync(); e != nil {
		return e
	}
	if e = controlSyncDirectory(s.Directory); e != nil {
		return e
	}
	cmd := &exec.Cmd{Path: s.Path, Args: append([]string(nil), s.Args...), Env: append([]string(nil), s.Environment...), Dir: s.Directory, Stdin: in, Stdout: out, Stderr: errout} // no CommandContext, ExtraFiles or Pdeathsig
	if e = cmd.Start(); e != nil {
		return e
	}
	pid := cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		done(LinuxControlExitRecord{PID: pid, WaitCompleted: true, ExitCode: code})
	}()
	return nil
}
func linuxControlPidfdOpen(pid int) (int, error) {
	r, _, e := syscall.Syscall(434, uintptr(pid), 0, 0)
	if e != 0 {
		return -1, e
	}
	flags, _, e := syscall.Syscall(syscall.SYS_FCNTL, r, syscall.F_GETFD, 0)
	if e != 0 || flags&syscall.FD_CLOEXEC == 0 {
		_ = syscall.Close(int(r))
		return -1, ErrLinuxControlRuntime
	}
	return int(r), nil
}
func linuxControlSignal(fd, sig int) error {
	_, _, e := syscall.Syscall6(424, uintptr(fd), uintptr(sig), 0, 0, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

type linuxControlPollFD struct {
	FD      int32
	Events  int16
	Revents int16
}

func linuxControlPoll(fd int, ms int) (bool, error) {
	p := linuxControlPollFD{FD: int32(fd), Events: 1}
	_, _, e := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&p)), 1, uintptr(ms))
	if e != 0 {
		return false, e
	}
	if p.Revents & ^int16(1|16|64) != 0 {
		return false, ErrLinuxControlRuntime
	}
	return p.Revents&1 != 0, nil
}
func linuxControlExited(fd int) (bool, error) { return linuxControlPoll(fd, 0) }
func linuxControlWaitExit(ctx context.Context, fd int, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		if e := ctx.Err(); e != nil {
			return false, e
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		wait := remaining
		if wait > 100*time.Millisecond {
			wait = 100 * time.Millisecond
		}
		ended, e := linuxControlPoll(fd, int((wait+time.Millisecond-1)/time.Millisecond))
		if e == syscall.EINTR {
			continue
		}
		if e != nil || ended {
			return ended, e
		}
	}
}
func linuxControlNetnsCookie() (uint64, error) {
	fd, e := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if e != nil {
		return 0, e
	}
	var v uint64
	n := uint32(8)
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), syscall.SOL_SOCKET, 71, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&n)), 0)
	c := syscall.Close(fd)
	if errno != 0 || n != 8 || v == 0 || c != nil {
		return 0, ErrLinuxControlRuntime
	}
	return v, nil
}

type linuxControlStat struct {
	PID        int
	StartTicks string
	State      byte
}

func linuxParseStat(raw []byte) (linuxControlStat, error) {
	var out linuxControlStat
	i := bytes.IndexByte(raw, '(')
	j := bytes.LastIndexByte(raw, ')')
	if i < 1 || j < i || len(raw) > 4096 {
		return out, ErrLinuxControlRuntime
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(raw[:i])))
	parts := strings.Fields(string(raw[j+1:]))
	if e != nil || pid <= 0 || pid > 2147483647 || len(parts) < 20 || len(parts[0]) != 1 || !controlTicks.MatchString(parts[19]) {
		return out, ErrLinuxControlRuntime
	}
	if _, e = strconv.ParseUint(parts[19], 10, 64); e != nil {
		return out, ErrLinuxControlRuntime
	}
	return linuxControlStat{PID: pid, StartTicks: parts[19], State: parts[0][0]}, nil
}
func linuxControlVerifyLaunchFiles(l ControlLaunch) error {
	for _, name := range []string{"home", "trace"} {
		if e := controlCheckPath(filepath.Join(l.Directory, name), true); e != nil {
			return ErrLinuxControlRuntime
		}
	}
	if _, e := os.Lstat(filepath.Join(l.Directory, "bootstrap")); !errors.Is(e, os.ErrNotExist) {
		return ErrLinuxControlRuntime
	}
	raw, e := linuxControlReadFile(filepath.Join(l.Directory, "home/config.json"), 8192)
	if e != nil || !bytes.Equal(raw, l.Bundle.Config) {
		return ErrLinuxControlRuntime
	}
	_, m, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		return e
	}
	m.ConfigGeneration = l.Generation
	var actual linuxControlRouteMap
	raw, e = linuxControlReadFile(filepath.Join(l.Directory, "home/route-map.json"), 8192)
	if e != nil || controlDecode(raw, &actual) != nil || !reflect.DeepEqual(m, actual) {
		return ErrLinuxControlRuntime
	}
	for _, name := range []string{"home/config.json", "home/route-map.json"} {
		if e = controlCheckPath(filepath.Join(l.Directory, name), false); e != nil {
			return ErrLinuxControlRuntime
		}
	}
	return nil
}
func linuxControlCaptureGeneration(l ControlLaunch) (string, error) {
	nonce, e := hex.DecodeString(l.Nonce)
	if e != nil || len(nonce) != 16 {
		return "", ErrLinuxControlRuntime
	}
	g := binary.LittleEndian.Uint64(nonce[:8])
	if g == 0 {
		return "", ErrLinuxControlRuntime
	}
	return strconv.FormatUint(g, 10), nil
}
func linuxControlExecSpec(l ControlLaunch, a LinuxControlAssets, c capabilityprofile.Config, cookie uint64) (linuxControlSpawn, error) {
	generation, e := linuxControlCaptureGeneration(l)
	if e != nil || cookie == 0 || len(c.Listeners) != 1 {
		return linuxControlSpawn{}, ErrLinuxControlRuntime
	}
	home := filepath.Join(l.Directory, "home")
	config := filepath.Join(home, "config.json")
	env := []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "PYTHONDONTWRITEBYTECODE=1", "HOOPA_CONTROL_NONCE=" + l.Nonce, "HOOPA_GUARD_GENERATION=" + l.Generation, "HOOPA_GUARD_CONFIG_SHA256=" + l.Bundle.ConfigSHA256, "HOOPA_TRACE_INBOUND=" + c.Listeners[0].Name, "HOOPA_TRACE_PROFILE=5", "HOOPA_TRACE_GENERATION=" + l.Generation, "HOOPA_TRACE_ROUTE_MAP=" + filepath.Join(home, "route-map.json"), "HOOPA_TRACE_DIR=" + filepath.Join(l.Directory, "trace")}
	argv := []string{a.Python.Path, "-S", "-B", a.Bootstrap.Path, "--allow-bpf-load", "--profile", "scoped-executable-online", "--identity-profile", identityprofile.ID, "--object", a.BPF.Path, "--object-sha256", a.BPF.SHA256, "--libbpf", a.Libbpf.Path, "--libbpf-sha256", a.Libbpf.SHA256, "--core", l.Bundle.CorePath, "--core-sha256", l.Bundle.CoreSHA256, "--core-config", config, "--core-home", home, "--core-generation", l.Generation, "--generation", generation, "--netns-cookie", strconv.FormatUint(cookie, 10), "--scope", linuxControlScope(l), "--port", strconv.Itoa(int(c.Listeners[0].Port)), "--output", filepath.Join(l.Directory, "bootstrap")}
	return linuxControlSpawn{Path: a.Python.Path, Args: argv, Environment: env}, nil
}

type linuxControlStatus struct {
	SchemaVersion          int    `json:"schema_version"`
	ConfigGeneration       string `json:"config_generation"`
	ConfigSHA256           string `json:"config_sha256"`
	State                  string `json:"state"`
	IdentityInstance       bool   `json:"identity_instance"`
	IdentityName           bool   `json:"identity_name"`
	IdentityPath           bool   `json:"identity_path"`
	AcceptedTCPConnections int    `json:"accepted_tcp_connections"`
	Admission              struct {
		Admitted uint64 `json:"admitted"`
		Rejected uint64 `json:"rejected"`
		Revoked  bool   `json:"revoked"`
	} `json:"experimental_admission"`
}

func linuxControlHTTPStatus(ctx context.Context, c capabilityprofile.Config) (linuxControlStatus, error) {
	var s linuxControlStatus
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tr := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := http.NewRequestWithContext(ctx, "GET", "http://"+c.Controller+"/hoopa/status", nil)
	if e != nil {
		return s, ErrLinuxControlRuntime
	}
	r.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, e := client.Do(r)
	if e != nil {
		return s, ErrLinuxControlRuntime
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if e != nil || len(raw) > 8192 || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || controlDecode(raw, &s) != nil {
		return s, ErrLinuxControlRuntime
	}
	return s, nil
}
func linuxControlStatusMatches(s linuxControlStatus, l ControlLaunch) bool {
	return s.SchemaVersion == 1 && s.ConfigGeneration == l.Generation && s.ConfigSHA256 == l.Bundle.ConfigSHA256 && s.State == "running" && !s.IdentityInstance && !s.IdentityName && !s.IdentityPath && s.AcceptedTCPConnections >= 0 && !s.Admission.Revoked
}
func linuxControlEnvironment(raw []byte) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > 64<<10 || raw[len(raw)-1] != 0 {
		return nil, ErrLinuxControlRuntime
	}
	out := map[string]string{}
	for _, part := range bytes.Split(raw[:len(raw)-1], []byte{0}) {
		key, v, ok := strings.Cut(string(part), "=")
		if !ok || key == "" {
			return nil, ErrLinuxControlRuntime
		}
		if _, exists := out[key]; exists {
			return nil, ErrLinuxControlRuntime
		}
		out[key] = v
	}
	return out, nil
}
func linuxControlEnvMatches(raw []byte, l ControlLaunch, c capabilityprofile.Config, a LinuxControlAssets) bool {
	env, e := linuxControlEnvironment(raw)
	if e != nil {
		return false
	}
	spec, e := linuxControlExecSpec(l, a, c, 1)
	if e != nil {
		return false
	}
	for _, row := range spec.Environment {
		k, v, _ := strings.Cut(row, "=")
		if env[k] != v {
			return false
		}
	}
	if len(env) != len(spec.Environment)+1 {
		return false
	}
	var h map[string]json.RawMessage
	if controlDecode([]byte(env["HOOPA_IDENTITY_HANDOFF"]), &h) != nil {
		return false
	}
	for key, want := range map[string]string{"schema": "hoopa-core-identity-handoff-v4", "core_generation": l.Generation, "source_path": linuxControlScope(l)} {
		var v string
		if json.Unmarshal(h[key], &v) != nil || v != want {
			return false
		}
	}
	nonce, _ := hex.DecodeString(l.Nonce)
	var generation string
	if json.Unmarshal(h["generation"], &generation) != nil || generation != strconv.FormatUint(binary.LittleEndian.Uint64(nonce[:8]), 10) {
		return false
	}
	return true
}
func (b *LinuxControlBackend) runtimeFiles(l ControlLaunch) error {
	for _, name := range []string{"home", "trace"} {
		if b.ops.privatePath(filepath.Join(l.Directory, name), true) != nil {
			return ErrLinuxControlRuntime
		}
	}
	for _, name := range []string{"home/config.json", "home/route-map.json"} {
		if b.ops.privatePath(filepath.Join(l.Directory, name), false) != nil {
			return ErrLinuxControlRuntime
		}
	}
	raw, e := b.ops.readFile(filepath.Join(l.Directory, "home/config.json"), 8192)
	if e != nil || !bytes.Equal(raw, l.Bundle.Config) {
		return ErrLinuxControlRuntime
	}
	_, m, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		return e
	}
	m.ConfigGeneration = l.Generation
	raw, e = b.ops.readFile(filepath.Join(l.Directory, "home/route-map.json"), 8192)
	var actual linuxControlRouteMap
	if e != nil || controlDecode(raw, &actual) != nil || !reflect.DeepEqual(actual, m) {
		return ErrLinuxControlRuntime
	}
	return nil
}
func (b *LinuxControlBackend) verify(ctx context.Context, l ControlLaunch) (ControlObservation, int, error) {
	unknown := ControlObservation{State: ControlUnknown}
	if e := b.intent(l); e != nil {
		return unknown, -1, ErrLinuxControlRuntime
	}
	if e := b.verifyAssets(); e != nil {
		return unknown, -1, e
	}
	c, _, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		return unknown, -1, e
	}
	if e = b.runtimeFiles(l); e != nil {
		return unknown, -1, e
	}
	claim, e := linuxReadClaim(l, b.assetsDigest, b.ops)
	if e != nil {
		return unknown, -1, e
	}
	boot, e := b.ops.readFile("/proc/sys/kernel/random/boot_id", 64)
	if e != nil || strings.TrimSpace(string(boot)) != claim.Instance.BootID {
		return unknown, -1, ErrLinuxControlRuntime
	}
	fd, e := b.ops.pidfdOpen(claim.Instance.PID)
	if e != nil {
		if errors.Is(e, syscall.ESRCH) {
			return ControlObservation{State: ControlVerifiedStopped}, -1, nil
		}
		return unknown, -1, ErrLinuxControlRuntime
	}
	proc := "/proc/" + strconv.Itoa(claim.Instance.PID)
	readStat := func() (linuxControlStat, error) {
		raw, e := b.ops.readFile(proc+"/stat", 4096)
		if e != nil {
			return linuxControlStat{}, ErrLinuxControlRuntime
		}
		p, e := linuxParseStat(raw)
		if e != nil || p.PID != claim.Instance.PID || p.StartTicks != claim.Instance.StartTicks {
			return p, ErrLinuxControlRuntime
		}
		return p, nil
	}
	firstStat, err := readStat()
	if err != nil {
		return unknown, fd, err
	}
	ended, e := b.ops.exited(fd)
	if e != nil {
		return unknown, fd, ErrLinuxControlRuntime
	}
	if ended {
		return ControlObservation{State: ControlVerifiedStopped}, fd, nil
	}
	if firstStat.State == 'Z' || firstStat.State == 'X' {
		return unknown, fd, ErrLinuxControlRuntime
	}
	exe, e := b.ops.readlink(proc + "/exe")
	if e != nil || exe != l.Bundle.CorePath {
		return unknown, fd, ErrLinuxControlRuntime
	}
	if b.ops.hashExecutable(claim.Instance.PID, LinuxControlCoreSHA256) != nil {
		return unknown, fd, ErrLinuxControlRuntime
	}
	cwd, e := b.ops.readlink(proc + "/cwd")
	if e != nil || cwd != filepath.Join(l.Directory, "home") {
		return unknown, fd, ErrLinuxControlRuntime
	}
	cmdline, e := b.ops.readFile(proc+"/cmdline", 16384)
	expected := []string{l.Bundle.CorePath, "-d", filepath.Join(l.Directory, "home"), "-f", filepath.Join(l.Directory, "home/config.json")}
	if e != nil || !bytes.Equal(cmdline, []byte(strings.Join(expected, "\x00")+"\x00")) {
		return unknown, fd, ErrLinuxControlRuntime
	}
	env, e := b.ops.readFile(proc+"/environ", 64<<10)
	if e != nil || !linuxControlEnvMatches(env, l, c, b.assets) {
		return unknown, fd, ErrLinuxControlRuntime
	}
	status, e := b.ops.status(ctx, c)
	if e != nil || !linuxControlStatusMatches(status, l) {
		return unknown, fd, ErrLinuxControlRuntime
	}
	lastStat, err := readStat()
	if err != nil || lastStat.State == 'Z' || lastStat.State == 'X' {
		return unknown, fd, ErrLinuxControlRuntime
	}
	lastExe, err := b.ops.readlink(proc + "/exe")
	if err != nil || lastExe != exe || b.ops.hashExecutable(claim.Instance.PID, LinuxControlCoreSHA256) != nil {
		return unknown, fd, ErrLinuxControlRuntime
	}
	lastCwd, err := b.ops.readlink(proc + "/cwd")
	if err != nil || lastCwd != cwd {
		return unknown, fd, ErrLinuxControlRuntime
	}
	lastArgs, err := b.ops.readFile(proc+"/cmdline", 16384)
	if err != nil || !bytes.Equal(lastArgs, cmdline) {
		return unknown, fd, ErrLinuxControlRuntime
	}
	lastEnv, err := b.ops.readFile(proc+"/environ", 64<<10)
	if err != nil || !bytes.Equal(lastEnv, env) {
		return unknown, fd, ErrLinuxControlRuntime
	}
	ended, e = b.ops.exited(fd)
	if e != nil || ended {
		return unknown, fd, ErrLinuxControlRuntime
	}
	return ControlObservation{State: ControlVerifiedRunning, Instance: &claim.Instance}, fd, nil
}
