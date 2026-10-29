//go:build linux && amd64

package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	profile "hoopa/protocol/sourceprofile"
)

func appCompileFixture(t *testing.T) compileFixture {
	t.Helper()
	f := newCompileFixture(t)
	cap, e := mihomo.ApprovedAppRoutingCapability("/synthetic/uninstalled-app-core")
	if e != nil {
		t.Fatal(e)
	}
	f.input.Capability = cap
	compileObj(f.intent["core"])["mihomo"] = cap.Binary
	f.input.SourceSettingsSHA256 = strings.Repeat("c", 64)
	f.input.Runtime.AppEntries = map[config.ID]profile.Entry{}
	f.input.AppBindings = map[config.ID][]byte{}
	f.intent["apps"] = []any{map[string]any{"id": "beta", "mode": "managed", "metering": false, "bypass": []any{}}, map[string]any{"id": "alpha", "mode": "managed", "metering": false, "bypass": []any{}}, map[string]any{"id": "metadata-direct", "mode": "direct", "metering": false, "bypass": []any{}}, map[string]any{"id": "metadata-inherit", "mode": "inherit", "metering": false, "bypass": []any{}}}
	for i, id := range []config.ID{"alpha", "beta"} {
		entry := profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: uint16(32001 + i), Users: []profile.User{{Username: string(id) + "-user", Password: strings.Repeat(string(rune('a'+i)), 32)}}}
		appCompilePutBinding(t, &f, id, entry, f.input.SourceSettingsSHA256)
	}
	f.intent["rules"] = []any{map[string]any{"id": "domain-first", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "EXAMPLE.TEST."}, "target": map[string]any{"kind": "group", "group_id": "US"}}, map[string]any{"id": "beta-app", "match": map[string]any{"kind": "app", "app_id": "beta"}, "target": map[string]any{"kind": "group", "group_id": "JP"}}, map[string]any{"id": "ordered-all", "match": map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "domain", "operator": "suffix", "value": "SUFFIX.TEST."}, map[string]any{"kind": "app", "app_id": "alpha"}, map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "ip_cidr", "value": "192.0.2.129/24"}, map[string]any{"kind": "domain", "operator": "exact", "value": "one.test"}}}}}, "target": map[string]any{"kind": "node", "subscription_id": "a", "node_id": compileNode(f.input.Sources["a"], "US shared")}}}
	f.sync(t)
	return f
}
func appCompilePutBinding(t *testing.T, f *compileFixture, id config.ID, entry profile.Entry, parent string) {
	t.Helper()
	binding, e := config.NewAppBinding(id, parent, entry)
	if e != nil {
		t.Fatal("binding fixture", e)
	}
	entry, e = binding.SensitiveEntry()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := binding.SensitiveBytes()
	if e != nil {
		t.Fatal(e)
	}
	f.input.Runtime.AppEntries[id] = entry
	f.input.AppBindings[id] = raw
}
func appCompileConfig(t *testing.T, files map[string][]byte) profile.Config {
	t.Helper()
	cfg, e := profile.Validate(files["core.json"], "generation:compile")
	if e != nil {
		t.Fatal("generated app profile not strict", e)
	}
	return cfg
}
func appCompileApp(f *compileFixture, id string) map[string]any {
	for _, v := range f.intent["apps"].([]any) {
		a := compileObj(v)
		if a["id"] == id {
			return a
		}
	}
	panic("app fixture")
}

