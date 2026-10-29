//go:build linux && amd64

package control

import (
	"context"

	"hoopa/internal/config"
	"hoopa/internal/policy"
)

// NewSourcesApplier uses the same admission, run ownership and recovery workflow
// as BasicApplier. Only the trusted local material producer and its pre-admission
// workdir preparation differ. Construction has no filesystem or runtime effects.
func NewSourcesApplier(c *Coordinator, launcherArgs []string) (*BasicApplier, error) {
	a, err := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		return BasicMaterial{}, ErrCapabilityUnsupported
	}, launcherArgs)
	if err != nil {
		return nil, err
	}
	a.sources = true
	return a, nil
}

// The caller holds c.mu and has already replayed known requests. A shape-only
// parse extracts untrusted Intent; it never grants admission or launch authority.
// The actual admission validator is bound to independently restored compilation.
// CurrentInputs repeats capture/compilation before the material callback can run.
func (c *Coordinator) sourceApplyDependenciesLocked(ctx context.Context, request Request) (IntentValidator, CurrentInputs, BasicMaterialSource, error) {
	fail := func(err error) (IntentValidator, CurrentInputs, BasicMaterialSource, error) {
		return nil, nil, nil, err
	}
	if c.serviceQuiesced {
		return fail(config.ErrClosed)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	untrusted, err := sourceApplyPlanShape(request)
	if err != nil {
		return fail(err)
	}
	doc := untrusted.Document()
	// Exact historical requests replay before this dependency builder. New
	// source/app admission requires the complete version-2 impact preview.
	if doc.SchemaVersion != 2 {
		return fail(ErrCapabilityUnsupported)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return fail(err)
	}
	if string(base.Revision) != doc.BaseRevision || !sameGeneration(base.Generation, doc.Impact.PreviousGeneration) {
		return fail(ErrConflict)
	}
	input, err := c.captureSourceCompileLocked(ctx, doc.Intent)
	if err != nil {
		return fail(err)
	}
	compiled, observed, err := compileCapturedSources(ctx, input)
	if err != nil {
		return fail(err)
	}
	if !sameCurrentInputs(doc, observed) {
		return fail(ErrConflict)
	}
	var accepted policy.Compilation
	haveAccepted := false
	current := func(ctx context.Context, plan Plan) (CurrentPlanInputs, error) {
		fresh, err := c.captureSourceCompileLocked(ctx, plan.Document().Intent)
		if err != nil {
			return CurrentPlanInputs{}, err
		}
		if string(fresh.Base.Revision) != plan.Document().BaseRevision {
			return CurrentPlanInputs{}, ErrConflict
		}
		value, inputs, err := compileCapturedSources(ctx, fresh)
		if err != nil {
			return CurrentPlanInputs{}, err
		}
		accepted, haveAccepted = value, true
		return inputs, nil
	}
	source := func(ctx context.Context, plan Plan) (BasicMaterial, error) {
		if !haveAccepted {
			return BasicMaterial{}, config.ErrReadUnavailable
		}
		if err := ctx.Err(); err != nil {
			return BasicMaterial{}, err
		}
		doc := plan.Document()
		if sourceCompilationValidator(accepted)(doc.Intent, doc.Inputs) != nil {
			return BasicMaterial{}, ErrPlan
		}
		manifest, files := accepted.Bundle()
		return BasicMaterial{Manifest: manifest, Files: files}, nil
	}
	return sourceCompilationValidator(compiled), current, source, nil
}
