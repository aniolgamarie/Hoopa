//go:build linux && amd64

package mihomoevents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	profile "hoopa/experiments/mihomo-events/capabilityprofile"
)

const capabilitySecret = "controller-ssssssssssssssssssssssssssss"
const capabilityPasswordA = "entry-a-pppppppppppppppppppppppppppppppp"
const capabilityPasswordB = "entry-b-qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"

type capabilityFixture struct {
	cfg              profile.Config
	raw              []byte
	hash, generation string
	ports            []int
}
type capabilityHarness struct {
	t                  *testing.T
	dir, binary        string
	ordinal            int
	echoPort, httpPort int
	httpRequests       atomic.Int64
	a, b               *capabilityProxy
}
type capabilityProcess struct {
	cmd                          *exec.Cmd
	done                         chan struct{}
	err                          error
	config, runtime, log, stdout string
	fixture                      capabilityFixture
}
type capabilityProxy struct {
	listener   net.Listener
	count      atomic.Int64
	mu         sync.Mutex
	conns      map[net.Conn]bool
	workers    sync.WaitGroup
	acceptDone chan struct{}
}

// TestCapabilityCandidate exercises only an explicitly supplied fixed candidate.
// Default package tests do not launch candidates or open experiment listeners.
func TestCapabilityCandidate(t *testing.T) {
	if os.Getenv("HOOPA_CAPABILITY_NETWORK_TEST") != "1" {
		t.Skip("set HOOPA_CAPABILITY_NETWORK_TEST=1 with an absolute candidate and SHA256")
	}
	h := newCapabilityHarness(t)
	t.Run("reject-before-listen", func(t *testing.T) { h.rejections(t) })
	f := h.fixture("gen:capability-1")
	p := h.start(t, f, nil, nil)
	h.ready(t, p)
	t.Run("routing-and-auth", func(t *testing.T) { h.routingAndAuth(t, p) })
	t.Run("controller", func(t *testing.T) { h.controllerChecks(t, p) })
	t.Run("sighup-and-rejected-replacement", func(t *testing.T) {
		changed := f.cfg
		changed.Rules = []string{"MATCH,node:b"}
		raw, _ := json.Marshal(changed)
		if err := os.WriteFile(p.config, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := p.cmd.Process.Signal(syscall.SIGHUP); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		h.assertStatus(t, p, "running")
		h.expectRoute(t, p, 0, "alpha.test", h.a)
		bad := h.fixture("gen:bad-replacement")
		bad.cfg.Rules = []string{"PROCESS-NAME,private-app,node:a", "MATCH,node:a"}
		bad.refresh()
		h.reject(t, bad, nil, nil, "")
		h.expectRoute(t, p, 0, "alpha.test", h.a)
	})
	t.Run("bind-rollback", func(t *testing.T) {
		next := h.fixture("gen:occupied")
		occupied, err := net.Listen("tcp4", capabilityAddress(next.ports[2]))
		if err != nil {
			t.Fatal(err)
		}
		defer occupied.Close()
		h.reject(t, next, nil, nil, capabilityAddress(next.ports[2]))
		h.expectRoute(t, p, 0, "alpha.test", h.a)
	})
	t.Run("drain-and-independent-replacement", func(t *testing.T) {
		old, br, err := h.connect(p.fixture, 0, "alpha.test", h.echoPort, "user-a", capabilityPasswordA)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		capabilityExchange(t, old, br, "before-drain")
		h.control(t, p, "POST", "/hoopa/drain", capabilitySecret, 200)
		h.control(t, p, "POST", "/hoopa/drain", capabilitySecret, 200)
		h.assertStatus(t, p, "draining")
		for _, port := range p.fixture.ports[1:] {
			c, err := net.DialTimeout("tcp4", capabilityAddress(port), 200*time.Millisecond)
			if err == nil {
				c.Close()
				t.Fatal("new TCP accepted after drain")
			}
		}
		capabilityExchange(t, old, br, "after-drain")
		replacement := h.fixture("gen:capability-2")
		replacement.cfg.Rules = []string{"MATCH,node:b"}
		replacement.refresh()
		fresh := h.start(t, replacement, nil, nil)
		h.ready(t, fresh)
		h.expectRoute(t, fresh, 0, "alpha.test", h.b)
		capabilityExchange(t, old, br, "old-stream-after-replacement")
		if err := old.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		_ = old.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := br.ReadByte(); err != io.EOF {
			t.Fatalf("old flow did not close normally: %v", err)
		}
		h.assertStatus(t, p, "draining")
	})
	h.noSecrets(t, p.log)
}

func newCapabilityHarness(t *testing.T) *capabilityHarness {
	t.Helper()
	source, want := os.Getenv("HOOPA_CAPABILITY_CANDIDATE"), os.Getenv("HOOPA_CAPABILITY_CANDIDATE_SHA256")
	digest, err := hex.DecodeString(want)
	if !filepath.IsAbs(source) || len(digest) != sha256.Size || err != nil || hex.EncodeToString(digest) != want {
		t.Fatal("explicit absolute candidate and canonical SHA256 required")
	}
	before, err := os.Lstat(source)
	if err != nil || !before.Mode().IsRegular() {
		t.Fatal("candidate must be a regular non-symlink file")
	}
	in, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	actual, err := in.Stat()
	if err != nil || !os.SameFile(before, actual) {
		t.Fatal("candidate changed before snapshot")
	}
	dir, err := os.MkdirTemp("/tmp", "hoopa-t135-capability-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h := &capabilityHarness{t: t, dir: dir, binary: filepath.Join(dir, "candidate")}
	out, err := os.OpenFile(h.binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != want {
		t.Fatal("candidate snapshot hash/write mismatch")
	}
	info, err := os.Stat(h.binary)
	if err != nil || info.Mode().Perm() != 0500 {
		t.Fatal("candidate snapshot is not private executable")
	}
	// Artifacts are deliberately retained in the private /tmp directory.
	t.Logf("candidate_sha256=%s private_evidence=%s", want, dir)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.echoPort = echo.Addr().(*net.TCPAddr).Port
	var echoMu sync.Mutex
	echoConns := map[net.Conn]bool{}
	var echoWorkers sync.WaitGroup
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			echoMu.Lock()
			echoConns[c] = true
			echoMu.Unlock()
			echoWorkers.Add(1)
			go func() {
				defer echoWorkers.Done()
				defer c.Close()
				defer func() { echoMu.Lock(); delete(echoConns, c); echoMu.Unlock() }()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	t.Cleanup(func() {
		echo.Close()
		<-echoDone
		echoMu.Lock()
		for c := range echoConns {
			c.Close()
		}
		echoMu.Unlock()
		echoWorkers.Wait()
	})
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.httpRequests.Add(1)
		_, _ = io.WriteString(w, "controlled-http-target")
	}))
	t.Cleanup(httpTarget.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(httpTarget.URL, "http://"))
	h.httpPort, _ = strconv.Atoi(port)
	h.a = h.newProxy(t)
	h.b = h.newProxy(t)
	return h
}
func capabilityAddress(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }
func (h *capabilityHarness) newProxy(t *testing.T) *capabilityProxy {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &capabilityProxy{listener: ln, conns: map[net.Conn]bool{}, acceptDone: make(chan struct{})}
	go func() {
		defer close(p.acceptDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.conns[c] = true
			p.mu.Unlock()
			p.workers.Add(1)
			go func() {
				defer p.workers.Done()
				defer c.Close()
				defer func() { p.mu.Lock(); delete(p.conns, c); p.mu.Unlock() }()
				_ = c.SetDeadline(time.Now().Add(20 * time.Second))
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != "CONNECT" {
					return
				}
				host, port, err := net.SplitHostPort(req.Host)
				if err != nil {
					return
				}
				n, _ := strconv.Atoi(port)
				if n != h.echoPort && n != h.httpPort {
					return
				}
				if host != "alpha.test" && host != "beta.test" && host != "gamma.test" && host != "http.test" && host != "127.0.0.1" {
					return
				}
				up, err := net.DialTimeout("tcp4", capabilityAddress(n), time.Second)
				if err != nil {
					return
				}
				defer up.Close()
				p.count.Add(1)
				if _, err = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
					return
				}
				copied := make(chan struct{})
				go func() {
					defer close(copied)
					_, _ = io.Copy(up, br)
					if tcp, ok := up.(*net.TCPConn); ok {
						_ = tcp.CloseWrite()
					}
				}()
				_, _ = io.Copy(c, up)
				c.Close()
				up.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-p.acceptDone
		p.mu.Lock()
		for c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
		p.workers.Wait()
	})
	return p
}
func (h *capabilityHarness) fixture(generation string) capabilityFixture {
	listeners := []net.Listener{}
	ports := []int{}
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			h.t.Fatal(err)
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range listeners {
		ln.Close()
	}
	f := capabilityFixture{generation: generation, ports: ports}
	f.cfg = profile.Config{Mode: "rule", LogLevel: "silent", FindProcessMode: "off", Controller: capabilityAddress(ports[0]), Secret: capabilitySecret,
		Listeners: []profile.Entry{{Name: "entry-a", Type: "mixed", Listen: "127.0.0.1", Port: uint16(ports[1]), Users: []profile.User{{Username: "user-a", Password: capabilityPasswordA}}}, {Name: "entry-b", Type: "mixed", Listen: "127.0.0.1", Port: uint16(ports[2]), Users: []profile.User{{Username: "user-b", Password: capabilityPasswordB}}}},
		Hosts:     map[string]string{"alpha.test": "127.0.0.1", "beta.test": "127.0.0.1", "gamma.test": "127.0.0.1", "http.test": "127.0.0.1"},
		Proxies:   []profile.Node{{Name: "node:a", Type: "http", Server: "127.0.0.1", Port: uint16(h.a.listener.Addr().(*net.TCPAddr).Port)}, {Name: "node:b", Type: "http", Server: "127.0.0.1", Port: uint16(h.b.listener.Addr().(*net.TCPAddr).Port)}},
		Groups:    []profile.Group{}, Rules: []string{"AND,((IN-NAME,entry-b),(DOMAIN,alpha.test)),node:b", "DOMAIN,alpha.test,node:a", "IN-NAME,entry-b,node:b", "DOMAIN-SUFFIX,test,node:a", "MATCH,node:b"}}
	f.refresh()
	if _, err := profile.Validate(f.raw, generation); err != nil {
		h.t.Fatal("invalid test fixture", err)
	}
	return f
}
func (f *capabilityFixture) refresh() { f.raw, _ = json.Marshal(f.cfg); f.rehash() }
func (f *capabilityFixture) rehash() {
	sum := sha256.Sum256(f.raw)
	f.hash = hex.EncodeToString(sum[:])
}
func (h *capabilityHarness) start(t *testing.T, f capabilityFixture, env map[string]string, args []string) *capabilityProcess {
	t.Helper()
	h.ordinal++
	dir := filepath.Join(h.dir, fmt.Sprintf("run-%03d", h.ordinal))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := &capabilityProcess{runtime: dir, config: filepath.Join(dir, "config.json"), log: filepath.Join(dir, "core.log"), fixture: f, done: make(chan struct{})}
	transportPrivateWrite(t, p.config, f.raw, 0600)
	if env["TEST_CONFIG_MODE"] != "" {
		if err := os.Chmod(p.config, 0640); err != nil {
			t.Fatal(err)
		}
	}
	if env["TEST_RUNTIME_MODE"] != "" {
		if err := os.Chmod(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	log, err := os.OpenFile(p.log, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if args == nil {
		args = []string{"-d", dir, "-f", p.config}
	} else {
		for i, s := range args {
			if s == "CONFIG" {
				args[i] = p.config
			}
			if s == "RUNTIME" {
				args[i] = dir
			}
		}
	}
	p.cmd = exec.Command(h.binary, args...)
	values := map[string]string{"PATH": "/usr/bin:/bin", "TZ": "UTC", "HOOPA_GUARD_CONFIG_SHA256": f.hash, "HOOPA_GUARD_GENERATION": f.generation}
	for k, v := range env {
		if !strings.HasPrefix(k, "TEST_") {
			values[k] = v
		}
	}
	for k, v := range values {
		p.cmd.Env = append(p.cmd.Env, k+"="+v)
	}
	p.stdout = filepath.Join(dir, "core.stdout")
	stdout, err := os.OpenFile(p.stdout, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Close()
		t.Fatal(err)
	}
	p.cmd.Stdout, p.cmd.Stderr = stdout, log
	if err = p.cmd.Start(); err != nil {
		log.Close()
		stdout.Close()
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); log.Close(); stdout.Close(); close(p.done) }()
	t.Cleanup(func() {
		defer h.noSecrets(t, p.log)
		defer h.noSecrets(t, p.stdout)
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
			return
		case <-time.After(2 * time.Second):
		}
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			t.Error("owned candidate was not reaped")
		}
	})
	return p
}
func (h *capabilityHarness) ready(t *testing.T, p *capabilityProcess) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatalf("candidate exited before ready; private log %s", p.log)
		default:
		}
		status, _, err := h.request(p, "GET", "/hoopa/status", capabilitySecret)
		if err == nil && status == 200 {
			h.assertStatus(t, p, "running")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("candidate never became ready; private log %s", p.log)
}
func (h *capabilityHarness) request(p *capabilityProcess, method, path, secret string) (int, []byte, error) {
	req, err := http.NewRequest(method, "http://"+p.fixture.cfg.Controller+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 500 * time.Millisecond}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	return res.StatusCode, b, err
}
func (h *capabilityHarness) control(t *testing.T, p *capabilityProcess, method, path, secret string, want int) []byte {
	t.Helper()
	status, b, err := h.request(p, method, path, secret)
	if err != nil || status != want {
		t.Fatalf("controller %s %s want=%d got=%d error=%v", method, path, want, status, err)
	}
	for _, value := range []string{capabilitySecret, capabilityPasswordA, capabilityPasswordB} {
		if bytes.Contains(b, []byte(value)) {
			t.Fatal("controller leaked credential")
		}
	}
	return b
}
func (h *capabilityHarness) assertStatus(t *testing.T, p *capabilityProcess, state string) {
	t.Helper()
	b := h.control(t, p, "GET", "/hoopa/status", capabilitySecret, 200)
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("status is not JSON")
	}
	if v["schema_version"] != float64(1) || v["config_generation"] != p.fixture.generation || v["config_sha256"] != p.fixture.hash || v["state"] != state || v["identity_instance"] != false || v["identity_name"] != false || v["identity_path"] != false {
		t.Fatalf("status identity/state/schema differs from registered fixture; private log %s", p.log)
	}
}
func (h *capabilityHarness) noSecrets(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{capabilitySecret, capabilityPasswordA, capabilityPasswordB, "private-app", "private-instance", "/private/application"} {
		if bytes.Contains(b, []byte(value)) {
			t.Fatal("candidate diagnostics leaked protected input")
		}
	}
}

