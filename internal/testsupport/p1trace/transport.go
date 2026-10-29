//go:build linux && amd64 && p1integration

// Package p1trace is a test-only synchronous supervisor transport. It is not a
// production hook or a security boundary against hostile same-UID processes.
package p1trace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"hoopa/internal/config"
)

const maxPacket = 65536
const maxSequence = uint64(9007199254740991)

var ErrTransport = errors.New("P1_TRACE_TRANSPORT_FAILED")

type Decision string

const (
	Continue    Decision = "continue"
	InjectError Decision = "inject-error"
)

type Event struct {
	Component   string          `json:"component"`
	Point       string          `json:"point"`
	OperationID string          `json:"operation_id"`
	RunID       string          `json:"run_id"`
	RequestID   string          `json:"request_id"`
	Data        json.RawMessage `json:"data"`
}

func (Event) String() string   { return "p1trace.Event{private}" }
func (Event) GoString() string { return "p1trace.Event{private}" }

type wire struct {
	Version  int    `json:"version"`
	StreamID string `json:"stream_id"`
	Sequence uint64 `json:"sequence"`
	Event    Event  `json:"event"`
}
type ack struct {
	Version    int      `json:"version"`
	StreamID   string   `json:"stream_id"`
	Sequence   uint64   `json:"sequence"`
	WireSHA256 string   `json:"wire_sha256"`
	Decision   Decision `json:"decision"`
}
type endpoint struct {
	conn     *net.UnixConn
	pid, uid int
	stream   string
}
type Producer struct {
	ep     endpoint
	gate   chan struct{}
	seq    uint64
	failed bool
}
type Collector struct {
	ep      endpoint
	gate    chan struct{}
	seq     uint64
	pending *Pending
	failed  bool
	dir     *os.File
	path    string
}
type Pending struct {
	collector        *Collector
	event            Event
	sequence         uint64
	hash             string
	decided          bool
	peerPID, peerUID int
	refsMu           sync.RWMutex
	refs             []OriginalRef
}

type OriginalRef struct {
	Path   string
	Bytes  int64
	SHA256 string
}

func (p *Pending) Originals() []OriginalRef {
	p.refsMu.RLock()
	defer p.refsMu.RUnlock()
	return append([]OriginalRef(nil), p.refs...)
}
func (p *Pending) PeerPID() int { return p.peerPID }
func (p *Pending) PeerUID() int { return p.peerUID }
func original(path string, b []byte) OriginalRef {
	h := sha256.Sum256(b)
	return OriginalRef{path, int64(len(b)), hex.EncodeToString(h[:])}
}
func acquire(ctx context.Context, gate chan struct{}) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrTransport
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	select {
	case gate <- struct{}{}:
		if ctx.Err() != nil {
			<-gate
			cancel()
			return nil, nil, ctx.Err()
		}
		return ctx, func() { <-gate; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, ctx.Err()
	}
}
func (*Pending) String() string   { return "p1trace.Pending{private}" }
func (*Pending) GoString() string { return "p1trace.Pending{private}" }
func (p *Pending) Event() Event {
	e := p.event
	e.Data = append(json.RawMessage(nil), e.Data...)
	return e
}
func (p *Pending) Sequence() uint64 { return p.sequence }

func SocketPair() (*os.File, *os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, ErrTransport
	}
	for _, fd := range fds {
		if syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1) != nil {
			syscall.Close(fds[0])
			syscall.Close(fds[1])
			return nil, nil, ErrTransport
		}
	}
	return os.NewFile(uintptr(fds[0]), "p1trace"), os.NewFile(uintptr(fds[1]), "p1trace"), nil
}
func newEndpoint(f *os.File, pid, uid int, stream string) (endpoint, error) {
	if f == nil || pid <= 0 || uid < 0 || len(stream) != 32 {
		return endpoint{}, ErrTransport
	}
	b, e := hex.DecodeString(stream)
	if e != nil || len(b) != 16 || strings.ToLower(stream) != stream {
		return endpoint{}, ErrTransport
	}
	raw, e := f.SyscallConn()
	if e != nil {
		return endpoint{}, ErrTransport
	}
	valid := false
	if raw.Control(func(fd uintptr) {
		typ, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_TYPE)
		if err == nil && typ == syscall.SOCK_SEQPACKET {
			valid = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1) == nil
		}
	}) != nil || !valid {
		return endpoint{}, ErrTransport
	}
	c, e := net.FileConn(f)
	if e != nil {
		return endpoint{}, ErrTransport
	}
	u, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return endpoint{}, ErrTransport
	}
	return endpoint{u, pid, uid, stream}, nil
}

