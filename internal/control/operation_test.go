//go:build linux

package control

import (
	"bytes"
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
)

func request(t *testing.T, id, version, name, params string) Request {
	t.Helper()
	raw, _ := encode(map[string]any{"request_id": id, "api_version": version, "operation": name, "params": json.RawMessage(params)})
	r, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func writer(t *testing.T) (*Journal, *config.Store, config.Paths) {
	t.Helper()
	p, err := config.ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	j, err := NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	return j, s, p
}
func intent() Intent {
	r := config.Revision("r2")
	old, next := config.Generation("g1"), config.Generation("g2")
	return Intent{"r1", &r, &old, &next, hash([]byte("candidate")), hash([]byte("impact"))}
}
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + hash(raw)
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func admit(t *testing.T, j *Journal, r Request) StoredOperation {
	t.Helper()
	stored, made, err := j.Admit(r, intent(), json.RawMessage(`{"outcome":"pending"}`))
	if err != nil || !made {
		t.Fatal("admit", made, err)
	}
	return stored
}

func TestOperationRequestCompleteDigestAndOpaqueID(t *testing.T) {
	base := request(t, "连接/请求\x00😀", "v1", "config.apply", `{"plan":{"base_revision":"r1","nodes":["A","B"]},"accept_disruption":true}`)
	same := request(t, base.ID(), "v1", "config.apply", `{"accept_disruption": true, "plan": {"nodes":["A","B"],"base_revision":"r1"}}`)
	if base.Digest() != same.Digest() || base.OperationID() != same.OperationID() {
		t.Fatal("equivalent object changed identity")
	}
	otherID := request(t, "另一个", "v1", "config.apply", string(base.params))
	if otherID.Digest() != base.Digest() || otherID.OperationID() == base.OperationID() {
		t.Fatal("ID/digest boundaries conflated")
	}
	for _, r := range []Request{
		request(t, base.ID(), "v2", "config.apply", string(base.params)),
		request(t, base.ID(), "v1", "runtime.stop", string(base.params)),
		request(t, base.ID(), "v1", "config.apply", `{"plan":{"base_revision":"r2","nodes":["A","B"]},"accept_disruption":true}`),
		request(t, base.ID(), "v1", "config.apply", `{"plan":{"base_revision":"r1","nodes":["B","A"]},"accept_disruption":true}`),
		request(t, base.ID(), "v1", "config.apply", `{"plan":{"base_revision":"r1","nodes":["A","B"]},"accept_disruption":false}`),
	} {
		if r.Digest() == base.Digest() {
			t.Fatal("full parameter/version/name omitted")
		}
	}
	request(t, strings.Repeat("😀", 128), "v1", "config.apply", `{}`)
	for _, bad := range []string{
		`{"api_version":"v1","request_id":"a","operation":"config.apply","params":{},"extra":1}`,
		`{"api_version":"v1","request_id":"a","Operation":"config.apply","params":{}}`,
		`{"api_version":"v1","request_id":"a","operation":"config.apply","params":null}`,
		`{"api_version":"v1","request_id":"\ud800","operation":"config.apply","params":{}}`,
		`{"api_version":"v1","request_id":"a","operation":"config.apply","params":{"x":1,"x":2}}`,
		`{"api_version":"v1","request_id":"a","operation":"config.apply","params":{"n":9007199254740993}}`,
	} {
		if _, err := ParseRequest([]byte(bad)); !errors.Is(err, ErrRequest) {
			t.Fatal("ambiguous request accepted", err)
		}
	}
	for _, id := range []string{"", strings.Repeat("😀", 129)} {
		raw, _ := encode(map[string]any{"api_version": "v1", "request_id": id, "operation": "config.apply", "params": map[string]any{}})
		if _, err := ParseRequest(raw); !errors.Is(err, ErrRequest) {
			t.Fatal("ID bound", err)
		}
	}
	if strings.Contains(fmt.Sprintf("%v %#v", base, base), base.ID()) {
		t.Fatal("private request leaked")
	}
}

func TestOperationReplayReopenAndTerminalCompaction(t *testing.T) {
	j, s, p := writer(t)
	r := request(t, "original", "v1", "config.apply", `{"base":"r1"}`)
	op := admit(t, j, r)
	if _, err := j.Compact(r.OperationID(), op.SHA256); !errors.Is(err, ErrOperationIncomplete) {
		t.Fatal(err)
	}
	for _, phase := range []Phase{Validated, Applying, Verifying, Publishing, Committed} {
		result, _ := encode(map[string]any{"outcome": phase, "text": "<kept>\u2028😀", "warning": "preserved"})
		var err error
		op, err = j.Advance(r.OperationID(), op.SHA256, phase, json.RawMessage(`{"proof":"immutable-reference"}`), result)
		if err != nil {
			t.Fatal(phase, err)
		}
	}
	original := append([]byte(nil), op.Record.Result...)
	before := snapshot(t, p.State)
	// New context/revision is deliberately invalid: an already accepted retry
	// returns its stored result first, without using that new admission context.
	replay, made, err := j.Admit(r, Intent{}, nil)
	if err != nil || made || !reflect.DeepEqual(op, replay) {
		t.Fatal("known result was not replayed", err)
	}
	changed := request(t, r.ID(), "v2", "config.apply", string(r.params))
	if _, _, err = j.Admit(changed, intent(), json.RawMessage(`{}`)); !errors.Is(err, ErrRequestIDReused) {
		t.Fatal(err)
	}
	if _, err = j.Advance(r.OperationID(), op.SHA256, Applying, json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, ErrOperationTerminal) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("replay/conflict mutated state")
	}
	compact, err := j.Compact(r.OperationID(), op.SHA256)
	if err != nil || !compact.Record.Compacted || compact.Record.Request != nil || compact.Record.Steps != nil || !bytes.Equal(compact.Record.Result, original) || compact.Record.BodySHA256 != op.Record.BodySHA256 {
		t.Fatal("compaction lost receipt or body evidence", err)
	}
	s.Close()
	s, err = config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, _ = NewJournal(s)
	got, err := j.Lookup(r)
	if err != nil || !reflect.DeepEqual(got, compact) {
		t.Fatal("tombstone failed after reopen", err)
	}
	if _, err = j.Lookup(changed); !errors.Is(err, ErrRequestIDReused) {
		t.Fatal(err)
	}
	// Detached result buffers must never change future replay.
	got.Record.Result[0] = '['
	got, err = j.Lookup(r)
	if err != nil || !bytes.Equal(got.Record.Result, original) {
		t.Fatal("caller changed stored result", err)
	}
	reader, err := config.OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ro, _ := NewJournal(reader)
	before = snapshot(t, p.State)
	if _, err = ro.Lookup(r); err != nil {
		t.Fatal(err)
	}
	if _, err = ro.IDs(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("lookup wrote files")
	}
}

