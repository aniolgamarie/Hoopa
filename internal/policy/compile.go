//go:build linux && amd64

package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"sort"
	"strings"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/subscription"
	profile "hoopa/protocol/sourceprofile"
)

const CompilerVersion = "hoopa.sources.v1"

var ErrCompileUnsupported = errors.New("SOURCE_COMPILE_UNSUPPORTED")

// RuntimeInput is explicit private endpoint material. Compilation neither
// chooses ports nor creates credentials, files, keys, processes or probes.
type RuntimeInput struct {
	Controller, Secret string
	Entry              profile.Entry
	AppEntries         map[config.ID]profile.Entry
}
type CompileInput struct {
	Intent               []byte
	Sources              map[config.ID]subscription.Snapshot
	Generation           config.Generation
	Capability           mihomo.CapabilityRecord
	Runtime              RuntimeInput
	AppBindings          map[config.ID][]byte
	SourceSettingsSHA256 string
}
type Compilation struct {
	intent   []byte
	manifest config.BundleManifest
	files    map[string][]byte
	inputs   []config.BundleInput
}

func (Compilation) String() string   { return "policy.Compilation{private material}" }
func (Compilation) GoString() string { return "policy.Compilation{private material}" }
func (c Compilation) Bundle() (config.BundleManifest, map[string][]byte) {
	m := c.manifest
	m.Binaries = append([]config.BinaryReference(nil), m.Binaries...)
	m.Files = append([]config.BundleFile(nil), m.Files...)
	if c.files == nil {
		return m, nil
	}
	files := make(map[string][]byte, len(c.files))
	for n, b := range c.files {
		files[n] = bytes.Clone(b)
	}
	return m, files
}
func (c Compilation) Inputs() []config.BundleInput {
	return append([]config.BundleInput(nil), c.inputs...)
}
func (c Compilation) ValidateIntent(raw []byte, inputs []config.BundleInput) error {
	if len(c.intent) == 0 || len(inputs) != len(c.inputs) {
		return ErrCompile
	}
	canonical, e := config.CanonicalJSON(raw)
	if e != nil || !bytes.Equal(canonical, c.intent) {
		return ErrCompile
	}
	want := map[string]string{}
	for _, in := range c.inputs {
		want[in.Kind+"/"+in.ID] = in.SHA256
	}
	for _, in := range inputs {
		key := in.Kind + "/" + in.ID
		if want[key] == "" || want[key] != in.SHA256 {
			return ErrCompile
		}
		delete(want, key)
	}
	if len(want) != 0 {
		return ErrCompile
	}
	return nil
}

type RouteNode struct {
	Alias          string    `json:"alias"`
	SubscriptionID config.ID `json:"subscription_id"`
	NodeID         config.ID `json:"node_id"`
}
type RouteGroup struct {
	ID       config.ID `json:"id"`
	Alias    string    `json:"alias"`
	Strategy string    `json:"strategy"`
	SourceID config.ID `json:"source_id"`
	Members  []string  `json:"members"`
}
type RouteRule struct {
	ID     config.ID `json:"id"`
	Index  int       `json:"index"`
	Target string    `json:"target"`
}
type RouteMap struct {
	SchemaVersion int               `json:"schema_version"`
	Generation    config.Generation `json:"generation"`
	SharedEntry   string            `json:"shared_entry"`
	Nodes         []RouteNode       `json:"nodes"`
	Groups        []RouteGroup      `json:"groups"`
	Rules         []RouteRule       `json:"rules"`
	DefaultTarget string            `json:"default_target"`
	Apps          []RouteApp        `json:"apps,omitempty"`
}

