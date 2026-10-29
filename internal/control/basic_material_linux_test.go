//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	wire "hoopa/protocol/corecontrol"
)

// Codec-only precompiled material, not a complete Intent compiler or proof of
// current routing behavior. Only the optional positive reads the actual approved ELF.
func basicMaterialFixture(t *testing.T, binary string) (Plan, BasicMaterial) {
	t.Helper()
	cap, e := mihomo.ApprovedBasicCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	capSHA, e := cap.Digest()
	if e != nil {
		t.Fatal(e)
	}
	work := filepath.Join(t.TempDir(), "work")
	if e := os.Mkdir(work, 0700); e != nil {
		t.Fatal(e)
	}
	users := []struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{{strings.Repeat("u", 32), strings.Repeat("p", 32)}}
	raw := planRaw(t, map[string]any{"mode": "rule", "log-level": "silent", "ipv6": false, "find-process-mode": "off", "external-controller": "127.0.0.1:32000", "secret": strings.Repeat("s", 32), "listeners": []any{map[string]any{"name": "entry-a", "type": "mixed", "listen": "127.0.0.1", "port": 32001, "udp": false, "users": users}}, "hosts": map[string]any{}, "proxies": []any{}, "proxy-groups": []any{}, "rules": []string{"MATCH,REJECT"}, "profile": map[string]any{"store-selected": false, "store-fake-ip": false}})
	snapshot := wire.Snapshot{SchemaVersion: 1, ConfigSHA256: hash(raw), Controller: "127.0.0.1:32000", Entries: []wire.Entry{{Name: "entry-a", Type: "mixed", Address: "127.0.0.1:32001", AuthSHA256: hash(planRaw(t, users))}}, Proxies: []wire.Proxy{{Name: "DIRECT", Type: "Direct", Members: []string{}}, {Name: "REJECT", Type: "Reject", Members: []string{}}, {Name: "REJECT-DROP", Type: "RejectDrop", Members: []string{}}, {Name: "COMPATIBLE", Type: "Compatible", Members: []string{}}, {Name: "PASS", Type: "Pass", Members: []string{}}, {Name: "GLOBAL", Type: "Selector", Members: []string{"DIRECT", "REJECT"}, Selected: "DIRECT"}}, Rules: []wire.Rule{{Type: "Match", Payload: "", Target: "REJECT"}}}
	sort.Slice(snapshot.Proxies, func(i, j int) bool { return snapshot.Proxies[i].Name < snapshot.Proxies[j].Name })
	if e := snapshot.Validate(); e != nil {
		t.Fatal(e)
	}
	obj := planFixture(t)
	var intent map[string]any
	json.Unmarshal(obj["intent"], &intent)
	intent["core"] = map[string]any{"mihomo": cap.Binary}
	obj["intent"] = planRaw(t, intent)
	obj["capabilities_digest"] = planRaw(t, capSHA)
	obj["inputs"] = planRaw(t, []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: cap.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}})
	obj["blockers"] = json.RawMessage(`[]`)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	material := BasicMaterial{Manifest: config.BundleManifest{Version: 1, Generation: "g2", CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Binaries: []config.BinaryReference{cap.Binary}}, Files: map[string][]byte{"intent.json": obj["intent"], "config.json": raw, mihomo.SnapshotFile: planRaw(t, snapshot)}, Workdir: work}
	for _, pair := range []struct{ kind, name string }{{"intent", "intent.json"}, {"generated_config", "config.json"}, {"validation", mihomo.SnapshotFile}} {
		data := material.Files[pair.name]
		material.Manifest.Files = append(material.Manifest.Files, config.BundleFile{Kind: pair.kind, Name: pair.name, Bytes: int64(len(data)), SHA256: hash(data)})
	}
	if e := os.WriteFile(filepath.Join(work, "config.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	return p, material
}
func basicMaterialRehash(m *BasicMaterial) {
	for i := range m.Manifest.Files {
		f := &m.Manifest.Files[i]
		if b, ok := m.Files[f.Name]; ok {
			f.Bytes = int64(len(b))
			f.SHA256 = hash(b)
		}
	}
}
func TestBasicMaterialRefusals(t *testing.T) {
	cases := map[string]func(*BasicMaterial){
		"generation": func(m *BasicMaterial) { m.Manifest.Generation = "other" }, "compiler": func(m *BasicMaterial) { m.Manifest.CompilerVersion = "other" }, "capability": func(m *BasicMaterial) { m.Manifest.CapabilitiesDigest = strings.Repeat("0", 64) }, "binary": func(m *BasicMaterial) { m.Manifest.Binaries[0].SHA256 = strings.Repeat("0", 64) }, "two-binaries": func(m *BasicMaterial) { m.Manifest.Binaries = append(m.Manifest.Binaries, m.Manifest.Binaries[0]) },
		"missing-file": func(m *BasicMaterial) { delete(m.Files, "intent.json") }, "extra-file": func(m *BasicMaterial) { m.Files["extra"] = []byte("x") }, "duplicate-name": func(m *BasicMaterial) { m.Manifest.Files = append(m.Manifest.Files, m.Manifest.Files[0]) }, "unknown-kind": func(m *BasicMaterial) { m.Manifest.Files[0].Kind = "unknown" }, "bad-file-name": func(m *BasicMaterial) { m.Manifest.Files[0].Name = "../intent.json" }, "wrong-bytes": func(m *BasicMaterial) { m.Files["intent.json"] = []byte("bad") }, "wrong-size": func(m *BasicMaterial) { m.Manifest.Files[0].Bytes++ },
		"missing-intent": func(m *BasicMaterial) { m.Manifest.Files[0].Kind = "route_map" }, "intent-mismatch": func(m *BasicMaterial) { m.Files["intent.json"] = []byte(`{"wrong":true}`); basicMaterialRehash(m) }, "snapshot-kind": func(m *BasicMaterial) { m.Manifest.Files[2].Kind = "route_map" }, "snapshot-null": func(m *BasicMaterial) { m.Files[mihomo.SnapshotFile] = []byte(`null`); basicMaterialRehash(m) }, "snapshot-binding": func(m *BasicMaterial) {
			var s wire.Snapshot
			json.Unmarshal(m.Files[mihomo.SnapshotFile], &s)
			s.ConfigSHA256 = strings.Repeat("0", 64)
			m.Files[mihomo.SnapshotFile] = planRaw(t, s)
			basicMaterialRehash(m)
		},
		"duplicate-config-key": func(m *BasicMaterial) { m.Files["config.json"] = []byte(`{"x":1,"x":2}`); basicMaterialRehash(m) }, "config-array": func(m *BasicMaterial) { m.Files["config.json"] = []byte(`[]`); basicMaterialRehash(m) }, "config-oversize": func(m *BasicMaterial) {
			m.Files["config.json"] = []byte(strings.Repeat(" ", 65537))
			basicMaterialRehash(m)
		}, "manifest-version": func(m *BasicMaterial) { m.Manifest.Version = 2 }, "too-many-files": func(m *BasicMaterial) {
			for len(m.Manifest.Files) <= config.MaxBundleFiles {
				m.Manifest.Files = append(m.Manifest.Files, m.Manifest.Files[0])
			}
		},
	}
	cases["two-generated"] = func(m *BasicMaterial) {
		data := append([]byte(nil), m.Files["config.json"]...)
		m.Files["config-other"] = data
		m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: "generated_config", Name: "config-other", Bytes: int64(len(data)), SHA256: hash(data)})
	}
	cases["two-intents"] = func(m *BasicMaterial) {
		data := append([]byte(nil), m.Files["intent.json"]...)
		m.Files["intent-other"] = data
		m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: "intent", Name: "intent-other", Bytes: int64(len(data)), SHA256: hash(data)})
	}
	cases["missing-snapshot"] = func(m *BasicMaterial) {
		delete(m.Files, mihomo.SnapshotFile)
		kept := m.Manifest.Files[:0]
		for _, file := range m.Manifest.Files {
			if file.Name != mihomo.SnapshotFile {
				kept = append(kept, file)
			}
		}
		m.Manifest.Files = kept
	}

	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			p, m := basicMaterialFixture(t, "/missing/approved-core")
			edit(&m)
			before := snapshot(t, filepath.Dir(m.Workdir))
			_, e := checkBasicMaterial(context.Background(), p, m)
			want := error(ErrPlan)
			if name == "binary" || name == "two-binaries" {
				want = ErrCapabilityUnsupported
			}
			if !errors.Is(e, want) {
				t.Fatal("unsafe failure", e)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(m.Workdir))) {
				t.Fatal("material check wrote filesystem")
			}
		})
	}
}
func TestBasicMaterialActualApprovedBinary(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit actual approved basic binary required; no fake-digest positive")
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("wrong approved input")
	}
	dir := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "mihomo")
	if e := os.WriteFile(binary, raw, 0755); e != nil {
		t.Fatal(e)
	}
	p, m := basicMaterialFixture(t, binary)
	before := snapshot(t, filepath.Dir(m.Workdir))
	checked, e := checkBasicMaterial(context.Background(), p, m)
	if e != nil {
		t.Fatal(e)
	}
	if checked.launch.RunID != "" || checked.launch.OperationID != "" || checked.launch.NonceSHA256 != "" || checked.launch.ManifestSHA256 == "" || checked.launch.ConfigSHA256 != hash(m.Files["config.json"]) {
		t.Fatal("launch template leaked authority or missing binding")
	}
	paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if e != nil {
		t.Fatal(e)
	}
	store, e := config.OpenStore(paths, true)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	sha, e := store.PublishBundle(checked.manifest, checked.files)
	if e != nil || sha != checked.launch.ManifestSHA256 {
		t.Fatal("Store serialization differs", e)
	}
	t.Run("assets-and-opaque-evidence", func(t *testing.T) {
		assetPlan, assets := basicMaterialFixture(t, binary)
		assetPlan, assets = basicMaterialAssets(t, assetPlan, assets)
		for _, spec := range []struct{ kind, name string }{{"validation", "other-validation"}, {"route_map", "route-evidence"}} {
			data := []byte("opaque evidence, no business-proof claim")
			assets.Files[spec.name] = data
			assets.Manifest.Files = append(assets.Manifest.Files, config.BundleFile{Kind: spec.kind, Name: spec.name, Bytes: int64(len(data)), SHA256: hash(data)})
		}
		if _, e := checkBasicMaterial(context.Background(), assetPlan, assets); e != nil {
			t.Fatal(e)
		}
	})
	for _, scenario := range []string{"nil-context", "cancelled", "workdir-config-mismatch", "workdir-mode", "binary-missing"} {
		t.Run(scenario, func(t *testing.T) {
			plan, material := basicMaterialFixture(t, binary)
			ctx := context.Background()
			switch scenario {
			case "nil-context":
				ctx = nil
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "workdir-config-mismatch":
				if e := os.WriteFile(filepath.Join(material.Workdir, "config.json"), []byte("different"), 0600); e != nil {
					t.Fatal(e)
				}
			case "workdir-mode":
				if e := os.Chmod(material.Workdir, 0755); e != nil {
					t.Fatal(e)
				}
			case "binary-missing":
				if e := os.Rename(binary, binary+".saved"); e != nil {
					t.Fatal(e)
				}
				defer func() {
					if e := os.Rename(binary+".saved", binary); e != nil {
						t.Error(e)
					}
				}()
			}
			before := snapshot(t, filepath.Dir(material.Workdir))
			if _, e := checkBasicMaterial(ctx, plan, material); !errors.Is(e, config.ErrReadUnavailable) {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(material.Workdir))) {
				t.Fatal("read failure wrote files")
			}
		})
	}

	m.Files["config.json"][0] = '!'
	m.Manifest.Binaries[0].Version = "changed"
	m.Manifest.Files[0].Name = "changed"
	if checked.files["config.json"][0] == '!' || checked.manifest.Binaries[0].Version == "changed" || checked.manifest.Files[0].Name == "changed" {
		t.Fatal("material aliases caller memory")
	}
	if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(m.Workdir))) {
		t.Fatal("read-only checker wrote workdir")
	}
	t.Log("actual approved ELF opened/hash/metadata verified and closed; no exec/network/core test")
}

