//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"hoopa/internal/config"
	"hoopa/internal/platform"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These preflight tests use real Store/journal publications and the explicitly
// synthetic lifecycle fixture. No process or adoption success is claimed here.
func TestManagedStartPreflightZeroWrites(t *testing.T) {
	for _, name := range []string{"nil-context", "canceled", "stale", "control-only", "missing-flag", "null-flag", "extra", "wrong-operation", "wrong-version", "reused-id", "pending", "unverified-running", "degraded"} {
		t.Run(name, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			raw := lifecycleRequest(t, "managed-refused", "runtime.start", f.base.Revision)
			var obj map[string]any
			if e := json.Unmarshal(raw, &obj); e != nil {
				t.Fatal(e)
			}
			params := obj["params"].(map[string]any)
			ctx := context.Background()
			want := ErrRequest
			switch name {
			case "nil-context":
				ctx = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "stale":
				params["expected_revision"] = "stale"
				want = ErrConflict
			case "control-only":
				params["control_only"] = true
			case "missing-flag":
				delete(params, "control_only")
			case "null-flag":
				params["control_only"] = nil
			case "extra":
				params["extra"] = true
			case "wrong-operation":
				obj["operation"] = "runtime.stop"
			case "wrong-version":
				obj["api_version"] = "v2"
			case "reused-id":
				obj["request_id"] = "ordered-apply"
				ctx = nil
				want = ErrRequestIDReused
			case "unverified-running":
				want = ErrRecoveryRequired
			case "degraded":
				stored, state, _ := f.admit(t, "unverified-stop-before-adoption", "stop")
				f.fail = "unknown"
				failed, err := f.c.executeStop(context.Background(), stored, state, f, nil)
				if err == nil || failed.Record.Phase != Degraded {
					t.Fatal("bad degraded fixture", err)
				}
				want = ErrRecoveryRequired
			case "pending":
				f.admit(t, "managed-blocking-stop", "stop")
				want = ErrRecoveryRequired
			}
			before := coordinatorTree(t, f.paths)
			_, made, e := recoveryApplier(t, f.c).EnsureStarted(ctx, planRaw(t, obj))
			if made || !errors.Is(e, want) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("managed start refusal mutated store or wrong error", made, e, want)
			}
		})
	}
}

