//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/testsupport/p1trace"
)

var errP1TraceProcess = errors.New("P1_TRACE_PROCESS_FAILED")

type p1TraceDecision string

const (
	p1TraceContinue     p1TraceDecision = "continue"
	p1TraceInjectError  p1TraceDecision = "inject-error"
	p1TraceCrashManager p1TraceDecision = "crash-manager"
)

type p1TraceRecord struct {
	Event             p1trace.Event
	Originals         []originalRef
	OperationOriginal *originalRef
	CrashOriginal     *originalRef
	Sequence          uint64
	PeerPID, PeerUID  int
	// Decision is set only after ACK, or after a durable crash kill/wait receipt.
	Decision p1TraceDecision
}

func (p1TraceRecord) String() string   { return "p1TraceRecord{private}" }
func (p1TraceRecord) GoString() string { return "p1TraceRecord{private}" }
func copyP1TraceRecord(r p1TraceRecord) p1TraceRecord {
	r.Event.Data = bytes.Clone(r.Event.Data)
	r.Originals = append([]originalRef(nil), r.Originals...)
	if r.OperationOriginal != nil {
		x := *r.OperationOriginal
		r.OperationOriginal = &x
	}
	if r.CrashOriginal != nil {
		x := *r.CrashOriginal
		r.CrashOriginal = &x
	}
	return r
}

// p1TracedProcess supervises only its direct exec child. These same-UID test
// fixtures are not hostile executable-path/mount isolation. No discovered PID
// is signalled, and core descendants are never cleaned up by this helper.
type p1TracedProcess struct {
	cmd                         *exec.Cmd
	collector                   *p1trace.Collector
	socket, dir, stdout, stderr *os.File
	captures                    string
	paths                       config.Paths
	decide                      func(p1TraceRecord) p1TraceDecision
	cancel                      context.CancelFunc
	waitDone, traceDone         chan struct{}
	closeOnce                   sync.Once
	mu                          sync.RWMutex
	records                     []p1TraceRecord
	waitErr, traceErr           error
}

func (*p1TracedProcess) String() string       { return "p1TracedProcess{private}" }
func (*p1TracedProcess) GoString() string     { return "p1TracedProcess{private}" }
func (p *p1TracedProcess) PID() int           { return p.cmd.Process.Pid }
func (p *p1TracedProcess) StdoutPath() string { return filepath.Join(p.captures, "stdout.raw") }
func (p *p1TracedProcess) StderrPath() string { return filepath.Join(p.captures, "stderr.raw") }
func (p *p1TracedProcess) Records() []p1TraceRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]p1TraceRecord, len(p.records))
	for i, r := range p.records {
		out[i] = copyP1TraceRecord(r)
	}
	return out
}
func (p *p1TracedProcess) TraceError() error { p.mu.RLock(); defer p.mu.RUnlock(); return p.traceErr }
func (p *p1TracedProcess) fail()             { p.mu.Lock(); p.traceErr = errP1TraceProcess; p.mu.Unlock() }

// Wait waits for both the direct child and the collector, retaining ExitError.
// ctx can bound the caller's wait, but cannot preempt in-flight filesystem sync.
func (p *p1TracedProcess) Wait(ctx context.Context) error {
	if ctx == nil {
		return errP1TraceProcess
	}
	select {
	case <-p.waitDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-p.traceDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.waitErr != nil {
		return p.waitErr
	}
	return p.traceErr
}
func (p *p1TracedProcess) Kill() error {
	if p == nil || p.cmd.Process == nil {
		return errP1TraceProcess
	}
	if e := p.cmd.Process.Kill(); e != nil && !errors.Is(e, os.ErrProcessDone) {
		return errP1TraceProcess
	}
	return nil
}

// Close cancels collection and kills only this helper's direct child, then waits
// for reaping and collector cleanup. A context expiry does not interrupt sync;
// the cleanup goroutine continues and callers can wait again.
func (p *p1TracedProcess) Close(ctx context.Context) error {
	if p == nil || ctx == nil {
		return errP1TraceProcess
	}
	p.closeOnce.Do(func() { p.cancel(); _ = p.Kill() })
	e := p.Wait(ctx)
	if errors.Is(e, ctx.Err()) && ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) {
		return p.TraceError()
	}
	return e
}

