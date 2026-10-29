//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"hoopa/internal/config"
)

func localInitOptions(t *testing.T, apps []config.App) InitOptions {
	t.Helper()
	intent := config.Intent{SchemaVersion: 1, Core: config.IntentCore{Mihomo: config.BinaryReference{Path: "/nonexistent/hoopa-test-core", Version: "1.2.3", SHA256: strings.Repeat("a", 64)}}, Subscriptions: []config.Subscription{}, Groups: []config.Group{}, Apps: apps, Rules: []config.Rule{}, DefaultTarget: config.Target{Kind: "reject"}, Retention: config.Retention{Days: 30}}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	return InitOptions{MihomoPath: intent.Core.Mihomo.Path, Intent: raw, AppPorts: map[config.ID]uint16{}}
}
func localInitPaths(t *testing.T) config.Paths {
	t.Helper()
	p, err := config.ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func localInitRead(t *testing.T, p config.Paths) *config.Store {
	t.Helper()
	s, err := config.OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func localInitApply(t *testing.T, p config.Paths, o InitOptions) InitReport {
	t.Helper()
	r, err := InitializeLocal(context.Background(), p, o)
	if err != nil || !r.Prepared || r.DraftSHA256 == nil || r.Revision == nil {
		t.Fatal("local preparation", r, err)
	}
	return r
}

func TestLocalInitMissingInvalidCancelledZeroWrites(t *testing.T) {
	p := localInitPaths(t)
	before := coordinatorTree(t, p)
	r, err := InitializeLocal(context.Background(), p, InitOptions{MihomoPath: "/missing/core"})
	if err != nil || r.Prepared || r.InputState != "incomplete" || !reflect.DeepEqual(r.Missing, []string{"intent"}) {
		t.Fatal(r, err)
	}
	if _, err = InitializeLocal(context.Background(), p, InitOptions{MihomoPath: "relative"}); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = InitializeLocal(ctx, p, localInitOptions(t, []config.App{})); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = InitializeLocal(nil, p, localInitOptions(t, []config.App{})); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("incomplete/invalid/cancelled created state")
	}
}

func TestLocalInitOfflineDraftHistoryAndIdempotence(t *testing.T) {
	p := localInitPaths(t)
	o := localInitOptions(t, []config.App{{ID: "shell", Mode: "direct", Bypass: []string{}}})
	r := localInitApply(t, p, o)
	if r.AppEntriesReady != 0 || len(r.Dependencies) != 1 || r.Dependencies[0].State != "unavailable" || !reflect.DeepEqual(r.Missing, []string{"source_settings"}) {
		t.Fatal(r)
	}
	s := localInitRead(t, p)
	a, err := s.ReadActive()
	if err != nil || a.Generation != nil || a.OperationID != nil || a.DesiredRuntime != "stopped" || a.Revision != *r.Revision {
		t.Fatal(a, err)
	}
	raw, sha, err := s.ReadDraft()
	want, _ := config.CanonicalJSON(o.Intent)
	if err != nil || !bytes.Equal(raw, want) || sha != *r.DraftSHA256 {
		t.Fatal(err)
	}
	id, err := s.ReadLaunchHistoryID()
	if err != nil || id == "" {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	again := localInitApply(t, p, o)
	if !reflect.DeepEqual(r, again) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("identical init replaced input/identity")
	}
	if _, _, err = s.ReadSourceSettings(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invented source settings", err)
	}
}

func TestLocalInitManagedInputsReuseAndConflicts(t *testing.T) {
	p := localInitPaths(t)
	o := localInitOptions(t, []config.App{{ID: "a", Mode: "managed", Bypass: []string{}}, {ID: "b", Mode: "managed", Bypass: []string{}}})
	o.ControllerPort = 32000
	o.SharedPort = 32001
	o.AppPorts = map[config.ID]uint16{"a": 32002, "b": 32003}
	r := localInitApply(t, p, o)
	if len(r.Missing) != 0 || r.AppEntriesReady != 2 {
		t.Fatal(r)
	}
	s := localInitRead(t, p)
	raw, sha, err := s.ReadSourceSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := DecodeSourceSettings(raw)
	if err != nil || settings.Digest() != sha {
		t.Fatal(err)
	}
	bindings, err := s.ListAppBindings()
	if err != nil || len(bindings) != 2 {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	if !reflect.DeepEqual(r, localInitApply(t, p, o)) || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("retry regenerated credentials")
	}
	// Omitted ports reuse immutable input; no random port selection.
	omitted := o
	omitted.ControllerPort = 0
	omitted.SharedPort = 0
	omitted.AppPorts = nil
	if got := localInitApply(t, p, omitted); got.AppEntriesReady != 2 {
		t.Fatal(got)
	}
	for _, change := range []string{"controller", "shared", "entry"} {
		t.Run(change, func(t *testing.T) {
			changed := o
			changed.AppPorts = map[config.ID]uint16{"a": 32002, "b": 32003}
			switch change {
			case "controller":
				changed.ControllerPort = 32100
			case "shared":
				changed.SharedPort = 32101
			case "entry":
				changed.AppPorts["a"] = 32102
			}
			before := coordinatorTree(t, p)
			if _, err := InitializeLocal(context.Background(), p, changed); !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("known conflict changed durable input")
			}
		})
	}
	// A removed app still reserves its existing entry endpoint.
	changed := localInitOptions(t, []config.App{{ID: "c", Mode: "managed", Bypass: []string{}}})
	changed.AppPorts = map[config.ID]uint16{"c": 32002}
	before = coordinatorTree(t, p)
	if _, err := InitializeLocal(context.Background(), p, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("retained collision", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("collision replaced draft")
	}
}

func TestLocalInitMissingInputsMeteringAndLostEvidence(t *testing.T) {
	p := localInitPaths(t)
	o := localInitOptions(t, []config.App{{ID: "a", Mode: "managed", Bypass: []string{}}, {ID: "metered", Mode: "managed", Metering: true, Bypass: []string{}}})
	r := localInitApply(t, p, o)
	if !reflect.DeepEqual(r.Missing, []string{"source_settings", "app_entries", "metering_backend"}) || !reflect.DeepEqual(r.MissingAppIDs, []config.ID{"a", "metered"}) {
		t.Fatal(r)
	}
	o.ControllerPort = 32000
	o.SharedPort = 32001
	o.AppPorts = map[config.ID]uint16{"a": 32002}
	localInitApply(t, p, o)
	if err := os.Remove(filepath.Join(p.State, "credentials", "source-settings-v1.json")); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	o.ControllerPort = 0
	o.SharedPort = 0
	o.AppPorts = nil
	if _, err := InitializeLocal(context.Background(), p, o); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal("lost identity accepted", err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("lost settings reset")
	}
}

func TestLocalInitWriterBusyAndMarkedHistoryLoss(t *testing.T) {
	p := localInitPaths(t)
	o := localInitOptions(t, []config.App{})
	localInitApply(t, p, o)
	s, err := config.OpenStore(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = InitializeLocal(context.Background(), p, o); !errors.Is(err, config.ErrWriterBusy) {
		t.Fatal(err)
	}
	s.Close()
	if err = os.RemoveAll(filepath.Join(p.State, "app-launches")); err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, p)
	if _, err = InitializeLocal(context.Background(), p, o); !errors.Is(err, config.ErrIntegrity) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("marked history recreated")
	}
}

