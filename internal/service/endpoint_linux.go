//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
)

var ErrServiceUnavailable = errors.New("service unavailable")
var ErrDiscovery = errors.New("invalid service discovery")

const discoveryName = "service.json"
const endpointTimeout = 2 * time.Second

type endpointDescriptor struct {
	Version  int    `json:"version"`
	Instance string `json:"instance"`
	Endpoint string `json:"endpoint"`
	PID      int    `json:"pid"`
}
type discoverySnapshot struct {
	descriptor endpointDescriptor
	raw        []byte
	info       os.FileInfo
}

// Endpoint copies share admission, cancellation and cleanup ownership.
type Endpoint struct{ *endpointState }
type endpointState struct {
	root      *os.Root
	listener  *platform.PrivateUnixListener
	owned     discoverySnapshot
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	accepting sync.WaitGroup
	once      sync.Once
	closeErr  error
}

func validInstance(s string) bool {
	if len(s) != 48 {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && hex.EncodeToString(b) == s
}
func descriptorBytes(d endpointDescriptor) ([]byte, error) {
	if d.Version != 1 || !validInstance(d.Instance) || d.Endpoint != "svc-"+d.Instance+".sock" || d.PID <= 0 {
		return nil, ErrDiscovery
	}
	raw, e := json.Marshal(d)
	if e != nil {
		return nil, ErrDiscovery
	}
	return config.CanonicalJSON(raw)
}
func privateDescriptor(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0600 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1
}
func readDiscovery(root *os.Root) (s discoverySnapshot, err error) {
	info, e := root.Lstat(discoveryName)
	if e != nil {
		return s, e
	}
	if !privateDescriptor(info) {
		return s, ErrDiscovery
	}
	f, e := root.Open(discoveryName)
	if e != nil {
		return s, ErrDiscovery
	}
	defer func() {
		if e := f.Close(); e != nil {
			err = ErrDiscovery
		}
	}()
	opened, e := f.Stat()
	if e != nil || !privateDescriptor(opened) || !os.SameFile(info, opened) {
		return s, ErrDiscovery
	}
	raw, e := io.ReadAll(io.LimitReader(f, 1025))
	if e != nil || len(raw) > 1024 {
		return s, ErrDiscovery
	}
	var d endpointDescriptor
	if json.Unmarshal(raw, &d) != nil {
		return s, ErrDiscovery
	}
	canonical, e := descriptorBytes(d)
	if e != nil || !bytes.Equal(raw, canonical) {
		return s, ErrDiscovery
	}
	current, e := root.Lstat(discoveryName)
	if e != nil || !privateDescriptor(current) || !os.SameFile(info, current) {
		return s, ErrDiscovery
	}
	return discoverySnapshot{d, raw, info}, nil
}
func sameDiscovery(a, b discoverySnapshot) bool {
	return os.SameFile(a.info, b.info) && bytes.Equal(a.raw, b.raw)
}
func unchangedDiscovery(root *os.Root, old discoverySnapshot, absent bool) error {
	now, e := readDiscovery(root)
	if absent && errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || absent || !sameDiscovery(old, now) {
		return ErrDiscovery
	}
	return nil
}
func syncEndpointDirectory(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func endpointCapacity(root *os.Root) (result error) {
	f, e := root.Open(".")
	if e != nil {
		return ErrDiscovery
	}
	defer func() {
		if f.Close() != nil {
			result = ErrDiscovery
		}
	}()
	entries, e := f.ReadDir(4097)
	if e != nil && e != io.EOF {
		return ErrDiscovery
	}
	if len(entries) > 4096 {
		return ErrDiscovery
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "svc-") && strings.HasSuffix(entry.Name(), ".sock") {
			count++
		}
	}
	if count >= 128 {
		return ErrDiscovery
	}
	return nil
}

// OpenEndpoint requires the Coordinator's live exclusive Store writer lease.
// Failed publication retains uncertain descriptor/temp evidence. It never evicts
// old sockets or interprets discovery data as core ownership.
func OpenEndpoint(c *control.Coordinator) (_ *Endpoint, result error) {
	if c == nil {
		return nil, ErrDiscovery
	}
	instance, root, e := c.OpenServiceRuntime()
	if e != nil {
		return nil, errors.Join(ErrDiscovery, e)
	}
	var listener *platform.PrivateUnixListener
	success := false
	defer func() {
		if !success {
			if listener != nil {
				result = errors.Join(result, listener.Close())
			}
			result = errors.Join(result, root.Close())
		}
	}()
	if !validInstance(instance) {
		return nil, ErrDiscovery
	}
	old, e := readDiscovery(root)
	absent := errors.Is(e, os.ErrNotExist)
	if e != nil && !absent {
		return nil, ErrDiscovery
	}
	if e = endpointCapacity(root); e != nil {
		return nil, e
	}
	d := endpointDescriptor{1, instance, "svc-" + instance + ".sock", os.Getpid()}
	raw, e := descriptorBytes(d)
	if e != nil {
		return nil, e
	}
	listener, e = platform.ListenPrivateUnix(root, d.Endpoint)
	if e != nil {
		return nil, errors.Join(ErrDiscovery, e)
	}
	tmp := ".service-" + instance + ".tmp"
	f, e := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, ErrDiscovery
	}
	n, e := f.Write(raw)
	if e == nil && n != len(raw) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return nil, ErrDiscovery
	}
	if e = unchangedDiscovery(root, old, absent); e != nil {
		return nil, e
	}
	if e = root.Rename(tmp, discoveryName); e != nil {
		return nil, ErrDiscovery
	}
	if e = syncEndpointDirectory(root); e != nil {
		return nil, ErrDiscovery
	}
	owned, e := readDiscovery(root)
	if e != nil || !bytes.Equal(owned.raw, raw) {
		return nil, ErrDiscovery
	}
	ctx, cancel := context.WithCancel(context.Background())
	success = true
	return &Endpoint{&endpointState{root: root, listener: listener, owned: owned, ctx: ctx, cancel: cancel}}, nil
}
func (e *Endpoint) Instance() string {
	if e == nil || e.endpointState == nil {
		return ""
	}
	return e.owned.descriptor.Instance
}
func handshake(ctx context.Context, conn *net.UnixConn, instance string, server bool) error {
	var expected [32]byte
	copy(expected[:], "HPSVC001")
	decoded, err := hex.DecodeString(instance)
	if err != nil || len(decoded) != 24 {
		return ErrDiscovery
	}
	copy(expected[8:], decoded)
	err = wireIO(ctx, conn, func() error {
		var got [32]byte
		if !server {
			if _, e := io.Copy(conn, bytes.NewReader(expected[:])); e != nil {
				return e
			}
		}
		if _, e := io.ReadFull(conn, got[:]); e != nil {
			return e
		}
		if got != expected {
			return ErrDiscovery
		}
		if server {
			_, e := io.Copy(conn, bytes.NewReader(expected[:]))
			return e
		}
		return nil
	})
	if err != nil {
		return errors.Join(ErrDiscovery, err)
	}
	return nil
}

