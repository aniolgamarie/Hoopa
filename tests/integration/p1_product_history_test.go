//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"encoding/hex"
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
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/testsupport/p1fixture"
	"hoopa/internal/testsupport/p1trace"
)

var errP1TraceHistory = errors.New("P1_TRACE_HISTORY_INVALID")

func p1HistoryOriginal(ref originalRef, limit int64) ([]byte, error) {
	if ref.Path == "" || ref.Bytes < 0 || ref.Bytes > limit || !operationDigest(ref.SHA256) {
		return nil, errP1TraceHistory
	}
	fd, err := syscall.Open(ref.Path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errP1TraceHistory
	}
	file := os.NewFile(uintptr(fd), "retained-trace-original")
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, errP1TraceHistory
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Size() != ref.Bytes {
		file.Close()
		return nil, errP1TraceHistory
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	err = errors.Join(err, file.Close())
	if err != nil || int64(len(raw)) != ref.Bytes || p1SHA(raw) != ref.SHA256 {
		return nil, errP1TraceHistory
	}
	return raw, nil
}
func p1HistoryJSON(raw []byte, out any, fields ...string) bool {
	if _, err := config.CanonicalJSON(raw); err != nil {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != len(fields) {
		return false
	}
	for _, field := range fields {
		if _, ok := object[field]; !ok {
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil
}

type p1HistoryWire struct {
	Version  int           `json:"version"`
	StreamID string        `json:"stream_id"`
	Sequence uint64        `json:"sequence"`
	Event    p1trace.Event `json:"event"`
}
type p1HistoryPeer struct {
	PID     int    `json:"pid"`
	UID     int    `json:"uid"`
	GID     int    `json:"gid"`
	WireSHA string `json:"wire_sha256"`
}
type p1HistoryDecision struct {
	Version  int    `json:"version"`
	StreamID string `json:"stream_id"`
	Sequence uint64 `json:"sequence"`
	WireSHA  string `json:"wire_sha256"`
	Decision string `json:"decision"`
}

func p1HistorySameRef(a, b originalRef) bool { return a.Bytes == b.Bytes && a.SHA256 == b.SHA256 }
func p1HistoryRecord(record p1TraceRecord) (string, error) {
	fail := func() (string, error) { return "", errP1TraceHistory }
	if record.Sequence == 0 || record.Sequence > 9007199254740991 || record.PeerPID <= 0 || record.PeerUID != os.Geteuid() {
		return fail()
	}
	journal := record.Event.Component == "journal" && record.Event.Point == "published"
	if journal != (record.OperationOriginal != nil) {
		return fail()
	}
	for _, value := range []string{record.Event.Component, record.Event.Point, record.Event.OperationID, record.Event.RunID, record.Event.RequestID} {
		if len(value) > 256 || !utf8.ValidString(value) {
			return fail()
		}
	}
	if record.Event.Component == "" || record.Event.Point == "" || len(record.Event.Data) > 48<<10 {
		return fail()
	}
	wrapper, err := p1TraceUnwrap(record.Event.Data)
	if err != nil {
		return fail()
	}
	expectedDecision := map[p1TraceDecision]string{p1TraceContinue: "continue", p1TraceInjectError: "inject-error", p1TraceCrashManager: "crash-cut"}[record.Decision]
	if expectedDecision == "" || record.Decision == p1TraceInjectError && !wrapper.Injectable || (record.CrashOriginal != nil) != (record.Decision == p1TraceCrashManager) {
		return fail()
	}
	var wire p1HistoryWire
	var peer p1HistoryPeer
	var decision p1HistoryDecision
	wireCount, peerCount, decisionCount, operationCount, crashCount := 0, 0, 0, 0, 0
	wireSHA := ""
	for _, ref := range record.Originals {
		raw, e := p1HistoryOriginal(ref, config.MaxOperationBytes)
		if e != nil {
			return fail()
		}
		if record.OperationOriginal != nil && p1HistorySameRef(ref, *record.OperationOriginal) {
			operationCount++
			continue
		}
		if record.CrashOriginal != nil && p1HistorySameRef(ref, *record.CrashOriginal) {
			crashCount++
			var crash struct {
				PID        int    `json:"pid"`
				Sequence   uint64 `json:"sequence"`
				KillSent   bool   `json:"kill_sent"`
				WaitSignal string `json:"wait_signal"`
			}
			if !p1HistoryJSON(raw, &crash, "pid", "sequence", "kill_sent", "wait_signal") || crash.PID != record.PeerPID || crash.Sequence != record.Sequence || !crash.KillSent || crash.WaitSignal != "SIGKILL" {
				return fail()
			}
			continue
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return fail()
		}
		switch {
		case object["event"] != nil:
			wireCount++
			if !p1HistoryJSON(raw, &wire, "version", "stream_id", "sequence", "event") {
				return fail()
			}
			var envelope map[string]json.RawMessage
			if len(raw) > 65536 || json.Unmarshal(raw, &envelope) != nil || !operationFields(envelope["event"], "component", "point", "operation_id", "run_id", "request_id", "data") {
				return fail()
			}
			wireSHA = ref.SHA256
		case object["decision"] != nil:
			decisionCount++
			if !p1HistoryJSON(raw, &decision, "version", "stream_id", "sequence", "wire_sha256", "decision") {
				return fail()
			}
		case object["wire_sha256"] != nil:
			peerCount++
			if !p1HistoryJSON(raw, &peer, "pid", "uid", "gid", "wire_sha256") {
				return fail()
			}
		default:
			return fail()
		}
	}
	if wireCount != 1 || peerCount != 1 || decisionCount != 1 || operationCount != p1HistoryBool(journal) || crashCount != p1HistoryBool(record.CrashOriginal != nil) {
		return fail()
	}
	stream, e := hex.DecodeString(wire.StreamID)
	if e != nil || len(stream) != 16 || hex.EncodeToString(stream) != wire.StreamID || wire.Version != 1 || wire.Sequence != record.Sequence || !reflect.DeepEqual(wire.Event, record.Event) || peer.PID != record.PeerPID || peer.UID != record.PeerUID || peer.GID < 0 || peer.WireSHA != wireSHA || decision.Version != 1 || decision.StreamID != wire.StreamID || decision.Sequence != record.Sequence || decision.WireSHA != wireSHA || decision.Decision != expectedDecision {
		return fail()
	}
	if record.OperationOriginal != nil {
		if _, e := p1HistoryOriginal(*record.OperationOriginal, config.MaxOperationBytes); e != nil {
			return fail()
		}
	}
	if record.CrashOriginal != nil {
		if _, e := p1HistoryOriginal(*record.CrashOriginal, 4096); e != nil {
			return fail()
		}
	}
	return wire.StreamID, nil
}
func p1HistoryBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

// p1ValidateTraceRecords validates authenticated originals and each complete
// supplied stream's contiguous sequence from1. It preserves caller stream order.
// The caller supplies complete arrays: a trailing suffix/whole omitted stream
// cannot be detected from a records-only API without a final-count/EOF witness.
func p1ValidateTraceRecords(records []p1TraceRecord) error {
	type streamState struct {
		last     uint64
		pid, uid int
		crashed  bool
	}
	streams := map[string]streamState{}
	active := ""
	closed := map[string]bool{}
	for _, record := range records {
		stream, err := p1HistoryRecord(record)
		if err != nil {
			return errP1TraceHistory
		}
		if stream != active {
			if closed[stream] {
				return errP1TraceHistory
			}
			if active != "" {
				closed[active] = true
			}
			active = stream
		}
		state, ok := streams[stream]
		if !ok {
			state.pid, state.uid = record.PeerPID, record.PeerUID
		}
		if state.crashed || record.Sequence != state.last+1 || state.pid != record.PeerPID || state.uid != record.PeerUID {
			return errP1TraceHistory
		}
		state.last = record.Sequence
		state.crashed = record.Decision == p1TraceCrashManager
		streams[stream] = state
	}
	return nil
}
func p1HistoryBundle(gen *config.Generation, bundles map[config.Generation]bundleSnapshot) (bundleSnapshot, error) {
	if gen == nil {
		return bundleSnapshot{}, nil
	}
	bundle, ok := bundles[*gen]
	if !ok || bundle.Generation != *gen || !operationDigest(bundle.ManifestSHA256) || !operationDigest(bundle.ConfigSHA256) || len(bundle.AssetSHA256) == 0 || len(bundle.OrderedRules) == 0 || len(bundle.Entries) != 2 || len(bundle.EntryAuthSHA256) != 2 || len(bundle.NodeByEntry) != 2 {
		return bundleSnapshot{}, errP1TraceHistory
	}
	for _, sha := range bundle.AssetSHA256 {
		if !operationDigest(sha) {
			return bundleSnapshot{}, errP1TraceHistory
		}
	}
	return bundle, nil
}
func p1ReadTraceHistory(records []p1TraceRecord, current map[string]operationRecord, bundles map[config.Generation]bundleSnapshot) ([]operationVersion, error) {
	if p1ValidateTraceRecords(records) != nil {
		return nil, errP1TraceHistory
	}
	for id, op := range current {
		var source control.Operation
		if json.Unmarshal(op.recordSource, &source) != nil {
			return nil, errP1TraceHistory
		}
		projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: op.Original.SHA256}, op.recordSource, op.Original, op.Previous, op.Target)
		if err != nil || id != op.ID || !reflect.DeepEqual(projected, op) {
			return nil, errP1TraceHistory
		}
	}
	history := []operationVersion{}
	for _, record := range records {
		if record.Event.Component != "journal" || record.Event.Point != "published" {
			continue
		}
		raw, err := p1HistoryOriginal(*record.OperationOriginal, config.MaxOperationBytes)
		if err != nil {
			return nil, errP1TraceHistory
		}
		var source control.Operation
		if json.Unmarshal(raw, &source) != nil {
			return nil, errP1TraceHistory
		}
		wrapper, err := p1TraceUnwrap(record.Event.Data)
		if err != nil || wrapper.Injectable {
			return nil, errP1TraceHistory
		}
		var detail struct {
			RecordSHA string        `json:"record_sha256"`
			BodySHA   string        `json:"body_sha256"`
			Operation string        `json:"operation"`
			Phase     control.Phase `json:"phase"`
		}
		if !p1HistoryJSON(wrapper.Detail, &detail, "record_sha256", "body_sha256", "operation", "phase") || detail.RecordSHA != record.OperationOriginal.SHA256 || detail.BodySHA != source.BodySHA256 || detail.Operation != source.Identity.Operation || detail.Phase != source.Phase || record.Event.OperationID != string(source.Identity.OperationID) || record.Event.RequestID != source.Identity.RequestID {
			return nil, errP1TraceHistory
		}
		previous, err := p1HistoryBundle(source.Identity.Intent.PreviousGeneration, bundles)
		if err != nil {
			return nil, errP1TraceHistory
		}
		target, err := p1HistoryBundle(source.Identity.Intent.TargetGeneration, bundles)
		if err != nil {
			return nil, errP1TraceHistory
		}
		projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: record.OperationOriginal.SHA256}, raw, *record.OperationOriginal, previous, target)
		if err != nil {
			return nil, errP1TraceHistory
		}
		latest, ok := current[projected.ID]
		var final control.Operation
		if !ok || json.Unmarshal(latest.recordSource, &final) != nil || !reflect.DeepEqual(source.Identity, final.Identity) || !bytes.Equal(source.Request, final.Request) {
			return nil, errP1TraceHistory
		}
		history = append(history, operationVersion{Original: *record.OperationOriginal, Raw: raw, Previous: previous, Target: target})
	}
	return history, nil
}

