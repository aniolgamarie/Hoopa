//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/policy"
	"hoopa/internal/service"
	"hoopa/internal/subscription"
	profile "hoopa/protocol/sourceprofile"
)

const cliSubscriptionProvider = `{"proxies":[{"name":"US private-provider-label","type":"http","server":"proxy.example","port":443,"username":"private-provider-user","password":"private-provider-password"}]}`

func subscriptionCLIIntent(path string) map[string]any {
	return map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": map[string]any{"path": "/private/uninstalled-cli-core", "version": "fixture", "sha256": strings.Repeat("a", 64)}}, "subscriptions": []any{map[string]any{"id": "source-a", "source": map[string]any{"kind": "file", "path": path}, "update_hours": 1}}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
}
func subscriptionCLIFile(t *testing.T, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
func subscriptionCLIJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func subscriptionCLIError(t *testing.T, response map[string]json.RawMessage) string {
	t.Helper()
	var failure struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(response["error"], &failure) != nil {
		t.Fatal("invalid error envelope")
	}
	return failure.Code
}
func subscriptionCLIPrivate(t *testing.T, out string, paths ...string) {
	t.Helper()
	for _, private := range append(paths, "private-provider-label", "private-provider-password", "private-provider-user", "private-file-body", "private/uninstalled-cli-core", strings.Repeat("s", 32), strings.Repeat("p", 32), strings.Repeat("k", 32), base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'k'}, 32))) {
		if private != "" && strings.Contains(out, private) {
			t.Fatal("CLI output exposed private input")
		}
	}
}
func subscriptionCLIService(t *testing.T, paths config.Paths) (config.Revision, control.SourceSettings, func()) {
	t.Helper()
	c, err := control.OpenCoordinator(paths, func(json.RawMessage, []control.PlanInput) error { return control.ErrCapabilityUnsupported }, func(context.Context, control.Plan) (control.CurrentPlanInputs, error) {
		return control.CurrentPlanInputs{}, control.ErrCapabilityUnsupported
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := control.NewSourceSettings(bytes.Repeat([]byte{'k'}, 32), policy.RuntimeInput{Controller: "127.0.0.1:19881", Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared", Type: "mixed", Listen: "127.0.0.1", Port: 19882, Users: []profile.User{{Username: "shared-user", Password: strings.Repeat("p", 32)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.InitializeSourceSettings(context.Background(), status.Revision, settings); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan service.ReadyInfo, 1)
	done := make(chan error, 1)
	go func() {
		done <- service.Run(ctx, paths, service.Options{LauncherArgs: []string{"never-execute-a-core"}, Ready: func(info service.ReadyInfo) error { ready <- info; return nil }})
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("management service did not join")
			}
		})
	}
	t.Cleanup(stop)
	select {
	case info := <-ready:
		if info.Revision != status.Revision {
			t.Fatal("service changed initial revision")
		}
	case err := <-done:
		once.Do(func() { cancel() })
		t.Fatal("service failed before ready", err)
	case <-time.After(5 * time.Second):
		t.Fatal("service readiness timeout")
	}
	return status.Revision, settings, stop
}
func subscriptionCLIPoll(t *testing.T, id config.ID) control.OperationView {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		code, response, out := cliResponse(t, []string{"operation", "get", string(id)}, strings.NewReader(""))
		if code != 0 {
			t.Fatal("operation polling failed", code)
		}
		subscriptionCLIPrivate(t, out)
		var view control.OperationView
		if json.Unmarshal(response["data"], &view) != nil || view.SourceUpdate == nil {
			t.Fatal("source progress absent")
		}
		if view.Phase == control.Committed || view.Phase == control.RolledBack {
			return view
		}
		select {
		case <-deadline.C:
			t.Fatal("source worker completion timeout")
		case <-tick.C:
		}
	}
}
func subscriptionCLIMachine(t *testing.T, id string, revision config.Revision, intent any, rules any, includeRules bool) string {
	t.Helper()
	params := map[string]any{"subscription_id": "source-a", "expected_revision": revision, "intent": intent}
	if includeRules {
		params["region_rules"] = rules
	}
	raw, err := makeRequest(id, "subscription.update", params)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func subscriptionCLIAdmit(t *testing.T, args []string) (config.ID, string) {
	t.Helper()
	code, response, out := cliResponse(t, args, strings.NewReader(""))
	if code != 0 {
		t.Fatal("source admission failed", code, out)
	}
	var data struct {
		OperationID config.ID     `json:"operation_id"`
		Phase       control.Phase `json:"phase"`
	}
	if json.Unmarshal(response["data"], &data) != nil || data.OperationID == "" || data.Phase != control.Prepared {
		t.Fatal("CLI did not return durable pending receipt")
	}
	return data.OperationID, out
}
func TestSubscriptionCLIActualServiceUpdateListFailureReplayAndMachine(t *testing.T) {
	paths := cliPaths(t)
	revision, settings, stop := subscriptionCLIService(t, paths)
	provider := subscriptionCLIFile(t, "private-provider.json", []byte(cliSubscriptionProvider))
	intent := subscriptionCLIIntent(provider)
	yaml := fmt.Sprintf("schema_version: 1\ncore:\n  mihomo:\n    path: /private/uninstalled-cli-core\n    version: fixture\n    sha256: %q\nsubscriptions:\n- id: source-a\n  source:\n    kind: file\n    path: %q\n  update_hours: 1\nselection:\n  active_subscription: null\ngroups: []\napps: []\nrules: []\ndefault_target:\n  kind: reject\n", strings.Repeat("a", 64), provider)
	declaration := subscriptionCLIFile(t, "intent.yaml", []byte(yaml))
	link := filepath.Join(t.TempDir(), "ordinary-intent-symlink")
	if err := os.Symlink(declaration, link); err != nil {
		t.Fatal(err)
	}
	regions := subscriptionCLIFile(t, "regions.yaml", []byte("- prefix: 'US '\n  region: US\n- prefix: never-matched\n  region: JP\n"))
	rules := []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "never-matched", Region: "JP"}}
	args := []string{"subscription", "update", "source-a", "--input", link, "--regions", regions, "--request-id", "cli-source-success"}
	op, out := subscriptionCLIAdmit(t, args)
	subscriptionCLIPrivate(t, out, provider, link, declaration, regions)
	success := subscriptionCLIPoll(t, op)
	if success.Phase != control.Committed || !success.SourceUpdate.Downloaded || !success.SourceUpdate.Validated || success.SourceUpdate.Sequence != 1 || success.SourceUpdate.SnapshotID == nil {
		t.Fatal("real source update did not commit")
	}
	machine := subscriptionCLIMachine(t, "cli-source-success", revision, intent, rules, true)
	code, _, machineOut := cliResponse(t, []string{"api", "v1", "subscription.update"}, strings.NewReader(machine))
	humanCode, _, humanOut := cliResponse(t, args, strings.NewReader(""))
	if code != 0 || humanCode != 0 || machineOut != humanOut {
		t.Fatal("human and machine update are different use cases")
	}
	before := cliTree(t, paths)
	listCode, listResponse, listOut := cliResponse(t, []string{"subscription", "list"}, strings.NewReader(""))
	var page control.SourcePage
	if listCode != 0 || json.Unmarshal(listResponse["data"], &page) != nil || page.Total != 1 || len(page.Sources) != 1 || page.Sources[0].SourceID != "source-a" || page.Sources[0].State != "succeeded" {
		t.Fatal("human list result")
	}
	subscriptionCLIPrivate(t, listOut, provider, declaration, link, regions)
	listRaw, _ := makeRequest("machine-source-list", "subscription.list", map[string]any{})
	code, response, _ := cliResponse(t, []string{"api", "v1", "subscription.list"}, bytes.NewReader(listRaw))
	var machinePage control.SourcePage
	if code != 0 || json.Unmarshal(response["data"], &machinePage) != nil || page.ViewID != machinePage.ViewID || !reflect.DeepEqual(page.Sources, machinePage.Sources) {
		t.Fatal("machine/human list disagree")
	}
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("list queries wrote")
	}
	// Valid token shape must reach the source-list use case verbatim, where its
	// deliberately changed view fails as SNAPSHOT_EXPIRED rather than bad syntax.
	otherView := strings.Repeat("0", 64)
	if page.ViewID == otherView {
		otherView = strings.Repeat("1", 64)
	}
	cursor := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1,"view":"` + otherView + `","after":"source-a"}`))
	code, response, _ = cliResponse(t, []string{"subscription", "list", "--cursor", cursor}, strings.NewReader(""))
	if code != 4 || subscriptionCLIError(t, response) != "SNAPSHOT_EXPIRED" {
		t.Fatal("cursor was altered or bypassed source validator")
	}
	// --input only declared this acquisition: it did not publish a committed
	// runtime Intent that could supply a later update with no declaration.
	before = cliTree(t, paths)
	code, _, _ = cliResponse(t, []string{"subscription", "update", "source-a", "--request-id", "no-committed-definition"}, strings.NewReader(""))
	if code != 2 || !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("input implicitly saved a runtime draft or fallback definition")
	}
	if err := os.WriteFile(provider, []byte(`{"private-provider-error":"private-file-body"}`), 0644); err != nil {
		t.Fatal(err)
	}
	failArgs := []string{"subscription", "update", "source-a", "--input", declaration, "--request-id", "cli-source-failed", "--expected-revision", string(revision)}
	failedOp, _ := subscriptionCLIAdmit(t, failArgs)
	failed := subscriptionCLIPoll(t, failedOp)
	if failed.Phase != control.RolledBack || failed.SourceUpdate.Sequence != 2 || !failed.SourceUpdate.Downloaded || failed.SourceUpdate.Validated || failed.SourceUpdate.Reason != "validation_failed" {
		t.Fatal("failed worker projection")
	}
	failRaw := subscriptionCLIMachine(t, "cli-source-failed", revision, intent, nil, false)
	code, response, failedMachine := cliResponse(t, []string{"api", "v1", "subscription.update"}, strings.NewReader(failRaw))
	humanCode, _, failedHuman := cliResponse(t, failArgs, strings.NewReader(""))
	if code != 4 || humanCode != 4 || subscriptionCLIError(t, response) != "SUBSCRIPTION_UPDATE_FAILED" || failedMachine != failedHuman {
		t.Fatal("durable failure replay differs across frontends")
	}
	subscriptionCLIPrivate(t, failedHuman, provider, declaration)
	if err := os.WriteFile(provider, []byte(cliSubscriptionProvider), 0644); err != nil {
		t.Fatal(err)
	}
	empty := subscriptionCLIFile(t, "clear-regions.json", []byte(`[]`))
	clearArgs := []string{"subscription", "update", "source-a", "--input", declaration, "--regions", empty, "--request-id", "cli-source-clear"}
	clearOp, _ := subscriptionCLIAdmit(t, clearArgs)
	cleared := subscriptionCLIPoll(t, clearOp)
	if cleared.Phase != control.Committed || cleared.SourceUpdate.Sequence != 3 || cleared.SourceUpdate.SnapshotID == nil || *cleared.SourceUpdate.SnapshotID == *success.SourceUpdate.SnapshotID {
		t.Fatal("explicit [] did not change complete policy")
	}
	clearRaw := subscriptionCLIMachine(t, "cli-source-clear", revision, intent, []any{}, true)
	code, clearResponse, _ := cliResponse(t, []string{"api", "v1", "subscription.update"}, strings.NewReader(clearRaw))
	if code != 0 {
		t.Fatal("explicit [] request not shared with machine")
	}
	var receipt struct {
		Asset string `json:"asset_sha256"`
	}
	if json.Unmarshal(clearResponse["data"], &receipt) != nil || receipt.Asset == "" {
		t.Fatal("clear candidate missing")
	}
	status, err := service.LocalStatus(context.Background(), paths)
	if err != nil || status.Revision != revision || status.Generation != nil || status.DesiredRuntime != "stopped" {
		t.Fatal("subscription commands changed runtime", err)
	}
	stop()
	if err := os.Remove(provider); err != nil {
		t.Fatal(err)
	}
	before = cliTree(t, paths)
	code, _, offlineList := cliResponse(t, []string{"subscription", "list"}, strings.NewReader(""))
	if code != 0 {
		t.Fatal("offline list failed")
	}
	subscriptionCLIPrivate(t, offlineList, provider, declaration)
	code, _, offlineFailure := cliResponse(t, failArgs, strings.NewReader(""))
	if code != 4 || offlineFailure != failedHuman {
		t.Fatal("offline known failure was not exact replay")
	}
	code, _, _ = cliResponse(t, []string{"subscription", "update", "source-a", "--input", declaration, "--request-id", "offline-new-update", "--expected-revision", string(revision)}, strings.NewReader(""))
	if code != 4 {
		t.Fatal("offline unknown update did not fail boundedly")
	}
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("offline query/replay/new request started service or wrote")
	}
	store, err := config.OpenStore(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asset, err := store.ReadSourceAsset(receipt.Asset)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := settings.SensitiveIdentityKey()
	snapshot, err := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: "source-a", IdentityKey: key, RegionRules: []subscription.RegionRule{}})
	if err != nil || snapshot.Nodes[0].Region.Kind != "unknown" {
		t.Fatal("explicit empty regions guessed country or retained policy", err)
	}
}
func TestSubscriptionCLIInvalidArgumentsAndFilesStayPrivate(t *testing.T) {
	paths := cliPaths(t)
	valid := subscriptionCLIFile(t, "valid-intent.json", subscriptionCLIJSON(t, subscriptionCLIIntent("/nonexistent/provider.json")))
	before := cliTree(t, paths)
	for _, args := range [][]string{{"subscription"}, {"subscription", "unknown"}, {"subscription", "list", "extra"}, {"subscription", "list", "--unknown"}, {"subscription", "list", "--cursor", ""}, {"subscription", "list", "--cursor", "private-file-body"}, {"subscription", "update"}, {"subscription", "update", "bad source"}, {"subscription", "update", "source-a", "extra"}, {"subscription", "update", "source-a", "--unknown"}, {"subscription", "update", "source-a", "--input", ""}, {"subscription", "update", "source-a", "--regions", ""}, {"subscription", "update", "source-a", "--input", valid, "--expected-revision", "bad revision"}, {"subscription", "update", "source-a", "--input", valid, "--request-id", string([]byte{255})}} {
		code, _, out := cliResponse(t, args, strings.NewReader(""))
		if code != 2 {
			t.Fatal("invalid subscription argv accepted", code)
		}
		subscriptionCLIPrivate(t, out, valid)
		if !reflect.DeepEqual(before, cliTree(t, paths)) {
			t.Fatal("bad argv created store")
		}
	}
	for _, field := range []string{"--input", "--regions"} {
		for _, raw := range []string{`null`, `true`, `"private-file-body"`, `{"private":"private-file-body"}`, "- prefix: 'US '\n  Prefix: private-file-body\n  region: US\n", "!!binary cHJpdmF0ZS1maWxlLWJvZHk=", "---\n[]\n---\n[]\n", "x: &loop [*loop]"} {
			path := subscriptionCLIFile(t, "private-declaration", []byte(raw))
			args := []string{"subscription", "update", "source-a", field, path, "--expected-revision", "revision-1"}
			code, _, out := cliResponse(t, args, strings.NewReader(""))
			if code != 2 {
				t.Fatal("invalid local declaration accepted", field, code)
			}
			subscriptionCLIPrivate(t, out, path)
			if !reflect.DeepEqual(before, cliTree(t, paths)) {
				t.Fatal("bad document created state")
			}
		}
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "missing-private-file"), t.TempDir(), subscriptionCLIFile(t, "invalid-utf8", []byte{255}), subscriptionCLIFile(t, "huge-input", bytes.Repeat([]byte{' '}, control.MaxRequestBytes+1))} {
		code, _, out := cliResponse(t, []string{"subscription", "update", "source-a", "--input", path}, strings.NewReader(""))
		if code != 2 {
			t.Fatal("invalid file accepted", code)
		}
		subscriptionCLIPrivate(t, out, path)
	}
	// A FIFO open must complete without waiting for a writer.
	fifo := filepath.Join(t.TempDir(), "private-fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var output bytes.Buffer
		if code := Execute(context.Background(), []string{"subscription", "update", "source-a", "--input", fifo}, strings.NewReader(""), &output, &bytes.Buffer{}); code != 2 {
			t.Errorf("FIFO accepted: %d", code)
		}
		subscriptionCLIPrivate(t, output.String(), fifo)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO blocked before regular-file validation")
	}
	code, _, out := cliResponse(t, []string{"subscription", "update", "source-missing", "--input", valid}, strings.NewReader(""))
	if code != 2 {
		t.Fatal("missing exact declared source accepted")
	}
	subscriptionCLIPrivate(t, out, valid)
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("invalid file/source changed state")
	}
}
func TestSubscriptionCLIColdQueriesNeverInitializeOrStart(t *testing.T) {
	paths := cliPaths(t)
	input := subscriptionCLIFile(t, "intent.json", subscriptionCLIJSON(t, subscriptionCLIIntent("/nonexistent/provider.json")))
	before := cliTree(t, paths)
	for _, args := range [][]string{{"subscription", "list"}, {"subscription", "update", "source-a", "--input", input, "--request-id", "offline-source", "--expected-revision", "revision-1"}, {"subscription", "update", "source-a", "--input", input, "--request-id", "offline-source-observe"}} {
		code, _, out := cliResponse(t, args, strings.NewReader(""))
		if code != 4 {
			t.Fatal("cold source call did not fail read-only", code)
		}
		subscriptionCLIPrivate(t, out, input)
		if !reflect.DeepEqual(before, cliTree(t, paths)) {
			t.Fatal("source call implicitly initialized or started service")
		}
	}
}
func TestSubscriptionCLILocalDocumentBoundsAndSymlinks(t *testing.T) {
	path := subscriptionCLIFile(t, "exact-json", append([]byte(`[]`), bytes.Repeat([]byte{' '}, control.MaxRequestBytes-2)...))
	value, err := subscriptionDocument(context.Background(), path)
	if err != nil || !reflect.DeepEqual(value, []any{}) {
		t.Fatal("inclusive local byte bound", err)
	}
	link := filepath.Join(t.TempDir(), "ordinary-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = subscriptionDocument(context.Background(), link); err != nil {
		t.Fatal("ordinary regular symlink rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, err := subscriptionDocument(ctx, path); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled local read")
	}
	if err := os.Truncate(path, control.MaxRequestBytes+1); err != nil {
		t.Fatal(err)
	}
	if value, err := subscriptionDocument(context.Background(), path); value != nil || err != control.ErrRequest {
		t.Fatal("exclusive local size bound")
	}
}
