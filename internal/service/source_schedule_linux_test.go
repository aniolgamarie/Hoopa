//go:build linux && amd64

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func TestSourceScheduleAPIActualServicePulseAndRestart(t *testing.T) {
	paths := apiPaths(t)
	sourceAPIInitialize(t, paths)
	provider := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(provider, []byte(`{"proxies":[{"name":"US service-fixture","type":"http","server":"fixture.example","port":443}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	// The stopped committed bundle is synthetic. This tests the actual service
	// timer/updater/IPC, not core Apply, original ownership or packet forwarding.
	intent := map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": config.BinaryReference{Path: "/uninstalled/scheduler-core", Version: "fixture", SHA256: strings.Repeat("a", 64)}}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": provider}, "update_hours": 1}}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	m, files, err := config.AssembleBundle("service-schedule-fixture", "synthetic-schedule", strings.Repeat("b", 64), []config.BinaryReference{{Path: "/uninstalled/scheduler-core", Version: "fixture", SHA256: strings.Repeat("a", 64)}}, []config.BundleAsset{{Kind: "intent", Name: "intent.json", Data: raw}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := store.PublishBundle(m, files)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	base, err := store.ReadActive()
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	g, op := m.Generation, config.ID("synthetic-schedule-operation")
	base.Generation, base.ManifestSHA256, base.OperationID = &g, sha, &op
	if err = store.PublishActive(base); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	var first control.SourceState
	for turn := 0; turn < 2; turn++ {
		if turn == 1 {
			if err = os.Remove(provider); err != nil {
				t.Fatal(err)
			}
		}
		_, running := capacityStartService(t, paths)
		deadline := time.After(5 * time.Second)
		for {
			response := capacityCall(t, paths, apiRequest(t, "schedule-status", "v1", "status.get", map[string]any{}))
			envelope, code := apiResponse(t, response)
			if code != "" {
				t.Fatal(code)
			}
			var status control.ServiceStatus
			if json.Unmarshal(envelope["data"], &status) != nil {
				t.Fatal("status decode")
			}
			response = capacityCall(t, paths, apiRequest(t, "schedule-list", "v1", "subscription.list", map[string]any{}))
			envelope, code = apiResponse(t, response)
			if code != "" {
				t.Fatal(code)
			}
			var page control.SourcePage
			if json.Unmarshal(envelope["data"], &page) != nil {
				t.Fatal("list decode")
			}
			if status.SourceSchedule != nil && status.SourceSchedule.LastCheckedAt != nil && len(page.Sources) == 1 && page.Sources[0].State == "succeeded" {
				state := page.Sources[0]
				if status.SourceSchedule.State != "waiting" || state.Sequence != 1 || !state.Scheduled || state.EffectiveNextDue == nil || state.NextDue == nil || *state.EffectiveNextDue != *state.NextDue || !state.Pending || state.AppliedSnapshot != nil {
					t.Fatal("timer/source projection or duplicate attempt")
				}
				if turn == 0 {
					first = state
				} else if !reflect.DeepEqual(first, state) {
					t.Fatal("service restart repeated reserved interval")
				}
				break
			}
			select {
			case <-deadline:
				t.Fatal("service timer did not complete first pulse")
			case <-time.After(time.Millisecond):
			}
		}
		response := capacityCall(t, paths, apiRequest(t, "stop-scheduled-service-"+string(rune('a'+turn)), "v1", "service.stop", map[string]any{"expected_revision": base.Revision}))
		if _, code := apiResponse(t, response); code != "" {
			t.Fatal("stop schedule service", code)
		}
		select {
		case <-running.done:
		case <-time.After(5 * time.Second):
			t.Fatal("service did not join timer")
		}
		read, err := config.OpenStore(paths, false)
		if err != nil {
			t.Fatal(err)
		}
		active, err := read.ReadActive()
		read.Close()
		if err != nil || !reflect.DeepEqual(base, active) {
			t.Fatal("automatic timer applied configuration", err)
		}
	}
	sum := sha256.Sum256(raw)
	t.Log("synthetic committed Intent SHA", hex.EncodeToString(sum[:]), "real service startup pulse fetched once; restart kept sequence 1; active unchanged; both service.stop calls joined")
	if _, err := control.ReadSources(context.Background(), paths, ""); err != nil {
		t.Fatal("offline list after scheduled shutdown", err)
	}
}
