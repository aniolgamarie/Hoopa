package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func probeBody(inner string) string {
	return `{"data":"` + base64.StdEncoding.EncodeToString([]byte(inner)) + `"}`
}
func TestObserverProbeRejectFirstAndExactNumbers(t *testing.T) {
	files := map[string][]byte{}
	p := newRecorderCollector("/private-token", true, func(name string, data []byte) error { files[name] = append([]byte(nil), data...); return nil })
	body := probeBody(`{"sid":"private-session","host":"secret.test","inputBytes":9007199254740993,"outputBytes":1,"duration":123}`)
	for i, want := range []int{503, 200} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/private-token", strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
		if !strings.Contains(w.Body.String(), map[int]string{503: `"ok":false`, 200: `"ok":true`}[want]) {
			t.Fatal(w.Body.String())
		}
	}
	s := p.summary()
	if s.Attempts != 2 || s.Received != 2 || s.Rejected != 1 || s.Decoded != 2 || s.DuplicateSID != 1 || s.ExactByteRecords != 2 || s.Status != Unverified {
		t.Fatalf("%+v", s)
	}
	if !bytes.Contains(files["observer-0001-decoded.json"], []byte("9007199254740993")) {
		t.Fatal("number precision lost")
	}
	raw, _ := json.Marshal(s)
	if bytes.Contains(raw, []byte("secret.test")) || bytes.Contains(raw, []byte("private-session")) || bytes.Contains(raw, []byte("private-token")) {
		t.Fatal("summary leaked raw values")
	}
}
func TestObserverProbePathMethodBodyAndBounds(t *testing.T) {
	p := newRecorderCollector("/private-token", true, func(string, []byte) error { return nil })
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/wrong", probeBody(`{}`), 404}, {"GET", "/private-token", "", 405},
		{"POST", "/private-token", "{", 400}, {"POST", "/private-token", `{"data":"!"}`, 400},
		{"POST", "/private-token", probeBody(`[]`), 400}, {"POST", "/private-token", probeBody(`{} {}`), 400},
		{"POST", "/private-token", strings.Repeat("x", recorderBodyLimit+1), 413},
	} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest(tc.method, "http://local"+tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("%s %s: %d want%d", tc.method, tc.path, w.Code, tc.code)
		}
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/private-token", strings.NewReader(probeBody(`{}`))))
	if w.Code != 503 {
		t.Fatal("invalid deliveries consumed reject-first")
	}
	for _, inner := range []string{`{"inputBytes":1.5,"outputBytes":1}`, `{"inputBytes":"1","outputBytes":1}`, `{"inputBytes":18446744073709551616,"outputBytes":1}`, `{"inputBytes":-1,"outputBytes":1}`} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/private-token", strings.NewReader(probeBody(inner))))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if p.summary().ExactByteRecords != 0 {
		t.Fatal("non uint64 byte facts accepted")
	}
	p.attempts = recorderRequestLimit
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://local/private-token", strings.NewReader(probeBody(`{}`))))
	if w.Code != 429 {
		t.Fatal("request bound missing")
	}
}
func TestObserverProbeWriteFailureAndTemplate(t *testing.T) {
	p := newRecorderCollector("/token", false, func(string, []byte) error { return errProtocol })
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/token", strings.NewReader(probeBody(`{}`))))
	if w.Code != 503 || p.summary().ReasonCode != "recorder_private_write_failed" {
		t.Fatal(w.Code, p.summary())
	}
	v := ConfigValues{RecorderURL: "http://127.0.0.1:1234/token"}
	_, gs, err := renderConfigs(v)
	if err != nil || strings.Count(string(gs), "record: recorder.service.handler") != 2 || !strings.Contains(string(gs), "addr: \""+v.RecorderURL+"\"") {
		t.Fatal(string(gs), err)
	}
	_, gs, err = renderConfigs(ConfigValues{})
	if err != nil || strings.Contains(string(gs), "flow-recorder") {
		t.Fatal("recorder must be opt in")
	}
}

