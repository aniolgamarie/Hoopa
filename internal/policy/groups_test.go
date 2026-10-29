package policy

// T036 definitions use the planned T037/T040 API. Synthetic snapshots below
// test pure group resolution, not provider validation, probes or core selection.
import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func groupID(id config.ID) *config.ID   { return &id }
func groupRegion(region string) *string { return &region }
func groupProbe() *config.Probe {
	return &config.Probe{URL: "https://probe.invalid/check", IntervalSeconds: 60, TimeoutMS: 1000, ToleranceMS: 0}
}
func groupActive() *config.GroupSource { return &config.GroupSource{Kind: "active"} }
func groupSource(id config.ID) *config.GroupSource {
	return &config.GroupSource{Kind: "subscription", SubscriptionID: groupID(id)}
}
func groupAuto(id config.ID, region *string) config.Group {
	return config.Group{ID: id, Required: true, Region: region, Strategy: "auto", Source: groupActive(), Probe: groupProbe()}
}
func groupManual(id, source, node config.ID) config.Group {
	return config.Group{ID: id, Required: true, Strategy: "manual", Source: groupSource(source), SelectedNodeID: groupID(node)}
}
func groupFollow(id, target config.ID, required bool) config.Group {
	return config.Group{ID: id, Required: required, Strategy: "follow", Follows: groupID(target)}
}
func groupSources(reverse bool) map[config.ID]subscription.Snapshot {
	makeSnapshot := func(source config.ID) subscription.Snapshot {
		node := func(suffix, region string) subscription.Node {
			evidence := subscription.RegionEvidence{Kind: "unknown"}
			if region != "" {
				evidence = subscription.RegionEvidence{Code: region, Kind: "declared-prefix", Rule: region + " "}
			}
			return subscription.Node{ID: config.ID(string(source) + "-" + suffix), DisplayName: "shared", Region: evidence}
		}
		nodes := []subscription.Node{node("us-2", "US"), node("unknown", ""), node("jp", "JP"), node("us-1", "US")}
		if reverse {
			for a, b := 0, len(nodes)-1; a < b; a, b = a+1, b-1 {
				nodes[a], nodes[b] = nodes[b], nodes[a]
			}
		}
		return subscription.Snapshot{ID: config.ID("snapshot-" + string(source)), SourceID: source, KeyID: "synthetic-key", Nodes: nodes}
	}
	sources := map[config.ID]subscription.Snapshot{}
	if reverse {
		sources["b"] = makeSnapshot("b")
		sources["a"] = makeSnapshot("a")
	} else {
		sources["a"] = makeSnapshot("a")
		sources["b"] = makeSnapshot("b")
	}
	return sources
}
func groupRefs(source config.ID, suffixes ...string) []subscription.NodeRef {
	refs := make([]subscription.NodeRef, 0, len(suffixes))
	for _, suffix := range suffixes {
		refs = append(refs, subscription.NodeRef{SubscriptionID: source, NodeID: config.ID(string(source) + "-" + suffix)})
	}
	return refs
}
func mustGroups(t *testing.T, active *config.ID, groups []config.Group, sources map[config.ID]subscription.Snapshot) map[config.ID]ResolvedGroup {
	t.Helper()
	result, err := ResolveGroups(active, groups, sources)
	if err != nil || len(result) != len(groups) {
		t.Fatal("valid group resolution failed (details withheld)")
	}
	return result
}
func rejectGroups(t *testing.T, active *config.ID, groups []config.Group, sources map[config.ID]subscription.Snapshot) {
	t.Helper()
	result, err := ResolveGroups(active, groups, sources)
	if err == nil || len(result) != 0 {
		t.Fatal("invalid groups accepted or partial resolution returned")
	}
}

