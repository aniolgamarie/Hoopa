//go:build linux && amd64 && p1integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

// Exact captured response + actual current records. A recovery response belongs
// to its independently admitted call; logical target state is a separate field.
// This helper classifies no failure from the requested scenario or injected fault.
func p1ProjectResult(requestID string, raw []byte, ref originalRef, operations map[string]operationRecord, history []operationVersion) (operationResult, error) {
	var wire struct {
		Data struct {
			ID      string `json:"operation_id"`
			Outcome string `json:"outcome"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Details struct {
				ID string `json:"operation_id"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return operationResult{}, operationPolicyError()
	}
	id := wire.Data.ID
	if id == "" {
		id = wire.Error.Details.ID
	}
	r := operationResult{RequestID: requestID, WireOutcome: wire.Data.Outcome, ErrorCode: wire.Error.Code, ResponseOperationID: id, Response: append([]byte(nil), raw...), ResponseOriginal: ref, Originals: []originalRef{ref}}
	if id != "" {
		op, ok := operations[id]
		if !ok {
			return operationResult{}, operationPolicyError()
		}
		r.Operation = op
		r.Originals = append(r.Originals, op.Original)
		if op.Operation == "operation.recover" {
			var source control.Operation
			if json.Unmarshal(op.recordSource, &source) != nil {
				return operationResult{}, operationPolicyError()
			}
			m, err := operationClassify(control.StoredOperation{Record: source, SHA256: op.Original.SHA256})
			if err != nil || m.kind != "recover" {
				return operationResult{}, operationPolicyError()
			}
			target, ok := operations[m.targetID]
			if !ok {
				return operationResult{}, operationPolicyError()
			}
			r.Operation = target
			r.RecoveryCall = &op
			r.Originals = append(r.Originals, target.Original)
			for _, version := range history {
				var old control.Operation
				if json.Unmarshal(version.Raw, &old) != nil {
					return operationResult{}, operationPolicyError()
				}
				if string(old.Identity.OperationID) == target.ID {
					r.TargetHistory = append(r.TargetHistory, version)
				}
			}
		}
	}
	r.Outcome = logicalOperationOutcome(r)
	if err := validateOperationResult(r); err != nil {
		return operationResult{}, err
	}
	return r, nil
}

func TestP1ArchivedOperationResults(t *testing.T) {
	root := os.Getenv("HOOPA_P1_PROJECTION_ARCHIVE")
	if root == "" {
		t.Fatal("explicit retained projection archive required")
	}
	records, operations, bundles, _ := p1LoadProjectionArchive(t, root)
	history, err := p1ReadTraceHistory(records, operations, bundles)
	must(t, err)
	var refs []originalRef
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(root)), "evidence-index.json"))
	must(t, err)
	must(t, json.Unmarshal(raw, &refs))
	seen := map[string]bool{}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source location")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	for _, ref := range refs {
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo, path)
		}
		if path != filepath.Join(root, "seed-response.raw") && filepath.Dir(path) != filepath.Join(root, "cli") {
			continue
		}
		ref.Path = path
		raw, err = p1HistoryOriginal(ref, control.MaxResultBytes+1)
		must(t, err)
		var wireID struct {
			Data struct {
				ID string `json:"operation_id"`
			} `json:"data"`
		}
		must(t, json.Unmarshal(raw, &wireID))
		// The accepted invocation ID comes from its independently retained
		// journal source, not from response.request_id. All seven calls are
		// inventoried uniquely below; rejected invocations need caller input.
		source, ok := operations[wireID.Data.ID]
		if !ok {
			t.Fatal("response has no independent admitted invocation")
		}
		result, err := p1ProjectResult(source.RequestID, raw, ref, operations, history)
		must(t, err)
		if result.ResponseOperationID == "" || seen[result.ResponseOperationID] || result.Outcome != "committed" || result.WireOutcome != "committed" || result.ErrorCode != "" {
			t.Fatal("actual result classification or full response inventory mismatch")
		}
		seen[result.ResponseOperationID] = true
		for _, change := range []string{"logical-outcome", "wire-outcome", "wire-error"} {
			bad := result
			switch change {
			case "logical-outcome":
				bad.Outcome = "adopted"
			case "wire-outcome":
				bad.WireOutcome = "uncertain"
			case "wire-error":
				bad.ErrorCode = "CONFLICT"
			}
			if validateOperationResult(bad) == nil {
				t.Fatal("invented response classification accepted")
			}
		}
	}
	if len(seen) != 7 || len(seen) != len(operations) {
		t.Fatal("seven actual response originals required")
	}
	t.Logf("actual archived result bindings=%d; no new core execution", len(seen))
}

