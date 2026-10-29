//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
)

// Runtime outcomes here are synthetic. Real Store/journal/config publications
// prove ordering and replay, not process exit or restoration of actual routing.
type rollbackFixture struct {
	*applyRuntimeFixture
	rollbackCalls  []string
	rollbackFail   string
	restored       *rollbackRunFixture
	candidateProof func(config.RunID, config.ID) (*owned.LaunchExitProof, error)
}

func (r *rollbackFixture) rollbackEffect(name, step string) error {
	r.t.Helper()
	stored, err := r.c.journal.Get(r.op)
	if err != nil {
		r.t.Fatal(err)
	}
	var state applyEvidence
	if json.Unmarshal(stored.Record.Steps[len(stored.Record.Steps)-1].Evidence, &state) != nil || state.Step != step || stored.Record.Phase != RollingBack {
		r.t.Fatalf("effect %s before durable %s", name, step)
	}
	r.rollbackCalls = append(r.rollbackCalls, name)
	if r.rollbackFail == name {
		return errors.New("injected rollback failure")
	}
	return nil
}
func (r *rollbackFixture) stopCandidate(_ context.Context, id config.RunID, op config.ID) (*owned.LaunchExitProof, error) {
	if id != r.launch.RunID || op != r.op {
		r.t.Fatal("wrong candidate")
	}
	if err := r.rollbackEffect("stop-candidate", "rollback-stop-candidate"); err != nil {
		return nil, err
	}
	if r.candidateProof != nil {
		return r.candidateProof(id, op)
	}
	return nil, nil
}
func (r *rollbackFixture) restore(id config.RunID, base config.ActivePointer, name string) (closableApplyRuntime, error) {
	if err := r.rollbackEffect("restore", "rollback-prepare-workdir"); err != nil {
		return nil, err
	}
	old, _, err := r.c.store.ReadRunIntent(id)
	if err != nil {
		return nil, err
	}
	bundle, err := r.c.store.ReadBundle(old.Generation)
	if err != nil {
		return nil, err
	}
	if *base.Generation != old.Generation || base.ManifestSHA256 != bundle.ManifestSHA256 {
		r.t.Fatal("wrong old material")
	}
	path, err := r.c.store.PrepareLaunchWorkdir(name, bundle.Files["config.json"])
	if err != nil {
		return nil, err
	}
	old.RunID, old.OperationID, old.NonceSHA256 = "rollback-run", r.op, strings.Repeat("e", 64)
	old.Workdir = path
	old.Arguments = []string{old.Binary.Path, "-d", path, "-f", filepath.Join(path, "config.json")}
	r.restored = &rollbackRunFixture{parent: r, launch: old}
	return r.restored, nil
}

type rollbackRunFixture struct {
	parent     *rollbackFixture
	launch     config.RunIntent
	openChecks int
	closed     bool
}

func (r *rollbackRunFixture) close() { r.closed = true }
func (r *rollbackRunFixture) stop(context.Context, config.RunID, config.ID) error {
	panic("restore must not stop an old run again")
}
func (r *rollbackRunFixture) prepare(op config.ID) (config.RunID, error) {
	if op != r.launch.OperationID {
		r.parent.t.Fatal("wrong rollback op")
	}
	if err := r.parent.rollbackEffect("prepare", "rollback-prepare-run"); err != nil {
		return "", err
	}
	_, err := r.parent.c.store.PrepareRun(r.launch)
	return r.launch.RunID, err
}
func (r *rollbackRunFixture) check(id config.RunID) {
	if id != r.launch.RunID {
		r.parent.t.Fatal("wrong rollback run")
	}
}
func (r *rollbackRunFixture) start(_ context.Context, id config.RunID) error {
	r.check(id)
	return r.parent.rollbackEffect("start", "rollback-spawn")
}
func (r *rollbackRunFixture) verifyClosed(_ context.Context, id config.RunID, _ config.ID) error {
	r.check(id)
	return r.parent.rollbackEffect("closed", "rollback-started")
}
func (r *rollbackRunFixture) activate(_ context.Context, id config.RunID, _ config.ID) error {
	r.check(id)
	return r.parent.rollbackEffect("activate", "rollback-activate")
}
func (r *rollbackRunFixture) verifyOpen(_ context.Context, id config.RunID) error {
	r.check(id)
	r.openChecks++
	if r.openChecks == 1 {
		return r.parent.rollbackEffect("open-before-publish", "rollback-activate")
	}
	return r.parent.rollbackEffect("open-after-publish", "rollback-verified")
}
func rollbackOrderingFixture(t *testing.T) (*Coordinator, config.Paths, StoredOperation, applyEvidence, *rollbackFixture, []byte) {
	t.Helper()
	c, paths, initial, firstState, firstRuntime, _ := applyOrderingFixture(t)
	if _, err := c.executeBasicApply(context.Background(), initial, firstState, firstRuntime, nil); err != nil {
		t.Fatal(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	previous := string(*base.Generation)
	inputs := coordinatorInputs()
	inputs.Impact.RuntimeAction, inputs.Impact.TargetGeneration, inputs.Impact.PreviousGeneration = "replace", "g3", &previous
	c.current = func(context.Context, Plan) (CurrentPlanInputs, error) { return inputs, nil }
	plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) { obj["impact"] = planRaw(t, inputs.Impact) })
	raw := coordinatorRequest(t, "replacement-to-rollback", plan, nil)
	request, _ := ParseRequest(raw)
	material, err := c.store.ReadBundle(*base.Generation)
	if err != nil {
		t.Fatal(err)
	}
	material.Manifest.Generation = "g3"
	sha, err := c.store.PublishBundle(material.Manifest, material.Files)
	if err != nil {
		t.Fatal(err)
	}
	stored, made, err := c.AdmitApply(context.Background(), raw)
	if err != nil || !made {
		t.Fatal(made, err)
	}
	launch := firstRuntime.launch
	launch.RunID, launch.OperationID, launch.Generation, launch.ManifestSHA256 = "target-run", request.OperationID(), "g3", sha
	launch.Workdir = "/fixed/replacement"
	old := firstRuntime.launch.RunID
	state := applyEvidence{Version: 1, Base: base, OldRunID: &old, TargetManifestSHA256: sha}
	r := &rollbackFixture{applyRuntimeFixture: &applyRuntimeFixture{t: t, c: c, launch: launch, op: request.OperationID(), fail: "activate"}}
	return c, paths, stored, state, r, raw
}

