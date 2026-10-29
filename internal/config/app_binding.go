//go:build linux

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	profile "hoopa/protocol/sourceprofile"
)

const MaxAppBindingBytes = 16 << 10

// ErrAppBinding never includes private record, endpoint or credential material.
var ErrAppBinding error = appBindingFailure{}

type appBindingFailure struct{}

func (appBindingFailure) Error() string    { return "APP_BINDING_INVALID" }
func (appBindingFailure) String() string   { return "APP_BINDING_INVALID" }
func (appBindingFailure) GoString() string { return "APP_BINDING_INVALID" }

// AppBinding is immutable private input, not evidence that settings exist or
// that an endpoint/credential is distinct from another retained binding.
type AppBinding struct {
	raw   string
	value appBindingRecord
}
type appBindingRecord struct {
	SchemaVersion  int           `json:"schema_version"`
	AppID          ID            `json:"app_id"`
	SettingsSHA256 string        `json:"source_settings_sha256"`
	Entry          profile.Entry `json:"entry"`
}

// AppEntryAlias is the deterministic entry name; its caller still validates
// the ID at its authority boundary. It conveys no original-process identity.
func AppEntryAlias(id ID) string {
	sum := sha256.Sum256([]byte(id))
	return "app:" + hex.EncodeToString(sum[:])
}

func NewAppBinding(appID ID, settingsSHA256 string, entry profile.Entry) (AppBinding, error) {
	return appBindingFromRecord(appBindingRecord{1, appID, settingsSHA256, entry})
}
func DecodeAppBinding(raw []byte) (AppBinding, error) {
	if len(raw) == 0 || len(raw) > MaxAppBindingBytes {
		return AppBinding{}, ErrAppBinding
	}
	var record appBindingRecord
	if json.Unmarshal(raw, &record) != nil {
		return AppBinding{}, ErrAppBinding
	}
	binding, err := appBindingFromRecord(record)
	// Only the exact writer bytes are admitted. Regeneration also rejects
	// case-folded fields, unknown/duplicate/missing/null fields, replacement
	// Unicode, alternate escapes/numbers and whitespace without another parser.
	if err != nil || !bytes.Equal(raw, []byte(binding.raw)) {
		return AppBinding{}, ErrAppBinding
	}
	return binding, nil
}
func appBindingFromRecord(record appBindingRecord) (AppBinding, error) {
	if record.SchemaVersion != 1 {
		return AppBinding{}, ErrAppBinding
	}
	if _, err := ParseID(string(record.AppID)); err != nil {
		return AppBinding{}, ErrAppBinding
	}
	if !appBindingHash(record.SettingsSHA256) {
		return AppBinding{}, ErrAppBinding
	}
	alias := AppEntryAlias(record.AppID)
	if record.Entry.Name == "" {
		record.Entry.Name = alias
	}
	entry := record.Entry
	if entry.Name != alias || entry.Type != "mixed" || entry.Listen != "127.0.0.1" || entry.Port == 0 || entry.UDP || len(entry.Users) != 1 {
		return AppBinding{}, ErrAppBinding
	}
	user := entry.Users[0]
	if !appBindingToken(user.Username) || strings.Contains(user.Username, ":") || !appBindingCredential(user.Password) {
		return AppBinding{}, ErrAppBinding
	}
	// Clone only after the bounded single-user shape is established. No caller
	// slice is retained, and invalid arbitrarily large user slices are not copied.
	record.Entry.Users = append([]profile.User{}, entry.Users...)
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > MaxAppBindingBytes {
		return AppBinding{}, ErrAppBinding
	}
	return AppBinding{raw: string(raw), value: record}, nil
}
func appBindingHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// These two narrow validators deliberately mirror sourceprofile's private
// token/credential ASCII grammar. No broader profile/settings approval occurs.
func appBindingToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i, c := range []byte(value) {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || !strings.ContainsRune("._:-", rune(c))) {
			return false
		}
	}
	return true
}
func appBindingCredential(value string) bool {
	if len(value) < 32 || len(value) > 128 {
		return false
	}
	for _, c := range []byte(value) {
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
func (b AppBinding) SensitiveBytes() ([]byte, error) {
	if b.raw == "" {
		return nil, ErrAppBinding
	}
	return []byte(b.raw), nil
}
func (b AppBinding) Digest() string {
	if b.raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(b.raw))
	return hex.EncodeToString(sum[:])
}
func (b AppBinding) AppID() ID {
	if b.raw == "" {
		return ""
	}
	return b.value.AppID
}
func (b AppBinding) SettingsSHA256() string {
	if b.raw == "" {
		return ""
	}
	return b.value.SettingsSHA256
}
func (b AppBinding) SensitiveEntry() (profile.Entry, error) {
	if b.raw == "" {
		return profile.Entry{}, ErrAppBinding
	}
	entry := b.value.Entry
	entry.Users = append([]profile.User{}, entry.Users...)
	return entry, nil
}
func (AppBinding) String() string   { return "config.AppBinding{private material}" }
func (AppBinding) GoString() string { return "config.AppBinding{private material}" }
