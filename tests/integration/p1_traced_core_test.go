//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
	"hoopa/internal/service"
	"hoopa/internal/testsupport/p1fixture"
)

// These scenes run actual product operations in a separately supervised build.
// They are evidence for named cuts, not the unimplemented full matrix adapter.
type p1TracedCore struct {
	t                                  *testing.T
	root, repo, probe, core, artifacts string
	cli                                *p1CLITransport
	store                              *config.Store
	fixtures                           map[string]*p1fixture.Fixture
	networks                           map[string]*p1FixtureNetwork
	processes                          []*p1TracedProcess
	handles                            map[config.RunID]*platform.ProcessHandle
	notLaunched                        map[config.RunID]originalRef
}

func p1LongContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 45*time.Second)
}

func newP1TracedCore(t *testing.T, label string) *p1TracedCore {
	t.Helper()
	f := &p1TracedCore{t: t, repo: os.Getenv("HOOPA_P1_REPO"), probe: os.Getenv("HOOPA_P1_PROBE"), fixtures: map[string]*p1fixture.Fixture{}, networks: map[string]*p1FixtureNetwork{}, handles: map[config.RunID]*platform.ProcessHandle{}}
	core, cliPath, artifacts := os.Getenv("HOOPA_P1_BASIC_CORE"), os.Getenv("HOOPA_P1_CLI"), os.Getenv("HOOPA_P1_ARTIFACTS")
	if f.repo == "" || f.probe == "" || core == "" || cliPath == "" || artifacts == "" {
		t.Fatal("explicit traced core inputs required")
	}
	var err error
	f.root, err = os.MkdirTemp("/tmp", "hpt-")
	must(t, err)
	t.Cleanup(f.close)
	f.artifacts = filepath.Join(artifacts, label)
	must(t, os.Mkdir(f.artifacts, 0700))
	f.core = filepath.Join(f.root, "mihomo")
	raw, err := os.ReadFile(core)
	must(t, err)
	if p1SHA(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("approved core pin mismatch")
	}
	must(t, os.WriteFile(f.core, raw, 0700))
	home, captures := filepath.Join(f.root, "h"), filepath.Join(f.artifacts, "cli")
	must(t, os.Mkdir(home, 0700))
	must(t, os.Mkdir(captures, 0700))
	f.cli, err = newP1CLITransport(cliPath, home, captures)
	must(t, err)
	workdirs := map[string]string{}
	for _, key := range []string{"app-first", "domain-first"} {
		work := filepath.Join(f.root, key)
		must(t, os.Mkdir(work, 0700))
		workdirs[key] = work
		fixture, err := p1fixture.Load(f.repo, key, f.core, work)
		must(t, err)
		must(t, os.WriteFile(filepath.Join(work, "config.json"), fixture.Material().Files["config.json"], 0600))
		f.fixtures[key] = fixture
		network, err := newP1FixtureNetwork(fixture, f.artifacts)
		must(t, err)
		f.networks[key] = network
	}
	// Explicit initialization only; the measured Apply runs in the trace child.
	c, _, err := p1fixture.OpenApplier(f.cli.Paths(), f.repo, f.core, workdirs, []string{"__hoopa_core_launcher"})
	must(t, err)
	must(t, c.Close())
	f.store, err = config.OpenStore(f.cli.Paths(), false)
	must(t, err)
	return f
}

func (f *p1TracedCore) active() config.ActivePointer {
	f.t.Helper()
	a, e := f.store.ReadActive()
	must(f.t, e)
	return a
}
func (f *p1TracedCore) launch(label string, args []string, raw []byte, decide func(p1TraceRecord) p1TraceDecision) *p1TracedProcess {
	f.t.Helper()
	path := filepath.Join(f.artifacts, label)
	must(f.t, os.Mkdir(path, 0700))
	p, e := startP1TracedProcess(f.probe, args, f.cli.environment(), raw, f.cli.Paths(), path, decide)
	must(f.t, e)
	f.processes = append(f.processes, p)
	return p
}
func (f *p1TracedCore) apply(key, id string) (*p1TracedProcess, control.StoredOperation) {
	f.t.Helper()
	p, raw := f.launchApply(key, id, nil)
	ctx, cancel := p1LongContext()
	defer cancel()
	must(f.t, p.Wait(ctx))
	must(f.t, p.TraceError())
	op, e := control.ReadReplay(f.cli.Paths(), raw)
	must(f.t, e)
	response, e := os.ReadFile(p.StdoutPath())
	must(f.t, e)
	if op.Record.Phase != control.Committed || !reflect.DeepEqual(response, append(append([]byte(nil), op.Record.Result...), '\n')) {
		f.t.Fatal("traced Apply result differs from committed receipt")
	}
	return p, op
}

