//go:build linux && amd64

package policy

// T045 definitions exercise a pure evaluator with supplied facts. Providers are
// synthetic bytes parsed into genuine validated snapshots, not network nodes.
// These tests define the frozen T047 API; no OS/process capability is implied.
import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func rulesFixture(t *testing.T) compileFixture {
	t.Helper()
	f := newCompileFixture(t)
	for _, s := range f.input.Sources {
		if s.Validate() != nil {
			t.Fatal("provider fixture lacks authority")
		}
	}
	f.intent["rules"] = []any{}
	f.intent["default_target"] = map[string]any{"kind": "reject"}
	f.intent["apps"] = []any{map[string]any{"id": "app-A", "mode": "inherit", "metering": false, "bypass": []any{}}}
	f.sync(t)
	return f
}
func rulesDomain(op, value string) map[string]any {
	return map[string]any{"kind": "domain", "operator": op, "value": value}
}
func rulesAll(children ...any) map[string]any {
	return map[string]any{"kind": "all", "conditions": children}
}
func rulesEntry(id string, predicate, target any) map[string]any {
	return map[string]any{"id": id, "match": predicate, "target": target}
}
func rulesKind(kind string) map[string]any { return map[string]any{"kind": kind} }
func rulesKnown(s string) RuleValue        { return RuleValue{Known: true, Value: s} }
func rulesSet(t *testing.T, f *compileFixture) RuleSet {
	t.Helper()
	f.sync(t)
	if _, e := config.ParseIntent(f.input.Intent, config.IntentApply); e != nil {
		t.Fatal("invalid Apply fixture", e)
	}
	set, e := NewRuleSet(f.input.Intent, f.input.Sources)
	if e != nil {
		t.Fatal("valid pure RuleSet refused", e)
	}
	return set
}
func rulesDecision(t *testing.T, set RuleSet, facts RuleFacts, outcome string, index int, id string, target config.Target, unknown ...string) RuleDecision {
	t.Helper()
	got, e := set.Evaluate(facts)
	if e != nil {
		t.Fatal("Evaluate", e)
	}
	if got.Outcome != outcome || got.RuleIndex != index || !reflect.DeepEqual(got.Target, target) {
		t.Fatalf("decision mismatch: %#v", got)
	}
	if id == "" {
		if got.RuleID != nil {
			t.Fatal("default has rule ID")
		}
	} else if got.RuleID == nil || string(*got.RuleID) != id {
		t.Fatal("wrong original rule ID")
	}
	if len(got.UnknownFields) != len(unknown) {
		t.Fatal("wrong missing fields", got.UnknownFields, unknown)
	}
	for i := range unknown {
		if got.UnknownFields[i] != unknown[i] {
			t.Fatal("missing fields not sorted/deduplicated", got.UnknownFields)
		}
	}
	return got
}
func rulesAssertInvalid(t *testing.T, raw []byte, sources map[config.ID]subscription.Snapshot) {
	t.Helper()
	set, e := NewRuleSet(raw, sources)
	if !errors.Is(e, ErrRules) || !reflect.DeepEqual(set, RuleSet{}) {
		t.Fatal("invalid declaration returned usable/partial RuleSet", e)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, e); got != "RULES_INVALID" {
			t.Fatal("error leaked private input", got)
		}
	}
	decision, err := set.Evaluate(RuleFacts{})
	if !errors.Is(err, ErrRules) || !reflect.DeepEqual(decision, RuleDecision{}) {
		t.Fatal("failed set remained usable")
	}
}

