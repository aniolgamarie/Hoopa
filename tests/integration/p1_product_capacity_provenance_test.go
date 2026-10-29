//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

var errP1CapacityProvenance = errors.New("P1_CAPACITY_SEED_PROVENANCE_INVALID")

// The caller authenticates complete trace records and current operation sources.
// This reads original metadata only; it never removes operations or grants them
// business effects. A fixture window is not a product request/admission scope.
func p1ReadCapacitySeeds(records []p1TraceRecord, current map[string]operationRecord) (map[string]p1CapacitySeed, error) {
	fail := func() (map[string]p1CapacitySeed, error) { return nil, errP1CapacityProvenance }
	marked := map[string]bool{}
	for id, op := range current {
		var source control.Operation
		if json.Unmarshal(op.recordSource, &source) != nil {
			return fail()
		}
		var result map[string]json.RawMessage
		if json.Unmarshal([]byte(source.Result), &result) != nil {
			return fail()
		}
		if _, present := result["capacity_metadata_seed"]; present {
			marked[id] = true
		}
	}
	seeds := map[string]p1CapacitySeed{}
	seenPublications := map[string]bool{}
	opened, closed := false, false
	peer, uid, initial := 0, 0, 0
	var begin, end []originalRef
	checkedRefs := func(refs []originalRef) bool {
		if len(refs) == 0 {
			return false
		}
		for _, ref := range refs {
			if _, err := p1HistoryOriginal(ref, config.MaxOperationBytes); err != nil {
				return false
			}
		}
		return true
	}
	for _, record := range records {
		e := record.Event
		fixture := e.Component == "fixture" && (e.Point == "capacity-seed-begin" || e.Point == "capacity-seed-end")
		if fixture {
			wrapper, err := p1TraceUnwrap(e.Data)
			if err != nil || wrapper.Injectable || e.OperationID != "" || e.RunID != "" || e.RequestID != "" || !checkedRefs(record.Originals) {
				return fail()
			}
			if e.Point == "capacity-seed-begin" {
				var detail struct {
					Initial  int `json:"initial_count"`
					Capacity int `json:"capacity"`
				}
				if opened || closed || !p1HistoryJSON(wrapper.Detail, &detail, "initial_count", "capacity") || detail.Initial < 0 || detail.Initial > config.MaxOperationFiles || detail.Capacity != config.MaxOperationFiles || record.PeerPID <= 0 {
					return fail()
				}
				opened = true
				peer, uid, initial = record.PeerPID, record.PeerUID, detail.Initial
				begin = append([]originalRef(nil), record.Originals...)
			} else {
				var detail struct {
					Initial  int `json:"initial_count"`
					Capacity int `json:"capacity"`
					Final    int `json:"final_count"`
				}
				if !opened || closed || record.PeerPID != peer || record.PeerUID != uid || !p1HistoryJSON(wrapper.Detail, &detail, "initial_count", "capacity", "final_count") || detail.Initial != initial || detail.Capacity != config.MaxOperationFiles || detail.Final != config.MaxOperationFiles || initial+len(seeds) != config.MaxOperationFiles || len(current) != config.MaxOperationFiles {
					return fail()
				}
				closed = true
				end = append([]originalRef(nil), record.Originals...)
			}
			continue
		}
		if opened && !closed {
			if record.PeerPID != peer || record.PeerUID != uid || !checkedRefs(record.Originals) {
				return fail()
			}
			if e.Component == "store" && e.Point == "checkpoint" {
				continue
			}
			if e.Component != "journal" || e.Point != "published" {
				return fail()
			}
			if seenPublications[e.OperationID] || record.OperationOriginal == nil {
				return fail()
			}
			raw, err := p1HistoryOriginal(*record.OperationOriginal, config.MaxOperationBytes)
			if err != nil {
				return fail()
			}
			op, ok := current[e.OperationID]
			if !ok || !marked[e.OperationID] || !bytes.Equal(raw, op.recordSource) || !p1HistorySameRef(*record.OperationOriginal, op.Original) {
				return fail()
			}
			var source control.Operation
			if json.Unmarshal(raw, &source) != nil {
				return fail()
			}
			projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: record.OperationOriginal.SHA256}, raw, *record.OperationOriginal, op.Previous, op.Target)
			if err != nil {
				return fail()
			}
			comparison := op
			comparison.Original = *record.OperationOriginal
			if !reflect.DeepEqual(projected, comparison) {
				return fail()
			}
			identity, intent := source.Identity, source.Identity.Intent
			request, err := control.ParseRequest([]byte(source.Request))
			if err != nil {
				return fail()
			}
			var params struct {
				Revision config.Revision `json:"expected_revision"`
			}
			suffix := strings.TrimPrefix(request.ID(), "capacity-metadata-")
			ordinal, parseErr := strconv.Atoi(suffix)
			if parseErr != nil || ordinal < 0 || fmt.Sprintf("capacity-metadata-%04d", ordinal) != request.ID() || request.Version() != "v1" || request.Operation() != "service.stop" || !p1HistoryJSON(request.Parameters(), &params, "expected_revision") || params.Revision != intent.BaseRevision || intent.TargetRevision != nil || intent.PreviousGeneration != nil || intent.TargetGeneration != nil || intent.CandidateDigest != strings.Repeat("a", 64) || intent.ImpactDigest != strings.Repeat("b", 64) {
				return fail()
			}
			if source.Version != 1 || source.Compacted || source.Phase != control.Prepared || len(source.Steps) != 1 || source.Steps[0].Sequence != 1 || source.Steps[0].Phase != control.Prepared || string(source.Steps[0].Evidence) != "{}" || string(source.Result) != `{"capacity_metadata_seed":true}` || !bytes.Equal([]byte(source.Steps[0].Result), []byte(source.Result)) || e.OperationID != string(identity.OperationID) || e.RequestID != identity.RequestID || e.RunID != "" {
				return fail()
			}
			wrapper, err := p1TraceUnwrap(e.Data)
			if err != nil || wrapper.Injectable {
				return fail()
			}
			var detail struct {
				SHA       string        `json:"record_sha256"`
				Phase     control.Phase `json:"phase"`
				Body      string        `json:"body_sha256"`
				Operation string        `json:"operation"`
			}
			if !p1HistoryJSON(wrapper.Detail, &detail, "record_sha256", "phase", "body_sha256", "operation") || detail.SHA != record.OperationOriginal.SHA256 || detail.Phase != source.Phase || detail.Body != source.BodySHA256 || detail.Operation != identity.Operation {
				return fail()
			}
			seeds[e.OperationID] = p1CapacitySeed{e.OperationID, identity.RequestID, identity.RequestDigest, *record.OperationOriginal, append([]originalRef(nil), begin...), append([]originalRef(nil), record.Originals...), nil}
		} else if e.Component == "journal" && e.Point == "published" {
			if _, isSeed := seeds[e.OperationID]; isSeed {
				return fail()
			}
		}
		if e.Component == "journal" && e.Point == "published" {
			seenPublications[e.OperationID] = true
		}
	}
	if opened && !closed {
		return fail()
	}
	if len(marked) != len(seeds) {
		return fail()
	}
	for id, seed := range seeds {
		if !marked[id] {
			return fail()
		}
		seed.EndOriginals = append([]originalRef(nil), end...)
		seeds[id] = seed
	}
	return seeds, nil
}

