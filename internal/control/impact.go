//go:build linux && amd64

package control

import (
	"sort"

	"hoopa/internal/config"
)

// impactGeneration is caller-captured, integrity-checked configuration metadata.
// Known=false is an explicit older unsupported projection, never a read error.
type impactGeneration struct {
	Generation     config.Generation
	ManifestSHA256 string
	Known          bool
	Nodes          []PlanImpactNode
	Groups         []string
	Rules          []string
	Apps           []string
}

// projectSourceImpact is pure. Full replacement determines interruption; the
// metadata union describes configured identities, not observed connections.
func projectSourceImpact(base config.ActivePointer, previous *impactGeneration, target impactGeneration) (PlanImpact, error) {
	fail := func(err error) (PlanImpact, error) { return PlanImpact{}, err }
	if !sourceContextBase(base) || !validImpactGeneration(target) || !target.Known {
		return fail(ErrPlan)
	}
	if base.Generation == nil {
		if previous != nil {
			return fail(ErrPlan)
		}
	} else {
		if previous == nil || !validImpactGeneration(*previous) || previous.Generation != *base.Generation || previous.ManifestSHA256 != base.ManifestSHA256 {
			return fail(ErrPlan)
		}
		if base.DesiredRuntime != "running" || target.Generation == *base.Generation {
			return fail(ErrCapabilityUnsupported)
		}
	}
	details := &PlanImpactDetails{Scope: "generation_union", AffectedNodes: impactNodeUnion(nil, target.Nodes), AffectedGroups: impactStringUnion(nil, target.Groups), AffectedRules: impactStringUnion(nil, target.Rules), ExistingConnections: "no_previous_generation", NewConnections: "target_after_activation", RollbackTarget: PlanRollbackTarget{Kind: "stop_candidate"}}
	result := PlanImpact{RequiresDisruption: true, AffectedApps: impactStringUnion(nil, target.Apps), TargetGeneration: string(target.Generation), ConnectionEffect: "interrupt", RuntimeAction: "start", Details: details}
	if previous != nil {
		prior := string(previous.Generation)
		rollbackPrior := prior
		hash := previous.ManifestSHA256
		result.PreviousGeneration = &prior
		result.RuntimeAction = "replace"
		details.ExistingConnections = "interrupt"
		details.RollbackTarget = PlanRollbackTarget{Kind: "restore_generation", Generation: &rollbackPrior, ManifestSHA256: &hash}
		if !previous.Known {
			details.Scope = "known_generation_union"
		}
		details.AffectedNodes = impactNodeUnion(previous.Nodes, target.Nodes)
		details.AffectedGroups = impactStringUnion(previous.Groups, target.Groups)
		details.AffectedRules = impactStringUnion(previous.Rules, target.Rules)
		result.AffectedApps = impactStringUnion(previous.Apps, target.Apps)
	}
	return result, nil
}
func validImpactGeneration(value impactGeneration) bool {
	if !sourceContextID(string(value.Generation)) || !isHash(value.ManifestSHA256) || value.Nodes == nil || value.Groups == nil || value.Rules == nil || value.Apps == nil || len(value.Nodes) > 64 || len(value.Groups) > 64 || len(value.Rules) > 64 || len(value.Apps) > 3 {
		return false
	}
	if !value.Known {
		return len(value.Nodes) == 0 && len(value.Groups) == 0 && len(value.Rules) == 0 && len(value.Apps) == 0
	}
	nodes := map[PlanImpactNode]bool{}
	for _, node := range value.Nodes {
		if !sourceContextID(string(node.SubscriptionID)) || !sourceContextID(string(node.NodeID)) || nodes[node] {
			return false
		}
		nodes[node] = true
	}
	for _, ids := range [][]string{value.Groups, value.Rules, value.Apps} {
		seen := map[string]bool{}
		for _, id := range ids {
			if !sourceContextID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}
func impactNodeUnion(previous, target []PlanImpactNode) []PlanImpactNode {
	seen := map[PlanImpactNode]bool{}
	result := []PlanImpactNode{}
	for _, nodes := range [][]PlanImpactNode{previous, target} {
		for _, node := range nodes {
			if !seen[node] {
				seen[node] = true
				result = append(result, node)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].SubscriptionID != result[j].SubscriptionID {
			return result[i].SubscriptionID < result[j].SubscriptionID
		}
		return result[i].NodeID < result[j].NodeID
	})
	return result
}
func impactStringUnion(previous, target []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, ids := range [][]string{previous, target} {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				result = append(result, id)
			}
		}
	}
	sort.Strings(result)
	return result
}