func TestRulesConstructorStrictAndConstantErrors(t *testing.T) {
	f := rulesFixture(t)
	for name, raw := range map[string][]byte{
		"syntax": []byte(`{"private-provider-secret":`), "null": []byte(`null`),
		"unknown":   bytes.Replace(f.input.Intent, []byte(`"schema_version":1`), []byte(`"schema_version":1,"private-provider-secret":true`), 1),
		"duplicate": bytes.Replace(f.input.Intent, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1),
		"case-fold": bytes.Replace(f.input.Intent, []byte(`"schema_version"`), []byte(`"Schema_Version"`), 1),
		"trailing":  append(bytes.Clone(f.input.Intent), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) { rulesAssertInvalid(t, raw, f.input.Sources) })
	}
	for name, edit := range map[string]func(map[string]any){
		"incomplete-draft":  func(m map[string]any) { compileObj(m["selection"])["active_subscription"] = "missing" },
		"unknown-predicate": func(m map[string]any) { m["rules"] = []any{rulesEntry("r", rulesKind("always"), rulesKind("direct"))} },
		"union-extra": func(m map[string]any) {
			m["rules"] = []any{rulesEntry("r", map[string]any{"kind": "domain", "operator": "exact", "value": "example.test", "app_id": "app-A"}, rulesKind("direct"))}
		},
		"empty-all": func(m map[string]any) {
			m["rules"] = []any{rulesEntry("r", map[string]any{"kind": "all", "conditions": []any{}}, rulesKind("direct"))}
		},
		"null-array": func(m map[string]any) { m["rules"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := rulesFixture(t)
			edit(f.intent)
			f.sync(t)
			rulesAssertInvalid(t, f.input.Intent, f.input.Sources)
		})
	}
}

func TestRulesConstructorValidatesEveryTargetAndReferencedAuthority(t *testing.T) {
	cases := []struct {
		name string
		edit func(*compileFixture)
	}{
		{"shadowed-pin", func(f *compileFixture) {
			f.intent["rules"] = []any{rulesEntry("first", rulesDomain("exact", "example.test"), rulesKind("direct")), rulesEntry("shadowed", rulesDomain("exact", "example.test"), map[string]any{"kind": "node", "subscription_id": "a", "node_id": "missing"})}
		}},
		{"default-pin", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": "missing"}
		}},
		{"cross-source-same-name", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "a", "node_id": compileNode(f.input.Sources["b"], "US shared")}
		}},
		{"missing-group", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "group", "group_id": "missing"}
		}},
		{"missing-source", func(f *compileFixture) {
			delete(f.input.Sources, "b")
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": "missing"}
		}},
		{"required-empty", func(f *compileFixture) {
			f.input.Sources["a"] = compileSource(t, "a", []byte(`{"proxies":[]}`), f.rules, 0x71)
		}},
		{"wrong-source-authority", func(f *compileFixture) { f.input.Sources["a"] = f.input.Sources["b"] }},
		{"forged-metadata", func(f *compileFixture) {
			s := f.input.Sources["a"]
			s.Nodes[0].Region.Code = "ZZ"
			f.input.Sources["a"] = s
		}},
		{"forged-snapshot", func(f *compileFixture) { s := f.input.Sources["a"]; s.ID = "forged"; f.input.Sources["a"] = s }},
		{"missing-active", func(f *compileFixture) { delete(f.input.Sources, "a") }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := rulesFixture(t)
			tt.edit(&f)
			f.sync(t)
			rulesAssertInvalid(t, f.input.Intent, f.input.Sources)
		})
	}
	t.Run("unused-corruption-not-dependency", func(t *testing.T) {
		f := rulesFixture(t)
		f.input.Sources["unused"] = subscription.Snapshot{}
		set := rulesSet(t, &f)
		rulesDecision(t, set, RuleFacts{}, "default", -1, "", config.Target{Kind: "reject"})
	})
	t.Run("exact-node-and-logical-auto-target", func(t *testing.T) {
		f := rulesFixture(t)
		node := compileNode(f.input.Sources["a"], "US shared")
		f.intent["rules"] = []any{rulesEntry("pin", rulesDomain("exact", "pin.test"), map[string]any{"kind": "node", "subscription_id": "a", "node_id": node}), rulesEntry("auto", rulesDomain("exact", "auto.test"), map[string]any{"kind": "group", "group_id": "US"})}
		set := rulesSet(t, &f)
		rulesDecision(t, set, RuleFacts{Domain: rulesKnown("pin.test")}, "matched", 0, "pin", config.Target{Kind: "node", SubscriptionID: groupID("a"), NodeID: &node})
		rulesDecision(t, set, RuleFacts{Domain: rulesKnown("auto.test")}, "matched", 1, "auto", config.Target{Kind: "group", GroupID: groupID("US")})
	})
}

