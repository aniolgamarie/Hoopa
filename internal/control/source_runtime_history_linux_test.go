//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/subscription"
)

func TestSourceRuntimeHistoryLiveDownloadIsNeutralButUnprovedPendingIsNot(t *testing.T) {
	gate, source := updaterHTTPS(t)
	c, p, updater := updaterFixture(t, subscription.FetchOptions{Transport: gate.server.Client().Transport})
	raw := updaterRequest(t, c, "live-history", "source-a", source, nil)
	updaterBegin(t, updater, raw)
	updaterEntered(t, gate)
	request, _ := ParseRequest(raw)
	base, _ := c.store.ReadActive()
	before := coordinatorTree(t, p)
	c.mu.Lock()
	record, err := c.journal.Lookup(request)
	allowed := err == nil && c.liveSourceUpdateLocked(record) && c.configurationNeutralHistory(record, base)
	id, applyErr := c.activeRunForApply(base, "start", "")
	alive := c.sourceAlive
	c.sourceAlive = nil
	deadAllowed := c.liveSourceUpdateLocked(record) || c.configurationNeutralHistory(record, base)
	_, deadErr := c.activeRunForApply(base, "start", "")
	c.sourceAlive = alive
	c.mu.Unlock()
	if err != nil || !allowed || id != nil || applyErr != nil || deadAllowed || !errors.Is(deadErr, ErrRecoveryRequired) {
		t.Fatal("live/dead history proof", err, applyErr, deadErr)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("history qualification wrote state")
	}
	gate.once.Do(func() { close(gate.release) })
	if entry := updaterWait(t, updater, raw); entry.Operation.Record.Phase != Committed {
		t.Fatal("history check affected original worker")
	}
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRuntimeHistoryAllowsProvedTerminalUpdates(t *testing.T) {
	for _, body := range []string{updaterProvider, `{"invalid":true}`} {
		t.Run(body[:10], func(t *testing.T) {
			c, p, u := updaterFixture(t, subscription.FetchOptions{})
			raw := updaterRequest(t, c, "neutral-source", "source-a", updaterFile(t, body), nil)
			updaterBegin(t, u, raw)
			entry := updaterWait(t, u, raw)
			base, _ := c.store.ReadActive()
			before := coordinatorTree(t, p)
			c.mu.Lock()
			allowed := c.configurationNeutralHistory(entry.Operation, base)
			id, err := c.activeRunForApply(base, "start", "")
			c.mu.Unlock()
			if !allowed || err != nil || id != nil {
				t.Fatal("terminal source history blocked first Apply", err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("admission proof wrote")
			}
			c.mu.Lock()
			compact, err := c.journal.Compact(entry.Context.OperationID, entry.Operation.SHA256)
			c.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			allowed = c.configurationNeutralHistory(compact, base)
			c.mu.Unlock()
			if !allowed {
				t.Fatal("compacted terminal source lost context proof")
			}
			if err = os.Remove(filepath.Join(p.State, "source-assets", entry.Receipt.ContextSHA256+".json")); err != nil {
				t.Fatal(err)
			}
			before = coordinatorTree(t, p)
			c.mu.Lock()
			id, err = c.activeRunForApply(base, "start", "")
			c.mu.Unlock()
			if !errors.Is(err, ErrRecoveryRequired) || id != nil {
				t.Fatal("missing source provenance allowed first start", err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("refused proof repaired")
			}
		})
	}
}

func TestSourceRuntimeHistoryRecoveryTargetLinkRetained(t *testing.T) {
	c, p := sourceRecoverTestCoordinator(t)
	f := sourceRecoverTestSeed(t, c, "neutral-pending", "source-a", updaterFile(t, updaterProvider), Applying, "")
	base, _ := c.store.ReadActive()
	c.mu.Lock()
	_, err := c.activeRunForApply(base, "start", "")
	c.mu.Unlock()
	if !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("pending source history allowed first start", err)
	}
	raw := sourceRecoverTestRequest(t, c, f.entry.Operation, "neutral-recover")
	call, made, err := sourceRecoverTestCall(t, c, raw, nil)
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("source recovery", err)
	}
	before := coordinatorTree(t, p)
	c.mu.Lock()
	allowed := c.configurationNeutralHistory(call, base)
	id, err := c.activeRunForApply(base, "start", "")
	_, compactErr := c.journal.Compact(call.Record.Identity.OperationID, call.SHA256)
	c.mu.Unlock()
	if !allowed || id != nil || err != nil {
		t.Fatal("source-only recover blocked first Apply", err)
	}
	if compactErr != ErrOperationIncomplete {
		t.Fatal("target link discarded", compactErr)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("proof/forbidden compaction wrote")
	}
}
