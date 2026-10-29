//go:build linux && amd64

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func initCLIIntent() map[string]any {
	return map[string]any{"schema_version": 1, "core": map[string]any{"mihomo": map[string]any{"path": "/private/never-installed-mihomo", "version": "declared-only", "sha256": strings.Repeat("a", 64)}}, "subscriptions": []any{}, "selection": map[string]any{"active_subscription": nil}, "groups": []any{}, "apps": []any{map[string]any{"id": "direct-a", "mode": "direct", "metering": false, "bypass": []any{}}}, "rules": []any{}, "default_target": map[string]any{"kind": "reject"}}
}
func initCLIReport(t *testing.T, response map[string]json.RawMessage) control.InitReport {
	t.Helper()
	if string(response["ok"]) != "true" || string(response["request_id"]) != "null" {
		t.Fatal("local init envelope not success/null identity")
	}
	var report control.InitReport
	if json.Unmarshal(response["data"], &report) != nil || report.SchemaVersion != 1 || report.PreparedComponents == nil || report.Missing == nil || report.MissingAppIDs == nil || report.Dependencies == nil || report.NextSteps == nil {
		t.Fatal("init report schema/array shape")
	}
	return report
}
func TestInitCLIIncompleteDeclarationsStayReadOnlyAndPrivate(t *testing.T) {
	p := cliPaths(t)
	before := cliTree(t, p)
	code, response, out := cliResponse(t, []string{"init", "--mihomo", "/private/never-installed-mihomo"}, strings.NewReader(""))
	if code != 0 {
		t.Fatal("missing intent should be finite report", code, out)
	}
	report := initCLIReport(t, response)
	if report.Prepared || report.InputState != "incomplete" || !reflect.DeepEqual(report.Missing, []string{"intent"}) {
		t.Fatal("missing intent reported ready")
	}
	partial := []byte("core:\n  mihomo:\n    path: /private/never-installed-mihomo\n    version: ''\n    sha256: ''\n")
	input := subscriptionCLIFile(t, "partial.yaml", partial)
	link := filepath.Join(t.TempDir(), "intent-link")
	if err := os.Symlink(input, link); err != nil {
		t.Fatal(err)
	}
	code, response, out = cliResponse(t, []string{"init", "--mihomo", "/private/never-installed-mihomo", "--input", link, "--controller-port", "30001", "--shared-port", "30002"}, strings.NewReader(""))
	if code != 0 {
		t.Fatal("partial YAML should produce report", code, out)
	}
	report = initCLIReport(t, response)
	if report.Prepared || report.InputState != "incomplete" || !reflect.DeepEqual(report.Missing, []string{"mihomo_version", "mihomo_sha256"}) {
		t.Fatal("metadata missing/guessed", report.Missing)
	}
	subscriptionCLIPrivate(t, out, input, link, "/private/never-installed-mihomo")
	if !reflect.DeepEqual(before, cliTree(t, p)) {
		t.Fatal("incomplete init created or mutated state")
	}
}
func TestInitCLICompleteDirectDraftWithoutCoreAndWriterConflict(t *testing.T) {
	p := cliPaths(t)
	raw := subscriptionCLIJSON(t, initCLIIntent())
	input := subscriptionCLIFile(t, "intent.json", raw)
	code, response, out := cliResponse(t, []string{"init", "--mihomo", "/private/never-installed-mihomo", "--input", input}, strings.NewReader(""))
	if code != 0 {
		t.Fatal("explicit direct draft preparation failed", code, out)
	}
	report := initCLIReport(t, response)
	if !report.Prepared || report.InputState != "complete" || report.DraftSHA256 == nil {
		t.Fatal("complete draft not durably prepared")
	}
	subscriptionCLIPrivate(t, out, input, "/private/never-installed-mihomo")
	store, err := config.OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.ReadActive()
	store.Close()
	if err != nil || active.Generation != nil || active.DesiredRuntime != "stopped" {
		t.Fatal("init applied/started runtime", err)
	}
	if _, err := os.Lstat(filepath.Join(p.Runtime, "service.json")); !os.IsNotExist(err) {
		t.Fatal("init started service")
	}
	// Holding the established Store writer must produce the normal finite
	// conflict, without creating a competing mutation/Apply path.
	writer, err := config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	before := cliTree(t, p)
	code, response, out = cliResponse(t, []string{"init", "--mihomo", "/private/never-installed-mihomo", "--input", input}, strings.NewReader(""))
	if code != 3 || subscriptionCLIError(t, response) != "CONFLICT" || string(response["request_id"]) != "null" {
		t.Fatal("writer conflict mapping", code, string(response["error"]))
	}
	subscriptionCLIPrivate(t, out, input, "/private/never-installed-mihomo")
	if !reflect.DeepEqual(before, cliTree(t, p)) {
		t.Fatal("conflicting initializer changed protected state")
	}
}
func TestInitCLIRejectsInvalidFlagsAndBoundedLocalDocuments(t *testing.T) {
	p := cliPaths(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	bad := subscriptionCLIFile(t, "bad-secret.json", []byte(`{"secret":"private-file-body","secret":null}`))
	scalar := subscriptionCLIFile(t, "scalar.yaml", []byte("just-a-string"))
	oversize := subscriptionCLIFile(t, "large", bytes.Repeat([]byte{' '}, control.MaxRequestBytes+1))
	complete := subscriptionCLIFile(t, "complete.json", subscriptionCLIJSON(t, initCLIIntent()))
	base := []string{"init", "--mihomo", "/private/never-installed-mihomo"}
	tests := [][]string{{"init"}, {"init", "extra"}, {"init", "--mihomo", "relative"}, append(initCLIArgs(base), "--unknown"), append(initCLIArgs(base), "--controller-port", "0"), append(initCLIArgs(base), "--controller-port", "65536"), append(initCLIArgs(base), "--controller-port", "-1"), append(initCLIArgs(base), "--controller-port", "1e3"), append(initCLIArgs(base), "--controller-port", "1"), append(initCLIArgs(base), "--controller-port", "1", "--shared-port", "1"), append(initCLIArgs(base), "--app-port", "app-a=0"), append(initCLIArgs(base), "--app-port", "bad id=3"), append(initCLIArgs(base), "--app-port", "app-a=3", "--app-port", "app-a=4"), append(initCLIArgs(base), "--input", complete, "--app-port", "direct-a=3")}
	for _, path := range []string{fifo, bad, scalar, oversize} {
		tests = append(tests, append(initCLIArgs(base), "--input", path))
	}
	for i, args := range tests {
		before := cliTree(t, p)
		code, response, out := cliResponse(t, args, strings.NewReader(""))
		if code != 2 || subscriptionCLIError(t, response) != "INVALID_ARGUMENT" {
			t.Fatalf("case%d wrong argument result: %d", i, code)
		}
		subscriptionCLIPrivate(t, out, fifo, bad, scalar, oversize, complete, "/private/never-installed-mihomo")
		if !reflect.DeepEqual(before, cliTree(t, p)) {
			t.Fatal("invalid init created state")
		}
	}
}
func initCLIArgs(values []string) []string { return append([]string(nil), values...) }
