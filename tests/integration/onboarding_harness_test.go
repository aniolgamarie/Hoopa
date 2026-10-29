//go:build linux && amd64 && us4integration && onboardingintegration

package integration

// T054 init is not implemented: settings/app bindings/provider history below
// are explicit prerequisites, not a shipped CLI onboarding operation.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/service"
)

type onboardingReply struct {
	APIVersion string          `json:"api_version"`
	RequestID  any             `json:"request_id"`
	OK         bool            `json:"ok"`
	Data       json.RawMessage `json:"data"`
	Error      *struct {
		Code    string                     `json:"code"`
		Details map[string]json.RawMessage `json:"details"`
	} `json:"error"`
	Warnings []json.RawMessage `json:"warnings"`
	raw      []byte
	code     int
}
type onboardingOutput struct{ bytes.Buffer }

func (w *onboardingOutput) Write(p []byte) (int, error) {
	if len(p) > 1<<20-w.Len() {
		return 0, errors.New("bounded CLI output exceeded")
	}
	return w.Buffer.Write(p)
}

type onboardingCLI struct {
	t                     *testing.T
	path, sha, home, logs string
	paths                 config.Paths
	serial                int
	secrets               []string
	network               func() int64
}

func newOnboardingCLI(t *testing.T, root, home string, paths config.Paths) *onboardingCLI {
	t.Helper()
	source, want := os.Getenv("HOOPA_T139_CLI"), os.Getenv("HOOPA_T139_CLI_SHA256")
	decoded, e := hex.DecodeString(want)
	if source == "" || !filepath.IsAbs(source) || e != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != want {
		t.Fatal("explicit pinned cmd/hoopa binary+SHA required; cannot skip")
	}
	f, e := os.Open(source)
	appMust(t, e)
	defer f.Close()
	info, e := f.Stat()
	appMust(t, e)
	if !info.Mode().IsRegular() || info.Size() > 128<<20 {
		t.Fatal("CLI artifact must be bounded regular binary")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 128<<20+1))
	appMust(t, e)
	if len(raw) > 128<<20 || appSHA(raw) != want {
		t.Fatal("CLI artifact SHA mismatch")
	}
	compiled, e := buildinfo.ReadFile(source)
	if e != nil || compiled.Path != "hoopa/cmd/hoopa" {
		t.Fatal("CLI must be independently built cmd/hoopa, not a test-dispatch image")
	}
	path := filepath.Join(root, "hoopa-cli")
	out, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	appMust(t, e)
	_, e = out.Write(raw)
	appMust(t, e)
	appMust(t, out.Close())
	st, e := os.Lstat(path)
	appMust(t, e)
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || native.Nlink != 1 || st.Mode().Perm() != 0700 {
		t.Fatal("CLI copy not private single-link")
	}
	return &onboardingCLI{t: t, path: path, sha: want, home: home, logs: root, paths: paths}
}
func (c *onboardingCLI) env() []string {
	return []string{"HOME=" + c.home, "XDG_CONFIG_HOME=" + filepath.Dir(c.paths.Config), "XDG_STATE_HOME=" + filepath.Dir(c.paths.State), "XDG_CACHE_HOME=" + filepath.Dir(c.paths.Cache), "XDG_RUNTIME_DIR=" + filepath.Dir(c.paths.Runtime), "TMPDIR=" + c.logs, "LANG=C", "PATH=/bin:/sbin"}
}
func (c *onboardingCLI) invoke(input []byte, args ...string) onboardingReply {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Env = c.env()
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr onboardingOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			c.t.Fatal("actual CLI subprocess failed to execute")
		}
		code = exit.ExitCode()
	}
	c.serial++
	for suffix, raw := range map[string][]byte{"stdout": stdout.Bytes(), "stderr": stderr.Bytes()} {
		appMust(c.t, os.WriteFile(filepath.Join(c.logs, fmt.Sprintf("cli-%03d.%s", c.serial, suffix)), raw, 0600))
		for _, secret := range c.secrets {
			if secret != "" && bytes.Contains(raw, []byte(secret)) {
				c.t.Fatal("CLI output leaked private fixture material")
			}
		}
	}
	if ctx.Err() != nil {
		c.t.Fatal("CLI deadline is not a valid rejection/cleanup witness")
	}
	if code < 0 || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		c.t.Fatal("CLI signal termination is not a valid rejection witness")
	}
	var r onboardingReply
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || r.APIVersion != "v1" {
		c.t.Fatal("CLI did not emit exactly one response envelope", code)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(stdout.Bytes(), &fields)
	if len(fields) != 6 || r.OK != (code == 0) || r.OK != (r.Error == nil) || r.Warnings == nil {
		c.t.Fatal("CLI response/exit inconsistency", code)
	}
	r.raw = bytes.Clone(stdout.Bytes())
	r.code = code
	c.t.Logf("actual pinned cmd/hoopa invocation=%d exit=%d", c.serial, code)
	return r
}
func (c *onboardingCLI) ok(input []byte, args ...string) onboardingReply {
	c.t.Helper()
	r := c.invoke(input, args...)
	if !r.OK {
		c.t.Fatal("required real CLI operation unavailable", args[0], r.Error.Code)
	}
	return r
}
func (c *onboardingCLI) tree() map[string]string {
	c.t.Helper()
	result := map[string]string{}
	roots := map[string]string{"home": c.home, "config": filepath.Dir(c.paths.Config), "state": filepath.Dir(c.paths.State), "cache": filepath.Dir(c.paths.Cache), "runtime": filepath.Dir(c.paths.Runtime)}
	for scope, root := range roots {
		if _, e := os.Lstat(root); errors.Is(e, os.ErrNotExist) {
			result[scope] = "absent"
			continue
		}
		appMust(c.t, filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			if info.Mode().IsRegular() {
				raw, e := os.ReadFile(path)
				if e != nil {
					return e
				}
				value += ":" + appSHA(raw)
			}
			result[scope+"/"+rel] = value
			return nil
		}))
	}
	return result
}
func (c *onboardingCLI) readonly(input []byte, args ...string) onboardingReply {
	c.t.Helper()
	before := c.tree()
	var networkBefore int64
	if c.network != nil {
		networkBefore = c.network()
	}
	r := c.invoke(input, args...)
	if !reflect.DeepEqual(before, c.tree()) {
		c.t.Fatal("query/local Plan/rejection changed internal tree")
	}
	if c.network != nil && c.network() != networkBefore {
		c.t.Fatal("readonly command triggered fixture business network/probe traffic")
	}
	return r
}
func onboardingDoctor(t *testing.T, r onboardingReply, stage string) {
	t.Helper()
	if !r.OK {
		t.Fatal("doctor failed instead of successful diagnostic", r.Error.Code)
	}
	var data struct {
		SchemaVersion int                          `json:"schema_version"`
		ObservedAt    string                       `json:"observed_at"`
		Checks        []map[string]json.RawMessage `json:"checks"`
	}
	appMust(t, json.Unmarshal(r.Data, &data))
	observed, e := time.Parse(time.RFC3339Nano, data.ObservedAt)
	if e != nil || observed.IsZero() || data.SchemaVersion != 1 || len(data.Checks) != 6 {
		t.Fatal("doctor data missing finite fields")
	}
	for i, id := range []string{"state", "configuration", "service", "core", "collector", "operations"} {
		row := data.Checks[i]
		if len(row) != 5 {
			t.Fatal("doctor check shape")
		}
		stringsByKey := map[string]string{}
		for _, key := range []string{"id", "result", "evidence", "reason", "recommendation"} {
			var value string
			if bytes.Equal(row[key], []byte("null")) || json.Unmarshal(row[key], &value) != nil {
				t.Fatal("doctor check missing scalar field", id, key)
			}
			stringsByKey[key] = value
		}
		if stringsByKey["id"] != id {
			t.Fatal("doctor check order")
		}
		switch stringsByKey["result"] {
		case "pass", "warning", "unavailable":
		default:
			t.Fatal("doctor invalid result")
		}
		switch stringsByKey["evidence"] {
		case "protected_local_files", "durable_operation_journal", "current_management_service", "fresh_core_identity", "not_observed":
		default:
			t.Fatal("doctor invented evidence category")
		}
		// Exact finite values from doctor-v1.schema.json; arbitrary strings
		// must never be accepted as diagnostic reason or advice evidence.
		switch stringsByKey["reason"] {
		case "state_readable", "committed_snapshot", "service_unavailable", "ownership_unverified", "not_implemented", "no_pending_operations", "no_committed_configuration", "service_running", "service_quiesced", "fresh_original_proof", "empty_configuration", "durable_original_exit", "activation_requires_management", "pending_operations", "state_unavailable", "state_missing_or_incomplete":
		default:
			t.Fatal("doctor reason outside finite schema")
		}
		switch stringsByKey["recommendation"] {
		case "none", "start_control_service", "inspect_runtime_status", "not_available_in_current_phase", "config_plan_apply", "start_management_for_verification", "inspect_operations", "inspect_instance_paths":
		default:
			t.Fatal("doctor recommendation outside finite schema")
		}
		if id == "collector" && (stringsByKey["result"] != "unavailable" || stringsByKey["reason"] != "not_implemented") {
			t.Fatal("collector capability fabricated")
		}
		if stage == "missing" {
			if id == "state" && (stringsByKey["result"] != "unavailable" || stringsByKey["reason"] != "state_missing_or_incomplete") {
				t.Fatal("missing is not proof of a fresh instance")
			}
			if id != "state" && stringsByKey["evidence"] != "not_observed" {
				t.Fatal("missing originals fabricated facts")
			}
		}
		if stage == "draft" && id == "configuration" && (stringsByKey["result"] != "warning" || stringsByKey["reason"] != "no_committed_configuration") {
			t.Fatal("explicit local draft misreported as committed")
		}
		if stage == "running" && id == "core" && (stringsByKey["result"] != "pass" || stringsByKey["evidence"] != "fresh_core_identity") {
			t.Fatal("applied core not distinguished from intent")
		}
	}
}

