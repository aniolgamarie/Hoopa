//go:build linux && amd64 && p1integration

// Package p1fixture supplies two exact reviewed test inputs, not a general
// Intent compiler. It never creates material, starts a process or observes a
// core. Callers must independently prove the executable and derive live impact.
package p1fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/control"
	wire "hoopa/protocol/corecontrol"
)

const CompilerVersion = "hoopa-test-finite-basic-v1"

var ErrFixture = errors.New("P1_FINITE_FIXTURE_INVALID_OR_UNAVAILABLE")

// Fixture retains private immutable input bytes. Accessors return detached
// copies; formatting never discloses the generated config's credentials.
type Fixture struct {
	key      string
	material control.BasicMaterial
	intent   json.RawMessage
	inputs   []control.PlanInput
	snapshot wire.Snapshot
}

func (*Fixture) String() string   { return "p1fixture.Fixture{private finite material}" }
func (*Fixture) GoString() string { return "p1fixture.Fixture{private finite material}" }
func (f *Fixture) Key() string {
	if f == nil {
		return ""
	}
	return f.key
}
func (f *Fixture) Generation() config.Generation {
	if f == nil {
		return ""
	}
	return f.material.Manifest.Generation
}
func (f *Fixture) CapabilityDigest() string {
	if f == nil {
		return ""
	}
	return f.material.Manifest.CapabilitiesDigest
}
func (f *Fixture) Inputs() []control.PlanInput {
	if f == nil {
		return nil
	}
	return append([]control.PlanInput(nil), f.inputs...)
}
func (f *Fixture) Material() control.BasicMaterial {
	if f == nil {
		return control.BasicMaterial{}
	}
	material := f.material
	material.Manifest.Binaries = append([]config.BinaryReference(nil), material.Manifest.Binaries...)
	material.Manifest.Files = append([]config.BundleFile(nil), material.Manifest.Files...)
	material.Files = make(map[string][]byte, len(f.material.Files))
	for name, raw := range f.material.Files {
		material.Files[name] = bytes.Clone(raw)
	}
	return material
}
func (f *Fixture) Snapshot() wire.Snapshot {
	if f == nil {
		return wire.Snapshot{}
	}
	snapshot := f.snapshot
	snapshot.Entries = append([]wire.Entry(nil), snapshot.Entries...)
	snapshot.Rules = append([]wire.Rule(nil), snapshot.Rules...)
	snapshot.Proxies = append([]wire.Proxy(nil), snapshot.Proxies...)
	for i := range snapshot.Proxies {
		snapshot.Proxies[i].Members = append([]string{}, snapshot.Proxies[i].Members...)
	}
	return snapshot
}

// ValidateIntent accepts exactly this fixture's canonical finite reference and
// exact ordered inputs. It reads no files and accepts no arbitrary user Intent.
func (f *Fixture) ValidateIntent(raw json.RawMessage, inputs []control.PlanInput) error {
	if f == nil || len(f.intent) == 0 {
		return ErrFixture
	}
	canonical, err := config.CanonicalJSON(raw)
	if err != nil || !bytes.Equal(canonical, f.intent) || !reflect.DeepEqual(inputs, f.inputs) {
		return ErrFixture
	}
	return nil
}

// Plan binds caller-supplied live impact. It neither derives nor echoes current
// observations; target, compiler, input identity and finite intent remain fixed.
func (f *Fixture) Plan(base config.Revision, impact control.PlanImpact) (control.Plan, error) {
	if f == nil || impact.TargetGeneration != string(f.Generation()) {
		return control.Plan{}, ErrFixture
	}
	if _, err := config.ParseID(string(base)); err != nil {
		return control.Plan{}, ErrFixture
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "base_revision": base, "compiler_version": CompilerVersion, "capabilities_digest": f.CapabilityDigest(), "intent": json.RawMessage(f.intent), "inputs": f.Inputs(), "impact": impact, "blockers": []control.PlanBlocker{}})
	if err != nil {
		return control.Plan{}, ErrFixture
	}
	plan, err := control.BuildPlan(raw, f.ValidateIntent)
	if err != nil {
		return control.Plan{}, ErrFixture
	}
	return plan, nil
}

// Source returns retained exact material only for this checked Plan. Filesystem
// rereads/current impact, real executable proof and control admission are caller
// responsibilities, never inferred from the finite capability declaration.
func (f *Fixture) Source(ctx context.Context, plan control.Plan) (control.BasicMaterial, error) {
	if ctx == nil || f == nil {
		return control.BasicMaterial{}, ErrFixture
	}
	if err := ctx.Err(); err != nil {
		return control.BasicMaterial{}, errors.Join(ErrFixture, err)
	}
	checked, err := control.ParsePlan(plan.Bytes(), f.ValidateIntent)
	if err != nil {
		return control.BasicMaterial{}, ErrFixture
	}
	document := checked.Document()
	if document.CompilerVersion != CompilerVersion || document.CapabilitiesDigest != f.CapabilityDigest() || document.Impact.TargetGeneration != string(f.Generation()) {
		return control.BasicMaterial{}, ErrFixture
	}
	if err := ctx.Err(); err != nil {
		return control.BasicMaterial{}, errors.Join(ErrFixture, err)
	}
	return f.Material(), nil
}
