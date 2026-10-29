//go:build linux

// Command preflight emits an explicit experiment report, not a product command.
package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"hoopa/experiments/sourceidentity"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := struct {
		Scope     string                          `json:"scope"`
		ParentPID int                             `json:"observer_parent_pid"`
		Report    sourceidentity.VisibilityReport `json:"report"`
	}{"T130_visibility_diagnostic_only", os.Getppid(), sourceidentity.InspectVisibility(ctx)}
	// Exit zero means the diagnostic was emitted, regardless of its status.
	// Consumers must inspect report.status; this does not assert G01 success.
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(1)
	}
}
