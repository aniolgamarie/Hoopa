//go:build p1integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
)

var errOperationPolicy = errors.New("invalid typed operation evidence or ordering")

func operationPolicyError() error    { return errOperationPolicy }
func operationSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func operationEncode(value any) []byte {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return nil
	}
	return out.Bytes()
}
func operationDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}
func operationID(value string) bool { _, err := config.ParseID(value); return err == nil }
func operationFields(raw []byte, names ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := object[name]; !ok {
			return false
		}
	}
	return true
}
func operationStrict(raw []byte, value any) bool {
	canonical, err := config.CanonicalJSON(raw)
	if err != nil || !bytes.Equal(canonical, raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return false
	}
	encoded, err := config.CanonicalJSON(operationEncode(value))
	return err == nil && bytes.Equal(encoded, raw)
}
func operationTransition(a, b control.Phase) bool {
	if a == control.Committed || a == control.RolledBack {
		return false
	}
	if a == b || b == control.Degraded || b == control.RollingBack {
		return true
	}
	switch a {
	case control.Prepared:
		return b == control.Validated
	case control.Validated:
		return b == control.Applying
	case control.Applying:
		return b == control.Verifying
	case control.Verifying:
		return b == control.Publishing
	case control.Publishing:
		return b == control.Committed
	case control.RollingBack:
		return b == control.RolledBack
	case control.Degraded:
		return b == control.Verifying
	}
	return false
}
func operationPhase(p control.Phase) bool {
	switch p {
	case control.Prepared, control.Validated, control.Applying, control.Verifying, control.Publishing, control.Committed, control.RollingBack, control.RolledBack, control.Degraded:
		return true
	}
	return false
}

type operationApplyEvidence struct {
	Version              int                  `json:"version"`
	Step                 string               `json:"step"`
	Base                 config.ActivePointer `json:"base"`
	TargetManifestSHA256 string               `json:"target_manifest_sha256"`
	OldRunID             *config.RunID        `json:"old_run_id"`
	TargetRunID          *config.RunID        `json:"target_run_id"`
	OldStopped           bool                 `json:"old_stopped"`
	TargetStopped        bool                 `json:"target_stopped"`
	RollbackWorkdir      string               `json:"rollback_workdir"`
	RollbackRunID        *config.RunID        `json:"rollback_run_id"`
}
type operationLifecycleEvidence struct {
	Version         int                  `json:"version"`
	Step            string               `json:"step"`
	Mode            string               `json:"mode"`
	Desired         string               `json:"desired_runtime"`
	Base            config.ActivePointer `json:"base"`
	SourceRunID     config.RunID         `json:"source_run_id"`
	ExitOperationID config.ID            `json:"exit_operation_id"`
	ExitGate        string               `json:"exit_gate"`
	SourceStopped   bool                 `json:"source_stopped"`
	Workdir         string               `json:"workdir"`
	TargetRunID     *config.RunID        `json:"target_run_id"`
}
type operationAdoptionEvidence struct {
	Version  int                      `json:"version"`
	Kind     string                   `json:"kind"`
	Step     string                   `json:"step"`
	Base     config.ActivePointer     `json:"base"`
	RunID    config.RunID             `json:"run_id"`
	Identity platform.ProcessIdentity `json:"identity"`
}
type operationServiceEvidence struct {
	Version         int                  `json:"version"`
	Kind            string               `json:"kind"`
	Step            string               `json:"step"`
	Base            config.ActivePointer `json:"base"`
	Instance        string               `json:"instance"`
	AdmissionClosed bool                 `json:"admission_closed"`
	LeaseReplacedBy string               `json:"lease_replaced_by"`
}
type operationRecoveryEvidence struct {
	Version      int           `json:"version"`
	TargetID     config.ID     `json:"target_operation_id"`
	TargetDigest string        `json:"target_request_digest"`
	TargetSHA    string        `json:"target_record_sha256"`
	TargetPhase  control.Phase `json:"target_phase"`
}
type operationMeaning struct {
	stored                            control.StoredOperation
	targetID, targetDigest, targetSHA string
	targetPhase                       control.Phase
	originID                          string
	runID                             config.RunID
	targetRunID, rollbackRunID        config.RunID // historical roles are independent of the final current run
	identity                          *platform.ProcessIdentity
	serviceInstance                   string
	admissionClosed                   bool
	kind                              string
	sourceRun                         config.RunID
	runs                              map[config.RunID]bool
}

