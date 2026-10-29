//go:build linux

package control

import "hoopa/internal/config"

// PlanImpactNode is a configured provider identity, not a current selection.
type PlanImpactNode struct {
	SubscriptionID config.ID `json:"subscription_id"`
	NodeID         config.ID `json:"node_id"`
}

// PlanRollbackTarget describes intended recovery, not a successful rollback.
type PlanRollbackTarget struct {
	Kind           string  `json:"kind"`
	Generation     *string `json:"generation"`
	ManifestSHA256 *string `json:"manifest_sha256"`
}
type PlanImpactDetails struct {
	Scope               string             `json:"scope"`
	AffectedNodes       []PlanImpactNode   `json:"affected_nodes"`
	AffectedGroups      []string           `json:"affected_groups"`
	AffectedRules       []string           `json:"affected_rules"`
	ExistingConnections string             `json:"existing_connections"`
	NewConnections      string             `json:"new_connections"`
	RollbackTarget      PlanRollbackTarget `json:"rollback_target"`
}