func (f *p1TracedCore) launchApply(key, id string, decide func(p1TraceRecord) p1TraceDecision) (*p1TracedProcess, []byte) {
	f.t.Helper()
	fixture := f.fixtures[key]
	active := f.active()
	impact, e := p1fixture.Impact(active, fixture)
	must(f.t, e)
	plan, e := fixture.Plan(active.Revision, impact)
	must(f.t, e)
	raw := p1Request(f.t, id, "config.apply", map[string]any{"plan": json.RawMessage(plan.Bytes()), "confirmed_plan_digest": plan.Digest(), "accept_disruption": true})
	_, e = p1WriteOriginal(filepath.Join(f.artifacts, id+"-request.json"), raw)
	must(f.t, e)
	p := f.launch(id, []string{"apply", f.repo, f.core, filepath.Join(f.root, "app-first"), filepath.Join(f.root, "domain-first")}, raw, decide)
	return p, raw
}
func (f *p1TracedCore) pin() (config.RunID, *platform.ProcessHandle) {
	f.t.Helper()
	active := f.active()
	ids, e := f.store.ListRunIDs()
	must(f.t, e)
	var found config.RunID
	for _, id := range ids {
		intent, _, e := f.store.ReadRunIntent(id)
		must(f.t, e)
		if active.OperationID != nil && intent.OperationID == *active.OperationID {
			if found != "" {
				f.t.Fatal("ambiguous active run")
			}
			found = id
		}
	}
	if found == "" {
		f.t.Fatal("active run missing")
	}
	h, e := f.observe(found)
	must(f.t, e)
	f.handles[found] = h
	return found, h
}
func (f *p1TracedCore) observe(id config.RunID) (*platform.ProcessHandle, error) {
	intent, _, e := f.store.ReadRunIntent(id)
	if e != nil {
		return nil, e
	}
	birth, _, e := f.store.ReadRunBirth(id)
	if e != nil {
		return nil, e
	}
	ctx, cancel := p1LongContext()
	defer cancel()
	h, e := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if e != nil {
		return nil, e
	}
	i := h.Identity()
	if i.PID != birth.PID || i.UID != birth.UID || i.BootID != birth.BootID || i.StartTicks != birth.StartTicks {
		h.Close()
		return nil, errors.New("birth identity mismatch")
	}
	if e = h.VerifyArguments(ctx, intent.Arguments); e != nil {
		h.Close()
		return nil, e
	}
	return h, nil
}
func (f *p1TracedCore) service(label string, decide func(p1TraceRecord) p1TraceDecision) *p1TracedProcess {
	f.t.Helper()
	p := f.launch(label, []string{"service"}, nil, decide)
	ctx, cancel := p1LongContext()
	defer cancel()
	for {
		conn, e := service.DialEndpoint(ctx, f.cli.Paths())
		if e == nil {
			raw, e := conn.SyscallConn()
			must(f.t, e)
			var peer *syscall.Ucred
			var pe error
			must(f.t, raw.Control(func(fd uintptr) { peer, pe = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) }))
			must(f.t, pe)
			must(f.t, conn.Close())
			if int(peer.Pid) == p.PID() && int(peer.Uid) == os.Getuid() {
				return p
			}
			f.t.Fatal("endpoint belongs to a different process")
		}
		select {
		case <-p.waitDone:
			f.t.Fatal("traced service exited before readiness")
		case <-ctx.Done():
			f.t.Fatal("traced service readiness timeout")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (f *p1TracedCore) call(id, operation string, params any) p1CLIResult {
	f.t.Helper()
	ctx, cancel := p1LongContext()
	defer cancel()
	r, e := f.cli.Call(ctx, p1Request(f.t, id, operation, params))
	must(f.t, e)
	if r.Rejected {
		f.t.Fatal("CLI request rejected", operation, r.BusinessCode)
	}
	verifyOriginal(f.t, r.Original)
	return r
}
func (f *p1TracedCore) archiveTrace(label string, p *p1TracedProcess) []p1TraceRecord {
	f.t.Helper()
	must(f.t, p.TraceError())
	records := p.Records()
	if len(records) == 0 {
		f.t.Fatal("missing actual trace")
	}
	for _, r := range records {
		if r.PeerPID != p.PID() || r.PeerUID != os.Getuid() || r.Decision == "" {
			f.t.Fatal("incomplete trace peer or decision")
		}
		for _, ref := range r.Originals {
			verifyOriginal(f.t, ref)
		}
	}
	_, e := p1WriteOriginal(filepath.Join(f.artifacts, label+"-trace.json"), p1JSON(f.t, records))
	must(f.t, e)
	return records
}
func (f *p1TracedCore) close() {
	t := f.t
	for _, p := range f.processes {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		if e := p.Close(ctx); e != nil {
			t.Error("trace child cleanup", e)
		}
		cancel()
	}
	if f.store != nil {
		ids, e := f.store.ListRunIDs()
		if e != nil {
			t.Error("run inventory cleanup", e)
		}
		var receipts []map[string]any
		for _, id := range ids {
			if ref, ok := f.notLaunched[id]; ok {
				verifyOriginal(t, ref)
				receipts = append(receipts, map[string]any{"run_id": id, "kind": "intent-with-proven-pre-launch-cut", "proof_original": ref, "launch_called": false})
				continue
			}
			h := f.handles[id]
			if h == nil {
				h, e = f.observe(id)
				if e != nil {
					t.Error("cleanup requires proven original core", e)
					continue
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			probe, pc := context.WithTimeout(ctx, 50*time.Millisecond)
			exited := h.WaitExited(probe) == nil
			pc()
			if !exited {
				if e = h.SignalVerified(ctx, syscall.SIGTERM); e != nil {
					t.Error("proven core cleanup signal", e)
				}
			}
			exitErr := h.WaitExited(ctx)
			if exitErr != nil {
				t.Error("core cleanup wait", exitErr)
			}
			// Every core's original launcher parent is a traced process, already
			// reaped above. The isolated test subreaper now owns that exact child.
			// A candidate stopped before its Apply worker exits can already have
			// been reaped by that original parent. Only wait on a still-present
			// orphan; a missing proc entry after original-pidfd exit is sufficient.
			_, statErr := os.Stat(filepath.Join("/proc", strconv.Itoa(h.Identity().PID)))
			reap := !errors.Is(statErr, os.ErrNotExist)
			status, absenceErr := p1WaitProcessAbsent(ctx, h.Identity().PID, reap)
			if absenceErr != nil {
				t.Error("core reap/absence", absenceErr)
			}
			receipts = append(receipts, map[string]any{"run_id": id, "identity": h.Identity(), "cleanup_signal_needed": !exited, "pidfd_exited": exitErr == nil, "proc_absent": absenceErr == nil, "wait_status": status, "orphan_reaped": reap && absenceErr == nil})
			h.Close()
			cancel()
		}
		if f.artifacts != "" {
			raw, e := json.Marshal(receipts)
			if e == nil {
				_, e = p1WriteOriginal(filepath.Join(f.artifacts, "cleanup.json"), raw)
			}
			if e != nil {
				t.Error("cleanup evidence", e)
			}
		}
		f.store.Close()
	}
	for _, n := range f.networks {
		if e := n.Close(); e != nil {
			t.Error("network cleanup", e)
		}
	}
	if f.cli != nil {
		f.cli.Close()
	}
	if f.root != "" {
		if e := os.RemoveAll(f.root); e != nil {
			t.Error(e)
		}
	}
}

func TestP1TracedStopRecovery(t *testing.T) {
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
	for _, point := range []string{"stop-intent-durable", "stop-exit-durable", "stop-active-published"} {
		t.Run(point, func(t *testing.T) {
			f := newP1TracedCore(t, point)
			apply, _ := f.apply("app-first", "trace-seed-apply")
			f.archiveTrace("seed", apply)
			run, handle := f.pin()
			before := f.active()
			held, err := f.networks["app-first"].open(p1FiniteContext(t), "entry-a")
			must(t, err)
			defer held.Close()
			first, err := held.exchange(p1FiniteContext(t), 4096)
			must(t, err)
			verifyOriginal(t, first.Original)
			manager := f.service("manager-cut", func(r p1TraceRecord) p1TraceDecision {
				if r.Event.Component == "workflow" && r.Event.Point == point {
					return p1TraceCrashManager
				}
				return p1TraceContinue
			})
			stopRaw := p1Request(t, "trace-stop-cut", "runtime.stop", map[string]any{"expected_revision": before.Revision, "accept_disruption": true})
			ctx, cancel := p1LongContext()
			defer cancel()
			interrupted, callErr := f.cli.Call(ctx, stopRaw)
			// The shipped frontend turns a broken service exchange into its own
			// valid READ_UNAVAILABLE envelope. This is not a durable Stop result.
			must(t, callErr)
			verifyOriginal(t, interrupted.Original)
			if !interrupted.Rejected || interrupted.BusinessCode != "READ_UNAVAILABLE" {
				t.Fatal("killed manager did not return the expected unavailable response")
			}
			var exit *exec.ExitError
			if err = manager.Wait(ctx); !errors.As(err, &exit) {
				t.Fatal("missing killed manager wait result", err)
			}
			records := f.archiveTrace("cut", manager)
			cuts, signals := 0, 0
			for _, r := range records {
				if r.Decision == p1TraceCrashManager {
					cuts++
					if r.Event.Point != point || r.CrashOriginal == nil {
						t.Fatal("crash receipt missing requested cut")
					}
				}
				if r.Event.Component == "platform" && r.Event.Point == "signal-sent" {
					signals++
				}
			}
			if cuts != 1 {
				t.Fatal("fault selector did not hit exactly once")
			}
			stop, err := control.ReadReplay(f.cli.Paths(), stopRaw)
			must(t, err)
			if stop.Record.Phase == control.Committed {
				t.Fatal("cut happened after stop committed")
			}
			if point == "stop-intent-durable" {
				if signals != 0 || !reflect.DeepEqual(f.active(), before) {
					t.Fatal("intent cut already signalled or published")
				}
				probe, pc := context.WithTimeout(ctx, 50*time.Millisecond)
				if handle.WaitExited(probe) == nil {
					t.Fatal("core exited with manager")
				}
				pc()
				second, err := held.exchange(p1FiniteContext(t), 4096)
				must(t, err)
				verifyOriginal(t, second.Original)
				fresh, err := f.networks["app-first"].open(p1FiniteContext(t), "entry-b")
				must(t, err)
				third, err := fresh.exchange(p1FiniteContext(t), 4096)
				must(t, err)
				must(t, fresh.Close())
				verifyOriginal(t, third.Original)
				if first.Node != second.Node || first.Node != third.Node {
					t.Fatal("route changed after manager crash")
				}
			} else {
				if signals != 1 {
					t.Fatal("missing unique real core signal before cut")
				}
				must(t, handle.WaitExited(ctx))
				if _, err = held.exchange(p1FiniteContext(t), 4096); err == nil {
					t.Fatal("stopped core retained forwarding")
				}
			}
			if point == "stop-active-published" && f.active().DesiredRuntime != "stopped" {
				t.Fatal("active publication cut not observed")
			}
			durableBefore := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-reopen")
			recoveredManager := f.service("manager-recover", nil)
			durableAfter := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-reopen")
			if !reflect.DeepEqual(durableBefore, durableAfter) {
				t.Fatal("service reopen silently recovered durable state")
			}
			recoverRaw := p1Request(t, "trace-recover-stop", "operation.recover", map[string]any{"operation_id": stop.Record.Identity.OperationID, "expected_revision": f.active().Revision})
			response, err := f.cli.Call(ctx, recoverRaw)
			must(t, err)
			if response.Rejected {
				t.Fatal("explicit recovery rejected", response.BusinessCode)
			}
			call, err := control.ReadReplay(f.cli.Paths(), recoverRaw)
			must(t, err)
			final, err := control.ReadReplay(f.cli.Paths(), stopRaw)
			must(t, err)
			if call.Record.Phase != control.Committed || final.Record.Phase != control.Committed || call.Record.Identity.OperationID == final.Record.Identity.OperationID || call.Record.Identity.Intent.TargetRevision != nil || f.active().DesiredRuntime != "stopped" {
				t.Fatal("recovery did not preserve separate call/target semantics")
			}
			must(t, handle.WaitExited(ctx))
			ids, err := f.store.ListRunIDs()
			must(t, err)
			if len(ids) != 1 || ids[0] != run {
				t.Fatal("recovery spawned another core")
			}
			f.call("trace-service-stop", "service.stop", map[string]any{"expected_revision": f.active().Revision})
			must(t, recoveredManager.Wait(ctx))
			recoveryRecords := f.archiveTrace("recovery", recoveredManager)
			for _, r := range recoveryRecords {
				if r.Event.Component == "runtime" && r.Event.Point == "launcher-started" {
					t.Fatal("recovery trace contains core spawn")
				}
				if r.Event.Component == "platform" && r.Event.Point == "signal-sent" {
					signals++
				}
			}
			if signals != 1 {
				t.Fatal("stop/recovery did not issue exactly one real core signal")
			}
			p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "final")
			_, err = p1WriteOriginal(filepath.Join(f.artifacts, "result.json"), p1JSON(t, map[string]any{"cut": point, "manager_sigkill_receipts": cuts, "core_signals": signals, "run_id": run, "original_identity": handle.Identity(), "recovery_call_id": call.Record.Identity.OperationID, "target_operation_id": final.Record.Identity.OperationID, "no_spawn_on_recovery": true, "read_only_reopen": true}))
			must(t, err)
		})
	}
}
