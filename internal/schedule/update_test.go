package schedule

// T036 definitions target the planned T042 scheduler. Clock and callback
// barriers are test inputs; they do not implement a scheduler or auto-apply.
import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
)

func scheduleHours(hours uint64) *uint64 { return &hours }
func scheduleNow() time.Time             { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

type scheduleClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *scheduleClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *scheduleClock) Set(value time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.value = value }
func scheduleState(id config.ID) SourceState {
	return SourceState{ID: id, UpdateHours: scheduleHours(1), LatestSnapshotID: config.ID("old-" + string(id)), PendingSnapshotID: config.ID("pending-" + string(id)), AppliedSnapshotID: config.ID("applied-" + string(id)), LastSuccess: scheduleNow().Add(-24 * time.Hour)}
}
func scheduleContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func scheduleReceive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-ctx.Done():
		t.Fatal("scheduler barrier did not complete")
		var zero T
		return zero
	}
}
func scheduleStates(t *testing.T, ctx context.Context, scheduler *Scheduler) []SourceState {
	t.Helper()
	done := make(chan []SourceState, 1)
	go func() { done <- scheduler.States() }()
	return scheduleReceive(t, ctx, done)
}
func scheduleFind(t *testing.T, states []SourceState, id config.ID) SourceState {
	t.Helper()
	var found SourceState
	count := 0
	for _, state := range states {
		if state.ID == id {
			found = state
			count++
		}
	}
	if count != 1 {
		t.Fatal("source state missing or duplicated")
	}
	return found
}
func scheduleRun(scheduler *Scheduler, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- scheduler.RunDue(ctx) }()
	return done
}
func mustSchedule(t *testing.T, states []SourceState, now func() time.Time, fetch FetchFunc, validate ValidateFunc) *Scheduler {
	t.Helper()
	scheduler, err := New(states, now, fetch, validate)
	if err != nil || scheduler == nil {
		t.Fatal("valid scheduler rejected (details withheld)")
	}
	return scheduler
}
func scheduleFetch(context.Context, config.ID) ([]byte, error) {
	return []byte("synthetic-provider"), nil
}
func scheduleValidate(_ context.Context, id config.ID, _ []byte) (config.ID, error) {
	return config.ID("new-" + string(id)), nil
}

func TestUpdateScheduleValidationAndCopies(t *testing.T) {
	for _, item := range []struct {
		name   string
		states []SourceState
	}{
		{"zero-hours", []SourceState{{ID: "a", UpdateHours: scheduleHours(0)}}},
		{"duration-overflow", []SourceState{{ID: "a", UpdateHours: scheduleHours(2562048)}}},
		{"integer-overflow", []SourceState{{ID: "a", UpdateHours: scheduleHours(^uint64(0))}}},
		{"duplicate-source", []SourceState{scheduleState("a"), scheduleState("a")}},
		{"invalid-source", []SourceState{scheduleState("bad id")}},
		{"empty-source", []SourceState{scheduleState("")}},
	} {
		t.Run(item.name, func(t *testing.T) {
			s, err := New(item.states, scheduleNow, scheduleFetch, scheduleValidate)
			if !errors.Is(err, ErrInvalidSchedule) || s != nil {
				t.Fatal("invalid schedule accepted")
			}
		})
	}
	for _, missing := range []string{"clock", "fetch", "validate"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			var now func() time.Time = scheduleNow
			var fetch FetchFunc = scheduleFetch
			var validate ValidateFunc = scheduleValidate
			switch missing {
			case "clock":
				now = nil
			case "fetch":
				fetch = nil
			case "validate":
				validate = nil
			}
			s, err := New([]SourceState{scheduleState("a")}, now, fetch, validate)
			if !errors.Is(err, ErrInvalidSchedule) || s != nil {
				t.Fatal("missing scheduler dependency accepted")
			}
		})
	}
	ctx := scheduleContext(t)
	input := []SourceState{scheduleState("a")}
	var calls atomic.Int32
	fetch := func(context.Context, config.ID) ([]byte, error) { calls.Add(1); return nil, nil }
	validate := func(context.Context, config.ID, []byte) (config.ID, error) { calls.Add(1); return "candidate", nil }
	scheduler := mustSchedule(t, input, scheduleNow, fetch, validate)
	input[0].ID = "mutated"
	*input[0].UpdateHours = 9
	state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if state.UpdateHours == nil || *state.UpdateHours != 1 {
		t.Fatal("constructor retained caller schedule pointer")
	}
	output := scheduleStates(t, ctx, scheduler)
	output[0].ID = "mutated"
	*output[0].UpdateHours = 7
	again := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if again.UpdateHours == nil || *again.UpdateHours != 1 || calls.Load() != 0 {
		t.Fatal("state getter aliases scheduler or performs updates")
	}
}

