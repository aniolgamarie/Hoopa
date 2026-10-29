package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func restoreFixture(t *testing.T, raw []byte, options ParseOptions) (Snapshot, []byte) {
	t.Helper()
	s := mustProvider(t, raw, options)
	asset, err := s.SensitiveAssetBytes()
	if err != nil {
		t.Fatal(err)
	}
	return s, asset
}
func requireRestoreInvalid(t *testing.T, raw []byte, options ParseOptions) {
	t.Helper()
	s, err := RestoreAsset(raw, options)
	if err != ErrInvalidProvider || !reflect.DeepEqual(s, Snapshot{}) {
		t.Fatal("restore must return fixed error and zero snapshot")
	}
}
func restoreField(t *testing.T, asset []byte, field string, value any) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if json.Unmarshal(asset, &fields) != nil {
		t.Fatal("bad fixture")
	}
	previous, ok := fields[field]
	if !ok {
		t.Fatal("missing fixture field")
	}
	next, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Replace(asset, append([]byte(`"`+field+`":`), previous...), append([]byte(`"`+field+`":`), next...), 1)
}
func TestRestoreRoundTripFiniteProtocolsAndSyntax(t *testing.T) {
	for _, kind := range profileKinds {
		t.Run(kind, func(t *testing.T) {
			options := providerOptions()
			options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}, {Prefix: "unmatched", Region: "JP"}}
			original, asset := restoreFixture(t, providerRaw(t, profileNode(kind)), options)
			restored, err := RestoreAsset(asset, options)
			if err != nil || restored.Validate() != nil || restored.ID != original.ID || !reflect.DeepEqual(restored.Nodes, original.Nodes) {
				t.Fatal("restore lost authority")
			}
			got, _ := restored.SensitiveAssetBytes()
			configs, _ := restored.SensitiveProxyConfigs()
			wantConfigs, _ := original.SensitiveProxyConfigs()
			if !bytes.Equal(got, asset) || !reflect.DeepEqual(configs, wantConfigs) {
				t.Fatal("restore changed private material")
			}
		})
	}
	for _, raw := range []string{
		`{"proxies":[]}`,
		"proxies: []\n",
		"# preserve original syntax\nproxies:\n  - name: ordinary\n    type: socks5\n    server: &host example.invalid\n    port: 443\n  - name: second\n    type: http\n    server: *host\n    port: 8080\n",
	} {
		_, asset := restoreFixture(t, []byte(raw), providerOptions())
		restored, err := RestoreAsset(asset, providerOptions())
		got, e := restored.SensitiveBytes()
		if err != nil || e != nil || string(got) != raw {
			t.Fatal("source syntax not preserved")
		}
	}
	// Parse owns the canonical [] rule representation even when options are nil.
	_, asset := restoreFixture(t, []byte(`{"proxies":[]}`), providerOptions())
	options := providerOptions()
	options.RegionRules = []RegionRule{}
	if _, err := RestoreAsset(asset, options); err != nil {
		t.Fatal("empty rules differ from nil options")
	}
}
func TestRestoreRejectsStoredMetadataAndTrustedInputChanges(t *testing.T) {
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}, {Prefix: "unmatched", Region: "JP"}}
	original, asset := restoreFixture(t, providerRaw(t, profileNode("trojan")), options)
	for field, value := range map[string]any{
		"schema_version": 2, "parser_version": "hoopa.provider.v99", "snapshot_id": "s-forged", "source_id": "other-source", "key_id": "forged",
		"original_bytes": providerRaw(t, profileNode("http")),
		"region_rules":   []RegionRule{{Prefix: "ordinary", Region: "US"}, {Prefix: "unmatched", Region: "CA"}},
		"nodes":          []Node{},
	} {
		t.Run(field, func(t *testing.T) { requireRestoreInvalid(t, restoreField(t, asset, field, value), options) })
	}
	for _, pair := range [][2]string{{`"display_name":"ordinary"`, `"display_name":"forged"`}, {`"code":"US"`, `"code":"JP"`}, {`"kind":"declared-prefix"`, `"kind":"forged"`}, {`"rule":"ordinary"`, `"rule":"forged"`}, {`"id":"` + string(original.Nodes[0].ID) + `"`, `"id":"n-forged"`}} {
		mutated := bytes.Replace(asset, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(mutated, asset) {
			t.Fatal("mutation did not affect fixture")
		}
		requireRestoreInvalid(t, mutated, options)
	}
	for _, change := range []func(*ParseOptions){
		func(o *ParseOptions) { o.SourceID = "other-source" },
		func(o *ParseOptions) { o.SourceID = "invalid source" },
		func(o *ParseOptions) { o.IdentityKey = nil },
		func(o *ParseOptions) { o.IdentityKey = make([]byte, 31) },
		func(o *ParseOptions) { o.IdentityKey = bytes.Repeat([]byte{99}, 32) },
		func(o *ParseOptions) { o.RegionRules = nil },
		func(o *ParseOptions) {
			o.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}, {Prefix: "unmatched", Region: "CA"}}
		},
		func(o *ParseOptions) { o.Limits.MaxBytes = 1 },
	} {
		changed := options
		change(&changed)
		requireRestoreInvalid(t, asset, changed)
	}
}
func TestRestoreRejectsNonexactJSON(t *testing.T) {
	options := providerOptions()
	_, asset := restoreFixture(t, providerRaw(t, profileNode("socks5")), options)
	var fields map[string]json.RawMessage
	if json.Unmarshal(asset, &fields) != nil {
		t.Fatal("bad fixture")
	}
	cases := map[string][]byte{
		"empty": nil, "null": []byte(`null`), "array": []byte(`[]`), "truncated": asset[:len(asset)-1],
		"leading_space": append([]byte(" "), asset...), "trailing_newline": append(append([]byte{}, asset...), '\n'),
		"trailing_value":     append(append([]byte{}, asset...), []byte(`{}`)...),
		"unknown":            append([]byte(`{"unknown":"private-secret",`), asset[1:]...),
		"duplicate_schema":   append([]byte(`{"schema_version":1,`), asset[1:]...),
		"duplicate_original": append(append([]byte(`{"original_bytes":`), append(fields["original_bytes"], ',')...), asset[1:]...),
		"escape":             bytes.Replace(asset, []byte(`"hoopa.provider.v1"`), []byte(`"hoopa.provider.v\u0031"`), 1),
		"casefold":           bytes.Replace(asset, []byte(`"original_bytes"`), []byte(`"ORIGINAL_BYTES"`), 1),
		"float_schema":       bytes.Replace(asset, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1),
		"surrogate":          bytes.Replace(asset, []byte(`"display_name":"ordinary"`), []byte(`"display_name":"\ud800"`), 1),
		"bad_utf8":           bytes.Replace(asset, []byte(`"display_name":"ordinary"`), []byte{'"', 'd', 'i', 's', 'p', 'l', 'a', 'y', '_', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"'}, 1),
		"bad_base64":         restoreField(t, asset, "original_bytes", "not base64!"),
		"null_original":      restoreField(t, asset, "original_bytes", nil),
	}
	for key := range fields {
		cases["null_"+key] = restoreField(t, asset, key, nil)
		prefix := []byte(`"` + key + `":`)
		item := append(append([]byte{}, prefix...), fields[key]...)
		missing := bytes.Replace(asset, append(item, ','), nil, 1)
		if bytes.Equal(missing, asset) {
			missing = bytes.Replace(asset, append([]byte{','}, item...), nil, 1)
		}
		cases["missing_"+key] = missing
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) { requireRestoreInvalid(t, raw, options) })
	}
}
func TestRestoreInputLimitAndErrorSecrecy(t *testing.T) {
	large := make([]byte, (64<<20)+1)
	// At the envelope limit malformed JSON is invalid, above it adds only LIMIT.
	s, err := RestoreAsset(large[:len(large)-1], providerOptions())
	if err != ErrInvalidProvider || !reflect.DeepEqual(s, Snapshot{}) {
		t.Fatal("exact limit classification")
	}
	s, err = RestoreAsset(large, providerOptions())
	if !errors.Is(err, ErrInvalidProvider) || !errors.Is(err, ErrInputLimit) || !reflect.DeepEqual(s, Snapshot{}) {
		t.Fatal("oversized asset accepted")
	}
	options := providerOptions()
	_, asset := restoreFixture(t, providerRaw(t, profileNode("trojan")), options)
	restored, err := RestoreAsset(asset, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{fmt.Sprint(restored), fmt.Sprintf("%#v", restored)} {
		if strings.Contains(value, "private-test-password") || strings.Contains(value, "example.invalid") {
			t.Fatal("formatted snapshot leaks credential material")
		}
	}
	requireRestoreInvalid(t, []byte(`{"original_bytes":"private-test-password"}`), options)
}
func TestRestoreDetachedPrivateAuthority(t *testing.T) {
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}}
	_, asset := restoreFixture(t, providerRaw(t, profileNode("trojan")), options)
	stable := append([]byte{}, asset...)
	restored, err := RestoreAsset(asset, options)
	if err != nil {
		t.Fatal(err)
	}
	asset[0] = '!'
	options.IdentityKey[0] ^= 255
	options.RegionRules[0].Region = "JP"
	configs, _ := restored.SensitiveProxyConfigs()
	configs[0]["password"] = "forged"
	original, _ := restored.SensitiveBytes()
	original[0] = '!'
	out, _ := restored.SensitiveAssetBytes()
	out[0] = '!'
	again, err := restored.SensitiveAssetBytes()
	if err != nil || !bytes.Equal(again, stable) {
		t.Fatal("caller mutated restored private authority")
	}
	second, err := RestoreAsset(stable, providerOptionsWithUS())
	if err != nil {
		t.Fatal(err)
	}
	restored.Nodes[0].DisplayName = "forged"
	if restored.Validate() == nil || second.Validate() != nil {
		t.Fatal("restores share mutable public metadata")
	}
}
func providerOptionsWithUS() ParseOptions {
	o := providerOptions()
	o.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}}
	return o
}
