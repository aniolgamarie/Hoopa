//go:build linux && amd64

package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func TestDoctorMissingInstanceIsReadOnly(t *testing.T) {
	paths := apiPaths(t)
	before := apiTree(t, paths)
	raw := apiRequest(t, "missing-doctor", "v1", "doctor.get", map[string]any{})
	response, err := Call(context.Background(), paths, raw)
	if err != nil {
		t.Fatal(err)
	}
	envelope, code := apiResponse(t, response)
	if code != "" || !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("doctor bootstrapped missing instance", code)
	}
	var report control.DoctorReport
	if err = json.Unmarshal(envelope["data"], &report); err != nil || len(report.Checks) != 6 || report.Checks[0].Result != "unavailable" {
		t.Fatal("missing authority fabricated", err)
	}
}

func TestReadOnlyIDsAreCorrelationNotMutationReplay(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "offline"}[offline], func(t *testing.T) {
			var original control.StoredOperation
			s, paths := apiServer(t, func(_ *config.Store, j *control.Journal, base config.ActivePointer) {
				original = apiSyntheticPending(t, j, base, "shared-query-id", "service.stop", map[string]any{"expected_revision": base.Revision}, false, false)
			})
			if offline {
				if err := s.endpoint.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := apiTree(t, paths)
			for _, op := range []string{"status.get", "doctor.get", "operation.get", "subscription.list", "config.plan"} {
				params := map[string]any{}
				if op == "operation.get" {
					params["operation_id"] = original.Record.Identity.OperationID
				}
				if op == "config.plan" {
					params["intent"] = map[string]any{}
				} // Fresh invalid input must be validated, never replayed.
				raw := apiRequest(t, "shared-query-id", "v1", op, params)
				var response []byte
				if offline {
					var err error
					response, err = Call(context.Background(), paths, raw)
					if err != nil {
						response = Failure(raw, err)
					}
				} else {
					response = s.dispatch(context.Background(), raw)
				}
				envelope, code := apiResponse(t, response)
				if code == "REQUEST_ID_REUSED" || string(envelope["request_id"]) != `"shared-query-id"` {
					t.Fatal("readonly query collided with mutation history", op, code)
				}
				if (op == "status.get" || op == "doctor.get" || op == "operation.get") && code != "" {
					t.Fatal("existing query failed", op, code)
				}
				if op == "config.plan" && code != "INVALID_ARGUMENT" {
					t.Fatal("fresh plan validation bypassed", code)
				}
				if !reflect.DeepEqual(before, apiTree(t, paths)) {
					t.Fatal("query wrote audit/dedupe state", op)
				}
			}
		})
	}
}
