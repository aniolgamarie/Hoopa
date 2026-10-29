//go:build linux && amd64 && p1integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/service"
)

const p1CLIStdoutLimit = control.MaxResultBytes + 1 // wire body plus one CLI LF
const p1CLIStderrLimit = 4096
const p1CLIRuntimeLimit = 20 * time.Second

var (
	errP1CLITransport = errors.New("P1 CLI transport failed")
	errP1CLIInput     = errors.New("P1 CLI request invalid")
	errP1CLIOutput    = errors.New("P1 CLI output exceeds bound")
	errP1CLIEnvelope  = errors.New("P1 CLI response invalid")
	errP1CLIProcess   = errors.New("P1 CLI process unavailable")
	errP1CLIExit      = errors.New("P1 CLI exit status mismatch")
	errP1CLICapture   = errors.New("P1 CLI private capture unavailable")
)

// This seam launches only the supplied, pinned executable. It is not a
// productHarness and cannot turn projections/driver fixtures into product facts.
// Close must follow all Calls. Calls do not bootstrap, retry or rewrite a request.
// Directory/executable checks assume test-owned fixtures and cooperative writers;
// they are not a hostile same-UID exec-path/mount isolation boundary.
type p1CLITransport struct {
	executable, executableSHA, home, captures string
	executableInfo                            os.FileInfo
	originals                                 *os.Root
	paths                                     config.Paths
	driverMode                                string // test-driver cases only; never inherited from ambient env
}
type p1CLIDiagnostics struct {
	raw       []byte
	Truncated bool
}

func (p1CLIDiagnostics) String() string   { return "p1CLIDiagnostics{private bounded bytes}" }
func (p1CLIDiagnostics) GoString() string { return "p1CLIDiagnostics{private bounded bytes}" }

type p1CLIResult struct {
	RequestID      string // parsed from this invocation's input before execution
	Response       []byte // exact stdout, including its original whitespace/final LF
	Original       originalRef
	ExitCode       int
	Rejected       bool // validated business rejection; err == nil, including exit 5
	BusinessCode   string
	diagnostics    p1CLIDiagnostics
	stdoutRetained int
}

func (p1CLIResult) String() string   { return "p1CLIResult{private response and diagnostics}" }
func (p1CLIResult) GoString() string { return "p1CLIResult{private response and diagnostics}" }

type p1CLITransportError struct{ kind, cause error }

func (e *p1CLITransportError) Error() string    { return e.kind.Error() }
func (e *p1CLITransportError) String() string   { return e.Error() }
func (e *p1CLITransportError) GoString() string { return "p1CLITransportError{redacted}" }
func (e *p1CLITransportError) Unwrap() []error {
	if e.cause == nil {
		return []error{errP1CLITransport, e.kind}
	}
	return []error{errP1CLITransport, e.kind, e.cause}
}
func p1CLIFail(kind, cause error) error { return &p1CLITransportError{kind: kind, cause: cause} }

