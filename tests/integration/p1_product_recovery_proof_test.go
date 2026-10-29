//go:build linux && amd64 && p1integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

// This is offline validation of an explicitly selected retained actual run. It
// neither reruns that run nor establishes completeness beyond its final capture.
func TestP1ArchivedRecoveryVerificationRoles(t *testing.T) {
	archive := os.Getenv("HOOPA_P1_HARNESS_ARCHIVE")
	if archive == "" || !filepath.IsAbs(archive) {
		t.Fatal("explicit absolute retained harness archive required")
	}
	entries, err := os.ReadDir(archive)
	must(t, err)
	latest, serial := "", -1
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) != len("000000-evidence") || !strings.HasSuffix(name, "-evidence") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, "-evidence"))
		if err == nil && n > serial {
			latest, serial = name, n
		}
	}
	if latest == "" {
		t.Fatal("retained evidence capture missing")
	}
	capture := filepath.Join(archive, latest)
	var records []p1TraceRecord
	var evidence productEvidence
	for name, destination := range map[string]any{"trace.json": &records, "evidence.json": &evidence} {
		raw, err := os.ReadFile(filepath.Join(capture, name))
		must(t, err)
		must(t, json.Unmarshal(raw, destination))
	}
	must(t, p1ValidateTraceRecords(records))
	if len(evidence.Operations) == 0 {
		t.Fatal("actual operation inventory missing")
	}
	bundles := map[config.Generation]bundleSnapshot{}
	coreSHA := ""
	for id, op := range evidence.Operations {
		raw, err := p1HistoryOriginal(op.Original, config.MaxOperationBytes)
		must(t, err)
		var source control.Operation
		must(t, json.Unmarshal(raw, &source))
		projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: op.Original.SHA256}, raw, op.Original, op.Previous, op.Target)
		must(t, err)
		comparison := projected
		comparison.recordSource = nil
		if id != projected.ID || !reflect.DeepEqual(comparison, op) {
			t.Fatal("captured operation differs from its actual original")
		}
		evidence.Operations[id] = projected
		for _, bundle := range []bundleSnapshot{op.Previous, op.Target} {
			if bundle.Generation == "" {
				continue
			}
			if previous, ok := bundles[bundle.Generation]; ok && !reflect.DeepEqual(previous, bundle) {
				t.Fatal("conflicting observed bundle projections")
			}
			bundles[bundle.Generation] = bundle
			for key, sha := range bundle.AssetSHA256 {
				if strings.HasPrefix(key, "binary:") {
					if coreSHA != "" && coreSHA != sha {
						t.Fatal("inconsistent retained core pin")
					}
					coreSHA = sha
				}
			}
		}
	}
	if coreSHA == "" {
		t.Fatal("retained core pin missing")
	}
	projected, err := p1ProjectTrace(records, evidence.Operations, bundles)
	must(t, err)
	evidence.Boundaries, evidence.OperationHistory = projected.Boundaries, projected.History
	evidence.Launches, evidence.Signals = projected.Launches, projected.Signals
	must(t, validateOperationOrdering(evidence, coreSHA))

	first, second, publishing := -1, -1, -1
	for i, event := range evidence.Boundaries {
		if event.Name == "recovery-target-verified" {
			if first < 0 {
				first = i
			} else if event.OperationID == evidence.Boundaries[first].OperationID && event.TargetOperationID == evidence.Boundaries[first].TargetOperationID && second < 0 {
				second = i
			}
		}
		if first >= 0 && publishing < 0 && event.Name == "active-published" && event.OperationID == evidence.Boundaries[first].TargetOperationID {
			publishing = i
		}
	}
	if first < 0 || second < 0 || publishing <= first || publishing >= second {
		t.Fatal("archive lacks original publication between distinct recovery proofs")
	}
	t.Run("drop-first-retain-later-proof", func(t *testing.T) {
		bad := clonePolicyEvidence(evidence)
		bad.Boundaries = append(bad.Boundaries[:first:first], bad.Boundaries[first+1:]...)
		if validateOperationOrdering(bad, coreSHA) == nil {
			t.Fatal("later recovery proof retroactively authorized original publication")
		}
	})
	for _, field := range []string{"target", "instance", "origin"} {
		t.Run("swap-"+field, func(t *testing.T) {
			bad := clonePolicyEvidence(evidence)
			proof := &bad.Boundaries[first]
			changed := false
			for _, other := range evidence.Boundaries {
				switch field {
				case "target":
					if other.OperationID != "" && other.OperationID != proof.TargetOperationID && other.OperationID != proof.OperationID {
						proof.TargetOperationID, changed = other.OperationID, true
					}
				case "instance":
					if other.Instance != nil && proof.Instance != nil && other.Instance.RunID != proof.Instance.RunID {
						instance := *other.Instance
						proof.Instance, changed = &instance, true
					}
				case "origin":
					if other.OriginOperationID != "" && other.OriginOperationID != proof.OriginOperationID {
						proof.OriginOperationID, changed = other.OriginOperationID, true
					}
				}
				if changed {
					break
				}
			}
			if !changed {
				t.Fatal("retained archive lacks distinct actual role for negative mutation")
			}
			if validateOperationOrdering(bad, coreSHA) == nil {
				t.Fatal("swapped recovery proof role accepted")
			}
		})
	}
	t.Logf("offline final capture=%s records=%d history=%d boundaries=%d operations=%d first-proof=%d publication=%d later-proof=%d", latest, len(records), len(projected.History), len(projected.Boundaries), len(evidence.Operations), evidence.Boundaries[first].Index, evidence.Boundaries[publishing].Index, evidence.Boundaries[second].Index)
}
