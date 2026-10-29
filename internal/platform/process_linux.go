//go:build linux && amd64

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hoopa/internal/p1testhook"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var ErrProcessUnverified = errors.New("PROCESS_UNVERIFIED")

const processNonceEnvironment = "HOOPA_RUN_NONCE"

// ProcessSpec comes from a durable launch intention. Matching it is necessary
// but not sufficient to adopt a core: the caller must also prove ownership of
// the connected controller before sending any credential or registering a run.
type ProcessSpec struct {
	BinaryPath, BinarySHA256, Workdir, NonceSHA256 string
}

// ProcessIdentity is an observation, never a portable authorization handle.
// PIDFD does not prevent exec or defend against hostile same-uid/root processes.
type ProcessIdentity struct {
	PID                                            int
	UID                                            uint32
	BootID                                         string
	StartTicks                                     uint64
	BinaryPath, BinarySHA256, Workdir, NonceSHA256 string
}

// ProcessHandle pins the observed process incarnation. Close never signals it;
// it may remain alive after the manager exits. WaitExited is not child reaping.
type ProcessHandle struct {
	mu       sync.Mutex
	fd       int
	identity ProcessIdentity
	spec     ProcessSpec
	closed   bool
}

func processDigest(data []byte) string {
	v := sha256.Sum256(data)
	return hex.EncodeToString(v[:])
}

func validProcessDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}

func validProcessSpec(s ProcessSpec) bool {
	for _, path := range []string{s.BinaryPath, s.Workdir} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) || len(path) > 4096 {
			return false
		}
	}
	return validProcessDigest(s.BinarySHA256) && validProcessDigest(s.NonceSHA256)
}

func readProc(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	err = errors.Join(err, f.Close())
	if err != nil || int64(len(b)) > limit {
		return nil, errors.Join(ErrProcessUnverified, err)
	}
	return b, nil
}

func parseProcessStat(raw []byte) (pid int, start uint64, state string, err error) {
	s := string(raw)
	left, right := strings.Index(s, " ("), strings.LastIndex(s, ") ")
	if left < 1 || right <= left {
		return 0, 0, "", ErrProcessUnverified
	}
	pid, err = strconv.Atoi(s[:left])
	fields := strings.Fields(s[right+2:])
	if err != nil || pid <= 0 || len(fields) < 20 || len(fields[0]) != 1 {
		return 0, 0, "", ErrProcessUnverified
	}
	start, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 || !strings.Contains("RSDZTWtXxKWPIN", fields[0]) {
		return 0, 0, "", ErrProcessUnverified
	}
	return pid, start, fields[0], nil
}

func liveStat(pid int) (uint64, error) {
	raw, err := readProc(fmt.Sprintf("/proc/%d/stat", pid), 4096)
	if err != nil {
		return 0, err
	}
	actual, start, state, err := parseProcessStat(raw)
	if err != nil || actual != pid || state == "Z" || state == "X" || state == "x" {
		return 0, errors.Join(ErrProcessUnverified, err)
	}
	return start, nil
}

func pidfdTargetMatches(raw []byte, pid int) error {
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Pid:") {
			continue
		}
		count++
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "Pid:" || fields[1] != strconv.Itoa(pid) || pid <= 0 {
			return ErrProcessUnverified
		}
	}
	if count != 1 {
		return ErrProcessUnverified
	}
	return nil
}

func verifyPidfdTarget(fd, pid int) error {
	// The kernel's pidfd fdinfo Pid is expressed in this procfs mount's PID
	// namespace. Bind /proc/<pid> to the original handle, not just to a numeric
	// equality between getpid() and /proc/self (which could coincide by chance).
	raw, err := readProc(fmt.Sprintf("/proc/self/fdinfo/%d", fd), 4096)
	if err != nil {
		return err
	}
	return pidfdTargetMatches(raw, pid)
}

// processPollOnce always probes the same pidfd without waiting. EINTR does not
// establish either liveness or exit, so only the bounded caller may retry it.
func processPollOnce(fd int) (int16, syscall.Errno) {
	var poll struct {
		FD      int32
		Events  int16
		Revents int16
	}
	poll.FD, poll.Events = int32(fd), 1 // POLLIN, supported for pidfds.
	_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 0)
	return poll.Revents, errno
}

func processPollExited(ctx context.Context, fd int, poll func(int) (int16, syscall.Errno)) (bool, error) {
	// Bound work even for context.Background while a caller holds its handle lock.
	for attempt := 0; attempt < 16; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		revents, errno := poll(fd)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return false, errno
		}
		if revents & ^int16(1|16) != 0 {
			return false, ErrProcessUnverified
		}
		return revents != 0, nil // Only POLLIN/POLLHUP can establish exit.
	}
	return false, syscall.EINTR
}

func processExited(ctx context.Context, fd int) (bool, error) {
	exited, err := processPollExited(ctx, fd, processPollOnce)
	if errno, ok := err.(syscall.Errno); ok && p1testhook.Enabled {
		// Recovered interruptions must not add synchronous observation work.
		_ = p1testhook.Record("platform", "pidfd-poll-failed", "", "", "", false, map[string]any{"errno": int(errno), "interrupted": errno == syscall.EINTR})
	}
	return exited, err
}

