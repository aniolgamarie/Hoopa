//go:build linux

package mihomoevents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hoopa/experiments/mihomo-events/capabilityprofile"
	"hoopa/protocol/identityprofile"
	"hoopa/protocol/telemetrywire"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func receiverV5(t *testing.T) (telemetrywire.Manifest, [][]byte) {
	m, rows := receiverConnectorFrames(t)
	var mf map[string]any
	json.Unmarshal(m.Raw, &mf)
	mf["schema_version"] = 2
	mf["source_schema_version"] = 5
	mf["identity_profile"] = identityprofile.ID
	mf["scope"] = "linux_amd64_tcp_scoped_connect_initiator_ipv4_loopback"
	b, _ := json.Marshal(mf)
	var err error
	m, err = telemetrywire.DecodeManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range rows {
		var f map[string]any
		json.Unmarshal(raw, &f)
		f["schema_version"] = 5
		if i == 0 {
			f["process"] = map[string]string{"status": "unknown", "reason": "legacy_process_fields_unused"}
			c := f["connector"].(map[string]any)
			delete(c, "identity_capability")
			delete(c, "process_path")
			c["schema"] = "sourceidentity-core-admission-v3"
			scope := make([]byte, 24)
			binary.LittleEndian.PutUint64(scope, 9007199254740993)
			binary.LittleEndian.PutUint32(scope[8:], 1)
			binary.LittleEndian.PutUint32(scope[12:], 2)
			binary.LittleEndian.PutUint32(scope[16:], 1)
			p, _ := identityprofile.New(hex.EncodeToString(scope), "9007199254740993")
			c["field_profile"] = p
			eraw := make([]byte, 296)
			binary.LittleEndian.PutUint64(eraw, 4)
			binary.LittleEndian.PutUint32(eraw[8:], 10)
			binary.LittleEndian.PutUint32(eraw[12:], 11)
			binary.LittleEndian.PutUint32(eraw[16:], 13)
			binary.LittleEndian.PutUint32(eraw[20:], 1)
			binary.LittleEndian.PutUint64(eraw[24:], 150)
			binary.LittleEndian.PutUint64(eraw[32:], 9007199254740995)
			copy(eraw[40:], "/bin/python3")
			c["executable"] = telemetrywire.ConnectorExecutableEvidence{Schema: "sourceidentity-executable-call-v2", Status: "available", Source: "task_exe_file_d_path_v1", NameSource: "executable_path_basename_v1", CallToken: "9007199254740995", ObservedBootNS: "150", PathRawHex: hex.EncodeToString([]byte("/bin/python3")), PathText: "/bin/python3", PathTextValid: true, ProgramNameRawHex: hex.EncodeToString([]byte("python3")), ProgramName: "python3", ProgramNameTextValid: true, RawRecord: hex.EncodeToString(eraw)}
		}
		rows[i], _ = json.Marshal(f)
	}
	return m, rows
}
func framedIdentity(rows [][]byte) []byte {
	var out []byte
	for _, r := range rows {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(len(r)))
		out = append(out, b...)
		out = append(out, r...)
		h := sha256.Sum256(r)
		out = append(out, h[:]...)
	}
	return out
}
func TestIdentityV5ManifestReceiverAndRecovery(t *testing.T) {
	m, rows := receiverV5(t)
	dir := receiverDir(t)
	r, e := OpenSourceReceiverWithManifests(dir, [][]byte{m.Raw})
	if e != nil {
		t.Fatal(e)
	}
	for i, b := range rows {
		if n, e := r.Accept(b); e != nil || n != uint64(i+1) {
			t.Fatal(i, e)
		}
	}
	if _, e = r.Accept(rows[0]); e != nil {
		t.Fatal(e)
	}
	bad := append([]byte(" "), rows[0]...)
	if _, e = r.Accept(bad); !errors.Is(e, ErrReceiverConflict) {
		t.Fatal(e)
	}
	var changed map[string]any
	json.Unmarshal(rows[1], &changed)
	changed["config_generation"] = "other-generation"
	conflict, _ := json.Marshal(changed)
	if _, e = r.Accept(conflict); !errors.Is(e, ErrReceiverConflict) {
		t.Fatal("same event conflict must precede immutable manifest/state checks", e)
	}
	r.Close()
	r, e = OpenSourceReceiverWithManifests(dir, [][]byte{m.Raw})
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	if r, e = OpenSourceReceiver(dir); e == nil {
		r.Close()
		t.Fatal("manifest-free v5 replay")
	}
	for _, partial := range []bool{false, true} {
		data := framedIdentity(rows)
		if partial {
			data = framedIdentity(rows[:1])
		}
		a, e := ReadSourceRecovery(bytes.NewReader(data), m.Raw)
		b, e2 := ReadSourceRecovery(bytes.NewReader(data), m.Raw)
		if e != nil || e2 != nil {
			t.Fatal(e, e2)
		}
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		if !bytes.Equal(x, y) {
			t.Fatal("unstable recovery")
		}
		if partial && (a.Flows[0].State != "pending" || a.Flows[0].TailBytes != nil) {
			t.Fatal("pending became zero")
		}
	}
	old, _ := receiverConnectorFrames(t)
	if _, e := ReadSourceRecovery(bytes.NewReader(framedIdentity(rows)), old.Raw); e == nil {
		t.Fatal("v1manifest carried5")
	}
}

