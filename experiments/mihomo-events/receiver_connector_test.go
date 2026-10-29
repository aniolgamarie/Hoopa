//go:build linux

package mihomoevents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hoopa/experiments/telemetry"
	"hoopa/protocol/telemetrywire"
)

func receiverConnectorFixture() telemetrywire.ConnectorEvidence {
	b, c, g := make([]byte, 56), make([]byte, 112), make([]byte, 40)
	le := binary.LittleEndian
	for i, n := range []uint64{9007199254740993, 2, 3, 4} {
		le.PutUint64(b[i*8:], n)
	}
	for i, n := range []uint32{0x0100007f, 0x0100007f, 12345, 23456, 1, 0} {
		le.PutUint32(b[32+i*4:], n)
	}
	for i, n := range []uint64{9007199254740993, 200, 4, 2, 5, 100} {
		le.PutUint64(c[i*8:], n)
	}
	for i, n := range []uint32{1, 10, 11, 12, 10, 1, 1, 0} {
		le.PutUint32(c[48+i*4:], n)
	}
	copy(c[80:96], "caller")
	copy(c[96:], "leader")
	le.PutUint64(g[8:], 1)
	le.PutUint64(g[16:], 2)
	le.PutUint64(g[24:], 1)
	return telemetrywire.ConnectorEvidence{Schema: "sourceidentity-core-admission-v1", IdentityCapability: "unverified", ProcessPath: "unknown", BootID: "12345678-1234-1234-1234-123456789abc", CoreGeneration: "generation-1", Generation: "9007199254740993", SourceCgroupID: "5", RootCgroupID: "6", AcceptedCookie: "3", ClientCookie: "4", SocketNetnsCookie: "2", TGID: 10, LeaderPID: 10, CallerTID: 11, LeaderStartBoottimeNS: "100", ObservedBootNS: "200", PIDNSInode: 12, LeaderComm: telemetrywire.ConnectorComm{RawHex: hex.EncodeToString(c[96:]), Text: "leader", TextValid: true, Status: "unverified"}, CallerComm: telemetrywire.ConnectorComm{RawHex: hex.EncodeToString(c[80:96]), Text: "caller", TextValid: true, Status: "unverified"}, LocalEndpoint: telemetrywire.ConnectorEndpoint{IP: "127.0.0.1", Port: 23456}, RemoteEndpoint: telemetrywire.ConnectorEndpoint{IP: "127.0.0.1", Port: 12345}, Raw: telemetrywire.ConnectorRawIdentity{Binding: hex.EncodeToString(b), Connect: hex.EncodeToString(c), Gate: hex.EncodeToString(g)}}
}

