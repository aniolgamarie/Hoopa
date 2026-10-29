//go:build linux && amd64

package mihomoevents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func routingAcceptanceInput(manifest, sha string) (bool, error) {
	if manifest == "" && sha == "" {
		return false, nil
	}
	if manifest == "" || sha == "" {
		return false, errors.New("both HOOPA_T016_ACCEPTANCE settings required; partial input cannot skip")
	}
	return true, nil
}

func TestSource5RoutingAcceptanceInputContract(t *testing.T) {
	for _, c := range []struct {
		manifest, sha string
		run, failed   bool
	}{{"", "", false, false}, {"x", "", false, true}, {"", "x", false, true}, {"x", "y", true, false}} {
		run, err := routingAcceptanceInput(c.manifest, c.sha)
		if run != c.run || (err != nil) != c.failed {
			t.Fatal("routing acceptance partial input contract changed")
		}
	}
}

// This only reads original accepted evidence. It creates no runtime samples.
func TestSource5AcceptedRoutingBundle(t *testing.T) {
	manifest, sha := os.Getenv("HOOPA_T016_ACCEPTANCE"), os.Getenv("HOOPA_T016_ACCEPTANCE_SHA256")
	run, err := routingAcceptanceInput(manifest, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !run {
		t.Skip("explicit hash-pinned T016 acceptance required; routing matrix unverified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, runErr := exec.CommandContext(ctx, "python3", "-B", "source5_routing_gate.py",
		"--manifest", manifest, "--manifest-sha256", sha).CombinedOutput()
	var result struct {
		Schema       string                    `json:"schema"`
		Passed       bool                      `json:"passed"`
		Counts       map[string]*int           `json:"counts"`
		Families     map[string]map[string]int `json:"families"`
		NewSamples   *int                      `json:"new_runtime_samples"`
		TaskComplete *bool                     `json:"task_T016_complete"`
		P0Released   *bool                     `json:"P0_released"`
		P1Released   *bool                     `json:"P1_released"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 routing gate did not return complete JSON (execution error: %v)", runErr)
	}
	if runErr != nil || result.Schema != "hoopa-source5-routing-acceptance-check-v1" || !result.Passed ||
		result.NewSamples == nil || *result.NewSamples != 0 || result.TaskComplete == nil || !*result.TaskComplete ||
		result.P0Released == nil || *result.P0Released || result.P1Released == nil || *result.P1Released {
		t.Fatal("source5 routing integrity failed; retain original evidence and wrapper scores")
	}
	for key, want := range map[string]int{"VMs": 39, "application_slots": 127, "admitted_flows": 124, "source_epochs": 59, "mandatory_frames": 805} {
		if result.Counts[key] == nil || *result.Counts[key] != want {
			t.Fatalf("source5 routing denominator mismatch: %s", key)
		}
	}
	cp, frames := result.Counts["checkpoints"], result.Counts["frames"]
	if cp == nil || frames == nil || *cp < 9 || *cp > 265 || *frames != 805+*cp {
		t.Fatal("source5 routing actual frame/checkpoint census mismatch")
	}
	want := []string{"dual_owner_attach_prerequisite", "new_core_legacy_single_entry", "current_direct_two_entry",
		"multi_entry_control_manager_offline", "native_partial_bind_v4", "native_typed_map_alias_v4", "C01_concurrent_control_v2",
		"F10_F33_full_rollback_v2", "F12_active_publication_failure_v2", "F13_failed_rollback_degraded_v1", "F11_persistence_matrix_v2",
		"provider_refresh_v2", "provider_switch_v3", "ordered_routing_nine_rows", "unreachable_selected_endpoint_failure_host_v2",
		"finite_missing_proof_restore_and_sticky_refusal"}
	if len(result.Families) != len(want) {
		t.Fatal("source5 routing sixteen-family census mismatch")
	}
	for _, name := range want {
		if _, ok := result.Families[name]; !ok {
			t.Fatalf("source5 routing accepted family missing: %s", name)
		}
	}
	t.Log("accepted routing evidence integrity verified; no new runtime samples, P0/P1 unreleased")
}
