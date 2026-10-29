//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
)

type sourceCompileAsset struct {
	raw     []byte
	context sourceUpdateContext
	receipt sourceUpdateReceipt
}
type sourceCompileInputs struct {
	Base            config.ActivePointer
	Settings        SourceSettings
	canonicalIntent []byte
	sources         map[config.ID]sourceCompileAsset
	appBindings     map[config.ID][]byte
	previousImpact  *impactGeneration
}

func (sourceCompileInputs) String() string   { return "control.sourceCompileInputs{private material}" }
func (sourceCompileInputs) GoString() string { return "control.sourceCompileInputs{private material}" }
func (sourceCompileAsset) String() string    { return "control.sourceCompileAsset{private material}" }
func (sourceCompileAsset) GoString() string  { return "control.sourceCompileAsset{private material}" }

// PlanSources captures one read-only view under the writer boundary, then does
// bounded restoration/compilation and executable verification without that lock.
// Its captured revision is never refreshed; Apply must reject a stale Plan.
func (c *Coordinator) PlanSources(ctx context.Context, intentRaw []byte) (Plan, error) {
	if c == nil || c.coordinatorState == nil {
		return Plan{}, config.ErrClosed
	}
	c.mu.Lock()
	input, err := c.captureSourceCompileLocked(ctx, intentRaw)
	c.mu.Unlock()
	if err != nil {
		return Plan{}, err
	}
	compiled, current, err := compileCapturedSources(ctx, input)
	if err != nil {
		return Plan{}, err
	}
	fields := map[string]any{"schema_version": 2, "base_revision": input.Base.Revision, "compiler_version": current.CompilerVersion, "capabilities_digest": current.CapabilitiesDigest, "intent": json.RawMessage(input.canonicalIntent), "inputs": current.Inputs, "impact": current.Impact, "blockers": []PlanBlocker{}}
	raw, err := json.Marshal(fields)
	if err != nil {
		return Plan{}, ErrPlan
	}
	if err = ctx.Err(); err != nil {
		return Plan{}, err
	}
	return BuildPlan(raw, sourceCompilationValidator(compiled))
}

