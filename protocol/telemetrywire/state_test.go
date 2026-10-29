package telemetrywire

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func stateEvent(t *testing.T, fields map[string]json.RawMessage, seq int, changes map[string]any) SourceEvent {
	t.Helper()
	copyFields := map[string]json.RawMessage{}
	for k, v := range fields {
		copyFields[k] = v
	}
	putFields(copyFields, map[string]any{"source_seq": strconv.Itoa(seq), "event_id": "event-" + strconv.Itoa(seq)})
	putFields(copyFields, changes)
	event, err := DecodeSourceEvent(encodeCounterFields(t, copyFields))
	if err != nil {
		t.Fatal(err)
	}
	return event
}
func commitState(t *testing.T, s *State, e SourceEvent) {
	t.Helper()
	if err := s.Commit(e); err != nil {
		t.Fatal(err)
	}
}

func TestStateRejectedTransitionNeverAdvances(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before []SourceEvent
		bad    SourceEvent
	}{
		{"orphan_counter", nil, stateEvent(t, counterFields(), 1, nil)},
		{"orphan_setup", nil, stateEvent(t, validSourceFixtures()[4], 1, nil)},
		{"sequence_gap", nil, stateEvent(t, openedFields(), 2, nil)},
		{"metadata", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, counterFields(), 2, map[string]any{"config_generation": "other"})},
		{"epoch", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, counterFields(), 2, map[string]any{"source_epoch": "other"})},
		{"instance", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, counterFields(), 2, map[string]any{"source_instance_id": "other"})},
		{"repeat_id", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, counterFields(), 2, map[string]any{"event_id": "event-1"})},
		{"repeat_open", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, openedFields(), 2, nil)},
		{"attempt_other_flow", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, openedFields(), 2, map[string]any{"flow_id": "other-flow"})},
		{"repeat_setup", []SourceEvent{stateEvent(t, openedFields(), 1, nil), stateEvent(t, validSourceFixtures()[4], 2, nil)}, stateEvent(t, validSourceFixtures()[4], 3, nil)},
		{"decreasing_counter", []SourceEvent{stateEvent(t, openedFields(), 1, nil), stateEvent(t, counterFields(), 2, map[string]any{"upload_bytes": "10"})}, stateEvent(t, counterFields(), 3, map[string]any{"upload_bytes": "9"})},
		{"after_terminal", []SourceEvent{stateEvent(t, openedFields(), 1, nil), stateEvent(t, counterFields(), 2, map[string]any{"kind": "closed"})}, stateEvent(t, counterFields(), 3, nil)},
		{"bytes_gap_after_closed", []SourceEvent{stateEvent(t, openedFields(), 1, nil), stateEvent(t, counterFields(), 2, map[string]any{"kind": "closed"})}, stateEvent(t, validSourceFixtures()[7], 3, nil)},
		{"false_complete_marker", []SourceEvent{stateEvent(t, openedFields(), 1, nil)}, stateEvent(t, validSourceFixtures()[8], 2, map[string]any{"unfinished_flows": "0", "finality": "complete"})},
		{"wrong_unfinished_count", []SourceEvent{stateEvent(t, openedFields(), 1, nil), stateEvent(t, validSourceFixtures()[7], 2, nil)}, stateEvent(t, validSourceFixtures()[8], 3, map[string]any{"unfinished_flows": "2"})},
		{"after_marker", []SourceEvent{stateEvent(t, validSourceFixtures()[8], 1, map[string]any{"unfinished_flows": "0", "finality": "complete"})}, stateEvent(t, openedFields(), 2, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s State
			for _, e := range tc.before {
				commitState(t, &s, e)
			}
			before := s.Snapshot()
			if !errors.Is(s.Validate(tc.bad), ErrInvalidLifecycle) || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("validation accepted/mutated invalid transition")
			}
			if !errors.Is(s.Commit(tc.bad), ErrInvalidLifecycle) || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("commit accepted/mutated invalid transition")
			}
		})
	}
}

