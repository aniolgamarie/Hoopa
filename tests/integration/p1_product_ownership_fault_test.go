//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Only private test-owned run metadata is arranged here. A changed start tick
// is a persisted-identity mismatch fixture, never evidence of kernel PID reuse.
func (h *p1ProductHarness) arrangeOwnershipFault(ctx context.Context, r faultRequest) error {
	if ctx == nil || ctx.Err() != nil || h.ownershipFault != nil || r.Point != "ownership-fixture" || r.Artifact != "" || r.Phase != "" || r.Cut != "" || r.Target != nil || r.Limit != 0 {
		return errP1Harness
	}
	switch r.Failure {
	case "birth-start-identity-mismatch", "missing-registration-and-birth", "incomplete-registration", "foreign-listener", "actual-pid-reuse":
	default:
		return errP1HarnessUnbuilt
	}
	o, err := h.Observe(ctx)
	if err != nil || o.Core.State != "observed" || o.Core.Value == nil || o.Runtime.Value == nil || *o.Runtime.Value != "verified-running" {
		return errP1Harness
	}
	p := *o.Core.Value
	if r.Failure == "foreign-listener" || r.Failure == "actual-pid-reuse" {
		return h.arrangeForeignListener(ctx, r, p)
	}
	h.mu.Lock()
	handle := h.f.handles[p.RunID]
	h.mu.Unlock()
	if handle == nil || handle.Verify(ctx) != nil {
		return errP1Harness
	}
	intent, _, err := h.f.store.ReadRunIntent(p.RunID)
	if err != nil {
		return err
	}
	if err = h.stopManager(ctx, false); err != nil {
		return err
	}
	dir := filepath.Join(h.f.cli.Paths().State, "runs", p1SHA([]byte(p.RunID)))
	active, err := h.f.store.ReadActive()
	if err != nil || active.OperationID == nil {
		return errP1Harness
	}
	registration := "registration-" + p1SHA([]byte(*active.OperationID)) + ".json"
	before := map[string]originalRef{}
	after := map[string]originalRef{}
	for _, name := range []string{"intent.json", "birth.json", registration} {
		raw, readErr := p1ReadRunOriginal(filepath.Join(dir, name))
		if readErr != nil {
			return readErr
		}
		ref, captureErr := p1WriteOriginal(filepath.Join(h.directory("ownership-before"), name), raw)
		if captureErr != nil {
			return captureErr
		}
		before[name] = ref
		h.extras = append(h.extras, ref)
	}
	removed := []string{}
	switch r.Failure {
	case "birth-start-identity-mismatch":
		birth, _, readErr := h.f.store.ReadRunBirth(p.RunID)
		if readErr != nil || birth.StartTicks == ^uint64(0) {
			return errP1Harness
		}
		birth.StartTicks++
		if birth.Validate() != nil {
			return errP1Harness
		}
		raw, marshalErr := json.Marshal(birth)
		if marshalErr != nil {
			return marshalErr
		}
		err = p1WriteOwnershipFixture(filepath.Join(dir, "birth.json"), raw)
	case "missing-registration-and-birth":
		for _, name := range []string{registration, "birth.json"} {
			if err = os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
			removed = append(removed, name)
		}
	case "incomplete-registration":
		// Keep the real intent/birth and live process, so the failure reaches
		// registration validation after independent process/controller proof.
		err = p1WriteOwnershipFixture(filepath.Join(dir, registration), []byte(`{"version":1}`))
	}
	if err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	for name := range before {
		raw, readErr := p1ReadRunOriginal(filepath.Join(dir, name))
		if errors.Is(readErr, os.ErrNotExist) && r.Failure == "missing-registration-and-birth" && name != "intent.json" {
			continue
		}
		if readErr != nil {
			return readErr
		}
		ref, captureErr := p1WriteOriginal(filepath.Join(h.directory("ownership-after"), name), raw)
		if captureErr != nil {
			return captureErr
		}
		after[name] = ref
		h.extras = append(h.extras, ref)
	}
	if handle.Verify(ctx) != nil || handle.VerifyArguments(ctx, intent.Arguments) != nil {
		return errP1Harness
	}
	ref, err := h.original("ownership-fixture", map[string]any{"kind": "controlled-private-run-metadata-fixture", "arrangement": r.Failure, "run_id": p.RunID, "original_process_identity": handle.Identity(), "original_pidfd_still_verified": true, "kernel_pid_reuse_observed": false, "before": before, "after": after, "removed": removed})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, ref)
	h.ownershipFault = &faultHit{Point: r.Point, Failure: r.Failure, Original: ref}
	return h.startManager(false)
}

