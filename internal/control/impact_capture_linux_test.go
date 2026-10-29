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
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/policy"
)

func TestImpactBundleProjectionUsesStoredConfiguration(t *testing.T) {
	f := sourceMaterialBuild(t, "/uninstalled/impact-source")
	got, err := impactFromBundle(f.material.Manifest, f.material.Files)
	if err != nil || !got.Known || len(got.Nodes) != 1 || got.Nodes[0].SubscriptionID != "source-a" || !reflect.DeepEqual(got.Groups, []string{"US", "follow"}) || !reflect.DeepEqual(got.Rules, []string{"domain-rule"}) || len(got.Apps) != 0 {
		t.Fatal("source projection", err)
	}
	raw, err := json.Marshal(got)
	if err != nil || bytes.Contains(raw, []byte("provider-private")) || bytes.Contains(raw, []byte("probe.example")) || bytes.Contains(raw, []byte("uninstalled")) {
		t.Fatal("metadata leaked private input", err)
	}
	s := initializeSettings(t)
	compiled := appPlanCompilation(t, appPlanIntent(t), s, initializeApp(t, s, "app-a", 27001, "q"))
	m, files := compiled.Bundle()
	app, err := impactFromBundle(m, files)
	if err != nil || !app.Known || !reflect.DeepEqual(app.Apps, []string{"app-a"}) || len(app.Nodes)+len(app.Groups)+len(app.Rules) != 0 {
		t.Fatal("app projection", err)
	}
	_, basic := basicMaterialFixture(t, "/uninstalled/impact-basic")
	legacy, err := impactFromBundle(basic.Manifest, basic.Files)
	if err != nil || legacy.Known || legacy.Generation != basic.Manifest.Generation || !isHash(legacy.ManifestSHA256) || legacy.Nodes == nil || len(legacy.Nodes)+len(legacy.Groups)+len(legacy.Rules)+len(legacy.Apps) != 0 {
		t.Fatal("older profile must explicitly lack metadata", err)
	}
}

func TestImpactBundleProjectionRejectsMissingOrInconsistentMetadata(t *testing.T) {
	for _, fault := range []string{"route-missing", "route-unknown-field", "route-too-large", "route-null", "route-generation", "node-source", "node-name", "node-extra", "group-source", "group-members", "group-missing", "rule-index", "rule-target", "app-extra", "default", "compiler", "capability", "bytes"} {
		t.Run(fault, func(t *testing.T) {
			f := sourceMaterialBuild(t, "/uninstalled/impact-source")
			m := f.material
			name := sourceMaterialFile(m, "route_map")
			var route policy.RouteMap
			if err := json.Unmarshal(m.Files[name], &route); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "route-missing":
				sourceMaterialDrop(&m, name)
			case "route-unknown-field":
				m.Files[name] = append(bytes.TrimSuffix(m.Files[name], []byte("}")), []byte(",\"extra\":true}")...)
			case "route-too-large":
				m.Files[name] = bytes.Repeat([]byte{' '}, (1<<20)+1)
			case "route-null":
				route.Nodes = nil
			case "route-generation":
				route.Generation = "wrong"
			case "node-source":
				route.Nodes[0].SubscriptionID = "undeclared"
			case "node-name":
				route.Nodes[0].Alias = "node:wrong"
			case "node-extra":
				route.Nodes = append(route.Nodes, policy.RouteNode{Alias: "node:extra", SubscriptionID: "source-a", NodeID: "extra"})
			case "group-source":
				route.Groups[0].SourceID = "undeclared"
			case "group-members":
				route.Groups[0].Members = []string{"REJECT"}
			case "group-missing":
				route.Groups = route.Groups[:1]
			case "rule-index":
				route.Rules[0].Index = 1
			case "rule-target":
				route.Rules[0].Target = "DIRECT"
			case "app-extra":
				route.Apps = []policy.RouteApp{{ID: "app-a", Entry: policy.AppEntryAlias("app-a")}}
			case "default":
				route.DefaultTarget = "DIRECT"
			case "compiler":
				m.Manifest.CompilerVersion = "unrecognized"
			case "capability":
				m.Manifest.CapabilitiesDigest = strings.Repeat("f", 64)
			case "bytes":
				m.Files[name] = append(m.Files[name], ' ')
			}
			if fault != "route-missing" && fault != "route-unknown-field" && fault != "route-too-large" && fault != "bytes" {
				m.Files[name] = planRaw(t, route)
			}
			// Rehashed corruption cannot count as a successful metadata projection.
			basicMaterialRehash(&m)
			if got, err := impactFromBundle(m.Manifest, m.Files); !errors.Is(err, config.ErrReadUnavailable) || !reflect.DeepEqual(got, impactGeneration{}) {
				t.Fatal("bad route metadata accepted", err)
			}
		})
	}
}

