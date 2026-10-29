// Package routingcandidate compiles bounded normalized experiment fixtures.
// It performs no I/O, selection mutation, publication, or runtime route prediction.
package routingcandidate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"
	"strings"
	"unicode/utf8"

	"hoopa/experiments/mihomo-events/capabilityprofile"
	"hoopa/protocol/identityprofile"
)

// ErrCandidate deliberately omits input values and credentials.
var ErrCandidate = errors.New("invalid routing experiment candidate")

// Spec is an experiment API, not a subscription or external JSON contract.
// ProgramRules is explicit opt-in; Requirements must also declare each used field.
type Spec struct {
	Generation, ControllerEndpoint, ControllerSecret string
	Entries                                          []Entry
	Hosts                                            map[string]string
	ActiveProviderID                                 string
	ProviderSnapshots                                [][]byte
	Groups                                           []Group
	Rules                                            []Rule
	Requirements                                     identityprofile.Requirements
	ProgramRules                                     bool
}
type Entry struct{ Name, Endpoint, Username, Password string }
type NodeRef struct{ ProviderID, NodeID string }
type Group struct {
	ID, DisplayAlias, ProviderID, Region string
	Selected                             NodeRef
}
type Condition struct{ Kind, Value string }

// Target has exactly one of GroupID, a complete Node, or Reject.
type Target struct {
	GroupID string
	Node    NodeRef
	Reject  bool
}

// Match is admitted only on the final rule, with no conditions.
// Other rules contain 1..8 flat AND conditions in caller order.
type Rule struct {
	ID         string
	Conditions []Condition
	Target     Target
	Match      bool
}
type RuleMetadata struct {
	ID             string
	Conditions     []Condition
	Match          bool
	InternalTarget string
}
type GroupSelection struct {
	GroupID, ProviderID, Region string
	Selected                    NodeRef
	InternalNodeID              string
}

// Candidate owns all its bytes and slices. Treat the returned value as an
// immutable replacement: changing selection requires compiling another Spec.
// RouteMap is inline intent; Provenance must never be used as observed flow data.
type Candidate struct {
	Config               []byte
	ConfigSHA256         string
	RouteMap, Provenance []byte
	Rules                []RuleMetadata
	GroupSelections      []GroupSelection
}

type routeEntry struct {
	InternalName string  `json:"internal_name"`
	AdapterType  string  `json:"adapter_type"`
	ProviderName string  `json:"provider_name"`
	NodeID       *string `json:"node_id"`
	ProviderID   *string `json:"provider_id"`
	GroupID      *string `json:"group_id"`
	DisplayAlias string  `json:"display_alias"`
}
type routeMap struct {
	SchemaVersion    int          `json:"schema_version"`
	ConfigGeneration string       `json:"config_generation"`
	Entries          []routeEntry `json:"entries"`
}
type snapshotEvidence struct {
	ProviderID string `json:"provider_id"`
	Revision   string `json:"revision"`
	SHA256     string `json:"sha256"`
}
type nodeEvidence struct {
	InternalID string `json:"internal_id"`
	ProviderID string `json:"provider_id"`
	NodeID     string `json:"node_id"`
	Revision   string `json:"snapshot_revision"`
	SHA256     string `json:"snapshot_sha256"`
}
type provenance struct {
	SchemaVersion int                `json:"schema_version"`
	Snapshots     []snapshotEvidence `json:"snapshots"`
	Nodes         []nodeEvidence     `json:"nodes"`
}

