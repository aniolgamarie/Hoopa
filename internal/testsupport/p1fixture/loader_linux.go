//go:build linux && amd64 && p1integration

package p1fixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/mihomo"
	wire "hoopa/protocol/corecontrol"
)

const descriptorPath = "testdata/runtime/basic-verified-bundle.json"
const descriptorSHA256 = "823680e294e43f10dede3c7cd772700b1a82db5360b6aa1e0b545be2a4f789bc"
const approvedCoreSHA256 = "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb"
const descriptorLimit = 32 << 10

type fileReference struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type bundleReference struct {
	Config       fileReference `json:"source_config"`
	Expected     fileReference `json:"independent_expected_snapshot"`
	Encoding     string        `json:"config_encoding"`
	ConfigBytes  int64         `json:"decoded_config_bytes"`
	ConfigSHA    string        `json:"decoded_config_sha256"`
	SnapshotSHA  string        `json:"snapshot_sha256"`
	Entries      []string      `json:"application_entries"`
	Complete     bool          `json:"complete_bundle_required"`
	RemoveRules  bool          `json:"rules_may_be_removed"`
	ProgramRules bool          `json:"contains_program_rules"`
	Converted    bool          `json:"production_format_conversion_executed"`
	Executed     bool          `json:"product_runtime_executed"`
}
type descriptor struct {
	Schema   string                     `json:"schema"`
	Status   string                     `json:"status"`
	Mode     string                     `json:"mode"`
	Protocol int                        `json:"control_protocol"`
	CoreSHA  string                     `json:"core_sha256"`
	Baseline string                     `json:"baseline_bundle"`
	Target   string                     `json:"apply_target_bundle"`
	Bundles  map[string]bundleReference `json:"bundles"`
}
type finiteIntent struct {
	SchemaVersion      int                    `json:"schema_version"`
	Kind               string                 `json:"kind"`
	BundleKey          string                 `json:"bundle_key"`
	ConfigSHA256       string                 `json:"config_sha256"`
	SnapshotSHA256     string                 `json:"snapshot_sha256"`
	CoreBinary         config.BinaryReference `json:"core_binary"`
	CapabilitiesDigest string                 `json:"capabilities_digest"`
}

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func absolutePath(path string) bool {
	return path != "" && len(path) <= 4096 && !strings.ContainsRune(path, 0) && filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) != path
}
func controlled(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || !directory && !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid())) && (directory || stat.Nlink == 1)
}

// The root is test-controlled and references are anchored below it. Explicitly
// reject symlinks below/root, hardlinks and uncontrolled writable inputs. This
// does not claim isolation from hostile same-UID ancestor/mount/rename races.
func openRepository(path string) (*os.Root, error) {
	if !absolutePath(path) {
		return nil, ErrFixture
	}
	before, err := os.Lstat(path)
	if err != nil || !controlled(before, true) {
		return nil, ErrFixture
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrFixture
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, ErrFixture
	}
	info, err := file.Stat()
	err = errors.Join(err, file.Close())
	if err != nil || !controlled(info, true) || !os.SameFile(before, info) {
		root.Close()
		return nil, ErrFixture
	}
	return root, nil
}
func readBounded(root *os.Root, path string, limit int64) (raw []byte, err error) {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) || limit < 0 || limit > descriptorLimit {
		return nil, ErrFixture
	}
	parts := strings.Split(path, string(filepath.Separator))
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, ErrFixture
		}
		if i < len(parts)-1 {
			info, e := root.Lstat(filepath.Join(parts[:i+1]...))
			if e != nil || !controlled(info, true) {
				return nil, ErrFixture
			}
		}
	}
	before, e := root.Lstat(path)
	if e != nil || !controlled(before, false) || before.Size() < 0 || before.Size() > limit {
		return nil, ErrFixture
	}
	file, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrFixture
	}
	defer func() {
		if file.Close() != nil {
			raw = nil
			err = ErrFixture
		}
	}()
	info, e := file.Stat()
	if e != nil || !controlled(info, false) || !os.SameFile(before, info) || info.Size() != before.Size() {
		return nil, ErrFixture
	}
	raw, e = io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil || int64(len(raw)) > limit || int64(len(raw)) != info.Size() {
		return nil, ErrFixture
	}
	current, e := root.Lstat(path)
	if e != nil || !controlled(current, false) || !os.SameFile(info, current) || current.Size() != info.Size() {
		return nil, ErrFixture
	}
	return raw, nil
}
func readReference(root *os.Root, ref fileReference, wantPath string) ([]byte, error) {
	if ref.Path != wantPath || ref.Bytes <= 0 || ref.Bytes > descriptorLimit || len(ref.SHA256) != 64 {
		return nil, ErrFixture
	}
	raw, err := readBounded(root, ref.Path, ref.Bytes)
	if err != nil || int64(len(raw)) != ref.Bytes || digest(raw) != ref.SHA256 {
		return nil, ErrFixture
	}
	return raw, nil
}

