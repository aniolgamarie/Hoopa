//go:build linux && amd64 && us4integration && onboardingintegration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/policy"
)

func TestOnboardingMissingInstanceIsReadOnly(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hoopa-onboarding-missing-")
	appMust(t, err)
	// Reuse only final resource/evidence cleanup; no fixture initialization occurs.
	t.Cleanup((&appHarness{t: t, root: root}).cleanup)
	home := filepath.Join(root, "home")
	paths, e := config.ResolvePaths(home, map[string]string{"XDG_CONFIG_HOME": filepath.Join(root, "config"), "XDG_STATE_HOME": filepath.Join(root, "state"), "XDG_CACHE_HOME": filepath.Join(root, "cache"), "XDG_RUNTIME_DIR": filepath.Join(root, "run")})
	appMust(t, e)
	cli := newOnboardingCLI(t, root, home, paths)
	// A missing reader cannot establish that this is a pristine installation.
	onboardingDoctor(t, cli.readonly(nil, "doctor"), "missing")
	request := appRequest(t, "missing-doctor", "doctor.get", map[string]any{})
	onboardingDoctor(t, cli.readonly(request, "api", "v1", "doctor.get"), "missing")
	status := cli.readonly(nil, "status")
	if status.OK {
		t.Fatal("missing instance status became empty success")
	}
}
func onboardingWriteIntent(t *testing.T, h *appHarness, name string) string {
	t.Helper()
	raw := appJSON(t, h.intent)
	if !strings.HasSuffix(name, ".json") {
		var value any
		appMust(t, json.Unmarshal(raw, &value))
		var err error
		raw, err = yaml.Marshal(value)
		appMust(t, err)
	}
	path := filepath.Join(h.root, name)
	appMust(t, os.WriteFile(path, raw, 0600))
	return path
}

func onboardingCLIPlan(t *testing.T, r *onboardingHarness, input, output string) control.PlanDocument {
	t.Helper()
	reply := r.cli.readonly(nil, "config", "plan", "--input", input, "--output", output)
	return onboardingPlan(t, reply, output)
}
func onboardingCLIApply(t *testing.T, r *onboardingHarness, path string, doc control.PlanDocument, id string) onboardingReply {
	t.Helper()
	return r.cli.invoke(nil, "config", "apply", "--plan", path, "--confirm", doc.PlanDigest, "--accept-disruption", "--request-id", id)
}
func onboardingReject(t *testing.T, r *onboardingHarness, wantCode string, wantExit int, args ...string) {
	t.Helper()
	reply := r.cli.readonly(nil, args...)
	if reply.OK || reply.Error == nil || reply.Error.Code != wantCode || reply.code != wantExit {
		t.Fatalf("CLI rejection mismatch: got exit=%d error=%v; want %s/%d", reply.code, reply.Error, wantCode, wantExit)
	}
}

