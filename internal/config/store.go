//go:build linux

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/internal/p1testhook"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	MaxManifestBytes = 1 << 20
	MaxBundleFiles   = 256
	MaxBundleBytes   = 64 << 20
	MaxActiveBytes   = 64 << 10
)

var (
	ErrReadUnavailable = errors.New("READ_UNAVAILABLE")
	ErrWriterBusy      = errors.New("STORE_WRITER_BUSY")
	ErrReadOnly        = errors.New("STORE_READ_ONLY")
	ErrClosed          = errors.New("STORE_CLOSED")
)

type BundleFile struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type BinaryReference struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// BundleManifest is an integrity envelope, not a runnable-config validation.
type BundleManifest struct {
	Version            int               `json:"version"`
	Generation         Generation        `json:"generation"`
	CompilerVersion    string            `json:"compiler_version"`
	CapabilitiesDigest string            `json:"capabilities_digest"`
	Binaries           []BinaryReference `json:"binaries"`
	Files              []BundleFile      `json:"files"`
}

type StoredBundle struct {
	Manifest       BundleManifest
	ManifestSHA256 string
	Files          map[string][]byte
}

type ActivePointer struct {
	Version        int         `json:"version"`
	Revision       Revision    `json:"revision"`
	Generation     *Generation `json:"generation"`
	ManifestSHA256 string      `json:"manifest_sha256"`
	OperationID    *ID         `json:"operation_id"`
	DesiredRuntime string      `json:"desired_runtime"`
}

// Store serializes its own calls and holds a persistent Linux flock for writers.
// It never runs, adopts or stops a core and never recovers an operation journal.
type Store struct {
	mu          sync.Mutex
	root        *os.Root
	lock        *os.File
	closed      bool
	poisoned    bool
	fault       func(string) error
	runtimePath string
}

// OpenStore creates state only when writable is explicitly true. Read-only
// opens do not create or lock anything. A nonempty state missing active.json is
// unavailable: it must be reconciled, not replaced with an initial default.
func OpenStore(paths Paths, writable bool) (_ *Store, err error) {
	if err := paths.Validate(); err != nil {
		return nil, err
	}
	if writable {
		for _, path := range []string{paths.Config, paths.Cache, paths.Runtime} {
			r, err := privateRoot(path, true)
			if err != nil {
				return nil, err
			}
			if err = r.Close(); err != nil {
				return nil, err
			}
		}
	}
	r, err := privateRoot(paths.State, writable)
	if err != nil {
		return nil, errors.Join(ErrReadUnavailable, err)
	}
	s := &Store{root: r, runtimePath: paths.Runtime}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	if writable {
		s.lock, err = lockWriter(r)
		if err != nil {
			return nil, err
		}
		_, activeErr := r.Lstat("active.json")
		initialize := errors.Is(activeErr, os.ErrNotExist)
		if activeErr != nil && !initialize {
			return nil, activeErr
		}
		if initialize {
			// Only a pristine state may bootstrap. Existing durable directories,
			// even empty ones, can be remnants of a previous interrupted writer.
			// Never convert a lost pointer into a fresh stopped authority.
			runtimeTop := ""
			if rel, e := filepath.Rel(paths.State, paths.Runtime); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				runtimeTop = strings.Split(rel, string(os.PathSeparator))[0]
			}
			f, e := r.Open(".")
			if e != nil {
				return nil, e
			}
			entries, e := f.ReadDir(3)
			e = errors.Join(e, f.Close())
			if e != nil {
				return nil, e
			}
			for _, entry := range entries {
				if entry.Name() != ".writer.lock" && entry.Name() != runtimeTop {
					return nil, fmt.Errorf("%w: missing active pointer in existing state", ErrReadUnavailable)
				}
			}
		}
		for _, name := range []string{"bundles", "operations", "runs", "credentials"} {
			// An existing authority may have lost history or ownership records.
			// Recreating an empty directory would silently forget that state.
			dir, e := openPrivateDir(r, name, initialize)
			if e != nil {
				return nil, errors.Join(ErrReadUnavailable, e)
			}
			// A previous writer may have published a name and failed its parent
			// sync. Reopening in the same boot is not a durability barrier. An
			// explicit writer must sync these directories before reusing entries.
			if e = errors.Join(s.syncDir(dir, "open."+name+".sync"), dir.Close()); e != nil {
				return nil, e
			}
		}
		if err = s.syncRunDirectories(); err != nil {
			return nil, err
		}
		if initialize {
			token, e := randomToken()
			if e != nil {
				return nil, e
			}
			initial := ActivePointer{Version: 1, Revision: Revision(token), DesiredRuntime: "stopped"}
			data, _ := encode(initial)
			if e = s.replaceActive(data); e != nil {
				return nil, e
			}
		}
	}
	if _, err = s.readActive(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Do not remove the lock inode: waiters/openers must address the same file.
	err := s.root.Close()
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
	}
	return err
}

