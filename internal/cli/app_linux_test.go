//go:build linux && amd64

package cli

import (
	"context"
	"encoding/json"
	"hoopa/internal/control"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAppCLIInspectIsReadOnlyLocalAndSafe(t *testing.T) {
	paths := cliPaths(t)
	t.Setenv("HTTP_PROXY", "http://credential-private:password-private@127.0.0.1:9999")
	raw := subscriptionCLIJSON(t, initCLIIntent())
	if _, err := control.InitializeLocal(context.Background(), paths, control.InitOptions{MihomoPath: "/private/never-installed-mihomo", Intent: raw}); err != nil {
		t.Fatal(err)
	}
	before := cliTree(t, paths)
	for _, args := range [][]string{{"app", "inspect", "direct-a"}, {"app", "inspect", "direct-a", "--mode", "inherit"}} {
		code, response, out := cliResponse(t, args, strings.NewReader(""))
		if code != 0 || string(response["ok"]) != "true" || string(response["request_id"]) != "null" {
			t.Fatal("local inspect failed", code, out)
		}
		var report map[string]json.RawMessage
		if json.Unmarshal(response["data"], &report) != nil || string(report["declared_mode"]) != `"direct"` || string(report["entry_state"]) != `"not_applicable"` || string(report["entry"]) != "null" || string(report["binding_sha256"]) != "null" || string(report["launches"]) != "[]" || string(report["descendants_scope"]) != `"not_enumerated"` {
			t.Fatal("unsafe/incomplete inspect", out)
		}
		if strings.Contains(out, "never-installed") || strings.Contains(out, "credential-private") || strings.Contains(out, "password-private") {
			t.Fatal("private input exposed")
		}
	}
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("inspect mutated state")
	}
	for _, args := range [][]string{{"app", "inspect", "direct-a", "--mode", "wrong"}, {"app", "inspect", "bad id"}, {"app", "inspect", "direct-a", "--unknown"}, {"app", "inspect", "direct-a", "--mode="}} {
		code, response, out := cliResponse(t, args, strings.NewReader(""))
		if code != 2 || string(response["ok"]) != "false" {
			t.Fatal("invalid inspect", code, out)
		}
	}
	// An incomplete history namespace must not be silently shown as no launches.
	if err := os.RemoveAll(filepath.Join(paths.State, "app-launches")); err != nil {
		t.Fatal(err)
	}
	before = cliTree(t, paths)
	code, response, out := cliResponse(t, []string{"app", "inspect", "direct-a"}, strings.NewReader(""))
	var failure struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(response["error"], &failure) != nil {
		t.Fatal("invalid failure envelope")
	}
	if code != 5 || failure.Code != "INTEGRITY_ERROR" || string(response["ok"]) != "false" {
		t.Fatal("missing history shown as empty", code, out)
	}
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("inspect repaired missing history")
	}
}
