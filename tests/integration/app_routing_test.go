//go:build linux && amd64 && us4integration

package integration

import (
	"encoding/json"
	"errors"
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

	"hoopa/internal/config"
	"hoopa/internal/control"
	owned "hoopa/internal/runtime"
	profile "hoopa/protocol/sourceprofile"
)

// Actual IN-NAME contexts, not original-process identities. localhost overlap
// uses the pinned native config parser's explicit default 127.0.0.1 mapping;
// no host-file mutation, public DNS or additional profile DNS field is used.
func TestAppRoutingOrderedProfilesActualPathsAndInterrupt(t *testing.T) {
	h := newAppHarness(t)
	plan := h.plan()
	if plan.Impact.RuntimeAction != "start" || plan.Impact.PreviousGeneration != nil {
		t.Fatal("first preview is not complete start")
	}
	op := h.apply(plan, true)
	if op.Record.Phase != control.Committed {
		t.Fatal("first multi-entry Apply failed")
	}
	oldID, old := h.run()
	h.assertProfile()
	h.fresh("app-a", "/app-first-a", "A-US")
	h.fresh("app-b", "/app-first-b", "B-JP")
	originalEntryB := h.entries["app-b"]
	wrongCredentials := originalEntryB
	wrongCredentials.Users = append([]profile.User{}, h.entries["app-a"].Users...)
	h.entries["app-b"] = wrongCredentials
	_, credentialErr := h.get("app-b", "/cross-entry-credentials")
	appTerminal(t, credentialErr)
	h.entries["app-b"] = originalEntryB
	h.fresh("", "/shared-has-no-app", "A-PIN")
	defaultBody, defaultErr := h.getHost("", "127.0.0.2", "/explicit-default-node", "A-PIN")
	appMust(t, defaultErr)
	if defaultBody != "A-PIN:/explicit-default-node" {
		t.Fatal("explicit MATCH default node was not used")
	}

	heldA, heldB := h.hold("app-a"), h.hold("app-b")
	appMust(t, heldA.exchange("/held-a-before", "A-US"))
	appMust(t, heldB.exchange("/held-b-before", "B-JP"))
	h.rules("domain-first")
	replacement := h.plan()
	if replacement.Impact.RuntimeAction != "replace" || replacement.Impact.PreviousGeneration == nil || *replacement.Impact.PreviousGeneration != plan.Impact.TargetGeneration {
		t.Fatal("replace preview lost old generation")
	}
	h.assertUnchangedApply(replacement, false)
	h.fresh("app-a", "/unconfirmed-still-a", "A-US")
	h.fresh("app-b", "/unconfirmed-still-b", "B-JP")
	op = h.apply(replacement, true)
	if op.Record.Phase != control.Committed {
		t.Fatal("full replacement failed")
	}
	newID, current := h.run()
	if newID == oldID || current.Identity() == old.Identity() {
		t.Fatal("replacement reused old core")
	}
	h.exit(old)
	appTerminal(t, heldA.exchange("/held-a-interrupted"))
	appTerminal(t, heldB.exchange("/held-b-interrupted"))
	h.fresh("app-a", "/domain-first-a", "A-PIN")
	h.fresh("app-b", "/domain-first-b", "A-PIN")
	h.assertProfile()
	// A node target remains exact source+ID even when active changes to B.
	h.intent["selection"] = map[string]any{"active_subscription": "source-b"}
	pinPlan := h.plan()
	if h.apply(pinPlan, true).Record.Phase != control.Committed {
		t.Fatal("active-source switch")
	}
	h.exit(current)
	_, current = h.run()
	h.fresh("app-a", "/pin-after-source-switch-a", "A-PIN")
	h.fresh("app-b", "/pin-after-source-switch-b", "A-PIN")
	// The exact node is unavailable while the other configured group remains
	// usable. Wait for terminal core refusal before reopening the same endpoint;
	// a client timeout does not establish no outstanding native dial/retry.
	h.proxies[2].close()
	_, unreachableErr := h.get("app-a", "/exact-node-unreachable")
	appTerminal(t, unreachableErr)
	body, groupErr := h.getHost("app-b", "127.0.0.1", "/group-while-node-down", "B-JP")
	appMust(t, groupErr)
	if body != "B-JP:/group-while-node-down" {
		t.Fatal("healthy group silently substituted for unavailable pin")
	}
	h.proxies[2].restart(t)
	h.fresh("app-a", "/same-node-restored", "A-PIN")
	h.rules("ip-first")
	ipPlan := h.plan()
	if h.apply(ipPlan, true).Record.Phase != control.Committed {
		t.Fatal("IP-first Apply")
	}
	h.exit(current)
	_, current = h.run()
	h.fresh("app-a", "/ip-first-a", "B-JP")
	h.fresh("app-b", "/ip-first-b", "B-JP")
	h.fresh("", "/ip-first-shared", "B-JP")
	// Rejection is an explicit ordered rule, not a listener-level target or a
	// missing evidence guess. Every failed token must remain absent at origin.
	h.intent["rules"] = []any{map[string]any{"id": "deny-localhost", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "localhost"}, "target": map[string]any{"kind": "reject"}}}
	denyPlan := h.plan()
	if h.apply(denyPlan, true).Record.Phase != control.Committed {
		t.Fatal("explicit reject Apply")
	}
	h.exit(current)
	h.assertProfile() // Declared managed apps still own entries without app predicates.
	for _, app := range []config.ID{"app-a", "app-b", ""} {
		_, e := h.get(app, "/explicit-reject")
		appTerminal(t, e)
	}
	h.finish()
	t.Log("actual source product Plan/Apply: two unmetered managed entry contexts; ordered app/domain/IP paths and exact node pin; disclosed full replacement interrupts both original held streams; no listener override; explicit refusal has no origin token")
}

