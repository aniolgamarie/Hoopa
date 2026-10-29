//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"hoopa/internal/config"
	"hoopa/internal/p1testhook"
	owned "hoopa/internal/runtime"
)

var ErrApplyIncomplete = errors.New("APPLY_REQUIRES_RECOVERY")

// BasicApplier implements the approved closed-start/full-replacement profile.
// source is trusted, local compilation code; launcherArgs is a fixed internal
// self-launcher dispatch, never an argument supplied by a config.apply request.
// The coordinator's shared lock covers candidate publication through commit.
type BasicApplier struct {
	coordinator  *Coordinator
	source       BasicMaterialSource
	launcherArgs []string
	sources      bool
}

func NewBasicApplier(c *Coordinator, source BasicMaterialSource, launcherArgs []string) (*BasicApplier, error) {
	if c == nil || c.coordinatorState == nil || source == nil || len(launcherArgs) == 0 || len(launcherArgs) > 16 {
		return nil, ErrRequest
	}
	for _, arg := range launcherArgs {
		if len(arg) > 256 || strings.ContainsRune(arg, 0) {
			return nil, ErrRequest
		}
	}
	return &BasicApplier{coordinator: c, source: source, launcherArgs: append([]string(nil), launcherArgs...)}, nil
}

// Apply replays a known request before resolving material or doing any effect.
// Failures after admission preserve the operation. A failed replacement restores
// the old bundle only after proving the candidate stopped; uncertainty requires
// explicit recovery and never implies that a failed launch had no effect.
func (a *BasicApplier) Apply(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
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
	if ctx == nil {
		return StoredOperation{}, false, ErrRequest
	}
	validator, current, source := c.validator, c.current, a.source
	if a.sources {
		validator, current, source, err = c.sourceApplyDependenciesLocked(ctx, request)
		if err != nil {
			return StoredOperation{}, false, err
		}
	}
	var material checkedBasicMaterial
	var state applyEvidence
	stored, made, _, err := c.admitCheckedApplyUsingLocked(ctx, request, validator, current, func(plan Plan, base config.ActivePointer) error {
		input, err := source(ctx, plan)
		if err != nil {
			return config.ErrReadUnavailable
		}
		var old *config.RunID
		if a.sources {
			material, old, err = c.prepareSourceMaterialLocked(ctx, request, plan, base, input, p1Boundary(request))
		} else {
			material, err = checkBasicMaterial(ctx, plan, input)
		}
		if err != nil {
			return err
		}
		if p1testhook.Enabled {
			// Validation is complete before candidate publication or admission.
			_ = p1testhook.Record("control", "candidate-validated", "", "", request.ID(), false, map[string]any{"generation": material.manifest.Generation, "manifest_sha256": material.launch.ManifestSHA256, "config_sha256": material.launch.ConfigSHA256})
		}
		if !a.sources {
			old, err = c.activeRunForApply(base, plan.Document().Impact.RuntimeAction, material.launch.Workdir)
			if err != nil {
				return err
			}
		}
		state = applyEvidence{Version: 1, Base: base, OldRunID: old, OldStopped: old == nil, TargetManifestSHA256: material.launch.ManifestSHA256}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.store.CheckOperationAdmission(); err != nil {
			return err
		}
		sha, err := c.store.PublishBundle(material.manifest, material.files)
		if err != nil {
			return err
		}
		if sha != state.TargetManifestSHA256 {
			return config.ErrIntegrity
		}
		if p1testhook.Enabled {
			// The candidate exists, but this request has not been admitted yet.
			_ = p1testhook.Record("store", "bundle-published", "", "", request.ID(), false, map[string]any{"generation": material.manifest.Generation, "manifest_sha256": sha})
		}
		return nil
	})
	if err != nil || !made {
		return stored, made, err
	}
	runtime, err := newBasicApplyRuntime(c.store, material, a.launcherArgs)
	if err != nil {
		return stored, true, errors.Join(ErrApplyIncomplete, err)
	}
	defer runtime.close()
	stored, err = c.executeBasicApply(ctx, stored, state, runtime, p1Boundary(request))
	return stored, true, err
}

