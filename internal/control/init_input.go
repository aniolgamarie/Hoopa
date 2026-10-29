//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"hoopa/internal/config"
)

// PrepareInitInput performs no IO and grants no writer authority to an
// incomplete declaration. Only metadata presence is interpreted in that case;
// it deliberately does not replace missing metadata with parser placeholders.
func PrepareInitInput(options InitOptions) (InitInput, error) {
	if len(options.Intent) > MaxRequestBytes || len(options.AppPorts) > config.MaxAppBindings {
		return InitInput{}, ErrRequest
	}
	if !initInputPath(options.MihomoPath) || options.GostPath != "" && !initInputPath(options.GostPath) {
		return InitInput{}, ErrRequest
	}
	if (options.ControllerPort == 0) != (options.SharedPort == 0) {
		return InitInput{}, ErrRequest
	}
	ports := map[uint16]bool{}
	for _, port := range []uint16{options.ControllerPort, options.SharedPort} {
		if port == 0 {
			continue
		}
		if ports[port] {
			return InitInput{}, ErrRequest
		}
		ports[port] = true
	}
	for id, port := range options.AppPorts {
		if _, err := config.ParseID(string(id)); err != nil || port == 0 || ports[port] {
			return InitInput{}, ErrRequest
		}
		ports[port] = true
	}
	owned := options
	owned.Intent = bytes.Clone(options.Intent)
	if options.AppPorts != nil {
		owned.AppPorts = make(map[config.ID]uint16, len(options.AppPorts))
		for id, port := range options.AppPorts {
			owned.AppPorts[id] = port
		}
	}
	if len(options.Intent) == 0 {
		return InitInput{options: owned, missing: []string{"intent"}}, nil
	}
	canonical, err := config.CanonicalJSON(options.Intent)
	if err != nil || len(canonical) > MaxRequestBytes {
		return InitInput{}, ErrRequest
	}
	document, ok := initInputObject(canonical)
	if !ok {
		return InitInput{}, ErrRequest
	}
	core, ok := initInputObject(document["core"])
	if !ok {
		return InitInput{}, ErrRequest
	}
	mihomo, ok := initInputObject(core["mihomo"])
	if !ok {
		return InitInput{}, ErrRequest
	}
	missing := []string{}
	if !initInputBinary(mihomo, options.MihomoPath, "mihomo", &missing) {
		return InitInput{}, ErrRequest
	}
	if raw, exists := core["gost"]; exists {
		gost, ok := initInputObject(raw)
		if !ok || !initInputBinary(gost, options.GostPath, "gost", &missing) {
			return InitInput{}, ErrRequest
		}
	} else if options.GostPath != "" {
		return InitInput{}, ErrRequest
	}
	if len(missing) != 0 {
		return InitInput{options: owned, missing: missing}, nil
	}
	intent, err := config.ParseIntent(canonical, config.IntentDraft)
	if err != nil {
		return InitInput{}, ErrRequest
	}
	apps := make(map[config.ID]config.App, len(intent.Apps))
	for _, app := range intent.Apps {
		apps[app.ID] = app
	}
	for id := range owned.AppPorts {
		app, exists := apps[id]
		if !exists || app.Mode != "managed" || app.Metering {
			return InitInput{}, ErrRequest
		}
	}
	return InitInput{options: owned, intent: intent, canonical: canonical, missing: missing}, nil
}
func initInputPath(path string) bool {
	return path != "" && len(path) <= 4096 && !strings.ContainsRune(path, 0) && filepath.IsAbs(path) && filepath.Clean(path) == path
}
func initInputObject(raw []byte) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, false
	}
	return object, true
}
func initInputBinary(object map[string]json.RawMessage, flagPath, kind string, missing *[]string) bool {
	var path string
	if json.Unmarshal(object["path"], &path) != nil || !initInputPath(path) || flagPath != "" && path != flagPath {
		return false
	}
	for _, field := range []string{"version", "sha256"} {
		raw, exists := object[field]
		value := ""
		if exists && (json.Unmarshal(raw, &value) != nil || bytes.Equal(raw, []byte("null"))) {
			return false
		}
		if !exists || value == "" {
			*missing = append(*missing, kind+"_"+field)
		}
	}
	return true
}