func TestGroupsActiveSwitchAndStablePins(t *testing.T) {
	definitions := []config.Group{
		groupFollow("US-follow-2", "US-follow", true), groupFollow("US-follow", "US", true),
		groupAuto("BEST", nil), groupAuto("JP", groupRegion("JP")), groupAuto("US", groupRegion("US")),
		groupManual("fixed-a", "a", "a-us-1"), groupFollow("fixed-follow", "fixed-a", true),
	}
	for _, reverse := range []bool{false, true} {
		sources := groupSources(reverse)
		for _, active := range []config.ID{"a", "b"} {
			resolved := mustGroups(t, &active, definitions, sources)
			for id, suffixes := range map[config.ID][]string{"US": {"us-1", "us-2"}, "JP": {"jp"}, "BEST": {"jp", "unknown", "us-1", "us-2"}} {
				g := resolved[id]
				if g.ID != id || g.Strategy != "auto" || g.SourceID != active || g.Selected != nil || g.Reject || !reflect.DeepEqual(g.Candidates, groupRefs(active, suffixes...)) || !reflect.DeepEqual(g.Probe, groupProbe()) {
					t.Fatal("active auto group not source-correct static intent")
				}
			}
			for id, target := range map[config.ID]config.ID{"US-follow": "US", "US-follow-2": "US-follow", "fixed-follow": "fixed-a"} {
				g, want := resolved[id], resolved[target]
				if g.ID != id || g.Strategy != "follow" || g.Follows == nil || *g.Follows != target || g.SourceID != want.SourceID || !reflect.DeepEqual(g.Candidates, want.Candidates) || !reflect.DeepEqual(g.Selected, want.Selected) || g.Reject != want.Reject || g.Probe != nil {
					t.Fatal("follow chain diverged from exact target")
				}
			}
			fixed := resolved["fixed-a"]
			want := groupRefs("a", "us-1")
			if fixed.ID != "fixed-a" || fixed.Strategy != "manual" || fixed.SourceID != "a" || fixed.Reject || !reflect.DeepEqual(fixed.Candidates, want) || fixed.Selected == nil || *fixed.Selected != want[0] {
				t.Fatal("explicit manual pin rebound with active source")
			}
		}
	}
	// Explicit sources require no active subscription at all.
	mustGroups(t, nil, []config.Group{groupManual("fixed-a", "a", "a-us-1")}, groupSources(false))
}

func TestGroupsMissingReferencesNeverFallback(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(*config.Group, map[config.ID]subscription.Snapshot)
	}{
		{"missing-node", func(g *config.Group, s map[config.ID]subscription.Snapshot) { g.SelectedNodeID = groupID("missing") }},
		{"wrong-source-node", func(g *config.Group, s map[config.ID]subscription.Snapshot) { g.SelectedNodeID = groupID("b-us-1") }},
		{"missing-source", func(g *config.Group, s map[config.ID]subscription.Snapshot) { delete(s, "a") }},
		{"snapshot-source-mismatch", func(g *config.Group, s map[config.ID]subscription.Snapshot) {
			v := s["a"]
			v.SourceID = "b"
			s["a"] = v
		}},
		{"region-mismatch", func(g *config.Group, s map[config.ID]subscription.Snapshot) { g.Region = groupRegion("JP") }},
		{"unknown-region", func(g *config.Group, s map[config.ID]subscription.Snapshot) {
			g.SelectedNodeID = groupID("a-unknown")
			g.Region = groupRegion("US")
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			g := groupManual("manual", "a", "a-us-1")
			g.Required = false
			sources := groupSources(false)
			item.mutate(&g, sources)
			rejectGroups(t, groupID("b"), []config.Group{groupAuto("valid", nil), g}, sources)
		})
	}
	rejectGroups(t, nil, []config.Group{groupAuto("requires-active", nil)}, groupSources(false))
}

