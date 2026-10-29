//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/p1testhook"
	owned "hoopa/internal/runtime"
)

var ErrRestartExhausted = errors.New("RESTART_BUDGET_EXHAUSTED")

// RestartWatch is a one-attempt, service-lifetime authorization. The service
// may create it only within an explicitly authorized managed-start scope, never
// for control-only, status, recovery, or automatic service initialization.
// Copies share both the original handle and the consumed budget. No background
// worker is created; the managed service drives Attempt with its own context.
type RestartWatch struct{ *restartState }

type restartState struct {
	mu, attempt sync.Mutex
	applier     *BasicApplier
	base        config.ActivePointer
	runID       config.RunID
	run         *owned.OwnedRun
	used        bool
	closed      bool
	exited      bool
}

// AuthorizeRestart is an internal service capability, not a JSON operation or
// a default policy. Each coordinator lifetime permits one explicit grant; a
// repeated grant is rejected even after Close, stop/start, or budget exhaustion.
// The caller grants exactly one attempt. It binds a
// fresh proved original run; reopening the manager cannot reconstruct it from
// disk, and later configuration/stop operations invalidate this scope.
func (a *BasicApplier) AuthorizeRestart(ctx context.Context, revision config.Revision) (*RestartWatch, error) {
	if a == nil || a.coordinator == nil || a.coordinator.coordinatorState == nil || ctx == nil {
		return nil, ErrRequest
	}
	c := a.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return nil, config.ErrClosed
	}
	if c.restartGranted {
		return nil, ErrRestartExhausted
	}
	base, err := c.store.ReadActive()
	if err != nil || base.Revision != revision {
		return nil, errors.Join(ErrConflict, err)
	}
	if err := c.noPendingLifecycle(); err != nil {
		return nil, err
	}
	id, err := c.activeRunForApply(base, "replace", "")
	if err != nil || id == nil {
		return nil, errors.Join(ErrRecoveryRequired, err)
	}
	run, err := c.observeLifecycleRun(ctx, *id, *base.OperationID)
	if err != nil {
		return nil, err
	}
	c.restartGranted = true
	return &RestartWatch{&restartState{applier: a, base: base, runID: *id, run: run}}, nil
}

func (c *Coordinator) noPendingLifecycle() error {
	ids, err := c.journal.IDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		op, err := c.journal.Get(id)
		if err != nil {
			return err
		}
		if !terminal(op.Record.Phase) {
			return ErrRecoveryRequired
		}
	}
	return nil
}

