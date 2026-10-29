package policy

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func TestGroupsReferencedMetadataValidation(t *testing.T) {
	cases := map[string]func(*subscription.Snapshot){
		"snapshot-id-empty":   func(s *subscription.Snapshot) { s.ID = "" },
		"snapshot-id-invalid": func(s *subscription.Snapshot) { s.ID = "bad snapshot" },
		"key-id-empty":        func(s *subscription.Snapshot) { s.KeyID = "" },
		"source-id-wrong":     func(s *subscription.Snapshot) { s.SourceID = "b" },
		"duplicate-node-id":   func(s *subscription.Snapshot) { s.Nodes = append(s.Nodes, s.Nodes[0]) },
		"empty-node-id":       func(s *subscription.Snapshot) { s.Nodes[0].ID = "" },
		"invalid-node-id":     func(s *subscription.Snapshot) { s.Nodes[0].ID = "bad node" },
		"unknown-with-code": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "unknown", Code: "US"}
		},
		"unknown-with-rule": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "unknown", Rule: "US "}
		},
		"ambiguous-with-code": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "ambiguous", Code: "US"}
		},
		"ambiguous-with-rule": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "ambiguous", Rule: "US "}
		},
		"declared-without-code": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "declared-prefix", Rule: "US "}
		},
		"declared-without-rule": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "declared-prefix", Code: "US"}
		},
		"invented-evidence-kind": func(s *subscription.Snapshot) {
			s.Nodes[0].Region = subscription.RegionEvidence{Kind: "guessed", Code: "US", Rule: "US "}
		},
		"empty-evidence-kind": func(s *subscription.Snapshot) { s.Nodes[0].Region = subscription.RegionEvidence{} },
		"oversize-code":       func(s *subscription.Snapshot) { s.Nodes[0].Region.Code = strings.Repeat("X", 65) },
		"oversize-rule":       func(s *subscription.Snapshot) { s.Nodes[0].Region.Rule = strings.Repeat("X", 257) },
		"control-code":        func(s *subscription.Snapshot) { s.Nodes[0].Region.Code = "US\n" },
		"control-rule":        func(s *subscription.Snapshot) { s.Nodes[0].Region.Rule = "US\x7f" },
		"invalid-utf8-code":   func(s *subscription.Snapshot) { s.Nodes[0].Region.Code = string([]byte{0xff}) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sources := groupSources(false)
			s := sources["a"]
			mutate(&s)
			sources["a"] = s
			for _, g := range []config.Group{groupAuto("auto", nil), groupManual("manual", "a", "a-us-1")} {
				output, err := ResolveGroups(groupID("a"), []config.Group{g}, sources)
				if !errors.Is(err, ErrGroups) || output != nil {
					t.Fatal("referenced malformed metadata accepted or partial result returned")
				}
			}
		})
	}
}
func TestGroupsUnusedSourcesAndActiveMetadata(t *testing.T) {
	sources := groupSources(false)
	sources["unused"] = subscription.Snapshot{SourceID: "wrong", Nodes: []subscription.Node{{ID: "bad id"}}}
	// An explicit source does not depend on an unprovided active snapshot.
	result := mustGroups(t, groupID("not-fetched"), []config.Group{groupManual("manual", "a", "a-us-1")}, sources)
	if result["manual"].SourceID != "a" {
		t.Fatal("unused active source changed explicit source")
	}
	if out, err := ResolveGroups(groupID("bad active id"), nil, sources); !errors.Is(err, ErrGroups) || out != nil {
		t.Fatal("invalid active ID accepted")
	}
	if out, err := ResolveGroups(nil, nil, sources); err != nil || len(out) != 0 {
		t.Fatal("empty independent groups failed")
	}
	// Requiring active makes the exact missing source relevant, without fallback.
	rejectGroups(t, groupID("not-fetched"), []config.Group{groupAuto("auto", nil)}, sources)
	// Synthetic source metadata is enough here; it is intentionally not private
	// snapshot authority. The compiler owns Snapshot.Validate authentication.
	if sources["a"].Validate() == nil {
		t.Fatal("fixture unexpectedly genuine")
	}
	mustGroups(t, nil, []config.Group{groupManual("manual", "a", "a-us-1")}, sources)
}
func TestGroupsAmbiguousRegionsNeverGuessed(t *testing.T) {
	sources := groupSources(false)
	s := sources["a"]
	s.Nodes[0].Region = subscription.RegionEvidence{Kind: "ambiguous"}
	sources["a"] = s
	all := mustGroups(t, groupID("a"), []config.Group{groupAuto("all", nil), groupAuto("US", groupRegion("US"))}, sources)
	if len(all["all"].Candidates) != 4 || !reflect.DeepEqual(all["US"].Candidates, groupRefs("a", "us-1")) {
		t.Fatal("ambiguous region guessed or unconstrained candidate dropped")
	}
	manual := groupManual("manual", "a", "a-us-2")
	mustGroups(t, nil, []config.Group{manual}, sources)
	manual.Region = groupRegion("US")
	rejectGroups(t, nil, []config.Group{manual}, sources)
}
func TestGroupsDeepFollowGraph(t *testing.T) {
	const depth = 20000
	definitions := make([]config.Group, 0, depth+1)
	for i := depth; i > 0; i-- {
		parent := config.ID("root")
		if i > 1 {
			parent = config.ID(fmt.Sprintf("f-%d", i-1))
		}
		definitions = append(definitions, groupFollow(config.ID(fmt.Sprintf("f-%d", i)), parent, true))
	}
	definitions = append(definitions, groupManual("root", "a", "a-us-1"))
	out := mustGroups(t, nil, definitions, groupSources(false))
	last := out[config.ID(fmt.Sprintf("f-%d", depth))]
	if last.SourceID != "a" || last.Selected == nil || last.Selected.NodeID != "a-us-1" || last.Reject || !reflect.DeepEqual(last.Candidates, groupRefs("a", "us-1")) {
		t.Fatal("deep follow chain changed exact pin")
	}
	last.Candidates[0].NodeID = "changed"
	last.Selected.NodeID = "selected-changed"
	if out["root"].Candidates[0].NodeID != "a-us-1" || out["root"].Selected.NodeID != "a-us-1" {
		t.Fatal("deep follow output aliases ancestor")
	}
	// A deep cycle, even with an independently resolvable root, returns no partial
	// output and does not recurse through the dependency chain.
	*definitions[len(definitions)-2].Follows = config.ID(fmt.Sprintf("f-%d", depth))
	rejectGroups(t, nil, definitions, groupSources(false))
}
func TestGroupsInputAliasesAndResultIndependence(t *testing.T) {
	first := groupAuto("first", groupRegion("US"))
	second := first
	second.ID = "second"
	definitions := []config.Group{first, second, groupFollow("follow-first", "first", true), groupFollow("follow-second", "first", true), groupManual("manual", "a", "a-us-1")}
	sources := groupSources(false)
	active := groupID("a")
	out := mustGroups(t, active, definitions, sources)
	out["first"].Probe.URL = "https://changed.invalid/"
	out["first"].Candidates[0].NodeID = "changed"
	*out["follow-first"].Follows = "changed"
	if out["second"].Probe.URL != groupProbe().URL || out["second"].Candidates[0].NodeID != "a-us-1" || out["follow-second"].Candidates[0].NodeID != "a-us-1" || *out["follow-second"].Follows != "first" {
		t.Fatal("separate outputs share mutable objects")
	}
	manual := out["manual"]
	manual.Candidates[0].NodeID = "changed"
	if manual.Selected.NodeID != "a-us-1" {
		t.Fatal("manual selected pointer aliases own candidate slice")
	}
	// Mutate aliased caller objects after the calculation; independent outputs
	// must retain their copied source IDs, pins, probe and follow references.
	first.Probe.URL = "https://input-changed.invalid/"
	*first.Region = "JP"
	first.Source.Kind = "broken"
	*active = "b"
	snapshot := sources["a"]
	snapshot.Nodes[3].ID = "input-node-changed"
	sources["a"] = snapshot
	if out["second"].Probe.URL != groupProbe().URL || out["second"].SourceID != "a" || out["second"].Candidates[0].NodeID != "a-us-1" || out["manual"].Selected.NodeID != "a-us-1" {
		t.Fatal("caller alias mutation changed resolved values")
	}
}
func TestGroupsNodeIDsOpaqueWithinExactSource(t *testing.T) {
	// Synthetic group calculation cannot infer source identity from an opaque node
	// ID spelling; the source+node tuple is authoritative for this module.
	sources := groupSources(false)
	a := sources["a"]
	b := sources["b"]
	b.Nodes = a.Nodes
	sources["b"] = b
	out := mustGroups(t, nil, []config.Group{groupManual("manual-a", "a", "a-us-1"), groupManual("manual-b", "b", "a-us-1")}, sources)
	if out["manual-a"].Selected.SubscriptionID != "a" || out["manual-b"].Selected.SubscriptionID != "b" {
		t.Fatal("opaque node ID spelling rebound source")
	}
	out["manual-a"].Selected.SubscriptionID = "changed"
	if out["manual-b"].Selected.SubscriptionID != "b" || sources["b"].Nodes[3].ID != "a-us-1" {
		t.Fatal("input/shared snapshots leaked mutable output alias")
	}
}
func TestGroupsTypedShapesAndConstantErrors(t *testing.T) {
	for _, g := range []config.Group{{}, groupFollow("f", "bad target", false), groupManual("m", "a", "bad node"), groupAuto("auto", groupRegion(string([]byte{0xff})))} {
		out, err := ResolveGroups(groupID("a"), []config.Group{g}, groupSources(false))
		if !errors.Is(err, ErrGroups) || out != nil {
			t.Fatal("invalid typed group accepted")
		}
	}
	const secret = "synthetic-private-probe-token"
	group := groupAuto("auto", nil)
	group.Probe.URL = "ftp://" + secret + ".invalid/"
	out, err := ResolveGroups(groupID("a"), []config.Group{group}, groupSources(false))
	if err != ErrGroups || out != nil {
		t.Fatal("wrong error classification")
	}
	for _, message := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(message, secret) {
			t.Fatal("group error leaked input")
		}
	}
}
func TestGroupsTypedProbeValidation(t *testing.T) {
	valid := *groupProbe()
	valid.IntervalSeconds = uint64(math.MaxInt64 / int64(time.Second))
	valid.TimeoutMS = uint64(math.MaxInt64 / int64(time.Millisecond))
	valid.ToleranceMS = valid.TimeoutMS
	if valid.Validate() != nil {
		t.Fatal("representable duration boundary rejected")
	}
	for _, url := range []string{"https://probe.invalid/check?status=204", "http://localhost:1/", "https://[2001:db8::1]:65535/"} {
		p := *groupProbe()
		p.URL = url
		if p.Validate() != nil {
			t.Fatal("valid typed probe rejected")
		}
		g := groupAuto("auto", nil)
		g.Probe = &p
		mustGroups(t, groupID("a"), []config.Group{g}, groupSources(false))
	}
	bad := []func(*config.Probe){
		func(p *config.Probe) { p.IntervalSeconds = math.MaxUint64 }, func(p *config.Probe) { p.TimeoutMS = math.MaxUint64 }, func(p *config.Probe) { p.ToleranceMS = math.MaxUint64 },
		func(p *config.Probe) { p.IntervalSeconds = valid.IntervalSeconds + 1 }, func(p *config.Probe) { p.TimeoutMS = valid.TimeoutMS + 1 }, func(p *config.Probe) { p.ToleranceMS = valid.ToleranceMS + 1 },
	}
	for _, url := range []string{"https://probe.invalid:0/", "https://probe.invalid:65536/", "https://probe.invalid:/", "https://[probe.invalid]/", "https://bad_host.invalid/", "https://user:secret@probe.invalid/", "https://probe.invalid/#", "https://probe.invalid/\x7f", "https://probe.invalid/" + string([]byte{0xff})} {
		url := url
		bad = append(bad, func(p *config.Probe) { p.URL = url })
	}
	for _, mutate := range bad {
		p := *groupProbe()
		mutate(&p)
		if !errors.Is(p.Validate(), config.ErrIntent) {
			t.Fatal("invalid typed probe accepted")
		}
		g := groupAuto("auto", nil)
		g.Probe = &p
		rejectGroups(t, groupID("a"), []config.Group{g}, groupSources(false))
	}
}

func TestGroupsConcurrentIndependentResults(t *testing.T) {
	definitions := []config.Group{groupAuto("US", groupRegion("US")), groupManual("manual", "a", "a-us-1"), groupFollow("follow", "manual", true)}
	sources := groupSources(false)
	active := groupID("a")
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			out, err := ResolveGroups(active, definitions, sources)
			if err != nil {
				t.Error("concurrent resolution failed")
				return
			}
			out["US"].Probe.URL = "https://changed.invalid/"
			out["US"].Candidates[0].NodeID = "changed"
			out["manual"].Selected.NodeID = "changed"
			*out["follow"].Follows = "changed"
		}()
	}
	workers.Wait()
	if definitions[0].Probe.URL != groupProbe().URL || sources["a"].Nodes[3].ID != "a-us-1" || *definitions[2].Follows != "manual" {
		t.Fatal("concurrent outputs shared mutable input")
	}
}
