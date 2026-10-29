//go:build linux && amd64

package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func impactFixture() (config.ActivePointer, *impactGeneration, impactGeneration) {
	generation := config.Generation("old-generation")
	operation := config.ID("old-operation")
	base := config.ActivePointer{Version: 1, Revision: "revision", Generation: &generation, ManifestSHA256: strings.Repeat("a", 64), OperationID: &operation, DesiredRuntime: "running"}
	previous := &impactGeneration{Generation: generation, ManifestSHA256: base.ManifestSHA256, Known: true, Nodes: []PlanImpactNode{{SubscriptionID: "b", NodeID: "same"}, {SubscriptionID: "a", NodeID: "removed"}}, Groups: []string{"old-group", "shared-group"}, Rules: []string{"old-rule", "shared-rule"}, Apps: []string{"removed-app", "shared-app"}}
	target := impactGeneration{Generation: "new-generation", ManifestSHA256: strings.Repeat("b", 64), Known: true, Nodes: []PlanImpactNode{{SubscriptionID: "a", NodeID: "same"}, {SubscriptionID: "b", NodeID: "same"}}, Groups: []string{"new-group", "shared-group"}, Rules: []string{"new-rule", "shared-rule"}, Apps: []string{"new-app", "shared-app"}}
	return base, previous, target
}
func TestImpactProjectStartAndReplace(t *testing.T) {
	base, previous, target := impactFixture()
	got, e := projectSourceImpact(base, previous, target)
	if e != nil {
		t.Fatal(e)
	}
	old := string(*base.Generation)
	hash := base.ManifestSHA256
	want := PlanImpact{RequiresDisruption: true, AffectedApps: []string{"new-app", "removed-app", "shared-app"}, PreviousGeneration: &old, TargetGeneration: "new-generation", ConnectionEffect: "interrupt", RuntimeAction: "replace", Details: &PlanImpactDetails{Scope: "generation_union", AffectedNodes: []PlanImpactNode{{SubscriptionID: "a", NodeID: "removed"}, {SubscriptionID: "a", NodeID: "same"}, {SubscriptionID: "b", NodeID: "same"}}, AffectedGroups: []string{"new-group", "old-group", "shared-group"}, AffectedRules: []string{"new-rule", "old-rule", "shared-rule"}, ExistingConnections: "interrupt", NewConnections: "target_after_activation", RollbackTarget: PlanRollbackTarget{Kind: "restore_generation", Generation: &old, ManifestSHA256: &hash}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrong detached generation union: %#v", got)
	}
	base = config.ActivePointer{Version: 1, Revision: "revision", DesiredRuntime: "stopped"}
	got, e = projectSourceImpact(base, nil, target)
	if e != nil || got.RuntimeAction != "start" || got.PreviousGeneration != nil || got.Details.Scope != "generation_union" || got.Details.ExistingConnections != "no_previous_generation" || got.Details.RollbackTarget.Kind != "stop_candidate" || got.Details.RollbackTarget.Generation != nil || got.Details.RollbackTarget.ManifestSHA256 != nil || !got.RequiresDisruption || got.ConnectionEffect != "interrupt" {
		t.Fatal("first start semantics", e, got)
	}
	raw, e := json.Marshal(got.Details.RollbackTarget)
	if e != nil || string(raw) != `{"kind":"stop_candidate","generation":null,"manifest_sha256":null}` {
		t.Fatal("rollback nulls/schema mismatch", string(raw), e)
	}
}
func TestImpactProjectPreviousUnknownAndEmpty(t *testing.T) {
	base, previous, target := impactFixture()
	previous.Known = false
	previous.Nodes = []PlanImpactNode{}
	previous.Groups = []string{}
	previous.Rules = []string{}
	previous.Apps = []string{}
	got, e := projectSourceImpact(base, previous, target)
	if e != nil || got.Details.Scope != "known_generation_union" || got.Details.ExistingConnections != "interrupt" || !got.RequiresDisruption || !reflect.DeepEqual(got.AffectedApps, []string{"new-app", "shared-app"}) || *got.Details.RollbackTarget.ManifestSHA256 != base.ManifestSHA256 {
		t.Fatal("unknown pretended complete or changed rollback", e)
	}
	previous.Nodes = nil
	bad, err := projectSourceImpact(base, previous, target)
	if err != ErrPlan || !reflect.DeepEqual(bad, PlanImpact{}) {
		t.Fatal("unknown nil metadata disguised as explicit empty")
	}
	previous.Nodes = []PlanImpactNode{}
	previous.Known = true
	emptyTarget := target
	emptyTarget.Nodes = []PlanImpactNode{}
	emptyTarget.Groups = []string{}
	emptyTarget.Rules = []string{}
	emptyTarget.Apps = []string{}
	got, e = projectSourceImpact(base, previous, emptyTarget)
	if e != nil || got.Details.Scope != "generation_union" || got.AffectedApps == nil || got.Details.AffectedNodes == nil || got.Details.AffectedGroups == nil || got.Details.AffectedRules == nil {
		t.Fatal("known empty became missing metadata", e)
	}
	raw, e := json.Marshal(got)
	if e != nil || strings.Contains(string(raw), `"affected_apps":null`) || strings.Contains(string(raw), `"affected_nodes":null`) {
		t.Fatal("empty projection not explicit arrays", e)
	}
}
func TestImpactProjectRejectsIdentityShapeAndBounds(t *testing.T) {
	edits := map[string]func(*config.ActivePointer, **impactGeneration, *impactGeneration){
		"base-version":  func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) { b.Version = 2 },
		"base-revision": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) { b.Revision = "bad value" },
		"base-generation": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) {
			v := config.Generation("bad value")
			b.Generation = &v
		},
		"base-hash": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) {
			b.ManifestSHA256 = strings.Repeat("A", 64)
		},
		"base-op": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) { b.OperationID = nil },
		"base-invalid-op": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) {
			id := config.ID("bad value")
			b.OperationID = &id
		},
		"previous-invalid-generation": func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) {
			(*p).Generation = "bad value"
		},
		"previous-invalid-hash": func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) {
			(*p).ManifestSHA256 = strings.Repeat("A", 64)
		},
		"target-uppercase-hash": func(_ *config.ActivePointer, _ **impactGeneration, v *impactGeneration) {
			v.ManifestSHA256 = strings.Repeat("A", 64)
		},
		"base-runtime":        func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) { b.DesiredRuntime = "unknown" },
		"missing-previous":    func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) { *p = nil },
		"previous-generation": func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) { (*p).Generation = "other" },
		"previous-hash": func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) {
			(*p).ManifestSHA256 = strings.Repeat("c", 64)
		},
		"target-generation": func(_ *config.ActivePointer, _ **impactGeneration, v *impactGeneration) { v.Generation = "bad value" },
		"target-hash":       func(_ *config.ActivePointer, _ **impactGeneration, v *impactGeneration) { v.ManifestSHA256 = "" },
		"target-unknown":    func(_ *config.ActivePointer, _ **impactGeneration, v *impactGeneration) { v.Known = false },
		"unexpected-previous-first-start": func(b *config.ActivePointer, _ **impactGeneration, _ *impactGeneration) {
			b.Generation = nil
			b.ManifestSHA256 = ""
			b.DesiredRuntime = "stopped"
		},
	}
	for _, field := range []string{"nodes", "groups", "rules", "apps"} {
		field := field
		for _, side := range []string{"previous", "target"} {
			side := side
			edits[side+"-nil-"+field] = func(_ *config.ActivePointer, p **impactGeneration, v *impactGeneration) {
				if side == "previous" {
					v = *p
				}
				switch field {
				case "nodes":
					v.Nodes = nil
				case "groups":
					v.Groups = nil
				case "rules":
					v.Rules = nil
				case "apps":
					v.Apps = nil
				}
			}
			edits[side+"-duplicate-"+field] = func(_ *config.ActivePointer, p **impactGeneration, v *impactGeneration) {
				if side == "previous" {
					v = *p
				}
				switch field {
				case "nodes":
					v.Nodes = append(v.Nodes, v.Nodes[0])
				case "groups":
					v.Groups = append(v.Groups, v.Groups[0])
				case "rules":
					v.Rules = append(v.Rules, v.Rules[0])
				case "apps":
					v.Apps = append(v.Apps, v.Apps[0])
				}
			}
			edits[side+"-invalid-"+field] = func(_ *config.ActivePointer, p **impactGeneration, v *impactGeneration) {
				if side == "previous" {
					v = *p
				}
				switch field {
				case "nodes":
					v.Nodes[0].NodeID = "bad value"
				case "groups":
					v.Groups[0] = "bad value"
				case "rules":
					v.Rules[0] = "bad value"
				case "apps":
					v.Apps[0] = "bad value"
				}
			}
			edits[side+"-overflow-"+field] = func(_ *config.ActivePointer, p **impactGeneration, v *impactGeneration) {
				if side == "previous" {
					v = *p
				}
				switch field {
				case "nodes":
					for i := len(v.Nodes); i <= 64; i++ {
						v.Nodes = append(v.Nodes, PlanImpactNode{SubscriptionID: "source", NodeID: config.ID(fmt.Sprint(i))})
					}
				case "groups":
					for i := len(v.Groups); i <= 64; i++ {
						v.Groups = append(v.Groups, fmt.Sprint(i))
					}
				case "rules":
					for i := len(v.Rules); i <= 64; i++ {
						v.Rules = append(v.Rules, fmt.Sprint(i))
					}
				case "apps":
					v.Apps = append(v.Apps, "third", "fourth")
				}
			}
		}
	}
	edits["node-source-invalid"] = func(_ *config.ActivePointer, _ **impactGeneration, v *impactGeneration) {
		v.Nodes[0].SubscriptionID = ""
	}
	edits["unknown-nonempty"] = func(_ *config.ActivePointer, p **impactGeneration, _ *impactGeneration) { (*p).Known = false }
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			base, previous, target := impactFixture()
			edit(&base, &previous, &target)
			got, e := projectSourceImpact(base, previous, target)
			if !errors.Is(e, ErrPlan) || !reflect.DeepEqual(got, PlanImpact{}) {
				t.Fatal("invalid input returned usable impact", e, got)
			}
		})
	}
	for _, kind := range []string{"stopped", "same-generation"} {
		t.Run(kind, func(t *testing.T) {
			base, previous, target := impactFixture()
			if kind == "stopped" {
				base.DesiredRuntime = "stopped"
			} else {
				target.Generation = *base.Generation
			}
			got, e := projectSourceImpact(base, previous, target)
			if !errors.Is(e, ErrCapabilityUnsupported) || !reflect.DeepEqual(got, PlanImpact{}) {
				t.Fatal("unsupported runtime action admitted", e)
			}
		})
	}
}
func TestImpactProjectOwnershipAndMaximumUnion(t *testing.T) {
	base, previous, target := impactFixture()
	got, e := projectSourceImpact(base, previous, target)
	if e != nil {
		t.Fatal(e)
	}
	detached := got
	detached.Details.AffectedNodes[0].NodeID = "output-changed"
	detached.Details.AffectedGroups[0] = "output-changed"
	detached.AffectedApps[0] = "output-changed"
	if previous.Nodes[1].NodeID != "removed" || target.Groups[0] != "new-group" || target.Apps[0] != "new-app" {
		t.Fatal("output mutation rewrote inputs")
	}
	got, e = projectSourceImpact(base, previous, target)
	if e != nil {
		t.Fatal(e)
	}
	previous.Nodes[0].NodeID = "changed"
	previous.Groups[0] = "changed"
	previous.Apps[0] = "changed"
	target.Nodes[0].NodeID = "changed"
	target.Rules[0] = "changed"
	*base.Generation = "changed"
	base.ManifestSHA256 = "changed"
	if got.Details.AffectedNodes[0].NodeID != "removed" || got.Details.AffectedGroups[1] != "old-group" || got.AffectedApps[1] != "removed-app" || *got.PreviousGeneration != "old-generation" || *got.Details.RollbackTarget.Generation != "old-generation" {
		t.Fatal("output aliases input")
	}
	*got.PreviousGeneration = "mutated"
	if *got.Details.RollbackTarget.Generation != "old-generation" {
		t.Fatal("output identity pointers alias")
	}
	base, previous, target = impactFixture()
	for _, v := range []*impactGeneration{previous, &target} {
		v.Nodes = []PlanImpactNode{}
		v.Groups = []string{}
		v.Rules = []string{}
		v.Apps = []string{}
		prefix := "old"
		if v == &target {
			prefix = "new"
		}
		for i := 63; i >= 0; i-- {
			id := fmt.Sprintf("%s-%02d", prefix, i)
			v.Nodes = append(v.Nodes, PlanImpactNode{SubscriptionID: config.ID(prefix), NodeID: config.ID(id)})
			v.Groups = append(v.Groups, id)
			v.Rules = append(v.Rules, id)
		}
		for i := 2; i >= 0; i-- {
			v.Apps = append(v.Apps, fmt.Sprintf("%s-app-%d", prefix, i))
		}
	}
	got, e = projectSourceImpact(base, previous, target)
	if e != nil || len(got.Details.AffectedNodes) != 128 || len(got.Details.AffectedGroups) != 128 || len(got.Details.AffectedRules) != 128 || len(got.AffectedApps) != 6 || got.Details.AffectedNodes[0].SubscriptionID != "new" || got.Details.AffectedGroups[0] != "new-00" {
		t.Fatal("per-generation bound confused with union bound", e)
	}
}
