//go:build linux

package mihomoevents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hoopa/experiments/telemetry"
)

func receiverFixture(t *testing.T, epoch, event, seq, kind string) []byte {
	t.Helper()
	f := counterFields()
	if kind == "opened" {
		f = openedFields()
	}
	putFields(f, map[string]any{"source_epoch": epoch, "event_id": event, "source_seq": seq, "kind": kind})
	if kind == "epoch_closed" {
		delete(f, "upload_bytes")
		delete(f, "download_bytes")
		putFields(f, map[string]any{"flow_id": nil, "attempt_id": nil, "reason": "core_shutdown", "unfinished_flows": "0", "finality": "complete"})
	}
	return encodeCounterFields(t, f)
}
func receiverDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func receiverOpen(t *testing.T, dir string) *SourceReceiver {
	t.Helper()
	r, err := OpenSourceReceiver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}
func receiverRecords(t *testing.T, dir string) []telemetry.Record {
	t.Helper()
	j, err := telemetry.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	return j.Replay()
}

func TestSourceReceiverDurableInterleavedEpochsAndRawDedup(t *testing.T) {
	dir := receiverDir(t)
	r := receiverOpen(t, dir)
	a := append([]byte(" \n"), receiverFixture(t, "epoch-a", "event-a1", "1", "opened")...)
	b := receiverFixture(t, "epoch-b", "event-b1", "1", "opened")
	c := receiverFixture(t, "epoch-a", "event-a2", "2", "closed")
	for i, raw := range [][]byte{a, b, c} {
		seq, err := r.Accept(raw)
		if err != nil || seq != uint64(i+1) {
			t.Fatalf("receipt %d: %d %v", i, seq, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r = receiverOpen(t, dir)
	if seq, err := r.Accept(a); err != nil || seq != 1 {
		t.Fatal("reopen lost original receipt", seq, err)
	}
	if seq, err := r.Accept(c); err != nil || seq != 3 {
		t.Fatal("source seq replaced receipt", seq, err)
	}
	if _, err := r.Accept(bytes.TrimSpace(a)); !errors.Is(err, ErrReceiverConflict) {
		t.Fatal("raw byte conflict normalized", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	rows := receiverRecords(t, dir)
	if len(rows) != 3 || !bytes.Equal(rows[0].Payload, a) || !bytes.Contains(rows[2].Payload, []byte(`"9007199254740993"`)) {
		t.Fatal("original bytes/precision changed")
	}
}

func TestSourceReceiverRejectsBeforeJournalAndAllowsClosedRetry(t *testing.T) {
	dir := receiverDir(t)
	r := receiverOpen(t, dir)
	first := receiverFixture(t, "epoch-a", "event-1", "1", "opened")
	if _, err := r.Accept(receiverFixture(t, "epoch-a", "event-2", "2", "closed")); !errors.Is(err, ErrReceiverOrder) {
		t.Fatal("missing initial source position accepted")
	}
	if _, err := r.Accept(first); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{receiverFixture(t, "epoch-a", "event-other", "1", "closed"), receiverFixture(t, "epoch-a", "event-3", "3", "closed")} {
		if _, err := r.Accept(raw); !errors.Is(err, ErrReceiverOrder) {
			t.Fatal("out of order accepted", err)
		}
	}
	for _, field := range []string{"source_instance_id", "config_generation"} {
		f := counterFields()
		putFields(f, map[string]any{"source_epoch": "epoch-a", "event_id": "event-2", "source_seq": "2", field: "private-changed"})
		if _, err := r.Accept(encodeCounterFields(t, f)); !errors.Is(err, ErrReceiverOrder) {
			t.Fatal("epoch metadata changed", err)
		}
	}
	if _, err := r.Accept(receiverFixture(t, "epoch-a", "event-2", "2", "closed")); err != nil {
		t.Fatal(err)
	}
	last := receiverFixture(t, "epoch-a", "event-end", "3", "epoch_closed")
	if _, err := r.Accept(last); err != nil {
		t.Fatal(err)
	}
	if seq, err := r.Accept(last); err != nil || seq != 3 {
		t.Fatal("closed marker retry refused")
	}
	if _, err := r.Accept(receiverFixture(t, "epoch-a", "event-3", "3", "closed")); !errors.Is(err, ErrReceiverOrder) {
		t.Fatal("event after epoch close accepted")
	}
	r.Close()
	if len(receiverRecords(t, dir)) != 3 {
		t.Fatal("rejected events reached journal")
	}
}

func TestSourceReceiverStrictInvalidInputNoStateAdvance(t *testing.T) {
	dir := receiverDir(t)
	r := receiverOpen(t, dir)
	raw := receiverFixture(t, "epoch-a", "event-1", "1", "opened")
	for _, bad := range [][]byte{[]byte("null"), append(append([]byte(nil), raw...), []byte(" null")...), append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"event\u005fid":"private-secret"}`)...), bytes.Replace(raw, []byte(`"target":"example.test"`), []byte(`"target":null`), 1)} {
		if _, err := r.Accept(bad); !errors.Is(err, ErrInvalidSourceEvent) || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("invalid payload accepted/leaked", err)
		}
	}
	if seq, err := r.Accept(raw); err != nil || seq != 1 {
		t.Fatal("invalid event advanced state")
	}
	r.Close()
	if len(receiverRecords(t, dir)) != 1 {
		t.Fatal("invalid events persisted")
	}
}

func TestSourceReceiverReopenRejectsOpaqueMismatchOrInvalidSequence(t *testing.T) {
	for _, tc := range []struct{ epoch, id, seq string }{{"outer-other", "event-1", "1"}, {"epoch-a", "outer-other", "1"}, {"epoch-a", "event-1", "2"}} {
		t.Run(tc.epoch+tc.id+tc.seq, func(t *testing.T) {
			dir := receiverDir(t)
			j, err := telemetry.OpenJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = j.Append(tc.epoch, tc.id, receiverFixture(t, "epoch-a", "event-1", tc.seq, "closed")); err != nil {
				t.Fatal(err)
			}
			j.Close()
			before, err := os.ReadFile(filepath.Join(dir, "events.frames"))
			if err != nil {
				t.Fatal(err)
			}
			if r, err := OpenSourceReceiver(dir); r != nil || !errors.Is(err, ErrReceiverRecovery) {
				t.Fatal("opaque source mismatch accepted", err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "events.frames"))
			if !bytes.Equal(before, after) {
				t.Fatal("reopen repaired invalid source")
			}
			j, err = telemetry.OpenJournal(dir)
			if err != nil {
				t.Fatal("failed receiver leaked lock", err)
			}
			j.Close()
		})
	}
}

func TestSourceReceiverConcurrentDuplicatesAndClose(t *testing.T) {
	dir := receiverDir(t)
	r := receiverOpen(t, dir)
	raw := receiverFixture(t, "epoch-a", "event-1", "1", "opened")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := r.Accept(raw)
			if err != nil || seq != 1 {
				t.Error("concurrent retry", seq, err)
			}
		}()
	}
	wg.Wait()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Accept(raw); !errors.Is(err, ErrReceiverClosed) {
		t.Fatal("closed receiver accepted")
	}
	if len(receiverRecords(t, dir)) != 1 {
		t.Fatal("duplicate counted twice")
	}
}

type uncertainReceiverJournal struct{ calls int }

func (j *uncertainReceiverJournal) Append(string, string, json.RawMessage) (uint64, error) {
	j.calls++
	return 0, telemetry.ErrUncertain
}
func (*uncertainReceiverJournal) Close() error { return nil }
func TestSourceReceiverUncertainAppendPoisonsWithoutAdvance(t *testing.T) {
	j := &uncertainReceiverJournal{}
	r := newSourceReceiver(j)
	raw := receiverFixture(t, "epoch-a", "event-1", "1", "opened")
	if _, err := r.Accept(raw); !errors.Is(err, ErrReceiverUncertain) {
		t.Fatal("uncertain append confirmed")
	}
	if len(r.epochs) != 0 || len(r.events) != 0 {
		t.Fatal("uncertain append advanced state")
	}
	if _, err := r.Accept(raw); !errors.Is(err, ErrReceiverUncertain) || j.calls != 1 {
		t.Fatal("poisoned receiver retried append")
	}
}

func TestSourceReceiverReopenValidatesEveryOpaqueRecord(t *testing.T) {
	for _, mode := range []string{"metadata", "repeat_sequence", "skipped_sequence", "after_epoch_closed", "invalid_shape"} {
		t.Run(mode, func(t *testing.T) {
			dir := receiverDir(t)
			j, err := telemetry.OpenJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			kind := "opened"
			if mode == "after_epoch_closed" {
				kind = "epoch_closed"
			}
			if _, err = j.Append("epoch-a", "event-1", receiverFixture(t, "epoch-a", "event-1", "1", kind)); err != nil {
				t.Fatal(err)
			}
			f := counterFields()
			putFields(f, map[string]any{"source_seq": "2", "event_id": "event-2"})
			switch mode {
			case "metadata":
				putFields(f, map[string]any{"config_generation": "private-new"})
			case "repeat_sequence":
				putFields(f, map[string]any{"source_seq": "1"})
			case "skipped_sequence":
				putFields(f, map[string]any{"source_seq": "3"})
			case "invalid_shape":
				f["upload_bytes"] = json.RawMessage(`null`)
			}
			if _, err = j.Append("epoch-a", "event-2", encodeCounterFields(t, f)); err != nil {
				t.Fatal(err)
			}
			j.Close()
			if r, err := OpenSourceReceiver(dir); r != nil || !errors.Is(err, ErrReceiverRecovery) {
				t.Fatal("later invalid source record accepted", err)
			}
			j, err = telemetry.OpenJournal(dir)
			if err != nil {
				t.Fatal("journal lock leaked", err)
			}
			if len(j.Replay()) != 2 {
				t.Fatal("invalid journal modified")
			}
			j.Close()
		})
	}
}

type lostReceiverResult struct{ j *telemetry.Journal }

func (j lostReceiverResult) Append(epoch, id string, raw json.RawMessage) (uint64, error) {
	if _, err := j.j.Append(epoch, id, raw); err != nil {
		return 0, err
	}
	return 0, telemetry.ErrUncertain
}
func (j lostReceiverResult) Close() error { return j.j.Close() }
func TestSourceReceiverLostAppendResultReopenDisambiguates(t *testing.T) {
	dir := receiverDir(t)
	j, err := telemetry.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newSourceReceiver(lostReceiverResult{j})
	raw := receiverFixture(t, "epoch-a", "event-1", "1", "opened")
	if _, err = r.Accept(raw); !errors.Is(err, ErrReceiverUncertain) || len(r.epochs) != 0 {
		t.Fatal("lost result advanced source")
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r = receiverOpen(t, dir)
	if seq, err := r.Accept(raw); err != nil || seq != 1 {
		t.Fatal("reopen failed to disambiguate durable original", seq, err)
	}
	r.Close()
	if len(receiverRecords(t, dir)) != 1 {
		t.Fatal("uncertain event counted twice")
	}
}

func TestSourceReceiverRejectsLifecycleBeforeDurableAppend(t *testing.T) {
	r := receiverOpen(t, receiverDir(t))
	for _, kind := range []string{"bytes_checkpoint", "closed"} {
		if _, err := r.Accept(receiverFixture(t, "epoch-a", "event-1", "1", kind)); !errors.Is(err, ErrReceiverOrder) {
			t.Fatalf("orphan %s reached journal: %v", kind, err)
		}
	}
	if len(r.epochs) != 0 || len(r.events) != 0 {
		t.Fatal("invalid lifecycle advanced receiver")
	}
}
