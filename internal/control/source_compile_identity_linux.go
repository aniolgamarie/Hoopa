//go:build linux && amd64

package control

import (
	"encoding/json"
	"sort"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
	profile "hoopa/protocol/sourceprofile"
)

// compileSourceIdentity is pure. Its caller supplies already restored Snapshot
// authority from the same installation settings and declared source definition.
// The preliminary compile discovers the complete finite input set; its generated
// snapshot is excluded from the commitment to avoid a generation/hash cycle.
// The actual executable and local settings/source freshness are checked by the
// later Plan/Apply integration, not by this deterministic memory-only helper.
func compileSourceIdentity(raw []byte, settings SourceSettings, sources map[config.ID]subscription.Snapshot) (policy.Compilation, error) {
	return compileSourceAppIdentity(raw, settings, sources, nil)
}

func compileSourceAppIdentity(raw []byte, settings SourceSettings, sources map[config.ID]subscription.Snapshot, bindings map[config.ID][]byte) (policy.Compilation, error) {
	fail := func(err error) (policy.Compilation, error) { return policy.Compilation{}, err }
	intent, err := config.ParseIntent(raw, config.IntentApply)
	if err != nil {
		return fail(ErrPlan)
	}
	canonical, err := config.CanonicalJSON(raw)
	if err != nil {
		return fail(ErrPlan)
	}
	capability, err := mihomo.ApprovedCapability(intent.Core.Mihomo)
	if err != nil || capability.Protocol != 2 {
		return fail(ErrCapabilityUnsupported)
	}
	runtime, err := settings.Runtime()
	if err != nil {
		return fail(ErrSourceSettings)
	}
	if len(bindings) > 0 {
		runtime.AppEntries = make(map[config.ID]profile.Entry, len(bindings))
		for id, raw := range bindings {
			b, err := config.DecodeAppBinding(raw)
			if err != nil || b.AppID() != id || b.SettingsSHA256() != settings.Digest() {
				return fail(config.ErrAppBinding)
			}
			entry, err := b.SensitiveEntry()
			if err != nil {
				return fail(config.ErrAppBinding)
			}
			runtime.AppEntries[id] = entry
		}
	}
	input := policy.CompileInput{Intent: canonical, Sources: sources, Generation: "source-input-discovery", Capability: capability, Runtime: runtime, AppBindings: bindings, SourceSettingsSHA256: settings.Digest()}
	preliminary, err := policy.Compile(input)
	if err != nil {
		return fail(err)
	}
	inputs := preliminary.Inputs()
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Kind != inputs[j].Kind {
			return inputs[i].Kind < inputs[j].Kind
		}
		return inputs[i].ID < inputs[j].ID
	})
	capSHA, err := capability.Digest()
	if err != nil {
		return fail(ErrCapabilityUnsupported)
	}
	preliminaryManifest, _ := preliminary.Bundle()
	binding, err := json.Marshal([]any{"hoopa.source-generation.v1", preliminaryManifest.CompilerVersion, json.RawMessage(canonical), capSHA, inputs, settings.Digest()})
	if err != nil {
		return fail(ErrPlan)
	}
	input.Generation = config.Generation("source-" + hash(binding))
	return policy.Compile(input)
}
