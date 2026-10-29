package telemetrywire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrInvalidRecovery = errors.New("source recovery evidence invalid")

// WriterEndEvidence is supplied only by an explicit recovery/start adapter
// after taking the source writer lock and validating a full stable snapshot.
// This pure package cannot acquire a lock or prove an OS/network process ended.
type WriterEndEvidence struct {
	Kind           string `json:"kind"`
	SourceEpoch    string `json:"source_epoch"`
	ManifestSHA256 string `json:"manifest_sha256"`
	FramesSHA256   string `json:"frames_sha256"`
	ObservedAt     string `json:"observed_at"`
}

type RecoveryCounter struct {
	EventID       string `json:"event_id"`
	SourceSeq     uint64 `json:"source_seq,string"`
	UploadBytes   uint64 `json:"upload_bytes,string"`
	DownloadBytes uint64 `json:"download_bytes,string"`
}

type RecoveryFlow struct {
	FlowID           string           `json:"flow_id"`
	OpenedEventID    string           `json:"opened_event_id"`
	State            string           `json:"state"`
	Reason           string           `json:"reason"`
	LastCheckpoint   *RecoveryCounter `json:"last_trusted_checkpoint"`
	Final            *RecoveryCounter `json:"final"`
	TailBytes        *uint64          `json:"tail_bytes"`
	GapDimensions    []string         `json:"gap_dimensions"`
	ReconciliationID string           `json:"reconciliation_id,omitempty"`
	FactSHA256       string           `json:"fact_sha256,omitempty"`
}

type RecoveryReport struct {
	Scope            string             `json:"scope"`
	SourceEpoch      string             `json:"source_epoch"`
	SourceInstanceID string             `json:"source_instance_id"`
	ConfigGeneration string             `json:"config_generation"`
	ManifestSHA256   string             `json:"manifest_sha256"`
	FramesSHA256     string             `json:"frames_sha256"`
	LastSourceSeq    uint64             `json:"last_source_seq,string"`
	EpochFinality    string             `json:"epoch_finality"`
	ScanStatus       string             `json:"scan_status"`
	HistoryQuality   string             `json:"history_quality"`
	RecordingStatus  string             `json:"recording_status"`
	WriterEnd        *WriterEndEvidence `json:"writer_end_evidence"`
	Flows            []RecoveryFlow     `json:"flows"`
}

func recoveryCounter(c *CounterEvent) *RecoveryCounter {
	if c == nil {
		return nil
	}
	return &RecoveryCounter{c.EventID, c.SourceSeq, c.UploadBytes, c.DownloadBytes}
}

// ProjectRecovery consumes a validated State.Snapshot and a paired manifest.
// A read-only adapter passes nil evidence and never seals a missing final.
// Reports are derived data: no source/receipt position or byte fact is created.
func ProjectRecovery(m Manifest, life Lifecycle, framesHash string, ended *WriterEndEvidence) (RecoveryReport, error) {
	bad := func() (RecoveryReport, error) { return RecoveryReport{}, ErrInvalidRecovery }
	validated, err := DecodeManifest(m.Raw)
	if err != nil || validated.SHA256 != m.SHA256 || !hexBytes(framesHash, 32) {
		return bad()
	}
	m = validated
	if life.LastSourceSeq != 0 && (m.SchemaVersion == 2 && life.SchemaVersion != 5 || m.SchemaVersion == 1 && life.SchemaVersion == 5) {
		return bad()
	}
	if life.LastSourceSeq != 0 && (life.SourceEpoch != m.SourceEpoch || life.SourceInstanceID != m.SourceInstanceID || life.ConfigGeneration != m.ConfigGeneration) {
		return bad()
	}
	if ended != nil {
		if ended.Kind != "exclusive_writer_full_scan" || ended.SourceEpoch != m.SourceEpoch || ended.ManifestSHA256 != m.SHA256 || ended.FramesSHA256 != framesHash {
			return bad()
		}
		if _, err := parseTimestamp(ended.ObservedAt); err != nil {
			return bad()
		}
	}
	r := RecoveryReport{Scope: "P0_derived_source_lifecycle_only", SourceEpoch: m.SourceEpoch, SourceInstanceID: m.SourceInstanceID, ConfigGeneration: m.ConfigGeneration, ManifestSHA256: m.SHA256, FramesSHA256: framesHash, LastSourceSeq: life.LastSourceSeq, EpochFinality: life.EpochFinality, ScanStatus: "validated", HistoryQuality: "no_missing_final", RecordingStatus: "unverified", Flows: []RecoveryFlow{}}
	if ended != nil {
		copy := *ended
		r.WriterEnd = &copy
	}
	for _, f := range life.Flows {
		out := RecoveryFlow{FlowID: f.FlowID, OpenedEventID: f.OpenedEventID, State: f.State, Reason: f.Reason, LastCheckpoint: recoveryCounter(f.LastCheckpoint), Final: recoveryCounter(f.Final), GapDimensions: append([]string{}, f.GapDimensions...)}
		if f.TailBytes != nil {
			copy := *f.TailBytes
			out.TailBytes = &copy
		}
		if f.State == "pending" && ended != nil {
			out.State, out.Reason, out.TailBytes = "gap", "epoch_ended_without_final", nil
			out.GapDimensions = []string{"bytes", "route_or_setup", "window_checkpoints"}
			key, _ := json.Marshal([]string{"hoopa_missing_final_v1", m.SourceInstanceID, m.SourceEpoch, f.FlowID, f.OpenedEventID, out.Reason})
			sum := sha256.Sum256(key)
			out.ReconciliationID = "reconciliation:" + hex.EncodeToString(sum[:])
			// Run timestamps and scan cutoffs are observations, not fact content.
			payload, _ := json.Marshal(struct {
				ID         string
				Checkpoint *RecoveryCounter
				Boundary   string
				Tail       *uint64
			}{out.ReconciliationID, out.LastCheckpoint, "mihomo_proxy_stream_v1", nil})
			fact := sha256.Sum256(payload)
			out.FactSHA256 = hex.EncodeToString(fact[:])
		}
		if out.State == "gap" || len(out.GapDimensions) != 0 {
			r.HistoryQuality = "gap"
		} else if out.State == "pending" && r.HistoryQuality != "gap" {
			r.HistoryQuality = "pending"
		}
		r.Flows = append(r.Flows, out)
	}
	return r, nil
}
