//go:build linux && amd64

package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"hoopa/internal/config"
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

var ErrSourceSettings = errors.New("SOURCE_SETTINGS_INVALID")

// SourceSettings owns immutable private initialization input. It neither installs
// the record nor establishes first-install history or identity-key continuity.
type SourceSettings struct {
	raw   string
	value sourceSettingsDTO
}
type sourceSettingsDTO struct {
	SchemaVersion int                   `json:"schema_version"`
	IdentityKey   []byte                `json:"identity_key"`
	Runtime       sourceSettingsRuntime `json:"runtime"`
}
type sourceSettingsRuntime struct {
	Controller string        `json:"controller"`
	Secret     string        `json:"secret"`
	Entry      profile.Entry `json:"entry"`
}

func NewSourceSettings(key []byte, runtime policy.RuntimeInput) (SourceSettings, error) {
	if len(key) != 32 || len(runtime.AppEntries) != 0 {
		return SourceSettings{}, ErrSourceSettings
	}
	runtime.Entry.Users = append([]profile.User(nil), runtime.Entry.Users...)
	return sourceSettingsFromDTO(sourceSettingsDTO{1, bytes.Clone(key), sourceSettingsRuntime{runtime.Controller, runtime.Secret, runtime.Entry}})
}
func DecodeSourceSettings(raw []byte) (SourceSettings, error) {
	if len(raw) == 0 || len(raw) > config.MaxSourceSettingsBytes {
		return SourceSettings{}, ErrSourceSettings
	}
	var dto sourceSettingsDTO
	if json.Unmarshal(raw, &dto) != nil {
		return SourceSettings{}, ErrSourceSettings
	}
	settings, err := sourceSettingsFromDTO(dto)
	// Comparing the full writer output also rejects duplicate/unknown/missing/null
	// fields, Unicode replacement and alternate representations or whitespace.
	if err != nil || !bytes.Equal(raw, []byte(settings.raw)) {
		return SourceSettings{}, ErrSourceSettings
	}
	return settings, nil
}
func sourceSettingsFromDTO(dto sourceSettingsDTO) (SourceSettings, error) {
	if dto.SchemaVersion != 1 || len(dto.IdentityKey) != 32 {
		return SourceSettings{}, ErrSourceSettings
	}
	// Reuse the finite generated-profile validator for endpoint, credential and
	// entry semantics; no core, filesystem, entropy or installed binary is involved.
	candidate := profile.Config{Mode: "rule", LogLevel: "silent", FindProcessMode: "off", Controller: dto.Runtime.Controller, Secret: dto.Runtime.Secret, Listeners: []profile.Entry{dto.Runtime.Entry}, Proxies: []map[string]any{}, Groups: []profile.Group{}, Rules: []string{"MATCH,REJECT"}}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return SourceSettings{}, ErrSourceSettings
	}
	if _, err = profile.Validate(raw, "settings-check"); err != nil {
		return SourceSettings{}, ErrSourceSettings
	}
	raw, err = json.Marshal(dto)
	if err != nil || len(raw) > config.MaxSourceSettingsBytes {
		return SourceSettings{}, ErrSourceSettings
	}
	return SourceSettings{raw: string(raw), value: dto}, nil
}
func (s SourceSettings) SensitiveBytes() ([]byte, error) {
	if s.raw == "" {
		return nil, ErrSourceSettings
	}
	return []byte(s.raw), nil
}
func (s SourceSettings) SensitiveIdentityKey() ([]byte, error) {
	if s.raw == "" {
		return nil, ErrSourceSettings
	}
	return bytes.Clone(s.value.IdentityKey), nil
}
func (s SourceSettings) Runtime() (policy.RuntimeInput, error) {
	if s.raw == "" {
		return policy.RuntimeInput{}, ErrSourceSettings
	}
	r := s.value.Runtime
	r.Entry.Users = append([]profile.User(nil), r.Entry.Users...)
	return policy.RuntimeInput{Controller: r.Controller, Secret: r.Secret, Entry: r.Entry}, nil
}
func (s SourceSettings) Digest() string {
	if s.raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s.raw))
	return hex.EncodeToString(h[:])
}
func (SourceSettings) String() string   { return "control.SourceSettings{private material}" }
func (SourceSettings) GoString() string { return "control.SourceSettings{private material}" }
