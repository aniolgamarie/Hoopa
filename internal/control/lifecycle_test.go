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
)

// Real Store/journal, synthetic runtime effects. These storage originals and
// exit flags are ordering fixtures, not proof that any real process stopped.
type lifecycleFixture struct {
	t      *testing.T
	c      *Coordinator
	paths  config.Paths
	base   config.ActivePointer
	source config.RunID
	calls  []string
	fail   string
}

func lifecycleRequest(t *testing.T, id, operation string, revision config.Revision) []byte {
	t.Helper()
	params := map[string]any{"expected_revision": revision}
	if operation == "runtime.stop" {
		params["accept_disruption"] = true
	} else {
		params["control_only"] = false
	}
	return planRaw(t, map[string]any{"api_version": "v1", "request_id": id, "operation": operation, "params": params})
}
func lifecycleRunningFixture(t *testing.T) *lifecycleFixture {
	t.Helper()
	c, p, stored, state, runtime, _ := applyOrderingFixture(t)
	done, e := c.executeBasicApply(context.Background(), stored, state, runtime, nil)
	if e != nil || done.Record.Phase != Committed {
		t.Fatal(e)
	}
	base, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	return &lifecycleFixture{t: t, c: c, paths: p, base: base, source: runtime.launch.RunID}
}
func lifecycleStage(t *testing.T, c *Coordinator, op config.ID, step string) lifecycleEvidence {
	t.Helper()
	record, e := c.journal.Get(op)
	if e != nil {
		t.Fatal(e)
	}
	state, e := decodeLifecycleEvidence(record)
	if e != nil || state.Step != step {
		t.Fatal("effect before exact durable lifecycle step", step, state.Step, e)
	}
	return state
}
func (f *lifecycleFixture) stop(_ context.Context, id config.RunID, op config.ID) error {
	state := lifecycleStage(f.t, f.c, op, "stop-original")
	if state.DesiredRuntime != "stopped" || state.SourceStopped || id != f.source {
		f.t.Fatal("stop effect without stopped intent")
	}
	current, e := f.c.store.ReadActive()
	if e != nil || !reflect.DeepEqual(current, state.Base) {
		f.t.Fatal("stop active published before exit", e)
	}
	f.calls = append(f.calls, "stop")
	if f.fail != "" {
		if f.fail == "uncertain" {
			return config.ErrUncertain
		}
		return errors.New("synthetic stop unknown")
	}
	recoveryStoppedOriginals(f.t, f.c, f.paths, op, applyEvidence{OldRunID: &id})
	return nil
}
func (f *lifecycleFixture) admit(t *testing.T, id, mode string) (StoredOperation, lifecycleEvidence, []byte) {
	t.Helper()
	base, e := f.c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	raw := lifecycleRequest(t, id, "runtime."+mode, base.Revision)
	request, e := ParseRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	got, e := f.c.runtimeAdmission(context.Background(), request)
	if e != nil || !reflect.DeepEqual(got, base) {
		t.Fatal(e)
	}
	state := lifecycleEvidence{Version: 1, Mode: mode, DesiredRuntime: "stopped", Base: base, SourceRunID: f.source, ExitOperationID: request.OperationID(), ExitGate: "open"}
	if mode == "start" {
		previous, e := f.c.verifiedStopped(base)
		if e != nil {
			t.Fatal(e)
		}
		state.DesiredRuntime = "running"
		state.SourceStopped = true
		state.SourceRunID = previous.SourceRunID
		state.ExitOperationID = *base.OperationID
	}
	stored, made, e := f.c.admitLifecycle(request, state)
	if e != nil || !made {
		t.Fatal(made, e)
	}
	return stored, state, raw
}
func (f *lifecycleFixture) stopped(t *testing.T) (StoredOperation, []byte) {
	t.Helper()
	stored, state, raw := f.admit(t, "lifecycle-stop", "stop")
	done, e := f.c.executeStop(context.Background(), stored, state, f, nil)
	if e != nil || done.Record.Phase != Committed {
		t.Fatal(e)
	}
	return done, raw
}

// This fake only creates storage originals and records ordered effects. It does
// not claim a process was launched or an entry opened.
type lifecycleStartFixture struct {
	f      *lifecycleFixture
	launch config.RunIntent
	op     config.ID
	calls  []string
	closed bool
	opens  int
}

