package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func pathsTestEnv() map[string]string {
	return map[string]string{"XDG_CONFIG_HOME": "/xdg/config", "XDG_STATE_HOME": "/xdg/state", "XDG_CACHE_HOME": "/xdg/cache", "XDG_RUNTIME_DIR": "/xdg/runtime"}
}
func TestPathsResolve(t *testing.T) {
	cases := []struct {
		name, home string
		env        map[string]string
		want       Paths
	}{
		{"defaults", "/home/user", nil, Paths{"/home/user/.config/hoopa", "/home/user/.local/state/hoopa", "/home/user/.cache/hoopa", "/home/user/.local/state/hoopa/runtime"}},
		{"explicit-home-unused", "relative-unused", pathsTestEnv(), Paths{"/xdg/config/hoopa", "/xdg/state/hoopa", "/xdg/cache/hoopa", "/xdg/runtime/hoopa"}},
		{"explicit-empty-home-unused", "", pathsTestEnv(), Paths{"/xdg/config/hoopa", "/xdg/state/hoopa", "/xdg/cache/hoopa", "/xdg/runtime/hoopa"}},
		{"partial", "/home/user", map[string]string{"XDG_STATE_HOME": "/custom/state"}, Paths{"/home/user/.config/hoopa", "/custom/state/hoopa", "/home/user/.cache/hoopa", "/custom/state/hoopa/runtime"}},
		{"empty-xdg-is-fallback", "/home/user", map[string]string{"XDG_CONFIG_HOME": "", "XDG_RUNTIME_DIR": ""}, Paths{"/home/user/.config/hoopa", "/home/user/.local/state/hoopa", "/home/user/.cache/hoopa", "/home/user/.local/state/hoopa/runtime"}},
		{"runtime-fallback-needs-no-home", "", map[string]string{"XDG_CONFIG_HOME": "/c", "XDG_STATE_HOME": "/s", "XDG_CACHE_HOME": "/k"}, Paths{"/c/hoopa", "/s/hoopa", "/k/hoopa", "/s/hoopa/runtime"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolvePaths(c.home, c.env)
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestPathsResolveInvalidInputs(t *testing.T) {
	for _, variable := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		for _, bad := range []struct{ name, path string }{{"relative", "relative"}, {"unclean", "/base/../other"}, {"nul", "/base\x00suffix"}} {
			t.Run(variable+"/"+bad.name, func(t *testing.T) {
				env := pathsTestEnv()
				env[variable] = bad.path
				if _, err := ResolvePaths("/home/user", env); err == nil {
					t.Fatal("invalid explicit XDG silently accepted/fell back")
				}
			})
		}
	}
	for _, home := range []struct{ name, path string }{{"empty", ""}, {"relative", "relative"}, {"nul", "/home/\x00user"}} {
		t.Run("home/"+home.name, func(t *testing.T) {
			if _, err := ResolvePaths(home.path, nil); err == nil {
				t.Fatal("invalid required home accepted")
			}
		})
	}
	t.Run("overlap-after-hoopa-suffix", func(t *testing.T) {
		env := pathsTestEnv()
		env["XDG_STATE_HOME"] = env["XDG_CONFIG_HOME"]
		if _, err := ResolvePaths("", env); err == nil {
			t.Fatal("overlapping resolved authority paths accepted")
		}
	})
}

func pathsTestValid() Paths {
	return Paths{"/private/config", "/private/state", "/private/cache", "/private/runtime"}
}
func TestPathsValidate(t *testing.T) {
	if err := pathsTestValid().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"config", "state", "cache", "runtime"} {
		for _, bad := range []struct{ name, path string }{{"empty", ""}, {"relative", "relative"}, {"root", "/"}, {"unclean", "/private/../other"}, {"nul", "/private/\x00other"}} {
			t.Run(field+"/"+bad.name, func(t *testing.T) {
				p := pathsTestValid()
				switch field {
				case "config":
					p.Config = bad.path
				case "state":
					p.State = bad.path
				case "cache":
					p.Cache = bad.path
				case "runtime":
					p.Runtime = bad.path
				}
				if err := p.Validate(); err == nil {
					t.Fatal("invalid direct Paths accepted")
				}
			})
		}
	}
	for _, pair := range []struct {
		name string
		a, b int
	}{{"config-state", 0, 1}, {"config-cache", 0, 2}, {"state-cache", 1, 2}} {
		for _, relation := range []string{"equal", "first-contains-second", "second-contains-first"} {
			t.Run(pair.name+"/"+relation, func(t *testing.T) {
				p := pathsTestValid()
				fields := []*string{&p.Config, &p.State, &p.Cache}
				*fields[pair.a] = "/authority/root"
				*fields[pair.b] = "/authority/root"
				if relation == "first-contains-second" {
					*fields[pair.b] = "/authority/root/child"
				}
				if relation == "second-contains-first" {
					*fields[pair.a] = "/authority/root/child"
				}
				if err := p.Validate(); err == nil {
					t.Fatal("overlapping persistent authority paths accepted")
				}
			})
		}
	}
	for _, kind := range []string{"config", "state", "cache"} {
		for _, relation := range []string{"equal", "contains", "inside"} {
			if kind == "state" && relation == "inside" {
				continue
			}
			t.Run("runtime/"+kind+"/"+relation, func(t *testing.T) {
				p := pathsTestValid()
				value := p.Config
				if kind == "state" {
					value = p.State
				}
				if kind == "cache" {
					value = p.Cache
				}
				switch relation {
				case "equal":
					p.Runtime = value
				case "inside":
					p.Runtime = filepath.Join(value, "child")
				case "contains":
					p.Runtime = "/private"
				}
				if err := p.Validate(); err == nil {
					t.Fatal("runtime authority/cleanup overlap accepted")
				}
			})
		}
	}
	for _, name := range []string{"bundles", "operations", "runs", "credentials", "source-assets", "app-launches", "draft-input-v1.json", "active.json", ".writer.lock"} {
		for _, suffix := range []string{"", "child"} {
			label := "equal"
			if suffix != "" {
				label = "inside"
			}
			t.Run("runtime/persistent-"+name+"/"+label, func(t *testing.T) {
				p := pathsTestValid()
				p.Runtime = filepath.Join(p.State, name, suffix)
				if err := p.Validate(); err == nil {
					t.Fatal("runtime overlaps persistent state subtree")
				}
			})
		}
	}
	for _, runtime := range []string{"/private/state/runtime", "/private/state/bundles-old", "/private/state-a"} {
		t.Run("allowed-"+filepath.Base(runtime), func(t *testing.T) {
			p := pathsTestValid()
			p.Runtime = runtime
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("sibling-prefixes", func(t *testing.T) {
		p := Paths{"/siblings/state-a", "/siblings/state", "/siblings/state-b", "/siblings/state/runtime"}
		if err := p.Validate(); err != nil {
			t.Fatal("component prefix mistaken for containment:", err)
		}
	})
}

func TestPathsResolutionCreatesNothing(t *testing.T) {
	parent := t.TempDir()
	before, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(parent, "absent-home")
	env := map[string]string{"XDG_CONFIG_HOME": filepath.Join(parent, "config"), "XDG_STATE_HOME": filepath.Join(parent, "state"), "XDG_CACHE_HOME": filepath.Join(parent, "cache")}
	for _, input := range []map[string]string{nil, env} {
		p, err := ResolvePaths(home, input)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Validate(); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{p.Config, p.State, p.Cache, p.Runtime} {
			if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("path resolution created/probed unexpected path %q: %v", path, err)
			}
		}
	}
	after, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("pure path resolution created filesystem entries")
	}
}
