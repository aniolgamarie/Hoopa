//go:build linux && amd64 && us4integration && onboardingintegration && us2integration

package integration

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestLaunchLiteralArgvPipedIOAndPrivateHistory(t *testing.T) {
	h := newLaunchHarness(t)
	defer h.finish()
	// Use the committed profile offline; these overrides require neither core
	// nor management. This is not the separate T054 draft initialization path.
	_, core := h.r.h.run()
	h.r.cli.ok(nil, "stop", "--accept-disruption", "--request-id", "literal-offline-stop")
	h.r.h.exit(core)
	h.r.stopService()
	parent := os.Environ()
	stable := h.stable()
	sideEffect := filepath.Join(h.r.h.root, "shell-side-effect")
	secret := "T053_PRIVATE_ARGV_725cb2f909"
	args := []string{"", "two words", "quoted'\"", "; touch " + sideEffect, "$(touch " + sideEffect + ")", "*", "--app", "other", "中文", "line\nbreak", secret}
	input := []byte{0, 1, 'x', '\n', 255}
	for _, mode := range []string{"direct", "inherit"} {
		before := h.views()
		p := h.start(mode, false, "argv", args...)
		p.write(input)
		p.closeInput()
		p.wait(17)
		var output struct {
			Event string   `json:"event"`
			Args  []string `json:"args"`
			Input string   `json:"stdin"`
			Env   string   `json:"environment_marker"`
			Lower string   `json:"proxy_lower"`
			Upper string   `json:"proxy_upper"`
		}
		appMust(t, json.Unmarshal(p.stdout(), &output))
		if output.Event != "argv" || !reflect.DeepEqual(output.Args, args) || output.Input != base64.StdEncoding.EncodeToString(input) || output.Env != launchEnvSecret {
			t.Fatal("actual child argv/stdin/env changed or wrapped")
		}
		if !bytes.Equal(p.stderr(), []byte("APP_STDERR_EXACT\n")) {
			t.Fatal("application stderr changed or Hoopa mixed diagnostics")
		}
		if mode == "direct" && (output.Lower != "" || output.Upper != "") {
			t.Fatal("direct did not clear child proxy input")
		}
		if mode == "inherit" && (output.Lower != "parent-private-lower" || output.Upper != "parent-private-upper") {
			t.Fatal("inherit did not preserve supplied parent values")
		}
		view := h.newView(before)
		h.normal(view, 17, mode)
	}
	// This existing executable has valid shell text but no executable format
	// or shebang. It must reach the fixed launcher's syscall.Exec and report
	// ENOEXEC, never a parent shell fallback or a claim that its main ran.
	invalidTarget := filepath.Join(h.r.h.root, "invalid-executable-format")
	execSentinel := filepath.Join(h.r.h.root, "exec-format-shell-sentinel")
	content := "printf 'MUST_NOT_EXECUTE' > '" + execSentinel + "'\nexit 17\n"
	appMust(t, os.WriteFile(invalidTarget, []byte(content), 0700))
	appMust(t, os.Chmod(invalidTarget, 0700))
	info, err := os.Stat(invalidTarget)
	appMust(t, err)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0700 {
		t.Fatal("invalid-format fixture is not an existing0700 executable")
	}
	beforeExec := h.views()
	failedExec := h.process(h.r.cli.path, []string{"run", "--app", "app-a", "--mode", "direct", "--", invalidTarget}, 0)
	failedExec.closeInput()
	failedExec.wait(126)
	failedExec.drain()
	if len(failedExec.stdout()) != 0 || !bytes.Equal(failedExec.stderr(), []byte("hoopa: EXEC_FORMAT\n")) {
		t.Fatal("exec-format failure was not exact empty-stdout finite diagnostic")
	}
	h.execFormat(h.newView(beforeExec))
	if _, err := os.Lstat(execSentinel); !os.IsNotExist(err) {
		t.Fatal("invalid target was executed by a shell fallback")
	}
	if _, err := os.Lstat(sideEffect); !os.IsNotExist(err) {
		t.Fatal("literal argv executed shell syntax")
	}
	if !reflect.DeepEqual(parent, os.Environ()) || !reflect.DeepEqual(stable, h.stable()) {
		t.Fatal("run changed parent environment or unrelated configuration/core inputs")
	}
	h.noPrivatePersisted(secret, launchEnvSecret)
}

