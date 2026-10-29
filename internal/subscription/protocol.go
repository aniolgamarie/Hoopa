package subscription

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// A protocol is an explicit finite profile, not an upstream option pass-through.
// Rendering uses exactly the normalized values used by connection identity.
func parseConnection(value any) (string, map[string]any, error) {
	p := nodeParser{}
	in, ok := value.(map[string]any)
	if !ok {
		return "", nil, ErrInvalidProvider
	}
	kind := p.text(in["type"], 4096, true)
	options := ""
	required := ""
	switch kind {
	case "socks5":
		options = "username password udp tls skip-cert-verify"
	case "http":
		options = "username password tls sni skip-cert-verify"
	case "ss":
		options = "udp"
		required = "cipher password"
	case "trojan":
		options = "network sni alpn skip-cert-verify udp ws-opts"
		required = "password"
	case "vmess":
		options = "network tls servername alpn skip-cert-verify udp ws-opts"
		required = "uuid cipher alterId"
	case "vless":
		options = "network tls servername alpn skip-cert-verify udp ws-opts"
		required = "uuid"
	case "hysteria2":
		options = "sni alpn skip-cert-verify"
		required = "password"
	default:
		if p.err != nil {
			return "", nil, p.err
		}
		return "", nil, ErrUnsupportedNode
	}
	p.object(in, "name type server port "+required, options)
	name := p.text(in["name"], 1024, true)
	if hasControl(name) {
		p.invalid()
	}
	server := p.host(in["server"])
	port := p.integer(in["port"], 1, 65535)
	out := map[string]any{"type": kind, "server": server, "port": port}
	if kind == "socks5" || kind == "http" {
		_, userPresent := in["username"]
		_, passPresent := in["password"]
		user, password := "", ""
		max := 4096
		if kind == "socks5" {
			max = 255
		}
		if userPresent {
			user = p.text(in["username"], max, true)
		}
		if passPresent {
			password = p.text(in["password"], max, true)
		}
		if userPresent != passPresent {
			p.invalid()
		}
		out["username"] = user
		out["password"] = password
	}
	if kind == "ss" || kind == "trojan" || kind == "hysteria2" {
		out["password"] = p.text(in["password"], 4096, true)
	}
	if kind == "ss" {
		out["cipher"] = p.choice(in["cipher"], "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305")
	}
	if kind == "vmess" || kind == "vless" {
		uuid := p.text(in["uuid"], 36, true)
		if !uuidForm.MatchString(uuid) {
			p.invalid()
		}
		out["uuid"] = strings.ToLower(uuid)
	}
	if kind == "vmess" {
		out["cipher"] = p.choice(in["cipher"], "auto", "aes-128-gcm", "chacha20-poly1305", "none")
		alter := p.integer(in["alterId"], 0, 9007199254740991)
		if alter != 0 {
			p.unsupported()
		}
		out["alterId"] = alter
	}
	if kind != "http" && kind != "hysteria2" {
		out["udp"] = p.optionalBool(in, "udp", false)
	}
	network := ""
	if kind == "trojan" || kind == "vmess" || kind == "vless" {
		network = "tcp"
		if value, ok := in["network"]; ok {
			network = p.choice(value, "tcp", "ws")
		}
		out["network"] = network
		if network == "ws" {
			out["ws-opts"] = p.websocket(in)
		} else if _, ok := in["ws-opts"]; ok {
			p.invalid()
		}
	}
	tls := kind == "trojan" || kind == "hysteria2"
	if kind == "socks5" || kind == "http" || kind == "vmess" || kind == "vless" {
		tls = p.optionalBool(in, "tls", false)
		out["tls"] = tls
	}
	if kind == "socks5" {
		skip := p.optionalBool(in, "skip-cert-verify", false)
		if skip {
			p.unsupported()
		}
		out["skip-cert-verify"] = false
	} else if kind != "ss" {
		sniKey := "sni"
		if kind == "vmess" || kind == "vless" {
			sniKey = "servername"
		}
		if tls {
			sni := server
			if value, ok := in[sniKey]; ok {
				sni = p.host(value)
			} else if network == "ws" && (kind == "vmess" || kind == "vless") {
				// Match native TLS WS fallback: explicit servername, then WS Host,
				// then server. A Host authority with a port requires explicit SNI.
				ws, _ := out["ws-opts"].(map[string]any)
				headers, _ := ws["headers"].(map[string]any)
				if host, ok := headers["Host"].(string); ok {
					var hasPort bool
					sni, hasPort = authorityHostname(host)
					if hasPort {
						p.invalid()
					}
				}
			}
			out[sniKey] = sni
			skip := p.optionalBool(in, "skip-cert-verify", false)
			if skip {
				p.unsupported()
			}
			out["skip-cert-verify"] = false
			if kind != "http" {
				p.alpn(in, out, kind, network)
			}
		} else {
			for _, key := range []string{sniKey, "alpn", "skip-cert-verify"} {
				if _, ok := in[key]; ok {
					p.invalid()
				}
			}
		}
	}

	if p.err != nil {
		return "", nil, p.err
	}
	return name, out, nil
}

