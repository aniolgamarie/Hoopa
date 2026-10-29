//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	"hoopa/internal/config"
)

// Lifecycle evidence keeps a stopped configuration distinct from an absent
// process. SourceStopped is usable only with the original birth/registration
// and the exact operation which observed that original pidfd exit.
type lifecycleEvidence struct {
	Version         int                  `json:"version"`
	Step            string               `json:"step"`
	Mode            string               `json:"mode"`
	DesiredRuntime  string               `json:"desired_runtime"`
	Base            config.ActivePointer `json:"base"`
	SourceRunID     config.RunID         `json:"source_run_id"`
	ExitOperationID config.ID            `json:"exit_operation_id"`
	ExitGate        string               `json:"exit_gate"`
	SourceStopped   bool                 `json:"source_stopped"`
	Workdir         string               `json:"workdir"`
	TargetRunID     *config.RunID        `json:"target_run_id"`
}

func parseRuntimeParameters(r Request, operation string) (config.Revision, error) {
	flag := "accept_disruption"
	if operation == "runtime.start" {
		flag = "control_only"
	}
	if r.version != "v1" || r.operation != operation || !planFields(r.params, "expected_revision", flag) {
		return "", ErrRequest
	}
	p, _ := planObject(r.params)
	var revision string
	if json.Unmarshal(p["expected_revision"], &revision) != nil {
		return "", ErrRequest
	}
	if _, err := config.ParseID(revision); err != nil {
		return "", ErrRequest
	}
	want := "true"
	if operation == "runtime.start" {
		want = "false" // control-only belongs to service observation, not a mutation.
	}
	if string(p[flag]) != want {
		if operation == "runtime.stop" && string(p[flag]) == "false" {
			return "", ErrConfirmation
		}
		return "", ErrRequest
	}
	return config.Revision(revision), nil
}

func lifecycleTarget(stored StoredOperation, s lifecycleEvidence) config.ActivePointer {
	op := stored.Record.Identity.OperationID
	p := s.Base
	if revision := stored.Record.Identity.Intent.TargetRevision; revision != nil {
		p.Revision = *revision
	}
	p.OperationID = &op
	p.DesiredRuntime = s.DesiredRuntime
	return p
}

func decodeLifecycleEvidence(stored StoredOperation) (lifecycleEvidence, error) {
	var s lifecycleEvidence
	if stored.Record.Compacted || len(stored.Record.Steps) == 0 {
		return s, ErrOperationRecord
	}
	raw := stored.Record.Steps[len(stored.Record.Steps)-1].Evidence
	if json.Unmarshal(raw, &s) != nil {
		return s, ErrOperationRecord
	}
	b, _ := encode(s)
	b, err := config.CanonicalJSON(b)
	i := stored.Record.Identity.Intent
	if err != nil || !bytes.Equal(b, raw) || s.Version != 1 || s.Base.Version != 1 || s.Base.Generation == nil || s.Base.OperationID == nil || !isHash(s.Base.ManifestSHA256) || s.Base.Revision != i.BaseRevision || !reflect.DeepEqual(i.PreviousGeneration, s.Base.Generation) || !reflect.DeepEqual(i.TargetGeneration, s.Base.Generation) || i.CandidateDigest != s.Base.ManifestSHA256 || (i.TargetRevision == nil) != (s.Mode == "restart") {
		return s, ErrOperationRecord
	}
	if _, err := config.ParseID(string(s.SourceRunID)); err != nil {
		return s, ErrOperationRecord
	}
	if s.Mode == "stop" {
		if stored.Record.Identity.Operation != "runtime.stop" || s.DesiredRuntime != "stopped" || s.Base.DesiredRuntime != "running" || s.ExitOperationID != stored.Record.Identity.OperationID || s.ExitGate != "open" || s.TargetRunID != nil || s.Workdir != "" {
			return s, ErrOperationRecord
		}
	} else if s.Mode == "start" {
		if stored.Record.Identity.Operation != "runtime.start" || s.DesiredRuntime != "running" || s.Base.DesiredRuntime != "stopped" || s.ExitOperationID != *s.Base.OperationID || s.ExitGate != "open" || !s.SourceStopped {
			return s, ErrOperationRecord
		}
	} else if s.Mode == "restart" {
		if stored.Record.Identity.Operation != "runtime.start" || s.DesiredRuntime != "running" || s.Base.DesiredRuntime != "running" || s.ExitOperationID != *s.Base.OperationID || s.ExitGate != "closed" || !s.SourceStopped {
			return s, ErrOperationRecord
		}
	} else {
		return s, ErrOperationRecord
	}
	request, err := ParseRequest(stored.Record.Request)
	if err != nil {
		return s, err
	}
	revision, err := parseRuntimeParameters(request, stored.Record.Identity.Operation)
	if err != nil || revision != s.Base.Revision || i.ImpactDigest != hash(request.params) {
		return s, ErrOperationRecord
	}
	return s, nil
}

