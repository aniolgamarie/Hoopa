//go:build linux && amd64

package coreadmission

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

type nativeBackend struct{ call func(uint32, []byte) error }

func newBackend() (backend, error) { return &nativeBackend{call: rawBPF}, nil }
func rawBPF(command uint32, attr []byte) error {
	r, _, errno := syscall.Syscall(321, uintptr(command), uintptr(unsafe.Pointer(&attr[0])), uintptr(len(attr)))
	runtime.KeepAlive(attr)
	if errno != 0 {
		return errno
	}
	if r != 0 {
		return fmt.Errorf("BPF command %d unexpected positive return %d", command, r)
	}
	return nil
}
func ptr(raw []byte) uint64 {
	if len(raw) == 0 {
		return 0
	}
	return uint64(uintptr(unsafe.Pointer(&raw[0])))
}

// Pin before encoding addresses into integer ABI fields. KeepAlive alone does
// not prevent stack movement between address encoding and the syscall.
func pinBuffers(buffers ...[]byte) func() {
	var pins runtime.Pinner
	for _, buffer := range buffers {
		if len(buffer) > 0 {
			pins.Pin(&buffer[0])
		}
	}
	return pins.Unpin
}

func (b *nativeBackend) readInfo(fd int, buffer []byte) ([]byte, error) {
	unpin := pinBuffers(buffer)
	defer unpin()
	attr := make([]byte, 16)
	le.PutUint32(attr, uint32(fd))
	le.PutUint32(attr[4:], uint32(len(buffer)))
	le.PutUint64(attr[8:], ptr(buffer))
	err := b.call(15, attr)
	runtime.KeepAlive(buffer)
	if err != nil {
		return nil, err
	}
	n := le.Uint32(attr[4:])
	if n > uint32(len(buffer)) {
		return nil, errors.New("oversized kernel info")
	}
	return buffer[:n], nil
}
func (b *nativeBackend) info(fd int) ([]byte, error) { return b.readInfo(fd, make([]byte, 256)) }
func (b *nativeBackend) programInfo(fd int) ([]byte, []uint32, error) {
	first, err := b.info(fd)
	if err != nil {
		return nil, nil, err
	}
	if len(first) < 80 {
		return nil, nil, errors.New("short program info")
	}
	count := le.Uint32(first[52:])
	if count > 16 {
		return nil, nil, errors.New("program map count exceeds bound")
	}
	// A byte array avoids a second pointer-bearing Go object in bpf_attr.
	ids := make([]byte, count*4)
	unpin := pinBuffers(ids)
	defer unpin()
	buf := make([]byte, 256)
	le.PutUint32(buf[52:], count)
	le.PutUint64(buf[56:], ptr(ids))
	second, err := b.readInfo(fd, buf)
	runtime.KeepAlive(ids)
	if err != nil {
		return nil, nil, err
	}
	if len(second) < 80 || le.Uint32(second[52:]) > count || string(second[:8]) != string(first[:8]) || string(second[64:80]) != string(first[64:80]) {
		return nil, nil, errors.New("program info changed/short")
	}
	out := make([]uint32, le.Uint32(second[52:]))
	for i := range out {
		out[i] = le.Uint32(ids[i*4:])
	}
	return second, out, nil
}
func fcntl(fd, command, arg int) (int, error) {
	r, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(command), uintptr(arg))
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}
func (b *nativeBackend) access(fd int) (int, error) {
	flags, err := fcntl(fd, syscall.F_GETFL, 0)
	return flags & syscall.O_ACCMODE, err
}
func (b *nativeBackend) cloexec(fd int) error {
	flags, err := fcntl(fd, syscall.F_GETFD, 0)
	if err != nil {
		return err
	}
	_, err = fcntl(fd, syscall.F_SETFD, flags|syscall.FD_CLOEXEC)
	return err
}
func (b *nativeBackend) lookup(fd int, key []byte, size int, flags uint64) ([]byte, error) {
	out := make([]byte, size)
	unpin := pinBuffers(key, out)
	defer unpin()
	attr := make([]byte, 32)
	le.PutUint32(attr, uint32(fd))
	le.PutUint64(attr[8:], ptr(key))
	le.PutUint64(attr[16:], ptr(out))
	le.PutUint64(attr[24:], flags)
	err := b.call(1, attr)
	runtime.KeepAlive(key)
	runtime.KeepAlive(out)
	return out, err
}
func (b *nativeBackend) nextKey(fd int, size int) error {
	out := make([]byte, size)
	unpin := pinBuffers(out)
	defer unpin()
	attr := make([]byte, 32)
	le.PutUint32(attr, uint32(fd))
	le.PutUint64(attr[16:], ptr(out))
	err := b.call(4, attr)
	runtime.KeepAlive(out)
	return err
}
func (b *nativeBackend) update(fd int, key, value []byte) error {
	unpin := pinBuffers(key, value)
	defer unpin()
	attr := make([]byte, 32)
	le.PutUint32(attr, uint32(fd))
	le.PutUint64(attr[8:], ptr(key))
	le.PutUint64(attr[16:], ptr(value))
	err := b.call(2, attr)
	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
	return err
}
func (b *nativeBackend) testRun(fd int) error {
	packet := make([]byte, 64)
	unpin := pinBuffers(packet)
	defer unpin()
	attr := make([]byte, 80)
	le.PutUint32(attr, uint32(fd))
	le.PutUint32(attr[8:], 64)
	le.PutUint64(attr[16:], ptr(packet))
	le.PutUint32(attr[32:], 1)
	err := b.call(10, attr)
	runtime.KeepAlive(packet)
	if err != nil {
		return err
	}
	if le.Uint32(attr[4:]) != 0 {
		return fmt.Errorf("stop program retval %d", le.Uint32(attr[4:]))
	}
	return nil
}
func (b *nativeBackend) close(fd int) error { return syscall.Close(fd) }
func inode(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("stat ABI")
	}
	return s.Ino, nil
}
func read(path string) (string, error) {
	raw, err := os.ReadFile(path)
	return strings.TrimSpace(string(raw)), err
}
func (b *nativeBackend) environment(h handoff) error {
	cmdline, err := read("/proc/cmdline")
	if err != nil {
		return err
	}
	lab := false
	for _, arg := range strings.Fields(cmdline) {
		if arg == "hoopa_lab=t132" {
			lab = true
		}
	}
	if os.Geteuid() != 0 || !lab {
		return errors.New("dedicated root t132 guest required")
	}
	// This prototype relies on the fixed guest's TCP_INFO TFO_CHILD ABI.
	// The launcher separately pins the kernel image hash; the release check
	// refuses accidental use on kernels where a zero bit would be ambiguous.
	release, err := read("/proc/sys/kernel/osrelease")
	if err != nil {
		return err
	}
	if err := validateKernelRelease(release); err != nil {
		return err
	}
	boot, err := read("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	if boot != h.BootID {
		return errors.New("boot ID mismatch")
	}
	namespaces := []string{"pid", "net", "user", "time", "time_for_children"}
	if h.scoped() {
		namespaces = append(namespaces, "mnt")
	}
	for _, ns := range namespaces {
		self, err := inode("/proc/self/ns/" + ns)
		if err != nil {
			return err
		}
		initial, err := inode("/proc/1/ns/" + ns)
		if err != nil {
			return err
		}
		if !h.namespaceMatches(ns, self, initial) {
			return fmt.Errorf("initial guest namespace mismatch: %s", ns)
		}
	}
	mounts, err := read("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	matches := 0
	for _, line := range strings.Split(mounts, "\n") {
		parts := strings.Split(line, " - ")
		if len(parts) != 2 {
			continue
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) > 4 && left[4] == "/sys/fs/cgroup" {
			if len(right) == 0 || right[0] != "cgroup2" || left[3] != "/" {
				return errors.New("cgroup2 root mount required")
			}
			matches++
		}
	}
	if matches != 1 {
		return errors.New("one cgroup2 root mount required")
	}
	info, err := os.Lstat(h.SourcePath)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source leaf must be a directory")
	}
	kind, err := read(filepath.Join(h.SourcePath, "cgroup.type"))
	if err != nil {
		return err
	}
	if kind != "domain" {
		return errors.New("domain source leaf required")
	}
	for path, want := range map[string]string{h.SourcePath: h.SourceCgroupID, "/sys/fs/cgroup": h.RootCgroupID} {
		got, err := inode(path)
		if err != nil {
			return err
		}
		n, _ := u64(want)
		if got != n {
			return errors.New("actual cgroup inode mismatch")
		}
	}
	membership, err := read("/proc/self/cgroup")
	if err != nil {
		return err
	}
	if membership != "0::/"+filepath.Base(h.SourcePath) {
		return errors.New("current process not in configured source leaf")
	}
	return nil
}
func socketU64(fd int, option int) (uint64, error) {
	var value uint64
	size := uint32(8)
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), syscall.SOL_SOCKET, uintptr(option), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&size)), 0)
	runtime.KeepAlive(&value)
	runtime.KeepAlive(&size)
	if errno != 0 {
		return 0, errno
	}
	if size != 8 {
		return 0, errors.New("socket cookie option size")
	}
	return value, nil
}
func endpoint(addr syscall.Sockaddr) (Endpoint, error) {
	v, ok := addr.(*syscall.SockaddrInet4)
	if !ok || v.Addr != [4]byte{127, 0, 0, 1} || v.Port < 1 || v.Port > 65535 {
		return Endpoint{}, errors.New("non-loopback IPv4 endpoint")
	}
	return Endpoint{"127.0.0.1", uint16(v.Port)}, nil
}
func (b *nativeBackend) socket(conn *net.TCPConn) (witness, error) {
	var w witness
	if conn == nil {
		return w, errors.New("nil original TCPConn")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return w, err
	}
	return captureWithControl(raw.Control, captureSocket)
}

