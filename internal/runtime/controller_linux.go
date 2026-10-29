//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"net"
	"time"

	"hoopa/internal/config"
)

// WithRegisteredController binds an existing durable registration to THIS
// original pidfd, then proves the exact connected socket before invoking visit.
// The trusted callback is synchronous: it must not retain conn or reconnect.
// Only this connection is closed; no process is stopped or record published.
// The caller must serialize control operations and keep store's writer open.
// An error after visit does not establish that its remote action had no effect.
func (r *OwnedRun) WithRegisteredController(ctx context.Context, store *config.Store, operation config.ID, visit func(context.Context, *net.TCPConn, config.RunIntent, config.RunRegistration, string) error) error {
	if r == nil || r.process == nil || ctx == nil || store == nil || visit == nil {
		return ErrOwnershipUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	reg, sha, err := store.ReadControlRegistration(r.intent.RunID, operation)
	if err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if err = reg.Match(r.intent, r.intentSHA, r.birth, r.birthSHA); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	if err = r.process.VerifyArguments(ctx, r.intent.Arguments); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	conn, err := r.process.DialOwnedTCP(ctx, r.intent.Controller)
	if err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return errors.Join(ErrOwnershipUnknown, err)
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	intent := r.intent
	intent.Arguments = append([]string(nil), intent.Arguments...)
	callErr := visit(ctx, conn, intent, reg, sha)
	if err = r.process.VerifyArguments(ctx, r.intent.Arguments); err != nil {
		return errors.Join(ErrOwnershipUnknown, callErr, err)
	}
	return callErr
}
