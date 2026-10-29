package mihomoevents

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
)

func lifecycleFrame(t *testing.T, f map[string]json.RawMessage, seq int) []byte {
	t.Helper()
	f["source_seq"], _ = json.Marshal(strconv.Itoa(seq))
	f["event_id"], _ = json.Marshal("event-" + strconv.Itoa(seq))
	raw := encodeCounterFields(t, f)
	frame := make([]byte, 4+len(raw)+sha256.Size)
	binary.BigEndian.PutUint32(frame, uint32(len(raw)))
	copy(frame[4:], raw)
	sum := sha256.Sum256(raw)
	copy(frame[4+len(raw):], sum[:])
	return frame
}

func TestSourceLifecyclePendingCensusReopenPreservesKnownCheckpoint(t *testing.T) {
	raw := lifecycleFrame(t, openedFields(), 1)
	cp := counterFields()
	cp["upload_bytes"] = json.RawMessage(`"9007199254740993"`)
	cp["download_bytes"] = json.RawMessage(`"17"`)
	raw = append(raw, lifecycleFrame(t, cp, 2)...)
	first, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a")
	if err != nil || len(first.Flows) != 1 {
		t.Fatal("census recovery failed", err)
	}
	f := first.Flows[0]
	if first.EpochFinality != "unverified" || f.State != "pending" || f.TailBytes != nil || f.Final != nil || f.LastCheckpoint == nil || uint64(f.LastCheckpoint.UploadBytes) != 9007199254740993 || f.LastCheckpoint.SourceSeq != 2 {
		t.Fatal("pending tail fabricated or checkpoint lost")
	}
	reopened, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a")
	if err != nil || !reflect.DeepEqual(first, reopened) {
		t.Fatal("reopen changed stable source evidence", err)
	}
}

func TestSourceLifecycleCumulativeFinalIsNotAddedToCheckpoint(t *testing.T) {
	raw := lifecycleFrame(t, openedFields(), 1)
	cp := counterFields()
	cp["upload_bytes"], cp["download_bytes"] = json.RawMessage(`"10"`), json.RawMessage(`"20"`)
	raw = append(raw, lifecycleFrame(t, cp, 2)...)
	final := counterFields()
	final["kind"], final["upload_bytes"], final["download_bytes"] = json.RawMessage(`"closed"`), json.RawMessage(`"13"`), json.RawMessage(`"24"`)
	raw = append(raw, lifecycleFrame(t, final, 3)...)
	epoch := validSourceFixtures()[8]
	epoch["unfinished_flows"], epoch["finality"] = json.RawMessage(`"0"`), json.RawMessage(`"complete"`)
	raw = append(raw, lifecycleFrame(t, epoch, 4)...)
	r, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a")
	if err != nil || r.EpochFinality != "complete" || len(r.Flows) != 1 {
		t.Fatal(err)
	}
	f := r.Flows[0]
	if f.State != "closed" || f.Final.UploadBytes != 13 || f.Final.DownloadBytes != 24 || f.LastCheckpoint.UploadBytes != 10 || f.TailBytes == nil || *f.TailBytes != 0 {
		t.Fatal("final was summed or exact closed lost")
	}
}

func TestSourceLifecycleShutdownGapHasNullTail(t *testing.T) {
	raw := lifecycleFrame(t, openedFields(), 1)
	raw = append(raw, lifecycleFrame(t, validSourceFixtures()[7], 2)...)
	raw = append(raw, lifecycleFrame(t, validSourceFixtures()[8], 3)...)
	r, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a")
	if err != nil || r.EpochFinality != "incomplete" || r.Flows[0].State != "gap" || r.Flows[0].TailBytes != nil || r.Flows[0].Final != nil {
		t.Fatal("unknown shutdown became zero", err)
	}
}

func TestSourceLifecycleRejectsCorruptIncompleteAndUnorderedEvidence(t *testing.T) {
	good := lifecycleFrame(t, openedFields(), 1)
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)-1] ^= 1
	cp := counterFields()
	closed := counterFields()
	closed["kind"] = json.RawMessage(`"closed"`)
	for _, raw := range [][]byte{good[:3], good[:len(good)-1], corrupt, lifecycleFrame(t, cp, 1), lifecycleFrame(t, openedFields(), 2), append(append([]byte(nil), good...), good...), append(append([]byte(nil), good...), lifecycleFrame(t, openedFields(), 2)...), append(append(append([]byte(nil), good...), lifecycleFrame(t, closed, 2)...), lifecycleFrame(t, cp, 3)...)} {
		r, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a")
		if !errors.Is(err, ErrInvalidSourceLifecycle) || !reflect.DeepEqual(r, SourceLifecycle{}) {
			t.Fatal("bad source accepted or partially returned", err)
		}
	}
	if _, err := ReadSourceLifecycle(bytes.NewReader(good), "wrong-epoch"); !errors.Is(err, ErrInvalidSourceLifecycle) {
		t.Fatal("wrong epoch accepted")
	}
}

func TestSourceLifecycleRejectsCounterDecreaseAndFalseEpochComplete(t *testing.T) {
	open := lifecycleFrame(t, openedFields(), 1)
	cp := counterFields()
	cp["upload_bytes"] = json.RawMessage(`"10"`)
	raw := append(append([]byte(nil), open...), lifecycleFrame(t, cp, 2)...)
	cp["upload_bytes"] = json.RawMessage(`"9"`)
	raw = append(raw, lifecycleFrame(t, cp, 3)...)
	if _, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a"); !errors.Is(err, ErrInvalidSourceLifecycle) {
		t.Fatal("counter decrease accepted")
	}
	epoch := validSourceFixtures()[8]
	epoch["unfinished_flows"], epoch["finality"] = json.RawMessage(`"0"`), json.RawMessage(`"complete"`)
	if _, err := ReadSourceLifecycle(bytes.NewReader(append(open, lifecycleFrame(t, epoch, 2)...)), "epoch-a"); !errors.Is(err, ErrInvalidSourceLifecycle) {
		t.Fatal("active flow hidden by complete epoch")
	}
}

func TestSourceLifecycleRejectsAttemptOwnedByAnotherFlow(t *testing.T) {
	first := openedFields()
	second := openedFields()
	putFields(second, map[string]any{"flow_id": "flow-2"})
	raw := append(lifecycleFrame(t, first, 1), lifecycleFrame(t, second, 2)...)
	if _, err := ReadSourceLifecycle(bytes.NewReader(raw), "epoch-a"); !errors.Is(err, ErrInvalidSourceLifecycle) {
		t.Fatal("attempt reused across flows")
	}
}
