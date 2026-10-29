//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	wire "hoopa/protocol/corecontrol"
	sources "hoopa/protocol/sourcecontrol"
	sourceprofile "hoopa/protocol/sourceprofile"
)

type BasicMaterial struct {
	Manifest config.BundleManifest
	Files    map[string][]byte
	Workdir  string
}

// BasicMaterialSource is trusted read-only integration code, not user-selected
// raw candidate admission. It supplies independently prevalidated fixed material;
// complete Intent compilation/source discovery belongs to the later compiler.
type BasicMaterialSource func(context.Context, Plan) (BasicMaterial, error)
type checkedBasicMaterial struct {
	manifest   config.BundleManifest
	files      map[string][]byte
	launch     config.RunIntent
	capability mihomo.CapabilityRecord
}

func (checkedBasicMaterial) String() string { return "control.checkedBasicMaterial{private material}" }
func (checkedBasicMaterial) GoString() string {
	return "control.checkedBasicMaterial{private material}"
}

// checkBasicMaterial binds original bytes without publishing, repairing, starting
// or authorizing anything. The source owns semantic compilation and independent
// snapshot projection. Generation/assets are bound through manifest/Plan, not
// invented Snapshot fields; extra validation/route_map bodies remain opaque.
// Actual closed-core inspection must match the complete snapshot before opening.
type basicMaterialContents checkedBasicMaterial

func checkBasicMaterial(ctx context.Context, plan Plan, material BasicMaterial) (checkedBasicMaterial, error) {
	contents, err := checkBasicMaterialContents(ctx, plan, material)
	if err != nil {
		return checkedBasicMaterial{}, err
	}
	return verifyBasicMaterial(ctx, contents)
}