func operationBase(base config.ActivePointer, i control.Intent) bool {
	return base.Version == 1 && base.Revision == i.BaseRevision && reflect.DeepEqual(base.Generation, i.PreviousGeneration)
}
func operationRaw(stored control.StoredOperation, raw []byte, ref originalRef) bool {
	if len(raw) == 0 || len(raw) > config.MaxOperationBytes || ref.Path == "" || ref.Bytes != int64(len(raw)) || ref.SHA256 != stored.SHA256 || operationSHA(raw) != stored.SHA256 {
		return false
	}
	var decoded control.Operation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF || !reflect.DeepEqual(decoded, stored.Record) {
		return false
	}
	// Verify the actual codec serialization (Encoder, HTML escaping disabled,
	// final LF), not json.Marshal's different bytes. Caller supplies real raw.
	if !bytes.Equal(operationEncode(decoded), raw) {
		return false
	}
	o := decoded
	i := o.Identity
	request, err := control.ParseRequest([]byte(o.Request))
	if err != nil || o.Version != 1 || o.Compacted || i.OperationID != request.OperationID() || i.RequestID != request.ID() || i.RequestDigest != request.Digest() || i.Operation != request.Operation() || i.APIVersion != "v1" || request.Version() != "v1" || !operationID(string(i.Intent.BaseRevision)) || !operationDigest(i.Intent.CandidateDigest) || !operationDigest(i.Intent.ImpactDigest) || !operationPhase(o.Phase) || len(o.Steps) == 0 || len(o.Steps) > control.MaxSteps {
		return false
	}
	if revision := i.Intent.TargetRevision; revision != nil && (!operationID(string(*revision)) || *revision == i.Intent.BaseRevision) {
		return false
	}
	for _, gen := range []*config.Generation{i.Intent.PreviousGeneration, i.Intent.TargetGeneration} {
		if gen != nil && !operationID(string(*gen)) {
			return false
		}
	}
	body := struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(o.Request), o.Steps}
	if operationSHA(operationEncode(body)) != o.BodySHA256 {
		return false
	}
	total := 0
	for n, step := range o.Steps {
		if step.Sequence != n+1 || !operationPhase(step.Phase) || n == 0 && step.Phase != control.Prepared || n > 0 && !operationTransition(o.Steps[n-1].Phase, step.Phase) {
			return false
		}
		for _, value := range [][]byte{[]byte(step.Evidence), []byte(step.Result)} {
			canonical, err := config.CanonicalJSON(value)
			if err != nil || len(canonical) == 0 || canonical[0] != '{' || !bytes.Equal(canonical, value) {
				return false
			}
			total += len(value)
		}
	}
	last := o.Steps[len(o.Steps)-1]
	return total <= control.MaxStepBytes && last.Phase == o.Phase && bytes.Equal([]byte(last.Result), []byte(o.Result))
}

