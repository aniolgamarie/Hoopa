// Package schedule runs bounded in-memory subscription update batches. Durable
// reservation/recovery and applying a candidate belong to the service layer.
package schedule

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"hoopa/internal/config"
)

var (
	ErrBusy            = errors.New("UPDATE_BUSY")
	ErrUpdateFailed    = errors.New("UPDATE_FAILED")
	ErrInvalidSchedule = errors.New("SCHEDULE_INVALID")
)

type SourceState struct {
	ID                                                     config.ID
	UpdateHours                                            *uint64
	NextDue, LastAttempt, LastSuccess                      time.Time
	Updating, Downloaded, Validated                        bool
	LatestSnapshotID, PendingSnapshotID, AppliedSnapshotID config.ID
	LastError                                              string
}
type FetchFunc func(context.Context, config.ID) ([]byte, error)
type ValidateFunc func(context.Context, config.ID, []byte) (config.ID, error)
type Scheduler struct {
	mu       sync.Mutex
	states   []SourceState
	running  bool
	now      func() time.Time
	fetch    FetchFunc
	validate ValidateFunc
}

func New(states []SourceState, now func() time.Time, fetch FetchFunc, validate ValidateFunc) (*Scheduler, error) {
	if now == nil || fetch == nil || validate == nil {
		return nil, ErrInvalidSchedule
	}
	copy := make([]SourceState, len(states))
	seen := map[config.ID]bool{}
	for i, state := range states {
		if !validID(state.ID) || seen[state.ID] || state.Updating {
			return nil, ErrInvalidSchedule
		}
		seen[state.ID] = true
		if state.UpdateHours != nil {
			if *state.UpdateHours == 0 || *state.UpdateHours > uint64(math.MaxInt64/int64(time.Hour)) {
				return nil, ErrInvalidSchedule
			}
			hours := *state.UpdateHours
			state.UpdateHours = &hours
		}
		for _, id := range []config.ID{state.LatestSnapshotID, state.PendingSnapshotID, state.AppliedSnapshotID} {
			if id != "" && !validID(id) {
				return nil, ErrInvalidSchedule
			}
		}
		switch state.LastError {
		case "", "fetch_failed", "validation_failed", "cancelled":
		default:
			return nil, ErrInvalidSchedule
		}
		for _, stamp := range []*time.Time{&state.NextDue, &state.LastAttempt, &state.LastSuccess} {
			if stamp.IsZero() {
				*stamp = time.Time{}
				continue
			}
			value, ok := wallTime(*stamp)
			if !ok {
				return nil, ErrInvalidSchedule
			}
			*stamp = value
		}
		copy[i] = state
	}
	return &Scheduler{states: copy, now: now, fetch: fetch, validate: validate}, nil
}
func (s *Scheduler) States() []SourceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SourceState, len(s.states))
	copy(out, s.states)
	for i := range out {
		if out[i].UpdateHours != nil {
			hours := *out[i].UpdateHours
			out[i].UpdateHours = &hours
		}
	}
	return out
}

// RunDue reserves one fixed cutoff batch in input order and runs at most two
// workers. It holds no state mutex across fetch, validation or clock callbacks.
// A failed callback affects only that source; actual ctx cancellation stops
// unstarted work and the call retains ownership until both workers have joined.
func (s *Scheduler) RunDue(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidSchedule
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return ErrBusy
	}
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	if ctx.Err() != nil {
		return outcomeError(failedFlag | contextFlags(ctx.Err()))
	}
	cutoff, ok := wallTime(s.now())
	if !ok {
		return ErrInvalidSchedule
	}
	if ctx.Err() != nil {
		return outcomeError(failedFlag | contextFlags(ctx.Err()))
	}
	s.mu.Lock()
	batch := make([]int, 0, len(s.states))
	for i, state := range s.states {
		due, _ := DueAt(state.NextDue, cutoff) // New already validated stored times.
		if state.UpdateHours != nil && due {
			batch = append(batch, i)
		}
	}
	s.mu.Unlock()
	jobs := make(chan int, len(batch))
	for _, index := range batch {
		jobs <- index
	}
	close(jobs)
	var result atomic.Uint32
	var workers sync.WaitGroup
	count := len(batch)
	if count > 2 {
		count = 2
	}
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				index, open := <-jobs
				if !open {
					return
				}
				if ctx.Err() != nil {
					return
				}
				flags := s.attempt(ctx, index)
				if flags != 0 {
					result.Or(uint32(flags))
				}
			}
		}()
	}
	workers.Wait()
	flags := outcomeFlags(result.Load())
	if ctx.Err() != nil {
		flags |= failedFlag | contextFlags(ctx.Err())
	}
	return outcomeError(flags)
}

