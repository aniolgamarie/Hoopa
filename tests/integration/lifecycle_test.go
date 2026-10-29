//go:build p1integration

package integration

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/control"
)

func TestP1FrontendExitKeepsCore(t *testing.T) { // F01: actual frontend process, existing and fresh traffic.
	for _, mode := range []string{"normal", "abnormal"} {
		t.Run(mode, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before, ev := start(t, h, ctx, false)
			p := running(t, before)
			traffic(t, h, ctx, "open-held", "old", "entry-a", 4096, p.RunID)
			must(t, h.ExitFrontend(ctx, mode))
			sameCore(t, p, observe(t, h, ctx))
			traffic(t, h, ctx, "continue-held", "old", "entry-a", 4096, p.RunID)
			traffic(t, h, ctx, "new", "fresh", "entry-b", 4096, p.RunID)
			noDuplicateLaunch(t, ev, evidence(t, h, ctx))
		})
	}
}

func TestP1ManagerKilledKeepsTraffic(t *testing.T) { // F02: collector failure is not a zero byte count.
	h := newProductHarness(t)
	ctx := testContext(t)
	before, ev := start(t, h, ctx, false)
	p := running(t, before)
	traffic(t, h, ctx, "open-held", "old", "entry-a", 4096, p.RunID)
	must(t, h.KillManager(ctx))
	after := observe(t, h, ctx)
	if need(t, after.ManagerAvailable, "manager availability") {
		t.Fatal("manager was not killed")
	}
	unavailable(t, after.Bytes)
	// These are independent traffic witnesses, not counters reported by the dead manager.
	traffic(t, h, ctx, "continue-held", "old", "entry-a", 4096, p.RunID)
	traffic(t, h, ctx, "new", "fresh", "entry-b", 4096, p.RunID)
	got := need(t, after.Core, "independent process identity")
	if got != p {
		t.Fatal("manager death changed core")
	}
	noDuplicateLaunch(t, ev, evidence(t, h, ctx))
}

func TestP1ManagerExplicitRecoveryAdoptsSameCore(t *testing.T) { // F03.
	h := newProductHarness(t)
	ctx := testContext(t)
	before, _ := start(t, h, ctx, false)
	p := running(t, before)
	must(t, h.KillManager(ctx))
	dead := evidence(t, h, ctx)
	must(t, h.RestartManager(ctx, "control-only"))
	noMutation(t, dead, evidence(t, h, ctx)) // a restart/observation alone is not takeover or Apply.
	r, err := h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "adopt-original")})
	result(t, r, err, "committed")
	assertRecoveryResult(t, r, evidence(t, h, ctx))
	after := observe(t, h, ctx)
	sameCore(t, p, after)
	if need(t, after.Revision, "revision") != need(t, before.Revision, "original revision") {
		t.Fatal("adoption created a configuration revision")
	}
	if before.Epoch.State == "observed" {
		if need(t, after.Epoch, "counter epoch") != need(t, before.Epoch, "original counter epoch") {
			t.Fatal("manager restart created a counter epoch")
		}
	} else {
		unavailable(t, before.Epoch)
		unavailable(t, after.Epoch)
	} // optional collector remains unverified, never a zero epoch.
	noDuplicateLaunch(t, dead, evidence(t, h, ctx))
	traffic(t, h, ctx, "new", "adopted-traffic", "entry-a", 4096, p.RunID)
}

