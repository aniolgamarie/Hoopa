//go:build linux && amd64

// Package runtime binds persisted launch material to independently verified
// processes. It does not publish active configuration or run recovery policy.
package runtime

import (
	"context"
	"errors"
	"hoopa/internal/p1testhook"
	"net"
	"os"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

var ErrOwnershipUnknown = errors.New("RUN_OWNERSHIP_UNVERIFIED")

// ControllerInspector must authenticate and verify the complete expected
// running configuration over conn. The caller already proved the owner of this
// EXACT connection, owns its lifetime, and rejects mismatched observations.
// Implementations must not reconnect, redirect or use an environment proxy.
// This is the T026 integration boundary, not permission to trust an API version.
type ControllerInspector interface {
	Inspect(context.Context, *net.TCPConn, config.RunIntent) (ControllerObservation, error)
}

type ControllerObservation struct {
	RunID         config.RunID
	Generation    config.Generation
	ConfigSHA256  string
	BinaryVersion string
	Gate          string
}

type Ownership struct {
	store      *config.Store
	controller ControllerInspector
}

func NewOwnership(store *config.Store, controller ControllerInspector) (*Ownership, error) {
	if store == nil || controller == nil {
		return nil, ErrOwnershipUnknown
	}
	return &Ownership{store: store, controller: controller}, nil
}

// OwnedRun holds the original pidfd from THIS observation. Its serialized
// identity is not authorization. Close releases the handle without stopping it.
type OwnedRun struct {
	process     *platform.ProcessHandle
	intent      config.RunIntent
	intentSHA   string
	birth       config.RunBirth
	birthSHA    string
	observation ControllerObservation
}

func (r *OwnedRun) Identity() platform.ProcessIdentity { return r.process.Identity() }
func (r *OwnedRun) Observation() ControllerObservation { return r.observation }
func (r *OwnedRun) Close() error                       { return r.process.Close() }

// DialOwnedTCP returns a connection whose server-side socket was proved owned
// by this observed original process. No credentials are sent here, and callers
// gain neither the process handle nor signal authority from this interface.
func (r *OwnedRun) DialOwnedTCP(ctx context.Context, address string) (*net.TCPConn, error) {
	if r == nil || r.process == nil || ctx == nil {
		return nil, ErrOwnershipUnknown
	}
	if err := r.process.VerifyArguments(ctx, r.intent.Arguments); err != nil {
		return nil, ErrOwnershipUnknown
	}
	conn, err := r.process.DialOwnedTCP(ctx, address)
	if err != nil {
		return nil, ErrOwnershipUnknown
	}
	if err := r.process.VerifyArguments(ctx, r.intent.Arguments); err != nil {
		conn.Close()
		return nil, ErrOwnershipUnknown
	}
	return conn, nil
}

func (o *Ownership) inspect(ctx context.Context, h *platform.ProcessHandle, intent config.RunIntent) (ControllerObservation, error) {
	var zero ControllerObservation
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := h.VerifyArguments(ctx, intent.Arguments); err != nil {
		return zero, p1ObservationFailure("arguments-before", intent.RunID, err)
	}
	conn, err := h.DialOwnedTCP(ctx, intent.Controller)
	if err != nil {
		return zero, p1ObservationFailure("dial-owned-controller", intent.RunID, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return zero, p1ObservationFailure("controller-deadline", intent.RunID, err)
	}
	observed, err := o.controller.Inspect(ctx, conn, intent)
	if err != nil {
		return zero, p1ObservationFailure("controller-inspect", intent.RunID, err)
	}
	if observed.RunID != intent.RunID || observed.Generation != intent.Generation || observed.ConfigSHA256 != intent.ConfigSHA256 || observed.BinaryVersion != intent.Binary.Version || observed.Gate != "closed" && observed.Gate != "open" {
		return zero, p1ObservationFailure("controller-identity", intent.RunID, ErrOwnershipUnknown)
	}
	if err = h.VerifyArguments(ctx, intent.Arguments); err != nil {
		return zero, p1ObservationFailure("arguments-after", intent.RunID, err)
	}
	return observed, nil
}

// Observe is read-only. Missing birth, a reused PID or inaccessible evidence is
// unknown, never "known stopped", and never permission to spawn a replacement.
func (o *Ownership) Observe(ctx context.Context, id config.RunID) (*OwnedRun, error) {
	if ctx == nil {
		return nil, ErrOwnershipUnknown
	}
	intent, intentSHA, err := o.store.ReadRunIntent(id)
	if err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, err)
	}
	birth, birthSHA, err := o.store.ReadRunBirth(id)
	if err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, err)
	}
	if err = birth.Match(intent, intentSHA); err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, err)
	}
	h, err := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, p1ObservationFailure("process", id, err))
	}
	kept := false
	defer func() {
		if !kept {
			h.Close()
		}
	}()
	identity := h.Identity()
	if identity.PID != birth.PID || identity.UID != birth.UID || identity.BootID != birth.BootID || identity.StartTicks != birth.StartTicks {
		return nil, ErrOwnershipUnknown
	}
	observed, err := o.inspect(ctx, h, intent)
	if err != nil {
		return nil, errors.Join(ErrOwnershipUnknown, err)
	}
	kept = true
	return &OwnedRun{process: h, intent: intent, intentSHA: intentSHA, birth: birth, birthSHA: birthSHA, observation: observed}, nil
}

