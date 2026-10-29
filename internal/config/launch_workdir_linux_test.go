//go:build linux

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchWorkdirValidation(t *testing.T) {
	_, _, base, _ := runStoreFixture(t)
	_, files := fixtureBundle()
	data := files["core.yaml"]
	for _, name := range []string{"valid", "missing-directory", "missing-config", "wrong-bytes", "empty", "oversize", "directory-mode", "config-mode", "config-symlink", "directory-symlink", "hardlink", "malformed-intent"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			work := filepath.Join(parent, "work")
			if err := os.Mkdir(work, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(work, "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			intent := base
			intent.Workdir = work
			switch name {
			case "missing-directory":
				os.Remove(path)
				os.Remove(work)
			case "missing-config":
				os.Remove(path)
			case "wrong-bytes":
				os.WriteFile(path, []byte("wrong"), 0600)
			case "empty":
				os.WriteFile(path, nil, 0600)
				intent.ConfigSHA256 = digest(nil)
			case "oversize":
				raw := []byte(strings.Repeat("x", 65537))
				os.WriteFile(path, raw, 0600)
				intent.ConfigSHA256 = digest(raw)
			case "directory-mode":
				os.Chmod(work, 0755)
			case "config-mode":
				os.Chmod(path, 0644)
			case "config-symlink":
				os.Rename(path, path+"-other")
				os.Symlink(path+"-other", path)
			case "directory-symlink":
				os.Rename(work, work+"-other")
				os.Symlink(work+"-other", work)
			case "hardlink":
				os.Link(path, path+"-other")
			case "malformed-intent":
				intent.Controller = "localhost:1"
			}
			before := tree(t, parent)
			root, err := OpenRunWorkdir(intent)
			if name == "valid" {
				if err != nil || root == nil {
					t.Fatal(err)
				}
				b, e := root.ReadFile("config.json")
				if e != nil || string(b) != string(data) {
					t.Fatal("anchored config changed", e)
				}
			} else if err == nil || root != nil {
				t.Fatal("unsafe workdir accepted")
			}
			if root != nil {
				root.Close()
			}
			if !reflect.DeepEqual(before, tree(t, parent)) {
				t.Fatal("validation repaired or wrote workspace")
			}
		})
	}
}
