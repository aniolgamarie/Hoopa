//go:build linux && amd64

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

// These helpers execute only disposable copies of this Go test process. The
// controller is an explicit synthetic protocol, not HTTP/Mihomo authentication.
func TestOwnershipFixture(t *testing.T) {
	mode := os.Getenv("HOOPA_OWNERSHIP_FIXTURE")
	if mode == "" {
		return
	} // Empty parent helper is not subprocess coverage.
	if mode == "launcher" {
		directory := os.NewFile(3, "inherited-birth")
		intent, err := ClaimLauncher(context.Background(), directory, os.Getenv("HOOPA_FIXTURE_INTENT_SHA"), os.Getenv("HOOPA_RUN_NONCE"))
		if err != nil {
			t.Fatal("launcher claim failed:", err)
		}
		if _, err := directory.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("launcher retained birth FD")
		}
		os.Setenv("HOOPA_OWNERSHIP_FIXTURE", "core")
		if os.Getenv("HOOPA_FIXTURE_BAD_NONCE") == "1" {
			os.Setenv("HOOPA_RUN_NONCE", strings.Repeat("cd", 32))
		}
		argv := append([]string(nil), intent.Arguments...)
		if os.Getenv("HOOPA_FIXTURE_BAD_ARGV") == "1" {
			argv[len(argv)-1] = "-test.timeout=29s"
		}
		if err := syscall.Exec(intent.Binary.Path, argv, os.Environ()); err != nil {
			t.Fatal("fixture exec failed:", err)
		}
		t.Fatal("exec returned")
	}
	if mode != "core" {
		t.Fatal("invalid fixture mode")
	}
	// Before constructing a listener (which may reuse FD3), verify exec did not
	// inherit the original directory inode. A reused unrelated runtime FD is OK.
	var info syscall.Stat_t
	err := syscall.Fstat(3, &info)
	if err == nil && fmt.Sprintf("%d:%d", info.Dev, info.Ino) == os.Getenv("HOOPA_FIXTURE_DIRECTORY_ID") {
		t.Fatal("core inherited birth directory FD3")
	}
	if err != nil && err != syscall.EBADF {
		t.Fatal("FD3 inspection failed:", err)
	}
	var observation ControllerObservation
	if err := json.Unmarshal([]byte(os.Getenv("HOOPA_FIXTURE_OBSERVATION")), &observation); err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(4, "inherited-listener")
	raw, err := net.FileListener(file)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	listener := raw.(*net.TCPListener)
	defer listener.Close()
	// Listener deadline also bounds an orphan fixture if its parent disappears.
	listener.SetDeadline(time.Now().Add(15 * time.Second))
	go func() { io.Copy(io.Discard, os.Stdin); listener.Close() }()
	fmt.Printf("CORE_READY pid=%d birth_fd_closed=true\n", os.Getpid())
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.Fatal("fixture accept:", err)
		}
		fmt.Println("CONTROLLER_ACCEPT")
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		request := make([]byte, 4)
		if _, err = io.ReadFull(conn, request); err != nil {
			conn.Close()
			continue
		}
		if string(request) != "OBS\n" {
			conn.Close()
			t.Fatal("unexpected synthetic request")
		}
		gate, err := os.ReadFile("gate")
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		observation.Gate = strings.TrimSpace(string(gate))
		if err := json.NewEncoder(conn).Encode(observation); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		// Keep the accepted FD stable while the observer performs its post-check.
		io.Copy(io.Discard, conn)
		conn.Close()
	}
}

func ownershipDigest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func ownershipContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func ownershipTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + ownershipDigest(b)
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type ownershipInspector struct {
	calls  atomic.Int32
	change string
}

