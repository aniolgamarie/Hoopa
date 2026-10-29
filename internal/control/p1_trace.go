//go:build linux && amd64

package control

import "hoopa/internal/p1testhook"

// Request scope is not admission. In particular a failed preflight must not
// become an invented accepted operation in the supervisor's projection.
func p1RequestScope(r Request) func() {
	if !p1testhook.Enabled {
		return func() {}
	}
	_ = p1testhook.Record("control", "request-begin", "", "", r.ID(), false, map[string]any{"operation": r.Operation(), "proposed_operation_id": r.OperationID(), "request_digest": r.Digest()})
	ended := false // The serialized caller can end before delegating to Start.
	return func() {
		if ended {
			return
		}
		ended = true
		_ = p1testhook.Record("control", "request-end", "", "", r.ID(), false, map[string]any{"operation": r.Operation(), "proposed_operation_id": r.OperationID()})
	}
}

func p1Boundary(r Request) func(string) error {
	if !p1testhook.Enabled {
		return nil
	}
	return func(point string) error {
		return p1testhook.Record("workflow", point, "", "", r.ID(), true, map[string]any{"caller_operation_id": r.OperationID(), "operation": r.Operation()})
	}
}
