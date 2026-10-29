//go:build linux && amd64 && p1integration

package p1session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/testsupport/p1trace"
)

const stream = "0123456789abcdef0123456789abcdef"

func private(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func listen(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: path, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	return listener
}
func credential(conn *net.UnixConn) (*syscall.Ucred, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var peer *syscall.Ucred
	var e error
	err = raw.Control(func(fd uintptr) { peer, e = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
	if err != nil {
		return nil, err
	}
	return peer, e
}

// Explicit disposable broker fixture only. No marker means no subprocess work;
// that standalone early return is not counted as actual bootstrap coverage.
func TestSessionBrokerChild(t *testing.T) {
	if os.Getenv("P1SESSION_BROKER_CHILD") != "1" {
		return
	}
	path, dir := os.Getenv("P1SESSION_BROKER_PATH"), os.Getenv("P1SESSION_CAPTURE")
	expected, _ := strconv.Atoi(os.Getenv("P1SESSION_MANAGER_PID"))
	parent, _ := strconv.Atoi(os.Getenv("P1SESSION_MANAGER_PARENT"))
	listener := listen(t, path)
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(8 * time.Second))
	fmt.Print("READY\n")
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal("broker accept failed")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	peer, err := credential(conn)
	if err != nil || int(peer.Pid) != expected || int(peer.Uid) != os.Getuid() {
		t.Fatal("unexpected actual manager peer")
	}
	raw := make([]byte, MaxPacket+1)
	n, err := conn.Read(raw)
	if err != nil {
		t.Fatal("request receive failed")
	}
	var request Request
	if !strict(raw[:n], &request, "version", "stream", "manager_pid", "manager_parent_pid") || request.Version != 1 || request.Stream != stream || request.ManagerPID != int(peer.Pid) || request.ManagerParentPID != parent {
		t.Fatal("request identity mismatch")
	}
	a, b, err := p1trace.SocketPair()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	collector, err := p1trace.NewCollector(a, int(peer.Pid), int(peer.Uid), stream, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	response, _ := json.Marshal(Response{1, stream})
	if _, _, err = conn.WriteMsgUnix(response, syscall.UnixRights(int(b.Fd())), nil); err != nil {
		t.Fatal("FD response failed")
	}
	conn.Close()
	b.Close()
	pending, err := collector.Next(context.Background())
	if err != nil || pending.PeerPID() != expected || pending.Event().Component != "fixture" || pending.Event().Point != "bootstrap-check" {
		t.Fatal("actual producer message credential mismatch")
	}
	if err = pending.Decide(context.Background(), p1trace.Continue); err != nil {
		t.Fatal("authenticated ACK failed")
	}
}
func TestSessionBootstrapNonParentSupervisor(t *testing.T) {
	dir := private(t)
	capture := private(t)
	path := filepath.Join(dir, "broker.sock")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSessionBrokerChild$", "-test.timeout=9s")
	cmd.Env = []string{"P1SESSION_BROKER_CHILD=1", "P1SESSION_BROKER_PATH=" + path, "P1SESSION_CAPTURE=" + capture, "P1SESSION_MANAGER_PID=" + strconv.Itoa(os.Getpid()), "P1SESSION_MANAGER_PARENT=" + strconv.Itoa(os.Getppid())}
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned broker cleanup incomplete")
		}
	})
	reader := bufio.NewReader(io.LimitReader(stdout, 4097))
	ready, err := reader.ReadString('\n')
	if err != nil || ready != "READY\n" {
		t.Fatal("broker ready failed")
	}
	if cmd.Process.Pid == os.Getppid() {
		t.Fatal("fixture does not establish independent nonparent supervisor")
	}
	producer, err := Connect(ctx, path, stream, cmd.Process.Pid, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	decision, err := producer.Emit(ctx, p1trace.Event{Component: "fixture", Point: "bootstrap-check", Data: json.RawMessage(`{}`)})
	if err != nil || decision != p1trace.Continue {
		t.Fatal("actual FD/SCM credential ACK failed", err)
	}
	if err = <-done; err != nil {
		t.Fatal("broker process failed")
	}
	done <- nil
	rows, err := os.ReadDir(capture)
	if err != nil || len(rows) != 3 {
		t.Fatal("actual wire/peer/decision originals missing")
	}
	t.Log("actual non-parent supervisor peer credentials, SCM_RIGHTS and producer ACK verified")
}

func TestSessionBootstrapRejectsResponses(t *testing.T) {
	for _, mode := range []string{"unknown", "duplicate", "wrong-stream", "wrong-version", "null", "missing-fd", "two-fds", "truncated-rights", "oversize", "wrong-type", "extra-packet", "extra-rights", "empty-extra-packet", "hold-open", "wrong-peer"} {
		t.Run(mode, func(t *testing.T) {
			dir := private(t)
			path := filepath.Join(dir, "b.sock")
			listener := listen(t, path)
			defer listener.Close()
			a, b, err := p1trace.SocketPair()
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			defer b.Close()
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				conn, err := listener.AcceptUnix()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				request := make([]byte, MaxPacket+1)
				if _, err = conn.Read(request); err != nil {
					return
				}
				body := []byte(`{"version":1,"stream":"` + stream + `"}`)
				rights := syscall.UnixRights(int(b.Fd()))
				switch mode {
				case "unknown":
					body = []byte(`{"version":1,"stream":"` + stream + `","extra":true}`)
				case "duplicate":
					body = []byte(`{"version":1,"version":1,"stream":"` + stream + `"}`)
				case "wrong-stream":
					body = []byte(`{"version":1,"stream":"` + strings.Repeat("f", 32) + `"}`)
				case "wrong-version":
					body = []byte(`{"version":2,"stream":"` + stream + `"}`)
				case "null":
					body = []byte("null")
				case "missing-fd":
					rights = nil
				case "two-fds":
					rights = syscall.UnixRights(int(b.Fd()), int(b.Fd()))
				case "truncated-rights":
					fds := make([]int, 32)
					for i := range fds {
						fds[i] = int(b.Fd())
					}
					rights = syscall.UnixRights(fds...)
				case "oversize":
					body = make([]byte, MaxPacket+1)
				case "wrong-type":
					file, e := os.Open("/dev/null")
					if e != nil {
						return
					}
					defer file.Close()
					rights = syscall.UnixRights(int(file.Fd()))
				}
				conn.WriteMsgUnix(body, rights, nil)
				if mode == "empty-extra-packet" {
					conn.WriteMsgUnix([]byte{}, nil, nil)
				}
				if mode == "extra-packet" {
					conn.WriteMsgUnix(body, nil, nil)
				}
				if mode == "extra-rights" {
					conn.WriteMsgUnix(body, rights, nil)
				}
				if mode == "hold-open" {
					io.Copy(io.Discard, conn)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			pid := os.Getpid()
			if mode == "wrong-peer" {
				pid++
			}
			before := fdCount(t)
			producer, err := Connect(ctx, path, stream, pid, os.Getuid())
			if producer != nil {
				producer.Close()
			}
			<-ended
			if err == nil {
				t.Fatal("invalid broker response accepted")
			}
			if mode == "hold-open" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("bootstrap context deadline cause lost")
			}
			if after := fdCount(t); after != before {
				t.Fatalf("rejected FD transfer leaked descriptors: before=%d after=%d", before, after)
			}
		})
	}
}
func fdCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
func TestSessionBootstrapRejectsInputs(t *testing.T) {
	dir := private(t)
	path := filepath.Join(dir, "broker.sock")
	listener := listen(t, path)
	defer listener.Close()
	for _, mode := range []string{"nil-context", "canceled", "stream", "pid", "uid", "relative", "symlink", "permissions"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			p, s, pid, uid := path, stream, os.Getpid(), os.Getuid()
			switch mode {
			case "nil-context":
				ctx = nil
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "stream":
				s = "BAD"
			case "pid":
				pid = 0
			case "uid":
				uid++
			case "relative":
				p = "relative.sock"
			case "symlink":
				link := filepath.Join(private(t), "linked")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				p = filepath.Join(link, "broker.sock")
			case "permissions":
				bad := t.TempDir()
				if err := os.Chmod(bad, 0755); err != nil {
					t.Fatal(err)
				}
				p = filepath.Join(bad, "broker.sock")
			}
			producer, err := Connect(ctx, p, s, pid, uid)
			if producer != nil {
				producer.Close()
			}
			if err == nil {
				t.Fatal("invalid bootstrap input accepted")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("canceled cause lost")
			}
		})
	}
}
