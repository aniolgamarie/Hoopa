package identityprofile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func profileFixture(t *testing.T) Profile {
	t.Helper()
	b := make([]byte, 24)
	binary.LittleEndian.PutUint64(b, 9007199254740993)
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[12:], 2)
	binary.LittleEndian.PutUint32(b[16:], 1)
	p, e := New(hex.EncodeToString(b), "9007199254740993")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestStrictFieldProfile(t *testing.T) {
	p := profileFixture(t)
	raw, _ := json.Marshal(p)
	q, e := Decode(raw, "9007199254740993")
	if e != nil || q != p {
		t.Fatal(e)
	}
	for i := 0; i < 24; i++ {
		b, _ := hex.DecodeString(p.ScopeRawHex)
		b[i] ^= 1
		r := p
		r.ScopeRawHex = hex.EncodeToString(b)
		if i >= 8 && i < 16 {
			b[i] ^= 1
			copy(b[(i/4)*4:(i/4)*4+4], []byte{0, 0, 0, 0})
			r.ScopeRawHex = hex.EncodeToString(b)
		}
		if r.Validate("9007199254740993") == nil {
			t.Fatal("bad scope", i)
		}
	}
	for _, b := range [][]byte{bytes.Replace(raw, []byte(`"source_schema_version":5`), []byte(`"source_schema_version":5.0`), 1), bytes.Replace(raw, []byte(`"id":`), []byte(`"id":null,"id":`), 1), bytes.Replace(raw, []byte(`"instance":`), []byte(`"\\u0069nstance":`), 1), bytes.Replace(raw, []byte(`"available"`), []byte(`"unverified"`), 1), bytes.Replace(raw, []byte(`"linux_bpf_connect_task_v1"`), []byte(`"proc"`), 1), append(raw, []byte(`{}`)...)} {
		if _, e := Decode(b, "9007199254740993"); e == nil {
			t.Fatal("malformed accepted", string(b))
		}
	}
}
func TestRequirementsNeverPromoteComm(t *testing.T) {
	p := profileFixture(t)
	for _, fields := range [][]string{{"instance"}, {"program_name", "program_path"}, {"instance", "program_name", "program_path"}} {
		r := Requirements{ID, 5, fields}
		b, _ := json.Marshal(r)
		got, e := DecodeRequirements(b)
		if e != nil || got.Match(p) != nil {
			t.Fatal(e)
		}
	}
	for _, f := range [][]string{nil, {}, {"comm"}, {"instance", "instance"}, {"PROCESS-NAME"}} {
		b, _ := json.Marshal(Requirements{ID, 5, f})
		if _, e := DecodeRequirements(b); e == nil {
			t.Fatal(f)
		}
	}
	for _, raw := range []string{`null`, `{"profile_id":"` + ID + `","source_schema_version":5,"required_fields":null}`, `{"profile_id":"` + ID + `","source_schema_version":5,"required_fields":["instance"],"x":0}`, strings.Replace(`{"profile_id":"`+ID+`","source_schema_version":5,"required_fields":["instance"]}`, `"source_schema_version":5`, `"source_schema_version":5e0`, 1)} {
		if _, e := DecodeRequirements([]byte(raw)); e == nil {
			t.Fatal(raw)
		}
	}
	if (Requirements{ID, 5, []string{"instance"}}).Match(Profile{}) == nil {
		t.Fatal("zero grants fields")
	}
}
