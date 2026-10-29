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
	"testing"
)

// Real normal runtime.start terminal target, then a task-owned missing immutable
// registration. This is one F14-related path, not the full product matrix.
func TestRecoveryCoreTerminalOwnershipConflict(t *testing.T) {
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
	home := filepath.Join(t.TempDir(), "home")
	paths, e := config.ResolvePaths(home, nil)
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

	base = active()
	stopped, made, e := a.Stop(applyCoreContext(t), lifecycleRequest(t, "preflight-core-stop", "runtime.stop", base.Revision))
	if e != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("seed stop", made, e)
	}
	started, made, e := a.Start(applyCoreContext(t), lifecycleRequest(t, "preflight-core-start", "runtime.start", active().Revision))
	if e != nil || !made || started.Record.Phase != Committed {
		t.Fatal("seed start", made, e)
	}
	state, e := decodeLifecycleEvidence(started)
	if e != nil || state.TargetRunID == nil {
		t.Fatal(e)
	}
	secondID := *state.TargetRunID
	second, secondIdentity := applyCoreHandle(t, c.store, secondID)
	handles[secondID] = second
	echo("normal-start")
	targetSHA := started.SHA256
	beforeActive := active()
	original := filepath.Join(paths.State, "runs", hash([]byte(secondID)), "registration-"+hash([]byte(started.Record.Identity.OperationID))+".json")
	held := filepath.Join(t.TempDir(), "held-registration.json")
	if e := os.Rename(original, held); e != nil {
		t.Fatal(e)
	}
	restored := false
	defer func() {
		if !restored {
			if e := os.Rename(held, original); e != nil {
				t.Error("restore task registration", e)
			}
		}
	}()
	recoverRaw := recoveryRequest(t, "actual-preflight-recover", started.Record.Identity.OperationID, beforeActive.Revision)
	before := coordinatorTree(t, paths)
	rejected, made, e := a.Recover(applyCoreContext(t), recoverRaw)
	if made || !errors.Is(e, ErrConflict) || len(rejected.Record.Result) != 0 {
		t.Fatal("preexisting missing registration admitted recovery", made, e)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("rejected preflight mutated protected tree")
	}
	if _, e := c.Lookup(recoverRaw); !errors.Is(e, ErrOperationNotFound) {
		t.Fatal("rejected request acquired durable identity", e)
	}
	if !reflect.DeepEqual(beforeActive, active()) {
		t.Fatal("conflict changed active")
	}
	if e := second.VerifyArguments(applyCoreContext(t), func() []string {
		i, _, e := c.store.ReadRunIntent(secondID)
		if e != nil {
			t.Fatal(e)
		}
		return i.Arguments
	}()); e != nil {
		t.Fatal(e)
	}
	echo("rejected-recovery-still-forwarding")
	if e := os.Rename(held, original); e != nil {
		t.Fatal(e)
	}
	restored = true
	recovered, made, e := a.Recover(applyCoreContext(t), recoverRaw)
	if e != nil || !made || recovered.Record.Phase != Committed {
		t.Fatal("same unaccepted identity after fixture restoration", made, e)
	}
	targetRecord, e := c.journal.Get(started.Record.Identity.OperationID)
	if e != nil || targetRecord.SHA256 != targetSHA {
		t.Fatal("terminal target changed", e)
	}
	checked, identity := applyCoreHandle(t, c.store, secondID)
	checked.Close()
	if identity != secondIdentity || !reflect.DeepEqual(beforeActive, active()) {
		t.Fatal("recovery changed core or active")
	}
	echo("after-proved-recovery")
	if sourceCalls.Load() != 1 || echoes.Load() != 4 {
		t.Fatal("unexpected source/echo count")
	}
	managedCoreArchive(t, paths, "terminal-ownership-preflight")
	t.Logf("actual recovery preflight: two cores original%d/current%d; missing current original registration rejected before admission with exact zero tree effects; same unaccepted request admitted only after restoring exact task-held original; terminal target SHA/active/core unchanged;4 echoes;oneSource", firstIdentity.PID, secondIdentity.PID)
}
