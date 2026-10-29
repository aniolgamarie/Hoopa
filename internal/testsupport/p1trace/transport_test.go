//go:build linux && amd64 && p1integration

package p1trace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testStream = "0123456789abcdef0123456789abcdef"

func sample() Event {
	return Event{"manager", "before-commit", "op", "run", "req", json.RawMessage(`{"private":"value"}`)}
}
func capture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if e := os.Chmod(d, 0700); e != nil {
		t.Fatal(e)
	}
	return d
}
func pair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	a, b, e := SocketPair()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func peers(t *testing.T) (*Producer, *Collector, string) {
	t.Helper()
	a, b := pair(t)
	d := capture(t)
	p, e := NewProducer(a, os.Getpid(), os.Getuid(), testStream)
	if e != nil {
		t.Fatal(e)
	}
	c, e := NewCollector(b, os.Getpid(), os.Getuid(), testStream, d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close(); c.Close() })
	return p, c, d
}
func TestProcessHelper(t *testing.T) {
	args := os.Args
	at := -1
	for i, a := range args {
		if a == "--p1trace-child" {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	if len(args) != at+4 {
		os.Exit(81)
	}
	pid, e := strconv.Atoi(args[at+2])
	if e != nil {
		os.Exit(82)
	}
	f := os.NewFile(3, "inherited-p1trace")
	p, e := NewProducer(f, pid, os.Getuid(), args[at+3])
	f.Close()
	if e != nil {
		os.Exit(83)
	}
	defer p.Close()
	d, e := p.Emit(context.Background(), sample())
	if e != nil {
		os.Exit(84)
	}
	want := Continue
	if args[at+1] == "inject" {
		want = InjectError
	}
	if d != want {
		os.Exit(85)
	}
	os.Exit(0)
}
func child(t *testing.T, mode string) (*exec.Cmd, *Collector, string) {
	t.Helper()
	a, b := pair(t)
	d := capture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$", "--", "--p1trace-child", mode, strconv.Itoa(os.Getpid()), testStream)
	cmd.ExtraFiles = []*os.File{a}
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	a.Close()
	c, e := NewCollector(b, cmd.Process.Pid, os.Getuid(), testStream, d)
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(e)
	}
	b.Close()
	t.Cleanup(func() { c.Close(); cmd.Process.Kill(); cmd.Wait() })
	return cmd, c, d
}
func checkRefs(t *testing.T, p *Pending, n int) {
	t.Helper()
	refs := p.Originals()
	if len(refs) != n {
		t.Fatalf("original count %d want %d", len(refs), n)
	}
	for _, r := range refs {
		b, e := os.ReadFile(r.Path)
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		if int64(len(b)) != r.Bytes || hex.EncodeToString(h[:]) != r.SHA256 {
			t.Fatal("original mismatch")
		}
		st, e := os.Stat(r.Path)
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("original mode")
		}
	}
	if len(refs) > 0 {
		refs[0].Path = "mutated"
		if p.Originals()[0].Path == "mutated" {
			t.Fatal("mutable refs")
		}
	}
}
func TestCrossProcessDurableBeforeACK(t *testing.T) {
	for _, decision := range []Decision{Continue, InjectError} {
		t.Run(string(decision), func(t *testing.T) {
			mode := "normal"
			if decision == InjectError {
				mode = "inject"
			}
			cmd, c, _ := child(t, mode)
			p, e := c.Next(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if p.PeerPID() != cmd.Process.Pid || p.PeerPID() == os.Getpid() || p.PeerUID() != os.Getuid() {
				t.Fatal("not actual child credentials")
			}
			checkRefs(t, p, 2)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case e := <-done:
				t.Fatalf("child exited before ACK: %v", e)
			case <-time.After(40 * time.Millisecond):
			}
			if _, e = c.Next(context.Background()); e == nil {
				t.Fatal("next while pending")
			}
			if e = p.Decide(context.Background(), decision); e != nil {
				t.Fatal(e)
			}
			checkRefs(t, p, 3)
			if e = <-done; e != nil {
				t.Fatal(e)
			}
			if p.Decide(context.Background(), Continue) == nil || p.RecordCrashCut() == nil {
				t.Fatal("duplicate decision accepted")
			}
		})
	}
}
func TestCrashCutSurvivesChildKill(t *testing.T) {
	cmd, c, _ := child(t, "normal")
	p, e := c.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.RecordCrashCut(); e != nil {
		t.Fatal(e)
	}
	checkRefs(t, p, 3)
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = cmd.Wait(); e == nil {
		t.Fatal("child not killed")
	}
	checkRefs(t, p, 3)
	if _, e = c.Next(context.Background()); e == nil {
		t.Fatal("next after crash-cut")
	}
}
func TestLostACKNoReplay(t *testing.T) {
	p, c, d := peers(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := p.Emit(ctx, sample()); done <- e }()
	pending, e := c.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", e)
	}
	if _, e = p.Emit(context.Background(), sample()); e == nil {
		t.Fatal("reused producer")
	}
	checkRefs(t, pending, 2)
	names, e := os.ReadDir(d)
	if e != nil || len(names) != 2 {
		t.Fatal("replayed original")
	}
}
func TestSequenceAndPrivateFormatting(t *testing.T) {
	p, c, _ := peers(t)
	for i := 1; i <= 2; i++ {
		done := make(chan error, 1)
		go func() { _, e := p.Emit(context.Background(), sample()); done <- e }()
		pending, e := c.Next(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if pending.Sequence() != uint64(i) {
			t.Fatal("sequence")
		}
		copy := pending.Event()
		copy.Data[0] = 'x'
		if pending.Event().Data[0] != '{' {
			t.Fatal("event alias")
		}
		if strings.Contains(fmt.Sprintf("%v %#v %v %#v", sample(), sample(), pending, pending), "value") {
			t.Fatal("private format")
		}
		if e = pending.Decide(context.Background(), Continue); e != nil {
			t.Fatal(e)
		}
		if e = <-done; e != nil {
			t.Fatal(e)
		}
	}
}
func rawPeers(t *testing.T) (endpoint, *Collector, string) {
	t.Helper()
	a, b := pair(t)
	d := capture(t)
	ep, e := newEndpoint(a, os.Getpid(), os.Getuid(), testStream)
	if e != nil {
		t.Fatal(e)
	}
	c, e := NewCollector(b, os.Getpid(), os.Getuid(), testStream, d)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ep.conn.Close(); c.Close() })
	return ep, c, d
}
func sendRaw(t *testing.T, ep endpoint, raw []byte) {
	t.Helper()
	ctx, done, e := operation(context.Background(), ep.conn)
	if e != nil {
		t.Fatal(e)
	}
	defer done()
	if e = ep.send(ctx, raw); e != nil {
		t.Fatal(e)
	}
}
func TestWireRejections(t *testing.T) {
	valid, _ := json.Marshal(wire{1, testStream, 1, sample()})
	cases := map[string][]byte{
		"duplicate":         []byte(strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1)),
		"unknown":           []byte(strings.Replace(string(valid), `"version":1`, `"version":1,"extra":0`, 1)),
		"event-duplicate":   []byte(strings.Replace(string(valid), `"component":"manager"`, `"component":"manager","component":"manager"`, 1)),
		"event-unknown":     []byte(strings.Replace(string(valid), `"component":"manager"`, `"component":"manager","extra":0`, 1)),
		"event-null":        []byte(strings.Replace(string(valid), `"request_id":"req"`, `"request_id":null`, 1)),
		"nested-duplicate":  []byte(strings.Replace(string(valid), `{"private":"value"}`, `{"private":"value","private":"again"}`, 1)),
		"missing":           []byte(strings.Replace(string(valid), `"request_id":"req",`, ``, 1)),
		"sequence-overflow": []byte(strings.Replace(string(valid), `"sequence":1`, `"sequence":9007199254740992`, 1)),
		"data-null":         []byte(strings.Replace(string(valid), `{"private":"value"}`, `null`, 1)),
		"stream":            []byte(strings.Replace(string(valid), testStream, strings.Repeat("a", 32), 1)),
		"sequence":          []byte(strings.Replace(string(valid), `"sequence":1`, `"sequence":2`, 1)),
		"zero":              []byte(strings.Replace(string(valid), `"sequence":1`, `"sequence":0`, 1)),
		"oversize-data":     []byte(strings.Replace(string(valid), `"value"`, `"`+strings.Repeat("a", 49<<10)+`"`, 1)),
		"truncated":         []byte(strings.Repeat("a", maxPacket+1)),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			ep, c, d := rawPeers(t)
			if name == "truncated" {
				if _, _, e := ep.conn.WriteMsgUnix(b, nil, nil); e != nil {
					t.Fatal(e)
				}
			} else {
				sendRaw(t, ep, b)
			}
			if _, e := c.Next(context.Background()); e == nil {
				t.Fatal("accepted invalid wire")
			}
			if _, e := c.Next(context.Background()); e == nil {
				t.Fatal("reused invalid channel")
			}
			names, _ := os.ReadDir(d)
			if len(names) != 0 {
				t.Fatal("persisted invalid wire")
			}
		})
	}
}
func TestPeerAndAncillaryRejections(t *testing.T) {
	for _, mode := range []string{"pid", "uid", "rights", "missing"} {
		t.Run(mode, func(t *testing.T) {
			ep, c, _ := rawPeers(t)
			if mode == "pid" {
				c.ep.pid++
			}
			if mode == "uid" {
				c.ep.uid++
			}
			raw, _ := json.Marshal(wire{1, testStream, 1, sample()})
			switch mode {
			case "rights":
				f, e := os.Open("/dev/null")
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if _, _, e = ep.conn.WriteMsgUnix(raw, syscall.UnixRights(int(f.Fd())), nil); e != nil {
					t.Fatal(e)
				}
			case "missing":
				sc, _ := c.ep.conn.SyscallConn()
				if e := sc.Control(func(fd uintptr) {
					if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_PASSCRED, 0); e != nil {
						t.Error(e)
					}
				}); e != nil {
					t.Fatal(e)
				}
				sendRaw(t, ep, raw)
			default:
				sendRaw(t, ep, raw)
			}
			if _, e := c.Next(context.Background()); e == nil {
				t.Fatal("accepted invalid peer/ancillary")
			}
		})
	}
}
func TestBadACK(t *testing.T) {
	for _, field := range []string{"hash", "sequence", "stream", "decision", "duplicate", "unknown", "null", "peer"} {
		t.Run(field, func(t *testing.T) {
			p, c, _ := peers(t)
			if field == "peer" {
				p.ep.pid++
			}
			done := make(chan error, 1)
			go func() { _, e := p.Emit(context.Background(), sample()); done <- e }()
			ctx, finish, e := operation(context.Background(), c.ep.conn)
			if e != nil {
				t.Fatal(e)
			}
			raw, _, e := c.ep.receive(ctx)
			finish()
			if e != nil {
				t.Fatal(e)
			}
			h := sha256.Sum256(raw)
			a := ack{1, testStream, 1, hex.EncodeToString(h[:]), Continue}
			switch field {
			case "hash":
				a.WireSHA256 = strings.Repeat("0", 64)
			case "sequence":
				a.Sequence = 2
			case "stream":
				a.StreamID = strings.Repeat("a", 32)
			case "decision":
				a.Decision = "crash-cut"

			}
			b, _ := json.Marshal(a)
			switch field {
			case "duplicate":
				b = []byte(strings.Replace(string(b), `"version":1`, `"version":1,"version":1`, 1))
			case "unknown":
				b = []byte(strings.Replace(string(b), `"version":1`, `"version":1,"extra":0`, 1))
			case "null":
				b = []byte(strings.Replace(string(b), `"decision":"continue"`, `"decision":null`, 1))
			}
			sendRaw(t, c.ep, b)
			if e = <-done; e == nil {
				t.Fatal("accepted bad ack")
			}
			if _, e = p.Emit(context.Background(), sample()); e == nil {
				t.Fatal("reused invalid channel")
			}
		})
	}
}
func TestPersistenceFailureNoACK(t *testing.T) {
	for _, suffix := range []string{"wire", "receipt", "decision"} {
		t.Run(suffix, func(t *testing.T) {
			ep, c, d := rawPeers(t)
			if suffix != "decision" {
				if e := os.WriteFile(filepath.Join(d, "0000000000000001."+suffix+".json"), nil, 0600); e != nil {
					t.Fatal(e)
				}
			}
			raw, _ := json.Marshal(wire{1, testStream, 1, sample()})
			sendRaw(t, ep, raw)
			p, e := c.Next(context.Background())
			if suffix == "decision" {
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(d, "0000000000000001.decision.json"), nil, 0600); e != nil {
					t.Fatal(e)
				}
				if p.Decide(context.Background(), Continue) == nil {
					t.Fatal("decision persist succeeded")
				}
				if len(p.Originals()) != 2 {
					t.Fatal("false durable decision")
				}
			} else if e == nil {
				t.Fatal("persist succeeded")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			ctx, finish, e := operation(ctx, ep.conn)
			if e != nil {
				t.Fatal(e)
			}
			defer finish()
			if _, _, e = ep.receive(ctx); e == nil {
				t.Fatal("ACK after failed persist")
			}
		})
	}
}
func TestCancellationNilAndTimeout(t *testing.T) {
	var e error
	p, c, _ := peers(t)
	if _, e = p.Emit(nil, sample()); e == nil {
		t.Fatal("nil producer ctx")
	}
	if _, e = c.Next(nil); e == nil {
		t.Fatal("nil collector ctx")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = p.Emit(ctx, sample()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = c.Next(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := c.Next(ctx); done <- e }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second {
		t.Fatal("slow cancellation")
	}
	p, _, _ = peers(t)
	start = time.Now()
	if _, e := p.Emit(context.Background(), sample()); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if elapsed := time.Since(start); elapsed < 2900*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("unbounded timeout %s", elapsed)
	}
}
func TestCaptureAndFDPermissions(t *testing.T) {
	a, b := pair(t)
	for _, f := range []*os.File{a, b} {
		flags, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFD, 0)
		if e != 0 || flags&syscall.FD_CLOEXEC == 0 {
			t.Fatal("fd not cloexec")
		}
		pass, err := syscall.GetsockoptInt(int(f.Fd()), syscall.SOL_SOCKET, syscall.SO_PASSCRED)
		if err != nil || pass != 1 {
			t.Fatal("passcred not enabled")
		}
	}
	d := capture(t)
	bad := filepath.Join(d, "public")
	if e := os.Mkdir(bad, 0755); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(d, "link")
	if e := os.Symlink(d, link); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"relative", bad, link, filepath.Join(link, "public"), filepath.Join(d, "missing")} {
		if c, e := NewCollector(b, os.Getpid(), os.Getuid(), testStream, path); e == nil {
			c.Close()
			t.Fatal("invalid capture accepted")
		}
	}
	if _, e := NewProducer(nil, os.Getpid(), os.Getuid(), testStream); e == nil {
		t.Fatal("nil file")
	}
	if _, e := NewProducer(a, os.Getpid(), os.Getuid(), strings.ToUpper(testStream)); e == nil {
		t.Fatal("uppercase stream")
	}
	p, e := NewProducer(a, os.Getpid(), os.Getuid(), testStream)
	if e != nil {
		t.Fatal(e)
	}
	p.Close()
	if _, e := a.Stat(); e != nil {
		t.Fatal("constructor consumed caller file")
	}
}

