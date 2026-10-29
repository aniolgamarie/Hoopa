//go:build linux

package platform

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// PeerIdentity is the actual SO_PEERCRED identity of this Unix connection.
// Same-account authorization does not establish ownership of a managed core.
type PeerIdentity struct {
	PID      int
	UID, GID uint32
}

// PrivateUnixListener owns its directory FD and listener, not the borrowed Root.
// The caller must hold the instance Store writer lock before Listen and exclude
// other controlled writers from changing this runtime endpoint. As in ADR012,
// hostile same-UID rename/mount races are outside this boundary. In particular,
// Lstat/SameFile/Remove is not a kernel conditional-unlink transaction.
// Returned connections belong to callers; Close does not close those connections.
type PrivateUnixListener struct {
	mu          sync.Mutex
	listener    *net.UnixListener
	directory   *os.File
	endpoint    string
	owned       os.FileInfo
	closed      bool
	closeErr    error
	acceptToken chan struct{} // one holder owns listener deadlines and AcceptUnix
	done        chan struct{} // closed by Close; never close acceptToken
}

var errPrivateUnix = errors.New("invalid or unavailable private Unix IPC")

func privateUnixName(name string) error {
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }
	if len(name) == 0 || len(name) > 60 || !alnum(name[0]) {
		return errPrivateUnix
	}
	for i := 1; i < len(name); i++ {
		if !alnum(name[i]) && name[i] != '.' && name[i] != '_' && name[i] != '-' {
			return errPrivateUnix
		}
	}
	return nil
}
func privateUnixDirectory(file *os.File) error {
	if file == nil {
		return errPrivateUnix
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errPrivateUnix
	}
	return nil
}
func borrowPrivateUnixDirectory(directory *os.Root) (*os.File, error) {
	if directory == nil {
		return nil, errPrivateUnix
	}
	file, err := directory.Open(".")
	if err != nil {
		return nil, err
	}
	if err = privateUnixDirectory(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
func privateUnixEndpoint(directory *os.File, name string) string {
	return filepath.Join("/proc/self/fd", fmt.Sprint(directory.Fd()), name)
}
func privateUnixSocket(info os.FileInfo) error {
	if info == nil {
		return errPrivateUnix
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeType != os.ModeSocket || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errPrivateUnix
	}
	return nil
}

// ListenPrivateUnix exclusively creates name in an already private runtime root.
// It neither creates the directory nor repairs permissions/removes old endpoints.
func ListenPrivateUnix(directory *os.Root, name string) (_ *PrivateUnixListener, result error) {
	if err := privateUnixName(name); err != nil {
		return nil, err
	}
	file, err := borrowPrivateUnixDirectory(directory)
	if err != nil {
		return nil, err
	}
	ownedFile := false
	defer func() {
		if !ownedFile {
			result = errors.Join(result, file.Close())
		}
	}()
	endpoint := privateUnixEndpoint(file, name)
	if _, err = os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = fmt.Errorf("endpoint already exists: %w", os.ErrExist)
		}
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	owned, err := os.Lstat(endpoint)
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	} // no proven pathname ownership: never unlink
	socket := &PrivateUnixListener{listener: listener, directory: file, endpoint: endpoint, owned: owned, acceptToken: make(chan struct{}, 1), done: make(chan struct{})}
	socket.acceptToken <- struct{}{}
	ownedFile = true
	if owned.Mode()&os.ModeType != os.ModeSocket {
		return nil, errors.Join(errPrivateUnix, socket.Close())
	}
	if err = os.Chmod(endpoint, 0600); err != nil {
		return nil, errors.Join(err, socket.Close())
	}
	current, err := os.Lstat(endpoint)
	if err != nil || !os.SameFile(owned, current) {
		return nil, errors.Join(errPrivateUnix, err, socket.Close())
	}
	if err = privateUnixSocket(current); err != nil {
		return nil, errors.Join(err, socket.Close())
	}
	return socket, nil
}