func TestRulesArrayOrderDomainIPAppAndDefault(t *testing.T) {
	for _, order := range [][]string{{"domain", "ip", "app"}, {"app", "domain", "ip"}, {"ip", "app", "domain"}} {
		t.Run(strings.Join(order, "-"), func(t *testing.T) {
			f := rulesFixture(t)
			pred := map[string]any{"domain": rulesDomain("suffix", "EXAMPLE.TEST."), "ip": map[string]any{"kind": "ip_cidr", "value": "192.0.2.129/24"}, "app": map[string]any{"kind": "app", "app_id": "app-A"}}
			entries := []any{}
			for _, id := range order {
				entries = append(entries, rulesEntry(id, pred[id], map[string]any{"kind": "group", "group_id": map[string]string{"domain": "US", "ip": "JP", "app": "BEST"}[id]}))
			}
			f.intent["rules"] = entries
			f.intent["default_target"] = rulesKind("direct")
			set := rulesSet(t, &f)
			facts := RuleFacts{Domain: rulesKnown("child.example.test"), IP: rulesKnown("192.0.2.4"), AppID: rulesKnown("app-A")}
			rulesDecision(t, set, facts, "matched", 0, order[0], config.Target{Kind: "group", GroupID: groupID(config.ID(map[string]string{"domain": "US", "ip": "JP", "app": "BEST"}[order[0]]))})
			for winner := 1; winner < len(order); winner++ {
				switch order[winner-1] {
				case "domain":
					facts.Domain = rulesKnown("other.test")
				case "ip":
					facts.IP = rulesKnown("198.51.100.1")
				case "app":
					facts.AppID = rulesKnown("")
				}
				rulesDecision(t, set, facts, "matched", winner, order[winner], config.Target{Kind: "group", GroupID: groupID(config.ID(map[string]string{"domain": "US", "ip": "JP", "app": "BEST"}[order[winner]]))})
			}
			facts = RuleFacts{Domain: rulesKnown("other.test"), IP: rulesKnown("198.51.100.1"), AppID: rulesKnown("")}
			rulesDecision(t, set, facts, "default", -1, "", config.Target{Kind: "direct"})
		})
	}
	t.Run("explicit-reject-is-match", func(t *testing.T) {
		f := rulesFixture(t)
		f.intent["rules"] = []any{rulesEntry("deny", rulesDomain("exact", "deny.test"), rulesKind("reject"))}
		f.intent["default_target"] = rulesKind("direct")
		set := rulesSet(t, &f)
		rulesDecision(t, set, RuleFacts{Domain: rulesKnown("deny.test")}, "matched", 0, "deny", config.Target{Kind: "reject"})
	})
}

func TestRulesAppModeCannotOverrideOrderedTargetAndExactDefault(t *testing.T) {
	f := rulesFixture(t)
	f.intent["apps"] = []any{map[string]any{"id": "app-A", "mode": "direct", "metering": false, "bypass": []any{}}}
	f.intent["rules"] = []any{rulesEntry("domain-route", rulesDomain("exact", "example.test"), map[string]any{"kind": "group", "group_id": "JP"})}
	set := rulesSet(t, &f)
	rulesDecision(t, set, RuleFacts{AppID: rulesKnown("app-A"), Domain: rulesKnown("example.test")}, "matched", 0, "domain-route", config.Target{Kind: "group", GroupID: groupID("JP")})
	rulesDecision(t, set, RuleFacts{AppID: rulesKnown("app-A"), Domain: rulesKnown("other.test")}, "default", -1, "", config.Target{Kind: "reject"})
	node := compileNode(f.input.Sources["b"], "JP shared")
	f.intent["rules"] = []any{}
	f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": node}
	set = rulesSet(t, &f)
	rulesDecision(t, set, RuleFacts{}, "default", -1, "", config.Target{Kind: "node", SubscriptionID: groupID("b"), NodeID: &node})
}

