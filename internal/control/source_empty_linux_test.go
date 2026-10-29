//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func TestSourceEmptyUpdateFailsAndPreservesLatest(t *testing.T) {
	for _, prior := range []bool{false, true} {
		name := "first-empty"
		if prior {
			name = "existing-latest"
		}
		t.Run(name, func(t *testing.T) {
			c, p, u := updaterFixture(t, subscription.FetchOptions{})
			source := updaterFile(t, updaterProvider)
			var old sourceHistoryEntry
			if prior {
				raw := updaterRequest(t, c, "old-nonempty", "source-a", source, nil)
				updaterBegin(t, u, raw)
				old = updaterWait(t, u, raw)
			}
			base, _ := c.store.ReadActive()
			if err := os.WriteFile(source.Path, []byte(`{"proxies":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			beforeAssets, _ := os.ReadDir(filepath.Join(p.State, "source-assets"))
			raw := updaterRequest(t, c, "empty-update", "source-a", source, nil)
			updaterBegin(t, u, raw)
			entry := updaterWait(t, u, raw)
			if entry.Operation.Record.Phase != RolledBack || !entry.Receipt.Downloaded || entry.Receipt.Validated || entry.Receipt.Reason != "validation_failed" || entry.Receipt.AssetSHA256 != "" || entry.Receipt.SnapshotID != "" {
				t.Fatal("empty provider became usable candidate")
			}
			afterAssets, _ := os.ReadDir(filepath.Join(p.State, "source-assets"))
			if len(afterAssets) != len(beforeAssets)+1 {
				t.Fatal("empty provider asset was published; only attempt context may be new")
			}
			page, err := c.Sources(context.Background(), "")
			if err != nil || len(page.Sources) != 1 {
				t.Fatal(err)
			}
			row := page.Sources[0]
			if row.State != "failed" || row.Validated || !row.Downloaded || row.AppliedSnapshot != nil {
				t.Fatal("empty failure projected success/zero usage")
			}
			if prior {
				if row.Sequence != 2 || row.LatestSnapshot == nil || *row.LatestSnapshot != old.Receipt.SnapshotID || !row.Pending {
					t.Fatal("empty update discarded usable latest")
				}
			} else if row.Sequence != 1 || row.LatestSnapshot != nil || row.Pending {
				t.Fatal("first empty invented latest")
			}
			after, _ := c.store.ReadActive()
			if !reflect.DeepEqual(base, after) {
				t.Fatal("empty update changed active")
			}
			if err := os.WriteFile(source.Path, []byte(updaterProvider), 0600); err != nil {
				t.Fatal(err)
			}
			before := coordinatorTree(t, p)
			if replay, made, err := u.Update(nil, raw); err != nil || made || replay.SHA256 != entry.Operation.SHA256 || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("empty failure replay reran provider", err)
			}
		})
	}
}

func TestSourceEmptyHistoricalCandidateCannotBePromoted(t *testing.T) {
	for _, phase := range []Phase{Publishing, Committed} {
		t.Run(string(phase), func(t *testing.T) {
			c, p := sourceRecoverTestCoordinator(t)
			seed := sourceRecoverTestSeedProvider(t, c, "historical-empty", "source-a", updaterFile(t, `{"proxies":[]}`), phase, "", `{"proxies":[]}`)
			// This intentionally valid old source record binds an actually empty
			// parsed asset. A digest mismatch is not what makes the reproof fail.
			if len(seed.snapshot.Nodes) != 0 || seed.snapshot.Validate() != nil {
				t.Fatal("empty structural fixture invalid")
			}
			before := coordinatorTree(t, p)
			call, made, err := sourceRecoverTestCall(t, c, sourceRecoverTestRequest(t, c, seed.entry.Operation, "recover-empty"), nil)
			if !errors.Is(err, config.ErrReadUnavailable) || made || !reflect.DeepEqual(call, StoredOperation{}) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("historical empty recovery admitted/promoted", err)
			}
			if phase == Committed {
				if page, err := c.Sources(context.Background(), ""); !errors.Is(err, config.ErrReadUnavailable) || !reflect.DeepEqual(page, SourcePage{}) {
					t.Fatal("empty historical Latest became source success", err)
				}
				intent := sourcePlanTestIntent(t, sourcePlanTestCore(t), &seed)
				plan, err := c.PlanSources(context.Background(), planRaw(t, intent))
				if !errors.Is(err, config.ErrReadUnavailable) || plan.Digest() != "" {
					t.Fatal("empty historical Latest accepted by Plan", err)
				}
				if reason, field, ok := SourcePlanFailureDetails(err); !ok || reason != "source_candidate_empty" || field != "subscriptions[source-a]" {
					t.Fatal("empty historical candidate lost precise diagnostic")
				}
				if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
					t.Fatal("query repaired historical empty")
				}
			}
		})
	}
}
