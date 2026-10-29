//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

const sourceStaticSHA = "c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6"

type sourceMaterialFixture struct {
	plan     Plan
	material BasicMaterial
	compiled policy.Compilation
	root     string
}

func sourceMaterialBuild(t *testing.T, binary string) sourceMaterialFixture {
	t.Helper()
	cap, e := mihomo.ApprovedSourcesCapability(binary)
	if e != nil {
		t.Fatal("sources approval must be restored before executing this suite", e)
	}
	if cap.Protocol != 2 || cap.Binary.SHA256 != sourceStaticSHA {
		t.Fatal("unexpected sources capability; dynamic build must not be admitted")
	}
	raw := planRaw(t, map[string]any{"proxies": []map[string]any{{"name": "US ordinary", "type": "http", "server": "edge.example.test", "port": 443, "username": "provider-user", "password": "provider-private-password"}}})
	parsed, e := subscription.Parse(raw, subscription.ParseOptions{SourceID: "source-a", IdentityKey: bytes.Repeat([]byte{0x71}, 32), RegionRules: []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "unmatched-", Region: "ZZ"}}})
	if e != nil {
		t.Fatal(e)
	}
	intent := planRaw(t, map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": cap.Binary}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": "/synthetic/provider.yaml"}, "update_hours": nil}}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": []any{map[string]any{"id": "US", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://probe.example.test/", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}}, map[string]any{"id": "follow", "required": true, "strategy": "follow", "follows": "US"}}, "apps": []any{}, "rules": []any{map[string]any{"id": "domain-rule", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "example.test"}, "target": map[string]any{"kind": "group", "group_id": "follow"}}}, "default_target": map[string]any{"kind": "reject"}})
	compiled, e := policy.Compile(policy.CompileInput{Intent: intent, Sources: map[config.ID]subscription.Snapshot{"source-a": parsed}, Generation: "generation:admission", Capability: cap, Runtime: policy.RuntimeInput{Controller: "127.0.0.1:32000", Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "entry-shared", Type: "mixed", Listen: "127.0.0.1", Port: 32001, Users: []profile.User{{Username: "user-shared", Password: strings.Repeat("p", 32)}}}}})
	if e != nil {
		t.Fatal("real compiler fixture", e)
	}
	manifest, files := compiled.Bundle()
	capSHA, _ := cap.Digest()
	envelope := map[string]any{"schema_version": 1, "base_revision": "revision:admission", "compiler_version": policy.CompilerVersion, "capabilities_digest": capSHA, "intent": json.RawMessage(intent), "inputs": sourceCoreInputs(compiled.Inputs()), "impact": PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(manifest.Generation), ConnectionEffect: "interrupt", RuntimeAction: "start"}, "blockers": []PlanBlocker{}}
	p, e := BuildPlan(planRaw(t, envelope), sourceCoreValidator(compiled))
	if e != nil {
		t.Fatal("real compiler Plan binding", e)
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if os.Mkdir(work, 0700) != nil {
		t.Fatal("workdir")
	}
	if e := os.WriteFile(filepath.Join(work, "config.json"), files["core.json"], 0600); e != nil {
		t.Fatal(e)
	}
	return sourceMaterialFixture{plan: p, material: BasicMaterial{Manifest: manifest, Files: files, Workdir: work}, compiled: compiled, root: root}
}
func sourceMaterialFile(m BasicMaterial, kind string) string {
	for _, f := range m.Manifest.Files {
		if f.Kind == kind {
			return f.Name
		}
	}
	panic("fixture asset kind")
}
func sourceMaterialSnapshot(t *testing.T, m BasicMaterial) wire.Snapshot {
	t.Helper()
	s, e := wire.DecodeSnapshot(m.Files[mihomo.SnapshotFile])
	if e != nil {
		t.Fatal("fixture snapshot", e)
	}
	return s
}
func sourceMaterialProxy(s *wire.Snapshot, kind string) *wire.Proxy {
	for i := range s.Proxies {
		if s.Proxies[i].Type == kind && strings.HasPrefix(s.Proxies[i].Name, "group:") {
			return &s.Proxies[i]
		}
	}
	panic("fixture group")
}
func sourceMaterialDrop(m *BasicMaterial, name string) {
	delete(m.Files, name)
	kept := []config.BundleFile{}
	for _, file := range m.Manifest.Files {
		if file.Name != name {
			kept = append(kept, file)
		}
	}
	m.Manifest.Files = kept
}
func sourceMaterialFail(t *testing.T, f sourceMaterialFixture, p Plan, m BasicMaterial, want error) {
	t.Helper()
	before := snapshot(t, f.root)
	checked, e := checkBasicMaterial(context.Background(), p, m)
	if !errors.Is(e, want) || !reflect.DeepEqual(checked, checkedBasicMaterial{}) {
		t.Fatal("unsafe v2 material accepted, reached wrong stage or retained partial checked authority", e)
	}
	if strings.Contains(e.Error(), "provider-private") || strings.Contains(e.Error(), strings.Repeat("s", 32)) {
		t.Fatal("private material echoed")
	}
	if !reflect.DeepEqual(before, snapshot(t, f.root)) {
		t.Fatal("rejected material check wrote run intent/process/workdir state")
	}
}

