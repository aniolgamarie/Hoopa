package sourceprofile_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

func TestEveryGeneratedObjectExactFields(t *testing.T) {
	type location struct {
		path []any
		keys []string
	}
	root := fixture(t)
	raw := encode(t, root)
	locations := []location{}
	var walk func(any, []any)
	walk = func(v any, path []any) {
		switch m := v.(type) {
		case map[string]any:
			loc := location{path: append([]any{}, path...)}
			for key, child := range m {
				loc.keys = append(loc.keys, key)
				walk(child, append(append([]any{}, path...), key))
			}
			locations = append(locations, loc)
		case []any:
			for i, child := range m {
				walk(child, append(append([]any{}, path...), i))
			}
		}
	}
	walk(root, nil)
	clone := func() map[string]any {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			t.Fatal("fixture")
		}
		return m
	}
	objectAt := func(m map[string]any, path []any) map[string]any {
		var v any = m
		for _, part := range path {
			switch p := part.(type) {
			case string:
				v = v.(map[string]any)[p]
			case int:
				v = v.([]any)[p]
			}
		}
		return v.(map[string]any)
	}
	for _, loc := range locations {
		m := clone()
		objectAt(m, loc.path)["unknown-secret-canary"] = true
		reject(t, encode(t, m))
		for _, key := range loc.keys {
			m := clone()
			delete(objectAt(m, loc.path), key)
			reject(t, encode(t, m))
			m = clone()
			objectAt(m, loc.path)[key] = nil
			reject(t, encode(t, m))
		}
	}
	for _, needle := range []string{`"username":"user-a"`, `"type":"mixed"`, `"tolerance":0`, `"store-selected":false`} {
		changed := bytes.Replace(raw, []byte(needle), []byte(needle+","+needle), 1)
		reject(t, changed)
	}
}
func TestExactGeneratedCollectionAndByteLimits(t *testing.T) {
	m := fixture(t)
	nodes := []any{}
	for i := 0; i < 63; i++ {
		nodes = append(nodes, map[string]any{"name": fmt.Sprintf("node:d%02d", i), "type": "direct"})
	}
	m["proxies"] = nodes
	m["proxy-groups"] = []any{map[string]any{"name": "group:reject", "type": "select", "proxies": []any{"REJECT"}}}
	rules := []any{}
	for i := 0; i < 63; i++ {
		rules = append(rules, "DOMAIN,example.test,REJECT")
	}
	m["rules"] = append(rules, "MATCH,REJECT")
	entries := m["listeners"].([]any)
	for i := 1; i < 4; i++ {
		entries = append(entries, map[string]any{"name": fmt.Sprintf("entry-%d", i), "type": "mixed", "listen": "127.0.0.1", "port": 19001 + i, "udp": false, "users": []any{map[string]any{"username": fmt.Sprintf("user-%d", i), "password": strings.Repeat(string(rune('u'+i)), 32)}}})
	}
	m["listeners"] = entries
	raw := encode(t, m)
	if _, err := profile.Project(raw, generation); err != nil {
		t.Fatal("exact finite collection bound rejected", err)
	}
	for _, collection := range []string{"listeners", "proxies", "rules"} {
		var over map[string]any
		if json.Unmarshal(raw, &over) != nil {
			t.Fatal("fixture")
		}
		switch collection {
		case "listeners":
			over[collection] = append(over[collection].([]any), map[string]any{"name": "entry-over", "type": "mixed", "listen": "127.0.0.1", "port": 19010, "udp": false, "users": []any{map[string]any{"username": "over", "password": strings.Repeat("x", 32)}}})
		case "proxies":
			over[collection] = append(over[collection].([]any), map[string]any{"name": "node:over", "type": "direct"})
		case "rules":
			r := over[collection].([]any)
			over[collection] = append(r[:len(r)-1], "DOMAIN,more.test,REJECT", "MATCH,REJECT")
		}
		reject(t, encode(t, over))
	}
	base := encode(t, fixture(t))
	boundary := append(append([]byte{}, base...), bytes.Repeat([]byte(" "), wire.MaxJSONBytes-len(base))...)
	if c, err := profile.Validate(boundary, generation); err != nil || c.Mode != "rule" {
		t.Fatal("exact64KiB input rejected", err)
	}
	reject(t, append(boundary, ' '))
	for _, key := range []string{"listeners", "proxies", "proxy-groups", "rules"} {
		m := fixture(t)
		m[key] = nil
		reject(t, encode(t, m))
	}
	for _, field := range []string{"secret", "password"} {
		for _, length := range []int{32, 128} {
			m := fixture(t)
			if field == "secret" {
				m[field] = strings.Repeat("x", length)
			} else {
				obj(firstEntry(m)["users"].([]any)[0])[field] = strings.Repeat("x", length)
			}
			if _, err := profile.Validate(encode(t, m), generation); err != nil {
				t.Fatal("exact credential bound rejected", err)
			}
		}
	}
	for _, value := range []string{strings.Repeat("x", 31), strings.Repeat("x", 129), strings.Repeat("x", 31) + ":", strings.Repeat("x", 31) + " ", strings.Repeat("x", 31) + "\x7f"} {
		m := fixture(t)
		m["secret"] = value
		reject(t, encode(t, m))
	}
}
func TestRuleGrammarAndProjectionLimits(t *testing.T) {
	for _, rule := range []string{"DOMAIN,example.test,REJECT,extra", "IP-CIDR,192.0.2.0/24,REJECT,unsupported", "IP-CIDR6,2001:db8::/32,REJECT", "IP-CIDR,bad,REJECT", "DOMAIN,bad_host,REJECT", "IN-NAME,missing,REJECT", "AND,((DOMAIN,example.test),(DOMAIN-SUFFIX,test)),DIRECT", "AND,((DOMAIN,example.test),(IP-CIDR,192.0.2.0/24,resolve)),REJECT"} {
		m := fixture(t)
		m["rules"] = []any{rule, "MATCH,REJECT"}
		reject(t, encode(t, m))
	}
	atom := "(DOMAIN,example.test)"
	for _, count := range []int{2, 8, 9} {
		m := fixture(t)
		m["rules"] = []any{"AND,(" + strings.TrimSuffix(strings.Repeat(atom+",", count), ",") + "),REJECT", "MATCH,REJECT"}
		_, err := profile.Project(encode(t, m), generation)
		if (err == nil) != (count <= 8) {
			t.Fatal("AND child bound incorrect", count, err)
		}
	}
}

