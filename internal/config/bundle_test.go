//go:build linux

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func assembleHash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func assembleBinary() BinaryReference {
	return BinaryReference{Path: "/synthetic/core", Version: "fixture", SHA256: strings.Repeat("a", 64)}
}
func assemble(t *testing.T, binaries []BinaryReference, assets []BundleAsset) (BundleManifest, map[string][]byte) {
	t.Helper()
	m, files, e := AssembleBundle("generation:unit", "compiler-unit", strings.Repeat("b", 64), binaries, assets)
	if e != nil {
		t.Fatal("valid pure assembly rejected", e)
	}
	return m, files
}
func assembleReject(t *testing.T, generation Generation, compiler, cap string, binaries []BinaryReference, assets []BundleAsset) {
	t.Helper()
	m, files, e := AssembleBundle(generation, compiler, cap, binaries, assets)
	if !errors.Is(e, ErrIntegrity) || e.Error() != ErrIntegrity.Error() || !reflect.DeepEqual(m, BundleManifest{}) || files != nil {
		t.Fatal("assembly failure not constant/zero", e)
	}
}
func TestAssembleActualHashesDetachmentAndDeterminism(t *testing.T) {
	binaries := []BinaryReference{assembleBinary()}
	assets := []BundleAsset{{Kind: "validation", Name: "z.json", Data: []byte("last")}, {Kind: "intent", Name: "a.json", Data: []byte("first")}}
	m, files := assemble(t, binaries, assets)
	if m.Version != 1 || m.Generation != "generation:unit" || m.CompilerVersion != "compiler-unit" || m.CapabilitiesDigest != strings.Repeat("b", 64) || len(m.Files) != 2 || m.Files[0].Name != "a.json" || m.Files[1].Name != "z.json" {
		t.Fatal("manifest metadata/sort mismatch")
	}
	for _, file := range m.Files {
		if file.Bytes != int64(len(files[file.Name])) || file.SHA256 != assembleHash(files[file.Name]) {
			t.Fatal("manifest does not bind actual bytes")
		}
	}
	reverse := []BundleAsset{assets[1], assets[0]}
	other, otherFiles := assemble(t, binaries, reverse)
	if !reflect.DeepEqual(m, other) || !reflect.DeepEqual(files, otherFiles) {
		t.Fatal("asset iteration order changed deterministic result")
	}
	binaries[0].SHA256 = "input-mutated"
	assets[0].Data[0] = '!'
	if m.Binaries[0].SHA256 != strings.Repeat("a", 64) || string(files["z.json"]) != "last" {
		t.Fatal("input slices alias returned bundle")
	}
	files["a.json"][0] = '!'
	m.Binaries[0].SHA256 = "output-mutated"
	m.Files[0].SHA256 = "output-mutated"
	if string(assets[1].Data) != "first" || binaries[0].SHA256 != "input-mutated" || other.Binaries[0].SHA256 != strings.Repeat("a", 64) || string(otherFiles["a.json"]) != "first" {
		t.Fatal("separate assembly results share mutable objects")
	}
}
func TestAssembleMalformedMetadataAndReservedNames(t *testing.T) {
	cap := strings.Repeat("b", 64)
	binary := []BinaryReference{assembleBinary()}
	asset := []BundleAsset{{Kind: "intent", Name: "intent.json", Data: []byte("{}")}}
	for _, name := range []string{"manifest.json", "../outside", "a/b.json", ".hidden", "bad name", string([]byte{'a', 0, 'b'})} {
		a := append([]BundleAsset{}, asset...)
		a[0].Name = name
		assembleReject(t, "generation:unit", "compiler-unit", cap, binary, a)
	}
	for _, kind := range []string{"unknown", "credential", ""} {
		a := append([]BundleAsset{}, asset...)
		a[0].Kind = kind
		assembleReject(t, "generation:unit", "compiler-unit", cap, binary, a)
	}
	assembleReject(t, "generation:unit", "compiler-unit", cap, binary, append(asset, asset[0]))
	for _, generation := range []Generation{"", "bad generation"} {
		assembleReject(t, generation, "compiler-unit", cap, binary, asset)
	}
	for _, compiler := range []string{"", strings.Repeat("x", 129)} {
		assembleReject(t, "generation:unit", compiler, cap, binary, asset)
	}
	for _, digest := range []string{"", strings.Repeat("B", 64), strings.Repeat("b", 63)} {
		assembleReject(t, "generation:unit", "compiler-unit", digest, binary, asset)
	}
	for _, mutate := range []func(*BinaryReference){func(b *BinaryReference) { b.Path = "relative" }, func(b *BinaryReference) { b.Version = "" }, func(b *BinaryReference) { b.SHA256 = "invalid" }} {
		b := assembleBinary()
		mutate(&b)
		assembleReject(t, "generation:unit", "compiler-unit", cap, []BinaryReference{b}, asset)
	}
	assembleReject(t, "generation:unit", "compiler-unit", cap, nil, asset)
	assembleReject(t, "generation:unit", "compiler-unit", cap, binary, nil)
}
func TestAssembleFiniteFileBinaryAndAggregateBounds(t *testing.T) {
	binaries := make([]BinaryReference, 16)
	for i := range binaries {
		binaries[i] = assembleBinary()
		binaries[i].Path = fmt.Sprintf("/synthetic/core-%d", i)
	}
	assets := make([]BundleAsset, MaxBundleFiles)
	for i := range assets {
		assets[i] = BundleAsset{Kind: "provider", Name: fmt.Sprintf("asset-%03d.json", i), Data: []byte{byte(i)}}
	}
	m, files := assemble(t, binaries, assets)
	if len(m.Binaries) != 16 || len(m.Files) != MaxBundleFiles || len(files) != MaxBundleFiles {
		t.Fatal("exact collection bounds changed")
	}
	assembleReject(t, "generation:unit", "compiler-unit", strings.Repeat("b", 64), append(binaries, assembleBinary()), assets)
	assembleReject(t, "generation:unit", "compiler-unit", strings.Repeat("b", 64), binaries, append(assets, BundleAsset{Kind: "provider", Name: "over.json", Data: []byte{0}}))
	// One bounded backing array exercises64MiB exact and+1 aggregate bytes; the
	// helper must reject the latter without returning any cloned partial files.
	data := make([]byte, MaxBundleBytes+1)
	exact := []BundleAsset{{Kind: "provider", Name: "first.json", Data: data[:MaxBundleBytes/2]}, {Kind: "provider", Name: "second.json", Data: data[MaxBundleBytes/2 : MaxBundleBytes]}}
	m, files = assemble(t, []BinaryReference{assembleBinary()}, exact)
	var total int64
	for _, f := range m.Files {
		total += f.Bytes
		if f.SHA256 != assembleHash(files[f.Name]) {
			t.Fatal("large actual byte hash mismatch")
		}
	}
	if total != MaxBundleBytes {
		t.Fatal("exact aggregate bound changed")
	}
	data[0] = 1
	if files["first.json"][0] != 0 {
		t.Fatal("large result aliases input")
	}
	files = nil
	over := []BundleAsset{{Kind: "provider", Name: "over.json", Data: data}}
	assembleReject(t, "generation:unit", "compiler-unit", strings.Repeat("b", 64), []BinaryReference{assembleBinary()}, over)
}
func TestAssembleEveryPermittedAssetKindAndOpaqueBytes(t *testing.T) {
	assets := []BundleAsset{}
	for _, kind := range []string{"intent", "provider", "rule", "generated_config", "route_map", "validation"} {
		assets = append(assets, BundleAsset{Kind: kind, Name: kind + ".json", Data: []byte{0xff, 0, 1}})
	}
	_, files := assemble(t, []BinaryReference{assembleBinary()}, assets)
	for _, a := range assets {
		if !bytes.Equal(files[a.Name], a.Data) {
			t.Fatal("assembly rewrote opaque asset bytes")
		}
	}
	// Assembly is an integrity envelope, not a parser: zero bytes are hashable;
	// compiler/profile semantics decide whether an actual asset is meaningful.
	m, files := assemble(t, []BinaryReference{assembleBinary()}, []BundleAsset{{Kind: "validation", Name: "empty.json", Data: nil}})
	if m.Files[0].Bytes != 0 || m.Files[0].SHA256 != assembleHash(nil) || len(files["empty.json"]) != 0 {
		t.Fatal("empty opaque byte envelope changed")
	}
}
