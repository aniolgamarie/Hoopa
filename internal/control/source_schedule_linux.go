//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/schedule"
)

type sourceDueCandidate struct {
	source      config.Subscription
	previousSHA string
	cutoff      time.Time
}

type SourceScheduleStatus struct {
	State         string  `json:"state"`
	LastCheckedAt *string `json:"last_checked_at"`
	ErrorCode     string  `json:"error_code"`
}

// RunSchedule is explicitly owned by the started management service. It has one
// fixed pulse interval, no backlog queue and no OS scheduler. Both service and
// updater cancellation join this loop before their writer lifetime is released.
func (u *SourceUpdater) RunSchedule(ctx context.Context) error {
	if u == nil || u.c == nil {
		return config.ErrClosed
	}
	if ctx == nil {
		return ErrRequest
	}
	u.c.mu.Lock()
	if u.closed || u.c.store == nil || u.c.serviceQuiesced {
		u.c.mu.Unlock()
		return config.ErrClosed
	}
	if u.scheduleStarted {
		u.c.mu.Unlock()
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		u.c.mu.Unlock()
		return err
	}
	u.scheduleStarted = true
	u.scheduler.Add(1)
	u.c.mu.Unlock()
	defer u.scheduler.Done()
	defer func() {
		u.c.mu.Lock()
		u.scheduleState.State = "stopped"
		u.c.mu.Unlock()
	}()
	for {
		_, err := u.RunDue(ctx)
		u.c.mu.Lock()
		checked := time.Now().UTC().Format(time.RFC3339Nano)
		u.scheduleState = SourceScheduleStatus{State: "waiting", LastCheckedAt: &checked}
		if err != nil {
			u.scheduleState.State = "degraded"
			u.scheduleState.ErrorCode = "update_failed"
			if errors.Is(err, ErrRecoveryRequired) {
				u.scheduleState.ErrorCode = "recovery_required"
			}
		}
		u.c.mu.Unlock()
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-u.ctx.Done():
			timer.Stop()
			return u.ctx.Err()
		case <-timer.C:
		}
	}
}

func sourceDue(source config.Subscription, history sourceHistory, cutoff time.Time) (bool, string, error) {
	if source.UpdateHours == nil {
		return false, "", nil
	}
	var sha string
	if index, ok := history.Attempt[source.ID]; ok {
		entry := history.Entries[index]
		sha = entry.Operation.SHA256
		if !terminal(entry.Operation.Record.Phase) {
			return false, sha, nil
		}
	}
	next, err := sourceEffectiveDue(source, history)
	if err != nil {
		return false, sha, err
	}
	due, err := schedule.DueAt(next, cutoff)
	return due, sha, err
}

// Preserve a durable reserved due. A historical manual acquisition with no
// declared interval still consumes one current interval when automatic updates
// are enabled; use its original start rather than scheduling an immediate second
// download. This derived time never rewrites that immutable attempt's context.
func sourceEffectiveDue(source config.Subscription, history sourceHistory) (time.Time, error) {
	index, ok := history.Attempt[source.ID]
	if source.UpdateHours == nil || !ok {
		return time.Time{}, nil
	}
	entry := history.Entries[index]
	if entry.Context.NextDue != nil {
		value, err := time.Parse(time.RFC3339Nano, *entry.Context.NextDue)
		return value, err
	}
	start, err := time.Parse(time.RFC3339Nano, entry.Context.StartedAt)
	if err != nil {
		return time.Time{}, config.ErrReadUnavailable
	}
	next := start.Add(time.Duration(*source.UpdateHours) * time.Hour)
	if _, err := schedule.DueAt(next, start); err != nil {
		return time.Time{}, err
	}
	return next, nil
}

