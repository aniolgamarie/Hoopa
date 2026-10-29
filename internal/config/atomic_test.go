//go:build linux

package config

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestStoreDurablePublicationOrder(t *testing.T) {
	p := storePaths(t)
	s := openWriter(t, p)
	var seen []string
	s.fault = func(point string) error { seen = append(seen, point); return nil }
	m, files := fixtureBundle()
	sha, err := s.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishActive(pointerFor(m.Generation, sha)); err != nil {
		t.Fatal(err)
	}
	want := []string{"bundle.file:core.yaml.write", "bundle.file:core.yaml.sync", "bundle.file:core.yaml.closed", "bundle.file:intent.json.write", "bundle.file:intent.json.sync", "bundle.file:intent.json.closed", "bundle.manifest.write", "bundle.manifest.sync", "bundle.manifest.closed", "bundle.temp-sync", "bundle.rename", "bundle.parent-sync", "active.file.write", "active.file.sync", "active.file.closed", "active.rename", "active.parent-sync"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("publication order\ngot %v\nwant %v", seen, want)
	}
}

func TestStoreBundleFailureBoundaries(t *testing.T) {
	for _, point := range []string{"bundle.file:core.yaml.write", "bundle.file:core.yaml.sync", "bundle.file:core.yaml.closed", "bundle.manifest.write", "bundle.manifest.sync", "bundle.manifest.closed", "bundle.temp-sync", "bundle.rename", "bundle.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			before, err := s.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("test filesystem boundary failure")
			hit := 0
			s.fault = func(at string) error {
				if at == point {
					hit++
					return injected
				}
				return nil
			}
			m, files := fixtureBundle()
			_, err = s.PublishBundle(m, files)
			if !errors.Is(err, injected) || hit != 1 {
				t.Fatalf("unhit failure %v/%d", err, hit)
			}
			uncertain := point == "bundle.parent-sync"
			if errors.Is(err, ErrUncertain) != uncertain {
				t.Fatalf("uncertainty %v", err)
			}
			after, err := s.ReadActive()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("bundle failure moved active %+v %v", after, err)
			}
			_, err = s.ReadBundle(m.Generation)
			if uncertain && err != nil {
				t.Fatalf("published generation unreadable: %v", err)
			}
			if !uncertain && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepublish generated authority: %v", err)
			}
			s.fault = nil
			if uncertain {
				if _, err = s.PublishBundle(m, files); !errors.Is(err, ErrUncertain) {
					t.Fatalf("uncertain writer reused %v", err)
				}
				if err = s.PublishActive(before); !errors.Is(err, ErrUncertain) {
					t.Fatalf("uncertain active reused %v", err)
				}
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				s = openWriter(t, p)
			}
			if _, err = s.PublishBundle(m, files); err != nil {
				t.Fatalf("retry/reconcile %v", err)
			}
		})
	}
}

func TestStoreActiveFailureBoundaries(t *testing.T) {
	for _, point := range []string{"active.file.write", "active.file.sync", "active.file.closed", "active.rename", "active.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p := storePaths(t)
			s := openWriter(t, p)
			m, files := fixtureBundle()
			sha, err := s.PublishBundle(m, files)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			target := pointerFor(m.Generation, sha)
			injected := errors.New("test filesystem boundary failure")
			hit := 0
			s.fault = func(at string) error {
				if at == point {
					hit++
					return injected
				}
				return nil
			}
			err = s.PublishActive(target)
			if !errors.Is(err, injected) || hit != 1 {
				t.Fatalf("unhit failure %v/%d", err, hit)
			}
			uncertain := point == "active.parent-sync"
			if errors.Is(err, ErrUncertain) != uncertain {
				t.Fatalf("uncertainty %v", err)
			}
			after, err := s.ReadActive()
			if err != nil {
				t.Fatal(err)
			}
			want := before
			if uncertain {
				want = target
			}
			if !reflect.DeepEqual(after, want) {
				t.Fatalf("partial pointer %+v; want %+v", after, want)
			}
			s.fault = nil
			if uncertain {
				if err = s.PublishActive(before); !errors.Is(err, ErrUncertain) {
					t.Fatalf("writer reused %v", err)
				}
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openWriter(t, p)
			after, err = s.ReadActive()
			if err != nil || !reflect.DeepEqual(after, want) {
				t.Fatalf("reopen differs %+v %v", after, err)
			}
			if err = s.PublishActive(target); err != nil {
				t.Fatal(err)
			}
		})
	}
}
