//go:build linux

// source5receiver is an opt-in experimental collector, not a production service.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	events "hoopa/experiments/mihomo-events"
	"hoopa/protocol/telemetrywire"
)

const fixtureSecret = "t128-collector-fixture-only"
const maxBody = 8192

type manifestsFlag []string

func (m *manifestsFlag) String() string     { return strings.Join(*m, ",") }
func (m *manifestsFlag) Set(s string) error { *m = append(*m, s); return nil }

type options struct {
	listen, journal, evidence, mode, release, secret string
	manifests                                        manifestsFlag
}

func parseOptions(args []string) (options, error) {
	var o options
	f := flag.NewFlagSet("source5receiver", flag.ContinueOnError)
	f.StringVar(&o.listen, "listen", "", "explicit IPv4 loopback address and port")
	f.StringVar(&o.journal, "journal", "", "existing private receiver journal directory")
	f.StringVar(&o.evidence, "evidence", "", "new private JSONL evidence path, outside source trace")
	f.Var(&o.manifests, "manifest", "fixed existing absolute source5 manifest path (repeat, at most 32)")
	f.StringVar(&o.mode, "mode", "normal", "normal|reject-first-closed|drop-first-closed|hold-opened")
	f.StringVar(&o.release, "release-file", "", "hold-opened release path; exact content release followed by LF")
	f.StringVar(&o.secret, "secret", fixtureSecret, "explicit fixture bearer secret (never read from environment)")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 {
		return o, errors.New("unexpected argument")
	}
	return o, validateOptions(o)
}
func cleanAbsolute(p string) bool { return filepath.IsAbs(p) && filepath.Clean(p) == p }
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
func validateOptions(o options) error {
	host, port, err := net.SplitHostPort(o.listen)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return errors.New("explicit IPv4 loopback listener required")
	}
	if !cleanAbsolute(o.journal) || !cleanAbsolute(o.evidence) || len(o.manifests) < 1 || len(o.manifests) > 32 {
		return errors.New("invalid private paths or manifest count")
	}
	if o.secret == "" || len(o.secret) > 256 || strings.ContainsAny(o.secret, "\r\n") {
		return errors.New("invalid explicit secret")
	}
	switch o.mode {
	case "normal", "reject-first-closed", "drop-first-closed":
		if o.release != "" {
			return errors.New("release file only valid for hold")
		}
	case "hold-opened":
		if !cleanAbsolute(o.release) {
			return errors.New("hold requires absolute release path")
		}
	default:
		return errors.New("invalid mode")
	}
	for _, m := range o.manifests {
		if !cleanAbsolute(m) || within(o.evidence, filepath.Dir(m)) {
			return errors.New("evidence must be outside source trace directories")
		}
	}
	return nil
}
func privateDir(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm() != 0700 {
		return errors.New("existing 0700 directory required")
	}
	return nil
}
func boundedFile(path string, limit int64) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid bounded regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errors.New("bounded read failed")
	}
	return raw, nil
}

type syncFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}
type evidenceLog struct {
	mu     sync.Mutex
	file   syncFile
	failed bool
}

func (l *evidenceLog) write(row record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failed {
		return errors.New("evidence unavailable")
	}
	raw, err := json.Marshal(row)
	if err == nil {
		raw = append(raw, '\n')
		var n int
		n, err = l.file.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if err == nil {
			err = l.file.Sync()
		}
	}
	if err != nil {
		l.failed = true
	}
	return err
}
func (l *evidenceLog) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.file.Sync()
	b := l.file.Close()
	return errors.Join(a, b)
}

type record struct {
	Schema        string           `json:"schema"`
	Event         string           `json:"event"`
	Mode          string           `json:"mode,omitempty"`
	Listen        string           `json:"listen,omitempty"`
	DeliveryIndex uint64           `json:"delivery_index,omitempty"`
	BodySHA256    string           `json:"body_sha256,omitempty"`
	BodyBytes     int              `json:"body_bytes,omitempty"`
	EventID       string           `json:"event_id,omitempty"`
	SourceEpoch   string           `json:"source_epoch,omitempty"`
	SourceSeq     uint64           `json:"source_seq,omitempty"`
	Kind          string           `json:"kind,omitempty"`
	ReceiptSeq    uint64           `json:"receipt_seq,omitempty"`
	Manifests     []manifestRecord `json:"manifests,omitempty"`
}
type manifestRecord struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Bytes       int    `json:"bytes"`
	SourceEpoch string `json:"source_epoch"`
}