func TestUpdateDueCatchUpAndClockRollback(t *testing.T) {
	ctx := scheduleContext(t)
	clock := &scheduleClock{value: scheduleNow()}
	missed, equal, never, future, disabled := scheduleState("missed"), scheduleState("equal"), scheduleState("never"), scheduleState("future"), scheduleState("disabled")
	missed.NextDue = clock.Now().Add(-3 * time.Hour)
	equal.NextDue = clock.Now()
	future.NextDue = clock.Now().Add(time.Hour)
	disabled.UpdateHours = nil
	var mu sync.Mutex
	counts := map[config.ID]int{}
	fetch := func(_ context.Context, id config.ID) ([]byte, error) {
		mu.Lock()
		counts[id]++
		mu.Unlock()
		return []byte("synthetic-provider"), nil
	}
	scheduler := mustSchedule(t, []SourceState{missed, equal, never, future, disabled}, clock.Now, fetch, scheduleValidate)
	if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil {
		t.Fatal("due update failed")
	}
	for _, id := range []config.ID{"missed", "equal", "never"} {
		state := scheduleFind(t, scheduleStates(t, ctx, scheduler), id)
		if counts[id] != 1 || !state.LastAttempt.Equal(scheduleNow()) || !state.NextDue.Equal(scheduleNow().Add(time.Hour)) {
			t.Fatal("missed periods replayed or next deadline based on stale due time")
		}
	}
	if counts["future"] != 0 || counts["disabled"] != 0 {
		t.Fatal("not-due or disabled source updated")
	}
	for _, now := range []time.Time{scheduleNow(), scheduleNow().Add(-time.Hour)} {
		clock.Set(now)
		if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil {
			t.Fatal("no-op schedule failed")
		}
	}
	for _, id := range []config.ID{"missed", "equal", "never"} {
		if counts[id] != 1 {
			t.Fatal("immediate/rollback clock caused another attempt")
		}
	}
	clock.Set(scheduleNow().Add(time.Hour))
	if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil {
		t.Fatal("equal next deadline not runnable")
	}
	for _, id := range []config.ID{"missed", "equal", "never"} {
		if counts[id] != 2 {
			t.Fatal("equality did not schedule exactly one next attempt")
		}
	}
	if counts["future"] != 1 || counts["disabled"] != 0 {
		t.Fatal("future/disabled due behavior incorrect")
	}
	state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "disabled")
	if !state.LastAttempt.IsZero() || !state.NextDue.IsZero() {
		t.Fatal("disabled source schedule mutated")
	}
}

func TestUpdateCompletionClockAndPendingNotApplied(t *testing.T) {
	ctx := scheduleContext(t)
	clock := &scheduleClock{value: scheduleNow()}
	original := scheduleState("a")
	original.LastError = "fetch_failed"
	fetch := func(context.Context, config.ID) ([]byte, error) {
		clock.Set(scheduleNow().Add(time.Minute))
		return []byte("synthetic-provider"), nil
	}
	validate := func(context.Context, config.ID, []byte) (config.ID, error) {
		clock.Set(scheduleNow().Add(2 * time.Minute))
		return "candidate-a", nil
	}
	scheduler := mustSchedule(t, []SourceState{original}, clock.Now, fetch, validate)
	if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil {
		t.Fatal("candidate update failed")
	}
	state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if !state.LastAttempt.Equal(scheduleNow()) || !state.NextDue.Equal(scheduleNow().Add(time.Hour)) || !state.LastSuccess.Equal(scheduleNow().Add(2*time.Minute)) {
		t.Fatal("attempt/completion clock boundaries conflated")
	}
	if state.LatestSnapshotID != "candidate-a" || state.PendingSnapshotID != "candidate-a" || state.AppliedSnapshotID != original.AppliedSnapshotID || !state.Downloaded || !state.Validated || state.Updating || state.LastError != "" {
		t.Fatal("validated candidate was not pending, or was implicitly applied")
	}
}

