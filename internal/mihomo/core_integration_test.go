//go:build linux && amd64

package mihomo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

const inspectorPinnedCoreSHA256 = "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb"

func TestMihomoInspectorPinnedCore(t *testing.T) {
	path := os.Getenv("HOOPA_P1_BASIC_CORE")
	if path == "" {
		t.Skip("explicit fixed HOOPA_P1_BASIC_CORE required; no real core launched")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Fatal("explicit core path must be canonical absolute")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read fixed core:", err)
	}
	if sha256String(binary) != inspectorPinnedCoreSHA256 {
		t.Fatal("explicit core is not the fixed build-02-state-history binary")
	}
	// Endpoints and manual validation projection are constructed BEFORE the child
	// starts, never from controller responses. No birth/registration is forged.
	var holders []*net.TCPListener
	var endpoints []string
	for i := 0; i < 3; i++ {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		holders = append(holders, listener)
		endpoints = append(endpoints, listener.Addr().String())
	}
	t.Cleanup(func() {
		for _, listener := range holders {
			listener.Close()
		}
	})
	work := filepath.Join(t.TempDir(), "core")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(work, "config.json")
	b := inspectorBlueprintFor(t, endpoints[2], config.BinaryReference{Path: path, Version: "1.10.0", SHA256: inspectorPinnedCoreSHA256}, endpoints[:2])
	b.intent.Workdir = work
	b.intent.Arguments = []string{path, "-d", work, "-f", configPath}
	if err := os.WriteFile(configPath, b.raw, 0600); err != nil {
		t.Fatal(err)
	}
	f := inspectorPublish(t, b)
	f.store.Close()
	reader, err := config.OpenStore(f.paths, false)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reader
	f.controller, err = NewController(reader, b.record)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.ReadRunBirth(b.intent.RunID); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("core test unexpectedly has a birth record:", err)
	}
	before := inspectorTree(t, f.paths.State)
	for _, listener := range holders {
		listener.Close()
	}
	cmd := exec.Command(path, b.intent.Arguments[1:]...)
	cmd.Dir = work
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "HOOPA_") && !strings.HasPrefix(item, "CLASH_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "HOOPA_CONTROL_PROTOCOL=1", "HOOPA_RUN_ID="+string(b.intent.RunID), "HOOPA_RUN_NONCE="+strings.Repeat("ab", 32), "HOOPA_GUARD_GENERATION="+string(b.intent.Generation), "HOOPA_GUARD_CONFIG_SHA256="+b.intent.ConfigSHA256)
	logPath := filepath.Join(work, "core.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal("start fixed owned core:", err)
	}
	logFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waited := false
	t.Cleanup(func() {
		if !waited {
			// This is our exact exec.Cmd child, not adoption by a serialized numeric PID.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error("owned core TERM:", err)
			}
			select {
			case err := <-exited:
				waited = true
				if err != nil {
					t.Error("owned core Wait:", err)
				}
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
				<-exited
				waited = true
				t.Error("owned core needed forced kill after TERM timeout")
			}
		}
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Error(err)
		} else if strings.Contains(string(data), b.secret) || strings.Contains(string(data), strings.Repeat("ab", 32)) {
			t.Error("core log contains private fixture material")
		} else {
			t.Logf("owned fixed-core original log:\n%s", data)
		}
		if cmd.ProcessState != nil {
			t.Logf("owned fixed core pid=%d Wait exit_code=%d normal_TERM_cleanup=true", cmd.Process.Pid, cmd.ProcessState.ExitCode())
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	handle, err := platform.ObserveProcess(ctx, cmd.Process.Pid, platform.ProcessSpec{BinaryPath: path, BinarySHA256: inspectorPinnedCoreSHA256, Workdir: work, NonceSHA256: b.intent.NonceSHA256})
	if err != nil {
		t.Fatal("strong owned process identity:", err)
	}
	defer handle.Close()
	if err := handle.VerifyArguments(ctx, b.intent.Arguments); err != nil {
		t.Fatal("actual core argv:", err)
	}
	// Readonly proc LISTEN polling is just startup readiness, not an owner claim.
	// Exactly one connection is later dialed and independently owner-proved.
	_, portText, _ := net.SplitHostPort(endpoints[2])
	port, _ := strconv.Atoi(portText)
	wanted := fmt.Sprintf("0100007F:%04X", port)
	ready := false
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !ready {
		raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", cmd.Process.Pid))
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[1] == wanted && fields[3] == "0A" {
				ready = true
				break
			}
		}
		if ready {
			break
		}
		select {
		case err := <-exited:
			waited = true
			t.Fatalf("fixed core exited before readiness: %v", err)
		case <-timer.C:
			t.Fatal("fixed controller not LISTEN within startup bound")
		case <-tick.C:
		}
	}
	conn, err := handle.DialOwnedTCP(ctx, b.intent.Controller)
	if err != nil {
		t.Fatal("same connected TCP owner proof:", err)
	}
	defer conn.Close()
	observation, err := f.controller.Inspect(ctx, conn, f.blueprint.intent)
	if err != nil {
		t.Fatal("fixed core Inspector integration:", err)
	}
	if observation.Gate != "closed" || observation.RunID != b.intent.RunID || observation.Generation != b.intent.Generation || observation.ConfigSHA256 != b.intent.ConfigSHA256 || observation.BinaryVersion != b.intent.Binary.Version {
		t.Fatal("fixed core observation differs from independent expectation")
	}
	for _, address := range endpoints[:2] {
		c, err := net.DialTimeout("tcp4", address, 150*time.Millisecond)
		if c != nil {
			c.Close()
			t.Fatal("closed core app endpoint listened")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			t.Fatal("closed endpoint did not refuse connect:", err)
		}
	}
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("fixed-core inspection mutated readonly store")
	}
	t.Logf("fixed build SHA=%s; independent expected snapshot SHA=%s; owner-proved controller connections=1; observed gate=closed; no birth/registration/activation", inspectorPinnedCoreSHA256, func() string { hash, _ := b.snapshot.Digest(); return hash }())
}
