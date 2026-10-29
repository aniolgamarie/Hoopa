// Package capabilityprofile validates the bounded, protected experiment profile.
// It does not perform I/O or claim support for connection process attribution.
package capabilityprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strings"
)

// ErrProfile deliberately contains no input values, including credentials.
var ErrProfile = errors.New("invalid protected experiment profile")

type Config struct {
	Mode            string            `json:"mode"`
	LogLevel        string            `json:"log-level"`
	IPv6            bool              `json:"ipv6"`
	FindProcessMode string            `json:"find-process-mode"`
	Controller      string            `json:"external-controller"`
	Secret          string            `json:"secret"`
	Listeners       []Entry           `json:"listeners"`
	Hosts           map[string]string `json:"hosts"`
	Proxies         []Node            `json:"proxies"`
	Groups          []Group           `json:"proxy-groups"`
	Rules           []string          `json:"rules"`
	Profile         CacheProfile      `json:"profile"`
	ProgramRules    string            `json:"-"`
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
type Node struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Server      string `json:"server,omitempty"`
	Port        uint16 `json:"port,omitempty"`
	DialerProxy string `json:"dialer-proxy,omitempty"`
}
type Group struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Proxies []string `json:"proxies"`
}
type CacheProfile struct {
	StoreSelected bool `json:"store-selected"`
	StoreFakeIP   bool `json:"store-fake-ip"`
}

// Validate checks the entire candidate before returning any configuration. The
// rules and reference order are retained exactly as supplied. generation is a
// separate immutable identity and is validated, not inserted into the profile.
func Validate(raw []byte, generation string) (Config, error) {
	return validateRules(raw, generation, validRule)
}

func validateRules(raw []byte, generation string, ruleCheck func(string, func(string) bool, map[string]bool) bool) (Config, error) {
	bad := func() (Config, error) { return Config{}, ErrProfile }
	if len(raw) == 0 || len(raw) > 64*1024 || !token(generation) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := readValue(d, 0)
	if err != nil {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	root, ok := object(value, "mode log-level ipv6 find-process-mode external-controller secret listeners proxies rules profile", "hosts proxy-groups")
	if !ok || !shape(root) {
		return bad()
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil {
		return bad()
	}
	if c.Mode != "rule" || c.LogLevel != "silent" || c.IPv6 || c.FindProcessMode != "off" || c.Profile.StoreSelected || c.Profile.StoreFakeIP || !credential(c.Secret) {
		return bad()
	}
	controller, e := netip.ParseAddrPort(c.Controller)
	if e != nil || controller.Addr().String() != "127.0.0.1" || controller.Port() == 0 {
		return bad()
	}
	endpoints := map[netip.AddrPort]bool{controller: true}
	entries := map[string]bool{}
	credentials := map[User]bool{}
	if len(c.Listeners) < 1 || len(c.Listeners) > 4 {
		return bad()
	}
	for _, l := range c.Listeners {
		ep := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), l.Port)
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
	if len(c.Proxies) == 0 || len(c.Proxies)+len(c.Groups) > 64 {
		return bad()
	}
	refs := map[string]bool{}
	edges := map[string][]string{}
	nodes := root["proxies"].([]any)
	for i, n := range c.Proxies {
		if !id(n.Name, "node:") || refs[n.Name] {
			return bad()
		}
		refs[n.Name] = true
		fields := nodes[i].(map[string]any)
		if n.Type == "direct" {
			if has(fields, "server") || has(fields, "port") || has(fields, "dialer-proxy") {
				return bad()
			}
		} else if n.Type == "http" || n.Type == "socks5" {
			if n.Server != "127.0.0.1" || n.Port == 0 {
				return bad()
			}
			ep := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), n.Port)
			if endpoints[ep] {
				return bad()
			}
			endpoints[ep] = true
			if has(fields, "dialer-proxy") {
				if n.DialerProxy == "" {
					return bad()
				}
				edges[n.Name] = []string{n.DialerProxy}
			}
		} else {
			return bad()
		}
	}
	for _, g := range c.Groups {
		if !id(g.Name, "group:") || refs[g.Name] || g.Type != "select" || len(g.Proxies) == 0 || len(g.Proxies) > 64 {
			return bad()
		}
		refs[g.Name] = true
		seen := map[string]bool{}
		for _, member := range g.Proxies {
			if seen[member] {
				return bad()
			}
			seen[member] = true
		}
		edges[g.Name] = g.Proxies
	}
	state, depth := map[string]int{}, map[string]int{}
	var visit func(string) bool
	visit = func(name string) bool {
		if !refs[name] || state[name] == 1 {
			return false
		}
		if state[name] == 2 {
			return true
		}
		state[name], depth[name] = 1, 1
		for _, ref := range edges[name] {
			if !visit(ref) {
				return false
			}
			if depth[ref]+1 > depth[name] {
				depth[name] = depth[ref] + 1
			}
		}
		if depth[name] > 8 {
			return false
		}
		state[name] = 2
		return true
	}
	for ref := range refs {
		if !visit(ref) {
			return bad()
		}
	}
	if len(c.Rules) < 1 || len(c.Rules) > 64 {
		return bad()
	}
	target := func(s string) bool { return s == "REJECT" || refs[s] }
	for i, rule := range c.Rules {
		if i == len(c.Rules)-1 {
			parts := strings.Split(rule, ",")
			if len(parts) != 2 || parts[0] != "MATCH" || !target(parts[1]) {
				return bad()
			}
		} else if !ruleCheck(rule, target, entries) {
			return bad()
		}
	}
	return c, nil
}

