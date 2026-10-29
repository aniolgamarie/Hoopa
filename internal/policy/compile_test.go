//go:build linux && amd64

package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/subscription"
	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

func compileJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func compileSHA(raw []byte) string       { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func compileGroupAlias(id string) string { return "group:" + compileSHA([]byte(id)) }

type compileFixture struct {
	input  CompileInput
	intent map[string]any
	raws   map[config.ID][]byte
	rules  []subscription.RegionRule
}

func compileSource(t *testing.T, id config.ID, raw []byte, rules []subscription.RegionRule, keyByte byte) subscription.Snapshot {
	t.Helper()
	s, e := subscription.Parse(raw, subscription.ParseOptions{SourceID: id, IdentityKey: bytes.Repeat([]byte{keyByte}, 32), RegionRules: rules})
	if e != nil {
		t.Fatal("source fixture", e)
	}
	return s
}
func compileNode(s subscription.Snapshot, name string) config.ID {
	for _, n := range s.Nodes {
		if n.DisplayName == name {
			return n.ID
		}
	}
	panic("source fixture node")
}
func newCompileFixture(t *testing.T) compileFixture {
	t.Helper()
	bin := config.BinaryReference{Path: "/synthetic/fixed-source-core", Version: "fixture-v2", SHA256: strings.Repeat("a", 64)}
	cap := mihomo.CapabilityRecord{SchemaVersion: 1, Binary: bin, GOOS: "linux", GOARCH: "amd64", Protocol: 2, Mode: "basic-sources", Capabilities: []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"}, EvidenceSHA256: compileSHA([]byte("synthetic compiler shape; never production approval")), Accepted: true}
	f := compileFixture{raws: map[config.ID][]byte{}, rules: []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "JP ", Region: "JP"}, {Prefix: "nonmatching-", Region: "ZZ"}}}
	sources := map[config.ID]subscription.Snapshot{}
	for _, id := range []config.ID{"a", "b"} {
		nodes := []map[string]any{}
		for i, name := range []string{"US shared", "US second", "JP shared", "unknown"} {
			nodes = append(nodes, map[string]any{"name": name, "type": "http", "server": "edge.example.test", "port": 24001 + i, "username": "provider-user", "password": "provider-private-" + string(id)})
		}
		raw := compileJSON(t, map[string]any{"proxies": nodes})
		f.raws[id] = raw
		sources[id] = compileSource(t, id, raw, f.rules, 0x71)
	}
	probe := map[string]any{"url": "https://probe.example.test/check", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}
	auto := func(id string, region any, required bool) map[string]any {
		m := map[string]any{"id": id, "required": required, "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": probe}
		if region != nil {
			m["region"] = region
		}
		return m
	}
	f.intent = map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": bin}, "subscriptions": []any{map[string]any{"id": "a", "source": map[string]any{"kind": "file", "path": "/synthetic/a.yaml"}, "update_hours": nil}, map[string]any{"id": "b", "source": map[string]any{"kind": "file", "path": "/synthetic/b.yaml"}, "update_hours": nil}}, "selection": map[string]any{"active_subscription": "a"},
		"groups": []any{auto("US", "US", true), auto("JP", "JP", true), auto("BEST", nil, true), map[string]any{"id": "US-follow", "required": true, "strategy": "follow", "follows": "US"}, map[string]any{"id": "BEST-follow", "required": true, "strategy": "follow", "follows": "BEST"}, map[string]any{"id": "pinned-A", "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "a"}, "selected_node_id": compileNode(sources["a"], "US shared")}, auto("optional-ZZ", "ZZ", false)},
		"apps":   []any{}, "rules": []any{
			map[string]any{"id": "domain-rule", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "EXAMPLE.TEST."}, "target": map[string]any{"kind": "group", "group_id": "US-follow"}},
			map[string]any{"id": "inactive-pin", "match": map[string]any{"kind": "domain", "operator": "suffix", "value": "SUFFIX.TEST"}, "target": map[string]any{"kind": "node", "subscription_id": "b", "node_id": compileNode(sources["b"], "JP shared")}},
			map[string]any{"id": "cidr-rule", "match": map[string]any{"kind": "ip_cidr", "value": "192.0.2.129/24"}, "target": map[string]any{"kind": "direct"}},
			map[string]any{"id": "all-rule", "match": map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "domain", "operator": "exact", "value": "one.test"}, map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "ip_cidr", "value": "198.51.100.5/24"}, map[string]any{"kind": "domain", "operator": "suffix", "value": "two.test"}}}}}, "target": map[string]any{"kind": "reject"}},
		}, "default_target": map[string]any{"kind": "group", "group_id": "BEST-follow"}}
	f.input = CompileInput{Sources: sources, Generation: "generation:compile", Capability: cap, Runtime: RuntimeInput{Controller: "127.0.0.1:31000", Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "entry-shared", Type: "mixed", Listen: "127.0.0.1", Port: 31001, UDP: false, Users: []profile.User{{Username: "user-shared", Password: strings.Repeat("p", 32)}}}}}
	f.sync(t)
	return f
}
func (f *compileFixture) sync(t *testing.T) { f.input.Intent = compileJSON(t, f.intent) }
func compileObj(v any) map[string]any       { return v.(map[string]any) }
func compileIntentGroup(m map[string]any, id string) map[string]any {
	for _, g := range m["groups"].([]any) {
		x := compileObj(g)
		if x["id"] == id {
			return x
		}
	}
	panic("fixture group")
}
func compileResult(t *testing.T, input CompileInput) (Compilation, config.BundleManifest, map[string][]byte, wire.Snapshot, RouteMap) {
	t.Helper()
	c, e := Compile(input)
	if e != nil {
		t.Fatal("valid compile failed", e)
	}
	m, files := c.Bundle()
	s, e := wire.DecodeSnapshot(files[mihomo.SnapshotFile])
	if e != nil {
		t.Fatal("compiled expected Snapshot not strict v2", e)
	}
	var route RouteMap
	if json.Unmarshal(files["route-map.json"], &route) != nil {
		t.Fatal("route map")
	}
	return c, m, files, s, route
}
func compileReject(t *testing.T, input CompileInput, want error) {
	t.Helper()
	c, e := Compile(input)
	if !errors.Is(e, want) || e.Error() != want.Error() {
		t.Fatal("wrong compile failure", e)
	}
	m, files := c.Bundle()
	if !reflect.DeepEqual(m, config.BundleManifest{}) || files != nil || c.Inputs() != nil {
		t.Fatal("failed compilation retained partial bundle/inputs")
	}
	if c.ValidateIntent(input.Intent, nil) == nil {
		t.Fatal("failed compilation validated intent")
	}
}
func compiledProxy(s wire.Snapshot, name string) wire.Proxy {
	for _, p := range s.Proxies {
		if p.Name == name {
			return p
		}
	}
	panic("compiled proxy")
}
func expectedNodeMembers(s subscription.Snapshot, region string) []string {
	members := []string{}
	for _, n := range s.Nodes {
		if region == "" || n.Region.Kind == "declared-prefix" && n.Region.Code == region {
			members = append(members, "node:"+string(n.ID))
		}
	}
	sort.Strings(members)
	return members
}
func TestCompileTwoSourcesActiveSwitchAndStaticGroups(t *testing.T) {
	f := newCompileFixture(t)
	for _, active := range []config.ID{"a", "b"} {
		compileObj(f.intent["selection"])["active_subscription"] = active
		f.sync(t)
		_, _, files, s, route := compileResult(t, f.input)
		if s.ConfigSHA256 != compileSHA(files["core.json"]) || route.SharedEntry != "entry-shared" || len(s.Entries) != 1 {
			t.Fatal("shared-entry/generated binding mismatch")
		}
		for id, region := range map[string]string{"US": "US", "JP": "JP", "BEST": ""} {
			g := compiledProxy(s, compileGroupAlias(id))
			if g.Type != "URLTest" || g.Selected != "" || g.Probe == nil || g.Probe.URL != "https://probe.example.test/check" || g.Probe.IntervalSeconds != 60 || g.Probe.TimeoutMS != 1000 || g.Probe.ToleranceMS != 0 || !reflect.DeepEqual(g.Members, expectedNodeMembers(f.input.Sources[active], region)) {
				t.Fatal("auto group lost exact source/candidates/static Probe")
			}
		}
		for _, pair := range [][2]string{{"US-follow", "US"}, {"BEST-follow", "BEST"}} {
			g := compiledProxy(s, compileGroupAlias(pair[0]))
			if g.Type != "Selector" || g.Selected != compileGroupAlias(pair[1]) || !reflect.DeepEqual(g.Members, []string{compileGroupAlias(pair[1])}) || g.Probe != nil {
				t.Fatal("follow replaced its parent with a leaf")
			}
		}
		pinned := "node:" + string(compileNode(f.input.Sources["a"], "US shared"))
		if g := compiledProxy(s, compileGroupAlias("pinned-A")); g.Selected != pinned || !reflect.DeepEqual(g.Members, []string{pinned}) {
			t.Fatal("explicit source pin changed with active source")
		}
		if g := compiledProxy(s, compileGroupAlias("optional-ZZ")); g.Type != "Selector" || g.Selected != "REJECT" || !reflect.DeepEqual(g.Members, []string{"REJECT"}) || g.Probe != nil {
			t.Fatal("optional empty group got fallback")
		}
		if len(route.Nodes) != 5 {
			t.Fatal("compiler emitted unneeded inactive remote leaves")
		}
		for _, node := range route.Nodes {
			if node.Alias != "node:"+string(node.NodeID) {
				t.Fatal("opaque node alias changed")
			}
			if node.SubscriptionID != active && !(node.SubscriptionID == "a" && node.NodeID == compileNode(f.input.Sources["a"], "US shared")) && !(node.SubscriptionID == "b" && node.NodeID == compileNode(f.input.Sources["b"], "JP shared")) {
				t.Fatal("unneeded remote node generated")
			}
		}
	}
}
func TestCompileCompleteSourceAssetsAndInputHashes(t *testing.T) {
	f := newCompileFixture(t)
	c, m, files, s, route := compileResult(t, f.input)
	if m.Generation != f.input.Generation || m.CompilerVersion != CompilerVersion || !reflect.DeepEqual(m.Binaries, []config.BinaryReference{f.input.Capability.Binary}) {
		t.Fatal("manifest omitted compiler/binary/generation")
	}
	capSHA, _ := f.input.Capability.Digest()
	if m.CapabilitiesDigest != capSHA {
		t.Fatal("capability binding omitted")
	}
	if len(m.Files) != len(files) {
		t.Fatal("manifest/files mismatch")
	}
	kinds := map[string]string{}
	for i, file := range m.Files {
		if file.Bytes != int64(len(files[file.Name])) || file.SHA256 != compileSHA(files[file.Name]) || i > 0 && m.Files[i-1].Name >= file.Name {
			t.Fatal("asset byte/hash/sort binding mismatch")
		}
		kinds[file.Name] = file.Kind
	}
	if kinds["intent.json"] != "intent" || kinds["core.json"] != "generated_config" || kinds["route-map.json"] != "route_map" || kinds[mihomo.SnapshotFile] != "validation" || kinds["rule-policy.json"] != "rule" {
		t.Fatal("required bundle asset kind missing")
	}
	canonical, _ := config.CanonicalJSON(f.input.Intent)
	if !bytes.Equal(files["intent.json"], canonical) {
		t.Fatal("intent asset not full canonical input")
	}
	seen := map[config.ID]bool{}
	for name, kind := range kinds {
		if kind != "provider" {
			continue
		}
		var asset struct {
			SchemaVersion int                       `json:"schema_version"`
			ParserVersion string                    `json:"parser_version"`
			SnapshotID    config.ID                 `json:"snapshot_id"`
			SourceID      config.ID                 `json:"source_id"`
			KeyID         string                    `json:"key_id"`
			Original      []byte                    `json:"original_bytes"`
			Rules         []subscription.RegionRule `json:"region_rules"`
			Nodes         []subscription.Node       `json:"nodes"`
		}
		if json.Unmarshal(files[name], &asset) != nil {
			t.Fatal("provider asset")
		}
		source := f.input.Sources[asset.SourceID]
		if asset.SchemaVersion != 1 || asset.ParserVersion != "hoopa.provider.v1" || asset.SnapshotID != source.ID || asset.KeyID != source.KeyID || !bytes.Equal(asset.Original, f.raws[asset.SourceID]) || !reflect.DeepEqual(asset.Rules, f.rules) || !reflect.DeepEqual(asset.Nodes, source.Nodes) {
			t.Fatal("provider asset omitted complete private authority")
		}
		seen[asset.SourceID] = true
	}
	if len(seen) != 2 || len(route.Nodes) != 5 {
		t.Fatal("inactive pinned source asset or generation missing")
	}
	inputKeys := map[string]bool{}
	for _, in := range c.Inputs() {
		inputKeys[in.Kind+"/"+in.ID] = true
		switch in.Kind {
		case "core_binary":
			if in.ID != "mihomo" || in.SHA256 != f.input.Capability.Binary.SHA256 {
				t.Fatal("binary input")
			}
		case "capabilities":
			if in.SHA256 != capSHA {
				t.Fatal("cap input")
			}
		case "provider", "rule_asset":
			if in.SHA256 != compileSHA(files[in.ID]) {
				t.Fatal("asset input hashes not actual bytes")
			}
		default:
			t.Fatal("unexpected input kind")
		}
	}
	if len(inputKeys) != 5 || !inputKeys["core_binary/mihomo"] || !inputKeys["capabilities/core-capabilities"] || !inputKeys["rule_asset/rule-policy.json"] {
		t.Fatal("complete explicit input kind/identity set missing")
	}
	for name, kind := range kinds {
		if kind == "provider" && !inputKeys["provider/"+name] {
			t.Fatal("referenced source asset absent from input set")
		}
	}
	if c.ValidateIntent(f.input.Intent, c.Inputs()) != nil {
		t.Fatal("complete input set did not validate")
	}
	var native profile.Config
	if json.Unmarshal(files["core.json"], &native) != nil {
		t.Fatal("generated config")
	}
	bjp := "node:" + string(compileNode(f.input.Sources["b"], "JP shared"))
	wantRules := []string{"DOMAIN,example.test," + compileGroupAlias("US-follow"), "DOMAIN-SUFFIX,suffix.test," + bjp, "IP-CIDR,192.0.2.0/24,node:local-direct", "AND,((DOMAIN,one.test),(IP-CIDR,198.51.100.0/24),(DOMAIN-SUFFIX,two.test)),REJECT", "MATCH," + compileGroupAlias("BEST-follow")}
	if !reflect.DeepEqual(native.Rules, wantRules) {
		t.Fatal("rule lowering order/CIDR masking/default-resolve changed")
	}
	if s.Rules[2].Type != "IPCIDR" || s.Rules[2].Payload != "192.0.2.0/24" || s.Rules[3].Payload != "((Domain,one.test) && (IPCIDR,198.51.100.0/24) && (DomainSuffix,two.test))" || route.Rules[1].ID != "inactive-pin" || route.Rules[1].Index != 1 || route.Rules[1].Target != bjp {
		t.Fatal("independent rule/route-map projection differs")
	}
}