// Compile implements finite source and separately qualified named-app profiles.
// Unsupported process, metering or IPv6-rule branches return an explicit
// blocker error and no partial bundle. All in-memory source authority is
// validated independently of ResolveGroups' pure metadata calculation.
// Capability shape is checked here; final US5 admission still performs exact
// approved-binary lookup and original-process ownership verification.
func Compile(input CompileInput) (Compilation, error) {
	fail := func(e error) (Compilation, error) { return Compilation{}, e }
	intent, e := config.ParseIntent(input.Intent, config.IntentApply)
	if e != nil {
		return fail(IntentReferenceFailure(input.Intent))
	}
	canonical, e := config.CanonicalJSON(input.Intent)
	if e != nil {
		return fail(ErrCompile)
	}
	if _, e = config.ParseID(string(input.Generation)); e != nil {
		return fail(ErrCompile)
	}
	cap := input.Capability
	if cap.Validate() != nil || cap.Protocol != 2 || cap.Binary != intent.Core.Mihomo {
		return fail(ErrCompile)
	}
	capSHA, e := cap.Digest()
	if e != nil {
		return fail(ErrCompile)
	}
	if intent.Core.Gost != nil || intent.Retention.Enabled || cap.Mode == "basic-sources" && len(intent.Apps) != 0 {
		return fail(ErrCompileUnsupported)
	}
	appProfile := cap.Mode == "basic-app-routing"
	compilerVersion := CompilerVersion
	if appProfile {
		compilerVersion = AppCompilerVersion
	}
	apps, e := compileAppEntries(intent, input, appProfile)
	if e != nil {
		return fail(e)
	}
	groups, e := ResolveGroups(intent.Selection.ActiveSubscription, intent.Groups, input.Sources)
	if e != nil {
		return fail(compileGroupDiagnostic(e))
	}
	if len(groups) > 64 {
		return fail(ErrCompileUnsupported)
	}
	referenced := map[config.ID]bool{}
	referenceFields := map[config.ID]string{}
	addReference := func(id config.ID, field string) {
		referenced[id] = true
		if _, exists := referenceFields[id]; !exists {
			referenceFields[id] = field
		}
	}
	needed := map[subscription.NodeRef]bool{}
	if intent.Selection.ActiveSubscription != nil {
		addReference(*intent.Selection.ActiveSubscription, "selection.active_subscription")
	}
	for _, definition := range intent.Groups {
		g := groups[definition.ID]
		if definition.Strategy != "follow" {
			addReference(g.SourceID, groupField(g.ID)+".source")
		}
		for _, ref := range g.Candidates {
			needed[ref] = true
		}
	}
	direct := false
	target := func(t config.Target, field string) (string, error) {
		switch t.Kind {
		case "reject":
			return "REJECT", nil
		case "direct":
			direct = true
			return "node:local-direct", nil
		case "group":
			if t.GroupID == nil {
				return "", ErrCompile
			}
			if _, ok := groups[*t.GroupID]; !ok {
				return "", compileDiagnostic("target_group_missing", field)
			}
			return groupAlias(*t.GroupID), nil
		case "node":
			if t.SubscriptionID == nil || t.NodeID == nil {
				return "", ErrCompile
			}
			ref := subscription.NodeRef{SubscriptionID: *t.SubscriptionID, NodeID: *t.NodeID}
			if _, e := subscription.ResolvePin(input.Sources, ref); e != nil {
				return "", compileDiagnostic("pinned_node_missing", field)
			}
			addReference(ref.SubscriptionID, field)
			needed[ref] = true
			return nodeAlias(ref.NodeID), nil
		default:
			return "", ErrCompile
		}
	}
	route := RouteMap{SchemaVersion: 1, Generation: input.Generation, SharedEntry: input.Runtime.Entry.Name, Nodes: []RouteNode{}, Groups: []RouteGroup{}, Rules: []RouteRule{}, Apps: apps.routes}
	nativeRules := []string{}
	for i, r := range intent.Rules {
		to, e := target(r.Target, ruleField(r.ID)+".target")
		if e != nil {
			return fail(e)
		}
		atoms, e := compileAtoms(r.Match, apps.managed)
		if e != nil {
			return fail(e)
		}
		if len(atoms) == 1 {
			nativeRules = append(nativeRules, atoms[0]+","+to)
		} else {
			if len(atoms) < 2 || len(atoms) > 8 {
				return fail(ErrCompileUnsupported)
			}
			children := make([]string, len(atoms))
			for i, a := range atoms {
				children[i] = "(" + a + ")"
			}
			nativeRules = append(nativeRules, "AND,("+strings.Join(children, ",")+"),"+to)
		}
		route.Rules = append(route.Rules, RouteRule{ID: r.ID, Index: i, Target: to})
	}
	defaultTarget, e := target(intent.DefaultTarget, "default_target")
	if e != nil {
		return fail(e)
	}
	route.DefaultTarget = defaultTarget
	nativeRules = append(nativeRules, "MATCH,"+defaultTarget)
	customCount := len(groups) + len(needed)
	if direct {
		customCount++
	}
	if customCount > 64 || len(nativeRules) > 64 {
		return fail(ErrCompileUnsupported)
	}
	assets := []config.BundleAsset{{Kind: "intent", Name: "intent.json", Data: canonical}}
	assetBytes := int64(len(canonical))
	inputs := []config.BundleInput{{Kind: "core_binary", ID: "mihomo", SHA256: cap.Binary.SHA256}, {Kind: "capabilities", ID: "core-capabilities", SHA256: capSHA}}
	for _, asset := range apps.assets {
		if int64(len(asset.Data)) > config.MaxBundleBytes-assetBytes {
			return fail(ErrCompileUnsupported)
		}
		assetBytes += int64(len(asset.Data))
		assets = append(assets, asset)
	}
	inputs = append(inputs, apps.inputs...)
	ids := make([]config.ID, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nodes := map[string]map[string]any{}
	aliases := map[string]subscription.NodeRef{}
	for _, id := range ids {
		s, ok := input.Sources[id]
		if !ok || s.SourceID != id || s.Validate() != nil {
			return fail(compileDiagnostic("source_unavailable", referenceFields[id]))
		}
		asset, e := s.SensitiveAssetBytes()
		if e != nil {
			return fail(ErrCompile)
		}
		if int64(len(asset)) > config.MaxBundleBytes-assetBytes {
			return fail(ErrCompileUnsupported)
		}
		assetBytes += int64(len(asset))
		name := "provider-" + compileDigest([]byte(id)) + ".json"
		assets = append(assets, config.BundleAsset{Kind: "provider", Name: name, Data: asset})
		inputs = append(inputs, config.BundleInput{Kind: "provider", ID: name, SHA256: compileDigest(asset)})
		configs, e := s.SensitiveProxyConfigs()
		if e != nil {
			return fail(ErrCompile)
		}
		for _, n := range configs {
			nodeID, ok := n["name"].(string)
			if !ok {
				return fail(ErrCompile)
			}
			ref := subscription.NodeRef{SubscriptionID: id, NodeID: config.ID(nodeID)}
			if !needed[ref] {
				continue
			}
			alias := nodeAlias(ref.NodeID)
			if old, exists := aliases[alias]; exists && old != ref {
				return fail(ErrCompile)
			}
			aliases[alias] = ref
			n["name"] = alias
			nodes[alias] = n
		}
	}
	for ref := range needed {
		if aliases[nodeAlias(ref.NodeID)] != ref {
			return fail(ErrCompile)
		}
	}
	if direct {
		nodes["node:local-direct"] = map[string]any{"name": "node:local-direct", "type": "direct"}
	}
	proxyNames := make([]string, 0, len(nodes))
	for name := range nodes {
		proxyNames = append(proxyNames, name)
	}
	sort.Strings(proxyNames)
	cfg := profile.Config{Mode: "rule", LogLevel: "silent", FindProcessMode: "off", Controller: input.Runtime.Controller, Secret: input.Runtime.Secret, Listeners: []profile.Entry{input.Runtime.Entry}, Proxies: []map[string]any{}, Groups: []profile.Group{}, Rules: nativeRules}
	cfg.Listeners = append(cfg.Listeners, apps.entries...)
	for _, name := range proxyNames {
		cfg.Proxies = append(cfg.Proxies, nodes[name])
		if ref, ok := aliases[name]; ok {
			route.Nodes = append(route.Nodes, RouteNode{Alias: name, SubscriptionID: ref.SubscriptionID, NodeID: ref.NodeID})
		}
	}
	groupIDs := make([]config.ID, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	for _, id := range groupIDs {
		g := groups[id]
		out := profile.Group{Name: groupAlias(id), Type: "select", Proxies: []string{}}
		switch {
		case g.Strategy == "follow":
			if g.Follows == nil {
				return fail(ErrCompile)
			}
			out.Proxies = []string{groupAlias(*g.Follows)}
		case g.Reject:
			out.Proxies = []string{"REJECT"}
		case g.Strategy == "manual":
			if g.Selected == nil {
				return fail(ErrCompile)
			}
			out.Proxies = []string{nodeAlias(g.Selected.NodeID)}
		case g.Strategy == "auto":
			if g.Probe == nil {
				return fail(ErrCompile)
			}
			p := *g.Probe
			lazy := false
			out.Type = "url-test"
			out.URL = p.URL
			out.Interval = p.IntervalSeconds
			out.Timeout = p.TimeoutMS
			out.Tolerance = &p.ToleranceMS
			out.Lazy = &lazy
			out.ExpectedStatus = "200-299"
			for _, ref := range g.Candidates {
				out.Proxies = append(out.Proxies, nodeAlias(ref.NodeID))
			}
		default:
			return fail(ErrCompile)
		}
		cfg.Groups = append(cfg.Groups, out)
		route.Groups = append(route.Groups, RouteGroup{ID: id, Alias: out.Name, Strategy: g.Strategy, SourceID: g.SourceID, Members: append([]string{}, out.Proxies...)})
	}
	generated, e := json.Marshal(cfg)
	if e != nil {
		return fail(ErrCompile)
	}
	snapshot, e := profile.Project(generated, string(input.Generation))
	if e != nil {
		return fail(ErrCompile)
	}
	projected, e := json.Marshal(snapshot)
	if e != nil {
		return fail(ErrCompile)
	}
	routeBytes, e := json.Marshal(route)
	if e != nil {
		return fail(ErrCompile)
	}
	rulePolicy, e := json.Marshal(struct {
		SchemaVersion int           `json:"schema_version"`
		Rules         []config.Rule `json:"rules"`
		DefaultTarget config.Target `json:"default_target"`
	}{1, intent.Rules, intent.DefaultTarget})
	if e != nil {
		return fail(ErrCompile)
	}
	assets = append(assets, config.BundleAsset{Kind: "rule", Name: "rule-policy.json", Data: rulePolicy}, config.BundleAsset{Kind: "generated_config", Name: "core.json", Data: generated}, config.BundleAsset{Kind: "route_map", Name: "route-map.json", Data: routeBytes}, config.BundleAsset{Kind: "validation", Name: mihomo.SnapshotFile, Data: projected})
	inputs = append(inputs, config.BundleInput{Kind: "rule_asset", ID: "rule-policy.json", SHA256: compileDigest(rulePolicy)})
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].Kind != inputs[j].Kind {
			return inputs[i].Kind < inputs[j].Kind
		}
		return inputs[i].ID < inputs[j].ID
	})
	m, files, e := config.AssembleBundle(input.Generation, compilerVersion, capSHA, []config.BinaryReference{cap.Binary}, assets)
	if e != nil {
		return fail(ErrCompile)
	}
	return Compilation{intent: bytes.Clone(canonical), manifest: m, files: files, inputs: inputs}, nil
}

