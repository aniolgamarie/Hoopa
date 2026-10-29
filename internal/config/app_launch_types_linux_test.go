//go:build linux

package config

import (
	"bytes"
	"strings"
	"testing"
)

func appLaunchInt(v int) *int          { return &v }
func appLaunchString(v string) *string { return &v }
func appLaunchTypesFixture() (AppLaunchReservation, AppLaunchBirth, AppLaunchAuthorization, AppLaunchTerminal) {
	g := Generation("generation-a")
	r := AppLaunchReservation{SchemaVersion: 1, HistoryID: "apphist-" + strings.Repeat("a", 48), LaunchID: ID("launch-" + strings.Repeat("b", 48)), ObservedAt: "2026-10-29T12:00:00Z", Profile: AppLaunchProfile{Kind: "committed", Revision: "revision-a", IntentSHA256: strings.Repeat("c", 64), Generation: &g, ManifestSHA256: appLaunchString(strings.Repeat("d", 64)), App: App{ID: "app-a", Mode: "managed", Bypass: []string{}}}, Mode: "managed", EntryBindingSHA256: appLaunchString(strings.Repeat("e", 64)), Wrapper: AppLaunchProcess{PID: 100, UID: 1000, BootID: "12345678-1234-1234-1234-123456789abc", StartTicks: 123, PPID: 90, ProcessGroup: 100, Session: 90}}
	raw, _ := encode(r)
	rsha := digest(raw)
	b := AppLaunchBirth{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: "2026-10-29T12:00:01Z", ReservationSHA256: rsha, Child: AppLaunchProcess{PID: 101, UID: 1000, BootID: r.Wrapper.BootID, StartTicks: 124, PPID: 100, ProcessGroup: 101, Session: 90}}
	raw, _ = encode(b)
	bsha := digest(raw)
	a := AppLaunchAuthorization{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: "2026-10-29T12:00:02Z", ReservationSHA256: rsha, BirthSHA256: bsha}
	raw, _ = encode(a)
	asha := digest(raw)
	t := AppLaunchTerminal{SchemaVersion: 1, HistoryID: r.HistoryID, LaunchID: r.LaunchID, ObservedAt: "2026-10-29T12:00:03Z", ReservationSHA256: rsha, BirthSHA256: bsha, AuthorizationSHA256: &asha, Result: AppLaunchResult{ExitCode: appLaunchInt(17), WaitStatus: 17 << 8}}
	return r, b, a, t
}

func TestAppLaunchReservationProfileAndIdentityBounds(t *testing.T) {
	r, _, _, _ := appLaunchTypesFixture()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"direct", "inherit"} {
		v := r
		v.Mode = mode
		v.EntryBindingSHA256 = nil
		if err := v.Validate(); err != nil {
			t.Fatal("mode override", err)
		}
		v.Profile.Kind = "draft"
		v.Profile.Revision = "draft:" + v.Profile.IntentSHA256
		v.Profile.Generation = nil
		v.Profile.ManifestSHA256 = nil
		if err := v.Validate(); err != nil {
			t.Fatal("draft", err)
		}
	}
	changes := map[string]func(*AppLaunchReservation){
		"zero-version": func(r *AppLaunchReservation) { r.SchemaVersion = 0 }, "history": func(r *AppLaunchReservation) { r.HistoryID = "apphist-" + strings.Repeat("A", 48) }, "launch": func(r *AppLaunchReservation) { r.LaunchID = "launch-x" }, "offset": func(r *AppLaunchReservation) { r.ObservedAt = "2026-10-29T12:00:00+00:00" }, "zero-time": func(r *AppLaunchReservation) { r.ObservedAt = "0001-01-01T00:00:00Z" }, "parent": func(r *AppLaunchReservation) { r.Wrapper.PPID = 0 }, "pid": func(r *AppLaunchReservation) { r.Wrapper.PID = -1 }, "start": func(r *AppLaunchReservation) { r.Wrapper.StartTicks = 0 }, "boot": func(r *AppLaunchReservation) { r.Wrapper.BootID = "invalid" }, "group": func(r *AppLaunchReservation) { r.Wrapper.ProcessGroup = 0 }, "session": func(r *AppLaunchReservation) { r.Wrapper.Session = 0 }, "meter": func(r *AppLaunchReservation) { r.Metering = true }, "configured-meter": func(r *AppLaunchReservation) { r.Profile.App.Metering = true }, "mode": func(r *AppLaunchReservation) { r.Mode = "auto" }, "entry": func(r *AppLaunchReservation) { r.EntryBindingSHA256 = nil }, "draft-managed": func(r *AppLaunchReservation) { r.Profile.Kind = "draft" }, "intent": func(r *AppLaunchReservation) { r.Profile.IntentSHA256 = "bad" }, "manifest": func(r *AppLaunchReservation) { r.Profile.ManifestSHA256 = nil }, "app": func(r *AppLaunchReservation) { r.Profile.App.ID = "bad id" }, "nil-bypass": func(r *AppLaunchReservation) { r.Profile.App.Bypass = nil }, "bad-unicode": func(r *AppLaunchReservation) { r.Profile.App.Bypass = []string{string([]byte{255})} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r, _, _, _ := appLaunchTypesFixture()
			change(&r)
			if err := r.Validate(); err != ErrAppLaunch {
				t.Fatal("invalid reservation accepted", err)
			}
		})
	}
}

