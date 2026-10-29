//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"syscall"

	"hoopa/internal/config"
)

// LaunchExitProof records a direct child's original cmd.Wait result. It is not
// observation/signal authority, a reusable launch ticket or proof of a core's
// handshake/ownership. Matching expects SHA values of protected original bytes.
type LaunchExitProof struct {
	Version      int          `json:"version"`
	Kind         string       `json:"kind"`
	RunID        config.RunID `json:"run_id"`
	OperationID  config.ID    `json:"operation_id"`
	IntentSHA256 string       `json:"intent_sha256"`
	BirthSHA256  string       `json:"birth_sha256"`
	PID          int          `json:"pid"`
	WaitStatus   uint32       `json:"wait_status"`
}

func (LaunchExitProof) String() string   { return "LaunchExitProof{direct child wait}" }
func (LaunchExitProof) GoString() string { return "LaunchExitProof{direct child wait}" }

func validLaunchExitSHA(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range []byte(value) {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}
func terminalLaunchWait(status uint32) bool {
	if status > 0xffff {
		return false
	}
	// Linux exit(code) occupies only the high byte. A signal occupies only the
	// low byte, optionally with the core-dump bit; stopped/continued are excluded.
	if status&0xff == 0 {
		return true
	}
	signal := status & 0x7f
	return status&0xff00 == 0 && signal >= 1 && signal <= 64
}
func (p LaunchExitProof) Match(intent config.RunIntent, intentSHA string, birth config.RunBirth, birthSHA string) error {
	if p.Version != 1 || p.Kind != "direct_child_wait" || p.PID <= 0 || !terminalLaunchWait(p.WaitStatus) || !validLaunchExitSHA(intentSHA) || !validLaunchExitSHA(birthSHA) || p.IntentSHA256 != intentSHA || p.BirthSHA256 != birthSHA || p.RunID != intent.RunID || p.OperationID != intent.OperationID || p.PID != birth.PID || birth.Match(intent, intentSHA) != nil {
		return ErrOwnershipUnknown
	}
	return nil
}

// ExitedOriginal waits only on this LaunchChild's sole cmd.Wait publication.
// It never observes/reopens a numeric PID, signals, or claims process ownership.
// Birth authority is the exclusive one-use ClaimRunBirth and protected original;
// this wait cannot independently reconstruct UID/boot/start for that birth.
func (c *LaunchChild) ExitedOriginal(ctx context.Context, store *config.Store, id config.RunID, op config.ID) (LaunchExitProof, error) {
	fail := func(err error) (LaunchExitProof, error) { return LaunchExitProof{}, err }
	if ctx == nil {
		return fail(ErrOwnershipUnknown)
	}
	if err := ctx.Err(); err != nil {
		return fail(errors.Join(ErrOwnershipUnknown, err))
	}
	if c == nil || c.cmd == nil || c.cmd.Process == nil || c.exit == nil || c.exit.done == nil || store == nil || c.intent.RunID != id || c.intent.OperationID != op || c.intent.Validate() != nil || !validLaunchExitSHA(c.intentSHA) {
		return fail(ErrOwnershipUnknown)
	}
	select {
	case <-ctx.Done():
		return fail(errors.Join(ErrOwnershipUnknown, ctx.Err()))
	case <-c.exit.done:
	}
	// Cancellation wins if both the Wait publication and context were ready.
	if err := ctx.Err(); err != nil {
		return fail(errors.Join(ErrOwnershipUnknown, err))
	}
	state := c.cmd.ProcessState
	if state == nil || state.Pid() != c.cmd.Process.Pid {
		return fail(ErrOwnershipUnknown)
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !terminalLaunchWait(uint32(status)) {
		return fail(ErrOwnershipUnknown)
	}
	if c.exit.err != nil {
		exit, ok := c.exit.err.(*exec.ExitError)
		if !ok || exit.ProcessState != state {
			return fail(ErrOwnershipUnknown)
		}
	}
	intent, intentSHA, err := store.ReadRunIntent(id)
	if err != nil || intentSHA != c.intentSHA || !reflect.DeepEqual(intent, c.intent) {
		return fail(ErrOwnershipUnknown)
	}
	birth, birthSHA, err := store.ReadRunBirth(id)
	if err != nil {
		return fail(ErrOwnershipUnknown)
	}
	proof := LaunchExitProof{Version: 1, Kind: "direct_child_wait", RunID: id, OperationID: op, IntentSHA256: intentSHA, BirthSHA256: birthSHA, PID: c.cmd.Process.Pid, WaitStatus: uint32(status)}
	if proof.Match(intent, intentSHA, birth, birthSHA) != nil {
		return fail(ErrOwnershipUnknown)
	}
	if err := ctx.Err(); err != nil {
		return fail(errors.Join(ErrOwnershipUnknown, err))
	}
	return proof, nil
}
