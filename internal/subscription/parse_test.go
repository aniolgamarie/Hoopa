package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hoopa/internal/config"
	"strings"
	"testing"
)

const testUUID = "01234567-89ab-cdef-0123-456789abcdef"

func profileNode(kind string) map[string]any {
	m := map[string]any{"name": "ordinary", "type": kind, "server": "example.invalid", "port": 443}
	switch kind {
	case "ss":
		m["cipher"] = "aes-128-gcm"
		m["password"] = "private-test-password"
	case "trojan", "hysteria2":
		m["password"] = "private-test-password"
	case "vmess":
		m["uuid"] = testUUID
		m["cipher"] = "auto"
		m["alterId"] = 0
	case "vless":
		m["uuid"] = testUUID
	}
	return m
}

var profileKinds = []string{"socks5", "http", "ss", "trojan", "vmess", "vless", "hysteria2"}

func TestParseFiniteProtocols(t *testing.T) {
	for _, kind := range profileKinds {
		t.Run(kind, func(t *testing.T) {
			node := profileNode(kind)
			snapshot := mustProvider(t, providerRaw(t, node), providerOptions())
			if snapshot.Validate() != nil {
				t.Fatal("parsed snapshot invalid")
			}
			configs, err := snapshot.SensitiveProxyConfigs()
			if err != nil || len(configs) != 1 {
				t.Fatal("missing private configuration")
			}
			if configs[0]["name"] != string(snapshot.Nodes[0].ID) || configs[0]["server"] != "example.invalid" || configs[0]["type"] != kind {
				t.Fatal("rendered connection mismatch")
			}
			for key := range node {
				missing := profileNode(kind)
				delete(missing, key)
				requireProviderError(t, providerRaw(t, missing), providerOptions(), ErrInvalidProvider)
			}
			for _, field := range []string{"dialer-proxy", "interface-name", "routing-mark", "ip-version", "tfo", "mptcp", "smux", "certificate", "private-key", "client-fingerprint", "fingerprint", "reality-opts", "unrecognized"} {
				bad := profileNode(kind)
				bad[field] = "foreign-private-value"
				requireProviderError(t, providerRaw(t, bad), providerOptions(), ErrInvalidProvider)
			}
		})
	}
	for _, cipher := range []string{"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
		node := profileNode("ss")
		node["cipher"] = cipher
		mustProvider(t, providerRaw(t, node), providerOptions())
	}
	for _, cipher := range []string{"auto", "aes-128-gcm", "chacha20-poly1305", "none"} {
		node := profileNode("vmess")
		node["cipher"] = cipher
		mustProvider(t, providerRaw(t, node), providerOptions())
	}
	for _, kind := range []string{"trojan", "vmess", "vless"} {
		node := profileNode(kind)
		node["network"] = "ws"
		if kind != "trojan" {
			node["tls"] = true
		}
		node["ws-opts"] = map[string]any{"path": "/socket?x=y", "headers": map[string]any{"Host": "WS.EXAMPLE.INVALID.:00443"}}
		node["alpn"] = []any{"http/1.1"}
		if kind != "trojan" {
			node["servername"] = "example.invalid"
		}
		snap := mustProvider(t, providerRaw(t, node), providerOptions())
		configs, _ := snap.SensitiveProxyConfigs()
		key := "sni"
		if kind != "trojan" {
			key = "servername"
		}
		if configs[0][key] != "example.invalid" {
			t.Fatal("explicit TLS servername did not take priority")
		}
		host := configs[0]["ws-opts"].(map[string]any)["headers"].(map[string]any)["Host"]
		if host != "ws.example.invalid:443" {
			t.Fatal("WS host not normalized")
		}
	}
}
func TestParseProfileDefaultsEquivalent(t *testing.T) {
	for _, kind := range profileKinds {
		t.Run(kind, func(t *testing.T) {
			base, explicit := profileNode(kind), profileNode(kind)
			if kind != "http" && kind != "hysteria2" {
				explicit["udp"] = false
			}
			switch kind {
			case "socks5":
				explicit["tls"] = false
				explicit["skip-cert-verify"] = false
			case "http":
				explicit["tls"] = false
			case "vmess", "vless":
				explicit["network"] = "tcp"
				explicit["tls"] = false
			case "trojan":
				explicit["network"] = "tcp"
				explicit["sni"] = "EXAMPLE.INVALID."
				explicit["skip-cert-verify"] = false
			case "hysteria2":
				explicit["sni"] = "EXAMPLE.INVALID."
				explicit["skip-cert-verify"] = false
			}
			first := mustProvider(t, providerRaw(t, base), providerOptions())
			second := mustProvider(t, providerRaw(t, explicit), providerOptions())
			if first.Nodes[0].ID != second.Nodes[0].ID {
				t.Fatal("explicit default changed identity")
			}
		})
	}
	for _, kind := range []string{"trojan", "vmess", "vless"} {
		a, b := profileNode(kind), profileNode(kind)
		a["network"] = "ws"
		b["network"] = "ws"
		b["ws-opts"] = map[string]any{"path": "/", "headers": map[string]any{}}
		if mustProvider(t, providerRaw(t, a), providerOptions()).Nodes[0].ID != mustProvider(t, providerRaw(t, b), providerOptions()).Nodes[0].ID {
			t.Fatal("WS defaults changed identity")
		}
	}
	for _, kind := range []string{"http", "vmess", "vless"} {
		a, b := profileNode(kind), profileNode(kind)
		a["tls"] = true
		b["tls"] = true
		b["skip-cert-verify"] = false
		key := "sni"
		if kind != "http" {
			key = "servername"
		}
		b[key] = "EXAMPLE.INVALID."
		if mustProvider(t, providerRaw(t, a), providerOptions()).Nodes[0].ID != mustProvider(t, providerRaw(t, b), providerOptions()).Nodes[0].ID {
			t.Fatal("TLS defaults changed identity")
		}
	}
}
func TestParseProfileRejections(t *testing.T) {
	cases := []struct {
		kind, field string
		value       any
		want        error
	}{
		{"ss", "cipher", "2022-blake3-aes-128-gcm", ErrUnsupportedNode}, {"ss", "plugin", "obfs", ErrInvalidProvider}, {"ss", "udp-over-tcp", true, ErrInvalidProvider},
		{"vmess", "cipher", "aes-128-cfb", ErrUnsupportedNode}, {"vmess", "alterId", 1, ErrUnsupportedNode}, {"vmess", "alterId", 1.25, ErrInvalidProvider}, {"vmess", "uuid", "bad", ErrInvalidProvider},
		{"vless", "flow", "xtls-rprx-vision", ErrInvalidProvider}, {"vless", "encryption", "none", ErrInvalidProvider}, {"trojan", "network", "grpc", ErrUnsupportedNode},
		{"trojan", "skip-cert-verify", true, ErrUnsupportedNode}, {"socks5", "skip-cert-verify", true, ErrUnsupportedNode}, {"socks5", "sni", "example.invalid", ErrInvalidProvider},
		{"http", "udp", false, ErrInvalidProvider}, {"http", "sni", "example.invalid", ErrInvalidProvider}, {"http", "skip-cert-verify", false, ErrInvalidProvider},
		{"vless", "servername", "example.invalid", ErrInvalidProvider}, {"vmess", "alpn", []any{"h2"}, ErrInvalidProvider}, {"hysteria2", "udp", true, ErrInvalidProvider},
		{"hysteria2", "ports", "1-65535", ErrInvalidProvider}, {"hysteria2", "obfs", "salamander", ErrInvalidProvider},
		{"socks5", "username", "user", ErrInvalidProvider}, {"socks5", "password", "pass", ErrInvalidProvider}, {"socks5", "udp", "false", ErrInvalidProvider}, {"socks5", "tls", nil, ErrInvalidProvider},
		{"vless", "ws-opts", map[string]any{}, ErrInvalidProvider},
	}
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := profileNode(c.kind)
			m[c.field] = c.value
			requireProviderError(t, providerRaw(t, m), providerOptions(), c.want)
		})
	}
	for _, kind := range []string{"socks5", "http"} {
		for _, values := range [][2]any{{"", ""}, {nil, nil}, {"user", ""}, {"user", "secret\n"}} {
			m := profileNode(kind)
			m["username"], m["password"] = values[0], values[1]
			requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
		}
		m := profileNode(kind)
		m["username"], m["password"] = "user", strings.Repeat("x", 256)
		if kind == "socks5" {
			requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
		} else {
			mustProvider(t, providerRaw(t, m), providerOptions())
		}
	}
	for _, value := range []any{[]any{}, []any{""}, []any{"h2\x7f"}, []any{true}, "h2", nil} {
		m := profileNode("trojan")
		m["alpn"] = value
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	m := profileNode("trojan")
	m["alpn"] = stringsToAny(make([]string, 17))
	requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
}
func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
func TestParseEndpointAndScalarBounds(t *testing.T) {
	for _, host := range []string{"", "https://example.invalid", "host:443", "host/path", "bad host", "bad_host", "-bad.host", "bad..host", "host..", "999.999.999.999", "[::1]", "fe80::1%eth0", "éxample.invalid", strings.Repeat("x", 64) + ".invalid"} {
		m := profileNode("socks5")
		m["server"] = host
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, port := range []any{0, 65536, -1, 1.1, "443", nil, json.Number("1e99999999999999999999999")} {
		m := profileNode("socks5")
		m["port"] = port
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, port := range []any{1, 65535, json.Number("443.0"), json.Number("4.43e2")} {
		m := profileNode("socks5")
		m["port"] = port
		mustProvider(t, providerRaw(t, m), providerOptions())
	}
	for _, name := range []string{"", "bad\x00name", "bad\x7fname", strings.Repeat("x", 1025)} {
		m := profileNode("socks5")
		m["name"] = name
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, password := range []string{"", "bad\rpassword", "bad\x00password", strings.Repeat("x", 4097)} {
		m := profileNode("trojan")
		m["password"] = password
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
}
func TestParseWebsocketAllowlist(t *testing.T) {
	for _, opts := range []any{nil, "/", map[string]any{"early-data": 1}, map[string]any{"path": "bad"}, map[string]any{"path": "/#fragment"}, map[string]any{"path": "/\n"}, map[string]any{"headers": nil}, map[string]any{"headers": map[string]any{"host": "example.invalid"}}, map[string]any{"headers": map[string]any{"Authorization": "secret"}}} {
		m := profileNode("trojan")
		m["network"] = "ws"
		m["ws-opts"] = opts
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, host := range []string{"example.invalid:", "example.invalid:0", "example.invalid:65536", "user@example.invalid", "example.invalid/path", "example.invalid\r\n", "[not-ip]", "[127.0.0.1]", "::1"} {
		m := profileNode("vless")
		m["network"] = "ws"
		m["ws-opts"] = map[string]any{"headers": map[string]any{"Host": host}}
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, host := range []string{"example.invalid", "example.invalid:65535", "[::1]", "[2001:0db8::1]:443"} {
		m := profileNode("vless")
		m["network"] = "ws"
		m["ws-opts"] = map[string]any{"headers": map[string]any{"Host": host}}
		mustProvider(t, providerRaw(t, m), providerOptions())
	}
}
func TestParseDefaultLimitsAndEmpty(t *testing.T) {
	empty := mustProvider(t, []byte(`{"proxies":[]}`), providerOptions())
	if len(empty.Nodes) != 0 || empty.Validate() != nil {
		t.Fatal("empty snapshot invalid")
	}
	requireProviderError(t, []byte(`{"proxies":null}`), providerOptions(), ErrInvalidProvider)
	for i := 0; i < 4; i++ {
		options := providerOptions()
		fields := []*int{&options.Limits.MaxBytes, &options.Limits.MaxNodes, &options.Limits.MaxDepth, &options.Limits.MaxAliases}
		*fields[i] = -1
		requireProviderError(t, []byte(`{"proxies":[]}`), options, ErrInputLimit)
	}
	raw := []byte(`{"proxies":[]}`)
	atLimit := append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), DefaultMaxBytes-len(raw))...)
	mustProvider(t, atLimit, providerOptions())
	requireProviderError(t, append(atLimit, ' '), providerOptions(), ErrInputLimit)
	nodes := make([]map[string]any, DefaultMaxNodes+1)
	for i := range nodes {
		nodes[i] = providerNode("ordinary", i+1)
	}
	snap := mustProvider(t, providerRaw(t, nodes[:DefaultMaxNodes]...), providerOptions())
	if len(snap.Nodes) != 10000 {
		t.Fatal("wrong default node budget")
	}
	requireProviderError(t, providerRaw(t, nodes...), providerOptions(), ErrInputLimit)
	first := "proxies:\n - {name: initial, type: socks5, server: &host 127.0.0.1, port: 1}\n"
	for i := 0; i < DefaultMaxAliases; i++ {
		first += fmt.Sprintf(" - {name: alias, type: socks5, server: *host, port: %d}\n", i+2)
	}
	mustProvider(t, []byte(first), providerOptions())
	requireProviderError(t, []byte(first+" - {name: excess, type: socks5, server: *host, port: 99}\n"), providerOptions(), ErrInputLimit)
	requireProviderError(t, []byte("proxies: "+strings.Repeat("[", 65)+"0"+strings.Repeat("]", 65)), providerOptions(), ErrInputLimit)
}
func TestParseRegionRuleBoundsAndOrder(t *testing.T) {
	raw := providerRaw(t, providerNode("US long node", 443))
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "US long ", Region: "US"}}
	a := mustProvider(t, raw, options)
	options.RegionRules[0], options.RegionRules[1] = options.RegionRules[1], options.RegionRules[0]
	b := mustProvider(t, raw, options)
	if a.Nodes[0].Region.Rule != "US long " || a.Nodes[0] != b.Nodes[0] {
		t.Fatal("region evidence depended on rule order")
	}
	for _, rule := range []RegionRule{{}, {Prefix: "US ", Region: ""}, {Prefix: "\n", Region: "US"}, {Prefix: "US ", Region: "US\x00"}, {Prefix: strings.Repeat("x", 257), Region: "US"}, {Prefix: "US ", Region: strings.Repeat("x", 65)}} {
		options := providerOptions()
		options.RegionRules = []RegionRule{rule}
		requireProviderError(t, raw, options, ErrInvalidProvider)
	}
	options = providerOptions()
	options.RegionRules = make([]RegionRule, 65)
	for i := range options.RegionRules {
		options.RegionRules[i] = RegionRule{Prefix: "US ", Region: "US"}
	}
	requireProviderError(t, raw, options, ErrInputLimit)
	for _, source := range []string{"", "bad source", strings.Repeat("x", 129)} {
		options := providerOptions()
		options.SourceID = config.ID(source)
		requireProviderError(t, raw, options, ErrInvalidProvider)
	}
}

func TestParseNativeALPNDefaults(t *testing.T) {
	rendered := func(node map[string]any) (Snapshot, map[string]any) {
		s := mustProvider(t, providerRaw(t, node), providerOptions())
		configs, err := s.SensitiveProxyConfigs()
		if err != nil {
			t.Fatal(err)
		}
		return s, configs[0]
	}
	trojan := profileNode("trojan")
	base, config := rendered(trojan)
	encoded, _ := json.Marshal(config["alpn"])
	if string(encoded) != `["h2","http/1.1"]` {
		t.Fatal("Trojan TCP native ALPN default changed")
	}
	trojan["alpn"] = []any{"h2", "http/1.1"}
	explicit, _ := rendered(trojan)
	if base.Nodes[0].ID != explicit.Nodes[0].ID {
		t.Fatal("Trojan TCP explicit native default changed identity")
	}
	hy2 := profileNode("hysteria2")
	_, config = rendered(hy2)
	if _, present := config["alpn"]; present {
		t.Fatal("HY2 missing ALPN lost downstream nil/default semantics")
	}
	hy2["alpn"] = []any{"h3"}
	_, config = rendered(hy2)
	if values := config["alpn"].([]any); len(values) != 1 || values[0] != "h3" {
		t.Fatal("HY2 explicit ALPN was lost")
	}
	for _, kind := range []string{"trojan", "hysteria2"} {
		m := profileNode(kind)
		m["alpn"] = []any{}
		requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
	}
	for _, kind := range []string{"vmess", "vless"} {
		a := profileNode(kind)
		a["tls"] = true
		base, config := rendered(a)
		encoded, _ := json.Marshal(config["alpn"])
		if string(encoded) != `[]` {
			t.Fatal("TLS TCP no-ALPN default changed")
		}
		a["alpn"] = []any{}
		explicit, _ := rendered(a)
		if base.Nodes[0].ID != explicit.Nodes[0].ID {
			t.Fatal("explicit no-ALPN changed TLS TCP identity")
		}
		a["alpn"] = nil
		requireProviderError(t, providerRaw(t, a), providerOptions(), ErrInvalidProvider)
	}
	for _, kind := range []string{"trojan", "vmess", "vless"} {
		t.Run(kind, func(t *testing.T) {
			a := profileNode(kind)
			a["network"] = "ws"
			if kind != "trojan" {
				a["tls"] = true
			}
			base, config := rendered(a)
			encoded, _ := json.Marshal(config["alpn"])
			if string(encoded) != `["http/1.1"]` {
				t.Fatal("TLS WS effective ALPN not HTTP/1.1")
			}
			a["alpn"] = []any{"http/1.1"}
			explicit, _ := rendered(a)
			if base.Nodes[0].ID != explicit.Nodes[0].ID {
				t.Fatal("explicit HTTP/1.1 changed TLS WS identity")
			}
			for _, alpn := range [][]any{{"h2"}, {"h2", "http/1.1"}, {"http/1.1", "http/1.1"}} {
				a["alpn"] = alpn
				requireProviderError(t, providerRaw(t, a), providerOptions(), ErrUnsupportedNode)
			}
			a["alpn"] = []any{}
			requireProviderError(t, providerRaw(t, a), providerOptions(), ErrUnsupportedNode)
			if kind != "trojan" {
				a["alpn"] = []any{"http/1.1"}
				a["tls"] = false
				requireProviderError(t, providerRaw(t, a), providerOptions(), ErrInvalidProvider)
			}
		})
	}
}
func TestParseTLSWebsocketSNIFallback(t *testing.T) {
	for _, kind := range []string{"vmess", "vless"} {
		t.Run(kind, func(t *testing.T) {
			for _, host := range []string{"WS.EXAMPLE.INVALID.", "[2001:0db8::1]", "127.0.0.1"} {
				a := profileNode(kind)
				a["network"] = "ws"
				a["tls"] = true
				a["ws-opts"] = map[string]any{"headers": map[string]any{"Host": host}}
				base := mustProvider(t, providerRaw(t, a), providerOptions())
				configs, _ := base.SensitiveProxyConfigs()
				expected := "ws.example.invalid"
				if host == "[2001:0db8::1]" {
					expected = "2001:db8::1"
				}
				if host == "127.0.0.1" {
					expected = host
				}
				if configs[0]["servername"] != expected {
					t.Fatal("WS Host did not supply normalized SNI fallback")
				}
				a["servername"] = expected
				explicit := mustProvider(t, providerRaw(t, a), providerOptions())
				if base.Nodes[0].ID != explicit.Nodes[0].ID {
					t.Fatal("explicit effective SNI changed identity")
				}
				a["servername"] = "example.invalid"
				override := mustProvider(t, providerRaw(t, a), providerOptions())
				configs, _ = override.SensitiveProxyConfigs()
				if configs[0]["servername"] != "example.invalid" || override.Nodes[0].ID == base.Nodes[0].ID {
					t.Fatal("explicit SNI did not override Host")
				}
			}
			for _, host := range []string{"ws.example.invalid:443", "[::1]:443"} {
				a := profileNode(kind)
				a["network"] = "ws"
				a["tls"] = true
				a["ws-opts"] = map[string]any{"headers": map[string]any{"Host": host}}
				requireProviderError(t, providerRaw(t, a), providerOptions(), ErrInvalidProvider)
				a["servername"] = "example.invalid"
				mustProvider(t, providerRaw(t, a), providerOptions())
			}
			a := profileNode(kind)
			a["network"] = "ws"
			a["tls"] = true
			base := mustProvider(t, providerRaw(t, a), providerOptions())
			configs, _ := base.SensitiveProxyConfigs()
			if configs[0]["servername"] != "example.invalid" {
				t.Fatal("server fallback without WS Host changed")
			}
		})
	}
	a := profileNode("trojan")
	a["network"] = "ws"
	a["ws-opts"] = map[string]any{"headers": map[string]any{"Host": "ws.example.invalid:443"}}
	s := mustProvider(t, providerRaw(t, a), providerOptions())
	configs, _ := s.SensitiveProxyConfigs()
	if configs[0]["sni"] != "example.invalid" {
		t.Fatal("Trojan SNI fallback incorrectly followed WS Host")
	}
}
func TestParseWebsocketPathSemantics(t *testing.T) {
	for _, kind := range []string{"trojan", "vmess", "vless"} {
		t.Run(kind, func(t *testing.T) {
			for _, path := range []string{"/socket?ed=1", "/socket?%65d=1", "/socket?e%64=1", "/socket?ed=1&ed=2", "/socket?ed", "/socket?x=y&%65%64=0"} {
				m := profileNode(kind)
				m["network"] = "ws"
				m["ws-opts"] = map[string]any{"path": path}
				requireProviderError(t, providerRaw(t, m), providerOptions(), ErrUnsupportedNode)
			}
			for _, path := range []string{"//other.invalid/path", "https://other.invalid/path", "/bad%", "/socket?x=%zz", "/socket?%zz=x", "/socket?x=bad;query", "/socket%0d%0aInjected", "/socket?x=%0d%0aInjected", "/socket?%0a=value", "/socket%C2%85", "/socket#fragment"} {
				m := profileNode(kind)
				m["network"] = "ws"
				m["ws-opts"] = map[string]any{"path": path}
				requireProviderError(t, providerRaw(t, m), providerOptions(), ErrInvalidProvider)
			}
			for _, path := range []string{"/socket?z=2&a=1&a=3", "/s%2Focket?x=%2B+y", "/socket?ED=1", "/socket?%2565d=1", "/socket?"} {
				m := profileNode(kind)
				m["network"] = "ws"
				m["ws-opts"] = map[string]any{"path": path}
				s := mustProvider(t, providerRaw(t, m), providerOptions())
				configs, _ := s.SensitiveProxyConfigs()
				if configs[0]["ws-opts"].(map[string]any)["path"] != path {
					t.Fatal("legal WS path/query bytes were rewritten")
				}
			}
		})
	}
}
