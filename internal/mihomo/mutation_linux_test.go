//go:build linux && amd64

package mihomo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
)

// Plans are explicit synthetic HTTP semantics. They do not implement Mihomo,
// authorization, or a product state machine, and never derive expected snapshots.
type mutationStep struct {
	Method, Path   string
	Body, Response []byte
}
type mutationPlan struct {
	Secret, Address string
	Steps           []mutationStep
}

func mutationServe(conn *net.TCPConn, p mutationPlan) (int, error) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(conn)
	for i, s := range p.Steps {
		r, err := http.ReadRequest(reader)
		if err != nil {
			return i, errors.New("synthetic request framing failed")
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		r.Body.Close()
		if err != nil || r.Method != s.Method || r.URL.Path != s.Path || r.URL.RawQuery != "" || r.Host != p.Address || r.Header.Get("Authorization") != "Bearer "+p.Secret || string(b) != string(s.Body) {
			return i + 1, errors.New("synthetic request binding failed")
		}
		if s.Response == nil {
			return i + 1, nil
		} // An ACK-lost fixture, not proof of no side effect.
		if _, err = conn.Write(s.Response); err != nil {
			return i + 1, errors.New("synthetic response write failed")
		}
	}
	return len(p.Steps), nil
}
func mutationStatusSteps(t *testing.T, b inspectorBlueprint, s wire.Status) []mutationStep {
	t.Helper()
	return []mutationStep{{Method: "GET", Path: "/hoopa/v1/status", Response: responseBytes(inspectorJSON(t, s))}, {Method: "GET", Path: "/hoopa/v1/snapshot", Response: responseBytes(inspectorJSON(t, b.snapshot))}, {Method: "GET", Path: "/hoopa/v1/status", Response: responseBytes(inspectorJSON(t, s))}}
}
func mutationRequest(t *testing.T, b inspectorBlueprint, reg config.RunRegistration, sha, action string) wire.Mutation {
	t.Helper()
	digest, err := b.snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return wire.Mutation{SchemaVersion: 1, Operation: action, OperationID: string(reg.OperationID), RunID: string(b.intent.RunID), ConfigGeneration: string(b.intent.Generation), ConfigSHA256: b.intent.ConfigSHA256, SnapshotSHA256: digest, RegistrationSHA256: sha}
}
func mutationResult(t *testing.T, r wire.Mutation, result string) wire.Result {
	t.Helper()
	digest, err := r.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return wire.Result{SchemaVersion: 1, OperationID: r.OperationID, RunID: r.RunID, RequestSHA256: digest, Result: result}
}
func mutationResponse(t *testing.T, code int, result wire.Result) []byte {
	t.Helper()
	raw := inspectorJSON(t, result)
	return append([]byte(fmt.Sprintf("HTTP/1.1 %d fixture\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", code, len(raw))), raw...)
}
func mutationRegistration(b inspectorBlueprint) config.RunRegistration {
	return config.RunRegistration{Version: 1, RunID: b.intent.RunID, OperationID: "operation:activate", IntentSHA256: strings.Repeat("1", 64), BirthSHA256: strings.Repeat("2", 64), PID: 1, UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1, Generation: b.intent.Generation, ManifestSHA256: b.intent.ManifestSHA256, BinarySHA256: b.intent.Binary.SHA256, ConfigSHA256: b.intent.ConfigSHA256, NonceSHA256: b.intent.NonceSHA256, Controller: b.intent.Controller, Gate: "closed"}
}
func mutationRound(t *testing.T, b inspectorBlueprint, reg config.RunRegistration, sha, action string, before, after wire.Status, result wire.Result, code int) mutationPlan {
	t.Helper()
	p := mutationPlan{Secret: b.secret, Address: b.intent.Controller, Steps: mutationStatusSteps(t, b, before)}
	r := mutationRequest(t, b, reg, sha, action)
	p.Steps = append(p.Steps, mutationStep{Method: "POST", Path: "/hoopa/v1/" + action, Body: inspectorJSON(t, r), Response: mutationResponse(t, code, result)})
	p.Steps = append(p.Steps, mutationStatusSteps(t, b, after)...)
	return p
}

