//go:build linux && amd64

package control

import (
	"context"
	"hoopa/internal/config"
)

type LaunchView struct {
	LaunchID           config.ID
	AppID              config.ID
	Mode               string
	Metering           bool
	ProfileRevision    string
	ProfileGeneration  *config.ID
	EntryBindingSHA256 *string
	DispatchState      string
	ExecFailureReason  *string
	Root               *LaunchRootView
	DescendantsState   string
	DescendantsReason  string
	EntryLeaseState    string
}
type LaunchRootView struct {
	PID        int
	BootID     string
	StartTicks uint64
	ExitCode   *int
	Signal     *int
	WaitStatus *uint32
}

// ReadLaunches is a protected local metadata projection, not a process census or
// management execution API. It never repairs, signals, replays or frees leases.
func ReadLaunches(ctx context.Context, paths config.Paths) ([]LaunchView, error) {
	if ctx == nil {
		return nil, ErrRequest
	}
	records, err := config.ReadAppLaunchRecords(ctx, paths)
	if err != nil {
		return nil, err
	}
	views := make([]LaunchView, 0, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r := record.Reservation
		view := LaunchView{LaunchID: r.LaunchID, AppID: r.Profile.App.ID, Mode: r.Mode, Metering: r.Metering, ProfileRevision: r.Profile.Revision, DispatchState: "not_authorized", DescendantsState: "unknown", DescendantsReason: "not_enumerated", EntryLeaseState: "none"}
		if r.Profile.Generation != nil {
			generation := config.ID(*r.Profile.Generation)
			view.ProfileGeneration = &generation
		}
		if r.EntryBindingSHA256 != nil {
			sha := *r.EntryBindingSHA256
			view.EntryBindingSHA256 = &sha
		}
		if r.Mode == "managed" {
			view.EntryLeaseState = "reserved"
		}
		if record.Birth != nil {
			b := record.Birth.Child
			view.Root = &LaunchRootView{PID: b.PID, BootID: b.BootID, StartTicks: b.StartTicks}
		}
		if record.Authorization != nil {
			view.DispatchState = "authorization_recorded"
		}
		if record.Terminal != nil {
			result := record.Terminal.Result
			if result.ExitCode != nil {
				code := *result.ExitCode
				view.Root.ExitCode = &code
			}
			if result.Signal != nil {
				signal := *result.Signal
				view.Root.Signal = &signal
			}
			status := result.WaitStatus
			view.Root.WaitStatus = &status
			if result.ExecFailureReason != nil {
				reason := *result.ExecFailureReason
				view.ExecFailureReason = &reason
				view.DispatchState = "exec_failure_reported"
			}
		}
		if r.Mode == "managed" && (record.Authorization != nil || record.Terminal != nil) {
			view.EntryLeaseState = "unknown"
		}
		views = append(views, view)
	}
	return views, nil
}
