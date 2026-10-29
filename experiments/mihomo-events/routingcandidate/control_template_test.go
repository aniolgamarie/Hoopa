package routingcandidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func templateFixture() Spec {
	s := fixture()
	s.Generation = ""
	return s
}

func specBytes(t *testing.T, s Spec) []byte {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestControlTemplateEquivalentAndOwned(t *testing.T) {
	for _, entries := range []int{2, 4} {
		t.Run(fmt.Sprint(entries), func(t *testing.T) {
			s := templateFixture()
			if entries == 4 {
				s.Entries = append(s.Entries, Entry{"app-c", "127.0.0.1:19003", "user-c", strings.Repeat("c", 32)}, Entry{"app-d", "127.0.0.1:19004", "user-d", strings.Repeat("d", 32)})
			}
			s.ProgramRules = true
			s.Requirements.RequiredFields = []string{"instance", "program_path", "program_name"}
			s.Rules[0].Conditions = []Condition{{Kind: "app", Value: "app-a"}, {Kind: "PROCESS-NAME", Value: "app-a"}, {Kind: "PROCESS-PATH", Value: "/work/apps/app-a"}}
			before := specBytes(t, s)
			ordinarySpec := s
			ordinarySpec.Generation = "control-template"
			ordinary := compileOK(t, ordinarySpec)
			var mapping routeMap
			if err := json.Unmarshal(ordinary.RouteMap, &mapping); err != nil {
				t.Fatal(err)
			}
			if mapping.ConfigGeneration != ordinarySpec.Generation {
				t.Fatal("ordinary generation missing")
			}
			mapping.ConfigGeneration = ""
			ordinary.RouteMap, _ = json.Marshal(mapping)
			got, err := CompileControlTemplate(s)
			if err != nil || !reflect.DeepEqual(got, ordinary) {
				t.Fatal("template differs beyond route-map generation", err)
			}
			if !bytes.Equal(before, specBytes(t, s)) {
				t.Fatal("caller mutated")
			}
			again, err := CompileControlTemplate(s)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("nondeterministic template", err)
			}
			sum := sha256.Sum256(got.Config)
			if got.ConfigSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatal("config hash")
			}
			for _, entry := range mapping.Entries {
				if entry.ProviderName != "" || entry.ProviderID != nil {
					t.Fatal("intent provenance became observed identity")
				}
			}
			if got.Rules[0].ID != "app" || got.Rules[1].ID != "pin" || got.GroupSelections[0].GroupID != "group:active" || got.GroupSelections[1].GroupID != "group:fixed" || got.Rules[1].InternalTarget != internalNodeID(NodeRef{"p2", "n2"}) {
				t.Fatal("rule/group order or explicit pin changed")
			}
			got.Config[0] = 'x'
			got.RouteMap[0] = 'x'
			got.Provenance[0] = 'x'
			got.Rules[0].Conditions[0].Value = "changed"
			got.GroupSelections[0].GroupID = "changed"
			if !bytes.Equal(before, specBytes(t, s)) || !reflect.DeepEqual(again, ordinary) {
				t.Fatal("returned candidate aliases caller or another output")
			}
		})
	}
}

func TestControlTemplateRejectsWithoutPartialResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Spec)
	}{
		{"runtime-generation", func(s *Spec) { s.Generation = "gen:caller" }},
		{"invalid-generation", func(s *Spec) { s.Generation = " " }},
		{"invalid-source", func(s *Spec) { s.ProviderSnapshots[0] = []byte(`{`) }},
		{"inactive-source", func(s *Spec) { s.ProviderSnapshots[1] = snapshot("p2", "removed", "Shared", 19102) }},
		{"missing-selection", func(s *Spec) { s.Groups[0].Selected = NodeRef{} }},
		{"shadowed-reference", func(s *Spec) { s.Rules[1].Target.Node.NodeID = "removed" }},
		{"program-without-optin", func(s *Spec) { s.Rules[0].Conditions = []Condition{{Kind: "PROCESS-NAME", Value: "app-a"}} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := templateFixture()
			test.change(&s)
			before := specBytes(t, s)
			got, err := CompileControlTemplate(s)
			if err != ErrCandidate || !reflect.DeepEqual(got, Candidate{}) {
				t.Fatal("usable partial result", err)
			}
			if !bytes.Equal(before, specBytes(t, s)) {
				t.Fatal("rejected caller mutated")
			}
		})
	}
	if got, err := Compile(templateFixture()); err != ErrCandidate || !reflect.DeepEqual(got, Candidate{}) {
		t.Fatal("ordinary Compile accepted empty runtime generation")
	}
}

// Size valid inputs using the ordinary runtime compiler: its only generation
// bytes are the explicit route-map label, which the bridge removes.
func exactBudgetSpec(t *testing.T) Spec {
	t.Helper()
	for rules := 1; rules <= 63; rules++ {
		s := templateFixture()
		s.Groups[0].DisplayAlias = "A"
		s.Rules = nil
		for i := 0; i < rules; i++ {
			s.Rules = append(s.Rules, Rule{ID: fmt.Sprintf("domain-%d", i), Conditions: []Condition{{Kind: "DOMAIN", Value: strings.Repeat("a", 63) + ".test"}}, Target: Target{Node: NodeRef{"p2", "n2"}}})
		}
		s.Rules = append(s.Rules, Rule{ID: "fallback", Match: true, Target: Target{Reject: true}})
		runtimeSpec := s
		runtimeSpec.Generation = "control-template"
		ordinary := compileOK(t, runtimeSpec)
		size := len(ordinary.Config) + len(ordinary.RouteMap) - len(runtimeSpec.Generation)
		padding := 8192 - size
		if padding >= 0 && padding < 255 {
			s.Groups[0].DisplayAlias = strings.Repeat("A", padding+1)
			return s
		}
	}
	t.Fatal("no valid exact-budget fixture within compiler bounds")
	return Spec{}
}

func TestControlTemplateExactBundleBudget(t *testing.T) {
	s := exactBudgetSpec(t)
	before := specBytes(t, s)
	got, err := CompileControlTemplate(s)
	if err != nil || len(got.Config)+len(got.RouteMap) != 8192 {
		t.Fatal("exact8192 rejected", err, len(got.Config)+len(got.RouteMap))
	}
	if !bytes.Equal(before, specBytes(t, s)) {
		t.Fatal("accepted boundary caller mutated")
	}
	s.Groups[0].DisplayAlias += "A"
	before = specBytes(t, s)
	runtimeSpec := s
	runtimeSpec.Generation = "control-template"
	ordinary := compileOK(t, runtimeSpec)
	if size := len(ordinary.Config) + len(ordinary.RouteMap) - len(runtimeSpec.Generation); size != 8193 {
		t.Fatal("overflow fixture is not exact8193", size)
	}
	got, err = CompileControlTemplate(s)
	if err != ErrCandidate || !reflect.DeepEqual(got, Candidate{}) {
		t.Fatal("oversize candidate escaped", err)
	}
	if !bytes.Equal(before, specBytes(t, s)) {
		t.Fatal("overflow caller mutated")
	}
	t.Log("valid compiler candidates exercise8192-byte acceptance and8193-byte atomic rejection")
}
