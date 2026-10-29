//go:build p1integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
)

// All traces/identities below are synthetic policy fixtures. They exercise a
// pure validator, never productHarness, a core, actual syscall order or acceptance.
const policyCoreSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func policyBundle() bundleSnapshot {
	return bundleSnapshot{Generation: "policy-generation", ManifestSHA256: strings.Repeat("c", 64), ConfigSHA256: strings.Repeat("d", 64)}
}
func policyIdentity(id string, pid int) processIdentity {
	return processIdentity{BootID: "synthetic-policy-boot", OSStartIdentity: "1001", BinaryPath: "/synthetic/core", BinarySHA256: policyCoreSHA, LaunchNonceSHA256: operationSHA([]byte("nonce-" + id)), PrivateWorkdir: "/synthetic/work/" + id, PID: pid, Generation: "policy-generation", RunID: config.RunID(id + "-run")}
}
func policyPlatform(p processIdentity) platform.ProcessIdentity {
	return platform.ProcessIdentity{PID: p.PID, UID: 1002, BootID: p.BootID, StartTicks: 1001, BinaryPath: p.BinaryPath, BinarySHA256: p.BinarySHA256, Workdir: p.PrivateWorkdir, NonceSHA256: p.LaunchNonceSHA256}
}
func policyBase(op operationRecord, desired string) config.ActivePointer {
	generation := op.Target.Generation
	id := config.ID(op.ID)
	revision := op.BaseRevision
	if op.TargetRevision != nil {
		revision = *op.TargetRevision
	}
	return config.ActivePointer{Version: 1, Revision: revision, Generation: &generation, ManifestSHA256: op.Target.ManifestSHA256, OperationID: &id, DesiredRuntime: desired}
}
func policyRecord(t *testing.T, kind, id string, base config.ActivePointer, previous, target bundleSnapshot, p processIdentity, targetOp *operationRecord) operationRecord {
	t.Helper()
	name := map[string]string{"apply": "config.apply", "fresh-start": "runtime.start", "restart": "runtime.start", "adopt-running": "runtime.start", "stop": "runtime.stop", "recover": "operation.recover", "service-stop": "service.stop"}[kind]
	params := map[string]any{"expected_revision": base.Revision}
	intent := control.Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, TargetGeneration: base.Generation, CandidateDigest: base.ManifestSHA256}
	var evidence any
	targetRevision := config.Revision("revision-" + id)
	switch kind {
	case "apply":
		gen := target.Generation
		intent.TargetGeneration = &gen
		intent.TargetRevision = &targetRevision
		impact := control.PlanImpact{RequiresDisruption: true, AffectedApps: []string{"entry-a", "entry-b"}, TargetGeneration: string(gen), ConnectionEffect: "interrupt", RuntimeAction: "start"}
		if base.Generation != nil {
			impact.RuntimeAction = "replace"
		}
		// Schema-valid synthetic Plan only, never a compiler/material source.
		doc := map[string]any{"schema_version": 1, "base_revision": base.Revision, "compiler_version": "synthetic-policy-driver", "capabilities_digest": policyCoreSHA, "intent": map[string]any{}, "inputs": []control.PlanInput{{Kind: "core_binary", ID: "driver", SHA256: policyCoreSHA}}, "impact": impact, "blockers": []control.PlanBlocker{}}
		plan, err := control.BuildPlan(operationEncode(doc), func(json.RawMessage, []control.PlanInput) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		params = map[string]any{"plan": json.RawMessage(plan.Bytes()), "confirmed_plan_digest": plan.Digest(), "accept_disruption": true}
		intent.CandidateDigest = plan.Digest()
		intent.ImpactDigest = operationSHA(mustCanonical(operationEncode(impact)))
		run := p.RunID
		evidence = operationApplyEvidence{Version: 1, Step: "target-committed", Base: base, TargetManifestSHA256: target.ManifestSHA256, TargetRunID: &run, OldStopped: true}
		if base.Generation != nil {
			e := evidence.(operationApplyEvidence)
			old := config.RunID("base-run")
			e.OldRunID = &old
			evidence = e
		}
	case "fresh-start", "restart", "stop":
		params["control_only"] = false
		mode := "start"
		desired := "running"
		gate := "open"
		source := config.RunID("base-run")
		exit := config.ID("")
		if base.OperationID != nil {
			exit = *base.OperationID
		}
		var run *config.RunID
		work := "work-" + id
		if kind == "stop" {
			delete(params, "control_only")
			params["accept_disruption"] = true
			mode = "stop"
			desired = "stopped"
			work = ""
		} else {
			targetRun := p.RunID
			run = &targetRun
		}
		if kind == "restart" {
			mode = "restart"
			gate = "closed"
		} else {
			intent.TargetRevision = &targetRevision
		}
		evidence = operationLifecycleEvidence{Version: 1, Step: "lifecycle-committed", Mode: mode, Desired: desired, Base: base, SourceRunID: source, ExitOperationID: exit, ExitGate: gate, SourceStopped: true, Workdir: work, TargetRunID: run}
	case "adopt-running":
		params["control_only"] = false
		evidence = operationAdoptionEvidence{Version: 1, Kind: "adopt-running", Step: "adopt-committed", Base: base, RunID: p.RunID, Identity: policyPlatform(p)}
	case "service-stop":
		intent.CandidateDigest = operationSHA(mustCanonical(operationEncode(base)))
		evidence = operationServiceEvidence{Version: 1, Kind: "service-stop", Step: "service-stop-committed", Base: base, Instance: strings.Repeat("e", 48), AdmissionClosed: true}
	case "recover":
		if targetOp == nil {
			t.Fatal("missing synthetic target")
		}
		params["operation_id"] = targetOp.ID
		intent.TargetGeneration = nil
		intent.CandidateDigest = targetOp.RequestDigest
		evidence = operationRecoveryEvidence{Version: 1, TargetID: config.ID(targetOp.ID), TargetDigest: targetOp.RequestDigest, TargetSHA: targetOp.Original.SHA256, TargetPhase: control.Phase(targetOp.Phase)}
	}
	rawRequest := operationEncode(map[string]any{"api_version": "v1", "request_id": id, "operation": name, "params": params})
	request, err := control.ParseRequest(rawRequest)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "apply" {
		intent.ImpactDigest = operationSHA(request.Parameters())
	}
	if kind == "stop" {
		e := evidence.(operationLifecycleEvidence)
		e.ExitOperationID = request.OperationID()
		evidence = e
	}
	record := control.Operation{Version: 1, Identity: control.Identity{OperationID: request.OperationID(), RequestID: id, RequestDigest: request.Digest(), APIVersion: "v1", Operation: name, Intent: intent}, Request: []byte(mustCanonical(rawRequest)), Phase: control.Committed}
	for n, phase := range []control.Phase{control.Prepared, control.Validated, control.Applying, control.Verifying, control.Publishing, control.Committed} {
		stepRaw := mustCanonical(operationEncode(evidence))
		if n == 0 && kind != "service-stop" {
			stepRaw = []byte(`{}`)
		}
		result := map[string]any{"api_version": "v1", "request_id": id, "ok": true, "data": map[string]any{"operation_id": request.OperationID(), "phase": phase, "base_revision": base.Revision, "target_revision": intent.TargetRevision, "outcome": string(phase)}, "error": nil, "warnings": []any{}}
		resultRaw := mustCanonical(operationEncode(result))
		record.Steps = append(record.Steps, control.Step{Sequence: n + 1, Phase: phase, Evidence: stepRaw, Result: resultRaw})
		record.Result = resultRaw
	}
	record.BodySHA256 = operationSHA(operationEncode(struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(record.Request), record.Steps}))
	raw := operationEncode(record)
	ref := originalRef{Path: "synthetic-record-" + id, Bytes: int64(len(raw)), SHA256: operationSHA(raw)}
	op, err := projectOperationRecord(control.StoredOperation{Record: record, SHA256: ref.SHA256}, raw, ref, previous, target)
	if err != nil {
		t.Fatal("synthetic policy record rejected", kind, err)
	}
	return op
}

