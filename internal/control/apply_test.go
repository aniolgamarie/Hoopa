//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

// This is an ordering oracle using real Store/journal publications and synthetic
// runtime observations. It does not prove process ownership, routing or readiness.
type applyRuntimeFixture struct {
	t          *testing.T
	c          *Coordinator
	launch     config.RunIntent
	op         config.ID
	calls      []string
	fail       string
	openChecks int
}

func (r *applyRuntimeFixture) effect(name, step string) error {
	r.t.Helper()
	stored, err := r.c.journal.Get(r.op)
	if err != nil {
		r.t.Fatal(err)
	}
	var state applyEvidence
	if json.Unmarshal(stored.Record.Steps[len(stored.Record.Steps)-1].Evidence, &state) != nil || state.Step != step {
		r.t.Fatalf("%s preceded durable %s: %+v", name, step, state)
	}
	r.calls = append(r.calls, name)
	if r.fail == name {
		return errors.New("injected runtime failure")
	}
	return nil
}
func (r *applyRuntimeFixture) stop(_ context.Context, _ config.RunID, op config.ID) error {
	if op != r.op {
		r.t.Fatal("wrong stop operation")
	}
	return r.effect("stop", "stop-old")
}
func (r *applyRuntimeFixture) prepare(op config.ID) (config.RunID, error) {
	if op != r.op {
		r.t.Fatal("wrong prepare operation")
	}
	if err := r.effect("prepare", "prepare-target"); err != nil {
		return "", err
	}
	_, err := r.c.store.PrepareRun(r.launch)
	return r.launch.RunID, err
}
func (r *applyRuntimeFixture) start(_ context.Context, id config.RunID) error {
	if id != r.launch.RunID {
		r.t.Fatal("wrong start run")
	}
	return r.effect("start", "spawn-target")
}
func (r *applyRuntimeFixture) verifyClosed(_ context.Context, id config.RunID, _ config.ID) error {
	if id != r.launch.RunID {
		r.t.Fatal("wrong closed run")
	}
	return r.effect("closed", "target-started")
}
func (r *applyRuntimeFixture) activate(_ context.Context, id config.RunID, _ config.ID) error {
	if id != r.launch.RunID {
		r.t.Fatal("wrong activate run")
	}
	return r.effect("activate", "activate-target")
}
func (r *applyRuntimeFixture) verifyOpen(_ context.Context, id config.RunID) error {
	if id != r.launch.RunID {
		r.t.Fatal("wrong open run")
	}
	r.openChecks++
	step, name := "activate-target", "open-before-publish"
	if r.openChecks == 2 {
		step, name = "target-verified", "open-after-publish"
	}
	return r.effect(name, step)
}

func applyOrderingFixture(t *testing.T) (*Coordinator, config.Paths, StoredOperation, applyEvidence, *applyRuntimeFixture, []byte) {
	t.Helper()
	c, paths := coordinatorFixture(t, nil)
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	plan := coordinatorPlan(t, c, nil)
	raw := coordinatorRequest(t, "ordered-apply", plan, nil)
	request, _ := ParseRequest(raw)
	data := []byte(`{"synthetic":"not a core configuration"}`)
	binary := config.BinaryReference{Path: "/fixed/synthetic-core", Version: "test", SHA256: strings.Repeat("b", 64)}
	manifest := config.BundleManifest{Version: 1, Generation: "g2", CompilerVersion: "test", CapabilitiesDigest: strings.Repeat("a", 64), Binaries: []config.BinaryReference{binary}, Files: []config.BundleFile{{Kind: "generated_config", Name: "config.json", Bytes: int64(len(data)), SHA256: hash(data)}}}
	sha, err := c.store.PublishBundle(manifest, map[string][]byte{"config.json": data})
	if err != nil {
		t.Fatal(err)
	}
	stored, made, err := c.AdmitApply(context.Background(), raw)
	if err != nil || !made {
		t.Fatal(made, err)
	}
	state := applyEvidence{Version: 1, Base: base, TargetManifestSHA256: sha, OldStopped: true}
	intent := config.RunIntent{Version: 1, RunID: "synthetic-run", OperationID: request.OperationID(), Generation: "g2", ManifestSHA256: sha, Binary: binary, Workdir: "/fixed/candidate", Arguments: []string{binary.Path}, Controller: "127.0.0.1:45678", ConfigSHA256: hash(data), NonceSHA256: strings.Repeat("c", 64)}
	runtime := &applyRuntimeFixture{t: t, c: c, launch: intent, op: request.OperationID()}
	return c, paths, stored, state, runtime, raw
}

