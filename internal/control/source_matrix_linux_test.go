//go:build linux && amd64

package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
	profile "hoopa/protocol/sourceprofile"
)

// App requests have an owned expectation before they enter the core. Origin
// observations are checked independently of whether the client gets a response.
type matrixAppExpectation struct {
	path string
	hops []string
}
type matrixGETTrace struct {
	mu            sync.Mutex
	next          uint64
	expected      map[string]matrixAppExpectation
	apps, builtin int
	violations    []string
}

func (tr *matrixGETTrace) register(path string, hops []string) string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.next++
	token := fmt.Sprintf("matrix-app-%04d", tr.next)
	tr.expected[token] = matrixAppExpectation{path: path, hops: append([]string{}, hops...)}
	return token
}
func (tr *matrixGETTrace) observe(t *testing.T, r *http.Request, hop string) {
	t.Helper()
	token := r.Header.Get("X-Matrix-Client")
	path := strings.TrimPrefix(r.URL.Path, "/")
	tr.mu.Lock()
	defer tr.mu.Unlock()
	kind := "non-app"
	if token != "" {
		kind = "app"
		tr.apps++
		expect, ok := tr.expected[token]
		valid := false
		if ok && expect.path == path {
			for _, allowed := range expect.hops {
				if hop == allowed {
					valid = true
				}
			}
		}
		if !valid {
			tr.violations = append(tr.violations, fmt.Sprintf("token=%s path=%s hop=%s", token, path, hop))
		}
	} else {
		tr.builtin++
	}
	t.Logf("origin GET kind=%s client=%q host=%q path=%q hop=%q", kind, token, r.Host, r.URL.Path, hop)
}
func (tr *matrixGETTrace) verify(t *testing.T) {
	t.Helper()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.violations) != 0 {
		t.Fatal("origin application route violations", tr.violations)
	}
	t.Logf("origin GET classified app=%d non-app=%d all_app_hops_verified=true", tr.apps, tr.builtin)
}

type matrixHead struct {
	hop    string
	status int
}
type matrixProbe struct {
	status  atomic.Int64
	entered chan string
	done    chan matrixHead
	release chan struct{}
	once    sync.Once
}
type matrixProxy struct {
	address, hop string
	server       *httptest.Server
	handler      http.Handler
}

// The fixture permits only these loopback targets on the one controlled origin
// port. It parses the first tunneled request and injects a hop marker. A HEAD
// cannot serve as application evidence: only origin GET bodies are asserted.
func matrixProxyNew(t *testing.T, hop, target string) *matrixProxy {
	t.Helper()
	_, port, _ := net.SplitHostPort(target)
	p := &matrixProxy{hop: hop}
	p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, pnum, e := net.SplitHostPort(r.Host)
		valid := false
		for i := 1; i <= 8; i++ {
			if host == fmt.Sprintf("127.0.0.%d", i) {
				valid = true
			}
		}
		if r.Method != "CONNECT" || e != nil || pnum != port || !valid || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("matrix-user:matrix-password")) {
			http.Error(w, "denied", 403)
			return
		}
		conn, rw, e := w.(http.Hijacker).Hijack()
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		_, _ = rw.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = rw.Flush()
		request, e := http.ReadRequest(rw.Reader)
		if e != nil {
			return
		}
		defer request.Body.Close()
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			return
		}
		request.Header.Set("X-Matrix-Hop", hop)
		request.Header.Del("Proxy-Authorization")
		request.Close = true
		upstream, e := net.DialTimeout("tcp4", target, time.Second)
		if e != nil {
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(8 * time.Second))
		if request.Write(upstream) != nil {
			return
		}
		response, e := http.ReadResponse(bufio.NewReader(upstream), request)
		if e != nil {
			return
		}
		defer response.Body.Close()
		_ = response.Write(conn)
	})
	p.server = httptest.NewServer(p.handler)
	p.address = strings.TrimPrefix(p.server.URL, "http://")
	t.Cleanup(func() { p.server.Close() })
	return p
}
func (p *matrixProxy) restart(t *testing.T) {
	t.Helper()
	listener, e := net.Listen("tcp4", p.address)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(p.handler)
	s.Listener.Close()
	s.Listener = listener
	s.Start()
	p.server = s
}
func matrixProvider(t *testing.T, proxies []*matrixProxy, renamed bool) []byte {
	t.Helper()
	nodes := []any{}
	for _, p := range proxies {
		host, port, _ := net.SplitHostPort(p.address)
		n, _ := strconv.Atoi(port)
		region := "US "
		if strings.HasSuffix(p.hop, "JP") {
			region = "JP "
		}
		name := region + "shared"
		if renamed {
			name = region + "renamed"
		}
		nodes = append(nodes, map[string]any{"name": name, "type": "http", "server": host, "port": n, "username": "matrix-user", "password": "matrix-password"})
	}
	return planRaw(t, map[string]any{"proxies": nodes})
}

