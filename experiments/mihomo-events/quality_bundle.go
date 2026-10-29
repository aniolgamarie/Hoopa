package mihomoevents

import (
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"unicode/utf8"
)

var ErrQualityBundle = errors.New("invalid experimental quality bundle")

// QualityNode IDs are persistent references, never display names. Endpoints
// are literal loopback addresses; this experiment does not resolve proxy hosts.
type QualityNode struct {
	ID, Type, Endpoint, DisplayAlias, DialerProxy string
}
type QualityGroup struct {
	ID, DisplayAlias string
	Members          []string
}
type QualityBundleSpec struct {
	Generation, EntryEndpoint, HealthEndpoint, ControllerEndpoint, ControllerSecret string
	Nodes                                                                           []QualityNode
	Groups                                                                          []QualityGroup
	DefaultTarget, DirectTarget                                                     string
}
type QualityBundle struct{ Config, RouteMap json.RawMessage }
type qualityMapEntry struct {
	InternalName string  `json:"internal_name"`
	AdapterType  string  `json:"adapter_type"`
	ProviderName string  `json:"provider_name"`
	NodeID       *string `json:"node_id"`
	ProviderID   *string `json:"provider_id"`
	GroupID      *string `json:"group_id"`
	DisplayAlias string  `json:"display_alias"`
}

func qualityToken(s string) bool {
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
func qualityEndpoint(s string) (netip.AddrPort, bool) {
	a, e := netip.ParseAddrPort(s)
	return a, e == nil && a.Addr().Is4() && a.Addr().IsLoopback() && a.Port() != 0
}
func qualityAlias(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// BuildQualityBundle produces JSON accepted as YAML by the fixed core and an
// immutable per-generation route mapping. It performs no I/O or live selection.
func BuildQualityBundle(s QualityBundleSpec) (QualityBundle, error) {
	bad := func() (QualityBundle, error) { return QualityBundle{}, ErrQualityBundle }
	if !qualityToken(s.Generation) || s.ControllerSecret == "" || len(s.Nodes) == 0 || len(s.Nodes)+len(s.Groups) > 64 {
		return bad()
	}
	endpoints := map[string]bool{}
	ports := []netip.AddrPort{}
	for _, v := range []string{s.EntryEndpoint, s.HealthEndpoint, s.ControllerEndpoint} {
		a, ok := qualityEndpoint(v)
		if !ok || endpoints[v] {
			return bad()
		}
		endpoints[v] = true
		ports = append(ports, a)
	}
	refs := map[string]bool{}
	edges := map[string][]string{}
	entries := []qualityMapEntry{}
	proxies := []map[string]any{}
	groups := []map[string]any{}
	for _, n := range s.Nodes {
		if !qualityToken(n.ID) || !strings.HasPrefix(n.ID, "node:") || len(n.ID) == 5 || refs[n.ID] || !qualityAlias(n.DisplayAlias) {
			return bad()
		}
		refs[n.ID] = true
		adapter := map[string]string{"direct": "Direct", "http": "Http", "socks5": "Socks5"}[n.Type]
		if adapter == "" {
			return bad()
		}
		p := map[string]any{"name": n.ID, "type": n.Type}
		if n.Type == "direct" {
			if n.Endpoint != "" || n.DialerProxy != "" {
				return bad()
			}
		} else {
			a, ok := qualityEndpoint(n.Endpoint)
			if !ok {
				return bad()
			}
			p["server"], p["port"] = a.Addr().String(), a.Port()
		}
		if n.DialerProxy != "" {
			p["dialer-proxy"] = n.DialerProxy
			edges[n.ID] = []string{n.DialerProxy}
		}
		id := n.ID
		entries = append(entries, qualityMapEntry{InternalName: id, AdapterType: adapter, NodeID: &id, DisplayAlias: n.DisplayAlias})
		proxies = append(proxies, p)
	}
	for _, g := range s.Groups {
		if !qualityToken(g.ID) || !strings.HasPrefix(g.ID, "group:") || len(g.ID) == 6 || refs[g.ID] || !qualityAlias(g.DisplayAlias) || len(g.Members) == 0 || len(g.Members) > 64 {
			return bad()
		}
		refs[g.ID] = true
		seen := map[string]bool{}
		for _, m := range g.Members {
			if seen[m] {
				return bad()
			}
			seen[m] = true
		}
		edges[g.ID] = append([]string(nil), g.Members...)
		id := g.ID
		entries = append(entries, qualityMapEntry{InternalName: id, AdapterType: "Selector", GroupID: &id, DisplayAlias: g.DisplayAlias})
		groups = append(groups, map[string]any{"name": id, "type": "select", "proxies": append([]string(nil), g.Members...)})
	}
	if !refs[s.DefaultTarget] || !refs[s.DirectTarget] {
		return bad()
	}
	direct := false
	for _, n := range s.Nodes {
		if n.ID == s.DirectTarget && n.Type == "direct" {
			direct = true
		}
	}
	if !direct {
		return bad()
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	depth := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if !refs[id] || visiting[id] {
			return false
		}
		if done[id] {
			return true
		}
		visiting[id] = true
		depth[id] = 1
		for _, ref := range edges[id] {
			if !visit(ref) {
				return false
			}
			if candidate := depth[ref] + 1; candidate > depth[id] {
				depth[id] = candidate
			}
		}
		if depth[id] > 8 {
			return false
		}
		visiting[id] = false
		done[id] = true
		return true
	}
	for id := range refs {
		if !visit(id) {
			return bad()
		}
	}
	config := map[string]any{"mode": "rule", "log-level": "silent", "ipv6": false, "find-process-mode": "off", "external-controller": s.ControllerEndpoint, "secret": s.ControllerSecret, "listeners": []map[string]any{{"name": "quality-measured", "type": "mixed", "listen": ports[0].Addr().String(), "port": ports[0].Port(), "udp": false}, {"name": "quality-health", "type": "mixed", "listen": ports[1].Addr().String(), "port": ports[1].Port(), "udp": false, "proxy": "DIRECT"}}, "hosts": map[string]string{"alpha.test": "127.0.0.1", "direct.test": "127.0.0.1"}, "proxies": proxies, "proxy-groups": groups, "rules": []string{"DOMAIN,direct.test," + s.DirectTarget, "MATCH," + s.DefaultTarget}}
	cfg, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return bad()
	}
	mapping, err := json.MarshalIndent(struct {
		SchemaVersion    int               `json:"schema_version"`
		ConfigGeneration string            `json:"config_generation"`
		Entries          []qualityMapEntry `json:"entries"`
	}{1, s.Generation, entries}, "", "  ")
	if err != nil {
		return bad()
	}
	return QualityBundle{cfg, mapping}, nil
}
