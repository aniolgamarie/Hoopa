// Package sourceprofile validates the finite generated basic-sources profile.
// It performs no IO and does not grant a binary any capability. Provider input
// is parsed separately; only fully canonical connection maps are accepted here.
package sourceprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"

	wire "hoopa/protocol/sourcecontrol"
)

var ErrProfile = errors.New("invalid protected sources profile")

type Config struct {
	Mode            string            `json:"mode"`
	LogLevel        string            `json:"log-level"`
	IPv6            bool              `json:"ipv6"`
	FindProcessMode string            `json:"find-process-mode"`
	Controller      string            `json:"external-controller"`
	Secret          string            `json:"secret"`
	Listeners       []Entry           `json:"listeners"`
	Hosts           map[string]string `json:"hosts,omitempty"`
	Proxies         []map[string]any  `json:"proxies"`
	Groups          []Group           `json:"proxy-groups"`
	Rules           []string          `json:"rules"`
	Profile         CacheProfile      `json:"profile"`
}
type Entry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Listen string `json:"listen"`
	Port   uint16 `json:"port"`
	UDP    bool   `json:"udp"`
	Users  []User `json:"users"`
}
type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type CacheProfile struct {
	StoreSelected bool `json:"store-selected"`
	StoreFakeIP   bool `json:"store-fake-ip"`
}
type Group struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Proxies        []string `json:"proxies"`
	URL            string   `json:"url,omitempty"`
	Interval       uint64   `json:"interval,omitempty"`
	Timeout        uint64   `json:"timeout,omitempty"`
	Tolerance      *uint64  `json:"tolerance,omitempty"`
	Lazy           *bool    `json:"lazy,omitempty"`
	ExpectedStatus string   `json:"expected-status,omitempty"`
}

func (g Group) Probe() wire.Probe {
	p := wire.Probe{URL: g.URL, IntervalSeconds: g.Interval, TimeoutMS: g.Timeout}
	if g.Tolerance != nil {
		p.ToleranceMS = *g.Tolerance
	}
	return p
}

func Validate(raw []byte, generation string) (Config, error) {
	bad := func() (Config, error) { return Config{}, ErrProfile }
	if len(raw) == 0 || len(raw) > wire.MaxJSONBytes || !validUnicode(raw) || !token(generation) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	root, ok := object(v, "mode log-level ipv6 find-process-mode external-controller secret listeners proxies proxy-groups rules profile", "hosts")
	if !ok {
		return bad()
	}
	if _, ok = object(root["profile"], "store-selected store-fake-ip", ""); !ok {
		return bad()
	}
	if !sourceShapes(root) {
		return bad()
	}
	var c Config
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&c) != nil || c.Mode != "rule" || c.LogLevel != "silent" || c.IPv6 || c.FindProcessMode != "off" || c.Profile.StoreSelected || c.Profile.StoreFakeIP || !credential(c.Secret) {
		return bad()
	}
	controller, e := netip.ParseAddrPort(c.Controller)
	if e != nil || controller.Addr() != netip.MustParseAddr("127.0.0.1") || controller.Port() == 0 || controller.String() != c.Controller {
		return bad()
	}
	endpoints := map[string]bool{c.Controller: true}
	entries := map[string]bool{}
	credentials := map[User]bool{}
	if len(c.Listeners) < 1 || len(c.Listeners) > 4 {
		return bad()
	}
	for _, l := range c.Listeners {
		ep := net.JoinHostPort(l.Listen, strconv.Itoa(int(l.Port)))
		if !token(l.Name) || entries[l.Name] || l.Type != "mixed" || l.Listen != "127.0.0.1" || l.Port == 0 || l.UDP || endpoints[ep] || len(l.Users) != 1 {
			return bad()
		}
		u := l.Users[0]
		if !token(u.Username) || strings.Contains(u.Username, ":") || !credential(u.Password) || u.Password == c.Secret || credentials[u] {
			return bad()
		}
		entries[l.Name], endpoints[ep], credentials[u] = true, true, true
	}
	for domain, addr := range c.Hosts {
		if !domainToken(domain) || addr != "127.0.0.1" {
			return bad()
		}
	}
	if len(c.Proxies)+len(c.Groups) > 64 {
		return bad()
	}
	refs := map[string]bool{}
	remote := map[string]bool{}
	edges := map[string][]string{}
	for _, n := range c.Proxies {
		name, _ := n["name"].(string)
		kind, _ := n["type"].(string)
		if !id(name, "node:") || refs[name] {
			return bad()
		}
		refs[name] = true
		if kind == "direct" {
			if len(n) != 2 {
				return bad()
			}
			continue
		}
		if !canonicalConnection(n) {
			return bad()
		}
		server, _ := n["server"].(string)
		port, _ := n["port"].(json.Number)
		// A source may expose several credentials for one endpoint. It must not
		// recurse into our own controller or application listeners.
		if endpoints[net.JoinHostPort(server, port.String())] {
			return bad()
		}
		// The finite hosts map is known at compilation time. Reject a declared
		// hostname alias of one of our own endpoints as well as a literal IP.
		for host, address := range c.Hosts {
			if strings.EqualFold(server, host) && endpoints[net.JoinHostPort(address, port.String())] {
				return bad()
			}
		}
		remote[name] = true
	}
	for _, g := range c.Groups {
		if !id(g.Name, "group:") || refs[g.Name] || len(g.Proxies) < 1 || len(g.Proxies) > 64 {
			return bad()
		}
		refs[g.Name] = true
		edges[g.Name] = g.Proxies
		if g.Type == "select" {
			if len(g.Proxies) != 1 {
				return bad()
			}
		} else if g.Type == "url-test" {
			if g.Probe().Validate() != nil || g.Tolerance == nil || g.Lazy == nil || *g.Lazy || g.ExpectedStatus != "200-299" {
				return bad()
			}
		} else {
			return bad()
		}
		seen := map[string]bool{}
		for _, member := range g.Proxies {
			if seen[member] || g.Type == "url-test" && !remote[member] {
				return bad()
			}
			seen[member] = true
		}
	}
	states := map[string]uint8{}
	depth := map[string]int{}
	var visit func(string) bool
	visit = func(name string) bool {
		if name == "REJECT" {
			return true
		}
		if !refs[name] || states[name] == 1 {
			return false
		}
		if states[name] == 2 {
			return true
		}
		states[name] = 1
		depth[name] = 1
		for _, member := range edges[name] {
			if !visit(member) {
				return false
			}
			if depth[member]+1 > depth[name] {
				depth[name] = depth[member] + 1
			}
		}
		if depth[name] > 8 {
			return false
		}
		states[name] = 2
		return true
	}
	for name := range refs {
		if !visit(name) {
			return bad()
		}
	}
	if len(c.Rules) < 1 || len(c.Rules) > 64 {
		return bad()
	}
	target := func(s string) bool { return s == "REJECT" || refs[s] }
	for i, r := range c.Rules {
		if len(r) > wire.MaxPayloadBytes {
			return bad()
		}
		if i == len(c.Rules)-1 {
			parts := strings.Split(r, ",")
			if len(parts) != 2 || parts[0] != "MATCH" || !target(parts[1]) {
				return bad()
			}
		} else if !validRule(r, target, entries) {
			return bad()
		}
	}
	return c, nil
}

