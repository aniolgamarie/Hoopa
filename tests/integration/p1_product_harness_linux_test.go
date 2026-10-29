//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/service"
)

var errP1Harness = errors.New("P1_PRODUCT_HARNESS_EVIDENCE_UNAVAILABLE")
var errP1HarnessUnbuilt = errors.New("P1_PRODUCT_HARNESS_SCENARIO_NOT_IMPLEMENTED")
var errP1HarnessInterrupted = errors.New("P1_PRODUCT_HARNESS_OBSERVED_MANAGER_CRASH")

// Real assembly under development. Unsupported scenario methods fail explicitly;
// implementing this factory is not itself completion of T034's fault matrix.
type p1ProductHarness struct {
	held            map[string]config.RunID
	t               *testing.T
	f               *p1TracedCore
	manager         *p1TracedProcess
	frontend        *p1Frontend
	traffic         *p1ProductTraffic
	mu              sync.Mutex
	serial          uint64
	observerErr     error
	stable          map[string]operationRecord
	extras          []originalRef
	restart         bool
	closing         bool
	applyPlans      map[p1ApplyPlanKey]control.Plan
	applyOriginal   map[string]p1SubmittedPlan
	faults          []*p1ArmedFault
	blockers        []net.Listener
	faultScopes     map[int]p1FaultScope
	launchProofs    []p1LaunchProof
	capacityFault   *faultHit
	optionalFixture bool
	ownershipFault  *faultHit
	sessions        []sessionRecord
	foreignListener *p1ForeignListener
	sessionDriver   *p1SessionDriver
	sessionTraceAt  int
}

func p1Fact[T any](value T, refs ...originalRef) observed[T] {
	return observed[T]{State: "observed", Value: &value, Originals: append([]originalRef(nil), refs...)}
}
func p1Missing[T any](reason string) observed[T] {
	return observed[T]{State: "unknown", Reason: reason}
}

func newP1ProductHarness(t *testing.T) productHarness {
	t.Helper()
	var previous int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&previous)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(previous), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	})
	h := &p1ProductHarness{t: t, stable: map[string]operationRecord{}, held: map[string]config.RunID{}}
	h.f = newP1TracedCore(t, "factory-"+p1SHA([]byte(t.Name()))[:16])
	t.Cleanup(h.close)
	// Explicit, fully retained preparation: the current product Start consumes
	// an existing stopped bundle. This Apply and Stop are part of all evidence.
	seed, _ := h.f.apply(baselineBundle, "factory-seed-apply")
	h.f.pin()
	h.f.archiveTrace("factory-seed", seed)
	must(t, h.startManager(false))
	h.f.call("factory-seed-stop", "runtime.stop", map[string]any{"expected_revision": h.f.active().Revision, "accept_disruption": true})
	trafficDir := h.directory("traffic")
	var err error
	h.traffic, err = newP1ProductTraffic(h.f.store, h.f.networks, trafficDir)
	must(t, err)
	return h
}

func (h *p1ProductHarness) directory(kind string) string {
	h.mu.Lock()
	h.serial++
	n := h.serial
	h.mu.Unlock()
	p := filepath.Join(h.f.artifacts, fmt.Sprintf("%06d-%s", n, kind))
	must(h.t, os.Mkdir(p, 0700))
	return p
}
func (h *p1ProductHarness) original(kind string, value any) (originalRef, error) {
	path := filepath.Join(h.directory(kind), "receipt.json")
	return p1WriteOriginal(path, p1JSON(h.t, value))
}
func (h *p1ProductHarness) nextID(kind string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serial++
	return fmt.Sprintf("factory-%s-%06d", kind, h.serial)
}

