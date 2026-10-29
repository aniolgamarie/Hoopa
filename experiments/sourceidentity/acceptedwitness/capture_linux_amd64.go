//go:build linux && amd64

package acceptedwitness

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	soCookie      = 57
	soNetNSCookie = 71
)

type controller interface{ Control(func(uintptr)) error }
type socketReader interface {
	integer(uintptr, int) (int, error)
	endpoint(uintptr, bool) (syscall.Sockaddr, error)
	cookie64(uintptr, int) (uint64, uint32, error)
}
type kernelReader struct{}

func supportedPlatform() error { return nil }
func captureTCP(tcp *net.TCPConn, generation uint64) (Snapshot, error) {
	raw, err := tcp.SyscallConn()
	if err != nil {
		return Snapshot{}, err
	}
	return capture(raw, kernelReader{}, generation)
}

// All descriptor reads are performed within a single Control callback. The Go
// runtime keeps Close from recycling the descriptor during this window. This
// makes no claim against hostile in-process raw close/unsafe or BPF association.
func capture(control controller, reader socketReader, generation uint64) (Snapshot, error) {
	if generation == 0 {
		return Snapshot{}, errors.New("generation must be nonzero")
	}
	var result Snapshot
	var readErr error
	called := false
	controlErr := control.Control(func(fd uintptr) {
		called = true
		for _, option := range []struct{ opt, want int }{{syscall.SO_DOMAIN, syscall.AF_INET}, {syscall.SO_TYPE, syscall.SOCK_STREAM}, {syscall.SO_PROTOCOL, syscall.IPPROTO_TCP}} {
			got, err := reader.integer(fd, option.opt)
			if err != nil {
				readErr = fmt.Errorf("socket option %d: %w", option.opt, err)
				return
			}
			if got != option.want {
				readErr = fmt.Errorf("unsupported socket option %d: %d", option.opt, got)
				return
			}
		}
		local, err := reader.endpoint(fd, false)
		if err != nil {
			readErr = fmt.Errorf("getsockname: %w", err)
			return
		}
		localText, err := endpointText(local)
		if err != nil {
			readErr = fmt.Errorf("local endpoint: %w", err)
			return
		}
		remote, err := reader.endpoint(fd, true)
		if err != nil {
			readErr = fmt.Errorf("getpeername: %w", err)
			return
		}
		remoteText, err := endpointText(remote)
		if err != nil {
			readErr = fmt.Errorf("remote endpoint: %w", err)
			return
		}
		cookies := [2]uint64{}
		for i, opt := range []int{soCookie, soNetNSCookie} {
			value, length, err := reader.cookie64(fd, opt)
			if err != nil {
				readErr = fmt.Errorf("cookie option %d: %w", opt, err)
				return
			}
			if length != 8 || value == 0 {
				readErr = fmt.Errorf("cookie option %d: invalid length %d or zero value", opt, length)
				return
			}
			cookies[i] = value
		}
		result = Snapshot{Schema: "accepted-socket-witness-v1", Generation: strconv.FormatUint(generation, 10), SocketCookie: strconv.FormatUint(cookies[0], 10), NetNSCookie: strconv.FormatUint(cookies[1], 10), LocalEndpoint: localText, RemoteEndpoint: remoteText, Association: "unverified", IdentityCapability: "unverified"}
	})
	if controlErr != nil || readErr != nil {
		return Snapshot{}, errors.Join(controlErr, readErr)
	}
	if !called {
		return Snapshot{}, errors.New("Control did not invoke callback")
	}
	return result, nil
}
func endpointText(addr syscall.Sockaddr) (string, error) {
	v, ok := addr.(*syscall.SockaddrInet4)
	if !ok || v == nil || v.Addr != [4]byte{127, 0, 0, 1} || v.Port < 1 || v.Port > 65535 {
		return "", errors.New("requires 127.0.0.1 with valid port")
	}
	return net.JoinHostPort(net.IP(v.Addr[:]).String(), strconv.Itoa(v.Port)), nil
}
func (kernelReader) integer(fd uintptr, opt int) (int, error) {
	return syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, opt)
}
func (kernelReader) endpoint(fd uintptr, peer bool) (syscall.Sockaddr, error) {
	if peer {
		return syscall.Getpeername(int(fd))
	}
	return syscall.Getsockname(int(fd))
}
func (kernelReader) cookie64(fd uintptr, opt int) (uint64, uint32, error) {
	// The kernel writes a native-endian uint64 directly; no int or float conversion.
	var value uint64
	length := uint32(unsafe.Sizeof(value))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.SOL_SOCKET, uintptr(opt), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return 0, length, errno
	}
	return value, length, nil
}
