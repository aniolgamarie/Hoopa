//go:build linux && amd64 && us4integration

package integration

// This harness uses named authenticated entry context, never a claimed source
// PID. All core mutations use the public source Plan/Apply and lifecycle APIs.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	"hoopa/internal/policy"
	owned "hoopa/internal/runtime"
	"hoopa/internal/service"
	"hoopa/internal/subscription"
	profile "hoopa/protocol/sourceprofile"
)

func appMust(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func appJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, e := json.Marshal(v)
	appMust(t, e)
	return raw
}
func appSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func appCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func appRequest(t *testing.T, id, operation string, params any) []byte {
	return appJSON(t, map[string]any{"api_version": "v1", "request_id": id, "operation": operation, "params": params})
}
func appAddress(t *testing.T) string {
	t.Helper()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	appMust(t, e)
	defer listener.Close()
	return listener.Addr().String()
}
func appEntry(t *testing.T, tag string) profile.Entry {
	t.Helper()
	_, text, _ := net.SplitHostPort(appAddress(t))
	port, _ := strconv.Atoi(text)
	return profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: uint16(port), Users: []profile.User{{Username: strings.Repeat(tag, 32), Password: strings.Repeat(tag+"p", 16)}}}
}

// The manager and launcher are real subprocesses of this test image. Neither
// dispatch supplies a fake material callback or pretends to be the core.
func TestAppRoutingManagerProcess(t *testing.T) {
	if os.Getenv("HOOPA_US4_MANAGER_CHILD") != "1" {
		return
	}
	var paths config.Paths
	appMust(t, json.Unmarshal([]byte(os.Getenv("HOOPA_US4_PATHS")), &paths))
	appMust(t, service.Run(context.Background(), paths, service.Options{LauncherArgs: []string{"-test.run=^TestAppRoutingLauncherProcess$", "-test.timeout=45s"}}))
}
func TestAppRoutingLauncherProcess(t *testing.T) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	appMust(t, mihomo.ExecBasicLauncher(ctx, os.NewFile(3, "birth"), os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE")))
	t.Fatal("core exec returned")
}

type appManager struct {
	cmd      *exec.Cmd
	done     chan error
	finished bool
	output   *os.File
}
type appExpectation struct {
	entry, path string
	hops        []string
}
type appTrace struct {
	mu         sync.Mutex
	next       int
	expected   map[string]appExpectation
	observed   map[string]int
	violations []string
	nonApp     int
}

func (tr *appTrace) token(entry, path string, hops []string) string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.next++
	token := fmt.Sprintf("us4-client-%05d", tr.next)
	tr.expected[token] = appExpectation{entry, path, append([]string{}, hops...)}
	return token
}
func (tr *appTrace) record(t *testing.T, r *http.Request) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	token, hop := r.Header.Get("X-US4-Client"), r.Header.Get("X-US4-Hop")
	if token == "" {
		tr.nonApp++
		t.Logf("non-app origin request method=%s host=%q path=%q hop=%q", r.Method, r.Host, r.URL.Path, hop)
		return
	}
	want, exists := tr.expected[token]
	valid := false
	if exists && want.path == r.URL.Path {
		for _, allowed := range want.hops {
			if hop == allowed {
				valid = true
			}
		}
	}
	if !valid {
		tr.violations = append(tr.violations, fmt.Sprintf("%s entry=%s path=%s hop=%s", token, want.entry, r.URL.Path, hop))
	}
	tr.observed[token]++
	t.Logf("actual app-context GET token=%s entry=%s host=%q path=%q hop=%q", token, want.entry, r.Host, r.URL.Path, hop)
}
func (tr *appTrace) success(t *testing.T, token string) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.observed[token] != 1 {
		t.Fatal("successful application response lacks unique origin witness", token)
	}
	if len(tr.violations) > 0 {
		t.Fatal("application hop violation", tr.violations)
	}
}
func (tr *appTrace) verify(t *testing.T) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.violations) > 0 {
		t.Fatal("including failed-client/retry application hop violations", tr.violations)
	}
	t.Logf("origin application tokens=%d non-app=%d (entry labels are not OS PID evidence)", len(tr.observed), tr.nonApp)
}

type appProxy struct {
	address, hop string
	server       *httptest.Server
	mu           sync.Mutex
	conns        map[net.Conn]bool
	wg           sync.WaitGroup
	closed       bool
}

