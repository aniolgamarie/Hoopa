//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func apiRequest(t *testing.T, id, version, operation string, params any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"api_version": version, "request_id": id, "operation": operation, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func apiResponse(t *testing.T, raw []byte) (map[string]json.RawMessage, string) {
	t.Helper()
	if _, err := responseID(raw); err != nil {
		t.Fatal("invalid response envelope", err, string(raw))
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	code := ""
	if string(response["error"]) != "null" {
		var failure struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(response["error"], &failure) != nil {
			t.Fatal("invalid error")
		}
		code = failure.Code
	}
	return response, code
}
func apiTree(t *testing.T, paths config.Paths) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range []string{paths.Config, paths.State, paths.Cache, paths.Runtime} {
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			out[root] = "absent"
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			if info.Mode().IsRegular() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(raw)
				value += ":" + hex.EncodeToString(sum[:])
			}
			out[path] = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
func apiPaths(t *testing.T) config.Paths {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "hoopa-service-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	paths, err := config.ResolvePaths(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

type apiSeed func(*config.Store, *control.Journal, config.ActivePointer)

func apiServer(t *testing.T, seed apiSeed) (*server, config.Paths) {
	t.Helper()
	paths := apiPaths(t)
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := control.NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	if seed != nil {
		seed(store, journal, base)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	c, err := control.OpenCoordinator(paths, unavailableIntent, unavailableInputs)
	if err != nil {
		t.Fatal(err)
	}
	applier, err := control.NewBasicApplier(c, unavailableMaterial, []string{"never-launch-core-from-api-test"})
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	endpoint, err := OpenEndpoint(c)
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &server{coordinator: c, applier: applier, endpoint: endpoint, background: ctx, cancel: cancel, restartState: "disabled"}
	t.Cleanup(func() {
		cancel()
		if err := endpoint.Close(); err != nil {
			t.Error(err)
		}
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, paths
}

// Only Journal's storage codec is exercised by this synthetic record helper.
// These records do not establish runtime ownership, recovery or launch proof.
func apiSyntheticPending(t *testing.T, journal *control.Journal, base config.ActivePointer, id, operation string, params any, degraded bool, longReferences bool) control.StoredOperation {
	t.Helper()
	raw := apiRequest(t, id, "v1", operation, params)
	request, err := control.ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	intent := control.Intent{BaseRevision: base.Revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}
	if longReferences {
		intent.BaseRevision = config.Revision(strings.Repeat("r", 128))
		target := config.Revision(strings.Repeat("t", 128))
		intent.TargetRevision = &target
		old, next := config.Generation(strings.Repeat("o", 128)), config.Generation(strings.Repeat("g", 128))
		intent.PreviousGeneration = &old
		intent.TargetGeneration = &next
	}
	pending, made, err := journal.Admit(request, intent, json.RawMessage(`{"outcome":"pending"}`))
	if err != nil || !made {
		t.Fatal(made, err)
	}
	phase := control.Validated
	if degraded {
		phase = control.Degraded
	}
	// Projection must expose neither evidence nor arbitrary stored error text.
	pending, err = journal.Advance(request.OperationID(), pending.SHA256, phase, json.RawMessage(`{"controller_secret":"private-evidence-secret","source":"private-source-token"}`), json.RawMessage(`{"error":{"code":"PRIVATE_SECRET_CODE","message":"private-result-secret"}}`))
	if err != nil {
		t.Fatal(err)
	}
	return pending
}
func TestAPIFailureRedactionAndContractExitCodes(t *testing.T) {
	raw := apiRequest(t, "safe-correlation-id", "v1", "status.get", map[string]any{"private": "request-body-secret"})
	for _, test := range []struct {
		name  string
		cause error
		code  string
		exit  int
	}{
		{"argument", control.ErrRequest, "INVALID_ARGUMENT", 2}, {"version", ErrUnsupportedVersion, "UNSUPPORTED_VERSION", 2}, {"unsupported", control.ErrCapabilityUnsupported, "CAPABILITY_UNSUPPORTED", 2}, {"confirmation", control.ErrConfirmation, "AUTHORIZATION_DENIED", 6}, {"id-reuse", control.ErrRequestIDReused, "REQUEST_ID_REUSED", 3}, {"revision", control.ErrConflict, "CONFLICT", 3}, {"writer", config.ErrWriterBusy, "CONFLICT", 3}, {"pending", control.ErrRecoveryRequired, "RECOVERY_REQUIRED", 5}, {"uncertain", config.ErrUncertain, "RECOVERY_REQUIRED", 5}, {"capacity", config.ErrOperationCapacity, "OPERATION_CAPACITY_EXHAUSTED", 4}, {"snapshot", ErrNoSnapshot, "DEPENDENCY_UNAVAILABLE", 4}, {"discovery", ErrDiscovery, "INTEGRITY_ERROR", 5}, {"unsafe-path", config.ErrUnsafePath, "INTEGRITY_ERROR", 5}, {"missing-operation", control.ErrOperationNotFound, "OPERATION_NOT_FOUND", 4}, {"offline", ErrServiceUnavailable, "NOT_RUNNING", 4}, {"generic", errors.New("untrusted file path and private-error-secret"), "READ_UNAVAILABLE", 4}, {"cancel-priority", errors.Join(ErrDiscovery, context.Canceled), "CANCELLED", 7}, {"deadline-priority", errors.Join(config.ErrWriterBusy, context.DeadlineExceeded), "CANCELLED", 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := Failure(raw, fmt.Errorf("private-error-secret: %w", test.cause))
			envelope, code := apiResponse(t, response)
			if code != test.code || ExitCode(response) != test.exit || string(envelope["request_id"]) != `"safe-correlation-id"` || bytes.Contains(response, []byte("secret")) || string(envelope["data"]) != "null" || string(envelope["ok"]) != "false" {
				t.Fatal("failure mapping/redaction", code, ExitCode(response))
			}
			var detail map[string]json.RawMessage
			json.Unmarshal(envelope["error"], &detail)
			if len(detail) != 3 || string(detail["message"]) != fmt.Sprintf("%q", test.code) || string(detail["retryable"]) != "false" {
				t.Fatal("failure disclosed uncontrolled details")
			}
		})
	}
	for _, bad := range [][]byte{nil, []byte(`{"request_id":"untrusted","request_id":"other"}`), append([]byte(nil), bytes.Repeat([]byte{'x'}, control.MaxRequestBytes+1)...)} {
		response := Failure(bad, errors.New("private-error-secret"))
		envelope, _ := apiResponse(t, response)
		if string(envelope["request_id"]) != "null" || bytes.Contains(response, []byte("secret")) {
			t.Fatal("invalid request identity trusted")
		}
	}
}
func TestAPIExitCodeRejectsInvalidSuccessAndMapsValidEnvelope(t *testing.T) {
	request, _ := control.ParseRequest(apiRequest(t, "code-id", "v1", "status.get", map[string]any{}))
	if ExitCode(success(request, map[string]any{"state": "unknown"})) != 0 {
		t.Fatal("success exit")
	}
	for _, code := range []string{"SNAPSHOT_EXPIRED", "APPLY_FAILED", "LEDGER_UNAVAILABLE", "OPERATION_NOT_FOUND", "OPERATION_CAPACITY_EXHAUSTED", "AUTHORIZATION_DENIED", "CANCELLED"} {
		raw := []byte(fmt.Sprintf(`{"api_version":"v1","request_id":"code-id","ok":false,"data":null,"error":{"code":%q,"message":"fixed","retryable":false},"warnings":[]}`, code))
		want := 5
		switch code {
		case "SNAPSHOT_EXPIRED", "OPERATION_NOT_FOUND", "OPERATION_CAPACITY_EXHAUSTED":
			want = 4
		case "AUTHORIZATION_DENIED":
			want = 6
		case "CANCELLED":
			want = 7
		}
		if ExitCode(raw) != want {
			t.Fatal(code, ExitCode(raw), want)
		}
	}
	for _, raw := range [][]byte{nil, []byte(`{"ok":true}`), []byte(`{"api_version":"v1","request_id":null,"ok":true,"data":{},"error":null,"warnings":[]}`), []byte(`{"api_version":"v1","request_id":"id","ok":true,"data":[],"error":null,"warnings":[]}`), []byte(`{"api_version":"v1","request_id":"id","ok":false,"data":null,"error":{"code":"UNKNOWN","message":"fixed","retryable":false},"warnings":[]}`), bytes.Repeat([]byte{' '}, control.MaxResultBytes+1)} {
		if ExitCode(raw) != 5 {
			t.Fatal("invalid envelope produced a success/misleading exit")
		}
	}
}
func TestAPIStrictDispatchSchemasDoNotWrite(t *testing.T) {
	s, paths := apiServer(t, nil)
	status, err := s.coordinator.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revision := string(status.Revision)
	for _, test := range []struct {
		name, operation string
		params          map[string]any
	}{
		{"status", "status.get", map[string]any{}}, {"doctor", "doctor.get", map[string]any{}}, {"get", "operation.get", map[string]any{"operation_id": "op-target"}}, {"start", "runtime.start", map[string]any{"expected_revision": revision, "control_only": true}}, {"stop", "runtime.stop", map[string]any{"expected_revision": revision, "accept_disruption": true}}, {"service-stop", "service.stop", map[string]any{"expected_revision": revision}}, {"recover", "operation.recover", map[string]any{"operation_id": "op-target", "expected_revision": revision}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, mutation := range []string{"extra", "missing", "null", "wrong-type", "path-token"} {
				params := map[string]any{}
				for key, value := range test.params {
					params[key] = value
				}
				key := ""
				var keys []string
				for candidate := range params {
					keys = append(keys, candidate)
				}
				sort.Strings(keys)
				if len(keys) > 0 {
					key = keys[0]
				}
				switch mutation {
				case "extra":
					params["credential"] = "private-schema-secret"
				case "missing":
					if key == "" {
						continue
					}
					delete(params, key)
				case "null":
					if key == "" {
						continue
					}
					params[key] = nil
				case "wrong-type":
					if key == "" {
						continue
					}
					switch params[key].(type) {
					case bool:
						params[key] = "true"
					default:
						params[key] = false
					}
				case "path-token":
					key = ""
					for _, candidate := range keys {
						if _, ok := params[candidate].(string); ok {
							key = candidate
							break
						}
					}
					if key == "" {
						continue
					}
					params[key] = "../private-schema-secret"
				}
				before := apiTree(t, paths)
				raw := apiRequest(t, "schema-"+test.name+"-"+mutation, "v1", test.operation, params)
				response := s.dispatch(context.Background(), raw)
				_, code := apiResponse(t, response)
				if code != "INVALID_ARGUMENT" || bytes.Contains(response, []byte("private-schema-secret")) || !reflect.DeepEqual(before, apiTree(t, paths)) {
					t.Fatal("schema rejection wrote/disclosed state", mutation, code)
				}
			}
		})
	}
	for _, test := range []struct{ version, operation, code string }{{"v2", "status.get", "UNSUPPORTED_VERSION"}, {"v1", "usage.query", "CAPABILITY_UNSUPPORTED"}, {"v1", "config.apply", "INVALID_ARGUMENT"}, {"v1", "connections.query", "CAPABILITY_UNSUPPORTED"}} {
		before := apiTree(t, paths)
		response := s.dispatch(context.Background(), apiRequest(t, "unsupported-"+test.operation, test.version, test.operation, map[string]any{}))
		_, code := apiResponse(t, response)
		if code != test.code || !reflect.DeepEqual(before, apiTree(t, paths)) {
			t.Fatal(test, code)
		}
	}
}
func TestAPIMalformedRequestCannotInitializeWriter(t *testing.T) {
	paths := apiPaths(t)
	before := apiTree(t, paths)
	for _, raw := range [][]byte{nil, []byte(`{"api_version":"v1","request_id":"id","request_id":"changed","operation":"status.get","params":{}}`), []byte(`{"api_version":"v1","request_id":"id","operation":"status.get","params":{}} {}`), []byte(`{"api_version":"v1","request_id":"\ud800","operation":"status.get","params":{}}`), []byte(`{"api_version":"v1","request_id":"id","operation":"status.get","params":null}`)} {
		response, err := Call(context.Background(), paths, raw)
		if !errors.Is(err, control.ErrRequest) || response != nil || !reflect.DeepEqual(before, apiTree(t, paths)) {
			t.Fatal("malformed input initialized or admitted", err)
		}
	}
}
func TestAPIOfflineQueryAndExactReplayNeverWrite(t *testing.T) {
	s, paths := apiServer(t, nil)
	status, err := s.coordinator.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw := apiRequest(t, "accepted-service-stop", "v1", "service.stop", map[string]any{"expected_revision": status.Revision})
	stored, made, err := s.coordinator.StopService(context.Background(), raw, func() error { return s.endpoint.Close() })
	if err != nil || !made || stored.Record.Phase != control.Committed {
		t.Fatal(made, err)
	}
	// Keep the actual exclusive Coordinator writer held while its endpoint is
	// offline: fallback must use a separate readonly Store, not take a writer.
	before := apiTree(t, paths)
	for _, operation := range []string{"status.get", "operation.get"} {
		params := map[string]any{}
		if operation == "operation.get" {
			params["operation_id"] = stored.Record.Identity.OperationID
		}
		request := apiRequest(t, "offline-"+operation, "v1", operation, params)
		response, err := Call(context.Background(), paths, request)
		if err != nil {
			t.Fatal(err)
		}
		envelope, code := apiResponse(t, response)
		if code != "" || string(envelope["request_id"]) != fmt.Sprintf("%q", "offline-"+operation) || !reflect.DeepEqual(before, apiTree(t, paths)) {
			t.Fatal("offline query wrote or lost identity", code)
		}
	}
	for _, replay := range [][]byte{raw, []byte(fmt.Sprintf(" {\n\"params\":{\"expected_revision\":%q},\"operation\":\"service.stop\",\"request_id\":\"accepted-service-stop\",\"api_version\":\"v1\"} ", status.Revision))} {
		response, err := Call(context.Background(), paths, replay)
		if err != nil || !bytes.Equal(response, stored.Record.Result) || !reflect.DeepEqual(before, apiTree(t, paths)) {
			t.Fatal("offline replay rewrote receipt or files", err)
		}
	}
	changed := apiRequest(t, "accepted-service-stop", "v1", "service.stop", map[string]any{"expected_revision": "stale-revision"})
	if _, err := Call(context.Background(), paths, changed); !errors.Is(err, control.ErrRequestIDReused) {
		t.Fatal("offline identity conflict hidden", err)
	}
	unknown := apiRequest(t, "unknown-offline-stop", "v1", "service.stop", map[string]any{"expected_revision": status.Revision})
	if _, err := Call(context.Background(), paths, unknown); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal("unknown offline mutation did not preserve unavailability", err)
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("offline rejected mutation changed state")
	}
	// An unsafe endpoint must not be bypassed even to replay a known receipt.
	if err := os.WriteFile(filepath.Join(paths.Runtime, discoveryName), []byte(`{"credential":"private-discovery-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before = apiTree(t, paths)
	if response, err := Call(context.Background(), paths, raw); response != nil || !errors.Is(err, ErrDiscovery) || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("discovery integrity bypassed for offline replay", err)
	}
}
func TestAPIDispatchPendingCoreRemainsConservativeAndRedacted(t *testing.T) {
	var pending control.StoredOperation
	s, paths := apiServer(t, func(_ *config.Store, journal *control.Journal, base config.ActivePointer) {
		pending = apiSyntheticPending(t, journal, base, "pending-core-private-request", "runtime.start", map[string]any{"expected_revision": base.Revision, "control_only": false}, true, false)
	})
	before := apiTree(t, paths)
	raw := apiRequest(t, "observe-pending-core", "v1", "status.get", map[string]any{})
	response := s.dispatch(context.Background(), raw)
	envelope, code := apiResponse(t, response)
	var status control.ServiceStatus
	if json.Unmarshal(envelope["data"], &status) != nil || code != "" {
		t.Fatal(code)
	}
	if status.Core.State != "unknown" || !status.Degraded || status.DesiredRuntime != "running" || status.CommittedRuntime != "stopped" || status.PendingTotal != 1 || status.PendingTruncated || len(status.Pending) != 1 || status.Pending[0].Phase != control.Degraded || !status.Pending[0].Recovery.Required {
		t.Fatal("pending core fabricated stopped/running proof", status.Core.State, status.DesiredRuntime)
	}
	for _, operation := range []string{"runtime.start", "runtime.stop"} {
		params := map[string]any{"expected_revision": status.Revision}
		if operation == "runtime.start" {
			params["control_only"] = false
		} else {
			params["accept_disruption"] = true
		}
		_, code := apiResponse(t, s.dispatch(context.Background(), apiRequest(t, "blocked-"+operation, "v1", operation, params)))
		if code != "RECOVERY_REQUIRED" {
			t.Fatal("pending core mutation allowed", operation, code)
		}
	}
	projected := s.dispatch(context.Background(), apiRequest(t, "project-operation", "v1", "operation.get", map[string]any{"operation_id": pending.Record.Identity.OperationID}))
	viewEnvelope, code := apiResponse(t, projected)
	var view control.OperationView
	if json.Unmarshal(viewEnvelope["data"], &view) != nil || code != "" || view.Recovery.RecordSHA256 != pending.SHA256 || view.Recovery.StepCount != 2 || view.ErrorCode != nil || view.Outcome != "degraded" {
		t.Fatal("operation projection lost conservative evidence", code)
	}
	for _, out := range [][]byte{response, projected} {
		for _, private := range []string{"private-evidence-secret", "private-source-token", "private-result-secret", "PRIVATE_SECRET_CODE", "pending-core-private-request"} {
			if bytes.Contains(out, []byte(private)) {
				t.Fatal("query exposed private retained data")
			}
		}
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("pending observation/rejection changed durable state")
	}
}
func TestAPIStatusPendingProjectionFitsResponseFrame(t *testing.T) {
	const total = 70
	s, paths := apiServer(t, func(_ *config.Store, journal *control.Journal, base config.ActivePointer) {
		for i := 0; i < total; i++ {
			id := fmt.Sprintf("synthetic-pending-%04d-", i) + strings.Repeat("x", 100)
			apiSyntheticPending(t, journal, base, id, "operation.recover", map[string]any{"operation_id": strings.Repeat("q", 128), "expected_revision": base.Revision}, false, true)
		}
	})
	before := apiTree(t, paths)
	raw := apiRequest(t, "bounded-status", "v1", "status.get", map[string]any{})
	response := s.dispatch(context.Background(), raw)
	envelope, code := apiResponse(t, response)
	var status control.ServiceStatus
	if json.Unmarshal(envelope["data"], &status) != nil || code != "" {
		t.Fatal(code)
	}
	if len(response) > control.MaxResultBytes || len(status.Pending) != 32 || status.PendingTotal != total || !status.PendingTruncated || status.Core.State != "unknown" || !status.Degraded {
		t.Fatal("pending projection exceeded/lost bounds", len(response), len(status.Pending), status.PendingTotal, status.PendingTruncated, status.Core.State)
	}
	// The exact generated status must be transmittable by the real frame codec,
	// rather than merely fitting an independently estimated JSON length.
	client, peer := wirePair(t)
	done := make(chan error, 1)
	go func() {
		request, err := ReadRequest(wireContext(t), peer)
		if err == nil && !bytes.Equal(request, raw) {
			err = ErrWire
		}
		if err == nil {
			err = WriteResponse(wireContext(t), peer, response)
		}
		done <- err
	}()
	got, err := Exchange(wireContext(t), client, raw)
	if err != nil || !bytes.Equal(got, response) {
		t.Fatal("bounded status did not fit actual response frame", len(response), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("status projection/frame wrote authority")
	}
	t.Logf("synthetic codec-valid pending records=%d projected=%d body_bytes=%d frame_limit=%d truncated=%v", total, len(status.Pending), len(response), control.MaxResultBytes, status.PendingTruncated)
}
func TestAPIRunReadyAndStrictRoundTrip(t *testing.T) {
	paths := apiPaths(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan ReadyInfo, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, paths, Options{LauncherArgs: []string{"never-launch-core-from-api-test"}, Ready: func(info ReadyInfo) error { ready <- info; return nil }})
	}()
	var info ReadyInfo
	select {
	case info = <-ready:
	case err := <-done:
		t.Fatal("service before ready", err)
	case <-time.After(3 * time.Second):
		t.Fatal("service readiness timeout")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("service did not close")
		}
	})
	before := apiTree(t, paths)
	// Bypass local Call validation to exercise the actual server's malformed
	// frame path after private peer/instance authentication. It must reject the
	// frame without trusting an ID and remain available for later requests.
	conn, err := DialEndpoint(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	malformed := []byte(`{"api_version":"v1","request_id":"one","request_id":"two","operation":"status.get","params":{"credential":"private-roundtrip-secret"}}`)
	wireSend(t, conn, wireFrame(malformed))
	var rejected []byte
	err = wireIO(wireContext(t), conn, func() error {
		var readErr error
		rejected, readErr = readFrame(conn, control.MaxResultBytes)
		return readErr
	})
	conn.Close()
	if err != nil {
		t.Fatal("malformed frame response", err)
	}
	rejectedEnvelope, rejectedCode := apiResponse(t, rejected)
	if rejectedCode != "INVALID_ARGUMENT" || string(rejectedEnvelope["request_id"]) != "null" || bytes.Contains(rejected, []byte("private-roundtrip-secret")) {
		t.Fatal("malformed frame was trusted/disclosed")
	}
	for _, test := range []struct {
		id, version, operation string
		params                 any
		code                   string
	}{{"actual-status", "v1", "status.get", map[string]any{}, ""}, {"actual-version", "v2", "status.get", map[string]any{}, "UNSUPPORTED_VERSION"}, {"actual-extra", "v1", "status.get", map[string]any{"private": "private-roundtrip-secret"}, "INVALID_ARGUMENT"}, {"actual-control-only", "v1", "runtime.start", map[string]any{"expected_revision": info.Revision, "control_only": true}, ""}} {
		raw := apiRequest(t, test.id, test.version, test.operation, test.params)
		response, err := Call(context.Background(), paths, raw)
		if err != nil {
			t.Fatal("authenticated API round trip", err)
		}
		envelope, code := apiResponse(t, response)
		if code != test.code || string(envelope["request_id"]) != fmt.Sprintf("%q", test.id) || bytes.Contains(response, []byte("private-roundtrip-secret")) {
			t.Fatal("API round trip mismatch", code)
		}
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("read/schema/control-only requests wrote authority")
	}
}
