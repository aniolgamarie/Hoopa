//go:build linux && amd64

// Deterministic test application, never a replacement Hoopa CLI or launcher.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func emit(event string, values map[string]any) {
	if values == nil {
		values = map[string]any{}
	}
	values["event"] = event
	raw, _ := json.Marshal(values)
	_, _ = os.Stdout.Write(append(raw, '\n'))
}
func ready() {
	var fg int32
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, 0, syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&fg)))
	tty := []bool{}
	for _, fd := range []uintptr{0, 1, 2} {
		var term syscall.Termios
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&term)))
		tty = append(tty, e == 0)
	}
	emit("fixture_ready", map[string]any{"pid": os.Getpid(), "ppid": os.Getppid(), "pgrp": syscall.Getpgrp(), "foreground": fg, "foreground_ok": err == 0, "tty": tty})
}
func fail() { fmt.Fprintln(os.Stderr, "FIXTURE_FAILURE"); os.Exit(86) }
func tunnel(target string) (net.Conn, *bufio.Reader) {
	destination, err := url.Parse(target)
	if err != nil || destination.Scheme != "http" || destination.Hostname() != "localhost" {
		fail()
	}
	proxy, err := url.Parse(os.Getenv("http_proxy"))
	if err != nil || proxy.Scheme != "http" || proxy.Hostname() != "127.0.0.1" || proxy.User == nil {
		fail()
	}
	conn, err := net.DialTimeout("tcp4", proxy.Host, 4*time.Second)
	if err != nil {
		fail()
	}
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	reader := bufio.NewReader(conn)
	password, ok := proxy.User.Password()
	if !ok {
		fail()
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: destination.Host}, Host: destination.Host, Header: http.Header{}}
	request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
	if request.Write(conn) != nil {
		fail()
	}
	response, err := http.ReadResponse(reader, request)
	if err != nil || response.StatusCode != 200 {
		fail()
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader
}
func get(conn net.Conn, reader *bufio.Reader, target, token, path string) string {
	u, err := url.Parse(target)
	if err != nil || !strings.HasPrefix(path, "/") {
		fail()
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: path}, Host: u.Host, Header: http.Header{}}
	req.Header.Set("X-US4-Client", token)
	if req.Write(conn) != nil {
		fail()
	}
	response, err := http.ReadResponse(reader, req)
	if err != nil || response.StatusCode != 200 {
		fail()
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	response.Body.Close()
	if err != nil || len(body) > 4096 {
		fail()
	}
	_ = conn.SetDeadline(time.Time{})
	return string(body)
}
func held(args []string) {
	if len(args) != 3 {
		fail()
	}
	conn, reader := tunnel(args[0])
	defer conn.Close()
	ready()
	emit("traffic", map[string]any{"body": get(conn, reader, args[0], args[1], args[2])})
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 1 && parts[0] == "EXIT" {
			os.Exit(17)
		}
		if len(parts) != 3 || parts[0] != "GET" {
			fail()
		}
		emit("traffic", map[string]any{"body": get(conn, reader, args[0], parts[1], parts[2])})
	}
	fail()
}
func background(args []string) {
	if len(args) != 4 {
		fail()
	}
	self, err := os.Executable()
	if err != nil {
		fail()
	}
	cmd := exec.Command(self, append([]string{"daemon"}, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if cmd.Start() != nil {
		fail()
	}
	ready()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != "EXIT\n" {
		fail()
	}
	os.Exit(17)
}
func daemon(args []string) {
	if len(args) != 4 {
		fail()
	}
	conn, reader := tunnel(args[0])
	defer conn.Close()
	listener, err := net.Listen("unix", args[3])
	if err != nil {
		fail()
	}
	defer listener.Close()
	defer os.Remove(args[3])
	emit("traffic", map[string]any{"body": get(conn, reader, args[0], args[1], args[2])})
	emit("daemon_ready", map[string]any{"pid": os.Getpid(), "ppid": os.Getppid(), "pgrp": syscall.Getpgrp(), "ttl_seconds": 15})
	timer := time.AfterFunc(15*time.Second, func() { listener.Close() })
	defer timer.Stop()
	for {
		client, err := listener.Accept()
		if err != nil {
			emit("daemon_exit", nil)
			return
		}
		_ = client.SetDeadline(time.Now().Add(4 * time.Second))
		var command struct{ Token, Path string }
		if json.NewDecoder(client).Decode(&command) != nil {
			client.Close()
			fail()
		}
		body := get(conn, reader, args[0], command.Token, command.Path)
		_ = json.NewEncoder(client).Encode(map[string]string{"body": body})
		client.Close()
	}
}
func main() {
	// A driver failure cannot leave this fixture running forever.
	ttl := time.AfterFunc(30*time.Second, func() { os.Exit(85) })
	defer ttl.Stop()
	if len(os.Args) < 2 {
		fail()
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "argv":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
		if err != nil || len(raw) > 65536 {
			fail()
		}
		emit("argv", map[string]any{"args": args, "stdin": base64.StdEncoding.EncodeToString(raw), "environment_marker": os.Getenv("HOOPA_T053_PRIVATE_ENV"), "proxy_lower": os.Getenv("http_proxy"), "proxy_upper": os.Getenv("HTTP_PROXY")})
		fmt.Fprint(os.Stderr, "APP_STDERR_EXACT\n")
		os.Exit(17)
	case "exit":
		if len(args) != 1 {
			fail()
		}
		code, err := strconv.Atoi(args[0])
		if err != nil || code < 0 || code > 255 {
			fail()
		}
		ready()
		os.Exit(code)
	case "wait":
		ready()
		_, _ = io.Copy(io.Discard, os.Stdin)
		fail()
	case "tty-normal":
		ready()
		fmt.Fprint(os.Stderr, "TTY_STDERR_TOKEN\n")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || line != "PTY_INPUT\n" {
			fail()
		}
		emit("tty_input", map[string]any{"line": line})
		os.Exit(17)
	case "tty-wait":
		ready()
		fmt.Fprint(os.Stderr, "TTY_STDERR_TOKEN\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		fail()
	case "held":
		held(args)
	case "background":
		background(args)
	case "daemon":
		daemon(args)
	case "sentinel":
		ready()
		time.Sleep(8 * time.Second)
		emit("sentinel_exit", nil)
	case "sentinel-file":
		if len(args) != 1 {
			fail()
		}
		if os.WriteFile(args[0], []byte("UNEXPECTED_DISPATCH"), 0600) != nil {
			fail()
		}
	default:
		fail()
	}
}