func appProxyNew(t *testing.T, hop, origin string) *appProxy {
	t.Helper()
	p := &appProxy{hop: hop, conns: map[net.Conn]bool{}}
	_, originPort, _ := net.SplitHostPort(origin)
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, e := net.SplitHostPort(r.Host)
		if e != nil || port != originPort || (host != "localhost" && host != "127.0.0.1" && host != "127.0.0.2" && host != "::1") || r.Method != "CONNECT" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("upstream-user:upstream-password")) {
			http.Error(w, "fixture target denied", 403)
			return
		}
		conn, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			conn.Close()
			return
		}
		p.conns[conn] = true
		p.wg.Add(1)
		p.mu.Unlock()
		defer func() { conn.Close(); p.mu.Lock(); delete(p.conns, conn); p.mu.Unlock(); p.wg.Done() }()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = rw.Flush()
		for {
			_ = conn.SetDeadline(time.Now().Add(40 * time.Second))
			request, e := http.ReadRequest(rw.Reader)
			if e != nil {
				return
			}
			if request.Method != http.MethodGet && request.Method != http.MethodHead {
				request.Body.Close()
				return
			}
			request.Header.Set("X-US4-Hop", hop)
			request.Close = true
			up, e := net.DialTimeout("tcp4", origin, time.Second)
			if e != nil {
				request.Body.Close()
				return
			}
			_ = up.SetDeadline(time.Now().Add(5 * time.Second))
			e = request.Write(up)
			request.Body.Close()
			if e != nil {
				up.Close()
				return
			}
			response, e := http.ReadResponse(bufio.NewReader(up), request)
			if e != nil {
				up.Close()
				return
			}
			response.Close = false
			response.Header.Del("Connection")
			e = response.Write(conn)
			response.Body.Close()
			up.Close()
			if e != nil {
				return
			}
		}
	}))
	p.address = strings.TrimPrefix(p.server.URL, "http://")
	return p
}
func (p *appProxy) close() {
	p.mu.Lock()
	p.closed = true
	for c := range p.conns {
		c.Close()
	}
	p.mu.Unlock()
	p.server.Close()
	p.wg.Wait()
}

func (p *appProxy) restart(t *testing.T) {
	t.Helper()
	listener, e := net.Listen("tcp4", p.address)
	appMust(t, e)
	handler := p.server.Config.Handler
	p.mu.Lock()
	p.closed = false
	p.mu.Unlock()
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	p.server = server
}

type appHarness struct {
	t             *testing.T
	root, core    string
	paths         config.Paths
	store         *config.Store
	settings      control.SourceSettings
	runtime       policy.RuntimeInput
	entries       map[config.ID]profile.Entry
	intent        map[string]any
	sourceFiles   map[string]string
	proxies       []*appProxy
	origin        *httptest.Server
	originExtra   []*httptest.Server
	originAddress string
	trace         *appTrace
	manager       *appManager
	handles       map[config.RunID]*platform.ProcessHandle
	orphan        map[int]bool
	serial        int
}

func newAppHarness(t *testing.T) *appHarness {
	t.Helper()
	return newAppHarnessPrepared(t, true)
}

