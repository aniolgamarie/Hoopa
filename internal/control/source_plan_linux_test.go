//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/policy"
	"hoopa/internal/subscription"
)

// Actual approved static ELF is only copied/hashed/opened, never executed.
func sourcePlanTestCore(t *testing.T) string {
	t.Helper()
	input := os.Getenv("HOOPA_P1_SOURCES_CORE")
	if input == "" {
		t.Fatal("HOOPA_P1_SOURCES_CORE is required; positive ELF validation cannot be skipped")
	}
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	path := filepath.Join(t.TempDir(), "mihomo")
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, digest), io.LimitReader(file, (128<<20)+1))
	if copyErr != nil || n > 128<<20 || hex.EncodeToString(digest.Sum(nil)) != "c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6" {
		out.Close()
		t.Fatal("actual approved static core checksum/size mismatch", copyErr)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func sourcePlanTestIntent(t *testing.T, core string, seed *sourceRecoverTestState) map[string]any {
	t.Helper()
	capability, err := mihomo.ApprovedSourcesCapability(core)
	if err != nil {
		t.Fatal(err)
	}
	m := updateRequestIntent(t)
	m["core"] = map[string]any{"mihomo": capability.Binary}
	m["subscriptions"] = []any{}
	m["selection"] = map[string]any{"active_subscription": nil}
	m["groups"] = []any{}
	m["rules"] = []any{}
	m["default_target"] = map[string]any{"kind": "reject"}
	if seed != nil {
		m["subscriptions"] = []any{seed.entry.Context.Source}
		m["selection"] = map[string]any{"active_subscription": seed.entry.Context.Source.ID}
		m["groups"] = []any{map[string]any{"id": "US", "required": true, "region": "US", "strategy": "auto", "source": map[string]any{"kind": "active"}, "probe": map[string]any{"url": "https://probe.example.test/", "interval_seconds": 60, "timeout_ms": 1000, "tolerance_ms": 0}}, map[string]any{"id": "follow", "required": true, "strategy": "follow", "follows": "US"}}
		m["default_target"] = map[string]any{"kind": "group", "group_id": "follow"}
	}
	return m
}
func sourcePlanTestRaw(t *testing.T, m map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func sourcePlanTestFixture(t *testing.T, core string) (*Coordinator, config.Paths, sourceRecoverTestState, map[string]any) {
	t.Helper()
	c, p := sourceRecoverTestCoordinator(t)
	seed := sourceRecoverTestSeed(t, c, "source-plan-seed", "source-a", updaterFile(t, updaterProvider), Committed, "")
	return c, p, seed, sourcePlanTestIntent(t, core, &seed)
}
func sourcePlanTestCapture(t *testing.T, c *Coordinator, raw []byte) sourceCompileInputs {
	t.Helper()
	c.mu.Lock()
	input, err := c.captureSourceCompileLocked(context.Background(), raw)
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func sourcePlanTestReject(t *testing.T, c *Coordinator, p config.Paths, raw []byte, want error) {
	t.Helper()
	before := coordinatorTree(t, p)
	plan, err := c.PlanSources(context.Background(), raw)
	if !errors.Is(err, want) || plan.Digest() != "" || plan.Bytes() != nil {
		t.Fatalf("Plan failure expected %v, got %v", want, err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("Plan failure changed persistent/runtime tree")
	}
}
func sourcePlanTestUpdate(t *testing.T, c *Coordinator, source config.SubscriptionSource, id, body string) sourceHistoryEntry {
	t.Helper()
	if err := os.WriteFile(source.Path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw := updaterRequest(t, c, id, "source-a", source, nil)
	updaterBegin(t, u, raw)
	entry := updaterWait(t, u, raw)
	u.Close()
	return entry
}
func TestSourcePlanReadOnlyDeterministicAndCompleteValidator(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p, seed, intent := sourcePlanTestFixture(t, core)
	raw := sourcePlanTestRaw(t, intent)
	before := coordinatorTree(t, p)
	plan, err := c.PlanSources(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.PlanSources(context.Background(), raw)
	if err != nil || !bytes.Equal(plan.Bytes(), again.Bytes()) || plan.Digest() != again.Digest() {
		t.Fatal("identical inputs changed Plan", err)
	}
	doc := plan.Document()
	if doc.BaseRevision != string(seed.entry.Context.Base.Revision) || doc.CompilerVersion != policy.CompilerVersion || doc.Impact.RuntimeAction != "start" || doc.Impact.PreviousGeneration != nil || doc.Impact.AffectedApps == nil || len(doc.Impact.AffectedApps) != 0 || !doc.Impact.RequiresDisruption || doc.Impact.ConnectionEffect != "interrupt" || len(doc.Blockers) != 0 {
		t.Fatal("first start impact or base mismatch")
	}
	input := sourcePlanTestCapture(t, c, raw)
	compiled, current, err := compileCapturedSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, files := compiled.Bundle()
	if doc.Impact.TargetGeneration != string(manifest.Generation) || !reflect.DeepEqual(doc.Inputs, current.Inputs) || doc.CapabilitiesDigest != manifest.CapabilitiesDigest {
		t.Fatal("Plan not bound to actual compilation")
	}
	if _, err = ParsePlan(plan.Bytes(), sourceCompilationValidator(compiled)); err != nil {
		t.Fatal("Plan failed independent real compilation validator", err)
	}
	validator := sourceCompilationValidator(compiled)
	for _, inputs := range [][]PlanInput{nil, append(append([]PlanInput{}, doc.Inputs...), doc.Inputs[0]), doc.Inputs[1:]} {
		if validator(doc.Intent, inputs) != ErrPlan {
			t.Fatal("partial/duplicate inputs accepted")
		}
	}
	bad := append([]PlanInput{}, doc.Inputs...)
	bad[0].SHA256 = strings.Repeat("f", 64)
	if validator(doc.Intent, bad) != ErrPlan {
		t.Fatal("modified digest accepted")
	}
	changed := sourcePlanTestIntent(t, core, &seed)
	changed["default_target"] = map[string]any{"kind": "reject"}
	if validator(sourcePlanTestRaw(t, changed), doc.Inputs) != ErrPlan {
		t.Fatal("different complete Intent accepted")
	}
	// Returned public/sensitive copies cannot mutate the compiled/Plan authority.
	copyBytes := plan.Bytes()
	copyBytes[0] = '!'
	doc.Inputs[0].SHA256 = "changed"
	for name, raw := range files {
		if len(raw) > 0 {
			raw[0] = '!'
			files[name] = raw
			break
		}
	}
	if !bytes.Equal(plan.Bytes(), again.Bytes()) || sourceCompilationValidator(compiled)(plan.Document().Intent, plan.Document().Inputs) != nil {
		t.Fatal("getter mutation reached authority")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("Plan captured, compiled, downloaded or wrote runtime state")
	}
	for _, value := range []any{input, input.sources["source-a"]} {
		if strings.Contains(fmt.Sprintf("%v %#v", value, value), "provider.yaml") || strings.Contains(fmt.Sprintf("%v %#v", value, value), "US fixture") {
			t.Fatal("capture formatting exposed source material")
		}
	}
}
func TestSourcePlanStrictInputAndMissingDependencies(t *testing.T) {
	core := sourcePlanTestCore(t)
	for _, kind := range []string{"missing-settings", "bad-settings", "changed-key", "missing-latest", "wrong-definition", "missing-asset", "corrupt-asset"} {
		t.Run(kind, func(t *testing.T) {
			c, p, seed, intent := sourcePlanTestFixture(t, core)
			switch kind {
			case "missing-settings":
				if err := os.Remove(filepath.Join(p.State, "credentials", "source-settings-v1.json")); err != nil {
					t.Fatal(err)
				}
			case "bad-settings":
				if err := os.WriteFile(filepath.Join(p.State, "credentials", "source-settings-v1.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-key":
				key, runtime := codecSettingsInputs()
				key[0] ^= 255
				s, err := NewSourceSettings(key, runtime)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := s.SensitiveBytes()
				if err := os.WriteFile(filepath.Join(p.State, "credentials", "source-settings-v1.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-latest":
				intent["subscriptions"] = []any{config.Subscription{ID: "source-missing", Source: seed.entry.Context.Source.Source, UpdateHours: seed.entry.Context.Source.UpdateHours}}
				intent["selection"] = map[string]any{"active_subscription": "source-missing"}
			case "wrong-definition":
				source := seed.entry.Context.Source
				source.Source.Path = "/private/changed-provider.yaml"
				intent["subscriptions"] = []any{source}
			case "missing-asset":
				if err := os.Remove(filepath.Join(p.State, "source-assets", seed.entry.Receipt.AssetSHA256+".json")); err != nil {
					t.Fatal(err)
				}
			case "corrupt-asset":
				if err := os.WriteFile(filepath.Join(p.State, "source-assets", seed.entry.Receipt.AssetSHA256+".json"), []byte(`{"forged":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			sourcePlanTestReject(t, c, p, sourcePlanTestRaw(t, intent), config.ErrReadUnavailable)
		})
	}
	c, p, _, intent := sourcePlanTestFixture(t, core)
	raw := sourcePlanTestRaw(t, intent)
	for _, bad := range [][]byte{nil, []byte(`null`), []byte(`{}`), append(append([]byte{}, raw...), []byte(`{}`)...), append([]byte(`{"schema_version":1,`), raw[1:]...), bytes.Repeat([]byte{' '}, config.MaxCanonicalBytes+1)} {
		sourcePlanTestReject(t, c, p, bad, ErrPlan)
	}
	badIntent := sourcePlanTestIntent(t, core, nil)
	badIntent["foreign"] = false
	sourcePlanTestReject(t, c, p, sourcePlanTestRaw(t, badIntent), ErrPlan)
	if plan, err := c.PlanSources(nil, raw); err != ErrRequest || plan.Digest() != "" {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if plan, err := c.PlanSources(ctx, raw); !errors.Is(err, context.Canceled) || plan.Digest() != "" {
		t.Fatal("cancelled Plan accepted")
	}
	c.Close()
	if plan, err := c.PlanSources(context.Background(), raw); err != config.ErrClosed || plan.Digest() != "" {
		t.Fatal("closed coordinator accepted")
	}
	var zero Coordinator
	if plan, err := zero.PlanSources(context.Background(), raw); err != config.ErrClosed || plan.Digest() != "" {
		t.Fatal("zero coordinator accepted")
	}
}
func TestSourcePlanUnusedSourceAndScheduleChangeNeedNoDownload(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p, seed, intent := sourcePlanTestFixture(t, core)
	unused := sourceRecoverTestSeed(t, c, "unused-source", "source-b", updaterFile(t, updaterProvider), Committed, "")
	intent["subscriptions"] = []any{seed.entry.Context.Source, unused.entry.Context.Source}
	if err := os.Remove(filepath.Join(p.State, "source-assets", unused.entry.Receipt.AssetSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(seed.entry.Context.Source.Source.Path); err != nil {
		t.Fatal(err)
	}
	raw := sourcePlanTestRaw(t, intent)
	before := coordinatorTree(t, p)
	if _, err := c.PlanSources(context.Background(), raw); err != nil {
		t.Fatal("unused asset or current provider body became dependency", err)
	}
	source := seed.entry.Context.Source
	hours := uint64(9)
	source.UpdateHours = &hours
	intent["subscriptions"] = []any{source, unused.entry.Context.Source}
	input := sourcePlanTestCapture(t, c, sourcePlanTestRaw(t, intent))
	if len(input.sources) != 1 {
		t.Fatal("unreferenced source was captured")
	}
	if _, _, err := compileCapturedSources(context.Background(), input); err != nil {
		t.Fatal("schedule change forced reacquisition", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("planning changed source/runtime tree")
	}
}
func TestSourcePlanReferenceDiscoveryAndIndependentCompiler(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, _, seed, base := sourcePlanTestFixture(t, core)
	for _, kind := range []string{"active", "fixed-group", "active-follow", "rule-node", "default-node"} {
		t.Run(kind, func(t *testing.T) {
			intent := sourcePlanTestIntent(t, core, &seed)
			intent["groups"] = []any{}
			intent["selection"] = map[string]any{"active_subscription": nil}
			intent["default_target"] = map[string]any{"kind": "reject"}
			node := map[string]any{"kind": "node", "subscription_id": "source-a", "node_id": seed.snapshot.Nodes[0].ID}
			switch kind {
			case "active":
				intent["selection"] = map[string]any{"active_subscription": "source-a"}
			case "fixed-group":
				intent["groups"] = []any{map[string]any{"id": "fixed", "required": true, "strategy": "manual", "source": map[string]any{"kind": "subscription", "subscription_id": "source-a"}, "selected_node_id": seed.snapshot.Nodes[0].ID}}
			case "active-follow":
				intent["selection"] = base["selection"]
				intent["groups"] = base["groups"]
			case "rule-node":
				intent["rules"] = []any{map[string]any{"id": "pin", "match": map[string]any{"kind": "domain", "operator": "exact", "value": "example.test"}, "target": node}}
			case "default-node":
				intent["default_target"] = node
			}
			input := sourcePlanTestCapture(t, c, sourcePlanTestRaw(t, intent))
			if len(input.sources) != 1 {
				t.Fatal("reference edge not discovered")
			}
			if _, _, err := compileCapturedSources(context.Background(), input); err != nil {
				t.Fatal("discovered source did not compile", err)
			}
		})
	}
	input := sourcePlanTestCapture(t, c, sourcePlanTestRaw(t, base))
	asset := input.sources["source-a"]
	asset.raw = bytes.Replace(asset.raw, []byte(`"display_name":"US fixture"`), []byte(`"display_name":"forged"`), 1)
	if hash(asset.raw) == asset.receipt.AssetSHA256 {
		t.Fatal("metadata mutation absent")
	}
	asset.receipt.AssetSHA256 = hash(asset.raw)
	input.sources["source-a"] = asset
	compiled, current, err := compileCapturedSources(context.Background(), input)
	if !errors.Is(err, config.ErrReadUnavailable) || len(current.Inputs) != 0 || len(compiled.Inputs()) != 0 {
		t.Fatal("digest-consistent metadata bypassed RestoreAsset", err)
	}
	if reason, field, ok := SourcePlanFailureDetails(err); !ok || reason != "source_candidate_invalid" || field != "subscriptions[source-a]" {
		t.Fatal("invalid source candidate lost precise diagnostic")
	}
}
func TestSourcePlanGenerationTracksRulesCorePathAndRuntime(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p, seed, intent := sourcePlanTestFixture(t, core)
	original, err := c.PlanSources(context.Background(), sourcePlanTestRaw(t, intent))
	if err != nil {
		t.Fatal(err)
	}
	intent["rules"] = []any{map[string]any{"id": "new-domain", "match": map[string]any{"kind": "domain", "operator": "suffix", "value": "example.test"}, "target": map[string]any{"kind": "reject"}}}
	changed, err := c.PlanSources(context.Background(), sourcePlanTestRaw(t, intent))
	if err != nil || changed.Document().Impact.TargetGeneration == original.Document().Impact.TargetGeneration {
		t.Fatal("rule policy omitted from generation", err)
	}
	core2 := sourcePlanTestCore(t)
	capability, _ := mihomo.ApprovedSourcesCapability(core2)
	intent["core"] = map[string]any{"mihomo": capability.Binary}
	changed2, err := c.PlanSources(context.Background(), sourcePlanTestRaw(t, intent))
	if err != nil || changed2.Document().Impact.TargetGeneration == changed.Document().Impact.TargetGeneration {
		t.Fatal("actual core path omitted from generation", err)
	}
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rules := []subscription.RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "unmatched", Region: "CA"}}
	request := updaterRequest(t, c, "plan-region-policy-change", "source-a", seed.entry.Context.Source.Source, rules)
	updaterBegin(t, u, request)
	policyChanged := updaterWait(t, u, request)
	u.Close()
	if policyChanged.Operation.Record.Phase != Committed {
		t.Fatal("real region policy update failed")
	}
	beforePolicyPlan := coordinatorTree(t, p)
	policyPlan, err := c.PlanSources(context.Background(), sourcePlanTestRaw(t, intent))
	if err != nil || policyPlan.Document().Impact.TargetGeneration == changed2.Document().Impact.TargetGeneration {
		t.Fatal("unmatched complete region policy omitted from generation", err)
	}
	if !reflect.DeepEqual(beforePolicyPlan, coordinatorTree(t, p)) {
		t.Fatal("policy Plan wrote state")
	}
	// A separate no-source fixture can alter private settings explicitly without
	// contradicting immutable source-context/settings bindings from prior updates.
	other, paths := sourceRecoverTestCoordinator(t)
	noSources := sourcePlanTestRaw(t, sourcePlanTestIntent(t, core, nil))
	first, err := other.PlanSources(context.Background(), noSources)
	if err != nil {
		t.Fatal(err)
	}
	key, runtime := codecSettingsInputs()
	runtime.Secret = strings.Repeat("q", 32)
	settings, err := NewSourceSettings(key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := settings.SensitiveBytes()
	if err := os.WriteFile(filepath.Join(paths.State, "credentials", "source-settings-v1.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := other.PlanSources(context.Background(), noSources)
	if err != nil || next.Document().Impact.TargetGeneration == first.Document().Impact.TargetGeneration || next.Digest() == first.Digest() {
		t.Fatal("runtime/settings commitment omitted", err)
	}
	// Runtime has no separate PlanInput kind: generation binds settings digest.
	if !reflect.DeepEqual(first.Document().Inputs, next.Document().Inputs) {
		t.Fatal("fixture unexpectedly changed ordinary provider/core/rule inputs")
	}
}
func TestSourcePlanCapturedAssetsIgnoreNewerStoreState(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p, seed, intent := sourcePlanTestFixture(t, core)
	raw := sourcePlanTestRaw(t, intent)
	captured := sourcePlanTestCapture(t, c, raw)
	before, current, err := compileCapturedSources(context.Background(), captured)
	if err != nil {
		t.Fatal(err)
	}
	newer := sourcePlanTestUpdate(t, c, seed.entry.Context.Source.Source, "newer-plan-source", `{"proxies":[{"name":"US newer","type":"http","server":"new.example","port":443}]}`)
	if newer.Operation.Record.Phase != Committed || newer.Context.Sequence != 2 {
		t.Fatal("newer actual file update failed")
	}
	next, err := c.PlanSources(context.Background(), raw)
	if err != nil || next.Document().Impact.TargetGeneration == current.Impact.TargetGeneration {
		t.Fatal("newer actual provider not bound", err)
	}
	if err := os.Remove(filepath.Join(p.State, "source-assets", seed.entry.Receipt.AssetSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	c.Close()
	repeated, repeatedInputs, err := compileCapturedSources(context.Background(), captured)
	if err != nil {
		t.Fatal("captured compile consulted closed Store or missing old asset", err)
	}
	m, _ := before.Bundle()
	m2, _ := repeated.Bundle()
	if m.Generation != m2.Generation || !reflect.DeepEqual(current, repeatedInputs) {
		t.Fatal("later update was mixed into captured input")
	}
}
func TestSourcePlanLatestCommittedDoesNotFallBack(t *testing.T) {
	core := sourcePlanTestCore(t)
	t.Run("newer-failure-keeps-committed", func(t *testing.T) {
		c, _, seed, intent := sourcePlanTestFixture(t, core)
		raw := sourcePlanTestRaw(t, intent)
		before, err := c.PlanSources(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		failed := sourcePlanTestUpdate(t, c, seed.entry.Context.Source.Source, "failed-plan-attempt", `{"invalid":"private-validation-error"}`)
		if failed.Operation.Record.Phase != RolledBack {
			t.Fatal("expected failed provider attempt")
		}
		after, err := c.PlanSources(context.Background(), raw)
		if err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("newer failure displaced latest committed input", err)
		}
	})
	c, p, seed, intent := sourcePlanTestFixture(t, core)
	newer := sourcePlanTestUpdate(t, c, seed.entry.Context.Source.Source, "newest-snapshot", `{"proxies":[{"name":"US changed","type":"http","server":"other.example","port":443}]}`)
	if err := os.Remove(filepath.Join(p.State, "source-assets", newer.Receipt.AssetSHA256+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.store.ReadSourceAsset(seed.entry.Receipt.AssetSHA256); err != nil {
		t.Fatal("old candidate missing from fallback fixture", err)
	}
	sourcePlanTestReject(t, c, p, sourcePlanTestRaw(t, intent), config.ErrReadUnavailable)
}
func TestSourcePlanUnsupportedActionsAndExecutableProof(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p, _, intent := sourcePlanTestFixture(t, core)
	raw := sourcePlanTestRaw(t, intent)
	input := sourcePlanTestCapture(t, c, raw)
	compiled, _, err := compileCapturedSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, files := compiled.Bundle()
	sha, err := c.store.PublishBundle(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	active := input.Base
	generation := manifest.Generation
	op := config.ID("synthetic-published-generation")
	active.Generation = &generation
	active.OperationID = &op
	active.ManifestSHA256 = sha
	active.DesiredRuntime = "running"
	if err := c.store.PublishActive(active); err != nil {
		t.Fatal(err)
	}
	sourcePlanTestReject(t, c, p, raw, ErrCapabilityUnsupported)
	active.DesiredRuntime = "stopped"
	if err := c.store.PublishActive(active); err != nil {
		t.Fatal(err)
	}
	sourcePlanTestReject(t, c, p, raw, ErrCapabilityUnsupported)
	// A running different generation has the one supported replacement impact.
	active.DesiredRuntime = "running"
	if err := c.store.PublishActive(active); err != nil {
		t.Fatal(err)
	}
	intent["default_target"] = map[string]any{"kind": "reject"}
	plan, err := c.PlanSources(context.Background(), sourcePlanTestRaw(t, intent))
	if err != nil || plan.Document().Impact.RuntimeAction != "replace" || plan.Document().Impact.PreviousGeneration == nil || *plan.Document().Impact.PreviousGeneration != string(generation) {
		t.Fatal("replacement impact", err)
	}
	// Actual approved checksum is not a substitute for safe installed ELF proof.
	if err := os.Chmod(core, 0777); err != nil {
		t.Fatal(err)
	}
	sourcePlanTestReject(t, c, p, sourcePlanTestRaw(t, intent), config.ErrReadUnavailable)
}

type sourcePlanBarrierContext struct {
	context.Context
	c                *Coordinator
	once             sync.Once
	entered, release chan struct{}
}

func (c *sourcePlanBarrierContext) Err() error {
	if c.c.mu.TryLock() {
		c.c.mu.Unlock()
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.Context.Err()
}
func TestSourcePlanMethodRetainsCapturedBaseOutsideLock(t *testing.T) {
	core := sourcePlanTestCore(t)
	c, p := sourceRecoverTestCoordinator(t)
	raw := sourcePlanTestRaw(t, sourcePlanTestIntent(t, core, nil))
	base, _ := c.store.ReadActive()
	oldSettings := initializeSettings(t)
	expected, err := compileSourceIdentity(raw, oldSettings, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedManifest, _ := expected.Bundle()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	barrier := &sourcePlanBarrierContext{Context: ctx, c: c, entered: make(chan struct{}), release: make(chan struct{})}
	type result struct {
		plan Plan
		err  error
	}
	done := make(chan result, 1)
	go func() { plan, err := c.PlanSources(barrier, raw); done <- result{plan, err} }()
	released, joined := false, false
	t.Cleanup(func() {
		cancel()
		if !released {
			close(barrier.release)
		}
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("Plan did not join")
			}
		}
	})
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Plan retained Coordinator lock during compile")
	}
	// Only a fixture changes state here; the in-flight Plan must remain read-only.
	c.mu.Lock()
	active := base
	active.Revision = "newer-captured-base"
	if err := c.store.PublishActive(active); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	key, runtime := codecSettingsInputs()
	runtime.Secret = strings.Repeat("q", 32)
	settings, err := NewSourceSettings(key, runtime)
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	settingsRaw, _ := settings.SensitiveBytes()
	if err := os.WriteFile(filepath.Join(p.State, "credentials", "source-settings-v1.json"), settingsRaw, 0600); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.mu.Unlock()
	before := coordinatorTree(t, p)
	raw[0] = '!'
	close(barrier.release)
	released = true
	select {
	case got := <-done:
		joined = true
		if got.err != nil || got.plan.Document().BaseRevision != string(base.Revision) || got.plan.Document().Impact.TargetGeneration != string(expectedManifest.Generation) {
			t.Fatal("Plan refreshed captured revision/inputs or retained caller bytes", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Plan did not finish")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("lock-free Plan compilation wrote state")
	}
}
