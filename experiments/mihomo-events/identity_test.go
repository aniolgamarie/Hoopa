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

// This opt-in test is a real 100-flow assertion. A visibility failure is a
// failure, never a smaller denominator or a retroactively unsupported sample.
func TestNativeCandidateIdentity(t *testing.T) {
	candidate := os.Getenv("HOOPA_NATIVE_CANDIDATE")
	sha := os.Getenv("HOOPA_NATIVE_CANDIDATE_SHA256")
	if candidate == "" || sha == "" {
		t.Skip("explicit hash-pinned isolated native candidate and loopback permission required; G01 unverified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "native_probe.py", "--candidate", candidate, "--sha256", sha, "--count", "100", "--require-identity")
	raw, runErr := command.CombinedOutput()
	var result struct {
		Passed           bool           `json:"passed"`
		Preregistered    int            `json:"preregistered"`
		IdentityExact    *int           `json:"identity_exact"`
		TerminalExact    *int           `json:"terminal_exact"`
		TargetExact      *int           `json:"target_exact"`
		SourceProcesses  int            `json:"source_processes"`
		TargetDomains    int            `json:"target_domains"`
		UnknownReasons   map[string]int `json:"identity_unknown_reasons"`
		PrivateDirectory string         `json:"private_directory"`
	}
	if json.Unmarshal(raw, &result) != nil {
		t.Fatalf("native probe did not produce a complete summary (execution error: %v)", runErr)
	}
	t.Logf("G01 preregistered=%d identity=%v terminal=%v target=%v processes=%d domains=%d unknown=%v evidence=%s", result.Preregistered, countValue(result.IdentityExact), countValue(result.TerminalExact), countValue(result.TargetExact), result.SourceProcesses, result.TargetDomains, result.UnknownReasons, result.PrivateDirectory)
	if runErr != nil || !result.Passed || result.Preregistered != 100 || result.SourceProcesses != 2 || result.TargetDomains != 2 || result.IdentityExact == nil || *result.IdentityExact != 100 || result.TerminalExact == nil || *result.TerminalExact != 100 || result.TargetExact == nil || *result.TargetExact != 100 {
		t.Fatal("G01 native identity/terminal assertion not satisfied; preserve all 100 preregistered samples and block dependent tasks")
	}
}

func countValue(count *int) any {
	if count == nil {
		return "unavailable"
	}
	return *count
}

// This opt-in entry revalidates existing source5 evidence, without running a
// candidate or opening a listener. Positive coverage alone does not pass G01.
func TestSource5RegisteredIdentityPositives(t *testing.T) {
	names := []string{
		"HOOPA_SOURCE5_FORMAL_REGISTRATION",
		"HOOPA_SOURCE5_FORMAL_REGISTRATION_SHA256",
		"HOOPA_SOURCE5_FORMAL_VALIDATOR",
		"HOOPA_SOURCE5_FORMAL_VALIDATOR_SHA256",
	}
	values := make([]string, len(names))
	provided := 0
	for i, name := range names {
		values[i] = os.Getenv(name)
		if values[i] != "" {
			provided++
		}
	}
	if provided == 0 {
		t.Skip("explicit hash-pinned source5 registration and validator required; source5 positives and G01 unverified")
	}
	if provided != len(names) {
		t.Fatal("all four HOOPA_SOURCE5_FORMAL_* settings are required; partial configuration is not an unverified skip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-B", "source5_identity_probe.py",
		"--registration", values[0], "--registration-sha256", values[1],
		"--validator", values[2], "--validator-sha256", values[3])
	raw, runErr := command.CombinedOutput()
	var result struct {
		Schema                 string `json:"schema"`
		Passed                 bool   `json:"passed"`
		Registered             int    `json:"registered"`
		PositiveSlotsPassed    int    `json:"positive_slots_passed"`
		SourceProcesses        int    `json:"source_processes"`
		TargetDomains          int    `json:"target_domains"`
		T127Complete           *bool  `json:"T127_complete"`
		BoundaryMatrixComplete *bool  `json:"boundary_matrix_complete"`
		FormalG01Complete      *bool  `json:"formal_g01_complete"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 probe did not produce a complete JSON summary (execution error: %v)", runErr)
	}
	if runErr != nil || result.Schema != "sourceidentity-source5-registered-identity-positives-v1" || !result.Passed ||
		result.Registered != 100 || result.PositiveSlotsPassed != 100 || result.SourceProcesses < 2 || result.TargetDomains < 2 ||
		result.T127Complete == nil || *result.T127Complete || result.BoundaryMatrixComplete == nil || *result.BoundaryMatrixComplete ||
		result.FormalG01Complete == nil || *result.FormalG01Complete {
		t.Fatal("source5 registered positive assertion failed; retain the complete 100-slot denominator and original evidence")
	}
	t.Logf("source5 registered positives=%d/%d processes=%d domains=%d; T127/G01 boundary assessment remains separate", result.PositiveSlotsPassed, result.Registered, result.SourceProcesses, result.TargetDomains)
}

// This gate hashes an already accepted evidence bundle. It starts no candidate
// and does not substitute for its original VM/checker/root acceptance.
func TestSource5AcceptedIdentityBundle(t *testing.T) {
	manifest := os.Getenv("HOOPA_SOURCE5_ACCEPTANCE")
	sha := os.Getenv("HOOPA_SOURCE5_ACCEPTANCE_SHA256")
	if manifest == "" && sha == "" {
		t.Skip("explicit hash-pinned source5 acceptance bundle required; accepted bundle integrity unverified")
	}
	if manifest == "" || sha == "" {
		t.Fatal("both HOOPA_SOURCE5_ACCEPTANCE settings are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, runErr := exec.CommandContext(ctx, "python3", "-B", "source5_identity_gate.py",
		"--manifest", manifest, "--manifest-sha256", sha).CombinedOutput()
	type count struct {
		Registered int `json:"registered"`
		Passed     int `json:"passed"`
	}
	var result struct {
		Schema       string           `json:"schema"`
		Passed       bool             `json:"passed"`
		Positive     count            `json:"positive"`
		Boundaries   map[string]count `json:"boundaries"`
		TaskComplete *bool            `json:"task_T127_complete"`
		P0Released   *bool            `json:"P0_released"`
		P1Released   *bool            `json:"P1_released"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("source5 acceptance gate did not produce complete JSON (execution error: %v)", runErr)
	}
	if runErr != nil || result.Schema != "hoopa-source5-identity-acceptance-check-v1" || !result.Passed ||
		result.Positive != (count{100, 100}) || len(result.Boundaries) != 4 ||
		result.TaskComplete == nil || !*result.TaskComplete || result.P0Released == nil || *result.P0Released ||
		result.P1Released == nil || *result.P1Released {
		t.Fatal("source5 accepted evidence integrity gate failed; preserve the original denominators and evidence")
	}
	for name, expected := range map[string]int{"pidreuse": 2, "permission": 1, "missing-proof": 2, "threevalue": 14} {
		if result.Boundaries[name] != (count{expected, expected}) {
			t.Fatalf("source5 accepted boundary count mismatch: %s", name)
		}
	}
	t.Log("accepted source5 bundle integrity verified; no new runtime samples, P0/P1 remain unreleased")
}
