package sourceprofile_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/subscription"
	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

const generation = "gen:fixture"

func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal("fixture encoding", err)
	}
	return raw
}
func canonical(t *testing.T, nodes ...map[string]any) []map[string]any {
	t.Helper()
	s, err := subscription.Parse(encode(t, map[string]any{"proxies": nodes}), subscription.ParseOptions{SourceID: "source-a", IdentityKey: bytes.Repeat([]byte{0x71}, 32)})
	if err != nil {
		t.Fatal("provider fixture rejected", err)
	}
	configs, err := s.SensitiveProxyConfigs()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range configs {
		n["name"] = "node:" + n["name"].(string)
	}
	return configs
}
func inputNode(kind string) map[string]any {
	m := map[string]any{"name": "ordinary", "type": kind, "server": "edge.example.test", "port": 443}
	switch kind {
	case "ss":
		m["cipher"] = "aes-128-gcm"
		m["password"] = "provider-private-password"
	case "trojan", "hysteria2":
		m["password"] = "provider-private-password"
	case "vmess":
		m["uuid"] = "01234567-89ab-cdef-0123-456789abcdef"
		m["cipher"] = "auto"
		m["alterId"] = 0
	case "vless":
		m["uuid"] = "01234567-89ab-cdef-0123-456789abcdef"
	}
	return m
}
func fixture(t *testing.T) map[string]any {
	t.Helper()
	nodes := canonical(t, inputNode("socks5"))
	leaf := nodes[0]["name"].(string)
	return map[string]any{
		"mode": "rule", "log-level": "silent", "ipv6": false, "find-process-mode": "off", "external-controller": "127.0.0.1:19000", "secret": strings.Repeat("s", 32),
		"listeners":    []any{map[string]any{"name": "entry-a", "type": "mixed", "listen": "127.0.0.1", "port": 19001, "udp": false, "users": []any{map[string]any{"username": "user-a", "password": strings.Repeat("p", 32)}}}},
		"proxies":      []any{nodes[0], map[string]any{"name": "node:direct", "type": "direct"}},
		"proxy-groups": []any{map[string]any{"name": "group:auto", "type": "url-test", "proxies": []any{leaf}, "url": "https://probe.example.test/check", "interval": 60, "timeout": 1000, "tolerance": 0, "lazy": false, "expected-status": "200-299"}, map[string]any{"name": "group:follow", "type": "select", "proxies": []any{"group:auto"}}, map[string]any{"name": "group:reject", "type": "select", "proxies": []any{"REJECT"}}},
		"rules":        []any{"DOMAIN,EXAMPLE.TEST,group:follow", "DOMAIN-SUFFIX,EXAMPLE.TEST,node:direct", "IP-CIDR,192.0.2.129/24,node:direct,no-resolve", "IN-NAME,entry-a,group:follow", "AND,((DOMAIN,EXAMPLE.TEST),(IP-CIDR,192.0.2.129/24,no-resolve),(IN-NAME,entry-a)),group:follow", "MATCH,group:follow"},
		"profile":      map[string]any{"store-selected": false, "store-fake-ip": false},
	}
}
func obj(v any) map[string]any                   { return v.(map[string]any) }
func firstNode(m map[string]any) map[string]any  { return obj(m["proxies"].([]any)[0]) }
func firstGroup(m map[string]any) map[string]any { return obj(m["proxy-groups"].([]any)[0]) }
func firstEntry(m map[string]any) map[string]any { return obj(m["listeners"].([]any)[0]) }
func reject(t *testing.T, raw []byte) {
	t.Helper()
	c, err := profile.Validate(raw, generation)
	if !errors.Is(err, profile.ErrProfile) || err.Error() != profile.ErrProfile.Error() || !reflect.DeepEqual(c, profile.Config{}) {
		t.Fatal("invalid profile accepted or partial Config returned", err)
	}
	s, err := profile.Project(raw, generation)
	if !errors.Is(err, profile.ErrProfile) || err.Error() != profile.ErrProfile.Error() || !reflect.DeepEqual(s, wire.Snapshot{}) {
		t.Fatal("invalid profile projected or partial Snapshot returned", err)
	}
}
func TestCanonicalSubscriptionProfilesAccepted(t *testing.T) {
	for _, kind := range []string{"http", "socks5", "ss", "trojan", "vmess", "vless", "hysteria2"} {
		t.Run(kind, func(t *testing.T) {
			cases := []map[string]any{inputNode(kind)}
			if kind == "socks5" || kind == "http" {
				n := inputNode(kind)
				n["username"], n["password"] = "provider-user", "provider-password"
				n["tls"] = true
				cases = append(cases, n)
			}
			if kind == "trojan" || kind == "vmess" || kind == "vless" {
				n := inputNode(kind)
				n["network"] = "ws"
				if kind != "trojan" {
					n["tls"] = true
				}
				n["ws-opts"] = map[string]any{"path": "/socket?z=2&a=1&a=3", "headers": map[string]any{"Host": "ws.example.test"}}
				cases = append(cases, n)
			}
			if kind == "hysteria2" {
				n := inputNode(kind)
				n["alpn"] = []any{"h3"}
				cases = append(cases, n)
			}
			if kind == "ss" {
				for _, cipher := range []string{"aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305"} {
					n := inputNode(kind)
					n["cipher"] = cipher
					cases = append(cases, n)
				}
			}
			if kind == "vmess" {
				for _, cipher := range []string{"aes-128-gcm", "chacha20-poly1305", "none"} {
					n := inputNode(kind)
					n["cipher"] = cipher
					cases = append(cases, n)
				}
			}
			for _, n := range cases {
				for _, host := range []string{"edge.example.test", "192.0.2.1", "2001:db8::1"} {
					n["server"] = host
					nodes := canonical(t, n)
					m := fixture(t)
					m["proxies"] = []any{nodes[0], map[string]any{"name": "node:direct", "type": "direct"}}
					firstGroup(m)["proxies"] = []any{nodes[0]["name"]}
					if _, err := profile.Validate(encode(t, m), generation); err != nil {
						t.Fatal("canonical provider config rejected", err)
					}
					s, err := profile.Project(encode(t, m), generation)
					if err != nil || s.Validate() != nil {
						t.Fatal("canonical profile projection rejected", err)
					}
				}
			}
		})
	}
}
func TestRejectProfileStructureAndPolicy(t *testing.T) {
	cases := map[string]func(map[string]any){
		"ipv6": func(m map[string]any) { m["ipv6"] = true }, "mode": func(m map[string]any) { m["mode"] = "global" }, "logging": func(m map[string]any) { m["log-level"] = "debug" }, "process-discovery": func(m map[string]any) { m["find-process-mode"] = "always" }, "cache-selected": func(m map[string]any) { obj(m["profile"])["store-selected"] = true }, "cache-fakeip": func(m map[string]any) { obj(m["profile"])["store-fake-ip"] = true },
		"controller-remote": func(m map[string]any) { m["external-controller"] = "192.0.2.1:19000" }, "controller-zero": func(m map[string]any) { m["external-controller"] = "127.0.0.1:0" }, "controller-noncanonical": func(m map[string]any) { m["external-controller"] = "127.0.0.1:019000" },
		"listener-remote": func(m map[string]any) { firstEntry(m)["listen"] = "0.0.0.0" }, "listener-type": func(m map[string]any) { firstEntry(m)["type"] = "http" }, "listener-udp": func(m map[string]any) { firstEntry(m)["udp"] = true }, "listener-zero": func(m map[string]any) { firstEntry(m)["port"] = 0 }, "listener-overflow": func(m map[string]any) { firstEntry(m)["port"] = 65536 }, "listener-fraction": func(m map[string]any) { firstEntry(m)["port"] = 19001.5 }, "endpoint-collision": func(m map[string]any) { firstEntry(m)["port"] = 19000 }, "listener-no-auth": func(m map[string]any) { firstEntry(m)["users"] = []any{} },
		"bad-secret": func(m map[string]any) { m["secret"] = "too-short" }, "shared-secret-password": func(m map[string]any) { obj(firstEntry(m)["users"].([]any)[0])["password"] = m["secret"] }, "bad-username": func(m map[string]any) { obj(firstEntry(m)["users"].([]any)[0])["username"] = "user:bad" }, "bad-password": func(m map[string]any) { obj(firstEntry(m)["users"].([]any)[0])["password"] = strings.Repeat("p", 31) },
		"host-external": func(m map[string]any) { m["hosts"] = map[string]any{"edge.example.test": "192.0.2.1"} }, "host-bad": func(m map[string]any) { m["hosts"] = map[string]any{"bad_host": "127.0.0.1"} },
		"noncanonical-server": func(m map[string]any) { firstNode(m)["server"] = "EDGE.EXAMPLE.TEST." }, "noncanonical-port": func(m map[string]any) { firstNode(m)["port"] = json.Number("443.0") }, "unsafe-port": func(m map[string]any) { firstNode(m)["port"] = json.Number("9007199254740992") }, "unsafe-node-id": func(m map[string]any) { firstNode(m)["name"] = "raw-provider-name" }, "direct-extra": func(m map[string]any) { obj(m["proxies"].([]any)[1])["server"] = "edge.example.test" },
		"literal-self-controller": func(m map[string]any) { firstNode(m)["server"] = "127.0.0.1"; firstNode(m)["port"] = 19000 }, "literal-self-listener": func(m map[string]any) { firstNode(m)["server"] = "127.0.0.1"; firstNode(m)["port"] = 19001 },
		"duplicate-node": func(m map[string]any) { m["proxies"] = append(m["proxies"].([]any), firstNode(m)) }, "duplicate-group": func(m map[string]any) { m["proxy-groups"] = append(m["proxy-groups"].([]any), firstGroup(m)) },
		"auto-direct": func(m map[string]any) { firstGroup(m)["proxies"] = []any{"node:direct"} }, "auto-builtin": func(m map[string]any) { firstGroup(m)["proxies"] = []any{"DIRECT"} }, "auto-group": func(m map[string]any) { firstGroup(m)["proxies"] = []any{"group:follow"} }, "auto-empty": func(m map[string]any) { firstGroup(m)["proxies"] = []any{} }, "auto-duplicate": func(m map[string]any) {
			g := firstGroup(m)
			g["proxies"] = append(g["proxies"].([]any), g["proxies"].([]any)[0])
		},
		"lazy": func(m map[string]any) { firstGroup(m)["lazy"] = true }, "status": func(m map[string]any) { firstGroup(m)["expected-status"] = "*" }, "interval-zero": func(m map[string]any) { firstGroup(m)["interval"] = 0 }, "interval-over": func(m map[string]any) { firstGroup(m)["interval"] = 86401 }, "timeout-over": func(m map[string]any) { firstGroup(m)["timeout"] = 60001 }, "timeout-zero": func(m map[string]any) { firstGroup(m)["timeout"] = 0 }, "tolerance-over": func(m map[string]any) { firstGroup(m)["tolerance"] = 65535 }, "probe-authority": func(m map[string]any) { firstGroup(m)["url"] = "https://user:secret@probe.example.test/" }, "probe-float": func(m map[string]any) { firstGroup(m)["interval"] = json.Number("60.0") },
		"selector-multiple": func(m map[string]any) {
			obj(m["proxy-groups"].([]any)[1])["proxies"] = []any{"group:auto", "node:direct"}
		}, "selector-probe": func(m map[string]any) { obj(m["proxy-groups"].([]any)[1])["url"] = "https://probe.example.test/" }, "group-cycle": func(m map[string]any) { obj(m["proxy-groups"].([]any)[1])["proxies"] = []any{"group:follow"} }, "dangling": func(m map[string]any) { obj(m["proxy-groups"].([]any)[1])["proxies"] = []any{"node:missing"} },
		"missing-match": func(m map[string]any) { m["rules"] = []any{"DOMAIN,example.test,REJECT"} }, "early-match": func(m map[string]any) { m["rules"] = []any{"MATCH,REJECT", "MATCH,REJECT"} }, "builtin-target": func(m map[string]any) { m["rules"] = []any{"MATCH,DIRECT"} }, "process-rule": func(m map[string]any) { m["rules"] = []any{"PROCESS-NAME,app,REJECT", "MATCH,REJECT"} }, "unknown-entry-rule": func(m map[string]any) { m["rules"] = []any{"IN-NAME,missing,REJECT", "MATCH,REJECT"} }, "bad-and": func(m map[string]any) { m["rules"] = []any{"AND,((DOMAIN,example.test)),REJECT", "MATCH,REJECT"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { m := fixture(t); mutate(m); reject(t, encode(t, m)) })
	}
	for _, field := range []string{"dns", "tun", "port", "mixed-port", "external-controller-unix", "external-ui", "proxy-providers", "rule-providers", "authentication", "allow-lan", "interface-name", "routing-mark", "geodata-mode", "sniffer"} {
		m := fixture(t)
		m[field] = false
		reject(t, encode(t, m))
	}
	for _, field := range []string{"dialer-proxy", "interface-name", "routing-mark", "tfo", "certificate", "private-key", "region", "fingerprint"} {
		m := fixture(t)
		firstNode(m)[field] = "foreign-canary"
		reject(t, encode(t, m))
	}
}
func TestProfileGraphDepthAndSameEndpointCredentials(t *testing.T) {
	for _, depth := range []int{7, 8} {
		m := fixture(t)
		groups := []any{}
		parent := "node:direct"
		for i := 0; i < depth; i++ {
			name := fmt.Sprintf("group:depth%d", i)
			groups = append(groups, map[string]any{"name": name, "type": "select", "proxies": []any{parent}})
			parent = name
		}
		m["proxy-groups"] = groups
		m["rules"] = []any{"MATCH," + parent}
		_, err := profile.Validate(encode(t, m), generation)
		if (err == nil) != (depth == 7) {
			t.Fatalf("leaf+group depth %d: %v", depth+1, err)
		}
	}
	a, b := inputNode("socks5"), inputNode("socks5")
	a["username"], a["password"] = "first-user", "first-password"
	b["username"], b["password"] = "second-user", "second-password"
	nodes := canonical(t, a, b)
	m := fixture(t)
	m["proxies"] = []any{nodes[0], nodes[1], map[string]any{"name": "node:direct", "type": "direct"}}
	firstGroup(m)["proxies"] = []any{nodes[0]["name"], nodes[1]["name"]}
	if _, err := profile.Validate(encode(t, m), generation); err != nil {
		t.Fatal("distinct credentials at same remote endpoint rejected", err)
	}
}
func TestProfileSelfEndpointThroughDeclaredHosts(t *testing.T) {
	for _, port := range []int{19000, 19001} {
		for _, host := range []string{"edge.example.test", "EdGe.ExAmPlE.TeSt"} {
			m := fixture(t)
			m["hosts"] = map[string]any{host: "127.0.0.1"}
			firstNode(m)["port"] = port
			reject(t, encode(t, m))
		}
	}
}
func TestProfileWireAmbiguitiesUnicodeAndBounds(t *testing.T) {
	raw := encode(t, fixture(t))
	for _, changed := range [][]byte{nil, []byte("null"), append(append([]byte{}, raw...), []byte(" {}")...), bytes.Replace(raw, []byte(`"mode":"rule"`), []byte(`"mode":"rule","mode":"rule"`), 1), bytes.Replace(raw, []byte(`"mode":"rule"`), []byte(`"mode":"rule","m\u006fde":"rule"`), 1), bytes.Replace(raw, []byte(`"mode":"rule"`), []byte(`"Mode":"rule"`), 1), bytes.Replace(raw, []byte("EXAMPLE.TEST"), []byte(`\ud800`), 1), bytes.Replace(raw, []byte("EXAMPLE.TEST"), []byte(`\udfff`), 1), bytes.Replace(raw, []byte("EXAMPLE.TEST"), []byte{0xff}, 1), append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), wire.MaxJSONBytes)...)} {
		reject(t, changed)
	}
	for _, groupField := range []string{"interval", "timeout", "tolerance"} {
		for _, bad := range []any{nil, true, "1", json.Number("1.0"), json.Number("1e0"), json.Number("-0"), json.Number("9007199254740992")} {
			m := fixture(t)
			firstGroup(m)[groupField] = bad
			reject(t, encode(t, m))
		}
	}
	for _, badGen := range []string{"", "bad generation", strings.Repeat("g", 129)} {
		if c, err := profile.Validate(raw, badGen); err != profile.ErrProfile || !reflect.DeepEqual(c, profile.Config{}) {
			t.Fatal("bad generation accepted")
		}
		if s, err := profile.Project(raw, badGen); err != profile.ErrProfile || !reflect.DeepEqual(s, wire.Snapshot{}) {
			t.Fatal("bad generation projected")
		}
	}
	// A valid surrogate pair and explicit replacement scalar remain legal UTF8
	// inside credential-bearing provider fields; no raw invalid text is repaired.
	m := fixture(t)
	firstNode(m)["username"], firstNode(m)["password"] = "unicode-user", "unicode-😀-password"
	valid := encode(t, m)
	valid = bytes.Replace(valid, []byte("😀"), []byte(`\ud83d\ude00`), 1)
	if _, err := profile.Validate(valid, generation); err != nil {
		t.Fatal("valid surrogate pair rejected", err)
	}
}
func sha(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }

