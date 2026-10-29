package corecontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func fixtureSnapshot() Snapshot {
	hash := strings.Repeat("a", 64)
	snapshot := Snapshot{SchemaVersion: 1, ConfigSHA256: hash, Controller: "127.0.0.1:19000", Entries: []Entry{{"entry-a", "mixed", "127.0.0.1:19001", strings.Repeat("b", 64)}, {"entry-b", "mixed", "127.0.0.1:19002", strings.Repeat("c", 64)}}, Proxies: []Proxy{
		{"DIRECT", "Direct", "", "", []string{}, ""}, {"REJECT", "Reject", "", "", []string{}, ""}, {"REJECT-DROP", "RejectDrop", "", "", []string{}, ""}, {"COMPATIBLE", "Compatible", "", "", []string{}, ""}, {"PASS", "Pass", "", "", []string{}, ""},
		{"GLOBAL", "Selector", "", "", []string{"DIRECT", "REJECT", "node:direct", "node:http", "node:socks", "node:chained", "group:inner", "group:outer"}, "group:outer"},
		{"node:direct", "Direct", "", "", []string{}, ""}, {"node:http", "Http", "127.0.0.1:19100", "", []string{}, ""}, {"node:socks", "Socks5", "127.0.0.1:19101", "", []string{}, ""}, {"node:chained", "Http", "127.0.0.1:19102", "group:inner", []string{}, ""},
		{"group:inner", "Selector", "", "", []string{"node:http", "node:socks"}, "node:socks"}, {"group:outer", "Selector", "", "", []string{"group:inner", "node:chained"}, "node:chained"},
	}, Rules: []Rule{{"Domain", "alpha.test", "group:outer"}, {"DomainSuffix", "test", "node:http"}, {"InName", "entry-b", "node:socks"}, {"IPCIDR", "127.0.0.0/8", "node:direct"}, {"AND", "((InName,entry-a),(Domain,alpha.test))", "group:inner"}, {"Match", "", "REJECT"}}}
	sort.Slice(snapshot.Proxies, func(i, j int) bool { return snapshot.Proxies[i].Name < snapshot.Proxies[j].Name })
	return snapshot
}
func fixtureMutation() Mutation {
	return Mutation{1, "activate", "op:activate", "run:one", "gen:one", strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
}
func fixtureResult() Result {
	return Result{1, "op:activate", strings.Repeat("d", 64), "run:one", "opened"}
}
func fixtureStatus() Status {
	return Status{SchemaVersion: 1, RunID: "run:one", ConfigGeneration: "gen:one", ConfigSHA256: strings.Repeat("a", 64), BinaryVersion: "fixture-version", Mode: "basic", Capabilities: []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}, State: "closed", SnapshotSHA256: strings.Repeat("b", 64)}
}
func wire(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func invalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) || err.Error() != ErrInvalid.Error() {
		t.Fatal("expected constant redacted error:", err)
	}
}
func proxy(s *Snapshot, name string) *Proxy {
	for i := range s.Proxies {
		if s.Proxies[i].Name == name {
			return &s.Proxies[i]
		}
	}
	panic("unknown fixture proxy")
}