func (s *Store) writable() error {
	if s.closed {
		return ErrClosed
	}
	if s.lock == nil {
		return ErrReadOnly
	}
	if s.poisoned {
		return ErrUncertain
	}
	return nil
}

func digest(data []byte) string {
	v := sha256.Sum256(data)
	return hex.EncodeToString(v[:])
}

func isDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}

func encode(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.Join(ErrIntegrity, err)
	}
	// The on-disk format has a single serialization; also rejects duplicates,
	// trailing values, omitted fields and ambiguous spellings of authority.
	canonical, err := encode(out)
	if err != nil || !bytes.Equal(data, canonical) {
		return errors.Join(ErrIntegrity, err)
	}
	return nil
}

func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := openPrivateFile(root, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("%w: %s exceeds size bound", ErrIntegrity, name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("%w: %s changed size", ErrIntegrity, name)
	}
	return data, nil
}

func validateManifest(m BundleManifest) error {
	_, err := ParseID(string(m.Generation))
	if err != nil || m.Version != 1 || m.CompilerVersion == "" || len(m.CompilerVersion) > 128 || !isDigest(m.CapabilitiesDigest) || len(m.Files) == 0 || len(m.Files) > MaxBundleFiles || len(m.Binaries) == 0 || len(m.Binaries) > 16 {
		return fmt.Errorf("%w: invalid manifest header", ErrIntegrity)
	}
	for _, b := range m.Binaries {
		if !filepath.IsAbs(b.Path) || len(b.Path) > 4096 || b.Version == "" || len(b.Version) > 128 || !isDigest(b.SHA256) {
			return fmt.Errorf("%w: invalid binary reference", ErrIntegrity)
		}
	}
	kinds := map[string]bool{"intent": true, "provider": true, "rule": true, "generated_config": true, "route_map": true, "validation": true, "app_binding": true}
	names := map[string]bool{}
	total := int64(0)
	for _, f := range m.Files {
		_, err := ParseID(f.Name)
		if err != nil || f.Name == "manifest.json" || !kinds[f.Kind] || names[f.Name] || f.Bytes < 0 || f.Bytes > MaxBundleBytes-total || !isDigest(f.SHA256) {
			return fmt.Errorf("%w: invalid manifest file %q", ErrIntegrity, f.Name)
		}
		names[f.Name] = true
		total += f.Bytes
	}
	return nil
}

func generationDirectory(g Generation) (string, error) {
	if _, err := ParseID(string(g)); err != nil {
		return "", errors.Join(ErrIntegrity, err)
	}
	return digest([]byte(g)), nil
}

func (s *Store) ReadBundle(g Generation) (StoredBundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return StoredBundle{}, ErrClosed
	}
	return s.readBundle(g)
}

func (s *Store) readBundle(g Generation) (StoredBundle, error) {
	var out StoredBundle
	name, err := generationDirectory(g)
	if err != nil {
		return out, err
	}
	bundles, err := openPrivateDir(s.root, "bundles", false)
	if err != nil {
		return out, err
	}
	defer bundles.Close()
	dir, err := openPrivateDir(bundles, name, false)
	if err != nil {
		return out, err
	}
	defer dir.Close()
	data, err := readBounded(dir, "manifest.json", MaxManifestBytes)
	if err != nil {
		return out, err
	}
	if err = decode(data, &out.Manifest); err != nil {
		return out, err
	}
	if err = validateManifest(out.Manifest); err != nil {
		return out, err
	}
	if out.Manifest.Generation != g {
		return out, ErrIntegrity
	}
	f, err := dir.Open(".")
	if err != nil {
		return out, err
	}
	entries, err := f.ReadDir(MaxBundleFiles + 2)
	err = errors.Join(err, f.Close())
	if err != nil || len(entries) != len(out.Manifest.Files)+1 {
		return out, errors.Join(ErrIntegrity, err)
	}
	out.ManifestSHA256 = digest(data)
	out.Files = make(map[string][]byte, len(out.Manifest.Files))
	for _, record := range out.Manifest.Files {
		b, err := readBounded(dir, record.Name, record.Bytes)
		if err != nil {
			return StoredBundle{}, err
		}
		if int64(len(b)) != record.Bytes || digest(b) != record.SHA256 {
			return StoredBundle{}, fmt.Errorf("%w: bundle file %s", ErrIntegrity, record.Name)
		}
		out.Files[record.Name] = b
	}
	return out, nil
}