func row(event string) record {
	return record{Schema: "hoopa-t128-source5-collector-evidence-v1", Event: event}
}

type receiver interface {
	Accept([]byte) (uint64, error)
	Close() error
}
type collector struct {
	receiver              receiver
	log                   *evidenceLog
	mode, release, secret string
	mu                    sync.Mutex
	delivery              uint64
	modeUsed              bool
	failed                bool
	stopping              bool
	pins                  []manifestRecord
	fatal                 chan struct{}
	closing               chan struct{}
	closeOnce             sync.Once
	handlers              sync.WaitGroup
}

func (c *collector) failLocked() {
	if !c.failed {
		c.failed = true
		close(c.fatal)
	}
}
func (c *collector) logLocked(r record) error {
	err := c.log.write(r)
	if err != nil {
		c.failLocked()
	}
	return err
}
func (c *collector) closingNow() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopping = true
	c.closeOnce.Do(func() { close(c.closing) })
}
func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	c.handlers.Add(1)
	c.mu.Unlock()
	defer c.handlers.Done()
	if r.Method != http.MethodPost || r.URL.Path != "/events" || r.URL.RawQuery != "" || len(r.Header.Values("Authorization")) != 1 || r.Header.Get("Authorization") != "Bearer "+c.secret {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	if len(raw) > maxBody {
		w.WriteHeader(413)
		return
	}
	event, err := telemetrywire.DecodeSourceEvent(raw)
	if err != nil || event.SchemaVersion != 5 {
		w.WriteHeader(400)
		return
	}
	h := sha256.Sum256(raw)
	c.mu.Lock()
	if c.failed {
		c.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	c.delivery++
	rec := row("accepted")
	rec.DeliveryIndex = c.delivery
	rec.BodySHA256 = hex.EncodeToString(h[:])
	rec.BodyBytes = len(raw)
	rec.EventID = event.EventID
	rec.SourceEpoch = event.SourceEpoch
	rec.SourceSeq = event.SourceSeq
	rec.Kind = event.Kind
	if c.mode == "reject-first-closed" && event.Kind == "closed" && !c.modeUsed {
		c.modeUsed = true
		rec.Event = "withheld_503"
		err = c.logLocked(rec)
		c.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	receipt, err := c.receiver.Accept(raw)
	if err != nil {
		rec.Event = "accept_rejected"
		_ = c.logLocked(rec)
		if errors.Is(err, events.ErrReceiverUncertain) || errors.Is(err, events.ErrReceiverUnavailable) || errors.Is(err, events.ErrReceiverClosed) {
			c.failLocked()
		}
		c.mu.Unlock()
		w.WriteHeader(409)
		return
	}
	rec.ReceiptSeq = receipt
	if err = c.logLocked(rec); err != nil {
		c.mu.Unlock()
		w.WriteHeader(503)
		return
	}
	drop := c.mode == "drop-first-closed" && event.Kind == "closed" && !c.modeUsed
	if drop {
		c.modeUsed = true
		rec.Event = "ack_dropped"
		err = c.logLocked(rec)
	}
	hold := c.mode == "hold-opened" && event.Kind == "opened"
	if hold && err == nil {
		rec.Event = "held"
		err = c.logLocked(rec)
	}
	c.mu.Unlock()
	if err != nil {
		w.WriteHeader(503)
		return
	}
	if drop {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			c.mu.Lock()
			c.failLocked()
			c.mu.Unlock()
			w.WriteHeader(503)
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			c.mu.Lock()
			c.failLocked()
			c.mu.Unlock()
			return
		}
		if conn.Close() != nil {
			c.mu.Lock()
			c.failLocked()
			c.mu.Unlock()
		}
		return
	}
	if hold {
		released, err := c.waitRelease(r.Context())
		c.mu.Lock()
		rec.Event = "ack_cancelled"
		if released {
			rec.Event = "ack_released"
		}
		logErr := c.logLocked(rec)
		if err != nil {
			c.failLocked()
		}
		c.mu.Unlock()
		if !released || err != nil || logErr != nil {
			w.WriteHeader(503)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
func (c *collector) waitRelease(ctx context.Context) (bool, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := boundedFile(c.release, 8)
		if err == nil {
			if string(raw) != "release\n" {
				return false, errors.New("invalid release content")
			}
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-c.closing:
			return false, nil
		case <-ticker.C:
		}
	}
}
func openCollector(o options) (*collector, error) {
	if err := validateOptions(o); err != nil {
		return nil, err
	}
	if err := privateDir(o.journal); err != nil {
		return nil, err
	}
	if err := privateDir(filepath.Dir(o.evidence)); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var manifests [][]byte
	var pins []manifestRecord
	for _, p := range o.manifests {
		raw, err := boundedFile(p, 8192)
		if err != nil {
			return nil, err
		}
		m, err := telemetrywire.DecodeManifest(raw)
		if err != nil || m.SchemaVersion != 2 || m.SourceSchemaVersion != 5 || seen[m.SourceEpoch] {
			return nil, errors.New("invalid or duplicate fixed source5 manifest")
		}
		seen[m.SourceEpoch] = true
		manifests = append(manifests, raw)
		pins = append(pins, manifestRecord{p, m.SHA256, len(raw), m.SourceEpoch})
	}
	// Resolve parents to prevent an alias from placing the log inside source trace.
	parent, err := filepath.EvalSymlinks(filepath.Dir(o.evidence))
	if err != nil {
		return nil, err
	}
	for _, p := range o.manifests {
		trace, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil || within(filepath.Join(parent, filepath.Base(o.evidence)), trace) {
			return nil, errors.New("evidence aliases source trace")
		}
	}
	f, err := os.OpenFile(o.evidence, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	log := &evidenceLog{file: f}
	r, err := events.OpenSourceReceiverWithManifests(o.journal, manifests)
	if err != nil {
		_ = log.close()
		return nil, err
	}
	c := &collector{receiver: r, log: log, mode: o.mode, release: o.release, secret: o.secret, fatal: make(chan struct{}), closing: make(chan struct{})}
	// Ready is written after the listener is bound, in run. Keep its immutable pins.
	c.pins = pins
	return c, nil
}
func run(ctx context.Context, o options) error {
	c, err := openCollector(o)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", o.listen)
	if err != nil {
		_ = c.receiver.Close()
		_ = c.log.close()
		return err
	}
	ready := row("ready")
	ready.Mode = o.mode
	ready.Listen = o.listen
	ready.Manifests = c.pins
	if err = c.log.write(ready); err != nil {
		_ = ln.Close()
		_ = c.receiver.Close()
		_ = c.log.close()
		return err
	}
	server := &http.Server{Handler: c, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ln) }()
	select {
	case <-ctx.Done():
	case <-c.fatal:
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	c.closingNow()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := server.Shutdown(shutdown)
	cancel()
	if closeErr != nil {
		_ = server.Close()
	}
	c.handlers.Wait()
	receiverErr := c.receiver.Close()
	c.mu.Lock()
	failed := c.failed || err != nil || closeErr != nil || receiverErr != nil
	stop := row("stop")
	if failed {
		stop.Event = "stop_failed"
	}
	logErr := c.logLocked(stop)
	c.mu.Unlock()
	finalErr := c.log.close()
	if failed {
		return errors.New("collector health failed")
	}
	return errors.Join(err, closeErr, receiverErr, logErr, finalErr)
}
func main() {
	o, err := parseOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err == nil {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = run(ctx, o)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, `{"schema":"hoopa-t128-source5-collector-health-v1","healthy":false,"error":"collector failed"}`)
		os.Exit(1)
	}
}