func TestAppLaunchBirthAuthorizationAndTerminalReferences(t *testing.T) {
	r, b, a, terminal := appLaunchTypesFixture()
	rraw, _ := encode(r)
	braw, _ := encode(b)
	araw, _ := encode(a)
	rsha, bsha, asha := digest(rraw), digest(braw), digest(araw)
	if b.Match(r, rsha) != nil || a.Match(r, rsha, b, bsha) != nil || terminal.Match(r, rsha, b, bsha, &a, &asha) != nil {
		t.Fatal("complete chain invalid")
	}
	// Wall-clock correction does not invalidate causal digest ordering.
	clock := a
	clock.ObservedAt = "2026-10-29T11:00:00Z"
	if clock.Match(r, rsha, b, bsha) != nil {
		t.Fatal("wall time treated as causal clock")
	}
	for _, field := range []string{"history", "launch", "parent", "uid", "boot", "session", "group", "same-pid", "reservation"} {
		t.Run(field, func(t *testing.T) {
			v := b
			switch field {
			case "history":
				v.HistoryID = "apphist-" + strings.Repeat("f", 48)
			case "launch":
				v.LaunchID = ID("launch-" + strings.Repeat("f", 48))
			case "parent":
				v.Child.PPID++
			case "uid":
				v.Child.UID++
			case "boot":
				v.Child.BootID = "22345678-1234-1234-1234-123456789abc"
			case "session":
				v.Child.Session++
			case "group":
				v.Child.ProcessGroup++
			case "same-pid":
				v.Child.PID = r.Wrapper.PID
			case "reservation":
				v.ReservationSHA256 = strings.Repeat("f", 64)
			}
			if v.Match(r, rsha) != ErrAppLaunch {
				t.Fatal("birth mismatch accepted")
			}
		})
	}
	for _, field := range []string{"reservation", "birth", "history", "launch"} {
		t.Run("auth-"+field, func(t *testing.T) {
			v := a
			switch field {
			case "reservation":
				v.ReservationSHA256 = strings.Repeat("f", 64)
			case "birth":
				v.BirthSHA256 = strings.Repeat("f", 64)
			case "history":
				v.HistoryID = "apphist-" + strings.Repeat("f", 48)
			case "launch":
				v.LaunchID = ID("launch-" + strings.Repeat("f", 48))
			}
			if v.Match(r, rsha, b, bsha) != ErrAppLaunch {
				t.Fatal("authorization mismatch accepted")
			}
		})
	}
	if terminal.Match(r, rsha, b, bsha, nil, nil) != ErrAppLaunch || terminal.Match(r, rsha, b, bsha, &a, nil) != ErrAppLaunch || terminal.Match(r, rsha, b, bsha, nil, &asha) != ErrAppLaunch {
		t.Fatal("authorization evidence mismatch accepted")
	}
	terminal.AuthorizationSHA256 = nil
	if terminal.Match(r, rsha, b, bsha, nil, nil) != nil {
		t.Fatal("preauthorization helper exit must be recordable")
	}
	terminal.Result.ExecFailureReason = appLaunchString("exec_format")
	terminal.Result.ExitCode = appLaunchInt(126)
	terminal.Result.WaitStatus = 126 << 8
	if terminal.Match(r, rsha, b, bsha, nil, nil) != ErrAppLaunch {
		t.Fatal("exec failure without authorization")
	}
}

