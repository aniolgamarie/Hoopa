//go:build linux && amd64

package launch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == AppLauncherEntry {
		os.Exit(RunAppLauncher(os.Args[1:]))
	}
	if len(os.Args) > 2 && os.Args[1] == "__hoopa_run_fixture" {
		switch os.Args[2] {
		case "touch":
			_ = os.WriteFile(os.Args[3], []byte("ran"), 0600)
			os.Exit(0)
		case "exit":
			code, _ := strconv.Atoi(os.Args[3])
			os.Exit(code)
		case "signal":
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			time.Sleep(time.Second)
			os.Exit(99)
		case "cancel-after-dispatch":
			_, _ = os.Stdout.Write([]byte{'R'})
			var token [1]byte
			if _, e := io.ReadFull(os.Stdin, token[:]); e != nil || token[0] != 'C' {
				os.Exit(96)
			}
			os.Exit(17)
		case "terminal-conflict":
			p, e := config.ResolvePaths(os.Getenv("HOOPA_RUN_TEST_ROOT"), nil)
			if e != nil {
				os.Exit(91)
			}
			rows, e := config.ReadAppLaunchRecords(context.Background(), p)
			if e != nil {
				os.Exit(92)
			}
			for _, r := range rows {
				if r.Birth != nil && r.Birth.Child.PID == os.Getpid() && r.Authorization != nil {
					f, e := os.OpenFile(filepath.Join(p.State, "app-launches", string(r.Reservation.LaunchID), "terminal.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
					if e != nil {
						os.Exit(97)
					}
					_, e = f.Write([]byte("CONFLICT_ORIGINAL\n"))
					if e != nil || f.Close() != nil {
						os.Exit(98)
					}
					os.Exit(17)
				}
			}
			os.Exit(93)
		case "argv":
			p, e := config.ResolvePaths(os.Getenv("HOOPA_RUN_TEST_ROOT"), nil)
			if e != nil {
				os.Exit(91)
			}
			rows, e := config.ReadAppLaunchRecords(context.Background(), p)
			if e != nil {
				os.Exit(92)
			}
			found := false
			for _, r := range rows {
				if r.Birth != nil && r.Birth.Child.PID == os.Getpid() && r.Authorization != nil && r.Terminal == nil {
					found = true
				}
			}
			if !found {
				os.Exit(93)
			}
			input, e := io.ReadAll(os.Stdin)
			if e != nil {
				os.Exit(94)
			}
			args := []string{}
			for _, v := range os.Args[3:] {
				args = append(args, base64.StdEncoding.EncodeToString([]byte(v)))
			}
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"argv": args, "input": base64.StdEncoding.EncodeToString(input), "secret": os.Getenv("HOOPA_RUN_PRIVATE"), "authorized_before_main": found})
			_, _ = os.Stderr.Write([]byte("EXACT_APP_STDERR\n"))
			os.Exit(17)
		}
		os.Exit(95)
	}
	os.Exit(m.Run())
}

