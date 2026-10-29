package telemetrywire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func connectorFixture() ConnectorEvidence {
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
	return ConnectorEvidence{Schema: "sourceidentity-core-admission-v1", IdentityCapability: "unverified", ProcessPath: "unknown", BootID: "12345678-1234-1234-1234-123456789abc", CoreGeneration: "generation-1", Generation: "9007199254740993", SourceCgroupID: "5", RootCgroupID: "6", AcceptedCookie: "3", ClientCookie: "4", SocketNetnsCookie: "2", TGID: 10, LeaderPID: 10, CallerTID: 11, LeaderStartBoottimeNS: "100", ObservedBootNS: "200", PIDNSInode: 12, LeaderComm: ConnectorComm{hex.EncodeToString(c[96:]), "leader", true, "unverified"}, CallerComm: ConnectorComm{hex.EncodeToString(c[80:96]), "caller", true, "unverified"}, LocalEndpoint: ConnectorEndpoint{"127.0.0.1", 23456}, RemoteEndpoint: ConnectorEndpoint{"127.0.0.1", 12345}, Raw: ConnectorRawIdentity{hex.EncodeToString(b), hex.EncodeToString(c), hex.EncodeToString(g)}}
}
func connectorFields(t *testing.T) []map[string]json.RawMessage {
	t.Helper()
	f := qualityFixtures(t)
	for _, v := range f {
		putFields(v, map[string]any{"schema_version": 3})
	}
	putFields(f[0], map[string]any{"process": map[string]string{"status": "unknown", "reason": "connector_experiment_unverified"}, "identity_scope": connectorScope, "connector": connectorFixture()})
	end := sourceFields("epoch_closed")
	putFields(end, map[string]any{"schema_version": 3, "flow_id": nil, "attempt_id": nil, "reason": "core_shutdown", "unfinished_flows": "0", "finality": "complete"})
	f = append(f, end)
	return f
}
func TestConnectorV3LifecycleReplayAndRecovery(t *testing.T) {
	f := connectorFields(t)
	m := manifestFixture(t)
	var s, replay State
	for i, v := range f {
		changes := map[string]any{"source_epoch": m.SourceEpoch, "source_instance_id": m.SourceInstanceID, "config_generation": m.ConfigGeneration}
		if i == 0 {
			c := connectorFixture()
			c.CoreGeneration = m.ConfigGeneration
			changes["connector"] = c
		}
		e := stateEvent(t, v, i+1, changes)
		if e.SchemaVersion != 3 || i == 0 && (e.Connector == nil || e.Connector.Generation != "9007199254740993") {
			t.Fatal("connector precision/version lost")
		}
		commitState(t, &s, e)
		again, err := DecodeSourceEvent(e.Raw)
		if err != nil || !bytes.Equal(again.Raw, e.Raw) {
			t.Fatal("raw replay", err)
		}
		commitState(t, &replay, again)
	}
	if !reflect.DeepEqual(s.Snapshot(), replay.Snapshot()) {
		t.Fatal("replay changed state")
	}
	report, err := ProjectRecovery(m, replay.Snapshot(), strings.Repeat("c", 64), nil)
	if err != nil || report.Flows[0].Final.UploadBytes != 9007199254740994 || replay.Snapshot().Flows[0].Final.SchemaVersion != 3 {
		t.Fatal("recovery precision/version", err)
	}
	raw, _ := json.Marshal(report)
	if bytes.Contains(raw, []byte("connector")) || bytes.Contains(raw, []byte("leader_comm")) {
		t.Fatal("report invented connector provenance")
	}
}
func TestConnectorV3StrictMutations(t *testing.T) {
	f := connectorFields(t)[0]
	for _, tc := range []struct {
		name   string
		mutate func(*ConnectorEvidence)
	}{
		{"schema", func(c *ConnectorEvidence) { c.Schema = "other" }},
		{"capability", func(c *ConnectorEvidence) { c.IdentityCapability = "verified" }},
		{"path", func(c *ConnectorEvidence) { c.ProcessPath = "/proc/10/exe" }},
		{"boot", func(c *ConnectorEvidence) { c.BootID = strings.ToUpper(c.BootID) }},
		{"generation", func(c *ConnectorEvidence) { c.CoreGeneration = "other" }},
		{"leading-zero", func(c *ConnectorEvidence) { c.Generation = "09007199254740993" }},
		{"overflow", func(c *ConnectorEvidence) { c.Generation = "18446744073709551616" }},
		{"zero", func(c *ConnectorEvidence) { c.SocketNetnsCookie = "0" }},
		{"same-cgroup", func(c *ConnectorEvidence) { c.RootCgroupID = c.SourceCgroupID }},
		{"same-cookie", func(c *ConnectorEvidence) { c.AcceptedCookie = c.ClientCookie }},
		{"pid-overflow", func(c *ConnectorEvidence) { c.TGID = 1 << 31 }},
		{"leader-mismatch", func(c *ConnectorEvidence) { c.LeaderPID++ }},
		{"zero-tid", func(c *ConnectorEvidence) { c.CallerTID = 0 }},
		{"zero-pidns", func(c *ConnectorEvidence) { c.PIDNSInode = 0 }},
		{"zero-start", func(c *ConnectorEvidence) { c.LeaderStartBoottimeNS = "0" }},
		{"before-start", func(c *ConnectorEvidence) { c.ObservedBootNS = "99" }},
		{"local-mismatch", func(c *ConnectorEvidence) { c.LocalEndpoint.Port++ }},
		{"remote-mismatch", func(c *ConnectorEvidence) { c.RemoteEndpoint.IP = "127.0.0.2" }},
		{"comm-text", func(c *ConnectorEvidence) { c.LeaderComm.Text = "other" }},
		{"comm-valid", func(c *ConnectorEvidence) { c.CallerComm.TextValid = false }},
		{"comm-status", func(c *ConnectorEvidence) { c.CallerComm.Status = "verified" }},
		{"comm-raw", func(c *ConnectorEvidence) { c.LeaderComm.RawHex = strings.Repeat("00", 16) }},
		{"uppercase", func(c *ConnectorEvidence) { c.Raw.Binding = strings.ToUpper(c.Raw.Binding) }},
		{"short-raw", func(c *ConnectorEvidence) { c.Raw.Connect = "00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectorFixture()
			tc.mutate(&c)
			putFields(f, map[string]any{"connector": c})
			requireInvalidSource(t, encodeCounterFields(t, f))
		})
	}
	// Every ABI byte participates in a public coordinate, schema, comm, or gate invariant.
	// Gate counters may validly vary together; a one-byte mutation breaks the equation.
	for _, kind := range []string{"binding", "connect", "gate"} {
		size := map[string]int{"binding": 56, "connect": 112, "gate": 40}[kind]
		for i := 0; i < size; i++ {
			c := connectorFixture()
			target := map[string]*string{"binding": &c.Raw.Binding, "connect": &c.Raw.Connect, "gate": &c.Raw.Gate}[kind]
			b, _ := hex.DecodeString(*target)
			b[i] ^= 0x80
			*target = hex.EncodeToString(b)
			putFields(f, map[string]any{"connector": c})
			requireInvalidSource(t, encodeCounterFields(t, f))
		}
	}
	putFields(f, map[string]any{"connector": connectorFixture()})
	for _, key := range []string{"identity_scope", "process", "flow_start_ns", "source_addr", "entry_addr"} {
		old := f[key]
		f[key] = json.RawMessage(`null`)
		requireInvalidSource(t, encodeCounterFields(t, f))
		f[key] = old
	}
	for _, version := range []string{`3.0`, `3e0`, `"3"`, `4`} {
		old := f["schema_version"]
		f["schema_version"] = json.RawMessage(version)
		requireInvalidSource(t, encodeCounterFields(t, f))
		f["schema_version"] = old
	}
	raw := encodeCounterFields(t, f)
	requireInvalidSource(t, bytes.Replace(raw, []byte(`"schema":"sourceidentity-core-admission-v1"`), []byte(`"schema":"sourceidentity-core-admission-v1","sch\u0065ma":"sourceidentity-core-admission-v1"`), 1))
	requireInvalidSource(t, bytes.Replace(raw, []byte(`"text_valid":true`), []byte(`"text_valid":true,"extra":0`), 1))
	var check func([]byte, func([]byte) []byte)
	check = func(raw []byte, wrap func([]byte) []byte) {
		fields, ok := object(raw)
		if !ok {
			return
		}
		for key, value := range fields {
			copy := wireFields{}
			for k, v := range fields {
				copy[k] = v
			}
			delete(copy, key)
			bad, _ := json.Marshal(copy)
			requireInvalidSource(t, wrap(bad))
			copy[key] = value
			copy["unexpected"] = json.RawMessage(`0`)
			bad, _ = json.Marshal(copy)
			requireInvalidSource(t, wrap(bad))
			duplicate := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"`+key+`":`+string(value)+`}`)...)
			requireInvalidSource(t, wrap(duplicate))
			check(value, func(child []byte) []byte {
				parent := wireFields{}
				for k, v := range fields {
					parent[k] = v
				}
				parent[key] = child
				b, _ := json.Marshal(parent)
				return wrap(b)
			})
		}
	}
	check(f["connector"], func(child []byte) []byte {
		parent := wireFields{}
		for k, v := range f {
			parent[k] = v
		}
		parent["connector"] = child
		b, _ := json.Marshal(parent)
		return b
	})

}
func TestConnectorV3QualityAndEpochBoundaries(t *testing.T) {
	f := connectorFields(t)
	for _, index := range []int{2, 3, 4} {
		var s State
		commitState(t, &s, stateEvent(t, f[0], 1, nil))
		if s.Commit(stateEvent(t, f[index], 2, nil)) == nil {
			t.Fatal("v3 bypassed v2 state", index)
		}
	}
	var s State
	commitState(t, &s, stateEvent(t, f[0], 1, nil))
	for _, legacy := range []map[string]json.RawMessage{validSourceFixtures()[2], qualityFixtures(t)[1]} {
		if s.Commit(stateEvent(t, legacy, 2, nil)) == nil {
			t.Fatal("mixed version accepted")
		}
	}
	bad := f[2]
	putFields(bad, map[string]any{"duration_ms": "999"})
	requireInvalidSource(t, encodeCounterFields(t, bad))
	var bounded State
	for i := 0; i < MaxActiveFlows; i++ {
		e := stateEvent(t, f[0], i+1, map[string]any{"flow_id": strings.Repeat("x", i+1), "attempt_id": strings.Repeat("a", i+1)})
		commitState(t, &bounded, e)
	}
	if bounded.Commit(stateEvent(t, f[0], 33, map[string]any{"flow_id": "overflow", "attempt_id": "overflow"})) == nil {
		t.Fatal("active bound bypassed")
	}
}

func TestConnectorV3CommAndUint64Boundaries(t *testing.T) {
	f := connectorFields(t)[0]
	for _, tc := range []struct {
		name   string
		raw    []byte
		status uint32
		text   string
		valid  bool
	}{
		{"empty-unavailable", make([]byte, 16), 0, "", true},
		{"invalid-utf8", append([]byte{0xff, 0}, make([]byte, 14)...), 1, "", false},
		{"unicode", append([]byte("进程"), make([]byte, 10)...), 1, "进程", true},
		{"comm-15", append([]byte("123456789012345"), 0), 1, "123456789012345", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connectorFixture()
			raw, _ := hex.DecodeString(c.Raw.Connect)
			copy(raw[80:96], tc.raw)
			binary.LittleEndian.PutUint32(raw[68:], tc.status)
			c.Raw.Connect = hex.EncodeToString(raw)
			status := "unverified"
			if tc.status == 0 {
				status = "unavailable"
			}
			c.CallerComm = ConnectorComm{hex.EncodeToString(tc.raw), tc.text, tc.valid, status}
			putFields(f, map[string]any{"connector": c})
			e, err := DecodeSourceEvent(encodeCounterFields(t, f))
			if err != nil || e.Connector.CallerComm != c.CallerComm {
				t.Fatal("valid comm boundary", err)
			}
			c.CallerComm.Text += "wrong"
			putFields(f, map[string]any{"connector": c})
			requireInvalidSource(t, encodeCounterFields(t, f))
		})
	}
	c := connectorFixture()
	c.Generation = "18446744073709551615"
	c.SourceCgroupID = "18446744073709551615"
	c.LeaderStartBoottimeNS = "18446744073709551615"
	c.ObservedBootNS = c.LeaderStartBoottimeNS
	c.PIDNSInode = ^uint32(0)
	c.TGID = 1<<31 - 1
	c.LeaderPID = c.TGID
	c.CallerTID = c.TGID
	b, _ := hex.DecodeString(c.Raw.Binding)
	conn, _ := hex.DecodeString(c.Raw.Connect)
	binary.LittleEndian.PutUint64(b, ^uint64(0))
	for _, off := range []int{0, 8, 32, 40} {
		binary.LittleEndian.PutUint64(conn[off:], ^uint64(0))
	}
	for _, off := range []int{52, 56, 64} {
		binary.LittleEndian.PutUint32(conn[off:], 1<<31-1)
	}
	binary.LittleEndian.PutUint32(conn[60:], ^uint32(0))
	c.Raw.Binding = hex.EncodeToString(b)
	c.Raw.Connect = hex.EncodeToString(conn)
	putFields(f, map[string]any{"connector": c})
	e, err := DecodeSourceEvent(encodeCounterFields(t, f))
	if err != nil || *e.Connector != c {
		t.Fatal("max uint64 identity lost", err)
	}
	// Raw field type and scalar token bounds must reject before unmarshalling a partial identity.
	raw := encodeCounterFields(t, f)
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"active_pidns_inum":4294967295`), []byte(`"active_pidns_inum":4294967296`), 1),
		bytes.Replace(raw, []byte(`"active_pidns_inum":4294967295`), []byte(`"active_pidns_inum":4.294967295e9`), 1),
		bytes.Replace(raw, []byte(`"generation":"18446744073709551615"`), []byte(`"generation":18446744073709551615`), 1),
	} {
		requireInvalidSource(t, bad)
	}
}