func TestP1SessionLoss(t *testing.T) { // F04: three real platform events, never a signal surrogate.
	for _, event := range []string{"ssh-disconnect", "login-shell-logout", "system-suspend-resume"} {
		t.Run(event, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			if event != "system-suspend-resume" {
				must(t, h.BeginSession(ctx, event))
			}
			before, ev := start(t, h, ctx, false)
			p := running(t, before)
			traffic(t, h, ctx, "open-held", "session-old", "entry-a", 4096, p.RunID)
			beforeSession := evidence(t, h, ctx)
			must(t, h.SessionEvent(ctx, event)) // exactly this one serial invocation must append one record
			afterEv := evidence(t, h, ctx)
			if len(afterEv.Sessions) != len(beforeSession.Sessions)+1 {
				t.Fatal("one SessionEvent must append exactly one record")
			}
			for i, prior := range beforeSession.Sessions {
				if prior != afterEv.Sessions[i] {
					t.Fatal("SessionEvent changed the old session-record prefix")
				}
			}
			session := afterEv.Sessions[len(beforeSession.Sessions)]
			if session.Kind != event || session.Began.Path == session.Ended.Path || session.Began.SHA256 == session.Ended.SHA256 {
				t.Fatal("current SessionEvent requires distinct original begin/end witnesses of the correct kind")
			}
			verifyOriginal(t, session.Began)
			verifyOriginal(t, session.Ended)
			noDuplicateLaunch(t, ev, afterEv)
			noMutation(t, ev, afterEv)
			after := observe(t, h, ctx)
			state := need(t, after.Runtime, "explicit runtime state")
			// Offline read-only status cannot activate management authority.
			// Actual continuity instead needs the same independently observed
			// process, connected-controller owner and both ready entries. Keep
			// the public unknown status intact while proving real traffic.
			if state == "verified-running" || (state == "unknown" && after.Core.State == "observed") {
				if independentlyOpenCore(t, after) != p {
					t.Fatal("session loss changed the independently verified core")
				}
				traffic(t, h, ctx, "continue-held", "session-old", "entry-a", 4096, p.RunID)
				traffic(t, h, ctx, "new", "session-new", "entry-b", 4096, p.RunID)
			} else { // This is an explicit unknown/conflict result, not a claim that traffic survived.
				unavailable(t, after.Core)
				if state != "unknown" && state != "conflict" {
					t.Fatal("session loss must preserve instance or explicitly fail closed")
				}
			}
		})
	}
}

func TestP1OwnedCoreRestartPolicy(t *testing.T) { // F06: one authorized bounded restart, new run identity.
	h := newProductHarness(t)
	ctx := testContext(t)
	before, ev := start(t, h, ctx, true)
	p := running(t, before)
	must(t, h.KillCore(ctx, p))
	after := awaitObservation(t, h, ctx, "authorized fresh core run", func(o productObservation) bool {
		return observedEquals(o.Runtime, "verified-running") && observedEquals(o.CorePresent, true) && o.Core.State == "observed" && o.Core.Value != nil && o.Core.Value.RunID != p.RunID
	})
	next := running(t, after)
	assertCommittedIntentUnchanged(t, before, after)
	now := evidence(t, h, ctx)
	if next.RunID == p.RunID || (next.PID == p.PID && next.OSStartIdentity == p.OSStartIdentity) || next.LaunchNonceSHA256 == p.LaunchNonceSHA256 || next.Generation != p.Generation {
		t.Fatal("restart must be a fresh run of the same committed generation")
	}
	if len(now.Launches) != len(ev.Launches)+1 {
		t.Fatal("restart policy allowed more/less than one actual launch")
	}
	traffic(t, h, ctx, "new", "after-core-kill", "entry-b", 4096, next.RunID)
	assertOrdering(t, now)
	// The exhausted budget cannot silently restart indefinitely or bypass through another engine.
	must(t, h.KillCore(ctx, next))
	stopped := awaitStopped(t, h, ctx)
	if need(t, stopped.Runtime, "bounded restart exhaustion") != "verified-stopped" {
		t.Fatal("restart budget did not stop")
	}
	assertCommittedIntentUnchanged(t, before, stopped)
	noDuplicateLaunch(t, now, evidence(t, h, ctx))
}

