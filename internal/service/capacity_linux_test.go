//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

type capacityService struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func capacityStartService(t *testing.T, paths config.Paths) (ReadyInfo, *capacityService) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	running := &capacityService{cancel: cancel, done: make(chan struct{})}
	ready := make(chan ReadyInfo, 1)
	go func() {
		running.err = Run(ctx, paths, Options{LauncherArgs: []string{"never-launch-core-from-capacity-test"}, Ready: func(info ReadyInfo) error { ready <- info; return nil }})
		close(running.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-running.done:
			if running.err != nil && !errors.Is(running.err, context.Canceled) {
				t.Error(running.err)
			}
		case <-time.After(5 * time.Second):
			t.Error("capacity service cleanup timeout")
		}
	})
	select {
	case info := <-ready:
		return info, running
	case <-running.done:
		t.Fatal("capacity service before ready", running.err)
	case <-time.After(10 * time.Second):
		t.Fatal("capacity service readiness timeout")
	}
	return ReadyInfo{}, nil
}
func capacityCall(t *testing.T, paths config.Paths, raw []byte) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := Call(ctx, paths, raw)
	if err != nil {
		t.Fatal("public capacity API exchange", err)
	}
	return response
}
func capacityOriginal(t *testing.T, name string, raw []byte) {
	t.Helper()
	dir := os.Getenv("HOOPA_CAPACITY_ARTIFACTS")
	if dir == "" {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	n, err := file.Write(raw)
	if n != len(raw) {
		t.Fatal("short capacity original write")
	}
	if err = errors.Join(err, file.Sync(), file.Close()); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
		t.Fatal(err)
	}
}