func TestAppCompileOrderedEntriesRulesAndPrivateCommitments(t *testing.T) {
	f := appCompileFixture(t)
	c, m, files, snapshot, route := compileResult(t, f.input)
	cfg := appCompileConfig(t, files)
	if m.CompilerVersion != "hoopa.apps.v1" || AppCompilerVersion != "hoopa.apps.v1" {
		t.Fatal("wrong app compiler identity")
	}
	if AppEntryAlias("alpha") != config.AppEntryAlias("alpha") {
		t.Fatal("policy alias bridge differs")
	}
	wantEntries := []profile.Entry{f.input.Runtime.Entry, f.input.Runtime.AppEntries["alpha"], f.input.Runtime.AppEntries["beta"]}
	if !reflect.DeepEqual(cfg.Listeners, wantEntries) {
		t.Fatal("shared-first/app-ID-sorted or exact entry material lost")
	}
	wantRules := []string{"DOMAIN,example.test," + compileGroupAlias("US"), "IN-NAME,app:" + compileSHA([]byte("beta")) + "," + compileGroupAlias("JP"), "AND,((DOMAIN-SUFFIX,suffix.test),(IN-NAME,app:" + compileSHA([]byte("alpha")) + "),(IP-CIDR,192.0.2.0/24),(DOMAIN,one.test)),node:" + string(compileNode(f.input.Sources["a"], "US shared")), "MATCH," + compileGroupAlias("BEST-follow")}
	if !reflect.DeepEqual(cfg.Rules, wantRules) {
		t.Fatal("ordered rule/flatten AND sequence changed", cfg.Rules, wantRules)
	}
	if snapshot.Rules[1].Type != "InName" || snapshot.Rules[1].Payload != config.AppEntryAlias("beta") || snapshot.Rules[2].Type != "AND" {
		t.Fatal("independently decoded snapshot lost app atom")
	}
	wantApps := []RouteApp{{ID: "alpha", Entry: config.AppEntryAlias("alpha")}, {ID: "beta", Entry: config.AppEntryAlias("beta")}}
	if !reflect.DeepEqual(route.Apps, wantApps) {
		t.Fatal("safe route app mapping missing or unsorted")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(files["core.json"], &root) != nil {
		t.Fatal("config JSON")
	}
	var listeners []map[string]any
	_ = json.Unmarshal(root["listeners"], &listeners)
	for _, entry := range listeners {
		if _, ok := entry["proxy"]; ok {
			t.Fatal("listener overrides ordered routing")
		}
	}
	if bytes.Contains(files["route-map.json"], []byte("alpha-user")) || bytes.Contains(files["route-map.json"], []byte(strings.Repeat("a", 32))) {
		t.Fatal("route app projection leaked credentials")
	}
	inputs := map[string]config.BundleInput{}
	for _, in := range c.Inputs() {
		inputs[in.Kind+"/"+in.ID] = in
	}
	assetNames := map[string]config.BundleFile{}
	total := int64(0)
	for _, file := range m.Files {
		assetNames[file.Name] = file
		total += file.Bytes
		if file.SHA256 != compileSHA(files[file.Name]) || file.Bytes != int64(len(files[file.Name])) {
			t.Fatal("manifest does not bind actual bytes")
		}
	}
	if total > config.MaxBundleBytes {
		t.Fatal("bundle budget escaped")
	}
	for _, id := range []config.ID{"alpha", "beta"} {
		name, e := config.AppBindingFilename(id)
		if e != nil {
			t.Fatal(e)
		}
		raw := f.input.AppBindings[id]
		if !bytes.Equal(files[name], raw) || assetNames[name].Kind != "app_binding" {
			t.Fatal("complete canonical private binding omitted")
		}
		in, ok := inputs["app_binding/"+name]
		if !ok || in.SHA256 != compileSHA(raw) {
			t.Fatal("binding input not actual canonical bytes")
		}
		if decoded, e := config.DecodeAppBinding(files[name]); e != nil || decoded.AppID() != id || decoded.SettingsSHA256() != f.input.SourceSettingsSHA256 {
			t.Fatal("bundle binding parent/application authority lost", e)
		}
	}
	if len(route.Rules) != 3 || route.Rules[1].ID != "beta-app" || route.Rules[1].Index != 1 {
		t.Fatal("route rule identity/order changed")
	}
	if c.ValidateIntent(f.input.Intent, c.Inputs()) != nil {
		t.Fatal("valid complete input set refused")
	}
}

func TestAppCompileBindingMaterialFailClosed(t *testing.T) {
	cases := map[string]func(*compileFixture){
		"missing-record": func(f *compileFixture) { delete(f.input.AppBindings, "alpha") }, "missing-entry": func(f *compileFixture) { delete(f.input.Runtime.AppEntries, "alpha") }, "missing-parent": func(f *compileFixture) { f.input.SourceSettingsSHA256 = "" }, "wrong-parent": func(f *compileFixture) { f.input.SourceSettingsSHA256 = strings.Repeat("d", 64) },
		"record-other-parent": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			appCompilePutBinding(t, f, "alpha", entry, strings.Repeat("d", 64))
		},
		"same-record-changed-port": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Port++
			f.input.Runtime.AppEntries["alpha"] = entry
		}, "same-record-changed-auth": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Users = append([]profile.User{}, entry.Users...)
			entry.Users[0].Password = strings.Repeat("x", 32)
			f.input.Runtime.AppEntries["alpha"] = entry
		}, "entry-name-empty": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Name = ""
			f.input.Runtime.AppEntries["alpha"] = entry
		},
		"record-key-swap": func(f *compileFixture) {
			f.input.AppBindings["alpha"], f.input.AppBindings["beta"] = f.input.AppBindings["beta"], f.input.AppBindings["alpha"]
		}, "unknown-record-field": func(f *compileFixture) {
			raw := f.input.AppBindings["alpha"]
			f.input.AppBindings["alpha"] = append([]byte(`{"unknown":true,`), raw[1:]...)
		}, "overlimit-record": func(f *compileFixture) { f.input.AppBindings["alpha"] = make([]byte, config.MaxAppBindingBytes+1) },
		"undeclared-record": func(f *compileFixture) { f.input.AppBindings["undeclared"] = f.input.AppBindings["alpha"] }, "undeclared-entry": func(f *compileFixture) {
			f.input.Runtime.AppEntries["undeclared"] = f.input.Runtime.AppEntries["alpha"]
		}, "nonmanaged-record": func(f *compileFixture) { f.input.AppBindings["metadata-direct"] = f.input.AppBindings["alpha"] }, "nonmanaged-entry": func(f *compileFixture) {
			f.input.Runtime.AppEntries["metadata-inherit"] = f.input.Runtime.AppEntries["alpha"]
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := appCompileFixture(t)
			edit(&f)
			f.sync(t)
			compileReject(t, f.input, ErrCompile)
		})
	}
}