type matrixSources struct {
	mu      sync.Mutex
	raw     map[string][]byte
	status  map[string]int
	gate    chan struct{}
	entered chan string
	calls   map[string]int
	server  *httptest.Server
}

func matrixSourcesNew(t *testing.T) *matrixSources {
	f := &matrixSources{raw: map[string][]byte{}, status: map[string]int{}, calls: map[string]int{}, entered: make(chan string, 16)}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		raw := bytes.Clone(f.raw[r.URL.Path])
		status := f.status[r.URL.Path]
		gate := f.gate
		f.calls[r.URL.Path]++
		f.mu.Unlock()
		if gate != nil {
			select {
			case f.entered <- r.URL.Path:
			case <-r.Context().Done():
				return
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *matrixSources) set(path string, raw []byte, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw[path] = bytes.Clone(raw)
	f.status[path] = status
}
func (f *matrixSources) definition(t *testing.T, path string) config.SubscriptionSource {
	t.Helper()
	secret := filepath.Join(t.TempDir(), "url-secret")
	if e := os.WriteFile(secret, []byte(f.server.URL+path), 0600); e != nil {
		t.Fatal(e)
	}
	return config.SubscriptionSource{Kind: "https", URLSecretRef: secret}
}
func matrixAwaitHeads(t *testing.T, p *matrixProbe, hops []string, status int) {
	t.Helper()
	need := map[string]bool{}
	for _, h := range hops {
		need[h] = true
	}
	deadline := time.After(8 * time.Second)
	for len(need) > 0 {
		select {
		case event := <-p.done:
			if event.status == status {
				delete(need, event.hop)
				t.Logf("completed actual HEAD hop=%s status=%d", event.hop, event.status)
			}
		case <-deadline:
			t.Fatal("missing actual completed HEAD", need, status)
		}
	}
}
func matrixGET(trace *matrixGETTrace, entry, target, path string, allowed ...string) (string, error) {
	return matrixGETBound(trace, entry, target, path, time.Second, allowed...)
}

// Native TCP dialing has a shared 5s context and at most 10 retry attempts
// (candidate source tunnel/tunnel.go and component/dialer/dialer.go). A 15s
// negative client bound outlasts that path; hitting it is a test failure, never
// proof that the core gave a terminal rejection.
func matrixGETBound(trace *matrixGETTrace, entry, target, path string, timeout time.Duration, allowed ...string) (string, error) {
	proxyURL := &url.URL{Scheme: "http", Host: entry, User: url.UserPassword(strings.Repeat("u", 32), strings.Repeat("p", 32))}
	tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: timeout}
	request, err := http.NewRequest(http.MethodGet, "http://"+target+"/"+path, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("X-Matrix-Client", trace.register(path, allowed))
	r, e := client.Do(request)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	body, e := io.ReadAll(io.LimitReader(r.Body, 256))
	if e != nil {
		return "", e
	}
	if r.StatusCode != 200 {
		return "", matrixHTTPRejection(r.StatusCode)
	}
	return string(body), nil
}
func matrixRoute(t *testing.T, trace *matrixGETTrace, entry, target, path string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(7 * time.Second)
	for {
		body, e := matrixGET(trace, entry, target, path, want...)
		if e == nil {
			for _, hop := range want {
				if body == hop+":"+path {
					t.Logf("application GET path=%s marker=%s", path, body)
					return
				}
			}
			t.Fatal("unexpected real GET hop", body, want)
		}
		if time.Now().After(deadline) {
			t.Fatal("GET route unavailable", path, e)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type matrixHTTPRejection int

func (e matrixHTTPRejection) Error() string { return fmt.Sprintf("proxy HTTP rejection %d", int(e)) }
func matrixAssertTerminal(t *testing.T, path string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected terminal rejection", path)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || os.IsTimeout(err) {
		t.Fatal("client timeout/cancellation is not core rejection", path, err)
	}
	var status matrixHTTPRejection
	if errors.As(err, &status) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		t.Logf("terminal application refusal path=%s error=%v", path, err)
		return
	}
	t.Fatal("unclassified application refusal", path, err)
}
func matrixRejectGET(t *testing.T, trace *matrixGETTrace, entry, target, path string, originGets *atomic.Int64) {
	t.Helper()
	before := originGets.Load()
	body, e := matrixGETBound(trace, entry, target, path, 15*time.Second)
	if e == nil {
		t.Fatal("rejected route succeeded", body)
	}
	matrixAssertTerminal(t, path, e)
	if originGets.Load() != before {
		t.Fatal("failed application GET reached origin", path)
	}
	t.Logf("application GET rejected path=%s origin_GET_delta=0", path)
}
func matrixPlanReject(t *testing.T, c *Coordinator, p config.Paths, intent map[string]any, reason, field string) {
	t.Helper()
	before := coordinatorTree(t, p)
	plan, e := c.PlanSources(applyCoreContext(t), planRaw(t, intent))
	r, f, ok := SourcePlanFailureDetails(e)
	if !ok || r != reason || f != field || !reflect.DeepEqual(plan, Plan{}) {
		t.Fatal("Plan diagnostic", e, r, f)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("rejected Plan wrote")
	}
}

func TestSourceMatrixActualTwoSourcesPolicyAndDurableCatchup(t *testing.T) {
	binary := sourcePlanTestCore(t)
	probes := &matrixProbe{entered: make(chan string, 128), done: make(chan matrixHead, 1024), release: make(chan struct{})}
	probes.status.Store(204)
	defer probes.once.Do(func() { close(probes.release) })
	var gets, directGets atomic.Int64
	trace := &matrixGETTrace{expected: map[string]matrixAppExpectation{}}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop := r.Header.Get("X-Matrix-Hop")
		if r.Method == http.MethodHead {
			select {
			case probes.entered <- hop:
			case <-r.Context().Done():
				return
			}
			select {
			case <-probes.release:
			case <-r.Context().Done():
				return
			}
			status := int(probes.status.Load())
			w.WriteHeader(status)
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
			select {
			case probes.done <- matrixHead{hop, status}:
			case <-r.Context().Done():
			}
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		gets.Add(1)
		trace.observe(t, r, hop)
		if hop == "" {
			directGets.Add(1)
			hop = "DIRECT"
		}
		_, _ = io.WriteString(w, hop+":"+strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer origin.Close()
	target := strings.TrimPrefix(origin.URL, "http://")
	_, originPort, _ := net.SplitHostPort(target)
	// Serve every rule-address locally too: an unintended DIRECT fallback would
	// reach the same handler without a marker and fail loudly, rather than merely
	// encountering an unbound loopback address.
	for i := 2; i <= 8; i++ {
		listener, err := net.Listen("tcp4", net.JoinHostPort(fmt.Sprintf("127.0.0.%d", i), originPort))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(origin.Config.Handler)
		server.Listener.Close()
		server.Listener = listener
		server.Start()
		defer server.Close()
	}
	aus, ajp := matrixProxyNew(t, "A-US", target), matrixProxyNew(t, "A-JP", target)
	aus2 := matrixProxyNew(t, "A-US2", target)
	bus, bjp := matrixProxyNew(t, "B-US", target), matrixProxyNew(t, "B-JP", target)
	sources := matrixSourcesNew(t)
	rawA, rawB := matrixProvider(t, []*matrixProxy{aus, aus2, ajp}, false), matrixProvider(t, []*matrixProxy{bus, bjp}, false)
	sources.set("/a", rawA, 200)
	sources.set("/b", rawB, 200)
	defA, defB := sources.definition(t, "/a"), sources.definition(t, "/b")
	controller, entry := sourceCoreAddress(t), sourceCoreAddress(t)
	for entry == controller {
		entry = sourceCoreAddress(t)
	}
	_, ep, _ := net.SplitHostPort(entry)
	port, _ := strconv.Atoi(ep)
	c, p := coordinatorFixture(t, nil)
	settings, e := NewSourceSettings(bytes.Repeat([]byte{0x44}, 32), policy.RuntimeInput{Controller: controller, Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared", Type: "mixed", Listen: "127.0.0.1", Port: uint16(port), Users: []profile.User{{Username: strings.Repeat("u", 32), Password: strings.Repeat("p", 32)}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.InitializeSourceSettings(context.Background(), initializeRevision(t, c), settings); e != nil {
		t.Fatal(e)
	}
	_, runtimeRoot, e := c.OpenServiceRuntime()
	if e != nil {
		t.Fatal(e)
	}
	if e = runtimeRoot.Close(); e != nil {
		t.Fatal(e)
	}
	u, e := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{Transport: sources.server.Client().Transport, Timeout: 15 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(u.Close)
	clock := sourceScheduleClock(u)
	rules := []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "JP ", Region: "JP"}}
	update := func(id, source string, definition config.SubscriptionSource) sourceHistoryEntry {
		t.Helper()
		raw := updaterRequest(t, c, id, source, definition, rules)
		updaterBegin(t, u, raw)
		entry := sourceApplyWait(t, c, u, settings, raw)
		t.Logf("actual source update id=%s source=%s phase=%s reason=%s snapshot=%s asset=%s", id, source, entry.Operation.Record.Phase, entry.Receipt.Reason, entry.Receipt.SnapshotID, entry.Receipt.AssetSHA256)
		return entry
	}
	cachedA, cachedB := update("matrix-initial-a", "source-a", defA), update("matrix-initial-b", "source-b", defB)
	if cachedA.Operation.Record.Phase != Committed || cachedB.Operation.Record.Phase != Committed {
		t.Fatal("initial candidates")
	}
	key, _ := settings.SensitiveIdentityKey()
	asset, _ := c.store.ReadSourceAsset(cachedA.Receipt.AssetSHA256)
	snapA, e := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: "source-a", IdentityKey: key, RegionRules: rules})
	if e != nil {
		t.Fatal(e)
	}
	asset, _ = c.store.ReadSourceAsset(cachedB.Receipt.AssetSHA256)
	snapB, e := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: "source-b", IdentityKey: key, RegionRules: rules})
	if e != nil {
		t.Fatal(e)
	}
	pinA, pinB := config.ID(""), config.ID("")
	singleA, err := subscription.Parse(matrixProvider(t, []*matrixProxy{aus}, false), subscription.ParseOptions{SourceID: "source-a", IdentityKey: key, RegionRules: rules})
	if err != nil {
		t.Fatal(err)
	}
	pinA = singleA.Nodes[0].ID
	if len(snapA.Nodes) != 3 || snapA.Nodes[0].ID == snapA.Nodes[1].ID {
		t.Fatal("same-name source nodes collapsed")
	}
	for _, node := range snapB.Nodes {
		if node.Region.Code == "US" {
			pinB = node.ID
		}
	}
	if pinA == "" || pinB == "" || pinA == pinB {
		t.Fatal("cross-source identity isolation")
	}
	cap, e := mihomo.ApprovedSourcesCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	groups := []any{}
	for _, id := range []string{"US", "JP", "BEST"} {
		g := map[string]any{"id": id, "required": true, "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": origin.URL + "/probe", "interval_seconds": 1, "timeout_ms": 2000, "tolerance_ms": 0}}
		if id != "BEST" {
			g["region"] = id
		}
		groups = append(groups, g, map[string]any{"id": id + "-follow", "required": true, "strategy": "follow", "follows": id})
	}
	groups = append(groups, map[string]any{"id": "fixed-A", "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "source-a"}, "selected_node_id": pinA})
	ids := []string{"US", "JP", "BEST", "fixed-A", "US-follow", "JP-follow", "BEST-follow"}
	routes := []any{}
	for i, id := range ids {
		routes = append(routes, map[string]any{"id": "route-" + id, "match": map[string]any{"kind": "ip_cidr", "value": fmt.Sprintf("127.0.0.%d/32", i+2)}, "target": map[string]any{"kind": "group", "group_id": id}})
	}
	intent := map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": cap.Binary}, "subscriptions": []any{cachedA.Context.Source, cachedB.Context.Source}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": groups, "apps": []any{}, "rules": routes, "default_target": map[string]any{"kind": "reject"}}
	a, e := NewSourcesApplier(c, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	cleanupStore, e := config.OpenStore(p, false)
	if e != nil {
		t.Fatal(e)
	}
	defer cleanupStore.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer applyCoreCleanup(t, cleanupStore, binary, handles)
	var previous *platform.ProcessHandle
	var pids []int
	var stable map[config.ID]string
	apply := func(name string) {
		t.Helper()
		plan, e := c.PlanSources(applyCoreContext(t), planRaw(t, intent))
		if e != nil {
			t.Fatal("Plan", e)
		}
		op, made, e := a.Apply(applyCoreContext(t), coordinatorRequest(t, name, plan, nil))
		if e != nil || !made || op.Record.Phase != Committed {
			t.Fatal("real Apply", e)
		}
		if previous != nil {
			if e = previous.WaitExited(applyCoreContext(t)); e != nil {
				t.Fatal(e)
			}
		}
		run := applyCoreRunID(t, op)
		h, identity := sourceCoreObserve(t, c.store, run)
		handles[run] = h
		previous = h
		pids = append(pids, identity.PID)
		route, snapshot := sourceApplyGraph(t, c)
		aliases := map[config.ID]string{}
		for _, g := range route.Groups {
			aliases[g.ID] = g.Alias
		}
		if stable != nil && !reflect.DeepEqual(stable, aliases) {
			t.Fatal("business aliases changed")
		}
		stable = aliases
		activeID := config.ID(intent["selection"].(map[string]any)["active_subscription"].(string))
		expectedSnap := snapA
		if activeID == "source-b" {
			expectedSnap = snapB
		}
		expectedMembers := map[string][]string{}
		for _, node := range expectedSnap.Nodes {
			alias := "node:" + string(node.ID)
			expectedMembers["BEST"] = append(expectedMembers["BEST"], alias)
			expectedMembers[node.Region.Code] = append(expectedMembers[node.Region.Code], alias)
		}
		for id := range expectedMembers {
			sort.Strings(expectedMembers[id])
		}
		for _, g := range route.Groups {
			if g.ID == "fixed-A" {
				if g.SourceID != "source-a" {
					t.Fatal("fixed source rebound")
				}
			} else if g.SourceID != activeID {
				t.Fatal("active group wrong source")
			}
		}
		for _, g := range snapshot.Proxies {
			for _, id := range []string{"US", "JP", "BEST"} {
				if g.Name == aliases[config.ID(id+"-follow")] && (g.Type != "Selector" || len(g.Members) != 1 || g.Members[0] != aliases[config.ID(id)] || g.Selected != aliases[config.ID(id)]) {
					t.Fatal("follow lost parent")
				}
				if g.Name == aliases[config.ID(id)] && (g.Type != "URLTest" || g.Selected != "" || g.Probe == nil) {
					t.Fatal("auto persisted dynamic choice")
				}
				if g.Name == aliases[config.ID(id)] && !reflect.DeepEqual(g.Members, expectedMembers[id]) {
					t.Fatal("static source/region membership changed", id, g.Members, expectedMembers[id])
				}
			}
			if g.Name == aliases["fixed-A"] && (len(g.Members) != 1 || g.Members[0] != "node:"+string(pinA)) {
				t.Fatal("fixed A rebound")
			}
		}
		digest, err := snapshot.Digest()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("real Apply %s pid=%d generation=%s static_snapshot_sha256=%s fixedA=%s", name, identity.PID, route.Generation, digest, pinA)
	}
	routeTarget := func(index int) string { return net.JoinHostPort(fmt.Sprintf("127.0.0.%d", index+2), originPort) }
	check := func(source string) {
		t.Helper()
		for i, id := range ids {
			want := []string{source + "-US"}
			if source == "A" {
				want = append(want, "A-US2")
			}
			switch id {
			case "JP", "JP-follow":
				want = []string{source + "-JP"}
			case "BEST", "BEST-follow":
				want = []string{source + "-US", source + "-JP"}
				if source == "A" {
					want = append(want, "A-US2")
				}
			case "fixed-A":
				want = []string{"A-US"}
			}
			matrixRoute(t, trace, entry, routeTarget(i), id, want...)
		}
	}
	apply("matrix-apply-a")
	seen := map[string]bool{}
	deadline := time.After(6 * time.Second)
	for !seen["A-US"] || !seen["A-US2"] || !seen["A-JP"] {
		select {
		case hop := <-probes.entered:
			seen[hop] = true
		case <-deadline:
			t.Fatal("unprobed barrier missing")
		}
	}
	beforeGets := gets.Load()
	matrixRejectGET(t, trace, entry, routeTarget(0), "unprobed", &gets)
	if gets.Load() != beforeGets {
		t.Fatal("unprobed application reached origin")
	}
	probes.once.Do(func() { close(probes.release) })
	matrixAwaitHeads(t, probes, []string{"A-US", "A-US2", "A-JP"}, 204)
	check("A")
	// Rename only labels: connection identity and exact A pin remain unchanged.
	sources.set("/a", matrixProvider(t, []*matrixProxy{aus, aus2, ajp}, true), 200)
	renamedA := update("matrix-rename-a", "source-a", defA)
	if renamedA.Operation.Record.Phase != Committed {
		t.Fatal("rename")
	}
	asset, _ = c.store.ReadSourceAsset(renamedA.Receipt.AssetSHA256)
	renamedSnap, e := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: "source-a", IdentityKey: key, RegionRules: rules})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, node := range renamedSnap.Nodes {
		if node.ID == pinA {
			found = true
		}
	}
	if !found {
		t.Fatal("rename changed pinned connection ID")
	}
	intent["selection"] = map[string]any{"active_subscription": "source-b"}
	apply("matrix-apply-b")
	matrixAwaitHeads(t, probes, []string{"B-US", "B-JP"}, 204)
	check("B")
	active, _ := c.store.ReadActive()
	latest := sourceApplyRows(t, c)["source-b"].LatestSnapshot
	for i, item := range []struct {
		raw    []byte
		status int
		reason string
	}{{rawB, 503, "download_failed"}, {[]byte(`{"proxies":[]}`), 200, "validation_failed"}} {
		sources.set("/b", item.raw, item.status)
		failure := update(fmt.Sprintf("matrix-failure-%d", i), "source-b", defB)
		if failure.Operation.Record.Phase != RolledBack || failure.Receipt.Reason != item.reason {
			t.Fatal("failed update", failure.Receipt.Reason)
		}
		after, _ := c.store.ReadActive()
		rows := sourceApplyRows(t, c)
		if !reflect.DeepEqual(after, active) || !reflect.DeepEqual(rows["source-b"].LatestSnapshot, latest) {
			t.Fatal("failure replaced latest/runtime")
		}
		check("B")
	}
	sources.set("/b", matrixProvider(t, []*matrixProxy{bus}, false), 200)
	if update("matrix-b-no-jp", "source-b", defB).Operation.Record.Phase != Committed {
		t.Fatal("nonempty missing JP candidate")
	}
	matrixPlanReject(t, c, p, intent, "required_group_no_region_match", "groups[JP].region")
	check("B")
	sources.set("/b", rawB, 200)
	update("matrix-b-restore", "source-b", defB)
	sources.set("/a", matrixProvider(t, []*matrixProxy{aus2, ajp}, false), 200)
	if update("matrix-a-no-pin", "source-a", defA).Operation.Record.Phase != Committed {
		t.Fatal("nonempty missing pin candidate")
	}
	matrixPlanReject(t, c, p, intent, "selected_node_missing", "groups[fixed-A].selected_node_id")
	check("B")
	sources.set("/a", rawA, 200)
	update("matrix-a-restore", "source-a", defA)
	fixed := groups[len(groups)-1].(map[string]any)
	fixed["selected_node_id"] = pinB
	matrixPlanReject(t, c, p, intent, "selected_node_missing", "groups[fixed-A].selected_node_id")
	fixed["selected_node_id"] = pinA
	delete(fixed, "selected_node_id")
	beforeMalformed := coordinatorTree(t, p)
	if _, err := c.PlanSources(applyCoreContext(t), planRaw(t, intent)); err == nil {
		t.Fatal("missing manual selected node accepted")
	}
	if !reflect.DeepEqual(beforeMalformed, coordinatorTree(t, p)) {
		t.Fatal("malformed manual Plan wrote")
	}
	fixed["selected_node_id"] = pinA
	// Stop the exact fixed endpoint, retaining B and A-JP. No alternate hop may
	// satisfy its request; restarting the very same address restores the pin.
	aus.server.Close()
	matrixRejectGET(t, trace, entry, routeTarget(3), "fixed-down", &gets)
	matrixRoute(t, trace, entry, routeTarget(0), "b-still-live", "B-US")
	aus.restart(t)
	matrixRoute(t, trace, entry, routeTarget(3), "fixed-restored", "A-US")
	probes.status.Store(503)
	matrixAwaitHeads(t, probes, []string{"B-US", "B-JP"}, 503)
	// Completion barrier proves real 503 HEADs; polling only waits for the core to
	// publish those failed observations, never establishes the probe premise.
	rejectDeadline := time.Now().Add(6 * time.Second)
	for {
		beforePoll := gets.Load()
		body, pollErr := matrixGETBound(trace, entry, routeTarget(2), "failed-probe-poll", 15*time.Second, "B-US", "B-JP")
		if pollErr != nil {
			matrixAssertTerminal(t, "failed-probe-poll", pollErr)
			if gets.Load() != beforePoll {
				t.Fatal("failed probe-publication poll reached origin")
			}
			break
		}
		// Until the failed observations are published, the prior qualified B
		// selection may still succeed. Every such response must retain exact source.
		if body != "B-US:failed-probe-poll" && body != "B-JP:failed-probe-poll" {
			t.Fatal("probe-publication poll escaped source B", body)
		}
		t.Logf("application publication-poll marker=%s", body)
		if time.Now().After(rejectDeadline) {
			t.Fatal("allfailed auto not REJECT")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, i := range []int{0, 1, 2, 4, 5, 6} {
		matrixRejectGET(t, trace, entry, routeTarget(i), "allfailed", &gets)
	}
	matrixRoute(t, trace, entry, routeTarget(3), "manual-independent", "A-US")
	probes.status.Store(204)
	matrixAwaitHeads(t, probes, []string{"B-US", "B-JP"}, 204)
	check("B")
	// SC006 uses a fake wall clock, not physical sleep. Both actual committed
	// subscriptions have one-hour periods and were applied above.
	sources.set("/a", rawA, 503)
	sources.set("/b", matrixProvider(t, []*matrixProxy{bus, bjp}, true), 200)
	sources.mu.Lock()
	gate := make(chan struct{})
	sources.gate = gate
	beforeCalls := map[string]int{"/a": sources.calls["/a"], "/b": sources.calls["/b"]}
	sources.mu.Unlock()
	defer func() {
		sources.mu.Lock()
		sources.gate = nil
		sources.mu.Unlock()
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	beforeScheduledRows := sourceApplyRows(t, c)
	clock.Add(int64(4 * time.Hour))
	idsDue, e := u.RunDue(context.Background())
	if e != nil || len(idsDue) != 2 {
		t.Fatal("durable catchup", len(idsDue), e)
	}
	seenPaths := map[string]bool{}
	deadline = time.After(6 * time.Second)
	for len(seenPaths) < 2 {
		select {
		case path := <-sources.entered:
			seenPaths[path] = true
		case <-deadline:
			t.Fatal("two real scheduled downloads not concurrent")
		}
	}
	beforePulse := coordinatorTree(t, p)
	if extra, e := u.RunDue(context.Background()); e != nil || len(extra) != 0 || !reflect.DeepEqual(beforePulse, coordinatorTree(t, p)) {
		t.Fatal("occupied per-source workers duplicated")
	}
	close(gate)
	sources.mu.Lock()
	sources.gate = nil
	sources.mu.Unlock()
	for _, id := range idsDue {
		c.mu.Lock()
		op, e := c.journal.Get(id)
		c.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		entry := sourceApplyWait(t, c, u, settings, op.Record.Request)
		prior := beforeScheduledRows[entry.Context.Source.ID]
		wantDue := time.Unix(0, clock.Load()).UTC().Add(time.Hour).Format(time.RFC3339Nano)
		if entry.Context.Sequence != prior.Sequence+1 || entry.Context.NextDue == nil || *entry.Context.NextDue != wantDue {
			t.Fatal("schedule sequence/deadline repeated or drifted")
		}
		t.Logf("durable catchup source=%s op=%s phase=%s reason=%s sequence=%d next_due=%s", entry.Context.Source.ID, id, entry.Operation.Record.Phase, entry.Receipt.Reason, entry.Context.Sequence, *entry.Context.NextDue)
		if entry.Context.Source.ID == "source-a" {
			if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "download_failed" {
				t.Fatal("schedule A failure")
			}
		} else if entry.Operation.Record.Phase != Committed {
			t.Fatal("schedule B success")
		}
	}
	sources.mu.Lock()
	countsCorrect := true
	for path, n := range beforeCalls {
		if sources.calls[path] != n+1 {
			countsCorrect = false
		}
	}
	sources.mu.Unlock()
	if !countsCorrect {
		t.Fatal("catchup repeated download")
	}
	beforePulse = coordinatorTree(t, p)
	if extra, e := u.RunDue(context.Background()); e != nil || len(extra) != 0 || !reflect.DeepEqual(beforePulse, coordinatorTree(t, p)) {
		t.Fatal("catchup replayed missed periods")
	}
	rows := sourceApplyRows(t, c)
	if !reflect.DeepEqual(rows["source-a"].LatestSnapshot, beforeScheduledRows["source-a"].LatestSnapshot) || !reflect.DeepEqual(rows["source-a"].AppliedSnapshot, beforeScheduledRows["source-a"].AppliedSnapshot) {
		t.Fatal("scheduled failure changed A usable/applied")
	}
	after, _ := c.store.ReadActive()
	if !reflect.DeepEqual(after, active) || rows["source-b"].LatestSnapshot == nil || rows["source-b"].AppliedSnapshot == nil || *rows["source-b"].LatestSnapshot == *rows["source-b"].AppliedSnapshot || !rows["source-b"].Pending {
		t.Fatal("scheduled candidate implicitly applied")
	}
	check("B")
	apply("matrix-confirm-pending-b")
	check("B")
	rows = sourceApplyRows(t, c)
	if rows["source-b"].Pending || !reflect.DeepEqual(rows["source-b"].LatestSnapshot, rows["source-b"].AppliedSnapshot) {
		t.Fatal("confirmed pending not applied")
	}
	final, _ := c.store.ReadActive()
	stopped, made, e := a.Stop(applyCoreContext(t), lifecycleRequest(t, "matrix-final-stop", "runtime.stop", final.Revision))
	if e != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("public Stop", e)
	}
	if e = previous.WaitExited(applyCoreContext(t)); e != nil {
		t.Fatal(e)
	}
	for _, pid := range pids {
		if e = applyCoreAbsent(applyCoreContext(t), pid); e != nil {
			t.Fatal(e)
		}
	}
	trace.verify(t)
	t.Logf("observed direct origin GETs=%d", directGets.Load())
	if directGets.Load() != 0 {
		t.Fatal("silent direct fallback observed", directGets.Load())
	}
	t.Logf("approved static core=c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6; actual GET marker observations=%d; 5 independent authenticated CONNECT leaves (same-source US duplicate and cross-source same names); A/B US JP BEST + parent follows + fixed A; F08/F09/F32 and unprobed/all503 reject; 4h fake-wall catchup two HTTPS gates one each; pending explicit Apply; public replacements/Stop pidfds exited and proc absent, no cleanup signal needed", gets.Load())
}
