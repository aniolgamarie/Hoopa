//go:build linux && amd64

package mihomo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

func basicArguments(intent config.RunIntent) bool {
	return slices.Equal(intent.Arguments, []string{intent.Binary.Path, "-d", intent.Workdir, "-f", filepath.Join(intent.Workdir, "config.json")})
}

// Start verifies reviewed bundle material before consuming the one-use launcher
// capability. It neither waits for readiness nor registers/activates the core.
func (c *Controller) Start(ctx context.Context, prepared *owned.PreparedLaunch, launcherEntryArgs []string) (*owned.LaunchChild, error) {
	if c == nil || prepared == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrController
	}
	intent := prepared.Intent()
	if !basicArguments(intent) {
		return nil, ErrController
	}
	if _, err := c.material(intent); err != nil {
		return nil, err
	}
	executable, err := platform.OpenExecutable(ctx, intent.Binary.Path, intent.Binary.SHA256)
	if err != nil {
		return nil, err
	}
	if err = executable.Close(); err != nil {
		return nil, err
	}
	return owned.StartBasicLauncher(ctx, prepared, launcherEntryArgs)
}

// ExecBasicLauncher belongs only in the dedicated self-launcher entry. It claims
// birth (closing the inherited directory), validates original config and fixed
// executable, then replaces itself with that exact opened ELF. Success never
// returns. A failure after birth must be reconciled; never grant a new ticket.
func ExecBasicLauncher(ctx context.Context, directory *os.File, intentSHA, nonce string) error {
	intent, err := owned.ClaimLauncher(ctx, directory, intentSHA, nonce)
	if err != nil {
		return err
	}
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" || !basicArguments(intent) {
		return ErrController
	}
	capability, err := ApprovedCapability(intent.Binary)
	if err != nil {
		return err
	}
	root, err := config.OpenRunWorkdir(intent)
	if err != nil {
		return err
	}
	defer root.Close()
	work, err := root.Open(".")
	if err != nil {
		return err
	}
	if err = errors.Join(work.Chdir(), work.Close()); err != nil {
		return err
	}
	// Verify the actual anchored cwd still has the configured canonical pathname.
	cwd, err := os.Readlink("/proc/self/cwd")
	if err != nil || cwd != intent.Workdir {
		return ErrController
	}
	executable, err := platform.OpenExecutable(ctx, intent.Binary.Path, intent.Binary.SHA256)
	if err != nil {
		return err
	}
	defer executable.Close()
	environment := []string{"HOME=" + intent.Workdir, "TMPDIR=" + intent.Workdir, "LANG=C", "HOOPA_CONTROL_PROTOCOL=" + strconv.Itoa(capability.Protocol), "HOOPA_RUN_ID=" + string(intent.RunID), "HOOPA_RUN_NONCE=" + nonce, "HOOPA_GUARD_GENERATION=" + string(intent.Generation), "HOOPA_GUARD_CONFIG_SHA256=" + intent.ConfigSHA256}
	return executable.Exec(ctx, intent.Arguments, environment)
}
