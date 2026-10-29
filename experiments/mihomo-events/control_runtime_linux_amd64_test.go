//go:build linux && amd64

package mihomoevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/experiments/mihomo-events/capabilityprofile"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Every native operation in this fixture is injected. It never opens a real
// pidfd, spawns, signals, connects or loads BPF.
type linuxRuntimeFake struct {
	files                      map[string][]byte
	links                      map[string]string
	dirs                       map[string]bool
	events                     []string
	signalFDs, signals, closed []int
	openError                  error
	exited                     bool
	exitChecks                 int
	exitAt                     int
	status                     linuxControlStatus
	statusError                error
	statusHook                 func()
	afterSpawn                 func()
	privateError               string
	hashError                  string
	clock                      time.Time
	backend                    *LinuxControlBackend
	launch                     ControlLaunch
	claim                      LinuxControlClaim
}

func linuxRuntimeBundle() ControlBundle {
	raw := []byte(`{"mode":"rule","log-level":"silent","ipv6":false,"find-process-mode":"off","external-controller":"127.0.0.1:18994","secret":"ssssssssssssssssssssssssssssssss","listeners":[{"name":"entry-a","type":"mixed","listen":"127.0.0.1","port":18993,"udp":false,"users":[{"username":"client","password":"pppppppppppppppppppppppppppppppp"}]}],"hosts":{"alpha.test":"127.0.0.1"},"proxies":[{"name":"node:a","type":"http","server":"127.0.0.1","port":18996}],"proxy-groups":[],"rules":["MATCH,node:a"],"profile":{"store-selected":false,"store-fake-ip":false},"hoopa-identity":{"profile_id":"linux_amd64_loopback_connect_v1","source_schema_version":5,"required_fields":["instance","program_name","program_path"]}}`)
	return ControlBundle{CorePath: "/work/core", CoreSHA256: LinuxControlCoreSHA256, Config: raw, ConfigSHA256: linuxControlSHA(raw), RouteMap: []byte(`{"schema_version":1,"config_generation":"","entries":[{"internal_name":"node:a","adapter_type":"Http","node_id":"node:a","display_alias":"node:a"}]}`)}
}
func linuxRuntimeStat(pid int, ticks, state string) []byte {
	return []byte(fmt.Sprintf("%d (core ) with parentheses) %s %s %s", pid, state, strings.Repeat("0 ", 18), ticks))
}
func linuxRuntimeFixture(t *testing.T) *linuxRuntimeFake {
	t.Helper()
	f := &linuxRuntimeFake{files: map[string][]byte{}, links: map[string]string{}, dirs: map[string]bool{}, clock: time.Unix(1, 0)}
	pin := func(p string) LinuxControlAsset { return LinuxControlAsset{Path: p, SHA256: strings.Repeat("a", 64)} }
	a := LinuxControlAssets{Version: 1, Launcher: pin("/work/controlprobe"), Python: pin("/bin/python3"), Bootstrap: pin("/work/sourceidentity/exec_core.py"), BPF: LinuxControlAsset{Path: "/work/online.bpf.o", SHA256: linuxControlBPF}, Libbpf: LinuxControlAsset{Path: "/work/libbpf.so", SHA256: linuxControlLibbpf}}
	for _, name := range []string{"inspect_object.py", "native_loader.py", "observations.py", "online_admission.py"} {
		a.Dependencies = append(a.Dependencies, pin("/work/sourceidentity/"+name))
	}
	nonce := "0123456789abcdef0123456789abcdef"
	l := ControlLaunch{Nonce: nonce, Generation: "gen:control-" + nonce, Directory: "/control/launches/" + nonce, Bundle: linuxRuntimeBundle()}
	l.BundleDigest = controlDigest(l.Bundle)
	instance := ControlInstance{PID: 123, BootID: "12345678-1234-1234-1234-123456789abc", StartTicks: "42", Nonce: l.Nonce, Generation: l.Generation, BundleDigest: l.BundleDigest}
	claim := LinuxControlClaim{Version: 1, LaunchDigest: controlDigest(l), AssetsDigest: controlDigest(a), Instance: instance}
	f.launch = l
	f.claim = claim
	put := func(p string, v any) { f.files[p], _ = json.Marshal(v) }
	put("/control/assets.json", a)
	put(linuxIntentPath(l), l)
	put(filepath.Join(l.Directory, "claim.json"), claim)
	f.files["/proc/sys/kernel/random/boot_id"] = []byte(instance.BootID + "\n")
	f.files["/proc/123/stat"] = linuxRuntimeStat(123, "42", "S")
	home := filepath.Join(l.Directory, "home")
	f.links["/proc/123/exe"] = l.Bundle.CorePath
	f.links["/proc/123/cwd"] = home
	f.files["/proc/123/cmdline"] = []byte(strings.Join([]string{l.Bundle.CorePath, "-d", home, "-f", home + "/config.json"}, "\x00") + "\x00")
	c, m, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		t.Fatal(e)
	}
	spec, e := linuxControlExecSpec(l, a, c, 3)
	if e != nil {
		t.Fatal(e)
	}
	g, _ := linuxControlCaptureGeneration(l)
	handoff, _ := json.Marshal(map[string]any{"schema": "hoopa-core-identity-handoff-v4", "core_generation": l.Generation, "source_path": linuxControlScope(l), "generation": g})
	f.files["/proc/123/environ"] = []byte(strings.Join(append(spec.Environment, "HOOPA_IDENTITY_HANDOFF="+string(handoff)), "\x00") + "\x00")
	f.files[home+"/config.json"] = append([]byte(nil), l.Bundle.Config...)
	m.ConfigGeneration = l.Generation
	f.files[home+"/route-map.json"], _ = json.Marshal(m)
	f.status = linuxControlStatus{SchemaVersion: 1, ConfigGeneration: l.Generation, ConfigSHA256: l.Bundle.ConfigSHA256, State: "running"}
	o := &linuxControlOps{}
	o.executable = func(path string) error {
		if f.hashError == "not-executable" {
			return ErrLinuxControlRuntime
		}
		return nil
	}
	o.guard = func() error { f.events = append(f.events, "guard"); return nil }
	o.readFile = func(p string, n int) ([]byte, error) {
		v, ok := f.files[p]
		if !ok {
			return nil, os.ErrNotExist
		}
		if len(v) > n {
			return nil, ErrLinuxControlRuntime
		}
		return append([]byte(nil), v...), nil
	}
	o.privatePath = func(p string, _ bool) error {
		if p == f.privateError {
			return ErrLinuxControlRuntime
		}
		return nil
	}
	o.hashFile = func(p, _ string, _ int64) error {
		if p == f.hashError {
			return ErrLinuxControlRuntime
		}
		return nil
	}
	o.hashExecutable = func(pid int, _ string) error {
		if pid != 123 || f.hashError == "exe" {
			return ErrLinuxControlRuntime
		}
		return nil
	}
	o.readlink = func(p string) (string, error) {
		v, ok := f.links[p]
		if !ok {
			return "", os.ErrNotExist
		}
		return v, nil
	}
	o.pidfdOpen = func(pid int) (int, error) {
		f.events = append(f.events, "pidfd-open")
		if pid != 123 {
			return -1, ErrLinuxControlRuntime
		}
		if f.openError != nil {
			return -1, f.openError
		}
		return 200, nil
	}
	o.exited = func(fd int) (bool, error) {
		if fd != 200 {
			t.Fatal("wrong pidfd")
		}
		f.exitChecks++
		return f.exited || (f.exitAt > 0 && f.exitChecks >= f.exitAt), nil
	}
	o.closeFD = func(fd int) error { f.closed = append(f.closed, fd); return nil }
	_ = c
	// Assign the typed HTTP seam separately to keep all actual network disabled.
	o.status = func(_ context.Context, _ capabilityprofile.Config) (linuxControlStatus, error) {
		if f.statusHook != nil {
			f.statusHook()
		}
		return f.status, f.statusError
	}
	o.signal = func(fd, sig int) error {
		f.signalFDs = append(f.signalFDs, fd)
		f.signals = append(f.signals, sig)
		return nil
	}
	o.waitExit = func(_ context.Context, fd int, _ time.Duration) (bool, error) {
		if fd != 200 {
			t.Fatal("wrong wait fd")
		}
		return true, nil
	}
	o.mkdir = func(p string) error {
		f.events = append(f.events, "mkdir:"+p)
		if f.dirs[p] {
			return os.ErrExist
		}
		f.dirs[p] = true
		return nil
	}
	o.writeExclusive = func(p string, raw []byte) error {
		f.events = append(f.events, "write:"+p)
		if _, ok := f.files[p]; ok {
			return os.ErrExist
		}
		f.files[p] = append([]byte(nil), raw...)
		return nil
	}
	o.syncDir = func(p string) error { f.events = append(f.events, "sync:"+p); return nil }
	o.spawn = func(_ linuxControlSpawn, done func(LinuxControlExitRecord)) error {
		f.events = append(f.events, "spawn")
		f.files[filepath.Join(l.Directory, "claim.json")], _ = json.Marshal(claim)
		if f.afterSpawn != nil {
			f.afterSpawn()
		}
		done(LinuxControlExitRecord{PID: 123, WaitCompleted: true, ExitCode: 0})
		return nil
	}
	o.exec = func(string, []string, []string) error { f.events = append(f.events, "exec"); return nil }
	o.chdir = func(p string) error { f.events = append(f.events, "chdir:"+p); return nil }
	o.pid = func() int { return 123 }
	o.netnsCookie = func() (uint64, error) { f.events = append(f.events, "netns-cookie"); return 3, nil }
	o.verifyLaunchFiles = func(ControlLaunch) error { return nil }
	o.now = func() time.Time { return f.clock }
	o.sleep = func(d time.Duration) { f.clock = f.clock.Add(d) }
	f.backend = &LinuxControlBackend{assetsFile: "/control/assets.json", assets: a, assetsDigest: controlDigest(a), ops: o, exits: map[string]LinuxControlExitRecord{}}
	return f
}

