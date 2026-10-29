//go:build linux && amd64

package mihomoevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/experiments/mihomo-events/capabilityprofile"
	"hoopa/protocol/identityprofile"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const LinuxControlCoreSHA256 = "616133dd060b14cdbde68c6d5029f003429d7a8b6adeee4452e8176c63ca80e6"
const linuxControlBPF = "df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49"
const linuxControlLibbpf = "1c928f61158f25d39cd3fbc7ddafeebec2448886225e2c1824455fcfcd15d04c"
const linuxControlBTF = "1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9"
const linuxControlKernel = "6.18.54.1-microsoft-standard-WSL2"

var ErrLinuxControlRuntime = errors.New("control runtime evidence unavailable or conflicting")
var ErrLinuxControlGuest = errors.New("dedicated t132/t136 initial-namespace root guest required")

type LinuxControlAsset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type LinuxControlAssets struct {
	Version      int                 `json:"version"`
	Launcher     LinuxControlAsset   `json:"launcher"`
	Python       LinuxControlAsset   `json:"python"`
	Bootstrap    LinuxControlAsset   `json:"bootstrap"`
	BPF          LinuxControlAsset   `json:"bpf"`
	Libbpf       LinuxControlAsset   `json:"libbpf"`
	Dependencies []LinuxControlAsset `json:"dependencies"`
}
type LinuxControlClaim struct {
	Version      int             `json:"version"`
	LaunchDigest string          `json:"launch_digest"`
	AssetsDigest string          `json:"assets_digest"`
	Instance     ControlInstance `json:"instance"`
}
type LinuxControlExitRecord struct {
	PID           int  `json:"pid"`
	WaitCompleted bool `json:"wait_completed"`
	ExitCode      int  `json:"exit_code"`
}

type LinuxControlBackend struct {
	assetsFile   string
	assets       LinuxControlAssets
	assetsDigest string
	ops          *linuxControlOps
	exitsMu      sync.Mutex
	exits        map[string]LinuxControlExitRecord
}

// Construction and Validate only read bounded inputs. Native mutations are gated
// separately at Start and at the launch helper's first instruction.
func NewLinuxControlBackend(assetsFile string) (*LinuxControlBackend, error) {
	ops := newLinuxControlOps()
	a, e := linuxLoadAssets(assetsFile, ops)
	if e != nil {
		return nil, e
	}
	return &LinuxControlBackend{assetsFile: assetsFile, assets: a, assetsDigest: controlDigest(a), ops: ops, exits: map[string]LinuxControlExitRecord{}}, nil
}
func (b *LinuxControlBackend) ExitRecord(nonce string) (LinuxControlExitRecord, bool) {
	b.exitsMu.Lock()
	defer b.exitsMu.Unlock()
	v, ok := b.exits[nonce]
	return v, ok
}
func linuxAssetList(a LinuxControlAssets) []LinuxControlAsset {
	v := []LinuxControlAsset{a.Launcher, a.Python, a.Bootstrap, a.BPF, a.Libbpf}
	return append(v, a.Dependencies...)
}
func linuxValidateAssets(a LinuxControlAssets) error {
	if a.Version != 1 || a.BPF.SHA256 != linuxControlBPF || a.Libbpf.SHA256 != linuxControlLibbpf || filepath.Base(a.Bootstrap.Path) != "exec_core.py" || len(a.Dependencies) != 4 {
		return ErrLinuxControlRuntime
	}
	seen := map[string]bool{}
	for _, p := range linuxAssetList(a) {
		if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path || len(p.Path) > 4096 || strings.ContainsRune(p.Path, 0) || !controlHex.MatchString(p.SHA256) || seen[p.Path] {
			return ErrLinuxControlRuntime
		}
		seen[p.Path] = true
	}
	names := []string{"inspect_object.py", "native_loader.py", "observations.py", "online_admission.py"}
	for i, p := range a.Dependencies {
		if p.Path != filepath.Join(filepath.Dir(a.Bootstrap.Path), names[i]) {
			return ErrLinuxControlRuntime
		}
	}
	return nil
}
func linuxLoadAssets(path string, ops *linuxControlOps) (LinuxControlAssets, error) {
	var a LinuxControlAssets
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 {
		return a, ErrLinuxControlRuntime
	}
	if e := ops.privatePath(filepath.Dir(path), true); e != nil {
		return a, ErrLinuxControlRuntime
	}
	if e := ops.privatePath(path, false); e != nil {
		return a, ErrLinuxControlRuntime
	}
	raw, e := ops.readFile(path, 64<<10)
	if e != nil {
		return a, ErrLinuxControlRuntime
	}
	if e = controlDecode(raw, &a); e != nil {
		return a, ErrLinuxControlRuntime
	}
	if e = linuxValidateAssets(a); e != nil {
		return a, e
	}
	for _, p := range linuxAssetList(a) {
		if e = ops.hashFile(p.Path, p.SHA256, 128<<20); e != nil {
			return a, ErrLinuxControlRuntime
		}
	}
	for _, p := range []string{a.Launcher.Path, a.Python.Path} {
		if e = ops.executable(p); e != nil {
			return a, ErrLinuxControlRuntime
		}
	}
	return a, nil
}
func (b *LinuxControlBackend) verifyAssets() error {
	a, e := linuxLoadAssets(b.assetsFile, b.ops)
	if e != nil || controlDigest(a) != b.assetsDigest {
		return ErrLinuxControlRuntime
	}
	return nil
}

