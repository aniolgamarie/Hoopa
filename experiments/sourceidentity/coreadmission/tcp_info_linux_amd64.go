//go:build linux && amd64

package coreadmission

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const fixedKernelRelease = "6.18.54.1-microsoft-standard-WSL2"

func validateKernelRelease(release string) error {
	if release != fixedKernelRelease {
		return errors.New("unsupported experimental TCP_INFO kernel profile")
	}
	return nil
}

func validateTCPInfo(info []byte) error {
	if len(info) < 8 {
		return errors.New("short original socket TCP_INFO")
	}
	// Fixed 6.18 guest: include/uapi/linux/tcp.h TCPI_OPT_TFO_CHILD.
	// TFO passive-established can precede the peer's active-established,
	// invalidating this prototype's tuple publication order. SYN_DATA alone
	// misses data-less TFO. State is deliberately not restricted to ESTABLISHED:
	// an ordinary peer may already have sent FIN, leaving CLOSE_WAIT.
	if info[5]&128 != 0 {
		return errors.New("TCP Fast Open child outside connector admission profile")
	}
	return nil
}

// Called inside the same Control callback as the cookies and endpoints,
// before any protocol I/O or lookup of a potentially stale tuple binding.
func rejectFastOpenChild(fd int) error {
	var info [256]byte
	size := uint32(len(info))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), syscall.IPPROTO_TCP,
		syscall.TCP_INFO, uintptr(unsafe.Pointer(&info[0])), uintptr(unsafe.Pointer(&size)), 0)
	runtime.KeepAlive(&info)
	runtime.KeepAlive(&size)
	if errno != 0 {
		return errno
	}
	if size > uint32(len(info)) {
		return errors.New("oversized original socket TCP_INFO")
	}
	return validateTCPInfo(info[:size])
}
