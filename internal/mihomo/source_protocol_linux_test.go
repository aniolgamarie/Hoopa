//go:build linux && amd64

package mihomo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"hoopa/internal/config"
	wire "hoopa/protocol/corecontrol"
	sources "hoopa/protocol/sourcecontrol"
)

// This fixture grants no production capability. The synthetic local declaration
// only exercises version dispatch; ApprovedBasicCapability remains v1-only.
func sourceBlueprint(t *testing.T, address string) (inspectorBlueprint, sources.Snapshot) {
	t.Helper()
	b := inspectorBlueprintFor(t, address, config.BinaryReference{}, nil)
	b.record.Protocol = 2
	b.record.Mode = "basic-sources"
	b.record.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"}
	b.record.EvidenceSHA256 = sha256String([]byte("synthetic v2 controller script; no approval grant"))
	b.capabilityDigest, _ = b.record.Digest()
	var raw map[string]any
	if json.Unmarshal(b.raw, &raw) != nil {
		t.Fatal("fixture JSON")
	}
	raw["proxies"] = []map[string]any{{"name": "node:a", "type": "http", "server": "proxy.example.test", "port": 443, "username": "", "password": "", "tls": false}, {"name": "node:b", "type": "direct"}}
	raw["proxy-groups"] = []map[string]any{{"name": "group:auto", "type": "url-test", "proxies": []string{"node:a"}, "url": "https://probe.example.test/", "interval": 60, "timeout": 1000, "tolerance": 0, "lazy": false, "expected-status": "200-299"}, {"name": "group:route", "type": "select", "proxies": []string{"group:auto"}}}
	b.rebindRaw(inspectorJSON(t, raw))
	entries := []sources.Entry{}
	for _, e := range b.snapshot.Entries {
		entries = append(entries, sources.Entry(e))
	}
	s := sources.Snapshot{SchemaVersion: 2, ConfigSHA256: b.intent.ConfigSHA256, Controller: address, Entries: entries, Proxies: []sources.Proxy{
		{Name: "COMPATIBLE", Type: "Compatible", Members: []string{}}, {Name: "DIRECT", Type: "Direct", Members: []string{}}, {Name: "GLOBAL", Type: "Selector", Members: []string{"DIRECT", "REJECT", "node:a", "node:b", "group:auto", "group:route"}, Selected: "DIRECT"}, {Name: "PASS", Type: "Pass", Members: []string{}}, {Name: "REJECT", Type: "Reject", Members: []string{}}, {Name: "REJECT-DROP", Type: "RejectDrop", Members: []string{}},
		{Name: "node:a", Type: "Http", Address: "proxy.example.test:443", Members: []string{}}, {Name: "node:b", Type: "Direct", Members: []string{}}, {Name: "group:auto", Type: "URLTest", Members: []string{"node:a"}, Probe: &sources.Probe{URL: "https://probe.example.test/", IntervalSeconds: 60, TimeoutMS: 1000, ToleranceMS: 0}}, {Name: "group:route", Type: "Selector", Members: []string{"group:auto"}, Selected: "group:auto"},
	}, Rules: []sources.Rule{{Type: "Domain", Payload: "alpha.test", Target: "group:route"}, {Type: "Match", Target: "node:a"}}}
	sort.Slice(s.Proxies, func(i, j int) bool { return s.Proxies[i].Name < s.Proxies[j].Name })
	if s.Validate() != nil {
		t.Fatal("invalid synthetic source snapshot")
	}
	b.extra[SnapshotFile] = inspectorJSON(t, s)
	return b, s
}
func sourceStatus(t *testing.T, b inspectorBlueprint, s sources.Snapshot, state string) wire.Status {
	t.Helper()
	digest, err := s.Digest()
	if err != nil {
		t.Fatal(err)
	}
	v := sources.Status{SchemaVersion: 2, RunID: string(b.intent.RunID), ConfigGeneration: string(b.intent.Generation), ConfigSHA256: b.intent.ConfigSHA256, BinaryVersion: b.intent.Binary.Version, Mode: "basic-sources", Capabilities: append([]string{}, b.record.Capabilities...), State: state, SnapshotSHA256: digest}
	if state == "open" {
		v.Activation = &sources.Result{SchemaVersion: 2, OperationID: "operation:other-activate", RequestSHA256: strings.Repeat("d", 64), RunID: v.RunID, Result: "opened"}
	}
	if state == "draining" {
		v.Drain = &sources.Result{SchemaVersion: 2, OperationID: "operation:other-drain", RequestSHA256: strings.Repeat("e", 64), RunID: v.RunID, Result: "draining"}
	}
	if state == "degraded" {
		v.Activation = &sources.Result{SchemaVersion: 2, OperationID: "operation:other-activate", RequestSHA256: strings.Repeat("d", 64), RunID: v.RunID, Result: "failed"}
	}
	encoded := inspectorJSON(t, v)
	if v.Validate() != nil {
		t.Fatal("invalid v2 status fixture")
	}
	// Construct the script's common carrier independently of the controller
	// adapter under test; encoding/json retains schema2 without v1 validation.
	var out wire.Status
	if json.Unmarshal(encoded, &out) != nil {
		t.Fatal("fixture common carrier")
	}
	return out
}
func sourceReadSteps(t *testing.T, s sources.Snapshot, status wire.Status) []mutationStep {
	t.Helper()
	return []mutationStep{{Method: "GET", Path: "/hoopa/v2/status", Response: responseBytes(inspectorJSON(t, status))}, {Method: "GET", Path: "/hoopa/v2/snapshot", Response: responseBytes(inspectorJSON(t, s))}, {Method: "GET", Path: "/hoopa/v2/status", Response: responseBytes(inspectorJSON(t, status))}}
}
func sourceRequest(t *testing.T, b inspectorBlueprint, s sources.Snapshot, reg config.RunRegistration, sha, action string) wire.Mutation {
	t.Helper()
	digest, err := s.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return wire.Mutation{SchemaVersion: 2, Operation: action, OperationID: string(reg.OperationID), RunID: string(b.intent.RunID), ConfigGeneration: string(b.intent.Generation), ConfigSHA256: b.intent.ConfigSHA256, SnapshotSHA256: digest, RegistrationSHA256: sha}
}
func sourceResult(t *testing.T, q wire.Mutation, kind string) wire.Result {
	t.Helper()
	raw := inspectorJSON(t, q)
	digest := sha256.Sum256(raw)
	return wire.Result{SchemaVersion: 2, OperationID: q.OperationID, RequestSHA256: hex.EncodeToString(digest[:]), RunID: q.RunID, Result: kind}
}
func sourceProxy(s *sources.Snapshot, name string) *sources.Proxy {
	for i := range s.Proxies {
		if s.Proxies[i].Name == name {
			return &s.Proxies[i]
		}
	}
	panic("source fixture proxy")
}
func TestSourceCapabilityDispatchAndCommonCarriers(t *testing.T) {
	b, s := sourceBlueprint(t, "127.0.0.1:31003")
	if b.record.Validate() != nil {
		t.Fatal("exact synthetic v2 capability shape rejected")
	}
	approved, err := ApprovedBasicCapability("/synthetic/core")
	if err != nil || approved.Protocol != 1 || approved.Mode != "basic" || approved.Binary.SHA256 == b.record.Binary.SHA256 {
		t.Fatal("fixture minted or replaced approved v1 capability")
	}
	for _, mutate := range []func(*CapabilityRecord){func(c *CapabilityRecord) { c.Protocol = 1 }, func(c *CapabilityRecord) { c.Mode = "basic" }, func(c *CapabilityRecord) { c.Capabilities = c.Capabilities[:3] }, func(c *CapabilityRecord) { c.Capabilities[2] = "immutable-snapshot-v1" }, func(c *CapabilityRecord) { c.Capabilities[0], c.Capabilities[1] = c.Capabilities[1], c.Capabilities[0] }} {
		c := b.record
		c.Capabilities = append([]string{}, c.Capabilities...)
		mutate(&c)
		if c.Validate() != ErrCapability {
			t.Fatal("cross-profile capability accepted")
		}
	}
	status := sourceStatus(t, b, s, "open")
	decodedStatus, decodeErr := decodeControllerStatus(2, inspectorJSON(t, status))
	if decodeErr != nil || !reflect.DeepEqual(decodedStatus, status) {
		t.Fatal("v2 status adapter lost carrier/history fields", decodeErr)
	}
	if status.SchemaVersion != 2 || status.Validate() == nil {
		t.Fatal("v2 status carrier unexpectedly became valid v1")
	}
	if _, err := decodeControllerStatus(1, inspectorJSON(t, status)); err == nil {
		t.Fatal("v1 decoder accepted v2 status")
	}
	q := sourceRequest(t, b, s, mutationRegistration(b), strings.Repeat("3", 64), "activate")
	r := sourceResult(t, q, "opened")
	if r.Validate() == nil {
		t.Fatal("v2 result carrier validated as v1")
	}
	got, err := decodeControllerResult(2, inspectorJSON(t, r))
	if err != nil || got != r {
		t.Fatal("v2 result carrier lost schema", err)
	}
	if _, err := decodeControllerResult(1, inspectorJSON(t, r)); err == nil {
		t.Fatal("v1 result accepted v2")
	}
	gotDigest, err := controllerMutationDigest(2, q)
	if err != nil || gotDigest != r.RequestSHA256 {
		t.Fatal("v2 mutation digest differs from independent SHA")
	}
	if _, err := controllerMutationDigest(1, q); err == nil {
		t.Fatal("v1 mutation digest accepted schema2")
	}
	raw := inspectorJSON(t, s)
	canonical, cfg, controller, digest, err := decodeControllerSnapshot(2, append([]byte(" \n"), raw...))
	want, _ := s.Digest()
	if err != nil || !reflect.DeepEqual(canonical, raw) || cfg != b.intent.ConfigSHA256 || controller != b.intent.Controller || digest != want {
		t.Fatal("strict v2 canonical material not bound", err)
	}
	if _, _, _, _, err := decodeControllerSnapshot(1, raw); err == nil {
		t.Fatal("v1 snapshot accepted v2")
	}
	for _, protocol := range []int{0, 3} {
		if _, _, _, _, err := decodeControllerSnapshot(protocol, raw); err == nil {
			t.Fatal("unknown protocol accepted snapshot")
		}
	}
}
