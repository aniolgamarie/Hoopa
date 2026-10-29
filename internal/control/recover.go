//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/p1testhook"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

type recoverParameters struct {
	OperationID      config.ID       `json:"operation_id"`
	ExpectedRevision config.Revision `json:"expected_revision"`
}

func parseRecoverParameters(request Request) (recoverParameters, error) {
	var p recoverParameters
	if request.version != "v1" || request.operation != "operation.recover" || !planFields(request.params, "operation_id", "expected_revision") || json.Unmarshal(request.params, &p) != nil {
		return p, ErrRequest
	}
	if _, err := config.ParseID(string(p.OperationID)); err != nil {
		return p, ErrRequest
	}
	if _, err := config.ParseID(string(p.ExpectedRevision)); err != nil {
		return p, ErrRequest
	}
	return p, nil
}

// Recover is an explicit mutation with its own request identity. Apply recovery
// reconciles an already-open target/rollback. Runtime stop/start recovery can
// finish a proved original stop or activate a proved existing closed start.
// It never spawns or recreates a consumed launch ticket. Unproved startup/exit
// windows remain degraded for explicit/manual resolution.
func (a *BasicApplier) Recover(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
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
	return c.recoverBasicLocked(ctx, request, c.verifyRecoveredRun, p1Boundary(request))
}

type recoveredRunVerifier func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error)

func (c *Coordinator) verifyRecoveredRun(ctx context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
	var zero platform.ProcessIdentity
	intent, _, err := c.store.ReadRunIntent(id)
	if err != nil {
		return zero, err
	}
	capability, err := mihomo.ApprovedCapability(intent.Binary)
	if err != nil || capability.Binary != intent.Binary {
		return zero, ErrCapabilityUnsupported
	}
	controller, err := mihomo.NewController(c.store, capability)
	if err != nil {
		return zero, err
	}
	owner, err := owned.NewOwnership(c.store, controller)
	if err != nil {
		return zero, err
	}
	run, err := owner.Observe(ctx, id)
	if err != nil {
		return zero, err
	}
	defer run.Close()
	if err := controller.VerifyActivation(ctx, run, op); err != nil {
		return zero, err
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("control", "activation-verified", string(op), string(id), "", false, map[string]any{"identity": run.Identity(), "observation": run.Observation()})
	}
	return run.Identity(), nil
}

func recoveryCallBound(call, target StoredOperation) bool {
	if call.Record.Phase == RollingBack {
		return false
	}
	r, err := ParseRequest(call.Record.Request)
	if err != nil {
		return false
	}
	p, err := parseRecoverParameters(r)
	i := call.Record.Identity.Intent
	return err == nil && p.OperationID == target.Record.Identity.OperationID && i.BaseRevision == p.ExpectedRevision && i.TargetRevision == nil && i.TargetGeneration == nil && i.CandidateDigest == target.Record.Identity.RequestDigest && i.ImpactDigest == hash(r.params)
}

func recoveryResult(call StoredOperation, phase Phase) json.RawMessage {
	var failure any
	outcome := "pending"
	if phase == Committed {
		outcome = "committed"
	}
	if phase == Degraded {
		outcome = "degraded"
		failure = map[string]any{"code": "RECOVERY_REQUIRED", "message": "The original operation still requires recovery", "retryable": false, "details": map[string]any{"operation_id": call.Record.Identity.OperationID, "reason": "degraded"}}
	}
	var data any = map[string]any{"operation_id": call.Record.Identity.OperationID, "phase": phase, "base_revision": call.Record.Identity.Intent.BaseRevision, "target_revision": nil, "outcome": outcome}
	if failure != nil {
		data = nil
	}
	raw, _ := encode(map[string]any{"api_version": "v1", "request_id": call.Record.Identity.RequestID, "ok": phase != Degraded,
		"data":  data,
		"error": failure, "warnings": []any{}})
	return raw
}