func TestLinuxControlRuntimeObserveRunningAndStopped(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		f := linuxRuntimeFixture(t)
		v, e := f.backend.Observe(context.Background(), f.launch)
		if e != nil || v.State != ControlVerifiedRunning || v.Instance.PID != 123 || !reflect.DeepEqual(f.closed, []int{200}) {
			t.Fatalf("%+v %v", v, e)
		}
	})
	t.Run("valid-claim-absent-pid", func(t *testing.T) {
		f := linuxRuntimeFixture(t)
		f.openError = syscall.ESRCH
		v, e := f.backend.Observe(context.Background(), f.launch)
		if e != nil || v.State != ControlVerifiedStopped || len(f.closed) != 0 {
			t.Fatalf("%+v %v", v, e)
		}
	})
	t.Run("same-instance-exit-ready", func(t *testing.T) {
		f := linuxRuntimeFixture(t)
		f.exited = true
		f.files["/proc/123/stat"] = linuxRuntimeStat(123, "42", "Z")
		v, e := f.backend.Observe(context.Background(), f.launch)
		if e != nil || v.State != ControlVerifiedStopped {
			t.Fatalf("%+v %v", v, e)
		}
	})
}
func TestLinuxControlRuntimeUnknownEvidenceNeverSignals(t *testing.T) {
	for _, kind := range []string{"missing-claim", "claim-version", "claim-digest", "claim-assets", "claim-nonce", "boot", "pid-reuse", "zombie-without-exit", "exe-path", "exe-hash", "cwd", "argv", "nonce", "generation", "config-env", "trace-env", "duplicate-env", "unknown-env", "handoff", "status-state", "status-schema", "status-hash", "status-identity", "status-revoked", "http-error", "pidfd-error", "exit-after-status", "reuse-after-status", "exec-after-status", "intent-instance", "assets-changed", "private-mode"} {
		t.Run(kind, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			claimPath := filepath.Join(f.launch.Directory, "claim.json")
			switch kind {
			case "missing-claim":
				delete(f.files, claimPath)
			case "claim-version", "claim-digest", "claim-assets", "claim-nonce":
				c := f.claim
				switch kind {
				case "claim-version":
					c.Version = 2
				case "claim-digest":
					c.LaunchDigest = strings.Repeat("0", 64)
				case "claim-assets":
					c.AssetsDigest = strings.Repeat("0", 64)
				case "claim-nonce":
					c.Instance.Nonce = strings.Repeat("0", 32)
				}
				f.files[claimPath], _ = json.Marshal(c)
			case "boot":
				f.files["/proc/sys/kernel/random/boot_id"] = []byte("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
			case "pid-reuse":
				f.files["/proc/123/stat"] = linuxRuntimeStat(123, "43", "S")
			case "zombie-without-exit":
				f.files["/proc/123/stat"] = linuxRuntimeStat(123, "42", "Z")
			case "exe-path":
				f.links["/proc/123/exe"] = "/work/core (deleted)"
			case "exe-hash":
				f.hashError = "exe"
			case "cwd":
				f.links["/proc/123/cwd"] = "/other"
			case "argv":
				f.files["/proc/123/cmdline"] = append(f.files["/proc/123/cmdline"], []byte("--extra\x00")...)
			case "nonce":
				f.files["/proc/123/environ"] = bytes.ReplaceAll(f.files["/proc/123/environ"], []byte("HOOPA_CONTROL_NONCE="+f.launch.Nonce), []byte("HOOPA_CONTROL_NONCE=bad"))
			case "generation":
				f.files["/proc/123/environ"] = bytes.ReplaceAll(f.files["/proc/123/environ"], []byte("HOOPA_GUARD_GENERATION="+f.launch.Generation), []byte("HOOPA_GUARD_GENERATION=bad"))
			case "config-env":
				f.files["/proc/123/environ"] = bytes.ReplaceAll(f.files["/proc/123/environ"], []byte("HOOPA_GUARD_CONFIG_SHA256="+f.launch.Bundle.ConfigSHA256), []byte("HOOPA_GUARD_CONFIG_SHA256=bad"))
			case "trace-env":
				f.files["/proc/123/environ"] = bytes.ReplaceAll(f.files["/proc/123/environ"], []byte("HOOPA_TRACE_PROFILE=5"), []byte("HOOPA_TRACE_PROFILE=4"))
			case "duplicate-env":
				f.files["/proc/123/environ"] = append(f.files["/proc/123/environ"], []byte("HOOPA_TRACE_PROFILE=5\x00")...)
			case "unknown-env":
				f.files["/proc/123/environ"] = append(f.files["/proc/123/environ"], []byte("UNREGISTERED=1\x00")...)
			case "handoff":
				f.files["/proc/123/environ"] = bytes.ReplaceAll(f.files["/proc/123/environ"], []byte("hoopa-core-identity-handoff-v4"), []byte("hoopa-core-identity-handoff-v3"))
			case "status-state":
				f.status.State = "draining"
			case "status-schema":
				f.status.SchemaVersion = 2
			case "status-hash":
				f.status.ConfigSHA256 = strings.Repeat("0", 64)
			case "status-identity":
				f.status.IdentityPath = true
			case "status-revoked":
				f.status.Admission.Revoked = true
			case "http-error":
				f.statusError = errors.New("unavailable")
			case "pidfd-error":
				f.openError = syscall.EPERM
			case "exit-after-status":
				f.exitAt = 2
			case "reuse-after-status":
				f.statusHook = func() { f.files["/proc/123/stat"] = linuxRuntimeStat(123, "43", "S") }
			case "exec-after-status":
				f.statusHook = func() { f.links["/proc/123/exe"] = "/work/another" }
			case "intent-instance":
				l := f.launch
				l.Instance = &f.claim.Instance
				f.files[linuxIntentPath(l)], _ = json.Marshal(l)
			case "assets-changed":
				a := f.backend.assets
				a.Python.SHA256 = strings.Repeat("b", 64)
				f.files["/control/assets.json"], _ = json.Marshal(a)
			case "private-mode":
				f.privateError = claimPath
			}
			v, e := f.backend.Observe(context.Background(), f.launch)
			if e == nil || v.State != ControlUnknown || len(f.signals) != 0 {
				t.Fatalf("unexpected evidence %+v %v", v, e)
			}
			l := f.launch
			l.Instance = &f.claim.Instance
			if e = f.backend.Stop(context.Background(), l); e == nil && kind != "exit-after-status" {
				t.Fatal("unknown was stopped")
			}
			if len(f.signals) != 0 {
				t.Fatal("signal issued for unknown")
			}
		})
	}
}
func TestLinuxControlRuntimeStopUsesVerifiedSamePidfd(t *testing.T) {
	for _, kill := range []bool{false, true} {
		t.Run(strconv.FormatBool(kill), func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			l := f.launch
			l.Instance = &f.claim.Instance
			waits := 0
			f.backend.ops.waitExit = func(_ context.Context, fd int, _ time.Duration) (bool, error) {
				waits++
				if fd != 200 {
					t.Fatal("fd changed")
				}
				return !kill || waits == 2, nil
			}
			if e := f.backend.Stop(context.Background(), l); e != nil {
				t.Fatal(e)
			}
			want := []int{15}
			fds := []int{200}
			if kill {
				want = append(want, 9)
				fds = append(fds, 200)
			}
			if !reflect.DeepEqual(f.signals, want) || !reflect.DeepEqual(f.signalFDs, fds) || !reflect.DeepEqual(f.closed, []int{200}) {
				t.Fatalf("signals%v fds%v close%v", f.signals, f.signalFDs, f.closed)
			}
		})
	}
}
func TestLinuxControlRuntimeStopFailuresNeverSwitchPidfd(t *testing.T) {
	for _, kind := range []string{"missing-instance", "signal", "wait", "kill-timeout", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			l := f.launch
			l.Instance = &f.claim.Instance
			ctx := context.Background()
			switch kind {
			case "missing-instance":
				l.Instance = nil
			case "signal":
				f.backend.ops.signal = func(fd, sig int) error {
					f.signalFDs = append(f.signalFDs, fd)
					f.signals = append(f.signals, sig)
					return syscall.EPERM
				}
			case "wait":
				f.backend.ops.waitExit = func(context.Context, int, time.Duration) (bool, error) { return false, syscall.EIO }
			case "kill-timeout":
				f.backend.ops.waitExit = func(context.Context, int, time.Duration) (bool, error) { return false, nil }
			case "cancel":
				f.backend.ops.waitExit = func(context.Context, int, time.Duration) (bool, error) { return false, context.Canceled }
			}
			if e := f.backend.Stop(ctx, l); e == nil {
				t.Fatal("stop failure accepted")
			}
			for _, fd := range f.signalFDs {
				if fd != 200 {
					t.Fatal("signal changed handle")
				}
			}
			if kind == "wait" || kind == "cancel" {
				if !reflect.DeepEqual(f.signals, []int{15}) {
					t.Fatal("uncertain wait escalated")
				}
			}
		})
	}
}
func TestLinuxControlRuntimeStartIntentAndNoImplicitRetry(t *testing.T) {
	f := linuxRuntimeFixture(t)
	for _, p := range []string{filepath.Join(f.launch.Directory, "claim.json"), filepath.Join(f.launch.Directory, "home/config.json"), filepath.Join(f.launch.Directory, "home/route-map.json")} {
		delete(f.files, p)
	}
	f.afterSpawn = func() {
		if _, ok := f.files[linuxIntentPath(f.launch)]; !ok {
			t.Fatal("spawn without intent")
		}
		if string(f.files[filepath.Join(f.launch.Directory, "home/config.json")]) != string(f.launch.Bundle.Config) {
			t.Fatal("config changed")
		}
	}
	v, e := f.backend.Start(context.Background(), f.launch)
	if e != nil || v.PID != 123 {
		t.Fatalf("%+v %v", v, e)
	}
	if r, ok := f.backend.ExitRecord(f.launch.Nonce); !ok || !r.WaitCompleted || r.PID != 123 {
		t.Fatal("owned Wait record absent")
	}
	before := append([]string(nil), f.events...)
	if _, e = f.backend.Start(context.Background(), f.launch); e == nil {
		t.Fatal("same intent spawned twice")
	}
	if strings.Count(strings.Join(f.events, "\n"), "spawn") != 1 || len(f.events) <= len(before) {
		t.Fatal("unexpected retry")
	}
}
func TestLinuxControlRuntimeStartTimeoutDoesNotKill(t *testing.T) {
	f := linuxRuntimeFixture(t)
	for _, p := range []string{filepath.Join(f.launch.Directory, "claim.json"), filepath.Join(f.launch.Directory, "home/config.json"), filepath.Join(f.launch.Directory, "home/route-map.json")} {
		delete(f.files, p)
	}
	f.backend.ops.spawn = func(linuxControlSpawn, func(LinuxControlExitRecord)) error {
		f.events = append(f.events, "spawn")
		return nil
	}
	_, e := f.backend.Start(context.Background(), f.launch)
	if e == nil || f.clock.Sub(time.Unix(1, 0)) != 10*time.Second || len(f.signals) != 0 {
		t.Fatalf("timeout %v %v", f.clock, e)
	}
	v, e := f.backend.Observe(context.Background(), f.launch)
	if e == nil || v.State != ControlUnknown {
		t.Fatal("missing claim guessed stopped")
	}
}