func TestAppCompileFullProfileCollisionAndCounts(t *testing.T) {
	cases := map[string]func(*compileFixture){
		"shared-port": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Port = f.input.Runtime.Entry.Port
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"controller-port": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Port = 31000
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"app-port": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Port = f.input.Runtime.AppEntries["beta"].Port
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"shared-auth-pair": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Users = append([]profile.User{}, f.input.Runtime.Entry.Users...)
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"app-auth-pair": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Users = append([]profile.User{}, f.input.Runtime.AppEntries["beta"].Users...)
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"controller-secret": func(f *compileFixture) {
			entry := f.input.Runtime.AppEntries["alpha"]
			entry.Users = append([]profile.User{}, entry.Users...)
			entry.Users[0].Password = f.input.Runtime.Secret
			appCompilePutBinding(t, f, "alpha", entry, f.input.SourceSettingsSHA256)
		},
		"shared-name": func(f *compileFixture) { f.input.Runtime.Entry.Name = config.AppEntryAlias("alpha") },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f := appCompileFixture(t)
			edit(&f)
			f.sync(t)
			compileReject(t, f.input, ErrCompile)
		})
	}
	for _, count := range []int{3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := appCompileFixture(t)
			for i := 2; i < count; i++ {
				id := config.ID(fmt.Sprintf("app-%d", i))
				f.intent["apps"] = append(f.intent["apps"].([]any), map[string]any{"id": id, "mode": "managed", "metering": false, "bypass": []any{}})
				entry := profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: uint16(32001 + i), Users: []profile.User{{Username: fmt.Sprintf("user-%d", i), Password: strings.Repeat(fmt.Sprint(i), 32)}}}
				appCompilePutBinding(t, &f, id, entry, f.input.SourceSettingsSHA256)
			}
			f.sync(t)
			if count == 4 {
				compileReject(t, f.input, ErrCompileUnsupported)
			} else {
				_, _, files, _, route := compileResult(t, f.input)
				if len(appCompileConfig(t, files).Listeners) != 4 || len(route.Apps) != 3 {
					t.Fatal("unreferenced declared app omitted")
				}
			}
		})
	}
}