func TestRulesDomainAndAddressMatching(t *testing.T) {
	cases := []struct {
		name      string
		predicate any
		facts     RuleFacts
		want      RuleTruth
	}{
		{"exact-normalization", rulesDomain("exact", "EXAMPLE.TEST."), RuleFacts{Domain: rulesKnown("Example.Test.")}, RuleTrue},
		{"exact-no-child", rulesDomain("exact", "example.test"), RuleFacts{Domain: rulesKnown("a.example.test")}, RuleFalse},
		{"suffix-self", rulesDomain("suffix", "example.test"), RuleFacts{Domain: rulesKnown("example.test")}, RuleTrue},
		{"suffix-child", rulesDomain("suffix", "example.test"), RuleFacts{Domain: rulesKnown("A.B.EXAMPLE.TEST.")}, RuleTrue},
		{"suffix-boundary", rulesDomain("suffix", "example.test"), RuleFacts{Domain: rulesKnown("notexample.test")}, RuleFalse},
		{"suffix-not-superdomain", rulesDomain("suffix", "example.test"), RuleFacts{Domain: rulesKnown("test")}, RuleFalse},
		{"ipv4-hostbits", map[string]any{"kind": "ip_cidr", "value": "192.0.2.129/24"}, RuleFacts{IP: rulesKnown("192.0.2.0")}, RuleTrue},
		{"ipv4-outside", map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, RuleFacts{IP: rulesKnown("192.0.3.1")}, RuleFalse},
		{"ipv6-pure-facts", map[string]any{"kind": "ip_cidr", "value": "2001:db8::/32"}, RuleFacts{IP: rulesKnown("2001:db8:1::1")}, RuleTrue},
		{"address-family", map[string]any{"kind": "ip_cidr", "value": "2001:db8::/32"}, RuleFacts{IP: rulesKnown("192.0.2.1")}, RuleFalse},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := rulesFixture(t)
			f.intent["rules"] = []any{rulesEntry("one", tt.predicate, rulesKind("direct"))}
			set := rulesSet(t, &f)
			got, e := set.Match(0, tt.facts)
			if e != nil || got != tt.want {
				t.Fatal("predicate", got, e)
			}
		})
	}
}

func TestRulesAllThreeValuedTruthTableBothOrders(t *testing.T) {
	// The entire nine-row table, with each pair placed in both child orders.
	rows := []struct{ left, right, want RuleTruth }{{RuleTrue, RuleTrue, RuleTrue}, {RuleTrue, RuleFalse, RuleFalse}, {RuleTrue, RuleUnknown, RuleUnknown}, {RuleFalse, RuleTrue, RuleFalse}, {RuleFalse, RuleFalse, RuleFalse}, {RuleFalse, RuleUnknown, RuleFalse}, {RuleUnknown, RuleTrue, RuleUnknown}, {RuleUnknown, RuleFalse, RuleFalse}, {RuleUnknown, RuleUnknown, RuleUnknown}}
	fact := func(truth RuleTruth, yes string) RuleValue {
		switch truth {
		case RuleTrue:
			return rulesKnown(yes)
		case RuleFalse:
			return rulesKnown("different")
		default:
			return RuleValue{Known: false, Value: yes}
		}
	}
	for _, row := range rows {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-%s-reverse-%v", row.left, row.right, reverse), func(t *testing.T) {
				f := rulesFixture(t)
				children := []any{rulesDomain("exact", "example.test"), map[string]any{"kind": "process_name", "value": "worker"}}
				if reverse {
					children[0], children[1] = children[1], children[0]
				}
				f.intent["rules"] = []any{rulesEntry("all", rulesAll(children...), rulesKind("direct"))}
				set := rulesSet(t, &f)
				facts := RuleFacts{Domain: fact(row.left, "example.test"), ProcessName: fact(row.right, "worker")}
				got, e := set.Match(0, facts)
				if e != nil || got != row.want {
					t.Fatal("all truth", got, e)
				}
				switch row.want {
				case RuleTrue:
					rulesDecision(t, set, facts, "matched", 0, "all", config.Target{Kind: "direct"})
				case RuleFalse:
					rulesDecision(t, set, facts, "default", -1, "", config.Target{Kind: "reject"})
				default:
					unknown := []string{}
					if row.left == RuleUnknown {
						unknown = append(unknown, "domain")
					}
					if row.right == RuleUnknown {
						unknown = append(unknown, "process_name")
					}
					rulesDecision(t, set, facts, "blocked", 0, "all", config.Target{Kind: "reject"}, unknown...)
				}
			})
		}
	}
}

