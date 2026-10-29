//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"hoopa/internal/p1testhook"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"hoopa/internal/config"
)

// LaunchChild represents only this manager's direct child and reaping result.
// It does not confer adoption/signal rights. Cancellation or dropping it never
// kills the core; explicit lifecycle control still requires original-pidfd proof.
type LaunchChild struct {
	cmd       *exec.Cmd
	exit      *launchExit
	intent    config.RunIntent
	intentSHA string
}

// LaunchChild's fields stay immutable after publication, so copying or
// formatting the redacted handle cannot race the wait goroutine. Its separate
// exit result is read only after the channel's publication barrier.
type launchExit struct {
	done chan struct{}
	err  error
}

func (LaunchChild) String() string   { return "LaunchChild{private launch material}" }
func (LaunchChild) GoString() string { return "LaunchChild{private launch material}" }
func (c *LaunchChild) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}
func (c *LaunchChild) Wait(ctx context.Context) error {
	if c == nil || c.exit == nil || ctx == nil {
		return ErrOwnershipUnknown
	}
	select {
	case <-c.exit.done:
		return c.exit.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StartBasicLauncher executes this exact manager image through /proc/self/exe.
// entryArgs is a trusted compiled dispatch choice (not user-supplied core argv).
// T033 will bind it to the internal launcher CLI. The dedicated child must call
// ClaimLauncher and then the fixed basic-core exec chain. A successful Start is
// NOT readiness, registration or permission to open application entries.
func StartBasicLauncher(ctx context.Context, prepared *PreparedLaunch, entryArgs []string) (*LaunchChild, error) {
	if ctx == nil || ctx.Err() != nil || prepared == nil || len(entryArgs) == 0 || len(entryArgs) > 16 {
		return nil, ErrOwnershipUnknown
	}
	for _, arg := range entryArgs {
		if len(arg) > 256 || strings.ContainsRune(arg, 0) {
			return nil, ErrOwnershipUnknown
		}
	}
	intent := prepared.Intent()
	root, err := config.OpenRunWorkdir(intent)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	log, err := root.OpenFile("launcher.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	directory, sha, nonce, err := prepared.TakeLauncherInput()
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, ErrOwnershipUnknown
	}
	cmd := exec.Command("/proc/self/exe") // Never CommandContext: core outlives manager.
	cmd.Args = append([]string{executable}, entryArgs...)
	cmd.Dir = intent.Workdir
	cmd.Env = []string{"HOME=" + intent.Workdir, "TMPDIR=" + intent.Workdir, "LANG=C", "HOOPA_LAUNCH_PROTOCOL=1", "HOOPA_LAUNCH_INTENT_SHA256=" + sha, "HOOPA_RUN_NONCE=" + nonce}
	cmd.ExtraFiles = []*os.File{directory}
	cmd.Stdout = log
	cmd.Stderr = log // nil Stdin is os/exec's /dev/null.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, err)
	}
	exit := &launchExit{done: make(chan struct{})}
	child := &LaunchChild{cmd: cmd, exit: exit, intent: intent, intentSHA: sha}
	go func() { exit.err = cmd.Wait(); close(exit.done) }()
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "launcher-started", string(intent.OperationID), string(intent.RunID), "", false, map[string]any{"pid": cmd.Process.Pid, "intent": intent})
	}
	// Return the actual launched child even if ctx was cancelled during Start;
	// cancellation cannot undo a spawn and must not hide a live child from caller.
	return child, nil
}
