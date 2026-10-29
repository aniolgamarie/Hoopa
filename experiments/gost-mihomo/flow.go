package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hoopa/internal/config"
)

const payloadSize = 32 * 1024
const uploadSize = 16 * 1024
const longSize = 128 * 1024

var errProtocol = errors.New("proxy_protocol_failed")
var metricLine = regexp.MustCompile(`^(gost_service_transfer_(?:input|output)_bytes_total)(\{.*\})?\s+(\S+)(?:\s+\S+)?$`)
var metricLabel = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=("(?:[^"\\]|\\.)*")`)

type CounterSnapshot struct {
	Epoch  string
	Values map[string]uint64
}
type CounterDifference struct {
	Status      EvidenceStatus `json:"status"`
	ReasonCode  string         `json:"reason_code"`
	InputBytes  *config.Bytes  `json:"input_bytes"`
	OutputBytes *config.Bytes  `json:"output_bytes"`
}

// Values are Prometheus float-source counters, not arbitrary-precision byte
// facts. Even integer-looking values at or above 2^53 are unsafe for exact deltas.
func ParseCounterSnapshot(raw []byte, epoch string) (CounterSnapshot, error) {
	snapshot := CounterSnapshot{epoch, map[string]uint64{}}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "gost_service_transfer_input_bytes_total") && !strings.HasPrefix(line, "gost_service_transfer_output_bytes_total") {
			continue
		}
		m := metricLine.FindStringSubmatch(line)
		if m == nil {
			return snapshot, errors.New("counter_format_unsupported")
		}
		rat, ok := new(big.Rat).SetString(m[3])
		if !ok || !rat.IsInt() || rat.Sign() < 0 || rat.Cmp(new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 53))) >= 0 {
			return snapshot, errors.New("counter_precision_unavailable")
		}
		labels := []string{}
		seen := map[string]bool{}
		if m[2] != "" {
			rest := strings.TrimSpace(m[2][1 : len(m[2])-1])
			for rest != "" {
				match := metricLabel.FindStringSubmatch(rest)
				if match == nil || seen[match[1]] {
					return snapshot, errors.New("counter_labels_invalid")
				}
				value, err := strconv.Unquote(match[2])
				if err != nil {
					return snapshot, errors.New("counter_labels_invalid")
				}
				seen[match[1]] = true
				labels = append(labels, match[1]+"="+strconv.Quote(value))
				rest = strings.TrimSpace(rest[len(match[0]):])
				if rest != "" {
					if rest[0] != ',' {
						return snapshot, errors.New("counter_labels_invalid")
					}
					rest = strings.TrimSpace(rest[1:])
					if rest == "" {
						return snapshot, errors.New("counter_labels_invalid")
					}
				}
			}
		}
		sort.Strings(labels)
		key := m[1] + "{" + strings.Join(labels, ",") + "}"
		if _, exists := snapshot.Values[key]; exists {
			return snapshot, errors.New("counter_duplicate_series")
		}
		snapshot.Values[key] = rat.Num().Uint64()
	}
	return snapshot, nil
}

func CompareCounters(before, after CounterSnapshot) CounterDifference {
	unavailable := func(reason string) CounterDifference {
		return CounterDifference{Status: Unverified, ReasonCode: reason}
	}
	if before.Epoch == "" || before.Epoch != after.Epoch {
		return unavailable("counter_epoch_changed")
	}
	if len(before.Values) == 0 || len(before.Values) != len(after.Values) {
		return unavailable("counter_series_changed")
	}
	var input, output config.Bytes
	haveInput, haveOutput := false, false
	for key, old := range before.Values {
		current, ok := after.Values[key]
		if !ok {
			return unavailable("counter_series_changed")
		}
		if current < old {
			return unavailable("counter_decreased")
		}
		var err error
		if strings.HasPrefix(key, "gost_service_transfer_input_") {
			haveInput = true
			input, err = input.Add(config.Bytes(current - old))
		} else {
			haveOutput = true
			output, err = output.Add(config.Bytes(current - old))
		}
		if err != nil {
			return unavailable("counter_sum_overflow")
		}
	}
	in, out := input, output
	result := CounterDifference{Status: Passed, ReasonCode: "same_epoch_series_deltas"}
	if haveInput {
		result.InputBytes = &in
	}
	if haveOutput {
		result.OutputBytes = &out
	}
	if !haveInput || !haveOutput {
		result.Status = Unverified
		result.ReasonCode = "counter_direction_missing"
	}
	return result
}

type TimingObservation struct {
	Definition, Target, Outcome string
	Start, End                  time.Time
}