func TestOnboardingLocalDraftCommittedAndExactReplayCLI(t *testing.T) {
	r := newOnboardingHarness(t)
	h := r.h
	// These materials came from explicit control primitives and a fixture file
	// provider. This journey does not claim the unimplemented hoopa init command.
	draft := onboardingWriteIntent(t, h, "explicit-draft.yaml")
	output := filepath.Join(h.root, "initial.plan.json")
	initial := h.active()
	if initial.Generation != nil {
		t.Fatal("prerequisite fixture unexpectedly applied")
	}
	r.queryStage("draft") // No service running; no creation/repair by either query.
	plan := onboardingCLIPlan(t, r, draft, output)
	if plan.Impact.RuntimeAction != "start" || !plan.Impact.RequiresDisruption || plan.Impact.ConnectionEffect != "interrupt" {
		t.Fatal("initial CLI preview missing complete interruption semantics")
	}
	if !reflect.DeepEqual(initial, h.active()) {
		t.Fatal("local output Plan published active")
	}

	// The actual service must still be absent; Plan never bootstraps it.
	if _, e := os.Lstat(filepath.Join(h.paths.Runtime, "service.json")); !os.IsNotExist(e) {
		t.Fatal("local Plan implicitly started management")
	}
	originalPlan, e := os.ReadFile(output)
	appMust(t, e)
	// Every named output is outside internal state; only this explicit new file
	// may appear. No overwrite, link following, FIFO blocking or parent mkdir.
	sentinel := filepath.Join(h.root, "sentinel")
	appMust(t, os.WriteFile(sentinel, []byte("caller-owned"), 0640))
	link := filepath.Join(h.root, "plan-link")
	appMust(t, os.Symlink(sentinel, link))
	fifo := filepath.Join(h.root, "plan-fifo")
	appMust(t, syscall.Mkfifo(fifo, 0600))
	for _, path := range []string{output, sentinel, link, fifo} {
		onboardingReject(t, r, "CONFLICT", 3, "config", "plan", "--input", draft, "--output", path)
	}
	onboardingReject(t, r, "INVALID_ARGUMENT", 2, "config", "plan", "--input", draft, "--output", filepath.Join(h.root, "missing-output-parent", "plan.json"))
	unchanged, e := os.ReadFile(output)
	appMust(t, e)
	if !bytes.Equal(unchanged, originalPlan) {
		t.Fatal("existing Plan overwritten")
	}
	unchanged, e = os.ReadFile(sentinel)
	appMust(t, e)
	if string(unchanged) != "caller-owned" {
		t.Fatal("output link/overwrite changed target")
	}
	if _, e = os.Lstat(filepath.Join(h.root, "missing-output-parent")); !os.IsNotExist(e) {
		t.Fatal("output created implicit parent")
	}
	r.start()
	r.queryStage("draft")
	onboardingReject(t, r, "INVALID_ARGUMENT", 2, "config", "apply", "--plan", output, "--accept-disruption", "--request-id", "no-confirm")
	onboardingReject(t, r, "AUTHORIZATION_DENIED", 6, "config", "apply", "--plan", output, "--confirm", plan.PlanDigest, "--request-id", "no-disruption")
	// Both unsigned tampering and a structurally rehashed but semantically forged
	// target must reject without workdir/run/operation/active changes.
	var changed map[string]any
	appMust(t, json.Unmarshal(originalPlan, &changed))
	changed["impact"].(map[string]any)["target_generation"] = "forged-generation"
	tampered := filepath.Join(h.root, "tampered.plan.json")
	appMust(t, os.WriteFile(tampered, appJSON(t, changed), 0600))
	onboardingReject(t, r, "INVALID_ARGUMENT", 2, "config", "apply", "--plan", tampered, "--confirm", plan.PlanDigest, "--accept-disruption", "--request-id", "tampered-digest")
	changedRaw := appJSON(t, changed)
	digest, e := control.ComputePlanDigest(changedRaw)
	appMust(t, e)
	changed["plan_digest"] = digest
	appMust(t, os.WriteFile(tampered, appJSON(t, changed), 0600))
	onboardingReject(t, r, "CONFLICT", 3, "config", "apply", "--plan", tampered, "--confirm", digest, "--accept-disruption", "--request-id", "tampered-rehashed")
	binding := h.bindingPath("app-a")
	backup := filepath.Join(h.root, "binding-backup")
	appMust(t, os.Rename(binding, backup))
	onboardingReject(t, r, "READ_UNAVAILABLE", 4, "config", "apply", "--plan", output, "--confirm", plan.PlanDigest, "--accept-disruption", "--request-id", "stale-binding")
	appMust(t, os.Rename(backup, binding))
	applied := onboardingCLIApply(t, r, output, plan, "accepted-initial")
	if !applied.OK {
		t.Fatal("initial actual CLI Apply failed", applied.Error.Code)
	}
	operationID := onboardingOperationID(t, applied)
	op := r.cli.readonly(appRequest(t, "observe-accepted", "operation.get", map[string]any{"operation_id": operationID}), "api", "v1", "operation.get")
	if !op.OK {
		t.Fatal("operation unavailable")
	}
	var view struct {
		Phase control.Phase `json:"phase"`
	}
	appMust(t, json.Unmarshal(op.Data, &view))
	if view.Phase != control.Committed {
		t.Fatal("CLI mutation did not commit")
	}
	originalRun, originalCore := h.run()
	originalRuns, e := h.store.ListRunIDs()
	appMust(t, e)
	h.fresh("app-a", "/cli-committed-a", "A-US")
	h.fresh("app-b", "/cli-committed-b", "B-JP")
	r.queryStage("running")
	// Read-only request IDs are correlation, even if they name an earlier write.
	onboardingDoctor(t, r.cli.readonly(appRequest(t, "accepted-initial", "doctor.get", map[string]any{}), "api", "v1", "doctor.get"), "running")
	beforeRestart := h.active()
	r.stopService()
	// Exact replay must not depend on current binding or executable availability,
	// and must not spawn management or recompute a new base revision.
	appMust(t, os.Rename(binding, backup))
	coreBackup := filepath.Join(h.root, "core-backup")
	appMust(t, os.Rename(h.core, coreBackup))
	replay := r.cli.readonly(nil, "config", "apply", "--plan", output, "--confirm", plan.PlanDigest, "--accept-disruption", "--request-id", "accepted-initial")
	if !replay.OK || !bytes.Equal(replay.raw, applied.raw) {
		t.Fatal("offline exact replay changed accepted result")
	}
	if _, e = os.Lstat(filepath.Join(h.paths.Runtime, "service.json")); !os.IsNotExist(e) {
		t.Fatal("replay started management")
	}
	appMust(t, os.Rename(backup, binding))
	appMust(t, os.Rename(coreBackup, h.core))
	if !reflect.DeepEqual(beforeRestart, h.active()) {
		t.Fatal("replay changed active/revision")
	}
	r.start()
	currentRun, currentCore := h.run()
	currentRuns, e := h.store.ListRunIDs()
	appMust(t, e)
	if currentRun != originalRun || currentCore.Identity() != originalCore.Identity() || !reflect.DeepEqual(currentRuns, originalRuns) {
		t.Fatal("offline replay/control-only restart duplicated or replaced core")
	}
	r.queryStage("running")
	h.fresh("app-a", "/cli-after-replay-a", "A-US")
	h.fresh("app-b", "/cli-after-replay-b", "B-JP")
	r.finish()
}