func token(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i, c := range []byte(s) {
		alpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alpha && (i == 0 || !strings.ContainsRune("._:-", rune(c))) {
			return false
		}
	}
	return true
}
func alias(s string) bool {
	return len(s) > 0 && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func internalNodeID(ref NodeRef) string {
	sum := sha256.Sum256([]byte("hoopa-routing-node-v1\x00" + ref.ProviderID + "\x00" + ref.NodeID))
	return "node:" + hex.EncodeToString(sum[:])
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// Compile validates all supplied sources, selections, and rules, including
// shadowed rules. Every error returns a zero Candidate, never a partial result.
func Compile(s Spec) (Candidate, error) {
	bad := func() (Candidate, error) { return Candidate{}, ErrCandidate }
	if !token(s.ActiveProviderID) || len(s.ProviderSnapshots) < 1 || len(s.ProviderSnapshots) > 8 || len(s.Entries) < 2 || len(s.Entries) > 4 || len(s.Groups) > 64 || len(s.Rules) < 1 || len(s.Rules) > 64 {
		return bad()
	}
	snapshots := make([]providerSnapshot, 0, len(s.ProviderSnapshots))
	providers := map[string]bool{}
	for _, raw := range s.ProviderSnapshots {
		p, e := decodeSnapshot(raw)
		if e != nil || providers[p.ProviderID] {
			return bad()
		}
		providers[p.ProviderID] = true
		snapshots = append(snapshots, p)
	}
	if !providers[s.ActiveProviderID] {
		return bad()
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].ProviderID < snapshots[j].ProviderID })
	config := capabilityprofile.Config{Mode: "rule", LogLevel: "silent", FindProcessMode: "off", Controller: s.ControllerEndpoint, Secret: s.ControllerSecret, Hosts: map[string]string{}, Groups: []capabilityprofile.Group{}}
	for host, ip := range s.Hosts {
		config.Hosts[host] = ip
	}
	for _, entry := range s.Entries {
		ep, e := netip.ParseAddrPort(entry.Endpoint)
		if e != nil || ep.Addr().String() != "127.0.0.1" {
			return bad()
		}
		config.Listeners = append(config.Listeners, capabilityprofile.Entry{Name: entry.Name, Type: "mixed", Listen: ep.Addr().String(), Port: ep.Port(), Users: []capabilityprofile.User{{Username: entry.Username, Password: entry.Password}}})
	}
	mapping := routeMap{SchemaVersion: 1, ConfigGeneration: s.Generation}
	evidence := provenance{SchemaVersion: 1}
	nodes := map[NodeRef]providerNode{}
	ids := map[string]NodeRef{}
	for _, p := range snapshots {
		evidence.Snapshots = append(evidence.Snapshots, snapshotEvidence{p.ProviderID, p.Revision, p.hash})
		sort.Slice(p.Nodes, func(i, j int) bool { return p.Nodes[i].NodeID < p.Nodes[j].NodeID })
		for _, node := range p.Nodes {
			ref := NodeRef{p.ProviderID, node.NodeID}
			id := internalNodeID(ref)
			if previous, exists := ids[id]; exists && previous != ref {
				return bad()
			}
			ids[id] = ref
			nodes[ref] = node
			config.Proxies = append(config.Proxies, capabilityprofile.Node{Name: id, Type: node.Type, Server: node.Server, Port: node.Port})
			nodeID := id
			mapping.Entries = append(mapping.Entries, routeEntry{InternalName: id, AdapterType: map[string]string{"http": "Http", "socks5": "Socks5"}[node.Type], NodeID: &nodeID, DisplayAlias: node.DisplayName})
			evidence.Nodes = append(evidence.Nodes, nodeEvidence{id, p.ProviderID, node.NodeID, p.Revision, p.hash})
		}
	}
	candidate := Candidate{}
	groups := map[string]bool{}
	for _, group := range s.Groups {
		if !strings.HasPrefix(group.ID, "group:") || !token(group.ID) || !token(strings.TrimPrefix(group.ID, "group:")) || groups[group.ID] || !alias(group.DisplayAlias) || !token(group.Region) {
			return bad()
		}
		provider := group.ProviderID
		if provider == "active" {
			provider = s.ActiveProviderID
		}
		node, exists := nodes[group.Selected]
		if !providers[provider] || group.Selected.ProviderID != provider || !exists || node.Region != group.Region {
			return bad()
		}
		groups[group.ID] = true
		id := internalNodeID(group.Selected)
		config.Groups = append(config.Groups, capabilityprofile.Group{Name: group.ID, Type: "select", Proxies: []string{id}})
		groupID := group.ID
		mapping.Entries = append(mapping.Entries, routeEntry{InternalName: group.ID, AdapterType: "Selector", GroupID: &groupID, DisplayAlias: group.DisplayAlias})
		candidate.GroupSelections = append(candidate.GroupSelections, GroupSelection{group.ID, provider, group.Region, group.Selected, id})
	}
	ruleIDs := map[string]bool{}
	for i, rule := range s.Rules {
		if !token(rule.ID) || ruleIDs[rule.ID] || rule.Match != (i == len(s.Rules)-1) {
			return bad()
		}
		ruleIDs[rule.ID] = true
		target := ""
		targetCount := 0
		if rule.Target.GroupID != "" {
			targetCount++
			if !groups[rule.Target.GroupID] {
				return bad()
			}
			target = rule.Target.GroupID
		}
		if rule.Target.Node != (NodeRef{}) {
			targetCount++
			if _, ok := nodes[rule.Target.Node]; !ok {
				return bad()
			}
			target = internalNodeID(rule.Target.Node)
		}
		if rule.Target.Reject {
			targetCount++
			target = "REJECT"
		}
		if targetCount != 1 {
			return bad()
		}
		encoded := "MATCH," + target
		if rule.Match {
			if len(rule.Conditions) != 0 {
				return bad()
			}
		} else {
			if len(rule.Conditions) < 1 || len(rule.Conditions) > 8 {
				return bad()
			}
			atoms := make([]string, 0, len(rule.Conditions))
			for _, condition := range rule.Conditions {
				kind := condition.Kind
				switch kind {
				case "app":
					kind = "IN-NAME"
				case "PROCESS-NAME", "PROCESS-PATH":
					if !s.ProgramRules {
						return bad()
					}
				case "DOMAIN", "DOMAIN-SUFFIX", "IP-CIDR":
				default:
					return bad()
				}
				if condition.Value == "" || !utf8.ValidString(condition.Value) || strings.ContainsAny(condition.Value, ",()") {
					return bad()
				}
				atom := kind + "," + condition.Value
				if kind == "IP-CIDR" {
					atom += ",no-resolve"
				}
				atoms = append(atoms, atom)
			}
			if len(atoms) == 1 {
				encoded = atoms[0] + "," + target
				if rule.Conditions[0].Kind == "IP-CIDR" {
					encoded = "IP-CIDR," + rule.Conditions[0].Value + "," + target + ",no-resolve"
				}
			} else {
				encoded = "AND,((" + strings.Join(atoms, "),(") + "))," + target
			}
		}
		config.Rules = append(config.Rules, encoded)
		candidate.Rules = append(candidate.Rules, RuleMetadata{rule.ID, append([]Condition(nil), rule.Conditions...), rule.Match, target})
	}
	// Marshal only compiler-owned fields, then add the two explicit declarations.
	raw, e := json.Marshal(config)
	if e != nil {
		return bad()
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return bad()
	}
	root["hoopa-identity"], e = json.Marshal(s.Requirements)
	if e != nil {
		return bad()
	}
	if s.ProgramRules {
		root["hoopa-program-rules"], e = json.Marshal(map[string]string{"profile_id": capabilityprofile.ProgramRuleProfileID})
		if e != nil {
			return bad()
		}
	}
	candidate.Config, e = json.Marshal(root)
	if e != nil {
		return bad()
	}
	if _, _, e = capabilityprofile.ValidateDeclaredProgramIdentity(candidate.Config, s.Generation); e != nil {
		return bad()
	}
	candidate.ConfigSHA256 = digest(candidate.Config)
	candidate.RouteMap, e = json.Marshal(mapping)
	if e != nil {
		return bad()
	}
	candidate.Provenance, e = json.Marshal(evidence)
	if e != nil {
		return bad()
	}
	return candidate, nil
}