// Content validation is memory-only. This distinct type is not accepted by the
// runtime constructor; verifyBasicMaterial must prove the actual workdir and ELF
// before producing checkedBasicMaterial.
func checkBasicMaterialContents(ctx context.Context, plan Plan, material BasicMaterial) (basicMaterialContents, error) {
	fail := func(e error) (basicMaterialContents, error) { return basicMaterialContents{}, e }
	if ctx == nil || ctx.Err() != nil {
		return fail(config.ErrReadUnavailable)
	}
	if len(plan.raw) == 0 || !isHash(plan.Digest()) {
		return fail(ErrPlan)
	}
	doc := plan.Document()
	m := material.Manifest
	if m.Version != 1 || string(m.Generation) != doc.Impact.TargetGeneration || m.CompilerVersion != doc.CompilerVersion || m.CapabilitiesDigest != doc.CapabilitiesDigest || !planID(string(m.Generation)) || len(m.Files) == 0 || len(m.Files) > config.MaxBundleFiles || len(material.Files) != len(m.Files) {
		return fail(ErrPlan)
	}
	if len(m.Binaries) != 1 {
		return fail(ErrCapabilityUnsupported)
	}
	capability, e := mihomo.ApprovedCapability(m.Binaries[0])
	if e != nil || m.Binaries[0] != capability.Binary {
		return fail(ErrCapabilityUnsupported)
	}
	capabilitySHA, e := capability.Digest()
	if e != nil || capabilitySHA != m.CapabilitiesDigest {
		return fail(ErrCapabilityUnsupported)
	}
	inputs := map[string]string{}
	coreCount, capCount := 0, 0
	for _, in := range doc.Inputs {
		switch in.Kind {
		case "core_binary":
			coreCount++
			if in.SHA256 != capability.Binary.SHA256 {
				return fail(ErrPlan)
			}
		case "capabilities":
			capCount++
			if in.SHA256 != capabilitySHA {
				return fail(ErrPlan)
			}
		case "provider", "rule_asset", "app_binding":
			inputs[in.Kind+"/"+in.ID] = in.SHA256
		default:
			return fail(ErrPlan)
		}
	}
	if coreCount != 1 || capCount != 1 {
		return fail(ErrPlan)
	}
	// Bound actual allocations before cloning the caller's complete byte map.
	total := int64(0)
	names := map[string]bool{}
	for _, f := range m.Files {
		raw, exists := material.Files[f.Name]
		if !planID(f.Name) || f.Name == "manifest.json" || names[f.Name] || !exists || f.Bytes < 0 || f.Bytes > config.MaxBundleBytes-total || f.Bytes != int64(len(raw)) || !isHash(f.SHA256) || hash(raw) != f.SHA256 {
			return fail(ErrPlan)
		}
		names[f.Name] = true
		total += f.Bytes
	}
	m.Binaries = append([]config.BinaryReference(nil), m.Binaries...)
	m.Files = append([]config.BundleFile(nil), m.Files...)
	files := make(map[string][]byte, len(material.Files))
	for name, raw := range material.Files {
		files[name] = bytes.Clone(raw)
	}
	intentCount, generatedCount, snapshotCount := 0, 0, 0
	var generated []byte
	for _, f := range m.Files {
		if f.Name == mihomo.SnapshotFile && f.Kind != "validation" {
			return fail(ErrPlan)
		}
		switch f.Kind {
		case "intent":
			intentCount++
			canonical, e := config.CanonicalJSON(files[f.Name])
			if e != nil || !bytes.Equal(canonical, doc.Intent) {
				return fail(ErrPlan)
			}
		case "generated_config":
			generatedCount++
			generated = files[f.Name]
			if len(generated) == 0 || len(generated) > 65536 {
				return fail(ErrPlan)
			}
			canonical, e := config.CanonicalJSON(generated)
			if e != nil || len(canonical) == 0 || canonical[0] != '{' {
				return fail(ErrPlan)
			}
		case "provider", "rule", "app_binding":
			kind := f.Kind
			if kind == "rule" {
				kind = "rule_asset"
			}
			key := kind + "/" + f.Name
			if inputs[key] != f.SHA256 {
				return fail(ErrPlan)
			}
			delete(inputs, key)
		case "validation":
			if f.Name == mihomo.SnapshotFile {
				snapshotCount++
			}
		case "route_map": // Integrity-only opaque evidence, not a business proof.
		default:
			return fail(ErrPlan)
		}
	}
	if intentCount != 1 || generatedCount != 1 || snapshotCount != 1 || len(inputs) != 0 {
		return fail(ErrPlan)
	}
	var snapshotController, snapshotConfigSHA string
	if capability.Protocol == 2 {
		snapshot, e := sources.DecodeSnapshot(files[mihomo.SnapshotFile])
		if e != nil {
			return fail(ErrPlan)
		}
		projected, e := sourceprofile.Project(generated, string(m.Generation))
		if e != nil {
			return fail(ErrPlan)
		}
		actualBytes, _ := json.Marshal(snapshot)
		expectedBytes, _ := json.Marshal(projected)
		if !bytes.Equal(actualBytes, expectedBytes) {
			return fail(ErrPlan)
		}
		snapshotController, snapshotConfigSHA = snapshot.Controller, snapshot.ConfigSHA256
	} else {
		snapshot, e := wire.DecodeSnapshot(files[mihomo.SnapshotFile])
		if e != nil {
			return fail(ErrPlan)
		}
		snapshotController, snapshotConfigSHA = snapshot.Controller, snapshot.ConfigSHA256
	}
	if snapshotConfigSHA != hash(generated) {
		return fail(ErrPlan)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
	rawManifest, e := json.Marshal(m)
	if e != nil || len(rawManifest)+1 > config.MaxManifestBytes {
		return fail(ErrPlan)
	}
	rawManifest = append(rawManifest, '\n')
	launch := config.RunIntent{Version: 1, Generation: m.Generation, ManifestSHA256: hash(rawManifest), Binary: capability.Binary, Workdir: material.Workdir, Controller: snapshotController, ConfigSHA256: hash(generated)}
	launch.Arguments = []string{launch.Binary.Path, "-d", launch.Workdir, "-f", filepath.Join(launch.Workdir, "config.json")}
	return basicMaterialContents{manifest: m, files: files, launch: launch, capability: capability}, nil
}

func verifyBasicMaterial(ctx context.Context, contents basicMaterialContents) (checkedBasicMaterial, error) {
	fail := func(e error) (checkedBasicMaterial, error) { return checkedBasicMaterial{}, e }
	if ctx == nil || ctx.Err() != nil {
		return fail(config.ErrReadUnavailable)
	}
	launch := contents.launch
	// Dummy shape-only identities never leave this check or grant a launch ticket.
	probe := launch
	probe.RunID = "material-check"
	probe.OperationID = "material-check"
	probe.NonceSHA256 = strings.Repeat("0", 64)
	directory, e := config.OpenRunWorkdir(probe)
	if e != nil {
		return fail(config.ErrReadUnavailable)
	}
	if e = directory.Close(); e != nil {
		return fail(config.ErrReadUnavailable)
	}
	executable, e := platform.OpenExecutable(ctx, launch.Binary.Path, launch.Binary.SHA256)
	if e != nil {
		return fail(config.ErrReadUnavailable)
	}
	if e = executable.Close(); e != nil || ctx.Err() != nil {
		return fail(config.ErrReadUnavailable)
	}
	return checkedBasicMaterial(contents), nil
}