func TestP1OwnershipConflictsNeverClaimKillOrSpawn(t *testing.T) { // F14, also F06 unowned controls.
	for _, arrangement := range []string{"birth-start-identity-mismatch", "foreign-listener", "actual-pid-reuse", "missing-registration-and-birth", "incomplete-registration"} {
		t.Run(arrangement, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			start(t, h, ctx, false)
			must(t, h.InjectFault(ctx, faultRequest{Point: "ownership-fixture", Failure: arrangement}))
			arranged := evidence(t, h, ctx)
			requireFault(t, arranged, faultRequest{Point: "ownership-fixture", Failure: arrangement})
			if (arrangement == "foreign-listener" || arrangement == "actual-pid-reuse") && (len(arranged.ForeignProcesses) != 1 || arranged.ForeignProcesses[0].PID <= 0 || arranged.ForeignProcesses[0].RunID != "" || arranged.ForeignProcesses[0].Generation != "") {
				t.Fatal("foreign listener needs an actual independently observed non-product process")
			}
			r, err := h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "conflicted")})
			resultError(t, r, err, "CONFLICT")
			after := observe(t, h, ctx)
			if arrangement == "incomplete-registration" {
				// A fresh independent owner observation may still identify the
				// actual process. Missing registration denies managed authority.
				need(t, after.Core, "independent live owner despite invalid registration")
			} else {
				unavailable(t, after.Core)
			}
			state := need(t, after.Runtime, "ownership conflict")
			if state != "unknown" && state != "conflict" {
				t.Fatal("PID/port/absent record cannot establish ownership")
			}
			got := evidence(t, h, ctx)
			noMutation(t, arranged, got)
			if !reflect.DeepEqual(arranged.ForeignProcesses, got.ForeignProcesses) {
				t.Fatal("foreign fixture process changed")
			}
		})
	}
}

func TestP1WithoutOptionalComponents(t *testing.T) { // F18.
	h := newProductHarness(t)
	ctx := testContext(t)
	o, _ := start(t, h, ctx, false, "gost", "sqlite")
	// This fixed basic build has only its pinned CLI dependencies and no extra
	// Go database-driver modules; its explicit child environment has no GOST
	// search path. This is not host-wide executable/native-library inventory.
	if !reflect.DeepEqual(need(t, o.UnavailableComponents, "verified fixed-build/launch fixture scope"), []string{"gost", "sqlite"}) {
		t.Fatal("optional fixture evidence missing")
	}
	p := running(t, o)
	traffic(t, h, ctx, "new", "no-optional-components", "entry-a", 4096, p.RunID)
	before := evidence(t, h, ctx)
	sameCore(t, p, observe(t, h, ctx))
	noMutation(t, before, evidence(t, h, ctx))
	r, err := h.Stop(ctx, request("runtime.stop", "stop-without-optional-components", need(t, o.Revision, "revision")))
	op := result(t, r, err, "committed")
	assertStopCommit(t, o, observe(t, h, ctx), op)
	assertOrdering(t, evidence(t, h, ctx))
}

func TestP1ControlOnlyNeverStartsCore(t *testing.T) {
	for _, live := range []bool{false, true} {
		name := "empty"
		if live {
			name = "live"
		}
		t.Run(name, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			if live {
				start(t, h, ctx, false)
			}
			before := observe(t, h, ctx)
			ev := evidence(t, h, ctx)
			must(t, h.RestartManager(ctx, "control-only"))
			after := observe(t, h, ctx)
			noMutation(t, ev, evidence(t, h, ctx))
			if live {
				sameCore(t, running(t, before), after)
			} else {
				assertStopped(t, after)
			}
		})
	}
}

func TestP1DurableStopSurvivesManagerCrash(t *testing.T) {
	h := newProductHarness(t)
	ctx := testContext(t)
	before, _ := start(t, h, ctx, true)
	fault := faultRequest{Point: "stop-intent-durable", Failure: "crash-manager"}
	must(t, h.InjectFault(ctx, fault))
	r, err := h.Stop(ctx, request("runtime.stop", "interrupted-stop", need(t, before.Revision, "revision")))
	requireInterrupted(t, r, err)
	pending := observe(t, h, ctx)
	if need(t, pending.PendingDesired, "durable stop desired") != "stopped" || need(t, pending.PendingOperation, "pending stop") == "" {
		t.Fatal("stop intention lost")
	}
	ev := evidence(t, h, ctx)
	requireFault(t, ev, fault)
	must(t, h.RestartManager(ctx, "control-only"))
	noMutation(t, ev, evidence(t, h, ctx))
	r, err = h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "finish-stop"), InterruptedRequestID: "interrupted-stop"})
	op := result(t, r, err, "committed")
	assertRecoveryResult(t, r, evidence(t, h, ctx))
	original := operationFromEvidence(t, ev, "interrupted-stop")
	if original == nil || op.ID != original.ID || op.RequestID != original.RequestID || op.RequestDigest != original.RequestDigest || op.BaseRevision != original.BaseRevision || !reflect.DeepEqual(op.TargetRevision, original.TargetRevision) || op.Desired != original.Desired {
		t.Fatal("recovered stop response does not name the original durable stop operation")
	}
	assertBundle(t, op.Previous, original.Previous)
	assertBundle(t, op.Target, original.Target)
	assertDurableResult(t, evidence(t, h, ctx), op)
	assertStopCommit(t, before, observe(t, h, ctx), op)
	noDuplicateLaunch(t, ev, evidence(t, h, ctx))
	assertOrdering(t, evidence(t, h, ctx))
}