// p1LoadProjectionArchive reads frozen actual component artifacts only. It
// validates the complete state/config snapshot inventory and never opens a
// writable Store, reconstructs a journal version, or starts a process.
func p1LoadProjectionArchive(t *testing.T, artifactRoot string) (records []p1TraceRecord, current map[string]operationRecord, bundles map[config.Generation]bundleSnapshot, durable map[string]fileSnapshot) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repository location")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(artifactRoot)), "evidence-index.json"))
	must(t, err)
	var evidence []originalRef
	must(t, json.Unmarshal(raw, &evidence))
	index := map[string]originalRef{}
	for _, ref := range evidence {
		path := ref.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(repo, path)
		}
		if index[path].Path != "" {
			t.Fatal("duplicate archived evidence entry")
		}
		ref.Path = path
		index[path] = ref
	}
	read := func(path string) []byte {
		t.Helper()
		ref, ok := index[path]
		if !ok {
			t.Fatal("archive file not bound by evidence index")
		}
		raw, err := p1HistoryOriginal(ref, config.MaxBundleBytes)
		must(t, err)
		return raw
	}
	snapshotRoot := filepath.Join(artifactRoot, "after-operation-read")
	raw = read(filepath.Join(artifactRoot, "after-operation-read-files.json"))
	must(t, json.Unmarshal(raw, &durable))
	archived := map[string][]byte{}
	for key, file := range durable {
		if filepath.IsAbs(key) || filepath.Clean(key) != key || strings.HasPrefix(key, "../") || !strings.HasPrefix(key, "state/") && !strings.HasPrefix(key, "config/") || file.Kind != "regular" || file.Mode != 0600 {
			t.Fatal("invalid durable archive inventory")
		}
		data := read(filepath.Join(snapshotRoot, key))
		if int64(len(data)) != file.Bytes || p1SHA(data) != file.SHA256 {
			t.Fatal("durable archive bytes differ from original inventory")
		}
		archived[key] = data
	}
	must(t, filepath.WalkDir(snapshotRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errP1TraceHistory
		}
		key, err := filepath.Rel(snapshotRoot, path)
		if err != nil {
			return err
		}
		if _, ok := archived[key]; !ok {
			return errP1TraceHistory
		}
		return nil
	}))
	bundles = map[config.Generation]bundleSnapshot{}
	for key, data := range archived {
		parts := strings.Split(key, "/")
		if len(parts) != 4 || parts[0] != "state" || parts[1] != "bundles" || parts[3] != "manifest.json" {
			continue
		}
		var manifest config.BundleManifest
		must(t, json.Unmarshal(data, &manifest))
		encoded, err := json.Marshal(manifest)
		must(t, err)
		if !bytes.Equal(append(encoded, '\n'), data) || len(manifest.Binaries) != 1 || p1SHA([]byte(manifest.Generation)) != parts[2] {
			t.Fatal("archived manifest codec/identity mismatch")
		}
		fixtureKey := strings.TrimPrefix(string(manifest.Generation), "fixture-")
		fixture, err := p1fixture.Load(repo, fixtureKey, manifest.Binaries[0].Path, "/not-executed-history-work")
		must(t, err)
		expected := fixture.Material()
		sort.Slice(expected.Manifest.Files, func(i, j int) bool { return expected.Manifest.Files[i].Name < expected.Manifest.Files[j].Name })
		stored := config.StoredBundle{Manifest: manifest, ManifestSHA256: p1SHA(data), Files: map[string][]byte{}}
		prefix := strings.TrimSuffix(key, "manifest.json")
		count := 0
		for path := range archived {
			if strings.HasPrefix(path, prefix) {
				count++
			}
		}
		if count != len(manifest.Files)+1 {
			t.Fatal("archived bundle omitted or added undeclared files")
		}
		for _, file := range manifest.Files {
			raw, ok := archived[prefix+file.Name]
			if !ok || int64(len(raw)) != file.Bytes || p1SHA(raw) != file.SHA256 {
				t.Fatal("archived declared asset mismatch")
			}
			stored.Files[file.Name] = raw
		}
		if !reflect.DeepEqual(manifest, expected.Manifest) || !reflect.DeepEqual(stored.Files, expected.Files) {
			t.Fatal("archived bundle differs from complete independent fixed material")
		}
		bundle, err := p1StoredBundleProjection(stored, fixedBundle(t, fixtureKey).ExpectedNodes)
		must(t, err)
		if _, ok := bundles[bundle.Generation]; ok {
			t.Fatal("duplicate archived generation")
		}
		bundles[bundle.Generation] = bundle
	}
	var result struct {
		OperationOriginals []originalRef `json:"operation_originals"`
	}
	must(t, json.Unmarshal(read(filepath.Join(artifactRoot, "result.json")), &result))
	current = map[string]operationRecord{}
	for _, ref := range result.OperationOriginals {
		raw, err := p1HistoryOriginal(ref, config.MaxOperationBytes)
		must(t, err)
		indexed := read(ref.Path)
		if !bytes.Equal(indexed, raw) {
			t.Fatal("operation capture not indexed exact bytes")
		}
		var source control.Operation
		must(t, json.Unmarshal(raw, &source))
		id := string(source.Identity.OperationID)
		file, ok := durable["state/operations/"+id+".json"]
		if !ok || file.SHA256 != ref.SHA256 || file.Bytes != ref.Bytes || !bytes.Equal(archived["state/operations/"+id+".json"], raw) {
			t.Fatal("captured current operation differs from durable inventory")
		}
		previous, err := p1HistoryBundle(source.Identity.Intent.PreviousGeneration, bundles)
		must(t, err)
		target, err := p1HistoryBundle(source.Identity.Intent.TargetGeneration, bundles)
		must(t, err)
		projected, err := projectOperationRecord(control.StoredOperation{Record: source, SHA256: ref.SHA256}, raw, ref, previous, target)
		must(t, err)
		if _, ok := current[id]; ok {
			t.Fatal("duplicate archived current operation")
		}
		current[id] = projected
	}
	for path := range durable {
		if !strings.HasPrefix(path, "state/operations/") || !strings.HasSuffix(path, ".json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "state/operations/"), ".json")
		if _, ok := current[id]; !ok {
			t.Fatal("current operation omitted from archived captures")
		}
	}
	for _, name := range []string{"seed-trace.json", "manager-trace.json"} {
		var stream []p1TraceRecord
		must(t, json.Unmarshal(read(filepath.Join(artifactRoot, name)), &stream))
		records = append(records, stream...)
	}
	must(t, p1ValidateTraceRecords(records))
	return records, current, bundles, durable
}

