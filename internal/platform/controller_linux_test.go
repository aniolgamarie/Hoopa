//go:build linux && amd64

package platform

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestControllerInodeParsing(t *testing.T) {
	server, _ := controllerEndpoint("127.0.0.1:18994")
	client, _ := controllerEndpoint("127.0.0.1:34567")
	header := " sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"
	row := "0: " + procTCPAddress(server) + " " + procTCPAddress(client) + " 01 00000000:00000000 00:00000000 00000000 1002 0 456 1 0\n"
	valid := header + row
	if inode, err := procTCPInode([]byte(valid), server, client); err != nil || inode != "456" {
		t.Fatalf("valid inode: %q %v", inode, err)
	}
	for name, raw := range map[string]string{
		"duplicate": valid + row, "not-established": strings.Replace(valid, " 01 ", " 0A ", 1),
		"noncanonical-inode": strings.Replace(valid, " 456 ", " 0456 ", 1),
		"overflow-inode":     strings.Replace(valid, " 456 ", " 18446744073709551616 ", 1),
		"short-row":          header + "0: incomplete\n", "bad-header": "invalid\n" + row,
		"unterminated": strings.TrimSuffix(valid, "\n"), "nul": valid + "\x00\n",
		"invalid-address": strings.Replace(valid, "0100007F", "GG00007F", 1),
		"oversized":       strings.Repeat("x", 1<<20) + "\n", "empty": "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := procTCPInode([]byte(raw), server, client); !errors.Is(err, ErrControllerUnverified) {
				t.Fatal("malformed or ambiguous table accepted", err)
			}
		})
	}
	for _, raw := range []string{header, strings.Replace(valid, " 456 ", " 0 ", 1)} {
		if inode, err := procTCPInode([]byte(raw), server, client); err != nil || inode != "" {
			t.Fatal("missing/zero inode became proof", inode, err)
		}
	}
	if inode, err := procTCPInode([]byte(valid), client, server); err != nil || inode != "" {
		t.Fatal("opposite socket end became proof", inode, err)
	}
}

func TestControllerEndpointBoundaries(t *testing.T) {
	for _, value := range []string{"127.0.0.1:1", "127.0.0.1:65535"} {
		if _, err := controllerEndpoint(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"", "localhost:123", "[::1]:123", "127.0.0.2:123", "0.0.0.0:123", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:0123", "127.0.0.1:+123"} {
		if _, err := controllerEndpoint(value); err == nil {
			t.Fatalf("endpoint accepted %q", value)
		}
	}
	for _, address := range []net.Addr{nil, &net.UnixAddr{Name: "fake"}, &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 1}, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1, Zone: "fake"}} {
		if _, err := controllerAddress(address); err == nil {
			t.Fatal("invalid connected address accepted")
		}
	}
}

// The parent helper invocation is empty, not an additional native test. Each
// explicit child accepts real loopback connections and reports application bytes.
func TestControllerFixture(t *testing.T) {
	if os.Getenv("HOOPA_CONTROLLER_FIXTURE") != "1" {
		return
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fmt.Printf("READY %s\n", l.Addr())
	for {
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		b, err := io.ReadAll(io.LimitReader(c, 1024))
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > 0 {
			if _, err = c.Write(b); err != nil {
				t.Fatal(err)
			}
		}
		c.Close()
		fmt.Printf("READ %d\n", len(b))
	}
}

type controllerFixture struct {
	address string
	lines   <-chan string
	handle  *ProcessHandle
}

func startControllerFixture(t *testing.T) controllerFixture {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	nonce, dir := strings.Repeat("cd", 32), t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestControllerFixture$", "-test.timeout=20s")
	cmd.Dir = dir
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, processNonceEnvironment+"=") && !strings.HasPrefix(v, "HOOPA_CONTROLLER_FIXTURE=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, processNonceEnvironment+"="+nonce, "HOOPA_CONTROLLER_FIXTURE=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(lines)
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			default:
				return // a broken fixture must not leave a blocked test goroutine
			}
		}
	}()
	t.Cleanup(func() {
		cmd.Process.Kill() // only this explicit, owned test child
		cmd.Wait()
		<-readDone
	})
	ready := controllerLine(t, lines)
	if !strings.HasPrefix(ready, "READY ") {
		t.Fatalf("unexpected child readiness %q", ready)
	}
	h, err := ObserveProcess(processTestContext(t), cmd.Process.Pid, ProcessSpec{BinaryPath: exe, BinarySHA256: processDigest(raw), Workdir: dir, NonceSHA256: processDigest([]byte(nonce))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return controllerFixture{address: strings.TrimPrefix(ready, "READY "), lines: lines, handle: h}
}

func controllerLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("controller fixture exited unexpectedly")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("controller fixture observation timed out")
		return ""
	}
}

func TestControllerActualOwnerAndRepeatedConnections(t *testing.T) {
	f := startControllerFixture(t)
	for i := 0; i < 2; i++ {
		c, err := f.handle.DialOwnedTCP(processTestContext(t), f.address)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		payload := []byte("AFTER-PROOF")
		if _, err = c.Write(payload); err != nil {
			t.Fatal(err)
		}
		c.CloseWrite()
		got, err := io.ReadAll(c)
		c.Close()
		if err != nil || !bytes.Equal(got, payload) || controllerLine(t, f.lines) != "READ 11" {
			t.Fatal("original proven connection failed", err, string(got))
		}
	}
}

func TestControllerWrongProcessRejectsWithoutBytes(t *testing.T) {
	server := startControllerFixture(t)
	wrong := startProcessFixture(t)
	h, err := ObserveProcess(processTestContext(t), wrong.cmd.Process.Pid, wrong.spec)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	c, err := h.DialOwnedTCP(processTestContext(t), server.address)
	if c != nil || !errors.Is(err, ErrControllerUnverified) {
		t.Fatal("namespace TCP row falsely proved wrong process ownership", err)
	}
	if line := controllerLine(t, server.lines); line != "READ 0" {
		t.Fatal("unverified peer received application bytes", line)
	}
	if err := server.handle.Verify(processTestContext(t)); err != nil {
		t.Fatal("rejection harmed unrelated server", err)
	}
}

func TestControllerCancellationAndClosedHandle(t *testing.T) {
	f := startControllerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := f.handle.DialOwnedTCP(ctx, f.address); c != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled proof accepted", err)
	}
	f.handle.Close()
	if c, err := f.handle.DialOwnedTCP(processTestContext(t), f.address); c != nil || !errors.Is(err, ErrControllerUnverified) {
		t.Fatal("closed original handle accepted", err)
	}
}
