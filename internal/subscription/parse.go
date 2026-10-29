package subscription

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/document"
)

func Parse(raw []byte, options ParseOptions) (Snapshot, error) {
	if len(options.IdentityKey) != 32 {
		return Snapshot{}, ErrIdentityKeyUnavailable
	}
	if _, err := config.ParseID(string(options.SourceID)); err != nil {
		return Snapshot{}, ErrInvalidProvider
	}
	limits := options.Limits
	fields := []*int{&limits.MaxBytes, &limits.MaxNodes, &limits.MaxDepth, &limits.MaxAliases}
	defaults := []int{DefaultMaxBytes, DefaultMaxNodes, DefaultMaxDepth, DefaultMaxAliases}
	for i, f := range fields {
		if *f < 0 {
			return Snapshot{}, ErrInputLimit
		}
		if *f == 0 {
			*f = defaults[i]
		}
	}
	if len(options.RegionRules) > 64 {
		return Snapshot{}, ErrInputLimit
	}
	for _, r := range options.RegionRules {
		if !boundedText(r.Prefix, 256, true) || !boundedText(r.Region, 64, true) || hasControl(r.Prefix) || hasControl(r.Region) {
			return Snapshot{}, ErrInvalidProvider
		}
	}
	value, err := document.Decode(raw, document.Limits{MaxBytes: limits.MaxBytes, MaxDepth: limits.MaxDepth, MaxAliases: limits.MaxAliases, MaxValues: 1000000})
	if err != nil {
		if errors.Is(err, document.ErrLimit) {
			return Snapshot{}, ErrInputLimit
		}
		return Snapshot{}, ErrInvalidProvider
	}
	root, ok := value.(map[string]any)
	if !ok || len(root) != 1 {
		return Snapshot{}, ErrInvalidProvider
	}
	nodes, ok := root["proxies"].([]any)
	if !ok {
		return Snapshot{}, ErrInvalidProvider
	}
	if len(nodes) > limits.MaxNodes {
		return Snapshot{}, ErrInputLimit
	}
	metadata := make([]Node, 0, len(nodes))
	configs := make([]string, 0, len(nodes))
	seen := map[config.ID]bool{}
	for _, value := range nodes {
		name, connection, err := parseConnection(value)
		if err != nil {
			return Snapshot{}, err
		}
		id := nodeIdentity(options.IdentityKey, options.SourceID, connection)
		if seen[id] {
			return Snapshot{}, ErrAmbiguousIdentity
		}
		seen[id] = true
		metadata = append(metadata, Node{ID: id, DisplayName: name, Region: regionFor(name, options.RegionRules)})
		connection["name"] = string(id)
		encoded, _ := json.Marshal(connection)
		configs = append(configs, string(encoded))
	}
	id := snapshotIdentity(options.IdentityKey, options.SourceID, raw, options.RegionRules)
	keyID := keyedHex(options.IdentityKey, []byte("hoopa.identity-key.v1"))
	authority := &snapshotAuthority{id: id, sourceID: options.SourceID, keyID: keyID, nodes: append([]Node{}, metadata...), configs: configs, raw: string(raw), regionRules: append([]RegionRule{}, options.RegionRules...)}
	return Snapshot{ID: id, SourceID: options.SourceID, KeyID: keyID, Nodes: metadata, authority: authority}, nil
}
func boundedText(s string, max int, nonempty bool) bool {
	return utf8.ValidString(s) && len(s) <= max && (!nonempty || len(s) > 0)
}