// Active operation identifies the authority publication, including a future
// verified rollback publication. Generation alone cannot name a process: the
// same old bundle can have a new rollback run. Ambiguous/missing linkage fails.
func (c *Coordinator) activeRunForApply(base config.ActivePointer, action, workdir string) (*config.RunID, error) {
	ids, err := c.store.ListRunIDs()
	if err != nil {
		return nil, err
	}
	if len(ids) >= config.MaxRuns {
		return nil, config.ErrIntegrity
	}
	if base.DesiredRuntime == "stopped" {
		// T032 supplies the later stopped-with-history proof. Absence of a birth
		// or a failed Observe is never used to authorize this first start.
		if action != "start" || base.Generation != nil || base.OperationID != nil || len(ids) != 0 {
			return nil, ErrRecoveryRequired
		}
		history, err := c.journal.IDs()
		if err != nil {
			return nil, errors.Join(ErrRecoveryRequired, err)
		}
		for _, id := range history {
			record, err := c.journal.Get(id)
			if err != nil || !c.configurationNeutralHistory(record, base) {
				return nil, errors.Join(ErrRecoveryRequired, err)
			}
		}
		return nil, nil
	}
	if action != "replace" || base.Generation == nil || base.OperationID == nil {
		return nil, ErrConflict
	}
	op, err := c.journal.Get(*base.OperationID)
	if err != nil || !terminal(op.Record.Phase) {
		return nil, errors.Join(ErrRecoveryRequired, err)
	}
	i := op.Record.Identity.Intent
	if op.Record.Identity.Operation == "runtime.start" {
		state, err := decodeLifecycleEvidence(op)
		if err != nil || state.Mode != "start" && state.Mode != "restart" || op.Record.Phase != Committed || state.TargetRunID == nil || !reflect.DeepEqual(lifecycleTarget(op, state), base) {
			return nil, ErrRecoveryRequired
		}
		if err := c.verifyLifecycleExit(state); err != nil {
			return nil, err
		}
	} else if op.Record.Identity.Operation != "config.apply" || op.Record.Phase == Committed && (i.TargetRevision == nil || *i.TargetRevision != base.Revision || i.TargetGeneration == nil || *i.TargetGeneration != *base.Generation) ||
		op.Record.Phase == RolledBack && (i.BaseRevision != base.Revision || i.PreviousGeneration == nil || *i.PreviousGeneration != *base.Generation) {
		return nil, ErrRecoveryRequired
	}
	var match *config.RunID
	for _, id := range ids {
		intent, _, err := c.store.ReadRunIntent(id)
		if err != nil {
			return nil, err
		}
		if intent.Workdir == workdir {
			return nil, config.ErrRunConflict
		}
		// An orphan intent may represent a child from an interrupted launch.
		// A missing journal entry must not make that history invisible.
		origin, err := c.journal.Get(intent.OperationID)
		if err != nil || !terminal(origin.Record.Phase) {
			return nil, errors.Join(ErrRecoveryRequired, err)
		}
		if intent.OperationID == *base.OperationID && intent.Generation == *base.Generation && intent.ManifestSHA256 == base.ManifestSHA256 {
			if match != nil {
				return nil, ErrRecoveryRequired
			}
			copy := id
			match = &copy
		}
	}
	if match == nil {
		return nil, ErrRecoveryRequired
	}
	if op.Record.Identity.Operation == "runtime.start" {
		state, _ := decodeLifecycleEvidence(op)
		if *match != *state.TargetRunID {
			return nil, ErrRecoveryRequired
		}
	}
	return match, nil
}

// Each step retains the full linkage needed to reconcile a crash. A RunID is
// evidence to inspect, never a reconstructed one-use launch permission.
type applyEvidence struct {
	Version              int                    `json:"version"`
	Step                 string                 `json:"step"`
	Base                 config.ActivePointer   `json:"base"`
	TargetManifestSHA256 string                 `json:"target_manifest_sha256"`
	OldRunID             *config.RunID          `json:"old_run_id"`
	TargetRunID          *config.RunID          `json:"target_run_id"`
	OldStopped           bool                   `json:"old_stopped"`
	TargetStopped        bool                   `json:"target_stopped"`
	TargetExit           *owned.LaunchExitProof `json:"target_exit,omitempty"`
	RollbackWorkdir      string                 `json:"rollback_workdir"`
	RollbackRunID        *config.RunID          `json:"rollback_run_id"`
}

// This private interface separates ordering tests from Linux process evidence.
// The public entry always constructs the concrete verified basic runtime.
type basicApplyRuntime interface {
	stop(context.Context, config.RunID, config.ID) error
	prepare(config.ID) (config.RunID, error)
	start(context.Context, config.RunID) error
	verifyClosed(context.Context, config.RunID, config.ID) error
	activate(context.Context, config.RunID, config.ID) error
	verifyOpen(context.Context, config.RunID) error
}