// RunDue starts at most one new attempt for each source in a fixed captured due
// batch, without waiting for downloads. Only committed Intent enables scheduling.
// Actual admission uses the same updater and rechecks the previous attempt so an
// intervening manual update cannot consume the same interval a second time.
func (u *SourceUpdater) RunDue(ctx context.Context) ([]config.ID, error) {
	if u == nil || u.c == nil {
		return nil, config.ErrClosed
	}
	if ctx == nil {
		return nil, ErrRequest
	}
	u.c.mu.Lock()
	base, batch, err := u.captureDueLocked(ctx)
	u.c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	started := []config.ID{}
	var failures error
	for _, candidate := range batch {
		if err := ctx.Err(); err != nil {
			return started, err
		}
		op, made, err := u.updateDue(ctx, base, candidate)
		if errors.Is(err, ErrConflict) {
			continue // Stale pulse/occupied workers leave the source unreserved.
		}
		if err != nil {
			if errors.Is(err, ErrRecoveryRequired) || errors.Is(err, config.ErrUncertain) || ctx.Err() != nil {
				return started, errors.Join(failures, err)
			}
			failures = errors.Join(failures, err)
			continue
		}
		if made {
			started = append(started, op.Record.Identity.OperationID)
		}
	}
	return started, failures
}

func (u *SourceUpdater) updateDue(ctx context.Context, base config.ActivePointer, candidate sourceDueCandidate) (StoredOperation, bool, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return StoredOperation{}, false, config.ErrReadUnavailable
	}
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": "source-auto-" + hex.EncodeToString(nonce[:]), "operation": "subscription.update", "params": map[string]any{"expected_revision": base.Revision, "subscription_id": candidate.source.ID}})
	if err != nil {
		return StoredOperation{}, false, ErrRequest
	}
	return u.update(ctx, raw, func(current config.Subscription, history sourceHistory) bool {
		if !reflect.DeepEqual(current, candidate.source) {
			return false
		}
		due, previous, err := sourceDue(current, history, candidate.cutoff)
		return err == nil && due && previous == candidate.previousSHA
	})
}

func (u *SourceUpdater) captureDueLocked(ctx context.Context) (config.ActivePointer, []sourceDueCandidate, error) {
	fail := func(err error) (config.ActivePointer, []sourceDueCandidate, error) {
		return config.ActivePointer{}, nil, err
	}
	if u.c.store == nil || u.closed || u.c.serviceQuiesced {
		return fail(config.ErrClosed)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := u.ctx.Err(); err != nil {
		return fail(err)
	}
	cutoff := u.now().Round(0).UTC()
	if _, err := schedule.DueAt(time.Time{}, cutoff); err != nil {
		return fail(err)
	}
	base, err := u.c.store.ReadActive()
	if err != nil {
		return fail(err)
	}
	intent, err := u.c.committedSourceIntentLocked(base)
	if err != nil {
		return fail(err)
	}
	if intent == nil {
		return base, nil, nil
	}
	enabled := false
	for _, source := range intent.Subscriptions {
		enabled = enabled || source.UpdateHours != nil
	}
	if !enabled {
		return base, nil, nil
	}
	settings, err := u.c.sourceSettingsLocked()
	if err != nil {
		return fail(err)
	}
	history, err := u.c.sourceHistoryLocked(settings)
	if err != nil {
		return fail(err)
	}
	ids, err := u.c.journal.IDs()
	if err != nil {
		return fail(err)
	}
	for _, id := range ids {
		op, err := u.c.journal.Get(id)
		if err != nil {
			return fail(err)
		}
		if !terminal(op.Record.Phase) && !u.c.liveSourceUpdateLocked(op) {
			return fail(ErrRecoveryRequired)
		}
	}
	batch := []sourceDueCandidate{}
	for _, source := range intent.Subscriptions {
		due, previous, err := sourceDue(source, history, cutoff)
		if err != nil {
			return fail(err)
		}
		if due {
			batch = append(batch, sourceDueCandidate{source, previous, cutoff})
		}
	}
	return base, batch, nil
}