type nodeParser struct{ err error }

func (p *nodeParser) invalid() {
	if p.err == nil {
		p.err = ErrInvalidProvider
	}
}
func (p *nodeParser) unsupported() {
	if p.err == nil {
		p.err = ErrUnsupportedNode
	}
}
func (p *nodeParser) object(m map[string]any, required, optional string) {
	allowed := map[string]bool{}
	for _, key := range strings.Fields(required) {
		allowed[key] = true
		if _, ok := m[key]; !ok {
			p.invalid()
		}
	}
	for _, key := range strings.Fields(optional) {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			p.invalid()
		}
	}
}
func (p *nodeParser) text(v any, max int, nonempty bool) string {
	s, ok := v.(string)
	if !ok || !boundedText(s, max, nonempty) || strings.ContainsAny(s, "\x00\r\n") {
		p.invalid()
	}
	return s
}
func (p *nodeParser) optionalBool(m map[string]any, key string, fallback bool) bool {
	v, present := m[key]
	if !present {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		p.invalid()
	}
	return b
}
func (p *nodeParser) choice(v any, values ...string) string {
	s := p.text(v, 4096, true)
	for _, value := range values {
		if s == value {
			return s
		}
	}
	p.unsupported()
	return s
}
func (p *nodeParser) host(v any) string {
	s := p.text(v, 4096, true)
	host, ok := canonicalHost(s)
	if !ok {
		p.invalid()
	}
	return host
}
func (p *nodeParser) integer(v any, min, max uint64) uint64 {
	n, ok := v.(json.Number)
	if !ok {
		p.invalid()
		return 0
	}
	s := n.String()
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	mantissa, exponent, hasExponent := strings.Cut(strings.ToLower(s), "e")
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		if min > 0 {
			p.invalid()
		}
		return 0
	}
	if negative {
		p.invalid()
		return 0
	}
	exp := int64(0)
	if hasExponent {
		var err error
		exp, err = strconv.ParseInt(exponent, 10, 32)
		if err != nil {
			p.invalid()
			return 0
		}
	}
	scale := exp - int64(fraction)
	for strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale++
	}
	if scale < 0 || scale > 16 || int64(len(digits))+scale > 16 {
		p.invalid()
		return 0
	}
	result, err := strconv.ParseUint(digits+strings.Repeat("0", int(scale)), 10, 64)
	if err != nil || result < min || result > max {
		p.invalid()
		return 0
	}
	return result
}
func hasControl(s string) bool {
	for _, c := range s {
		if unicode.IsControl(c) {
			return true
		}
	}
	return false
}