// readValue rejects duplicate keys and null at every depth, before Go's normal
// decoder can silently replace duplicates or convert null to a zero value.
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrProfile
	}
	t, e := d.Token()
	if e != nil || t == nil {
		return nil, ErrProfile
	}
	delim, compound := t.(json.Delim)
	if !compound {
		return t, nil
	}
	switch delim {
	case '{':
		v := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, ErrProfile
			}
			key, ok := k.(string)
			if !ok {
				return nil, ErrProfile
			}
			if _, ok = v[key]; ok {
				return nil, ErrProfile
			}
			child, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			v[key] = child
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, ErrProfile
		}
		return v, nil
	case '[':
		v := []any{}
		for d.More() {
			child, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			v = append(v, child)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, ErrProfile
		}
		return v, nil
	}
	return nil, ErrProfile
}
func object(v any, required, optional string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		if !has(m, k) {
			return nil, false
		}
		allowed[k] = true
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return nil, false
		}
	}
	return m, true
}
func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }
func shape(root map[string]any) bool {
	if _, ok := object(root["profile"], "store-selected store-fake-ip", ""); !ok {
		return false
	}
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
			if _, ok := object(u, "username password", ""); !ok {
				return false
			}
		}
	}
	nodes, ok := root["proxies"].([]any)
	if !ok {
		return false
	}
	for _, n := range nodes {
		if _, ok := object(n, "name type", "server port dialer-proxy"); !ok {
			return false
		}
	}
	if has(root, "proxy-groups") {
		groups, ok := root["proxy-groups"].([]any)
		if !ok {
			return false
		}
		for _, g := range groups {
			if _, ok := object(g, "name type proxies", ""); !ok {
				return false
			}
		}
	}
	return true
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
func id(s, prefix string) bool {
	return token(s) && strings.HasPrefix(s, prefix) && token(strings.TrimPrefix(s, prefix))
}
func credential(s string) bool {
	if len(s) < 32 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
func domainToken(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func atom(parts []string, entries map[string]bool) bool {
	if len(parts) < 2 || len(parts) > 3 || parts[1] == "" || strings.ContainsAny(parts[1], ",() \t\n\r\v\f") {
		return false
	}
	switch parts[0] {
	case "DOMAIN", "DOMAIN-SUFFIX":
		return len(parts) == 2 && domainToken(parts[1])
	case "IN-NAME":
		return len(parts) == 2 && entries[parts[1]]
	case "IP-CIDR":
		p, e := netip.ParsePrefix(parts[1])
		return e == nil && p.Addr().Is4() && (len(parts) == 2 || parts[2] == "no-resolve")
	}
	return false
}
func validRule(rule string, target func(string) bool, entries map[string]bool) bool {
	return validRuleWithAtom(rule, target, entries, atom)
}

func validRuleWithAtom(rule string, target func(string) bool, entries map[string]bool, checkAtom func([]string, map[string]bool) bool) bool {
	if strings.HasPrefix(rule, "AND,(") {
		// The grammar is AND,((atom),(atom)),target. Splitting at the
		// outer close keeps no-resolve inside an IP-CIDR atom intact.
		end := strings.LastIndex(rule, ")),")
		if end < 0 || !target(rule[end+3:]) {
			return false
		}
		if !strings.HasPrefix(rule, "AND,((") || end < len("AND,((") {
			return false
		}
		body := rule[len("AND,(("):end]
		atoms := strings.Split(body, "),(")
		if len(atoms) < 2 || len(atoms) > 8 {
			return false
		}
		for _, a := range atoms {
			if !checkAtom(strings.Split(a, ","), entries) {
				return false
			}
		}
		return true
	}
	parts := strings.Split(rule, ",")
	if len(parts) < 3 {
		return false
	}
	// no-resolve is the sole supported rule option and follows the target.
	if parts[0] == "IP-CIDR" && len(parts) == 4 {
		return target(parts[2]) && checkAtom([]string{parts[0], parts[1], parts[3]}, entries)
	}
	return len(parts) == 3 && target(parts[2]) && checkAtom(parts[:2], entries)
}
