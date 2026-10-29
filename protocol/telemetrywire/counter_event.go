// Package telemetrywire decodes candidate experimental counter frames. Parsing a
// frame does not establish an event source, delivery guarantee or capability gate.
package telemetrywire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const counterInputLimit = 64 * 1024

type CounterKind string

const (
	BytesCheckpoint CounterKind = "bytes_checkpoint"
	Closed          CounterKind = "closed"
)

// ErrInvalidCounterEvent intentionally contains no input values or raw parser
// errors, which could disclose private frame content.
var ErrInvalidCounterEvent = errors.New("invalid candidate counter event")

type CounterEvent struct {
	SchemaVersion    uint8       `json:"schema_version"`
	SourceInstanceID string      `json:"source_instance_id"`
	SourceEpoch      string      `json:"source_epoch"`
	EventID          string      `json:"event_id"`
	SourceSeq        uint64      `json:"source_seq,string"`
	FlowID           string      `json:"flow_id"`
	AttemptID        string      `json:"attempt_id"`
	ObservedAt       time.Time   `json:"observed_at"`
	ConfigGeneration string      `json:"config_generation"`
	BoundaryRef      string      `json:"boundary_ref"`
	Kind             CounterKind `json:"kind"`
	UploadBytes      uint64      `json:"upload_bytes,string"`
	DownloadBytes    uint64      `json:"download_bytes,string"`
}

func knownCounterField(name string) bool {
	switch name {
	case "schema_version", "source_instance_id", "source_epoch", "event_id", "source_seq", "flow_id", "attempt_id", "observed_at", "config_generation", "boundary_ref", "kind", "upload_bytes", "download_bytes":
		return true
	default:
		return false
	}
}

// DecodeCounterEvent accepts only the candidate v1 flat wire profile. Version is
// the literal JSON number token 1, sequence/counts are canonical decimal strings,
// and observed_at must retain UTC Z syntax. No numeric value passes through float64.
func DecodeCounterEvent(raw []byte) (CounterEvent, error) {
	invalid := func() (CounterEvent, error) { return CounterEvent{}, ErrInvalidCounterEvent }
	if len(raw) == 0 || len(raw) > counterInputLimit || !utf8.Valid(raw) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := make(map[string]json.RawMessage, 13)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalid()
		}
		name, ok := token.(string)
		if !ok || !knownCounterField(name) {
			return invalid()
		}
		// Token has already unescaped the name, so escaped duplicates also collide.
		if _, exists := fields[name]; exists {
			return invalid()
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return invalid()
		}
		fields[name] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(fields) != 13 {
		return invalid()
	}
	var trailing json.RawMessage
	if decoder.Decode(&trailing) != io.EOF {
		return invalid()
	}
	if !bytes.Equal(bytes.TrimSpace(fields["schema_version"]), []byte("1")) {
		return invalid()
	}
	values := make(map[string]string, 12)
	for name, value := range fields {
		if name == "schema_version" {
			continue
		}
		value = bytes.TrimSpace(value)
		if len(value) == 0 || value[0] != '"' {
			return invalid()
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return invalid()
		}
		values[name] = text
	}
	var instance, epoch, event, flow, attempt, generation string
	for _, id := range []struct {
		name   string
		target *string
	}{
		{"source_instance_id", &instance}, {"source_epoch", &epoch}, {"event_id", &event}, {"flow_id", &flow}, {"attempt_id", &attempt}, {"config_generation", &generation},
	} {
		parsed, err := parseID(values[id.name])
		if err != nil {
			return invalid()
		}
		*id.target = parsed
	}
	seq, err := parseBytes(values["source_seq"])
	if err != nil || seq == 0 {
		return invalid()
	}
	upload, err := parseBytes(values["upload_bytes"])
	if err != nil {
		return invalid()
	}
	download, err := parseBytes(values["download_bytes"])
	if err != nil {
		return invalid()
	}
	if !strings.HasSuffix(values["observed_at"], "Z") {
		return invalid()
	}
	observed, err := parseTimestamp(values["observed_at"])
	if err != nil {
		return invalid()
	}
	if values["boundary_ref"] != "mihomo_proxy_stream_v1" {
		return invalid()
	}
	kind := CounterKind(values["kind"])
	if kind != BytesCheckpoint && kind != Closed {
		return invalid()
	}
	return CounterEvent{
		SchemaVersion: 1, SourceInstanceID: instance, SourceEpoch: string(epoch), EventID: event, SourceSeq: uint64(seq), FlowID: flow, AttemptID: attempt, ObservedAt: observed, ConfigGeneration: string(generation), BoundaryRef: values["boundary_ref"], Kind: kind, UploadBytes: upload, DownloadBytes: download,
	}, nil
}
