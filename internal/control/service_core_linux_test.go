//go:build linux && amd64

package control

import (
	"bytes"
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
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Seed via the accepted independent fixture, then exercise the actual shipped
// CLI/service binary. Product commands never receive the fixture compiler.
func TestServiceCoreCLIContinuity(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	cli := os.Getenv("HOOPA_P1_CLI")
	if source == "" || cli == "" {
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
	protected := serviceCoreProtectedTree(t, paths)
	managedCoreArchive(t, paths, "service-before")
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	c = nil
	var receipts [][]byte
	run := func(input []byte, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = []string{"HOME=" + home, "LANG=C", "TMPDIR=" + os.TempDir()}
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, e := cmd.Output()
		if e != nil {
			t.Fatalf("CLI %v failed: %v; safe envelope=%s stderr bytes=%d", args, e, output, stderr.Len())
		}
		var envelope struct {
			OK   bool            `json:"ok"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(output, &envelope) != nil || !envelope.OK {
			t.Fatal("invalid CLI success envelope")
		}
		receipts = append(receipts, bytes.Clone(output))
		return output
	}
	getStatus := func() ServiceStatus {
		t.Helper()
		raw := run(nil, "status")
		var response struct {
			Data ServiceStatus `json:"data"`
		}
		if e := json.Unmarshal(raw, &response); e != nil {
			t.Fatal(e)
		}
		return response.Data
	}
	// Cleanup is explicit and limited to this test's isolated service/state.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, "service", "stop", "--request-id", "service-fixture-cleanup")
		cmd.Env = []string{"HOME=" + home, "LANG=C", "TMPDIR=" + os.TempDir()}
		_, _ = cmd.Output()
	}()
	waitWriter := func() {
		t.Helper()
		f, e := os.OpenFile(filepath.Join(paths.State, ".writer.lock"), os.O_RDWR, 0)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		deadline := time.Now().Add(10 * time.Second)
		for {
			e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if e == nil {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				return
			}
			if e != syscall.EWOULDBLOCK || time.Now().After(deadline) {
				t.Fatal("writer release not observed", e)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	assertOriginal := func(serviceState string) {
		t.Helper()
		s := getStatus()
		if serviceState == "unavailable" {
			if s.Service != serviceState || s.Core.State != "unknown" || s.Core.Reason != "activation_requires_management" || s.Revision != base.Revision {
				t.Fatalf("offline status falsely claimed activation: %+v", s)
			}
			checked, identity := applyCoreHandle(t, cleanupStore, firstID)
			checked.Close()
			if identity != firstIdentity {
				t.Fatal("independent offline witness changed")
			}
			return
		}
		if s.Service != serviceState || s.Core.State != "verified-running" || s.Core.PID == nil || *s.Core.PID != firstIdentity.PID || s.Core.StartTicks == nil || *s.Core.StartTicks != firstIdentity.StartTicks || s.Core.RunID == nil || *s.Core.RunID != firstID || s.Revision != base.Revision {
			t.Fatalf("same original not proved: %+v", s)
		}
	}
	run(nil, "start", "--request-id", "service-actual-adopt", "--expected-revision", string(base.Revision))
	assertOriginal("running")
	echo("cli-start-adopt")
	stopRaw := serviceRequest(t, "service-actual-stop", base.Revision)
	stopReceipt := run(stopRaw, "api", "v1", "service.stop")
	waitWriter()
	assertOriginal("unavailable")
	echo("service-stopped")
	if !reflect.DeepEqual(protected, serviceCoreProtectedTree(t, paths)) {
		t.Fatal("service stop/adoption rewrote core originals")
	}
	if replay := run(stopRaw, "api", "v1", "service.stop"); !bytes.Equal(replay, stopReceipt) {
		t.Fatal("offline replay changed receipt")
	}
	run(nil, "start", "--control-only", "--request-id", "service-actual-control-only", "--expected-revision", string(base.Revision))
	assertOriginal("running")
	if replay := run(stopRaw, "api", "v1", "service.stop"); !bytes.Equal(replay, stopReceipt) {
		t.Fatal("online replay changed receipt")
	}
	assertOriginal("running")
	echo("new-service-old-stop-replay")
	if !reflect.DeepEqual(protected, serviceCoreProtectedTree(t, paths)) {
		t.Fatal("control-only/replay rewrote core originals")
	}
	run(nil, "stop", "--accept-disruption", "--request-id", "service-actual-core-stop", "--expected-revision", string(base.Revision))
	if e := first.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal("actual stop did not exit original", e)
	}
	stopped := getStatus()
	if stopped.Core.State != "verified-stopped" || stopped.DesiredRuntime != "stopped" || stopped.Revision == base.Revision {
		t.Fatalf("stop state incorrect %+v", stopped)
	}
	run(nil, "start", "--request-id", "service-actual-core-start", "--expected-revision", string(stopped.Revision))
	started := getStatus()
	if started.Core.State != "verified-running" || started.Core.RunID == nil || *started.Core.RunID == firstID || started.Revision == stopped.Revision {
		t.Fatalf("start state incorrect %+v", started)
	}
	second, secondIdentity := applyCoreHandle(t, cleanupStore, *started.Core.RunID)
	handles[*started.Core.RunID] = second
	if secondIdentity == firstIdentity {
		t.Fatal("new start reused original")
	}
	echo("cli-new-core")
	ids, e := cleanupStore.ListRunIDs()
	if e != nil || len(ids) != 2 {
		t.Fatal("duplicate or missing run", e)
	}
	run(serviceRequest(t, "service-final-stop", started.Revision), "api", "v1", "service.stop")
	waitWriter()
	final := getStatus()
	if final.Service != "unavailable" || final.Core.State != "unknown" || final.Core.Reason != "activation_requires_management" {
		t.Fatalf("final offline status exceeded its evidence: %+v", final)
	}
	checked, identity := applyCoreHandle(t, cleanupStore, *started.Core.RunID)
	checked.Close()
	if identity != secondIdentity {
		t.Fatal("final original core identity changed")
	}
	echo("final-service-stopped")
	if sourceCalls.Load() != 1 || echoes.Load() != 6 {
		t.Fatal("unexpected fixture/source count", sourceCalls.Load(), echoes.Load())
	}
	managedCoreArchive(t, paths, "service-after")
	if directory := os.Getenv("HOOPA_MANAGED_START_ARTIFACTS"); directory != "" {
		raw, e := json.Marshal(receipts)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(directory, "cli-receipts.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("actual shipped CLI: two original cores %d/%d; six echoes; one fixture Source; adoption and service-only stop/reopen preserve original; offline+online exact stop replay; explicit stop/start uses product launcher; final core forwarding survives service exit", firstIdentity.PID, secondIdentity.PID)
}

// Only core authority and immutable bundle/run records are protected here.
// Service discovery and its private logs are expected to change on bootstrap.
func serviceCoreProtectedTree(t *testing.T, paths config.Paths) map[string]string {
	t.Helper()
	tree := coordinatorTree(t, paths)
	for path := range tree {
		keep := path == filepath.Join(paths.State, "active.json")
		for _, name := range []string{"runs", "bundles"} {
			root := filepath.Join(paths.State, name)
			keep = keep || path == root || strings.HasPrefix(path, root+string(filepath.Separator))
		}
		if !keep {
			delete(tree, path)
		}
	}
	return tree
}
