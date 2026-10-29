package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The valid .yaml fixture envelopes use the JSON subset of YAML 1.2 so these
// stdlib tests validate their structure, not a future production YAML parser.
func fixtureJSON(t *testing.T, name string, target any) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "username", "secret", "token", "authorization", "prompt", "argv", "env"} {
		if strings.Contains(strings.ToLower(string(data)), `"`+key+`"`) {
			t.Fatalf("sensitive field %s in %s", key, name)
		}
	}
	return data
}

type fixtureProxy struct {
	Name, Type, Server string
	Port               int
}
type providerSnapshot struct {
	ID       string
	Provider struct{ Proxies []fixtureProxy }
}

func TestFixtureProvidersCoverStableIdentityAndMissingRegion(t *testing.T) {
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Synthetic     bool
		Sources       []struct {
			ID        string
			Snapshots []providerSnapshot
		}
	}
	fixtureJSON(t, "providers/two-sources.yaml", &doc)
	if doc.SchemaVersion != 1 || !doc.Synthetic || len(doc.Sources) != 2 {
		t.Fatal("expected two synthetic sources")
	}
	a, b := doc.Sources[0], doc.Sources[1]
	if a.ID == b.ID || len(a.Snapshots) != 3 || len(b.Snapshots) != 1 {
		t.Fatal("missing source/snapshot cases")
	}
	initial, renamed, missing := a.Snapshots[0].Provider.Proxies, a.Snapshots[1].Provider.Proxies, a.Snapshots[2].Provider.Proxies
	if len(initial) != 2 || len(renamed) != 2 || len(missing) != 1 || !strings.HasPrefix(initial[1].Name, "JP") || !strings.HasPrefix(missing[0].Name, "US") {
		t.Fatal("missing region case absent")
	}
	if initial[0].Name == renamed[0].Name || initial[0].Server != renamed[0].Server || initial[0].Port != renamed[0].Port {
		t.Fatal("rename must preserve connection parameters")
	}
	other := b.Snapshots[0].Provider.Proxies[0]
	if initial[0].Name != other.Name || initial[0].Port == other.Port {
		t.Fatal("cross-source duplicate name must have distinct synthetic identity")
	}
	for _, source := range doc.Sources {
		for _, snap := range source.Snapshots {
			for _, proxy := range snap.Provider.Proxies {
				if proxy.Type != "socks5" || proxy.Server != "127.0.0.1" || proxy.Port < 1024 || proxy.Port > 65535 {
					t.Fatal("fixture endpoint is not synthetic loopback")
				}
			}
		}
	}
}

func TestFixtureInvalidYAMLPayloadIsPreserved(t *testing.T) {
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Synthetic     bool
		Cases         []struct {
			ID             string
			RawYAML        string `json:"raw_yaml"`
			ExpectedReason string `json:"expected_reason"`
		}
	}
	fixtureJSON(t, "providers/invalid-cases.yaml", &doc)
	if doc.SchemaVersion != 1 || !doc.Synthetic || len(doc.Cases) != 2 {
		t.Fatal("missing invalid cases")
	}
	if doc.Cases[0].ID != "malformed-yaml" || doc.Cases[0].RawYAML != "proxies: [\n" || doc.Cases[0].ExpectedReason != "invalid_yaml" {
		t.Fatal("malformed YAML payload altered")
	}
	if doc.Cases[1].ID != "duplicate-identity" || doc.Cases[1].ExpectedReason != "ambiguous_node_identity" {
		t.Fatal("missing ambiguous identity case")
	}
}

func TestFixtureTruthSetRequiresRuntimeRegistration(t *testing.T) {
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Status        EvidenceStatus
		Registration  struct {
			RequiredBeforeExecution      bool `json:"required_before_execution"`
			CalibrationCountsAsRealAgent bool `json:"calibration_counts_as_real_agent"`
		}
		Processes []struct {
			Symbol           string  `json:"symbol"`
			Name             string  `json:"name"`
			PIDSlot          string  `json:"pid_slot"`
			Parent           *string `json:"parent"`
			PID              *int    `json:"pid"`
			OSStartIdentity  *string `json:"os_start_identity"`
			MustFollowExitOf *string `json:"must_follow_exit_of"`
		} `json:"processes"`
		Domains    []struct{ Symbol, Name string }
		ShortFlows []struct {
			ID                    string `json:"id"`
			Process               string `json:"process"`
			Domain                string `json:"domain"`
			ExpectedProcess       string `json:"expected_process"`
			TerminalBytesRequired bool   `json:"terminal_bytes_required"`
		} `json:"short_flows"`
		EdgeCases []struct {
			ID              string `json:"id"`
			ExpectedProcess string `json:"expected_process"`
			ExpectedDomain  string `json:"expected_domain"`
			ExpectedReason  string `json:"expected_reason"`
			QualityReason   string `json:"quality_reason"`
		} `json:"edge_cases"`
	}
	fixtureJSON(t, "traffic/truth-set.json", &doc)
	if doc.SchemaVersion != 1 || doc.Status != Unverified || !doc.Registration.RequiredBeforeExecution || doc.Registration.CalibrationCountsAsRealAgent {
		t.Fatal("fixture claims an executed workflow")
	}
	if len(doc.Processes) != 5 || len(doc.Domains) != 2 || len(doc.ShortFlows) != 100 {
		t.Fatal("incomplete preregistered truth set")
	}
	processes := map[string]bool{}
	for _, p := range doc.Processes {
		if p.PID != nil || p.OSStartIdentity != nil || p.Symbol == "" {
			t.Fatal("synthetic PID/start identity presented as fact")
		}
		processes[p.Symbol] = true
	}
	if doc.Processes[0].Name != doc.Processes[1].Name || doc.Processes[2].Parent == nil || *doc.Processes[2].Parent != doc.Processes[0].Symbol || doc.Processes[3].Parent == nil || *doc.Processes[3].Parent != doc.Processes[2].Symbol {
		t.Fatal("same-name/child/grandchild matrix absent")
	}
	if doc.Processes[4].PIDSlot != doc.Processes[0].PIDSlot || doc.Processes[4].MustFollowExitOf == nil || *doc.Processes[4].MustFollowExitOf != doc.Processes[0].Symbol {
		t.Fatal("PID reuse lacks ordered distinct process instances")
	}
	seen := map[string]bool{}
	for _, flow := range doc.ShortFlows {
		if seen[flow.ID] || !processes[flow.Process] || flow.ExpectedProcess != "identified" || !flow.TerminalBytesRequired || (flow.Domain != doc.Domains[0].Symbol && flow.Domain != doc.Domains[1].Symbol) {
			t.Fatal("short-flow oracle weakened")
		}
		seen[flow.ID] = true
	}
	if len(doc.EdgeCases) != 3 || doc.EdgeCases[0].ExpectedDomain != "ip-only" || doc.EdgeCases[1].ExpectedProcess != "unknown" || doc.EdgeCases[1].ExpectedReason != "shared_socket" || doc.EdgeCases[2].QualityReason != "encrypted_request_boundary" {
		t.Fatal("predeclared invisible/shared/IP-only reasons missing")
	}
}