func p1CLIPrivateRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errP1CLICapture
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errP1CLICapture
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, errP1CLICapture
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, errP1CLICapture
	}
	info, err := file.Stat()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		root.Close()
		return nil, errP1CLICapture
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !os.SameFile(before, info) || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || st.Uid != uint32(os.Geteuid()) {
		root.Close()
		return nil, errP1CLICapture
	}
	return root, nil
}
func p1CLIExecutable(path string) (os.FileInfo, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", errP1CLIProcess
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Mode().Perm()&0022 != 0 {
		return nil, "", errP1CLIProcess
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", errP1CLIProcess
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		return nil, "", errP1CLIProcess
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return nil, "", errP1CLIProcess
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, file); err != nil {
		return nil, "", errP1CLIProcess
	}
	return info, hex.EncodeToString(digest.Sum(nil)), nil
}
func newP1CLITransport(executable, home, captures string) (*p1CLITransport, error) {
	info, digest, err := p1CLIExecutable(executable)
	if err != nil {
		return nil, p1CLIFail(errP1CLIProcess, nil)
	}
	root, err := p1CLIPrivateRoot(home)
	if err != nil {
		return nil, p1CLIFail(errP1CLIInput, nil)
	}
	if err = root.Close(); err != nil {
		return nil, p1CLIFail(errP1CLIInput, nil)
	}
	originals, err := p1CLIPrivateRoot(captures)
	if err != nil {
		return nil, p1CLIFail(errP1CLICapture, nil)
	}
	paths, err := config.ResolvePaths(home, map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache"), "XDG_RUNTIME_DIR": filepath.Join(home, "runtime")})
	if err != nil {
		originals.Close()
		return nil, p1CLIFail(errP1CLIInput, nil)
	}
	return &p1CLITransport{executable: executable, executableInfo: info, executableSHA: digest, home: home, captures: captures, originals: originals, paths: paths}, nil
}
func (c *p1CLITransport) Close() error        { return c.originals.Close() }
func (c *p1CLITransport) Paths() config.Paths { return c.paths }
func (c *p1CLITransport) environment() []string {
	env := []string{"HOME=" + c.home, "XDG_CONFIG_HOME=" + filepath.Join(c.home, "config"), "XDG_STATE_HOME=" + filepath.Join(c.home, "state"), "XDG_CACHE_HOME=" + filepath.Join(c.home, "cache"), "XDG_RUNTIME_DIR=" + filepath.Join(c.home, "runtime"), "TMPDIR=" + c.home, "LANG=C"}
	if c.driverMode != "" {
		env = append(env, "HOOPA_P1_CLI_TRANSPORT_DRIVER="+c.driverMode)
	}
	return env
}

type p1CLIBuffer struct {
	limit    int
	raw      []byte
	overflow bool
	cancel   context.CancelCauseFunc
}

func (b *p1CLIBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.raw)
	if remaining > len(p) {
		remaining = len(p)
	}
	b.raw = append(b.raw, p[:remaining]...)
	if remaining < len(p) {
		b.overflow = true
		if b.cancel != nil {
			b.cancel(errP1CLIOutput)
		}
	}
	return len(p), nil
}
func (c *p1CLITransport) Call(ctx context.Context, raw []byte) (result p1CLIResult, err error) {
	if ctx == nil {
		return result, p1CLIFail(errP1CLIInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return result, p1CLIFail(errP1CLIProcess, err)
	}
	if len(raw) == 0 || len(raw) > control.MaxRequestBytes {
		return result, p1CLIFail(errP1CLIInput, nil)
	}
	request, err := control.ParseRequest(raw)
	if err != nil || request.Version() != "v1" || strings.HasPrefix(request.Operation(), "-") {
		return result, p1CLIFail(errP1CLIInput, nil)
	}
	result.RequestID = request.ID()
	info, digest, err := p1CLIExecutable(c.executable)
	if err != nil || !os.SameFile(c.executableInfo, info) || digest != c.executableSHA {
		return result, p1CLIFail(errP1CLIProcess, nil)
	}
	bounded, finish := context.WithTimeout(ctx, p1CLIRuntimeLimit)
	defer finish()
	active, cancel := context.WithCancelCause(bounded)
	defer cancel(nil)
	stdout := &p1CLIBuffer{limit: p1CLIStdoutLimit, cancel: cancel}
	stderr := &p1CLIBuffer{limit: p1CLIStderrLimit}
	cmd := exec.CommandContext(active, c.executable, "api", "v1", request.Operation())
	cmd.Dir = c.home
	cmd.Env = c.environment()
	cmd.Stdin = bytes.NewReader(raw)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Cancel only exec.Cmd's original direct child. WaitDelay also fences pipe
	// goroutines if an unexpected descendant retains a stdio descriptor.
	cmd.WaitDelay = 250 * time.Millisecond
	runErr := cmd.Run()
	result.diagnostics = p1CLIDiagnostics{raw: bytes.Clone(stderr.raw), Truncated: stderr.overflow}
	result.stdoutRetained = len(stdout.raw)
	if stdout.overflow {
		return result, p1CLIFail(errP1CLIOutput, nil)
	}
	if cause := bounded.Err(); cause != nil {
		return result, p1CLIFail(errP1CLIProcess, cause)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		return result, p1CLIFail(errP1CLIProcess, nil)
	}
	actualExit := cmd.ProcessState.ExitCode()
	result.ExitCode = actualExit
	if runErr != nil {
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) {
			return result, p1CLIFail(errP1CLIProcess, nil)
		}
	}
	body, code, rejected, err := validateP1CLIResponse(stdout.raw, request.ID())
	if err != nil {
		return result, p1CLIFail(err, nil)
	}
	if actualExit != service.ExitCode(body) {
		return result, p1CLIFail(errP1CLIExit, nil)
	}
	original, err := c.capture(stdout.raw)
	if err != nil {
		return result, p1CLIFail(errP1CLICapture, nil)
	}
	result.Response = bytes.Clone(stdout.raw)
	result.Original = original
	result.BusinessCode = code
	result.Rejected = rejected
	return result, nil
}
func (c *p1CLITransport) capture(raw []byte) (originalRef, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return originalRef{}, errP1CLICapture
	}
	name := "cli-response-" + hex.EncodeToString(random[:]) + ".json"
	file, err := c.originals.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return originalRef{}, errP1CLICapture
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return originalRef{}, errP1CLICapture
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		file.Close()
		return originalRef{}, errP1CLICapture
	}
	n, err := file.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return originalRef{}, errP1CLICapture
	}
	directory, err := c.originals.Open(".")
	if err != nil {
		return originalRef{}, errP1CLICapture
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		return originalRef{}, errP1CLICapture
	}
	current, err := c.originals.Lstat(name)
	if err != nil || !os.SameFile(info, current) {
		return originalRef{}, errP1CLICapture
	}
	sum := sha256.Sum256(raw)
	return originalRef{Path: filepath.Join(c.captures, name), Bytes: int64(len(raw)), SHA256: hex.EncodeToString(sum[:])}, nil
}

