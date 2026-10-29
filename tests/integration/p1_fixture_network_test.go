//go:build linux && amd64 && p1integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"hoopa/internal/testsupport/p1fixture"
)

// These are independent loopback upstreams/origin, never a simulated core or
// product adapter. The selected listener witnesses the node actually traversed.
// Test payload framing is deliberately separate from product byte accounting.
type p1FixtureNetwork struct {
	key, target, originals string
	entries                map[string]p1NetworkEntry
	mu                     sync.Mutex
	closing                bool
	listeners              []net.Listener
	connections            map[net.Conn]bool
	receipts               map[string]p1NodeReceipt
	errors                 []error
	wg                     sync.WaitGroup
}

type p1NetworkEntry struct{ address, authorization string }
type p1NodeReceipt struct {
	Bundle, Node, Target, SourceAddress, PayloadSHA256, EchoSHA256 string
	PayloadBytes, FramedBytes                                      int
	Original                                                       originalRef
}

func (p1NetworkEntry) String() string      { return "p1NetworkEntry{private auth}" }
func (p1NetworkEntry) GoString() string    { return "p1NetworkEntry{private auth}" }
func (*p1FixtureNetwork) String() string   { return "p1FixtureNetwork{private inputs}" }
func (*p1FixtureNetwork) GoString() string { return "p1FixtureNetwork{private inputs}" }

func p1Loopback(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

func newP1FixtureNetwork(f *p1fixture.Fixture, originals string) (*p1FixtureNetwork, error) {
	// The loader pins the complete raw bytes before these private fields decode.
	var cfg struct {
		Controller string `json:"external-controller"`
		Listeners  []struct {
			Name, Listen string
			Port         int
			Users        []struct{ Username, Password string }
		}
		Proxies []struct {
			Name, Type, Server string
			Port               int
		}
	}
	if json.Unmarshal(f.Material().Files["config.json"], &cfg) != nil || len(cfg.Listeners) != 2 || len(cfg.Proxies) != 2 {
		return nil, errors.New("fixed network input invalid")
	}
	originPort := map[string]int{"app-first": 23515, "domain-first": 15683}[f.Key()]
	if originPort == 0 {
		return nil, errors.New("fixed network key invalid")
	}
	n := &p1FixtureNetwork{key: f.Key(), target: net.JoinHostPort("alpha.test", strconv.Itoa(originPort)), originals: originals, entries: map[string]p1NetworkEntry{}, connections: map[net.Conn]bool{}, receipts: map[string]p1NodeReceipt{}}
	// Probing does not reserve ports through core launch. A subsequent contention
	// must still fail the actual Apply; it never causes pinned config rewriting.
	for _, address := range append([]string{cfg.Controller}, func() []string {
		var a []string
		for _, e := range cfg.Listeners {
			a = append(a, net.JoinHostPort(e.Listen, strconv.Itoa(e.Port)))
		}
		return a
	}()...) {
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			return nil, errors.New("fixed endpoint is not loopback")
		}
		l, err := net.Listen("tcp4", address)
		if err != nil {
			return nil, errors.New("fixed core port unavailable")
		}
		if err := l.Close(); err != nil {
			return nil, err
		}
	}
	for _, entry := range cfg.Listeners {
		if len(entry.Users) != 1 || entry.Listen != "127.0.0.1" {
			return nil, errors.New("fixed entry invalid")
		}
		user := entry.Users[0]
		n.entries[entry.Name] = p1NetworkEntry{p1Loopback(entry.Port), "Basic " + base64.StdEncoding.EncodeToString([]byte(user.Username+":"+user.Password))}
	}
	if err := n.listen(p1Loopback(originPort), func(c net.Conn) { _, _ = io.Copy(c, c) }); err != nil {
		return nil, err
	}
	for _, proxy := range cfg.Proxies {
		if proxy.Type != "http" || proxy.Server != "127.0.0.1" {
			n.Close()
			return nil, errors.New("fixed node invalid")
		}
		node := proxy.Name
		if err := n.listen(p1Loopback(proxy.Port), func(c net.Conn) { n.serveNode(c, node, p1Loopback(originPort)) }); err != nil {
			n.Close()
			return nil, err
		}
	}
	return n, nil
}

func (n *p1FixtureNetwork) listen(address string, serve func(net.Conn)) error {
	l, err := net.Listen("tcp4", address)
	if err != nil {
		return errors.New("fixed upstream port unavailable")
	}
	n.listeners = append(n.listeners, l)
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.mu.Lock()
			if n.closing || len(n.connections) >= 32 {
				n.mu.Unlock()
				c.Close()
				continue
			}
			n.connections[c] = true
			n.wg.Add(1)
			n.mu.Unlock()
			go func() {
				defer n.wg.Done()
				defer c.Close()
				defer func() { n.mu.Lock(); delete(n.connections, c); n.mu.Unlock() }()
				_ = c.SetDeadline(time.Now().Add(45 * time.Second))
				serve(c)
			}()
		}
	}()
	return nil
}

func (n *p1FixtureNetwork) recordError(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.closing && len(n.errors) < 32 {
		n.errors = append(n.errors, err)
	}
}