// Register repeats actual observation before persisting this operation's first
// verification snapshot. First launch requires closed=true. A later recovery may
// observe an already-open core; an existing identical instance registration keeps
// its historical gate snapshot rather than being rewritten to today's state.
// Failure closes only the handle, preserving the process for explicit recovery.
func (o *Ownership) Register(ctx context.Context, id config.RunID, operation config.ID, requireClosed bool) (*OwnedRun, string, error) {
	if _, err := config.ParseID(string(operation)); err != nil {
		return nil, "", err
	}
	run, err := o.Observe(ctx, id)
	if err != nil {
		return nil, "", err
	}
	kept := false
	defer func() {
		if !kept {
			run.Close()
		}
	}()
	if requireClosed && run.observation.Gate != "closed" {
		return nil, "", ErrOwnershipUnknown
	}
	i, b := run.intent, run.birth
	reg := config.RunRegistration{Version: 1, RunID: id, OperationID: operation, IntentSHA256: run.intentSHA, BirthSHA256: run.birthSHA, PID: b.PID, UID: b.UID, BootID: b.BootID, StartTicks: b.StartTicks, Generation: i.Generation, ManifestSHA256: i.ManifestSHA256, BinarySHA256: i.Binary.SHA256, ConfigSHA256: i.ConfigSHA256, NonceSHA256: i.NonceSHA256, Controller: i.Controller, Gate: run.observation.Gate}
	// Keep original evidence for this operation after activate/reconnect. Reading
	// old metadata never replaces the fresh full Observe performed above.
	old, _, readErr := o.store.ReadRunRegistration(id, operation)
	if readErr == nil {
		current := reg
		current.Gate = old.Gate
		if current != old {
			return nil, "", config.ErrRunConflict
		}
		reg = old
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, "", errors.Join(ErrOwnershipUnknown, readErr)
	}
	// Only a missing registration NAME is eligible for creation. The Store
	// independently rechecks the complete intent/birth and private file boundary.
	// Even an identical retry passes through the Store's writer/poison guard.
	sha, err := o.store.PublishRunRegistration(reg)
	if err != nil {
		return nil, "", errors.Join(ErrOwnershipUnknown, err)
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("runtime", "registration-verified", string(operation), string(id), "", false, map[string]any{"registration_sha256": sha, "registration": reg, "identity": run.Identity(), "new_name": errors.Is(readErr, os.ErrNotExist)})
	}
	kept = true
	return run, sha, nil
}