func p1TraceCaptureDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errP1TraceProcess
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, errP1TraceProcess
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if err != nil {
			return nil, errP1TraceProcess
		}
		fd = next
	}
	var st syscall.Stat_t
	if syscall.Fstat(fd, &st) != nil || st.Uid != uint32(os.Getuid()) || st.Mode&07777 != 0700 {
		syscall.Close(fd)
		return nil, errP1TraceProcess
	}
	d := os.NewFile(uintptr(fd), "private-trace-directory")
	entries, e := d.Readdirnames(1)
	if len(entries) != 0 || e != io.EOF {
		d.Close()
		return nil, errP1TraceProcess
	}
	return d, nil
}
func p1TraceCreate(dir *os.File, name string) (*os.File, error) {
	fd, e := syscall.Openat(int(dir.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, errP1TraceProcess
	}
	return os.NewFile(uintptr(fd), "private-trace-output"), nil
}
func (p *p1TracedProcess) original(name string, raw []byte) (originalRef, error) {
	f, e := p1TraceCreate(p.dir, name)
	if e != nil {
		return originalRef{}, errP1TraceProcess
	}
	n, e := f.Write(raw)
	if e == nil && n == len(raw) {
		e = f.Sync()
	} else {
		e = errP1TraceProcess
	}
	ce := f.Close()
	if e != nil || ce != nil || p.dir.Sync() != nil {
		return originalRef{}, errP1TraceProcess
	}
	return originalRef{Path: filepath.Join(p.captures, name), Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, nil
}

// startP1TracedProcess launches an explicit absolute pinned executable directly,
// never through a shell, PATH lookup or CommandContext. args omit the generated
// trace prefix; env is explicit and does not inherit the parent's environment.
func startP1TracedProcess(executable string, args, env []string, stdin []byte, paths config.Paths, captureDir string, decide func(p1TraceRecord) p1TraceDecision) (*p1TracedProcess, error) {
	info, pin, e := p1CLIExecutable(executable)
	if e != nil || paths.Validate() != nil || len(args) == 0 {
		return nil, errP1TraceProcess
	}
	d, e := p1TraceCaptureDirectory(captureDir)
	if e != nil {
		return nil, e
	}
	owned := true
	defer func() {
		if owned {
			d.Close()
		}
	}()
	out, e := p1TraceCreate(d, "stdout.raw")
	if e != nil {
		return nil, e
	}
	defer func() {
		if owned {
			out.Close()
		}
	}()
	stderr, e := p1TraceCreate(d, "stderr.raw")
	if e != nil {
		return nil, e
	}
	defer func() {
		if owned {
			stderr.Close()
		}
	}()
	a, b, e := p1trace.SocketPair()
	if e != nil {
		return nil, errP1TraceProcess
	}
	defer b.Close()
	defer func() {
		if owned {
			a.Close()
		}
	}()
	var token [16]byte
	if _, e = rand.Read(token[:]); e != nil {
		return nil, errP1TraceProcess
	}
	stream := hex.EncodeToString(token[:])
	argv := append([]string{"trace", stream, strconv.Itoa(os.Getpid())}, args...)
	fresh, freshPin, e := p1CLIExecutable(executable)
	if e != nil || !os.SameFile(info, fresh) || pin != freshPin {
		return nil, errP1TraceProcess
	}
	cmd := exec.Command(executable, argv...)
	cmd.Env = append([]string{}, env...)
	cmd.Stdin = bytes.NewReader(bytes.Clone(stdin))
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{b}
	if e = cmd.Start(); e != nil {
		return nil, errP1TraceProcess
	}
	b.Close()
	collector, e := p1trace.NewCollector(a, cmd.Process.Pid, os.Getuid(), stream, captureDir)
	if e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, errP1TraceProcess
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &p1TracedProcess{cmd: cmd, collector: collector, socket: a, dir: d, stdout: out, stderr: stderr, captures: captureDir, paths: paths, decide: decide, cancel: cancel, waitDone: make(chan struct{}), traceDone: make(chan struct{})}
	owned = false
	go func() { e := cmd.Wait(); p.mu.Lock(); p.waitErr = e; p.mu.Unlock(); close(p.waitDone) }()
	go p.collect(ctx)
	return p, nil
}

// Readiness never consumes a packet. POLLIN is also set for EOF on Linux, so
// peek distinguishes a queued packet from the terminal zero-byte read. Peek
// rights are CLOEXEC and closed; zero packets carrying control are rejected.
func p1TraceReady(socket *os.File) (packet, hangup bool, err error) {
	raw, e := socket.SyscallConn()
	if e != nil {
		return false, false, errP1TraceProcess
	}
	var pollErr error
	e = raw.Control(func(fd uintptr) {
		poll := struct {
			FD              int32
			Events, Revents int16
		}{FD: int32(fd), Events: 1}
		_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 0)
		if errno == syscall.EINTR {
			return
		}
		if errno != 0 {
			pollErr = errP1TraceProcess
			return
		}
		if poll.Revents&(8|32) != 0 {
			pollErr = errP1TraceProcess
			return
		}
		hangup = poll.Revents&16 != 0
		if poll.Revents&1 == 0 {
			return
		}
		buf := make([]byte, 1)
		oob := make([]byte, 4096)
		n, on, _, _, peekErr := syscall.Recvmsg(int(fd), buf, oob, syscall.MSG_PEEK|syscall.MSG_DONTWAIT|syscall.MSG_TRUNC|syscall.MSG_CMSG_CLOEXEC)
		cms, parseErr := syscall.ParseSocketControlMessage(oob[:on])
		if parseErr != nil {
			pollErr = errP1TraceProcess
		}
		for _, m := range cms {
			if m.Header.Level == syscall.SOL_SOCKET && m.Header.Type == syscall.SCM_RIGHTS {
				fds, e := syscall.ParseUnixRights(&m)
				if e != nil {
					pollErr = errP1TraceProcess
				}
				for _, got := range fds {
					syscall.Close(got)
				}
			}
		}
		if peekErr == syscall.EAGAIN || peekErr == syscall.EINTR {
			hangup = false // A transient peek must never discard a final queued packet.
			return
		}
		if peekErr != nil {
			pollErr = errP1TraceProcess
			return
		}
		packet = n > 0
		if n == 0 && (!hangup || on > 0) {
			pollErr = errP1TraceProcess
		}
	})
	if e != nil || pollErr != nil {
		return false, false, errP1TraceProcess
	}
	return packet, hangup, nil
}

type p1TraceWrapper struct {
	Injectable bool            `json:"injectable"`
	Detail     json.RawMessage `json:"detail"`
}

func p1TraceUnwrap(raw []byte) (p1TraceWrapper, error) {
	var w p1TraceWrapper
	if _, e := config.CanonicalJSON(raw); e != nil {
		return w, errP1TraceProcess
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || len(m) != 2 || m["injectable"] == nil || m["detail"] == nil || bytes.Equal(bytes.TrimSpace(m["injectable"]), []byte("null")) {
		return w, errP1TraceProcess
	}
	if json.Unmarshal(raw, &w) != nil {
		return w, errP1TraceProcess
	}
	var detail map[string]json.RawMessage
	if json.Unmarshal(w.Detail, &detail) != nil || detail == nil {
		return w, errP1TraceProcess
	}
	return w, nil
}
func traceOriginals(pending *p1trace.Pending) []originalRef {
	out := []originalRef{}
	for _, r := range pending.Originals() {
		out = append(out, originalRef{Path: r.Path, Bytes: r.Bytes, SHA256: r.SHA256})
	}
	return out
}
func (p *p1TracedProcess) journalOriginal(record p1TraceRecord, w p1TraceWrapper) (*originalRef, error) {
	if record.Event.Component != "journal" || record.Event.Point != "published" {
		return nil, nil
	}
	var detail struct {
		RecordSHA256 string `json:"record_sha256"`
	}
	if json.Unmarshal(w.Detail, &detail) != nil || len(detail.RecordSHA256) != 64 {
		return nil, errP1TraceProcess
	}
	s, e := config.OpenStore(p.paths, false)
	if e != nil {
		return nil, errP1TraceProcess
	}
	raw, hash, e := s.ReadOperationFile(config.ID(record.Event.OperationID))
	ce := s.Close()
	if e != nil || ce != nil || hash != detail.RecordSHA256 || p1SHA(raw) != hash {
		return nil, errP1TraceProcess
	}
	r, e := p.original(fmt.Sprintf("%016d.operation.json", record.Sequence), raw)
	if e != nil {
		return nil, e
	}
	return &r, nil
}
func (p *p1TracedProcess) collect(ctx context.Context) {
	defer close(p.traceDone)
	defer func() {
		p.collector.Close()
		p.socket.Close()
		<-p.waitDone
		for _, f := range []*os.File{p.stdout, p.stderr} {
			if f.Sync() != nil {
				p.fail()
			}
			if f.Close() != nil {
				p.fail()
			}
		}
		if p.dir.Sync() != nil {
			p.fail()
		}
		if p.dir.Close() != nil {
			p.fail()
		}
	}()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		ready, hup, e := p1TraceReady(p.socket)
		if e != nil {
			p.fail()
			_ = p.Kill()
			return
		}
		if ready {
			if e := p.collectOne(ctx); e != nil {
				if ctx.Err() == nil || !errors.Is(e, ctx.Err()) {
					p.fail()
				}
				_ = p.Kill()
				return
			}
			continue
		}
		if hup {
			<-p.waitDone
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (p *p1TracedProcess) collectOne(ctx context.Context) error {
	pending, e := p.collector.Next(ctx)
	if e != nil {
		return e
	}
	record := p1TraceRecord{Event: pending.Event(), Originals: traceOriginals(pending), Sequence: pending.Sequence(), PeerPID: pending.PeerPID(), PeerUID: pending.PeerUID()}
	w, e := p1TraceUnwrap(record.Event.Data)
	p.mu.Lock()
	index := len(p.records)
	p.records = append(p.records, copyP1TraceRecord(record))
	p.mu.Unlock()
	defer func() {
		record.Originals = traceOriginals(pending)
		if record.OperationOriginal != nil {
			record.Originals = append(record.Originals, *record.OperationOriginal)
		}
		if record.CrashOriginal != nil {
			record.Originals = append(record.Originals, *record.CrashOriginal)
		}
		p.mu.Lock()
		p.records[index] = copyP1TraceRecord(record)
		p.mu.Unlock()
	}()
	if e != nil {
		return e
	}
	record.OperationOriginal, e = p.journalOriginal(record, w)
	if e != nil {
		return e
	}
	if record.OperationOriginal != nil {
		record.Originals = append(record.Originals, *record.OperationOriginal)
	}
	decision := p1TraceContinue
	if p.decide != nil {
		decision = p.decide(copyP1TraceRecord(record))
	}
	switch decision {
	case p1TraceContinue:
		if e = pending.Decide(ctx, p1trace.Continue); e != nil {
			return e
		}
		record.Decision = decision
		return nil
	case p1TraceInjectError:
		if !w.Injectable {
			return errP1TraceProcess
		}
		if e = pending.Decide(ctx, p1trace.InjectError); e != nil {
			return e
		}
		record.Decision = decision
		return nil
	case p1TraceCrashManager:
		if e = pending.RecordCrashCut(); e != nil {
			return e
		}
		if e = p.cmd.Process.Kill(); e != nil {
			return errP1TraceProcess
		}
		<-p.waitDone
		p.mu.RLock()
		waitErr := p.waitErr
		p.mu.RUnlock()
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return errP1TraceProcess
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			return errP1TraceProcess
		}
		raw, _ := json.Marshal(struct {
			PID        int    `json:"pid"`
			Sequence   uint64 `json:"sequence"`
			KillSent   bool   `json:"kill_sent"`
			WaitSignal string `json:"wait_signal"`
		}{p.PID(), record.Sequence, true, "SIGKILL"})
		receipt, e := p.original(fmt.Sprintf("%016d.kill-receipt.json", record.Sequence), raw)
		if e != nil {
			return errP1TraceProcess
		}
		record.CrashOriginal = &receipt
		record.Decision = decision
		return nil
	default:
		return errP1TraceProcess
	}
}

// Explicit test-binary entry only. This environment marker exists solely in
// this _test.go fixture and never enables a product hook or harness factory.
func init() {
	mode := os.Getenv("HOOPA_P1_TRACE_PROCESS_DRIVER")
	if mode == "" || len(os.Args) != 5 || os.Args[1] != "trace" || os.Args[4] != "driver" {
		return
	}
	pid, e := strconv.Atoi(os.Args[3])
	if e != nil || pid != os.Getppid() {
		os.Exit(80)
	}
	f := os.NewFile(3, "trace-driver")
	if mode == "bad-wire" {
		if _, e := syscall.Write(int(f.Fd()), []byte("{invalid-json")); e != nil {
			os.Exit(89)
		}
		f.Close()
		os.Exit(0)
	}
	producer, e := p1trace.NewProducer(f, pid, os.Getuid(), os.Args[2])
	f.Close()
	if e != nil {
		os.Exit(81)
	}
	defer producer.Close()
	if mode == "exit" {
		os.Exit(7)
	}
	event := func(point string, injectable bool) p1trace.Event {
		raw, _ := json.Marshal(p1TraceWrapper{injectable, json.RawMessage(`{"driver":true}`)})
		return p1trace.Event{Component: "driver", Point: point, Data: raw}
	}
	if mode == "bad-wrapper" {
		v := event("one", false)
		v.Data = []byte(`{"injectable":false,"detail":null}`)
		_, _ = producer.Emit(context.Background(), v)
		os.Exit(82)
	}
	if mode == "journal" || mode == "bad-journal" {
		hash := os.Getenv("HOOPA_P1_TRACE_PROCESS_HASH")
		if mode == "bad-journal" {
			hash = strings.Repeat("0", 64)
		}
		raw, _ := json.Marshal(p1TraceWrapper{false, json.RawMessage(fmt.Sprintf(`{"record_sha256":%q}`, hash))})
		_, e = producer.Emit(context.Background(), p1trace.Event{Component: "journal", Point: "published", OperationID: os.Getenv("HOOPA_P1_TRACE_PROCESS_OP"), Data: raw})
		if e != nil {
			os.Exit(83)
		}
		os.Exit(0)
	}
	d, e := producer.Emit(context.Background(), event("one", mode == "inject"))
	if e != nil {
		os.Exit(84)
	}
	if mode == "inject" && d != p1trace.InjectError {
		os.Exit(85)
	}
	if mode == "idle" {
		time.Sleep(3300 * time.Millisecond)
	}
	if mode == "linger" {
		time.Sleep(30 * time.Second)
	}
	_, e = producer.Emit(context.Background(), event("two", false))
	if e != nil {
		os.Exit(86)
	}
	_, _ = os.Stdout.WriteString("private-driver-output\n")
	_, _ = os.Stderr.WriteString("private-driver-stderr\n")
	os.Exit(0)
}

func p1TraceDriverFixture(t *testing.T) (string, config.Paths, string) {
	t.Helper()
	base, e := os.MkdirTemp("/tmp", "hoopa-p1-trace-process-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	paths := config.Paths{Config: filepath.Join(base, "config"), State: filepath.Join(base, "state"), Cache: filepath.Join(base, "cache"), Runtime: filepath.Join(base, "runtime")}
	s, e := config.OpenStore(paths, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	d := filepath.Join(base, "capture")
	if e = os.Mkdir(d, 0700); e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return exe, paths, d
}
func p1TraceDriverStart(t *testing.T, mode string, decide func(p1TraceRecord) p1TraceDecision) (*p1TracedProcess, config.Paths, string) {
	t.Helper()
	exe, paths, d := p1TraceDriverFixture(t)
	p, e := startP1TracedProcess(exe, []string{"driver"}, []string{"HOOPA_P1_TRACE_PROCESS_DRIVER=" + mode}, nil, paths, d, decide)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Close(ctx)
	})
	return p, paths, d
}
func p1TraceDriverWait(t *testing.T, p *p1TracedProcess) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return p.Wait(ctx)
}
func p1TraceDriverRefs(t *testing.T, records []p1TraceRecord) {
	t.Helper()
	for _, r := range records {
		for _, o := range r.Originals {
			raw, e := os.ReadFile(o.Path)
			if e != nil {
				t.Fatal(e)
			}
			if int64(len(raw)) != o.Bytes || p1SHA(raw) != o.SHA256 {
				t.Fatal("original mismatch")
			}
			st, e := os.Stat(o.Path)
			if e != nil || st.Mode().Perm() != 0600 {
				t.Fatal("private original mode")
			}
		}
	}
}
func TestP1TraceProcessDriverNormalAndIdle(t *testing.T) {
	for _, mode := range []string{"normal", "idle"} {
		t.Run(mode, func(t *testing.T) {
			p, _, _ := p1TraceDriverStart(t, mode, nil)
			if e := p1TraceDriverWait(t, p); e != nil {
				t.Fatal(e)
			}
			if p.TraceError() != nil {
				t.Fatal(p.TraceError())
			}
			r := p.Records()
			if len(r) != 2 || r[0].Sequence != 1 || r[1].Sequence != 2 || r[0].PeerPID != p.PID() || p.PID() == os.Getpid() {
				t.Fatal("actual child records missing")
			}
			for _, record := range r {
				if record.Decision != p1TraceContinue || len(record.Originals) != 3 {
					t.Fatal("decision original missing")
				}
			}
			p1TraceDriverRefs(t, r)
			r[0].Event.Data[0] = 'x'
			r[0].Originals[0].Path = "changed"
			if p.Records()[0].Event.Data[0] == 'x' || p.Records()[0].Originals[0].Path == "changed" {
				t.Fatal("records alias")
			}
			for _, path := range []string{p.StdoutPath(), p.StderrPath()} {
				raw, e := os.ReadFile(path)
				if e != nil || !bytes.Contains(raw, []byte("private-driver")) {
					t.Fatal("missing private diagnostics")
				}
			}
			if strings.Contains(fmt.Sprintf("%v %#v %v", p, p, p.Records()[0]), "private-driver") {
				t.Fatal("private formatter")
			}
		})
	}
}
func TestP1TraceProcessDriverDecisions(t *testing.T) {
	t.Run("inject", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "inject", func(r p1TraceRecord) p1TraceDecision {
			if r.Event.Point == "one" {
				return p1TraceInjectError
			}
			return p1TraceContinue
		})
		if e := p1TraceDriverWait(t, p); e != nil {
			t.Fatal(e)
		}
		if p.Records()[0].Decision != p1TraceInjectError {
			t.Fatal("injection unobserved")
		}
	})
	t.Run("noninjectable", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "normal", func(p1TraceRecord) p1TraceDecision { return p1TraceInjectError })
		if e := p1TraceDriverWait(t, p); e == nil || p.TraceError() == nil {
			t.Fatal("invalid injection accepted")
		}
		r := p.Records()
		if len(r) != 1 || r[0].Decision != "" || len(r[0].Originals) != 2 {
			t.Fatal("false ACK")
		}
	})
	t.Run("crash", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "normal", func(p1TraceRecord) p1TraceDecision { return p1TraceCrashManager })
		e := p1TraceDriverWait(t, p)
		var exit *exec.ExitError
		if !errors.As(e, &exit) {
			t.Fatal("exit error lost")
		}
		st, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !st.Signaled() || st.Signal() != syscall.SIGKILL || p.TraceError() != nil {
			t.Fatal("kill not confirmed")
		}
		r := p.Records()
		if len(r) != 1 || r[0].Decision != p1TraceCrashManager || r[0].CrashOriginal == nil || len(r[0].Originals) != 4 {
			t.Fatal("durable actual kill receipt missing")
		}
		p1TraceDriverRefs(t, r)
		raw, e := os.ReadFile(r[0].CrashOriginal.Path)
		if e != nil {
			t.Fatal(e)
		}
		var receipt struct {
			PID        int    `json:"pid"`
			KillSent   bool   `json:"kill_sent"`
			WaitSignal string `json:"wait_signal"`
		}
		if json.Unmarshal(raw, &receipt) != nil || receipt.PID != p.PID() || !receipt.KillSent || receipt.WaitSignal != "SIGKILL" {
			t.Fatal("kill receipt mismatch")
		}
	})
}
func TestP1TraceProcessDriverJournalCapture(t *testing.T) {
	for _, mode := range []string{"journal", "bad-journal"} {
		t.Run(mode, func(t *testing.T) {
			exe, paths, d := p1TraceDriverFixture(t)
			s, e := config.OpenStore(paths, true)
			if e != nil {
				t.Fatal(e)
			}
			id := config.ID("op-" + strings.Repeat("a", 64))
			raw := []byte(`{"opaque":"private-driver-journal"}`)
			hash, e := s.CompareAndSwapOperation(id, "", raw)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			p, e := startP1TracedProcess(exe, []string{"driver"}, []string{"HOOPA_P1_TRACE_PROCESS_DRIVER=" + mode, "HOOPA_P1_TRACE_PROCESS_OP=" + string(id), "HOOPA_P1_TRACE_PROCESS_HASH=" + hash}, nil, paths, d, func(r p1TraceRecord) p1TraceDecision {
				if mode == "journal" && r.OperationOriginal == nil {
					t.Error("journal not captured before decision")
				}
				return p1TraceContinue
			})
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close(context.Background())
			e = p1TraceDriverWait(t, p)
			r := p.Records()
			if len(r) != 1 {
				t.Fatal("journal event missing")
			}
			if mode == "journal" {
				if e != nil || p.TraceError() != nil || r[0].OperationOriginal == nil || r[0].OperationOriginal.SHA256 != hash || len(r[0].Originals) != 4 {
					t.Fatal("journal capture failed")
				}
				saved, er := os.ReadFile(r[0].OperationOriginal.Path)
				if er != nil || !bytes.Equal(saved, raw) {
					t.Fatal("journal transformed")
				}
				p1TraceDriverRefs(t, r)
			} else {
				if e == nil || p.TraceError() == nil || r[0].OperationOriginal != nil || r[0].Decision != "" || len(r[0].Originals) != 2 {
					t.Fatal("false journal capture")
				}
			}
		})
	}
}
func TestP1TraceProcessDriverCleanupAndErrors(t *testing.T) {
	t.Run("cleanup", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "linger", nil)
		deadline := time.Now().Add(3 * time.Second)
		for len(p.Records()) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := p.Close(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Stat(filepath.Join("/proc", strconv.Itoa(p.PID()))); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("direct child not reaped")
		}
		select {
		case <-p.traceDone:
		default:
			t.Fatal("collector not stopped")
		}
	})
	t.Run("abnormal-exit", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "exit", nil)
		e := p1TraceDriverWait(t, p)
		var exit *exec.ExitError
		if !errors.As(e, &exit) || exit.ExitCode() != 7 || p.TraceError() != nil {
			t.Fatal("exit status lost")
		}
	})
	t.Run("bad-wrapper", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "bad-wrapper", nil)
		if e := p1TraceDriverWait(t, p); e == nil || p.TraceError() == nil {
			t.Fatal("invalid wrapper accepted")
		}
	})
	t.Run("wait-context", func(t *testing.T) {
		p, _, _ := p1TraceDriverStart(t, "linger", nil)
		if p.Wait(nil) == nil {
			t.Fatal("nil context")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := p.Wait(ctx); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	})
	t.Run("invalid-start", func(t *testing.T) {
		exe, paths, d := p1TraceDriverFixture(t)
		if p, e := startP1TracedProcess("relative", []string{"driver"}, nil, nil, paths, d, nil); e == nil {
			p.Close(context.Background())
			t.Fatal("relative executable")
		}
		if e := os.WriteFile(filepath.Join(d, "existing"), nil, 0600); e != nil {
			t.Fatal(e)
		}
		if p, e := startP1TracedProcess(exe, []string{"driver"}, nil, nil, paths, d, nil); e == nil {
			p.Close(context.Background())
			t.Fatal("nonempty capture")
		}
	})
}

