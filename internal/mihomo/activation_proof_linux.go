//go:build linux && amd64

package mihomo

import (
	"context"
	"net"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
)

// VerifyActivation adds the exact original activation receipt to an ordinary
// ownership observation of an open run. It sends GETs only, on the connection
// whose owner is proved by the retained pidfd. Closed/degraded/draining cannot
// become an adopted open run through the termination-specific inspector.
func (c *Controller) VerifyActivation(ctx context.Context, run *owned.OwnedRun, operation config.ID) error {
	if c == nil || c.store == nil || run == nil || run.Observation().Gate != "open" {
		return ErrController
	}
	return run.WithRegisteredController(ctx, c.store, operation, func(ctx context.Context, conn *net.TCPConn, intent config.RunIntent, reg config.RunRegistration, sha string) error {
		observed, err := c.InspectTermination(ctx, conn, intent, reg, sha)
		if err != nil {
			return err
		}
		if observed.Gate != "open" {
			return ErrController
		}
		return nil
	})
}