func TestCompleteSnapshotAndDigestEncoding(t *testing.T) {
	snapshot := fixtureSnapshot()
	raw := wire(t, snapshot)
	decoded, err := DecodeSnapshot(raw)
	if err != nil || !reflect.DeepEqual(decoded, snapshot) {
		t.Fatal("complete snapshot rejected:", err)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if digest != hex.EncodeToString(sum[:]) {
		t.Fatal("digest not of compact wire")
	}
	// Independent hard-coded field ordering for mutation establishes the exact
	// deterministic encoding used for request receipts (not map iteration order).
	mutation := fixtureMutation()
	golden := `{"schema_version":1,"operation":"activate","operation_id":"op:activate","run_id":"run:one","config_generation":"gen:one","config_sha256":"` + strings.Repeat("a", 64) + `","snapshot_sha256":"` + strings.Repeat("b", 64) + `","registration_sha256":"` + strings.Repeat("c", 64) + `"}`
	if string(wire(t, mutation)) != golden {
		t.Fatal("mutation wire order changed")
	}
	got, err := mutation.Digest()
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256([]byte(golden))
	if got != hex.EncodeToString(sum[:]) {
		t.Fatal("mutation digest order changed")
	}
	for name, change := range map[string]func(*Snapshot){
		"config": func(s *Snapshot) { s.ConfigSHA256 = strings.Repeat("d", 64) },
		"member-order": func(s *Snapshot) {
			p := proxy(s, "group:inner")
			p.Members[0], p.Members[1] = p.Members[1], p.Members[0]
		},
		"rule-order": func(s *Snapshot) { s.Rules[0], s.Rules[1] = s.Rules[1], s.Rules[0] },
		"selection":  func(s *Snapshot) { proxy(s, "group:inner").Selected = "node:http" },
	} {
		t.Run(name, func(t *testing.T) {
			s := fixtureSnapshot()
			change(&s)
			hash, err := s.Digest()
			if err != nil || hash == digest {
				t.Fatal("snapshot binding omitted from digest:", err)
			}
		})
	}
	mutation.RunID = "run:other"
	changed, err := mutation.Digest()
	if err != nil || changed == got {
		t.Fatal("run identity omitted from request digest")
	}
}

func TestStrictWireAmbiguitiesAcrossMessages(t *testing.T) {
	decoders := []struct {
		name   string
		raw    []byte
		decode func([]byte) error
	}{
		{"snapshot", wire(t, fixtureSnapshot()), func(b []byte) error { _, e := DecodeSnapshot(b); return e }},
		{"status", wire(t, fixtureStatus()), func(b []byte) error { _, e := DecodeStatus(b); return e }},
		{"mutation", wire(t, fixtureMutation()), func(b []byte) error { _, e := DecodeMutation(b); return e }},
		{"result", wire(t, fixtureResult()), func(b []byte) error { _, e := DecodeResult(b); return e }},
	}
	for _, d := range decoders {
		t.Run(d.name, func(t *testing.T) {
			for name, change := range map[string]func([]byte) []byte{
				"duplicate": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
				},
				"escaped-duplicate": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_\u0076ersion":1`), 1)
				},
				"unknown":   func(b []byte) []byte { return append([]byte(`{"secret-canary":1,`), b[1:]...) },
				"null-root": func(b []byte) []byte { return []byte(`null`) },
				"null-field": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":null`), 1)
				},
				"missing": func(b []byte) []byte { return bytes.Replace(b, []byte(`"schema_version":1,`), nil, 1) },
				"float-one": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1)
				},
				"exponent-one": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":1e0`), 1)
				},
				"string-one": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":"1"`), 1)
				},
				"version-two": func(b []byte) []byte {
					return bytes.Replace(b, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1)
				},
				"trailing":  func(b []byte) []byte { return append(append([]byte(nil), b...), []byte(` {}`)...) },
				"oversized": func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), MaxJSONBytes)...) },
			} {
				t.Run(name, func(t *testing.T) {
					err := d.decode(change(d.raw))
					invalid(t, err)
					if strings.Contains(err.Error(), "secret-canary") {
						t.Fatal("input leaked")
					}
				})
			}
			// JSON whitespace/key escapes do not change meaning. No canonical input
			// serialization requirement is invented; output and digest are canonical.
			escaped := bytes.Replace(d.raw, []byte(`"schema_version"`), []byte(`"schema_\u0076ersion"`), 1)
			if err := d.decode(append(append([]byte(" \n"), escaped...), []byte(" \t")...)); err != nil {
				t.Fatal("valid JSON alias rejected:", err)
			}
		})
	}
}

