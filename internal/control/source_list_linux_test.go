//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

func TestSourceListEmptyAndLatestFailureRemainReadOnly(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	before := coordinatorTree(t, p)
	page, err := c.Sources(context.Background(), "")
	if err != nil || page.Total != 0 || page.Sources == nil || page.NextCursor != nil {
		t.Fatal("empty inventory", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("list initialized settings")
	}
	initializePut(t, c, initializeSettings(t))
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	source := updaterFile(t, updaterProvider)
	first := updaterRequest(t, c, "list-good", "source-a", source, nil)
	updaterBegin(t, u, first)
	good := updaterWait(t, u, first)
	if err = os.WriteFile(source.Path, []byte(`{"invalid":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	second := updaterRequest(t, c, "list-bad", "source-a", source, nil)
	updaterBegin(t, u, second)
	updaterWait(t, u, second)
	before = coordinatorTree(t, p)
	page, err = c.Sources(context.Background(), "")
	if err != nil || len(page.Sources) != 1 {
		t.Fatal(err)
	}
	state := page.Sources[0]
	if state.Sequence != 2 || state.State != "failed" || !state.Downloaded || state.Validated || state.LatestSnapshot == nil || *state.LatestSnapshot != good.Receipt.SnapshotID || state.AppliedSnapshot != nil || !state.Pending || state.Scheduled || state.UpdateHours != nil || state.LastSuccess == nil || state.ErrorCode == nil || *state.ErrorCode != "validation_failed" {
		t.Fatal("attempt/latest/applied/schedule conflated")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("query changed storage")
	}
	if err = os.Remove(filepath.Join(p.State, "source-assets", good.Receipt.AssetSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	before = coordinatorTree(t, p)
	page, err = c.Sources(context.Background(), "")
	if !errors.Is(err, config.ErrReadUnavailable) || !reflect.DeepEqual(page, SourcePage{}) {
		t.Fatal("missing usable candidate became empty success", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("query repaired missing asset")
	}
}

func TestSourceListCursorBindsWholeViewAndIncludesAllRows(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	source := updaterFile(t, updaterProvider)
	for i := 0; i < sourcePageSize+1; i++ {
		id := fmt.Sprintf("source-%03d", i)
		raw := updaterRequest(t, c, id, id, source, nil)
		updaterBegin(t, u, raw)
		updaterWait(t, u, raw)
	}
	before := coordinatorTree(t, p)
	page, err := c.Sources(context.Background(), "")
	if err != nil || page.Total != 33 || len(page.Sources) != 32 || page.NextCursor == nil {
		t.Fatal("first bounded page", err)
	}
	raw, _ := json.Marshal(page)
	if len(raw) > MaxResultBytes-1024 {
		t.Fatal("page exceeded transport budget")
	}
	last, err := c.Sources(context.Background(), *page.NextCursor)
	if err != nil || last.Total != 33 || len(last.Sources) != 1 || last.Sources[0].SourceID != "source-032" || last.NextCursor != nil || last.ViewID != page.ViewID {
		t.Fatal("stable second page", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("pagination wrote")
	}
	for _, bad := range []string{"bad", *page.NextCursor + "=", ""} {
		if bad == "" {
			continue
		}
		if _, err = c.Sources(context.Background(), bad); err != ErrRequest {
			t.Fatal("invalid cursor accepted", err)
		}
	}
	newRaw := updaterRequest(t, c, "change-view", "source-000", source, nil)
	updaterBegin(t, u, newRaw)
	updaterWait(t, u, newRaw)
	before = coordinatorTree(t, p)
	if expired, err := c.Sources(context.Background(), *page.NextCursor); err != ErrSourceSnapshotExpired || !reflect.DeepEqual(expired, SourcePage{}) {
		t.Fatal("mixed pagination views", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("expired page wrote")
	}
}

func TestSourceListAppliedProvenanceIndependentFromLatest(t *testing.T) {
	c, p, u := updaterFixture(t, subscription.FetchOptions{})
	source := updaterFile(t, updaterProvider)
	raw := updaterRequest(t, c, "applied-source-old", "source-a", source, nil)
	updaterBegin(t, u, raw)
	old := updaterWait(t, u, raw)
	request, _ := ParseRequest(raw)
	params, _ := planObject(request.params)
	asset, err := c.store.ReadSourceAsset(old.Receipt.AssetSHA256)
	if err != nil {
		t.Fatal(err)
	}
	// A synthetic stopped active bundle exercises query provenance only. This
	// test does not claim a successful core Apply or launch.
	m, files, err := config.AssembleBundle("list-applied-generation", "query-fixture", hash([]byte("query-cap")), []config.BinaryReference{{Path: "/uninstalled/query-fixture", Version: "fixture", SHA256: hash([]byte("fixture"))}}, []config.BundleAsset{{Kind: "intent", Name: "intent.json", Data: params["intent"]}, {Kind: "provider", Name: "provider-" + hash([]byte("source-a")) + ".json", Data: asset}})
	if err != nil {
		t.Fatal(err)
	}
	sha, err := c.store.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := c.store.ReadActive()
	generation := m.Generation
	operation := old.Context.OperationID
	base.Generation, base.ManifestSHA256, base.OperationID = &generation, sha, &operation
	if err = c.store.PublishActive(base); err != nil {
		t.Fatal(err)
	}
	page, err := c.Sources(context.Background(), "")
	if err != nil || page.Sources[0].AppliedSnapshot == nil || page.Sources[0].Pending || !page.Sources[0].Scheduled || *page.Sources[0].UpdateHours != 1 {
		t.Fatal("applied snapshot or committed schedule missing", err)
	}
	if err = os.WriteFile(source.Path, []byte(`{"proxies":[{"name":"US replacement","type":"http","server":"replacement.example","port":443}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	newRaw := updaterRequest(t, c, "applied-source-new", "source-a", source, nil)
	updaterBegin(t, u, newRaw)
	latest := updaterWait(t, u, newRaw)
	before := coordinatorTree(t, p)
	page, err = c.Sources(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	state := page.Sources[0]
	if !state.Pending || *state.AppliedSnapshot != old.Receipt.SnapshotID || *state.LatestSnapshot != latest.Receipt.SnapshotID || *state.AppliedSnapshot == *state.LatestSnapshot {
		t.Fatal("new candidate rewrote applied identity")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("applied query wrote")
	}
}
