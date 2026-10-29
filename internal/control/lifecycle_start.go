//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"

	"hoopa/internal/config"
)

// Start starts the last committed bundle after a proved explicit stop. The
// first configuration is committed by Apply; an empty state cannot start a core.
// control_only is handled by the service layer and never enters this mutation.
func (a *BasicApplier) Start(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
	if a == nil || a.coordinator == nil || a.coordinator.coordinatorState == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	request, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, false, err
	}
	c := a.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	defer p1RequestScope(request)()
	if c.store == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	if old, err := c.journal.Lookup(request); !errors.Is(err, ErrOperationNotFound) {
		return old, false, err
	}
	if _, err := parseRuntimeParameters(request, "runtime.start"); err != nil {
		return StoredOperation{}, false, err
	}
	base, err := c.runtimeAdmission(ctx, request)
	if err != nil {
		return StoredOperation{}, false, err
	}
	previous, err := c.verifiedStopped(base)
	if err != nil {
		return StoredOperation{}, false, err
	}
	ids, err := c.store.ListRunIDs()
	if err != nil || len(ids) >= config.MaxRuns {
		return StoredOperation{}, false, errors.Join(config.ErrRunConflict, err)
	}
	// No unknown origin may be hidden by the stopped pointer.
	for _, id := range ids {
		run, _, err := c.store.ReadRunIntent(id)
		if err != nil {
			return StoredOperation{}, false, err
		}
		origin, err := c.journal.Get(run.OperationID)
		if err != nil || !terminal(origin.Record.Phase) {
			return StoredOperation{}, false, errors.Join(ErrRecoveryRequired, err)
		}
	}
	s := lifecycleEvidence{Version: 1, Mode: "start", DesiredRuntime: "running", Base: base, SourceRunID: previous.SourceRunID, ExitOperationID: *base.OperationID, ExitGate: "open", SourceStopped: true}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	stored, made, err := c.admitLifecycle(request, s)
	if err != nil || !made {
		return stored, made, err
	}
	runtime := &basicRuntime{store: c.store, entryArgs: a.launcherArgs}
	stored, err = c.executeStart(ctx, stored, s, runtime.restore, p1Boundary(request))
	return stored, true, err
}

type lifecycleClone func(config.RunID, config.ActivePointer, string) (closableApplyRuntime, error)

func (c *Coordinator) executeStart(ctx context.Context, stored StoredOperation, s lifecycleEvidence, clone lifecycleClone, boundary func(string) error) (StoredOperation, error) {
	advance := func(phase Phase, step string) error {
		next, err := c.advanceLifecycle(stored, s, phase, step)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		stored = next
		return lifecycleCut(boundary, step)
	}
	if err := advance(Validated, "start-intent-durable"); err != nil {
		return stored, err
	}
	if err := c.verifyLifecycleExit(s); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	var name [24]byte
	if _, err := rand.Read(name[:]); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	s.Workdir = "launch-" + hex.EncodeToString(name[:])
	if err := advance(Applying, "start-prepare-workdir"); err != nil {
		return stored, err
	}
	if err := ctx.Err(); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	runtime, err := clone(s.SourceRunID, s.Base, s.Workdir)
	if err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	defer runtime.close()
	if err := advance(Applying, "start-prepare-run"); err != nil {
		return stored, err
	}
	op := stored.Record.Identity.OperationID
	id, err := runtime.prepare(op)
	if err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := lifecycleCut(boundary, "start-run-intent-published"); err != nil {
		return stored, err
	}
	s.TargetRunID = &id
	intent, _, err := c.store.ReadRunIntent(id)
	if err != nil || intent.OperationID != op || intent.Generation != *s.Base.Generation || intent.ManifestSHA256 != s.Base.ManifestSHA256 {
		return c.lifecycleFailed(stored, s, errors.Join(config.ErrRunConflict, err))
	}
	if err := advance(Applying, "start-spawn"); err != nil {
		return stored, err
	}
	if err := runtime.start(ctx, id); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := lifecycleCut(boundary, "spawn-returned-before-registration"); err != nil {
		return stored, err
	}
	if err := advance(Verifying, "start-verify-closed"); err != nil {
		return stored, err
	}
	if err := runtime.verifyClosed(ctx, id, op); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := advance(Verifying, "start-activate"); err != nil {
		return stored, err
	}
	if err := runtime.activate(ctx, id, op); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := lifecycleCut(boundary, "start-entry-opened"); err != nil {
		return stored, err
	}
	if err := runtime.verifyOpen(ctx, id); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := advance(Publishing, "start-publish"); err != nil {
		return stored, err
	}
	current, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(current, s.Base) {
		return c.lifecycleFailed(stored, s, errors.Join(ErrConflict, err))
	}
	if err := c.store.PublishActive(lifecycleTarget(stored, s)); err != nil {
		return stored, errors.Join(errRecoveryPersistence, err)
	}
	if err := lifecycleCut(boundary, "start-active-published"); err != nil {
		return stored, err
	}
	if err := runtime.verifyOpen(ctx, id); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := advance(Committed, "start-committed"); err != nil {
		return stored, err
	}
	return stored, nil
}
