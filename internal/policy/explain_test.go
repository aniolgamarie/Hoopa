//go:build linux && amd64

package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"hoopa/internal/config"
)

func explainFixture(t *testing.T) compileFixture {
	t.Helper()
	f := rulesFixture(t)
	f.intent["rules"] = []any{
		rulesEntry("domain", rulesDomain("exact", "Example.TEST."), map[string]any{"kind": "group", "group_id": "US"}),
		rulesEntry("app", map[string]any{"kind": "app", "app_id": "app-A"}, rulesKind("direct")),
		rulesEntry("ip", map[string]any{"kind": "ip_cidr", "value": "192.0.2.99/24"}, rulesKind("reject")),
	}
	f.sync(t)
	return f
}
func explainGet(t *testing.T, f compileFixture, facts *RuleFacts) RuleExplanation {
	t.Helper()
	f.sync(t)
	r, e := ExplainRules(f.input.Intent, f.input.Sources, facts)
	if e != nil || r.Validation != "valid" || r.Basis != "configuration_intent" || r.Evidence != "not_observed" || r.Blocker != nil {
		t.Fatal("valid explanation refused", e)
	}
	return r
}
func TestExplainOriginalOrderAndSuppliedFacts(t *testing.T) {
	f := explainFixture(t)
	r := explainGet(t, f, nil)
	if r.Calculation != nil || r.CalculationBasis != "" || r.ShadowAnalysis != "complete" || len(r.Rules) != 3 || r.DefaultTarget.Kind != "reject" {
		t.Fatal("static report invented calculation")
	}
	for i, id := range []config.ID{"domain", "app", "ip"} {
		if r.Rules[i].Index != i || r.Rules[i].ID != id || r.Rules[i].ShadowProof != "not_proven" || r.Rules[i].ShadowedBy != nil {
			t.Fatal("original order lost")
		}
	}
	if !reflect.DeepEqual(r.Rules[0].Fields, []string{"domain"}) || !reflect.DeepEqual(r.Rules[1].Fields, []string{"app_id"}) || !reflect.DeepEqual(r.Rules[2].Fields, []string{"ip"}) {
		t.Fatal("wrong logical fields")
	}
	facts := RuleFacts{Domain: rulesKnown("example.test"), AppID: rulesKnown("app-A"), IP: rulesKnown("192.0.2.1")}
	r = explainGet(t, f, &facts)
	if r.CalculationBasis != "supplied_facts" || r.Calculation.Outcome != "matched" || r.Calculation.RuleIndex != 0 || r.Calculation.Target.GroupID == nil || *r.Calculation.Target.GroupID != "US" {
		t.Fatal("domain did not retain original priority")
	}
	rs := f.intent["rules"].([]any)
	rs[0], rs[1] = rs[1], rs[0]
	f.sync(t)
	r = explainGet(t, f, &facts)
	if *r.Calculation.RuleID != "app" || r.Calculation.Target.Kind != "direct" {
		t.Fatal("reordering declaration did not reorder decision")
	}
	facts = RuleFacts{Domain: rulesKnown(""), AppID: rulesKnown(""), IP: rulesKnown("203.0.113.1")}
	r = explainGet(t, f, &facts)
	if r.Calculation.Outcome != "default" || r.Calculation.RuleIndex != -1 || r.Calculation.RuleID != nil {
		t.Fatal("known empty confused with unavailable")
	}
}
func TestExplainShadowProofSubsetAndCounterexamples(t *testing.T) {
	app := map[string]any{"kind": "app", "app_id": "app-A"}
	domain := rulesDomain("exact", "example.test")
	cases := []struct {
		name         string
		first, later any
		proof        string
	}{
		{"normalized-domain", rulesDomain("exact", "EXAMPLE.TEST."), domain, "duplicate"},
		{"masked-ip", map[string]any{"kind": "ip_cidr", "value": "192.0.2.99/24"}, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, "duplicate"},
		{"conjunction-order-duplicates", rulesAll(app, domain), rulesAll(domain, rulesAll(app, domain)), "duplicate"},
		{"atom-in-all", app, rulesAll(domain, app), "conjunction_subset"},
		{"all-subset", rulesAll(domain, app), rulesAll(app, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, domain), "conjunction_subset"},
		{"no-suffix-containment", rulesDomain("suffix", "example.test"), rulesDomain("exact", "a.example.test"), "not_proven"},
		{"no-subnet-containment", map[string]any{"kind": "ip_cidr", "value": "192.0.0.0/16"}, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, "not_proven"},
		{"path-case", map[string]any{"kind": "process_path", "value": "/opt/App"}, map[string]any{"kind": "process_path", "value": "/opt/app"}, "not_proven"},
		{"name-case", map[string]any{"kind": "process_name", "value": "App"}, map[string]any{"kind": "process_name", "value": "app"}, "not_proven"},
		{"exact-v-suffix", domain, rulesDomain("suffix", "example.test"), "not_proven"},
		{"reverse-subset", rulesAll(domain, app), app, "not_proven"},
		{"cross-field", domain, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, "not_proven"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := rulesFixture(t)
			f.intent["rules"] = []any{rulesEntry("first", tt.first, rulesKind("reject")), rulesEntry("later", tt.later, rulesKind("direct"))}
			r := explainGet(t, f, nil)
			if r.Rules[1].ShadowProof != tt.proof {
				t.Fatalf("proof=%s want=%s", r.Rules[1].ShadowProof, tt.proof)
			}
			if tt.proof == "not_proven" {
				if r.Rules[1].ShadowedBy != nil {
					t.Fatal("false proof witness")
				}
			} else if r.Rules[1].ShadowedBy == nil || *r.Rules[1].ShadowedBy != 0 {
				t.Fatal("original earlier index missing")
			}
		})
	}
	f := rulesFixture(t)
	f.intent["rules"] = []any{rulesEntry("first", app, rulesKind("reject")), rulesEntry("second", app, rulesKind("direct")), rulesEntry("third", rulesAll(app, domain), rulesKind("direct"))}
	r := explainGet(t, f, nil)
	if *r.Rules[2].ShadowedBy != 0 {
		t.Fatal("did not choose first earlier proof")
	}
}
func TestExplainUnknownAndMalformedUsedFacts(t *testing.T) {
	f := explainFixture(t)
	facts := RuleFacts{Domain: RuleValue{Value: "stale-secret-domain"}, AppID: rulesKnown("app-A")}
	r := explainGet(t, f, &facts)
	if r.Calculation.Outcome != "blocked" || r.Calculation.RuleIndex != 0 || r.Calculation.Target.Kind != "reject" || !reflect.DeepEqual(r.Calculation.UnknownFields, []string{"domain"}) || r.Rules[0].Target.Kind != "group" {
		t.Fatal("unknown fell through or overwrote intent target")
	}
	for _, reverse := range []bool{false, true} {
		children := []any{rulesDomain("exact", "false.test"), map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}}
		if reverse {
			children[0], children[1] = children[1], children[0]
		}
		f.intent["rules"] = []any{rulesEntry("all", rulesAll(children...), rulesKind("reject")), rulesEntry("next", map[string]any{"kind": "app", "app_id": "app-A"}, rulesKind("direct"))}
		f.sync(t)
		facts = RuleFacts{Domain: rulesKnown("other.test"), AppID: rulesKnown("app-A"), IP: RuleValue{Value: "stale-bad-ip"}}
		r = explainGet(t, f, &facts)
		if r.Calculation.RuleIndex != 1 {
			t.Fatal("false did not dominate unknown")
		}
		facts.IP = rulesKnown("not-an-ip")
		bad, e := ExplainRules(f.input.Intent, f.input.Sources, &facts)
		if !errors.Is(e, ErrRules) || !reflect.DeepEqual(bad, RuleExplanation{}) {
			t.Fatal("false hid malformed known fact")
		}
	}
	f = explainFixture(t)
	facts = RuleFacts{Domain: rulesKnown("example.test"), IP: rulesKnown("bad-unused-ip")}
	if explainGet(t, f, &facts).Calculation.RuleIndex != 0 {
		t.Fatal("later unused fact became dependency")
	}
	f = rulesFixture(t)
	f.intent["rules"] = []any{rulesEntry("fields", rulesAll(map[string]any{"kind": "process_path", "value": "/x"}, map[string]any{"kind": "app", "app_id": "app-A"}, rulesDomain("exact", "x.test"), rulesDomain("exact", "x.test")), rulesKind("direct"))}
	r = explainGet(t, f, &RuleFacts{})
	if !reflect.DeepEqual(r.Rules[0].Fields, []string{"app_id", "domain", "process_path"}) || !reflect.DeepEqual(r.Calculation.UnknownFields, r.Rules[0].Fields) {
		t.Fatal("logical fields not sorted unique")
	}
}
func TestExplainInvalidReferencesAuthorityAndTargets(t *testing.T) {
	cases := []struct {
		name, reason, field string
		edit                func(*compileFixture)
	}{
		{"missing-group", "target_group_missing", "rules[domain].target", func(f *compileFixture) {
			compileObj(f.intent["rules"].([]any)[0])["target"] = map[string]any{"kind": "group", "group_id": "absent"}
		}},
		{"source-reference", "source_reference_missing", "selection.active_subscription", func(f *compileFixture) { f.intent["selection"] = map[string]any{"active_subscription": "absent"} }},
		{"app-reference", "app_reference_missing", "rules[app].match.app_id", func(f *compileFixture) {
			compileObj(compileObj(f.intent["rules"].([]any)[1])["match"])["app_id"] = "absent"
		}},
		{"follow-missing", "follow_target_missing", "groups[US-follow].follows", func(f *compileFixture) { compileIntentGroup(f.intent, "US-follow")["follows"] = "absent" }},
		{"follow-cycle", "follow_cycle", "groups[US-follow].follows", func(f *compileFixture) { compileIntentGroup(f.intent, "US-follow")["follows"] = "US-follow" }},
		{"required-region", "required_group_no_region_match", "groups[US].region", func(f *compileFixture) { compileIntentGroup(f.intent, "US")["region"] = "ZZ" }},
		{"active-unset", "active_source_unset", "groups[US].source", func(f *compileFixture) { f.intent["selection"] = map[string]any{"active_subscription": nil} }},
		{"missing-source", "source_unavailable", "selection.active_subscription", func(f *compileFixture) { delete(f.input.Sources, "a") }},
		{"bad-authority", "source_unavailable", "selection.active_subscription", func(f *compileFixture) {
			s := f.input.Sources["a"]
			s.Nodes[0].DisplayName = "rewritten"
			f.input.Sources["a"] = s
		}},
		{"manual-pin", "selected_node_missing", "groups[pinned-A].selected_node_id", func(f *compileFixture) { compileIntentGroup(f.intent, "pinned-A")["selected_node_id"] = "absent" }},
		{"default-pin", "pinned_node_missing", "default_target", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "a", "node_id": "absent"}
		}},
		{"shadowed-pin", "pinned_node_missing", "rules[later].target", func(f *compileFixture) {
			f.intent["rules"] = []any{rulesEntry("first", rulesDomain("exact", "x.test"), rulesKind("direct")), rulesEntry("later", rulesDomain("exact", "x.test"), map[string]any{"kind": "node", "subscription_id": "a", "node_id": "absent"})}
		}},
		{"wrong-source-pin", "pinned_node_missing", "default_target", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": compileNode(f.input.Sources["a"], "US shared")}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := explainFixture(t)
			tt.edit(&f)
			f.sync(t)
			facts := RuleFacts{Domain: rulesKnown("x.test")}
			r, e := ExplainRules(f.input.Intent, f.input.Sources, &facts)
			if e != nil || r.Validation != "invalid" || r.Blocker == nil || r.Blocker.Reason != tt.reason || r.Blocker.Field != tt.field || r.Calculation != nil || r.CalculationBasis != "" || r.ShadowAnalysis != "not_performed" {
				t.Fatal("invalid report wrong blocker", e, r.Blocker)
			}
			for _, row := range r.Rules {
				if row.ShadowedBy != nil || row.ShadowProof != "not_proven" {
					t.Fatal("invalid candidate received shadow admission")
				}
			}
			rulesAssertInvalid(t, f.input.Intent, f.input.Sources)
		})
	}
	f := explainFixture(t)
	f.input.Sources["unused"] = f.input.Sources["a"]
	r := explainGet(t, f, nil)
	if r.Validation != "valid" {
		t.Fatal("unused source became dependency")
	}
}
func TestExplainMalformedZeroAndPrivateFormatting(t *testing.T) {
	f := explainFixture(t)
	for _, raw := range [][]byte{[]byte(`null`), []byte(`{"schema_version":1,"schema_version":1}`), append(bytes.Clone(f.input.Intent), []byte(` {}`)...), []byte("\xff")} {
		r, e := ExplainRules(raw, f.input.Sources, nil)
		if !errors.Is(e, ErrRules) || !reflect.DeepEqual(r, RuleExplanation{}) {
			t.Fatal("malformed input received report")
		}
	}
	for _, predicate := range []any{rulesDomain("exact", "https://private.invalid"), map[string]any{"kind": "ip_cidr", "value": "invalid-cidr"}} {
		f := explainFixture(t)
		f.intent["rules"] = []any{rulesEntry("bad", predicate, rulesKind("direct"))}
		f.sync(t)
		if _, e := config.ParseIntent(f.input.Intent, config.IntentDraft); e != nil {
			t.Fatal("not a Draft-only malformed scalar fixture")
		}
		r, e := ExplainRules(f.input.Intent, f.input.Sources, nil)
		if e != ErrRules || !reflect.DeepEqual(r, RuleExplanation{}) {
			t.Fatal("Draft lenience admitted malformed Apply scalar")
		}
		f.intent["selection"] = map[string]any{"active_subscription": "also-absent"}
		f.sync(t)
		r, e = ExplainRules(f.input.Intent, f.input.Sources, nil)
		if e != ErrRules || !reflect.DeepEqual(r, RuleExplanation{}) {
			t.Fatal("missing reference hid malformed scalar")
		}
	}
	badProbe := explainFixture(t)
	compileObj(compileIntentGroup(badProbe.intent, "US")["probe"])["url"] = "https://bad.invalid:0/"
	badProbe.intent["selection"] = map[string]any{"active_subscription": "absent"}
	badProbe.sync(t)
	bad, e := ExplainRules(badProbe.input.Intent, badProbe.input.Sources, nil)
	if e != ErrRules || !reflect.DeepEqual(bad, RuleExplanation{}) {
		t.Fatal("missing reference hid malformed probe")
	}
	r := explainGet(t, f, nil)
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"edge.example.test", "EXAMPLE.TEST", "fixed-source-core", "url_secret_ref", "username", "password", "interval_seconds"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private declaration material escaped", secret)
		}
	}
	for _, value := range []any{r, &r} {
		for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, value); got != "policy.RuleExplanation{private material}" {
				t.Fatal("aggregate formatting leaked", got)
			}
		}
	}
}
func TestExplainOwnedResultsAndBudget(t *testing.T) {
	f := explainFixture(t)
	facts := RuleFacts{Domain: rulesKnown("example.test")}
	first := explainGet(t, f, &facts)
	*first.Rules[0].Target.GroupID = "changed"
	if *first.Calculation.Target.GroupID != "US" {
		t.Fatal("calculation aliases displayed target")
	}
	first.Rules[0].Fields[0] = "changed"
	*first.Calculation.RuleID = "changed"
	*first.Calculation.Target.GroupID = "changed"
	facts.Domain.Value = "changed.test"
	r := explainGet(t, f, nil)
	if *r.Rules[0].Target.GroupID != "US" || r.Rules[0].Fields[0] != "domain" {
		t.Fatal("returned report shares mutable state")
	}
	for _, atoms := range []int{1, 32} {
		t.Run(fmt.Sprint(atoms), func(t *testing.T) {
			f := rulesFixture(t)
			rules := []any{}
			count := 100
			if atoms == 32 {
				count = 70
			}
			for i := 0; i < count; i++ {
				var p any = map[string]any{"kind": "process_name", "value": fmt.Sprintf("z%03d", i)}
				if atoms == 1 && i == 1 {
					p = map[string]any{"kind": "process_name", "value": "z000"}
				}
				if atoms > 1 {
					children := []any{}
					for j := 0; j < atoms-1; j++ {
						children = append(children, map[string]any{"kind": "process_name", "value": fmt.Sprintf("a%02d", j)})
					}
					children = append(children, p)
					p = rulesAll(children...)
				}
				rules = append(rules, rulesEntry(fmt.Sprintf("r%d", i), p, rulesKind("direct")))
			}
			f.intent["rules"] = rules
			f.sync(t)
			facts := RuleFacts{ProcessName: rulesKnown("does-not-match")}
			r := explainGet(t, f, &facts)
			if r.ShadowAnalysis != "budget_exhausted" || len(r.Rules) != count || r.Calculation.Outcome != "default" {
				t.Fatal("proof budget changed listing/admission/calculation", r.ShadowAnalysis)
			}
			for index, row := range r.Rules {
				if atoms == 1 && index == 1 {
					if row.ShadowedBy == nil || *row.ShadowedBy != 0 || row.ShadowProof != "duplicate" {
						t.Fatal("budget lost completed proof")
					}
					continue
				}
				if row.ShadowedBy != nil || row.ShadowProof != "not_proven" {
					t.Fatal("unproved relationships marked shadowed")
				}
			}
		})
	}
}