type linuxControlRouteEntry struct {
	InternalName string  `json:"internal_name"`
	AdapterType  string  `json:"adapter_type"`
	NodeID       *string `json:"node_id,omitempty"`
	GroupID      *string `json:"group_id,omitempty"`
	DisplayAlias string  `json:"display_alias"`
}
type linuxControlRouteMap struct {
	SchemaVersion    int                      `json:"schema_version"`
	ConfigGeneration string                   `json:"config_generation"`
	Entries          []linuxControlRouteEntry `json:"entries"`
}

func linuxControlConfig(bundle ControlBundle, generation string) (capabilityprofile.Config, linuxControlRouteMap, error) {
	var m linuxControlRouteMap
	var zero capabilityprofile.Config
	if controlValidateBundle(bundle) != nil || bundle.CoreSHA256 != LinuxControlCoreSHA256 {
		return zero, m, ErrLinuxControlRuntime
	}
	c, r, e := capabilityprofile.ValidateDeclaredIdentity(bundle.Config, generation)
	if e != nil || len(c.Listeners) != 1 || r.ProfileID != identityprofile.ID || r.SourceSchemaVersion != 5 {
		return zero, m, ErrLinuxControlRuntime
	}
	fields := map[string]bool{}
	for _, f := range r.RequiredFields {
		fields[f] = true
	}
	if len(fields) != 3 || !fields["instance"] || !fields["program_name"] || !fields["program_path"] {
		return zero, m, ErrLinuxControlRuntime
	}
	if e = controlDecode(bundle.RouteMap, &m); e != nil || m.SchemaVersion != 1 || m.ConfigGeneration != "" || len(m.Entries) != len(c.Proxies)+len(c.Groups) {
		return zero, m, ErrLinuxControlRuntime
	}
	expected := map[string]string{}
	for _, n := range c.Proxies {
		expected[n.Name] = map[string]string{"http": "Http", "socks5": "Socks5", "direct": "Direct"}[n.Type]
	}
	for _, g := range c.Groups {
		expected[g.Name] = "Selector"
	}
	seen := map[string]bool{}
	for _, e := range m.Entries {
		t, ok := expected[e.InternalName]
		if !ok || seen[e.InternalName] || t != e.AdapterType || !qualityAlias(e.DisplayAlias) {
			return zero, m, ErrLinuxControlRuntime
		}
		seen[e.InternalName] = true
		if strings.HasPrefix(e.InternalName, "node:") {
			if e.NodeID == nil || *e.NodeID != e.InternalName || e.GroupID != nil {
				return zero, m, ErrLinuxControlRuntime
			}
		} else if e.GroupID == nil || *e.GroupID != e.InternalName || e.NodeID != nil {
			return zero, m, ErrLinuxControlRuntime
		}
	}
	return c, m, nil
}
func (b *LinuxControlBackend) Validate(bundle ControlBundle) error {
	if _, _, e := linuxControlConfig(bundle, "gen:control-0123456789abcdef0123456789abcdef"); e != nil {
		return e
	}
	if e := b.verifyAssets(); e != nil {
		return e
	}
	if e := b.ops.executable(bundle.CorePath); e != nil {
		return ErrLinuxControlRuntime
	}
	return b.ops.hashFile(bundle.CorePath, LinuxControlCoreSHA256, 128<<20)
}