func TestGroupsEmptyAndUnknownRegion(t *testing.T) {
	sources := groupSources(false)
	optional := groupAuto("optional", groupRegion("ZZ"))
	optional.Required = false
	definitions := []config.Group{optional, groupFollow("optional-follow", "optional", false)}
	result := mustGroups(t, groupID("a"), definitions, sources)
	for _, id := range []config.ID{"optional", "optional-follow"} {
		g := result[id]
		if !g.Reject || len(g.Candidates) != 0 || g.Selected != nil {
			t.Fatal("optional empty group acquired fallback")
		}
	}
	required := optional
	required.Required = true
	rejectGroups(t, groupID("a"), []config.Group{required}, sources)
	rejectGroups(t, groupID("a"), append(definitions, groupFollow("required-follow", "optional", true)), sources)
	// Unknown nodes can be explicitly included only when no region is required.
	manual := groupManual("unknown", "a", "a-unknown")
	resolved := mustGroups(t, nil, []config.Group{manual}, sources)
	if resolved["unknown"].Selected == nil || resolved["unknown"].Selected.NodeID != "a-unknown" {
		t.Fatal("explicit unconstrained unknown node lost")
	}
	constrained := groupFollow("constrained", "all", true)
	constrained.Region = groupRegion("US")
	rejectGroups(t, groupID("a"), []config.Group{groupAuto("all", nil), constrained}, sources)
	matched := groupFollow("matched", "US", true)
	matched.Region = groupRegion("US")
	mustGroups(t, groupID("a"), []config.Group{matched, groupAuto("US", groupRegion("US"))}, sources)
}

func TestGroupsGraphAndStrategyValidation(t *testing.T) {
	for _, item := range []struct {
		name   string
		groups []config.Group
	}{
		{"self-cycle", []config.Group{groupFollow("loop", "loop", true)}},
		{"cycle", []config.Group{groupFollow("first", "second", true), groupFollow("second", "first", true)}},
		{"dangling", []config.Group{groupFollow("first", "absent", true)}},
		{"duplicate-id", []config.Group{groupAuto("same", nil), groupAuto("same", nil)}},
		{"invalid-id", []config.Group{groupAuto("bad id", nil)}},
		{"empty-id", []config.Group{groupAuto("", nil)}},
	} {
		t.Run(item.name, func(t *testing.T) { rejectGroups(t, groupID("a"), item.groups, groupSources(false)) })
	}
	for _, item := range []struct {
		name   string
		mutate func(*config.Group)
	}{
		{"missing-source", func(g *config.Group) { g.Source = nil }},
		{"unknown-source-kind", func(g *config.Group) { g.Source = &config.GroupSource{Kind: "other"} }},
		{"active-extra-subscription", func(g *config.Group) { g.Source.SubscriptionID = groupID("a") }},
		{"explicit-missing-id", func(g *config.Group) { g.Source = &config.GroupSource{Kind: "subscription"} }},
		{"explicit-invalid-id", func(g *config.Group) { g.Source = groupSource("bad id") }},
		{"missing-probe", func(g *config.Group) { g.Probe = nil }},
		{"auto-extra-selection", func(g *config.Group) { g.SelectedNodeID = groupID("a-us-1") }},
		{"auto-extra-follow", func(g *config.Group) { g.Follows = groupID("target") }},
		{"unknown-strategy", func(g *config.Group) { g.Strategy = "first-node" }},
		{"empty-region", func(g *config.Group) { g.Region = groupRegion("") }},
	} {
		t.Run(item.name, func(t *testing.T) {
			g := groupAuto("bad", nil)
			item.mutate(&g)
			rejectGroups(t, groupID("a"), []config.Group{g}, groupSources(false))
		})
	}
	for _, strategy := range []string{"manual", "follow"} {
		for _, field := range []string{"probe", "source", "selected", "follows"} {
			if strategy == "manual" && (field == "source" || field == "selected") || strategy == "follow" && field == "follows" {
				continue
			}
			t.Run(strategy+"-extra-"+field, func(t *testing.T) {
				var g config.Group
				if strategy == "manual" {
					g = groupManual("bad", "a", "a-us-1")
				} else {
					g = groupFollow("bad", "target", true)
				}
				switch field {
				case "probe":
					g.Probe = groupProbe()
				case "source":
					g.Source = groupSource("a")
				case "selected":
					g.SelectedNodeID = groupID("a-us-1")
				case "follows":
					g.Follows = groupID("target")
				}
				rejectGroups(t, groupID("a"), []config.Group{groupAuto("target", nil), g}, groupSources(false))
			})
		}
	}
	missingManual := groupManual("manual", "a", "a-us-1")
	missingManual.SelectedNodeID = nil
	rejectGroups(t, nil, []config.Group{missingManual}, groupSources(false))
	missingFollow := groupFollow("follow", "target", true)
	missingFollow.Follows = nil
	rejectGroups(t, groupID("a"), []config.Group{groupAuto("target", nil), missingFollow}, groupSources(false))
}

