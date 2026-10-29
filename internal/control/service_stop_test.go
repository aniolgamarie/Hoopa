//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

// Real private Store/writer/journal; callbacks close only synthetic admission.
// These tests neither launch a core nor claim an OS process has exited.
func serviceBind(t *testing.T, c *Coordinator) string {
	t.Helper()
	id, root, err := c.OpenServiceRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if !serviceInstance(id) {
		t.Fatal("invalid session")
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}
func serviceRequest(t *testing.T, id string, revision config.Revision) []byte {
	t.Helper()
	return planRaw(t, map[string]any{"api_version": "v1", "request_id": id, "operation": "service.stop", "params": map[string]any{"expected_revision": revision}})
}
func serviceBase(t *testing.T, c *Coordinator) config.ActivePointer {
	t.Helper()
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	return base
}
func serviceReopen(t *testing.T, c *Coordinator, p config.Paths) *Coordinator {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenCoordinator(p, coordinatorValidator(t), func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	return next
}
func serviceProtected(t *testing.T, p config.Paths) map[string]string {
	t.Helper()
	tree := coordinatorTree(t, p)
	operations := filepath.Join(p.State, "operations")
	for path := range tree {
		if path == operations || strings.HasPrefix(path, operations+"/") {
			delete(tree, path)
		}
	}
	return tree
}
func serviceAssertBase(t *testing.T, c *Coordinator, base config.ActivePointer) {
	t.Helper()
	if got := serviceBase(t, c); !reflect.DeepEqual(got, base) {
		t.Fatal("service operation changed core authority/revision", got, base)
	}
}
func serviceAdmitCut(t *testing.T, c *Coordinator, id string) (StoredOperation, serviceStopEvidence, []byte) {
	t.Helper()
	return serviceAdmitFixture(t, c, id, false)
}
func serviceAdmitFixture(t *testing.T, c *Coordinator, id string, legacy bool) (StoredOperation, serviceStopEvidence, []byte) {
	t.Helper()
	base := serviceBase(t, c)
	raw := serviceRequest(t, id, base.Revision)
	r, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	state := serviceStopEvidence{Version: 1, Kind: "service-stop", Step: "service-stop-prepared", Base: base, Instance: c.serviceID}
	intent := Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, TargetGeneration: base.Generation, CandidateDigest: serviceBaseDigest(base), ImpactDigest: hash(r.params)}
	seed := StoredOperation{Record: Operation{Identity: Identity{OperationID: r.OperationID(), RequestID: r.ID(), Intent: intent}}}
	var stored StoredOperation
	var made bool
	if legacy {
		stored, made, err = c.journal.Admit(r, intent, serviceStopResult(seed, Prepared))
	} else {
		evidence, _ := encode(state)
		stored, made, err = c.journal.admitWithEvidence(r, intent, serviceStopResult(seed, Prepared), evidence)
	}
	if err != nil || !made {
		t.Fatal("admit stop fixture", made, err)
	}
	return stored, state, raw
}
func serviceExecute(t *testing.T, c *Coordinator, stored StoredOperation, s serviceStopEvidence, quiesce func() error, boundary func(string) error) (StoredOperation, error) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.executeServiceStop(context.Background(), stored, s, quiesce, boundary)
}
func serviceCommitted(t *testing.T, c *Coordinator, id string) (StoredOperation, []byte) {
	t.Helper()
	raw := serviceRequest(t, id, serviceBase(t, c).Revision)
	op, made, err := c.StopService(context.Background(), raw, func() error { return nil })
	if err != nil || !made || op.Record.Phase != Committed {
		t.Fatal(made, op.Record.Phase, err)
	}
	return op, raw
}
func serviceGeneric(t *testing.T, c *Coordinator, r Request, terminalRecord bool) StoredOperation {
	t.Helper()
	op, made, err := c.journal.Admit(r, intent(), json.RawMessage(`{"outcome":"pending"}`))
	if err != nil || !made {
		t.Fatal(made, err)
	}
	if terminalRecord {
		for _, phase := range []Phase{Validated, Applying, Verifying, Publishing, Committed} {
			op, err = c.journal.Advance(r.OperationID(), op.SHA256, phase, json.RawMessage(`{}`), json.RawMessage(`{"outcome":"fixture"}`))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return op
}
func TestServiceSessionOnePerSharedWriterLifetime(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	before := coordinatorTree(t, p)
	first := serviceBind(t, c)
	copy := *c
	if _, root, err := copy.OpenServiceRuntime(); !errors.Is(err, ErrConflict) || root != nil {
		if root != nil {
			root.Close()
		}
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("session mint changed files")
	}
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	if _, root, err := c.OpenServiceRuntime(); !errors.Is(err, config.ErrClosed) || root != nil {
		t.Fatal(err)
	}
	next := serviceReopen(t, c, p)
	second := serviceBind(t, next)
	if first == second {
		t.Fatal("new writer reused service incarnation")
	}
}
func TestServiceStopDurableBeforeQuiesceAndProtectedTree(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "pristine"
		if running {
			name = "synthetic-running-authority"
		}
		t.Run(name, func(t *testing.T) {
			var c *Coordinator
			var p config.Paths
			if running {
				fixture := lifecycleRunningFixture(t)
				c, p = fixture.c, fixture.paths
			} else {
				c, p = coordinatorFixture(t, nil)
			}
			instance := serviceBind(t, c)
			base := serviceBase(t, c)
			protected := serviceProtected(t, p)
			raw := serviceRequest(t, "public-service-stop", base.Revision)
			r, _ := ParseRequest(raw)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			op, made, err := c.StopService(ctx, raw, func() error {
				calls++
				if c.mu.TryLock() {
					c.mu.Unlock()
					t.Fatal("quiesce callback escaped control lock")
				}
				durable, e := c.journal.Get(r.OperationID())
				if e != nil {
					t.Fatal(e)
				}
				initial := durable
				initial.Record.Steps = durable.Record.Steps[:1]
				initial.Record.Phase = Prepared
				first, initialErr := decodeServiceStop(initial)
				if initialErr != nil || first.Step != "service-stop-prepared" || first.Instance != instance || !reflect.DeepEqual(first.Base, base) || first.AdmissionClosed {
					t.Fatal("Prepared lost originating service evidence", first, initialErr)
				}
				state, e := decodeServiceStop(durable)
				if e != nil || durable.Record.Phase != Applying || state.Step != "service-quiesce" || state.AdmissionClosed || state.Instance != instance || !c.serviceQuiesced {
					t.Fatal("quiesce preceded durable intent/admission fence", durable.Record.Phase, state, e)
				}
				phases := []Phase{}
				for _, step := range durable.Record.Steps {
					phases = append(phases, step.Phase)
				}
				if !reflect.DeepEqual(phases, []Phase{Prepared, Validated, Applying}) {
					t.Fatal(phases)
				}
				serviceAssertBase(t, c, base)
				cancel()
				return nil
			})
			if err != nil || !made || op.Record.Phase != Committed || calls != 1 {
				t.Fatal("stop", made, op.Record.Phase, calls, err)
			}
			state, err := decodeServiceStop(op)
			if err != nil || !state.AdmissionClosed || state.Instance != instance || state.LeaseReplacedBy != "" {
				t.Fatal(state, err)
			}
			var result struct {
				OK       bool `json:"ok"`
				Warnings []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"warnings"`
			}
			if json.Unmarshal(op.Record.Result, &result) != nil || !result.OK || len(result.Warnings) != 1 || result.Warnings[0].Code != "SERVICE_ADMISSION_CLOSED" || !strings.Contains(result.Warnings[0].Message, "OS exit is not confirmed") || !strings.Contains(result.Warnings[0].Message, "Core forwarding was not stopped") {
				t.Fatal("missing scope warning", string(op.Record.Result))
			}
			serviceAssertBase(t, c, base)
			if !reflect.DeepEqual(protected, serviceProtected(t, p)) {
				t.Fatal("stop changed protected core/config/runtime files")
			}
			replay, newly, e := c.StopService(nil, raw, func() error { t.Fatal("replay quiesced again"); return nil })
			if e != nil || newly || !reflect.DeepEqual(replay, op) {
				t.Fatal("nil-context replay", e)
			}
			next := serviceReopen(t, c, p)
			newID := serviceBind(t, next)
			if newID == instance {
				t.Fatal("new lease reused incarnation")
			}
			before := coordinatorTree(t, p)
			replay, newly, e = next.StopService(nil, raw, nil)
			if e != nil || newly || !reflect.DeepEqual(replay, op) || next.serviceQuiesced || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("old stop affected new service", e)
			}
		})
	}
}
func TestServiceStopAdmissionRejectionsHaveZeroEffects(t *testing.T) {
	for _, kind := range []string{"prebinding", "nil-context", "nil-callback", "cancelled", "stale", "extra", "missing", "null", "numeric", "wrong-operation", "wrong-version", "same-id-changed"} {
		t.Run(kind, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			if kind != "prebinding" {
				serviceBind(t, c)
			}
			base := serviceBase(t, c)
			raw := serviceRequest(t, "rejected-stop", base.Revision)
			ctx := context.Background()
			callback := func() error { t.Fatal("invalid request quiesced service"); return nil }
			want := ErrRequest
			var obj map[string]any
			json.Unmarshal(raw, &obj)
			switch kind {
			case "nil-context":
				ctx = nil
			case "nil-callback":
				callback = nil
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
				want = context.Canceled
			case "stale":
				obj["params"].(map[string]any)["expected_revision"] = "stale-revision"
				want = ErrConflict
			case "extra":
				obj["params"].(map[string]any)["extra"] = true
			case "missing":
				obj["params"] = map[string]any{}
			case "null":
				obj["params"].(map[string]any)["expected_revision"] = nil
			case "numeric":
				obj["params"].(map[string]any)["expected_revision"] = 1
			case "wrong-operation":
				obj["operation"] = "runtime.stop"
			case "wrong-version":
				obj["api_version"] = "v2"
			case "same-id-changed":
				serviceCommitted(t, c, "rejected-stop")
				obj["params"].(map[string]any)["expected_revision"] = "different-revision"
				want = ErrRequestIDReused
			}
			if kind != "prebinding" && kind != "nil-context" && kind != "nil-callback" && kind != "cancelled" {
				raw = planRaw(t, obj)
			}
			before := coordinatorTree(t, p)
			_, made, err := c.StopService(ctx, raw, callback)
			if made || !errors.Is(err, want) {
				t.Fatal(made, err, want)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("rejection changed files")
			}
		})
	}
}
func TestServiceQuiescedBlocksNewWorkAndKeepsExactReplays(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	serviceBind(t, c)
	base := serviceBase(t, c)
	applyRaw := coordinatorRequest(t, "old-apply", coordinatorPlan(t, c, nil), nil)
	apply, made, err := c.AdmitApply(context.Background(), applyRaw)
	if err != nil || !made {
		t.Fatal(err)
	}
	stopRaw := lifecycleRequest(t, "old-runtime-stop", "runtime.stop", base.Revision)
	stopRequest, _ := ParseRequest(stopRaw)
	stop := serviceGeneric(t, c, stopRequest, false)
	startRaw := lifecycleRequest(t, "old-runtime-start", "runtime.start", base.Revision)
	startRequest, _ := ParseRequest(startRaw)
	start := serviceGeneric(t, c, startRequest, false)
	recoverRaw := recoveryRequest(t, "old-recover", apply.Record.Identity.OperationID, base.Revision)
	recoverRequest, _ := ParseRequest(recoverRaw)
	recover := serviceGeneric(t, c, recoverRequest, false)
	service, serviceRaw := serviceCommitted(t, c, "quiesce")
	a := recoveryApplier(t, c)
	before := coordinatorTree(t, p)
	type mutation func(context.Context, []byte) (StoredOperation, bool, error)
	for _, test := range []struct {
		name string
		call mutation
		raw  []byte
		want StoredOperation
	}{{"apply", a.Apply, applyRaw, apply}, {"admit-apply", c.AdmitApply, applyRaw, apply}, {"runtime-stop", a.Stop, stopRaw, stop}, {"runtime-start", a.Start, startRaw, start}, {"ensure-started", a.EnsureStarted, startRaw, start}, {"recover", a.Recover, recoverRaw, recover}} {
		t.Run(test.name, func(t *testing.T) {
			got, made, err := test.call(nil, test.raw)
			if err != nil || made || !reflect.DeepEqual(got, test.want) {
				t.Fatal("exact replay failed", made, err)
			}
			var obj map[string]any
			json.Unmarshal(test.raw, &obj)
			obj["request_id"] = "new-" + test.name
			_, made, err = test.call(context.Background(), planRaw(t, obj))
			if made || !errors.Is(err, config.ErrClosed) {
				t.Fatal("new work passed closed admission", made, err)
			}
		})
	}
	if _, made, err := c.StopService(context.Background(), serviceRequest(t, "new-service-stop", base.Revision), func() error { t.Fatal("new service stop invoked callback"); return nil }); made || !errors.Is(err, config.ErrClosed) {
		t.Fatal("new service stop after quiesce", made, err)
	}
	if watch, err := a.AuthorizeRestart(context.Background(), base.Revision); watch != nil || !errors.Is(err, config.ErrClosed) {
		t.Fatal("restart grant after service stop", err)
	}
	if _, root, err := c.OpenServiceRuntime(); root != nil || !errors.Is(err, config.ErrClosed) {
		t.Fatal(err)
	}
	replay, newly, err := c.StopService(nil, serviceRaw, nil)
	if err != nil || newly || !reflect.DeepEqual(replay, service) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("replay/closed admission changed tree")
	}
	serviceAssertBase(t, c, base)
}
func TestServiceStopEveryDurableCutRecoversUnderNewWriter(t *testing.T) {
	for _, step := range []string{"service-stop-prepared", "service-stop-intent", "service-quiesce", "service-stop-verifying", "service-stop-publishing", "service-stop-committed"} {
		t.Run(step, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			oldID := serviceBind(t, c)
			base := serviceBase(t, c)
			protected := serviceProtected(t, p)
			stored, state, raw := serviceAdmitCut(t, c, "cut-stop")
			calls := 0
			cut := errors.New("durable stop cut")
			stopped, err := stored, error(nil)
			if step == "service-stop-prepared" {
				// Simulate interruption immediately after the first durable admission.
				err = cut
			} else {
				stopped, err = serviceExecute(t, c, stored, state, func() error { calls++; return nil }, func(point string) error {
					if point == step {
						return cut
					}
					return nil
				})
			}
			if !errors.Is(err, cut) {
				t.Fatal(err)
			}
			expectedCalls := 1
			if step == "service-stop-prepared" || step == "service-stop-intent" || step == "service-quiesce" {
				expectedCalls = 0
			}
			if calls != expectedCalls {
				t.Fatal("cut callback order", calls)
			}
			next := serviceReopen(t, c, p)
			newID := serviceBind(t, next)
			if newID == oldID {
				t.Fatal("lease unchanged")
			}
			a := recoveryApplier(t, next)
			recoverRaw := recoveryRequest(t, "recover-old-service", stored.Record.Identity.OperationID, base.Revision)
			call, made, err := a.Recover(context.Background(), recoverRaw)
			if err != nil || !made || call.Record.Phase != Committed {
				t.Fatal("recover", made, call.Record.Phase, err)
			}
			target, err := next.journal.Get(stored.Record.Identity.OperationID)
			if err != nil || target.Record.Phase != Committed || !reflect.DeepEqual(target.Record.Identity, stored.Record.Identity) || !bytes.Equal(target.Record.Request, stored.Record.Request) {
				t.Fatal("recovery changed identity or failed", err)
			}
			evidence, err := decodeServiceStop(target)
			if err != nil || !evidence.AdmissionClosed || evidence.Instance != oldID {
				t.Fatal(evidence, err)
			}
			if stopped.Record.Phase != Committed && evidence.LeaseReplacedBy != newID {
				t.Fatal("recovery lacks replacement lease")
			}
			if next.serviceQuiesced || calls != expectedCalls {
				t.Fatal("old recovery quiesced current service")
			}
			serviceAssertBase(t, next, base)
			if !reflect.DeepEqual(protected, serviceProtected(t, p)) {
				t.Fatal("service recovery changed protected files")
			}
			before := coordinatorTree(t, p)
			replay, newly, err := a.Recover(nil, recoverRaw)
			if err != nil || newly || !reflect.DeepEqual(replay, call) {
				t.Fatal("recovery replay", err)
			}
			original, newly, err := next.StopService(nil, raw, nil)
			if err != nil || newly || !reflect.DeepEqual(original, target) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("stop replay after recovery", err)
			}
		})
	}
}
func TestServiceStopPreparedEvidenceRemainsDegraded(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	serviceBind(t, c)
	base := serviceBase(t, c)
	stored, _, _ := serviceAdmitFixture(t, c, "prepared-without-instance-evidence", true)
	if string(stored.Record.Steps[0].Evidence) != "{}" {
		t.Fatal("prepared premise")
	}
	next := serviceReopen(t, c, p)
	serviceBind(t, next)
	a := recoveryApplier(t, next)
	call, made, err := a.Recover(context.Background(), recoveryRequest(t, "cannot-invent-old-instance", stored.Record.Identity.OperationID, base.Revision))
	if !errors.Is(err, ErrRecoveryRequired) || !made || call.Record.Phase != Degraded {
		t.Fatal(made, call.Record.Phase, err)
	}
	target, err := next.journal.Get(stored.Record.Identity.OperationID)
	if err != nil || target.Record.Phase != Degraded || string(target.Record.Steps[len(target.Record.Steps)-1].Evidence) != "{}" || next.serviceQuiesced {
		t.Fatal("fabricated service evidence", err)
	}
	serviceAssertBase(t, next, base)
}
func TestServiceStopRecoveryRequiresDifferentWriterInstance(t *testing.T) {
	c, _ := coordinatorFixture(t, nil)
	serviceBind(t, c)
	base := serviceBase(t, c)
	stored, state, _ := serviceAdmitCut(t, c, "same-instance-stop")
	cut := errors.New("cut before effect")
	target, err := serviceExecute(t, c, stored, state, func() error { t.Fatal("cut callback"); return nil }, func(step string) error {
		if step == "service-stop-intent" {
			return cut
		}
		return nil
	})
	if !errors.Is(err, cut) {
		t.Fatal(err)
	}
	a := recoveryApplier(t, c)
	call, made, err := a.Recover(context.Background(), recoveryRequest(t, "same-instance-recover", stored.Record.Identity.OperationID, base.Revision))
	if !errors.Is(err, ErrRecoveryRequired) || !made || call.Record.Phase != Degraded {
		t.Fatal(made, call.Record.Phase, err)
	}
	unchanged, err := c.journal.Get(stored.Record.Identity.OperationID)
	if err != nil || !reflect.DeepEqual(target, unchanged) || c.serviceQuiesced {
		t.Fatal("same instance resolved its own stop", err)
	}
}
func TestServiceStopRecoveryLeavesUnrelatedPendingCoreWork(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	serviceBind(t, c)
	base := serviceBase(t, c)
	applyRaw := coordinatorRequest(t, "pending-core-apply", coordinatorPlan(t, c, nil), nil)
	pending, made, err := c.AdmitApply(context.Background(), applyRaw)
	if err != nil || !made {
		t.Fatal(err)
	}
	stopped, _, _ := serviceAdmitCut(t, c, "stop-with-pending-core")
	state := serviceStopEvidence{Version: 1, Kind: "service-stop", Base: base, Instance: c.serviceID}
	cut := errors.New("cut")
	_, err = serviceExecute(t, c, stopped, state, func() error { return nil }, func(step string) error {
		if step == "service-stop-intent" {
			return cut
		}
		return nil
	})
	if !errors.Is(err, cut) {
		t.Fatal(err)
	}
	next := serviceReopen(t, c, p)
	serviceBind(t, next)
	a := recoveryApplier(t, next)
	before := snapshot(t, filepath.Join(p.State, "operations"))
	protected := serviceProtected(t, p)
	call, made, err := a.Recover(context.Background(), recoveryRequest(t, "recover-service-before-core", stopped.Record.Identity.OperationID, base.Revision))
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("recovery cycle", made, call.Record.Phase, err)
	}
	still, err := next.journal.Get(pending.Record.Identity.OperationID)
	path := filepath.Join(p.State, "operations", string(pending.Record.Identity.OperationID)+".json")
	if err != nil || !reflect.DeepEqual(still, pending) || snapshot(t, filepath.Join(p.State, "operations"))[path] != before[path] {
		t.Fatal("service recovery changed unrelated core journal", err)
	}
	if next.serviceQuiesced || !reflect.DeepEqual(protected, serviceProtected(t, p)) {
		t.Fatal("recovery altered current service/core")
	}
	serviceAssertBase(t, next, base)
}
func TestServiceStopFailureNeverFabricatesSuccess(t *testing.T) {
	for _, kind := range []string{"callback", "writer-before-effect", "writer-after-effect"} {
		t.Run(kind, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			serviceBind(t, c)
			base := serviceBase(t, c)
			calls := 0
			var target StoredOperation
			var err error
			if kind == "callback" {
				raw := serviceRequest(t, "callback-error", base.Revision)
				target, _, err = c.StopService(context.Background(), raw, func() error { calls++; return errors.New("admission not verified") })
				if !errors.Is(err, ErrRecoveryRequired) || target.Record.Phase != Degraded || calls != 1 {
					t.Fatal(target.Record.Phase, calls, err)
				}
			} else {
				stored, state, _ := serviceAdmitCut(t, c, "writer-loss")
				point := "service-stop-intent"
				if kind == "writer-after-effect" {
					point = "service-quiesce"
				}
				target, err = serviceExecute(t, c, stored, state, func() error { calls++; return nil }, func(step string) error {
					if step == point {
						return c.store.Close()
					}
					return nil
				})
				expected := 0
				phase := Validated
				if kind == "writer-after-effect" {
					expected = 1
					phase = Applying
				}
				if !errors.Is(err, errRecoveryPersistence) || !errors.Is(err, config.ErrClosed) || calls != expected || target.Record.Phase != phase {
					t.Fatal("lost-writer persistence", target.Record.Phase, calls, err)
				}
			}
			next := serviceReopen(t, c, p)
			persisted, e := next.journal.Get(target.Record.Identity.OperationID)
			if e != nil || persisted.Record.Phase == Committed || !reflect.DeepEqual(persisted, target) {
				t.Fatal("fabricated durable success", e)
			}
			evidence, e := decodeServiceStop(persisted)
			if e != nil || evidence.AdmissionClosed {
				t.Fatal("fabricated verified admission close", e)
			}
			serviceAssertBase(t, next, base)
		})
	}
}
func TestServiceOnlyHistoryAllowsFirstApplyAndRetainsBodies(t *testing.T) {
	for _, recoverStop := range []bool{false, true} {
		name := "committed-stop"
		if recoverStop {
			name = "stop-and-bound-recovery"
		}
		t.Run(name, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			serviceBind(t, c)
			base := serviceBase(t, c)
			var stop, call StoredOperation
			if !recoverStop {
				stop, _ = serviceCommitted(t, c, "terminal-service-stop")
				c = serviceReopen(t, c, p)
				serviceBind(t, c)
			} else {
				stored, state, _ := serviceAdmitCut(t, c, "interrupted-service-stop")
				cut := errors.New("cut")
				_, err := serviceExecute(t, c, stored, state, func() error { return nil }, func(step string) error {
					if step == "service-stop-intent" {
						return cut
					}
					return nil
				})
				if !errors.Is(err, cut) {
					t.Fatal(err)
				}
				c = serviceReopen(t, c, p)
				serviceBind(t, c)
				var made bool
				var errRecover error
				call, made, errRecover = recoveryApplier(t, c).Recover(context.Background(), recoveryRequest(t, "bound-service-recover", stored.Record.Identity.OperationID, base.Revision))
				if errRecover != nil || !made || call.Record.Phase != Committed {
					t.Fatal(errRecover)
				}
				stop, errRecover = c.journal.Get(stored.Record.Identity.OperationID)
				if errRecover != nil {
					t.Fatal(errRecover)
				}
			}
			before := coordinatorTree(t, p)
			if id, err := c.activeRunForApply(base, "start", "/fresh/work"); err != nil || id != nil {
				t.Fatal("proved service-only history blocked first apply", id, err)
			}
			records := []StoredOperation{stop}
			if recoverStop {
				records = append(records, call)
			}
			for _, record := range records {
				if !c.serviceOnlyHistory(record, base) {
					t.Fatal("service-only history not recognized")
				}
				if _, err := c.journal.Compact(record.Record.Identity.OperationID, record.SHA256); !errors.Is(err, ErrOperationIncomplete) {
					t.Fatal("service evidence body compacted", err)
				}
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("history inspection/compaction refusal wrote files")
			}
		})
	}
}
func TestServiceOnlyHistoryRefusesUnprovedHistories(t *testing.T) {
	for _, kind := range []string{"pending-service", "unknown-service-evidence", "core", "subscription", "unbound-recovery"} {
		t.Run(kind, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			serviceBind(t, c)
			base := serviceBase(t, c)
			switch kind {
			case "pending-service":
				serviceAdmitCut(t, c, "pending-stop")
			case "unknown-service-evidence":
				r, _ := ParseRequest(serviceRequest(t, "unproved-stop", base.Revision))
				serviceGeneric(t, c, r, true)
			case "core":
				r, _ := ParseRequest(lifecycleRequest(t, "historical-core-op", "runtime.stop", base.Revision))
				serviceGeneric(t, c, r, true)
			case "subscription":
				r := request(t, "unknown-for-service-only", "v1", "subscription.update", `{}`)
				serviceGeneric(t, c, r, true)
			case "unbound-recovery":
				stop, _ := serviceCommitted(t, c, "proved-stop")
				c = serviceReopen(t, c, p)
				serviceBind(t, c)
				r, _ := ParseRequest(recoveryRequest(t, "unbound-recovery", stop.Record.Identity.OperationID, base.Revision))
				serviceGeneric(t, c, r, true)
			}
			before := coordinatorTree(t, p)
			if id, err := c.activeRunForApply(base, "start", "/fresh/work"); id != nil || !errors.Is(err, ErrRecoveryRequired) {
				t.Fatal("unproved history treated as pristine", id, err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("history rejection wrote files")
			}
		})
	}
}

func TestServiceStopRecoveryPreservesRunningCoreAuthority(t *testing.T) {
	f := lifecycleRunningFixture(t)
	c, p := f.c, f.paths
	old := serviceBind(t, c)
	base := serviceBase(t, c)
	if base.Generation == nil || base.OperationID == nil || base.DesiredRuntime != "running" {
		t.Fatal("fixture lacks running authority")
	}
	protected := serviceProtected(t, p)
	raw := serviceRequest(t, "unverified-stop-running", base.Revision)
	stopped, made, err := c.StopService(context.Background(), raw, func() error { return errors.New("synthetic admission callback unknown") })
	if !errors.Is(err, ErrRecoveryRequired) || !made || stopped.Record.Phase != Degraded {
		t.Fatal(made, err)
	}
	next := serviceReopen(t, c, p)
	newID := serviceBind(t, next)
	call, made, err := recoveryApplier(t, next).Recover(context.Background(), recoveryRequest(t, "recover-running-service", stopped.Record.Identity.OperationID, base.Revision))
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal(made, err)
	}
	target, err := next.journal.Get(stopped.Record.Identity.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeServiceStop(target)
	if err != nil || target.Record.Phase != Committed || state.Instance != old || state.LeaseReplacedBy != newID || !state.AdmissionClosed || next.serviceQuiesced {
		t.Fatal(state, err)
	}
	serviceAssertBase(t, next, base)
	if !reflect.DeepEqual(protected, serviceProtected(t, p)) {
		t.Fatal("service recovery changed core/config/runtime originals")
	}
}
