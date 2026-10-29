package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"hoopa/internal/config"
)

var ErrCapability = errors.New("CORE_CAPABILITY_UNVERIFIED")

// CapabilityRecord is an installed, reviewed local capability declaration. Its
// digest is pinned by the immutable bundle; a core's own claim cannot mint one.
// EvidenceSHA256 identifies the accepted evidence artifact, not an API version.
// Shape validation covers the finite basic-v1, sources-v2 and separately
// qualified app-routing-v2 profiles.
// Approval lookup separately binds each granted profile to reviewed evidence.
type CapabilityRecord struct {
	SchemaVersion  int                    `json:"schema_version"`
	Binary         config.BinaryReference `json:"binary"`
	GOOS           string                 `json:"goos"`
	GOARCH         string                 `json:"goarch"`
	Protocol       int                    `json:"protocol"`
	Mode           string                 `json:"mode"`
	Capabilities   []string               `json:"capabilities"`
	EvidenceSHA256 string                 `json:"evidence_sha256"`
	Accepted       bool                   `json:"accepted"`
}

func sha256String(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func isDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}
func (c CapabilityRecord) Validate() error {
	b := c.Binary
	if c.SchemaVersion != 1 || c.GOOS != "linux" || c.GOARCH != "amd64" || !c.Accepted || !isDigest(c.EvidenceSHA256) || !isDigest(b.SHA256) || !filepath.IsAbs(b.Path) || filepath.Clean(b.Path) != b.Path || len(b.Path) > 4096 || strings.ContainsRune(b.Path, 0) || b.Version == "" || !utf8.ValidString(b.Version) || len(b.Version) > 128 || strings.ContainsRune(b.Version, 0) {
		return ErrCapability
	}
	want := []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
	switch {
	case c.Protocol == 1 && c.Mode == "basic":
	case c.Protocol == 2 && c.Mode == "basic-sources":
		want = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"}
	case c.Protocol == 2 && c.Mode == "basic-app-routing":
		want = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "ordered-app-rules-v1", "probe-qualified-groups-v1", "unknown-ip-reject-v1"}
	default:
		return ErrCapability
	}
	if len(c.Capabilities) != len(want) {
		return ErrCapability
	}
	for i := range want {
		if c.Capabilities[i] != want[i] {
			return ErrCapability
		}
	}
	return nil
}
func (c CapabilityRecord) Digest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", ErrCapability
	}
	return sha256String(raw), nil
}