func TestUpdateDownloadedValidatedAndAppliedRemainSeparate(t *testing.T) {
	ctx := scheduleContext(t)
	clock := &scheduleClock{value: scheduleNow()}
	original := scheduleState("a")
	original.Downloaded, original.Validated = true, true
	fetchEntered, validateEntered := make(chan struct{}, 1), make(chan struct{}, 1)
	releaseFetch, releaseValidate := make(chan struct{}), make(chan struct{})
	fetch := func(ctx context.Context, id config.ID) ([]byte, error) {
		fetchEntered <- struct{}{}
		select {
		case <-releaseFetch:
			return []byte("synthetic-provider"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	validate := func(ctx context.Context, id config.ID, raw []byte) (config.ID, error) {
		if string(raw) != "synthetic-provider" {
			return "", errors.New("unexpected synthetic bytes")
		}
		validateEntered <- struct{}{}
		select {
		case <-releaseValidate:
			return "candidate-a", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	scheduler := mustSchedule(t, []SourceState{original}, clock.Now, fetch, validate)
	done := scheduleRun(scheduler, ctx)
	scheduleReceive(t, ctx, fetchEntered)
	duringFetch := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if !duringFetch.Updating || duringFetch.Downloaded || duringFetch.Validated || duringFetch.LatestSnapshotID != original.LatestSnapshotID || duringFetch.PendingSnapshotID != original.PendingSnapshotID || duringFetch.AppliedSnapshotID != original.AppliedSnapshotID {
		t.Fatal("fetch stage claimed validation/application")
	}
	close(releaseFetch)
	scheduleReceive(t, ctx, validateEntered)
	duringValidate := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if !duringValidate.Updating || !duringValidate.Downloaded || duringValidate.Validated || duringValidate.LatestSnapshotID != original.LatestSnapshotID || duringValidate.PendingSnapshotID != original.PendingSnapshotID || duringValidate.AppliedSnapshotID != original.AppliedSnapshotID || !duringValidate.LastSuccess.Equal(original.LastSuccess) {
		t.Fatal("download success advanced usable/applied snapshot")
	}
	clock.Set(scheduleNow().Add(time.Minute))
	close(releaseValidate)
	if err := scheduleReceive(t, ctx, done); err != nil {
		t.Fatal("validated update failed")
	}
	state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if state.Updating || !state.Downloaded || !state.Validated || state.LatestSnapshotID != "candidate-a" || state.PendingSnapshotID != "candidate-a" || state.AppliedSnapshotID != original.AppliedSnapshotID {
		t.Fatal("validation did not preserve applied snapshot")
	}
}

func TestUpdateAlreadyAppliedCandidateClearsPending(t *testing.T) {
	ctx := scheduleContext(t)
	original := scheduleState("a")
	validate := func(context.Context, config.ID, []byte) (config.ID, error) { return original.AppliedSnapshotID, nil }
	scheduler := mustSchedule(t, []SourceState{original}, scheduleNow, scheduleFetch, validate)
	if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil {
		t.Fatal("already-applied candidate rejected")
	}
	state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
	if state.LatestSnapshotID != original.AppliedSnapshotID || state.AppliedSnapshotID != original.AppliedSnapshotID || state.PendingSnapshotID != "" || !state.Validated {
		t.Fatal("already applied candidate fabricated another application/pending change")
	}
}

func TestUpdateFailureRetainsUsableAndSanitizesErrors(t *testing.T) {
	const privateError = "synthetic-private-body https://user:password@private.invalid/provider"
	for _, kind := range []string{"fetch", "validate", "empty-snapshot", "invalid-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			ctx := scheduleContext(t)
			original := scheduleState("a")
			var calls atomic.Int32
			fetch := func(context.Context, config.ID) ([]byte, error) {
				calls.Add(1)
				if kind == "fetch" {
					return nil, errors.New(privateError)
				}
				return []byte(privateError), nil
			}
			validate := func(context.Context, config.ID, []byte) (config.ID, error) {
				switch kind {
				case "validate":
					return "", errors.New(privateError)
				case "empty-snapshot":
					return "", nil
				default:
					return "bad id", nil
				}
			}
			scheduler := mustSchedule(t, []SourceState{original}, scheduleNow, fetch, validate)
			err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx))
			if !errors.Is(err, ErrUpdateFailed) {
				t.Fatal("failed update missing classification")
			}
			state := scheduleFind(t, scheduleStates(t, ctx, scheduler), "a")
			wantCode := "validation_failed"
			if kind == "fetch" {
				wantCode = "fetch_failed"
			}
			if state.LastError != wantCode || strings.Contains(err.Error(), privateError) || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "private.invalid") {
				t.Fatal("unsafe update error exposed or wrong bounded code")
			}
			if state.LatestSnapshotID != original.LatestSnapshotID || state.PendingSnapshotID != original.PendingSnapshotID || state.AppliedSnapshotID != original.AppliedSnapshotID || !state.LastSuccess.Equal(original.LastSuccess) || state.Updating || state.Validated || state.Downloaded != (kind != "fetch") {
				t.Fatal("failed update discarded usable/pending/applied snapshot")
			}
			if !state.LastAttempt.Equal(scheduleNow()) || !state.NextDue.Equal(scheduleNow().Add(time.Hour)) {
				t.Fatal("failure did not schedule bounded next attempt")
			}
			if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); err != nil || calls.Load() != 1 {
				t.Fatal("failed source retried before next due")
			}
		})
	}
}