func TestP1SpawnRegistrationGap(t *testing.T) {
	for _, point := range []string{"spawn-intent-durable", "spawn-returned-before-registration"} {
		t.Run(point, func(t *testing.T) {
			h := newProductHarness(t)
			ctx := testContext(t)
			before := observe(t, h, ctx)
			fault := faultRequest{Point: point, Failure: "crash-manager"}
			must(t, h.InjectFault(ctx, fault))
			r, err := h.Start(ctx, startRequest{Identity: request("runtime.start", "interrupted-spawn", need(t, before.Revision, "revision")), Fixture: verifiedFixture, BundleID: baselineBundle})
			requireInterrupted(t, r, err)
			ev := evidence(t, h, ctx)
			requireFault(t, ev, fault)
			assertOrdering(t, ev)
			// Positive gap fixture retains durable intent/nonce and a strongly verifiable original core.
			// The negative F14 fixture explicitly lacks that proof.
			var original processIdentity
			if point == "spawn-returned-before-registration" {
				pending := observe(t, h, ctx)
				original = need(t, pending.Core, "independently verified gap process")
				strongIdentity(t, original)
				if !need(t, pending.CorePresent, "gap core presence") || len(need(t, pending.ControllerOwnerProof, "gap connected owner proof")) != 64 {
					t.Fatal("positive gap fixture lacks strong ownership proof")
				}
			}
			must(t, h.RestartManager(ctx, "control-only"))
			noMutation(t, ev, evidence(t, h, ctx))
			r, err = h.Recover(ctx, recoverRequest{Identity: freshRecoveryIdentity(t, h, ctx, "resolve-spawn-gap"), InterruptedRequestID: "interrupted-spawn"})
			must(t, err)
			after := observe(t, h, ctx)
			now := evidence(t, h, ctx)
			if point == "spawn-intent-durable" {
				// A persisted intent is not a reconstructible one-use launch
				// ticket. This pending target follows admitted recovery: retain
				// its diagnosis and a separate failed recovery-call receipt.
				op := result(t, r, nil, "degraded")
				assertRecoveryResult(t, r, now)
				assertDegraded(t, after)
				if op.Phase != "degraded" || r.RecoveryCall == nil || r.RecoveryCall.Phase != "degraded" {
					t.Fatal("unproved pre-spawn gap must retain degraded target and call")
				}
				prior := operationFromEvidence(t, ev, "interrupted-spawn")
				if prior == nil || prior.ID != op.ID {
					t.Fatal("recovery replaced original interrupted target")
				}
				var oldSource, newSource control.Operation
				must(t, json.Unmarshal(prior.recordSource, &oldSource))
				must(t, json.Unmarshal(op.recordSource, &newSource))
				if !reflect.DeepEqual(oldSource.Identity, newSource.Identity) || !reflect.DeepEqual(oldSource.Request, newSource.Request) || !reflect.DeepEqual(prior.Previous, op.Previous) || !reflect.DeepEqual(prior.Target, op.Target) {
					t.Fatal("recovery changed immutable target identity or bundle binding")
				}
				assertOnlyGapDiagnostics(t, ev, now, op.ID, r.RecoveryCall.ID)
				assertOrdering(t, now)
				return
			}
			if r.Outcome == "committed" {
				result(t, r, nil, r.Outcome)
				p := running(t, after)
				if point == "spawn-returned-before-registration" {
					if p != original {
						t.Fatal("strongly verified original gap process was not adopted")
					}
					found := false
					for _, v := range ev.Launches {
						if v.Instance == p {
							found = true
						}
					}
					if !found {
						t.Fatal("gap recovery did not adopt the original nonce-bound process")
					}
					noDuplicateLaunch(t, ev, now)
				}
			} else {
				if point == "spawn-returned-before-registration" {
					t.Fatal("complete verifiable intent/nonce/process proof must allow original adoption")
				}
				resultError(t, r, nil, "CONFLICT")
				unavailable(t, after.Core)
				noMutation(t, ev, now)
			}
			// Recovery adopts the proved original run; it never rebuilds a ticket.
			seen := map[string]bool{}
			for _, v := range now.Launches {
				if len(v.NonceSHA256) != 64 || seen[v.NonceSHA256] {
					t.Fatal("duplicate spawn for one durable nonce")
				}
				seen[v.NonceSHA256] = true
			}
		})
	}
}