func sourceShapes(root map[string]any) bool {
	listeners, ok := root["listeners"].([]any)
	if !ok {
		return false
	}
	for _, v := range listeners {
		l, ok := object(v, "name type listen port udp users", "")
		if !ok {
			return false
		}
		users, ok := l["users"].([]any)
		if !ok {
			return false
		}
		for _, u := range users {
			if _, ok = object(u, "username password", ""); !ok {
				return false
			}
		}
	}
	nodes, ok := root["proxies"].([]any)
	if !ok {
		return false
	}
	for _, n := range nodes {
		if _, ok := n.(map[string]any); !ok {
			return false
		}
	}
	groups, ok := root["proxy-groups"].([]any)
	if !ok {
		return false
	}
	for _, v := range groups {
		g, ok := v.(map[string]any)
		if !ok {
			return false
		}
		extra := ""
		if g["type"] == "url-test" {
			extra = "url interval timeout tolerance lazy expected-status"
		}
		if _, ok = object(g, "name type proxies "+extra, ""); !ok {
			return false
		}
		for _, key := range []string{"interval", "timeout", "tolerance"} {
			if x, exists := g[key]; exists {
				n, ok := x.(json.Number)
				if !ok {
					return false
				}
				u, e := strconv.ParseUint(n.String(), 10, 64)
				if e != nil || strconv.FormatUint(u, 10) != n.String() {
					return false
				}
			}
		}
	}
	return true
}

func canonicalConnection(n map[string]any) bool {
	// The provider parser normalizes absent credentials to an explicit empty
	// pair. Reconstruct only that absence, then require byte-equivalent maps.
	in := make(map[string]any, len(n))
	for k, v := range n {
		in[k] = v
	}
	if n["username"] == "" && n["password"] == "" {
		delete(in, "username")
		delete(in, "password")
	}
	name, normalized, err := parseConnection(in)
	if err != nil {
		return false
	}
	normalized["name"] = name
	a, e1 := json.Marshal(n)
	b, e2 := json.Marshal(normalized)
	return e1 == nil && e2 == nil && bytes.Equal(a, b)
}