func TestOperationPendingDegradedCASAndReadRequests(t *testing.T) {
	j, _, p := writer(t)
	before := snapshot(t, p.State)
	for _, item := range []struct{ name, params string }{{"status.get", `{}`}, {"config.plan", `{}`}, {"probe.run", `{}`}, {"runtime.start", `{"control_only":true}`}} {
		r := request(t, item.name, "v1", item.name, item.params)
		if _, _, err := j.Admit(r, intent(), json.RawMessage(`{}`)); !errors.Is(err, ErrRequest) {
			t.Fatal(item.name, err)
		}
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("read request persisted")
	}
	r := request(t, "pending", "v1", "runtime.stop", `{"expected_revision":"r1","accept_disruption":true}`)
	first := admit(t, j, r)
	latest, err := j.Advance(r.OperationID(), first.SHA256, Validated, json.RawMessage(`{"checked":true}`), json.RawMessage(`{"outcome":"pending","stage":2}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := j.Lookup(r)
	if err != nil || !reflect.DeepEqual(got, latest) {
		t.Fatal("pending replay frozen to first response", err)
	}
	before = snapshot(t, p.State)
	if _, err = j.Advance(r.OperationID(), first.SHA256, Applying, json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, config.ErrOperationConflict) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("stale update mutated")
	}
	latest, err = j.Advance(r.OperationID(), latest.SHA256, Degraded, json.RawMessage(`{"unknown":true}`), json.RawMessage(`{"outcome":"degraded"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.Compact(r.OperationID(), latest.SHA256); !errors.Is(err, ErrOperationIncomplete) {
		t.Fatal("degraded lost recovery body", err)
	}
	for _, phase := range []Phase{RollingBack, RolledBack} {
		phaseResult, _ := encode(map[string]any{"reported_phase": phase})
		latest, err = j.Advance(r.OperationID(), latest.SHA256, phase, json.RawMessage(`{"verified":"old"}`), phaseResult)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Lifecycle bodies retain exit evidence even at a terminal phase. Generic
	// config.apply tombstone behavior is covered by the separate replay test.
	before = snapshot(t, p.State)
	if _, err = j.Compact(r.OperationID(), latest.SHA256); !errors.Is(err, ErrOperationIncomplete) {
		t.Fatal("terminal lifecycle body compacted", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("refused lifecycle compaction mutated authority")
	}
}

func TestOperationCapacityAndConcurrentSameID(t *testing.T) {
	j, s, p := writer(t)
	r := request(t, "same", "v1", "config.apply", `{}`)
	var wg sync.WaitGroup
	outcomes := make(chan bool, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, made, err := j.Admit(r, intent(), json.RawMessage(`{}`))
			outcomes <- made
			failures <- err
		}()
	}
	wg.Wait()
	close(outcomes)
	close(failures)
	created := 0
	for made := range outcomes {
		if made {
			created++
		}
	}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatal(created)
	}
	initial, err := j.Lookup(r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < config.MaxOperationFiles; i++ {
		rq := request(t, fmt.Sprint("filler-", i), "v1", "config.apply", `{}`)
		o := initial.Record
		o.Identity.OperationID = rq.OperationID()
		o.Identity.RequestID = rq.ID()
		o.Identity.RequestDigest = rq.Digest()
		o.Request = retainedJSON(rq.raw)
		o.BodySHA256 = bodyHash(o)
		if err = o.validate(rq.OperationID()); err != nil {
			t.Fatal(err)
		}
		raw, _ := encode(o)
		if err = os.WriteFile(filepath.Join(p.State, "operations", string(rq.OperationID())+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j, _ = NewJournal(s)
	before := snapshot(t, p.State)
	if _, _, err = j.Admit(request(t, "over-capacity", "v1", "config.apply", `{}`), intent(), json.RawMessage(`{}`)); !errors.Is(err, config.ErrOperationCapacity) {
		t.Fatal(err)
	}
	if got, err := j.Lookup(r); err != nil || !reflect.DeepEqual(got, initial) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("capacity rejection/replay mutated")
	}
	if _, err = j.Advance(r.OperationID(), initial.SHA256, Validated, json.RawMessage(`{}`), json.RawMessage(`{}`)); err != nil {
		t.Fatal("capacity blocked existing progress", err)
	}
}

func TestOperationCorruptRecordNeverAbsent(t *testing.T) {
	j, s, p := writer(t)
	r := request(t, "damaged", "v1", "config.apply", `{}`)
	op := admit(t, j, r)
	file := filepath.Join(p.State, "operations", string(r.OperationID())+".json")
	for _, corrupt := range [][]byte{[]byte(`{`), []byte(`{}`), bytes.ReplaceAll(mustEncode(t, op.Record), []byte(`"prepared"`), []byte(`"committed"`))} {
		if err := os.WriteFile(file, corrupt, 0600); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, p.State)
		if _, _, err := j.Admit(r, intent(), json.RawMessage(`{}`)); !errors.Is(err, ErrOperationRecord) {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, snapshot(t, p.State)) {
			t.Fatal("damage overwritten")
		}
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(p.State, "operations")); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lookup(r); !errors.Is(err, config.ErrReadUnavailable) || errors.Is(err, ErrOperationNotFound) {
		t.Fatal("missing journal mistaken for unknown ID", err)
	}
	s.Close()
}
func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := encode(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
