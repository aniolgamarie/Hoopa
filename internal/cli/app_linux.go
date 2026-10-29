//go:build linux && amd64

package cli

import (
	"encoding/json"
	"github.com/spf13/cobra"
	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/launch"
	"os"
)

func (r *commandRun) appCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "app", Args: cobra.NoArgs}
	var mode string
	var meter bool
	inspect := &cobra.Command{Use: "inspect ID", Args: cobra.ExactArgs(1)}
	inspect.Flags().StringVar(&mode, "mode", "", "Preview this invocation's mode")
	inspect.Flags().BoolVar(&meter, "meter", false, "Preview requested metering")
	inspect.RunE = func(_ *cobra.Command, args []string) error {
		id, err := config.ParseID(args[0])
		if err != nil || inspect.Flags().Changed("mode") && mode == "" {
			return control.ErrRequest
		}
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		capture, err := control.CaptureApplication(r.ctx, paths, id, mode, meter)
		if err != nil {
			return err
		}
		profile, err := capture.Profile()
		if err != nil {
			return err
		}
		selected, err := capture.Application()
		if err != nil {
			return err
		}
		binding, err := capture.SensitiveBinding()
		if err != nil {
			return err
		}
		environment, err := launch.BuildEnvironment(os.Environ(), selected, binding)
		if err != nil {
			return err
		}
		records, err := control.ReadLaunches(r.ctx, paths)
		if err != nil {
			return err
		}
		launches := make([]control.LaunchView, 0)
		for _, record := range records {
			if record.AppID == id {
				launches = append(launches, record)
			}
		}
		var entry any
		var digest *string
		state := "not_applicable"
		if binding != nil {
			value, err := binding.SensitiveEntry()
			if err != nil {
				return control.ErrManagedEntryUnavailable
			}
			entry = map[string]any{"name": value.Name, "host": value.Listen, "port": value.Port}
			sha := binding.Digest()
			digest = &sha
			state = "not_probed"
		}
		data := map[string]any{"schema_version": 1, "app_id": id, "declared_mode": profile.App.Mode, "selected_mode": selected.Mode, "declared_metering": profile.App.Metering, "selected_metering": selected.Metering, "bypass": selected.Bypass, "profile_kind": profile.Kind, "profile_revision": profile.Revision, "profile_generation": profile.Generation, "binding_sha256": digest, "entry": entry, "entry_state": state, "environment_changes": environment.Changes(), "launches": launches, "root_scope": "original_direct_child", "descendants_scope": "not_enumerated"}
		r.response, err = json.Marshal(map[string]any{"api_version": "v1", "request_id": nil, "ok": true, "data": data, "error": nil, "warnings": []any{}})
		if err != nil {
			return control.ErrRequest
		}
		r.localSuccess = true
		return nil
	}
	cmd.AddCommand(inspect)
	return cmd
}
