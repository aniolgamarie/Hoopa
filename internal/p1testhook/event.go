// Package p1testhook is an inert bridge in ordinary builds. Only an explicitly
// compiled p1integration supervisor can install a sink; there is no environment
// switch, CLI option or product protocol for faults.
package p1testhook

import (
	"encoding/json"
	"errors"
)

var ErrInjected = errors.New("P1_TEST_INJECTED_FAILURE")

// Data contains bounded identity/digest metadata, never config text or a raw
// launch nonce. A point marked injectable precedes the attempted effect or is
// an existing explicit workflow cut; ordinary observations cannot inject error.
type Event struct {
	Component, Point, OperationID, RunID, RequestID string
	Injectable                                      bool
	Data                                            json.RawMessage
}

func (Event) String() string   { return "p1testhook.Event{private metadata}" }
func (Event) GoString() string { return "p1testhook.Event{private metadata}" }

// Record is synchronous only in the test build. A sink must never reenter a
// Coordinator/Store: callers can hold their locks. The separate supervisor may
// capture immutable originals directly from disk, then acknowledge. Sink loss
// must abort the test process, not silently continue or cause product rollback.
func Record(component, point, operationID, runID, requestID string, injectable bool, data any) error {
	if !Enabled {
		return nil
	}
	raw, err := json.Marshal(data)
	// Leave space for the supervisor's injectable/detail transport wrapper.
	if err != nil || len(raw) > 47<<10 || len(raw) == 0 || raw[0] != '{' {
		panic("invalid P1 test metadata")
	}
	e := Event{component, point, operationID, runID, requestID, injectable, raw}
	if emit(e) {
		if !injectable {
			panic("P1 fault at observation-only point")
		}
		return ErrInjected
	}
	return nil
}
