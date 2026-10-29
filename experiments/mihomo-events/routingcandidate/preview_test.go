package routingcandidate

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func previewSpec() Spec {
	s := fixture()
	s.ProgramRules = true
	s.Requirements.RequiredFields = []string{"program_name", "program_path"}
	s.Rules = []Rule{
		{ID: "program", Conditions: []Condition{{Kind: "PROCESS-NAME", Value: "curl"}, {Kind: "PROCESS-PATH", Value: "/bin/curl"}}, Target: Target{GroupID: "group:active"}},
		{ID: "fallback", Match: true, Target: Target{Reject: true}},
	}
	return s
}
func previewFacts() Facts {
	return Facts{Entry: "app-a", RuleHost: "alpha.test", DestinationIP: netip.MustParseAddr("127.0.0.1"), ProgramName: ProgramText{Known: true, Value: "curl"}, ProgramPath: ProgramText{Known: true, Value: "/bin/curl"}}
}
func explainOK(t *testing.T, s Spec, f Facts) Explanation {
	t.Helper()
	e, err := Explain(s, f)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExplainANDTruthTable(t *testing.T) {
	states := []struct {
		name string
		text ProgramText
	}{
		{"true", ProgramText{Known: true, Value: "curl"}},
		{"false", ProgramText{Known: true, Value: "other"}},
		{"unknown", ProgramText{}},
	}
	for _, left := range states {
		for _, right := range states {
			t.Run(left.name+"/"+right.name, func(t *testing.T) {
				s := previewSpec()
				f := previewFacts()
				f.ProgramName = left.text
				f.ProgramPath = right.text
				if f.ProgramPath.Known {
					f.ProgramPath.Value = "/bin/" + f.ProgramPath.Value
				}
				expected := StateTrue
				if left.name == "false" || right.name == "false" {
					expected = StateFalse
				} else if left.name == "unknown" || right.name == "unknown" {
					expected = StateUnknown
				}
				got := explainOK(t, s, f)
				if got.Rules[0].State != expected {
					t.Fatalf("got %s, want %s", got.Rules[0].State, expected)
				}
				switch expected {
				case StateTrue:
					if got.Decision != "matched" || got.IntendedTarget != "group:active" || got.Rules[1].State != StateNotEvaluated || got.Rules[1].StoppedBy != "program" {
						t.Fatal("true did not stop", got)
					}
				case StateFalse:
					if got.Decision != "matched" || got.StoppingRuleID != "fallback" || got.IntendedTarget != "REJECT" || len(got.Rules[0].UnavailableFields) != 0 {
						t.Fatal("false did not dominate", got)
					}
				case StateUnknown:
					if got.Decision != "blocked_unknown" || got.IntendedTarget != "" || got.StoppingRuleID != "program" || len(got.UnavailableFields) == 0 || got.Rules[1].State != StateNotEvaluated {
						t.Fatal("unknown did not block", got)
					}
				}
			})
		}
	}
}

func TestExplainPredicates(t *testing.T) {
	tests := []struct {
		name      string
		condition Condition
		change    func(*Facts)
		want      RuleState
	}{
		{"app exact", Condition{"app", "app-a"}, func(f *Facts) {}, StateTrue},
		{"app other declared entry", Condition{"app", "app-a"}, func(f *Facts) { f.Entry = "app-b" }, StateFalse},
		{"name exact", Condition{"PROCESS-NAME", "curl"}, func(f *Facts) {}, StateTrue},
		{"name case sensitive", Condition{"PROCESS-NAME", "CURL"}, func(f *Facts) {}, StateFalse},
		{"path exact", Condition{"PROCESS-PATH", "/bin/curl"}, func(f *Facts) {}, StateTrue},
		{"path case sensitive", Condition{"PROCESS-PATH", "/BIN/curl"}, func(f *Facts) {}, StateFalse},
		{"known empty name", Condition{"PROCESS-NAME", "curl"}, func(f *Facts) { f.ProgramName = ProgramText{Known: true} }, StateFalse},
		{"known empty path", Condition{"PROCESS-PATH", "/bin/curl"}, func(f *Facts) { f.ProgramPath = ProgramText{Known: true} }, StateFalse},
		{"domain lowercase pattern", Condition{"DOMAIN", "ALPHA.TEST"}, func(f *Facts) {}, StateTrue},
		{"domain literal host case", Condition{"DOMAIN", "alpha.test"}, func(f *Facts) { f.RuleHost = "ALPHA.TEST" }, StateFalse},
		{"domain absent", Condition{"DOMAIN", "alpha.test"}, func(f *Facts) { f.RuleHost = "" }, StateFalse},
		{"suffix exact", Condition{"DOMAIN-SUFFIX", "ALPHA.TEST"}, func(f *Facts) {}, StateTrue},
		{"suffix child", Condition{"DOMAIN-SUFFIX", "test"}, func(f *Facts) {}, StateTrue},
		{"suffix false tail", Condition{"DOMAIN-SUFFIX", "pha.test"}, func(f *Facts) {}, StateFalse},
		{"suffix host case", Condition{"DOMAIN-SUFFIX", "test"}, func(f *Facts) { f.RuleHost = "alpha.TEST" }, StateFalse},
		{"suffix absent", Condition{"DOMAIN-SUFFIX", "test"}, func(f *Facts) { f.RuleHost = "" }, StateFalse},
		{"ip contains", Condition{"IP-CIDR", "127.0.0.0/8"}, func(f *Facts) {}, StateTrue},
		{"ip outside", Condition{"IP-CIDR", "192.0.2.0/24"}, func(f *Facts) {}, StateFalse},
		{"ip absent despite host", Condition{"IP-CIDR", "127.0.0.0/8"}, func(f *Facts) { f.DestinationIP = netip.Addr{} }, StateFalse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := previewSpec()
			s.Rules[0].Conditions = []Condition{tt.condition}
			f := previewFacts()
			tt.change(&f)
			got := explainOK(t, s, f)
			if got.Rules[0].State != tt.want {
				t.Fatalf("got %s, want %s", got.Rules[0].State, tt.want)
			}
		})
	}
}

