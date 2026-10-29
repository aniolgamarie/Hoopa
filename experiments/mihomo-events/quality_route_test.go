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

// This only verifies independently accepted evidence. It starts no proxy or VM.
func TestSource5AcceptedQualityBundle(t *testing.T) {
	manifest := os.Getenv("HOOPA_T129_ACCEPTANCE")
	sha := os.Getenv("HOOPA_T129_ACCEPTANCE_SHA256")
	if manifest == "" && sha == "" {
		t.Skip("explicit hash-pinned T129 acceptance bundle required; quality matrix unverified")
	}
	if manifest == "" || sha == "" {
		t.Fatal("both HOOPA_T129_ACCEPTANCE settings required; partial input cannot skip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, runErr := exec.CommandContext(ctx, "python3", "-B", "source5_quality_gate.py",
		"--manifest", manifest, "--manifest-sha256", sha).CombinedOutput()
	type count struct {
		Registered      int   `json:"registered"`
		Attempted       int   `json:"attempted"`
		Executed        int   `json:"executed"`
		Passed          int   `json:"passed"`
		FailedOrPending *int  `json:"failed_or_pending"`
		Unobserved      *int  `json:"unobserved"`
		VMStarted       *bool `json:"vm_started"`
	}
	var result struct {
		Schema            string           `json:"schema"`
		Passed            bool             `json:"passed"`
		Cases             map[string]count `json:"cases"`
		VMs               int              `json:"vms"`
		Slots             int              `json:"slots"`
		NewRuntimeSamples *int             `json:"new_runtime_samples"`
		TaskComplete      *bool            `json:"task_T129_complete"`
		P0Released        *bool            `json:"P0_released"`
		P1Released        *bool            `json:"P1_released"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 quality gate did not return complete JSON (execution error: %v)", runErr)
	}
	if runErr != nil || result.Schema != "hoopa-source5-quality-acceptance-check-v1" || !result.Passed ||
		result.VMs != 6 || result.Slots != 21 || len(result.Cases) != 6 ||
		result.NewRuntimeSamples == nil || *result.NewRuntimeSamples != 0 ||
		result.TaskComplete == nil || !*result.TaskComplete ||
		result.P0Released == nil || *result.P0Released || result.P1Released == nil || *result.P1Released {
		t.Fatal("source5 quality evidence integrity failed; retain original registrations and failures")
	}
	for name, n := range map[string]int{
		"http-ready": 2, "socks-ready": 2, "direct-slow-dns": 2,
		"full-replacement": 4, "upstream-failure": 7, "nested-retry": 4,
	} {
		c := result.Cases[name]
		if c.Registered != n || c.Attempted != n || c.Executed != n || c.Passed != n ||
			c.FailedOrPending == nil || *c.FailedOrPending != 0 ||
			c.Unobserved == nil || *c.Unobserved != 0 || c.VMStarted == nil || !*c.VMStarted {
			t.Fatalf("source5 quality case denominator mismatch: %s", name)
		}
	}
	t.Log("accepted quality evidence integrity verified; no new runtime samples, P0/P1 remain unreleased")
}
