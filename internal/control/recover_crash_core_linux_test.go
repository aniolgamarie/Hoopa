//go:build linux && amd64

package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
)

type crashCoreBootstrap struct {
	Version  int
	Paths    config.Paths
	Plan     json.RawMessage
	Material BasicMaterial
	Current  CurrentPlanInputs
	ApplyRaw []byte
}
type crashCoreReady struct {
	Version     int
	ManagerPID  int
	OperationID config.ID
	RunID       config.RunID
	Step        string
}

// Normal parent invocation is empty and counted separately. This dedicated
// child is a REAL manager OS process, not a synthetic runtime implementation.
func TestRecoveryCrashManagerDispatch(t *testing.T) {
	if os.Getenv("HOOPA_CRASH_MANAGER") != "1" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("HOOPA_CRASH_BOOTSTRAP"))
	if e != nil || len(raw) > 128<<10 || hash(raw) != os.Getenv("HOOPA_CRASH_BOOTSTRAP_SHA256") {
		t.Fatal("private bootstrap binding rejected")
	}
	var b crashCoreBootstrap
	if e := json.Unmarshal(raw, &b); e != nil || b.Version != 1 {
		t.Fatal("private bootstrap invalid")
	}
	// Inherited descriptors arrive in blocking mode; set O_NONBLOCK before
	// NewFile so Go registers the pipes with its deadline-aware poller.
	if e := syscall.SetNonblock(3, true); e != nil {
		t.Fatal(e)
	}
	if e := syscall.SetNonblock(4, true); e != nil {
		t.Fatal(e)
	}
	ready := os.NewFile(3, "manager-ready")
	release := os.NewFile(4, "parent-hold")
	defer ready.Close()
	defer release.Close()
	// These inherited TEST pipes must not be retained by the separately execed
	// launcher/core; actual core fd link originals are checked by the parent.
	syscall.CloseOnExec(int(ready.Fd()))
	syscall.CloseOnExec(int(release.Fd()))
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(b.Plan, &obj); e != nil {
		t.Fatal(e)
	}
	c, e := OpenCoordinator(b.Paths, planFixtureValidator(t, obj), func(context.Context, Plan) (CurrentPlanInputs, error) { return b.Current, nil })
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	request, e := ParseRequest(b.ApplyRaw)
	if e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var checked checkedBasicMaterial
	var state applyEvidence
	stored, made, _, e := c.admitCheckedApplyLocked(ctx, request, func(plan Plan, base config.ActivePointer) error {
		var e error
		checked, e = checkBasicMaterial(ctx, plan, b.Material)
		if e != nil {
			return e
		}
		old, e := c.activeRunForApply(base, plan.Document().Impact.RuntimeAction, checked.launch.Workdir)
		if e != nil {
			return e
		}
		state = applyEvidence{Version: 1, Base: base, OldRunID: old, OldStopped: old == nil, TargetManifestSHA256: checked.launch.ManifestSHA256}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := c.store.CheckOperationAdmission(); e != nil {
			return e
		}
		sha, e := c.store.PublishBundle(checked.manifest, checked.files)
		if e != nil {
			return e
		}
		if sha != state.TargetManifestSHA256 {
			return config.ErrIntegrity
		}
		return nil
	})
	if e != nil || !made {
		t.Fatal("manager admission failed", made, e)
	}
	runtime, e := newBasicApplyRuntime(c.store, checked, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.close()
	_, e = c.executeBasicApply(ctx, stored, state, runtime, func(step string) error {
		if step != "active-published" {
			return nil
		}
		if runtime.registered == nil || runtime.registered.Observation().Gate != "closed" {
			t.Fatal("manager lost original registration")
		}
		message := crashCoreReady{1, os.Getpid(), request.OperationID(), runtime.prepared.Intent().RunID, step}
		if e := ready.SetWriteDeadline(time.Now().Add(2 * time.Second)); e != nil {
			t.Fatal(e)
		}
		if e := json.NewEncoder(ready).Encode(message); e != nil {
			t.Fatal("manager ready pipe", e)
		}
		if e := release.SetReadDeadline(time.Now().Add(20 * time.Second)); e != nil {
			t.Fatal(e)
		}
		// The successful test SIGKILLs this exact OS process while it holds the
		// coordinator mutex and Store flock. No Close path is executed before death.
		var one [1]byte
		_, e := release.Read(one[:])
		return errors.Join(errors.New("manager hold ended without SIGKILL"), e)
	})
	t.Fatal("manager execution returned instead of being killed", e)
}
func crashCoreSubreaper(t *testing.T) {
	t.Helper()
	var previous int32
	// Linux UAPI PR_SET_CHILD_SUBREAPER=36, PR_GET_CHILD_SUBREAPER=37.
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&previous)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal("subreaper query unavailable; environment unverified", errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal("subreaper setup unavailable; environment unverified", errno)
	}
	t.Logf("test process subreaper previous=%d set=1 parent_pid=%d", previous, os.Getpid())
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(previous), 0, 0, 0, 0)
		if e != 0 {
			t.Error("restore test process subreaper", e)
		} else {
			t.Logf("test process subreaper restored=%d", previous)
		}
	})
}
func crashCoreReap(ctx context.Context, pid int) (syscall.WaitStatus, error) {
	for {
		var status syscall.WaitStatus
		got, e := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if e != nil {
			return status, e
		}
		if got == pid {
			return status, nil
		}
		if got != 0 {
			return status, errors.New("unexpected adopted child wait PID")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return status, ctx.Err()
		case <-timer.C:
		}
	}
}
func crashCorePPID(t *testing.T, pid int) int {
	t.Helper()
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "PPid:") {
			var parent int
			if _, e := fmt.Sscan(strings.TrimPrefix(line, "PPid:"), &parent); e != nil {
				t.Fatal(e)
			}
			return parent
		}
	}
	t.Fatal("process parent observation missing")
	return 0
}
func crashCoreArtifact(t *testing.T, name string, raw []byte) {
	t.Helper()
	dir := os.Getenv("HOOPA_MANAGER_CRASH_ARTIFACTS")
	if dir != "" {
		st, e := os.Lstat(dir)
		if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
			t.Fatal("artifact directory must already be private", e)
		}
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(raw); e != nil {
			f.Close()
			t.Fatal(e)
		}
		if e = f.Close(); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("task original %s bytes=%d sha256=%s", name, len(raw), hash(raw))
}
func TestRecoveryCoreManagerSIGKILL(t *testing.T) {
	source := os.Getenv("HOOPA_P1_BASIC_CORE")
	if source == "" {
		t.Skip("explicit approved fixed core required")
	}
	crashCoreSubreaper(t)
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if hash(raw) != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" {
		t.Fatal("wrong approved core")
	}
	private := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(private, "mihomo")
	if e := os.WriteFile(binary, raw, 0755); e != nil {
		t.Fatal(e)
	}
	var addresses []string
	var reservations []*net.TCPListener
	for i := 0; i < 2; i++ {
		l, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if e != nil {
			t.Fatal(e)
		}
		defer l.Close()
		reservations = append(reservations, l)
		addresses = append(addresses, l.Addr().String())
	}
	for _, l := range reservations {
		l.Close()
	}
	seed, material := applyCoreMaterial(t, binary, addresses[0], addresses[1], "manager-crash-generation")
	cap, e := mihomo.ApprovedBasicCapability(binary)
	if e != nil {
		t.Fatal(e)
	}
	capSHA, e := cap.Digest()
	if e != nil {
		t.Fatal(e)
	}
	impact := PlanImpact{RequiresDisruption: true, AffectedApps: []string{}, TargetGeneration: string(material.Manifest.Generation), ConnectionEffect: "unknown", RuntimeAction: "start"}
	inputs := CurrentPlanInputs{CompilerVersion: "fixture-compiler-v1", CapabilitiesDigest: capSHA, Inputs: []PlanInput{{Kind: "core_binary", ID: "installation", SHA256: cap.Binary.SHA256}, {Kind: "capabilities", ID: "approved-profile", SHA256: capSHA}}, Impact: impact}
	paths, e := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if e != nil {
		t.Fatal(e)
	}
	setup, e := config.OpenStore(paths, true)
	if e != nil {
		t.Fatal(e)
	}
	base, e := setup.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	if e := setup.Close(); e != nil {
		t.Fatal(e)
	}
	plan := applyCorePlan(t, seed, base.Revision, impact)
	applyRaw := coordinatorRequest(t, "manager-crash-apply", plan, nil)
	request, e := ParseRequest(applyRaw)
	if e != nil {
		t.Fatal(e)
	}
	bootstrap := crashCoreBootstrap{1, paths, json.RawMessage(plan.Bytes()), material, inputs, applyRaw}
	encoded, e := json.Marshal(bootstrap)
	if e != nil {
		t.Fatal(e)
	}
	crashCoreArtifact(t, "manager-bootstrap.json", encoded)
	bootPath := filepath.Join(private, "manager-bootstrap.json")
	if e := os.WriteFile(bootPath, encoded, 0600); e != nil {
		t.Fatal(e)
	}
	readyRead, readyWrite, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer readyRead.Close()
	defer readyWrite.Close()
	holdRead, holdWrite, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer holdRead.Close()
	defer holdWrite.Close()
	pipeLinks := []string{}
	for _, f := range []*os.File{readyRead, holdWrite} {
		// File.Fd can change os.Pipe descriptors back to blocking mode. Keep
		// the parent's deadline-aware poller intact while observing its inode.
		raw, e := f.SyscallConn()
		if e != nil {
			t.Fatal(e)
		}
		var link string
		var linkErr error
		if e := raw.Control(func(fd uintptr) { link, linkErr = os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd)) }); e != nil {
			t.Fatal(e)
		}
		if linkErr != nil {
			t.Fatal(linkErr)
		}
		pipeLinks = append(pipeLinks, link)
	}
	// Probe this SAME ready pipe before any manager exists or any ready bytes
	// can be written. Reset before starting the actual protocol. A guard closes
	// the sole writer if a future regression makes the deadline ineffective.
	if e := readyRead.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	type deadlineRead struct {
		n   int
		err error
	}
	probe := make(chan deadlineRead, 1)
	go func() { var one [1]byte; n, e := readyRead.Read(one[:]); probe <- deadlineRead{n, e} }()
	select {
	case result := <-probe:
		if result.n != 0 || !errors.Is(result.err, os.ErrDeadlineExceeded) {
			t.Fatal("same ready pipe did not time out without consuming bytes", result.n, result.err)
		}
	case <-time.After(time.Second):
		readyWrite.Close()
		select {
		case <-probe:
		case <-time.After(time.Second):
			t.Error("deadline probe reader could not be collected")
		}
		t.Fatal("same ready pipe deadline failed before manager spawn")
	}
	if e := readyRead.SetReadDeadline(time.Time{}); e != nil {
		t.Fatal(e)
	}
	t.Log("same ready pipe no-data short deadline observed before manager start; zero bytes consumed; deadline reset")
	stdoutPath, stderrPath := filepath.Join(private, "manager.stdout"), filepath.Join(private, "manager.stderr")
	stdout, e := os.OpenFile(stdoutPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer stdout.Close()
	stderr, e := os.OpenFile(stderrPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer stderr.Close()
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(self, "-test.run=^TestRecoveryCrashManagerDispatch$", "-test.timeout=40s")
	cmd.Env = []string{"HOME=" + private, "TMPDIR=" + private, "LANG=C", "HOOPA_CRASH_MANAGER=1", "HOOPA_CRASH_BOOTSTRAP=" + bootPath, "HOOPA_CRASH_BOOTSTRAP_SHA256=" + hash(encoded)}
	cmd.ExtraFiles = []*os.File{readyWrite, holdRead}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	readyWrite.Close()
	holdRead.Close()
	managerPID := cmd.Process.Pid
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	waited := false
	var retained *platform.ProcessHandle
	var coreIntent config.RunIntent
	var store *config.Store
	var c *Coordinator
	defer func() {
		// Manager is our direct exec.Cmd child, so its Process is safe to signal even
		// if readiness failed. Core signals require a separately proved pidfd.
		if !waited {
			_ = cmd.Process.Kill()
			select {
			case <-wait:
				waited = true
			case <-time.After(5 * time.Second):
				t.Error("owned manager Wait timeout")
			}
		}
		if c != nil {
			defer c.Close()
		}
		if store != nil {
			defer store.Close()
		}
		if retained == nil {
			if store == nil {
				r, e := config.OpenStore(paths, false)
				if e == nil {
					store = r
					defer r.Close()
				}
			}
			if store != nil {
				ids, e := store.ListRunIDs()
				if e == nil {
					for _, id := range ids {
						intent, _, ie := store.ReadRunIntent(id)
						birth, _, be := store.ReadRunBirth(id)
						if ie != nil || be != nil || intent.OperationID != request.OperationID() {
							continue
						}
						h, he := platform.ObserveProcess(applyCoreContext(t), birth.PID, platform.ProcessSpec{BinaryPath: binary, BinarySHA256: cap.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
						if he == nil {
							actual := h.Identity()
							if actual.PID == birth.PID && actual.UID == birth.UID && actual.BootID == birth.BootID && actual.StartTicks == birth.StartTicks && h.VerifyArguments(applyCoreContext(t), intent.Arguments) == nil {
								retained = h
								coreIntent = intent
								break
							}
							h.Close()
						}
					}
				}
			}
		}
		if retained == nil {
			t.Error("no strongly verified core cleanup identity; no numeric PID signal")
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e := retained.VerifyArguments(ctx, coreIntent.Arguments)
			if e == nil {
				e = retained.SignalVerified(ctx, syscall.SIGTERM)
			}
			if e == nil {
				e = retained.WaitExited(ctx)
			}
			var status syscall.WaitStatus
			if e == nil {
				status, e = crashCoreReap(ctx, retained.Identity().PID)
			}
			if e == nil {
				e = applyCoreAbsent(ctx, retained.Identity().PID)
			}
			if e != nil {
				t.Error("proved adopted core cleanup incomplete", e)
			} else if !status.Exited() || status.ExitStatus() != 0 {
				t.Errorf("actual adopted core Wait4 returned status=%#x", uint32(status))
			} else {
				t.Logf("adopted core cleanup original_pidfd_exit=true wait4_pid=%d exit=0 signal=0 proc_absent=true", retained.Identity().PID)
			}
			retained.Close()
			cancel()
		}
		for _, v := range []struct{ name, path string }{{"manager.stdout", stdoutPath}, {"manager.stderr", stderrPath}, {"core-launcher.log", filepath.Join(material.Workdir, "launcher.log")}} {
			b, e := os.ReadFile(v.path)
			if e != nil {
				t.Error("task original log unavailable", v.name, e)
				continue
			}
			crashCoreArtifact(t, v.name, b)
			if strings.Contains(string(b), strings.Repeat("s", 32)) {
				t.Error("fixture credential found in task diagnostic; content withheld")
			}
		}
	}()
	if e := readyRead.SetReadDeadline(time.Now().Add(20 * time.Second)); e != nil {
		t.Fatal(e)
	}
	line, e := bufio.NewReader(io.LimitReader(readyRead, 4096)).ReadBytes('\n')
	if e != nil {
		t.Fatal("manager readiness", e)
	}
	var ready crashCoreReady
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&ready); e != nil || ready.Version != 1 || ready.ManagerPID != managerPID || ready.OperationID != request.OperationID() || ready.Step != "active-published" || ready.RunID == "" {
		t.Fatal("manager ready binding", e)
	}
	crashCoreArtifact(t, "manager-ready.json", line)
	store, e = config.OpenStore(paths, false)
	if e != nil {
		t.Fatal(e)
	}
	coreIntent, intentSHA, e := store.ReadRunIntent(ready.RunID)
	if e != nil {
		t.Fatal(e)
	}
	birth, birthSHA, e := store.ReadRunBirth(ready.RunID)
	if e != nil {
		t.Fatal(e)
	}
	reg, registrationSHA, e := store.ReadRunRegistration(ready.RunID, request.OperationID())
	if e != nil || reg.Match(coreIntent, intentSHA, birth, birthSHA) != nil {
		t.Fatal("original birth/registration binding", e)
	}
	// Archive only bytes whose original Store digests were actually used above.
	originals := map[string]struct{ path, sha string }{
		"run-intent.json":       {filepath.Join(paths.State, "runs", hash([]byte(ready.RunID)), "intent.json"), intentSHA},
		"run-birth.json":        {filepath.Join(paths.State, "runs", hash([]byte(ready.RunID)), "birth.json"), birthSHA},
		"run-registration.json": {filepath.Join(paths.State, "runs", hash([]byte(ready.RunID)), "registration-"+hash([]byte(request.OperationID()))+".json"), registrationSHA},
	}
	for name, original := range originals {
		bytes, e := os.ReadFile(original.path)
		if e != nil || hash(bytes) != original.sha {
			t.Fatal("archived run original differs from used Store digest", name, e)
		}
		crashCoreArtifact(t, "before-kill-"+name, bytes)
	}
	var identity platform.ProcessIdentity
	retained, identity = applyCoreHandle(t, store, ready.RunID)
	if identity.PID == managerPID || identity.PID != birth.PID || crashCorePPID(t, identity.PID) != managerPID {
		t.Fatal("core is not original manager child")
	}
	fds, e := os.ReadDir(fmt.Sprintf("/proc/%d/fd", identity.PID))
	if e != nil {
		t.Fatal(e)
	}
	for _, fd := range fds {
		link, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", identity.PID, fd.Name()))
		for _, pipe := range pipeLinks {
			if link == pipe {
				t.Fatal("actual core inherited test manager pipe")
			}
		}
	}
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(plan.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	if other, e := OpenCoordinator(paths, planFixtureValidator(t, obj), func(context.Context, Plan) (CurrentPlanInputs, error) { return inputs, nil }); !errors.Is(e, config.ErrWriterBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatal("manager did not hold writer flock", e)
	}
	journal, e := NewJournal(store)
	if e != nil {
		t.Fatal(e)
	}
	interrupted, e := journal.Lookup(request)
	if e != nil || interrupted.Record.Phase != Publishing {
		t.Fatal("original durable publishing cut", e)
	}
	managerStat, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", managerPID))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(string(managerStat), fmt.Sprintf("%d (", managerPID)) {
		t.Fatal("manager stat original PID mismatch")
	}
	crashCoreArtifact(t, "manager-proc-stat", managerStat)
	publishedActive, e := store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	crashCoreActiveOriginal(t, "before-kill-active.json", paths, publishedActive)
	interruptedRaw, interruptedSHA, e := store.ReadOperationFile(request.OperationID())
	if e != nil || interruptedSHA != interrupted.SHA256 {
		t.Fatal("original apply archive binding", e)
	}
	crashCoreArtifact(t, "before-kill-apply-operation.json", interruptedRaw)
	var echoes atomic.Int32
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		echoes.Add(1)
		io.WriteString(w, "manager-crash-echo:"+r.URL.Path)
	}))
	defer echo.Close()
	target := strings.TrimPrefix(echo.URL, "http://")
	checkEcho := func(phase string) {
		t.Helper()
		status, body := applyCoreHTTP(t, addresses[1], target, phase, true)
		if status != 200 || body != "manager-crash-echo:/"+phase {
			t.Fatal("core forwarding across manager death", phase, status)
		}
	}
	checkEcho("before-kill")
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal("kill own manager Process", e)
	}
	var waitErr error
	select {
	case waitErr = <-wait:
		waited = true
	case <-time.After(5 * time.Second):
		t.Fatal("manager SIGKILL Wait timeout")
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if waitErr == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("manager did not exit via actual SIGKILL", waitErr)
	}
	if e := retained.VerifyArguments(applyCoreContext(t), coreIntent.Arguments); e != nil {
		t.Fatal("manager SIGKILL affected core", e)
	}
	if crashCorePPID(t, identity.PID) != os.Getpid() {
		t.Fatal("subreaper did not adopt original core")
	}
	checkEcho("after-kill")
	t.Logf("manager actual SIGKILL Process.Wait pid=%d signal=%d parent_pid=%d core_pid=%d retained_original_identity=%+v writer_was_busy=true test_pipes_not_inherited=true", managerPID, status.Signal(), os.Getpid(), identity.PID, identity)
	// This newly opened Coordinator gets only immutable paths/fixtures. No parent
	// retained process handle is passed into its public recovery implementation.
	c, e = OpenCoordinator(paths, planFixtureValidator(t, obj), func(context.Context, Plan) (CurrentPlanInputs, error) {
		t.Fatal("recovery compiler dependency")
		return CurrentPlanInputs{}, nil
	})
	if e != nil {
		t.Fatal("writer flock unavailable after OS death", e)
	}
	applier, e := NewBasicApplier(c, func(context.Context, Plan) (BasicMaterial, error) {
		t.Fatal("recovery Source/spawn path")
		return BasicMaterial{}, ErrPlan
	}, applyCoreEntryArgs())
	if e != nil {
		t.Fatal(e)
	}
	active, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	recoverRaw := recoveryRequest(t, "manager-crash-recover", request.OperationID(), active.Revision)
	recoveryRequestIdentity, e := ParseRequest(recoverRaw)
	if e != nil {
		t.Fatal(e)
	}
	beforeRecoverRaw, beforeRecoverSHA, e := c.store.ReadOperationFile(request.OperationID())
	if e != nil || beforeRecoverSHA != interruptedSHA || !reflect.DeepEqual(beforeRecoverRaw, interruptedRaw) {
		t.Fatal("OS death changed original apply bytes", e)
	}
	crashCoreArtifact(t, "before-recover-apply-operation.json", beforeRecoverRaw)
	crashCoreActiveOriginal(t, "before-recover-active.json", paths, active)
	if _, _, e := c.store.ReadOperationFile(recoveryRequestIdentity.OperationID()); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("new recovery call was not absent", e)
	}
	absence, e := json.Marshal(map[string]any{"operation_id": recoveryRequestIdentity.OperationID(), "observed": "absent_before_admission"})
	if e != nil {
		t.Fatal(e)
	}
	crashCoreArtifact(t, "before-recover-call-absence.json", append(absence, '\n'))
	recovered, made, e := applier.Recover(applyCoreContext(t), recoverRaw)
	if e != nil || !made || recovered.Record.Phase != Committed {
		t.Fatal("public recovery after manager SIGKILL", made, e)
	}
	final, e := c.journal.Get(request.OperationID())
	if e != nil || final.Record.Phase != Committed || !reflect.DeepEqual(final.Record.Identity, interrupted.Record.Identity) {
		t.Fatal("original request identity changed", e)
	}
	afterApply, afterApplySHA, e := c.store.ReadOperationFile(request.OperationID())
	if e != nil || afterApplySHA != final.SHA256 {
		t.Fatal("final original archive binding", e)
	}
	crashCoreArtifact(t, "after-recover-apply-operation.json", afterApply)
	afterCall, afterCallSHA, e := c.store.ReadOperationFile(recovered.Record.Identity.OperationID)
	if e != nil || afterCallSHA != recovered.SHA256 {
		t.Fatal("final recovery call archive binding", e)
	}
	crashCoreArtifact(t, "after-recover-call-operation.json", afterCall)
	afterActive, e := c.store.ReadActive()
	if e != nil || !reflect.DeepEqual(afterActive, active) {
		t.Fatal("recovery altered published pointer", e)
	}
	crashCoreActiveOriginal(t, "after-recover-active.json", paths, afterActive)
	for name, original := range originals {
		bytes, e := os.ReadFile(original.path)
		if e != nil || hash(bytes) != original.sha {
			t.Fatal("recovery rewrote original run bytes", name, e)
		}
		crashCoreArtifact(t, "after-recover-"+name, bytes)
	}
	fresh, freshIdentity := applyCoreHandle(t, c.store, ready.RunID)
	fresh.Close()
	if freshIdentity != identity {
		t.Fatal("new manager recovered a different core")
	}
	ids, e := c.store.ListRunIDs()
	if e != nil || !reflect.DeepEqual(ids, []config.RunID{ready.RunID}) {
		t.Fatal("recovery created extra RunIntent", ids, e)
	}
	checkEcho("after-recover")
	if echoes.Load() != 3 {
		t.Fatal("unexpected forwarding count", echoes.Load())
	}
	before := coordinatorTree(t, paths)
	replay, made, e := applier.Recover(nil, recoverRaw)
	if e != nil || made || !reflect.DeepEqual(replay, recovered) {
		t.Fatal("recover request replay", e)
	}
	replay, made, e = applier.Apply(nil, applyRaw)
	if e != nil || made || !reflect.DeepEqual(replay, final) || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("original request replay had effects", e)
	}
	if e := retained.VerifyArguments(applyCoreContext(t), coreIntent.Arguments); e != nil {
		t.Fatal("recovery/replay signalled original core", e)
	}
	t.Logf("actual manager-death recovery one_core=true manager_sigkill_wait=true same_core_run=%s same_pid=%d new_manager_fresh_ownership=true original_apply_committed=true independent_recover_committed=true authorized_echoes=%d replay_new_runs=0", ready.RunID, identity.PID, echoes.Load())
}

// The decoded pointer compared here comes from the real Store read used by the
// assertions, while the retained original is its exact persisted byte envelope.
func crashCoreActiveOriginal(t *testing.T, name string, paths config.Paths, want config.ActivePointer) {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(paths.State, "active.json"))
	if e != nil {
		t.Fatal(e)
	}
	var got config.ActivePointer
	if e := json.Unmarshal(raw, &got); e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("active original differs from Store observation", e)
	}
	crashCoreArtifact(t, name, raw)
}