func TestProfileCanonicalTransportTampering(t *testing.T) {
	n := inputNode("vless")
	n["tls"] = true
	n["network"] = "ws"
	n["ws-opts"] = map[string]any{"path": "/socket?z=2&a=1", "headers": map[string]any{"Host": "ws.example.test"}}
	for _, mutate := range []func(map[string]any){
		func(n map[string]any) { n["uuid"] = strings.ToUpper(n["uuid"].(string)) },
		func(n map[string]any) { n["alpn"] = []any{"h2"} },
		func(n map[string]any) { n["skip-cert-verify"] = true },
		func(n map[string]any) { obj(n["ws-opts"])["path"] = "/socket?%65d=123" },
		func(n map[string]any) { obj(obj(n["ws-opts"])["headers"])["Authorization"] = "foreign-private-value" },
		func(n map[string]any) { n["servername"] = "WS.EXAMPLE.TEST" },
	} {
		nodes := canonical(t, n)
		m := fixture(t)
		m["proxies"] = []any{nodes[0], map[string]any{"name": "node:direct", "type": "direct"}}
		firstGroup(m)["proxies"] = []any{nodes[0]["name"]}
		mutate(nodes[0])
		reject(t, encode(t, m))
	}
	m := fixture(t)
	g := firstGroup(m)
	g["interval"], g["timeout"], g["tolerance"] = 86400, 60000, 65534
	if _, err := profile.Project(encode(t, m), generation); err != nil {
		t.Fatal("exact native Probe bounds rejected", err)
	}
}