func (h *p1ProductHarness) arrangeForeignListener(ctx context.Context, r faultRequest, p processIdentity) error {
	if r.Failure == "actual-pid-reuse" {
		// A host environment variable cannot enable writes to kernel PID state.
		if _, err := p1SessionGuest(); err != nil {
			return err
		}
	}
	intent, _, err := h.f.store.ReadRunIntent(p.RunID)
	if err != nil {
		return err
	}
	if err = h.stopManager(ctx, false); err != nil {
		return err
	}
	// The exact old core must exit before its TCP address can be occupied.
	// This controlled setup does not claim that a live core lost its listener.
	if err = h.KillCore(ctx, p); err != nil {
		return err
	}
	exit := h.extras[len(h.extras)-1]
	work := filepath.Join(h.f.root, "foreign-controller")
	if err = os.Mkdir(work, 0700); err != nil {
		return err
	}
	var reuseSetup originalRef
	if r.Failure == "actual-pid-reuse" {
		status, err := p1WaitProcessAbsent(ctx, p.PID, true)
		if err != nil {
			return err
		}
		before, err := os.ReadFile("/proc/sys/kernel/ns_last_pid")
		if err != nil {
			return err
		}
		reuseSetup, err = h.original("pid-reuse-setup", map[string]any{"kind": "dedicated-guest-original-core-exited-and-reaped", "original_core": p, "exit_original": exit, "wait_status": status, "proc_absent": true, "ns_last_pid_before": string(before), "requested_next_pid": p.PID})
		if err != nil {
			return err
		}
		if err = os.WriteFile("/proc/sys/kernel/ns_last_pid", []byte(strconv.Itoa(p.PID-1)), 0600); err != nil {
			return err
		}
		h.extras = append(h.extras, reuseSetup)
	}
	foreign, err := startP1ForeignListener(ctx, intent.Controller, work, h.directory("foreign-controller"))
	if err != nil {
		return err
	}
	h.foreignListener = foreign
	identity, live, err := foreign.Observe(ctx)
	if err != nil {
		return err
	}
	if r.Failure == "actual-pid-reuse" {
		h.mu.Lock()
		oldHandle := h.f.handles[p.RunID]
		h.mu.Unlock()
		if identity.PID != p.PID || identity.BootID != p.BootID || strconv.FormatUint(identity.StartTicks, 10) == p.OSStartIdentity || oldHandle == nil || oldHandle.WaitExited(ctx) != nil {
			return errP1Harness
		}
	}
	ref, err := h.original("ownership-fixture", map[string]any{"kind": "actual-test-owned-foreign-controller-listener", "arrangement": r.Failure, "original_core": p, "original_core_exit": exit, "controller": foreign.Address(), "foreign_identity": identity, "foreign_begin": foreign.Begin(), "foreign_live_owned_tcp": live, "pid_reuse_setup": reuseSetup, "kernel_pid_reuse_observed": r.Failure == "actual-pid-reuse", "scope": "original core exited; listener-only presence grants no product ownership"})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, foreign.Begin(), live, ref)
	h.ownershipFault = &faultHit{Point: r.Point, Failure: r.Failure, Original: ref}
	return h.startManager(false)
}

func p1WriteOwnershipFixture(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(raw)
	if n != len(raw) && writeErr == nil {
		writeErr = errP1Harness
	}
	return errors.Join(writeErr, file.Sync(), file.Close())
}