func TestObserverProbePrivateFilesAndKeepRefusal(t *testing.T) {
	s, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.root.Close()
	p := newRecorderCollector("/token", false, func(name string, data []byte) error { return privateWrite(s, name, data) })
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/token", strings.NewReader(probeBody(`{"inputBytes":0,"outputBytes":18446744073709551615}`))))
	if w.Code != 200 || p.summary().ExactByteRecords != 1 {
		t.Fatal(w.Code, p.summary())
	}
	for _, name := range []string{"observer-0001-outer.json", "observer-0001-decoded.json"} {
		f, err := s.root.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := f.Stat()
		f.Close()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("raw recorder file is not private")
		}
	}
	done := make(chan struct{})
	close(done)
	if (&ownedCore{done: done}).verifyForKeep() {
		t.Fatal("exited core kept")
	}
	if (&ownedCore{done: make(chan struct{}), instance: CoreInstance{PID: -1}}).verifyForKeep() {
		t.Fatal("missing identity kept")
	}
	result := runHarness(context.Background(), Options{Mihomo: "/not-executed", Gost: "/not-executed", MihomoSHA: strings.Repeat("0", 64), GostSHA: strings.Repeat("0", 64), Timeout: time.Second, RecorderRejectFirst: true})
	if result.ReasonCode != "invalid_argument" || result.Directory != "" {
		t.Fatal("reject-first without probe must fail before creating a sandbox")
	}
}

func setupObserverJournal(t *testing.T, reject bool) (*recorderCollector, *Sandbox) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("unverified: recorder journal is Linux only")
	}
	s, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.root.Close() })
	p := newRecorderCollector("/private-token", reject, func(name string, data []byte) error { return privateWrite(s, name, data) })
	if err = p.enableJournal(s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p.journal != nil {
			p.journal.Close()
		}
	})
	return p, s
}
func bindTestObserverJournal(t *testing.T, p *recorderCollector, s *Sandbox) {
	t.Helper()
	core := CoreInstance{Role: "gost", PID: 123, OSStartIdentity: "456", BootIdentity: "synthetic-unit-boot", BinarySHA256: strings.Repeat("a", 64), RunID: s.RunID()}
	if err := p.bindJournalSource(s, core, []string{"run-http", "run-socks"}); err != nil {
		t.Fatal(err)
	}
	if p.sourceEpoch != recorderSourceEpoch(core) {
		t.Fatal("binding did not use persisted core identity")
	}
}
func observerPost(p *recorderCollector, inner string) int {
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "http://local/private-token", strings.NewReader(probeBody(inner))))
	return w.Code
}
func TestObserverJournalDurableBeforeAckAndReopenDedup(t *testing.T) {
	p, s := setupObserverJournal(t, true)
	bindTestObserverJournal(t, p, s)
	raw := ` {"sid":"sample-1","service":"run-http","inputBytes":9007199254740993,"outputBytes":1} `
	if code := observerPost(p, raw); code != 503 {
		t.Fatal("first durable ACK should be rejected", code)
	}
	if len(p.journal.Replay()) != 1 {
		t.Fatal("503 event was not persisted")
	}
	if code := observerPost(p, raw); code != 200 {
		t.Fatal(code)
	}
	summary := p.closeJournalForReplay()
	if summary.Status != Passed || summary.Unique != 1 || summary.Replayed != 1 || summary.Deduplicated != 1 || summary.AckRejected != 1 {
		t.Fatalf("%+v", summary)
	}
	if code := observerPost(p, raw); code != 503 {
		t.Fatal("closed collector accepted waiting handler", code)
	}
	encoded, _ := json.Marshal(summary)
	for _, secret := range []string{"sample-1", s.RunID(), "run-http", "9007199254740993"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("journal summary leaked private data")
		}
	}
}
func TestObserverJournalRejectsUnboundSourceInvalidSIDAndConflict(t *testing.T) {
	p, s := setupObserverJournal(t, false)
	raw := `{"sid":"sample-1","service":"run-http"}`
	if code := observerPost(p, raw); code != 503 || len(p.journal.Replay()) != 0 {
		t.Fatal("unbound source persisted", code)
	}
	bindTestObserverJournal(t, p, s)
	for _, bad := range []string{`{"sid":"bad/id","service":"run-http"}`, `{"sid":"sample-1","service":"another-service"}`, `{"service":"run-http"}`} {
		if code := observerPost(p, bad); code != 503 {
			t.Fatal("untrusted source accepted", code)
		}
	}
	if code := observerPost(p, raw); code != 200 {
		t.Fatal(code)
	}
	if code := observerPost(p, `{"sid":"sample-1","service":"run-http","changed":true}`); code != 503 {
		t.Fatal("content conflict accepted", code)
	}
	if code := observerPost(p, `{"sid":"new-sample","service":"run-http"}`); code != 503 {
		t.Fatal("poisoned callback continued", code)
	}
	if len(p.journal.Replay()) != 1 {
		t.Fatal("conflict changed journal")
	}
	summary := p.closeJournalForReplay()
	if summary.Status != Failed || summary.Conflicts != 1 || summary.Unbound != 1 || summary.RejectedSource != 3 || summary.Replayed != 1 {
		t.Fatalf("%+v", summary)
	}
}

