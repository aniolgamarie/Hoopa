//go:build linux && amd64

package mihomo

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

func TestBasicCoreLauncherDispatch(t *testing.T) {
	if os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" {
		return
	} // Empty parent dispatcher.
	if err := ExecBasicLauncher(context.Background(), os.NewFile(3, "birth"), os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE")); err != nil {
		t.Fatal("dedicated basic exec failed", err)
	}
	t.Fatal("successful core exec returned")
}
func TestBasicCoreStartMaterialRejections(t *testing.T) {
	for _, name := range []string{"argv", "capability", "canceled", "nil-controller", "nil-prepared"} {
		t.Run(name, func(t *testing.T) {
			b := inspectorBlueprintFor(t, "127.0.0.1:23456", config.BinaryReference{}, nil)
			work := filepath.Join(t.TempDir(), "work")
			if err := os.Mkdir(work, 0700); err != nil {
				t.Fatal(err)
			}
			b.intent.Workdir = work
			b.intent.Arguments = []string{b.intent.Binary.Path, "-d", work, "-f", filepath.Join(work, "config.json")}
			if name == "argv" {
				b.intent.Arguments = append(b.intent.Arguments, "--extra")
			}
			if name == "capability" {
				b.capabilityDigest = sha256String([]byte("wrong capability"))
			}
			if err := os.WriteFile(filepath.Join(work, "config.json"), b.raw, 0600); err != nil {
				t.Fatal(err)
			}
			f := inspectorPublish(t, b)
			input := f.blueprint.intent
			input.RunID = ""
			input.NonceSHA256 = ""
			prepared, err := owned.PrepareLaunch(f.store, input)
			if err != nil {
				t.Fatal(err)
			}
			before := inspectorTree(t, f.paths.State)
			ctx := context.Background()
			if name == "canceled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			controller := f.controller
			request := prepared
			if name == "nil-controller" {
				controller = nil
			}
			if name == "nil-prepared" {
				request = nil
			}
			child, err := controller.Start(ctx, request, []string{"-test.run=^TestBasicCoreLauncherDispatch$"})
			if child != nil || !errors.Is(err, ErrController) {
				t.Fatal("material preflight launched child or wrong error", err)
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("rejected Start changed records")
			}
			if _, e := os.Stat(filepath.Join(work, "launcher.log")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("rejected Start created log", e)
			}
			fd, _, _, err := prepared.TakeLauncherInput()
			if err != nil {
				t.Fatal("preflight consumed launch capability", err)
			}
			fd.Close()
		})
	}
}

func basicCoreContext(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}
func basicCoreRejectEntry(t *testing.T, address string) {
	t.Helper()
	c, err := net.DialTimeout("tcp4", address, 200*time.Millisecond)
	if c != nil {
		c.Close()
	}
	if err == nil {
		t.Fatal("closed/drained entry accepted TCP")
	}
}
func basicCoreEcho(t *testing.T, c net.Conn, r *bufio.Reader, message string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, message); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, len(message))
	if _, err := io.ReadFull(r, raw); err != nil || string(raw) != message {
		t.Fatal("real local echo mismatch", err)
	}
}