func (s *Store) PublishBundle(m BundleManifest, files map[string][]byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	if err := validateManifest(m); err != nil {
		return "", err
	}
	if len(files) != len(m.Files) {
		return "", ErrIntegrity
	}
	// Canonical order is part of this format, independently of Plan/JCS.
	m.Files = append([]BundleFile(nil), m.Files...)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
	for _, f := range m.Files {
		data, ok := files[f.Name]
		if !ok || int64(len(data)) != f.Bytes || digest(data) != f.SHA256 {
			return "", fmt.Errorf("%w: bundle data mismatch", ErrIntegrity)
		}
	}
	data, err := encode(m)
	if err != nil || len(data) > MaxManifestBytes {
		return "", errors.Join(ErrIntegrity, err)
	}
	sha := digest(data)
	bundles, err := openPrivateDir(s.root, "bundles", false)
	if err != nil {
		return "", err
	}
	defer bundles.Close()
	name, _ := generationDirectory(m.Generation)
	if _, err = bundles.Lstat(name); err == nil {
		old, err := s.readBundle(m.Generation)
		if err != nil || old.ManifestSHA256 != sha {
			return "", errors.Join(ErrIntegrity, err)
		}
		return sha, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	tmp := ".bundle-" + token
	if err = bundles.Mkdir(tmp, 0700); err != nil {
		return "", err
	}
	defer bundles.RemoveAll(tmp) // no prior attempt's temporary state is removed
	dir, err := openPrivateDir(bundles, tmp, false)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	for _, f := range m.Files {
		if err = s.writeNew(dir, f.Name, files[f.Name], "bundle.file:"+f.Name); err != nil {
			return "", err
		}
	}
	if err = s.writeNew(dir, "manifest.json", data, "bundle.manifest"); err != nil {
		return "", err
	}
	if err = s.syncDir(dir, "bundle.temp-sync"); err != nil {
		return "", err
	}
	if err = s.checkpoint("bundle.rename"); err != nil {
		return "", err
	}
	// All cooperating writers hold the persistent lock; never replace a known
	// generation. Same-uid mutation bypassing this store is outside isolation.
	if _, err = bundles.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return "", errors.Join(ErrIntegrity, err)
	}
	if err = bundles.Rename(tmp, name); err != nil {
		return "", s.uncertain(err)
	}
	if err = s.syncDir(bundles, "bundle.parent-sync"); err != nil {
		return "", s.uncertain(err)
	}
	return sha, nil
}

func validateActive(p ActivePointer) error {
	_, err := ParseID(string(p.Revision))
	if err != nil || p.Version != 1 || p.DesiredRuntime != "running" && p.DesiredRuntime != "stopped" {
		return ErrIntegrity
	}
	if p.OperationID != nil {
		if _, err = ParseID(string(*p.OperationID)); err != nil {
			return ErrIntegrity
		}
	}
	if p.Generation == nil {
		if p.ManifestSHA256 != "" || p.DesiredRuntime != "stopped" {
			return ErrIntegrity
		}
	} else if _, err = generationDirectory(*p.Generation); err != nil || !isDigest(p.ManifestSHA256) || p.OperationID == nil {
		return ErrIntegrity
	}
	return nil
}

func (s *Store) validateReference(p ActivePointer) error {
	if err := validateActive(p); err != nil {
		return err
	}
	if p.Generation != nil {
		bundle, err := s.readBundle(*p.Generation)
		if err != nil || bundle.ManifestSHA256 != p.ManifestSHA256 {
			return errors.Join(ErrIntegrity, err)
		}
	}
	return nil
}

func (s *Store) ReadActive() (ActivePointer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ActivePointer{}, ErrClosed
	}
	return s.readActive()
}

func (s *Store) readActive() (ActivePointer, error) {
	var p ActivePointer
	data, err := readBounded(s.root, "active.json", MaxActiveBytes)
	if err == nil {
		err = decode(data, &p)
	}
	if err == nil {
		err = s.validateReference(p)
	}
	if err != nil {
		return ActivePointer{}, errors.Join(ErrReadUnavailable, err)
	}
	return p, nil
}

func (s *Store) PublishActive(p ActivePointer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	if err := s.validateReference(p); err != nil {
		return err
	}
	// Reject unsafe or unreadable authority rather than replacing it as repair.
	if _, err := s.readActive(); err != nil {
		return err
	}
	data, err := encode(p)
	if err != nil || len(data) > MaxActiveBytes {
		return errors.Join(ErrIntegrity, err)
	}
	if err := s.replaceActive(data); err != nil {
		return err
	}
	if p1testhook.Enabled {
		op := ""
		if p.OperationID != nil {
			op = string(*p.OperationID)
		}
		_ = p1testhook.Record("store", "active-published", op, "", "", false, map[string]any{"active": p, "sha256": digest(data)})
	}
	return nil
}
