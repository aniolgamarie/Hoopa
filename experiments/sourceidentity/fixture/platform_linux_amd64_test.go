//go:build linux && amd64

package main

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type fakeControl struct {
	active bool
	calls  int
	skip   bool
	err    error
}

func (c *fakeControl) Control(fn func(uintptr)) error {
	c.calls++
	if !c.skip {
		c.active = true
		fn(42)
		c.active = false
	}
	return c.err
}

type fakeFDReader struct {
	t         *testing.T
	control   *fakeControl
	fail      string
	badInt    int
	addr      syscall.Sockaddr
	badCookie int
	value     uint64
	length    uint32
	reads     []string
}

func (r *fakeFDReader) check(fd uintptr, name string) error {
	r.t.Helper()
	if !r.control.active || fd != 42 {
		r.t.Fatal("read outside original FD control window", name, fd)
	}
	r.reads = append(r.reads, name)
	if r.fail == name {
		return errors.New("injected " + name)
	}
	return nil
}
func (r *fakeFDReader) integer(fd uintptr, opt int) (int, error) {
	name := strconv.Itoa(opt)
	if err := r.check(fd, name); err != nil {
		return 0, err
	}
	if r.fail == "wrong-"+name {
		return r.badInt, nil
	}
	switch opt {
	case syscall.SO_DOMAIN:
		return syscall.AF_INET, nil
	case syscall.SO_TYPE:
		return syscall.SOCK_STREAM, nil
	default:
		return syscall.IPPROTO_TCP, nil
	}
}
func (r *fakeFDReader) endpoint(fd uintptr, peer bool) (syscall.Sockaddr, error) {
	name := "local"
	if peer {
		name = "remote"
	}
	if err := r.check(fd, name); err != nil {
		return nil, err
	}
	if r.fail == "wrong-"+name {
		return r.addr, nil
	}
	return &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 8080}, nil
}
func (r *fakeFDReader) cookie64(fd uintptr, opt int) (uint64, uint32, error) {
	if err := r.check(fd, strconv.Itoa(opt)); err != nil {
		return 0, 0, err
	}
	if r.badCookie != 0 && opt != r.badCookie {
		return 17, 8, nil
	}
	return r.value, r.length, nil
}
func readerFixture(t *testing.T) (*fakeControl, *fakeFDReader) {
	c := &fakeControl{}
	return c, &fakeFDReader{t: t, control: c, value: math.MaxUint64, length: 8}
}
func TestOriginalFDCaptureOneWindowAndPrecision(t *testing.T) {
	for _, v := range []uint64{math.MaxUint64, 1<<53 + 1} {
		c, r := readerFixture(t)
		r.value = v
		got, err := captureSocket(c, r)
		if err != nil {
			t.Fatal(err)
		}
		if got.SocketCookie != strconv.FormatUint(v, 10) || got.NetNSCookie != got.SocketCookie || got.LocalEndpoint != "127.0.0.1:8080" || got.RemoteEndpoint != got.LocalEndpoint || c.calls != 1 || len(r.reads) != 7 {
			t.Fatalf("%+v calls=%d reads=%v", got, c.calls, r.reads)
		}
	}
}
func assertCaptureRejected(t *testing.T, c *fakeControl, r *fakeFDReader) {
	t.Helper()
	result, err := captureSocket(c, r)
	if err == nil || result != (socketObservation{}) {
		t.Fatalf("partial result %+v err=%v", result, err)
	}
}
func TestOriginalFDCaptureErrors(t *testing.T) {
	for _, stage := range []string{strconv.Itoa(syscall.SO_DOMAIN), strconv.Itoa(syscall.SO_TYPE), strconv.Itoa(syscall.SO_PROTOCOL), "local", "remote", "57", "71"} {
		t.Run(stage, func(t *testing.T) { c, r := readerFixture(t); r.fail = stage; assertCaptureRejected(t, c, r) })
	}
	for _, opt := range []int{syscall.SO_DOMAIN, syscall.SO_TYPE, syscall.SO_PROTOCOL} {
		c, r := readerFixture(t)
		r.fail = "wrong-" + strconv.Itoa(opt)
		r.badInt = -1
		assertCaptureRejected(t, c, r)
	}
	for _, option := range []int{57, 71} {
		for _, length := range []uint32{0, 4, 7, 9, 16} {
			c, r := readerFixture(t)
			r.badCookie = option
			r.length = length
			assertCaptureRejected(t, c, r)
		}
		c, r := readerFixture(t)
		r.badCookie = option
		r.value = 0
		assertCaptureRejected(t, c, r)
	}
	for _, endpoint := range []string{"local", "remote"} {
		for _, addr := range []syscall.Sockaddr{nil, (*syscall.SockaddrInet4)(nil), &syscall.SockaddrInet6{}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 2}, Port: 1}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 0}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 65536}} {
			c, r := readerFixture(t)
			r.fail = "wrong-" + endpoint
			r.addr = addr
			assertCaptureRejected(t, c, r)
		}
	}
	c, r := readerFixture(t)
	c.skip = true
	assertCaptureRejected(t, c, r)
	c, r = readerFixture(t)
	c.err = errors.New("Control failed after callback")
	assertCaptureRejected(t, c, r)
}

type failCloser struct {
	io.Reader
	closeErr error
	closed   int
}

func (c *failCloser) Close() error { c.closed++; return c.closeErr }
func TestProcessIndependentBoundedRead(t *testing.T) {
	stat := &failCloser{Reader: bytes.NewReader(procStat(42, "tricky ) comm", "9007199254740993"))}
	got, err := readProcess(42, func(path string) (io.ReadCloser, error) {
		if path != "/proc/self/stat" {
			t.Fatal(path)
		}
		return stat, nil
	}, func(path string) (uint64, error) {
		if path != "/proc/self/ns/pid" {
			t.Fatal(path)
		}
		return math.MaxUint64, nil
	})
	if err != nil || got.PID != 42 || got.StartTicks != "9007199254740993" || got.PIDNSInode != "18446744073709551615" || stat.closed != 1 {
		t.Fatalf("%+v %v closed=%d", got, err, stat.closed)
	}
	for _, mode := range []string{"open", "read", "close", "oversize", "pid", "inode-error", "inode-zero"} {
		t.Run(mode, func(t *testing.T) {
			file := &failCloser{Reader: bytes.NewReader(procStat(42, "x", "1"))}
			if mode == "oversize" {
				file.Reader = strings.NewReader(strings.Repeat("x", 8192))
			}
			if mode == "pid" {
				file.Reader = bytes.NewReader(procStat(43, "x", "1"))
			}
			if mode == "close" {
				file.closeErr = errors.New("close")
			}
			if mode == "read" {
				file.Reader = errorReader{}
			}
			result, err := readProcess(42, func(string) (io.ReadCloser, error) {
				if mode == "open" {
					return nil, errors.New("open")
				}
				return file, nil
			}, func(string) (uint64, error) {
				if mode == "inode-error" {
					return 0, errors.New("inode")
				}
				if mode == "inode-zero" {
					return 0, nil
				}
				return 1, nil
			})
			if err == nil || result != (processObservation{}) {
				t.Fatalf("%+v %v", result, err)
			}
			want := 1
			if mode == "open" {
				want = 0
			}
			if file.closed != want {
				t.Fatal("close count", file.closed)
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read") }
