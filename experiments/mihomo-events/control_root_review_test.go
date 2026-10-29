//go:build linux

package mihomoevents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestControlStoreRuntimeMaterialIsNotControlRecord(t *testing.T) {
	p, f, d := controlOpen(t)
	if _, err := p.Execute(context.Background(), controlApply("first", 0, "/core")); err != nil {
		t.Fatal(err)
	}
	runtime := p.active.Launch.Directory
	if err := os.Mkdir(filepath.Join(runtime, "trace"), 0700); err != nil {
		t.Fatal(err)
	}
	// Source journals have an independent 8 MiB budget. No fake backend
	// test claims that these bytes are a valid source journal.
	if err := os.WriteFile(filepath.Join(runtime, "trace", "source.frames"), make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	before := controlTree(t, d)
	q, err := OpenControlProbe(d, f)
	if err != nil {
		t.Fatal(err)
	}
	q.Close()
	if !reflect.DeepEqual(before, controlTree(t, d)) || f.starts != 1 || f.stops != 0 {
		t.Fatal("reopen changed runtime or control material")
	}
	if err := os.Chmod(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if q, err = OpenControlProbe(d, f); err == nil {
		q.Close()
		t.Fatal("unsafe runtime boundary accepted")
	}
}

func TestControlStoreRejectsUnrelatedActiveDuringPending(t *testing.T) {
	p, f, d := controlOpen(t)
	p.hook = func(point string) error {
		if point == "recorded" {
			return errors.New("crash after durable operation")
		}
		return nil
	}
	if _, err := p.Execute(context.Background(), controlApply("first", 0, "/core")); err == nil {
		t.Fatal("missing injected failure")
	}
	p.Close()
	// Individually well-formed, but neither the operation base nor a possible
	// publication. Recovery must not overwrite it by returning to revision 1.
	raw, err := json.Marshal(ControlActive{Version: 1, Revision: 9, Desired: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "active.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := controlTree(t, d)
	if q, err := OpenControlProbe(d, f); err == nil {
		q.Close()
		t.Fatal("unrelated active accepted")
	}
	if !reflect.DeepEqual(before, controlTree(t, d)) || f.starts != 0 || f.stops != 0 {
		t.Fatal("rejected open wrote or invoked runtime actions")
	}
}

func TestControlFailedStartDoesNotReachSuccessfulSpawnWindow(t *testing.T) {
	p, f, _ := controlOpen(t)
	f.failPath = "/failed"
	hit := false
	p.hook = func(point string) error {
		if point == "spawn-returned-before-record" {
			hit = true
			return errors.New("successful start window reached")
		}
		return nil
	}
	o, err := p.Execute(context.Background(), controlApply("first", 0, "/failed"))
	if err != nil || hit || o.Phase != "rolled_back" || p.poisoned || f.starts != 1 {
		t.Fatalf("failed start misclassified: phase=%s hit=%v starts=%d err=%v", o.Phase, hit, f.starts, err)
	}
}

func TestControlStrictFieldNames(t *testing.T) {
	for _, raw := range []string{
		`{"version":2,"Version":1,"request_id":"case","action":"stop","expected_revision":0,"accept_disruption":true}`,
		`{"version":1,"request_id":"case","action":"stop","expected_revision":0,"accept_disruption":false,"Accept_Disruption":true}`,
	} {
		if _, err := DecodeControlRequest([]byte(raw)); err == nil {
			t.Fatal("case alias overwrote declared field")
		}
	}
	for _, raw := range []string{`{"items":[{"value":1,"Value":2}]}`, `{"items":[{}]}`, `{"items":[null]}`} {
		var out struct {
			Items []struct {
				Value int `json:"value"`
			} `json:"items"`
		}
		if err := controlDecode([]byte(raw), &out); err == nil {
			t.Fatal("invalid object in typed array accepted")
		}
	}
}
