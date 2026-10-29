//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"hoopa/internal/control"
	"hoopa/internal/service"
	"hoopa/internal/testsupport/p1fixture"
)

type p1ApplyPlanKey struct{ base, candidate string }
type p1SubmittedPlan struct {
	candidate string
	plan      control.Plan
}

// Apply uses the finite integration driver of the public BasicApplier. A shipped
// config compiler/service config.apply endpoint is not claimed by this adapter.
// Keep the exact proposed Plan when repeating a base/candidate, so a retry after
// commit cannot silently substitute a newly observed impact into the request.
func (h *p1ProductHarness) Apply(ctx context.Context, r applyRequest) (operationResult, error) {
	if ctx == nil || r.Fixture != verifiedFixture || h.f.fixtures[r.Candidate] == nil {
		return operationResult{}, errP1HarnessUnbuilt
	}
	key := p1ApplyPlanKey{string(r.Identity.BaseRevision), r.Candidate}
	if h.applyPlans == nil {
		h.applyPlans = map[p1ApplyPlanKey]control.Plan{}
	}
	plan, ok := h.applyPlans[key]
	if !ok {
		fixture := h.f.fixtures[r.Candidate]
		var impact control.PlanImpact
		var err error
		if submitted, reused := h.applyOriginal[r.Identity.ID]; reused && submitted.candidate == r.Candidate {
			// An identity-conflict probe changes only the requested field. Keep
			// the original proposed impact instead of replanning an already
			// applied target and rejecting the test before product admission.
			impact = submitted.plan.Document().Impact
		} else {
			active, readErr := h.f.store.ReadActive()
			if readErr != nil {
				return operationResult{}, readErr
			}
			impact, err = p1fixture.Impact(active, fixture)
			if err != nil {
				return operationResult{}, err
			}
		}
		plan, err = fixture.Plan(r.Identity.BaseRevision, impact)
		if err != nil {
			return operationResult{}, err
		}
		h.applyPlans[key] = plan
	}
	if h.applyOriginal == nil {
		h.applyOriginal = map[string]p1SubmittedPlan{}
	}
	if _, exists := h.applyOriginal[r.Identity.ID]; !exists {
		h.applyOriginal[r.Identity.ID] = p1SubmittedPlan{r.Candidate, plan}
	}
	raw := p1JSON(h.t, map[string]any{"api_version": "v" + r.Identity.Version, "request_id": r.Identity.ID, "operation": r.Identity.Name, "params": map[string]any{"plan": json.RawMessage(plan.Bytes()), "confirmed_plan_digest": plan.Digest(), "accept_disruption": r.Identity.AcceptDisruption}})
	input, err := control.ParseRequest(raw)
	if err != nil {
		return operationResult{}, err
	}
	requestRef, err := p1WriteOriginal(filepath.Join(h.directory("apply-request"), "request.json"), raw)
	if err != nil {
		return operationResult{}, err
	}
	// Release the real service writer without changing the core's intent. The
	// subsequent worker acquires the same production single-writer capability.
	if err = h.stopManager(ctx, false); err != nil {
		return operationResult{}, err
	}
	p := h.f.launch(h.nextID("apply"), []string{"apply", h.f.repo, h.f.core, filepath.Join(h.f.root, "app-first"), filepath.Join(h.f.root, "domain-first")}, raw, h.decide)
	h.manager = p
	h.extras = append(h.extras, requestRef)
	waitErr := p.Wait(ctx)
	if p.TraceError() != nil {
		return operationResult{}, errP1Harness
	}
	if p1ObservedTraceCrash(p) {
		return operationResult{}, errP1HarnessInterrupted
	}
	var exit *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exit) {
		return operationResult{}, waitErr
	}
	if p.cmd.ProcessState == nil || !p.cmd.ProcessState.Exited() {
		// A real killed worker is an interruption, never a business outcome.
		return operationResult{}, errP1Harness
	}
	file, err := os.Open(p.StdoutPath())
	if err != nil {
		return operationResult{}, err
	}
	response, readErr := io.ReadAll(io.LimitReader(file, p1CLIStdoutLimit+1))
	err = errors.Join(readErr, file.Close())
	if err != nil {
		return operationResult{}, err
	}
	body, _, _, err := validateP1CLIResponse(response, input.ID())
	if err != nil || p.cmd.ProcessState.ExitCode() != service.ExitCode(body) {
		return operationResult{}, errP1Harness
	}
	ref, err := p1WriteOriginal(filepath.Join(h.directory("apply-response"), "response.json"), response)
	if err != nil {
		return operationResult{}, err
	}
	stdoutRef := originalRef{Path: p.StdoutPath(), Bytes: int64(len(response)), SHA256: p1SHA(response)}
	waitRef, err := h.original("apply-worker-exit", map[string]any{"kind": "original-direct-apply-worker-wait", "pid": p.PID(), "executable_path": h.f.probe, "request_original": requestRef, "stdout_original": stdoutRef, "response_original": ref, "exit_code": p.cmd.ProcessState.ExitCode(), "wait_status": p.cmd.ProcessState.Sys(), "trace_completed": true})
	if err != nil {
		return operationResult{}, err
	}
	h.extras = append(h.extras, stdoutRef, ref, waitRef)
	ops, bundles, _, err := h.operations()
	if err != nil {
		return operationResult{}, err
	}
	records, err := h.traceRecords(ctx)
	if err != nil {
		return operationResult{}, err
	}
	history, err := p1ReadTraceHistory(records, ops, bundles)
	if err != nil {
		return operationResult{}, err
	}
	result, err := p1ProjectResult(input.ID(), response, ref, ops, history)
	if err != nil {
		return operationResult{}, err
	}
	result.Originals = append(result.Originals, requestRef, stdoutRef, waitRef)
	// Reopening management alone must not recover/adopt/rewrite the operation.
	if err = h.startManager(false); err != nil {
		return operationResult{}, err
	}
	return result, nil
}
