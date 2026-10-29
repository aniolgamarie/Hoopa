//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
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
	"testing"
)

// One actual fixed approved core with trusted precompiled fixture inputs, not
// a general compiler or service bootstrap. Public Recover receives no retained
// oracle handle and must reconstruct fresh proof after writer close/reopen.
func TestManagedStartCoreAdoptRecoverSameOriginal(t *testing.T) {
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

	protected := managedProtectedTree(t, paths)
	runs, e := c.store.ListRunIDs()
	if e != nil || len(runs) != 1 {
		t.Fatal("initial run inventory", e)
	}
	initialSHA := initial.SHA256
	beforeActive := active()
	managedCoreArchive(t, paths, "before-adoption")
	rawStart := lifecycleRequest(t, "actual-managed-adopt", "runtime.start", beforeActive.Revision)
	adopted, made, e := a.EnsureStarted(applyCoreContext(t), rawStart)
	if e != nil || !made || adopted.Record.Phase != Committed {
		t.Fatal("public EnsureStarted adoption", made, e)
	}
	state, e := decodeAdoption(adopted)
	if e != nil || state.Identity != firstIdentity || state.RunID != firstID || !reflect.DeepEqual(state.Base, beforeActive) || adopted.Record.Identity.Intent.TargetRevision != nil {
		t.Fatal("adoption did not bind full original", e)
	}
	if !reflect.DeepEqual(protected, managedProtectedTree(t, paths)) || !reflect.DeepEqual(beforeActive, active()) {
		t.Fatal("adoption rewrote persistent/runtime originals")
	}
	old, e := c.journal.Get(initial.Record.Identity.OperationID)
	if e != nil || old.SHA256 != initialSHA {
		t.Fatal("adoption rewrote original Apply", e)
	}
	ids, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, runs) {
		t.Fatal("adoption spawned or added run", e)
	}
	checked, identity := applyCoreHandle(t, c.store, firstID)
	checked.Close()
	if identity != firstIdentity {
		t.Fatal("adoption changed identity")
	}
	echo("after-adopt")
	managedCoreArchive(t, paths, "after-adoption")
	// This is a writer reopen, not a manager OS-death sample. The original oracle
	// remains only for independent identity and task-owned cleanup, never recovery.
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	if e := first.VerifyArguments(applyCoreContext(t), func() []string {
		i, _, e := cleanupStore.ReadRunIntent(firstID)
		if e != nil {
			t.Fatal(e)
		}
		return i.Arguments
	}()); e != nil {
		t.Fatal("Close killed/changed core", e)
	}
	echo("after-writer-close")
	c, e = OpenCoordinator(paths, validator, func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil })
	if e != nil {
		t.Fatal(e)
	}
	a, e = NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("adoption/recovery called Source")
		return BasicMaterial{}, ErrPlan
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(protected, managedProtectedTree(t, paths)) {
		t.Fatal("reopen changed authority")
	}
	recoverRaw := recoveryRequest(t, "actual-managed-recover", adopted.Record.Identity.OperationID, active().Revision)
	recovered, made, e := a.Recover(applyCoreContext(t), recoverRaw)
	if e != nil || !made || recovered.Record.Phase != Committed {
		t.Fatal("public adoption Recover after reopen", made, e)
	}
	same, e := c.journal.Get(adopted.Record.Identity.OperationID)
	if e != nil || same.SHA256 != adopted.SHA256 {
		t.Fatal("terminal adoption changed during reproof", e)
	}
	checked, identity = applyCoreHandle(t, c.store, firstID)
	checked.Close()
	if identity != firstIdentity {
		t.Fatal("Recover changed original full identity")
	}
	echo("after-recover")
	ids, e = c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, runs) || !reflect.DeepEqual(protected, managedProtectedTree(t, paths)) || !reflect.DeepEqual(beforeActive, active()) {
		t.Fatal("Recover altered run/registration/active/revision", e)
	}
	before := coordinatorTree(t, paths)
	again, made, e := a.EnsureStarted(nil, rawStart)
	if e != nil || made || again.SHA256 != adopted.SHA256 {
		t.Fatal("managed request replay", e)
	}
	again, made, e = a.Recover(nil, recoverRaw)
	if e != nil || made || again.SHA256 != recovered.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("recover replay", e)
	}
	if sourceCalls.Load() != 1 || echoes.Load() != 4 {
		t.Fatal("unexpected Source/forwarding count", sourceCalls.Load(), echoes.Load())
	}
	managedCoreArchive(t, paths, "after-recovery-and-replay")
	t.Logf("actual managed adoption: one core pid=%d start=%d boot=%s RunID=%s; original full identity unchanged; Source1; 4 authorized echoes; EnsureStarted/Recover exact replay; no active/run/registration rewrite; final cleanup retains original pidfd", firstIdentity.PID, firstIdentity.StartTicks, firstIdentity.BootID, firstID)
}
func managedCoreArchive(t *testing.T, p config.Paths, label string) {
	t.Helper()
	root := os.Getenv("HOOPA_MANAGED_START_ARTIFACTS")
	if root == "" {
		return
	}
	dest := filepath.Join(root, label)
	if e := os.MkdirAll(dest, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"active.json", "operations", "runs", "bundles"} {
		source := filepath.Join(p.State, name)
		if e := filepath.WalkDir(source, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(p.State, path)
			if e != nil {
				return e
			}
			target := filepath.Join(dest, rel)
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
	// Workdir originals may contain credentials: retain only private files, never
	// print their content. They let review compare the exact launch input bytes.
	runs := filepath.Join(p.State, "runs")
	entries, e := os.ReadDir(runs)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		raw, e := os.ReadFile(filepath.Join(runs, entry.Name(), "intent.json"))
		if e != nil {
			t.Fatal(e)
		}
		var intent config.RunIntent
		if e := json.Unmarshal(raw, &intent); e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(dest, "workdirs", entry.Name())
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"config.json", "launcher.log"} {
			raw, e := os.ReadFile(filepath.Join(intent.Workdir, name))
			if e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
}
