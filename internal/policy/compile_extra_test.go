//go:build linux && amd64

package policy

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func TestCompileRejectsUnboundAuthorityAndReferences(t *testing.T) {
	cases := map[string]func(*compileFixture){
		"synthetic-authority": func(f *compileFixture) {
			s := f.input.Sources["a"]
			f.input.Sources["a"] = subscription.Snapshot{ID: s.ID, SourceID: s.SourceID, KeyID: s.KeyID, Nodes: append([]subscription.Node{}, s.Nodes...)}
		},
		"metadata-display-rewrite": func(f *compileFixture) {
			s := f.input.Sources["a"]
			s.Nodes = append([]subscription.Node{}, s.Nodes...)
			s.Nodes[0].DisplayName = "forged"
			f.input.Sources["a"] = s
		},
		"metadata-region-rewrite": func(f *compileFixture) {
			s := f.input.Sources["a"]
			s.Nodes = append([]subscription.Node{}, s.Nodes...)
			s.Nodes[0].Region.Code = "JP"
			f.input.Sources["a"] = s
		},
		"key-id-rewrite": func(f *compileFixture) {
			s := f.input.Sources["a"]
			s.KeyID = strings.Repeat("0", 64)
			f.input.Sources["a"] = s
		},
		"source-relabel": func(f *compileFixture) { f.input.Sources["a"] = f.input.Sources["b"] },
		"missing-active": func(f *compileFixture) { delete(f.input.Sources, "a") }, "missing-inactive-pin": func(f *compileFixture) { delete(f.input.Sources, "b") },
		"rotated-key-invalidates-pin": func(f *compileFixture) { f.input.Sources["b"] = compileSource(t, "b", f.raws["b"], f.rules, 0x72) },
		"wrong-source-pin": func(f *compileFixture) {
			rule := compileObj(f.intent["rules"].([]any)[1])
			compileObj(rule["target"])["node_id"] = compileNode(f.input.Sources["a"], "JP shared")
		},
		"missing-manual-pin": func(f *compileFixture) {
			g := compileIntentGroup(f.intent, "pinned-A")
			g["selected_node_id"] = "missing"
			g["required"] = false
		},
		"unknown-required-region":  func(f *compileFixture) { compileIntentGroup(f.intent, "US")["region"] = "ZZ" },
		"wrong-binary-binding":     func(f *compileFixture) { f.input.Capability.Binary.SHA256 = strings.Repeat("b", 64) },
		"wrong-capability-profile": func(f *compileFixture) { f.input.Capability.Protocol = 1 }, "unaccepted-shape": func(f *compileFixture) { f.input.Capability.Accepted = false },
		"invalid-generation": func(f *compileFixture) { f.input.Generation = "bad generation" }, "bad-runtime-secret": func(f *compileFixture) { f.input.Runtime.Secret = "short" }, "runtime-endpoint-collision": func(f *compileFixture) { f.input.Runtime.Entry.Port = 31000 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCompileFixture(t)
			mutate(&f)
			f.sync(t)
			compileReject(t, f.input, ErrCompile)
		})
	}
	f := newCompileFixture(t)
	f.input.Sources["unused"] = subscription.Snapshot{}
	compileResult(t, f.input)
	// No groups or pins still requires the explicitly active source authority.
	f = newCompileFixture(t)
	f.intent["groups"] = []any{}
	f.intent["rules"] = []any{}
	f.intent["default_target"] = map[string]any{"kind": "reject"}
	s := f.input.Sources["a"]
	s.KeyID = "forged"
	f.input.Sources["a"] = s
	f.sync(t)
	compileReject(t, f.input, ErrCompile)
	// An inactive source used only by default_target remains a required input.
	f = newCompileFixture(t)
	f.intent["rules"] = []any{}
	f.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "b", "node_id": compileNode(f.input.Sources["b"], "US shared")}
	f.sync(t)
	compileResult(t, f.input)
	delete(f.input.Sources, "b")
	compileReject(t, f.input, ErrCompile)
}
func TestCompilationDeterminismInputsAndDetachedGetters(t *testing.T) {
	f := newCompileFixture(t)
	c, m, files, _, _ := compileResult(t, f.input)
	initialInputs := c.Inputs()
	reversed := f.input
	reversed.Sources = map[config.ID]subscription.Snapshot{"b": f.input.Sources["b"], "a": f.input.Sources["a"]}
	reversed.Intent = append([]byte(" \n"), f.input.Intent...)
	other, m2, files2, _, _ := compileResult(t, reversed)
	if !reflect.DeepEqual(m, m2) || !reflect.DeepEqual(files, files2) || !reflect.DeepEqual(initialInputs, other.Inputs()) {
		t.Fatal("map order/JSON whitespace changed deterministic compilation")
	}
	inputs := c.Inputs()
	for left, right := 0, len(inputs)-1; left < right; left, right = left+1, right-1 {
		inputs[left], inputs[right] = inputs[right], inputs[left]
	}
	if c.ValidateIntent(reversed.Intent, inputs) != nil {
		t.Fatal("input set order or canonical whitespace rejected")
	}
	for _, mutate := range []func([]config.BundleInput) []config.BundleInput{
		func(in []config.BundleInput) []config.BundleInput { return in[:len(in)-1] }, func(in []config.BundleInput) []config.BundleInput {
			return append(in, config.BundleInput{Kind: "unknown", ID: "unknown", SHA256: strings.Repeat("a", 64)})
		}, func(in []config.BundleInput) []config.BundleInput { in[0].SHA256 = strings.Repeat("b", 64); return in }, func(in []config.BundleInput) []config.BundleInput { in[1] = in[0]; return in }, func(in []config.BundleInput) []config.BundleInput { in[0].Kind = "foreign"; return in },
	} {
		if c.ValidateIntent(f.input.Intent, mutate(c.Inputs())) != ErrCompile {
			t.Fatal("non-exact compilation input set accepted")
		}
	}
	changed := newCompileFixture(t)
	changed.intent["retention"] = map[string]any{"enabled": false, "days": 31}
	changed.sync(t)
	if c.ValidateIntent(changed.input.Intent, c.Inputs()) != ErrCompile {
		t.Fatal("complete intent binding ignored retention metadata")
	}
	detached, detachedFiles := c.Bundle()
	detached.Binaries[0].SHA256 = "changed"
	detached.Files[0].SHA256 = "changed"
	detachedFiles["core.json"][0] = '!'
	delete(detachedFiles, "intent.json")
	inputs = c.Inputs()
	inputs[0].SHA256 = "changed"
	next, nextFiles := c.Bundle()
	if !reflect.DeepEqual(next, m) || !reflect.DeepEqual(nextFiles, files) || !reflect.DeepEqual(c.Inputs(), initialInputs) {
		t.Fatal("private bundle/input getters share mutable authority")
	}
	// Caller-owned runtime credentials and intent bytes are not retained by alias.
	f.input.Runtime.Entry.Users[0].Password = "changed"
	f.input.Intent[0] = '!'
	again, againFiles := c.Bundle()
	if !reflect.DeepEqual(again, m) || !reflect.DeepEqual(againFiles, files) {
		t.Fatal("caller mutation rewrote compiled bundle")
	}
	for _, out := range []string{fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), string(compileJSON(t, c))} {
		if strings.Contains(out, "provider-private-") || strings.Contains(out, strings.Repeat("s", 32)) || strings.Contains(out, strings.Repeat("p", 32)) {
			t.Fatal("private compiled material leaked through formatting")
		}
	}
}
func TestCompileCompletePolicyAndBinaryBinding(t *testing.T) {
	f := newCompileFixture(t)
	first, manifest, files, snapshot, _ := compileResult(t, f.input)
	rules := append([]subscription.RegionRule{}, f.rules...)
	rules[2].Region = "CA"
	f.input.Sources["a"] = compileSource(t, "a", f.raws["a"], rules, 0x71)
	changed, m2, files2, s2, _ := compileResult(t, f.input)
	if !bytes.Equal(files["core.json"], files2["core.json"]) || !reflect.DeepEqual(snapshot, s2) || reflect.DeepEqual(first.Inputs(), changed.Inputs()) || reflect.DeepEqual(manifest, m2) {
		t.Fatal("nonmatching local region policy did not bind full source asset independently")
	}
	if changed.ValidateIntent(f.input.Intent, first.Inputs()) != ErrCompile || first.ValidateIntent(f.input.Intent, changed.Inputs()) != ErrCompile {
		t.Fatal("old source input accepted after complete region policy change")
	}
	// Shape acceptance is not an approved binary lookup. A different explicit
	// candidate binary changes both intent/capability and manifest/input binding.
	f = newCompileFixture(t)
	base, baseManifest, baseFiles, _, _ := compileResult(t, f.input)
	f.input.Capability.EvidenceSHA256 = strings.Repeat("e", 64)
	capChange, capManifest, capFiles, _, _ := compileResult(t, f.input)
	if capManifest.CapabilitiesDigest == baseManifest.CapabilitiesDigest || reflect.DeepEqual(capChange.Inputs(), base.Inputs()) || !bytes.Equal(capFiles["core.json"], baseFiles["core.json"]) {
		t.Fatal("capability evidence not independently bound")
	}
	f.input.Capability.Binary.SHA256 = strings.Repeat("c", 64)
	compileObj(f.intent["core"])["mihomo"] = f.input.Capability.Binary
	f.sync(t)
	binaryChange, binaryManifest, _, _, _ := compileResult(t, f.input)
	if binaryManifest.Binaries[0].SHA256 != strings.Repeat("c", 64) || reflect.DeepEqual(binaryChange.Inputs(), capChange.Inputs()) {
		t.Fatal("explicit binary not bound")
	}
	f = newCompileFixture(t)
	_, runtimeBase, runtimeFiles, _, _ := compileResult(t, f.input)
	f.input.Runtime.Secret = strings.Repeat("q", 32)
	_, runtimeChanged, runtimeFilesChanged, _, _ := compileResult(t, f.input)
	if bytes.Equal(runtimeFiles["core.json"], runtimeFilesChanged["core.json"]) || reflect.DeepEqual(runtimeBase, runtimeChanged) {
		t.Fatal("private runtime endpoints/credentials omitted from generated asset binding")
	}
}
func TestCompileExplicitUnsupportedBranches(t *testing.T) {
	cases := map[string]func(*compileFixture){
		"apps": func(f *compileFixture) {
			f.intent["apps"] = []any{map[string]any{"id": "app", "mode": "managed", "metering": false, "bypass": []any{}}}
		},
		"gost": func(f *compileFixture) { compileObj(f.intent["core"])["gost"] = f.input.Capability.Binary }, "retention-enabled": func(f *compileFixture) { f.intent["retention"] = map[string]any{"enabled": true, "days": 30} },
		"process-name": func(f *compileFixture) {
			compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "process_name", "value": "app"}
		}, "process-path": func(f *compileFixture) {
			compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "process_path", "value": "/opt/app"}
		}, "process-instance": func(f *compileFixture) {
			compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "process_instance", "process_instance_id": "instance"}
		},
		"ipv6-cidr": func(f *compileFixture) {
			compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "ip_cidr", "value": "2001:db8::/32"}
		},
		"nine-atoms": func(f *compileFixture) {
			conditions := []any{}
			for i := 0; i < 9; i++ {
				conditions = append(conditions, map[string]any{"kind": "domain", "operator": "exact", "value": "example.test"})
			}
			compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "all", "conditions": conditions}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newCompileFixture(t)
			mutate(&f)
			f.sync(t)
			compileReject(t, f.input, ErrCompileUnsupported)
		})
	}
	for _, count := range []int{1, 8} {
		f := newCompileFixture(t)
		conditions := []any{}
		for i := 0; i < count; i++ {
			conditions = append(conditions, map[string]any{"kind": "domain", "operator": "exact", "value": "example.test"})
		}
		compileObj(f.intent["rules"].([]any)[0])["match"] = map[string]any{"kind": "all", "conditions": conditions}
		f.sync(t)
		compileResult(t, f.input)
	}
}
func compilerMinimal(t *testing.T) compileFixture {
	f := newCompileFixture(t)
	f.intent["groups"] = []any{}
	f.intent["rules"] = []any{}
	f.intent["default_target"] = map[string]any{"kind": "reject"}
	f.sync(t)
	return f
}
func TestCompileFiniteNativeCollectionAndSizeLimits(t *testing.T) {
	for _, count := range []int{64, 65} {
		f := compilerMinimal(t)
		groups := []any{}
		for i := 0; i < count; i++ {
			groups = append(groups, map[string]any{"id": fmt.Sprintf("empty-%02d", i), "required": false, "region": "ZZ", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://probe.example.test/", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}})
		}
		f.intent["groups"] = groups
		f.sync(t)
		if count == 64 {
			compileResult(t, f.input)
		} else {
			compileReject(t, f.input, ErrCompileUnsupported)
		}
	}
	for _, count := range []int{63, 64} {
		f := compilerMinimal(t)
		nodes := []map[string]any{}
		for i := 0; i < count; i++ {
			nodes = append(nodes, map[string]any{"name": fmt.Sprintf("node-%d", i), "type": "http", "server": "edge.example.test", "port": 24001 + i})
		}
		raw := compileJSON(t, map[string]any{"proxies": nodes})
		f.input.Sources["a"] = compileSource(t, "a", raw, nil, 0x71)
		f.intent["groups"] = []any{map[string]any{"id": "all", "required": true, "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://probe.example.test/", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}}}
		f.intent["default_target"] = map[string]any{"kind": "group", "group_id": "all"}
		f.sync(t)
		if count == 63 {
			compileResult(t, f.input)
		} else {
			compileReject(t, f.input, ErrCompileUnsupported)
		}
	}
	for _, count := range []int{63, 64} {
		f := compilerMinimal(t)
		rules := []any{}
		for i := 0; i < count; i++ {
			rules = append(rules, map[string]any{"id": fmt.Sprintf("r-%d", i), "match": map[string]any{"kind": "domain", "operator": "exact", "value": "example.test"}, "target": map[string]any{"kind": "reject"}})
		}
		f.intent["rules"] = rules
		f.sync(t)
		if count == 63 {
			compileResult(t, f.input)
		} else {
			compileReject(t, f.input, ErrCompileUnsupported)
		}
	}
	// Credential-bearing generated JSON may exceed64KiB with fewer than64 leaves.
	f := compilerMinimal(t)
	nodes := []map[string]any{}
	for i := 0; i < 18; i++ {
		nodes = append(nodes, map[string]any{"name": fmt.Sprintf("large-%d", i), "type": "http", "server": "edge.example.test", "port": 24001 + i, "username": "u", "password": strings.Repeat("x", 4096)})
	}
	raw := compileJSON(t, map[string]any{"proxies": nodes})
	f.input.Sources["a"] = compileSource(t, "a", raw, nil, 0x71)
	f.intent["groups"] = []any{map[string]any{"id": "all", "required": true, "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://probe.example.test/", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}}}
	f.intent["default_target"] = map[string]any{"kind": "group", "group_id": "all"}
	f.sync(t)
	compileReject(t, f.input, ErrCompile)
}
func TestCompileProviderAssetAggregateBudget(t *testing.T) {
	f := compilerMinimal(t)
	raw := compileJSON(t, map[string]any{"proxies": []map[string]any{{"name": "ordinary", "type": "http", "server": "edge.example.test", "port": 24001}}})
	raw = append(raw, bytes.Repeat([]byte(" "), 9<<20-len(raw))...)
	f.input.Sources = map[config.ID]subscription.Snapshot{}
	subscriptions := []any{}
	groups := []any{}
	for i := 0; i < 6; i++ {
		id := config.ID(fmt.Sprintf("source-%d", i))
		s := compileSource(t, id, raw, nil, 0x71)
		f.input.Sources[id] = s
		subscriptions = append(subscriptions, map[string]any{"id": id, "source": map[string]any{"kind": "file", "path": "/synthetic/provider"}, "update_hours": nil})
		groups = append(groups, map[string]any{"id": fmt.Sprintf("group-%d", i), "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": id}, "selected_node_id": s.Nodes[0].ID})
	}
	f.intent["subscriptions"] = subscriptions
	f.intent["selection"] = map[string]any{"active_subscription": nil}
	f.intent["groups"] = groups
	f.sync(t)
	compileReject(t, f.input, ErrCompileUnsupported)
}

func TestCompileMaximumStableGroupIDHasBoundedAlias(t *testing.T) {
	f := compilerMinimal(t)
	id := strings.Repeat("G", 128)
	selected := compileNode(f.input.Sources["a"], "US shared")
	f.intent["groups"] = []any{map[string]any{"id": id, "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "a"}, "selected_node_id": selected}}
	f.intent["default_target"] = map[string]any{"kind": "group", "group_id": id}
	f.sync(t)
	_, _, _, snapshot, route := compileResult(t, f.input)
	alias := compileGroupAlias(id)
	if len(alias) != 70 || compiledProxy(snapshot, alias).Selected != "node:"+string(selected) || len(route.Groups) != 1 || string(route.Groups[0].ID) != id || route.Groups[0].Alias != alias {
		t.Fatal("stable group ID truncated or alias exceeded finite wire ID")
	}
}
