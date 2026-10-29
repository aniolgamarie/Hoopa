//go:build linux

package mihomoevents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestControlStoreExclusiveAndInit(t *testing.T) {
	p, _, dir := controlOpen(t)
	if _, e := OpenControlProbe(dir, &controlFake{}); e == nil {
		t.Fatal("second writer")
	}
	if e := InitControlProbe(dir); e == nil {
		t.Fatal("initialization overwrote existing instance")
	}
	if e := p.Close(); e != nil {
		t.Fatal(e)
	}
	q, e := OpenControlProbe(dir, &controlFake{})
	if e != nil {
		t.Fatal(e)
	}
	q.Close()
}
func TestControlStoreUncertaintyPoisons(t *testing.T) {
	for _, point := range []string{"write", "file-sync", "rename", "directory-sync"} {
		t.Run(point, func(t *testing.T) {
			p, f, d := controlOpen(t)
			p.store.fault = func(s string) error {
				if s == point {
					return errors.New("injected uncertain storage")
				}
				return nil
			}
			_, e := p.Execute(context.Background(), controlApply("first", 0, "/core"))
			if !errors.Is(e, ErrControlPoisoned) || !p.poisoned {
				t.Fatalf("%v", e)
			}
			before := controlTree(t, d)
			if _, e = p.Execute(context.Background(), controlApply("next", 0, "/core")); !errors.Is(e, ErrControlPoisoned) {
				t.Fatal(e)
			}
			if _, e = p.Recover(context.Background(), "first", 0); !errors.Is(e, ErrControlPoisoned) {
				t.Fatal(e)
			}
			if f.starts != 0 || !reflect.DeepEqual(before, controlTree(t, d)) {
				t.Fatal("poison did not stop writes")
			}
			if _, e = p.Status(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestControlStoreRejectsUnsafeAndCorrupt(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "unknown-version", "unknown-field", "duplicate", "trailing", "oversize", "operation-digest", "missing-launch", "bundle-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			p, _, d := controlOpen(t)
			if kind == "operation-digest" || kind == "missing-launch" || kind == "bundle-mismatch" {
				if _, e := p.Execute(context.Background(), controlApply("first", 0, "/core")); e != nil {
					t.Fatal(e)
				}
			}
			p.Close()
			path := filepath.Join(d, "active.json")
			switch kind {
			case "mode":
				os.Chmod(path, 0644)
			case "symlink":
				os.Remove(path)
				os.Symlink("/does-not-exist", path)
			case "unknown-version":
				os.WriteFile(path, []byte(`{"version":2,"revision":0,"desired":"stopped"}`), 0600)
			case "unknown-field":
				os.WriteFile(path, []byte(`{"version":1,"revision":0,"desired":"stopped","extra":0}`), 0600)
			case "duplicate":
				os.WriteFile(path, []byte(`{"version":1,"version":1,"revision":0,"desired":"stopped"}`), 0600)
			case "trailing":
				os.WriteFile(path, []byte(`{"version":1,"revision":0,"desired":"stopped"}{}`), 0600)
			case "oversize":
				os.WriteFile(path, []byte(strings.Repeat(" ", 65537)), 0600)
			case "operation-digest":
				var o ControlOperation
				b, _ := os.ReadFile(filepath.Join(d, "operations/first.json"))
				json.Unmarshal(b, &o)
				o.RequestDigest = strings.Repeat("0", 64)
				b, _ = json.Marshal(o)
				os.WriteFile(filepath.Join(d, "operations/first.json"), b, 0600)
			case "missing-launch":
				var a ControlActive
				b, _ := os.ReadFile(path)
				json.Unmarshal(b, &a)
				os.Remove(filepath.Join(d, "launches", a.Launch.Nonce+".json"))
			case "bundle-mismatch":
				entries, _ := os.ReadDir(filepath.Join(d, "bundles"))
				os.WriteFile(filepath.Join(d, "bundles", entries[0].Name()), []byte(`{}`), 0600)
			}
			if q, e := OpenControlProbe(d, &controlFake{}); e == nil {
				q.Close()
				t.Fatal("accepted " + kind)
			}
		})
	}
}
func TestControlProbeStrictRequests(t *testing.T) {
	r := controlApply("first", 0, "/core")
	raw, _ := json.Marshal(r)
	if _, e := DecodeControlRequest(raw); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"version":1,"version":1}`, `{"version":1,"\u0076ersion":1}`, `{"extra":1}`, `{} {}`, `{"version":2}`, `{"version":1,"request_id":"good","action":"stop","accept_disruption":true}`, `{"version":1,"request_id":"good","action":"stop","expected_revision":null,"accept_disruption":true}`, `{"version":1,"request_id":"good","action":"stop","expected_revision":0,"accept_disruption":true,"bundle":null}`, `{"version":1,"request_id":"../bad","action":"stop","accept_disruption":true}`, `{"version":1,"request_id":"good","action":"stop","accept_disruption":false}`, `{"version":1,"request_id":"good","action":"stop","accept_disruption":true,"bundle":{}}`} {
		if _, e := DecodeControlRequest([]byte(raw)); e == nil {
			t.Fatal("accepted " + raw)
		}
	}
}
func TestControlProbeLimitAndInputIsolation(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	r := controlApply("first", 0, "/core")
	r.Bundle.RouteMap = []byte("{ \"nodes\" : [] }\n")
	beforeConfig := append([]byte(nil), r.Bundle.Config...)
	op, e := p.Execute(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	r.Bundle.Config[0] = 'X'
	r.Bundle.RouteMap[0] = 'X'
	op.Request.Bundle.Config[0] = 'Y'
	s, _ := p.Status(ctx)
	if !reflect.DeepEqual(s.Active.Bundle.Config, beforeConfig) || string(s.Active.Bundle.RouteMap) != "{ \"nodes\" : [] }\n" {
		t.Fatal("input alias or original template changed")
	}
	for i := 1; i < 32; i++ {
		id := strings.Repeat("x", i)
		if _, e = p.Execute(ctx, ControlRequest{Version: 1, RequestID: id, Action: "stop", ExpectedRevision: uint64(i), AcceptDisruption: true}); e != nil {
			t.Fatal(e)
		}
	}
	before := controlTree(t, d)
	if _, e = p.Execute(ctx, ControlRequest{Version: 1, RequestID: "overflow", Action: "stop", ExpectedRevision: 32, AcceptDisruption: true}); e == nil || !reflect.DeepEqual(before, controlTree(t, d)) {
		t.Fatal("operation limit")
	}
	if _, e = p.Execute(ctx, controlApply("first", 0, "/core")); !errors.Is(e, ErrControlIDConflict) {
		t.Fatal("old IDs forgotten")
	}
	if f.starts != 1 {
		t.Fatal("unexpected start")
	}
}
func TestControlProbeConcurrentCloseStatusExecute(t *testing.T) {
	p, _, _ := controlOpen(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Status(context.Background())
			p.Execute(context.Background(), controlApply("first", 0, "/core"))
			p.Close()
		}()
	}
	wg.Wait()
	if _, e := p.Status(context.Background()); !errors.Is(e, ErrControlClosed) {
		t.Fatal(e)
	}
}

func TestControlStoreMissingFieldsRejectWithoutPanic(t *testing.T) {
	for _, kind := range []string{"active-null", "active-running-no-bundle", "operation-no-base", "operation-no-request-bundle", "operation-no-target", "operation-unknown-step", "orphan-unknown-version", "unknown-root"} {
		t.Run(kind, func(t *testing.T) {
			p, _, d := controlOpen(t)
			if _, e := p.Execute(context.Background(), controlApply("first", 0, "/core")); e != nil {
				t.Fatal(e)
			}
			p.Close()
			active := filepath.Join(d, "active.json")
			opPath := filepath.Join(d, "operations/first.json")
			var o map[string]any
			raw, _ := os.ReadFile(opPath)
			json.Unmarshal(raw, &o)
			switch kind {
			case "active-null":
				os.WriteFile(active, []byte(`null`), 0600)
			case "active-running-no-bundle":
				os.WriteFile(active, []byte(`{"version":1,"revision":1,"desired":"running"}`), 0600)
			case "operation-no-base":
				delete(o, "base")
			case "operation-no-request-bundle":
				o["request"].(map[string]any)["bundle"] = nil
			case "operation-no-target":
				delete(o, "target")
			case "operation-unknown-step":
				o["step"] = "unexpected"
			case "orphan-unknown-version":
				os.WriteFile(filepath.Join(d, "bundles/orphan.json"), []byte(`{"version":2}`), 0600)
			case "unknown-root":
				os.WriteFile(filepath.Join(d, "unexpected.json"), []byte(`{}`), 0600)
			}
			if strings.HasPrefix(kind, "operation-") {
				raw, _ = json.Marshal(o)
				if e := os.WriteFile(opPath, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if q, e := OpenControlProbe(d, &controlFake{}); e == nil {
				q.Close()
				t.Fatal("accepted missing/corrupt fields")
			}
		})
	}
}