func TestStateMultipleAttemptsBindingPendingAndExactFinal(t *testing.T) {
	var s State
	commitState(t, &s, stateEvent(t, openedFields(), 1, map[string]any{"attempt_id": "attempt-0"}))
	// NewAttempt has no wire event; even its checkpoint can precede setup.
	cp := stateEvent(t, counterFields(), 2, map[string]any{"attempt_id": "attempt-1", "upload_bytes": "10", "download_bytes": "20"})
	before := s.Snapshot()
	if s.Validate(cp) != nil || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("Validate advanced confirmed state")
	}
	commitState(t, &s, cp)
	pending := s.Snapshot()
	if pending.LastSourceSeq != 2 || pending.EpochFinality != "unverified" || pending.Flows[0].State != "pending" || pending.Flows[0].TailBytes != nil {
		t.Fatal("pending tail fabricated")
	}
	commitState(t, &s, stateEvent(t, validSourceFixtures()[5], 3, map[string]any{"attempt_id": "attempt-1"}))
	commitState(t, &s, stateEvent(t, validSourceFixtures()[4], 4, map[string]any{"attempt_id": "attempt-2"}))
	commitState(t, &s, stateEvent(t, validSourceFixtures()[3], 5, map[string]any{"attempt_id": "attempt-2"}))
	commitState(t, &s, stateEvent(t, counterFields(), 6, map[string]any{"attempt_id": "attempt-2", "kind": "closed", "upload_bytes": "13", "download_bytes": "24"}))
	commitState(t, &s, stateEvent(t, validSourceFixtures()[7], 7, map[string]any{"attempt_id": "attempt-2", "reason": "source_event_uncertain", "dimensions": []string{"route_or_setup"}}))
	commitState(t, &s, stateEvent(t, validSourceFixtures()[8], 8, map[string]any{"unfinished_flows": "0", "finality": "complete"}))
	snap := s.Snapshot()
	f := snap.Flows[0]
	if snap.LastSourceSeq != 8 || snap.EpochFinality != "complete" || f.State != "closed" || f.Final.UploadBytes != 13 || f.LastCheckpoint.UploadBytes != 10 || f.TailBytes == nil || *f.TailBytes != 0 || len(f.GapDimensions) != 1 {
		t.Fatal("exact final/dimension gap changed")
	}
	// Snapshot mutations cannot change the validator or another projection.
	f.Final.UploadBytes = 0
	f.LastCheckpoint.UploadBytes = 0
	*f.TailBytes = 99
	f.GapDimensions[0] = "bytes"
	again := s.Snapshot().Flows[0]
	if again.Final.UploadBytes != 13 || again.LastCheckpoint.UploadBytes != 10 || *again.TailBytes != 0 || again.GapDimensions[0] != "route_or_setup" {
		t.Fatal("snapshot aliases validator state")
	}
}

func TestStateEnforcesConcurrentActiveFlowLimit(t *testing.T) {
	var s State
	for i := 0; i < 32; i++ {
		e := stateEvent(t, openedFields(), i+1, map[string]any{"flow_id": "flow-live-" + strconv.Itoa(i), "attempt_id": "attempt-live-" + strconv.Itoa(i)})
		commitState(t, &s, e)
	}
	before := s.Snapshot()
	extra := stateEvent(t, openedFields(), 33, map[string]any{"flow_id": "flow-over-limit", "attempt_id": "attempt-over-limit"})
	if s.Validate(extra) == nil || s.Commit(extra) == nil || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("33 concurrent source flows accepted or state advanced")
	}
}

func TestStateActiveLimitDoesNotLimitTerminalHistory(t *testing.T) {
	var s State
	seq := 0
	for i := 0; i < 65; i++ {
		seq++
		changes := map[string]any{"flow_id": "flow-ended-" + strconv.Itoa(i), "attempt_id": "attempt-ended-" + strconv.Itoa(i)}
		commitState(t, &s, stateEvent(t, openedFields(), seq, changes))
		seq++
		fields := counterFields()
		changes["kind"] = "closed"
		if i%2 == 1 {
			fields = validSourceFixtures()[7]
			changes["kind"] = "gap"
		}
		commitState(t, &s, stateEvent(t, fields, seq, changes))
	}
	for i := 0; i < 32; i++ {
		seq++
		commitState(t, &s, stateEvent(t, openedFields(), seq, map[string]any{"flow_id": "flow-active-" + strconv.Itoa(i), "attempt_id": "attempt-active-" + strconv.Itoa(i)}))
	}
	if len(s.Snapshot().Flows) != 97 {
		t.Fatal("historical flows were evicted or counted as active")
	}
}