// Actual Journal codec/files, but synthetic trace DTOs: these are pure reader
// fixtures, not authenticated captures or product capacity acceptance.
func p1CapacityProvenanceFixture(t *testing.T) ([]p1TraceRecord, map[string]operationRecord) {
	t.Helper()
	paths, err := config.ResolvePaths(t.TempDir(), nil)
	must(t, err)
	store, err := config.OpenStore(paths, true)
	must(t, err)
	defer store.Close()
	journal, err := control.NewJournal(store)
	must(t, err)
	active, err := store.ReadActive()
	must(t, err)
	current := map[string]operationRecord{}
	var seed control.StoredOperation
	var seedRef originalRef
	for n := 0; n < config.MaxOperationFiles; n++ {
		name := fmt.Sprintf("prior-metadata-%04d", n)
		result := json.RawMessage(`{"prior_metadata":true}`)
		if n == config.MaxOperationFiles-1 {
			name = "capacity-metadata-0000"
			result = json.RawMessage(`{"capacity_metadata_seed":true}`)
		}
		request, err := control.ParseRequest(p1JSON(t, map[string]any{"api_version": "v1", "request_id": name, "operation": "service.stop", "params": map[string]any{"expected_revision": active.Revision}}))
		must(t, err)
		row, made, err := journal.Admit(request, control.Intent{BaseRevision: active.Revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}, result)
		must(t, err)
		if !made {
			t.Fatal("real codec fixture did not admit")
		}
		raw, sha, err := store.ReadOperationFile(request.OperationID())
		must(t, err)
		ref := p1CapacityProvenanceOriginal(t, raw)
		if ref.SHA256 != sha {
			t.Fatal("actual original digest mismatch")
		}
		op, err := projectOperationRecord(row, raw, ref, bundleSnapshot{}, bundleSnapshot{})
		must(t, err)
		current[op.ID] = op
		if n == config.MaxOperationFiles-1 {
			seed, seedRef = row, ref
		}
	}
	event := func(component, point string, detail any) p1TraceRecord {
		data := p1JSON(t, p1TraceWrapper{false, p1JSON(t, detail)})
		return p1TraceRecord{PeerPID: 123, PeerUID: os.Geteuid(), Event: p1SelectorEvent(component, point, data), Originals: []originalRef{p1CapacityProvenanceOriginal(t, data)}}
	}
	begin := event("fixture", "capacity-seed-begin", map[string]int{"initial_count": config.MaxOperationFiles - 1, "capacity": config.MaxOperationFiles})
	publish := event("journal", "published", map[string]any{"record_sha256": seed.SHA256, "phase": seed.Record.Phase, "body_sha256": seed.Record.BodySHA256, "operation": seed.Record.Identity.Operation})
	publish.Event.OperationID = string(seed.Record.Identity.OperationID)
	publish.Event.RequestID = seed.Record.Identity.RequestID
	publish.OperationOriginal = &seedRef
	publish.Originals = append(publish.Originals, seedRef)
	end := event("fixture", "capacity-seed-end", map[string]int{"initial_count": config.MaxOperationFiles - 1, "capacity": config.MaxOperationFiles, "final_count": config.MaxOperationFiles})
	return []p1TraceRecord{begin, publish, end}, current
}
func p1CapacityProvenanceOriginal(t *testing.T, raw []byte) originalRef {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic-original")
	must(t, os.WriteFile(path, raw, 0600))
	return originalRef{Path: path, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}
}

