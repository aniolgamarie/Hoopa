//go:build linux

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"

	"hoopa/internal/config"
)

// ErrPlan never includes source material or validator diagnostics.
var ErrPlan = errors.New("INVALID_PLAN")

const maxPlanBytes = 128 << 10

type PlanInput struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type PlanImpact struct {
	RequiresDisruption bool               `json:"requires_disruption"`
	AffectedApps       []string           `json:"affected_apps"`
	PreviousGeneration *string            `json:"previous_generation"`
	TargetGeneration   string             `json:"target_generation"`
	ConnectionEffect   string             `json:"connection_effect"`
	RuntimeAction      string             `json:"runtime_action"`
	Details            *PlanImpactDetails `json:"details,omitempty"`
}
type PlanBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type PlanDocument struct {
	SchemaVersion      int             `json:"schema_version"`
	BaseRevision       string          `json:"base_revision"`
	CompilerVersion    string          `json:"compiler_version"`
	CapabilitiesDigest string          `json:"capabilities_digest"`
	Intent             json.RawMessage `json:"intent"`
	Inputs             []PlanInput     `json:"inputs"`
	Impact             PlanImpact      `json:"impact"`
	Blockers           []PlanBlocker   `json:"blockers"`
	PlanDigest         string          `json:"plan_digest"`
}

// IntentValidator is trusted pure code. It must validate the complete Intent and
// its input bindings using only supplied memory, never external state or effects.
type IntentValidator func(json.RawMessage, []PlanInput) error

// Plan owns canonical authority; the exported document is only a detached view.
// The zero value is invalid and has empty accessors.
type Plan struct {
	raw    []byte
	digest string
}

func (Plan) String() string   { return "control.Plan{private material}" }
func (Plan) GoString() string { return "control.Plan{private material}" }
func (p Plan) Bytes() []byte  { return bytes.Clone(p.raw) }
func (p Plan) Digest() string { return p.digest }
func (p Plan) Document() PlanDocument {
	var d PlanDocument
	if p.raw != nil {
		_ = json.Unmarshal(p.raw, &d)
	}
	return d
}

func planCanonical(raw []byte) ([]byte, error) {
	b, e := config.CanonicalJSON(raw)
	if e != nil || len(b) > maxPlanBytes {
		return nil, ErrPlan
	}
	return b, nil
}
func planObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || raw[0] != '{' {
		return nil, ErrPlan
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, ErrPlan
	}
	return m, nil
}
func planFields(raw []byte, names ...string) bool {
	m, e := planObject(raw)
	if e != nil || len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}

// Values already came from strict JCS. Encode keys without HTML or LS/PS
// escaping, avoiding RawMessage's secondary escaping and artificial size growth.
func planJoin(m map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []byte{'{'}
	for i, k := range keys {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '"')
		for _, r := range k {
			switch r {
			case '"', '\\':
				out = append(out, '\\', byte(r))
			case '\b':
				out = append(out, '\\', 'b')
			case '\t':
				out = append(out, '\\', 't')
			case '\n':
				out = append(out, '\\', 'n')
			case '\f':
				out = append(out, '\\', 'f')
			case '\r':
				out = append(out, '\\', 'r')
			default:
				if r < 0x20 {
					const hex = "0123456789abcdef"
					out = append(out, '\\', 'u', '0', '0', hex[byte(r)>>4], hex[byte(r)&15])
				} else {
					out = append(out, string(r)...)
				}
			}
		}
		out = append(out, '"', ':')
		out = append(out, m[k]...)
	}
	out = append(out, '}')
	return planCanonical(out)
}

// ComputePlanDigest validates strict JSON and object/size constraints only. It
// does not validate Plan semantics or establish permission to apply a plan.
func ComputePlanDigest(raw []byte) (string, error) {
	b, e := planCanonical(raw)
	if e != nil {
		return "", ErrPlan
	}
	m, e := planObject(b)
	if e != nil {
		return "", ErrPlan
	}
	delete(m, "plan_digest")
	b, e = planJoin(m)
	if e != nil {
		return "", ErrPlan
	}
	return hash(b), nil
}

func BuildPlan(raw []byte, validator IntentValidator) (Plan, error) {
	return readPlan(raw, validator, true)
}
func ParsePlan(raw []byte, validator IntentValidator) (Plan, error) {
	return readPlan(raw, validator, false)
}