func TestRulesUnknownBeforeLaterTrueAndMissingFieldSet(t *testing.T) {
	f := rulesFixture(t)
	f.intent["rules"] = []any{rulesEntry("unknown-first", map[string]any{"kind": "process_name", "value": "worker"}, rulesKind("direct")), rulesEntry("later-true", rulesDomain("exact", "example.test"), map[string]any{"kind": "group", "group_id": "US"})}
	f.intent["default_target"] = rulesKind("direct")
	set := rulesSet(t, &f)
	facts := RuleFacts{AppID: rulesKnown("app-A"), Domain: rulesKnown("example.test"), ProcessName: RuleValue{Known: false, Value: "worker"}}
	rulesDecision(t, set, facts, "blocked", 0, "unknown-first", config.Target{Kind: "reject"}, "process_name")
	// Nest and repeat missing fields in deliberately nonlexical order.
	f.intent["rules"] = []any{rulesEntry("all-missing", rulesAll(map[string]any{"kind": "process_path", "value": "/opt/worker"}, map[string]any{"kind": "process_name", "value": "worker"}, map[string]any{"kind": "process_instance", "process_instance_id": "instance-A"}, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, rulesAll(rulesDomain("exact", "example.test"), map[string]any{"kind": "app", "app_id": "app-A"}, rulesDomain("exact", "example.test"))), rulesKind("direct"))}
	set = rulesSet(t, &f)
	rulesDecision(t, set, RuleFacts{}, "blocked", 0, "all-missing", config.Target{Kind: "reject"}, "app_id", "domain", "ip", "process_instance_id", "process_name", "process_path")
}

func TestRulesKnownAbsentDiffersFromUnknownAndStaleValues(t *testing.T) {
	cases := []struct {
		name, field     string
		predicate       any
		unknown, absent RuleFacts
	}{
		{"app", "app_id", map[string]any{"kind": "app", "app_id": "app-A"}, RuleFacts{AppID: RuleValue{Known: false, Value: "app-A"}}, RuleFacts{AppID: rulesKnown("")}},
		{"IP-only-domain", "domain", rulesDomain("exact", "example.test"), RuleFacts{Domain: RuleValue{Known: false, Value: "example.test"}}, RuleFacts{Domain: rulesKnown("")}},
		{"name-stale", "process_name", map[string]any{"kind": "process_name", "value": "worker"}, RuleFacts{ProcessName: RuleValue{Known: false, Value: "worker"}}, RuleFacts{ProcessName: rulesKnown("other")}},
		{"path-stale", "process_path", map[string]any{"kind": "process_path", "value": "/opt/worker"}, RuleFacts{ProcessPath: RuleValue{Known: false, Value: "/opt/worker"}}, RuleFacts{ProcessPath: rulesKnown("/opt/other")}},
		{"instance-stale", "process_instance_id", map[string]any{"kind": "process_instance", "process_instance_id": "instance-A"}, RuleFacts{ProcessInstanceID: RuleValue{Known: false, Value: "instance-A"}}, RuleFacts{ProcessInstanceID: rulesKnown("instance-B")}},
		{"ip-stale", "ip", map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, RuleFacts{IP: RuleValue{Known: false, Value: "192.0.2.3"}}, RuleFacts{IP: rulesKnown("198.51.100.3")}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := rulesFixture(t)
			f.intent["rules"] = []any{rulesEntry("one", tt.predicate, rulesKind("direct"))}
			set := rulesSet(t, &f)
			rulesDecision(t, set, tt.unknown, "blocked", 0, "one", config.Target{Kind: "reject"}, tt.field)
			rulesDecision(t, set, tt.absent, "default", -1, "", config.Target{Kind: "reject"})
		})
	}
}

