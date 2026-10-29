package capabilityprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const validProfile = `{"mode":"rule","log-level":"silent","ipv6":false,"find-process-mode":"off","external-controller":"127.0.0.1:19090","secret":"ssssssssssssssssssssssssssssssss","listeners":[{"name":"entry-a","type":"mixed","listen":"127.0.0.1","port":18080,"udp":false,"users":[{"username":"user-a","password":"pppppppppppppppppppppppppppppppp"}]}],"hosts":{"alpha.test":"127.0.0.1"},"proxies":[{"name":"node:direct","type":"direct"},{"name":"node:http","type":"http","server":"127.0.0.1","port":18081}],"proxy-groups":[{"name":"group:main","type":"select","proxies":["node:direct","node:http"]}],"rules":["DOMAIN,alpha.test,group:main","MATCH,node:direct"],"profile":{"store-selected":false,"store-fake-ip":false}}`

func profileMap(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(validProfile), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func encode(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestRejectProfiles(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown root", func(v map[string]any) { v["tun"] = map[string]any{"enable": true} }},
		{"case variant", func(v map[string]any) { v["Mode"] = v["mode"]; delete(v, "mode") }},
		{"missing required", func(v map[string]any) { delete(v, "ipv6") }},
		{"null optional", func(v map[string]any) { v["hosts"] = nil }},
		{"null profile", func(v map[string]any) { v["profile"] = nil }},
		{"wrong bool", func(v map[string]any) { v["ipv6"] = "false" }},
		{"ipv6", func(v map[string]any) { v["ipv6"] = true }},
		{"mode", func(v map[string]any) { v["mode"] = "global" }},
		{"logs", func(v map[string]any) { v["log-level"] = "debug" }},
		{"process", func(v map[string]any) { v["find-process-mode"] = "always" }},
		{"wildcard controller", func(v map[string]any) { v["external-controller"] = "0.0.0.0:19090" }},
		{"other loopback", func(v map[string]any) { v["external-controller"] = "127.0.0.2:19090" }},
		{"controller zero", func(v map[string]any) { v["external-controller"] = "127.0.0.1:0" }},
		{"short secret", func(v map[string]any) { v["secret"] = "sensitive-short" }},
		{"colon secret", func(v map[string]any) { v["secret"] = strings.Repeat("x", 32) + ":" }},
		{"unicode secret", func(v map[string]any) { v["secret"] = strings.Repeat("x", 32) + "界" }},
		{"empty listeners", func(v map[string]any) { v["listeners"] = []any{} }},
		{"unknown listener", func(v map[string]any) { listener(v)["proxy"] = "DIRECT" }},
		{"duplicate endpoint", func(v map[string]any) { listener(v)["port"] = 19090 }},
		{"negative port", func(v map[string]any) { listener(v)["port"] = -1 }},
		{"large port", func(v map[string]any) { listener(v)["port"] = 65536 }},
		{"fractional port", func(v map[string]any) { listener(v)["port"] = 80.5 }},
		{"udp", func(v map[string]any) { listener(v)["udp"] = true }},
		{"no auth", func(v map[string]any) { delete(listener(v), "users") }},
		{"empty auth", func(v map[string]any) { listener(v)["users"] = []any{} }},
		{"unsafe user", func(v map[string]any) { listener(v)["users"].([]any)[0].(map[string]any)["username"] = "u:bad" }},
		{"controller password reuse", func(v map[string]any) { listener(v)["users"].([]any)[0].(map[string]any)["password"] = v["secret"] }},
		{"short password", func(v map[string]any) { listener(v)["users"].([]any)[0].(map[string]any)["password"] = "sensitive" }},
		{"host escape", func(v map[string]any) { v["hosts"] = map[string]string{"alpha.test": "192.0.2.1"} }},
		{"invalid host", func(v map[string]any) { v["hosts"] = map[string]string{"bad host": "127.0.0.1"} }},
		{"direct endpoint", func(v map[string]any) { v["proxies"].([]any)[0].(map[string]any)["port"] = 0 }},
		{"long node ID", func(v map[string]any) {
			v["proxies"].([]any)[0].(map[string]any)["name"] = "node:" + strings.Repeat("x", 128)
		}},
		{"unsupported proxy", func(v map[string]any) { v["proxies"].([]any)[1].(map[string]any)["type"] = "ss" }},
		{"proxy hostname", func(v map[string]any) { v["proxies"].([]any)[1].(map[string]any)["server"] = "localhost" }},
		{"unknown proxy", func(v map[string]any) { v["proxies"].([]any)[1].(map[string]any)["udp"] = true }},
		{"duplicate node", func(v map[string]any) { v["proxies"].([]any)[1].(map[string]any)["name"] = "node:direct" }},
		{"missing ref", func(v map[string]any) {
			v["proxy-groups"].([]any)[0].(map[string]any)["proxies"] = []string{"node:absent"}
		}},
		{"duplicate member", func(v map[string]any) {
			v["proxy-groups"].([]any)[0].(map[string]any)["proxies"] = []string{"node:direct", "node:direct"}
		}},
		{"cycle", func(v map[string]any) { v["proxies"].([]any)[1].(map[string]any)["dialer-proxy"] = "group:main" }},
		{"profile cache", func(v map[string]any) { v["profile"].(map[string]any)["store-selected"] = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { v := profileMap(t); tt.edit(v); assertRejected(t, encode(t, v), "generation-1") })
	}
	for _, raw := range []string{strings.Replace(validProfile, `"mode":"rule"`, `"mode":"rule","mode":"rule"`, 1), strings.Replace(validProfile, `"username":"user-a"`, `"username":"user-a","username":"user-a"`, 1), validProfile + ` {}`, `[]`, `null`, strings.Repeat(" ", 65537) + validProfile} {
		assertRejected(t, []byte(raw), "generation-1")
	}
	for _, generation := range []string{"", "a b", strings.Repeat("x", 129)} {
		assertRejected(t, []byte(validProfile), generation)
	}
}
func listener(v map[string]any) map[string]any { return v["listeners"].([]any)[0].(map[string]any) }
func assertRejected(t *testing.T, raw []byte, generation string) {
	t.Helper()
	got, err := Validate(raw, generation)
	if !errors.Is(err, ErrProfile) || err != ErrProfile {
		t.Fatalf("want fixed ErrProfile, got %v", err)
	}
	if got.Mode != "" {
		t.Fatal("returned partial config")
	}
}

func TestRules(t *testing.T) {
	rejected := []string{"PROCESS-NAME,app,node:direct", "UID,1000,node:direct", "OR,((DOMAIN,a.test),(DOMAIN,b.test)),node:direct", "NOT,((DOMAIN,a.test)),node:direct", "RULE-SET,remote,node:direct", "SUB-RULE,x,node:direct", "DOMAIN,a.test,DIRECT", "DOMAIN,a.test,node:missing", "DOMAIN,bad host,node:direct", "IP-CIDR,::1/128,node:direct", "IP-CIDR,127.0.0.1/8,node:direct,bad", "IN-NAME,absent,node:direct", "AND,((DOMAIN,a.test)),node:direct", "AND,((DOMAIN,a.test),(PROCESS-NAME,x)),node:direct", "AND,((DOMAIN,a.test),(AND,((DOMAIN,a.test),(DOMAIN,b.test))))),node:direct", "MATCH,node:direct", "DOMAIN,a.test,node:direct,extra"}
	for _, rule := range rejected {
		t.Run(rule, func(t *testing.T) {
			v := profileMap(t)
			v["rules"] = []string{rule, "MATCH,node:direct"}
			assertRejected(t, encode(t, v), "gen")
		})
	}
	for _, rules := range [][]string{{}, {"DOMAIN,a.test,node:direct"}, {"MATCH,DIRECT"}, {"MATCH,node:missing"}} {
		v := profileMap(t)
		v["rules"] = rules
		assertRejected(t, encode(t, v), "gen")
	}
	accepted := []string{"DOMAIN,a.test,node:direct", "DOMAIN-SUFFIX,test,group:main", "IP-CIDR,127.0.0.0/8,node:http,no-resolve", "IN-NAME,entry-a,node:direct", "AND,((DOMAIN,a.test),(IN-NAME,entry-a),(IP-CIDR,127.0.0.0/8,no-resolve)),group:main"}
	v := profileMap(t)
	v["rules"] = append(accepted, "MATCH,REJECT")
	cfg, err := Validate(encode(t, v), "gen")
	if err != nil {
		t.Fatal(err)
	}
	for i, rule := range accepted {
		if cfg.Rules[i] != rule {
			t.Fatal("rule order/content changed")
		}
	}
}
func TestValidAndOptional(t *testing.T) {
	v := profileMap(t)
	delete(v, "hosts")
	delete(v, "proxy-groups")
	v["rules"] = []string{"MATCH,node:direct"}
	if _, err := Validate(encode(t, v), "generation-1"); err != nil {
		t.Fatal(err)
	}
}

func TestBoundsAndGraph(t *testing.T) {
	for _, n := range []int{8, 9} {
		t.Run(fmt.Sprintf("chain-%d", n), func(t *testing.T) {
			v := profileMap(t)
			v["proxies"] = []any{map[string]any{"name": "node:direct", "type": "direct"}}
			groups := []any{}
			prev := "node:direct"
			for i := 1; i < n; i++ {
				name := fmt.Sprintf("group:g%d", i)
				groups = append(groups, map[string]any{"name": name, "type": "select", "proxies": []string{prev}})
				prev = name
			}
			v["proxy-groups"] = groups
			v["rules"] = []string{"MATCH," + prev}
			_, err := Validate(encode(t, v), "gen")
			if n == 8 && err != nil {
				t.Fatal(err)
			}
			if n == 9 {
				assertRejected(t, encode(t, v), "gen")
			}
		})
	}
	for _, count := range []int{4, 5} {
		t.Run(fmt.Sprintf("listeners-%d", count), func(t *testing.T) {
			v := profileMap(t)
			listeners := []any{}
			for i := 0; i < count; i++ {
				listeners = append(listeners, map[string]any{"name": fmt.Sprintf("entry-%d", i), "type": "mixed", "listen": "127.0.0.1", "port": 18100 + i, "udp": false, "users": []User{{Username: fmt.Sprintf("user-%d", i), Password: strings.Repeat("p", 32)}}})
			}
			v["listeners"] = listeners
			_, err := Validate(encode(t, v), "gen")
			if count == 4 && err != nil {
				t.Fatal(err)
			}
			if count == 5 {
				assertRejected(t, encode(t, v), "gen")
			}
		})
	}
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprintf("nodes-%d", count), func(t *testing.T) {
			v := profileMap(t)
			nodes := []Node{}
			for i := 0; i < count; i++ {
				nodes = append(nodes, Node{Name: fmt.Sprintf("node:n%d", i), Type: "direct"})
			}
			v["proxies"] = nodes
			v["proxy-groups"] = []Group{}
			v["rules"] = []string{"MATCH,node:n0"}
			_, err := Validate(encode(t, v), "gen")
			if count == 64 && err != nil {
				t.Fatal(err)
			}
			if count == 65 {
				assertRejected(t, encode(t, v), "gen")
			}
		})
	}
	for _, count := range []int{64, 65} {
		t.Run(fmt.Sprintf("rules-%d", count), func(t *testing.T) {
			v := profileMap(t)
			rules := []string{}
			for i := 1; i < count; i++ {
				rules = append(rules, "DOMAIN,a.test,node:direct")
			}
			rules = append(rules, "MATCH,node:direct")
			v["rules"] = rules
			_, err := Validate(encode(t, v), "gen")
			if count == 64 && err != nil {
				t.Fatal(err)
			}
			if count == 65 {
				assertRejected(t, encode(t, v), "gen")
			}
		})
	}
	for _, count := range []int{2, 8, 9} {
		t.Run(fmt.Sprintf("and-%d", count), func(t *testing.T) {
			v := profileMap(t)
			atoms := []string{}
			for i := 0; i < count; i++ {
				atoms = append(atoms, "(DOMAIN,a.test)")
			}
			v["rules"] = []string{"AND,(" + strings.Join(atoms, ",") + "),node:direct", "MATCH,node:direct"}
			_, err := Validate(encode(t, v), "gen")
			if count <= 8 && err != nil {
				t.Fatal(err)
			}
			if count > 8 {
				assertRejected(t, encode(t, v), "gen")
			}
		})
	}
}
func TestFullPreservationAndCredentialIsolation(t *testing.T) {
	cfg, err := Validate([]byte(validProfile), "gen")
	if err != nil {
		t.Fatal(err)
	}
	var expected Config
	if err = json.Unmarshal([]byte(validProfile), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, expected) {
		t.Fatal("config changed")
	}
	v := profileMap(t)
	first := listener(v)
	second := map[string]any{}
	for k, value := range first {
		second[k] = value
	}
	second["name"] = "entry-b"
	second["port"] = 18082
	v["listeners"] = []any{first, second}
	assertRejected(t, encode(t, v), "gen")
	second["users"] = []User{{Username: "user-b", Password: strings.Repeat("q", 32)}}
	if _, err = Validate(encode(t, v), "gen"); err != nil {
		t.Fatal(err)
	}
}
func FuzzValidate(f *testing.F) {
	f.Add([]byte(validProfile))
	f.Add([]byte(`{"rules":["AND,()),REJECT"]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		cfg, err := Validate(raw, "gen")
		if err != nil && (err != ErrProfile || !reflect.DeepEqual(cfg, Config{})) {
			t.Fatal("non-fixed error or partial configuration")
		}
	})
}

func TestStrictNestedObjects(t *testing.T) {
	for _, path := range []string{"listener", "user", "node", "group", "profile", "host"} {
		t.Run(path, func(t *testing.T) {
			var old string
			switch path {
			case "listener":
				old = `"name":"entry-a"`
			case "user":
				old = `"username":"user-a"`
			case "node":
				old = `"name":"node:direct"`
			case "group":
				old = `"name":"group:main"`
			case "profile":
				old = `"store-selected":false`
			case "host":
				old = `"alpha.test":"127.0.0.1"`
			}
			assertRejected(t, []byte(strings.Replace(validProfile, old, old+","+old, 1)), "gen")
		})
	}
	for _, fragment := range []string{`"port":18080`, `"type":"mixed"`, `"udp":false`, `"username":"user-a"`, `"password":"pppppppppppppppppppppppppppppppp"`, `"store-selected":false`} {
		t.Run("null-"+fragment, func(t *testing.T) {
			key := fragment[:strings.Index(fragment, ":")+1]
			assertRejected(t, []byte(strings.Replace(validProfile, fragment, key+"null", 1)), "gen")
		})
	}
	for _, extra := range []string{`"allow-lan":true`, `"proxy-providers":{}`, `"rule-providers":{}`, `"authentication":[]`, `"external-controller-unix":"/tmp/socket"`} {
		assertRejected(t, []byte(strings.Replace(validProfile, `{"mode":`, `{`+extra+`,"mode":`, 1)), "gen")
	}
}

func TestMalformedANDCannotPanic(t *testing.T) {
	for _, rule := range []string{"AND,()),node:direct", "AND,()),REJECT", "AND,(()),node:direct"} {
		v := profileMap(t)
		v["rules"] = []string{rule, "MATCH,node:direct"}
		assertRejected(t, encode(t, v), "gen:bad-and")
	}
}
