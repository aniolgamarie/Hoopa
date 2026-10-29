//go:build linux

package control

import (
	"bytes"
	"encoding/json"
)

// Version 2 is an exact nested shape. Version 1 never accepts this object;
// retaining that distinction keeps archived Plan bytes and digests meaningful.
func validPlanImpactDetails(raw []byte, impact PlanImpact) bool {
	if impact.Details == nil || !planFields(raw, "scope", "affected_nodes", "affected_groups", "affected_rules", "existing_connections", "new_connections", "rollback_target") {
		return false
	}
	fields, _ := planObject(raw)
	for _, value := range fields {
		if bytes.Equal(value, []byte("null")) {
			return false
		}
	}
	var nodes []json.RawMessage
	if json.Unmarshal(fields["affected_nodes"], &nodes) != nil || nodes == nil || len(nodes) > 128 {
		return false
	}
	for _, node := range nodes {
		if !planFields(node, "subscription_id", "node_id") {
			return false
		}
		values, _ := planObject(node)
		if bytes.Equal(values["subscription_id"], []byte("null")) || bytes.Equal(values["node_id"], []byte("null")) {
			return false
		}
	}
	d := impact.Details
	if !impactSortedIDs(d.AffectedGroups, 128) || !impactSortedIDs(d.AffectedRules, 128) || !impactSortedIDs(impact.AffectedApps, 6) {
		return false
	}
	for i, node := range d.AffectedNodes {
		if !planID(string(node.SubscriptionID)) || !planID(string(node.NodeID)) {
			return false
		}
		if i > 0 {
			previous := d.AffectedNodes[i-1]
			if node.SubscriptionID < previous.SubscriptionID || node.SubscriptionID == previous.SubscriptionID && node.NodeID <= previous.NodeID {
				return false
			}
		}
	}
	if !planFields(fields["rollback_target"], "kind", "generation", "manifest_sha256") {
		return false
	}
	rollback := d.RollbackTarget
	if d.Scope != "generation_union" && d.Scope != "known_generation_union" {
		return false
	}
	if d.NewConnections != "target_after_activation" && d.NewConnections != "unknown" {
		return false
	}
	if d.ExistingConnections == "unknown" || d.NewConnections == "unknown" {
		if impact.ConnectionEffect != "unknown" {
			return false
		}
	} else if impact.ConnectionEffect != "interrupt" {
		return false
	}
	switch impact.RuntimeAction {
	case "start":
		return impact.PreviousGeneration == nil && d.Scope == "generation_union" && d.ExistingConnections == "no_previous_generation" && rollback.Kind == "stop_candidate" && rollback.Generation == nil && rollback.ManifestSHA256 == nil
	case "replace":
		return impact.PreviousGeneration != nil && (d.ExistingConnections == "interrupt" || d.ExistingConnections == "unknown") && rollback.Kind == "restore_generation" && rollback.Generation != nil && *rollback.Generation == *impact.PreviousGeneration && rollback.ManifestSHA256 != nil && isHash(*rollback.ManifestSHA256)
	}
	return false
}

func impactSortedIDs(ids []string, max int) bool {
	if ids == nil || len(ids) > max {
		return false
	}
	for i, id := range ids {
		if !planID(id) || i > 0 && id <= ids[i-1] {
			return false
		}
	}
	return true
}