func runFixture(t *testing.T) (RunOptions, *config.Store, string) {
	t.Helper()
	root := t.TempDir()
	p, e := config.ResolvePaths(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	s, e := config.OpenStore(p, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.InitializeLaunchHistory(); e != nil {
		t.Fatal(e)
	}
	app := config.App{ID: "run-app", Mode: "inherit", Bypass: []string{}}
	env, e := BuildEnvironment([]string{"PATH=/usr/bin:/bin", "HOOPA_RUN_TEST_ROOT=" + root, "HOOPA_RUN_PRIVATE=RUN_PRIVATE_9812", "GORACE=atexit_sleep_ms=0"}, app, nil)
	if e != nil {
		t.Fatal(e)
	}
	stdio := make([]*os.File, 3)
	for i := range stdio {
		f, e := os.CreateTemp(root, "stdio-")
		if e != nil {
			t.Fatal(e)
		}
		stdio[i] = f
		t.Cleanup(func() { f.Close() })
	}
	sha := strings.Repeat("a", 64)
	return RunOptions{Paths: p, Profile: config.AppLaunchProfile{Kind: "draft", Revision: "draft:" + sha, IntentSHA256: sha, App: app}, App: app, Environment: env, Stdin: stdio[0], Stdout: stdio[1], Stderr: stdio[2]}, s, root
}
func runFixtureRead(t *testing.T, f *os.File) []byte {
	t.Helper()
	if _, e := f.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(f)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestApplicationRunLiteralIOAndDurableAuthorization(t *testing.T) {
	o, store, root := runFixture(t)
	input := []byte{0, 1, 255, 'x', '\n'}
	if _, e := o.Stdin.Write(input); e != nil {
		t.Fatal(e)
	}
	o.Stdin.Seek(0, 0)
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	args := []string{"", "two words", "$(touch NEVER)", "a'\"b", "line\nbreak", string([]byte{255, 254}), "ARG_PRIVATE_3271"}
	o.Argv = append([]string{self, "__hoopa_run_fixture", "argv"}, args...)
	before, e := store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	r, e := Run(context.Background(), o)
	if e != nil || r.Code != 17 || r.Observed == nil || r.Observed.Signal != nil || r.Observed.ExitCode == nil || *r.Observed.ExitCode != 17 {
		t.Fatal("actual runner", r.Code, e)
	}
	var got struct {
		Argv       []string `json:"argv"`
		Input      string   `json:"input"`
		Secret     string   `json:"secret"`
		Authorized bool     `json:"authorized_before_main"`
	}
	if e = json.Unmarshal(runFixtureRead(t, o.Stdout), &got); e != nil {
		t.Fatal(e)
	}
	if !got.Authorized || got.Input != base64.StdEncoding.EncodeToString(input) || got.Secret != "RUN_PRIVATE_9812" || len(got.Argv) != len(args) {
		t.Fatal("application inputs")
	}
	for i, v := range args {
		if got.Argv[i] != base64.StdEncoding.EncodeToString([]byte(v)) {
			t.Fatal("argv bytes changed")
		}
	}
	if !bytes.Equal(runFixtureRead(t, o.Stderr), []byte("EXACT_APP_STDERR\n")) {
		t.Fatal("stderr changed")
	}
	rows, e := config.ReadAppLaunchRecords(context.Background(), o.Paths)
	if e != nil || len(rows) != 1 || rows[0].Terminal == nil || rows[0].Authorization == nil || rows[0].Reservation.LaunchID != r.LaunchID {
		t.Fatal("missing exact terminal history", e)
	}
	after, e := store.ReadActive()
	if e != nil || before.Revision != after.Revision || before.Generation != after.Generation {
		t.Fatal("run changed active")
	}
	err := filepath.WalkDir(o.Paths.State, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			if bytes.Contains(raw, []byte("ARG_PRIVATE_3271")) || bytes.Contains(raw, []byte("RUN_PRIVATE_9812")) {
				t.Fatal("payload persisted")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = root // The fixture's input/output files are intentionally outside Store.
}

func TestApplicationRunActualExecFailuresAndNumericExits(t *testing.T) {
	for _, kind := range []string{"format", "missing", "permission", "normal126", "normal130", "signal", "path"} {
		t.Run(kind, func(t *testing.T) {
			o, _, root := runFixture(t)
			self, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			want, reason := 126, ""
			switch kind {
			case "format":
				target := filepath.Join(root, "bad-format")
				if e = os.WriteFile(target, []byte("echo MUST_NOT_RUN\n"), 0700); e != nil {
					t.Fatal(e)
				}
				o.Argv = []string{target}
				reason = "exec_format"
			case "missing":
				o.Argv = []string{filepath.Join(root, "missing")}
				want = 127
				reason = "exec_not_found"
			case "permission":
				target := filepath.Join(root, "no-execute")
				if e = os.WriteFile(target, []byte("x"), 0600); e != nil {
					t.Fatal(e)
				}
				o.Argv = []string{target}
				reason = "exec_permission"
			case "normal126":
				o.Argv = []string{self, "__hoopa_run_fixture", "exit", "126"}
			case "normal130":
				o.Argv = []string{self, "__hoopa_run_fixture", "exit", "130"}
				want = 130
			case "signal":
				o.Argv = []string{self, "__hoopa_run_fixture", "signal"}
				want = 143
			case "path":
				o.Argv = []string{"true"}
				want = 0
			}
			r, e := Run(context.Background(), o)
			if e != nil || r.Code != want || r.Observed == nil {
				t.Fatal("run", r.Code, e)
			}
			if reason == "" && r.Observed.ExecFailureReason != nil || reason != "" && (r.Observed.ExecFailureReason == nil || *r.Observed.ExecFailureReason != reason) {
				t.Fatal("invented/lost exec failure")
			}
			if kind == "signal" {
				if r.Observed.Signal == nil || *r.Observed.Signal != 15 || r.Observed.ExitCode != nil {
					t.Fatal("signal confused with normal exit")
				}
			} else if r.Observed.Signal != nil || r.Observed.ExitCode == nil || *r.Observed.ExitCode != want {
				t.Fatal("normal exit confused with signal")
			}
			rows, e := config.ReadAppLaunchRecords(context.Background(), o.Paths)
			if e != nil || len(rows) != 1 || rows[0].Terminal == nil {
				t.Fatal("terminal record", e)
			}
			if len(runFixtureRead(t, o.Stdout)) != 0 {
				t.Fatal("shell fallback or output")
			}
		})
	}
}

func TestApplicationRunRefusesBeforeFork(t *testing.T) {
	for _, kind := range []string{"closed-stdio", "cancelled", "invalid-argv"} {
		t.Run(kind, func(t *testing.T) {
			o, _, root := runFixture(t)
			o.Argv = []string{"/nonexistent"}
			ctx := context.Background()
			if kind == "closed-stdio" {
				o.Stdin.Close()
			}
			if kind == "invalid-argv" {
				o.Argv = []string{"bad\x00argv"}
			}
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r, e := Run(ctx, o)
			if e == nil || r.Observed != nil || r.LaunchID != "" {
				t.Fatal("refusal dispatched", e)
			}
			rows, e := config.ReadAppLaunchRecords(context.Background(), o.Paths)
			if e != nil || len(rows) != 0 {
				t.Fatal("refusal reserved", e)
			}
			_ = root
		})
	}
}

func TestApplicationRunCancellationAfterDispatchStillWaitsOriginal(t *testing.T) {
	o, _, _ := runFixture(t)
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	o.Argv = []string{self, "__hoopa_run_fixture", "cancel-after-dispatch"}
	pr, pw, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	sr, sw, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer pr.Close()
	defer pw.Close()
	defer sr.Close()
	defer sw.Close()
	o.Stdin, o.Stdout = pr, sw
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type completed struct {
		result RunResult
		err    error
	}
	done := make(chan completed, 1)
	go func() { r, e := Run(ctx, o); done <- completed{r, e} }()
	// Real application's R can only arrive after the durable authorization GO.
	if e = sr.SetReadDeadline(time.Now().Add(5 * time.Second)); e != nil {
		t.Fatal(e)
	}
	var token [1]byte
	if _, e = io.ReadFull(sr, token[:]); e != nil || token[0] != 'R' {
		t.Fatal("actual app ready", e)
	}
	cancel()
	select {
	case <-done:
		t.Fatal("abandoned authorized child on cancellation")
	case <-time.After(30 * time.Millisecond):
	}
	if _, e = pw.Write([]byte{'C'}); e != nil {
		t.Fatal(e)
	}
	select {
	case got := <-done:
		if got.err != nil || got.result.Code != 17 {
			t.Fatal("cancel changed application result", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original wait failed")
	}
}

func TestApplicationRunTerminalPublicationFailureRetainsObservedExit(t *testing.T) {
	o, _, _ := runFixture(t)
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	o.Argv = []string{self, "__hoopa_run_fixture", "terminal-conflict"}
	r, e := Run(context.Background(), o)
	if !errors.Is(e, ErrLaunchBookkeeping) || r.Code != 5 || r.Observed == nil || r.Observed.ExitCode == nil || *r.Observed.ExitCode != 17 || r.Observed.Signal != nil {
		t.Fatal("terminal write failure hid observed result", r.Code, e)
	}
	raw, e := os.ReadFile(filepath.Join(o.Paths.State, "app-launches", string(r.LaunchID), "terminal.json"))
	if e != nil || !bytes.Equal(raw, []byte("CONFLICT_ORIGINAL\n")) {
		t.Fatal("conflicting original overwritten", e)
	}
	if rows, e := config.ReadAppLaunchRecords(context.Background(), o.Paths); e == nil || rows != nil {
		t.Fatal("corrupt terminal became successful inventory")
	}
}

func TestApplicationHelperCannotExecuteBeforeExactGate(t *testing.T) {
	for _, gate := range []string{"", "GG", "X"} {
		t.Run(strconv.Itoa(len(gate))+gate, func(t *testing.T) {
			root := t.TempDir()
			sentinel := filepath.Join(root, "sentinel")
			self, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			payload, e := encodeLaunchPayload(launchPayload{Argv: []string{self, "__hoopa_run_fixture", "touch", sentinel}, Env: []string{"GORACE=atexit_sleep_ms=0"}})
			if e != nil {
				t.Fatal(e)
			}
			pr, pw, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			gr, gw, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			sr, sw, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			for _, f := range []*os.File{pr, pw, gr, gw, sr, sw} {
				defer f.Close()
			}
			fd := -1
			cmd := exec.Command(self, AppLauncherEntry)
			cmd.Env = []string{"GORACE=atexit_sleep_ms=0"}
			cmd.ExtraFiles = []*os.File{pr, gr, sw}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, PidFD: &fd}
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer syscall.Close(fd)
			defer func() { _ = signalOriginalChild(fd, syscall.SIGKILL) }()
			pr.Close()
			gr.Close()
			sw.Close()
			if _, e = pw.Write(payload); e != nil {
				t.Fatal(e)
			}
			pw.Close()
			var ready [1]byte
			if _, e = io.ReadFull(sr, ready[:]); e != nil || ready[0] != 'R' {
				t.Fatal("ready", e)
			}
			if _, e = os.Stat(sentinel); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("executed before gate")
			}
			if _, e = gw.Write([]byte(gate)); e != nil {
				t.Fatal(e)
			}
			gw.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e = observeChildExit(ctx, fd); e != nil {
				t.Fatal(e)
			}
			e = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 125 {
				t.Fatal("helper accepted bad gate", e)
			}
			raw, e := io.ReadAll(sr)
			if e != nil || !bytes.Equal(raw, []byte{'P'}) {
				t.Fatal("finite protocol error", e)
			}
			if _, e = os.Stat(sentinel); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("executed rejected payload")
			}
		})
	}
}