func TestRulesEvidenceEqualityValidationAndUnusedFacts(t *testing.T) {
	// Invalid known evidence is an input error, not an unknown truth-table
	// operand. A false sibling cannot hide it in either child order.
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("all-invalid-known-fact-reverse-%v", reverse), func(t *testing.T) {
			f := rulesFixture(t)
			children := []any{rulesDomain("exact", "example.test"), map[string]any{"kind": "process_name", "value": "worker"}}
			if reverse {
				children[0], children[1] = children[1], children[0]
			}
			f.intent["rules"] = []any{rulesEntry("invalid-known", rulesAll(children...), rulesKind("direct"))}
			set := rulesSet(t, &f)
			facts := RuleFacts{Domain: rulesKnown("other.test"), ProcessName: rulesKnown("")}
			truth, err := set.Match(0, facts)
			if !errors.Is(err, ErrRules) || truth != "" {
				t.Fatal("false sibling hid invalid known fact", truth, err)
			}
			decision, err := set.Evaluate(facts)
			if !errors.Is(err, ErrRules) || !reflect.DeepEqual(decision, RuleDecision{}) {
				t.Fatal("invalid known fact selected a route", decision, err)
			}
		})
	}
	cases := []struct {
		name                      string
		predicate                 any
		equal, unequal, malformed RuleFacts
	}{
		{"app", map[string]any{"kind": "app", "app_id": "app-A"}, RuleFacts{AppID: rulesKnown("app-A")}, RuleFacts{AppID: rulesKnown("App-A")}, RuleFacts{AppID: rulesKnown("bad app")}},
		{"instance", map[string]any{"kind": "process_instance", "process_instance_id": "instance-A"}, RuleFacts{ProcessInstanceID: rulesKnown("instance-A")}, RuleFacts{ProcessInstanceID: rulesKnown("Instance-A")}, RuleFacts{ProcessInstanceID: rulesKnown("bad instance")}},
		{"name", map[string]any{"kind": "process_name", "value": "Worker"}, RuleFacts{ProcessName: rulesKnown("Worker")}, RuleFacts{ProcessName: rulesKnown("worker")}, RuleFacts{ProcessName: rulesKnown("")}},
		{"path", map[string]any{"kind": "process_path", "value": "/Opt/Worker"}, RuleFacts{ProcessPath: rulesKnown("/Opt/Worker")}, RuleFacts{ProcessPath: rulesKnown("/opt/worker")}, RuleFacts{ProcessPath: rulesKnown(string([]byte{0xff}))}},
		{"ip", map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, RuleFacts{IP: rulesKnown("192.0.2.3")}, RuleFacts{IP: rulesKnown("198.51.100.3")}, RuleFacts{IP: rulesKnown("not-an-address")}},
		{"domain", rulesDomain("exact", "example.test"), RuleFacts{Domain: rulesKnown("example.test")}, RuleFacts{Domain: rulesKnown("other.test")}, RuleFacts{Domain: rulesKnown("bad..test")}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := rulesFixture(t)
			f.intent["rules"] = []any{rulesEntry("one", tt.predicate, rulesKind("direct"))}
			set := rulesSet(t, &f)
			for _, item := range []struct {
				facts RuleFacts
				truth RuleTruth
			}{{tt.equal, RuleTrue}, {tt.unequal, RuleFalse}} {
				got, e := set.Match(0, item.facts)
				if e != nil || got != item.truth {
					t.Fatal("equality", got, e)
				}
			}
			got, e := set.Evaluate(tt.malformed)
			if !errors.Is(e, ErrRules) || !reflect.DeepEqual(got, RuleDecision{}) {
				t.Fatal("malformed used fact was permissive", got, e)
			}
			truth, e := set.Match(0, tt.malformed)
			if !errors.Is(e, ErrRules) || truth != "" {
				t.Fatal("Match accepted malformed fact", truth, e)
			}
		})
	}
	f := rulesFixture(t)
	f.intent["rules"] = []any{rulesEntry("domain", rulesDomain("exact", "example.test"), rulesKind("direct"))}
	set := rulesSet(t, &f)
	rulesDecision(t, set, RuleFacts{Domain: rulesKnown("example.test"), IP: rulesKnown("broken"), AppID: rulesKnown("bad app"), ProcessName: rulesKnown("")}, "matched", 0, "domain", config.Target{Kind: "direct"})
	// Unknown ignores even a stale malformed value, rather than parsing it.
	rulesDecision(t, set, RuleFacts{Domain: RuleValue{Known: false, Value: string([]byte{0xff})}}, "blocked", 0, "domain", config.Target{Kind: "reject"}, "domain")
}