type policyTrace struct {
	e     productEvidence
	index uint64
}

func newPolicyTrace() *policyTrace {
	return &policyTrace{e: productEvidence{Operations: map[string]operationRecord{}, DurableFiles: map[string]fileSnapshot{}}}
}
func (p *policyTrace) add(op operationRecord) {
	p.e.Operations[op.ID] = op
	p.e.DurableFiles["/synthetic/state/operations/"+op.ID+".json"] = fileSnapshot{Kind: "regular", Bytes: op.Original.Bytes, SHA256: op.Original.SHA256, Mode: 0600}
}
func (p *policyTrace) event(name string, op operationRecord, instance *processIdentity) int {
	p.index++
	raw := operationEncode(map[string]any{"synthetic_policy_event": p.index, "name": name})
	event := boundaryEvent{Index: p.index, Name: name, OperationID: op.ID, Originals: []originalRef{{Path: "synthetic-event", Bytes: int64(len(raw)), SHA256: operationSHA(raw)}}}
	if instance != nil {
		copy := *instance
		event.Instance = &copy
		event.NonceSHA256 = copy.LaunchNonceSHA256
	}
	p.e.Boundaries = append(p.e.Boundaries, event)
	return len(p.e.Boundaries) - 1
}
func (p *policyTrace) core(op operationRecord, instance processIdentity) {
	p.add(op)
	p.event("operation-durable", op, nil)
	p.event("spawn-intent-durable", op, &instance)
	n := p.event("core-spawn", op, &instance)
	p.e.Launches = append(p.e.Launches, launchInvocation{BoundaryIndex: p.e.Boundaries[n].Index, ID: string(instance.RunID), NonceSHA256: instance.LaunchNonceSHA256, Instance: instance})
	for _, name := range []string{"registration-durable", "preopen-verified", "entry-opening", "entry-open", "target-verified", "publishing-durable", "active-published", "committed-durable"} {
		p.event(name, op, &instance)
	}
}
func (p *policyTrace) stop(op operationRecord, instance processIdentity) {
	p.add(op)
	p.event("operation-durable", op, nil)
	p.event("stop-intent-durable", op, nil)
	p.event("registration-durable", op, &instance)
	n := p.event("core-stop", op, &instance)
	p.e.Signals = append(p.e.Signals, signalInvocation{BoundaryIndex: p.e.Boundaries[n].Index, Signal: "TERM", Target: instance})
	for _, name := range []string{"core-stopped", "target-verified", "publishing-durable", "active-published", "committed-durable"} {
		p.event(name, op, &instance)
	}
}
func policyEvidence(t *testing.T, kind string) (productEvidence, operationRecord) {
	t.Helper()
	trace := newPolicyTrace()
	bundle := policyBundle()
	baseProcess := policyIdentity("base", 101)
	base := policyRecord(t, "apply", "base", config.ActivePointer{Version: 1, Revision: "empty", DesiredRuntime: "stopped"}, bundleSnapshot{}, bundle, baseProcess, nil)
	trace.core(base, baseProcess)
	if kind == "apply" {
		return trace.e, base
	}
	active := policyBase(base, "running")
	process := baseProcess
	if kind == "fresh-start" {
		stop := policyRecord(t, "stop", "stop-before-start", active, bundle, bundle, baseProcess, nil)
		trace.stop(stop, baseProcess)
		active = policyBase(stop, "stopped")
	}
	if kind == "fresh-start" || kind == "restart" {
		process = policyIdentity(kind, 202)
	}
	op := policyRecord(t, kind, "operation-"+kind, active, bundle, bundle, process, &base)
	switch kind {
	case "fresh-start", "restart":
		trace.core(op, process)
	case "stop":
		trace.stop(op, process)
	case "adopt-running":
		trace.add(op)
		trace.event("operation-durable", op, nil)
		n := trace.event("ownership-revalidated", op, &process)
		trace.e.Boundaries[n].OriginOperationID = base.ID
		trace.event("journal-publishing-durable", op, nil)
		trace.event("committed-durable", op, nil)
	case "recover":
		trace.add(op)
		n := trace.event("ownership-preflight", operationRecord{}, &process)
		trace.e.Boundaries[n].RequestID = op.RequestID
		trace.e.Boundaries[n].TargetOperationID = base.ID
		trace.e.Boundaries[n].OriginOperationID = base.ID
		trace.event("operation-durable", op, nil)
		n = trace.event("recovery-target-verified", op, &process)
		trace.e.Boundaries[n].TargetOperationID = base.ID
		trace.e.Boundaries[n].OriginOperationID = base.ID
		trace.event("journal-publishing-durable", op, nil)
		trace.event("committed-durable", op, nil)
	case "service-stop":
		trace.add(op)
		trace.event("operation-durable", op, nil)
		trace.event("service-stop-intent-durable", op, nil)
		n := trace.event("service-admission-closed", op, nil)
		trace.e.Boundaries[n].OriginOperationID = strings.Repeat("e", 48)
		trace.event("journal-publishing-durable", op, nil)
		trace.event("committed-durable", op, nil)
	}
	return trace.e, op
}
func clonePolicyEvidence(e productEvidence) productEvidence {
	out := e
	out.Operations = map[string]operationRecord{}
	for id, op := range e.Operations {
		out.Operations[id] = op
	}
	out.DurableFiles = map[string]fileSnapshot{}
	for path, f := range e.DurableFiles {
		out.DurableFiles[path] = f
	}
	out.Boundaries = append([]boundaryEvent(nil), e.Boundaries...)
	out.Launches = append([]launchInvocation(nil), e.Launches...)
	out.Signals = append([]signalInvocation(nil), e.Signals...)
	return out
}
func TestP1OperationPolicySevenClasses(t *testing.T) {
	for _, kind := range []string{"apply", "fresh-start", "restart", "adopt-running", "stop", "recover", "service-stop"} {
		t.Run(kind, func(t *testing.T) {
			e, op := policyEvidence(t, kind)
			if err := validateOperationOrdering(e, policyCoreSHA); err != nil {
				t.Fatal("synthetic policy positive", kind, err)
			}
			if op.Subtype != kind {
				t.Fatal("source subtype not projected")
			}
		})
	}
}
func TestP1OperationPolicyRejectsDTOAndEffectForgery(t *testing.T) {
	for _, test := range []struct{ kind, change string }{{"apply", "null-target"}, {"fresh-start", "null-target"}, {"stop", "null-target"}, {"restart", "fake-revision"}, {"adopt-running", "fake-revision"}, {"service-stop", "fake-revision"}, {"recover", "fake-revision"}, {"restart", "drop-publication"}, {"restart", "drop-spawn"}, {"fresh-start", "drop-spawn"}, {"apply", "source-unknown-field"}, {"apply", "source-revision-forgery"}, {"adopt-running", "fake-active"}, {"service-stop", "fake-active"}, {"recover", "fake-active"}, {"adopt-running", "fake-registration"}, {"recover", "fake-registration"}, {"adopt-running", "spawn"}, {"adopt-running", "signal"}, {"service-stop", "signal"}, {"recover", "drop-target"}, {"recover", "drop-call"}, {"service-stop", "drop-history"}, {"apply", "unknown-subtype"}, {"restart", "wrong-subtype"}, {"apply", "missing-original"}, {"apply", "foreign-signal"}} {
		t.Run(test.kind+"/"+test.change, func(t *testing.T) {
			e, op := policyEvidence(t, test.kind)
			e = clonePolicyEvidence(e)
			switch test.change {
			case "null-target":
				op.TargetRevision = nil
				e.Operations[op.ID] = op
			case "fake-revision":
				rev := config.Revision("invented-revision")
				op.TargetRevision = &rev
				e.Operations[op.ID] = op
			case "unknown-subtype":
				op.Subtype = "unknown"
				e.Operations[op.ID] = op
			case "wrong-subtype":
				op.Subtype = "adopt-running"
				e.Operations[op.ID] = op
			case "missing-original":
				op.Original.SHA256 = strings.Repeat("f", 64)
				e.Operations[op.ID] = op
			case "drop-target":
				var source control.Operation
				json.Unmarshal(op.recordSource, &source)
				m, _ := operationClassify(control.StoredOperation{Record: source, SHA256: op.Original.SHA256})
				delete(e.Operations, m.targetID)
			case "drop-call", "drop-history":
				delete(e.Operations, op.ID)
			case "drop-spawn":
				for i := len(e.Boundaries) - 1; i >= 0; i-- {
					v := e.Boundaries[i]
					if v.OperationID == op.ID && v.Name == "core-spawn" {
						e.Boundaries = append(e.Boundaries[:i], e.Boundaries[i+1:]...)
						for j, launch := range e.Launches {
							if launch.BoundaryIndex == v.Index {
								e.Launches = append(e.Launches[:j], e.Launches[j+1:]...)
								break
							}
						}
					}
				}
			case "source-unknown-field":
				op.recordSource = append(bytes.Clone(op.recordSource[:len(op.recordSource)-2]), []byte(`,"untrusted":true}`)...)
				op.recordSource = append(op.recordSource, '\n')
				op.Original.Bytes = int64(len(op.recordSource))
				op.Original.SHA256 = operationSHA(op.recordSource)
				e.Operations[op.ID] = op
			case "source-revision-forgery":
				var source control.Operation
				json.Unmarshal(op.recordSource, &source)
				source.Identity.Intent.TargetRevision = nil
				op.recordSource = operationEncode(source)
				op.Original.Bytes = int64(len(op.recordSource))
				op.Original.SHA256 = operationSHA(op.recordSource)
				op.TargetRevision = nil
				e.Operations[op.ID] = op
			case "drop-publication":
				for i, v := range e.Boundaries {
					if v.OperationID == op.ID && v.Name == "active-published" {
						e.Boundaries = append(e.Boundaries[:i], e.Boundaries[i+1:]...)
						break
					}
				}
			case "fake-active", "fake-registration", "spawn", "signal":
				event := e.Boundaries[len(e.Boundaries)-1]
				event.Index++
				event.OperationID = op.ID
				event.Name = map[string]string{"fake-active": "active-published", "fake-registration": "registration-durable", "spawn": "core-spawn", "signal": "core-stop"}[test.change]
				instance := policyIdentity("base", 101)
				event.Instance = &instance
				event.NonceSHA256 = instance.LaunchNonceSHA256
				e.Boundaries = append(e.Boundaries, event)
				if test.change == "spawn" {
					e.Launches = append(e.Launches, launchInvocation{BoundaryIndex: event.Index, ID: "fake", NonceSHA256: instance.LaunchNonceSHA256, Instance: instance})
				}
				if test.change == "signal" {
					e.Signals = append(e.Signals, signalInvocation{BoundaryIndex: event.Index, Signal: "TERM", Target: instance})
				}
			case "foreign-signal":
				event := e.Boundaries[len(e.Boundaries)-1]
				event.Index++
				event.Name = "core-stop"
				instance := policyIdentity("base", 101)
				event.Instance = &instance
				e.Boundaries = append(e.Boundaries, event)
				e.Signals = append(e.Signals, signalInvocation{BoundaryIndex: event.Index, Signal: "TERM", Target: instance})
				e.ForeignProcesses = []processIdentity{instance}
			}
			if err := validateOperationOrdering(e, policyCoreSHA); err == nil {
				t.Fatal("forged policy accepted")
			}
		})
	}
}
func policyResult(t *testing.T, e productEvidence, op operationRecord) operationResult {
	t.Helper()
	responseOp := op
	r := operationResult{Outcome: "committed", Operation: op}
	if op.Subtype == "recover" {
		var source control.Operation
		json.Unmarshal(op.recordSource, &source)
		m, _ := operationClassify(control.StoredOperation{Record: source, SHA256: op.Original.SHA256})
		r.Operation = e.Operations[m.targetID]
		copy := op
		r.RecoveryCall = &copy
	}
	var source control.Operation
	json.Unmarshal(responseOp.recordSource, &source)
	r.Response = append([]byte(source.Result), '\n')
	r.ResponseOriginal = originalRef{Path: "synthetic-wire-response", Bytes: int64(len(r.Response)), SHA256: operationSHA(r.Response)}
	r.ResponseOperationID = responseOp.ID
	r.RequestID = responseOp.RequestID
	r.Originals = []originalRef{r.ResponseOriginal}
	var wire struct {
		Data struct {
			Outcome string `json:"outcome"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	must(t, json.Unmarshal(r.Response, &wire))
	r.WireOutcome, r.ErrorCode = wire.Data.Outcome, wire.Error.Code
	r.Outcome = logicalOperationOutcome(r)
	return r
}
func TestP1OperationPolicyResponseCallTargetBinding(t *testing.T) {
	for _, kind := range []string{"apply", "recover"} {
		e, op := policyEvidence(t, kind)
		r := policyResult(t, e, op)
		if err := validateOperationResult(r); err != nil {
			t.Fatal("valid synthetic wire relation", err)
		}
		for _, change := range []string{"response-id", "response-bytes", "missing-call", "target-digest"} {
			changed := r
			switch change {
			case "response-id":
				changed.ResponseOperationID = "invented"
			case "response-bytes":
				changed.Response = bytes.ReplaceAll(r.Response, []byte(`"outcome":"committed"`), []byte(`"outcome":"invented"`))
				changed.ResponseOriginal.Bytes = int64(len(changed.Response))
				changed.ResponseOriginal.SHA256 = operationSHA(changed.Response)
			case "missing-call":
				if kind != "recover" {
					continue
				}
				changed.RecoveryCall = nil
			case "target-digest":
				if kind != "recover" {
					continue
				}
				changed.Operation.RequestDigest = strings.Repeat("f", 64)
			}
			if validateOperationResult(changed) == nil {
				t.Fatal("response/call/target forgery accepted", kind, change)
			}
		}
	}
}
func TestP1OperationProjectionActualJournalAndPreparedPrefix(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	paths, err := config.ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err := control.NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	request, err := control.ParseRequest(operationEncode(map[string]any{"api_version": "v1", "request_id": "actual-codec-driver", "operation": "runtime.start", "params": map[string]any{"expected_revision": "base", "control_only": false}}))
	if err != nil {
		t.Fatal(err)
	}
	stored, made, err := journal.Admit(request, control.Intent{BaseRevision: "base", CandidateDigest: policyCoreSHA, ImpactDigest: policyCoreSHA}, json.RawMessage(`{"driver_only":true}`))
	if err != nil || !made {
		t.Fatal(err)
	}
	raw, sha, err := store.ReadOperationFile(request.OperationID())
	if err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(t.TempDir(), "original.json")
	if err = os.WriteFile(refPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ref := originalRef{Path: refPath, Bytes: int64(len(raw)), SHA256: sha}
	op, err := projectOperationRecord(stored, raw, ref, bundleSnapshot{}, bundleSnapshot{})
	if err != nil || op.Subtype != "unknown" {
		t.Fatal("actual Store codec/prepared projection", err)
	}
	trace := newPolicyTrace()
	trace.add(op)
	trace.event("operation-durable", op, nil)
	if err := validateOperationOrdering(trace.e, policyCoreSHA); err != nil {
		t.Fatal("actual prepared prefix", err)
	}
	bad := clonePolicyEvidence(trace.e)
	bad.Boundaries = append(bad.Boundaries, boundaryEvent{Index: 2, Name: "target-verified", OperationID: op.ID, Originals: trace.e.Boundaries[0].Originals})
	if validateOperationOrdering(bad, policyCoreSHA) == nil {
		t.Fatal("unclassified prefix authorized effect")
	}
	corrupt := bytes.Clone(raw)
	corrupt[0] ^= 1
	if _, err := projectOperationRecord(stored, corrupt, ref, bundleSnapshot{}, bundleSnapshot{}); err == nil {
		t.Fatal("changed raw original accepted")
	}
	if !reflect.DeepEqual(raw, op.recordSource) {
		t.Fatal("source not preserved")
	}
}

// An admitted failure carries its real operation ID in error.details; it still
// must match the exact retained receipt, not merely an allowed business code.
func TestP1OperationPolicyAdmittedFailureResponse(t *testing.T) {
	e, op := policyEvidence(t, "fresh-start")
	var source control.Operation
	if err := json.Unmarshal(op.recordSource, &source); err != nil {
		t.Fatal(err)
	}
	response := mustCanonical(operationEncode(map[string]any{"api_version": "v1", "request_id": op.RequestID, "ok": false, "data": nil, "error": map[string]any{"code": "RECOVERY_REQUIRED", "message": "Recovery required", "retryable": false, "details": map[string]any{"operation_id": op.ID, "reason": "degraded"}}, "warnings": []any{}}))
	source.Phase = control.Degraded
	source.Result = response
	last := &source.Steps[len(source.Steps)-1]
	last.Phase, last.Result = control.Degraded, response
	source.BodySHA256 = operationSHA(operationEncode(struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(source.Request), source.Steps}))
	raw := operationEncode(source)
	ref := originalRef{Path: op.Original.Path, Bytes: int64(len(raw)), SHA256: operationSHA(raw)}
	var err error
	op, err = projectOperationRecord(control.StoredOperation{Record: source, SHA256: ref.SHA256}, raw, ref, op.Previous, op.Target)
	if err != nil {
		t.Fatal(err)
	}
	r := policyResult(t, e, op)
	r.Outcome = "degraded"
	if err = validateOperationResult(r); err != nil {
		t.Fatal("valid admitted error receipt rejected", err)
	}
	r.ResponseOperationID = "invented"
	if validateOperationResult(r) == nil {
		t.Fatal("admitted error accepted wrong response identity")
	}
}

// This edits synthetic policy sources only; a real adapter must capture each
// historical file independently and must never recreate it using this helper.
func policyRewrite(t *testing.T, op operationRecord, edit func(*control.Operation)) operationRecord {
	t.Helper()
	var source control.Operation
	if err := json.Unmarshal(op.recordSource, &source); err != nil {
		t.Fatal(err)
	}
	edit(&source)
	source.BodySHA256 = operationSHA(operationEncode(struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(source.Request), source.Steps}))
	raw := operationEncode(source)
	ref := originalRef{Path: op.Original.Path, Bytes: int64(len(raw)), SHA256: operationSHA(raw)}
	projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: ref.SHA256}, raw, ref, op.Previous, op.Target)
	if err != nil {
		t.Fatal(err)
	}
	return projected
}
func TestP1OperationPolicyRecoveryHistoricalTarget(t *testing.T) {
	e, call := policyEvidence(t, "recover")
	var source control.Operation
	json.Unmarshal(call.recordSource, &source)
	m, _ := operationClassify(control.StoredOperation{Record: source, SHA256: call.Original.SHA256})
	current := e.Operations[m.targetID]
	old := policyRewrite(t, current, func(op *control.Operation) {
		op.Steps = op.Steps[:len(op.Steps)-1]
		op.Phase, op.Result = op.Steps[len(op.Steps)-1].Phase, op.Steps[len(op.Steps)-1].Result
	})
	historical := operationVersion{Original: old.Original, Raw: append([]byte(nil), old.recordSource...), Previous: old.Previous, Target: old.Target}
	// Archive path intentionally differs from authoritative inventory path.
	historical.Original.Path = "synthetic-independent-archive"
	rewriteCall := func(sha string, phase control.Phase, committed bool) operationRecord {
		return policyRewrite(t, call, func(op *control.Operation) {
			evidence := operationRecoveryEvidence{Version: 1, TargetID: config.ID(current.ID), TargetDigest: current.RequestDigest, TargetSHA: sha, TargetPhase: phase}
			last := &op.Steps[len(op.Steps)-1]
			last.Evidence = mustCanonical(operationEncode(evidence))
			if !committed {
				op.Phase, last.Phase = control.Degraded, control.Degraded
				last.Result = mustCanonical(operationEncode(map[string]any{"api_version": "v1", "request_id": op.Identity.RequestID, "ok": false, "data": nil, "error": map[string]any{"code": "RECOVERY_REQUIRED", "message": "Recovery required", "retryable": false, "details": map[string]any{"operation_id": op.Identity.OperationID, "reason": "degraded"}}, "warnings": []any{}}))
				op.Result = last.Result
			}
		})
	}
	check := func(candidate operationRecord, history []operationVersion, want bool) {
		t.Helper()
		trace, _ := policyEvidence(t, "recover")
		trace.Operations[candidate.ID] = candidate
		trace.DurableFiles["/synthetic/state/operations/"+candidate.ID+".json"] = fileSnapshot{Kind: "regular", Mode: 0600, Bytes: candidate.Original.Bytes, SHA256: candidate.Original.SHA256}
		trace.OperationHistory = history
		if candidate.Phase != "committed" {
			trace.Boundaries[len(trace.Boundaries)-1].Name = "degraded-durable"
		}
		if got := validateOperationOrdering(trace, policyCoreSHA); (got == nil) != want {
			t.Fatalf("ordering accepted=%v want=%v", got == nil, want)
		}
		result := policyResult(t, trace, candidate)
		result.TargetHistory = history
		if candidate.Phase == "degraded" && result.Outcome != "degraded" {
			t.Fatal("failed recovery call was hidden by the old terminal target")
		}
		if got := validateOperationResult(result); (got == nil) != want {
			t.Fatalf("response accepted=%v want=%v", got == nil, want)
		}
		if want && candidate.Phase == "degraded" {
			result.Outcome = "committed"
			if validateOperationResult(result) == nil {
				t.Fatal("failed recovery call falsely reported target success")
			}
		}
	}
	pending := rewriteCall(old.Original.SHA256, control.Phase(old.Phase), false)
	t.Run("target-advanced-call-not-updated", func(t *testing.T) { check(pending, []operationVersion{historical}, true) })
	t.Run("current-target", func(t *testing.T) {
		check(rewriteCall(current.Original.SHA256, control.Phase(current.Phase), false), nil, true)
	})
	t.Run("missing-history", func(t *testing.T) { check(pending, nil, false) })
	t.Run("forged-sha", func(t *testing.T) {
		check(rewriteCall(strings.Repeat("f", 64), control.Phase(old.Phase), false), []operationVersion{historical}, false)
	})
	t.Run("forged-phase", func(t *testing.T) {
		check(rewriteCall(old.Original.SHA256, control.Applying, false), []operationVersion{historical}, false)
	})
	t.Run("committed-requires-current-terminal", func(t *testing.T) {
		check(rewriteCall(old.Original.SHA256, control.Phase(old.Phase), true), []operationVersion{historical}, false)
	})
	t.Run("corrupt-history", func(t *testing.T) {
		bad := historical
		bad.Raw = append(append([]byte(nil), bad.Raw...), ' ')
		check(pending, []operationVersion{bad}, false)
	})
	t.Run("different-immutable-intent", func(t *testing.T) {
		other := policyRewrite(t, old, func(op *control.Operation) {
			revision := config.Revision("other-valid-revision")
			op.Identity.Intent.TargetRevision = &revision
		})
		bad := operationVersion{Original: other.Original, Raw: other.recordSource, Previous: other.Previous, Target: other.Target}
		check(rewriteCall(other.Original.SHA256, control.Phase(other.Phase), false), []operationVersion{bad}, false)
	})
}
