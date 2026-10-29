package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"hoopa/internal/config"
)

const parserVersion = "hoopa.provider.v1"

func keyedHex(key, payload []byte) string {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}
func nodeIdentity(key []byte, source config.ID, connection map[string]any) config.ID {
	// Fixed v1 encoding: Go JSON sorted map keys, standard HTML escaping,
	// normalized defaults, integer decimal numbers; independent of Plan JCS.
	raw, _ := json.Marshal([]any{"hoopa.node.v1", source, connection})
	return config.ID("n-" + keyedHex(key, raw))
}
func snapshotIdentity(key []byte, source config.ID, raw []byte, rules []RegionRule) config.ID {
	// Snapshot v1 encoding binds the original UTF8 document, ordered declared
	// rules (normalized nil to []), source and exact parser profile version.
	if rules == nil {
		rules = []RegionRule{}
	}
	encoded, _ := json.Marshal([]any{"hoopa.snapshot.v1", parserVersion, source, string(raw), rules})
	return config.ID("s-" + keyedHex(key, encoded))
}
func regionFor(name string, rules []RegionRule) RegionEvidence {
	out := RegionEvidence{Kind: "unknown"}
	for _, rule := range rules {
		if !strings.HasPrefix(name, rule.Prefix) {
			continue
		}
		if out.Kind == "ambiguous" {
			continue
		}
		if out.Kind == "declared-prefix" && out.Code != rule.Region {
			out = RegionEvidence{Kind: "ambiguous"}
			continue
		}
		if out.Kind == "unknown" || len(rule.Prefix) > len(out.Rule) {
			out = RegionEvidence{Code: rule.Region, Kind: "declared-prefix", Rule: rule.Prefix}
		}
	}
	return out
}
func ResolvePin(snapshots map[config.ID]Snapshot, ref NodeRef) (Node, error) {
	if _, err := config.ParseID(string(ref.SubscriptionID)); err != nil {
		return Node{}, ErrPinUnresolved
	}
	if _, err := config.ParseID(string(ref.NodeID)); err != nil {
		return Node{}, ErrPinUnresolved
	}
	snapshot, ok := snapshots[ref.SubscriptionID]
	if !ok || snapshot.SourceID != ref.SubscriptionID || snapshot.Validate() != nil {
		return Node{}, ErrPinUnresolved
	}
	for _, node := range snapshot.authority.nodes {
		if node.ID == ref.NodeID {
			return node, nil
		}
	}
	return Node{}, ErrPinUnresolved
}
