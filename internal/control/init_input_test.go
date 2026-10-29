//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func initInputFixture() map[string]any {
	return map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": map[string]any{"path": "/explicit/not-installed/mihomo", "version": "1.10.0", "sha256": strings.Repeat("a", 64)}}, "subscriptions": []any{}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{map[string]any{"id": "managed-a", "mode": "managed", "metering": false, "bypass": []any{}}, map[string]any{"id": "direct-a", "mode": "direct", "metering": false, "bypass": []any{}}}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
}
func initInputJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func initInputOptions(t *testing.T) InitOptions {
	return InitOptions{MihomoPath: "/explicit/not-installed/mihomo", Intent: initInputJSON(t, initInputFixture())}
}
func initInputBad(t *testing.T, options InitOptions) {
	t.Helper()
	got, err := PrepareInitInput(options)
	if !errors.Is(err, ErrRequest) || !reflect.DeepEqual(got, InitInput{}) {
		t.Fatal("invalid input not constant error and zero result", err)
	}
}
func TestPrepareInitInputCompleteStrictDraftAndOwnership(t *testing.T) {
	options := initInputOptions(t)
	options.ControllerPort = 30001
	options.SharedPort = 30002
	options.AppPorts = map[config.ID]uint16{"managed-a": 65535}
	rawBefore := bytes.Clone(options.Intent)
	mapBefore := map[config.ID]uint16{"managed-a": 65535}
	got, err := PrepareInitInput(options)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := config.CanonicalJSON(rawBefore)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := config.ParseIntent(expected, config.IntentDraft)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.missing) != 0 || !bytes.Equal(got.canonical, expected) || !reflect.DeepEqual(got.intent, parsed) || !bytes.Equal(got.options.Intent, rawBefore) {
		t.Fatal("complete declaration changed bytes/parser semantics")
	}
	if !reflect.DeepEqual(options.AppPorts, mapBefore) || !bytes.Equal(options.Intent, rawBefore) {
		t.Fatal("caller options mutated")
	}
	options.Intent[0] = 'x'
	options.AppPorts["managed-a"] = 1
	if !bytes.Equal(got.canonical, expected) || !bytes.Equal(got.options.Intent, rawBefore) || got.options.AppPorts["managed-a"] != 65535 {
		t.Fatal("input retained caller slices/map")
	}
	got.options.Intent[0] = 'z'
	if !bytes.Equal(got.canonical, expected) || got.intent.Core.Mihomo.Path != "/explicit/not-installed/mihomo" {
		t.Fatal("owned raw/canonical/typed inputs alias")
	}
	// Draft admission remains Draft, rather than silently requiring Apply-ready
	// references during explicit local input preparation.
	draft := initInputFixture()
	draft["selection"] = map[string]any{"active_subscription": "future-source"}
	options = initInputOptions(t)
	options.Intent = initInputJSON(t, draft)
	if _, err := PrepareInitInput(options); err != nil {
		t.Fatal("valid draft reference rejected", err)
	}
}
func TestPrepareInitInputFiniteMissingMetadataNeverAuthorizesDraft(t *testing.T) {
	options := InitOptions{MihomoPath: "/explicit/not-installed/mihomo"}
	got, err := PrepareInitInput(options)
	if err != nil || !reflect.DeepEqual(got.missing, []string{"intent"}) || len(got.canonical) != 0 || got.intent.SchemaVersion != 0 {
		t.Fatal("absent input did not remain incomplete", err)
	}
	partial := map[string]any{"core": map[string]any{"mihomo": map[string]any{"path": options.MihomoPath, "version": ""}, "gost": map[string]any{"path": "/explicit/gost", "sha256": ""}}, "unvalidated_private": "private-init-input", "apps": "malformed but not claimed valid"}
	options.Intent = initInputJSON(t, partial)
	got, err = PrepareInitInput(options)
	want := []string{"mihomo_version", "mihomo_sha256", "gost_version", "gost_sha256"}
	if err != nil || !reflect.DeepEqual(got.missing, want) || len(got.canonical) != 0 || got.intent.SchemaVersion != 0 {
		t.Fatal("partial report claimed complete validation or guessed metadata", err)
	}
	options.Intent[0] = '!'
	if !bytes.HasPrefix(got.options.Intent, []byte("{")) {
		t.Fatal("partial raw input not owned")
	}
	for _, value := range []any{got, &got, got.options, &got.options} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, "private-init-input") || strings.Contains(text, "/explicit/") {
				t.Fatal("private options/input exposed through formatting")
			}
		}
	}
}
func TestPrepareInitInputBinaryPathAndMetadataShapes(t *testing.T) {
	for _, path := range []string{"", "relative", "/a/../b", "/a//b", "/a/b/", "/a\x00b", "/" + strings.Repeat("a", 4096)} {
		options := initInputOptions(t)
		options.MihomoPath = path
		initInputBad(t, options)
	}
	got, err := PrepareInitInput(InitOptions{MihomoPath: "/" + strings.Repeat("a", 4095)})
	if err != nil || len(got.missing) != 1 {
		t.Fatal("exact4096 pure path requires no filesystem validity", err)
	}
	cases := []func(map[string]any){
		func(v map[string]any) { delete(v, "core") }, func(v map[string]any) { v["core"] = nil }, func(v map[string]any) { v["core"] = []any{} },
		func(v map[string]any) { delete(v["core"].(map[string]any), "mihomo") }, func(v map[string]any) { v["core"].(map[string]any)["mihomo"] = nil },
		func(v map[string]any) { delete(v["core"].(map[string]any)["mihomo"].(map[string]any), "path") },
		func(v map[string]any) { v["core"].(map[string]any)["mihomo"].(map[string]any)["path"] = 3 },
		func(v map[string]any) { v["core"].(map[string]any)["mihomo"].(map[string]any)["path"] = "/different" },
		func(v map[string]any) { v["core"].(map[string]any)["mihomo"].(map[string]any)["version"] = nil },
		func(v map[string]any) { v["core"].(map[string]any)["mihomo"].(map[string]any)["sha256"] = true },
		func(v map[string]any) { v["core"].(map[string]any)["gost"] = nil },
	}
	for _, change := range cases {
		value := initInputFixture()
		change(value)
		options := initInputOptions(t)
		options.Intent = initInputJSON(t, value)
		initInputBad(t, options)
	}
	// Path agreement is enforced even before the incomplete-metadata return.
	partial := initInputFixture()
	core := partial["core"].(map[string]any)
	binary := core["mihomo"].(map[string]any)
	delete(binary, "version")
	binary["path"] = "/different"
	options := initInputOptions(t)
	options.Intent = initInputJSON(t, partial)
	initInputBad(t, options)
	options = initInputOptions(t)
	options.GostPath = "/explicit/gost"
	initInputBad(t, options)
	complete := initInputFixture()
	complete["core"].(map[string]any)["gost"] = map[string]any{"path": "/explicit/gost", "version": "declared-only", "sha256": strings.Repeat("b", 64)}
	options = initInputOptions(t)
	options.Intent = initInputJSON(t, complete)
	got, err = PrepareInitInput(options)
	if err != nil || got.intent.Core.Gost == nil || got.options.GostPath != "" {
		t.Fatal("omitted optional flag removed/guessed declaration", err)
	}
	options.GostPath = "/explicit/gost"
	if _, err = PrepareInitInput(options); err != nil {
		t.Fatal(err)
	}
	options.GostPath = "/different"
	initInputBad(t, options)
	options.GostPath = ""
	complete["core"].(map[string]any)["gost"].(map[string]any)["path"] = "relative"
	options.Intent = initInputJSON(t, complete)
	initInputBad(t, options)
}
func TestPrepareInitInputStrictCompleteJSONAndBounds(t *testing.T) {
	options := initInputOptions(t)
	for _, raw := range [][]byte{[]byte(""), []byte(" "), []byte("null"), []byte("[]"), []byte(`{"core":{},"core":{}}`), []byte(`{"x":"\ud800"}`), []byte("{} {}"), bytes.Repeat([]byte{' '}, MaxRequestBytes+1)} {
		if len(raw) == 0 {
			continue
		}
		options.Intent = raw
		initInputBad(t, options)
	}
	for _, change := range []func(map[string]any){func(v map[string]any) { v["unknown"] = true }, func(v map[string]any) { delete(v, "apps") }, func(v map[string]any) { v["rules"] = nil }, func(v map[string]any) { v["Schema_version"] = v["schema_version"]; delete(v, "schema_version") }, func(v map[string]any) { v["core"].(map[string]any)["mihomo"].(map[string]any)["version"] = "latest" }, func(v map[string]any) {
		v["core"].(map[string]any)["mihomo"].(map[string]any)["sha256"] = strings.Repeat("A", 64)
	}} {
		v := initInputFixture()
		change(v)
		options = initInputOptions(t)
		options.Intent = initInputJSON(t, v)
		initInputBad(t, options)
	}
}
func TestPrepareInitInputPortAndAppAdmission(t *testing.T) {
	for _, change := range []func(*InitOptions){
		func(o *InitOptions) { o.ControllerPort = 1 }, func(o *InitOptions) { o.SharedPort = 1 }, func(o *InitOptions) { o.ControllerPort = 1; o.SharedPort = 1 },
		func(o *InitOptions) {
			o.ControllerPort = 1
			o.SharedPort = 2
			o.AppPorts = map[config.ID]uint16{"managed-a": 1}
		},
		func(o *InitOptions) { o.AppPorts = map[config.ID]uint16{"managed-a": 0} }, func(o *InitOptions) { o.AppPorts = map[config.ID]uint16{"../bad": 3} },
		func(o *InitOptions) { o.AppPorts = map[config.ID]uint16{"unknown": 3} }, func(o *InitOptions) { o.AppPorts = map[config.ID]uint16{"direct-a": 3} },
		func(o *InitOptions) { o.AppPorts = map[config.ID]uint16{"managed-a": 3, "direct-a": 3} },
	} {
		options := initInputOptions(t)
		change(&options)
		initInputBad(t, options)
	}
	options := initInputOptions(t)
	value := initInputFixture()
	value["apps"].([]any)[0].(map[string]any)["metering"] = true
	options.Intent = initInputJSON(t, value)
	got, err := PrepareInitInput(options)
	if err != nil || !got.intent.Apps[0].Metering {
		t.Fatal("declared metering must be reported by stateful layer, not disabled", err)
	}
	options.AppPorts = map[config.ID]uint16{"managed-a": 3}
	initInputBad(t, options)
	options = initInputOptions(t)
	options.ControllerPort = 65534
	options.SharedPort = 65535
	if _, err := PrepareInitInput(options); err != nil {
		t.Fatal("valid explicit pair refused", err)
	}
}

func TestPrepareInitInputLimitsBeforeOwnershipEvenWithoutIntent(t *testing.T) {
	options := InitOptions{MihomoPath: "/explicit/not-installed/mihomo", AppPorts: map[config.ID]uint16{}}
	for i := 0; i < config.MaxAppBindings; i++ {
		options.AppPorts[config.ID(fmt.Sprintf("app-%d", i))] = uint16(i + 1)
	}
	got, err := PrepareInitInput(options)
	if err != nil || len(got.options.AppPorts) != config.MaxAppBindings || !reflect.DeepEqual(got.missing, []string{"intent"}) {
		t.Fatal("exact bounded incomplete map refused", err)
	}
	options.AppPorts["one-too-many"] = 65535
	initInputBad(t, options)
	partial := []byte(`{"core":{"mihomo":{"path":"/explicit/not-installed/mihomo"}}}`)
	options = InitOptions{MihomoPath: "/explicit/not-installed/mihomo", Intent: append(partial, bytes.Repeat([]byte{' '}, MaxRequestBytes-len(partial))...)}
	if _, err := PrepareInitInput(options); err != nil {
		t.Fatal("exact raw byte bound refused", err)
	}
	options.Intent = append(options.Intent, ' ')
	initInputBad(t, options)
}