func captureWithControl(control func(func(uintptr)) error, capture func(int) (witness, error)) (witness, error) {
	var w witness
	var captureErr error
	calls := 0
	err := control(func(fd uintptr) { calls++; w, captureErr = capture(int(fd)) })
	if calls != 1 {
		return witness{}, errors.Join(err, captureErr, errors.New("original socket Control callback count"))
	}
	if err != nil || captureErr != nil {
		return witness{}, errors.Join(err, captureErr)
	}
	return w, nil
}
func captureSocket(fd int) (witness, error) {
	var w witness
	for _, item := range []struct{ option, want int }{{39, syscall.AF_INET}, {syscall.SO_TYPE, syscall.SOCK_STREAM}, {38, syscall.IPPROTO_TCP}} {
		got, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, item.option)
		if err != nil {
			return w, err
		}
		if got != item.want {
			return w, errors.New("original socket must be ordinary IPv4 TCP")
		}
	}
	if err := rejectFastOpenChild(fd); err != nil {
		return w, err
	}
	var err error
	w.cookie, err = socketU64(fd, 57)
	if err != nil {
		return w, err
	}
	w.netns, err = socketU64(fd, 71)
	if err != nil {
		return w, err
	}
	local, err := syscall.Getsockname(fd)
	if err != nil {
		return w, err
	}
	remote, err := syscall.Getpeername(fd)
	if err != nil {
		return w, err
	}
	w.local, err = endpoint(local)
	if err != nil {
		return w, err
	}
	w.remote, err = endpoint(remote)
	return w, err
}