// Validation deliberately precedes service.ExitCode. That function returns 5
// for both valid business failures and malformed envelopes, so it is no parser.
// Data remains opaque, exactly as in service's frame-level envelope contract.
func validateP1CLIResponse(raw []byte, requestID string) (body []byte, code string, rejected bool, err error) {
	if len(raw) == 0 || len(raw) > p1CLIStdoutLimit {
		return nil, "", false, errP1CLIOutput
	}
	body = raw
	if body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	if len(body) == 0 || len(body) > control.MaxResultBytes {
		return nil, "", false, errP1CLIOutput
	}
	canonical, e := config.CanonicalJSON(body)
	if e != nil || len(canonical) > control.MaxResultBytes {
		return nil, "", false, errP1CLIEnvelope
	}
	object, ok := p1CLIObject(canonical)
	if !ok || !p1CLIFields(object, []string{"api_version", "request_id", "ok", "data", "error", "warnings"}) || string(object["api_version"]) != `"v1"` {
		return nil, "", false, errP1CLIEnvelope
	}
	id, ok := p1CLIString(object["request_id"])
	if !ok || utf8.RuneCountInString(id) > 128 || id != requestID {
		return nil, "", false, errP1CLIEnvelope
	}
	success, ok := p1CLIBool(object["ok"])
	if !ok {
		return nil, "", false, errP1CLIEnvelope
	}
	if success {
		if _, ok := p1CLIObject(object["data"]); !ok || string(object["error"]) != "null" {
			return nil, "", false, errP1CLIEnvelope
		}
	} else {
		if string(object["data"]) != "null" {
			return nil, "", false, errP1CLIEnvelope
		}
		code, ok = p1CLIError(object["error"])
		if !ok {
			return nil, "", false, errP1CLIEnvelope
		}
	}
	if len(object["warnings"]) == 0 || object["warnings"][0] != '[' {
		return nil, "", false, errP1CLIEnvelope
	}
	var warnings []json.RawMessage
	if json.Unmarshal(object["warnings"], &warnings) != nil || warnings == nil {
		return nil, "", false, errP1CLIEnvelope
	}
	for _, warning := range warnings {
		fields, ok := p1CLIObject(warning)
		if !ok || !p1CLIFields(fields, []string{"code", "message"}) {
			return nil, "", false, errP1CLIEnvelope
		}
		for _, key := range []string{"code", "message"} {
			if _, ok := p1CLIString(fields[key]); !ok {
				return nil, "", false, errP1CLIEnvelope
			}
		}
	}
	return body, code, !success, nil
}
func p1CLIObject(raw []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	return obj, true
}
func p1CLIFields(obj map[string]json.RawMessage, required []string, optional ...string) bool {
	allowed := map[string]bool{}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return false
		}
		allowed[key] = true
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range obj {
		if !allowed[key] {
			return false
		}
	}
	return true
}
func p1CLIString(raw []byte) (string, bool) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}
func p1CLIBool(raw []byte) (bool, bool) {
	switch string(raw) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}
