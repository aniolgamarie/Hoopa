//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/platform"
)

func TestLifecycleExitGuards(t *testing.T) {
	for _, test := range []struct {
		name string
		run  *OwnedRun
		ctx  context.Context
	}{
		{"nil-receiver", nil, context.Background()},
		{"missing-process", &OwnedRun{}, context.Background()},
		{"nil-context", &OwnedRun{}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if e := test.run.WaitExited(test.ctx); !errors.Is(e, ErrOwnershipUnknown) {
				t.Fatal("invalid exit observer", e)
			}
		})
	}
}
func TestLifecycleExitOriginalObserveAndRegister(t *testing.T) {
	f := startOwnershipFixture(t, "")
	inspector := &ownershipInspector{}
	owner := ownershipManager(t, f.store, inspector)
	observed, e := owner.Observe(ownershipContext(t), f.intent.RunID)
	if e != nil {
		t.Fatal(e)
	}
	defer observed.Close()
	registered, _, e := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
	if e != nil {
		t.Fatal(e)
	}
	defer registered.Close()
	identity := observed.Identity()
	if identity != registered.Identity() || identity.PID != f.cmd.Process.Pid {
		t.Fatal("observations did not retain the original task-owned child")
	}
	calls := inspector.calls.Load()
	// Exit observation must not require a live writer/registration reader, nor
	// use serialized argv as authority after the process has gone away.
	if e := f.store.Close(); e != nil {
		t.Fatal(e)
	}
	observed.intent.Arguments = []string{"deliberately irrelevant historical argv"}
	before := ownershipTree(t, f.paths.State)
	for _, run := range []*OwnedRun{observed, registered} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		e := run.WaitExited(ctx)
		cancel()
		if !errors.Is(e, context.DeadlineExceeded) || !errors.Is(e, ErrOwnershipUnknown) {
			t.Fatal("live original did not retain deadline", e)
		}
		f.assertRunning(t)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := observed.WaitExited(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation cause lost", e)
	}
	if e := registered.WaitExited(nil); !errors.Is(e, ErrOwnershipUnknown) {
		t.Fatal("nil context with live handle", e)
	}
	f.assertRunning(t)
	// This signal is an external TEST action on our own directly spawned child;
	// the method under test never signals or invokes runtime termination policy.
	if e := f.cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	if e := observed.WaitExited(ownershipContext(t)); e != nil {
		t.Fatal("original pidfd exit", e)
	}
	select {
	case e := <-f.exited:
		f.waited = true
		var child *exec.ExitError
		if !errors.As(e, &child) {
			t.Fatal("child Wait did not report external signal", e)
		}
		status, ok := child.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
			t.Fatal("child Wait signal", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child Wait timeout")
	}
	// Repeat after independent child reaping: no live argv or controller remains
	// available. Both separately acquired original pidfds still prove the fact.
	for i := 0; i < 3; i++ {
		for _, run := range []*OwnedRun{observed, registered} {
			if e := run.WaitExited(ownershipContext(t)); e != nil {
				t.Fatal("historical original exit not repeatable", e)
			}
		}
	}
	if inspector.calls.Load() != calls || !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("exit observation consulted controller or wrote Store")
	}
	t.Logf("actual fixture pid=%d boot=%s start=%d original Observe/Register pidfds observed external SIGTERM exit; independent child Wait signal15; repeated after reaping; no writer/controller consultation", identity.PID, identity.BootID, identity.StartTicks)
}

type lifecycleEnteredContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *lifecycleEnteredContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Err()
}
func TestLifecycleExitConcurrentCloseDoesNotSignal(t *testing.T) {
	f := startOwnershipFixture(t, "")
	inspector := &ownershipInspector{}
	owner := ownershipManager(t, f.store, inspector)
	run, e := owner.Observe(ownershipContext(t), f.intent.RunID)
	if e != nil {
		t.Fatal(e)
	}
	defer run.Close()
	before := ownershipTree(t, f.paths.State)
	calls := inspector.calls.Load()
	base, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx := &lifecycleEnteredContext{Context: base, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- run.WaitExited(ctx) }()
	select {
	case <-ctx.entered:
	case <-time.After(time.Second):
		t.Fatal("wait observer did not enter")
	}
	if e := run.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, ErrOwnershipUnknown) || !errors.Is(e, platform.ErrProcessUnverified) {
			t.Fatal("closed handle refusal", e)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt bounded poll")
	}
	f.assertRunning(t)
	if e := run.WaitExited(ownershipContext(t)); !errors.Is(e, platform.ErrProcessUnverified) {
		t.Fatal("closed observer reused pidfd", e)
	}
	if inspector.calls.Load() != calls || !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("closing exit observer touched controller/Store")
	}
	// Graceful fixture-only EOF shutdown is separate from the closed observer.
	if e := f.stdin.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-f.exited:
		f.waited = true
		if e != nil {
			t.Fatal("fixture EOF child Wait", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fixture child Wait timeout")
	}
	t.Logf("actual fixture pid=%d concurrent observer Close returned unverified; child stayed live until external EOF and Wait exit0", f.cmd.Process.Pid)
}