// Preserve all files, directory metadata and runtime material, excluding only
// the operation journal where adoption and explicit recovery add receipts.
func managedProtectedTree(t *testing.T, p config.Paths) map[string]string {
	t.Helper()
	tree := coordinatorTree(t, p)
	ops := filepath.Join(p.State, "operations")
	for path := range tree {
		if path == ops || strings.HasPrefix(path, ops+string(filepath.Separator)) {
			delete(tree, path)
		}
	}
	return tree
}
func managedIdentity(t *testing.T, f *lifecycleFixture) platform.ProcessIdentity {
	t.Helper()
	i, _, e := f.c.store.ReadRunIntent(f.source)
	if e != nil {
		t.Fatal(e)
	}
	return platform.ProcessIdentity{PID: 4242, UID: 1002, BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 42, BinaryPath: i.Binary.Path, BinarySHA256: i.Binary.SHA256, Workdir: i.Workdir, NonceSHA256: i.NonceSHA256}
}
func managedAdmit(t *testing.T, f *lifecycleFixture, id string) (StoredOperation, adoptionEvidence, []byte) {
	t.Helper()
	raw := lifecycleRequest(t, id, "runtime.start", f.base.Revision)
	request, e := ParseRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	stored, state, made, e := f.c.admitAdoption(request, f.base, f.source, managedIdentity(t, f))
	if e != nil || !made {
		t.Fatal(made, e)
	}
	return stored, state, raw
}
func managedVerifier(t *testing.T, f *lifecycleFixture, state adoptionEvidence, calls *int) recoveredRunVerifier {
	t.Helper()
	return func(ctx context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
		if ctx == nil || id != f.source || op != *state.Base.OperationID {
			t.Fatal("adoption verifier not bound to original publishing operation")
		}
		*calls++
		return state.Identity, nil
	}
}
func TestManagedStartAdoptionOrderingReplay(t *testing.T) {
	f := lifecycleRunningFixture(t)
	before := managedProtectedTree(t, f.paths)
	oldIDs, e := f.c.journal.IDs()
	if e != nil {
		t.Fatal(e)
	}
	old := map[config.ID]StoredOperation{}
	for _, id := range oldIDs {
		v, e := f.c.journal.Get(id)
		if e != nil {
			t.Fatal(e)
		}
		old[id] = v
	}
	stored, state, raw := managedAdmit(t, f, "synthetic-adopt")
	calls := 0
	got, e := f.c.executeAdoption(context.Background(), stored, state, managedVerifier(t, f, state, &calls), nil)
	if e != nil || got.Record.Phase != Committed || calls != 3 {
		t.Fatal("adoption ordered proof count", e, calls)
	}
	decoded, e := decodeAdoption(got)
	if e != nil || decoded.Kind != "adopt-running" || decoded.Identity != state.Identity || decoded.RunID != f.source || !reflect.DeepEqual(decoded.Base, f.base) || got.Record.Identity.Intent.TargetRevision != nil {
		t.Fatal("adoption identity/authority receipt", e)
	}
	want := []string{"adopt-validated", "adopt-applying", "adopt-verifying", "adopt-publishing", "adopt-committed"}
	steps := []string{}
	for _, s := range got.Record.Steps {
		var v adoptionEvidence
		if e := json.Unmarshal(s.Evidence, &v); e != nil {
			t.Fatal(e)
		}
		if v.Step != "" {
			steps = append(steps, v.Step)
		}
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatal("durable adoption steps", steps)
	}
	if !reflect.DeepEqual(before, managedProtectedTree(t, f.paths)) {
		t.Fatal("adoption rewrote run/birth/registration/bundle/active/runtime")
	}
	for id, v := range old {
		now, e := f.c.journal.Get(id)
		if e != nil || !reflect.DeepEqual(now, v) {
			t.Fatal("adoption changed original journal", e)
		}
	}
	ids, e := f.c.journal.IDs()
	if e != nil || len(ids) != len(oldIDs)+1 {
		t.Fatal("adoption created more than one receipt", e)
	}
	// Replay must precede current state/revision and context checks.
	changed := f.base
	changed.Revision = "synthetic-later-revision"
	if e := f.c.store.PublishActive(changed); e != nil {
		t.Fatal(e)
	}
	whole := coordinatorTree(t, f.paths)
	replayed, made, e := recoveryApplier(t, f.c).EnsureStarted(nil, raw)
	if e != nil || made || replayed.SHA256 != got.SHA256 || !reflect.DeepEqual(whole, coordinatorTree(t, f.paths)) {
		t.Fatal("adoption replay depended on new revision/context", e)
	}
	obj := map[string]any{}
	if e := json.Unmarshal(raw, &obj); e != nil {
		t.Fatal(e)
	}
	obj["params"].(map[string]any)["expected_revision"] = "different"
	_, made, e = recoveryApplier(t, f.c).EnsureStarted(nil, planRaw(t, obj))
	if made || !errors.Is(e, ErrRequestIDReused) || !reflect.DeepEqual(whole, coordinatorTree(t, f.paths)) {
		t.Fatal("changed ID replay", e)
	}
}
func TestManagedStartAdoptionCutsRecoverAfterReopen(t *testing.T) {
	for _, cut := range []string{"adopt-validated", "adopt-applying", "adopt-verifying", "adopt-publishing", "adopt-committed"} {
		t.Run(cut, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			before := managedProtectedTree(t, f.paths)
			stored, state, raw := managedAdmit(t, f, "cut-adopt")
			calls := 0
			verify := managedVerifier(t, f, state, &calls)
			target, e := f.c.executeAdoption(context.Background(), stored, state, verify, func(step string) error {
				if step == cut {
					return errors.New("synthetic durable cut")
				}
				return nil
			})
			if !errors.Is(e, errRecoveryCut) {
				t.Fatal(e)
			}
			original := target.Record.Identity
			validator, current := f.c.validator, f.c.current
			if e := f.c.Close(); e != nil {
				t.Fatal(e)
			}
			fresh, e := OpenCoordinator(f.paths, validator, current)
			if e != nil {
				t.Fatal(e)
			}
			defer fresh.Close()
			f.c = fresh
			if !reflect.DeepEqual(before, managedProtectedTree(t, f.paths)) {
				t.Fatal("reopen changed protected state")
			}
			proofCalls := 0
			verify = managedVerifier(t, f, state, &proofCalls)
			recoverRaw := recoveryRequest(t, "synthetic-recover-adopt", original.OperationID, f.base.Revision)
			call, made, e := recoveryCall(t, f.c, recoverRaw, verify, nil)
			if e != nil || !made || call.Record.Phase != Committed || proofCalls < 1 {
				t.Fatal("recovery did not freshly prove original", made, e, proofCalls)
			}
			final, e := f.c.journal.Get(original.OperationID)
			if e != nil || final.Record.Phase != Committed || !reflect.DeepEqual(final.Record.Identity, original) {
				t.Fatal("adoption origin changed", e)
			}
			if !reflect.DeepEqual(before, managedProtectedTree(t, f.paths)) {
				t.Fatal("adoption recovery rewrote runtime authority")
			}
			whole := coordinatorTree(t, f.paths)
			same, made, e := recoveryApplier(t, f.c).EnsureStarted(nil, raw)
			if e != nil || made || same.SHA256 != final.SHA256 {
				t.Fatal(e)
			}
			same, made, e = recoveryApplier(t, f.c).Recover(nil, recoverRaw)
			if e != nil || made || same.SHA256 != call.SHA256 || !reflect.DeepEqual(whole, coordinatorTree(t, f.paths)) {
				t.Fatal("recovered exact replay changed files", e)
			}
		})
	}
}
func TestManagedStartAdoptionProofMismatch(t *testing.T) {
	for _, field := range []string{"PID", "UID", "boot", "start", "binary-path", "binary-sha", "workdir", "nonce", "error", "before-publish", "before-commit"} {
		t.Run(field, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			protected := managedProtectedTree(t, f.paths)
			stored, state, _ := managedAdmit(t, f, "mismatch-adopt")
			count := 0
			verify := func(ctx context.Context, id config.RunID, op config.ID) (platform.ProcessIdentity, error) {
				if id != f.source || op != *f.base.OperationID {
					t.Fatal("wrong proof binding")
				}
				count++
				identity := state.Identity
				if field == "before-publish" || field == "before-commit" {
					threshold := 2
					if field == "before-commit" {
						threshold = 3
					}
					if count < threshold {
						return identity, nil
					}
					identity.StartTicks++
					return identity, nil
				}
				switch field {
				case "PID":
					identity.PID++
				case "UID":
					identity.UID++
				case "boot":
					identity.BootID = "11111111-1111-1111-1111-111111111111"
				case "start":
					identity.StartTicks++
				case "binary-path":
					identity.BinaryPath += "-foreign"
				case "binary-sha":
					identity.BinarySHA256 = strings.Repeat("a", 64)
				case "workdir":
					identity.Workdir += "-foreign"
				case "nonce":
					identity.NonceSHA256 = strings.Repeat("d", 64)
				case "error":
					return identity, errors.New("synthetic proof unavailable")
				}
				return identity, nil
			}
			got, e := f.c.executeAdoption(context.Background(), stored, state, verify, nil)
			wantCalls := 1
			if field == "before-publish" {
				wantCalls = 2
			}
			if field == "before-commit" {
				wantCalls = 3
			}
			if e == nil || count != wantCalls || got.Record.Phase != Degraded || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
				t.Fatal("mismatched proof adopted or changed runtime", e, count, got.Record.Phase)
			}
		})
	}
}
func TestManagedStartUnknownPreparedNotAdopted(t *testing.T) {
	f := lifecycleRunningFixture(t)
	stored, _, _ := managedAdmit(t, f, "prepared-adopt")
	proofs := 0
	call, made, e := recoveryCall(t, f.c, recoveryRequest(t, "recover-unknown-prepared", stored.Record.Identity.OperationID, f.base.Revision), func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
		proofs++
		return managedIdentity(t, f), nil
	}, nil)
	if !made || e == nil || call.Record.Phase != Degraded || proofs != 0 {
		t.Fatal("empty prepared evidence became adoption proof", made, e, proofs)
	}
	got, e := f.c.journal.Get(stored.Record.Identity.OperationID)
	if e != nil || got.Record.Phase != Degraded {
		t.Fatal(e)
	}
}