func TestBasicCoreLauncherRealCombination(t *testing.T) {
	original := os.Getenv("HOOPA_P1_BASIC_CORE")
	if original == "" {
		t.Skip("explicit fixed core input required")
	}
	raw, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if sha256String(raw) != inspectorPinnedCoreSHA256 {
		t.Fatal("unexpected fixed core hash")
	}
	// The source /data path is only an input byte reference. Execute a new,
	// single-link copy beneath our own actual 0700 /tmp descendant.
	private := filepath.Join(t.TempDir(), "private")
	if err = os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(private, "mihomo")
	if err = os.WriteFile(binary, raw, 0755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(private, "work")
	if err = os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	var endpoints []string
	var reservations []*net.TCPListener
	for i := 0; i < 3; i++ {
		l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		reservations = append(reservations, l)
		endpoints = append(endpoints, l.Addr().String())
	}
	for _, l := range reservations {
		l.Close()
	}
	b := inspectorBlueprintFor(t, endpoints[2], config.BinaryReference{Path: binary, Version: "1.10.0", SHA256: inspectorPinnedCoreSHA256}, endpoints[:2])
	b.intent.Workdir = work
	b.intent.Arguments = []string{binary, "-d", work, "-f", filepath.Join(work, "config.json")}
	// This independent expected snapshot exists before PrepareLaunch or core start.
	expectedSnapshotSHA, err := b.snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(work, "config.json"), b.raw, 0600); err != nil {
		t.Fatal(err)
	}
	f := inspectorPublish(t, b)
	input := f.blueprint.intent
	input.RunID = ""
	input.NonceSHA256 = ""
	prepared, err := owned.PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	intent := prepared.Intent()
	if got, _, e := f.store.ReadRunIntent(intent.RunID); e != nil || !reflect.DeepEqual(got, intent) {
		t.Fatal("launch intent not durable", e)
	}
	entry := []string{"-test.run=^TestBasicCoreLauncherDispatch$", "-test.timeout=15s"}
	child, err := f.controller.Start(basicCoreContext(t), prepared, entry)
	if err != nil {
		t.Fatal(err)
	}
	var cleanupHandle *platform.ProcessHandle
	// Cleanup signals only a freshly verified retained original pidfd. If a
	// failed launcher never execed, separately prove the exact manager test image.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if cleanupHandle == nil {
			birth, _, e := f.store.ReadRunBirth(intent.RunID)
			if e == nil {
				self, _ := os.Executable()
				selfBytes, _ := os.ReadFile(self)
				candidates := []struct {
					path, sha string
					args      []string
				}{{binary, inspectorPinnedCoreSHA256, intent.Arguments}, {self, sha256String(selfBytes), append([]string{self}, entry...)}}
				for _, candidate := range candidates {
					h, e := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: candidate.path, BinarySHA256: candidate.sha, Workdir: work, NonceSHA256: intent.NonceSHA256})
					if e == nil {
						identity := h.Identity()
						if identity.PID == birth.PID && identity.StartTicks == birth.StartTicks && identity.BootID == birth.BootID && identity.UID == birth.UID && h.VerifyArguments(ctx, candidate.args) == nil {
							cleanupHandle = h
							break
						}
						h.Close()
					}
				}
			}
		}
		if cleanupHandle != nil {
			if e := cleanupHandle.SignalVerified(ctx, syscall.SIGTERM); e != nil {
				t.Error("verified original-pidfd TERM", e)
			}
			cleanupHandle.Close()
		} else {
			t.Error("no verified original pidfd for cleanup; no numeric PID signal sent")
		}
		if e := child.Wait(ctx); e != nil {
			t.Error("actual core child Wait", e)
		} else {
			t.Logf("actual fixed core PID=%d verified original-pidfd TERM and Wait exit=0", child.PID())
		}
		if log, e := os.ReadFile(filepath.Join(work, "launcher.log")); e == nil {
			if strings.Contains(string(log), b.secret) {
				t.Error("core log contained fixture credential; suppressed")
			} else {
				t.Logf("original basic launcher/core log:\n%s", log)
			}
		}
	})
	// Read-only proc polling is only startup synchronization, never ownership.
	deadline := time.Now().Add(5 * time.Second)
	for {
		birth, _, e := f.store.ReadRunBirth(intent.RunID)
		if e == nil {
			h, e := platform.ObserveProcess(basicCoreContext(t), birth.PID, platform.ProcessSpec{BinaryPath: binary, BinarySHA256: inspectorPinnedCoreSHA256, Workdir: work, NonceSHA256: intent.NonceSHA256})
			if e == nil {
				identity := h.Identity()
				if identity.PID == child.PID() && identity.StartTicks == birth.StartTicks && identity.BootID == birth.BootID && identity.UID == birth.UID && h.VerifyArguments(basicCoreContext(t), intent.Arguments) == nil {
					cleanupHandle = h
					break
				}
				h.Close()
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("core exec strong identity timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Original birth-directory FD must not survive into the actual core, even if
	// its numeric slot has been reused. Compare the inode/link target, not FD3.
	runDir := filepath.Join(f.paths.State, "runs", sha256String([]byte(intent.RunID)))
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", child.PID()))
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		link, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", child.PID(), fd.Name()))
		if link == runDir {
			t.Fatal("actual core inherited delegated birth directory")
		}
	}
	owner, err := owned.NewOwnership(f.store, f.controller)
	if err != nil {
		t.Fatal(err)
	}
	// Controller may finish Listen slightly after exec identity becomes stable.
	// Wait using proc-only hints, without an early unauthenticated TCP connection.
	_, port, _ := net.SplitHostPort(intent.Controller)
	var portNumber int
	fmt.Sscan(port, &portNumber)
	needle := fmt.Sprintf("0100007F:%04X", portNumber)
	deadline = time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/net/tcp", child.PID()))
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 3 && fields[1] == needle && fields[3] == "0A" {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("controller Listen startup timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, registrationSHA, err := owner.Register(basicCoreContext(t), intent.RunID, intent.OperationID, true)
	if err != nil {
		t.Fatal("actual closed registration", err)
	}
	defer run.Close()
	originalIdentity := run.Identity()
	if originalIdentity.PID != child.PID() || run.Observation().Gate != "closed" {
		t.Fatal("registered wrong process/gate")
	}
	for _, address := range endpoints[:2] {
		basicCoreRejectEntry(t, address)
	}
	before := inspectorTree(t, f.paths.State)
	outcome, err := f.controller.Activate(basicCoreContext(t), run, intent.OperationID)
	if err != nil || outcome.Receipt == nil || outcome.Receipt.Result != "opened" || outcome.Current == nil || outcome.Current.State != "open" || outcome.Current.SnapshotSHA256 != expectedSnapshotSHA {
		t.Fatal("actual activation binding", err)
	}
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("activation wrote records")
	}
	echo, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	echoDone := make(chan error, 1)
	acceptedEcho := make(chan *net.TCPConn, 1)
	echoCollected := false
	t.Cleanup(func() {
		echo.Close()
		select {
		case c := <-acceptedEcho:
			c.Close()
		default:
		}
		if !echoCollected {
			select {
			case <-echoDone:
			case <-time.After(2 * time.Second):
				t.Error("echo fixture not collected on failure")
			}
		}
	})
	go func() {
		c, e := echo.AcceptTCP()
		if e != nil {
			echoDone <- e
			return
		}
		acceptedEcho <- c
		defer c.Close()
		c.SetDeadline(time.Now().Add(10 * time.Second))
		_, e = io.Copy(c, c)
		echoDone <- e
	}()
	tunnel, err := net.DialTimeout("tcp4", endpoints[0], time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	tunnel.SetDeadline(time.Now().Add(2 * time.Second))
	auth := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("u", 32) + ":" + strings.Repeat("p", 32)))
	fmt.Fprintf(tunnel, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", echo.Addr(), echo.Addr(), auth)
	reader := bufio.NewReader(tunnel)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("actual authenticated echo tunnel", err)
	}
	basicCoreEcho(t, tunnel, reader, "echo-before-drain")
	run.Close()
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = cleanupHandle.VerifyArguments(basicCoreContext(t), intent.Arguments); err != nil {
		t.Fatal("closing handle/store killed core", err)
	}
	reopened, err := config.OpenStore(f.paths, true)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reopened
	controller, err := NewController(reopened, b.record)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = owned.NewOwnership(reopened, controller)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _, err := owner.Register(basicCoreContext(t), intent.RunID, "operation:drain", false)
	if err != nil {
		t.Fatal("same-instance reopen registration", err)
	}
	defer recovered.Close()
	if recovered.Identity() != originalIdentity || recovered.Observation().Gate != "open" {
		t.Fatal("reopen spawned or rebound identity")
	}
	old, oldSHA, err := reopened.ReadRunRegistration(intent.RunID, intent.OperationID)
	if err != nil || oldSHA != registrationSHA || old.Gate != "closed" {
		t.Fatal("historical registration overwritten", err)
	}
	before = inspectorTree(t, f.paths.State)
	outcome, err = controller.Drain(basicCoreContext(t), recovered, "operation:drain")
	if err != nil || outcome.Receipt == nil || outcome.Receipt.Result != "draining" || outcome.Current == nil || outcome.Current.State != "draining" {
		t.Fatal("actual drain", err)
	}
	basicCoreEcho(t, tunnel, reader, "echo-after-drain")
	for _, address := range endpoints[:2] {
		basicCoreRejectEntry(t, address)
	}
	outcome, err = controller.Activate(basicCoreContext(t), recovered, intent.OperationID)
	if err != nil || outcome.Receipt == nil || outcome.Receipt.Result != "opened" || outcome.Current == nil || outcome.Current.State != "draining" {
		t.Fatal("actual historical replay reopened/confused gate", err)
	}
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("drain/replay wrote records")
	}
	tunnel.Close()
	select {
	case e := <-echoDone:
		echoCollected = true
		if e != nil {
			t.Fatal("echo cleanup", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("echo server not collected")
	}
	t.Logf("actual core SHA=%s independent snapshot SHA=%s PID=%d; PrepareLaunch/Start/same-PID exec/closed registration/activate/real local echo/reopen same birth/drain/old echo/new refusal/historical replay; no VM/BPF or manager-death claim", inspectorPinnedCoreSHA256, expectedSnapshotSHA, child.PID())
}
