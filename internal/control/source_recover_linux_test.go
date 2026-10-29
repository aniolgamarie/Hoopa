//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	"hoopa/internal/subscription"
)

type sourceRecoverTestState struct {
	entry    sourceHistoryEntry
	snapshot subscription.Snapshot
}

// These are real private files and Journal transitions, with synthetic persisted
// stage facts. No process crash/download is implied; the separate root SIGKILL
// test supplies that evidence. Provider authority is established by actual Parse.
func sourceRecoverTestSeed(t *testing.T, c *Coordinator, id, sourceID string, source config.SubscriptionSource, phase Phase, reason string) sourceRecoverTestState {
	return sourceRecoverTestSeedProvider(t, c, id, sourceID, source, phase, reason, updaterProvider)
}

func sourceRecoverTestSeedProvider(t *testing.T, c *Coordinator, id, sourceID string, source config.SubscriptionSource, phase Phase, reason, provider string) sourceRecoverTestState {
	t.Helper()
	rules := []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "unmatched", Region: "JP"}}
	raw := updaterRequest(t, c, id, sourceID, source, rules)
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseSourceUpdate(request)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := sourceUpdateDefinition(parsed, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := subscription.PrepareFetchSource(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	settings := initializeSettings(t)
	key, err := settings.SensitiveIdentityKey()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := subscription.Parse([]byte(provider), subscription.ParseOptions{SourceID: config.ID(sourceID), IdentityKey: key, RegionRules: rules})
	if err != nil {
		t.Fatal(err)
	}
	started := "2026-10-28T00:00:00Z"
	due := "2026-10-28T01:00:00Z"
	ctx := sourceUpdateContext{Version: 1, Kind: "source-update-context", OperationID: request.OperationID(), RequestDigest: request.Digest(), Base: base, Source: definition, RegionRules: rules, SettingsSHA256: settings.Digest(), PreparedSHA256: prepared.Digest(), Sequence: 1, StartedAt: started, NextDue: &due}
	ctxRaw, err := encodeSourceUpdateContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctxSHA, err := c.store.PublishSourceAsset(ctxRaw)
	if err != nil {
		t.Fatal(err)
	}
	receipt := sourceReceipt(ctx, ctxSHA)
	result, err := encodeSourceUpdateResult(request.ID(), receipt)
	if err != nil {
		t.Fatal(err)
	}
	op, made, err := c.journal.Admit(request, Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, CandidateDigest: ctxSHA, ImpactDigest: hash(request.params)}, result)
	if err != nil || !made {
		t.Fatal("synthetic durable reservation", made, err)
	}
	phases := []Phase{}
	if phase == RollingBack || phase == RolledBack {
		phases = []Phase{Validated, Applying}
		if reason != "download_failed" {
			phases = append(phases, Verifying)
		}
		phases = append(phases, RollingBack)
		if phase == RolledBack {
			phases = append(phases, RolledBack)
		}
	} else {
		for _, next := range []Phase{Validated, Applying, Verifying, Publishing, Committed} {
			if phase == Prepared {
				break
			}
			phases = append(phases, next)
			if next == phase {
				break
			}
		}
	}
	finished := "2026-10-28T00:00:02Z"
	for _, next := range phases {
		switch next {
		case Verifying:
			receipt.Downloaded = true
		case Publishing:
			asset, err := snapshot.SensitiveAssetBytes()
			if err != nil {
				t.Fatal(err)
			}
			sha, err := c.store.PublishSourceAsset(asset)
			if err != nil {
				t.Fatal(err)
			}
			receipt.AssetSHA256 = sha
			receipt.SnapshotID = string(snapshot.ID)
			receipt.FinishedAt = &finished
		case Committed:
			receipt.Validated = true
		case RollingBack:
			receipt.Reason = reason
			receipt.FinishedAt = &finished
		}
		op, receipt, err = c.advanceSourceLocked(op, receipt, next)
		if err != nil {
			t.Fatal("synthetic durable phase", next, err)
		}
	}
	entry, err := c.sourceEntryLocked(op, settings)
	if err != nil {
		t.Fatal("invalid source fixture", err)
	}
	return sourceRecoverTestState{entry: entry, snapshot: snapshot}
}
func sourceRecoverTestCoordinator(t *testing.T) (*Coordinator, config.Paths) {
	t.Helper()
	c, p := coordinatorFixture(t, nil)
	initializePut(t, c, initializeSettings(t))
	return c, p
}
func sourceRecoverTestCall(t *testing.T, c *Coordinator, raw []byte, cut func(string) error) (StoredOperation, bool, error) {
	t.Helper()
	return recoveryCall(t, c, raw, func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
		t.Error("source recovery invoked runtime verifier")
		return platform.ProcessIdentity{}, ErrCapabilityUnsupported
	}, cut)
}
func sourceRecoverTestRequest(t *testing.T, c *Coordinator, target StoredOperation, id string) []byte {
	t.Helper()
	return recoveryRequest(t, id, target.Record.Identity.OperationID, initializeRevision(t, c))
}
func sourceRecoverTestEntry(t *testing.T, c *Coordinator, target config.ID) sourceHistoryEntry {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	op, err := c.journal.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := c.sourceEntryLocked(op, initializeSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	return entry
}
func sourceRecoverTestReject(t *testing.T, c *Coordinator, p config.Paths, raw []byte, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	call, made, err := sourceRecoverTestCall(t, c, raw, nil)
	if made || !reflect.DeepEqual(call, StoredOperation{}) || !errors.Is(err, want) {
		t.Fatalf("recovery rejection made=%v err=%v want=%v", made, err, want)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("preflight rejection wrote settings/assets/journal/active")
	}
}
func sourceRecoverTestRestore(t *testing.T, c *Coordinator, entry sourceHistoryEntry, want subscription.Snapshot) {
	t.Helper()
	raw, err := c.store.ReadSourceAsset(entry.Receipt.AssetSHA256)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := initializeSettings(t).SensitiveIdentityKey()
	restored, err := subscription.RestoreAsset(raw, subscription.ParseOptions{SourceID: entry.Context.Source.ID, IdentityKey: key, RegionRules: entry.Context.RegionRules})
	if err != nil || restored.Validate() != nil || restored.ID != want.ID || !reflect.DeepEqual(restored.Nodes, want.Nodes) {
		t.Fatal("independent real RestoreAsset disagrees", err)
	}
}
func TestSourceRecoveryPublishingCommitsAndRetryIsReadOnly(t *testing.T) {
	gate, source := updaterHTTPS(t)
	c, p := sourceRecoverTestCoordinator(t)
	seed := sourceRecoverTestSeed(t, c, "recover-publishing", "source-a", source, Publishing, "")
	base, _ := c.store.ReadActive()
	c = initializeReopen(t, c, p)
	request := sourceRecoverTestRequest(t, c, seed.entry.Operation, "finish-publishing")
	call, made, err := sourceRecoverTestCall(t, c, request, nil)
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("publishing recovery failed", made, err)
	}
	entry := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
	if entry.Operation.Record.Phase != Committed || !entry.Receipt.Validated || entry.Receipt.AssetSHA256 != seed.entry.Receipt.AssetSHA256 || entry.Receipt.SnapshotID != seed.entry.Receipt.SnapshotID || !reflect.DeepEqual(entry.Receipt.FinishedAt, seed.entry.Receipt.FinishedAt) {
		t.Fatal("recovery replaced candidate or completion time")
	}
	sourceRecoverTestRestore(t, c, entry, seed.snapshot)
	after, _ := c.store.ReadActive()
	if !reflect.DeepEqual(base, after) || gate.calls.Load() != 0 {
		t.Fatal("recovery changed runtime or downloaded")
	}
	before := coordinatorTree(t, p)
	parsed, err := ParseRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	replay, recreated, err := c.recoverBasicLocked(nil, parsed, nil, nil)
	c.mu.Unlock()
	if err != nil || recreated || replay.SHA256 != call.SHA256 || !reflect.DeepEqual(replay.Record, call.Record) {
		t.Fatal("exact recovery replay revalidated or changed result", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("exact replay wrote")
	}
}
func TestSourceRecoveryPublishingChangedInputsRollBack(t *testing.T) {
	for _, kind := range []string{"secret-change", "secret-missing", "base-change"} {
		t.Run(kind, func(t *testing.T) {
			gate, source := updaterHTTPS(t)
			c, p := sourceRecoverTestCoordinator(t)
			seed := sourceRecoverTestSeed(t, c, "changed-"+kind, "source-a", source, Publishing, "")
			switch kind {
			case "secret-change":
				if err := os.WriteFile(source.URLSecretRef, []byte(gate.server.URL+"/changed-token"), 0600); err != nil {
					t.Fatal(err)
				}
			case "secret-missing":
				if err := os.Remove(source.URLSecretRef); err != nil {
					t.Fatal(err)
				}
			case "base-change":
				a, err := c.store.ReadActive()
				if err != nil {
					t.Fatal(err)
				}
				a.Revision = "changed-global-revision"
				if err := c.store.PublishActive(a); err != nil {
					t.Fatal(err)
				}
			}
			c = initializeReopen(t, c, p)
			current, _ := c.store.ReadActive()
			request := sourceRecoverTestRequest(t, c, seed.entry.Operation, "recover-"+kind)
			call, made, err := sourceRecoverTestCall(t, c, request, nil)
			if err != nil || !made || call.Record.Phase != Committed {
				t.Fatal("input change recovery call failed", err)
			}
			entry := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
			if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "input_changed" || !entry.Receipt.Downloaded || entry.Receipt.Validated || entry.Receipt.AssetSHA256 != "" || entry.Receipt.SnapshotID != "" {
				t.Fatal("changed input published candidate")
			}
			after, _ := c.store.ReadActive()
			if !reflect.DeepEqual(current, after) || gate.calls.Load() != 0 {
				t.Fatal("changed-input recovery fetched or changed active")
			}
			// The now-orphan candidate remains private bytes, never deleted as rollback.
			if _, err := c.store.ReadSourceAsset(seed.entry.Receipt.AssetSHA256); err != nil {
				t.Fatal("rollback deleted immutable original asset", err)
			}
		})
	}
}
func TestSourceRecoveryEarlyPhasesInterruptWithoutFetch(t *testing.T) {
	for _, phase := range []Phase{Prepared, Validated, Applying, Verifying} {
		t.Run(string(phase), func(t *testing.T) {
			gate, source := updaterHTTPS(t)
			c, p := sourceRecoverTestCoordinator(t)
			seed := sourceRecoverTestSeed(t, c, "early-"+string(phase), "source-a", source, phase, "")
			c = initializeReopen(t, c, p)
			request := sourceRecoverTestRequest(t, c, seed.entry.Operation, "recover-early")
			call, made, err := sourceRecoverTestCall(t, c, request, nil)
			if err != nil || !made || call.Record.Phase != Committed {
				t.Fatal("early interruption recovery failed", err)
			}
			entry := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
			if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "interrupted" || entry.Receipt.Downloaded != (phase == Verifying) || entry.Receipt.Validated || entry.Receipt.AssetSHA256 != "" || entry.Receipt.SnapshotID != "" || entry.Receipt.FinishedAt == nil {
				t.Fatal("early phase fabricated download/candidate")
			}
			if gate.calls.Load() != 0 {
				t.Fatal("interrupted source fetched again")
			}
		})
	}
}
func TestSourceRecoveryRollingBackPreservesReasonTimeAndDownload(t *testing.T) {
	for _, reason := range []string{"download_failed", "validation_failed", "storage_failed", "cancelled"} {
		t.Run(reason, func(t *testing.T) {
			c, p := sourceRecoverTestCoordinator(t)
			seed := sourceRecoverTestSeed(t, c, "original-rollback", "source-a", updaterFile(t, updaterProvider), RollingBack, reason)
			c = initializeReopen(t, c, p)
			call, made, err := sourceRecoverTestCall(t, c, sourceRecoverTestRequest(t, c, seed.entry.Operation, "continue-rollback"), nil)
			if err != nil || !made || call.Record.Phase != Committed {
				t.Fatal("rollback recovery failed", err)
			}
			entry := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
			if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != reason || entry.Receipt.Downloaded != seed.entry.Receipt.Downloaded || !reflect.DeepEqual(entry.Receipt.FinishedAt, seed.entry.Receipt.FinishedAt) {
				t.Fatal("rollback recovery forged interruption or finish time")
			}
		})
	}
}
func TestSourceRecoveryDamagedDependenciesRejectBeforeAdmission(t *testing.T) {
	for _, kind := range []string{"missing-asset", "bad-asset", "non-authoritative-asset", "snapshot-mismatch", "missing-context", "bad-context", "missing-settings", "bad-settings", "changed-key", "asset-mode", "context-mode"} {
		t.Run(kind, func(t *testing.T) {
			c, p := sourceRecoverTestCoordinator(t)
			seed := sourceRecoverTestSeed(t, c, "damaged-dependency", "source-a", updaterFile(t, updaterProvider), Publishing, "")
			assetPath := filepath.Join(p.State, "source-assets", seed.entry.Receipt.AssetSHA256+".json")
			contextPath := filepath.Join(p.State, "source-assets", seed.entry.Receipt.ContextSHA256+".json")
			settingsPath := filepath.Join(p.State, "credentials", "source-settings-v1.json")
			switch kind {
			case "missing-asset":
				if err := os.Remove(assetPath); err != nil {
					t.Fatal(err)
				}
			case "bad-asset":
				if err := os.WriteFile(assetPath, []byte(`{"forged":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "non-authoritative-asset", "snapshot-mismatch":
				receipt := seed.entry.Receipt
				if kind == "non-authoritative-asset" {
					sha, err := c.store.PublishSourceAsset([]byte(`{"not_a_provider_asset":true}`))
					if err != nil {
						t.Fatal(err)
					}
					receipt.AssetSHA256 = sha
				} else {
					receipt.SnapshotID = "s-forged-valid-token"
				}
				c.mu.Lock()
				next, _, err := c.advanceSourceLocked(seed.entry.Operation, receipt, Publishing)
				c.mu.Unlock()
				if err != nil {
					t.Fatal("synthetic consistent-but-unproved reference", err)
				}
				seed.entry.Operation = next
			case "missing-context":
				if err := os.Remove(contextPath); err != nil {
					t.Fatal(err)
				}
			case "bad-context":
				if err := os.WriteFile(contextPath, []byte(`{"forged":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-settings":
				if err := os.Remove(settingsPath); err != nil {
					t.Fatal(err)
				}
			case "bad-settings":
				if err := os.WriteFile(settingsPath, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-key":
				key, runtime := codecSettingsInputs()
				key[0] ^= 255
				other, err := NewSourceSettings(key, runtime)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := other.SensitiveBytes()
				if err := os.WriteFile(settingsPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "asset-mode":
				if err := os.Chmod(assetPath, 0644); err != nil {
					t.Fatal(err)
				}
			case "context-mode":
				if err := os.Chmod(contextPath, 0644); err != nil {
					t.Fatal(err)
				}
			}
			request := sourceRecoverTestRequest(t, c, seed.entry.Operation, "reject-damaged")
			sourceRecoverTestReject(t, c, p, request, config.ErrReadUnavailable)
		})
	}
}
func TestSourceRecoveryLiveWorkerIsReadOnlyConflict(t *testing.T) {
	gate, source := updaterHTTPS(t)
	c, p, u := updaterFixture(t, subscription.FetchOptions{Transport: gate.server.Client().Transport})
	target := updaterBegin(t, u, updaterRequest(t, c, "actual-live", "source-a", source, nil))
	updaterEntered(t, gate)
	request := sourceRecoverTestRequest(t, c, target, "live-recovery")
	sourceRecoverTestReject(t, c, p, request, ErrConflict)
	if gate.calls.Load() != 1 {
		t.Fatal("live recovery started another request")
	}
	u.Close()
}
func TestSourceRecoveryCompactedTerminalProofAndOldCallSettlement(t *testing.T) {
	gate, source := updaterHTTPS(t)
	c, p := sourceRecoverTestCoordinator(t)
	seed := sourceRecoverTestSeed(t, c, "settlement-target", "source-a", source, Publishing, "")
	unrelated := sourceRecoverTestSeed(t, c, "unrelated-pending", "source-b", source, Prepared, "")
	cut := errors.New("synthetic post-target boundary")
	firstRaw := sourceRecoverTestRequest(t, c, seed.entry.Operation, "old-recovery-call")
	first, made, err := sourceRecoverTestCall(t, c, firstRaw, func(point string) error {
		if point == "recover-target-terminal" {
			return cut
		}
		return nil
	})
	if !made || !errors.Is(err, cut) || first.Record.Phase != Applying {
		t.Fatal("failed to preserve original incomplete recovery call", made, err)
	}
	beforeRetry := coordinatorTree(t, p)
	pendingReplay, remade, err := sourceRecoverTestCall(t, c, firstRaw, nil)
	if err != nil || remade || pendingReplay.SHA256 != first.SHA256 || pendingReplay.Record.Phase != Applying || !reflect.DeepEqual(beforeRetry, coordinatorTree(t, p)) {
		t.Fatal("old call retry resumed incomplete recovery or wrote", err)
	}
	target := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
	if target.Operation.Record.Phase != Committed {
		t.Fatal("cut preceded target completion")
	}
	c.mu.Lock()
	compact, err := c.journal.Compact(target.Context.OperationID, target.Operation.SHA256)
	c.mu.Unlock()
	if err != nil || !compact.Record.Compacted {
		t.Fatal("source target compaction", err)
	}
	c = initializeReopen(t, c, p)
	nextRaw := sourceRecoverTestRequest(t, c, compact, "new-recovery-call")
	next, made, err := sourceRecoverTestCall(t, c, nextRaw, nil)
	if err != nil || !made || next.Record.Phase != Committed {
		t.Fatal("terminal/older-call recovery failed", err)
	}
	old, err := c.journal.Get(first.Record.Identity.OperationID)
	if err != nil || old.Record.Phase != Committed {
		t.Fatal("older bound recovery not settled", err)
	}
	current, err := c.journal.Get(compact.Record.Identity.OperationID)
	if err != nil || current.SHA256 != compact.SHA256 {
		t.Fatal("terminal target was rewritten", err)
	}
	other, err := c.journal.Get(unrelated.entry.Context.OperationID)
	if err != nil || other.SHA256 != unrelated.entry.Operation.SHA256 || !reflect.DeepEqual(other.Record, unrelated.entry.Operation.Record) {
		t.Fatal("unrelated pending source was modified", err)
	}
	sourceRecoverTestRestore(t, c, target, seed.snapshot)
	if gate.calls.Load() != 0 {
		t.Fatal("terminal recovery repeated fetch")
	}
	before := coordinatorTree(t, p)
	replay, recreated, err := sourceRecoverTestCall(t, c, nextRaw, nil)
	if err != nil || recreated || replay.SHA256 != next.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("new-call exact replay wrote", err)
	}
	oldReplay, recreated, err := sourceRecoverTestCall(t, c, firstRaw, nil)
	if err != nil || recreated || oldReplay.SHA256 != old.SHA256 || oldReplay.Record.Phase != Committed || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("settled older identity did not replay its final receipt", err)
	}
	// A *new* recovery of a compacted terminal must still verify candidate bytes,
	// unlike replaying a previously admitted recovery identity.
	if err := os.Remove(filepath.Join(p.State, "source-assets", target.Receipt.AssetSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	sourceRecoverTestReject(t, c, p, sourceRecoverTestRequest(t, c, compact, "terminal-missing-asset"), config.ErrReadUnavailable)
}
func TestSourceRecoveryCompactedFailureStillBindsContext(t *testing.T) {
	c, p := sourceRecoverTestCoordinator(t)
	seed := sourceRecoverTestSeed(t, c, "failed-terminal", "source-a", updaterFile(t, updaterProvider), RolledBack, "download_failed")
	c.mu.Lock()
	compact, err := c.journal.Compact(seed.entry.Context.OperationID, seed.entry.Operation.SHA256)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	call, made, err := sourceRecoverTestCall(t, c, sourceRecoverTestRequest(t, c, compact, "recover-failed-terminal"), nil)
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("failed terminal recovery", err)
	}
	target := sourceRecoverTestEntry(t, c, seed.entry.Context.OperationID)
	if target.Operation.SHA256 != compact.SHA256 || !reflect.DeepEqual(target.Receipt, seed.entry.Receipt) {
		t.Fatal("failed terminal assertion changed")
	}
	if err := os.Remove(filepath.Join(p.State, "source-assets", seed.entry.Receipt.ContextSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	sourceRecoverTestReject(t, c, p, sourceRecoverTestRequest(t, c, compact, "terminal-missing-context"), config.ErrReadUnavailable)
}
func TestSourceRecoveryCancelledAndStaleAreReadOnly(t *testing.T) {
	c, p := sourceRecoverTestCoordinator(t)
	seed := sourceRecoverTestSeed(t, c, "precheck-source", "source-a", updaterFile(t, updaterProvider), Publishing, "")
	requestRaw := sourceRecoverTestRequest(t, c, seed.entry.Operation, "cancelled-recover")
	request, err := ParseRequest(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := coordinatorTree(t, p)
	c.mu.Lock()
	call, made, err := c.recoverBasicLocked(ctx, request, func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
		t.Error("unexpected verify")
		return platform.ProcessIdentity{}, nil
	}, nil)
	c.mu.Unlock()
	if made || !reflect.DeepEqual(call, StoredOperation{}) || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("cancelled recovery wrote", err)
	}
	stale := recoveryRequest(t, "stale-recover", seed.entry.Context.OperationID, "stale-revision")
	sourceRecoverTestReject(t, c, p, stale, ErrConflict)
	// Body bytes and parser authority remain exact throughout all read-only checks.
	got, err := c.store.ReadSourceAsset(seed.entry.Receipt.AssetSHA256)
	want, _ := seed.snapshot.SensitiveAssetBytes()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("prechecks changed provider asset", err)
	}
}
