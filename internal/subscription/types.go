package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"hoopa/internal/config"
)

const (
	DefaultMaxBytes   = 10 << 20
	DefaultMaxNodes   = 10000
	DefaultMaxDepth   = 64
	DefaultMaxAliases = 64
)

var (
	ErrInvalidProvider        = errors.New("PROVIDER_INVALID")
	ErrUnsupportedNode        = errors.New("NODE_UNSUPPORTED")
	ErrInputLimit             = errors.New("PROVIDER_LIMIT")
	ErrIdentityKeyUnavailable = errors.New("IDENTITY_KEY_UNAVAILABLE")
	ErrAmbiguousIdentity      = errors.New("IDENTITY_AMBIGUOUS")
	ErrPinUnresolved          = errors.New("PIN_UNRESOLVED")
)

type Limits struct{ MaxBytes, MaxNodes, MaxDepth, MaxAliases int }
type RegionRule struct {
	Prefix string `json:"prefix"`
	Region string `json:"region"`
}
type RegionEvidence struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
	Rule string `json:"rule"`
}
type ParseOptions struct {
	SourceID    config.ID
	IdentityKey []byte
	Limits      Limits
	RegionRules []RegionRule
}
type Node struct {
	ID          config.ID      `json:"id"`
	DisplayName string         `json:"display_name"`
	Region      RegionEvidence `json:"region"`
}
type NodeRef struct{ SubscriptionID, NodeID config.ID }
type Snapshot struct {
	ID        config.ID `json:"id"`
	SourceID  config.ID `json:"source_id"`
	KeyID     string    `json:"key_id"`
	Nodes     []Node    `json:"nodes"`
	authority *snapshotAuthority
}

// All authority material is privately owned and immutable after construction.
// Credential-bearing normalized configs and original bytes are stored as strings;
// public metadata and every sensitive getter return independent copies.
type snapshotAuthority struct {
	id, sourceID config.ID
	keyID        string
	nodes        []Node
	configs      []string
	raw          string
	regionRules  []RegionRule
}

// SensitiveAssetBytes binds the original provider document, the complete local
// region policy (including nonmatching rules), parser/key/source identity and
// detached public metadata into one versioned private compilation input. It
// is not a trusted deserialization format and contains provider credentials.
func (s Snapshot) SensitiveAssetBytes() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	a := s.authority
	asset := struct {
		SchemaVersion int          `json:"schema_version"`
		ParserVersion string       `json:"parser_version"`
		SnapshotID    config.ID    `json:"snapshot_id"`
		SourceID      config.ID    `json:"source_id"`
		KeyID         string       `json:"key_id"`
		Original      []byte       `json:"original_bytes"`
		RegionRules   []RegionRule `json:"region_rules"`
		Nodes         []Node       `json:"nodes"`
	}{1, parserVersion, a.id, a.sourceID, a.keyID, []byte(a.raw), a.regionRules, a.nodes}
	raw, err := json.Marshal(asset)
	if err != nil {
		return nil, ErrInvalidProvider
	}
	return raw, nil
}

func (s Snapshot) Validate() error {
	a := s.authority
	if a == nil || s.ID != a.id || s.SourceID != a.sourceID || s.KeyID != a.keyID || !reflect.DeepEqual(s.Nodes, a.nodes) {
		return ErrInvalidProvider
	}
	return nil
}

// SensitiveProxyConfigs deliberately exposes credentials for compilation only.
// Provider display names are replaced with opaque node IDs before returning.
func (s Snapshot) SensitiveProxyConfigs() ([]map[string]any, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(s.authority.configs))
	for i, raw := range s.authority.configs {
		// Normalized numeric fields are bounded ports and alterId=0; json's numeric
		// representation is exact for these values and marshals as decimal integers.
		if json.Unmarshal([]byte(raw), &out[i]) != nil {
			return nil, ErrInvalidProvider
		}
	}
	return out, nil
}
func (s Snapshot) SensitiveBytes() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return []byte(s.authority.raw), nil
}
func (s Snapshot) String() string {
	return fmt.Sprintf("Snapshot(%s, source=%s, nodes=%d)", s.ID, s.SourceID, len(s.Nodes))
}
func (s Snapshot) GoString() string { return s.String() }
func (n Node) String() string       { return fmt.Sprintf("Node(%s)", n.ID) }
func (n Node) GoString() string     { return n.String() }
