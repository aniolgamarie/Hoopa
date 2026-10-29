package telemetrywire

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func qualityFixtures(t *testing.T) []map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/telemetry-quality-v2/positive.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]json.RawMessage
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		var f map[string]json.RawMessage
		if err := json.Unmarshal(s.Bytes(), &f); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestQualityV2StrictFixtures(t *testing.T) {
	fixtures := qualityFixtures(t)
	for i, f := range fixtures {
		raw := encodeCounterFields(t, f)
		e, err := DecodeSourceEvent(raw)
		if err != nil || e.SchemaVersion != 2 || !bytes.Equal(e.Raw, raw) {
			t.Fatalf("fixture %d: %v", i, err)
		}
		for key := range f {
			copy := map[string]json.RawMessage{}
			for k, v := range f {
				copy[k] = v
			}
			delete(copy, key)
			requireInvalidSource(t, encodeCounterFields(t, copy))
		}
		requireInvalidSource(t, append(append([]byte(nil), raw[:len(raw)-1]...), `,"kind":"opened"}`...))
	}
	if e, _ := DecodeSourceEvent(encodeCounterFields(t, fixtures[4])); e.Counter.SchemaVersion != 2 || e.Counter.UploadBytes != 9007199254740993 {
		t.Fatal("v2 common counter projection lost precision/version")
	}
	raw, err := os.ReadFile("../../testdata/telemetry-quality-v2/negative.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string
		Event json.RawMessage
	}
	if json.Unmarshal(raw, &cases) != nil {
		t.Fatal("bad fixtures")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) { requireInvalidSource(t, c.Event) })
	}
}
func TestQualityV2NestedStrictnessAndBounds(t *testing.T) {
	fixtures := qualityFixtures(t)
	for _, tc := range []struct {
		index    int
		key, raw string
	}{
		{1, "ordered_chain", `[{"internal_name":"x","adapter_type":"Direct","provider_name":"","node_id":null,"provider_id":null,"group_id":null,"node\u005fid":null}]`},
		{1, "ordered_chain", `[{"internal_name":"x","adapter_type":"Direct","provider_name":"","node_id":null,"provider_id":null,"group_id":null,"extra":0}]`},
		{1, "ordered_chain", `[{"internal_name":"x","adapter_type":"Direct","provider_name":"","node_id":"y","provider_id":null,"group_id":null}]`},
		{1, "ordered_chain", `[{"internal_name":"x","adapter_type":"Direct","provider_name":"p","node_id":null,"provider_id":"other","group_id":null}]`},
		{1, "ordered_chain", `[]`},
		{4, "segments", `[{"actual_connection_ref":"actual-1","attempt_id":"attempt-1","upload_bytes":"9007199254740993","download_bytes":"4","download\u005fbytes":"4"}]`},
		{4, "segments", `[{"actual_connection_ref":"actual-1","attempt_id":"attempt-1","upload_bytes":"18446744073709551615","download_bytes":"4"}]`},
		{2, "ready_ns", `"18446744073709551616"`}, {2, "finish_ns", `"06000000"`}, {2, "protocol", `"unknown"`}, {2, "failure_stage", `"dial_or_handshake"`},
	} {
		t.Run(tc.key+tc.raw, func(t *testing.T) {
			f := fixtures[tc.index]
			old := f[tc.key]
			f[tc.key] = json.RawMessage(tc.raw)
			requireInvalidSource(t, encodeCounterFields(t, f))
			f[tc.key] = old
		})
	}
	for _, p := range []struct{ protocol, ready string }{{"direct_tcp", "tcp_connected"}, {"http_connect", "http_connect_accepted"}, {"socks5", "socks5_connect_accepted"}} {
		e := stateEvent(t, fixtures[2], 1, map[string]any{"protocol": p.protocol, "ready_kind": p.ready})
		if e.Quality.ReadyKind != p.ready {
			t.Fatal("ready proof changed")
		}
	}
	for _, outcome := range []string{"failure", "timeout", "cancelled", "unknown"} {
		stage := "dial_or_handshake"
		if outcome == "unknown" {
			stage = ""
		}
		e := stateEvent(t, fixtures[2], 1, map[string]any{"protocol": "unknown", "ready_kind": "ready_unproven", "ready_ns": nil, "duration_ms": nil, "flow_duration_ms": nil, "reason": "ready_unknown", "outcome": outcome, "failure_stage": stage})
		if e.Quality.Outcome != outcome {
			t.Fatal("outcome changed")
		}
	}
	for _, tc := range []struct {
		index int
		key   string
		limit int
	}{{1, "ordered_chain", 8}, {4, "segments", 10}} {
		f := fixtures[tc.index]
		var items []json.RawMessage
		json.Unmarshal(f[tc.key], &items)
		var tooMany []json.RawMessage
		for i := 0; i <= tc.limit; i++ {
			tooMany = append(tooMany, items[0])
		}
		old := f[tc.key]
		f[tc.key], _ = json.Marshal(tooMany)
		requireInvalidSource(t, encodeCounterFields(t, f))
		f[tc.key] = old
	}
}
func TestQualityV2LifecycleRejectsWithoutMutation(t *testing.T) {
	f := qualityFixtures(t)
	for _, tc := range []struct {
		name    string
		before  []int
		index   int
		changes map[string]any
	}{
		{"unregistered-binding", []int{0}, 3, nil},
		{"unregistered-setup", []int{0}, 2, nil},
		{"unregistered-segment", []int{0}, 4, nil},
		{"wrong-attempt-setup", []int{0, 1}, 2, map[string]any{"attempt_id": "other"}},
		{"wrong-attempt-binding", []int{0, 1}, 3, map[string]any{"attempt_id": "other"}},
		{"wrong-flow-start", []int{0, 1}, 2, map[string]any{"flow_start_ns": "0", "flow_duration_ms": "5", "flow_observed_wait_ms": "6"}},
		{"duplicate-reference", []int{0, 1}, 1, nil},
		{"duplicate-binding", []int{0, 1, 3}, 3, nil},
		{"duplicate-setup", []int{0, 1, 2}, 2, nil},
		{"missing-zero-segment", []int{0, 1}, 4, map[string]any{"segments": []SegmentCounter{}, "upload_bytes": "0", "download_bytes": "0"}},
		{"segment-regression", []int{0, 1, 4}, 4, map[string]any{"upload_bytes": "9007199254740993", "segments": []SegmentCounter{{"actual-1", "attempt-1", 9007199254740992, 4}}, "unallocated_upload_bytes": "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s State
			for i, index := range tc.before {
				commitState(t, &s, stateEvent(t, f[index], i+1, nil))
			}
			e := stateEvent(t, f[tc.index], len(tc.before)+1, tc.changes)
			before := s.Snapshot()
			if s.Validate(e) == nil || s.Commit(e) == nil || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("invalid transition accepted or changed snapshot")
			}
		})
	}
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	mixed := stateEvent(t, validSourceFixtures()[2], 2, nil)
	if s.Commit(mixed) == nil {
		t.Fatal("mixed epoch accepted")
	}
}
func TestQualityV2ReplayRecoveryAndDetachedValidation(t *testing.T) {
	f := qualityFixtures(t)
	m := manifestFixture(t)
	changes := map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration}
	var s, replay State
	for i, fields := range f {
		e := stateEvent(t, fields, i+1, changes)
		if s.Validate(e) != nil || s.Validate(e) != nil {
			t.Fatal("validation mutated evidence")
		}
		commitState(t, &s, e)
		again, err := DecodeSourceEvent(e.Raw)
		if err != nil {
			t.Fatal(err)
		}
		commitState(t, &replay, again)
	}
	if !reflect.DeepEqual(s.Snapshot(), replay.Snapshot()) {
		t.Fatal("raw replay changed lifecycle")
	}
	life := s.Snapshot()
	if life.Flows[0].Final.SchemaVersion != 2 {
		t.Fatal("snapshot lost version")
	}
	report, err := ProjectRecovery(m, life, strings.Repeat("c", 64), nil)
	if err != nil || report.Flows[0].Final.UploadBytes != 9007199254740994 {
		t.Fatal("recovery lost v2 totals", err)
	}
	raw, _ := json.Marshal(report)
	for _, key := range []string{"actual_connection_ref", "quality", "segments"} {
		if bytes.Contains(raw, []byte(`"`+key+`"`)) {
			t.Fatal("recovery invented quality provenance")
		}
	}
	// A second source may reuse local IDs, with a fresh validator.
	var reopened State
	commitState(t, &reopened, stateEvent(t, f[0], 1, map[string]any{"source_epoch": "fresh-epoch"}))
}
func TestQualityV2TenSegmentsAndCrossFlowReferences(t *testing.T) {
	f := qualityFixtures(t)
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	for i := 0; i < 10; i++ {
		commitState(t, &s, stateEvent(t, f[1], i+2, map[string]any{"actual_connection_ref": "conn-" + strconv.Itoa(i), "attempt_id": "attempt-" + strconv.Itoa(i)}))
	}
	if s.Commit(stateEvent(t, f[1], 12, map[string]any{"actual_connection_ref": "eleventh", "attempt_id": "eleventh"})) == nil {
		t.Fatal("eleventh segment registered")
	}
	commitState(t, &s, stateEvent(t, f[0], 12, map[string]any{"flow_id": "other-flow", "attempt_id": "other-attempt"}))
	if s.Commit(stateEvent(t, f[1], 13, map[string]any{"flow_id": "other-flow", "attempt_id": "other-attempt", "actual_connection_ref": "conn-1"})) == nil {
		t.Fatal("cross-flow ref reused")
	}
}

