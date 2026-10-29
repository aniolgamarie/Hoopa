//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	"hoopa/internal/policy"
	owned "hoopa/internal/runtime"
	"hoopa/internal/subscription"
	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

// This upstream only permits CONNECT to one separately running local origin.
// The observed route is a real native HTTP proxy hop, not a mocked controller.
func sourceCoreProxy(t *testing.T, target string) (string, *atomic.Int64) {
	t.Helper()
	count := &atomic.Int64{}
	var mu sync.Mutex
	closed := false
	conns := map[net.Conn]bool{}
	var wg sync.WaitGroup
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != target {
			http.Error(w, "unsupported fixture destination", 403)
			return
		}
		up, e := net.DialTimeout("tcp4", target, time.Second)
		if e != nil {
			http.Error(w, "fixture origin unavailable", 502)
			return
		}
		c, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			up.Close()
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			c.Close()
			up.Close()
			return
		}
		conns[c] = true
		conns[up] = true
		wg.Add(1)
		mu.Unlock()
		defer func() {
			c.Close()
			up.Close()
			mu.Lock()
			delete(conns, c)
			delete(conns, up)
			mu.Unlock()
			wg.Done()
		}()
		count.Add(1)
		c.SetDeadline(time.Now().Add(5 * time.Second))
		up.SetDeadline(time.Now().Add(5 * time.Second))
		rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() { io.Copy(up, rw); close(done) }()
		io.Copy(c, up)
		c.Close()
		up.Close()
		<-done
	}))
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		s.Close()
		mu.Lock()
		for c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return strings.TrimPrefix(s.URL, "http://"), count
}
func sourceCoreAddress(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().String()
}
func sourceCoreInputs(in []config.BundleInput) []PlanInput {
	out := make([]PlanInput, len(in))
	for i, x := range in {
		out[i] = PlanInput{Kind: x.Kind, ID: x.ID, SHA256: x.SHA256}
	}
	return out
}
func sourceCoreValidator(compiled policy.Compilation) IntentValidator {
	return func(raw json.RawMessage, in []PlanInput) error {
		out := make([]config.BundleInput, len(in))
		for i, x := range in {
			out[i] = config.BundleInput{Kind: x.Kind, ID: x.ID, SHA256: x.SHA256}
		}
		return compiled.ValidateIntent(raw, out)
	}
}
func sourceCoreObserve(t *testing.T, store *config.Store, id config.RunID) (*platform.ProcessHandle, platform.ProcessIdentity) {
	t.Helper()
	intent, _, e := store.ReadRunIntent(id)
	if e != nil {
		t.Fatal(e)
	}
	birth, _, e := store.ReadRunBirth(id)
	if e != nil {
		t.Fatal(e)
	}
	cap, e := mihomo.ApprovedCapability(intent.Binary)
	if e != nil {
		t.Fatal(e)
	}
	controller, e := mihomo.NewController(store, cap)
	if e != nil {
		t.Fatal(e)
	}
	ownership, e := owned.NewOwnership(store, controller)
	if e != nil {
		t.Fatal(e)
	}
	run, e := ownership.Observe(applyCoreContext(t), id)
	if e != nil {
		t.Fatal(e)
	}
	defer run.Close()
	if run.Observation().Gate != "open" {
		t.Fatal("source core not open")
	}
	h, e := platform.ObserveProcess(applyCoreContext(t), birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if e != nil {
		t.Fatal(e)
	}
	identity := h.Identity()
	if identity != run.Identity() || identity.PID != birth.PID || identity.StartTicks != birth.StartTicks || identity.BootID != birth.BootID || h.VerifyArguments(applyCoreContext(t), intent.Arguments) != nil {
		h.Close()
		t.Fatal("source original identity mismatch")
	}
	return h, identity
}

func TestSourceCompilerCoreApplyAdoptStopStart(t *testing.T) {
	source := os.Getenv("HOOPA_P1_SOURCES_CORE")
	if source == "" {
		t.Skip("explicit approved sources-v2 core required")
	}
	core, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	private := filepath.Join(t.TempDir(), "private")
	if e = os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(private, "mihomo")
	cap, e := mihomo.ApprovedSourcesCapability(binary)
	if e != nil || hash(core) != cap.Binary.SHA256 {
		t.Fatal("fixed source core mismatch", e)
	}
	if e = os.WriteFile(binary, core, 0755); e != nil {
		t.Fatal(e)
	}
	var echoes atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.WriteHeader(204)
			return
		}
		echoes.Add(1)
		io.WriteString(w, "compiled-source:"+r.URL.Path)
	}))
	defer origin.Close()
	target := strings.TrimPrefix(origin.URL, "http://")
	upstream, connects := sourceCoreProxy(t, target)
	_, upPort, e := net.SplitHostPort(upstream)
	if e != nil {
		t.Fatal(e)
	}
	port, e := strconv.Atoi(upPort)
	if e != nil {
		t.Fatal(e)
	}
	provider := planRaw(t, map[string]any{"proxies": []any{map[string]any{"name": "US local controlled hop", "type": "http", "server": "127.0.0.1", "port": port}}})
	snapshot, e := subscription.Parse(provider, subscription.ParseOptions{SourceID: "source-a", IdentityKey: bytes.Repeat([]byte{0x32}, 32), RegionRules: []subscription.RegionRule{{Prefix: "US ", Region: "US"}}})
	if e != nil {
		t.Fatal(e)
	}
	controller, entry := sourceCoreAddress(t), sourceCoreAddress(t)
	_, entryPort, _ := net.SplitHostPort(entry)
	entryNumber, _ := strconv.Atoi(entryPort)
	intent := planRaw(t, map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": cap.Binary}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": "/fixture/provider-only-read-before-compile.json"}, "update_hours": nil}}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": []any{map[string]any{"id": "US", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": origin.URL + "/probe", "interval_seconds": 1, "timeout_ms": 250, "tolerance_ms": 0}}, map[string]any{"id": "BEST", "required": true, "strategy": "follow", "follows": "US"}}, "apps": []any{}, "rules": []any{map[string]any{"id": "local-origin", "match": map[string]any{"kind": "ip_cidr", "value": "127.0.0.1/8"}, "target": map[string]any{"kind": "group", "group_id": "BEST"}}}, "default_target": map[string]any{"kind": "reject"}})
	compiled, e := policy.Compile(policy.CompileInput{Intent: intent, Sources: map[config.ID]subscription.Snapshot{"source-a": snapshot}, Generation: "source-compiled-generation", Capability: cap, Runtime: policy.RuntimeInput{Controller: controller, Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared", Type: "mixed", Listen: "127.0.0.1", Port: uint16(entryNumber), Users: []profile.User{{Username: strings.Repeat("u", 32), Password: strings.Repeat("p", 32)}}}}})
	if e != nil {
		t.Fatal("actual source compiler rejected", e)
	}
	manifest, files := compiled.Bundle()
	expected, e := wire.DecodeSnapshot(files[mihomo.SnapshotFile])
	if e != nil {
		t.Fatal(e)
	}
	expectedSHA, _ := expected.Digest()
	work := filepath.Join(private, "work")
	if e = os.Mkdir(work, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(work, "config.json"), files["core.json"], 0600); e != nil {
		t.Fatal(e)
	}
	material := BasicMaterial{Manifest: manifest, Files: files, Workdir: work}
	impact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(manifest.Generation), ConnectionEffect: "unknown", RuntimeAction: "start"}
	current := CurrentPlanInputs{CompilerVersion: policy.CompilerVersion, CapabilitiesDigest: manifest.CapabilitiesDigest, Inputs: sourceCoreInputs(compiled.Inputs()), Impact: impact}
	validator := sourceCoreValidator(compiled)
	currentInputs := func(context.Context, Plan) (CurrentPlanInputs, error) { return current, nil }
	paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := OpenCoordinator(paths, validator, currentInputs)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if c != nil {
			c.Close()
		}
	}()
	cleanupStore, e := config.OpenStore(paths, false)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanupStore.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer applyCoreCleanup(t, cleanupStore, binary, handles)
	var sourceCalls atomic.Int32
	a, e := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) { sourceCalls.Add(1); return material, nil }, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	base, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	plan, e := BuildPlan(planRaw(t, map[string]any{"schema_version": 1, "base_revision": string(base.Revision), "compiler_version": policy.CompilerVersion, "capabilities_digest": manifest.CapabilitiesDigest, "intent": json.RawMessage(intent), "inputs": current.Inputs, "impact": impact, "blockers": []any{}}), validator)
	if e != nil {
		t.Fatal(e)
	}
	initial, made, e := a.Apply(applyCoreContext(t), coordinatorRequest(t, "source-core-apply", plan, nil))
	if e != nil || !made || initial.Record.Phase != Committed {
		t.Fatal("compiled source Apply", made, e)
	}
	id := applyCoreRunID(t, initial)
	first, identity := sourceCoreObserve(t, c.store, id)
	handles[id] = first
	proxyURL := &url.URL{Scheme: "http", Host: entry, User: url.UserPassword(strings.Repeat("u", 32), strings.Repeat("p", 32))}
	echo := func(phase string) {
		t.Helper()
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			r, e := client.Get(origin.URL + "/" + phase)
			if e == nil {
				body, re := io.ReadAll(io.LimitReader(r.Body, 128))
				r.Body.Close()
				if re == nil && r.StatusCode == 200 && string(body) == "compiled-source:/"+phase {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("compiled source forwarding unavailable", phase)
	}
	echo("after-apply")
	status, e := c.Status(applyCoreContext(t))
	if e != nil || status.Core.State != "verified-running" {
		t.Fatal("v2 status did not verify", e, status.Core.State)
	}
	before, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	protected := managedProtectedTree(t, paths)
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	c = nil
	echo("writer-closed")
	c, e = OpenCoordinator(paths, validator, currentInputs)
	if e != nil {
		t.Fatal(e)
	}
	a, e = NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("adoption/start recompiles sources")
		return BasicMaterial{}, ErrPlan
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	adopted, made, e := a.EnsureStarted(applyCoreContext(t), lifecycleRequest(t, "source-core-adopt", "runtime.start", before.Revision))
	if e != nil || !made || adopted.Record.Phase != Committed {
		t.Fatal("v2 original adoption", made, e)
	}
	same, sameIdentity := sourceCoreObserve(t, c.store, id)
	same.Close()
	if sameIdentity != identity {
		t.Fatal("adoption changed original identity")
	}
	active, e := c.store.ReadActive()
	if e != nil || !reflect.DeepEqual(before, active) || !reflect.DeepEqual(protected, managedProtectedTree(t, paths)) {
		t.Fatal("adoption changed protected originals", e)
	}
	echo("after-adopt")
	recovered, made, e := a.Recover(applyCoreContext(t), recoveryRequest(t, "source-core-reproof", adopted.Record.Identity.OperationID, active.Revision))
	if e != nil || !made || recovered.Record.Phase != Committed {
		t.Fatal("v2 read-only recovery reproof", made, e)
	}
	stopped, made, e := a.Stop(applyCoreContext(t), lifecycleRequest(t, "source-core-stop", "runtime.stop", active.Revision))
	if e != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("v2 stop", made, e)
	}
	if e = first.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal("original source process did not exit", e)
	}
	active, e = c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	started, made, e := a.Start(applyCoreContext(t), lifecycleRequest(t, "source-core-start", "runtime.start", active.Revision))
	if e != nil || !made || started.Record.Phase != Committed {
		t.Fatal("v2 start from sealed source bundle", made, e)
	}
	var life lifecycleEvidence
	if e = json.Unmarshal(started.Record.Steps[len(started.Record.Steps)-1].Evidence, &life); e != nil || life.TargetRunID == nil {
		t.Fatal("new source run identity absent", e)
	}
	second, secondIdentity := sourceCoreObserve(t, c.store, *life.TargetRunID)
	handles[*life.TargetRunID] = second
	if *life.TargetRunID == id || secondIdentity == identity {
		t.Fatal("restarted source run reused identity")
	}
	echo("after-stored-start")
	if sourceCalls.Load() != 1 || echoes.Load() != 4 || connects.Load() < 4 {
		t.Fatal("unexpected compile/real-hop/forwarding count", sourceCalls.Load(), echoes.Load(), connects.Load())
	}
	active, e = c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	finalStop, made, e := a.Stop(applyCoreContext(t), lifecycleRequest(t, "source-core-final-stop", "runtime.stop", active.Revision))
	if e != nil || !made || finalStop.Record.Phase != Committed {
		t.Fatal("v2 final public stop", made, e)
	}
	if e = second.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal("restarted source process did not exit", e)
	}
	for _, pid := range []int{identity.PID, secondIdentity.PID} {
		if e = applyCoreAbsent(applyCoreContext(t), pid); e != nil {
			t.Fatal("stopped source process remains", e)
		}
	}
	t.Logf("compiled complete source bundle snapshot=%s; real original PID=%d -> restarted PID=%d; caller sources read once;4 native HTTP proxy forwards;writer reopen/adoption/reproof/stop/start verified;both public stops committed, original pidfds exited and proc absent;no cleanup signal needed", expectedSHA, identity.PID, secondIdentity.PID)
}
