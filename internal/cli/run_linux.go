//go:build linux && amd64

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"
	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/launch"
	"hoopa/internal/service"
)

type runArguments struct {
	app   config.ID
	mode  string
	meter bool
	argv  []string
}

func parseRunArguments(args []string) (runArguments, error) {
	var result runArguments
	split := -1
	for i, arg := range args {
		if arg == "--" {
			split = i
			break
		}
	}
	if split < 0 || split == len(args)-1 {
		return result, control.ErrRequest
	}
	flags := pflag.NewFlagSet("run", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var app string
	flags.StringVar(&app, "app", "", "")
	flags.StringVar(&result.mode, "mode", "", "")
	flags.BoolVar(&result.meter, "meter", false, "")
	if flags.Parse(args[:split]) != nil || len(flags.Args()) != 0 || !flags.Changed("app") {
		return runArguments{}, control.ErrRequest
	}
	id, err := config.ParseID(app)
	if err != nil || result.mode != "" && result.mode != "managed" && result.mode != "direct" && result.mode != "inherit" || flags.Changed("mode") && result.mode == "" {
		return runArguments{}, control.ErrRequest
	}
	result.app = id
	result.argv = append([]string{}, args[split+1:]...)
	if len(result.argv) > 4096 || result.argv[0] == "" {
		return runArguments{}, control.ErrRequest
	}
	for _, arg := range result.argv {
		if len(arg) > 131071 || strings.IndexByte(arg, 0) >= 0 {
			return runArguments{}, control.ErrRequest
		}
	}
	return result, nil
}

// executeApplication is deliberately outside management JSON handling: child
// output is the original stream and the observed application code is returned.
func executeApplication(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	parsed, err := parseRunArguments(args)
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	stdin, inOK := input.(*os.File)
	stdout, outOK := output.(*os.File)
	stderr, errOK := diagnostics.(*os.File)
	if ctx == nil || !inOK || !outOK || !errOK || stdin == nil || stdout == nil || stderr == nil {
		return runDiagnostic(diagnostics, control.ErrRequest, nil)
	}
	paths, err := resolvePaths()
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	captured, err := control.CaptureApplication(ctx, paths, parsed.app, parsed.mode, parsed.meter)
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	original, err := captured.Profile()
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	selected, err := captured.Application()
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	binding, err := captured.SensitiveBinding()
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	environment, err := launch.BuildEnvironment(os.Environ(), selected, binding)
	if err != nil {
		return runDiagnostic(diagnostics, err, nil)
	}
	var bindingSHA *string
	if selected.Mode == "managed" {
		if err = qualifyRunEntry(ctx, paths, captured); err != nil {
			return runDiagnostic(diagnostics, control.ErrManagedEntryUnavailable, nil)
		}
		if binding == nil {
			return runDiagnostic(diagnostics, control.ErrManagedEntryUnavailable, nil)
		}
		digest := binding.Digest()
		bindingSHA = &digest
	}
	result, err := launch.Run(ctx, launch.RunOptions{Paths: paths, Profile: original, App: selected, EntryBindingSHA256: bindingSHA, Environment: environment, Argv: parsed.argv, Stdin: stdin, Stdout: stdout, Stderr: stderr})
	if err != nil {
		return runDiagnostic(diagnostics, err, result.Observed)
	}
	if result.Observed != nil && result.Observed.ExecFailureReason != nil {
		return runExecDiagnostic(diagnostics, *result.Observed.ExecFailureReason)
	}
	return result.Code
}

func qualifyRunEntry(ctx context.Context, paths config.Paths, capture control.ApplicationCapture) error {
	id, err := randomHex(24)
	if err != nil {
		return control.ErrManagedEntryUnavailable
	}
	request, err := makeRequest(id, "status.get", map[string]any{})
	if err != nil {
		return control.ErrManagedEntryUnavailable
	}
	// Exchange uses one authenticated live endpoint; Call's offline status
	// fallback is intentionally unavailable to managed launch qualification.
	conn, err := service.DialEndpoint(ctx, paths)
	if err != nil {
		return control.ErrManagedEntryUnavailable
	}
	defer conn.Close()
	raw, err := service.Exchange(ctx, conn, request)
	if err != nil || service.ExitCode(raw) != 0 {
		return control.ErrManagedEntryUnavailable
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return control.ErrManagedEntryUnavailable
	}
	var status control.ServiceStatus
	decoder := json.NewDecoder(strings.NewReader(string(envelope.Data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&status) != nil {
		return control.ErrManagedEntryUnavailable
	}
	return control.QualifyApplicationEntry(ctx, paths, capture, status)
}
func runExecDiagnostic(out io.Writer, reason string) int {
	code, label := 126, "EXEC_UNAVAILABLE"
	switch reason {
	case "exec_format":
		label = "EXEC_FORMAT"
	case "exec_not_found":
		label = "EXEC_NOT_FOUND"
		code = 127
	case "exec_permission":
		label = "EXEC_PERMISSION"
	case "exec_unavailable":
	default:
		return runDiagnostic(out, launch.ErrLaunchBookkeeping, nil)
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "hoopa: %s\n", label)
	}
	return code
}
func runDiagnostic(out io.Writer, err error, observed *config.AppLaunchResult) int {
	code, label := 4, "LAUNCH_UNAVAILABLE"
	switch {
	case errors.Is(err, control.ErrRequest), errors.Is(err, launch.ErrLaunchArguments), errors.Is(err, launch.ErrEnvironment):
		code, label = 2, "INVALID_ARGUMENT"
	case errors.Is(err, control.ErrAppMeteringMode), errors.Is(err, launch.ErrMeteringMode):
		code, label = 2, "METERING_MODE_INVALID"
	case errors.Is(err, control.ErrAppMeteringUnsupported), errors.Is(err, launch.ErrMeteringUnsupported):
		code, label = 2, "METERING_UNSUPPORTED"
	case errors.Is(err, control.ErrAppNotFound):
		label = "APP_NOT_FOUND"
	case errors.Is(err, control.ErrProfileUnavailable):
		label = "PROFILE_UNAVAILABLE"
	case errors.Is(err, control.ErrManagedEntryUnavailable):
		label = "MANAGED_ENTRY_UNAVAILABLE"
	case errors.Is(err, launch.ErrLaunchBookkeeping):
		code, label = 5, "LAUNCH_BOOKKEEPING_FAILED"
	}
	if out != nil {
		_, _ = fmt.Fprintf(out, "hoopa: %s\n", label)
		if code == 5 && observed != nil {
			_, _ = fmt.Fprintf(out, "hoopa: observed_root exit=%s signal=%s wait_status=%d\n", runObservedInteger(observed.ExitCode), runObservedInteger(observed.Signal), observed.WaitStatus)
		}
	}
	return code
}
func runObservedInteger(value *int) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *value)
}
