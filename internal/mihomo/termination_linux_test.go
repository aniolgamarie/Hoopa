//go:build linux && amd64

package mihomo

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	wire "hoopa/protocol/corecontrol"
)

// Synthetic HTTP transport only: no process owner or native candidate proof.
// Unlike a server that closes after three replies, this records any extra POST
// or connection attempt until the caller releases its existing connection.
func terminationExchange(t *testing.T, ln *net.TCPListener, b inspectorBlueprint, steps []mutationStep, call func(*net.TCPConn) error) (int, error) {
	t.Helper()
	type report struct {
		methods, paths []string
		err            error
	}
	done := make(chan report, 1)
	go func() {
		r := report{}
		conn, e := ln.AcceptTCP()
		if e != nil {
			r.err = e
			done <- r
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		reader := bufio.NewReader(conn)
		for i := 0; ; i++ {
			req, e := http.ReadRequest(reader)
			if e == io.EOF {
				break
			}
			if e != nil {
				r.err = e
				break
			}
			r.methods = append(r.methods, req.Method)
			r.paths = append(r.paths, req.URL.Path)
			body, e := io.ReadAll(io.LimitReader(req.Body, 16))
			req.Body.Close()
			if e != nil || req.Method != "GET" || len(body) != 0 || req.Host != b.intent.Controller || req.URL.RawQuery != "" || req.Header.Get("Authorization") != "Bearer "+b.secret || i >= len(steps) || req.URL.Path != steps[i].Path {
				r.err = errors.New("unexpected synthetic read-only request")
				break
			}
			if _, e = conn.Write(steps[i].Response); e != nil {
				r.err = e
				break
			}
		}
		done <- r
	}()
	conn, e := net.DialTCP("tcp4", nil, ln.Addr().(*net.TCPAddr))
	if e != nil {
		t.Fatal(e)
	}
	callErr := call(conn)
	conn.Close()
	var r report
	select {
	case r = <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("synthetic server timeout")
	}
	if r.err != nil {
		t.Fatal("transport fixture", r.err)
	}
	for _, m := range r.methods {
		if m != "GET" {
			t.Fatal("termination wrote a mutation")
		}
	}
	ln.SetDeadline(time.Now().Add(15 * time.Millisecond))
	extra, e := ln.AcceptTCP()
	if e == nil {
		extra.Close()
		t.Fatal("termination reconnected")
	}
	if timeout, ok := e.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal("extra connection check", e)
	}
	return len(r.methods), callErr
}
func terminationFixture(t *testing.T) (*net.TCPListener, *inspectorFixture, config.RunRegistration, string) {
	t.Helper()
	ln, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	f := inspectorPublish(t, inspectorBlueprintFor(t, ln.Addr().String(), config.BinaryReference{}, nil))
	reg := mutationRegistration(f.blueprint)
	reg.OperationID = f.blueprint.intent.OperationID
	return ln, f, reg, strings.Repeat("3", 64)
}
func terminationStatus(t *testing.T, b inspectorBlueprint, reg config.RunRegistration, sha, state string) wire.Status {
	t.Helper()
	s := inspectorStatus(t, b, "closed")
	s.State = state
	if state == "open" || state == "degraded" {
		kind := "opened"
		if state == "degraded" {
			kind = "failed"
		}
		r := mutationResult(t, mutationRequest(t, b, reg, sha, "activate"), kind)
		s.Activation = &r
	}
	return s
}
func TestTerminationInspectorStatesAndOrdinaryBoundary(t *testing.T) {
	for _, state := range []string{"closed", "open", "degraded"} {
		t.Run(state, func(t *testing.T) {
			ln, f, reg, sha := terminationFixture(t)
			b := f.blueprint
			status := terminationStatus(t, b, reg, sha, state)
			steps := mutationStatusSteps(t, b, status)
			before := inspectorTree(t, f.paths.State)
			n, e := terminationExchange(t, ln, b, steps, func(c *net.TCPConn) error {
				obs, e := f.controller.InspectTermination(inspectorContext(t), c, b.intent, reg, sha)
				if e == nil && (obs.Gate != state || obs.RunID != b.intent.RunID || obs.Generation != b.intent.Generation || obs.ConfigSHA256 != b.intent.ConfigSHA256 || obs.BinaryVersion != b.intent.Binary.Version) {
					t.Fatal("termination observation binding")
				}
				return e
			})
			if e != nil || n != 3 {
				t.Fatal("valid exact termination proof rejected", n, e)
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("wrote Store")
			}
		})
	}
	t.Run("ordinary-Inspect-still-rejects-degraded", func(t *testing.T) {
		ln, f, reg, sha := terminationFixture(t)
		b := f.blueprint
		status := terminationStatus(t, b, reg, sha, "degraded")
		n, e := terminationExchange(t, ln, b, mutationStatusSteps(t, b, status), func(c *net.TCPConn) error {
			obs, e := f.controller.Inspect(inspectorContext(t), c, b.intent)
			if e != nil && obs.Gate != "" {
				t.Fatal("invalid ordinary observation escaped")
			}
			return e
		})
		if !errors.Is(e, ErrController) || n != 3 {
			t.Fatal("ordinary inspector widened", n, e)
		}
	})
}
func TestTerminationInspectorReceiptAndSnapshotRefusals(t *testing.T) {
	names := []string{"wrong-operation", "wrong-run", "wrong-request", "drain-present", "draining", "degraded-no-activation", "closed-to-open", "open-to-degraded", "first-wrong-receipt", "last-wrong-receipt", "snapshot-drift", "status-config-drift", "registration-sha-not-receipt"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ln, f, reg, sha := terminationFixture(t)
			b := f.blueprint
			first := terminationStatus(t, b, reg, sha, "degraded")
			last := first
			last.Activation = nil
			if first.Activation != nil {
				copy := *first.Activation
				last.Activation = &copy
			}
			wantRequests := 1
			switch name {
			case "wrong-operation":
				first.Activation.OperationID = "other-operation"
				last = first
			case "wrong-run":
				first.Activation.RunID = "other-run"
				last = first
			case "wrong-request":
				first.Activation.RequestSHA256 = strings.Repeat("4", 64)
				last = first
			case "drain-present":
				first.Activation = nil
				first.Drain = &wire.Result{SchemaVersion: 1, OperationID: "other-drain", RunID: string(b.intent.RunID), RequestSHA256: strings.Repeat("4", 64), Result: "failed"}
				last = first
			case "draining":
				first = inspectorStatus(t, b, "draining")
				last = first
			case "degraded-no-activation":
				first.Activation = nil
				last = first
			case "closed-to-open":
				first = terminationStatus(t, b, reg, sha, "closed")
				last = terminationStatus(t, b, reg, sha, "open")
				wantRequests = 3
			case "open-to-degraded":
				first = terminationStatus(t, b, reg, sha, "open")
				wantRequests = 3
			case "first-wrong-receipt":
				first.Activation.RequestSHA256 = strings.Repeat("5", 64)
			case "last-wrong-receipt":
				last.Activation.RequestSHA256 = strings.Repeat("5", 64)
				wantRequests = 3
			case "snapshot-drift":
				wantRequests = 2
			case "status-config-drift":
				first.ConfigSHA256 = strings.Repeat("4", 64)
				last = first
			case "registration-sha-not-receipt":
				sha = strings.Repeat("6", 64)
			}
			steps := mutationStatusSteps(t, b, first)
			steps[2] = mutationStatusSteps(t, b, last)[2]
			if name == "snapshot-drift" {
				changed := b.snapshot
				changed.Rules = append([]wire.Rule{}, b.snapshot.Rules...)
				changed.Rules[0].Target = "node:b"
				steps[1].Response = responseBytes(inspectorJSON(t, changed))
			}
			before := inspectorTree(t, f.paths.State)
			n, e := terminationExchange(t, ln, b, steps, func(c *net.TCPConn) error {
				obs, e := f.controller.InspectTermination(inspectorContext(t), c, b.intent, reg, sha)
				if e != nil && obs.Gate != "" {
					t.Fatal("unverified observation escaped")
				}
				return e
			})
			if !errors.Is(e, ErrController) || n != wantRequests {
				t.Fatal("unsafe termination proof accepted/wrong read sequence", n, e)
			}
			if strings.Contains(e.Error(), b.secret) || !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("secret leak or Store mutation")
			}
		})
	}
}
func TestTerminationInspectorRegistrationAndContextGuards(t *testing.T) {
	edits := map[string]func(*config.RunRegistration){"gate": func(r *config.RunRegistration) { r.Gate = "open" }, "operation": func(r *config.RunRegistration) { r.OperationID = "other-op" }, "run": func(r *config.RunRegistration) { r.RunID = "other-run" }, "generation": func(r *config.RunRegistration) { r.Generation = "other-generation" }, "config": func(r *config.RunRegistration) { r.ConfigSHA256 = strings.Repeat("4", 64) }, "manifest": func(r *config.RunRegistration) { r.ManifestSHA256 = strings.Repeat("4", 64) }, "binary": func(r *config.RunRegistration) { r.BinarySHA256 = strings.Repeat("4", 64) }, "nonce": func(r *config.RunRegistration) { r.NonceSHA256 = strings.Repeat("4", 64) }, "controller": func(r *config.RunRegistration) { r.Controller = "127.0.0.1:1" }, "invalid-shape": func(r *config.RunRegistration) { r.StartTicks = 0 }}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			ln, f, reg, sha := terminationFixture(t)
			edit(&reg)
			before := inspectorTree(t, f.paths.State)
			n, e := terminationExchange(t, ln, f.blueprint, nil, func(c *net.TCPConn) error {
				_, e := f.controller.InspectTermination(inspectorContext(t), c, f.blueprint.intent, reg, sha)
				return e
			})
			if !errors.Is(e, ErrController) || n != 0 {
				t.Fatal("bad registration reached controller", n, e)
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("guard wrote Store")
			}
		})
	}
	for _, name := range []string{"nil-context", "cancelled", "nil-connection", "bad-digest", "nil-controller"} {
		t.Run(name, func(t *testing.T) {
			ln, f, reg, sha := terminationFixture(t)
			n, e := terminationExchange(t, ln, f.blueprint, nil, func(c *net.TCPConn) error {
				ctx := inspectorContext(t)
				controller := f.controller
				switch name {
				case "nil-context":
					ctx = nil
				case "cancelled":
					cc, cancel := context.WithCancel(ctx)
					cancel()
					ctx = cc
				case "nil-connection":
					c = nil
				case "bad-digest":
					sha = "INVALID"
				case "nil-controller":
					controller = nil
				}
				_, e := controller.InspectTermination(ctx, c, f.blueprint.intent, reg, sha)
				return e
			})
			if !errors.Is(e, ErrController) || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}
