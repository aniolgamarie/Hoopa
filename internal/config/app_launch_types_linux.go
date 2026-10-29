//go:build linux

package config

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxAppLaunchRecordBytes = 64 << 10
const MaxAppLaunches = 4096

var (
	ErrAppLaunch         = errors.New("APP_LAUNCH_INVALID")
	ErrAppLaunchBusy     = errors.New("APP_LAUNCH_BUSY")
	ErrAppLaunchCapacity = errors.New("APP_LAUNCH_CAPACITY_EXHAUSTED")
	ErrAppLaunchConflict = errors.New("APP_LAUNCH_RECORD_CONFLICT")
)

// AppLaunchProcess is historical birth metadata, never a signal/adoption handle.
type AppLaunchProcess struct {
	PID          int    `json:"pid"`
	UID          uint32 `json:"uid"`
	BootID       string `json:"boot_id"`
	StartTicks   uint64 `json:"start_ticks,string"`
	PPID         int    `json:"ppid"`
	ProcessGroup int    `json:"process_group"`
	Session      int    `json:"session"`
}

type AppLaunchProfile struct {
	Kind           string      `json:"kind"`
	Revision       string      `json:"revision"`
	IntentSHA256   string      `json:"intent_sha256"`
	Generation     *Generation `json:"generation"`
	ManifestSHA256 *string     `json:"manifest_sha256"`
	App            App         `json:"app"`
}

type AppLaunchReservation struct {
	SchemaVersion      int              `json:"schema_version"`
	HistoryID          string           `json:"history_id"`
	LaunchID           ID               `json:"launch_id"`
	ObservedAt         string           `json:"observed_at"`
	Profile            AppLaunchProfile `json:"profile"`
	Mode               string           `json:"mode"`
	Metering           bool             `json:"metering"`
	EntryBindingSHA256 *string          `json:"entry_binding_sha256"`
	Wrapper            AppLaunchProcess `json:"wrapper"`
}

type AppLaunchBirth struct {
	SchemaVersion     int              `json:"schema_version"`
	HistoryID         string           `json:"history_id"`
	LaunchID          ID               `json:"launch_id"`
	ObservedAt        string           `json:"observed_at"`
	ReservationSHA256 string           `json:"reservation_sha256"`
	Child             AppLaunchProcess `json:"child"`
}

type AppLaunchAuthorization struct {
	SchemaVersion     int    `json:"schema_version"`
	HistoryID         string `json:"history_id"`
	LaunchID          ID     `json:"launch_id"`
	ObservedAt        string `json:"observed_at"`
	ReservationSHA256 string `json:"reservation_sha256"`
	BirthSHA256       string `json:"birth_sha256"`
}

type AppLaunchResult struct {
	ExitCode          *int    `json:"exit_code"`
	Signal            *int    `json:"signal"`
	WaitStatus        uint32  `json:"wait_status"`
	ExecFailureReason *string `json:"exec_failure_reason"`
	WrapperFailure    *string `json:"wrapper_failure"`
}

type AppLaunchTerminal struct {
	SchemaVersion       int             `json:"schema_version"`
	HistoryID           string          `json:"history_id"`
	LaunchID            ID              `json:"launch_id"`
	ObservedAt          string          `json:"observed_at"`
	ReservationSHA256   string          `json:"reservation_sha256"`
	BirthSHA256         string          `json:"birth_sha256"`
	AuthorizationSHA256 *string         `json:"authorization_sha256"`
	Result              AppLaunchResult `json:"result"`
}

// AppLaunchRecord is a validated immutable prefix, not a liveness assertion.
// Optional records are absent, never synthesized from wrapper/owner-lock state.
type AppLaunchRecord struct {
	Reservation         AppLaunchReservation
	ReservationSHA256   string
	Birth               *AppLaunchBirth
	BirthSHA256         *string
	Authorization       *AppLaunchAuthorization
	AuthorizationSHA256 *string
	Terminal            *AppLaunchTerminal
	TerminalSHA256      *string
}

func appLaunchToken(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	token := strings.TrimPrefix(value, prefix)
	b, err := hex.DecodeString(token)
	return err == nil && len(b) == 24 && hex.EncodeToString(b) == token
}

func appLaunchTimestamp(value string) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !t.IsZero() && t.UTC().Format(time.RFC3339Nano) == value
}

func appLaunchHeader(version int, history string, id ID, observed string) bool {
	return version == 1 && appLaunchToken(history, "apphist-") && appLaunchToken(string(id), "launch-") && appLaunchTimestamp(observed)
}

func (p AppLaunchProcess) valid() bool {
	return runIdentity(p.PID, p.BootID, p.StartTicks) && p.PPID > 0 && p.PPID <= 2147483647 && p.ProcessGroup > 0 && p.ProcessGroup <= 2147483647 && p.Session > 0 && p.Session <= 2147483647
}

