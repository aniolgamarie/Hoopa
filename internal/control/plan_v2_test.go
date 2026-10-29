//go:build linux

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func planV2Fixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	obj := planFixture(t)
	obj["schema_version"] = json.RawMessage(`2`)
	obj["impact"] = json.RawMessage(`{"requires_disruption":true,"affected_apps":["app-a"],"previous_generation":"g1","target_generation":"g2","connection_effect":"interrupt","runtime_action":"replace","details":{"scope":"generation_union","affected_nodes":[{"subscription_id":"a","node_id":"z"},{"subscription_id":"a.b","node_id":"a"}],"affected_groups":["G1"],"affected_rules":["R1"],"existing_connections":"interrupt","new_connections":"target_after_activation","rollback_target":{"kind":"restore_generation","generation":"g1","manifest_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`)
	return obj
}
func planV2Impact(t *testing.T, obj map[string]json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if json.Unmarshal(obj["impact"], &m) != nil {
		t.Fatal("fixture impact")
	}
	return m
}
func planV2Object(v any) map[string]any             { return v.(map[string]any) }
func planV2Details(m map[string]any) map[string]any { return planV2Object(m["details"]) }
func planV2Reject(t *testing.T, obj map[string]json.RawMessage) {
	t.Helper()
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if !errors.Is(e, ErrPlan) || p.Digest() != "" || p.Bytes() != nil {
		t.Fatal("invalid v2 returned usable plan", e)
	}
}
func TestPlanV2IndependentDigestAndRoundtrip(t *testing.T) {
	obj := planV2Fixture(t)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	// Python hashlib over UTF8 compact JSON with sorted ASCII property names (independent JCS vector).
	const expected = "748ca2feb60fbbc703946b8a07dde67d9c5d39987824967f251822a8840c9ddf"
	if e != nil || p.Digest() != expected {
		t.Fatal("independent v2 golden mismatch", e, p.Digest())
	}
	q, e := ParsePlan(p.Bytes(), planFixtureValidator(t, obj))
	if e != nil || !bytes.Equal(p.Bytes(), q.Bytes()) {
		t.Fatal("v2 roundtrip", e)
	}
	d := p.Document()
	if d.SchemaVersion != 2 || d.Impact.Details.Scope != "generation_union" || d.Impact.Details.AffectedNodes[0].SubscriptionID != "a" || d.Impact.Details.AffectedNodes[1].SubscriptionID != "a.b" {
		t.Fatal("fieldwise node sorting/projection lost")
	}
	*d.Impact.Details.RollbackTarget.ManifestSHA256 = "changed"
	d.Impact.Details.AffectedNodes[0].NodeID = "changed"
	d.Impact.Details.AffectedGroups[0] = "changed"
	if p.Digest() != expected || *p.Document().Impact.Details.RollbackTarget.ManifestSHA256 != strings.Repeat("a", 64) || p.Document().Impact.Details.AffectedNodes[0].NodeID != "z" {
		t.Fatal("document view aliases canonical authority")
	}
	// A v1 shape stays exact; adding details does not upgrade that schema.
	obj["schema_version"] = json.RawMessage(`1`)
	planV2Reject(t, obj)
	obj = planV2Fixture(t)
	m := planV2Impact(t, obj)
	delete(m, "details")
	obj["impact"] = planRaw(t, m)
	planV2Reject(t, obj)
}
func TestPlanV2StrictNestedFields(t *testing.T) {
	scopes := map[string][]string{"impact": {"requires_disruption", "affected_apps", "previous_generation", "target_generation", "connection_effect", "runtime_action", "details"}, "details": {"scope", "affected_nodes", "affected_groups", "affected_rules", "existing_connections", "new_connections", "rollback_target"}, "rollback": {"kind", "generation", "manifest_sha256"}, "node": {"subscription_id", "node_id"}}
	selectObject := func(m map[string]any, scope string) map[string]any {
		switch scope {
		case "impact":
			return m
		case "details":
			return planV2Details(m)
		case "rollback":
			return planV2Object(planV2Details(m)["rollback_target"])
		default:
			return planV2Object(planV2Details(m)["affected_nodes"].([]any)[0])
		}
	}
	for scope, fields := range scopes {
		for _, field := range fields {
			for _, kind := range []string{"missing", "null", "casefold"} {
				t.Run(scope+"-"+field+"-"+kind, func(t *testing.T) {
					obj := planV2Fixture(t)
					m := planV2Impact(t, obj)
					o := selectObject(m, scope)
					value := o[field]
					switch kind {
					case "missing":
						delete(o, field)
					case "null":
						o[field] = nil
					case "casefold":
						delete(o, field)
						o[strings.ToUpper(field)] = value
					}
					obj["impact"] = planRaw(t, m)
					planV2Reject(t, obj)
				})
			}
		}
		t.Run(scope+"-unknown", func(t *testing.T) {
			obj := planV2Fixture(t)
			m := planV2Impact(t, obj)
			selectObject(m, scope)["unknown"] = true
			obj["impact"] = planRaw(t, m)
			planV2Reject(t, obj)
		})
	}
	for _, bad := range []string{
		strings.Replace(string(planRaw(t, planV2Fixture(t))), `"scope":"generation_union"`, `"scope":"generation_union","scope":"generation_union"`, 1),
		strings.Replace(string(planRaw(t, planV2Fixture(t))), `"scope":"generation_union"`, `"scope":"\ud800"`, 1),
	} {
		p, e := BuildPlan([]byte(bad), planFixtureValidator(t, planV2Fixture(t)))
		if e != ErrPlan || p.Digest() != "" {
			t.Fatal("duplicate/Unicode accepted")
		}
	}
}
func TestPlanV2BoundsSortingAndConditionalVariants(t *testing.T) {
	edits := map[string]func(map[string]any){
		"select": func(m map[string]any) { m["runtime_action"] = "select" }, "none": func(m map[string]any) { m["runtime_action"] = "none" },
		"scope": func(m map[string]any) { planV2Details(m)["scope"] = "all" }, "existing": func(m map[string]any) { planV2Details(m)["existing_connections"] = "preserve_old" }, "new": func(m map[string]any) { planV2Details(m)["new_connections"] = "migrate" },
		"replace-no-previous": func(m map[string]any) { m["previous_generation"] = nil }, "replace-no-previous-connections": func(m map[string]any) { planV2Details(m)["existing_connections"] = "no_previous_generation" },
		"replace-stop-rollback":        func(m map[string]any) { planV2Object(planV2Details(m)["rollback_target"])["kind"] = "stop_candidate" },
		"rollback-generation-mismatch": func(m map[string]any) { planV2Object(planV2Details(m)["rollback_target"])["generation"] = "other" },
		"rollback-hash-uppercase": func(m map[string]any) {
			planV2Object(planV2Details(m)["rollback_target"])["manifest_sha256"] = strings.Repeat("A", 64)
		},
		"rollback-hash-short":     func(m map[string]any) { planV2Object(planV2Details(m)["rollback_target"])["manifest_sha256"] = "abc" },
		"node-string-concat-sort": func(m map[string]any) { n := planV2Details(m)["affected_nodes"].([]any); n[0], n[1] = n[1], n[0] },
		"node-within-source-unsorted": func(m map[string]any) {
			planV2Details(m)["affected_nodes"] = []any{map[string]any{"subscription_id": "a", "node_id": "z"}, map[string]any{"subscription_id": "a", "node_id": "a"}}
		},
		"node-invalid-source": func(m map[string]any) {
			planV2Object(planV2Details(m)["affected_nodes"].([]any)[0])["subscription_id"] = "bad value"
		},
		"node-invalid-id": func(m map[string]any) {
			planV2Object(planV2Details(m)["affected_nodes"].([]any)[0])["node_id"] = "bad value"
		},
		"unknown-needs-global-unknown":      func(m map[string]any) { planV2Details(m)["new_connections"] = "unknown" },
		"global-unknown-with-known-details": func(m map[string]any) { m["connection_effect"] = "unknown" },
		"preserve-old":                      func(m map[string]any) { m["connection_effect"] = "preserve_old" },
	}
	for _, key := range []string{"affected_apps", "affected_nodes", "affected_groups", "affected_rules"} {
		for _, kind := range []string{"duplicate", "unsorted", "overflow", "invalid"} {
			key, kind := key, kind
			edits[key+"-"+kind] = func(m map[string]any) {
				o := m
				if key != "affected_apps" {
					o = planV2Details(m)
				}
				a := o[key].([]any)
				switch kind {
				case "duplicate":
					a = append(a, a[0])
				case "unsorted":
					if key == "affected_nodes" {
						a[0], a[1] = a[1], a[0]
					} else {
						a = []any{"z", "a"}
					}
				case "invalid":
					if key == "affected_nodes" {
						a[0] = map[string]any{"subscription_id": "source", "node_id": "bad value"}
					} else {
						a[0] = "bad value"
					}
				case "overflow":
					max := 128
					if key == "affected_apps" {
						max = 6
					}
					a = []any{}
					for i := 0; i <= max; i++ {
						id := fmt.Sprintf("i%03d", i)
						if key == "affected_nodes" {
							a = append(a, map[string]any{"subscription_id": "s", "node_id": id})
						} else {
							a = append(a, id)
						}
					}
				}
				o[key] = a
			}
		}
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			obj := planV2Fixture(t)
			m := planV2Impact(t, obj)
			edit(m)
			obj["impact"] = planRaw(t, m)
			planV2Reject(t, obj)
		})
	}
	for _, variant := range []string{"start", "known_generation_union", "maxima"} {
		t.Run(variant, func(t *testing.T) {
			obj := planV2Fixture(t)
			m := planV2Impact(t, obj)
			d := planV2Details(m)
			switch variant {
			case "start":
				m["runtime_action"] = "start"
				m["previous_generation"] = nil
				d["existing_connections"] = "no_previous_generation"
				d["rollback_target"] = map[string]any{"kind": "stop_candidate", "generation": nil, "manifest_sha256": nil}
			case "known_generation_union":
				d["scope"] = "known_generation_union"
			case "maxima":
				for _, key := range []string{"affected_nodes", "affected_groups", "affected_rules"} {
					a := []any{}
					for i := 0; i < 128; i++ {
						id := fmt.Sprintf("i%03d", i)
						if key == "affected_nodes" {
							a = append(a, map[string]any{"subscription_id": "s", "node_id": id})
						} else {
							a = append(a, id)
						}
					}
					d[key] = a
				}
				m["affected_apps"] = []any{"a", "b", "c", "d", "e", "f"}
			}
			obj["impact"] = planRaw(t, m)
			p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil {
				t.Fatal("valid v2 variant", e)
			}
			if _, e = ParsePlan(p.Bytes(), planFixtureValidator(t, obj)); e != nil {
				t.Fatal(e)
			}
			if variant == "start" {
				d["existing_connections"] = "interrupt"
				obj["impact"] = planRaw(t, m)
				planV2Reject(t, obj)
				d["existing_connections"] = "unknown"
				m["connection_effect"] = "unknown"
				obj["impact"] = planRaw(t, m)
				planV2Reject(t, obj)
				d["existing_connections"] = "no_previous_generation"
				m["connection_effect"] = "interrupt"
				d["scope"] = "known_generation_union"
				obj["impact"] = planRaw(t, m)
				planV2Reject(t, obj)
				d["scope"] = "generation_union"
				d["rollback_target"] = map[string]any{"kind": "stop_candidate", "generation": "g1", "manifest_sha256": nil}
				obj["impact"] = planRaw(t, m)
				planV2Reject(t, obj)
			}
		})
	}
}
func TestPlanV2UnknownDisruptionNormalization(t *testing.T) {
	for _, key := range []string{"existing_connections", "new_connections"} {
		t.Run(key, func(t *testing.T) {
			obj := planV2Fixture(t)
			m := planV2Impact(t, obj)
			planV2Details(m)[key] = "unknown"
			m["connection_effect"] = "unknown"
			m["requires_disruption"] = false
			obj["impact"] = planRaw(t, m)
			p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil || !p.Document().Impact.RequiresDisruption {
				t.Fatal("unknown did not normalize disruption", e)
			}
			var forged map[string]json.RawMessage
			json.Unmarshal(p.Bytes(), &forged)
			bad := planV2Impact(t, forged)
			bad["requires_disruption"] = false
			forged["impact"] = planRaw(t, bad)
			digest, e := ComputePlanDigest(planRaw(t, forged))
			if e != nil {
				t.Fatal(e)
			}
			forged["plan_digest"] = planRaw(t, digest)
			if _, e = ParsePlan(planRaw(t, forged), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal("rehashed false disruption admitted", e)
			}
		})
	}
}
func TestPlanV2EveryDetailDigestAndCurrentInputBinding(t *testing.T) {
	obj := planV2Fixture(t)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	changes := map[string]func(map[string]any){"scope": func(d map[string]any) { d["scope"] = "known_generation_union" }, "node-subscription": func(d map[string]any) {
		planV2Object(d["affected_nodes"].([]any)[0])["subscription_id"] = "another-source"
	},
		"nodes": func(d map[string]any) { planV2Object(d["affected_nodes"].([]any)[0])["node_id"] = "new" }, "groups": func(d map[string]any) { d["affected_groups"] = []any{"G2"} }, "rules": func(d map[string]any) { d["affected_rules"] = []any{"R2"} }, "existing": func(d map[string]any) { d["existing_connections"] = "unknown" }, "new": func(d map[string]any) { d["new_connections"] = "unknown" }, "rollback-kind": func(d map[string]any) { planV2Object(d["rollback_target"])["kind"] = "stop_candidate" }, "rollback-generation": func(d map[string]any) { planV2Object(d["rollback_target"])["generation"] = "other" }, "rollback-hash": func(d map[string]any) {
			planV2Object(d["rollback_target"])["manifest_sha256"] = strings.Repeat("b", 64)
		}}
	for name, edit := range changes {
		t.Run(name, func(t *testing.T) {
			o := planV2Fixture(t)
			m := planV2Impact(t, o)
			edit(planV2Details(m))
			o["impact"] = planRaw(t, m)
			digest, e := ComputePlanDigest(planRaw(t, o))
			if e != nil || digest == p.Digest() {
				t.Fatal("nested detail escaped digest", e)
			}
			// Some one-field changes intentionally violate cross-field semantics. The
			// digest primitive still binds them; it does not establish Plan admission.
			var changed PlanImpact
			if json.Unmarshal(o["impact"], &changed) != nil {
				t.Fatal("fixture")
			}
			doc := p.Document()
			current := CurrentPlanInputs{CompilerVersion: doc.CompilerVersion, CapabilitiesDigest: doc.CapabilitiesDigest, Inputs: doc.Inputs, Impact: changed}
			if sameCurrentInputs(doc, current) {
				t.Fatal("nested detail escaped deep current-input comparison")
			}
		})
	}
	doc := p.Document()
	current := CurrentPlanInputs{CompilerVersion: doc.CompilerVersion, CapabilitiesDigest: doc.CapabilitiesDigest, Inputs: append([]PlanInput{}, doc.Inputs...), Impact: p.Document().Impact}
	if !reflect.DeepEqual(doc.Impact, current.Impact) || !sameCurrentInputs(doc, current) {
		t.Fatal("equal detached details rejected")
	}
}
