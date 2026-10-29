//go:build linux && amd64

package mihomoevents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"hoopa/protocol/telemetrywire"
)

const qualityReplyDelay = 120 * time.Millisecond
const qualityDurationTolerance = 250 * time.Millisecond

type qualityReply struct {
	target   string
	delayed  chan struct{}
	release  chan struct{}
	response string
}
type qualityProxy struct {
	listener           net.Listener
	protocol           string
	echoPort, httpPort int
	requests           chan *qualityReply
	stop               chan struct{}
	mu                 sync.Mutex
	conns              map[net.Conn]bool
	targets            []string
	responsePlan       []string
	errors             []error
	workers            sync.WaitGroup
	acceptDone         chan struct{}
	once               sync.Once
}

func newQualityProxy(t *testing.T, protocol string, echoPort, httpPort int) *qualityProxy {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &qualityProxy{listener: ln, protocol: protocol, echoPort: echoPort, httpPort: httpPort, requests: make(chan *qualityReply, 16), stop: make(chan struct{}), conns: map[net.Conn]bool{}, acceptDone: make(chan struct{})}
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
				if err := p.serve(c); err != nil {
					select {
					case <-p.stop:
						return
					default:
					}
					p.mu.Lock()
					p.errors = append(p.errors, err)
					p.mu.Unlock()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		p.once.Do(func() {
			close(p.stop)
			ln.Close()
			<-p.acceptDone
			p.mu.Lock()
			for c := range p.conns {
				c.Close()
			}
			p.mu.Unlock()
			p.workers.Wait()
		})
	})
	return p
}
func (p *qualityProxy) port() int        { return p.listener.Addr().(*net.TCPAddr).Port }
func (p *qualityProxy) endpoint() string { return p.listener.Addr().String() }
func (p *qualityProxy) serve(c net.Conn) error {
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(c)
	target := ""
	if p.protocol == "http" {
		req, err := http.ReadRequest(br)
		if err != nil {
			return err
		}
		if req.Method != http.MethodConnect {
			return fmt.Errorf("proxy expected CONNECT")
		}
		target = req.Host
	} else {
		var greeting [2]byte
		if _, err := io.ReadFull(br, greeting[:]); err != nil {
			return err
		}
		if greeting[0] != 5 || greeting[1] != 1 {
			return fmt.Errorf("unexpected SOCKS greeting")
		}
		var method [1]byte
		if _, err := io.ReadFull(br, method[:]); err != nil || method[0] != 0 {
			return fmt.Errorf("unexpected SOCKS method")
		}
		if _, err := c.Write([]byte{5, 0}); err != nil {
			return err
		}
		var h [4]byte
		if _, err := io.ReadFull(br, h[:]); err != nil || h[0] != 5 || h[1] != 1 || h[2] != 0 {
			return fmt.Errorf("unexpected SOCKS request")
		}
		host := ""
		switch h[3] {
		case 1:
			var addr [4]byte
			if _, err := io.ReadFull(br, addr[:]); err != nil {
				return err
			}
			host = net.IP(addr[:]).String()
		case 3:
			n, err := br.ReadByte()
			if err != nil {
				return err
			}
			name := make([]byte, int(n))
			if _, err := io.ReadFull(br, name); err != nil {
				return err
			}
			host = string(name)
		default:
			return fmt.Errorf("unexpected SOCKS address family")
		}
		var port [2]byte
		if _, err := io.ReadFull(br, port[:]); err != nil {
			return err
		}
		target = net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:]))))
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return err
	}
	// Only preregistered loopback echo and (for nested SOCKS) HTTP endpoints
	// may be reached. No arbitrary CONNECT target becomes a network dial.
	if !(host == "alpha.test" || host == "127.0.0.1") || n != p.echoPort && (p.protocol != "socks5" || n != p.httpPort) {
		return fmt.Errorf("unregistered proxy target")
	}
	upstream, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", n), time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	request := &qualityReply{target: target, delayed: make(chan struct{}), release: make(chan struct{})}
	p.mu.Lock()
	p.targets = append(p.targets, target)
	if len(p.responsePlan) > 0 {
		request.response = p.responsePlan[0]
		p.responsePlan = p.responsePlan[1:]
	}
	p.mu.Unlock()
	select {
	case p.requests <- request:
	case <-p.stop:
		return nil
	}
	timer := time.NewTimer(qualityReplyDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-p.stop:
		return nil
	}
	close(request.delayed)
	select {
	case <-request.release:
	case <-p.stop:
		return nil
	}
	switch request.response {
	case "http_503":
		_, err := io.WriteString(c, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return err
	case "socks_bad_rep":
		_, err := c.Write([]byte{5, 5, 0, 1, 127, 0, 0, 1, 0, 0})
		return err
	case "socks_truncated":
		_, err := c.Write([]byte{5, 0, 0, 1, 127, 0})
		return err
	}
	preload := 0
	if p.protocol == "http" {
		prefix := transportPayload()[:17]
		reply := append([]byte("HTTP/1.1 200 Connection established\r\nX-Quality: preread\r\n\r\n"), prefix...)
		if _, err := c.Write(reply); err != nil {
			return err
		}
		preload = len(prefix)
	} else {
		if _, err := c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
			return err
		}
	}
	// Send all upstream bytes to the actual echo. For HTTP, its first 17 echo
	// bytes were preloaded with the header and are omitted once downstream.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, br)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	if preload > 0 {
		if _, err := io.CopyN(io.Discard, upstream, int64(preload)); err != nil {
			return err
		}
	}
	_, copyErr := io.Copy(c, upstream)
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	<-done
	return copyErr
}