var uuidForm = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func canonicalHost(s string) (string, bool) {
	// Brackets and zones are authority syntax, never part of a provider server.
	if s == "" || strings.ContainsAny(s, "[]%/\\:@ \t\r\n\x00") {
		if addr, err := netip.ParseAddr(s); err == nil && addr.Zone() == "" {
			return addr.String(), true
		}
		return "", false
	}
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	if addr, err := netip.ParseAddr(s); err == nil && addr.Zone() == "" {
		return addr.String(), true
	}
	if len(s) == 0 || len(s) > 253 {
		return "", false
	}
	numericDotted := strings.Contains(s, ".")
	for _, c := range s {
		if c != '.' && (c < '0' || c > '9') {
			numericDotted = false
			break
		}
	}
	if numericDotted {
		return "", false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	return s, true
}
func (p *nodeParser) websocket(in map[string]any) map[string]any {
	path := "/"
	headers := map[string]any{}
	if value, present := in["ws-opts"]; present {
		m, ok := value.(map[string]any)
		if !ok {
			p.invalid()
			return nil
		}
		p.object(m, "", "path headers")
		if value, present := m["path"]; present {
			path = p.text(value, 4096, true)
			p.websocketPath(path)
		}
		if value, present := m["headers"]; present {
			h, ok := value.(map[string]any)
			if !ok {
				p.invalid()
				return nil
			}
			p.object(h, "", "Host")
			if value, present := h["Host"]; present {
				host := p.text(value, 4096, true)
				normalized, ok := canonicalAuthority(host)
				if !ok {
					p.invalid()
				}
				headers["Host"] = normalized
			}
		}
	}
	return map[string]any{"path": path, "headers": headers}
}
func canonicalAuthority(s string) (string, bool) {
	host, port, hasPort := s, "", false
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", false
		}
		host = s[1:end]
		rest := s[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", false
			}
			hasPort = true
			port = rest[1:]
		}
		addr, err := netip.ParseAddr(host)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", false
		}
		host = "[" + addr.String() + "]"
	} else {
		if strings.Count(s, ":") > 1 {
			return "", false
		}
		if i := strings.IndexByte(s, ':'); i >= 0 {
			host, port, hasPort = s[:i], s[i+1:], true
		}
		var ok bool
		host, ok = canonicalHost(host)
		if !ok {
			return "", false
		}
	}
	if hasPort {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return "", false
		}
		host += ":" + strconv.FormatUint(value, 10)
	}
	return host, true
}

// authorityHostname receives only a previously validated normalized authority.
func authorityHostname(authority string) (string, bool) {
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		return authority[1:end], len(authority) > end+1
	}
	host, _, hasPort := strings.Cut(authority, ":")
	return host, hasPort
}
func (p *nodeParser) alpn(in, out map[string]any, kind, network string) {
	tlsWS := network == "ws"
	allowEmpty := (kind == "vmess" || kind == "vless") && !tlsWS
	alpn := []any{}
	if tlsWS {
		alpn = []any{"http/1.1"}
	} else if kind == "trojan" {
		alpn = []any{"h2", "http/1.1"}
	}
	value, present := in["alpn"]
	if present {
		values, ok := value.([]any)
		if !ok || len(values) > 16 || len(values) == 0 && !allowEmpty && !tlsWS {
			p.invalid()
		}
		alpn = []any{}
		for _, value := range values {
			protocol := p.text(value, 255, true)
			if hasControl(protocol) {
				p.invalid()
			}
			alpn = append(alpn, protocol)
		}
		if tlsWS && (len(alpn) != 1 || alpn[0] != "http/1.1") {
			p.unsupported()
		}
	}
	// HY2 must preserve nil so its downstream native defaults remain effective.
	if kind != "hysteria2" || present {
		out["alpn"] = alpn
	}
}
func (p *nodeParser) websocketPath(path string) {
	if !strings.HasPrefix(path, "/") || hasControl(path) || strings.Contains(path, "#") {
		p.invalid()
		return
	}
	u, err := url.Parse(path)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.Fragment != "" || hasControl(u.Path) {
		p.invalid()
		return
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		p.invalid()
		return
	}
	for key, values := range query {
		if hasControl(key) {
			p.invalid()
		}
		for _, value := range values {
			if hasControl(value) {
				p.invalid()
			}
		}
	}
	// The pinned core interprets decoded query key ed as early-data settings and
	// rewrites the path. Reject every occurrence, including escaped key spellings.
	if _, present := query["ed"]; present {
		p.unsupported()
	}
}
