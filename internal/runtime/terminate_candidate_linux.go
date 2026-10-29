//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// CandidateTerminationInspector is deliberately separate from normal ownership.
// It must authenticate the complete immutable snapshot and a stable gate/history
// on this EXACT owner-proved connection. Degraded is allowed only for this
// candidate's precisely bound failed activation receipt; draining is refused.
// Implementations must not write, reconnect, redirect or retain the connection.
type CandidateTerminationInspector interface {
	InspectTermination(context.Context, *net.TCPConn, config.RunIntent, config.RunRegistration, string) (ControllerObservation, error)
}

// TerminateCandidate is only for an already closed-registered candidate of this
// original launch operation, after durable rollback/stop-candidate intent and
// under the control lock. It never creates registration, returns an OwnedRun,
// authorizes adoption/restart, or infers absence from an observation error.
// A failed call may already have sent TERM. No retry or KILL follows it.
func (o *Ownership) TerminateCandidate(ctx context.Context, id config.RunID, operation config.ID) error {
	fail := func(err error) error { return errors.Join(ErrOwnershipUnknown, err) }
	if o == nil || o.store == nil || o.controller == nil || ctx == nil {
		return ErrOwnershipUnknown
	}
	inspector, ok := o.controller.(CandidateTerminationInspector)
	if !ok {
		return ErrOwnershipUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	reg, registrationSHA, err := o.store.ReadControlRegistration(id, operation)
	if err != nil {
		return fail(err)
	}
	intent, intentSHA, err := o.store.ReadRunIntent(id)
	if err != nil {
		return fail(err)
	}
	birth, birthSHA, err := o.store.ReadRunBirth(id)
	if err != nil {
		return fail(err)
	}
	if reg.Gate != "closed" || intent.OperationID != operation || reg.OperationID != operation {
		return ErrOwnershipUnknown
	}
	if err := reg.Match(intent, intentSHA, birth, birthSHA); err != nil {
		return fail(err)
	}
	handle, err := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if err != nil {
		return fail(err)
	}
	defer handle.Close()
	identity := handle.Identity()
	if identity.PID != birth.PID || identity.UID != birth.UID || identity.BootID != birth.BootID || identity.StartTicks != birth.StartTicks {
		return ErrOwnershipUnknown
	}
	if err := inspectCandidateTermination(ctx, handle, inspector, intent, reg, registrationSHA); err != nil {
		return fail(err)
	}
	// Recheck writer/poison qualification and the exact original registration
	// after network inspection. The following signal uses this same pidfd.
	current, currentSHA, err := o.store.ReadControlRegistration(id, operation)
	if err != nil {
		return fail(err)
	}
	if current != reg || currentSHA != registrationSHA {
		return ErrOwnershipUnknown
	}
	if err := handle.VerifyArguments(ctx, intent.Arguments); err != nil {
		return fail(err)
	}
	if err := handle.SignalVerified(ctx, syscall.SIGTERM); err != nil {
		return fail(err)
	}
	if err := handle.WaitExited(ctx); err != nil {
		return fail(err)
	}
	return nil
}

func inspectCandidateTermination(ctx context.Context, handle *platform.ProcessHandle, inspector CandidateTerminationInspector, intent config.RunIntent, reg config.RunRegistration, registrationSHA string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := handle.VerifyArguments(ctx, intent.Arguments); err != nil {
		return err
	}
	conn, err := handle.DialOwnedTCP(ctx, intent.Controller)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	inspectedIntent := intent
	inspectedIntent.Arguments = append([]string(nil), intent.Arguments...)
	observed, err := inspector.InspectTermination(ctx, conn, inspectedIntent, reg, registrationSHA)
	if err != nil {
		return err
	}
	if observed.RunID != intent.RunID || observed.Generation != intent.Generation || observed.ConfigSHA256 != intent.ConfigSHA256 || observed.BinaryVersion != intent.Binary.Version || observed.Gate != "closed" && observed.Gate != "open" && observed.Gate != "degraded" {
		return ErrOwnershipUnknown
	}
	return handle.VerifyArguments(ctx, intent.Arguments)
}