func TestUpdateWorkerBoundsAndConcurrentRunBusy(t *testing.T) {
	ctx := scheduleContext(t)
	states := []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}
	entered := make(chan config.ID, 3)
	gates := map[config.ID]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{}), "c": make(chan struct{})}
	var mu sync.Mutex
	active, maxActive := 0, 0
	counts := map[config.ID]int{}
	validate := func(ctx context.Context, id config.ID, raw []byte) (config.ID, error) {
		mu.Lock()
		counts[id]++
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		entered <- id
		select {
		case <-gates[id]:
			return config.ID("candidate-" + string(id)), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	scheduler := mustSchedule(t, states, scheduleNow, scheduleFetch, validate)
	done := scheduleRun(scheduler, ctx)
	first, second := scheduleReceive(t, ctx, entered), scheduleReceive(t, ctx, entered)
	if first == second {
		t.Fatal("one source has concurrent workers")
	}
	current := scheduleStates(t, ctx, scheduler)
	updating := 0
	for _, state := range current {
		if state.Updating {
			updating++
		}
	}
	if updating != 2 {
		t.Fatal("scheduler did not expose exactly two held workers")
	}
	before := scheduleStates(t, ctx, scheduler)
	if err := scheduleReceive(t, ctx, scheduleRun(scheduler, ctx)); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent RunDue accepted")
	}
	if !reflect.DeepEqual(before, scheduleStates(t, ctx, scheduler)) {
		t.Fatal("busy call mutated state")
	}
	close(gates[first])
	third := scheduleReceive(t, ctx, entered)
	if third == first || third == second {
		t.Fatal("source retried instead of starting remaining due source")
	}
	close(gates[second])
	close(gates[third])
	if err := scheduleReceive(t, ctx, done); err != nil {
		t.Fatal("bounded worker run failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if active != 0 || maxActive != 2 || len(counts) != 3 {
		t.Fatal("workers not joined or global concurrency bound violated")
	}
	for _, count := range counts {
		if count != 1 {
			t.Fatal("source attempted more than once")
		}
	}
}

func TestUpdateCancellationJoinsWorkersAndLeavesUnstartedDue(t *testing.T) {
	guard := scheduleContext(t)
	ctx, cancel := context.WithCancel(guard)
	defer cancel()
	states := []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}
	entered := make(chan config.ID, 3)
	cancelled := make(chan config.ID, 3)
	returnGate := make(chan struct{})
	releaseWorkers := sync.OnceFunc(func() { close(returnGate) })
	t.Cleanup(releaseWorkers)
	var completed atomic.Int32
	validate := func(ctx context.Context, id config.ID, raw []byte) (config.ID, error) {
		entered <- id
		<-ctx.Done()
		cancelled <- id
		select {
		case <-returnGate:
		case <-guard.Done():
		}
		completed.Add(1)
		return "", ctx.Err()
	}
	scheduler := mustSchedule(t, states, scheduleNow, scheduleFetch, validate)
	done := scheduleRun(scheduler, ctx)
	first, second := scheduleReceive(t, guard, entered), scheduleReceive(t, guard, entered)
	if first == second {
		t.Fatal("duplicate per-source worker")
	}
	cancel()
	cancelFirst, cancelSecond := scheduleReceive(t, guard, cancelled), scheduleReceive(t, guard, cancelled)
	if cancelFirst == cancelSecond || completed.Load() != 0 {
		t.Fatal("cancellation barrier did not retain both workers")
	}
	// Cancelling does not finish RunDue until callbacks return; the active call
	// still excludes another RunDue while it joins those original workers.
	if err := scheduleReceive(t, guard, scheduleRun(scheduler, guard)); !errors.Is(err, ErrBusy) {
		t.Fatal("cancelling call released worker ownership before joining")
	}
	select {
	case <-done:
		t.Fatal("cancelled run returned while callbacks remain held")
	default:
	}
	releaseWorkers()
	err := scheduleReceive(t, guard, done)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUpdateFailed) || completed.Load() != 2 {
		t.Fatal("cancellation lost cause or returned before workers joined")
	}
	started, unstarted := 0, 0
	for _, state := range scheduleStates(t, guard, scheduler) {
		original := scheduleFind(t, states, state.ID)
		if state.Updating || state.LatestSnapshotID != original.LatestSnapshotID || state.PendingSnapshotID != original.PendingSnapshotID || state.AppliedSnapshotID != original.AppliedSnapshotID || !state.LastSuccess.Equal(original.LastSuccess) {
			t.Fatal("cancellation mutated usable state or left worker active")
		}
		if state.ID == first || state.ID == second {
			started++
			if state.LastError != "cancelled" || !state.LastAttempt.Equal(scheduleNow()) || !state.NextDue.Equal(scheduleNow().Add(time.Hour)) || !state.Downloaded || state.Validated {
				t.Fatal("started cancellation state incorrect")
			}
		} else {
			unstarted++
			if !state.LastAttempt.Equal(original.LastAttempt) || !state.NextDue.Equal(original.NextDue) || state.Downloaded != original.Downloaded || state.Validated != original.Validated {
				t.Fatal("unstarted source no longer due")
			}
		}
	}
	if started != 2 || unstarted != 1 {
		t.Fatal("unexpected cancellation worker inventory")
	}
}

