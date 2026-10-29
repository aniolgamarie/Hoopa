//go:build linux && amd64

package mihomoevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func controlIPCInstance(t *testing.T) string {
	t.Helper()
	base, e := os.MkdirTemp("/tmp", "hoopa-ctl-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	dir := filepath.Join(base, "i")
	if e = InitControlInstance(dir); e != nil {
		t.Fatal(e)
	}
	return dir
}

type controlIPCTestService struct {
	dir, token string
	cancel     context.CancelFunc
	done       chan error
	once       sync.Once
}

func controlIPCStart(t *testing.T, dir string, b ControlBackend, options ControlServeOptions) *controlIPCTestService {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	options.Ready = func() { close(ready) }
	done := make(chan error, 1)
	go func() { done <- ServeControlInstance(ctx, dir, b, options) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("service startup: %v", err)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("service startup timed out")
	}
	token, e := os.ReadFile(filepath.Join(dir, "control.token"))
	if e != nil {
		t.Fatal(e)
	}
	s := &controlIPCTestService{dir: dir, token: strings.TrimSpace(string(token)), cancel: cancel, done: done}
	t.Cleanup(func() { s.stop(t) })
	return s
}
func (s *controlIPCTestService) stop(t *testing.T) {
	t.Helper()
	s.once.Do(func() {
		s.cancel()
		select {
		case err := <-s.done:
			if err != nil {
				t.Errorf("service shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("service shutdown timeout")
		}
	})
}
func controlIPCRequest(t *testing.T, s *controlIPCTestService, method, path string, raw []byte, headers []string) (int, []byte) {
	t.Helper()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", filepath.Join(s.dir, "control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, e := http.NewRequest(method, "http://unix"+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	for _, h := range headers {
		request.Header.Add("Authorization", h)
	}
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		t.Fatal(e)
	}
	return response.StatusCode, body
}
func controlIPCAuth(s *controlIPCTestService) []string { return []string{"Bearer " + s.token} }
func TestControlIPCInitializationAndPathBounds(t *testing.T) {
	dir := controlIPCInstance(t)
	token, e := controlInstanceMaterial(dir)
	if e != nil || len(token) != 64 {
		t.Fatal("invalid initial material")
	}
	if InitControlInstance(dir) == nil {
		t.Fatal("overwrote instance")
	}
	other := controlIPCInstance(t)
	second, _ := controlInstanceMaterial(other)
	if token == second {
		t.Fatal("token reused")
	}
	for _, path := range []string{"relative", "/tmp/../tmp/bad", filepath.Join("/tmp", strings.Repeat("x", 100))} {
		if e = InitControlInstance(path); e == nil {
			t.Fatal("unsafe/long path accepted")
		}
		if e = ServeControlInstance(context.Background(), path, &controlFake{}, ControlServeOptions{}); e == nil {
			t.Fatal("service accepted unsafe/long path")
		}
	}
}
func TestControlIPCAuthenticationAndReadOnly(t *testing.T) {
	dir := controlIPCInstance(t)
	f := &controlFake{}
	s := controlIPCStart(t, dir, f, ControlServeOptions{})
	before := controlTree(t, filepath.Join(dir, "store"))
	for _, headers := range [][]string{nil, {"Bearer wrong"}, {"Bearer " + s.token, "Bearer " + s.token}, {"bearer " + s.token}, {"Bearer  " + s.token}} {
		status, body := controlIPCRequest(t, s, "DELETE", "/wrong", nil, headers)
		if status != 401 || bytes.Contains(body, []byte(s.token)) {
			t.Fatal("unauthorized response invalid")
		}
	}
	status, body := controlIPCRequest(t, s, "GET", "/v1/status", nil, controlIPCAuth(s))
	if status != 200 {
		t.Fatal("status failed")
	}
	var v controlIPCStatus
	if json.Unmarshal(body, &v) != nil || v.Version != 1 || v.ActiveRevision != 0 || v.Desired != "stopped" || v.Observation != ControlVerifiedStopped {
		t.Fatal("status projection invalid")
	}
	if !reflect.DeepEqual(before, controlTree(t, filepath.Join(dir, "store"))) {
		t.Fatal("auth/status wrote store")
	}
	s.stop(t)
	if f.starts != 0 || f.stops != 0 {
		t.Fatal("service start/close changed core")
	}
}
func TestControlIPCInjectedWrongUID(t *testing.T) {
	dir := controlIPCInstance(t)
	s := controlIPCStart(t, dir, &controlFake{}, ControlServeOptions{peerUID: func(net.Conn) (uint32, error) { return uint32(os.Geteuid()) + 1, nil }})
	status, _ := controlIPCRequest(t, s, "GET", "/v1/status", nil, controlIPCAuth(s))
	if status != 401 {
		t.Fatal("injected wrong UID accepted despite right token")
	}
}
func TestControlIPCMethodPathAndStrictBodies(t *testing.T) {
	dir := controlIPCInstance(t)
	s := controlIPCStart(t, dir, &controlFake{}, ControlServeOptions{})
	for _, row := range []struct {
		method, path, body string
		want               int
	}{{"POST", "/v1/status", "", 405}, {"GET", "/v1/execute", "", 405}, {"GET", "/v1/status?query=1", "", 404}, {"GET", "/v1/%73tatus", "", 404}, {"GET", "/v1/status/", "", 404}, {"GET", "/unknown", "", 404}, {"GET", "/v1/status", "{}", 422}, {"POST", "/v1/execute", "{}", 422}, {"POST", "/v1/execute", `{"version":1,"version":1}`, 422}, {"POST", "/v1/execute", strings.Repeat("x", 256*1024+1), 422}, {"POST", "/v1/recover", `{"version":1,"operation_id":"first","expected_revision":0} {}`, 422}, {"POST", "/v1/recover", `{"version":1,"operation_id":"first","expected_revision":null}`, 422}, {"POST", "/v1/recover", `{"version":1,"operation_id":"first","expected_revision":0,"request_id":"bad"}`, 422}} {
		status, _ := controlIPCRequest(t, s, row.method, row.path, []byte(row.body), controlIPCAuth(s))
		if status != row.want {
			t.Fatalf("%s %s got%d want%d", row.method, row.path, status, row.want)
		}
	}
}
func TestControlIPCExecuteProjectionDuplicateAndC01(t *testing.T) {
	dir := controlIPCInstance(t)
	f := &controlFake{}
	s := controlIPCStart(t, dir, f, ControlServeOptions{})
	r := controlApply("first", 9, "/core")
	raw, _ := json.Marshal(r)
	before := controlTree(t, filepath.Join(dir, "store"))
	status, body := controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	if status != 409 || !bytes.Contains(body, []byte(`"CONFLICT"`)) || !reflect.DeepEqual(before, controlTree(t, filepath.Join(dir, "store"))) {
		t.Fatal("C01 failed")
	}
	r.ExpectedRevision = 0
	raw, _ = json.Marshal(r)
	status, body = controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	if status != 200 {
		t.Fatal("apply failed")
	}
	for _, secret := range []string{string(r.Bundle.Config), "original config", "core_path", "route_map", "request_digest", "bundle_digest", "nonce"} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatal("operation projection leaked private material")
		}
	}
	before = controlTree(t, filepath.Join(dir, "store"))
	status, _ = controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	if status != 200 || f.starts != 1 || !reflect.DeepEqual(before, controlTree(t, filepath.Join(dir, "store"))) {
		t.Fatal("duplicate reexecuted")
	}
	r.Bundle = ptrControlBundle(controlBundle("/other"))
	raw, _ = json.Marshal(r)
	status, body = controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	if status != 409 || !bytes.Contains(body, []byte(`"ID_CONFLICT"`)) {
		t.Fatal("ID conflict lost")
	}
	status, body = controlIPCRequest(t, s, "GET", "/v1/status", nil, controlIPCAuth(s))
	if status != 200 || bytes.Contains(body, []byte("config")) || bytes.Contains(body, []byte("/core")) {
		t.Fatal("status leaked config/path")
	}
	s.stop(t)
	if f.stops != 0 {
		t.Fatal("close stopped core")
	}
	s2 := controlIPCStart(t, dir, f, ControlServeOptions{})
	status, body = controlIPCRequest(t, s2, "GET", "/v1/status", nil, controlIPCAuth(s2))
	if status != 200 || !bytes.Contains(body, []byte(ControlVerifiedRunning)) || f.starts != 1 || f.stops != 0 {
		t.Fatal("open implicitly changed runtime")
	}
}
func TestControlIPCTwoWritersAndStaleSocket(t *testing.T) {
	dir := controlIPCInstance(t)
	f := &controlFake{}
	s := controlIPCStart(t, dir, f, ControlServeOptions{})
	before, e := os.Lstat(filepath.Join(dir, "control.sock"))
	if e != nil {
		t.Fatal(e)
	}
	if e = ServeControlInstance(context.Background(), dir, f, ControlServeOptions{}); e == nil {
		t.Fatal("second manager started")
	}
	after, e := os.Lstat(filepath.Join(dir, "control.sock"))
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("second manager unlinked live socket")
	}
	s.stop(t)
	stale, e := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "control.sock"), Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	stale.SetUnlinkOnClose(false)
	os.Chmod(filepath.Join(dir, "control.sock"), 0600)
	stale.Close()
	s2 := controlIPCStart(t, dir, f, ControlServeOptions{})
	status, _ := controlIPCRequest(t, s2, "GET", "/v1/status", nil, controlIPCAuth(s2))
	if status != 200 {
		t.Fatal("stale socket not replaced under lock")
	}
}
func TestControlIPCRejectsUnsafeMaterialWithoutRemoval(t *testing.T) {
	for _, kind := range []string{"token-mode", "token-size", "token-invalid", "token-symlink", "socket-file", "socket-symlink", "unknown-entry", "directory-mode"} {
		t.Run(kind, func(t *testing.T) {
			dir := controlIPCInstance(t)
			token := filepath.Join(dir, "control.token")
			socket := filepath.Join(dir, "control.sock")
			switch kind {
			case "token-mode":
				os.Chmod(token, 0644)
			case "token-size":
				os.WriteFile(token, []byte(strings.Repeat("a", 66)), 0600)
			case "token-invalid":
				os.WriteFile(token, []byte(strings.Repeat("A", 64)+"\n"), 0600)
			case "token-symlink":
				os.Remove(token)
				os.Symlink("/does-not-exist", token)
			case "socket-file":
				os.WriteFile(socket, []byte("retain"), 0600)
			case "socket-symlink":
				os.Symlink("/does-not-exist", socket)
			case "unknown-entry":
				os.WriteFile(filepath.Join(dir, "extra"), []byte("retain"), 0600)
			case "directory-mode":
				os.Chmod(dir, 0755)
			}
			if e := ServeControlInstance(context.Background(), dir, &controlFake{}, ControlServeOptions{}); e == nil {
				t.Fatal("unsafe material accepted")
			}
			if kind == "socket-file" || kind == "socket-symlink" {
				if _, e := os.Lstat(socket); e != nil {
					t.Fatal("unsafe path removed")
				}
			}
		})
	}
}
func TestControlIPCSocketCleanupOnlyOwnInode(t *testing.T) {
	dir := controlIPCInstance(t)
	s := controlIPCStart(t, dir, &controlFake{}, ControlServeOptions{})
	socket := filepath.Join(dir, "control.sock")
	if e := os.Remove(socket); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(socket, []byte("unrelated"), 0600); e != nil {
		t.Fatal(e)
	}
	s.once.Do(func() {
		s.cancel()
		select {
		case e := <-s.done:
			if e == nil {
				t.Fatal("replaced socket not reported")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown timeout")
		}
	})
	b, e := os.ReadFile(socket)
	if e != nil || string(b) != "unrelated" {
		t.Fatal("removed unrelated inode")
	}
}
func TestControlIPCCrashHookAndExplicitRecover(t *testing.T) {
	dir := controlIPCInstance(t)
	f := &controlFake{}
	exits := make(chan int, 1)
	s := controlIPCStart(t, dir, f, ControlServeOptions{CrashAt: "spawn-returned-before-record", guard: func() error { return nil }, exit: func(code int) { exits <- code }})
	r := controlApply("first", 0, "/core")
	raw, _ := json.Marshal(r)
	status, body := controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	if status != 503 || !bytes.Contains(body, []byte(`"POISONED"`)) {
		t.Fatal("fake crash boundary not retained")
	}
	select {
	case code := <-exits:
		if code != 86 {
			t.Fatal("wrong crash exit")
		}
	default:
		t.Fatal("exit seam not reached")
	}
	s.stop(t)
	if f.starts != 1 || f.stops != 0 {
		t.Fatal("fake manager crash changed core")
	}
	s2 := controlIPCStart(t, dir, f, ControlServeOptions{})
	if f.starts != 1 {
		t.Fatal("startup recovered implicitly")
	}
	raw = []byte(`{"version":1,"operation_id":"first","expected_revision":0}`)
	status, body = controlIPCRequest(t, s2, "POST", "/v1/recover", raw, controlIPCAuth(s2))
	if status != 200 || !bytes.Contains(body, []byte(`"committed"`)) || f.starts != 1 {
		t.Fatal("recover spawned or lost operation")
	}
	status, _ = controlIPCRequest(t, s2, "POST", "/v1/recover", []byte(`{"version":1,"operation_id":"first","expected_revision":1}`), controlIPCAuth(s2))
	if status != 200 || f.starts != 1 {
		t.Fatal("recover not idempotent")
	}
}
func TestControlIPCShutdownWaitsForInFlightOperation(t *testing.T) {
	dir := controlIPCInstance(t)
	f := &controlFake{}
	entered, release := make(chan struct{}), make(chan struct{})
	f.beforeStart = func(ControlLaunch) { close(entered); <-release }
	s := controlIPCStart(t, dir, f, ControlServeOptions{})
	raw, _ := json.Marshal(controlApply("first", 0, "/core"))
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("operation did not enter")
	}
	s.cancel()
	select {
	case <-s.done:
		t.Fatal("service closed store while operation running")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-requestDone
	s.stop(t)
	if f.stops != 0 {
		t.Fatal("shutdown stopped core")
	}
}
func TestControlIPCFixedErrorsAndCrashRefusal(t *testing.T) {
	for _, row := range []struct {
		err    error
		status int
		code   string
	}{{ErrControlConflict, 409, "CONFLICT"}, {ErrControlIDConflict, 409, "ID_CONFLICT"}, {ErrControlPending, 409, "PENDING"}, {ErrControlUnknown, 503, "UNKNOWN"}, {ErrLinuxControlRuntime, 503, "UNKNOWN"}, {ErrControlPoisoned, 503, "POISONED"}, {ErrControlClosed, 503, "CLOSED"}, {errors.New("do-not-echo-private-detail"), 500, "ACTION_FAILED"}} {
		status, code := controlIPCError(row.err)
		if status != row.status || code != row.code {
			t.Fatal("error projection changed")
		}
	}
	dir := controlIPCInstance(t)
	for _, point := range []string{"other", "spawn-returned-before-record"} {
		called := false
		e := ServeControlInstance(context.Background(), dir, &controlFake{}, ControlServeOptions{CrashAt: point, guard: func() error { called = true; return ErrLinuxControlGuest }})
		if e == nil {
			t.Fatal("crash option accepted without guest")
		}
		if point == "spawn-returned-before-record" && !called {
			t.Fatal("guest guard omitted")
		}
		if _, e := os.Lstat(filepath.Join(dir, "control.sock")); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("socket created before crash guard")
		}
	}
}

