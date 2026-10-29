//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func TestP1TracedApplyRecovery(t *testing.T) {
	var prior int32
	_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if e != 0 {
		t.Fatal(e)
	}
	_, _, e = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if e != 0 {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	})
	for _, mode := range []string{"actual-bind-conflict", "crash-after-active-publication"} {
		t.Run(mode, func(t *testing.T) {
			f := newP1TracedCore(t, mode)
			seed, _ := f.apply("app-first", "apply-seed")
			f.archiveTrace("seed", seed)
			oldRun, oldHandle := f.pin()
			oldActive := f.active()
			held, err := f.networks["app-first"].open(p1FiniteContext(t), "entry-a")
			must(t, err)
			defer held.Close()
			oldReceipt, err := held.exchange(p1FiniteContext(t), 4096)
			must(t, err)
			verifyOriginal(t, oldReceipt.Original)
			var blocker net.Listener
			defer func() {
				if blocker != nil {
					blocker.Close()
				}
			}()
			var observerErr error
			var conflictOriginal originalRef
			bindHits := 0
			worker, raw := f.launchApply("domain-first", "apply-target", func(r p1TraceRecord) p1TraceDecision {
				if r.Event.Component == "runtime" && r.Event.Point == "registration-verified" {
					id := config.RunID(r.Event.RunID)
					if f.handles[id] == nil {
						h, err := f.observe(id)
						if err != nil {
							observerErr = err
							return p1TraceCrashManager
						}
						f.handles[id] = h
					}
				}
				if mode == "actual-bind-conflict" && r.Event.Component == "runtime" && r.Event.Point == "entry-opening" {
					intent, _, err := f.store.ReadRunIntent(config.RunID(r.Event.RunID))
					if err != nil {
						observerErr = err
						return p1TraceCrashManager
					}
					if intent.Generation == f.fixtures["domain-first"].Generation() {
						bindHits++
						// This listener is the actual address-in-use cause. The ACK
						// continues normal Activate; it does not inject a Go error.
						blocker, observerErr = net.Listen("tcp4", f.networks["domain-first"].entries["entry-a"].address)
						if observerErr != nil {
							return p1TraceCrashManager
						}
						conflictOriginal, observerErr = p1WriteOriginal(filepath.Join(f.artifacts, "bind-conflict.json"), []byte(`{"kind":"actual-loopback-listener","before_activate":true}`))
						if observerErr != nil {
							return p1TraceCrashManager
						}
					}
				}
				if mode == "crash-after-active-publication" && r.Event.Component == "store" && r.Event.Point == "active-published" {
					return p1TraceCrashManager
				}
				return p1TraceContinue
			})
			ctx, cancel := p1LongContext()
			defer cancel()
			waitErr := worker.Wait(ctx)
			must(t, observerErr)
			must(t, worker.TraceError())
			var exit *exec.ExitError
			if !errors.As(waitErr, &exit) {
				t.Fatal("faulted Apply must return actual failure or killed process", waitErr)
			}
			records := f.archiveTrace("target", worker)
			op, err := control.ReadReplay(f.cli.Paths(), raw)
			must(t, err)
			must(t, oldHandle.WaitExited(ctx))
			if _, err = held.exchange(p1FiniteContext(t), 4096); err == nil {
				t.Fatal("replacement retained old connection")
			}
			active := f.active()
			var live config.RunID
			if mode == "actual-bind-conflict" {
				if exit.ExitCode() != 5 || bindHits != 1 || op.Record.Phase != control.RolledBack {
					t.Fatal("actual bind failure did not produce durable rollback")
				}
				verifyOriginal(t, conflictOriginal)
				response, err := os.ReadFile(worker.StdoutPath())
				must(t, err)
				if !bytes.Equal(response, append(append([]byte(nil), op.Record.Result...), '\n')) {
					t.Fatal("rollback response differs from original receipt")
				}
				if active.Revision != oldActive.Revision || !reflect.DeepEqual(active.Generation, oldActive.Generation) || active.ManifestSHA256 != oldActive.ManifestSHA256 || active.OperationID == nil || *active.OperationID != op.Record.Identity.OperationID {
					t.Fatal("rollback did not preserve complete old authority")
				}
				for id, h := range f.handles {
					intent, _, err := f.store.ReadRunIntent(id)
					must(t, err)
					if intent.OperationID == op.Record.Identity.OperationID && intent.Generation == *oldActive.Generation {
						live = id
						if h.Identity().NonceSHA256 == oldHandle.Identity().NonceSHA256 {
							t.Fatal("rollback reused old launch identity")
						}
					}
				}
			} else {
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatal("missing actual SIGKILL")
				}
				if op.Record.Phase != control.Publishing || active.Generation == nil || *active.Generation != f.fixtures["domain-first"].Generation() {
					t.Fatal("cut did not land after target publication")
				}
				for id := range f.handles {
					intent, _, err := f.store.ReadRunIntent(id)
					must(t, err)
					if intent.OperationID == op.Record.Identity.OperationID && intent.Generation == *active.Generation {
						live = id
					}
				}
			}
			if live == "" || live == oldRun {
				t.Fatal("missing independently pinned resulting run")
			}
			trafficKey := map[bool]string{true: "app-first", false: "domain-first"}[mode == "actual-bind-conflict"]
			for _, entry := range []string{"entry-a", "entry-b"} {
				h, err := f.networks[trafficKey].open(p1FiniteContext(t), entry)
				must(t, err)
				receipt, err := h.exchange(p1FiniteContext(t), 4096)
				must(t, err)
				must(t, h.Close())
				verifyOriginal(t, receipt.Original)
				if (receipt.Node == oldReceipt.Node) != (mode == "actual-bind-conflict") {
					t.Fatal("resulting run routes through wrong fixed node")
				}
			}
			beforeOpen := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-reopen")
			manager := f.service("manager-recovery", nil)
			if !reflect.DeepEqual(beforeOpen, p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-reopen")) {
				t.Fatal("service reopening mutated Apply evidence")
			}
			recoverRaw := p1Request(t, "recover-apply", "operation.recover", map[string]any{"operation_id": op.Record.Identity.OperationID, "expected_revision": active.Revision})
			response, err := f.cli.Call(ctx, recoverRaw)
			must(t, err)
			if response.Rejected {
				t.Fatal("Apply recovery rejected", response.BusinessCode)
			}
			final, err := control.ReadReplay(f.cli.Paths(), raw)
			must(t, err)
			wanted := control.Committed
			if mode == "actual-bind-conflict" {
				wanted = control.RolledBack
			}
			if final.Record.Phase != wanted || !reflect.DeepEqual(active, f.active()) {
				t.Fatal("recovery changed resulting authority or target phase")
			}
			f.call("apply-final-stop", "runtime.stop", map[string]any{"expected_revision": f.active().Revision, "accept_disruption": true})
			f.call("apply-service-stop", "service.stop", map[string]any{"expected_revision": f.active().Revision})
			must(t, manager.Wait(ctx))
			f.archiveTrace("recovery", manager)
			must(t, f.handles[live].WaitExited(ctx))
			kills, launches := 0, 0
			for _, r := range records {
				if r.Decision == p1TraceCrashManager {
					kills++
					if r.CrashOriginal == nil {
						t.Fatal("missing durable kill receipt")
					}
				}
				if r.Event.Component == "runtime" && r.Event.Point == "launcher-started" {
					launches++
				}
			}
			if mode == "actual-bind-conflict" && (kills != 0 || launches != 2) || mode != "actual-bind-conflict" && (kills != 1 || launches != 1) {
				t.Fatal("unexpected Apply launch/kill history")
			}
			p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "final")
			_, err = p1WriteOriginal(filepath.Join(f.artifacts, "result.json"), p1JSON(t, map[string]any{"mode": mode, "phase": final.Record.Phase, "bind_listener_hits": bindHits, "manager_sigkill_receipts": kills, "replacement_launches": launches, "old_run": oldRun, "result_run": live, "result_identity": f.handles[live].Identity(), "read_only_reopen": true}))
			must(t, err)
		})
	}
}
