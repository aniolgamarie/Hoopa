//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

// Actual approved core, test-only precompiled Source/Intent and existing private
// cut seam. Each subtest creates a fresh scenario, never edits terminal history.
func TestRecoveryCoreOpenTargetAfterCoordinatorReopen(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit fixed approved basic core required")
	}
	for _, point := range []string{"public-after-reopen", "recover-target-terminal"} {
		t.Run(point, func(t *testing.T) {
			raw, e := os.ReadFile(source)
			if e != nil {
				t.Fatal(e)
			}
			if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
				t.Fatal("wrong approved binary")
			}
			private := filepath.Join(t.TempDir(), "private")
			if e := os.Mkdir(private, 0700); e != nil {
				t.Fatal(e)
			}
			binary := filepath.Join(private, "mihomo")
			if e := os.WriteFile(binary, raw, 0755); e != nil {
				t.Fatal(e)
			}
			var endpoints []string
			var reservations []*net.TCPListener
			for i := 0; i < 2; i++ {
				l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
				reservations = append(reservations, l)
				endpoints = append(endpoints, l.Addr().String())
			}
			for _, l := range reservations {
				l.Close()
			}
			seed, material := applyCoreMaterial(t, binary, endpoints[0], endpoints[1], "recovery-fixed-generation")
			impact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(material.Manifest.Generation), ConnectionEffect: "unknown", RuntimeAction: "start"}
			cap, e := mihomo.ApprovedBasicCapability(binary)
			if e != nil {
				t.Fatal(e)
			}
			capSHA, e := cap.Digest()
			if e != nil {
				t.Fatal(e)
			}
			current := CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Inputs: []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: cap.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}}, Impact: impact}
			var obj map[string]json.RawMessage
			if e := json.Unmarshal(seed.Bytes(), &obj); e != nil {
				t.Fatal(e)
			}
			validator := planFixtureValidator(t, obj)
			paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
			if e != nil {
				t.Fatal(e)
			}
			c, e := OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil })
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if c != nil {
					c.Close()
				}
			}()
			handles := map[config.RunID]*platform.ProcessHandle{}
			var retained *platform.ProcessHandle
			var intent config.RunIntent
			var id config.RunID
			defer func() {
				if retained == nil {
					if c != nil && c.store != nil {
						applyCoreCleanup(t, c.store, binary, handles)
					} else {
						t.Error("core cleanup proof unavailable; no numeric PID signal")
					}
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				defer retained.Close()
				if e := retained.VerifyArguments(ctx, intent.Arguments); e != nil {
					t.Error("cleanup argv proof unavailable; no signal", e)
					return
				}
				e := retained.SignalVerified(ctx, syscall.SIGTERM)
				if e == nil {
					e = retained.WaitExited(ctx)
				}
				if e == nil {
					e = applyCoreAbsent(ctx, retained.Identity().PID)
				}
				if e != nil {
					t.Error("task-owned original pidfd cleanup incomplete", e)
				} else {
					t.Logf("task-only recovery core cleanup pid=%d original_pidfd_exit=true proc_absent=true", retained.Identity().PID)
				}
			}()
			var sources atomic.Int32
			sourceFn := func(context.Context, Plan) (BasicMaterial, error) { sources.Add(1); return material, nil }
			applier, e := NewBasicApplier(c, sourceFn, applyCoreEntryArgs())
			if e != nil {
				t.Fatal(e)
			}
			active, e := c.store.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			plan := applyCorePlan(t, seed, active.Revision, impact)
			applyRaw := coordinatorRequest(t, "recovery-core-apply", plan, nil)
			request, e := ParseRequest(applyRaw)
			if e != nil {
				t.Fatal(e)
			}
			cut := errors.New("actual cut after active publication")
			closedRejected := false
			original, e := func() (StoredOperation, error) {
				c.mu.Lock()
				defer c.mu.Unlock()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				var checked checkedBasicMaterial
				var state applyEvidence
				stored, made, _, e := c.admitCheckedApplyLocked(ctx, request, func(plan Plan, base config.ActivePointer) error {
					input, e := applier.source(ctx, plan)
					if e != nil {
						return e
					}
					checked, e = checkBasicMaterial(ctx, plan, input)
					if e != nil {
						return e
					}
					old, e := c.activeRunForApply(base, plan.Document().Impact.RuntimeAction, checked.launch.Workdir)
					if e != nil {
						return e
					}
					state = applyEvidence{Version: 1, Base: base, OldRunID: old, OldStopped: old == nil, TargetManifestSHA256: checked.launch.ManifestSHA256}
					if e := ctx.Err(); e != nil {
						return e
					}
					if e := c.store.CheckOperationAdmission(); e != nil {
						return e
					}
					sha, e := c.store.PublishBundle(checked.manifest, checked.files)
					if e != nil {
						return e
					}
					if sha != state.TargetManifestSHA256 {
						return config.ErrIntegrity
					}
					return nil
				})
				if e != nil || !made {
					t.Fatal("actual admitted fixture", made, e)
				}
				runtime, e := newBasicApplyRuntime(c.store, checked, applyCoreEntryArgs())
				if e != nil {
					t.Fatal(e)
				}
				defer runtime.close()
				return c.executeBasicApply(ctx, stored, state, runtime, func(step string) error {
					if step == "activate-target" {
						if runtime.registered == nil || runtime.registered.Observation().Gate != "closed" {
							t.Fatal("missing real closed registration")
						}
						if e := runtime.controller.VerifyActivation(ctx, runtime.registered, stored.Record.Identity.OperationID); !errors.Is(e, mihomo.ErrController) {
							t.Fatal("closed VerifyActivation accepted", e)
						}
						closedRejected = true
						id = runtime.registered.Observation().RunID
						var e error
						intent, _, e = c.store.ReadRunIntent(id)
						if e != nil {
							t.Fatal(e)
						}
						birth, _, e := c.store.ReadRunBirth(id)
						if e != nil {
							t.Fatal(e)
						}
						retained, e = platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: binary, BinarySHA256: cap.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
						if e != nil {
							t.Fatal(e)
						}
						if retained.Identity() != runtime.registered.Identity() || retained.Identity().PID != birth.PID || retained.Identity().StartTicks != birth.StartTicks || retained.Identity().BootID != birth.BootID || retained.Identity().UID != birth.UID || retained.VerifyArguments(ctx, intent.Arguments) != nil {
							t.Fatal("original closed pidfd mismatch")
						}
					}
					if step == "active-published" {
						return cut
					}
					return nil
				})
			}()
			if !errors.Is(e, cut) || original.Record.Phase != Publishing || !closedRejected || retained == nil {
				t.Fatal("actual active-published cut", original.Record.Phase, e)
			}
			identity := retained.Identity()
			if e := retained.VerifyArguments(applyCoreContext(t), intent.Arguments); e != nil {
				t.Fatal(e)
			}
			// The authoritative retained handle is independent of Coordinator.Close.
			if e := c.Close(); e != nil {
				t.Fatal(e)
			}
			if e := retained.VerifyArguments(applyCoreContext(t), intent.Arguments); e != nil {
				t.Fatal("coordinator Close killed core", e)
			}
			c, e = OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) {
				t.Fatal("Recover consulted current compiler")
				return CurrentPlanInputs{}, nil
			})
			if e != nil {
				t.Fatal(e)
			}
			applier, e = NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
				sources.Add(1)
				t.Fatal("Recover called Source")
				return BasicMaterial{}, ErrPlan
			}, applyCoreEntryArgs())
			if e != nil {
				t.Fatal(e)
			}
			beforeIDs, e := c.store.ListRunIDs()
			if e != nil || !reflect.DeepEqual(beforeIDs, []config.RunID{id}) {
				t.Fatal("pre-recovery run census", beforeIDs, e)
			}
			active, e = c.store.ReadActive()
			if e != nil {
				t.Fatal(e)
			}
			recoverRaw := recoveryRequest(t, "recovery-core-call", original.Record.Identity.OperationID, active.Revision)
			var call StoredOperation
			var made bool
			if point == "recover-target-terminal" {
				request, e := ParseRequest(recoverRaw)
				if e != nil {
					t.Fatal(e)
				}
				interrupted := errors.New("actual target-terminal cut")
				call, made, e = func() (StoredOperation, bool, error) {
					c.mu.Lock()
					defer c.mu.Unlock()
					return c.recoverBasicLocked(applyCoreContext(t), request, c.verifyRecoveredRun, func(step string) error {
						if step == "recover-target-terminal" {
							return interrupted
						}
						return nil
					})
				}()
				if !made || !errors.Is(e, interrupted) || call.Record.Phase != Applying {
					t.Fatal("actual target-terminal cut", made, e)
				}
				terminal, e := c.journal.Get(original.Record.Identity.OperationID)
				if e != nil || terminal.Record.Phase != Committed {
					t.Fatal("original not terminal at cut", e)
				}
				firstCall := call
				if e := c.Close(); e != nil {
					t.Fatal(e)
				}
				if e := retained.VerifyArguments(applyCoreContext(t), intent.Arguments); e != nil {
					t.Fatal("second Close killed core", e)
				}
				c, e = OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) {
					t.Fatal("Recover compiler dependency")
					return CurrentPlanInputs{}, nil
				})
				if e != nil {
					t.Fatal(e)
				}
				applier, e = NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
					t.Fatal("second Recover Source")
					return BasicMaterial{}, ErrPlan
				}, applyCoreEntryArgs())
				if e != nil {
					t.Fatal(e)
				}
				active, e = c.store.ReadActive()
				if e != nil {
					t.Fatal(e)
				}
				recoverRaw = recoveryRequest(t, "recovery-core-second-call", original.Record.Identity.OperationID, active.Revision)
				call, made, e = applier.Recover(applyCoreContext(t), recoverRaw)
				old, oe := c.journal.Get(firstCall.Record.Identity.OperationID)
				if oe != nil || old.Record.Phase != Committed || !reflect.DeepEqual(old.Record.Identity, firstCall.Record.Identity) {
					t.Fatal("older actual recover call not completed", oe)
				}
			} else {
				call, made, e = applier.Recover(applyCoreContext(t), recoverRaw)
			}
			if e != nil || !made || call.Record.Phase != Committed {
				t.Fatal("actual public Recover", made, e)
			}
			final, e := c.journal.Get(original.Record.Identity.OperationID)
			if e != nil || final.Record.Phase != Committed || !reflect.DeepEqual(final.Record.Identity, original.Record.Identity) {
				t.Fatal("original identity/finalization", e)
			}
			recovered, gotIdentity := applyCoreHandle(t, c.store, id)
			defer recovered.Close()
			if gotIdentity != identity {
				t.Fatal("Recover changed actual instance")
			}
			controller, e := mihomo.NewController(c.store, cap)
			if e != nil {
				t.Fatal(e)
			}
			owner, e := owned.NewOwnership(c.store, controller)
			if e != nil {
				t.Fatal(e)
			}
			observed, e := owner.Observe(applyCoreContext(t), id)
			if e != nil {
				t.Fatal(e)
			}
			e = controller.VerifyActivation(applyCoreContext(t), observed, original.Record.Identity.OperationID)
			observed.Close()
			if e != nil {
				t.Fatal("actual original activation receipt proof", e)
			}
			echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "recovery-echo:"+r.URL.Path) }))
			defer echo.Close()
			status, body := applyCoreHTTP(t, endpoints[1], strings.TrimPrefix(echo.URL, "http://"), "after", true)
			if status != 200 || body != "recovery-echo:/after" {
				t.Fatal("same core echo after recovery", status)
			}
			ids, e := c.store.ListRunIDs()
			if e != nil || !reflect.DeepEqual(ids, beforeIDs) || sources.Load() != 1 {
				t.Fatal("Recover created new run/material effect", ids, e, sources.Load())
			}
			before := coordinatorTree(t, paths)
			replay, made, e := applier.Recover(nil, recoverRaw)
			if e != nil || made || !reflect.DeepEqual(replay, call) {
				t.Fatal("actual independent call replay", e)
			}
			replay, made, e = applier.Apply(nil, applyRaw)
			if e != nil || made || !reflect.DeepEqual(replay, final) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("actual original replay changed state", e)
			}
			if e := retained.VerifyArguments(applyCoreContext(t), intent.Arguments); e != nil {
				t.Fatal("recovery/replay signalled core", e)
			}
			t.Logf("actual recovery scenario=%s run=%s identity=%+v same_instance=true closed_activation_rejected=true source_calls=1 run_inventory=1 authorized_local_echo=1 coordinator_reopens_do_not_signal=true", point, id, identity)
		})
	}
}