// Requires the actual approved source ELF for positive Plan verification, but
// never launches it. The published prior bundle is an explicit metadata fixture.
func TestSourcePlanImpactVersion2ActualELFAndForgedRollbackRefusal(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, paths, _, intent := sourcePlanTestFixture(t, core)
	raw := sourcePlanTestRaw(t, intent)
	first, err := c.PlanSources(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	doc := first.Document()
	if doc.SchemaVersion != 2 || doc.Impact.Details == nil || doc.Impact.Details.Scope != "generation_union" || len(doc.Impact.Details.AffectedNodes) == 0 || doc.Impact.Details.ExistingConnections != "no_previous_generation" || doc.Impact.Details.RollbackTarget.Kind != "stop_candidate" {
		t.Fatal("incomplete first preview")
	}
	captured := sourcePlanTestCapture(t, c, raw)
	compiled, _, err := compileCapturedSources(context.Background(), captured)
	if err != nil {
		t.Fatal(err)
	}
	m, files := compiled.Bundle()
	sha, err := c.store.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	base := captured.Base
	gen := m.Generation
	op := config.ID("impact-published-metadata-fixture")
	base.Generation, base.ManifestSHA256, base.OperationID, base.DesiredRuntime = &gen, sha, &op, "running"
	if err := c.store.PublishActive(base); err != nil {
		t.Fatal(err)
	}
	// Candidate deliberately has no groups/nodes; affected metadata must still
	// include the actually applied prior bundle, not the candidate or Latest.
	intent["groups"], intent["subscriptions"] = []any{}, []any{}
	intent["selection"] = map[string]any{"active_subscription": nil}
	intent["default_target"] = map[string]any{"kind": "reject"}
	newRaw := sourcePlanTestRaw(t, intent)
	next, err := c.PlanSources(context.Background(), newRaw)
	if err != nil {
		t.Fatal(err)
	}
	nd := next.Document()
	if !reflect.DeepEqual(nd.Impact.Details.AffectedNodes, doc.Impact.Details.AffectedNodes) || !reflect.DeepEqual(nd.Impact.Details.AffectedGroups, doc.Impact.Details.AffectedGroups) || nd.Impact.Details.ExistingConnections != "interrupt" || nd.Impact.Details.RollbackTarget.Generation == nil || *nd.Impact.Details.RollbackTarget.Generation != string(gen) || *nd.Impact.Details.RollbackTarget.ManifestSHA256 != sha {
		t.Fatal("prior metadata/restoration identity lost")
	}
	fresh := sourcePlanTestCapture(t, c, newRaw)
	current, _, err := compileCapturedSources(context.Background(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	wrong := strings.Repeat("f", 64)
	nd.Impact.Details.RollbackTarget.ManifestSHA256 = &wrong
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(planRaw(t, nd), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "plan_digest")
	forged, err := BuildPlan(planRaw(t, fields), sourceCompilationValidator(current))
	if err != nil {
		t.Fatal("structural Plan fixture", err)
	}
	a, err := NewSourcesApplier(c, []string{"internal-launch"})
	if err != nil {
		t.Fatal(err)
	}
	sourceApplyReject(t, c, paths, a, coordinatorRequest(t, "forged-rollback", forged, nil), ErrConflict)
}

func TestImpactCaptureBindsActualPreviousManifestAndOwnedBytes(t *testing.T) {
	c, paths := coordinatorFixture(t, nil)
	f := sourceMaterialBuild(t, "/uninstalled/impact-source")
	sha, err := c.store.PublishBundle(f.material.Manifest, f.material.Files)
	if err != nil {
		t.Fatal(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	gen := f.material.Manifest.Generation
	op := config.ID("impact-history-fixture")
	base.Generation, base.ManifestSHA256, base.OperationID, base.DesiredRuntime = &gen, sha, &op, "running"
	if err := c.store.PublishActive(base); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, paths)
	c.mu.Lock()
	captured, err := c.captureImpactBaseLocked(base)
	c.mu.Unlock()
	if err != nil || captured == nil || captured.ManifestSHA256 != sha || captured.Generation != gen || !captured.Known {
		t.Fatal("previous capture", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("capture wrote")
	}
	// A target with no provider metadata must still disclose the previous source
	// nodes and groups, rather than deriving both sides from the new declaration.
	s := initializeSettings(t)
	compiled := appPlanCompilation(t, appPlanIntent(t), s, initializeApp(t, s, "app-a", 27001, "q"))
	m, files := compiled.Bundle()
	target, err := impactFromBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	impact, err := projectSourceImpact(base, captured, target)
	if err != nil || len(impact.Details.AffectedNodes) != 1 || !reflect.DeepEqual(impact.Details.AffectedGroups, []string{"US", "follow"}) || !reflect.DeepEqual(impact.AffectedApps, []string{"app-a"}) || *impact.Details.RollbackTarget.ManifestSHA256 != sha {
		t.Fatal("old metadata lost", err)
	}
	wrong := base
	wrong.ManifestSHA256 = strings.Repeat("f", 64)
	c.mu.Lock()
	bad, err := c.captureImpactBaseLocked(wrong)
	c.mu.Unlock()
	if !errors.Is(err, config.ErrReadUnavailable) || bad != nil {
		t.Fatal("wrong active manifest accepted", err)
	}
	name := sourceMaterialFile(f.material, "route_map")
	path := filepath.Join(paths.State, "bundles", hash([]byte(gen)), name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	before = coordinatorTree(t, paths)
	c.mu.Lock()
	bad, err = c.captureImpactBaseLocked(base)
	c.mu.Unlock()
	if !errors.Is(err, config.ErrReadUnavailable) || bad != nil || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("read failure became unknown or repaired", err)
	}
	if !captured.Known || len(captured.Nodes) != 1 {
		t.Fatal("owned prior capture changed after file loss")
	}
}

func TestImpactNewSourceApplyRejectsLegacyPlanBeforeEffects(t *testing.T) {
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
	fields["schema_version"] = json.RawMessage("1")
	fields["base_revision"] = planRaw(t, initializeRevision(t, c))
	var impact map[string]json.RawMessage
	if err := json.Unmarshal(fields["impact"], &impact); err != nil {
		t.Fatal(err)
	}
	delete(impact, "details")
	fields["impact"] = planRaw(t, impact)
	legacy, err := BuildPlan(planRaw(t, fields), sourceCompilationValidator(compiled))
	if err != nil {
		t.Fatal("legacy read compatibility", err)
	}
	a, err := NewSourcesApplier(c, []string{"internal-launch"})
	if err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, paths)
	op, made, err := a.Apply(context.Background(), coordinatorRequest(t, "legacy-new", legacy, nil))
	if !errors.Is(err, ErrCapabilityUnsupported) || made || !reflect.DeepEqual(op, StoredOperation{}) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("new legacy admission", err)
	}
}

func TestImpactLegacySourceRequestReplaysBeforeVersionGate(t *testing.T) {
	// The original v1 operation is committed through the existing synthetic
	// runtime ordering fixture; only exact durable replay is asserted here.
	c, paths, stored, state, runtime, raw := applyOrderingFixture(t)
	c.mu.Lock()
	final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
	c.mu.Unlock()
	if err != nil || final.Record.Phase != Committed {
		t.Fatal(err)
	}
	a, err := NewSourcesApplier(c, []string{"internal-launch"})
	if err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, paths)
	replayed, made, err := a.Apply(context.Background(), raw)
	if err != nil || made || replayed.SHA256 != final.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("historical v1 request reinterpreted as a new v2 admission", err)
	}
}
