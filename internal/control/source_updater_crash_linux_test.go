//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	"hoopa/internal/subscription"
)

// This child is a real writer process with a real HTTPS request. It does not
// launch a proxy core. The parent only sends SIGKILL after the server observes
// that request, which implies the Applying record preceded network acquisition.
func TestSourceUpdaterCrashChild(t *testing.T) {
	fixture := os.Getenv("HOOPA_TEST_SOURCE_CRASH_FIXTURE")
	if fixture == "" {
		t.Skip("private subprocess helper")
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Paths   config.Paths
		Request []byte
		CA      []byte
	}
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("invalid child fixture")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(f.CA) {
		t.Fatal("invalid test CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	c, err := OpenCoordinator(f.Paths, func(json.RawMessage, []PlanInput) error { return ErrCapabilityUnsupported }, func(context.Context, Plan) (CurrentPlanInputs, error) {
		return CurrentPlanInputs{}, ErrCapabilityUnsupported
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	u, err := NewSourceUpdater(c, context.Background(), subscription.FetchOptions{Transport: transport, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, made, err := u.Update(context.Background(), f.Request); err != nil || !made {
		t.Fatal("child admission", made, err)
	}
	<-time.After(45 * time.Second)
	t.Fatal("parent did not terminate child at held request")
}

func TestSourceUpdaterRealCrashReplayAndExplicitRecovery(t *testing.T) {
	g, source := updaterHTTPS(t)
	c, p := coordinatorFixture(t, nil)
	initializePut(t, c, initializeSettings(t))
	base, _ := c.store.ReadActive()
	raw := updaterRequest(t, c, "real-crash", "source-a", source, nil)
	fixture := filepath.Join(t.TempDir(), "child.json")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: g.server.Certificate().Raw})
	data, err := json.Marshal(struct {
		Paths   config.Paths
		Request []byte
		CA      []byte
	}{p, raw, ca})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSourceUpdaterCrashChild$", "-test.timeout=50s")
	cmd.Env = append(os.Environ(), "HOOPA_TEST_SOURCE_CRASH_FIXTURE="+fixture)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	updaterEntered(t, g)
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	joined = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal("child unexpectedly exited normally", err, log.String())
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("child did not die by actual SIGKILL", log.String())
	}
	next, err := OpenCoordinator(p, coordinatorValidator(t), func(context.Context, Plan) (CurrentPlanInputs, error) { return coordinatorInputs(), nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.Close() })
	u, err := NewSourceUpdater(next, context.Background(), subscription.FetchOptions{Transport: g.server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(u.Close)
	before := coordinatorTree(t, p)
	op, made, err := u.Update(context.Background(), raw)
	if err != nil || made || op.Record.Phase != Applying {
		t.Fatal("restart refetched or changed interrupted reservation", made, err, op.Record.Phase)
	}
	if !reflectTreesEqual(before, coordinatorTree(t, p)) {
		t.Fatal("restart replay wrote")
	}
	if _, made, err := u.Update(context.Background(), updaterRequest(t, next, "new-before-recovery", "source-b", source, nil)); made || err != ErrRecoveryRequired {
		t.Fatal("interrupted reservation ignored", err)
	}
	requestRaw, _ := json.Marshal(map[string]any{"api_version": "v1", "request_id": "recover-real-source-crash", "operation": "operation.recover", "params": map[string]any{"operation_id": op.Record.Identity.OperationID, "expected_revision": base.Revision}})
	recoverRequest, err := ParseRequest(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	verify := func(context.Context, config.RunID, config.ID) (platform.ProcessIdentity, error) {
		t.Error("source recovery tried to verify a core")
		return platform.ProcessIdentity{}, ErrCapabilityUnsupported
	}
	next.mu.Lock()
	call, made, err := next.recoverBasicLocked(context.Background(), recoverRequest, verify, nil)
	next.mu.Unlock()
	if err != nil || !made || call.Record.Phase != Committed {
		t.Fatal("explicit original workflow recovery", made, err, call.Record.Phase)
	}
	entry := updaterWait(t, u, raw)
	if entry.Operation.Record.Phase != RolledBack || entry.Receipt.Reason != "interrupted" || entry.Receipt.Downloaded || entry.Context.Sequence != 1 || entry.Context.NextDue == nil {
		t.Fatal("recovery lost attempt or fabricated success")
	}
	if g.calls.Load() != 1 {
		t.Fatal("recovery retried network acquisition")
	}
	after, _ := next.store.ReadActive()
	if after.Revision != base.Revision || after.Generation != nil {
		t.Fatal("source recovery changed runtime authority")
	}
	// A new explicit attempt can use sequence 2; recovery itself consumed none.
	g.once.Do(func() { close(g.release) })
	newRaw := updaterRequest(t, next, "after-recovery", "source-a", source, nil)
	updaterBegin(t, u, newRaw)
	entry = updaterWait(t, u, newRaw)
	if entry.Operation.Record.Phase != Committed || entry.Context.Sequence != 2 || g.calls.Load() != 2 {
		t.Fatal("post-recovery explicit retry", entry.Context.Sequence)
	}
}

func reflectTreesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