func linuxIntentPath(l ControlLaunch) string {
	return filepath.Join(filepath.Dir(l.Directory), l.Nonce+".json")
}
func linuxOriginalLaunch(l ControlLaunch) ControlLaunch {
	l = controlClone(l)
	l.Instance = nil
	return l
}
func (b *LinuxControlBackend) intent(l ControlLaunch) error {
	if !filepath.IsAbs(l.Directory) || filepath.Clean(l.Directory) != l.Directory {
		return ErrLinuxControlRuntime
	}
	if controlValidateLaunch(l, filepath.Dir(filepath.Dir(l.Directory))) != nil {
		return ErrLinuxControlRuntime
	}
	if _, e := linuxControlCaptureGeneration(l); e != nil {
		return e
	}
	for _, p := range []string{filepath.Dir(l.Directory), l.Directory} {
		if e := b.ops.privatePath(p, true); e != nil {
			return ErrLinuxControlRuntime
		}
	}
	path := linuxIntentPath(l)
	if e := b.ops.privatePath(path, false); e != nil {
		return ErrLinuxControlRuntime
	}
	raw, e := b.ops.readFile(path, 256<<10)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	var original ControlLaunch
	if controlDecode(raw, &original) != nil || original.Instance != nil || controlDigest(original) != controlDigest(linuxOriginalLaunch(l)) {
		return ErrLinuxControlRuntime
	}
	return nil
}
func (b *LinuxControlBackend) Start(ctx context.Context, l ControlLaunch) (ControlInstance, error) {
	if e := b.ops.guard(); e != nil {
		return ControlInstance{}, e
	}
	if e := b.intent(l); e != nil {
		return ControlInstance{}, e
	}
	if l.Instance != nil {
		return ControlInstance{}, ErrLinuxControlRuntime
	}
	if e := b.Validate(l.Bundle); e != nil {
		return ControlInstance{}, e
	}
	_, m, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		return ControlInstance{}, e
	}
	m.ConfigGeneration = l.Generation
	route, e := json.Marshal(m)
	if e != nil {
		return ControlInstance{}, ErrLinuxControlRuntime
	}
	for _, name := range []string{"home", "trace"} {
		if e = b.ops.mkdir(filepath.Join(l.Directory, name)); e != nil {
			return ControlInstance{}, ErrLinuxControlRuntime
		}
	}
	for name, raw := range map[string][]byte{"home/config.json": l.Bundle.Config, "home/route-map.json": route} {
		if e = b.ops.writeExclusive(filepath.Join(l.Directory, name), raw); e != nil {
			return ControlInstance{}, ErrLinuxControlRuntime
		}
	}
	if e = b.ops.syncDir(l.Directory); e != nil {
		return ControlInstance{}, ErrLinuxControlRuntime
	}
	spec := linuxControlSpawn{Path: b.assets.Launcher.Path, Args: []string{b.assets.Launcher.Path, "launch", "--intent", linuxIntentPath(l), "--assets", b.assetsFile}, Directory: l.Directory, Stdout: filepath.Join(l.Directory, "launcher.stdout"), Stderr: filepath.Join(l.Directory, "launcher.stderr"), Environment: []string{"PATH=/usr/bin:/bin", "LANG=C", "PYTHONDONTWRITEBYTECODE=1"}}
	if e = b.ops.spawn(spec, func(record LinuxControlExitRecord) { b.exitsMu.Lock(); b.exits[l.Nonce] = record; b.exitsMu.Unlock() }); e != nil {
		return ControlInstance{}, ErrLinuxControlRuntime
	}
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deadline := b.ops.now().Add(10 * time.Second)
	for {
		v, err := b.Observe(waitCtx, l)
		if err == nil && v.State == ControlVerifiedRunning {
			return *v.Instance, nil
		}
		if err == nil && v.State == ControlVerifiedStopped {
			return ControlInstance{}, ErrLinuxControlRuntime
		}
		if waitCtx.Err() != nil || !b.ops.now().Before(deadline) {
			return ControlInstance{}, ErrLinuxControlRuntime
		}
		b.ops.sleep(25 * time.Millisecond)
	}
}