// Driver records below exercise validation only. Their transport/peer receipts
// are explicitly synthetic, never product traces or proof of a process effect.
func p1HistoryDriverRecord(t *testing.T, raw []byte, sequence uint64, stream string, edit func(*p1trace.Event)) p1TraceRecord {
	t.Helper()
	var source control.Operation
	must(t, json.Unmarshal(raw, &source))
	dir := t.TempDir()
	operation, err := p1WriteOriginal(filepath.Join(dir, "original-operation.json"), raw)
	must(t, err)
	detail := map[string]any{"record_sha256": operation.SHA256, "body_sha256": source.BodySHA256, "operation": source.Identity.Operation, "phase": source.Phase}
	event := p1trace.Event{Component: "journal", Point: "published", OperationID: string(source.Identity.OperationID), RequestID: source.Identity.RequestID, Data: p1JSON(t, p1TraceWrapper{false, p1JSON(t, detail)})}
	if edit != nil {
		edit(&event)
	}
	wire, err := p1WriteOriginal(filepath.Join(dir, "wire.json"), p1JSON(t, p1HistoryWire{1, stream, sequence, event}))
	must(t, err)
	peer, err := p1WriteOriginal(filepath.Join(dir, "peer.json"), p1JSON(t, p1HistoryPeer{os.Getpid(), os.Geteuid(), os.Getgid(), wire.SHA256}))
	must(t, err)
	decision, err := p1WriteOriginal(filepath.Join(dir, "decision.json"), p1JSON(t, p1HistoryDecision{1, stream, sequence, wire.SHA256, "continue"}))
	must(t, err)
	return p1TraceRecord{Event: event, Originals: []originalRef{wire, peer, decision, operation}, OperationOriginal: &operation, Sequence: sequence, PeerPID: os.Getpid(), PeerUID: os.Geteuid(), Decision: p1TraceContinue}
}
func p1HistoryDriverFixture(t *testing.T) ([]p1TraceRecord, map[string]operationRecord, map[config.Generation]bundleSnapshot) {
	t.Helper()
	store, fixture, nodes := p1ProductReadFixture(t)
	material := fixture.Material()
	sha, err := store.PublishBundle(material.Manifest, material.Files)
	must(t, err)
	bundle, err := p1ProjectBundle(store, fixture, nodes)
	must(t, err)
	gen := bundle.Generation
	base := config.ActivePointer{Version: 1, Revision: "history-base", Generation: &gen, ManifestSHA256: sha, DesiredRuntime: "stopped"}
	request, err := control.ParseRequest(p1Request(t, "history-codec-driver", "service.stop", map[string]any{"expected_revision": base.Revision}))
	must(t, err)
	journal, err := control.NewJournal(store)
	must(t, err)
	prepared, _, err := journal.Admit(request, control.Intent{BaseRevision: base.Revision, PreviousGeneration: &gen, TargetGeneration: &gen, CandidateDigest: operationSHA(mustCanonical(operationEncode(base))), ImpactDigest: operationSHA(request.Parameters())}, json.RawMessage(`{}`))
	must(t, err)
	first, _, err := store.ReadOperationFile(request.OperationID())
	must(t, err)
	evidence := operationServiceEvidence{Version: 1, Kind: "service-stop", Step: "service-stop-validated", Base: base, Instance: strings.Repeat("e", 48)}
	final, err := journal.Advance(request.OperationID(), prepared.SHA256, control.Validated, mustCanonical(operationEncode(evidence)), json.RawMessage(`{}`))
	must(t, err)
	last, _, err := store.ReadOperationFile(request.OperationID())
	must(t, err)
	ref, err := p1WriteOriginal(filepath.Join(t.TempDir(), "current.json"), last)
	must(t, err)
	current, err := projectOperationRecord(final, last, ref, bundle, bundle)
	must(t, err)
	records := []p1TraceRecord{p1HistoryDriverRecord(t, first, 1, strings.Repeat("a", 32), nil), p1HistoryDriverRecord(t, last, 2, strings.Repeat("a", 32), nil)}
	return records, map[string]operationRecord{current.ID: current}, map[config.Generation]bundleSnapshot{bundle.Generation: bundle}
}
func TestP1ProductHistoryActualJournalCodecAndOrder(t *testing.T) {
	records, current, bundles := p1HistoryDriverFixture(t)
	history, err := p1ReadTraceHistory(records, current, bundles)
	must(t, err)
	if len(history) != 2 {
		t.Fatal("actual codec history omitted versions")
	}
	var first, last control.Operation
	must(t, json.Unmarshal(history[0].Raw, &first))
	must(t, json.Unmarshal(history[1].Raw, &last))
	if first.Phase != control.Prepared || last.Phase != control.Validated {
		t.Fatal("history was reordered or rewritten to final phase")
	}
	third := p1HistoryDriverRecord(t, history[1].Raw, 3, strings.Repeat("a", 32), nil)
	history, err = p1ReadTraceHistory(append(records, third), current, bundles)
	must(t, err)
	if len(history) != 3 || !bytes.Equal(history[1].Raw, history[2].Raw) {
		t.Fatal("history duplicate version was deduplicated")
	}
}
func TestP1ProductHistoryRejectsForgery(t *testing.T) {
	for _, change := range []string{"hash", "phase", "body", "operation", "operation-id", "request-id", "missing-original", "missing-file", "other-event-original", "unfinished-decision", "gap", "reorder", "peer", "missing-current", "missing-bundle", "fake-immutable"} {
		t.Run(change, func(t *testing.T) {
			records, current, bundles := p1HistoryDriverFixture(t)
			raw, err := p1HistoryOriginal(*records[0].OperationOriginal, config.MaxOperationBytes)
			must(t, err)
			rewriteDetail := func(field string, value any) {
				records[0] = p1HistoryDriverRecord(t, raw, 1, strings.Repeat("a", 32), func(event *p1trace.Event) {
					w, e := p1TraceUnwrap(event.Data)
					must(t, e)
					var detail map[string]any
					must(t, json.Unmarshal(w.Detail, &detail))
					detail[field] = value
					event.Data = p1JSON(t, p1TraceWrapper{false, p1JSON(t, detail)})
				})
			}
			switch change {
			case "hash":
				rewriteDetail("record_sha256", strings.Repeat("f", 64))
			case "phase":
				rewriteDetail("phase", "applying")
			case "body":
				rewriteDetail("body_sha256", strings.Repeat("f", 64))
			case "operation":
				rewriteDetail("operation", "runtime.start")
			case "operation-id":
				records[0] = p1HistoryDriverRecord(t, raw, 1, strings.Repeat("a", 32), func(event *p1trace.Event) { event.OperationID = "op-" + strings.Repeat("f", 64) })
			case "request-id":
				records[0] = p1HistoryDriverRecord(t, raw, 1, strings.Repeat("a", 32), func(event *p1trace.Event) { event.RequestID = "different" })
			case "missing-original":
				records[0].OperationOriginal = nil
			case "missing-file":
				copied := *records[0].OperationOriginal
				copied.Path = filepath.Join(t.TempDir(), "missing")
				records[0].OperationOriginal = &copied
			case "other-event-original":
				records[0] = p1HistoryDriverRecord(t, raw, 1, strings.Repeat("a", 32), func(event *p1trace.Event) { event.Component = "control"; event.Point = "request-begin" })
			case "unfinished-decision":
				records[0].Decision = ""
			case "gap":
				records[1] = p1HistoryDriverRecord(t, recordsCurrentRaw(t, current), 3, strings.Repeat("a", 32), nil)
			case "reorder":
				records[0], records[1] = records[1], records[0]
			case "peer":
				records[0].PeerPID++
			case "missing-current":
				current = map[string]operationRecord{}
			case "missing-bundle":
				bundles = nil
			case "fake-immutable":
				var source control.Operation
				must(t, json.Unmarshal(recordsCurrentRaw(t, current), &source))
				var evidence operationServiceEvidence
				must(t, json.Unmarshal(source.Steps[len(source.Steps)-1].Evidence, &evidence))
				id := config.ID("op-" + strings.Repeat("f", 64))
				evidence.Base.OperationID = &id
				source.Identity.Intent.CandidateDigest = operationSHA(mustCanonical(operationEncode(evidence.Base)))
				source.Steps[len(source.Steps)-1].Evidence = mustCanonical(operationEncode(evidence))
				source.BodySHA256 = operationSHA(operationEncode(struct {
					Request []byte         `json:"request"`
					Steps   []control.Step `json:"steps"`
				}{[]byte(source.Request), source.Steps}))
				records = []p1TraceRecord{p1HistoryDriverRecord(t, operationEncode(source), 1, strings.Repeat("a", 32), nil)}
			}
			if _, err := p1ReadTraceHistory(records, current, bundles); err == nil {
				t.Fatal("forged or incomplete history accepted")
			}
		})
	}
}
func recordsCurrentRaw(t *testing.T, current map[string]operationRecord) []byte {
	t.Helper()
	for _, op := range current {
		return append([]byte(nil), op.recordSource...)
	}
	t.Fatal("driver current missing")
	return nil
}
func TestP1ProductHistoryStreamBoundary(t *testing.T) {
	records, current, bundles := p1HistoryDriverFixture(t)
	raw := recordsCurrentRaw(t, current)
	other := p1HistoryDriverRecord(t, raw, 1, strings.Repeat("b", 32), nil)
	if _, err := p1ReadTraceHistory(append(records, other), current, bundles); err != nil {
		t.Fatal("independent stream sequence did not reset", err)
	}
	returned := p1HistoryDriverRecord(t, raw, 3, strings.Repeat("a", 32), nil)
	if p1ValidateTraceRecords(append(append(records, other), returned)) == nil {
		t.Fatal("returned to closed stream")
	}
}
func TestP1ProductHistoryFrozenActualArchives(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("repository location")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	for _, round := range []string{"actual-01", "race-01"} {
		t.Run(round, func(t *testing.T) {
			root := filepath.Join(repo, "private/p1-trace-preparation/product-projection-core", round, "artifacts/projection-core")
			records, current, bundles, _ := p1LoadProjectionArchive(t, root)
			history, err := p1ReadTraceHistory(records, current, bundles)
			must(t, err)
			journals := 0
			for _, record := range records {
				if record.Event.Component == "journal" && record.Event.Point == "published" {
					journals++
				}
			}
			if len(current) != 7 || len(history) != journals || journals == 0 {
				t.Fatal("frozen actual journal history incomplete")
			}
			t.Logf("offline frozen %s: %d actual records, %d independently captured journal originals, %d current operations; no new core execution", round, len(records), len(history), len(current))
		})
	}
}