// Load preserves the complete raw config and independently pinned expected
// snapshot. It never reads observed_snapshot, materializes a workdir or opens
// the declared binary; the caller must prove the actual executable separately.
func Load(repoRoot, key, binaryPath, workdir string) (loaded *Fixture, result error) {
	if key != "app-first" && key != "domain-first" || !absolutePath(binaryPath) || !absolutePath(workdir) {
		return nil, ErrFixture
	}
	capability, err := mihomo.ApprovedBasicCapability(binaryPath)
	if err != nil || capability.Binary.SHA256 != approvedCoreSHA256 || capability.Binary.Path != binaryPath {
		return nil, ErrFixture
	}
	capabilitySHA, err := capability.Digest()
	if err != nil {
		return nil, ErrFixture
	}
	root, err := openRepository(repoRoot)
	if err != nil {
		return nil, ErrFixture
	}
	defer func() {
		if root.Close() != nil {
			loaded = nil
			result = ErrFixture
		}
	}()
	raw, err := readBounded(root, descriptorPath, descriptorLimit)
	if err != nil || digest(raw) != descriptorSHA256 {
		return nil, ErrFixture
	}
	if _, err = config.CanonicalJSON(raw); err != nil {
		return nil, ErrFixture
	}
	var definition descriptor
	if json.Unmarshal(raw, &definition) != nil || definition.Schema != "hoopa-p1-basic-verified-bundle-reference-v1" || definition.Status != "test_definition_reference_only" || definition.Mode != "basic" || definition.Protocol != 1 || definition.CoreSHA != approvedCoreSHA256 || definition.Baseline != "app-first" || definition.Target != "domain-first" || len(definition.Bundles) != 2 {
		return nil, ErrFixture
	}
	ref, ok := definition.Bundles[key]
	if !ok || ref.Encoding != "raw" || !ref.Complete || ref.RemoveRules || ref.ProgramRules || ref.Converted || ref.Executed || ref.ConfigBytes != ref.Config.Bytes || ref.ConfigSHA != ref.Config.SHA256 || ref.SnapshotSHA != ref.Expected.SHA256 || len(ref.Entries) != 2 || ref.Entries[0] != "entry-a" || ref.Entries[1] != "entry-b" {
		return nil, ErrFixture
	}
	prefix := "private/p1-basic-fixture-validation/" + key + "/"
	generated, err := readReference(root, ref.Config, prefix+"config.json")
	if err != nil {
		return nil, ErrFixture
	}
	canonical, err := config.CanonicalJSON(generated)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return nil, ErrFixture
	}
	snapshotRaw, err := readReference(root, ref.Expected, prefix+"expected-snapshot.json")
	if err != nil {
		return nil, ErrFixture
	}
	snapshot, err := wire.DecodeSnapshot(snapshotRaw)
	if err != nil || snapshot.ConfigSHA256 != ref.Config.SHA256 || len(snapshot.Entries) != 2 || snapshot.Entries[0].Name != "entry-a" || snapshot.Entries[1].Name != "entry-b" {
		return nil, ErrFixture
	}
	intentRaw, err := json.Marshal(finiteIntent{1, CompilerVersion, key, ref.Config.SHA256, ref.Expected.SHA256, capability.Binary, capabilitySHA})
	if err != nil {
		return nil, ErrFixture
	}
	intentRaw, err = config.CanonicalJSON(intentRaw)
	if err != nil {
		return nil, ErrFixture
	}
	files := map[string][]byte{"config.json": generated, mihomo.SnapshotFile: snapshotRaw, "intent.json": intentRaw}
	specs := []config.BundleFile{{Kind: "generated_config", Name: "config.json", Bytes: int64(len(generated)), SHA256: digest(generated)}, {Kind: "validation", Name: mihomo.SnapshotFile, Bytes: int64(len(snapshotRaw)), SHA256: digest(snapshotRaw)}, {Kind: "intent", Name: "intent.json", Bytes: int64(len(intentRaw)), SHA256: digest(intentRaw)}}
	material := control.BasicMaterial{Manifest: config.BundleManifest{Version: 1, Generation: config.Generation("fixture-" + key), CompilerVersion: CompilerVersion, CapabilitiesDigest: capabilitySHA, Binaries: []config.BinaryReference{capability.Binary}, Files: specs}, Files: files, Workdir: workdir}
	return &Fixture{key: key, material: material, intent: intentRaw, inputs: []control.PlanInput{{Kind: "core_binary", ID: "approved-basic-core", SHA256: capability.Binary.SHA256}, {Kind: "capabilities", ID: "approved-basic-v1", SHA256: capabilitySHA}}, snapshot: snapshot}, nil
}