// Explicit settings/binding/provider prerequisites can be prepared independently
// of service startup. The established US4 harness keeps its default behavior.
func newAppHarnessPrepared(t *testing.T, startManager bool) *appHarness {
	t.Helper()
	var prior int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() {
		_, _, err := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if err != 0 {
			t.Error(err)
		}
	})
	root, e := os.MkdirTemp("/tmp", "hua-")
	appMust(t, e)
	h := &appHarness{t: t, root: root, entries: map[config.ID]profile.Entry{}, sourceFiles: map[string]string{}, trace: &appTrace{expected: map[string]appExpectation{}, observed: map[string]int{}}, handles: map[config.RunID]*platform.ProcessHandle{}, orphan: map[int]bool{}}
	t.Cleanup(h.cleanup)
	input := os.Getenv("HOOPA_P1_APP_CORE")
	if input == "" {
		t.Fatal("explicit separately approved app-routing core is required, cannot skip")
	}
	h.core = filepath.Join(root, "mihomo")
	capability, capErr := mihomo.ApprovedAppRoutingCapability(h.core)
	appMust(t, capErr)
	if capability.Validate() != nil || !capability.Accepted || capability.Binary.Path != h.core {
		t.Fatal("invalid app-routing approval record")
	}
	raw, e := os.ReadFile(input)
	appMust(t, e)
	if len(raw) > 128<<20 || appSHA(raw) != capability.Binary.SHA256 {
		t.Fatal("separately approved app-routing core checksum mismatch")
	}
	t.Logf("app-routing approved core SHA256=%s protocol=%d mode=%s", capability.Binary.SHA256, capability.Protocol, capability.Mode)
	appMust(t, os.WriteFile(h.core, raw, 0700))
	home := filepath.Join(root, "home")
	appMust(t, os.Mkdir(home, 0700))
	h.paths, e = config.ResolvePaths(home, map[string]string{"XDG_RUNTIME_DIR": filepath.Join(root, "run")})
	appMust(t, e)
	h.origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		h.trace.record(t, r)
		_, _ = io.WriteString(w, r.Header.Get("X-US4-Hop")+":"+r.URL.Path)
	}))
	h.originAddress = strings.TrimPrefix(h.origin.URL, "http://")
	_, originPort, _ := net.SplitHostPort(h.originAddress)
	literalListener, listenErr := net.Listen("tcp4", net.JoinHostPort("127.0.0.2", originPort))
	appMust(t, listenErr)
	literalOrigin := httptest.NewUnstartedServer(h.origin.Config.Handler)
	literalOrigin.Listener.Close()
	literalOrigin.Listener = literalListener
	literalOrigin.Start()
	h.originExtra = append(h.originExtra, literalOrigin)
	for _, hop := range []string{"A-US", "B-JP", "A-PIN"} {
		h.proxies = append(h.proxies, appProxyNew(t, hop, h.originAddress))
	}
	shared := appEntry(t, "s")
	shared.Name = "shared"
	h.runtime = policy.RuntimeInput{Controller: appAddress(t), Secret: strings.Repeat("c", 32), Entry: shared, AppEntries: map[config.ID]profile.Entry{}}
	h.settings, e = control.NewSourceSettings(bytes.Repeat([]byte{0x48}, 32), policy.RuntimeInput{Controller: h.runtime.Controller, Secret: h.runtime.Secret, Entry: shared})
	appMust(t, e)
	c := h.open()
	_, e = c.InitializeSourceSettings(appCtx(t), h.activeWith(c).Revision, h.settings)
	appMust(t, e)
	for i, id := range []config.ID{"app-a", "app-b"} {
		entry := appEntry(t, string(rune('a'+i)))
		for {
			_, ct, _ := net.SplitHostPort(h.runtime.Controller)
			cp, _ := strconv.Atoi(ct)
			collision := entry.Port == h.runtime.Entry.Port || int(entry.Port) == cp
			for _, prior := range h.entries {
				if prior.Port == entry.Port {
					collision = true
				}
			}
			if !collision {
				break
			}
			entry = appEntry(t, string(rune('a'+i)))
		}
		binding, e := control.NewAppEntryBinding(id, entry, h.settings)
		appMust(t, e)
		_, e = c.InitializeAppEntry(appCtx(t), h.activeWith(c).Revision, binding)
		appMust(t, e)
		entry.Name = policy.AppEntryAlias(id)
		h.entries[id] = entry
		h.runtime.AppEntries[id] = entry
	}
	definitions := []any{}
	snapshots := map[string]subscription.Snapshot{}
	key, _ := h.settings.SensitiveIdentityKey()
	regionRules := []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "JP ", Region: "JP"}}
	updater, e := control.NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	appMust(t, e)
	for _, id := range []string{"source-a", "source-b"} {
		nodes := []any{}
		selected := []*appProxy{h.proxies[0], h.proxies[2]}
		if id == "source-b" {
			selected = []*appProxy{h.proxies[1]}
		}
		for _, proxy := range selected {
			host, text, _ := net.SplitHostPort(proxy.address)
			port, _ := strconv.Atoi(text)
			name := "US shared"
			if proxy.hop == "B-JP" {
				name = "JP shared"
			}
			if proxy.hop == "A-PIN" {
				name = "US pin"
			}
			nodes = append(nodes, map[string]any{"name": name, "type": "http", "server": host, "port": port, "username": "upstream-user", "password": "upstream-password"})
		}
		provider := appJSON(t, map[string]any{"proxies": nodes})
		path := filepath.Join(root, id+".json")
		appMust(t, os.WriteFile(path, provider, 0600))
		h.sourceFiles[id] = path
		definition := config.Subscription{ID: config.ID(id), Source: config.SubscriptionSource{Kind: "file", Path: path}}
		definitions = append(definitions, definition)
		intent := appBaseIntent(t, h.core)
		intent["subscriptions"] = []any{definition}
		intent["selection"] = map[string]any{"active_subscription": id}
		request := appRequest(t, "cache-"+id, "subscription.update", map[string]any{"subscription_id": id, "expected_revision": h.activeWith(c).Revision, "intent": intent, "region_rules": regionRules})
		_, made, e := updater.Update(appCtx(t), request)
		appMust(t, e)
		if !made {
			t.Fatal("no source reservation")
		}
		h.waitOperation(request, control.Committed)
		snapshot, e := subscription.Parse(provider, subscription.ParseOptions{SourceID: config.ID(id), IdentityKey: key, RegionRules: regionRules})
		appMust(t, e)
		snapshots[id] = snapshot
	}
	updater.Close()
	appMust(t, c.Close())
	h.store, e = config.OpenStore(h.paths, false)
	appMust(t, e)
	find := func(source, name string) config.ID {
		for _, node := range snapshots[source].Nodes {
			if node.DisplayName == name {
				return node.ID
			}
		}
		t.Fatal("node fixture missing")
		return ""
	}
	h.intent = appBaseIntent(t, h.core)
	h.intent["subscriptions"] = definitions
	h.intent["selection"] = map[string]any{"active_subscription": "source-a"}
	h.intent["apps"] = []any{map[string]any{"id": "app-a", "mode": "managed", "metering": false, "bypass": []any{}}, map[string]any{"id": "app-b", "mode": "managed", "metering": false, "bypass": []any{}}}
	h.intent["groups"] = []any{map[string]any{"id": "US", "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "source-a"}, "selected_node_id": find("source-a", "US shared")}, map[string]any{"id": "JP", "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "source-b"}, "selected_node_id": find("source-b", "JP shared")}}
	h.intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "source-a", "node_id": find("source-a", "US pin")}
	h.rules("app-first")
	active := h.active()
	if active.Generation != nil || active.DesiredRuntime != "stopped" {
		t.Fatal("explicit input initialization changed runtime")
	}
	runs, e := h.store.ListRunIDs()
	appMust(t, e)
	if len(runs) != 0 {
		t.Fatal("input preparation created runtime launch intentions")
	}
	for _, entry := range h.entries {
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort(entry.Listen, strconv.Itoa(int(entry.Port))), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatal("entry opened before complete Apply")
		}
	}
	if startManager {
		h.startManager()
	}
	return h
}
func appBaseIntent(t *testing.T, core string) map[string]any {
	cap, e := mihomo.ApprovedAppRoutingCapability(core)
	appMust(t, e)
	return map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": cap.Binary}, "subscriptions": []any{}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
}
func (h *appHarness) open() *control.Coordinator {
	c, e := control.OpenCoordinator(h.paths, func(json.RawMessage, []control.PlanInput) error { return control.ErrCapabilityUnsupported }, func(context.Context, control.Plan) (control.CurrentPlanInputs, error) {
		return control.CurrentPlanInputs{}, control.ErrCapabilityUnsupported
	})
	appMust(h.t, e)
	return c
}
func (h *appHarness) activeWith(_ *control.Coordinator) config.ActivePointer {
	s, e := config.OpenStore(h.paths, false)
	appMust(h.t, e)
	defer s.Close()
	active, e := s.ReadActive()
	appMust(h.t, e)
	return active
}
func (h *appHarness) active() config.ActivePointer {
	active, e := h.store.ReadActive()
	appMust(h.t, e)
	return active
}
func (h *appHarness) waitOperation(raw []byte, want control.Phase) control.StoredOperation {
	h.t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for {
		op, e := control.ReadReplay(h.paths, raw)
		if e == nil && (op.Record.Phase == control.Committed || op.Record.Phase == control.RolledBack) {
			if op.Record.Phase != want {
				h.t.Fatal("unexpected durable terminal phase", op.Record.Phase, want)
			}
			return op
		}
		if time.Now().After(deadline) {
			h.t.Fatal("source operation never terminal", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (h *appHarness) id(label string) string {
	h.serial++
	return fmt.Sprintf("us4-%03d-%s", h.serial, label)
}
func (h *appHarness) call(operation string, params any) (json.RawMessage, string, []byte) {
	h.t.Helper()
	raw := appRequest(h.t, h.id(operation), operation, params)
	response, e := service.Call(appCtx(h.t), h.paths, raw)
	appMust(h.t, e)
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	appMust(h.t, json.Unmarshal(response, &envelope))
	if envelope.Error != nil {
		return nil, envelope.Error.Code, raw
	}
	if !envelope.OK {
		h.t.Fatal("malformed service response")
	}
	return envelope.Data, "", raw
}
func (h *appHarness) startManager() {
	h.t.Helper()
	if h.manager != nil && !h.manager.finished {
		h.t.Fatal("manager already running")
	}
	image, e := os.Executable()
	appMust(h.t, e)
	output, e := os.OpenFile(filepath.Join(h.root, h.id("manager")+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	appMust(h.t, e)
	cmd := exec.Command(image, "-test.run=^TestAppRoutingManagerProcess$", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "HOOPA_US4_MANAGER_CHILD=1", "HOOPA_US4_PATHS="+string(appJSON(h.t, h.paths)))
	cmd.Stdout = output
	cmd.Stderr = output
	m := &appManager{cmd: cmd, done: make(chan error, 1), output: output}
	appMust(h.t, cmd.Start())
	go func() { m.done <- cmd.Wait() }()
	h.manager = m
	deadline := time.Now().Add(10 * time.Second)
	for {
		connection, readyErr := service.DialEndpoint(appCtx(h.t), h.paths)
		e = readyErr
		if e == nil {
			connection.Close()
			return
		}
		select {
		case err := <-m.done:
			m.finished = true
			h.t.Fatal("manager exited before ready", err)
		default:
		}
		if time.Now().After(deadline) {
			h.t.Fatal("manager readiness unavailable", e)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func (h *appHarness) waitManager(killed bool) {
	h.t.Helper()
	m := h.manager
	select {
	case e := <-m.done:
		m.finished = true
		if killed {
			var exit *exec.ExitError
			if !errors.As(e, &exit) {
				h.t.Fatal("no manager SIGKILL evidence")
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				h.t.Fatal("wrong manager death")
			}
		} else {
			appMust(h.t, e)
		}
	case <-time.After(10 * time.Second):
		h.t.Fatal("manager did not exit")
	}
	m.output.Close()
}
func (h *appHarness) stopManager() {
	_, code, _ := h.call("service.stop", map[string]any{"expected_revision": h.active().Revision})
	if code != "" {
		h.t.Fatal(code)
	}
	h.waitManager(false)
}
func (h *appHarness) rules(order string) {
	appRule := func(id, group string) map[string]any {
		return map[string]any{"id": "app-" + id, "match": map[string]any{"kind": "app", "app_id": "app-" + id}, "target": map[string]any{"kind": "group", "group_id": group}}
	}
	domain := map[string]any{"id": "domain", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "LOCALHOST."}, "target": h.intent["default_target"]}
	ip := map[string]any{"id": "ip", "match": map[string]any{"kind": "ip_cidr", "value": "127.0.0.1/32"}, "target": map[string]any{"kind": "group", "group_id": "JP"}}
	apps := []any{appRule("a", "US"), appRule("b", "JP")}
	switch order {
	case "app-first":
		h.intent["rules"] = append(apps, domain, ip)
	case "domain-first":
		h.intent["rules"] = append([]any{domain, ip}, apps...)
	case "ip-first":
		h.intent["rules"] = append([]any{ip, domain}, apps...)
	default:
		h.t.Fatal("unknown rule order")
	}
}
func (h *appHarness) plan() control.PlanDocument {
	h.t.Helper()
	before := h.tree()
	raw, code, _ := h.call("config.plan", map[string]any{"intent": h.intent})
	if code != "" {
		h.t.Fatal("source Plan refused", code)
	}
	if !reflect.DeepEqual(before, h.tree()) {
		h.t.Fatal("pure source Plan mutated files")
	}
	var doc control.PlanDocument
	appMust(h.t, json.Unmarshal(raw, &doc))
	if !doc.Impact.RequiresDisruption || doc.Impact.ConnectionEffect != "interrupt" || doc.Impact.TargetGeneration == "" {
		h.t.Fatal("missing explicit interruption preview")
	}
	want := []string{}
	for _, item := range h.intent["apps"].([]any) {
		if item.(map[string]any)["mode"] == "managed" {
			want = append(want, item.(map[string]any)["id"].(string))
		}
	}
	sort.Strings(want)
	got := append([]string{}, doc.Impact.AffectedApps...)
	sort.Strings(got)
	if !reflect.DeepEqual(want, got) {
		h.t.Fatal("app impact preview incomplete", got, want)
	}
	return doc
}
func (h *appHarness) apply(doc control.PlanDocument, accept bool) control.StoredOperation {
	h.t.Helper()
	_, code, request := h.call("config.apply", map[string]any{"plan": doc, "confirmed_plan_digest": doc.PlanDigest, "accept_disruption": accept})
	op, e := control.ReadReplay(h.paths, request)
	appMust(h.t, e)
	if code != "" && op.Record.Phase != control.RolledBack {
		h.t.Fatal("source Apply refused", code)
	}
	if op.Record.Phase != control.Committed && op.Record.Phase != control.RolledBack {
		h.t.Fatal("Apply not terminal")
	}
	return op
}
func (h *appHarness) run() (config.RunID, *platform.ProcessHandle) {
	h.t.Helper()
	active := h.active()
	if active.Generation == nil {
		h.t.Fatal("no active generation")
	}
	cap, e := mihomo.ApprovedAppRoutingCapability(h.core)
	appMust(h.t, e)
	controller, e := mihomo.NewController(h.store, cap)
	appMust(h.t, e)
	owner, e := owned.NewOwnership(h.store, controller)
	appMust(h.t, e)
	ids, e := h.store.ListRunIDs()
	appMust(h.t, e)
	for _, id := range ids {
		intent, _, e := h.store.ReadRunIntent(id)
		if e != nil || intent.Generation != *active.Generation {
			continue
		}
		run, e := owner.Observe(appCtx(h.t), id)
		if e != nil {
			continue
		}
		if run.Observation().Gate != "open" {
			run.Close()
			continue
		}
		birth, _, e := h.store.ReadRunBirth(id)
		appMust(h.t, e)
		handle, e := platform.ObserveProcess(appCtx(h.t), birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
		appMust(h.t, e)
		appMust(h.t, handle.VerifyArguments(appCtx(h.t), intent.Arguments))
		if handle.Identity() != run.Identity() {
			h.t.Fatal("ownership/PID identity mismatch")
		}
		run.Close()
		if old := h.handles[id]; old != nil {
			handle.Close()
			return id, old
		}
		h.handles[id] = handle
		return id, handle
	}
	h.t.Fatal("no independently verified active core")
	return "", nil
}
func (h *appHarness) exit(handle *platform.ProcessHandle) {
	h.t.Helper()
	appMust(h.t, handle.WaitExited(appCtx(h.t)))
	pid := handle.Identity().PID
	deadline := time.Now().Add(8 * time.Second)
	for {
		// The verified pidfd has already reported exit. A manager may have
		// exited gracefully too, adopting this exact child into our subreaper.
		// Reap only this PID; never wait-any or signal a process here.
		var status syscall.WaitStatus
		got, waitErr := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if got == pid {
			delete(h.orphan, pid)
		}
		if waitErr != nil && !errors.Is(waitErr, syscall.ECHILD) {
			h.t.Fatal(waitErr)
		}
		_, e := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		if errors.Is(e, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			procStat, statErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
			h.t.Fatalf("original core PID still exists %d; proc/stat=%q; read_error=%v", pid, procStat, statErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (h *appHarness) tree() map[string]string {
	h.t.Helper()
	out := map[string]string{}
	for scope, root := range map[string]string{"state": h.paths.State, "config": h.paths.Config, "cache": h.paths.Cache} {
		appMust(h.t, filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if errors.Is(e, os.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			rel, _ := filepath.Rel(root, path)
			value := fmt.Sprintf("%v:%d", info.Mode(), info.ModTime().UnixNano())
			if info.Mode().IsRegular() {
				raw, e := os.ReadFile(path)
				if e != nil {
					return e
				}
				value += ":" + appSHA(raw)
			}
			out[scope+"/"+rel] = value
			return nil
		}))
	}
	return out
}
func (h *appHarness) rejectPlan() {
	h.t.Helper()
	before := h.tree()
	_, code, _ := h.call("config.plan", map[string]any{"intent": h.intent})
	if code == "" || !reflect.DeepEqual(before, h.tree()) {
		h.t.Fatal("invalid candidate Plan accepted or mutated runtime", code)
	}
}
func (h *appHarness) bindingPath(id config.ID) string {
	return filepath.Join(h.paths.State, "credentials", "app-entry-"+appSHA([]byte(id))+".json")
}
func (h *appHarness) initialize(id config.ID, entry profile.Entry) error {
	c := h.open()
	defer c.Close()
	binding, e := control.NewAppEntryBinding(id, entry, h.settings)
	if e != nil {
		return e
	}
	_, e = c.InitializeAppEntry(appCtx(h.t), h.activeWith(c).Revision, binding)
	return e
}
func (h *appHarness) assertProfile() {
	h.t.Helper()
	active := h.active()
	bundle, e := h.store.ReadBundle(*active.Generation)
	appMust(h.t, e)
	cfg, e := profile.Validate(bundle.Files["core.json"], string(*active.Generation))
	appMust(h.t, e)
	if len(cfg.Listeners) != len(h.intent["apps"].([]any))+1 {
		h.t.Fatal("managed entry missing or duplicated")
	}
	names := map[string]bool{}
	for _, entry := range cfg.Listeners {
		names[entry.Name] = true
		if entry.Name == h.runtime.Entry.Name {
			if !reflect.DeepEqual(entry, h.runtime.Entry) {
				h.t.Fatal("shared entry changed")
			}
			continue
		}
		matched := false
		for id, want := range h.entries {
			if entry.Name == policy.AppEntryAlias(id) {
				want.Name = policy.AppEntryAlias(id)
				if !reflect.DeepEqual(entry, want) {
					h.t.Fatal("compiled app binding changed endpoint/credentials")
				}
				matched = true
			}
		}
		if !matched {
			h.t.Fatal("unexpected managed listener")
		}
	}
	for _, item := range h.intent["apps"].([]any) {
		id := config.ID(item.(map[string]any)["id"].(string))
		if !names[policy.AppEntryAlias(id)] {
			h.t.Fatal("app listener alias missing")
		}
	}
	var raw map[string]json.RawMessage
	appMust(h.t, json.Unmarshal(bundle.Files["core.json"], &raw))
	var listeners []map[string]any
	appMust(h.t, json.Unmarshal(raw["listeners"], &listeners))
	for _, entry := range listeners {
		if _, override := entry["proxy"]; override {
			h.t.Fatal("listener-level override bypasses ordered rules")
		}
	}
	parsed, parseErr := config.ParseIntent(appJSON(h.t, h.intent), config.IntentApply)
	appMust(h.t, parseErr)
	requiredAtoms := map[config.ID]bool{}
	var collect func(config.Predicate)
	collect = func(p config.Predicate) {
		if p.AppID != nil {
			requiredAtoms[*p.AppID] = true
		}
		for _, child := range p.Conditions {
			collect(child)
		}
	}
	for _, rule := range parsed.Rules {
		collect(rule.Match)
	}
	for app := range requiredAtoms {
		found := false
		for _, rule := range cfg.Rules {
			if strings.Contains(rule, "IN-NAME,"+policy.AppEntryAlias(app)) {
				found = true
			}
		}
		if !found {
			h.t.Fatal("ordered app atom missing")
		}
	}

}

func (h *appHarness) cleanup() {
	t := h.t
	if h.manager != nil && !h.manager.finished {
		_ = h.manager.cmd.Process.Kill()
		select {
		case <-h.manager.done:
			h.manager.finished = true
		case <-time.After(5 * time.Second):
			t.Error("owned manager emergency cleanup timed out")
		}
		h.manager.output.Close()
	}
	if h.store != nil {
		ids, e := h.store.ListRunIDs()
		if e == nil {
			for _, id := range ids {
				intent, _, ie := h.store.ReadRunIntent(id)
				birth, _, be := h.store.ReadRunBirth(id)
				if ie != nil || be != nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				handle, e := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
				if e == nil {
					if handle.VerifyArguments(ctx, intent.Arguments) == nil {
						t.Error("passing path left core requiring emergency verified cleanup", id)
						_ = handle.SignalVerified(ctx, syscall.SIGKILL)
						_ = handle.WaitExited(ctx)
					}
					handle.Close()
				}
				cancel()
			}
		}
		h.store.Close()
	}
	for _, handle := range h.handles {
		handle.Close()
	}
	for _, proxy := range h.proxies {
		proxy.close()
	}
	for _, origin := range h.originExtra {
		origin.Close()
	}
	if h.origin != nil {
		h.origin.Close()
	}
	if evidence := os.Getenv("HOOPA_US4_EVIDENCE_DIR"); evidence != "" {
		// Explicit test-only retention happens after the original cleanup and
		// ownership checks. Never overwrite another run's evidence directory.
		name := t.Name()
		if !filepath.IsAbs(evidence) || filepath.Clean(evidence) != evidence || filepath.IsAbs(name) || filepath.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			t.Error("invalid test evidence directory/name")
			return
		}
		destination := filepath.Join(evidence, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Error("test evidence parent creation failed", err)
			return
		}
		oldName, err := syscall.BytePtrFromString(h.root)
		if err != nil {
			t.Error("test evidence source path invalid", err)
			return
		}
		newName, err := syscall.BytePtrFromString(destination)
		if err != nil {
			t.Error("test evidence destination path invalid", err)
			return
		}
		// Linux/amd64 renameat2(316), AT_FDCWD=-100, RENAME_NOREPLACE=1.
		// The standard syscall package is frozen and omits this newer number.
		cwd := ^uintptr(99)
		_, _, errno := syscall.Syscall6(316, cwd, uintptr(unsafe.Pointer(oldName)), cwd, uintptr(unsafe.Pointer(newName)), 1, 0)
		if errno != 0 {
			t.Error("test evidence retention failed", errno)
			return
		}
		t.Logf("retained fixture evidence %s -> %s", h.root, destination)
		return
	}
	_ = os.RemoveAll(h.root)
}

// Owned token expectations inspect actual origin observations even when a
// request fails at the client. A15s negative bound never counts as core refusal.
func (h *appHarness) get(id config.ID, path string, want ...string) (string, error) {
	return h.getHost(id, "localhost", path, want...)
}
func (h *appHarness) getHost(id config.ID, host, path string, want ...string) (string, error) {
	entry := h.runtime.Entry
	if id != "" {
		entry = h.entries[id]
	}
	proxy := &url.URL{Scheme: "http", Host: net.JoinHostPort(entry.Listen, strconv.Itoa(int(entry.Port))), User: url.UserPassword(entry.Users[0].Username, entry.Users[0].Password)}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	token := h.trace.token(string(id), path, want)
	request, e := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(host, strings.Split(h.originAddress, ":")[1])+path, nil)
	if e != nil {
		return "", e
	}
	request.Header.Set("X-US4-Client", token)
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	response, e := client.Do(request)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 512))
	if e != nil {
		return "", e
	}
	if response.StatusCode != 200 {
		return "", appHTTPRejection(response.StatusCode)
	}
	h.trace.success(h.t, token)
	return string(body), nil
}

type appHTTPRejection int

func (e appHTTPRejection) Error() string { return fmt.Sprintf("proxy refusal %d", int(e)) }
func appTerminal(t *testing.T, e error) {
	t.Helper()
	if e == nil || errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) || os.IsTimeout(e) {
		t.Fatal("no terminal core failure (client timeout/cancel never proves rejection)", e)
	}
	var response appHTTPRejection
	if errors.As(e, &response) || errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) || errors.Is(e, syscall.ECONNRESET) || errors.Is(e, syscall.ECONNREFUSED) || errors.Is(e, syscall.EPIPE) {
		return
	}
	t.Fatal("unclassified refusal", e)
}
func (h *appHarness) fresh(id config.ID, path, hop string) {
	h.t.Helper()
	body, e := h.get(id, path, hop)
	appMust(h.t, e)
	if body != hop+":"+path {
		h.t.Fatal("wrong actual entry route", body, hop)
	}
}

type appHeld struct {
	h      *appHarness
	entry  config.ID
	conn   net.Conn
	reader *bufio.Reader
}

func (h *appHarness) hold(id config.ID) *appHeld {
	h.t.Helper()
	entry := h.entries[id]
	conn, e := net.DialTimeout("tcp4", net.JoinHostPort(entry.Listen, strconv.Itoa(int(entry.Port))), time.Second)
	appMust(h.t, e)
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	target := "localhost:" + strings.Split(h.originAddress, ":")[1]
	_, e = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, base64.StdEncoding.EncodeToString([]byte(entry.Users[0].Username+":"+entry.Users[0].Password)))
	appMust(h.t, e)
	reader := bufio.NewReader(conn)
	response, e := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	appMust(h.t, e)
	if response.StatusCode != 200 {
		conn.Close()
		h.t.Fatal("held CONNECT refused")
	}
	held := &appHeld{h, id, conn, reader}
	h.t.Cleanup(func() { conn.Close() })
	return held
}
func (c *appHeld) exchange(path string, hops ...string) error {
	token := c.h.trace.token(string(c.entry), path, hops)
	_ = c.conn.SetDeadline(time.Now().Add(15 * time.Second))
	_, e := fmt.Fprintf(c.conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nX-US4-Client: %s\r\n\r\n", path, token)
	if e != nil {
		return e
	}
	response, e := http.ReadResponse(c.reader, &http.Request{Method: http.MethodGet})
	if e != nil {
		return e
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 512))
	if e != nil {
		return e
	}
	if response.StatusCode != 200 {
		return appHTTPRejection(response.StatusCode)
	}
	c.h.trace.success(c.h.t, token)
	for _, hop := range hops {
		if string(body) == hop+":"+path {
			return nil
		}
	}
	return errors.New("wrong held application hop")
}

func (h *appHarness) finish() {
	h.t.Helper()
	_, handle := h.run()
	_, code, request := h.call("runtime.stop", map[string]any{"expected_revision": h.active().Revision, "accept_disruption": true})
	if code != "" {
		h.t.Fatal("public runtime Stop", code)
	}
	op, e := control.ReadReplay(h.paths, request)
	appMust(h.t, e)
	if op.Record.Phase != control.Committed {
		h.t.Fatal("public Stop not committed")
	}
	h.exit(handle)
	h.trace.verify(h.t)
	h.stopManager()
}
func (h *appHarness) cloneIntent() map[string]any {
	var result map[string]any
	appMust(h.t, json.Unmarshal(appJSON(h.t, h.intent), &result))
	return result
}
func (h *appHarness) assertUnchangedApply(doc control.PlanDocument, accept bool) {
	h.t.Helper()
	before := h.tree()
	_, code, request := h.call("config.apply", map[string]any{"plan": doc, "confirmed_plan_digest": doc.PlanDigest, "accept_disruption": accept})
	if code == "" || !reflect.DeepEqual(before, h.tree()) {
		h.t.Fatal("admission rejection mutated durable/runtime resources", code)
	}
	if _, err := control.ReadReplay(h.paths, request); !errors.Is(err, control.ErrOperationNotFound) {
		h.t.Fatal("rejected admission created operation", err)
	}
}
func (h *appHarness) assertStoppedRun(id config.RunID) {
	h.t.Helper()
	intent, _, e := h.store.ReadRunIntent(id)
	appMust(h.t, e)
	birth, _, e := h.store.ReadRunBirth(id)
	appMust(h.t, e)
	if intent.RunID != id || birth.RunID != id || birth.PID <= 0 || birth.StartTicks == 0 {
		h.t.Fatal("candidate exit lacks retained original birth")
	}
	if _, e = os.Stat(filepath.Join("/proc", strconv.Itoa(birth.PID))); !errors.Is(e, os.ErrNotExist) {
		h.t.Fatal("failed candidate original PID still exists", e)
	}
}

func (h *appHarness) candidateListenerOrder(id config.RunID) {
	h.t.Helper()
	intent, _, e := h.store.ReadRunIntent(id)
	appMust(h.t, e)
	raw, e := os.ReadFile(filepath.Join(intent.Workdir, "config.json"))
	appMust(h.t, e)
	if appSHA(raw) != intent.ConfigSHA256 {
		h.t.Fatal("candidate config not bound to original intent")
	}
	cfg, e := profile.Validate(raw, string(intent.Generation))
	appMust(h.t, e)
	want := []string{h.runtime.Entry.Name, policy.AppEntryAlias("app-a"), policy.AppEntryAlias("app-b"), policy.AppEntryAlias("app-z")}
	got := []string{}
	for _, entry := range cfg.Listeners {
		got = append(got, entry.Name)
	}
	if !reflect.DeepEqual(got, want) {
		h.t.Fatal("foreign endpoint is not the last declared binding", got, want)
	}
}