// Caller holds c.mu. All retained bytes, pointers and rules are newly decoded
// owned values, never a later Store view. Only referenced provider assets are
// captured; the original history contexts remain subject to global validation.
func (c *Coordinator) captureSourceCompileLocked(ctx context.Context, intentRaw []byte) (sourceCompileInputs, error) {
	fail := func(err error) (sourceCompileInputs, error) { return sourceCompileInputs{}, err }
	if ctx == nil {
		return fail(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if c == nil || c.coordinatorState == nil || c.store == nil {
		return fail(config.ErrClosed)
	}
	intent, err := config.ParseIntent(intentRaw, config.IntentApply)
	if err != nil {
		return fail(sourcePlanIntentFailure(intentRaw))
	}
	canonical, err := config.CanonicalJSON(intentRaw)
	if err != nil {
		return fail(ErrPlan)
	}
	base, err := c.store.ReadActive()
	if err != nil || !sourceContextBase(base) {
		return fail(config.ErrReadUnavailable)
	}
	previousImpact, err := c.captureImpactBaseLocked(base)
	if err != nil {
		return fail(err)
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return fail(config.ErrReadUnavailable)
	}
	history, err := c.sourceHistoryLocked(settings)
	if err != nil {
		return fail(config.ErrReadUnavailable)
	}
	referenced := map[config.ID]bool{}
	if intent.Selection.ActiveSubscription != nil {
		referenced[*intent.Selection.ActiveSubscription] = true
	}
	for _, group := range intent.Groups {
		if group.Strategy == "follow" || group.Source == nil {
			continue
		}
		switch group.Source.Kind {
		case "active":
			if intent.Selection.ActiveSubscription == nil {
				return fail(ErrPlan)
			}
			referenced[*intent.Selection.ActiveSubscription] = true
		case "subscription":
			if group.Source.SubscriptionID == nil {
				return fail(ErrPlan)
			}
			referenced[*group.Source.SubscriptionID] = true
		}
	}
	addTarget := func(target config.Target) {
		if target.Kind == "node" && target.SubscriptionID != nil {
			referenced[*target.SubscriptionID] = true
		}
	}
	for _, rule := range intent.Rules {
		addTarget(rule.Target)
	}
	addTarget(intent.DefaultTarget)
	definitions := map[config.ID]config.SubscriptionSource{}
	for _, definition := range intent.Subscriptions {
		definitions[definition.ID] = definition.Source
	}
	ids := make([]config.ID, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := sourceCompileInputs{Base: base, Settings: settings, previousImpact: previousImpact, canonicalIntent: canonical, sources: map[config.ID]sourceCompileAsset{}}
	total := int64(0)
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		index, ok := history.Latest[id]
		if !ok {
			return fail(sourcePlanSourceFailure(id, "source_snapshot_missing"))
		}
		entry := history.Entries[index]
		definition, ok := definitions[id]
		if !ok || entry.Context.Source.ID != id || entry.Context.Source.Source != definition {
			return fail(sourcePlanSourceFailure(id, "source_definition_changed"))
		}
		raw, err := c.store.ReadSourceAsset(entry.Receipt.AssetSHA256)
		if err != nil {
			return fail(config.ErrReadUnavailable)
		}
		if int64(len(raw)) > config.MaxBundleBytes-total {
			return fail(ErrCapabilityUnsupported)
		}
		total += int64(len(raw))
		out.sources[id] = sourceCompileAsset{raw: raw, context: entry.Context, receipt: entry.Receipt}
	}
	out.appBindings, err = c.captureAppBindingsLocked(ctx, settings, intent.Apps)
	if err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	return out, nil
}

// compileCapturedSources deliberately has no Coordinator/Store access. A later
// source/settings/base change cannot be mixed into this one captured input set.
func compileCapturedSources(ctx context.Context, input sourceCompileInputs) (policy.Compilation, CurrentPlanInputs, error) {
	fail := func(err error) (policy.Compilation, CurrentPlanInputs, error) {
		return policy.Compilation{}, CurrentPlanInputs{}, err
	}
	if ctx == nil {
		return fail(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if !sourceContextBase(input.Base) {
		return fail(config.ErrReadUnavailable)
	}
	key, err := input.Settings.SensitiveIdentityKey()
	if err != nil {
		return fail(config.ErrReadUnavailable)
	}
	sources := make(map[config.ID]subscription.Snapshot, len(input.sources))
	for id, asset := range input.sources {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		contextRaw, e := encodeSourceUpdateContext(asset.context)
		if e != nil || !validSourceReceipt(asset.receipt) || asset.receipt.Phase != Committed || asset.context.Source.ID != id || asset.receipt.SourceID != id || asset.receipt.OperationID != asset.context.OperationID || asset.receipt.BaseRevision != asset.context.Base.Revision || asset.receipt.Sequence != asset.context.Sequence || asset.context.SettingsSHA256 != input.Settings.Digest() || hash(contextRaw) != asset.receipt.ContextSHA256 || hash(asset.raw) != asset.receipt.AssetSHA256 {
			return fail(config.ErrReadUnavailable)
		}
		snapshot, e := subscription.RestoreAsset(asset.raw, subscription.ParseOptions{SourceID: id, IdentityKey: key, RegionRules: asset.context.RegionRules})
		if e != nil || string(snapshot.ID) != asset.receipt.SnapshotID {
			return fail(sourcePlanSourceFailure(id, "source_candidate_invalid"))
		}
		if len(snapshot.Nodes) == 0 {
			return fail(sourcePlanSourceFailure(id, "source_candidate_empty"))
		}
		sources[id] = snapshot
	}
	compiled, err := compileSourceAppIdentity(input.canonicalIntent, input.Settings, sources, input.appBindings)
	if err != nil {
		if errors.Is(err, ErrCapabilityUnsupported) || errors.Is(err, policy.ErrCompileUnsupported) {
			return fail(ErrCapabilityUnsupported)
		}
		return fail(sourcePlanPolicyFailure(err))
	}
	manifest, _ := compiled.Bundle()
	for _, binary := range manifest.Binaries {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		executable, e := platform.OpenExecutable(ctx, binary.Path, binary.SHA256)
		if e != nil {
			if err = ctx.Err(); err != nil {
				return fail(err)
			}
			return fail(config.ErrReadUnavailable)
		}
		if e = executable.Close(); e != nil {
			return fail(config.ErrReadUnavailable)
		}
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	_, files := compiled.Bundle()
	targetImpact, err := impactFromBundle(manifest, files)
	if err != nil {
		return fail(err)
	}
	impact, err := projectSourceImpact(input.Base, input.previousImpact, targetImpact)
	if err != nil {
		return fail(err)
	}
	actual := compiled.Inputs()
	inputs := make([]PlanInput, len(actual))
	for i, value := range actual {
		inputs[i] = PlanInput{Kind: value.Kind, ID: value.ID, SHA256: value.SHA256}
	}
	return compiled, CurrentPlanInputs{CompilerVersion: manifest.CompilerVersion, CapabilitiesDigest: manifest.CapabilitiesDigest, Inputs: inputs, Impact: impact}, nil
}
func sourceCompilationValidator(compiled policy.Compilation) IntentValidator {
	return func(raw json.RawMessage, inputs []PlanInput) error {
		converted := make([]config.BundleInput, len(inputs))
		for i, input := range inputs {
			converted[i] = config.BundleInput{Kind: input.Kind, ID: input.ID, SHA256: input.SHA256}
		}
		if compiled.ValidateIntent(bytes.Clone(raw), converted) != nil {
			return ErrPlan
		}
		return nil
	}
}
