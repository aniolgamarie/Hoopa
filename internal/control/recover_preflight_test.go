//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// These are real private Store/journal ordering tests with deliberately synthetic
// verifier results. They do not prove ownership/activation of an actual process
// and are not F14 actual-runtime acceptance.
func terminalPreflightApplyFixture(t *testing.T, rollback bool) recoveryFixture {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp") // Reused Store fixtures require safe owned /tmp ancestors.
	f := recoveryOrderingFixture(t, rollback)
	calls := 0
	f.c.mu.Lock()
	target, err := f.c.reconcileBasicApply(context.Background(), f.target, recoveryVerifier(t, f, &calls), nil)
	f.c.mu.Unlock()
	want := Committed
	if rollback {
		want = RolledBack
	}
	if err != nil || target.Record.Phase != want || calls != 2 {
		t.Fatal("could not prepare synthetic terminal target", target.Record.Phase, calls, err)
	}
	f.target = target
	return f
}
func terminalPreflightCall(t *testing.T, c *Coordinator, ctx context.Context, raw []byte, verify recoveredRunVerifier, boundary func(string) error) (StoredOperation, bool, error) {
	t.Helper()
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recoverBasicLocked(ctx, request, verify, boundary)
}
func TestRecoveryTerminalPreflightOwnershipFailureZeroWrites(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "committed"
		if rollback {
			name = "rolled-back"
		}
		t.Run(name, func(t *testing.T) {
			for _, failure := range []string{"unknown", "identity-change"} {
				t.Run(failure, func(t *testing.T) {
					f := terminalPreflightApplyFixture(t, rollback)
					raw := recoveryCurrentRequest(t, f, "preflight-refused")
					// A no-effects refusal must also precede explicit staging maintenance.
					staging := filepath.Join(f.paths.State, "operations", ".operation-"+strings.Repeat("a", 48))
					if err := os.WriteFile(staging, []byte("synthetic unpublished staging"), 0600); err != nil {
						t.Fatal(err)
					}
					before := coordinatorTree(t, f.paths)
					calls := 0
					verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
						calls++
						if id != f.run || op != f.target.Record.Identity.OperationID {
							t.Fatal("preflight not bound to original target")
						}
						if failure == "unknown" {
							return platform.ProcessIdentity{}, errors.New("synthetic ownership unknown")
						}
						identity := recoveryIdentity()
						if calls%2 == 0 {
							identity.StartTicks++
						}
						return identity, nil
					}
					_, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, nil)
					if made || !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
						t.Fatal("terminal ownership rejection admitted/mutated recovery", made, err, calls)
					}
					wantCalls := 1
					if failure == "identity-change" {
						wantCalls = 2
					}
					if calls != wantCalls {
						t.Fatal("preflight proof count", calls, wantCalls)
					}
					target, err := f.c.journal.Get(f.target.Record.Identity.OperationID)
					if err != nil || !reflect.DeepEqual(target, f.target) {
						t.Fatal("terminal target changed", err)
					}
				})
			}
		})
	}
}

