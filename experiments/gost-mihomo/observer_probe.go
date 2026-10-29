package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hoopa/internal/config"
)

const recorderBodyLimit = 1 << 20
const recorderRequestLimit = 256

type ObserverTypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}
type ObserverField struct {
	Name    string              `json:"name"`
	Present int                 `json:"present"`
	Types   []ObserverTypeCount `json:"types"`
}
type ObserverSummary struct {
	Status           EvidenceStatus          `json:"status"`
	ReasonCode       string                  `json:"reason_code"`
	Attempts         int                     `json:"attempts"`
	Received         int                     `json:"received"`
	Rejected         int                     `json:"rejected"`
	Decoded          int                     `json:"decoded"`
	Invalid          int                     `json:"invalid"`
	DuplicateSID     int                     `json:"duplicate_sid"`
	ExactByteRecords int                     `json:"exact_byte_records"`
	Fields           []ObserverField         `json:"fields"`
	Journal          *ObserverJournalSummary `json:"journal,omitempty"`
}

// Names are a fixed inspection checklist, never field names supplied by a peer.
// Presence of a numeric PID or duration alone does not prove source identity or
// forward_setup timing. Missing fields are reported, never synthesized.
var observerFields = []string{"sid", "service", "network", "remote", "local", "src", "dst", "host", "route", "time", "duration", "inputBytes", "outputBytes", "err", "pid", "processName", "processStartTime", "seq", "sourceEpoch", "forwardStart"}

type recorderCollector struct {
	mu                                                                             sync.Mutex
	path                                                                           string
	rejectFirst                                                                    bool
	write                                                                          func(string, []byte) error
	attempts, received, rejected, decoded, invalid, duplicateSID, exactByteRecords int
	sid                                                                            map[string]bool
	fields                                                                         map[string]map[string]int
	reason                                                                         string
	server                                                                         *http.Server
	done                                                                           chan struct{}
	closed                                                                         bool
	journal                                                                        observerJournal
	reopenJournal                                                                  func() (observerJournal, error)
	sourceEpoch                                                                    string
	sourceServices                                                                 map[string]bool
	journalPoison                                                                  bool
	journalSummary                                                                 ObserverJournalSummary
}