func p1CLIError(raw []byte) (string, bool) {
	object, ok := p1CLIObject(raw)
	if !ok || !p1CLIFields(object, []string{"code", "message", "retryable"}, "details") {
		return "", false
	}
	code, ok := p1CLIString(object["code"])
	if !ok {
		return "", false
	}
	switch code {
	case "INVALID_ARGUMENT", "UNSUPPORTED_VERSION", "CAPABILITY_UNSUPPORTED", "CONFLICT", "REQUEST_ID_REUSED", "NOT_RUNNING", "DEPENDENCY_UNAVAILABLE", "READ_UNAVAILABLE", "OPERATION_CAPACITY_EXHAUSTED", "SNAPSHOT_EXPIRED", "OPERATION_NOT_FOUND", "APPLY_FAILED", "RECOVERY_REQUIRED", "LEDGER_UNAVAILABLE", "INTEGRITY_ERROR", "AUTHORIZATION_DENIED", "CANCELLED":
	default:
		return "", false
	}
	if _, ok := p1CLIString(object["message"]); !ok {
		return "", false
	}
	if _, ok := p1CLIBool(object["retryable"]); !ok {
		return "", false
	}
	if raw, exists := object["details"]; exists {
		details, ok := p1CLIObject(raw)
		if !ok || !p1CLIFields(details, nil, "reason", "current_revision", "expected_revision", "operation_id", "field") {
			return "", false
		}
		for key, raw := range details {
			value, ok := p1CLIString(raw)
			if !ok {
				return "", false
			}
			if key == "current_revision" || key == "expected_revision" || key == "operation_id" {
				if _, err := config.ParseID(value); err != nil {
					return "", false
				}
			}
		}
	}
	return code, true
}