func TestQualityV2NegativeEpochFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/telemetry-quality-v2/epoch-negative.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string
		Events []json.RawMessage
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var s State
			for i, raw := range tc.Events {
				e, err := DecodeSourceEvent(raw)
				if err != nil {
					t.Fatal("epoch fixture must be wire-valid", err)
				}
				if i < len(tc.Events)-1 {
					commitState(t, &s, e)
				} else {
					if s.Validate(e) == nil || s.Commit(e) == nil {
						t.Fatal("invalid epoch accepted")
					}
				}
			}
		})
	}
}

func TestQualityV2AttemptOrderAndUnallocatedMonotonic(t *testing.T) {
	f := qualityFixtures(t)
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	commitState(t, &s, stateEvent(t, f[1], 2, nil))
	commitState(t, &s, stateEvent(t, f[2], 3, nil))
	commitState(t, &s, stateEvent(t, f[1], 4, map[string]any{"actual_connection_ref": "actual-2", "attempt_id": "attempt-2"}))
	e := stateEvent(t, f[2], 5, map[string]any{"actual_connection_ref": "actual-2", "attempt_id": "attempt-2", "attempt_start_ns": "2000000", "duration_ms": "3", "observed_wait_ms": "4"})
	if s.Commit(e) == nil {
		t.Fatal("attempt start moved backwards")
	}
	var other State
	commitState(t, &other, stateEvent(t, f[0], 1, nil))
	commitState(t, &other, stateEvent(t, f[4], 2, map[string]any{"segments": []SegmentCounter{}, "upload_bytes": "10", "download_bytes": "0", "unallocated_upload_bytes": "10"}))
	commitState(t, &other, stateEvent(t, f[1], 3, nil))
	e = stateEvent(t, f[4], 4, map[string]any{"segments": []SegmentCounter{{"actual-1", "attempt-1", 1, 0}}, "upload_bytes": "10", "download_bytes": "0", "unallocated_upload_bytes": "9"})
	if other.Commit(e) == nil {
		t.Fatal("unallocated bytes moved to segment")
	}
}

