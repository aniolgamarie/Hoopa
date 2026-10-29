//go:build linux && amd64

package control

import (
	"bytes"
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
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

func TestAppPlanSourceSettingsRejectsEmbeddedEntries(t *testing.T) {
	key, runtime := codecSettingsInputs()
	runtime.AppEntries = map[config.ID]profile.Entry{"app-a": runtime.Entry}
	value, err := NewSourceSettings(key, runtime)
	if !errors.Is(err, ErrSourceSettings) || value.Digest() != "" {
		t.Fatal("v1 source settings silently dropped separate app inputs", err)
	}
}

func appPlanIntent(t *testing.T) []byte {
	t.Helper()
	cap, err := mihomo.ApprovedAppRoutingCapability("/uninstalled/app-plan-core")
	if err != nil {
		t.Fatal(err)
	}
	m := updateRequestIntent(t)
	m["core"] = map[string]any{"mihomo": cap.Binary}
	m["subscriptions"], m["groups"], m["rules"] = []any{}, []any{}, []any{}
	m["selection"] = map[string]any{"active_subscription": nil}
	m["apps"] = []any{map[string]any{"id": "app-a", "mode": "managed", "metering": false, "bypass": []any{}}}
	m["default_target"] = map[string]any{"kind": "reject"}
	return planRaw(t, m)
}

func appPlanCompilation(t *testing.T, raw []byte, settings SourceSettings, b AppEntryBinding) policy.Compilation {
	t.Helper()
	data, err := b.SensitiveBytes()
	if err != nil {
		t.Fatal(err)
	}
	c, err := compileSourceAppIdentity(raw, settings, nil, map[config.ID][]byte{b.AppID(): data})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func appPlanBuild(t *testing.T, compiled policy.Compilation, raw []byte) (Plan, BasicMaterial) {
	t.Helper()
	m, files := compiled.Bundle()
	target, err := impactFromBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	impact, err := projectSourceImpact(config.ActivePointer{Version: 1, Revision: "app-plan-base", DesiredRuntime: "stopped"}, nil, target)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{"schema_version": 2, "base_revision": "app-plan-base", "compiler_version": m.CompilerVersion, "capabilities_digest": m.CapabilitiesDigest, "intent": json.RawMessage(raw), "inputs": sourceCoreInputs(compiled.Inputs()), "impact": impact, "blockers": []PlanBlocker{}}
	p, err := BuildPlan(planRaw(t, fields), sourceCompilationValidator(compiled))
	if err != nil {
		t.Fatal(err)
	}
	return p, BasicMaterial{Manifest: m, Files: files, Workdir: "/uninstalled/app-plan-work"}
}

func TestAppPlanIdentityCommitsExactBindingAndCompiler(t *testing.T) {
	s := initializeSettings(t)
	b := initializeApp(t, s, "app-a", 27001, "q")
	raw := appPlanIntent(t)
	compiled := appPlanCompilation(t, raw, s, b)
	m, files := compiled.Bundle()
	if m.CompilerVersion != policy.AppCompilerVersion {
		t.Fatal("app compiler identity missing")
	}
	inputs := compiled.Inputs()
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Kind != inputs[j].Kind {
			return inputs[i].Kind < inputs[j].Kind
		}
		return inputs[i].ID < inputs[j].ID
	})
	canonical, _ := config.CanonicalJSON(raw)
	seed := planRaw(t, []any{"hoopa.source-generation.v1", "hoopa.apps.v1", json.RawMessage(canonical), m.CapabilitiesDigest, inputs, s.Digest()})
	if string(m.Generation) != "source-"+hash(seed) {
		t.Fatal("generation omits app compiler/full inputs/settings")
	}
	name, _ := config.AppBindingFilename("app-a")
	want, _ := b.SensitiveBytes()
	if !bytes.Equal(files[name], want) {
		t.Fatal("record bytes not preserved")
	}
	p, material := appPlanBuild(t, compiled, raw)
	if _, err := ParsePlan(p.Bytes(), sourceCompilationValidator(compiled)); err != nil {
		t.Fatal(err)
	}
	if _, err := checkBasicMaterialContents(context.Background(), p, material); err != nil {
		t.Fatal("pure material", err)
	}
	for _, secret := range []string{strings.Repeat("q", 32), "user-app-a", string(want)} {
		if bytes.Contains(p.Bytes(), []byte(secret)) {
			t.Fatal("private record leaked to Plan")
		}
	}
	pretty := new(bytes.Buffer)
	if err := json.Indent(pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	again := appPlanCompilation(t, pretty.Bytes(), s, b)
	m2, f2 := again.Bundle()
	if !reflect.DeepEqual(m, m2) || !reflect.DeepEqual(files, f2) {
		t.Fatal("representation changed compilation")
	}
	changed := initializeApp(t, s, "app-a", 27002, "z")
	next := appPlanCompilation(t, raw, s, changed)
	m3, _ := next.Bundle()
	if m3.Generation == m.Generation {
		t.Fatal("record change retained generation")
	}
	if sourceCompilationValidator(next)(p.Document().Intent, p.Document().Inputs) == nil {
		t.Fatal("old approval accepts changed private input")
	}
	key, r := codecSettingsInputs()
	key[0] ^= 255
	other, err := NewSourceSettings(key, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compileSourceAppIdentity(raw, other, nil, map[config.ID][]byte{"app-a": want}); !errors.Is(err, config.ErrAppBinding) {
		t.Fatal("wrong parent accepted", err)
	}
}

func TestAppPlanMaterialRequiresExactPrivateAsset(t *testing.T) {
	for _, mutation := range []string{"missing", "changed", "kind", "name", "extra", "input-missing", "input-hash", "input-kind", "input-duplicate"} {
		t.Run(mutation, func(t *testing.T) {
			s := initializeSettings(t)
			raw := appPlanIntent(t)
			compiled := appPlanCompilation(t, raw, s, initializeApp(t, s, "app-a", 27001, "q"))
			p, m := appPlanBuild(t, compiled, raw)
			name, _ := config.AppBindingFilename("app-a")
			switch mutation {
			case "missing":
				sourceMaterialDrop(&m, name)
			case "changed":
				m.Files[name] = append(m.Files[name], ' ')
				basicMaterialRehash(&m)
			case "kind":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == name {
						m.Manifest.Files[i].Kind = "validation"
					}
				}
			case "name":
				for i := range m.Manifest.Files {
					if m.Manifest.Files[i].Name == name {
						m.Manifest.Files[i].Name = "renamed-app.json"
					}
				}
				m.Files["renamed-app.json"] = m.Files[name]
				delete(m.Files, name)
			case "extra":
				data := []byte("unbound")
				m.Files["extra-app.json"] = data
				m.Manifest.Files = append(m.Manifest.Files, config.BundleFile{Kind: "app_binding", Name: "extra-app.json", Bytes: int64(len(data)), SHA256: hash(data)})
			default:
				p = sourceMaterialFaultPlan(t, sourceMaterialFixture{plan: p, compiled: compiled}, func(d *PlanDocument) {
					for i, in := range d.Inputs {
						if in.Kind != "app_binding" {
							continue
						}
						switch mutation {
						case "input-missing":
							d.Inputs = append(d.Inputs[:i], d.Inputs[i+1:]...)
						case "input-hash":
							d.Inputs[i].SHA256 = strings.Repeat("f", 64)
						case "input-kind":
							d.Inputs[i].Kind = "provider"
						case "input-duplicate":
							d.Inputs = append(d.Inputs, in)
						}
						return
					}
				})
				// A forged internal Plan is not a codec admission. Duplicates are
				// rejected by ParsePlan before material is ever consulted.
				if mutation == "input-duplicate" {
					if _, err := ParsePlan(p.Bytes(), sourceCompilationValidator(compiled)); !errors.Is(err, ErrPlan) {
						t.Fatal(err)
					}
					return
				}
			}
			if got, err := checkBasicMaterialContents(context.Background(), p, m); !errors.Is(err, ErrPlan) || !reflect.DeepEqual(got, basicMaterialContents{}) {
				t.Fatal("partial/unbound material admitted", err)
			}
		})
	}
}

func TestAppPlanCaptureOwnsInputsAndRechecksHistory(t *testing.T) {
	for _, fault := range []string{"none", "missing", "changed-after-history", "missing-after-history", "unused-conflict", "unused-wrong-parent", "malformed-unused"} {
		t.Run(fault, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			s := initializeSettings(t)
			initializePut(t, c, s)
			b := initializeApp(t, s, "app-a", 27001, "q")
			raw := appPlanIntent(t)
			if fault != "missing" {
				appInitializePut(t, c, b)
			}
			name, _ := config.AppBindingFilename("app-a")
			path := filepath.Join(p.State, "credentials", name)
			wantErr := error(nil)
			switch fault {
			case "missing":
				wantErr = config.ErrReadUnavailable
			case "changed-after-history", "missing-after-history":
				appInitializeHistory(t, c, b)
				wantErr = ErrRecoveryRequired
				if fault == "missing-after-history" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					data, _ := initializeApp(t, s, "app-a", 27002, "z").SensitiveBytes()
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "unused-conflict", "unused-wrong-parent":
				parent := s
				port := uint16(27001)
				wantErr = config.ErrAppBindingConflict
				if fault == "unused-wrong-parent" {
					key, r := codecSettingsInputs()
					key[0] ^= 255
					var err error
					parent, err = NewSourceSettings(key, r)
					if err != nil {
						t.Fatal(err)
					}
					port = 27003
					wantErr = ErrRecoveryRequired
				}
				// Direct Store publication deliberately bypasses the Coordinator's
				// global preparation check to model valid-file out-of-band damage.
				if _, err := c.store.PublishAppBinding(initializeApp(t, parent, "unused", port, "x")); err != nil {
					t.Fatal(err)
				}
			case "malformed-unused":
				wantErr = config.ErrReadUnavailable
				if err := os.WriteFile(filepath.Join(p.State, "credentials", "app-entry-invalid.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := coordinatorTree(t, p)
			c.mu.Lock()
			captured, err := c.captureSourceCompileLocked(context.Background(), raw)
			c.mu.Unlock()
			if !errors.Is(err, wantErr) {
				t.Fatalf("capture got%v want%v", err, wantErr)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("capture wrote")
			}
			if wantErr != nil {
				if !reflect.DeepEqual(captured, sourceCompileInputs{}) {
					t.Fatal("partial capture returned")
				}
				return
			}
			data, _ := b.SensitiveBytes()
			if len(captured.appBindings) != 1 || !bytes.Equal(captured.appBindings["app-a"], data) {
				t.Fatal("missing canonical capture")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := compileSourceAppIdentity(captured.canonicalIntent, captured.Settings, nil, captured.appBindings); err != nil {
				t.Fatal("owned capture changed", err)
			}
			c.mu.Lock()
			_, err = c.captureSourceCompileLocked(context.Background(), raw)
			c.mu.Unlock()
			if !errors.Is(err, config.ErrReadUnavailable) {
				t.Fatal("fresh capture missed loss", err)
			}
		})
	}
}

func TestAppPlanNoManagedEntriesDoesNotConsumeAppInventory(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	if err := os.WriteFile(filepath.Join(p.State, "credentials", "app-entry-invalid.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := appPlanIntent(t)
	var intent map[string]any
	if err := json.Unmarshal(raw, &intent); err != nil {
		t.Fatal(err)
	}
	intent["apps"] = []any{map[string]any{"id": "app-a", "mode": "direct", "metering": false, "bypass": []any{}}}
	before := coordinatorTree(t, p)
	captured := sourcePlanTestCapture(t, c, planRaw(t, intent))
	if len(captured.appBindings) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("nonmanaged capture consumed/mutated inventory")
	}
}

// Pure compilation supplies an honest content Plan, but no installed ELF is
// claimed here. These pre-ELF failures must stop the real Apply entry point
// before any operation, workdir or runtime state is created.
func TestAppPlanApplyRechecksLostAndChangedBindingsBeforeEffects(t *testing.T) {
	for _, fault := range []string{"missing", "changed-with-history", "settings-loss"} {
		t.Run(fault, func(t *testing.T) {
			c, paths := coordinatorFixture(t, nil)
			s := initializeSettings(t)
			initializePut(t, c, s)
			b := initializeApp(t, s, "app-a", 27001, "q")
			appInitializePut(t, c, b)
			raw := appPlanIntent(t)
			compiled := appPlanCompilation(t, raw, s, b)
			p, _ := appPlanBuild(t, compiled, raw)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(p.Bytes(), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "plan_digest")
			fields["base_revision"] = planRaw(t, initializeRevision(t, c))
			p, err := BuildPlan(planRaw(t, fields), sourceCompilationValidator(compiled))
			if err != nil {
				t.Fatal(err)
			}
			name, _ := config.AppBindingFilename("app-a")
			path := filepath.Join(paths.State, "credentials", name)
			want := config.ErrReadUnavailable
			switch fault {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed-with-history":
				appInitializeHistory(t, c, b)
				data, _ := initializeApp(t, s, "app-a", 27002, "z").SensitiveBytes()
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrRecoveryRequired
			case "settings-loss":
				if err := os.Remove(filepath.Join(paths.State, "credentials", "source-settings-v1.json")); err != nil {
					t.Fatal(err)
				}
			}
			a, err := NewSourcesApplier(c, []string{"internal-launch"})
			if err != nil {
				t.Fatal(err)
			}
			before := coordinatorTree(t, paths)
			op, made, err := a.Apply(context.Background(), coordinatorRequest(t, "app-recapture", p, nil))
			if made || !errors.Is(err, want) || !reflect.DeepEqual(op, StoredOperation{}) {
				t.Fatalf("Apply got%v made%v want%v", err, made, want)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("rejected Apply wrote")
			}
		})
	}
}
