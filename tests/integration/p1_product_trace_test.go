//go:build linux && amd64 && p1integration

package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

var errP1ProductTrace = errors.New("P1_PRODUCT_TRACE_PROJECTION_FAILED")

type p1ProductTrace struct {
	FixtureSeeds map[string]p1CapacitySeed
	Boundaries   []boundaryEvent
	Launches     []launchInvocation
	Signals      []signalInvocation
	History      []operationVersion
}

type p1TraceRun struct {
	intent   config.RunIntent
	identity platform.ProcessIdentity
	proof    []originalRef
}

func (r p1TraceRun) process() processIdentity {
	p := r.identity
	return processIdentity{BootID: p.BootID, OSStartIdentity: strconv.FormatUint(p.StartTicks, 10), BinaryPath: p.BinaryPath, BinarySHA256: p.BinarySHA256, LaunchNonceSHA256: p.NonceSHA256, PrivateWorkdir: p.Workdir, PID: p.PID, Generation: r.intent.Generation, RunID: r.intent.RunID}
}

// This projects authenticated actual events, not a guessed event log from final
// journal steps. The caller supplies complete streams in actual execution order.
// Unrelated diagnostic/checkpoint events remain in the retained trace originals.
// A launcher creation can be linked to a later independently verified incarnation;
// that link does not assert that exec, registration or readiness happened at fork.
// A pre-registration gap may instead use independently captured intent, birth,
// and fresh connected-owner proof. Neither source fabricates registration or
// readiness at fork. A missing or mismatched proof remains unavailable.
func p1ProjectTrace(records []p1TraceRecord, current map[string]operationRecord, bundles map[config.Generation]bundleSnapshot, launchProofs ...p1LaunchProof) (p1ProductTrace, error) {
	fail := func() (p1ProductTrace, error) { return p1ProductTrace{}, errP1ProductTrace }
	if err := p1ValidateTraceRecords(records); err != nil {
		return fail()
	}
	history, err := p1ReadTraceHistory(records, current, bundles)
	if err != nil {
		return fail()
	}
	seeds, err := p1ReadCapacitySeeds(records, current)
	if err != nil {
		return fail()
	}
	out := p1ProductTrace{History: history, FixtureSeeds: seeds}
	meanings := map[string]operationMeaning{}
	byRequest := map[string]string{}
	for id, op := range current {
		var source control.Operation
		if json.Unmarshal(op.recordSource, &source) != nil {
			return fail()
		}
		m, e := operationClassify(control.StoredOperation{Record: source, SHA256: op.Original.SHA256})
		if e != nil || byRequest[op.RequestID] != "" {
			return fail()
		}
		meanings[id], byRequest[op.RequestID] = m, id
	}
	// Only the actual registration hook (fresh Observe + protected publication)
	// supplies a live identity here. Reading an intent alone is never process proof.
	runs := map[string]p1TraceRun{}
	for _, r := range records {
		w, e := p1TraceUnwrap(r.Event.Data)
		if e != nil {
			return fail()
		}
		if r.Event.Component == "runtime" && r.Event.Point == "intent-published" {
			var d struct {
				Intent config.RunIntent `json:"intent"`
			}
			if json.Unmarshal(w.Detail, &d) != nil || d.Intent.Validate() != nil || string(d.Intent.RunID) != r.Event.RunID || string(d.Intent.OperationID) != r.Event.OperationID {
				return fail()
			}
			b, ok := bundles[d.Intent.Generation]
			if !ok || b.ManifestSHA256 != d.Intent.ManifestSHA256 || b.ConfigSHA256 != d.Intent.ConfigSHA256 || runs[r.Event.RunID].intent.RunID != "" {
				return fail()
			}
			runs[r.Event.RunID] = p1TraceRun{intent: d.Intent}
		}
		if r.Event.Component == "runtime" && r.Event.Point == "registration-verified" {
			var d struct {
				Registration config.RunRegistration   `json:"registration"`
				Identity     platform.ProcessIdentity `json:"identity"`
				SHA          string                   `json:"registration_sha256"`
			}
			if json.Unmarshal(w.Detail, &d) != nil {
				return fail()
			}
			run, ok := runs[r.Event.RunID]
			reg, p, i := d.Registration, d.Identity, run.intent
			if !ok || reg.Validate() != nil || reg.RunID != i.RunID || string(reg.OperationID) != r.Event.OperationID || reg.Generation != i.Generation || reg.ManifestSHA256 != i.ManifestSHA256 || reg.ConfigSHA256 != i.ConfigSHA256 || reg.Controller != i.Controller || reg.IntentSHA256 != operationSHA(operationEncode(i)) || d.SHA != operationSHA(operationEncode(reg)) || reg.PID != p.PID || reg.UID != p.UID || reg.BootID != p.BootID || reg.StartTicks != p.StartTicks || reg.BinarySHA256 != p.BinarySHA256 || reg.NonceSHA256 != p.NonceSHA256 || p.BinaryPath != i.Binary.Path || p.BinarySHA256 != i.Binary.SHA256 || p.Workdir != i.Workdir || p.NonceSHA256 != i.NonceSHA256 {
				return fail()
			}
			if len(run.proof) > 0 && run.identity != p {
				return fail()
			}
			if len(run.proof) == 0 {
				run.identity = p
				run.proof = append([]originalRef(nil), r.Originals...)
				runs[r.Event.RunID] = run
			}
		}
	}
	if err := p1ApplyLaunchProofs(records, runs, launchProofs); err != nil {
		return fail()
	}
	admitted := map[string]bool{}
	scope, scopeName, scopeID := "", "", ""
	scopeDigest := ""
	var activation *boundaryEvent
	emit := func(r p1TraceRecord, name, id string, runID string) *boundaryEvent {
		v := boundaryEvent{Index: uint64(len(out.Boundaries) + 1), Name: name, OperationID: id, RequestID: scope, RequestDigest: scopeDigest, Originals: append([]originalRef(nil), r.Originals...)}
		if runID != "" {
			run := runs[runID]
			p := run.process()
			v.Instance = &p
			v.NonceSHA256 = p.LaunchNonceSHA256
		}
		out.Boundaries = append(out.Boundaries, v)
		return &out.Boundaries[len(out.Boundaries)-1]
	}
	for _, r := range records {
		e := r.Event
		w, _ := p1TraceUnwrap(e.Data)
		caller := scopeID
		call := meanings[caller]
		effect := caller
		if call.kind == "recover" {
			effect = call.targetID
		}
		key := e.Component + "/" + e.Point
		switch key {
		case "control/request-begin":
			if scope != "" {
				return fail()
			}
			var d struct {
				Operation, Proposed string
				Digest              string
			}
			var raw map[string]json.RawMessage
			json.Unmarshal(w.Detail, &raw)
			json.Unmarshal(raw["operation"], &d.Operation)
			json.Unmarshal(raw["proposed_operation_id"], &d.Proposed)
			json.Unmarshal(raw["request_digest"], &d.Digest)
			if e.RequestID == "" || !operationID(d.Proposed) || !operationDigest(d.Digest) {
				return fail()
			}
			scopeID = ""
			if id := byRequest[e.RequestID]; id != "" {
				op := current[id]
				if id == d.Proposed && op.RequestDigest == d.Digest && op.Operation == d.Operation {
					scopeID = id
				}
			}
			// Same request ID with a different digest is a real rejected request,
			// not the earlier admitted operation. Retain its raw scope without
			// assigning that earlier operation's effect authority to this caller.
			scope, scopeName = e.RequestID, d.Operation
			scopeDigest = d.Digest
			activation = nil
		case "control/request-end":
			if scope != e.RequestID {
				return fail()
			}
			scope, scopeName, scopeID = "", "", ""
			scopeDigest = ""
			activation = nil
		case "control/inputs-revalidated", "control/candidate-validated", "store/bundle-published":
			if scope != e.RequestID || scopeName != "config.apply" {
				return fail()
			}
			name := e.Point
			if e.Point == "bundle-published" {
				name = "candidate-published"
			}
			emit(r, name, "", "") // request context remains explicitly unadmitted.
		case "journal/published":
			if r.OperationOriginal == nil {
				return fail()
			}
			raw, readErr := os.ReadFile(r.OperationOriginal.Path)
			var source control.Operation
			if readErr != nil || json.Unmarshal(raw, &source) != nil {
				return fail()
			}
			id := e.OperationID
			m, ok := meanings[id]
			if !ok {
				return fail()
			}
			if !admitted[id] {
				if seed, fixture := seeds[id]; fixture {
					if scope != "" {
						return fail()
					}
					v := emit(r, "fixture-seed-durable", id, "")
					v.Originals = append(append(append([]originalRef{}, seed.BeginOriginals...), seed.PublishOriginals...), seed.EndOriginals...)
				} else {
					emit(r, "operation-durable", id, "")
				}
				admitted[id] = true
			}
			if source.Phase == control.Validated && m.kind == "stop" {
				emit(r, "stop-intent-durable", id, "")
			}
			if source.Phase == control.Validated && m.kind == "service-stop" {
				emit(r, "service-stop-intent-durable", id, "")
			}
			name := ""
			switch source.Phase {
			case control.Publishing:
				name = "publishing-durable"
				if !operationCore(m.kind) {
					name = "journal-publishing-durable"
				}
			case control.Committed:
				name = "committed-durable"
			case control.RollingBack:
				name = "rolling-back-durable"
			case control.RolledBack:
				name = "rolled-back-durable"
			case control.Degraded:
				name = "degraded-durable"
			}
			if name != "" {
				emit(r, name, id, "")
			}
		case "runtime/intent-published":
			run := runs[e.RunID]
			if run.intent.RunID == "" || !admitted[e.OperationID] {
				return fail()
			}
			v := emit(r, "spawn-intent-durable", e.OperationID, "")
			v.NonceSHA256 = run.intent.NonceSHA256
		case "runtime/launcher-started":
			var d struct {
				PID    int              `json:"pid"`
				Intent config.RunIntent `json:"intent"`
			}
			json.Unmarshal(w.Detail, &d)
			run := runs[e.RunID]
			if len(run.proof) == 0 || !reflect.DeepEqual(run.intent, d.Intent) || d.PID != run.identity.PID || string(run.intent.OperationID) != e.OperationID {
				return fail()
			}
			v := emit(r, "core-spawn", e.OperationID, e.RunID)
			v.Originals = append(v.Originals, run.proof...)
			out.Launches = append(out.Launches, launchInvocation{BoundaryIndex: v.Index, ID: e.RunID, NonceSHA256: v.NonceSHA256, Instance: *v.Instance})
		case "runtime/registration-verified", "runtime/closed-verified", "runtime/entry-opening", "runtime/entry-opened", "runtime/open-verified", "runtime/exit-observed":
			run := runs[e.RunID]
			var d struct {
				Identity    platform.ProcessIdentity    `json:"identity"`
				Observation owned.ControllerObservation `json:"observation"`
			}
			if json.Unmarshal(w.Detail, &d) != nil || len(run.proof) == 0 || d.Identity != run.identity {
				return fail()
			}
			id := e.OperationID
			if id == "" {
				id = effect
			}
			if !admitted[id] {
				return fail()
			}
			name := map[string]string{"registration-verified": "registration-durable", "closed-verified": "preopen-verified", "entry-opening": "entry-opening", "entry-opened": "entry-open", "exit-observed": "core-stopped"}[e.Point]
			if e.Point == "closed-verified" || e.Point == "open-verified" {
				gate := "closed"
				if e.Point == "open-verified" {
					gate = "open"
				}
				if d.Observation.RunID != run.intent.RunID || d.Observation.Generation != run.intent.Generation || d.Observation.ConfigSHA256 != run.intent.ConfigSHA256 || d.Observation.Gate != gate {
					return fail()
				}
			}
			if e.Point == "open-verified" {
				name = "target-verified"
				if current[id].Previous.Generation == run.intent.Generation && current[id].Target.Generation != run.intent.Generation {
					name = "old-state-verified"
				}
			}
			emit(r, name, id, e.RunID)
			if e.Point == "exit-observed" && meanings[id].kind == "stop" {
				emit(r, "target-verified", id, e.RunID)
			}
		case "platform/signal-sent":
			var d struct {
				Identity platform.ProcessIdentity `json:"identity"`
				Signal   int                      `json:"signal"`
			}
			if json.Unmarshal(w.Detail, &d) != nil || !admitted[effect] {
				return fail()
			}
			runID := ""
			for id, run := range runs {
				if len(run.proof) > 0 && run.identity == d.Identity {
					if runID != "" {
						return fail()
					}
					runID = id
				}
			}
			if runID == "" || d.Signal != 15 {
				return fail()
			}
			v := emit(r, "core-stop", effect, runID)
			out.Signals = append(out.Signals, signalInvocation{BoundaryIndex: v.Index, Signal: "SIGTERM", Target: *v.Instance})
		case "control/activation-verified":
			// Startup/status proofs with no requesting operation stay in the raw
			// trace; they are not a fabricated management admission or mutation.
			if scope == "" {
				continue
			}
			run := runs[e.RunID]
			var d struct {
				Identity    platform.ProcessIdentity    `json:"identity"`
				Observation owned.ControllerObservation `json:"observation"`
			}
			if json.Unmarshal(w.Detail, &d) != nil || len(run.proof) == 0 || d.Identity != run.identity || d.Observation.RunID != run.intent.RunID || d.Observation.Generation != run.intent.Generation || d.Observation.Gate != "open" {
				return fail()
			}
			if caller == "" {
				continue
			} // Rejected preflight has no accepted call to bind.
			if !admitted[caller] {
				if call.kind != "recover" && call.kind != "adopt-running" {
					return fail()
				}
				v := emit(r, "ownership-preflight", "", e.RunID)
				v.TargetOperationID = e.OperationID
				v.OriginOperationID = meanings[e.OperationID].originID
			} else if call.kind == "adopt-running" {
				v := emit(r, "ownership-revalidated", caller, e.RunID)
				v.OriginOperationID = call.originID
			} else if call.kind == "recover" {
				v := emit(r, "recovery-target-verified", caller, e.RunID)
				v.TargetOperationID = call.targetID
				v.OriginOperationID = meanings[call.targetID].originID
				copy := *v
				activation = &copy
			} else {
				return fail()
			}
		case "workflow/recover-target-terminal":
			if call.kind != "recover" || !admitted[caller] {
				return fail()
			}
			target := meanings[call.targetID]
			if target.kind == "stop" || target.kind == "service-stop" {
				v := emit(r, "recovery-target-verified", caller, "")
				v.TargetOperationID = call.targetID
			} else if activation == nil || activation.TargetOperationID != call.targetID {
				return fail()
			}
		case "store/active-published":
			var d struct {
				Active config.ActivePointer `json:"active"`
				SHA    string               `json:"sha256"`
			}
			if json.Unmarshal(w.Detail, &d) != nil || d.Active.OperationID == nil || string(*d.Active.OperationID) != e.OperationID || d.SHA != operationSHA(operationEncode(d.Active)) || !admitted[e.OperationID] {
				return fail()
			}
			name := "active-published"
			op := current[e.OperationID]
			if meanings[e.OperationID].kind == "apply" && d.Active.Generation != nil && *d.Active.Generation == op.Previous.Generation && *d.Active.Generation != op.Target.Generation {
				name = "old-active-published"
			}
			emit(r, name, e.OperationID, "")
		case "service/admission-closed":
			var d struct {
				Instance string `json:"service_instance"`
			}
			json.Unmarshal(w.Detail, &d)
			if meanings[e.OperationID].kind != "service-stop" || d.Instance != meanings[e.OperationID].serviceInstance {
				return fail()
			}
			v := emit(r, "service-admission-closed", e.OperationID, "")
			v.OriginOperationID = d.Instance
		case "fixture/capacity-seed-begin", "fixture/capacity-seed-end":
			// The complete window and each actual publication were checked by
			// p1ReadCapacitySeeds. They are fixture provenance, not API effects.
			if scope != "" {
				return fail()
			}
		case "store/checkpoint", "fixture/source-resolved",
			"runtime/termination-signal-confirmed", "runtime/source-exit-observed",
			"runtime/ownership-observation-failed", "runtime/open-proof-mismatch",
			"platform/controller-proof-failed", "platform/pidfd-poll-failed":
			// Retained diagnostics, effect-intent checkpoints, or duplicate
			// confirmations. None creates another operation/launch/signal fact.
		default:
			// Workflow cuts are named interruption opportunities. Their actual
			// journal/runtime primitives above carry the effects. A new primitive
			// outside those known categories must be explicitly reviewed first.
			if e.Component != "workflow" || !p1KnownWorkflowCut(e.Point) {
				return fail()
			}
		}
		// An observed crash closes this stream's unfinished request scope; the
		// next owned manager has a new authenticated stream, not a resumed stack.
		if r.Decision == p1TraceCrashManager {
			scope, scopeName, scopeID = "", "", ""
			scopeDigest = ""
			activation = nil
		}
	}
	if scope != "" {
		return fail()
	}
	for id := range current {
		if !admitted[id] {
			return fail()
		}
	}
	return out, nil
}

