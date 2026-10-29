package coreadmission

import (
	"bytes"
	"encoding/json"
	"hoopa/protocol/identityprofile"
	"testing"
)

func exampleV4() handoff {
	h := exampleV3()
	h.Schema = identityprofile.HandoffSchema
	h.IdentityProfile = identityprofile.ID
	return h
}
func TestFieldProfileAdoptReadbackAndImmutableGrant(t *testing.T) {
	h := exampleV4()
	f := newScopedFake(h)
	o, e := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close()
	p, e := o.FieldProfile()
	if e != nil || p.Validate(h.Generation) != nil || o.EventSchemaVersion() != 5 {
		t.Fatal(p, e)
	}
	f.publishExecutable()
	id, e := o.Admit(nil)
	if e != nil || !id.HasFieldProfile || id.FieldProfile != p || id.Schema != "sourceidentity-core-admission-v3" || id.Executable.Status != "available" || id.Executable.Schema != "sourceidentity-executable-call-v2" || id.IdentityCapability != "" || id.ProcessPath != "" {
		t.Fatal(id, e)
	}
	saved := id
	f.values[h.Maps["identity_scope"]][8] ^= 1
	if _, e = o.Admit(nil); e == nil {
		t.Fatal("changed scope grants")
	}
	if id != saved {
		t.Fatal("historical grant changed")
	}
	if _, e = o.FieldProfile(); e == nil {
		t.Fatal("revoked owner grants capability")
	}
	if o.scopeRawHex != p.ScopeRawHex {
		t.Fatal("failed readback replaced success")
	}
	for _, old := range []handoff{example(), exampleV2(), exampleV3()} {
		b := newScopedFake(exampleV3())
		_ = b
		raw := encoded(old)
		if bytes.Contains(raw, []byte("identity_profile")) {
			t.Fatal("legacy serialization changed")
		}
	}
}
func TestFieldProfileHandoffExactVersionIsolation(t *testing.T) {
	h := exampleV4()
	if _, e := parse(encoded(h)); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*handoff){func(h *handoff) { h.IdentityProfile = "" }, func(h *handoff) { h.IdentityProfile = "other" }, func(h *handoff) { h.Schema = "hoopa-core-identity-handoff-v3" }, func(h *handoff) { delete(h.Maps, "identity_scope") }} {
		v := exampleV4()
		change(&v)
		if _, e := parse(encoded(v)); e == nil {
			t.Fatal("bad handoff")
		}
	}
	var fields map[string]any
	json.Unmarshal(encoded(h), &fields)
	delete(fields, "identity_profile")
	raw, _ := json.Marshal(fields)
	if _, e := parse(raw); e == nil {
		t.Fatal("missing identity id")
	}
	old := exampleV3()
	f := newScopedFake(old)
	o, e := adopt(encoded(old), old.CoreGeneration, old.Port, f)
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close()
	if _, e = o.FieldProfile(); e == nil {
		t.Fatal("legacy grants fields")
	}
	f.publishExecutable()
	id, e := o.Admit(nil)
	if e != nil || id.HasFieldProfile || id.IdentityCapability != "unverified" || id.Executable.Status != "unverified" {
		t.Fatal(id, e)
	}
}
