//go:build linux && amd64

package mihomoevents

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/protocol/telemetrywire"
)

const transportDeadline = 30 * time.Second
const transportSecret = "closure-integration-fixture-only"

type transportRun struct {
	t                                              *testing.T
	dir, binary, trace, runtime, config, collector string
	entry, health                                  int
	deadline                                       time.Time
	offlineCollector                               *transportBoundCollector
}

func transportPrivateWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal("private fixture write failed", err, closeErr)
	}
}
func transportPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// transportBoundCollector owns an IPv4 TCP port without listening. No
// SO_REUSEADDR is enabled; another process cannot become its collector while
// the offline source checkpoint/final is produced. Close is idempotent even
// when failure cleanup runs after the descriptor number has been reused.
type transportBoundCollector struct {
	fd   int
	port int
	once sync.Once
	err  error
}

func (b *transportBoundCollector) close() error {
	b.once.Do(func() { b.err = syscall.Close(b.fd) })
	return b.err
}
func transportReserveOfflineCollector(t *testing.T) *transportBoundCollector {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, syscall.IPPROTO_TCP)
	if err != nil {
		t.Fatal(err)
	}
	b := &transportBoundCollector{fd: fd}
	t.Cleanup(func() {
		if err := b.close(); err != nil {
			t.Error("bound collector socket cleanup", err)
		}
	})
	reuse, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR)
	if err != nil || reuse != 0 {
		t.Fatal("offline reservation must not reuse its port", err)
	}
	if err = syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	address, err := syscall.Getsockname(fd)
	local, ok := address.(*syscall.SockaddrInet4)
	if err != nil || !ok || local.Port == 0 || local.Addr != [4]byte{127, 0, 0, 1} {
		t.Fatal("offline reservation is not IPv4 loopback", err)
	}
	b.port = local.Port
	return b
}
func (b *transportBoundCollector) assertOffline(t *testing.T) {
	t.Helper()
	listening, err := syscall.GetsockoptInt(b.fd, syscall.SOL_SOCKET, syscall.SO_ACCEPTCONN)
	if err != nil || listening != 0 {
		t.Fatal("offline collector socket became a listener", err)
	}
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", b.port), 250*time.Millisecond)
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatal("bound_not_listening did not refuse connect", err)
	}
}

