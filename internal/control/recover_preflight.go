//go:build linux && amd64

package control

import (
	"context"
	"errors"

	"hoopa/internal/config"
)

// preflightTerminalOwnership is read-only and holds the coordinator lock.
// Terminal reconcilers must keep their terminal paths free of Advance,
// PublishActive, registration, activation and signals. They receive no boundary
// callback here. Their ordinary checks are repeated after admission: this proof
// is not a capability to skip verification if the process changes meanwhile.
// Pending targets retain their existing explicitly admitted recovery workflow.
func (c *Coordinator) preflightTerminalOwnership(ctx context.Context, target StoredOperation, verify recoveredRunVerifier) error {
	if !terminal(target.Record.Phase) {
		return nil
	}
	var err error
	switch target.Record.Identity.Operation {
	case "config.apply":
		if _, decodeErr := decodeApplyEvidence(target); decodeErr != nil {
			return nil // Preserve the existing malformed-evidence diagnosis path.
		}
		_, err = c.reconcileBasicApply(ctx, target, verify, nil)
	case "runtime.start":
		if target.Record.Phase != Committed {
			return nil // A rolled-back start is not a supported live-core target.
		}
		if isAdoption(target) {
			if _, decodeErr := decodeAdoption(target); decodeErr != nil {
				return nil
			}
			_, err = c.reconcileAdoption(ctx, target, verify, nil)
		} else {
			if _, decodeErr := decodeLifecycleEvidence(target); decodeErr != nil {
				return nil
			}
			_, err = c.reconcileLifecycle(ctx, target, nil)
		}
	default:
		// Historical runtime.stop/service.stop facts do not demand a live core.
		return nil
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, config.ErrUncertain) {
		return err
	}
	return errors.Join(ErrConflict, err)
}