func readPlan(raw []byte, validator IntentValidator, build bool) (Plan, error) {
	if validator == nil {
		return Plan{}, ErrPlan
	}
	b, e := planCanonical(raw)
	if e != nil {
		return Plan{}, ErrPlan
	}
	fields := []string{"schema_version", "base_revision", "compiler_version", "capabilities_digest", "intent", "inputs", "impact", "blockers"}
	if !build {
		fields = append(fields, "plan_digest")
	}
	if !planFields(b, fields...) {
		return Plan{}, ErrPlan
	}
	m, _ := planObject(b)
	var version int
	if json.Unmarshal(m["schema_version"], &version) != nil || version != 1 && version != 2 {
		return Plan{}, ErrPlan
	}
	impactFields := []string{"requires_disruption", "affected_apps", "previous_generation", "target_generation", "connection_effect", "runtime_action"}
	if version == 2 {
		impactFields = append(impactFields, "details")
	}
	if !planFields(m["impact"], impactFields...) {
		return Plan{}, ErrPlan
	}
	var inputObjects, blockObjects []json.RawMessage
	if json.Unmarshal(m["inputs"], &inputObjects) != nil || inputObjects == nil || len(inputObjects) == 0 || json.Unmarshal(m["blockers"], &blockObjects) != nil || blockObjects == nil {
		return Plan{}, ErrPlan
	}
	for _, v := range inputObjects {
		if !planFields(v, "kind", "id", "sha256") {
			return Plan{}, ErrPlan
		}
	}
	for _, v := range blockObjects {
		if !planFields(v, "code", "message") {
			return Plan{}, ErrPlan
		}
	}
	// Null must not silently decode into scalar zero values.
	for _, key := range fields {
		if bytes.Equal(m[key], []byte("null")) {
			return Plan{}, ErrPlan
		}
	}
	impact, _ := planObject(m["impact"])
	for key, v := range impact {
		if key != "previous_generation" && bytes.Equal(v, []byte("null")) {
			return Plan{}, ErrPlan
		}
	}
	for _, objects := range [][]json.RawMessage{inputObjects, blockObjects} {
		for _, raw := range objects {
			obj, _ := planObject(raw)
			for _, v := range obj {
				if bytes.Equal(v, []byte("null")) {
					return Plan{}, ErrPlan
				}
			}
		}
	}
	var d PlanDocument
	if json.Unmarshal(b, &d) != nil || d.SchemaVersion != version || !planID(d.BaseRevision) || !boundedString(d.CompilerVersion) || !isHash(d.CapabilitiesDigest) || len(d.Intent) == 0 || d.Intent[0] != '{' || d.Impact.AffectedApps == nil || !planID(d.Impact.TargetGeneration) {
		return Plan{}, ErrPlan
	}
	if d.Impact.PreviousGeneration != nil && !planID(*d.Impact.PreviousGeneration) {
		return Plan{}, ErrPlan
	}
	if version == 2 && !validPlanImpactDetails(impact["details"], d.Impact) {
		return Plan{}, ErrPlan
	}
	seen := map[string]bool{}
	for _, in := range d.Inputs {
		switch in.Kind {
		case "provider", "core_binary", "capabilities", "rule_asset", "app_binding":
		default:
			return Plan{}, ErrPlan
		}
		key := in.Kind + "/" + in.ID
		if !planID(in.ID) || !isHash(in.SHA256) || seen[key] {
			return Plan{}, ErrPlan
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, id := range d.Impact.AffectedApps {
		if !planID(id) || seen[id] {
			return Plan{}, ErrPlan
		}
		seen[id] = true
	}
	for _, v := range d.Blockers {
		if !boundedString(v.Code) || v.Message == "" || len(v.Message) > 4096 {
			return Plan{}, ErrPlan
		}
	}
	switch d.Impact.RuntimeAction {
	case "start", "replace", "select", "none":
	default:
		return Plan{}, ErrPlan
	}
	switch d.Impact.ConnectionEffect {
	case "preserve_old":
	case "unknown", "interrupt":
		if !d.Impact.RequiresDisruption {
			if !build {
				return Plan{}, ErrPlan
			}
			impact["requires_disruption"] = json.RawMessage("true")
			m["impact"], e = planJoin(impact)
			if e != nil {
				return Plan{}, ErrPlan
			}
		}
	default:
		return Plan{}, ErrPlan
	}
	if e = validator(bytes.Clone(d.Intent), append([]PlanInput(nil), d.Inputs...)); e != nil {
		return Plan{}, ErrPlan
	}
	delete(m, "plan_digest")
	without, e := planJoin(m)
	if e != nil {
		return Plan{}, ErrPlan
	}
	digest := hash(without)
	if !build && (!isHash(d.PlanDigest) || d.PlanDigest != digest) {
		return Plan{}, ErrPlan
	}
	m["plan_digest"] = json.RawMessage(`"` + digest + `"`)
	b, e = planJoin(m)
	if e != nil {
		return Plan{}, ErrPlan
	}
	return Plan{raw: b, digest: digest}, nil
}
func planID(s string) bool { _, e := config.ParseID(s); return e == nil }
