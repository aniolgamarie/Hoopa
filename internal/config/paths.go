package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Paths separates persistent configuration/state, disposable cache, and runtime resources.
// Resolving or validating these paths never inspects or creates filesystem objects.
type Paths struct {
	Config  string
	State   string
	Cache   string
	Runtime string
}

// ResolvePaths interprets the four XDG locations without reading process environment.
// Nonempty XDG values must already be absolute and clean. Home is needed only for
// config/state/cache fallbacks; an unset runtime directory falls back inside State.
func ResolvePaths(home string, env map[string]string) (Paths, error) {
	if strings.ContainsRune(home, '\x00') {
		return Paths{}, fmt.Errorf("home contains NUL")
	}
	resolve := func(variable, fallback string) (string, error) {
		if base := env[variable]; base != "" {
			if strings.ContainsRune(base, '\x00') || !filepath.IsAbs(base) || filepath.Clean(base) != base {
				return "", fmt.Errorf("%s must be an absolute clean path without NUL", variable)
			}
			return filepath.Join(base, "hoopa"), nil
		}
		if home == "" || !filepath.IsAbs(home) {
			return "", fmt.Errorf("absolute nonempty home required for %s fallback", variable)
		}
		return filepath.Join(home, fallback, "hoopa"), nil
	}
	var p Paths
	var err error
	if p.Config, err = resolve("XDG_CONFIG_HOME", ".config"); err != nil {
		return Paths{}, err
	}
	if p.State, err = resolve("XDG_STATE_HOME", filepath.Join(".local", "state")); err != nil {
		return Paths{}, err
	}
	if p.Cache, err = resolve("XDG_CACHE_HOME", ".cache"); err != nil {
		return Paths{}, err
	}
	if env["XDG_RUNTIME_DIR"] == "" {
		p.Runtime = filepath.Join(p.State, "runtime")
	} else if p.Runtime, err = resolve("XDG_RUNTIME_DIR", ""); err != nil {
		return Paths{}, err
	}
	if err = p.Validate(); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// pathContains is lexical and component based: /state does not contain /state-a.
// Symlink, ownership, permissions and filesystem checks belong to explicit store opens.
func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

// Validate rejects overlapping authority and cleanup scopes. A runtime directory
// may live strictly inside State, except within/above its persistent subdirectories.
func (p Paths) Validate() error {
	fields := []struct{ name, path string }{{"config", p.Config}, {"state", p.State}, {"cache", p.Cache}, {"runtime", p.Runtime}}
	for _, field := range fields {
		path := field.path
		if path == "" || strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) == path {
			return fmt.Errorf("%s must be an absolute clean non-root path without NUL", field.name)
		}
	}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			if pathContains(fields[i].path, fields[j].path) || pathContains(fields[j].path, fields[i].path) {
				return fmt.Errorf("%s and %s paths overlap", fields[i].name, fields[j].name)
			}
		}
	}
	for _, field := range fields[:3] {
		if pathContains(p.Runtime, field.path) {
			return fmt.Errorf("runtime must not equal or contain %s", field.name)
		}
		if field.name != "state" && pathContains(field.path, p.Runtime) {
			return fmt.Errorf("runtime must not be inside %s", field.name)
		}
	}
	for _, name := range []string{"bundles", "operations", "runs", "credentials", "source-assets", "app-launches", "draft-input-v1.json", "active.json", ".writer.lock"} {
		persistent := filepath.Join(p.State, name)
		if pathContains(p.Runtime, persistent) || pathContains(persistent, p.Runtime) {
			return fmt.Errorf("runtime overlaps persistent state/%s", name)
		}
	}
	return nil
}