// Constructors duplicate file; callers retain ownership of the supplied file.
func NewProducer(f *os.File, parentPID, uid int, stream string) (*Producer, error) {
	ep, e := newEndpoint(f, parentPID, uid, stream)
	if e != nil {
		return nil, e
	}
	return &Producer{ep: ep, gate: make(chan struct{}, 1)}, nil
}
func NewCollector(f *os.File, childPID, uid int, stream, dir string) (*Collector, error) {
	d, e := openCapture(dir)
	if e != nil {
		return nil, e
	}
	ep, e := newEndpoint(f, childPID, uid, stream)
	if e != nil {
		d.Close()
		return nil, e
	}
	return &Collector{ep: ep, dir: d, path: dir, gate: make(chan struct{}, 1)}, nil
}
func openCapture(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrTransport
	}
	// Walk every component without following symlinks, then retain the directory FD.
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, ErrTransport
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if err != nil {
			return nil, ErrTransport
		}
		fd = next
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Uid != uint32(os.Getuid()) || st.Mode&07777 != 0700 {
		syscall.Close(fd)
		return nil, ErrTransport
	}
	return os.NewFile(uintptr(fd), "p1trace-capture"), nil
}
func (p *Producer) Close() error {
	if p == nil {
		return ErrTransport
	}
	if p.ep.conn.Close() != nil {
		return ErrTransport
	}
	return nil
}