func (r *lifecycleStartFixture) effect(name, step string) error {
	lifecycleStage(r.f.t, r.f.c, r.op, step)
	r.calls = append(r.calls, name)
	if r.f.fail == name {
		return errors.New("synthetic start unverified")
	}
	return nil
}
func (r *lifecycleStartFixture) clone(id config.RunID, base config.ActivePointer, name string) (closableApplyRuntime, error) {
	if id != r.f.source {
		r.f.t.Fatal("wrong original bundle source")
	}
	if e := r.effect("clone", "start-prepare-workdir"); e != nil {
		return nil, e
	}
	old, _, e := r.f.c.store.ReadRunIntent(id)
	if e != nil {
		return nil, e
	}
	bundle, e := r.f.c.store.ReadBundle(old.Generation)
	if e != nil {
		return nil, e
	}
	if old.Generation != *base.Generation || old.ManifestSHA256 != base.ManifestSHA256 {
		r.f.t.Fatal("clone not bound to stopped bundle")
	}
	path, e := r.f.c.store.PrepareLaunchWorkdir(name, bundle.Files["config.json"])
	if e != nil {
		return nil, e
	}
	old.RunID = "synthetic-start-run"
	old.OperationID = r.op
	old.NonceSHA256 = strings.Repeat("e", 64)
	old.Workdir = path
	old.Arguments = []string{old.Binary.Path, "-d", path, "-f", filepath.Join(path, "config.json")}
	r.launch = old
	return r, nil
}
func (r *lifecycleStartFixture) close() { r.closed = true }
func (r *lifecycleStartFixture) stop(context.Context, config.RunID, config.ID) error {
	r.f.t.Fatal("start must not stop a process")
	return ErrRecoveryRequired
}
func (r *lifecycleStartFixture) prepare(op config.ID) (config.RunID, error) {
	if op != r.op {
		r.f.t.Fatal("wrong start operation")
	}
	if e := r.effect("prepare", "start-prepare-run"); e != nil {
		return "", e
	}
	_, e := r.f.c.store.PrepareRun(r.launch)
	return r.launch.RunID, e
}
func (r *lifecycleStartFixture) check(id config.RunID) {
	if id != r.launch.RunID {
		r.f.t.Fatal("wrong fresh run")
	}
}
func (r *lifecycleStartFixture) start(_ context.Context, id config.RunID) error {
	r.check(id)
	return r.effect("spawn", "start-spawn")
}
func (r *lifecycleStartFixture) verifyClosed(_ context.Context, id config.RunID, op config.ID) error {
	r.check(id)
	if op != r.op {
		r.f.t.Fatal("wrong closed operation")
	}
	return r.effect("closed", "start-verify-closed")
}
func (r *lifecycleStartFixture) activate(_ context.Context, id config.RunID, op config.ID) error {
	r.check(id)
	if op != r.op {
		r.f.t.Fatal("wrong activation operation")
	}
	return r.effect("activate", "start-activate")
}
func (r *lifecycleStartFixture) verifyOpen(_ context.Context, id config.RunID) error {
	r.check(id)
	r.opens++
	step := "start-activate"
	if r.opens == 2 {
		step = "start-publish"
	}
	return r.effect("open", step)
}
func lifecycleSteps(t *testing.T, stored StoredOperation) []string {
	t.Helper()
	out := []string{}
	for _, step := range stored.Record.Steps {
		var state lifecycleEvidence
		if len(step.Evidence) == 0 {
			continue
		}
		if e := json.Unmarshal(step.Evidence, &state); e != nil {
			t.Fatal(e)
		}
		if state.Step != "" {
			out = append(out, state.Step)
		}
	}
	return out
}
func TestLifecycleStopStartOrderingReplay(t *testing.T) {
	f := lifecycleRunningFixture(t)
	original := f.base
	done, raw := f.stopped(t)
	if !reflect.DeepEqual(f.calls, []string{"stop"}) || !reflect.DeepEqual(lifecycleSteps(t, done), []string{"stop-intent-durable", "stop-original", "stop-exit-durable", "stop-publish", "stop-committed"}) {
		t.Fatal("stop ordering", f.calls, lifecycleSteps(t, done))
	}
	stopped, e := f.c.store.ReadActive()
	if e != nil || stopped.Revision == original.Revision || stopped.DesiredRuntime != "stopped" || *stopped.Generation != *original.Generation || stopped.ManifestSHA256 != original.ManifestSHA256 {
		t.Fatal("stop pointer", e)
	}
	a := recoveryApplier(t, f.c)
	before := coordinatorTree(t, f.paths)
	replayed, made, e := a.Stop(nil, raw)
	if e != nil || made || replayed.SHA256 != done.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
		t.Fatal("stop retry changed state", e)
	}
	stored, state, startRaw := f.admit(t, "lifecycle-start", "start")
	runtime := &lifecycleStartFixture{f: f, op: stored.Record.Identity.OperationID}
	started, e := f.c.executeStart(context.Background(), stored, state, runtime.clone, nil)
	if e != nil || started.Record.Phase != Committed {
		t.Fatal(e)
	}
	if !runtime.closed || !reflect.DeepEqual(runtime.calls, []string{"clone", "prepare", "spawn", "closed", "activate", "open", "open"}) {
		t.Fatal("start effects", runtime.calls)
	}
	want := []string{"start-intent-durable", "start-prepare-workdir", "start-prepare-run", "start-spawn", "start-verify-closed", "start-activate", "start-publish", "start-committed"}
	if !reflect.DeepEqual(lifecycleSteps(t, started), want) {
		t.Fatal(lifecycleSteps(t, started))
	}
	active, e := f.c.store.ReadActive()
	if e != nil || active.Revision == stopped.Revision || active.Revision == original.Revision || active.DesiredRuntime != "running" || *active.Generation != *original.Generation || active.ManifestSHA256 != original.ManifestSHA256 || runtime.launch.RunID == f.source || runtime.launch.Workdir == "/fixed/candidate" {
		t.Fatal("start did not preserve bundle with fresh runtime", e)
	}
	for _, original := range []StoredOperation{done, started} {
		before := coordinatorTree(t, f.paths)
		if _, err := f.c.journal.Compact(original.Record.Identity.OperationID, original.SHA256); !errors.Is(err, ErrOperationIncomplete) {
			t.Fatal("lifecycle originals compacted", err)
		}
		if !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
			t.Fatal("compact refusal wrote files")
		}
	}
	before = coordinatorTree(t, f.paths)
	replayed, made, e = a.Start(nil, startRaw)
	if e != nil || made || replayed.SHA256 != started.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
		t.Fatal("start retry changed state", e)
	}
}
func TestLifecycleStrictRejectZeroWrites(t *testing.T) {
	for _, mode := range []string{"stop", "start"} {
		for _, name := range []string{"flag", "missing", "extra", "null", "revision", "cancel", "wrong-state", "id-conflict"} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				f := lifecycleRunningFixture(t)
				if mode == "start" && name != "wrong-state" {
					f.stopped(t)
				}
				base, e := f.c.store.ReadActive()
				if e != nil {
					t.Fatal(e)
				}
				raw := lifecycleRequest(t, "reject-lifecycle", "runtime."+mode, base.Revision)
				var obj map[string]any
				if e := json.Unmarshal(raw, &obj); e != nil {
					t.Fatal(e)
				}
				params := obj["params"].(map[string]any)
				flag := "accept_disruption"
				if mode == "start" {
					flag = "control_only"
				}
				ctx := context.Background()
				want := ErrRequest
				switch name {
				case "flag":
					params[flag] = mode != "stop"
					if mode == "stop" {
						want = ErrConfirmation
					}
				case "missing":
					delete(params, flag)
				case "extra":
					params["unrecognized"] = true
				case "null":
					params[flag] = nil
				case "revision":
					params["expected_revision"] = "stale"
					want = ErrConflict
				case "cancel":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					want = context.Canceled
				case "wrong-state":
					if mode == "stop" {
						f.stopped(t)
						base, _ = f.c.store.ReadActive()
						params["expected_revision"] = base.Revision
					}
					want = ErrRecoveryRequired
				case "id-conflict":
					obj["request_id"] = "ordered-apply"
					want = ErrRequestIDReused
				}
				raw = planRaw(t, obj)
				before := coordinatorTree(t, f.paths)
				a := recoveryApplier(t, f.c)
				var made bool
				if mode == "stop" {
					_, made, e = a.Stop(ctx, raw)
				} else {
					_, made, e = a.Start(ctx, raw)
				}
				if made || !errors.Is(e, want) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
					t.Fatal("reject mutated tree or wrong error", made, e, "want", want)
				}
			})
		}
	}
}
func TestLifecycleCutsDoNotReplayEffects(t *testing.T) {
	for _, mode := range []string{"stop", "start"} {
		cuts := []string{"stop-intent-durable", "stop-exit-observed", "stop-active-published"}
		if mode == "start" {
			cuts = []string{"start-intent-durable", "start-run-intent-published", "spawn-returned-before-registration", "start-entry-opened", "start-active-published"}
		}
		for _, cut := range cuts {
			t.Run(mode+"/"+cut, func(t *testing.T) {
				f := lifecycleRunningFixture(t)
				if mode == "start" {
					f.stopped(t)
				}
				stored, state, raw := f.admit(t, "cut-"+mode, mode)
				boundary := func(step string) error {
					if step == cut {
						return errors.New("synthetic crash cut")
					}
					return nil
				}
				var got StoredOperation
				var e error
				if mode == "stop" {
					got, e = f.c.executeStop(context.Background(), stored, state, f, boundary)
				} else {
					r := &lifecycleStartFixture{f: f, op: stored.Record.Identity.OperationID}
					got, e = f.c.executeStart(context.Background(), stored, state, r.clone, boundary)
				}
				if !errors.Is(e, errRecoveryCut) || terminal(got.Record.Phase) {
					t.Fatal("cut not preserved", e)
				}
				before := coordinatorTree(t, f.paths)
				a := recoveryApplier(t, f.c)
				var replay StoredOperation
				var made bool
				if mode == "stop" {
					replay, made, e = a.Stop(nil, raw)
				} else {
					replay, made, e = a.Start(nil, raw)
				}
				if e != nil || made || replay.SHA256 != got.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
					t.Fatal("replay resumed a cut", e)
				}
			})
		}
	}
}
func TestLifecycleUnknownAndUncertainStop(t *testing.T) {
	for _, failure := range []string{"unknown", "uncertain"} {
		t.Run(failure, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			stored, state, _ := f.admit(t, "failed-stop", "stop")
			f.fail = failure
			got, e := f.c.executeStop(context.Background(), stored, state, f, nil)
			if e == nil {
				t.Fatal("failed stop succeeded")
			}
			want := Degraded
			if failure == "uncertain" {
				want = Applying
				if !errors.Is(e, config.ErrUncertain) {
					t.Fatal(e)
				}
			}
			if got.Record.Phase != want {
				t.Fatal(got.Record.Phase, want)
			}
			active, _ := f.c.store.ReadActive()
			if !reflect.DeepEqual(active, f.base) {
				t.Fatal("unknown exit published stopped")
			}
			if failure == "unknown" {
				raw := lifecycleRequest(t, "blocked-start", "runtime.start", active.Revision)
				before := coordinatorTree(t, f.paths)
				_, made, e := recoveryApplier(t, f.c).Start(context.Background(), raw)
				if made || !errors.Is(e, ErrRecoveryRequired) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestLifecycleStoppedOriginalsRequired(t *testing.T) {
	for _, name := range []string{"birth", "registration", "corrupt-stop", "pristine"} {
		t.Run(name, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			done, _ := f.stopped(t)
			switch name {
			case "birth":
				if e := os.Remove(filepath.Join(f.paths.State, "runs", hash([]byte(f.source)), "birth.json")); e != nil {
					t.Fatal(e)
				}
			case "registration":
				root := filepath.Join(f.paths.State, "runs", hash([]byte(f.source)))
				if e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
					if e != nil {
						return e
					}
					if !d.IsDir() && strings.Contains(p, "registration") {
						return os.Remove(p)
					}
					return nil
				}); e != nil {
					t.Fatal(e)
				}
			case "corrupt-stop":
				if e := os.WriteFile(filepath.Join(f.paths.State, "operations", string(done.Record.Identity.OperationID)+".json"), []byte("corrupt\n"), 0600); e != nil {
					t.Fatal(e)
				}
			case "pristine":
				c, p := coordinatorFixture(t, nil)
				f.c, f.paths = c, p
			}
			base, e := f.c.store.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			raw := lifecycleRequest(t, "missing-proof-start", "runtime.start", base.Revision)
			before := coordinatorTree(t, f.paths)
			_, made, e := recoveryApplier(t, f.c).Start(context.Background(), raw)
			if made || e == nil || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("start accepted missing originals", made, e)
			}
		})
	}
}
