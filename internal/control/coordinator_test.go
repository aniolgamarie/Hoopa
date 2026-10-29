//go:build linux

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
)

// These inputs and validator are synthetic, immutable codec fixtures. They are
// not a real compiler, source reader, controller or an authorization to apply.
func coordinatorInputs() CurrentPlanInputs {
	return CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: strings.Repeat("a", 64), Impact: PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: "g2", ConnectionEffect: "unknown", RuntimeAction: "start"}, Inputs: []PlanInput{
		{Kind: "provider", ID: "subscription", SHA256: hash([]byte("provider"))},
		{Kind: "core_binary", ID: "mihomo", SHA256: strings.Repeat("b", 64)},
		{Kind: "capabilities", ID: "basic", SHA256: strings.Repeat("a", 64)},
		{Kind: "rule_asset", ID: "rules", SHA256: hash([]byte("rules"))},
	}}
}
func coordinatorValidator(t *testing.T) IntentValidator {
	obj := planFixture(t)
	obj["inputs"] = planRaw(t, coordinatorInputs().Inputs)
	return planFixtureValidator(t, obj)
}
func coordinatorFixture(t *testing.T, reader CurrentInputs) (*Coordinator, config.Paths) {
	t.Helper()
	p, e := config.ResolvePaths(t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if reader == nil {
		reader = func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil }
	}
	c, e := OpenCoordinator(p, coordinatorValidator(t), reader)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := c.Close(); e != nil {
			t.Error(e)
		}
	})
	return c, p
}
func coordinatorPlan(t *testing.T, c *Coordinator, edit func(map[string]json.RawMessage)) Plan {
	t.Helper()
	a, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	obj := planFixture(t)
	obj["base_revision"] = planRaw(t, a.Revision)
	obj["inputs"] = planRaw(t, coordinatorInputs().Inputs)
	obj["blockers"] = json.RawMessage(`[]`)
	impact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: "g2", ConnectionEffect: "unknown", RuntimeAction: "start"}
	if a.Generation != nil {
		old := string(*a.Generation)
		impact.PreviousGeneration = &old
	}
	obj["impact"] = planRaw(t, impact)
	if edit != nil {
		edit(obj)
	}
	plan, e := BuildPlan(planRaw(t, obj), coordinatorValidator(t))
	if e != nil {
		t.Fatal(e)
	}
	return plan
}
func coordinatorRequest(t *testing.T, id string, p Plan, edit func(map[string]any)) []byte {
	t.Helper()
	params := map[string]any{"plan": json.RawMessage(p.Bytes()), "confirmed_plan_digest": p.Digest(), "accept_disruption": true}
	if edit != nil {
		edit(params)
	}
	return planRaw(t, map[string]any{"api_version": "v1", "request_id": id, "operation": "config.apply", "params": params})
}
func coordinatorTree(t *testing.T, p config.Paths) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{p.Config, p.State, p.Cache, p.Runtime} {
		if _, e := os.Lstat(root); os.IsNotExist(e) {
			out[root] = "absent"
			continue
		}
		for path, value := range snapshot(t, root) {
			out[path] = value
		}
	}
	return out
}
func coordinatorReject(t *testing.T, c *Coordinator, p config.Paths, ctx context.Context, raw []byte, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	_, made, e := c.AdmitApply(ctx, raw)
	if !errors.Is(e, want) || made {
		t.Fatalf("rejection: made=%v err=%v want=%v", made, e, want)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("rejection changed tree bytes/mode/size/mtime")
	}
}
func TestCoordinatorConstructorAndPrepared(t *testing.T) {
	t.Run("missing-dependencies-zero-creation", func(t *testing.T) {
		for _, which := range []string{"validator", "reader"} {
			t.Run(which, func(t *testing.T) {
				home := t.TempDir()
				p, e := config.ResolvePaths(home, nil)
				if e != nil {
					t.Fatal(e)
				}
				before := snapshot(t, home)
				validator := coordinatorValidator(t)
				reader := CurrentInputs(func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
				if which == "validator" {
					validator = nil
				} else {
					reader = nil
				}
				c, e := OpenCoordinator(p, validator, reader)
				if c != nil {
					c.Close()
					t.Fatal("unexpected coordinator")
				}
				if !errors.Is(e, ErrRequest) || !reflect.DeepEqual(before, snapshot(t, home)) {
					t.Fatal("missing dependency created state", e)
				}
			})
		}
	})
	t.Run("prepared-only", func(t *testing.T) {
		c, p := coordinatorFixture(t, nil)
		before, e := c.store.ReadActive()
		if e != nil {
			t.Fatal(e)
		}
		plan := coordinatorPlan(t, c, nil)
		raw := coordinatorRequest(t, "new", plan, nil)
		got, made, e := c.AdmitApply(context.Background(), raw)
		if e != nil || !made {
			t.Fatal(made, e)
		}
		if got.Record.Phase != Prepared {
			t.Fatal("not prepared")
		}
		var result struct {
			APIVersion string            `json:"api_version"`
			RequestID  string            `json:"request_id"`
			Error      json.RawMessage   `json:"error"`
			Warnings   []json.RawMessage `json:"warnings"`
			OK         bool              `json:"ok"`
			Data       struct {
				Outcome        string          `json:"outcome"`
				Phase          Phase           `json:"phase"`
				OperationID    config.ID       `json:"operation_id"`
				BaseRevision   config.Revision `json:"base_revision"`
				TargetRevision config.Revision `json:"target_revision"`
			} `json:"data"`
		}
		if e := json.Unmarshal(got.Record.Result, &result); e != nil || result.APIVersion != "v1" || result.RequestID != "new" || string(result.Error) != "null" || result.Warnings == nil || !result.OK || result.Data.Outcome != "pending" || result.Data.Phase != Prepared || result.Data.OperationID != got.Record.Identity.OperationID || result.Data.BaseRevision != before.Revision || got.Record.Identity.Intent.TargetRevision == nil || result.Data.TargetRevision != *got.Record.Identity.Intent.TargetRevision {
			t.Fatal("not pending", e)
		}
		id := got.Record.Identity
		if id.Intent.TargetRevision == nil || *id.Intent.TargetRevision == before.Revision || id.Intent.CandidateDigest != plan.Digest() {
			t.Fatal("missing revision/plan binding")
		}
		impact, e := config.CanonicalJSON(planRaw(t, plan.Document().Impact))
		if e != nil || id.Intent.ImpactDigest != hash(impact) {
			t.Fatal("impact binding", e)
		}
		after, e := c.store.ReadActive()
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("admission changed active", e)
		}
		ids, e := c.journal.IDs()
		if e != nil || len(ids) != 1 {
			t.Fatal(ids, e)
		}
		entries, e := os.ReadDir(filepath.Join(p.State, "bundles"))
		if e != nil || len(entries) != 0 {
			t.Fatal("created candidate", e)
		}
	})
}

func TestCoordinatorRequestAndPlanRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want error
	}{
		{"missing-plan", func(p map[string]any) { delete(p, "plan") }, ErrRequest},
		{"missing-confirmation", func(p map[string]any) { delete(p, "confirmed_plan_digest") }, ErrRequest},
		{"missing-disruption", func(p map[string]any) { delete(p, "accept_disruption") }, ErrRequest},
		{"unknown-param", func(p map[string]any) { p["extra"] = true }, ErrRequest},
		{"case-spelling", func(p map[string]any) { p["Plan"] = p["plan"]; delete(p, "plan") }, ErrRequest},
		{"null-plan", func(p map[string]any) { p["plan"] = nil }, ErrRequest},
		{"null-confirmation", func(p map[string]any) { p["confirmed_plan_digest"] = nil }, ErrRequest},
		{"null-disruption", func(p map[string]any) { p["accept_disruption"] = nil }, ErrRequest},
		{"wrong-plan-type", func(p map[string]any) { p["plan"] = []any{} }, ErrRequest},
		{"wrong-disruption-type", func(p map[string]any) { p["accept_disruption"] = "true" }, ErrRequest},
		{"confirmation", func(p map[string]any) { p["confirmed_plan_digest"] = strings.Repeat("0", 64) }, ErrConfirmation},
		{"disruption", func(p map[string]any) { p["accept_disruption"] = false }, ErrConfirmation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			plan := coordinatorPlan(t, c, nil)
			coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, tc.name, plan, tc.edit), tc.want)
		})
	}
	for _, name := range []string{"version", "operation", "unknown-top", "malformed-plan"} {
		t.Run(name, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			plan := coordinatorPlan(t, c, nil)
			raw := coordinatorRequest(t, name, plan, nil)
			var env map[string]any
			json.Unmarshal(raw, &env)
			want := ErrRequest
			switch name {
			case "version":
				env["api_version"] = "v2"
			case "operation":
				env["operation"] = "runtime.stop"
			case "unknown-top":
				env["extra"] = true
			case "malformed-plan":
				env["params"].(map[string]any)["plan"].(map[string]any)["plan_digest"] = strings.Repeat("0", 64)
				want = ErrPlan
			}
			coordinatorReject(t, c, p, context.Background(), planRaw(t, env), want)
		})
	}
	for _, name := range []string{"blockers", "stale-revision", "previous-generation", "select", "none"} {
		t.Run(name, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) {
				switch name {
				case "blockers":
					obj["blockers"] = json.RawMessage(`[{"code":"blocked","message":"not applicable"}]`)
				case "stale-revision":
					obj["base_revision"] = json.RawMessage(`"stale"`)
				default:
					var impact PlanImpact
					json.Unmarshal(obj["impact"], &impact)
					if name == "previous-generation" {
						g := "absent"
						impact.PreviousGeneration = &g
					} else {
						impact.RuntimeAction = name
					}
					obj["impact"] = planRaw(t, impact)
				}
			})
			want := ErrConflict
			if name == "blockers" {
				want = ErrPlan
			}
			if name == "select" || name == "none" {
				want = ErrCapabilityUnsupported
			}
			coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, name, plan, nil), want)
		})
	}
	t.Run("validator-rejects", func(t *testing.T) {
		p, e := config.ResolvePaths(t.TempDir(), nil)
		if e != nil {
			t.Fatal(e)
		}
		c, e := OpenCoordinator(p, func(json.RawMessage, []PlanInput) error { return errors.New("PRIVATE-VALIDATOR") }, func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		plan := coordinatorPlan(t, c, nil)
		coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, "invalid-intent", plan, nil), ErrPlan)
	})
}

