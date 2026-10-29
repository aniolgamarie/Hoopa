//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

// Synthetic protected metadata only: no real process or managed entry is
// qualified or controlled by these projection tests.
func launchViewFixture(t *testing.T, mode string) (config.Paths, *config.AppLaunchWriter, config.AppLaunchReservation) {
	t.Helper()
	p := localInitPaths(t)
	options := localInitOptions(t, []config.App{{ID: "view-app", Mode: "direct", Bypass: []string{}}})
	report := localInitApply(t, p, options)
	profile := config.AppLaunchProfile{Kind: "draft", Revision: "draft:" + *report.DraftSHA256, IntentSHA256: *report.DraftSHA256, App: config.App{ID: "view-app", Mode: "direct", Bypass: []string{}}}
	var binding *string
	if mode == "managed" {
		g := config.Generation("synthetic-generation")
		sha := strings.Repeat("a", 64)
		entry := strings.Repeat("b", 64)
		profile.Kind = "committed"
		profile.Revision = "synthetic-revision"
		profile.Generation = &g
		profile.ManifestSHA256 = &sha
		binding = &entry
	}
	w, err := config.ReserveAppLaunch(context.Background(), p, config.AppLaunchInput{Profile: profile, Mode: mode, EntryBindingSHA256: binding, Wrapper: config.AppLaunchProcess{PID: os.Getpid(), UID: uint32(os.Geteuid()), BootID: "12345678-1234-1234-1234-123456789abc", StartTicks: 1, PPID: os.Getppid(), ProcessGroup: 1, Session: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r, err := w.TakeSpawn()
	if err != nil {
		t.Fatal(err)
	}
	return p, w, r
}

func TestLaunchReadProjectionHonestPrefixesAndUnknownDescendants(t *testing.T) {
	for _, mode := range []string{"managed", "direct", "inherit"} {
		t.Run(mode, func(t *testing.T) {
			p, w, r := launchViewFixture(t, mode)
			read := func() LaunchView {
				t.Helper()
				before := coordinatorTree(t, p)
				views, err := ReadLaunches(context.Background(), p)
				if err != nil || len(views) != 1 {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
					t.Fatal("projection wrote")
				}
				v := views[0]
				if v.LaunchID != r.LaunchID || v.AppID != "view-app" || v.Mode != mode || v.Metering || v.ProfileRevision != r.Profile.Revision || v.DescendantsState != "unknown" || v.DescendantsReason != "not_enumerated" {
					t.Fatal("projection fabricated metadata", v)
				}
				return v
			}
			v := read()
			if v.Root != nil || v.DispatchState != "not_authorized" {
				t.Fatal("reservation inferred child")
			}
			if mode == "managed" {
				if v.EntryLeaseState != "reserved" || v.EntryBindingSHA256 == nil || v.ProfileGeneration == nil {
					t.Fatal(v)
				}
			} else if v.EntryLeaseState != "none" || v.EntryBindingSHA256 != nil || v.ProfileGeneration != nil {
				t.Fatal(v)
			}
			child := r.Wrapper
			child.PID += 10000
			child.PPID = r.Wrapper.PID
			child.ProcessGroup = child.PID
			child.StartTicks++
			if err := w.RecordBirth(context.Background(), child); err != nil {
				t.Fatal(err)
			}
			v = read()
			if v.Root == nil || v.Root.PID != child.PID || v.Root.StartTicks != child.StartTicks || v.Root.ExitCode != nil || v.Root.WaitStatus != nil {
				t.Fatal("birth inferred completion")
			}
			if err := w.Authorize(context.Background()); err != nil {
				t.Fatal(err)
			}
			v = read()
			if v.DispatchState != "authorization_recorded" || mode == "managed" && v.EntryLeaseState != "unknown" {
				t.Fatal("authorization invented liveness")
			}
			exit := 130
			if err := w.RecordTerminal(context.Background(), config.AppLaunchResult{ExitCode: &exit, WaitStatus: 130 << 8}); err != nil {
				t.Fatal(err)
			}
			v = read()
			if v.Root.ExitCode == nil || *v.Root.ExitCode != 130 || v.Root.Signal != nil || v.Root.WaitStatus == nil || *v.Root.WaitStatus != 130<<8 || v.ExecFailureReason != nil {
				t.Fatal("normal130 misreported as SIGINT")
			}
		})
	}
}

func TestLaunchReadProjectionExecFailureRequiresStoredReason(t *testing.T) {
	for _, actualFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal126", true: "exec-format"}[actualFailure], func(t *testing.T) {
			p, w, r := launchViewFixture(t, "direct")
			child := r.Wrapper
			child.PID += 10000
			child.PPID = r.Wrapper.PID
			child.ProcessGroup = child.PID
			child.StartTicks++
			if err := w.RecordBirth(context.Background(), child); err != nil {
				t.Fatal(err)
			}
			if err := w.Authorize(context.Background()); err != nil {
				t.Fatal(err)
			}
			code := 126
			result := config.AppLaunchResult{ExitCode: &code, WaitStatus: 126 << 8}
			if actualFailure {
				reason := "exec_format"
				result.ExecFailureReason = &reason
			}
			if err := w.RecordTerminal(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			views, err := ReadLaunches(context.Background(), p)
			if err != nil || len(views) != 1 {
				t.Fatal(err)
			}
			v := views[0]
			if actualFailure {
				if v.DispatchState != "exec_failure_reported" || v.ExecFailureReason == nil || *v.ExecFailureReason != "exec_format" {
					t.Fatal(v)
				}
			} else if v.DispatchState != "authorization_recorded" || v.ExecFailureReason != nil {
				t.Fatal("126 fabricated an exec failure", v)
			}
		})
	}
}

func TestLaunchReadProjectionMissingAndCorruptReturnsNoPartialRows(t *testing.T) {
	p := localInitPaths(t)
	if views, err := ReadLaunches(context.Background(), p); err == nil || views != nil {
		t.Fatal("missing history became empty success")
	}
	if _, err := ReadLaunches(nil, p); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	p, w, r := launchViewFixture(t, "direct")
	w.Close()
	path := filepath.Join(p.State, "app-launches", string(r.LaunchID), "reservation.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	if views, err := ReadLaunches(context.Background(), p); err == nil || views != nil {
		t.Fatal("corrupt history returned rows")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("read repaired")
	}
}
