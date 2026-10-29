//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
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
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
	"hoopa/internal/service"
	"hoopa/internal/testsupport/p1fixture"
)

func p1SHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func p1WriteOriginal(path string, raw []byte) (originalRef, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return originalRef{}, err
	}
	_, err = f.Write(raw)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return originalRef{}, err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return originalRef{}, err
	}
	err = errors.Join(d.Sync(), d.Close())
	if err != nil {
		return originalRef{}, err
	}
	return originalRef{Path: path, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, nil
}
func p1JSON(t *testing.T, v any) []byte { t.Helper(); b, e := json.Marshal(v); must(t, e); return b }
func p1Request(t *testing.T, id, operation string, params any) []byte {
	t.Helper()
	return p1JSON(t, map[string]any{"api_version": "v1", "request_id": id, "operation": operation, "params": params})
}
func p1FiniteContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func p1WaitProcessAbsent(ctx context.Context, pid int, reap bool) (int, error) {
	statusCode := -1
	for {
		if reap {
			var status syscall.WaitStatus
			got, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
			if err != nil {
				return statusCode, err
			}
			if got == pid {
				reap = false
				statusCode = int(status)
			}
		}
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		if errors.Is(err, os.ErrNotExist) && !reap {
			return statusCode, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return statusCode, err
		}
		select {
		case <-ctx.Done():
			return statusCode, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Public BasicApplier invokes this fixed test image dispatch. Managed runtime
// start below instead invokes the actual shipped hoopa launcher dispatch.
func TestP1FiniteCoreLauncherDispatch(t *testing.T) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	must(t, mihomo.ExecBasicLauncher(ctx, os.NewFile(3, "birth"), os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE")))
	t.Fatal("successful core exec returned")
}

type p1Manager struct {
	cmd     *exec.Cmd
	done    chan error
	reaped  bool
	outputs []*os.File
}

func p1StartManager(t *testing.T, cli *p1CLITransport, artifacts, label string) *p1Manager {
	t.Helper()
	cmd := exec.Command(cli.executable, "service", "run")
	cmd.Env = cli.environment()
	m := &p1Manager{cmd: cmd, done: make(chan error, 1)}
	for _, name := range []string{"stdout", "stderr"} {
		f, e := os.OpenFile(filepath.Join(artifacts, label+"."+name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		must(t, e)
		m.outputs = append(m.outputs, f)
	}
	cmd.Stdout = m.outputs[0]
	cmd.Stderr = m.outputs[1]
	must(t, cmd.Start())
	go func() { m.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if !m.reaped {
			// Exact direct child created by this fixture; never a discovered PID.
			_ = m.cmd.Process.Kill()
			select {
			case <-m.done:
				m.reaped = true
			case <-time.After(5 * time.Second):
				t.Error("owned service cleanup timeout")
			}
		}
		for _, f := range m.outputs {
			_ = f.Sync()
			_ = f.Close()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for {
		select {
		case e := <-m.done:
			m.reaped = true
			t.Fatal("service exited before ready", e)
		default:
		}
		conn, e := service.DialEndpoint(ctx, cli.Paths())
		if e == nil {
			raw, e := conn.SyscallConn()
			must(t, e)
			var peer *syscall.Ucred
			var peerErr error
			must(t, raw.Control(func(fd uintptr) {
				peer, peerErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
			}))
			must(t, peerErr)
			must(t, conn.Close())
			if int(peer.Pid) != cmd.Process.Pid || int(peer.Uid) != os.Geteuid() {
				t.Fatal("service ready endpoint is not original child")
			}
			_, e = p1WriteOriginal(filepath.Join(artifacts, label+"-ready.json"), p1JSON(t, map[string]any{"pid": cmd.Process.Pid, "uid": peer.Uid, "authenticated_endpoint": true}))
			must(t, e)
			return m
		}
		select {
		case <-ctx.Done():
			t.Fatal("service readiness timeout", e)
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (m *p1Manager) wait(t *testing.T, killed bool) {
	t.Helper()
	select {
	case err := <-m.done:
		m.reaped = true
		if killed {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal("manager did not exit by SIGKILL")
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("wrong manager termination")
			}
		} else {
			must(t, err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("manager did not exit")
	}
}

func p1FiniteImpact(active config.ActivePointer, f *p1fixture.Fixture) (control.PlanImpact, error) {
	impact := control.PlanImpact{RequiresDisruption: true, AffectedApps: []string{"entry-a", "entry-b"}, TargetGeneration: string(f.Generation()), ConnectionEffect: "unknown", RuntimeAction: "start"}
	if active.Generation == nil && active.OperationID == nil && active.DesiredRuntime == "stopped" {
		return impact, nil
	}
	if active.Generation == nil || active.OperationID == nil || active.DesiredRuntime != "running" || *active.Generation == f.Generation() {
		return control.PlanImpact{}, control.ErrPlan
	}
	previous := string(*active.Generation)
	impact.PreviousGeneration = &previous
	impact.RuntimeAction = "replace"
	impact.ConnectionEffect = "interrupt"
	return impact, nil
}

func p1ArchiveTree(t *testing.T, paths config.Paths, artifacts, label string) map[string]fileSnapshot {
	t.Helper()
	result := map[string]fileSnapshot{}
	for scope, root := range map[string]string{"state": paths.State, "config": paths.Config} {
		must(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("unexpected durable entry")
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			key := scope + "/" + rel
			result[key] = fileSnapshot{Kind: "regular", Mode: uint32(info.Mode().Perm()), Bytes: info.Size(), SHA256: p1SHA(data), ModifiedNS: info.ModTime().UnixNano()}
			destination := filepath.Join(artifacts, label, key)
			if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				return err
			}
			_, err = p1WriteOriginal(destination, data)
			return err
		}))
	}
	_, e := p1WriteOriginal(filepath.Join(artifacts, label+"-files.json"), p1JSON(t, result))
	must(t, e)
	return result
}

// A finite actual-core integration scene, not a substitute result for the full
// productHarness fault matrix. Missing explicit inputs fail instead of skipping.
func TestP1FiniteDualEntryCoreContinuity(t *testing.T) {
	repo, core, cliPath, artifacts := os.Getenv("HOOPA_P1_REPO"), os.Getenv("HOOPA_P1_BASIC_CORE"), os.Getenv("HOOPA_P1_CLI"), os.Getenv("HOOPA_P1_ARTIFACTS")
	if repo == "" || core == "" || cliPath == "" || artifacts == "" {
		t.Fatal("explicit finite core/CLI/repo/artifacts required")
	}
	// This isolated test process reaps the core orphaned by its own service kill.
	// Never reap arbitrary host children or silently treat a zombie as absent.
	var prior int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	defer func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	}()
	// Keep the authenticated Unix socket below sockaddr_un's path bound.
	private, e := os.MkdirTemp("/tmp", "hpf-")
	must(t, e)
	t.Cleanup(func() {
		if e := os.RemoveAll(private); e != nil {
			t.Error(e)
		}
	})
	binaryPath := filepath.Join(private, "mihomo")
	coreRaw, e := os.ReadFile(core)
	must(t, e)
	if p1SHA(coreRaw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("approved core pin mismatch")
	}
	must(t, os.WriteFile(binaryPath, coreRaw, 0700))
	home, captures := filepath.Join(private, "h"), filepath.Join(artifacts, "cli")
	must(t, os.Mkdir(home, 0700))
	must(t, os.Mkdir(captures, 0700))
	cli, e := newP1CLITransport(cliPath, home, captures)
	must(t, e)
	defer cli.Close()
	fixtures := map[string]*p1fixture.Fixture{}
	networks := map[string]*p1FixtureNetwork{}
	for _, key := range []string{"app-first", "domain-first"} {
		work := filepath.Join(private, key)
		must(t, os.Mkdir(work, 0700))
		f, e := p1fixture.Load(repo, key, binaryPath, work)
		must(t, e)
		m := f.Material()
		must(t, os.WriteFile(filepath.Join(work, "config.json"), m.Files["config.json"], 0600))
		fixtures[key] = f
		n, e := newP1FixtureNetwork(f, artifacts)
		must(t, e)
		networks[key] = n
		defer func() {
			if e := n.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	validator := func(raw json.RawMessage, inputs []control.PlanInput) error {
		for _, f := range fixtures {
			if f.ValidateIntent(raw, inputs) == nil {
				return nil
			}
		}
		return control.ErrPlan
	}
	resolve := func(plan control.Plan) (*p1fixture.Fixture, error) {
		for _, f := range fixtures {
			if plan.Document().Impact.TargetGeneration == string(f.Generation()) {
				return f, nil
			}
		}
		return nil, control.ErrPlan
	}
	current := func(ctx context.Context, plan control.Plan) (control.CurrentPlanInputs, error) {
		if ctx == nil || ctx.Err() != nil {
			return control.CurrentPlanInputs{}, control.ErrPlan
		}
		f, e := resolve(plan)
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		// Freshly pin originals; do not echo Plan inputs/impact as an observation.
		fresh, e := p1fixture.Load(repo, f.Key(), binaryPath, f.Material().Workdir)
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		executable, e := platform.OpenExecutable(ctx, binaryPath, fresh.Material().Manifest.Binaries[0].SHA256)
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		if e = executable.Close(); e != nil {
			return control.CurrentPlanInputs{}, e
		}
		s, e := config.OpenStore(cli.Paths(), false)
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		defer s.Close()
		active, e := s.ReadActive()
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		impact, e := p1FiniteImpact(active, fresh)
		if e != nil {
			return control.CurrentPlanInputs{}, e
		}
		return control.CurrentPlanInputs{CompilerVersion: p1fixture.CompilerVersion, CapabilitiesDigest: fresh.CapabilityDigest(), Inputs: fresh.Inputs(), Impact: impact}, nil
	}
	var coordinator *control.Coordinator
	defer func() {
		if coordinator != nil {
			_ = coordinator.Close()
		}
	}()
	open := func() {
		var e error
		coordinator, e = control.OpenCoordinator(cli.Paths(), validator, current)
		must(t, e)
	}
	open()
	store, e := config.OpenStore(cli.Paths(), false)
	must(t, e)
	defer store.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	var orphan config.RunID
	defer func() {
		ids, e := store.ListRunIDs()
		if e != nil {
			t.Error("cleanup inventory", e)
			return
		}
		var cleanup []map[string]any
		for _, id := range ids {
			h := handles[id]
			intent, _, ie := store.ReadRunIntent(id)
			birth, _, be := store.ReadRunBirth(id)
			if ie != nil || be != nil {
				t.Error("cleanup authority unavailable")
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if h == nil {
				h, e = platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
				if e == nil && (h.Identity().PID != birth.PID || h.Identity().UID != birth.UID || h.Identity().BootID != birth.BootID || h.Identity().StartTicks != birth.StartTicks || h.VerifyArguments(ctx, intent.Arguments) != nil) {
					h.Close()
					h = nil
				}
			}
			if h == nil {
				t.Error("cleanup lacks original process proof")
				cancel()
				continue
			}
			probe, stopProbe := context.WithTimeout(ctx, 50*time.Millisecond)
			exited := h.WaitExited(probe) == nil
			stopProbe()
			if !exited {
				if e := h.SignalVerified(ctx, syscall.SIGTERM); e != nil {
					t.Error("cleanup signal", e)
				}
			}
			exitErr := h.WaitExited(ctx)
			if exitErr != nil {
				t.Error("cleanup exit", exitErr)
			}
			waitStatus, absenceErr := p1WaitProcessAbsent(ctx, birth.PID, id == orphan)
			if absenceErr != nil {
				t.Error("owned process cleanup/reaping incomplete", absenceErr)
			}
			cleanup = append(cleanup, map[string]any{"run_id": id, "pid": birth.PID, "pidfd_exited": exitErr == nil, "proc_absent": absenceErr == nil, "orphan_reaped": id == orphan && absenceErr == nil, "orphan_wait_status": waitStatus, "cleanup_signal_needed": !exited})
			h.Close()
			cancel()
		}
		raw, err := json.Marshal(cleanup)
		if err == nil {
			_, err = p1WriteOriginal(filepath.Join(artifacts, "cleanup.json"), raw)
		}
		if err != nil {
			t.Error("cleanup evidence unavailable", err)
		}
	}()
	active := func() config.ActivePointer { v, e := store.ReadActive(); must(t, e); return v }
	var sourceCalls int
	applier := func() *control.BasicApplier {
		a, e := control.NewBasicApplier(coordinator, func(ctx context.Context, p control.Plan) (control.BasicMaterial, error) {
			sourceCalls++
			f, e := resolve(p)
			if e != nil {
				return control.BasicMaterial{}, e
			}
			return f.Source(ctx, p)
		}, []string{"-test.run=^TestP1FiniteCoreLauncherDispatch$", "-test.v"})
		must(t, e)
		return a
	}
	archiveOperation := func(name string, op control.StoredOperation) {
		_, e := p1WriteOriginal(filepath.Join(artifacts, name+".json"), p1JSON(t, op.Record))
		must(t, e)
	}
	apply := func(key, id string) control.StoredOperation {
		f := fixtures[key]
		base := active()
		impact, e := p1FiniteImpact(base, f)
		must(t, e)
		plan, e := f.Plan(base.Revision, impact)
		must(t, e)
		raw := p1Request(t, id, "config.apply", map[string]any{"plan": json.RawMessage(plan.Bytes()), "confirmed_plan_digest": plan.Digest(), "accept_disruption": true})
		op, made, e := applier().Apply(p1FiniteContext(t), raw)
		if e != nil || !made || op.Record.Phase != control.Committed {
			t.Fatal("fixed public Apply", key, made, e)
		}
		archiveOperation(id, op)
		return op
	}
	pin := func(expected config.Generation) config.RunID {
		base := active()
		if base.Generation == nil || *base.Generation != expected || base.OperationID == nil {
			t.Fatal("active generation missing")
		}
		ids, e := store.ListRunIDs()
		must(t, e)
		var found config.RunID
		for _, id := range ids {
			intent, _, e := store.ReadRunIntent(id)
			must(t, e)
			if intent.OperationID != *base.OperationID {
				continue
			}
			if found != "" {
				t.Fatal("ambiguous active run")
			}
			found = id
			birth, _, e := store.ReadRunBirth(id)
			must(t, e)
			cap, e := mihomo.ApprovedBasicCapability(binaryPath)
			must(t, e)
			controller, e := mihomo.NewController(store, cap)
			must(t, e)
			ownership, e := owned.NewOwnership(store, controller)
			must(t, e)
			run, e := ownership.Observe(p1FiniteContext(t), id)
			must(t, e)
			if run.Observation().Gate != "open" {
				t.Fatal("active gate not open")
			}
			h, e := platform.ObserveProcess(p1FiniteContext(t), birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
			must(t, e)
			if h.Identity() != run.Identity() {
				t.Fatal("independent identity mismatch")
			}
			must(t, run.Close())
			must(t, h.VerifyArguments(p1FiniteContext(t), intent.Arguments))
			if old := handles[id]; old != nil {
				if old.Identity() != h.Identity() {
					t.Fatal("original core identity changed")
				}
				h.Close()
			} else {
				handles[id] = h
			}
		}
		if found == "" {
			t.Fatal("no independently verified active core")
		}
		return found
	}
	call := func(id, operation string, params any) p1CLIResult {
		r, e := cli.Call(p1FiniteContext(t), p1Request(t, id, operation, params))
		must(t, e)
		if r.Rejected {
			t.Fatal("CLI business rejection", operation, r.BusinessCode)
		}
		verifyOriginal(t, r.Original)
		return r
	}
	status := func(id string) control.ServiceStatus {
		r := call(id, "status.get", map[string]any{})
		var envelope struct{ Data control.ServiceStatus }
		must(t, json.Unmarshal(r.Response, &envelope))
		return envelope.Data
	}
	var samples []map[string]any
	exchange := func(h *p1HeldTunnel, phase, entry, key string, run config.RunID) {
		receipt, e := h.exchange(p1FiniteContext(t), 4096)
		must(t, e)
		verifyOriginal(t, receipt.Original)
		want := map[string]string{"app-first": "node:dee775b86614224f1b7da82180edf80bc75670126e57a0dcae4ae4f951232fe0", "domain-first": "node:5ba26d4d8cd6ce5f59843769e4e3d5d69e1cfcf115b9c77b2df94d39e2f15f06"}[key]
		if receipt.Node != want {
			t.Fatal("independent upstream selected wrong node", phase, entry)
		}
		samples = append(samples, map[string]any{"phase": phase, "entry": entry, "run_id": run, "receipt": receipt})
	}
	fresh := func(phase, entry, key string, run config.RunID) {
		h, e := networks[key].open(p1FiniteContext(t), entry)
		must(t, e)
		defer h.Close()
		exchange(h, phase, entry, key, run)
	}
	initial := apply("app-first", "finite-prepare-apply")
	_ = initial
	first := pin(fixtures["app-first"].Generation())
	op, made, e := applier().Stop(p1FiniteContext(t), p1Request(t, "finite-prepare-stop", "runtime.stop", map[string]any{"expected_revision": active().Revision, "accept_disruption": true}))
	if e != nil || !made || op.Record.Phase != control.Committed {
		t.Fatal("explicit fixture setup Stop", e)
	}
	archiveOperation("finite-prepare-stop", op)
	must(t, handles[first].WaitExited(p1FiniteContext(t)))
	must(t, coordinator.Close())
	coordinator = nil
	p1ArchiveTree(t, cli.Paths(), artifacts, "prepared-stopped")
	manager := p1StartManager(t, cli, artifacts, "manager-1")
	stopped := active()
	startRaw := p1Request(t, "finite-runtime-start", "runtime.start", map[string]any{"expected_revision": stopped.Revision, "control_only": false})
	started, e := cli.Call(p1FiniteContext(t), startRaw)
	must(t, e)
	if started.Rejected {
		t.Fatal("real CLI start refused", started.BusinessCode)
	}
	second := pin(fixtures["app-first"].Generation())
	if second == first || active().Revision == stopped.Revision {
		t.Fatal("ordinary start did not create new run/revision")
	}
	originalActive := active()
	originalIdentity := handles[second].Identity()
	startRecord, e := control.ReadReplay(cli.Paths(), startRaw)
	must(t, e)
	archiveOperation("finite-runtime-start", startRecord)
	held, e := networks["app-first"].open(p1FiniteContext(t), "entry-a")
	must(t, e)
	defer held.Close()
	exchange(held, "before-manager-kill", "entry-a", "app-first", second)
	fresh("before-manager-kill", "entry-b", "app-first", second)
	beforeKill := p1ArchiveTree(t, cli.Paths(), artifacts, "before-manager-kill")
	_, e = p1WriteOriginal(filepath.Join(artifacts, "manager-kill-intent.json"), p1JSON(t, map[string]any{"pid": manager.cmd.Process.Pid, "signal": "SIGKILL", "target": "owned-direct-child"}))
	must(t, e)
	must(t, manager.cmd.Process.Kill())
	manager.wait(t, true)
	orphan = second
	_, e = p1WriteOriginal(filepath.Join(artifacts, "manager-kill-exit.json"), p1JSON(t, map[string]any{"pid": manager.cmd.Process.Pid, "wait_signal": "SIGKILL", "wait_complete": true}))
	must(t, e)
	offline := status("finite-offline-status")
	if offline.Service != "unavailable" || offline.Collector != "unavailable" || offline.Core.State != "unknown" {
		t.Fatal("offline state overstated proof", offline.Service, offline.Collector, offline.Core.State)
	}
	if !reflect.DeepEqual(beforeKill, p1ArchiveTree(t, cli.Paths(), artifacts, "after-manager-kill")) {
		t.Fatal("kill/offline observation mutated durable state")
	}
	if pin(fixtures["app-first"].Generation()) != second || handles[second].Identity() != originalIdentity {
		t.Fatal("manager kill changed core")
	}
	exchange(held, "manager-dead", "entry-a", "app-first", second)
	fresh("manager-dead", "entry-b", "app-first", second)
	manager = p1StartManager(t, cli, artifacts, "manager-2")
	if !reflect.DeepEqual(beforeKill, p1ArchiveTree(t, cli.Paths(), artifacts, "after-service-reopen")) {
		t.Fatal("service reopen mutated durable authority")
	}
	call("finite-explicit-recover", "operation.recover", map[string]any{"expected_revision": active().Revision, "operation_id": startRecord.Record.Identity.OperationID})
	if !reflect.DeepEqual(active(), originalActive) || pin(fixtures["app-first"].Generation()) != second {
		t.Fatal("recovery changed active core/revision")
	}
	replayed, e := control.ReadReplay(cli.Paths(), startRaw)
	must(t, e)
	if replayed.SHA256 != startRecord.SHA256 || !bytes.Equal(replayed.Record.Result, startRecord.Record.Result) {
		t.Fatal("recovery rewrote original start")
	}
	exchange(held, "after-explicit-recover", "entry-a", "app-first", second)
	fresh("after-explicit-recover", "entry-b", "app-first", second)
	call("finite-service-stop", "service.stop", map[string]any{"expected_revision": active().Revision})
	manager.wait(t, false)
	exchange(held, "after-service-stop", "entry-a", "app-first", second)
	open()
	apply("domain-first", "finite-replace-apply")
	third := pin(fixtures["domain-first"].Generation())
	if third == second {
		t.Fatal("replacement retained old run")
	}
	must(t, handles[second].WaitExited(p1FiniteContext(t)))
	if _, e := held.exchange(p1FiniteContext(t), 4096); e == nil {
		t.Fatal("replacement unexpectedly kept terminated original stream")
	}
	for _, entry := range []string{"entry-a", "entry-b"} {
		fresh("after-replacement", entry, "domain-first", third)
	}
	finalStop, made, e := applier().Stop(p1FiniteContext(t), p1Request(t, "finite-final-stop", "runtime.stop", map[string]any{"expected_revision": active().Revision, "accept_disruption": true}))
	if e != nil || !made || finalStop.Record.Phase != control.Committed {
		t.Fatal("final Stop", e)
	}
	archiveOperation("finite-final-stop", finalStop)
	must(t, handles[third].WaitExited(p1FiniteContext(t)))
	if sourceCalls != 2 {
		t.Fatal("lifecycle/recovery called fixture compiler", sourceCalls)
	}
	ids, e := store.ListRunIDs()
	must(t, e)
	if len(ids) != 3 {
		t.Fatal("unexpected extra product core run", len(ids))
	}
	p1ArchiveTree(t, cli.Paths(), artifacts, "final-stopped")
	_, e = p1WriteOriginal(filepath.Join(artifacts, "scene.json"), p1JSON(t, map[string]any{"scope": "finite actual dual-entry core, normal start, service SIGKILL, terminal recovery, replacement; not full T034 matrix", "runs": ids, "source_calls": sourceCalls, "samples": samples, "first": handles[first].Identity(), "second": originalIdentity, "third": handles[third].Identity(), "collector": "unavailable", "full_matrix": false}))
	must(t, e)
	t.Logf("finite actual scene: cores=%d source_calls=%d independent_payload_echoes=%d manager_SIGKILL=1", len(ids), sourceCalls, len(samples))
}