func TestAppRoutingMissingInvalidAndStaleInputsZeroEffects(t *testing.T) {
	h := newAppHarness(t)
	if h.apply(h.plan(), true).Record.Phase != control.Committed {
		t.Fatal("initial Apply")
	}
	_, original := h.run()
	baseline := h.cloneIntent()
	// Even an unreferenced declared managed app must have its own binding.
	h.intent["apps"] = append(h.intent["apps"].([]any), map[string]any{"id": "unbound", "mode": "managed", "metering": false, "bypass": []any{}})
	h.rejectPlan()
	h.intent = baseline
	h.fresh("app-a", "/after-missing-binding", "A-US")
	metered := h.cloneIntent()
	metered["apps"].([]any)[0].(map[string]any)["metering"] = true
	h.intent = metered
	h.rejectPlan()
	h.intent = baseline
	// All targets are checked, including the exact default node and a pin on a
	// rule shadowed by earlier app predicates. No first node/default substitution.
	broken := h.cloneIntent()
	target := broken["default_target"].(map[string]any)
	target["node_id"] = "missing"
	h.intent = broken
	h.rejectPlan()
	h.intent = baseline
	broken = h.cloneIntent()
	broken["rules"] = append(broken["rules"].([]any), map[string]any{"id": "shadowed-invalid-pin", "match": map[string]any{"kind": "app", "app_id": "app-a"}, "target": map[string]any{"kind": "node", "subscription_id": "source-b", "node_id": baseline["default_target"].(map[string]any)["node_id"]}})
	h.intent = broken
	h.rejectPlan()
	h.intent = baseline
	broken = h.cloneIntent()
	broken["rules"] = []any{map[string]any{"id": "unsupported-process", "match": map[string]any{"kind": "all", "conditions": []any{map[string]any{"kind": "domain", "operator": "exact", "value": "never.test"}, map[string]any{"kind": "process_name", "value": "unavailable-worker"}}}, "target": map[string]any{"kind": "reject"}}}
	h.intent = broken
	h.rejectPlan()
	h.intent = baseline
	h.rules("domain-first")
	approved := h.plan()
	bindingPath := h.bindingPath("app-a")
	originalBytes, e := os.ReadFile(bindingPath)
	appMust(t, e)
	// Removing private material after Plan is an explicit fixture fault. Apply
	// must re-read authority, reject zero-write, and preserve the old core.
	backup := filepath.Join(h.root, "binding-backup")
	appMust(t, os.Rename(bindingPath, backup))
	h.assertUnchangedApply(approved, true)
	appMust(t, os.Rename(backup, bindingPath))
	// Change one canonical scalar in-place; do not construct a new private schema
	// or ask an initializer to overwrite immutable credentials.
	oldPort := h.entries["app-a"].Port
	_, text, _ := net.SplitHostPort(appAddress(t))
	changed := strings.Replace(string(originalBytes), `"port":`+strconv.Itoa(int(oldPort)), `"port":`+text, 1)
	if changed == string(originalBytes) {
		t.Fatal("binding fixture did not alter port")
	}
	appMust(t, os.WriteFile(bindingPath, []byte(changed), 0600))
	h.assertUnchangedApply(approved, true)
	appMust(t, os.WriteFile(bindingPath, originalBytes, 0600))
	_, after := h.run()
	if after.Identity() != original.Identity() {
		t.Fatal("invalid candidates replaced old core")
	}
	h.fresh("app-a", "/after-stale-binding-a", "A-US")
	h.fresh("app-b", "/after-stale-binding-b", "B-JP")
	// Explicit setup shares writer/revision/uniqueness boundaries. A different
	// orphan binding may be prepared while the old runtime remains live, but it
	// cannot collide with either an endpoint or retained credentials.
	h.stopManager()
	for _, kind := range []string{"endpoint", "credentials"} {
		entry := appEntry(t, "x")
		existing := h.entries["app-a"]
		if kind == "endpoint" {
			entry.Listen, entry.Port = existing.Listen, existing.Port
		} else {
			entry.Users = append([]profile.User{}, existing.Users...)
		}
		before := h.tree()
		if e := h.initialize(config.ID("collision-"+kind), entry); e == nil || !reflect.DeepEqual(before, h.tree()) {
			t.Fatal("colliding explicit binding accepted or wrote", kind, e)
		}
	}
	h.startManager()
	_, same := h.run()
	if same.Identity() != original.Identity() {
		t.Fatal("local setup implicitly restarted core")
	}
	h.intent = baseline
	h.finish()
}