func validatePrivateUnixPeer(peer PeerIdentity, expectedUID uint32) error {
	if peer.PID <= 0 || peer.UID != expectedUID {
		return errPrivateUnix
	}
	return nil
}
func privateUnixPeer(conn *net.UnixConn) (PeerIdentity, error) {
	var peer PeerIdentity
	raw, err := conn.SyscallConn()
	if err != nil {
		return peer, err
	}
	var credential *syscall.Ucred
	var inner error
	if err = raw.Control(func(fd uintptr) {
		credential, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return peer, err
	}
	if inner != nil {
		return peer, inner
	}
	if credential == nil {
		return peer, errPrivateUnix
	}
	peer = PeerIdentity{PID: int(credential.Pid), UID: credential.Uid, GID: credential.Gid}
	if err = validatePrivateUnixPeer(peer, uint32(os.Geteuid())); err != nil {
		return PeerIdentity{}, err
	}
	return peer, nil
}

// Accept polls cancellation with at most a 250ms listener deadline per wait.
// Concurrent callers acquire a cancellable token before changing shared deadlines.
// It starts no helper goroutine. Close interrupts active and queued waits.
func (l *PrivateUnixListener) Accept(ctx context.Context) (*net.UnixConn, PeerIdentity, error) {
	if l == nil || ctx == nil {
		return nil, PeerIdentity{}, errPrivateUnix
	}
	l.mu.Lock()
	if l.closed || l.listener == nil {
		l.mu.Unlock()
		return nil, PeerIdentity{}, net.ErrClosed
	}
	token, done := l.acceptToken, l.done
	l.mu.Unlock()
	if token == nil || done == nil {
		return nil, PeerIdentity{}, errPrivateUnix
	}
	select {
	case <-ctx.Done():
		return nil, PeerIdentity{}, ctx.Err()
	case <-done:
		return nil, PeerIdentity{}, net.ErrClosed
	case <-token:
	}
	// Only the token holder sends it back; Close never waits for or closes it.
	defer func() { token <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return nil, PeerIdentity{}, err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, PeerIdentity{}, net.ErrClosed
	}
	err := privateUnixDirectory(l.directory)
	listener := l.listener
	l.mu.Unlock()
	if err != nil {
		return nil, PeerIdentity{}, err
	}
	for {
		if err = ctx.Err(); err != nil {
			return nil, PeerIdentity{}, err
		}
		deadline := time.Now().Add(250 * time.Millisecond)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err = listener.SetDeadline(deadline); err != nil {
			return nil, PeerIdentity{}, err
		}
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil, PeerIdentity{}, ctx.Err()
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return nil, PeerIdentity{}, err
		}
		if err = ctx.Err(); err != nil {
			return nil, PeerIdentity{}, errors.Join(err, conn.Close())
		}
		peer, err := privateUnixPeer(conn)
		if err != nil {
			return nil, PeerIdentity{}, errors.Join(err, conn.Close())
		}
		return conn, peer, nil
	}
}

// Close synchronizes cleanup and removes only the still-identical created socket.
// A missing/replaced endpoint is reported, never repaired or removed as stale.
func (l *PrivateUnixListener) Close() error {
	if l == nil {
		return errPrivateUnix
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	l.closed = true
	if l.done != nil {
		close(l.done)
	}
	if l.listener == nil || l.directory == nil {
		l.closeErr = errPrivateUnix
		return l.closeErr
	}
	err := l.listener.Close()
	if dirErr := privateUnixDirectory(l.directory); dirErr != nil {
		err = errors.Join(err, dirErr)
	} else {
		current, statErr := os.Lstat(l.endpoint)
		if statErr != nil {
			err = errors.Join(err, statErr)
		} else if l.owned == nil || !os.SameFile(l.owned, current) || current.Mode()&os.ModeType != os.ModeSocket {
			err = errors.Join(err, fmt.Errorf("endpoint changed: %w", errPrivateUnix))
		} else {
			err = errors.Join(err, os.Remove(l.endpoint))
		}
	}
	l.closeErr = errors.Join(err, l.directory.Close())
	return l.closeErr
}

// DialPrivateUnix authenticates the actual server peer and bounds the dial to 2s.
func DialPrivateUnix(ctx context.Context, directory *os.Root, name string) (connection *net.UnixConn, peer PeerIdentity, result error) {
	if ctx == nil {
		return nil, peer, errPrivateUnix
	}
	if err := ctx.Err(); err != nil {
		return nil, peer, err
	}
	if err := privateUnixName(name); err != nil {
		return nil, peer, err
	}
	file, err := borrowPrivateUnixDirectory(directory)
	if err != nil {
		return nil, peer, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			if connection != nil {
				err = errors.Join(err, connection.Close())
				connection = nil
				peer = PeerIdentity{}
			}
			result = errors.Join(result, err)
		}
	}()
	endpoint := privateUnixEndpoint(file, name)
	info, err := os.Lstat(endpoint)
	if err != nil {
		return nil, peer, err
	}
	if err = privateUnixSocket(info); err != nil {
		return nil, peer, err
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(bounded, "unix", endpoint)
	if err != nil {
		return nil, peer, err
	}
	conn, ok := raw.(*net.UnixConn)
	if !ok {
		return nil, peer, errors.Join(errPrivateUnix, raw.Close())
	}
	if err = bounded.Err(); err != nil {
		return nil, peer, errors.Join(err, conn.Close())
	}
	peer, err = privateUnixPeer(conn)
	if err != nil {
		return nil, PeerIdentity{}, errors.Join(err, conn.Close())
	}
	if err = bounded.Err(); err != nil {
		return nil, PeerIdentity{}, errors.Join(err, conn.Close())
	}
	return conn, peer, nil
}
