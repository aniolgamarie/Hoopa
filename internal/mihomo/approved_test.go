package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestApprovedBasicCapabilityArtifactBinding(t *testing.T) {
	// This verifies the internal factory's declaration against accepted artifact
	// bytes. It does not execute a core or upgrade earlier synthetic runtime tests.
	raw, err := os.ReadFile(filepath.Join("..", "..", "experiments", "mihomo-events", "approved-basic-core-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Schema       string                           `json:"schema"`
		Status       string                           `json:"status"`
		Binary       struct{ Version, SHA256 string } `json:"binary"`
		GOOS         string                           `json:"goos"`
		GOARCH       string                           `json:"goarch"`
		Protocol     int                              `json:"protocol"`
		Mode         string                           `json:"mode"`
		Capabilities []string                         `json:"capabilities"`
	}
	if err = json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Schema != "hoopa-approved-basic-core-v1" || artifact.Status != "accepted_bounded_basic_profile" {
		t.Fatal("artifact no longer grants this finite profile")
	}
	path := filepath.Join(t.TempDir(), "not-installed-core")
	record, err := ApprovedBasicCapability(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if record.EvidenceSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("factory evidence digest does not bind original artifact bytes")
	}
	if record.SchemaVersion != 1 || !record.Accepted || record.Binary.Path != path || record.Binary.Version != artifact.Binary.Version || record.Binary.SHA256 != artifact.Binary.SHA256 || record.GOOS != artifact.GOOS || record.GOARCH != artifact.GOARCH || record.Protocol != artifact.Protocol || record.Mode != artifact.Mode || !reflect.DeepEqual(record.Capabilities, artifact.Capabilities) {
		t.Fatal("factory declaration differs from approved artifact")
	}
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("factory touched nonexistent installation", err)
	}
}

func TestApprovedBasicCapabilityLocalPathAndDetachedSlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deliberately-not-a-core")
	sentinel := []byte("factory must not read, rewrite, chmod, or execute local bytes")
	if err := os.WriteFile(path, sentinel, 0000); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApprovedBasicCapability(path)
	if err != nil {
		t.Fatal("trusted declaration unexpectedly requires local executable IO", err)
	}
	second, err := ApprovedBasicCapability(filepath.Join(dir, "different-installation"))
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest == secondDigest || first.Binary.Path == second.Binary.Path || first.EvidenceSHA256 != second.EvidenceSHA256 || first.Binary.SHA256 != second.Binary.SHA256 {
		t.Fatal("local path is not independently bound by capability digest")
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		t.Fatal("declaration modified local metadata")
	}
	children, err := os.ReadDir(dir)
	if err != nil || len(children) != 1 {
		t.Fatal("factory created local files", err)
	}
	// Restore access only in this test fixture, then verify original content. The
	// production factory never repairs permissions or verifies installed bytes.
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(sentinel) {
		t.Fatal("factory changed local bytes", err)
	}
	first.Capabilities[0] = "caller-mutated"
	again, err := ApprovedBasicCapability(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Capabilities, second.Capabilities) || again.Capabilities[0] == first.Capabilities[0] {
		t.Fatal("factory returned shared mutable capabilities")
	}
}

func TestApprovedBasicCapabilityPathShape(t *testing.T) {
	for _, path := range []string{"", "relative-core", "./core", "/tmp/../core", "/tmp/core/", "/tmp/nul\x00core", "/" + strings.Repeat("x", 4096)} {
		if record, err := ApprovedBasicCapability(path); !errors.Is(err, ErrCapability) || !reflect.DeepEqual(record, CapabilityRecord{}) {
			t.Fatal("invalid path produced trusted capability")
		}
	}
	// The factory does not demand a pre-existing filesystem object. The later
	// pinned-FD launcher is responsible for safe traversal and actual hash/ELF.
	if record, err := ApprovedBasicCapability("/" + strings.Repeat("x", 4095)); err != nil || len(record.Binary.Path) != 4096 {
		t.Fatal("documented path bound rejected", err)
	}
}
