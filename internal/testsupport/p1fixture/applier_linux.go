//go:build linux && amd64 && p1integration

package p1fixture

import (
	"context"
	"encoding/json"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/p1testhook"
	"hoopa/internal/platform"
)

// OpenApplier initializes an explicit isolated test writer with both complete
// finite inputs. It is not part of shipped service startup or a user compiler.
func OpenApplier(paths config.Paths, repo, binary string, workdirs map[string]string, launcher []string) (*control.Coordinator, *control.BasicApplier, error) {
	fixtures := map[config.Generation]*Fixture{}
	for _, key := range []string{"app-first", "domain-first"} {
		f, err := Load(repo, key, binary, workdirs[key])
		if err != nil {
			return nil, nil, err
		}
		fixtures[f.Generation()] = f
	}
	validator := func(raw json.RawMessage, inputs []control.PlanInput) error {
		for _, f := range fixtures {
			if f.ValidateIntent(raw, inputs) == nil {
				return nil
			}
		}
		return ErrFixture
	}
	resolve := func(p control.Plan) (*Fixture, error) {
		f := fixtures[config.Generation(p.Document().Impact.TargetGeneration)]
		if f == nil {
			return nil, ErrFixture
		}
		return f, nil
	}
	current := func(ctx context.Context, p control.Plan) (control.CurrentPlanInputs, error) {
		if ctx == nil || ctx.Err() != nil {
			return control.CurrentPlanInputs{}, ErrFixture
		}
		f, err := resolve(p)
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		fresh, err := Load(repo, f.Key(), binary, f.material.Workdir)
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		executable, err := platform.OpenExecutable(ctx, binary, fresh.material.Manifest.Binaries[0].SHA256)
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		if err = executable.Close(); err != nil {
			return control.CurrentPlanInputs{}, err
		}
		s, err := config.OpenStore(paths, false)
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		defer s.Close()
		active, err := s.ReadActive()
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		impact, err := Impact(active, fresh)
		if err != nil {
			return control.CurrentPlanInputs{}, err
		}
		return control.CurrentPlanInputs{CompilerVersion: CompilerVersion, CapabilitiesDigest: fresh.CapabilityDigest(), Inputs: fresh.Inputs(), Impact: impact}, nil
	}
	c, err := control.OpenCoordinator(paths, validator, current)
	if err != nil {
		return nil, nil, err
	}
	a, err := control.NewBasicApplier(c, func(ctx context.Context, p control.Plan) (control.BasicMaterial, error) {
		f, err := resolve(p)
		if err != nil {
			return control.BasicMaterial{}, err
		}
		material, err := f.Source(ctx, p)
		if err == nil && p1testhook.Enabled {
			_ = p1testhook.Record("fixture", "source-resolved", "", "", "", false, map[string]any{"key": f.Key(), "generation": f.Generation()})
		}
		return material, err
	}, launcher)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	return c, a, nil
}

// Impact derives the finite policy independently from current authority. A
// stopped configuration with history must use runtime.start before replacement.
func Impact(active config.ActivePointer, f *Fixture) (control.PlanImpact, error) {
	if f == nil {
		return control.PlanImpact{}, ErrFixture
	}
	i := control.PlanImpact{RequiresDisruption: true, AffectedApps: []string{"entry-a", "entry-b"}, TargetGeneration: string(f.Generation()), ConnectionEffect: "unknown", RuntimeAction: "start"}
	if active.Generation == nil && active.OperationID == nil && active.DesiredRuntime == "stopped" {
		return i, nil
	}
	if active.Generation == nil || active.OperationID == nil || active.DesiredRuntime != "running" || *active.Generation == f.Generation() {
		return control.PlanImpact{}, ErrFixture
	}
	previous := string(*active.Generation)
	i.PreviousGeneration = &previous
	i.RuntimeAction = "replace"
	i.ConnectionEffect = "interrupt"
	return i, nil
}
