package mihomo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func TestApprovedAppRoutingArtifactAndExactBinarySelection(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "experiments", "mihomo-events", "approved-app-core-v2.json"))
	if e != nil {
		t.Fatal(e)
	}
	var artifact struct {
		Schema, Status string
		Binary         config.BinaryReference
		GOOS, GOARCH   string
		Protocol       int
		Mode           string
		Capabilities   []string
		References     []struct {
			Path   string
			Bytes  int64
			SHA256 string
		}
	}
	if json.Unmarshal(raw, &artifact) != nil || artifact.Schema != "hoopa-approved-app-core-v2" || artifact.Status != "accepted_bounded_app_routing_profile" {
		t.Fatal("artifact not the separate app qualification")
	}
	if sha256String(raw) != "06724bac18f5557552e983ee48cce9e3564f3fd222c4ad3e8fc0d018324146e5" {
		t.Fatal("approval artifact bytes drifted")
	}
	path := filepath.Join(t.TempDir(), "not-installed-core")
	c, e := ApprovedAppRoutingCapability(path)
	if e != nil || c.Validate() != nil || !c.Accepted || c.SchemaVersion != 1 || c.Binary.Path != path || c.Binary.Version != "1.10.0" || c.Binary.SHA256 != "6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b" || c.EvidenceSHA256 != sha256String(raw) || c.Binary.Version != artifact.Binary.Version || c.Binary.SHA256 != artifact.Binary.SHA256 || c.GOOS != artifact.GOOS || c.GOARCH != artifact.GOARCH || c.Protocol != artifact.Protocol || c.Protocol != 2 || c.Mode != artifact.Mode || c.Mode != "basic-app-routing" || !reflect.DeepEqual(c.Capabilities, artifact.Capabilities) {
		t.Fatal("factory does not bind exact approved artifact", e)
	}
	selected, e := ApprovedCapability(c.Binary)
	if e != nil || !reflect.DeepEqual(selected, c) {
		t.Fatal("complete app BinaryReference selection", e)
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("registry lookup touched installation")
	}
	for _, ref := range artifact.References {
		data, e := os.ReadFile(filepath.Join("..", "..", ref.Path))
		if e != nil || int64(len(data)) != ref.Bytes || sha256String(data) != ref.SHA256 {
			t.Fatal("approval reference integrity differs", ref.Path, e)
		}
	}
	for _, edit := range []func(*config.BinaryReference){func(b *config.BinaryReference) { b.Version = "latest" }, func(b *config.BinaryReference) { b.Version = "1.10.1" }, func(b *config.BinaryReference) { b.SHA256 = strings.ToUpper(b.SHA256) }, func(b *config.BinaryReference) { b.SHA256 = strings.Repeat("f", 64) }, func(b *config.BinaryReference) { b.Path = "relative" }, func(b *config.BinaryReference) { b.Path = "/tmp/../core" }, func(b *config.BinaryReference) { b.Path = "/tmp/nul\x00core" }} {
		bad := c.Binary
		edit(&bad)
		got, e := ApprovedCapability(bad)
		if e != ErrCapability || !reflect.DeepEqual(got, CapabilityRecord{}) {
			t.Fatal("inexact reference minted profile")
		}
	}
	// Any canonical local installation path can be bound, but changes its full
	// record digest. The factory never chooses a profile by a pathname hint.
	moved := c.Binary
	moved.Path = filepath.Join(t.TempDir(), "named-like-basic-or-source-core")
	again, e := ApprovedCapability(moved)
	if e != nil || again.Mode != c.Mode || again.Binary.Path != moved.Path {
		t.Fatal("canonical installation binding lost", e)
	}
	a, _ := c.Digest()
	b, _ := again.Digest()
	if a == b {
		t.Fatal("local path omitted from commitment")
	}
	c.Capabilities[0] = "caller-mutated"
	selected.Capabilities[1] = "caller-mutated"
	fresh, e := ApprovedAppRoutingCapability(path)
	if e != nil || !reflect.DeepEqual(fresh.Capabilities, artifact.Capabilities) {
		t.Fatal("registry returned shared mutable capabilities")
	}
}

func TestApprovedAppRoutingNeverPromotesLegacyFromClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-profile-claimed-path")
	for _, factory := range []func(string) (CapabilityRecord, error){ApprovedBasicCapability, ApprovedSourcesCapability} {
		old, e := factory(path)
		if e != nil {
			t.Fatal(e)
		}
		original := old
		original.Capabilities = append([]string{}, old.Capabilities...)
		old.Mode = "basic-app-routing"
		old.Protocol = 2
		old.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "ordered-app-rules-v1", "probe-qualified-groups-v1", "unknown-ip-reject-v1"}
		old.EvidenceSHA256 = "06724bac18f5557552e983ee48cce9e3564f3fd222c4ad3e8fc0d018324146e5"
		if old.Validate() != nil {
			t.Fatal("shape alone should not be approval lookup")
		}
		selected, e := ApprovedCapability(old.Binary)
		if e != nil || !reflect.DeepEqual(selected, original) {
			t.Fatal("self-claimed mode/capabilities upgraded old binary", e)
		}
	}
	source, e := ApprovedSourcesCapability(path)
	if e != nil || source.Binary.SHA256 != "c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6" || source.EvidenceSHA256 != "e61272a2d656e08249ac0067ea83d1489f4e98a01fee7f88947fd63f039f2bbc" {
		t.Fatal("old source approval changed")
	}
	basic, e := ApprovedBasicCapability(path)
	if e != nil || basic.Binary.SHA256 != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" || basic.EvidenceSHA256 != "50b3ba6bbd309e3273784695001c15bfaf2d4c1d5b8709421357fae249f7df77" {
		t.Fatal("basic approval changed")
	}
	for _, badPath := range []string{"", "relative", "./core", "/tmp/../core", "/tmp/core/", "/tmp/nul\x00core", "/" + strings.Repeat("a", 4096)} {
		c, e := ApprovedAppRoutingCapability(badPath)
		if e != ErrCapability || !reflect.DeepEqual(c, CapabilityRecord{}) {
			t.Fatal("bad local path admitted")
		}
	}
}