func receiverConnectorFrames(t *testing.T) (telemetrywire.Manifest, [][]byte) {
	t.Helper()
	exe, hash, boot, nonce, start := "/private/core", strings.Repeat("a", 64), "12345678-1234-1234-1234-123456789abc", strings.Repeat("b", 32), "123"
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s", exe, hash, boot, 123, start, nonce)))
	manifest, _ := json.Marshal(map[string]any{"schema_version": 1, "source_epoch": "epoch:" + hex.EncodeToString(sum[:]), "binary_path": exe, "binary_sha256": hash, "boot_id": boot, "pid": 123, "start_ticks": start, "nonce": nonce, "config_generation": "generation-1", "max_source_bytes": 8 * 1024 * 1024, "max_frame_bytes": 8192, "max_active_flows": 32, "reclamation": "not_implemented_bounded_stop", "scope": "linux_amd64_tcp_same_uid_netns_ipv4_loopback"})
	m, err := telemetrywire.DecodeManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../testdata/telemetry-quality-v2/positive.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		var f map[string]json.RawMessage
		if json.Unmarshal(s.Bytes(), &f) != nil {
			t.Fatal("bad fixture")
		}
		putFields(f, map[string]any{"schema_version": 3, "source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
		if len(out) == 0 {
			putFields(f, map[string]any{"process": map[string]string{"status": "unknown", "reason": "connector_experiment_unverified"}, "identity_scope": "connect_initiator_v1_initial_ns_ipv4_loopback_experimental", "connector": receiverConnectorFixture()})
		}
		out = append(out, encodeCounterFields(t, f))
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	var end map[string]json.RawMessage
	if json.Unmarshal(receiverFixture(t, m.SourceEpoch, "event-7", "7", "epoch_closed"), &end) != nil {
		t.Fatal("bad epoch fixture")
	}
	putFields(end, map[string]any{"schema_version": 3, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
	out = append(out, encodeCounterFields(t, end))
	return m, out
}

func TestSourceReceiverConnectorV3DurableReplayRecovery(t *testing.T) {
	m, frames := receiverConnectorFrames(t)
	dir := receiverDir(t)
	r := receiverOpen(t, dir)
	frames[0] = append([]byte(" \n"), frames[0]...)
	for i, raw := range frames {
		if seq, err := r.Accept(raw); err != nil || seq != uint64(i+1) {
			t.Fatal("accept", i, seq, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r = receiverOpen(t, dir)
	if seq, err := r.Accept(frames[0]); err != nil || seq != 1 {
		t.Fatal("raw retry", seq, err)
	}
	if _, err := r.Accept(bytes.TrimSpace(frames[0])); !errors.Is(err, ErrReceiverConflict) {
		t.Fatal("raw conflict", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	rows := receiverRecords(t, dir)
	if len(rows) != len(frames) {
		t.Fatal("unexpected durable count")
	}
	var state telemetrywire.State
	for i, row := range rows {
		if !bytes.Equal(row.Payload, frames[i]) {
			t.Fatal("raw bytes changed")
		}
		e, err := telemetrywire.DecodeSourceEvent(row.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (e.Connector == nil || e.Connector.Raw != receiverConnectorFixture().Raw || e.Connector.IdentityCapability != "unverified") {
			t.Fatal("connector provenance changed")
		}
		if state.Commit(e) != nil {
			t.Fatal("raw source replay failed")
		}
	}
	report, err := telemetrywire.ProjectRecovery(m, state.Snapshot(), strings.Repeat("c", 64), nil)
	if err != nil || report.Flows[0].Final.UploadBytes != 9007199254740994 {
		t.Fatal("recovery projection", err)
	}
	raw, _ := json.Marshal(report)
	if bytes.Contains(raw, []byte("connector")) {
		t.Fatal("report claims connector identity")
	}
}

func TestSourceReceiverConnectorV3RejectsBadRawOnAcceptAndReopen(t *testing.T) {
	_, frames := receiverConnectorFrames(t)
	for _, tc := range []struct {
		name string
		bad  []byte
	}{
		{"capability", bytes.Replace(frames[0], []byte(`"identity_capability":"unverified"`), []byte(`"identity_capability":"verified"`), 1)},
		{"raw", bytes.Replace(frames[0], []byte(`"binding":"`), []byte(`"binding":"00`), 1)},
		{"duplicate", bytes.Replace(frames[0], []byte(`"tgid_initial":10`), []byte(`"tgid_initial":10,"tgid_initial":10`), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := receiverDir(t)
			r := receiverOpen(t, dir)
			if _, err := r.Accept(tc.bad); !errors.Is(err, ErrInvalidSourceEvent) {
				t.Fatal("invalid connector accepted", err)
			}
			if seq, err := r.Accept(frames[0]); err != nil || seq != 1 {
				t.Fatal("rejection advanced state", seq, err)
			}
			r.Close()
			if len(receiverRecords(t, dir)) != 1 {
				t.Fatal("invalid reached journal")
			}
			badDir := receiverDir(t)
			j, err := telemetry.OpenJournal(badDir)
			if err != nil {
				t.Fatal(err)
			}
			e, err := telemetrywire.DecodeSourceEvent(frames[0])
			if err != nil {
				t.Fatal(err)
			}
			if _, err = j.Append(e.SourceEpoch, e.EventID, tc.bad); err != nil {
				t.Fatal(err)
			}
			j.Close()
			path := filepath.Join(badDir, "events.frames")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if reopened, err := OpenSourceReceiver(badDir); reopened != nil || !errors.Is(err, ErrReceiverRecovery) {
				t.Fatal("bad durable connector reopened", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid source repaired")
			}
		})
	}
}
