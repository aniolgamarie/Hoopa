//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"net"
	"os"

	"hoopa/internal/config"
	"hoopa/internal/p1testhook"
	"hoopa/internal/platform"
)

// Keep actual error values on the original return path. Diagnostics use only
// fixed stage names and classifications; arbitrary error strings can contain
// private paths, request material or controller credentials.
func p1ObservationFailure(stage string, run config.RunID, err error) error {
	if p1testhook.Enabled {
		var network net.Error
		_ = p1testhook.Record("runtime", "ownership-observation-failed", "", string(run), "", false, map[string]any{
			"stage": stage, "deadline_exceeded": errors.Is(err, context.DeadlineExceeded),
			"canceled": errors.Is(err, context.Canceled), "network_timeout": errors.As(err, &network) && network.Timeout(),
			"process_unverified": errors.Is(err, platform.ErrProcessUnverified), "ownership_unknown": errors.Is(err, ErrOwnershipUnknown),
			"controller_unverified": errors.Is(err, platform.ErrControllerUnverified), "not_exist": errors.Is(err, os.ErrNotExist),
		})
	}
	return err
}