func (i *ownershipInspector) Inspect(ctx context.Context, conn *net.TCPConn, intent config.RunIntent) (ControllerObservation, error) {
	i.calls.Add(1)
	if _, err := conn.Write([]byte("OBS\n")); err != nil {
		return ControllerObservation{}, err
	}
	var obs ControllerObservation
	err := json.NewDecoder(conn).Decode(&obs)
	if i.change == "config" {
		obs.ConfigSHA256 = ownershipDigest([]byte("wrong-config"))
	}
	if i.change == "gate" {
		obs.Gate = "partially-open"
	}
	return obs, err
}

type ownershipFixture struct {
	paths                     config.Paths
	store                     *config.Store
	intent                    config.RunIntent
	nonce, intentSHA, runPath string
	cmd                       *exec.Cmd
	stdin                     io.WriteCloser
	exited                    chan error
	waited                    bool
	log                       string
	forbidNetwork             bool
}

func ownershipPrepared(t *testing.T, controller string, badBinary bool) (*ownershipFixture, *config.PreparedRun) {
	t.Helper()
	base := t.TempDir()
	paths, err := config.ResolvePaths(filepath.Join(base, "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	f := &ownershipFixture{paths: paths, store: store, nonce: strings.Repeat("ab", 32)}
	t.Cleanup(func() {
		if f.store != nil {
			if err := f.store.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	configData := []byte("synthetic fake-core configuration\n")
	binarySHA := ownershipDigest(binary)
	if badBinary {
		binarySHA = ownershipDigest([]byte("wrong-binary"))
	}
	manifest := config.BundleManifest{Version: 1, Generation: "fixture-bundle", CompilerVersion: "fixture-v1", CapabilitiesDigest: ownershipDigest([]byte("synthetic-controller")), Binaries: []config.BinaryReference{{Path: exe, Version: "fake-core-v1", SHA256: binarySHA}}, Files: []config.BundleFile{{Kind: "generated_config", Name: "core.yaml", Bytes: int64(len(configData)), SHA256: ownershipDigest(configData)}}}
	manifestSHA, err := store.PublishBundle(manifest, map[string][]byte{"core.yaml": configData})
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(base, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "gate"), []byte("closed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.intent = config.RunIntent{Version: 1, RunID: "fixture-run", OperationID: "launch-operation", Generation: manifest.Generation, ManifestSHA256: manifestSHA, Binary: manifest.Binaries[0], Workdir: work, Arguments: []string{exe, "-test.run=^TestOwnershipFixture$", "-test.timeout=30s"}, Controller: controller, ConfigSHA256: ownershipDigest(configData), NonceSHA256: ownershipDigest([]byte(f.nonce))}
	ticket, err := store.PrepareRun(f.intent)
	if err != nil {
		t.Fatal(err)
	}
	f.intentSHA = ticket.Digest()
	f.runPath = filepath.Join(paths.State, "runs", ownershipDigest([]byte(f.intent.RunID)))
	return f, ticket
}
func startOwnershipFixture(t *testing.T, option string) *ownershipFixture {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	f, ticket := ownershipPrepared(t, listener.Addr().String(), option == "binary")
	directory, err := ticket.TakeBirthDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	tcp, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	observation := ControllerObservation{RunID: f.intent.RunID, Generation: f.intent.Generation, ConfigSHA256: f.intent.ConfigSHA256, BinaryVersion: f.intent.Binary.Version, Gate: "closed"}
	encoded, _ := json.Marshal(observation)
	cmd := exec.Command(f.intent.Binary.Path, "-test.run=^TestOwnershipFixture$", "-test.timeout=30s")
	cmd.Dir = f.intent.Workdir
	cmd.ExtraFiles = []*os.File{directory, tcp}
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "HOOPA_FIXTURE_") && !strings.HasPrefix(item, "HOOPA_OWNERSHIP_FIXTURE=") && !strings.HasPrefix(item, "HOOPA_RUN_NONCE=") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "HOOPA_OWNERSHIP_FIXTURE=launcher", "HOOPA_RUN_NONCE="+f.nonce, "HOOPA_FIXTURE_INTENT_SHA="+f.intentSHA, "HOOPA_FIXTURE_OBSERVATION="+string(encoded), "HOOPA_FIXTURE_DIRECTORY_ID="+fmt.Sprintf("%d:%d", stat.Dev, stat.Ino))
	if option == "nonce" {
		cmd.Env = append(cmd.Env, "HOOPA_FIXTURE_BAD_NONCE=1")
	}
	if option == "argv" {
		cmd.Env = append(cmd.Env, "HOOPA_FIXTURE_BAD_ARGV=1")
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	f.log = filepath.Join(f.intent.Workdir, "child.log")
	output, err := os.OpenFile(f.log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	output.Close()
	f.cmd = cmd
	f.stdin = input
	f.exited = make(chan error, 1)
	go func() { f.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		f.stdin.Close()
		if !f.waited {
			select {
			case err := <-f.exited:
				f.waited = true
				if err != nil {
					t.Error("fixture child failed:", err)
				}
			case <-time.After(4 * time.Second):
				f.cmd.Process.Kill()
				err := <-f.exited
				f.waited = true
				t.Error("fixture required kill after timeout:", err)
			}
		}
		if data, err := os.ReadFile(f.log); err == nil {
			t.Logf("child original log:\n%s", data)
			if f.forbidNetwork && strings.Contains(string(data), "CONTROLLER_ACCEPT") {
				t.Error("unverified birth/identity reached controller socket")
			}
		}
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		data, _ := os.ReadFile(f.log)
		if strings.Contains(string(data), fmt.Sprintf("CORE_READY pid=%d birth_fd_closed=true", cmd.Process.Pid)) {
			return f
		}
		select {
		case err := <-f.exited:
			f.waited = true
			t.Fatalf("fixture exited before ready: %v\n%s", err, data)
		case <-timer.C:
			t.Fatalf("fixture ready timeout:\n%s", data)
		case <-tick.C:
		}
	}
}
func (f *ownershipFixture) assertRunning(t *testing.T) {
	t.Helper()
	select {
	case err := <-f.exited:
		f.waited = true
		t.Fatal("core fixture unexpectedly exited:", err)
	default:
	}
}
func ownershipManager(t *testing.T, s *config.Store, i ControllerInspector) *Ownership {
	t.Helper()
	o, err := NewOwnership(s, i)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOwnershipObserveRegisterAndRecoverSameProcess(t *testing.T) {
	f := startOwnershipFixture(t, "")
	inspector := &ownershipInspector{}
	owner := ownershipManager(t, f.store, inspector)
	before := ownershipTree(t, f.paths.State)
	observed, err := owner.Observe(ownershipContext(t), f.intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	id := observed.Identity()
	if id.PID != f.cmd.Process.Pid {
		t.Fatal("ownership changed launch PID")
	}
	observed.Close()
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("Observe wrote store")
	}
	registered, sha, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
	if err != nil {
		t.Fatal(err)
	}
	registered.Close()
	f.assertRunning(t)
	old, _, err := f.store.ReadRunRegistration(f.intent.RunID, f.intent.OperationID)
	if err != nil || old.Gate != "closed" || old.PID != id.PID || old.StartTicks != id.StartTicks {
		t.Fatal("closed registration not bound:", err)
	}
	f.store.Close()
	reader, err := config.OpenStore(f.paths, false)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reader
	owner = ownershipManager(t, reader, inspector)
	before = ownershipTree(t, f.paths.State)
	observed, err = owner.Observe(ownershipContext(t), f.intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	observed.Close()
	if got, _, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, false); got != nil || !errors.Is(err, config.ErrReadOnly) {
		if got != nil {
			got.Close()
		}
		t.Fatal("existing registration bypassed read-only writer guard:", err)
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("read-only ownership changed store")
	}
	reader.Close()
	writer, err := config.OpenStore(f.paths, true)
	if err != nil {
		t.Fatal(err)
	}
	f.store = writer
	owner = ownershipManager(t, writer, inspector)
	if err := os.WriteFile(filepath.Join(f.intent.Workdir, "gate"), []byte("open\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before = ownershipTree(t, f.paths.State)
	current, again, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, false)
	if err != nil {
		t.Fatal(err)
	}
	if again != sha || current.Observation().Gate != "open" || current.Identity() != id {
		t.Fatal("recovery lost original instance or fresh gate")
	}
	current.Close()
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("same operation rewrote historical closed gate")
	}
	recovered, _, err := owner.Register(ownershipContext(t), f.intent.RunID, "recovery-operation", false)
	if err != nil {
		t.Fatal(err)
	}
	recovered.Close()
	reg, _, err := writer.ReadRunRegistration(f.intent.RunID, "recovery-operation")
	if err != nil || reg.Gate != "open" || reg.PID != old.PID || reg.StartTicks != old.StartTicks || reg.BirthSHA256 != old.BirthSHA256 {
		t.Fatal("new recovery operation did not bind same birth:", err)
	}
	if run, _, err := owner.Register(ownershipContext(t), f.intent.RunID, "closed-required", true); run != nil || !errors.Is(err, ErrOwnershipUnknown) {
		if run != nil {
			run.Close()
		}
		t.Fatal("open core accepted initial closed requirement:", err)
	}
	if _, _, err := writer.ReadRunRegistration(f.intent.RunID, "closed-required"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("open failure registered:", err)
	}
	f.assertRunning(t)
}

func TestOwnershipRejectsUnverifiedEvidence(t *testing.T) {
	for _, name := range []string{"missing-birth", "birth-start", "birth-pid", "argv", "nonce", "binary", "config", "gate"} {
		t.Run(name, func(t *testing.T) {
			f := startOwnershipFixture(t, name)
			inspector := &ownershipInspector{}
			if name == "config" || name == "gate" {
				inspector.change = name
			}
			owner := ownershipManager(t, f.store, inspector)
			f.forbidNetwork = name != "config" && name != "gate"
			birthPath := filepath.Join(f.runPath, "birth.json")
			if name == "missing-birth" {
				if err := os.Remove(birthPath); err != nil {
					t.Fatal(err)
				}
			}
			if name == "birth-start" || name == "birth-pid" {
				raw, err := os.ReadFile(birthPath)
				if err != nil {
					t.Fatal(err)
				}
				var b config.RunBirth
				if err := json.Unmarshal(raw, &b); err != nil {
					t.Fatal(err)
				}
				if name == "birth-pid" {
					b.PID = os.Getpid()
				} else {
					b.StartTicks++
				}
				raw, _ = json.Marshal(b)
				raw = append(raw, '\n')
				if err := os.WriteFile(birthPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := ownershipTree(t, f.paths.State)
			run, _, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
			if run != nil || !errors.Is(err, ErrOwnershipUnknown) {
				if run != nil {
					run.Close()
				}
				t.Fatal("unverified evidence accepted:", err)
			}
			if name != "config" && name != "gate" && inspector.calls.Load() != 0 {
				t.Fatal("inspector reached before complete identity/birth proof")
			}
			if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
				t.Fatal("unverified observation wrote registration")
			}
			f.assertRunning(t)
		})
	}
}

func TestOwnershipMalformedRegistrationPreserved(t *testing.T) {
	f := startOwnershipFixture(t, "")
	owner := ownershipManager(t, f.store, &ownershipInspector{})
	run, _, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, true)
	if err != nil {
		t.Fatal(err)
	}
	run.Close()
	path := filepath.Join(f.runPath, "registration-"+ownershipDigest([]byte(f.intent.OperationID))+".json")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := ownershipTree(t, f.paths.State)
	if run, _, err := owner.Register(ownershipContext(t), f.intent.RunID, f.intent.OperationID, false); err == nil || run != nil {
		if run != nil {
			run.Close()
		}
		t.Fatal("malformed registration repaired")
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("malformed registration changed")
	}
	f.assertRunning(t)
}