func (h *capabilityHarness) rejections(t *testing.T) {
	cases := []struct {
		name string
		edit func(*capabilityFixture)
		env  map[string]string
		args []string
	}{
		{name: "instance-rule", edit: func(f *capabilityFixture) {
			f.cfg.Rules = []string{"PROCESS-INSTANCE,private-instance,node:a", "MATCH,node:a"}
			f.refresh()
		}},
		{name: "name-rule", edit: func(f *capabilityFixture) {
			f.cfg.Rules = []string{"PROCESS-NAME,private-app,node:a", "MATCH,node:a"}
			f.refresh()
		}},
		{name: "path-rule", edit: func(f *capabilityFixture) {
			f.cfg.Rules = []string{"PROCESS-PATH,/private/application,node:a", "MATCH,node:a"}
			f.refresh()
		}},
		{name: "unreachable-process-rule", edit: func(f *capabilityFixture) {
			f.cfg.Rules = []string{"DOMAIN-SUFFIX,test,node:a", "PROCESS-NAME,private-app,node:b", "MATCH,node:a"}
			f.refresh()
		}},
		{name: "or-rule", edit: func(f *capabilityFixture) {
			f.cfg.Rules = []string{"OR,((DOMAIN,alpha.test),(PROCESS-NAME,private-app)),node:a", "MATCH,node:a"}
			f.refresh()
		}},
		{name: "udp", edit: func(f *capabilityFixture) { f.cfg.Listeners[0].UDP = true; f.refresh() }},
		{name: "nonloopback-entry", edit: func(f *capabilityFixture) { f.cfg.Listeners[0].Listen = "0.0.0.0"; f.refresh() }},
		{name: "nonloopback-controller", edit: func(f *capabilityFixture) { f.cfg.Controller = fmt.Sprintf("0.0.0.0:%d", f.ports[0]); f.refresh() }},
		{name: "nonloopback-proxy", edit: func(f *capabilityFixture) { f.cfg.Proxies[0].Server = "192.0.2.1"; f.refresh() }},
		{name: "named-proxy-bypass", edit: func(f *capabilityFixture) {
			f.raw = bytes.Replace(f.raw, []byte(`"name":"entry-a"`), []byte(`"name":"entry-a","proxy":"node:b"`), 1)
			f.rehash()
		}},
		{name: "unknown-field", edit: func(f *capabilityFixture) { f.raw = append([]byte(`{"allow-lan":true,`), f.raw[1:]...); f.rehash() }},
		{name: "duplicate-key", edit: func(f *capabilityFixture) { f.raw = append([]byte(`{"mode":"rule",`), f.raw[1:]...); f.rehash() }},
		{name: "missing-user", edit: func(f *capabilityFixture) { f.cfg.Listeners[0].Users = nil; f.refresh() }},
		{name: "missing-controller-secret", edit: func(f *capabilityFixture) { f.cfg.Secret = ""; f.refresh() }},
		{name: "credential-reuse", edit: func(f *capabilityFixture) { f.cfg.Listeners[0].Users[0].Password = capabilitySecret; f.refresh() }},
		{name: "bad-config-hash", env: map[string]string{"HOOPA_GUARD_CONFIG_SHA256": strings.Repeat("0", 64)}},
		{name: "missing-config-hash", env: map[string]string{"HOOPA_GUARD_CONFIG_SHA256": ""}},
		{name: "missing-generation", env: map[string]string{"HOOPA_GUARD_GENERATION": ""}},
		{name: "bad-generation", env: map[string]string{"HOOPA_GUARD_GENERATION": "bad generation"}},
		{name: "trace-generation-mismatch", env: map[string]string{"HOOPA_TRACE_INBOUND": "entry-a", "HOOPA_TRACE_PROFILE": "2", "HOOPA_TRACE_GENERATION": "gen:different"}},
		{name: "config-permissions", env: map[string]string{"TEST_CONFIG_MODE": "0640"}},
		{name: "runtime-permissions", env: map[string]string{"TEST_RUNTIME_MODE": "0750"}},
		{name: "bad-flags", args: []string{"-d", "RUNTIME", "-f", "CONFIG", "-ext-ctl", "0.0.0.0:19090"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := h.fixture("gen:reject")
			if tt.edit != nil {
				tt.edit(&f)
			}
			h.reject(t, f, tt.env, tt.args, "")
		})
	}
	t.Run("validate-only", func(t *testing.T) {
		f := h.fixture("gen:validate-only")
		p := h.start(t, f, nil, []string{"-d", "RUNTIME", "-f", "CONFIG", "-t"})
		h.closedProcess(t, p, "", true)
	})
}
func (h *capabilityHarness) reject(t *testing.T, f capabilityFixture, env map[string]string, args []string, occupied string) {
	t.Helper()
	p := h.start(t, f, env, args)
	h.closedProcess(t, p, occupied, false)
}
func (h *capabilityHarness) closedProcess(t *testing.T, p *capabilityProcess, occupied string, success bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		// Probe throughout startup; a failed child may not briefly accept traffic.
		for _, port := range p.fixture.ports {
			addr := capabilityAddress(port)
			if addr == occupied {
				continue
			}
			c, err := net.DialTimeout("tcp4", addr, 5*time.Millisecond)
			if err == nil {
				c.Close()
				t.Fatalf("rejected/validation candidate briefly opened listener %d; private log %s", port, p.log)
			}
		}
		select {
		case <-p.done:
			goto exited
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate did not exit after bounded validation; private log %s", p.log)
		}
		time.Sleep(time.Millisecond)
	}
