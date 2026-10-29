package capabilityprofile

import (
	"bytes"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func programConfig(t *testing.T, fields []string, rules ...string) map[string]any {
	t.Helper()
	v := profileMap(t)
	v["hoopa-identity"] = identityprofile.Requirements{ProfileID: identityprofile.ID, SourceSchemaVersion: 5, RequiredFields: fields}
	v["hoopa-program-rules"] = map[string]any{"profile_id": ProgramRuleProfileID}
	v["rules"] = append(append([]string(nil), rules...), "MATCH,node:direct")
	return v
}

func rejectProgramConfig(t *testing.T, raw []byte) {
	t.Helper()
	c, r, err := ValidateDeclaredProgramIdentity(raw, "gen")
	if err != ErrProfile || !reflect.DeepEqual(c, Config{}) || !reflect.DeepEqual(r, identityprofile.Requirements{}) {
		t.Fatalf("invalid config returned data: c=%#v r=%#v err=%v", c, r, err)
	}
	if c, err := ValidateWithProgramIdentity(raw, "gen", actualFields(t)); err != ErrProfile || !reflect.DeepEqual(c, Config{}) {
		t.Fatal("actual profile bypassed bad configuration")
	}
}

func TestProgramRulesExplicitOptInAndOrdering(t *testing.T) {
	rules := []string{
		"PROCESS-NAME,python3,node:http",
		"PROCESS-PATH,/bin/python3,group:main",
		"AND,((PROCESS-NAME,Python3),(DOMAIN,alpha.test),(PROCESS-PATH,/Bin/python3)),REJECT",
		"AND,((IP-CIDR,127.0.0.0/8,no-resolve),(PROCESS-NAME,python3)),node:direct",
	}
	v := programConfig(t, []string{"program_name", "program_path"}, rules...)
	raw := encode(t, v)
	c, r, err := ValidateDeclaredProgramIdentity(raw, "gen")
	if err != nil || c.ProgramRules != ProgramRuleProfileID || !reflect.DeepEqual(c.Rules, v["rules"]) || !reflect.DeepEqual(r.RequiredFields, []string{"program_name", "program_path"}) {
		t.Fatalf("declaration/order: %#v %#v %v", c, r, err)
	}
	actual, err := ValidateWithProgramIdentity(raw, "gen", actualFields(t))
	if err != nil || !reflect.DeepEqual(c, actual) {
		t.Fatal("valid adopted profile rejected", err)
	}
	serialized, err := json.Marshal(c)
	if err != nil || bytes.Contains(serialized, []byte("ProgramRules")) || bytes.Contains(serialized, []byte(ProgramRuleProfileID)) {
		t.Fatal("internal opt-in field serialized")
	}
	if _, err := ValidateWithProgramIdentity(raw, "gen", identityprofile.Profile{}); err == nil {
		t.Fatal("missing adopted profile accepted")
	}
	for _, generation := range []string{"", "bad generation"} {
		if _, _, err := ValidateDeclaredProgramIdentity(raw, generation); err == nil {
			t.Fatal("invalid immutable generation accepted")
		}
	}
	if _, err := Validate(raw, "gen"); err == nil {
		t.Fatal("legacy Validate accepted opt-in")
	}
	if _, _, err := ValidateDeclaredIdentity(raw, "gen"); err == nil {
		t.Fatal("legacy declaration accepted opt-in")
	}
	if _, err := ValidateWithIdentity(raw, "gen", actualFields(t)); err == nil {
		t.Fatal("legacy adopted profile accepted opt-in")
	}
	// Opt-in is rejected by the old API even when no program condition exists.
	v = programConfig(t, []string{"instance"})
	if _, _, err := ValidateDeclaredIdentity(encode(t, v), "gen"); err == nil {
		t.Fatal("old API silently ignored opt-in")
	}
}

func TestProgramRulesNoOptInCompatibility(t *testing.T) {
	for _, raw := range [][]byte{[]byte(validProfile), fieldConfig(t, []string{"instance"}), fieldConfig(t, []string{"program_name", "program_path"})} {
		old, oldR, oldErr := ValidateDeclaredIdentity(raw, "gen")
		got, gotR, err := ValidateDeclaredProgramIdentity(raw, "gen")
		if err != oldErr || !reflect.DeepEqual(got, old) || !reflect.DeepEqual(gotR, oldR) || got.ProgramRules != "" {
			t.Fatal("absent opt-in changed declaration behavior")
		}
		for _, actual := range []identityprofile.Profile{actualFields(t), {}} {
			old, oldErr := ValidateWithIdentity(raw, "gen", actual)
			got, err := ValidateWithProgramIdentity(raw, "gen", actual)
			if err != oldErr || !reflect.DeepEqual(got, old) {
				t.Fatal("absent opt-in changed adopted profile behavior")
			}
		}
	}
	for _, rule := range []string{"PROCESS-NAME,python3,node:direct", "PROCESS-PATH,/bin/python3,node:direct", "AND,((DOMAIN,alpha.test),(PROCESS-NAME,python3)),node:direct"} {
		v := programConfig(t, []string{"program_name", "program_path"}, rule)
		delete(v, "hoopa-program-rules")
		raw := encode(t, v)
		rejectProgramConfig(t, raw)
		if _, _, err := ValidateDeclaredIdentity(raw, "gen"); err == nil {
			t.Fatal("old declaration accepted PROCESS without opt-in")
		}
		if _, err := ValidateWithIdentity(raw, "gen", actualFields(t)); err == nil {
			t.Fatal("old adopted profile accepted PROCESS without opt-in")
		}
	}
}

func TestProgramRulesFieldCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		fields     []string
		valid      bool
	}{
		{"name", "PROCESS-NAME,python3,node:direct", []string{"program_name"}, true},
		{"path", "PROCESS-PATH,/bin/python3,node:direct", []string{"program_path"}, true},
		{"name missing", "PROCESS-NAME,python3,node:direct", []string{"program_path"}, false},
		{"path missing", "PROCESS-PATH,/bin/python3,node:direct", []string{"program_name"}, false},
		{"AND missing", "AND,((DOMAIN,alpha.test),(PROCESS-NAME,python3)),node:direct", []string{"instance"}, false},
		{"AND second field missing", "AND,((PROCESS-NAME,python3),(PROCESS-PATH,/bin/python3)),node:direct", []string{"program_name"}, false},
		{"instance not a rule", "PROCESS-ID,42,node:direct", []string{"instance"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := encode(t, programConfig(t, tc.fields, tc.rule))
			if !tc.valid {
				rejectProgramConfig(t, raw)
			} else if _, _, err := ValidateDeclaredProgramIdentity(raw, "gen"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProgramRulesPatternBoundaries(t *testing.T) {
	for i, pattern := range []string{"p", "Python3", "程序", "python*", "python?", "p[3]", strings.Repeat("a", 255), strings.Repeat("界", 85)} {
		t.Run("literal "+strconv.Itoa(i), func(t *testing.T) {
			if _, _, err := ValidateDeclaredProgramIdentity(encode(t, programConfig(t, []string{"program_name"}, "PROCESS-NAME,"+pattern+",REJECT")), "gen"); err != nil {
				t.Fatal("valid exact literal rejected", err)
			}
		})
	}
	for _, pattern := range []string{"/", "/bin/python3", "/bin/Python?", "/" + strings.Repeat("a", 254)} {
		if _, _, err := ValidateDeclaredProgramIdentity(encode(t, programConfig(t, []string{"program_path"}, "PROCESS-PATH,"+pattern+",node:direct")), "gen"); err != nil {
			t.Fatal("valid path rejected", err)
		}
	}
	for i, pattern := range []string{"", "bin/python3", "/python3", "a b", "a\tb", "a\nb", "a\x00b", "a\x7fb", "a\u0085b", "a\u00a0b", "a\u2003b", "a,b", "a(b", "a)b", strings.Repeat("a", 256), strings.Repeat("界", 86)} {
		t.Run("bad name "+strconv.Itoa(i), func(t *testing.T) {
			rejectProgramConfig(t, encode(t, programConfig(t, []string{"program_name"}, "PROCESS-NAME,"+pattern+",node:direct")))
		})
	}
	for _, pattern := range []string{"", "bin/python3", "/a b", "/a\u0085b", "/a,b", "/a(b", "/" + strings.Repeat("a", 255)} {
		rejectProgramConfig(t, encode(t, programConfig(t, []string{"program_path"}, "PROCESS-PATH,"+pattern+",node:direct")))
	}
	if validProgramPattern(string([]byte{0xff}), false) || validProgramPattern("/"+string([]byte{0xff}), true) {
		t.Fatal("invalid UTF8 pattern accepted")
	}
	raw := encode(t, programConfig(t, []string{"program_name"}, "PROCESS-NAME,python3,node:direct"))
	rejectProgramConfig(t, bytes.Replace(raw, []byte("python3"), []byte{0xff}, 1))
}

func TestProgramRulesStrictShapeAndExistingConstraints(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing identity", func(v map[string]any) { delete(v, "hoopa-identity") }},
		{"null optin", func(v map[string]any) { v["hoopa-program-rules"] = nil }},
		{"wrong type", func(v map[string]any) { v["hoopa-program-rules"] = ProgramRuleProfileID }},
		{"missing id", func(v map[string]any) { v["hoopa-program-rules"] = map[string]any{} }},
		{"unknown id", func(v map[string]any) { v["hoopa-program-rules"] = map[string]any{"profile_id": "other"} }},
		{"extra field", func(v map[string]any) {
			v["hoopa-program-rules"] = map[string]any{"profile_id": ProgramRuleProfileID, "version": 1}
		}},
		{"case alias", func(v map[string]any) { v["hoopa-program-rules"] = map[string]any{"Profile_ID": ProgramRuleProfileID} }},
		{"ipv6", func(v map[string]any) { v["ipv6"] = true }},
		{"process lookup", func(v map[string]any) { v["find-process-mode"] = "always" }},
		{"auth", func(v map[string]any) { v["secret"] = "short" }},
		{"listener conflict", func(v map[string]any) { listener(v)["port"] = 19090 }},
		{"unknown config", func(v map[string]any) { v["tun"] = map[string]any{"enable": true} }},
		{"unknown node", func(v map[string]any) { v["rules"] = []string{"PROCESS-NAME,python3,node:missing", "MATCH,REJECT"} }},
		{"missing terminal", func(v map[string]any) { v["rules"] = []string{"PROCESS-NAME,python3,node:direct"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := programConfig(t, []string{"program_name"}, "AND,((PROCESS-NAME,python3),(DOMAIN,alpha.test)),node:direct")
			tc.edit(v)
			rejectProgramConfig(t, encode(t, v))
		})
	}
	raw := encode(t, programConfig(t, []string{"program_name"}, "PROCESS-NAME,python3,node:direct"))
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"hoopa-program-rules":`), []byte(`"hoopa-program-rules":{},"hoopa-program-rules":`), 1),
		bytes.Replace(raw, []byte(`"profile_id":"`+ProgramRuleProfileID+`"`), []byte(`"profile_id":"`+ProgramRuleProfileID+`","profile_id":"`+ProgramRuleProfileID+`"`), 1),
		bytes.Replace(raw, []byte(`"profile_id":"`+ProgramRuleProfileID+`"`), []byte(`"profile_id":"`+ProgramRuleProfileID+`","prof\u0069le_id":"`+ProgramRuleProfileID+`"`), 1),
		append(append([]byte(nil), raw...), []byte(` {}`)...),
	} {
		rejectProgramConfig(t, bad)
	}
	for _, rule := range []string{
		"PROCESS-NAME-WILDCARD,python*,node:direct", "PROCESS-NAME-REGEX,python.*,node:direct", "PROCESS-PATH-REGEX,/bin/.*,node:direct", "UID,65534,node:direct", "PROCESS-NAME,python3,DIRECT", "PROCESS-NAME,python3,node:direct,no-resolve",
		"AND,((PROCESS-NAME,python3)),node:direct", "AND,((PROCESS-NAME,python3),(AND,((DOMAIN,a),(DOMAIN,b)))),node:direct", "OR,((PROCESS-NAME,python3),(DOMAIN,alpha.test)),node:direct", "AND,((UID,1),(PROCESS-NAME,python3)),node:direct",
	} {
		rejectProgramConfig(t, encode(t, programConfig(t, []string{"program_name", "program_path"}, rule)))
	}
}

func TestProgramRulesUnicodeEscapesRemainExact(t *testing.T) {
	raw := encode(t, programConfig(t, []string{"program_name"}, "PROCESS-NAME,python3,node:direct"))
	for _, value := range []string{`\uD800`, `\uDC00`, `\uD800a`, `\uD800\u0041`, `\uDC00\uD800`} {
		rejectProgramConfig(t, bytes.Replace(raw, []byte("python3"), []byte(value), 1))
	}
	for _, tc := range []struct{ raw, want string }{{`\uD83D\uDE00`, "😀"}, {`\\uD800`, `\uD800`}, {`\uFFFD`, "�"}, {`\u7A0B\u5E8F`, "程序"}, {`A\u0062\uD83D\uDE00\\uD800`, `Ab😀\uD800`}} {
		input := bytes.Replace(raw, []byte("python3"), []byte(tc.raw), 1)
		c, _, err := ValidateDeclaredProgramIdentity(input, "gen")
		if err != nil || c.Rules[0] != "PROCESS-NAME,"+tc.want+",node:direct" {
			t.Fatal("valid literal changed", err)
		}
	}
}
