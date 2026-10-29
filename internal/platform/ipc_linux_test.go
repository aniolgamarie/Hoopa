//go:build linux

package platform

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func ipcTestRoot(t *testing.T) (string, *os.Root) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return path, root
}
func ipcTestListener(t *testing.T, root *os.Root, name string) *PrivateUnixListener {
	t.Helper()
	listener, err := ListenPrivateUnix(root, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
func ipcTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func ipcTestPeer(t *testing.T, peer PeerIdentity) {
	t.Helper()
	want := PeerIdentity{PID: os.Getpid(), UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}
	if peer != want {
		t.Fatalf("actual SO_PEERCRED mismatch: got %+v want %+v", peer, want)
	}
}
func ipcTestExchange(t *testing.T, listener *PrivateUnixListener, root *os.Root, name string) {
	t.Helper()
	ctx := ipcTestContext(t)
	client, serverPeer, err := DialPrivateUnix(ctx, root, name)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ipcTestPeer(t, serverPeer)
	server, clientPeer, err := listener.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ipcTestPeer(t, clientPeer)
	for _, conn := range []*net.UnixConn{client, server} {
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 7)
	if _, err = io.ReadFull(server, raw); err != nil || string(raw) != "request" {
		t.Fatalf("request bytes: %q %v", raw, err)
	}
	if _, err = server.Write([]byte("response")); err != nil {
		t.Fatal(err)
	}
	raw = make([]byte, 8)
	if _, err = io.ReadFull(client, raw); err != nil || string(raw) != "response" {
		t.Fatalf("response bytes: %q %v", raw, err)
	}
}

func TestPrivateUnixRoundTripAndPeerIdentity(t *testing.T) {
	path, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	info, err := os.Lstat(filepath.Join(path, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = privateUnixSocket(info); err != nil {
		t.Fatal("new socket not 0600/owned:", err)
	}
	ipcTestExchange(t, listener, root, "control.sock")
	ownDir := listener.directory
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(path, "control.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned socket was not removed:", err)
	}
	if _, err = ownDir.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("owned directory FD not closed:", err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal("idempotent close:", err)
	}
}

func TestPrivateUnixCancellation(t *testing.T) {
	_, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	t.Run("accept-before-call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		conn, _, err := listener.Accept(ctx)
		if conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled accept: conn=%v err=%v", conn, err)
		}
	})
	t.Run("accept-wait-deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		began := time.Now()
		conn, _, err := listener.Accept(ctx)
		if conn != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > time.Second {
			t.Fatalf("bounded accept: conn=%v err=%v elapsed=%v", conn, err, time.Since(began))
		}
	})
	t.Run("dial-before-call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		conn, _, err := DialPrivateUnix(ctx, root, "control.sock")
		if conn != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled dial: conn=%v err=%v", conn, err)
		}
	})
	t.Run("dial-expired-deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		conn, _, err := DialPrivateUnix(ctx, root, "control.sock")
		if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired dial: conn=%v err=%v", conn, err)
		}
	})
}

