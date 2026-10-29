//go:build linux && amd64

package platform

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

func TestProcessPollInterruptedThenResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		revents int16
		exited  bool
	}{{"live", 0, false}, {"in", 1, true}, {"hup", 16, true}, {"in-hup", 17, true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			exited, err := processPollExited(context.Background(), 73, func(fd int) (int16, syscall.Errno) {
				if fd != 73 {
					t.Fatal("retry changed pidfd")
				}
				calls++
				if calls == 1 {
					return 17, syscall.EINTR
				} // Interrupted revents are never proof.
				return tc.revents, 0
			})
			if err != nil || exited != tc.exited || calls != 2 {
				t.Fatalf("exited=%v err=%v calls=%d", exited, err, calls)
			}
		})
	}
}
func TestProcessPollCancellationBeforeRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	exited, err := processPollExited(ctx, 74, func(fd int) (int16, syscall.Errno) { calls++; cancel(); return 0, syscall.EINTR })
	if exited || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("exited=%v err=%v calls=%d", exited, err, calls)
	}
	calls = 0
	exited, err = processPollExited(ctx, 74, func(int) (int16, syscall.Errno) { calls++; return 1, 0 })
	if exited || !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled probe issued a poll")
	}
}
func TestProcessPollInterruptExhaustion(t *testing.T) {
	calls := 0
	exited, err := processPollExited(context.Background(), 75, func(fd int) (int16, syscall.Errno) {
		if fd != 75 {
			t.Fatal("exhaustion changed pidfd")
		}
		calls++
		return 1, syscall.EINTR
	})
	if exited || !errors.Is(err, syscall.EINTR) || calls != 16 {
		t.Fatalf("exited=%v err=%v calls=%d", exited, err, calls)
	}
}
func TestProcessPollFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		revents int16
		errno   syscall.Errno
		want    error
	}{{"bad-fd", 1, syscall.EBADF, syscall.EBADF}, {"io", 16, syscall.EIO, syscall.EIO}, {"nval", 32, 0, ErrProcessUnverified}, {"err", 8, 0, ErrProcessUnverified}, {"in-and-err", 9, 0, ErrProcessUnverified}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			exited, err := processPollExited(context.Background(), 76, func(int) (int16, syscall.Errno) { calls++; return tc.revents, tc.errno })
			if exited || !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("exited=%v err=%v calls=%d", exited, err, calls)
			}
		})
	}
}
