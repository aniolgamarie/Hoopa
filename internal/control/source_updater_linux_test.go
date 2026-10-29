//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

const updaterProvider = `{"proxies":[{"name":"US fixture","type":"http","server":"proxy.example","port":443}]}`

func updaterRequest(t *testing.T, c *Coordinator, id, source string, definition config.SubscriptionSource, rules any) []byte {
	t.Helper()
	intent := updateRequestIntent(t)
	intent["subscriptions"] = []any{map[string]any{"id": source, "source": definition, "update_hours": 1}}
	intent["selection"] = map[string]any{"active_subscription": source}
	p := map[string]any{"expected_revision": initializeRevision(t, c), "subscription_id": source, "intent": intent}
	if rules != nil {
		p["region_rules"] = rules
	}
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": id, "operation": "subscription.update", "params": p})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func updaterFile(t *testing.T, raw string) config.SubscriptionSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return config.SubscriptionSource{Kind: "file", Path: path}
}
func updaterFixture(t *testing.T, options subscription.FetchOptions) (*Coordinator, config.Paths, *SourceUpdater) {
	t.Helper()
	c, p := coordinatorFixture(t, nil)
	initializePut(t, c, initializeSettings(t))
	u, err := NewSourceUpdater(c, context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	return c, p, u
}
func updaterWait(t *testing.T, u *SourceUpdater, raw []byte) sourceHistoryEntry {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		u.c.mu.Lock()
		r, err := ParseRequest(raw)
		if err != nil {
			u.c.mu.Unlock()
			t.Fatal(err)
		}
		op, err := u.c.journal.Lookup(r)
		alive := false
		_, alive = u.active[r.OperationID()]
		var entry sourceHistoryEntry
		if err == nil && !alive {
			entry, err = u.c.sourceEntryLocked(op, initializeSettings(t))
		}
		u.c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			return entry
		}
		select {
		case <-deadline:
			t.Fatal("source worker did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}
func updaterBegin(t *testing.T, u *SourceUpdater, raw []byte) StoredOperation {
	t.Helper()
	op, made, err := u.Update(context.Background(), raw)
	if err != nil || !made || op.Record.Phase != Prepared {
		t.Fatal("expected asynchronous durable reservation", made, err)
	}
	return op
}

func TestSourceUpdaterFileCommitReplayAndReopen(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	base, _ := c.store.ReadActive()
	source := updaterFile(t, updaterProvider)
	raw := updaterRequest(t, c, "file-success", "source-a", source, []subscription.RegionRule{{Prefix: "US ", Region: "US"}})
	admitted := updaterBegin(t, u, raw)
	entry := updaterWait(t, u, raw)
	if entry.Operation.Record.Phase != Committed || !entry.Receipt.Downloaded || !entry.Receipt.Validated || entry.Context.Sequence != 1 || entry.Context.NextDue == nil {
		t.Fatal("incomplete committed result")
	}
	asset, err := c.store.ReadSourceAsset(entry.Receipt.AssetSHA256)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := initializeSettings(t).SensitiveIdentityKey()
	snapshot, err := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: "source-a", IdentityKey: key, RegionRules: entry.Context.RegionRules})
	if err != nil || string(snapshot.ID) != entry.Receipt.SnapshotID || snapshot.Nodes[0].Region.Code != "US" {
		t.Fatal("real provider/key restoration", err)
	}
	after, _ := c.store.ReadActive()
	if !reflect.DeepEqual(base, after) {
		t.Fatal("source update published runtime")
	}
	if err := os.Remove(source.Path); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	for _, ctx := range []context.Context{nil, context.Background()} {
		op, made, err := u.Update(ctx, raw)
		if err != nil || made || op.SHA256 != entry.Operation.SHA256 {
			t.Fatal("replay consulted current source or started work", err)
		}
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("replay wrote")
	}
	changed := append([]byte{}, raw...)
	var object map[string]any
	_ = json.Unmarshal(changed, &object)
	object["params"].(map[string]any)["subscription_id"] = "source-b"
	changed, _ = json.Marshal(object)
	if _, made, err := u.Update(context.Background(), changed); made || !errors.Is(err, ErrRequestIDReused) {
		t.Fatal("identity reuse", err)
	}
	if admitted.Record.Identity.OperationID != entry.Context.OperationID {
		t.Fatal("reservation/context binding")
	}
	c.mu.Lock()
	compact, err := c.journal.Compact(entry.Context.OperationID, entry.Operation.SHA256)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	u.Close()
	c = initializeReopen(t, c, p)
	u2, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u2.Close)
	op, made, err := u2.Update(nil, raw)
	if err != nil || made || op.SHA256 != compact.SHA256 {
		t.Fatal("compacted restart replay", err)
	}
	c.mu.Lock()
	history, err := c.sourceHistoryLocked(initializeSettings(t))
	c.mu.Unlock()
	if err != nil || len(history.Entries) != 1 || !history.Entries[history.Latest["source-a"]].Operation.Record.Compacted {
		t.Fatal("compacted latest", err)
	}
}

