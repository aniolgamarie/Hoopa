//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// Named actual component scene only; this does not fill productHarness/factory.
func TestP1ProductProjectionCore(t *testing.T) {
	var prior int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	})
	var f *p1TracedCore
	// Registered before f.close, so this verifies the completed actual cleanup.
	t.Cleanup(func() {
		if f == nil {
			return
		}
		raw, err := os.ReadFile(filepath.Join(f.artifacts, "cleanup.json"))
		if err != nil {
			t.Error(err)
			return
		}
		var receipts []struct {
			CleanupSignalNeeded bool `json:"cleanup_signal_needed"`
			PIDFDExited         bool `json:"pidfd_exited"`
			ProcAbsent          bool `json:"proc_absent"`
		}
		if err = json.Unmarshal(raw, &receipts); err != nil {
			t.Error(err)
			return
		}
		if len(receipts) != 2 {
			t.Error("cleanup did not retain both original core handles")
		}
		for _, r := range receipts {
			if r.CleanupSignalNeeded || !r.PIDFDExited || !r.ProcAbsent {
				t.Error("core cleanup required a supplemental signal or lacked original exit/absence")
			}
		}
	})
	f = newP1TracedCore(t, "projection-core")
	seedProcess, seed := f.apply("app-first", "projection-seed")
	seedRun, seedHandle := f.pin()
	sourceBefore := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-fresh-read")
	bundle, err := p1ProjectBundle(f.store, f.fixtures["app-first"], fixedBundle(t, "app-first").ExpectedNodes)
	must(t, err)
	capture := filepath.Join(f.artifacts, "observations")
	must(t, os.Mkdir(capture, 0700))
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	observed, err := p1ObserveRun(ctx, f.store, seedRun, capture)
	must(t, err)
	p1ProjectionObserved(t, f, observed, seedRun, seedHandle.Identity(), bundle)
	sourceAfter := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-fresh-read")
	if !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatal("bundle/fresh-owner read changed actual durable tree")
	}
	if active := f.active(); active.Generation == nil || *active.Generation != bundle.Generation || active.ManifestSHA256 != bundle.ManifestSHA256 {
		t.Fatal("persisted bundle does not match actual committed pointer")
	}
	seedRaw, err := os.ReadFile(seedProcess.StdoutPath())
	must(t, err)
	seedRef, err := p1WriteOriginal(filepath.Join(f.artifacts, "seed-response.raw"), seedRaw)
	must(t, err)
	type response struct {
		RequestID string
		Raw       []byte
		Original  originalRef
		Recovery  bool
	}
	responses := []response{{seed.Record.Identity.RequestID, seedRaw, seedRef, false}}
	add := func(r p1CLIResult, recovery bool) {
		responses = append(responses, response{r.RequestID, r.Response, r.Original, recovery})
	}
	manager := f.service("manager", nil)
	add(f.call("projection-adopt", "runtime.start", map[string]any{"expected_revision": f.active().Revision, "control_only": false}), false)
	if !reflect.DeepEqual(f.active().Generation, &bundle.Generation) {
		t.Fatal("adoption changed configured bundle")
	}
	add(f.call("projection-recover-seed", "operation.recover", map[string]any{"expected_revision": f.active().Revision, "operation_id": seed.Record.Identity.OperationID}), true)
	add(f.call("projection-stop-first", "runtime.stop", map[string]any{"expected_revision": f.active().Revision, "accept_disruption": true}), false)
	must(t, seedHandle.WaitExited(ctx))
	stoppedBefore := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-stopped-read")
	failed, observeErr := p1ObserveRun(ctx, f.store, seedRun, capture)
	if observeErr == nil || !reflect.DeepEqual(failed, p1RunObservation{}) {
		t.Fatal("exited original became live proof or known-zero success")
	}
	stoppedAfter := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-stopped-read")
	if !reflect.DeepEqual(stoppedBefore, stoppedAfter) {
		t.Fatal("failed exited-core observation wrote durable state")
	}
	add(f.call("projection-start-second", "runtime.start", map[string]any{"expected_revision": f.active().Revision, "control_only": false}), false)
	secondRun, secondHandle := f.pin()
	if secondRun == seedRun || secondHandle.Identity() == seedHandle.Identity() {
		t.Fatal("new start reused original run/process")
	}
	secondBefore := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-second-read")
	secondObserved, err := p1ObserveRun(ctx, f.store, secondRun, capture)
	must(t, err)
	p1ProjectionObserved(t, f, secondObserved, secondRun, secondHandle.Identity(), bundle)
	secondAfter := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-second-read")
	if !reflect.DeepEqual(secondBefore, secondAfter) {
		t.Fatal("second fresh observation wrote durable state")
	}
	add(f.call("projection-stop-second", "runtime.stop", map[string]any{"expected_revision": f.active().Revision, "accept_disruption": true}), false)
	must(t, secondHandle.WaitExited(ctx))
	add(f.call("projection-service-stop", "service.stop", map[string]any{"expected_revision": f.active().Revision}), false)
	must(t, manager.Wait(ctx))
	f.archiveTrace("seed", seedProcess)
	f.archiveTrace("manager", manager)
	operationsDir := filepath.Join(f.artifacts, "operations")
	must(t, os.Mkdir(operationsDir, 0700))
	operationsBefore := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-operation-read")
	operations, originals, err := p1ReadOperations(f.store, operationsDir, map[config.Generation]bundleSnapshot{bundle.Generation: bundle})
	must(t, err)
	operationsAfter := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-operation-read")
	if !reflect.DeepEqual(operationsBefore, operationsAfter) {
		t.Fatal("complete operation projection wrote actual Store")
	}
	if len(operations) != 7 || len(originals) != 7 || len(responses) != 7 {
		t.Fatal("actual operation/response inventory incomplete")
	}
	kinds := map[string]int{}
	seen := map[string]bool{}
	for _, ref := range originals {
		verifyOriginal(t, ref)
	}
	for _, reply := range responses {
		var envelope struct {
			Data struct {
				OperationID string `json:"operation_id"`
				Outcome     string `json:"outcome"`
			} `json:"data"`
		}
		must(t, json.Unmarshal(reply.Raw, &envelope))
		actual, ok := operations[envelope.Data.OperationID]
		if !ok || seen[actual.ID] {
			t.Fatal("actual wire response missing or duplicated its durable operation")
		}
		seen[actual.ID] = true
		kinds[actual.Subtype]++
		result := operationResult{RequestID: reply.RequestID, WireOutcome: envelope.Data.Outcome, Operation: actual, ResponseOperationID: envelope.Data.OperationID, Response: reply.Raw, ResponseOriginal: reply.Original, Originals: []originalRef{reply.Original, actual.Original}}
		if reply.Recovery {
			target, ok := operations[string(seed.Record.Identity.OperationID)]
			if !ok {
				t.Fatal("recovery target omitted")
			}
			call := actual
			result.Operation = target
			result.RecoveryCall = &call
			if call.Operation != "operation.recover" || result.ResponseOperationID == target.ID {
				t.Fatal("recovery wire receipt was disguised as target")
			}
		}
		result.Outcome = logicalOperationOutcome(result)
		verifyOriginal(t, result.ResponseOriginal)
		must(t, validateOperationResult(result))
	}
	want := map[string]int{"apply": 1, "adopt-running": 1, "recover": 1, "stop": 2, "fresh-start": 1, "service-stop": 1}
	if !reflect.DeepEqual(kinds, want) || len(seen) != len(operations) {
		t.Fatal("actual complete history had an unexpected or omitted operation kind")
	}
	_, err = p1WriteOriginal(filepath.Join(f.artifacts, "result.json"), p1JSON(t, map[string]any{"first_run": seedRun, "second_run": secondRun, "first_identity": seedHandle.Identity(), "second_identity": secondHandle.Identity(), "operation_count": len(operations), "operation_kinds": kinds, "response_count": len(responses), "fresh_receipts": []originalRef{observed.Original, secondObserved.Original}, "operation_originals": originals, "durable_reads_unchanged": true, "stopped_observation_unknown": true}))
	must(t, err)
}