func TestUnicodeAndNestedRequiredFields(t *testing.T) {
	raw := wire(t, fixtureSnapshot())
	for name, data := range map[string][]byte{
		"lone-high":              bytes.Replace(raw, []byte("alpha.test"), []byte(`\ud800`), 1),
		"lone-low":               bytes.Replace(raw, []byte("alpha.test"), []byte(`\udfff`), 1),
		"high-with-non-low":      bytes.Replace(raw, []byte("alpha.test"), []byte(`\ud800\u0041`), 1),
		"malformed-UTF8":         bytes.Replace(raw, []byte("alpha.test"), []byte{0xff}, 1),
		"nested-duplicate":       bytes.Replace(raw, []byte(`"type":"mixed"`), []byte(`"type":"mixed","type":"mixed"`), 1),
		"nested-unknown":         bytes.Replace(raw, []byte(`"type":"mixed"`), []byte(`"type":"mixed","secret":1`), 1),
		"missing-empty-selected": bytes.Replace(raw, []byte(`,"selected":""`), nil, 1),
		"null-members":           bytes.Replace(raw, []byte(`"members":[]`), []byte(`"members":null`), 1),
		"null-member":            bytes.Replace(raw, []byte(`"members":["DIRECT"`), []byte(`"members":[null`), 1),
		"null-entry":             bytes.Replace(raw, []byte(`"entries":[`), []byte(`"entries":[null,`), 1),
	} {
		t.Run(name, func(t *testing.T) { _, err := DecodeSnapshot(data); invalid(t, err) })
	}
	for _, payload := range []string{`\ud83d\ude00`, `\\ud800`, `\ufffd`} {
		data := bytes.Replace(raw, []byte("alpha.test"), []byte(payload), 1)
		if _, err := DecodeSnapshot(data); err != nil {
			t.Fatal("valid Unicode payload rejected:", err)
		}
	}
	status := fixtureStatus()
	r := fixtureResult()
	status.Activation = &r
	raw = wire(t, status)
	for name, data := range map[string][]byte{
		"receipt-null":     bytes.Replace(raw, wire(t, r), []byte("null"), 1),
		"receipt-required": bytes.Replace(raw, []byte(`"request_sha256":"`+strings.Repeat("d", 64)+`",`), nil, 1),
		"receipt-unknown":  bytes.Replace(raw, []byte(`"result":"opened"`), []byte(`"result":"opened","extra":true`), 1),
	} {
		t.Run(name, func(t *testing.T) { _, err := DecodeStatus(data); invalid(t, err) })
	}
}

