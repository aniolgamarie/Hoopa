//go:build linux && amd64

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"hoopa/experiments/sourceidentity/acceptedwitness"
)

const (
	soCookie      = 57
	soNetNSCookie = 71
)

type controller interface{ Control(func(uintptr)) error }
type fdReader interface {
	integer(uintptr, int) (int, error)
	endpoint(uintptr, bool) (syscall.Sockaddr, error)
	cookie64(uintptr, int) (uint64, uint32, error)
}
type kernelReader struct{}

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
	var value uint64
	length := uint32(unsafe.Sizeof(value))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.SOL_SOCKET, uintptr(opt), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return 0, length, errno
	}
	return value, length, nil
}
func endpointText(addr syscall.Sockaddr) (string, error) {
	v, ok := addr.(*syscall.SockaddrInet4)
	if !ok || v == nil || v.Addr != [4]byte{127, 0, 0, 1} || v.Port < 1 || v.Port > 65535 {
		return "", errors.New("endpoint must be 127.0.0.1 with valid port")
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(v.Port)), nil
}

// All kernel reads observe the original descriptor within one Control window.
func captureSocket(control controller, reader fdReader) (socketObservation, error) {
	var result socketObservation
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
		var endpoints [2]string
		for i, peer := range []bool{false, true} {
			addr, err := reader.endpoint(fd, peer)
			if err != nil {
				readErr = err
				return
			}
			endpoints[i], err = endpointText(addr)
			if err != nil {
				readErr = err
				return
			}
		}
		var cookies [2]string
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
			cookies[i] = strconv.FormatUint(value, 10)
		}
		result = socketObservation{SocketCookie: cookies[0], NetNSCookie: cookies[1], LocalEndpoint: endpoints[0], RemoteEndpoint: endpoints[1]}
	})
	if controlErr != nil || readErr != nil {
		return socketObservation{}, errors.Join(controlErr, readErr)
	}
	if !called {
		return socketObservation{}, errors.New("Control did not invoke callback")
	}
	return result, nil
}

func readProcess(pid int, open func(string) (io.ReadCloser, error), inode func(string) (uint64, error)) (processObservation, error) {
	file, err := open("/proc/self/stat")
	if err != nil {
		return processObservation{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 4097))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return processObservation{}, err
	}
	ticks, err := parseStartTicks(raw, pid)
	if err != nil {
		return processObservation{}, err
	}
	ns, err := inode("/proc/self/ns/pid")
	if err != nil {
		return processObservation{}, err
	}
	if ns == 0 {
		return processObservation{}, errors.New("zero pid namespace inode")
	}
	return processObservation{PID: pid, StartTicks: ticks, PIDNSInode: strconv.FormatUint(ns, 10)}, nil
}
func actualProcess() (processObservation, error) {
	return readProcess(os.Getpid(), func(path string) (io.ReadCloser, error) { return os.Open(path) }, func(path string) (uint64, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, errors.New("pid namespace stat unavailable")
		}
		return stat.Ino, nil
	})
}

type tcpWitnessListener struct {
	tcp        *net.TCPListener
	generation uint64
}

func (l *tcpWitnessListener) Accept() (witnessStream, error) {
	conn, err := acceptedwitness.Accept(l.tcp, l.generation)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
func (l *tcpWitnessListener) SetDeadline(t time.Time) error { return l.tcp.SetDeadline(t) }
func (l *tcpWitnessListener) Close() error                  { return l.tcp.Close() }
func runFixture(o options, output io.Writer) error {
	return execute(o, output, runtimeDeps{
		now: time.Now,
		dial: func(port int, total time.Time) (clientStream, error) {
			dialer := net.Dialer{Deadline: total}
			conn, err := dialer.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return nil, err
			}
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				return nil, errors.Join(errors.New("original TCP connection required"), conn.Close())
			}
			return tcp, nil
		},
		listen: func(port int, generation uint64) (witnessListener, error) {
			tcp, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
			if err != nil {
				return nil, err
			}
			return &tcpWitnessListener{tcp: tcp, generation: generation}, nil
		},
		capture: func(conn clientStream) (socketObservation, error) {
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				return socketObservation{}, errors.New("original TCP connection required")
			}
			raw, err := tcp.SyscallConn()
			if err != nil {
				return socketObservation{}, err
			}
			return captureSocket(raw, kernelReader{})
		},
		process: actualProcess,
	})
}