func newTransportRun(t *testing.T, scenario string, echoPort int, offline bool) *transportRun {
	t.Helper()
	if os.Getenv("HOOPA_CLOSURE_NETWORK_TEST") != "1" {
		t.Skip("explicit pinned candidate and local-loopback opt-in required; this is not formal G02")
	}
	candidate, want := os.Getenv("HOOPA_CLOSURE_CANDIDATE"), os.Getenv("HOOPA_CLOSURE_CANDIDATE_SHA256")
	decoded, err := hex.DecodeString(want)
	if !filepath.IsAbs(candidate) || err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != want {
		t.Fatal("explicit absolute candidate and canonical SHA256 required")
	}
	var manifest struct {
		Binary struct {
			SHA256 string `json:"sha256"`
		} `json:"binary"`
	}
	manifestRaw, manifestErr := os.ReadFile("closure-candidate-manifest.json")
	if manifestErr != nil || json.Unmarshal(manifestRaw, &manifest) != nil || manifest.Binary.SHA256 != want {
		t.Fatal("candidate SHA does not match the fixed 001-005 manifest")
	}
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("candidate must be an existing regular non-symlink file")
	}
	dir, err := os.MkdirTemp("/tmp", "hoopa-closure-transport-")
	if err != nil {
		t.Fatal(err)
	}
	r := &transportRun{t: t, dir: dir, binary: filepath.Join(dir, "candidate"), trace: filepath.Join(dir, "trace"), runtime: filepath.Join(dir, "runtime"), deadline: time.Now().Add(transportDeadline)}
	// The source descriptor and copied bytes are verified, never execute the
	// caller's mutable path. This private snapshot is intentionally mode 0500.
	in, err := os.OpenFile(candidate, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	actualInfo, err := in.Stat()
	if err != nil || !os.SameFile(info, actualInfo) {
		in.Close()
		t.Fatal("candidate changed before snapshot")
	}
	out, err := os.OpenFile(r.binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, digest), in)
	syncErr := out.Sync()
	out.Close()
	in.Close()
	if copyErr != nil || syncErr != nil || hex.EncodeToString(digest.Sum(nil)) != want {
		t.Fatal("candidate snapshot digest/write mismatch")
	}
	for _, p := range []string{r.trace, r.runtime, filepath.Join(dir, "receiver")} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ports := map[int]bool{echoPort: true}
	collectorMode := "receiver_listener_before_core"
	if offline {
		r.offlineCollector = transportReserveOfflineCollector(t)
		ports[r.offlineCollector.port] = true
		r.collector = fmt.Sprintf("127.0.0.1:%d", r.offlineCollector.port)
		collectorMode = "bound_not_listening"
	}
	nextPort := func() int {
		for {
			p := transportPort(t)
			if !ports[p] {
				ports[p] = true
				return p
			}
		}
	}
	r.entry, r.health = nextPort(), nextPort()
	if !offline {
		r.collector = fmt.Sprintf("127.0.0.1:%d", nextPort())
	}
	// Stock Mihomo otherwise creates cache.db with mode 0644. Precreate its
	// private runtime file at the required mode; this is not a telemetry ledger.
	transportPrivateWrite(t, filepath.Join(r.runtime, "cache.db"), nil, 0600)
	r.config = filepath.Join(r.runtime, "mihomo.yaml")
	config := fmt.Sprintf("mode: rule\nlog-level: silent\nipv6: false\nfind-process-mode: off\nlisteners:\n  - name: closure-measured\n    type: mixed\n    listen: 127.0.0.1\n    port: %d\n    udp: false\n  - name: closure-health\n    type: mixed\n    listen: 127.0.0.1\n    port: %d\n    udp: false\nhosts:\n  alpha.test: 127.0.0.1\nrules:\n  - MATCH,DIRECT\n", r.entry, r.health)
	transportPrivateWrite(t, r.config, []byte(config), 0600)
	truth, _ := json.MarshalIndent(map[string]any{"scope": "real_transport_subexperiment_not_formal_G02", "scenario": scenario, "candidate_sha256": want, "upload_bytes": "257", "download_bytes": "257", "deadline_seconds": 30, "protocol": "HTTP CONNECT to controlled loopback echo", "startup_preflight": "separate untraced CONNECT plus echo; TCP accept is not readiness", "identity_asserted": false, "collector_initial_mode": collectorMode}, "", "  ")
	transportPrivateWrite(t, filepath.Join(dir, "preregistered.json"), truth, 0600)
	t.Logf("transport scenario=%s preregistered=257B_each_direction deadline=30s candidate_sha256=%s private_evidence=%s", scenario, want, dir)
	return r
}

// Poll only observations, with a fixed run deadline; synchronization of ACK
// stalls below uses explicit channels rather than elapsed-time guesses.
func (r *transportRun) wait(stage string, predicate func() (bool, error)) {
	r.t.Helper()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	remaining := time.Until(r.deadline)
	if remaining <= 0 {
		r.t.Fatal("fixed deadline exceeded", stage)
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	for {
		ok, err := predicate()
		if err != nil {
			r.t.Fatal(stage, err)
		}
		if ok {
			return
		}
		select {
		case <-timer.C:
			r.t.Fatal("fixed deadline exceeded", stage)
		case <-ticker.C:
		}
	}
}
func (r *transportRun) barrier(stage string, ch <-chan struct{}) {
	r.t.Helper()
	r.wait(stage, func() (bool, error) {
		select {
		case <-ch:
			return true, nil
		default:
			return false, nil
		}
	})
}

type transportChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  *os.File
}

