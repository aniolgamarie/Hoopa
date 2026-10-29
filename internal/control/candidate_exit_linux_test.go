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
	owned "hoopa/internal/runtime"
)

// The historical proof and process verifier here are synthetic. These tests
// exercise real journal/original-file integrity, not an OS Wait observation.
func TestRecoveryDirectChildExitOriginals(t *testing.T) {
	for _, fault := range []string{"none", "missing-proof", "missing-birth", "changed-birth", "intent-hash", "birth-hash", "wrong-pid", "wrong-run", "wrong-operation", "nonterminal-wait", "wrong-kind", "wrong-version", "not-stopped", "registration-present", "registration-corrupt"} {
		t.Run(fault, func(t *testing.T) {
			f := recoveryOrderingFixture(t, true)
			state, err := decodeApplyEvidence(f.target)
			if err != nil {
				t.Fatal(err)
			}
			id, op := *state.TargetRunID, f.target.Record.Identity.OperationID
			intent, intentSHA, err := f.c.store.ReadRunIntent(id)
			if err != nil {
				t.Fatal(err)
			}
			birth, birthSHA, err := f.c.store.ReadRunBirth(id)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.paths.State, "runs", hash([]byte(id)))
			regPath := filepath.Join(dir, "registration-"+hash([]byte(op))+".json")
			if fault != "registration-present" && fault != "registration-corrupt" {
				if err = os.Remove(regPath); err != nil {
					t.Fatal(err)
				}
			}
			state.TargetExit = &owned.LaunchExitProof{Version: 1, Kind: "direct_child_wait", RunID: id, OperationID: op, IntentSHA256: intentSHA, BirthSHA256: birthSHA, PID: birth.PID, WaitStatus: 1 << 8}
			if err = state.TargetExit.Match(intent, intentSHA, birth, birthSHA); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "missing-proof":
				state.TargetExit = nil
			case "missing-birth":
				err = os.Remove(filepath.Join(dir, "birth.json"))
			case "changed-birth":
				birth.StartTicks++
				var raw []byte
				raw, err = json.Marshal(birth)
				if err == nil {
					err = os.WriteFile(filepath.Join(dir, "birth.json"), append(raw, '\n'), 0600)
				}
			case "intent-hash":
				state.TargetExit.IntentSHA256 = strings.Repeat("0", 64)
			case "birth-hash":
				state.TargetExit.BirthSHA256 = strings.Repeat("0", 64)
			case "wrong-pid":
				state.TargetExit.PID++
			case "wrong-run":
				state.TargetExit.RunID = "other-run"
			case "wrong-operation":
				state.TargetExit.OperationID = "other-operation"
			case "nonterminal-wait":
				state.TargetExit.WaitStatus = 0x137f
			case "wrong-kind":
				state.TargetExit.Kind = "proc_absent"
			case "wrong-version":
				state.TargetExit.Version = 2
			case "not-stopped":
				state.TargetStopped = false
			case "registration-corrupt":
				err = os.WriteFile(regPath, []byte("{}\n"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := encode(state)
			if err != nil {
				t.Fatal(err)
			}
			f.target, err = f.c.journal.Advance(op, f.target.SHA256, f.target.Record.Phase, raw, json.RawMessage(f.target.Record.Result))
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.c.store.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, made, err := recoveryCall(t, f.c, recoveryCurrentRequest(t, f, "recover-child-exit"), recoveryVerifier(t, f, &calls), nil)
			if fault == "none" {
				if err != nil || !made || calls == 0 {
					t.Fatal("bound historical child exit refused", made, calls, err)
				}
				stored, e := f.c.journal.Get(op)
				if e != nil || stored.Record.Phase != RolledBack {
					t.Fatal("rollback recovery", e)
				}
			} else {
				if err == nil || calls != 0 {
					t.Fatal("invalid exit authority reached runtime verification", calls, err)
				}
				after, e := f.c.store.ReadActive()
				if e != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("bad witness changed active", e)
				}
			}
		})
	}
}

