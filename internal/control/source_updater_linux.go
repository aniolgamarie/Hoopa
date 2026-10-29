//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

// SourceUpdater owns asynchronous acquisition for one Coordinator lifetime.
// Options are trusted local fetch policy, never caller/provider JSON. New does
// not resume old operations, perform IO, start a timer or fetch any source.
type SourceUpdater struct {
	c               *Coordinator
	ctx             context.Context
	cancel          context.CancelFunc
	options         subscription.FetchOptions
	workers         sync.WaitGroup
	scheduler       sync.WaitGroup
	active          map[config.ID]config.ID // operation -> source, protected by c.mu
	closed          bool                    // protected by c.mu
	now             func() time.Time
	scheduleStarted bool
	scheduleState   SourceScheduleStatus
}

// ValidateSourceUpdateRequest checks the versioned parameter schema only. It is
// shared by transport/offline validation and cannot authorize an acquisition.
func ValidateSourceUpdateRequest(r Request) error { _, err := parseSourceUpdate(r); return err }

func NewSourceUpdater(c *Coordinator, lifetime context.Context, options subscription.FetchOptions) (*SourceUpdater, error) {
	if c == nil || c.coordinatorState == nil {
		return nil, config.ErrClosed
	}
	if lifetime == nil || options.Timeout < 0 || options.MaxBytes < 0 || options.MaxBytes > subscription.DefaultMaxBytes {
		return nil, ErrRequest
	}
	if err := lifetime.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return nil, config.ErrClosed
	}
	if c.sourceStop != nil {
		return nil, ErrConflict
	}
	ctx, cancel := context.WithCancel(lifetime)
	u := &SourceUpdater{c: c, ctx: ctx, cancel: cancel, options: options, active: map[config.ID]config.ID{}, now: time.Now}
	c.sourceStop = func() { u.closed = true; cancel() }
	c.sourceWait = func() { u.workers.Wait(); u.scheduler.Wait() }
	c.sourceAlive = func(id config.ID) bool { _, alive := u.active[id]; return alive }
	u.scheduleState.State = "inactive"
	c.sourceSchedule = func() SourceScheduleStatus {
		state := u.scheduleState
		if state.LastCheckedAt != nil {
			checked := *state.LastCheckedAt
			state.LastCheckedAt = &checked
		}
		return state
	}
	return u, nil
}

// Close rejects new admissions, cancels and joins original workers. Coordinator
// Close also performs this ordering before releasing its Store writer lifetime.
// Never call Close from a callback that already holds the coordinator mutex.
func (u *SourceUpdater) Close() {
	if u == nil || u.c == nil {
		return
	}
	u.c.mu.Lock()
	u.closed = true
	u.cancel()
	u.c.mu.Unlock()
	u.workers.Wait()
	u.scheduler.Wait()
}

// Update returns the durable reservation, not an assertion of completion. Replay
// always precedes new-input checks and never creates another worker. Fetch and
// provider parsing run after releasing mu using frozen owned inputs.
func (u *SourceUpdater) Update(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
	return u.update(ctx, raw, nil)
}

