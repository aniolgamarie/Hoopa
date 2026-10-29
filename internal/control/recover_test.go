//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// Real Store/journal, deliberately synthetic process verifier. These tests prove
// ordering and record bindings, not a process, activation or exit observation.
type recoveryFixture struct {
	c        *Coordinator
	paths    config.Paths
	target   StoredOperation
	run      config.RunID
	applyRaw []byte
}

func recoveryOrderingFixture(t *testing.T, rollback bool) recoveryFixture {
	t.Helper()
	cut := errors.New("fixture cut")
	var c *Coordinator
	var p config.Paths
	var target StoredOperation
	var raw []byte
	var e error
	if rollback {
		owner, paths, stored, state, runtime, request := rollbackOrderingFixture(t)
		c, p, raw = owner, paths, request
		target, e = c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
			if step == "rollback-verified" {
				return cut
			}
			return nil
		})
	} else {
		owner, paths, stored, state, runtime, request := applyOrderingFixture(t)
		c, p, raw = owner, paths, request
		target, e = c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
			if step == "entry-opened" {
				return cut
			}
			return nil
		})
	}
	if !errors.Is(e, cut) {
		t.Fatal("synthetic fixture cut", e)
	}
	state, e := decodeApplyEvidence(target)
	if e != nil {
		t.Fatal(e)
	}
	id := state.TargetRunID
	if rollback {
		id = state.RollbackRunID
	}
	if id == nil {
		t.Fatal("fixture has no intended run")
	}
	recoveryStoppedOriginals(t, c, p, target.Record.Identity.OperationID, state)
	return recoveryFixture{c, p, target, *id, raw}
}
func recoveryRequest(t *testing.T, id string, target config.ID, revision config.Revision) []byte {
	t.Helper()
	return planRaw(t, map[string]any{"api_version": "v1", "request_id": id, "operation": "operation.recover", "params": map[string]any{"operation_id": target, "expected_revision": revision}})
}
func recoveryCurrentRequest(t *testing.T, f recoveryFixture, id string) []byte {
	t.Helper()
	active, e := f.c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	return recoveryRequest(t, id, f.target.Record.Identity.OperationID, active.Revision)
}
func recoveryCall(t *testing.T, c *Coordinator, raw []byte, verify recoveredRunVerifier, cut func(string) error) (StoredOperation, bool, error) {
	t.Helper()
	request, e := ParseRequest(raw)
	if e != nil {
		return StoredOperation{}, false, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recoverBasicLocked(context.Background(), request, verify, cut)
}
func recoveryIdentity() platform.ProcessIdentity {
	return platform.ProcessIdentity{PID: 4242, UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
}
func recoveryVerifier(t *testing.T, f recoveryFixture, calls *int) recoveredRunVerifier {
	t.Helper()
	return func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
		*calls++
		if id != f.run || op != f.target.Record.Identity.OperationID {
			t.Fatal("verifier received wrong run/original operation")
		}
		return recoveryIdentity(), nil
	}
}
func recoveryApplier(t *testing.T, c *Coordinator) *BasicApplier {
	t.Helper()
	a, e := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("recovery/replay called material Source")
		return BasicMaterial{}, ErrPlan
	}, []string{"internal-launch"})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestRecoveryOrderingTargetAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "target"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			f := recoveryOrderingFixture(t, rollback)
			original := f.target.Record.Identity
			protected := map[config.ID]StoredOperation{}
			historyIDs, e := f.c.journal.IDs()
			if e != nil {
				t.Fatal(e)
			}
			metadata := snapshot(t, filepath.Join(f.paths.State, "operations"))
			for _, id := range historyIDs {
				if id != original.OperationID {
					v, e := f.c.journal.Get(id)
					if e != nil {
						t.Fatal(e)
					}
					protected[id] = v
				}
			}
			requestBytes := append([]byte(nil), f.target.Record.Request...)
			beforeIDs, e := f.c.store.ListRunIDs()
			if e != nil {
				t.Fatal(e)
			}
			tmp := filepath.Join(f.paths.State, "operations", ".operation-"+strings.Repeat("a", 48))
			if e := os.WriteFile(tmp, []byte("opaque unpublished bytes"), 0600); e != nil {
				t.Fatal(e)
			}
			calls := 0
			raw := recoveryCurrentRequest(t, f, "explicit-recovery")
			call, made, e := recoveryCall(t, f.c, raw, recoveryVerifier(t, f, &calls), nil)
			if e != nil || !made || call.Record.Phase != Committed || calls != 2 {
				t.Fatal("recover failed", made, e, calls)
			}
			if call.Record.Identity.OperationID == original.OperationID || call.Record.Identity.Operation != "operation.recover" {
				t.Fatal("call identity was not independent")
			}
			if _, e := os.Lstat(tmp); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("explicit maintenance did not remove staging", e)
			}
			target, e := f.c.journal.Get(original.OperationID)
			want := Committed
			if rollback {
				want = RolledBack
			}
			if e != nil || target.Record.Phase != want || !reflect.DeepEqual(target.Record.Identity, original) || !reflect.DeepEqual([]byte(target.Record.Request), requestBytes) {
				t.Fatal("original identity/request changed", e)
			}
			for id, old := range protected {
				got, e := f.c.journal.Get(id)
				path := filepath.Join(f.paths.State, "operations", string(id)+".json")
				if e != nil || !reflect.DeepEqual(got, old) || snapshot(t, filepath.Dir(path))[path] != metadata[path] {
					t.Fatal("unrelated authority changed", e)
				}
			}
			ids, e := f.c.store.ListRunIDs()
			if e != nil || !reflect.DeepEqual(ids, beforeIDs) {
				t.Fatal("recovery fabricated launch intent", e)
			}
			before := coordinatorTree(t, f.paths)
			a := recoveryApplier(t, f.c)
			replay, made, e := a.Recover(nil, raw)
			if e != nil || made || !reflect.DeepEqual(replay, call) {
				t.Fatal("recover same ID did not replay", e)
			}
			applied, made, e := a.Apply(nil, f.applyRaw)
			if e != nil || made || !reflect.DeepEqual(applied, target) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("original apply replay performed effects", e)
			}
			var obj map[string]any
			if e := json.Unmarshal(raw, &obj); e != nil {
				t.Fatal(e)
			}
			obj["params"].(map[string]any)["expected_revision"] = "different-revision"
			if _, made, e := a.Recover(nil, planRaw(t, obj)); made || !errors.Is(e, ErrRequestIDReused) {
				t.Fatal("same ID changed parameters", e)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("conflicting replay wrote")
			}
		})
	}
}
func TestRecoveryInterruptedCallsReopenAndReconcile(t *testing.T) {
	for _, point := range []string{"recover-admitted", "recover-active-published", "recover-target-terminal"} {
		t.Run(point, func(t *testing.T) {
			f := recoveryOrderingFixture(t, false)
			cut := errors.New("recover interruption")
			calls := 0
			raw := recoveryCurrentRequest(t, f, "first-recover")
			first, made, e := recoveryCall(t, f.c, raw, recoveryVerifier(t, f, &calls), func(step string) error {
				if step == point {
					return cut
				}
				return nil
			})
			if !made || !errors.Is(e, cut) || first.Record.Phase != Applying {
				t.Fatal("cut not preserved", made, e, first.Record.Phase)
			}
			before := coordinatorTree(t, f.paths)
			replay, made, e := recoveryApplier(t, f.c).Recover(nil, raw)
			if e != nil || made || !reflect.DeepEqual(replay, first) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("same interrupted ID resumed", e)
			}
			if e := f.c.Close(); e != nil {
				t.Fatal(e)
			}
			reopened, e := OpenCoordinator(f.paths, coordinatorValidator(t), func(context.Context, Plan) (CurrentPlanInputs, error) {
				t.Fatal("recover required current compiler")
				return CurrentPlanInputs{}, nil
			})
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			f.c = reopened
			nextRaw := recoveryCurrentRequest(t, f, "second-recover")
			newCalls := 0
			second, made, e := recoveryCall(t, f.c, nextRaw, recoveryVerifier(t, f, &newCalls), nil)
			wantProofs := 2
			if point == "recover-target-terminal" {
				// An already terminal running target is also proved before the
				// new call is admitted; the two post-admission proofs remain.
				wantProofs = 4
			}
			if e != nil || !made || second.Record.Phase != Committed || newCalls != wantProofs {
				t.Fatal("new explicit recovery failed", made, e, newCalls)
			}
			old, e := f.c.journal.Get(first.Record.Identity.OperationID)
			if e != nil || old.Record.Phase != Committed || !reflect.DeepEqual(old.Record.Identity, first.Record.Identity) {
				t.Fatal("old recover call not completed independently", e)
			}
			target, e := f.c.journal.Get(f.target.Record.Identity.OperationID)
			if e != nil || target.Record.Phase != Committed || !reflect.DeepEqual(target.Record.Identity, f.target.Record.Identity) {
				t.Fatal("original not finalized", e)
			}
		})
	}
}
func TestRecoveryPreflightRejectionsDoNotMaintain(t *testing.T) {
	for _, name := range []string{"stale", "extra", "null", "wrong-operation", "missing-field", "unknown-target", "unrelated-pending", "bad-related", "corrupt-authority", "compact-target"} {
		t.Run(name, func(t *testing.T) {
			f := recoveryOrderingFixture(t, false)
			raw := recoveryCurrentRequest(t, f, "rejected-recover")
			want := error(ErrRequest)
			switch name {
			case "stale":
				var o map[string]any
				json.Unmarshal(raw, &o)
				o["params"].(map[string]any)["expected_revision"] = "stale"
				raw = planRaw(t, o)
				want = ErrConflict
			case "extra":
				var o map[string]any
				json.Unmarshal(raw, &o)
				o["params"].(map[string]any)["extra"] = true
				raw = planRaw(t, o)
			case "null":
				var o map[string]any
				json.Unmarshal(raw, &o)
				o["params"] = nil
				raw = planRaw(t, o)
			case "wrong-operation":
				var o map[string]any
				json.Unmarshal(raw, &o)
				o["operation"] = "config.apply"
				raw = planRaw(t, o)
			case "missing-field":
				var o map[string]any
				json.Unmarshal(raw, &o)
				delete(o["params"].(map[string]any), "operation_id")
				raw = planRaw(t, o)
			case "unknown-target":
				active, _ := f.c.store.ReadActive()
				raw = recoveryRequest(t, "rejected-recover", operationKeyForRecovery("unknown"), active.Revision)
				want = ErrOperationNotFound
			case "unrelated-pending":
				r, _ := ParseRequest(coordinatorRequest(t, "unrelated-pending", coordinatorPlan(t, f.c, nil), nil))
				if _, made, e := f.c.journal.Admit(r, f.target.Record.Identity.Intent, json.RawMessage(`{}`)); e != nil || !made {
					t.Fatal(e)
				}
				want = ErrRecoveryRequired
			case "bad-related":
				r, _ := ParseRequest(recoveryCurrentRequest(t, f, "bad-bound-recover"))
				active, _ := f.c.store.ReadActive()
				i := Intent{BaseRevision: active.Revision, PreviousGeneration: active.Generation, CandidateDigest: strings.Repeat("0", 64), ImpactDigest: hash(r.params)}
				if _, made, e := f.c.journal.Admit(r, i, json.RawMessage(`{}`)); e != nil || !made {
					t.Fatal(e)
				}
				want = ErrRecoveryRequired
			case "corrupt-authority":
				if _, e := f.c.store.CompareAndSwapOperation(operationKeyForRecovery("corrupt"), "", []byte(`{}`)); e != nil {
					t.Fatal(e)
				}
				want = ErrOperationRecord
			case "compact-target":
				calls := 0
				if _, _, e := recoveryCall(t, f.c, recoveryCurrentRequest(t, f, "finish-before-compact"), recoveryVerifier(t, f, &calls), nil); e != nil {
					t.Fatal(e)
				}
				target, _ := f.c.journal.Get(f.target.Record.Identity.OperationID)
				if _, e := f.c.journal.Compact(target.Record.Identity.OperationID, target.SHA256); e != nil {
					t.Fatal(e)
				}
				raw = recoveryCurrentRequest(t, f, "rejected-recover")
				want = ErrRecoveryRequired
			}
			tmp := filepath.Join(f.paths.State, "operations", ".operation-"+strings.Repeat("b", 48))
			if e := os.WriteFile(tmp, []byte("must remain on rejection"), 0600); e != nil {
				t.Fatal(e)
			}
			before := coordinatorTree(t, f.paths)
			calls := 0
			_, made, e := recoveryCall(t, f.c, raw, recoveryVerifier(t, f, &calls), nil)
			if made || !errors.Is(e, want) || calls != 0 || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("preflight maintained/wrote/verified", made, e, want, calls)
			}
		})
	}
}
func operationKeyForRecovery(s string) config.ID { return config.ID("op-" + hash([]byte(s))) }
func TestRecoveryUnknownAndAmbiguousRemainDegraded(t *testing.T) {
	for _, name := range []string{"no-target", "old-exit-unproved", "target-already-stopped", "rollback-link-without-history", "candidate-ambiguity", "rollback-ambiguity", "rollback-exit-unproved", "closed-proof", "identity-changed"} {
		t.Run(name, func(t *testing.T) {
			rollback := strings.HasPrefix(name, "rollback-") && name != "rollback-link-without-history"
			var f recoveryFixture
			if name == "old-exit-unproved" {
				c, p, stored, state, runtime, raw := rollbackOrderingFixture(t)
				cut := errors.New("before replacement activation")
				target, e := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
					if step == "activate-target" {
						return cut
					}
					return nil
				})
				if !errors.Is(e, cut) {
					t.Fatal(e)
				}
				evidence, e := decodeApplyEvidence(target)
				if e != nil || evidence.TargetRunID == nil {
					t.Fatal("replacement fixture", e)
				}
				recoveryStoppedOriginals(t, c, p, target.Record.Identity.OperationID, evidence)
				f = recoveryFixture{c, p, target, *evidence.TargetRunID, raw}
			} else {
				f = recoveryOrderingFixture(t, rollback)
			}
			state, e := decodeApplyEvidence(f.target)
			if e != nil {
				t.Fatal(e)
			}
			switch name {
			case "no-target":
				state.TargetRunID = nil
			case "old-exit-unproved":
				state.OldStopped = false
			case "target-already-stopped":
				state.TargetStopped = true
			case "rollback-link-without-history":
				id := config.RunID("unproved-rollback")
				state.RollbackRunID = &id
			case "rollback-exit-unproved":
				state.TargetStopped = false
			}
			if name == "candidate-ambiguity" || name == "rollback-ambiguity" {
				intent, _, e := f.c.store.ReadRunIntent(f.run)
				if e != nil {
					t.Fatal(e)
				}
				intent.RunID = "ambiguous-additional-run"
				if _, e := f.c.store.PrepareRun(intent); e != nil {
					t.Fatal(e)
				}
			}
			data, _ := encode(state)
			updated, e := f.c.journal.Advance(f.target.Record.Identity.OperationID, f.target.SHA256, f.target.Record.Phase, data, json.RawMessage(f.target.Record.Result))
			if e != nil {
				t.Fatal(e)
			}
			f.target = updated
			calls := 0
			verify := recoveryVerifier(t, f, &calls)
			if name == "closed-proof" {
				verify = func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
					calls++
					return platform.ProcessIdentity{}, errors.New("synthetic closed proof rejected")
				}
			}
			if name == "identity-changed" {
				verify = func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
					calls++
					id := recoveryIdentity()
					id.StartTicks = uint64(calls)
					return id, nil
				}
			}
			call, made, e := recoveryCall(t, f.c, recoveryCurrentRequest(t, f, "unproved-recover"), verify, nil)
			if !made || !errors.Is(e, ErrRecoveryRequired) || call.Record.Phase != Degraded {
				t.Fatal("unproved recovery succeeded", made, e)
			}
			target, e := f.c.journal.Get(f.target.Record.Identity.OperationID)
			if e != nil || target.Record.Phase != Degraded {
				t.Fatal("original uncertainty lost", e)
			}
			if name != "closed-proof" && name != "identity-changed" && calls != 0 {
				t.Fatal("unproved precondition invoked verifier", calls)
			}
			inputs, _ := f.c.current(context.Background(), Plan{})
			active, e := f.c.store.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			if active.Generation != nil {
				previous := string(*active.Generation)
				inputs.Impact = PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, PreviousGeneration: &previous, TargetGeneration: "new-after-recovery", ConnectionEffect: "interrupt", RuntimeAction: "replace"}
			}
			f.c.current = func(context.Context, Plan) (CurrentPlanInputs, error) { return inputs, nil }
			p := coordinatorPlan(t, f.c, func(o map[string]json.RawMessage) { o["impact"] = planRaw(t, inputs.Impact) })
			before := coordinatorTree(t, f.paths)
			if _, made, e := f.c.AdmitApply(context.Background(), coordinatorRequest(t, "blocked-after-degraded", p, nil)); made || !errors.Is(e, ErrRecoveryRequired) {
				t.Fatal("degraded admitted new apply", made, e)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("blocked apply wrote")
			}
		})
	}
}
func TestRecoveryVerifierUncertaintyDoesNotAdvanceTarget(t *testing.T) {
	f := recoveryOrderingFixture(t, false)
	active, _ := f.c.store.ReadActive()
	calls := 0
	call, made, e := recoveryCall(t, f.c, recoveryCurrentRequest(t, f, "uncertain-recover"), func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
		calls++
		return platform.ProcessIdentity{}, config.ErrUncertain
	}, nil)
	if !made || !errors.Is(e, config.ErrUncertain) || call.Record.Phase != Applying || calls != 1 {
		t.Fatal("uncertainty not preserved", made, e)
	}
	target, e := f.c.journal.Get(f.target.Record.Identity.OperationID)
	after, ae := f.c.store.ReadActive()
	if e != nil || ae != nil || target.SHA256 != f.target.SHA256 || !reflect.DeepEqual(active, after) {
		t.Fatal("uncertain proof advanced original/pointer", e, ae)
	}
	// This explicitly tests propagation of a verifier's uncertain Store read;
	// native Store write-poison fault injection remains config's existing tests.
}