func TestLaunchOriginalChildSignalsAndNormal130AreDistinct(t *testing.T) {
	h := newLaunchHarness(t)
	defer h.finish()
	before := h.views()
	normal := h.start("direct", false, "exit", "130")
	normal.closeInput()
	normal.event("fixture_ready")
	normal.wait(130)
	h.normal(h.newView(before), 130, "direct")
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		before = h.views()
		p := h.start("direct", false, "wait")
		ready := p.event("fixture_ready")
		pid := launchInt(t, ready, "pid")
		root := launchObserve(t, pid)
		var sentinel *launchProcess
		if sig == syscall.SIGTERM {
			sentinel = h.sentinel(pid)
			sentinelReady := sentinel.event("fixture_ready")
			if launchInt(t, sentinelReady, "pgrp") != pid {
				t.Fatal("sentinel did not join the original child group")
			}
		}
		p.signal(sig)
		p.wait(128 + int(sig))
		root.exited(t, 4*time.Second)
		view := h.newView(before)
		h.signaled(view, int(sig), "direct")
		if view.Root.PID != pid || view.Root.StartTicks != root.start {
			t.Fatal("terminal record changed original child incarnation")
		}
		if sentinel != nil {
			if sentinel.handle.ready() {
				t.Fatal("signal broadcast reached independently owned same-group sentinel")
			}
			sentinel.wait(0)
		}
	}
}

func TestLaunchControlledPTYReadWriteCtrlCAndForegroundRestore(t *testing.T) {
	h := newLaunchHarness(t)
	defer h.finish()
	for _, mode := range []string{"tty-normal", "tty-wait"} {
		before := h.views()
		report := h.pty(mode)
		if !report.TTY[0] || !report.TTY[1] || !report.TTY[2] || report.ChildForeground != report.RootPID || report.AfterForeground != report.WrapperPID || !report.TermiosUnchanged || !report.WrapperUnreaped {
			t.Fatal("actual PTY/session/foreground evidence incomplete")
		}
		if !bytes.Contains(report.Raw, []byte("TTY_STDERR_TOKEN")) {
			t.Fatal("actual PTY stderr not observed")
		}
		view := h.newView(before)
		if view.Root == nil || view.Root.PID != report.RootPID || view.Root.StartTicks != report.RootStartTicks {
			t.Fatal("PTY root metadata not original application")
		}
		if mode == "tty-normal" {
			if report.WrapperExit != 17 || !launchPTYReadWitness(report.Raw) {
				t.Fatal("PTY input/exit changed")
			}
			h.normal(view, 17, "direct")
		} else {
			if report.WrapperExit != 130 {
				t.Fatal("Ctrl-C exit mapping")
			}
			h.signaled(view, int(syscall.SIGINT), "direct")
		}
	}
}

func TestLaunchRootExitKeepsUnknownDescendantAndManagedLease(t *testing.T) {
	h := newLaunchHarness(t)
	defer h.finish()
	before := h.views()
	path := "/launch-background-before"
	token := h.r.h.trace.token("app-a", path, []string{"A-US"})
	socket := filepath.Join(h.r.h.root, "descendant.sock")
	p := h.start("managed", false, "background", h.target(), token, path, socket)
	rootReady := p.event("fixture_ready")
	rootHandle := launchObserve(t, launchInt(t, rootReady, "pid"))
	daemonReady := p.event("daemon_ready")
	grand := launchObserve(t, launchInt(t, daemonReady, "pid"))
	p.traffic("A-US:" + path)
	h.r.h.trace.success(t, token)
	p.write([]byte("EXIT\n"))
	p.wait(17)
	if grand.ready() || p.stdoutEnded() {
		t.Fatal("wrapper completion depended on descendant EOF or background witness already exited")
	}
	view := h.newView(before)
	h.normal(view, 17, "managed")
	if view.Root.PID != launchInt(t, rootReady, "pid") || view.Root.StartTicks != rootHandle.start || view.Root.PID == launchInt(t, daemonReady, "pid") || view.EntryLeaseState != "unknown" {
		t.Fatal("root result conflated descendants/managed lease completion")
	}
	path = "/launch-background-after-root"
	token = h.r.h.trace.token("app-a", path, []string{"A-US"})
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	appMust(t, err)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	appMust(t, json.NewEncoder(conn).Encode(map[string]string{"Token": token, "Path": path}))
	var response struct {
		Body string `json:"body"`
	}
	appMust(t, json.NewDecoder(conn).Decode(&response))
	conn.Close()
	if response.Body != "A-US:"+path {
		t.Fatal("known surviving descendant lost its existing entry")
	}
	h.r.h.trace.success(t, token)
	if !reflect.DeepEqual(view, h.view(view.LaunchID)) {
		t.Fatal("independent fixture knowledge silently rewrote product descendant/lease state")
	}
	grand.exited(t, 22*time.Second)
	grand.reapAdopted(t)
	p.drain()
}