func (r *transportRun) start(echoPort, ordinal int) *transportChild {
	r.t.Helper()
	log, err := os.OpenFile(filepath.Join(r.dir, fmt.Sprintf("core-%d.log", ordinal)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command(r.binary, "-d", r.runtime, "-f", r.config)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TZ=UTC", "HOOPA_TRACE_DIR=" + r.trace, "HOOPA_TRACE_INBOUND=closure-measured", "HOOPA_TRACE_GENERATION=transport-generation", "HOOPA_TRACE_RECEIVER=http://" + r.collector + "/events", "HOOPA_TRACE_SECRET=" + transportSecret}
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		log.Close()
		r.t.Fatal(err)
	}
	child := &transportChild{cmd: cmd, done: make(chan struct{}), log: log}
	go func() { child.err = cmd.Wait(); log.Close(); close(child.done) }()
	r.t.Cleanup(func() { child.stop(r.t, false) })
	// Verify proxy forwarding through a differently named, untraced inbound.
	r.wait("untraced forwarding preflight", func() (bool, error) {
		select {
		case <-child.done:
			return false, fmt.Errorf("owned core exited before forwarding; see private core log")
		default:
		}
		conn, reader, err := transportConnect(r.health, echoPort, 250*time.Millisecond)
		if err != nil {
			return false, nil
		}
		defer conn.Close()
		if err = transportExchange(conn, reader, []byte("untraced-health"), 250*time.Millisecond); err != nil {
			return false, nil
		}
		return true, nil
	})
	return child
}
func (c *transportChild) stop(t *testing.T, kill bool) {
	t.Helper()
	select {
	case <-c.done:
		return
	default:
	}
	if kill {
		_ = c.cmd.Process.Kill()
	} else {
		_ = c.cmd.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-c.done:
		t.Logf("owned child reaped pid=%d signal_kill=%v", c.cmd.Process.Pid, kill)
		return
	case <-timer.C:
	}
	_ = c.cmd.Process.Kill()
	select {
	case <-c.done:
		t.Logf("owned child reaped after bounded cleanup kill pid=%d", c.cmd.Process.Pid)
	case <-time.After(3 * time.Second):
		t.Error("owned child cleanup did not complete")
	}
}

func transportConnect(port, target int, timeout time.Duration) (*net.TCPConn, *bufio.Reader, error) {
	raw, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return nil, nil, err
	}
	conn := raw.(*net.TCPConn)
	conn.SetDeadline(time.Now().Add(timeout))
	reader := bufio.NewReader(conn)
	request := fmt.Sprintf("CONNECT alpha.test:%d HTTP/1.1\r\nHost: alpha.test:%d\r\n\r\n", target, target)
	if _, err = io.WriteString(conn, request); err != nil {
		conn.Close()
		return nil, nil, err
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != http.StatusOK {
		conn.Close()
		return nil, nil, fmt.Errorf("CONNECT response unavailable or rejected")
	}
	return conn, reader, nil
}
func transportExchange(conn *net.TCPConn, reader io.Reader, payload []byte, timeout time.Duration) error {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	n, err := conn.Write(payload)
	if err != nil || n != len(payload) {
		return fmt.Errorf("echo write incomplete")
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(reader, got); err != nil {
		return fmt.Errorf("echo reply incomplete")
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("echo payload mismatch")
	}
	return nil
}
func (r *transportRun) flow(echoPort int) (*net.TCPConn, *bufio.Reader) {
	r.t.Helper()
	conn, reader, err := transportConnect(r.entry, echoPort, 3*time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { conn.Close() })
	return conn, reader
}
func transportPayload() []byte {
	p := make([]byte, 257)
	for i := 0; i < 256; i++ {
		p[i] = byte(i)
	}
	p[256] = '!'
	return p
}

func transportEcho(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, _ = io.Copy(conn, conn)
				conn.Close()
				mu.Lock()
				delete(connections, conn)
				mu.Unlock()
			}()
		}
	}()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			ln.Close()
			<-done
			mu.Lock()
			for c := range connections {
				c.Close()
			}
			mu.Unlock()
			workers.Wait()
		})
	}
	return ln.Addr().(*net.TCPAddr).Port, cleanup
}

