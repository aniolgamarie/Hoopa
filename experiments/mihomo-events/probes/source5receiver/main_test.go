//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	events "hoopa/experiments/mihomo-events"
	"hoopa/experiments/telemetry"
)

func fixtures(t *testing.T) ([]byte, [][]byte) {
	t.Helper()
	m, err := os.ReadFile("testdata/source5-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/source5-first-flow.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	rows := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	if len(rows) != 6 {
		t.Fatal("original first flow changed")
	}
	return m, rows
}
func testCollector(t *testing.T, mode string) (*collector, options, [][]byte) {
	t.Helper()
	root := t.TempDir()
	journal := filepath.Join(root, "journal")
	trace := filepath.Join(root, "trace")
	out := filepath.Join(root, "out")
	for _, p := range []string{journal, trace, out} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m, frames := fixtures(t)
	mp := filepath.Join(trace, "source.json")
	if err := os.WriteFile(mp, m, 0600); err != nil {
		t.Fatal(err)
	}
	o := options{listen: "127.0.0.1:19001", journal: journal, evidence: filepath.Join(out, "evidence.jsonl"), mode: mode, secret: fixtureSecret, manifests: []string{mp}}
	if mode == "hold-opened" {
		o.release = filepath.Join(out, "release")
	}
	c, err := openCollector(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.closingNow(); c.handlers.Wait(); _ = c.receiver.Close(); _ = c.log.close() })
	return c, o, frames
}
func request(raw []byte) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:19001/events", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+fixtureSecret)
	return r
}
func post(c *collector, raw []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c.ServeHTTP(w, request(raw))
	return w
}
func records(t *testing.T, path string) []record {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []record
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var r record
		if json.Unmarshal(line, &r) != nil {
			t.Fatal("incomplete evidence record")
		}
		out = append(out, r)
	}
	return out
}
func feed(t *testing.T, c *collector, frames [][]byte) {
	t.Helper()
	for _, raw := range frames {
		if w := post(c, raw); w.Code != 204 {
			t.Fatalf("prefix rejected: %d", w.Code)
		}
	}
}
func TestCollectorAuthBoundStrictAndPrefix(t *testing.T) {
	c, o, frames := testCollector(t, "normal")
	cases := []struct {
		name   string
		r      *http.Request
		status int
	}{
		{"missing auth", httptest.NewRequest("POST", "/events", bytes.NewReader(frames[0])), 403},
		{"oversize", request(bytes.Repeat([]byte(" "), 8193)), 413},
		{"invalid", request([]byte(`{"schema_version":5}`)), 400},
		{"prefix", request(frames[5]), 409},
	}
	duplicate := request(frames[0])
	duplicate.Header.Add("Authorization", "Bearer "+fixtureSecret)
	cases = append(cases, struct {
		name   string
		r      *http.Request
		status int
	}{"duplicate authorization", duplicate, 403})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.ServeHTTP(w, tc.r)
			if w.Code != tc.status {
				t.Fatal(w.Code)
			}
		})
	}
	feed(t, c, frames)
	rows := records(t, o.evidence)
	if rows[0].Event != "accept_rejected" || rows[0].ReceiptSeq != 0 {
		t.Fatal(rows[0])
	}
	if len(rows) != 7 || rows[1].ReceiptSeq != 1 || rows[6].ReceiptSeq != 6 {
		t.Fatal(rows)
	}
	if w := post(c, frames[5]); w.Code != 204 {
		t.Fatal(w.Code)
	}
	rows = records(t, o.evidence)
	last := rows[len(rows)-1]
	if last.ReceiptSeq != 6 || last.DeliveryIndex != 8 || last.SourceSeq != 6 {
		t.Fatal(last)
	}
	if err := c.receiver.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err := telemetry.OpenJournal(o.journal)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	got := journal.Replay()
	if len(got) != 6 {
		t.Fatal(len(got))
	}
	for i, r := range got {
		if !bytes.Equal(r.Payload, frames[i]) || r.Seq != uint64(i+1) {
			t.Fatal("original payload changed")
		}
	}
}
func TestCollectorRejectFirstClosedBeforeAccept(t *testing.T) {
	c, o, f := testCollector(t, "reject-first-closed")
	feed(t, c, f[:5])
	before, err := os.ReadFile(filepath.Join(o.journal, "events.frames"))
	if err != nil {
		t.Fatal(err)
	}
	if w := post(c, f[5]); w.Code != 503 {
		t.Fatal(w.Code)
	}
	after, _ := os.ReadFile(filepath.Join(o.journal, "events.frames"))
	if !bytes.Equal(before, after) {
		t.Fatal("503 reached journal")
	}
	rows := records(t, o.evidence)
	last := rows[len(rows)-1]
	if last.Event != "withheld_503" || last.ReceiptSeq != 0 || last.DeliveryIndex != 6 {
		t.Fatal(last)
	}
	if w := post(c, f[5]); w.Code != 204 {
		t.Fatal(w.Code)
	}
	rows = records(t, o.evidence)
	last = rows[len(rows)-1]
	if last.ReceiptSeq != 6 || last.DeliveryIndex != 7 {
		t.Fatal(last)
	}
}

