//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/testsupport/p1fixture"
)

var errP1CapacityAdapter = errors.New("P1_CAPACITY_WORKER_EVIDENCE_INVALID")

// This exact codec is the tagged worker's fixture summary, not a product API
// response. Roundtrip equality also rejects duplicate fields and extra values
// without applying the smaller product request canonicalization byte limit.
func p1DecodeCapacitySummary(raw []byte) (p1fixture.CapacitySeedSummary, error) {
	var summary p1fixture.CapacitySeedSummary
	if len(raw) == 0 || len(raw) > 1<<20 {
		return summary, errP1CapacityAdapter
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&summary) != nil {
		return summary, errP1CapacityAdapter
	}
	encoded, err := json.Marshal(summary)
	if err != nil || !bytes.Equal(raw, append(encoded, '\n')) {
		return summary, errP1CapacityAdapter
	}
	return summary, nil
}

func p1CheckCapacitySummary(summary p1fixture.CapacitySeedSummary, before, after map[string]operationRecord, seeds map[string]p1CapacitySeed, base, active config.ActivePointer) error {
	if summary.Kind != "capacity-metadata-fixture" || summary.InitialCount != len(before) || summary.Capacity != config.MaxOperationFiles || summary.FinalCount != config.MaxOperationFiles || len(after) != config.MaxOperationFiles || len(summary.Existing) != len(before) || len(summary.Seeds) != config.MaxOperationFiles-len(before) || len(seeds) != len(summary.Seeds) || !reflect.DeepEqual(base, active) {
		return errP1CapacityAdapter
	}
	seen := map[string]bool{}
	for _, row := range summary.Existing {
		id := string(row.OperationID)
		old, ok := before[id]
		now, present := after[id]
		if !ok || !present || seen[id] || row.SHA256 != old.Original.SHA256 || row.SHA256 != now.Original.SHA256 || !bytes.Equal(old.recordSource, now.recordSource) {
			return errP1CapacityAdapter
		}
		seen[id] = true
	}
	requests := map[string]bool{}
	for _, row := range summary.Seeds {
		id := string(row.OperationID)
		op, ok := after[id]
		proof, proved := seeds[id]
		if !ok || !proved || seen[id] || requests[row.RequestID] || row.RequestID == "" || before[id].ID != "" || row.SHA256 != op.Original.SHA256 || row.SHA256 != proof.RecordOriginal.SHA256 || row.RequestID != op.RequestID || row.RequestID != proof.RequestID || row.RequestDigest != op.RequestDigest || row.RequestDigest != proof.RequestDigest || proof.OperationID != id {
			return errP1CapacityAdapter
		}
		seen[id] = true
		requests[row.RequestID] = true
	}
	if len(seen) != len(after) {
		return errP1CapacityAdapter
	}
	return nil
}