func TestSnapshotRejectsGraphAndProjectionErrors(t *testing.T) {
	for name, change := range map[string]func(*Snapshot){
		"missing-builtin":            func(s *Snapshot) { s.Proxies = s.Proxies[1:] },
		"wrong-builtin-type":         func(s *Snapshot) { proxy(s, "PASS").Type = "Direct" },
		"unknown-node-type":          func(s *Snapshot) { proxy(s, "node:http").Type = "Shadowsocks" },
		"unscoped-name":              func(s *Snapshot) { proxy(s, "node:http").Name = "http" },
		"duplicate-entry":            func(s *Snapshot) { s.Entries[1].Name = s.Entries[0].Name },
		"unsorted-entry":             func(s *Snapshot) { s.Entries[0], s.Entries[1] = s.Entries[1], s.Entries[0] },
		"duplicate-proxy":            func(s *Snapshot) { s.Proxies[1].Name = s.Proxies[0].Name },
		"unsorted-proxy":             func(s *Snapshot) { s.Proxies[0], s.Proxies[1] = s.Proxies[1], s.Proxies[0] },
		"controller-entry-collision": func(s *Snapshot) { s.Entries[0].Address = s.Controller },
		"duplicate-address":          func(s *Snapshot) { s.Entries[1].Address = s.Entries[0].Address },
		"IPv6":                       func(s *Snapshot) { s.Controller = "[::1]:19000" },
		"hostname":                   func(s *Snapshot) { proxy(s, "node:http").Address = "localhost:19100" },
		"noncanonical-port":          func(s *Snapshot) { s.Controller = "127.0.0.1:019000" },
		"outside-loopback":           func(s *Snapshot) { s.Entries[0].Address = "127.0.0.2:19001" },
		"zero-port":                  func(s *Snapshot) { s.Controller = "127.0.0.1:0" },
		"direct-address":             func(s *Snapshot) { proxy(s, "node:direct").Address = "127.0.0.1:19103" },
		"direct-dialer":              func(s *Snapshot) { proxy(s, "node:direct").DialerProxy = "node:http" },
		"direct-members":             func(s *Snapshot) { proxy(s, "DIRECT").Members = []string{"node:http"} },
		"group-address":              func(s *Snapshot) { proxy(s, "group:inner").Address = "127.0.0.1:19100" },
		"nil-array":                  func(s *Snapshot) { s.Rules = nil },
		"nil-members":                func(s *Snapshot) { proxy(s, "DIRECT").Members = nil },
		"duplicate-member":           func(s *Snapshot) { p := proxy(s, "group:inner"); p.Members = append(p.Members, p.Members[0]) },
		"unknown-member":             func(s *Snapshot) { proxy(s, "group:inner").Members[0] = "node:absent" },
		"builtin-group-member":       func(s *Snapshot) { proxy(s, "group:inner").Members[0] = "DIRECT" },
		"global-pass":                func(s *Snapshot) { proxy(s, "GLOBAL").Members[0] = "PASS" },
		"unknown-selection":          func(s *Snapshot) { proxy(s, "group:inner").Selected = "node:direct" },
		"unknown-dialer":             func(s *Snapshot) { proxy(s, "node:chained").DialerProxy = "node:absent" },
		"builtin-dialer":             func(s *Snapshot) { proxy(s, "node:chained").DialerProxy = "DIRECT" },
		"group-cycle": func(s *Snapshot) {
			proxy(s, "group:inner").Members = []string{"group:outer"}
			proxy(s, "group:inner").Selected = "group:outer"
		},
		"mixed-dialer-cycle": func(s *Snapshot) { proxy(s, "node:http").DialerProxy = "group:outer" },
		"unknown-rule":       func(s *Snapshot) { s.Rules[0].Type = "ProcessName" },
		"unknown-target":     func(s *Snapshot) { s.Rules[0].Target = "node:absent" },
		"builtin-target":     func(s *Snapshot) { s.Rules[0].Target = "DIRECT" },
		"unknown-entry-rule": func(s *Snapshot) { s.Rules[2].Payload = "entry-absent" },
		"empty-rule-payload": func(s *Snapshot) { s.Rules[0].Payload = "" },
		"duplicate-Match":    func(s *Snapshot) { s.Rules[0] = Rule{"Match", "", "REJECT"} },
		"nonempty-Match":     func(s *Snapshot) { s.Rules[len(s.Rules)-1].Payload = "anything" },
		"missing-Match":      func(s *Snapshot) { s.Rules = s.Rules[:len(s.Rules)-1] },
		"uppercase-hash":     func(s *Snapshot) { s.ConfigSHA256 = strings.Repeat("A", 64) },
		"non-ASCII-ID":       func(s *Snapshot) { s.Entries[0].Name = "入口" },
		"NUL-payload":        func(s *Snapshot) { s.Rules[0].Payload = "a\x00b" },
	} {
		t.Run(name, func(t *testing.T) {
			s := fixtureSnapshot()
			change(&s)
			invalid(t, s.Validate())
			if hash, err := s.Digest(); hash != "" || !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid snapshot got digest")
			}
			_, err := DecodeSnapshot(wire(t, s))
			invalid(t, err)
		})
	}
}

