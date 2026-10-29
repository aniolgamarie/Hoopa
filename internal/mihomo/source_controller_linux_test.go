//go:build linux && amd64

package mihomo

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	owned "hoopa/internal/runtime"
	sources "hoopa/protocol/sourcecontrol"
)

func sourceFixture(t *testing.T) (*net.TCPListener, *inspectorFixture, sources.Snapshot) {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	b, s := sourceBlueprint(t, ln.Addr().String())
	return ln, inspectorPublish(t, b), s
}
func TestSourceInspectSnapshotBindingsAndNoDowngrade(t *testing.T) {
	names := []string{"closed", "open", "late-open", "schema1-status", "schema1-snapshot", "mode1", "capabilities1", "reordered-capabilities", "wrong-run", "wrong-generation", "wrong-config", "wrong-binary", "wrong-snapshot-digest", "probe-policy-change", "dynamic-selected", "snapshot-body-extra", "nested-v1-receipt", "v2-route-404", "late-draining"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ln, f, snapshot := sourceFixture(t)
			b := f.blueprint
			state := "closed"
			if name == "open" || name == "nested-v1-receipt" {
				state = "open"
			}
			status := sourceStatus(t, b, snapshot, state)
			steps := sourceReadSteps(t, snapshot, status)
			wantCount := 3
			wantOK := name == "closed" || name == "open" || name == "late-open"
			wantGate := state
			switch name {
			case "late-open":
				steps[2].Response = responseBytes(inspectorJSON(t, sourceStatus(t, b, snapshot, "open")))
				wantGate = "open"
			case "schema1-status":
				status.SchemaVersion = 1
				status.Mode = "basic"
				status.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "schema1-snapshot":
				snapshot.SchemaVersion = 1
				steps[1].Response = responseBytes(inspectorJSON(t, snapshot))
				wantCount = 2
			case "mode1":
				status.Mode = "basic"
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "capabilities1":
				status.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "reordered-capabilities":
				status.Capabilities[0], status.Capabilities[1] = status.Capabilities[1], status.Capabilities[0]
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-run":
				status.RunID = "run:foreign"
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-generation":
				status.ConfigGeneration = "generation:foreign"
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-config":
				status.ConfigSHA256 = strings.Repeat("1", 64)
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-binary":
				status.BinaryVersion = "other-version"
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-snapshot-digest":
				status.SnapshotSHA256 = strings.Repeat("1", 64)
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "probe-policy-change":
				sourceProxy(&snapshot, "group:auto").Probe.IntervalSeconds++
				steps[1].Response = responseBytes(inspectorJSON(t, snapshot))
				wantCount = 2
			case "dynamic-selected":
				sourceProxy(&snapshot, "group:auto").Selected = "node:a"
				steps[1].Response = responseBytes(inspectorJSON(t, snapshot))
				wantCount = 2
			case "snapshot-body-extra":
				raw := inspectorJSON(t, snapshot)
				raw = append([]byte(`{"private-canary":true,`), raw[1:]...)
				steps[1].Response = responseBytes(raw)
				wantCount = 2
			case "nested-v1-receipt":
				status.Activation.SchemaVersion = 1
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "v2-route-404":
				steps[0].Response = []byte("HTTP/1.1 404 Not Found\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
				wantCount = 1
			case "late-draining":
				steps[2].Response = responseBytes(inspectorJSON(t, sourceStatus(t, b, snapshot, "draining")))
			}
			tree := inspectorTree(t, f.paths.State)
			var observation owned.ControllerObservation
			count, err := terminationExchange(t, ln, b, steps, func(conn *net.TCPConn) error {
				var err error
				observation, err = f.controller.Inspect(inspectorContext(t), conn, b.intent)
				return err
			})
			if count != wantCount || (err == nil) != wantOK {
				t.Fatalf("v2 inspect requests=%d want=%d success=%v err=%v", count, wantCount, wantOK, err)
			}
			if wantOK {
				if observation.Gate != wantGate || observation.RunID != b.intent.RunID || observation.ConfigSHA256 != b.intent.ConfigSHA256 {
					t.Fatal("v2 inspection omitted bound/current fields")
				}
			} else if !reflect.DeepEqual(observation, owned.ControllerObservation{}) || !errors.Is(err, ErrController) {
				t.Fatal("invalid v2 observation not zero or error not classified")
			}
			if !reflect.DeepEqual(tree, inspectorTree(t, f.paths.State)) {
				t.Fatal("read-only v2 inspect changed store")
			}
		})
	}
}
func TestSourceMaterialStrictV2AndCanonicalBytes(t *testing.T) {
	for _, name := range []string{"valid-whitespace", "schema1-bundle", "changed-controller", "changed-config", "dynamic-selected", "malformed-body"} {
		t.Run(name, func(t *testing.T) {
			b, s := sourceBlueprint(t, "127.0.0.1:31003")
			switch name {
			case "valid-whitespace":
				b.extra[SnapshotFile] = append([]byte(" \n"), inspectorJSON(t, s)...)
			case "schema1-bundle":
				s.SchemaVersion = 1
				b.extra[SnapshotFile] = inspectorJSON(t, s)
			case "changed-controller":
				s.Controller = "127.0.0.1:31004"
				b.extra[SnapshotFile] = inspectorJSON(t, s)
			case "changed-config":
				s.ConfigSHA256 = strings.Repeat("1", 64)
				b.extra[SnapshotFile] = inspectorJSON(t, s)
			case "dynamic-selected":
				sourceProxy(&s, "group:auto").Selected = "node:a"
				b.extra[SnapshotFile] = inspectorJSON(t, s)
			case "malformed-body":
				b.extra[SnapshotFile] = []byte(`{"private-canary":true}`)
			}
			f := inspectorPublish(t, b)
			material, err := f.controller.material(f.blueprint.intent)
			if name == "valid-whitespace" {
				digest, _ := s.Digest()
				if err != nil || string(material.snapshotJSON) != string(inspectorJSON(t, s)) || material.snapshotSHA != digest || material.secret != b.secret {
					t.Fatal("canonical source material rejected or incomplete", err)
				}
			} else if err == nil {
				t.Fatal("invalid expected v2 material accepted")
			}
		})
	}
}
func sourceSessionExchange(t *testing.T, ln *net.TCPListener, b inspectorBlueprint, steps []mutationStep, call func(*controllerSession) error) error {
	t.Helper()
	type report struct {
		n   int
		err error
	}
	done := make(chan report, 1)
	go func() {
		conn, err := ln.AcceptTCP()
		if err != nil {
			done <- report{err: err}
			return
		}
		n, err := mutationServe(conn, mutationPlan{Secret: b.secret, Address: b.intent.Controller, Steps: steps})
		done <- report{n, err}
	}()
	conn, err := net.DialTCP("tcp4", nil, ln.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	session, cleanup, err := beginControllerSession(inspectorContext(t), conn, b.intent.Controller, b.secret)
	if err != nil {
		t.Fatal(err)
	}
	callErr := call(session)
	if callErr != nil && (strings.Contains(callErr.Error(), b.secret) || strings.Contains(callErr.Error(), "private-canary")) {
		t.Fatal("private controller material echoed in error")
	}
	cleanup()
	conn.Close()
	select {
	case r := <-done:
		if r.err != nil || r.n != len(steps) {
			t.Fatal("v2 script request/path/body binding or request count", r.n, r.err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("v2 mutation script not reaped")
	}
	return callErr
}
func TestSourceMutationV2DigestPathAndRecoveryEvidence(t *testing.T) {
	names := []string{"activate", "drain", "explicit-replay", "historical-opened-current-draining", "failed-503", "v1-reply", "reply-extra-field", "wrong-request-digest", "lost-reply", "post-state-mismatch", "post-probe-mismatch", "conflicting-receipt"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			ln, f, s := sourceFixture(t)
			b := f.blueprint
			reg := mutationRegistration(b)
			registrationSHA := strings.Repeat("3", 64)
			action := "activate"
			if name == "drain" {
				action = "drain"
				reg.OperationID = "operation:drain"
			}
			q := sourceRequest(t, b, s, reg, registrationSHA, action)
			kind := "opened"
			if action == "drain" {
				kind = "draining"
			}
			result := sourceResult(t, q, kind)
			before := sourceStatus(t, b, s, "closed")
			after := sourceStatus(t, b, s, "open")
			after.Activation = &result
			if action == "drain" {
				before = sourceStatus(t, b, s, "open")
				after = before
				after.State = "draining"
				after.Drain = &result
			}
			wantErr := false
			wantReceipt, wantCurrent := true, true
			code := 200
			switch name {
			case "explicit-replay":
				before = after
			case "historical-opened-current-draining":
				before = after
				before.State = "draining"
				before.Drain = sourceStatus(t, b, s, "draining").Drain
				after = before
			case "failed-503":
				result.Result = "failed"
				code = 503
				after = sourceStatus(t, b, s, "degraded")
				after.Activation = &result
			case "v1-reply":
				result.SchemaVersion = 1
				wantErr = true
				wantReceipt, wantCurrent = false, false
			case "reply-extra-field":
				wantErr = true
				wantReceipt, wantCurrent = false, false
			case "wrong-request-digest":
				result.RequestSHA256 = strings.Repeat("5", 64)
				wantErr = true
				wantReceipt, wantCurrent = false, false
			case "lost-reply":
				wantErr = true
				wantReceipt, wantCurrent = false, false
			case "post-state-mismatch", "post-probe-mismatch":
				wantErr = true
				wantCurrent = false
			case "conflicting-receipt":
				before = after
				r := result
				r.RequestSHA256 = strings.Repeat("6", 64)
				before.Activation = &r
				wantErr = true
				wantReceipt, wantCurrent = false, false
			}
			steps := sourceReadSteps(t, s, before)
			steps = append(steps, mutationStep{Method: "POST", Path: "/hoopa/v2/" + action, Body: inspectorJSON(t, q), Response: mutationResponse(t, code, result)})
			steps = append(steps, sourceReadSteps(t, s, after)...)
			switch name {
			case "v1-reply", "wrong-request-digest":
				steps = steps[:4]
			case "reply-extra-field":
				raw := inspectorJSON(t, result)
				raw = append([]byte(`{"private-canary":true,`), raw[1:]...)
				steps[3].Response = responseBytes(raw)
				steps = steps[:4]
			case "lost-reply":
				steps = steps[:4]
				steps[3].Response = nil
			case "post-state-mismatch":
				bad := after
				bad.ConfigSHA256 = strings.Repeat("7", 64)
				steps[4].Response = responseBytes(inspectorJSON(t, bad))
				steps = steps[:5]
			case "post-probe-mismatch":
				bad := s
				sourceProxy(&bad, "group:auto").Probe.TimeoutMS++
				steps[5].Response = responseBytes(inspectorJSON(t, bad))
				steps = steps[:6]
			case "conflicting-receipt":
				steps = steps[:3]
			}
			material, err := f.controller.material(b.intent)
			if err != nil {
				t.Fatal(err)
			}
			tree := inspectorTree(t, f.paths.State)
			var outcome MutationOutcome
			err = sourceSessionExchange(t, ln, b, steps, func(session *controllerSession) error {
				var err error
				outcome, err = f.controller.mutateSession(session, b.intent, material, reg, registrationSHA, action)
				return err
			})
			if (err != nil) != wantErr || (outcome.Receipt != nil) != wantReceipt || (outcome.Current != nil) != wantCurrent {
				t.Fatalf("v2 outcome receipt=%v current=%v err=%v", outcome.Receipt != nil, outcome.Current != nil, err)
			}
			if wantReceipt && (outcome.Receipt.SchemaVersion != 2 || *outcome.Receipt != result) {
				t.Fatal("v2 mutation receipt changed/schema downgraded")
			}
			if wantCurrent && (outcome.Current.SchemaVersion != 2 || outcome.Current.State != after.State) {
				t.Fatal("historical receipt replaced current v2 state")
			}
			if name == "conflicting-receipt" && !errors.Is(err, ErrMutationConflict) {
				t.Fatal("known v2 request conflict not distinguished")
			}
			if name == "lost-reply" {
				// A lost reply is not proof of no remote effect. A separate explicit read
				// on the same declared endpoint may observe open; no automatic POST retry.
				current := sourceStatus(t, b, s, "open")
				count, e := terminationExchange(t, ln, b, sourceReadSteps(t, s, current), func(conn *net.TCPConn) error {
					o, e := f.controller.Inspect(inspectorContext(t), conn, b.intent)
					if e == nil && o.Gate != "open" {
						t.Error("explicit recovery read lost current gate")
					}
					return e
				})
				if e != nil || count != 3 {
					t.Fatal("explicit post-loss observation failed", e)
				}
			}
			if !reflect.DeepEqual(tree, inspectorTree(t, f.paths.State)) {
				t.Fatal("v2 mutation session changed local store")
			}
		})
	}
}
func TestSourceTerminationThreeGETBindingsAndFailedReceipts(t *testing.T) {
	for _, name := range []string{"closed", "open", "degraded-failed", "first-schema1", "second-schema1", "probe-drift", "dynamic-selected", "last-state-changed", "last-receipt-changed", "v1-activation", "wrong-request", "draining"} {
		t.Run(name, func(t *testing.T) {
			ln, f, s := sourceFixture(t)
			b := f.blueprint
			reg := mutationRegistration(b)
			reg.OperationID = b.intent.OperationID
			registrationSHA := strings.Repeat("3", 64)
			q := sourceRequest(t, b, s, reg, registrationSHA, "activate")
			result := sourceResult(t, q, "opened")
			status := sourceStatus(t, b, s, "closed")
			if name == "open" {
				status.State = "open"
				status.Activation = &result
			}
			if name == "degraded-failed" {
				result.Result = "failed"
				status.State = "degraded"
				status.Activation = &result
			}
			steps := sourceReadSteps(t, s, status)
			wantCount := 3
			wantOK := name == "closed" || name == "open" || name == "degraded-failed"
			switch name {
			case "first-schema1":
				status.SchemaVersion = 1
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "second-schema1":
				bad := s
				bad.SchemaVersion = 1
				steps[1].Response = responseBytes(inspectorJSON(t, bad))
				wantCount = 2
			case "probe-drift":
				bad := s
				sourceProxy(&bad, "group:auto").Probe.ToleranceMS++
				steps[1].Response = responseBytes(inspectorJSON(t, bad))
				wantCount = 2
			case "dynamic-selected":
				bad := s
				sourceProxy(&bad, "group:auto").Selected = "node:a"
				steps[1].Response = responseBytes(inspectorJSON(t, bad))
				wantCount = 2
			case "last-state-changed":
				last := status
				last.State = "open"
				last.Activation = &result
				steps[2].Response = responseBytes(inspectorJSON(t, last))
			case "last-receipt-changed":
				status.State = "open"
				status.Activation = &result
				steps = sourceReadSteps(t, s, status)
				last := status
				r := result
				r.OperationID = "operation:foreign"
				last.Activation = &r
				steps[2].Response = responseBytes(inspectorJSON(t, last))
			case "v1-activation":
				status.State = "degraded"
				result.Result = "failed"
				result.SchemaVersion = 1
				status.Activation = &result
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "wrong-request":
				status.State = "degraded"
				result.Result = "failed"
				result.RequestSHA256 = strings.Repeat("8", 64)
				status.Activation = &result
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			case "draining":
				status = sourceStatus(t, b, s, "draining")
				steps[0].Response = responseBytes(inspectorJSON(t, status))
				wantCount = 1
			}
			var observation owned.ControllerObservation
			count, err := terminationExchange(t, ln, b, steps, func(conn *net.TCPConn) error {
				var err error
				observation, err = f.controller.InspectTermination(inspectorContext(t), conn, b.intent, reg, registrationSHA)
				return err
			})
			if count != wantCount || (err == nil) != wantOK {
				t.Fatalf("termination requests=%d want=%d valid=%v err=%v", count, wantCount, wantOK, err)
			}
			if wantOK {
				if observation.Gate != status.State || observation.RunID != b.intent.RunID {
					t.Fatal("v2 termination lost exact current binding")
				}
			} else if !reflect.DeepEqual(observation, owned.ControllerObservation{}) || !errors.Is(err, ErrController) {
				t.Fatal("unverified termination observation survived")
			}
		})
	}
}
