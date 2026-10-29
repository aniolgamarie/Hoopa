package capabilityprofile

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"strings"
	"testing"
)

func actualFields(t *testing.T) identityprofile.Profile {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint64(b, 1)
	binary.LittleEndian.PutUint32(b[8:], 2)
	binary.LittleEndian.PutUint32(b[12:], 3)
	binary.LittleEndian.PutUint32(b[16:], 1)
	p, e := identityprofile.New(hex.EncodeToString(b), "1")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func fieldConfig(t *testing.T, fields []string) []byte {
	v := profileMap(t)
	v["hoopa-identity"] = identityprofile.Requirements{ProfileID: identityprofile.ID, SourceSchemaVersion: 5, RequiredFields: fields}
	return encode(t, v)
}
func TestIdentityRequirementsPreflight(t *testing.T) {
	p := actualFields(t)
	for _, fields := range [][]string{{"instance"}, {"program_name", "program_path"}, {"instance", "program_name", "program_path"}} {
		raw := fieldConfig(t, fields)
		if _, e := Validate(raw, "gen"); e == nil {
			t.Fatal("legacy accepted new config")
		}
		if _, _, e := ValidateDeclaredIdentity(raw, "gen"); e != nil {
			t.Fatal(e)
		}
		if _, e := ValidateWithIdentity(raw, "gen", p); e != nil {
			t.Fatal(e)
		}
		if _, e := ValidateWithIdentity(raw, "gen", identityprofile.Profile{}); e == nil {
			t.Fatal("missing actual fields")
		}
	}
	if _, e := ValidateWithIdentity([]byte(validProfile), "gen", p); e == nil {
		t.Fatal("new profile missing config")
	}
	for _, fields := range [][]string{nil, {"comm"}, {"instance", "instance"}, {"unknown"}} {
		if _, _, e := ValidateDeclaredIdentity(fieldConfig(t, fields), "gen"); e == nil {
			t.Fatal("bad requirements", fields)
		}
	}
	for _, rule := range []string{"PROCESS-NAME,python3,node:direct", "PROCESS-PATH,/bin/python3,node:direct", "UID,65534,node:direct", "AND,((PROCESS-NAME,python3),(DOMAIN,alpha.test)),node:direct"} {
		var v map[string]any
		json.Unmarshal(fieldConfig(t, []string{"program_name"}), &v)
		v["rules"] = []string{rule}
		if _, e := ValidateWithIdentity(encode(t, v), "gen", p); e == nil {
			t.Fatal("legacy process/UID enabled", rule)
		}
	}
	for _, fromto := range [][2]string{{`"source_schema_version":5`, `"source_schema_version":5.0`}, {`"profile_id":"` + identityprofile.ID + `"`, `"profile_id":"other"`}, {`"required_fields":["instance"]`, `"required_fields":null`}, {`"required_fields":["instance"]`, `"required_fields":["instance"],"extra":true`}} {
		raw := strings.Replace(string(fieldConfig(t, []string{"instance"})), fromto[0], fromto[1], 1)
		if _, _, e := ValidateDeclaredIdentity([]byte(raw), "gen"); e == nil {
			t.Fatal("shape accepted", raw)
		}
	}
}