func TestStatusReceiptsAreHistoricalAndRunBound(t *testing.T) {
	activation := fixtureResult()
	drain := Result{1, "op:drain", strings.Repeat("e", 64), activation.RunID, "draining"}
	// This table enumerates protocol transitions, independently of Validate's
	// predicates. Historical opened survives drain, but closed cannot reappear.
	reachable := map[string]bool{
		"closed/none/none": true, "open/opened/none": true,
		"draining/none/draining": true, "draining/opened/draining": true,
		"degraded/failed/none": true, "degraded/opened/none": true,
		"degraded/opened/failed": true, "degraded/none/failed": true,
	}
	for _, state := range []string{"closed", "open", "draining", "degraded"} {
		for _, a := range []string{"none", "opened", "failed"} {
			for _, d := range []string{"none", "draining", "failed"} {
				name := state + "/" + a + "/" + d
				t.Run(name, func(t *testing.T) {
					status := fixtureStatus()
					status.State = state
					if a != "none" {
						r := activation
						r.Result = a
						status.Activation = &r
					}
					if d != "none" {
						r := drain
						r.Result = d
						status.Drain = &r
					}
					err := status.Validate()
					_, decodeErr := DecodeStatus(wire(t, status))
					if reachable[name] {
						if err != nil || decodeErr != nil {
							t.Fatal("reachable history rejected", err, decodeErr)
						}
					} else {
						invalid(t, err)
						invalid(t, decodeErr)
					}
				})
			}
		}
	}
	for name, change := range map[string]func(*Status){
		"unknown-state": func(s *Status) { s.State = "running" }, "wrong-mode": func(s *Status) { s.Mode = "identity" },
		"capability-order":        func(s *Status) { s.Capabilities[0], s.Capabilities[1] = s.Capabilities[1], s.Capabilities[0] },
		"capability-missing":      func(s *Status) { s.Capabilities = s.Capabilities[:2] },
		"capability-extra":        func(s *Status) { s.Capabilities = append(s.Capabilities, "extra") },
		"other-run":               func(s *Status) { r := activation; r.RunID = "run:other"; s.Activation = &r },
		"wrong-activation-result": func(s *Status) { r := drain; s.Activation = &r },
		"wrong-drain-result":      func(s *Status) { r := activation; s.Drain = &r },
		"reused-operation": func(s *Status) {
			r := drain
			r.OperationID = activation.OperationID
			s.Activation = &activation
			s.Drain = &r
		},
		"bad-receipt": func(s *Status) { r := activation; r.RequestSHA256 = "bad"; s.Activation = &r },
	} {
		t.Run(name, func(t *testing.T) { s := fixtureStatus(); change(&s); invalid(t, s.Validate()) })
	}
	status := fixtureStatus()
	raw := wire(t, status)
	if bytes.Contains(raw, []byte("activation")) || bytes.Contains(raw, []byte("drain")) {
		t.Fatal("absent receipt must be omitted")
	}
	activation.Result = "failed"
	drain.Result = "failed"
	status.Activation = &activation
	status.Drain = &drain
	invalid(t, status.Validate())
}

