//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

// Fixed approved core, precompiled fixture material and explicit durable cuts.
// This module test is not a CLI/service, full Intent compiler or VM acceptance.
func TestLifecycleCoreStopStartRecover(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit approved fixed core required")
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("unexpected approved core")
	}
	private := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(private, "mihomo")
	if e := os.WriteFile(binary, raw, 0755); e != nil {
		t.Fatal(e)
	}
	endpoints := []string{}
	listeners := []*net.TCPListener{}
	for i := 0; i < 2; i++ {
		l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		listeners = append(listeners, l)
		endpoints = append(endpoints, l.Addr().String())
	}
	for _, l := range listeners {
		l.Close()
	}
	seed, material := applyCoreMaterial(t, binary, endpoints[0], endpoints[1], "lifecycle-fixed-generation")
	impact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(material.Manifest.Generation), ConnectionEffect: "unknown", RuntimeAction: "start"}
	capability, e := mihomo.ApprovedBasicCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	capSHA, e := capability.Digest()
	if e != nil {
		t.Fatal(e)
	}
	current := CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Inputs: []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: capability.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}}, Impact: impact}
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(seed.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	validator := planFixtureValidator(t, obj)
	paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil })
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer applyCoreCleanup(t, c.store, binary, handles)
	var sourceCalls atomic.Int32
	a, e := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		if sourceCalls.Add(1) != 1 {
			t.Fatal("Stop/Start called compiler Source")
		}
		return material, nil
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	var echoes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echoes.Add(1)
		io.WriteString(w, "lifecycle:"+r.URL.Path)
	}))
	defer server.Close()
	target := strings.TrimPrefix(server.URL, "http://")
	echo := func(phase string) {
		t.Helper()
		status, body := applyCoreHTTP(t, endpoints[1], target, phase, true)
		if status != 200 || body != "lifecycle:/"+phase {
			t.Fatal("same-port echo failed", phase, status)
		}
	}
	active := func() config.ActivePointer {
		t.Helper()
		v, e := c.store.ReadActive()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	assertTransition := func(old, next config.ActivePointer, desired string) {
		t.Helper()
		if old.Revision == next.Revision || next.DesiredRuntime != desired || next.Generation == nil || *next.Generation != material.Manifest.Generation || next.ManifestSHA256 != old.ManifestSHA256 {
			t.Fatal("lifecycle revision/generation/bundle transition")
		}
	}
	exited := func(h *platform.ProcessHandle, label string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := h.WaitExited(ctx); e != nil {
			t.Fatal(label, "original pidfd exit", e)
		}
		if e := applyCoreAbsent(ctx, h.Identity().PID); e != nil {
			t.Fatal(label, "child reaping not observed", e)
		}
		t.Logf("%s original pidfd exited and process absent pid=%d", label, h.Identity().PID)
	}
	noNewRuns := func(want []config.RunID) {
		t.Helper()
		got, e := c.store.ListRunIDs()
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("replay/recovery spawned", got, want, e)
		}
	}
	base := active()
	plan := applyCorePlan(t, seed, base.Revision, impact)
	applyRaw := coordinatorRequest(t, "lifecycle-core-apply", plan, nil)
	initial, made, e := a.Apply(applyCoreContext(t), applyRaw)
	if e != nil || !made || initial.Record.Phase != Committed {
		t.Fatal("initial public Apply", made, e)
	}
	firstID := applyCoreRunID(t, initial)
	first, firstIdentity := applyCoreHandle(t, c.store, firstID)
	handles[firstID] = first
	echo("initial")
	firstIntent, _, e := c.store.ReadRunIntent(firstID)
	if e != nil {
		t.Fatal(e)
	}
	originalConfig, e := os.ReadFile(filepath.Join(firstIntent.Workdir, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	originalLog, e := os.ReadFile(filepath.Join(firstIntent.Workdir, "launcher.log"))
	if e != nil {
		t.Fatal(e)
	}
	beforeStop := active()
	stopRaw := lifecycleRequest(t, "lifecycle-core-stop", "runtime.stop", beforeStop.Revision)
	stopped, made, e := a.Stop(applyCoreContext(t), stopRaw)
	if e != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("public Stop", made, e)
	}
	exited(first, "first stop")
	stoppedPointer := active()
	assertTransition(beforeStop, stoppedPointer, "stopped")
	stopState, e := decodeLifecycleEvidence(stopped)
	if e != nil || !stopState.SourceStopped || c.verifyLifecycleExit(stopState) != nil {
		t.Fatal("Stop missing immutable exit originals", e)
	}
	if conn, e := net.DialTimeout("tcp4", endpoints[1], 100*time.Millisecond); e == nil {
		conn.Close()
		t.Fatal("stopped entry accepts")
	}
	lifecycleCoreArchive(t, paths, "after-stop")
	before := coordinatorTree(t, paths)
	again, made, e := a.Stop(nil, stopRaw)
	if e != nil || made || again.SHA256 != stopped.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("Stop retry effects", e)
	}
	startRaw := lifecycleRequest(t, "lifecycle-core-start", "runtime.start", stoppedPointer.Revision)
	started, made, e := a.Start(applyCoreContext(t), startRaw)
	if e != nil || !made || started.Record.Phase != Committed {
		t.Fatal("public Start", made, e)
	}
	state, e := decodeLifecycleEvidence(started)
	if e != nil || state.TargetRunID == nil {
		t.Fatal(e)
	}
	secondID := *state.TargetRunID
	second, secondIdentity := applyCoreHandle(t, c.store, secondID)
	handles[secondID] = second
	if firstIdentity.PID == secondIdentity.PID || firstID == secondID {
		t.Fatal("Start reused old instance")
	}
	assertTransition(stoppedPointer, active(), "running")
	echo("started")
	before = coordinatorTree(t, paths)
	again, made, e = a.Start(nil, startRaw)
	if e != nil || made || again.SHA256 != started.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("Start retry effects", e)
	}
	// Test-only cut after the same admission guards and durable stop intent used
	// by public Stop. Recover itself is the public path and performs real TERM.
	stop2, stop2State, stop2Raw := lifecycleCoreAdmit(t, c, "lifecycle-core-stop-cut", "stop", secondID)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cut := func(step string) error {
		if step == "stop-intent-durable" {
			return errors.New("test durable cut")
		}
		return nil
	}
	cutStop, e := c.executeStop(ctx, stop2, stop2State, &basicRuntime{store: c.store}, cut)
	cancel()
	if !errors.Is(e, errRecoveryCut) || cutStop.Record.Phase != Validated {
		t.Fatal("stop durable cut", e)
	}
	// A cut is not a stop: independently prove this exact core still forwards.
	if e := second.VerifyArguments(applyCoreContext(t), func() []string {
		i, _, e := c.store.ReadRunIntent(secondID)
		if e != nil {
			t.Fatal(e)
		}
		return i.Arguments
	}()); e != nil {
		t.Fatal(e)
	}
	echo("stop-cut-live")
	lifecycleCoreArchive(t, paths, "before-stop-recover")
	recoverStopRaw := recoveryRequest(t, "lifecycle-core-recover-stop", stop2.Record.Identity.OperationID, active().Revision)
	recovered, made, e := a.Recover(applyCoreContext(t), recoverStopRaw)
	if e != nil || !made || recovered.Record.Phase != Committed {
		t.Fatal("public Recover stop", made, e)
	}
	exited(second, "recovered stop")
	stopped2 := active()
	assertTransition(stop2State.Base, stopped2, "stopped")
	originalStop, e := c.journal.Get(stop2.Record.Identity.OperationID)
	if e != nil || originalStop.Record.Phase != Committed {
		t.Fatal(e)
	}
	runs, e := c.store.ListRunIDs()
	if e != nil {
		t.Fatal(e)
	}
	before = coordinatorTree(t, paths)
	again, made, e = a.Stop(nil, stop2Raw)
	if e != nil || made || again.SHA256 != originalStop.SHA256 {
		t.Fatal("original stop replay", e)
	}
	again, made, e = a.Recover(nil, recoverStopRaw)
	if e != nil || made || again.SHA256 != recovered.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("recover stop replay", e)
	}
	noNewRuns(runs)
	// Start is cut after the one real spawn but before registration. No fake
	// registration is written; the public Recover must prove/register this core.
	start2, start2State, start2Raw := lifecycleCoreAdmit(t, c, "lifecycle-core-start-cut", "start", secondID)
	runtime := &basicRuntime{store: c.store, entryArgs: applyCoreEntryArgs()}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
	cut = func(step string) error {
		if step == "spawn-returned-before-registration" {
			return errors.New("test spawn cut")
		}
		return nil
	}
	cutStart, e := c.executeStart(ctx, start2, start2State, runtime.restore, cut)
	cancel()
	if !errors.Is(e, errRecoveryCut) {
		t.Fatal("start spawn cut", e)
	}
	lastState, e := decodeLifecycleEvidence(cutStart)
	if e != nil || lastState.TargetRunID == nil {
		t.Fatal(e)
	}
	thirdID := *lastState.TargetRunID
	third, thirdIdentity := lifecycleCoreClosedHandle(t, c.store, thirdID)
	handles[thirdID] = third
	if thirdID == secondID || thirdID == firstID || thirdIdentity.PID == secondIdentity.PID || thirdIdentity.PID == firstIdentity.PID {
		t.Fatal("fresh Start reused identity")
	}
	if _, _, e := c.store.ReadRunRegistration(thirdID, start2.Record.Identity.OperationID); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("spawn cut unexpectedly has registration", e)
	}
	if conn, e := net.DialTimeout("tcp4", endpoints[1], 100*time.Millisecond); e == nil {
		conn.Close()
		t.Fatal("closed start forwarded before recover")
	}
	lifecycleCoreArchive(t, paths, "before-start-recover")
	runs, e = c.store.ListRunIDs()
	if e != nil {
		t.Fatal(e)
	}
	recoverStartRaw := recoveryRequest(t, "lifecycle-core-recover-start", start2.Record.Identity.OperationID, active().Revision)
	recoveredStart, made, e := a.Recover(applyCoreContext(t), recoverStartRaw)
	if e != nil || !made || recoveredStart.Record.Phase != Committed {
		t.Fatal("public Recover closed start", made, e)
	}
	noNewRuns(runs)
	finalState := active()
	assertTransition(stopped2, finalState, "running")
	echo("recovered-start")
	proof, proofIdentity := applyCoreHandle(t, c.store, thirdID)
	proof.Close()
	if proofIdentity != thirdIdentity {
		t.Fatal("Recover changed original instance")
	}
	reg, _, e := c.store.ReadRunRegistration(thirdID, start2.Record.Identity.OperationID)
	if e != nil || reg.Gate != "closed" {
		t.Fatal("Recover did not publish closed registration", e)
	}
	thirdIntent, _, e := c.store.ReadRunIntent(thirdID)
	if e != nil || thirdIntent.Workdir == firstIntent.Workdir {
		t.Fatal("Start overwrote original workdir", e)
	}
	if got, e := os.ReadFile(filepath.Join(firstIntent.Workdir, "config.json")); e != nil || !reflect.DeepEqual(got, originalConfig) {
		t.Fatal("old config changed", e)
	}
	if got, e := os.ReadFile(filepath.Join(firstIntent.Workdir, "launcher.log")); e != nil || !reflect.DeepEqual(got, originalLog) {
		t.Fatal("old launcher log changed", e)
	}
	originalStart, e := c.journal.Get(start2.Record.Identity.OperationID)
	if e != nil || originalStart.Record.Phase != Committed {
		t.Fatal(e)
	}
	before = coordinatorTree(t, paths)
	again, made, e = a.Start(nil, start2Raw)
	if e != nil || made || again.SHA256 != originalStart.SHA256 {
		t.Fatal("original Start replay", e)
	}
	again, made, e = a.Recover(nil, recoverStartRaw)
	if e != nil || made || again.SHA256 != recoveredStart.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("Recover Start replay", e)
	}
	noNewRuns(runs)
	if sourceCalls.Load() != 1 || echoes.Load() != 4 {
		t.Fatal("unexpected compile/forwarding count", sourceCalls.Load(), echoes.Load())
	}
	lifecycleCoreArchive(t, paths, "after-start-recover")
	t.Logf("actual lifecycle: three core execs; first=%d second=%d third=%d; same generation=%s; Source calls=%d authorized echoes=%d; two original pidfds exited; final cleanup retains third original pidfd", firstIdentity.PID, secondIdentity.PID, thirdIdentity.PID, material.Manifest.Generation, sourceCalls.Load(), echoes.Load())
}