func p1KnownWorkflowCut(point string) bool {
	switch point {
	case "candidate-validated", "stop-old", "old-stop-observed", "prepare-target", "target-intent-published", "spawn-target", "spawn-returned-before-registration", "target-started", "activate-target", "entry-opened", "target-verified", "active-published", "target-committed", "runtime-unverified",
		"rollback-stop-candidate", "rollback-candidate-exited", "rollback-prepare-workdir", "rollback-prepare-run", "rollback-intent-published", "rollback-spawn", "rollback-started", "rollback-activate", "rollback-verified", "rollback-active-published", "rollback-committed", "rollback-unverified",
		"start-intent-durable", "start-prepare-workdir", "start-prepare-run", "start-run-intent-published", "start-spawn", "start-verify-closed", "start-activate", "start-entry-opened", "start-publish", "start-active-published", "start-committed",
		"stop-intent-durable", "stop-original", "stop-exit-observed", "stop-exit-durable", "stop-publish", "stop-active-published", "stop-committed",
		"recover-admitted", "recover-active-published", "recover-start-verify-closed", "recover-start-activate", "recover-start-open-verified", "recover-start-publish", "recover-start-committed",
		"adopt-validated", "adopt-applying", "adopt-verifying", "adopt-publishing", "adopt-committed",
		"service-stop-intent", "service-quiesce", "service-stop-verifying", "service-stop-publishing", "service-stop-committed",
		"recover-service-validated", "recover-service-applying", "recover-service-verifying", "recover-service-publishing", "recover-service-committed":
		return true
	}
	return false
}

