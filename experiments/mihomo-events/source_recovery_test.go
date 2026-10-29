package mihomoevents

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"hoopa/protocol/telemetrywire"
)

type recoveryCountingReader struct {
	io.Reader
	read int
}

func (r *recoveryCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestReadSourceRecoveryEnforcesManifestPairBudget(t *testing.T) {
	manifest, frames := sourceRecoveryFixture(t)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &fields); err != nil {
		t.Fatal(err)
	}
	const limit = 20 * (8192 + 36)
	fields["max_source_bytes"] = json.RawMessage(fmt.Sprint(limit))
	manifest, _ = json.Marshal(fields)
	m, err := telemetrywire.DecodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cp := counterFields()
	putFields(cp, map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
	lastStart := 0
	for seq := 3; ; seq++ {
		next := lifecycleFrame(t, cp, seq)
		if len(manifest)+len(frames)+len(next) > limit {
			// The exact boundary is still a valid snapshot, including JSON
			// whitespace in the final frame. Only excess bytes are rejected.
			raw := append([]byte(nil), frames[lastStart+4:len(frames)-sha256.Size]...)
			raw = append(raw, bytes.Repeat([]byte(" "), limit-len(manifest)-len(frames))...)
			if len(raw) > telemetrywire.SourceInputLimit {
				t.Fatal("invalid padded fixture")
			}
			exact := append([]byte(nil), frames[:lastStart]...)
			frame := make([]byte, 4+len(raw)+sha256.Size)
			binary.BigEndian.PutUint32(frame, uint32(len(raw)))
			copy(frame[4:], raw)
			sum := sha256.Sum256(raw)
			copy(frame[4+len(raw):], sum[:])
			exact = append(exact, frame...)
			if len(manifest)+len(exact) != limit {
				t.Fatal("fixture not at declared boundary")
			}
			if _, err := ReadSourceRecovery(bytes.NewReader(exact), manifest); err != nil {
				t.Fatal("exact declared pair budget rejected", err)
			}
			frames = append(exact, next...)
			break
		}
		lastStart = len(frames)
		frames = append(frames, next...)
	}
	r := &recoveryCountingReader{Reader: bytes.NewReader(frames)}
	if out, err := ReadSourceRecovery(r, manifest); err == nil || out.Scope != "" {
		t.Fatal("accepted a valid lifecycle exceeding its manifest byte budget")
	}
	if r.read > limit-len(manifest)+1 {
		t.Fatal("read beyond the manifest pair budget and one-byte overflow guard")
	}
}

func sourceRecoveryFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	exe, hash, boot, nonce, start := "/private/core", strings.Repeat("a", 64), "12345678-1234-1234-1234-123456789abc", strings.Repeat("b", 32), "123"
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s", exe, hash, boot, 123, start, nonce)))
	epoch := "epoch:" + hex.EncodeToString(sum[:])
	instance := "source:" + hex.EncodeToString(sum[:])
	manifest, _ := json.Marshal(map[string]any{"schema_version": 1, "source_epoch": epoch, "binary_path": exe, "binary_sha256": hash, "boot_id": boot, "pid": 123, "start_ticks": start, "nonce": nonce, "config_generation": "generation-a", "max_source_bytes": 8 * 1024 * 1024, "max_frame_bytes": 8192, "max_active_flows": 32, "reclamation": "not_implemented_bounded_stop", "scope": "linux_amd64_tcp_same_uid_netns_ipv4_loopback"})
	open := openedFields()
	cp := counterFields()
	for _, fields := range []map[string]json.RawMessage{open, cp} {
		putFields(fields, map[string]any{"source_epoch": epoch, "source_instance_id": instance, "config_generation": "generation-a"})
	}
	return manifest, append(lifecycleFrame(t, open, 1), lifecycleFrame(t, cp, 2)...)
}

func TestReadSourceRecoveryIsReadOnlyAndCannotSealWriter(t *testing.T) {
	manifest, frames := sourceRecoveryFixture(t)
	before := append([]byte(nil), frames...)
	report, err := ReadSourceRecovery(bytes.NewReader(frames), manifest)
	if err != nil || report.WriterEnd != nil || report.HistoryQuality != "pending" || report.RecordingStatus != "unverified" || len(report.Flows) != 1 {
		t.Fatal("snapshot falsely recovered writer", err)
	}
	flow := report.Flows[0]
	if flow.State != "pending" || flow.TailBytes != nil || flow.Final != nil || flow.ReconciliationID != "" || !bytes.Equal(frames, before) {
		t.Fatal("read-only recovery altered facts")
	}
}

func TestReadSourceRecoveryRejectsUnpairedOrCorruptSnapshot(t *testing.T) {
	manifest, frames := sourceRecoveryFixture(t)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(manifest, &fields)
	fields["config_generation"] = json.RawMessage(`"other"`)
	wrong, _ := json.Marshal(fields)
	for _, pair := range []struct{ meta, frames []byte }{{nil, frames}, {wrong, frames}, {manifest, frames[:len(frames)-1]}} {
		out, err := ReadSourceRecovery(bytes.NewReader(pair.frames), pair.meta)
		if err == nil || out.Scope != "" {
			t.Fatal("partial/unpaired recovery report exposed")
		}
	}
	if _, err := telemetrywire.DecodeManifest(manifest); err != nil {
		t.Fatal("test fixture invalid", err)
	}
}