func TestCoordinatorCurrentInputBindings(t *testing.T) {
	edits := map[string]func(*CurrentPlanInputs){
		"compiler": func(c *CurrentPlanInputs) { c.CompilerVersion = "changed" }, "capabilities-digest": func(c *CurrentPlanInputs) { c.CapabilitiesDigest = strings.Repeat("c", 64) },
		"missing": func(c *CurrentPlanInputs) { c.Inputs = c.Inputs[:3] }, "extra": func(c *CurrentPlanInputs) {
			c.Inputs = append(c.Inputs, PlanInput{Kind: "provider", ID: "extra", SHA256: strings.Repeat("a", 64)})
		},
		"duplicate": func(c *CurrentPlanInputs) { c.Inputs = append(c.Inputs, c.Inputs[0]) },
	}
	for i, kind := range []string{"provider", "core_binary", "capabilities", "rule_asset"} {
		i := i
		edits[kind] = func(c *CurrentPlanInputs) { c.Inputs[i].SHA256 = strings.Repeat("c", 64) }
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
				current := coordinatorInputs()
				edit(&current)
				return current, nil
			})
			plan := coordinatorPlan(t, c, nil)
			coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, name, plan, nil), ErrConflict)
		})
	}
	t.Run("order-independent", func(t *testing.T) {
		c, _ := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
			current := coordinatorInputs()
			current.Inputs[0], current.Inputs[3] = current.Inputs[3], current.Inputs[0]
			return current, nil
		})
		plan := coordinatorPlan(t, c, nil)
		if _, made, e := c.AdmitApply(context.Background(), coordinatorRequest(t, "reordered", plan, nil)); e != nil || !made {
			t.Fatal(made, e)
		}
	})
	t.Run("dependency-error-safe", func(t *testing.T) {
		c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
			return CurrentPlanInputs{}, errors.New("PRIVATE-READER")
		})
		plan := coordinatorPlan(t, c, nil)
		raw := coordinatorRequest(t, "unavailable", plan, nil)
		coordinatorReject(t, c, p, context.Background(), raw, config.ErrReadUnavailable)
		_, _, e := c.AdmitApply(context.Background(), raw)
		if e == nil || strings.Contains(e.Error(), "PRIVATE-READER") {
			t.Fatal("diagnostic leak", e)
		}
	})
	t.Run("cancelled-before-read", func(t *testing.T) {
		var calls atomic.Int32
		c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) { calls.Add(1); return coordinatorInputs(), nil })
		plan := coordinatorPlan(t, c, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		coordinatorReject(t, c, p, ctx, coordinatorRequest(t, "cancel", plan, nil), context.Canceled)
		if calls.Load() != 0 {
			t.Fatal("cancelled call read inputs")
		}
	})
	t.Run("cancelled-during-read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) { cancel(); return coordinatorInputs(), nil })
		plan := coordinatorPlan(t, c, nil)
		coordinatorReject(t, c, p, ctx, coordinatorRequest(t, "cancel-final", plan, nil), context.Canceled)
	})
}