func TestLaunchManagedRunUsesExistingEntryWithoutReloadOrMigration(t *testing.T) {
	h := newLaunchHarness(t)
	defer h.finish()
	stable := h.stable()
	runID, core := h.r.h.run()
	firstViews := h.views()
	path := "/launch-old-first"
	token := h.r.h.trace.token("app-a", path, []string{"A-US"})
	old := h.start("managed", false, "held", h.target(), token, path)
	old.event("fixture_ready")
	old.traffic("A-US:" + path)
	h.r.h.trace.success(t, token)
	oldView := h.newView(firstViews)
	if oldView.DispatchState != "authorization_recorded" || oldView.ExecFailureReason != nil {
		t.Fatal("known fixture execution lacks recorded authorization")
	}
	path = "/launch-new-b"
	token = h.r.h.trace.token("app-b", path, []string{"B-JP"})
	beforeNext := h.views()
	next := h.startApp("app-b", "managed", false, "held", h.target(), token, path)
	next.event("fixture_ready")
	next.traffic("B-JP:" + path)
	h.r.h.trace.success(t, token)
	next.write([]byte("EXIT\n"))
	next.wait(17)
	nextView := h.newView(beforeNext)
	if nextView.AppID != "app-b" || nextView.DispatchState != "authorization_recorded" || nextView.ExecFailureReason != nil {
		t.Fatal("successful second application has incorrect dispatch/exec-failure metadata")
	}
	marker := filepath.Join(h.r.h.root, "must-not-dispatch")
	for _, test := range []struct {
		app, mode string
		meter     bool
		code      int
		stderr    string
	}{
		{"missing-app", "managed", false, 4, "hoopa: APP_NOT_FOUND\n"},
		{"app-a", "managed", true, 2, "hoopa: METERING_UNSUPPORTED\n"},
		{"app-a", "direct", true, 2, "hoopa: METERING_MODE_INVALID\n"},
		{"app-a", "inherit", true, 2, "hoopa: METERING_MODE_INVALID\n"},
		{"app-a", "bogus", false, 2, "hoopa: INVALID_ARGUMENT\n"},
	} {
		h.reject(test.app, test.mode, test.meter, test.code, test.stderr, marker)
	}

	if !reflect.DeepEqual(stable, h.stable()) || !reflect.DeepEqual(oldView, h.view(oldView.LaunchID)) {
		t.Fatal("new launch/refusal changed core configuration or prior launch")
	}
	id, now := h.r.h.run()
	if id != runID || now.Identity() != core.Identity() {
		t.Fatal("run implicitly reloaded/replaced core")
	}
	path = "/launch-old-still-bound"
	token = h.r.h.trace.token("app-a", path, []string{"A-US"})
	old.write([]byte("GET " + token + " " + path + "\n"))
	old.traffic("A-US:" + path)
	h.r.h.trace.success(t, token)
	// Service unavailability is a missing qualification, even while the old
	// entry/core continue. A new managed launch must not auto-bootstrap/fallback.
	h.r.stopService()
	h.reject("app-a", "managed", false, 4, "hoopa: MANAGED_ENTRY_UNAVAILABLE\n", marker)
	h.r.start()
	old.write([]byte("EXIT\n"))
	old.wait(17)
	h.r.cli.ok(nil, "stop", "--accept-disruption", "--request-id", "launch-explicit-stop")
	h.r.h.exit(core)
	h.reject("app-a", "managed", false, 4, "hoopa: MANAGED_ENTRY_UNAVAILABLE\n", marker) // bound listener is now unavailable; no auto-start
}

// The metadata checks above are qualified root/entry facts, not connector PID
// attribution. Explicit settings/app binding/history initialization is fixture
// preparation; it does not claim the future hoopa init/draft onboarding path.

func launchPTYReadWitness(raw []byte) bool {
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var event struct {
			Event string `json:"event"`
			Line  string `json:"line"`
		}
		if json.Unmarshal(bytes.TrimSpace(line), &event) == nil && event.Event == "tty_input" && event.Line == "PTY_INPUT\n" {
			return true
		}
	}
	return false
}