func TestRecoveryTerminalPreflightSuccessRechecksAfterAdmissionAndReplays(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "committed"
		if rollback {
			name = "rolled-back"
		}
		t.Run(name, func(t *testing.T) {
			f := terminalPreflightApplyFixture(t, rollback)
			raw := recoveryCurrentRequest(t, f, "terminal-recovery-success")
			request, _ := ParseRequest(raw)
			before := coordinatorTree(t, f.paths)
			protected := managedProtectedTree(t, f.paths)
			calls := 0
			admissionBoundary := false
			verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
				calls++
				if id != f.run || op != f.target.Record.Identity.OperationID {
					t.Fatal("proof rebound away from original target")
				}
				call, err := f.c.journal.Get(request.OperationID())
				if calls <= 2 {
					if !errors.Is(err, ErrOperationNotFound) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
						t.Fatal("preflight already admitted or changed files", calls, err)
					}
				} else {
					if err != nil || call.Record.Phase != Applying || !recoveryCallBound(call, f.target) {
						t.Fatal("post-admission proof lacked bound durable call", calls, err)
					}
				}
				return recoveryIdentity(), nil
			}
			boundary := func(step string) error {
				call, err := f.c.journal.Get(request.OperationID())
				if err != nil || call.Record.Phase != Applying || calls < 2 {
					t.Fatal("preflight invoked mutation boundary", step, calls, err)
				}
				if step == "recover-admitted" {
					admissionBoundary = true
					if calls != 2 {
						t.Fatal("admission before full preflight", calls)
					}
				}
				return nil
			}
			call, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, boundary)
			if err != nil || !made || call.Record.Phase != Committed || calls != 4 || !admissionBoundary {
				t.Fatal("terminal recovery did not prove before and after admission", made, call.Record.Phase, calls, err)
			}
			target, err := f.c.journal.Get(f.target.Record.Identity.OperationID)
			if err != nil || !reflect.DeepEqual(target, f.target) || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
				t.Fatal("terminal proof changed original/core authority", err)
			}
			replayBefore := coordinatorTree(t, f.paths)
			replay, newly, err := terminalPreflightCall(t, f.c, nil, raw, nil, func(string) error { t.Fatal("replay invoked boundary"); return nil })
			if err != nil || newly || !reflect.DeepEqual(replay, call) || calls != 4 || !reflect.DeepEqual(replayBefore, coordinatorTree(t, f.paths)) {
				t.Fatal("known request replay reverified or mutated", newly, err)
			}
			// A fresh request cannot turn nil dependencies into verifierless success.
			fresh := recoveryCurrentRequest(t, f, "fresh-without-verifier")
			_, newly, err = terminalPreflightCall(t, f.c, context.Background(), fresh, nil, nil)
			if newly || !errors.Is(err, ErrRequest) || !reflect.DeepEqual(replayBefore, coordinatorTree(t, f.paths)) {
				t.Fatal("fresh request bypassed verifier", newly, err)
			}
		})
	}
}
func TestRecoveryTerminalPreflightLaterFailureKeepsAdmittedDegradedCall(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "committed"
		if rollback {
			name = "rolled-back"
		}
		t.Run(name, func(t *testing.T) {
			for _, failure := range []string{"unknown-after-admission", "identity-change-after-admission"} {
				t.Run(failure, func(t *testing.T) {
					f := terminalPreflightApplyFixture(t, rollback)
					raw := recoveryCurrentRequest(t, f, "later-failed-recovery")
					request, _ := ParseRequest(raw)
					protected := managedProtectedTree(t, f.paths)
					calls := 0
					verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
						calls++
						if id != f.run || op != f.target.Record.Identity.OperationID {
							t.Fatal("wrong original proof target")
						}
						if calls >= 3 {
							call, err := f.c.journal.Get(request.OperationID())
							if err != nil || call.Record.Phase != Applying {
								t.Fatal("later failure actually preceded admission", calls, err)
							}
						}
						if failure == "unknown-after-admission" && calls == 3 {
							return platform.ProcessIdentity{}, errors.New("synthetic ownership lost after preflight")
						}
						identity := recoveryIdentity()
						if failure == "identity-change-after-admission" && calls == 4 {
							identity.StartTicks++
						}
						return identity, nil
					}
					call, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, nil)
					wantCalls := 3
					if failure == "identity-change-after-admission" {
						wantCalls = 4
					}
					if !made || !errors.Is(err, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != wantCalls {
						t.Fatal("post-admission failure lost pending/degraded receipt", made, call.Record.Phase, calls, err)
					}
					persisted, readErr := f.c.journal.Get(request.OperationID())
					if readErr != nil || !reflect.DeepEqual(persisted, call) {
						t.Fatal("degraded call not durable", readErr)
					}
					target, readErr := f.c.journal.Get(f.target.Record.Identity.OperationID)
					if readErr != nil || !reflect.DeepEqual(target, f.target) || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
						t.Fatal("late failure rewrote terminal/core authority", readErr)
					}
				})
			}
		})
	}
}
func TestRecoveryTerminalPreflightPendingTargetKeepsAdmittedSemantics(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	for _, rollback := range []bool{false, true} {
		name := "target"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			f := recoveryOrderingFixture(t, rollback)
			if terminal(f.target.Record.Phase) {
				t.Fatal("fixture unexpectedly terminal")
			}
			raw := recoveryCurrentRequest(t, f, "unknown-pending-recovery")
			request, _ := ParseRequest(raw)
			protected := managedProtectedTree(t, f.paths)
			calls := 0
			verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
				calls++
				if id != f.run || op != f.target.Record.Identity.OperationID {
					t.Fatal("wrong pending proof target")
				}
				call, err := f.c.journal.Get(request.OperationID())
				if err != nil || call.Record.Phase != Applying {
					t.Fatal("pending path was incorrectly moved into preflight", err)
				}
				return platform.ProcessIdentity{}, errors.New("synthetic pending ownership unknown")
			}
			call, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, nil)
			if !made || !errors.Is(err, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != 1 {
				t.Fatal("pending recovery semantics changed", made, call.Record.Phase, calls, err)
			}
			target, err := f.c.journal.Get(f.target.Record.Identity.OperationID)
			if err != nil || target.Record.Phase != Degraded || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
				t.Fatal("pending failure not retained conservatively", err)
			}
		})
	}
}
func TestRecoveryTerminalPreflightMalformedTerminalKeepsDiagnosticAdmission(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	for _, operation := range []string{"config.apply", "runtime.start", "runtime.stop", "service.stop"} {
		t.Run(operation, func(t *testing.T) {
			c, paths := coordinatorFixture(t, nil)
			base, err := c.store.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"expected_revision": base.Revision}
			if operation == "runtime.start" {
				params["control_only"] = false
			}
			rawTarget := planRaw(t, map[string]any{"api_version": "v1", "request_id": "malformed-terminal", "operation": operation, "params": params})
			request, err := ParseRequest(rawTarget)
			if err != nil {
				t.Fatal(err)
			}
			// Codec-valid but semantically uninterpretable terminal fixture, not a proof
			// of apply/start/stop success. Its {} evidence must remain uninterpretable.
			target, made, err := c.journal.Admit(request, Intent{BaseRevision: base.Revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}, json.RawMessage(`{"outcome":"fixture"}`))
			if err != nil || !made {
				t.Fatal(made, err)
			}
			for _, phase := range []Phase{Validated, Applying, Verifying, Publishing, Committed} {
				target, err = c.journal.Advance(request.OperationID(), target.SHA256, phase, json.RawMessage(`{}`), json.RawMessage(`{"outcome":"fixture"}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			beforeTarget := target
			protected := managedProtectedTree(t, paths)
			calls := 0
			call, made, err := terminalPreflightCall(t, c, context.Background(), recoveryRequest(t, "malformed-terminal-recovery", target.Record.Identity.OperationID, base.Revision), func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
				calls++
				t.Fatal("malformed evidence invoked verifier")
				return platform.ProcessIdentity{}, nil
			}, nil)
			if !made || !errors.Is(err, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != 0 {
				t.Fatal("malformed terminal lost diagnostic receipt", made, call.Record.Phase, calls, err)
			}
			after, err := c.journal.Get(target.Record.Identity.OperationID)
			if err != nil || !reflect.DeepEqual(after, beforeTarget) || !reflect.DeepEqual(protected, managedProtectedTree(t, paths)) {
				t.Fatal("malformed terminal invented proof or changed authority", err)
			}
		})
	}
}
func terminalPreflightAdoptionFixture(t *testing.T) (*lifecycleFixture, StoredOperation, adoptionEvidence) {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	f := lifecycleRunningFixture(t)
	stored, state, _ := managedAdmit(t, f, "terminal-synthetic-adoption")
	calls := 0
	f.c.mu.Lock()
	target, err := f.c.executeAdoption(context.Background(), stored, state, managedVerifier(t, f, state, &calls), nil)
	f.c.mu.Unlock()
	if err != nil || target.Record.Phase != Committed || calls != 3 {
		t.Fatal("synthetic adoption terminal fixture", calls, err)
	}
	return f, target, state
}
func TestRecoveryTerminalPreflightCommittedAdoption(t *testing.T) {
	for _, outcome := range []string{"unknown", "wrong-identity", "success", "failure-after-admission"} {
		t.Run(outcome, func(t *testing.T) {
			f, target, state := terminalPreflightAdoptionFixture(t)
			raw := recoveryRequest(t, "terminal-adoption-recovery", target.Record.Identity.OperationID, f.base.Revision)
			request, _ := ParseRequest(raw)
			before := coordinatorTree(t, f.paths)
			protected := managedProtectedTree(t, f.paths)
			calls := 0
			verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
				calls++
				if id != f.source || op != *state.Base.OperationID {
					t.Fatal("adoption proof did not retain original publishing identity")
				}
				if calls == 1 {
					if _, err := f.c.journal.Get(request.OperationID()); !errors.Is(err, ErrOperationNotFound) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
						t.Fatal("adoption proof occurred after admission")
					}
				} else {
					call, err := f.c.journal.Get(request.OperationID())
					if err != nil || call.Record.Phase != Applying {
						t.Fatal("adoption recheck preceded durable call", err)
					}
				}
				if outcome == "unknown" || outcome == "failure-after-admission" && calls == 2 {
					return platform.ProcessIdentity{}, errors.New("synthetic adoption ownership unknown")
				}
				identity := state.Identity
				if outcome == "wrong-identity" {
					identity.StartTicks++
				}
				return identity, nil
			}
			call, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, nil)
			switch outcome {
			case "unknown", "wrong-identity":
				if made || !errors.Is(err, ErrConflict) || calls != 1 || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
					t.Fatal("adoption preflight refusal admitted/mutated", made, calls, err)
				}
			case "success":
				if err != nil || !made || call.Record.Phase != Committed || calls != 2 {
					t.Fatal("adoption did not prove twice", made, calls, err)
				}
				replayBefore := coordinatorTree(t, f.paths)
				got, newly, err := terminalPreflightCall(t, f.c, nil, raw, nil, nil)
				if err != nil || newly || !reflect.DeepEqual(got, call) || !reflect.DeepEqual(replayBefore, coordinatorTree(t, f.paths)) {
					t.Fatal("adoption recovery replay reverified", err)
				}
			case "failure-after-admission":
				if !made || !errors.Is(err, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != 2 {
					t.Fatal("adoption late failure lost admitted receipt", made, calls, err)
				}
			}
			unchanged, err := f.c.journal.Get(target.Record.Identity.OperationID)
			if err != nil || !reflect.DeepEqual(unchanged, target) || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
				t.Fatal("terminal adoption proof changed original/core authority", err)
			}
		})
	}
}

func TestRecoveryTerminalPreflightRolledBackStartStaysDiagnostic(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	f := lifecycleRunningFixture(t)
	stored, state, _ := managedAdmit(t, f, "unsupported-rolled-back-start")
	// This is well-decoded adoption evidence in an unsupported terminal phase,
	// not an observed rollback/adoption success. Only Committed runtime.start
	// belongs to the new preflight; this keeps the prior diagnostic admission.
	target, err := f.c.advanceAdoption(stored, state, RollingBack, "synthetic-unsupported-rollback")
	if err != nil {
		t.Fatal(err)
	}
	target, err = f.c.advanceAdoption(target, state, RolledBack, "synthetic-unsupported-rolled-back-start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAdoption(target); err != nil {
		t.Fatal("fixture not well-decoded", err)
	}
	raw := recoveryRequest(t, "diagnose-rolled-back-start", target.Record.Identity.OperationID, f.base.Revision)
	request, _ := ParseRequest(raw)
	protected := managedProtectedTree(t, f.paths)
	calls := 0
	verify := func(_ context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
		calls++
		if id != f.source || op != *state.Base.OperationID {
			t.Fatal("wrong publishing identity")
		}
		call, err := f.c.journal.Get(request.OperationID())
		if err != nil || call.Record.Phase != Applying {
			t.Fatal("noncommitted start was incorrectly preflighted", err)
		}
		return state.Identity, nil
	}
	call, made, err := terminalPreflightCall(t, f.c, context.Background(), raw, verify, nil)
	if !made || !errors.Is(err, ErrRecoveryRequired) || call.Record.Phase != Degraded || calls != 1 {
		t.Fatal("unsupported terminal start lost diagnostic path", made, call.Record.Phase, calls, err)
	}
	unchanged, err := f.c.journal.Get(target.Record.Identity.OperationID)
	if err != nil || !reflect.DeepEqual(unchanged, target) || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
		t.Fatal("diagnostic recovery changed terminal/core authority", err)
	}
}
