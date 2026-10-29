package routingcandidate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func providerSwitchFixture() Spec {
	s := fixture()
	s.Generation = ""
	s.Groups = []Group{{"group:US", "US", "active", "us", NodeRef{"p1", "n1"}}}
	s.Rules[0].Target = Target{GroupID: "group:US"}
	s.Rules[1].Conditions = []Condition{{Kind: "app", Value: "app-b"}}
	s.Rules[1].Target = Target{Node: NodeRef{"p1", "n1"}}
	return s
}

func compileSwitchTemplate(t *testing.T, s Spec) Candidate {
	t.Helper()
	c, err := CompileControlTemplate(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Config)+len(c.RouteMap) > 8192 {
		t.Fatal("minimal switch fixture exceeds the existing control budget")
	}
	return c
}

func TestControlTemplateAliasOnlyKeepsRoutingIdentity(t *testing.T) {
	s := providerSwitchFixture()
	before := compileSwitchTemplate(t, s)
	oldSnapshot := append([]byte(nil), s.ProviderSnapshots[0]...)
	// Keep provider, node, revision and endpoint exact. The older rename test
	// also changed the endpoint, which could hide an alias-only regression.
	s.ProviderSnapshots[0] = snapshot("p1", "n1", "Renamed only", 19101)
	after := compileSwitchTemplate(t, s)
	if !bytes.Equal(before.Config, after.Config) || before.ConfigSHA256 != after.ConfigSHA256 ||
		!reflect.DeepEqual(before.Rules, after.Rules) || !reflect.DeepEqual(before.GroupSelections, after.GroupSelections) {
		t.Fatal("a display alias changed routing configuration or stable choices")
	}
	if bytes.Equal(before.RouteMap, after.RouteMap) || bytes.Equal(before.Provenance, after.Provenance) {
		t.Fatal("the replacement bundle must preserve the new alias and snapshot provenance")
	}
	var oldMap, newMap routeMap
	if json.Unmarshal(before.RouteMap, &oldMap) != nil || json.Unmarshal(after.RouteMap, &newMap) != nil {
		t.Fatal("invalid route-map output")
	}
	changed := 0
	for i := range oldMap.Entries {
		if oldMap.Entries[i].InternalName == before.Rules[1].InternalTarget {
			if oldMap.Entries[i].DisplayAlias != "Shared" || newMap.Entries[i].DisplayAlias != "Renamed only" {
				t.Fatal("wrong node alias changed")
			}
			oldMap.Entries[i].DisplayAlias = "Renamed only"
			changed++
		}
	}
	if changed != 1 || !reflect.DeepEqual(oldMap, newMap) {
		t.Fatal("route-map changed beyond the one display alias")
	}
	var oldProvenance, newProvenance provenance
	if json.Unmarshal(before.Provenance, &oldProvenance) != nil || json.Unmarshal(after.Provenance, &newProvenance) != nil {
		t.Fatal("invalid provenance")
	}
	oldHash, newHash := digest(oldSnapshot), digest(s.ProviderSnapshots[0])
	if oldHash == newHash {
		t.Fatal("fixture did not change the original snapshot")
	}
	for i := range oldProvenance.Snapshots {
		if oldProvenance.Snapshots[i].ProviderID == "p1" {
			if oldProvenance.Snapshots[i].SHA256 != oldHash {
				t.Fatal("original snapshot provenance differs")
			}
			oldProvenance.Snapshots[i].SHA256 = newHash
		}
	}
	for i := range oldProvenance.Nodes {
		if oldProvenance.Nodes[i].ProviderID == "p1" {
			if oldProvenance.Nodes[i].SHA256 != oldHash {
				t.Fatal("original node provenance differs")
			}
			oldProvenance.Nodes[i].SHA256 = newHash
		}
	}
	if !reflect.DeepEqual(oldProvenance, newProvenance) {
		t.Fatal("alias-only revision rebound stable provider or node identity")
	}
}

func TestControlTemplateActiveProviderKeepsOtherProviderPin(t *testing.T) {
	s := providerSwitchFixture()
	before := compileSwitchTemplate(t, s)
	s.ActiveProviderID = "p2"
	s.Groups[0].Selected = NodeRef{"p2", "n2"}
	input, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	after := compileSwitchTemplate(t, s)
	unchanged, _ := json.Marshal(s)
	if !bytes.Equal(input, unchanged) {
		t.Fatal("compile mutated caller snapshots or selections")
	}
	pin := after.Rules[1].InternalTarget
	selected := after.GroupSelections[0]
	if pin != before.Rules[1].InternalTarget || selected.Selected != (NodeRef{"p2", "n2"}) ||
		selected.ProviderID != "p2" || selected.InternalNodeID == pin || selected.InternalNodeID == before.GroupSelections[0].InternalNodeID {
		t.Fatal("active source change rebound the pin or failed to change the group")
	}
	var mapping routeMap
	if json.Unmarshal(after.RouteMap, &mapping) != nil {
		t.Fatal("invalid inline mapping")
	}
	for _, entry := range mapping.Entries {
		if entry.ProviderID != nil || entry.ProviderName != "" {
			t.Fatal("compilation provenance masquerades as observed provider identity")
		}
	}
	for _, test := range []struct {
		name   string
		change func(*Spec)
	}{
		{"remove inactive pinned provider", func(s *Spec) { s.ProviderSnapshots = s.ProviderSnapshots[1:] }},
		{"replace inactive pinned node with same alias", func(s *Spec) {
			s.ProviderSnapshots[0] = snapshot("p1", "replacement", "Shared", 19101)
		}},
		{"remove provider referenced by shadowed rule", func(s *Spec) {
			s.Rules[0].Conditions = []Condition{{Kind: "app", Value: "app-b"}}
			s.ProviderSnapshots = s.ProviderSnapshots[1:]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var invalid Spec
			if json.Unmarshal(input, &invalid) != nil {
				t.Fatal("fixture clone")
			}
			test.change(&invalid)
			original, _ := json.Marshal(invalid)
			candidate, err := CompileControlTemplate(invalid)
			current, _ := json.Marshal(invalid)
			if err == nil || !reflect.DeepEqual(candidate, Candidate{}) || !bytes.Equal(original, current) {
				t.Fatal("invalid pin accepted, partial candidate returned, or input mutated")
			}
			if !reflect.DeepEqual(after, compileSwitchTemplate(t, s)) {
				t.Fatal("rejection changed the independently retained valid candidate")
			}
		})
	}
}