func TestPrivateUnixCloseInterruptsAcceptAndSynchronizes(t *testing.T) {
	path, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	began := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(began)
		conn, _, err := listener.Accept(ctx)
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	<-began
	ipcTestWaitAcceptTokenHeld(t, listener)
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := listener.Close(); err != nil {
				failures <- err
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal("accept not interrupted by close:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accept goroutine not collected")
	}
	if _, err := os.Lstat(filepath.Join(path, "control.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("concurrent close left endpoint:", err)
	}
}

func TestPrivateUnixExistingEndpointsNeverRemoved(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "socket"} {
		t.Run(kind, func(t *testing.T) {
			path, root := ipcTestRoot(t)
			endpoint := filepath.Join(path, "control.sock")
			switch kind {
			case "file":
				if err := os.WriteFile(endpoint, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(endpoint, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("untouched-target", endpoint); err != nil {
					t.Fatal(err)
				}
			case "socket":
				ipcTestListener(t, root, "control.sock")
			}
			before, err := os.Lstat(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if unexpected, err := ListenPrivateUnix(root, "control.sock"); err == nil {
				unexpected.Close()
				t.Fatal("existing endpoint accepted")
			}
			after, err := os.Lstat(endpoint)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("existing endpoint changed:", err)
			}
			if kind == "file" {
				raw, err := os.ReadFile(endpoint)
				if err != nil || string(raw) != "keep" {
					t.Fatal("existing file changed")
				}
			}
		})
	}
}

func TestPrivateUnixSameEndpointConflictLeavesLiveListener(t *testing.T) {
	_, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	if unexpected, err := ListenPrivateUnix(root, "control.sock"); err == nil {
		unexpected.Close()
		t.Fatal("second listener accepted")
	}
	ipcTestExchange(t, listener, root, "control.sock")
}

func TestPrivateUnixCloseRejectsReplacedEndpoint(t *testing.T) {
	for _, kind := range []string{"file", "socket", "missing"} {
		t.Run(kind, func(t *testing.T) {
			path, root := ipcTestRoot(t)
			listener := ipcTestListener(t, root, "control.sock")
			endpoint := filepath.Join(path, "control.sock")
			if err := os.Rename(endpoint, filepath.Join(path, "original.sock")); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				if err := os.WriteFile(endpoint, []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
			case "socket":
				replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: privateUnixEndpoint(listener.directory, "control.sock"), Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				replacement.SetUnlinkOnClose(false)
				defer replacement.Close()
			}
			var before os.FileInfo
			var err error
			if kind != "missing" {
				before, err = os.Lstat(endpoint)
				if err != nil {
					t.Fatal(err)
				}
			}
			directory := listener.directory
			if err = listener.Close(); err == nil {
				t.Fatal("changed endpoint close was not reported")
			}
			if _, err = directory.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatal("conflict close leaked directory FD:", err)
			}
			if kind != "missing" {
				after, err := os.Lstat(endpoint)
				if err != nil || !os.SameFile(before, after) {
					t.Fatal("replacement removed/changed:", err)
				}
			}
			if kind == "file" {
				raw, err := os.ReadFile(endpoint)
				if err != nil || string(raw) != "replacement" {
					t.Fatal("replacement file changed")
				}
			}
		})
	}
}

func TestPrivateUnixRejectsInvalidNamesAndDirectory(t *testing.T) {
	for _, name := range []string{"", ".hidden", "..", "a/b", "a\\b", "a\x00b", "é", strings.Repeat("a", 61)} {
		t.Run("name-"+fmtName(name), func(t *testing.T) {
			path, root := ipcTestRoot(t)
			before, err := os.ReadDir(path)
			if err != nil {
				t.Fatal(err)
			}
			if listener, err := ListenPrivateUnix(root, name); err == nil {
				listener.Close()
				t.Fatal("invalid name accepted")
			}
			if conn, _, err := DialPrivateUnix(ipcTestContext(t), root, name); err == nil {
				conn.Close()
				t.Fatal("invalid dial name accepted")
			}
			after, err := os.ReadDir(path)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid name created/changed files")
			}
		})
	}
	for _, test := range []struct {
		name string
		mode os.FileMode
	}{{"world-readable", 0755}, {"searchable", 0711}, {"group-writable", 0770}, {"sticky", 0700 | os.ModeSticky}} {
		t.Run(test.name, func(t *testing.T) {
			path, root := ipcTestRoot(t)
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(path, 0700)
			if listener, err := ListenPrivateUnix(root, "control.sock"); err == nil {
				listener.Close()
				t.Fatal("unsafe directory accepted")
			}
			if conn, _, err := DialPrivateUnix(ipcTestContext(t), root, "control.sock"); err == nil {
				conn.Close()
				t.Fatal("unsafe directory dial accepted")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != test.mode.Perm() || info.Mode()&os.ModeSticky != test.mode&os.ModeSticky {
				t.Fatal("unsafe directory silently repaired")
			}
		})
	}
	t.Run("nil-root", func(t *testing.T) {
		if _, err := ListenPrivateUnix(nil, "control.sock"); err == nil {
			t.Fatal("nil root accepted")
		}
	})
}
func fmtName(name string) string {
	if name == "" {
		return "empty"
	}
	return strings.ReplaceAll(strings.ReplaceAll(name, "/", "slash"), "\x00", "NUL")
}

func TestPrivateUnixBorrowedRootCloseAndDirectoryRename(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "runtime")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	listener := ipcTestListener(t, root, "control.sock")
	moved := filepath.Join(parent, "moved")
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(path, "control.sock")
	if err = os.WriteFile(replacement, []byte("different-directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	dialRoot, err := os.OpenRoot(moved)
	if err != nil {
		t.Fatal(err)
	}
	defer dialRoot.Close()
	ipcTestExchange(t, listener, dialRoot, "control.sock")
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(moved, "control.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("original directory socket not removed:", err)
	}
	raw, err := os.ReadFile(replacement)
	if err != nil || string(raw) != "different-directory" {
		t.Fatal("replacement directory touched")
	}
}

func TestPrivateUnixPeerPolicySynthetic(t *testing.T) {
	// These are synthetic policy inputs, not claims of actual other-UID processes.
	for _, test := range []struct {
		name   string
		peer   PeerIdentity
		uid    uint32
		accept bool
	}{{"same-account", PeerIdentity{PID: 100, UID: 42, GID: 99}, 42, true}, {"zero-pid", PeerIdentity{PID: 0, UID: 42}, 42, false}, {"negative-pid", PeerIdentity{PID: -1, UID: 42}, 42, false}, {"other-uid", PeerIdentity{PID: 100, UID: 43}, 42, false}} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePrivateUnixPeer(test.peer, test.uid)
			if (err == nil) != test.accept {
				t.Fatalf("synthetic peer policy: %+v, %v", test, err)
			}
		})
	}
}