func (p AppLaunchProfile) valid() bool {
	if !runIDs(p.Revision, string(p.App.ID)) || !isDigest(p.IntentSHA256) || p.App.Metering || p.App.Mode != "managed" && p.App.Mode != "inherit" && p.App.Mode != "direct" || p.App.Bypass == nil || len(p.App.Bypass) > MaxAppLaunchRecordBytes {
		return false
	}
	// Preserve the captured declaration without interpreting bypass for modes
	// that do not use it. The managed environment performs its own exact grammar.
	total := 0
	for _, token := range p.App.Bypass {
		if !utf8.ValidString(token) || len(token) > MaxAppLaunchRecordBytes {
			return false
		}
		total += len(token) + 1
		if total > MaxAppLaunchRecordBytes {
			return false
		}
	}
	switch p.Kind {
	case "draft":
		return p.Revision == "draft:"+p.IntentSHA256 && p.Generation == nil && p.ManifestSHA256 == nil
	case "committed":
		return p.Generation != nil && runIDs(string(*p.Generation)) && p.ManifestSHA256 != nil && isDigest(*p.ManifestSHA256)
	default:
		return false
	}
}

func (r AppLaunchReservation) Validate() error {
	if !appLaunchHeader(r.SchemaVersion, r.HistoryID, r.LaunchID, r.ObservedAt) || !r.Profile.valid() || !r.Wrapper.valid() || r.Metering {
		return ErrAppLaunch
	}
	switch r.Mode {
	case "managed":
		if r.Profile.Kind != "committed" || r.EntryBindingSHA256 == nil || !isDigest(*r.EntryBindingSHA256) {
			return ErrAppLaunch
		}
	case "direct", "inherit":
		if r.EntryBindingSHA256 != nil {
			return ErrAppLaunch
		}
	default:
		return ErrAppLaunch
	}
	return nil
}

func (b AppLaunchBirth) Match(r AppLaunchReservation, sha string) error {
	if r.Validate() != nil || !isDigest(sha) || !appLaunchHeader(b.SchemaVersion, b.HistoryID, b.LaunchID, b.ObservedAt) || b.HistoryID != r.HistoryID || b.LaunchID != r.LaunchID || b.ReservationSHA256 != sha || !b.Child.valid() || b.Child.PID == r.Wrapper.PID || b.Child.PPID != r.Wrapper.PID || b.Child.UID != r.Wrapper.UID || b.Child.BootID != r.Wrapper.BootID || b.Child.Session != r.Wrapper.Session || b.Child.ProcessGroup != b.Child.PID {
		return ErrAppLaunch
	}
	return nil
}

func (a AppLaunchAuthorization) Match(r AppLaunchReservation, reservationSHA string, b AppLaunchBirth, birthSHA string) error {
	if b.Match(r, reservationSHA) != nil || !isDigest(birthSHA) || !appLaunchHeader(a.SchemaVersion, a.HistoryID, a.LaunchID, a.ObservedAt) || a.HistoryID != r.HistoryID || a.LaunchID != r.LaunchID || a.ReservationSHA256 != reservationSHA || a.BirthSHA256 != birthSHA {
		return ErrAppLaunch
	}
	return nil
}

func (r AppLaunchResult) Validate(authorized bool) error {
	if (r.ExitCode == nil) == (r.Signal == nil) {
		return ErrAppLaunch
	}
	if r.ExitCode != nil {
		if *r.ExitCode < 0 || *r.ExitCode > 255 || r.WaitStatus != uint32(*r.ExitCode)<<8 {
			return ErrAppLaunch
		}
	} else if *r.Signal < 1 || *r.Signal > 64 || r.WaitStatus&^uint32(0xff) != 0 || r.WaitStatus&0x7f != uint32(*r.Signal) {
		return ErrAppLaunch
	}
	if r.ExecFailureReason != nil {
		if !authorized || r.ExitCode == nil {
			return ErrAppLaunch
		}
		want := 126
		switch *r.ExecFailureReason {
		case "exec_format", "exec_permission", "exec_unavailable":
		case "exec_not_found":
			want = 127
		default:
			return ErrAppLaunch
		}
		if *r.ExitCode != want {
			return ErrAppLaunch
		}
	}
	if r.WrapperFailure != nil {
		switch *r.WrapperFailure {
		case "tty_restore_failed", "signal_forward_failed", "launcher_protocol_failed", "dispatch_aborted":
		default:
			return ErrAppLaunch
		}
	}
	return nil
}

func (t AppLaunchTerminal) Match(r AppLaunchReservation, reservationSHA string, b AppLaunchBirth, birthSHA string, a *AppLaunchAuthorization, authorizationSHA *string) error {
	if b.Match(r, reservationSHA) != nil || !isDigest(birthSHA) || !appLaunchHeader(t.SchemaVersion, t.HistoryID, t.LaunchID, t.ObservedAt) || t.HistoryID != r.HistoryID || t.LaunchID != r.LaunchID || t.ReservationSHA256 != reservationSHA || t.BirthSHA256 != birthSHA || (a == nil) != (authorizationSHA == nil) || (a == nil) != (t.AuthorizationSHA256 == nil) {
		return ErrAppLaunch
	}
	if a != nil && (!isDigest(*authorizationSHA) || a.Match(r, reservationSHA, b, birthSHA) != nil || *authorizationSHA != *t.AuthorizationSHA256) {
		return ErrAppLaunch
	}
	return t.Result.Validate(a != nil)
}
