//go:build linux && amd64

package cli

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"hoopa/internal/config"
	"hoopa/internal/control"
)

func (r *commandRun) initCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "init", Args: cobra.NoArgs}
	var mihomo, gost, input, controller, shared string
	var apps []string
	cmd.Flags().StringVar(&mihomo, "mihomo", "", "Use this explicitly supplied local Mihomo binary")
	cmd.Flags().StringVar(&gost, "gost", "", "Declare this local optional GOST dependency")
	cmd.Flags().StringVar(&input, "input", "", "Read a local JSON/YAML Intent declaration")
	cmd.Flags().StringVar(&controller, "controller-port", "", "Declare the local controller port")
	cmd.Flags().StringVar(&shared, "shared-port", "", "Declare the shared entry port")
	cmd.Flags().StringArrayVar(&apps, "app-port", nil, "Declare an app entry as APP=PORT (repeat for distinct apps)")
	cmd.RunE = func(*cobra.Command, []string) error {
		options := control.InitOptions{MihomoPath: mihomo, GostPath: gost}
		var err error
		if cmd.Flags().Changed("controller-port") {
			options.ControllerPort, err = initPort(controller)
			if err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("shared-port") {
			options.SharedPort, err = initPort(shared)
			if err != nil {
				return err
			}
		}
		if len(apps) > 0 {
			options.AppPorts = map[config.ID]uint16{}
		}
		for _, pair := range apps {
			name, value, ok := strings.Cut(pair, "=")
			id, parseErr := config.ParseID(name)
			if !ok || parseErr != nil {
				return control.ErrRequest
			}
			if _, exists := options.AppPorts[id]; exists {
				return control.ErrRequest
			}
			port, parseErr := initPort(value)
			if parseErr != nil {
				return parseErr
			}
			options.AppPorts[id] = port
		}
		if cmd.Flags().Changed("input") {
			value, readErr := subscriptionDocument(r.ctx, input)
			if readErr != nil {
				return readErr
			}
			if _, ok := value.(map[string]any); !ok {
				return control.ErrRequest
			}
			options.Intent, err = json.Marshal(value)
			if err != nil {
				return control.ErrRequest
			}
		}
		// Validate before even resolving/consulting the local store. Stateful
		// preparation belongs solely to the explicit InitializeLocal use case.
		if _, err = control.PrepareInitInput(options); err != nil {
			return err
		}
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		report, err := control.InitializeLocal(r.ctx, paths, options)
		if err != nil {
			return err
		}
		r.response, err = json.Marshal(map[string]any{"api_version": "v1", "request_id": nil, "ok": true, "data": report, "error": nil, "warnings": []any{}})
		if err != nil {
			return control.ErrRequest
		}
		r.localSuccess = true
		return nil
	}
	return cmd
}
func initPort(raw string) (uint16, error) {
	if raw == "" {
		return 0, control.ErrRequest
	}
	for _, c := range []byte(raw) {
		if c < '0' || c > '9' {
			return 0, control.ErrRequest
		}
	}
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil || value == 0 {
		return 0, control.ErrRequest
	}
	return uint16(value), nil
}
