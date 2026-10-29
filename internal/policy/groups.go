package policy

import (
	"errors"
	"sort"
	"unicode"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

var ErrGroups = errors.New("GROUPS_INVALID")

type ResolvedGroup struct {
	ID         config.ID
	Strategy   string
	SourceID   config.ID
	Candidates []subscription.NodeRef
	Selected   *subscription.NodeRef
	Follows    *config.ID
	Probe      *config.Probe
	Reject     bool
}

// ResolveGroups computes static candidates and exact manual pins. Auto groups
// retain every matching candidate; selection/probing belong to the core later.
// Only referenced snapshot metadata is validated here. Synthetic snapshots are
// intentional inputs for pure policy calculation; the compiler must separately
// authenticate snapshots and their private material using Snapshot.Validate.
func ResolveGroups(active *config.ID, groups []config.Group, sources map[config.ID]subscription.Snapshot) (map[config.ID]ResolvedGroup, error) {
	if active != nil && !groupValidID(*active) {
		return nil, ErrGroups
	}
	definitions := make(map[config.ID]config.Group, len(groups))
	for _, g := range groups {
		if !validGroupShape(g) {
			return nil, ErrGroups
		}
		if _, duplicate := definitions[g.ID]; duplicate {
			return nil, ErrGroups
		}
		definitions[g.ID] = g
	}
	// Each follow group has exactly one dependency. Processing roots and their
	// dependents iteratively detects dangling refs/cycles without recursive stacks.
	dependents := map[config.ID][]config.ID{}
	queue := make([]config.ID, 0, len(groups))
	for _, g := range groups {
		if g.Strategy != "follow" {
			queue = append(queue, g.ID)
			continue
		}
		if _, ok := definitions[*g.Follows]; !ok {
			return nil, groupDiagnostic("follow_target_missing", groupField(g.ID)+".follows")
		}
		dependents[*g.Follows] = append(dependents[*g.Follows], g.ID)
	}
	views := map[config.ID]groupSourceView{}
	sourceView := func(id config.ID) (groupSourceView, bool) {
		if cached, ok := views[id]; ok {
			return cached, true
		}
		snapshot, ok := sources[id]
		if !ok {
			return groupSourceView{}, false
		}
		view, ok := validateGroupSource(id, snapshot)
		if ok {
			views[id] = view
		}
		return view, ok
	}
	result := make(map[config.ID]ResolvedGroup, len(groups))
	for head := 0; head < len(queue); head++ {
		g := definitions[queue[head]]
		resolved := ResolvedGroup{ID: g.ID, Strategy: g.Strategy}
		var view groupSourceView
		if g.Strategy == "follow" {
			target := result[*g.Follows]
			resolved.SourceID = target.SourceID
			resolved.Candidates = append([]subscription.NodeRef{}, target.Candidates...)
			if target.Selected != nil {
				selected := *target.Selected
				resolved.Selected = &selected
			}
			follows := *g.Follows
			resolved.Follows = &follows
			resolved.Reject = target.Reject
			view = views[resolved.SourceID]
			if g.Region != nil {
				for _, ref := range resolved.Candidates {
					if !groupRegionMatches(view.nodes[ref.NodeID], g.Region) {
						return nil, groupDiagnostic("follow_region_mismatch", groupField(g.ID)+".region")
					}
				}
			}
		} else {
			sourceID := config.ID("")
			if g.Source.Kind == "active" {
				if active == nil {
					return nil, groupDiagnostic("active_source_unset", groupField(g.ID)+".source")
				}
				sourceID = *active
			} else {
				sourceID = *g.Source.SubscriptionID
			}
			var ok bool
			view, ok = sourceView(sourceID)
			if !ok {
				return nil, groupDiagnostic("source_unavailable", groupField(g.ID)+".source")
			}
			resolved.SourceID = sourceID
			resolved.Candidates = make([]subscription.NodeRef, 0)
			if g.Strategy == "manual" {
				node, exists := view.nodes[*g.SelectedNodeID]
				if !exists {
					return nil, groupDiagnostic("selected_node_missing", groupField(g.ID)+".selected_node_id")
				}
				if !groupRegionMatches(node, g.Region) {
					return nil, groupDiagnostic("selected_node_region_mismatch", groupField(g.ID)+".selected_node_id")
				}
				ref := subscription.NodeRef{SubscriptionID: sourceID, NodeID: node.ID}
				resolved.Candidates = append(resolved.Candidates, ref)
				selected := ref
				resolved.Selected = &selected
			} else {
				for _, id := range view.ordered {
					if groupRegionMatches(view.nodes[id], g.Region) {
						resolved.Candidates = append(resolved.Candidates, subscription.NodeRef{SubscriptionID: sourceID, NodeID: id})
					}
				}
				probe := *g.Probe
				resolved.Probe = &probe
				resolved.Reject = len(resolved.Candidates) == 0
			}
		}
		if g.Required && len(resolved.Candidates) == 0 {
			if g.Region != nil {
				return nil, groupDiagnostic("required_group_no_region_match", groupField(g.ID)+".region")
			}
			return nil, groupDiagnostic("required_group_empty", groupField(g.ID))
		}
		result[g.ID] = resolved
		queue = append(queue, dependents[g.ID]...)
	}
	if len(result) != len(groups) {
		if id, ok := followCycleGroup(groups); ok {
			return nil, groupDiagnostic("follow_cycle", groupField(id)+".follows")
		}
		return nil, ErrGroups
	}
	return result, nil
}

func groupValidID(id config.ID) bool { _, err := config.ParseID(string(id)); return err == nil }
func validGroupShape(g config.Group) bool {
	if !groupValidID(g.ID) || g.Region != nil && (*g.Region == "" || !utf8.ValidString(*g.Region)) {
		return false
	}
	if g.Strategy == "follow" {
		return g.Follows != nil && groupValidID(*g.Follows) && g.Source == nil && g.Probe == nil && g.SelectedNodeID == nil
	}
	if g.Source == nil || g.Follows != nil {
		return false
	}
	switch g.Source.Kind {
	case "active":
		if g.Source.SubscriptionID != nil {
			return false
		}
	case "subscription":
		if g.Source.SubscriptionID == nil || !groupValidID(*g.Source.SubscriptionID) {
			return false
		}
	default:
		return false
	}
	switch g.Strategy {
	case "manual":
		return g.SelectedNodeID != nil && groupValidID(*g.SelectedNodeID) && g.Probe == nil
	case "auto":
		return g.SelectedNodeID == nil && g.Probe != nil && g.Probe.Validate() == nil
	default:
		return false
	}
}

type groupSourceView struct {
	nodes   map[config.ID]subscription.Node
	ordered []config.ID
}

func validateGroupSource(id config.ID, snapshot subscription.Snapshot) (groupSourceView, bool) {
	if snapshot.SourceID != id || !groupValidID(snapshot.ID) || snapshot.KeyID == "" {
		return groupSourceView{}, false
	}
	view := groupSourceView{nodes: make(map[config.ID]subscription.Node, len(snapshot.Nodes)), ordered: make([]config.ID, 0, len(snapshot.Nodes))}
	for _, node := range snapshot.Nodes {
		if !groupValidID(node.ID) || !validGroupRegionEvidence(node.Region) {
			return groupSourceView{}, false
		}
		if _, duplicate := view.nodes[node.ID]; duplicate {
			return groupSourceView{}, false
		}
		view.nodes[node.ID] = node
		view.ordered = append(view.ordered, node.ID)
	}
	sort.Slice(view.ordered, func(i, j int) bool { return view.ordered[i] < view.ordered[j] })
	return view, true
}
func validGroupRegionEvidence(r subscription.RegionEvidence) bool {
	switch r.Kind {
	case "unknown", "ambiguous":
		return r.Code == "" && r.Rule == ""
	case "declared-prefix":
		return groupEvidenceText(r.Code, 64) && groupEvidenceText(r.Rule, 256)
	default:
		return false
	}
}
func groupEvidenceText(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func groupRegionMatches(node subscription.Node, region *string) bool {
	return region == nil || node.Region.Kind == "declared-prefix" && node.Region.Code == *region
}
