//go:build linux && amd64

package mihomo

import (
	"context"
	"errors"
	"fmt"
	"net"
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

// Actual fixed-core integration only. The fixture capability and intent are
// explicit test declarations, not production approval or a generic compiler.
func TestTerminationCoreFailedActivation(t *testing.T) {
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
	terminated := false
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
			if !terminated {
				if e := cleanupHandle.SignalVerified(ctx, syscall.SIGTERM); e != nil {
					t.Error("verified original-pidfd TERM", e)
				}
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

	// The core's reservations use SO_REUSEADDR without listening. A task-owned
	// socket with the same option can bind and listen first AFTER closed proof.
	// This is deterministic resource interference, not a timing-window probe.
	competitor := terminationCoreCompetitor(t, endpoints[1])
	defer competitor.Close()
	outcome, err := f.controller.Activate(basicCoreContext(t), run, intent.OperationID)
	// Mutation post-inspection independently reads the degraded state and returns
	// its authenticated failed receipt; ordinary ownership Inspect stays narrower.
	if !errors.Is(err, ErrTransitionFailed) || outcome.Receipt == nil || outcome.Receipt.Result != "failed" || outcome.Current == nil || outcome.Current.State != "degraded" {
		t.Fatal("failed activation did not retain failed receipt and verified degraded current", err)
	}
	if outcome.Receipt.OperationID != string(intent.OperationID) || outcome.Receipt.RunID != string(intent.RunID) {
		t.Fatal("failed receipt identity mismatch")
	}
	reg, regSHA, err := f.store.ReadControlRegistration(intent.RunID, intent.OperationID)
	if err != nil || regSHA != registrationSHA {
		t.Fatal("original closed registration changed", err)
	}
	conn, err := cleanupHandle.DialOwnedTCP(basicCoreContext(t), intent.Controller)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := f.controller.InspectTermination(basicCoreContext(t), conn, intent, reg, regSHA)
	conn.Close()
	if err != nil || observation.Gate != "degraded" {
		t.Fatal("actual degraded termination inspection", err)
	}
	if observed, e := owner.Observe(basicCoreContext(t), intent.RunID); e == nil || observed != nil {
		if observed != nil {
			observed.Close()
		}
		t.Fatal("ordinary Observe adopted degraded core")
	}
	if e := owner.Terminate(basicCoreContext(t), run, intent.OperationID); !errors.Is(e, owned.ErrOwnershipUnknown) {
		t.Fatal("ordinary Terminate accepted degraded core", e)
	}
	if e := cleanupHandle.VerifyArguments(basicCoreContext(t), intent.Arguments); e != nil {
		t.Fatal("rejected ordinary path signalled core", e)
	}
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("GET/mutation inspections wrote Store")
	}
	basicCoreRejectEntry(t, endpoints[0])
	terminationCoreCompetitorAlive(t, competitor)
	// This fixture is the caller holding its isolated writer and a test-only
	// stop-candidate decision; it does not claim a product journal workflow.
	if e := owner.TerminateCandidate(basicCoreContext(t), intent.RunID, intent.OperationID); e != nil {
		t.Fatal("actual candidate TERM", e)
	}
	terminated = true
	if e := cleanupHandle.WaitExited(basicCoreContext(t)); e != nil {
		t.Fatal("independent retained pidfd exit", e)
	}
	if e := child.Wait(basicCoreContext(t)); e != nil {
		t.Fatal("independent launcher child Wait exit", e)
	}
	terminationCoreCompetitorAlive(t, competitor)
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("candidate termination wrote Store")
	}
	t.Logf("actual fixed core identity=%+v snapshot=%s activation=failed termination-only gate=%s; candidate TERM + independent retained pidfd WaitExited + child Wait exit=0; competitor alive before/after", originalIdentity, expectedSnapshotSHA, observation.Gate)
}

func terminationCoreCompetitor(t *testing.T, address string) *net.TCPListener {
	t.Helper()
	a, e := net.ResolveTCPAddr("tcp4", address)
	if e != nil {
		t.Fatal(e)
	}
	fd, e := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, syscall.IPPROTO_TCP)
	if e != nil {
		t.Fatal(e)
	}
	f := os.NewFile(uintptr(fd), "task-only-competitor")
	defer f.Close()
	if e = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
		t.Fatal(e)
	}
	sa := &syscall.SockaddrInet4{Port: a.Port, Addr: [4]byte{127, 0, 0, 1}}
	if e = syscall.Bind(fd, sa); e != nil {
		t.Fatal("competitor bind after closed registration", e)
	}
	if e = syscall.Listen(fd, 1); e != nil {
		t.Fatal(e)
	}
	l, e := net.FileListener(f)
	if e != nil {
		t.Fatal(e)
	}
	return l.(*net.TCPListener)
}
func terminationCoreCompetitorAlive(t *testing.T, l *net.TCPListener) {
	t.Helper()
	l.SetDeadline(time.Now().Add(time.Second))
	c, e := net.DialTimeout("tcp4", l.Addr().String(), time.Second)
	if e != nil {
		t.Fatal("task competitor dial", e)
	}
	defer c.Close()
	a, e := l.AcceptTCP()
	if e != nil {
		t.Fatal("task competitor accept", e)
	}
	defer a.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	a.SetDeadline(time.Now().Add(time.Second))
	if _, e = a.Write([]byte{0x71}); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1)
	if n, e := c.Read(b); e != nil || n != 1 || b[0] != 0x71 {
		t.Fatal("task competitor did not retain its own socket", e)
	}
}
