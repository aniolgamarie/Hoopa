//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
)

func rollbackCoreAddEntry(t *testing.T, m *BasicMaterial, address string) {
	t.Helper()
	var raw map[string]any
	if e := json.Unmarshal(m.Files["config.json"], &raw); e != nil {
		t.Fatal(e)
	}
	listeners := raw["listeners"].([]any)
	// Fixed profile requires distinct per-entry credential pairs. This literal
	// projection is independent of any running core response.
	secondUsers := []struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{{"rollback-user-b", strings.Repeat("q", 32)}}
	_, p, e := net.SplitHostPort(address)
	if e != nil {
		t.Fatal(e)
	}
	var port int
	if _, e := fmt.Sscan(p, &port); e != nil {
		t.Fatal(e)
	}
	raw["listeners"] = append(listeners, map[string]any{"name": "entry-b", "type": "mixed", "listen": "127.0.0.1", "port": port, "udp": false, "users": secondUsers})
	m.Files["config.json"] = planRaw(t, raw)
	var s wire.Snapshot
	if e := json.Unmarshal(m.Files[mihomo.SnapshotFile], &s); e != nil {
		t.Fatal(e)
	}
	// The expected projection is defined from our fixture BEFORE any core starts.
	s.ConfigSHA256 = hash(m.Files["config.json"])
	s.Entries = append(s.Entries, wire.Entry{Name: "entry-b", Type: "mixed", Address: address, AuthSHA256: hash(planRaw(t, secondUsers))})
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	m.Files[mihomo.SnapshotFile] = planRaw(t, s)
	basicMaterialRehash(m)
	if e := os.WriteFile(filepath.Join(m.Workdir, "config.json"), m.Files["config.json"], 0600); e != nil {
		t.Fatal(e)
	}
	digest, e := s.Digest()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("independent two-entry prelaunch snapshot generation=%s digest=%s config_sha=%s", m.Manifest.Generation, digest, s.ConfigSHA256)
}
func rollbackCoreCompetitor(t *testing.T, address string) *net.TCPListener {
	t.Helper()
	a, e := net.ResolveTCPAddr("tcp4", address)
	if e != nil {
		t.Fatal(e)
	}
	fd, e := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, syscall.IPPROTO_TCP)
	if e != nil {
		t.Fatal(e)
	}
	f := os.NewFile(uintptr(fd), "rollback-task-competitor")
	defer f.Close()
	if e = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Bind(fd, &syscall.SockaddrInet4{Port: a.Port, Addr: [4]byte{127, 0, 0, 1}}); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Listen(fd, 1); e != nil {
		t.Fatal(e)
	}
	l, e := net.FileListener(f)
	if e != nil {
		t.Fatal(e)
	}
	return l.(*net.TCPListener)
}
func rollbackCoreCompetitorAlive(t *testing.T, l *net.TCPListener) {
	t.Helper()
	l.SetDeadline(time.Now().Add(time.Second))
	c, e := net.DialTimeout("tcp4", l.Addr().String(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	a, e := l.AcceptTCP()
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	a.SetDeadline(time.Now().Add(time.Second))
	if _, e = a.Write([]byte{0x72}); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1)
	if _, e = io.ReadFull(c, b); e != nil || b[0] != 0x72 {
		t.Fatal("competitor lost its own socket", e)
	}
}

// This uses the real production admitted workflow with a test-only boundary;
// public BasicApplier always passes nil. It does not claim the replacement was
// admitted through the public method, a generic compiler, or manager recovery.
func TestBasicRollbackCoreActivationFailure(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit approved fixed core required")
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("wrong approved input")
	}
	private := filepath.Join(t.TempDir(), "private")
	if e = os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(private, "mihomo")
	if e = os.WriteFile(binary, raw, 0755); e != nil {
		t.Fatal(e)
	}
	var endpoints []string
	var reservations []*net.TCPListener
	for i := 0; i < 3; i++ {
		l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		reservations = append(reservations, l)
		defer l.Close()
		endpoints = append(endpoints, l.Addr().String())
	}
	for _, l := range reservations {
		l.Close()
	}
	controller, entry, extra := endpoints[0], endpoints[1], endpoints[2]
	seed, first := applyCoreMaterial(t, binary, controller, entry, "rollback-old-generation")
	_, second := applyCoreMaterial(t, binary, controller, entry, "rollback-target-generation")
	rollbackCoreAddEntry(t, &second, extra)
	oldGen := string(first.Manifest.Generation)
	startImpact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: oldGen, ConnectionEffect: "unknown", RuntimeAction: "start"}
	replaceImpact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, PreviousGeneration: &oldGen, TargetGeneration: string(second.Manifest.Generation), ConnectionEffect: "interrupt", RuntimeAction: "replace"}
	cap, e := mihomo.ApprovedBasicCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	capSHA, e := cap.Digest()
	if e != nil {
		t.Fatal(e)
	}
	current := CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Inputs: []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: cap.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}}, Impact: startImpact}
	var obj map[string]json.RawMessage
	if e = json.Unmarshal(seed.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := OpenCoordinator(paths, planFixtureValidator(t, obj), func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil })
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer func() { rollbackCoreOriginals(t, c.store); applyCoreCleanup(t, c.store, binary, handles) }()
	var calls atomic.Int32
	applier, e := NewBasicApplier(c, func(_ context.Context, p Plan) (BasicMaterial, error) {
		calls.Add(1)
		switch p.Document().Impact.TargetGeneration {
		case oldGen:
			return first, nil
		case string(second.Manifest.Generation):
			return second, nil
		}
		return BasicMaterial{}, ErrPlan
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	var echoes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echoes.Add(1)
		io.WriteString(w, "rollback-echo:"+r.URL.Path)
	}))
	defer server.Close()
	target := strings.TrimPrefix(server.URL, "http://")
	initial, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	startPlan := applyCorePlan(t, seed, initial.Revision, startImpact)
	started, made, e := applier.Apply(applyCoreContext(t), coordinatorRequest(t, "rollback-core-start", startPlan, nil))
	if e != nil || !made || started.Record.Phase != Committed {
		t.Fatal("public baseline start", made, e)
	}
	oldID := applyCoreRunID(t, started)
	oldHandle, oldIdentity := applyCoreHandle(t, c.store, oldID)
	handles[oldID] = oldHandle
	status, body := applyCoreHTTP(t, entry, target, "before", true)
	if status != 200 || body != "rollback-echo:/before" || echoes.Load() != 1 {
		t.Fatal("baseline echo", status)
	}
	base, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	oldBundle, e := c.store.ReadBundle(first.Manifest.Generation)
	if e != nil {
		t.Fatal(e)
	}
	oldWork := snapshot(t, first.Workdir)
	current.Impact = replaceImpact
	replacePlan := applyCorePlan(t, seed, base.Revision, replaceImpact)
	replaceRaw := coordinatorRequest(t, "rollback-core-replace", replacePlan, nil)
	request, e := ParseRequest(replaceRaw)
	if e != nil {
		t.Fatal(e)
	}
	var targetID config.RunID
	var targetHandle *platform.ProcessHandle
	var targetIdentity platform.ProcessIdentity
	var competitor *net.TCPListener
	defer func() {
		if competitor != nil {
			competitor.Close()
		}
	}()
	var seen []string
	degradedObserved := false
	rolled, e := func() (StoredOperation, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var checked checkedBasicMaterial
		var state applyEvidence
		admitted, made, _, e := c.admitCheckedApplyLocked(ctx, request, func(p Plan, b config.ActivePointer) error {
			material, e := applier.source(ctx, p)
			if e != nil {
				return e
			}
			checked, e = checkBasicMaterial(ctx, p, material)
			if e != nil {
				return e
			}
			old, e := c.activeRunForApply(b, p.Document().Impact.RuntimeAction, checked.launch.Workdir)
			if e != nil {
				return e
			}
			state = applyEvidence{Version: 1, Base: b, OldRunID: old, OldStopped: old == nil, TargetManifestSHA256: checked.launch.ManifestSHA256}
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := c.store.CheckOperationAdmission(); e != nil {
				return e
			}
			sha, e := c.store.PublishBundle(checked.manifest, checked.files)
			if e != nil {
				return e
			}
			if sha != state.TargetManifestSHA256 {
				return config.ErrIntegrity
			}
			return nil
		})
		if e != nil || !made {
			t.Fatal("internal same-check admission", made, e)
		}
		runtime, e := newBasicApplyRuntime(c.store, checked, applyCoreEntryArgs())
		if e != nil {
			t.Fatal(e)
		}
		defer runtime.close()
		return c.executeBasicApply(ctx, admitted, state, runtime, func(point string) error {
			seen = append(seen, point)
			if point == "old-stop-observed" {
				if e := oldHandle.WaitExited(ctx); e != nil {
					t.Fatal("old retained pidfd exit at boundary", e)
				}
				if e := applyCoreAbsent(ctx, oldIdentity.PID); e != nil {
					t.Fatal("old child absence at boundary", e)
				}
				t.Logf("old original retained pidfd exit and proc absence confirmed pid=%d", oldIdentity.PID)
			}
			if point == "activate-target" {
				if runtime.registered == nil || runtime.registered.Observation().Gate != "closed" {
					t.Fatal("interference preceded actual closed registration")
				}
				targetID = runtime.registered.Observation().RunID
				intent, intentSHA, e := c.store.ReadRunIntent(targetID)
				if e != nil {
					t.Fatal(e)
				}
				birth, birthSHA, e := c.store.ReadRunBirth(targetID)
				if e != nil {
					t.Fatal(e)
				}
				reg, _, e := c.store.ReadControlRegistration(targetID, admitted.Record.Identity.OperationID)
				if e != nil || reg.Match(intent, intentSHA, birth, birthSHA) != nil {
					t.Fatal("closed registration originals", e)
				}
				h, e := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
				if e != nil {
					t.Fatal(e)
				}
				targetHandle = h
				handles[targetID] = h
				targetIdentity = h.Identity()
				if targetIdentity != runtime.registered.Identity() || targetIdentity.PID != birth.PID || targetIdentity.UID != birth.UID || targetIdentity.BootID != birth.BootID || targetIdentity.StartTicks != birth.StartTicks || h.VerifyArguments(ctx, intent.Arguments) != nil {
					t.Fatal("independent target pidfd differs")
				}
				t.Logf("actual closed target run=%s identity=%+v", targetID, targetIdentity)
				competitor = rollbackCoreCompetitor(t, extra)
			}
			if point == "rollback-stop-candidate" {
				if competitor == nil || targetHandle == nil {
					t.Fatal("failed target not captured")
				}
				intent, _, e := c.store.ReadRunIntent(targetID)
				if e != nil {
					t.Fatal(e)
				}
				reg, sha, e := c.store.ReadControlRegistration(targetID, admitted.Record.Identity.OperationID)
				if e != nil {
					t.Fatal(e)
				}
				conn, e := targetHandle.DialOwnedTCP(ctx, intent.Controller)
				if e != nil {
					t.Fatal(e)
				}
				obs, e := runtime.controller.InspectTermination(ctx, conn, intent, reg, sha)
				conn.Close()
				if e != nil || obs.Gate != "degraded" {
					t.Fatal("actual failed target degraded proof", e)
				}
				degradedObserved = true
				rollbackCoreCompetitorAlive(t, competitor)
			}
			return nil
		})
	}()
	if !errors.Is(e, ErrApplyRolledBack) || !errors.Is(e, mihomo.ErrTransitionFailed) || rolled.Record.Phase != RolledBack || !degradedObserved {
		t.Fatal("automatic actual rollback", rolled.Record.Phase, e)
	}
	if targetHandle == nil {
		t.Fatal("target proof missing")
	}
	for _, v := range []struct {
		h   *platform.ProcessHandle
		pid int
	}{{oldHandle, oldIdentity.PID}, {targetHandle, targetIdentity.PID}} {
		if e := v.h.WaitExited(applyCoreContext(t)); e != nil {
			t.Fatal("original pidfd did not observe exit", e)
		}
		if e := applyCoreAbsent(applyCoreContext(t), v.pid); e != nil {
			t.Fatal("old/target child not reaped", e)
		}
	}
	restoredID := applyCoreRunID(t, rolled)
	restoredHandle, restoredIdentity := applyCoreHandle(t, c.store, restoredID)
	handles[restoredID] = restoredHandle
	if restoredID == oldID || restoredID == targetID || restoredIdentity.PID == oldIdentity.PID || restoredIdentity.PID == targetIdentity.PID {
		t.Fatal("restoration reused old instance")
	}
	restoredIntent, _, e := c.store.ReadRunIntent(restoredID)
	if e != nil {
		t.Fatal(e)
	}
	if restoredIntent.Workdir == first.Workdir || restoredIntent.Workdir == second.Workdir || filepath.Dir(restoredIntent.Workdir) != paths.Runtime || restoredIntent.Generation != first.Manifest.Generation || restoredIntent.ManifestSHA256 != base.ManifestSHA256 || restoredIntent.OperationID != rolled.Record.Identity.OperationID {
		t.Fatal("rollback not fresh workdir/original bundle/current operation")
	}
	active, e := c.store.ReadActive()
	want := base
	op := rolled.Record.Identity.OperationID
	want.OperationID = &op
	if e != nil || !reflect.DeepEqual(active, want) {
		t.Fatal("rollback changed old pointer revision/generation/manifest", e)
	}
	nowBundle, e := c.store.ReadBundle(first.Manifest.Generation)
	if e != nil || !reflect.DeepEqual(oldBundle, nowBundle) || !reflect.DeepEqual(oldWork, snapshot(t, first.Workdir)) {
		t.Fatal("old originals overwritten", e)
	}
	status, body = applyCoreHTTP(t, entry, target, "restored", true)
	if status != 200 || body != "rollback-echo:/restored" || echoes.Load() != 2 {
		t.Fatal("restored echo", status)
	}
	status, _ = applyCoreHTTP(t, entry, target, "unauthorized", false)
	if status != http.StatusProxyAuthRequired || echoes.Load() != 2 {
		t.Fatal("restored credential refusal", status)
	}
	rollbackCoreCompetitorAlive(t, competitor)
	before := coordinatorTree(t, paths)
	ids, e := c.store.ListRunIDs()
	if e != nil || len(ids) != 3 {
		t.Fatal("actual run census", ids, e)
	}
	sourceCalls := calls.Load()
	current.CompilerVersion = "unavailable-after-terminal"
	replay, made, e := applier.Apply(nil, replaceRaw)
	if e != nil || made || !reflect.DeepEqual(replay, rolled) || calls.Load() != sourceCalls {
		t.Fatal("terminal replay caused effects", made, e)
	}
	after, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, after) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("terminal replay changed records", e)
	}
	// Explicit recovery of an ALREADY RolledBack actual operation must validate
	// both stopped original chains without changing the live restored instance.
	originalReg, originalRegSHA, e := c.store.ReadControlRegistration(restoredID, op)
	if e != nil {
		t.Fatal(e)
	}
	recoverCall, made, e := applier.Recover(applyCoreContext(t), recoveryRequest(t, "actual-rolled-back-recover", op, active.Revision))
	if e != nil || !made || recoverCall.Record.Phase != Committed || recoverCall.Record.Identity.OperationID == op {
		t.Fatal("explicit actual rollback recovery", made, e)
	}
	original, e := c.journal.Get(op)
	if e != nil || original.SHA256 != rolled.SHA256 || !reflect.DeepEqual(original, rolled) {
		t.Fatal("explicit recover rewrote terminal original", e)
	}
	currentPointer, e := c.store.ReadActive()
	if e != nil || !reflect.DeepEqual(currentPointer, active) {
		t.Fatal("terminal recovery rewrote active", e)
	}
	recoveredIDs, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(recoveredIDs, ids) || calls.Load() != sourceCalls {
		t.Fatal("terminal recover created a run/read Source", e)
	}
	fresh, identity := applyCoreHandle(t, c.store, restoredID)
	fresh.Close()
	if identity != restoredIdentity {
		t.Fatal("terminal recover changed restored process")
	}
	currentReg, currentRegSHA, e := c.store.ReadControlRegistration(restoredID, op)
	if e != nil || currentReg != originalReg || currentRegSHA != originalRegSHA {
		t.Fatal("terminal recover changed activation registration binding", e)
	}
	proofController, e := mihomo.NewController(c.store, cap)
	if e != nil {
		t.Fatal(e)
	}
	proofOwner, e := owned.NewOwnership(c.store, proofController)
	if e != nil {
		t.Fatal(e)
	}
	observed, e := proofOwner.Observe(applyCoreContext(t), restoredID)
	if e != nil {
		t.Fatal(e)
	}
	proofErr := proofController.VerifyActivation(applyCoreContext(t), observed, op)
	observed.Close()
	if proofErr != nil {
		t.Fatal("original restored activation receipt changed", proofErr)
	}
	t.Logf("public explicit recovery of RolledBack original preserved SHA=%s caller=%s committed same_restored_pid=%d stopped_old_open_and_target_closed_originals_matched=true run_inventory=3 new_spawn_or_signal=false", rolled.SHA256, recoverCall.Record.Identity.OperationID, identity.PID)

	t.Logf("actual_cores=3 old_pid=%d target_pid=%d restored_pid=%d old_and_target_original_pidfds_exited=true new_old_bundle_workdir=%s restored_revision=%s current_operation=%s authorized_echoes=2 unauth_refusals=1 competitor_alive=true replay_new_runs=0 boundaries=%v", oldIdentity.PID, targetIdentity.PID, restoredIdentity.PID, restoredIntent.Workdir, active.Revision, op, seen)
}

