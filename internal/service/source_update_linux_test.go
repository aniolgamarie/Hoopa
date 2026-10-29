//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

func sourceAPIInitialize(t *testing.T, paths config.Paths) config.Revision {
	t.Helper()
	c, err := control.OpenCoordinator(paths, unavailableIntent, unavailableInputs)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s, err := control.NewSourceSettings(bytes.Repeat([]byte{'k'}, 32), policy.RuntimeInput{Controller: "127.0.0.1:19871", Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared", Type: "mixed", Listen: "127.0.0.1", Port: 19872, Users: []profile.User{{Username: strings.Repeat("u", 32), Password: strings.Repeat("p", 32)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.InitializeSourceSettings(context.Background(), status.Revision, s); err != nil {
		t.Fatal(err)
	}
	return status.Revision
}
func sourceAPIRequest(t *testing.T, id string, revision config.Revision, path string) []byte {
	t.Helper()
	intent := map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": map[string]any{"path": "/private/uninstalled-core", "version": "fixture", "sha256": strings.Repeat("a", 64)}}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": path}, "update_hours": 1}}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": []any{}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
	return apiRequest(t, id, "v1", "subscription.update", map[string]any{"expected_revision": revision, "subscription_id": "source-a", "intent": intent, "region_rules": []any{}})
}
func sourceAPIPoll(t *testing.T, paths config.Paths, id config.ID) control.OperationView {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		raw := apiRequest(t, "poll-source-update", "v1", "operation.get", map[string]any{"operation_id": id})
		response, err := Call(context.Background(), paths, raw)
		if err != nil {
			t.Fatal(err)
		}
		envelope, code := apiResponse(t, response)
		if code != "" {
			t.Fatal(code)
		}
		var view control.OperationView
		if json.Unmarshal(envelope["data"], &view) != nil || view.SourceUpdate == nil {
			t.Fatal("missing source progress")
		}
		if view.Phase == control.Committed || view.Phase == control.RolledBack {
			return view
		}
		if view.Recovery.Required {
			t.Fatal("live worker incorrectly needs explicit recovery")
		}
		select {
		case <-deadline:
			t.Fatal("source completion timeout")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestSourceAPIActualRunUpdatePollFailureAndOfflineReplay(t *testing.T) {
	paths := apiPaths(t)
	revision := sourceAPIInitialize(t, paths)
	provider := filepath.Join(t.TempDir(), "private-provider.json")
	if err := os.WriteFile(provider, []byte(`{"proxies":[{"name":"US service-fixture","type":"http","server":"fixture.example","port":443}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan ReadyInfo, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, paths, Options{LauncherArgs: []string{"never-launch-core"}, Ready: func(info ReadyInfo) error { ready <- info; return nil }})
	}()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("service failed to join")
			}
		}
	})
	select {
	case info := <-ready:
		if info.Revision != revision {
			t.Fatal("initial revision changed")
		}
	case err := <-done:
		joined = true
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("service readiness")
	}
	var failed []byte
	for i, id := range []string{"source-success", "source-failed"} {
		if i == 1 {
			if err := os.WriteFile(provider, []byte(`{"private-error":"invalid"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		raw := sourceAPIRequest(t, id, revision, provider)
		request, _ := control.ParseRequest(raw)
		response, err := Call(context.Background(), paths, raw)
		if err != nil {
			t.Fatal(err)
		}
		envelope, code := apiResponse(t, response)
		if code != "" || !bytes.Contains(envelope["data"], []byte(`"phase":"prepared"`)) {
			t.Fatal("admission did not return a pending receipt", code)
		}
		view := sourceAPIPoll(t, paths, request.OperationID())
		if view.SourceUpdate.Sequence != uint64(i+1) || !view.SourceUpdate.Downloaded || view.SourceUpdate.Validated != (i == 0) || view.Recovery.Required {
			t.Fatal("attempt stage/recovery projection")
		}
		response, err = Call(context.Background(), paths, raw)
		if err != nil {
			t.Fatal(err)
		}
		_, code = apiResponse(t, response)
		if i == 0 && (code != "" || ExitCode(response) != 0 || view.Phase != control.Committed) {
			t.Fatal("successful replay")
		}
		if i == 1 && (code != "SUBSCRIPTION_UPDATE_FAILED" || ExitCode(response) != 4 || view.Phase != control.RolledBack || view.SourceUpdate.Reason != "validation_failed") {
			t.Fatal("failed replay did not cross strict wire", code, ExitCode(response))
		}
		for _, secret := range []string{provider, "private-error", "private/uninstalled-core"} {
			if bytes.Contains(response, []byte(secret)) {
				t.Fatal("private source input exposed")
			}
		}
		if i == 1 {
			failed = raw
		}
	}
	list := apiRequest(t, "list-source", "v1", "subscription.list", map[string]any{})
	listed, err := Call(context.Background(), paths, list)
	if err != nil {
		t.Fatal(err)
	}
	envelope, code := apiResponse(t, listed)
	var page control.SourcePage
	if code != "" || json.Unmarshal(envelope["data"], &page) != nil || page.Total != 1 || len(page.Sources) != 1 || page.Sources[0].State != "failed" || page.Sources[0].LatestSnapshot == nil || !page.Sources[0].Pending {
		t.Fatal("source list conflated failed attempt and usable candidate", code)
	}
	stop := apiRequest(t, "source-service-stop", "v1", "service.stop", map[string]any{"expected_revision": revision})
	response, err := Call(context.Background(), paths, stop)
	if err != nil {
		t.Fatal(err)
	}
	if _, code := apiResponse(t, response); code != "" {
		t.Fatal("stop", code)
	}
	select {
	case err = <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service stop did not join sources")
	}
	before := apiTree(t, paths)
	listed, err = Call(context.Background(), paths, list)
	if err != nil {
		t.Fatal("offline list", err)
	}
	if _, code := apiResponse(t, listed); code != "" {
		t.Fatal("offline list envelope", code)
	}
	response, err = Call(context.Background(), paths, failed)
	if err != nil {
		t.Fatal(err)
	}
	if _, code := apiResponse(t, response); code != "SUBSCRIPTION_UPDATE_FAILED" {
		t.Fatal("offline durable failure replay", code)
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("offline replay wrote")
	}
	unknown := sourceAPIRequest(t, "offline-new-source", revision, provider)
	if _, err = Call(context.Background(), paths, unknown); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal("offline update silently started service", err)
	}
}