func TestP1OperationPolicyAdoptionPreflight(t *testing.T) {
	// Synthetic policy cases only. The separate archived test supplies the
	// actual Start-before-admission trace that motivated this narrow rule.
	e, op := policyEvidence(t, "adopt-running")
	at := -1
	var proof boundaryEvent
	for i, v := range e.Boundaries {
		if v.OperationID == op.ID && v.Name == "operation-durable" {
			at = i
		}
		if v.OperationID == op.ID && v.Name == "ownership-revalidated" {
			proof = v
		}
	}
	if at < 0 || proof.Instance == nil {
		t.Fatal("policy fixture missing adoption")
	}
	proof.Name = "ownership-preflight"
	proof.OperationID = ""
	proof.RequestID = op.RequestID
	proof.TargetOperationID = proof.OriginOperationID
	e.Boundaries = append(e.Boundaries[:at], append([]boundaryEvent{proof}, e.Boundaries[at:]...)...)
	for i := range e.Boundaries {
		e.Boundaries[i].Index = uint64(i + 1)
	}
	// The inserted preflight follows the setup core's launch and signals, so
	// their indices remain unchanged.
	must(t, validateOperationOrdering(e, policyCoreSHA))
	for _, change := range []string{"claim-admission", "wrong-origin", "wrong-target", "different-instance", "after-admission"} {
		t.Run(change, func(t *testing.T) {
			bad := clonePolicyEvidence(e)
			v := &bad.Boundaries[at]
			switch change {
			case "claim-admission":
				v.OperationID = op.ID
			case "wrong-origin":
				v.OriginOperationID = op.ID
			case "wrong-target":
				v.TargetOperationID = op.ID
			case "different-instance":
				p := *v.Instance
				p.OSStartIdentity = "2002"
				v.Instance = &p
			case "after-admission":
				bad.Boundaries[at], bad.Boundaries[at+1] = bad.Boundaries[at+1], bad.Boundaries[at]
				bad.Boundaries[at].Index = uint64(at + 1)
				bad.Boundaries[at+1].Index = uint64(at + 2)
			}
			if validateOperationOrdering(bad, policyCoreSHA) == nil {
				t.Fatal("unbound or admitted preflight accepted")
			}
		})
	}
}