// Preserve non-secret diagnostics before temporary fixture removal. Original
// process absence is observation only, never permission to signal a PID.
func rollbackCoreOriginals(t *testing.T, store *config.Store) {
	t.Helper()
	ids, e := store.ListRunIDs()
	if e != nil {
		t.Log("original inventory unavailable", e)
		return
	}
	for _, id := range ids {
		intent, _, e := store.ReadRunIntent(id)
		if e != nil {
			t.Log("original intent unavailable", e)
			continue
		}
		birth, _, e := store.ReadRunBirth(id)
		if e != nil {
			t.Logf("original birth unavailable run=%s", id)
		} else {
			_, pe := os.Stat(fmt.Sprintf("/proc/%d", birth.PID))
			t.Logf("original birth run=%s pid=%d uid=%d boot=%s start=%d proc_absent=%t", id, birth.PID, birth.UID, birth.BootID, birth.StartTicks, os.IsNotExist(pe))
		}
		if b, e := os.ReadFile(filepath.Join(intent.Workdir, "launcher.log")); e == nil {
			t.Logf("original launcher log run=%s bytes=%d sha256=%s", id, len(b), hash(b))
			if string(b) == "Hoopa protected configuration rejected\n" {
				t.Log("original launcher log fixed error: Hoopa protected configuration rejected")
			}
		}
	}
}