// With no client and no other Accept caller, an empty token channel proves the
// first Accept has acquired ownership. Polling observes synchronization state,
// rather than assuming a goroutine ran after an arbitrary sleep.
func ipcTestWaitAcceptTokenHeld(t *testing.T, listener *PrivateUnixListener) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if len(listener.acceptToken) == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("first Accept did not acquire token")
		case <-tick.C:
		}
	}
}

func TestPrivateUnixConcurrentAcceptQueueCancellation(t *testing.T) {
	_, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	first := make(chan error, 1)
	go func() {
		conn, _, err := listener.Accept(context.Background())
		if conn != nil {
			_ = conn.Close()
		}
		first <- err
	}()
	// Always close and collect the first goroutine, including on assertion failure.
	collected := false
	defer func() {
		_ = listener.Close()
		if !collected {
			select {
			case <-first:
			case <-time.After(time.Second):
				t.Error("first Accept leaked")
			}
		}
	}()
	ipcTestWaitAcceptTokenHeld(t, listener)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	began := time.Now()
	conn, _, err := listener.Accept(ctx)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("queued Accept returned connection")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued Accept ignored deadline:", err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("queued cancellation blocked on active Accept")
	}
	select {
	case err := <-first:
		collected = true
		t.Fatal("first Accept unexpectedly returned:", err)
	default:
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		collected = true
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal("Close did not interrupt first Accept:", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Accept not interrupted")
	}
}

func TestPrivateUnixClosePreservesReturnedConnection(t *testing.T) {
	_, root := ipcTestRoot(t)
	listener := ipcTestListener(t, root, "control.sock")
	ctx := ipcTestContext(t)
	client, peer, err := DialPrivateUnix(ctx, root, "control.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ipcTestPeer(t, peer)
	server, peer, err := listener.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ipcTestPeer(t, peer)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*net.UnixConn{client, server} {
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]*net.UnixConn{{client, server}, {server, client}} {
		if _, err := pair[0].Write([]byte("live")); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 4)
		if _, err := io.ReadFull(pair[1], data); err != nil {
			t.Fatal(err)
		}
		if string(data) != "live" {
			t.Fatal("connection data changed:", string(data))
		}
	}
}

func TestPrivateUnixZeroValueFailsClosed(t *testing.T) {
	var listener PrivateUnixListener
	conn, _, err := listener.Accept(context.Background())
	if conn != nil || err == nil {
		t.Fatal("zero listener accepted")
	}
	if err := listener.Close(); err == nil {
		t.Fatal("zero listener Close succeeded")
	}
}
