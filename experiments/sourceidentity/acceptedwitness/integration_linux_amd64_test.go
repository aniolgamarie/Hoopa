//go:build linux && amd64

package acceptedwitness

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func loopbackListener(t *testing.T) *net.TCPListener {
	t.Helper()
	if os.Getenv("HOOPA_WITNESS_LOOPBACK") != "1" {
		t.Skip("requires explicit HOOPA_WITNESS_LOOPBACK=1 authorization")
	}
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err = l.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return l
}
func dialLoopback(t *testing.T, l *net.TCPListener) *net.TCPConn {
	t.Helper()
	c, err := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err = c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestLoopbackOriginalSocket(t *testing.T) {
	l := loopbackListener(t)
	var previous Snapshot
	for i := 1; i <= 2; i++ {
		client := dialLoopback(t, l)
		c, err := Accept(l, uint64(i))
		if err != nil {
			if errors.Is(err, syscall.ENOPROTOOPT) {
				if c != nil {
					t.Fatal("published failed capture")
				}
				var b [1]byte
				if _, readErr := client.Read(b[:]); !errors.Is(readErr, io.EOF) {
					t.Fatalf("capture failure did not close accepted socket: %v", readErr)
				}
				// Listener ownership stays with the caller even after capture rejection.
				_ = dialLoopback(t, l)
				raw, acceptErr := l.AcceptTCP()
				if acceptErr != nil {
					t.Fatal(acceptErr)
				}
				_ = raw.Close()
				t.Skipf("positive witness UNVERIFIED: kernel cookie unsupported; strict rejection and accepted-socket cleanup observed: %v", err)
			}
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		s, err := c.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		if s.LocalEndpoint != l.Addr().String() || s.RemoteEndpoint != client.LocalAddr().String() {
			t.Fatalf("endpoints %+v", s)
		}
		// Re-read the still-private original socket to prove the wrapper binds to it.
		actual, err := captureTCP(c.tcp, uint64(i))
		if err != nil || actual != s {
			t.Fatalf("original mismatch: %+v %v", actual, err)
		}
		if i == 2 && s.SocketCookie == previous.SocketCookie {
			t.Fatal("independent accepted sockets share cookie")
		}
		previous = s
		s.SocketCookie = "changed"
		if copy, _ := c.Snapshot(); copy != actual {
			t.Fatal("mutable snapshot")
		}
		if err = c.Close(); err != nil {
			t.Fatal(err)
		}
		if history, err := c.Snapshot(); err != nil || history != actual {
			t.Fatal("closed history", err)
		}
	}
}
func TestLoopbackControlCloseWindow(t *testing.T) {
	l := loopbackListener(t)
	_ = dialLoopback(t, l)
	tcp, err := l.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	requested := make(chan struct{})
	var inner error
	err = raw.Control(func(fd uintptr) {
		before, n, e := (kernelReader{}).cookie64(fd, soCookie)
		if e != nil || n != 8 || before == 0 {
			inner = errors.New("initial SO_COOKIE unavailable")
			return
		}
		go func() { close(requested); done <- tcp.Close() }()
		<-requested
		// Wait until Go Close has marked the connection closed, without waiting
		// for descriptor destruction (which must wait for this callback).
		until := time.Now().Add(5 * time.Second)
		for {
			e := tcp.SetReadDeadline(time.Now().Add(time.Second))
			if errors.Is(e, net.ErrClosed) {
				break
			}
			if e != nil || time.Now().After(until) {
				inner = errors.New("Go Close did not mark socket closed")
				return
			}
			runtime.Gosched()
		}
		// Reads continue inside the runtime's descriptor reference despite Go Close.
		for i := 0; i < 100; i++ {
			after, n, e := (kernelReader{}).cookie64(fd, soCookie)
			if e != nil || n != 8 || after != before {
				inner = errors.New("descriptor changed during Control")
				return
			}
		}
	})
	if err != nil || inner != nil {
		t.Fatalf("Control=%v reads=%v", err, inner)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after Control")
	}
}