func (h *p1ProductHarness) seedCapacity(ctx context.Context, r faultRequest) error {
	if ctx == nil || ctx.Err() != nil {
		return errP1CapacityAdapter
	}
	if r.Point != "request-history-capacity-fixture" || r.Failure != "fill-valid-retained-records" || r.Artifact != "" || r.Phase != "" || r.Cut != "" || r.Target != nil || r.Limit != 0 || h.capacityFault != nil {
		return errP1CapacityAdapter
	}
	before, _, beforeRefs, err := h.operations()
	if err != nil {
		return err
	}
	base, err := h.f.store.ReadActive()
	if err != nil {
		return err
	}
	baseRef, err := h.original("capacity-active-before", map[string]any{"kind": "actual-store-active-read", "active": base})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, beforeRefs...)
	h.extras = append(h.extras, baseRef)
	if err = h.stopManager(ctx, false); err != nil {
		return err
	}
	worker := h.f.launch(h.nextID("capacity-seed"), []string{"seed-capacity"}, nil, h.decide)
	h.manager = worker
	waitErr := worker.Wait(ctx)
	state := worker.cmd.ProcessState
	if state == nil {
		return errP1CapacityAdapter
	}
	waitRef, err := h.original("capacity-worker-wait", map[string]any{"kind": "original-direct-capacity-fixture-worker-wait", "pid": worker.PID(), "executable_path": h.f.probe, "stdout_path": worker.StdoutPath(), "exit_code": state.ExitCode(), "wait_status": state.Sys(), "trace_completed": worker.TraceError() == nil})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, waitRef)
	status, ok := state.Sys().(syscall.WaitStatus)
	if waitErr != nil || worker.TraceError() != nil || !state.Exited() || state.ExitCode() != 0 || !ok || !status.Exited() || status.ExitStatus() != 0 {
		return errP1CapacityAdapter
	}
	file, err := os.Open(worker.StdoutPath())
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	err = errors.Join(readErr, file.Close())
	if err != nil || len(raw) > 1<<20 {
		return errP1CapacityAdapter
	}
	stdoutRef := originalRef{Path: worker.StdoutPath(), Bytes: int64(len(raw)), SHA256: p1SHA(raw)}
	if _, err = p1HistoryOriginal(stdoutRef, 1<<20); err != nil {
		return err
	}
	summaryRef, err := p1WriteOriginal(filepath.Join(h.directory("capacity-summary"), "summary.json"), raw)
	if err != nil {
		return err
	}
	h.extras = append(h.extras, stdoutRef, summaryRef)
	summary, err := p1DecodeCapacitySummary(raw)
	if err != nil {
		return err
	}
	after, _, afterRefs, err := h.operations()
	if err != nil {
		return err
	}
	h.extras = append(h.extras, afterRefs...)
	active, err := h.f.store.ReadActive()
	if err != nil {
		return err
	}
	records, err := h.traceRecords(ctx)
	if err != nil {
		return err
	}
	if err = p1ValidateTraceRecords(records); err != nil {
		return err
	}
	seeds, err := p1ReadCapacitySeeds(records, after)
	if err != nil {
		return err
	}
	if err = p1CheckCapacitySummary(summary, before, after, seeds, base, active); err != nil {
		return err
	}
	activeRef, err := h.original("capacity-active-after", map[string]any{"kind": "actual-store-active-read", "active": active})
	if err != nil {
		return err
	}
	receipt, err := h.original("capacity-fixture", map[string]any{"kind": "actual-capacity-metadata-fixture", "worker_pid": worker.PID(), "executable_path": h.f.probe, "wait_original": waitRef, "stdout_original": stdoutRef, "summary_original": summaryRef, "active_before": baseRef, "active_after": activeRef, "initial_count": summary.InitialCount, "seed_count": len(seeds), "final_count": summary.FinalCount, "trace_validated": true})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, activeRef, receipt)
	h.capacityFault = &faultHit{Point: r.Point, Artifact: r.Artifact, Phase: r.Phase, Cut: r.Cut, Failure: r.Failure, Original: receipt}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return h.startManager(false)
}

