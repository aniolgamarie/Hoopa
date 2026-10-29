//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// One explicitly authorized service-lifetime budget, two actual approved core
// processes. There is no background service, CLI policy or persisted auto-retry.
func TestRestartCoreOneAttemptBudget(t *testing.T) {
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
	running := active()
	watch, e := a.AuthorizeRestart(applyCoreContext(t), running.Revision)
	if e != nil {
		t.Fatal("explicit authorization", e)
	}
	defer watch.Close()
	copy := *watch
	if id, exit := watch.ExitObserved(); id != firstID || exit {
		t.Fatal("watch guessed live core exited")
	}
	// A canceled wait has no restart effects and does not signal the original.
	before := coordinatorTree(t, paths)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, made, e = watch.Attempt(ctx)
	cancel()
	if made || !errors.Is(e, context.DeadlineExceeded) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("live watch timeout effects", made, e)
	}
	echo("after-wait-timeout")
	// A Coordinator value copy and an applier copy share the single grant.
	coordinatorCopy := *c
	applierCopy := *a
	applierCopy.coordinator = &coordinatorCopy
	before = coordinatorTree(t, paths)
	if extra, err := applierCopy.AuthorizeRestart(applyCoreContext(t), running.Revision); extra != nil || !errors.Is(err, ErrRestartExhausted) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("duplicate grant through copy", err)
	}
	if e := first.SignalVerified(applyCoreContext(t), syscall.SIGKILL); e != nil {
		t.Fatal("task-owned original SIGKILL", e)
	}
	exited(first, "original killed")
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
	restarted, made, e := watch.Attempt(ctx)
	cancel()
	if e != nil || !made || restarted.Record.Phase != Committed {
		t.Fatal("single authorized restart", made, e)
	}
	state, e := decodeLifecycleEvidence(restarted)
	if e != nil || state.Mode != "restart" || state.TargetRunID == nil || restarted.Record.Identity.Intent.TargetRevision != nil {
		t.Fatal("restart changed desired revision contract", e)
	}
	secondID := *state.TargetRunID
	second, secondIdentity := applyCoreHandle(t, c.store, secondID)
	handles[secondID] = second
	if secondID == firstID || secondIdentity.PID == firstIdentity.PID || secondIdentity == firstIdentity {
		t.Fatal("restart reused instance")
	}
	now := active()
	if now.Revision != running.Revision || now.Generation == nil || *now.Generation != *running.Generation || now.ManifestSHA256 != running.ManifestSHA256 || now.DesiredRuntime != "running" || now.OperationID == nil || *now.OperationID != restarted.Record.Identity.OperationID {
		t.Fatal("automatic restart changed revision/generation/desired or lost current operation")
	}
	if id, exit := copy.ExitObserved(); id != secondID || exit {
		t.Fatal("value copy did not share successor observation")
	}
	echo("restart-successor")
	runs, e := c.store.ListRunIDs()
	if e != nil || len(runs) != 2 {
		t.Fatal("not exactly two actual run intents", runs, e)
	}
	lifecycleCoreArchive(t, paths, "restart-after-success")
	if e := second.SignalVerified(applyCoreContext(t), syscall.SIGKILL); e != nil {
		t.Fatal("successor task-owned SIGKILL", e)
	}
	exited(second, "successor killed")
	before = coordinatorTree(t, paths)
	_, made, e = copy.Attempt(applyCoreContext(t))
	if made || !errors.Is(e, ErrRestartExhausted) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("shared exhausted budget wrote/restarted", made, e)
	}
	after, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(after, runs) {
		t.Fatal("exhausted budget spawned", e)
	}
	if id, exit := watch.ExitObserved(); id != secondID || !exit {
		t.Fatal("exhaustion failed to retain specific successor exit")
	}
	if !reflect.DeepEqual(active(), now) || sourceCalls.Load() != 1 || echoes.Load() != 3 {
		t.Fatal("exhaustion changed desired state/revision or recompiled")
	}
	lifecycleCoreArchive(t, paths, "restart-after-exhaustion")
	t.Logf("actual restart budget: first pid=%d successor pid=%d; both original pidfds observed SIGKILL exit and proc absence; same revision=%s generation=%s; 2 run intents; one Source call; 3 authorized echoes; shared value copy exhausted with zero writes", firstIdentity.PID, secondIdentity.PID, running.Revision, *running.Generation)
}