type fakeConn struct{ close func() error }

func (f *fakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (f *fakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (f *fakeConn) Close() error                     { return f.close() }
func (f *fakeConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (f *fakeConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (f *fakeConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error { return nil }

type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn     net.Conn
	hijacked bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return h.conn, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}
func TestCollectorDropDurableThenDuplicateSameReceipt(t *testing.T) {
	c, o, f := testCollector(t, "drop-first-closed")
	feed(t, c, f[:5])
	closed := false
	w := &hijackRecorder{ResponseRecorder: httptest.NewRecorder(), conn: &fakeConn{close: func() error {
		rows := records(t, o.evidence)
		last := rows[len(rows)-1]
		if last.Event != "ack_dropped" || last.ReceiptSeq != 6 {
			t.Error("drop before durable evidence")
		}
		seq, err := c.receiver.Accept(f[5])
		if err != nil || seq != 6 {
			t.Error("drop before durable accept", seq, err)
		}
		closed = true
		return nil
	}}}
	c.ServeHTTP(w, request(f[5]))
	if !closed || !w.hijacked || w.Body.Len() != 0 || w.Flushed {
		t.Fatal("did not close without HTTP response")
	}
	if r := post(c, f[5]); r.Code != 204 {
		t.Fatal(r.Code)
	}
	rows := records(t, o.evidence)
	if rows[len(rows)-1].ReceiptSeq != 6 || rows[len(rows)-1].DeliveryIndex != 7 {
		t.Fatal(rows)
	}
}
func waitRows(t *testing.T, c *collector, event string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		f := c.log.file.(*os.File)
		raw, _ := os.ReadFile(f.Name())
		c.mu.Unlock()
		n := 0
		for _, line := range bytes.Split(raw, []byte("\n")) {
			var r record
			if json.Unmarshal(line, &r) == nil && r.Event == event {
				n++
			}
		}
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bounded handler progress wait timed out")
}
func TestCollectorHoldEveryOpenedReleaseOutsideReceiverLock(t *testing.T) {
	c, o, f := testCollector(t, "hold-opened")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- post(c, f[0]).Code }()
	}
	waitRows(t, c, "held", 2)
	// This new delivery could not reach held if the first ACK wait held receiver or collector mutex.
	if seq, err := c.receiver.Accept(f[0]); err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
	if err := os.WriteFile(o.release, []byte("release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != 204 {
			t.Fatal(code)
		}
	}
	rows := records(t, o.evidence)
	released := 0
	for _, r := range rows {
		if r.Event == "ack_released" {
			released++
		}
		if r.ReceiptSeq != 1 {
			t.Fatal(r)
		}
	}
	if released != 2 {
		t.Fatal(released)
	}
}
func TestCollectorHoldCancellationAndShutdown(t *testing.T) {
	for _, how := range []string{"request", "shutdown", "bad_release"} {
		t.Run(how, func(t *testing.T) {
			c, o, f := testCollector(t, "hold-opened")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := request(f[0]).WithContext(ctx)
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { c.ServeHTTP(w, r); close(done) }()
			waitRows(t, c, "held", 1)
			switch how {
			case "request":
				cancel()
			case "shutdown":
				c.closingNow()
			case "bad_release":
				if err := os.WriteFile(o.release, []byte("wrong\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("hold blocked cleanup")
			}
			if w.Code == 204 {
				t.Fatal("cancelled hold falsely ACKed")
			}
			rows := records(t, o.evidence)
			if rows[len(rows)-1].Event != "ack_cancelled" {
				t.Fatal(rows)
			}
		})
	}
}

type failedFile struct {
	*os.File
	kind string
}

func (f *failedFile) Write(p []byte) (int, error) {
	if f.kind == "short" {
		return f.File.Write(p[:len(p)/2])
	}
	return f.File.Write(p)
}
func (f *failedFile) Sync() error {
	if f.kind == "sync" {
		return errors.New("injected sync failure")
	}
	return f.File.Sync()
}
func TestCollectorEvidenceFailureNeverAcknowledges(t *testing.T) {
	for _, kind := range []string{"short", "sync"} {
		t.Run(kind, func(t *testing.T) {
			c, o, f := testCollector(t, "normal")
			c.log.file = &failedFile{c.log.file.(*os.File), kind}
			if w := post(c, f[0]); w.Code != 503 {
				t.Fatal(w.Code)
			}
			select {
			case <-c.fatal:
			default:
				t.Fatal("health not failed")
			}
			if w := post(c, f[0]); w.Code != 503 {
				t.Fatal("poisoned helper ACKed")
			}
			if err := c.receiver.Close(); err != nil {
				t.Fatal(err)
			}
			m, _ := fixtures(t)
			r, err := events.OpenSourceReceiverWithManifests(o.journal, [][]byte{m})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if seq, err := r.Accept(f[0]); err != nil || seq != 1 {
				t.Fatal("durable delivery lost after evidence failure", seq, err)
			}
		})
	}
}
func TestCollectorOptionsAndStartupBoundaries(t *testing.T) {
	c, o, _ := testCollector(t, "normal")
	_ = c
	for _, address := range []string{"", ":19001", "localhost:19001", "127.0.0.2:19001", "[::1]:19001", "127.0.0.1:0", "127.0.0.1:019001"} {
		q := o
		q.listen = address
		if validateOptions(q) == nil {
			t.Fatal(address)
		}
	}
	q := o
	q.evidence = filepath.Join(filepath.Dir(q.manifests[0]), "log")
	if validateOptions(q) == nil {
		t.Fatal("trace log allowed")
	}
	q = o
	q.mode = "hold-opened"
	if validateOptions(q) == nil {
		t.Fatal("missing release allowed")
	}
	q = o
	q.manifests = append(q.manifests, q.manifests[0])
	q.evidence = filepath.Join(filepath.Dir(o.evidence), "duplicate")
	if _, err := openCollector(q); err == nil {
		t.Fatal("duplicate epoch allowed")
	}
	if _, err := openCollector(o); err == nil {
		t.Fatal("evidence overwritten")
	}
	if _, err := parseOptions([]string{"--listen", o.listen, "--journal", o.journal, "--manifest", o.manifests[0], "--evidence", filepath.Join(filepath.Dir(o.evidence), "new"), "extra"}); err == nil {
		t.Fatal("positional arg accepted")
	}
	q = o
	q.evidence = filepath.Join(filepath.Dir(o.evidence), "modebad")
	q.mode = "unknown"
	if _, err := openCollector(q); err == nil {
		t.Fatal("bad mode allowed")
	}
}

func TestCollectorDropUnsupportedWriterFailsClosed(t *testing.T) {
	c, _, f := testCollector(t, "drop-first-closed")
	feed(t, c, f[:5])
	if w := post(c, f[5]); w.Code != 503 {
		t.Fatal("unsupported drop transport ACKed")
	}
	select {
	case <-c.fatal:
	default:
		t.Fatal("missing unhealthy signal")
	}
}
func TestCollectorManifestAndPrivateFileLimits(t *testing.T) {
	_, o, _ := testCollector(t, "normal")
	q := o
	q.manifests = make([]string, 33)
	if validateOptions(q) == nil {
		t.Fatal("unbounded fixed set allowed")
	}
	big := filepath.Join(filepath.Dir(o.manifests[0]), "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte(" "), 8193), 0600); err != nil {
		t.Fatal(err)
	}
	q = o
	q.manifests = []string{big}
	q.evidence = filepath.Join(filepath.Dir(o.evidence), "unused")
	if _, err := openCollector(q); err == nil {
		t.Fatal("oversize manifest accepted")
	}
	if _, err := os.Stat(q.evidence); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid manifest created evidence")
	}
	alias := filepath.Join(filepath.Dir(o.manifests[0]), "alias")
	if err := os.Symlink(o.manifests[0], alias); err != nil {
		t.Fatal(err)
	}
	q.manifests = []string{alias}
	if _, err := openCollector(q); err == nil {
		t.Fatal("symlink manifest accepted")
	}
}
