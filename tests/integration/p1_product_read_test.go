//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/mihomo"
	"hoopa/internal/testsupport/p1fixture"
	wire "hoopa/protocol/corecontrol"
)

var errP1ProductRead = errors.New("P1_PRODUCT_READ_FAILED")

// p1ProjectBundle describes persisted expected material, never a live controller
// observation. Callers archive actual Store files separately when needed.
func p1ProjectBundle(store *config.Store, fixture *p1fixture.Fixture, expectedNodes map[string]string) (bundleSnapshot, error) {
	if store == nil || fixture == nil {
		return bundleSnapshot{}, errP1ProductRead
	}
	stored, err := store.ReadBundle(fixture.Generation())
	if err != nil {
		return bundleSnapshot{}, errP1ProductRead
	}
	material := fixture.Material()
	sort.Slice(material.Manifest.Files, func(i, j int) bool { return material.Manifest.Files[i].Name < material.Manifest.Files[j].Name })
	if !reflect.DeepEqual(stored.Manifest, material.Manifest) || !reflect.DeepEqual(stored.Files, material.Files) {
		return bundleSnapshot{}, errP1ProductRead
	}
	snapshot, err := wire.DecodeSnapshot(stored.Files[mihomo.SnapshotFile])
	if err != nil || !reflect.DeepEqual(snapshot, fixture.Snapshot()) {
		return bundleSnapshot{}, errP1ProductRead
	}
	return p1StoredBundleProjection(stored, expectedNodes)
}

// Rules are complete canonical wire.Rule projections in their original array
// order, not original config strings. expectedNodes is static test intent only.
func p1StoredBundleProjection(stored config.StoredBundle, expectedNodes map[string]string) (bundleSnapshot, error) {
	m := stored.Manifest
	if m.Generation == "" || !operationDigest(stored.ManifestSHA256) || len(m.Files) != len(stored.Files) || len(m.Binaries) == 0 || !operationDigest(m.CapabilitiesDigest) || m.CompilerVersion == "" {
		return bundleSnapshot{}, errP1ProductRead
	}
	snapshot, err := wire.DecodeSnapshot(stored.Files[mihomo.SnapshotFile])
	if err != nil || operationSHA(stored.Files["config.json"]) != snapshot.ConfigSHA256 || len(snapshot.Entries) != 2 || len(expectedNodes) != 2 {
		return bundleSnapshot{}, errP1ProductRead
	}
	out := bundleSnapshot{Generation: m.Generation, ManifestSHA256: stored.ManifestSHA256, ConfigSHA256: snapshot.ConfigSHA256, AssetSHA256: map[string]string{}, Selections: map[string]string{}, NodeByEntry: map[string]string{}, EntryAuthSHA256: map[string]string{}}
	for _, file := range m.Files {
		raw, ok := stored.Files[file.Name]
		key := "file:" + file.Kind + ":" + file.Name
		if !ok || int64(len(raw)) != file.Bytes || operationSHA(raw) != file.SHA256 || out.AssetSHA256[key] != "" {
			return bundleSnapshot{}, errP1ProductRead
		}
		out.AssetSHA256[key] = file.SHA256
	}
	for _, binary := range m.Binaries {
		key := "binary:" + binary.Path
		if !operationDigest(binary.SHA256) || out.AssetSHA256[key] != "" {
			return bundleSnapshot{}, errP1ProductRead
		}
		out.AssetSHA256[key] = binary.SHA256
	}
	out.AssetSHA256["capability:"+m.CompilerVersion] = m.CapabilitiesDigest
	nodes := map[string]bool{}
	for _, proxy := range snapshot.Proxies {
		nodes[proxy.Name] = true
		if proxy.Type == "Selector" {
			out.Selections[proxy.Name] = proxy.Selected
		}
	}
	for _, entry := range snapshot.Entries {
		node, ok := expectedNodes[entry.Name]
		if !ok || !strings.HasPrefix(node, "node:") || !nodes[node] || out.EntryAuthSHA256[entry.Name] != "" {
			return bundleSnapshot{}, errP1ProductRead
		}
		out.Entries = append(out.Entries, entry.Name)
		out.EntryAuthSHA256[entry.Name] = entry.AuthSHA256
		out.NodeByEntry[entry.Name] = node
	}
	if out.Entries[0] != "entry-a" || out.Entries[1] != "entry-b" {
		return bundleSnapshot{}, errP1ProductRead
	}
	for _, rule := range snapshot.Rules {
		raw, e := config.CanonicalJSON(operationEncode(rule))
		if e != nil {
			return bundleSnapshot{}, errP1ProductRead
		}
		out.OrderedRules = append(out.OrderedRules, string(raw))
	}
	return out, nil
}

