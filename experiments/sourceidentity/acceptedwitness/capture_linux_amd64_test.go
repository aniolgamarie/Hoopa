//go:build linux && amd64

package acceptedwitness

import (
	"errors"
	"math"
	"net"
	"reflect"
	"strconv"
	"syscall"
	"testing"
	"time"
)

type fakeControl struct {
	active bool
	calls  int
	skip   bool
	err    error
}

func (f *fakeControl) Control(fn func(uintptr)) error {
	f.calls++
	if !f.skip {
		f.active = true
		fn(42)
		f.active = false
	}
	return f.err
}

type fakeReader struct {
	t            *testing.T
	c            *fakeControl
	fail         string
	badInt       int
	badAddr      syscall.Sockaddr
	cookie       uint64
	length       uint32
	badCookieOpt int
	reads        []string
}

func (f *fakeReader) check(fd uintptr, name string) error {
	f.t.Helper()
	if !f.c.active || fd != 42 {
		f.t.Fatalf("read outside original Control: %s", name)
	}
	f.reads = append(f.reads, name)
	if f.fail == name {
		return errors.New("injected " + name)
	}
	return nil
}
func (f *fakeReader) integer(fd uintptr, opt int) (int, error) {
	name := strconv.Itoa(opt)
	if err := f.check(fd, name); err != nil {
		return 0, err
	}
	if f.fail == "mismatch-"+name {
		return f.badInt, nil
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
func (f *fakeReader) endpoint(fd uintptr, peer bool) (syscall.Sockaddr, error) {
	name := "local"
	if peer {
		name = "remote"
	}
	if err := f.check(fd, name); err != nil {
		return nil, err
	}
	if f.fail == "mismatch-"+name {
		return f.badAddr, nil
	}
	return &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 1234}, nil
}
func (f *fakeReader) cookie64(fd uintptr, opt int) (uint64, uint32, error) {
	if err := f.check(fd, strconv.Itoa(opt)); err != nil {
		return 0, 0, err
	}
	if f.badCookieOpt != 0 && opt != f.badCookieOpt {
		return 17, 8, nil
	}
	return f.cookie, f.length, nil
}
func fixture(t *testing.T) (*fakeControl, *fakeReader) {
	c := &fakeControl{}
	return c, &fakeReader{t: t, c: c, cookie: math.MaxUint64, length: 8}
}
func TestFakeCaptureSingleWindowAndPrecision(t *testing.T) {
	for _, v := range []uint64{math.MaxUint64, 1<<53 + 1} {
		t.Run(strconv.FormatUint(v, 10), func(t *testing.T) {
			c, r := fixture(t)
			r.cookie = v
			s, err := capture(c, r, math.MaxUint64)
			if err != nil {
				t.Fatal(err)
			}
			if c.calls != 1 || len(r.reads) != 7 {
				t.Fatalf("calls=%d reads=%v", c.calls, r.reads)
			}
			if s.SocketCookie != strconv.FormatUint(v, 10) || s.NetNSCookie != s.SocketCookie || s.Generation != "18446744073709551615" || s.Schema != "accepted-socket-witness-v1" || s.Association != "unverified" || s.IdentityCapability != "unverified" || s.LocalEndpoint != "127.0.0.1:1234" || s.RemoteEndpoint != s.LocalEndpoint {
				t.Fatalf("snapshot: %+v", s)
			}
		})
	}
}
func TestFakeCaptureRejectsPartial(t *testing.T) {
	for _, stage := range []string{strconv.Itoa(syscall.SO_DOMAIN), strconv.Itoa(syscall.SO_TYPE), strconv.Itoa(syscall.SO_PROTOCOL), "local", "remote", "57", "71"} {
		t.Run(stage, func(t *testing.T) { c, r := fixture(t); r.fail = stage; assertEmpty(t, c, r) })
	}
	for _, opt := range []int{57, 71} {
		for _, n := range []uint32{0, 4, 7, 9} {
			t.Run("length-"+strconv.Itoa(opt)+"-"+strconv.Itoa(int(n)), func(t *testing.T) { c, r := fixture(t); r.badCookieOpt = opt; r.length = n; assertEmpty(t, c, r) })
		}
		t.Run("zero-cookie-"+strconv.Itoa(opt), func(t *testing.T) { c, r := fixture(t); r.badCookieOpt = opt; r.cookie = 0; assertEmpty(t, c, r) })
	}
	for _, opt := range []int{syscall.SO_DOMAIN, syscall.SO_TYPE, syscall.SO_PROTOCOL} {
		t.Run("wrong"+strconv.Itoa(opt), func(t *testing.T) {
			c, r := fixture(t)
			r.fail = "mismatch-" + strconv.Itoa(opt)
			r.badInt = -1
			assertEmpty(t, c, r)
		})
	}
	for _, stage := range []string{"local", "remote"} {
		for _, addr := range []syscall.Sockaddr{nil, &syscall.SockaddrInet6{}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 2}, Port: 80}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}, Port: 65536}} {
			t.Run(stage, func(t *testing.T) {
				c, r := fixture(t)
				r.fail = "mismatch-" + stage
				r.badAddr = addr
				assertEmpty(t, c, r)
			})
		}
	}
	t.Run("callback-not-called", func(t *testing.T) { c, r := fixture(t); c.skip = true; assertEmpty(t, c, r) })
	t.Run("outer-error-after-success", func(t *testing.T) { c, r := fixture(t); c.err = errors.New("outer"); assertEmpty(t, c, r) })
}
func assertEmpty(t *testing.T, c *fakeControl, r *fakeReader) {
	t.Helper()
	s, err := capture(c, r, 1)
	if err == nil || s != (Snapshot{}) {
		t.Fatalf("published partial snapshot: %+v err=%v", s, err)
	}
}
func TestFakeConnBoundary(t *testing.T) {
	var zero Conn
	for _, c := range []*Conn{nil, &zero} {
		if _, err := c.Snapshot(); err == nil {
			t.Fatal("empty snapshot accepted")
		}
		if _, err := c.Read(nil); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		if _, err := c.Write(nil); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		if err := c.Close(); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		if c.LocalAddr() != nil || c.RemoteAddr() != nil {
			t.Fatal("empty addresses")
		}
		for _, f := range []func(time.Time) error{c.SetDeadline, c.SetReadDeadline, c.SetWriteDeadline} {
			if !errors.Is(f(time.Time{}), net.ErrClosed) {
				t.Fatal("empty deadline")
			}
		}
	}
	c := &Conn{tcp: &net.TCPConn{}, snapshot: Snapshot{Schema: "test"}}
	s, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s.Schema = "changed"
	if got, _ := c.Snapshot(); got.Schema != "test" {
		t.Fatal("mutable snapshot")
	}
	_ = c.Close()
	if got, err := c.Snapshot(); err != nil || got.Schema != "test" {
		t.Fatal("historical snapshot lost", err)
	}
	typ := reflect.TypeOf(Conn{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Fatal("public mutable field")
		}
	}
	fn := reflect.TypeOf(Accept)
	if fn.NumIn() != 2 || fn.In(0) != reflect.TypeOf((*net.TCPListener)(nil)) || fn.In(1).Kind() != reflect.Uint64 {
		t.Fatal("Accept boundary")
	}
	if _, err := Accept(nil, 1); err == nil {
		t.Fatal("nil listener")
	}
	if _, err := Accept(&net.TCPListener{}, 0); err == nil {
		t.Fatal("zero generation")
	}
}

func TestFakeCaptureFailureCloseError(t *testing.T) {
	captureErr := errors.New("injected capture failure")
	c, err := finishCapture(&net.TCPConn{}, Snapshot{Schema: "must not publish"}, captureErr)
	if c != nil {
		t.Fatal("published connection after capture failure")
	}
	if !errors.Is(err, captureErr) || !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("capture and actual TCPConn.Close errors must both survive: %v", err)
	}
}
