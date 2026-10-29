//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// ClaimLauncher consumes and CLOSES the inherited birth-directory FD, including
// on failure. The caller is a dedicated launcher before exec, not the manager.
// Success is only permission to proceed with the separately validated T026
// bootstrap chain; it does not load BPF, exec a core, register it, or open traffic.
// The launcher must abort on error and must not retry with a newly read intent.
// The nonce must remain in HOOPA_RUN_NONCE across the same-PID bootstrap/exec chain.
func ClaimLauncher(ctx context.Context, directory *os.File, intentSHA, nonce string) (intent config.RunIntent, result error) {
	if directory == nil {
		return intent, ErrOwnershipUnknown
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	if ctx == nil {
		return intent, ErrOwnershipUnknown
	}
	if err := ctx.Err(); err != nil {
		return intent, err
	}
	if os.Getenv("HOOPA_RUN_NONCE") != nonce {
		return intent, ErrOwnershipUnknown
	}
	root, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", directory.Fd()))
	if err != nil {
		return intent, errors.Join(ErrOwnershipUnknown, err)
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	intent, err = config.ReadLauncherIntent(root, intentSHA)
	if err != nil {
		return config.RunIntent{}, errors.Join(ErrOwnershipUnknown, err)
	}
	born, err := platform.CurrentProcessBirth(ctx)
	if err != nil {
		return config.RunIntent{}, errors.Join(ErrOwnershipUnknown, err)
	}
	birth := config.RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: intentSHA, PID: born.PID, UID: born.UID, BootID: born.BootID, StartTicks: born.StartTicks}
	if err = ctx.Err(); err != nil {
		return config.RunIntent{}, err
	}
	if err = config.ClaimRunBirth(root, intentSHA, nonce, birth); err != nil {
		return config.RunIntent{}, errors.Join(ErrOwnershipUnknown, err)
	}
	// Cancellation after publication still prohibits exec. Preserve the birth
	// record for explicit reconciliation; never turn this into a spawn retry.
	if err = ctx.Err(); err != nil {
		return config.RunIntent{}, err
	}
	return intent, nil
}