func TestUpdateCancellationReachesFetch(t *testing.T) {
	guard := scheduleContext(t)
	ctx, cancel := context.WithCancel(guard)
	defer cancel()
	entered := make(chan struct{}, 1)
	var completed, validations atomic.Int32
	fetch := func(ctx context.Context, id config.ID) ([]byte, error) {
		entered <- struct{}{}
		<-ctx.Done()
		completed.Add(1)
		return nil, ctx.Err()
	}
	validate := func(context.Context, config.ID, []byte) (config.ID, error) {
		validations.Add(1)
		return "candidate", nil
	}
	original := scheduleState("a")
	scheduler := mustSchedule(t, []SourceState{original}, scheduleNow, fetch, validate)
	done := scheduleRun(scheduler, ctx)
	scheduleReceive(t, guard, entered)
	cancel()
	err := scheduleReceive(t, guard, done)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUpdateFailed) || completed.Load() != 1 || validations.Load() != 0 {
		t.Fatal("cancelled fetch not joined or still validated")
	}
	state := scheduleFind(t, scheduleStates(t, guard, scheduler), "a")
	if state.LastError != "cancelled" || state.Downloaded || state.Validated || state.Updating || state.LatestSnapshotID != original.LatestSnapshotID || state.PendingSnapshotID != original.PendingSnapshotID || state.AppliedSnapshotID != original.AppliedSnapshotID {
		t.Fatal("cancelled fetch produced candidate/application")
	}
}