// Driver fixture entry only: exact `api v1 OPERATION` args plus an explicit
// test-only marker. It emits no product receipt, operation log or harness fact.
// Ordinary go test/matrix invocation cannot enter this branch.
func init() {
	mode := os.Getenv("HOOPA_P1_CLI_TRANSPORT_DRIVER")
	if mode == "" || len(os.Args) != 4 || os.Args[1] != "api" || os.Args[2] != "v1" {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, control.MaxRequestBytes+1))
	if err != nil {
		os.Exit(90)
	}
	request, err := control.ParseRequest(raw)
	if err != nil || request.Operation() != os.Args[3] {
		os.Exit(90)
	}
	value := map[string]any{"api_version": "v1", "request_id": request.ID(), "ok": true, "data": map[string]any{"driver_fixture": true}, "error": nil, "warnings": []any{}}
	exit := 0
	switch mode {
	case "reject", "mismatch":
		value["ok"] = false
		value["data"] = nil
		value["error"] = map[string]any{"code": "RECOVERY_REQUIRED", "message": "driver rejection", "retryable": false}
		if mode == "reject" {
			exit = 5
		}
	case "unknown-code":
		value["ok"] = false
		value["data"] = nil
		value["error"] = map[string]any{"code": "PRIVATE_UNKNOWN_CODE", "message": "driver rejection", "retryable": false}
		exit = 5
	case "wrong-id":
		value["request_id"] = "different-driver-id"
	case "null-id":
		value["request_id"] = nil
		value["ok"] = false
		value["data"] = nil
		value["error"] = map[string]any{"code": "INVALID_ARGUMENT", "message": "driver rejection", "retryable": false}
		exit = 2
	case "env":
		allowed := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true, "TMPDIR": true, "LANG": true, "HOOPA_P1_CLI_TRANSPORT_DRIVER": true}
		for _, item := range os.Environ() {
			key, _, _ := strings.Cut(item, "=")
			if !allowed[key] {
				os.Exit(91)
			}
		}
		value["data"] = map[string]any{"driver_fixture": true, "environment_isolated": true}
	case "stderr":
		_, _ = io.WriteString(os.Stderr, strings.Repeat("private-stderr-secret", p1CLIStderrLimit))
	case "cancel":
		_, _ = io.WriteString(os.Stderr, "private-stderr-secret")
		time.Sleep(10 * time.Second)
	case "endless-stderr":
		for {
			_, _ = io.WriteString(os.Stderr, "private-stderr-secret")
			time.Sleep(time.Millisecond)
		}
	case "oversize":
		_, _ = io.WriteString(os.Stdout, strings.Repeat("x", p1CLIStdoutLimit+10000))
		os.Exit(0)
	case "exact-max", "too-large-body":
		data := map[string]any{"driver_fixture": true, "padding": ""}
		value["data"] = data
		encoded, _ := json.Marshal(value)
		padding := control.MaxResultBytes - len(encoded)
		if mode == "too-large-body" {
			padding++
		}
		data["padding"] = strings.Repeat("x", padding)
	case "success", "duplicate", "trailing", "warning-extra", "error-extra", "bad-details", "top-extra", "invalid-ok", "null-warnings", "invalid-unicode":
	default:
		os.Exit(92)
	}
	encoded, _ := json.Marshal(value)
	switch mode {
	case "top-extra":
		value["credential"] = "private-stderr-secret"
		encoded, _ = json.Marshal(value)
	case "invalid-ok":
		value["ok"] = nil
		encoded, _ = json.Marshal(value)
	case "null-warnings":
		value["warnings"] = nil
		encoded, _ = json.Marshal(value)
	case "invalid-unicode":
		encoded = bytes.Replace(encoded, []byte(`"transport-driver-id"`), []byte(`"\ud800"`), 1)
	case "duplicate":
		encoded = append(encoded[:len(encoded)-1], []byte(`,"ok":true}`)...)
	case "trailing":
		encoded = append(encoded, []byte(` {}`)...)
	case "warning-extra":
		value["warnings"] = []any{map[string]any{"code": "DRIVER", "message": "driver warning", "credential": "private-stderr-secret"}}
		encoded, _ = json.Marshal(value)
	case "error-extra", "bad-details":
		value["ok"] = false
		value["data"] = nil
		failure := map[string]any{"code": "RECOVERY_REQUIRED", "message": "driver rejection", "retryable": false}
		if mode == "error-extra" {
			failure["credential"] = "private-stderr-secret"
		} else {
			failure["details"] = map[string]any{"operation_id": "../invalid"}
		}
		value["error"] = failure
		encoded, _ = json.Marshal(value)
		exit = 5
	}
	if mode == "too-large-body" {
		_, _ = os.Stdout.Write(encoded)
	} else if mode == "exact-max" {
		_, _ = os.Stdout.Write(append(encoded, '\n'))
	} else {
		_, _ = os.Stdout.Write(append(append([]byte(" \n"), encoded...), '\n'))
	}
	os.Exit(exit)
}