func TestDirectChildExitDurabilityBeforeRollbackEffects(t *testing.T) {
	for _, point := range []string{"rollback-candidate-exited", "rollback-prepare-workdir", "invalid-proof"} {
		t.Run(point, func(t *testing.T) {
			c, paths, stored, state, runtime, request := rollbackOrderingFixture(t)
			runtime.candidateProof = func(id config.RunID, op config.ID) (*owned.LaunchExitProof, error) {
				intent, intentSHA, err := c.store.ReadRunIntent(id)
				if err != nil {
					t.Fatal(err)
				}
				// Synthetic file originals, not ClaimLauncher or real Wait.
				birth := config.RunBirth{Version: 1, RunID: id, IntentSHA256: intentSHA, PID: 4242, UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
				raw, err := json.Marshal(birth)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(paths.State, "runs", hash([]byte(id)), "birth.json"), append(raw, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				_, birthSHA, err := c.store.ReadRunBirth(id)
				if err != nil {
					t.Fatal(err)
				}
				proof := &owned.LaunchExitProof{Version: 1, Kind: "direct_child_wait", RunID: id, OperationID: op, IntentSHA256: intentSHA, BirthSHA256: birthSHA, PID: birth.PID, WaitStatus: 1 << 8}
				if err = proof.Match(intent, intentSHA, birth, birthSHA); err != nil {
					t.Fatal(err)
				}
				if point == "invalid-proof" {
					proof.BirthSHA256 = strings.Repeat("0", 64)
				}
				return proof, nil
			}
			cut := errors.New("candidate exit checkpoint cut")
			target, err := c.executeBasicApply(context.Background(), stored, state, runtime, func(step string) error {
				if step == point {
					return cut
				}
				return nil
			})
			if point == "invalid-proof" {
				if err == nil || target.Record.Phase != Degraded {
					t.Fatal("invalid proof permitted rollback", err)
				}
			} else if !errors.Is(err, cut) {
				t.Fatal("missing checkpoint cut", err)
			}
			persisted, err := decodeApplyEvidence(target)
			if err != nil {
				t.Fatal(err)
			}
			wantProof := point == "rollback-prepare-workdir"
			if persisted.TargetStopped != wantProof || (persisted.TargetExit != nil) != wantProof || runtime.restored != nil || persisted.RollbackRunID != nil {
				t.Fatal("exit proof/effect persistence boundary incorrect", persisted.Step)
			}
			if wantProof {
				if persisted.RollbackWorkdir == "" {
					t.Fatal("missing durable rollback workdir intent")
				}
				if _, err = os.Stat(filepath.Join(paths.Runtime, persisted.RollbackWorkdir)); !os.IsNotExist(err) {
					t.Fatal("rollback directory created before durable checkpoint", err)
				}
			}
			before := coordinatorTree(t, paths)
			replayed, made, err := recoveryApplier(t, c).Apply(nil, request)
			if err != nil || made || !reflect.DeepEqual(replayed, target) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
				t.Fatal("replay resumed child-exit rollback", made, err)
			}
			calls := 0
			verify := func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
				calls++
				return platform.ProcessIdentity{}, nil
			}
			_, _, err = recoveryCall(t, c, recoveryRequest(t, "recover-incomplete-child-exit", target.Record.Identity.OperationID, state.Base.Revision), verify, nil)
			if err == nil || calls != 0 || runtime.restored != nil {
				t.Fatal("incomplete child-exit rollback reconstructed launch", calls, err)
			}
		})
	}
}

func TestUnregisteredCandidateCannotInventChildExit(t *testing.T) {
	f := recoveryOrderingFixture(t, true)
	state, err := decodeApplyEvidence(f.target)
	if err != nil {
		t.Fatal(err)
	}
	id, op := *state.TargetRunID, f.target.Record.Identity.OperationID
	path := filepath.Join(f.paths.State, "runs", hash([]byte(id)), "registration-"+hash([]byte(op))+".json")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, f.paths)
	r := &basicRuntime{store: f.c.store}
	proof, err := r.stopCandidate(context.Background(), id, op)
	if err == nil || proof != nil || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
		t.Fatal("missing live child became exit proof", err)
	}
	// A public zero value cannot reconstruct a consumed in-memory launch handle.
	r.child = &owned.LaunchChild{}
	proof, err = r.stopCandidate(context.Background(), id, op)
	if err == nil || proof != nil || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
		t.Fatal("zero live child became exit proof", err)
	}
}
