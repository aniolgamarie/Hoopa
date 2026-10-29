package telemetrywire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func manifestFixture(t *testing.T) Manifest {
	t.Helper()
	exe, hash, boot, nonce, start := "/private/core", strings.Repeat("a", 64), "12345678-1234-1234-1234-123456789abc", strings.Repeat("b", 32), "123"
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s", exe, hash, boot, 123, start, nonce)))
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "source_epoch": "epoch:" + hex.EncodeToString(sum[:]), "binary_path": exe, "binary_sha256": hash, "boot_id": boot, "pid": 123, "start_ticks": start, "nonce": nonce, "config_generation": "generation-a", "max_source_bytes": 8 * 1024 * 1024, "max_frame_bytes": 8192, "max_active_flows": 32, "reclamation": "not_implemented_bounded_stop", "scope": "linux_amd64_tcp_same_uid_netns_ipv4_loopback"})
	m, err := DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestIdentityAndRawAreBound(t *testing.T) {
	m := manifestFixture(t)
	if m.SourceInstanceID != "source:"+strings.TrimPrefix(m.SourceEpoch, "epoch:") || m.MaxSourceBytes != 8*1024*1024 {
		t.Fatal("manifest identity lost")
	}
	before := append([]byte(nil), m.Raw...)
	decoded, err := DecodeManifest(before)
	if err != nil {
		t.Fatal(err)
	}
	before[0] = ' '
	if string(decoded.Raw) != string(m.Raw) {
		t.Fatal("raw aliases caller")
	}
}

func TestManifestRejectsMalformedOrUnboundEvidence(t *testing.T) {
	m := manifestFixture(t)
	for _, field := range []string{"source_epoch", "binary_path", "binary_sha256", "boot_id", "pid", "start_ticks", "nonce", "max_source_bytes", "max_frame_bytes", "max_active_flows", "schema_version", "scope", "reclamation"} {
		t.Run(field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(m.Raw, &fields)
			values := map[string]string{"source_epoch": `"epoch:bad"`, "binary_path": `"relative"`, "binary_sha256": `"abc"`, "boot_id": `"invalid"`, "pid": `1.0`, "start_ticks": `"01"`, "nonce": `"00000000000000000000000000000000"`, "max_source_bytes": `8388609`, "max_frame_bytes": `8193`, "max_active_flows": `33`, "schema_version": `1e0`, "scope": `"unverified"`, "reclamation": `"automatic"`}
			fields[field] = json.RawMessage(values[field])
			bad, _ := json.Marshal(fields)
			if got, err := DecodeManifest(bad); err == nil || len(got.Raw) != 0 {
				t.Fatal("unbound manifest accepted")
			}
		})
	}
	for _, raw := range [][]byte{nil, []byte(`null`), append(append([]byte(nil), m.Raw[:len(m.Raw)-1]...), []byte(`,"schema_version":1}`)...), append(append([]byte(nil), m.Raw...), []byte(` {}`)...)} {
		if _, err := DecodeManifest(raw); err == nil {
			t.Fatal("bad shape accepted")
		}
	}
}
