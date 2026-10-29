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
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Actual foreground service and its one-use worker, with task-owned pidfd kills.
func TestServiceCoreCLIRestartBudget(t *testing.T) {
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
	if e := c.Close(); e != nil {
		t.Fatal(e)
	}
	c = nil
	manager := exec.Command(cli, "service", "run", "--restart-once")
	manager.Env = []string{"HOME=" + home, "LANG=C", "TMPDIR=" + os.TempDir()}
	var managerLog bytes.Buffer
	manager.Stdout = &managerLog
	manager.Stderr = &managerLog
	if e := manager.Start(); e != nil {
		t.Fatal(e)
	}
	managerDone := make(chan error, 1)
	go func() { managerDone <- manager.Wait() }()
	defer func() {
		_ = manager.Process.Kill()
		select {
		case <-managerDone:
		case <-time.After(10 * time.Second):
			t.Error("test-owned manager cleanup timeout")
		}
	}()
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = manager.Env
		return cmd.Output()
	}
	status := func() (ServiceStatus, error) {
		raw, e := call("status")
		if e != nil {
			return ServiceStatus{}, e
		}
		var envelope struct {
			Data ServiceStatus `json:"data"`
		}
		e = json.Unmarshal(raw, &envelope)
		return envelope.Data, e
	}
	await := func(label string, ready func(ServiceStatus) bool) ServiceStatus {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		var last ServiceStatus
		var err error
		for time.Now().Before(deadline) {
			last, err = status()
			if err == nil && ready(last) {
				return last
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s status condition timeout: %+v err=%v", label, last, err)
		return last
	}
	initialStatus := await("foreground ready", func(s ServiceStatus) bool { return s.Service == "running" })
	if initialStatus.RestartPolicy != "armed_for_next_start" {
		t.Fatal("startup granted restart unexpectedly")
	}
	if _, e := call("start", "--control-only", "--request-id", "watch-control-only", "--expected-revision", string(base.Revision)); e != nil {
		t.Fatal(e)
	}
	controlOnly, e := status()
	if e != nil || controlOnly.RestartPolicy != "armed_for_next_start" {
		t.Fatal("control-only consumed grant", e)
	}
	if _, e := call("start", "--request-id", "watch-adopt", "--expected-revision", string(base.Revision)); e != nil {
		t.Fatal("managed start", e)
	}
	await("watch granted", func(s ServiceStatus) bool { return s.RestartPolicy == "watching_original" })
	echo("watch-adopt")
	if e := first.SignalVerified(applyCoreContext(t), syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	if e := first.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal(e)
	}
	next := await("one restart", func(s ServiceStatus) bool {
		return s.Core.State == "verified-running" && s.Core.RunID != nil && *s.Core.RunID != firstID && s.RestartPolicy == "budget_used_watching_successor"
	})
	if next.Revision != base.Revision || next.Generation == nil || *next.Generation != *base.Generation {
		t.Fatal("restart changed desired revision")
	}
	second, secondIdentity := applyCoreHandle(t, cleanupStore, *next.Core.RunID)
	handles[*next.Core.RunID] = second
	echo("watch-successor")
	if e := second.SignalVerified(applyCoreContext(t), syscall.SIGKILL); e != nil {
		t.Fatal(e)
	}
	if e := second.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal(e)
	}
	exhausted := await("successor exit", func(s ServiceStatus) bool { return s.RestartPolicy == "inactive" && s.Core.State == "verified-stopped" })
	if exhausted.Core.Reason != "observed_original_exit" || exhausted.DesiredRuntime != "running" {
		t.Fatal("lost actual vs desired state")
	}
	ids, e := cleanupStore.ListRunIDs()
	if e != nil || len(ids) != 2 {
		t.Fatal("restart budget exceeded", e)
	}
	if _, e := call("service", "stop", "--request-id", "watch-service-stop", "--expected-revision", string(base.Revision)); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-managerDone:
		if e != nil {
			t.Fatal("foreground service exit", e)
		}
		managerDone <- nil // consumed again only by deferred owned-child cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("service stop did not drain watch")
	}
	if sourceCalls.Load() != 1 || echoes.Load() != 3 {
		t.Fatal("unexpected fixture/echo count")
	}
	if directory := os.Getenv("HOOPA_MANAGED_START_ARTIFACTS"); directory != "" {
		raw, _ := json.Marshal([]ServiceStatus{initialStatus, controlOnly, next, exhausted})
		if e := os.WriteFile(filepath.Join(directory, "restart-statuses.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		managedCoreArchive(t, paths, "restart-after")
	}
	t.Logf("actual foreground service restart: explicit armed scope; control-only no grant; one managed start grants; killed original%d replaced once by%d; second original exit observed; exactly2 runs, unchanged revision,3 echoes, graceful service stop drains worker", firstIdentity.PID, secondIdentity.PID)
}
