//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"hoopa/internal/config"
	profile "hoopa/protocol/sourceprofile"
)

func initializeApp(t *testing.T, s SourceSettings, id config.ID, port uint16, password string) AppEntryBinding {
	t.Helper()
	b, err := NewAppEntryBinding(id, profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: port, Users: []profile.User{{Username: "user-" + string(id), Password: strings.Repeat(password, 32)}}}, s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func appInitializeReject(t *testing.T, c *Coordinator, p config.Paths, ctx context.Context, rev config.Revision, b AppEntryBinding, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	sha, err := c.InitializeAppEntry(ctx, rev, b)
	if sha != "" || !errors.Is(err, want) {
		t.Fatalf("binding rejection %s %v want%v", sha, err, want)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("rejection wrote")
	}
}
func appInitializePut(t *testing.T, c *Coordinator, b AppEntryBinding) {
	t.Helper()
	sha, err := c.InitializeAppEntry(context.Background(), initializeRevision(t, c), b)
	if err != nil || sha != b.Digest() {
		t.Fatal(sha, err)
	}
}
func appInitializeHistory(t *testing.T, c *Coordinator, b AppEntryBinding) {
	t.Helper()
	raw, _ := b.SensitiveBytes()
	name, _ := config.AppBindingFilename(b.AppID())
	m, files, err := config.AssembleBundle("app-history-fixture", "fixture-v1", strings.Repeat("a", 64), []config.BinaryReference{{Path: "/fixture/core", Version: "fixture", SHA256: strings.Repeat("b", 64)}}, []config.BundleAsset{{Kind: "app_binding", Name: name, Data: raw}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.store.PublishBundle(m, files); err != nil {
		t.Fatal(err)
	}
}

func TestAppEntryInitializeConstructorParentAndSharedCollisions(t *testing.T) {
	s := initializeSettings(t)
	r, _ := s.Runtime()
	entry := profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: 27001, Users: []profile.User{{Username: "app-user", Password: strings.Repeat("q", 32)}}}
	if _, err := NewAppEntryBinding("app-a", entry, SourceSettings{}); !errors.Is(err, ErrSourceSettings) {
		t.Fatal(err)
	}
	for _, mutation := range []func(*profile.Entry){func(e *profile.Entry) { e.Port = r.Entry.Port }, func(e *profile.Entry) {
		_, p, _ := net.SplitHostPort(r.Controller)
		n, _ := strconv.Atoi(p)
		e.Port = uint16(n)
	}, func(e *profile.Entry) { e.Users = append([]profile.User{}, r.Entry.Users...) }, func(e *profile.Entry) { e.Users[0].Password = r.Secret }} {
		e := entry
		e.Users = append([]profile.User{}, entry.Users...)
		mutation(&e)
		if b, err := NewAppEntryBinding("app-a", e, s); b.Digest() != "" || !errors.Is(err, config.ErrAppBinding) {
			t.Fatal("shared collision accepted", err)
		}
	}
}

func TestAppEntryInitializePrivatePreparationReplayAndReopen(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	active, _ := c.store.ReadActive()
	// Holding the requested port makes startup/reservation impossible. Explicit
	// input preparation must nevertheless succeed without touching this listener.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	b := initializeApp(t, s, "app-a", uint16(ln.Addr().(*net.TCPAddr).Port), "q")
	before := coordinatorTree(t, p)
	appInitializePut(t, c, b)
	name, _ := config.AppBindingFilename(b.AppID())
	after := coordinatorTree(t, p)
	delete(after, filepath.Join(p.State, "credentials", name))
	delete(after, filepath.Join(p.State, "credentials"))
	delete(before, filepath.Join(p.State, "credentials"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("prepare touched runtime/authority")
	}
	if got, err := c.store.ReadActive(); err != nil || !reflect.DeepEqual(active, got) {
		t.Fatal("moved active", err)
	}
	conn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal("closed foreign listener", err)
	}
	conn.Close()
	before = coordinatorTree(t, p)
	appInitializePut(t, c, b)
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("retry changed bytes")
	}
	c = initializeReopen(t, c, p)
	appInitializePut(t, c, b)
	other := initializeApp(t, s, "app-a", appEntryPort(t, b), "z")
	appInitializeReject(t, c, p, context.Background(), active.Revision, other, config.ErrAppBindingConflict)
}

func appEntryPort(t *testing.T, b AppEntryBinding) uint16 {
	t.Helper()
	e, err := b.SensitiveEntry()
	if err != nil {
		t.Fatal(err)
	}
	return e.Port
}

func TestAppEntryInitializeUnusedCollisionAndSharedLock(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	a := initializeApp(t, s, "app-a", 27001, "q")
	appInitializePut(t, c, a)
	b := initializeApp(t, s, "app-b", 27001, "z")
	appInitializeReject(t, c, p, context.Background(), initializeRevision(t, c), b, config.ErrAppBindingConflict)
	e, _ := a.SensitiveEntry()
	e.Name = ""
	e.Port = 27002
	b, err := NewAppEntryBinding("app-b", e, s)
	if err != nil {
		t.Fatal(err)
	}
	appInitializeReject(t, c, p, context.Background(), initializeRevision(t, c), b, config.ErrAppBindingConflict)
	// Two preparations on copied handles with the same new endpoint must serialize.
	copyHandle := *c
	one := initializeApp(t, s, "app-one", 27003, "m")
	two := initializeApp(t, s, "app-two", 27003, "n")
	rev := initializeRevision(t, c)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, b := range []AppEntryBinding{one, two} {
		wg.Add(1)
		go func(i int, b AppEntryBinding) {
			defer wg.Done()
			target := c
			if i == 1 {
				target = &copyHandle
			}
			_, err := target.InitializeAppEntry(context.Background(), rev, b)
			results <- err
		}(i, b)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, config.ErrAppBindingConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}

func TestAppEntryInitializeHistoryLossAndRekeyRefusal(t *testing.T) {
	for _, loss := range []string{"live-binding", "settings"} {
		t.Run(loss, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			s := initializeSettings(t)
			initializePut(t, c, s)
			b := initializeApp(t, s, "app-a", 27001, "q")
			appInitializePut(t, c, b)
			if loss == "live-binding" {
				appInitializeHistory(t, c, b)
				name, _ := config.AppBindingFilename(b.AppID())
				if err := os.Remove(filepath.Join(p.State, "credentials", name)); err != nil {
					t.Fatal(err)
				}
				appInitializeReject(t, c, p, context.Background(), initializeRevision(t, c), b, ErrRecoveryRequired)
			} else {
				if err := os.Remove(filepath.Join(p.State, "credentials", "source-settings-v1.json")); err != nil {
					t.Fatal(err)
				}
				initializeReject(t, c, p, context.Background(), initializeRevision(t, c), s, ErrRecoveryRequired)
				appInitializeReject(t, c, p, context.Background(), initializeRevision(t, c), b, config.ErrReadUnavailable)
			}
		})
	}
}

func TestAppEntryInitializeShapeParentCancellationAndStale(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	rev := initializeRevision(t, c)
	b := initializeApp(t, s, "app-a", 27001, "q")
	appInitializeReject(t, c, p, nil, rev, b, ErrRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	appInitializeReject(t, c, p, ctx, rev, b, context.Canceled)
	appInitializeReject(t, c, p, context.Background(), "bad revision", b, ErrRequest)
	appInitializeReject(t, c, p, context.Background(), "stale", b, ErrConflict)
	appInitializeReject(t, c, p, context.Background(), rev, AppEntryBinding{}, config.ErrAppBinding)
	key, r := codecSettingsInputs()
	key[0] ^= 255
	different, err := NewSourceSettings(key, r)
	if err != nil {
		t.Fatal(err)
	}
	wrong := initializeApp(t, different, "app-a", 27001, "q")
	appInitializeReject(t, c, p, context.Background(), rev, wrong, config.ErrAppBinding)
	c.serviceQuiesced = true
	appInitializeReject(t, c, p, context.Background(), rev, b, config.ErrClosed)
	c.serviceQuiesced = false
	c.Close()
	appInitializeReject(t, c, p, context.Background(), rev, b, config.ErrClosed)
	var absent *Coordinator
	if _, err = absent.InitializeAppEntry(context.Background(), rev, b); !errors.Is(err, config.ErrClosed) {
		t.Fatal(err)
	}
}

func TestAppEntryInitializeCancellationWhileWaitingForSharedLock(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	b := initializeApp(t, s, "app-a", 27001, "q")
	rev := initializeRevision(t, c)
	copyHandle := *c
	before := coordinatorTree(t, p)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &initializeObserveContext{Context: parent, entered: make(chan struct{})}
	type result struct {
		sha string
		err error
	}
	done := make(chan result, 1)
	c.mu.Lock()
	go func() { sha, err := copyHandle.InitializeAppEntry(ctx, rev, b); done <- result{sha, err} }()
	<-ctx.entered
	cancel()
	c.mu.Unlock()
	got := <-done
	if got.sha != "" || !errors.Is(got.err, context.Canceled) {
		t.Fatal("cancelled waiter published", got.err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("cancelled preparation changed state")
	}
}