func TestP1ProductHistoryMissingWireEventField(t *testing.T) {
	records, _, _ := p1HistoryDriverFixture(t)
	record := records[0]
	raw, err := p1HistoryOriginal(record.Originals[0], config.MaxOperationBytes)
	must(t, err)
	var envelope map[string]json.RawMessage
	must(t, json.Unmarshal(raw, &envelope))
	var event map[string]json.RawMessage
	must(t, json.Unmarshal(envelope["event"], &event))
	delete(event, "run_id")
	envelope["event"] = p1JSON(t, event)
	ref, err := p1WriteOriginal(filepath.Join(t.TempDir(), "omitted-event-field.json"), p1JSON(t, envelope))
	must(t, err)
	record.Originals[0] = ref
	peer := p1HistoryPeer{record.PeerPID, record.PeerUID, os.Getgid(), ref.SHA256}
	record.Originals[1], err = p1WriteOriginal(filepath.Join(t.TempDir(), "peer.json"), p1JSON(t, peer))
	must(t, err)
	decision := p1HistoryDecision{1, strings.Repeat("a", 32), 1, ref.SHA256, "continue"}
	record.Originals[2], err = p1WriteOriginal(filepath.Join(t.TempDir(), "decision.json"), p1JSON(t, decision))
	must(t, err)
	if p1ValidateTraceRecords([]p1TraceRecord{record}) == nil {
		t.Fatal("wire omission masked by DTO zero value")
	}
}
