//go:build linux && amd64

package cli

import (
	"context"
	"io"
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/document"
)

func (r *commandRun) subscriptionCommand() *cobra.Command {
	parent := &cobra.Command{Use: "subscription", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return control.ErrRequest }}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs}
	var cursor string
	list.Flags().StringVar(&cursor, "cursor", "", "Continue the exact observed source-list snapshot")
	list.RunE = func(cmd *cobra.Command, _ []string) error {
		params := map[string]any{}
		if cmd.Flags().Changed("cursor") {
			params["cursor"] = cursor
		}
		raw, err := makeRequest("validate-subscription-list", "subscription.list", params)
		if err != nil {
			return control.ErrRequest
		}
		request, err := control.ParseRequest(raw)
		if err != nil {
			return control.ErrRequest
		}
		if _, err = control.SourceListCursor(request); err != nil {
			return control.ErrRequest
		}
		return r.query("subscription.list", params)
	}
	update := &cobra.Command{Use: "update ID", Args: cobra.ExactArgs(1)}
	var input, regions, id, revision string
	update.Flags().StringVar(&input, "input", "", "Read an explicit local JSON/YAML Intent declaration")
	update.Flags().StringVar(&regions, "regions", "", "Read complete local JSON/YAML prefix/region rules")
	mutationFlags(update, &id, &revision)
	update.RunE = func(cmd *cobra.Command, args []string) error {
		if _, err := config.ParseID(args[0]); err != nil {
			return control.ErrRequest
		}
		params := map[string]any{"subscription_id": args[0]}
		if cmd.Flags().Changed("input") {
			value, err := subscriptionDocument(r.ctx, input)
			if err != nil {
				return err
			}
			if _, ok := value.(map[string]any); !ok {
				return control.ErrRequest
			}
			params["intent"] = value
		}
		if cmd.Flags().Changed("regions") {
			value, err := subscriptionDocument(r.ctx, regions)
			if err != nil {
				return err
			}
			if _, ok := value.([]any); !ok {
				return control.ErrRequest
			}
			params["region_rules"] = value
		}
		// This provisional envelope only validates the local declaration's schema.
		// mutate owns the real identity and observed/explicit revision and invokes the
		// same service use case; no draft is saved and no service is implicitly started.
		validationID, validationRevision := id, revision
		if validationID == "" {
			validationID = "validate-subscription-update"
		}
		if validationRevision == "" {
			validationRevision = "unobserved-revision"
		}
		params["expected_revision"] = validationRevision
		raw, err := makeRequest(validationID, "subscription.update", params)
		if err != nil {
			return control.ErrRequest
		}
		request, err := control.ParseRequest(raw)
		if err != nil {
			return control.ErrRequest
		}
		if err = control.ValidateSourceUpdateRequest(request); err != nil {
			return control.ErrRequest
		}
		delete(params, "expected_revision")
		return r.mutate("subscription.update", id, revision, params)
	}
	parent.AddCommand(list, update)
	return parent
}

// Explicit declaration files need not be Store-private; normal symlinks to
// regular files are allowed. NONBLOCK prevents a supplied FIFO from hanging
// before type validation. Neither filesystem errors nor document bytes escape.
func subscriptionDocument(ctx context.Context, path string) (any, error) {
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
	value, err := document.Decode(raw, document.Limits{MaxBytes: control.MaxRequestBytes})
	if err != nil {
		return nil, control.ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return value, nil
}