type onboardingPeerIdentity struct {
	pid             int
	start           uint64
	boot, sha, argv string
}
type onboardingService struct {
	fd            *os.File
	identity      onboardingPeerIdentity
	stopped       bool
	reapAttempted bool
}

func onboardingServiceIdentity(pid int, cli *onboardingCLI) (onboardingPeerIdentity, error) {
	var zero onboardingPeerIdentity
	prefix := filepath.Join("/proc", strconv.Itoa(pid))
	raw, e := os.ReadFile(filepath.Join(prefix, "stat"))
	if e != nil {
		return zero, e
	}
	close := strings.LastIndexByte(string(raw), ')')
	open := strings.IndexByte(string(raw), '(')
	if open < 1 || close < open {
		return zero, errors.New("invalid service stat")
	}
	actual, e := strconv.Atoi(strings.TrimSpace(string(raw[:open])))
	fields := strings.Fields(string(raw[close+1:]))
	if e != nil || actual != pid || len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return zero, errors.New("invalid service stat identity")
	}
	start, e := strconv.ParseUint(fields[19], 10, 64)
	if e != nil || start == 0 {
		return zero, errors.New("invalid start")
	}
	boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return zero, e
	}
	if strings.TrimSpace(string(boot)) == "" {
		return zero, errors.New("service boot identity unavailable")
	}
	path, e := os.Readlink(filepath.Join(prefix, "exe"))
	if e != nil || path != cli.path {
		return zero, errors.New("service exe path mismatch")
	}
	exe, e := os.Open(filepath.Join(prefix, "exe"))
	if e != nil {
		return zero, e
	}
	hasher := sha256.New()
	n, e := io.Copy(hasher, io.LimitReader(exe, 128<<20+1))
	closeErr := exe.Close()
	if e != nil || closeErr != nil || n > 128<<20 || hex.EncodeToString(hasher.Sum(nil)) != cli.sha {
		return zero, errors.New("service executable SHA mismatch")
	}
	argv, e := os.ReadFile(filepath.Join(prefix, "cmdline"))
	if e != nil || string(argv) != cli.path+"\x00__hoopa_service\x00" {
		return zero, errors.New("service argv mismatch")
	}
	return onboardingPeerIdentity{pid: pid, start: start, boot: strings.TrimSpace(string(boot)), sha: cli.sha, argv: string(argv)}, nil
}
func onboardingObserveService(t *testing.T, c *onboardingCLI) *onboardingService {
	t.Helper()
	conn, e := service.DialEndpoint(appCtx(t), c.paths)
	appMust(t, e)
	defer conn.Close()
	raw, e := conn.SyscallConn()
	appMust(t, e)
	var peer *syscall.Ucred
	var peerErr error
	appMust(t, raw.Control(func(fd uintptr) {
		peer, peerErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}))
	appMust(t, peerErr)
	if peer == nil || peer.Pid <= 0 || peer.Uid != uint32(os.Geteuid()) {
		t.Fatal("service peer identity unavailable")
	}
	before, e := onboardingServiceIdentity(int(peer.Pid), c)
	appMust(t, e)
	fd, _, errno := syscall.Syscall(434, uintptr(peer.Pid), 0, 0)
	if errno != 0 {
		t.Fatal("original service pidfd unavailable", errno)
	}
	handle := os.NewFile(fd, "original-real-cli-service")
	after, e := onboardingServiceIdentity(int(peer.Pid), c)
	if e != nil || before != after {
		handle.Close()
		t.Fatal("service identity changed around pidfd")
	}
	return &onboardingService{fd: handle, identity: before}
}
func (s *onboardingService) wait(ctx context.Context) error {
	if s == nil || s.fd == nil {
		return errors.New("service proof unavailable")
	}
	type pollFD struct {
		fd              int32
		events, revents int16
	}
	p := pollFD{fd: int32(s.fd.Fd()), events: 1}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		p.revents = 0
		n, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&p)), 1, 50)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		if n == 0 {
			continue
		}
		if p.revents&0x20 != 0 {
			return errors.New("invalid original pidfd")
		}
		if p.revents&(1|0x10) == 0 {
			return errors.New("unexpected pidfd event")
		}
		break
	}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		raw, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(s.identity.pid), "stat"))
		if errors.Is(e, os.ErrNotExist) {
			s.stopped = true
			return nil
		}
		if e != nil {
			return e
		}
		// A numeric PID is never authority to reap. Only the original exited
		// pidfd plus matching start ticks and our own zombie child allow one
		// exact wait. After that attempt all observations remain read-only.
		open, close := strings.IndexByte(string(raw), '('), strings.LastIndexByte(string(raw), ')')
		if open < 1 || close < open {
			return errors.New("invalid exited service stat")
		}
		pid, pidErr := strconv.Atoi(strings.TrimSpace(string(raw[:open])))
		fields := strings.Fields(string(raw[close+1:]))
		if pidErr != nil || pid != s.identity.pid || len(fields) < 20 {
			return errors.New("invalid exited service identity")
		}
		ppid, parentErr := strconv.Atoi(fields[1])
		ticks, tickErr := strconv.ParseUint(fields[19], 10, 64)
		if parentErr != nil || tickErr != nil || ticks != s.identity.start || ppid != os.Getpid() || fields[0] != "Z" {
			return errors.New("exited service identity or parent mismatch")
		}
		if !s.reapAttempted {
			s.reapAttempted = true
			var status syscall.WaitStatus
			got, waitErr := syscall.Wait4(s.identity.pid, &status, syscall.WNOHANG, nil)
			if waitErr != nil && !errors.Is(waitErr, syscall.ECHILD) {
				return waitErr
			}
			if waitErr == nil && (got != s.identity.pid || !status.Exited() && !status.Signaled()) {
				return errors.New("original service wait did not return terminal child")
			}
			// ECHILD grants no further wait authority. Re-read only, requiring
			// actual absence or retaining a conservative timeout/error.
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type onboardingHarness struct {
	h       *appHarness
	cli     *onboardingCLI
	manager *onboardingService
	started bool
}

func newOnboardingHarness(t *testing.T) *onboardingHarness {
	t.Helper()
	h := newAppHarnessPrepared(t, false)
	c := newOnboardingCLI(t, h.root, filepath.Join(h.root, "home"), h.paths)
	for _, entry := range h.entries {
		for _, user := range entry.Users {
			c.secrets = append(c.secrets, user.Username, user.Password)
		}
	}
	c.secrets = append(c.secrets, h.runtime.Secret, h.runtime.Entry.Users[0].Username, h.runtime.Entry.Users[0].Password, "upstream-user", "upstream-password")
	var requests atomic.Int64
	wrap := func(server *httptest.Server) {
		original := server.Config.Handler
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { requests.Add(1); original.ServeHTTP(w, req) })
	}
	for _, proxy := range h.proxies {
		wrap(proxy.server)
	}
	wrap(h.origin)
	for _, origin := range h.originExtra {
		wrap(origin)
	}
	c.network = requests.Load
	r := &onboardingHarness{h: h, cli: c}
	t.Cleanup(r.cleanup)
	return r
}
func (r *onboardingHarness) start() {
	r.h.t.Helper()
	r.started = true // Any failed start attempt still needs bounded public cleanup.
	r.cli.ok(nil, "start", "--control-only", "--request-id", r.h.id("cli-start"))
	r.manager = onboardingObserveService(r.h.t, r.cli)
	if r.h.manager != nil {
		r.h.t.Fatal("test manager must not own real CLI service")
	}
}
func (r *onboardingHarness) stopService() {
	r.h.t.Helper()
	r.cli.ok(nil, "service", "stop", "--request-id", r.h.id("cli-service-stop"))
	appMust(r.h.t, r.manager.wait(appCtx(r.h.t)))
	appMust(r.h.t, r.manager.fd.Close())
	r.started = false
}
func (r *onboardingHarness) cleanup() {
	if !r.started {
		return
	}
	t := r.h.t
	t.Error("passing path did not complete real CLI public shutdown; fallback cannot count as success")
	if active, e := r.h.store.ReadActive(); e == nil && active.Generation != nil && active.DesiredRuntime == "running" {
		reply := r.cli.invoke(nil, "stop", "--accept-disruption", "--request-id", r.h.id("cli-emergency-stop"))
		if !reply.OK {
			t.Error("public core cleanup refused")
		}
	}
	reply := r.cli.invoke(nil, "service", "stop", "--request-id", r.h.id("cli-emergency-service-stop"))
	if !reply.OK {
		t.Error("public service cleanup refused")
	}
	if r.manager != nil {
		if e := r.manager.wait(appCtx(t)); e != nil {
			t.Error("service cleanup unverified; retain fixture and destroy isolated VM", e)
		}
		r.manager.fd.Close()
	} else {
		t.Error("no original service handle; cannot certify cleanup")
	}
}
func (r *onboardingHarness) finish() {
	r.h.t.Helper()
	_, original := r.h.run()
	r.cli.ok(nil, "stop", "--accept-disruption", "--request-id", r.h.id("cli-stop"))
	r.h.exit(original)
	r.h.trace.verify(r.h.t)
	r.stopService()
}
func (r *onboardingHarness) queryStage(stage string) {
	r.h.t.Helper()
	status := r.cli.readonly(nil, "status")
	if !status.OK {
		r.h.t.Fatal("status unavailable")
	}
	var state control.ServiceStatus
	appMust(r.h.t, json.Unmarshal(status.Data, &state))
	active := r.h.active()
	if state.Revision != active.Revision || !reflect.DeepEqual(state.Generation, active.Generation) {
		r.h.t.Fatal("CLI status lost committed identity")
	}
	if stage == "running" {
		id, handle := r.h.run()
		if state.Core.State != "verified-running" || state.Core.PID == nil || *state.Core.PID != handle.Identity().PID || state.Core.RunID == nil || *state.Core.RunID != id {
			r.h.t.Fatal("CLI status did not show actual verified current core")
		}
	} else if stage == "draft" && (state.Generation != nil || state.Core.State == "verified-running") {
		r.h.t.Fatal("CLI status promoted local draft to runtime")
	}

	onboardingDoctor(r.h.t, r.cli.readonly(nil, "doctor"), stage)
	request := appRequest(r.h.t, r.h.id("cli-doctor"), "doctor.get", map[string]any{})
	onboardingDoctor(r.h.t, r.cli.readonly(request, "api", "v1", "doctor.get"), stage)
}
func onboardingPlan(t *testing.T, r onboardingReply, path string) control.PlanDocument {
	t.Helper()
	if !r.OK {
		t.Fatal("local Plan entry unavailable", r.Error.Code)
	}
	raw, e := os.ReadFile(path)
	appMust(t, e)
	var doc control.PlanDocument
	appMust(t, json.Unmarshal(raw, &doc))
	digest, e := control.ComputePlanDigest(raw)
	appMust(t, e)
	var output any
	var response any
	appMust(t, json.Unmarshal(raw, &output))
	appMust(t, json.Unmarshal(r.Data, &response))
	st, e := os.Lstat(path)
	appMust(t, e)
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || digest != doc.PlanDigest || !reflect.DeepEqual(output, response) || doc.SchemaVersion != 2 {
		t.Fatal("output is not complete original private Plan")
	}
	return doc
}
func onboardingOperationID(t *testing.T, r onboardingReply) string {
	t.Helper()
	var data map[string]json.RawMessage
	_ = json.Unmarshal(r.Data, &data)
	var id string
	_ = json.Unmarshal(data["operation_id"], &id)
	if id == "" && r.Error != nil {
		_ = json.Unmarshal(r.Error.Details["operation_id"], &id)
	}
	if _, e := config.ParseID(id); e != nil {
		t.Fatal("admitted CLI result lacks operation identity")
	}
	return id
}
