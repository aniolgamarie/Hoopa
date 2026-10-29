//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
)

// WaitExited observes exit using only the original process handle retained by
// this strong Observe/Register. It neither reopens a PID nor rechecks argv or
// controller state after exit. This historical fact is not child reaping or a
// runtime policy decision; a caller supplies its own cancellation/deadline.
func (r *OwnedRun) WaitExited(ctx context.Context) error {
	if r == nil || r.process == nil || ctx == nil {
		return ErrOwnershipUnknown
	}
	if err := r.process.WaitExited(ctx); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	return nil
}
