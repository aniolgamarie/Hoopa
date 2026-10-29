//go:build linux && amd64 && p1integration

package p1fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/p1testhook"
)

func capacityRequest(t *testing.T, id string, revision config.Revision) control.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": id, "operation": "service.stop", "params": map[string]any{"expected_revision": revision}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := control.ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func capacityIntent(revision config.Revision) control.Intent {
	return control.Intent{BaseRevision: revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}
}

// Real Store/Journal metadata admission, explicitly not a core or successful
// service-stop scenario. Four preexisting actual journal rows vary the capacity.
func TestSeedCapacityActualJournal(t *testing.T) {
	paths, err := config.ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	journal, err := control.NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	originals := map[config.ID][]byte{}
	pins := map[config.ID]string{}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("existing-metadata-%d", i)
		if i == 0 {
			id = "capacity-metadata-0000"
		}
		r := capacityRequest(t, id, active.Revision)
		row, made, err := journal.Admit(r, capacityIntent(active.Revision), json.RawMessage(`{"preexisting_metadata":true}`))
		if err != nil || !made {
			t.Fatal("preexisting metadata admission", err)
		}
		raw, sha, err := store.ReadOperationFile(r.OperationID())
		if err != nil || sha != row.SHA256 {
			t.Fatal("preexisting actual original", err)
		}
		originals[r.OperationID()] = raw
		pins[r.OperationID()] = sha
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []p1testhook.Event
	if err = p1testhook.Install(func(e p1testhook.Event) bool { mu.Lock(); events = append(events, e); mu.Unlock(); return false }); err != nil {
		t.Fatal(err)
	}
	summary, err := SeedCapacity(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Kind != "capacity-metadata-fixture" || summary.InitialCount != 4 || summary.Capacity != config.MaxOperationFiles || summary.FinalCount != config.MaxOperationFiles || len(summary.Existing) != 4 || len(summary.Seeds) != config.MaxOperationFiles-4 {
		t.Fatal("actual dynamic capacity summary incorrect")
	}
	raw, err := json.Marshal(summary)
	if err != nil || len(raw)+1 >= 1<<20 {
		t.Fatal("driver summary exceeds bound")
	}
	store, err = config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err = control.NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := store.ListOperationIDs()
	if err != nil || len(ids) != config.MaxOperationFiles {
		t.Fatal("actual capacity inventory", err)
	}
	for _, existing := range summary.Existing {
		raw, sha, err := store.ReadOperationFile(existing.OperationID)
		if err != nil || sha != pins[existing.OperationID] || sha != existing.SHA256 || !bytes.Equal(raw, originals[existing.OperationID]) {
			t.Fatal("preexisting journal original changed", err)
		}
	}
	seen := map[config.ID]bool{}
	for _, seed := range summary.Seeds {
		row, err := journal.Get(seed.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		r := capacityRequest(t, seed.RequestID, active.Revision)
		if seen[seed.OperationID] || originals[seed.OperationID] != nil || r.OperationID() != seed.OperationID || row.Record.Identity.RequestDigest != seed.RequestDigest || row.SHA256 != seed.SHA256 || row.Record.Identity.Intent != capacityIntent(active.Revision) || row.Record.Identity.Operation != "service.stop" || row.Record.Phase != control.Prepared || !bytes.Equal(row.Record.Result, []byte(`{"capacity_metadata_seed":true}`)) {
			t.Fatal("seed is not exact Prepared metadata")
		}
		seen[seed.OperationID] = true
	}
	fresh := capacityRequest(t, "capacity-unaccepted-fresh", active.Revision)
	if _, made, err := journal.Admit(fresh, capacityIntent(active.Revision), json.RawMessage(`{}`)); made || !errors.Is(err, config.ErrOperationCapacity) {
		t.Fatal("fresh actual admission did not reject full capacity", err)
	}
	after, err := store.ReadActive()
	if err != nil || !reflect.DeepEqual(active, after) {
		t.Fatal("metadata seeding changed active state", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := SeedCapacity(context.Background(), paths)
	if err != nil || again.InitialCount != config.MaxOperationFiles || len(again.Seeds) != 0 || again.FinalCount != config.MaxOperationFiles {
		t.Fatal("full capacity pass did not remain read-only", err)
	}
	mu.Lock()
	defer mu.Unlock()
	begin, end, published := 0, 0, 0
	for _, e := range events {
		if e.Component == "control" && (e.Point == "request-begin" || e.Point == "request-end") {
			t.Fatal("metadata falsely emitted product request scope")
		}
		if e.Component == "fixture" {
			if e.Injectable || e.OperationID != "" || e.RequestID != "" || e.RunID != "" {
				t.Fatal("fixture hook grants product scope")
			}
			var detail map[string]int
			if json.Unmarshal(e.Data, &detail) != nil {
				t.Fatal("non-count fixture hook")
			}
			if e.Point == "capacity-seed-begin" {
				begin++
				if len(detail) != 2 {
					t.Fatal("unexpected begin metadata")
				}
			}
			if e.Point == "capacity-seed-end" {
				end++
				if len(detail) != 3 {
					t.Fatal("unexpected end metadata")
				}
			}
		}
		if e.Component == "journal" && e.Point == "published" {
			published++
		}
	}
	if begin != 2 || end != 2 || published != len(summary.Seeds) {
		t.Fatal("actual journal/fixture hook counts inconsistent")
	}
	t.Logf("actual metadata capacity initial=%d seeds=%d final=%d summary_bytes=%d journal_published=%d", summary.InitialCount, len(summary.Seeds), summary.FinalCount, len(raw), published)
}

func TestSeedCapacityCanceledNoCreate(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent-home")
	paths, err := config.ResolvePaths(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SeedCapacity(ctx, paths); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled seed lost cause")
	}
	if _, err = os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled seed created state")
	}
	if _, err = SeedCapacity(nil, paths); !errors.Is(err, ErrCapacitySeed) {
		t.Fatal("nil context accepted")
	}
}