// p1ReadOperations captures every validated current operation, without repair,
// history reconstruction, or authority over the writer. Observed concurrent
// changes fail the read; this is not an atomic multi-file Store transaction.
func p1ReadOperations(store *config.Store, captureDir string, bundles map[config.Generation]bundleSnapshot) (map[string]operationRecord, []originalRef, error) {
	fail := func() (map[string]operationRecord, []originalRef, error) { return nil, nil, errP1ProductRead }
	if store == nil {
		return fail()
	}
	info, err := os.Lstat(captureDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fail()
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return fail()
	}
	dir, err := os.OpenRoot(captureDir)
	if err != nil {
		return fail()
	}
	defer dir.Close()
	anchored, err := dir.Stat(".")
	if err != nil || !os.SameFile(info, anchored) {
		return fail()
	}
	ids, err := store.ListOperationIDs()
	if err != nil {
		return fail()
	}
	journal, err := control.NewJournal(store)
	if err != nil {
		return fail()
	}
	lookup := func(gen *config.Generation) (bundleSnapshot, error) {
		if gen == nil {
			return bundleSnapshot{}, nil
		}
		supplied, ok := bundles[*gen]
		if !ok || supplied.Generation != *gen {
			return bundleSnapshot{}, errP1ProductRead
		}
		stored, e := store.ReadBundle(*gen)
		if e != nil {
			return bundleSnapshot{}, errP1ProductRead
		}
		actual, e := p1StoredBundleProjection(stored, supplied.NodeByEntry)
		if e != nil || !reflect.DeepEqual(actual, supplied) {
			return bundleSnapshot{}, errP1ProductRead
		}
		return actual, nil
	}
	operations := map[string]operationRecord{}
	refs := make([]originalRef, 0, len(ids))
	for _, id := range ids {
		stored, e := journal.Get(id)
		if e != nil {
			return fail()
		}
		raw, sha, e := store.ReadOperationFile(id)
		if e != nil || sha != stored.SHA256 {
			return fail()
		}
		previous, e := lookup(stored.Record.Identity.Intent.PreviousGeneration)
		if e != nil {
			return fail()
		}
		target, e := lookup(stored.Record.Identity.Intent.TargetGeneration)
		if e != nil {
			return fail()
		}
		name := string(id) + ".json"
		ref := originalRef{Path: filepath.Join(captureDir, name), Bytes: int64(len(raw)), SHA256: sha}
		projected, e := projectOperationRecord(stored, raw, ref, previous, target)
		if e != nil {
			return fail()
		}
		f, e := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return fail()
		}
		n, writeErr := f.Write(raw)
		if n != len(raw) {
			writeErr = errors.Join(writeErr, io.ErrShortWrite)
		}
		writeErr = errors.Join(writeErr, f.Sync(), f.Close())
		if writeErr != nil {
			return fail()
		}
		operations[string(id)] = projected
		refs = append(refs, ref)
	}
	d, err := dir.Open(".")
	if err != nil {
		return fail()
	}
	if errors.Join(d.Sync(), d.Close()) != nil {
		return fail()
	}
	// No silent omission or acceptance of a record that advanced during capture.
	again, err := store.ListOperationIDs()
	if err != nil || !reflect.DeepEqual(ids, again) {
		return fail()
	}
	for _, id := range ids {
		_, sha, e := store.ReadOperationFile(id)
		if e != nil || sha != operations[string(id)].Original.SHA256 {
			return fail()
		}
	}
	return operations, refs, nil
}

