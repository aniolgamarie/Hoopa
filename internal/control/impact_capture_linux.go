//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

// Caller holds c.mu. The applied side is the exact active bundle, never the
// latest downloaded source or a reconstruction from the candidate declaration.
func (c *Coordinator) captureImpactBaseLocked(base config.ActivePointer) (*impactGeneration, error) {
	if base.Generation == nil {
		return nil, nil
	}
	bundle, err := c.store.ReadBundle(*base.Generation)
	if err != nil || bundle.ManifestSHA256 != base.ManifestSHA256 {
		return nil, config.ErrReadUnavailable
	}
	value, err := impactFromBundle(bundle.Manifest, bundle.Files)
	if err != nil || value.Generation != *base.Generation || value.ManifestSHA256 != base.ManifestSHA256 {
		return nil, config.ErrReadUnavailable
	}
	return &value, nil
}

// This is a configuration metadata projection. It verifies the route map's
// schema and links to the stored Intent/generated profile; it does not observe
// a running route, resolve an auto group's current member or prove reachability.
func impactFromBundle(manifest config.BundleManifest, files map[string][]byte) (impactGeneration, error) {
	fail := func() (impactGeneration, error) { return impactGeneration{}, config.ErrReadUnavailable }
	if manifest.Version != 1 || !planID(string(manifest.Generation)) || len(manifest.Binaries) != 1 {
		return fail()
	}
	capability, err := mihomo.ApprovedCapability(manifest.Binaries[0])
	if err != nil {
		return fail()
	}
	capSHA, err := capability.Digest()
	if err != nil || capSHA != manifest.CapabilitiesDigest {
		return fail()
	}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		return fail()
	}
	out := impactGeneration{Generation: manifest.Generation, ManifestSHA256: hash(append(rawManifest, '\n')), Nodes: []PlanImpactNode{}, Groups: []string{}, Rules: []string{}, Apps: []string{}}
	// Older basic-v1 material did not promise a semantic route map. Retain its
	// exact restoration identity and explicitly disclose incomplete metadata.
	if capability.Protocol == 1 {
		return out, nil
	}
	if capability.Mode == "basic-sources" && manifest.CompilerVersion != policy.CompilerVersion || capability.Mode == "basic-app-routing" && manifest.CompilerVersion != policy.AppCompilerVersion {
		return fail()
	}
	selected := map[string][]byte{}
	for _, file := range manifest.Files {
		if file.Kind != "intent" && file.Kind != "route_map" && file.Kind != "generated_config" {
			continue
		}
		raw, ok := files[file.Name]
		if file.Kind == "route_map" && len(raw) > 1<<20 {
			return fail()
		}
		if !ok || selected[file.Kind] != nil || len(raw) == 0 || int64(len(raw)) != file.Bytes || hash(raw) != file.SHA256 {
			return fail()
		}
		selected[file.Kind] = raw
	}
	intent, err := config.ParseIntent(selected["intent"], config.IntentApply)
	if err != nil || intent.Core.Mihomo != capability.Binary {
		return fail()
	}
	generated, err := profile.Validate(selected["generated_config"], string(manifest.Generation))
	if err != nil {
		return fail()
	}
	var route policy.RouteMap
	if json.Unmarshal(selected["route_map"], &route) != nil {
		return fail()
	}
	writer, err := json.Marshal(route)
	if err != nil || !bytes.Equal(writer, selected["route_map"]) || route.SchemaVersion != 1 || route.Generation != manifest.Generation || route.Nodes == nil || route.Groups == nil || route.Rules == nil || len(route.Nodes) > 64 || len(route.Groups) > 64 || len(route.Rules) > 64 || len(route.Apps) > 3 || route.SharedEntry != generated.Listeners[0].Name {
		return fail()
	}
	sources := map[config.ID]bool{}
	for _, source := range intent.Subscriptions {
		sources[source.ID] = true
	}
	nodes := map[string]PlanImpactNode{}
	for _, node := range route.Nodes {
		if !sources[node.SubscriptionID] || !planID(string(node.NodeID)) || node.Alias != "node:"+string(node.NodeID) {
			return fail()
		}
		if _, exists := nodes[node.Alias]; exists {
			return fail()
		}
		value := PlanImpactNode{SubscriptionID: node.SubscriptionID, NodeID: node.NodeID}
		nodes[node.Alias] = value
		out.Nodes = append(out.Nodes, value)
	}
	for _, proxy := range generated.Proxies {
		name, _ := proxy["name"].(string)
		if name == "node:local-direct" && proxy["type"] == "direct" {
			continue
		}
		if _, exists := nodes[name]; !exists {
			return fail()
		}
	}
	proxyNames := map[string]bool{}
	for _, proxy := range generated.Proxies {
		name, _ := proxy["name"].(string)
		proxyNames[name] = true
	}
	for name := range nodes {
		if !proxyNames[name] {
			return fail()
		}
	}
	groups := map[config.ID]policy.RouteGroup{}
	if len(route.Groups) != len(intent.Groups) || len(route.Groups) != len(generated.Groups) {
		return fail()
	}
	for _, group := range route.Groups {
		if _, exists := groups[group.ID]; exists || !planID(string(group.ID)) || group.Alias != "group:"+hash([]byte(group.ID)) || !sources[group.SourceID] || group.Members == nil {
			return fail()
		}
		groups[group.ID] = group
		out.Groups = append(out.Groups, string(group.ID))
	}
	for _, group := range generated.Groups {
		found := false
		for _, entry := range route.Groups {
			if group.Name == entry.Alias && reflect.DeepEqual(group.Proxies, entry.Members) {
				found = true
				break
			}
		}
		if !found {
			return fail()
		}
	}
	for _, group := range intent.Groups {
		entry, ok := groups[group.ID]
		if !ok || entry.Strategy != group.Strategy {
			return fail()
		}
		if group.Strategy == "follow" {
			parent, exists := groups[*group.Follows]
			if !exists || entry.SourceID != parent.SourceID || !reflect.DeepEqual(entry.Members, []string{parent.Alias}) {
				return fail()
			}
			continue
		}
		var source config.ID
		if group.Source.Kind == "active" {
			source = *intent.Selection.ActiveSubscription
		} else {
			source = *group.Source.SubscriptionID
		}
		if entry.SourceID != source || len(entry.Members) == 0 {
			return fail()
		}
		for _, name := range entry.Members {
			if name == "REJECT" && len(entry.Members) == 1 && group.Strategy == "auto" && !group.Required {
				continue
			}
			node, ok := nodes[name]
			if !ok || node.SubscriptionID != source || group.SelectedNodeID != nil && node.NodeID != *group.SelectedNodeID {
				return fail()
			}
		}
	}
	targetAlias := func(target config.Target) (string, bool) {
		switch target.Kind {
		case "reject":
			return "REJECT", true
		case "direct":
			return "node:local-direct", proxyNames["node:local-direct"]
		case "group":
			group, exists := groups[*target.GroupID]
			return group.Alias, exists
		case "node":
			name := "node:" + string(*target.NodeID)
			node, exists := nodes[name]
			return name, exists && node.SubscriptionID == *target.SubscriptionID
		}
		return "", false
	}
	if len(route.Rules) != len(intent.Rules) || len(generated.Rules) != len(intent.Rules)+1 {
		return fail()
	}
	for i, rule := range intent.Rules {
		entry := route.Rules[i]
		target, valid := targetAlias(rule.Target)
		if !valid || entry.ID != rule.ID || entry.Index != i || entry.Target != target || !strings.HasSuffix(generated.Rules[i], ","+target) {
			return fail()
		}
		out.Rules = append(out.Rules, string(rule.ID))
	}
	target, valid := targetAlias(intent.DefaultTarget)
	if !valid || route.DefaultTarget != target || generated.Rules[len(generated.Rules)-1] != "MATCH,"+target {
		return fail()
	}
	apps := map[config.ID]string{}
	for _, app := range route.Apps {
		if _, exists := apps[app.ID]; exists || !planID(string(app.ID)) || app.Entry != policy.AppEntryAlias(app.ID) {
			return fail()
		}
		apps[app.ID] = app.Entry
		out.Apps = append(out.Apps, string(app.ID))
	}
	managed := 0
	for _, app := range intent.Apps {
		if app.Metering {
			return fail()
		}
		if app.Mode == "managed" {
			managed++
			if apps[app.ID] != policy.AppEntryAlias(app.ID) {
				return fail()
			}
		}
	}
	if managed != len(apps) || len(generated.Listeners) != managed+1 {
		return fail()
	}
	for _, listener := range generated.Listeners[1:] {
		found := false
		for _, name := range apps {
			found = found || name == listener.Name
		}
		if !found {
			return fail()
		}
	}
	out.Known = true
	if !validImpactGeneration(out) {
		return fail()
	}
	return out, nil
}
