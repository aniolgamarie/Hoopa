package telemetrywire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func executableFixture(path []byte) ConnectorExecutableEvidence {
	b := make([]byte, 296)
	binary.LittleEndian.PutUint64(b, 4)
	binary.LittleEndian.PutUint32(b[8:], 10)
	binary.LittleEndian.PutUint32(b[12:], 11)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(path)+1))
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint64(b[24:], 150)
	binary.LittleEndian.PutUint64(b[32:], 9007199254740993)
	copy(b[40:], path)
	// Preserve uninterpreted tail bytes, including nonzero bytes after the NUL.
	if len(path) < 255 {
		b[295] = 0xa5
	}
	name := path[bytes.LastIndexByte(path, '/')+1:]
	e := ConnectorExecutableEvidence{Schema: "sourceidentity-executable-call-v1", Status: "unverified", Source: "task_exe_file_d_path_v1", NameSource: "executable_path_basename_v1", CallToken: "9007199254740993", ObservedBootNS: "150", PathRawHex: hex.EncodeToString(path), PathTextValid: utf8.Valid(path), ProgramNameRawHex: hex.EncodeToString(name), ProgramNameTextValid: utf8.Valid(name), RawRecord: hex.EncodeToString(b)}
	if e.PathTextValid {
		e.PathText = string(path)
	}
	if e.ProgramNameTextValid {
		e.ProgramName = string(name)
	}
	return e
}
func executableFields(t *testing.T, e ConnectorExecutableEvidence, c ConnectorEvidence) map[string]json.RawMessage {
	t.Helper()
	f := connectorFields(t)[0]
	putFields(f, map[string]any{"schema_version": 4})
	raw, _ := json.Marshal(c)
	cf, _ := object(raw)
	cf["schema"] = json.RawMessage(`"sourceidentity-core-admission-v2"`)
	exe, _ := json.Marshal(e)
	cf["executable"] = exe
	raw, _ = json.Marshal(cf)
	f["connector"] = raw
	return f
}
func executableLifecycle(t *testing.T) []map[string]json.RawMessage {
	f := connectorFields(t)
	for _, v := range f {
		putFields(v, map[string]any{"schema_version": 4})
	}
	f[0] = executableFields(t, executableFixture([]byte("/bin/python3")), connectorFixture())
	return f
}
func TestConnectorV4LifecycleReplayRecovery(t *testing.T) {
	f := executableLifecycle(t)
	m := manifestFixture(t)
	c := connectorFixture()
	c.CoreGeneration = m.ConfigGeneration
	f[0] = executableFields(t, executableFixture([]byte("/bin/python3")), c)
	var s, replay State
	for i, v := range f {
		e := stateEvent(t, v, i+1, map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration})
		if e.SchemaVersion != 4 || e.Counter != nil && e.Counter.SchemaVersion != 4 {
			t.Fatal("version lost")
		}
		if i == 0 && (e.Connector.Schema != "sourceidentity-core-admission-v2" || e.Executable.CallToken != "9007199254740993") {
			t.Fatal("evidence lost")
		}
		if i > 0 && (e.Connector != nil || e.Executable != nil) {
			t.Fatal("identity leaked to non-opened")
		}
		commitState(t, &s, e)
		again, err := DecodeSourceEvent(e.Raw)
		if err != nil || !bytes.Equal(again.Raw, e.Raw) || !reflect.DeepEqual(again.Executable, e.Executable) {
			t.Fatal("replay changed evidence", err)
		}
		commitState(t, &replay, again)
	}
	if !reflect.DeepEqual(s.Snapshot(), replay.Snapshot()) {
		t.Fatal("replay state")
	}
	report, err := ProjectRecovery(m, replay.Snapshot(), strings.Repeat("c", 64), nil)
	if err != nil || report.Flows[0].Final.UploadBytes != 9007199254740994 || replay.Snapshot().Flows[0].Final.SchemaVersion != 4 {
		t.Fatal("recovery", err)
	}
	b, _ := json.Marshal(report)
	if bytes.Contains(b, []byte("executable")) {
		t.Fatal("recovery invented identity")
	}
}
func TestConnectorV4ExecutableMutations(t *testing.T) {
	base := executableFixture([]byte("/bin/python3"))
	for _, tc := range []struct {
		name   string
		change func(*ConnectorExecutableEvidence)
	}{
		{"schema", func(e *ConnectorExecutableEvidence) { e.Schema = "other" }}, {"status", func(e *ConnectorExecutableEvidence) { e.Status = "verified" }}, {"source", func(e *ConnectorExecutableEvidence) { e.Source = "proc" }}, {"name-source", func(e *ConnectorExecutableEvidence) { e.NameSource = "comm" }},
		{"token-zero", func(e *ConnectorExecutableEvidence) { e.CallToken = "0" }}, {"token-leading", func(e *ConnectorExecutableEvidence) { e.CallToken = "01" }}, {"token-overflow", func(e *ConnectorExecutableEvidence) { e.CallToken = "18446744073709551616" }},
		{"time-zero", func(e *ConnectorExecutableEvidence) { e.ObservedBootNS = "0" }}, {"before-start", func(e *ConnectorExecutableEvidence) { e.ObservedBootNS = "99" }}, {"after-connect", func(e *ConnectorExecutableEvidence) { e.ObservedBootNS = "201" }},
		{"path", func(e *ConnectorExecutableEvidence) { e.PathRawHex = "2f61" }}, {"path-upper", func(e *ConnectorExecutableEvidence) { e.PathRawHex = strings.ToUpper(e.PathRawHex) }}, {"text", func(e *ConnectorExecutableEvidence) { e.PathText = "wrong" }}, {"text-valid", func(e *ConnectorExecutableEvidence) { e.PathTextValid = false }}, {"name", func(e *ConnectorExecutableEvidence) { e.ProgramName = "wrong" }}, {"name-raw", func(e *ConnectorExecutableEvidence) { e.ProgramNameRawHex = "61" }}, {"name-valid", func(e *ConnectorExecutableEvidence) { e.ProgramNameTextValid = false }}, {"short", func(e *ConnectorExecutableEvidence) { e.RawRecord = "00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.change(&e)
			requireInvalidSource(t, encodeCounterFields(t, executableFields(t, e, connectorFixture())))
		})
	}
	// Every scalar ABI byte must bind to the corresponding public value.
	for i := 0; i < 40; i++ {
		t.Run("raw-"+strconv.Itoa(i), func(t *testing.T) {
			e := base
			b, _ := hex.DecodeString(e.RawRecord)
			b[i] ^= 0x80
			e.RawRecord = hex.EncodeToString(b)
			requireInvalidSource(t, encodeCounterFields(t, executableFields(t, e, connectorFixture())))
		})
	}
	for _, i := range []int{40, 42, 52} {
		e := base
		b, _ := hex.DecodeString(e.RawRecord)
		b[i] = 0xff
		e.RawRecord = hex.EncodeToString(b)
		requireInvalidSource(t, encodeCounterFields(t, executableFields(t, e, connectorFixture())))
	}
	f := executableFields(t, base, connectorFixture())
	raw := encodeCounterFields(t, f)
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"call_token":"9007199254740993"`), []byte(`"call_token":"9007199254740993","call_tok\u0065n":"9007199254740993"`), 1),
		bytes.Replace(raw, []byte(`"path_text":"/bin/python3"`), []byte(`"path_text":"\ud800"`), 1),
		bytes.Replace(raw, []byte(`"schema_version":4`), []byte(`"schema_version":4.0`), 1),
	} {
		requireInvalidSource(t, bad)
	}
	fields, _ := object(f["connector"])
	ef, _ := object(fields["executable"])
	for key := range ef {
		copy := wireFields{}
		for k, v := range ef {
			copy[k] = v
		}
		delete(copy, key)
		fields["executable"], _ = json.Marshal(copy)
		f["connector"], _ = json.Marshal(fields)
		requireInvalidSource(t, encodeCounterFields(t, f))
	}
	ef["unexpected"] = json.RawMessage(`true`)
	fields["executable"], _ = json.Marshal(ef)
	f["connector"], _ = json.Marshal(fields)
	requireInvalidSource(t, encodeCounterFields(t, f))
}
func TestConnectorV4PathsAndIntegerBoundaries(t *testing.T) {
	for _, path := range [][]byte{[]byte("/bin/程序 (deleted)"), []byte("/" + strings.Repeat("a", 254)), {'/', 0xff, '/', 'o', 'k'}, {'/', 'x', '/', 0xff}} {
		e := executableFixture(path)
		f := executableFields(t, e, connectorFixture())
		got, err := DecodeSourceEvent(encodeCounterFields(t, f))
		if err != nil || !reflect.DeepEqual(got.Executable, &e) {
			t.Fatal("path", path, err)
		}
	}
	for _, path := range [][]byte{[]byte("/"), []byte("/bin/"), []byte("relative"), {'/', 'a', 0, 'b'}} {
		e := executableFixture(path)
		requireInvalidSource(t, encodeCounterFields(t, executableFields(t, e, connectorFixture())))
	}
	for _, path := range [][]byte{{'/', 0xff, '/', 'o', 'k'}, {'/', 'x', '/', 0xff}} {
		e := executableFixture(path)
		e.PathTextValid = true
		requireInvalidSource(t, encodeCounterFields(t, executableFields(t, e, connectorFixture())))
	}
	for _, token := range []uint64{1, ^uint64(0)} {
		e := executableFixture([]byte("/a"))
		b, _ := hex.DecodeString(e.RawRecord)
		binary.LittleEndian.PutUint64(b[32:], token)
		e.RawRecord = hex.EncodeToString(b)
		e.CallToken = strconv.FormatUint(token, 10)
		if _, err := DecodeSourceEvent(encodeCounterFields(t, executableFields(t, e, connectorFixture()))); err != nil {
			t.Fatal("u64 precision", err)
		}
	}
	// BOOT uses exact u64 values independently of quality monotonic timestamps.
	for _, observed := range []uint64{9007199254740993, ^uint64(0)} {
		c := connectorFixture()
		c.LeaderStartBoottimeNS = strconv.FormatUint(observed-1, 10)
		c.ObservedBootNS = strconv.FormatUint(observed, 10)
		b, _ := hex.DecodeString(c.Raw.Connect)
		binary.LittleEndian.PutUint64(b[8:], observed)
		binary.LittleEndian.PutUint64(b[40:], observed-1)
		c.Raw.Connect = hex.EncodeToString(b)
		e := executableFixture([]byte("/a"))
		e.ObservedBootNS = c.ObservedBootNS
		b, _ = hex.DecodeString(e.RawRecord)
		binary.LittleEndian.PutUint64(b[24:], observed)
		e.RawRecord = hex.EncodeToString(b)
		if _, err := DecodeSourceEvent(encodeCounterFields(t, executableFields(t, e, c))); err != nil {
			t.Fatal("BOOT precision", err)
		}
	}
}
func TestConnectorV4LegacyAndQualityBoundaries(t *testing.T) {
	f := executableLifecycle(t)
	for _, index := range []int{2, 3, 4} {
		var s State
		commitState(t, &s, stateEvent(t, f[0], 1, nil))
		if s.Commit(stateEvent(t, f[index], 2, nil)) == nil {
			t.Fatal("quality bypass", index)
		}
	}
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	for _, old := range []map[string]json.RawMessage{validSourceFixtures()[2], qualityFixtures(t)[1], connectorFields(t)[1]} {
		before := s.Snapshot()
		if s.Commit(stateEvent(t, old, 2, nil)) == nil || !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("mixed version")
		}
	}
	bad := executableLifecycle(t)[2]
	putFields(bad, map[string]any{"duration_ms": "999"})
	requireInvalidSource(t, encodeCounterFields(t, bad))
	for _, version := range []int{1, 2, 3} {
		copy := executableFields(t, executableFixture([]byte("/a")), connectorFixture())
		putFields(copy, map[string]any{"schema_version": version})
		requireInvalidSource(t, encodeCounterFields(t, copy))
	}
	old := connectorFields(t)[0]
	putFields(old, map[string]any{"schema_version": 4})
	requireInvalidSource(t, encodeCounterFields(t, old))
	// Even a v1-schema connector must not smuggle executable into v3.
	old = executableFields(t, executableFixture([]byte("/a")), connectorFixture())
	c, _ := object(old["connector"])
	c["schema"] = json.RawMessage(`"sourceidentity-core-admission-v1"`)
	old["connector"], _ = json.Marshal(c)
	putFields(old, map[string]any{"schema_version": 3})
	requireInvalidSource(t, encodeCounterFields(t, old))
	for _, v := range [][]map[string]json.RawMessage{qualityFixtures(t), connectorFields(t)} {
		for _, row := range v {
			if _, err := DecodeSourceEvent(encodeCounterFields(t, row)); err != nil {
				t.Fatal("legacy changed", err)
			}
		}
	}
	for i := 1; i < len(f); i++ {
		putFields(f[i], map[string]any{"executable": executableFixture([]byte("/a"))})
		requireInvalidSource(t, encodeCounterFields(t, f[i]))
	}
}

func TestConnectorV4ActiveBoundAndVersionFreeze(t *testing.T) {
	f := executableLifecycle(t)
	var s State
	for i := 0; i < MaxActiveFlows; i++ {
		commitState(t, &s, stateEvent(t, f[0], i+1, map[string]any{"flow_id": "flow-" + strconv.Itoa(i), "attempt_id": "attempt-" + strconv.Itoa(i)}))
	}
	before := s.Snapshot()
	if s.Commit(stateEvent(t, f[0], 33, map[string]any{"flow_id": "overflow", "attempt_id": "overflow"})) == nil || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("active bound bypassed")
	}
	for _, version := range []int{1, 2, 3} {
		legacy := validSourceFixtures()[0]
		if version == 2 {
			legacy = qualityFixtures(t)[0]
		}
		if version == 3 {
			legacy = connectorFields(t)[0]
		}
		var old State
		commitState(t, &old, stateEvent(t, legacy, 1, nil))
		if old.Commit(stateEvent(t, f[1], 2, nil)) == nil {
			t.Fatal("epoch version changed", version)
		}
	}
}
