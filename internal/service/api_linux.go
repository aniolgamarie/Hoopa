//go:build linux && amd64

package service

import (
	"context"
	"encoding/json"
	"errors"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

var ErrNoSnapshot = errors.New("no committed snapshot")
var ErrUnsupportedVersion = errors.New("unsupported API version")

func failureCode(err error) (string, int) {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "CANCELLED", 7
	case errors.Is(err, ErrUnsupportedVersion):
		return "UNSUPPORTED_VERSION", 2
	case errors.Is(err, control.ErrRequest), errors.Is(err, control.ErrPlan):
		return "INVALID_ARGUMENT", 2
	case errors.Is(err, control.ErrCapabilityUnsupported):
		return "CAPABILITY_UNSUPPORTED", 2
	case errors.Is(err, control.ErrConfirmation):
		return "AUTHORIZATION_DENIED", 6
	case errors.Is(err, control.ErrRequestIDReused):
		return "REQUEST_ID_REUSED", 3
	case errors.Is(err, control.ErrConflict), errors.Is(err, config.ErrWriterBusy), errors.Is(err, config.ErrRunConflict), errors.Is(err, config.ErrOperationConflict):
		return "CONFLICT", 3
	case errors.Is(err, control.ErrRecoveryRequired), errors.Is(err, control.ErrApplyIncomplete), errors.Is(err, config.ErrUncertain), errors.Is(err, config.ErrOperationStaging), errors.Is(err, config.ErrRunStaging):
		return "RECOVERY_REQUIRED", 5
	case errors.Is(err, config.ErrOperationCapacity):
		return "OPERATION_CAPACITY_EXHAUSTED", 4
	case errors.Is(err, ErrNoSnapshot):
		return "DEPENDENCY_UNAVAILABLE", 4
	case errors.Is(err, control.ErrSourceSnapshotExpired):
		return "SNAPSHOT_EXPIRED", 4
	case errors.Is(err, ErrDiscovery), errors.Is(err, config.ErrIntegrity), errors.Is(err, config.ErrUnsafePath), errors.Is(err, control.ErrOperationRecord):
		return "INTEGRITY_ERROR", 5
	case errors.Is(err, control.ErrOperationNotFound):
		return "OPERATION_NOT_FOUND", 4
	case errors.Is(err, ErrServiceUnavailable):
		return "NOT_RUNNING", 4
	default:
		return "READ_UNAVAILABLE", 4
	}
}

// Failure emits constant, non-sensitive messages. Neither OS errors nor the
// request body are copied into the response. Only a validated request ID echoes.
func Failure(raw []byte, cause error) []byte {
	var id any
	if r, err := control.ParseRequest(raw); err == nil {
		id = r.ID()
	}
	code, _ := failureCode(cause)
	failure := map[string]any{"code": code, "message": code, "retryable": false}
	if reason, field, ok := control.SourcePlanFailureDetails(cause); ok {
		failure["details"] = map[string]any{"reason": reason, "field": field}
	}
	b, _ := json.Marshal(map[string]any{"api_version": "v1", "request_id": id, "ok": false, "data": nil, "error": failure, "warnings": []any{}})
	return b
}

func ExitCode(raw []byte) int {
	if _, err := responseID(raw); err != nil {
		return 5
	}
	var r struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return 5
	}
	if r.OK {
		return 0
	}
	switch r.Error.Code {
	case "INVALID_ARGUMENT", "UNSUPPORTED_VERSION", "CAPABILITY_UNSUPPORTED":
		return 2
	case "CONFLICT", "REQUEST_ID_REUSED":
		return 3
	case "NOT_RUNNING", "DEPENDENCY_UNAVAILABLE", "READ_UNAVAILABLE", "SNAPSHOT_EXPIRED", "OPERATION_NOT_FOUND", "OPERATION_CAPACITY_EXHAUSTED", "SUBSCRIPTION_UPDATE_FAILED":
		return 4
	case "AUTHORIZATION_DENIED":
		return 6
	case "CANCELLED":
		return 7
	default:
		return 5
	}
}

