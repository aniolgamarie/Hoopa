package capabilityprofile

import (
	"bytes"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ProgramRuleProfileID = "connect_initiator_exact_program_v1"

// ValidateDeclaredProgramIdentity accepts exact original-connector program
// conditions only with explicit opt-in and the corresponding declared fields.
// It grants no runtime capability; the adopted profile must still be matched.
func ValidateDeclaredProgramIdentity(raw []byte, generation string) (Config, identityprofile.Requirements, error) {
	bad := func() (Config, identityprofile.Requirements, error) {
		return Config{}, identityprofile.Requirements{}, ErrProfile
	}
	if len(raw) == 0 || len(raw) > 64*1024 {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := readValue(d, 0)
	if err != nil {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	root, ok := value.(map[string]any)
	if !ok {
		return bad()
	}
	if !has(root, "hoopa-program-rules") {
		return ValidateDeclaredIdentity(raw, generation)
	}
	opt, ok := object(root["hoopa-program-rules"], "profile_id", "")
	if !ok || opt["profile_id"] != ProgramRuleProfileID || !validProgramJSON(raw) {
		return bad()
	}
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		return bad()
	}
	requirements, err := identityprofile.DecodeRequirements(original["hoopa-identity"])
	if err != nil {
		return bad()
	}
	required := make(map[string]bool, len(requirements.RequiredFields))
	for _, field := range requirements.RequiredFields {
		required[field] = true
	}
	checkAtom := func(parts []string, entries map[string]bool) bool {
		field := ""
		switch parts[0] {
		case "PROCESS-NAME":
			field = "program_name"
		case "PROCESS-PATH":
			field = "program_path"
		default:
			return atom(parts, entries)
		}
		return len(parts) == 2 && required[field] && validProgramPattern(parts[1], field == "program_path")
	}
	delete(root, "hoopa-program-rules")
	delete(root, "hoopa-identity")
	profileRaw, err := json.Marshal(root)
	if err != nil {
		return bad()
	}
	c, err := validateRules(profileRaw, generation, func(rule string, target func(string) bool, entries map[string]bool) bool {
		return validRuleWithAtom(rule, target, entries, checkAtom)
	})
	if err != nil {
		return bad()
	}
	c.ProgramRules = ProgramRuleProfileID
	return c, requirements, nil
}

// ValidateWithProgramIdentity additionally verifies the adopted field profile.
// Without opt-in it has the same behavior as ValidateWithIdentity.
func ValidateWithProgramIdentity(raw []byte, generation string, actual identityprofile.Profile) (Config, error) {
	c, requirements, err := ValidateDeclaredProgramIdentity(raw, generation)
	if err != nil || requirements.Match(actual) != nil {
		return Config{}, ErrProfile
	}
	return c, nil
}

func validProgramPattern(pattern string, path bool) bool {
	if len(pattern) < 1 || len(pattern) > 255 || !utf8.ValidString(pattern) || strings.ContainsAny(pattern, ",()") {
		return false
	}
	if path {
		if !strings.HasPrefix(pattern, "/") {
			return false
		}
	} else if strings.Contains(pattern, "/") {
		return false
	}
	for _, r := range pattern {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// encoding/json replaces malformed Unicode escapes with U+FFFD. Explicit
// opt-in must reject that replacement rather than silently change a pattern.
// The JSON parser already checked syntax before this bounded byte scan.
func validProgramJSON(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		value, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil || value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+12 > len(raw) || string(raw[i+6:i+8]) != "\\u" {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 12
		} else {
			i += 6
		}
	}
	return true
}
