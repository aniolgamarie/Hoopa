package schedule

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
)

func TestUpdateMixedFailuresContinueAndBoundErrors(t *testing.T) {
	states := []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}
	private := errors.New("private credential response")
	scheduler := mustSchedule(t, states, scheduleNow, func(_ context.Context, id config.ID) ([]byte, error) {
		if id == "a" {
			return nil, private
		}
		return []byte("provider"), nil
	}, func(_ context.Context, id config.ID, _ []byte) (config.ID, error) {
		if id == "b" {
			return "", private
		}
		return "new-c", nil
	})
	err := scheduler.RunDue(scheduleContext(t))
	if !errors.Is(err, ErrUpdateFailed) || err.Error() != ErrUpdateFailed.Error() || errors.Is(err, private) {
		t.Fatal("failure aggregation leaked callback chains or lost classification", err)
	}
	for _, state := range scheduler.States() {
		old := scheduleFind(t, states, state.ID)
		if state.Updating || state.AppliedSnapshotID != old.AppliedSnapshotID {
			t.Fatal("worker not joined or applied mutated")
		}
		switch state.ID {
		case "a":
			if state.LastError != "fetch_failed" || state.Downloaded || state.Validated || state.LatestSnapshotID != old.LatestSnapshotID {
				t.Fatal("fetch failure lost old candidate")
			}
		case "b":
			if state.LastError != "validation_failed" || !state.Downloaded || state.Validated || state.PendingSnapshotID != old.PendingSnapshotID {
				t.Fatal("validation failure lost pending")
			}
		case "c":
			if state.LastError != "" || !state.Downloaded || !state.Validated || state.LatestSnapshotID != "new-c" || state.PendingSnapshotID != "new-c" {
				t.Fatal("failed peer prevented successful source")
			}
		}
	}
}
func TestUpdatePreCancelledAndNilContextNoEffects(t *testing.T) {
	var clocks, callbacks atomic.Int32
	s := mustSchedule(t, []SourceState{scheduleState("a")}, func() time.Time { clocks.Add(1); return scheduleNow() }, func(context.Context, config.ID) ([]byte, error) { callbacks.Add(1); return nil, nil }, func(context.Context, config.ID, []byte) (config.ID, error) { callbacks.Add(1); return "candidate", nil })
	before := s.States()
	if clocks.Load() != 0 || callbacks.Load() != 0 {
		t.Fatal("construction/getter invoked dependency")
	}
	if err := s.RunDue(nil); err != ErrInvalidSchedule {
		t.Fatal("nil context classification", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	for _, ctx := range []context.Context{cancelled, expired} {
		err := s.RunDue(ctx)
		if !errors.Is(err, ErrUpdateFailed) || !errors.Is(err, ctx.Err()) {
			t.Fatal("pre-cancelled context cause lost", err)
		}
	}
	if clocks.Load() != 0 || callbacks.Load() != 0 || !reflect.DeepEqual(before, s.States()) {
		t.Fatal("pre-cancelled/nil context consumed work or clock")
	}
}
func TestUpdateSuccessAfterCancellationCannotPublish(t *testing.T) {
	for _, stage := range []string{"fetch", "validate"} {
		t.Run(stage, func(t *testing.T) {
			guard := scheduleContext(t)
			ctx, cancel := context.WithCancel(guard)
			defer cancel()
			var clocks, validations atomic.Int32
			old := scheduleState("a")
			s := mustSchedule(t, []SourceState{old}, func() time.Time { clocks.Add(1); return scheduleNow() }, func(got context.Context, _ config.ID) ([]byte, error) {
				if got != ctx {
					t.Error("fetch context replaced")
				}
				if stage == "fetch" {
					cancel()
				}
				return []byte("completed bytes"), nil
			}, func(got context.Context, _ config.ID, _ []byte) (config.ID, error) {
				validations.Add(1)
				if got != ctx {
					t.Error("validation context replaced")
				}
				cancel()
				return "new-but-cancelled", nil
			})
			err := scheduleReceive(t, guard, scheduleRun(s, ctx))
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUpdateFailed) {
				t.Fatal("successful late callback ignored cancellation", err)
			}
			state := scheduleFind(t, s.States(), "a")
			if state.Updating || state.Validated || state.Downloaded != (stage == "validate") || state.LastError != "cancelled" || state.LatestSnapshotID != old.LatestSnapshotID || state.PendingSnapshotID != old.PendingSnapshotID || state.AppliedSnapshotID != old.AppliedSnapshotID || !state.LastSuccess.Equal(old.LastSuccess) {
				t.Fatal("cancelled success advanced accepted stage/candidate")
			}
			if clocks.Load() != 2 || validations.Load() != map[string]int32{"fetch": 0, "validate": 1}[stage] {
				t.Fatal("cancelled completion sampled clock or entered unexpected validation")
			}
		})
	}
}
func TestUpdateCallbackContextErrorsRemainLocal(t *testing.T) {
	for _, stage := range []string{"fetch", "validate"} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
			t.Run(stage+"/"+cause.Error(), func(t *testing.T) {
				ctx := scheduleContext(t)
				private := errors.New("private source body and URL")
				callbackError := errors.Join(private, cause)
				s := mustSchedule(t, []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}, scheduleNow, func(_ context.Context, id config.ID) ([]byte, error) {
					if id == "a" && stage == "fetch" {
						return nil, callbackError
					}
					return []byte("provider"), nil
				}, func(_ context.Context, id config.ID, _ []byte) (config.ID, error) {
					if id == "a" {
						return "discarded", callbackError
					}
					return config.ID("new-" + string(id)), nil
				})
				err := s.RunDue(ctx)
				if ctx.Err() != nil || !errors.Is(err, ErrUpdateFailed) || !errors.Is(err, cause) || errors.Is(err, private) || strings.Contains(err.Error(), "private") {
					t.Fatal("callback context failure cancelled peers or leaked chain", err)
				}
				a := scheduleFind(t, s.States(), "a")
				if a.LastError != "cancelled" || a.Validated || a.Downloaded != (stage == "validate") {
					t.Fatal("callback context cause stage mismatch")
				}
				for _, id := range []config.ID{"b", "c"} {
					if state := scheduleFind(t, s.States(), id); state.LatestSnapshotID != config.ID("new-"+string(id)) || !state.Validated {
						t.Fatal("local callback context error stopped fixed batch")
					}
				}
			})
		}
	}
}
func TestUpdateClockAndCallbacksCanReenterGetters(t *testing.T) {
	ctx := scheduleContext(t)
	var s *Scheduler
	var clockCalls atomic.Int32
	now := func() time.Time {
		clockCalls.Add(1)
		states := s.States()
		if len(states) != 1 {
			t.Error("clock cannot see states")
		}
		if !errors.Is(s.RunDue(ctx), ErrBusy) {
			t.Error("clock reentrant run not busy")
		}
		return scheduleNow()
	}
	fetch := func(got context.Context, _ config.ID) ([]byte, error) {
		states := s.States()
		if !states[0].Updating || states[0].Downloaded {
			t.Error("fetch getter stage mismatch")
		}
		*states[0].UpdateHours = 99
		if !errors.Is(s.RunDue(ctx), ErrBusy) {
			t.Error("fetch reentrant run not busy")
		}
		if got != ctx {
			t.Error("fetch context changed")
		}
		return []byte("unchanged bytes"), nil
	}
	validate := func(got context.Context, _ config.ID, raw []byte) (config.ID, error) {
		states := s.States()
		if !states[0].Updating || !states[0].Downloaded || states[0].Validated || string(raw) != "unchanged bytes" || got != ctx {
			t.Error("validation reentrant getter/context mismatch")
		}
		if !errors.Is(s.RunDue(ctx), ErrBusy) {
			t.Error("validation reentrant run not busy")
		}
		return "candidate", nil
	}
	s = mustSchedule(t, []SourceState{scheduleState("a")}, now, fetch, validate)
	if err := scheduleReceive(t, ctx, scheduleRun(s, ctx)); err != nil {
		t.Fatal("reentrant callbacks deadlocked/failed", err)
	}
	if clockCalls.Load() != 3 || *s.States()[0].UpdateHours != 1 {
		t.Fatal("reentrancy changed scheduling/clock count")
	}
}
func TestUpdateFixedCutoffDoesNotChaseNewlyDue(t *testing.T) {
	guard := scheduleContext(t)
	clock := &scheduleClock{value: scheduleNow()}
	a, b, c := scheduleState("a"), scheduleState("b"), scheduleState("c")
	c.NextDue = scheduleNow().Add(time.Hour)
	entered := make(chan config.ID, 3)
	release := make(chan struct{})
	var aCalls, bCalls, cCalls atomic.Int32
	fetch := func(ctx context.Context, id config.ID) ([]byte, error) {
		switch id {
		case "a":
			aCalls.Add(1)
		case "b":
			bCalls.Add(1)
		case "c":
			cCalls.Add(1)
		}
		entered <- id
		select {
		case <-release:
			return []byte("provider"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s := mustSchedule(t, []SourceState{a, b, c}, clock.Now, fetch, scheduleValidate)
	done := scheduleRun(s, guard)
	first, second := scheduleReceive(t, guard, entered), scheduleReceive(t, guard, entered)
	if first == "c" || second == "c" || first == second {
		t.Fatal("wrong initial cutoff batch")
	}
	clock.Set(scheduleNow().Add(2 * time.Hour))
	close(release)
	if err := scheduleReceive(t, guard, done); err != nil {
		t.Fatal(err)
	}
	if aCalls.Load() != 1 || bCalls.Load() != 1 || cCalls.Load() != 0 || !reflect.DeepEqual(scheduleFind(t, s.States(), "c"), c) {
		t.Fatal("run chased newly due sources or replayed old slots")
	}
	if err := s.RunDue(guard); err != nil {
		t.Fatal(err)
	}
	if aCalls.Load() != 2 || bCalls.Load() != 2 || cCalls.Load() != 1 {
		t.Fatal("next explicit run did not acquire newly due fixed batch")
	}
}
func TestUpdateQueuedSourceReservesAtActualStart(t *testing.T) {
	ctx := scheduleContext(t)
	clock := &scheduleClock{value: scheduleNow()}
	entered := make(chan config.ID, 3)
	gates := map[config.ID]chan struct{}{"a": make(chan struct{}), "b": make(chan struct{}), "c": make(chan struct{})}
	validate := func(ctx context.Context, id config.ID, _ []byte) (config.ID, error) {
		entered <- id
		select {
		case <-gates[id]:
			return config.ID("new-" + string(id)), nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s := mustSchedule(t, []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}, clock.Now, scheduleFetch, validate)
	done := scheduleRun(s, ctx)
	first, second := scheduleReceive(t, ctx, entered), scheduleReceive(t, ctx, entered)
	queued := scheduleFind(t, s.States(), "c")
	if !queued.LastAttempt.IsZero() || !queued.NextDue.IsZero() || queued.Updating {
		t.Fatal("queued source consumed due slot before worker start")
	}
	later := scheduleNow().Add(30 * time.Minute)
	clock.Set(later)
	close(gates[first])
	third := scheduleReceive(t, ctx, entered)
	if third != "c" {
		t.Fatal("remaining due source not started")
	}
	started := scheduleFind(t, s.States(), "c")
	if !started.LastAttempt.Equal(later) || !started.NextDue.Equal(later.Add(time.Hour)) {
		t.Fatal("queued source reserved using stale batch cutoff")
	}
	close(gates[second])
	close(gates[third])
	if err := scheduleReceive(t, ctx, done); err != nil {
		t.Fatal(err)
	}
}
func TestUpdateRejectsIllegalPersistedState(t *testing.T) {
	for _, mutate := range []func(*SourceState){func(s *SourceState) { s.Updating = true }, func(s *SourceState) { s.LatestSnapshotID = "bad id" }, func(s *SourceState) { s.PendingSnapshotID = "bad id" }, func(s *SourceState) { s.AppliedSnapshotID = "bad id" }, func(s *SourceState) { s.LastError = "private error" }, func(s *SourceState) { s.NextDue = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, func(s *SourceState) { s.LastSuccess = time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC) }, func(s *SourceState) { s.LastAttempt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }} {
		state := scheduleState("a")
		mutate(&state)
		scheduler, err := New([]SourceState{state}, scheduleNow, scheduleFetch, scheduleValidate)
		if scheduler != nil || err != ErrInvalidSchedule {
			t.Fatal("invalid input state accepted")
		}
	}
	emptyIDs := SourceState{ID: "a", UpdateHours: scheduleHours(uint64(math.MaxInt64 / int64(time.Hour)))}
	mustSchedule(t, []SourceState{emptyIDs}, scheduleNow, scheduleFetch, scheduleValidate)
}
func TestUpdateInvalidClockAndDeadlineBoundaries(t *testing.T) {
	old := scheduleState("a")
	var callbacks atomic.Int32
	for _, cutoff := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		s := mustSchedule(t, []SourceState{old}, func() time.Time { return cutoff }, func(context.Context, config.ID) ([]byte, error) { callbacks.Add(1); return nil, nil }, scheduleValidate)
		before := s.States()
		err := s.RunDue(context.Background())
		if err != ErrInvalidSchedule || errors.Is(err, ErrUpdateFailed) || !reflect.DeepEqual(before, s.States()) || callbacks.Load() != 0 {
			t.Fatal("invalid cutoff mutated state or wrong classification", err)
		}
	}
	for _, name := range []string{"start-zero", "start-range", "deadline"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			start := scheduleNow()
			if name == "deadline" {
				start = time.Date(9999, 12, 31, 23, 30, 0, 0, time.UTC)
			}
			now := func() time.Time {
				n := calls.Add(1)
				if n == 1 {
					return start
				}
				switch name {
				case "start-zero":
					return time.Time{}
				case "start-range":
					return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
				default:
					return start
				}
			}
			s := mustSchedule(t, []SourceState{old}, now, func(context.Context, config.ID) ([]byte, error) { callbacks.Add(1); return nil, nil }, scheduleValidate)
			before := s.States()
			err := s.RunDue(context.Background())
			if !errors.Is(err, ErrInvalidSchedule) || !errors.Is(err, ErrUpdateFailed) || !reflect.DeepEqual(before, s.States()) || callbacks.Load() != 0 {
				t.Fatal("invalid start/deadline consumed reservation", err)
			}
		})
	}
	var calls atomic.Int32
	now := func() time.Time {
		if calls.Add(1) == 3 {
			return time.Time{}
		}
		return scheduleNow()
	}
	s := mustSchedule(t, []SourceState{old}, now, scheduleFetch, scheduleValidate)
	err := s.RunDue(context.Background())
	state := s.States()[0]
	if !errors.Is(err, ErrInvalidSchedule) || !errors.Is(err, ErrUpdateFailed) || state.Updating || !state.Downloaded || state.Validated || state.LastError != "validation_failed" || state.LatestSnapshotID != old.LatestSnapshotID || state.PendingSnapshotID != old.PendingSnapshotID || state.AppliedSnapshotID != old.AppliedSnapshotID || state.LastSuccess != old.LastSuccess || !state.NextDue.Equal(scheduleNow().Add(time.Hour)) {
		t.Fatal("invalid completion clock published candidate/lost accepted download", err)
	}
}
func TestUpdateBadWorkerClockContinuesPeersAndStripsMonotonic(t *testing.T) {
	var calls atomic.Int32
	now := func() time.Time {
		if calls.Add(1) == 2 {
			return time.Time{}
		}
		return scheduleNow()
	}
	s := mustSchedule(t, []SourceState{scheduleState("a"), scheduleState("b"), scheduleState("c")}, now, scheduleFetch, scheduleValidate)
	err := s.RunDue(scheduleContext(t))
	if !errors.Is(err, ErrUpdateFailed) || !errors.Is(err, ErrInvalidSchedule) {
		t.Fatal(err)
	}
	unchanged, success := 0, 0
	for _, state := range s.States() {
		if state.LastAttempt.IsZero() {
			unchanged++
		} else if state.Validated {
			success++
		}
	}
	if unchanged != 1 || success != 2 {
		t.Fatal("invalid one-worker clock prevented batch peers")
	}
	stamp := time.Now()
	state := scheduleState("a")
	state.NextDue = stamp.Add(-time.Hour)
	state.LastAttempt = stamp.Add(-2 * time.Hour)
	s = mustSchedule(t, []SourceState{state}, func() time.Time { return stamp }, scheduleFetch, scheduleValidate)
	if s.States()[0].NextDue != state.NextDue.Round(0).UTC() {
		t.Fatal("input monotonic/zone data retained")
	}
	if err := s.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.States()[0].LastAttempt != stamp.Round(0).UTC() || s.States()[0].LastSuccess != stamp.Round(0).UTC() {
		t.Fatal("sampled wall timestamps retain monotonic/location state")
	}
}
func TestUpdateCancellationCausesAreBounded(t *testing.T) {
	private := errors.New("private nested callback body")
	s := mustSchedule(t, []SourceState{scheduleState("a")}, scheduleNow, func(context.Context, config.ID) ([]byte, error) {
		return nil, fmt.Errorf("private URL: %w", errors.Join(private, context.Canceled, context.DeadlineExceeded))
	}, scheduleValidate)
	err := s.RunDue(context.Background())
	if !errors.Is(err, ErrUpdateFailed) || !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, private) || strings.Contains(err.Error(), "private") {
		t.Fatal("callback context aggregation exposed arbitrary chain/lost known causes", err)
	}
}
