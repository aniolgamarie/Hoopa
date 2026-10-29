//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// EnsureStarted is the service's explicit ordinary-start use case. A proved
// running core is adopted without changing its authority or creating a run.
// A proved stopped core uses Start's existing fresh-launch workflow. Merely
// opening a service, observing status, or recovering never calls this method.
func (a *BasicApplier) EnsureStarted(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
	if a == nil || a.coordinator == nil || a.coordinator.coordinatorState == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	r, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, false, err
	}
	c := a.coordinator
	c.mu.Lock()
	locked := true
	defer func() {
		if locked {
			c.mu.Unlock()
		}
	}()
	endTrace := p1RequestScope(r)
	defer endTrace()
	if c.store == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	if old, err := c.journal.Lookup(r); !errors.Is(err, ErrOperationNotFound) {
		return old, false, err
	}
	if _, err := parseRuntimeParameters(r, "runtime.start"); err != nil {
		return StoredOperation{}, false, err
	}
	base, err := c.runtimeAdmission(ctx, r)
	if err != nil {
		return StoredOperation{}, false, err
	}
	if base.DesiredRuntime == "stopped" {
		// Start reacquires the same lock and repeats replay/revision/admission.
		// This read-only inspection conveys no launch permission across the gap.
		endTrace()
		locked = false
		c.mu.Unlock()
		return a.Start(ctx, raw)
	}
	id, err := c.activeRunForApply(base, "replace", "")
	if err != nil || id == nil {
		return StoredOperation{}, false, errors.Join(ErrRecoveryRequired, err)
	}
	identity, err := c.verifyRecoveredRun(ctx, *id, *base.OperationID)
	if err != nil {
		return StoredOperation{}, false, errors.Join(ErrRecoveryRequired, err)
	}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	stored, state, made, err := c.admitAdoption(r, base, *id, identity)
	if err != nil || !made {
		return stored, made, err
	}
	stored, err = c.executeAdoption(ctx, stored, state, c.verifyRecoveredRun, p1Boundary(r))
	return stored, true, err
}

// The base's publishing operation remains the core's original operation. This
// receipt proves adoption only; it is never used as the source of a new RunIntent.
type adoptionEvidence struct {
	Version  int                      `json:"version"`
	Kind     string                   `json:"kind"`
	Step     string                   `json:"step"`
	Base     config.ActivePointer     `json:"base"`
	RunID    config.RunID             `json:"run_id"`
	Identity platform.ProcessIdentity `json:"identity"`
}

func isAdoption(stored StoredOperation) bool {
	if stored.Record.Identity.Operation != "runtime.start" || len(stored.Record.Steps) == 0 {
		return false
	}
	var kind struct {
		Kind string `json:"kind"`
	}
	raw := stored.Record.Steps[len(stored.Record.Steps)-1].Evidence
	return json.Unmarshal(raw, &kind) == nil && kind.Kind == "adopt-running"
}

func decodeAdoption(stored StoredOperation) (adoptionEvidence, error) {
	var s adoptionEvidence
	if stored.Record.Compacted || !isAdoption(stored) {
		return s, ErrOperationRecord
	}
	raw := stored.Record.Steps[len(stored.Record.Steps)-1].Evidence
	if json.Unmarshal(raw, &s) != nil {
		return s, ErrOperationRecord
	}
	b, _ := encode(s)
	b, err := config.CanonicalJSON(b)
	i := stored.Record.Identity.Intent
	if err != nil || !bytes.Equal(b, raw) || s.Version != 1 || s.Base.Version != 1 || s.Base.DesiredRuntime != "running" || s.Base.Generation == nil || s.Base.OperationID == nil || *s.Base.OperationID == stored.Record.Identity.OperationID || !isHash(s.Base.ManifestSHA256) || s.Base.Revision != i.BaseRevision || i.TargetRevision != nil || !reflect.DeepEqual(i.PreviousGeneration, s.Base.Generation) || !reflect.DeepEqual(i.TargetGeneration, s.Base.Generation) || i.CandidateDigest != s.Base.ManifestSHA256 {
		return s, ErrOperationRecord
	}
	if _, err := config.ParseID(string(s.RunID)); err != nil || s.Identity.PID <= 0 || s.Identity.StartTicks == 0 {
		return s, ErrOperationRecord
	}
	r, err := ParseRequest(stored.Record.Request)
	if err != nil {
		return s, err
	}
	revision, err := parseRuntimeParameters(r, "runtime.start")
	if err != nil || revision != s.Base.Revision || i.ImpactDigest != hash(r.params) {
		return s, ErrOperationRecord
	}
	return s, nil
}