// Direct Plan construction below is fault injection after proving the real
// compiler validator rejects the changed envelope. It never asserts that such
// a Plan was honestly admitted or grants any capability.
func sourceMaterialFaultPlan(t *testing.T, f sourceMaterialFixture, mutate func(*PlanDocument)) Plan {
	t.Helper()
	doc := f.plan.Document()
	mutate(&doc)
	var fields map[string]json.RawMessage
	if json.Unmarshal(planRaw(t, doc), &fields) != nil {
		t.Fatal("fault Plan fixture")
	}
	delete(fields, "plan_digest")
	raw := planRaw(t, fields)
	if p, e := BuildPlan(raw, sourceCoreValidator(f.compiled)); !errors.Is(e, ErrPlan) || p.Digest() != "" || len(p.Bytes()) != 0 {
		t.Fatal("changed inputs unexpectedly passed real compiler Plan validator", e)
	}
	digest, e := ComputePlanDigest(raw)
	if e != nil {
		t.Fatal(e)
	}
	fields["plan_digest"] = planRaw(t, digest)
	canonical, e := config.CanonicalJSON(planRaw(t, fields))
	if e != nil {
		t.Fatal(e)
	}
	return Plan{raw: canonical, digest: digest}
}
func TestSourceMaterialAssetAndPlanBindingsBeforeELF(t *testing.T) {
	for _, name := range []string{"provider-missing", "rule-missing", "provider-byte-change", "rule-byte-change", "provider-extra", "rule-extra", "provider-kind", "provider-name", "input-provider-missing", "input-rule-missing", "input-provider-wrong-sha", "input-rule-wrong-sha", "input-core-wrong-sha", "input-cap-wrong-sha", "input-core-missing", "input-cap-missing"} {
		t.Run(name, func(t *testing.T) {
			f := sourceMaterialBuild(t, "/missing/approved-sources-core")
			m := f.material
			p := f.plan
			provider := sourceMaterialFile(m, "provider")
			rule := sourceMaterialFile(m, "rule")
			switch name {
			case "provider-missing":
				sourceMaterialDrop(&m, provider)
			case "rule-missing":
				sourceMaterialDrop(&m, rule)
			case "provider-byte-change":
				m.Files[provider] = append(m.Files[provider], ' ')
				basicMaterialRehash(&m)
			case "rule-byte-change":
				m.Files[rule] = append(m.Files[rule], ' ')
				basicMaterialRehash(&m)
			case "provider-extra", "rule-extra":
				kind := "provider"
				if name == "rule-extra" {
					kind = "rule"
				}
				data := []byte("unbound private asset")
				m.Files["extra-asset.json"] = data
				m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: kind, Name: "extra-asset.json", Bytes: int64(len(data)), SHA256: hash(data)})
			case "provider-kind":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == provider {
						m.Manifest.Files[i].Kind = "rule"
					}
				}
			case "provider-name":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == provider {
						m.Manifest.Files[i].Name = "renamed-provider.json"
					}
				}
				m.Files["renamed-provider.json"] = m.Files[provider]
				delete(m.Files, provider)
			default:
				p = sourceMaterialFaultPlan(t, f, func(d *PlanDocument) {
					kind := "provider"
					if strings.Contains(name, "rule-") {
						kind = "rule_asset"
					}
					if strings.Contains(name, "core-") {
						kind = "core_binary"
					}
					if strings.Contains(name, "cap-") {
						kind = "capabilities"
					}
					if strings.HasSuffix(name, "missing") {
						kept := []PlanInput{}
						for _, input := range d.Inputs {
							if input.Kind != kind {
								kept = append(kept, input)
							}
						}
						d.Inputs = kept
					} else {
						for i := range d.Inputs {
							if d.Inputs[i].Kind == kind {
								d.Inputs[i].SHA256 = strings.Repeat("0", 64)
							}
						}
					}
				})
			}
			sourceMaterialFail(t, f, p, m, ErrPlan)
		})
	}
}
func TestSourceMaterialGenerationCapabilityAndIntentBindings(t *testing.T) {
	for _, name := range []string{"generation", "compiler", "cap-digest", "binary-hash", "withdrawn-dynamic-binary", "binary-version", "binary-path", "binary-count", "intent-rehashed", "plan-intent-changed", "duplicate-file", "wrong-size", "wrong-kind"} {
		t.Run(name, func(t *testing.T) {
			f := sourceMaterialBuild(t, "/missing/approved-sources-core")
			m := f.material
			p := f.plan
			want := error(ErrPlan)
			switch name {
			case "generation":
				m.Manifest.Generation = "generation:other"
			case "compiler":
				m.Manifest.CompilerVersion = "other"
			case "cap-digest":
				m.Manifest.CapabilitiesDigest = strings.Repeat("0", 64)
			case "binary-hash":
				m.Manifest.Binaries[0].SHA256 = strings.Repeat("0", 64)
				want = ErrCapabilityUnsupported
			case "withdrawn-dynamic-binary":
				m.Manifest.Binaries[0].SHA256 = "1edcbb3646ec89c99d45fac5dfe47d053695b51b71c6af8b76f381ef1704ff77"
				want = ErrCapabilityUnsupported
			case "binary-version":
				m.Manifest.Binaries[0].Version = "wrong"
				want = ErrCapabilityUnsupported
			case "binary-path":
				m.Manifest.Binaries[0].Path = "/missing/other-sources-core"
				want = ErrCapabilityUnsupported
			case "binary-count":
				m.Manifest.Binaries = append(m.Manifest.Binaries, m.Manifest.Binaries[0])
				want = ErrCapabilityUnsupported
			case "intent-rehashed":
				var intent map[string]any
				if json.Unmarshal(m.Files["intent.json"], &intent) != nil {
					t.Fatal("fixture")
				}
				intent["retention"] = map[string]any{"enabled": false, "days": 31}
				m.Files["intent.json"] = planRaw(t, intent)
				basicMaterialRehash(&m)
			case "plan-intent-changed":
				p = sourceMaterialFaultPlan(t, f, func(d *PlanDocument) { d.Intent = []byte(`{"schema_version":1}`) })
			case "duplicate-file":
				m.Manifest.Files = append(m.Manifest.Files, m.Manifest.Files[0])
			case "wrong-size":
				m.Manifest.Files[0].Bytes++
			case "wrong-kind":
				m.Manifest.Files[0].Kind = "foreign"
			}
			sourceMaterialFail(t, f, p, m, want)
		})
	}
}
func TestSourceMaterialSnapshotProfileAndVersionConfusion(t *testing.T) {
	for _, name := range []string{"schema1", "schema3", "dynamic-selected", "probe-policy", "members", "entry-auth", "controller", "config-hash", "snapshot-kind", "generated-bytes", "unsafe-profile-rebound", "v1-capability"} {
		t.Run(name, func(t *testing.T) {
			f := sourceMaterialBuild(t, "/missing/approved-sources-core")
			m := f.material
			s := sourceMaterialSnapshot(t, m)
			want := error(ErrPlan)
			switch name {
			case "schema1":
				s.SchemaVersion = 1
			case "schema3":
				s.SchemaVersion = 3
			case "dynamic-selected":
				sourceMaterialProxy(&s, "URLTest").Selected = s.Proxies[len(s.Proxies)-1].Name
			case "probe-policy":
				sourceMaterialProxy(&s, "URLTest").Probe.ToleranceMS++
			case "members":
				sourceMaterialProxy(&s, "Selector").Members = []string{"REJECT"}
				sourceMaterialProxy(&s, "Selector").Selected = "REJECT"
			case "entry-auth":
				s.Entries[0].AuthSHA256 = strings.Repeat("0", 64)
			case "controller":
				s.Controller = "127.0.0.1:32002"
			case "config-hash":
				s.ConfigSHA256 = strings.Repeat("0", 64)
			case "snapshot-kind":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == mihomo.SnapshotFile {
						m.Manifest.Files[i].Kind = "route_map"
					}
				}
			case "generated-bytes":
				m.Files["core.json"] = append(m.Files["core.json"], ' ')
			case "unsafe-profile-rebound":
				var raw map[string]any
				if json.Unmarshal(m.Files["core.json"], &raw) != nil {
					t.Fatal("fixture")
				}
				raw["tun"] = map[string]any{"enable": true}
				m.Files["core.json"] = planRaw(t, raw)
				s.ConfigSHA256 = hash(m.Files["core.json"])
			case "v1-capability":
				cap, e := mihomo.ApprovedBasicCapability(m.Manifest.Binaries[0].Path)
				if e != nil {
					t.Fatal(e)
				}
				m.Manifest.Binaries[0] = cap.Binary
				want = ErrCapabilityUnsupported
			}
			m.Files[mihomo.SnapshotFile] = planRaw(t, s)
			basicMaterialRehash(&m)
			sourceMaterialFail(t, f, f.plan, m, want)
		})
	}
}
func TestSourceMaterialActualStaticApprovedELFReadOnly(t *testing.T) {
	source := os.Getenv("HOOPA_P1_SOURCES_CORE")
	if source == "" {
		t.Fatal("HOOPA_P1_SOURCES_CORE is required for actual static positive; skipping is not acceptance")
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != sourceStaticSHA {
		t.Fatal("actual source binary is not approved static build02")
	}
	dir := filepath.Join(t.TempDir(), "private-binary")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "mihomo")
	if e := os.WriteFile(binary, raw, 0700); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(binary)
	if e != nil {
		t.Fatal(e)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 1 {
		t.Fatal("test copy must be a single-link inode")
	}
	parsed, e := elf.Open(binary)
	if e != nil {
		t.Fatal(e)
	}
	for _, program := range parsed.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("withdrawn dynamic-interpreter binary must not be used")
		}
	}
	if parsed.Close() != nil {
		t.Fatal("close ELF")
	}
	f := sourceMaterialBuild(t, binary)
	before := snapshot(t, f.root)
	binaryBefore := snapshot(t, dir)
	checked, e := checkBasicMaterial(context.Background(), f.plan, f.material)
	if e != nil {
		t.Fatal("actual static v2 material refused", e)
	}
	if checked.capability.Protocol != 2 || checked.capability.Binary.SHA256 != sourceStaticSHA || checked.launch.RunID != "" || checked.launch.OperationID != "" || checked.launch.NonceSHA256 != "" || checked.launch.ManifestSHA256 == "" || checked.launch.ConfigSHA256 != hash(f.material.Files["core.json"]) || checked.launch.Binary.Path != binary {
		t.Fatal("checked v2 template lost binding or minted runtime identities")
	}
	if !reflect.DeepEqual(before, snapshot(t, f.root)) || !reflect.DeepEqual(binaryBefore, snapshot(t, dir)) {
		t.Fatal("read-only static check wrote workdir/binary or created runtime state")
	}
	f.material.Files["core.json"][0] = '!'
	f.material.Manifest.Files[0].SHA256 = "changed"
	f.material.Manifest.Binaries[0].Version = "changed"
	if checked.files["core.json"][0] == '!' || checked.manifest.Files[0].SHA256 == "changed" || checked.manifest.Binaries[0].Version == "changed" {
		t.Fatal("checked material aliases caller slices")
	}
	for _, scenario := range []string{"workdir-config-mismatch", "unsafe-workdir"} {
		t.Run(scenario, func(t *testing.T) {
			next := sourceMaterialBuild(t, binary)
			if scenario == "workdir-config-mismatch" {
				if e := os.WriteFile(filepath.Join(next.material.Workdir, "config.json"), []byte("different"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.Chmod(next.material.Workdir, 0755); e != nil {
					t.Fatal(e)
				}
			}
			sourceMaterialFail(t, next, next.plan, next.material, config.ErrReadUnavailable)
		})
	}
	t.Log("actual approved static ELF copied to private single-link path, checked and closed; no core execution")
}

func TestSourceMaterialV2SnapshotCannotDowngradeThroughV1Registry(t *testing.T) {
	f := sourceMaterialBuild(t, "/missing/approved-sources-core")
	m := f.material
	old, e := mihomo.ApprovedBasicCapability(m.Manifest.Binaries[0].Path)
	if e != nil {
		t.Fatal(e)
	}
	digest, e := old.Digest()
	if e != nil {
		t.Fatal(e)
	}
	// Even a fault-injected envelope aligned to a genuine v1 registry entry must
	// not let its v1 decoder consume a v2 typed snapshot. The real compiler Plan
	// validator first proves that the altered binary/capability inputs are invalid.
	p := sourceMaterialFaultPlan(t, f, func(d *PlanDocument) {
		d.CapabilitiesDigest = digest
		for i := range d.Inputs {
			switch d.Inputs[i].Kind {
			case "core_binary":
				d.Inputs[i].SHA256 = old.Binary.SHA256
			case "capabilities":
				d.Inputs[i].SHA256 = digest
			}
		}
	})
	m.Manifest.Binaries = []config.BinaryReference{old.Binary}
	m.Manifest.CapabilitiesDigest = digest
	sourceMaterialFail(t, f, p, m, ErrPlan)
}
func TestSourceMaterialEmptyPlanAndCancelledCheckHaveNoAuthority(t *testing.T) {
	root := t.TempDir()
	before := snapshot(t, root)
	for _, ctx := range []context.Context{nil, context.Background()} {
		checked, e := checkBasicMaterial(ctx, Plan{}, BasicMaterial{Workdir: root})
		want := error(ErrPlan)
		if ctx == nil {
			want = config.ErrReadUnavailable
		}
		if !errors.Is(e, want) || !reflect.DeepEqual(checked, checkedBasicMaterial{}) {
			t.Fatal("empty/cancelled preflight retained authority", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checked, e := checkBasicMaterial(ctx, Plan{}, BasicMaterial{Workdir: root})
	if !errors.Is(e, config.ErrReadUnavailable) || !reflect.DeepEqual(checked, checkedBasicMaterial{}) {
		t.Fatal("cancelled preflight retained authority", e)
	}
	if !reflect.DeepEqual(before, snapshot(t, root)) {
		t.Fatal("early rejection created state")
	}
}
