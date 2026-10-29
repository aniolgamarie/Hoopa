//go:build p1integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"hoopa/internal/control"
)

// Synthetic policy fixture: candidate verification and publication succeeded,
// its second verification failed, and restoration reached the supplied phase.
// No runtime event, original syscall or actual failure is claimed by this test.
func policyRollbackAfterOpen(t *testing.T, phase control.Phase) (productEvidence, operationRecord, processIdentity, processIdentity) {
	t.Helper()
	e, base := policyEvidence(t, "apply")
	trace := &policyTrace{e: e, index: uint64(len(e.Boundaries))}
	old := policyIdentity("base", 101)
	target := policyBundle()
	target.Generation = "replacement-generation"
	target.ManifestSHA256 = strings.Repeat("1", 64)
	target.ConfigSHA256 = strings.Repeat("2", 64)
	candidate := policyIdentity("candidate", 202)
	candidate.Generation = target.Generation
	restored := policyIdentity("rollback", 303)
	op := policyRecord(t, "apply", "replace-after-open", policyBase(base, "running"), policyBundle(), target, candidate, nil)
	var source control.Operation
	must(t, json.Unmarshal(op.recordSource, &source))
	source.Steps = source.Steps[:len(source.Steps)-1] // candidate has not committed
	var state operationApplyEvidence
	must(t, json.Unmarshal(source.Steps[len(source.Steps)-1].Evidence, &state))
	state.TargetStopped = true
	state.RollbackRunID = &restored.RunID
	state.RollbackWorkdir = "synthetic-rollback-workdir"
	appendPhase := func(p control.Phase) {
		state.Step = "synthetic-rollback-" + string(p)
		result := mustCanonical(operationEncode(map[string]any{"api_version": "v1", "request_id": op.RequestID, "ok": true, "data": map[string]any{"operation_id": op.ID, "phase": p, "outcome": p}, "error": nil, "warnings": []any{}}))
		source.Steps = append(source.Steps, control.Step{Sequence: len(source.Steps) + 1, Phase: p, Evidence: mustCanonical(operationEncode(state)), Result: result})
		source.Phase = p
		source.Result = result
	}
	appendPhase(control.RollingBack)
	if phase != control.RollingBack {
		appendPhase(phase)
	}
	source.BodySHA256 = operationSHA(operationEncode(struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(source.Request), source.Steps}))
	raw := operationEncode(source)
	ref := originalRef{Path: "synthetic-rollback-source", Bytes: int64(len(raw)), SHA256: operationSHA(raw)}
	var err error
	op, err = projectOperationRecord(control.StoredOperation{Record: source, SHA256: ref.SHA256}, raw, ref, policyBundle(), target)
	must(t, err)
	trace.add(op)
	trace.event("operation-durable", op, nil)
	stop := func(p processIdentity) {
		n := trace.event("core-stop", op, &p)
		trace.e.Signals = append(trace.e.Signals, signalInvocation{BoundaryIndex: trace.e.Boundaries[n].Index, Signal: "SIGTERM", Target: p})
		trace.event("core-stopped", op, &p)
	}
	spawn := func(p processIdentity) {
		trace.event("spawn-intent-durable", op, &p)
		n := trace.event("core-spawn", op, &p)
		trace.e.Launches = append(trace.e.Launches, launchInvocation{BoundaryIndex: trace.e.Boundaries[n].Index, ID: string(p.RunID), NonceSHA256: p.LaunchNonceSHA256, Instance: p})
		for _, name := range []string{"registration-durable", "preopen-verified", "entry-opening", "entry-open"} {
			trace.event(name, op, &p)
		}
	}
	stop(old)
	spawn(candidate)
	trace.event("target-verified", op, &candidate)
	trace.event("publishing-durable", op, nil)
	trace.event("active-published", op, nil)
	trace.event("rolling-back-durable", op, nil)
	stop(candidate)
	spawn(restored)
	trace.event("old-state-verified", op, &restored)
	trace.event("old-active-published", op, nil)
	if phase == control.RolledBack {
		trace.event("rolled-back-durable", op, nil)
	}
	if phase == control.Degraded {
		trace.event("degraded-durable", op, nil)
	}
	return trace.e, op, candidate, restored
}

func TestP1OperationPolicyRollbackRoles(t *testing.T) {
	for _, phase := range []control.Phase{control.RolledBack, control.RollingBack, control.Degraded} {
		t.Run(string(phase), func(t *testing.T) {
			e, op, candidate, restored := policyRollbackAfterOpen(t, phase)
			must(t, validateOperationOrdering(e, policyCoreSHA))
			var source control.Operation
			must(t, json.Unmarshal(op.recordSource, &source))
			meaning, err := operationClassify(control.StoredOperation{Record: source, SHA256: op.Original.SHA256})
			must(t, err)
			if meaning.targetRunID != candidate.RunID || meaning.rollbackRunID != restored.RunID || meaning.runID != restored.RunID {
				t.Fatal("historical target and current rollback roles collapsed")
			}
			for _, role := range []string{"target-verified", "old-state-verified"} {
				t.Run("reject-swapped-"+role, func(t *testing.T) {
					bad := clonePolicyEvidence(e)
					for i, v := range bad.Boundaries {
						if v.OperationID == op.ID && v.Name == role {
							p := restored
							if role == "old-state-verified" {
								p = candidate
							}
							bad.Boundaries[i].Instance = &p
							bad.Boundaries[i].NonceSHA256 = p.LaunchNonceSHA256
						}
					}
					if validateOperationOrdering(bad, policyCoreSHA) == nil {
						t.Fatal("wrong historical run role accepted")
					}
				})
			}
			// A pre-admission recovery proof must select the restored incarnation,
			// also while the original is RollingBack or subsequently Degraded.
			proof := boundaryEvent{Index: uint64(len(e.Boundaries) + 1), Name: "ownership-preflight", TargetOperationID: op.ID, OriginOperationID: op.ID, RequestID: "unadmitted-recovery", Instance: &restored, NonceSHA256: restored.LaunchNonceSHA256, Originals: []originalRef{{Path: "synthetic-recovery-proof", Bytes: 1, SHA256: policyCoreSHA}}}
			e.Boundaries = append(e.Boundaries, proof)
			must(t, validateOperationOrdering(e, policyCoreSHA))
			e.Boundaries[len(e.Boundaries)-1].Instance = &candidate
			e.Boundaries[len(e.Boundaries)-1].NonceSHA256 = candidate.LaunchNonceSHA256
			if validateOperationOrdering(e, policyCoreSHA) == nil {
				t.Fatal("recovery selected the stopped candidate instead of restored run")
			}
		})
	}
}