func (c *Coordinator) admitAdoption(r Request, base config.ActivePointer, id config.RunID, identity platform.ProcessIdentity) (StoredOperation, adoptionEvidence, bool, error) {
	s := adoptionEvidence{Version: 1, Kind: "adopt-running", Base: base, RunID: id, Identity: identity}
	i := Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, TargetGeneration: base.Generation, CandidateDigest: base.ManifestSHA256, ImpactDigest: hash(r.params)}
	seed := StoredOperation{Record: Operation{Identity: Identity{OperationID: r.OperationID(), RequestID: r.ID(), Intent: i}}}
	result, err := applyResult(seed, applyEvidence{}, Prepared, "pending")
	if err != nil {
		return StoredOperation{}, s, false, err
	}
	stored, made, err := c.journal.Admit(r, i, result)
	return stored, s, made, err
}

func (c *Coordinator) advanceAdoption(stored StoredOperation, s adoptionEvidence, phase Phase, step string) (StoredOperation, error) {
	s.Step = step
	raw, err := encode(s)
	if err != nil {
		return stored, err
	}
	outcome := "pending"
	if terminal(phase) || phase == Degraded {
		outcome = string(phase)
	}
	result, err := applyResult(stored, applyEvidence{}, phase, outcome)
	if err != nil {
		return stored, err
	}
	return c.journal.Advance(stored.Record.Identity.OperationID, stored.SHA256, phase, raw, result)
}

func (c *Coordinator) reconcileAdoption(ctx context.Context, target StoredOperation, verify recoveredRunVerifier, boundary func(string) error) (StoredOperation, error) {
	s, err := decodeAdoption(target)
	if err == nil {
		return c.executeAdoption(ctx, target, s, verify, boundary)
	}
	if terminal(target.Record.Phase) {
		return target, errors.Join(ErrRecoveryRequired, err)
	}
	// Preserve invalid original evidence; never manufacture an adoption proof.
	// The target and recovery call must both expose their unresolved state.
	result, _ := applyResult(target, applyEvidence{}, Degraded, "degraded")
	next, writeErr := c.journal.Advance(target.Record.Identity.OperationID, target.SHA256, Degraded,
		json.RawMessage(target.Record.Steps[len(target.Record.Steps)-1].Evidence), result)
	if writeErr != nil {
		return target, errors.Join(errRecoveryPersistence, writeErr)
	}
	return next, errors.Join(ErrRecoveryRequired, err)
}

func (c *Coordinator) executeAdoption(ctx context.Context, stored StoredOperation, s adoptionEvidence, verify recoveredRunVerifier, boundary func(string) error) (StoredOperation, error) {
	fail := func(cause error) (StoredOperation, error) {
		if terminal(stored.Record.Phase) || errors.Is(cause, config.ErrUncertain) || errors.Is(cause, errRecoveryPersistence) || errors.Is(cause, errRecoveryCut) {
			return stored, errors.Join(ErrRecoveryRequired, cause)
		}
		next, err := c.advanceAdoption(stored, s, Degraded, "adoption-unverified")
		if err == nil {
			stored = next
		}
		return stored, errors.Join(ErrRecoveryRequired, cause, err)
	}
	prove := func() error {
		if ctx == nil || verify == nil {
			return ErrRequest
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		base, err := c.store.ReadActive()
		if err != nil || !reflect.DeepEqual(base, s.Base) {
			return errors.Join(ErrConflict, err)
		}
		id, err := c.activeRunForApply(base, "replace", "")
		if err != nil || id == nil || *id != s.RunID {
			return errors.Join(ErrRecoveryRequired, err)
		}
		got, err := verify(ctx, s.RunID, *s.Base.OperationID)
		if err != nil || got != s.Identity {
			return errors.Join(ErrRecoveryRequired, err)
		}
		return nil
	}
	if err := prove(); err != nil {
		return fail(err)
	}
	if terminal(stored.Record.Phase) {
		if stored.Record.Phase != Committed {
			return stored, ErrRecoveryRequired
		}
		return stored, nil
	}
	for stored.Record.Phase != Committed {
		var next Phase
		switch stored.Record.Phase {
		case Prepared:
			next = Validated
		case Validated:
			next = Applying
		case Applying, Degraded:
			next = Verifying
		case Verifying:
			next = Publishing
		case Publishing:
			next = Committed
		default:
			return fail(ErrOperationRecord)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if next == Publishing || next == Committed {
			if err := prove(); err != nil {
				return fail(err)
			}
		}
		updated, err := c.advanceAdoption(stored, s, next, "adopt-"+string(next))
		if err != nil {
			return stored, errors.Join(errRecoveryPersistence, err)
		}
		stored = updated
		if err := lifecycleCut(boundary, "adopt-"+string(next)); err != nil {
			return stored, err
		}
	}
	return stored, nil
}