func (c *Coordinator) observeLifecycleRun(ctx context.Context, id config.RunID, op config.ID) (*owned.OwnedRun, error) {
	intent, _, err := c.store.ReadRunIntent(id)
	if err != nil {
		return nil, err
	}
	capability, err := mihomo.ApprovedCapability(intent.Binary)
	if err != nil || capability.Binary != intent.Binary {
		return nil, ErrCapabilityUnsupported
	}
	controller, err := mihomo.NewController(c.store, capability)
	if err != nil {
		return nil, err
	}
	owner, err := owned.NewOwnership(c.store, controller)
	if err != nil {
		return nil, err
	}
	run, err := owner.Observe(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := controller.VerifyActivation(ctx, run, op); err != nil {
		run.Close()
		return nil, err
	}
	return run, nil
}

func (w *RestartWatch) Close() error {
	if w == nil || w.restartState == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.run != nil {
		return w.run.Close() // Wakes a pending WaitExited; never stops the core.
	}
	return nil
}

// ExitObserved returns only the historical exit fact for this exact run. It is
// not an aggregate "core stopped" result when another operation is pending.
func (w *RestartWatch) ExitObserved() (config.RunID, bool) {
	if w == nil || w.restartState == nil {
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runID, w.exited
}

// Attempt waits on the original pidfd, then makes at most one fresh launch.
// The wait holds no coordinator lock, so explicit stop can invalidate the scope.
// Budget exhaustion still observes the successor's exit but never writes/spawns.
func (w *RestartWatch) Attempt(ctx context.Context) (StoredOperation, bool, error) {
	if w == nil || w.restartState == nil || ctx == nil {
		return StoredOperation{}, false, ErrRequest
	}
	w.attempt.Lock()
	defer w.attempt.Unlock()
	w.mu.Lock()
	if w.closed || w.run == nil {
		w.mu.Unlock()
		return StoredOperation{}, false, config.ErrClosed
	}
	run := w.run
	w.mu.Unlock()
	if err := run.WaitExited(ctx); err != nil {
		return StoredOperation{}, false, err
	}
	if p1testhook.Enabled {
		// A completed wait is a source-run fact, even if a concurrent explicit
		// stop invalidates restart authorization. It is not a restart admission.
		_ = p1testhook.Record("runtime", "source-exit-observed", "", string(run.Observation().RunID), "", false, map[string]any{"identity": run.Identity()})
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return StoredOperation{}, false, config.ErrClosed
	}
	w.exited = true
	if w.used {
		return StoredOperation{}, false, ErrRestartExhausted
	}
	// A single bounded backoff, never a retry queue or a missed-timer replay.
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return StoredOperation{}, false, ctx.Err()
	case <-timer.C:
	}
	c := w.applier.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return StoredOperation{}, false, config.ErrClosed
	}
	base, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(base, w.base) || base.DesiredRuntime != "running" {
		return StoredOperation{}, false, errors.Join(ErrConflict, err)
	}
	if err := c.noPendingLifecycle(); err != nil {
		return StoredOperation{}, false, err
	}
	if err := c.store.CheckOperationAdmission(); err != nil {
		return StoredOperation{}, false, err
	}
	ids, err := c.store.ListRunIDs()
	if err != nil || len(ids) >= config.MaxRuns {
		return StoredOperation{}, false, errors.Join(config.ErrRunConflict, err)
	}
	for _, id := range ids {
		intent, _, err := c.store.ReadRunIntent(id)
		if err != nil {
			return StoredOperation{}, false, err
		}
		origin, err := c.journal.Get(intent.OperationID)
		if err != nil || !terminal(origin.Record.Phase) {
			return StoredOperation{}, false, errors.Join(ErrRecoveryRequired, err)
		}
	}
	s := lifecycleEvidence{Version: 1, Mode: "restart", DesiredRuntime: "running", Base: base, SourceRunID: w.runID, ExitOperationID: *base.OperationID, ExitGate: "closed", SourceStopped: true}
	if err := c.verifyLifecycleExit(s); err != nil {
		return StoredOperation{}, false, err
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return StoredOperation{}, false, err
	}
	raw, _ := encode(map[string]any{"api_version": "v1", "request_id": "restart-" + hex.EncodeToString(nonce[:]), "operation": "runtime.start", "params": map[string]any{"expected_revision": base.Revision, "control_only": false}})
	request, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	defer p1RequestScope(request)()
	// Consume before the first potentially durable admission, including failures.
	// A crashed service loses this in-memory capability; it cannot grant a retry.
	w.used = true
	stored, made, err := c.admitLifecycle(request, s)
	if err != nil || !made {
		return stored, made, err
	}
	runtime := &basicRuntime{store: c.store, entryArgs: w.applier.launcherArgs}
	stored, err = c.executeStart(ctx, stored, s, runtime.restore, p1Boundary(request))
	if err != nil {
		return stored, true, err
	}
	state, err := decodeLifecycleEvidence(stored)
	if err != nil || state.TargetRunID == nil {
		return stored, true, ErrOperationRecord
	}
	w.run.Close()
	w.run, w.exited, w.runID = nil, false, *state.TargetRunID
	w.base = lifecycleTarget(stored, state)
	w.run, err = c.observeLifecycleRun(ctx, w.runID, stored.Record.Identity.OperationID)
	return stored, true, err
}