// Separate one-core sample: Close interrupts an active wait without signaling;
// a fresh explicit service scope after reopen still yields to public Stop.
func TestRestartCoreCloseAndStopPriority(t *testing.T) {
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
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	// An independent read-only anchor remains open across writer reopen, so
	// even a failed reopen cannot remove the task's safe cleanup originals.
	cleanupStore, e := config.OpenStore(paths, false)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanupStore.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer applyCoreCleanup(t, cleanupStore, binary, handles)
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

	running := active()
	watch, e := a.AuthorizeRestart(applyCoreContext(t), running.Revision)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _, _, err := watch.Attempt(ctx); done <- err }()
	// The attempt token is held for the entire active attempt. Synchronize on
	// this existing mutex, rather than guessing that a sleeping goroutine waited.
	for {
		if watch.attempt.TryLock() {
			watch.attempt.Unlock()
		} else {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("attempt never entered")
		case <-time.After(time.Millisecond):
		}
	}
	before := coordinatorTree(t, paths)
	if e := watch.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("Close failed to interrupt active attempt", err)
		}
	case <-ctx.Done():
		t.Fatal("Close wait stuck")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("watch Close wrote files")
	}
	echo("after-close")
	if extra, err := a.AuthorizeRestart(applyCoreContext(t), running.Revision); extra != nil || !errors.Is(err, ErrRestartExhausted) {
		t.Fatal("Close renewed budget", err)
	}
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	before = coordinatorTree(t, paths)
	c, e = OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil })
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("reopen wrote authority or automatically restarted")
	}
	ids, e := c.store.ListRunIDs()
	if e != nil || len(ids) != 1 || ids[0] != firstID {
		t.Fatal("reopen spawned", e)
	}
	a, e = NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("Stop/reopen called Source")
		return BasicMaterial{}, ErrPlan
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	watch, e = a.AuthorizeRestart(applyCoreContext(t), running.Revision)
	if e != nil {
		t.Fatal("new explicit grant after reopen", e)
	}
	defer watch.Close()
	observed, identity := applyCoreHandle(t, c.store, firstID)
	observed.Close()
	if identity != firstIdentity {
		t.Fatal("reopen changed core")
	}
	stopRaw := lifecycleRequest(t, "watch-priority-stop", "runtime.stop", running.Revision)
	stopped, made, e := a.Stop(applyCoreContext(t), stopRaw)
	if e != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("Stop priority", made, e)
	}
	exited(first, "explicit Stop")
	stoppedPointer := active()
	if stoppedPointer.DesiredRuntime != "stopped" || stoppedPointer.Revision == running.Revision || *stoppedPointer.Generation != *running.Generation {
		t.Fatal("stop pointer")
	}
	before = coordinatorTree(t, paths)
	_, made, e = watch.Attempt(applyCoreContext(t))
	if made || !errors.Is(e, ErrConflict) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("watch reversed explicit Stop", made, e)
	}
	after, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, after) || !reflect.DeepEqual(active(), stoppedPointer) {
		t.Fatal("stop priority spawned or changed revision", e)
	}
	if id, exit := watch.ExitObserved(); id != firstID || !exit {
		t.Fatal("Stop exit not specific to watched run")
	}
	lifecycleCoreArchive(t, paths, "restart-close-stop-priority")
	t.Logf("actual separate Close/Stop priority: one core pid=%d RunID=%s; active wait interrupted; reopen no spawn; fresh explicit scope yielded to durable Stop; original pidfd exit/proc absence proved; Source calls=%d echoes=%d", firstIdentity.PID, firstID, sourceCalls.Load(), echoes.Load())
}
