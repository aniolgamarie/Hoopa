package mihomo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func TestApprovedSourcesArtifactAndExactBinarySelection(t *testing.T) {
	raw, e := os.ReadFile(filepath.Join("..", "..", "experiments", "mihomo-events", "approved-sources-core-v2.json"))
	if e != nil {
		t.Fatal(e)
	}
	var a struct {
		Schema       string                 `json:"schema"`
		Status       string                 `json:"status"`
		Binary       config.BinaryReference `json:"binary"`
		Protocol     int                    `json:"protocol"`
		Mode         string                 `json:"mode"`
		Capabilities []string               `json:"capabilities"`
	}
	if json.Unmarshal(raw, &a) != nil || a.Schema != "hoopa-approved-sources-core-v2" || a.Status != "accepted_bounded_sources_profile" {
		t.Fatal("wrong evidence artifact")
	}
	path := filepath.Join(t.TempDir(), "not-installed")
	c, e := ApprovedSourcesCapability(path)
	if e != nil || c.EvidenceSHA256 != sha256String(raw) || c.Binary.SHA256 != a.Binary.SHA256 || c.Binary.Version != a.Binary.Version || c.Protocol != a.Protocol || c.Mode != a.Mode || !reflect.DeepEqual(c.Capabilities, a.Capabilities) {
		t.Fatal("evidence not bound", e)
	}
	selected, e := ApprovedCapability(c.Binary)
	if e != nil || !reflect.DeepEqual(c, selected) {
		t.Fatal("exact source binary selection", e)
	}
	old, e := ApprovedBasicCapability(path)
	if e != nil {
		t.Fatal(e)
	}
	selected, e = ApprovedCapability(old.Binary)
	if e != nil || !reflect.DeepEqual(old, selected) || selected.Protocol != 1 {
		t.Fatal("old profile changed", e)
	}
	for _, change := range []func(*config.BinaryReference){func(b *config.BinaryReference) { b.Version = "latest" }, func(b *config.BinaryReference) { b.SHA256 = strings.Repeat("f", 64) }, func(b *config.BinaryReference) {
		b.SHA256 = "1edcbb3646ec89c99d45fac5dfe47d053695b51b71c6af8b76f381ef1704ff77"
	}, func(b *config.BinaryReference) { b.Path = "relative" }, func(b *config.BinaryReference) { b.Path = "/tmp/../core" }} {
		b := c.Binary
		change(&b)
		got, e := ApprovedCapability(b)
		if e != ErrCapability || !reflect.DeepEqual(got, CapabilityRecord{}) {
			t.Fatal("inexact identity granted capability")
		}
	}
	c.Capabilities[0] = "forged"
	again, e := ApprovedSourcesCapability(path)
	if e != nil || again.Capabilities[0] == "forged" {
		t.Fatal("shared mutable capability")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("pure lookup touched installation")
	}
}