func success(r control.Request, data any) []byte {
	b, _ := json.Marshal(map[string]any{"api_version": "v1", "request_id": r.ID(), "ok": true, "data": data, "error": nil, "warnings": []any{}})
	return b
}

// Mutations apply schema only after exact durable lookup, preserving replay.
// Read-only request IDs only correlate responses and never enter that lookup.
func schema(r control.Request) (map[string]json.RawMessage, error) {
	if r.Version() != "v1" {
		return nil, ErrUnsupportedVersion
	}
	p, ok := wireObject(r.Parameters())
	if !ok {
		return nil, control.ErrRequest
	}
	var fields []string
	switch r.Operation() {
	case "status.get", "doctor.get":
		fields = []string{}
	case "subscription.update":
		return p, control.ValidateSourceUpdateRequest(r)
	case "subscription.list":
		_, err := control.SourceListCursor(r)
		return p, err
	case "config.plan":
		_, err := control.SourcePlanIntent(r)
		return p, err
	case "config.apply":
		return p, control.ValidateSourceApplyRequest(r)
	case "operation.get":
		fields = []string{"operation_id"}
	case "runtime.start":
		fields = []string{"expected_revision", "control_only"}
	case "runtime.stop":
		fields = []string{"expected_revision", "accept_disruption"}
	case "service.stop":
		fields = []string{"expected_revision"}
	case "operation.recover":
		fields = []string{"operation_id", "expected_revision"}
	default:
		return nil, control.ErrCapabilityUnsupported
	}
	if !wireFields(p, fields) {
		return nil, control.ErrRequest
	}
	for _, key := range fields {
		switch key {
		case "control_only", "accept_disruption":
			if _, ok := wireBool(p[key]); !ok {
				return nil, control.ErrRequest
			}
		default:
			s, ok := wireString(p[key])
			if !ok {
				return nil, control.ErrRequest
			}
			if _, e := config.ParseID(s); e != nil {
				return nil, control.ErrRequest
			}
		}
	}
	return p, nil
}

func parameter(p map[string]json.RawMessage, key string) string { s, _ := wireString(p[key]); return s }

func LocalStatus(ctx context.Context, paths config.Paths) (control.ServiceStatus, error) {
	return control.ReadServiceStatus(ctx, paths)
}

// Call never spawns or retries. Offline reads/replays do not acquire a writer.
// An unknown mutation preserves ErrServiceUnavailable for an explicit CLI start
// to bootstrap after separately checking its complete schema and revision.
func Call(ctx context.Context, paths config.Paths, raw []byte) ([]byte, error) {
	r, err := control.ParseRequest(raw)
	if err != nil {
		return nil, err
	}
	conn, err := DialEndpoint(ctx, paths)
	if err == nil {
		defer conn.Close()
		return Exchange(ctx, conn, raw)
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		return nil, err
	}
	if !control.IsReadOnlyOperation(r.Operation()) {
		old, replayErr := control.ReadReplay(paths, raw)
		if replayErr == nil {
			return old.Record.Result, nil
		}
		if !errors.Is(replayErr, control.ErrOperationNotFound) {
			return nil, replayErr
		}
	}
	p, err := schema(r)
	if err != nil {
		return nil, err
	}
	switch r.Operation() {
	case "doctor.get":
		v, err := control.ReadDoctor(ctx, paths)
		if err != nil {
			return nil, err
		}
		return success(r, v), nil
	case "status.get":
		v, err := LocalStatus(ctx, paths)
		if err != nil {
			return nil, err
		}
		return success(r, v), nil
	case "subscription.list":
		cursor, _ := control.SourceListCursor(r)
		v, err := control.ReadSources(ctx, paths, cursor)
		if err != nil {
			return nil, err
		}
		return success(r, v), nil
	case "config.plan":
		intent, _ := control.SourcePlanIntent(r)
		plan, err := control.ReadSourcePlan(ctx, paths, intent)
		if err != nil {
			return nil, err
		}
		return success(r, json.RawMessage(plan.Bytes())), nil
	case "operation.get":
		v, err := control.ReadOperation(paths, config.ID(parameter(p, "operation_id")))
		if err != nil {
			return nil, err
		}
		return success(r, v), nil
	default:
		return nil, ErrServiceUnavailable
	}
}
