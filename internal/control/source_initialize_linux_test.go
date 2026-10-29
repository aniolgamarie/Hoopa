//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hoopa/internal/config"
)

func initializeSettings(t *testing.T) SourceSettings {
	t.Helper()
	key, runtime := codecSettingsInputs()
	s, err := NewSourceSettings(key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func initializeRevision(t *testing.T, c *Coordinator) config.Revision {
	t.Helper()
	a, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	return a.Revision
}
func initializeReject(t *testing.T, c *Coordinator, p config.Paths, ctx context.Context, rev config.Revision, s SourceSettings, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	sha, err := c.InitializeSourceSettings(ctx, rev, s)
	if sha != "" || !errors.Is(err, want) {
		t.Fatalf("initialize rejection %q %v want %v", sha, err, want)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("initialize rejection changed durable tree")
	}
}
func initializePut(t *testing.T, c *Coordinator, s SourceSettings) {
	t.Helper()
	sha, err := c.InitializeSourceSettings(context.Background(), initializeRevision(t, c), s)
	if err != nil || sha != s.Digest() {
		t.Fatalf("initialize %q %v", sha, err)
	}
}
func initializeRemoveSettings(t *testing.T, c *Coordinator, p config.Paths, s SourceSettings) {
	t.Helper()
	initializePut(t, c, s)
	if err := os.Remove(filepath.Join(p.State, "credentials", "source-settings-v1.json")); err != nil {
		t.Fatal(err)
	}
}
func initializeReopen(t *testing.T, c *Coordinator, p config.Paths) *Coordinator {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenCoordinator(p, coordinatorValidator(t), func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	return next
}

func TestSourceInitializeFirstInstallExactReplayAndReopen(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	settings := initializeSettings(t)
	active, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	initializePut(t, c, settings)
	raw, sha, err := c.store.ReadSourceSettings()
	expected, _ := settings.SensitiveBytes()
	if err != nil || sha != settings.Digest() || !bytes.Equal(raw, expected) {
		t.Fatal("settings roundtrip", err)
	}
	after := coordinatorTree(t, p)
	delete(after, filepath.Join(p.State, "credentials", "source-settings-v1.json"))
	delete(after, filepath.Join(p.State, "credentials"))
	delete(before, filepath.Join(p.State, "credentials"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("first install touched active/journal/outside credentials")
	}
	before = coordinatorTree(t, p)
	initializePut(t, c, settings)
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("exact resync mutated bytes or metadata")
	}
	c = initializeReopen(t, c, p)
	initializePut(t, c, settings)
	a, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(active, a) {
		t.Fatal("reopen initialize moved revision")
	}
	key, runtime := codecSettingsInputs()
	key[0] ^= 255
	other, err := NewSourceSettings(key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	initializeReject(t, c, p, context.Background(), active.Revision, other, config.ErrSourceSettingsConflict)
}
func TestSourceInitializeShapeCancellationStaleQuiesceClosed(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	rev := initializeRevision(t, c)
	initializeReject(t, c, p, nil, rev, s, ErrRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	initializeReject(t, c, p, ctx, rev, s, context.Canceled)
	initializeReject(t, c, p, context.Background(), "invalid revision", s, ErrRequest)
	initializeReject(t, c, p, context.Background(), rev, SourceSettings{}, ErrSourceSettings)
	initializeReject(t, c, p, context.Background(), "stale-revision", s, ErrConflict)
	c.serviceQuiesced = true
	initializeReject(t, c, p, context.Background(), rev, s, config.ErrClosed)
	c.serviceQuiesced = false
	c.Close()
	initializeReject(t, c, p, context.Background(), rev, s, config.ErrClosed)
	var zero Coordinator
	if sha, err := zero.InitializeSourceSettings(context.Background(), rev, s); sha != "" || err != config.ErrClosed {
		t.Fatal("zero coordinator", err)
	}
	var absent *Coordinator
	if sha, err := absent.InitializeSourceSettings(context.Background(), rev, s); sha != "" || err != config.ErrClosed {
		t.Fatal("nil coordinator", err)
	}
}

type initializeObserveContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *initializeObserveContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.entered) })
	return err
}
func TestSourceInitializeSharedCoordinatorLockAndCancellationBeforePublish(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	rev := initializeRevision(t, c)
	copyHandle := *c
	before := coordinatorTree(t, p)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &initializeObserveContext{Context: parent, entered: make(chan struct{})}
	type outcome struct {
		sha string
		err error
	}
	done := make(chan outcome, 1)
	c.mu.Lock()
	go func() { sha, err := copyHandle.InitializeSourceSettings(ctx, rev, s); done <- outcome{sha, err} }()
	// The first Err captures a live context before the original shared lock is
	// released. Cancellation must then win at the final pre-publication check.
	<-ctx.entered
	cancel()
	c.mu.Unlock()
	result := <-done
	if result.sha != "" || !errors.Is(result.err, context.Canceled) {
		t.Fatal("blocked cancellation published", result.err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("cancelled initialization wrote")
	}
}

// These generic journal records test only immutable history guards. They are
// not real subscription updates, runtime effects, successful Apply or recovery.
func initializeGenericHistory(t *testing.T, c *Coordinator, name, state string) StoredOperation {
	t.Helper()
	params := `{}`
	if name == "runtime.start" {
		params = `{"control_only":false}`
	}
	r := request(t, "history-"+name, "v1", name, params)
	op := admit(t, c.journal, r)
	if state != "pending" {
		for _, phase := range []Phase{Validated, Applying, Verifying, Publishing, Committed} {
			next, err := c.journal.Advance(r.OperationID(), op.SHA256, phase, json.RawMessage(`{"synthetic_guard":true}`), json.RawMessage(`{"outcome":"guard-fixture"}`))
			if err != nil {
				t.Fatal(err)
			}
			op = next
		}
	}
	if state == "compact" {
		next, err := c.journal.Compact(r.OperationID(), op.SHA256)
		if err != nil {
			t.Fatal(err)
		}
		op = next
		if !op.Record.Compacted {
			t.Fatal("fixture not compacted")
		}
	}
	return op
}
func initializeCompactRefused(t *testing.T, c *Coordinator, p config.Paths, op StoredOperation) {
	t.Helper()
	before := coordinatorTree(t, p)
	if _, err := c.journal.Compact(op.Record.Identity.OperationID, op.SHA256); err != ErrOperationIncomplete {
		t.Fatal("lifecycle original unexpectedly compacted", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("refused compaction changed original history")
	}
}

func TestSourceInitializeMissingSettingsWithJournalHistoryRefusesRekey(t *testing.T) {
	for _, name := range []string{"subscription.update", "config.apply", "runtime.start", "runtime.stop"} {
		states := []string{"pending", "terminal", "compact"}
		if name == "runtime.start" || name == "runtime.stop" {
			states = states[:2] // Lifecycle originals are deliberately not compactable.
		}
		for _, state := range states {
			t.Run(name+"/"+state, func(t *testing.T) {
				c, p := coordinatorFixture(t, nil)
				s := initializeSettings(t)
				initializeRemoveSettings(t, c, p, s)
				op := initializeGenericHistory(t, c, name, state)
				if state == "terminal" && (name == "runtime.start" || name == "runtime.stop") {
					initializeCompactRefused(t, c, p, op)
				}
				c = initializeReopen(t, c, p)
				initializeReject(t, c, p, context.Background(), initializeRevision(t, c), s, ErrRecoveryRequired)
			})
		}
	}
	t.Run("unrelated-nonterminal", func(t *testing.T) {
		c, p := coordinatorFixture(t, nil)
		initializeGenericHistory(t, c, "service.stop", "pending")
		initializeReject(t, c, p, context.Background(), initializeRevision(t, c), initializeSettings(t), ErrRecoveryRequired)
	})
	for _, state := range []string{"terminal"} {
		t.Run("unrelated-"+state, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			op := initializeGenericHistory(t, c, "service.stop", state)
			initializeCompactRefused(t, c, p, op)
			before := snapshot(t, filepath.Join(p.State, "operations"))
			initializePut(t, c, initializeSettings(t))
			if !reflect.DeepEqual(before, snapshot(t, filepath.Join(p.State, "operations"))) {
				t.Fatal("unrelated terminal journal changed")
			}
		})
	}
}
func TestSourceInitializeMissingSettingsWithAssetsOrActiveRefusesRekey(t *testing.T) {
	for _, kind := range []string{"asset", "staging", "active-operation", "active-generation-stopped", "active-running"} {
		t.Run(kind, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			s := initializeSettings(t)
			initializeRemoveSettings(t, c, p, s)
			switch kind {
			case "asset":
				if _, err := c.store.PublishSourceAsset([]byte(`{"orphan":"not latest"}`)); err != nil {
					t.Fatal(err)
				}
			case "staging":
				dir := filepath.Join(p.State, "source-assets")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".source-"+strings.Repeat("ab", 24)), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "active-operation":
				a, err := c.store.ReadActive()
				if err != nil {
					t.Fatal(err)
				}
				op := config.ID("old-operation")
				a.OperationID = &op
				if err := c.store.PublishActive(a); err != nil {
					t.Fatal(err)
				}
			default:
				files := map[string][]byte{"intent.json": []byte(`{}`), "core.yaml": []byte("mode: rule\n")}
				m := config.BundleManifest{Version: 1, Generation: "synthetic-history-generation", CompilerVersion: "guard-fixture", CapabilitiesDigest: strings.Repeat("a", 64), Binaries: []config.BinaryReference{{Path: "/uninstalled/fixture-core", Version: "guard-fixture", SHA256: strings.Repeat("b", 64)}}}
				for _, item := range []struct{ kind, name string }{{"intent", "intent.json"}, {"generated_config", "core.yaml"}} {
					b := files[item.name]
					m.Files = append(m.Files, config.BundleFile{Kind: item.kind, Name: item.name, Bytes: int64(len(b)), SHA256: hash(b)})
				}
				sha, err := c.store.PublishBundle(m, files)
				if err != nil {
					t.Fatal(err)
				}
				a, err := c.store.ReadActive()
				if err != nil {
					t.Fatal(err)
				}
				op := config.ID("old-operation")
				g := m.Generation
				a.Generation = &g
				a.ManifestSHA256 = sha
				a.OperationID = &op
				if kind == "active-running" {
					a.DesiredRuntime = "running"
				}
				if err := c.store.PublishActive(a); err != nil {
					t.Fatal(err)
				}
			}
			c = initializeReopen(t, c, p)
			initializeReject(t, c, p, context.Background(), initializeRevision(t, c), s, ErrRecoveryRequired)
		})
	}
}
func TestSourceInitializeEmptyDirectoryAndExactPriorIgnoreUnrelatedHistory(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	if err := os.Mkdir(filepath.Join(p.State, "source-assets"), 0700); err != nil {
		t.Fatal(err)
	}
	s := initializeSettings(t)
	initializePut(t, c, s)
	// Existing exact settings are re-synced even if a later operation is pending.
	initializeGenericHistory(t, c, "subscription.update", "pending")
	if _, err := c.store.PublishSourceAsset([]byte(`{"history":true}`)); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	initializePut(t, c, s)
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("exact replay touched history")
	}
}
func TestSourceInitializeCorruptOrUnsafeStateIsReadFailureAndZeroWrite(t *testing.T) {
	for _, kind := range []string{"corrupt-settings", "foreign-settings", "settings-mode", "settings-link", "corrupt-journal", "bad-asset-name", "unsafe-asset-mode"} {
		t.Run(kind, func(t *testing.T) {
			c, p := coordinatorFixture(t, nil)
			s := initializeSettings(t)
			path := filepath.Join(p.State, "credentials", "source-settings-v1.json")
			switch kind {
			case "corrupt-settings", "foreign-settings", "settings-mode", "settings-link":
				initializePut(t, c, s)
				switch kind {
				case "corrupt-settings":
					if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
						t.Fatal(err)
					}
				case "foreign-settings":
					if err := os.WriteFile(path, []byte(`{"foreign":true}`), 0600); err != nil {
						t.Fatal(err)
					}
				case "settings-mode":
					if err := os.Chmod(path, 0644); err != nil {
						t.Fatal(err)
					}
				case "settings-link":
					if err := os.Link(path, filepath.Join(p.State, "linked-settings")); err != nil {
						t.Fatal(err)
					}
				}
			case "corrupt-journal":
				op := initializeGenericHistory(t, c, "service.stop", "terminal")
				if err := os.WriteFile(filepath.Join(p.State, "operations", string(op.Record.Identity.OperationID)+".json"), []byte("invalid journal"), 0600); err != nil {
					t.Fatal(err)
				}
			case "bad-asset-name", "unsafe-asset-mode":
				dir := filepath.Join(p.State, "source-assets")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				name := "malformed"
				mode := os.FileMode(0600)
				if kind == "unsafe-asset-mode" {
					name = strings.Repeat("a", 64) + ".json"
					mode = 0644
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), mode); err != nil {
					t.Fatal(err)
				}
			}
			initializeReject(t, c, p, context.Background(), initializeRevision(t, c), s, config.ErrReadUnavailable)
		})
	}
}