// runtimeAdmission checks all identities before writing. Staging cleanup is
// explicit Recover's responsibility; ordinary lifecycle calls never repair it.
func (c *Coordinator) runtimeAdmission(ctx context.Context, request Request) (config.ActivePointer, error) {
	var zero config.ActivePointer
	if c.serviceQuiesced {
		return zero, config.ErrClosed
	}
	if ctx == nil {
		return zero, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	revision, err := parseRuntimeParameters(request, request.operation)
	if err != nil {
		return zero, err
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return zero, err
	}
	if base.Revision != revision {
		return zero, ErrConflict
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return zero, err
	}
	for _, id := range ids {
		old, err := c.journal.Get(id)
		if err != nil {
			return zero, err
		}
		if !terminal(old.Record.Phase) {
			return zero, ErrRecoveryRequired
		}
	}
	if err := c.store.CheckOperationAdmission(); err != nil {
		return zero, err
	}
	return base, nil
}

func (c *Coordinator) admitLifecycle(request Request, s lifecycleEvidence) (StoredOperation, bool, error) {
	var targetRevision *config.Revision
	if s.Mode != "restart" {
		var entropy [24]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return StoredOperation{}, false, err
		}
		revision := config.Revision("r-" + hex.EncodeToString(entropy[:]))
		targetRevision = &revision
	}
	i := Intent{BaseRevision: s.Base.Revision, TargetRevision: targetRevision, PreviousGeneration: s.Base.Generation, TargetGeneration: s.Base.Generation, CandidateDigest: s.Base.ManifestSHA256, ImpactDigest: hash(request.params)}
	seed := StoredOperation{Record: Operation{Identity: Identity{OperationID: request.OperationID(), RequestID: request.ID(), Intent: i}}}
	result, err := applyResult(seed, applyEvidence{}, Prepared, "pending")
	if err != nil {
		return StoredOperation{}, false, err
	}
	return c.journal.Admit(request, i, result)
}

func (c *Coordinator) advanceLifecycle(stored StoredOperation, s lifecycleEvidence, phase Phase, step string) (StoredOperation, error) {
	s.Step = step
	evidence, err := encode(s)
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
	return c.journal.Advance(stored.Record.Identity.OperationID, stored.SHA256, phase, evidence, result)
}

type lifecycleStopper interface {
	stop(context.Context, config.RunID, config.ID) error
}

// Stop persists the stopped intent before signalling. The same request is
// replayed without any environment, ownership, or process side effects.
func (a *BasicApplier) Stop(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
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
	if _, err := parseRuntimeParameters(request, "runtime.stop"); err != nil {
		return StoredOperation{}, false, err
	}
	base, err := c.runtimeAdmission(ctx, request)
	if err != nil {
		return StoredOperation{}, false, err
	}
	id, err := c.activeRunForApply(base, "replace", "")
	if err != nil || id == nil {
		return StoredOperation{}, false, errors.Join(ErrRecoveryRequired, err)
	}
	s := lifecycleEvidence{Version: 1, Mode: "stop", DesiredRuntime: "stopped", Base: base, SourceRunID: *id, ExitOperationID: request.OperationID(), ExitGate: "open"}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	stored, made, err := c.admitLifecycle(request, s)
	if err != nil || !made {
		return stored, made, err
	}
	runtime := &basicRuntime{store: c.store}
	stored, err = c.executeStop(ctx, stored, s, runtime, p1Boundary(request))
	return stored, true, err
}

func lifecycleCut(boundary func(string) error, name string) error {
	if boundary != nil {
		if err := boundary(name); err != nil {
			return errors.Join(errRecoveryCut, err)
		}
	}
	return nil
}

func (c *Coordinator) lifecycleFailed(stored StoredOperation, s lifecycleEvidence, cause error) (StoredOperation, error) {
	if terminal(stored.Record.Phase) || errors.Is(cause, config.ErrUncertain) || errors.Is(cause, errRecoveryPersistence) || errors.Is(cause, errRecoveryCut) {
		return stored, errors.Join(ErrRecoveryRequired, cause)
	}
	next, err := c.advanceLifecycle(stored, s, Degraded, "runtime-unverified")
	if err == nil {
		stored = next
	}
	return stored, errors.Join(ErrRecoveryRequired, cause, err)
}