func TestQualityV2Uint64BoundaryAndSegmentOverflow(t *testing.T) {
	f := qualityFixtures(t)
	max := "18446744073709551615"
	ms := "18446744073709"
	e := stateEvent(t, f[2], 1, map[string]any{"flow_start_ns": "0", "attempt_start_ns": "0", "ready_ns": max, "finish_ns": max, "duration_ms": ms, "flow_duration_ms": ms, "observed_wait_ms": ms, "flow_observed_wait_ms": ms})
	if e.Quality.FinishNS != ^uint64(0) {
		t.Fatal("uint64 nanoseconds truncated")
	}
	bad := f[4]
	putFields(bad, map[string]any{"upload_bytes": max, "unallocated_upload_bytes": "1", "segments": []SegmentCounter{{"actual-1", "attempt-1", ^uint64(0), 4}}})
	requireInvalidSource(t, encodeCounterFields(t, bad))
}
func TestQualityV2NestedRequiredFieldsAndValidLimits(t *testing.T) {
	f := qualityFixtures(t)
	for _, tc := range []struct {
		index int
		key   string
	}{{1, "ordered_chain"}, {4, "segments"}} {
		var items []map[string]json.RawMessage
		json.Unmarshal(f[tc.index][tc.key], &items)
		for key := range items[0] {
			copy := map[string]json.RawMessage{}
			for k, v := range items[0] {
				if k != key {
					copy[k] = v
				}
			}
			original := f[tc.index][tc.key]
			f[tc.index][tc.key], _ = json.Marshal([]map[string]json.RawMessage{copy})
			requireInvalidSource(t, encodeCounterFields(t, f[tc.index]))
			f[tc.index][tc.key] = original
		}
	}
	var chain []RouteHop
	json.Unmarshal(f[1]["ordered_chain"], &chain)
	for len(chain) < 8 {
		chain = append(chain, chain[0])
	}
	if stateEvent(t, f[1], 1, map[string]any{"ordered_chain": chain}).OrderedChain == nil {
		t.Fatal("eight-hop limit rejected")
	}
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	segments := []SegmentCounter{}
	for i := 0; i < 10; i++ {
		ref, attempt := "conn-"+strconv.Itoa(i), "attempt-"+strconv.Itoa(i)
		commitState(t, &s, stateEvent(t, f[1], i+2, map[string]any{"actual_connection_ref": ref, "attempt_id": attempt}))
		segments = append(segments, SegmentCounter{ref, attempt, uint64(i), 0})
	}
	commitState(t, &s, stateEvent(t, f[4], 12, map[string]any{"upload_bytes": "45", "download_bytes": "0", "segments": segments}))
}

