//go:build linux && amd64

package platform

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestLaunchCurrentProcessBirth(t *testing.T) {
	birth, err := CurrentProcessBirth(processTestContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if birth.PID != os.Getpid() || birth.UID != uint32(os.Geteuid()) || birth.StartTicks == 0 || !launchBootID(birth.BootID) {
		t.Fatal("incorrect current process birth:", birth)
	}
	raw, err := readProc("/proc/self/stat", 4096)
	if err != nil {
		t.Fatal(err)
	}
	pid, start, state, err := parseProcessStat(raw)
	if err != nil || pid != birth.PID || start != birth.StartTicks || state == "Z" || state == "X" || state == "x" {
		t.Fatal("birth does not match actual self stat")
	}
	boot, err := readProc("/proc/sys/kernel/random/boot_id", 64)
	if err != nil || string(boot) != birth.BootID+"\n" {
		t.Fatal("birth does not match actual boot ID:", err)
	}
	info, err := os.Stat("/proc/self")
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*syscall.Stat_t).Uid != birth.UID {
		t.Fatal("birth UID differs from proc owner")
	}
}

func TestLaunchBirthCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	birth, err := CurrentProcessBirth(ctx)
	if birth != (ProcessBirth{}) || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("birth cancellation not preserved:", birth, err)
	}
	if _, err := CurrentProcessBirth(nil); !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("nil context accepted")
	}
}

func TestLaunchBootCanonicalShape(t *testing.T) {
	for _, value := range []string{"00000000-0000-0000-0000-000000000000", "01234567-89ab-cdef-0123-456789abcdef"} {
		if !launchBootID(value) {
			t.Fatal("UUID version must not be restricted")
		}
	}
	for _, value := range []string{"", "0123456789abcdef0123456789abcdef", "01234567-89AB-cdef-0123-456789abcdef", "0123456-789ab-cdef-0123-456789abcdef0", "01234567-89ab-cdef-0123-456789abcdeg", "01234567-89ab-cdef-0123-456789abcdef\n"} {
		if launchBootID(value) {
			t.Fatal("noncanonical UUID accepted")
		}
	}
}

func TestLaunchVerifyArgumentsActualProcess(t *testing.T) {
	fixture := startProcessFixture(t)
	handle, err := ObserveProcess(processTestContext(t), fixture.cmd.Process.Pid, fixture.spec)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	args := append([]string(nil), fixture.cmd.Args...)
	if err := handle.VerifyArguments(processTestContext(t), args); err != nil {
		t.Fatal("exact exec argv rejected:", err)
	}
	for name, changed := range map[string][]string{
		"wrong-first":    append([]string{"/unrelated/binary"}, args[1:]...),
		"wrong-argument": {args[0], "-test.run=^Other$", args[2]},
		"extra-argument": append(append([]string(nil), args...), "sensitive-test-canary"),
		"prefix-only":    args[:len(args)-1],
	} {
		t.Run(name, func(t *testing.T) {
			err := handle.VerifyArguments(processTestContext(t), changed)
			if !errors.Is(err, ErrProcessUnverified) {
				t.Fatal("wrong argv accepted:", err)
			}
			if strings.Contains(err.Error(), "sensitive-test-canary") || strings.Contains(err.Error(), "-test.run") {
				t.Fatal("error disclosed argv")
			}
			if err := handle.Verify(processTestContext(t)); err != nil {
				t.Fatal("argv mismatch affected child:", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = handle.VerifyArguments(ctx, args)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("argument cancellation lost:", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.VerifyArguments(processTestContext(t), args); !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("closed handle accepted")
	}
	if _, err := liveStat(fixture.cmd.Process.Pid); err != nil {
		t.Fatal("closing handle affected child:", err)
	}
}

func TestLaunchArgumentBounds(t *testing.T) {
	const binary = "/core"
	for name, args := range map[string][]string{
		"none": nil, "wrong-executable": {"/wrong"}, "nul": {binary, "a\x00b"},
		"long-item":      {binary, strings.Repeat("é", 2049)},
		"too-many":       append([]string{binary}, make([]string, 64)...),
		"total-overflow": {binary, strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096), strings.Repeat("x", 4096)},
	} {
		t.Run(name, func(t *testing.T) {
			if launchArguments(binary, args) {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
	args := []string{binary}
	for i := 0; i < 7; i++ {
		args = append(args, strings.Repeat("x", 4096))
	}
	args = append(args, strings.Repeat("x", 4096-len(binary)))
	args = append(args, make([]string, 64-len(args))...)
	if !launchArguments(binary, args) {
		t.Fatal("exact byte/count bounds rejected")
	}
	if err := (*ProcessHandle)(nil).VerifyArguments(context.Background(), []string{binary}); !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("nil handle accepted")
	}
	if err := (&ProcessHandle{}).VerifyArguments(nil, []string{binary}); !errors.Is(err, ErrProcessUnverified) {
		t.Fatal("nil context accepted")
	}
}
