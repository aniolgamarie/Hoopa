package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func intentFixture(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	raw := `{"schema_version":1,"core":{"mihomo":{"path":"/opt/mihomo","version":"v1.19.26","sha256":"` + strings.Repeat("a", 64) + `"}},"subscriptions":[{"id":"sub","source":{"kind":"file","path":"/tmp/source.yaml"},"update_hours":null}],"selection":{"active_subscription":null},"groups":[],"apps":[],"rules":[],"default_target":{"kind":"direct"}}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func intentEncode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func intentObject(v any) map[string]any { return v.(map[string]any) }
func TestIntentMinimalAndDefaults(t *testing.T) {
	raw := intentEncode(t, intentFixture(t))
	for _, mode := range []IntentMode{IntentDraft, IntentApply} {
		in, err := ParseIntent(raw, mode)
		if err != nil {
			t.Fatal(err)
		}
		if in.Retention.Enabled || in.Retention.Days != 30 || in.Selection.ActiveSubscription != nil || in.Subscriptions[0].UpdateHours != nil {
			t.Fatalf("wrong defaults: %+v", in)
		}
	}
	if _, err := ParseIntent(raw, IntentMode(99)); !errors.Is(err, ErrIntent) {
		t.Fatal(err)
	}
}
func TestIntentRequiredAndUnknown(t *testing.T) {
	base := intentFixture(t)
	for key := range base {
		m := intentFixture(t)
		delete(m, key)
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("missing %s: %v", key, err)
		}
	}
	mutations := map[string]func(map[string]any){
		"top unknown":    func(m map[string]any) { m["secret-value"] = true },
		"case":           func(m map[string]any) { m["CORE"] = m["core"]; delete(m, "core") },
		"null core":      func(m map[string]any) { m["core"] = nil },
		"core unknown":   func(m map[string]any) { intentObject(m["core"])["extra"] = false },
		"binary missing": func(m map[string]any) { delete(intentObject(intentObject(m["core"])["mihomo"]), "sha256") },
		"latest":         func(m map[string]any) { intentObject(intentObject(m["core"])["mihomo"])["version"] = "latest" },
		"hash uppercase": func(m map[string]any) {
			intentObject(intentObject(m["core"])["mihomo"])["sha256"] = strings.Repeat("A", 64)
		},
		"empty path": func(m map[string]any) { intentObject(intentObject(m["core"])["mihomo"])["path"] = "" },
		"inline URL": func(m map[string]any) {
			intentObject(m["subscriptions"].([]any)[0])["source"] = map[string]any{"kind": "https", "url_secret_ref": "https://secret.test/token"}
		},
		"source union": func(m map[string]any) {
			intentObject(intentObject(m["subscriptions"].([]any)[0])["source"])["url_secret_ref"] = "/private/token"
		},
		"update missing":    func(m map[string]any) { delete(intentObject(m["subscriptions"].([]any)[0]), "update_hours") },
		"selection missing": func(m map[string]any) { m["selection"] = map[string]any{} },
		"retention missing": func(m map[string]any) { m["retention"] = map[string]any{"enabled": false} },
		"retention zero":    func(m map[string]any) { m["retention"] = map[string]any{"enabled": false, "days": 0} },
		"meter inherit": func(m map[string]any) {
			m["apps"] = []any{map[string]any{"id": "app", "mode": "inherit", "metering": true, "bypass": []any{}}}
		},
		"required false missing": func(m map[string]any) {
			m["groups"] = []any{map[string]any{"id": "g", "strategy": "follow", "follows": "missing"}}
		},
		"wrong target union": func(m map[string]any) { m["default_target"] = map[string]any{"kind": "direct", "group_id": "g"} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			m := intentFixture(t)
			mutate(m)
			if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func intentRich(t *testing.T) map[string]any {
	m := intentFixture(t)
	m["selection"] = map[string]any{"active_subscription": "sub"}
	m["groups"] = []any{
		map[string]any{"id": "manual", "required": false, "region": nil, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "sub"}, "selected_node_id": "node"},
		map[string]any{"id": "auto", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://example.test/check", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}},
		map[string]any{"id": "follow", "required": false, "strategy": "follow", "follows": "auto"},
	}
	m["apps"] = []any{map[string]any{"id": "app", "mode": "managed", "metering": true, "bypass": []any{"localhost"}}}
	conditions := []any{map[string]any{"kind": "app", "app_id": "app"}, map[string]any{"kind": "process_instance", "process_instance_id": "instance"}, map[string]any{"kind": "process_name", "value": "app"}, map[string]any{"kind": "process_path", "value": "/opt/app"}, map[string]any{"kind": "ip_cidr", "value": "192.0.2.0/24"}, map[string]any{"kind": "domain", "operator": "suffix", "value": "example.test"}}
	m["rules"] = []any{map[string]any{"id": "rule", "match": map[string]any{"kind": "all", "conditions": conditions}, "target": map[string]any{"kind": "node", "subscription_id": "sub", "node_id": "node"}}}
	m["default_target"] = map[string]any{"kind": "group", "group_id": "follow"}
	return m
}
func TestIntentUnions(t *testing.T) {
	m := intentRich(t)
	in, err := ParseIntent(intentEncode(t, m), IntentApply)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Groups) != 3 || len(in.Rules[0].Match.Conditions) != 6 || in.Groups[1].Probe.ToleranceMS != 0 || in.Groups[0].Required {
		t.Fatal("lost fields")
	}
	m["default_target"] = map[string]any{"kind": "reject"}
	if _, err := ParseIntent(intentEncode(t, m), IntentApply); err != nil {
		t.Fatal(err)
	}
	intentObject(m["subscriptions"].([]any)[0])["source"] = map[string]any{"kind": "https", "url_secret_ref": "/private/source-url"}
	if _, err := ParseIntent(intentEncode(t, m), IntentApply); err != nil {
		t.Fatal(err)
	}
}
func TestIntentDraftAndApply(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing active": func(m map[string]any) { m["selection"] = map[string]any{"active_subscription": nil} },
		"active unknown": func(m map[string]any) { m["selection"] = map[string]any{"active_subscription": "missing"} },
		"source missing": func(m map[string]any) {
			intentObject(intentObject(m["groups"].([]any)[0])["source"])["subscription_id"] = "missing"
		},
		"follow missing": func(m map[string]any) { intentObject(m["groups"].([]any)[2])["follows"] = "missing" },
		"follow cycle": func(m map[string]any) {
			m["groups"] = append(m["groups"].([]any), map[string]any{"id": "cycle", "required": false, "strategy": "follow", "follows": "follow"})
			intentObject(m["groups"].([]any)[2])["follows"] = "cycle"
		},
		"target missing": func(m map[string]any) { m["default_target"] = map[string]any{"kind": "group", "group_id": "missing"} },
		"node source missing": func(m map[string]any) {
			intentObject(intentObject(m["rules"].([]any)[0])["target"])["subscription_id"] = "missing"
		},
		"app missing": func(m map[string]any) {
			intentObject(intentObject(intentObject(m["rules"].([]any)[0])["match"])["conditions"].([]any)[0])["app_id"] = "missing"
		},
		"bad cidr": func(m map[string]any) {
			intentObject(intentObject(intentObject(m["rules"].([]any)[0])["match"])["conditions"].([]any)[4])["value"] = "bad/cidr"
		},
		"bad domain": func(m map[string]any) {
			intentObject(intentObject(intentObject(m["rules"].([]any)[0])["match"])["conditions"].([]any)[5])["value"] = "https://example.test"
		},
		"bad probe": func(m map[string]any) {
			intentObject(intentObject(m["groups"].([]any)[1])["probe"])["url"] = "file:///tmp/probe"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := intentRich(t)
			mutate(m)
			raw := intentEncode(t, m)
			if _, err := ParseIntent(raw, IntentDraft); err != nil {
				t.Fatalf("draft: %v", err)
			}
			if _, err := ParseIntent(raw, IntentApply); err != ErrIntent {
				t.Fatalf("apply: %v", err)
			}
		})
	}
	// Null active is complete when every group explicitly pins its source.
	m := intentRich(t)
	m["selection"] = map[string]any{"active_subscription": nil}
	intentObject(m["groups"].([]any)[1])["source"] = map[string]any{"kind": "subscription", "subscription_id": "sub"}
	if _, err := ParseIntent(intentEncode(t, m), IntentApply); err != nil {
		t.Fatal(err)
	}
}
func TestIntentNumbers(t *testing.T) {
	for _, number := range []string{"1.0", "1e0", "1.000e+0", "10e-1"} {
		m := intentFixture(t)
		m["schema_version"] = json.Number(number)
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != nil {
			t.Fatalf("integer %s: %v", number, err)
		}
	}
	for _, number := range []string{"0", "-1", "1.1", "9007199254740992", "1e99999999999999999999", "2562048"} {
		m := intentFixture(t)
		intentObject(m["subscriptions"].([]any)[0])["update_hours"] = json.Number(number)
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("hours %s: %v", number, err)
		}
	}
	for _, number := range []string{"9007199254740991", "9007199254740991.0"} {
		m := intentFixture(t)
		m["retention"] = map[string]any{"enabled": false, "days": json.Number(number)}
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"interval_seconds", "timeout_ms", "tolerance_ms"} {
		m := intentRich(t)
		intentObject(intentObject(m["groups"].([]any)[1])["probe"])[field] = 9007199254740991
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("duration overflow %s: %v", field, err)
		}
	}
}
func TestIntentDuplicateAndBounds(t *testing.T) {
	for _, field := range []string{"subscriptions", "groups", "apps", "rules"} {
		m := intentRich(t)
		values := m[field].([]any)
		m[field] = append(values, values[0])
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("duplicate %s: %v", field, err)
		}
	}
	for _, raw := range [][]byte{[]byte(`{"schema_version":1,"schema_version":1}`), []byte("secret: !bad super-secret-value"), []byte(strings.Repeat(" ", 128<<10) + "{}"), []byte(`{"secret":"\uD800"}`)} {
		if _, err := ParseIntent(raw, IntentDraft); err != ErrIntent || strings.Contains(err.Error(), "super-secret-value") {
			t.Fatalf("unsafe error %v", err)
		}
	}
	for _, depth := range []int{16, 17} {
		m := intentFixture(t)
		var pred any = map[string]any{"kind": "process_name", "value": "app"}
		for i := 1; i < depth; i++ {
			pred = map[string]any{"kind": "all", "conditions": []any{pred}}
		}
		m["rules"] = []any{map[string]any{"id": "r", "match": pred, "target": map[string]any{"kind": "direct"}}}
		_, err := ParseIntent(intentEncode(t, m), IntentDraft)
		if (err == nil) != (depth == 16) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
}
func TestIntentYAMLSafeAlias(t *testing.T) {
	m := intentFixture(t)
	raw := intentEncode(t, m)
	in, err := ParseIntent(raw, IntentApply)
	if err != nil {
		t.Fatal(err)
	}
	yaml := `schema_version: 1
core:
  mihomo:
    path: /opt/mihomo
    version: v1.19.26
    sha256: '` + strings.Repeat("a", 64) + `'
subscriptions:
  - id: &id sub
    source: {kind: file, path: /tmp/source.yaml}
    update_hours: null
selection: {active_subscription: *id}
groups: []
apps: []
rules: []
default_target: {kind: direct}
`
	got, err := ParseIntent([]byte(yaml), IntentApply)
	if err != nil {
		t.Fatal(err)
	}
	id := ID("sub")
	in.Selection.ActiveSubscription = &id
	if !reflect.DeepEqual(in, got) {
		t.Fatalf("representations differ")
	}
}

// Every schema object in the union-rich fixture rejects extra properties and
// requires each declared property, including explicit false/zero/null values.
func TestIntentNestedExactFields(t *testing.T) {
	type location struct {
		path []any
		keys []string
	}
	var locations []location
	var walk func(any, []any)
	walk = func(v any, path []any) {
		switch x := v.(type) {
		case map[string]any:
			loc := location{path: append([]any(nil), path...)}
			for key, child := range x {
				loc.keys = append(loc.keys, key)
				walk(child, append(append([]any(nil), path...), key))
			}
			locations = append(locations, loc)
		case []any:
			for i, child := range x {
				walk(child, append(append([]any(nil), path...), i))
			}
		}
	}
	fixture := intentRich(t)
	fixture["retention"] = map[string]any{"enabled": false, "days": 30}
	fixture["core"].(map[string]any)["gost"] = map[string]any{"path": "/opt/gost", "version": "v3", "sha256": strings.Repeat("b", 64)}
	walk(fixture, nil)
	objectAt := func(m map[string]any, path []any) map[string]any {
		var v any = m
		for _, part := range path {
			switch key := part.(type) {
			case string:
				v = v.(map[string]any)[key]
			case int:
				v = v.([]any)[key]
			}
		}
		return v.(map[string]any)
	}
	for _, loc := range locations {
		clone := func() map[string]any {
			var m map[string]any
			if err := json.Unmarshal(intentEncode(t, fixture), &m); err != nil {
				t.Fatal(err)
			}
			return m
		}
		m := clone()
		objectAt(m, loc.path)["unknown_secret_field"] = true
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("unknown at %v: %v", loc.path, err)
		}
		for _, key := range loc.keys {
			if key == "region" || len(loc.path) == 0 && key == "retention" || len(loc.path) == 1 && loc.path[0] == "core" && key == "gost" {
				continue
			}
			m := clone()
			delete(objectAt(m, loc.path), key)
			if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
				t.Fatalf("missing %v/%s: %v", loc.path, key, err)
			}
		}
	}
}
func TestIntentScalarTypes(t *testing.T) {
	for _, bad := range []any{nil, false, "1", []any{}, map[string]any{}, 1.25} {
		m := intentFixture(t)
		m["schema_version"] = bad
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("schema type %#v: %v", bad, err)
		}
	}
	for _, bad := range []any{nil, 0, "false", []any{}, map[string]any{}} {
		m := intentRich(t)
		intentObject(m["groups"].([]any)[0])["required"] = bad
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("required type %#v: %v", bad, err)
		}
	}
	for _, bad := range []string{"ftp://example.test", "https:secret", "https:%zz", "file:///secret", "\x00", " "} {
		m := intentFixture(t)
		intentObject(m["subscriptions"].([]any)[0])["source"] = map[string]any{"kind": "https", "url_secret_ref": bad}
		if _, err := ParseIntent(intentEncode(t, m), IntentDraft); err != ErrIntent {
			t.Fatalf("secret reference accepted %q", bad)
		}
	}
}

func TestIntentNegativeZero(t *testing.T) {
	for _, number := range []string{"-0", "-0.0", "-0e99"} {
		m := intentRich(t)
		intentObject(intentObject(m["groups"].([]any)[1])["probe"])["tolerance_ms"] = json.Number(number)
		if _, err := ParseIntent(intentEncode(t, m), IntentApply); err != nil {
			t.Fatalf("nonnegative zero %s: %v", number, err)
		}
	}
}

func TestIntentProbeURLAuthority(t *testing.T) {
	valid := []string{"https://example.test/check?status=204", "http://localhost:8080/", "https://example.test:65535/", "http://127.0.0.1:1/", "https://[2001:db8::1]:443/check?x=y", "http://[::1]/", "HTTPS://EXAMPLE.TEST./probe"}
	invalid := []string{"https://example.test:65536/", "https://example.test:0/", "https://example.test:/", "http://[example.test]/", "http://[127.0.0.1]/", "http://[2001:db8::gg]/", "http://2001:db8::1/", "http://bad_host.test/", "http://-bad.example/", "http://bad..example/", "http://example.test../", "http://éxample.test/", "http://999.999.999.999/", "http://user:password@example.test/", "http:opaque", "http://example.test/#fragment", "http://example.test/#", "http://example.test/\x7f", "http://example.test/\u0085", "http://example.test/\n", "http://[fe80::1%25eth0]/", "ftp://example.test/"}
	for _, s := range valid {
		m := intentRich(t)
		intentObject(intentObject(m["groups"].([]any)[1])["probe"])["url"] = s
		if _, err := ParseIntent(intentEncode(t, m), IntentApply); err != nil {
			t.Errorf("valid URL %q: %v", s, err)
		}
	}
	for _, s := range invalid {
		m := intentRich(t)
		intentObject(intentObject(m["groups"].([]any)[1])["probe"])["url"] = s
		raw := intentEncode(t, m)
		if _, err := ParseIntent(raw, IntentDraft); err != nil {
			t.Errorf("draft prematurely rejects %q: %v", s, err)
		}
		if _, err := ParseIntent(raw, IntentApply); err != ErrIntent {
			t.Errorf("invalid URL %q: %v", s, err)
		}
	}
}