func TestFiniteSizeAndIdentityBounds(t *testing.T) {
	s := fixtureSnapshot()
	s.Rules[0].Payload = strings.Repeat("x", MaxPayloadBytes)
	s.Entries[0].Name = strings.Repeat("A", 128)
	if err := s.Validate(); err != nil {
		t.Fatal("exact text/ID bounds rejected:", err)
	}
	s.Rules[0].Payload += "x"
	invalid(t, s.Validate())
	status := fixtureStatus()
	status.BinaryVersion = strings.Repeat("é", 64)
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	status.BinaryVersion += "é"
	invalid(t, status.Validate())
	mutation := fixtureMutation()
	mutation.RunID = strings.Repeat("r", 128)
	if err := mutation.Validate(); err != nil {
		t.Fatal(err)
	}
	mutation.RunID += "r"
	invalid(t, mutation.Validate())
	for _, operation := range []string{"activate", "drain"} {
		m := fixtureMutation()
		m.Operation = operation
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, outcome := range []string{"opened", "draining", "failed"} {
		r := fixtureResult()
		r.Result = outcome
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	mutation = fixtureMutation()
	mutation.Operation = "stop"
	invalid(t, mutation.Validate())
	if hash, err := mutation.Digest(); hash != "" || err == nil {
		t.Fatal("invalid mutation digest")
	}
	r := fixtureResult()
	r.Result = "success"
	invalid(t, r.Validate())
	// Small individual payloads can still exceed the whole-message byte bound.
	s = fixtureSnapshot()
	s.Rules = nil
	for i := 0; i < 63; i++ {
		s.Rules = append(s.Rules, Rule{"Domain", strings.Repeat("x", 1024), "REJECT"})
	}
	s.Rules = append(s.Rules, Rule{"Match", "", "REJECT"})
	invalid(t, s.Validate())
	// A valid >8-level dependency chain is legal; only cycles are forbidden.
	s = fixtureSnapshot()
	last := "node:http"
	for i := 0; i < 12; i++ {
		name := "group:depth" + strings.Repeat("x", i)
		s.Proxies = append(s.Proxies, Proxy{name, "Selector", "", "", []string{last}, last})
		last = name
	}
	sort.Slice(s.Proxies, func(i, j int) bool { return s.Proxies[i].Name < s.Proxies[j].Name })
	if err := s.Validate(); err != nil {
		t.Fatal("invented depth cap:", err)
	}
}

func TestProjectionCollectionCapacity(t *testing.T) {
	s := fixtureSnapshot()
	s.Entries = append(s.Entries, Entry{"entry-c", "mixed", "127.0.0.1:19003", strings.Repeat("a", 64)}, Entry{"entry-d", "mixed", "127.0.0.1:19004", strings.Repeat("a", 64)})
	// Exactly 64 declared nodes plus six required builtins and 66 GLOBAL members.
	var builtins []Proxy
	for _, p := range s.Proxies {
		if !strings.Contains(p.Name, ":") {
			builtins = append(builtins, p)
		}
	}
	s.Proxies = builtins
	global := proxy(&s, "GLOBAL")
	global.Members = []string{"DIRECT", "REJECT"}
	global.Selected = "DIRECT"
	for i := 0; i < 64; i++ {
		name := "node:" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		s.Proxies = append(s.Proxies, Proxy{name, "Direct", "", "", []string{}, ""})
	}
	global = proxy(&s, "GLOBAL") // append may have moved the backing array
	for _, p := range s.Proxies {
		if strings.HasPrefix(p.Name, "node:") {
			global.Members = append(global.Members, p.Name)
		}
	}
	sort.Slice(s.Proxies, func(i, j int) bool { return s.Proxies[i].Name < s.Proxies[j].Name })
	s.Rules = nil
	for i := 0; i < 63; i++ {
		s.Rules = append(s.Rules, Rule{"Domain", "alpha.test", "REJECT"})
	}
	s.Rules = append(s.Rules, Rule{"Match", "", "REJECT"})
	if err := s.Validate(); err != nil {
		t.Fatal("exact finite collection capacity rejected:", err)
	}
	if _, err := DecodeSnapshot(wire(t, s)); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"entries", "proxies", "rules"} {
		t.Run(collection, func(t *testing.T) {
			raw := wire(t, s)
			var copy Snapshot
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			switch collection {
			case "entries":
				copy.Entries = append(copy.Entries, Entry{"entry-e", "mixed", "127.0.0.1:19005", strings.Repeat("a", 64)})
			case "proxies":
				copy.Proxies = append(copy.Proxies, Proxy{"node:extra", "Direct", "", "", []string{}, ""})
			case "rules":
				copy.Rules = append(copy.Rules[:63], Rule{"Domain", "beta.test", "REJECT"}, Rule{"Match", "", "REJECT"})
			}
			invalid(t, copy.Validate())
		})
	}
}
