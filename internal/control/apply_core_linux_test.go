//go:build linux && amd64

package control

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// Empty parent dispatch is not core coverage. A successful exec replaces this
// process, including its context timer; it does not impose a timer on the core.
func TestBasicApplyCoreLauncherDispatch(t *testing.T) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if e := mihomo.ExecBasicLauncher(ctx, os.NewFile(3, "birth"), os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE")); e != nil {
		t.Fatal("dedicated test launcher failed", e)
	}
	t.Fatal("successful exec returned")
}
func applyCoreContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func applyCoreMaterial(t *testing.T, binary, controller, entry string, generation config.Generation) (Plan, BasicMaterial) {
	t.Helper()
	seed, m := basicMaterialFixture(t, binary)
	var raw map[string]any
	if e := json.Unmarshal(m.Files["config.json"], &raw); e != nil {
		t.Fatal(e)
	}
	_, portText, e := net.SplitHostPort(entry)
	if e != nil {
		t.Fatal(e)
	}
	var port int
	if _, e := fmt.Sscan(portText, &port); e != nil {
		t.Fatal(e)
	}
	raw["external-controller"] = controller
	raw["listeners"].([]any)[0].(map[string]any)["port"] = port
	raw["proxies"] = []any{map[string]any{"name": "node:direct", "type": "direct"}}
	raw["rules"] = []string{"MATCH,node:direct"}
	m.Files["config.json"] = planRaw(t, raw)
	var snapshot wire.Snapshot
	if e := json.Unmarshal(m.Files[mihomo.SnapshotFile], &snapshot); e != nil {
		t.Fatal(e)
	}
	snapshot.Controller = controller
	snapshot.ConfigSHA256 = hash(m.Files["config.json"])
	snapshot.Entries[0].Address = entry
	snapshot.Proxies = append(snapshot.Proxies, wire.Proxy{Name: "node:direct", Type: "Direct", Members: []string{}})
	for i := range snapshot.Proxies {
		if snapshot.Proxies[i].Name == "GLOBAL" {
			snapshot.Proxies[i].Members = []string{"DIRECT", "REJECT", "node:direct"}
			snapshot.Proxies[i].Selected = "DIRECT"
		}
	}
	sort.Slice(snapshot.Proxies, func(i, j int) bool { return snapshot.Proxies[i].Name < snapshot.Proxies[j].Name })
	snapshot.Rules = []wire.Rule{{Type: "Match", Payload: "", Target: "node:direct"}}
	if e := snapshot.Validate(); e != nil {
		t.Fatal(e)
	}
	m.Files[mihomo.SnapshotFile] = planRaw(t, snapshot)
	digest, e := snapshot.Digest()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("independent prelaunch snapshot generation=%s digest=%s config_sha=%s", generation, digest, snapshot.ConfigSHA256)
	m.Manifest.Generation = generation
	basicMaterialRehash(&m)
	if e := os.WriteFile(filepath.Join(m.Workdir, "config.json"), m.Files["config.json"], 0600); e != nil {
		t.Fatal(e)
	}
	return seed, m
}
func applyCorePlan(t *testing.T, seed Plan, base config.Revision, impact PlanImpact) Plan {
	t.Helper()
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(seed.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	delete(obj, "plan_digest")
	obj["base_revision"] = planRaw(t, base)
	obj["impact"] = planRaw(t, impact)
	p, e := BuildPlan(planRaw(t, obj), planFixtureValidator(t, obj))
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func applyCoreRunID(t *testing.T, record StoredOperation) config.RunID {
	t.Helper()
	var state applyEvidence
	if len(record.Record.Steps) == 0 {
		t.Fatal("missing run evidence")
	}
	if e := json.Unmarshal(record.Record.Steps[len(record.Record.Steps)-1].Evidence, &state); e != nil {
		t.Fatal(e)
	}
	id := state.TargetRunID
	if record.Record.Phase == RolledBack {
		id = state.RollbackRunID
	}
	if id == nil {
		t.Fatal("missing verified run")
	}
	return *id
}
func applyCoreHandle(t *testing.T, store *config.Store, id config.RunID) (*platform.ProcessHandle, platform.ProcessIdentity) {
	t.Helper()
	intent, _, e := store.ReadRunIntent(id)
	if e != nil {
		t.Fatal(e)
	}
	birth, _, e := store.ReadRunBirth(id)
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
	observed, e := ownership.Observe(applyCoreContext(t), id)
	if e != nil {
		t.Fatal(e)
	}
	defer observed.Close()
	if observed.Observation().Gate != "open" {
		t.Fatal("committed core not observed open")
	}
	h, e := platform.ObserveProcess(applyCoreContext(t), birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if e != nil {
		t.Fatal(e)
	}
	identity := h.Identity()
	if identity != observed.Identity() || identity.PID != birth.PID || identity.UID != birth.UID || identity.BootID != birth.BootID || identity.StartTicks != birth.StartTicks {
		h.Close()
		t.Fatal("independent original pidfd differs from ownership proof")
	}
	if e := h.VerifyArguments(applyCoreContext(t), intent.Arguments); e != nil {
		h.Close()
		t.Fatal(e)
	}
	t.Logf("actual owned identity run=%s pid=%d uid=%d boot=%s start=%d generation=%s", id, identity.PID, identity.UID, identity.BootID, identity.StartTicks, intent.Generation)
	return h, identity
}
func applyCoreAbsent(ctx context.Context, pid int) error {
	for {
		_, e := os.Stat(fmt.Sprintf("/proc/%d", pid))
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func applyCoreHTTP(t *testing.T, entry, target, phase string, authorized bool) (int, string) {
	t.Helper()
	conn, e := net.DialTimeout("tcp4", entry, time.Second)
	if e != nil {
		t.Fatal("entry dial", e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	auth := ""
	if authorized {
		auth = "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("u", 32)+":"+strings.Repeat("p", 32))) + "\r\n"
	}
	if _, e := fmt.Fprintf(conn, "GET http://%s/%s HTTP/1.1\r\nHost: %s\r\n%sConnection: close\r\n\r\n", target, phase, target, auth); e != nil {
		t.Fatal("proxy write", e)
	}
	response, e := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
	if e != nil {
		t.Fatal("proxy response", e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 1024))
	if e != nil {
		t.Fatal("proxy response body", e)
	}
	return response.StatusCode, string(body)
}

// Cleanup is test-owned process cleanup, not a product stop or fabricated
// business journal operation. No unproved numeric PID is ever signaled.
func applyCoreCleanup(t *testing.T, store *config.Store, binary string, handles map[config.RunID]*platform.ProcessHandle) {
	t.Helper()
	ids, e := store.ListRunIDs()
	if e != nil {
		t.Error("cleanup inventory unknown", e)
		return
	}
	for _, id := range ids {
		if h := handles[id]; h != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			e := h.WaitExited(ctx)
			cancel()
			if e == nil {
				h.Close()
				delete(handles, id)
				continue
			}
		}
		intent, _, e := store.ReadRunIntent(id)
		if e == nil {
			if log, le := os.ReadFile(filepath.Join(intent.Workdir, "launcher.log")); le == nil {
				t.Logf("task launcher original log run=%s bytes=%d sha256=%s (content withheld to avoid secrets)", id, len(log), hash(log))
			}
		}
		if e != nil {
			t.Error("cleanup intent unknown", e)
			continue
		}
		birth, _, e := store.ReadRunBirth(id)
		if e != nil {
			t.Error("cleanup birth unknown; no PID scan/signal", e)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var h *platform.ProcessHandle
		var expectedArgs []string
		capability, e := mihomo.ApprovedBasicCapability(binary)
		if e == nil {
			controller, ce := mihomo.NewController(store, capability)
			if ce == nil {
				ownership, oe := owned.NewOwnership(store, controller)
				if oe == nil {
					run, re := ownership.Observe(ctx, id)
					if re == nil {
						reg, _, ge := store.ReadControlRegistration(id, intent.OperationID)
						_, intentSHA, ie := store.ReadRunIntent(id)
						_, birthSHA, be := store.ReadRunBirth(id)
						if ge == nil && ie == nil && be == nil && reg.Match(intent, intentSHA, birth, birthSHA) == nil {
							candidate, pe := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
							if pe == nil {
								if candidate.Identity() == run.Identity() && candidate.VerifyArguments(ctx, intent.Arguments) == nil {
									h = candidate
									expectedArgs = append([]string(nil), intent.Arguments...)
								} else {
									candidate.Close()
								}
							}
						}
						run.Close()
					}
				}
			}
		}
		if h == nil {
			// A failure before registration may leave either the original core or this
			// fixed self-launcher. Both require the original persisted birth + full proof.
			self, se := os.Executable()
			selfBytes, re := os.ReadFile(self)
			if se == nil && re == nil {
				for _, candidate := range []struct {
					path, sha string
					argv      []string
				}{{intent.Binary.Path, intent.Binary.SHA256, intent.Arguments}, {self, hash(selfBytes), append([]string{self}, applyCoreEntryArgs()...)}} {
					observed, pe := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: candidate.path, BinarySHA256: candidate.sha, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
					if pe != nil {
						continue
					}
					actual := observed.Identity()
					if actual.PID == birth.PID && actual.UID == birth.UID && actual.BootID == birth.BootID && actual.StartTicks == birth.StartTicks && observed.VerifyArguments(ctx, candidate.argv) == nil {
						h = observed
						expectedArgs = append([]string(nil), candidate.argv...)
						break
					}
					observed.Close()
				}
			}
		}
		if h == nil {
			cancel()
			t.Error("cleanup ownership unknown; no unproved signal")
			continue
		}
		if e := h.VerifyArguments(ctx, expectedArgs); e != nil {
			h.Close()
			cancel()
			t.Error("cleanup argv no longer proved; no signal", e)
			continue
		}

		e = h.SignalVerified(ctx, syscall.SIGTERM)
		if e == nil {
			e = h.WaitExited(ctx)
		}
		if e == nil {
			e = applyCoreAbsent(ctx, birth.PID)
		}
		t.Logf("task-only cleanup pid=%d original_pidfd_exit=%t proc_absent=%t", birth.PID, e == nil, e == nil)
		if e != nil {
			t.Error("owned cleanup incomplete", e)
		}
		h.Close()
		cancel()
		if previous := handles[id]; previous != nil {
			previous.Close()
			delete(handles, id)
		}
	}
	for _, h := range handles {
		h.Close()
	}
}
func applyCoreEntryArgs() []string {
	return []string{"-test.run=^TestBasicApplyCoreLauncherDispatch$", "-test.timeout=15s"}
}
func TestBasicApplyCoreStartReplace(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit approved basic core required")
	}
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("unexpected approved binary")
	}
	private := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(private, "mihomo")
	if e := os.WriteFile(binary, raw, 0755); e != nil {
		t.Fatal(e)
	}
	reserved := []*net.TCPListener{}
	endpoints := []string{}
	for i := 0; i < 2; i++ {
		l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		reserved = append(reserved, l)
		defer l.Close()
		endpoints = append(endpoints, l.Addr().String())
	}
	for _, l := range reserved {
		l.Close()
	}
	controllerAddress, entry := endpoints[0], endpoints[1]
	seed, first := applyCoreMaterial(t, binary, controllerAddress, entry, "generation-start")
	_, second := applyCoreMaterial(t, binary, controllerAddress, entry, "generation-replace")
	firstImpact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(first.Manifest.Generation), ConnectionEffect: "unknown", RuntimeAction: "start"}
	oldGeneration := string(first.Manifest.Generation)
	secondImpact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, PreviousGeneration: &oldGeneration, TargetGeneration: string(second.Manifest.Generation), ConnectionEffect: "interrupt", RuntimeAction: "replace"}
	capability, e := mihomo.ApprovedBasicCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	capSHA, e := capability.Digest()
	if e != nil {
		t.Fatal(e)
	}
	current := CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Inputs: []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: capability.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}}, Impact: firstImpact}
	var validatorObj map[string]json.RawMessage
	json.Unmarshal(seed.Bytes(), &validatorObj)
	validator := planFixtureValidator(t, validatorObj)
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
	applier, e := NewBasicApplier(c, func(_ context.Context, p Plan) (BasicMaterial, error) {
		sourceCalls.Add(1)
		switch p.Document().Impact.TargetGeneration {
		case "generation-start":
			return first, nil
		case "generation-replace":
			return second, nil
		default:
			return BasicMaterial{}, ErrPlan
		}
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	var echoes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echoes.Add(1)
		io.WriteString(w, "local-echo:"+r.URL.Path)
	}))
	defer server.Close()
	target := strings.TrimPrefix(server.URL, "http://")
	base, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	firstPlan := applyCorePlan(t, seed, base.Revision, firstImpact)
	firstRaw := coordinatorRequest(t, "actual-start", firstPlan, nil)
	started, made, e := applier.Apply(applyCoreContext(t), firstRaw)
	if e != nil || !made || started.Record.Phase != Committed {
		t.Fatal("actual start failed", made, e, started.Record.Phase)
	}
	firstID := applyCoreRunID(t, started)
	firstHandle, firstIdentity := applyCoreHandle(t, c.store, firstID)
	handles[firstID] = firstHandle
	status, _ := applyCoreHTTP(t, entry, target, "unauthorized", false)
	if status != http.StatusProxyAuthRequired || echoes.Load() != 0 {
		t.Fatal("protected entry allowed unauthenticated forwarding", status)
	}
	status, body := applyCoreHTTP(t, entry, target, "before", true)
	if status != 200 || body != "local-echo:/before" || echoes.Load() != 1 {
		t.Fatal("baseline local proxy echo mismatch", status)
	}
	current.Impact = secondImpact
	base, e = c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	replacePlan := applyCorePlan(t, seed, base.Revision, secondImpact)
	replaceRaw := coordinatorRequest(t, "actual-replace", replacePlan, nil)
	replaced, made, e := applier.Apply(applyCoreContext(t), replaceRaw)
	if e != nil || !made || replaced.Record.Phase != Committed {
		t.Fatal("actual replace failed", made, e, replaced.Record.Phase)
	}
	secondID := applyCoreRunID(t, replaced)
	secondHandle, secondIdentity := applyCoreHandle(t, c.store, secondID)
	handles[secondID] = secondHandle
	if firstID == secondID || firstIdentity == secondIdentity || firstIdentity.PID == secondIdentity.PID {
		t.Fatal("replacement reused original instance")
	}
	if e := firstHandle.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal("old original pidfd not exited", e)
	}
	if e := applyCoreAbsent(applyCoreContext(t), firstIdentity.PID); e != nil {
		t.Fatal("old child not observed reaped", e)
	}
	status, body = applyCoreHTTP(t, entry, target, "after", true)
	if status != 200 || body != "local-echo:/after" || echoes.Load() != 2 {
		t.Fatal("replacement local proxy echo mismatch", status)
	}
	before := coordinatorTree(t, paths)
	ids, e := c.store.ListRunIDs()
	if e != nil || len(ids) != 2 {
		t.Fatal("unexpected run inventory", ids, e)
	}
	calls := sourceCalls.Load()
	current.CompilerVersion = "unavailable-after-commit"
	replay, made, e := applier.Apply(nil, replaceRaw)
	if e != nil || made || !reflect.DeepEqual(replay, replaced) || sourceCalls.Load() != calls {
		t.Fatal("same-ID retry did effects", made, e)
	}
	afterIDs, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, afterIDs) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("retry changed runtime records", e)
	}
	t.Logf("actual_core_generations=2 distinct_pids=true shared_entry=%s shared_controller=%s authorized_local_echoes=%d unauthenticated_refusals=1 same_request_duplicate_runs=0", entry, controllerAddress, echoes.Load())
}