func newRecorderCollector(path string, reject bool, write func(string, []byte) error) *recorderCollector {
	return &recorderCollector{path: path, rejectFirst: reject, write: write, sid: map[string]bool{}, fields: map[string]map[string]int{}}
}
func startRecorderProbe(s *Sandbox, reject, journal bool) (*recorderCollector, string, error) {
	token, err := NewSecret()
	if err != nil {
		return nil, "", err
	}
	p := newRecorderCollector("/"+token, reject, func(name string, data []byte) error { return privateWrite(s, name, data) })
	if journal {
		if err := p.enableJournal(s); err != nil {
			return nil, "", err
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		if p.journal != nil {
			p.journal.Close()
		}
		return nil, "", err
	}
	p.done = make(chan struct{})
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	go func() {
		err := p.server.Serve(listener)
		if err != nil && err != http.ErrServerClosed {
			p.mu.Lock()
			p.reason = "recorder_server_failed"
			p.mu.Unlock()
		}
		close(p.done)
	}()
	return p, "http://" + listener.Addr().String() + p.path, nil
}
func recorderReply(w http.ResponseWriter, code int, ok bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if ok {
		io.WriteString(w, `{"ok":true}`)
	} else {
		io.WriteString(w, `{"ok":false}`)
	}
}
func decodeRecorderObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil || v == nil {
		return nil, errProtocol
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errProtocol
	}
	return v, nil
}
func recorderValueType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case json.Number:
		return "number"
	case string:
		return "string"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unsupported"
	}
}
func exactRecorderBytes(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	text := n.String()
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return false
	}
	for _, b := range []byte(text) {
		if b < '0' || b > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(text, 10, 64)
	return err == nil
}
func (p *recorderCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte(p.path)) != 1 {
		recorderReply(w, 404, false)
		return
	}
	if r.Method != http.MethodPost {
		recorderReply(w, 405, false)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.journalPoison {
		recorderReply(w, 503, false)
		return
	}
	if p.attempts >= recorderRequestLimit {
		p.reason = "recorder_request_limit"
		recorderReply(w, 429, false)
		return
	}
	p.attempts++
	if p.reason == "recorder_private_write_failed" {
		recorderReply(w, 503, false)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, recorderBodyLimit+1))
	if err != nil {
		p.invalid++
		recorderReply(w, 400, false)
		return
	}
	if len(raw) > recorderBodyLimit {
		p.invalid++
		recorderReply(w, 413, false)
		return
	}
	p.received++
	prefix := fmt.Sprintf("observer-%04d", p.attempts)
	if err := p.write(prefix+"-outer.json", raw); err != nil {
		p.reason = "recorder_private_write_failed"
		recorderReply(w, 503, false)
		return
	}
	outer, err := decodeRecorderObject(raw)
	encoded, ok := outer["data"].(string)
	if err != nil || !ok {
		p.invalid++
		recorderReply(w, 400, false)
		return
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		p.invalid++
		recorderReply(w, 400, false)
		return
	}
	if err := p.write(prefix+"-decoded.json", decoded); err != nil {
		p.reason = "recorder_private_write_failed"
		recorderReply(w, 503, false)
		return
	}
	object, err := decodeRecorderObject(decoded)
	if err != nil {
		p.invalid++
		recorderReply(w, 400, false)
		return
	}
	p.decoded++
	for _, name := range observerFields {
		if value, present := object[name]; present {
			if p.fields[name] == nil {
				p.fields[name] = map[string]int{}
			}
			p.fields[name][recorderValueType(value)]++
		}
	}
	if sid, ok := object["sid"].(string); ok && sid != "" {
		if p.sid[sid] {
			p.duplicateSID++
		}
		p.sid[sid] = true
	}
	if exactRecorderBytes(object["inputBytes"]) && exactRecorderBytes(object["outputBytes"]) {
		p.exactByteRecords++
	}
	if p.journal != nil {
		if !p.appendJournal(object, decoded) {
			recorderReply(w, 503, false)
			return
		}
		if p.rejectFirst && p.journalSummary.AckRejected == 0 {
			p.journalSummary.AckRejected++
			p.rejected++
			recorderReply(w, 503, false)
			return
		}
	} else if p.rejectFirst && p.decoded == 1 {
		p.rejected++
		recorderReply(w, 503, false)
		return
	}
	recorderReply(w, 200, true)
}
func (p *recorderCollector) summary() ObserverSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	reason := p.reason
	if reason == "" {
		reason = "recorder_observation_only"
		if p.decoded == 0 {
			reason = "recorder_no_decoded_delivery"
		} else if p.invalid != 0 {
			reason = "recorder_invalid_delivery"
		}
	}
	fields := []ObserverField{}
	for _, name := range observerFields {
		f := ObserverField{Name: name, Types: []ObserverTypeCount{}}
		types := []string{}
		for t := range p.fields[name] {
			types = append(types, t)
		}
		sort.Strings(types)
		for _, t := range types {
			n := p.fields[name][t]
			f.Present += n
			f.Types = append(f.Types, ObserverTypeCount{t, n})
		}
		fields = append(fields, f)
	}
	summary := ObserverSummary{Status: Unverified, ReasonCode: reason, Attempts: p.attempts, Received: p.received, Rejected: p.rejected, Decoded: p.decoded, Invalid: p.invalid, DuplicateSID: p.duplicateSID, ExactByteRecords: p.exactByteRecords, Fields: fields}
	if p.reopenJournal != nil {
		js := p.journalSummary
		summary.Journal = &js
		if js.Status != Passed {
			summary.ReasonCode = js.ReasonCode
		}
	}
	return summary
}

// The collector remains available for at most one second after calibration for
// late terminal deliveries. Close cuts off slow peers. Optional journal replay
// verifies delivered records separately; this drain never proves source completeness.
func (p *recorderCollector) finish(s *Sandbox) ObserverSummary {
	time.Sleep(time.Second)
	_ = p.server.Close()
	<-p.done
	if p.reopenJournal != nil {
		js := p.closeJournalForReplay()
		if err := privateJSON(s, "observer-journal-summary.json", js); err != nil {
			p.mu.Lock()
			p.journalSummary.Status = Failed
			p.journalSummary.ReasonCode = "recorder_journal_summary_write_failed"
			p.mu.Unlock()
		}
	} else {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
	}
	summary := p.summary()
	if err := privateJSON(s, "observer-summary.json", summary); err != nil {
		summary.ReasonCode = "recorder_summary_write_failed"
	}
	return summary
}

