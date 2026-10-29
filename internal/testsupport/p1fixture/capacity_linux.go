//go:build linux && amd64 && p1integration

package p1fixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/p1testhook"
)

type CapacityExisting struct {
	OperationID config.ID `json:"operation_id"`
	SHA256      string    `json:"sha256"`
}
type CapacitySeed struct {
	OperationID   config.ID `json:"operation_id"`
	RequestID     string    `json:"request_id"`
	RequestDigest string    `json:"request_digest"`
	SHA256        string    `json:"sha256"`
}
type CapacitySeedSummary struct {
	Kind         string             `json:"kind"`
	InitialCount int                `json:"initial_count"`
	Capacity     int                `json:"capacity"`
	FinalCount   int                `json:"final_count"`
	Existing     []CapacityExisting `json:"existing"`
	Seeds        []CapacitySeed     `json:"seeds"`
}

var ErrCapacitySeed = errors.New("P1_CAPACITY_SEED_FAILED")

// SeedCapacity creates explicitly labelled Prepared journal metadata fixtures,
// never successful API receipts or runtime effects. The actual Store writer
// lock serializes this one attempt; existing rows are read and left untouched.
func SeedCapacity(ctx context.Context, paths config.Paths) (summary CapacitySeedSummary, resultErr error) {
	fail := func() (CapacitySeedSummary, error) {
		if ctx != nil && ctx.Err() != nil {
			return CapacitySeedSummary{}, errors.Join(ErrCapacitySeed, ctx.Err())
		}
		return CapacitySeedSummary{}, ErrCapacitySeed
	}
	if ctx == nil || ctx.Err() != nil {
		return fail()
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		return fail()
	}
	defer func() {
		if store.Close() != nil {
			summary = CapacitySeedSummary{}
			resultErr = ErrCapacitySeed
		}
	}()
	active, err := store.ReadActive()
	if err != nil {
		return fail()
	}
	journal, err := control.NewJournal(store)
	if err != nil {
		return fail()
	}
	ids, err := store.ListOperationIDs()
	if err != nil {
		return fail()
	}
	summary = CapacitySeedSummary{Kind: "capacity-metadata-fixture", InitialCount: len(ids), Capacity: config.MaxOperationFiles, Existing: []CapacityExisting{}, Seeds: []CapacitySeed{}}
	occupied := map[config.ID]bool{}
	for _, id := range ids {
		if ctx.Err() != nil {
			return fail()
		}
		stored, err := journal.Get(id)
		if err != nil {
			return fail()
		}
		_, sha, err := store.ReadOperationFile(id)
		if err != nil || sha != stored.SHA256 {
			return fail()
		}
		summary.Existing = append(summary.Existing, CapacityExisting{id, sha})
		occupied[id] = true
	}
	if ctx.Err() != nil {
		return fail()
	}
	if p1testhook.Record("fixture", "capacity-seed-begin", "", "", "", false, map[string]int{"initial_count": summary.InitialCount, "capacity": summary.Capacity}) != nil {
		return fail()
	}
	remaining := config.MaxOperationFiles - len(ids)
	// Reserve names against the entire actual inventory before admission. A
	// preexisting metadata prefix is never overwritten or replayed as a new seed.
	for candidate := 0; len(summary.Seeds) < remaining; candidate++ {
		if ctx.Err() != nil {
			return fail()
		}
		requestID := fmt.Sprintf("capacity-metadata-%04d", candidate)
		raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": requestID, "operation": "service.stop", "params": map[string]any{"expected_revision": active.Revision}})
		if err != nil {
			return fail()
		}
		request, err := control.ParseRequest(raw)
		if err != nil {
			return fail()
		}
		if occupied[request.OperationID()] {
			continue
		}
		if ctx.Err() != nil {
			return fail()
		}
		stored, made, err := journal.Admit(request, control.Intent{BaseRevision: active.Revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}, json.RawMessage(`{"capacity_metadata_seed":true}`))
		if err != nil || !made || stored.Record.Phase != control.Prepared {
			return fail()
		}
		summary.Seeds = append(summary.Seeds, CapacitySeed{request.OperationID(), stored.Record.Identity.RequestID, stored.Record.Identity.RequestDigest, stored.SHA256})
		occupied[request.OperationID()] = true
	}
	if ctx.Err() != nil {
		return fail()
	}
	finalIDs, err := store.ListOperationIDs()
	if err != nil || len(finalIDs) != config.MaxOperationFiles {
		return fail()
	}
	summary.FinalCount = len(finalIDs)
	for _, row := range summary.Existing {
		if ctx.Err() != nil {
			return fail()
		}
		_, sha, err := store.ReadOperationFile(row.OperationID)
		if err != nil || sha != row.SHA256 {
			return fail()
		}
	}
	after, err := store.ReadActive()
	if err != nil || !reflect.DeepEqual(active, after) || ctx.Err() != nil {
		return fail()
	}
	if p1testhook.Record("fixture", "capacity-seed-end", "", "", "", false, map[string]int{"initial_count": summary.InitialCount, "capacity": summary.Capacity, "final_count": summary.FinalCount}) != nil {
		return fail()
	}
	if ctx.Err() != nil {
		return fail()
	}
	return summary, nil
}
