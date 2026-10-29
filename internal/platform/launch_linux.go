//go:build linux && amd64

package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ProcessBirth is a pre-exec observation, not permission to control a core.
type ProcessBirth struct {
	PID        int
	UID        uint32
	BootID     string
	StartTicks uint64
}

func launchBootID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !(value[i] >= '0' && value[i] <= '9' || value[i] >= 'a' && value[i] <= 'f') {
			return false
		}
	}
	return true
}

// CurrentProcessBirth reads this process's actual procfs identity on both sides
// of the observation. It deliberately does not open a self pidfd or hash its
// executable: later ownership verification must use the core's original pidfd
// and full ProcessSpec after exec; this pre-exec hint cannot replace that proof.
func CurrentProcessBirth(ctx context.Context) (ProcessBirth, error) {
	var zero ProcessBirth
	if ctx == nil {
		return zero, ErrProcessUnverified
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	pid := os.Getpid()
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(pid) {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	before, err := liveStat(pid)
	if err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	bootRaw, err := readProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	boot := strings.TrimSuffix(string(bootRaw), "\n")
	if !launchBootID(boot) {
		return zero, ErrProcessUnverified
	}
	raw, err := readProc("/proc/self/stat", 4096)
	if err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	actual, start, state, err := parseProcessStat(raw)
	if err != nil || actual != pid || start != before || state == "Z" || state == "X" || state == "x" {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	info, err := os.Stat("/proc/self")
	if err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return zero, ErrProcessUnverified
	}
	after, err := liveStat(pid)
	if err != nil || after != before {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	bootAgain, err := readProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil || !bytes.Equal(bootAgain, bootRaw) {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	self, err = os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(pid) {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if err := ctx.Err(); err != nil {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	return ProcessBirth{PID: pid, UID: stat.Uid, BootID: boot, StartTicks: start}, nil
}

func launchArguments(binary string, args []string) bool {
	if len(args) < 1 || len(args) > 64 || args[0] != binary {
		return false
	}
	total := 0
	for _, arg := range args {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return false
		}
		total += len(arg)
	}
	return total <= 32768
}

// VerifyArguments checks the whole argv, including separators and the terminal
// NUL, between full checks of the original process handle. It never reports argv.
// This is an observation; it does not freeze the process against later changes.
func (h *ProcessHandle) VerifyArguments(ctx context.Context, args []string) error {
	if h == nil || ctx == nil {
		return ErrProcessUnverified
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrProcessUnverified, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !launchArguments(h.spec.BinaryPath, args) {
		return ErrProcessUnverified
	}
	if err := h.verify(ctx); err != nil {
		return errors.Join(ErrProcessUnverified, err)
	}
	raw, err := readProc(fmt.Sprintf("/proc/%d/cmdline", h.identity.PID), 32768+64)
	if err != nil {
		return errors.Join(ErrProcessUnverified, err)
	}
	if err := h.verify(ctx); err != nil {
		return errors.Join(ErrProcessUnverified, err)
	}
	if !bytes.Equal(raw, []byte(strings.Join(args, "\x00")+"\x00")) {
		return ErrProcessUnverified
	}
	return nil
}
