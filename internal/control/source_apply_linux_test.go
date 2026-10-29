//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
	wire "hoopa/protocol/sourcecontrol"
	profile "hoopa/protocol/sourceprofile"
)

func sourceApplyWait(t *testing.T, c *Coordinator, u *SourceUpdater, settings SourceSettings, raw []byte) sourceHistoryEntry {
	t.Helper()
	request, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(6 * time.Second)
	for {
		c.mu.Lock()
		op, err := c.journal.Lookup(request)
		_, alive := u.active[request.OperationID()]
		var entry sourceHistoryEntry
		if err == nil && !alive {
			entry, err = c.sourceEntryLocked(op, settings)
		}
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			return entry
		}
		select {
		case <-deadline:
			t.Fatal("actual source update did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}
func sourceApplyUpdate(t *testing.T, c *Coordinator, u *SourceUpdater, settings SourceSettings, id, sourceID string, definition config.SubscriptionSource) sourceHistoryEntry {
	t.Helper()
	raw := updaterRequest(t, c, id, sourceID, definition, []subscription.RegionRule{{Prefix: "US ", Region: "US"}})
	updaterBegin(t, u, raw)
	entry := sourceApplyWait(t, c, u, settings, raw)
	if entry.Operation.Record.Phase != Committed {
		t.Fatal("actual provider cache update failed", entry.Receipt.Reason)
	}
	return entry
}
func sourceApplyProvider(t *testing.T, address, label string) []byte {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return planRaw(t, map[string]any{"proxies": []any{map[string]any{"name": "US " + label, "type": "http", "server": host, "port": port}}})
}
func sourceApplyReject(t *testing.T, c *Coordinator, p config.Paths, a *BasicApplier, raw []byte, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	op, made, err := a.Apply(applyCoreContext(t), raw)
	if made || !errors.Is(err, want) || !reflect.DeepEqual(op, StoredOperation{}) {
		t.Fatalf("Apply admission expected %v got made=%v err=%v", want, made, err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("pre-admission rejection created workdir/asset/operation or changed runtime")
	}
}
func sourceApplyRows(t *testing.T, c *Coordinator) map[config.ID]SourceState {
	t.Helper()
	page, err := c.Sources(applyCoreContext(t), "")
	if err != nil || page.Total != 2 || len(page.Sources) != 2 {
		t.Fatal("actual sources page", err)
	}
	rows := map[config.ID]SourceState{}
	for _, row := range page.Sources {
		rows[row.SourceID] = row
	}
	return rows
}
func sourceApplyGraph(t *testing.T, c *Coordinator) (policy.RouteMap, wire.Snapshot) {
	t.Helper()
	active, err := c.store.ReadActive()
	if err != nil || active.Generation == nil {
		t.Fatal("active source generation missing", err)
	}
	bundle, err := c.store.ReadBundle(*active.Generation)
	if err != nil {
		t.Fatal(err)
	}
	var route policy.RouteMap
	if json.Unmarshal(bundle.Files["route-map.json"], &route) != nil {
		t.Fatal("route map absent")
	}
	snapshot, err := wire.DecodeSnapshot(bundle.Files[mihomo.SnapshotFile])
	if err != nil {
		t.Fatal(err)
	}
	return route, snapshot
}
func sourceApplyFollow(t *testing.T, route policy.RouteMap, snapshot wire.Snapshot, sourceID config.ID) (string, string) {
	t.Helper()
	aliases := map[config.ID]string{}
	for _, group := range route.Groups {
		aliases[group.ID] = group.Alias
		if group.SourceID != sourceID {
			t.Fatal("group rebound to wrong exact source")
		}
	}
	us, best := aliases["US"], aliases["BEST"]
	if us == "" || best == "" {
		t.Fatal("stable business aliases missing")
	}
	foundUS, foundBEST := false, false
	for _, proxy := range snapshot.Proxies {
		switch proxy.Name {
		case us:
			foundUS = true
			if proxy.Type != "URLTest" || proxy.Selected != "" || proxy.Probe == nil || len(proxy.Members) != 1 {
				t.Fatal("auto group stored a dynamic choice or lost fixed probe")
			}
		case best:
			foundBEST = true
			if proxy.Type != "Selector" || proxy.Selected != us || !reflect.DeepEqual(proxy.Members, []string{us}) || proxy.Probe != nil {
				t.Fatal("follow stored a dynamic leaf instead of parent group")
			}
		}
	}
	if !foundUS || !foundBEST {
		t.Fatal("static group projections absent")
	}
	return us, best
}

func TestSourceApplyActualAdmissionForwardSwitchAndPublicStop(t *testing.T) {
	binary := sourcePlanTestCore(t)
	var echoes atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(204)
			return
		}
		echoes.Add(1)
		io.WriteString(w, "source-apply:"+r.URL.Path)
	}))
	defer origin.Close()
	target := strings.TrimPrefix(origin.URL, "http://")
	proxyA, countA := sourceCoreProxy(t, target)
	proxyB, countB := sourceCoreProxy(t, target)
	controller, entry := sourceCoreAddress(t), sourceCoreAddress(t)
	for entry == controller {
		entry = sourceCoreAddress(t)
	}
	_, portText, _ := net.SplitHostPort(entry)
	entryPort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	c, p := coordinatorFixture(t, nil)
	settings, err := NewSourceSettings(bytes.Repeat([]byte{0x32}, 32), policy.RuntimeInput{Controller: controller, Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared", Type: "mixed", Listen: "127.0.0.1", Port: uint16(entryPort), Users: []profile.User{{Username: strings.Repeat("u", 32), Password: strings.Repeat("p", 32)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.InitializeSourceSettings(context.Background(), initializeRevision(t, c), settings); err != nil {
		t.Fatal(err)
	}
	_, runtimeRoot, err := c.OpenServiceRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err = runtimeRoot.Close(); err != nil {
		t.Fatal(err)
	}
	gate, refreshDefinition := updaterHTTPS(t)
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{Transport: gate.server.Client().Transport, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	defA := updaterFile(t, string(sourceApplyProvider(t, proxyA, "supplier-A")))
	defB := updaterFile(t, string(sourceApplyProvider(t, proxyB, "supplier-B")))
	cachedA := sourceApplyUpdate(t, c, u, settings, "source-a-initial", "source-a", defA)
	cachedB := sourceApplyUpdate(t, c, u, settings, "source-b-initial", "source-b", defB)
	capability, err := mihomo.ApprovedSourcesCapability(binary)
	if err != nil {
		t.Fatal(err)
	}
	intent := map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": capability.Binary}, "subscriptions": []any{cachedA.Context.Source, cachedB.Context.Source}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": []any{map[string]any{"id": "US", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": origin.URL + "/probe", "interval_seconds": 1, "timeout_ms": 500, "tolerance_ms": 0}}, map[string]any{"id": "BEST", "required": true, "strategy": "follow", "follows": "US"}}, "apps": []any{}, "rules": []any{map[string]any{"id": "local-origin", "match": map[string]any{"kind": "ip_cidr", "value": "127.0.0.1/8"}, "target": map[string]any{"kind": "group", "group_id": "BEST"}}}, "default_target": map[string]any{"kind": "reject"}}
	beforeConstructor := coordinatorTree(t, p)
	a, err := NewSourcesApplier(c, applyCoreEntryArgs())
	if err != nil || !reflect.DeepEqual(beforeConstructor, coordinatorTree(t, p)) {
		t.Fatal("applier construction has effects", err)
	}
	cleanupStore, err := config.OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupStore.Close()
	handles := map[config.RunID]*platform.ProcessHandle{}
	defer applyCoreCleanup(t, cleanupStore, binary, handles)
	stalePlan, err := c.PlanSources(applyCoreContext(t), planRaw(t, intent))
	if err != nil {
		t.Fatal(err)
	}
	// Real source refresh changes exact provider input but retains its same route.
	if err = os.WriteFile(defA.Path, sourceApplyProvider(t, proxyA, "supplier-A-refreshed"), 0600); err != nil {
		t.Fatal(err)
	}
	cachedA = sourceApplyUpdate(t, c, u, settings, "source-a-refreshed", "source-a", defA)
	sourceApplyReject(t, c, p, a, coordinatorRequest(t, "stale-source-apply", stalePlan, nil), ErrConflict)
	planA, err := c.PlanSources(applyCoreContext(t), planRaw(t, intent))
	if err != nil {
		t.Fatal(err)
	}
	sourceApplyReject(t, c, p, a, coordinatorRequest(t, "unconfirmed-source-apply", planA, func(params map[string]any) { params["accept_disruption"] = false }), ErrConfirmation)
	// Real overlap: another attempt for A is durably Applying with a held HTTPS
	// request while its old committed candidate remains the one being applied.
	refreshRaw := updaterRequest(t, c, "source-a-held-refresh", "source-a", refreshDefinition, nil)
	updaterBegin(t, u, refreshRaw)
	updaterEntered(t, gate)
	rows := sourceApplyRows(t, c)
	if rows["source-a"].State != "updating" || rows["source-a"].LatestSnapshot == nil || *rows["source-a"].LatestSnapshot != cachedA.Receipt.SnapshotID || rows["source-a"].AppliedSnapshot != nil || !rows["source-a"].Pending {
		t.Fatal("pending download conflated latest/applied")
	}
	applyARaw := coordinatorRequest(t, "actual-source-apply-a", planA, nil)
	appliedA, made, err := a.Apply(applyCoreContext(t), applyARaw)
	if err != nil || !made || appliedA.Record.Phase != Committed {
		t.Fatal("first actual sources Apply with live download", made, err)
	}
	runA := applyCoreRunID(t, appliedA)
	handleA, identityA := sourceCoreObserve(t, c.store, runA)
	handles[runA] = handleA
	routeA, snapshotA := sourceApplyGraph(t, c)
	usAlias, bestAlias := sourceApplyFollow(t, routeA, snapshotA, "source-a")
	proxyURL := &url.URL{Scheme: "http", Host: entry, User: url.UserPassword(strings.Repeat("u", 32), strings.Repeat("p", 32))}
	echo := func(path string) {
		t.Helper()
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			response, err := client.Get(origin.URL + "/" + path)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 128))
				response.Body.Close()
				if readErr == nil && response.StatusCode == 200 && string(body) == "source-apply:/"+path {
					return
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("actual BEST->US forwarding unavailable", path)
	}
	echo("through-a")
	if countA.Load() == 0 || countB.Load() != 0 {
		t.Fatal("first Apply did not use exact supplier A")
	}
	rows = sourceApplyRows(t, c)
	if rows["source-a"].AppliedSnapshot == nil || *rows["source-a"].AppliedSnapshot != cachedA.Receipt.SnapshotID || rows["source-a"].Pending || rows["source-b"].AppliedSnapshot != nil || !rows["source-b"].Pending {
		t.Fatal("first applied/pending projection incorrect")
	}
	gate.once.Do(func() { close(gate.release) })
	interrupted := sourceApplyWait(t, c, u, settings, refreshRaw)
	if interrupted.Operation.Record.Phase != RolledBack || interrupted.Receipt.Reason != "input_changed" || !interrupted.Receipt.Downloaded || interrupted.Receipt.Validated {
		t.Fatal("real Apply did not invalidate old download context")
	}
	rows = sourceApplyRows(t, c)
	if rows["source-a"].State != "failed" || rows["source-a"].LatestSnapshot == nil || *rows["source-a"].LatestSnapshot != cachedA.Receipt.SnapshotID || rows["source-a"].Pending {
		t.Fatal("stale download replaced old latest or applied snapshot")
	}
	// Replay must precede even current settings dependency checks. The missing
	// file is a deliberate fixture mutation, restored before any new operation.
	settingsPath := filepath.Join(p.State, "credentials", "source-settings-v1.json")
	backup := settingsPath + ".test-backup"
	if err = os.Rename(settingsPath, backup); err != nil {
		t.Fatal(err)
	}
	beforeReplay := coordinatorTree(t, p)
	replayed, remade, replayErr := a.Apply(nil, applyARaw)
	afterReplay := coordinatorTree(t, p)
	restoreErr := os.Rename(backup, settingsPath)
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if replayErr != nil || remade || replayed.SHA256 != appliedA.SHA256 || !reflect.DeepEqual(beforeReplay, afterReplay) {
		t.Fatal("exact Apply replay read current resources or wrote", replayErr)
	}
	echo("a-after-replay")
	intent["selection"] = map[string]any{"active_subscription": "source-b"}
	planB, err := c.PlanSources(applyCoreContext(t), planRaw(t, intent))
	if err != nil {
		t.Fatal(err)
	}
	if planB.Document().Impact.RuntimeAction != "replace" || planB.Document().Impact.PreviousGeneration == nil {
		t.Fatal("switch did not use normal replacement Plan")
	}
	appliedB, made, err := a.Apply(applyCoreContext(t), coordinatorRequest(t, "actual-source-apply-b", planB, nil))
	if err != nil || !made || appliedB.Record.Phase != Committed {
		t.Fatal("actual supplier B replacement", made, err)
	}
	if err = handleA.WaitExited(applyCoreContext(t)); err != nil {
		t.Fatal("replaced original A pidfd did not exit", err)
	}
	runB := applyCoreRunID(t, appliedB)
	handleB, identityB := sourceCoreObserve(t, c.store, runB)
	handles[runB] = handleB
	if runA == runB || identityA == identityB {
		t.Fatal("replacement reused run/process identity")
	}
	routeB, snapshotB := sourceApplyGraph(t, c)
	usB, bestB := sourceApplyFollow(t, routeB, snapshotB, "source-b")
	if usAlias != usB || bestAlias != bestB {
		t.Fatal("supplier change renamed stable business group aliases")
	}
	aBefore, bBefore := countA.Load(), countB.Load()
	echo("through-b")
	if countA.Load() != aBefore || countB.Load() <= bBefore {
		t.Fatal("new actual traffic did not switch exclusively to B")
	}
	rows = sourceApplyRows(t, c)
	if rows["source-b"].LatestSnapshot == nil || *rows["source-b"].LatestSnapshot != cachedB.Receipt.SnapshotID || rows["source-b"].AppliedSnapshot == nil || *rows["source-b"].AppliedSnapshot != cachedB.Receipt.SnapshotID || rows["source-b"].Pending || rows["source-a"].AppliedSnapshot != nil || !rows["source-a"].Pending {
		t.Fatal("supplier switch conflated latest/applied/pending")
	}
	active, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	stopped, made, err := a.Stop(applyCoreContext(t), lifecycleRequest(t, "actual-sources-final-stop", "runtime.stop", active.Revision))
	if err != nil || !made || stopped.Record.Phase != Committed {
		t.Fatal("public source Stop failed", made, err)
	}
	if err = handleB.WaitExited(applyCoreContext(t)); err != nil {
		t.Fatal("public Stop did not exit original B pidfd", err)
	}
	for _, pid := range []int{identityA.PID, identityB.PID} {
		if err = applyCoreAbsent(applyCoreContext(t), pid); err != nil {
			t.Fatal("old source process remains", err)
		}
	}
	if gate.calls.Load() != 1 || echoes.Load() < 3 {
		t.Fatal("unexpected actual held-download/forwarding observations")
	}
	t.Logf("actual NewSourcesApplier: source-a PID %d -> source-b PID %d; stable US=%s BEST=%s parent references; stale Plan Conflict and unconfirmed rejection zero-write; live HTTPS overlap exactly1 request rolled back input_changed; actual native forwards=%d; latest/applied/pending independently observed; public replacement/Stop original pidfds exited and proc absent; no cleanup signal needed", identityA.PID, identityB.PID, usAlias, bestAlias, echoes.Load())
}