func TestCoordinatorReplayBeforeEnvironmentAndConflicts(t *testing.T) {
	var unavailable atomic.Bool
	var reads atomic.Int32
	reader := func(context.Context, Plan) (CurrentPlanInputs, error) {
		reads.Add(1)
		if unavailable.Load() {
			return CurrentPlanInputs{}, errors.New("unavailable")
		}
		return coordinatorInputs(), nil
	}
	p, e := config.ResolvePaths(t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	verify := coordinatorValidator(t)
	c, e := OpenCoordinator(p, func(raw json.RawMessage, in []PlanInput) error {
		if unavailable.Load() {
			return errors.New("unavailable")
		}
		return verify(raw, in)
	}, reader)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	plan := coordinatorPlan(t, c, nil)
	raw := coordinatorRequest(t, "stable-id", plan, nil)
	old, made, e := c.AdmitApply(context.Background(), raw)
	if e != nil || !made {
		t.Fatal(e)
	}
	// Explicit synthetic state advance makes the accepted request's base stale;
	// replay must still use its stored identity before checking current active.
	active, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	active.Revision = "post-admission-revision"
	if e := c.store.PublishActive(active); e != nil {
		t.Fatal(e)
	}
	unavailable.Store(true)
	before := coordinatorTree(t, p)
	count := reads.Load()
	// The validator becomes unavailable too. Replays must not call it.
	got, made, e := c.AdmitApply(context.Background(), raw)
	if e != nil || made || !reflect.DeepEqual(got, old) || reads.Load() != count {
		t.Fatal("replay revalidated environment", made, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replay, made, e := c.AdmitApply(ctx, raw)
	if e != nil || made || !reflect.DeepEqual(replay, old) || reads.Load() != count {
		t.Fatal("cancelled known replay revalidated", e)
	}
	for _, field := range []string{"api_version", "operation", "params"} {
		t.Run(field, func(t *testing.T) {
			var env map[string]any
			json.Unmarshal(raw, &env)
			switch field {
			case "api_version":
				env[field] = "v2"
			case "operation":
				env[field] = "runtime.stop"
			case "params":
				env[field].(map[string]any)["accept_disruption"] = false
			}
			coordinatorReject(t, c, p, context.Background(), planRaw(t, env), ErrRequestIDReused)
		})
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("replay changed tree")
	}
}

func TestCoordinatorHistoryAndConcurrentAdmission(t *testing.T) {
	for _, state := range []string{"prepared", "degraded", "corrupt", "missing-directory", "unsafe-directory"} {
		t.Run(state, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			plan := coordinatorPlan(t, c, nil)
			old, made, e := c.AdmitApply(context.Background(), coordinatorRequest(t, "first", plan, nil))
			if e != nil || !made {
				t.Fatal(e)
			}
			want := ErrRecoveryRequired
			switch state {
			case "degraded":
				if _, e := c.journal.Advance(old.Record.Identity.OperationID, old.SHA256, Degraded, json.RawMessage(`{}`), json.RawMessage(`{"outcome":"degraded"}`)); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				if e := os.WriteFile(filepath.Join(p.State, "operations", string(old.Record.Identity.OperationID)+".json"), []byte(`{"broken":true}`), 0600); e != nil {
					t.Fatal(e)
				}
				want = ErrOperationRecord
			case "missing-directory":
				if e := os.RemoveAll(filepath.Join(p.State, "operations")); e != nil {
					t.Fatal(e)
				}
				want = config.ErrReadUnavailable
			case "unsafe-directory":
				if e := os.Chmod(filepath.Join(p.State, "operations"), 0755); e != nil {
					t.Fatal(e)
				}
				want = config.ErrReadUnavailable
			}
			coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, "second", plan, nil), want)
		})
	}
	t.Run("concurrent-only-one", func(t *testing.T) {
		c, p := coordinatorFixture(t, nil)
		plan := coordinatorPlan(t, c, nil)
		raws := [][]byte{coordinatorRequest(t, "one", plan, nil), coordinatorRequest(t, "two", plan, nil)}
		copies := []Coordinator{*c, *c}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var made atomic.Int32
		errs := make(chan error, 2)
		for i, raw := range raws {
			wg.Add(1)
			go func(receiver Coordinator, b []byte) {
				defer wg.Done()
				<-start
				_, created, e := receiver.AdmitApply(context.Background(), b)
				if created {
					made.Add(1)
				}
				errs <- e
			}(copies[i], raw)
		}
		close(start)
		wg.Wait()
		close(errs)
		var nilCount, recovery int
		for e := range errs {
			if e == nil {
				nilCount++
			} else if errors.Is(e, ErrRecoveryRequired) {
				recovery++
			} else {
				t.Fatal(e)
			}
		}
		if made.Load() != 1 || nilCount != 1 || recovery != 1 {
			t.Fatal("not serialized", made.Load(), nilCount, recovery)
		}
		if e := copies[0].Close(); e != nil {
			t.Fatal(e)
		}
		coordinatorReject(t, c, p, context.Background(), raws[0], config.ErrClosed)
	})
}