// Synthetic STORAGE originals only. Directly materializing a shape-valid birth
// does not call ClaimRunBirth, grant exec, or observe a real PID/exit. The fake
// verifier above supplies explicitly synthetic current-process observations.
func recoveryStoppedOriginals(t *testing.T, c *Coordinator, p config.Paths, op config.ID, state applyEvidence) {
	t.Helper()
	for _, v := range []struct {
		id   *config.RunID
		gate string
	}{{state.OldRunID, "open"}, {func() *config.RunID {
		if state.TargetStopped {
			return state.TargetRunID
		}
		return nil
	}(), "closed"}} {
		if v.id == nil {
			continue
		}
		intent, intentSHA, e := c.store.ReadRunIntent(*v.id)
		if e != nil {
			t.Fatal(e)
		}
		birth := config.RunBirth{Version: 1, RunID: *v.id, IntentSHA256: intentSHA, PID: 4242, UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
		if e := birth.Match(intent, intentSHA); e != nil {
			t.Fatal("synthetic storage birth", e)
		}
		path := filepath.Join(p.State, "runs", hash([]byte(*v.id)), "birth.json")
		encoded, e := json.Marshal(birth)
		if e != nil {
			t.Fatal(e)
		}
		encoded = append(encoded, '\n')
		if e := os.WriteFile(path, encoded, 0600); e != nil {
			t.Fatal(e)
		}
		got, birthSHA, e := c.store.ReadRunBirth(*v.id)
		if e != nil || got != birth {
			t.Fatal("synthetic original birth shape", e)
		}
		reg := config.RunRegistration{Version: 1, RunID: *v.id, OperationID: op, IntentSHA256: intentSHA, BirthSHA256: birthSHA, PID: birth.PID, UID: birth.UID, BootID: birth.BootID, StartTicks: birth.StartTicks, Generation: intent.Generation, ManifestSHA256: intent.ManifestSHA256, BinarySHA256: intent.Binary.SHA256, ConfigSHA256: intent.ConfigSHA256, NonceSHA256: intent.NonceSHA256, Controller: intent.Controller, Gate: v.gate}
		if _, e := c.store.PublishRunRegistration(reg); e != nil {
			t.Fatal("synthetic storage registration", e)
		}
	}
}
func TestRecoveryStoppedOriginalsAndBaseBindings(t *testing.T) {
	for _, name := range []string{"old-birth-missing", "old-intent-missing", "old-reg-missing", "old-reg-hash", "old-gate", "old-origin-ambiguous", "target-birth-missing", "target-reg-hash", "target-gate", "base-stopped-running", "base-running-stopped", "base-running-bad-manifest"} {
		t.Run(name, func(t *testing.T) {
			rollback := name != "base-stopped-running"
			f := recoveryOrderingFixture(t, rollback)
			state, e := decodeApplyEvidence(f.target)
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(name, "base-") {
				switch name {
				case "base-stopped-running":
					state.Base.DesiredRuntime = "running"
				case "base-running-stopped":
					state.Base.DesiredRuntime = "stopped"
				case "base-running-bad-manifest":
					state.Base.ManifestSHA256 = "invalid"
				}
				raw, _ := encode(state)
				target, e := f.c.journal.Advance(f.target.Record.Identity.OperationID, f.target.SHA256, f.target.Record.Phase, raw, json.RawMessage(f.target.Record.Result))
				if e != nil {
					t.Fatal(e)
				}
				f.target = target
			} else {
				id := state.OldRunID
				if strings.HasPrefix(name, "target-") {
					id = state.TargetRunID
				}
				if id == nil {
					t.Fatal("missing synthetic original")
				}
				dir := filepath.Join(f.paths.State, "runs", hash([]byte(*id)))
				regFile := filepath.Join(dir, "registration-"+hash([]byte(f.target.Record.Identity.OperationID))+".json")
				switch name {
				case "old-birth-missing", "target-birth-missing":
					e = os.Remove(filepath.Join(dir, "birth.json"))
				case "old-intent-missing":
					e = os.Remove(filepath.Join(dir, "intent.json"))
				case "old-reg-missing":
					e = os.Remove(regFile)
				case "old-reg-hash", "target-reg-hash", "old-gate", "target-gate":
					reg, _, readErr := f.c.store.ReadControlRegistration(*id, f.target.Record.Identity.OperationID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if strings.HasSuffix(name, "hash") {
						reg.IntentSHA256 = strings.Repeat("0", 64)
					} else if reg.Gate == "closed" {
						reg.Gate = "open"
					} else {
						reg.Gate = "closed"
					}
					data, je := json.Marshal(reg)
					if je != nil {
						t.Fatal(je)
					}
					e = os.WriteFile(regFile, append(data, '\n'), 0600)
				case "old-origin-ambiguous":
					intent, _, re := f.c.store.ReadRunIntent(*id)
					if re != nil {
						t.Fatal(re)
					}
					intent.RunID = "extra-old-origin"
					_, e = f.c.store.PrepareRun(intent)
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			calls := 0
			call, made, e := recoveryCall(t, f.c, recoveryCurrentRequest(t, f, "reject-unbound-originals"), recoveryVerifier(t, f, &calls), nil)
			if !made || !errors.Is(e, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != 0 {
				t.Fatal("unbound original invoked current verifier/committed", made, e, calls)
			}
			target, e := f.c.journal.Get(f.target.Record.Identity.OperationID)
			if e != nil || target.Record.Phase != Degraded {
				t.Fatal("bad original lost degraded", e)
			}
		})
	}
}
