//go:build linux && amd64

package mihomoevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxControlProcessStatBoundaries(t *testing.T) {
	for _, ticks := range []string{"1", "42", "18446744073709551615"} {
		p, e := linuxParseStat(linuxRuntimeStat(123, ticks, "S"))
		if e != nil || p.PID != 123 || p.StartTicks != ticks {
			t.Fatalf("stat %+v %v", p, e)
		}
	}
	for _, raw := range [][]byte{nil, []byte("123 (short) S 0"), linuxRuntimeStat(0, "42", "S"), linuxRuntimeStat(123, "0", "S"), linuxRuntimeStat(123, "01", "S"), linuxRuntimeStat(123, "18446744073709551616", "S"), linuxRuntimeStat(123, "-1", "S")} {
		if _, e := linuxParseStat(raw); e == nil {
			t.Fatal("malformed stat accepted")
		}
	}
}
func TestLinuxControlProcessAssetsStrictAndPinned(t *testing.T) {
	for _, kind := range []string{"valid", "unknown", "duplicate", "trailing", "version", "hash", "path", "dependency", "order", "asset-private", "asset-hash"} {
		t.Run(kind, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			a := controlClone(f.backend.assets)
			raw := append([]byte(nil), f.files["/control/assets.json"]...)
			switch kind {
			case "unknown":
				raw = append(raw[:len(raw)-1], []byte(`,"unknown":0}`)...)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"version":1}`)...)
			case "trailing":
				raw = append(raw, []byte(`{}`)...)
			case "version":
				a.Version = 2
			case "hash":
				a.BPF.SHA256 = strings.Repeat("0", 64)
			case "path":
				a.Python.Path = "relative"
			case "dependency":
				a.Dependencies[0].Path = "/work/sourceidentity/test.py"
			case "order":
				a.Dependencies[0], a.Dependencies[1] = a.Dependencies[1], a.Dependencies[0]
			case "asset-private":
				f.privateError = "/control/assets.json"
			case "asset-hash":
				f.hashError = a.Python.Path
			}
			if kind != "unknown" && kind != "duplicate" && kind != "trailing" {
				raw, _ = json.Marshal(a)
			}
			f.files["/control/assets.json"] = raw
			_, e := linuxLoadAssets("/control/assets.json", f.backend.ops)
			if (e == nil) != (kind == "valid") {
				t.Fatalf("validation %v", e)
			}
		})
	}
}
func TestLinuxControlProcessProfileAndRouteExact(t *testing.T) {
	for _, kind := range []string{"valid", "config-hash", "old-core", "process-rule", "no-identity", "source4", "multiple-listeners", "route-generation", "route-node", "route-type", "route-missing", "route-extra", "route-duplicate", "route-credential", "template-original"} {
		t.Run(kind, func(t *testing.T) {
			b := linuxRuntimeBundle()
			var config map[string]any
			json.Unmarshal(b.Config, &config)
			var route map[string]any
			json.Unmarshal(b.RouteMap, &route)
			switch kind {
			case "config-hash":
				b.ConfigSHA256 = strings.Repeat("0", 64)
			case "old-core":
				b.CoreSHA256 = strings.Repeat("0", 64)
			case "process-rule":
				config["rules"] = []string{"PROCESS-NAME,python3,node:a", "MATCH,node:a"}
			case "no-identity":
				delete(config, "hoopa-identity")
			case "source4":
				config["hoopa-identity"].(map[string]any)["source_schema_version"] = 4
			case "multiple-listeners":
				config["listeners"] = append(config["listeners"].([]any), config["listeners"].([]any)[0])
			case "route-generation":
				route["config_generation"] = "gen:old"
			case "route-node":
				route["entries"].([]any)[0].(map[string]any)["node_id"] = "node:other"
			case "route-type":
				route["entries"].([]any)[0].(map[string]any)["adapter_type"] = "Socks5"
			case "route-missing":
				route["entries"] = []any{}
			case "route-extra":
				route["extra"] = 1
			case "route-duplicate":
				b.RouteMap = []byte(`{"schema_version":1,"schema_version":1,"config_generation":"","entries":[]}`)
			case "route-credential":
				route["entries"].([]any)[0].(map[string]any)["password"] = "not-allowed"
			case "template-original":
				b.RouteMap = append([]byte(" \n"), b.RouteMap...)
			}
			if kind == "process-rule" || kind == "no-identity" || kind == "source4" || kind == "multiple-listeners" {
				b.Config, _ = json.Marshal(config)
				b.ConfigSHA256 = linuxControlSHA(b.Config)
			}
			if strings.HasPrefix(kind, "route-") && kind != "route-duplicate" {
				b.RouteMap, _ = json.Marshal(route)
			}
			before := controlClone(b)
			_, m, e := linuxControlConfig(b, "gen:control-0123456789abcdef0123456789abcdef")
			ok := kind == "valid" || kind == "template-original"
			if (e == nil) != ok {
				t.Fatalf("profile error %v", e)
			}
			if ok {
				m.ConfigGeneration = "gen:new"
				if !reflect.DeepEqual(b, before) {
					t.Fatal("original bytes changed")
				}
			}
		})
	}
}
func TestLinuxControlProcessStatusAndEnvironmentStrict(t *testing.T) {
	f := linuxRuntimeFixture(t)
	raw, _ := json.Marshal(f.status)
	var s linuxControlStatus
	if e := controlDecode(raw, &s); e != nil || !linuxControlStatusMatches(s, f.launch) {
		t.Fatal(e)
	}
	for _, raw := range []string{`{}`, `null`, `{"schema_version":1,"schema_version":1}`, string(raw) + `{}`, strings.TrimSuffix(string(raw), "}") + `,"extra":0}`} {
		if e := controlDecode([]byte(raw), &s); e == nil {
			t.Fatal("status malformed accepted")
		}
	}
	for _, raw := range [][]byte{nil, []byte("A=1"), []byte("A=1\x00A=2\x00"), []byte("=value\x00"), []byte("missing_equals\x00")} {
		if _, e := linuxControlEnvironment(raw); e == nil {
			t.Fatal("bad environment accepted")
		}
	}
}
func TestLinuxControlProcessGenerationAndExecSpec(t *testing.T) {
	f := linuxRuntimeFixture(t)
	l := f.launch
	c, _, _ := linuxControlConfig(l.Bundle, l.Generation)
	spec, e := linuxControlExecSpec(l, f.backend.assets, c, 3)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(spec.Args[:4], []string{"/bin/python3", "-S", "-B", "/work/sourceidentity/exec_core.py"}) || !strings.Contains(strings.Join(spec.Args, " "), "--profile scoped-executable-online --identity-profile linux_amd64_loopback_connect_v1") || !strings.Contains(strings.Join(spec.Args, " "), "--generation 17279655951921914625") {
		t.Fatalf("exec spec generation/path mismatch")
	}
	if bytes.Contains([]byte(strings.Join(spec.Environment, "\n")), []byte("secret")) {
		t.Fatal("config credential copied to environment")
	}
	l.Nonce = "00000000000000000123456789abcdef"
	l.Generation = "gen:control-" + l.Nonce
	if _, e := linuxControlCaptureGeneration(l); e == nil {
		t.Fatal("zero generation replaced silently")
	}
	c.Listeners = nil
	if _, e := linuxControlExecSpec(f.launch, f.backend.assets, c, 3); e == nil {
		t.Fatal("invalid config indexed")
	}
}
func TestLinuxControlProcessLauncherClaimBeforeScopeExec(t *testing.T) {
	f := linuxRuntimeFixture(t)
	delete(f.files, filepath.Join(f.launch.Directory, "claim.json"))
	f.files["/proc/123/stat"] = linuxRuntimeStat(123, "42", "S")
	if e := runLinuxControlLauncher(linuxIntentPath(f.launch), f.backend.assetsFile, f.backend.ops); e != nil {
		t.Fatal(e)
	}
	events := strings.Join(f.events, "\n")
	claim := "write:" + filepath.Join(f.launch.Directory, "claim.json")
	scope := "mkdir:" + linuxControlScope(f.launch)
	if strings.Index(events, claim) < 0 || strings.Index(events, scope) < strings.Index(events, claim) || strings.Index(events, "exec") < strings.Index(events, scope) {
		t.Fatal("claim not before native actions")
	}
	var c LinuxControlClaim
	if e := controlDecode(f.files[filepath.Join(f.launch.Directory, "claim.json")], &c); e != nil || c.AssetsDigest != f.backend.assetsDigest || c.LaunchDigest != controlDigest(f.launch) || c.Instance.PID != 123 {
		t.Fatal("claim mismatch")
	}
}
func TestLinuxControlProcessLauncherFailureNeverExecsOrRewritesClaim(t *testing.T) {
	for _, stage := range []string{"guard", "intent", "assets", "claim-exists", "claim-write", "scope", "cookie", "chdir"} {
		t.Run(stage, func(t *testing.T) {
			f := linuxRuntimeFixture(t)
			claimPath := filepath.Join(f.launch.Directory, "claim.json")
			original := append([]byte(nil), f.files[claimPath]...)
			if stage != "claim-exists" {
				delete(f.files, claimPath)
			}
			switch stage {
			case "guard":
				f.backend.ops.guard = func() error { return ErrLinuxControlGuest }
			case "intent":
				delete(f.files, linuxIntentPath(f.launch))
			case "assets":
				f.hashError = f.backend.assets.Python.Path
			case "claim-write":
				f.backend.ops.writeExclusive = func(string, []byte) error { return errors.New("uncertain claim sync") }
			case "scope":
				f.backend.ops.mkdir = func(string) error { return errors.New("scope unavailable") }
			case "cookie":
				f.backend.ops.netnsCookie = func() (uint64, error) { return 0, errors.New("cookie unavailable") }
			case "chdir":
				f.backend.ops.chdir = func(string) error { return errors.New("cwd unavailable") }
			}
			if e := runLinuxControlLauncher(linuxIntentPath(f.launch), f.backend.assetsFile, f.backend.ops); e == nil {
				t.Fatal("failure accepted")
			}
			for _, e := range f.events {
				if e == "exec" {
					t.Fatal("exec after failed evidence")
				}
			}
			if stage == "claim-exists" && !bytes.Equal(original, f.files[claimPath]) {
				t.Fatal("old claim overwritten")
			}
		})
	}
}
func TestLinuxControlProcessHostEntryRefusal(t *testing.T) {
	raw, e := linuxControlReadFile("/proc/cmdline", 8192)
	if e != nil {
		t.Fatal(e)
	}
	if linuxControlGuestMarkers(raw) == nil {
		t.Fatal("safe host test is only registered outside the owned control guest")
	}
	b := &LinuxControlBackend{ops: newLinuxControlOps()}
	if _, e = b.Start(context.Background(), ControlLaunch{Directory: "/never-create-control-runtime-host"}); !errors.Is(e, ErrLinuxControlGuest) {
		t.Fatal(e)
	}
	if e = RunLinuxControlLauncher("/never-create-control-runtime-host/intent.json", "/never-create-control-runtime-host/assets.json"); !errors.Is(e, ErrLinuxControlGuest) {
		t.Fatal(e)
	}
	if _, e = os.Lstat("/never-create-control-runtime-host"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("host directory exists")
	}
}
func TestLinuxControlProcessGuestMarkersExact(t *testing.T) {
	for _, raw := range []string{"hoopa_lab=t132 hoopa_control_lab=t136", "console=ttyS0 hoopa_control_lab=t136 hoopa_lab=t132"} {
		if linuxControlGuestMarkers([]byte(raw)) != nil {
			t.Fatal("valid guest marker rejected")
		}
	}
	for _, raw := range []string{"", "hoopa_lab=t132", "hoopa_lab=t132 hoopa_control_lab=t136-other", "hoopa_lab=t132 hoopa_control_lab=t136 hoopa_lab=t132", "hoopa_lab=t132 hoopa_control_lab=t136 hoopa_lab=other"} {
		if linuxControlGuestMarkers([]byte(raw)) == nil {
			t.Fatal("invalid marker accepted")
		}
	}
}
