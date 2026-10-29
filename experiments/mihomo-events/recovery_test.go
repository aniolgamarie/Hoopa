//go:build linux && amd64

package mihomoevents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// This entry verifies the integrity of independently accepted original runs.
// It starts no VM, core, collector, listener, decoder, or recovery writer.
func TestSource5AcceptedRecoveryBundle(t *testing.T) {
	manifest := os.Getenv("HOOPA_T128_ACCEPTANCE")
	sha := os.Getenv("HOOPA_T128_ACCEPTANCE_SHA256")
	if manifest == "" && sha == "" {
		t.Skip("explicit hash-pinned T128 acceptance bundle required; recovery matrix unverified")
	}
	if manifest == "" || sha == "" {
		t.Fatal("both HOOPA_T128_ACCEPTANCE settings are required; partial input is not an unverified skip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, runErr := exec.CommandContext(ctx, "python3", "-B", "source5_recovery_gate.py",
		"--manifest", manifest, "--manifest-sha256", sha).CombinedOutput()
	type count struct {
		Registered int `json:"registered"`
		Executed   int `json:"executed"`
		Passed     int `json:"passed"`
	}
	var result struct {
		Schema            string           `json:"schema"`
		Passed            bool             `json:"passed"`
		Cases             map[string]count `json:"cases"`
		VMs               int              `json:"vms"`
		Slots             int              `json:"slots"`
		NewRuntimeSamples *int             `json:"new_runtime_samples"`
		TaskComplete      *bool            `json:"task_T128_complete"`
		P0Released        *bool            `json:"P0_released"`
		P1Released        *bool            `json:"P1_released"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 recovery gate did not produce complete JSON (execution error: %v)", runErr)
	}
	if runErr != nil || result.Schema != "hoopa-source5-recovery-acceptance-check-v1" || !result.Passed ||
		result.VMs != 8 || result.Slots != 16 || len(result.Cases) != 8 ||
		result.NewRuntimeSamples == nil || *result.NewRuntimeSamples != 0 ||
		result.TaskComplete == nil || !*result.TaskComplete ||
		result.P0Released == nil || *result.P0Released || result.P1Released == nil || *result.P1Released {
		t.Fatal("source5 recovery evidence integrity failed; retain every original registration and failure")
	}
	for name, n := range map[string]int{
		"offline-drop-closed": 2, "offline-reject-closed": 2, "held-opened": 2,
		"combined-reopen": 2, "source-write-enospc": 2, "logical-quota": 3,
		"active-term": 1, "buffer-conflict": 2,
	} {
		if result.Cases[name] != (count{n, n, n}) {
			t.Fatalf("source5 recovery case denominator mismatch: %s", name)
		}
	}
	t.Log("accepted recovery matrix integrity verified; no new runtime samples, P0/P1 remain unreleased")
}