func TestOnboardingFailedApplyRollsBackCompleteCLIEntry(t *testing.T) {
	r := newOnboardingHarness(t)
	h := r.h
	input := onboardingWriteIntent(t, h, "first.intent.yaml")
	path := filepath.Join(h.root, "first.plan.json")
	plan := onboardingCLIPlan(t, r, input, path)
	r.start()
	initial := onboardingCLIApply(t, r, path, plan, "rollback-initial")
	if !initial.OK {
		t.Fatal("initial Apply unavailable")
	}
	oldActive := h.active()
	oldIntent := h.cloneIntent()
	_, old := h.run()
	heldA, heldB := h.hold("app-a"), h.hold("app-b")
	appMust(t, heldA.exchange("/cli-held-before-a", "A-US"))
	appMust(t, heldB.exchange("/cli-held-before-b", "B-JP"))
	r.stopService() // The public service stop leaves the already applied core live.
	foreign, e := net.Listen("tcp4", "127.0.0.1:0")
	appMust(t, e)
	defer foreign.Close()
	_, text, _ := net.SplitHostPort(foreign.Addr().String())
	port, _ := strconv.Atoi(text)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "independent-foreign-listener") })}
	done := make(chan error, 1)
	go func() { done <- server.Serve(foreign) }()
	defer func() { server.Close(); <-done }()
	entry := appEntry(t, "z")
	entry.Port = uint16(port)
	appMust(t, h.initialize("app-z", entry))
	entry.Name = policy.AppEntryAlias("app-z")
	h.entries["app-z"] = entry
	h.intent["apps"] = append(h.intent["apps"].([]any), map[string]any{"id": "app-z", "mode": "managed", "metering": false, "bypass": []any{}})
	nextInput := onboardingWriteIntent(t, h, "failing.intent.json")
	nextPath := filepath.Join(h.root, "failing.plan.json")
	replacement := onboardingCLIPlan(t, r, nextInput, nextPath)
	if replacement.Impact.RuntimeAction != "replace" || len(replacement.Impact.AffectedApps) != 3 || replacement.Impact.PreviousGeneration == nil || *replacement.Impact.PreviousGeneration != string(*oldActive.Generation) {
		t.Fatal("incomplete failure preview")
	}
	r.start()
	failed := onboardingCLIApply(t, r, nextPath, replacement, "partial-listener-failure")
	if failed.OK || failed.code != 5 || failed.Error.Code != "APPLY_FAILED" {
		t.Fatal("partial startup not a complete verified rollback", failed.code)
	}
	operationID := onboardingOperationID(t, failed)
	observed := r.cli.ok(appRequest(t, "inspect-rollback", "operation.get", map[string]any{"operation_id": operationID}), "api", "v1", "operation.get")
	var view struct {
		Phase control.Phase `json:"phase"`
	}
	appMust(t, json.Unmarshal(observed.Data, &view))
	if view.Phase != control.RolledBack {
		t.Fatal("rollback receipt not terminal/verified")
	}
	current := h.active()
	if current.Generation == nil || *current.Generation != *oldActive.Generation || current.Revision != oldActive.Revision || current.OperationID == nil || string(*current.OperationID) != operationID {
		t.Fatal("rollback did not preserve old configuration identity/current recovery operation")
	}
	h.exit(old)
	restoredID, restored := h.run()
	if restored.Identity() == old.Identity() {
		t.Fatal("failed full replace reused interrupted core")
	}
	appTerminal(t, heldA.exchange("/cli-held-interrupted-a"))
	appTerminal(t, heldB.exchange("/cli-held-interrupted-b"))
	h.intent = oldIntent
	h.assertProfile()
	h.fresh("app-a", "/cli-rollback-a", "A-US")
	h.fresh("app-b", "/cli-rollback-b", "B-JP")
	h.fresh("", "/cli-rollback-shared", "A-PIN")
	r.queryStage("running")
	client := &http.Client{Timeout: 3 * time.Second}
	response, e := client.Get("http://" + foreign.Addr().String())
	appMust(t, e)
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	appMust(t, e)
	if string(body) != "independent-foreign-listener" {
		t.Fatal("foreign listener not preserved")
	}
	r.stopService()
	r.start()
	sameID, same := h.run()
	if sameID != restoredID || same.Identity() != restored.Identity() {
		t.Fatal("CLI manager reopen lost restored core")
	}
	r.queryStage("running")
	r.finish()
}