// Tests below use real private Store files and finite expected material only.
// They start no core/service and do not implement the productHarness factory.
func p1ProductReadFixture(t *testing.T, keys ...string) (*config.Store, *p1fixture.Fixture, map[string]string) {
	t.Helper()
	t.Setenv("TMPDIR", "/tmp")
	key := "app-first"
	if len(keys) > 0 {
		key = keys[0]
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repository location")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	root := t.TempDir()
	paths, err := config.ResolvePaths(root, nil)
	must(t, err)
	store, err := config.OpenStore(paths, true)
	must(t, err)
	t.Cleanup(func() { must(t, store.Close()) })
	fixture, err := p1fixture.Load(repo, key, filepath.Join(root, "not-executed-core"), filepath.Join(root, "not-created-work"))
	must(t, err)
	raw, err := os.ReadFile(filepath.Join(repo, "testdata/runtime/basic-verified-bundle.json"))
	must(t, err)
	var descriptor struct {
		Bundles map[string]struct {
			ExpectedNodes map[string]string `json:"expected_node_by_entry"`
		} `json:"bundles"`
	}
	must(t, json.Unmarshal(raw, &descriptor))
	return store, fixture, descriptor.Bundles[key].ExpectedNodes
}
func TestP1ProductReadBundleExactAndDetached(t *testing.T) {
	for _, key := range []string{"app-first", "domain-first"} {
		t.Run(key, func(t *testing.T) {
			store, fixture, nodes := p1ProductReadFixture(t, key)
			material := fixture.Material()
			sha, err := store.PublishBundle(material.Manifest, material.Files)
			must(t, err)
			got, err := p1ProjectBundle(store, fixture, nodes)
			must(t, err)
			if got.ManifestSHA256 != sha || got.Generation != fixture.Generation() || len(got.AssetSHA256) != len(material.Manifest.Files)+len(material.Manifest.Binaries)+1 || len(got.OrderedRules) != len(fixture.Snapshot().Rules) {
				t.Fatal("incomplete persisted bundle")
			}
			nodes["entry-a"] = "changed"
			got.Entries[0] = "changed"
			got.AssetSHA256["changed"] = "changed"
			freshNodes := map[string]string{"entry-a": got.NodeByEntry["entry-a"], "entry-b": got.NodeByEntry["entry-b"]}
			fresh, err := p1ProjectBundle(store, fixture, freshNodes)
			must(t, err)
			if fresh.Entries[0] != "entry-a" || fresh.AssetSHA256["changed"] != "" {
				t.Fatal("projection aliases mutable inputs")
			}
			for _, rule := range fresh.OrderedRules {
				var decoded wire.Rule
				must(t, json.Unmarshal([]byte(rule), &decoded))
			}

		})
	}
}
func TestP1ProductReadBundleRejectsMismatch(t *testing.T) {
	for _, change := range []string{"file", "manifest", "snapshot", "missing-node", "foreign-entry", "unknown-node"} {
		t.Run(change, func(t *testing.T) {
			store, fixture, nodes := p1ProductReadFixture(t)
			material := fixture.Material()
			switch change {
			case "file":
				material.Files["config.json"] = append(material.Files["config.json"], ' ')
				for i := range material.Manifest.Files {
					if material.Manifest.Files[i].Name == "config.json" {
						material.Manifest.Files[i].Bytes = int64(len(material.Files["config.json"]))
						material.Manifest.Files[i].SHA256 = operationSHA(material.Files["config.json"])
					}
				}
			case "manifest":
				material.Manifest.CompilerVersion = "different"
			case "snapshot":
				snapshot := fixture.Snapshot()
				snapshot.Rules[0], snapshot.Rules[1] = snapshot.Rules[1], snapshot.Rules[0]
				material.Files[mihomo.SnapshotFile] = operationEncode(snapshot)
				for i := range material.Manifest.Files {
					if material.Manifest.Files[i].Name == mihomo.SnapshotFile {
						material.Manifest.Files[i].Bytes = int64(len(material.Files[mihomo.SnapshotFile]))
						material.Manifest.Files[i].SHA256 = operationSHA(material.Files[mihomo.SnapshotFile])
					}
				}
			case "missing-node":
				delete(nodes, "entry-b")
			case "foreign-entry":
				nodes["foreign"] = nodes["entry-a"]
				delete(nodes, "entry-a")
			case "unknown-node":
				nodes["entry-b"] = "node:" + strings.Repeat("f", 64)
			}
			_, err := store.PublishBundle(material.Manifest, material.Files)
			must(t, err)
			if _, err = p1ProjectBundle(store, fixture, nodes); err == nil {
				t.Fatal("mismatched bundle accepted")
			}
		})
	}
}

func p1ProductReadTree(t *testing.T, fixture *p1fixture.Fixture) map[string]fileSnapshot {
	t.Helper()
	root := filepath.Dir(fixture.Material().Manifest.Binaries[0].Path)
	out := map[string]fileSnapshot{}
	must(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := fileSnapshot{Kind: "directory", Mode: uint32(info.Mode()), ModifiedNS: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			item.Kind = "regular"
			item.Bytes = int64(len(raw))
			item.SHA256 = operationSHA(raw)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[relative] = item
		return nil
	}))
	return out
}
func TestP1ProductReadOperationsActualPreparedAndNoWrites(t *testing.T) {
	store, fixture, nodes := p1ProductReadFixture(t)
	material := fixture.Material()
	sha, err := store.PublishBundle(material.Manifest, material.Files)
	must(t, err)
	bundle, err := p1ProjectBundle(store, fixture, nodes)
	must(t, err)
	journal, err := control.NewJournal(store)
	must(t, err)
	var ids []config.ID
	for index, withGeneration := range []bool{false, true} {
		request, err := control.ParseRequest(operationEncode(map[string]any{"api_version": "v1", "request_id": []string{"pristine-prepared", "known-generation-prepared"}[index], "operation": "runtime.start", "params": map[string]any{"expected_revision": "base", "control_only": false}}))
		must(t, err)
		intent := control.Intent{BaseRevision: "base", CandidateDigest: sha, ImpactDigest: operationSHA(request.Parameters())}
		if withGeneration {
			generation := fixture.Generation()
			intent.PreviousGeneration = &generation
			intent.TargetGeneration = &generation
		}
		_, made, err := journal.Admit(request, intent, json.RawMessage(`{"driver_only":true}`))
		must(t, err)
		if !made {
			t.Fatal("actual admission not created")
		}
		ids = append(ids, request.OperationID())
	}
	must(t, store.Close())
	paths, err := config.ResolvePaths(filepath.Dir(fixture.Material().Manifest.Binaries[0].Path), nil)
	must(t, err)
	store, err = config.OpenStore(paths, false)
	must(t, err)
	t.Cleanup(func() { must(t, store.Close()) })
	before := p1ProductReadTree(t, fixture)
	capture := t.TempDir()
	must(t, os.Chmod(capture, 0700))
	operations, refs, err := p1ReadOperations(store, capture, map[config.Generation]bundleSnapshot{bundle.Generation: bundle})
	must(t, err)
	if len(operations) != 2 || len(refs) != 2 {
		t.Fatal("operation inventory omitted actual record")
	}
	for index, id := range ids {
		op := operations[string(id)]
		if op.Subtype != "unknown" || op.Phase != "prepared" {
			t.Fatal("early admission invented a classified effect")
		}
		if index == 0 {
			if !reflect.DeepEqual(op.Previous, bundleSnapshot{}) || !reflect.DeepEqual(op.Target, bundleSnapshot{}) {
				t.Fatal("pristine admission borrowed a bundle")
			}
		} else if !reflect.DeepEqual(op.Previous, bundle) || !reflect.DeepEqual(op.Target, bundle) {
			t.Fatal("real intent generation not resolved")
		}
		raw, sha, err := store.ReadOperationFile(id)
		must(t, err)
		captured, err := os.ReadFile(op.Original.Path)
		must(t, err)
		if !bytes.Equal(raw, captured) || op.Original.SHA256 != sha {
			t.Fatal("capture is not actual raw authority")
		}
		verifyOriginal(t, op.Original)
		info, err := os.Lstat(op.Original.Path)
		must(t, err)
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			t.Fatal("capture metadata")
		}
	}
	if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
		t.Fatal("readonly projection changed Store tree")
	}
	bundle.AssetSHA256["caller-mutated"] = "changed"
	if operations[string(ids[1])].Previous.AssetSHA256["caller-mutated"] != "" || operations[string(ids[1])].Target.AssetSHA256["caller-mutated"] != "" {
		t.Fatal("operation projection aliases supplied bundle maps")
	}
	delete(bundle.AssetSHA256, "caller-mutated")
	if _, _, err = p1ReadOperations(store, capture, map[config.Generation]bundleSnapshot{bundle.Generation: bundle}); err == nil {
		t.Fatal("existing originals overwritten")
	}
	if _, _, err = p1ReadOperations(store, t.TempDir(), nil); err == nil {
		t.Fatal("missing generation silently accepted")
	}
}

