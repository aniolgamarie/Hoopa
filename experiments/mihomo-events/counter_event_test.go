package mihomoevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func counterFields() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"schema_version": json.RawMessage(`1`), "source_instance_id": json.RawMessage(`"core-a"`), "source_epoch": json.RawMessage(`"epoch-a"`), "event_id": json.RawMessage(`"event-1"`), "source_seq": json.RawMessage(`"18446744073709551615"`), "flow_id": json.RawMessage(`"flow-1"`), "attempt_id": json.RawMessage(`"attempt-1"`), "observed_at": json.RawMessage(`"2026-10-22T10:11:12.123456789Z"`), "config_generation": json.RawMessage(`"generation-1"`), "boundary_ref": json.RawMessage(`"mihomo_proxy_stream_v1"`), "kind": json.RawMessage(`"bytes_checkpoint"`), "upload_bytes": json.RawMessage(`"9007199254740993"`), "download_bytes": json.RawMessage(`"18446744073709551615"`),
	}
}
func encodeCounterFields(t *testing.T, fields map[string]json.RawMessage) []byte {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func requireInvalidCounter(t *testing.T, raw []byte) {
	t.Helper()
	event, err := DecodeCounterEvent(raw)
	if !errors.Is(err, ErrInvalidCounterEvent) {
		t.Fatalf("expected fixed invalid-frame error, got %v", err)
	}
	if !reflect.DeepEqual(event, CounterEvent{}) {
		t.Fatal("invalid frame returned partially decoded fields")
	}
	if strings.Contains(err.Error(), "private-value") {
		t.Fatal("error leaked wire value")
	}
}
func TestCounterEventExactLargeValuesAndTypedFields(t *testing.T) {
	fields := counterFields()
	raw := encodeCounterFields(t, fields)
	event, err := DecodeCounterEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != 1 || event.SourceSeq != ^uint64(0) || uint64(event.UploadBytes) != 9007199254740993 || uint64(event.DownloadBytes) != ^uint64(0) {
		t.Fatalf("numeric precision changed: %+v", event)
	}
	if event.SourceInstanceID != "core-a" || event.SourceEpoch != "epoch-a" || event.EventID != "event-1" || event.FlowID != "flow-1" || event.AttemptID != "attempt-1" || event.ConfigGeneration != "generation-1" || event.BoundaryRef != "mihomo_proxy_stream_v1" || event.Kind != BytesCheckpoint {
		t.Fatalf("fields changed: %+v", event)
	}
	if event.ObservedAt.Time().Format(time.RFC3339Nano) != "2026-10-22T10:11:12.123456789Z" {
		t.Fatal("timestamp lost precision")
	}
	fields["kind"] = json.RawMessage(`"closed"`)
	fields["upload_bytes"] = json.RawMessage(`"0"`)
	fields["source_seq"] = json.RawMessage(`"1"`)
	event, err = DecodeCounterEvent(encodeCounterFields(t, fields))
	if err != nil || event.Kind != Closed || event.UploadBytes != 0 || event.SourceSeq != 1 {
		t.Fatal(event, err)
	}
	if _, err = DecodeCounterEvent(append(append([]byte(" \n"), raw...), []byte("\t \n")...)); err != nil {
		t.Fatal("JSON whitespace rejected", err)
	}
}
func TestCounterEventRejectsMissingNullAndUnknownFields(t *testing.T) {
	for name := range counterFields() {
		t.Run("missing/"+name, func(t *testing.T) {
			fields := counterFields()
			delete(fields, name)
			requireInvalidCounter(t, encodeCounterFields(t, fields))
		})
		t.Run("null/"+name, func(t *testing.T) {
			fields := counterFields()
			fields[name] = json.RawMessage(`null`)
			requireInvalidCounter(t, encodeCounterFields(t, fields))
		})
	}
	fields := counterFields()
	fields["private-value"] = json.RawMessage(`"private-value"`)
	requireInvalidCounter(t, encodeCounterFields(t, fields))
}
func TestCounterEventRejectsWireTypeAndScalarViolations(t *testing.T) {
	cases := []struct{ name, field, raw string }{
		{"version_unknown", "schema_version", `2`}, {"version_string", "schema_version", `"1"`}, {"version_decimal", "schema_version", `1.0`}, {"version_exponent", "schema_version", `1e0`},
		{"kind_unknown", "kind", `"private-value"`}, {"kind_number", "kind", `1`}, {"boundary_unknown", "boundary_ref", `"private-value"`},
		{"utc_offset", "observed_at", `"2026-10-22T10:11:12+00:00"`}, {"non_utc", "observed_at", `"2026-10-22T10:11:12+01:00"`}, {"lowercase_z", "observed_at", `"2026-10-22T10:11:12z"`}, {"bad_date", "observed_at", `"2026-02-30T10:11:12Z"`}, {"time_excess_precision", "observed_at", `"2026-10-22T10:11:12.1234567890Z"`},
	}
	for _, field := range []string{"source_seq", "upload_bytes", "download_bytes"} {
		for _, value := range []string{`1`, `9007199254740993`, `""`, `"01"`, `"-1"`, `"1.0"`, `"1e0"`, `"18446744073709551616"`, `" 1"`, `"+1"`, `"١"`, `true`, `[]`, `{}`} {
			cases = append(cases, struct{ name, field, raw string }{field + "/" + value, field, value})
		}
	}
	cases = append(cases, struct{ name, field, raw string }{"zero_sequence", "source_seq", `"0"`})
	for _, field := range []string{"source_instance_id", "source_epoch", "event_id", "flow_id", "attempt_id", "config_generation"} {
		for _, value := range []string{`""`, `"bad/id"`, `"private-value with space"`, `1`, `{}`} {
			cases = append(cases, struct{ name, field, raw string }{field + "/" + value, field, value})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := counterFields()
			fields[tc.field] = json.RawMessage(tc.raw)
			requireInvalidCounter(t, encodeCounterFields(t, fields))
		})
	}
}
func TestCounterEventRejectsDuplicateEscapedKeysAndTrailingJSON(t *testing.T) {
	raw := encodeCounterFields(t, counterFields())
	for _, suffix := range []string{`,"event_id":"event-1"}`, `,"event\u005fid":"event-1"}`, `,"schema_version":1}`} {
		malformed := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(suffix)...)
		requireInvalidCounter(t, malformed)
	}
	for _, raw := range [][]byte{nil, []byte(`null`), []byte(`[]`), []byte(`1`), []byte(`{"event_id":`), append(append([]byte(nil), raw...), []byte(` {}`)...), append(append([]byte(nil), raw...), []byte(` null`)...), append(append([]byte(nil), raw...), ',')} {
		requireInvalidCounter(t, raw)
	}
}
func TestCounterEventInputAndIDBounds(t *testing.T) {
	fields := counterFields()
	fields["event_id"] = json.RawMessage(`"` + strings.Repeat("a", 128) + `"`)
	raw := encodeCounterFields(t, fields)
	if _, err := DecodeCounterEvent(raw); err != nil {
		t.Fatal("128-byte ID rejected", err)
	}
	padded := append(append([]byte(nil), raw...), bytes.Repeat([]byte(" "), 64*1024-len(raw))...)
	if _, err := DecodeCounterEvent(padded); err != nil {
		t.Fatal("64KiB input rejected", err)
	}
	requireInvalidCounter(t, append(padded, ' '))
	fields["event_id"] = json.RawMessage(`"` + strings.Repeat("a", 129) + `"`)
	requireInvalidCounter(t, encodeCounterFields(t, fields))
}