// Offline checking of retained real executions. This runs no new core and makes
// no claim that the incomplete productHarness factory or fault matrix has run.
func TestP1ArchivedProductTraceProjection(t *testing.T) {
	root := os.Getenv("HOOPA_P1_PROJECTION_ARCHIVE")
	if root == "" {
		t.Fatal("explicit retained projection archive required")
	}
	records, operations, bundles, durable := p1LoadProjectionArchive(t, root)
	got, err := p1ProjectTrace(records, operations, bundles)
	must(t, err)
	t.Run("reject-unclosed-tail", func(t *testing.T) {
		if len(records) == 0 || records[len(records)-1].Event.Point != "request-end" {
			t.Fatal("archive needs its actual request-end tail")
		}
		// This is a valid authenticated contiguous prefix with all terminal
		// operation files still present. Sequence/hash checks alone cannot reject
		// it; the projector must refuse the missing request completion witness.
		if _, err := p1ProjectTrace(records[:len(records)-1], operations, bundles); err == nil {
			t.Fatal("unfinished request prefix accepted as a completed snapshot")
		}
	})
	core := ""
	for _, b := range bundles {
		for key, sha := range b.AssetSHA256 {
			if len(key) > 7 && key[:7] == "binary:" {
				core = sha
			}
		}
	}
	e := productEvidence{Operations: operations, DurableFiles: durable, OperationHistory: got.History, Boundaries: got.Boundaries, Launches: got.Launches, Signals: got.Signals}
	if err = validateOperationOrdering(e, core); err != nil {
		// Fixed category names/indices only, never original requests/credentials.
		for _, v := range got.Boundaries {
			t.Logf("boundary %d %s op=%s", v.Index, v.Name, v.OperationID)
		}
		t.Fatal(err)
	}
	if len(got.Launches) != 2 || len(got.Signals) != 2 || len(operations) != 7 {
		t.Fatal("incomplete actual projection scene")
	}
	t.Logf("retained trace: records=%d history=%d boundaries=%d launches=%d signals=%d operations=%d", len(records), len(got.History), len(got.Boundaries), len(got.Launches), len(got.Signals), len(operations))
	if output := os.Getenv("HOOPA_P1_PROJECTION_RESULT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("absolute projection output required")
		}
		_, err = p1WriteOriginal(output, p1JSON(t, map[string]any{"kind": "offline-actual-trace-projection", "archive": root, "records": len(records), "history": len(got.History), "boundaries": got.Boundaries, "launches": got.Launches, "signals": got.Signals, "operations": len(operations)}))
		must(t, err)
	}
}
