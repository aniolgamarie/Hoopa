//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	owned "hoopa/internal/runtime"
)

// The recovery call is already durable before reaching here. Neither a stored
// intent nor a missing process authorizes a new spawn. Existing closed starts
// may be registered/activated only after fresh full ownership proof.
func (c *Coordinator) reconcileLifecycle(ctx context.Context, target StoredOperation, boundary func(string) error) (StoredOperation, error) {
	s, err := decodeLifecycleEvidence(target)
	if err != nil {
		// Preserve the uninterpretable last evidence rather than manufacture a
		// stopped/started fact from prepared's empty evidence.
		if terminal(target.Record.Phase) {
			return target, ErrRecoveryRequired
		}
		result, _ := applyResult(target, applyEvidence{}, Degraded, "degraded")
		next, writeErr := c.journal.Advance(target.Record.Identity.OperationID, target.SHA256, Degraded, json.RawMessage(target.Record.Steps[len(target.Record.Steps)-1].Evidence), result)
		if writeErr != nil {
			return target, errors.Join(errRecoveryPersistence, writeErr)
		}
		return next, errors.Join(ErrRecoveryRequired, err)
	}
	current, err := c.store.ReadActive()
	desired := lifecycleTarget(target, s)
	if err != nil || !reflect.DeepEqual(current, s.Base) && !reflect.DeepEqual(current, desired) {
		return target, errors.Join(ErrConflict, err)
	}
	if target.Record.Phase == RolledBack {
		return target, ErrOperationRecord
	}
	if s.Mode == "stop" {
		if target.Record.Phase == Committed {
			if !reflect.DeepEqual(current, desired) {
				return target, ErrConflict
			}
			return target, c.verifyLifecycleExit(s)
		}
		if !s.SourceStopped && !reflect.DeepEqual(current, s.Base) {
			return c.lifecycleFailed(target, s, ErrRecoveryRequired)
		}
		return c.executeStop(ctx, target, s, &basicRuntime{store: c.store}, boundary)
	}
	if err := c.verifyLifecycleExit(s); err != nil {
		return c.lifecycleFailed(target, s, err)
	}
	if s.TargetRunID == nil {
		return c.lifecycleFailed(target, s, ErrRecoveryRequired)
	}
	op, id := target.Record.Identity.OperationID, *s.TargetRunID
	intent, err := c.lifecycleStartOriginal(target, s)
	if err != nil {
		return c.lifecycleFailed(target, s, err)
	}
	capability, err := mihomo.ApprovedCapability(intent.Binary)
	if err != nil || capability.Binary != intent.Binary {
		return c.lifecycleFailed(target, s, ErrCapabilityUnsupported)
	}
	runtime, err := newBasicApplyRuntime(c.store, checkedBasicMaterial{launch: intent, capability: capability}, nil)
	if err != nil {
		return c.lifecycleFailed(target, s, err)
	}
	defer runtime.close()
	run, err := runtime.ownership.Observe(ctx, id)
	if err != nil {
		return c.lifecycleFailed(target, s, err)
	}
	identity, gate := run.Identity(), run.Observation().Gate
	run.Close()
	advance := func(phase Phase, step string) error {
		next, err := c.advanceLifecycle(target, s, phase, step)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		target = next
		return lifecycleCut(boundary, step)
	}
	if gate == "closed" {
		if terminal(target.Record.Phase) || target.Record.Phase == Publishing || !reflect.DeepEqual(current, s.Base) {
			return c.lifecycleFailed(target, s, ErrRecoveryRequired)
		}
		if err := advance(Verifying, "recover-start-verify-closed"); err != nil {
			return target, err
		}
		if err := runtime.verifyClosed(ctx, id, op); err != nil {
			return c.lifecycleFailed(target, s, err)
		}
		if runtime.registered.Identity() != identity {
			return c.lifecycleFailed(target, s, owned.ErrOwnershipUnknown)
		}
		if err := advance(Verifying, "recover-start-activate"); err != nil {
			return target, err
		}
		if err := runtime.activate(ctx, id, op); err != nil {
			return c.lifecycleFailed(target, s, err)
		}
	}
	// An open gate alone is insufficient; the exact immutable registration and
	// activation receipt must belong to this original start operation.
	first, err := c.verifyRecoveredRun(ctx, id, op)
	if err != nil || first != identity {
		return c.lifecycleFailed(target, s, errors.Join(owned.ErrOwnershipUnknown, err))
	}
	if target.Record.Phase == Committed {
		if !reflect.DeepEqual(current, desired) {
			return target, ErrConflict
		}
	} else {
		if target.Record.Phase != Publishing {
			if err := advance(Verifying, "recover-start-open-verified"); err != nil {
				return target, err
			}
			if err := advance(Publishing, "recover-start-publish"); err != nil {
				return target, err
			}
		}
		if !reflect.DeepEqual(current, desired) {
			if err := c.store.PublishActive(desired); err != nil {
				return target, errors.Join(errRecoveryPersistence, err)
			}
		}
	}
	if err := lifecycleCut(boundary, "recover-active-published"); err != nil {
		return target, err
	}
	last, err := c.verifyRecoveredRun(ctx, id, op)
	if err != nil || last != first {
		return c.lifecycleFailed(target, s, errors.Join(owned.ErrOwnershipUnknown, err))
	}
	if !terminal(target.Record.Phase) {
		if err := advance(Committed, "recover-start-committed"); err != nil {
			return target, err
		}
	}
	return target, nil
}

func (c *Coordinator) lifecycleStartOriginal(target StoredOperation, s lifecycleEvidence) (config.RunIntent, error) {
	var zero config.RunIntent
	if s.TargetRunID == nil || s.Workdir == "" || s.Base.Generation == nil {
		return zero, config.ErrRunConflict
	}
	ids, err := c.store.ListRunIDs()
	if err != nil {
		return zero, err
	}
	count := 0
	var matched config.RunIntent
	for _, id := range ids {
		run, _, err := c.store.ReadRunIntent(id)
		if err != nil {
			return zero, err
		}
		if run.OperationID == target.Record.Identity.OperationID {
			if id != *s.TargetRunID || run.Generation != *s.Base.Generation || run.ManifestSHA256 != s.Base.ManifestSHA256 || filepath.Base(run.Workdir) != s.Workdir {
				return zero, config.ErrRunConflict
			}
			count++
			matched = run
		}
	}
	if count != 1 {
		return zero, config.ErrRunConflict
	}
	return matched, nil
}
