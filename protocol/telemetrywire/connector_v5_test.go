package telemetrywire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"reflect"
	"strings"
	"testing"
)

func fieldProfileFixture(t *testing.T) identityprofile.Profile {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint64(b, 9007199254740993)
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[12:], 2)
	binary.LittleEndian.PutUint32(b[16:], 1)
	p, e := identityprofile.New(hex.EncodeToString(b), "9007199254740993")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func v5Fields(t *testing.T, path []byte) map[string]json.RawMessage {
	f := executableFields(t, executableFixture(path), connectorFixture())
	putFields(f, map[string]any{"schema_version": 5, "process": map[string]string{"status": "unknown", "reason": "legacy_process_fields_unused"}})
	c, _ := object(f["connector"])
	delete(c, "identity_capability")
	delete(c, "process_path")
	putFields(c, map[string]any{"schema": "sourceidentity-core-admission-v3", "field_profile": fieldProfileFixture(t)})
	exe, _ := object(c["executable"])
	putFields(exe, map[string]any{"schema": "sourceidentity-executable-call-v2", "status": "available"})
	c["executable"], _ = json.Marshal(exe)
	f["connector"], _ = json.Marshal(c)
	return f
}
func manifest2Fixture(t *testing.T) Manifest {
	m := manifestFixture(t)
	f, _ := object(m.Raw)
	putFields(f, map[string]any{"schema_version": 2, "source_schema_version": 5, "identity_profile": identityprofile.ID, "scope": "linux_amd64_tcp_scoped_connect_initiator_ipv4_loopback"})
	b, _ := json.Marshal(f)
	m, e := DecodeManifest(b)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestV5CompletePendingReplayManifestBinding(t *testing.T) {
	m := manifest2Fixture(t)
	f := executableLifecycle(t)
	f[0] = v5Fields(t, []byte{'/', 'b', 'i', 'n', '/', 0xff})
	var state, replay State
	for i, v := range f {
		putFields(v, map[string]any{"schema_version": 5})
		if i == 0 {
			c, _ := object(v["connector"])
			putFields(c, map[string]any{"core_generation": m.ConfigGeneration})
			v["connector"], _ = json.Marshal(c)
		}
		e := stateEvent(t, v, i+1, map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
		if ValidateManifestEvent(m, e) != nil {
			t.Fatal("binding")
		}
		if i == 0 && (e.FieldProfile == nil || e.Connector.IdentityCapability != "" || e.Connector.ProcessPath != "" || e.Executable.Status != "available" || e.Executable.ProgramNameTextValid) {
			t.Fatal(e)
		}
		if e.Counter != nil && e.Counter.SchemaVersion != 5 {
			t.Fatal("counter version")
		}
		commitState(t, &state, e)
		again, err := DecodeSourceEvent(e.Raw)
		if err != nil || !bytes.Equal(e.Raw, again.Raw) {
			t.Fatal(err)
		}
		commitState(t, &replay, again)
		if i == 0 {
			a, err := ProjectRecovery(m, state.Snapshot(), strings.Repeat("c", 64), nil)
			b, err2 := ProjectRecovery(m, replay.Snapshot(), strings.Repeat("c", 64), nil)
			if err != nil || err2 != nil || !reflect.DeepEqual(a, b) || a.Flows[0].State != "pending" || a.Flows[0].TailBytes != nil {
				t.Fatal("pending recovery", err, err2)
			}
		}
	}
	if !reflect.DeepEqual(state.Snapshot(), replay.Snapshot()) {
		t.Fatal("replay")
	}
	r, err := ProjectRecovery(m, replay.Snapshot(), strings.Repeat("c", 64), nil)
	if err != nil || r.Flows[0].Final.UploadBytes != 9007199254740994 {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r)
	if bytes.Contains(b, []byte("field_profile")) {
		t.Fatal("report invented identity")
	}
}
func TestV5StrictShapesAndRawMutations(t *testing.T) {
	for _, tc := range []string{"legacycap", "legacypath", "oldschema", "oldexe", "oldreason", "missingprofile", "badprofile", "extraexe"} {
		t.Run(tc, func(t *testing.T) {
			f := v5Fields(t, []byte("/bin/python3"))
			c, _ := object(f["connector"])
			switch tc {
			case "legacycap":
				putFields(c, map[string]any{"identity_capability": "unverified"})
			case "legacypath":
				putFields(c, map[string]any{"process_path": "unknown"})
			case "oldschema":
				putFields(c, map[string]any{"schema": "sourceidentity-core-admission-v2"})
			case "missingprofile":
				delete(c, "field_profile")
			case "badprofile":
				c["field_profile"] = json.RawMessage(`null`)
			case "oldreason":
				putFields(f, map[string]any{"process": map[string]string{"status": "unknown", "reason": "connector_experiment_unverified"}})
			case "oldexe", "extraexe":
				e, _ := object(c["executable"])
				if tc == "oldexe" {
					putFields(e, map[string]any{"status": "unverified"})
				} else {
					putFields(e, map[string]any{"extra": 0})
				}
				c["executable"], _ = json.Marshal(e)
			}
			f["connector"], _ = json.Marshal(c)
			b, _ := json.Marshal(f)
			if _, err := DecodeSourceEvent(b); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, part := range []string{"binding", "connect", "gate", "executable"} {
		for _, offset := range []int{0, 8, 16, 20, 24, 32} {
			f := v5Fields(t, []byte("/bin/python3"))
			c, _ := object(f["connector"])
			var target wireFields
			if part == "executable" {
				target, _ = object(c[part])
			} else {
				target, _ = object(c["raw"])
			}
			key := part
			if part == "executable" {
				key = "raw_record"
			}
			text, _ := scalarText(target[key])
			b, _ := hex.DecodeString(text)
			if offset >= len(b) {
				continue
			}
			b[offset] ^= 1
			putFields(target, map[string]any{key: hex.EncodeToString(b)})
			if part == "executable" {
				c[part], _ = json.Marshal(target)
			} else {
				c["raw"], _ = json.Marshal(target)
			}
			f["connector"], _ = json.Marshal(c)
			raw, _ := json.Marshal(f)
			if _, err := DecodeSourceEvent(raw); err == nil {
				t.Fatalf("raw accepted %s/%d", part, offset)
			}
		}
	}
	f := v5Fields(t, []byte("/bin/python3"))
	putFields(f, map[string]any{"schema_version": 4})
	b, _ := json.Marshal(f)
	if _, err := DecodeSourceEvent(b); err == nil {
		t.Fatal("old v4 accepted v5")
	}
}
func TestV5ManifestMismatchAndMixedEpoch(t *testing.T) {
	m := manifest2Fixture(t)
	f := v5Fields(t, []byte("/bin/python3"))
	c, _ := object(f["connector"])
	putFields(c, map[string]any{"core_generation": m.ConfigGeneration})
	f["connector"], _ = json.Marshal(c)
	e := stateEvent(t, f, 1, map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
	old := manifestFixture(t)
	if ValidateManifestEvent(old, e) == nil {
		t.Fatal("v1 carried5")
	}
	bad := e
	bad.ConfigGeneration = "different"
	if ValidateManifestEvent(m, bad) == nil {
		t.Fatal("generation")
	}
	bad = e
	bad.SchemaVersion = 4
	if ValidateManifestEvent(m, bad) == nil {
		t.Fatal("v2 carried4")
	}
	var s State
	commitState(t, &s, e)
	next := executableLifecycle(t)[1]
	bad = stateEvent(t, next, 2, map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
	if s.Commit(bad) == nil {
		t.Fatal("mixed version")
	}
	for _, key := range []string{"identity_profile", "source_schema_version", "scope"} {
		f, _ := object(m.Raw)
		delete(f, key)
		b, _ := json.Marshal(f)
		if _, err := DecodeManifest(b); err == nil {
			t.Fatal("missing manifest key", key)
		}
	}
}
