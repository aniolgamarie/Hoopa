package sourcecontrol

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func fixtureAutoSnapshot() Snapshot {
	s := Snapshot{SchemaVersion: 2, ConfigSHA256: strings.Repeat("a", 64), Controller: "127.0.0.1:19000", Entries: []Entry{{Name: "entry-a", Type: "mixed", Address: "127.0.0.1:19001", AuthSHA256: strings.Repeat("b", 64)}}, Rules: []Rule{{Type: "Match", Target: "group:auto"}}}
	for _, builtin := range [][2]string{{"COMPATIBLE", "Compatible"}, {"DIRECT", "Direct"}, {"GLOBAL", "Selector"}, {"PASS", "Pass"}, {"REJECT", "Reject"}, {"REJECT-DROP", "RejectDrop"}} {
		p := Proxy{Name: builtin[0], Type: builtin[1], Members: []string{}}
		if p.Name == "GLOBAL" {
			p.Members = []string{"DIRECT", "REJECT", "group:auto", "node:remote"}
			p.Selected = "group:auto"
		}
		s.Proxies = append(s.Proxies, p)
	}
	s.Proxies = append(s.Proxies, Proxy{Name: "group:auto", Type: "URLTest", Members: []string{"node:remote"}, Probe: &Probe{URL: "https://probe.example.test/check?a=1&b=2", IntervalSeconds: 60, TimeoutMS: 1000, ToleranceMS: 0}}, Proxy{Name: "node:remote", Type: "Trojan", Address: "edge.example.test:443", Members: []string{}})
	return s
}
func TestRemoteLeafTypesAndCanonicalEndpoints(t *testing.T) {
	for _, kind := range []string{"Http", "Socks5", "Shadowsocks", "Trojan", "Vmess", "Vless", "Hysteria2"} {
		t.Run(kind, func(t *testing.T) {
			for _, address := range []string{"edge.example.test:443", "localhost:1", "192.0.2.1:65535", "[2001:db8::1]:1080"} {
				s := fixtureAutoSnapshot()
				proxy(&s, "node:remote").Type = kind
				proxy(&s, "node:remote").Address = address
				decoded, err := DecodeSnapshot(wire(t, s))
				if err != nil || !reflect.DeepEqual(decoded, s) {
					t.Fatal("canonical remote endpoint rejected", err)
				}
			}
		})
	}
	s := fixtureSnapshot()
	if _, err := DecodeSnapshot(wire(t, s)); err != nil {
		t.Fatal("named Direct projection rejected", err)
	}
	for _, address := range []string{"", "edge.example.test", "EDGE.EXAMPLE.TEST:443", "edge.example.test.:443", "edge.example.test:0443", "edge.example.test:0", "edge.example.test:65536", "[2001:0db8::1]:443", "[fe80::1%eth0]:443", "https://edge.example.test:443", "user@edge.example.test:443", "edge.example.test/path:443", "bad_host:443", "-bad.host:443", "bad..host:443", "999.999.999.999:443", "éxample.test:443", "[127.0.0.1]:443", "2001:db8::1:443"} {
		s := fixtureAutoSnapshot()
		proxy(&s, "node:remote").Address = address
		invalid(t, s.Validate())
		_, err := DecodeSnapshot(wire(t, s))
		invalid(t, err)
	}
}
func TestStaticURLTestAndSingleParentGroups(t *testing.T) {
	s := fixtureAutoSnapshot()
	s.Proxies = append(s.Proxies, Proxy{Name: "group:follow", Type: "Selector", Members: []string{"group:auto"}, Selected: "group:auto"}, Proxy{Name: "group:reject", Type: "Selector", Members: []string{"REJECT"}, Selected: "REJECT"})
	sort.Slice(s.Proxies, func(i, j int) bool { return s.Proxies[i].Name < s.Proxies[j].Name })
	if _, err := DecodeSnapshot(wire(t, s)); err != nil {
		t.Fatal("one-parent follower or explicit reject group rejected", err)
	}
	mutations := map[string]func(*Snapshot){
		"dynamic-selected": func(s *Snapshot) { proxy(s, "group:auto").Selected = "node:remote" },
		"missing-probe":    func(s *Snapshot) { proxy(s, "group:auto").Probe = nil },
		"leaf-probe":       func(s *Snapshot) { proxy(s, "node:remote").Probe = proxy(s, "group:auto").Probe },
		"selector-probe":   func(s *Snapshot) { proxy(s, "GLOBAL").Probe = proxy(s, "group:auto").Probe },
		"builtin-probe":    func(s *Snapshot) { proxy(s, "DIRECT").Probe = proxy(s, "group:auto").Probe },
		"direct-member":    func(s *Snapshot) { proxy(s, "node:remote").Type = "Direct"; proxy(s, "node:remote").Address = "" },
		"builtin-member":   func(s *Snapshot) { proxy(s, "group:auto").Members = []string{"REJECT"} },
		"group-member":     func(s *Snapshot) { proxy(s, "group:auto").Members = []string{"GLOBAL"} },
		"missing-member":   func(s *Snapshot) { proxy(s, "group:auto").Members = []string{"node:missing"} },
		"duplicate-member": func(s *Snapshot) { proxy(s, "group:auto").Members = []string{"node:remote", "node:remote"} },
		"empty-members":    func(s *Snapshot) { proxy(s, "group:auto").Members = []string{} },
		"nil-members":      func(s *Snapshot) { proxy(s, "group:auto").Members = nil },
		"node-members":     func(s *Snapshot) { proxy(s, "node:remote").Members = []string{"node:remote"} },
		"node-selected":    func(s *Snapshot) { proxy(s, "node:remote").Selected = "node:remote" },
		"node-dialer":      func(s *Snapshot) { proxy(s, "node:remote").DialerProxy = "group:auto" },
		"auto-address":     func(s *Snapshot) { proxy(s, "group:auto").Address = "edge.example.test:443" },
		"auto-dialer":      func(s *Snapshot) { proxy(s, "group:auto").DialerProxy = "node:remote" },
		"node-type-group": func(s *Snapshot) {
			proxy(s, "node:remote").Type = "Selector"
			proxy(s, "node:remote").Address = ""
			proxy(s, "node:remote").Members = []string{"REJECT"}
			proxy(s, "node:remote").Selected = "REJECT"
		},
		"group-type-node": func(s *Snapshot) {
			proxy(s, "group:auto").Type = "Http"
			proxy(s, "group:auto").Address = "edge.example.test:443"
			proxy(s, "group:auto").Members = []string{}
			proxy(s, "group:auto").Probe = nil
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s := fixtureAutoSnapshot()
			mutate(&s)
			invalid(t, s.Validate())
			_, err := DecodeSnapshot(wire(t, s))
			invalid(t, err)
		})
	}
	s = fixtureSnapshot()
	p := proxy(&s, "group:inner")
	p.Members = []string{"node:http", "node:socks"}
	p.Selected = "node:socks"
	invalid(t, s.Validate())
	s = fixtureAutoSnapshot()
	s.Proxies = append(s.Proxies, Proxy{Name: "group:other", Type: "Selector", Members: []string{"node:remote"}, Selected: "node:remote"})
	sort.Slice(s.Proxies, func(i, j int) bool { return s.Proxies[i].Name < s.Proxies[j].Name })
	proxy(&s, "group:auto").Members = []string{"group:other"}
	invalid(t, s.Validate())
	for _, target := range []string{"GLOBAL", "COMPATIBLE", "PASS", "DIRECT", "REJECT-DROP"} {
		s := fixtureAutoSnapshot()
		s.Rules[0].Target = target
		invalid(t, s.Validate())
	}
}
func TestProbeProfileAndStrictLiteralIntegers(t *testing.T) {
	for _, p := range []Probe{{"https://probe.example.test/", 1, 1, 0}, {"http://localhost:1/check?x=y", 86400, 60000, 65534}, {"https://[2001:db8::1]:65535/", 60, 1000, 0}} {
		if err := p.Validate(); err != nil {
			t.Fatal("valid probe rejected", err)
		}
		s := fixtureAutoSnapshot()
		proxy(&s, "group:auto").Probe = &p
		if _, err := DecodeSnapshot(wire(t, s)); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*Probe){func(p *Probe) { p.IntervalSeconds = 0 }, func(p *Probe) { p.IntervalSeconds = 86401 }, func(p *Probe) { p.TimeoutMS = 0 }, func(p *Probe) { p.TimeoutMS = 60001 }, func(p *Probe) { p.ToleranceMS = 65535 }, func(p *Probe) { p.IntervalSeconds = math.MaxUint64 }} {
		s := fixtureAutoSnapshot()
		mutate(proxy(&s, "group:auto").Probe)
		invalid(t, s.Validate())
	}
	for _, url := range []string{"/relative", "ftp://probe.example.test/", "https:///check", "https://user:secret@probe.example.test/", "https://probe.example.test/#", "https://probe.example.test/#fragment", "https://probe.example.test:0/", "https://probe.example.test:65536/", "https://probe.example.test:/", "https://[probe.example.test]/", "https://[127.0.0.1]/", "https://[fe80::1%25eth0]/", "https://bad_host/", "https://probe.example.test/\x7f"} {
		s := fixtureAutoSnapshot()
		proxy(&s, "group:auto").Probe.URL = url
		invalid(t, s.Validate())
		_, err := DecodeSnapshot(wire(t, s))
		invalid(t, err)
	}
	for _, url := range []string{"https://probe.example.test/a b", "https://probe.example.test/?a=two words", "https://probe.example.test/\u00a0", "https://probe.example.test/a\\b"} {
		s := fixtureAutoSnapshot()
		proxy(&s, "group:auto").Probe.URL = url
		invalid(t, s.Validate())
		_, err := DecodeSnapshot(wire(t, s))
		invalid(t, err)
	}
	badUTF8 := fixtureAutoSnapshot()
	proxy(&badUTF8, "group:auto").Probe.URL = "https://probe.example.test/" + string([]byte{0xff})
	invalid(t, badUTF8.Validate())
	raw := wire(t, fixtureAutoSnapshot())
	malformedUTF8 := bytes.Replace(raw, []byte("probe.example.test"), []byte{0xff}, 1)
	_, unicodeErr := DecodeSnapshot(malformedUTF8)
	invalid(t, unicodeErr)
	for key, original := range map[string]string{"interval_seconds": "60", "timeout_ms": "1000", "tolerance_ms": "0"} {
		for _, literal := range []string{"1.0", "1e0", "-0", "-1", "\"1\"", "null", "true", "9007199254740992", "18446744073709551616", "01"} {
			changed := bytes.Replace(raw, []byte(`"`+key+`":`+original), []byte(`"`+key+`":`+literal), 1)
			_, err := DecodeSnapshot(changed)
			invalid(t, err)
		}
	}
	// All four fields are required, even zero tolerance. Unknown live state and
	// alternative probe behavior cannot be smuggled into immutable intent.
	for _, key := range []string{"url", "interval_seconds", "timeout_ms", "tolerance_ms"} {
		s := fixtureAutoSnapshot()
		var object map[string]any
		if json.Unmarshal(wire(t, s), &object) != nil {
			t.Fatal("fixture")
		}
		for _, value := range object["proxies"].([]any) {
			p := value.(map[string]any)
			if p["name"] == "group:auto" {
				delete(p["probe"].(map[string]any), key)
			}
		}
		_, err := DecodeSnapshot(wire(t, object))
		invalid(t, err)
	}
	for _, extra := range []string{`"delay":0`, `"best":"node:remote"`, `"success_status":"200-299"`, `"url":"https://other.test/"`, `"u\u0072l":"https://other.test/"`} {
		changed := bytes.Replace(raw, []byte(`"probe":{`), []byte(`"probe":{`+extra+`,`), 1)
		_, err := DecodeSnapshot(changed)
		invalid(t, err)
	}
	for _, replacement := range []string{"null", "[]", "false"} {
		p := proxyValue(fixtureAutoSnapshot(), "group:auto").Probe
		changed := bytes.Replace(raw, wire(t, p), []byte(replacement), 1)
		_, err := DecodeSnapshot(changed)
		invalid(t, err)
	}
}
func proxyValue(s Snapshot, name string) Proxy {
	for _, p := range s.Proxies {
		if p.Name == name {
			return p
		}
	}
	panic("fixture proxy")
}
func TestV2VersionsNestedReceiptsAndZeroFailureResults(t *testing.T) {
	status := fixtureStatus()
	status.State = "draining"
	activation := fixtureResult()
	drain := Result{2, "op:drain", strings.Repeat("e", 64), activation.RunID, "draining"}
	status.Activation = &activation
	status.Drain = &drain
	if _, err := DecodeStatus(wire(t, status)); err != nil {
		t.Fatal("valid v2 nested receipts rejected", err)
	}
	for _, which := range []string{"activation", "drain"} {
		s := status
		a, d := activation, drain
		s.Activation = &a
		s.Drain = &d
		if which == "activation" {
			s.Activation.SchemaVersion = 1
		} else {
			s.Drain.SchemaVersion = 1
		}
		invalid(t, s.Validate())
		_, err := DecodeStatus(wire(t, s))
		invalid(t, err)
	}
	for _, mode := range []string{"basic", "identity", "basic-sources-v2"} {
		s := fixtureStatus()
		s.Mode = mode
		invalid(t, s.Validate())
	}
	s := fixtureStatus()
	s.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
	invalid(t, s.Validate())
	// Explicit v1 receipt objects fail every public entry point in the new codec.
	snapshot := fixtureAutoSnapshot()
	snapshot.SchemaVersion = 1
	decoded, err := DecodeSnapshot(wire(t, snapshot))
	invalid(t, err)
	if !reflect.DeepEqual(decoded, Snapshot{}) {
		t.Fatal("partial failed snapshot returned")
	}
	m := fixtureMutation()
	m.SchemaVersion = 1
	mutation, err := DecodeMutation(wire(t, m))
	invalid(t, err)
	if mutation != (Mutation{}) {
		t.Fatal("partial failed mutation returned")
	}
	r := fixtureResult()
	r.SchemaVersion = 1
	result, err := DecodeResult(wire(t, r))
	invalid(t, err)
	if result != (Result{}) {
		t.Fatal("partial failed result returned")
	}
	s = fixtureStatus()
	s.SchemaVersion = 1
	st, err := DecodeStatus(wire(t, s))
	invalid(t, err)
	if !reflect.DeepEqual(st, Status{}) {
		t.Fatal("partial failed status returned")
	}
}
func TestIndependentV2SHAGoldenVectors(t *testing.T) {
	// Constants computed independently using Python hashlib.sha256 over explicit
	// ordered JSON fields and Go-standard HTML escaping (the URL includes &).
	snapshot := fixtureAutoSnapshot()
	got, err := snapshot.Digest()
	if err != nil || got != "b8e4dac4b2a96fb1c2e86a88b5a6cc130e9bed1833f72562675f3077728d0034" {
		t.Fatal("v2 snapshot golden SHA changed", err)
	}
	mutation := fixtureMutation()
	got, err = mutation.Digest()
	if err != nil || got != "59a81dbb6b3492797730c4876962e926ca596574ac8d03ef57c964a632c69cb1" {
		t.Fatal("v2 mutation golden SHA changed", err)
	}
	baseline, _ := snapshot.Digest()
	for _, mutate := range []func(*Snapshot){func(s *Snapshot) { proxy(s, "group:auto").Probe.URL = "https://other.example.test/" }, func(s *Snapshot) { proxy(s, "group:auto").Probe.IntervalSeconds = 61 }, func(s *Snapshot) { proxy(s, "group:auto").Probe.TimeoutMS = 1001 }, func(s *Snapshot) { proxy(s, "group:auto").Probe.ToleranceMS = 1 }} {
		s := fixtureAutoSnapshot()
		mutate(&s)
		hash, err := s.Digest()
		if err != nil || hash == baseline {
			t.Fatal("probe immutable intent omitted from digest", err)
		}
	}
}