func TestQualityV2OneActualConnectionPerAttempt(t *testing.T) {
	f := qualityFixtures(t)
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	commitState(t, &s, stateEvent(t, f[1], 2, nil))
	if s.Commit(stateEvent(t, f[1], 3, map[string]any{"actual_connection_ref": "second-for-same-attempt"})) == nil {
		t.Fatal("attempt acquired two actual connections")
	}
	// Rejection must not advance sequence or mutate connection state.
	commitState(t, &s, stateEvent(t, f[2], 3, nil))
}

func TestQualityV2PreRouteFailure(t *testing.T) {
	f := qualityFixtures(t)
	changes := map[string]any{"protocol": "unknown", "ready_kind": "ready_unproven", "outcome": "timeout", "reason": "ready_unknown", "failure_stage": "pre_route", "actual_connection_ref": nil, "ready_ns": nil, "duration_ms": nil, "flow_duration_ms": nil, "attempt_start_ns": "1000000", "observed_wait_ms": "5"}
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	commitState(t, &s, stateEvent(t, f[2], 2, changes))
	if s.Commit(stateEvent(t, f[1], 3, nil)) == nil {
		t.Fatal("actual connection after aborted pre-route accepted")
	}
	var existing State
	commitState(t, &existing, stateEvent(t, f[0], 1, nil))
	commitState(t, &existing, stateEvent(t, f[1], 2, nil))
	if existing.Commit(stateEvent(t, f[2], 3, changes)) == nil {
		t.Fatal("pre-route failure after actual dial accepted")
	}
}