type controlIPCErrorBackend struct {
	*controlFake
	observeError, validateError error
}

func (b *controlIPCErrorBackend) Observe(ctx context.Context, l ControlLaunch) (ControlObservation, error) {
	if b.observeError != nil {
		return ControlObservation{State: ControlUnknown}, b.observeError
	}
	return b.controlFake.Observe(ctx, l)
}
func (b *controlIPCErrorBackend) Validate(v ControlBundle) error {
	if b.validateError != nil {
		return b.validateError
	}
	return b.controlFake.Validate(v)
}
func TestControlIPCPrivateProblemAndUnknownStatusProjection(t *testing.T) {
	t.Run("private-problem", func(t *testing.T) {
		dir := controlIPCInstance(t)
		fake := &controlFake{}
		secret := "private-backend-diagnostic-must-not-escape"
		s := controlIPCStart(t, dir, &controlIPCErrorBackend{controlFake: fake, observeError: errors.New(secret)}, ControlServeOptions{})
		raw, _ := json.Marshal(controlApply("first", 0, "/core"))
		status, body := controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
		if status != 500 || bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte("problem")) || !bytes.Contains(body, []byte("degraded")) {
			t.Fatal("private failure projection invalid")
		}
		before := controlTree(t, filepath.Join(dir, "store"))
		status, body = controlIPCRequest(t, s, "GET", "/v1/status", nil, controlIPCAuth(s))
		if status != 200 || bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte("bundle")) || !reflect.DeepEqual(before, controlTree(t, filepath.Join(dir, "store"))) {
			t.Fatal("pending status leaked or wrote")
		}
	})
	t.Run("running-intent-unknown-observation", func(t *testing.T) {
		dir := controlIPCInstance(t)
		fake := &controlFake{}
		s := controlIPCStart(t, dir, fake, ControlServeOptions{})
		raw, _ := json.Marshal(controlApply("first", 0, "/core"))
		status, _ := controlIPCRequest(t, s, "POST", "/v1/execute", raw, controlIPCAuth(s))
		if status != 200 {
			t.Fatal("first apply failed")
		}
		s.stop(t)
		s2 := controlIPCStart(t, dir, &controlIPCErrorBackend{controlFake: fake, observeError: ErrLinuxControlRuntime}, ControlServeOptions{})
		status, body := controlIPCRequest(t, s2, "GET", "/v1/status", nil, controlIPCAuth(s2))
		var response controlIPCStatus
		if json.Unmarshal(body, &response) != nil || status != 503 || response.ErrorCode != "UNKNOWN" || response.Observation != ControlUnknown || response.ActiveRevision != 1 || response.Desired != "running" || response.Instance != nil {
			t.Fatal("unknown folded into stopped or lost active intent")
		}
	})
}

func TestControlIPCAssetsOutsideInstance(t *testing.T) {
	dir := controlIPCInstance(t)
	backend := &LinuxControlBackend{assetsFile: filepath.Join(dir, "store", "assets.json")}
	if e := ServeControlInstance(context.Background(), dir, backend, ControlServeOptions{}); !errors.Is(e, ErrControlIPC) {
		t.Fatal("in-instance assets not refused")
	}
	if _, e := os.Lstat(filepath.Join(dir, "control.sock")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("socket created before external-assets check")
	}
}