func TestExactWireAndRepeatedSequence(t *testing.T) {
	ep, c, _ := rawPeers(t)
	raw, _ := json.Marshal(wire{1, testStream, 1, sample()})
	raw = append([]byte(" \n"), append(raw, []byte("\n ")...)...)
	sendRaw(t, ep, raw)
	p, e := c.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	saved, e := os.ReadFile(p.Originals()[0].Path)
	if e != nil || string(saved) != string(raw) {
		t.Fatal("wire transformed")
	}
	if e = p.Decide(context.Background(), Continue); e != nil {
		t.Fatal(e)
	}
	ctx, finish, e := operation(context.Background(), ep.conn)
	if e != nil {
		t.Fatal(e)
	}
	reply, _, e := ep.receive(ctx)
	finish()
	if e != nil {
		t.Fatal(e)
	}
	var a ack
	if json.Unmarshal(reply, &a) != nil {
		t.Fatal("ack invalid")
	}
	h := sha256.Sum256(raw)
	if a.WireSHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("ack not bound to exact wire")
	}
	sendRaw(t, ep, raw)
	if _, e = c.Next(context.Background()); e == nil {
		t.Fatal("repeated sequence accepted")
	}
}
func TestControlTruncationClosesReceivedRights(t *testing.T) {
	ep, c, _ := rawPeers(t)
	raw, _ := json.Marshal(wire{1, testStream, 1, sample()})
	f, e := os.Open("/dev/null")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	fds := make([]int, 128)
	for i := range fds {
		fds[i] = int(f.Fd())
	}
	before, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = ep.conn.WriteMsgUnix(raw, syscall.UnixRights(fds...), nil); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Next(context.Background()); e == nil {
		t.Fatal("control truncation accepted")
	}
	after, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	if len(after) != len(before) {
		t.Fatalf("received rights leaked: before=%d after=%d", len(before), len(after))
	}
}
func TestSerializedProducerCancellation(t *testing.T) {
	p, c, _ := peers(t)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := p.Emit(ctx, sample()); first <- e }()
	pending, e := c.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, e = p.Emit(ctx2, sample()); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	cancel()
	if e = <-first; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	checkRefs(t, pending, 2)
}