// eligibility is a service-owned read-only due recheck, never request-supplied
// code. It runs under the same writer boundary as the unique Journal reservation.
func (u *SourceUpdater) update(ctx context.Context, raw []byte, eligibility func(config.Subscription, sourceHistory) bool) (StoredOperation, bool, error) {
	fail := func(err error) (StoredOperation, bool, error) { return StoredOperation{}, false, err }
	if u == nil || u.c == nil {
		return fail(config.ErrClosed)
	}
	request, err := ParseRequest(raw)
	if err != nil {
		return fail(err)
	}
	c := u.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		return fail(config.ErrClosed)
	}
	if old, err := c.journal.Lookup(request); !errors.Is(err, ErrOperationNotFound) {
		return old, false, err
	}
	if u.closed || c.serviceQuiesced {
		return fail(config.ErrClosed)
	}
	if ctx == nil {
		return fail(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := u.ctx.Err(); err != nil {
		return fail(err)
	}
	p, err := parseSourceUpdate(request)
	if err != nil {
		return fail(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return fail(err)
	}
	if base.Revision != p.Revision {
		return fail(ErrConflict)
	}
	var committed *config.Intent
	if p.Intent == nil {
		committed, err = c.committedSourceIntentLocked(base)
		if err != nil {
			return fail(err)
		}
	}
	source, err := sourceUpdateDefinition(p, committed)
	if err != nil {
		return fail(err)
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return fail(err)
	}
	history, err := c.sourceHistoryLocked(settings)
	if err != nil {
		return fail(err)
	}
	if eligibility != nil && !eligibility(source, history) {
		return fail(ErrConflict)
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return fail(err)
	}
	for _, id := range ids {
		op, err := c.journal.Get(id)
		if err != nil {
			return fail(err)
		}
		if terminal(op.Record.Phase) {
			continue
		}
		if _, alive := u.active[id]; !alive || op.Record.Identity.Operation != "subscription.update" {
			return fail(ErrRecoveryRequired)
		}
	}
	if len(u.active) >= 2 {
		return fail(ErrConflict)
	}
	for _, activeSource := range u.active {
		if activeSource == source.ID {
			return fail(ErrConflict)
		}
	}
	sequence := uint64(1)
	rules := p.RegionRules
	if previous, exists := history.Attempt[source.ID]; exists {
		sequence = history.Entries[previous].Context.Sequence + 1
		if !p.HasRegionRules {
			rules = history.Entries[previous].Context.RegionRules
		}
	}
	if rules == nil {
		rules = []subscription.RegionRule{}
	}
	prepared, err := subscription.PrepareFetchSource(ctx, source.Source)
	if err != nil {
		return fail(err)
	}
	start := u.now().UTC()
	context := sourceUpdateContext{Version: 1, Kind: "source-update-context", OperationID: request.OperationID(), RequestDigest: request.Digest(), Base: base, Source: source, RegionRules: rules, SettingsSHA256: settings.Digest(), PreparedSHA256: prepared.Digest(), Sequence: sequence, StartedAt: start.Format(time.RFC3339Nano)}
	if source.UpdateHours != nil {
		due := start.Add(time.Duration(*source.UpdateHours) * time.Hour).Format(time.RFC3339Nano)
		context.NextDue = &due
	}
	contextRaw, err := encodeSourceUpdateContext(context)
	if err != nil {
		return fail(ErrRequest)
	}
	if err := c.store.CheckOperationAdmission(); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := u.ctx.Err(); err != nil {
		return fail(err)
	}
	contextSHA, err := c.store.PublishSourceAsset(contextRaw)
	if err != nil {
		return fail(err)
	}
	receipt := sourceReceipt(context, contextSHA)
	result, err := encodeSourceUpdateResult(request.ID(), receipt)
	if err != nil {
		return fail(err)
	}
	// Context publication is not admission. A cancelled caller may leave only
	// this unreferenced immutable asset; it never reserves an attempt or fetches.
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := u.ctx.Err(); err != nil {
		return fail(err)
	}
	intent := Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, CandidateDigest: contextSHA, ImpactDigest: hash(request.params)}
	stored, made, err := c.journal.Admit(request, intent, result)
	if err != nil || !made {
		return stored, made, err
	}
	// Add under the same lock that closes admission before any Wait can begin.
	u.active[request.OperationID()] = source.ID
	u.workers.Add(1)
	go u.run(stored, context, receipt, settings, prepared)
	return stored, true, nil
}

func (u *SourceUpdater) advanceLocked(stored StoredOperation, receipt sourceUpdateReceipt, phase Phase) (StoredOperation, sourceUpdateReceipt, error) {
	return u.c.advanceSourceLocked(stored, receipt, phase)
}

func (c *Coordinator) advanceSourceLocked(stored StoredOperation, receipt sourceUpdateReceipt, phase Phase) (StoredOperation, sourceUpdateReceipt, error) {
	receipt.Phase = phase
	raw, err := encodeSourceUpdateResult(stored.Record.Identity.RequestID, receipt)
	if err != nil {
		return stored, receipt, err
	}
	next, err := c.journal.Advance(stored.Record.Identity.OperationID, stored.SHA256, phase, []byte(`{}`), raw)
	if err != nil {
		return stored, receipt, err
	}
	return next, receipt, nil
}

func (u *SourceUpdater) advance(stored StoredOperation, receipt sourceUpdateReceipt, phase Phase) (StoredOperation, sourceUpdateReceipt, error) {
	u.c.mu.Lock()
	defer u.c.mu.Unlock()
	return u.advanceLocked(stored, receipt, phase)
}

func (u *SourceUpdater) fail(stored StoredOperation, receipt sourceUpdateReceipt, reason string) {
	u.c.mu.Lock()
	defer u.c.mu.Unlock()
	u.failLocked(stored, receipt, reason)
}

func (u *SourceUpdater) failLocked(stored StoredOperation, receipt sourceUpdateReceipt, reason string) {
	finished := u.now().UTC().Format(time.RFC3339Nano)
	receipt.FinishedAt, receipt.Reason = &finished, reason
	receipt.AssetSHA256, receipt.SnapshotID, receipt.Validated = "", "", false
	next, updated, err := u.advanceLocked(stored, receipt, RollingBack)
	if err == nil {
		_, _, _ = u.advanceLocked(next, updated, RolledBack)
	}
}

func (u *SourceUpdater) run(stored StoredOperation, context sourceUpdateContext, receipt sourceUpdateReceipt, settings SourceSettings, prepared subscription.PreparedFetchSource) {
	defer u.workers.Done()
	defer func() { u.c.mu.Lock(); delete(u.active, context.OperationID); u.c.mu.Unlock() }()
	var err error
	for _, phase := range []Phase{Validated, Applying} {
		if u.ctx.Err() != nil {
			u.fail(stored, receipt, "cancelled")
			return
		}
		stored, receipt, err = u.advance(stored, receipt, phase)
		if err != nil {
			return
		}
	}
	raw, err := subscription.FetchPrepared(u.ctx, prepared, u.options)
	if err != nil {
		reason := "download_failed"
		if u.ctx.Err() != nil {
			reason = "cancelled"
		}
		u.fail(stored, receipt, reason)
		return
	}
	receipt.Downloaded = true
	stored, receipt, err = u.advance(stored, receipt, Verifying)
	if err != nil {
		return
	}
	key, err := settings.SensitiveIdentityKey()
	if err != nil {
		u.fail(stored, receipt, "input_changed")
		return
	}
	snapshot, err := subscription.Parse(raw, subscription.ParseOptions{SourceID: context.Source.ID, IdentityKey: key, RegionRules: context.RegionRules})
	if err != nil || len(snapshot.Nodes) == 0 {
		u.fail(stored, receipt, "validation_failed")
		return
	}
	asset, err := snapshot.SensitiveAssetBytes()
	if err != nil {
		u.fail(stored, receipt, "validation_failed")
		return
	}
	c := u.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if u.ctx.Err() != nil {
		u.failLocked(stored, receipt, "cancelled")
		return
	}
	if err := c.revalidateSourceLocked(u.ctx, context); err != nil {
		u.failLocked(stored, receipt, "input_changed")
		return
	}
	assetSHA, err := c.store.PublishSourceAsset(asset)
	if err != nil {
		// An uncertain write poisons the Store; never append a guessed failure.
		if !errors.Is(err, config.ErrUncertain) {
			u.failLocked(stored, receipt, "storage_failed")
		}
		return
	}
	finished := u.now().UTC().Format(time.RFC3339Nano)
	receipt.AssetSHA256, receipt.SnapshotID, receipt.FinishedAt = assetSHA, string(snapshot.ID), &finished
	stored, receipt, err = u.advanceLocked(stored, receipt, Publishing)
	if err != nil {
		return
	}
	receipt.Validated = true
	_, _, _ = u.advanceLocked(stored, receipt, Committed)
}

func (c *Coordinator) revalidateSourceLocked(ctx context.Context, original sourceUpdateContext) error {
	base, err := c.store.ReadActive()
	if err != nil || !sourceContextMatchesBase(original, base) {
		return ErrConflict
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil || settings.Digest() != original.SettingsSHA256 {
		return ErrConflict
	}
	op, err := c.journal.Get(original.OperationID)
	if err != nil {
		return err
	}
	entry, err := c.sourceEntryLocked(op, settings)
	if err != nil || !reflect.DeepEqual(entry.Context, original) {
		return ErrConflict
	}
	request, err := ParseRequest(op.Record.Request)
	if err != nil {
		return err
	}
	p, err := parseSourceUpdate(request)
	if err != nil {
		return err
	}
	var committed *config.Intent
	if p.Intent == nil {
		committed, err = c.committedSourceIntentLocked(base)
		if err != nil {
			return err
		}
	}
	source, err := sourceUpdateDefinition(p, committed)
	if err != nil || !reflect.DeepEqual(source, original.Source) {
		return ErrConflict
	}
	prepared, err := subscription.PrepareFetchSource(ctx, source.Source)
	if err != nil || prepared.Digest() != original.PreparedSHA256 {
		return ErrConflict
	}
	return ctx.Err()
}
