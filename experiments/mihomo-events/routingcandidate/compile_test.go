package routingcandidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hoopa/experiments/mihomo-events/capabilityprofile"
	"hoopa/protocol/identityprofile"
)

func fixture() Spec {
	return Spec{
		Generation: "candidate-1", ControllerEndpoint: "127.0.0.1:19000", ControllerSecret: strings.Repeat("s", 32),
		Entries: []Entry{{"app-a", "127.0.0.1:19001", "user-a", strings.Repeat("a", 32)}, {"app-b", "127.0.0.1:19002", "user-b", strings.Repeat("b", 32)}},
		Hosts:   map[string]string{"alpha.test": "127.0.0.1"}, ActiveProviderID: "p1",
		ProviderSnapshots: [][]byte{snapshot("p1", "n1", "Shared", 19101), snapshot("p2", "n2", "Shared", 19102)},
		Groups:            []Group{{"group:active", "Active", "active", "us", NodeRef{"p1", "n1"}}, {"group:fixed", "Fixed", "p2", "us", NodeRef{"p2", "n2"}}},
		Rules:             []Rule{{ID: "app", Conditions: []Condition{{Kind: "app", Value: "app-a"}}, Target: Target{GroupID: "group:active"}}, {ID: "pin", Conditions: []Condition{{Kind: "DOMAIN", Value: "alpha.test"}}, Target: Target{Node: NodeRef{"p2", "n2"}}}, {ID: "fallback", Match: true, Target: Target{Reject: true}}},
		Requirements:      identityprofile.Requirements{ProfileID: identityprofile.ID, SourceSchemaVersion: 5, RequiredFields: []string{"instance"}},
	}
}
func snapshot(provider, node, alias string, port int) []byte {
	b, _ := json.Marshal(map[string]any{"schema": "hoopa-routing-provider-fixture-v1", "provider_id": provider, "revision": "rev-1", "nodes": []any{map[string]any{"node_id": node, "display_name": alias, "region": "us", "type": "http", "server": "127.0.0.1", "port": port}}})
	return b
}
func compileOK(t *testing.T, s Spec) Candidate {
	t.Helper()
	c, e := Compile(s)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestCompleteCandidate(t *testing.T) {
	s := fixture()
	c := compileOK(t, s)
	cfg, _, e := capabilityprofile.ValidateDeclaredProgramIdentity(c.Config, s.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if len(cfg.Listeners) != 2 || len(cfg.Groups) != 2 || len(cfg.Proxies) != 2 {
		t.Fatalf("incomplete config: %+v", cfg)
	}
	for i, g := range cfg.Groups {
		if len(g.Proxies) != 1 || g.Proxies[0] != c.GroupSelections[i].InternalNodeID {
			t.Fatal("implicit selection", g)
		}
	}
	if c.Rules[1].InternalTarget != internalNodeID(NodeRef{"p2", "n2"}) {
		t.Fatal("pin changed")
	}
	sum := sha256.Sum256(c.Config)
	if c.ConfigSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("config hash")
	}
	var p provenance
	if json.Unmarshal(c.Provenance, &p) != nil || len(p.Nodes) != 2 || len(p.Snapshots) != 2 {
		t.Fatal("provenance")
	}
	for i, ps := range p.Snapshots {
		sum := sha256.Sum256(s.ProviderSnapshots[i])
		if ps.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("raw snapshot hash")
		}
	}
	var rm routeMap
	if json.Unmarshal(c.RouteMap, &rm) != nil || rm.SchemaVersion != 1 || len(rm.Entries) != 4 {
		t.Fatal("route map")
	}
	for _, entry := range rm.Entries {
		if entry.ProviderID != nil || entry.ProviderName != "" {
			t.Fatal("compile provenance leaked into observed chain")
		}
	}
	if !reflect.DeepEqual(c, compileOK(t, s)) {
		t.Fatal("nondeterministic")
	}
	s.ProviderSnapshots[0], s.ProviderSnapshots[1] = s.ProviderSnapshots[1], s.ProviderSnapshots[0]
	if !reflect.DeepEqual(c, compileOK(t, s)) {
		t.Fatal("provider order affected candidate")
	}
}
func TestActiveSwitchAndStableIdentity(t *testing.T) {
	s := fixture()
	before := compileOK(t, s)
	s.ActiveProviderID = "p2"
	s.Groups[0].Selected = NodeRef{"p2", "n2"}
	after := compileOK(t, s)
	if before.Rules[1].InternalTarget != after.Rules[1].InternalTarget || after.GroupSelections[0].InternalNodeID != after.Rules[1].InternalTarget {
		t.Fatal("active switch rebound pin")
	}
	s.ProviderSnapshots[1] = snapshot("p2", "n2", "Renamed", 19103)
	renamed := compileOK(t, s)
	if renamed.Rules[1].InternalTarget != after.Rules[1].InternalTarget || bytes.Equal(renamed.Config, after.Config) {
		t.Fatal("explicit ID / endpoint binding")
	}
	s.ProviderSnapshots[1] = snapshot("p2", "replacement", "Shared", 19103)
	if c, e := Compile(s); e == nil || !reflect.DeepEqual(c, Candidate{}) {
		t.Fatal("alias rebound removed node")
	}
}
func TestRejectionsReturnZeroCandidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Spec)
	}{
		{"missing inactive provider", func(s *Spec) { s.ProviderSnapshots = s.ProviderSnapshots[:1] }},
		{"missing inactive pinned node", func(s *Spec) { s.ProviderSnapshots[1] = snapshot("p2", "other", "Shared", 19102) }},
		{"empty snapshot", func(s *Spec) { s.ProviderSnapshots[1] = nil }},
		{"empty nodes", func(s *Spec) {
			s.ProviderSnapshots[1] = []byte(`{"schema":"hoopa-routing-provider-fixture-v1","provider_id":"p2","revision":"r","nodes":[]}`)
		}},
		{"duplicate provider", func(s *Spec) { s.ProviderSnapshots[1] = s.ProviderSnapshots[0] }},
		{"duplicate key", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte(`"provider_id":"p2"`), []byte(`"provider_id":"p2","provider_id":"p2"`), 1)
		}},
		{"unknown field", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte(`"revision"`), []byte(`"extra":1,"revision"`), 1)
		}},
		{"missing field", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte(`"region":"us",`), nil, 1)
		}},
		{"null", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte(`"Shared"`), []byte(`null`), 1)
		}},
		{"trailing", func(s *Spec) { s.ProviderSnapshots[1] = append(s.ProviderSnapshots[1], []byte(` {}`)...) }},
		{"malformed", func(s *Spec) { s.ProviderSnapshots[1] = []byte(`{`) }},
		{"oversize", func(s *Spec) { s.ProviderSnapshots[1] = bytes.Repeat([]byte(" "), 65537) }},
		{"invalid utf8", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte("Shared"), []byte{255}, 1)
		}},
		{"unpaired surrogate", func(s *Spec) {
			s.ProviderSnapshots[1] = bytes.Replace(s.ProviderSnapshots[1], []byte("Shared"), []byte(`\ud800`), 1)
		}},
		{"too many snapshots", func(s *Spec) {
			for len(s.ProviderSnapshots) < 9 {
				s.ProviderSnapshots = append(s.ProviderSnapshots, s.ProviderSnapshots[0])
			}
		}},
		{"no selection", func(s *Spec) { s.Groups[0].Selected = NodeRef{} }},
		{"not member", func(s *Spec) { s.Groups[0].Selected.NodeID = "absent" }},
		{"wrong provider", func(s *Spec) { s.Groups[0].Selected = NodeRef{"p2", "n2"} }},
		{"wrong region", func(s *Spec) { s.Groups[0].Region = "eu" }},
		{"missing active", func(s *Spec) { s.ActiveProviderID = "absent" }},
		{"unknown group", func(s *Spec) { s.Rules[0].Target.GroupID = "group:missing" }},
		{"ambiguous target", func(s *Spec) { s.Rules[0].Target.Reject = true }},
		{"unsupported even shadowed", func(s *Spec) { s.Rules[1].Conditions[0] = Condition{Kind: "OR", Value: "a"} }},
		{"undeclared app", func(s *Spec) { s.Rules[0].Conditions[0].Value = "absent" }},
		{"injection", func(s *Spec) { s.Rules[1].Conditions[0].Value = "alpha.test,REJECT" }},
		{"process no optin", func(s *Spec) { s.Rules[0].Conditions[0] = Condition{Kind: "PROCESS-NAME", Value: "curl"} }},
		{"process undeclared field", func(s *Spec) {
			s.ProgramRules = true
			s.Rules[0].Conditions[0] = Condition{Kind: "PROCESS-NAME", Value: "curl"}
		}},
		{"bad identity", func(s *Spec) { s.Requirements.RequiredFields = []string{"pid"} }},
		{"early match", func(s *Spec) { s.Rules[0].Match = true; s.Rules[0].Conditions = nil }},
		{"no match", func(s *Spec) { s.Rules = s.Rules[:2] }},
		{"match with conditions", func(s *Spec) { s.Rules[2].Conditions = []Condition{{Kind: "DOMAIN", Value: "x.test"}} }},
		{"duplicate rule", func(s *Spec) { s.Rules[1].ID = s.Rules[0].ID }},
		{"no conditions", func(s *Spec) { s.Rules[0].Conditions = nil }},
		{"too many conditions", func(s *Spec) {
			for len(s.Rules[0].Conditions) < 9 {
				s.Rules[0].Conditions = append(s.Rules[0].Conditions, Condition{Kind: "app", Value: "app-a"})
			}
		}},
		{"one entry", func(s *Spec) { s.Entries = s.Entries[:1] }},
		{"duplicate endpoint", func(s *Spec) { s.Entries[0].Endpoint = s.ControllerEndpoint }},
		{"unauthenticated entry", func(s *Spec) { s.Entries[0].Password = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			tt.change(&s)
			c, e := Compile(s)
			if e == nil || !reflect.DeepEqual(c, Candidate{}) {
				t.Fatalf("got candidate/error: %+v %v", c, e)
			}
		})
	}
}
func TestOrderedConditionsAndOwnership(t *testing.T) {
	s := fixture()
	s.ProgramRules = true
	s.Requirements.RequiredFields = []string{"program_name", "program_path"}
	s.Rules[0].Conditions = []Condition{{Kind: "app", Value: "app-a"}, {Kind: "PROCESS-NAME", Value: "curl"}, {Kind: "PROCESS-PATH", Value: "/usr/bin/curl"}, {Kind: "IP-CIDR", Value: "127.0.0.0/8"}}
	s.Rules[1].Conditions = []Condition{{Kind: "DOMAIN-SUFFIX", Value: "test"}}
	c := compileOK(t, s)
	cfg, _, e := capabilityprofile.ValidateDeclaredProgramIdentity(c.Config, s.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(cfg.Rules[0], "AND,((IN-NAME,app-a),(PROCESS-NAME,curl),(PROCESS-PATH,/usr/bin/curl),(IP-CIDR,127.0.0.0/8,no-resolve)),") || c.Rules[0].ID != "app" || c.Rules[1].ID != "pin" {
		t.Fatal("order or conditions changed", cfg.Rules)
	}
	original := compileOK(t, s)
	s.Rules[0].Conditions[0].Value = "changed"
	s.Requirements.RequiredFields[0] = "changed"
	s.Entries[0].Name = "changed"
	s.Hosts["alpha.test"] = "changed"
	s.ProviderSnapshots[0][0] = 'x'
	s.Groups[0].ID = "changed"
	if !reflect.DeepEqual(c, original) {
		t.Fatal("candidate aliases input")
	}
	c.Rules[0].Conditions[0].Value = "output mutation"
	c.Config[0] = 'x'
	c.RouteMap[0] = 'x'
	c.Provenance[0] = 'x'
	c.GroupSelections[0].GroupID = "changed"
	if reflect.DeepEqual(c, original) {
		t.Fatal("test did not mutate output")
	}
}

func TestSnapshotSchemaBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong schema", func(p map[string]any) { p["schema"] = "future" }},
		{"empty provider", func(p map[string]any) { p["provider_id"] = "" }},
		{"non ascii provider", func(p map[string]any) { p["provider_id"] = "来源" }},
		{"long revision", func(p map[string]any) { p["revision"] = strings.Repeat("r", 129) }},
		{"empty revision", func(p map[string]any) { p["revision"] = "" }},
		{"null array", func(p map[string]any) { p["nodes"] = nil }},
		{"wrong array type", func(p map[string]any) { p["nodes"] = map[string]any{} }},
		{"null node", func(p map[string]any) { p["nodes"] = []any{nil} }},
		{"duplicate node", func(p map[string]any) { a := p["nodes"].([]any); p["nodes"] = append(a, a[0]) }},
		{"excess nodes", func(p map[string]any) {
			a := p["nodes"].([]any)
			for len(a) < 65 {
				a = append(a, a[0])
			}
			p["nodes"] = a
		}},
		{"empty node id", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["node_id"] = "" }},
		{"long node id", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["node_id"] = strings.Repeat("n", 129) }},
		{"empty alias", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["display_name"] = "" }},
		{"long alias", func(p map[string]any) {
			p["nodes"].([]any)[0].(map[string]any)["display_name"] = strings.Repeat("a", 257)
		}},
		{"nul alias", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["display_name"] = "a\x00b" }},
		{"region not token", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["region"] = "us west" }},
		{"direct node", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["type"] = "direct" }},
		{"dns endpoint", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["server"] = "localhost" }},
		{"nonexact loopback", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["server"] = "127.0.0.2" }},
		{"zero port", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["port"] = 0 }},
		{"large port", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["port"] = 65536 }},
		{"fractional port", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["port"] = 1.5 }},
		{"string port", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["port"] = "19102" }},
		{"upstream credentials", func(p map[string]any) { p["nodes"].([]any)[0].(map[string]any)["password"] = "hidden" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			var p map[string]any
			if e := json.Unmarshal(s.ProviderSnapshots[1], &p); e != nil {
				t.Fatal(e)
			}
			tt.change(p)
			s.ProviderSnapshots[1], _ = json.Marshal(p)
			c, e := Compile(s)
			if e == nil || !reflect.DeepEqual(c, Candidate{}) {
				t.Fatalf("accepted %s: %v", tt.name, e)
			}
		})
	}
	for name, raw := range map[string][]byte{
		"nested duplicate": []byte(`{"schema":"hoopa-routing-provider-fixture-v1","provider_id":"p2","revision":"r","nodes":[{"node_id":"n2","node_id":"n2","display_name":"a","region":"us","type":"http","server":"127.0.0.1","port":19102}]}`),
		"nesting excess":   []byte(`{"extra":` + strings.Repeat("[", 10) + `1` + strings.Repeat("]", 10) + `}`),
	} {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			s.ProviderSnapshots[1] = raw
			c, e := Compile(s)
			if e == nil || !reflect.DeepEqual(c, Candidate{}) {
				t.Fatal("accepted strict JSON violation")
			}
		})
	}
}