func TestBasicRollbackRestoresWholeOldAuthority(t *testing.T) {
	for _, failure := range []string{"prepare", "start", "closed", "activate", "open-before-publish", "open-after-publish"} {
		t.Run(failure, func(t *testing.T) {
			c, paths, stored, state, runtime, raw := rollbackOrderingFixture(t)
			runtime.fail = failure
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
			if !errors.Is(err, ErrApplyRolledBack) || errors.Is(err, ErrApplyIncomplete) || final.Record.Phase != RolledBack {
				t.Fatal(final.Record.Phase, err)
			}
			wantCalls := []string{"stop-candidate", "restore", "prepare", "start", "closed", "activate", "open-before-publish", "open-after-publish"}
			if failure == "prepare" {
				wantCalls = wantCalls[1:]
			}
			if !reflect.DeepEqual(runtime.rollbackCalls, wantCalls) {
				t.Fatal(runtime.rollbackCalls)
			}
			active, err := c.store.ReadActive()
			want := state.Base
			op := stored.Record.Identity.OperationID
			want.OperationID = &op
			if err != nil || !reflect.DeepEqual(active, want) {
				t.Fatal(active, err)
			}
			if runtime.restored == nil || !runtime.restored.closed {
				t.Fatal("rollback handle leaked")
			}
			var result struct {
				OK    bool `json:"ok"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(final.Record.Result, &result) != nil || result.OK || result.Error.Code != "APPLY_FAILED" || applyCoreRunID(t, final) != "rollback-run" {
				t.Fatal("rollback reported success or wrong run")
			}
			// Later replacement must discover the new old-bundle instance, not the
			// terminated original run. No process observation is claimed here.
			id, err := c.activeRunForApply(active, "replace", "/fixed/next")
			if err != nil || id == nil || *id != "rollback-run" {
				t.Fatal(id, err)
			}
			before := coordinatorTree(t, paths)
			applier, _ := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
				t.Fatal("replay called compiler")
				return BasicMaterial{}, nil
			}, []string{"internal-launch"})
			replayed, made, err := applier.Apply(nil, raw)
			if err != nil || made || replayed.SHA256 != final.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("rollback replay performed effects", made, err)
			}
		})
	}
}

func TestBasicRollbackFailureStaysDegraded(t *testing.T) {
	for _, point := range []string{"stop-candidate", "restore", "prepare", "start", "closed", "activate", "open-before-publish", "open-after-publish"} {
		t.Run(point, func(t *testing.T) {
			c, _, stored, state, runtime, _ := rollbackOrderingFixture(t)
			runtime.rollbackFail = point
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
			if !errors.Is(err, ErrApplyIncomplete) || final.Record.Phase != Degraded || runtime.rollbackCalls[len(runtime.rollbackCalls)-1] != point {
				t.Fatal(final.Record.Phase, runtime.rollbackCalls, err)
			}
			if runtime.restored != nil && !runtime.restored.closed {
				t.Fatal("rollback handle leaked")
			}
			var evidence applyEvidence
			if json.Unmarshal(final.Record.Steps[len(final.Record.Steps)-1].Evidence, &evidence) != nil || evidence.Step != "rollback-unverified" || evidence.TargetStopped != (point != "stop-candidate") {
				t.Fatal("bad degraded evidence")
			}
			// Degraded remains an admission blocker even if the active file still
			// names the old bundle or already names the restored new instance.
			inputs, _ := c.current(context.Background(), Plan{})
			_, _, err = c.AdmitApply(context.Background(), coordinatorRequest(t, "blocked-new-write", coordinatorPlan(t, c, func(obj map[string]json.RawMessage) { obj["impact"] = planRaw(t, inputs.Impact) }), nil))
			if !errors.Is(err, ErrRecoveryRequired) {
				t.Fatal("degraded accepted a new request")
			}
		})
	}
}

func TestBasicRollbackRefusesUnrelatedActivePublication(t *testing.T) {
	c, _, stored, state, runtime, _ := rollbackOrderingFixture(t)
	unrelated := state.Base
	unrelated.Revision = "unrelated-revision"
	final, err := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
		if step == "rollback-verified" {
			return c.store.PublishActive(unrelated)
		}
		return nil
	})
	if !errors.Is(err, ErrConflict) || final.Record.Phase != Degraded {
		t.Fatal(final.Record.Phase, err)
	}
	active, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(active, unrelated) || runtime.restored.openChecks != 1 {
		t.Fatal("overwrote unrelated authority", active, err)
	}
}

func TestBasicApplyResultMatchesMutationReceiptFields(t *testing.T) {
	_, _, stored, state, _, _ := rollbackOrderingFixture(t)
	for _, phase := range []Phase{Validated, Applying, Verifying, Publishing, Committed, RollingBack, RolledBack, Degraded} {
		t.Run(string(phase), func(t *testing.T) {
			outcome := "pending"
			if terminal(phase) || phase == Degraded {
				outcome = string(phase)
			}
			raw, err := applyResult(stored, state, phase, outcome)
			if err != nil {
				t.Fatal(err)
			}
			obj, parseErr := planObject(raw)
			if parseErr != nil || !planFields(raw, "api_version", "request_id", "ok", "data", "error", "warnings") {
				t.Fatal("response fields violate API v1")
			}
			if phase == RolledBack || phase == Degraded {
				if string(obj["ok"]) != "false" || string(obj["data"]) != "null" || !planFields(obj["error"], "code", "message", "retryable", "details") {
					t.Fatal("missing failure fields")
				}
				var failure map[string]any
				json.Unmarshal(obj["error"], &failure)
				code := "APPLY_FAILED"
				if phase == Degraded {
					code = "RECOVERY_REQUIRED"
				}
				if failure["code"] != code || failure["retryable"] != false {
					t.Fatal(failure)
				}
				details := failure["details"].(map[string]any)
				if details["operation_id"] != string(stored.Record.Identity.OperationID) || details["reason"] != string(phase) {
					t.Fatal(details)
				}
			} else if string(obj["error"]) != "null" || string(obj["ok"]) != "true" {
				t.Fatal("pending/success envelope")
			} else {
				if !planFields(obj["data"], "operation_id", "phase", "base_revision", "target_revision", "outcome") {
					t.Fatal("receipt fields")
				}
				var data map[string]any
				json.Unmarshal(obj["data"], &data)
				if data["outcome"] != outcome {
					t.Fatal(data)
				}
			}
		})
	}
}

func TestBasicRollbackInterruptionDoesNotResumeOnReplay(t *testing.T) {
	for _, point := range []string{"rollback-stop-candidate", "rollback-candidate-exited", "rollback-prepare-workdir", "rollback-prepare-run", "rollback-intent-published", "rollback-spawn", "rollback-started", "rollback-activate", "rollback-verified", "rollback-active-published", "rollback-committed"} {
		t.Run(point, func(t *testing.T) {
			c, paths, stored, state, runtime, raw := rollbackOrderingFixture(t)
			cut := errors.New("crash boundary")
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
				if step == point {
					return cut
				}
				return nil
			})
			if !errors.Is(err, cut) {
				t.Fatal("cut not reached", err)
			}
			if runtime.restored != nil && !runtime.restored.closed {
				t.Fatal("handle not released")
			}
			before := coordinatorTree(t, paths)
			request, _ := ParseRequest(raw)
			replay, err := c.journal.Lookup(request)
			if err != nil || replay.SHA256 != final.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("replay changed state", err)
			}
			if point == "rollback-intent-published" {
				intent, _, err := c.store.ReadRunIntent("rollback-run")
				if err != nil || intent.OperationID != runtime.op || intent.Generation != *state.Base.Generation {
					t.Fatal("lost durable rollback linkage", err)
				}
			}
		})
	}
}
