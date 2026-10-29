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
	"hoopa/internal/platform"
)

type terminateInspector func(context.Context, *net.TCPConn, config.RunIntent) (ControllerObservation, error)

func (f terminateInspector) Inspect(ctx context.Context, c *net.TCPConn, i config.RunIntent) (ControllerObservation, error) {
	return f(ctx, c, i)
}

const terminateOperation config.ID = "stop-operation"

func terminateFixture(t *testing.T) (*ownershipFixture, *Ownership, *OwnedRun, *ownershipInspector) {
	t.Helper()
	f := startOwnershipFixture(t, "")
	inspector := &ownershipInspector{}
	owner := ownershipManager(t, f.store, inspector)
	run, _, e := owner.Register(ownershipContext(t), f.intent.RunID, terminateOperation, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { run.Close() })
	return f, owner, run, inspector
}
func terminateRefused(t *testing.T, f *ownershipFixture, owner *Ownership, ctx context.Context, run *OwnedRun, operation config.ID, cause error) {
	t.Helper()
	before := ownershipTree(t, f.paths.State)
	e := owner.Terminate(ctx, run, operation)
	if !errors.Is(e, ErrOwnershipUnknown) || (cause != nil && !errors.Is(e, cause)) {
		t.Fatalf("refusal did not retain sentinel/cause: %v", e)
	}
	f.assertRunning(t)
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("termination refusal wrote Store bytes/mode/mtime")
	}
}
func TestTerminateOriginalPIDFDAndChildWait(t *testing.T) {
	f, owner, run, inspector := terminateFixture(t)
	pid := run.Identity().PID
	before := ownershipTree(t, f.paths.State)
	calls := inspector.calls.Load()
	if e := owner.Terminate(ownershipContext(t), run, terminateOperation); e != nil {
		t.Fatal(e)
	}
	if inspector.calls.Load() != calls+1 {
		t.Fatal("did not perform fresh connected inspection")
	}
	// Success is the original pidfd's exit observation. Reaping is a separate
	// launcher responsibility, independently observed here from the original child.
	if e := run.process.WaitExited(ownershipContext(t)); e != nil {
		t.Fatal("original pidfd not exited", e)
	}
	select {
	case e := <-f.exited:
		f.waited = true
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal("expected signal exit, not successful child completion", e)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
			t.Fatal("not original child TERM", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child Wait did not complete")
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("termination wrote Store")
	}
	t.Logf("original pid=%d pidfd_exit_observed=true child_Wait_signaled_SIGTERM=true", pid)
}
func TestTerminateRefusalsKeepProcessAlive(t *testing.T) {
	f, owner, run, inspector := terminateFixture(t)
	t.Run("missing-registration", func(t *testing.T) {
		calls := inspector.calls.Load()
		terminateRefused(t, f, owner, ownershipContext(t), run, "missing-operation", os.ErrNotExist)
		if inspector.calls.Load() != calls {
			t.Fatal("missing registration reached inspector")
		}
	})
	t.Run("wrong-run", func(t *testing.T) {
		copy := *run
		copy.intent.RunID = "other-run"
		terminateRefused(t, f, owner, ownershipContext(t), &copy, terminateOperation, nil)
	})
	t.Run("wrong-birth", func(t *testing.T) {
		copy := *run
		copy.birth.StartTicks++
		terminateRefused(t, f, owner, ownershipContext(t), &copy, terminateOperation, config.ErrIntegrity)
	})
	t.Run("wrong-argv", func(t *testing.T) {
		copy := *run
		copy.intent.Arguments = append(append([]string{}, run.intent.Arguments...), "extra")
		terminateRefused(t, f, owner, ownershipContext(t), &copy, terminateOperation, platform.ErrProcessUnverified)
	})
	t.Run("readonly", func(t *testing.T) {
		reader, e := config.OpenStore(f.paths, false)
		if e != nil {
			t.Fatal(e)
		}
		defer reader.Close()
		ro := ownershipManager(t, reader, inspector)
		terminateRefused(t, f, ro, ownershipContext(t), run, terminateOperation, config.ErrReadOnly)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		terminateRefused(t, f, owner, ctx, run, terminateOperation, context.Canceled)
	})
	t.Run("nil-context", func(t *testing.T) { terminateRefused(t, f, owner, nil, run, terminateOperation, nil) })
	t.Run("nil-owner", func(t *testing.T) { terminateRefused(t, f, nil, ownershipContext(t), run, terminateOperation, nil) })
	t.Run("nil-run", func(t *testing.T) { terminateRefused(t, f, owner, ownershipContext(t), nil, terminateOperation, nil) })
	t.Run("nil-process", func(t *testing.T) {
		copy := *run
		copy.process = nil
		terminateRefused(t, f, owner, ownershipContext(t), &copy, terminateOperation, nil)
	})
	t.Run("fresh-controller-mismatch", func(t *testing.T) {
		inspector.change = "config"
		defer func() { inspector.change = "" }()
		calls := inspector.calls.Load()
		terminateRefused(t, f, owner, ownershipContext(t), run, terminateOperation, nil)
		if inspector.calls.Load() != calls+1 {
			t.Fatal("trusted old registration without fresh inspection")
		}
	})
	t.Run("draining-unsupported", func(t *testing.T) {
		other := ownershipManager(t, f.store, terminateInspector(func(ctx context.Context, c *net.TCPConn, i config.RunIntent) (ControllerObservation, error) {
			obs, e := inspector.Inspect(ctx, c, i)
			obs.Gate = "draining"
			return obs, e
		}))
		terminateRefused(t, f, other, ownershipContext(t), run, terminateOperation, nil)
	})
	t.Run("inspector-deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		deadline, _ := ctx.Deadline()
		called := false
		other := ownershipManager(t, f.store, terminateInspector(func(inner context.Context, c *net.TCPConn, i config.RunIntent) (ControllerObservation, error) {
			called = true
			actual, ok := inner.Deadline()
			if !ok || !actual.Equal(deadline) {
				return ControllerObservation{}, errors.New("earlier deadline not preserved")
			}
			<-inner.Done()
			return ControllerObservation{}, inner.Err()
		}))
		start := time.Now()
		terminateRefused(t, f, other, ctx, run, terminateOperation, context.DeadlineExceeded)
		if !called || time.Since(start) > 2*time.Second {
			t.Fatal("deadline not bounded or inspector not called")
		}
	})
	name := filepath.Join(f.runPath, "registration-"+ownershipDigest([]byte(terminateOperation))+".json")
	original, e := os.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"corrupt", "foreign-operation"} {
		t.Run(kind, func(t *testing.T) {
			raw := []byte(`{"corrupt":true}`)
			if kind == "foreign-operation" {
				var reg config.RunRegistration
				if e := json.Unmarshal(original, &reg); e != nil {
					t.Fatal(e)
				}
				reg.OperationID = "foreign-operation"
				raw, e = json.Marshal(reg)
				if e != nil {
					t.Fatal(e)
				}
				raw = append(raw, '\n')
			}
			if e := os.WriteFile(name, raw, 0600); e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := os.WriteFile(name, original, 0600); e != nil {
					t.Error(e)
				}
			}()
			terminateRefused(t, f, owner, ownershipContext(t), run, terminateOperation, nil)
		})
	}
	t.Run("closed-handle", func(t *testing.T) {
		if e := run.Close(); e != nil {
			t.Fatal(e)
		}
		terminateRefused(t, f, owner, ownershipContext(t), run, terminateOperation, platform.ErrProcessUnverified)
	})
	t.Run("closed-store", func(t *testing.T) {
		if e := f.store.Close(); e != nil {
			t.Fatal(e)
		}
		terminateRefused(t, f, owner, ownershipContext(t), run, terminateOperation, config.ErrClosed)
	})
}
func TestTerminateRegistrationRecheckedBeforeSignal(t *testing.T) {
	f, _, run, inspector := terminateFixture(t)
	name := filepath.Join(f.runPath, "registration-"+ownershipDigest([]byte(terminateOperation))+".json")
	original, e := os.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []string{"registration", "corrupt-extra-newline", "writer-closed"} {
		t.Run(change, func(t *testing.T) {
			var afterMutation map[string]string
			called := false
			validChangeConfirmed := false
			owner := ownershipManager(t, f.store, terminateInspector(func(ctx context.Context, c *net.TCPConn, i config.RunIntent) (ControllerObservation, error) {
				obs, e := inspector.Inspect(ctx, c, i)
				if e != nil {
					return obs, e
				}
				called = true
				switch change {
				case "writer-closed":
					e = f.store.Close()
				case "corrupt-extra-newline":
					e = os.WriteFile(name, append(append([]byte{}, original...), '\n'), 0600)
				default:
					var reg config.RunRegistration
					if e = json.Unmarshal(original, &reg); e == nil {
						reg.Gate = "open"
						var raw []byte
						raw, e = json.Marshal(reg)
						if e == nil {
							raw = append(raw, '\n')
							e = os.WriteFile(name, raw, 0600)
						}
						if e == nil {
							actual, sha, readErr := f.store.ReadControlRegistration(f.intent.RunID, terminateOperation)
							validChangeConfirmed = readErr == nil && actual == reg && sha == ownershipDigest(raw)
							if !validChangeConfirmed {
								e = errors.New("controlled changed registration was not readable")
							}
						}
					}
				}
				afterMutation = ownershipTree(t, f.paths.State)
				return obs, e
			}))
			cause := error(nil)
			if change == "writer-closed" {
				cause = config.ErrClosed
			}
			e := owner.Terminate(ownershipContext(t), run, terminateOperation)
			if !called || (change == "registration" && !validChangeConfirmed) || !errors.Is(e, ErrOwnershipUnknown) || (cause != nil && !errors.Is(e, cause)) {
				t.Fatal("second registration/writer read did not refuse", e)
			}
			f.assertRunning(t)
			if !reflect.DeepEqual(afterMutation, ownershipTree(t, f.paths.State)) {
				t.Fatal("termination wrote after controlled fixture mutation")
			}
			if change != "writer-closed" {
				if e := os.WriteFile(name, original, 0600); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
