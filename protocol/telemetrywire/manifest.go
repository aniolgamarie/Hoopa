package telemetrywire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/protocol/identityprofile"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var ErrInvalidManifest = errors.New("invalid source manifest")

// Manifest is the existing bounded P0 profile. Decode checks the identity
// digest, not whether the historical binary or process still exists.
type Manifest struct {
	SchemaVersion                                   uint8
	SourceSchemaVersion                             uint8
	IdentityProfile                                 string
	SourceEpoch, SourceInstanceID, ConfigGeneration string
	SHA256                                          string
	MaxSourceBytes                                  uint64
	Raw                                             json.RawMessage
}

func hexBytes(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n && hex.EncodeToString(b) == s
}

func DecodeManifest(raw []byte) (Manifest, error) {
	bad := func() (Manifest, error) { return Manifest{}, ErrInvalidManifest }
	f, ok := object(raw)
	fields := "schema_version source_epoch binary_path binary_sha256 boot_id pid start_ticks nonce config_generation max_source_bytes max_frame_bytes max_active_flows reclamation scope"
	version := uint8(1)
	scope := "linux_amd64_tcp_same_uid_netns_ipv4_loopback"
	if strings.TrimSpace(string(f["schema_version"])) == "2" {
		version = 2
		fields += " source_schema_version identity_profile"
		scope = "linux_amd64_tcp_scoped_connect_initiator_ipv4_loopback"
	} else if strings.TrimSpace(string(f["schema_version"])) != "1" {
		return bad()
	}
	if !ok || len(raw) > 8192 || !utf8.Valid(raw) || !exactFields(f, fields) || version == 2 && (!literal(f, "identity_profile", identityprofile.ID) || strings.TrimSpace(string(f["source_schema_version"])) != "5") {
		return bad()
	}
	epoch, ok := id(f["source_epoch"])
	if !ok || !strings.HasPrefix(epoch, "epoch:") || !hexBytes(strings.TrimPrefix(epoch, "epoch:"), 32) {
		return bad()
	}
	exe, a := scalarText(f["binary_path"])
	binaryHash, b := scalarText(f["binary_sha256"])
	boot, c := scalarText(f["boot_id"])
	nonce, d := scalarText(f["nonce"])
	start, e := scalarText(f["start_ticks"])
	generation, g := id(f["config_generation"])
	pid, h := decimal(f["pid"], false, true)
	if !a || !b || !c || !d || !e || !g || !h || !filepath.IsAbs(exe) || !hexBytes(binaryHash, 32) || !hexBytes(nonce, 16) || pid > 1<<31-1 {
		return bad()
	}
	if len(boot) != 36 || boot[8] != '-' || boot[13] != '-' || boot[18] != '-' || boot[23] != '-' || !hexBytes(strings.ReplaceAll(boot, "-", ""), 16) {
		return bad()
	}
	if _, err := parseBytes(start); err != nil {
		return bad()
	}
	limit, ok := decimal(f["max_source_bytes"], false, true)
	frame, frameOK := decimal(f["max_frame_bytes"], false, true)
	active, activeOK := decimal(f["max_active_flows"], false, true)
	if !ok || limit < 20*(8192+36) || limit > 8*1024*1024 || !frameOK || frame != 8192 || !activeOK || active != MaxActiveFlows || !literal(f, "reclamation", "not_implemented_bounded_stop") || !literal(f, "scope", scope) {
		return bad()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%s|%s", exe, binaryHash, boot, pid, start, nonce)))
	if epoch != "epoch:"+hex.EncodeToString(digest[:]) {
		return bad()
	}
	sum := sha256.Sum256(raw)
	return Manifest{SchemaVersion: version, SourceSchemaVersion: func() uint8 {
		if version == 2 {
			return 5
		}
		return 0
	}(), IdentityProfile: func() string {
		if version == 2 {
			return identityprofile.ID
		}
		return ""
	}(), SourceEpoch: epoch, SourceInstanceID: "source:" + strings.TrimPrefix(epoch, "epoch:"), ConfigGeneration: generation, SHA256: hex.EncodeToString(sum[:]), MaxSourceBytes: limit, Raw: append(json.RawMessage(nil), raw...)}, nil
}

// ValidateManifestEvent binds an already strictly decoded original frame to its
// immutable manifest. Invoke before committing that frame to lifecycle state.
func ValidateManifestEvent(m Manifest, e SourceEvent) error {
	if e.SourceEpoch != m.SourceEpoch || e.SourceInstanceID != m.SourceInstanceID || e.ConfigGeneration != m.ConfigGeneration {
		return ErrInvalidManifest
	}
	if m.SchemaVersion == 2 {
		if e.SchemaVersion != 5 || m.SourceSchemaVersion != 5 || m.IdentityProfile != identityprofile.ID {
			return ErrInvalidManifest
		}
		if e.Kind == "opened" && (e.FieldProfile == nil || e.FieldProfile.ID != m.IdentityProfile) {
			return ErrInvalidManifest
		}
	} else if m.SchemaVersion != 1 || e.SchemaVersion == 5 {
		return ErrInvalidManifest
	}
	return nil
}