func TestSnapshotOrderHashAndCallerOwnership(t *testing.T) {
	s := fixture()
	var p map[string]any
	if e := json.Unmarshal(s.ProviderSnapshots[0], &p); e != nil {
		t.Fatal(e)
	}
	node := map[string]any{"node_id": "aaa", "display_name": "😀 renamed", "region": "eu", "type": "socks5", "server": "127.0.0.1", "port": 19103}
	p["nodes"] = append(p["nodes"].([]any), node)
	s.ProviderSnapshots[0], _ = json.Marshal(p)
	inputBefore, _ := json.Marshal(s)
	c := compileOK(t, s)
	inputAfter, _ := json.Marshal(s)
	if !bytes.Equal(inputBefore, inputAfter) {
		t.Fatal("Compile mutated caller input")
	}
	nodes := p["nodes"].([]any)
	nodes[0], nodes[1] = nodes[1], nodes[0]
	s.ProviderSnapshots[0], _ = json.Marshal(p)
	reordered := compileOK(t, s)
	if !bytes.Equal(c.Config, reordered.Config) || !bytes.Equal(c.RouteMap, reordered.RouteMap) || !reflect.DeepEqual(c.Rules, reordered.Rules) {
		t.Fatal("node order changed intent")
	}
	if bytes.Equal(c.Provenance, reordered.Provenance) {
		t.Fatal("exact bytes hash lost input order")
	}
	s.ProviderSnapshots[0] = append([]byte(" \n"), s.ProviderSnapshots[0]...)
	whitespace := compileOK(t, s)
	if !bytes.Equal(whitespace.Config, c.Config) || bytes.Equal(whitespace.Provenance, reordered.Provenance) {
		t.Fatal("whitespace hashing")
	}
}

