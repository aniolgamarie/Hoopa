//go:build linux && amd64

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

// This synthetic inspector only proves the runtime termination boundary. The
// real HTTP snapshot/failed-receipt predicate has separate mihomo tests.
type candidateTerminationFixtureInspector struct {
	ownershipInspector
	change           string
	after            func() error
	terminationCalls int
}

func (i *candidateTerminationFixtureInspector) InspectTermination(ctx context.Context, conn *net.TCPConn, intent config.RunIntent, reg config.RunRegistration, sha string) (ControllerObservation, error) {
	i.terminationCalls++
	obs, err := i.ownershipInspector.Inspect(ctx, conn, intent)
	if err != nil {
		return obs, err
	}
	if reg.Gate != "closed" || reg.OperationID != intent.OperationID || len(sha) != 64 {
		return ControllerObservation{}, errors.New("unbound fixture registration")
	}
	if i.after != nil {
		if err := i.after(); err != nil {
			return obs, err
		}
	}
	switch i.change {
	case "config":
		obs.ConfigSHA256 = ownershipDigest([]byte("wrong-config"))
	case "run":
		obs.RunID = "wrong-run"
	case "generation":
		obs.Generation = "wrong-generation"
	case "binary":
		obs.BinaryVersion = "wrong-version"
	case "gate":
		obs.Gate = "draining"
	case "error":
		return obs, errors.New("injected inspector unavailable")
	case "mutate-argv":
		intent.Arguments[0] = "/different-argv"
	}
	return obs, nil
}

func candidateTerminationFixture(t *testing.T) (*ownershipFixture, *Ownership, *OwnedRun, *candidateTerminationFixtureInspector) {
	t.Helper()
	f := startOwnershipFixture(t, "")
	i := &candidateTerminationFixtureInspector{}
	o := ownershipManager(t, f.store, i)
	run, _, err := o.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { run.Close() })
	return f, o, run, i
}

