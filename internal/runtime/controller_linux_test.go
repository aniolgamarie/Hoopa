//go:build linux && amd64

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"hoopa/internal/config"
)

func TestRegisteredControllerOriginalHandle(t *testing.T) {
	f := startOwnershipFixture(t, "")
	inspector := &ownershipInspector{}
	owner := ownershipManager(t, f.store, inspector)
	run, sha, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	before := ownershipTree(t, f.paths.State)
	calls := 0
	var retained *net.TCPConn
	visit := func(ctx context.Context, conn *net.TCPConn, intent config.RunIntent, reg config.RunRegistration, digest string) error {
		calls++
		retained = conn
		original, h, e := f.store.ReadRunRegistration(f.intent.RunID, f.intent.OperationID)
		if e != nil || original != reg || digest != h || h != sha || !reflect.DeepEqual(intent, f.intent) {
			t.Fatal("callback registration/intent/hash drift", e)
		}
		// This uses the exact owner-proved socket passed to the callback.
		obs, e := inspector.Inspect(ctx, conn, intent)
		if e != nil || obs.RunID != intent.RunID {
			t.Fatal("exact socket failed", e)
		}
		intent.Arguments[0] = "detached-copy"
		return nil
	}
	if err = run.WithRegisteredController(ownershipContext(t), f.store, f.intent.OperationID, visit); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || run.intent.Arguments[0] != f.intent.Arguments[0] {
		t.Fatal("callback count/intent alias")
	}
	if _, err = retained.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatal("callback connection retained", err)
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("control callback wrote store")
	}
	forbid := func(context.Context, *net.TCPConn, config.RunIntent, config.RunRegistration, string) error {
		calls++
		return nil
	}
	check := func(label string, ctx context.Context, store *config.Store, op config.ID) {
		t.Helper()
		old := calls
		if e := run.WithRegisteredController(ctx, store, op, forbid); e == nil || calls != old {
			t.Fatal(label, "reached callback", e)
		}
	}
	check("missing registration", ownershipContext(t), f.store, "missing-operation")
	originalArgs := run.intent.Arguments
	run.intent.Arguments = append(append([]string(nil), originalArgs...), "extra-argument")
	check("wrong full argv", ownershipContext(t), f.store, f.intent.OperationID)
	run.intent.Arguments = originalArgs
	oldSHA := run.intent.ConfigSHA256
	run.intent.ConfigSHA256 = ownershipDigest([]byte("wrong config"))
	check("conflicting intent", ownershipContext(t), f.store, f.intent.OperationID)
	run.intent.ConfigSHA256 = oldSHA
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	check("canceled", ctx, f.store, f.intent.OperationID)
	// An active callback is also interrupted by its caller, without another dial.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	callbackElapsed := time.Duration(0)
	reached := false
	err = run.WithRegisteredController(ctx, f.store, f.intent.OperationID, func(_ context.Context, c *net.TCPConn, _ config.RunIntent, _ config.RunRegistration, _ string) error {
		reached = true
		timer := time.AfterFunc(40*time.Millisecond, cancel)
		defer timer.Stop()
		began := time.Now()
		_, e := io.ReadFull(c, make([]byte, 1))
		callbackElapsed = time.Since(began)
		return e
	})
	if !reached || err == nil || callbackElapsed > time.Second {
		t.Fatal("callback cancellation was not bounded", err)
	}

	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("negative callbacks wrote store")
	}
	f.store.Close()
	reader, e := config.OpenStore(f.paths, false)
	if e != nil {
		t.Fatal(e)
	}
	f.store = reader
	check("readonly", ownershipContext(t), reader, f.intent.OperationID)
	reader.Close()
	writer, e := config.OpenStore(f.paths, true)
	if e != nil {
		t.Fatal(e)
	}
	f.store = writer
	// Replace only a private test record with internally inconsistent bytes.
	path := filepath.Join(f.runPath, "registration-"+ownershipDigest([]byte(f.intent.OperationID))+".json")
	reg, _, e := writer.ReadRunRegistration(f.intent.RunID, f.intent.OperationID)
	if e != nil {
		t.Fatal(e)
	}
	originalReg := reg
	reg.StartTicks++
	raw, _ := json.Marshal(reg)
	if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	before = ownershipTree(t, f.paths.State)
	check("conflicting record", ownershipContext(t), writer, f.intent.OperationID)
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("conflict repaired")
	}
	raw, _ = json.Marshal(originalReg)
	if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	// Losing the original handle after a trusted callback is still an error;
	// its remote action must not be inferred to have been absent.
	sentinel := errors.New("trusted callback returned after action")
	visited := false
	err = run.WithRegisteredController(ownershipContext(t), writer, f.intent.OperationID, func(context.Context, *net.TCPConn, config.RunIntent, config.RunRegistration, string) error {
		visited = true
		run.Close()
		return sentinel
	})
	if !visited || !errors.Is(err, ErrOwnershipUnknown) || !errors.Is(err, sentinel) {
		t.Fatal("post-callback original handle verification missing", err)
	}
	check("closed original handle", ownershipContext(t), writer, f.intent.OperationID)
	f.assertRunning(t)
}