exited:
	if success != (p.err == nil) {
		t.Fatalf("unexpected candidate exit success=%v expected=%v; private log %s", p.err == nil, success, p.log)
	}
	for _, port := range p.fixture.ports {
		addr := capabilityAddress(port)
		if addr == occupied {
			continue
		}
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			t.Fatalf("candidate leaked prebound port %d: %v", port, err)
		}
		ln.Close()
	}
	b, err := os.ReadFile(p.log)
	if err != nil {
		t.Fatal(err)
	}
	if !success && string(b) != "Hoopa protected configuration rejected\n" {
		t.Fatalf("rejection diagnostics differ from fixed public error; private log %s", p.log)
	}
	stdout, err := os.ReadFile(p.stdout)
	if err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 {
		t.Fatalf("validation/rejection wrote unexpected stdout; private log %s", p.stdout)
	}
	h.noSecrets(t, p.log)
}
func (h *capabilityHarness) connect(f capabilityFixture, entry int, host string, port int, user, password string) (net.Conn, *bufio.Reader, error) {
	c, err := net.DialTimeout("tcp4", capabilityAddress(f.ports[entry+1]), time.Second)
	if err != nil {
		return nil, nil, err
	}
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	auth := ""
	if user != "" {
		auth = "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password)) + "\r\n"
	}
	_, err = fmt.Fprintf(c, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n%s\r\n", host, port, host, port, auth)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	res, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	if res.StatusCode != 200 {
		c.Close()
		return nil, nil, fmt.Errorf("CONNECT rejected status %d", res.StatusCode)
	}
	return c, br, nil
}
func capabilityExchange(t *testing.T, c net.Conn, br io.Reader, payload string) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, payload); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, len(payload))
	if _, err := io.ReadFull(br, b); err != nil {
		t.Fatal(err)
	}
	if string(b) != payload {
		t.Fatal("controlled echo differs")
	}
}
func (h *capabilityHarness) expectRoute(t *testing.T, p *capabilityProcess, entry int, host string, want *capabilityProxy) {
	t.Helper()
	a, b := h.a.count.Load(), h.b.count.Load()
	user, password := "user-a", capabilityPasswordA
	if entry == 1 {
		user, password = "user-b", capabilityPasswordB
	}
	c, br, err := h.connect(p.fixture, entry, host, h.echoPort, user, password)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	capabilityExchange(t, c, br, "route-"+host)
	da, db := h.a.count.Load()-a, h.b.count.Load()-b
	if want == h.a && !(da == 1 && db == 0) || want == h.b && !(da == 0 && db == 1) {
		t.Fatalf("first match routed through wrong controlled proxy A=%d B=%d", da, db)
	}
}
func (h *capabilityHarness) routingAndAuth(t *testing.T, p *capabilityProcess) {
	h.expectRoute(t, p, 0, "alpha.test", h.a)
	h.expectRoute(t, p, 1, "alpha.test", h.b) // AND precedes DOMAIN and IN-NAME.
	h.expectRoute(t, p, 1, "beta.test", h.b)  // IN-NAME precedes DOMAIN-SUFFIX.
	h.expectRoute(t, p, 0, "beta.test", h.a)
	for _, test := range []struct {
		name, user, password string
		entry                int
	}{{"missing", "", "", 0}, {"wrong", "user-a", "wrong-password", 0}, {"other-entry", "user-b", capabilityPasswordB, 0}, {"reverse-entry", "user-a", capabilityPasswordA, 1}} {
		t.Run("connect-"+test.name, func(t *testing.T) {
			a, b := h.a.count.Load(), h.b.count.Load()
			c, _, err := h.connect(p.fixture, test.entry, "alpha.test", h.echoPort, test.user, test.password)
			if c != nil {
				c.Close()
			}
			if err == nil {
				t.Fatal("unauthorized CONNECT succeeded")
			}
			h.noForward(t, a, b)
		})
	}
	for _, test := range []struct {
		name, user, password string
		entry                int
		command              byte
		ok                   bool
	}{{"authorized", "user-a", capabilityPasswordA, 0, 1, true}, {"other-authorized", "user-b", capabilityPasswordB, 1, 1, true}, {"missing", "", "", 0, 1, false}, {"wrong", "user-a", "wrong", 0, 1, false}, {"cross-entry", "user-b", capabilityPasswordB, 0, 1, false}, {"udp-associate", "user-a", capabilityPasswordA, 0, 3, false}} {
		t.Run("socks5-"+test.name, func(t *testing.T) {
			a, b := h.a.count.Load(), h.b.count.Load()
			c, br, err := h.socks(p.fixture, test.entry, test.user, test.password, test.command)
			if c != nil {
				defer c.Close()
			}
			if test.ok {
				if err != nil {
					t.Fatal(err)
				}
				capabilityExchange(t, c, br, "socks5-echo")
			} else {
				if err == nil {
					t.Fatal("unauthorized or unsupported SOCKS request succeeded")
				}
				h.noForward(t, a, b)
			}
		})
	}
	t.Run("socks4", func(t *testing.T) {
		a, b := h.a.count.Load(), h.b.count.Load()
		c, err := net.DialTimeout("tcp4", capabilityAddress(p.fixture.ports[1]), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(time.Second))
		request := []byte{4, 1, byte(h.echoPort >> 8), byte(h.echoPort), 127, 0, 0, 1, 0}
		_, _ = c.Write(request)
		reply := make([]byte, 8)
		_, err = io.ReadFull(c, reply)
		if err == nil && reply[1] == 90 {
			t.Fatal("SOCKS4 bypassed mandatory auth")
		}
		h.noForward(t, a, b)
	})
	for _, nextAuth := range []string{"", "Basic " + base64.StdEncoding.EncodeToString([]byte("user-a:wrong")), "Basic " + base64.StdEncoding.EncodeToString([]byte("user-b:"+capabilityPasswordB))} {
		t.Run("keepalive-recheck-"+strconv.Itoa(len(nextAuth)), func(t *testing.T) { h.keepalive(t, p, nextAuth) })
	}
}
func (h *capabilityHarness) noForward(t *testing.T, a, b int64) {
	t.Helper()
	time.Sleep(30 * time.Millisecond)
	if h.a.count.Load() != a || h.b.count.Load() != b {
		t.Fatal("rejected request reached controlled upstream")
	}
}
func (h *capabilityHarness) socks(f capabilityFixture, entry int, user, password string, command byte) (net.Conn, *bufio.Reader, error) {
	c, err := net.DialTimeout("tcp4", capabilityAddress(f.ports[entry+1]), time.Second)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (net.Conn, *bufio.Reader, error) { c.Close(); return nil, nil, e }
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	method := byte(2)
	if user == "" {
		method = 0
	}
	if _, err = c.Write([]byte{5, 1, method}); err != nil {
		return fail(err)
	}
	reply := make([]byte, 2)
	if _, err = io.ReadFull(br, reply); err != nil {
		return fail(err)
	}
	if reply[0] != 5 || reply[1] != method {
		return fail(fmt.Errorf("SOCKS method rejected"))
	}
	if method == 2 {
		auth := append([]byte{1, byte(len(user))}, []byte(user)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err = c.Write(auth); err != nil {
			return fail(err)
		}
		if _, err = io.ReadFull(br, reply); err != nil {
			return fail(err)
		}
		if reply[0] != 1 || reply[1] != 0 {
			return fail(fmt.Errorf("SOCKS authentication rejected"))
		}
	}
	host := "alpha.test"
	request := append([]byte{5, command, 0, 3, byte(len(host))}, []byte(host)...)
	request = append(request, byte(h.echoPort>>8), byte(h.echoPort))
	if _, err = c.Write(request); err != nil {
		return fail(err)
	}
	head := make([]byte, 4)
	if _, err = io.ReadFull(br, head); err != nil {
		return fail(err)
	}
	if head[0] != 5 || head[1] != 0 {
		return fail(fmt.Errorf("SOCKS command rejected"))
	}
	n := 0
	switch head[3] {
	case 1:
		n = 4
	case 4:
		n = 16
	case 3:
		b, e := br.ReadByte()
		if e != nil {
			return fail(e)
		}
		n = int(b)
	default:
		return fail(fmt.Errorf("SOCKS invalid reply"))
	}
	if _, err = io.CopyN(io.Discard, br, int64(n+2)); err != nil {
		return fail(err)
	}
	return c, br, nil
}
func (h *capabilityHarness) keepalive(t *testing.T, p *capabilityProcess, nextAuth string) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", capabilityAddress(p.fixture.ports[1]), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(c)
	send := func(auth string) error {
		header := ""
		if auth != "" {
			header = "Proxy-Authorization: " + auth + "\r\n"
		}
		_, err := fmt.Fprintf(c, "GET http://http.test:%d/check HTTP/1.1\r\nHost: http.test:%d\r\n%s\r\n", h.httpPort, h.httpPort, header)
		return err
	}
	before := h.httpRequests.Load()
	if err = send("Basic " + base64.StdEncoding.EncodeToString([]byte("user-a:"+capabilityPasswordA))); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(body) != "controlled-http-target" || h.httpRequests.Load() != before+1 {
		t.Fatal("first keepalive request did not reach controlled target")
	}
	a, b := h.a.count.Load(), h.b.count.Load()
	before = h.httpRequests.Load()
	if err = send(nextAuth); err != nil {
		t.Fatal("authorized HTTP request closed keepalive before authentication could be tested")
	}
	response, err = http.ReadResponse(br, &http.Request{Method: "GET"})
	if err == nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 407 {
			t.Fatalf("HTTP keepalive authorization bypass status=%d", response.StatusCode)
		}
	}
	h.noForward(t, a, b)
	if h.httpRequests.Load() != before {
		t.Fatal("unauthorized keepalive request reached target")
	}
}
func (h *capabilityHarness) controllerChecks(t *testing.T, p *capabilityProcess) {
	for _, secret := range []string{"", "wrong-controller-secret", capabilityPasswordA} {
		for _, method := range []string{"GET", "POST"} {
			path := "/hoopa/status"
			if method == "POST" {
				path = "/hoopa/drain"
			}
			body := h.control(t, p, method, path, secret, 401)
			if bytes.Contains(body, []byte(p.fixture.generation)) || bytes.Contains(body, []byte(p.fixture.hash)) {
				t.Fatal("unauthorized controller exposed status")
			}
		}
	}
	h.assertStatus(t, p, "running")
	a, b := h.a.count.Load(), h.b.count.Load()
	targetRequests := h.httpRequests.Load()
	for _, test := range []struct{ method, path string }{{"PUT", "/configs"}, {"PATCH", "/configs"}, {"PUT", "/proxies/group:main"}, {"PATCH", "/rules"}, {"PUT", "/providers/proxies/test"}, {"PUT", "/providers/rules/test"}, {"POST", "/restart"}, {"GET", fmt.Sprintf("/proxies/node:a/delay?url=http://127.0.0.1:%d/delay&timeout=1000", h.httpPort)}, {"GET", "/configs"}} {
		h.control(t, p, test.method, test.path, "", 401)
		h.control(t, p, test.method, test.path, "wrong-controller-secret", 401)
		h.control(t, p, test.method, test.path, capabilitySecret, 409)
	}
	h.noForward(t, a, b)
	if h.httpRequests.Load() != targetRequests {
		t.Fatal("controller API initiated active target probe")
	}
	h.assertStatus(t, p, "running")
}