func TestCoordinatorCloseWaitsForAdmission(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRead()
	c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
		close(entered)
		<-release
		return coordinatorInputs(), nil
	})
	plan := coordinatorPlan(t, c, nil)
	raw := coordinatorRequest(t, "in-flight", plan, nil)
	admitted := make(chan error, 1)
	go func() {
		_, made, e := c.AdmitApply(context.Background(), raw)
		if e == nil && !made {
			e = errors.New("not created")
		}
		admitted <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader not entered")
	}
	if c.mu.TryLock() {
		c.mu.Unlock()
		t.Fatal("admission did not hold control lock through input read")
	}
	closed := make(chan error, 1)
	copy := *c
	go func() { closed <- copy.Close() }()
	if s, e := config.OpenStore(p, true); s != nil || !errors.Is(e, config.ErrWriterBusy) {
		if s != nil {
			s.Close()
		}
		releaseRead()
		t.Fatal("writer released while admission blocked", e)
	}
	select {
	case e := <-closed:
		releaseRead()
		t.Fatal("Close did not wait", e)
	case <-time.After(30 * time.Millisecond):
	}
	releaseRead()
	select {
	case e := <-admitted:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admission timeout")
	}
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close timeout")
	}
	s, e := config.OpenStore(p, true)
	if e != nil {
		t.Fatal("writer not released", e)
	}
	defer s.Close()
	coordinatorReject(t, c, p, context.Background(), raw, config.ErrClosed)
}

