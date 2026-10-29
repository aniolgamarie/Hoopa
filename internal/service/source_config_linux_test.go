//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/control"
	"hoopa/internal/mihomo"
)

func TestSourceConfigAPIOnlineOfflinePlanAndApplyRefusal(t *testing.T) {
	input := os.Getenv("HOOPA_P1_SOURCES_CORE")
	if input == "" {
		t.Fatal("actual approved static core is required for source Plan acceptance")
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Join(t.TempDir(), "mihomo")
	capability, err := mihomo.ApprovedSourcesCapability(core)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != capability.Binary.SHA256 {
		t.Fatal("wrong fixed source core")
	}
	if err = os.WriteFile(core, raw, 0700); err != nil {
		t.Fatal(err)
	}
	paths := apiPaths(t)
	revision := sourceAPIInitialize(t, paths)
	intent := map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": capability.Binary}, "subscriptions": []any{}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
	request := apiRequest(t, "local-plan", "v1", "config.plan", map[string]any{"intent": intent})
	before := apiTree(t, paths)
	offline, err := Call(context.Background(), paths, request)
	if err != nil || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("offline Plan wrote or failed", err)
	}
	envelope, code := apiResponse(t, offline)
	if code != "" {
		t.Fatal(code)
	}
	var plan control.PlanDocument
	if json.Unmarshal(envelope["data"], &plan) != nil || plan.BaseRevision != string(revision) || plan.PlanDigest == "" || plan.Impact.RuntimeAction != "start" {
		t.Fatal("public Plan shape/binding")
	}
	_, running := capacityStartService(t, paths)
	before = apiTree(t, paths)
	online := capacityCall(t, paths, request)
	if !bytes.Equal(offline, online) || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("online Plan differs or writes")
	}
	for _, kind := range []string{"wrong-confirmation", "no-disruption-consent", "missing-param", "unknown-param", "invalid-plan"} {
		t.Run(kind, func(t *testing.T) {
			params := map[string]any{"plan": json.RawMessage(envelope["data"]), "confirmed_plan_digest": plan.PlanDigest, "accept_disruption": true}
			want := "AUTHORIZATION_DENIED"
			switch kind {
			case "wrong-confirmation":
				params["confirmed_plan_digest"] = strings.Repeat("0", 64)
			case "no-disruption-consent":
				params["accept_disruption"] = false
			case "missing-param":
				delete(params, "accept_disruption")
				want = "INVALID_ARGUMENT"
			case "unknown-param":
				params["repair"] = true
				want = "INVALID_ARGUMENT"
			case "invalid-plan":
				params["plan"] = map[string]any{}
				want = "INVALID_ARGUMENT"
			}
			before := apiTree(t, paths)
			response := capacityCall(t, paths, apiRequest(t, "apply-"+kind, "v1", "config.apply", params))
			_, code := apiResponse(t, response)
			if code != want || !reflect.DeepEqual(before, apiTree(t, paths)) {
				t.Fatal("Apply refusal wrote or lost error class", code, want)
			}
		})
	}
	running.cancel()
	select {
	case <-running.done:
	case <-time.After(5 * time.Second):
		t.Fatal("service failed to stop")
	}
	before = apiTree(t, paths)
	validApply := apiRequest(t, "offline-apply", "v1", "config.apply", map[string]any{"plan": json.RawMessage(envelope["data"]), "confirmed_plan_digest": plan.PlanDigest, "accept_disruption": true})
	if result, err := Call(context.Background(), paths, validApply); !errors.Is(err, ErrServiceUnavailable) || result != nil || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("offline Apply implicitly started/initialized service", err)
	}
	if err = os.Remove(core); err != nil {
		t.Fatal(err)
	}
	before = apiTree(t, paths)
	if result, err := Call(context.Background(), paths, request); err == nil || result != nil || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("missing local core silently repaired/accepted")
	}
}