func (n *p1FixtureNetwork) serveNode(c net.Conn, node, origin string) {
	reader := bufio.NewReader(c)
	// Read a bounded header without an unbounded net/http parser allocation.
	var header bytes.Buffer
	for header.Len() <= 4096 {
		part, err := reader.ReadSlice('\n')
		line := string(part)
		if err != nil || len(line)+header.Len() > 4096 {
			n.recordError(errors.New("node CONNECT header invalid"))
			return
		}
		header.WriteString(line)
		if line == "\r\n" {
			break
		}
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(header.Bytes())))
	if err != nil || req.Method != "CONNECT" || req.Host != n.target {
		n.recordError(errors.New("unexpected node target"))
		return
	}
	upstream, err := net.DialTimeout("tcp4", origin, 2*time.Second)
	if err != nil {
		n.recordError(errors.New("origin unavailable"))
		return
	}
	defer upstream.Close()
	_ = upstream.SetDeadline(time.Now().Add(45 * time.Second))
	if _, err = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	for {
		var size [4]byte
		if _, err = io.ReadFull(reader, size[:]); err != nil {
			return
		}
		length := binary.BigEndian.Uint32(size[:])
		if length < 32 || length > 65536 {
			n.recordError(errors.New("node payload bound"))
			return
		}
		payload := make([]byte, int(length))
		if _, err = io.ReadFull(reader, payload); err != nil {
			return
		}
		frame := append(size[:], payload...)
		if _, err = io.Copy(upstream, bytes.NewReader(frame)); err != nil {
			n.recordError(errors.New("origin write failed"))
			return
		}
		echo := make([]byte, len(frame))
		if _, err = io.ReadFull(upstream, echo); err != nil || !bytes.Equal(echo, frame) {
			n.recordError(errors.New("origin echo mismatch"))
			return
		}
		receipt := p1NodeReceipt{Bundle: n.key, Node: node, Target: req.Host, SourceAddress: c.RemoteAddr().String(), PayloadSHA256: p1SHA(payload), EchoSHA256: p1SHA(echo[4:]), PayloadBytes: len(payload), FramedBytes: len(frame)}
		raw, _ := json.Marshal(receipt)
		ref, err := p1WriteOriginal(filepath.Join(n.originals, "node-"+receipt.PayloadSHA256+".json"), raw)
		if err != nil {
			n.recordError(errors.New("node evidence unavailable"))
			return
		}
		receipt.Original = ref
		n.mu.Lock()
		_, repeated := n.receipts[receipt.PayloadSHA256]
		n.receipts[receipt.PayloadSHA256] = receipt
		n.mu.Unlock()
		if repeated {
			n.recordError(errors.New("repeated payload witness"))
			return
		}
		if _, err = io.Copy(c, bytes.NewReader(echo)); err != nil {
			return
		}
	}
}

type p1HeldTunnel struct {
	conn    net.Conn
	reader  *bufio.Reader
	network *p1FixtureNetwork
}

func (n *p1FixtureNetwork) open(ctx context.Context, entry string) (*p1HeldTunnel, error) {
	e, ok := n.entries[entry]
	if !ok {
		return nil, errors.New("unknown entry")
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp4", e.address)
	if err != nil {
		return nil, errors.New("entry dial failed")
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", n.target, n.target, e.authorization)
	if _, err = io.WriteString(c, request); err != nil {
		c.Close()
		return nil, errors.New("entry CONNECT write failed")
	}
	r := bufio.NewReader(c)
	response, err := http.ReadResponse(r, nil)
	if err != nil || response.StatusCode != 200 {
		c.Close()
		return nil, errors.New("entry CONNECT refused")
	}
	return &p1HeldTunnel{c, r, n}, nil
}
func (h *p1HeldTunnel) exchange(ctx context.Context, count int) (p1NodeReceipt, error) {
	if count < 32 || count > 65536 {
		return p1NodeReceipt{}, errors.New("test payload bound")
	}
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = h.conn.SetDeadline(deadline)
	payload := make([]byte, count)
	if _, err := rand.Read(payload); err != nil {
		return p1NodeReceipt{}, err
	}
	frame := make([]byte, count+4)
	binary.BigEndian.PutUint32(frame[:4], uint32(count))
	copy(frame[4:], payload)
	if _, err := io.Copy(h.conn, bytes.NewReader(frame)); err != nil {
		return p1NodeReceipt{}, errors.New("tunnel write failed")
	}
	echo := make([]byte, len(frame))
	if _, err := io.ReadFull(h.reader, echo); err != nil || !bytes.Equal(echo, frame) {
		return p1NodeReceipt{}, errors.New("tunnel echo failed")
	}
	h.network.mu.Lock()
	receipt, ok := h.network.receipts[p1SHA(payload)]
	h.network.mu.Unlock()
	if !ok || receipt.PayloadBytes != count || receipt.EchoSHA256 != p1SHA(payload) {
		return p1NodeReceipt{}, errors.New("node witness missing")
	}
	return receipt, nil
}
func (h *p1HeldTunnel) Close() error { return h.conn.Close() }
func (n *p1FixtureNetwork) Close() error {
	n.mu.Lock()
	n.closing = true
	for c := range n.connections {
		c.Close()
	}
	n.mu.Unlock()
	for _, l := range n.listeners {
		l.Close()
	}
	n.wg.Wait()
	n.mu.Lock()
	defer n.mu.Unlock()
	return errors.Join(n.errors...)
}