func TestBasicApplyOrderedPublicationAndReplay(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "start"
		if replace {
			name = "replace-ordering"
		}
		t.Run(name, func(t *testing.T) {
			c, paths, stored, state, runtime, raw := applyOrderingFixture(t)
			if replace {
				old := config.RunID("synthetic-old")
				state.OldRunID = &old
				state.OldStopped = false
			}
			c.mu.Lock()
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
			c.mu.Unlock()
			if err != nil || final.Record.Phase != Committed {
				t.Fatal(final.Record.Phase, err)
			}
			want := []string{"prepare", "start", "closed", "activate", "open-before-publish", "open-after-publish"}
			if replace {
				want = append([]string{"stop"}, want...)
			}
			if !reflect.DeepEqual(runtime.calls, want) {
				t.Fatal(runtime.calls)
			}
			active, err := c.store.ReadActive()
			if err != nil || active.Revision != *stored.Record.Identity.Intent.TargetRevision || *active.Generation != "g2" || *active.OperationID != runtime.op || active.DesiredRuntime != "running" {
				t.Fatal(active, err)
			}
			ids, err := c.store.ListRunIDs()
			if err != nil || !reflect.DeepEqual(ids, []config.RunID{runtime.launch.RunID}) {
				t.Fatal(ids, err)
			}
			before := coordinatorTree(t, paths)
			sourceCalls := 0
			applier, err := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
				sourceCalls++
				return BasicMaterial{}, errors.New("unavailable")
			}, []string{"internal-launch"})
			if err != nil {
				t.Fatal(err)
			}
			// Even nil context and an unavailable compiler cannot repeat an accepted request.
			replayed, made, err := applier.Apply(nil, raw)
			if err != nil || made || sourceCalls != 0 || replayed.SHA256 != final.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("replay performed work", made, err, sourceCalls)
			}
		})
	}
}

func TestBasicApplyRuntimeFailurePreservesPendingAuthority(t *testing.T) {
	for _, point := range []string{"stop", "prepare", "start", "closed", "activate", "open-before-publish", "open-after-publish"} {
		t.Run(point, func(t *testing.T) {
			c, _, stored, state, runtime, raw := applyOrderingFixture(t)
			old := config.RunID("synthetic-old")
			state.OldRunID = &old
			state.OldStopped = false
			runtime.fail = point
			c.mu.Lock()
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
			c.mu.Unlock()
			if !errors.Is(err, ErrApplyIncomplete) || final.Record.Phase != Degraded {
				t.Fatal(final.Record.Phase, err)
			}
			if runtime.calls[len(runtime.calls)-1] != point {
				t.Fatal("effect continued after failure", runtime.calls)
			}
			active, err := c.store.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			if point == "open-after-publish" {
				if active.Revision != *stored.Record.Identity.Intent.TargetRevision {
					t.Fatal(active)
				}
			} else if !reflect.DeepEqual(active, state.Base) {
				t.Fatal("early active", active)
			}
			request, _ := ParseRequest(raw)
			replay, err := c.journal.Lookup(request)
			if err != nil || replay.SHA256 != final.SHA256 {
				t.Fatal("failure not retained", err)
			}
		})
	}
}

func TestBasicApplyInterruptionNeverContinuesEffects(t *testing.T) {
	for _, point := range []string{"candidate-validated", "stop-old", "old-stop-observed", "prepare-target", "target-intent-published", "spawn-target", "spawn-returned-before-registration", "target-started", "activate-target", "entry-opened", "target-verified", "active-published", "target-committed"} {
		t.Run(point, func(t *testing.T) {
			c, paths, stored, state, runtime, raw := applyOrderingFixture(t)
			old := config.RunID("synthetic-old")
			state.OldRunID = &old
			state.OldStopped = false
			cutErr := errors.New("injected caller interruption")
			c.mu.Lock()
			final, err := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
				if step == point {
					return cutErr
				}
				return nil
			})
			c.mu.Unlock()
			if !errors.Is(err, cutErr) {
				t.Fatal("cut not reached", err)
			}
			before := coordinatorTree(t, paths)
			applier, _ := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
				t.Fatal("replay called compiler")
				return BasicMaterial{}, nil
			}, []string{"internal-launch"})
			replay, made, err := applier.Apply(context.Background(), raw)
			if err != nil || made || replay.SHA256 != final.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("interrupted retry wrote", made, err)
			}
			if point == "target-intent-published" {
				ids, err := c.store.ListRunIDs()
				if err != nil || len(ids) != 1 {
					t.Fatal("lost interrupted intent", ids, err)
				}
				var ev applyEvidence
				json.Unmarshal(final.Record.Steps[len(final.Record.Steps)-1].Evidence, &ev)
				if ev.TargetRunID != nil {
					t.Fatal("cut did not hit linkage gap")
				}
				intent, _, err := c.store.ReadRunIntent(ids[0])
				if err != nil || intent.OperationID != runtime.op {
					t.Fatal("intent not linked to original operation", err)
				}
			}
		})
	}
}