// Close closes the socket, then waits for any in-flight persistence before
// closing the capture directory. It does not interrupt filesystem sync and has
// no total duration bound. The three-second limits on Emit, Next and Decide
// cover their context-aware gate acquisition and socket operations only.
func (c *Collector) Close() error {
	if c == nil {
		return ErrTransport
	}
	e := c.ep.conn.Close()
	c.gate <- struct{}{}
	de := c.dir.Close()
	<-c.gate
	if e != nil || de != nil {
		return ErrTransport
	}
	return nil
}
func operation(ctx context.Context, conn *net.UnixConn) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrTransport
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if e := ctx.Err(); e != nil {
		cancel()
		return nil, nil, e
	}
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		cancel()
		return nil, nil, ErrTransport
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()); close(done) })
	return ctx, func() {
		if !stop() {
			<-done
		}
		cancel()
	}, nil
}
func transportError(ctx context.Context) error {
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrTransport
}
func (ep endpoint) receive(ctx context.Context) ([]byte, *syscall.Ucred, error) {
	b := make([]byte, maxPacket)
	oob := make([]byte, syscall.CmsgSpace(12)+syscall.CmsgSpace(256))
	n, on, flags, _, e := ep.conn.ReadMsgUnix(b, oob)
	if e != nil {
		return nil, nil, transportError(ctx)
	}
	cms, e := syscall.ParseSocketControlMessage(oob[:on])
	var cred *syscall.Ucred
	bad := false
	for _, m := range cms {
		if m.Header.Level == syscall.SOL_SOCKET && m.Header.Type == syscall.SCM_RIGHTS {
			fds, err := syscall.ParseUnixRights(&m)
			if err == nil {
				for _, fd := range fds {
					syscall.Close(fd)
				}
			}
			bad = true
			continue
		}
		if m.Header.Level != syscall.SOL_SOCKET || m.Header.Type != syscall.SCM_CREDENTIALS || cred != nil {
			bad = true
			continue
		}
		cred, e = syscall.ParseUnixCredentials(&m)
		if e != nil {
			bad = true
		}
	}
	if e != nil || bad || n == 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 || cred == nil || int(cred.Pid) != ep.pid || int(cred.Uid) != ep.uid {
		return nil, nil, ErrTransport
	}
	return b[:n], cred, nil
}
func (ep endpoint) send(ctx context.Context, b []byte) error {
	if len(b) > maxPacket {
		return ErrTransport
	}
	n, _, e := ep.conn.WriteMsgUnix(b, nil, nil)
	if e != nil {
		return transportError(ctx)
	}
	if n != len(b) {
		return ErrTransport
	}
	return nil
}
func strict(raw []byte, out any, keys ...string) error {
	if _, e := config.CanonicalJSON(raw); e != nil {
		return ErrTransport
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != len(keys) {
		return ErrTransport
	}
	for _, k := range keys {
		v, ok := m[k]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return ErrTransport
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrTransport
	}
	return nil
}
func validEvent(e Event) bool {
	for _, s := range []string{e.Component, e.Point, e.OperationID, e.RunID, e.RequestID} {
		if len(s) > 256 || !utf8.ValidString(s) {
			return false
		}
	}
	if e.Component == "" || e.Point == "" || len(e.Data) > 48<<10 {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(e.Data, &m) != nil || m == nil {
		return false
	}
	_, err := config.CanonicalJSON(e.Data)
	return err == nil
}
func decodeWire(raw []byte) (wire, error) {
	var w wire
	if strict(raw, &w, "version", "stream_id", "sequence", "event") != nil {
		return w, ErrTransport
	}
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	if strict(m["event"], &w.Event, "component", "point", "operation_id", "run_id", "request_id", "data") != nil || !validEvent(w.Event) {
		return w, ErrTransport
	}
	return w, nil
}
func (p *Producer) Emit(ctx context.Context, event Event) (Decision, error) {
	if p == nil || ctx == nil {
		return "", ErrTransport
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if p.failed || !validEvent(event) || p.seq >= maxSequence {
		p.failed = true
		return "", ErrTransport
	}
	ctx, done, e := operation(ctx, p.ep.conn)
	if e != nil {
		return "", e
	}
	defer done()
	p.seq++
	p.failed = true
	raw, e := json.Marshal(wire{1, p.ep.stream, p.seq, event})
	if e != nil || len(raw) > maxPacket {
		return "", ErrTransport
	}
	p.failed = true
	if e = p.ep.send(ctx, raw); e != nil {
		p.ep.conn.Close()
		return "", e
	}
	reply, _, e := p.ep.receive(ctx)
	if e != nil {
		p.ep.conn.Close()
		return "", e
	}
	var a ack
	sum := sha256.Sum256(raw)
	if strict(reply, &a, "version", "stream_id", "sequence", "wire_sha256", "decision") != nil || a.Version != 1 || a.StreamID != p.ep.stream || a.Sequence != p.seq || a.WireSHA256 != hex.EncodeToString(sum[:]) || (a.Decision != Continue && a.Decision != InjectError) {
		p.ep.conn.Close()
		return "", ErrTransport
	}
	p.failed = false
	return a.Decision, nil
}
func (c *Collector) persist(name string, b []byte) error {
	fd, e := syscall.Openat(int(c.dir.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return ErrTransport
	}
	f := os.NewFile(uintptr(fd), "p1trace-private")
	n, e := f.Write(b)
	if e == nil && n == len(b) {
		e = f.Sync()
	} else {
		e = ErrTransport
	}
	ce := f.Close()
	if e != nil || ce != nil || c.dir.Sync() != nil {
		return ErrTransport
	}
	return nil
}
func (c *Collector) Next(ctx context.Context) (*Pending, error) {
	if c == nil || ctx == nil {
		return nil, ErrTransport
	}
	ctx, unlock, lockErr := acquire(ctx, c.gate)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	if c.failed || c.pending != nil || c.seq >= maxSequence {
		return nil, ErrTransport
	}
	ctx, done, e := operation(ctx, c.ep.conn)
	if e != nil {
		return nil, e
	}
	defer done()
	c.failed = true
	raw, cred, e := c.ep.receive(ctx)
	if e != nil {
		return nil, e
	}
	w, e := decodeWire(raw)
	if e != nil || w.Version != 1 || w.StreamID != c.ep.stream || w.Sequence != c.seq+1 {
		return nil, ErrTransport
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	prefix := fmt.Sprintf("%016d", w.Sequence)
	receipt, _ := json.Marshal(struct {
		PID        int32  `json:"pid"`
		UID        uint32 `json:"uid"`
		GID        uint32 `json:"gid"`
		WireSHA256 string `json:"wire_sha256"`
	}{cred.Pid, cred.Uid, cred.Gid, hash})
	if c.persist(prefix+".wire.json", raw) != nil || c.persist(prefix+".receipt.json", receipt) != nil {
		return nil, ErrTransport
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	p := &Pending{collector: c, event: w.Event, sequence: w.Sequence, hash: hash, peerPID: int(cred.Pid), peerUID: int(cred.Uid), refs: []OriginalRef{original(filepath.Join(c.path, prefix+".wire.json"), raw), original(filepath.Join(c.path, prefix+".receipt.json"), receipt)}}
	c.seq = w.Sequence
	c.pending = p
	c.failed = false
	return p, nil
}
func (p *Pending) Decide(ctx context.Context, d Decision) error {
	if d != Continue && d != InjectError {
		return ErrTransport
	}
	return p.decide(ctx, string(d), true)
}
func (p *Pending) RecordCrashCut() error { return p.decide(context.Background(), "crash-cut", false) }
func (p *Pending) decide(ctx context.Context, d string, send bool) error {
	if p == nil || ctx == nil {
		return ErrTransport
	}
	c := p.collector
	ctx, unlock, lockErr := acquire(ctx, c.gate)
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	if p.decided || c.failed || c.pending != p {
		return ErrTransport
	}
	ctx, done, e := operation(ctx, c.ep.conn)
	if e != nil {
		return e
	}
	defer done()
	p.decided = true
	c.failed = true
	raw, _ := json.Marshal(ack{1, c.ep.stream, p.sequence, p.hash, Decision(d)})
	if c.persist(fmt.Sprintf("%016d.decision.json", p.sequence), raw) != nil {
		return ErrTransport
	}
	p.refsMu.Lock()
	p.refs = append(p.refs, original(filepath.Join(c.path, fmt.Sprintf("%016d.decision.json", p.sequence)), raw))
	p.refsMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if send {
		if e = c.ep.send(ctx, raw); e != nil {
			return e
		}
		c.pending = nil
		c.failed = false
	}
	return nil
}