func TestSingleIPRuleAndOverlappingOrder(t *testing.T) {
	s := fixture()
	s.Rules[0].Conditions = []Condition{{Kind: "DOMAIN-SUFFIX", Value: "test"}}
	s.Rules[1].Conditions = []Condition{{Kind: "IP-CIDR", Value: "127.0.0.1/32"}}
	c := compileOK(t, s)
	cfg, _, e := capabilityprofile.ValidateDeclaredProgramIdentity(c.Config, s.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Rules[0] != "DOMAIN-SUFFIX,test,group:active" || cfg.Rules[1] != "IP-CIDR,127.0.0.1/32,"+c.Rules[1].InternalTarget+",no-resolve" {
		t.Fatal("ordered rule text", cfg.Rules)
	}
	s.Rules[1].Conditions = []Condition{{Kind: "DOMAIN", Value: "alpha.test"}}
	overlap := compileOK(t, s)
	if overlap.Rules[0].ID != "app" || overlap.Rules[1].ID != "pin" {
		t.Fatal("overlap reordered")
	}
	s.Rules[0], s.Rules[1] = s.Rules[1], s.Rules[0]
	swapped := compileOK(t, s)
	if swapped.Rules[0].ID != "pin" || bytes.Equal(swapped.Config, overlap.Config) {
		t.Fatal("rule order not retained")
	}
}

func TestProvenanceAndSelectionReplacement(t *testing.T) {
	s := fixture()
	c := compileOK(t, s)
	expected := []nodeEvidence{
		{"node:4a2534550d99c9430ee1a70a2ebb4b5265cc1f74c679e1c0dfbac7b2c55be95a", "p1", "n1", "rev-1", digest(s.ProviderSnapshots[0])},
		{"node:8accd2e33896aef86a1a73a2a5964f73cf27888de7c931babf0aad10871b37b8", "p2", "n2", "rev-1", digest(s.ProviderSnapshots[1])},
	}
	var p provenance
	if e := json.Unmarshal(c.Provenance, &p); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(p.Nodes, expected) {
		t.Fatalf("provenance binding: %+v", p.Nodes)
	}
	s.Groups[0].ProviderID = "p2"
	s.Groups[0].Selected = NodeRef{"p2", "n2"}
	replacement := compileOK(t, s)
	if replacement.GroupSelections[0].InternalNodeID != expected[1].InternalID || replacement.ConfigSHA256 == c.ConfigSHA256 {
		t.Fatal("selection did not replace complete config")
	}
	if c.GroupSelections[0].InternalNodeID != expected[0].InternalID {
		t.Fatal("replacement mutated old candidate")
	}
}

func TestUnusedSourcesAndGroupsStillValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Spec)
	}{
		{"unused source", func(s *Spec) { s.ProviderSnapshots = append(s.ProviderSnapshots, []byte(`{}`)) }},
		{"unreachable group", func(s *Spec) {
			s.Rules = []Rule{{ID: "only", Match: true, Target: Target{Reject: true}}}
			s.Groups[0].Selected = NodeRef{}
		}},
		{"shadowed pin", func(s *Spec) {
			s.Rules[0].Conditions = []Condition{{Kind: "DOMAIN-SUFFIX", Value: "test"}}
			s.Rules[1].Target.Node.NodeID = "missing"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			tt.change(&s)
			c, e := Compile(s)
			if e == nil || !reflect.DeepEqual(c, Candidate{}) {
				t.Fatal("accepted unused invalid source/reference")
			}
		})
	}
}

