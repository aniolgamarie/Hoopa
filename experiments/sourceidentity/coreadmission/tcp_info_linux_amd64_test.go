//go:build linux && amd64

package coreadmission

import (
	"errors"
	"syscall"
	"testing"
)

func TestTCPInfoProfilePreservesHalfCloseAndRejectsTFO(t *testing.T) {
	for _, state := range []byte{1, 3, 8} { // ESTABLISHED, SYN_RECV, CLOSE_WAIT
		for _, options := range []byte{0, 7, 32, 128, 128 | 7, 128 | 32} {
			info := make([]byte, 8)
			info[0], info[5] = state, options
			err := validateTCPInfo(info)
			if (err != nil) != (options&128 != 0) {
				t.Fatalf("state=%d options=%d: %v", state, options, err)
			}
		}
	}
	for size := 0; size < 8; size++ {
		if err := validateTCPInfo(make([]byte, size)); err == nil {
			t.Fatalf("short TCP_INFO accepted: %d", size)
		}
	}
}

func TestTCPInfoKernelProfileAndReadFailure(t *testing.T) {
	for _, release := range []string{"", "6.6.0", "6.18.54.1", fixedKernelRelease + "-other", fixedKernelRelease} {
		if err := validateKernelRelease(release); (err == nil) != (release == fixedKernelRelease) {
			t.Fatalf("release=%q: %v", release, err)
		}
	}
	// A syscall sandbox can deny getsockopt before the fd is resolved. Both
	// errors must propagate; neither may become a zero-filled success result.
	if err := rejectFastOpenChild(-1); !errors.Is(err, syscall.EBADF) && !errors.Is(err, syscall.EPERM) {
		t.Fatalf("original fd read failure must reject: %v", err)
	}
}