func TestExplainOrderingAndUnknownFields(t *testing.T) {
	s := previewSpec()
	f := previewFacts()
	f.ProgramName = ProgramText{}
	f.ProgramPath = ProgramText{}
	s.Rules[0].Conditions = append(s.Rules[0].Conditions, Condition{Kind: "PROCESS-NAME", Value: "curl"})
	unknown := explainOK(t, s, f)
	if !reflect.DeepEqual(unknown.UnavailableFields, []string{"program_name", "program_path"}) || !reflect.DeepEqual(unknown.Rules[0].UnavailableFields, unknown.UnavailableFields) {
		t.Fatal("unavailable fields", unknown)
	}
	domain := Rule{ID: "domain", Conditions: []Condition{{Kind: "DOMAIN-SUFFIX", Value: "test"}}, Target: Target{Node: NodeRef{"p2", "n2"}}}
	s.Rules = append([]Rule{domain}, s.Rules...)
	first := explainOK(t, s, f)
	if first.Decision != "matched" || first.StoppingRuleID != "domain" || len(first.UnavailableFields) != 0 || first.Rules[1].State != StateNotEvaluated || first.Rules[2].StoppedBy != "domain" {
		t.Fatal("later unknown evaluated", first)
	}
	s.Rules[0], s.Rules[1] = s.Rules[1], s.Rules[0]
	reversed := explainOK(t, s, f)
	if reversed.Decision != "blocked_unknown" || reversed.Rules[1].State != StateNotEvaluated {
		t.Fatal("unknown bypassed", reversed)
	}
	s.Rules[0] = domain
	s.Rules[0].ID = "repeated"
	s.Rules[1] = domain
	repeated := explainOK(t, s, f)
	if repeated.Rules[0].RuleID != "repeated" || repeated.Rules[1].RuleID != "domain" || repeated.Rules[1].State != StateNotEvaluated || repeated.Rules[1].StoppedBy != "repeated" {
		t.Fatal("repeated rule order", repeated)
	}
	s.Rules[1].Conditions = []Condition{{Kind: "DOMAIN", Value: "alpha.test"}}
	overlap := explainOK(t, s, f)
	if overlap.StoppingRuleID != "repeated" {
		t.Fatal("overlap order")
	}
}

