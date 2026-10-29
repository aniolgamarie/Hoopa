//go:build linux && amd64

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

func launchExitDispatch(t *testing.T, nonzero bool) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	}
	_, e := ClaimLauncher(context.Background(), os.NewFile(3, "birth"), os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile("exit-ready", []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, e = os.Stat("exit-release"); e == nil {
			if nonzero {
				os.Exit(17)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("parent did not release child")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestLaunchExitDispatchZero(t *testing.T)    { launchExitDispatch(t, false) }
func TestLaunchExitDispatchNonzero(t *testing.T) { launchExitDispatch(t, true) }
func launchExitFixture(t *testing.T, helper string) (*ownershipFixture, *LaunchChild, config.RunIntent) {
	t.Helper()
	f, input := prepareLaunchFixture(t)
	bundle, e := f.store.ReadBundle(input.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(input.Workdir, "config.json"), bundle.Files["core.yaml"], 0600); e != nil {
		t.Fatal(e)
	}
	prepared, e := PrepareLaunch(f.store, input)
	if e != nil {
		t.Fatal(e)
	}
	child, e := StartBasicLauncher(context.Background(), prepared, []string{"-test.run=^" + helper + "$", "-test.timeout=12s"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(input.Workdir, "exit-release"), nil, 0600)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		e := child.Wait(ctx)
		var exit *exec.ExitError
		if e != nil && !errors.As(e, &exit) {
			t.Error("child fixture cleanup", e)
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e = os.Stat(filepath.Join(input.Workdir, "exit-ready")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual child birth/readiness timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	captured := prepared.Intent()
	// Start must own its detached input even if another package-local holder
	// changes the prepared capsule after transfer; the protected record stays.
	prepared.intent.Arguments[0] = "/changed-prepared-holder"
	return f, child, captured
}
func launchExitRelease(t *testing.T, child *LaunchChild, intent config.RunIntent) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(intent.Workdir, "exit-release"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := child.Wait(ctx)
	var exit *exec.ExitError
	if e != nil && !errors.As(e, &exit) {
		t.Fatal(e)
	}
}
func TestLaunchExitActualTerminalProofs(t *testing.T) {
	for _, mode := range []string{"zero", "nonzero", "signal"} {
		t.Run(mode, func(t *testing.T) {
			helper := "TestLaunchExitDispatchZero"
			if mode == "nonzero" {
				helper = "TestLaunchExitDispatchNonzero"
			}
			f, child, intent := launchExitFixture(t, helper)
			if mode == "signal" {
				if e := child.cmd.Process.Signal(syscall.SIGTERM); e != nil {
					t.Fatal(e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if e := child.Wait(ctx); e == nil {
					t.Fatal("signal did not exit fixture")
				}
			} else {
				launchExitRelease(t, child, intent)
			}
			proof, e := child.ExitedOriginal(context.Background(), f.store, intent.RunID, intent.OperationID)
			if e != nil {
				t.Fatal(e)
			}
			stored, sha, e := f.store.ReadRunIntent(intent.RunID)
			if e != nil {
				t.Fatal(e)
			}
			birth, bsha, e := f.store.ReadRunBirth(intent.RunID)
			if e != nil {
				t.Fatal(e)
			}
			if proof.Match(stored, sha, birth, bsha) != nil || proof.PID != child.PID() || proof.Version != 1 || proof.Kind != "direct_child_wait" {
				t.Fatal("proof did not bind actual originals")
			}
			status := syscall.WaitStatus(proof.WaitStatus)
			if mode == "signal" {
				if !status.Signaled() || status.Signal() != syscall.SIGTERM {
					t.Fatal("raw signal wait status lost")
				}
			} else {
				want := 0
				if mode == "nonzero" {
					want = 17
				}
				if !status.Exited() || status.ExitStatus() != want {
					t.Fatal("raw exit status lost")
				}
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					p, e := child.ExitedOriginal(context.Background(), f.store, intent.RunID, intent.OperationID)
					if e != nil || p != proof {
						t.Error("publication not repeatable", e)
					}
				}()
			}
			wg.Wait()
			t.Logf("actual direct child PID=%d terminal raw status=%d; fixture test image, not core ownership", proof.PID, proof.WaitStatus)
		})
	}
}
func TestLaunchExitCancellationAndRefusals(t *testing.T) {
	f, child, intent := launchExitFixture(t, "TestLaunchExitDispatchZero")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p, e := child.ExitedOriginal(ctx, f.store, intent.RunID, intent.OperationID)
	if !errors.Is(e, context.DeadlineExceeded) || !errors.Is(e, ErrOwnershipUnknown) || p != (LaunchExitProof{}) {
		t.Fatal("non-exited child admitted", e)
	}
	// Cancellation did not send a signal: the actual child can still be normally released.
	launchExitRelease(t, child, intent)
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	p, e = child.ExitedOriginal(canceled, f.store, intent.RunID, intent.OperationID)
	if !errors.Is(e, context.Canceled) || p != (LaunchExitProof{}) {
		t.Fatal("already canceled chose done success", e)
	}
	cases := map[string]func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID){
		"nil-child": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return nil, context.Background(), f.store, intent.RunID, intent.OperationID
		},
		"zero-child": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return &LaunchChild{}, context.Background(), f.store, intent.RunID, intent.OperationID
		},
		"nil-context": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return child, nil, f.store, intent.RunID, intent.OperationID
		},
		"nil-store": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return child, context.Background(), nil, intent.RunID, intent.OperationID
		},
		"wrong-run": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return child, context.Background(), f.store, "other", intent.OperationID
		},
		"wrong-operation": func() (*LaunchChild, context.Context, *config.Store, config.RunID, config.ID) {
			return child, context.Background(), f.store, intent.RunID, "other"
		},
	}
	for name, makeCase := range cases {
		t.Run(name, func(t *testing.T) {
			c, ctx, store, id, op := makeCase()
			p, e := c.ExitedOriginal(ctx, store, id, op)
			if !errors.Is(e, ErrOwnershipUnknown) || p != (LaunchExitProof{}) {
				t.Fatal("invalid inputs admitted", e)
			}
		})
	}
	for _, cause := range []error{syscall.ECHILD, io.ErrUnexpectedEOF, context.Canceled, &exec.ExitError{}} {
		copy := *child
		done := make(chan struct{})
		close(done)
		copy.exit = &launchExit{done: done, err: cause}
		p, e := copy.ExitedOriginal(context.Background(), f.store, intent.RunID, intent.OperationID)
		if e != ErrOwnershipUnknown || p != (LaunchExitProof{}) {
			t.Fatal("non-original Wait error admitted", e)
		}
	}
	for _, kind := range []string{"no-state", "different-pid"} {
		copy := *child
		command := *child.cmd
		copy.cmd = &command
		if kind == "no-state" {
			command.ProcessState = nil
		} else {
			command.Process = &os.Process{Pid: child.PID() + 1}
		}
		p, e := copy.ExitedOriginal(context.Background(), f.store, intent.RunID, intent.OperationID)
		if e != ErrOwnershipUnknown || p != (LaunchExitProof{}) {
			t.Fatal("non-original ProcessState admitted", kind, e)
		}
	}

}
func TestLaunchExitOriginalRecordRefusals(t *testing.T) {
	for _, kind := range []string{"missing-birth", "changed-birth-pid", "changed-birth-intent", "changed-intent", "closed-store"} {
		t.Run(kind, func(t *testing.T) {
			f, child, intent := launchExitFixture(t, "TestLaunchExitDispatchZero")
			launchExitRelease(t, child, intent)
			run := filepath.Join(f.paths.State, "runs", ownershipDigest([]byte(intent.RunID)))
			switch kind {
			case "missing-birth":
				if e := os.Remove(filepath.Join(run, "birth.json")); e != nil {
					t.Fatal(e)
				}
			case "changed-birth-pid", "changed-birth-intent":
				raw, e := os.ReadFile(filepath.Join(run, "birth.json"))
				if e != nil {
					t.Fatal(e)
				}
				var birth config.RunBirth
				if json.Unmarshal(raw, &birth) != nil {
					t.Fatal("fixture")
				}
				if kind == "changed-birth-pid" {
					birth.PID++
				} else {
					birth.IntentSHA256 = strings.Repeat("f", 64)
				}
				raw, _ = json.Marshal(birth)
				if e = os.WriteFile(filepath.Join(run, "birth.json"), append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
			case "changed-intent":
				raw, e := os.ReadFile(filepath.Join(run, "intent.json"))
				if e != nil {
					t.Fatal(e)
				}
				var changed config.RunIntent
				if json.Unmarshal(raw, &changed) != nil {
					t.Fatal("fixture")
				}
				changed.Arguments = append(changed.Arguments, "changed-private-arg")
				raw, _ = json.Marshal(changed)
				if e = os.WriteFile(filepath.Join(run, "intent.json"), append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
			case "closed-store":
				if e := f.store.Close(); e != nil {
					t.Fatal(e)
				}
			}
			p, e := child.ExitedOriginal(context.Background(), f.store, intent.RunID, intent.OperationID)
			if e != ErrOwnershipUnknown || !reflect.DeepEqual(p, LaunchExitProof{}) {
				t.Fatal("modified originals admitted", e)
			}
		})
	}
}
func TestLaunchExitProofMatchAndTerminalShape(t *testing.T) {
	_, intent := prepareLaunchFixture(t)
	intent.RunID = "proof-run"
	intent.NonceSHA256 = strings.Repeat("a", 64)
	isha := strings.Repeat("b", 64)
	bsha := strings.Repeat("c", 64)
	birth := config.RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: isha, PID: 123, UID: 1002, BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
	proof := LaunchExitProof{Version: 1, Kind: "direct_child_wait", RunID: intent.RunID, OperationID: intent.OperationID, IntentSHA256: isha, BirthSHA256: bsha, PID: 123, WaitStatus: 0}
	for _, status := range []uint32{0, 17 << 8, 255 << 8, 1, 15, 64, 128 | 11} {
		p := proof
		p.WaitStatus = status
		if p.Match(intent, isha, birth, bsha) != nil {
			t.Fatal("valid terminal status rejected", status)
		}
	}
	for _, status := range []uint32{128, 65, 127, 0xffff, 0x137f, 0x10000, 15 | (1 << 8)} {
		p := proof
		p.WaitStatus = status
		if p.Match(intent, isha, birth, bsha) != ErrOwnershipUnknown {
			t.Fatal("nonterminal/illegal status admitted", status)
		}
	}
	edits := map[string]func(*LaunchExitProof){"version": func(p *LaunchExitProof) { p.Version = 2 }, "kind": func(p *LaunchExitProof) { p.Kind = "observed_pid" }, "run": func(p *LaunchExitProof) { p.RunID = "other" }, "operation": func(p *LaunchExitProof) { p.OperationID = "other" }, "intent-sha": func(p *LaunchExitProof) { p.IntentSHA256 = strings.Repeat("d", 64) }, "birth-sha": func(p *LaunchExitProof) { p.BirthSHA256 = strings.Repeat("d", 64) }, "uppercase": func(p *LaunchExitProof) { p.BirthSHA256 = strings.Repeat("A", 64) }, "pid": func(p *LaunchExitProof) { p.PID = 0 }}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			p := proof
			edit(&p)
			if p.Match(intent, isha, birth, bsha) != ErrOwnershipUnknown {
				t.Fatal("proof tamper admitted")
			}
		})
	}
	changed := birth
	changed.StartTicks++
	if proof.Match(intent, isha, changed, strings.Repeat("d", 64)) != ErrOwnershipUnknown {
		t.Fatal("replacement birth bytes escaped original SHA binding")
	}
	badIntent := intent
	badIntent.Arguments = nil
	if proof.Match(badIntent, isha, birth, bsha) != ErrOwnershipUnknown {
		t.Fatal("invalid intent bypassed Match")
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if text := fmt.Sprintf(format, proof); strings.Contains(text, intent.Workdir) {
			t.Fatal("proof leaked original material")
		}
	}
}