// Source observation reuses the strict public reader. A live writer can be
// between frame writes: retry a stable read under the original fixed deadline;
// no frame parser is relaxed or tail repaired for this test.
func (r *transportRun) source() (map[string]telemetrywire.RecoveryReport, map[string][]byte, bool) {
	names, err := filepath.Glob(filepath.Join(r.trace, "epoch-*.source.json"))
	if err != nil || len(names) == 0 {
		return nil, nil, false
	}
	reports := map[string]telemetrywire.RecoveryReport{}
	files := map[string][]byte{}
	for _, name := range names {
		frameName := strings.TrimSuffix(name, ".source.json") + ".frames"
		meta, err := os.ReadFile(name)
		if err != nil {
			return nil, nil, false
		}
		raw, err := os.ReadFile(frameName)
		if err != nil {
			return nil, nil, false
		}
		again, err := os.ReadFile(frameName)
		if err != nil || !bytes.Equal(raw, again) {
			return nil, nil, false
		}
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			r.t.Fatal("unsafe source manifest fixture")
		}
		info, err = os.Lstat(frameName)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			r.t.Fatal("unsafe source frame fixture")
		}
		report, err := ReadSourceRecovery(bytes.NewReader(raw), meta)
		if err != nil {
			return nil, nil, false
		}
		reports[report.SourceEpoch] = report
		files[name] = meta
		files[frameName] = raw
	}
	return reports, files, true
}
func (r *transportRun) waitCounter(epoch string, final bool) telemetrywire.RecoveryReport {
	r.t.Helper()
	var found telemetrywire.RecoveryReport
	r.wait("source exact checkpoint/final", func() (bool, error) {
		reports, _, ok := r.source()
		if !ok {
			return false, nil
		}
		for e, report := range reports {
			if epoch != "" && epoch != e {
				continue
			}
			if len(report.Flows) != 1 {
				continue
			}
			f := report.Flows[0]
			counter := f.LastCheckpoint
			if final {
				counter = f.Final
			}
			if counter != nil && counter.UploadBytes == 257 && counter.DownloadBytes == 257 {
				found = report
				return true, nil
			}
		}
		return false, nil
	})
	return found
}
func (r *transportRun) closeFlow(conn *net.TCPConn, reader io.Reader) {
	r.t.Helper()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := conn.CloseWrite(); err != nil {
		r.t.Fatal(err)
	}
	var one [1]byte
	n, err := reader.Read(one[:])
	if n != 0 || err != io.EOF {
		r.t.Fatal("flow did not end at exact echoed boundary", n, err)
	}
	conn.Close()
}

type transportDelivery struct {
	event   SourceEvent
	receipt uint64
}
type transportCollector struct {
	receiver                        *SourceReceiver
	server                          *http.Server
	listener                        net.Listener
	done                            chan struct{}
	handlers                        sync.WaitGroup
	mu                              sync.Mutex
	deliveries                      []transportDelivery
	failure                         error
	holdKind                        string
	held, release                   chan struct{}
	holdOnce, releaseOnce, stopOnce sync.Once
}

func startTransportCollector(t *testing.T, address string, receiver *SourceReceiver, hold string) *transportCollector {
	t.Helper()
	ln, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	c := &transportCollector{receiver: receiver, listener: ln, done: make(chan struct{}), holdKind: hold, held: make(chan struct{}), release: make(chan struct{})}
	c.server = &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.handlers.Add(1)
		defer c.handlers.Done()
		if r.Method != http.MethodPost || r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer "+transportSecret {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, sourceInputLimit+1))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		event, err := DecodeSourceEvent(raw)
		if err != nil {
			c.fail(err)
			w.WriteHeader(400)
			return
		}
		receipt, err := c.receiver.Accept(raw)
		if err != nil {
			c.fail(err)
			w.WriteHeader(409)
			return
		}
		c.mu.Lock()
		c.deliveries = append(c.deliveries, transportDelivery{event, receipt})
		c.mu.Unlock()
		if event.Kind == c.holdKind {
			c.holdOnce.Do(func() { close(c.held) })
			// Accept has already returned after Write+Sync. No receiver lock is held
			// while withholding the HTTP ACK; every 2s-timeout retry shares this gate.
			select {
			case <-c.release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = c.server.Serve(ln); close(c.done) }()
	t.Cleanup(func() { c.stop(t) })
	return c
}
func (c *transportCollector) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil {
		c.failure = err
	}
}
func (c *transportCollector) unblock() { c.releaseOnce.Do(func() { close(c.release) }) }
func (c *transportCollector) stop(t *testing.T) {
	t.Helper()
	c.stopOnce.Do(func() {
		c.unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := c.server.Shutdown(ctx); err != nil {
			_ = c.server.Close()
		}
		cancel()
		<-c.done
		done := make(chan struct{})
		go func() { c.handlers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("collector handler cleanup deadline")
		}
	})
}
func (c *transportCollector) received(epoch, kind string) (transportDelivery, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return transportDelivery{}, false, c.failure
	}
	for _, d := range c.deliveries {
		if d.event.Kind == kind && (epoch == "" || string(d.event.SourceEpoch) == epoch) {
			return d, true, nil
		}
	}
	return transportDelivery{}, false, nil
}
func (r *transportRun) waitReceived(c *transportCollector, epoch, kind string) transportDelivery {
	var found transportDelivery
	r.wait("receiver durable "+kind, func() (bool, error) {
		d, ok, err := c.received(epoch, kind)
		if ok {
			found = d
		}
		return ok, err
	})
	return found
}
func transportReceiver(t *testing.T, dir string) *SourceReceiver {
	t.Helper()
	r, err := OpenSourceReceiver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func transportReceipts(r *SourceReceiver) map[receiverKey]receiverReceipt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[receiverKey]receiverReceipt{}
	for k, v := range r.events {
		out[k] = receiverReceipt{append(json.RawMessage(nil), v.raw...), v.seq}
	}
	return out
}
func transportAssertUniqueFinal(t *testing.T, receiver *SourceReceiver, epoch string) {
	t.Helper()
	count := 0
	for key, receipt := range transportReceipts(receiver) {
		if key.epoch != epoch {
			continue
		}
		event, err := DecodeSourceEvent(receipt.raw)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "closed" {
			count++
			if event.Counter.UploadBytes != 257 || event.Counter.DownloadBytes != 257 {
				t.Fatal("cumulative final changed")
			}
		}
	}
	if count != 1 {
		t.Fatal("terminal cumulative duplicated/missing", count)
	}
}