func linuxReadClaim(l ControlLaunch, assetsDigest string, ops *linuxControlOps) (LinuxControlClaim, error) {
	var c LinuxControlClaim
	path := filepath.Join(l.Directory, "claim.json")
	if e := ops.privatePath(path, false); e != nil {
		return c, ErrLinuxControlRuntime
	}
	raw, e := ops.readFile(path, 16<<10)
	if e != nil {
		return c, ErrLinuxControlRuntime
	}
	if controlDecode(raw, &c) != nil || c.Version != 1 || c.LaunchDigest != controlDigest(linuxOriginalLaunch(l)) || c.AssetsDigest != assetsDigest || !controlMatches(l, &c.Instance) {
		return c, ErrLinuxControlRuntime
	}
	return c, nil
}
func (b *LinuxControlBackend) Observe(ctx context.Context, l ControlLaunch) (ControlObservation, error) {
	v, fd, e := b.verify(ctx, l)
	if fd >= 0 {
		if closeErr := b.ops.closeFD(fd); closeErr != nil {
			return ControlObservation{State: ControlUnknown}, ErrLinuxControlRuntime
		}
	}
	return v, e
}
func (b *LinuxControlBackend) Stop(ctx context.Context, l ControlLaunch) (result error) {
	if e := b.ops.guard(); e != nil {
		return e
	}
	if l.Instance == nil {
		return ErrLinuxControlRuntime
	}
	v, fd, e := b.verify(ctx, l)
	if fd >= 0 {
		defer func() {
			if b.ops.closeFD(fd) != nil {
				result = ErrLinuxControlRuntime
			}
		}()
	}
	if e != nil {
		return e
	}
	if v.State == ControlVerifiedStopped {
		return nil
	}
	if v.State != ControlVerifiedRunning || fd < 0 {
		return ErrLinuxControlRuntime
	}
	if e = b.ops.signal(fd, 15); e != nil {
		return ErrLinuxControlRuntime
	}
	ended, e := b.ops.waitExit(ctx, fd, 3*time.Second)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	if ended {
		return nil
	}
	if e = b.ops.signal(fd, 9); e != nil {
		return ErrLinuxControlRuntime
	}
	ended, e = b.ops.waitExit(ctx, fd, 3*time.Second)
	if e != nil || !ended {
		return ErrLinuxControlRuntime
	}
	return nil
}

// RunLinuxControlLauncher never returns on success: same PID execs the pinned
// bootstrap, which itself execs the fixed core. Claim is durable before exec.
func RunLinuxControlLauncher(intentPath, assetsFile string) error {
	return runLinuxControlLauncher(intentPath, assetsFile, newLinuxControlOps())
}
func runLinuxControlLauncher(intentPath, assetsFile string, ops *linuxControlOps) error {
	if e := ops.guard(); e != nil {
		return e
	}
	a, e := linuxLoadAssets(assetsFile, ops)
	if e != nil {
		return e
	}
	b := &LinuxControlBackend{assetsFile: assetsFile, assets: a, assetsDigest: controlDigest(a), ops: ops}
	if !filepath.IsAbs(intentPath) || filepath.Clean(intentPath) != intentPath {
		return ErrLinuxControlRuntime
	}
	if e = ops.privatePath(intentPath, false); e != nil {
		return ErrLinuxControlRuntime
	}
	raw, e := ops.readFile(intentPath, 256<<10)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	var l ControlLaunch
	if controlDecode(raw, &l) != nil || l.Instance != nil || linuxIntentPath(l) != intentPath || b.intent(l) != nil {
		return ErrLinuxControlRuntime
	}
	if e = b.Validate(l.Bundle); e != nil {
		return e
	}
	c, _, e := linuxControlConfig(l.Bundle, l.Generation)
	if e != nil {
		return e
	}
	if e = ops.verifyLaunchFiles(l); e != nil {
		return e
	}
	boot, e := ops.readFile("/proc/sys/kernel/random/boot_id", 64)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	pid := ops.pid()
	stat, e := ops.readFile(fmt.Sprintf("/proc/%d/stat", pid), 4096)
	if e != nil {
		return ErrLinuxControlRuntime
	}
	p, e := linuxParseStat(stat)
	if e != nil || p.PID != pid {
		return ErrLinuxControlRuntime
	}
	instance := ControlInstance{PID: pid, BootID: strings.TrimSpace(string(boot)), StartTicks: p.StartTicks, Nonce: l.Nonce, Generation: l.Generation, BundleDigest: l.BundleDigest}
	if !controlMatches(l, &instance) {
		return ErrLinuxControlRuntime
	}
	claim := LinuxControlClaim{Version: 1, LaunchDigest: controlDigest(linuxOriginalLaunch(l)), AssetsDigest: b.assetsDigest, Instance: instance}
	data, _ := json.Marshal(claim)
	if e = ops.writeExclusive(filepath.Join(l.Directory, "claim.json"), data); e != nil {
		return ErrLinuxControlRuntime
	}
	if e = ops.mkdir(linuxControlScope(l)); e != nil {
		return ErrLinuxControlRuntime
	}
	cookie, e := ops.netnsCookie()
	if e != nil || cookie == 0 {
		return ErrLinuxControlRuntime
	}
	spec, e := linuxControlExecSpec(l, a, c, cookie)
	if e != nil {
		return e
	}
	if e = ops.chdir(filepath.Join(l.Directory, "home")); e != nil {
		return ErrLinuxControlRuntime
	}
	return ops.exec(spec.Path, spec.Args, spec.Environment)
}
func linuxControlScope(l ControlLaunch) string {
	return "/sys/fs/cgroup/hoopa-online-control-" + l.Nonce
}
func linuxControlSHA(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
