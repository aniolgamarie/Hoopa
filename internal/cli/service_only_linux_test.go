//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/service"
)

// This executes the actual Cobra binary and detached service child. There is
// no core fixture, compiler, managed start, signal-to-core or OS-exit claim.
func TestCLIBootstrapServiceOnlyActualBinary(t *testing.T) {
	paths := cliPaths(t)
	binary := filepath.Join(t.TempDir(), "hoopa")
	buildArgs := []string{"build", "-buildvcs=false"}
	if cliChildRace {
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs, "-o", binary, "./cmd/hoopa")
	build := exec.Command("/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go", buildArgs...)
	t.Logf("actual CLI/service child race instrumentation: %v", cliChildRace)
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, out)
	}
	invoke := func(input string, args ...string) ([]byte, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Stdin = strings.NewReader(input)
		var output, diagnostics bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &diagnostics
		err := cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal("CLI execution", err)
			}
			code = exit.ExitCode()
		}
		if diagnostics.Len() != 0 {
			t.Fatal("CLI emitted request diagnostics", diagnostics.String())
		}
		decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
		var response map[string]json.RawMessage
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("invalid CLI response %v: %s", err, output.Bytes())
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			t.Fatal("CLI emitted multiple responses", err)
		}
		t.Logf("argv=%q exit=%d stdout=%s", args, code, bytes.TrimSpace(output.Bytes()))
		return append([]byte(nil), output.Bytes()...), code
	}
	t.Cleanup(func() {
		status, err := service.LocalStatus(context.Background(), paths)
		if err != nil {
			return
		}
		raw, _ := makeRequest("e2e-service-cleanup", "service.stop", map[string]any{"expected_revision": status.Revision})
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "api", "v1", "service.stop")
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.CombinedOutput()
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 4 {
				t.Errorf("service cleanup: %v %s", err, out)
			}
		}
		if err := waitServiceWriter(paths, 3*time.Second); err != nil {
			t.Errorf("writer still held after cleanup: %v", err)
		}
	})
	if out, code := invoke("", "start", "--control-only", "--request-id", "cold-control-only"); code != 0 {
		t.Fatalf("cold bootstrap failed %d: %s", code, out)
	}
	first := cliDiscoveryInstance(t, paths)
	if _, code := invoke("", "status"); code != 0 {
		t.Fatal("live status failed", code)
	}
	status, err := service.LocalStatus(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Generation != nil || status.DesiredRuntime != "stopped" {
		t.Fatal("control-only bootstrap fabricated core state")
	}
	revision := status.Revision
	stopped, code := invoke("", "service", "stop", "--request-id", "stop-first-service", "--expected-revision", string(revision))
	if code != 0 {
		t.Fatal("service stop", code)
	}
	if err := waitServiceWriter(paths, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	before := cliTree(t, paths)
	raw, _ := makeRequest("stop-first-service", "service.stop", map[string]any{"expected_revision": revision})
	replay, code := invoke(string(raw), "api", "v1", "service.stop")
	if code != 0 || !bytes.Equal(replay, stopped) || !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("offline exact replay changed receipt or files", code)
	}
	if out, code := invoke("", "start", "--control-only", "--request-id", "second-control-only"); code != 0 {
		t.Fatalf("second explicit bootstrap failed %d: %s", code, out)
	}
	second := cliDiscoveryInstance(t, paths)
	if first == second {
		t.Fatal("new service reused old incarnation")
	}
	replay, code = invoke(string(raw), "api", "v1", "service.stop")
	if code != 0 || !bytes.Equal(replay, stopped) || cliDiscoveryInstance(t, paths) != second {
		t.Fatal("old stop affected new service", code)
	}
	if _, code := invoke("", "status"); code != 0 {
		t.Fatal("new service stopped on historical replay", code)
	}
	if _, code := invoke("", "service", "stop", "--request-id", "stop-second-service", "--expected-revision", string(revision)); code != 0 {
		t.Fatal("second service stop", code)
	}
	if err := waitServiceWriter(paths, 3*time.Second); err != nil {
		t.Fatal(err)
	}

	// Two explicit frontends may race to create management. The writer flock
	// selects one service; the loser may only recheck that existing endpoint.
	var commands [2]*exec.Cmd
	var outputs, diagnostics [2]bytes.Buffer
	for i := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		id := []string{"simultaneous-control-a", "simultaneous-control-b"}[i]
		commands[i] = exec.CommandContext(ctx, binary, "start", "--control-only", "--request-id", id)
		commands[i].Stdout = &outputs[i]
		commands[i].Stderr = &diagnostics[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("simultaneous frontend %d: %v stdout=%s stderr=%s", i, err, outputs[i].Bytes(), diagnostics[i].Bytes())
		}
		if diagnostics[i].Len() != 0 {
			t.Fatal("simultaneous frontend diagnostics", diagnostics[i].String())
		}
		t.Logf("simultaneous frontend=%d stdout=%s", i, bytes.TrimSpace(outputs[i].Bytes()))
	}
	third := cliDiscoveryInstance(t, paths)
	if third == first || third == second {
		t.Fatal("simultaneous new writer reused incarnation")
	}
	for i := range commands {
		var response struct {
			OK   bool `json:"ok"`
			Data struct {
				Instance string `json:"instance"`
			} `json:"data"`
		}
		decoder := json.NewDecoder(bytes.NewReader(outputs[i].Bytes()))
		if decoder.Decode(&response) != nil || !response.OK || response.Data.Instance != third {
			t.Fatal("frontends did not converge on one service")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			t.Fatal("simultaneous stdout not one JSON", err)
		}
	}
	if _, code := invoke("", "service", "stop", "--request-id", "stop-simultaneous-service", "--expected-revision", string(revision)); code != 0 {
		t.Fatal("simultaneous service stop", code)
	}
	if err := waitServiceWriter(paths, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	after, err := service.LocalStatus(context.Background(), paths)
	if err != nil || after.Revision != revision || after.Generation != nil || after.DesiredRuntime != "stopped" {
		t.Fatal("service-only scenario changed core authority", err)
	}
	t.Logf("service-only actual binary: cold=%s second=%s; two sequential and two simultaneous explicit starts; old stop replayed offline and online; no core launched", first, second)
}
func cliDiscoveryInstance(t *testing.T, paths config.Paths) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(paths.Runtime, "service.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Instance string `json:"instance"`
	}
	if json.Unmarshal(raw, &d) != nil || !lowerHex(d.Instance, 24) {
		t.Fatal("invalid discovery instance")
	}
	return d.Instance
}

// Observe release of the existing test-owned writer flock, without opening a
// writable Store, creating a lock, syncing authority or inferring anything from
// a PID/EOF. This allows the next explicit bootstrap to begin after shutdown.
func waitServiceWriter(paths config.Paths, limit time.Duration) error {
	file, err := os.Open(filepath.Join(paths.State, ".writer.lock"))
	if err != nil {
		return err
	}
	defer file.Close()
	deadline := time.Now().Add(limit)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}