func (r *transportRun) assertPrivateTree() {
	r.t.Helper()
	err := filepath.Walk(r.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		expected := os.FileMode(0600)
		if info.IsDir() {
			expected = 0700
		} else if path == r.binary {
			expected = 0500
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular private fixture")
		}
		if info.Mode().Perm() != expected {
			return fmt.Errorf("private fixture mode mismatch: %s", filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func TestHoopaClosureTransportOfflineReopen(t *testing.T) {
	// Gate before creating any local endpoint or child process.
	if os.Getenv("HOOPA_CLOSURE_NETWORK_TEST") != "1" {
		t.Skip("explicit pinned candidate/local-loopback opt-in required; not formal G02")
	}
	echo, closeEcho := transportEcho(t)
	t.Cleanup(closeEcho)
	r := newTransportRun(t, "collector_offline_then_durable_closed_ACK_loss_and_reopen", echo, true)
	core := r.start(echo, 0)
	conn, reader := r.flow(echo)
	if err := transportExchange(conn, reader, transportPayload(), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	checkpoint := r.waitCounter("", false)
	r.closeFlow(conn, reader)
	r.waitCounter(checkpoint.SourceEpoch, true)
	// The fixture has exclusively bound this port without Listen throughout
	// source CP/closed production, so no collector could receive that closed.
	r.offlineCollector.assertOffline(t)
	receiver := transportReceiver(t, filepath.Join(r.dir, "receiver"))
	if err := r.offlineCollector.close(); err != nil {
		t.Fatal(err)
	}
	// If a process wins the handoff race, Listen fails rather than weakening
	// the completed bound_not_listening offline observation.
	collector := startTransportCollector(t, r.collector, receiver, "closed")
	r.barrier("closed accepted durably but ACK withheld", collector.held)
	closed := r.waitReceived(collector, checkpoint.SourceEpoch, "closed")
	core.stop(t, true)
	if core.cmd.ProcessState == nil {
		t.Fatal("owned SIGKILL was not reaped")
	}
	status, ok := core.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("core did not exit from the deliberately requested SIGKILL")
	}
	_, oldFiles, ok := r.source()
	if !ok {
		t.Fatal("frozen source snapshot unavailable")
	}
	collector.stop(t)
	accepted := transportReceipts(receiver)
	if err := receiver.Close(); err != nil {
		t.Fatal(err)
	}
	receiver = transportReceiver(t, filepath.Join(r.dir, "receiver"))
	before := len(transportReceipts(receiver))
	for key, value := range accepted {
		receipt, err := receiver.Accept(value.raw)
		if err != nil || receipt != value.seq {
			t.Fatal("journal reopen raw retry changed receipt", key, receipt, err)
		}
	}
	if len(transportReceipts(receiver)) != before {
		t.Fatal("journal reopen duplicate appended")
	}
	reopenedCollector := startTransportCollector(t, r.collector, receiver, "")
	restarted := r.start(echo, 1)
	newConn, newReader := r.flow(echo)
	if err := transportExchange(newConn, newReader, transportPayload(), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var newEpoch string
	r.wait("new epoch checkpoint", func() (bool, error) {
		reports, _, ok := r.source()
		if !ok {
			return false, nil
		}
		for epoch, p := range reports {
			if epoch != checkpoint.SourceEpoch && len(p.Flows) == 1 && p.Flows[0].LastCheckpoint != nil && p.Flows[0].LastCheckpoint.UploadBytes == 257 && p.Flows[0].LastCheckpoint.DownloadBytes == 257 {
				newEpoch = epoch
				return true, nil
			}
		}
		return false, nil
	})
	r.closeFlow(newConn, newReader)
	r.waitCounter(newEpoch, true)
	replay := r.waitReceived(reopenedCollector, checkpoint.SourceEpoch, "closed")
	if replay.receipt != closed.receipt || !bytes.Equal(replay.event.Raw, closed.event.Raw) {
		t.Fatal("old source replay changed raw/ID/seq/receipt")
	}
	r.waitReceived(reopenedCollector, newEpoch, "closed")
	restarted.stop(t, false)
	reopenedCollector.stop(t)
	for name, raw := range oldFiles {
		after, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatal("old source/manifest changed after re-open", err)
		}
	}
	for _, receipt := range accepted {
		found := false
		for name, raw := range oldFiles {
			if strings.HasSuffix(name, ".frames") && bytes.Contains(raw, receipt.raw) {
				found = true
			}
		}
		if !found {
			t.Fatal("durable receiver payload not exact old source raw")
		}
	}
	transportAssertUniqueFinal(t, receiver, checkpoint.SourceEpoch)
	transportAssertUniqueFinal(t, receiver, newEpoch)
	r.assertPrivateTree()
	t.Logf("PASS local transport: bound_not_listening offline 257B+CP+closed; closed persisted before ACK loss; SIGKILL/reopen original raw/receipt; two unique epoch finals each257B; cleanup owned_children=2. No SQL/G01/lazy-ready proof. evidence=%s", r.dir)
}

func TestHoopaClosureTransportStalledACK(t *testing.T) {
	if os.Getenv("HOOPA_CLOSURE_NETWORK_TEST") != "1" {
		t.Skip("explicit pinned candidate/local-loopback opt-in required; not formal G02")
	}
	echo, closeEcho := transportEcho(t)
	t.Cleanup(closeEcho)
	r := newTransportRun(t, "opened_durable_ACK_stall_does_not_block_existing_forwarding", echo, false)
	receiver := transportReceiver(t, filepath.Join(r.dir, "receiver"))
	collector := startTransportCollector(t, r.collector, receiver, "opened")
	core := r.start(echo, 0)
	conn, reader := r.flow(echo)
	r.barrier("opened durable ACK stall", collector.held)
	if err := transportExchange(conn, reader, transportPayload(), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	checkpoint := r.waitCounter("", false)
	r.closeFlow(conn, reader)
	r.waitCounter(checkpoint.SourceEpoch, true)
	if _, received, err := collector.received(checkpoint.SourceEpoch, "closed"); err != nil || received {
		t.Fatal("source advanced past deliberately withheld ACK", err)
	}
	collector.unblock()
	r.waitReceived(collector, checkpoint.SourceEpoch, "closed")
	core.stop(t, false)
	collector.stop(t)
	transportAssertUniqueFinal(t, receiver, checkpoint.SourceEpoch)
	r.assertPrivateTree()
	t.Logf("PASS local transport: held opened ACK after durable Accept; existing flow echoed257B and persisted CP/closed before release; unique final257B after replay; cleanup owned_children=1. No SQL/G01/lazy-ready proof. evidence=%s", r.dir)
}
