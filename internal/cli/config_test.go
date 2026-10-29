//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func configCLIPlan(t *testing.T) control.Plan {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/control/plan-digest-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Raw string `json:"raw"`
		} `json:"cases"`
	}
	if json.Unmarshal(raw, &vectors) != nil || len(vectors.Cases) == 0 {
		t.Fatal("missing independent Plan fixture")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(vectors.Cases[0].Raw), &fields) != nil {
		t.Fatal("invalid vector")
	}
	delete(fields, "plan_digest")
	input := subscriptionCLIJSON(t, fields)
	plan, err := control.BuildPlan(input, func(json.RawMessage, []control.PlanInput) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestConfigCLIPlanOutputPreservesCompleteEnvelopeAndFailures(t *testing.T) {
	plan := configCLIPlan(t)
	response := subscriptionCLIJSON(t, map[string]any{"ok": true, "data": json.RawMessage(plan.Bytes())})
	r := commandRun{ctx: context.Background(), response: response}
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := r.savePlanOutput(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, func() []byte {
		var v struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(response, &v)
		return v.Data
	}()) || !bytes.Equal(r.response, response) {
		t.Fatal("Plan output or original response changed", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("output mode")
	}
	if err := r.savePlanOutput(path); !errors.Is(err, control.ErrConflict) {
		t.Fatal("overwrite accepted", err)
	}
	fail := []byte(`{"ok":false,"data":null,"error":{"code":"CONFLICT"}}`)
	r.response = fail
	absent := filepath.Join(t.TempDir(), "absent")
	if err := r.savePlanOutput(absent); err != nil || !bytes.Equal(r.response, fail) {
		t.Fatal("original service failure replaced", err)
	}
	if _, err := os.Lstat(absent); !os.IsNotExist(err) {
		t.Fatal("failed Plan published output")
	}
}

func TestConfigCLIApplyExactPlanOfflineDoesNotRefreshOrStart(t *testing.T) {
	p := cliPaths(t)
	store, err := config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	plan := configCLIPlan(t)
	path := subscriptionCLIFile(t, "plan.json", plan.Bytes())
	before := cliTree(t, p)
	code, response, _ := cliResponse(t, []string{"config", "apply", "--plan", path, "--confirm", plan.Digest(), "--accept-disruption", "--request-id", "exact-old-request"}, strings.NewReader(""))
	if code == 0 || string(response["request_id"]) != `"exact-old-request"` || subscriptionCLIError(t, response) != "NOT_RUNNING" {
		t.Fatal("Apply refreshed revision, lost identity or bootstrapped", code, string(response["error"]))
	}
	if !reflect.DeepEqual(before, cliTree(t, p)) {
		t.Fatal("offline Apply changed state")
	}
}

func TestConfigCLIInvalidDeclarationsAndFlagsArePrivateAndReadOnly(t *testing.T) {
	p := cliPaths(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "output.json")
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	badJSON := subscriptionCLIFile(t, "private-plan", []byte(`{"secret":"private-file-body","secret":null}`))
	yaml := subscriptionCLIFile(t, "yaml-plan", []byte("schema_version: 2\n"))
	oversize := subscriptionCLIFile(t, "oversize", bytes.Repeat([]byte{' '}, control.MaxRequestBytes+1))
	list := subscriptionCLIFile(t, "list", []byte(`[]`))
	for i, args := range [][]string{
		{"config"}, {"config", "plan"}, {"config", "plan", "--input", list, "--output", output}, {"config", "plan", "--input", fifo, "--output", output},
		{"config", "plan", "--input", oversize, "--output", output}, {"config", "plan", "--input", badJSON, "--output", output},
		{"config", "apply", "--plan", yaml, "--confirm", strings.Repeat("a", 64)},
		{"config", "apply", "--plan", badJSON, "--confirm", strings.Repeat("a", 64)},
		{"config", "apply", "--plan", fifo, "--confirm", strings.Repeat("a", 64)},
		{"config", "apply", "--plan", oversize, "--confirm", strings.Repeat("a", 64)},
		{"config", "apply", "--plan", badJSON}, {"config", "apply", "--expected-revision", "rev"},
		{"config", "plan", "--unknown"}, {"config", "apply", "extra"},
	} {
		before := cliTree(t, p)
		code, _, out := cliResponse(t, args, strings.NewReader(""))
		if code != 2 {
			t.Fatalf("case %d: exit %d", i, code)
		}
		subscriptionCLIPrivate(t, out, badJSON, fifo, oversize, yaml)
		if !reflect.DeepEqual(before, cliTree(t, p)) {
			t.Fatal("invalid declaration wrote state")
		}
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			t.Fatal("invalid input published output")
		}
	}
}

func TestConfigCLIPlanBackendFailureDoesNotPublish(t *testing.T) {
	p := cliPaths(t)
	// An Apply-valid local declaration reaches the read-only service use case;
	// missing protected state remains the backend error, without output publication.
	input := subscriptionCLIFile(t, "intent.yaml", []byte("schema_version: 1\ncore:\n  mihomo:\n    path: /private/uninstalled-cli-core\n    version: fixture\n    sha256: '"+strings.Repeat("a", 64)+"'\nsubscriptions: []\nselection:\n  active_subscription: null\ngroups: []\napps: []\nrules: []\ndefault_target:\n  kind: reject\n"))
	output := filepath.Join(t.TempDir(), "plan.json")
	before := cliTree(t, p)
	code, response, out := cliResponse(t, []string{"config", "plan", "--input", input, "--output", output}, strings.NewReader(""))
	if code == 0 || subscriptionCLIError(t, response) == "INVALID_ARGUMENT" {
		t.Fatal("valid YAML did not reach backend", code, string(response["error"]))
	}
	subscriptionCLIPrivate(t, out, input)
	if !reflect.DeepEqual(before, cliTree(t, p)) {
		t.Fatal("failed Plan changed state")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("backend failure published Plan")
	}
}

func TestConfigCLIPlanJSONStrictAndOwned(t *testing.T) {
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":"\ud800"}`, `{} {}`, "x: 1", `null`, "\xff"} {
		path := subscriptionCLIFile(t, "plan", []byte(raw))
		got, err := readCLIPlan(context.Background(), path)
		// null is valid JSON, but the request validator must subsequently reject it.
		if raw == "null" {
			if err != nil || string(got) != "null" {
				t.Fatal("JSON scalar parser")
			}
			continue
		}
		if err == nil || got != nil {
			t.Fatal("non-strict JSON accepted")
		}
	}
	path := subscriptionCLIFile(t, "plan", []byte(" { \"x\": 1 } "))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	got, err := readCLIPlan(context.Background(), link)
	if err != nil || string(got) != `{"x":1}` {
		t.Fatal("regular symlink/canonical JSON", err)
	}
	got[0] = 'x'
	again, _ := readCLIPlan(context.Background(), path)
	if string(again) != `{"x":1}` {
		t.Fatal("returned bytes alias file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readCLIPlan(ctx, path); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancelled read accepted")
	}
}
