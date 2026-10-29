//go:build linux && amd64

package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"syscall"

	"github.com/spf13/cobra"
	"hoopa/internal/config"
	"hoopa/internal/control"
)

func (r *commandRun) configCommand() *cobra.Command {
	parent := &cobra.Command{Use: "config", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return control.ErrRequest }}
	plan := &cobra.Command{Use: "plan", Args: cobra.NoArgs}
	var input, output string
	plan.Flags().StringVar(&input, "input", "", "Read an explicit local JSON/YAML Intent")
	plan.Flags().StringVar(&output, "output", "", "Save the complete Plan to a new private file")
	plan.RunE = func(*cobra.Command, []string) error {
		if input == "" || output == "" {
			return control.ErrRequest
		}
		value, err := subscriptionDocument(r.ctx, input)
		if err != nil {
			return err
		}
		if _, ok := value.(map[string]any); !ok {
			return control.ErrRequest
		}
		if err = r.query("config.plan", map[string]any{"intent": value}); err != nil {
			return err
		}
		return r.savePlanOutput(output)
	}
	apply := &cobra.Command{Use: "apply", Args: cobra.NoArgs}
	var path, confirmation, id string
	var disruption bool
	apply.Flags().StringVar(&path, "plan", "", "Read an exact JSON Plan")
	apply.Flags().StringVar(&confirmation, "confirm", "", "Confirm this exact Plan digest")
	apply.Flags().BoolVar(&disruption, "accept-disruption", false, "Confirm the previewed connection interruption")
	apply.Flags().StringVar(&id, "request-id", "", "Use this exact request identity")
	apply.RunE = func(*cobra.Command, []string) error {
		if path == "" || confirmation == "" {
			return control.ErrRequest
		}
		raw, err := readCLIPlan(r.ctx, path)
		if err != nil {
			return err
		}
		identity, err := requestIdentity(id)
		if err != nil {
			return err
		}
		requestRaw, err := makeRequest(identity, "config.apply", map[string]any{"plan": json.RawMessage(raw), "confirmed_plan_digest": confirmation, "accept_disruption": disruption})
		if err != nil {
			return err
		}
		request, err := control.ParseRequest(requestRaw)
		if err != nil {
			return control.ErrRequest
		}
		if err = control.ValidateSourceApplyRequest(request); err != nil {
			return err
		}
		paths, err := resolvePaths()
		if err != nil {
			return err
		}
		// The exact Plan and request go directly to the service replay/admission path.
		// In particular, mutate would incorrectly refresh a historical base revision.
		return r.call(paths, requestRaw)
	}
	parent.AddCommand(plan, apply)
	return parent
}

func (r *commandRun) savePlanOutput(path string) error {
	var envelope struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(r.response, &envelope) != nil {
		return control.ErrRequest
	}
	if !envelope.OK {
		return nil
	} // Preserve the service's original error envelope.
	var object map[string]json.RawMessage
	if json.Unmarshal(envelope.Data, &object) != nil || object == nil {
		return control.ErrRequest
	}
	if _, err := control.ComputePlanDigest(envelope.Data); err != nil {
		return control.ErrRequest
	}
	return writePlanOutput(r.ctx, path, envelope.Data)
}

// Plan files are JSON only. The strict canonical parser rejects duplicate keys,
// invalid Unicode and trailing values before any envelope can hide them.
func readCLIPlan(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, control.ErrRequest
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > control.MaxRequestBytes {
		return nil, control.ErrRequest
	}
	raw, err := io.ReadAll(io.LimitReader(file, control.MaxRequestBytes+1))
	if err != nil || len(raw) > control.MaxRequestBytes {
		return nil, control.ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := config.CanonicalJSON(raw)
	if err != nil {
		return nil, control.ErrRequest
	}
	return canonical, nil
}