type qualityRun struct {
	*transportRun
	spec      QualityBundleSpec
	routeMap  string
	receiver  *SourceReceiver
	collector *transportCollector
	digest    string
}

func qualityCandidate() (string, string, error) {
	candidate, want := os.Getenv("HOOPA_QUALITY_CANDIDATE"), os.Getenv("HOOPA_QUALITY_CANDIDATE_SHA256")
	decoded, err := hex.DecodeString(want)
	if !filepath.IsAbs(candidate) || err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != want {
		return "", "", fmt.Errorf("explicit absolute candidate and canonical SHA256 required")
	}
	return candidate, want, nil
}
func TestQualityCandidateGateValidation(t *testing.T) {
	t.Setenv("HOOPA_QUALITY_CANDIDATE", "relative")
	t.Setenv("HOOPA_QUALITY_CANDIDATE_SHA256", strings.Repeat("0", 64))
	if _, _, err := qualityCandidate(); err == nil {
		t.Fatal("relative candidate accepted")
	}
	t.Setenv("HOOPA_QUALITY_CANDIDATE", "/tmp/candidate")
	t.Setenv("HOOPA_QUALITY_CANDIDATE_SHA256", "BAD")
	if _, _, err := qualityCandidate(); err == nil {
		t.Fatal("bad digest accepted")
	}
}
func newQualityRun(t *testing.T, a, b *qualityProxy, echoPort int) *qualityRun {
	return newQualityRunScenario(t, a, b, echoPort, "")
}
func newQualityRunScenario(t *testing.T, a, b *qualityProxy, echoPort int, failure string) *qualityRun {
	t.Helper()
	candidate, want, err := qualityCandidate()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("candidate must be regular non-symlink", err)
	}
	dir, err := os.MkdirTemp("/tmp", "hoopa-t134-setup-")
	if err != nil {
		t.Fatal(err)
	}
	base := &transportRun{t: t, dir: dir, binary: filepath.Join(dir, "candidate"), trace: filepath.Join(dir, "trace"), runtime: filepath.Join(dir, "runtime"), deadline: time.Now().Add(45 * time.Second)}
	r := &qualityRun{transportRun: base, digest: want, routeMap: filepath.Join(dir, "route-map-1.json")}
	in, err := os.OpenFile(candidate, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := in.Stat()
	if err != nil || !os.SameFile(info, actual) {
		in.Close()
		t.Fatal("candidate changed before snapshot")
	}
	out, err := os.OpenFile(r.binary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0500)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
	syncErr := out.Sync()
	closeErr := out.Close()
	in.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != want {
		t.Fatal("candidate snapshot digest/write mismatch")
	}
	for _, p := range []string{r.trace, r.runtime, filepath.Join(dir, "receiver")} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ports := map[int]bool{echoPort: true, a.port(): true, b.port(): true}
	next := func() int {
		for {
			p := transportPort(t)
			if !ports[p] {
				ports[p] = true
				return p
			}
		}
	}
	r.entry, r.health = next(), next()
	control := next()
	r.collectorAddr(next())
	r.spec = qualityBundleFixture()
	r.spec.Generation = "generation:quality-1"
	r.spec.EntryEndpoint = fmt.Sprintf("127.0.0.1:%d", r.entry)
	r.spec.HealthEndpoint = fmt.Sprintf("127.0.0.1:%d", r.health)
	r.spec.ControllerEndpoint = fmt.Sprintf("127.0.0.1:%d", control)
	r.spec.ControllerSecret = transportSecret
	r.spec.Nodes[0].Endpoint = a.endpoint()
	r.spec.Nodes[1].Endpoint = b.endpoint()
	r.spec.Nodes[3].Endpoint = a.endpoint()
	bundle, err := BuildQualityBundle(r.spec)
	if err != nil {
		t.Fatal(err)
	}
	r.config = filepath.Join(r.runtime, "mihomo.json")
	transportPrivateWrite(t, r.config, bundle.Config, 0600)
	transportPrivateWrite(t, r.routeMap, bundle.RouteMap, 0600)
	transportPrivateWrite(t, filepath.Join(r.runtime, "cache.db"), nil, 0600)
	truthFields := map[string]any{"scope": "T134_local_quality_not_formal_G03", "candidate_sha256": want, "flows_generation_1": 4, "flows_generation_2": 1, "flow_total_bytes_each_direction": 257, "protocol_reply_delay_ms": 120, "duration_tolerance_lower_ms": 20, "duration_tolerance_upper_ms": 250, "nested_expected_delay_ms": 240, "deadline_seconds": 45, "http_preread_payload_bytes": 17, "input_bundle": json.RawMessage(bundle.Config), "mapping": json.RawMessage(bundle.RouteMap), "startup_preflight": "separate untraced DIRECT CONNECT plus echo", "switch": "A first128B; B257B; A remaining129B; DIRECT257B; nested257B", "identity_asserted": false}
	if failure != "" {
		truthFields = map[string]any{"scope": "T134_actual_rejected_then_retry_not_formal_G03", "candidate_sha256": want, "failure_response": failure, "flow_denominator": 1, "attempt_denominator": 2, "expected_outcomes": []string{"failure", "success"}, "failure_actual_connection_ref": nil, "failure_ready_ns": nil, "failure_duration_ms": nil, "flow_total_bytes_each_direction": 257, "registered_segment_count": 1, "protocol_reply_delay_ms": 120, "duration_tolerance_lower_ms": 20, "duration_tolerance_upper_ms": 250, "deadline_seconds": 45, "input_bundle": json.RawMessage(bundle.Config), "mapping": json.RawMessage(bundle.RouteMap), "startup_preflight": "separate untraced DIRECT CONNECT plus echo", "identity_asserted": false}
	}
	truth, _ := json.MarshalIndent(truthFields, "", "  ")
	transportPrivateWrite(t, filepath.Join(dir, "preregistered.json"), truth, 0600)
	r.receiver = transportReceiver(t, filepath.Join(dir, "receiver"))
	r.collector = startTransportCollector(t, base.collector, r.receiver, "")
	t.Logf("candidate_sha256=%s preregistered=%s", want, dir)
	return r
}
func (r *qualityRun) collectorAddr(port int) {
	r.transportRun.collector = fmt.Sprintf("127.0.0.1:%d", port)
}
func (r *qualityRun) startQuality(echoPort, ordinal int) *transportChild {
	t := r.t
	log, err := os.OpenFile(filepath.Join(r.dir, fmt.Sprintf("core-%d.log", ordinal)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(r.binary, "-d", r.runtime, "-f", r.config)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "TZ=UTC", "HOOPA_TRACE_DIR=" + r.trace, "HOOPA_TRACE_INBOUND=quality-measured", "HOOPA_TRACE_GENERATION=" + r.spec.Generation, "HOOPA_TRACE_PROFILE=2", "HOOPA_TRACE_ROUTE_MAP=" + r.routeMap, "HOOPA_TRACE_RECEIVER=http://" + r.transportRun.collector + "/events", "HOOPA_TRACE_SECRET=" + transportSecret}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	child := &transportChild{cmd: cmd, done: make(chan struct{}), log: log}
	go func() { child.err = cmd.Wait(); log.Close(); close(child.done) }()
	t.Cleanup(func() { child.stop(t, false) })
	r.wait("untraced direct forwarding preflight", func() (bool, error) {
		select {
		case <-child.done:
			return false, fmt.Errorf("owned quality candidate exited; inspect private core log")
		default:
		}
		conn, reader, err := transportConnect(r.health, echoPort, 250*time.Millisecond)
		if err != nil {
			return false, nil
		}
		defer conn.Close()
		return transportExchange(conn, reader, []byte("untraced-health"), 250*time.Millisecond) == nil, nil
	})
	return child
}
func (r *qualityRun) selectNode(id string) {
	body, _ := json.Marshal(map[string]string{"name": id})
	request, err := http.NewRequest(http.MethodPut, "http://"+r.spec.ControllerEndpoint+"/proxies/"+url.PathEscape("group:main"), bytes.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+transportSecret)
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		r.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 204 {
		r.t.Fatal("group switch rejected", response.StatusCode)
	}
}
func (r *qualityRun) received() []telemetrywire.SourceEvent {
	r.collector.mu.Lock()
	defer r.collector.mu.Unlock()
	if r.collector.failure != nil {
		r.t.Fatal("strict receiver failed", r.collector.failure)
	}
	out := []telemetrywire.SourceEvent{}
	seen := map[string]json.RawMessage{}
	for _, d := range r.collector.deliveries {
		key := string(d.event.SourceEpoch) + "/" + string(d.event.EventID)
		if original, ok := seen[key]; ok {
			if !bytes.Equal(original, d.event.Raw) {
				r.t.Fatal("replayed event changed raw bytes")
			}
			continue
		}
		seen[key] = d.event.Raw
		out = append(out, d.event.wire)
	}
	return out
}
func (r *qualityRun) setupCount() int {
	n := 0
	for _, e := range r.received() {
		if e.Kind == "setup_finished" {
			n++
		}
	}
	return n
}
func (r *qualityRun) releaseReply(p *qualityProxy, baseline int) {
	var request *qualityReply
	r.wait("controlled proxy request", func() (bool, error) {
		select {
		case request = <-p.requests:
			return true, nil
		default:
			return false, nil
		}
	})
	r.barrier("complete reply still held after fixed delay", request.delayed)
	if r.setupCount() != baseline {
		r.t.Fatal("ready/setup appeared before actual complete protocol reply")
	}
	r.wait("source snapshot while protocol reply held", func() (bool, error) {
		count, stable, err := r.sourceSetupCount()
		if err != nil {
			return false, err
		}
		if stable && count != baseline {
			return false, fmt.Errorf("source setup appeared before actual complete protocol reply")
		}
		return stable, nil
	})
	close(request.release)
}
func (r *qualityRun) waitQuality(count int) telemetrywire.SourceEvent {
	var found telemetrywire.SourceEvent
	r.wait("strict setup quality", func() (bool, error) {
		n := 0
		for _, e := range r.received() {
			if e.Kind == "setup_finished" {
				n++
				if n == count {
					found = e
					return true, nil
				}
			}
		}
		return false, nil
	})
	return found
}
func (r *qualityRun) verifyQuality(e telemetrywire.SourceEvent, protocol string, minimum time.Duration, nodes ...string) {
	q := e.Quality
	if q == nil || q.Outcome != "success" || q.Protocol != protocol || q.ReadyNS == nil || q.DurationMS == nil || q.FlowDurationMS == nil || q.ActualConnectionRef == nil {
		r.t.Fatal("missing proven setup", protocol, q)
	}
	duration := time.Duration(*q.DurationMS) * time.Millisecond
	if duration < minimum-20*time.Millisecond || duration > minimum+qualityDurationTolerance {
		r.t.Fatalf("ready duration outside preregistered range: protocol=%s duration=%v expected=%v lower=-20ms upper=250ms", protocol, duration, minimum)
	}
	if *q.DurationMS != (*q.ReadyNS-q.AttemptStartNS)/1000000 || *q.FlowDurationMS != (*q.ReadyNS-q.FlowStartNS)/1000000 {
		r.t.Fatal("duration used finish instead of ready")
	}
	var actual telemetrywire.SourceEvent
	binding := false
	for _, a := range r.received() {
		if a.FlowID != e.FlowID || a.AttemptID != e.AttemptID {
			continue
		}
		if a.Phase == "actual_connection" && a.ActualConnectionRef == *q.ActualConnectionRef {
			actual = a
		}
		if a.Phase == "connection_binding" && a.ActualConnectionRef == *q.ActualConnectionRef {
			binding = true
		}
	}
	r.wait("tracker binding", func() (bool, error) {
		for _, a := range r.received() {
			if a.Phase == "connection_binding" && a.FlowID == e.FlowID && a.AttemptID == e.AttemptID && a.ActualConnectionRef == *q.ActualConnectionRef {
				binding = true
			}
		}
		return binding, nil
	})
	if actual.ActualConnectionRef == "" {
		r.t.Fatal("quality has no earlier actual connection")
	}
	got := []string{}
	groupSeen := false
	for _, h := range actual.OrderedChain {
		if h.NodeID != nil {
			got = append(got, *h.NodeID)
			adapter := ""
			for _, n := range r.spec.Nodes {
				if n.ID == *h.NodeID {
					adapter = map[string]string{"http": "Http", "socks5": "Socks5", "direct": "Direct"}[n.Type]
				}
			}
			if h.InternalName != *h.NodeID || h.AdapterType != adapter {
				r.t.Fatal("actual node mapping changed type or internal ID")
			}
		}
		if h.GroupID != nil {
			if *h.GroupID != "group:main" || h.InternalName != "group:main" || h.AdapterType != "Selector" {
				r.t.Fatal("actual group mapping mismatch")
			}
			groupSeen = true
		}
		if h.ProviderID != nil || h.ProviderName != "" {
			r.t.Fatal("inline route invented provider")
		}
	}
	if (protocol != "direct_tcp") != groupSeen {
		r.t.Fatal("actual group identity missing or invented")
	}
	if strings.Join(got, ",") != strings.Join(nodes, ",") {
		r.t.Fatalf("actual core chain=%v expected=%v full=%+v", got, nodes, actual.OrderedChain)
	}
	r.t.Logf("quality flow=%s protocol=%s ready_ns=%d finish_ns=%d duration_ms=%d wait_ms=%d chain=%v", e.FlowID, protocol, *q.ReadyNS, q.FinishNS, *q.DurationMS, q.ObservedWaitMS, got)
}
func (r *qualityRun) waitFinal(flow string) {
	r.wait("exact 257B cumulative final", func() (bool, error) {
		for _, e := range r.received() {
			if e.Kind == "closed" && e.FlowID == flow {
				if e.Counter == nil || e.Counter.UploadBytes != 257 || e.Counter.DownloadBytes != 257 || len(e.Segments) != 1 || e.Segments[0].UploadBytes != 257 || e.Segments[0].DownloadBytes != 257 || e.UnallocatedUpload != 0 || e.UnallocatedDownload != 0 {
					return false, fmt.Errorf("final/segment conservation differs from preregistered257B")
				}
				return true, nil
			}
		}
		return false, nil
	})
}
func (r *qualityRun) connectTarget(echoPort int, host string) (*net.TCPConn, *bufio.Reader) {
	if host == "alpha.test" {
		return r.flow(echoPort)
	}
	c, err := net.DialTimeout("tcp4", r.spec.EntryEndpoint, time.Second)
	if err != nil {
		r.t.Fatal(err)
	}
	conn := c.(*net.TCPConn)
	r.t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(conn)
	_, err = fmt.Fprintf(conn, "CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, echoPort, host, echoPort)
	if err != nil {
		r.t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != 200 {
		r.t.Fatal("direct inbound CONNECT", err)
	}
	return conn, br
}
func qualityWrite(t *testing.T, c *net.TCPConn, payload []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Write(payload); err != nil || n != len(payload) {
		t.Fatal("measured write", n, err)
	}
}
func qualityRead(t *testing.T, c *net.TCPConn, br io.Reader, want []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(br, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("measured echo mismatch", err)
	}
}
func (r *qualityRun) frozenFiles() map[string][]byte {
	names, err := filepath.Glob(filepath.Join(r.trace, "epoch-*.source.json"))
	if err != nil || len(names) == 0 {
		r.t.Fatal("no source manifests", err)
	}
	files := map[string][]byte{}
	for _, name := range names {
		meta, err := os.ReadFile(name)
		if err != nil {
			r.t.Fatal(err)
		}
		frameName := strings.TrimSuffix(name, ".source.json") + ".frames"
		frames, err := os.ReadFile(frameName)
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := ReadSourceRecovery(bytes.NewReader(frames), meta); err != nil {
			r.t.Fatal("frozen source strict lifecycle/recovery", err)
		}
		files[name] = meta
		files[frameName] = frames
	}
	return files
}
func TestHoopaQualityActualTwoLines(t *testing.T) {
	// This check precedes every listen, process, file and candidate lookup.
	if os.Getenv("HOOPA_QUALITY_NETWORK_TEST") != "1" {
		t.Skip("explicit pinned quality candidate/local-loopback opt-in required; not formal G03")
	}
	if _, _, err := qualityCandidate(); err != nil {
		t.Fatal(err)
	}
	echo, closeEcho := transportEcho(t)
	t.Cleanup(closeEcho)
	a := newQualityProxy(t, "http", echo, 0)
	b := newQualityProxy(t, "socks5", echo, a.port())
	r := newQualityRun(t, a, b, echo)
	core := r.startQuality(echo, 0)
	payload := transportPayload()
	// A is retained while current selection moves to B.
	ac, ar := r.connectTarget(echo, "alpha.test")
	qualityWrite(t, ac, payload[:128])
	r.releaseReply(a, 0)
	qualityRead(t, ac, ar, payload[:128])
	qa := r.waitQuality(1)
	r.verifyQuality(qa, "http_connect", qualityReplyDelay, "node:a")
	r.selectNode("node:b")
	bc, br := r.connectTarget(echo, "alpha.test")
	qualityWrite(t, bc, payload)
	r.releaseReply(b, 1)
	qualityRead(t, bc, br, payload)
	qb := r.waitQuality(2)
	r.verifyQuality(qb, "socks5", qualityReplyDelay, "node:b")
	r.closeFlow(bc, br)
	r.waitFinal(qb.FlowID)
	qualityWrite(t, ac, payload[128:])
	qualityRead(t, ac, ar, payload[128:])
	r.closeFlow(ac, ar)
	r.waitFinal(qa.FlowID)
	dc, dr := r.connectTarget(echo, "direct.test")
	qualityWrite(t, dc, payload)
	qualityRead(t, dc, dr, payload)
	qd := r.waitQuality(3)
	r.verifyQuality(qd, "direct_tcp", 0, "node:direct")
	r.closeFlow(dc, dr)
	r.waitFinal(qd.FlowID)
	r.selectNode("node:nested")
	nc, nr := r.connectTarget(echo, "alpha.test")
	qualityWrite(t, nc, payload)
	r.releaseReply(b, 3)
	r.releaseReply(a, 3)
	qualityRead(t, nc, nr, payload)
	qn := r.waitQuality(4)
	r.verifyQuality(qn, "http_connect", 2*qualityReplyDelay, "node:b", "node:nested")
	r.closeFlow(nc, nr)
	r.waitFinal(qn.FlowID)
	core.stop(t, false)
	oldFiles := r.frozenFiles()
	oldA := append([]byte(nil), qa.Raw...)
	// A display rename in a second generation keeps all core internal IDs. The
	// old source epoch remains byte-for-byte immutable after actual new traffic.
	r.spec.Generation = "generation:quality-2"
	r.spec.Nodes[0].DisplayAlias = "A renamed"
	r.spec.Groups[0].DisplayAlias = "Main renamed"
	bundle, err := BuildQualityBundle(r.spec)
	if err != nil {
		t.Fatal(err)
	}
	r.routeMap = filepath.Join(r.dir, "route-map-2.json")
	transportPrivateWrite(t, r.routeMap, bundle.RouteMap, 0600)
	r.config = filepath.Join(r.runtime, "mihomo-2.json")
	transportPrivateWrite(t, r.config, bundle.Config, 0600)
	secondTruth, _ := json.MarshalIndent(map[string]any{"candidate_sha256": r.digest, "generation": r.spec.Generation, "bytes_each_direction": 257, "reply_delay_ms": 120, "duration_tolerance_lower_ms": 20, "duration_tolerance_upper_ms": 250, "input_bundle": json.RawMessage(bundle.Config), "mapping": json.RawMessage(bundle.RouteMap), "old_epoch": qa.SourceEpoch}, "", "  ")
	transportPrivateWrite(t, filepath.Join(r.dir, "preregistered-generation-2.json"), secondTruth, 0600)
	restarted := r.startQuality(echo, 1)
	r.selectNode("node:a")
	rc, rr := r.connectTarget(echo, "alpha.test")
	qualityWrite(t, rc, payload)
	r.releaseReply(a, 4)
	qualityRead(t, rc, rr, payload)
	q2 := r.waitQuality(5)
	r.verifyQuality(q2, "http_connect", qualityReplyDelay, "node:a")
	if q2.SourceEpoch == qa.SourceEpoch || q2.ConfigGeneration == qa.ConfigGeneration {
		t.Fatal("reopen reused epoch/generation")
	}
	r.closeFlow(rc, rr)
	r.waitFinal(q2.FlowID)
	restarted.stop(t, false)
	r.collector.stop(t)
	for name, raw := range oldFiles {
		after, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatal("old history changed across rename/reopen", err)
		}
	}
	if !bytes.Equal(oldA, qa.Raw) {
		t.Fatal("A historical quality mutated")
	}
	r.frozenFiles()
	// Reopen the actual durable receiver and replay all old bytes with the same
	// receipt IDs; parser/state validation above is also exercised by Accept.
	accepted := transportReceipts(r.receiver)
	if err := r.receiver.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := transportReceiver(t, filepath.Join(r.dir, "receiver"))
	for _, v := range accepted {
		seq, err := reopened.Accept(v.raw)
		if err != nil || seq != v.seq {
			t.Fatal("receiver reopen changed exact replay receipt", err)
		}
	}
	for _, p := range []*qualityProxy{a, b} {
		p.mu.Lock()
		targets := append([]string(nil), p.targets...)
		errs := append([]error(nil), p.errors...)
		p.mu.Unlock()
		if len(errs) != 0 {
			t.Fatal("controlled proxy service errors", errs)
		}
		t.Logf("actual endpoint protocol=%s targets=%v", p.protocol, targets)
	}
	b.mu.Lock()
	targets := append([]string(nil), b.targets...)
	b.mu.Unlock()
	nestedEndpoint := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(a.port())).String()
	found := false
	for _, target := range targets {
		if target == nestedEndpoint {
			found = true
		}
	}
	if !found {
		t.Fatal("nested dialer did not actually connect to HTTP endpoint through SOCKS")
	}
	r.assertPrivateTree()
	t.Logf("PASS T134 controlled success denominator=5: HTTPA,SOCKSB,DIRECT,nestedSOCKS+HTTP,renamedA; each257B; old A retained during switch; strict receiver/source replay; evidence=%s", r.dir)
}

// A stable complete prefix from the actual source is checked while replies
// remain blocked. Receiver lag therefore cannot hide prematurely emitted ready.
func (r *qualityRun) sourceSetupCount() (int, bool, error) {
	names, err := filepath.Glob(filepath.Join(r.trace, "epoch-*.source.json"))
	if err != nil || len(names) == 0 {
		return 0, false, err
	}
	count := 0
	for _, name := range names {
		meta, err := os.ReadFile(name)
		if err != nil {
			return 0, false, err
		}
		manifest, err := telemetrywire.DecodeManifest(meta)
		if err != nil {
			return 0, false, err
		}
		frameName := strings.TrimSuffix(name, ".source.json") + ".frames"
		raw, err := os.ReadFile(frameName)
		if err != nil {
			return 0, false, err
		}
		again, err := os.ReadFile(frameName)
		if err != nil {
			return 0, false, err
		}
		if !bytes.Equal(raw, again) {
			return 0, false, nil
		}
		var state telemetrywire.State
		for len(raw) > 0 {
			if len(raw) < 4 {
				return 0, false, nil
			}
			n := int(binary.BigEndian.Uint32(raw[:4]))
			if n == 0 || n > telemetrywire.SourceInputLimit {
				return 0, false, fmt.Errorf("invalid source frame size")
			}
			if len(raw) < 4+n+sha256.Size {
				return 0, false, nil
			}
			frame := raw[4 : 4+n]
			sum := sha256.Sum256(frame)
			if !bytes.Equal(sum[:], raw[4+n:4+n+sha256.Size]) {
				return 0, false, fmt.Errorf("source checksum mismatch")
			}
			e, err := telemetrywire.DecodeSourceEvent(frame)
			if err != nil {
				return 0, false, err
			}
			if telemetrywire.ValidateManifestEvent(manifest, e) != nil || state.Commit(e) != nil {
				return 0, false, fmt.Errorf("source lifecycle rejected")
			}
			if e.Kind == "setup_finished" {
				count++
			}
			raw = raw[4+n+sha256.Size:]
		}
	}
	return count, true, nil
}

func TestHoopaQualityActualRejectedThenRetry(t *testing.T) {
	if os.Getenv("HOOPA_QUALITY_NETWORK_TEST") != "1" {
		t.Skip("explicit pinned quality candidate/local-loopback opt-in required; not formal G03")
	}
	if _, _, err := qualityCandidate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ name, response, protocol, node string }{{"HTTP503", "http_503", "http_connect", "node:a"}, {"SOCKSBadREP", "socks_bad_rep", "socks5", "node:b"}, {"SOCKSTruncatedBND", "socks_truncated", "socks5", "node:b"}} {
		t.Run(scenario.name, func(t *testing.T) {
			echo, closeEcho := transportEcho(t)
			t.Cleanup(closeEcho)
			a := newQualityProxy(t, "http", echo, 0)
			b := newQualityProxy(t, "socks5", echo, a.port())
			p := a
			if scenario.protocol == "socks5" {
				p = b
			}
			p.mu.Lock()
			p.responsePlan = []string{scenario.response}
			p.mu.Unlock()
			r := newQualityRunScenario(t, a, b, echo, scenario.response)
			core := r.startQuality(echo, 0)
			r.selectNode(scenario.node)
			c, br := r.connectTarget(echo, "alpha.test")
			payload := transportPayload()
			qualityWrite(t, c, payload)
			r.releaseReply(p, 0)
			failed := r.waitQuality(1)
			q := failed.Quality
			if q == nil || q.Outcome != "failure" || q.ReadyNS != nil || q.DurationMS != nil || q.FlowDurationMS != nil || q.ActualConnectionRef != nil || failed.ActualConnectionRef != "" || q.FailureStage != "dial_or_handshake" {
				t.Fatalf("rejected attempt fabricated ready/actual or lost failure: %+v", q)
			}
			for _, e := range r.received() {
				if e.AttemptID == failed.AttemptID && e.Phase == "actual_connection" {
					t.Fatal("failed protocol attempt registered actual returned connection")
				}
			}
			r.releaseReply(p, 1)
			qualityRead(t, c, br, payload)
			success := r.waitQuality(2)
			r.verifyQuality(success, scenario.protocol, qualityReplyDelay, scenario.node)
			if failed.FlowID != success.FlowID || failed.SourceEpoch != success.SourceEpoch || failed.AttemptID == success.AttemptID {
				t.Fatal("retry did not preserve flow and use fresh attempt")
			}
			sq := success.Quality
			if sq.FlowStartNS != q.FlowStartNS || sq.AttemptStartNS < q.FinishNS || sq.AttemptStartNS <= q.AttemptStartNS || *sq.FlowDurationMS < *sq.DurationMS+q.ObservedWaitMS-1 {
				t.Fatal("flow/attempt timing lost failed setup wait")
			}
			r.closeFlow(c, br)
			r.waitFinal(success.FlowID)
			core.stop(t, false)
			r.collector.stop(t)
			r.frozenFiles()
			attempts, actual, final := 0, 0, 0
			for _, e := range r.received() {
				if e.Kind == "epoch_closed" {
					continue
				}
				if e.FlowID != success.FlowID {
					t.Fatal("health contaminated measured flow denominator")
				}
				switch e.Kind {
				case "setup_finished":
					attempts++
				case "closed":
					final++
					if len(e.Segments) != 1 || e.Segments[0].AttemptID != success.AttemptID || e.Segments[0].ActualConnectionRef != success.ActualConnectionRef {
						t.Fatal("final attributed bytes to rejected attempt")
					}
				}
				if e.Phase == "actual_connection" {
					actual++
					if e.AttemptID != success.AttemptID || e.ActualConnectionRef != success.ActualConnectionRef {
						t.Fatal("actual ref belongs to rejected attempt")
					}
				}
			}
			if attempts != 2 || actual != 1 || final != 1 {
				t.Fatalf("fixed oracle mismatch attempts=%d actual=%d final=%d", attempts, actual, final)
			}
			p.mu.Lock()
			requestCount := len(p.targets)
			remaining := len(p.responsePlan)
			errs := append([]error(nil), p.errors...)
			p.mu.Unlock()
			if requestCount != 2 || remaining != 0 || len(errs) != 0 {
				t.Fatal("controlled proxy response oracle mismatch", requestCount, remaining, errs)
			}
			r.assertPrivateTree()
			t.Logf("PASS T134 %s denominator=1flow/2attempts: first failure null ready/duration/ref; fresh successful attempt one actual segment257B_each_direction; evidence=%s", scenario.name, r.dir)
		})
	}
}