func TestAppRoutingPartialBindRestoresEveryEntryAndForeignListener(t *testing.T) {
	h := newAppHarness(t)
	if h.apply(h.plan(), true).Record.Phase != control.Committed {
		t.Fatal("old Apply")
	}
	oldActive := h.active()
	_, oldHandle := h.run()
	oldIntent := h.cloneIntent()
	heldA, heldB := h.hold("app-a"), h.hold("app-b")
	appMust(t, heldA.exchange("/pre-rollback-a", "A-US"))
	appMust(t, heldB.exchange("/pre-rollback-b", "B-JP"))
	h.stopManager()
	foreign, e := net.Listen("tcp4", "127.0.0.1:0")
	appMust(t, e)
	defer foreign.Close()
	_, text, _ := net.SplitHostPort(foreign.Addr().String())
	port, _ := strconv.Atoi(text)
	foreignServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "foreign-listener-alive") })}
	foreignDone := make(chan error, 1)
	go func() { foreignDone <- foreignServer.Serve(foreign) }()
	defer func() { foreignServer.Close(); <-foreignDone }()
	entry := appEntry(t, "z")
	entry.Port = uint16(port)
	appMust(t, h.initialize("app-z", entry))
	h.entries["app-z"] = entry
	h.startManager()
	h.intent["apps"] = append(h.intent["apps"].([]any), map[string]any{"id": "app-z", "mode": "managed", "metering": false, "bypass": []any{}})
	plan := h.plan()
	if len(plan.Impact.AffectedApps) != 3 {
		t.Fatal("partial-bind preview omitted new app")
	}
	op := h.apply(plan, true)
	if op.Record.Phase != control.RolledBack {
		t.Fatal("partial listener candidate was accepted")
	}
	// The old immutable configuration is restored by a new run belonging to
	// this rollback operation (basic-apply-v1), without a new config revision.
	rollbackOperation := op.Record.Identity.OperationID
	oldActive.OperationID = &rollbackOperation
	if !reflect.DeepEqual(h.active(), oldActive) {
		t.Fatal("failed bind did not restore complete old active pointer")
	}
	h.exit(oldHandle)
	var proof struct {
		TargetRunID   *config.RunID `json:"target_run_id"`
		RollbackRunID *config.RunID `json:"rollback_run_id"`
		OldStopped    bool          `json:"old_stopped"`
		TargetStopped bool          `json:"target_stopped"`
	}
	appMust(t, jsonUnmarshalLastAppEvidence(op, &proof))
	if proof.TargetRunID == nil || proof.RollbackRunID == nil || !proof.OldStopped || !proof.TargetStopped {
		t.Fatal("rollback lacks complete original target/old linkage")
	}
	h.candidateListenerOrder(*proof.TargetRunID)
	h.assertStoppedRun(*proof.TargetRunID)
	rollbackID, rollback := h.run()
	if rollbackID != *proof.RollbackRunID || rollback.Identity() == oldHandle.Identity() {
		t.Fatal("rollback core ownership/identity wrong")
	}
	// This fault happens before closed registration: require the live launch's
	// persisted direct-child Wait witness, then exercise its public recovery.
	var exited struct {
		TargetExit *owned.LaunchExitProof `json:"target_exit"`
	}
	appMust(t, jsonUnmarshalLastAppEvidence(op, &exited))
	if exited.TargetExit == nil {
		t.Fatal("pre-registration candidate lacks direct-child exit proof")
	}
	candidate, intentSHA, e := h.store.ReadRunIntent(*proof.TargetRunID)
	appMust(t, e)
	birth, birthSHA, e := h.store.ReadRunBirth(*proof.TargetRunID)
	appMust(t, e)
	appMust(t, exited.TargetExit.Match(candidate, intentSHA, birth, birthSHA))
	runsBefore, e := h.store.ListRunIDs()
	appMust(t, e)
	h.stopManager()
	h.startManager() // Discard the live LaunchChild before testing stored proof.
	_, code, _ := h.call("operation.recover", map[string]any{"operation_id": rollbackOperation, "expected_revision": oldActive.Revision})
	if code != "" {
		t.Fatal("bound pre-registration rollback recovery refused", code)
	}
	runsAfter, e := h.store.ListRunIDs()
	appMust(t, e)
	recoveredID, recovered := h.run()
	if recoveredID != rollbackID || recovered.Identity() != rollback.Identity() || !reflect.DeepEqual(runsBefore, runsAfter) || !reflect.DeepEqual(h.active(), oldActive) {
		t.Fatal("terminal rollback recovery changed runtime or configuration")
	}
	appTerminal(t, heldA.exchange("/old-stream-a-interrupted"))
	appTerminal(t, heldB.exchange("/old-stream-b-interrupted"))
	h.intent = oldIntent
	h.assertProfile()
	h.fresh("app-a", "/rollback-a", "A-US")
	h.fresh("app-b", "/rollback-b", "B-JP")
	h.fresh("", "/rollback-shared", "A-PIN")
	client := &http.Client{Timeout: 2 * time.Second}
	response, e := client.Get("http://" + foreign.Addr().String())
	appMust(t, e)
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	appMust(t, e)
	if string(body) != "foreign-listener-alive" {
		t.Fatal("foreign listener was killed/replaced")
	}
	h.finish()
	t.Log("real later-listener bind fault: candidate exit, complete old bundle/three old entries restored, held streams interrupted as previewed, independent foreign listener remains alive; no partial success")
}

