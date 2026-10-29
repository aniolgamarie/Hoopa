package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"hoopa/internal/config"
)

// Independently calculated with Python stdlib hmac/sha256 and sorted compact
// JSON plus Go's standard HTML escaping, not through the implementation helper.
func TestIdentityV1GoldenEncoding(t *testing.T) {
	const raw = `{"proxies":[{"name":"gold","type":"socks5","server":"Example.INVALID.","port":443,"username":"alice","password":"s3cr<et&"}]}`
	const canonical = `["hoopa.node.v1","source-a",{"password":"s3cr\u003cet\u0026","port":443,"server":"example.invalid","skip-cert-verify":false,"tls":false,"type":"socks5","udp":false,"username":"alice"}]`
	var input map[string]any
	if json.Unmarshal([]byte(raw), &input) != nil {
		t.Fatal("bad fixture")
	}
	// Use the provider parser's numeric representation for the canonical description.
	node := profileNode("socks5")
	node["server"] = "Example.INVALID."
	node["username"] = "alice"
	node["password"] = "s3cr<et&"
	var canonicalInput map[string]any
	decoder := json.NewDecoder(bytes.NewReader(providerRaw(t, node)))
	decoder.UseNumber()
	if decoder.Decode(&canonicalInput) != nil {
		t.Fatal("bad fixture")
	}
	_, connection, err := parseConnection(canonicalInput["proxies"].([]any)[0])
	if err != nil {
		t.Fatal("bad canonical fixture")
	}
	encoded, err := json.Marshal([]any{"hoopa.node.v1", config.ID("source-a"), connection})
	if err != nil || string(encoded) != canonical {
		t.Fatal("v1 canonical byte encoding changed")
	}
	snapshot := mustProvider(t, []byte(raw), providerOptions())
	if snapshot.Nodes[0].ID != "n-617d3548662d933b06ce3a367b3b7391c5f23bd0da2ea83a59c0fa2baf4ee9f5" {
		t.Fatal("node v1 golden digest changed")
	}
	if snapshot.KeyID != "6c4ace10a8977a9eb0c8cc250d073a1de69477078d38083b8fa74133dca76973" {
		t.Fatal("key v1 golden digest changed")
	}
	if snapshot.ID != "s-91bef72ae965ccc74071379ea935c24d56484a79db2dbdfe68741d4689c2c4bd" {
		t.Fatal("snapshot v1 golden digest changed")
	}
}
func TestIdentityEveryConnectionParameter(t *testing.T) {
	changes := []struct {
		kind, field string
		value       any
		tls, ws     bool
	}{
		{"socks5", "udp", true, false, false}, {"socks5", "tls", true, false, false}, {"http", "tls", true, false, false},
		{"http", "sni", "other.invalid", true, false}, {"ss", "cipher", "aes-256-gcm", false, false}, {"ss", "password", "different-password", false, false},
		{"trojan", "password", "different-password", false, false}, {"trojan", "sni", "other.invalid", false, false}, {"trojan", "alpn", []any{"h2"}, false, false},
		{"trojan", "alpn", []any{"http/1.1", "h2"}, false, false}, {"trojan", "network", "ws", false, false},
		{"vmess", "uuid", "11234567-89ab-cdef-0123-456789abcdef", false, false}, {"vmess", "cipher", "none", false, false}, {"vmess", "tls", true, false, false},
		{"vless", "servername", "other.invalid", true, false}, {"vless", "ws-opts", map[string]any{"path": "/different"}, false, true}, {"vless", "ws-opts", map[string]any{"headers": map[string]any{"Host": "different.invalid"}}, false, true},
		{"hysteria2", "password", "different-password", false, false}, {"hysteria2", "sni", "other.invalid", false, false}, {"hysteria2", "alpn", []any{"h3"}, false, false},
	}
	for i, change := range changes {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			base, candidate := profileNode(change.kind), profileNode(change.kind)
			if change.tls {
				base["tls"] = true
				candidate["tls"] = true
			}
			if change.ws {
				base["network"] = "ws"
				candidate["network"] = "ws"
			}
			if change.field == "alpn" && change.kind == "trojan" {
				base["alpn"] = []any{"h2", "http/1.1"}
			}
			candidate[change.field] = change.value
			first := mustProvider(t, providerRaw(t, base), providerOptions())
			second := mustProvider(t, providerRaw(t, candidate), providerOptions())
			if first.Nodes[0].ID == second.Nodes[0].ID {
				t.Fatal("connection setting changed without changing identity")
			}
		})
	}
	a, b := profileNode("vless"), profileNode("vless")
	b["uuid"] = strings.ToUpper(testUUID)
	if mustProvider(t, providerRaw(t, a), providerOptions()).Nodes[0].ID != mustProvider(t, providerRaw(t, b), providerOptions()).Nodes[0].ID {
		t.Fatal("UUID case changed canonical identity")
	}
}
func TestIdentitySnapshotBinding(t *testing.T) {
	raw := providerRaw(t, profileNode("socks5"))
	first := mustProvider(t, raw, providerOptions())
	same := mustProvider(t, raw, providerOptions())
	if first.ID != same.ID || first.Nodes[0].ID != same.Nodes[0].ID {
		t.Fatal("same input not stable")
	}
	spaced := mustProvider(t, append(append([]byte{}, raw...), ' '), providerOptions())
	if spaced.ID == first.ID || spaced.Nodes[0].ID != first.Nodes[0].ID {
		t.Fatal("snapshot raw-byte binding wrong")
	}
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}}
	declared := mustProvider(t, raw, options)
	if declared.ID == first.ID || declared.Nodes[0].ID != first.Nodes[0].ID {
		t.Fatal("region rules not bound independently of node identity")
	}
	options.RegionRules = []RegionRule{}
	empty := mustProvider(t, raw, options)
	if empty.ID != first.ID {
		t.Fatal("nil and empty region rules differ")
	}
}
func TestSnapshotRejectsMetadataTampering(t *testing.T) {
	raw := providerRaw(t, profileNode("socks5"))
	base := mustProvider(t, raw, providerOptions())
	mutations := []func(*Snapshot){
		func(s *Snapshot) { s.ID = "forged" }, func(s *Snapshot) { s.SourceID = "foreign" }, func(s *Snapshot) { s.KeyID = "forged" },
		func(s *Snapshot) { s.Nodes[0].ID = "forged" }, func(s *Snapshot) { s.Nodes[0].DisplayName = "forged" }, func(s *Snapshot) {
			s.Nodes[0].Region = RegionEvidence{Code: "JP", Kind: "declared-prefix", Rule: "fake"}
		},
		func(s *Snapshot) { s.Nodes = append(s.Nodes, s.Nodes[0]) }, func(s *Snapshot) { s.Nodes = nil },
	}
	for i, mutate := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s := base
			s.Nodes = append([]Node{}, base.Nodes...)
			mutate(&s)
			if !errors.Is(s.Validate(), ErrInvalidProvider) {
				t.Fatal("altered snapshot validated")
			}
			if out, err := s.SensitiveProxyConfigs(); out != nil || !errors.Is(err, ErrInvalidProvider) {
				t.Fatal("altered snapshot exposed compiler configs")
			}
			if out, err := s.SensitiveBytes(); out != nil || !errors.Is(err, ErrInvalidProvider) {
				t.Fatal("altered snapshot exposed bytes")
			}
			if _, err := ResolvePin(map[config.ID]Snapshot{base.SourceID: s}, NodeRef{SubscriptionID: base.SourceID, NodeID: base.Nodes[0].ID}); !errors.Is(err, ErrPinUnresolved) {
				t.Fatal("altered snapshot resolved")
			}
		})
	}
	for _, s := range []Snapshot{{}, {ID: "synthetic", SourceID: "source-a", KeyID: "synthetic", Nodes: append([]Node{}, base.Nodes...)}} {
		if s.Validate() != ErrInvalidProvider {
			t.Fatal("synthetic snapshot trusted")
		}
	}
	if base.Validate() != nil {
		t.Fatal("tamper attempt changed prior authority")
	}
}
func TestSnapshotSensitiveCopiesAndCallerOwnership(t *testing.T) {
	node := profileNode("trojan")
	node["network"] = "ws"
	node["ws-opts"] = map[string]any{"path": "/initial", "headers": map[string]any{"Host": "ws.example.invalid"}}
	raw := providerRaw(t, node)
	original := append([]byte{}, raw...)
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}}
	s := mustProvider(t, raw, options)
	firstID := s.Nodes[0].ID
	raw[0] = '!'
	for i := range options.IdentityKey {
		options.IdentityKey[i] = 0
	}
	options.RegionRules[0] = RegionRule{Prefix: "other", Region: "JP"}
	originalCopy, err := s.SensitiveBytes()
	if err != nil || !bytes.Equal(originalCopy, original) {
		t.Fatal("caller mutation changed snapshot bytes")
	}
	originalCopy[0] = '!'
	fresh, _ := s.SensitiveBytes()
	if !bytes.Equal(fresh, original) {
		t.Fatal("sensitive bytes share ownership")
	}
	configs, err := s.SensitiveProxyConfigs()
	if err != nil {
		t.Fatal(err)
	}
	if configs[0]["name"] != string(firstID) {
		t.Fatal("provider display name used for route")
	}
	configs[0]["password"] = "forged"
	opts := configs[0]["ws-opts"].(map[string]any)
	opts["path"] = "/forged"
	opts["headers"].(map[string]any)["Host"] = "forged.invalid"
	clone, _ := s.SensitiveProxyConfigs()
	if clone[0]["password"] != "private-test-password" || clone[0]["ws-opts"].(map[string]any)["path"] != "/initial" || clone[0]["ws-opts"].(map[string]any)["headers"].(map[string]any)["Host"] != "ws.example.invalid" {
		t.Fatal("compiler clones share mutable ownership")
	}
	if s.Validate() != nil || s.Nodes[0].ID != firstID {
		t.Fatal("caller mutation changed authority")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			configs, err := s.SensitiveProxyConfigs()
			if err != nil {
				t.Error("concurrent clone failed")
				return
			}
			configs[0]["password"] = "local-mutation"
		}()
	}
	wg.Wait()
}
func TestSnapshotCredentialFormatsStayPrivate(t *testing.T) {
	for _, kind := range profileKinds {
		node := profileNode(kind)
		raw := providerRaw(t, node)
		s := mustProvider(t, raw, providerOptions())
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		for _, out := range []string{string(encoded), fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s), fmt.Sprintf("%v", s.Nodes), fmt.Sprintf("%#v", s.Nodes)} {
			for _, secret := range []string{"private-test-password", testUUID, string(raw)} {
				if strings.Contains(out, secret) {
					t.Fatal("public metadata or formatting exposed connection material")
				}
			}
		}
	}
}