// executeBasicApply is called with the coordinator lock and a new durable
// prepared record. boundary is test-only interruption, never a config option.
func (c *Coordinator) executeBasicApply(ctx context.Context, stored StoredOperation, state applyEvidence, runtime basicApplyRuntime, boundary func(string) error) (StoredOperation, error) {
	op := stored.Record.Identity.OperationID
	cut := func(name string) error {
		if boundary != nil {
			return boundary(name)
		}
		return nil
	}
	advance := func(phase Phase, step, outcome string) error {
		state.Step = step
		evidence, err := encode(state)
		if err != nil {
			return err
		}
		result, err := applyResult(stored, state, phase, outcome)
		if err != nil {
			return err
		}
		next, err := c.journal.Advance(op, stored.SHA256, phase, evidence, result)
		if err != nil {
			return err // No further effects or attempts to log after write failure.
		}
		stored = next
		return cut(step)
	}
	failed := func(cause error) (StoredOperation, error) {
		if errors.Is(cause, config.ErrUncertain) {
			return stored, errors.Join(ErrApplyIncomplete, cause)
		}
		if rollback, ok := runtime.(basicRollbackRuntime); ok && state.OldRunID != nil && state.OldStopped {
			return c.executeBasicRollback(ctx, stored, state, rollback, cause, boundary)
		}
		err := advance(Degraded, "runtime-unverified", "degraded")
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := advance(Validated, "candidate-validated", "pending"); err != nil {
		return stored, err
	}
	if state.OldRunID != nil {
		if err := advance(Applying, "stop-old", "pending"); err != nil {
			return stored, err
		}
		if err := runtime.stop(ctx, *state.OldRunID, op); err != nil {
			return failed(err)
		}
		if err := cut("old-stop-observed"); err != nil {
			return stored, err
		}
		state.OldStopped = true
	}
	if err := advance(Applying, "prepare-target", "pending"); err != nil {
		return stored, err
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	id, err := runtime.prepare(op)
	if err != nil {
		return failed(err)
	}
	// A crash here leaves a discoverable RunIntent linked to this operation.
	// Recover must inspect it and must never create another launch ticket.
	if err := cut("target-intent-published"); err != nil {
		return stored, err
	}
	intent, _, err := c.store.ReadRunIntent(id)
	if err != nil || intent.OperationID != op || intent.Generation != *stored.Record.Identity.Intent.TargetGeneration || intent.ManifestSHA256 != state.TargetManifestSHA256 {
		return failed(errors.Join(config.ErrIntegrity, err))
	}
	state.TargetRunID = &id
	if err := advance(Applying, "spawn-target", "pending"); err != nil {
		return stored, err
	}
	if err := runtime.start(ctx, id); err != nil {
		return failed(err)
	}
	if err := cut("spawn-returned-before-registration"); err != nil {
		return stored, err
	}
	if err := advance(Verifying, "target-started", "pending"); err != nil {
		return stored, err
	}
	if err := runtime.verifyClosed(ctx, id, op); err != nil {
		return failed(err)
	}
	if err := advance(Verifying, "activate-target", "pending"); err != nil {
		return stored, err
	}
	if err := runtime.activate(ctx, id, op); err != nil {
		return failed(err)
	}
	if err := cut("entry-opened"); err != nil {
		return stored, err
	}
	if err := runtime.verifyOpen(ctx, id); err != nil {
		return failed(err)
	}
	if err := advance(Publishing, "target-verified", "pending"); err != nil {
		return stored, err
	}
	current, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(current, state.Base) {
		return failed(errors.Join(ErrConflict, err))
	}
	i := stored.Record.Identity.Intent
	target := config.ActivePointer{Version: 1, Revision: *i.TargetRevision, Generation: i.TargetGeneration, ManifestSHA256: state.TargetManifestSHA256, OperationID: &op, DesiredRuntime: "running"}
	if err := c.store.PublishActive(target); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, err)
	}
	if err := cut("active-published"); err != nil {
		return stored, err
	}
	if err := runtime.verifyOpen(ctx, id); err != nil {
		return failed(err)
	}
	if err := advance(Committed, "target-committed", "committed"); err != nil {
		return stored, err
	}
	return stored, nil
}

func applyResult(stored StoredOperation, state applyEvidence, phase Phase, outcome string) (json.RawMessage, error) {
	var failure any
	if phase == Degraded {
		failure = map[string]any{"code": "RECOVERY_REQUIRED", "message": "Runtime state requires explicit recovery", "retryable": false, "details": map[string]any{"operation_id": stored.Record.Identity.OperationID, "reason": "degraded"}}
	}
	if phase == RolledBack {
		failure = map[string]any{"code": "APPLY_FAILED", "message": "The replacement failed; the previous configuration was restored", "retryable": false, "details": map[string]any{"operation_id": stored.Record.Identity.OperationID, "reason": "rolled_back"}}
	}
	if phase == RollingBack {
		outcome = "pending"
	}
	var data any = map[string]any{"operation_id": stored.Record.Identity.OperationID, "phase": phase, "outcome": outcome,
		"base_revision": stored.Record.Identity.Intent.BaseRevision, "target_revision": stored.Record.Identity.Intent.TargetRevision}
	if failure != nil {
		data = nil
	}
	return encode(map[string]any{
		"api_version": "v1", "request_id": stored.Record.Identity.RequestID, "ok": phase != Degraded && phase != RolledBack,
		"data":  data,
		"error": failure, "warnings": []any{},
	})
}