func TestAppRoutingManagerSIGKILLKeepsHeldAndNewTraffic(t *testing.T) {
	h := newAppHarness(t)
	applied := h.apply(h.plan(), true)
	if applied.Record.Phase != control.Committed {
		t.Fatal("initial Apply")
	}
	run, handle := h.run()
	identity, base := handle.Identity(), h.active()
	runs, e := h.store.ListRunIDs()
	appMust(t, e)
	heldA, heldB := h.hold("app-a"), h.hold("app-b")
	appMust(t, heldA.exchange("/before-kill-a", "A-US"))
	appMust(t, heldB.exchange("/before-kill-b", "B-JP"))
	// This is the direct child running the real management service. It is never
	// the app-context label, inferred source PID, core PID, or a foreign process.
	appMust(t, h.manager.cmd.Process.Signal(syscall.SIGKILL))
	h.waitManager(true)
	h.orphan[identity.PID] = true
	appMust(t, heldA.exchange("/manager-dead-held-a", "A-US"))
	appMust(t, heldB.exchange("/manager-dead-held-b", "B-JP"))
	h.fresh("app-a", "/manager-dead-new-a", "A-US")
	h.fresh("app-b", "/manager-dead-new-b", "B-JP")
	if !reflect.DeepEqual(h.active(), base) {
		t.Fatal("manager death changed runtime authority")
	}
	h.startManager()
	sameRun, sameHandle := h.run()
	afterRuns, e := h.store.ListRunIDs()
	appMust(t, e)
	if sameRun != run || sameHandle.Identity() != identity || !reflect.DeepEqual(runs, afterRuns) {
		t.Fatal("manager reopen duplicated/replaced core")
	}
	_, code, _ := h.call("operation.recover", map[string]any{"operation_id": applied.Record.Identity.OperationID, "expected_revision": h.active().Revision})
	if code != "" {
		t.Fatal("terminal explicit recovery refused", code)
	}
	afterRuns, e = h.store.ListRunIDs()
	appMust(t, e)
	if !reflect.DeepEqual(runs, afterRuns) || !reflect.DeepEqual(h.active(), base) {
		t.Fatal("terminal recovery duplicated core or revised intent")
	}
	appMust(t, heldA.exchange("/reopen-held-a", "A-US"))
	appMust(t, heldB.exchange("/reopen-held-b", "B-JP"))
	h.fresh("app-a", "/reopen-new-a", "A-US")
	h.fresh("app-b", "/reopen-new-b", "B-JP")
	h.finish()
	t.Log("actual manager SIGKILL; two separately authenticated app contexts retain original held and fresh routes while manager is dead; real reopen/recovery retains exact original core identity/run inventory")
}

func jsonUnmarshalLastAppEvidence(op control.StoredOperation, value any) error {
	if len(op.Record.Steps) == 0 {
		return errors.New("missing operation evidence")
	}
	return json.Unmarshal(op.Record.Steps[len(op.Record.Steps)-1].Evidence, value)
}