func TestCoordinatorActiveGenerationAndPreserveOld(t *testing.T) {
	for _, scenario := range []string{"same-target", "replace", "preserve-old-without-confirmed-disruption"} {
		t.Run(scenario, func(t *testing.T) {
			oldGeneration := "g1"
			// Independently declared fixture outcome, fixed before the Plan is built.
			expectedImpact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, PreviousGeneration: &oldGeneration, TargetGeneration: "g2", ConnectionEffect: "unknown", RuntimeAction: "replace"}
			c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
				current := coordinatorInputs()
				current.Impact = expectedImpact
				return current, nil
			})
			// A synthetic valid storage bundle establishes previous_generation only. No
			// compiler, process or core effect is represented by this fixture.
			files := map[string][]byte{"intent.json": []byte("{}\n"), "core.yaml": []byte("mode: rule\n")}
			manifest := config.BundleManifest{Version: 1, Generation: "g1", CompilerVersion: "fixture-v1", CapabilitiesDigest: hash([]byte("cap")), Binaries: []config.BinaryReference{{Path: "/fixture/mihomo", Version: "fixture-core", SHA256: hash([]byte("binary"))}}}
			for _, pair := range []struct{ kind, name string }{{"intent", "intent.json"}, {"generated_config", "core.yaml"}} {
				body := files[pair.name]
				manifest.Files = append(manifest.Files, config.BundleFile{Kind: pair.kind, Name: pair.name, Bytes: int64(len(body)), SHA256: hash(body)})
			}
			sha, e := c.store.PublishBundle(manifest, files)
			if e != nil {
				t.Fatal(e)
			}
			active, e := c.store.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			g := manifest.Generation
			op := config.ID("fixture-operation")
			active.Generation = &g
			active.ManifestSHA256 = sha
			active.OperationID = &op
			active.DesiredRuntime = "running"
			if e := c.store.PublishActive(active); e != nil {
				t.Fatal(e)
			}
			plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) {
				var impact PlanImpact
				json.Unmarshal(obj["impact"], &impact)
				impact.RuntimeAction = "replace"
				if scenario == "same-target" {
					impact.TargetGeneration = "g1"
				}
				if scenario == "preserve-old-without-confirmed-disruption" {
					impact.ConnectionEffect = "preserve_old"
					impact.RequiresDisruption = false
				}
				obj["impact"] = planRaw(t, impact)
			})
			raw := coordinatorRequest(t, scenario, plan, func(p map[string]any) {
				if scenario == "preserve-old-without-confirmed-disruption" {
					p["accept_disruption"] = false
				}
			})
			if scenario == "same-target" {
				coordinatorReject(t, c, p, context.Background(), raw, ErrConflict)
				return
			}
			if scenario == "preserve-old-without-confirmed-disruption" {
				// Recomputing the complete digest cannot create a preserve-old capability.
				coordinatorReject(t, c, p, context.Background(), raw, ErrCapabilityUnsupported)
				return
			}
			before := coordinatorTree(t, p)
			got, made, e := c.AdmitApply(context.Background(), raw)
			if e != nil || !made {
				t.Fatal(made, e)
			}
			if got.Record.Identity.Intent.PreviousGeneration == nil || *got.Record.Identity.Intent.PreviousGeneration != "g1" {
				t.Fatal("previous generation unbound")
			}
			after, e := c.store.ReadActive()
			if e != nil || !reflect.DeepEqual(active, after) {
				t.Fatal("changed active", e)
			}
			// Only operations may change; immutable bundles must retain bytes/mode/mtime.
			for path, value := range before {
				if strings.Contains(path, string(os.PathSeparator)+"bundles") {
					if coordinatorTree(t, p)[path] != value {
						t.Fatal("changed bundle")
					}
				}
			}
		})
	}
}