func TestAppLaunchTerminalExactWaitAndFailureSemantics(t *testing.T) {
	for _, code := range []int{0, 1, 17, 126, 127, 130, 143, 255} {
		r := AppLaunchResult{ExitCode: appLaunchInt(code), WaitStatus: uint32(code) << 8}
		if r.Validate(true) != nil {
			t.Fatal(code)
		}
	}
	for _, signal := range []int{1, 2, 9, 15, 64} {
		for _, core := range []uint32{0, 128} {
			r := AppLaunchResult{Signal: appLaunchInt(signal), WaitStatus: uint32(signal) | core}
			if r.Validate(true) != nil {
				t.Fatal(signal, core)
			}
		}
	}
	for _, r := range []AppLaunchResult{{}, {ExitCode: appLaunchInt(130), Signal: appLaunchInt(2), WaitStatus: 130 << 8}, {ExitCode: appLaunchInt(-1)}, {ExitCode: appLaunchInt(256), WaitStatus: 256 << 8}, {ExitCode: appLaunchInt(130), WaitStatus: 2}, {Signal: appLaunchInt(2), WaitStatus: 130 << 8}, {Signal: appLaunchInt(2), WaitStatus: 2 | 1<<16}, {Signal: appLaunchInt(127), WaitStatus: 127}, {Signal: appLaunchInt(0)}, {ExitCode: appLaunchInt(0), WaitStatus: 0xffff}} {
		if r.Validate(true) != ErrAppLaunch {
			t.Fatal("invalid raw status accepted")
		}
	}
	for _, reason := range []string{"exec_format", "exec_permission", "exec_unavailable", "exec_not_found"} {
		code := 126
		if reason == "exec_not_found" {
			code = 127
		}
		r := AppLaunchResult{ExitCode: appLaunchInt(code), WaitStatus: uint32(code) << 8, ExecFailureReason: appLaunchString(reason)}
		if r.Validate(true) != nil || r.Validate(false) != ErrAppLaunch {
			t.Fatal("exec failure provenance gate")
		}
		r.ExitCode = appLaunchInt(17)
		r.WaitStatus = 17 << 8
		if r.Validate(true) != ErrAppLaunch {
			t.Fatal("wrong failure exit")
		}
	}
	for _, reason := range []string{"tty_restore_failed", "signal_forward_failed", "launcher_protocol_failed", "dispatch_aborted"} {
		r := AppLaunchResult{ExitCode: appLaunchInt(17), WaitStatus: 17 << 8, WrapperFailure: appLaunchString(reason)}
		if r.Validate(false) != nil {
			t.Fatal(reason)
		}
	}
	r := AppLaunchResult{ExitCode: appLaunchInt(17), WaitStatus: 17 << 8, WrapperFailure: appLaunchString("untrusted private failure")}
	if r.Validate(true) != ErrAppLaunch {
		t.Fatal("unbounded diagnostic accepted")
	}
}

func TestAppLaunchPrivateCodecExactOriginals(t *testing.T) {
	r, b, a, terminal := appLaunchTypesFixture()
	for _, v := range []any{r, b, a, terminal} {
		raw, err := encode(v)
		if err != nil || len(raw) > MaxAppLaunchRecordBytes {
			t.Fatal(err)
		}
		var out any
		switch v.(type) {
		case AppLaunchReservation:
			out = &AppLaunchReservation{}
		case AppLaunchBirth:
			out = &AppLaunchBirth{}
		case AppLaunchAuthorization:
			out = &AppLaunchAuthorization{}
		case AppLaunchTerminal:
			out = &AppLaunchTerminal{}
		}
		if err = decode(raw, out); err != nil {
			t.Fatal(err)
		}
		if err = decode(bytes.TrimSuffix(raw, []byte("\n")), out); err == nil {
			t.Fatal("alternate representation accepted")
		}
		duplicate := bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
		if err = decode(duplicate, out); err == nil {
			t.Fatal("duplicate accepted")
		}
	}
	r.Wrapper.StartTicks = ^uint64(0)
	raw, err := encode(r)
	if err != nil || !bytes.Contains(raw, []byte(`"start_ticks":"18446744073709551615"`)) {
		t.Fatal("integer ticks rounded", err)
	}
	var got AppLaunchReservation
	if err = decode(raw, &got); err != nil || got.Wrapper.StartTicks != r.Wrapper.StartTicks {
		t.Fatal(err)
	}
}
