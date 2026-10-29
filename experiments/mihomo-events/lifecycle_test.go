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

// This checks original accepted files only. It creates no runtime samples.
func TestSource5AcceptedLifecycleBundle(t *testing.T) {
	manifest := os.Getenv("HOOPA_T015_ACCEPTANCE")
	sha := os.Getenv("HOOPA_T015_ACCEPTANCE_SHA256")
	if manifest == "" && sha == "" {
		t.Skip("explicit hash-pinned T015 acceptance required; lifecycle matrix unverified")
	}
	if manifest == "" || sha == "" {
		t.Fatal("both HOOPA_T015_ACCEPTANCE settings required; partial input cannot skip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, runErr := exec.CommandContext(ctx, "python3", "-B", "source5_lifecycle_gate.py",
		"--manifest", manifest, "--manifest-sha256", sha).CombinedOutput()
	type counts struct {
		Registered      *int  `json:"registered"`
		Attempted       *int  `json:"attempted"`
		Executed        *int  `json:"executed"`
		Passed          *int  `json:"passed"`
		FailedOrPending *int  `json:"failed_or_pending"`
		Unobserved      *int  `json:"unobserved"`
		VMStarted       *bool `json:"vm_started"`
		Forwarded       *int  `json:"forwarded_flows"`
		Control         *int  `json:"control_probes"`
		Filesystem      *int  `json:"filesystem_probes"`
	}
	var result struct {
		Schema       string            `json:"schema"`
		Passed       bool              `json:"passed"`
		Cases        map[string]counts `json:"cases"`
		VMs          *int              `json:"vms"`
		Slots        *int              `json:"slots"`
		Forwarded    *int              `json:"forwarded_flows"`
		Control      *int              `json:"control_probes"`
		Filesystem   *int              `json:"filesystem_probes"`
		NewSamples   *int              `json:"new_runtime_samples"`
		TaskComplete *bool             `json:"task_T015_complete"`
		P0Released   *bool             `json:"P0_released"`
		P1Released   *bool             `json:"P1_released"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 lifecycle gate did not return complete JSON (execution error: %v)", runErr)
	}
	equal := func(value *int, want int) bool { return value != nil && *value == want }
	if runErr != nil || result.Schema != "hoopa-source5-lifecycle-acceptance-check-v1" || !result.Passed ||
		!equal(result.VMs, 11) || !equal(result.Slots, 22) || !equal(result.Forwarded, 18) ||
		!equal(result.Control, 14) || !equal(result.Filesystem, 14) || !equal(result.NewSamples, 0) ||
		result.TaskComplete == nil || !*result.TaskComplete ||
		result.P0Released == nil || *result.P0Released || result.P1Released == nil || *result.P1Released {
		t.Fatal("source5 lifecycle integrity failed; retain original evidence and wrapper scores")
	}
	want := map[string][4]int{
		"takeover": {2, 2, 0, 0}, "spawn-window": {2, 2, 0, 0}, "foreign-controller": {0, 0, 1, 0},
		"missing-claim": {2, 2, 1, 0}, "core-pid-reuse": {1, 1, 1, 0}, "collector-reopen": {2, 2, 0, 0},
		"management-data-auth": {6, 2, 11, 0}, "spool-permissions": {1, 1, 0, 14},
		"frontend-quit-login-logout": {2, 2, 0, 0}, "ssh-disconnect": {2, 2, 0, 0}, "guest-s3": {2, 2, 0, 0},
	}
	if len(result.Cases) != len(want) {
		t.Fatal("source5 lifecycle case census mismatch")
	}
	for name, n := range want {
		c, ok := result.Cases[name]
		if !ok || !equal(c.Registered, n[0]) || !equal(c.Attempted, n[0]) || !equal(c.Executed, n[0]) ||
			!equal(c.Passed, n[0]) || !equal(c.FailedOrPending, 0) || !equal(c.Unobserved, 0) ||
			c.VMStarted == nil || !*c.VMStarted || !equal(c.Forwarded, n[1]) ||
			!equal(c.Control, n[2]) || !equal(c.Filesystem, n[3]) {
			t.Fatalf("source5 lifecycle denominator mismatch: %s", name)
		}
	}
	t.Log("accepted lifecycle evidence integrity verified; no new runtime samples, P0/P1 unreleased")
}
