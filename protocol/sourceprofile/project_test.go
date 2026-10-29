package sourceprofile_test

import (
	"reflect"
	"strings"
	"testing"

	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

const goldenNode = "node:n-c199b2d300eaa7d6fb531a5cab4cd2233df8f5663cabadc397074147fb102245"
const goldenConfigSHA = "43bb88988c9aabfc199deea48cf2f942122ab24c327d43a3659653bff2ebee06"
const goldenAuthSHA = "7284be74a997418d686164ffd86abab2f34408b44b63c39d671aba54b53f0831"
const goldenSnapshotSHA = "bdd51f120a8a3c0c91cd793c362aaa5a6530f048d544263d28f50195e1e03dbd"

func expectedGolden() wire.Snapshot {
	leaf := func(name, kind, address string) wire.Proxy {
		return wire.Proxy{Name: name, Type: kind, Address: address, Members: []string{}}
	}
	return wire.Snapshot{SchemaVersion: 2, ConfigSHA256: goldenConfigSHA, Controller: "127.0.0.1:19000", Entries: []wire.Entry{{Name: "entry-a", Type: "mixed", Address: "127.0.0.1:19001", AuthSHA256: goldenAuthSHA}}, Proxies: []wire.Proxy{
		leaf("COMPATIBLE", "Compatible", ""), leaf("DIRECT", "Direct", ""),
		{Name: "GLOBAL", Type: "Selector", Members: []string{"DIRECT", "REJECT", goldenNode, "node:direct", "group:auto", "group:follow", "group:reject"}, Selected: "DIRECT"},
		leaf("PASS", "Pass", ""), leaf("REJECT", "Reject", ""), leaf("REJECT-DROP", "RejectDrop", ""),
		{Name: "group:auto", Type: "URLTest", Members: []string{goldenNode}, Probe: &wire.Probe{URL: "https://probe.example.test/check", IntervalSeconds: 60, TimeoutMS: 1000, ToleranceMS: 0}},
		{Name: "group:follow", Type: "Selector", Members: []string{"group:auto"}, Selected: "group:auto"},
		{Name: "group:reject", Type: "Selector", Members: []string{"REJECT"}, Selected: "REJECT"}, leaf("node:direct", "Direct", ""), leaf(goldenNode, "Socks5", "edge.example.test:443"),
	}, Rules: []wire.Rule{{Type: "Domain", Payload: "example.test", Target: "group:follow"}, {Type: "DomainSuffix", Payload: "example.test", Target: "node:direct"}, {Type: "IPCIDR", Payload: "192.0.2.129/24", Target: "node:direct"}, {Type: "InName", Payload: "entry-a", Target: "group:follow"}, {Type: "AND", Payload: "((Domain,example.test) && (IPCIDR,192.0.2.129/24) && (InName,entry-a))", Target: "group:follow"}, {Type: "Match", Target: "group:follow"}}}
}
func projectedProxy(s wire.Snapshot, name string) wire.Proxy {
	for _, p := range s.Proxies {
		if p.Name == name {
			return p
		}
	}
	panic("fixture proxy")
}
func TestIndependentGoldenProjectionAndCredentialBinding(t *testing.T) {
	raw := encode(t, fixture(t))
	if sha(raw) != goldenConfigSHA {
		t.Fatal("independent generated config golden bytes changed")
	}
	actual, err := profile.Project(raw, generation)
	if err != nil {
		t.Fatal(err)
	}
	expected := expectedGolden()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("independent expected Snapshot differs from Project")
	}
	// Constants were computed with independent Python stdlib ordered compact JSON,
	// SHA256 and HMAC, rather than by Project or Snapshot.Digest.
	hash, err := actual.Digest()
	if err != nil || hash != goldenSnapshotSHA {
		t.Fatal("independent projected snapshot SHA changed", err)
	}
	for _, which := range []string{"controller-secret", "entry-password", "provider-authentication"} {
		m := fixture(t)
		switch which {
		case "controller-secret":
			m["secret"] = strings.Repeat("q", 32)
		case "entry-password":
			obj(firstEntry(m)["users"].([]any)[0])["password"] = strings.Repeat("r", 32)
		case "provider-authentication":
			firstNode(m)["username"], firstNode(m)["password"] = "another-user", "another-provider-password"
		}
		changed, err := profile.Project(encode(t, m), generation)
		if err != nil {
			t.Fatal("valid credential change rejected", err)
		}
		digest, err := changed.Digest()
		if err != nil || changed.ConfigSHA256 == actual.ConfigSHA256 || digest == hash {
			t.Fatal("credential material not bound by full config/snapshot digest")
		}
		if (changed.Entries[0].AuthSHA256 != actual.Entries[0].AuthSHA256) != (which == "entry-password") {
			t.Fatal("entry authentication hash binds wrong credential scope")
		}
		if which == "provider-authentication" && !reflect.DeepEqual(changed.Proxies, actual.Proxies) {
			t.Fatal("provider credentials exposed via public proxy projection")
		}
	}
}
func TestProjectDeclarationOrderParentAndRuleSemantics(t *testing.T) {
	m := fixture(t)
	raw := encode(t, m)
	base, err := profile.Project(raw, generation)
	if err != nil {
		t.Fatal(err)
	}
	// Source declaration order, not sorted outer order, determines GLOBAL members.
	nodes := m["proxies"].([]any)
	nodes[0], nodes[1] = nodes[1], nodes[0]
	groups := m["proxy-groups"].([]any)
	groups[0], groups[2] = groups[2], groups[0]
	reordered, err := profile.Project(encode(t, m), generation)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"DIRECT", "REJECT", "node:direct", goldenNode, "group:reject", "group:follow", "group:auto"}
	if !reflect.DeepEqual(projectedProxy(reordered, "GLOBAL").Members, want) {
		t.Fatal("GLOBAL order sorted or detached from declaration order")
	}
	if projectedProxy(reordered, "group:follow").Selected != "group:auto" || projectedProxy(reordered, "group:auto").Selected != "" {
		t.Fatal("follow parent replaced by dynamic leaf or static URLTest selection")
	}
	for i := 1; i < len(reordered.Proxies); i++ {
		if reordered.Proxies[i-1].Name >= reordered.Proxies[i].Name {
			t.Fatal("outer projection not sorted")
		}
	}
	if !reflect.DeepEqual(base.Rules, reordered.Rules) {
		t.Fatal("group ordering rewrote rule projection")
	}
	// Native projection cannot recover no-resolve, while full configSHA still
	// binds that option. Prefix payload preserves host bits, not Masked().
	m = fixture(t)
	rules := m["rules"].([]any)
	rules[2] = "IP-CIDR,192.0.2.129/24,node:direct"
	rules[4] = "AND,((DOMAIN,EXAMPLE.TEST),(IP-CIDR,192.0.2.129/24),(IN-NAME,entry-a)),group:follow"
	noOption, err := profile.Project(encode(t, m), generation)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.Rules, noOption.Rules) || base.ConfigSHA256 == noOption.ConfigSHA256 {
		t.Fatal("no-resolve projection/binding scope incorrect")
	}
}
func TestProjectAllRemoteAddrMappings(t *testing.T) {
	kinds := map[string]string{"http": "Http", "socks5": "Socks5", "ss": "Shadowsocks", "trojan": "Trojan", "vmess": "Vmess", "vless": "Vless", "hysteria2": "Hysteria2"}
	for kind, wireKind := range kinds {
		for _, endpoint := range []struct{ server, address string }{{"edge.example.test", "edge.example.test:443"}, {"192.0.2.1", "192.0.2.1:443"}, {"2001:db8::1", "[2001:db8::1]:443"}} {
			n := inputNode(kind)
			n["server"] = endpoint.server
			nodes := canonical(t, n)
			m := fixture(t)
			m["proxies"] = []any{nodes[0], map[string]any{"name": "node:direct", "type": "direct"}}
			firstGroup(m)["proxies"] = []any{nodes[0]["name"]}
			s, err := profile.Project(encode(t, m), generation)
			if err != nil {
				t.Fatal(err)
			}
			remote := projectedProxy(s, nodes[0]["name"].(string))
			if remote.Type != wireKind || remote.Address != endpoint.address || remote.DialerProxy != "" || len(remote.Members) != 0 || remote.Selected != "" || remote.Probe != nil {
				t.Fatal("remote leaf mapping differs from fixed constructor projection")
			}
		}
	}
}
func TestValidateReturnsIndependentMutableConfig(t *testing.T) {
	m := fixture(t)
	m["hosts"] = map[string]any{"loop.example.test": "127.0.0.1"}
	raw := encode(t, m)
	baseline, err := profile.Validate(raw, generation)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := profile.Project(raw, generation)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := profile.Validate(raw, generation)
	if err != nil {
		t.Fatal(err)
	}
	changed.Secret = "mutated"
	changed.Hosts["loop.example.test"] = "192.0.2.1"
	changed.Listeners[0].Users[0].Password = "mutated"
	changed.Proxies[0]["password"] = "mutated"
	changed.Groups[0].Proxies[0] = "mutated"
	*changed.Groups[0].Tolerance = 65535
	*changed.Groups[0].Lazy = true
	changed.Rules[0] = "mutated"
	fresh, err := profile.Validate(raw, generation)
	if err != nil || !reflect.DeepEqual(fresh, baseline) {
		t.Fatal("returned Config mutation changed later Validate")
	}
	next, err := profile.Project(raw, generation)
	if err != nil || !reflect.DeepEqual(next, snapshot) {
		t.Fatal("returned Config mutation changed later Project")
	}
}