func TestInitDependencyInspectionNeverExecutes(t *testing.T) {
	base := t.TempDir()
	sentinel := filepath.Join(base, "executed")
	path := filepath.Join(base, "core")
	raw := []byte("#!/bin/sh\nprintf executed > '" + sentinel + "'\n")
	if err := os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	ref := config.BinaryReference{Path: path, Version: "declared-1", SHA256: hex.EncodeToString(sum[:])}
	out, err := inspectInitDependency(context.Background(), "mihomo", ref)
	if err != nil || out.State != "available" || out.Version == nil || *out.Version != ref.Version {
		t.Fatal(out, err)
	}
	if _, err = os.Lstat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dependency executed", err)
	}
	ref.SHA256 = strings.Repeat("a", 64)
	out, err = inspectInitDependency(context.Background(), "mihomo", ref)
	if err != nil || out.State != "mismatch" || out.Version != nil {
		t.Fatal(out, err)
	}
	for _, kind := range []string{"directory", "fifo", "missing", "large"} {
		t.Run(kind, func(t *testing.T) {
			ref.Path = filepath.Join(base, kind)
			reason := "not_readable"
			switch kind {
			case "directory":
				if err := os.Mkdir(ref.Path, 0700); err != nil {
					t.Fatal(err)
				}
				reason = "not_regular"
			case "fifo":
				if err := syscall.Mkfifo(ref.Path, 0600); err != nil {
					t.Fatal(err)
				}
				reason = "not_regular"
			case "large":
				f, err := os.OpenFile(ref.Path, os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Truncate((128 << 20) + 1); err != nil {
					t.Fatal(err)
				}
				f.Close()
				reason = "too_large"
			}
			out, err := inspectInitDependency(context.Background(), "mihomo", ref)
			if err != nil || out.State != "unavailable" || out.Reason != reason || out.SHA256 != nil || out.Version != nil {
				t.Fatal(out, err)
			}
		})
	}
}

func TestLocalInitCorruptHistoryRefusesNewCredentialsBeforeWrites(t *testing.T) {
	p := localInitPaths(t)
	o := localInitOptions(t, []config.App{{ID: "a", Mode: "managed", Bypass: []string{}}})
	localInitApply(t, p, o)
	marker := filepath.Join(p.State, "credentials", "app-launch-history-v1.json")
	if err := os.WriteFile(marker, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	o.ControllerPort, o.SharedPort = 32000, 32001
	o.AppPorts = map[config.ID]uint16{"a": 32002}
	before := coordinatorTree(t, p)
	if _, err := InitializeLocal(context.Background(), p, o); !errors.Is(err, config.ErrIntegrity) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("known history corruption still published credentials")
	}
}

func TestInitializeLaunchHistoryCoordinatorGate(t *testing.T) {
	c, p := coordinatorFixture(t, nil)
	revision := initializeRevision(t, c)
	before := coordinatorTree(t, p)
	if err := c.InitializeLaunchHistory(context.Background(), "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.InitializeLaunchHistory(ctx, revision); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("gate wrote")
	}
	if err := c.InitializeLaunchHistory(context.Background(), revision); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := c.InitializeLaunchHistory(context.Background(), revision); !errors.Is(err, config.ErrClosed) {
		t.Fatal(err)
	}
}