func TestAppCompileUnsupportedCannotHideInAll(t *testing.T) {
	for _, predicate := range []map[string]any{{"kind": "process_name", "value": "worker"}, {"kind": "process_path", "value": "/opt/worker"}, {"kind": "process_instance", "process_instance_id": "process-A"}, {"kind": "app", "app_id": "metadata-direct"}, {"kind": "app", "app_id": "metadata-inherit"}} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-%v-%v", predicate["kind"], predicate["app_id"], reverse), func(t *testing.T) {
				f := appCompileFixture(t)
				conditions := []any{map[string]any{"kind": "domain", "operator": "exact", "value": "never.test"}, map[string]any{"kind": "all", "conditions": []any{predicate}}}
				if reverse {
					conditions[0], conditions[1] = conditions[1], conditions[0]
				}
				f.intent["rules"] = []any{map[string]any{"id": "unsupported", "match": map[string]any{"kind": "all", "conditions": conditions}, "target": map[string]any{"kind": "reject"}}}
				f.sync(t)
				compileReject(t, f.input, ErrCompileUnsupported)
			})
		}
	}
	f := appCompileFixture(t)
	appCompileApp(&f, "alpha")["metering"] = true
	f.sync(t)
	compileReject(t, f.input, ErrCompileUnsupported)
}

func TestAppCompileDeterminismOwnershipAndBindingChanges(t *testing.T) {
	f := appCompileFixture(t)
	first, m, files, _, _ := compileResult(t, f.input)
	entries := map[config.ID]profile.Entry{}
	bindings := map[config.ID][]byte{}
	for _, id := range []config.ID{"beta", "alpha"} {
		entries[id] = f.input.Runtime.AppEntries[id]
		bindings[id] = bytes.Clone(f.input.AppBindings[id])
	}
	f.input.Runtime.AppEntries = entries
	f.input.AppBindings = bindings
	second, m2, files2, _, _ := compileResult(t, f.input)
	if !reflect.DeepEqual(m, m2) || !reflect.DeepEqual(files, files2) || !reflect.DeepEqual(first.Inputs(), second.Inputs()) {
		t.Fatal("map iteration changed complete output")
	}
	entry := entries["alpha"]
	entry.Port = 32010
	appCompilePutBinding(t, &f, "alpha", entry, f.input.SourceSettingsSHA256)
	changed, m3, files3, _, _ := compileResult(t, f.input)
	if bytes.Equal(files["core.json"], files3["core.json"]) || reflect.DeepEqual(first.Inputs(), changed.Inputs()) || reflect.DeepEqual(m, m3) || first.ValidateIntent(f.input.Intent, changed.Inputs()) != ErrCompile {
		t.Fatal("changed actual binding not committed")
	}
	// Caller bytes/entry slices and returned files/manifests/inputs do not alias.
	for _, raw := range f.input.AppBindings {
		for i := range raw {
			raw[i] = 'x'
		}
	}
	entries["alpha"].Users[0].Password = "caller changed"
	for name := range files3 {
		files3[name][0] = 'x'
	}
	m3.Files[0].SHA256 = "changed"
	ins := changed.Inputs()
	ins[0].SHA256 = "changed"
	ownedM, ownedFiles := changed.Bundle()
	if ownedM.Files[0].SHA256 == "changed" || changed.Inputs()[0].SHA256 == "changed" || len(ownedFiles["core.json"]) == 0 || ownedFiles["core.json"][0] == 'x' {
		t.Fatal("compiled material alias escaped")
	}
	name, _ := config.AppBindingFilename("alpha")
	if _, e := config.DecodeAppBinding(ownedFiles[name]); e != nil {
		t.Fatal("caller raw mutation rewrote retained private record")
	}
}

