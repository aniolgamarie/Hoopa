package telemetrywire

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func sourceFields(kind string) map[string]json.RawMessage {
	f := counterFields()
	f["source_seq"] = json.RawMessage(`"1"`)
	f["kind"], _ = json.Marshal(kind)
	delete(f, "upload_bytes")
	delete(f, "download_bytes")
	return f
}

func putFields(f map[string]json.RawMessage, extra map[string]any) {
	for k, v := range extra {
		f[k], _ = json.Marshal(v)
	}
}

func openedFields() map[string]json.RawMessage {
	f := sourceFields("opened")
	putFields(f, map[string]any{"process": map[string]any{"status": "unknown", "reason": "owner_visibility"}, "source_addr": "127.0.0.1:12345", "entry_addr": "127.0.0.1:23456", "target_kind": "host", "target": "example.test", "identity_scope": "same_uid_netns_ipv4_loopback_held_socket;fd_transfer_unverified"})
	return f
}

func validSourceFixtures() []map[string]json.RawMessage {
	opened := openedFields()
	identified := openedFields()
	putFields(identified, map[string]any{"process": map[string]any{"status": "identified", "pid": 123, "name": "app", "name_source": "linux_proc_comm", "start_ticks": "100", "boot_id": "12345678-1234-1234-1234-123456789abc", "pid_namespace": "pid:[123]", "network_namespace": "net:[456]", "socket_inode": "789", "path": "/usr/bin/app"}})
	route := sourceFields("route_decided")
	putFields(route, map[string]any{"rule": "MATCH", "rule_payload": "", "logical_target": "DIRECT", "process": map[string]any{"status": "unknown", "reason": "owner_visibility"}})
	bind := sourceFields("route_decided")
	putFields(bind, map[string]any{"phase": "connection_binding", "connection_id": "connection-1"})
	setup := sourceFields("setup_finished")
	putFields(setup, map[string]any{"definition": "attempt_dial_handshake_v1_candidate", "duration_ms": 4, "waited_ms": 4, "flow_duration_ms": 6, "flow_waited_ms": 6, "flow_definition": "request_known_to_ready_v1_candidate", "outcome": "success", "failure_stage": "", "ordered_chain": []string{"DIRECT"}, "provider_chain": []string{""}, "route_evidence": "returned_conn_only", "protocol_ready_validation": "unverified"})
	failure := sourceFields("setup_finished")
	putFields(failure, map[string]any{"definition": "attempt_dial_handshake_v1_candidate", "duration_ms": nil, "waited_ms": 4, "flow_duration_ms": nil, "flow_waited_ms": 6, "flow_definition": "request_known_to_ready_v1_candidate", "outcome": "failure", "failure_stage": "dial", "ordered_chain": []string{}, "provider_chain": []string{}, "route_evidence": "returned_conn_only", "protocol_ready_validation": "unverified"})
	gap := sourceFields("gap")
	putFields(gap, map[string]any{"reason": "source_shutdown_before_final", "dimensions": []string{"bytes", "route_or_setup", "window_checkpoints"}, "upload_bytes": nil, "download_bytes": nil, "connection_id": ""})
	epoch := sourceFields("epoch_closed")
	putFields(epoch, map[string]any{"flow_id": nil, "attempt_id": nil, "reason": "core_shutdown", "unfinished_flows": "1", "finality": "incomplete"})
	return []map[string]json.RawMessage{opened, identified, route, bind, setup, failure, counterFields(), gap, epoch}
}

func requireInvalidSource(t *testing.T, raw []byte) {
	t.Helper()
	e, err := DecodeSourceEvent(raw)
	if !errors.Is(err, ErrInvalidSourceEvent) || !reflect.DeepEqual(e, SourceEvent{}) {
		t.Fatalf("invalid event was accepted or partially exposed: %v", err)
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatal("parser error leaked input")
	}
}

func TestSourceEventAllKindsPreserveRawAndExactIntegers(t *testing.T) {
	for i, f := range validSourceFixtures() {
		raw := append([]byte(" \n"), encodeCounterFields(t, f)...)
		e, err := DecodeSourceEvent(raw)
		if err != nil || !bytes.Equal(e.Raw, raw) {
			t.Fatalf("fixture %d: %v", i, err)
		}
		if e.Kind == "bytes_checkpoint" && (e.SourceSeq != ^uint64(0) || uint64(e.Counter.UploadBytes) != 9007199254740993) {
			t.Fatal("exact uint64 changed")
		}
		raw[0] = '\t'
		if bytes.Equal(e.Raw, raw) {
			t.Fatal("decoded raw aliases caller memory")
		}
		for name := range f {
			copyFields := make(map[string]json.RawMessage)
			for k, v := range f {
				copyFields[k] = v
			}
			delete(copyFields, name)
			requireInvalidSource(t, encodeCounterFields(t, copyFields))
		}
	}
}

func TestSourceEventRejectsDuplicatesUnknownsAndTrailingData(t *testing.T) {
	for _, f := range validSourceFixtures() {
		raw := encodeCounterFields(t, f)
		for _, suffix := range []string{`,"event_id":"event-1"}`, `,"event\u005fid":"event-1"}`, `,"private-value":{}}`} {
			requireInvalidSource(t, append(append([]byte(nil), raw[:len(raw)-1]...), suffix...))
		}
		requireInvalidSource(t, append(append([]byte(nil), raw...), []byte(` null`)...))
	}
	f := openedFields()
	for _, process := range []string{`{"status":"unknown","reason":"owner_visibility","rea\u0073on":"owner_visibility"}`, `{"status":"unknown","reason":"owner_visibility","pid":123}`, `{"status":"identified","pid":123}`, `null`} {
		f["process"] = json.RawMessage(process)
		requireInvalidSource(t, encodeCounterFields(t, f))
	}
}

func TestSourceEventRejectsUnknownAsZeroAndMisleadingFinality(t *testing.T) {
	fixtures := validSourceFixtures()
	for _, tc := range []struct {
		index        int
		field, value string
	}{
		{0, "source_seq", `"0"`}, {0, "source_seq", `1`}, {0, "source_seq", `"01"`},
		{0, "flow_id", `null`}, {0, "source_instance_id", `"private-value /"`}, {0, "receipt_seq", `"1"`},
		{0, "schema_version", `1e0`}, {0, "observed_at", `"2026-10-22T10:11:12+00:00"`},
		{0, "target_kind", `"unknown"`}, {0, "source_addr", `"private-value"`},
		{4, "duration_ms", `null`}, {4, "waited_ms", `1.0`}, {4, "duration_ms", `-1`}, {4, "flow_duration_ms", `1e1`},
		{4, "protocol_ready_validation", `"passed"`}, {4, "ordered_chain", `null`}, {4, "ordered_chain", `[""]`},
		{5, "duration_ms", `0`}, {7, "upload_bytes", `"0"`}, {7, "dimensions", `[]`},
		{7, "dimensions", `["bytes","bytes"]`}, {8, "flow_id", `"flow-1"`},
		{7, "dimensions", `[""]`},
		{8, "finality", `"complete"`}, {8, "unfinished_flows", `"01"`},
	} {
		f := make(map[string]json.RawMessage)
		for k, v := range fixtures[tc.index] {
			f[k] = v
		}
		f[tc.field] = json.RawMessage(tc.value)
		requireInvalidSource(t, encodeCounterFields(t, f))
	}
	for _, raw := range [][]byte{nil, []byte(`null`), []byte(`[]`), []byte(`{"kind":`), bytes.Repeat([]byte(" "), SourceInputLimit+1)} {
		requireInvalidSource(t, raw)
	}
}
