//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	owned "hoopa/internal/runtime"
)

func (r *basicRuntime) stopCandidate(ctx context.Context, id config.RunID, op config.ID) (*owned.LaunchExitProof, error) {
	// A missing registration never grants signal rights. Only this live launch's
	// direct-child Wait result can prove a failure before closed registration.
	_, _, err := r.store.ReadControlRegistration(id, op)
	if err == nil {
		return nil, r.ownership.TerminateCandidate(ctx, id, op)
	}
	if !errors.Is(err, os.ErrNotExist) || r.registered != nil || r.child == nil || ctx == nil {
		return nil, errors.Join(owned.ErrOwnershipUnknown, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	proof, err := r.child.ExitedOriginal(ctx, r.store, id, op)
	if err != nil {
		return nil, err
	}
	return &proof, nil
}

// restore uses the exact old immutable bundle and fixed executable reference.
// It never recompiles against current subscriptions or overwrites old logs.
func (r *basicRuntime) restore(old config.RunID, base config.ActivePointer, name string) (closableApplyRuntime, error) {
	intent, _, err := r.store.ReadRunIntent(old)
	if err != nil {
		return nil, err
	}
	if base.Generation == nil || intent.Generation != *base.Generation || intent.ManifestSHA256 != base.ManifestSHA256 {
		return nil, config.ErrIntegrity
	}
	bundle, err := r.store.ReadBundle(intent.Generation)
	if err != nil {
		return nil, err
	}
	capability, err := mihomo.ApprovedCapability(intent.Binary)
	if err != nil || capability.Binary != intent.Binary || bundle.ManifestSHA256 != intent.ManifestSHA256 {
		return nil, ErrCapabilityUnsupported
	}
	capabilitySHA, err := capability.Digest()
	if err != nil || bundle.Manifest.CapabilitiesDigest != capabilitySHA || len(bundle.Manifest.Binaries) != 1 || bundle.Manifest.Binaries[0] != intent.Binary {
		return nil, ErrCapabilityUnsupported
	}
	var raw []byte
	count := 0
	for _, f := range bundle.Manifest.Files {
		if f.Kind == "generated_config" {
			count++
			raw = bundle.Files[f.Name]
		}
	}
	if count != 1 || len(raw) == 0 || len(raw) > 65536 || hash(raw) != intent.ConfigSHA256 {
		return nil, config.ErrIntegrity
	}
	workdir, err := r.store.PrepareLaunchWorkdir(name, raw)
	if err != nil {
		return nil, err
	}
	intent.RunID, intent.OperationID, intent.NonceSHA256 = "", "", ""
	intent.Workdir = workdir
	intent.Arguments = []string{intent.Binary.Path, "-d", workdir, "-f", filepath.Join(workdir, "config.json")}
	return newBasicApplyRuntime(r.store, checkedBasicMaterial{manifest: bundle.Manifest, files: bundle.Files, launch: intent, capability: capability}, r.entryArgs)
}