func TestRulesDepth16And17Boundary(t *testing.T) {
	for _, depth := range []int{16, 17} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			f := rulesFixture(t)
			var predicate any = rulesDomain("exact", "example.test")
			for i := 1; i < depth; i++ {
				predicate = rulesAll(predicate)
			}
			f.intent["rules"] = []any{rulesEntry("deep", predicate, rulesKind("direct"))}
			f.sync(t)
			if depth == 17 {
				rulesAssertInvalid(t, f.input.Intent, f.input.Sources)
				return
			}
			set := rulesSet(t, &f)
			rulesDecision(t, set, RuleFacts{Domain: rulesKnown("example.test")}, "matched", 0, "deep", config.Target{Kind: "direct"})
		})
	}
}

func TestRulesOwnInputsAndDetachDecisions(t *testing.T) {
	f := rulesFixture(t)
	node := compileNode(f.input.Sources["a"], "US shared")
	f.intent["rules"] = []any{rulesEntry("node-rule", rulesDomain("exact", "node.test"), map[string]any{"kind": "node", "subscription_id": "a", "node_id": node}), rulesEntry("group-rule", rulesDomain("exact", "group.test"), map[string]any{"kind": "group", "group_id": "US"})}
	set := rulesSet(t, &f)
	copySet := set
	first := rulesDecision(t, set, RuleFacts{Domain: rulesKnown("node.test")}, "matched", 0, "node-rule", config.Target{Kind: "node", SubscriptionID: groupID("a"), NodeID: &node})
	*first.RuleID = "changed"
	*first.Target.NodeID = "changed"
	*first.Target.SubscriptionID = "b"
	for i := range f.input.Intent {
		f.input.Intent[i] = 'x'
	}
	for id, snapshot := range f.input.Sources {
		snapshot.Nodes = nil
		f.input.Sources[id] = snapshot
	}
	delete(f.input.Sources, "a")
	rulesDecision(t, set, RuleFacts{Domain: rulesKnown("node.test")}, "matched", 0, "node-rule", config.Target{Kind: "node", SubscriptionID: groupID("a"), NodeID: &node})
	group := rulesDecision(t, copySet, RuleFacts{Domain: rulesKnown("group.test")}, "matched", 1, "group-rule", config.Target{Kind: "group", GroupID: groupID("US")})
	*group.Target.GroupID = "changed"
	rulesDecision(t, set, RuleFacts{Domain: rulesKnown("group.test")}, "matched", 1, "group-rule", config.Target{Kind: "group", GroupID: groupID("US")})
	blocked := rulesDecision(t, set, RuleFacts{}, "blocked", 0, "node-rule", config.Target{Kind: "reject"}, "domain")
	blocked.UnknownFields[0] = "forged"
	*blocked.RuleID = "changed"
	rulesDecision(t, copySet, RuleFacts{}, "blocked", 0, "node-rule", config.Target{Kind: "reject"}, "domain")
}

