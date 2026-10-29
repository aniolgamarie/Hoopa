//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
)

var ErrApplyRolledBack = errors.New("APPLY_ROLLED_BACK")

type closableApplyRuntime interface {
	basicApplyRuntime
	close()
}

type basicRollbackRuntime interface {
	stopCandidate(context.Context, config.RunID, config.ID) (*owned.LaunchExitProof, error)
	restore(config.RunID, config.ActivePointer, string) (closableApplyRuntime, error)
}

// Automatic rollback is limited to the still-running coordinator that actually
// observed the old pidfd exit. An explicit recovery must independently reconcile
// the persisted evidence; it must not call this to recreate lost launch tickets.
func (c *Coordinator) executeBasicRollback(ctx context.Context, stored StoredOperation, state applyEvidence, candidate basicRollbackRuntime, cause error, boundary func(string) error) (StoredOperation, error) {
	op := stored.Record.Identity.OperationID
	cut := func(step string) error {
		if boundary != nil {
			return boundary(step)
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
			return err
		}
		stored = next
		return cut(step)
	}
	failed := func(err error) (StoredOperation, error) {
		if errors.Is(err, config.ErrUncertain) {
			return stored, errors.Join(ErrApplyIncomplete, cause, err)
		}
		logErr := advance(Degraded, "rollback-unverified", "degraded")
		return stored, errors.Join(ErrApplyIncomplete, cause, err, logErr)
	}
	if state.OldRunID == nil || !state.OldStopped || state.Base.Generation == nil || state.Base.DesiredRuntime != "running" || state.RollbackRunID != nil || state.RollbackWorkdir != "" {
		return failed(ErrRecoveryRequired)
	}
	if err := advance(RollingBack, "rollback-stop-candidate", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if state.TargetRunID != nil {
		proof, err := candidate.stopCandidate(ctx, *state.TargetRunID, op)
		if err != nil {
			return failed(err)
		}
		if proof != nil {
			intent, err := c.stoppedCandidateOriginals(*state.TargetRunID, op, proof)
			if err != nil || intent.OperationID != op || intent.Generation != *stored.Record.Identity.Intent.TargetGeneration || intent.ManifestSHA256 != state.TargetManifestSHA256 {
				return failed(errors.Join(config.ErrRunConflict, err))
			}
		}
		state.TargetExit = proof
	}
	// No TargetRunID in this live workflow means start was never called. A
	// recovered missing birth/RunID is never interpreted through this shortcut.
	if err := cut("rollback-candidate-exited"); err != nil {
		return stored, err
	}
	state.TargetStopped = true
	var entropy [24]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return failed(err)
	}
	state.RollbackWorkdir = "launch-" + hex.EncodeToString(entropy[:])
	if err := advance(RollingBack, "rollback-prepare-workdir", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := ctx.Err(); err != nil {
		return failed(err)
	}
	restored, err := candidate.restore(*state.OldRunID, state.Base, state.RollbackWorkdir)
	if err != nil {
		return failed(err)
	}
	defer restored.close()
	if err := advance(RollingBack, "rollback-prepare-run", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	id, err := restored.prepare(op)
	if err != nil {
		return failed(err)
	}
	if err := cut("rollback-intent-published"); err != nil {
		return stored, err
	}
	intent, _, err := c.store.ReadRunIntent(id)
	if err != nil || intent.OperationID != op || intent.Generation != *state.Base.Generation || intent.ManifestSHA256 != state.Base.ManifestSHA256 {
		return failed(errors.Join(config.ErrIntegrity, err))
	}
	state.RollbackRunID = &id
	if err := advance(RollingBack, "rollback-spawn", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := restored.start(ctx, id); err != nil {
		return failed(err)
	}
	if err := advance(RollingBack, "rollback-started", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := restored.verifyClosed(ctx, id, op); err != nil {
		return failed(err)
	}
	if err := advance(RollingBack, "rollback-activate", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := restored.activate(ctx, id, op); err != nil {
		return failed(err)
	}
	if err := restored.verifyOpen(ctx, id); err != nil {
		return failed(err)
	}
	if err := advance(RollingBack, "rollback-verified", "rolling_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	i := stored.Record.Identity.Intent
	target := config.ActivePointer{Version: 1, Revision: *i.TargetRevision, Generation: i.TargetGeneration, ManifestSHA256: state.TargetManifestSHA256, OperationID: &op, DesiredRuntime: "running"}
	current, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(current, state.Base) && !reflect.DeepEqual(current, target) {
		return failed(errors.Join(ErrConflict, err))
	}
	previous := state.Base
	previous.OperationID = &op // This operation verified the new old-bundle run.
	if err := c.store.PublishActive(previous); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	if err := cut("rollback-active-published"); err != nil {
		return stored, err
	}
	if err := restored.verifyOpen(ctx, id); err != nil {
		return failed(err)
	}
	if err := advance(RolledBack, "rollback-committed", "rolled_back"); err != nil {
		return stored, errors.Join(ErrApplyIncomplete, cause, err)
	}
	return stored, errors.Join(ErrApplyRolledBack, cause)
}
