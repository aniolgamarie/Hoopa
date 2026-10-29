// Package identityprofile defines one finite, trusted-candidate field profile.
// Parsing validates declarations and bindings; it does not authenticate a source.
package identityprofile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ID = "linux_amd64_loopback_connect_v1"
const Definition = "connect_initiator_v1"
const HandoffSchema = "hoopa-core-identity-handoff-v4"
const SourceSchemaVersion = 5
const Schema = "sourceidentity-field-profile-v1"

var ErrProfile = errors.New("invalid finite identity field profile")

type Field struct {
	Status string `json:"status"`
	Source string `json:"source"`
}
type Fields struct {
	Instance    Field `json:"instance"`
	ProgramName Field `json:"program_name"`
	ProgramPath Field `json:"program_path"`
	Comm        Field `json:"comm"`
}
type Profile struct {
	Schema              string `json:"schema"`
	ID                  string `json:"id"`
	Definition          string `json:"definition"`
	HandoffSchema       string `json:"handoff_schema"`
	SourceSchemaVersion int    `json:"source_schema_version"`
	ScopeRawHex         string `json:"scope_raw_hex"`
	Fields              Fields `json:"fields"`
}
type Requirements struct {
	ProfileID           string   `json:"profile_id"`
	SourceSchemaVersion int      `json:"source_schema_version"`
	RequiredFields      []string `json:"required_fields"`
}

func fixed(raw string) Profile {
	return Profile{Schema, ID, Definition, HandoffSchema, 5, raw, Fields{
		Field{"available", "linux_bpf_connect_task_v1"}, Field{"available", "executable_path_basename_v1"}, Field{"available", "task_exe_file_d_path_v1"}, Field{"unverified", "linux_bpf_task_comm_v1"}}}
}
func Scope(rawHex, generation string) error {
	raw, err := hex.DecodeString(rawHex)
	if err != nil || len(raw) != 24 || hex.EncodeToString(raw) != rawHex {
		return ErrProfile
	}
	g, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || g == 0 || strconv.FormatUint(g, 10) != generation {
		return ErrProfile
	}
	le := binary.LittleEndian
	if le.Uint64(raw) != g || le.Uint32(raw[8:]) == 0 || le.Uint32(raw[12:]) == 0 || le.Uint32(raw[16:]) != 1 || le.Uint32(raw[20:]) != 0 {
		return ErrProfile
	}
	return nil
}
func New(rawHex, generation string) (Profile, error) {
	if Scope(rawHex, generation) != nil {
		return Profile{}, ErrProfile
	}
	return fixed(rawHex), nil
}
func (p Profile) Validate(generation string) error {
	if p != fixed(p.ScopeRawHex) || Scope(p.ScopeRawHex, generation) != nil {
		return ErrProfile
	}
	return nil
}

// object rejects null, unknown, duplicate and escaped member names recursively.
func object(raw []byte, names string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 8192 || !utf8.Valid(raw) {
		return nil, ErrProfile
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, ErrProfile
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		before := d.InputOffset()
		t, e = d.Token()
		name, ok := t.(string)
		if e != nil || !ok {
			return nil, ErrProfile
		}
		keyRaw := raw[before:d.InputOffset()]
		if bytes.ContainsRune(keyRaw, '\\') {
			return nil, ErrProfile
		}
		if _, ok = out[name]; ok {
			return nil, ErrProfile
		}
		var v json.RawMessage
		if d.Decode(&v) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, ErrProfile
		}
		out[name] = v
	}
	if _, e = d.Token(); e != nil {
		return nil, ErrProfile
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrProfile
	}
	want := strings.Fields(names)
	if len(out) != len(want) {
		return nil, ErrProfile
	}
	for _, n := range want {
		if _, ok := out[n]; !ok {
			return nil, ErrProfile
		}
	}
	return out, nil
}
func Decode(raw []byte, generation string) (Profile, error) {
	f, e := object(raw, "schema id definition handoff_schema source_schema_version scope_raw_hex fields")
	if e != nil {
		return Profile{}, e
	}
	fields, e := object(f["fields"], "instance program_name program_path comm")
	if e != nil {
		return Profile{}, e
	}
	for _, v := range fields {
		if _, e = object(v, "status source"); e != nil {
			return Profile{}, e
		}
	}
	var p Profile
	if json.Unmarshal(raw, &p) != nil || !bytes.Equal(bytes.TrimSpace(f["source_schema_version"]), []byte("5")) || p.Validate(generation) != nil {
		return Profile{}, ErrProfile
	}
	return p, nil
}
func DecodeRequirements(raw []byte) (Requirements, error) {
	f, e := object(raw, "profile_id source_schema_version required_fields")
	if e != nil {
		return Requirements{}, e
	}
	var r Requirements
	if json.Unmarshal(raw, &r) != nil || r.ProfileID != ID || !bytes.Equal(bytes.TrimSpace(f["source_schema_version"]), []byte("5")) || r.SourceSchemaVersion != 5 || len(r.RequiredFields) < 1 || len(r.RequiredFields) > 4 {
		return Requirements{}, ErrProfile
	}
	seen := map[string]bool{}
	for _, n := range r.RequiredFields {
		if seen[n] || (n != "instance" && n != "program_name" && n != "program_path") {
			return Requirements{}, ErrProfile
		}
		seen[n] = true
	}
	return r, nil
}
func (r Requirements) Match(p Profile) error {
	if p.ID != r.ProfileID || p.SourceSchemaVersion != r.SourceSchemaVersion || p.Validate(scopeGeneration(p.ScopeRawHex)) != nil {
		return ErrProfile
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return ErrProfile
	}
	_, e = DecodeRequirements(raw)
	return e
}
func scopeGeneration(raw string) string {
	b, e := hex.DecodeString(raw)
	if e != nil || len(b) != 24 {
		return ""
	}
	return strconv.FormatUint(binary.LittleEndian.Uint64(b), 10)
}
