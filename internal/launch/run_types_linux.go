//go:build linux && amd64

package launch

import (
	"errors"
	"os"

	"hoopa/internal/config"
)

const AppLauncherEntry = "__hoopa_app_launcher"

var (
	ErrLaunchArguments   = errors.New("LAUNCH_ARGUMENTS_INVALID")
	ErrLaunchUnavailable = errors.New("LAUNCH_UNAVAILABLE")
	ErrLaunchBookkeeping = errors.New("LAUNCH_BOOKKEEPING_FAILED")
)

// RunOptions is private in-process input, never a JSON request or log record.
// Managed entry qualification belongs to the local caller before Run.
type RunOptions struct {
	Paths                 config.Paths
	Profile               config.AppLaunchProfile
	App                   config.App
	EntryBindingSHA256    *string
	Environment           Environment
	Argv                  []string
	Stdin, Stdout, Stderr *os.File
}

// RunResult keeps the observed original root result even when bookkeeping or
// terminal restoration fails. Code is then 5, not an invented application exit.
type RunResult struct {
	Code     int
	LaunchID config.ID
	Observed *config.AppLaunchResult
}