func TestEntryUniquenessAndPortLiteralBoundaries(t *testing.T) {
	for _, which := range []string{"name", "address", "credentials"} {
		m := fixture(t)
		next := map[string]any{"name": "entry-b", "type": "mixed", "listen": "127.0.0.1", "port": 19002, "udp": false, "users": []any{map[string]any{"username": "user-b", "password": strings.Repeat("q", 32)}}}
		switch which {
		case "name":
			next["name"] = "entry-a"
		case "address":
			next["port"] = 19001
		case "credentials":
			next["users"] = firstEntry(m)["users"]
		}
		m["listeners"] = append(m["listeners"].([]any), next)
		reject(t, encode(t, m))
	}
	for _, number := range []string{"19001.0", "19001e0", "-0", "65536", "9007199254740992"} {
		m := fixture(t)
		firstEntry(m)["port"] = json.Number(number)
		reject(t, encode(t, m))
	}
	for _, number := range []string{"443.0", "443e0", "-0", "65536"} {
		m := fixture(t)
		firstNode(m)["port"] = json.Number(number)
		reject(t, encode(t, m))
	}
	for _, port := range []int{1, 65535} {
		m := fixture(t)
		firstEntry(m)["port"] = port
		if _, err := profile.Project(encode(t, m), generation); err != nil {
			t.Fatal("valid local port bound rejected", err)
		}
		node := inputNode("http")
		node["port"] = port
		nodes := canonical(t, node)
		m = fixture(t)
		m["proxies"] = []any{nodes[0], map[string]any{"name": "node:direct", "type": "direct"}}
		firstGroup(m)["proxies"] = []any{nodes[0]["name"]}
		if _, err := profile.Project(encode(t, m), generation); err != nil {
			t.Fatal("valid remote port bound rejected", err)
		}
	}
}
