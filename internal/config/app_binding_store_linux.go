//go:build linux

package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
)

const MaxAppBindings = 1024
const MaxAppBindingInventoryEntries = 4096
const MaxAppBindingHistoryBundles = 4096

var ErrAppBindingConflict = errors.New("APP_BINDING_ALREADY_INITIALIZED")
var ErrAppBindingInventoryBound = errors.New("APP_BINDING_INVENTORY_BOUND")

func AppBindingFilename(id ID) (string, error) {
	if _, err := ParseID(string(id)); err != nil {
		return "", ErrAppBinding
	}
	return "app-entry-" + strings.TrimPrefix(AppEntryAlias(id), "app:") + ".json", nil
}

func readAppBinding(dir *os.Root, name string) (AppBinding, error) {
	raw, err := readBounded(dir, name, MaxAppBindingBytes)
	if err != nil {
		return AppBinding{}, err
	}
	binding, err := DecodeAppBinding(raw)
	if err != nil {
		return AppBinding{}, ErrIntegrity
	}
	want, err := AppBindingFilename(binding.AppID())
	if err != nil || name != want {
		return AppBinding{}, ErrIntegrity
	}
	return binding, nil
}

// ReadAppBinding is input-only. It never creates a record or repairs lost input
// from bundle history; that evidence is available through the separate reader.
func (s *Store) ReadAppBinding(id ID) (AppBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return AppBinding{}, ErrClosed
	}
	if s.poisoned {
		return AppBinding{}, ErrUncertain
	}
	name, err := AppBindingFilename(id)
	if err != nil {
		return AppBinding{}, err
	}
	dir, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return AppBinding{}, errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	binding, err := readAppBinding(dir, name)
	if err != nil {
		return AppBinding{}, errors.Join(ErrReadUnavailable, err)
	}
	return binding, nil
}

// PublishAppBinding installs immutable prepared input only. The Coordinator is
// responsible for revision, parent settings, global uniqueness and lost-history
// checks while holding its writer boundary; this method never changes active.
func (s *Store) PublishAppBinding(binding AppBinding) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	raw, err := binding.SensitiveBytes()
	if err != nil {
		return "", ErrIntegrity
	}
	verified, err := DecodeAppBinding(raw)
	if err != nil {
		return "", ErrIntegrity
	}
	name, _ := AppBindingFilename(verified.AppID())
	dir, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	old, err := readAppBinding(dir, name)
	if err == nil {
		oldRaw, _ := old.SensitiveBytes()
		if !bytes.Equal(raw, oldRaw) {
			return "", ErrAppBindingConflict
		}
		f, err := openPrivateFile(dir, name, os.O_RDONLY)
		if err != nil {
			return "", err
		}
		err = s.checkpoint("app.binding-existing-file-sync")
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return "", s.uncertain(err)
		}
		if err = s.syncDir(dir, "app.binding-existing-parent-sync"); err != nil {
			return "", s.uncertain(err)
		}
		return verified.Digest(), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".app-binding-" + token
	defer dir.Remove(tmp)
	if err = s.writeNew(dir, tmp, raw, "app.binding-file"); err != nil {
		return "", err
	}
	if err = s.checkpoint("app.binding-rename"); err != nil {
		return "", err
	}
	if _, err = dir.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(ErrAppBindingConflict, err)
	}
	if err = dir.Rename(tmp, name); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(dir, "app.binding-parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return verified.Digest(), nil
}

// visitAppInputEntries enumerates through an already verified private root.
// Batches bound memory; every entry counts toward the scan budget, including
// unrelated input/staging names. Callers discard all accumulated values on error.
func visitAppInputEntries(root *os.Root, limit int, visit func(string) error) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	count := 0
	for {
		entries, readErr := f.ReadDir(64)
		for _, entry := range entries {
			count++
			if count > limit {
				return ErrAppBindingInventoryBound
			}
			if err := visit(entry.Name()); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func sortedAppBindings(bindings map[ID]AppBinding) []AppBinding {
	ids := make([]ID, 0, len(bindings))
	for id := range bindings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]AppBinding, 0, len(ids))
	for _, id := range ids {
		out = append(out, bindings[id])
	}
	return out
}

// ListAppBindings validates every published record, including unused apps. The
// non-app namespace is not interpreted or cleaned; its entries still bound work.
func (s *Store) ListAppBindings() ([]AppBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.poisoned {
		return nil, ErrUncertain
	}
	dir, err := openPrivateDir(s.root, "credentials", false)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	defer dir.Close()
	all := map[ID]AppBinding{}
	err = visitAppInputEntries(dir, MaxAppBindingInventoryEntries, func(name string) error {
		if !strings.HasPrefix(name, "app-entry-") {
			return nil
		}
		b, err := readAppBinding(dir, name)
		if err != nil {
			return err
		}
		if _, exists := all[b.AppID()]; exists {
			return ErrIntegrity
		}
		if len(all) == MaxAppBindings {
			return ErrAppBindingInventoryBound
		}
		all[b.AppID()] = b
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	return sortedAppBindings(all), nil
}

// ReadAppBindingHistory verifies every retained bundle, sequentially retaining
// only immutable binding records. A missing live input therefore does not erase
// prior identity evidence. No staged/unreadable bundle is inferred to be absent,
// no partial list is returned, and conflicting historical bindings fail closed.
func (s *Store) ReadAppBindingHistory() ([]AppBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if s.poisoned {
		return nil, ErrUncertain
	}
	bundles, err := openPrivateDir(s.root, "bundles", false)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	defer bundles.Close()
	all := map[ID]AppBinding{}
	err = visitAppInputEntries(bundles, MaxAppBindingHistoryBundles, func(name string) error {
		if !isDigest(name) {
			return ErrIntegrity
		}
		dir, err := openPrivateDir(bundles, name, false)
		if err != nil {
			return err
		}
		raw, readErr := readBounded(dir, "manifest.json", MaxManifestBytes)
		if err = errors.Join(readErr, dir.Close()); err != nil {
			return err
		}
		var manifest BundleManifest
		if decode(raw, &manifest) != nil || validateManifest(manifest) != nil {
			return ErrIntegrity
		}
		want, err := generationDirectory(manifest.Generation)
		if err != nil || want != name {
			return ErrIntegrity
		}
		// The same Store mutex is held; use the internal reader, avoiding recursive
		// locking and validating the complete bundle, not just binding-bearing files.
		bundle, err := s.readBundle(manifest.Generation)
		if err != nil {
			return err
		}
		for _, file := range bundle.Manifest.Files {
			if file.Kind != "app_binding" {
				if strings.HasPrefix(file.Name, "app-entry-") {
					return ErrIntegrity
				}
				continue
			}
			b, err := DecodeAppBinding(bundle.Files[file.Name])
			if err != nil {
				return ErrIntegrity
			}
			want, err := AppBindingFilename(b.AppID())
			if err != nil || want != file.Name {
				return ErrIntegrity
			}
			if old, exists := all[b.AppID()]; exists {
				if old.Digest() != b.Digest() {
					return ErrIntegrity
				}
				continue
			}
			if len(all) == MaxAppBindings {
				return ErrAppBindingInventoryBound
			}
			all[b.AppID()] = b
		}
		return nil
	})
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	return sortedAppBindings(all), nil
}