func CheckTiming(o TimingObservation, definition, target string) CheckResult {
	if o.Definition != definition || o.Target != target {
		return CheckResult{"G03", Unverified, "timing_not_comparable"}
	}
	if o.Outcome != "success" {
		return CheckResult{"G03", Unverified, "successful_timing_unavailable"}
	}
	if o.Start.IsZero() || o.End.IsZero() {
		return CheckResult{"G03", Unverified, "timing_boundary_missing"}
	}
	if _, err := config.DurationBetween(o.Start, o.End); err != nil {
		return CheckResult{"G03", Failed, "timing_boundary_invalid"}
	}
	return CheckResult{"G03", Passed, "matching_timing_boundaries"}
}

type RouteObservation struct {
	ConnectionID, Generation, Node, Provider, EvidenceID string
	Chain                                                []string
	CurrentSelection                                     bool
}

func CheckRoute(o RouteObservation, connection, generation string) CheckResult {
	if o.CurrentSelection {
		return CheckResult{"G03", Failed, "historical_route_backfilled"}
	}
	if o.ConnectionID == "" || o.EvidenceID == "" || o.Node == "" || o.Provider == "" || len(o.Chain) == 0 {
		return CheckResult{"G03", Unverified, "actual_chain_missing"}
	}
	if o.ConnectionID != connection || o.Generation != generation {
		return CheckResult{"G03", Failed, "route_identity_mismatch"}
	}
	return CheckResult{"G03", Passed, "original_connection_chain"}
}

type FlowObservation struct {
	Name, Outcome, ExpectedOutcome string
	Bytes, Expected                int64
	Terminal                       bool
}

func CheckFlow(o FlowObservation) CheckResult {
	expected := o.ExpectedOutcome
	if expected == "" {
		expected = "success"
	}
	if o.Outcome != expected {
		return CheckResult{"CALIBRATION", Failed, "controlled_outcome_mismatch"}
	}
	if expected == "success" && (!o.Terminal || o.Bytes != o.Expected) {
		return CheckResult{"CALIBRATION", Failed, "controlled_bytes_mismatch"}
	}
	return CheckResult{"CALIBRATION", Passed, "controlled_reference_matches"}
}

type Credential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func newBufferedReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }
func httpConnect(conn net.Conn, target string, auth Credential) error {
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if auth.Username != "" {
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(auth.Username+":"+auth.Password)))
	}
	if err := request.Write(conn); err != nil {
		return errProtocol
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil || response.StatusCode != 200 {
		return errProtocol
	}
	return nil
}
func socksConnect(conn net.Conn, target string, auth Credential) error {
	method := byte(0)
	if auth.Username != "" {
		method = 2
	}
	if _, err := conn.Write([]byte{5, 1, method}); err != nil {
		return errProtocol
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != method {
		return errProtocol
	}
	if method == 2 {
		if len(auth.Username) > 255 || len(auth.Password) > 255 {
			return errProtocol
		}
		message := append([]byte{1, byte(len(auth.Username))}, []byte(auth.Username)...)
		message = append(message, byte(len(auth.Password)))
		message = append(message, []byte(auth.Password)...)
		if _, err := conn.Write(message); err != nil {
			return errProtocol
		}
		if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 1 || reply[1] != 0 {
			return errProtocol
		}
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return errProtocol
	}
	ip := net.ParseIP(host).To4()
	port, err := strconv.Atoi(portText)
	if ip == nil || err != nil || port < 1 || port > 65535 {
		return errProtocol
	}
	request := append([]byte{5, 1, 0, 1}, ip...)
	request = append(request, byte(port>>8), byte(port))
	if _, err := conn.Write(request); err != nil {
		return errProtocol
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 || head[1] != 0 {
		return errProtocol
	}
	size := 0
	switch head[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		one := make([]byte, 1)
		if _, err := io.ReadFull(conn, one); err != nil {
			return errProtocol
		}
		size = int(one[0])
	default:
		return errProtocol
	}
	if _, err := io.CopyN(io.Discard, conn, int64(size+2)); err != nil {
		return errProtocol
	}
	return nil
}

func proxyTransport(proxy *url.URL, roots *x509.CertPool) *http.Transport {
	return &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, MaxConnsPerHost: 2, ResponseHeaderTimeout: 3 * time.Second}
}
func proxyURL(scheme, address string, auth Credential) *url.URL {
	u := &url.URL{Scheme: scheme, Host: address}
	if auth.Username != "" {
		u.User = url.UserPassword(auth.Username, auth.Password)
	}
	return u
}

// The controlled handler knows application payload bytes only. It makes no
// assertion that proxy ingress counters exclude CONNECT/TLS/protocol overhead.
func calibrationHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/download":
		w.Write(bytes.Repeat([]byte{'d'}, payloadSize))
	case "/upload":
		n, err := io.Copy(io.Discard, io.LimitReader(r.Body, uploadSize+1))
		if err != nil || n != uploadSize {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("X-Received-Bytes", strconv.FormatInt(n, 10))
		w.WriteHeader(204)
	case "/long":
		chunk := bytes.Repeat([]byte{'l'}, 8*1024)
		for sent := 0; sent < longSize; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	case "/failure":
		w.WriteHeader(503)
	case "/slow":
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			w.WriteHeader(200)
		}
	default:
		w.WriteHeader(404)
	}
}