// These records use synthetic semantic evidence solely to exercise mapping of
// real Store/Journal codec records. They are not runtime proof or admissions
// through a product workflow; no core effect or factory acceptance is claimed.
func TestP1ProductReadOperationKindsPreserveIntent(t *testing.T) {
	store, fixture, nodes := p1ProductReadFixture(t)
	material := fixture.Material()
	_, err := store.PublishBundle(material.Manifest, material.Files)
	must(t, err)
	bundle, err := p1ProjectBundle(store, fixture, nodes)
	must(t, err)
	process := policyIdentity("read-original", 101)
	apply := policyRecord(t, "apply", "read-apply", config.ActivePointer{Version: 1, Revision: "empty", DesiredRuntime: "stopped"}, bundleSnapshot{}, bundle, process, nil)
	base := policyBase(apply, "running")
	records := []operationRecord{apply}
	for _, kind := range []string{"fresh-start", "restart", "adopt-running", "stop", "service-stop", "recover"} {
		kindBase := base
		if kind == "fresh-start" {
			kindBase.DesiredRuntime = "stopped"
		}
		records = append(records, policyRecord(t, kind, "read-"+kind, kindBase, bundle, bundle, process, &apply))
	}
	for _, op := range records {
		_, err = store.CompareAndSwapOperation(config.ID(op.ID), "", op.recordSource)
		must(t, err)
	}
	capture := t.TempDir()
	must(t, os.Chmod(capture, 0700))
	before := p1ProductReadTree(t, fixture)
	projected, refs, err := p1ReadOperations(store, capture, map[config.Generation]bundleSnapshot{bundle.Generation: bundle})
	must(t, err)
	if len(projected) != 7 || len(refs) != 7 {
		t.Fatal("classified history omitted")
	}
	for _, op := range records {
		got := projected[op.ID]
		if got.Subtype != op.Subtype || got.Operation != op.Operation || !reflect.DeepEqual(got.TargetRevision, op.TargetRevision) {
			t.Fatal("operation kind/nullable revision changed")
		}
		if op.Subtype == "apply" {
			if !reflect.DeepEqual(got.Previous, bundleSnapshot{}) || !reflect.DeepEqual(got.Target, bundle) {
				t.Fatal("apply bundle mapping")
			}
		} else if op.Subtype == "recover" {
			if !reflect.DeepEqual(got.Previous, bundle) || !reflect.DeepEqual(got.Target, bundleSnapshot{}) {
				t.Fatal("recovery call borrowed target bundle")
			}
		} else if !reflect.DeepEqual(got.Previous, bundle) || !reflect.DeepEqual(got.Target, bundle) {
			t.Fatal("lifecycle/service stop lost retained bundle")
		}
	}
	if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
		t.Fatal("operation projection wrote Store")
	}
}
func TestP1ProductReadRefusesMalformedInventoryAndCapture(t *testing.T) {
	for _, change := range []string{"malformed-record", "symlink-capture", "public-capture", "forged-bundle"} {
		t.Run(change, func(t *testing.T) {
			store, fixture, nodes := p1ProductReadFixture(t)
			material := fixture.Material()
			sha, err := store.PublishBundle(material.Manifest, material.Files)
			must(t, err)
			bundle, err := p1ProjectBundle(store, fixture, nodes)
			must(t, err)
			request, err := control.ParseRequest(operationEncode(map[string]any{"api_version": "v1", "request_id": "bad-read-driver", "operation": "runtime.start", "params": map[string]any{"expected_revision": "base", "control_only": false}}))
			must(t, err)
			journal, err := control.NewJournal(store)
			must(t, err)
			gen := fixture.Generation()
			_, _, err = journal.Admit(request, control.Intent{BaseRevision: "base", PreviousGeneration: &gen, TargetGeneration: &gen, CandidateDigest: sha, ImpactDigest: operationSHA(request.Parameters())}, json.RawMessage(`{}`))
			must(t, err)
			capture := t.TempDir()
			must(t, os.Chmod(capture, 0700))
			switch change {
			case "malformed-record":
				_, hash, err := store.ReadOperationFile(request.OperationID())
				must(t, err)
				_, err = store.CompareAndSwapOperation(request.OperationID(), hash, []byte(`{}`))
				must(t, err)
			case "symlink-capture":
				link := filepath.Join(t.TempDir(), "link")
				must(t, os.Symlink(capture, link))
				capture = link
			case "public-capture":
				must(t, os.Chmod(capture, 0755))
			case "forged-bundle":
				bundle.OrderedRules = []string{"forged"}
			}
			before := p1ProductReadTree(t, fixture)
			if _, _, err = p1ReadOperations(store, capture, map[config.Generation]bundleSnapshot{bundle.Generation: bundle}); err == nil {
				t.Fatal("unsafe or incomplete read accepted")
			}
			if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
				t.Fatal("failed read mutated Store")
			}
		})
	}
}

