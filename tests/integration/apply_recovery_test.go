//go:build p1integration

package integration

import (
	"context"
	"hoopa/internal/config"
	"reflect"
	"testing"
	"time"
)

func TestP1ApplyBindFailureRestoresCompleteOldBundle(t *testing.T) { // F10.
	h := newProductHarness(t)
	ctx := testContext(t)
	before, _ := start(t, h, ctx, false)
	old := need(t, before.ActiveBundle, "complete old bundle")
	fault := faultRequest{Point: "target-entry-bind", Failure: "address-in-use-after-old-stop"}
	must(t, h.InjectFault(ctx, fault))
	r, err := h.Apply(ctx, applyRequest{Identity: request("config.apply", "bind-failure", need(t, before.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
	op := result(t, r, err, "rolled_back")
	if op.Phase != "rolled_back" {
		t.Fatal("failed bind cannot commit target")
	}
	after := observe(t, h, ctx)
	running(t, after)
	assertBundle(t, need(t, after.ActiveBundle, "restored active bundle"), old)
	if need(t, after.Revision, "restored revision") != need(t, before.Revision, "original revision") {
		t.Fatal("rollback did not restore original revision")
	}
	if need(t, after.Generation, "restored generation") != old.Generation {
		t.Fatal("rollback changed old compiled generation")
	}
	ev := evidence(t, h, ctx)
	assertDurableResult(t, ev, op)
	requireFault(t, ev, fault)
	assertOrdering(t, ev)
	requireBoundary(t, ev, op.ID, "old-state-verified")
	// Rollback may launch a new RunID, but it must route using all old rules, selections, assets and auth.
	p := running(t, after)
	for _, entry := range old.Entries {
		traffic(t, h, ctx, "new", "rollback-"+entry, entry, 4096, p.RunID)
	}
}

func TestP1CrashAtEveryDurableApplyBoundary(t *testing.T) { // F11: these are logical seams, not a product file layout.
	artifacts := []struct {
		artifact, phase    string
		rollback, degraded bool
	}{
		{"candidate", "prepared", false, false}, {"operation", "applying", false, false},
		{"launch-intent", "applying", false, false}, {"registration", "verifying", false, false},
		{"operation", "publishing", false, false}, {"active", "publishing", false, false},
		{"operation", "committed", false, false}, {"operation", "rolling_back", true, false},
		{"active", "rolling_back", true, false}, {"operation", "rolled_back", true, false},
		{"operation", "degraded", true, true},
	}
	// One crash at each distinct authoritative publication; T024 separately tests file primitives.
	for _, a := range artifacts {
		cut := "after-publication"
		t.Run(a.artifact+"/"+a.phase, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before, baseline := start(t, h, ctx, false)
			old := need(t, before.ActiveBundle, "old immutable bundle")
			if a.rollback {
				must(t, h.InjectFault(ctx, faultRequest{Point: "target-entry-bind", Failure: "address-in-use-after-old-stop"}))
			}
			if a.degraded {
				must(t, h.InjectFault(ctx, faultRequest{Point: "old-state-verification", Failure: "unverifiable-owner"}))
			}
			fault := faultRequest{Point: "durable-boundary", Artifact: a.artifact, Phase: a.phase, Cut: cut, Failure: "crash-manager"}
			must(t, h.InjectFault(ctx, fault))
			r, err := h.Apply(ctx, applyRequest{Identity: request("config.apply", "crashed-apply", need(t, before.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
			requireInterrupted(t, r, err)
			interrupted := evidence(t, h, ctx)
			requireFault(t, interrupted, fault)
			must(t, h.RestartManager(ctx, "control-only"))
			noMutation(t, interrupted, evidence(t, h, ctx))
			if a.artifact == "candidate" && a.phase == "prepared" {
				// This publication precedes admission of an Operation: there is nothing to Recover by ID.
				after := observe(t, h, ctx)
				sameCore(t, running(t, before), after)
				assertBundle(t, need(t, after.ActiveBundle, "unchanged old bundle"), old)
				if need(t, after.Revision, "unchanged revision") != need(t, before.Revision, "old revision") {
					t.Fatal("unaccepted candidate changed revision")
				}
				if !reflect.DeepEqual(baseline.Operations, interrupted.Operations) || !reflect.DeepEqual(baseline.Launches, interrupted.Launches) || !reflect.DeepEqual(baseline.Signals, interrupted.Signals) {
					t.Fatal("candidate publication admitted an operation or changed runtime")
				}
				for _, op := range interrupted.Operations {
					if op.RequestID == "crashed-apply" {
						t.Fatal("pre-admission request unexpectedly exists")
					}
				}
				// An explicit resubmission must revalidate inputs and the candidate, even if bytes are cached.
				last := uint64(0)
				for _, v := range interrupted.Boundaries {
					if v.Index > last {
						last = v.Index
					}
				}
				r, err = h.Apply(ctx, applyRequest{Identity: request("config.apply", "crashed-apply", need(t, after.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
				op := result(t, r, err, "committed")
				ev := evidence(t, h, ctx)
				requireBoundaryAfter(t, ev, op.ID, "inputs-revalidated", last)
				requireBoundaryAfter(t, ev, op.ID, "candidate-validated", last)
				// The first request never reached admission. This is a newly
				// accepted Apply, not an operation.recover call or its target.
				if r.RecoveryCall != nil {
					t.Fatal("resubmitted Apply fabricated a recovery call")
				}
				assertOperationBinding(t, op, old, nil)
				assertDurableResult(t, ev, op)
				committed := observe(t, h, ctx)
				p := running(t, committed)
				if p.Generation != op.Target.Generation || need(t, committed.Revision, "resubmitted target revision") != requiredTargetRevision(t, op) {
					t.Fatal("resubmitted Apply did not commit the independently observed target")
				}
				assertBundle(t, need(t, committed.ActiveBundle, "resubmitted target bundle"), op.Target)
				for _, entry := range op.Target.Entries {
					trafficForBundle(t, h, ctx, targetBundle, "new", "resubmitted-"+entry, entry, 4096, p.RunID)
				}
				assertOrdering(t, ev)
				return
			}
			r, err = h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "recover-durable-cut"), InterruptedRequestID: "crashed-apply"})
			recoveryState(t, h, ctx, old, need(t, before.Revision, "original revision"), r, err, "crashed-apply", operationFromEvidence(t, interrupted, "crashed-apply"))
			assertOrdering(t, evidence(t, h, ctx))
		})
	}
	for _, point := range []string{"old-stop-issued", "old-stop-observed", "spawn-returned-before-registration", "target-verified", "entry-opening", "entry-opened"} {
		t.Run("runtime/"+point, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before, _ := start(t, h, ctx, false)
			old := need(t, before.ActiveBundle, "old bundle")
			fault := faultRequest{Point: point, Failure: "crash-manager"}
			must(t, h.InjectFault(ctx, fault))
			r, err := h.Apply(ctx, applyRequest{Identity: request("config.apply", "crashed-apply", need(t, before.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
			requireInterrupted(t, r, err)
			interrupted := evidence(t, h, ctx)
			requireFault(t, interrupted, fault)
			must(t, h.RestartManager(ctx, "control-only"))
			noMutation(t, interrupted, evidence(t, h, ctx))
			r, err = h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "recover-runtime-cut"), InterruptedRequestID: "crashed-apply"})
			recoveryState(t, h, ctx, old, need(t, before.Revision, "original revision"), r, err, "crashed-apply", operationFromEvidence(t, interrupted, "crashed-apply"))
			assertOrdering(t, evidence(t, h, ctx))
		})
	}
}

func TestP1UncertainActivePublicationReobservesSameTarget(t *testing.T) { // F12.
	for _, cut := range []string{"temporary-write", "file-sync", "rename", "directory-sync"} {
		t.Run(cut, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before, _ := start(t, h, ctx, false)
			fault := faultRequest{Point: "durable-boundary", Artifact: "active", Phase: "publishing", Cut: cut, Failure: "io-error-uncertain"}
			must(t, h.InjectFault(ctx, fault))
			r, err := h.Apply(ctx, applyRequest{Identity: request("config.apply", "uncertain-active", need(t, before.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
			// A live verified target and an uncertain pointer publication are separate facts.
			must(t, err)
			if r.Outcome != "pending" || r.WireOutcome != "pending" {
				t.Fatal("uncertain publication must retain the actual pending receipt")
			}
			prior := result(t, r, nil, "pending")
			old := need(t, before.ActiveBundle, "old complete bundle")
			assertOperationBinding(t, prior, old, nil)
			targetObs := observe(t, h, ctx)
			assertDegraded(t, targetObs)
			target := independentlyOpenCore(t, targetObs)
			ev := evidence(t, h, ctx)
			requireFault(t, ev, fault)
			if target.Generation == need(t, before.Generation, "old generation") {
				t.Fatal("target was not actually started/verified")
			}
			must(t, h.RestartManager(ctx, "control-only"))
			noMutation(t, ev, evidence(t, h, ctx))
			r, err = h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "recover-uncertain"), InterruptedRequestID: "uncertain-active"})
			must(t, err)
			after := observe(t, h, ctx)
			if r.Outcome == "committed" {
				op := result(t, r, nil, "committed")
				assertRecoveryResult(t, r, evidence(t, h, ctx))
				assertOperationBinding(t, op, old, &prior)
				assertDurableResult(t, evidence(t, h, ctx), op)
				if need(t, after.Revision, "committed target revision") != requiredTargetRevision(t, op) {
					t.Fatal("recovered target revision mismatch")
				}
				sameCore(t, target, after)
				if target.Generation != op.Target.Generation {
					t.Fatal("verified runtime generation differs from committed target")
				}
				assertBundle(t, need(t, after.ActiveBundle, "committed target"), op.Target)
				for _, entry := range op.Target.Entries {
					trafficForBundle(t, h, ctx, targetBundle, "new", "published-"+entry, entry, 4096, target.RunID)
				}
			} else {
				result(t, r, nil, "degraded")
				assertDegraded(t, after)
				preservedOperation(t, evidence(t, h, ctx), "uncertain-active", old, &prior)
			}
			noDuplicateLaunch(t, ev, evidence(t, h, ctx))
		})
	}
}

func TestP1FailedRollbackPreservesEvidenceAndBlocksWrites(t *testing.T) { // F13.
	for _, point := range []string{"old-state-verification", "rollback-active-publication"} {
		t.Run(point, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before, _ := start(t, h, ctx, true)
			must(t, h.InjectFault(ctx, faultRequest{Point: "target-entry-bind", Failure: "address-in-use-after-old-stop"}))
			fault := faultRequest{Point: point, Failure: "io-error-or-unverifiable-owner"}
			must(t, h.InjectFault(ctx, fault))
			r, err := h.Apply(ctx, applyRequest{Identity: request("config.apply", "rollback-failed", need(t, before.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
			wantOutcome, wantPhase := "degraded", "degraded"
			if point == "rollback-active-publication" {
				// A persistence error stops further writes. Preserve the last
				// actual RollingBack/pending record for explicit reconciliation;
				// do not append a fabricated Degraded transition after that error.
				wantOutcome, wantPhase = "pending", "rolling_back"
			}
			prior := result(t, r, err, wantOutcome)
			if prior.Phase != wantPhase {
				t.Fatal("rollback failure lost the actual retained phase")
			}
			old := need(t, before.ActiveBundle, "old complete bundle")
			assertOperationBinding(t, prior, old, nil)
			assertDurableResult(t, evidence(t, h, ctx), prior)
			after := observe(t, h, ctx)
			assertDegraded(t, after)
			ev := evidence(t, h, ctx)
			requireFault(t, ev, fault)
			preservedOperationPhase(t, ev, "rollback-failed", old, &prior, wantPhase)
			assertOrdering(t, ev)
			// Read-only inspection cannot repair/delete evidence or silently trigger restart policy.
			observe(t, h, ctx)
			noMutation(t, ev, evidence(t, h, ctx))
			r, err = h.Start(ctx, startRequest{Identity: request("runtime.start", "blocked-start", need(t, after.Revision, "revision")), Fixture: verifiedFixture, BundleID: baselineBundle, AllowRestart: true, RestartLimit: 1})
			resultError(t, r, err, "RECOVERY_REQUIRED")
			noMutation(t, ev, evidence(t, h, ctx))
			r, err = h.Apply(ctx, applyRequest{Identity: request("config.apply", "blocked-apply", need(t, after.Revision, "revision")), Fixture: verifiedFixture, Candidate: targetBundle})
			resultError(t, r, err, "RECOVERY_REQUIRED")
			noMutation(t, ev, evidence(t, h, ctx))
			must(t, h.RestartManager(ctx, "managed"))
			observe(t, h, ctx)
			noMutation(t, ev, evidence(t, h, ctx))
		})
	}
}

func TestP1RequestIdentityRevisionAndCapacity(t *testing.T) { // C01.
	h := newProductHarness(t)
	// Full-store fsync/capture work is a correctness test, not a latency budget.
	ctx, cancel := context.WithTimeout(context.Background(), 480*time.Second)
	t.Cleanup(cancel)
	before, ev := start(t, h, ctx, false)
	revision := need(t, before.Revision, "revision")
	stale := applyRequest{Identity: request("config.apply", "stale-request", "unissued-revision-token"), Fixture: verifiedFixture, Candidate: targetBundle}
	r, err := h.Apply(ctx, stale)
	resultError(t, r, err, "CONFLICT")
	noMutation(t, ev, evidence(t, h, ctx))
	accepted := applyRequest{Identity: request("config.apply", "dedup-request", revision), Fixture: verifiedFixture, Candidate: targetBundle}
	r, err = h.Apply(ctx, accepted)
	op := result(t, r, err, "committed")
	if op.RequestID != accepted.Identity.ID || len(op.RequestDigest) != 64 || op.TargetRevision == nil || *op.TargetRevision == revision {
		t.Fatal("committed full-content request identity missing")
	}
	after := observe(t, h, ctx)
	p := running(t, after)
	if p.Generation != op.Target.Generation {
		t.Fatal("actual apply generation mismatch")
	}
	for _, entry := range []string{"entry-a", "entry-b"} {
		trafficForBundle(t, h, ctx, targetBundle, "new", "applied-"+entry, entry, 4096, p.RunID)
	}
	applied := evidence(t, h, ctx)
	if need(t, after.Revision, "new revision") != requiredTargetRevision(t, op) {
		t.Fatal("committed revision mismatch")
	}
	// Complete identical content still contains the now stale base revision. Lookup must precede that check.
	r, err = h.Apply(ctx, accepted)
	again := result(t, r, err, "committed")
	if !reflect.DeepEqual(op, again) {
		t.Fatal("same request must return original result")
	}
	noMutation(t, applied, evidence(t, h, ctx))
	for _, field := range []string{"version", "name", "base-revision", "disruption-consent", "candidate"} {
		t.Run("changed-"+field, func(t *testing.T) {
			changed := accepted
			switch field {
			case "version":
				changed.Identity.Version = "2"
			case "name":
				changed.Identity.Name = "runtime.start"
			case "base-revision":
				changed.Identity.BaseRevision = requiredTargetRevision(t, op)
			case "disruption-consent":
				changed.Identity.AcceptDisruption = false
			case "candidate":
				changed.Candidate = baselineBundle
			}
			r, err := h.Apply(ctx, changed)
			resultError(t, r, err, "REQUEST_ID_REUSED")
			noMutation(t, applied, evidence(t, h, ctx))
		})
	}
	// Fill actual storage slots with explicitly labelled Prepared metadata;
	// these are not fabricated terminal product operations.
	capacity := faultRequest{Point: "request-history-capacity-fixture", Failure: "fill-valid-retained-records"}
	must(t, h.InjectFault(ctx, capacity))
	full := evidence(t, h, ctx)
	requireFault(t, full, capacity)
	if len(full.Operations) != config.MaxOperationFiles || len(full.FixtureSeeds) != config.MaxOperationFiles-len(applied.Operations) {
		t.Fatal("capacity fixture lost complete operation provenance")
	}
	fresh := accepted
	fresh.Identity = request("config.apply", "pending-fixture-rejected", requiredTargetRevision(t, op))
	fresh.Candidate = baselineBundle
	r, err = h.Apply(ctx, fresh)
	resultError(t, r, err, "RECOVERY_REQUIRED")
	noMutation(t, full, evidence(t, h, ctx))
	// service.stop permits existing pending records, so its new admission reaches
	// the capacity check. Apply correctly rejects earlier on pending operations.
	r, err = h.ServiceStop(ctx, request("service.stop", "capacity-rejected", requiredTargetRevision(t, op)))
	resultError(t, r, err, "OPERATION_CAPACITY_EXHAUSTED")
	noMutation(t, full, evidence(t, h, ctx))
	r, err = h.Apply(ctx, accepted)
	again = result(t, r, err, "committed")
	if !reflect.DeepEqual(op, again) {
		t.Fatal("capacity must not prevent retry of existing request")
	}
	noMutation(t, full, evidence(t, h, ctx))
}

// The detail in these helpers is an assertion contract for a future adapter, never a fake outcome.
func requireInterrupted(t *testing.T, r operationResult, err error) {
	t.Helper()
	if err == nil || r.Outcome == "committed" {
		t.Fatal("injected manager crash must not return a successful response")
	}
}
func requireFault(t *testing.T, e productEvidence, want faultRequest) {
	t.Helper()
	for _, v := range e.FaultHits {
		if v.Point == want.Point && v.Artifact == want.Artifact && v.Phase == want.Phase && v.Cut == want.Cut && v.Failure == want.Failure {
			verifyOriginal(t, v.Original)
			return
		}
	}
	t.Fatalf("fault seam was not actually reached: %+v", want)
}
func requireBoundary(t *testing.T, e productEvidence, op, name string) {
	t.Helper()
	for _, v := range e.Boundaries {
		if v.OperationID == op && v.Name == name {
			for _, ref := range v.Originals {
				verifyOriginal(t, ref)
			}
			if len(v.Originals) > 0 {
				return
			}
		}
	}
	t.Fatalf("missing original %s for operation %s", name, op)
}
func assertStopped(t *testing.T, o productObservation) {
	t.Helper()
	if need(t, o.Runtime, "stopped runtime") != "verified-stopped" || need(t, o.Desired, "stopped desired") != "stopped" {
		t.Fatal("explicit stop was not completed")
	}
	if need(t, o.CorePresent, "verified process absence") {
		t.Fatal("stopped core still present")
	}
	unavailable(t, o.Core)
}
func assertDegraded(t *testing.T, o productObservation) {
	t.Helper()
	if !need(t, o.Degraded, "public degraded status") || need(t, o.Runtime, "unverified runtime") != "unknown" || need(t, o.PendingOperation, "retained pending operation") == "" {
		t.Fatal("uncertainty must expose degraded with original pending operation")
	}
}
func operationFromEvidence(t *testing.T, e productEvidence, requestID string) *operationRecord {
	t.Helper()
	var found *operationRecord
	for key, op := range e.Operations {
		if op.RequestID == requestID {
			if found != nil || op.ID != key {
				t.Fatal("ambiguous original operation identity")
			}
			copy := op
			found = &copy
		}
	}
	return found
}
func assertOperationBinding(t *testing.T, op operationRecord, old bundleSnapshot, prior *operationRecord) {
	t.Helper()
	if op.ID == "" || op.RequestID == "" || len(op.RequestDigest) != 64 || op.BaseRevision == "" || op.TargetRevision == nil || op.BaseRevision == requiredTargetRevision(t, op) {
		t.Fatal("incomplete original operation identity/revisions")
	}
	assertBundle(t, op.Previous, old)
	validBundle(t, op.Target)
	assertFixedTarget(t, op.Target)
	if prior != nil {
		if op.ID != prior.ID || op.RequestID != prior.RequestID || op.RequestDigest != prior.RequestDigest || op.BaseRevision != prior.BaseRevision || !reflect.DeepEqual(op.TargetRevision, prior.TargetRevision) {
			t.Fatal("operation identity/digest/revision changed during recovery")
		}
		assertBundle(t, op.Previous, prior.Previous)
		assertBundle(t, op.Target, prior.Target)
	}
}
func preservedOperation(t *testing.T, e productEvidence, requestID string, old bundleSnapshot, prior *operationRecord) operationRecord {
	return preservedOperationPhase(t, e, requestID, old, prior, "degraded")
}
func preservedOperationPhase(t *testing.T, e productEvidence, requestID string, old bundleSnapshot, prior *operationRecord, phase string) operationRecord {
	t.Helper()
	op := operationFromEvidence(t, e, requestID)
	if op == nil {
		t.Fatal("old/new operation evidence deleted")
	}
	assertOperationBinding(t, *op, old, prior)
	if op.Phase != phase {
		t.Fatal("retained recovery-required operation phase changed")
	}
	return *op
}
func recoveryState(t *testing.T, h productHarness, ctx context.Context, old bundleSnapshot, oldRevision config.Revision, r operationResult, err error, requestID string, prior *operationRecord) {
	t.Helper()
	must(t, err)
	result(t, r, nil, r.Outcome)
	after := observe(t, h, ctx)
	ev := evidence(t, h, ctx)
	if prior == nil {
		t.Fatal("accepted interrupted operation original is missing")
	}
	assertOperationBinding(t, r.Operation, old, prior)
	assertDurableResult(t, ev, r.Operation)
	assertRecoveryResult(t, r, ev)
	switch r.Outcome {
	case "committed":
		op := r.Operation
		if op.RequestID != requestID || op.Phase != "committed" || op.Target.Generation == old.Generation {
			t.Fatal("recovery did not finish original target operation")
		}
		running(t, after)
		assertBundle(t, need(t, after.ActiveBundle, "recovered target bundle"), op.Target)
		if need(t, after.Revision, "target revision") != requiredTargetRevision(t, op) {
			t.Fatal("target pointer revision mismatch")
		}
		p := running(t, after)
		if p.Generation != op.Target.Generation {
			t.Fatal("actual runtime generation differs from committed target bundle")
		}
		for _, entry := range op.Target.Entries {
			trafficForBundle(t, h, ctx, targetBundle, "new", "target-"+entry, entry, 4096, p.RunID)
		}
	case "rolled_back":
		op := r.Operation
		if op.RequestID != requestID || op.Phase != "rolled_back" {
			t.Fatal("rollback is not original operation")
		}
		p := running(t, after)
		if p.Generation != old.Generation {
			t.Fatal("actual rollback runtime is not the old generation")
		}
		assertBundle(t, need(t, after.ActiveBundle, "verified old bundle"), old)
		if need(t, after.Revision, "rollback revision") != oldRevision {
			t.Fatal("rollback did not restore original revision")
		}
		requireBoundary(t, ev, op.ID, "old-state-verified")
	case "degraded":
		assertDegraded(t, after)
		preservedOperation(t, ev, requestID, old, prior)
	default:
		t.Fatalf("recovery must prove complete old/new state or explicit degraded, got %s", r.Outcome)
	}
}

func requireBoundaryAfter(t *testing.T, e productEvidence, operationID, name string, after uint64) {
	t.Helper()
	for _, v := range e.Boundaries {
		bound := v.OperationID == operationID
		if name == "inputs-revalidated" || name == "candidate-validated" {
			op, ok := e.Operations[operationID]
			bound = ok && v.OperationID == "" && v.RequestID == op.RequestID && v.RequestDigest == op.RequestDigest
		}
		if bound && v.Name == name && v.Index > after {
			if len(v.Originals) == 0 {
				t.Fatal("missing fresh verification originals")
			}
			for _, r := range v.Originals {
				verifyOriginal(t, r)
			}
			return
		}
	}
	t.Fatalf("explicit resubmission did not freshly prove %s", name)
}