func TestProgramConditionUTF8BeforeEncoding(t *testing.T) {
	tests := []struct {
		name, kind, value string
		valid             bool
	}{
		{"invalid name", "PROCESS-NAME", "curl\xff", false},
		{"invalid path", "PROCESS-PATH", "/bin/curl\xff", false},
		{"unicode name", "PROCESS-NAME", "程序", true},
		{"unicode path", "PROCESS-PATH", "/程序/工具", true},
		{"literal replacement name", "PROCESS-NAME", "curl\ufffd", true},
		{"literal replacement path", "PROCESS-PATH", "/bin/curl\ufffd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fixture()
			s.ProgramRules = true
			s.Requirements.RequiredFields = []string{"program_name", "program_path"}
			s.Rules[0].Conditions = []Condition{{Kind: tt.kind, Value: tt.value}}
			c, e := Compile(s)
			if !tt.valid {
				if e == nil || !reflect.DeepEqual(c, Candidate{}) {
					t.Fatal("accepted malformed UTF-8 condition")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			cfg, _, e := capabilityprofile.ValidateDeclaredProgramIdentity(c.Config, s.Generation)
			if e != nil {
				t.Fatal(e)
			}
			if cfg.Rules[0] != tt.kind+","+tt.value+",group:active" || c.Rules[0].Conditions[0].Value != tt.value {
				t.Fatal("valid Unicode condition changed", cfg.Rules[0])
			}
		})
	}
}