func TestP1ProductReadRolledBackKeepsAttemptedTarget(t *testing.T) {
	store, previousFixture, previousNodes := p1ProductReadFixture(t)
	oldMaterial := previousFixture.Material()
	_, err := store.PublishBundle(oldMaterial.Manifest, oldMaterial.Files)
	must(t, err)
	previous, err := p1ProjectBundle(store, previousFixture, previousNodes)
	must(t, err)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repository location")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	targetFixture, err := p1fixture.Load(repo, "domain-first", oldMaterial.Manifest.Binaries[0].Path, oldMaterial.Workdir)
	must(t, err)
	raw, err := os.ReadFile(filepath.Join(repo, "testdata/runtime/basic-verified-bundle.json"))
	must(t, err)
	var descriptor struct {
		Bundles map[string]struct {
			Nodes map[string]string `json:"expected_node_by_entry"`
		} `json:"bundles"`
	}
	must(t, json.Unmarshal(raw, &descriptor))
	targetMaterial := targetFixture.Material()
	_, err = store.PublishBundle(targetMaterial.Manifest, targetMaterial.Files)
	must(t, err)
	target, err := p1ProjectBundle(store, targetFixture, descriptor.Bundles["domain-first"].Nodes)
	must(t, err)
	initial := policyRecord(t, "apply", "read-rollback", config.ActivePointer{Version: 1, Revision: "empty", DesiredRuntime: "stopped"}, bundleSnapshot{}, target, policyIdentity("rollback-candidate", 101), nil)
	// Synthetic semantic body in a real Store: only mapping is under test.
	var record control.Operation
	must(t, json.Unmarshal(initial.recordSource, &record))
	generation := previous.Generation
	record.Identity.Intent.PreviousGeneration = &generation
	var evidence operationApplyEvidence
	must(t, json.Unmarshal(record.Steps[len(record.Steps)-1].Evidence, &evidence))
	oldOperation := config.ID("op-" + strings.Repeat("b", 64))
	oldRun := config.RunID("old-run")
	rollbackRun := config.RunID("rollback-run")
	evidence.Base = config.ActivePointer{Version: 1, Revision: "empty", Generation: &generation, ManifestSHA256: previous.ManifestSHA256, OperationID: &oldOperation, DesiredRuntime: "running"}
	evidence.OldRunID = &oldRun
	evidence.TargetStopped = true
	evidence.RollbackRunID = &rollbackRun
	evidence.RollbackWorkdir = "rollback-work"
	step := &record.Steps[len(record.Steps)-1]
	step.Phase = control.RollingBack
	step.Evidence = mustCanonical(operationEncode(evidence))
	final := *step
	final.Sequence++
	final.Phase = control.RolledBack
	record.Steps = append(record.Steps, final)
	record.Phase = control.RolledBack
	record.Result = final.Result
	record.BodySHA256 = operationSHA(operationEncode(struct {
		Request []byte         `json:"request"`
		Steps   []control.Step `json:"steps"`
	}{[]byte(record.Request), record.Steps}))
	_, err = store.CompareAndSwapOperation(record.Identity.OperationID, "", operationEncode(record))
	must(t, err)
	capture := t.TempDir()
	must(t, os.Chmod(capture, 0700))
	operations, _, err := p1ReadOperations(store, capture, map[config.Generation]bundleSnapshot{previous.Generation: previous, target.Generation: target})
	must(t, err)
	got := operations[string(record.Identity.OperationID)]
	if got.Phase != "rolled_back" || !reflect.DeepEqual(got.Previous, previous) || !reflect.DeepEqual(got.Target, target) || got.Target.Generation == got.Previous.Generation {
		t.Fatal("rollback replaced attempted target with restored bundle")
	}
}
