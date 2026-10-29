//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/subscription"
)

func TestSourceCompileIdentityBindsLocalSettingsAndCanonicalInputs(t *testing.T) {
	capability, err := mihomo.ApprovedSourcesCapability("/uninstalled/source-identity-fixture")
	if err != nil {
		t.Fatal(err)
	}
	intent := updateRequestIntent(t)
	intent["core"] = map[string]any{"mihomo": capability.Binary}
	intent["subscriptions"] = []any{}
	intent["selection"] = map[string]any{"active_subscription": nil}
	intent["default_target"] = map[string]any{"kind": "reject"}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	settings := initializeSettings(t)
	compiled, err := compileSourceIdentity(raw, settings, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, files := compiled.Bundle()
	pretty, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	again, err := compileSourceIdentity(pretty, settings, map[config.ID]subscription.Snapshot{"unused": {}})
	if err != nil {
		t.Fatal("unused source is not a referenced compilation input", err)
	}
	m2, files2 := again.Bundle()
	if !reflect.DeepEqual(m, m2) || !reflect.DeepEqual(files, files2) {
		t.Fatal("representation or unused input changed generation")
	}
	key, runtime := codecSettingsInputs()
	for _, change := range []func(){func() { runtime.Controller = "127.0.0.1:39001" }, func() { runtime.Entry.Port++ }, func() { runtime.Secret = "changed-private-secret-0123456789abcdef" }, func() { key = bytes.Repeat([]byte{'z'}, 32) }} {
		change()
		next, err := NewSourceSettings(key, runtime)
		if err != nil {
			t.Fatal(err)
		}
		c, err := compileSourceIdentity(raw, next, nil)
		if err != nil {
			t.Fatal(err)
		}
		manifest, _ := c.Bundle()
		if manifest.Generation == m.Generation {
			t.Fatal("private settings change retained approved generation")
		}
		m = manifest
	}
	if _, err = compileSourceIdentity(raw, SourceSettings{}, nil); err != ErrSourceSettings {
		t.Fatal("zero settings accepted", err)
	}
}

func TestSourceCompileIdentityBindsExactReferencedProviderAsset(t *testing.T) {
	capability, err := mihomo.ApprovedSourcesCapability("/uninstalled/source-identity-fixture")
	if err != nil {
		t.Fatal(err)
	}
	settings := initializeSettings(t)
	key, _ := settings.SensitiveIdentityKey()
	var prior config.Generation
	for i, rawProvider := range []string{updaterProvider, " " + updaterProvider} {
		snapshot, err := subscription.Parse([]byte(rawProvider), subscription.ParseOptions{SourceID: "source-a", IdentityKey: key})
		if err != nil {
			t.Fatal(err)
		}
		intent := updateRequestIntent(t)
		intent["core"] = map[string]any{"mihomo": capability.Binary}
		intent["selection"] = map[string]any{"active_subscription": "source-a"}
		intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "source-a", "node_id": snapshot.Nodes[0].ID}
		raw, _ := json.Marshal(intent)
		compiled, err := compileSourceIdentity(raw, settings, map[config.ID]subscription.Snapshot{"source-a": snapshot})
		if err != nil {
			t.Fatal(err)
		}
		manifest, _ := compiled.Bundle()
		if i > 0 && manifest.Generation == prior {
			t.Fatal("different original provider bytes lost from commitment")
		}
		prior = manifest.Generation
		if _, err = compileSourceIdentity(raw, settings, nil); err == nil {
			t.Fatal("missing referenced provider accepted")
		}
	}
}
