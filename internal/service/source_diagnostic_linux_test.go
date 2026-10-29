//go:build linux && amd64

package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/control"
	"hoopa/internal/mihomo"
)

func TestSourcePlanDiagnosticActualWirePreservesReasonWithoutPrivateData(t *testing.T) {
	input := os.Getenv("HOOPA_P1_SOURCES_CORE")
	if input == "" {
		t.Fatal("approved source core input required")
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
		t.Fatal("unexpected core bytes")
	}
	if err = os.WriteFile(core, raw, 0700); err != nil {
		t.Fatal(err)
	}
	paths := apiPaths(t)
	revision := sourceAPIInitialize(t, paths)
	provider := filepath.Join(t.TempDir(), "private-provider.json")
	if err = os.WriteFile(provider, []byte(`{"proxies":[{"name":"US private-display","type":"http","server":"private-endpoint.example","port":443,"username":"private-user","password":"private-password"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	baseIntent := func() map[string]any {
		return map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": capability.Binary}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": provider}, "update_hours": nil}}, "selection": map[string]any{"active_subscription": "source-a"}, "groups": []any{map[string]any{"id": "US", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "http://127.0.0.1:1/probe", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}}}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "group", "group_id": "US"}}
	}
	_, _ = capacityStartService(t, paths)
	update := apiRequest(t, "diagnostic-cache", "v1", "subscription.update", map[string]any{"expected_revision": revision, "subscription_id": "source-a", "intent": baseIntent(), "region_rules": []any{map[string]any{"prefix": "US ", "region": "US"}}})
	if _, code := apiResponse(t, capacityCall(t, paths, update)); code != "" {
		t.Fatal(code)
	}
	request, _ := control.ParseRequest(update)
	if view := sourceAPIPoll(t, paths, request.OperationID()); view.Phase != control.Committed {
		t.Fatal("provider not committed")
	}
	for _, kind := range []string{"required-region", "manual-missing", "fixed-missing", "target-group", "source-reference", "source-snapshot"} {
		t.Run(kind, func(t *testing.T) {
			intent := baseIntent()
			group := intent["groups"].([]any)[0].(map[string]any)
			reason, field, code := "", "", "INVALID_ARGUMENT"
			switch kind {
			case "required-region":
				group["region"] = "JP"
				reason, field = "required_group_no_region_match", "groups[US].region"
			case "manual-missing":
				group["strategy"] = "manual"
				delete(group, "probe")
				group["selected_node_id"] = "absent-node"
				reason, field = "selected_node_missing", "groups[US].selected_node_id"
			case "fixed-missing":
				intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "source-a", "node_id": "absent-node"}
				reason, field = "pinned_node_missing", "default_target"
			case "target-group":
				intent["default_target"] = map[string]any{"kind": "group", "group_id": "absent-group"}
				reason, field = "target_group_missing", "default_target"
			case "source-reference":
				intent["selection"] = map[string]any{"active_subscription": "absent-source"}
				reason, field = "source_reference_missing", "selection.active_subscription"
			case "source-snapshot":
				intent["subscriptions"] = append(intent["subscriptions"].([]any), map[string]any{"id": "source-b", "source": map[string]any{"kind": "file", "path": provider}, "update_hours": nil})
				intent["selection"] = map[string]any{"active_subscription": "source-b"}
				reason, field, code = "source_snapshot_missing", "subscriptions[source-b]", "READ_UNAVAILABLE"
			}
			before := apiTree(t, paths)
			response := capacityCall(t, paths, apiRequest(t, "diagnostic-"+kind, "v1", "config.plan", map[string]any{"intent": intent}))
			envelope, actualCode := apiResponse(t, response)
			var failure struct {
				Details struct {
					Reason string `json:"reason"`
					Field  string `json:"field"`
				} `json:"details"`
			}
			if json.Unmarshal(envelope["error"], &failure) != nil || actualCode != code || failure.Details.Reason != reason || failure.Details.Field != field || string(envelope["data"]) != "null" {
				t.Fatal("specific reason lost in actual transport", actualCode, failure.Details)
			}
			wantExit := 2
			if code == "READ_UNAVAILABLE" {
				wantExit = 4
			}
			if ExitCode(response) != wantExit || !reflect.DeepEqual(before, apiTree(t, paths)) {
				t.Fatal("diagnostic changed exit class or wrote state")
			}
			for _, secret := range []string{provider, core, "private-display", "private-endpoint.example", "private-user", "private-password", strings.Repeat("s", 32)} {
				if bytes.Contains(response, []byte(secret)) {
					t.Fatal("diagnostic exposed private material")
				}
			}
		})
	}
}
