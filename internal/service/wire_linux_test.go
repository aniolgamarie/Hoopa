//go:build linux && amd64

package service

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/control"
)

// Task-owned socketpair supplies an already connected local transport. Peer
// authentication belongs to platform.PrivateUnixListener, not this codec.
func wirePair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, e := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	cs := []*net.UnixConn{}
	for _, fd := range fds {
		f := os.NewFile(uintptr(fd), "wire-fixture")
		c, e := net.FileConn(f)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		u := c.(*net.UnixConn)
		t.Cleanup(func() { u.Close() })
		cs = append(cs, u)
	}
	return cs[0], cs[1]
}
func wireContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}
func wireRequest() []byte {
	return []byte(`{"api_version":"v1","request_id":"request-one","operation":"status.get","params":{}}`)
}
func wireResponse() []byte {
	return []byte(`{"api_version":"v1","data":{"observed":"fixture"},"error":null,"ok":true,"request_id":"request-one","warnings":[]}`)
}
func wireFrame(raw []byte) []byte {
	p := make([]byte, 4+len(raw))
	binary.BigEndian.PutUint32(p, uint32(len(raw)))
	copy(p[4:], raw)
	return p
}
func wireSend(t *testing.T, c *net.UnixConn, payload []byte) {
	t.Helper()
	if e := c.SetWriteDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e := c.CloseWrite(); e != nil {
		t.Fatal(e)
	}
}
func TestWireExchange(t *testing.T) {
	client, server := wirePair(t)
	done := make(chan error, 1)
	go func() {
		raw, e := ReadRequest(wireContext(t), server)
		if e == nil && string(raw) != string(wireRequest()) {
			e = ErrWire
		}
		if e == nil {
			e = WriteResponse(wireContext(t), server, wireResponse())
		}
		done <- e
	}()
	raw, e := Exchange(wireContext(t), client, wireRequest())
	if e != nil || string(raw) != string(wireResponse()) {
		t.Fatal(e, string(raw))
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	// Success does not close the caller's descriptor. The write half is closed,
	// so this connection is single-use and a second Exchange cannot succeed.
	for _, conn := range []*net.UnixConn{client, server} {
		rc, e := conn.SyscallConn()
		if e != nil {
			t.Fatal("connection closed on success", e)
		}
		if e := rc.Control(func(uintptr) {}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Exchange(wireContext(t), client, wireRequest()); !errors.Is(e, ErrWire) {
		t.Fatal("second exchange succeeded", e)
	}
}
func TestWireReadRequestRejects(t *testing.T) {
	huge := make([]byte, 4)
	binary.BigEndian.PutUint32(huge, uint32(control.MaxRequestBytes+1))
	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{"zero", []byte{0, 0, 0, 0}}, {"oversize", huge}, {"short-header", []byte{0, 0}}, {"short-payload", append([]byte{0, 0, 0, 2}, '{')},
		{"trailing-byte", append(wireFrame(wireRequest()), 'x')}, {"second-frame", append(wireFrame(wireRequest()), wireFrame(wireRequest())...)},
		{"bad-json", wireFrame([]byte(`{"token":"do-not-echo"`))}, {"duplicate", wireFrame([]byte(`{"api_version":"v1","request_id":"a","request_id":"b","operation":"status.get","params":{}}`))},
		{"null-params", wireFrame([]byte(`{"api_version":"v1","request_id":"a","operation":"status.get","params":null}`))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := wirePair(t)
			wireSend(t, client, tc.bytes)
			_, e := ReadRequest(wireContext(t), server)
			if !errors.Is(e, ErrWire) || strings.Contains(e.Error(), "do-not-echo") {
				t.Fatal(e)
			}
		})
	}
}
func TestWireResponseStrict(t *testing.T) {
	goodFailure := []byte(`{"api_version":"v1","data":null,"error":{"code":"CONFLICT","details":{"current_revision":"r:1","expected_revision":"r:0","field":"revision","operation_id":"op:1","reason":"changed"},"message":"conflict","retryable":false},"ok":false,"request_id":null,"warnings":[{"code":"LIMITED","message":"fixture"}]}`)
	for _, raw := range [][]byte{wireResponse(), goodFailure} {
		client, server := wirePair(t)
		done := make(chan error, 1)
		go func() { done <- WriteResponse(wireContext(t), server, raw) }()
		got, e := readWireTestFrame(client)
		if e != nil || string(got) != string(raw) {
			t.Fatal(e)
		}
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"unknown-envelope", strings.Replace(string(wireResponse()), `"warnings":[]`, `"warnings":[],"extra":0`, 1)},
		{"missing", strings.Replace(string(wireResponse()), `"warnings":[]`, `"removed":[]`, 1)},
		{"null-success-id", strings.Replace(string(wireResponse()), `"request-one"`, `null`, 1)},
		{"success-error", strings.Replace(string(wireResponse()), `"error":null`, `"error":{}`, 1)},
		{"success-array", strings.Replace(string(wireResponse()), `{"observed":"fixture"}`, `[]`, 1)},
		{"warning-string", strings.Replace(string(wireResponse()), `"warnings":[]`, `"warnings":["warning"]`, 1)},
		{"warning-extra", strings.Replace(string(wireResponse()), `"warnings":[]`, `"warnings":[{"code":"a","extra":0,"message":"b"}]`, 1)},
		{"warning-empty", strings.Replace(string(wireResponse()), `"warnings":[]`, `"warnings":[{"code":"","message":"b"}]`, 1)},
		{"bad-error-code", strings.Replace(string(goodFailure), `"CONFLICT"`, `"NEW_SECRET_ERROR"`, 1)},
		{"bad-details-id", strings.Replace(string(goodFailure), `"r:1"`, `"bad revision"`, 1)},
		{"extra-details", strings.Replace(string(goodFailure), `"reason":"changed"`, `"reason":"changed","secret":"do-not-echo"`, 1)},
		{"failure-data", strings.Replace(string(goodFailure), `"data":null`, `"data":{}`, 1)},
		{"retryable-null", strings.Replace(string(goodFailure), `"retryable":false`, `"retryable":null`, 1)},
		{"invalid-unicode", `{"api_version":"v1","data":{},"error":null,"ok":true,"request_id":"\ud800","warnings":[]}`},
		{"duplicate", strings.Replace(string(wireResponse()), `"ok":true`, `"ok":true,"ok":true`, 1)},
		{"null-warnings", strings.Replace(string(wireResponse()), `"warnings":[]`, `"warnings":null`, 1)},
		{"oversize", strings.Repeat(" ", control.MaxResultBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := wirePair(t)
			e := WriteResponse(wireContext(t), server, []byte(tc.raw))
			if !errors.Is(e, ErrWire) || strings.Contains(e.Error(), "do-not-echo") {
				t.Fatal(e)
			}
			client.SetReadDeadline(time.Now().Add(15 * time.Millisecond))
			var one [1]byte
			n, e := client.Read(one[:])
			var timeout net.Error
			if n != 0 || !errors.As(e, &timeout) || !timeout.Timeout() {
				t.Fatal("invalid response wrote bytes or CloseWrite", e)
			}
		})
	}
}
func readWireTestFrame(c *net.UnixConn) ([]byte, error) {
	c.SetReadDeadline(time.Now().Add(time.Second))
	var h [4]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n > control.MaxResultBytes {
		return nil, ErrWire
	}
	raw := make([]byte, n)
	if _, e := io.ReadFull(c, raw); e != nil {
		return nil, e
	}
	var one [1]byte
	k, e := c.Read(one[:])
	if k != 0 || e != io.EOF {
		return nil, ErrWire
	}
	return raw, nil
}
func TestWireExchangeResponseBindingAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"wrong-id", wireFrame([]byte(strings.Replace(string(wireResponse()), `request-one`, `request-two`, 1)))},
		{"null-id", wireFrame([]byte(`{"api_version":"v1","data":null,"error":{"code":"INVALID_ARGUMENT","message":"fixture","retryable":false},"ok":false,"request_id":null,"warnings":[]}`))},
		{"extra-response", append(wireFrame(wireResponse()), wireFrame(wireResponse())...)},
		{"zero", []byte{0, 0, 0, 0}}, {"oversize", []byte{0, 1, 0, 1}}, {"truncated", []byte{0, 0, 0, 2, '{'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := wirePair(t)
			done := make(chan error, 1)
			go func() {
				_, e := ReadRequest(wireContext(t), server)
				if e == nil {
					_, e = server.Write(tc.frame)
					server.CloseWrite()
				}
				done <- e
			}()
			if _, e := Exchange(wireContext(t), client, wireRequest()); !errors.Is(e, ErrWire) {
				t.Fatal(e)
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestWireEarlyRequestValidation(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`null`), []byte(`{"secret":"do-not-echo"}`), make([]byte, control.MaxRequestBytes+1)} {
		client, server := wirePair(t)
		if _, e := Exchange(wireContext(t), client, raw); !errors.Is(e, ErrWire) {
			t.Fatal(e)
		}
		server.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		var one [1]byte
		n, e := server.Read(one[:])
		var timeout net.Error
		if n != 0 || !errors.As(e, &timeout) || !timeout.Timeout() {
			t.Fatal("invalid request wrote bytes or CloseWrite", e)
		}
	}
}
func TestWireReadDeadlineAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancel"}[cancelled], func(t *testing.T) {
			_, server := wirePair(t)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if cancelled {
				cancel()
			}
			start := time.Now()
			_, e := ReadRequest(ctx, server)
			want := context.DeadlineExceeded
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(e, ErrWire) || !errors.Is(e, want) || time.Since(start) > time.Second {
				t.Fatal(e)
			}
		})
	}
}
func TestWireBlockedWriteCancellation(t *testing.T) {
	for _, response := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "response"}[response], func(t *testing.T) {
			client, server := wirePair(t)
			if e := client.SetWriteBuffer(1024); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(wireContext(t))
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if response {
					done <- WriteResponse(ctx, client, []byte(`{"api_version":"v1","data":{"padding":"`+strings.Repeat("x", 60000)+`"},"error":null,"ok":true,"request_id":"request-one","warnings":[]}`))
				} else {
					_, e := Exchange(ctx, client, []byte(`{"api_version":"v1","request_id":"request-one","operation":"status.get","params":{"padding":"`+strings.Repeat("x", 120000)+`"}}`))
					done <- e
				}
			}()
			// Reading the header and first payload byte proves the large write began.
			// The remaining payload exceeds the explicitly small kernel send buffer.
			server.SetReadDeadline(time.Now().Add(time.Second))
			var prefix [5]byte
			if _, e := io.ReadFull(server, prefix[:]); e != nil {
				t.Fatal(e)
			}
			cancel()
			select {
			case e := <-done:
				if !errors.Is(e, ErrWire) || !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("blocked write did not cancel")
			}
		})
	}
}
func TestWireNilBoundaries(t *testing.T) {
	client, _ := wirePair(t)
	if _, e := ReadRequest(nil, client); !errors.Is(e, ErrWire) {
		t.Fatal(e)
	}
	if _, e := ReadRequest(context.Background(), nil); !errors.Is(e, ErrWire) {
		t.Fatal(e)
	}
	if e := WriteResponse(nil, client, wireResponse()); !errors.Is(e, ErrWire) {
		t.Fatal(e)
	}
	if _, e := Exchange(nil, client, wireRequest()); !errors.Is(e, ErrWire) {
		t.Fatal(e)
	}
}
func TestWirePreservesOriginalJSON(t *testing.T) {
	request := []byte(" { \"request_id\": \"request-one\", \"operation\":\"status.get\", \"params\":{}, \"api_version\": \"v1\" }\n")
	response := []byte(" {\"ok\":true,\"api_version\":\"v1\",\"request_id\":\"request-one\",\"error\":null,\"warnings\":[],\"data\":{\"literal\":\"\\u2028\"}}\n")
	client, server := wirePair(t)
	done := make(chan error, 1)
	go func() {
		raw, e := ReadRequest(wireContext(t), server)
		if e == nil && string(raw) != string(request) {
			e = ErrWire
		}
		if e == nil {
			e = WriteResponse(wireContext(t), server, response)
		}
		done <- e
	}()
	raw, e := Exchange(wireContext(t), client, request)
	if e != nil || string(raw) != string(response) {
		t.Fatal("wire rewrote original JSON", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestWireRequiresEOFEvenAfterCompletePayload(t *testing.T) {
	client, server := wirePair(t)
	if _, e := client.Write(wireFrame(wireRequest())); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, e := ReadRequest(ctx, server); !errors.Is(e, ErrWire) || !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("missing sender EOF accepted or lost timeout", e)
	}
}
func TestWireRawRequestBudgetBeforeNormalization(t *testing.T) {
	// A canonically tiny, whitespace-padded input must fail before any send.
	client, server := wirePair(t)
	raw := append(wireRequest(), []byte(strings.Repeat(" ", control.MaxRequestBytes))...)
	if _, e := Exchange(wireContext(t), client, raw); !errors.Is(e, ErrWire) {
		t.Fatal(e)
	}
	server.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	var one [1]byte
	n, e := server.Read(one[:])
	var timeout net.Error
	if n != 0 || !errors.As(e, &timeout) || !timeout.Timeout() {
		t.Fatal("raw over-budget request wrote bytes or CloseWrite", e)
	}
}
func TestWireCancellationDuringPartialRead(t *testing.T) {
	client, server := wirePair(t)
	raw := wireFrame(wireRequest())
	if _, e := client.Write(raw[:5]); e != nil {
		t.Fatal(e)
	}
	pending := func() bool {
		t.Helper()
		rc, e := server.SyscallConn()
		if e != nil {
			t.Fatal(e)
		}
		var peekErr error
		var n int
		e = rc.Control(func(fd uintptr) {
			var b [1]byte
			n, _, peekErr = syscall.Recvfrom(int(fd), b[:], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		})
		if e != nil {
			t.Fatal(e)
		}
		if peekErr != nil && !errors.Is(peekErr, syscall.EAGAIN) {
			t.Fatal(peekErr)
		}
		return n > 0
	}
	if !pending() {
		t.Fatal("partial original not queued before reader")
	}
	ctx, cancel := context.WithCancel(wireContext(t))
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := ReadRequest(ctx, server); done <- e }()
	// Non-consuming peeks prove the active reader consumed the prefix and is
	// awaiting its incomplete body, without sleeps as the synchronization oracle.
	deadline := time.Now().Add(time.Second)
	for pending() {
		if time.Now().After(deadline) {
			t.Fatal("reader never consumed partial payload")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, ErrWire) || !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("active read not canceled")
	}
}
