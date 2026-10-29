//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type p1GuestBinding struct {
	Kind       string `json:"kind"`
	Token      string `json:"token"`
	BootID     string `json:"boot_id"`
	TestSHA256 string `json:"test_sha256"`
}

// System power writes are allowed only in the dedicated booted guest: the
// kernel marker, PID1 parent, image-bound test binary and boot ID must agree.
// An environment variable alone can never enable this path on the host.
func p1SessionGuest() (p1GuestBinding, error) {
	var b p1GuestBinding
	raw, err := os.ReadFile("/work/p1-guest-binding.json")
	if err != nil || json.Unmarshal(raw, &b) != nil || b.Kind != "hoopa-p1-isolated-session-guest" || !operationDigest(b.Token) || !operationDigest(b.TestSHA256) || os.Geteuid() != 0 || os.Getppid() != 1 {
		return b, errP1HarnessUnbuilt
	}
	cmdline, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return b, err
	}
	count := 0
	for _, item := range strings.Fields(string(cmdline)) {
		if strings.HasPrefix(item, "hoopa_p1_session_vm=") {
			if item != "hoopa_p1_session_vm="+b.Token {
				return b, errP1Harness
			}
			count++
		}
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || count != 1 || strings.TrimSpace(string(boot)) != b.BootID {
		return b, errP1Harness
	}
	path, err := os.Executable()
	if err != nil {
		return b, err
	}
	_, sha, err := p1CLIExecutable(path)
	if err != nil || sha != b.TestSHA256 {
		return b, errP1Harness
	}
	return b, nil
}

func p1SessionClock(clock uintptr) (int64, error) {
	var ts syscall.Timespec
	_, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clock, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		return 0, errno
	}
	return ts.Nano(), nil
}

func (h *p1ProductHarness) BeginSession(ctx context.Context, kind string) error {
	if ctx == nil || ctx.Err() != nil || (kind != "ssh-disconnect" && kind != "login-shell-logout") || h.sessionDriver != nil {
		return errP1HarnessUnbuilt
	}
	if _, err := p1SessionGuest(); err != nil {
		return err
	}
	if err := h.stopManager(ctx, false); err != nil {
		return err
	}
	h.sessionTraceAt = len(h.f.processes)
	driver, err := startP1SessionDriver(ctx, h, kind)
	if driver != nil {
		h.sessionDriver = driver
		if driver.Trace != nil {
			h.manager = nil
		}
	}
	return err
}

func (h *p1ProductHarness) SessionEvent(ctx context.Context, kind string) error {
	if ctx == nil || ctx.Err() != nil {
		return errP1HarnessUnbuilt
	}
	if kind == "ssh-disconnect" || kind == "login-shell-logout" {
		if h.sessionDriver == nil || h.sessionDriver.Trace == nil {
			return errP1Harness
		}
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		before, err := h.Observe(bounded)
		if err != nil {
			return err
		}
		if before.Core.State != "observed" || before.Core.Value == nil || before.ControllerOwnerProof.State != "observed" {
			return errP1Harness
		}
		ref, err := h.original("session-core-before-trigger", before)
		if err != nil {
			return err
		}
		h.extras = append(h.extras, ref)
		record, err := h.sessionDriver.End(bounded)
		if err != nil {
			return err
		}
		if record.Kind != kind || record.Began.Path == "" || record.Ended.Path == "" || record.Began == record.Ended {
			return errP1Harness
		}
		h.extras = append(h.extras, record.Began, record.Ended)
		h.sessions = append(h.sessions, record)
		return nil
	}
	if kind != "system-suspend-resume" {
		return errP1HarnessUnbuilt
	}
	binding, err := p1SessionGuest()
	if err != nil {
		return err
	}
	state, err := os.ReadFile("/sys/power/state")
	if err != nil || !strings.Contains(" "+strings.TrimSpace(string(state))+" ", " mem ") {
		return errP1Harness
	}
	sleep, err := os.ReadFile("/sys/power/mem_sleep")
	if err != nil || !strings.Contains(string(sleep), "deep") {
		return errP1Harness
	}
	beforeMono, err := p1SessionClock(1)
	if err != nil {
		return err
	}
	beforeBoot, err := p1SessionClock(7)
	if err != nil {
		return err
	}
	begin, err := h.original("session-suspend-begin", map[string]any{"kind": kind, "phase": "before-guest-kernel-mem-write", "guest_binding": binding, "power_state": string(state), "mem_sleep": string(sleep), "monotonic_ns": beforeMono, "boottime_ns": beforeBoot, "host_power_write": false})
	if err != nil || ctx.Err() != nil {
		return errP1Harness
	}
	h.extras = append(h.extras, begin)
	if err = os.WriteFile("/sys/power/mem_sleep", []byte("deep"), 0600); err != nil {
		return err
	}
	// The external VM supervisor must observe QMP SUSPEND and request wakeup.
	// This write returns only after the dedicated guest resumes or refuses it.
	if err = os.WriteFile("/sys/power/state", []byte("mem"), 0600); err != nil {
		return err
	}
	afterMono, err := p1SessionClock(1)
	if err != nil {
		return err
	}
	afterBoot, err := p1SessionClock(7)
	if err != nil {
		return err
	}
	again, err := p1SessionGuest()
	if err != nil || again != binding || afterMono < beforeMono || (afterBoot-beforeBoot)-(afterMono-beforeMono) < 1_000_000_000 || ctx.Err() != nil {
		return errP1Harness
	}
	end, err := h.original("session-suspend-end", map[string]any{"kind": kind, "phase": "returned-from-guest-kernel-mem-write", "begin_original": begin, "guest_binding": again, "monotonic_ns": afterMono, "boottime_ns": afterBoot, "suspended_clock_delta_ns": (afterBoot - beforeBoot) - (afterMono - beforeMono), "host_qmp_and_kernel_log_validation_required": true})
	if err != nil {
		return err
	}
	h.extras = append(h.extras, end)
	h.sessions = append(h.sessions, sessionRecord{Kind: kind, Began: begin, Ended: end})
	return nil
}