func lifecycleCoreAdmit(t *testing.T, c *Coordinator, id, mode string, source config.RunID) (StoredOperation, lifecycleEvidence, []byte) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	base, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	raw := lifecycleRequest(t, id, "runtime."+mode, base.Revision)
	request, e := ParseRequest(raw)
	if e != nil {
		t.Fatal(e)
	}
	got, e := c.runtimeAdmission(context.Background(), request)
	if e != nil || !reflect.DeepEqual(got, base) {
		t.Fatal(e)
	}
	state := lifecycleEvidence{Version: 1, Mode: mode, DesiredRuntime: "stopped", Base: base, SourceRunID: source, ExitOperationID: request.OperationID(), ExitGate: "open"}
	if mode == "start" {
		previous, e := c.verifiedStopped(base)
		if e != nil {
			t.Fatal(e)
		}
		state.DesiredRuntime = "running"
		state.SourceStopped = true
		state.SourceRunID = previous.SourceRunID
		state.ExitOperationID = *base.OperationID
	} else {
		actual, e := c.activeRunForApply(base, "replace", "")
		if e != nil || actual == nil || *actual != source {
			t.Fatal("stop source preflight", e)
		}
	}
	stored, made, e := c.admitLifecycle(request, state)
	if e != nil || !made {
		t.Fatal(made, e)
	}
	return stored, state, raw
}
func lifecycleCoreClosedHandle(t *testing.T, store *config.Store, id config.RunID) (*platform.ProcessHandle, platform.ProcessIdentity) {
	t.Helper()
	intent, _, e := store.ReadRunIntent(id)
	if e != nil {
		t.Fatal(e)
	}
	capability, e := mihomo.ApprovedBasicCapability(intent.Binary.Path)
	if e != nil {
		t.Fatal(e)
	}
	controller, e := mihomo.NewController(store, capability)
	if e != nil {
		t.Fatal(e)
	}
	ownership, e := owned.NewOwnership(store, controller)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var observed *owned.OwnedRun
	for {
		observed, e = ownership.Observe(ctx, id)
		if e == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("closed original ownership unavailable", e)
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer observed.Close()
	if observed.Observation().Gate != "closed" {
		t.Fatal("spawned start not closed")
	}
	birth, _, e := store.ReadRunBirth(id)
	if e != nil {
		t.Fatal(e)
	}
	h, e := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if e != nil {
		t.Fatal(e)
	}
	identity := h.Identity()
	if identity != observed.Identity() || identity.PID != birth.PID || identity.UID != birth.UID || identity.StartTicks != birth.StartTicks || identity.BootID != birth.BootID {
		h.Close()
		t.Fatal("closed original identity differs")
	}
	if e := h.VerifyArguments(ctx, intent.Arguments); e != nil {
		h.Close()
		t.Fatal(e)
	}
	t.Logf("independent closed original run=%s pid=%d start=%d boot=%s gate=closed (no registration)", id, identity.PID, identity.StartTicks, identity.BootID)
	return h, identity
}
func lifecycleCoreArchive(t *testing.T, p config.Paths, label string) {
	t.Helper()
	root := os.Getenv("HOOPA_LIFECYCLE_ARTIFACTS")
	if root == "" {
		return
	}
	directory := filepath.Join(root, label)
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"active.json", "operations", "runs"} {
		source := filepath.Join(p.State, rel)
		if _, e := os.Stat(source); os.IsNotExist(e) {
			continue
		}
		if e := filepath.WalkDir(source, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			relative, e := filepath.Rel(p.State, path)
			if e != nil {
				return e
			}
			target := filepath.Join(directory, relative)
			if d.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			return os.WriteFile(target, raw, 0600)
		}); e != nil {
			t.Fatal(e)
		}
	}
}