func TestP1OperationPolicyUnadmittedErrorClassification(t *testing.T) {
	// Synthetic wire envelope for pure binding policy, not a product rejection.
	for _, code := range []string{"CONFLICT", "OPERATION_CAPACITY_EXHAUSTED"} {
		t.Run(code, func(t *testing.T) {
			raw := operationEncode(map[string]any{"api_version": "v1", "request_id": "synthetic-rejected", "ok": false, "data": nil, "error": map[string]any{"code": code, "message": code, "retryable": false}, "warnings": []any{}})
			ref := originalRef{Path: "synthetic-error-envelope", Bytes: int64(len(raw)), SHA256: operationSHA(raw)}
			r, err := p1ProjectResult("synthetic-rejected", raw, ref, map[string]operationRecord{}, nil)
			must(t, err)
			if _, err := p1ProjectResult("different-invocation", raw, ref, map[string]operationRecord{}, nil); err == nil {
				t.Fatal("unadmitted error accepted for a different invocation")
			}
			if r.Outcome != "" || r.WireOutcome != "" || r.ErrorCode != code || r.Operation.ID != "" {
				t.Fatal("unadmitted error turned into a mutation outcome")
			}
			for _, outcome := range []string{"conflict", "revision_conflict", "capacity_exhausted", "blocked_degraded", "uncertain", "adopted"} {
				r.Outcome = outcome
				if validateOperationResult(r) == nil {
					t.Fatal("scenario label accepted as an operation outcome")
				}
			}
		})
	}
}

func TestP1OperationPolicyRecoveryOutcomeSeparation(t *testing.T) {
	// Separate call/target outcomes over synthetic policy records only.
	e, target, _, restored := policyRollbackAfterOpen(t, control.RolledBack)
	gen, id := target.Previous.Generation, config.ID(target.ID)
	base := config.ActivePointer{Version: 1, Revision: target.BaseRevision, Generation: &gen, ManifestSHA256: target.Previous.ManifestSHA256, OperationID: &id, DesiredRuntime: "running"}
	call := policyRecord(t, "recover", "recover-rolled-back", base, target.Previous, bundleSnapshot{}, restored, &target)
	e.Operations[call.ID] = call
	r := policyResult(t, e, call)
	must(t, validateOperationResult(r))
	if r.Outcome != "rolled_back" || r.WireOutcome != "committed" || r.ErrorCode != "" || r.RecoveryCall == nil {
		t.Fatal("committed recovery call overwrote original rollback outcome")
	}
	r.Outcome = "committed"
	if validateOperationResult(r) == nil {
		t.Fatal("call success was presented as candidate success")
	}
	pending := policyRewrite(t, call, func(source *control.Operation) {
		source.Steps = source.Steps[:len(source.Steps)-1]
		last := &source.Steps[len(source.Steps)-1]
		var body map[string]any
		must(t, json.Unmarshal(last.Result, &body))
		body["data"].(map[string]any)["outcome"] = "pending"
		last.Result = mustCanonical(operationEncode(body))
		source.Phase = last.Phase
		source.Result = last.Result
	})
	e.Operations[pending.ID] = pending
	r = policyResult(t, e, pending)
	must(t, validateOperationResult(r))
	if r.Outcome != "pending" || r.WireOutcome != "pending" || r.Operation.Phase != "rolled_back" {
		t.Fatal("unfinished recovery call hidden by prior terminal target")
	}
	r.Outcome = "rolled_back"
	if validateOperationResult(r) == nil {
		t.Fatal("unfinished recovery reported a completed logical result")
	}
	for _, phase := range []control.Phase{control.RollingBack, control.RolledBack} {
		t.Run("reject-recovery-call-"+string(phase), func(t *testing.T) {
			var source control.Operation
			must(t, json.Unmarshal(pending.recordSource, &source))
			last := source.Steps[len(source.Steps)-1]
			last.Sequence++
			last.Phase = control.RollingBack
			source.Steps = append(source.Steps, last)
			if phase == control.RolledBack {
				last.Sequence++
				last.Phase = control.RolledBack
				source.Steps = append(source.Steps, last)
			}
			source.Phase = phase
			if _, err := operationClassify(control.StoredOperation{Record: source}); err == nil {
				t.Fatal("impossible recovery-call rollback history accepted")
			}
			bad := r
			copy := *r.RecoveryCall
			copy.Phase = string(phase)
			bad.RecoveryCall = &copy
			if logicalOperationOutcome(bad) != "" {
				t.Fatal("impossible recovery phase became an original target result")
			}
		})
	}
}
