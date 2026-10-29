package mihomoevents

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func qualityBundleFixture() QualityBundleSpec {
	return QualityBundleSpec{Generation: "generation:quality-1", EntryEndpoint: "127.0.0.1:10001", HealthEndpoint: "127.0.0.1:10002", ControllerEndpoint: "127.0.0.1:10003", ControllerSecret: "local-fixture", Nodes: []QualityNode{{ID: "node:a", Type: "http", Endpoint: "127.0.0.1:10004", DisplayAlias: "A"}, {ID: "node:b", Type: "socks5", Endpoint: "127.0.0.1:10005", DisplayAlias: "B"}, {ID: "node:direct", Type: "direct", DisplayAlias: "Direct"}, {ID: "node:nested", Type: "http", Endpoint: "127.0.0.1:10004", DisplayAlias: "Nested", DialerProxy: "node:b"}}, Groups: []QualityGroup{{ID: "group:main", DisplayAlias: "Main", Members: []string{"node:a", "node:b", "node:nested"}}}, DefaultTarget: "group:main", DirectTarget: "node:direct"}
}
func TestQualityBundleStableIdentityAcrossDisplayRename(t *testing.T) {
	s := qualityBundleFixture()
	first, err := BuildQualityBundle(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Nodes[0].DisplayAlias = "Renamed A"
	s.Groups[0].DisplayAlias = "Renamed Group"
	second, err := BuildQualityBundle(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Config, second.Config) || bytes.Equal(first.RouteMap, second.RouteMap) {
		t.Fatal("display rename changed core names or lost alias metadata")
	}
	var mapping struct {
		SchemaVersion int               `json:"schema_version"`
		Entries       []qualityMapEntry `json:"entries"`
	}
	if json.Unmarshal(second.RouteMap, &mapping) != nil || mapping.SchemaVersion != 1 || len(mapping.Entries) != 5 {
		t.Fatal("mapping shape")
	}
	for _, e := range mapping.Entries {
		if e.ProviderID != nil || e.ProviderName != "" || (e.NodeID == nil) == (e.GroupID == nil) {
			t.Fatal("inline identity mismatch")
		}
		if e.NodeID != nil && *e.NodeID != e.InternalName || e.GroupID != nil && *e.GroupID != e.InternalName {
			t.Fatal("stable ID not core name")
		}
	}
	s.Generation = "generation:quality-2"
	third, err := BuildQualityBundle(s)
	if err != nil || !bytes.Equal(second.Config, third.Config) {
		t.Fatal("new generation changed persistent identity")
	}
}
func TestQualityBundleRejectsInvalidRefsTypesAndEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualityBundleSpec)
	}{
		{"duplicate-node", func(s *QualityBundleSpec) { s.Nodes[1].ID = s.Nodes[0].ID }},
		{"unknown-member", func(s *QualityBundleSpec) { s.Groups[0].Members[0] = "node:missing" }},
		{"duplicate-member", func(s *QualityBundleSpec) { s.Groups[0].Members[1] = s.Groups[0].Members[0] }},
		{"self-cycle", func(s *QualityBundleSpec) { s.Nodes[0].DialerProxy = s.Nodes[0].ID }},
		{"group-cycle", func(s *QualityBundleSpec) {
			s.Groups[0].Members = []string{"node:a"}
			s.Nodes[0].DialerProxy = s.Groups[0].ID
		}},
		{"unknown-dialer", func(s *QualityBundleSpec) { s.Nodes[0].DialerProxy = "node:missing" }},
		{"bad-type", func(s *QualityBundleSpec) { s.Nodes[0].Type = "unknown" }},
		{"remote-endpoint", func(s *QualityBundleSpec) { s.Nodes[0].Endpoint = "192.0.2.1:80" }},
		{"dns-endpoint", func(s *QualityBundleSpec) { s.Nodes[0].Endpoint = "localhost:80" }},
		{"zero-port", func(s *QualityBundleSpec) { s.EntryEndpoint = "127.0.0.1:0" }},
		{"same-listener", func(s *QualityBundleSpec) { s.HealthEndpoint = s.EntryEndpoint }},
		{"remote-controller", func(s *QualityBundleSpec) { s.ControllerEndpoint = "0.0.0.0:8080" }},
		{"direct-endpoint", func(s *QualityBundleSpec) { s.Nodes[2].Endpoint = "127.0.0.1:80" }},
		{"bad-id", func(s *QualityBundleSpec) { s.Nodes[0].ID = "A display name" }},
		{"empty-alias", func(s *QualityBundleSpec) { s.Nodes[0].DisplayAlias = "" }},
		{"missing-target", func(s *QualityBundleSpec) { s.DefaultTarget = "group:missing" }},
		{"non-direct-target", func(s *QualityBundleSpec) { s.DirectTarget = "node:a" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qualityBundleFixture()
			tc.mutate(&s)
			bundle, err := BuildQualityBundle(s)
			if err != ErrQualityBundle || !reflect.DeepEqual(bundle, QualityBundle{}) {
				t.Fatal("invalid bundle accepted", err)
			}
		})
	}
}

func TestQualityBundleActualChainHopBoundary(t *testing.T) {
	for _, hops := range []int{8, 9} {
		s := qualityBundleFixture()
		s.Groups = nil
		target := "node:direct"
		for i := 1; i < hops; i++ {
			id := "group:depth-" + strconv.Itoa(i)
			s.Groups = append(s.Groups, QualityGroup{ID: id, DisplayAlias: id, Members: []string{target}})
			target = id
		}
		s.DefaultTarget = target
		bundle, err := BuildQualityBundle(s)
		if hops == 8 && (err != nil || len(bundle.Config) == 0) {
			t.Fatal("valid eight-hop chain rejected", err)
		}
		if hops == 9 && (err != ErrQualityBundle || !reflect.DeepEqual(bundle, QualityBundle{})) {
			t.Fatal("nine-hop chain accepted", err)
		}
	}
}