func TestCoordinatorIndependentlyObservedImpact(t *testing.T) {
	// Every expected value is declared independently before constructing the Plan.
	// These are synthetic reader fixtures, not actual runtime connection proofs.
	for _, field := range []string{"requires_disruption", "affected_apps", "previous_generation", "target_generation", "connection_effect", "runtime_action"} {
		t.Run(field, func(t *testing.T) {
			expected := coordinatorInputs().Impact
			if field == "requires_disruption" {
				expected.ConnectionEffect = "preserve_old"
			}
			if field == "affected_apps" {
				expected.AffectedApps = []string{"a", "b"}
			}
			c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
				current := coordinatorInputs()
				current.Impact = expected
				return current, nil
			})
			plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) {
				claimed := expected
				claimed.AffectedApps = append([]string{}, expected.AffectedApps...)
				switch field {
				case "requires_disruption":
					claimed.RequiresDisruption = false
				case "affected_apps":
					claimed.AffectedApps = []string{"b", "a"}
				case "previous_generation":
					previous := "g0"
					claimed.PreviousGeneration = &previous
				case "target_generation":
					claimed.TargetGeneration = "g3"
				case "connection_effect":
					claimed.ConnectionEffect = "interrupt"
				case "runtime_action":
					claimed.RuntimeAction = "replace"
				}
				obj["impact"] = planRaw(t, claimed)
			})
			// BuildPlan has recomputed a valid digest for the changed claim. Comparing
			// that digest alone cannot establish the independent effect assessment.
			coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, field, plan, nil), ErrConflict)
		})
	}
	t.Run("nil-current-apps-is-not-proved-empty", func(t *testing.T) {
		c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
			current := coordinatorInputs()
			current.Impact.AffectedApps = nil
			return current, nil
		})
		plan := coordinatorPlan(t, c, nil)
		coordinatorReject(t, c, p, context.Background(), coordinatorRequest(t, "unknown-apps", plan, nil), ErrConflict)
	})
	t.Run("independently-proven-start-preserve-old", func(t *testing.T) {
		expected := PlanImpact{RequiresDisruption: false, AffectedApps: []string{}, TargetGeneration: "g2", ConnectionEffect: "preserve_old", RuntimeAction: "start"}
		c, p := coordinatorFixture(t, func(context.Context, Plan) (CurrentPlanInputs, error) {
			current := coordinatorInputs()
			current.Impact = expected
			return current, nil
		})
		plan := coordinatorPlan(t, c, func(obj map[string]json.RawMessage) { obj["impact"] = planRaw(t, expected) })
		before, e := c.store.ReadActive()
		if e != nil {
			t.Fatal(e)
		}
		got, made, e := c.AdmitApply(context.Background(), coordinatorRequest(t, "safe-start", plan, func(params map[string]any) { params["accept_disruption"] = false }))
		if e != nil || !made || got.Record.Phase != Prepared {
			t.Fatal("independent start impact rejected", made, e)
		}
		after, e := c.store.ReadActive()
		if e != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("changed active", e)
		}
		entries, e := os.ReadDir(filepath.Join(p.State, "bundles"))
		if e != nil || len(entries) != 0 {
			t.Fatal("created bundle", e)
		}
	})
}
