package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFlowCountersUseCompleteLabelsOnceAndRejectFloatPrecision(t *testing.T) {
	before, err := ParseCounterSnapshot([]byte("gost_service_transfer_input_bytes_total{host=\"h\",service=\"s\",client=\"a\"} 10\ngost_service_transfer_input_bytes_total{client=\"b\",service=\"s\",host=\"h\"} 20\ngost_service_transfer_output_bytes_total{service=\"s\",host=\"h\"} 1e1\n"), "epoch-a")
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseCounterSnapshot([]byte("gost_service_transfer_input_bytes_total{client=\"a\",service=\"s\",host=\"h\"} 13\ngost_service_transfer_input_bytes_total{client=\"b\",service=\"s\",host=\"h\"} 25\ngost_service_transfer_output_bytes_total{host=\"h\",service=\"s\"} 1.1e1\n"), "epoch-a")
	if err != nil {
		t.Fatal(err)
	}
	delta := CompareCounters(before, after)
	if delta.Status != Passed || delta.InputBytes == nil || *delta.InputBytes != 8 {
		t.Fatalf("double-counted or dropped labels: %+v", delta)
	}
	encoded, err := json.Marshal(delta)
	if err != nil || !strings.Contains(string(encoded), `"input_bytes":"8"`) || !strings.Contains(string(encoded), `"output_bytes":"1"`) {
		t.Fatalf("counter bytes must remain decimal JSON strings: %s, %v", encoded, err)
	}
	for _, value := range []string{"9007199254740990.5", "9007199254740992", "9007199254740993", "9.007199254740992e+15", "NaN", "+Inf", "-1", "1.5"} {
		if _, err := ParseCounterSnapshot([]byte("gost_service_transfer_input_bytes_total{service=\"s\"} "+value+"\n"), "e"); err == nil {
			t.Fatalf("accepted imprecise byte source %s", value)
		}
	}
	if _, err := ParseCounterSnapshot([]byte("gost_service_transfer_input_bytes_total{service=\"s\"} 1\ngost_service_transfer_input_bytes_total{service=\"s\"} 2\n"), "e"); err == nil {
		t.Fatal("duplicate series accepted")
	}
	after.Epoch = "epoch-b"
	if r := CompareCounters(before, after); r.Status != Unverified || r.InputBytes != nil {
		t.Fatal("cross-epoch subtraction")
	}
	after.Epoch = "epoch-a"
	for key := range after.Values {
		after.Values[key] = 0
		break
	}
	if r := CompareCounters(before, after); r.Status != Unverified || r.InputBytes != nil {
		t.Fatal("decrease silently zero-filled")
	}
	after.Values = map[string]uint64{}
	if r := CompareCounters(before, after); r.Status != Unverified || r.InputBytes != nil {
		t.Fatal("missing series silently zero-filled")
	}
}

func TestFlowQualityDefinitionsAndHistoricalChainsStaySeparate(t *testing.T) {
	timing := TimingObservation{Definition: "client_round_trip", Target: "controlled", Outcome: "success", Start: time.Unix(1, 0), End: time.Unix(2, 0)}
	if r := CheckTiming(timing, "forward_setup", "controlled"); r.Status != Unverified {
		t.Fatal("client RTT masqueraded as forward setup")
	}
	timing.Definition = "forward_setup"
	if r := CheckTiming(timing, "forward_setup", "controlled"); r.Status != Passed {
		t.Fatal(r)
	}
	timing.Outcome = "timeout"
	if r := CheckTiming(timing, "forward_setup", "controlled"); r.Status != Unverified {
		t.Fatal("timeout duration treated as successful latency")
	}
	route := RouteObservation{ConnectionID: "original-1", Generation: "gen-old", Node: "node-a", Provider: "source-a", Chain: []string{"node-a", "DIRECT"}, EvidenceID: "raw-connection-event"}
	if r := CheckRoute(route, "original-1", "gen-old"); r.Status != Passed {
		t.Fatal(r)
	}
	route.CurrentSelection = true
	if r := CheckRoute(route, "original-1", "gen-old"); r.Status != Failed {
		t.Fatal("current now backfilled historical connection")
	}
}

