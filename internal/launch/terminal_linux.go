//go:build linux && amd64

package launch

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"hoopa/internal/config"
)

var ErrLaunchTerminal = errors.New("LAUNCH_TERMINAL_INVALID")

// terminalState borrows the caller's stable stdin duplicate until restoration.
// It neither closes it nor owns the child's wait/pidfd lifetime.
type terminalState struct {
	stdin  *os.File
	active bool
	owner  config.AppLaunchProcess
	prior  int32
}

func prepareTerminal(stdin *os.File) (*terminalState, error) {
	if stdin == nil {
		return nil, ErrLaunchTerminal
	}
	// TIOCGPGRP can also report ENOTTY for a real but non-controlling TTY.
	// Classify the descriptor itself first so that such a conflict cannot be
	// silently treated as ordinary non-TTY input.
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	if errno == syscall.ENOTTY {
		return &terminalState{stdin: stdin}, nil
	}
	if errno != 0 {
		return nil, ErrLaunchTerminal
	}
	owner, err := currentIdentity()
	if err != nil {
		return nil, ErrLaunchTerminal
	}
	var sid int32
	if launchTerminalIoctl(stdin.Fd(), 0x5429, &sid) != nil || sid != int32(owner.Session) {
		return nil, ErrLaunchTerminal
	}
	fg, err := launchForeground(stdin.Fd())
	if err != nil || fg != int32(owner.ProcessGroup) {
		return nil, ErrLaunchTerminal
	}
	return &terminalState{stdin: stdin, active: true, owner: owner, prior: fg}, nil
}
func (t *terminalState) attributes(pidfd *int) *syscall.SysProcAttr {
	if t == nil {
		return nil
	}
	attrs := &syscall.SysProcAttr{Setpgid: true, PidFD: pidfd}
	if t.active {
		attrs.Foreground = true
		attrs.Ctty = int(t.stdin.Fd())
	}
	return attrs
}

// handoff is the product runner's post-birth transition. The helper is already
// identified by its original pidfd, but remains blocked on the authorization
// pipe. Delaying foreground transfer until this point avoids leaving a terminal
// behind when Start fails or an emulated kernel fails to return a clone pidfd.
// The bool reports that the foreground ioctl was attempted; callers retain the
// original child unreaped and attempt restore on that path even on uncertainty.
func (t *terminalState) handoff(child config.AppLaunchProcess) (changed bool, err error) {
	if t == nil || t.stdin == nil {
		return false, ErrLaunchTerminal
	}
	if !t.active {
		return false, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var old uint64
	mask := uint64(1) << uint(syscall.SIGTTOU-1)
	if launchThreadMask(0, &mask, &old) != nil {
		return false, ErrLaunchTerminal
	}
	defer func() {
		if launchThreadMask(2, &old, nil) != nil {
			err = ErrLaunchTerminal
		}
	}()
	current, e := currentIdentity()
	if e != nil || current != t.owner || child.PPID != t.owner.PID || child.PID == t.owner.PID || child.ProcessGroup != child.PID || child.Session != t.owner.Session || child.UID != t.owner.UID || child.BootID != t.owner.BootID {
		return false, ErrLaunchTerminal
	}
	now, e := readLaunchProcess(child.PID)
	if e != nil || now.identity != child || now.state == "Z" {
		return false, ErrLaunchTerminal
	}
	var sid int32
	if launchTerminalIoctl(t.stdin.Fd(), 0x5429, &sid) != nil || sid != int32(t.owner.Session) {
		return false, ErrLaunchTerminal
	}
	fg, e := launchForeground(t.stdin.Fd())
	if e != nil || fg != t.prior {
		return false, ErrLaunchTerminal
	}
	group := int32(child.ProcessGroup)
	if launchTerminalIoctl(t.stdin.Fd(), syscall.TIOCSPGRP, &group) != nil {
		return true, ErrLaunchTerminal
	}
	fg, e = launchForeground(t.stdin.Fd())
	if e != nil || fg != group {
		return true, ErrLaunchTerminal
	}
	return true, nil
}
func launchTerminalIoctl(fd, request uintptr, value *int32) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(value)))
	if errno != 0 {
		return errno
	}
	return nil
}
func launchForeground(fd uintptr) (int32, error) {
	var fg int32
	err := launchTerminalIoctl(fd, syscall.TIOCGPGRP, &fg)
	return fg, err
}
func launchThreadMask(how uintptr, new, old *uint64) error {
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK, how, uintptr(unsafe.Pointer(new)), uintptr(unsafe.Pointer(old)), 8, 0, 0)
	if errno != 0 {
		return ErrLaunchTerminal
	}
	return nil
}
func (t *terminalState) heldZombie(child config.AppLaunchProcess) error {
	if child.PID <= 0 || child.PID == t.owner.PID || child.PPID != t.owner.PID || child.ProcessGroup != child.PID || child.Session != t.owner.Session || child.UID != t.owner.UID || child.BootID != t.owner.BootID || child.StartTicks == 0 {
		return ErrLaunchTerminal
	}
	now, err := readLaunchProcess(child.PID)
	if err != nil || now.state != "Z" || now.identity != child {
		return ErrLaunchTerminal
	}
	return nil
}

// restore must run while the caller still holds the original child unreaped.
// Normal cooperative-session checks are not a cross-process foreground CAS.
func (t *terminalState) restore(child config.AppLaunchProcess) (err error) {
	if t == nil || t.stdin == nil {
		return ErrLaunchTerminal
	}
	if !t.active {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var old uint64
	mask := uint64(1) << uint(syscall.SIGTTOU-1)
	if launchThreadMask(0, &mask, &old) != nil {
		return ErrLaunchTerminal
	}
	defer func() {
		if launchThreadMask(2, &old, nil) != nil {
			err = ErrLaunchTerminal
		}
	}()
	current, e := currentIdentity()
	if e != nil || current.PID != t.owner.PID || current.UID != t.owner.UID || current.BootID != t.owner.BootID || current.StartTicks != t.owner.StartTicks || current.ProcessGroup != t.owner.ProcessGroup || current.Session != t.owner.Session {
		return ErrLaunchTerminal
	}
	var sid int32
	if launchTerminalIoctl(t.stdin.Fd(), 0x5429, &sid) != nil || sid != int32(t.owner.Session) {
		return ErrLaunchTerminal
	}
	fg, e := launchForeground(t.stdin.Fd())
	if e != nil || fg != int32(child.ProcessGroup) {
		return ErrLaunchTerminal
	}
	if t.heldZombie(child) != nil {
		return ErrLaunchTerminal
	}
	if launchTerminalIoctl(t.stdin.Fd(), syscall.TIOCSPGRP, &t.prior) != nil {
		return ErrLaunchTerminal
	}
	fg, e = launchForeground(t.stdin.Fd())
	if e != nil || fg != t.prior || t.heldZombie(child) != nil {
		return ErrLaunchTerminal
	}
	return nil
}
