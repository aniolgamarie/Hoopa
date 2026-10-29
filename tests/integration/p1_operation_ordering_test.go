//go:build p1integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hoopa/internal/control"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type operationOrderState struct {
	durable, preopen, verified, publishing, active, committed, oldVerified, oldActive, rolledBack, degraded, proof, serviceIntent, closed, spawned bool
	registered                                                                                                                                     *processIdentity
}

func operationStrong(p processIdentity, core string) bool {
	return p.PID > 1 && p.BootID != "" && p.OSStartIdentity != "" && p.BinaryPath != "" && p.BinarySHA256 == core && operationDigest(p.LaunchNonceSHA256) && p.PrivateWorkdir != "" && p.Generation != "" && p.RunID != ""
}
func operationRef(ref originalRef) bool {
	return ref.Path != "" && ref.Bytes > 0 && operationDigest(ref.SHA256)
}
func operationCore(kind string) bool {
	return kind == "apply" || kind == "fresh-start" || kind == "restart" || kind == "stop"
}

// History checks exact independently retained sources, including immutable admission
// identity/request. It does not reconstruct intermediate files from journal steps.
func operationHistory(current map[string]operationRecord, versions []operationVersion) (map[string][]operationRecord, error) {
	out := map[string][]operationRecord{}
	for _, version := range versions {
		var source control.Operation
		if json.Unmarshal(version.Raw, &source) != nil {
			return nil, operationPolicyError()
		}
		op, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: version.Original.SHA256}, version.Raw, version.Original, version.Previous, version.Target)
		latest, ok := current[op.ID]
		var latestSource control.Operation
		if err != nil || !ok || json.Unmarshal(latest.recordSource, &latestSource) != nil || !reflect.DeepEqual(source.Identity, latestSource.Identity) || !bytes.Equal(source.Request, latestSource.Request) {
			return nil, operationPolicyError()
		}
		out[op.ID] = append(out[op.ID], op)
	}
	return out, nil
}
func operationTargetBound(m operationMeaning, target operationRecord, history []operationRecord) bool {
	match := func(op operationRecord) bool {
		return op.ID == m.targetID && op.RequestDigest == m.targetDigest && op.Original.SHA256 == m.targetSHA && op.Phase == string(m.targetPhase)
	}
	if m.stored.Record.Phase == control.Committed {
		return (target.Phase == "committed" || target.Phase == "rolled_back") && match(target)
	}
	if match(target) {
		return true
	}
	for _, op := range history {
		if match(op) {
			return true
		}
	}
	return false
}