type CalibrationResult struct {
	CheckResult
	Path              string `json:"path"`
	Name              string `json:"name"`
	Outcome           string `json:"outcome"`
	Bytes             string `json:"application_bytes"`
	ClientRoundTripMS int64  `json:"client_round_trip_ms"`
}

func calibrate(ctx context.Context, path string, proxy *url.URL, httpTarget, tlsTarget string, roots *x509.CertPool, mid func()) []CalibrationResult {
	tr := proxyTransport(proxy, roots)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	results := []CalibrationResult{}
	for _, name := range []string{"download", "upload", "connect_https", "long", "failure", "timeout"} {
		target := httpTarget + "/" + name
		if name == "connect_https" {
			target = tlsTarget + "/download"
		}
		if name == "timeout" {
			target = httpTarget + "/slow"
		}
		requestContext, cancel := context.WithCancel(ctx)
		if name == "timeout" {
			requestContext, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
		}
		method := http.MethodGet
		var body io.Reader
		if name == "upload" {
			method = http.MethodPost
			body = bytes.NewReader(bytes.Repeat([]byte{'u'}, uploadSize))
		}
		req, _ := http.NewRequestWithContext(requestContext, method, target, body)
		start := time.Now()
		response, err := client.Do(req)
		observation := FlowObservation{Name: name, Outcome: "success", Terminal: true}
		switch name {
		case "download", "connect_https":
			observation.Expected = payloadSize
		case "long":
			observation.Expected = longSize
		case "upload":
			observation.Expected = uploadSize
		case "failure":
			observation.ExpectedOutcome = "failure"
		case "timeout":
			observation.ExpectedOutcome = "timeout"
		}
		if err != nil {
			observation.Outcome = "failure"
			if errors.Is(err, context.DeadlineExceeded) {
				observation.Outcome = "timeout"
			}
		} else {
			if response.StatusCode == 503 {
				observation.Outcome = "failure"
			} else if response.StatusCode < 200 || response.StatusCode >= 300 {
				observation.Outcome = "failure"
			}
			if name == "upload" {
				observation.Bytes, _ = strconv.ParseInt(response.Header.Get("X-Received-Bytes"), 10, 64)
				_, err = io.Copy(io.Discard, response.Body)
			} else if name == "long" {
				first := make([]byte, 4096)
				var n int
				n, err = response.Body.Read(first)
				observation.Bytes = int64(n)
				if mid != nil {
					mid()
				}
				var rest int64
				rest, err = io.Copy(io.Discard, response.Body)
				observation.Bytes += rest
			} else {
				observation.Bytes, err = io.Copy(io.Discard, response.Body)
			}
			response.Body.Close()
			if err != nil {
				observation.Terminal = false
				observation.Outcome = "failure"
			}
		}
		elapsed := time.Since(start).Milliseconds()
		cancel()
		results = append(results, CalibrationResult{CheckFlow(observation), path, name, observation.Outcome, strconv.FormatInt(observation.Bytes, 10), elapsed})
	}
	return results
}

func parseLoopbackProxy(address string) (string, string, error) {
	if address == "" {
		return "", "", nil
	}
	host, port, err := net.SplitHostPort(address)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("invalid_loopback_upstream")
	}
	return host, port, nil
}

func startTCPReference() (net.Listener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(4 * time.Second))
				payload := make([]byte, uploadSize)
				if _, err := io.ReadFull(conn, payload); err != nil || !bytes.Equal(payload, bytes.Repeat([]byte{'u'}, uploadSize)) {
					return
				}
				conn.Write(bytes.Repeat([]byte{'d'}, payloadSize))
			}()
		}
	}()
	return listener, nil
}
func calibrateTCP(ctx context.Context, path string, proxy *url.URL, target string) CalibrationResult {
	start := time.Now()
	observation := FlowObservation{Name: "tcp_fixed", Outcome: "success", Expected: payloadSize, Terminal: true}
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", proxy.Host)
	if err == nil {
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(4 * time.Second))
		auth := Credential{}
		if proxy.User != nil {
			auth.Username = proxy.User.Username()
			auth.Password, _ = proxy.User.Password()
		}
		if proxy.Scheme == "socks5" {
			err = socksConnect(conn, target, auth)
		} else {
			err = httpConnect(conn, target, auth)
		}
		if err == nil {
			_, err = conn.Write(bytes.Repeat([]byte{'u'}, uploadSize))
		}
		if err == nil {
			observation.Bytes, err = io.Copy(io.Discard, io.LimitReader(conn, payloadSize+1))
		}
	}
	if err != nil {
		observation.Outcome = "failure"
		observation.Terminal = false
	}
	return CalibrationResult{CheckFlow(observation), path, "tcp_fixed", observation.Outcome, strconv.FormatInt(observation.Bytes, 10), time.Since(start).Milliseconds()}
}