// This is the actual public service/Unix API path and real journal capacity.
// Seed rows are explicitly Prepared storage-codec metadata, not successful
// product operations or runtime proof. Only the first service.stop is a genuine
// product receipt, obtained before filling slots and replayed unchanged later.
func TestAPIOperationCapacityPublicReplay(t *testing.T) {
	paths := apiPaths(t)
	first, originalService := capacityStartService(t, paths)
	oldRaw := apiRequest(t, "capacity-genuine-service-stop", "v1", "service.stop", map[string]any{"expected_revision": first.Revision})
	accepted := capacityCall(t, paths, oldRaw)
	envelope, code := apiResponse(t, accepted)
	if code != "" || string(envelope["ok"]) != "true" || ExitCode(accepted) != 0 {
		t.Fatal("initial actual service stop did not commit")
	}
	select {
	case <-originalService.done:
		if originalService.err != nil {
			t.Fatal(originalService.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original service did not stop")
	}
	old, err := control.ReadReplay(paths, oldRaw)
	if err != nil || old.Record.Phase != control.Committed || !bytes.Equal(old.Record.Result, accepted) {
		t.Fatal("genuine initial receipt not retained exactly", err)
	}
	capacityOriginal(t, "accepted-response.json", accepted)
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	journal, err := control.NewJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	var seedID config.ID
	for index := 1; index < config.MaxOperationFiles; index++ {
		raw := apiRequest(t, fmt.Sprintf("capacity-metadata-seed-%04d", index), "v1", "service.stop", map[string]any{"expected_revision": first.Revision})
		request, err := control.ParseRequest(raw)
		if err != nil {
			t.Fatal(err)
		}
		// Journal owns strict codec/CAS/fsync admission. No success envelope is forged
		// for these rows; they are never replayed as a product response.
		stored, made, err := journal.Admit(request, control.Intent{BaseRevision: first.Revision, CandidateDigest: strings.Repeat("a", 64), ImpactDigest: strings.Repeat("b", 64)}, json.RawMessage(`{"capacity_metadata_seed":true}`))
		if err != nil || !made || stored.Record.Phase != control.Prepared {
			t.Fatal("real journal metadata seed admission", index, err)
		}
		seedID = request.OperationID()
	}
	ids, err := store.ListOperationIDs()
	if err != nil || len(ids) != config.MaxOperationFiles {
		t.Fatal("actual inventory did not reach capacity", len(ids), err)
	}
	originalRaw, _, err := store.ReadOperationFile(old.Record.Identity.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	capacityOriginal(t, "accepted-operation.json", originalRaw)
	seedRaw, _, err := store.ReadOperationFile(seedID)
	if err != nil {
		t.Fatal(err)
	}
	capacityOriginal(t, "seed-metadata-example.json", seedRaw)
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	second, service := capacityStartService(t, paths)
	if second.Instance == first.Instance {
		t.Fatal("writer reopen reused originating service instance")
	}
	before := apiTree(t, paths)
	freshRaw := apiRequest(t, "capacity-fresh-rejected", "v1", "service.stop", map[string]any{"expected_revision": second.Revision})
	rejected := capacityCall(t, paths, freshRaw)
	envelope, code = apiResponse(t, rejected)
	if code != "OPERATION_CAPACITY_EXHAUSTED" || ExitCode(rejected) != 4 || string(envelope["ok"]) != "false" || string(envelope["data"]) != "null" {
		t.Fatal("actual full journal rejection mapping", code, ExitCode(rejected))
	}
	var failure struct {
		Retryable bool `json:"retryable"`
	}
	if json.Unmarshal(envelope["error"], &failure) != nil || failure.Retryable {
		t.Fatal("capacity incorrectly retryable")
	}
	replay := capacityCall(t, paths, oldRaw)
	if !bytes.Equal(replay, accepted) {
		t.Fatal("capacity or new writer changed old exact receipt")
	}
	changedRaw := apiRequest(t, "capacity-genuine-service-stop", "v1", "service.stop", map[string]any{"expected_revision": "different-revision"})
	reused := capacityCall(t, paths, changedRaw)
	_, code = apiResponse(t, reused)
	if code != "REQUEST_ID_REUSED" || ExitCode(reused) != 3 {
		t.Fatal("capacity hid reused identity conflict", code)
	}
	status := capacityCall(t, paths, apiRequest(t, "capacity-still-serving", "v1", "status.get", map[string]any{}))
	envelope, code = apiResponse(t, status)
	var data control.ServiceStatus
	if code != "" || json.Unmarshal(envelope["data"], &data) != nil || data.Service != "running" {
		t.Fatal("capacity rejection or old replay quiesced new service")
	}
	select {
	case <-service.done:
		t.Fatal("full-capacity API closed the current service")
	default:
	}
	if !reflect.DeepEqual(before, apiTree(t, paths)) {
		t.Fatal("capacity/replay/conflict/status implicitly changed or cleaned authority")
	}
	reader, err := config.OpenStore(paths, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	idsAfter, err := reader.ListOperationIDs()
	if err != nil || !reflect.DeepEqual(ids, idsAfter) {
		t.Fatal("capacity rejection changed identity inventory")
	}
	freshRequest, err := control.ParseRequest(freshRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = reader.ReadOperationFile(freshRequest.OperationID()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("capacity rejection admitted a fresh identity", err)
	}
	capacityOriginal(t, "capacity-response.json", rejected)
	capacityOriginal(t, "replayed-response.json", replay)
	capacityOriginal(t, "id-reused-response.json", reused)
	capacityOriginal(t, "status-response.json", status)
	raw, _ := json.Marshal(map[string]any{"identities": len(idsAfter), "metadata_seed_rows": config.MaxOperationFiles - 1, "genuine_committed_receipt_replayed_exactly": true, "fresh_id_not_admitted": true, "new_service_remained_running": true, "protected_tree_unchanged": true, "core_scene": false})
	capacityOriginal(t, "result.json", raw)
	t.Logf("actual public service path: %d identity slots (%d Prepared metadata seeds), fresh error OPERATION_CAPACITY_EXHAUSTED/exit4, genuine committed prior-writer receipt replay byte-identical; no implicit cleanup or core", len(idsAfter), config.MaxOperationFiles-1)
}

func TestAPIOperationCapacityWireRoundTrip(t *testing.T) {
	raw := apiRequest(t, "capacity-wire", "v1", "service.stop", map[string]any{"expected_revision": "revision"})
	response := Failure(raw, config.ErrOperationCapacity)
	client, peer := wirePair(t)
	done := make(chan error, 1)
	go func() {
		request, err := ReadRequest(wireContext(t), peer)
		if err == nil && !bytes.Equal(request, raw) {
			err = ErrWire
		}
		if err == nil {
			err = WriteResponse(wireContext(t), peer, response)
		}
		done <- err
	}()
	got, err := Exchange(wireContext(t), client, raw)
	if err != nil || !bytes.Equal(got, response) {
		t.Fatal("new capacity code rejected by real response frame", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, code := apiResponse(t, got)
	if code != "OPERATION_CAPACITY_EXHAUSTED" || ExitCode(got) != 4 {
		t.Fatal("capacity frame code/exit")
	}
	sum := sha256.Sum256(got)
	t.Logf("capacity response actual frame bytes=%d sha256=%s", len(got), hex.EncodeToString(sum[:]))
}