func TestEveryWireObjectRequiresExactFields(t *testing.T) {
	type location struct {
		path []any
		keys []string
	}
	cases := []struct {
		value  any
		decode func([]byte) error
	}{
		{fixtureAutoSnapshot(), func(raw []byte) error { _, err := DecodeSnapshot(raw); return err }},
		{fixtureMutation(), func(raw []byte) error { _, err := DecodeMutation(raw); return err }},
		{fixtureResult(), func(raw []byte) error { _, err := DecodeResult(raw); return err }},
	}
	status := fixtureStatus()
	status.State = "draining"
	a := fixtureResult()
	d := Result{2, "op:drain", strings.Repeat("e", 64), a.RunID, "draining"}
	status.Activation = &a
	status.Drain = &d
	cases = append(cases, struct {
		value  any
		decode func([]byte) error
	}{status, func(raw []byte) error { _, err := DecodeStatus(raw); return err }})
	for _, c := range cases {
		raw := wire(t, c.value)
		if err := c.decode(raw); err != nil {
			t.Fatal("invalid exact-field fixture", err)
		}
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			t.Fatal("fixture")
		}
		locations := []location{}
		var walk func(any, []any)
		walk = func(value any, path []any) {
			switch m := value.(type) {
			case map[string]any:
				loc := location{path: append([]any{}, path...)}
				for key, child := range m {
					loc.keys = append(loc.keys, key)
					walk(child, append(append([]any{}, path...), key))
				}
				locations = append(locations, loc)
			case []any:
				for i, child := range m {
					walk(child, append(append([]any{}, path...), i))
				}
			}
		}
		walk(root, nil)
		objectAt := func(root map[string]any, path []any) map[string]any {
			var value any = root
			for _, part := range path {
				switch p := part.(type) {
				case string:
					value = value.(map[string]any)[p]
				case int:
					value = value.([]any)[p]
				}
			}
			return value.(map[string]any)
		}
		clone := func() map[string]any {
			var root map[string]any
			if json.Unmarshal(raw, &root) != nil {
				t.Fatal("fixture")
			}
			return root
		}
		for _, loc := range locations {
			copy := clone()
			objectAt(copy, loc.path)["foreign_canary"] = true
			invalid(t, c.decode(wire(t, copy)))
			for _, key := range loc.keys {
				if key == "activation" || key == "drain" {
					continue
				}
				copy := clone()
				delete(objectAt(copy, loc.path), key)
				invalid(t, c.decode(wire(t, copy)))
			}
		}
	}
}