type uncertainObserverJournal struct {
	observerJournal
	calls int
}

func (j *uncertainObserverJournal) Append(string, string, json.RawMessage) (uint64, error) {
	j.calls++
	return 0, errObserverJournalUncertain
}
func TestObserverJournalUncertainPoisonsAndMissingFlagRejected(t *testing.T) {
	p, s := setupObserverJournal(t, false)
	bindTestObserverJournal(t, p, s)
	injected := &uncertainObserverJournal{observerJournal: p.journal}
	p.journal = injected
	for i := 0; i < 2; i++ {
		if code := observerPost(p, `{"sid":"sample-1","service":"run-http"}`); code != 503 {
			t.Fatal(code)
		}
	}
	if injected.calls != 1 {
		t.Fatal("uncertain journal was blindly retried", injected.calls)
	}
	summary := p.closeJournalForReplay()
	if summary.Status != Failed || summary.ReasonCode != "recorder_journal_append_uncertain" {
		t.Fatalf("%+v", summary)
	}
	result := runHarness(context.Background(), Options{Mihomo: "/not-executed", Gost: "/not-executed", MihomoSHA: strings.Repeat("0", 64), GostSHA: strings.Repeat("0", 64), Timeout: time.Second, RecorderJournal: true})
	if result.ReasonCode != "invalid_argument" || result.Directory != "" {
		t.Fatal("journal without probe created sandbox")
	}
}

func TestObserverSourceEpochUsesPersistedCoreIdentity(t *testing.T) {
	core := CoreInstance{Role: "gost", BootIdentity: "boot-a", PID: 123, OSStartIdentity: "456", BinarySHA256: strings.Repeat("a", 64), RunID: "retained-core-start-nonce"}
	epoch := recorderSourceEpoch(core)
	if epoch != recorderSourceEpoch(core) || !strings.HasPrefix(epoch, "gost:") || len(epoch) != 69 {
		t.Fatal("unstable or invalid source epoch")
	}
	changes := map[string]func(*CoreInstance){
		"role":        func(c *CoreInstance) { c.Role = "other" },
		"boot":        func(c *CoreInstance) { c.BootIdentity = "boot-b" },
		"pid":         func(c *CoreInstance) { c.PID++ },
		"start":       func(c *CoreInstance) { c.OSStartIdentity = "457" },
		"binary":      func(c *CoreInstance) { c.BinarySHA256 = strings.Repeat("b", 64) },
		"start_nonce": func(c *CoreInstance) { c.RunID = "different-core-start-nonce" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := core
			change(&changed)
			if recorderSourceEpoch(changed) == epoch {
				t.Fatal("core generation identity change did not change epoch")
			}
		})
	}
	// Collector/manager lifetime is deliberately absent from the function inputs.
	// Moving a collector or its private files cannot change the retained identity.
	relocated := core
	relocated.PrivateWorkdir = "/different-collector-run"
	relocated.ConfigPath = "/different-collector-run/config.yaml"
	relocated.BinaryPath = "/same-binary-content-at-another-path"
	if recorderSourceEpoch(relocated) != epoch {
		t.Fatal("collector location entered the core epoch")
	}
}