func TestAppCompileBypassRemainsMetadata(t *testing.T) {
	f := appCompileFixture(t)
	original, _, before, _, _ := compileResult(t, f.input)
	appCompileApp(&f, "alpha")["bypass"] = []any{"localhost", "127.0.0.1", "*.internal.test"}
	f.sync(t)
	_, _, after, _, _ := compileResult(t, f.input)
	if !bytes.Equal(before["core.json"], after["core.json"]) || !bytes.Equal(before["route-map.json"], after["route-map.json"]) {
		t.Fatal("bypass metadata changed rules or entry routing")
	}
	if original.ValidateIntent(f.input.Intent, original.Inputs()) != ErrCompile {
		t.Fatal("changed canonical bypass metadata escaped intent identity")
	}
}

func TestAppCompileProfileBoundaryAndLegacyGolden(t *testing.T) {
	// Existing frozen diagnostic golden binds every old no-app file and input.
	t.Run("old-source-golden", TestDiagnosticValidGenerationGolden)
	for _, metadataOnly := range []bool{false, true} {
		f := appCompileFixture(t)
		f.intent["apps"] = []any{}
		if metadataOnly {
			f.intent["apps"] = []any{map[string]any{"id": "metadata", "mode": "direct", "metering": false, "bypass": []any{}}}
		}
		f.intent["rules"] = []any{}
		f.input.Runtime.AppEntries = nil
		f.input.AppBindings = nil
		f.input.SourceSettingsSHA256 = "unused"
		f.sync(t)
		_, m, files, _, route := compileResult(t, f.input)
		if m.CompilerVersion != AppCompilerVersion || len(appCompileConfig(t, files).Listeners) != 1 || len(route.Apps) != 0 || bytes.Contains(files["route-map.json"], []byte(`"apps"`)) {
			t.Fatal("empty/nonmanaged app profile wrong identity/listeners")
		}
	}
	f := appCompileFixture(t)
	old, e := mihomo.ApprovedSourcesCapability("/synthetic/old-source-core")
	if e != nil {
		t.Fatal(e)
	}
	f.input.Capability = old
	compileObj(f.intent["core"])["mihomo"] = old.Binary
	f.sync(t)
	compileReject(t, f.input, ErrCompileUnsupported)
	f = newCompileFixture(t)
	f.input.SourceSettingsSHA256 = "unused"
	_, m, files, _, _ := compileResult(t, f.input)
	if m.CompilerVersion != CompilerVersion || bytes.Contains(files["route-map.json"], []byte(`"apps"`)) {
		t.Fatal("legacy no-app serialization changed")
	}
	f.input.AppBindings = map[config.ID][]byte{"extra": []byte("secret")}
	compileReject(t, f.input, ErrCompile)
	// Pure compilation checks shape only; exact approved registry remains US5.
	f = appCompileFixture(t)
	f.input.Capability.Binary.SHA256 = strings.Repeat("e", 64)
	f.input.Capability.EvidenceSHA256 = strings.Repeat("f", 64)
	compileObj(f.intent["core"])["mihomo"] = f.input.Capability.Binary
	f.sync(t)
	_, m, _, _, _ = compileResult(t, f.input)
	if m.Binaries[0].SHA256 != strings.Repeat("e", 64) {
		t.Fatal("shape fixture unexpectedly uses installed-core authority")
	}
}