func hashProcessExecutable(ctx context.Context, pid int, expected string) error {
	f, err := os.Open(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || info.Size() <= 0 || info.Size() > 128<<20 {
		return ErrProcessUnverified
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > 128<<20 {
			return ErrProcessUnverified
		}
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if total != info.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
		return ErrProcessUnverified
	}
	return nil
}

func inspectProcess(ctx context.Context, fd, pid int, spec ProcessSpec) (ProcessIdentity, error) {
	var zero ProcessIdentity
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if exited, err := processExited(ctx, fd); err != nil || exited {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if err := verifyPidfdTarget(fd, pid); err != nil {
		return zero, err
	}
	start, err := liveStat(pid)
	if err != nil {
		return zero, err
	}
	boot, err := readProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil {
		return zero, err
	}
	bootID := strings.TrimSpace(string(boot))
	if len(bootID) != 36 || bootID[8] != '-' || bootID[13] != '-' || bootID[18] != '-' || bootID[23] != '-' {
		return zero, ErrProcessUnverified
	}
	if b, err := hex.DecodeString(strings.ReplaceAll(bootID, "-", "")); err != nil || len(b) != 16 {
		return zero, ErrProcessUnverified
	}
	root := fmt.Sprintf("/proc/%d", pid)
	info, err := os.Stat(root)
	if err != nil {
		return zero, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return zero, ErrProcessUnverified
	}
	work, err := os.Readlink(root + "/cwd")
	if err != nil || work != spec.Workdir {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	exe, err := os.Readlink(root + "/exe")
	if err != nil || exe != spec.BinaryPath {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	env, err := readProc(root+"/environ", 1<<20)
	if err != nil {
		return zero, err
	}
	count := 0
	for _, item := range strings.Split(string(env), "\x00") {
		if strings.HasPrefix(item, processNonceEnvironment+"=") {
			count++
			nonce := strings.TrimPrefix(item, processNonceEnvironment+"=")
			// Launch nonces are 32 random bytes encoded in lowercase hex. Never
			// include the original nonce or environment in diagnostics/errors.
			if !validProcessDigest(nonce) || processDigest([]byte(nonce)) != spec.NonceSHA256 {
				return zero, ErrProcessUnverified
			}
		}
	}
	if count != 1 {
		return zero, ErrProcessUnverified
	}
	if err = hashProcessExecutable(ctx, pid, spec.BinarySHA256); err != nil {
		return zero, err
	}
	if again, err := os.Readlink(root + "/exe"); err != nil || again != exe {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if again, err := os.Readlink(root + "/cwd"); err != nil || again != work {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if again, err := liveStat(pid); err != nil || again != start {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if err := verifyPidfdTarget(fd, pid); err != nil {
		return zero, err
	}
	if exited, err := processExited(ctx, fd); err != nil || exited {
		return zero, errors.Join(ErrProcessUnverified, err)
	}
	if err = ctx.Err(); err != nil {
		return zero, err
	}
	return ProcessIdentity{PID: pid, UID: st.Uid, BootID: bootID, StartTicks: start, BinaryPath: exe, BinarySHA256: spec.BinarySHA256, Workdir: work, NonceSHA256: spec.NonceSHA256}, nil
}

// ObserveProcess verifies a process against launch material, with no signal or
// mutation. A missing/inaccessible PID is unverified, not proof of absence.
func ObserveProcess(ctx context.Context, pid int, spec ProcessSpec) (*ProcessHandle, error) {
	if pid <= 0 || !validProcessSpec(spec) {
		return nil, ErrProcessUnverified
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Early procfs visibility sanity check; verifyPidfdTarget additionally binds
	// each inspected pathname PID to the actual original pidfd.
	self, err := os.Readlink("/proc/self")
	if err != nil || self != strconv.Itoa(os.Getpid()) {
		return nil, errors.Join(ErrProcessUnverified, err)
	}
	fd, _, errno := syscall.Syscall(434, uintptr(pid), 0, 0) // pidfd_open, Linux amd64.
	if errno != 0 {
		return nil, errors.Join(ErrProcessUnverified, errno)
	}
	syscall.CloseOnExec(int(fd))
	identity, err := inspectProcess(ctx, int(fd), pid, spec)
	if err != nil {
		syscall.Close(int(fd))
		return nil, errors.Join(ErrProcessUnverified, err)
	}
	return &ProcessHandle{fd: int(fd), identity: identity, spec: spec}, nil
}

func (h *ProcessHandle) Identity() ProcessIdentity {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.identity
}

func (h *ProcessHandle) Verify(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verify(ctx)
}

func (h *ProcessHandle) verify(ctx context.Context) error {
	if h.closed {
		return ErrProcessUnverified
	}
	identity, err := inspectProcess(ctx, h.fd, h.identity.PID, h.spec)
	if err != nil || identity != h.identity {
		return errors.Join(ErrProcessUnverified, err)
	}
	return nil
}

// SignalVerified is a platform primitive, not an adoption policy. A coordinator
// may call it only after a durable stop intent and fresh connected-core proof.
// The signal uses the original pidfd; it never falls back to kill(numeric PID).
func (h *ProcessHandle) SignalVerified(ctx context.Context, signal syscall.Signal) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if signal != syscall.SIGTERM && signal != syscall.SIGKILL {
		return ErrProcessUnverified
	}
	if err := h.verify(ctx); err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(424, uintptr(h.fd), uintptr(signal), 0, 0, 0, 0) // pidfd_send_signal.
	if errno != 0 {
		return errors.Join(ErrProcessUnverified, errno)
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("platform", "signal-sent", "", "", "", false, map[string]any{"signal": int(signal), "identity": h.identity})
	}
	return nil
}

func (h *ProcessHandle) WaitExited(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return ErrProcessUnverified
		}
		exited, err := processExited(ctx, h.fd)
		h.mu.Unlock()
		if err != nil {
			return err
		}
		if exited {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (h *ProcessHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	return syscall.Close(h.fd)
}