func TestSourceUpdaterFailureKeepsLatestAndDeclaredRules(t *testing.T) {
	c, _, u := updaterFixture(t, subscription.FetchOptions{})
	source := updaterFile(t, updaterProvider)
	first := updaterRequest(t, c, "good", "source-a", source, []subscription.RegionRule{{Prefix: "US ", Region: "US"}})
	updaterBegin(t, u, first)
	good := updaterWait(t, u, first)
	for i, body := range []string{`{"invalid":"private-error"}`, updaterProvider} {
		if err := os.WriteFile(source.Path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		raw := updaterRequest(t, c, fmt.Sprintf("attempt-%d", i), "source-a", source, nil)
		updaterBegin(t, u, raw)
		entry := updaterWait(t, u, raw)
		if entry.Context.Sequence != uint64(i+2) || !reflect.DeepEqual(entry.Context.RegionRules, good.Context.RegionRules) {
			t.Fatal("durable sequence/policy lost")
		}
		c.mu.Lock()
		history, err := c.sourceHistoryLocked(initializeSettings(t))
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if entry.Operation.Record.Phase != RolledBack || !entry.Receipt.Downloaded || entry.Receipt.Validated || entry.Receipt.Reason != "validation_failed" || history.Entries[history.Latest["source-a"]].Context.Sequence != 1 {
				t.Fatal("failed validation cleared or replaced usable snapshot")
			}
		} else if entry.Operation.Record.Phase != Committed || history.Entries[history.Latest["source-a"]].Context.Sequence != 3 {
			t.Fatal("new valid attempt not latest")
		}
	}
	if err := os.Remove(source.Path); err != nil {
		t.Fatal(err)
	}
	raw := updaterRequest(t, c, "missing-file", "source-a", source, nil)
	updaterBegin(t, u, raw)
	entry := updaterWait(t, u, raw)
	if entry.Receipt.Reason != "download_failed" || entry.Receipt.Downloaded || entry.Operation.Record.Phase != RolledBack {
		t.Fatal("download failure misreported")
	}
}

func TestSourceUpdaterPreAdmissionFailuresWriteNothing(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	good := updaterRequest(t, c, "new", "source-a", updaterFile(t, updaterProvider), nil)
	before := coordinatorTree(t, p)
	bad := []byte(`{"api_version":"v1","request_id":"bad","operation":"subscription.update","params":{}}`)
	for _, raw := range [][]byte{bad, []byte(`null`)} {
		if _, made, err := u.Update(context.Background(), raw); made || err == nil {
			t.Fatal("invalid admitted")
		}
	}
	if _, made, err := u.Update(nil, good); made || err != ErrRequest {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, made, err := u.Update(ctx, good); made || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled new admission")
	}
	if _, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{}); err != ErrConflict {
		t.Fatal("second writer registered")
	}
	u.now = func() time.Time { return time.Time{} }
	if _, made, err := u.Update(context.Background(), good); made || err != ErrRequest {
		t.Fatal("invalid reservation time", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("rejected admission changed files")
	}
}

type updaterGate struct {
	server  *httptest.Server
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	once    sync.Once
}

