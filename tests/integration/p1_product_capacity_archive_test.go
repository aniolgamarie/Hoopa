//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

// This rechecks retained originals from a FAILED actual C01 run. A passing
// offline projection does not relabel that original run or rerun the product.
func TestP1ArchivedCapacityProjection(t *testing.T) {
	root := os.Getenv("HOOPA_P1_CAPACITY_ARCHIVE")
	if root == "" || !filepath.IsAbs(root) {
		t.Fatal("explicit absolute retained capacity archive required")
	}
	entries, err := os.ReadDir(root)
	must(t, err)
	operations, evidence := "", ""
	opSerial, evidenceSerial := -1, -1
	traces := map[int]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			for suffix, destination := range map[string]*string{"-operations": &operations, "-evidence": &evidence} {
				if len(name) == 6+len(suffix) && strings.HasSuffix(name, suffix) {
					serial, err := strconv.Atoi(name[:6])
					if err != nil {
						t.Fatal("invalid capture serial")
					}
					if suffix == "-operations" && serial > opSerial {
						opSerial = serial
						*destination = name
					}
					if suffix == "-evidence" && serial > evidenceSerial {
						evidenceSerial = serial
						*destination = name
					}
				}
			}
			continue
		}
		if strings.HasPrefix(name, "complete-process-") && strings.HasSuffix(name, "-trace.json") {
			number := strings.TrimSuffix(strings.TrimPrefix(name, "complete-process-"), "-trace.json")
			n, err := strconv.Atoi(number)
			if err != nil || n < 0 || traces[n] != "" {
				t.Fatal("invalid process trace inventory")
			}
			traces[n] = filepath.Join(root, name)
		}
	}
	if operations == "" || evidence == "" || len(traces) == 0 {
		t.Fatal("actual capacity archive inputs missing")
	}
	var records []p1TraceRecord
	for n := 0; n < len(traces); n++ {
		path, ok := traces[n]
		if !ok {
			t.Fatal("incomplete process trace numbering")
		}
		raw, err := os.ReadFile(path)
		must(t, err)
		var process []p1TraceRecord
		must(t, json.Unmarshal(raw, &process))
		records = append(records, process...)
	}
	var prior productEvidence
	raw, err := os.ReadFile(filepath.Join(root, evidence, "evidence.json"))
	must(t, err)
	must(t, json.Unmarshal(raw, &prior))
	if len(prior.Operations) < 4 || len(prior.Operations) > 5 {
		t.Fatal("archive lacks full actual pre-seed operation inventory")
	}
	bundles := map[config.Generation]bundleSnapshot{}
	for _, op := range prior.Operations {
		for _, bundle := range []bundleSnapshot{op.Previous, op.Target} {
			if bundle.Generation != "" {
				if previous, ok := bundles[bundle.Generation]; ok && !reflect.DeepEqual(previous, bundle) {
					t.Fatal("inconsistent earlier bundle evidence")
				}
				bundles[bundle.Generation] = bundle
			}
		}
	}
	// Recheck the earlier accepted bundle DTOs against their actual archived
	// complete manifest/files; do not derive expected data from a live snapshot.
	var snapshot map[string]fileSnapshot
	raw, err = os.ReadFile(filepath.Join(root, evidence, "snapshot-files.json"))
	must(t, err)
	must(t, json.Unmarshal(raw, &snapshot))
	for generation, expected := range bundles {
		prefix := "state/bundles/" + p1SHA([]byte(generation)) + "/"
		read := func(name string) []byte {
			t.Helper()
			file, ok := snapshot[prefix+name]
			if !ok || file.Kind != "regular" || file.Mode != 0600 {
				t.Fatal("archived bundle inventory missing")
			}
			data, err := p1HistoryOriginal(originalRef{Path: filepath.Join(root, evidence, "snapshot", prefix+name), Bytes: file.Bytes, SHA256: file.SHA256}, config.MaxBundleBytes)
			must(t, err)
			return data
		}
		manifestRaw := read("manifest.json")
		var manifest config.BundleManifest
		must(t, json.Unmarshal(manifestRaw, &manifest))
		encoded, err := json.Marshal(manifest)
		must(t, err)
		if !bytes.Equal(append(encoded, '\n'), manifestRaw) {
			t.Fatal("archived manifest codec mismatch")
		}
		stored := config.StoredBundle{Manifest: manifest, ManifestSHA256: p1SHA(manifestRaw), Files: map[string][]byte{}}
		count := 0
		for path := range snapshot {
			if strings.HasPrefix(path, prefix) {
				count++
			}
		}
		if count != len(manifest.Files)+1 {
			t.Fatal("archived bundle omitted declared files")
		}
		for _, file := range manifest.Files {
			data := read(file.Name)
			if int64(len(data)) != file.Bytes || p1SHA(data) != file.SHA256 {
				t.Fatal("archived declared asset differs")
			}
			stored.Files[file.Name] = data
		}
		actual, err := p1StoredBundleProjection(stored, expected.NodeByEntry)
		must(t, err)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("earlier bundle projection differs from actual source")
		}
	}
	current := map[string]operationRecord{}
	durable := map[string]fileSnapshot{}
	authorityCopies := map[string]string{}
	rows, err := os.ReadDir(filepath.Join(root, operations))
	must(t, err)
	if len(rows) != config.MaxOperationFiles {
		t.Fatal("latest actual operation capture is not full")
	}
	for _, row := range rows {
		if row.IsDir() || !strings.HasSuffix(row.Name(), ".json") {
			t.Fatal("unexpected operation capture entry")
		}
		path := filepath.Join(root, operations, row.Name())
		raw, err := os.ReadFile(path)
		must(t, err)
		ref := originalRef{Path: path, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}
		raw, err = p1HistoryOriginal(ref, config.MaxOperationBytes)
		must(t, err)
		var source control.Operation
		must(t, json.Unmarshal(raw, &source))
		previous, err := p1HistoryBundle(source.Identity.Intent.PreviousGeneration, bundles)
		must(t, err)
		target, err := p1HistoryBundle(source.Identity.Intent.TargetGeneration, bundles)
		must(t, err)
		op, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: ref.SHA256}, raw, ref, previous, target)
		must(t, err)
		if string(source.Identity.OperationID)+".json" != row.Name() {
			t.Fatal("operation filename/source mismatch")
		}
		if _, duplicate := current[op.ID]; duplicate {
			t.Fatal("duplicate current operation")
		}
		current[op.ID] = op
		info, err := os.Lstat(path)
		must(t, err)
		alias := "capture/operations/" + row.Name()
		durable[alias] = fileSnapshot{Kind: "regular", Mode: uint32(info.Mode().Perm()), Bytes: info.Size(), SHA256: ref.SHA256, ModifiedNS: info.ModTime().UnixNano()}
		authorityCopies[path] = alias
	}
	for id, old := range prior.Operations {
		now, ok := current[id]
		if !ok || now.Original.SHA256 != old.Original.SHA256 {
			t.Fatal("actual prior operation missing or changed")
		}
		oldRaw, err := p1HistoryOriginal(old.Original, config.MaxOperationBytes)
		must(t, err)
		if !bytes.Equal(oldRaw, now.recordSource) {
			t.Fatal("prior operation raw mismatch")
		}
	}
	projection, err := p1ProjectTrace(records, current, bundles)
	must(t, err)
	if len(projection.FixtureSeeds) != config.MaxOperationFiles-len(prior.Operations) {
		t.Fatal("actual seed/prior inventory accounting mismatch")
	}
	coreSHA := ""
	for _, bundle := range bundles {
		for key, sha := range bundle.AssetSHA256 {
			if strings.HasPrefix(key, "binary:") {
				if coreSHA != "" && coreSHA != sha {
					t.Fatal("conflicting core pins")
				}
				coreSHA = sha
			}
		}
	}
	if coreSHA == "" {
		t.Fatal("missing actual core pin")
	}
	checked := productEvidence{DurableFiles: durable, Operations: current, OperationHistory: projection.History, FixtureSeeds: projection.FixtureSeeds, Boundaries: projection.Boundaries, Launches: projection.Launches, Signals: projection.Signals}
	must(t, validateOperationOrdering(checked, coreSHA))
	for _, point := range []string{"capacity-seed-begin", "capacity-seed-end"} {
		t.Run("missing-"+point, func(t *testing.T) {
			bad := make([]p1TraceRecord, 0, len(records))
			removed := 0
			for _, record := range records {
				if record.Event.Component == "fixture" && record.Event.Point == point {
					removed++
					continue
				}
				bad = append(bad, record)
			}
			if removed != 1 {
				t.Fatal("actual archive lacks one closed seed window")
			}
			if _, err := p1ReadCapacitySeeds(bad, current); err == nil {
				t.Fatal("unclosed actual seed window accepted")
			}
		})
	}
	seedIDs := make([]string, 0, len(projection.FixtureSeeds))
	for id := range projection.FixtureSeeds {
		seedIDs = append(seedIDs, id)
	}
	sort.Strings(seedIDs)
	if len(seedIDs) == 0 {
		t.Fatal("actual seed evidence absent")
	}
	for _, change := range []string{"business-admission", "missing-provenance"} {
		t.Run(change, func(t *testing.T) {
			bad := clonePolicyEvidence(checked)
			bad.FixtureSeeds = map[string]p1CapacitySeed{}
			for id, seed := range checked.FixtureSeeds {
				bad.FixtureSeeds[id] = seed
			}
			id := seedIDs[0]
			if change == "missing-provenance" {
				delete(bad.FixtureSeeds, id)
			} else {
				found := false
				for n, event := range bad.Boundaries {
					if event.OperationID == id && event.Name == "fixture-seed-durable" {
						bad.Boundaries[n].Name = "operation-durable"
						found = true
						break
					}
				}
				if !found {
					t.Fatal("seed boundary missing")
				}
			}
			if validateOperationOrdering(bad, coreSHA) == nil {
				t.Fatal("actual metadata granted business admission or lost provenance")
			}
		})
	}
	if output := os.Getenv("HOOPA_P1_CAPACITY_PROJECTION_RESULT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("absolute offline projection output required")
		}
		_, err := p1WriteOriginal(output, p1JSON(t, map[string]any{"kind": "offline-failed-C01-authority-copy-projection", "archive": root, "operation_capture": operations, "prior_evidence": evidence, "actual_original_to_ordering_alias": authorityCopies, "authority_copy_files": durable, "scope": "actual captured operation-file stat/size/SHA authority copies, not complete protected state snapshot; original actual C01 remains failed", "records": len(records), "operations": len(current), "seeds": len(projection.FixtureSeeds), "genuine_prior": len(prior.Operations), "history": len(projection.History), "boundaries": len(projection.Boundaries)}))
		must(t, err)
	}
	t.Logf("offline FAILED C01 archive: streams=%d records=%d current=%d genuine-prior=%d seeds=%d history=%d boundaries=%d launches=%d signals=%d capture=%s", len(traces), len(records), len(current), len(prior.Operations), len(projection.FixtureSeeds), len(projection.History), len(projection.Boundaries), len(projection.Launches), len(projection.Signals), operations)
}
