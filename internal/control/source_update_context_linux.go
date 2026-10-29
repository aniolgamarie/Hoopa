//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

var ErrSourceContext = errors.New("SOURCE_UPDATE_CONTEXT_INVALID")

const maxSourceUpdateContextBytes = 64 << 10

// sourceUpdateContext is immutable download-input evidence once encoded and
// stored. It contains no key, URL secret or provider body and grants no network,
// runtime or commit authority. Publication and journal ordering belong elsewhere.
type sourceUpdateContext struct {
	Version        int                       `json:"schema_version"`
	Kind           string                    `json:"kind"`
	OperationID    config.ID                 `json:"operation_id"`
	RequestDigest  string                    `json:"request_digest"`
	Base           config.ActivePointer      `json:"base"`
	Source         config.Subscription       `json:"source"`
	RegionRules    []subscription.RegionRule `json:"region_rules"`
	SettingsSHA256 string                    `json:"settings_sha256"`
	PreparedSHA256 string                    `json:"prepared_sha256"`
	Sequence       uint64                    `json:"sequence"`
	StartedAt      string                    `json:"started_at"`
	NextDue        *string                   `json:"next_due"`
}

func (sourceUpdateContext) String() string   { return "control.sourceUpdateContext{private material}" }
func (sourceUpdateContext) GoString() string { return "control.sourceUpdateContext{private material}" }

func encodeSourceUpdateContext(value sourceUpdateContext) ([]byte, error) {
	if !validSourceUpdateContext(value) {
		return nil, ErrSourceContext
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxSourceUpdateContextBytes {
		return nil, ErrSourceContext
	}
	return raw, nil
}
func decodeSourceUpdateContext(raw []byte) (sourceUpdateContext, error) {
	if len(raw) == 0 || len(raw) > maxSourceUpdateContextBytes {
		return sourceUpdateContext{}, ErrSourceContext
	}
	var value sourceUpdateContext
	if json.Unmarshal(raw, &value) != nil {
		return sourceUpdateContext{}, ErrSourceContext
	}
	canonical, err := encodeSourceUpdateContext(value)
	// Full exact representation also rejects duplicate/unknown/missing/null fields,
	// lossy Unicode decoding and alternate escapes, numbers or field ordering.
	if err != nil || !bytes.Equal(canonical, raw) {
		return sourceUpdateContext{}, ErrSourceContext
	}
	return value, nil
}
func validSourceUpdateContext(v sourceUpdateContext) bool {
	if v.Version != 1 || v.Kind != "source-update-context" || !strings.HasPrefix(string(v.OperationID), "op-") || !isHash(strings.TrimPrefix(string(v.OperationID), "op-")) || !isHash(v.RequestDigest) || !isHash(v.SettingsSHA256) || !isHash(v.PreparedSHA256) || v.Sequence == 0 || v.Sequence > (1<<53)-1 {
		return false
	}
	if !sourceContextBase(v.Base) || !sourceContextID(string(v.Source.ID)) || !validSourceRegionRules(v.RegionRules) {
		return false
	}
	source := v.Source.Source
	switch source.Kind {
	case "file":
		if !sourceContextPath(source.Path) || source.URLSecretRef != "" {
			return false
		}
	case "https":
		if !sourceContextPath(source.URLSecretRef) || source.Path != "" {
			return false
		}
	default:
		return false
	}
	start, ok := sourceContextTime(v.StartedAt)
	if !ok {
		return false
	}
	if v.Source.UpdateHours == nil {
		return v.NextDue == nil
	}
	hours := *v.Source.UpdateHours
	if hours == 0 || hours > uint64(math.MaxInt64/int64(time.Hour)) || v.NextDue == nil {
		return false
	}
	due := start.Add(time.Duration(hours) * time.Hour)
	if _, err := due.MarshalJSON(); err != nil {
		return false
	}
	if _, ok := sourceContextTime(*v.NextDue); !ok {
		return false
	}
	return due.UTC().Format(time.RFC3339Nano) == *v.NextDue
}
func sourceContextBase(b config.ActivePointer) bool {
	if b.Version != 1 || !sourceContextID(string(b.Revision)) || (b.DesiredRuntime != "running" && b.DesiredRuntime != "stopped") {
		return false
	}
	if b.OperationID != nil && !sourceContextID(string(*b.OperationID)) {
		return false
	}
	if b.Generation == nil {
		return b.ManifestSHA256 == "" && b.DesiredRuntime == "stopped"
	}
	return sourceContextID(string(*b.Generation)) && isHash(b.ManifestSHA256) && b.OperationID != nil
}
func sourceContextID(s string) bool { _, err := config.ParseID(s); return err == nil }
func sourceContextPath(s string) bool {
	return utf8.ValidString(s) && filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n") && !strings.Contains(s, "://")
}
func sourceContextText(s string, max int) bool {
	if len(s) == 0 || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validSourceRegionRules(rules []subscription.RegionRule) bool {
	if rules == nil || len(rules) > 64 {
		return false
	}
	for _, r := range rules {
		if !sourceContextText(r.Prefix, 256) || !sourceContextText(r.Region, 64) {
			return false
		}
	}
	return true
}

func sourceContextTime(s string) (time.Time, bool) {
	value, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || value.IsZero() || value.UTC().Format(time.RFC3339Nano) != s {
		return time.Time{}, false
	}
	if _, err = value.MarshalJSON(); err != nil {
		return time.Time{}, false
	}
	return value, true
}
