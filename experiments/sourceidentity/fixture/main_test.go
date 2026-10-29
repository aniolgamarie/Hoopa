package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"hoopa/experiments/sourceidentity/acceptedwitness"
)

const tokenA = "0123456789abcdef0123456789abcdef"
const tokenB = "abcdef0123456789abcdef0123456789"

func TestOptions(t *testing.T) {
	base := []string{"--role", "client", "--generation", "18446744073709551615", "--port", "65535", "--token", tokenA, "--allow-loopback"}
	o, err := parseOptions(base)
	if err != nil || o.generation != math.MaxUint64 || !o.allow {
		t.Fatalf("%+v %v", o, err)
	}
	server := []string{"--role", "serve", "--generation", "1", "--port", "1", "--count", "16", "--allow-loopback"}
	if _, err := parseOptions(server); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		base[:len(base)-1], append(append([]string{}, base...), "extra"), append(append([]string{}, base...), "--count", "1"),
		append(append([]string{}, server...), "--token", tokenA),
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	for field, values := range map[string][]string{
		"--role": {"other", ""}, "--generation": {"0", "-1", "18446744073709551616", "0x10", ""},
		"--port": {"0", "65536", "-1"}, "--token": {"", tokenA + "a", tokenA[:31], strings.ToUpper(tokenA), strings.Repeat("g", 32)},
	} {
		for _, value := range values {
			args := append([]string{}, base...)
			for i := range args {
				if args[i] == field {
					args[i+1] = value
					break
				}
			}
			if _, err := parseOptions(args); err == nil {
				t.Fatalf("accepted %s=%q", field, value)
			}
		}
	}
	for _, value := range []string{"0", "17", "-1"} {
		args := append([]string{}, server...)
		args[7] = value
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("accepted count=%s", value)
		}
	}
}

type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(b []byte) (int, error) {
	if len(b) > 1 {
		b = b[:1]
	}
	return r.Reader.Read(b)
}

type brokenWriter struct {
	n   int
	err error
}

