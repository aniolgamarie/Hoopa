package mihomoevents

import (
	"hoopa/internal/config"
	"hoopa/protocol/telemetrywire"
)

type CounterKind = telemetrywire.CounterKind

const (
	BytesCheckpoint CounterKind = telemetrywire.BytesCheckpoint
	Closed          CounterKind = telemetrywire.Closed
)

// ErrInvalidCounterEvent intentionally contains no input values or raw parser
// errors, which could disclose private frame content.
var ErrInvalidCounterEvent = telemetrywire.ErrInvalidCounterEvent

type CounterEvent struct {
	SchemaVersion    uint8               `json:"schema_version"`
	SourceInstanceID config.ID           `json:"source_instance_id"`
	SourceEpoch      config.CounterEpoch `json:"source_epoch"`
	EventID          config.ID           `json:"event_id"`
	SourceSeq        uint64              `json:"source_seq,string"`
	FlowID           config.ID           `json:"flow_id"`
	AttemptID        config.ID           `json:"attempt_id"`
	ObservedAt       config.Timestamp    `json:"observed_at"`
	ConfigGeneration config.Generation   `json:"config_generation"`
	BoundaryRef      string              `json:"boundary_ref"`
	Kind             CounterKind         `json:"kind"`
	UploadBytes      config.Bytes        `json:"upload_bytes"`
	DownloadBytes    config.Bytes        `json:"download_bytes"`
}

func counterFromWire(e telemetrywire.CounterEvent) CounterEvent {
	observed, _ := config.ParseTimestamp(e.ObservedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	return CounterEvent{SchemaVersion: e.SchemaVersion, SourceInstanceID: config.ID(e.SourceInstanceID), SourceEpoch: config.CounterEpoch(e.SourceEpoch), EventID: config.ID(e.EventID), SourceSeq: e.SourceSeq, FlowID: config.ID(e.FlowID), AttemptID: config.ID(e.AttemptID), ObservedAt: observed, ConfigGeneration: config.Generation(e.ConfigGeneration), BoundaryRef: e.BoundaryRef, Kind: e.Kind, UploadBytes: config.Bytes(e.UploadBytes), DownloadBytes: config.Bytes(e.DownloadBytes)}
}

// DecodeCounterEvent preserves the frozen typed experiment API.
func DecodeCounterEvent(raw []byte) (CounterEvent, error) {
	e, err := telemetrywire.DecodeCounterEvent(raw)
	if err != nil {
		return CounterEvent{}, err
	}
	return counterFromWire(e), nil
}
