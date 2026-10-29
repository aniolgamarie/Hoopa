//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func updateRequestIntent(t *testing.T) map[string]any {
	t.Helper()
	var intent map[string]any
	raw := `{"schema_version":1,"core":{"mihomo":{"path":"/uninstalled/private-core","version":"v1.19.26","sha256":"` + strings.Repeat("a", 64) + `"}},"subscriptions":[{"id":"source-a","source":{"kind":"file","path":"/nonexistent/provider-a.yaml"},"update_hours":2},{"id":"source-b","source":{"kind":"https","url_secret_ref":"/nonexistent/private-b"},"update_hours":null}],"selection":{"active_subscription":"source-b"},"groups":[],"apps":[],"rules":[],"default_target":{"kind":"direct"}}`
	if json.Unmarshal([]byte(raw), &intent) != nil {
		t.Fatal("bad declaration fixture")
	}
	return intent
}
func updateRequestRaw(t *testing.T, params any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": "source-update-fixture", "operation": "subscription.update", "params": params})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func updateRequestParse(t *testing.T, params any) (Request, sourceUpdateRequest) {
	t.Helper()
	r, err := ParseRequest(updateRequestRaw(t, params))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseSourceUpdate(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, parsed
}
func updateRequestReject(t *testing.T, raw []byte) {
	t.Helper()
	r, err := ParseRequest(raw)
	if err == nil {
		var parsed sourceUpdateRequest
		parsed, err = parseSourceUpdate(r)
		if !reflect.DeepEqual(parsed, sourceUpdateRequest{}) {
			t.Fatal("invalid request returned partial declaration")
		}
	}
	if err != ErrRequest {
		t.Fatal("invalid request must return bounded ErrRequest")
	}
	if strings.Contains(fmt.Sprint(err), "private-material") {
		t.Fatal("error contains request material")
	}
}
func updateRequestParams() map[string]any {
	return map[string]any{"subscription_id": "source-a", "expected_revision": "revision-1"}
}

func TestSourceUpdateRequestRequiredAndRegionPresence(t *testing.T) {
	_, p := updateRequestParse(t, updateRequestParams())
	if p.SourceID != "source-a" || p.Revision != "revision-1" || p.Intent != nil || p.HasRegionRules || p.RegionRules != nil {
		t.Fatal("omitted optional fields changed")
	}
	params := updateRequestParams()
	params["region_rules"] = []any{}
	_, p = updateRequestParse(t, params)
	if !p.HasRegionRules || p.RegionRules == nil || len(p.RegionRules) != 0 {
		t.Fatal("explicit empty policy indistinguishable from omitted")
	}
	params["region_rules"] = []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "unmatched", Region: "JP"}}
	r, p := updateRequestParse(t, params)
	if !p.HasRegionRules || len(p.RegionRules) != 2 || p.RegionRules[1].Prefix != "unmatched" {
		t.Fatal("complete ordered policy lost")
	}
	reversed := []subscription.RegionRule{p.RegionRules[1], p.RegionRules[0]}
	params["region_rules"] = reversed
	other, next := updateRequestParse(t, params)
	if r.Digest() == other.Digest() || next.RegionRules[0] != reversed[0] {
		t.Fatal("ordered policy not bound")
	}
	// Public request JSON is JCS, rather than the private codec's Go field order.
	pretty := []byte(` { "operation":"subscription.update", "request_id":"source-update-fixture", "params": { "region_rules":[{"region":"US", "prefix":"US "}], "expected_revision":"revision-1", "subscription_id":"source-a" }, "api_version":"v1" } `)
	rr, err := ParseRequest(pretty)
	if err != nil {
		t.Fatal(err)
	}
	p, err = parseSourceUpdate(rr)
	if err != nil || !p.HasRegionRules || p.RegionRules[0].Region != "US" {
		t.Fatal("legal key order/whitespace rejected", err)
	}
}
func TestSourceUpdateRequestStrictEnvelopeAndParams(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `{"api_version":"v1","request_id":"id","operation":"subscription.update","params":null}`, `{"api_version":"v1","request_id":"id","operation":"subscription.update","params":{},"foreign":true}`, `{"api_version":"v1","api_version":"v1","request_id":"id","operation":"subscription.update","params":{}}`, `{"API_VERSION":"v1","request_id":"id","operation":"subscription.update","params":{}}`} {
		updateRequestReject(t, []byte(raw))
	}
	for _, params := range []any{nil, []any{}, map[string]any{}, map[string]any{"subscription_id": "source-a"}, map[string]any{"expected_revision": "revision-1"}} {
		updateRequestReject(t, updateRequestRaw(t, params))
	}
	for _, field := range []string{"subscription_id", "expected_revision"} {
		for _, value := range []any{nil, "", "bad id", 123, true, []any{}, map[string]any{}} {
			params := updateRequestParams()
			params[field] = value
			updateRequestReject(t, updateRequestRaw(t, params))
		}
	}
	for _, field := range []string{"foreign", "Subscription_ID", "EXPECTED_REVISION", "Intent", "Region_Rules"} {
		params := updateRequestParams()
		params[field] = "private-material"
		updateRequestReject(t, updateRequestRaw(t, params))
	}
	for _, name := range []string{"config.apply", "runtime.start", "Subscription.Update"} {
		r := request(t, "wrong-operation", "v1", name, `{"subscription_id":"source-a","expected_revision":"revision-1"}`)
		p, err := parseSourceUpdate(r)
		if err != ErrRequest || !reflect.DeepEqual(p, sourceUpdateRequest{}) {
			t.Fatal("foreign operation accepted")
		}
	}
	r := request(t, "wrong-version", "v2", "subscription.update", `{"subscription_id":"source-a","expected_revision":"revision-1"}`)
	p, err := parseSourceUpdate(r)
	if err != ErrRequest || !reflect.DeepEqual(p, sourceUpdateRequest{}) {
		t.Fatal("foreign version accepted")
	}
	p, err = parseSourceUpdate(Request{})
	if err != ErrRequest || !reflect.DeepEqual(p, sourceUpdateRequest{}) {
		t.Fatal("zero request accepted")
	}
	good := updateRequestRaw(t, updateRequestParams())
	for _, raw := range [][]byte{append(append([]byte{}, good...), []byte(`{}`)...), bytes.Replace(good, []byte(`"source-a"`), []byte(`"source\ud800"`), 1), bytes.Replace(good, []byte(`"source-a"`), []byte{'"', 255, '"'}, 1), bytes.Replace(good, []byte(`"subscription_id":"source-a"`), []byte(`"subscription_id":"source-b","subscription_id":"source-a"`), 1)} {
		updateRequestReject(t, raw)
	}
	params := updateRequestParams()
	params["unexpected"] = strings.Repeat("x", MaxRequestBytes)
	updateRequestReject(t, updateRequestRaw(t, params))
}
func TestSourceUpdateRequestRegionRuleBoundaries(t *testing.T) {
	for _, rules := range []any{nil, "private-material", true, map[string]any{}, []any{nil}, []any{map[string]any{"prefix": "US "}}, []any{map[string]any{"region": "US"}}, []any{map[string]any{"prefix": nil, "region": "US"}}, []any{map[string]any{"prefix": "US ", "region": nil}}, []any{map[string]any{"Prefix": "US ", "region": "US"}}, []any{map[string]any{"prefix": "US ", "Region": "US"}}, []any{map[string]any{"prefix": "US ", "region": "US", "foreign": false}}, []any{map[string]any{"prefix": "US ", "Prefix": "US ", "region": "US"}}, []any{map[string]any{"prefix": 1, "region": "US"}}} {
		params := updateRequestParams()
		params["region_rules"] = rules
		updateRequestReject(t, updateRequestRaw(t, params))
	}
	for _, rule := range []subscription.RegionRule{{Prefix: "", Region: "US"}, {Prefix: "US ", Region: ""}, {Prefix: strings.Repeat("p", 257), Region: "US"}, {Prefix: "US ", Region: strings.Repeat("R", 65)}, {Prefix: "US\n", Region: "US"}, {Prefix: "US ", Region: "U\u0085S"}, {Prefix: strings.Repeat("東", 86), Region: "JP"}} {
		params := updateRequestParams()
		params["region_rules"] = []subscription.RegionRule{rule}
		updateRequestReject(t, updateRequestRaw(t, params))
	}
	rules := make([]subscription.RegionRule, 64)
	for i := range rules {
		rules[i] = subscription.RegionRule{Prefix: strings.Repeat("p", 256), Region: strings.Repeat("R", 64)}
	}
	params := updateRequestParams()
	params["region_rules"] = rules
	_, p := updateRequestParse(t, params)
	if len(p.RegionRules) != 64 {
		t.Fatal("boundary policy lost")
	}
	params["region_rules"] = append(rules, subscription.RegionRule{Prefix: "x", Region: "US"})
	updateRequestReject(t, updateRequestRaw(t, params))
	params["region_rules"] = []subscription.RegionRule{{Prefix: "東京 ", Region: "日本"}}
	updateRequestParse(t, params)
	// Malformed Unicode is passed as raw JSON, without Go Marshal repairing it.
	raw := updateRequestRaw(t, params)
	raw = bytes.Replace(raw, []byte("東京 "), []byte{255}, 1)
	updateRequestReject(t, raw)
}
func TestSourceUpdateRequestOptionalDraftIsStrictAndExact(t *testing.T) {
	params := updateRequestParams()
	intent := updateRequestIntent(t)
	intent["groups"] = []any{map[string]any{"id": "unresolved-follow", "required": false, "strategy": "follow", "follows": "missing-parent"}}
	params["intent"] = intent
	r, p := updateRequestParse(t, params)
	if p.Intent == nil || p.SourceID != "source-a" {
		t.Fatal("draft declaration lost")
	}
	// Unresolved runtime references are legal Draft input; this is no compilation.
	declared, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = config.ParseIntent(declared, config.IntentApply); err == nil {
		t.Fatal("fixture was not an incomplete Draft")
	}
	selected, err := sourceUpdateDefinition(p, nil)
	if err != nil || selected.ID != "source-a" || selected.Source.Path != "/nonexistent/provider-a.yaml" {
		t.Fatal("exact supplied source ignored", err)
	}
	// Parsed slices/pointers are detached from Request and a subsequent parse.
	p.Intent.Subscriptions[0].Source.Path = "/changed"
	*p.Intent.Subscriptions[0].UpdateHours = 99
	again, err := parseSourceUpdate(r)
	if err != nil || again.Intent.Subscriptions[0].Source.Path != "/nonexistent/provider-a.yaml" || *again.Intent.Subscriptions[0].UpdateHours != 2 {
		t.Fatal("parser retained caller-modifiable declaration", err)
	}
	for _, bad := range []any{nil, true, []any{}, "private-material", map[string]any{}} {
		params := updateRequestParams()
		params["intent"] = bad
		updateRequestReject(t, updateRequestRaw(t, params))
	}
	for _, change := range []func(map[string]any){
		func(m map[string]any) { m["schema_version"] = 2 }, func(m map[string]any) { m["foreign"] = false }, func(m map[string]any) { m["CORE"] = m["core"]; delete(m, "core") },
		func(m map[string]any) { subs := m["subscriptions"].([]any); m["subscriptions"] = append(subs, subs[0]) },
		func(m map[string]any) { m["subscriptions"] = []any{m["subscriptions"].([]any)[1]} },
		func(m map[string]any) {
			s := m["subscriptions"].([]any)[0].(map[string]any)
			s["source"] = map[string]any{"kind": "file", "path": "/tmp/file", "url_secret_ref": "/tmp/private"}
		},
	} {
		intent := updateRequestIntent(t)
		change(intent)
		params := updateRequestParams()
		params["intent"] = intent
		updateRequestReject(t, updateRequestRaw(t, params))
	}
}
func TestSourceUpdateRequestPrivateFormattingAndPolicyCopies(t *testing.T) {
	params := updateRequestParams()
	params["intent"] = updateRequestIntent(t)
	params["region_rules"] = []subscription.RegionRule{{Prefix: "US ", Region: "US"}}
	r, p := updateRequestParse(t, params)
	p.RegionRules[0].Prefix = "changed"
	next, err := parseSourceUpdate(r)
	if err != nil || next.RegionRules[0].Prefix != "US " {
		t.Fatal("request shares policy with parse output")
	}
	for _, value := range []sourceUpdateRequest{p, {}, next} {
		if fmt.Sprint(value) != "control.sourceUpdateRequest{private input}" || fmt.Sprintf("%#v", value) != "control.sourceUpdateRequest{private input}" {
			t.Fatal("format exposed declaration")
		}
	}
}
func TestSourceUpdateDefinitionExactSourceAndNoFallback(t *testing.T) {
	intent := updateRequestIntent(t)
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := config.ParseIntent(raw, config.IntentDraft)
	if err != nil {
		t.Fatal(err)
	}
	r := sourceUpdateRequest{SourceID: "source-a"}
	got, err := sourceUpdateDefinition(r, &committed)
	if err != nil || got.ID != "source-a" || got.Source.Kind != "file" {
		t.Fatal("active source overrode exact requested ID", err)
	}
	*got.UpdateHours = 77
	if *committed.Subscriptions[0].UpdateHours != 2 {
		t.Fatal("returned schedule aliases committed declaration")
	}
	other, err := sourceUpdateDefinition(r, &committed)
	if err != nil || *other.UpdateHours != 2 {
		t.Fatal("schedule output not detached")
	}
	r.SourceID = "source-b"
	got, err = sourceUpdateDefinition(r, &committed)
	if err != nil || got.UpdateHours != nil || got.Source.Kind != "https" {
		t.Fatal("nil schedule changed")
	}
	for _, test := range []struct {
		r         sourceUpdateRequest
		committed *config.Intent
	}{{sourceUpdateRequest{SourceID: "missing"}, &committed}, {sourceUpdateRequest{SourceID: "source-a"}, nil}, {sourceUpdateRequest{SourceID: "missing", Intent: &config.Intent{}}, &committed}} {
		got, err := sourceUpdateDefinition(test.r, test.committed)
		if err != ErrRequest || !reflect.DeepEqual(got, config.Subscription{}) {
			t.Fatal("missing source fell back")
		}
	}
	duplicate := committed
	duplicate.Subscriptions = append(append([]config.Subscription{}, committed.Subscriptions...), committed.Subscriptions[0])
	got, err = sourceUpdateDefinition(sourceUpdateRequest{SourceID: "source-a"}, &duplicate)
	if err != ErrRequest || !reflect.DeepEqual(got, config.Subscription{}) {
		t.Fatal("ambiguous source accepted")
	}
	// A supplied candidate declaration takes priority even when the committed
	// source has the same ID; no active supplier, path, schedule or pin is rebound.
	supplied := committed
	supplied.Subscriptions = append([]config.Subscription{}, committed.Subscriptions...)
	hours := uint64(7)
	supplied.Subscriptions[0].Source.Path = "/candidate/provider.yaml"
	supplied.Subscriptions[0].UpdateHours = &hours
	got, err = sourceUpdateDefinition(sourceUpdateRequest{SourceID: "source-a", Intent: &supplied}, &committed)
	if err != nil || got.Source.Path != "/candidate/provider.yaml" || *got.UpdateHours != 7 {
		t.Fatal("committed definition overrode supplied declaration", err)
	}
	*got.UpdateHours = 9
	if hours != 7 {
		t.Fatal("supplied schedule aliases result")
	}
	supplied.Subscriptions = supplied.Subscriptions[1:]
	got, err = sourceUpdateDefinition(sourceUpdateRequest{SourceID: "source-a", Intent: &supplied}, &committed)
	if err != ErrRequest || !reflect.DeepEqual(got, config.Subscription{}) {
		t.Fatal("missing supplied exact source fell back to committed")
	}
}
