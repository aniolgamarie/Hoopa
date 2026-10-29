//go:build linux && amd64

package cli

import "github.com/spf13/cobra"

func (r *commandRun) doctorCommand() *cobra.Command {
	return &cobra.Command{Use: "doctor", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return r.query("doctor.get", map[string]any{})
	}}
}
