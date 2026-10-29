//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/p1testhook"
	owned "hoopa/internal/runtime"
)

type basicRuntime struct {
	store      *config.Store
	controller *mihomo.Controller
	ownership  *owned.Ownership
	launch     config.RunIntent
	entryArgs  []string
	prepared   *owned.PreparedLaunch
	registered *owned.OwnedRun
	child      *owned.LaunchChild
}

func newBasicApplyRuntime(store *config.Store, material checkedBasicMaterial, entryArgs []string) (*basicRuntime, error) {
	controller, err := mihomo.NewController(store, material.capability)
	if err != nil {
		return nil, err
	}
	ownership, err := owned.NewOwnership(store, controller)
	if err != nil {
		return nil, err
	}
	return &basicRuntime{store: store, controller: controller, ownership: ownership, launch: material.launch, entryArgs: append([]string(nil), entryArgs...)}, nil
}

func (r *basicRuntime) close() {
	if r.registered != nil {
		r.registered.Close() // Release the original handle; never stop the core.
		r.registered = nil
	}
}

func (r *basicRuntime) stop(ctx context.Context, id config.RunID, operation config.ID) error {
	intent, _, err := r.store.ReadRunIntent(id)
	if err != nil {
		return err
	}
	// The old bundle may name another installed path for the same approved
	// build. Reconstruct its controller from its own fixed binary reference.
	capability, err := mihomo.ApprovedCapability(intent.Binary)
	if err != nil || capability.Binary != intent.Binary {
		return ErrCapabilityUnsupported
	}
	controller, err := mihomo.NewController(r.store, capability)
	if err != nil {
		return err
	}
	ownership, err := owned.NewOwnership(r.store, controller)
	if err != nil {
		return err
	}
	run, _, err := ownership.Register(ctx, id, operation, false)
	if err != nil {
		return err
	}
	defer run.Close()
	if run.Observation().Gate != "open" {
		return owned.ErrOwnershipUnknown
	}
	return ownership.Terminate(ctx, run, operation)
}

func (r *basicRuntime) prepare(operation config.ID) (config.RunID, error) {
	if r.prepared != nil {
		return "", config.ErrRunConflict
	}
	input := r.launch
	input.OperationID = operation
	prepared, err := owned.PrepareLaunch(r.store, input)
	if err != nil {
		return "", err
	}
	r.prepared = prepared
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "intent-published", string(operation), string(prepared.Intent().RunID), "", false, map[string]any{"intent": prepared.Intent()})
	}
	return prepared.Intent().RunID, nil
}

func (r *basicRuntime) start(ctx context.Context, id config.RunID) error {
	if r.prepared == nil || r.prepared.Intent().RunID != id || r.child != nil {
		return config.ErrRunConflict
	}
	// StartBasicLauncher owns exactly one child Wait goroutine. Losing the
	// manager or this return value does not send a signal to that child.
	child, err := r.controller.Start(ctx, r.prepared, r.entryArgs)
	r.child = child
	return err
}

func (r *basicRuntime) verifyClosed(ctx context.Context, id config.RunID, operation config.ID) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Startup polling is observation only. Failed ownership does not authorize
	// a second spawn, a signal, or registration of a different process.
	for {
		run, err := r.ownership.Observe(ctx, id)
		if err == nil {
			gate := run.Observation().Gate
			run.Close()
			if gate != "closed" {
				return owned.ErrOwnershipUnknown
			}
			registered, _, err := r.ownership.Register(ctx, id, operation, true)
			if err != nil {
				return err // A failed registration publication is not polled.
			}
			r.registered = registered
			if p1testhook.Enabled {
				_ = p1testhook.Record("runtime", "closed-verified", string(operation), string(id), "", false, map[string]any{"identity": registered.Identity(), "observation": registered.Observation()})
			}
			return nil
		}
		if errors.Is(err, config.ErrUncertain) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(owned.ErrOwnershipUnknown, ctx.Err())
		case <-timer.C:
		}
	}
}

func (r *basicRuntime) activate(ctx context.Context, id config.RunID, operation config.ID) error {
	if r.registered == nil || r.registered.Observation().RunID != id {
		return owned.ErrOwnershipUnknown
	}
	if p1testhook.Enabled {
		if err := p1testhook.Record("runtime", "entry-opening", string(operation), string(id), "", true, map[string]any{"identity": r.registered.Identity()}); err != nil {
			return err
		}
	}
	result, err := r.controller.Activate(ctx, r.registered, operation)
	if err != nil {
		return err
	}
	if result.Receipt == nil || result.Current == nil || result.Receipt.Result != "opened" || result.Current.State != "open" {
		return owned.ErrOwnershipUnknown
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "entry-opened", string(operation), string(id), "", false, map[string]any{"identity": r.registered.Identity(), "receipt": result.Receipt, "current": result.Current})
	}
	return nil
}

func (r *basicRuntime) verifyOpen(ctx context.Context, id config.RunID) error {
	run, err := r.ownership.Observe(ctx, id)
	if err != nil {
		return err
	}
	defer run.Close()
	if r.registered == nil || run.Identity() != r.registered.Identity() || run.Observation().Gate != "open" {
		if p1testhook.Enabled {
			_ = p1testhook.Record("runtime", "open-proof-mismatch", "", string(id), "", false, map[string]any{"registration_present": r.registered != nil, "gate": run.Observation().Gate})
		}
		return owned.ErrOwnershipUnknown
	}
	if p1testhook.Enabled {
		// The caller's request scope supplies the current operation; this proof
		// itself identifies the run and must not invent a publishing operation.
		_ = p1testhook.Record("runtime", "open-verified", "", string(id), "", false, map[string]any{"identity": run.Identity(), "observation": run.Observation()})
	}
	return nil
}
