package subscription

// T035 definitions bind the planned pure provider API. They intentionally remain
// RED until T038/T039 implement parsing and identity; no fetch/core is simulated.
import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func providerOptions() ParseOptions {
	return ParseOptions{SourceID: "source-a", IdentityKey: bytes.Repeat([]byte{0x71}, 32)}
}

func providerNode(name string, port int) map[string]any {
	return map[string]any{"name": name, "type": "socks5", "server": "127.0.0.1", "port": port}
}

func providerRaw(t *testing.T, nodes ...map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"proxies": nodes})
	if err != nil {
		t.Fatal("cannot encode synthetic provider")
	}
	return raw
}

func mustProvider(t *testing.T, raw []byte, options ParseOptions) Snapshot {
	t.Helper()
	snapshot, err := Parse(raw, options)
	if err != nil {
		t.Fatal("valid provider rejected (details withheld)")
	}
	if snapshot.SourceID != options.SourceID || snapshot.ID == "" || snapshot.KeyID == "" {
		t.Fatal("missing snapshot identity metadata")
	}
	seen := map[config.ID]bool{}
	for _, node := range snapshot.Nodes {
		if node.ID == "" || seen[node.ID] {
			t.Fatal("empty or repeated node identity")
		}
		seen[node.ID] = true
	}
	return snapshot
}

func requireProviderError(t *testing.T, raw []byte, options ParseOptions, want error) {
	t.Helper()
	_, err := Parse(raw, options)
	if !errors.Is(err, want) {
		t.Fatal("wrong provider rejection classification (details withheld)")
	}
}

