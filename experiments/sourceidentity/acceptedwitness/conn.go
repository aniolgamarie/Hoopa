// Package acceptedwitness captures a bounded observation of an original accepted
// socket. It does not establish process identity or continued socket liveness.
package acceptedwitness

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// Snapshot is a historical observation. Generation is caller registration context,
// not a claim of complete collection. Both association fields remain unverified.
type Snapshot struct {
	Schema             string `json:"schema"`
	Generation         string `json:"generation"`
	SocketCookie       string `json:"socket_cookie"`
	NetNSCookie        string `json:"netns_cookie"`
	LocalEndpoint      string `json:"local_endpoint"`
	RemoteEndpoint     string `json:"remote_endpoint"`
	Association        string `json:"association"`
	IdentityCapability string `json:"identity_capability"`
}

// Conn owns the original accepted TCP connection and an immutable observation.
// It deliberately exposes neither a raw descriptor nor an unwrap operation.
type Conn struct {
	tcp      *net.TCPConn
	snapshot Snapshot
}

var _ net.Conn = (*Conn)(nil)

// Accept captures the original socket before returning any wrapper to the caller.
// Unsupported platforms and invalid registration context fail before accept.
func Accept(listener *net.TCPListener, generation uint64) (*Conn, error) {
	if listener == nil {
		return nil, errors.New("accepted witness: nil listener")
	}
	if generation == 0 {
		return nil, errors.New("accepted witness: generation must be nonzero")
	}
	if err := supportedPlatform(); err != nil {
		return nil, err
	}
	tcp, err := listener.AcceptTCP()
	if err != nil {
		return nil, err
	}
	snapshot, err := captureTCP(tcp, generation)
	return finishCapture(tcp, snapshot, err)
}

func finishCapture(tcp *net.TCPConn, snapshot Snapshot, captureErr error) (*Conn, error) {
	if captureErr != nil {
		return nil, errors.Join(fmt.Errorf("accepted witness capture: %w", captureErr), tcp.Close())
	}
	return &Conn{tcp: tcp, snapshot: snapshot}, nil
}

// Snapshot returns a value copy, including after Close. It never proves liveness.
func (c *Conn) Snapshot() (Snapshot, error) {
	if c == nil || c.tcp == nil || c.snapshot.Schema == "" {
		return Snapshot{}, net.ErrClosed
	}
	return c.snapshot, nil
}
func (c *Conn) Read(b []byte) (int, error) {
	if c == nil || c.tcp == nil {
		return 0, net.ErrClosed
	}
	return c.tcp.Read(b)
}
func (c *Conn) Write(b []byte) (int, error) {
	if c == nil || c.tcp == nil {
		return 0, net.ErrClosed
	}
	return c.tcp.Write(b)
}
func (c *Conn) Close() error {
	if c == nil || c.tcp == nil {
		return net.ErrClosed
	}
	return c.tcp.Close()
}
func (c *Conn) LocalAddr() net.Addr {
	if c == nil || c.tcp == nil {
		return nil
	}
	return c.tcp.LocalAddr()
}
func (c *Conn) RemoteAddr() net.Addr {
	if c == nil || c.tcp == nil {
		return nil
	}
	return c.tcp.RemoteAddr()
}
func (c *Conn) SetDeadline(t time.Time) error {
	if c == nil || c.tcp == nil {
		return net.ErrClosed
	}
	return c.tcp.SetDeadline(t)
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	if c == nil || c.tcp == nil {
		return net.ErrClosed
	}
	return c.tcp.SetReadDeadline(t)
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	if c == nil || c.tcp == nil {
		return net.ErrClosed
	}
	return c.tcp.SetWriteDeadline(t)
}
