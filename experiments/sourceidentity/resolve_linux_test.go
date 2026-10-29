//go:build linux && amd64

package sourceidentity

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestSourceHelper(t *testing.T) {
	mode := os.Getenv("HOOPA_SOURCE_TEST_HELPER")
	if mode == "" {
		return
	}
	if mode == "share" {
		f := os.NewFile(3, "shared-source-socket")
		defer f.Close()
		conn, err := net.FileConn(f)
		if err != nil {
			t.Fatal("shared socket unavailable")
		}
		defer conn.Close()
		fmt.Fprintln(os.Stdout, "ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp4", os.Getenv("HOOPA_SOURCE_TEST_ENTRY"), 3*time.Second)
		if err != nil {
			t.Fatal("entry unavailable")
		}
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		var b [1]byte
		_, err = io.ReadFull(conn, b[:]) // Entry holds admission before any client payload.
		conn.Close()
		if err != nil || b[0] != 1 {
			t.Fatal("admission failed")
		}
	}
}

func originalEntry(t *testing.T) *net.TCPListener {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal("loopback permission required:", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_ = ln.SetDeadline(time.Now().Add(30 * time.Second))
	return ln
}

func child(t *testing.T, mode, entry string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSourceHelper$")
	cmd.Env = append(os.Environ(), "HOOPA_SOURCE_TEST_HELPER="+mode, "HOOPA_SOURCE_TEST_ENTRY="+entry)
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd
}

func TestSourceOriginalEntryTwoSameNamedProcesses100Connections(t *testing.T) {
	ln := originalEntry(t)
	expected := map[int]Identity{}
	commands := []*exec.Cmd{}
	// Register actual OS PID/start/name before permitting any accepted flow.
	for i := 0; i < 2; i++ {
		cmd := child(t, "connect", ln.Addr().String())
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		id, err := processIdentity(cmd.Process.Pid)
		if err != nil {
			t.Fatal("registration unavailable")
		}
		expected[id.PID] = id
		commands = append(commands, cmd)
	}
	names := map[string]bool{}
	counts := map[int]int{}
	for i := 0; i < 100; i++ {
		conn, err := ln.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		id, err := Resolve(conn.RemoteAddr().(*net.TCPAddr).AddrPort(), conn.LocalAddr().(*net.TCPAddr).AddrPort())
		if err != nil {
			conn.Close()
			if errors.Is(err, ErrVisibility) {
				t.Skip("unverified: cannot inspect the full same-UID owner set; 100-source assertion has NOT passed")
			}
			t.Fatalf("original-source sample %d: %v", i+1, err)
		}
		want, ok := expected[id.PID]
		if !ok || id.StartTicks != want.StartTicks || id.Name != want.Name || id.NetNS != want.NetNS || id.BootID == "" || id.SocketInode == "" {
			conn.Close()
			t.Fatal("source differs from pre-registered process")
		}
		names[id.Name] = true
		counts[id.PID]++
		_, err = conn.Write([]byte{1})
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal("source helper failed")
		}
		if counts[cmd.Process.Pid] != 50 {
			t.Fatal("source sample lost or assigned to other instance")
		}
	}
	if len(names) != 1 || len(counts) != 2 {
		t.Fatal("same-name instances merged")
	}
	t.Log("100/100 held original TCP entries match two pre-registered same-name PIDs; no proxy, terminal-byte or domain guarantee inferred")
}

func TestSourceSharedDescriptorRejectsUniqueOwner(t *testing.T) {
	ln := originalEntry(t)
	conn, err := net.DialTCP("tcp4", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	accepted, err := ln.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	f, err := conn.File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := child(t, "share", "")
	cmd.ExtraFiles = []*os.File{f}
	ready, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(ready).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("shared owner not ready")
	}
	_, err = Resolve(conn.LocalAddr().(*net.TCPAddr).AddrPort(), conn.RemoteAddr().(*net.TCPAddr).AddrPort())
	if errors.Is(err, ErrVisibility) {
		stdin.Close()
		_ = cmd.Wait()
		t.Skip("unverified: owner-set visibility denied before shared-owner assertion")
	}
	if !errors.Is(err, ErrShared) {
		t.Fatalf("shared socket claimed unique: %v", err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal("shared helper failed")
	}
}

func TestSourceRejectsOutsideQualifiedScopeAndRecordsActualIdentity(t *testing.T) {
	for _, address := range []string{"192.0.2.1:1234", "[::1]:1234", "127.0.0.1:0"} {
		if _, err := procAddress(netip.MustParseAddrPort(address)); !errors.Is(err, ErrUnknown) {
			t.Fatal("unsupported address claimed supported")
		}
	}
	id, err := processIdentity(os.Getpid())
	if err != nil || id.PID != os.Getpid() || id.Name == "" || id.NetNS == "" {
		t.Fatal("actual process identity unavailable")
	}
	if _, err := strconv.ParseUint(id.StartTicks, 10, 64); err != nil {
		t.Fatal("start identity is not an OS fact")
	}
	if _, err := processIdentity(-1); !errors.Is(err, ErrUnknown) {
		t.Fatal("invented process accepted")
	}
}