func TestGroupsProbeValidation(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(*config.Probe)
	}{
		{"relative-url", func(p *config.Probe) { p.URL = "/check" }},
		{"missing-host", func(p *config.Probe) { p.URL = "https:///check" }},
		{"unsupported-scheme", func(p *config.Probe) { p.URL = "ftp://probe.invalid/check" }},
		{"zero-interval", func(p *config.Probe) { p.IntervalSeconds = 0 }},
		{"zero-timeout", func(p *config.Probe) { p.TimeoutMS = 0 }},
		{"unsafe-interval", func(p *config.Probe) { p.IntervalSeconds = 9007199254740992 }},
		{"unsafe-timeout", func(p *config.Probe) { p.TimeoutMS = 9007199254740992 }},
		{"unsafe-tolerance", func(p *config.Probe) { p.ToleranceMS = 9007199254740992 }},
		{"duration-interval-overflow", func(p *config.Probe) { p.IntervalSeconds = 9223372037 }},
		{"duration-timeout-overflow", func(p *config.Probe) { p.TimeoutMS = 9223372036855 }},
		{"duration-tolerance-overflow", func(p *config.Probe) { p.ToleranceMS = 9223372036855 }},
	} {
		t.Run(item.name, func(t *testing.T) {
			g := groupAuto("auto", nil)
			item.mutate(g.Probe)
			rejectGroups(t, groupID("a"), []config.Group{g}, groupSources(false))
		})
	}
	// Both declared HTTP schemes are accepted without any probe being performed.
	for _, url := range []string{"http://probe.invalid/check", "https://probe.invalid/check"} {
		g := groupAuto("auto", nil)
		g.Probe.URL = url
		mustGroups(t, groupID("a"), []config.Group{g}, groupSources(false))
	}
}

func TestGroupsInputAndOutputIndependence(t *testing.T) {
	groups := []config.Group{groupAuto("US", groupRegion("US")), groupFollow("follower", "US", true), groupManual("manual", "a", "a-us-1"), groupFollow("manual-follow", "manual", true)}
	sources := groupSources(false)
	active := groupID("a")
	before, err := json.Marshal(struct {
		Groups  []config.Group
		Sources map[config.ID]subscription.Snapshot
		Active  *config.ID
	}{groups, sources, active})
	if err != nil {
		t.Fatal("synthetic input encoding failed")
	}
	output := mustGroups(t, active, groups, sources)
	baseline := mustGroups(t, active, groups, sources)
	follow := output["follower"]
	follow.Candidates[0].NodeID = "mutated"
	if output["US"].Candidates[0].NodeID == "mutated" {
		t.Fatal("follow candidates alias target output")
	}
	manualFollow := output["manual-follow"]
	manualFollow.Selected.NodeID = "mutated"
	if output["manual"].Selected.NodeID == "mutated" {
		t.Fatal("follow selection aliases target output")
	}
	*follow.Follows = "mutated"
	output["US"].Probe.URL = "http://mutated.invalid/"
	after, err := json.Marshal(struct {
		Groups  []config.Group
		Sources map[config.ID]subscription.Snapshot
		Active  *config.ID
	}{groups, sources, active})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("resolution or output mutation changed input")
	}
	fresh := mustGroups(t, active, groups, sources)
	if !reflect.DeepEqual(fresh, baseline) {
		t.Fatal("one result mutated later independent resolution")
	}
}