func (h *p1ProductHarness) decide(r p1TraceRecord) p1TraceDecision {
	if err := h.recordFaultScope(r); err != nil {
		h.mu.Lock()
		h.observerErr = err
		h.mu.Unlock()
		return p1TraceCrashManager
	}
	// Pin each actual incarnation while the product's fresh registration hook
	// holds it live. Even a candidate later stopped during rollback stays known.
	if r.Event.Component == "runtime" && r.Event.Point == "registration-verified" {
		id := config.RunID(r.Event.RunID)
		h.mu.Lock()
		exists := h.f.handles[id] != nil
		h.mu.Unlock()
		if !exists {
			p, err := h.f.observe(id)
			h.mu.Lock()
			if err != nil {
				h.observerErr = errP1Harness
			} else if h.f.handles[id] == nil {
				h.f.handles[id] = p
			} else {
				p.Close()
			}
			h.mu.Unlock()
			if err != nil {
				return p1TraceCrashManager
			}
		}
	}
	decision := h.decideFault(r)
	if decision == p1TraceCrashManager {
		h.mu.Lock()
		delete(h.faultScopes, r.PeerPID)
		h.mu.Unlock()
	}
	return decision
}

func (h *p1ProductHarness) startManager(restart bool) error {
	args := []string{"service"}
	if restart {
		args = append(args, "--restart-once")
	}
	p := h.f.launch(h.nextID("manager"), args, nil, h.decide)
	h.manager = p
	h.restart = restart
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		conn, err := service.DialEndpoint(ctx, h.f.cli.Paths())
		if err == nil {
			raw, e := conn.SyscallConn()
			var peer *syscall.Ucred
			var pe error
			if e == nil {
				e = raw.Control(func(fd uintptr) { peer, pe = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
			}
			closeErr := conn.Close()
			if e != nil || pe != nil || closeErr != nil || peer == nil || int(peer.Pid) != p.PID() || int(peer.Uid) != os.Geteuid() {
				return errP1Harness
			}
			return nil
		}
		select {
		case <-p.waitDone:
			return errP1Harness
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (h *p1ProductHarness) stopManager(ctx context.Context, killed bool) error {
	if h.manager == nil {
		if killed {
			return errP1Harness
		}
		return nil
	}
	p := h.manager
	select {
	case <-p.waitDone:
		if killed {
			return errP1Harness
		}
		return p.Wait(ctx)
	default:
	}
	signal := syscall.SIGTERM
	if killed {
		signal = syscall.SIGKILL
	}
	if err := p.cmd.Process.Signal(signal); err != nil {
		return errP1Harness
	}
	err := p.Wait(ctx)
	var exit *exec.ExitError
	if killed {
		if !errors.As(err, &exit) {
			return errP1Harness
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			return errP1Harness
		}
	} else if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 7) {
		return errP1Harness
	}
	ref, e := h.original("manager-exit", map[string]any{"kind": "owned-direct-manager-exit", "pid": p.PID(), "requested_signal": int(signal), "wait_status": p.cmd.ProcessState.Sys(), "trace_completed": p.TraceError() == nil})
	if e != nil {
		return e
	}
	h.extras = append(h.extras, ref)
	return nil
}

func (h *p1ProductHarness) bundleMap() (map[config.Generation]bundleSnapshot, error) {
	out := map[config.Generation]bundleSnapshot{}
	for key, f := range h.f.fixtures {
		_, err := h.f.store.ReadBundle(f.Generation())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, errP1Harness
		}
		b, err := p1ProjectBundle(h.f.store, f, fixedBundle(h.t, key).ExpectedNodes)
		if err != nil {
			return nil, err
		}
		out[b.Generation] = b
	}
	return out, nil
}

func (h *p1ProductHarness) operations() (map[string]operationRecord, map[config.Generation]bundleSnapshot, []originalRef, error) {
	bundles, err := h.bundleMap()
	if err != nil {
		return nil, nil, nil, err
	}
	ops, refs, err := p1ReadOperations(h.f.store, h.directory("operations"), bundles)
	if err != nil {
		return nil, nil, nil, err
	}
	for id, op := range ops {
		if old, ok := h.stable[id]; ok {
			check := old
			check.Original = op.Original
			if reflect.DeepEqual(check, op) {
				ops[id] = old
				continue
			}
		}
		h.stable[id] = op
	}
	return ops, bundles, refs, nil
}

func (h *p1ProductHarness) traceRecords(ctx context.Context) ([]p1TraceRecord, error) {
	for {
		var records []p1TraceRecord
		complete := true
		appendRecords := func(batch []p1TraceRecord) {
			for _, r := range batch {
				if r.Decision == "" {
					complete = false
				}
				records = append(records, r)
			}
		}
		// Preserve real stream chronology: local preparation, the remote
		// session manager, then any local cleanup/recovery manager. A remote
		// manager is not represented by the SSH client's child Wait status.
		for n := 0; n <= len(h.f.processes); n++ {
			if h.sessionDriver != nil && h.sessionDriver.Trace != nil && n == h.sessionTraceAt {
				p := h.sessionDriver.Trace
				if p.TraceError() != nil {
					return nil, errP1Harness
				}
				appendRecords(p.Records())
			}
			if n < len(h.f.processes) {
				p := h.f.processes[n]
				if p.TraceError() != nil {
					return nil, errP1Harness
				}
				appendRecords(p.Records())
			}
		}
		if complete {
			return records, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}

func (h *p1ProductHarness) call(ctx context.Context, i requestIdentity, params any) (operationResult, error) {
	raw := p1JSON(h.t, map[string]any{"api_version": "v" + i.Version, "request_id": i.ID, "operation": i.Name, "params": params})
	requestRef, err := p1WriteOriginal(filepath.Join(h.directory("request"), "request.json"), raw)
	if err != nil {
		return operationResult{}, err
	}
	reply, err := h.f.cli.Call(ctx, raw)
	h.extras = append(h.extras, requestRef)
	if reply.Original.Path != "" {
		h.extras = append(h.extras, reply.Original)
	}
	if h.manager != nil {
		select {
		case <-h.manager.waitDone:
			_ = h.manager.Wait(ctx) // also fence collection of the actual kill receipt
			if p1ObservedTraceCrash(h.manager) {
				return operationResult{}, errP1HarnessInterrupted
			}
		default:
		}
	}
	if err != nil {
		return operationResult{}, err
	}
	ops, bundles, _, err := h.operations()
	if err != nil {
		return operationResult{}, err
	}
	records, err := h.traceRecords(ctx)
	if err != nil {
		return operationResult{}, err
	}
	history, err := p1ReadTraceHistory(records, ops, bundles)
	if err != nil {
		return operationResult{}, err
	}
	result, err := p1ProjectResult(reply.RequestID, reply.Response, reply.Original, ops, history)
	if err != nil {
		return operationResult{}, err
	}
	result.Originals = append(result.Originals, requestRef)
	return result, nil
}

func (h *p1ProductHarness) Start(ctx context.Context, r startRequest) (operationResult, error) {
	if r.Fixture != verifiedFixture || r.BundleID != baselineBundle || r.AllowRestart && r.RestartLimit != 1 {
		return operationResult{}, errP1HarnessUnbuilt
	}
	if len(r.UnavailableComponents) != 0 {
		if !reflect.DeepEqual(r.UnavailableComponents, []string{"gost", "sqlite"}) {
			return operationResult{}, errP1HarnessUnbuilt
		}
		if _, err := h.optionalComponentEvidence(ctx); err != nil {
			return operationResult{}, err
		}
		h.optionalFixture = true
	}
	if r.AllowRestart != h.restart {
		if err := h.stopManager(ctx, false); err != nil {
			return operationResult{}, err
		}
		if err := h.startManager(r.AllowRestart); err != nil {
			return operationResult{}, err
		}
	}
	result, err := h.call(ctx, r.Identity, map[string]any{"expected_revision": r.Identity.BaseRevision, "control_only": false})
	if err == nil && result.Outcome == "committed" && h.frontend == nil {
		h.frontend, err = startP1Frontend(h.f.cli, h.directory("frontend"), h.nextID("frontend-status"))
		if err == nil {
			h.extras = append(h.extras, h.frontend.Begin())
		}
	}
	return result, err
}
func (h *p1ProductHarness) Stop(ctx context.Context, i requestIdentity) (operationResult, error) {
	return h.call(ctx, i, map[string]any{"expected_revision": i.BaseRevision, "accept_disruption": i.AcceptDisruption})
}
func (h *p1ProductHarness) ServiceStop(ctx context.Context, i requestIdentity) (operationResult, error) {
	return h.call(ctx, i, map[string]any{"expected_revision": i.BaseRevision})
}
func (h *p1ProductHarness) Recover(ctx context.Context, r recoverRequest) (operationResult, error) {
	active := h.f.active()
	target := ""
	if r.InterruptedRequestID == "" {
		if active.OperationID != nil {
			target = string(*active.OperationID)
		}
	} else {
		ops, _, _, err := h.operations()
		if err != nil {
			return operationResult{}, err
		}
		for id, op := range ops {
			if op.RequestID == r.InterruptedRequestID {
				target = id
			}
		}
	}
	if target == "" {
		return operationResult{}, errP1Harness
	}
	return h.call(ctx, r.Identity, map[string]any{"expected_revision": r.Identity.BaseRevision, "operation_id": target})
}
func (h *p1ProductHarness) RestartManager(ctx context.Context, mode string) error {
	if mode != "control-only" && mode != "managed" {
		return errP1HarnessUnbuilt
	}
	if h.manager != nil {
		select {
		case <-h.manager.waitDone:
		default:
			if err := h.stopManager(ctx, false); err != nil {
				return err
			}
		}
	}
	return h.startManager(false)
}
func (h *p1ProductHarness) KillManager(ctx context.Context) error { return h.stopManager(ctx, true) }
func (h *p1ProductHarness) ExitFrontend(ctx context.Context, mode string) error {
	if h.frontend == nil {
		return errP1Harness
	}
	ref, err := h.frontend.Finish(ctx, mode)
	if err == nil {
		h.extras = append(h.extras, ref)
	}
	return err
}
func (h *p1ProductHarness) KillCore(ctx context.Context, p processIdentity) error {
	h.mu.Lock()
	handle := h.f.handles[p.RunID]
	h.mu.Unlock()
	if handle == nil {
		return errP1Harness
	}
	i := handle.Identity()
	intent, _, err := h.f.store.ReadRunIntent(p.RunID)
	if err != nil || (p1TraceRun{intent: intent, identity: i}).process() != p {
		return errP1Harness
	}
	if err = handle.SignalVerified(ctx, syscall.SIGKILL); err != nil {
		return err
	}
	if err = handle.WaitExited(ctx); err != nil {
		return err
	}
	ref, err := h.original("core-fault", map[string]any{"kind": "supervisor-original-pidfd-sigkill", "identity": i, "run_id": p.RunID, "pidfd_exited": true})
	if err == nil {
		h.extras = append(h.extras, ref)
	}
	return err
}

// Remaining observation/evidence methods are implemented below. They do not
// call Recover, acquire a writer lease, or promote unknown state to false/zero.
func (h *p1ProductHarness) Observe(ctx context.Context) (productObservation, error) {
	o := productObservation{
		Generation: p1Missing[config.Generation]("no_fresh_runtime_observation"), Runtime: p1Missing[string]("status_unavailable"),
		CorePresent: p1Missing[bool]("ownership_unverified"), Core: p1Missing[processIdentity]("ownership_unverified"),
		ControllerOwnerProof: p1Missing[string]("ownership_unverified"), ManagerAvailable: p1Missing[bool]("manager_unverified"),
		EntryStates: p1Missing[map[string]string]("entry_state_unverified"), ActiveBundle: p1Missing[bundleSnapshot]("no_committed_bundle"),
		PendingOperation: p1Missing[string]("no_unique_pending_target"), PendingDesired: p1Missing[string]("no_unique_pending_target"),
		Bytes: observed[uint64]{State: "unavailable", Reason: "collector_not_implemented"}, Epoch: observed[config.CounterEpoch]{State: "unavailable", Reason: "collector_not_implemented"},
		UnavailableComponents: p1Missing[[]string]("component_inventory_not_exposed"),
	}
	if ctx == nil {
		return o, errP1Harness
	}
	if h.optionalFixture {
		var err error
		o.UnavailableComponents, err = h.optionalComponentEvidence(ctx)
		if err != nil {
			return o, err
		}
	}
	reply, err := h.f.cli.Call(ctx, p1Request(h.t, h.nextID("status"), "status.get", map[string]any{}))
	if err != nil || reply.Rejected {
		return o, errP1Harness
	}
	var wire struct {
		Data control.ServiceStatus `json:"data"`
	}
	if json.Unmarshal(reply.Response, &wire) != nil || wire.Data.ObservedAt.IsZero() {
		return o, errP1Harness
	}
	status := wire.Data
	a, err := h.f.store.ReadActive()
	if err != nil || status.Revision != a.Revision || status.CommittedRuntime != a.DesiredRuntime || !reflect.DeepEqual(status.Generation, a.Generation) {
		return o, errP1Harness
	}
	o.Revision = p1Fact(a.Revision, reply.Original)
	o.Desired = p1Fact(a.DesiredRuntime, reply.Original)
	o.Degraded = p1Fact(status.Degraded, reply.Original)
	o.Runtime = p1Fact(status.Core.State, reply.Original)
	operations, bundles, opRefs, err := h.operations()
	if err != nil {
		return o, err
	}
	if a.Generation != nil {
		bundle, ok := bundles[*a.Generation]
		if !ok || bundle.ManifestSHA256 != a.ManifestSHA256 {
			return o, errP1Harness
		}
		ref, e := h.original("bundle-observation", map[string]any{"kind": "readonly-persisted-bundle-projection", "active": a, "bundle": bundle})
		if e != nil {
			return o, e
		}
		o.ActiveBundle = p1Fact(bundle, ref)
	}
	var pending []operationRecord
	for _, op := range operations {
		if op.Phase != "committed" && op.Phase != "rolled_back" && op.Operation != "operation.recover" && op.Operation != "service.stop" {
			pending = append(pending, op)
		}
	}
	if len(pending) == 1 {
		o.PendingOperation = p1Fact(pending[0].ID, pending[0].Original)
		o.PendingDesired = p1Fact(pending[0].Desired, pending[0].Original)
	}
	// Manager presence is a direct-child wait + a fresh SO_PEERCRED observation,
	// independent of the read-only API's "service unavailable" projection.
	if h.manager != nil {
		done := false
		select {
		case <-h.manager.waitDone:
			done = true
		default:
		}
		probe, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, dialErr := service.DialEndpoint(probe, h.f.cli.Paths())
		cancel()
		if dialErr == nil {
			raw, e := conn.SyscallConn()
			var peer *syscall.Ucred
			var pe error
			if e == nil {
				e = raw.Control(func(fd uintptr) { peer, pe = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
			}
			closeErr := conn.Close()
			if done || e != nil || pe != nil || closeErr != nil || peer == nil || int(peer.Pid) != h.manager.PID() || int(peer.Uid) != os.Geteuid() {
				return o, errP1Harness
			}
			ref, e := h.original("manager-observation", map[string]any{"kind": "fresh-owned-endpoint-peer", "pid": peer.Pid, "uid": peer.Uid, "original_child_not_reaped": true})
			if e != nil {
				return o, e
			}
			o.ManagerAvailable = p1Fact(true, ref)
		} else if done {
			ref, e := h.original("manager-observation", map[string]any{"kind": "original-manager-reaped-endpoint-unavailable", "pid": h.manager.PID(), "wait_status": h.manager.cmd.ProcessState.Sys()})
			if e != nil {
				return o, e
			}
			o.ManagerAvailable = p1Fact(false, ref)
		}
	}
	ids, err := h.f.store.ListRunIDs()
	if err != nil {
		return o, err
	}
	var live []p1RunObservation
	var exits []map[string]any
	allClassified := true
	for _, id := range ids {
		h.mu.Lock()
		handle := h.f.handles[id]
		h.mu.Unlock()
		if handle != nil {
			probe, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			exited := handle.WaitExited(probe) == nil
			cancel()
			if exited {
				exits = append(exits, map[string]any{"run_id": id, "identity": handle.Identity(), "original_pidfd_exited": true})
				continue
			}
		}
		fresh, e := p1ObserveRun(ctx, h.f.store, id, h.directory("owner"))
		if e != nil {
			allClassified = false
			continue
		}
		live = append(live, fresh)
	}
	if len(live) == 1 && allClassified {
		fresh := live[0]
		o.CorePresent = p1Fact(true, fresh.Original)
		o.Core = p1Fact(fresh.Identity, fresh.Original)
		o.Generation = p1Fact(fresh.Observation.Generation, fresh.Original)
		o.ControllerOwnerProof = p1Fact(fresh.Original.SHA256, fresh.Original)
		bundle, ok := bundles[fresh.Observation.Generation]
		if !ok || bundle.ConfigSHA256 != fresh.Observation.ConfigSHA256 {
			return o, errP1Harness
		}
		entries := map[string]string{}
		for _, name := range bundle.Entries {
			state := "closed"
			if fresh.Observation.Gate == "open" {
				state = "ready"
			}
			entries[name] = state
		}
		o.EntryStates = p1Fact(entries, fresh.Original)
		if status.Core.State == "verified-running" && (status.Core.RunID == nil || *status.Core.RunID != fresh.Identity.RunID || status.Core.PID == nil || *status.Core.PID != fresh.Identity.PID || status.Core.BootID == nil || *status.Core.BootID != fresh.Identity.BootID || status.Core.StartTicks == nil || strconv.FormatUint(*status.Core.StartTicks, 10) != fresh.Identity.OSStartIdentity) {
			return o, errP1Harness
		}
	} else if len(live) == 0 && allClassified {
		ref, e := h.original("managed-exit-observation", map[string]any{"kind": "complete-managed-run-inventory-exit-observation", "run_ids": ids, "original_pidfds": exits, "operation_sources": opRefs})
		if e != nil {
			return o, e
		}
		o.CorePresent = p1Fact(false, ref)
		if status.Core.State == "verified-running" {
			return o, errP1Harness
		}
	}
	return o, nil
}

func (h *p1ProductHarness) Evidence(ctx context.Context) (observed[productEvidence], error) {
	h.mu.Lock()
	observerErr := h.observerErr
	h.mu.Unlock()
	if observerErr != nil {
		return p1Missing[productEvidence]("observer_failed"), observerErr
	}
	ops, bundles, refs, err := h.operations()
	if err != nil {
		return p1Missing[productEvidence]("operation_read_failed"), err
	}
	records, err := h.traceRecords(ctx)
	if err != nil {
		return p1Missing[productEvidence]("trace_incomplete"), err
	}
	if err = h.captureUnlaunchedIntents(records); err != nil {
		return p1Missing[productEvidence]("unlaunched_intent_unverified"), err
	}
	if err = h.captureLaunchProofs(ctx, records); err != nil {
		return p1Missing[productEvidence]("launch_incarnation_unverified"), err
	}
	projection, err := p1ProjectTrace(records, ops, bundles, h.launchProofs...)
	if err != nil {
		return p1Missing[productEvidence]("trace_projection_failed"), err
	}
	dir := h.directory("evidence")
	durable := p1ArchiveTree(h.t, h.f.cli.Paths(), dir, "snapshot")
	e := productEvidence{Sessions: append([]sessionRecord(nil), h.sessions...), FixtureSeeds: projection.FixtureSeeds, Operations: ops, DurableFiles: durable, OperationHistory: projection.History, Boundaries: projection.Boundaries, Launches: projection.Launches, Signals: projection.Signals}
	e.FaultHits, err = h.faultEvidence(records)
	if err != nil {
		return p1Missing[productEvidence]("fault_evidence_incomplete"), err
	}
	if h.capacityFault != nil {
		e.FaultHits = append(e.FaultHits, *h.capacityFault)
	}
	if h.ownershipFault != nil {
		e.FaultHits = append(e.FaultHits, *h.ownershipFault)
	}
	if h.foreignListener != nil {
		p, ref, observeErr := h.foreignListener.Observe(ctx)
		if observeErr != nil {
			return p1Missing[productEvidence]("foreign_fixture_not_proven_alive"), observeErr
		}
		// This process is a test-owned impostor, with no product RunID or
		// generation. A fresh accepted TCP connection proves it remains alive.
		e.ForeignProcesses = []processIdentity{{PID: p.PID, BootID: p.BootID, OSStartIdentity: strconv.FormatUint(p.StartTicks, 10), BinaryPath: p.BinaryPath, BinarySHA256: p.BinarySHA256, LaunchNonceSHA256: p.NonceSHA256, PrivateWorkdir: p.Workdir}}
		refs = append(refs, ref, h.foreignListener.Begin())
	}
	ref, err := p1WriteOriginal(filepath.Join(dir, "trace.json"), p1JSON(h.t, records))
	if err != nil {
		return p1Missing[productEvidence]("capture_failed"), err
	}
	refs = append(refs, ref)
	ref, err = p1WriteOriginal(filepath.Join(dir, "evidence.json"), p1JSON(h.t, e))
	if err != nil {
		return p1Missing[productEvidence]("capture_failed"), err
	}
	refs = append(refs, ref)
	refs = append(refs, h.extras...)
	if h.sessionDriver != nil {
		refs = append(refs, h.sessionDriver.Originals()...)
	}
	return p1Fact(e, refs...), nil
}

func (h *p1ProductHarness) Traffic(ctx context.Context, r trafficRequest) (trafficObservation, error) {
	// Continuation keeps the connection's original binding even if configuration
	// later changes. The traffic component rejects a mismatched selector itself.
	if r.Kind == "continue-held" {
		return h.traffic.Exchange(ctx, r, h.held[r.ConnectionID], nil)
	}
	o, err := h.Observe(ctx)
	if err != nil || o.Core.Value == nil || o.Core.State != "observed" {
		return p1TrafficUnknown(r.ConnectionID), errP1Harness
	}
	p := *o.Core.Value
	h.mu.Lock()
	handle := h.f.handles[p.RunID]
	h.mu.Unlock()
	if handle == nil {
		fresh, e := h.f.observe(p.RunID)
		if e != nil {
			return p1TrafficUnknown(r.ConnectionID), e
		}
		h.mu.Lock()
		if h.f.handles[p.RunID] == nil {
			h.f.handles[p.RunID] = fresh
			handle = fresh
		} else {
			handle = h.f.handles[p.RunID]
			fresh.Close()
		}
		h.mu.Unlock()
	}
	got, err := h.traffic.Exchange(ctx, r, p.RunID, handle)
	if err == nil && r.Kind == "open-held" && got.CoreRunID.State == "observed" && got.CoreRunID.Value != nil {
		h.held[r.ConnectionID] = *got.CoreRunID.Value
	}
	return got, err
}

func (h *p1ProductHarness) close() {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return
	}
	h.closing = true
	blockers := append([]net.Listener(nil), h.blockers...)
	h.mu.Unlock()
	defer func() {
		if h.foreignListener != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := h.foreignListener.Close(ctx); err != nil {
				h.t.Error("owned foreign listener cleanup", err)
			}
		}
	}()
	for _, blocker := range blockers {
		if err := blocker.Close(); err != nil {
			h.t.Error("owned bind blocker cleanup", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if h.frontend != nil {
		if err := h.frontend.Close(ctx); err != nil {
			h.t.Error("frontend cleanup", err)
		}
	}
	if h.sessionDriver != nil {
		if err := h.sessionDriver.Close(ctx); err != nil {
			h.t.Error("owned session fixture cleanup", err)
			return
		}
		if h.manager == nil {
			if err := h.startManager(false); err != nil {
				h.t.Error("post-session cleanup manager", err)
				return
			}
		}
	}
	if h.traffic != nil {
		if err := h.traffic.Close(); err != nil {
			h.t.Error("traffic cleanup", err)
		}
	}
	// Teardown uses explicit product Stop and retains its operation/trace. The
	// final shared cleanup still reports any necessary emergency pidfd signals.
	if h.manager != nil {
		select {
		case <-h.manager.waitDone:
			if err := h.startManager(false); err != nil {
				h.t.Error("cleanup manager", err)
				return
			}
		default:
		}
	}
	a, err := h.f.store.ReadActive()
	if err != nil {
		h.t.Error(err)
		return
	}
	// A test that deliberately killed every known core may leave the committed
	// intent running. Teardown must not manufacture a new failed Stop operation
	// merely to clean up an already exited original process. Skip Stop only on
	// a complete inventory of retained original pidfd exit observations.
	ids, inventoryErr := h.f.store.ListRunIDs()
	allExited := inventoryErr == nil
	var exitedRuns []map[string]any
	for _, id := range ids {
		h.mu.Lock()
		handle := h.f.handles[id]
		h.mu.Unlock()
		if handle == nil {
			allExited = false
			continue
		}
		probe, done := context.WithTimeout(ctx, 20*time.Millisecond)
		exited := handle.WaitExited(probe) == nil
		done()
		if !exited {
			allExited = false
		} else {
			exitedRuns = append(exitedRuns, map[string]any{"run_id": id, "identity": handle.Identity(), "original_pidfd_exited": true})
		}
	}
	if allExited {
		ref, e := h.original("teardown-exited-inventory", map[string]any{"kind": "complete-original-pidfd-exit-inventory", "run_ids": ids, "exits": exitedRuns, "product_stop_required": false})
		if e != nil {
			h.t.Error(e)
		} else {
			h.extras = append(h.extras, ref)
		}
	}
	// A retained pending/degraded operation deliberately blocks ordinary Stop.
	// Preserve that diagnosis; the separate fixture cleanup owns and records
	// any final pidfd signal after the product evidence has been archived.
	var pendingCleanup []string
	ops, _, _, readErr := h.operations()
	if readErr != nil {
		h.t.Error("teardown operation inventory", readErr)
		return
	}
	// Capacity fixtures deliberately retain real Prepared metadata. Do not try
	// to admit a cleanup operation into a full store or rewrite those records.
	if h.capacityFault != nil {
		ref, e := h.original("teardown-capacity-fixture", map[string]any{"kind": "preserve-full-operation-store-before-owned-fixture-cleanup", "operation_count": len(ops), "capacity_fault": h.capacityFault.Original, "product_stop_attempted": false, "fixture_cleanup_report": filepath.Join(h.f.artifacts, "cleanup.json")})
		if e != nil {
			h.t.Error(e)
		} else {
			h.extras = append(h.extras, ref)
		}
		if err = h.stopManager(ctx, false); err != nil {
			h.t.Error("capacity fixture manager cleanup", err)
		}
		for n, p := range h.f.processes {
			h.f.archiveTrace(fmt.Sprintf("complete-process-%02d", n), p)
		}
		if _, err = h.Evidence(ctx); err != nil {
			h.t.Error("complete capacity fixture evidence", err)
		}
		return
	}
	for id, op := range ops {
		if op.Phase != "committed" && op.Phase != "rolled_back" && op.Operation != "service.stop" {
			pendingCleanup = append(pendingCleanup, id)
		}
	}
	if (len(pendingCleanup) != 0 || h.ownershipFault != nil) && !allExited {
		ref, e := h.original("teardown-pending-operation", map[string]any{"kind": "preserve-product-state-before-owned-fixture-cleanup", "pending_operations": pendingCleanup, "ownership_fixture": h.ownershipFault, "run_ids": ids, "product_stop_attempted": false, "fixture_cleanup_report": filepath.Join(h.f.artifacts, "cleanup.json")})
		if e != nil {
			h.t.Error(e)
		} else {
			h.extras = append(h.extras, ref)
		}
	}
	if a.DesiredRuntime == "running" && !allExited && len(pendingCleanup) == 0 && h.ownershipFault == nil {
		r, err := h.Stop(ctx, request("runtime.stop", h.nextID("cleanup-stop"), a.Revision))
		if err != nil || r.Outcome != "committed" {
			h.t.Error("product teardown stop incomplete", err)
		}
	}
	a, err = h.f.store.ReadActive()
	if err == nil {
		_, err = h.call(ctx, request("service.stop", h.nextID("cleanup-service-stop"), a.Revision), map[string]any{"expected_revision": a.Revision})
	}
	if err != nil {
		h.t.Error("product teardown service stop", err)
	} else if h.manager != nil {
		if err = h.manager.Wait(ctx); err != nil {
			h.t.Error("service teardown wait", err)
		}
	}
	for n, p := range h.f.processes {
		h.f.archiveTrace(fmt.Sprintf("complete-process-%02d", n), p)
	}
	if _, err = h.Evidence(ctx); err != nil {
		h.t.Error("complete teardown evidence", err)
	}
}

var _ productHarness = (*p1ProductHarness)(nil)
