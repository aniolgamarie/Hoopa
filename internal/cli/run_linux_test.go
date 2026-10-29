//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/launch"
)

func TestRunCLIArgumentsAreLiteralAndStrict(t *testing.T) {
	argv := []string{"/literal path", "a b", "$(touch never)", "--meter", "", "'quoted'"}
	args := append([]string{"--app", "app-a", "--mode=direct", "--meter", "--"}, argv...)
	parsed, err := parseRunArguments(args)
	if err != nil || parsed.app != "app-a" || parsed.mode != "direct" || !parsed.meter || !reflect.DeepEqual(parsed.argv, argv) {
		t.Fatal("literal argument parse", err)
	}
	args[5] = "mutated"
	if parsed.argv[0] != "/literal path" {
		t.Fatal("argv slice aliased")
	}
	for _, args := range [][]string{
		{}, {"--app", "app-a", "/bin/true"}, {"--app", "app-a", "--"}, {"--", "x"}, {"--app", "bad id", "--", "x"},
		{"--app", "app-a", "--mode", "wrong", "--", "x"}, {"--app", "app-a", "--mode=", "--", "x"},
		{"--app", "app-a", "--unknown", "--", "x"}, {"--app", "app-a", "--", ""}, {"--app", "app-a", "--", "a\x00b"}, {"--app", "app-a", "extra", "--", "x"},
	} {
		if _, err := parseRunArguments(args); err == nil {
			t.Fatal("invalid invocation parsed successfully")
		}
		var out, diagnostics bytes.Buffer
		code := Execute(context.Background(), append([]string{"run"}, args...), strings.NewReader(""), &out, &diagnostics)
		if code != 2 || out.Len() != 0 || diagnostics.String() != "hoopa: INVALID_ARGUMENT\n" {
			t.Fatalf("invalid invocation result code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
		}
	}
	var out, diagnostics bytes.Buffer
	if code := Execute(context.Background(), []string{"run", "--app", "app-a", "--", "x"}, strings.NewReader(""), &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.String() != "hoopa: INVALID_ARGUMENT\n" {
		t.Fatal("non-file stdio accepted")
	}
}

func runCLIRefusal(t *testing.T, args []string, want int, wantText string) {
	t.Helper()
	in, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	diagnostic, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	defer diagnostic.Close()
	code := Execute(context.Background(), append([]string{"run"}, args...), in, out, diagnostic)
	stdout, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(diagnostic.Name())
	if err != nil {
		t.Fatal(err)
	}
	if code != want || len(stdout) != 0 || string(stderr) != wantText {
		t.Fatalf("refusal code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
func TestRunCLICaptureRefusalsAreRawReadOnlyAndNeverStart(t *testing.T) {
	paths := cliPaths(t)
	before := cliTree(t, paths)
	runCLIRefusal(t, []string{"--app", "direct-a", "--", "/never-spawn"}, 4, "hoopa: PROFILE_UNAVAILABLE\n")
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("missing profile created state")
	}
	raw := subscriptionCLIJSON(t, initCLIIntent())
	if _, err := control.InitializeLocal(context.Background(), paths, control.InitOptions{MihomoPath: "/private/never-installed-mihomo", Intent: raw}); err != nil {
		t.Fatal(err)
	}
	before = cliTree(t, paths)
	runCLIRefusal(t, []string{"--app", "missing", "--", "/never-spawn"}, 4, "hoopa: APP_NOT_FOUND\n")
	runCLIRefusal(t, []string{"--app", "direct-a", "--mode", "managed", "--", "/never-spawn"}, 4, "hoopa: MANAGED_ENTRY_UNAVAILABLE\n")
	runCLIRefusal(t, []string{"--app", "direct-a", "--meter", "--", "/never-spawn"}, 2, "hoopa: METERING_MODE_INVALID\n")
	runCLIRefusal(t, []string{"--app", "direct-a", "--mode", "managed", "--meter", "--", "/never-spawn"}, 2, "hoopa: METERING_UNSUPPORTED\n")
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("refused launch changed state")
	}
}
func TestRunCLIFiniteDiagnosticsDoNotReclassifyApplicationCodes(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  int
		label string
	}{
		{launch.ErrLaunchArguments, 2, "INVALID_ARGUMENT"}, {launch.ErrEnvironment, 2, "INVALID_ARGUMENT"},
		{launch.ErrLaunchUnavailable, 4, "LAUNCH_UNAVAILABLE"}, {launch.ErrLaunchBookkeeping, 5, "LAUNCH_BOOKKEEPING_FAILED"},
		{errors.New("private executable path secret"), 4, "LAUNCH_UNAVAILABLE"},
	} {
		var out bytes.Buffer
		if got := runDiagnostic(&out, tc.err, nil); got != tc.code || out.String() != "hoopa: "+tc.label+"\n" {
			t.Fatal("diagnostic mismatch")
		}
	}
	for _, tc := range []struct {
		reason, label string
		code          int
	}{{"exec_format", "EXEC_FORMAT", 126}, {"exec_not_found", "EXEC_NOT_FOUND", 127}, {"exec_permission", "EXEC_PERMISSION", 126}, {"exec_unavailable", "EXEC_UNAVAILABLE", 126}} {
		var out bytes.Buffer
		if got := runExecDiagnostic(&out, tc.reason); got != tc.code || out.String() != "hoopa: "+tc.label+"\n" {
			t.Fatal("finite exec diagnostic")
		}
	}
	code := 17
	status := uint32(4352)
	var out bytes.Buffer
	observed := config.AppLaunchResult{ExitCode: &code, WaitStatus: status}
	if got := runDiagnostic(&out, launch.ErrLaunchBookkeeping, &observed); got != 5 || out.String() != "hoopa: LAUNCH_BOOKKEEPING_FAILED\nhoopa: observed_root exit=17 signal=unknown wait_status=4352\n" {
		t.Fatal("observed result lost")
	}
}

func TestRunCLIReservedHelperRejectsArgumentsBeforePaths(t *testing.T) {
	if code := Execute(nil, []string{launch.AppLauncherEntry, "not-an-authorized-helper-argument"}, nil, nil, nil); code != 125 {
		t.Fatalf("reserved helper not dispatched first: %d", code)
	}
}
