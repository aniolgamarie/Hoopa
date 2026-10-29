//go:build linux && amd64

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

type basicLauncherReport struct {
	PID, SID, Group                     int
	BirthFDClosed, NullStdin, SharedLog bool
	NonceSHA                            string
	Environment                         []string
}

func TestBasicLauncherDispatch(t *testing.T) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	} // Empty parent helper.
	fd := os.NewFile(3, "birth")
	intent, err := ClaimLauncher(context.Background(), fd, os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE"))
	if err != nil {
		t.Fatal(err)
	}
	_, e := fd.Stat()
	sid, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	si, err := os.Stdin.Stat()
	if err != nil {
		t.Fatal(err)
	}
	null, err := os.Stat("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := os.Stdout.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Stderr.Stat()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Stat("launcher.log")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, v := range os.Environ() {
		keys = append(keys, strings.SplitN(v, "=", 2)[0])
	}
	sort.Strings(keys)
	report := basicLauncherReport{PID: os.Getpid(), SID: int(sid), Group: syscall.Getpgrp(), BirthFDClosed: errors.Is(e, os.ErrClosed), NullStdin: len(input) == 0 && os.SameFile(si, null), SharedLog: os.SameFile(stdout, stderr) && os.SameFile(stdout, log) && log.Mode().Perm() == 0600, NonceSHA: ownershipDigest([]byte(os.Getenv("HOOPA_RUN_NONCE"))), Environment: keys}
	if report.NonceSHA != intent.NonceSHA256 {
		t.Fatal("nonce not bound")
	}
	raw, _ := json.Marshal(report)
	if err = os.WriteFile("launcher-report.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("BASIC_LAUNCHER_READY pid=%d birth_closed=%t session=%d\n", report.PID, report.BirthFDClosed, report.SID)
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err = os.Stat("launcher-stop"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture parent did not release")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestBasicLauncherStartAndWait(t *testing.T) {
	zeroCtx, zeroCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer zeroCancel()
	var zero LaunchChild
	if err := zero.Wait(zeroCtx); !errors.Is(err, ErrOwnershipUnknown) {
		t.Fatal("zero child Wait accepted", err)
	}
	if err := (*LaunchChild)(nil).Wait(zeroCtx); !errors.Is(err, ErrOwnershipUnknown) {
		t.Fatal("nil child Wait accepted", err)
	}
	f, input := prepareLaunchFixture(t)
	bundle, err := f.store.ReadBundle(input.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(input.Workdir, "config.json"), bundle.Files["core.yaml"], 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	intent := prepared.Intent()
	if got, _, e := f.store.ReadRunIntent(intent.RunID); e != nil || !reflect.DeepEqual(got, intent) {
		t.Fatal("launch intent not durable", e)
	}
	args := []string{"-test.run=^TestBasicLauncherDispatch$", "-test.timeout=12s"}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, entry := range [][]string{nil, make([]string, 17), {strings.Repeat("x", 257)}, {"nul\x00arg"}} {
		if child, e := StartBasicLauncher(context.Background(), prepared, entry); child != nil || e == nil {
			t.Fatal("bad dispatch accepted", e)
		}
	}
	if child, e := StartBasicLauncher(canceled, prepared, args); child != nil || e == nil {
		t.Fatal("canceled start accepted", e)
	}
	child, err := StartBasicLauncher(context.Background(), prepared, args)
	if err != nil || child == nil {
		t.Fatal(err)
	}
	// This child is bounded independently. Releasing its private fixture file
	// lets it exit normally; no unverified numeric-PID signal is used.
	t.Cleanup(func() {
		os.WriteFile(filepath.Join(input.Workdir, "launcher-stop"), nil, 0600)
		ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if e := child.Wait(ctx); e != nil {
			t.Error("launcher cleanup Wait", e)
		}
		if log, e := os.ReadFile(filepath.Join(input.Workdir, "launcher.log")); e == nil {
			t.Logf("actual launcher original output:\n%s", log)
		}
		t.Logf("actual launcher child PID=%d normal fixture release and Wait; not Mihomo", child.PID())
	})
	var report basicLauncherReport
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, e := os.ReadFile(filepath.Join(input.Workdir, "launcher-report.json"))
		if e == nil {
			if e = json.Unmarshal(raw, &report); e != nil {
				t.Fatal(e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("launcher report timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	keys := []string{"HOME", "HOOPA_LAUNCH_INTENT_SHA256", "HOOPA_LAUNCH_PROTOCOL", "HOOPA_RUN_NONCE", "LANG", "TMPDIR"}
	if report.PID != child.PID() || report.SID != child.PID() || report.Group != child.PID() || !report.BirthFDClosed || !report.NullStdin || !report.SharedLog || report.NonceSHA != intent.NonceSHA256 || !reflect.DeepEqual(report.Environment, keys) {
		t.Fatal("launcher environment/session/fd/stdio binding failed")
	}
	birth, _, err := f.store.ReadRunBirth(intent.RunID)
	if err != nil || birth.PID != child.PID() {
		t.Fatal("actual birth missing", err)
	}
	h, err := platform.ObserveProcess(ownershipContext(t), child.PID(), platform.ProcessSpec{BinaryPath: input.Binary.Path, BinarySHA256: input.Binary.SHA256, Workdir: input.Workdir, NonceSHA256: intent.NonceSHA256})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err = h.VerifyArguments(ownershipContext(t), append([]string{input.Binary.Path}, args...)); err != nil {
		t.Fatal("self launcher argv", err)
	}
	wait, c := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer c()
	if e := child.Wait(wait); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("Wait cancellation unexpected", e)
	}
	if err = h.VerifyArguments(ownershipContext(t), append([]string{input.Binary.Path}, args...)); err != nil {
		t.Fatal("Wait cancellation killed child", err)
	}
	if again, e := StartBasicLauncher(context.Background(), prepared, args); again != nil || e == nil {
		t.Fatal("one-use launch restarted", e)
	}
	if fd, _, nonce, e := prepared.TakeLauncherInput(); fd != nil || nonce != "" || !errors.Is(e, config.ErrRunConflict) {
		t.Fatal("consumed ticket reused", e)
	}
	if s := fmt.Sprintf("%v %#v %v %#v", *child, *child, child, child); strings.Contains(s, prepared.nonce) {
		t.Fatal("child representation leaked nonce")
	}
}