func basicMaterialAssets(t *testing.T, p Plan, m BasicMaterial) (Plan, BasicMaterial) {
	t.Helper()
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(p.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	delete(obj, "plan_digest")
	in := p.Document().Inputs
	for _, asset := range []struct{ kind, planKind, name string }{{"provider", "provider", "provider-a"}, {"rule", "rule_asset", "rules-a"}} {
		data := []byte("opaque fixed " + asset.kind + " original\n")
		m.Files[asset.name] = data
		m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: asset.kind, Name: asset.name, Bytes: int64(len(data)), SHA256: hash(data)})
		in = append(in, PlanInput{Kind: asset.planKind, ID: asset.name, SHA256: hash(data)})
	}
	obj["inputs"] = planRaw(t, in)
	next, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	return next, m
}
func TestBasicMaterialAssetBindings(t *testing.T) {
	for _, change := range []string{"missing", "extra", "wrong-provider-sha", "wrong-rule-sha", "wrong-name", "wrong-kind", "core-count", "capability-count", "core-missing", "capability-missing"} {
		t.Run(change, func(t *testing.T) {
			p, m := basicMaterialFixture(t, "/missing/approved-core")
			p, m = basicMaterialAssets(t, p, m)
			switch change {
			case "missing":
				delete(m.Files, "provider-a")
				kept := m.Manifest.Files[:0]
				for _, file := range m.Manifest.Files {
					if file.Name != "provider-a" {
						kept = append(kept, file)
					}
				}
				m.Manifest.Files = kept
			case "extra":
				data := []byte("extra")
				m.Files["extra"] = data
				m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: "provider", Name: "extra", Bytes: int64(len(data)), SHA256: hash(data)})
			case "wrong-provider-sha":
				m.Files["provider-a"] = []byte("different")
				basicMaterialRehash(&m)
			case "wrong-rule-sha":
				m.Files["rules-a"] = []byte("different")
				basicMaterialRehash(&m)
			case "wrong-name":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == "provider-a" {
						m.Manifest.Files[i].Name = "provider-other"
					}
				}
				m.Files["provider-other"] = m.Files["provider-a"]
				delete(m.Files, "provider-a")
			case "wrong-kind":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == "provider-a" {
						m.Manifest.Files[i].Kind = "rule"
					}
				}
			default:
				var obj map[string]json.RawMessage
				json.Unmarshal(p.Bytes(), &obj)
				delete(obj, "plan_digest")
				inputs := p.Document().Inputs
				kind := "core_binary"
				if strings.HasPrefix(change, "capability-") {
					kind = "capabilities"
				}
				if strings.HasSuffix(change, "missing") {
					kept := []PlanInput{}
					for _, input := range inputs {
						if input.Kind != kind {
							kept = append(kept, input)
						}
					}
					inputs = kept
				} else {
					for _, input := range inputs {
						if input.Kind == kind {
							input.ID = "extra-installation"
							inputs = append(inputs, input)
							break
						}
					}
				}
				obj["inputs"] = planRaw(t, inputs)
				var e error
				p, e = BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
				if e != nil {
					t.Fatal(e)
				}
			}
			before := snapshot(t, filepath.Dir(m.Workdir))
			if _, e := checkBasicMaterial(context.Background(), p, m); !errors.Is(e, ErrPlan) {
				t.Fatal("asset binding not enforced before missing binary", e)
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(m.Workdir))) {
				t.Fatal("wrote files")
			}
		})
	}
}