func compileAtoms(p config.Predicate, managed map[config.ID]bool) ([]string, error) {
	switch p.Kind {
	case "app":
		if p.AppID == nil || !managed[*p.AppID] {
			return nil, ErrCompileUnsupported
		}
		return []string{"IN-NAME," + AppEntryAlias(*p.AppID)}, nil
	case "domain":
		kind := "DOMAIN"
		if p.Operator == "suffix" {
			kind = "DOMAIN-SUFFIX"
		}
		return []string{kind + "," + strings.TrimSuffix(strings.ToLower(p.Value), ".")}, nil
	case "ip_cidr":
		prefix, e := netip.ParsePrefix(p.Value)
		if e != nil {
			return nil, ErrCompile
		}
		if !prefix.Addr().Is4() {
			return nil, ErrCompileUnsupported
		}
		return []string{"IP-CIDR," + prefix.Masked().String()}, nil
	case "all":
		out := []string{}
		for _, p := range p.Conditions {
			atoms, e := compileAtoms(p, managed)
			if e != nil {
				return nil, e
			}
			out = append(out, atoms...)
			if len(out) > 8 {
				return nil, ErrCompileUnsupported
			}
		}
		return out, nil
	default:
		return nil, ErrCompileUnsupported
	}
}
func groupAlias(id config.ID) string  { return "group:" + compileDigest([]byte(id)) }
func nodeAlias(id config.ID) string   { return "node:" + string(id) }
func compileDigest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