func TestExplainInvalidInputsReturnZero(t *testing.T) {
	facts := []struct {
		name   string
		change func(*Facts)
	}{
		{"unknown name carries value", func(f *Facts) { f.ProgramName = ProgramText{Value: "curl"} }},
		{"unknown path carries value", func(f *Facts) { f.ProgramPath = ProgramText{Value: "/bin/curl"} }},
		{"invalid name utf8", func(f *Facts) { f.ProgramName.Value = "curl\xff" }},
		{"invalid path utf8", func(f *Facts) { f.ProgramPath.Value = "/bin/curl\xff" }},
		{"long name", func(f *Facts) { f.ProgramName.Value = strings.Repeat("x", 256) }},
		{"long path", func(f *Facts) { f.ProgramPath.Value = strings.Repeat("x", 256) }},
		{"invalid host utf8", func(f *Facts) { f.RuleHost = "host\xff" }},
		{"long host", func(f *Facts) { f.RuleHost = strings.Repeat("x", 254) }},
		{"undeclared entry", func(f *Facts) { f.Entry = "App-a" }},
		{"empty entry", func(f *Facts) { f.Entry = "" }},
		{"ipv6", func(f *Facts) { f.DestinationIP = netip.MustParseAddr("::1") }},
		{"mapped ipv6", func(f *Facts) { f.DestinationIP = netip.MustParseAddr("::ffff:127.0.0.1") }},
	}
	for _, tt := range facts {
		t.Run(tt.name, func(t *testing.T) {
			f := previewFacts()
			tt.change(&f)
			got, e := Explain(previewSpec(), f)
			if e == nil || !reflect.DeepEqual(got, Explanation{}) {
				t.Fatal("invalid facts accepted", got, e)
			}
		})
	}
	for name, change := range map[string]func(*Spec){
		"invalid later rule": func(s *Spec) {
			s.Rules = append(s.Rules[:1], Rule{ID: "bad", Conditions: []Condition{{Kind: "OR", Value: "x"}}, Target: Target{Reject: true}}, s.Rules[1])
		},
		"missing later source": func(s *Spec) { s.Rules[1].Target = Target{Node: NodeRef{"absent", "n"}} },
		"invalid later process utf8": func(s *Spec) {
			s.Rules = append(s.Rules[:1], Rule{ID: "bad", Conditions: []Condition{{Kind: "PROCESS-NAME", Value: "bad\xff"}}, Target: Target{Reject: true}}, s.Rules[1])
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := previewSpec()
			change(&s)
			got, e := Explain(s, previewFacts())
			if e == nil || !reflect.DeepEqual(got, Explanation{}) {
				t.Fatal("later invalid rule escaped full compile")
			}
		})
	}
}

func TestExplainBindingAndOwnership(t *testing.T) {
	s := previewSpec()
	f := previewFacts()
	f.ProgramName = ProgramText{}
	f.ProgramPath = ProgramText{}
	before, _ := json.Marshal(s)
	factsBefore := f
	got := explainOK(t, s, f)
	want := explainOK(t, s, f)
	compiled := compileOK(t, s)
	if got.ConfigSHA256 != compiled.ConfigSHA256 || got.Schema != "hoopa-routing-explanation-v1" || got.Basis != "supplied_facts_v1" {
		t.Fatal("unbound explanation", got)
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) || f != factsBefore {
		t.Fatal("input mutated")
	}
	got.UnavailableFields[0] = "changed"
	if got.Rules[0].UnavailableFields[0] != "program_name" {
		t.Fatal("result slices alias each other")
	}
	got.Rules[0].UnavailableFields[0] = "changed"
	got.Rules[0].RuleID = "changed"
	if !reflect.DeepEqual(want, explainOK(t, s, f)) {
		t.Fatal("result aliases another call")
	}
	s.Rules[0].ID = "input changed"
	s.Rules[0].Conditions[0].Value = "input changed"
	s.Requirements.RequiredFields[0] = "input changed"
	if want.Rules[0].RuleID != "program" || want.UnavailableFields[0] != "program_name" {
		t.Fatal("result aliases input")
	}
}