func TestLinuxControlRuntimeRefusesBeforeMutation(t *testing.T) {
	for _, kind := range []string{"guest", "zero-generation", "relative-directory", "intent-missing", "core-hash", "not-executable"} {
		t.Run(kind, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			l := f.launch
			switch kind {
			case "guest":
				f.backend.ops.guard = func() error { return ErrLinuxControlGuest }
			case "zero-generation":
				l.Nonce = "00000000000000000123456789abcdef"
				l.Generation = "gen:control-" + l.Nonce
				l.Directory = "/control/launches/" + l.Nonce
				f.files[linuxIntentPath(l)], _ = json.Marshal(l)
			case "relative-directory":
				l.Directory = strings.TrimPrefix(l.Directory, "/")
			case "intent-missing":
				delete(f.files, linuxIntentPath(l))
			case "core-hash":
				f.hashError = l.Bundle.CorePath
			case "not-executable":
				f.hashError = "not-executable"
			}
			if _, e := f.backend.Start(context.Background(), l); e == nil {
				t.Fatal("precondition accepted")
			}
			for _, event := range f.events {
				if strings.HasPrefix(event, "mkdir:") || strings.HasPrefix(event, "write:") || event == "spawn" {
					t.Fatal("mutation before refused precondition")
				}
			}
		})
	}
}
func TestLinuxControlRuntimeChangedFilesAndCleanup(t *testing.T) {
	for _, kind := range []string{"config", "route", "close-observe", "close-stop", "poll-error"} {
		t.Run(kind, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			switch kind {
			case "config":
				f.files[filepath.Join(f.launch.Directory, "home/config.json")] = []byte("changed")
			case "route":
				f.files[filepath.Join(f.launch.Directory, "home/route-map.json")] = []byte("{}")
			case "close-observe", "close-stop":
				f.backend.ops.closeFD = func(fd int) error { f.closed = append(f.closed, fd); return syscall.EIO }
			case "poll-error":
				f.backend.ops.exited = func(int) (bool, error) { return false, syscall.EBADF }
			}
			if kind == "close-stop" {
				l := f.launch
				l.Instance = &f.claim.Instance
				if e := f.backend.Stop(context.Background(), l); e == nil {
					t.Fatal("close error discarded")
				}
			} else {
				v, e := f.backend.Observe(context.Background(), f.launch)
				if e == nil || v.State != ControlUnknown {
					t.Fatalf("%+v %v", v, e)
				}
			}
			if len(f.closed) > 1 {
				t.Fatal("pidfd double closed")
			}
		})
	}
}
func TestLinuxControlRuntimeSpawnFailureMissingClaimUnknown(t *testing.T) {
	f := linuxRuntimeFixture(t)
	for _, p := range []string{filepath.Join(f.launch.Directory, "claim.json"), filepath.Join(f.launch.Directory, "home/config.json"), filepath.Join(f.launch.Directory, "home/route-map.json")} {
		delete(f.files, p)
	}
	f.backend.ops.spawn = func(linuxControlSpawn, func(LinuxControlExitRecord)) error {
		return errors.New("spawn failed before known claim")
	}
	if _, e := f.backend.Start(context.Background(), f.launch); e == nil {
		t.Fatal("spawn failure accepted")
	}
	v, e := f.backend.Observe(context.Background(), f.launch)
	if e == nil || v.State != ControlUnknown || len(f.signals) != 0 {
		t.Fatalf("missing claim guessed absent %+v %v", v, e)
	}
}
