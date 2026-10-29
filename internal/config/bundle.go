//go:build linux

package config

import (
	"bytes"
	"sort"
)

// BundleInput binds one complete private source asset or an explicitly supplied
// binary/capability. It has the same envelope as control.PlanInput without an
// upward package dependency. It is evidence, never launch authorization.
type BundleInput struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}
type BundleAsset struct {
	Kind, Name string
	Data       []byte
}

// AssembleBundle is pure. It checks aggregate bounds before cloning any input,
// computes hashes from actual bytes and returns no partial material on failure.
// Source semantics and runnable-profile validation belong to the compiler.
func AssembleBundle(generation Generation, compiler, capabilities string, binaries []BinaryReference, assets []BundleAsset) (BundleManifest, map[string][]byte, error) {
	if len(binaries) < 1 || len(binaries) > 16 {
		return BundleManifest{}, nil, ErrIntegrity
	}
	m := BundleManifest{Version: 1, Generation: generation, CompilerVersion: compiler, CapabilitiesDigest: capabilities, Binaries: append([]BinaryReference(nil), binaries...), Files: []BundleFile{}}
	if len(assets) < 1 || len(assets) > MaxBundleFiles {
		return BundleManifest{}, nil, ErrIntegrity
	}
	total := int64(0)
	for _, a := range assets {
		if int64(len(a.Data)) > MaxBundleBytes-total {
			return BundleManifest{}, nil, ErrIntegrity
		}
		total += int64(len(a.Data))
		m.Files = append(m.Files, BundleFile{Kind: a.Kind, Name: a.Name, Bytes: int64(len(a.Data)), SHA256: digest(a.Data)})
	}
	if validateManifest(m) != nil {
		return BundleManifest{}, nil, ErrIntegrity
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })
	raw, err := encode(m)
	if err != nil || len(raw) > MaxManifestBytes {
		return BundleManifest{}, nil, ErrIntegrity
	}
	files := make(map[string][]byte, len(assets))
	for _, a := range assets {
		files[a.Name] = bytes.Clone(a.Data)
	}
	return m, files, nil
}
