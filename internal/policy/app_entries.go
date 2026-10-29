//go:build linux && amd64

package policy

import (
	"reflect"
	"sort"

	"hoopa/internal/config"
	profile "hoopa/protocol/sourceprofile"
)

const AppCompilerVersion = "hoopa.apps.v1"

// AppEntryAlias bridges the one canonical private binding naming algorithm.
// An entry context is not evidence of an original OS process identity.
func AppEntryAlias(id config.ID) string { return config.AppEntryAlias(id) }

type RouteApp struct {
	ID    config.ID `json:"id"`
	Entry string    `json:"entry"`
}
type compiledAppEntries struct {
	entries []profile.Entry
	routes  []RouteApp
	assets  []config.BundleAsset
	inputs  []config.BundleInput
	managed map[config.ID]bool
}

// compileAppEntries owns every accepted private record and entry. It validates
// material independently; an entry beside an unchanged digest cannot replace
// its canonical record. Real parent-settings/installed approval is established
// by the coordinator, not inferred here from a syntactically valid digest.
func compileAppEntries(intent config.Intent, input CompileInput, appProfile bool) (compiledAppEntries, error) {
	fail := func(err error) (compiledAppEntries, error) { return compiledAppEntries{}, err }
	out := compiledAppEntries{managed: map[config.ID]bool{}}
	ids := []config.ID{}
	// Bypass is access-environment metadata; ordered rules alone define routing.
	for _, app := range intent.Apps {
		if app.Metering {
			return fail(ErrCompileUnsupported)
		}
		if app.Mode == "managed" {
			ids = append(ids, app.ID)
			out.managed[app.ID] = true
			if len(ids) > 3 {
				return fail(ErrCompileUnsupported)
			}
		}
	}
	if !appProfile && len(intent.Apps) != 0 {
		return fail(ErrCompileUnsupported)
	}
	if len(input.Runtime.AppEntries) != len(ids) || len(input.AppBindings) != len(ids) {
		return fail(ErrCompile)
	}
	for id := range input.Runtime.AppEntries {
		if !out.managed[id] {
			return fail(ErrCompile)
		}
	}
	for id := range input.AppBindings {
		if !out.managed[id] {
			return fail(ErrCompile)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		record, err := config.DecodeAppBinding(input.AppBindings[id])
		if err != nil || record.AppID() != id || record.SettingsSHA256() != input.SourceSettingsSHA256 {
			return fail(ErrCompile)
		}
		entry, err := record.SensitiveEntry()
		if err != nil || !reflect.DeepEqual(entry, input.Runtime.AppEntries[id]) {
			return fail(ErrCompile)
		}
		name, err := config.AppBindingFilename(id)
		if err != nil {
			return fail(ErrCompile)
		}
		raw, err := record.SensitiveBytes()
		if err != nil {
			return fail(ErrCompile)
		}
		out.entries = append(out.entries, entry)
		out.routes = append(out.routes, RouteApp{ID: id, Entry: entry.Name})
		out.assets = append(out.assets, config.BundleAsset{Kind: "app_binding", Name: name, Data: raw})
		out.inputs = append(out.inputs, config.BundleInput{Kind: "app_binding", ID: name, SHA256: compileDigest(raw)})
	}
	return out, nil
}