func TestRulesZeroSetAndInvalidIndex(t *testing.T) {
	var zero RuleSet
	decision, e := zero.Evaluate(RuleFacts{})
	if !errors.Is(e, ErrRules) || !reflect.DeepEqual(decision, RuleDecision{}) {
		t.Fatal("zero set usable")
	}
	truth, e := zero.Match(0, RuleFacts{})
	if !errors.Is(e, ErrRules) || truth != "" {
		t.Fatal("zero Match usable")
	}
	f := rulesFixture(t)
	f.intent["rules"] = []any{rulesEntry("one", rulesDomain("exact", "example.test"), rulesKind("direct"))}
	set := rulesSet(t, &f)
	for _, index := range []int{-1, 1, 1000000} {
		truth, e := set.Match(index, RuleFacts{})
		if !errors.Is(e, ErrRules) || truth != "" {
			t.Fatal("invalid index used", index)
		}
	}
	f.intent["rules"] = []any{}
	empty := rulesSet(t, &f)
	rulesDecision(t, empty, RuleFacts{IP: rulesKnown("unused-malformed")}, "default", -1, "", config.Target{Kind: "reject"})
}

func TestRulesCompileProcessCapabilityCannotBeHiddenByFalse(t *testing.T) {
	for _, predicate := range []map[string]any{{"kind": "process_name", "value": "Worker"}, {"kind": "process_path", "value": "/opt/Worker"}, {"kind": "process_instance", "process_instance_id": "instance-A"}} {
		for _, position := range []string{"alone", "false-first", "false-last", "nested"} {
			t.Run(predicate["kind"].(string)+"/"+position, func(t *testing.T) {
				f := rulesFixture(t)
				f.intent["apps"] = []any{}
				var match any = predicate
				falsePredicate := rulesDomain("exact", "never-matches.test")
				switch position {
				case "false-first":
					match = rulesAll(falsePredicate, predicate)
				case "false-last":
					match = rulesAll(predicate, falsePredicate)
				case "nested":
					match = rulesAll(falsePredicate, rulesAll(predicate))
				}
				f.intent["rules"] = []any{rulesEntry("process", match, rulesKind("reject"))}
				f.sync(t)
				// Supplied facts prove this particular all false, but cannot authorize a
				// production core with process lookup disabled to accept its configuration.
				set, e := NewRuleSet(f.input.Intent, f.input.Sources)
				if e != nil {
					t.Fatal("pure process facts must be representable", e)
				}
				facts := RuleFacts{Domain: rulesKnown("other.test"), ProcessName: rulesKnown("Worker"), ProcessPath: rulesKnown("/opt/Worker"), ProcessInstanceID: rulesKnown("instance-A")}
				truth, e := set.Match(0, facts)
				want := RuleFalse
				if position == "alone" {
					want = RuleTrue
				}
				if e != nil || truth != want {
					t.Fatal("pure process predicate", truth, e)
				}
				c, e := Compile(f.input)
				if !errors.Is(e, ErrCompileUnsupported) || !reflect.DeepEqual(c, Compilation{}) {
					t.Fatal("unsupported process accepted or partial compilation", e)
				}
			})
		}
	}
}