// Accept authenticates the actual peer, then binds this incarnation before
// returning an open connection ready for the wire protocol.
func (e *Endpoint) Accept(ctx context.Context) (*net.UnixConn, error) {
	if e == nil || e.endpointState == nil || ctx == nil {
		return nil, ErrDiscovery
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, net.ErrClosed
	}
	e.accepting.Add(1)
	e.mu.Unlock()
	defer e.accepting.Done()
	active, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	stop := context.AfterFunc(e.ctx, func() { cancel(); close(finished) })
	defer func() {
		if !stop() {
			<-finished
		}
		cancel()
	}()
	conn, _, err := e.listener.Accept(active)
	if err != nil {
		return nil, err
	}
	bounded, finish := context.WithTimeout(active, endpointTimeout)
	defer finish()
	if err = handshake(bounded, conn, e.Instance(), true); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return conn, nil
}
func (e *Endpoint) Close() error {
	if e == nil || e.endpointState == nil {
		return nil
	}
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.cancel()
		e.mu.Unlock()
		err := e.listener.Close()
		e.accepting.Wait()
		if check := unchangedDiscovery(e.root, e.owned, false); check != nil {
			err = errors.Join(err, check)
		} else {
			err = errors.Join(err, e.root.Remove(discoveryName))
			err = errors.Join(err, syncEndpointDirectory(e.root))
		}
		e.closeErr = errors.Join(err, e.root.Close())
	})
	return e.closeErr
}
func discoveryError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrDiscovery, err)
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return ErrServiceUnavailable
	}
	return ErrDiscovery
}

// DialEndpoint performs read-only discovery and peer/incarnation authentication.
func DialEndpoint(ctx context.Context, paths config.Paths) (connection *net.UnixConn, result error) {
	if ctx == nil {
		return nil, ErrDiscovery
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrDiscovery, err)
	}
	bounded, cancel := context.WithTimeout(ctx, endpointTimeout)
	defer cancel()
	root, err := config.OpenRuntimeDirectory(paths)
	if err != nil {
		return nil, discoveryError(err)
	}
	var conn *net.UnixConn
	defer func() {
		if err := root.Close(); err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			connection = nil
			result = errors.Join(result, ErrDiscovery, err)
		}
	}()
	snapshot, err := readDiscovery(root)
	if err != nil {
		return nil, discoveryError(err)
	}
	conn, peer, err := platform.DialPrivateUnix(bounded, root, snapshot.descriptor.Endpoint)
	if err != nil {
		return nil, discoveryError(err)
	}
	if peer.PID != snapshot.descriptor.PID {
		return nil, errors.Join(ErrDiscovery, conn.Close())
	}
	if err = handshake(bounded, conn, snapshot.descriptor.Instance, false); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	if err = unchangedDiscovery(root, snapshot, false); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return conn, nil
}