func TestManagedStartStoppedRequiresOriginalProof(t *testing.T) {
	f := lifecycleRunningFixture(t)
	f.stopped(t)
	base, e := f.c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(f.paths.State, "runs", hash([]byte(f.source)), "birth.json")); e != nil {
		t.Fatal(e)
	}
	before := coordinatorTree(t, f.paths)
	_, made, e := recoveryApplier(t, f.c).EnsureStarted(context.Background(), lifecycleRequest(t, "stopped-missing-proof", "runtime.start", base.Revision))
	if made || e == nil || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
		t.Fatal("stopped delegation bypassed original proof", made, e)
	}
}

// Structurally valid journal records with deliberately invalid adoption bodies
// are storage-corruption fixtures, never process or committed-effect evidence.
func TestManagedStartMalformedAdoptionRecovery(t *testing.T) {
	for _, terminalTarget := range []bool{false, true} {
		for _, field := range []string{"version", "base-binding"} {
			name := field + "/pending"
			if terminalTarget {
				name = field + "/terminal"
			}
			t.Run(name, func(t *testing.T) {
				f := lifecycleRunningFixture(t)
				stored, state, _ := managedAdmit(t, f, "malformed-adoption")
				if field == "version" {
					state.Version = 2
				} else {
					state.Base.Revision = "incorrect-base-revision"
				}
				phases := []Phase{Validated}
				if terminalTarget {
					phases = append(phases, Applying, Verifying, Publishing, Committed)
				}
				var e error
				for _, phase := range phases {
					stored, e = f.c.advanceAdoption(stored, state, phase, "adopt-"+string(phase))
					if e != nil {
						t.Fatal("synthetic malformed body publication", e)
					}
				}
				evidence := append([]byte(nil), stored.Record.Steps[len(stored.Record.Steps)-1].Evidence...)
				protected := managedProtectedTree(t, f.paths)
				proofs := 0
				raw := recoveryRequest(t, "recover-malformed-adoption", stored.Record.Identity.OperationID, f.base.Revision)
				call, made, e := recoveryCall(t, f.c, raw, func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
					proofs++
					return managedIdentity(t, f), nil
				}, nil)
				if !made || e == nil || call.Record.Phase != Degraded || proofs != 0 {
					t.Fatal("malformed recovery call outcome", made, e, call.Record.Phase, proofs)
				}
				target, readErr := f.c.journal.Get(stored.Record.Identity.OperationID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if terminalTarget {
					if !reflect.DeepEqual(target, stored) {
						t.Fatal("terminal malformed original rewritten")
					}
				} else if target.Record.Phase != Degraded {
					t.Fatal("malformed pending target not marked degraded", target.Record.Phase)
				}
				if string(target.Record.Steps[len(target.Record.Steps)-1].Evidence) != string(evidence) || !reflect.DeepEqual(protected, managedProtectedTree(t, f.paths)) {
					t.Fatal("malformed recovery lost raw evidence or changed authority")
				}
			})
		}
	}
}