// This is a final, read-only ownership/liveness check, not production takeover.
func (p *ownedCore) verifyForKeep() bool {
	select {
	case <-p.done:
		return false
	default:
	}
	current, boot, err := startIdentity(p.instance.PID)
	if err != nil || current != p.instance.OSStartIdentity || boot != p.instance.BootIdentity || current == "" || boot == "" {
		return false
	}
	exe, err := binaryHash(fmt.Sprintf("/proc/%d/exe", p.instance.PID))
	if err != nil || exe != p.instance.BinarySHA256 {
		return false
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", p.instance.PID))
	if err != nil || cwd != p.instance.PrivateWorkdir {
		return false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p.instance.PID))
	if err != nil {
		return false
	}
	actual := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if len(actual) != len(p.cmd.Args) {
		return false
	}
	for i, arg := range actual {
		if arg != p.cmd.Args[i] {
			return false
		}
	}
	if filepath.Clean(actual[0]) != filepath.Clean(p.instance.BinaryPath) {
		return false
	}
	env, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", p.instance.PID))
	if err != nil {
		return false
	}
	found := false
	for _, v := range strings.Split(string(env), "\x00") {
		if v == "HOOPA_EXPERIMENT_RUN_ID="+p.instance.RunID {
			found = true
		}
	}
	if !found {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

var (
	errObserverJournalUnsupported = errors.New("recorder journal unsupported on this platform")
	errObserverJournalConflict    = errors.New("recorder journal event conflict")
	errObserverJournalInvalid     = errors.New("recorder journal invalid event")
	errObserverJournalUncertain   = errors.New("recorder journal append uncertain")
)

type observerJournalRecord struct {
	Seq       uint64
	Epoch, ID string
	Payload   json.RawMessage
}
type observerJournal interface {
	Append(epoch, id string, payload json.RawMessage) (uint64, error)
	Replay() []observerJournalRecord
	Close() error
}
type ObserverJournalSummary struct {
	Status         EvidenceStatus `json:"status"`
	ReasonCode     string         `json:"reason_code"`
	Unique         int            `json:"unique"`
	Replayed       int            `json:"replayed"`
	Deduplicated   int            `json:"deduplicated"`
	Conflicts      int            `json:"dedup_conflict"`
	Unbound        int            `json:"unbound"`
	RejectedSource int            `json:"rejected_source"`
	AckRejected    int            `json:"ack_rejected_after_persist"`
}

func (p *recorderCollector) enableJournal(s *Sandbox) error {
	if err := s.root.Mkdir("recorder-journal", 0700); err != nil {
		return err
	}
	dir := filepath.Join(s.Path(), "recorder-journal")
	j, err := openObserverJournal(dir)
	if err != nil {
		return err
	}
	p.journal = j
	p.reopenJournal = func() (observerJournal, error) { return openObserverJournal(dir) }
	p.journalSummary = ObserverJournalSummary{Status: Unverified, ReasonCode: "recorder_journal_source_unbound"}
	return nil
}

// recorderSourceEpoch derives solely from persisted core-generation identity.
// Core.RunID is the nonce retained at core start, never the current collector or
// manager run. Ownership checks are separate; this is not production takeover.
func recorderSourceEpoch(core CoreInstance) string {
	identity := struct {
		Role         string `json:"role"`
		Boot         string `json:"boot"`
		PID          int    `json:"pid"`
		Start        string `json:"start"`
		BinarySHA256 string `json:"binary_sha256"`
		StartNonce   string `json:"start_nonce"`
	}{core.Role, core.BootIdentity, core.PID, core.OSStartIdentity, core.BinarySHA256, core.RunID}
	// This fixed struct contains only strings and an int, so Marshal cannot fail.
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("gost:%x", digest)
}

// Source binding is installed once, before any harness data-entry request. The
// nonce URL and exact service names scope delivery to this owned GOST generation;
// neither binding nor a SID proves the original application identity.
func (p *recorderCollector) bindJournalSource(s *Sandbox, core CoreInstance, services []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.journal == nil {
		return nil
	}
	epoch := recorderSourceEpoch(core)
	_, err := config.ParseID(epoch)
	ticks, ticksErr := strconv.ParseUint(core.OSStartIdentity, 10, 64)
	if p.closed || p.sourceEpoch != "" || err != nil || ticksErr != nil || ticks == 0 || core.Role != "gost" || core.PID <= 0 || core.RunID != s.RunID() || core.BootIdentity == "" || !shaForm.MatchString(core.BinarySHA256) || len(services) != 2 {
		return errObserverJournalInvalid
	}
	allowed := map[string]bool{}
	for _, service := range services {
		if _, err := config.ParseID(service); err != nil || allowed[service] {
			return errObserverJournalInvalid
		}
		allowed[service] = true
	}
	metadata := struct {
		Epoch        string       `json:"source_epoch"`
		RecorderPath string       `json:"recorder_path"`
		Core         CoreInstance `json:"core"`
		Services     []string     `json:"services"`
	}{epoch, p.path, core, append([]string(nil), services...)}
	if err := privateJSON(s, "observer-journal-source.private.json", metadata); err != nil {
		return err
	}
	p.sourceEpoch = epoch
	p.sourceServices = allowed
	p.journalSummary.ReasonCode = "recorder_journal_collecting"
	return nil
}

// Called only while the collector mutex is held. ACK is gated by journal Sync;
// the legacy raw snapshots alone never qualify as durable event acknowledgement.
func (p *recorderCollector) appendJournal(object map[string]any, payload []byte) bool {
	if p.sourceEpoch == "" {
		p.journalSummary.Unbound++
		return false
	}
	sid, ok := object["sid"].(string)
	_, idErr := config.ParseID(sid)
	service, serviceOK := object["service"].(string)
	if !ok || idErr != nil || !serviceOK || !p.sourceServices[service] {
		p.journalSummary.RejectedSource++
		return false
	}
	seq, err := p.journal.Append(p.sourceEpoch, sid, json.RawMessage(payload))
	if err != nil {
		if errors.Is(err, errObserverJournalInvalid) {
			p.journalSummary.RejectedSource++
			return false
		}
		p.journalPoison = true
		p.journalSummary.Status = Failed
		p.journalSummary.ReasonCode = "recorder_journal_append_uncertain"
		if errors.Is(err, errObserverJournalConflict) {
			p.journalSummary.Conflicts++
			p.journalSummary.ReasonCode = "recorder_journal_event_conflict"
		}
		return false
	}
	if seq <= uint64(p.journalSummary.Unique) {
		p.journalSummary.Deduplicated++
	} else {
		p.journalSummary.Unique++
	}
	return true
}

// HTTP must be closed before this call in real runs. The mutex and closed guard
// also reject handlers already waiting for the collector, before closing storage.
func (p *recorderCollector) closeJournalForReplay() ObserverJournalSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.journal == nil {
		return p.journalSummary
	}
	expected := p.journal.Replay()
	fail := func(reason string) ObserverJournalSummary {
		p.journalSummary.Status = Failed
		p.journalSummary.ReasonCode = reason
		return p.journalSummary
	}
	err := p.journal.Close()
	p.journal = nil
	if err != nil {
		return fail("recorder_journal_close_failed")
	}
	reopened, err := p.reopenJournal()
	if err != nil {
		return fail("recorder_journal_reopen_failed")
	}
	actual := reopened.Replay()
	p.journalSummary.Replayed = len(actual)
	same := len(actual) == len(expected) && len(expected) == p.journalSummary.Unique
	if same {
		for i, r := range actual {
			e := expected[i]
			if r.Seq != e.Seq || r.Epoch != e.Epoch || r.ID != e.ID || !bytes.Equal(r.Payload, e.Payload) {
				same = false
				break
			}
		}
	}
	if err := reopened.Close(); err != nil {
		return fail("recorder_journal_reopen_close_failed")
	}
	if !same {
		return fail("recorder_journal_replay_mismatch")
	}
	if p.journalPoison {
		return p.journalSummary
	}
	if p.journalSummary.Unbound > 0 || p.journalSummary.RejectedSource > 0 {
		return fail("recorder_journal_source_rejected")
	}
	if p.journalSummary.Unique == 0 {
		p.journalSummary.Status = Unverified
		p.journalSummary.ReasonCode = "recorder_journal_no_delivered_events"
		return p.journalSummary
	}
	p.journalSummary.Status = Passed
	p.journalSummary.ReasonCode = "recorder_journal_delivered_replay_verified"
	return p.journalSummary
}