func (c *Coordinator) executeStop(ctx context.Context, stored StoredOperation, s lifecycleEvidence, runtime lifecycleStopper, boundary func(string) error) (StoredOperation, error) {
	advance := func(phase Phase, step string) error {
		next, err := c.advanceLifecycle(stored, s, phase, step)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		stored = next
		return lifecycleCut(boundary, step)
	}
	if stored.Record.Phase == Prepared {
		if err := advance(Validated, "stop-intent-durable"); err != nil {
			return stored, err
		}
	}
	if !s.SourceStopped {
		phase := Applying
		if stored.Record.Phase == Degraded || stored.Record.Phase == Verifying {
			phase = Verifying
		}
		if err := advance(phase, "stop-original"); err != nil {
			return stored, err
		}
		if err := runtime.stop(ctx, s.SourceRunID, stored.Record.Identity.OperationID); err != nil {
			return c.lifecycleFailed(stored, s, err)
		}
		if err := lifecycleCut(boundary, "stop-exit-observed"); err != nil {
			return stored, err
		}
		s.SourceStopped = true
	}
	if err := c.verifyLifecycleExit(s); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if stored.Record.Phase != Publishing {
		if err := advance(Verifying, "stop-exit-durable"); err != nil {
			return stored, err
		}
		if err := advance(Publishing, "stop-publish"); err != nil {
			return stored, err
		}
	}
	desired := lifecycleTarget(stored, s)
	current, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(current, s.Base) && !reflect.DeepEqual(current, desired) {
		return c.lifecycleFailed(stored, s, errors.Join(ErrConflict, err))
	}
	if !reflect.DeepEqual(current, desired) {
		if err := c.store.PublishActive(desired); err != nil {
			return stored, errors.Join(errRecoveryPersistence, err)
		}
	}
	if err := lifecycleCut(boundary, "stop-active-published"); err != nil {
		return stored, err
	}
	if err := c.verifyLifecycleExit(s); err != nil {
		return c.lifecycleFailed(stored, s, err)
	}
	if err := advance(Committed, "stop-committed"); err != nil {
		return stored, err
	}
	return stored, nil
}

func (c *Coordinator) verifyLifecycleExit(s lifecycleEvidence) error {
	if !s.SourceStopped || s.Base.Generation == nil || s.Base.OperationID == nil {
		return ErrRecoveryRequired
	}
	intent, err := c.stoppedRunOriginals(s.SourceRunID, s.ExitOperationID, s.ExitGate)
	if err != nil || intent.Generation != *s.Base.Generation || intent.ManifestSHA256 != s.Base.ManifestSHA256 {
		return errors.Join(ErrRecoveryRequired, err)
	}
	if (s.Mode == "stop" || s.Mode == "restart") && intent.OperationID != *s.Base.OperationID {
		return ErrRecoveryRequired
	}
	if s.Mode == "start" {
		previous, err := c.verifiedStopped(s.Base)
		if err != nil || previous.SourceRunID != s.SourceRunID || previous.ExitOperationID != s.ExitOperationID || s.ExitGate != "open" {
			return errors.Join(ErrRecoveryRequired, err)
		}
	}
	ids, err := c.store.ListRunIDs()
	if err != nil {
		return err
	}
	matches := 0
	for _, id := range ids {
		candidate, _, err := c.store.ReadRunIntent(id)
		if err != nil {
			return err
		}
		if candidate.OperationID == intent.OperationID && candidate.Generation == intent.Generation {
			if id != s.SourceRunID || candidate.ManifestSHA256 != intent.ManifestSHA256 {
				return config.ErrRunConflict
			}
			matches++
		}
	}
	if matches != 1 {
		return config.ErrRunConflict
	}
	return nil
}

func (c *Coordinator) verifiedStopped(base config.ActivePointer) (lifecycleEvidence, error) {
	var zero lifecycleEvidence
	if base.DesiredRuntime != "stopped" || base.Generation == nil || base.OperationID == nil {
		return zero, ErrRecoveryRequired
	}
	op, err := c.journal.Get(*base.OperationID)
	if err != nil || op.Record.Phase != Committed {
		return zero, errors.Join(ErrRecoveryRequired, err)
	}
	s, err := decodeLifecycleEvidence(op)
	if err != nil || s.Mode != "stop" || !reflect.DeepEqual(lifecycleTarget(op, s), base) {
		return zero, ErrRecoveryRequired
	}
	if err := c.verifyLifecycleExit(s); err != nil {
		return zero, err
	}
	return s, nil
}