func TestBasicApplyStaleRequestDoesNotResolveCandidate(t *testing.T) {
	c, paths := coordinatorFixture(t, nil)
	plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) { obj["base_revision"] = json.RawMessage(`"stale"`) })
	before := coordinatorTree(t, paths)
	applier, _ := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("stale request resolved material")
		return BasicMaterial{}, nil
	}, []string{"internal-launch"})
	_, made, err := applier.Apply(context.Background(), coordinatorRequest(t, "stale-apply", plan, nil))
	if made || !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal(made, err)
	}
}

func TestBasicApplyActiveRunBindingAndAmbiguity(t *testing.T) {
	c, paths, stored, state, runtime, _ := applyOrderingFixture(t)
	c.mu.Lock()
	final, err := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, paths)
	id, err := c.activeRunForApply(base, "replace", "/fixed/new-work")
	if err != nil || id == nil || *id != runtime.launch.RunID {
		t.Fatal("unique immutable linkage", id, err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("resolution wrote authority")
	}
	// Discovery alone deliberately does not claim a process exists: the synthetic
	// run has no birth. The concrete runtime must independently Register it.
	for _, scenario := range []string{"same-workdir", "wrong-operation", "wrong-manifest", "missing-operation", "wrong-revision", "stopped-history", "wrong-action"} {
		t.Run(scenario, func(t *testing.T) {
			copy := base
			action, work := "replace", "/fixed/new-work"
			switch scenario {
			case "same-workdir":
				work = runtime.launch.Workdir
			case "wrong-operation":
				other := config.ID("op-" + strings.Repeat("0", 64))
				copy.OperationID = &other
			case "wrong-manifest":
				copy.ManifestSHA256 = strings.Repeat("0", 64)
			case "missing-operation":
				copy.OperationID = nil
			case "wrong-revision":
				copy.Revision = "unbound-revision"
			case "stopped-history":
				copy.DesiredRuntime = "stopped"
				action = "start"
			case "wrong-action":
				action = "start"
			}
			before := coordinatorTree(t, paths)
			if id, err := c.activeRunForApply(copy, action, work); err == nil || id != nil {
				t.Fatal("incomplete binding accepted", id, err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("reject wrote authority")
			}
		})
	}
	if _, err := c.journal.Compact(runtime.op, final.SHA256); err != nil {
		t.Fatal(err)
	}
	if id, err := c.activeRunForApply(base, "replace", "/fixed/new-work"); err != nil || id == nil {
		t.Fatal("compaction broke intent linkage", err)
	}
	duplicate := runtime.launch
	duplicate.RunID = "another-run-same-operation"
	if _, err := c.store.PrepareRun(duplicate); err != nil {
		t.Fatal(err)
	}
	before = coordinatorTree(t, paths)
	if id, err := c.activeRunForApply(base, "replace", "/fixed/new-work"); !errors.Is(err, ErrRecoveryRequired) || id != nil {
		t.Fatal("ambiguous run accepted", id, err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("ambiguity repaired history")
	}
}

func TestBasicApplyClosedWriterStopsBeforeNextEffect(t *testing.T) {
	c, _, stored, state, runtime, _ := applyOrderingFixture(t)
	old := config.RunID("synthetic-old")
	state.OldRunID = &old
	state.OldStopped = false
	c.mu.Lock()
	final, err := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
		if step == "candidate-validated" {
			return c.store.Close()
		}
		return nil
	})
	c.mu.Unlock()
	if !errors.Is(err, config.ErrClosed) || final.Record.Phase != Validated || len(runtime.calls) != 0 {
		t.Fatal("effects followed lost writer", final.Record.Phase, runtime.calls, err)
	}
}

func TestBasicApplyPreparedReplayAndPristineGuard(t *testing.T) {
	c, paths, stored, state, _, raw := applyOrderingFixture(t)
	before := coordinatorTree(t, paths)
	applier, _ := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("prepared replay compiled candidate")
		return BasicMaterial{}, nil
	}, []string{"internal-launch"})
	got, made, err := applier.Apply(nil, raw)
	if err != nil || made || got.SHA256 != stored.SHA256 || got.Record.Phase != Prepared || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("prepared replay ran effects", made, err)
	}
	if id, err := c.activeRunForApply(state.Base, "start", "/fresh/work"); !errors.Is(err, ErrRecoveryRequired) || id != nil {
		t.Fatal("operation history was treated as pristine", id, err)
	}
	fresh, _ := coordinatorFixture(t, nil)
	base, err := fresh.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	if id, err := fresh.activeRunForApply(base, "start", "/fresh/work"); err != nil || id != nil {
		t.Fatal("pristine rejected", id, err)
	}
	op := config.ID("op-" + strings.Repeat("0", 64))
	base.OperationID = &op
	if id, err := fresh.activeRunForApply(base, "start", "/fresh/work"); !errors.Is(err, ErrRecoveryRequired) || id != nil {
		t.Fatal("stopped authority link ignored", id, err)
	}
}
