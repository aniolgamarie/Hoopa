//go:build linux

package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSourceAssetsCannotOverlapRuntime(t *testing.T) {
	for _, suffix := range []string{"", "child"} {
		t.Run("overlap-"+suffix, func(t *testing.T) {
			paths := storePaths(t)
			paths.Runtime = filepath.Join(paths.State, "source-assets", suffix)
			before := tree(t, filepath.Dir(paths.State))
			if err := paths.Validate(); err == nil {
				t.Fatal("durable source assets treated as runtime cleanup scope")
			}
			for _, writer := range []bool{false, true} {
				store, err := OpenStore(paths, writer)
				if err == nil || store != nil {
					if store != nil {
						store.Close()
					}
					t.Fatal("overlapping paths reached store initialization")
				}
			}
			if !reflect.DeepEqual(before, tree(t, filepath.Dir(paths.State))) {
				t.Fatal("overlap refusal mutated the filesystem")
			}
		})
	}
	paths := storePaths(t)
	paths.Runtime = filepath.Join(paths.State, "source-assets-other")
	if err := paths.Validate(); err != nil {
		t.Fatal("sibling prefix incorrectly overlaps", err)
	}
}