func (w brokenWriter) Write([]byte) (int, error) { return w.n, w.err }
func TestFrames(t *testing.T) {
	for _, input := range []string{tokenA + "\n", tokenB + "\n"} {
		token, err := readFrame(oneByteReader{strings.NewReader(input)})
		if err != nil || token+"\n" != input {
			t.Fatalf("%q %v", token, err)
		}
	}
	for _, input := range []string{"", tokenA, tokenA[:31] + "\n", tokenA + "x", tokenA + "\n\n", tokenA + "\n" + tokenB + "\n", strings.ToUpper(tokenA) + "\n"} {
		if _, err := readFrame(oneByteReader{strings.NewReader(input)}); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for _, writer := range []io.Writer{brokenWriter{0, nil}, brokenWriter{34, nil}, brokenWriter{0, errors.New("write")}} {
		if err := writeFrame(writer, tokenA); err == nil {
			t.Fatal("accepted bad writer")
		}
	}
	var written bytes.Buffer
	if err := writeFrame(&written, tokenA); err != nil || written.String() != tokenA+"\n" {
		t.Fatal(written.String(), err)
	}
	if err := writeFrame(&written, "bad"); err == nil {
		t.Fatal("invalid token written")
	}
}

func procStat(pid int, comm, ticks string) []byte {
	// Fields 3..21 precede field 22 (starttime).
	return []byte(strconv.Itoa(pid) + " (" + comm + ") S " + strings.Repeat("0 ", 18) + ticks + " 0 0\n")
}
func TestProcStat(t *testing.T) {
	for _, comm := range []string{"name", "with spaces", "ends)", "a ) b (c))"} {
		ticks, err := parseStartTicks(procStat(42, comm, "18446744073709551615"), 42)
		if err != nil || ticks != "18446744073709551615" {
			t.Fatalf("comm=%q ticks=%q err=%v", comm, ticks, err)
		}
	}
	for _, raw := range [][]byte{nil, []byte("42 broken"), procStat(43, "name", "1"), procStat(42, "name", "0"), procStat(42, "name", "bad"), procStat(42, "name", "18446744073709551616"), []byte("42 (x) S 0"), bytes.Repeat([]byte("x"), 4097)} {
		if _, err := parseStartTicks(raw, 42); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

type fakeConn struct {
	reader             io.Reader
	writes             bytes.Buffer
	deadline           time.Time
	closed, halfClosed int
	fail               string
	snapshot           acceptedwitness.Snapshot
	events             []string
}

func (c *fakeConn) Read(b []byte) (int, error) {
	c.events = append(c.events, "read")
	if c.fail == "read" {
		return 0, errors.New("read")
	}
	return c.reader.Read(b)
}
func (c *fakeConn) Write(b []byte) (int, error) {
	c.events = append(c.events, "write")
	if c.fail == "write" {
		return 0, errors.New("write")
	}
	return c.writes.Write(b)
}
func (c *fakeConn) Close() error {
	c.closed++
	c.events = append(c.events, "close")
	if c.fail == "close" {
		return errors.New("close")
	}
	return nil
}
func (c *fakeConn) CloseWrite() error {
	c.halfClosed++
	c.events = append(c.events, "halfclose")
	if c.fail == "halfclose" {
		return errors.New("halfclose")
	}
	return nil
}
func (c *fakeConn) SetDeadline(t time.Time) error {
	c.deadline = t
	if c.fail == "deadline" {
		return errors.New("deadline")
	}
	return nil
}
func (c *fakeConn) Snapshot() (acceptedwitness.Snapshot, error) {
	if c.fail == "snapshot" {
		return acceptedwitness.Snapshot{}, errors.New("snapshot")
	}
	return c.snapshot, nil
}

type fakeListener struct {
	conns           []*fakeConn
	accepts, closed int
	deadline        time.Time
	fail            string
}

func (l *fakeListener) Accept() (witnessStream, error) {
	if l.fail == "accept" {
		return nil, errors.New("accept")
	}
	c := l.conns[l.accepts]
	l.accepts++
	return c, nil
}
func (l *fakeListener) SetDeadline(t time.Time) error {
	l.deadline = t
	if l.fail == "deadline" {
		return errors.New("deadline")
	}
	return nil
}
func (l *fakeListener) Close() error {
	l.closed++
	if l.fail == "close" {
		return errors.New("close")
	}
	return nil
}
func testOptions(role string) options {
	return options{role: role, generation: math.MaxUint64, port: 8080, count: 2, token: tokenA, allow: true}
}
func clientDeps(t *testing.T, c *fakeConn) runtimeDeps {
	now := time.Unix(100, 0)
	return runtimeDeps{now: func() time.Time { return now }, dial: func(port int, total time.Time) (clientStream, error) {
		if port != 8080 || !total.Equal(now.Add(10*time.Second)) {
			t.Fatal("dial deadline/port", port, total)
		}
		return c, nil
	}, capture: func(got clientStream) (socketObservation, error) {
		if got != c {
			t.Fatal("not original connection")
		}
		c.events = append(c.events, "capture")
		return socketObservation{SocketCookie: "18446744073709551615", NetNSCookie: "9007199254740993", LocalEndpoint: "127.0.0.1:40000", RemoteEndpoint: "127.0.0.1:8080"}, nil
	}, process: func() (processObservation, error) {
		return processObservation{PID: 42, StartTicks: "9007199254740995", PIDNSInode: "18446744073709551615"}, nil
	}}
}
func TestClientProtocolAndJSONPrecision(t *testing.T) {
	c := &fakeConn{reader: strings.NewReader(tokenA + "\n")}
	var out bytes.Buffer
	if err := execute(testOptions("client"), &out, clientDeps(t, c)); err != nil {
		t.Fatal(err)
	}
	if c.closed != 1 || c.halfClosed != 1 || c.writes.String() != tokenA+"\n" {
		t.Fatalf("%+v", c)
	}
	if !c.deadline.Equal(time.Unix(103, 0)) {
		t.Fatal("connection deadline", c.deadline)
	}
	if !reflect.DeepEqual(c.events[:3], []string{"capture", "write", "halfclose"}) {
		t.Fatal(c.events)
	}
	var r map[string]any
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"schema": "sourceidentity-fixture-v1", "event": "connection", "role": "client", "token": tokenA, "generation": "18446744073709551615", "socket_cookie": "18446744073709551615", "netns_cookie": "9007199254740993", "start_ticks": "9007199254740995", "pidns_inode": "18446744073709551615", "identity_capability": "unverified"} {
		if r[k] != want {
			t.Fatalf("%s=%v", k, r[k])
		}
	}
	if r["pid"] != float64(42) || r["witness"] != nil {
		t.Fatal(r)
	}
}
func TestClientFailuresCloseAndPublishNothing(t *testing.T) {
	for _, stage := range []string{"dial", "deadline", "capture", "process", "write", "halfclose", "read", "echo", "truncated", "extra", "close"} {
		t.Run(stage, func(t *testing.T) {
			c := &fakeConn{reader: strings.NewReader(tokenA + "\n"), fail: stage}
			deps := clientDeps(t, c)
			switch stage {
			case "dial":
				deps.dial = func(int, time.Time) (clientStream, error) { return nil, errors.New("dial") }
			case "capture":
				deps.capture = func(clientStream) (socketObservation, error) { return socketObservation{}, errors.New("capture") }
			case "process":
				deps.process = func() (processObservation, error) { return processObservation{}, errors.New("process") }
			case "echo":
				c.reader = strings.NewReader(tokenB + "\n")
			case "truncated":
				c.reader = strings.NewReader(tokenA)
			case "extra":
				c.reader = strings.NewReader(tokenA + "\nx")
			}
			var out bytes.Buffer
			if err := execute(testOptions("client"), &out, deps); err == nil {
				t.Fatal("success")
			}
			if out.Len() != 0 {
				t.Fatal("failure published", out.String())
			}
			want := 1
			if stage == "dial" {
				want = 0
			}
			if c.closed != want {
				t.Fatalf("closed=%d", c.closed)
			}
		})
	}
}
func TestServerProtocolAndWitness(t *testing.T) {
	witness := acceptedwitness.Snapshot{Schema: "accepted-socket-witness-v1", Generation: "18446744073709551615", SocketCookie: "18446744073709551615", NetNSCookie: "9007199254740993", LocalEndpoint: "127.0.0.1:8080", RemoteEndpoint: "127.0.0.1:40000", Association: "unverified", IdentityCapability: "unverified"}
	l := &fakeListener{conns: []*fakeConn{{reader: strings.NewReader(tokenA + "\n"), snapshot: witness}, {reader: strings.NewReader(tokenB + "\n"), snapshot: witness}}}
	now := time.Unix(100, 0)
	deps := runtimeDeps{now: func() time.Time { return now }, listen: func(port int, generation uint64) (witnessListener, error) {
		if port != 8080 || generation != math.MaxUint64 {
			t.Fatal(port, generation)
		}
		return l, nil
	}}
	var out bytes.Buffer
	if err := execute(testOptions("serve"), &out, deps); err != nil {
		t.Fatal(err)
	}
	if l.closed != 1 || l.accepts != 2 || !l.deadline.Equal(now.Add(10*time.Second)) {
		t.Fatalf("%+v", l)
	}
	decoder := json.NewDecoder(&out)
	var ready map[string]any
	if err := decoder.Decode(&ready); err != nil || ready["event"] != "listening" || ready["port"] != float64(8080) {
		t.Fatal(ready, err)
	}
	for i, token := range []string{tokenA, tokenB} {
		var got record
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Token != token || got.Witness == nil || *got.Witness != witness || got.Role != "serve" || got.socketObservation != nil || got.processObservation != nil {
			t.Fatalf("%+v", got)
		}
		c := l.conns[i]
		if c.closed != 1 || c.halfClosed != 0 || c.writes.String() != token+"\n" || !c.deadline.Equal(now.Add(3*time.Second)) {
			t.Fatalf("%+v", c)
		}
	}
}
func TestServerFailuresCloseAndPublishNoConnection(t *testing.T) {
	for _, stage := range []string{"listen", "listener-deadline", "accept", "deadline", "snapshot", "read", "write", "close", "truncated", "extra"} {
		t.Run(stage, func(t *testing.T) {
			c := &fakeConn{reader: strings.NewReader(tokenA + "\n"), fail: stage}
			l := &fakeListener{conns: []*fakeConn{c}}
			deps := runtimeDeps{now: func() time.Time { return time.Unix(0, 0) }, listen: func(int, uint64) (witnessListener, error) { return l, nil }}
			switch stage {
			case "listen":
				deps.listen = func(int, uint64) (witnessListener, error) { return nil, errors.New("listen") }
			case "listener-deadline":
				l.fail = "deadline"
			case "accept":
				l.fail = "accept"
			case "truncated":
				c.reader = strings.NewReader(tokenA)
			case "extra":
				c.reader = strings.NewReader(tokenA + "\nx")
			}
			var out bytes.Buffer
			if err := execute(testOptions("serve"), &out, deps); err == nil {
				t.Fatal("success")
			}
			if strings.Contains(out.String(), `"event":"connection"`) {
				t.Fatal("failure published", out.String())
			}
			want := 1
			if stage == "listen" {
				want = 0
			}
			if l.closed != want {
				t.Fatal("listener closes", l.closed)
			}
			want = 1
			if stage == "listen" || stage == "listener-deadline" || stage == "accept" {
				want = 0
			}
			if c.closed != want {
				t.Fatal("conn closes", c.closed)
			}
		})
	}
}
func TestNoAllowBeforeAnySocketAndDeadlineCap(t *testing.T) {
	o := testOptions("client")
	o.allow = false
	if err := execute(o, io.Discard, runtimeDeps{}); err == nil {
		t.Fatal("socket allowed")
	}
	now := time.Unix(100, 0)
	total := now.Add(time.Second)
	if got := connectionDeadline(now, total); !got.Equal(total) {
		t.Fatal(got)
	}
}

func TestServerSecondFailureOrListenerClosePublishesOnlyReady(t *testing.T) {
	for _, failure := range []string{"second-read", "second-close", "listener-close"} {
		t.Run(failure, func(t *testing.T) {
			first := &fakeConn{reader: strings.NewReader(tokenA + "\n")}
			second := &fakeConn{reader: strings.NewReader(tokenB + "\n")}
			listener := &fakeListener{conns: []*fakeConn{first, second}}
			switch failure {
			case "second-read":
				second.reader = strings.NewReader(tokenB)
			case "second-close":
				second.fail = "close"
			case "listener-close":
				listener.fail = "close"
			}
			deps := runtimeDeps{now: func() time.Time { return time.Unix(0, 0) }, listen: func(int, uint64) (witnessListener, error) { return listener, nil }}
			var output bytes.Buffer
			if err := execute(testOptions("serve"), &output, deps); err == nil {
				t.Fatal("failure accepted")
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 1 || !strings.Contains(lines[0], `"event":"listening"`) {
				t.Fatal("partial server records", output.String())
			}
			if first.closed != 1 || second.closed != 1 || listener.closed != 1 {
				t.Fatalf("close counts %d %d %d", first.closed, second.closed, listener.closed)
			}
		})
	}
}

type failingOutput struct{ writes int }

func (w *failingOutput) Write(b []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("stdout failure")
	}
	return len(b), nil
}
func TestServerOutputFailureStillClosesAllResources(t *testing.T) {
	first := &fakeConn{reader: strings.NewReader(tokenA + "\n")}
	second := &fakeConn{reader: strings.NewReader(tokenB + "\n")}
	listener := &fakeListener{conns: []*fakeConn{first, second}}
	deps := runtimeDeps{now: func() time.Time { return time.Unix(0, 0) }, listen: func(int, uint64) (witnessListener, error) { return listener, nil }}
	if err := execute(testOptions("serve"), &failingOutput{}, deps); err == nil {
		t.Fatal("stdout failure accepted")
	}
	if first.closed != 1 || second.closed != 1 || listener.closed != 1 {
		t.Fatal("resources not closed before emitting records")
	}
}
