//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
	"hoopa/internal/service"
)

// This scene observes actual product restart budgeting; it does not implement
// the unfinished productHarness or infer side effects from final journal files.
func TestP1TracedRestartBudget(t *testing.T) {
	var prior int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	})
	f := newP1TracedCore(t, "restart-budget")
	seed, _ := f.apply("app-first", "restart-seed")
	f.archiveTrace("seed", seed)
	seedRun, _ := f.pin() // Keep the original handle even after setup Stop.
	manager := f.launch("manager", []string{"service", "--restart-once"}, nil, nil)
	p1RestartReady(t, f, manager)
	f.call("restart-stop-seed", "runtime.stop", map[string]any{"expected_revision": f.active().Revision, "accept_disruption": true})
	f.call("restart-explicit-start", "runtime.start", map[string]any{"expected_revision": f.active().Revision, "control_only": false})
	before := f.active()
	original, first := f.pin()
	if original == seedRun {
		t.Fatal("fresh start reused seed run")
	}
	p1RestartStatus(t, f, "watching_original", func(v control.ServiceStatus) bool {
		return v.Core.State == "verified-running" && v.Core.RunID != nil && *v.Core.RunID == original
	})
	p1RestartEcho(t, f, "original")
	p1RestartKill(t, f, "original", original, first)
	p1RestartStatus(t, f, "budget_used_watching_successor", func(v control.ServiceStatus) bool {
		return v.Core.State == "verified-running" && v.Core.RunID != nil && *v.Core.RunID != original
	})
	successor, second := f.pin()
	firstIdentity, secondIdentity := first.Identity(), second.Identity()
	if successor == original || secondIdentity.PID == firstIdentity.PID || secondIdentity.StartTicks == 0 || secondIdentity.NonceSHA256 == firstIdentity.NonceSHA256 {
		t.Fatal("restart reused original identity/run/nonce")
	}
	current := f.active()
	if current.Revision != before.Revision || !reflect.DeepEqual(current.Generation, before.Generation) || current.ManifestSHA256 != before.ManifestSHA256 || current.DesiredRuntime != "running" || reflect.DeepEqual(current.OperationID, before.OperationID) {
		t.Fatal("restart revision/generation/publication mismatch")
	}
	p1RestartEcho(t, f, "successor")
	p1RestartKill(t, f, "successor", successor, second)
	stopped := p1RestartStatus(t, f, "inactive", func(v control.ServiceStatus) bool {
		return v.Core.State == "verified-stopped" && v.Core.Reason == "observed_original_exit" && v.Core.RunID != nil && *v.Core.RunID == successor
	})
	if stopped.Core.PID != nil || stopped.Core.StartTicks != nil || stopped.Core.BootID != nil {
		t.Fatal("exited core status invented live or zero identity")
	}
	runs, err := f.store.ListRunIDs()
	must(t, err)
	if len(runs) != 3 {
		t.Fatal("expected seed, fresh start, and exactly one successor")
	}
	tree := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "budget-exhausted")
	time.Sleep(350 * time.Millisecond)
	p1RestartStatus(t, f, "inactive", func(v control.ServiceStatus) bool {
		return v.Core.State == "verified-stopped" && v.Core.RunID != nil && *v.Core.RunID == successor
	})
	after := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-readonly")
	if !reflect.DeepEqual(tree, after) || !reflect.DeepEqual(current, f.active()) {
		t.Fatal("exhausted budget or readonly status changed durable authority")
	}
	f.call("restart-service-stop", "service.stop", map[string]any{"expected_revision": current.Revision})
	ctx, cancel := p1LongContext()
	defer cancel()
	must(t, manager.Wait(ctx))
	records := f.archiveTrace("manager", manager)
	launches := 0
	launchSequence := map[config.RunID]uint64{}
	for _, r := range records {
		if r.Event.Component == "runtime" && r.Event.Point == "launcher-started" {
			launches++
			launchSequence[config.RunID(r.Event.RunID)] = r.Sequence
		}
	}
	if launches != 2 {
		t.Fatal("manager did not launch exactly fresh start and one successor")
	}
	exits := map[config.RunID]int{}
	exitSequence := map[config.RunID]uint64{}
	for _, r := range records {
		if r.Event.Component != "runtime" || r.Event.Point != "source-exit-observed" {
			continue
		}
		id := config.RunID(r.Event.RunID)
		expected := firstIdentity
		if id == successor {
			expected = secondIdentity
		} else if id != original {
			t.Fatal("source exit names an unwatched run")
		}
		var detail struct {
			Identity platform.ProcessIdentity `json:"identity"`
		}
		wrapper, unwrapErr := p1TraceUnwrap(r.Event.Data)
		must(t, unwrapErr)
		if wrapper.Injectable {
			t.Fatal("source exit was injectable")
		}
		must(t, json.Unmarshal(wrapper.Detail, &detail))
		if r.Event.OperationID != "" || r.Event.RequestID != "" || detail.Identity != expected {
			t.Fatal("source exit identity/binding mismatch")
		}
		exits[id]++
		exitSequence[id] = r.Sequence
	}
	if len(exits) != 2 || exits[original] != 1 || exits[successor] != 1 || launchSequence[original] == 0 || launchSequence[successor] <= exitSequence[original] || exitSequence[successor] <= launchSequence[successor] {
		t.Fatal("missing or repeated exact watched source exit")
	}
	_, err = p1WriteOriginal(filepath.Join(f.artifacts, "result.json"), p1JSON(t, map[string]any{"seed_run": seedRun, "original_run": original, "successor_run": successor, "first_identity": firstIdentity, "second_identity": secondIdentity, "revision": current.Revision, "generation": current.Generation, "manager_launches": launches, "source_exit_observations": exits, "run_count": len(runs), "readonly_exhausted_tree_unchanged": true, "stopped_status": stopped}))
	must(t, err)
}
func p1RestartReady(t *testing.T, f *p1TracedCore, p *p1TracedProcess) {
	t.Helper()
	ctx, cancel := p1LongContext()
	defer cancel()
	for {
		conn, err := service.DialEndpoint(ctx, f.cli.Paths())
		if err == nil {
			raw, e := conn.SyscallConn()
			must(t, e)
			var peer *syscall.Ucred
			var pe error
			must(t, raw.Control(func(fd uintptr) { peer, pe = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) }))
			must(t, pe)
			must(t, conn.Close())
			if int(peer.Pid) != p.PID() || int(peer.Uid) != os.Getuid() {
				t.Fatal("restart endpoint has wrong peer")
			}
			return
		}
		select {
		case <-p.waitDone:
			t.Fatal("restart service exited before readiness")
		case <-ctx.Done():
			t.Fatal("restart readiness timeout")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func p1RestartStatus(t *testing.T, f *p1TracedCore, policy string, check func(control.ServiceStatus) bool) control.ServiceStatus {
	t.Helper()
	ctx, cancel := p1LongContext()
	defer cancel()
	for i := 0; ; i++ {
		r := f.call("restart-status-"+policy+"-"+time.Now().Format("150405.000000000"), "status.get", map[string]any{})
		var envelope struct {
			Data control.ServiceStatus `json:"data"`
		}
		must(t, json.Unmarshal(r.Response, &envelope))
		if envelope.Data.RestartPolicy == policy && check(envelope.Data) {
			return envelope.Data
		}
		select {
		case <-ctx.Done():
			t.Fatal("restart status did not reach expected state")
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func p1RestartEcho(t *testing.T, f *p1TracedCore, label string) {
	t.Helper()
	for _, entry := range []string{"entry-a", "entry-b"} {
		stream, err := f.networks["app-first"].open(p1FiniteContext(t), entry)
		must(t, err)
		receipt, err := stream.exchange(p1FiniteContext(t), 4096)
		must(t, err)
		must(t, stream.Close())
		verifyOriginal(t, receipt.Original)
		_, err = p1WriteOriginal(filepath.Join(f.artifacts, label+"-"+entry+"-echo.json"), p1JSON(t, receipt))
		must(t, err)
	}
}
func p1RestartKill(t *testing.T, f *p1TracedCore, label string, run config.RunID, h *platform.ProcessHandle) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	intent, intentSHA, err := f.store.ReadRunIntent(run)
	must(t, err)
	birth, birthSHA, err := f.store.ReadRunBirth(run)
	must(t, err)
	_, err = p1WriteOriginal(filepath.Join(f.artifacts, label+"-kill-intent.json"), p1JSON(t, map[string]any{"signal": int(syscall.SIGKILL), "run_id": run, "identity": h.Identity(), "run_intent": intent, "intent_sha256": intentSHA, "run_birth": birth, "birth_sha256": birthSHA}))
	must(t, err)
	must(t, h.SignalVerified(ctx, syscall.SIGKILL))
	must(t, h.WaitExited(ctx))
	_, err = p1WriteOriginal(filepath.Join(f.artifacts, label+"-exit.json"), p1JSON(t, map[string]any{"signal": int(syscall.SIGKILL), "run_id": run, "identity": h.Identity(), "signal_verified": true, "original_pidfd_exited": true}))
	must(t, err)
}