// Only the original pending operation and its separate recovery-call diagnosis
// may change. Terminal ownership-conflict tests keep the stronger noMutation.
func assertOnlyGapDiagnostics(t *testing.T, before, after productEvidence, target, call string) {
	t.Helper()
	if target == call || before.Operations[call].ID != "" || len(after.Operations) != len(before.Operations)+1 || !reflect.DeepEqual(before.Launches, after.Launches) || !reflect.DeepEqual(before.Signals, after.Signals) || !reflect.DeepEqual(before.ForeignProcesses, after.ForeignProcesses) {
		t.Fatal("gap recovery performed effects beyond diagnostic records")
	}
	for id, op := range before.Operations {
		if id != target && !reflect.DeepEqual(op, after.Operations[id]) {
			t.Fatal("gap recovery changed unrelated operation")
		}
	}
	allowed := func(path string) bool {
		return filepath.Base(filepath.Dir(path)) == "operations" && (filepath.Base(path) == target+".json" || filepath.Base(path) == call+".json")
	}
	for path, value := range before.DurableFiles {
		if !allowed(path) && after.DurableFiles[path] != value {
			t.Fatal("gap recovery changed nondiagnostic durable file")
		}
	}
	for path, value := range after.DurableFiles {
		if !allowed(path) && before.DurableFiles[path] != value {
			t.Fatal("gap recovery created nondiagnostic durable file")
		}
	}
	for _, event := range after.Boundaries {
		if event.OperationID == call || event.OperationID == target {
			if event.Name == "registration-durable" || event.Name == "core-spawn" || event.Name == "active-published" || event.Name == "old-active-published" || event.Name == "core-stop" {
				retained := false
				for _, prior := range before.Boundaries {
					if reflect.DeepEqual(prior, event) {
						retained = true
						break
					}
				}
				if !retained {
					t.Fatal("gap recovery introduced a runtime publication or effect")
				}
			}
		}
	}
}

func TestP1CoreDeathWithoutRestartAuthorization(t *testing.T) { // F06 negative policy control.
	h := newProductHarness(t)
	ctx := testContext(t)
	before, ev := start(t, h, ctx, false)
	p := running(t, before)
	must(t, h.KillCore(ctx, p))
	// With no authorized restart watch, public status has no retained watch
	// exit fact and must leave the still-desired-running runtime unknown. The
	// harness separately proves original pidfd exit over its complete inventory.
	after := awaitObservation(t, h, ctx, "original core exited without restart", func(o productObservation) bool {
		return observedEquals(o.Runtime, "unknown") && observedEquals(o.CorePresent, false)
	})
	if need(t, after.Runtime, "unverified runtime after original exit") != "unknown" || need(t, after.CorePresent, "actual process absence") {
		t.Fatal("unauthorized automatic restart or unverified absence")
	}
	assertCommittedIntentUnchanged(t, before, after)
	unavailable(t, after.Core)
	noDuplicateLaunch(t, ev, evidence(t, h, ctx))
}