// DueAt is the shared pure selection rule for in-memory and durable adapters.
// It reserves nothing: only an accepted durable attempt can consume a due slot.
func DueAt(nextDue, cutoff time.Time) (bool, error) {
	cutoff, ok := wallTime(cutoff)
	if !ok {
		return false, ErrInvalidSchedule
	}
	if nextDue.IsZero() {
		return true, nil
	}
	nextDue, ok = wallTime(nextDue)
	if !ok {
		return false, ErrInvalidSchedule
	}
	return !nextDue.After(cutoff), nil
}
func (s *Scheduler) attempt(ctx context.Context, index int) outcomeFlags {
	if ctx.Err() != nil {
		return failedFlag | contextFlags(ctx.Err())
	}
	start, ok := wallTime(s.now())
	if ctx.Err() != nil {
		return failedFlag | contextFlags(ctx.Err())
	}
	if !ok {
		return failedFlag | invalidFlag
	}
	// No schedule pointer changes after construction. Only this worker can reserve
	// this index, and an invalid start/deadline must leave the full state untouched.
	s.mu.Lock()
	period := time.Duration(*s.states[index].UpdateHours) * time.Hour
	s.mu.Unlock()
	next, ok := wallTime(start.Add(period))
	if !ok {
		return failedFlag | invalidFlag
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return failedFlag | contextFlags(ctx.Err())
	}
	state := &s.states[index]
	id := state.ID
	state.LastAttempt = start
	state.NextDue = next
	state.Updating = true
	state.Downloaded = false
	state.Validated = false
	state.LastError = ""
	s.mu.Unlock()
	raw, err := s.fetch(ctx, id)
	// Cancellation wins before accepting download completion. This does not
	// assert that the network received no bytes; it describes accepted stage state.
	if cause := contextFlags(ctx.Err()) | contextFlags(err); cause != 0 {
		return s.failure(index, "cancelled", failedFlag|cause)
	}
	if err != nil {
		return s.failure(index, "fetch_failed", failedFlag)
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return s.failure(index, "cancelled", failedFlag|contextFlags(ctx.Err()))
	}
	s.states[index].Downloaded = true
	s.mu.Unlock()
	if ctx.Err() != nil {
		return s.failure(index, "cancelled", failedFlag|contextFlags(ctx.Err()))
	}
	snapshot, err := s.validate(ctx, id, raw)
	if cause := contextFlags(ctx.Err()) | contextFlags(err); cause != 0 {
		return s.failure(index, "cancelled", failedFlag|cause)
	}
	if err != nil || !validID(snapshot) {
		return s.failure(index, "validation_failed", failedFlag)
	}
	completed, ok := wallTime(s.now())
	if ctx.Err() != nil {
		return s.failure(index, "cancelled", failedFlag|contextFlags(ctx.Err()))
	}
	// Once fetching succeeded, an invalid completion clock is a failed validation
	// publication: keep Downloaded but retain all prior usable/applied identities.
	if !ok {
		return s.failure(index, "validation_failed", failedFlag|invalidFlag)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		s.states[index].Updating = false
		s.states[index].LastError = "cancelled"
		return failedFlag | contextFlags(ctx.Err())
	}
	state = &s.states[index]
	state.LatestSnapshotID = snapshot
	state.LastSuccess = completed
	state.Validated = true
	state.PendingSnapshotID = snapshot
	if snapshot == state.AppliedSnapshotID {
		state.PendingSnapshotID = ""
	}
	state.LastError = ""
	state.Updating = false
	return 0
}
func (s *Scheduler) failure(index int, code string, flags outcomeFlags) outcomeFlags {
	s.mu.Lock()
	s.states[index].Updating = false
	s.states[index].LastError = code
	s.mu.Unlock()
	return flags
}
func validID(id config.ID) bool { _, err := config.ParseID(string(id)); return err == nil }
func wallTime(value time.Time) (time.Time, bool) {
	if value.IsZero() {
		return time.Time{}, false
	}
	value = value.Round(0).UTC()
	if _, err := value.MarshalJSON(); err != nil {
		return time.Time{}, false
	}
	return value, true
}

// Fold failures into a fixed set of public sentinel codes. Arbitrary callback
// errors and their private chains never escape; standard context causes survive.
type outcomeFlags uint32

const (
	failedFlag outcomeFlags = 1 << iota
	invalidFlag
	cancelledFlag
	deadlineFlag
)

func contextFlags(err error) outcomeFlags {
	var flags outcomeFlags
	if errors.Is(err, context.Canceled) {
		flags |= cancelledFlag
	}
	if errors.Is(err, context.DeadlineExceeded) {
		flags |= deadlineFlag
	}
	return flags
}
func outcomeError(flags outcomeFlags) error {
	if flags == 0 {
		return nil
	}
	causes := []error{}
	if flags&failedFlag != 0 {
		causes = append(causes, ErrUpdateFailed)
	}
	if flags&invalidFlag != 0 {
		causes = append(causes, ErrInvalidSchedule)
	}
	if flags&cancelledFlag != 0 {
		causes = append(causes, context.Canceled)
	}
	if flags&deadlineFlag != 0 {
		causes = append(causes, context.DeadlineExceeded)
	}
	return errors.Join(causes...)
}