func TestTerminateCandidateVerifiedStatesAndOriginalExit(t *testing.T) {
	for _, gate := range []string{"closed", "open", "degraded"} {
		t.Run(gate, func(t *testing.T) {
			f, o, run, i := candidateTerminationFixture(t)
			if err := os.WriteFile(filepath.Join(f.intent.Workdir, "gate"), []byte(gate+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if gate == "degraded" {
				if candidate, err := o.Observe(ownershipContext(t), f.intent.RunID); err == nil || candidate != nil {
					if candidate != nil {
						candidate.Close()
					}
					t.Fatal("normal adoption admitted degraded")
				}
				if err := o.Terminate(ownershipContext(t), run, f.intent.OperationID); !errors.Is(err, ErrOwnershipUnknown) {
					t.Fatal("normal termination unexpectedly widened", err)
				}
				f.assertRunning(t)
			}
			// Mutation of a callback's detached argv cannot rewrite the final proof.
			i.change = "mutate-argv"
			before := ownershipTree(t, f.paths.State)
			if err := o.TerminateCandidate(ownershipContext(t), f.intent.RunID, f.intent.OperationID); err != nil {
				t.Fatal(err)
			}
			if i.terminationCalls != 1 {
				t.Fatal("unexpected inspection retry", i.terminationCalls)
			}
			if err := run.process.WaitExited(ownershipContext(t)); err != nil {
				t.Fatal("preexisting original handle did not exit", err)
			}
			select {
			case err := <-f.exited:
				f.waited = true
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal("missing child signal exit", err)
				}
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
					t.Fatal("not SIGTERM", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("child Wait incomplete")
			}
			if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
				t.Fatal("candidate termination wrote authority")
			}
			t.Logf("synthetic_gate=%s original_pid=%d original_pidfd_exit=true child_Wait_SIGTERM=true", gate, run.Identity().PID)
		})
	}
}

func TestTerminateCandidateRefusesWithoutSignaling(t *testing.T) {
	f, o, _, i := candidateTerminationFixture(t)
	refuse := func(t *testing.T, owner *Ownership, ctx context.Context, op config.ID, cause error) {
		t.Helper()
		before := ownershipTree(t, f.paths.State)
		err := owner.TerminateCandidate(ctx, f.intent.RunID, op)
		if !errors.Is(err, ErrOwnershipUnknown) || cause != nil && !errors.Is(err, cause) {
			t.Fatal("wrong refusal", err, cause)
		}
		f.assertRunning(t)
		if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
			t.Fatal("refusal changed authority")
		}
	}
	for _, change := range []string{"config", "run", "generation", "binary", "gate", "error"} {
		t.Run(change, func(t *testing.T) {
			i.change = change
			defer func() { i.change = "" }()
			refuse(t, o, ownershipContext(t), f.intent.OperationID, nil)
		})
	}
	t.Run("no-inspector-capability", func(t *testing.T) {
		refuse(t, ownershipManager(t, f.store, &ownershipInspector{}), ownershipContext(t), f.intent.OperationID, nil)
	})
	t.Run("missing-registration", func(t *testing.T) { refuse(t, o, ownershipContext(t), "missing", os.ErrNotExist) })
	t.Run("foreign-control-operation", func(t *testing.T) {
		run, _, err := o.Register(ownershipContext(t), f.intent.RunID, "another-operation", true)
		if err != nil {
			t.Fatal(err)
		}
		run.Close()
		refuse(t, o, ownershipContext(t), "another-operation", nil)
	})
	t.Run("readonly", func(t *testing.T) {
		r, err := config.OpenStore(f.paths, false)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		refuse(t, ownershipManager(t, r, i), ownershipContext(t), f.intent.OperationID, config.ErrReadOnly)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		refuse(t, o, ctx, f.intent.OperationID, context.Canceled)
	})
	t.Run("nil-context", func(t *testing.T) { refuse(t, o, nil, f.intent.OperationID, nil) })
	t.Run("nil-owner", func(t *testing.T) { refuse(t, nil, ownershipContext(t), f.intent.OperationID, nil) })
	name := filepath.Join(f.runPath, "registration-"+ownershipDigest([]byte(f.intent.OperationID))+".json")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("registration-not-closed", func(t *testing.T) {
		var reg config.RunRegistration
		if err := json.Unmarshal(original, &reg); err != nil {
			t.Fatal(err)
		}
		reg.Gate = "open"
		raw, _ := json.Marshal(reg)
		raw = append(raw, '\n')
		if err := os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(name, original, 0600); err != nil {
				t.Error(err)
			}
		}()
		refuse(t, o, ownershipContext(t), f.intent.OperationID, nil)
	})
	t.Run("valid-registration-changed-during-inspection", func(t *testing.T) {
		var after map[string]string
		i.after = func() error {
			var reg config.RunRegistration
			if err := json.Unmarshal(original, &reg); err != nil {
				return err
			}
			reg.Gate = "open"
			raw, _ := json.Marshal(reg)
			raw = append(raw, '\n')
			if err := os.WriteFile(name, raw, 0600); err != nil {
				return err
			}
			actual, sha, err := f.store.ReadControlRegistration(f.intent.RunID, f.intent.OperationID)
			if err != nil || actual != reg || sha != ownershipDigest(raw) {
				t.Fatal("changed valid registration was not read back", err)
			}
			after = ownershipTree(t, f.paths.State)
			return nil
		}
		defer func() {
			i.after = nil
			if err := os.WriteFile(name, original, 0600); err != nil {
				t.Error(err)
			}
		}()
		if err := o.TerminateCandidate(ownershipContext(t), f.intent.RunID, f.intent.OperationID); !errors.Is(err, ErrOwnershipUnknown) {
			t.Fatal(err)
		}
		f.assertRunning(t)
		if after == nil || !reflect.DeepEqual(after, ownershipTree(t, f.paths.State)) {
			t.Fatal("did not stop after changed registration")
		}
	})
	t.Run("writer-closed-during-inspection", func(t *testing.T) {
		i.after = func() error { return f.store.Close() }
		defer func() { i.after = nil }()
		refuse(t, o, ownershipContext(t), f.intent.OperationID, config.ErrClosed)
	})
}
