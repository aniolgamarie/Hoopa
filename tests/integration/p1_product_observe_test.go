//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
	"hoopa/internal/testsupport/p1fixture"
)

var errP1RunObservation = errors.New("P1_FRESH_RUN_OBSERVATION_UNKNOWN")

type p1RunObservation struct {
	Identity                  processIdentity
	Observation               owned.ControllerObservation
	Original                  originalRef
	IntentSHA256, BirthSHA256 string
}

func (p1RunObservation) String() string   { return "p1RunObservation{private}" }
func (p1RunObservation) GoString() string { return "p1RunObservation{private}" }

// p1ObserveRun is fresh connected-owner evidence, not managed activation proof.
// The caller supplies OpenStore(paths,false); Store has no public readonly-state
// accessor. This helper uses read-only APIs even if supplied a writer handle.
// Receipt time is this observation time, not forwarding readiness or latency.
func p1ObserveRun(ctx context.Context, store *config.Store, id config.RunID, captureDir string) (p1RunObservation, error) {
	fail := func() (p1RunObservation, error) {
		if ctx != nil && ctx.Err() != nil {
			return p1RunObservation{}, errors.Join(errP1RunObservation, ctx.Err())
		}
		return p1RunObservation{}, errP1RunObservation
	}
	if ctx == nil || store == nil || ctx.Err() != nil {
		return fail()
	}
	directory, err := p1OpenObserveCapture(captureDir)
	if err != nil {
		return fail()
	}
	defer directory.Close()
	intent, intentSHA, err := store.ReadRunIntent(id)
	if err != nil {
		return fail()
	}
	birth, birthSHA, err := store.ReadRunBirth(id)
	if err != nil || birth.Match(intent, intentSHA) != nil {
		return fail()
	}
	capability, err := mihomo.ApprovedBasicCapability(intent.Binary.Path)
	if err != nil || capability.Binary != intent.Binary {
		return fail()
	}
	controller, err := mihomo.NewController(store, capability)
	if err != nil {
		return fail()
	}
	ownership, err := owned.NewOwnership(store, controller)
	if err != nil {
		return fail()
	}
	run, err := ownership.Observe(ctx, id)
	if err != nil {
		return fail()
	}
	closed := false
	defer func() {
		if !closed {
			_ = run.Close()
		}
	}()
	native, observation := run.Identity(), run.Observation()
	// Bind receipt references to the same immutable source originals after the
	// fresh observer independently read and validated them. No activation call.
	againIntent, againIntentSHA, err := store.ReadRunIntent(id)
	if err != nil || againIntentSHA != intentSHA || !reflect.DeepEqual(againIntent, intent) {
		return fail()
	}
	againBirth, againBirthSHA, err := store.ReadRunBirth(id)
	if err != nil || againBirthSHA != birthSHA || againBirth != birth || ctx.Err() != nil {
		return fail()
	}
	observedAt := time.Now().UTC()
	err = run.Close()
	closed = true
	if err != nil {
		return fail()
	}
	identity := processIdentity{BootID: native.BootID, OSStartIdentity: strconv.FormatUint(native.StartTicks, 10), BinaryPath: native.BinaryPath, BinarySHA256: native.BinarySHA256, LaunchNonceSHA256: native.NonceSHA256, PrivateWorkdir: native.Workdir, PID: native.PID, Generation: observation.Generation, RunID: observation.RunID}
	captureID := make([]byte, 24)
	if _, err = rand.Read(captureID); err != nil {
		return fail()
	}
	token := hex.EncodeToString(captureID)
	receipt := struct {
		Version         int                         `json:"version"`
		Kind            string                      `json:"kind"`
		CaptureID       string                      `json:"capture_id"`
		ObservedAt      time.Time                   `json:"observed_at"`
		RunID           config.RunID                `json:"run_id"`
		Identity        platform.ProcessIdentity    `json:"identity"`
		ProcessIdentity processIdentity             `json:"process_identity"`
		Observation     owned.ControllerObservation `json:"observation"`
		IntentSHA256    string                      `json:"intent_sha256"`
		BirthSHA256     string                      `json:"birth_sha256"`
	}{1, "fresh-connected-owner-observation", token, observedAt, id, native, identity, observation, intentSHA, birthSHA}
	raw, err := json.Marshal(receipt)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	raw = append(raw, '\n')
	name := "run-observation-" + token + ".json"
	fd, err := syscall.Openat(int(directory.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return fail()
	}
	file := os.NewFile(uintptr(fd), "private-run-observation")
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fail()
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		file.Close()
		return fail()
	}
	n, writeErr := file.Write(raw)
	if n != len(raw) {
		writeErr = errors.Join(writeErr, io.ErrShortWrite)
	}
	writeErr = errors.Join(writeErr, file.Sync(), file.Close())
	if writeErr != nil || directory.Sync() != nil || ctx.Err() != nil {
		return fail()
	}
	return p1RunObservation{Identity: identity, Observation: observation, Original: originalRef{Path: filepath.Join(captureDir, name), Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, IntentSHA256: intentSHA, BirthSHA256: birthSHA}, nil
}

// Every path component is opened relative to its original parent without
// symlinks. Only the final directory needs private0700/current-UID ownership;
// ancestors may be ordinary root-owned directories such as /tmp or /data.
func p1OpenObserveCapture(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return nil, errP1RunObservation
	}
	directory, err := os.Open("/")
	if err != nil {
		return nil, errP1RunObservation
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if component == "" {
			continue
		}
		fd, err := syscall.Openat(int(directory.Fd()), component, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		_ = directory.Close()
		if err != nil {
			return nil, errP1RunObservation
		}
		directory = os.NewFile(uintptr(fd), "private-observation-directory")
	}
	info, err := directory.Stat()
	if err != nil {
		directory.Close()
		return nil, errP1RunObservation
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Mode&07777 != 0700 || stat.Uid != uint32(os.Geteuid()) {
		directory.Close()
		return nil, errP1RunObservation
	}
	return directory, nil
}

// Negative, no-core fixtures only. Synthetic birth identifies this test process
// with deliberately false boot/start/executable material; it cannot grant proof.
func p1ObserveNoCoreFixture(t *testing.T, mode string) (*config.Store, *p1fixture.Fixture, config.RunID) {
	t.Helper()
	store, fixture, _ := p1ProductReadFixture(t)
	material := fixture.Material()
	if mode == "unsupported-binary" {
		material.Manifest.Binaries[0].Version = "unsupported"
	}
	sha, err := store.PublishBundle(material.Manifest, material.Files)
	must(t, err)
	nonce := strings.Repeat("ab", 32)
	intent := config.RunIntent{Version: 1, RunID: "no-core-observation", OperationID: "no-core-operation", Generation: material.Manifest.Generation, ManifestSHA256: sha, Binary: material.Manifest.Binaries[0], Workdir: material.Workdir, Arguments: []string{material.Manifest.Binaries[0].Path, "-d", material.Workdir}, Controller: fixture.Snapshot().Controller, ConfigSHA256: fixture.Snapshot().ConfigSHA256, NonceSHA256: p1SHA([]byte(nonce))}
	if mode != "missing-run" {
		ticket, err := store.PrepareRun(intent)
		must(t, err)
		if mode != "missing-birth" {
			fd, err := ticket.TakeBirthDirectory()
			must(t, err)
			dir, err := os.OpenRoot("/proc/self/fd/" + strconv.Itoa(int(fd.Fd())))
			must(t, err)
			birth := config.RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: ticket.Digest(), PID: os.Getpid(), UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
			must(t, config.ClaimRunBirth(dir, ticket.Digest(), nonce, birth))
			must(t, dir.Close())
			must(t, fd.Close())
		}
	}
	must(t, store.Close())
	paths, err := config.ResolvePaths(filepath.Dir(material.Manifest.Binaries[0].Path), nil)
	must(t, err)
	reader, err := config.OpenStore(paths, false)
	must(t, err)
	t.Cleanup(func() { must(t, reader.Close()) })
	return reader, fixture, intent.RunID
}
func TestP1ProductObserveNoCoreNeverFabricatesProof(t *testing.T) {
	for _, mode := range []string{"missing-run", "missing-birth", "synthetic-birth", "unsupported-binary"} {
		t.Run(mode, func(t *testing.T) {
			store, fixture, id := p1ObserveNoCoreFixture(t, mode)
			before := p1ProductReadTree(t, fixture)
			capture := t.TempDir()
			must(t, os.Chmod(capture, 0700))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := p1ObserveRun(ctx, store, id, capture)
			if !errors.Is(err, errP1RunObservation) || !reflect.DeepEqual(got, p1RunObservation{}) {
				t.Fatal("no-core failure became observation or known absence")
			}
			entries, err := os.ReadDir(capture)
			must(t, err)
			if len(entries) != 0 {
				t.Fatal("failure created a success receipt")
			}
			if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
				t.Fatal("fresh read failure changed durable Store")
			}
		})
	}
}
func TestP1ProductObserveCanceledAndNilContext(t *testing.T) {
	store, fixture, id := p1ObserveNoCoreFixture(t, "missing-run")
	capture := t.TempDir()
	must(t, os.Chmod(capture, 0700))
	before := p1ProductReadTree(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, candidate := range []context.Context{nil, ctx} {
		got, err := p1ObserveRun(candidate, store, id, capture)
		if err == nil || !reflect.DeepEqual(got, p1RunObservation{}) {
			t.Fatal("invalid context returned proof")
		}
		if candidate != nil && !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation cause lost")
		}
	}
	if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
		t.Fatal("canceled observation changed Store")
	}
}
func TestP1ProductObserveCapturePathConstraints(t *testing.T) {
	root := t.TempDir()
	must(t, os.Chmod(root, 0700))
	private := filepath.Join(root, "private")
	must(t, os.Mkdir(private, 0700))
	finalLink := filepath.Join(root, "final-link")
	must(t, os.Symlink(private, finalLink))
	parentLink := filepath.Join(root, "parent-link")
	must(t, os.Symlink(root, parentLink))
	public := filepath.Join(root, "public")
	must(t, os.Mkdir(public, 0755))
	regular := filepath.Join(root, "regular")
	must(t, os.WriteFile(regular, nil, 0600))
	for _, path := range []string{"relative", finalLink, filepath.Join(parentLink, "private"), public, regular, private + "/../private", filepath.Join(root, "missing")} {
		file, err := p1OpenObserveCapture(path)
		if file != nil {
			file.Close()
			t.Fatal("unsafe capture returned directory handle")
		}
		if err == nil {
			t.Fatal("unsafe capture accepted")
		}
	}
	directory, err := p1OpenObserveCapture(private)
	must(t, err)
	must(t, directory.Close()) // Filesystem anchor only, no core proof.
}