// Synthetic summary checker fixtures only, not worker results or product API
// receipts. Real worker/provenance validation is exercised by root's C01 run.
func TestP1CapacityAdapterSummary(t *testing.T) {
	summary := p1fixture.CapacitySeedSummary{Kind: "capacity-metadata-fixture", InitialCount: 1, Capacity: config.MaxOperationFiles, FinalCount: config.MaxOperationFiles}
	before := map[string]operationRecord{"prior": {ID: "prior", Original: originalRef{SHA256: strings.Repeat("a", 64)}, recordSource: []byte("synthetic-prior")}}
	after := map[string]operationRecord{"prior": before["prior"]}
	seeds := map[string]p1CapacitySeed{}
	summary.Existing = []p1fixture.CapacityExisting{{OperationID: "prior", SHA256: strings.Repeat("a", 64)}}
	for i := 1; i < config.MaxOperationFiles; i++ {
		id := fmt.Sprintf("synthetic-%d", i)
		request := fmt.Sprintf("capacity-metadata-%04d", i)
		sha := strings.Repeat("b", 64)
		digest := strings.Repeat("c", 64)
		op := operationRecord{ID: id, RequestID: request, RequestDigest: digest, Original: originalRef{SHA256: sha}}
		after[id] = op
		seeds[id] = p1CapacitySeed{OperationID: id, RequestID: request, RequestDigest: digest, RecordOriginal: op.Original}
		summary.Seeds = append(summary.Seeds, p1fixture.CapacitySeed{OperationID: config.ID(id), RequestID: request, RequestDigest: digest, SHA256: sha})
	}
	base := config.ActivePointer{Version: 1, Revision: "synthetic-revision"}
	must(t, p1CheckCapacitySummary(summary, before, after, seeds, base, base))
	raw := append(p1JSON(t, summary), '\n')
	decoded, err := p1DecodeCapacitySummary(raw)
	must(t, err)
	if !reflect.DeepEqual(summary, decoded) {
		t.Fatal("worker summary codec changed")
	}
	for _, change := range []string{"kind", "initial", "capacity", "final", "existing-duplicate", "existing-sha", "existing-raw", "seed-duplicate", "seed-request", "seed-digest", "seed-sha", "seed-prior", "missing-provenance", "missing-current", "extra-current", "active"} {
		t.Run(change, func(t *testing.T) {
			s := summary
			s.Existing = append([]p1fixture.CapacityExisting(nil), summary.Existing...)
			s.Seeds = append([]p1fixture.CapacitySeed(nil), summary.Seeds...)
			a := map[string]operationRecord{}
			for id, op := range after {
				a[id] = op
			}
			p := map[string]p1CapacitySeed{}
			for id, seed := range seeds {
				p[id] = seed
			}
			active := base
			switch change {
			case "kind":
				s.Kind = "api-success"
			case "initial":
				s.InitialCount++
			case "capacity":
				s.Capacity--
			case "final":
				s.FinalCount--
			case "existing-duplicate":
				s.Existing = append(s.Existing, s.Existing[0])
			case "existing-sha":
				s.Existing[0].SHA256 = strings.Repeat("f", 64)
			case "existing-raw":
				op := a["prior"]
				op.recordSource = []byte("mutated")
				a["prior"] = op
			case "seed-duplicate":
				s.Seeds[1] = s.Seeds[0]
			case "seed-request":
				s.Seeds[0].RequestID = "other"
			case "seed-digest":
				s.Seeds[0].RequestDigest = strings.Repeat("f", 64)
			case "seed-sha":
				s.Seeds[0].SHA256 = strings.Repeat("f", 64)
			case "seed-prior":
				s.Seeds[0].OperationID = "prior"
			case "missing-provenance":
				delete(p, string(s.Seeds[0].OperationID))
			case "missing-current":
				delete(a, string(s.Seeds[0].OperationID))
			case "extra-current":
				a["extra"] = operationRecord{ID: "extra"}
			case "active":
				active.Revision = "changed"
			}
			if p1CheckCapacitySummary(s, before, a, p, base, active) == nil {
				t.Fatal("invalid synthetic summary accepted")
			}
		})
	}
}
func TestP1CapacityAdapterSummaryCodec(t *testing.T) {
	for name, raw := range map[string][]byte{"empty": nil, "invalid": []byte("{"), "api": []byte(`{"ok":true}` + "\n"), "duplicate": []byte(`{"kind":"x","kind":"x"}` + "\n"), "oversize": bytes.Repeat([]byte{' '}, (1<<20)+1), "trailing": []byte("{}\n{}\n")} {
		t.Run(name, func(t *testing.T) {
			if _, err := p1DecodeCapacitySummary(raw); err == nil {
				t.Fatal("invalid worker codec accepted")
			}
		})
	}
}
