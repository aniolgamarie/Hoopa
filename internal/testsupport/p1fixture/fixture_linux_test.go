//go:build linux && amd64 && p1integration

package p1fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/mihomo"
)

func repository(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}
func originalPaths() []string {
	paths := []string{descriptorPath}
	for _, key := range []string{"app-first", "domain-first"} {
		prefix := "private/p1-basic-fixture-validation/" + key + "/"
		paths = append(paths, prefix+"config.json", prefix+"expected-snapshot.json")
	}
	return paths
}
func sourceState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, name := range originalPaths() {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			t.Fatal("fixed fixture is missing", name, err)
		}
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal("fixed fixture cannot be read", name, err)
		}
		state[name] = fmt.Sprintf("%v:%d:%d:%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), digest(raw))
	}
	return state
}
func copiedRepository(t *testing.T) string {
	t.Helper()
	original := repository(t)
	root := t.TempDir()
	for _, name := range originalPaths() {
		raw, err := os.ReadFile(filepath.Join(original, name))
		if err != nil {
			t.Fatal("required fixture missing; no synthetic replacement", name, err)
		}
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func loadFixture(t *testing.T, key string) *Fixture {
	t.Helper()
	fixture, err := Load(repository(t), key, filepath.Join(t.TempDir(), "declared-approved-core"), filepath.Join(t.TempDir(), "future-workdir"))
	if err != nil {
		t.Fatal("required original finite fixture unavailable", key, err)
	}
	return fixture
}
func impactFor(f *Fixture) control.PlanImpact {
	return control.PlanImpact{RequiresDisruption: true, AffectedApps: []string{"entry-a", "entry-b"}, TargetGeneration: string(f.Generation()), ConnectionEffect: "interrupt", RuntimeAction: "start"}
}
func planFor(t *testing.T, f *Fixture) control.Plan {
	t.Helper()
	plan, err := f.Plan("fixture-base-revision", impactFor(f))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestFixtureLoadsExactIndependentOriginalsReadOnly(t *testing.T) {
	root := repository(t)
	before := sourceState(t, root)
	for _, key := range []string{"app-first", "domain-first"} {
		t.Run(key, func(t *testing.T) {
			declaredBinary := filepath.Join(t.TempDir(), "declared-not-executed-core")
			workdir := filepath.Join(t.TempDir(), "future-workdir")
			f, err := Load(root, key, declaredBinary, workdir)
			if err != nil {
				t.Fatal("required fixed fixture missing/invalid", err)
			}
			if f.Key() != key || f.Generation() != config.Generation("fixture-"+key) {
				t.Fatal("wrong finite identity")
			}
			material := f.Material()
			if material.Workdir != workdir || len(material.Files) != 3 || len(material.Manifest.Files) != 3 || material.Manifest.CompilerVersion != CompilerVersion || len(material.Manifest.Binaries) != 1 || material.Manifest.Binaries[0].Path != declaredBinary || material.Manifest.Binaries[0].SHA256 != approvedCoreSHA256 {
				t.Fatal("material binding incomplete")
			}
			prefix := "private/p1-basic-fixture-validation/" + key + "/"
			rawConfig, err := os.ReadFile(filepath.Join(root, prefix+"config.json"))
			if err != nil {
				t.Fatal(err)
			}
			rawExpected, err := os.ReadFile(filepath.Join(root, prefix+"expected-snapshot.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(material.Files["config.json"], rawConfig) || !bytes.Equal(material.Files[mihomo.SnapshotFile], rawExpected) {
				t.Fatal("config or independent expected bytes were rewritten")
			}
			for _, spec := range material.Manifest.Files {
				raw := material.Files[spec.Name]
				if spec.Bytes != int64(len(raw)) || spec.SHA256 != digest(raw) {
					t.Fatal("manifest omitted original bytes")
				}
			}
			var configObject map[string]json.RawMessage
			if json.Unmarshal(rawConfig, &configObject) != nil {
				t.Fatal("fixture config")
			}
			var listeners, proxies, groups, rules []json.RawMessage
			json.Unmarshal(configObject["listeners"], &listeners)
			json.Unmarshal(configObject["proxies"], &proxies)
			json.Unmarshal(configObject["proxy-groups"], &groups)
			json.Unmarshal(configObject["rules"], &rules)
			snapshot := f.Snapshot()
			if len(listeners) != 2 || len(proxies) != 2 || len(groups) != 2 || len(rules) != 4 || len(snapshot.Entries) != 2 || len(snapshot.Rules) != 4 || snapshot.ConfigSHA256 != digest(rawConfig) || snapshot.Entries[0].AuthSHA256 == snapshot.Entries[1].AuthSHA256 {
				t.Fatal("complete dual-entry/rule/node/auth structure lost")
			}
			if _, err := os.Lstat(workdir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("loader created workdir")
			}
			if _, err := os.Lstat(declaredBinary); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("loader created/opened a binary artifact")
			}
			var secret string
			json.Unmarshal(configObject["secret"], &secret)
			if secret == "" || strings.Contains(fmt.Sprintf("%v %#v", f, f), secret) {
				t.Fatal("fixture formatting is not redacted")
			}
			t.Logf("finite fixture=%s generation=%s config_bytes=%d config_sha256=%s independent_snapshot_bytes=%d independent_snapshot_sha256=%s; no core executed", key, f.Generation(), len(rawConfig), digest(rawConfig), len(rawExpected), digest(rawExpected))
		})
	}
	if !reflect.DeepEqual(before, sourceState(t, root)) {
		t.Fatal("Load rewrote a source fixture")
	}
}
func TestFixtureDoesNotReadObservedOrHistoricalRecords(t *testing.T) {
	root := copiedRepository(t)
	for _, key := range []string{"app-first", "domain-first"} {
		f, err := Load(root, key, "/test-only/approved-core", "/test-only/workdir")
		if err != nil || f == nil {
			t.Fatal("loader depended on absent observed/result evidence", err)
		}
	}
	// The copied repository deliberately contains only descriptor/config/expected,
	// never observed_snapshot, run_result, historical bundle or acceptance files.
}
func TestFixtureRejectsChangedHashSizeAndPaths(t *testing.T) {
	for _, change := range []string{"descriptor-hash", "descriptor-over-limit", "reference-path", "reference-size", "reference-hash", "config-hash", "config-short", "config-long", "expected-hash", "expected-short", "missing-config", "missing-expected", "file-symlink", "parent-symlink", "hardlink", "root-symlink"} {
		t.Run(change, func(t *testing.T) {
			root := copiedRepository(t)
			configPath := filepath.Join(root, "private/p1-basic-fixture-validation/app-first/config.json")
			expectedPath := filepath.Join(root, "private/p1-basic-fixture-validation/app-first/expected-snapshot.json")
			descriptorFile := filepath.Join(root, descriptorPath)
			switch change {
			case "descriptor-hash":
				raw, err := os.ReadFile(descriptorFile)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(descriptorFile, append(raw, '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			case "descriptor-over-limit":
				if err := os.WriteFile(descriptorFile, bytes.Repeat([]byte{' '}, descriptorLimit+1), 0644); err != nil {
					t.Fatal(err)
				}
			case "reference-path", "reference-size", "reference-hash":
				raw, err := os.ReadFile(descriptorFile)
				if err != nil {
					t.Fatal(err)
				}
				var object map[string]any
				json.Unmarshal(raw, &object)
				ref := object["bundles"].(map[string]any)["app-first"].(map[string]any)["source_config"].(map[string]any)
				if change == "reference-path" {
					ref["path"] = "../config.json"
				} else if change == "reference-size" {
					ref["bytes"] = 1247
				} else {
					ref["sha256"] = strings.Repeat("0", 64)
				}
				changed, _ := json.Marshal(object)
				if err = os.WriteFile(descriptorFile, changed, 0644); err != nil {
					t.Fatal(err)
				}
			case "config-hash", "config-short", "config-long", "expected-hash", "expected-short":
				path := configPath
				if strings.HasPrefix(change, "expected-") {
					path = expectedPath
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(change, "-hash") {
					raw[0] ^= 1
				} else if strings.HasSuffix(change, "-short") {
					raw = raw[:len(raw)-1]
				} else {
					raw = append(raw, ' ')
				}
				if err = os.WriteFile(path, raw, 0644); err != nil {
					t.Fatal(err)
				}
			case "missing-config":
				if err := os.Remove(configPath); err != nil {
					t.Fatal(err)
				}
			case "missing-expected":
				if err := os.Remove(expectedPath); err != nil {
					t.Fatal(err)
				}
			case "file-symlink":
				if err := os.Rename(configPath, configPath+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("config.json.real", configPath); err != nil {
					t.Fatal(err)
				}
			case "parent-symlink":
				parent := filepath.Dir(configPath)
				if err := os.Rename(parent, parent+"-real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("app-first-real", parent); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(configPath, configPath+".alias"); err != nil {
					t.Fatal(err)
				}
			case "root-symlink":
				alias := filepath.Join(t.TempDir(), "repo-link")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				root = alias
			}
			if fixture, err := Load(root, "app-first", "/test-only/approved-core", "/test-only/workdir"); fixture != nil || !errors.Is(err, ErrFixture) {
				t.Fatal("changed fixture accepted", err)
			}
		})
	}
}
func TestFixtureRejectsUnknownKeysAndPathShapes(t *testing.T) {
	root := repository(t)
	for _, key := range []string{"", "app", "APP-FIRST", "../app-first", "app-first/../domain-first", "unknown"} {
		if fixture, err := Load(root, key, "/test-only/approved-core", "/test-only/workdir"); fixture != nil || !errors.Is(err, ErrFixture) {
			t.Fatal("unknown key accepted")
		}
	}
	for _, test := range []struct{ root, binary, work string }{{".", "/test/core", "/test/work"}, {"/", "/test/core", "/test/work"}, {root, "core", "/test/work"}, {root, "/", "/test/work"}, {root, "/test/core", "work"}, {root, "/test/core", "/"}, {root, "/test/../core", "/test/work"}, {root, "/test/core", "/test/work\x00"}} {
		if fixture, err := Load(test.root, "app-first", test.binary, test.work); fixture != nil || !errors.Is(err, ErrFixture) {
			t.Fatal("unsafe path shape accepted")
		}
	}
}
func TestFixtureDetachedMaterialInputsAndSnapshot(t *testing.T) {
	f := loadFixture(t, "app-first")
	baseline := f.Material()
	inputs := f.Inputs()
	snapshot := f.Snapshot()
	changed := f.Material()
	for name := range changed.Files {
		changed.Files[name][0] ^= 1
	}
	delete(changed.Files, "intent.json")
	changed.Manifest.Binaries[0].Path = "/changed"
	changed.Manifest.Files[0].Name = "changed"
	changed.Workdir = "/changed"
	detachedInputs := f.Inputs()
	detachedInputs[0].SHA256 = strings.Repeat("f", 64)
	changedSnapshot := f.Snapshot()
	changedSnapshot.Entries[0].Name = "changed"
	changedSnapshot.Rules[0].Target = "changed"
	changedSnapshot.Proxies[0].Name = "changed"
	for i := range changedSnapshot.Proxies {
		if len(changedSnapshot.Proxies[i].Members) > 0 {
			changedSnapshot.Proxies[i].Members[0] = "changed"
			break
		}
	}
	if !reflect.DeepEqual(f.Material(), baseline) || !reflect.DeepEqual(f.Inputs(), inputs) || !reflect.DeepEqual(f.Snapshot(), snapshot) {
		t.Fatal("caller mutation changed retained finite fixture")
	}
	plan := planFor(t, f)
	source, err := f.Source(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	source.Files["config.json"][0] ^= 1
	again, err := f.Source(context.Background(), plan)
	if err != nil || !reflect.DeepEqual(again, baseline) {
		t.Fatal("Source shared material buffers", err)
	}
}
func TestFixtureExactIntentAndInputs(t *testing.T) {
	f := loadFixture(t, "app-first")
	intent := f.Material().Files["intent.json"]
	if err := f.ValidateIntent(intent, f.Inputs()); err != nil {
		t.Fatal(err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, intent, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := f.ValidateIntent(formatted.Bytes(), f.Inputs()); err != nil {
		t.Fatal("canonical-equivalent reference rejected", err)
	}
	for _, field := range []string{"schema_version", "kind", "bundle_key", "config_sha256", "snapshot_sha256", "core_binary", "capabilities_digest", "extra"} {
		var object map[string]any
		json.Unmarshal(intent, &object)
		switch field {
		case "schema_version":
			object[field] = 2
		case "core_binary":
			object[field].(map[string]any)["path"] = "/different/core"
		default:
			object[field] = "changed"
		}
		changed, _ := json.Marshal(object)
		if err := f.ValidateIntent(changed, f.Inputs()); !errors.Is(err, ErrFixture) {
			t.Fatal("changed finite intent accepted", field)
		}
	}
	for _, change := range []string{"nil", "missing", "extra", "order", "kind", "id", "sha"} {
		inputs := f.Inputs()
		switch change {
		case "nil":
			inputs = nil
		case "missing":
			inputs = inputs[:1]
		case "extra":
			inputs = append(inputs, control.PlanInput{Kind: "rule_asset", ID: "other", SHA256: strings.Repeat("a", 64)})
		case "order":
			inputs[0], inputs[1] = inputs[1], inputs[0]
		case "kind":
			inputs[0].Kind = "provider"
		case "id":
			inputs[0].ID = "other"
		case "sha":
			inputs[0].SHA256 = strings.Repeat("f", 64)
		}
		if err := f.ValidateIntent(intent, inputs); !errors.Is(err, ErrFixture) {
			t.Fatal("changed inputs accepted", change)
		}
	}
	duplicate := append(append([]byte(nil), intent[:len(intent)-1]...), []byte(`,"kind":"duplicate"}`)...)
	if err := f.ValidateIntent(duplicate, f.Inputs()); !errors.Is(err, ErrFixture) {
		t.Fatal("duplicate finite intent accepted")
	}
}
func adversarialPlan(t *testing.T, plan control.Plan, change func(map[string]any)) control.Plan {
	t.Helper()
	var object map[string]any
	if json.Unmarshal(plan.Bytes(), &object) != nil {
		t.Fatal("plan fixture")
	}
	delete(object, "plan_digest")
	change(object)
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately permissive construction ONLY creates adversarial test plans.
	// It is never used by Fixture, a product Source or actual control admission.
	bad, err := control.BuildPlan(raw, func(json.RawMessage, []control.PlanInput) error { return nil })
	if err != nil {
		t.Fatal("adversarial plan shape", err)
	}
	return bad
}
func TestFixturePlanAndSourceRestrictions(t *testing.T) {
	for _, key := range []string{"app-first", "domain-first"} {
		t.Run(key, func(t *testing.T) {
			f := loadFixture(t, key)
			plan := planFor(t, f)
			document := plan.Document()
			if document.CompilerVersion != CompilerVersion || document.CapabilitiesDigest != f.CapabilityDigest() || document.Impact.TargetGeneration != string(f.Generation()) || document.BaseRevision != "fixture-base-revision" || len(document.Blockers) != 0 || !reflect.DeepEqual(document.Inputs, f.Inputs()) {
				t.Fatal("Plan lost finite identity")
			}
			if _, err := control.ParsePlan(plan.Bytes(), f.ValidateIntent); err != nil {
				t.Fatal("generated plan invalid", err)
			}
			material, err := f.Source(context.Background(), plan)
			if err != nil || !reflect.DeepEqual(material, f.Material()) {
				t.Fatal("valid finite Source rejected", err)
			}
			if _, err := f.Source(nil, plan); !errors.Is(err, ErrFixture) {
				t.Fatal("nil context accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := f.Source(ctx, plan); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrFixture) {
				t.Fatal("cancel cause lost", err)
			}
			if _, err := f.Source(context.Background(), control.Plan{}); !errors.Is(err, ErrFixture) {
				t.Fatal("zero plan accepted")
			}
			for _, change := range []string{"compiler", "capability", "target", "intent", "inputs"} {
				bad := adversarialPlan(t, plan, func(object map[string]any) {
					switch change {
					case "compiler":
						object["compiler_version"] = "other-compiler"
					case "capability":
						object["capabilities_digest"] = strings.Repeat("f", 64)
					case "target":
						object["impact"].(map[string]any)["target_generation"] = "other-generation"
					case "intent":
						object["intent"].(map[string]any)["bundle_key"] = "unapproved"
					case "inputs":
						object["inputs"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("f", 64)
					}
				})
				if _, err := f.Source(context.Background(), bad); !errors.Is(err, ErrFixture) {
					t.Fatal("Source accepted foreign plan metadata", change)
				}
			}
			impact := impactFor(f)
			impact.TargetGeneration = "foreign-generation"
			if _, err := f.Plan("base", impact); !errors.Is(err, ErrFixture) {
				t.Fatal("Plan changed fixed generation")
			}
			if _, err := f.Plan("../bad", impactFor(f)); !errors.Is(err, ErrFixture) {
				t.Fatal("invalid base accepted")
			}
		})
	}
	first := loadFixture(t, "app-first")
	second := loadFixture(t, "domain-first")
	if _, err := second.Source(context.Background(), planFor(t, first)); !errors.Is(err, ErrFixture) {
		t.Fatal("Source accepted another finite reference")
	}
}
