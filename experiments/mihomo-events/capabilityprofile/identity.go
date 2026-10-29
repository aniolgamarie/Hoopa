package capabilityprofile

import (
	"bytes"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"io"
)

// ValidateDeclaredIdentity parses the complete protected configuration and the
// explicit field requirements. It grants nothing; startup must subsequently
// match the adopted Owner with ValidateWithIdentity before preparing listeners.
func ValidateDeclaredIdentity(raw []byte, generation string) (Config, identityprofile.Requirements, error) {
	bad := func() (Config, identityprofile.Requirements, error) {
		return Config{}, identityprofile.Requirements{}, ErrProfile
	}
	if len(raw) == 0 || len(raw) > 64*1024 {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return bad()
	}
	if _, err = d.Token(); err != io.EOF {
		return bad()
	}
	root, ok := v.(map[string]any)
	if !ok {
		return bad()
	}
	_, ok = root["hoopa-identity"]
	if !ok {
		return bad()
	}
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil {
		return bad()
	}
	b := original["hoopa-identity"]
	requirements, err := identityprofile.DecodeRequirements(b)
	if err != nil {
		return bad()
	}
	delete(root, "hoopa-identity")
	b, err = json.Marshal(root)
	if err != nil {
		return bad()
	}
	c, err := Validate(b, generation)
	if err != nil {
		return bad()
	}
	return c, requirements, nil
}

// ValidateWithIdentity checks the same raw complete config against values from a
// successfully adopted Owner. Legacy profiles continue to use Validate.
func ValidateWithIdentity(raw []byte, generation string, actual identityprofile.Profile) (Config, error) {
	c, r, err := ValidateDeclaredIdentity(raw, generation)
	if err != nil || r.Match(actual) != nil {
		return Config{}, ErrProfile
	}
	return c, nil
}