func p1ProjectionObserved(t *testing.T, f *p1TracedCore, got p1RunObservation, id config.RunID, native platform.ProcessIdentity, bundle bundleSnapshot) {
	t.Helper()
	expected := processIdentity{BootID: native.BootID, OSStartIdentity: strconv.FormatUint(native.StartTicks, 10), BinaryPath: native.BinaryPath, BinarySHA256: native.BinarySHA256, LaunchNonceSHA256: native.NonceSHA256, PrivateWorkdir: native.Workdir, PID: native.PID, Generation: bundle.Generation, RunID: id}
	if got.Identity != expected || got.Observation.RunID != id || got.Observation.Generation != bundle.Generation || got.Observation.ConfigSHA256 != bundle.ConfigSHA256 || got.Observation.Gate != "open" {
		t.Fatal("fresh connected-owner observation differs from independently pinned actual core")
	}
	intent, intentSHA, err := f.store.ReadRunIntent(id)
	must(t, err)
	_, birthSHA, err := f.store.ReadRunBirth(id)
	must(t, err)
	if got.IntentSHA256 != intentSHA || got.BirthSHA256 != birthSHA || got.Observation.BinaryVersion != intent.Binary.Version {
		t.Fatal("fresh receipt source digest/version mismatch")
	}
	verifyOriginal(t, got.Original)
	raw, err := os.ReadFile(got.Original.Path)
	must(t, err)
	var receipt struct {
		Version    int                      `json:"version"`
		Kind       string                   `json:"kind"`
		CaptureID  string                   `json:"capture_id"`
		ObservedAt time.Time                `json:"observed_at"`
		Identity   platform.ProcessIdentity `json:"identity"`
		IntentSHA  string                   `json:"intent_sha256"`
		BirthSHA   string                   `json:"birth_sha256"`
	}
	must(t, json.Unmarshal(raw, &receipt))
	if receipt.Version != 1 || receipt.Kind != "fresh-connected-owner-observation" || len(receipt.CaptureID) != 48 || receipt.ObservedAt.IsZero() || receipt.Identity != native || receipt.IntentSHA != intentSHA || receipt.BirthSHA != birthSHA {
		t.Fatal("generated observation receipt provenance mismatch")
	}
}