func updaterHTTPS(t *testing.T) (*updaterGate, config.SubscriptionSource) {
	t.Helper()
	g := &updaterGate{entered: make(chan struct{}, 8), release: make(chan struct{})}
	g.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.calls.Add(1)
		g.entered <- struct{}{}
		select {
		case <-g.release:
			_, _ = w.Write([]byte(updaterProvider))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { g.once.Do(func() { close(g.release) }); g.server.Close() })
	secret := filepath.Join(t.TempDir(), "url")
	if err := os.WriteFile(secret, []byte(g.server.URL+"/private-path"), 0600); err != nil {
		t.Fatal(err)
	}
	return g, config.SubscriptionSource{Kind: "https", URLSecretRef: secret}
}
func updaterEntered(t *testing.T, g *updaterGate) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual HTTPS request did not start")
	}
}
func TestSourceUpdaterConcurrentBoundAndReadOnlyStatus(t *testing.T) {
	g, source := updaterHTTPS(t)
	c, p, u := updaterFixture(t, subscription.FetchOptions{Transport: g.server.Client().Transport})
	first := updaterRequest(t, c, "a", "source-a", source, nil)
	second := updaterRequest(t, c, "b", "source-b", source, nil)
	updaterBegin(t, u, first)
	updaterEntered(t, g)
	// Callback is held in real TLS IO; status/replay must still complete.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := c.Status(ctx)
	if err != nil || status.PendingTotal != 1 || status.Core.State != "verified-stopped" {
		t.Fatal("fetch held writer lock or changed core", err)
	}
	if _, made, err := u.Update(ctx, first); made || err != nil {
		t.Fatal("pending replay", err)
	}
	before := coordinatorTree(t, p)
	if _, made, err := u.Update(ctx, updaterRequest(t, c, "duplicate-source", "source-a", source, nil)); made || err != ErrConflict {
		t.Fatal("same-source concurrency admitted", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("same-source rejection wrote")
	}
	updaterBegin(t, u, second)
	updaterEntered(t, g)
	before = coordinatorTree(t, p)
	if _, made, err := u.Update(ctx, updaterRequest(t, c, "third", "source-c", source, nil)); made || err != ErrConflict {
		t.Fatal("third worker admitted", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("capacity rejection wrote")
	}
	g.once.Do(func() { close(g.release) })
	for _, raw := range [][]byte{first, second} {
		if entry := updaterWait(t, u, raw); entry.Operation.Record.Phase != Committed {
			t.Fatal("concurrent source failed")
		}
	}
	if g.calls.Load() != 2 {
		t.Fatal("replay caused extra network request")
	}
}

func TestSourceUpdaterSecretChangeRejectsCommit(t *testing.T) {
	g, source := updaterHTTPS(t)
	c, _, u := updaterFixture(t, subscription.FetchOptions{Transport: g.server.Client().Transport})
	raw := updaterRequest(t, c, "changed-secret", "source-a", source, nil)
	updaterBegin(t, u, raw)
	updaterEntered(t, g)
	if err := os.WriteFile(source.URLSecretRef, []byte(g.server.URL+"/changed-private-path"), 0600); err != nil {
		t.Fatal(err)
	}
	g.once.Do(func() { close(g.release) })
	entry := updaterWait(t, u, raw)
	if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "input_changed" || !entry.Receipt.Downloaded || entry.Receipt.Validated || g.calls.Load() != 1 {
		t.Fatal("stale secret committed or refetched")
	}
}

func TestSourceUpdaterCoordinatorCloseCancelsAndJoins(t *testing.T) {
	g, source := updaterHTTPS(t)
	c, p, u := updaterFixture(t, subscription.FetchOptions{Transport: g.server.Client().Transport})
	raw := updaterRequest(t, c, "cancel-on-close", "source-a", source, nil)
	updaterBegin(t, u, raw)
	updaterEntered(t, g)
	errs := make(chan error, 2)
	go func() { errs <- c.Close() }()
	go func() { errs <- c.Close() }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Close deadlocked with source finalization")
		}
	}
	if len(u.active) != 0 {
		t.Fatal("Store closed before worker joined")
	}
	next, err := OpenCoordinator(p, coordinatorValidator(t), func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
	if err != nil {
		t.Fatal("writer was not released", err)
	}
	t.Cleanup(func() { next.Close() })
	r, _ := ParseRequest(raw)
	next.mu.Lock()
	op, err := next.journal.Lookup(r)
	var entry sourceHistoryEntry
	if err == nil {
		entry, err = next.sourceEntryLocked(op, initializeSettings(t))
	}
	next.mu.Unlock()
	if err != nil || entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "cancelled" || entry.Receipt.Downloaded {
		t.Fatal("cancellation not durable before writer release", err)
	}
	if g.calls.Load() != 1 {
		t.Fatal("duplicate request")
	}
}

type sourcePublicationCancelled struct {
	context.Context
	directory string
}

func (c sourcePublicationCancelled) Err() error {
	entries, err := os.ReadDir(c.directory)
	if err == nil && len(entries) > 0 {
		return context.Canceled
	}
	return nil
}
func TestSourceUpdaterCancellationAfterContextPublicationDoesNotAdmit(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	source := config.SubscriptionSource{Kind: "file", Path: filepath.Join(t.TempDir(), "never-read.json")}
	raw := updaterRequest(t, c, "cancel-before-admission", "source-a", source, nil)
	ctx := sourcePublicationCancelled{context.Background(), filepath.Join(p.State, "source-assets")}
	if _, made, err := u.Update(ctx, raw); made || err != context.Canceled {
		t.Fatal("cancelled post-publication request admitted", made, err)
	}
	ids, err := c.journal.IDs()
	if err != nil || len(ids) != 0 {
		t.Fatal("orphan reserved an attempt", err)
	}
	u.Close()
	if len(u.active) != 0 {
		t.Fatal("cancelled admission started a worker")
	}
	assets, err := os.ReadDir(ctx.directory)
	if err != nil || len(assets) != 1 {
		t.Fatal("expected only unreferenced context artifact", err)
	}
	page, err := c.Sources(context.Background(), "")
	if err != nil || page.Total != 0 {
		t.Fatal("orphan context became a source attempt", err)
	}
}
