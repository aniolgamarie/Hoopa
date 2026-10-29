//go:build linux && amd64

package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/internal/config"
	"hoopa/internal/subscription"
	"reflect"
	"strings"
	"testing"
)

func TestDiagnosticValidGenerationGolden(t *testing.T) {
	f := newCompileFixture(t)
	compiled, err := Compile(f.input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, files := compiled.Bundle()
	hashes := map[string]string{}
	for name, raw := range files {
		hashes[name] = compileSHA(raw)
	}
	raw, err := json.Marshal(struct {
		Manifest string            `json:"manifest"`
		Files    map[string]string `json:"files"`
		Inputs   string            `json:"inputs"`
	}{compileSHA(compileJSON(t, manifest)), hashes, compileSHA(compileJSON(t, compiled.Inputs()))})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"manifest":"bc70c62e3691a8466ac899d1c07e06e30986d73f18ebe6ce53c4c7695f50fc71","files":{"controller-snapshot.json":"b1625bf954e04be1335a025a5e75b70cbcb444f30c8ae932647ee9c9a69a59d7","core.json":"6e26e7e716e993497c2a68a16dbe57503b5d3db715fb7efd2c607502c4cbbf51","intent.json":"419e00e9c247676b7989b6c29b42c94fe0e2edff9d812a9862b9061b7da8096a","provider-3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d.json":"313cf55c3212eaa10318fefde34072cc4df08d19e9dae6e173cde5cacf6f9791","provider-ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb.json":"7b261c135193e5aba58b14800c33cb59ad05f64a2d9f4bd314411663fd91408c","route-map.json":"fcb8e89dd159343f6fc75afa592c73dfae6841d156b81c3321af4142406707ce","rule-policy.json":"d6dd0651e3c8dbf7c2fb1a90282d624f6ea72bd145db7130c55408052e14fa73"},"inputs":"46810da4fb514f2f8b03fcea605eb97774774f6a652e2e4250a833e9ab9a8fb4"}`
	if string(raw) != want {
		t.Fatalf("valid compilation bytes changed: %s", raw)
	}
}

func diagnosticAssert(t *testing.T, err, class error, reason, field string) {
	t.Helper()
	r, f, ok := FailureDetails(err)
	if !ok || r != reason || f != field || !errors.Is(err, class) || err.Error() != class.Error() {
		t.Fatalf("diagnostic mismatch: %q %q %v class=%v", r, f, ok, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		got := fmt.Sprintf(format, err)
		want := class.Error()
		if format == "%q" {
			want = fmt.Sprintf("%q", want)
		}
		if got != want {
			t.Fatalf("format %s exposed details: %s", format, got)
		}
	}
	if len(r) > 64 || len(f) > 512 {
		t.Fatal("unbounded detail")
	}
}
func TestDiagnosticGroups(t *testing.T) {
	cases := []struct {
		name, reason, field string
		prepare             func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot)
	}{
		{"empty", "required_group_empty", "groups[US]", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			s := groupSources(false)
			a := s["a"]
			a.Nodes = []subscription.Node{}
			s["a"] = a
			return groupID("a"), []config.Group{groupAuto("US", nil)}, s
		}},
		{"region", "required_group_no_region_match", "groups[US].region", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return groupID("a"), []config.Group{groupAuto("US", groupRegion("ZZ"))}, groupSources(false)
		}},
		{"selected", "selected_node_missing", "groups[US].selected_node_id", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return nil, []config.Group{groupManual("US", "a", "missing")}, groupSources(false)
		}},
		{"selected-region", "selected_node_region_mismatch", "groups[US].selected_node_id", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			g := groupManual("US", "a", "a-us-1")
			g.Region = groupRegion("JP")
			return nil, []config.Group{g}, groupSources(false)
		}},
		{"source", "source_unavailable", "groups[US].source", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return groupID("a"), []config.Group{groupAuto("US", nil)}, nil
		}},
		{"unset", "active_source_unset", "groups[US].source", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return nil, []config.Group{groupAuto("US", nil)}, groupSources(false)
		}},
		{"follow", "follow_target_missing", "groups[US].follows", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return nil, []config.Group{groupFollow("US", "absent", true)}, nil
		}},
		{"cycle", "follow_cycle", "groups[US].follows", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			return nil, []config.Group{groupFollow("lead", "US", true), groupFollow("US", "BEST", true), groupFollow("BEST", "US", true)}, nil
		}},
		{"follow-region", "follow_region_mismatch", "groups[BEST].region", func() (*config.ID, []config.Group, map[config.ID]subscription.Snapshot) {
			g := groupFollow("BEST", "US", true)
			g.Region = groupRegion("JP")
			return groupID("a"), []config.Group{g, groupAuto("US", groupRegion("US"))}, groupSources(false)
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			active, gs, ss := tt.prepare()
			out, err := ResolveGroups(active, gs, ss)
			if out != nil {
				t.Fatal("partial groups")
			}
			diagnosticAssert(t, err, ErrGroups, tt.reason, tt.field)
		})
	}
	t.Run("optional", func(t *testing.T) {
		g := groupAuto("optional", groupRegion("ZZ"))
		g.Required = false
		out, err := ResolveGroups(groupID("a"), []config.Group{g}, groupSources(false))
		if err != nil || !out["optional"].Reject {
			t.Fatal("optional changed")
		}
	})
	t.Run("malformed", func(t *testing.T) {
		g := groupAuto("US", nil)
		g.Probe.URL = "ftp://private-probe-token.invalid"
		_, err := ResolveGroups(groupID("a"), []config.Group{g}, groupSources(false))
		if err != ErrGroups {
			t.Fatal("malformed diagnostic")
		}
	})
	t.Run("untrusted-source", func(t *testing.T) {
		s := groupSources(false)
		v := s["a"]
		v.SourceID = "b"
		s["a"] = v
		_, err := ResolveGroups(groupID("a"), []config.Group{groupAuto("US", nil)}, s)
		diagnosticAssert(t, err, ErrGroups, "source_unavailable", "groups[US].source")
	})
}
func TestDiagnosticIntentReferences(t *testing.T) {
	cases := []struct {
		name, reason, field string
		edit                func(map[string]any)
	}{
		{"active", "source_reference_missing", "selection.active_subscription", func(m map[string]any) { compileObj(m["selection"])["active_subscription"] = "absent" }},
		{"unset", "active_source_unset", "groups[US].source", func(m map[string]any) { compileObj(m["selection"])["active_subscription"] = nil }},
		{"fixed", "source_reference_missing", "groups[pinned-A].source.subscription_id", func(m map[string]any) {
			compileObj(compileIntentGroup(m, "pinned-A")["source"])["subscription_id"] = "absent"
		}},
		{"follow", "follow_target_missing", "groups[US-follow].follows", func(m map[string]any) { compileIntentGroup(m, "US-follow")["follows"] = "absent" }},
		{"cycle", "follow_cycle", "groups[US-follow].follows", func(m map[string]any) {
			compileIntentGroup(m, "US-follow")["follows"] = "BEST-follow"
			compileIntentGroup(m, "BEST-follow")["follows"] = "US-follow"
		}},
		{"rule-group", "target_group_missing", "rules[domain-rule].target", func(m map[string]any) {
			compileObj(m["rules"].([]any)[0])["target"] = map[string]any{"kind": "group", "group_id": "absent"}
		}},
		{"rule-source", "source_reference_missing", "rules[domain-rule].target", func(m map[string]any) {
			compileObj(m["rules"].([]any)[0])["target"] = map[string]any{"kind": "node", "subscription_id": "absent", "node_id": "missing"}
		}},
		{"default", "target_group_missing", "default_target", func(m map[string]any) { m["default_target"] = map[string]any{"kind": "group", "group_id": "absent"} }},
		{"default-source", "source_reference_missing", "default_target", func(m map[string]any) {
			m["default_target"] = map[string]any{"kind": "node", "subscription_id": "absent", "node_id": "missing"}
		}},
		{"app", "app_reference_missing", "rules[domain-rule].match.conditions[1].app_id", func(m map[string]any) {
			compileObj(m["rules"].([]any)[0])["match"] = map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "domain", "operator": "exact", "value": "test.invalid"}, map[string]any{"kind": "app", "app_id": "absent"}}}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newCompileFixture(t)
			tt.edit(f.intent)
			f.sync(t)
			if _, e := config.ParseIntent(f.input.Intent, config.IntentDraft); e != nil {
				t.Fatal("invalid draft fixture")
			}
			err := IntentReferenceFailure(f.input.Intent)
			diagnosticAssert(t, err, ErrCompile, tt.reason, tt.field)
			c, e := Compile(f.input)
			diagnosticAssert(t, e, ErrCompile, tt.reason, tt.field)
			if len(c.Inputs()) != 0 {
				t.Fatal("partial compile")
			}
		})
	}
	f := newCompileFixture(t)
	if e := IntentReferenceFailure(f.input.Intent); e != nil {
		t.Fatal(e)
	}
	for _, raw := range [][]byte{[]byte(`{"secret":"private-body"}`), []byte(`null`), []byte(`{`)} {
		if e := IntentReferenceFailure(raw); e != ErrCompile {
			t.Fatal("malformed explained")
		}
	}
	compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "domain", "operator": "exact", "value": "bad/private-body"}
	f.sync(t)
	if e := IntentReferenceFailure(f.input.Intent); e != ErrCompile {
		t.Fatal("nonreference explained")
	}
}
func TestDiagnosticCompileFailures(t *testing.T) {
	cases := []struct {
		name, reason, field string
		edit                func(*compileFixture)
	}{
		{"manual", "selected_node_missing", "groups[pinned-A].selected_node_id", func(f *compileFixture) { compileIntentGroup(f.intent, "pinned-A")["selected_node_id"] = "missing" }},
		{"manual-region", "selected_node_region_mismatch", "groups[pinned-A].selected_node_id", func(f *compileFixture) { compileIntentGroup(f.intent, "pinned-A")["region"] = "JP" }},
		{"rule-pin", "pinned_node_missing", "rules[inactive-pin].target", func(f *compileFixture) {
			compileObj(compileObj(f.intent["rules"].([]any)[1])["target"])["node_id"] = "missing"
		}},
		{"default-pin", "pinned_node_missing", "default_target", func(f *compileFixture) {
			f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": "missing"}
		}},
		{"source", "source_unavailable", "groups[US].source", func(f *compileFixture) { delete(f.input.Sources, "a") }},
		{"active-only-source", "source_unavailable", "selection.active_subscription", func(f *compileFixture) {
			f.intent["groups"] = []any{}
			f.intent["rules"] = []any{}
			f.intent["default_target"] = map[string]any{"kind": "reject"}
			delete(f.input.Sources, "a")
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newCompileFixture(t)
			tt.edit(&f)
			f.sync(t)
			c, e := Compile(f.input)
			diagnosticAssert(t, e, ErrCompile, tt.reason, tt.field)
			if errors.Is(e, ErrGroups) || len(c.Inputs()) != 0 {
				t.Fatal("wrong class/partial result")
			}
			m, files := c.Bundle()
			if len(files) != 0 || !reflect.DeepEqual(m, config.BundleManifest{}) {
				t.Fatal("partial bundle")
			}
		})
	}
}
func TestDiagnosticRecognitionAndDeterminism(t *testing.T) {
	err := groupDiagnostic("selected_node_missing", "groups[US].selected_node_id")
	r, f, ok := FailureDetails(fmt.Errorf("ordinary wrapper: %w", err))
	if !ok || r != "selected_node_missing" || f != "groups[US].selected_node_id" {
		t.Fatal("wrapped details lost")
	}
	for _, e := range []error{nil, ErrGroups, ErrCompile, errors.New("selected_node_missing"), fmt.Errorf("%w", ErrGroups)} {
		if _, _, ok := FailureDetails(e); ok {
			t.Fatal("foreign diagnostic")
		}
	}
	if groupDiagnostic(strings.Repeat("x", 65), "field") != ErrGroups || compileDiagnostic("reason", strings.Repeat("x", 513)) != ErrCompile {
		t.Fatal("unbounded detail")
	}
	fxt := newCompileFixture(t)
	rules := fxt.intent["rules"].([]any)
	compileObj(rules[0])["target"] = map[string]any{"kind": "group", "group_id": "first-missing"}
	compileObj(rules[1])["target"] = map[string]any{"kind": "group", "group_id": "second-missing"}
	fxt.sync(t)
	for i := 0; i < 10; i++ {
		diagnosticAssert(t, IntentReferenceFailure(fxt.input.Intent), ErrCompile, "target_group_missing", "rules[domain-rule].target")
	}
	gs := make([]config.Group, 10000)
	for i := range gs {
		gs[i] = groupFollow(config.ID(fmt.Sprintf("g%d", i)), config.ID(fmt.Sprintf("g%d", i+1)), true)
	}
	gs[len(gs)-1].Follows = groupID("g5000")
	id, ok := followCycleGroup(gs)
	if !ok || id != "g5000" {
		t.Fatal("deep cycle member")
	}
}
