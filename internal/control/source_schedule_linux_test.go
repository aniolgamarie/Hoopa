//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

// This installs a synthetic stopped bundle to isolate durable scheduling. It is
// deliberately not evidence of a successful Plan/Apply or a running core.
func sourceSchedulePublish(t *testing.T, c *Coordinator, generation string, definitions []config.Subscription) config.ActivePointer {
	t.Helper()
	intent := updateRequestIntent(t)
	intent["subscriptions"] = definitions
	intent["selection"] = map[string]any{"active_subscription": nil}
	intent["groups"] = []any{}
	intent["rules"] = []any{}
	intent["default_target"] = map[string]any{"kind": "reject"}
	raw := planRaw(t, intent)
	if _, err := config.ParseIntent(raw, config.IntentApply); err != nil {
		t.Fatal(err)
	}
	m, files, err := config.AssembleBundle(config.Generation(generation), "scheduler-fixture", hash([]byte("scheduler-cap")), []config.BinaryReference{{Path: "/uninstalled/scheduler-fixture", Version: "fixture", SHA256: hash([]byte("fixture"))}}, []config.BundleAsset{{Kind: "intent", Name: "intent.json", Data: raw}})
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sha, err := c.store.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	g, op := m.Generation, config.ID("synthetic-schedule-configuration")
	base.Generation, base.ManifestSHA256, base.OperationID = &g, sha, &op
	base.Revision = config.Revision("revision-" + generation)
	if err := c.store.PublishActive(base); err != nil {
		t.Fatal(err)
	}
	return base
}
func sourceScheduleClock(u *SourceUpdater) *atomic.Int64 {
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC).UnixNano())
	u.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	return clock
}
func sourceScheduleWait(t *testing.T, u *SourceUpdater, ids []config.ID) []sourceHistoryEntry {
	t.Helper()
	out := []sourceHistoryEntry{}
	for _, id := range ids {
		u.c.mu.Lock()
		op, err := u.c.journal.Get(id)
		u.c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, updaterWait(t, u, op.Record.Request))
	}
	return out
}
func TestSourceScheduleUncommittedIntentCannotEnableTimer(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	sourceScheduleClock(u)
	raw := updaterRequest(t, c, "uncommitted-schedule", "source-a", updaterFile(t, updaterProvider), nil)
	updaterBegin(t, u, raw)
	updaterWait(t, u, raw)
	before := coordinatorTree(t, p)
	ids, err := u.RunDue(context.Background())
	if err != nil || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("uncommitted Intent enabled scheduling", err)
	}
}
func TestSourceScheduleDurableCatchupAndReopen(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	clock := sourceScheduleClock(u)
	hours := uint64(1)
	source := updaterFile(t, updaterProvider)
	base := sourceSchedulePublish(t, c, "schedule-catchup", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	for turn := 0; turn < 2; turn++ {
		if turn == 1 {
			clock.Add(int64(48 * time.Hour))
		}
		ids, err := u.RunDue(context.Background())
		if err != nil || len(ids) != 1 {
			t.Fatal("one catchup reservation", turn, err, len(ids))
		}
		entries := sourceScheduleWait(t, u, ids)
		entry := entries[0]
		wantDue := time.Unix(0, clock.Load()).UTC().Add(time.Hour).Format(time.RFC3339Nano)
		if entry.Operation.Record.Phase != Committed || entry.Context.Sequence != uint64(turn+1) || entry.Context.NextDue == nil || *entry.Context.NextDue != wantDue {
			t.Fatal("reservation drift/backlog")
		}
		before := coordinatorTree(t, p)
		if ids, err = u.RunDue(context.Background()); err != nil || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
			t.Fatal("repeated pulse duplicated interval", err)
		}
	}
	after, _ := c.store.ReadActive()
	if !reflect.DeepEqual(after, base) {
		t.Fatal("automatic acquisition applied configuration")
	}
	if err := os.Remove(source.Path); err != nil {
		t.Fatal(err)
	}
	u.Close()
	c = initializeReopen(t, c, p)
	next, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	next.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	before := coordinatorTree(t, p)
	if ids, err := next.RunDue(context.Background()); err != nil || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("reopen forgot consumed interval", err)
	}
}
func TestSourceScheduleSharesManualWorkerLimit(t *testing.T) {
	gate, source := updaterHTTPS(t)
	c, p, u := updaterFixture(t, subscription.FetchOptions{Transport: gate.server.Client().Transport})
	sourceScheduleClock(u)
	hours := uint64(1)
	sourceSchedulePublish(t, c, "schedule-workers", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}, {ID: "source-b", Source: source, UpdateHours: &hours}, {ID: "source-c", Source: source, UpdateHours: &hours}})
	raw := updaterRequest(t, c, "manual-worker", "source-a", source, nil)
	updaterBegin(t, u, raw)
	updaterEntered(t, gate)
	ids, err := u.RunDue(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("manual/auto worker budget", len(ids), err)
	}
	updaterEntered(t, gate)
	before := coordinatorTree(t, p)
	if more, err := u.RunDue(context.Background()); err != nil || len(more) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("occupied workers reserved more", err)
	}
	gate.once.Do(func() { close(gate.release) })
	updaterWait(t, u, raw)
	sourceScheduleWait(t, u, ids)
	last, err := u.RunDue(context.Background())
	if err != nil || len(last) != 1 {
		t.Fatal("queued source was lost/pre-reserved", len(last), err)
	}
	sourceScheduleWait(t, u, last)
	if gate.calls.Load() != 3 {
		t.Fatal("duplicate/absent fetch", gate.calls.Load())
	}
}
func TestSourceScheduleManualInterveningAttemptInvalidatesPulse(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	sourceScheduleClock(u)
	hours := uint64(1)
	source := updaterFile(t, updaterProvider)
	sourceSchedulePublish(t, c, "schedule-stale", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	c.mu.Lock()
	base, batch, err := u.captureDueLocked(context.Background())
	c.mu.Unlock()
	if err != nil || len(batch) != 1 {
		t.Fatal(err)
	}
	raw := updaterRequest(t, c, "manual-consumed", "source-a", source, nil)
	updaterBegin(t, u, raw)
	updaterWait(t, u, raw)
	before := coordinatorTree(t, p)
	if _, made, err := u.updateDue(context.Background(), base, batch[0]); made || !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("stale pulse admitted duplicate", made, err)
	}
}
func TestSourceScheduleDeadPendingNeedsExplicitRecovery(t *testing.T) {
	c, p := sourceRecoverTestCoordinator(t)
	source := updaterFile(t, updaterProvider)
	hours := uint64(1)
	sourceSchedulePublish(t, c, "schedule-pending", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	seed := sourceRecoverTestSeed(t, c, "dead-schedule", "source-a", source, Applying, "")
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	clock := sourceScheduleClock(u)
	clock.Add(int64(48 * time.Hour))
	before := coordinatorTree(t, p)
	if ids, err := u.RunDue(context.Background()); !errors.Is(err, ErrRecoveryRequired) || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("dead pending auto-replayed/recovered", err)
	}
	call, made, err := sourceRecoverTestCall(t, c, sourceRecoverTestRequest(t, c, seed.entry.Operation, "schedule-explicit-recover"), nil)
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("explicit recovery", err)
	}
	ids, err := u.RunDue(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("post-recovery catchup", err)
	}
	if entry := sourceScheduleWait(t, u, ids)[0]; entry.Context.Sequence != 2 {
		t.Fatal("recovery rewrote sequence")
	}
}
func TestSourceSchedulePeriodChangePreservesReservedDueAndDisable(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	clock := sourceScheduleClock(u)
	hours := uint64(2)
	source := updaterFile(t, updaterProvider)
	sourceSchedulePublish(t, c, "period-first", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	ids, err := u.RunDue(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal(err)
	}
	sourceScheduleWait(t, u, ids)
	clock.Add(int64(time.Hour))
	hours = 1
	sourceSchedulePublish(t, c, "period-shorter", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	before := coordinatorTree(t, p)
	if ids, err := u.RunDue(context.Background()); err != nil || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("period change rewrote old due", err)
	}
	clock.Add(int64(4 * time.Hour))
	sourceSchedulePublish(t, c, "period-disabled", []config.Subscription{{ID: "source-a", Source: source}})
	if ids, err := u.RunDue(context.Background()); err != nil || len(ids) != 0 {
		t.Fatal("disabled schedule ran", err)
	}
	sourceSchedulePublish(t, c, "period-enabled", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	ids, err = u.RunDue(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("reenable did not catch up once", err)
	}
	entry := sourceScheduleWait(t, u, ids)[0]
	if entry.Context.Sequence != 2 || *entry.Context.Source.UpdateHours != 1 || *entry.Context.NextDue != time.Unix(0, clock.Load()).UTC().Add(time.Hour).Format(time.RFC3339Nano) {
		t.Fatal("new period not reserved")
	}
}
func TestSourceScheduleLoopCloseJoinsAndStatusDoesNotStartIt(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	before := coordinatorTree(t, p)
	status, err := c.Status(context.Background())
	if err != nil || status.SourceSchedule == nil || status.SourceSchedule.State != "inactive" || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("query started timer", err)
	}
	done := make(chan error, 1)
	go func() { done <- u.RunSchedule(context.Background()) }()
	deadline := time.After(5 * time.Second)
	for {
		status, err = c.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.SourceSchedule.LastCheckedAt != nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("schedule loop did not pulse")
		case <-time.After(time.Millisecond):
		}
	}
	originalStamp := *status.SourceSchedule.LastCheckedAt
	*status.SourceSchedule.LastCheckedAt = "caller-mutation"
	again, err := c.Status(context.Background())
	if err != nil || *again.SourceSchedule.LastCheckedAt != originalStamp {
		t.Fatal("caller mutated shared scheduler state", err)
	}
	if err := u.RunSchedule(context.Background()); !errors.Is(err, ErrConflict) {
		t.Fatal("second loop admitted", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer closed before scheduler joined")
	}
}

func TestSourceScheduleManualNullPeriodConsumesCommittedInterval(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	clock := sourceScheduleClock(u)
	hours := uint64(1)
	source := updaterFile(t, updaterProvider)
	sourceSchedulePublish(t, c, "schedule-manual-null", []config.Subscription{{ID: "source-a", Source: source, UpdateHours: &hours}})
	raw := updaterRequest(t, c, "manual-no-period", "source-a", source, nil)
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("request fixture")
	}
	body["params"].(map[string]any)["intent"].(map[string]any)["subscriptions"].([]any)[0].(map[string]any)["update_hours"] = nil
	raw = planRaw(t, body)
	updaterBegin(t, u, raw)
	entry := updaterWait(t, u, raw)
	if entry.Context.NextDue != nil {
		t.Fatal("manual history rewritten")
	}
	before := coordinatorTree(t, p)
	if ids, err := u.RunDue(context.Background()); err != nil || len(ids) != 0 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("manual null period caused immediate duplicate", err)
	}
	page, err := c.Sources(context.Background(), "")
	want := time.Unix(0, clock.Load()).UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if err != nil || len(page.Sources) != 1 || page.Sources[0].NextDue != nil || page.Sources[0].EffectiveNextDue == nil || *page.Sources[0].EffectiveNextDue != want {
		t.Fatal("reserved/effective time conflated", err)
	}
	clock.Add(int64(time.Hour))
	ids, err := u.RunDue(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("effective due boundary", err)
	}
	if next := sourceScheduleWait(t, u, ids)[0]; next.Context.Sequence != 2 || next.Context.NextDue == nil {
		t.Fatal("automatic reservation missing current interval")
	}
}

func TestSourceScheduleSourceDependencyFailureDoesNotStarveOthers(t *testing.T) {
	c, _, u := updaterFixture(t, subscription.FetchOptions{})
	sourceScheduleClock(u)
	hours := uint64(1)
	bad := config.SubscriptionSource{Kind: "https", URLSecretRef: filepath.Join(t.TempDir(), "missing-secret")}
	good := updaterFile(t, updaterProvider)
	sourceSchedulePublish(t, c, "schedule-isolation", []config.Subscription{{ID: "source-bad", Source: bad, UpdateHours: &hours}, {ID: "source-good", Source: good, UpdateHours: &hours}})
	ids, err := u.RunDue(context.Background())
	if err == nil || len(ids) != 1 {
		t.Fatal("dependency failure hid/stopped other source", err, len(ids))
	}
	if entry := sourceScheduleWait(t, u, ids)[0]; entry.Context.Source.ID != "source-good" || entry.Operation.Record.Phase != Committed {
		t.Fatal("healthy source did not commit")
	}
}
