//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/p1testhook"
)

// Terminate requires the coordinator's control lock, open writer, and durable
// stop/applying intent. It only sends TERM to this original, freshly proved
// pidfd; it neither writes authority nor closes/reaps the handle or child.
// Closed/open controller observations are supported; draining/degraded are
// unknown. An error may occur after TERM and never proves the process stayed up.
func (o *Ownership) Terminate(ctx context.Context, run *OwnedRun, operation config.ID) error {
	if o == nil || o.store == nil || o.controller == nil || run == nil || run.process == nil || ctx == nil {
		return ErrOwnershipUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	reg, sha, err := o.store.ReadControlRegistration(run.intent.RunID, operation)
	if err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if err = reg.Match(run.intent, run.intentSHA, run.birth, run.birthSHA); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if _, err = o.inspect(ctx, run.process, run.intent); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	current, currentSHA, err := o.store.ReadControlRegistration(run.intent.RunID, operation)
	if err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if current != reg || currentSHA != sha {
		return ErrOwnershipUnknown
	}
	if err = run.process.VerifyArguments(ctx, run.intent.Arguments); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if err = run.process.SignalVerified(ctx, syscall.SIGTERM); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "termination-signal-confirmed", string(operation), string(run.intent.RunID), "", false, map[string]any{"identity": run.Identity(), "registration_sha256": sha})
	}
	if err = run.process.WaitExited(ctx); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "exit-observed", string(operation), string(run.intent.RunID), "", false, map[string]any{"identity": run.Identity()})
	}
	return nil
}
