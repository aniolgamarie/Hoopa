//go:build linux && amd64

package launch

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"hoopa/internal/config"
)

var ErrLaunchProcess = errors.New("LAUNCH_PROCESS_INVALID")

type launchProcessSignalError struct{ errno syscall.Errno }

func (launchProcessSignalError) Error() string    { return "LAUNCH_PROCESS_INVALID" }
func (launchProcessSignalError) String() string   { return "LAUNCH_PROCESS_INVALID" }
func (launchProcessSignalError) GoString() string { return "LAUNCH_PROCESS_INVALID" }
func (e launchProcessSignalError) Is(target error) bool {
	return target == ErrLaunchProcess || target == e.errno
}

type launchProcessSnapshot struct {
	identity config.AppLaunchProcess
	state    string
}

func currentIdentity() (config.AppLaunchProcess, error) { return childIdentity(os.Getpid()) }

// childIdentity reads historical metadata only. The caller retains the original
// clone-created pidfd and has not waited/reaped this child; metadata alone never
// grants adoption or signal authority. Z is permitted for pre-exec death.
func childIdentity(pid int) (config.AppLaunchProcess, error) {
	snapshot, err := readLaunchProcess(pid)
	if err != nil {
		return config.AppLaunchProcess{}, ErrLaunchProcess
	}
	return snapshot.identity, nil
}
func readLaunchProcess(pid int) (launchProcessSnapshot, error) {
	var zero launchProcessSnapshot
	if pid <= 0 || pid > 2147483647 {
		return zero, ErrLaunchProcess
	}
	prefix := "/proc/" + strconv.Itoa(pid)
	first, err := boundedLaunchRead(prefix+"/stat", 8192)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	before, err := parseLaunchStat(first, pid)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	status, err := boundedLaunchRead(prefix+"/status", 1<<20)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	uid, err := launchEffectiveUID(status)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	boot, err := boundedLaunchRead("/proc/sys/kernel/random/boot_id", 128)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	bootID := strings.TrimSuffix(string(boot), "\n")
	if !launchBootID(bootID) {
		return zero, ErrLaunchProcess
	}
	last, err := boundedLaunchRead(prefix+"/stat", 8192)
	if err != nil {
		return zero, ErrLaunchProcess
	}
	after, err := parseLaunchStat(last, pid)
	if err != nil || after.identity.PID != before.identity.PID || after.identity.StartTicks != before.identity.StartTicks {
		return zero, ErrLaunchProcess
	}
	after.identity.UID = uid
	after.identity.BootID = bootID
	return after, nil
}
func boundedLaunchRead(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrLaunchProcess
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(raw) > limit {
		return nil, ErrLaunchProcess
	}
	return raw, nil
}
func parseLaunchStat(raw []byte, pid int) (launchProcessSnapshot, error) {
	var result launchProcessSnapshot
	if len(raw) == 0 || len(raw) > 8192 {
		return result, ErrLaunchProcess
	}
	open, close := strings.IndexByte(string(raw), '('), strings.LastIndexByte(string(raw), ')')
	if open < 1 || close < open {
		return result, ErrLaunchProcess
	}
	actual, err := strconv.Atoi(strings.TrimSpace(string(raw[:open])))
	fields := strings.Fields(string(raw[close+1:]))
	if err != nil || actual != pid || pid <= 0 || pid > 2147483647 || len(fields) < 20 || len(fields[0]) != 1 {
		return result, ErrLaunchProcess
	}
	result.identity.PID = actual
	result.state = fields[0]
	for i, target := range []*int{&result.identity.PPID, &result.identity.ProcessGroup, &result.identity.Session} {
		value, err := strconv.ParseUint(fields[i+1], 10, 31)
		if err != nil || value == 0 {
			return launchProcessSnapshot{}, ErrLaunchProcess
		}
		*target = int(value)
	}
	result.identity.StartTicks, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil || result.identity.StartTicks == 0 {
		return launchProcessSnapshot{}, ErrLaunchProcess
	}
	return result, nil
}
func launchEffectiveUID(raw []byte) (uint32, error) {
	var uid uint32
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if found || len(fields) != 5 {
			return 0, ErrLaunchProcess
		}
		for i := 1; i < 5; i++ {
			value, err := strconv.ParseUint(fields[i], 10, 32)
			if err != nil {
				return 0, ErrLaunchProcess
			}
			if i == 2 {
				uid = uint32(value)
			}
		}
		found = true
	}
	if !found {
		return 0, ErrLaunchProcess
	}
	return uid, nil
}
func launchBootID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range []byte(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// signalOriginalChild never resolves a PID. The caller owns this exact raw
// clone pidfd. Forwarding policy is separate; the primitive also permits KILL
// for the caller's explicitly authorized pre-dispatch helper cleanup.
func signalOriginalChild(fd int, sig syscall.Signal) error {
	if fd < 0 || fd > 2147483647 || sig < 1 || sig > 64 {
		return ErrLaunchProcess
	}
	_, _, errno := syscall.Syscall6(424, uintptr(fd), uintptr(sig), 0, 0, 0, 0)
	if errno != 0 {
		return launchProcessSignalError{errno: errno}
	}
	return nil
}

// observeChildExit only polls the original pidfd. It never Waits, reaps, closes
// or signals it; even a ready result grants no replacement-process authority.
func observeChildExit(ctx context.Context, fd int) error {
	if ctx == nil {
		return ErrLaunchProcess
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if fd < 0 || fd > 2147483647 {
		return ErrLaunchProcess
	}
	type pollFD struct {
		fd              int32
		events, revents int16
	}
	p := pollFD{fd: int32(fd), events: 1}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := 100
		if deadline, ok := ctx.Deadline(); ok {
			left := time.Until(deadline)
			if left <= 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
				return context.DeadlineExceeded
			} else if left < 100*time.Millisecond {
				timeout = int((left + time.Millisecond - 1) / time.Millisecond)
			}
		}
		p.revents = 0
		n, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&p)), 1, uintptr(timeout))
		if err := ctx.Err(); err != nil {
			return err
		}
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 || p.revents&0x20 != 0 {
			return ErrLaunchProcess
		}
		if n == 0 {
			continue
		}
		if p.revents&1 != 0 {
			return nil
		}
		return ErrLaunchProcess
	}
}