// The only injectable values are package-private ordering-test seams. Public
// Recover always uses actual ownership+snapshot+activation proof, with no cuts.
func (c *Coordinator) recoverBasicLocked(ctx context.Context, request Request, verify recoveredRunVerifier, boundary func(string) error) (StoredOperation, bool, error) {
	fail := func(err error) (StoredOperation, bool, error) { return StoredOperation{}, false, err }
	if c.store == nil {
		return fail(config.ErrClosed)
	}
	if old, err := c.journal.Lookup(request); !errors.Is(err, ErrOperationNotFound) {
		return old, false, err
	}
	if c.serviceQuiesced {
		return fail(config.ErrClosed)
	}
	if ctx == nil || verify == nil {
		return fail(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	p, err := parseRecoverParameters(request)
	if err != nil || p.OperationID == request.OperationID() {
		return fail(ErrRequest)
	}
	active, err := c.store.ReadActive()
	if err != nil {
		return fail(err)
	}
	if active.Revision != p.ExpectedRevision {
		return fail(ErrConflict)
	}
	target, err := c.journal.Get(p.OperationID)
	if err != nil {
		return fail(err)
	}
	sourceTarget := target.Record.Identity.Operation == "subscription.update"
	if target.Record.Compacted && !sourceTarget || target.Record.Identity.Operation != "config.apply" && target.Record.Identity.Operation != "runtime.stop" && target.Record.Identity.Operation != "runtime.start" && target.Record.Identity.Operation != "service.stop" && !sourceTarget {
		return fail(ErrRecoveryRequired)
	}
	if sourceTarget {
		if _, err := c.sourceRecoveryEntryLocked(target); err != nil {
			return fail(err)
		}
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return fail(err)
	}
	if len(ids) >= config.MaxOperationFiles {
		return fail(config.ErrOperationCapacity)
	}
	var related []config.ID
	for _, id := range ids {
		record, err := c.journal.Get(id)
		if err != nil {
			return fail(err)
		}
		if id == p.OperationID || terminal(record.Record.Phase) {
			continue
		}
		if !recoveryCallBound(record, target) {
			if target.Record.Identity.Operation == "service.stop" || sourceTarget {
				continue // Resolve this prior service lease without changing unrelated core work.
			}
			return fail(ErrRecoveryRequired)
		}
		related = append(related, id)
	}
	// A terminal running target must already be independently provable before
	// even staging maintenance or a new recovery receipt can change the store.
	if err := c.preflightTerminalOwnership(ctx, target, verify); err != nil {
		return fail(err)
	}
	// Maintenance is authorized only after full new-request/revision/history
	// and applicable ownership validation, never on a replay.
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := c.store.MaintainOperationStaging(); err != nil {
		return fail(err)
	}
	if err := c.store.CheckOperationAdmission(); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	intent := Intent{BaseRevision: active.Revision, PreviousGeneration: active.Generation, CandidateDigest: target.Record.Identity.RequestDigest, ImpactDigest: hash(request.params)}
	seed := StoredOperation{Record: Operation{Identity: Identity{OperationID: request.OperationID(), RequestID: request.ID(), Intent: intent}}}
	call, made, err := c.journal.Admit(request, intent, recoveryResult(seed, Prepared))
	if err != nil || !made {
		return call, made, err
	}
	cut := func(name string) error {
		if boundary != nil {
			return boundary(name)
		}
		return nil
	}
	advanceCall := func(record StoredOperation, phase Phase) (StoredOperation, error) {
		evidence, _ := encode(map[string]any{"version": 1, "target_operation_id": p.OperationID, "target_request_digest": target.Record.Identity.RequestDigest, "target_record_sha256": target.SHA256, "target_phase": target.Record.Phase})
		return c.journal.Advance(record.Record.Identity.OperationID, record.SHA256, phase, evidence, recoveryResult(record, phase))
	}
	for _, phase := range []Phase{Validated, Applying} {
		next, err := advanceCall(call, phase)
		if err != nil {
			return call, true, err
		}
		call = next
	}
	if err := cut("recover-admitted"); err != nil {
		return call, true, err
	}
	if sourceTarget {
		target, err = c.reconcileSourceUpdate(ctx, target, boundary)
	} else if target.Record.Identity.Operation == "service.stop" {
		target, err = c.reconcileServiceStop(ctx, target, boundary)
	} else if target.Record.Identity.Operation == "config.apply" {
		target, err = c.reconcileBasicApply(ctx, target, verify, boundary)
	} else if isAdoption(target) {
		target, err = c.reconcileAdoption(ctx, target, verify, boundary)
	} else {
		target, err = c.reconcileLifecycle(ctx, target, boundary)
	}
	if err != nil {
		if errors.Is(err, config.ErrUncertain) || errors.Is(err, errRecoveryPersistence) || errors.Is(err, errRecoveryCut) {
			return call, true, err
		}
		next, logErr := advanceCall(call, Degraded)
		if logErr == nil {
			call = next
		}
		return call, true, errors.Join(ErrRecoveryRequired, err, logErr)
	}
	if err := cut("recover-target-terminal"); err != nil {
		return call, true, err
	}
	// Two records are not one transaction. A later explicit recovery can finish
	// these bound older calls after independently re-verifying the target again.
	related = append(related, call.Record.Identity.OperationID)
	for _, id := range related {
		old, err := c.journal.Get(id)
		if err != nil {
			return call, true, err
		}
		if !recoveryCallBound(old, target) {
			return call, true, ErrOperationRecord
		}
		for !terminal(old.Record.Phase) {
			if err := ctx.Err(); err != nil {
				return call, true, err
			}
			var phase Phase
			switch old.Record.Phase {
			case Prepared:
				phase = Validated
			case Validated:
				phase = Applying
			case Applying, Degraded:
				phase = Verifying
			case Verifying:
				phase = Publishing
			case Publishing:
				phase = Committed
			default:
				return call, true, ErrOperationRecord
			}
			next, err := advanceCall(old, phase)
			if err != nil {
				return call, true, err
			}
			old = next
			if old.Record.Identity.OperationID == call.Record.Identity.OperationID {
				call = old
			}
		}
	}
	return call, true, nil
}

var errRecoveryPersistence = errors.New("recovery persistence stopped")
var errRecoveryCut = errors.New("recovery interrupted")

func decodeApplyEvidence(target StoredOperation) (applyEvidence, error) {
	var state applyEvidence
	if len(target.Record.Steps) == 0 {
		return state, ErrOperationRecord
	}
	raw := target.Record.Steps[len(target.Record.Steps)-1].Evidence
	if json.Unmarshal(raw, &state) != nil {
		return state, ErrOperationRecord
	}
	encoded, _ := encode(state)
	canonical, err := config.CanonicalJSON(encoded)
	if err != nil || !bytes.Equal(canonical, raw) || state.Version != 1 {
		return state, ErrOperationRecord
	}
	if state.TargetExit != nil && (!state.TargetStopped || !state.OldStopped || state.TargetRunID == nil || state.Base.Generation == nil || state.TargetExit.RunID != *state.TargetRunID || state.TargetExit.OperationID != target.Record.Identity.OperationID) {
		return state, ErrOperationRecord
	}
	i := target.Record.Identity.Intent
	if i.TargetRevision == nil || i.TargetGeneration == nil || state.Base.Version != 1 || state.Base.Revision != i.BaseRevision || !reflect.DeepEqual(state.Base.Generation, i.PreviousGeneration) || !isHash(state.TargetManifestSHA256) || state.OldRunID == nil != (state.Base.Generation == nil) {
		return state, ErrOperationRecord
	}
	if state.Base.Generation == nil {
		if state.Base.DesiredRuntime != "stopped" || state.Base.ManifestSHA256 != "" || state.Base.OperationID != nil {
			return state, ErrOperationRecord
		}
	} else {
		if state.Base.DesiredRuntime != "running" || !isHash(state.Base.ManifestSHA256) || state.Base.OperationID == nil || *state.Base.Generation == *i.TargetGeneration {
			return state, ErrOperationRecord
		}
		if _, err := config.ParseID(string(*state.Base.OperationID)); err != nil {
			return state, ErrOperationRecord
		}
	}
	return state, nil
}

// A persisted exit fact is usable only with the immutable identity originals
// that the live coordinator checked before observing that pidfd exit. This does
// not re-observe an exited PID or infer an exit from a missing /proc entry.
func (c *Coordinator) stoppedRunOriginals(id config.RunID, operation config.ID, gate string) (config.RunIntent, error) {
	intent, intentSHA, err := c.store.ReadRunIntent(id)
	if err != nil {
		return config.RunIntent{}, err
	}
	birth, birthSHA, err := c.store.ReadRunBirth(id)
	if err != nil {
		return config.RunIntent{}, err
	}
	reg, _, err := c.store.ReadControlRegistration(id, operation)
	if err != nil {
		return config.RunIntent{}, err
	}
	if reg.Gate != gate {
		return config.RunIntent{}, config.ErrRunConflict
	}
	if err := reg.Match(intent, intentSHA, birth, birthSHA); err != nil {
		return config.RunIntent{}, err
	}
	return intent, nil
}

// This alternate historical proof is published only by the live manager that
// reaped its own launcher before closed registration. Recovery checks the exact
// originals; it never reconstructs Wait, a launch ticket, or signal authority.
func (c *Coordinator) stoppedCandidateOriginals(id config.RunID, operation config.ID, proof *owned.LaunchExitProof) (config.RunIntent, error) {
	if proof == nil {
		return c.stoppedRunOriginals(id, operation, "closed")
	}
	intent, intentSHA, err := c.store.ReadRunIntent(id)
	if err != nil {
		return config.RunIntent{}, err
	}
	birth, birthSHA, err := c.store.ReadRunBirth(id)
	if err != nil {
		return config.RunIntent{}, err
	}
	if intent.OperationID != operation || proof.Match(intent, intentSHA, birth, birthSHA) != nil {
		return config.RunIntent{}, config.ErrRunConflict
	}
	if _, _, err = c.store.ReadControlRegistration(id, operation); !errors.Is(err, os.ErrNotExist) {
		return config.RunIntent{}, errors.Join(config.ErrRunConflict, err)
	}
	return intent, nil
}

func (c *Coordinator) reconcileBasicApply(ctx context.Context, target StoredOperation, verify recoveredRunVerifier, boundary func(string) error) (StoredOperation, error) {
	state, decodeErr := decodeApplyEvidence(target)
	op := target.Record.Identity.OperationID
	advance := func(phase Phase, step string) error {
		state.Step = step
		evidence, _ := encode(state)
		if decodeErr != nil {
			evidence = append([]byte(nil), target.Record.Steps[len(target.Record.Steps)-1].Evidence...)
		}
		outcome := "pending"
		if terminal(phase) || phase == Degraded {
			outcome = string(phase)
		}
		result, err := applyResult(target, state, phase, outcome)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		next, err := c.journal.Advance(op, target.SHA256, phase, evidence, result)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		target = next
		return nil
	}
	failed := func(cause error) (StoredOperation, error) {
		if terminal(target.Record.Phase) || errors.Is(cause, config.ErrUncertain) {
			return target, errors.Join(ErrRecoveryRequired, cause)
		}
		err := advance(Degraded, "recovery-unverified")
		return target, errors.Join(ErrRecoveryRequired, cause, err)
	}
	if decodeErr != nil {
		return failed(decodeErr)
	}
	rollback := false
	for _, step := range target.Record.Steps {
		if step.Phase == RollingBack {
			rollback = true
		}
	}
	i := target.Record.Identity.Intent
	id := state.TargetRunID
	desired := config.ActivePointer{Version: 1, Revision: *i.TargetRevision, Generation: i.TargetGeneration, ManifestSHA256: state.TargetManifestSHA256, OperationID: &op, DesiredRuntime: "running"}
	finalPhase := Committed
	if rollback {
		if !state.TargetStopped || state.RollbackRunID == nil || state.Base.Generation == nil {
			return failed(ErrRecoveryRequired)
		}
		id, desired, finalPhase = state.RollbackRunID, state.Base, RolledBack
		desired.OperationID = &op
	} else if state.TargetStopped || state.RollbackRunID != nil || state.RollbackWorkdir != "" {
		return failed(ErrRecoveryRequired)
	}
	if !state.OldStopped || id == nil {
		return failed(ErrRecoveryRequired)
	}
	if state.OldRunID != nil {
		old, err := c.stoppedRunOriginals(*state.OldRunID, op, "open")
		if err != nil || old.OperationID != *state.Base.OperationID || old.Generation != *state.Base.Generation || old.ManifestSHA256 != state.Base.ManifestSHA256 {
			return failed(errors.Join(config.ErrRunConflict, err))
		}
	}
	if rollback && state.TargetRunID != nil {
		candidate, err := c.stoppedCandidateOriginals(*state.TargetRunID, op, state.TargetExit)
		if err != nil || candidate.OperationID != op || candidate.Generation != *i.TargetGeneration || candidate.ManifestSHA256 != state.TargetManifestSHA256 {
			return failed(errors.Join(config.ErrRunConflict, err))
		}
	}
	if terminal(target.Record.Phase) && target.Record.Phase != finalPhase {
		return target, ErrOperationRecord
	}
	if !rollback && target.Record.Phase != Verifying && target.Record.Phase != Publishing && target.Record.Phase != Degraded && target.Record.Phase != Committed {
		return failed(ErrRecoveryRequired)
	}
	ids, err := c.store.ListRunIDs()
	if err != nil {
		return failed(err)
	}
	matched, oldMatched := 0, 0
	for _, candidate := range ids {
		run, _, err := c.store.ReadRunIntent(candidate)
		if err != nil {
			return failed(err)
		}
		if run.OperationID == op && run.Generation == *desired.Generation {
			if candidate != *id || run.ManifestSHA256 != desired.ManifestSHA256 {
				return failed(config.ErrRunConflict)
			}
			matched++
		}
		if state.OldRunID != nil && run.OperationID == *state.Base.OperationID && run.Generation == *state.Base.Generation {
			if candidate != *state.OldRunID || run.ManifestSHA256 != state.Base.ManifestSHA256 {
				return failed(config.ErrRunConflict)
			}
			oldMatched++
		}
	}
	if matched != 1 || state.OldRunID != nil && oldMatched != 1 {
		return failed(config.ErrRunConflict)
	}
	current, err := c.store.ReadActive()
	targetPointer := config.ActivePointer{Version: 1, Revision: *i.TargetRevision, Generation: i.TargetGeneration, ManifestSHA256: state.TargetManifestSHA256, OperationID: &op, DesiredRuntime: "running"}
	if err != nil || !reflect.DeepEqual(current, state.Base) && !reflect.DeepEqual(current, targetPointer) && !reflect.DeepEqual(current, desired) {
		return target, errors.Join(ErrConflict, err)
	}
	first, err := verify(ctx, *id, op)
	if err != nil {
		return failed(err)
	}
	if !terminal(target.Record.Phase) {
		phase := Verifying
		if rollback {
			phase = RollingBack
		} else if target.Record.Phase == Publishing {
			phase = Publishing
		}
		if err := advance(phase, "recovery-run-verified"); err != nil {
			return target, err
		}
		if !rollback && phase != Publishing {
			if err := advance(Publishing, "recovery-publish-target"); err != nil {
				return target, err
			}
		}
		if !reflect.DeepEqual(current, desired) {
			if err := c.store.PublishActive(desired); err != nil {
				return target, errors.Join(errRecoveryPersistence, err)
			}
		}
	} else if !reflect.DeepEqual(current, desired) {
		return target, ErrConflict
	}
	if boundary != nil {
		if err := boundary("recover-active-published"); err != nil {
			return target, errors.Join(errRecoveryCut, err)
		}
	}
	last, err := verify(ctx, *id, op)
	if err != nil || first != last {
		return failed(errors.Join(owned.ErrOwnershipUnknown, err))
	}
	if !terminal(target.Record.Phase) {
		if err := advance(finalPhase, "recovery-committed"); err != nil {
			return target, err
		}
	}
	return target, nil
}