func TestP1TraceProcessDriverMalformedFinalPacket(t *testing.T) {
	p, _, _ := p1TraceDriverStart(t, "bad-wire", nil)
	if e := p1TraceDriverWait(t, p); e == nil || p.TraceError() == nil {
		t.Fatal("invalid final packet treated as EOF")
	}
	if len(p.Records()) != 0 {
		t.Fatal("invalid packet became event")
	}
}
func TestP1TraceProcessDriverUnmatchedCrashSelector(t *testing.T) {
	p, _, _ := p1TraceDriverStart(t, "normal", func(r p1TraceRecord) p1TraceDecision {
		if r.Event.Point == "absent-cut" {
			return p1TraceCrashManager
		}
		return p1TraceContinue
	})
	if e := p1TraceDriverWait(t, p); e != nil {
		t.Fatal(e)
	}
	for _, r := range p.Records() {
		if r.Decision == p1TraceCrashManager || r.CrashOriginal != nil {
			t.Fatal("unmatched fault reported as crash")
		}
	}
}
func TestP1TraceProcessDriverReadinessRejectsZeroPacket(t *testing.T) {
	a, b, e := p1trace.SocketPair()
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = syscall.Sendmsg(int(b.Fd()), nil, nil, nil, 0); e != nil {
		b.Close()
		t.Fatal(e)
	}
	b.Close()
	if _, _, e = p1TraceReady(a); e == nil {
		t.Fatal("zero packet followed by EOF accepted")
	}
}