func providerFixture(t *testing.T, source, snapshot string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("fixture location unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../testdata/providers/two-sources.yaml"))
	if err != nil {
		t.Fatal("required provider fixture unavailable")
	}
	var envelope struct {
		Sources []struct {
			ID        string `json:"id"`
			Snapshots []struct {
				ID       string          `json:"id"`
				Provider json.RawMessage `json:"provider"`
			} `json:"snapshots"`
		} `json:"sources"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("required provider fixture invalid")
	}
	for _, s := range envelope.Sources {
		if s.ID == source {
			for _, p := range s.Snapshots {
				if p.ID == snapshot {
					return append([]byte(nil), p.Provider...)
				}
			}
		}
	}
	t.Fatal("required provider fixture case missing")
	return nil
}

// Test fixture lookup only; ResolvePin itself always receives explicit source+ID.
func providerNamed(t *testing.T, snapshot Snapshot, name string) Node {
	t.Helper()
	var found Node
	count := 0
	for _, node := range snapshot.Nodes {
		if node.DisplayName == name {
			found = node
			count++
		}
	}
	if count != 1 {
		t.Fatal("required unique fixture node missing")
	}
	return found
}

func TestProviderFixtureIdentityAndRename(t *testing.T) {
	options := providerOptions()
	initial := mustProvider(t, providerFixture(t, "source-a", "a-initial"), options)
	renamed := mustProvider(t, providerFixture(t, "source-a", "a-renamed"), options)
	otherOptions := providerOptions()
	otherOptions.SourceID = "source-b"
	other := mustProvider(t, providerFixture(t, "source-b", "b-initial"), otherOptions)
	if len(initial.Nodes) != 2 || len(renamed.Nodes) != 2 || len(other.Nodes) != 2 {
		t.Fatal("fixture node count changed")
	}
	for _, region := range []string{"US", "JP"} {
		first := providerNamed(t, initial, region+" shared")
		next := providerNamed(t, renamed, region+" renamed")
		foreign := providerNamed(t, other, region+" shared")
		pin, err := ResolvePin(map[config.ID]Snapshot{options.SourceID: renamed}, NodeRef{SubscriptionID: options.SourceID, NodeID: first.ID})
		if err != nil || pin.DisplayName != next.DisplayName {
			t.Fatal("renamed node no longer resolves by original pin")
		}
		if first.ID != next.ID {
			t.Fatal("rename changed stable identity")
		}
		if first.ID == foreign.ID {
			t.Fatal("cross-source same names conflated identities")
		}
	}

	// F08/F09/F32: even within one source, names are not unique keys.
	sameName := mustProvider(t, providerRaw(t, providerNode("shared", 24001), providerNode("shared", 24002)), options)
	if len(sameName.Nodes) != 2 || sameName.Nodes[0].ID == sameName.Nodes[1].ID {
		t.Fatal("same-name nodes were overwritten")
	}
}

func TestProviderIdentityConnectionChanges(t *testing.T) {
	base := providerNode("ordinary", 24001)
	base["username"] = "synthetic-user-private"
	base["password"] = "synthetic-password-private"
	options := providerOptions()
	initial := mustProvider(t, providerRaw(t, base), options)
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"endpoint-host", "server", "127.0.0.2"}, {"endpoint-port", "port", 24002},
		{"username", "username", "changed-user-private"}, {"password", "password", "changed-password-private"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := map[string]any{}
			for k, v := range base {
				changed[k] = v
			}
			changed[tc.field] = tc.value
			candidate := mustProvider(t, providerRaw(t, changed), options)
			if candidate.Nodes[0].ID == initial.Nodes[0].ID {
				t.Fatal("connection change retained old identity")
			}
			_, err := ResolvePin(map[config.ID]Snapshot{options.SourceID: candidate}, NodeRef{SubscriptionID: options.SourceID, NodeID: initial.Nodes[0].ID})
			if !errors.Is(err, ErrPinUnresolved) {
				t.Fatal("old pin rebound after connection change")
			}
		})
	}
	for _, kind := range []string{"source", "key"} {
		t.Run(kind, func(t *testing.T) {
			changed := providerOptions()
			if kind == "source" {
				changed.SourceID = "source-b"
			} else {
				changed.IdentityKey = bytes.Repeat([]byte{0x72}, 32)
			}
			candidate := mustProvider(t, providerRaw(t, base), changed)
			if candidate.Nodes[0].ID == initial.Nodes[0].ID {
				t.Fatal("source/key change retained identity")
			}
			_, err := ResolvePin(map[config.ID]Snapshot{changed.SourceID: candidate}, NodeRef{SubscriptionID: options.SourceID, NodeID: initial.Nodes[0].ID})
			if !errors.Is(err, ErrPinUnresolved) {
				t.Fatal("old pin rebound after source/key change")
			}
		})
	}
}

func TestProviderCanonicalIdentity(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"dns", "ExAmPlE.invalid.", "example.invalid"},
		{"ipv6", "2001:0db8:0:0:0:0:0:1", "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := providerNode("first", 24001), providerNode("renamed", 24001)
			a["server"], b["server"] = tc.first, tc.second
			first := mustProvider(t, providerRaw(t, a), providerOptions())
			second := mustProvider(t, providerRaw(t, b), providerOptions())
			if first.Nodes[0].ID != second.Nodes[0].ID {
				t.Fatal("equivalent endpoint changed identity")
			}
			requireProviderError(t, providerRaw(t, a, b), providerOptions(), ErrAmbiguousIdentity)
		})
	}
	requireProviderError(t, providerRaw(t, providerNode("US first", 24001), providerNode("JP second", 24001)), providerOptions(), ErrAmbiguousIdentity)
}

func TestProviderIdentityKeyRequired(t *testing.T) {
	for _, size := range []int{0, 1, 31, 33} {
		t.Run(fmt.Sprintf("length-%d", size), func(t *testing.T) {
			options := providerOptions()
			options.IdentityKey = bytes.Repeat([]byte{0x71}, size)
			requireProviderError(t, providerRaw(t, providerNode("ordinary", 24001)), options, ErrIdentityKeyUnavailable)
		})
	}
}

func TestProviderRegionEvidence(t *testing.T) {
	raw := providerRaw(t, providerNode("US shared", 24001), providerNode("JP shared", 24002), providerNode("unclassified", 24003))
	unknown := mustProvider(t, raw, providerOptions())
	for _, node := range unknown.Nodes {
		if node.Region.Code != "" || node.Region.Kind != "unknown" {
			t.Fatal("undeclared region guessed")
		}
	}
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "JP ", Region: "JP"}}
	declared := mustProvider(t, raw, options)
	for _, item := range []struct {
		name string
		want RegionEvidence
	}{
		{"US shared", RegionEvidence{Code: "US", Kind: "declared-prefix", Rule: "US "}},
		{"JP shared", RegionEvidence{Code: "JP", Kind: "declared-prefix", Rule: "JP "}},
	} {
		node := providerNamed(t, declared, item.name)
		if node.Region != item.want {
			t.Fatal("declared region evidence mismatch")
		}
		if node.ID != providerNamed(t, unknown, item.name).ID {
			t.Fatal("region evidence changed connection identity")
		}
	}
	unclassified := providerNamed(t, declared, "unclassified")
	if unclassified.Region.Code != "" || unclassified.Region.Kind != "unknown" {
		t.Fatal("unmatched declared rules guessed a region")
	}

	options.RegionRules = []RegionRule{{Prefix: "US ", Region: "US"}, {Prefix: "US shared", Region: "JP"}}
	ambiguous := mustProvider(t, providerRaw(t, providerNode("US shared", 24001)), options)
	if ambiguous.Nodes[0].Region.Code != "" || ambiguous.Nodes[0].Region.Kind != "ambiguous" {
		t.Fatal("ambiguous region silently selected")
	}
}

func TestProviderPinExactSourceAndIdentity(t *testing.T) {
	options := providerOptions()
	a := mustProvider(t, providerFixture(t, "source-a", "a-initial"), options)
	bOptions := providerOptions()
	bOptions.SourceID = "source-b"
	// Identical connection bytes must still yield source-scoped IDs.
	b := mustProvider(t, providerFixture(t, "source-a", "a-initial"), bOptions)
	aPin := providerNamed(t, a, "JP shared")
	bPin := providerNamed(t, b, "JP shared")
	if aPin.ID == bPin.ID {
		t.Fatal("identity is not source scoped")
	}
	ref := NodeRef{SubscriptionID: "source-a", NodeID: aPin.ID}
	node, err := ResolvePin(map[config.ID]Snapshot{"source-a": a, "source-b": b}, ref)
	if err != nil || node.ID != aPin.ID {
		t.Fatal("exact cross-source pin failed")
	}
	missing := mustProvider(t, providerFixture(t, "source-a", "a-missing-jp"), options)
	for _, tc := range []struct {
		name      string
		snapshots map[config.ID]Snapshot
		ref       NodeRef
	}{
		{"missing-node-no-first-or-other-source", map[config.ID]Snapshot{"source-a": missing, "source-b": b}, ref},
		{"missing-source-no-active-fallback", map[config.ID]Snapshot{"source-b": b}, ref},
		{"wrong-source-same-connection", map[config.ID]Snapshot{"source-a": a, "source-b": b}, NodeRef{SubscriptionID: "source-b", NodeID: aPin.ID}},
		{"relabeled-source-snapshot", map[config.ID]Snapshot{"source-a": b}, NodeRef{SubscriptionID: "source-a", NodeID: bPin.ID}},
		{"empty-reference", map[config.ID]Snapshot{"source-a": a}, NodeRef{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ResolvePin(tc.snapshots, tc.ref); !errors.Is(err, ErrPinUnresolved) {
				t.Fatal("unresolved pin silently rebound")
			}
		})
	}
}

func TestProviderFailedCandidatePreservesPriorSnapshot(t *testing.T) {
	prior := mustProvider(t, providerFixture(t, "source-a", "a-initial"), providerOptions())
	before, err := json.Marshal(prior)
	if err != nil {
		t.Fatal("public metadata encoding failed")
	}
	nodes := append([]Node(nil), prior.Nodes...)
	ref := NodeRef{SubscriptionID: prior.SourceID, NodeID: providerNamed(t, prior, "JP shared").ID}
	// Independent pure candidates, not a simulated persistence/update adapter.
	for _, tc := range []struct {
		name    string
		raw     []byte
		options ParseOptions
		want    error
	}{
		{"malformed", []byte("proxies: ["), providerOptions(), ErrInvalidProvider},
		{"ambiguous", providerRaw(t, providerNode("one", 24001), providerNode("two", 24001)), providerOptions(), ErrAmbiguousIdentity},
		{"missing-key", providerRaw(t, providerNode("ordinary", 24003)), ParseOptions{SourceID: "source-a"}, ErrIdentityKeyUnavailable},
		{"bounded", providerRaw(t, providerNode("ordinary", 24003)), ParseOptions{SourceID: "source-a", IdentityKey: bytes.Repeat([]byte{0x71}, 32), Limits: Limits{MaxBytes: 1}}, ErrInputLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireProviderError(t, tc.raw, tc.options, tc.want)
			after, e := json.Marshal(prior)
			if e != nil || !bytes.Equal(before, after) || !reflect.DeepEqual(nodes, prior.Nodes) {
				t.Fatal("failed candidate mutated retained snapshot")
			}
			n, e := ResolvePin(map[config.ID]Snapshot{prior.SourceID: prior}, ref)
			if e != nil || n.ID != ref.NodeID {
				t.Fatal("failed candidate invalidated prior pin")
			}
		})
	}
}

func TestProviderRejectsHostileStructure(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"malformed", "proxies: ["}, {"duplicate-root-json", `{"proxies":[],"proxies":[]}`},
		{"duplicate-root-yaml", "proxies: []\nproxies: []\n"},
		{"duplicate-node-json", `{"proxies":[{"name":"first","name":"second","type":"socks5","server":"127.0.0.1","port":24001}]}`},
		{"duplicate-node-yaml", "proxies:\n - {name: first, name: second, type: socks5, server: 127.0.0.1, port: 24001}\n"},
		{"multiple-documents", "proxies: []\n---\nproxies: []\n"},
		{"non-provider-root", "[]"}, {"missing-proxies", `{}`}, {"non-node", `{"proxies":["foreign"]}`}, {"wrong-proxies-type", `{"proxies":{}}`},
		{"dangerous-custom-tag", "proxies: !include elsewhere\n"},
		{"dangerous-object-tag", "!!python/object/apply:os.system [forbidden]\n"},
		{"dangerous-node-tag", "proxies:\n - !foreign {name: ordinary, type: socks5, server: 127.0.0.1, port: 24001}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { requireProviderError(t, []byte(tc.raw), providerOptions(), ErrInvalidProvider) })
	}
	for _, field := range []string{"rules", "dns", "tun", "listeners", "external-controller", "secret", "profile", "proxy-groups"} {
		t.Run("root-"+field, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"proxies": []map[string]any{providerNode("ordinary", 24001)}, field: map[string]any{"enabled": true}})
			if err != nil {
				t.Fatal("synthetic encoding failed")
			}
			requireProviderError(t, raw, providerOptions(), ErrInvalidProvider)
		})
	}
	for _, field := range []string{"rules", "dns", "tun", "external-controller", "proxy-groups", "dialer-proxy", "region", "unrecognized-field"} {
		t.Run("node-"+field, func(t *testing.T) {
			node := providerNode("ordinary", 24001)
			node[field] = "foreign-private-value"
			requireProviderError(t, providerRaw(t, node), providerOptions(), ErrInvalidProvider)
		})
	}
	node := providerNode("ordinary", 24001)
	node["type"] = "unknown-protocol"
	requireProviderError(t, providerRaw(t, node), providerOptions(), ErrUnsupportedNode)
}

func TestProviderInputLimits(t *testing.T) {
	raw := providerRaw(t, providerNode("one", 24001))
	t.Run("raw-bytes", func(t *testing.T) {
		options := providerOptions()
		options.Limits.MaxBytes = len(raw)
		mustProvider(t, raw, options)
		requireProviderError(t, append(append([]byte(nil), raw...), ' '), options, ErrInputLimit)
	})
	t.Run("nodes", func(t *testing.T) {
		options := providerOptions()
		options.Limits.MaxNodes = 1
		mustProvider(t, raw, options)
		requireProviderError(t, providerRaw(t, providerNode("one", 24001), providerNode("two", 24002)), options, ErrInputLimit)
	})
	t.Run("depth", func(t *testing.T) {
		options := providerOptions()
		options.Limits.MaxDepth = 16
		mustProvider(t, raw, options)
		options.Limits.MaxDepth = 1
		requireProviderError(t, raw, options, ErrInputLimit)
	})
	t.Run("aliases", func(t *testing.T) {
		first := "proxies:\n - {name: one, type: socks5, server: &host 127.0.0.1, port: 24001}\n - {name: two, type: socks5, server: *host, port: 24002}\n"
		second := first + " - {name: three, type: socks5, server: *host, port: 24003}\n"
		options := providerOptions()
		options.Limits.MaxAliases = 1
		mustProvider(t, []byte(first), options)
		requireProviderError(t, []byte(second), options, ErrInputLimit)
	})
}

func TestProviderSecretsStayPrivate(t *testing.T) {
	const user = "synthetic-sensitive-user-6d83"
	const password = "synthetic-sensitive-password-a951"
	options := providerOptions()
	options.IdentityKey = bytes.Repeat([]byte("k9"), 16)
	node := providerNode("ordinary", 24001)
	node["username"], node["password"] = user, password
	raw := providerRaw(t, node)
	snapshot := mustProvider(t, raw, options)
	public, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal("public metadata encoding failed")
	}
	outputs := []string{string(public), fmt.Sprint(snapshot), fmt.Sprintf("%+v", snapshot), fmt.Sprintf("%#v", snapshot), fmt.Sprint(snapshot.Nodes[0]), fmt.Sprintf("%+v", snapshot.Nodes[0]), fmt.Sprintf("%#v", snapshot.Nodes[0])}

	invalidNode := providerNode("ordinary", 24001)
	invalidNode["username"], invalidNode["password"], invalidNode["unrecognized-field"] = user, password, password
	unsupported := providerNode("ordinary", 24001)
	unsupported["username"], unsupported["password"], unsupported["type"] = user, password, "unknown-protocol"
	keyless := options
	keyless.IdentityKey = nil
	bounded := options
	bounded.Limits.MaxBytes = 1
	for _, item := range []struct {
		raw     []byte
		options ParseOptions
		want    error
	}{
		{providerRaw(t, invalidNode), options, ErrInvalidProvider},
		{append(append([]byte(nil), raw...), '['), options, ErrInvalidProvider},
		{providerRaw(t, unsupported), options, ErrUnsupportedNode},
		{providerRaw(t, node, node), options, ErrAmbiguousIdentity},
		{raw, keyless, ErrIdentityKeyUnavailable},
		{raw, bounded, ErrInputLimit},
	} {
		_, err := Parse(item.raw, item.options)
		if !errors.Is(err, item.want) {
			t.Fatal("secret-bearing candidate rejection classification mismatch")
		}
		outputs = append(outputs, fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err))
	}

	for _, output := range outputs {
		for _, secret := range []string{user, password, string(options.IdentityKey), hex.EncodeToString(options.IdentityKey), base64.StdEncoding.EncodeToString(options.IdentityKey), fmt.Sprint(options.IdentityKey), fmt.Sprintf("%#v", options.IdentityKey), string(raw), base64.StdEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString([]byte(user)), base64.StdEncoding.EncodeToString([]byte(password))} {
			if strings.Contains(output, secret) {
				t.Fatal("private material exposed (details withheld)")
			}
		}
	}
}

func TestProviderParseDoesNotMutateInputs(t *testing.T) {
	options := providerOptions()
	options.RegionRules = []RegionRule{{Prefix: "US ", Region: "US"}}
	raw := providerRaw(t, providerNode("US ordinary", 24001))
	beforeRaw := append([]byte(nil), raw...)
	beforeKey := append([]byte(nil), options.IdentityKey...)
	beforeRules := append([]RegionRule(nil), options.RegionRules...)
	mustProvider(t, raw, options)
	if !bytes.Equal(beforeRaw, raw) || !bytes.Equal(beforeKey, options.IdentityKey) || !reflect.DeepEqual(beforeRules, options.RegionRules) {
		t.Fatal("pure parser mutated caller inputs")
	}
}
