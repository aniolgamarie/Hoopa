//go:build linux && amd64

// Package cli binds the shipped commands to the local management service.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/launch"
	"hoopa/internal/service"
)

const coreLauncherEntry = "__hoopa_core_launcher"
const serviceEntry = "__hoopa_service"

type commandRun struct {
	ctx      context.Context
	input    io.Reader
	raw      []byte
	response []byte
	started  bool
	// Only a successfully constructed local report may omit RPC correlation.
	// Management wire responses still pass service.ExitCode's strict validator.
	localSuccess bool
}

// Execute emits one redacted response for each completed request. The reserved
// launcher entry is dispatched before Cobra, paths or any service initialization.
func Execute(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	if len(args) > 0 && args[0] == launch.AppLauncherEntry {
		return launch.RunAppLauncher(args)
	}
	if len(args) > 0 && args[0] == "run" {
		return executeApplication(ctx, args[1:], input, output, diagnostics)
	}
	if ctx == nil || input == nil || output == nil {
		return 2
	}
	if len(args) > 0 && args[0] == coreLauncherEntry {
		return runCoreLauncher(ctx, args, diagnostics)
	}
	if len(args) > 0 && args[0] == serviceEntry {
		return runServiceChild(ctx, args, diagnostics)
	}
	run := &commandRun{ctx: ctx, input: input}
	var err error
	// Machine mode accepts exactly the documented argv, including on help/flag
	// mistakes; Cobra must never replace its single JSON response with help text.
	if len(args) > 0 && args[0] == "api" && (len(args) != 3 || strings.HasPrefix(args[2], "-")) {
		err = control.ErrRequest
	} else if len(args) > 0 && args[0] == "api" && args[1] != "v1" {
		err = service.ErrUnsupportedVersion
	} else {
		cmd := run.command()
		cmd.SetArgs(args)
		cmd.SetIn(input)
		cmd.SetOut(output)
		cmd.SetErr(io.Discard)
		err = cmd.ExecuteContext(ctx)
		if err != nil && !run.started {
			err = control.ErrRequest
		}
	}
	if err != nil {
		run.response = service.Failure(run.raw, err)
	}
	if run.response != nil {
		if _, err := output.Write(append(append([]byte(nil), run.response...), '\n')); err != nil {
			return 4
		}
		if err == nil && run.localSuccess {
			return 0
		}
		return service.ExitCode(run.response)
	}
	return 0
}
func (r *commandRun) command() *cobra.Command {
	root := &cobra.Command{Use: "hoopa", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return control.ErrRequest }}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentPreRunE = func(*cobra.Command, []string) error { r.started = true; return nil }
	start := &cobra.Command{Use: "start", Args: cobra.NoArgs}
	var controlOnly bool
	var startID, startRevision string
	start.Flags().BoolVar(&controlOnly, "control-only", false, "Start the management service without starting forwarding")
	mutationFlags(start, &startID, &startRevision)
	start.RunE = func(*cobra.Command, []string) error { return r.start(controlOnly, startID, startRevision) }
	stop := &cobra.Command{Use: "stop", Args: cobra.NoArgs}
	var disruption bool
	var stopID, stopRevision string
	stop.Flags().BoolVar(&disruption, "accept-disruption", false, "Confirm stopping core forwarding")
	mutationFlags(stop, &stopID, &stopRevision)
	stop.RunE = func(*cobra.Command, []string) error {
		if !disruption {
			return control.ErrRequest
		}
		return r.mutate("runtime.stop", stopID, stopRevision, map[string]any{"accept_disruption": true})
	}
	svc := &cobra.Command{Use: "service", Args: cobra.NoArgs}
	foreground := &cobra.Command{Use: "run", Args: cobra.NoArgs}
	var restartOnce bool
	foreground.Flags().BoolVar(&restartOnce, "restart-once", false, "Authorize one restart after the next new successful runtime start")
	foreground.RunE = func(*cobra.Command, []string) error {
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		return service.Run(r.ctx, paths, service.Options{LauncherArgs: []string{coreLauncherEntry}, RestartOnce: restartOnce})
	}
	serviceStop := &cobra.Command{Use: "stop", Args: cobra.NoArgs}
	var serviceID, serviceRevision string
	mutationFlags(serviceStop, &serviceID, &serviceRevision)
	serviceStop.RunE = func(*cobra.Command, []string) error { return r.mutate("service.stop", serviceID, serviceRevision, nil) }
	svc.AddCommand(foreground, serviceStop)
	operation := &cobra.Command{Use: "operation", Args: cobra.NoArgs}
	get := &cobra.Command{Use: "get ID", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if _, err := config.ParseID(args[0]); err != nil {
			return control.ErrRequest
		}
		return r.query("operation.get", map[string]any{"operation_id": args[0]})
	}}
	recoverCmd := &cobra.Command{Use: "recover ID", Args: cobra.ExactArgs(1)}
	var recoverID, recoverRevision string
	mutationFlags(recoverCmd, &recoverID, &recoverRevision)
	recoverCmd.RunE = func(_ *cobra.Command, args []string) error {
		if _, err := config.ParseID(args[0]); err != nil {
			return control.ErrRequest
		}
		return r.mutate("operation.recover", recoverID, recoverRevision, map[string]any{"operation_id": args[0]})
	}
	operation.AddCommand(get, recoverCmd)
	status := &cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return r.query("status.get", map[string]any{}) }}
	api := &cobra.Command{Use: "api", Args: cobra.NoArgs}
	v1 := &cobra.Command{Use: "v1 OPERATION", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error { return r.machine(args[0]) }}
	api.AddCommand(v1)
	root.AddCommand(start, stop, svc, operation, status, api)
	root.AddCommand(r.subscriptionCommand())
	root.AddCommand(r.configCommand(), r.doctorCommand())
	root.AddCommand(r.initCommand())
	root.AddCommand(r.appCommand())
	return root
}
func mutationFlags(cmd *cobra.Command, id, revision *string) {
	cmd.Flags().StringVar(id, "request-id", "", "Use this exact request identity")
	cmd.Flags().StringVar(revision, "expected-revision", "", "Require this exact current revision")
}
func resolvePaths() (config.Paths, error) {
	home := os.Getenv("HOME")
	env := map[string]string{}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env[key] = os.Getenv(key)
	}
	paths, err := config.ResolvePaths(home, env)
	if err != nil {
		return config.Paths{}, control.ErrRequest
	}
	return paths, nil
}
func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func makeRequest(id, operation string, params map[string]any) ([]byte, error) {
	if !utf8.ValidString(id) || utf8.RuneCountInString(id) < 1 || utf8.RuneCountInString(id) > 128 {
		return nil, control.ErrRequest
	}
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": id, "operation": operation, "params": params})
	if err != nil {
		return nil, control.ErrRequest
	}
	if _, err = control.ParseRequest(raw); err != nil {
		return nil, control.ErrRequest
	}
	return raw, nil
}
func requestIdentity(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return randomHex(24)
}
func (r *commandRun) call(paths config.Paths, raw []byte) error {
	r.raw = raw
	response, err := service.Call(r.ctx, paths, raw)
	if err != nil {
		return err
	}
	r.response = response
	return nil
}
func (r *commandRun) query(operation string, params map[string]any) error {
	id, err := randomHex(24)
	if err != nil {
		return err
	}
	raw, err := makeRequest(id, operation, params)
	if err != nil {
		return err
	}
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	return r.call(paths, raw)
}
func (r *commandRun) mutate(operation, id, revision string, params map[string]any) error {
	var err error
	id, err = requestIdentity(id)
	if err != nil {
		return err
	}
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	if revision == "" {
		status, err := service.LocalStatus(r.ctx, paths)
		if err != nil {
			return err
		}
		revision = string(status.Revision)
	}
	if _, err := config.ParseID(revision); err != nil {
		return control.ErrRequest
	}
	if params == nil {
		params = map[string]any{}
	}
	params["expected_revision"] = revision
	raw, err := makeRequest(id, operation, params)
	if err != nil {
		return err
	}
	return r.call(paths, raw)
}
func (r *commandRun) start(controlOnly bool, id, revision string) error {
	var err error
	id, err = requestIdentity(id)
	if err != nil {
		return err
	}
	// Validate the supplied identity/revision before any explicit initialization.
	if _, err = makeRequest(id, "runtime.start", map[string]any{}); err != nil {
		return err
	}
	if revision != "" {
		if _, err = config.ParseID(revision); err != nil {
			return control.ErrRequest
		}
	}
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	bootstrapped := false
	status, statusErr := service.LocalStatus(r.ctx, paths)
	if !controlOnly {
		if statusErr != nil {
			return statusErr
		}
		if status.Generation == nil {
			return service.ErrNoSnapshot
		}
	}
	if statusErr != nil {
		if !controlOnly || revision != "" {
			return statusErr
		}
		// Cold control-only bootstrap is explicit. Endpoint integrity is checked
		// before the initializer can create a directory or take a writer lease.
		readyRevision, err := bootstrap(r.ctx, paths)
		if err != nil {
			return err
		}
		revision = string(readyRevision)
		bootstrapped = true
	} else if revision == "" {
		revision = string(status.Revision)
	}
	raw, err := makeRequest(id, "runtime.start", map[string]any{"expected_revision": revision, "control_only": controlOnly})
	if err != nil {
		return err
	}
	err = r.call(paths, raw)
	if !errors.Is(err, service.ErrServiceUnavailable) || bootstrapped {
		return err
	}
	// An offline known replay was already returned by Call. An unknown request
	// may start management once, only after validating the existing snapshot.
	if statusErr == nil && config.Revision(revision) != status.Revision {
		return control.ErrConflict
	}
	if _, err = bootstrap(r.ctx, paths); err != nil {
		return err
	}
	return r.call(paths, raw)
}
func parseStart(raw []byte) (revision config.Revision, controlOnly bool, err error) {
	canonical, e := config.CanonicalJSON(raw)
	if e != nil {
		return "", false, control.ErrRequest
	}
	var envelope struct {
		APIVersion string                     `json:"api_version"`
		Operation  string                     `json:"operation"`
		Params     map[string]json.RawMessage `json:"params"`
	}
	if json.Unmarshal(canonical, &envelope) != nil || envelope.APIVersion != "v1" || envelope.Operation != "runtime.start" || len(envelope.Params) != 2 {
		return "", false, control.ErrRequest
	}
	var rev string
	if json.Unmarshal(envelope.Params["expected_revision"], &rev) != nil {
		return "", false, control.ErrRequest
	}
	if _, e = config.ParseID(rev); e != nil {
		return "", false, control.ErrRequest
	}
	if string(envelope.Params["control_only"]) != "true" && string(envelope.Params["control_only"]) != "false" {
		return "", false, control.ErrRequest
	}
	return config.Revision(rev), string(envelope.Params["control_only"]) == "true", nil
}
func (r *commandRun) machine(operation string) error {
	raw, err := io.ReadAll(io.LimitReader(r.input, control.MaxRequestBytes+1))
	if err != nil || len(raw) > control.MaxRequestBytes {
		return control.ErrRequest
	}
	r.raw = raw
	_, err = control.ParseRequest(raw)
	if err != nil {
		return control.ErrRequest
	}
	var envelope struct {
		Version   string `json:"api_version"`
		Operation string `json:"operation"`
	}
	json.Unmarshal(raw, &envelope)
	if envelope.Version != "v1" {
		return service.ErrUnsupportedVersion
	}
	if envelope.Operation != operation {
		return control.ErrRequest
	}
	var revision config.Revision
	var controlOnly bool
	if operation == "runtime.start" {
		revision, controlOnly, err = parseStart(raw)
		if err != nil {
			return err
		}
	}
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	err = r.call(paths, raw)
	if operation != "runtime.start" || !errors.Is(err, service.ErrServiceUnavailable) {
		return err
	}
	status, err := service.LocalStatus(r.ctx, paths)
	if err != nil {
		return err
	}
	if status.Revision != revision {
		return control.ErrConflict
	}
	if !controlOnly && status.Generation == nil {
		return service.ErrNoSnapshot
	}
	if _, err = bootstrap(r.ctx, paths); err != nil {
		return err
	}
	return r.call(paths, raw)
}