func TestMihomoMutationSession(t *testing.T) {
	names := []string{"activate", "drain", "explicit-replay", "historical-opened-current-draining", "verified-503", "wrong-operation", "wrong-run", "wrong-request-digest", "wrong-success-kind", "failed-with-200", "opened-with-503", "unexpected-http-status", "malformed-result", "truncated-ack", "lost-ack", "post-observation-mismatch", "post-history-mismatch", "conflicting-id", "conflicting-content", "other-action-same-id", "first-activation-historical-open", "drain-already-draining"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			f := inspectorPublish(t, inspectorBlueprintFor(t, ln.Addr().String(), config.BinaryReference{}, nil))
			b := f.blueprint
			reg := mutationRegistration(b)
			sha := strings.Repeat("3", 64)
			action := "activate"
			if name == "drain" || name == "drain-already-draining" {
				action = "drain"
				reg.OperationID = "operation:drain"
			}
			request := mutationRequest(t, b, reg, sha, action)
			kind := "opened"
			if action == "drain" {
				kind = "draining"
			}
			result := mutationResult(t, request, kind)
			before := inspectorStatus(t, b, "closed")
			after := inspectorStatus(t, b, "open")
			after.Activation = &result
			if action == "drain" {
				before = inspectorStatus(t, b, "open")
				after = before
				after.State = "draining"
				after.Drain = &result
			}
			expectedErr := false
			expectedReceipt := true
			expectedCurrent := true
			noPost := false
			code := 200
			switch name {
			case "explicit-replay":
				before = after
			case "historical-opened-current-draining":
				before = after
				before.State = "draining"
				before.Drain = inspectorStatus(t, b, "draining").Drain
				after = before
			case "verified-503":
				result.Result = "failed"
				code = 503
				after = inspectorStatus(t, b, "degraded")
				after.Activation = &result
			case "wrong-operation":
				result.OperationID = "operation:wrong"
			case "wrong-run":
				result.RunID = "run:wrong"
			case "wrong-request-digest":
				result.RequestSHA256 = strings.Repeat("4", 64)
			case "wrong-success-kind":
				result.Result = "draining"
			case "failed-with-200":
				result.Result = "failed"
			case "opened-with-503":
				code = 503
			case "unexpected-http-status":
				code = 409
			case "post-observation-mismatch", "post-history-mismatch":
				expectedErr = true
				expectedCurrent = false
			case "conflicting-id":
				before = after
				old := result
				old.OperationID = "operation:other"
				before.Activation = &old
				noPost = true
			case "conflicting-content":
				before = after
				old := result
				old.RequestSHA256 = strings.Repeat("5", 64)
				before.Activation = &old
				noPost = true
			case "other-action-same-id":
				before = inspectorStatus(t, b, "draining")
				old := *before.Drain
				old.OperationID = string(reg.OperationID)
				before.Drain = &old
				noPost = true
			case "first-activation-historical-open":
				reg.Gate = "open"
				noPost = true
			case "drain-already-draining":
				before = inspectorStatus(t, b, "draining")
				noPost = true
			}
			invalidReply := name == "wrong-operation" || name == "wrong-run" || name == "wrong-request-digest" || name == "wrong-success-kind" || name == "failed-with-200" || name == "opened-with-503" || name == "unexpected-http-status" || name == "malformed-result" || name == "truncated-ack" || name == "lost-ack"
			if invalidReply || noPost {
				expectedErr = true
				expectedReceipt = false
				expectedCurrent = false
			}
			p := mutationRound(t, b, reg, sha, action, before, after, result, code)
			switch name {
			case "malformed-result":
				p.Steps[3].Response = responseBytes([]byte(`{"unrecognized":"fixture-body-must-not-leak"}`))
			case "truncated-ack":
				p.Steps[3].Response = []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{false")
			case "lost-ack":
				p.Steps[3].Response = nil
			case "post-observation-mismatch":
				bad := after
				bad.ConfigSHA256 = strings.Repeat("6", 64)
				p.Steps[4].Response = responseBytes(inspectorJSON(t, bad))
			case "post-history-mismatch":
				bad := after
				old := *bad.Activation
				old.RequestSHA256 = strings.Repeat("7", 64)
				bad.Activation = &old
				copy(p.Steps[4:], mutationStatusSteps(t, b, bad))
			}
			expectedRequests := 7
			if noPost {
				p.Steps = p.Steps[:3]
				expectedRequests = 3
			} else if invalidReply {
				p.Steps = p.Steps[:4]
				expectedRequests = 4
			} else if name == "post-observation-mismatch" {
				p.Steps = p.Steps[:5]
				expectedRequests = 5
			}
			type report struct {
				n   int
				err error
			}
			done := make(chan report, 1)
			go func() {
				conn, e := ln.AcceptTCP()
				if e != nil {
					done <- report{err: e}
					return
				}
				n, e := mutationServe(conn, p)
				done <- report{n, e}
			}()
			conn, err := net.DialTCP("tcp4", nil, ln.Addr().(*net.TCPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			session, closeSession, err := beginControllerSession(inspectorContext(t), conn, b.intent.Controller, b.secret)
			if err != nil {
				t.Fatal(err)
			}
			defer closeSession()
			material, err := f.controller.material(b.intent)
			if err != nil {
				t.Fatal(err)
			}
			treeBefore := inspectorTree(t, f.paths.State)
			out, err := f.controller.mutateSession(session, b.intent, material, reg, sha, action)
			if (err != nil) != expectedErr || (out.Receipt != nil) != expectedReceipt || (out.Current != nil) != expectedCurrent {
				t.Fatalf("outcome receipt=%v current=%v err=%v", out.Receipt != nil, out.Current != nil, err)
			}
			if err != nil && (strings.Contains(err.Error(), b.secret) || strings.Contains(err.Error(), "fixture-body-must-not-leak")) {
				t.Fatal("private material leaked")
			}
			if noPost && !errors.Is(err, ErrMutationConflict) {
				t.Fatal("known conflict not distinguished", err)
			}
			if expectedReceipt && *out.Receipt != result {
				t.Fatal("receipt changed")
			}
			if expectedCurrent && out.Current.State != after.State {
				t.Fatal("historical receipt confused with current")
			}
			conn.Close()
			select {
			case r := <-done:
				if r.err != nil || r.n != expectedRequests {
					t.Fatal("request sequence/retry", r.n, r.err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("fixture not reaped")
			}
			if !reflect.DeepEqual(treeBefore, inspectorTree(t, f.paths.State)) {
				t.Fatal("mutation session changed store")
			}
		})
	}
}

// Empty in the parent; one actual child below claims birth then execs this same
// fixed test binary. The controller responses remain synthetic HTTP fixtures.
func TestMihomoMutationFixture(t *testing.T) {
	mode := os.Getenv("HOOPA_MUTATION_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "launcher" {
		intent, err := owned.ClaimLauncher(context.Background(), os.NewFile(3, "birth"), os.Getenv("HOOPA_MUTATION_INTENT_SHA"), os.Getenv("HOOPA_RUN_NONCE"))
		if err != nil {
			t.Fatal(err)
		}
		os.Setenv("HOOPA_MUTATION_FIXTURE", "core")
		if err = syscall.Exec(intent.Binary.Path, intent.Arguments, os.Environ()); err != nil {
			t.Fatal(err)
		}
		t.Fatal("exec returned")
	}
	if mode != "core" {
		t.Fatal("unknown fixture mode")
	}
	f := os.NewFile(4, "controller-listener")
	raw, err := net.FileListener(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	listener := raw.(*net.TCPListener)
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(20 * time.Second))
	go func() { io.Copy(io.Discard, os.Stdin); listener.Close() }()
	fmt.Printf("MUTATION_READY pid=%d\n", os.Getpid())
	for i := 0; ; i++ {
		conn, err := listener.AcceptTCP()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.Fatal(err)
		}
		raw, err := os.ReadFile("http-plan.json")
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var p mutationPlan
		if err = json.Unmarshal(raw, &p); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		n, err := mutationServe(conn, p)
		fmt.Printf("MUTATION_CONNECTION index=%d requests=%d ok=%t\n", i, n, err == nil)
		if err != nil {
			t.Fatal(err)
		}
	}
}

type registeredMutationFixture struct {
	f        *inspectorFixture
	owner    *owned.Ownership
	run      *owned.OwnedRun
	reg      config.RunRegistration
	sha      string
	log      string
	cmd      *exec.Cmd
	expected []int
}

func startRegisteredMutationFixture(t *testing.T) *registeredMutationFixture {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	b := inspectorBlueprintFor(t, ln.Addr().String(), config.BinaryReference{Path: exe, Version: "synthetic-go-fixture-v1", SHA256: sha256String(binary)}, nil)
	work := filepath.Join(t.TempDir(), "work")
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	b.intent.Workdir = work
	b.intent.Arguments = []string{exe, "-test.run=^TestMihomoMutationFixture$", "-test.timeout=30s"}
	f := inspectorPublish(t, b)
	b = f.blueprint
	fixture := &registeredMutationFixture{f: f, log: filepath.Join(work, "child.log"), expected: []int{3}}
	fixture.plan(t, mutationPlan{Secret: b.secret, Address: b.intent.Controller, Steps: mutationStatusSteps(t, b, inspectorStatus(t, b, "closed"))})
	ticket, err := f.store.PrepareRun(b.intent)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := ticket.TakeBirthDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	listener, err := ln.File()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cmd := exec.Command(exe, b.intent.Arguments[1:]...)
	cmd.Dir = work
	cmd.ExtraFiles = []*os.File{directory, listener}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "HOOPA_MUTATION_") && !strings.HasPrefix(item, "HOOPA_RUN_NONCE=") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "HOOPA_MUTATION_FIXTURE=launcher", "HOOPA_MUTATION_INTENT_SHA="+ticket.Digest(), "HOOPA_RUN_NONCE="+strings.Repeat("ab", 32))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(fixture.log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = output
	cmd.Stderr = output
	if err = cmd.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	output.Close()
	fixture.cmd = cmd
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waited := false
	t.Cleanup(func() {
		if fixture.run != nil {
			fixture.run.Close()
		}
		stdin.Close()
		if !waited {
			select {
			case e := <-exited:
				waited = true
				if e != nil {
					t.Error("synthetic child exit", e)
				}
			case <-time.After(4 * time.Second):
				cmd.Process.Kill()
				e := <-exited
				waited = true
				t.Error("synthetic child required kill", e)
			}
		}
		log, e := os.ReadFile(fixture.log)
		if e != nil {
			t.Error(e)
		} else {
			t.Logf("actual synthetic child original output:\n%s", log)
			lines := []string{}
			for _, line := range strings.Split(string(log), "\n") {
				if strings.HasPrefix(line, "MUTATION_CONNECTION ") {
					lines = append(lines, line)
				}
			}
			if len(lines) != len(fixture.expected) {
				t.Errorf("controller connection count: got %d want %d", len(lines), len(fixture.expected))
			}
			for i, n := range fixture.expected {
				if i < len(lines) && lines[i] != fmt.Sprintf("MUTATION_CONNECTION index=%d requests=%d ok=true", i, n) {
					t.Error("controller request sequence differed")
				}
			}
		}
		t.Logf("actual synthetic child PID=%d same-PID launcher exec; Wait exit=%d; not Mihomo", cmd.Process.Pid, cmd.ProcessState.ExitCode())
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(fixture.log)
		if strings.Contains(string(raw), fmt.Sprintf("MUTATION_READY pid=%d", cmd.Process.Pid)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic child ready timeout")
		}
		select {
		case e := <-exited:
			waited = true
			t.Fatal("synthetic child exited before ready", e)
		case <-time.After(5 * time.Millisecond):
		}
	}
	fixture.owner, err = owned.NewOwnership(f.store, f.controller)
	if err != nil {
		t.Fatal(err)
	}
	fixture.run, fixture.sha, err = fixture.owner.Register(inspectorContext(t), b.intent.RunID, b.intent.OperationID, true)
	if err != nil {
		t.Fatal("real fixture ownership registration", err)
	}
	fixture.reg, _, err = f.store.ReadRunRegistration(b.intent.RunID, b.intent.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.reg.PID != cmd.Process.Pid || fixture.reg.Gate != "closed" {
		t.Fatal("registration did not bind actual fixture")
	}
	return fixture
}
func (f *registeredMutationFixture) plan(t *testing.T, p mutationPlan) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.f.blueprint.intent.Workdir, "http-plan.json"), inspectorJSON(t, p), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *registeredMutationFixture) round(t *testing.T, reg config.RunRegistration, sha, action string, before, after wire.Status, result wire.Result, code int) (MutationOutcome, error) {
	t.Helper()
	b := f.f.blueprint
	f.plan(t, mutationRound(t, b, reg, sha, action, before, after, result, code))
	f.expected = append(f.expected, 7)
	treeBefore := inspectorTree(t, f.f.paths.State)
	var out MutationOutcome
	var err error
	if action == "activate" {
		out, err = f.f.controller.Activate(inspectorContext(t), f.run, reg.OperationID)
	} else {
		out, err = f.f.controller.Drain(inspectorContext(t), f.run, reg.OperationID)
	}
	if !reflect.DeepEqual(treeBefore, inspectorTree(t, f.f.paths.State)) {
		t.Fatal("public mutation changed durable tree")
	}
	return out, err
}
func TestMihomoMutationRegisteredPublic(t *testing.T) {
	for _, name := range []string{"lifecycle-replays", "verified-transition-failed"} {
		t.Run(name, func(t *testing.T) {
			f := startRegisteredMutationFixture(t)
			b := f.f.blueprint
			request := mutationRequest(t, b, f.reg, f.sha, "activate")
			result := mutationResult(t, request, "opened")
			closed := inspectorStatus(t, b, "closed")
			open := inspectorStatus(t, b, "open")
			open.Activation = &result
			if name == "verified-transition-failed" {
				result.Result = "failed"
				degraded := inspectorStatus(t, b, "degraded")
				degraded.Activation = &result
				out, err := f.round(t, f.reg, f.sha, "activate", closed, degraded, result, 503)
				if !errors.Is(err, ErrTransitionFailed) || out.Receipt == nil || *out.Receipt != result || out.Current == nil || out.Current.State != "degraded" {
					t.Fatal("verified failure lost outcome", err)
				}
				return
			}
			out, err := f.round(t, f.reg, f.sha, "activate", closed, open, result, 200)
			if err != nil || out.Receipt == nil || *out.Receipt != result || out.Current == nil || out.Current.State != "open" {
				t.Fatal("activation combination", err)
			}
			out, err = f.round(t, f.reg, f.sha, "activate", open, open, result, 200)
			if err != nil || out.Receipt == nil || *out.Receipt != result {
				t.Fatal("explicit identical replay", err)
			}
			// A distinct drain operation obtains its own fresh registration on the same
			// actual birth/process, independently inspecting current open state first.
			f.plan(t, mutationPlan{Secret: b.secret, Address: b.intent.Controller, Steps: mutationStatusSteps(t, b, open)})
			f.expected = append(f.expected, 3)
			drainRun, drainSHA, err := f.owner.Register(inspectorContext(t), b.intent.RunID, "operation:drain", false)
			if err != nil {
				t.Fatal(err)
			}
			drainRun.Close()
			drainReg, _, err := f.f.store.ReadRunRegistration(b.intent.RunID, "operation:drain")
			if err != nil || drainReg.Gate != "open" || drainReg.PID != f.reg.PID || drainReg.BirthSHA256 != f.reg.BirthSHA256 {
				t.Fatal("drain registration binding", err)
			}
			drainRequest := mutationRequest(t, b, drainReg, drainSHA, "drain")
			drainResult := mutationResult(t, drainRequest, "draining")
			draining := open
			draining.State = "draining"
			draining.Drain = &drainResult
			out, err = f.round(t, drainReg, drainSHA, "drain", open, draining, drainResult, 200)
			if err != nil || out.Receipt == nil || *out.Receipt != drainResult || out.Current == nil || out.Current.State != "draining" {
				t.Fatal("drain combination", err)
			}
			out, err = f.round(t, f.reg, f.sha, "activate", draining, draining, result, 200)
			if err != nil || out.Receipt == nil || out.Receipt.Result != "opened" || out.Current == nil || out.Current.State != "draining" {
				t.Fatal("old activation receipt reopened/confused gate", err)
			}
			// A verified POST receipt survives a bad subsequent observation; Current
			// must not be exposed by the public wrapper on this error.
			plan := mutationRound(t, b, f.reg, f.sha, "activate", draining, draining, result, 200)
			bad := draining
			bad.ConfigSHA256 = strings.Repeat("9", 64)
			plan.Steps[4].Response = responseBytes(inspectorJSON(t, bad))
			plan.Steps = plan.Steps[:5]
			f.plan(t, plan)
			f.expected = append(f.expected, 5)
			before := inspectorTree(t, f.f.paths.State)
			out, err = f.f.controller.Activate(inspectorContext(t), f.run, f.reg.OperationID)
			if err == nil || out.Receipt == nil || *out.Receipt != result || out.Current != nil {
				t.Fatal("post-failure receipt/current separation", err)
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.f.paths.State)) {
				t.Fatal("failed public observation wrote store")
			}
		})
	}
}