func TestP1CapacityProvenance(t *testing.T) {
	records, current := p1CapacityProvenanceFixture(t)
	seeds, err := p1ReadCapacitySeeds(records, current)
	must(t, err)
	if len(seeds) != 1 || len(current) != config.MaxOperationFiles {
		t.Fatal("provenance lost actual inventory")
	}
	for _, seed := range seeds {
		if len(seed.BeginOriginals) == 0 || len(seed.PublishOriginals) == 0 || len(seed.EndOriginals) == 0 || seed.RecordOriginal != *records[1].OperationOriginal {
			t.Fatal("actual source links missing")
		}
	}
	for _, change := range []string{"missing-end", "nested", "peer", "control", "runtime", "platform", "duplicate", "prior-publication", "later-publication", "original-sha", "missing-original", "request", "phase", "begin-count", "end-count", "extra-marker", "unknown-publication", "marker-outside", "no-window", "mutated-current", "noninjectable-fixture", "empty-refs"} {
		t.Run(change, func(t *testing.T) {
			copyRecords := make([]p1TraceRecord, len(records))
			for i, r := range records {
				copyRecords[i] = copyP1TraceRecord(r)
			}
			copyCurrent := map[string]operationRecord{}
			for id, op := range current {
				copyCurrent[id] = op
			}
			rewrite := func(index int, detail any, injectable bool) {
				copyRecords[index].Event.Data = p1JSON(t, p1TraceWrapper{injectable, p1JSON(t, detail)})
			}
			switch change {
			case "missing-end":
				copyRecords = copyRecords[:2]
			case "nested":
				copyRecords = append(copyRecords[:1:1], append([]p1TraceRecord{copyRecords[0]}, copyRecords[1:]...)...)
			case "peer":
				copyRecords[1].PeerPID++
			case "control", "runtime", "platform":
				copyRecords[1].Event.Component = change
				copyRecords[1].Event.Point = "unexpected"
			case "duplicate":
				copyRecords = append(copyRecords[:2:2], append([]p1TraceRecord{copyRecords[1]}, copyRecords[2:]...)...)
			case "prior-publication":
				copyRecords = append([]p1TraceRecord{copyRecords[1]}, copyRecords...)
			case "later-publication":
				copyRecords = append(copyRecords, copyRecords[1])
			case "original-sha":
				copyRecords[1].OperationOriginal.SHA256 = strings.Repeat("f", 64)
			case "missing-original":
				copyRecords[1].OperationOriginal = nil
			case "request":
				copyRecords[1].Event.RequestID = "other"
			case "phase":
				var w p1TraceWrapper
				must(t, json.Unmarshal(copyRecords[1].Event.Data, &w))
				var d map[string]any
				must(t, json.Unmarshal(w.Detail, &d))
				d["phase"] = "committed"
				rewrite(1, d, false)
			case "begin-count":
				rewrite(0, map[string]int{"initial_count": 0, "capacity": config.MaxOperationFiles}, false)
			case "end-count":
				rewrite(2, map[string]int{"initial_count": config.MaxOperationFiles - 1, "capacity": config.MaxOperationFiles, "final_count": 1}, false)
			case "extra-marker":
				for id, op := range copyCurrent {
					if id != copyRecords[1].Event.OperationID {
						var source control.Operation
						must(t, json.Unmarshal(op.recordSource, &source))
						var target control.Operation
						must(t, json.Unmarshal(current[copyRecords[1].Event.OperationID].recordSource, &target))
						source.Result = target.Result
						op.recordSource = p1JSON(t, source)
						copyCurrent[id] = op
						break
					}
				}
			case "unknown-publication":
				copyRecords[1].Event.OperationID = "unknown"
			case "marker-outside", "no-window":
				copyRecords = nil
			case "mutated-current":
				op := copyCurrent[copyRecords[1].Event.OperationID]
				op.recordSource = append([]byte(nil), op.recordSource...)
				op.recordSource = append(op.recordSource, ' ')
				copyCurrent[op.ID] = op
			case "noninjectable-fixture":
				rewrite(0, map[string]int{"initial_count": config.MaxOperationFiles - 1, "capacity": config.MaxOperationFiles}, true)
			case "empty-refs":
				copyRecords[0].Originals = nil
			}
			if _, err := p1ReadCapacitySeeds(copyRecords, copyCurrent); err == nil {
				t.Fatal("invalid synthetic provenance accepted")
			}
		})
	}
	t.Run("no-window-no-marker", func(t *testing.T) {
		empty, err := p1ReadCapacitySeeds(nil, map[string]operationRecord{})
		must(t, err)
		if len(empty) != 0 {
			t.Fatal("invented seeds")
		}
	})
	t.Logf("real journal codec inventory=%d synthetic-window-seeds=%d", len(current), len(seeds))
}
