//go:build linux

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type planDigestFixture struct {
	Name   string `json:"name"`
	Raw    string `json:"raw"`
	SHA256 string `json:"sha256"`
}

func planDigestFixtures(t *testing.T) []planDigestFixture {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/control/plan-digest-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int                 `json:"schema_version"`
		Source        string              `json:"source"`
		Cases         []planDigestFixture `json:"cases"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatal("fixture trailing content", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Source == "" || len(fixture.Cases) != 3 {
		t.Fatal("fixture contract")
	}
	names := []string{"blocked-draft-plan", "top-only-exclusion-primitive", "utf16-and-string-primitive"}
	for i, c := range fixture.Cases {
		if c.Name != names[i] || c.Raw == "" || !isHash(c.SHA256) {
			t.Fatal("invalid finite vector", i)
		}
	}
	return fixture.Cases
}
func planFixture(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	cases := planDigestFixtures(t)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cases[0].Raw), &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "plan_digest")
	return obj
}
func planRaw(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

// This validator recognizes only these exact codec fixtures. It is not an Intent compiler.
func planFixtureValidator(t *testing.T, obj map[string]json.RawMessage) IntentValidator {
	t.Helper()
	want := append([]byte(nil), obj["intent"]...)
	var inputs []PlanInput
	if err := json.Unmarshal(obj["inputs"], &inputs); err != nil {
		t.Fatal(err)
	}
	return func(raw json.RawMessage, got []PlanInput) error {
		var a, b any
		json.Unmarshal(want, &a)
		json.Unmarshal(raw, &b)
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(inputs, got) {
			return errors.New("fixture mismatch")
		}
		return nil
	}
}
func TestPlanOracle(t *testing.T) {
	cases := planDigestFixtures(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, e := ComputePlanDigest([]byte(c.Raw))
			if e != nil || got != c.SHA256 {
				t.Fatalf("digest %s %v", got, e)
			}
		})
	}
	obj := planFixture(t)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil || p.Digest() != cases[0].SHA256 {
		t.Fatalf("build %s %v", p.Digest(), e)
	}
	q, e := ParsePlan(p.Bytes(), planFixtureValidator(t, obj))
	if e != nil || !bytes.Equal(p.Bytes(), q.Bytes()) {
		t.Fatal("round trip", e)
	}
}
func TestPlanBindingAndOrder(t *testing.T) {
	base := planFixture(t)
	p, e := BuildPlan(planRaw(t, base), planFixtureValidator(t, base))
	if e != nil {
		t.Fatal(e)
	}
	changes := map[string]json.RawMessage{"base_revision": json.RawMessage(`"r2"`), "compiler_version": json.RawMessage(`"another"`), "capabilities_digest": planRaw(t, strings.Repeat("c", 64)), "intent": json.RawMessage(`{"fixture":"distinct"}`), "blockers": json.RawMessage(`[]`), "impact": json.RawMessage(`{"requires_disruption":true,"affected_apps":["app"],"previous_generation":"g1","target_generation":"g3","connection_effect":"interrupt","runtime_action":"replace"}`)}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			obj := planFixture(t)
			obj[key] = value
			q, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil || q.Digest() == p.Digest() {
				t.Fatal("binding", e)
			}
		})
	}
	for _, kind := range []string{"provider", "core_binary", "capabilities", "rule_asset"} {
		t.Run(kind, func(t *testing.T) {
			obj := planFixture(t)
			obj["inputs"] = planRaw(t, []PlanInput{{Kind: kind, ID: "source", SHA256: strings.Repeat("c", 64)}})
			q, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil || q.Digest() == p.Digest() {
				t.Fatal(e)
			}
		})
	}
	obj := planFixture(t)
	var in []PlanInput
	json.Unmarshal(obj["inputs"], &in)
	in[0], in[1] = in[1], in[0]
	obj["inputs"] = planRaw(t, in)
	q, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil || q.Digest() == p.Digest() {
		t.Fatal("array order", e)
	}
	a, _ := ComputePlanDigest([]byte(`{"b":2,"a":1}`))
	b, _ := ComputePlanDigest([]byte(`{"a":1,"b":2,"plan_digest":42}`))
	if a != b {
		t.Fatal("key order/top exclusion")
	}
}
func TestPlanStrict(t *testing.T) {
	for _, key := range []string{"schema_version", "base_revision", "compiler_version", "capabilities_digest", "intent", "inputs", "impact", "blockers"} {
		t.Run("missing-"+key, func(t *testing.T) {
			obj := planFixture(t)
			delete(obj, key)
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, planFixture(t))); e != ErrPlan {
				t.Fatal(e)
			}
		})
		t.Run("null-"+key, func(t *testing.T) {
			obj := planFixture(t)
			obj[key] = json.RawMessage(`null`)
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, planFixture(t))); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	bad := []string{`[]`, `null`, `{} {}`, `{"a":1,"\u0061":2}`, `{"a":"\ud800"}`, "{\"a\":\"\xff\"}", `{"n":9007199254740992}`, `{"n":1e999}`}
	for i, raw := range bad {
		t.Run(fmt.Sprint("json-", i), func(t *testing.T) {
			if _, e := ComputePlanDigest([]byte(raw)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	variants := map[string]json.RawMessage{"schema_version": json.RawMessage(`2`), "base_revision": json.RawMessage(`"bad/id"`), "compiler_version": planRaw(t, strings.Repeat("é", 65)), "capabilities_digest": json.RawMessage(`"bad"`), "inputs": json.RawMessage(`[]`), "blockers": json.RawMessage(`[{"code":"x","message":""}]`), "extra": json.RawMessage(`true`)}
	for key, value := range variants {
		t.Run("bad-"+key, func(t *testing.T) {
			obj := planFixture(t)
			obj[key] = value
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	obj := planFixture(t)
	var inputs []PlanInput
	json.Unmarshal(obj["inputs"], &inputs)
	inputs = append(inputs, inputs[0])
	obj["inputs"] = planRaw(t, inputs)
	if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
		t.Fatal("duplicate", e)
	}
}
func TestPlanValidatorCopyAndDisruption(t *testing.T) {
	obj := planFixture(t)
	raw := planRaw(t, obj)
	if _, e := BuildPlan(raw, nil); e != ErrPlan {
		t.Fatal(e)
	}
	if _, e := BuildPlan(raw, func(json.RawMessage, []PlanInput) error { return errors.New("SECRET") }); e != ErrPlan {
		t.Fatal(e)
	}
	verify := planFixtureValidator(t, obj)
	p, e := BuildPlan(raw, func(b json.RawMessage, in []PlanInput) error {
		if e := verify(b, in); e != nil {
			return e
		}
		b[0] = '!'
		in[0].ID = "changed"
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	original := p.Bytes()
	b := p.Bytes()
	b[0] = '!'
	d := p.Document()
	d.Intent[0] = '!'
	d.Inputs[0].ID = "changed"
	d.Blockers[0].Message = "changed"
	if !bytes.Equal(p.Bytes(), original) {
		t.Fatal("alias")
	}
	if strings.Contains(fmt.Sprintf("%v %#v", p, p), "fixture") || strings.Contains(fmt.Sprintf("%v", p), "待选择") {
		t.Fatal("format leak")
	}
	var zero Plan
	if zero.Bytes() != nil || zero.Digest() != "" || zero.Document().SchemaVersion != 0 {
		t.Fatal("zero")
	}
	for _, effect := range []string{"unknown", "interrupt"} {
		t.Run(effect, func(t *testing.T) {
			obj := planFixture(t)
			var impact map[string]any
			json.Unmarshal(obj["impact"], &impact)
			impact["requires_disruption"] = false
			impact["connection_effect"] = effect
			obj["impact"] = planRaw(t, impact)
			p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil || !p.Document().Impact.RequiresDisruption {
				t.Fatal(e)
			}
			var all map[string]json.RawMessage
			json.Unmarshal(p.Bytes(), &all)
			all["impact"] = obj["impact"]
			digest, _ := ComputePlanDigest(planRaw(t, all))
			all["plan_digest"] = planRaw(t, digest)
			if _, e := ParsePlan(planRaw(t, all), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal("parse repaired", e)
			}
		})
	}
}
func TestPlanLimitsAndPurity(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/not-created"
	t.Setenv("XDG_STATE_HOME", path)
	before := os.Environ()
	obj := planFixture(t)
	obj["compiler_version"] = planRaw(t, strings.Repeat("é", 64))
	obj["blockers"] = planRaw(t, []PlanBlocker{{Code: strings.Repeat("é", 64), Message: strings.Repeat("é", 2048)}})
	if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("filesystem side effect", e)
	}
	if !reflect.DeepEqual(before, os.Environ()) {
		t.Fatal("environment side effect")
	}
	if _, e := ComputePlanDigest(bytes.Repeat([]byte(" "), (128<<10)+1)); e != ErrPlan {
		t.Fatal(e)
	}
	// Input fits, but JCS expands each short exponent to a longer fixed-point value.
	raw := []byte(`{"n":[` + strings.Repeat("1e-6,", 21000) + `0]}`)
	if len(raw) > 128<<10 {
		t.Fatal("fixture")
	}
	if _, e := ComputePlanDigest(raw); e != ErrPlan {
		t.Fatal("output budget", e)
	}
}

func TestPlanNestedStrictness(t *testing.T) {
	cases := map[string]string{
		"input-kind":    `[{"kind":"other","id":"a","sha256":"HASH"}]`,
		"input-id":      `[{"kind":"provider","id":"a/b","sha256":"HASH"}]`,
		"input-sha":     `[{"kind":"provider","id":"a","sha256":"AASH"}]`,
		"input-extra":   `[{"kind":"provider","id":"a","sha256":"HASH","extra":1}]`,
		"input-missing": `[{"kind":"provider","id":"a"}]`,
		"input-null":    `[{"kind":"provider","id":null,"sha256":"HASH"}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			obj := planFixture(t)
			obj["inputs"] = json.RawMessage(strings.ReplaceAll(raw, "HASH", strings.Repeat("a", 64)))
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	for _, key := range []string{"requires_disruption", "affected_apps", "previous_generation", "target_generation", "connection_effect", "runtime_action"} {
		t.Run("impact-missing-"+key, func(t *testing.T) {
			obj := planFixture(t)
			var impact map[string]json.RawMessage
			json.Unmarshal(obj["impact"], &impact)
			delete(impact, key)
			obj["impact"] = planRaw(t, impact)
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	for name, change := range map[string]map[string]any{
		"apps-null": {"affected_apps": nil}, "apps-duplicate": {"affected_apps": []string{"a", "a"}}, "apps-invalid": {"affected_apps": []string{"a/b"}}, "effect": {"connection_effect": "maybe"}, "action": {"runtime_action": "reload"}, "previous": {"previous_generation": "bad/id"}, "extra": {"extra": false}, "requires-null": {"requires_disruption": nil},
	} {
		t.Run(name, func(t *testing.T) {
			obj := planFixture(t)
			var impact map[string]any
			json.Unmarshal(obj["impact"], &impact)
			for k, v := range change {
				impact[k] = v
			}
			obj["impact"] = planRaw(t, impact)
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	for name, blocks := range map[string]any{"null-item": []any{nil}, "missing": []any{map[string]any{"code": "a"}}, "null-code": []any{map[string]any{"code": nil, "message": "a"}}, "extra": []any{map[string]any{"code": "a", "message": "a", "extra": false}}, "code-size": []PlanBlocker{{Code: strings.Repeat("a", 129), Message: "a"}}, "message-size": []PlanBlocker{{Code: "a", Message: strings.Repeat("é", 2049)}}} {
		t.Run("block-"+name, func(t *testing.T) {
			obj := planFixture(t)
			obj["blockers"] = planRaw(t, blocks)
			if _, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
	obj := planFixture(t)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := BuildPlan(p.Bytes(), planFixtureValidator(t, obj)); e != ErrPlan {
		t.Fatal("Build accepted digest", e)
	}
	if _, e := ParsePlan(planRaw(t, obj), planFixtureValidator(t, obj)); e != ErrPlan {
		t.Fatal("Parse missing digest", e)
	}
	obj["plan_digest"] = planRaw(t, strings.Repeat("0", 64))
	if _, e := ParsePlan(planRaw(t, obj), planFixtureValidator(t, planFixture(t))); e != ErrPlan {
		t.Fatal("wrong digest", e)
	}
}

func TestPlanEveryInputAndImpactBinding(t *testing.T) {
	for _, kind := range []string{"provider", "core_binary", "capabilities", "rule_asset"} {
		t.Run(kind, func(t *testing.T) {
			obj := planFixture(t)
			input := []PlanInput{{Kind: kind, ID: "a", SHA256: strings.Repeat("a", 64)}}
			obj["inputs"] = planRaw(t, input)
			p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil {
				t.Fatal(e)
			}
			for _, field := range []string{"id", "sha256"} {
				t.Run(field, func(t *testing.T) {
					changed := append([]PlanInput(nil), input...)
					if field == "id" {
						changed[0].ID = "b"
					} else {
						changed[0].SHA256 = strings.Repeat("b", 64)
					}
					obj["inputs"] = planRaw(t, changed)
					q, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
					if e != nil || p.Digest() == q.Digest() {
						t.Fatal("not bound", e)
					}
				})
			}
		})
	}
	changes := map[string]any{"requires_disruption": false, "affected_apps": []string{"a", "b"}, "previous_generation": "g1", "target_generation": "g3", "connection_effect": "interrupt", "runtime_action": "none"}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			obj := planFixture(t)
			var impact map[string]any
			json.Unmarshal(obj["impact"], &impact)
			impact["connection_effect"] = "preserve_old"
			obj["impact"] = planRaw(t, impact)
			p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil {
				t.Fatal(e)
			}
			impact[key] = value
			obj["impact"] = planRaw(t, impact)
			q, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
			if e != nil || q.Digest() == p.Digest() {
				t.Fatal("impact not bound", e)
			}
		})
	}
}

func TestPlanCanonicalBudgetAndUnicode(t *testing.T) {
	// Generic digest keys may themselves contain LS/PS or short JSON escapes.
	for _, unit := range []string{"\u2028", "\b"} {
		t.Run(fmt.Sprintf("key-%U", []rune(unit)[0]), func(t *testing.T) {
			key := strings.Repeat(unit, 30000)
			raw := planRaw(t, map[string]any{key: 0})
			raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
			if len(raw) > 128<<10 {
				t.Fatal("fixture")
			}
			if _, e := ComputePlanDigest(raw); e != nil {
				t.Fatal(e)
			}
		})
	}
	obj := planFixture(t)
	obj["intent"] = json.RawMessage(`{"fixture":"` + strings.Repeat("\u2028", 35000) + `"}`)
	raw := planRaw(t, obj)
	raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
	p, e := BuildPlan(raw, planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(p.Bytes(), []byte(`\u2028`)) {
		t.Fatal("secondary escaping")
	}
	if _, e := ParsePlan(p.Bytes(), planFixtureValidator(t, obj)); e != nil {
		t.Fatal(e)
	}
	// A raw eight-field object may fit while the required digest makes it too large.
	obj = planFixture(t)
	obj["intent"] = json.RawMessage(`{"fixture":"x"}`)
	raw = planRaw(t, obj)
	padding := (128 << 10) - len(raw)
	obj["intent"] = json.RawMessage(`{"fixture":"x` + strings.Repeat("x", padding) + `"}`)
	raw = planRaw(t, obj)
	if len(raw) != 128<<10 {
		t.Fatal("boundary fixture", len(raw))
	}
	if _, e := BuildPlan(raw, planFixtureValidator(t, obj)); e != ErrPlan {
		t.Fatal("final budget", e)
	}
	for name, raw := range map[string]string{"deep": `{"x":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`, "escaped-duplicate": `{"nested":{"a":0,"\u0061":1}}`} {
		t.Run(name, func(t *testing.T) {
			if _, e := ComputePlanDigest([]byte(raw)); e != ErrPlan {
				t.Fatal(e)
			}
		})
	}
}