// Pure policy only. assertOrdering separately reads/verifies original files.
// Nothing here converts retained journal steps into historical runtime events.
func validateOperationOrdering(e productEvidence, coreSHA string) error {
	fail := operationPolicyError
	if !operationDigest(coreSHA) || len(e.Operations) == 0 || len(e.Boundaries) == 0 {
		return fail()
	}
	meanings := map[string]operationMeaning{}
	states := map[string]*operationOrderState{}
	byRequest := map[string]string{}
	for id, op := range e.Operations {
		var source control.Operation
		if json.Unmarshal(op.recordSource, &source) != nil {
			return fail()
		}
		stored := control.StoredOperation{Record: source, SHA256: op.Original.SHA256}
		var marker map[string]json.RawMessage
		if json.Unmarshal(source.Result, &marker) == nil {
			if _, marked := marker["capacity_metadata_seed"]; marked {
				if _, fixture := e.FixtureSeeds[id]; !fixture {
					return fail()
				}
			}
		}
		projected, err := projectOperationRecord(stored, op.recordSource, op.Original, op.Previous, op.Target)
		if err != nil || !reflect.DeepEqual(projected, op) || id != op.ID {
			return fail()
		}
		meaning, err := operationClassify(stored)
		if err != nil {
			return fail()
		}
		if previous, ok := byRequest[op.RequestID]; ok && previous != id {
			return fail()
		}
		meanings[id] = meaning
		states[id] = &operationOrderState{}
		byRequest[op.RequestID] = id
	}
	for id, seed := range e.FixtureSeeds {
		op, ok := e.Operations[id]
		var source control.Operation
		if !ok || seed.OperationID != id || seed.RequestID != op.RequestID || seed.RequestDigest != op.RequestDigest || seed.RecordOriginal.SHA256 != op.Original.SHA256 || seed.RecordOriginal.Bytes != op.Original.Bytes || !operationRef(seed.RecordOriginal) || meanings[id].kind != "unknown" || op.Phase != "prepared" || json.Unmarshal(op.recordSource, &source) != nil || string(source.Result) != `{"capacity_metadata_seed":true}` {
			return fail()
		}
		for _, refs := range [][]originalRef{seed.BeginOriginals, seed.PublishOriginals, seed.EndOriginals} {
			if len(refs) == 0 {
				return fail()
			}
			for _, ref := range refs {
				if !operationRef(ref) {
					return fail()
				}
			}
		}
	}
	authorities := map[string]bool{}
	for path, file := range e.DurableFiles {
		if filepath.Base(filepath.Dir(path)) != "operations" || !strings.HasPrefix(filepath.Base(path), "op-") || !strings.HasSuffix(path, ".json") {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		op, ok := e.Operations[id]
		if !ok || file.SHA256 != op.Original.SHA256 || file.Bytes != op.Original.Bytes {
			return fail()
		}
		authorities[id] = true
	}
	for id := range e.Operations {
		if !authorities[id] {
			return fail()
		}
	}
	history, historyErr := operationHistory(e.Operations, e.OperationHistory)
	if historyErr != nil {
		return fail()
	}
	for _, m := range meanings {
		if m.kind == "recover" {
			target, ok := e.Operations[m.targetID]
			if !ok || target.Operation == "operation.recover" || target.RequestDigest != m.targetDigest {
				return fail()
			}
			if !operationTargetBound(m, target, history[target.ID]) {
				return fail()
			}
		}
	}
	signals := map[uint64]signalInvocation{}
	for _, sig := range e.Signals {
		if _, ok := signals[sig.BoundaryIndex]; ok {
			return fail()
		}
		signals[sig.BoundaryIndex] = sig
	}
	launches := map[uint64]launchInvocation{}
	for _, launch := range e.Launches {
		if _, ok := launches[launch.BoundaryIndex]; ok {
			return fail()
		}
		launches[launch.BoundaryIndex] = launch
	}
	events := map[uint64]boundaryEvent{}
	births := map[string]boundaryEvent{}
	intents := map[string]string{}
	last := uint64(0)
	proof := func(v boundaryEvent, m operationMeaning) bool {
		if v.Instance == nil || !operationStrong(*v.Instance, coreSHA) || v.Instance.RunID != m.runID || v.NonceSHA256 != v.Instance.LaunchNonceSHA256 || v.OriginOperationID != m.originID {
			return false
		}
		born, ok := births[v.NonceSHA256]
		if !ok || born.OperationID != m.originID || born.Instance == nil || *born.Instance != *v.Instance {
			return false
		}
		if m.identity != nil {
			p := m.identity
			if p.PID != v.Instance.PID || p.BootID != v.Instance.BootID || fmt.Sprint(p.StartTicks) != v.Instance.OSStartIdentity || p.BinaryPath != v.Instance.BinaryPath || p.BinarySHA256 != v.Instance.BinarySHA256 || p.Workdir != v.Instance.PrivateWorkdir || p.NonceSHA256 != v.Instance.LaunchNonceSHA256 {
				return false
			}
		}
		return true
	}
	for _, v := range e.Boundaries {
		if v.Index <= last || len(v.Originals) == 0 {
			return fail()
		}
		last = v.Index
		for _, ref := range v.Originals {
			if !operationRef(ref) {
				return fail()
			}
		}
		events[v.Index] = v
		if v.Name == "ownership-preflight" {
			target, ok := meanings[v.TargetOperationID]
			if v.OperationID != "" || v.RequestID == "" || !ok || target.kind == "unknown" || target.kind == "stop" || target.kind == "service-stop" || target.kind == "recover" || !proof(v, target) {
				return fail()
			}
			if callID, ok := byRequest[v.RequestID]; ok {
				call := meanings[callID]
				bound := call.kind == "recover" && call.targetID == v.TargetOperationID
				// Ordinary Start verifies the existing activation before admitting
				// an adoption. Its later record must bind the exact same source run
				// and identity; the preflight still carries no admitted operation ID.
				if call.kind == "adopt-running" {
					bound = call.originID == v.TargetOperationID && proof(v, call)
				}
				if !bound || states[callID].durable {
					return fail()
				}
			}
			continue
		}
		pre := v.Name == "inputs-revalidated" || v.Name == "candidate-validated" || v.Name == "candidate-published"
		op, ok := e.Operations[v.OperationID]
		if pre && v.OperationID == "" {
			continue
		}
		if !ok {
			return fail()
		}
		m, s := meanings[v.OperationID], states[v.OperationID]
		if seed, fixture := e.FixtureSeeds[v.OperationID]; fixture {
			refs := append(append(append([]originalRef{}, seed.BeginOriginals...), seed.PublishOriginals...), seed.EndOriginals...)
			if v.Name != "fixture-seed-durable" || s.durable || v.RequestID != "" || v.RequestDigest != "" || v.Instance != nil || v.NonceSHA256 != "" || v.TargetOperationID != "" || v.OriginOperationID != "" || !reflect.DeepEqual(refs, v.Originals) {
				return fail()
			}
			s.durable = true
			continue
		}
		if pre {
			if m.kind != "apply" {
				return fail()
			}
			continue
		}
		if v.Name != "operation-durable" && !s.durable {
			return fail()
		}
		if v.RequestID != "" && v.RequestID != op.RequestID {
			callID, ok := byRequest[v.RequestID]
			call := meanings[callID]
			if !ok || call.kind != "recover" || call.targetID != op.ID || !states[callID].durable || v.Name == "core-spawn" || v.Name == "spawn-intent-durable" {
				return fail()
			}
		}
		if v.Name == "operation-durable" {
			s.durable = true
			continue
		}
		if m.kind == "unknown" {
			return fail()
		}
		if !operationCore(m.kind) {
			switch v.Name {
			case "ownership-revalidated":
				if m.kind != "adopt-running" || !proof(v, m) {
					return fail()
				}
				s.proof = true
			case "recovery-target-verified":
				target, ok := meanings[m.targetID]
				if m.kind != "recover" || !ok || v.TargetOperationID != m.targetID {
					return fail()
				}
				if target.kind == "stop" || target.kind == "service-stop" {
					if target.stored.Record.Phase != control.Committed {
						return fail()
					}
				} else if !proof(v, target) {
					return fail()
				}
				// The same actual recovery activation proof has two roles: it
				// proves the admitted call's target and re-verifies that original
				// core operation before it may publish. A crash after entry-open
				// can precede the original open-verified hook. Do not invent that
				// lost event or treat entry-open itself as verification.
				if operationCore(target.kind) && target.kind != "stop" {
					targetState := states[m.targetID]
					if targetState == nil || !targetState.durable || targetState.registered == nil || v.Instance == nil || *targetState.registered != *v.Instance {
						return fail()
					}
					targetOp := e.Operations[m.targetID]
					if target.kind == "apply" && v.Instance.RunID == target.rollbackRunID {
						if v.Instance.Generation != targetOp.Previous.Generation {
							return fail()
						}
						targetState.oldVerified = true
					} else {
						expectedRun := target.runID
						if target.kind == "apply" {
							expectedRun = target.targetRunID
						}
						if v.Instance.RunID != expectedRun || v.Instance.Generation != targetOp.Target.Generation {
							return fail()
						}
						targetState.verified = true
					}
				}
				s.proof = true
			case "service-stop-intent-durable":
				if m.kind != "service-stop" {
					return fail()
				}
				s.serviceIntent = true
			case "service-admission-closed":
				if m.kind != "service-stop" || !s.serviceIntent || !m.admissionClosed || v.OriginOperationID != m.serviceInstance {
					return fail()
				}
				s.closed = true
			case "journal-publishing-durable":
				if m.kind == "service-stop" && !s.closed || m.kind != "service-stop" && !s.proof {
					return fail()
				}
				s.publishing = true
			case "committed-durable":
				if !s.publishing || m.kind == "service-stop" && !s.closed || m.kind != "service-stop" && !s.proof {
					return fail()
				}
				s.committed = true
			case "degraded-durable":
				s.degraded = true
			default:
				return fail()
			}
			continue
		}
		registration := func(required bool) bool {
			if s.registered == nil || required && v.Instance == nil {
				return false
			}
			return (v.Instance == nil || *v.Instance == *s.registered) && (v.NonceSHA256 == "" || v.NonceSHA256 == s.registered.LaunchNonceSHA256)
		}
		switch v.Name {
		case "stop-intent-durable":
			if m.kind != "stop" || op.Desired != "stopped" {
				return fail()
			}
			s.serviceIntent = true
		case "spawn-intent-durable":
			if m.kind == "stop" || !operationDigest(v.NonceSHA256) {
				return fail()
			}
			if old, ok := intents[v.NonceSHA256]; ok && old != v.OperationID {
				return fail()
			}
			intents[v.NonceSHA256] = v.OperationID
		case "core-spawn":
			if m.runs != nil && (v.Instance == nil || !m.runs[v.Instance.RunID] || v.Instance.RunID == m.sourceRun) {
				return fail()
			}
			if m.kind == "stop" || v.Instance == nil || !operationStrong(*v.Instance, coreSHA) || intents[v.NonceSHA256] != v.OperationID || v.NonceSHA256 != v.Instance.LaunchNonceSHA256 {
				return fail()
			}
			if _, ok := births[v.NonceSHA256]; ok {
				return fail()
			}
			if launch, ok := launches[v.Index]; !ok || launch.Instance != *v.Instance || launch.NonceSHA256 != v.NonceSHA256 {
				return fail()
			}
			births[v.NonceSHA256] = v
			s.spawned = true
			s.registered = nil
			s.preopen = false
			s.verified = false
		case "registration-durable":
			if m.runs != nil && (v.Instance == nil || !m.runs[v.Instance.RunID]) {
				return fail()
			}
			if v.Instance == nil || !operationStrong(*v.Instance, coreSHA) {
				return fail()
			}
			born, ok := births[v.NonceSHA256]
			if !ok || born.Instance == nil || *born.Instance != *v.Instance || v.NonceSHA256 != v.Instance.LaunchNonceSHA256 {
				return fail()
			}
			if s.registered != nil && *s.registered != *v.Instance {
				s.preopen = false
				s.verified = false
			}
			p := *v.Instance
			s.registered = &p
		case "core-stop":
			if sig, ok := signals[v.Index]; !ok || v.Instance == nil || sig.Target != *v.Instance {
				return fail()
			}
			if m.runs != nil && (v.Instance == nil || !m.runs[v.Instance.RunID]) {
				return fail()
			}
			if v.Instance == nil || !operationStrong(*v.Instance, coreSHA) || m.kind == "stop" && !s.serviceIntent {
				return fail()
			}
		case "core-stopped":
			if v.Instance == nil || !operationStrong(*v.Instance, coreSHA) {
				return fail()
			}
		case "preopen-verified":
			if m.kind == "stop" || !registration(false) {
				return fail()
			}
			s.preopen = true
		case "entry-opening", "entry-open":
			if m.kind == "stop" || !registration(true) || !s.preopen {
				return fail()
			}
		case "target-verified":
			targetRun := m.runID
			if m.kind == "apply" {
				targetRun = m.targetRunID
			}
			if m.kind != "stop" && (!registration(false) || s.registered.Generation != op.Target.Generation || s.registered.RunID != targetRun) {
				return fail()
			}
			s.verified = true
		case "publishing-durable":
			if !s.verified {
				return fail()
			}
			s.publishing = true
		case "active-published":
			if !s.publishing {
				return fail()
			}
			s.active = true
		case "committed-durable":
			if !s.active || !s.publishing || !s.verified || m.kind != "stop" && (!s.spawned || !registration(false) || s.registered.Generation != op.Target.Generation || s.registered.RunID != m.runID) {
				return fail()
			}
			s.committed = true
		case "rolling-back-durable":
			if m.kind != "apply" {
				return fail()
			}
		case "old-state-verified":
			if m.kind != "apply" || !s.spawned || !registration(false) || s.registered.Generation != op.Previous.Generation || s.registered.RunID != m.rollbackRunID {
				return fail()
			}
			s.oldVerified = true
		case "old-active-published":
			if m.kind != "apply" || !s.oldVerified {
				return fail()
			}
			s.oldActive = true
		case "rolled-back-durable":
			if m.kind != "apply" || !s.oldVerified || !s.oldActive {
				return fail()
			}
			s.rolledBack = true
		case "degraded-durable":
			s.degraded = true
		default:
			return fail()
		}
	}
	for id, op := range e.Operations {
		s := states[id]
		m := meanings[id]
		if !s.durable {
			return fail()
		}
		switch op.Phase {
		case "committed":
			if !s.committed {
				return fail()
			}
		case "rolled_back":
			if m.kind != "apply" || !s.rolledBack {
				return fail()
			}
		case "degraded":
			if !s.degraded {
				return fail()
			}
		}
		switch m.kind {
		case "apply", "fresh-start", "stop":
			if op.TargetRevision == nil || *op.TargetRevision == op.BaseRevision {
				return fail()
			}
		case "restart", "adopt-running", "recover", "service-stop":
			if op.TargetRevision != nil {
				return fail()
			}
		case "unknown":
			if op.Phase != "prepared" {
				return fail()
			}
		default:
			return fail()
		}
	}
	seen := map[string]bool{}
	for _, launch := range e.Launches {
		event, ok := events[launch.BoundaryIndex]
		if !ok || event.Name != "core-spawn" || event.Instance == nil || launch.Instance != *event.Instance || launch.NonceSHA256 != event.NonceSHA256 || !operationStrong(launch.Instance, coreSHA) || launch.ID == "" || seen[launch.NonceSHA256] {
			return fail()
		}
		seen[launch.NonceSHA256] = true
	}
	for _, signal := range e.Signals {
		event, ok := events[signal.BoundaryIndex]
		if !ok || event.Name != "core-stop" || event.Instance == nil || *event.Instance != signal.Target || !operationStrong(signal.Target, coreSHA) {
			return fail()
		}
		for _, foreign := range e.ForeignProcesses {
			if signal.Target == foreign {
				return fail()
			}
		}
	}
	return nil
}
func logicalOperationOutcome(r operationResult) string {
	phase := r.Operation.Phase
	if r.RecoveryCall != nil {
		switch r.RecoveryCall.Phase {
		case "committed": // Only this call state can expose a terminal target.
		case "degraded":
			return "degraded"
		case "prepared", "validated", "applying", "verifying", "publishing":
			return "pending"
		default:
			return "" // recovery calls never roll back as an operation
		}
	}
	switch phase {
	case "committed", "rolled_back", "degraded":
		return phase
	case "prepared", "validated", "applying", "verifying", "publishing", "rolling_back":
		return "pending"
	}
	return ""
}

func validateOperationResult(r operationResult) error {
	var wire struct {
		RequestID string `json:"request_id"`
		OK        bool   `json:"ok"`
		Data      struct {
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
	if json.Unmarshal(r.Response, &wire) != nil || !operationRef(r.ResponseOriginal) || r.ResponseOriginal.Bytes != int64(len(r.Response)) || r.ResponseOriginal.SHA256 != operationSHA(r.Response) {
		return operationPolicyError()
	}
	if r.WireOutcome != wire.Data.Outcome || r.ErrorCode != wire.Error.Code || r.Outcome != logicalOperationOutcome(r) {
		return operationPolicyError()
	}
	if r.RequestID == "" || r.RequestID != wire.RequestID {
		return operationPolicyError()
	}
	body, _, _, wireErr := validateP1CLIResponse(r.Response, r.RequestID)
	if wireErr != nil {
		return operationPolicyError()
	}
	responseID := wire.Data.ID
	if responseID == "" {
		responseID = wire.Error.Details.ID
	} else if wire.Error.Details.ID != "" {
		return operationPolicyError()
	}
	if r.RecoveryCall == nil && len(r.TargetHistory) != 0 {
		return operationPolicyError()
	}
	response := r.Operation
	if r.RecoveryCall != nil {
		response = *r.RecoveryCall
		var source control.Operation
		if json.Unmarshal(response.recordSource, &source) != nil {
			return operationPolicyError()
		}
		m, err := operationClassify(control.StoredOperation{Record: source, SHA256: response.Original.SHA256})
		history, historyErr := operationHistory(map[string]operationRecord{r.Operation.ID: r.Operation}, r.TargetHistory)
		if err != nil || historyErr != nil || m.kind != "recover" || m.targetID != r.Operation.ID || m.targetDigest != r.Operation.RequestDigest || !operationTargetBound(m, r.Operation, history[r.Operation.ID]) {
			return operationPolicyError()
		}
	}
	if response.ID == "" {
		if wire.OK || r.ErrorCode == "" || r.Outcome != "" || r.WireOutcome != "" || r.RecoveryCall != nil || r.ResponseOperationID != "" || responseID != "" {
			return operationPolicyError()
		}
		return nil
	}
	if r.ResponseOperationID != response.ID || responseID != response.ID || wire.RequestID != response.RequestID {
		return operationPolicyError()
	}
	var source control.Operation
	if json.Unmarshal(response.recordSource, &source) != nil {
		return operationPolicyError()
	}
	projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: response.Original.SHA256}, response.recordSource, response.Original, response.Previous, response.Target)
	if err != nil || !reflect.DeepEqual(projected, response) || !bytes.Equal(body, []byte(source.Result)) {
		return operationPolicyError()
	}
	if r.RecoveryCall != nil {
		var target control.Operation
		if json.Unmarshal(r.Operation.recordSource, &target) != nil {
			return operationPolicyError()
		}
		again, err := projectOperationRecord(control.StoredOperation{Record: target, SHA256: r.Operation.Original.SHA256}, r.Operation.recordSource, r.Operation.Original, r.Operation.Previous, r.Operation.Target)
		if err != nil || !reflect.DeepEqual(again, r.Operation) {
			return operationPolicyError()
		}
	}
	return nil
}
func assertRecoveryResult(t *testing.T, r operationResult, e productEvidence) {
	t.Helper()
	if r.RecoveryCall == nil || r.RecoveryCall.Operation != "operation.recover" {
		t.Fatal("recovery lacks separate response call")
	}
	if err := validateOperationResult(r); err != nil {
		t.Fatal(err)
	}
	assertDurableResult(t, e, r.Operation)
	assertDurableResult(t, e, *r.RecoveryCall)
}