func p1CLITransportFixture(t *testing.T, mode string) (*p1CLITransport, string) {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "hoopa-p1-cli-transport-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	home, captures := filepath.Join(base, "home"), filepath.Join(base, "originals")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(captures, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	executable := filepath.Join(base, "cli-driver-fixture")
	output, err := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(output, input)
	err = errors.Join(err, output.Close())
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newP1CLITransport(executable, home, captures)
	if err != nil {
		t.Fatal(err)
	}
	transport.driverMode = mode
	t.Cleanup(func() { transport.Close() })
	return transport, captures
}
func p1CLITransportRequest(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"api_version": "v1", "request_id": "transport-driver-id", "operation": "status.get", "params": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func p1CLITransportNoCapture(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid response created an original", err, len(entries))
	}
}
func TestP1CLITransportDriverSuccessAndBusinessRejection(t *testing.T) {
	for _, mode := range []string{"success", "reject", "stderr", "env", "exact-max"} {
		t.Run(mode, func(t *testing.T) {
			transport, _ := p1CLITransportFixture(t, mode)
			t.Setenv("HTTP_PROXY", "ambient-proxy-secret")
			t.Setenv("HTTPS_PROXY", "ambient-proxy-secret")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-credential-secret")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := transport.Call(ctx, p1CLITransportRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			request, err := control.ParseRequest(p1CLITransportRequest(t))
			if err != nil || result.RequestID != request.ID() {
				t.Fatal("transport did not retain this invocation's request identity")
			}
			if result.Rejected != (mode == "reject") {
				t.Fatal("business rejection lost")
			}
			if mode == "reject" && (result.ExitCode != 5 || result.BusinessCode != "RECOVERY_REQUIRED") {
				t.Fatal("valid exit5 classified as transport failure")
			}
			verifyOriginal(t, result.Original)
			captured, err := os.ReadFile(result.Original.Path)
			if err != nil || !bytes.Equal(captured, result.Response) {
				t.Fatal("capture changed original stdout", err)
			}
			info, err := os.Lstat(result.Original.Path)
			if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSymlink != 0 {
				t.Fatal("capture is not private regular original", err)
			}
			if (mode != "exact-max" && !bytes.HasPrefix(result.Response, []byte(" \n"))) || !bytes.HasSuffix(result.Response, []byte{'\n'}) {
				t.Fatal("response whitespace rewritten")
			}
			if mode == "exact-max" && len(result.Response) != p1CLIStdoutLimit {
				t.Fatal("maximum wire body plus LF not accepted exactly")
			}
			if mode == "stderr" && (len(result.diagnostics.raw) != p1CLIStderrLimit || !result.diagnostics.Truncated) {
				t.Fatal("stderr retention unbounded")
			}
			if strings.Contains(fmt.Sprintf("%v %#v %v", result, result, result.diagnostics), "private-stderr-secret") {
				t.Fatal("private diagnostics formatted publicly")
			}
			entries, err := os.ReadDir(transport.home)
			if err != nil || len(entries) != 0 {
				t.Fatal("driver changed isolated home", err)
			}
		})
	}
}
func TestP1CLITransportDriverRejectsInvalidOutputWithoutCapture(t *testing.T) {
	for _, mode := range []string{"unknown-code", "wrong-id", "null-id", "duplicate", "trailing", "warning-extra", "error-extra", "bad-details", "top-extra", "invalid-ok", "null-warnings", "invalid-unicode", "mismatch", "oversize", "too-large-body"} {
		t.Run(mode, func(t *testing.T) {
			transport, captures := p1CLITransportFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := transport.Call(ctx, p1CLITransportRequest(t))
			if err == nil || !errors.Is(err, errP1CLITransport) || result.Response != nil || result.Original.Path != "" {
				t.Fatal("invalid output was accepted/captured")
			}
			if mode == "mismatch" && !errors.Is(err, errP1CLIExit) {
				t.Fatal(err)
			}
			if mode == "oversize" && (!errors.Is(err, errP1CLIOutput) || result.stdoutRetained > p1CLIStdoutLimit) {
				t.Fatal("stdout overflow not bounded")
			}
			if strings.Contains(fmt.Sprintf("%v %#v", err, result), "private-stderr-secret") {
				t.Fatal("invalid diagnostic leaked")
			}
			p1CLITransportNoCapture(t, captures)
		})
	}
}
func TestP1CLITransportDriverCancellationOnlyOwnChild(t *testing.T) {
	transport, captures := p1CLITransportFixture(t, "cancel")
	sibling := exec.Command(transport.executable, "api", "v1", "status.get")
	sibling.Env = transport.environment()
	sibling.Stdin = bytes.NewReader(p1CLITransportRequest(t))
	sibling.Stdout = io.Discard
	sibling.Stderr = io.Discard
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sibling.Process.Kill(); sibling.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	began := time.Now()
	result, err := transport.Call(ctx, p1CLITransportRequest(t))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > time.Second || result.Response != nil {
		t.Fatal("cancellation not bounded", err)
	}
	if err := sibling.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("cancellation affected separate original child", err)
	}
	if strings.Contains(fmt.Sprintf("%v %#v", err, result), "private-stderr-secret") {
		t.Fatal("cancel diagnostics leaked")
	}
	p1CLITransportNoCapture(t, captures)
}
func TestP1CLITransportDriverInvalidInputAndPinnedExecutable(t *testing.T) {
	transport, captures := p1CLITransportFixture(t, "success")
	for _, raw := range [][]byte{nil, []byte(`{"api_version":"v1","request_id":"id","request_id":"other","operation":"status.get","params":{}}`), bytes.Repeat([]byte{' '}, control.MaxRequestBytes+1), []byte(`{"api_version":"v1","request_id":"id","operation":"--help","params":{}}`)} {
		if _, err := transport.Call(context.Background(), raw); !errors.Is(err, errP1CLIInput) {
			t.Fatal("invalid request invoked child", err)
		}
	}
	if err := os.Rename(transport.executable, transport.executable+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transport.executable, []byte("changed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Call(context.Background(), p1CLITransportRequest(t)); !errors.Is(err, errP1CLIProcess) {
		t.Fatal("fixed executable replacement accepted", err)
	}
	p1CLITransportNoCapture(t, captures)
}
func TestP1CLITransportActualReadonlyFailure(t *testing.T) {
	// Actual product CLI, no driver mode and no productHarness factory. This is
	// one readonly failure roundtrip, not any T034 lifecycle/apply matrix row.
	transport, captures := p1CLITransportFixture(t, "")
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(filepath.Dir(transport.home), "hoopa")
	build := exec.Command("/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go", "build", "-buildvcs=false", "-o", binary, "./cmd/hoopa")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOMODCACHE=/tmp/hoopa-p1-cobra-eywc0j61/modcache")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixed offline CLI build: %v\n%s", err, output)
	}
	// Optional test-owned artifact destination retains actual response evidence;
	// it is not inherited by the child or exposed as a product runtime setting.
	retained := false
	if artifactDir := os.Getenv("HOOPA_P1_CLI_TRANSPORT_ARTIFACTS"); artifactDir != "" {
		captures = artifactDir
		retained = true
	}
	actual, err := newP1CLITransport(binary, transport.home, captures)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := actual.Call(ctx, p1CLITransportRequest(t))
	if err != nil || !result.Rejected || result.ExitCode != 4 || result.BusinessCode != "READ_UNAVAILABLE" {
		t.Fatal("actual readonly failure roundtrip", err, result.ExitCode, result.BusinessCode)
	}
	verifyOriginal(t, result.Original)
	for _, path := range []string{actual.Paths().Config, actual.Paths().State, actual.Paths().Cache, actual.Paths().Runtime} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("readonly CLI created isolated state", path, err)
		}
	}
	if retained {
		receipt := struct {
			Kind             string      `json:"kind"`
			Original         originalRef `json:"original"`
			ExecutableSHA256 string      `json:"executable_sha256"`
			ExitCode         int         `json:"exit_code"`
			Code             string      `json:"code"`
			StateAbsent      bool        `json:"state_absent"`
			StderrBytes      int         `json:"stderr_bytes"`
			StderrTruncated  bool        `json:"stderr_truncated"`
		}{"actual_cli_readonly_failure_observation", result.Original, actual.executableSHA, result.ExitCode, result.BusinessCode, true, len(result.diagnostics.raw), result.diagnostics.Truncated}
		encoded, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(result.Original.Path+".receipt.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.Write(append(encoded, '\n'))
		if err == nil {
			err = file.Sync()
		}
		err = errors.Join(err, file.Close())
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual readonly CLI rejection: exit=%d code=%s original_bytes=%d sha256=%s retained=%v original=%q executable_sha256=%s; all four state scopes remain absent before fixture cleanup", result.ExitCode, result.BusinessCode, result.Original.Bytes, result.Original.SHA256, retained, result.Original.Path, actual.executableSHA)
}

func TestP1CLITransportDriverBackgroundRuntimeBound(t *testing.T) {
	transport, captures := p1CLITransportFixture(t, "endless-stderr")
	began := time.Now()
	result, err := transport.Call(context.Background(), p1CLITransportRequest(t))
	elapsed := time.Since(began)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed > p1CLIRuntimeLimit+2*time.Second || len(result.diagnostics.raw) > p1CLIStderrLimit || !result.diagnostics.Truncated {
		t.Fatal("background runtime/stderr not bounded", err, elapsed)
	}
	p1CLITransportNoCapture(t, captures)
	t.Logf("driver-only endless stderr with context.Background terminated by helper deadline after %s; private prefix=%dB truncated=%v", elapsed, len(result.diagnostics.raw), result.diagnostics.Truncated)
}
