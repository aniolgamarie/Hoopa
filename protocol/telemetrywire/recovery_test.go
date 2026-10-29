package telemetrywire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func recoveryFixture(t *testing.T) (Manifest, Lifecycle, string) {
	m := manifestFixture(t)
	changes := map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration}
	var state State
	commitState(t, &state, stateEvent(t, openedFields(), 1, changes))
	changes["upload_bytes"] = "9007199254740993"
	changes["download_bytes"] = "17"
	commitState(t, &state, stateEvent(t, counterFields(), 2, changes))
	return m, state.Snapshot(), strings.Repeat("c", 64)
}

func TestRecoveryPendingNeedsExplicitWriterEndEvidence(t *testing.T) {
	m, life, hash := recoveryFixture(t)
	r, err := ProjectRecovery(m, life, hash, nil)
	if err != nil || r.HistoryQuality != "pending" || r.WriterEnd != nil || r.RecordingStatus != "unverified" {
		t.Fatal("snapshot fabricated end/ready", err)
	}
	f := r.Flows[0]
	if f.State != "pending" || f.TailBytes != nil || f.Final != nil || f.ReconciliationID != "" || f.LastCheckpoint.UploadBytes != 9007199254740993 {
		t.Fatal("pending tail/CP changed")
	}
	raw, _ := json.Marshal(r)
	if !strings.Contains(string(raw), `"tail_bytes":null`) || strings.Contains(string(raw), `"receipt_seq"`) {
		t.Fatal("report is not independent of source/receipt positions")
	}
}

func TestRecoveryGapIdentitySurvivesRepeatedReopen(t *testing.T) {
	m, life, hash := recoveryFixture(t)
	before := life
	end := &WriterEndEvidence{"exclusive_writer_full_scan", m.SourceEpoch, m.SHA256, hash, "2026-10-23T01:02:03Z"}
	first, err := ProjectRecovery(m, life, hash, end)
	if err != nil {
		t.Fatal(err)
	}
	end.ObservedAt = "2026-10-24T01:02:03Z"
	again, err := ProjectRecovery(m, life, hash, end)
	if err != nil {
		t.Fatal(err)
	}
	a, b := first.Flows[0], again.Flows[0]
	if a.State != "gap" || a.Reason != "epoch_ended_without_final" || a.TailBytes != nil || a.Final != nil || a.ReconciliationID == "" || a.ReconciliationID != b.ReconciliationID || a.FactSHA256 != b.FactSHA256 {
		t.Fatal("reopen fabricated final or a new fact")
	}
	if !reflect.DeepEqual(life, before) || first.WriterEnd.ObservedAt == again.WriterEnd.ObservedAt {
		t.Fatal("projection aliases input/evidence")
	}
	life.Flows[0].LastCheckpoint.UploadBytes++
	conflict, err := ProjectRecovery(m, life, hash, end)
	if err != nil || conflict.Flows[0].ReconciliationID != a.ReconciliationID || conflict.Flows[0].FactSHA256 == a.FactSHA256 {
		t.Fatal("conflicting fact content cannot be detected")
	}
}

func TestRecoveryClosedPreservesFinalAndDimensionGap(t *testing.T) {
	m, life, hash := recoveryFixture(t)
	final := *life.Flows[0].LastCheckpoint
	final.DownloadBytes = 24
	zero := uint64(0)
	life.Flows[0].Final = &final
	life.Flows[0].TailBytes = &zero
	life.Flows[0].State = "closed"
	life.Flows[0].Reason = ""
	life.Flows[0].GapDimensions = []string{"route_or_setup"}
	end := &WriterEndEvidence{"exclusive_writer_full_scan", m.SourceEpoch, m.SHA256, hash, "2026-10-23T01:02:03Z"}
	r, err := ProjectRecovery(m, life, hash, end)
	if err != nil || r.HistoryQuality != "gap" || r.Flows[0].Final.UploadBytes != 9007199254740993 || r.Flows[0].Final.DownloadBytes != 24 || r.Flows[0].ReconciliationID != "" || r.Flows[0].TailBytes == nil || *r.Flows[0].TailBytes != 0 {
		t.Fatal("exact final added again or gap erased", err)
	}
}

func TestRecoveryRejectsMismatchedProvenance(t *testing.T) {
	m, life, hash := recoveryFixture(t)
	for _, end := range []*WriterEndEvidence{{"lock_only", m.SourceEpoch, m.SHA256, hash, "2026-10-23T01:02:03Z"}, {"exclusive_writer_full_scan", "wrong", m.SHA256, hash, "2026-10-23T01:02:03Z"}, {"exclusive_writer_full_scan", m.SourceEpoch, m.SHA256, strings.Repeat("0", 64), "2026-10-23T01:02:03Z"}} {
		if _, err := ProjectRecovery(m, life, hash, end); err == nil {
			t.Fatal("unbound end evidence accepted")
		}
	}
	life.ConfigGeneration = "wrong"
	if _, err := ProjectRecovery(m, life, hash, nil); err == nil {
		t.Fatal("manifest/frame mismatch accepted")
	}
}