// Optional actual-candidate exercise uses only startup rejection inputs. The
// source-order unit tests in the candidate establish that these returns precede
// PrepareGuard/listeners. A final unbound port alone would not prove that order.
func TestIdentityPreflightActualCandidate(t *testing.T) {
	path := os.Getenv("HOOPA_IDENTITY_PREFLIGHT_CANDIDATE")
	if path == "" {
		t.Skip("actual v5 candidate not supplied; no listener-preflight runtime validation claimed")
	}
	expected := os.Getenv("HOOPA_IDENTITY_PREFLIGHT_SHA256")
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(b)
	if !filepath.IsAbs(path) || hex.EncodeToString(h[:]) != expected {
		t.Fatal("explicit pinned v5 candidate required")
	}
	for _, required := range [][]string{{"comm"}, {"instance", "instance"}, {"unknown"}} {
		t.Run(strings.Join(required, "-"), func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			config := map[string]any{"mode": "rule", "log-level": "silent", "ipv6": false, "find-process-mode": "off", "external-controller": "127.0.0.1:19094", "secret": strings.Repeat("s", 32), "listeners": []any{map[string]any{"name": "entry-a", "type": "mixed", "listen": "127.0.0.1", "port": 19093, "udp": false, "users": []any{map[string]string{"username": "client", "password": strings.Repeat("p", 32)}}}}, "proxies": []any{map[string]string{"name": "node:direct", "type": "direct"}}, "rules": []string{"MATCH,node:direct"}, "profile": map[string]bool{"store-selected": false, "store-fake-ip": false}, "hoopa-identity": identityprofile.Requirements{ProfileID: identityprofile.ID, SourceSchemaVersion: 5, RequiredFields: required}}
			raw, _ := json.Marshal(config)
			if _, _, e := capabilityprofile.ValidateDeclaredIdentity(raw, "gen:preflight"); e == nil {
				t.Fatal("pure preflight accepted invalid requirements")
			}
			file := filepath.Join(dir, "config.json")
			os.WriteFile(file, raw, 0600)
			sum := sha256.Sum256(raw)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path, "-d", dir, "-f", file)
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "HOOPA_") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			cmd.Env = append(cmd.Env, "HOOPA_GUARD_GENERATION=gen:preflight", "HOOPA_GUARD_CONFIG_SHA256="+hex.EncodeToString(sum[:]), "HOOPA_TRACE_PROFILE=5", "HOOPA_TRACE_GENERATION=gen:preflight", "HOOPA_TRACE_INBOUND=entry-a")
			out, runErr := cmd.CombinedOutput()
			var rejected *exec.ExitError
			if ctx.Err() != nil || !errors.As(runErr, &rejected) || rejected.ExitCode() != 1 || !bytes.Contains(out, []byte("Hoopa protected configuration rejected")) {
				t.Fatalf("expected bounded protected-entry rejection marker and exit1: %v %s", runErr, out)
			}
		})
	}
}
