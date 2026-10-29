package subscription

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSnapshotPrivateAssetBindsCompleteRegionPolicy(t *testing.T) {
	raw := providerRaw(t, profileNode("socks5"))
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "ordinary", Region: "US"}, {Prefix: "not-a-matching-prefix", Region: "JP"}}
	s := mustProvider(t, raw, options)
	asset, e := s.SensitiveAssetBytes()
	if e != nil {
		t.Fatal(e)
	}
	var decoded struct {
		SchemaVersion int          `json:"schema_version"`
		ParserVersion string       `json:"parser_version"`
		SnapshotID    string       `json:"snapshot_id"`
		SourceID      string       `json:"source_id"`
		KeyID         string       `json:"key_id"`
		Original      []byte       `json:"original_bytes"`
		Rules         []RegionRule `json:"region_rules"`
		Nodes         []Node       `json:"nodes"`
	}
	if json.Unmarshal(asset, &decoded) != nil || decoded.SchemaVersion != 1 || decoded.ParserVersion != "hoopa.provider.v1" || decoded.SnapshotID != string(s.ID) || decoded.SourceID != string(s.SourceID) || decoded.KeyID != s.KeyID || !bytes.Equal(decoded.Original, raw) || !reflect.DeepEqual(decoded.Rules, options.RegionRules) || !reflect.DeepEqual(decoded.Nodes, s.Nodes) {
		t.Fatal("asset omitted or changed source authority")
	}
	options.RegionRules[1].Region = "CA"
	changed := mustProvider(t, raw, options)
	next, e := changed.SensitiveAssetBytes()
	if e != nil || bytes.Equal(asset, next) || !reflect.DeepEqual(s.Nodes, changed.Nodes) {
		t.Fatal("nonmatching local policy must change asset without changing node evidence")
	}
	again, e := s.SensitiveAssetBytes()
	if e != nil || !bytes.Equal(asset, again) {
		t.Fatal("caller changed immutable policy")
	}
	asset[0] = '!'
	again, e = s.SensitiveAssetBytes()
	if e != nil || !json.Valid(again) {
		t.Fatal("returned bytes alias authority")
	}
}
func TestSnapshotPrivateAssetRejectsUnboundMetadata(t *testing.T) {
	s := mustProvider(t, providerRaw(t, profileNode("socks5")), providerOptions())
	s.Nodes[0].DisplayName = "forged"
	for _, candidate := range []Snapshot{s, {}} {
		raw, e := candidate.SensitiveAssetBytes()
		if raw != nil || e != ErrInvalidProvider {
			t.Fatal("unbound asset exposed")
		}
	}
}