func TestFlowHTTPConnectAndSOCKS5HandshakesInMemory(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		done := make(chan error, 1)
		go func() {
			defer server.Close()
			req, err := http.ReadRequest(newBufferedReader(server))
			if err != nil {
				done <- err
				return
			}
			if req.Method != "CONNECT" || req.Host != "127.0.0.1:443" || req.Header.Get("Proxy-Authorization") == "" {
				done <- errProtocol
				return
			}
			_, err = io.WriteString(server, "HTTP/1.1 200 Connection Established\r\n\r\n")
			done <- err
		}()
		if err := httpConnect(client, "127.0.0.1:443", Credential{"u", "p"}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("socks", func(t *testing.T) {
		client, server := net.Pipe()
		defer client.Close()
		done := make(chan error, 1)
		go func() {
			defer server.Close()
			buf := make([]byte, 3)
			if _, e := io.ReadFull(server, buf); e != nil {
				done <- e
				return
			}
			if string(buf) != string([]byte{5, 1, 2}) {
				done <- errProtocol
				return
			}
			server.Write([]byte{5, 2})
			auth := make([]byte, 5)
			if _, e := io.ReadFull(server, auth); e != nil {
				done <- e
				return
			}
			if string(auth) != string([]byte{1, 1, 'u', 1, 'p'}) {
				done <- errProtocol
				return
			}
			server.Write([]byte{1, 0})
			request := make([]byte, 10)
			if _, e := io.ReadFull(server, request); e != nil {
				done <- e
				return
			}
			if request[0] != 5 || request[1] != 1 || request[3] != 1 {
				done <- errProtocol
				return
			}
			_, e := server.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1})
			done <- e
		}()
		if err := socksConnect(client, "127.0.0.1:443", Credential{"u", "p"}); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestFlowExplicitProxyDoesNotBypassLocalhost(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://unexpected.invalid:1")
	t.Setenv("NO_PROXY", "*")
	proxy, _ := url.Parse("http://u:p@127.0.0.1:12345")
	tr := proxyTransport(proxy, nil)
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequest("GET", "http://127.0.0.1:23456/download", nil)
	got, err := tr.Proxy(req)
	if err != nil || got.String() != proxy.String() {
		t.Fatal("local target bypassed explicit proxy")
	}
	for _, o := range []FlowObservation{{Name: "download", Outcome: "success", Bytes: payloadSize, Expected: payloadSize, Terminal: true}, {Name: "long", Outcome: "success", Bytes: longSize, Expected: longSize, Terminal: true}, {Name: "failure", Outcome: "failure", ExpectedOutcome: "failure"}, {Name: "timeout", Outcome: "timeout", ExpectedOutcome: "timeout"}} {
		if r := CheckFlow(o); r.Status != Passed {
			t.Fatalf("reference flow: %+v", r)
		}
	}
	if r := CheckFlow(FlowObservation{Name: "long", Outcome: "success", Bytes: 1, Expected: longSize}); r.Status == Passed {
		t.Fatal("long flow terminal bytes omitted")
	}
}

func TestFlowIntegrationOptIn(t *testing.T) {
	if os.Getenv("HOOPA_P0_INTEGRATION") != "1" {
		t.Skip("unverified: explicit integration opt-in required; no core or socket started")
	}
	o := Options{Mihomo: os.Getenv("HOOPA_TEST_MIHOMO"), MihomoSHA: os.Getenv("HOOPA_TEST_MIHOMO_SHA256"), Gost: os.Getenv("HOOPA_TEST_GOST"), GostSHA: os.Getenv("HOOPA_TEST_GOST_SHA256"), Base: t.TempDir(), Timeout: 45 * time.Second}
	if o.Mihomo == "" || o.Gost == "" || o.MihomoSHA == "" || o.GostSHA == "" {
		t.Skip("unverified: explicit fixed core inputs missing")
	}
	result := runHarness(context.Background(), o)
	if result.ReasonCode != "completed" {
		t.Fatalf("controlled harness: %s", result.ReasonCode)
	}
	for _, flow := range result.Flows {
		if flow.Status != Passed {
			t.Fatalf("calibration %s: %s", flow.CaseID, flow.ReasonCode)
		}
	}
	for _, gate := range result.Gates {
		if gate.Status != Unverified {
			t.Fatal("minimal harness must not fabricate P0 gate closure")
		}
	}
}

func TestFlowVersionExtractionAndConfiguration(t *testing.T) {
	for _, c := range []struct{ role, raw, want string }{{"mihomo", "Mihomo Meta v1.19.26 linux amd64 with go1.25.5", "1.19.26"}, {"gost", "gost 3.3.1 (go1.24 linux/amd64)", "3.3.1"}, {"agent", "codex-cli 0.116.0", "0.116.0"}} {
		got, err := extractVersion(c.role, []byte(c.raw))
		if err != nil || got != c.want {
			t.Fatalf("version field %s: %s %v", c.role, got, err)
		}
	}
	cfg := ConfigValues{MihomoPort: "10001", ControllerPort: "10002", HTTPPort: "10003", SOCKSPort: "10004", MetricsPort: "10005", MihomoAuth: Credential{"mh", "mhpassword"}, HTTPAuth: Credential{"http", "httppassword"}, SOCKSAuth: Credential{"socks", "sockspassword"}, MetricsAuth: Credential{"metrics", "metricspassword"}, ControllerSecret: "controllerpassword", ServicePrefix: "run-a", DefaultTarget: "DIRECT"}
	mh, gs, err := renderConfigs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"skip-auth-prefixes: []", "authentication:", "mh:mhpassword", "secret: \"controllerpassword\"", "geo-auto-update: false"} {
		if !strings.Contains(string(mh), want) {
			t.Fatal("missing Mihomo protection", want)
		}
	}
	for _, want := range []string{"type: http", "type: socks5", "handler:", "connector:", "metrics:", "httppassword", "sockspassword", "metricspassword", "mhpassword"} {
		if !strings.Contains(string(gs), want) {
			t.Fatal("missing GOST protection", want)
		}
	}
	if strings.Contains(string(gs), "api:") {
		t.Fatal("GOST management API unexpectedly enabled")
	}
}

func TestFlowCoreStartIdentityIsObservedLocally(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("unverified: Linux /proc identity probe only")
	}
	start, boot, err := startIdentity(os.Getpid())
	if err != nil || start == "" || boot == "" {
		t.Fatalf("local OS identity unavailable: %v", err)
	}
	// This proves only this helper's local read, not a source socket or G04 gate.
}