// projectOperationRecord is a pure codec/evidence projection. It does not read
// originals, perform process proof or synthesize trace events from final steps.
func projectOperationRecord(stored control.StoredOperation, raw []byte, ref originalRef, previous, target bundleSnapshot) (operationRecord, error) {
	if !operationRaw(stored, raw, ref) {
		return operationRecord{}, operationPolicyError()
	}
	o := stored.Record
	i := o.Identity
	op := operationRecord{ID: string(i.OperationID), RequestID: i.RequestID, RequestDigest: i.RequestDigest, Phase: string(o.Phase), BaseRevision: i.Intent.BaseRevision, Operation: i.Operation, Subtype: "unknown", Original: ref, recordSource: bytes.Clone(raw), Previous: previous, Target: target}
	if i.Intent.TargetRevision != nil {
		copy := *i.Intent.TargetRevision
		op.TargetRevision = &copy
	}
	meaning, err := operationClassify(stored)
	if err != nil {
		return operationRecord{}, err
	}
	op.Subtype = meaning.kind
	switch op.Subtype {
	case "apply", "fresh-start", "restart", "adopt-running":
		op.Desired = "running"
	case "stop":
		op.Desired = "stopped"
	case "service-stop":
		var evidence operationServiceEvidence
		json.Unmarshal([]byte(o.Steps[len(o.Steps)-1].Evidence), &evidence)
		op.Desired = evidence.Base.DesiredRuntime
	}
	if gen := i.Intent.PreviousGeneration; gen != nil && previous.Generation != *gen {
		return operationRecord{}, operationPolicyError()
	}
	if gen := i.Intent.TargetGeneration; gen != nil && target.Generation != *gen {
		return operationRecord{}, operationPolicyError()
	}
	if meaning.kind == "apply" {
		var evidence operationApplyEvidence
		json.Unmarshal([]byte(o.Steps[len(o.Steps)-1].Evidence), &evidence)
		if op.Target.ManifestSHA256 != evidence.TargetManifestSHA256 || evidence.Base.Generation != nil && op.Previous.ManifestSHA256 != evidence.Base.ManifestSHA256 {
			return operationRecord{}, operationPolicyError()
		}
	}
	return op, nil
}
func operationClassify(stored control.StoredOperation) (operationMeaning, error) {
	o := stored.Record
	i := o.Identity.Intent
	r, _ := control.ParseRequest([]byte(o.Request))
	params := r.Parameters()
	raw := []byte(o.Steps[len(o.Steps)-1].Evidence)
	meaning := operationMeaning{stored: stored, kind: "unknown"}
	switch r.Operation() {
	case "config.apply", "runtime.start", "runtime.stop", "operation.recover", "service.stop":
	default:
		return meaning, operationPolicyError()
	}
	if bytes.Equal(raw, []byte(`{}`)) {
		if o.Phase != control.Prepared || len(o.Steps) != 1 {
			return meaning, operationPolicyError()
		}
		return meaning, nil
	}
	expected := func() bool {
		var p struct {
			Revision config.Revision `json:"expected_revision"`
		}
		return json.Unmarshal(params, &p) == nil && p.Revision == i.BaseRevision && i.ImpactDigest == operationSHA(params)
	}
	switch r.Operation() {
	case "config.apply":
		var e operationApplyEvidence
		if !operationStrict(raw, &e) || e.Version != 1 || !operationBase(e.Base, i) || i.TargetRevision == nil || i.TargetGeneration == nil || !operationDigest(e.TargetManifestSHA256) || e.OldRunID == nil != (e.Base.Generation == nil) {
			return meaning, operationPolicyError()
		}
		if !operationFields(params, "plan", "confirmed_plan_digest", "accept_disruption") {
			return meaning, operationPolicyError()
		}
		var p struct {
			Plan   json.RawMessage `json:"plan"`
			Digest string          `json:"confirmed_plan_digest"`
		}
		json.Unmarshal(params, &p)
		var d control.PlanDocument
		json.Unmarshal(p.Plan, &d)
		calculated, err := control.ComputePlanDigest(p.Plan)
		var planObject map[string]json.RawMessage
		json.Unmarshal(p.Plan, &planObject)
		if err != nil || calculated != d.PlanDigest || p.Digest != d.PlanDigest || i.CandidateDigest != d.PlanDigest || i.ImpactDigest != operationSHA(planObject["impact"]) || d.BaseRevision != string(i.BaseRevision) || d.Impact.TargetGeneration != string(*i.TargetGeneration) {
			return meaning, operationPolicyError()
		}
		meaning.runs = map[config.RunID]bool{}
		for _, id := range []*config.RunID{e.OldRunID, e.TargetRunID, e.RollbackRunID} {
			if id != nil {
				meaning.runs[*id] = true
			}
		}
		if o.Phase == control.Committed && (!e.OldStopped || e.TargetRunID == nil || e.TargetStopped || e.RollbackRunID != nil) {
			return meaning, operationPolicyError()
		}
		if o.Phase == control.RolledBack && (!e.OldStopped || !e.TargetStopped || e.RollbackRunID == nil || e.Base.Generation == nil) {
			return meaning, operationPolicyError()
		}
		meaning.kind = "apply"
		meaning.targetRunID = valueRun(e.TargetRunID)
		meaning.rollbackRunID = valueRun(e.RollbackRunID)
		meaning.runID = meaning.targetRunID
		// Recover chooses the rollback branch from retained RollingBack history,
		// including a later Degraded state. A missing rollback run stays unknown;
		// it does not authorize proving or restarting the stopped candidate.
		for _, step := range o.Steps {
			if step.Phase == control.RollingBack || step.Phase == control.RolledBack {
				meaning.runID = meaning.rollbackRunID
				break
			}
		}
		meaning.originID = string(o.Identity.OperationID)
	case "runtime.start":
		if !operationFields(params, "expected_revision", "control_only") || !expected() {
			return meaning, operationPolicyError()
		}
		var p struct {
			Only bool `json:"control_only"`
		}
		if json.Unmarshal(params, &p) != nil || p.Only {
			return meaning, operationPolicyError()
		}
		var marker struct {
			Kind string `json:"kind"`
		}
		json.Unmarshal(raw, &marker)
		if marker.Kind == "adopt-running" {
			var e operationAdoptionEvidence
			if !operationStrict(raw, &e) || e.Version != 1 || !operationBase(e.Base, i) || e.Base.Generation == nil || e.Base.OperationID == nil || e.Base.DesiredRuntime != "running" || i.TargetRevision != nil || !reflect.DeepEqual(i.TargetGeneration, e.Base.Generation) || i.CandidateDigest != e.Base.ManifestSHA256 || !operationID(string(e.RunID)) || e.Identity.PID <= 0 || e.Identity.StartTicks == 0 {
				return meaning, operationPolicyError()
			}
			meaning.kind = "adopt-running"
			meaning.runID = e.RunID
			meaning.originID = string(*e.Base.OperationID)
			meaning.identity = &e.Identity
		} else {
			var e operationLifecycleEvidence
			if !operationStrict(raw, &e) || e.Version != 1 || !operationBase(e.Base, i) || e.Base.Generation == nil || e.Base.OperationID == nil || e.Desired != "running" || !e.SourceStopped || e.ExitOperationID != *e.Base.OperationID || !reflect.DeepEqual(i.TargetGeneration, e.Base.Generation) || i.CandidateDigest != e.Base.ManifestSHA256 || !operationID(string(e.SourceRunID)) {
				return meaning, operationPolicyError()
			}
			switch e.Mode {
			case "start":
				if i.TargetRevision == nil || e.Base.DesiredRuntime != "stopped" || e.ExitGate != "open" {
					return meaning, operationPolicyError()
				}
				meaning.kind = "fresh-start"
			case "restart":
				if i.TargetRevision != nil || e.Base.DesiredRuntime != "running" || e.ExitGate != "closed" {
					return meaning, operationPolicyError()
				}
				meaning.kind = "restart"
			default:
				return meaning, operationPolicyError()
			}
			meaning.sourceRun = e.SourceRunID
			meaning.runs = map[config.RunID]bool{e.SourceRunID: true}
			if e.TargetRunID != nil {
				if *e.TargetRunID == e.SourceRunID {
					return meaning, operationPolicyError()
				}
				meaning.runs[*e.TargetRunID] = true
			}
			meaning.runID = valueRun(e.TargetRunID)
			meaning.originID = string(o.Identity.OperationID)
		}
	case "runtime.stop":
		var e operationLifecycleEvidence
		if !operationStrict(raw, &e) || e.Version != 1 || e.Mode != "stop" || !operationBase(e.Base, i) || e.Base.Generation == nil || e.Base.OperationID == nil || e.Base.DesiredRuntime != "running" || e.Desired != "stopped" || e.ExitOperationID != o.Identity.OperationID || e.ExitGate != "open" || e.TargetRunID != nil || e.Workdir != "" || i.TargetRevision == nil || !reflect.DeepEqual(i.TargetGeneration, e.Base.Generation) || i.CandidateDigest != e.Base.ManifestSHA256 || !operationFields(params, "expected_revision", "accept_disruption") || !expected() {
			return meaning, operationPolicyError()
		}
		var p struct {
			Accepted bool `json:"accept_disruption"`
		}
		json.Unmarshal(params, &p)
		if !p.Accepted {
			return meaning, operationPolicyError()
		}
		meaning.runs = map[config.RunID]bool{e.SourceRunID: true}
		meaning.sourceRun = e.SourceRunID
		meaning.originID = string(*e.Base.OperationID)
		meaning.kind = "stop"
		meaning.runID = e.SourceRunID
	case "service.stop":
		var e operationServiceEvidence
		if !operationStrict(raw, &e) || e.Version != 1 || e.Kind != "service-stop" || !operationBase(e.Base, i) || i.TargetRevision != nil || !reflect.DeepEqual(i.TargetGeneration, e.Base.Generation) || !operationFields(params, "expected_revision") || !expected() || i.CandidateDigest != operationSHA(mustCanonical(operationEncode(e.Base))) || !operationHex(e.Instance, 24) || o.Phase == control.Committed && !e.AdmissionClosed {
			return meaning, operationPolicyError()
		}
		if e.LeaseReplacedBy != "" && (!operationHex(e.LeaseReplacedBy, 24) || e.Instance == e.LeaseReplacedBy || !e.AdmissionClosed) {
			return meaning, operationPolicyError()
		}
		meaning.kind = "service-stop"
		meaning.serviceInstance = e.Instance
		meaning.admissionClosed = e.AdmissionClosed
	case "operation.recover":
		for _, step := range o.Steps {
			if step.Phase == control.RollingBack || step.Phase == control.RolledBack {
				return meaning, operationPolicyError()
			}
		}
		var e operationRecoveryEvidence
		var p struct {
			Target config.ID `json:"operation_id"`
		}
		json.Unmarshal(params, &p)
		if !operationStrict(raw, &e) || e.Version != 1 || !operationFields(params, "operation_id", "expected_revision") || !expected() || i.TargetRevision != nil || i.TargetGeneration != nil || e.TargetID != p.Target || !operationID(string(e.TargetID)) || e.TargetID == o.Identity.OperationID || i.CandidateDigest != e.TargetDigest || !operationDigest(e.TargetDigest) || !operationDigest(e.TargetSHA) || !operationPhase(e.TargetPhase) {
			return meaning, operationPolicyError()
		}
		meaning.kind = "recover"
		meaning.targetID = string(e.TargetID)
		meaning.targetDigest = e.TargetDigest
		meaning.targetSHA = e.TargetSHA
		meaning.targetPhase = e.TargetPhase
	}
	return meaning, nil
}
func valueRun(id *config.RunID) config.RunID {
	if id == nil {
		return ""
	}
	return *id
}
func operationHex(value string, n int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == n && hex.EncodeToString(decoded) == value
}
func mustCanonical(raw []byte) []byte { value, _ := config.CanonicalJSON(raw); return value }
