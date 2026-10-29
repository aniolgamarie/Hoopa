//go:build linux && amd64 && p1integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	"hoopa/internal/platform"
	wire "hoopa/protocol/corecontrol"
)

var errP1ProductTraffic = errors.New("P1_PRODUCT_TRAFFIC_UNKNOWN")

type p1TrafficBinding struct {
	tunnel  *p1HeldTunnel
	run     config.RunID
	native  platform.ProcessIdentity
	request trafficRequest
	proof   originalRef
}
type p1ProductTraffic struct {
	mu          sync.Mutex
	store       *config.Store
	networks    map[string]*p1FixtureNetwork
	artifactDir string
	directory   *os.File
	held        map[string]*p1TrafficBinding
	used        map[string]bool
	closed      bool
}

func (*p1ProductTraffic) String() string   { return "p1ProductTraffic{private}" }
func (*p1ProductTraffic) GoString() string { return "p1ProductTraffic{private}" }
func (p1TrafficBinding) String() string    { return "p1TrafficBinding{private}" }
func (p1TrafficBinding) GoString() string  { return "p1TrafficBinding{private}" }
func newP1ProductTraffic(store *config.Store, networks map[string]*p1FixtureNetwork, artifactDir string) (*p1ProductTraffic, error) {
	if store == nil || len(networks) == 0 {
		return nil, errP1ProductTraffic
	}
	directory, err := p1OpenObserveCapture(artifactDir)
	if err != nil {
		return nil, errP1ProductTraffic
	}
	copied := map[string]*p1FixtureNetwork{}
	for key, network := range networks {
		if network == nil || key != network.key || len(network.entries) != 2 {
			directory.Close()
			return nil, errP1ProductTraffic
		}
		copied[key] = network
	}
	return &p1ProductTraffic{store: store, networks: copied, artifactDir: artifactDir, directory: directory, held: map[string]*p1TrafficBinding{}, used: map[string]bool{}}, nil
}
func p1TrafficUnknown(id string) trafficObservation {
	return trafficObservation{ConnectionID: id, Forwarded: observed[bool]{State: "unknown", Reason: "traffic_unverified"}, Upload: observed[uint64]{State: "unknown", Reason: "traffic_unverified"}, Download: observed[uint64]{State: "unknown", Reason: "traffic_unverified"}, CoreRunID: observed[config.RunID]{State: "unknown", Reason: "traffic_unverified"}, ActualNode: observed[string]{State: "unknown", Reason: "traffic_unverified"}}
}
func (p *p1ProductTraffic) original(prefix string, value any) (originalRef, error) {
	token := make([]byte, 24)
	if _, err := rand.Read(token); err != nil {
		return originalRef{}, errP1ProductTraffic
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return originalRef{}, errP1ProductTraffic
	}
	raw = append(raw, '\n')
	name := prefix + "-" + hex.EncodeToString(token) + ".json"
	fd, err := syscall.Openat(int(p.directory.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return originalRef{}, errP1ProductTraffic
	}
	file := os.NewFile(uintptr(fd), "private-traffic-original")
	n, err := file.Write(raw)
	if n != len(raw) {
		err = errors.Join(err, io.ErrShortWrite)
	}
	err = errors.Join(err, file.Sync(), file.Close())
	if err != nil || p.directory.Sync() != nil {
		return originalRef{}, errP1ProductTraffic
	}
	return originalRef{Path: filepath.Join(p.artifactDir, name), Bytes: int64(len(raw)), SHA256: p1SHA(raw)}, nil
}

// Context cancellation and SetDeadline share a lock, so a canceled call cannot
// overwrite the cancellation deadline while shared tunnel.exchange sets it.
type p1TrafficDeadlineConn struct {
	net.Conn
	mu  *sync.Mutex
	ctx context.Context
}

func (c *p1TrafficDeadlineConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		deadline = time.Now()
	}
	return c.Conn.SetDeadline(deadline)
}
func p1TrafficIO(ctx context.Context, connection net.Conn, action func(net.Conn) error) error {
	var mu sync.Mutex
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { mu.Lock(); _ = connection.SetDeadline(time.Now()); mu.Unlock(); close(done) })
	err := action(&p1TrafficDeadlineConn{connection, &mu, ctx})
	if !stop() {
		<-done
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (p *p1ProductTraffic) open(ctx context.Context, request trafficRequest, runID config.RunID, handle *platform.ProcessHandle, network *p1FixtureNetwork) (*p1TrafficBinding, error) {
	if handle == nil {
		return nil, errP1ProductTraffic
	}
	intent, intentSHA, err := p.store.ReadRunIntent(runID)
	if err != nil {
		return nil, errP1ProductTraffic
	}
	birth, birthSHA, err := p.store.ReadRunBirth(runID)
	if err != nil || birth.Match(intent, intentSHA) != nil {
		return nil, errP1ProductTraffic
	}
	native := handle.Identity()
	if native.PID != birth.PID || native.UID != birth.UID || native.BootID != birth.BootID || native.StartTicks != birth.StartTicks || native.BinaryPath != intent.Binary.Path || native.BinarySHA256 != intent.Binary.SHA256 || native.NonceSHA256 != intent.NonceSHA256 || native.Workdir != intent.Workdir {
		return nil, errP1ProductTraffic
	}
	capability, err := mihomo.ApprovedBasicCapability(intent.Binary.Path)
	if err != nil || capability.Binary != intent.Binary {
		return nil, errP1ProductTraffic
	}
	bundle, err := p.store.ReadBundle(intent.Generation)
	if err != nil || bundle.ManifestSHA256 != intent.ManifestSHA256 || intent.Generation != config.Generation("fixture-"+network.key) {
		return nil, errP1ProductTraffic
	}
	snapshot, err := wire.DecodeSnapshot(bundle.Files[mihomo.SnapshotFile])
	if err != nil || snapshot.ConfigSHA256 != intent.ConfigSHA256 || snapshot.Controller != intent.Controller {
		return nil, errP1ProductTraffic
	}
	entry, ok := network.entries[request.Entry]
	if !ok {
		return nil, errP1ProductTraffic
	}
	matched := false
	for _, expected := range snapshot.Entries {
		if expected.Name == request.Entry && expected.Address == entry.address {
			matched = true
		}
	}
	if !matched || handle.VerifyArguments(ctx, intent.Arguments) != nil {
		return nil, errP1ProductTraffic
	}
	connection, err := handle.DialOwnedTCP(ctx, entry.address)
	if err != nil {
		return nil, errP1ProductTraffic
	}
	accepted := false
	defer func() {
		if !accepted {
			connection.Close()
		}
	}()
	again, againSHA, err := p.store.ReadRunIntent(runID)
	if err != nil || againSHA != intentSHA || !reflect.DeepEqual(again, intent) {
		return nil, errP1ProductTraffic
	}
	againBirth, againBirthSHA, err := p.store.ReadRunBirth(runID)
	if err != nil || againBirthSHA != birthSHA || againBirth != birth {
		return nil, errP1ProductTraffic
	}
	reader := bufio.NewReader(connection)
	err = p1TrafficIO(ctx, connection, func(guarded net.Conn) error {
		deadline := time.Now().Add(5 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if guarded.SetDeadline(deadline) != nil {
			return errP1ProductTraffic
		}
		header := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", network.target, network.target, entry.authorization)
		if _, err := io.WriteString(guarded, header); err != nil {
			return errP1ProductTraffic
		}
		var response bytes.Buffer
		for response.Len() <= 4096 {
			part, err := reader.ReadSlice('\n')
			if err != nil || response.Len()+len(part) > 4096 {
				return errP1ProductTraffic
			}
			response.Write(part)
			if bytes.Equal(part, []byte("\r\n")) {
				break
			}
		}
		parsed, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(response.Bytes())), nil)
		if err != nil || parsed.StatusCode != http.StatusOK {
			return errP1ProductTraffic
		}
		return nil
	})
	if err != nil {
		return nil, errP1ProductTraffic
	}
	proof, err := p.original("traffic-connection", map[string]any{"version": 1, "kind": "generated-entry-connection-owner-proof", "provenance": "original-pidfd-revalidated-DialOwnedTCP-exact-connection-then-CONNECT-200", "observed_at": time.Now().UTC(), "connection_id": request.ConnectionID, "bundle": network.key, "entry": request.Entry, "entry_address": entry.address, "target": network.target, "local_address": connection.LocalAddr().String(), "remote_address": connection.RemoteAddr().String(), "run_id": intent.RunID, "generation": intent.Generation, "identity": native, "intent_sha256": intentSHA, "birth_sha256": birthSHA})
	if err != nil || ctx.Err() != nil {
		return nil, errP1ProductTraffic
	}
	accepted = true
	return &p1TrafficBinding{tunnel: &p1HeldTunnel{connection, reader, network}, run: intent.RunID, native: native, request: request, proof: proof}, nil
}

// Exchange reports this payload only (excludes CONNECT and four framing bytes).
// CoreRunID comes from the proved original entry connection, never active or
// the caller's selector. Held calls reuse that same conn/proof without redial.
func (p *p1ProductTraffic) Exchange(ctx context.Context, request trafficRequest, runID config.RunID, handle *platform.ProcessHandle) (trafficObservation, error) {
	fail := func() (trafficObservation, error) {
		if ctx != nil && ctx.Err() != nil {
			return p1TrafficUnknown(request.ConnectionID), errors.Join(errP1ProductTraffic, ctx.Err())
		}
		return p1TrafficUnknown(request.ConnectionID), errP1ProductTraffic
	}
	if p == nil || ctx == nil {
		return fail()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || ctx.Err() != nil || request.Bytes < 32 || request.Bytes > 65536 {
		return fail()
	}
	if _, err := config.ParseID(request.ConnectionID); err != nil {
		return fail()
	}
	network, ok := p.networks[request.BundleID]
	if !ok {
		return fail()
	}
	domain, _, err := net.SplitHostPort(network.target)
	if err != nil || request.TargetDomain != domain {
		return fail()
	}
	if _, ok = network.entries[request.Entry]; !ok {
		return fail()
	}
	var binding *p1TrafficBinding
	switch request.Kind {
	case "continue-held":
		binding = p.held[request.ConnectionID]
		if binding == nil || binding.request.Entry != request.Entry || binding.request.BundleID != request.BundleID || binding.request.TargetDomain != request.TargetDomain || runID != binding.run || handle != nil && handle.Identity() != binding.native {
			return fail()
		}
	case "open-held", "new":
		if p.used[request.ConnectionID] || len(p.used) >= 1024 || request.Kind == "open-held" && len(p.held) >= 32 {
			return fail()
		}
		p.used[request.ConnectionID] = true // No identity reuse after an attempted dial.
		binding, err = p.open(ctx, request, runID, handle, network)
		if err != nil {
			return fail()
		}
	default:
		return fail()
	}
	keep := false
	defer func() {
		if !keep {
			_ = binding.tunnel.Close()
			delete(p.held, request.ConnectionID)
		}
	}()
	var receipt p1NodeReceipt
	err = p1TrafficIO(ctx, binding.tunnel.conn, func(guarded net.Conn) error {
		tunnel := *binding.tunnel
		tunnel.conn = guarded
		var e error
		receipt, e = tunnel.exchange(ctx, int(request.Bytes))
		return e
	})
	if err != nil || receipt.Bundle != network.key || receipt.Target != network.target || receipt.Node == "" || receipt.PayloadBytes != int(request.Bytes) || receipt.FramedBytes != receipt.PayloadBytes+4 || receipt.PayloadSHA256 != receipt.EchoSHA256 {
		return fail()
	}
	raw, err := p1HistoryOriginal(receipt.Original, 4096)
	if err != nil {
		return fail()
	}
	var witness p1NodeReceipt
	if json.Unmarshal(raw, &witness) != nil || witness.Bundle != receipt.Bundle || witness.Node != receipt.Node || witness.Target != receipt.Target || witness.PayloadSHA256 != receipt.PayloadSHA256 || witness.EchoSHA256 != receipt.EchoSHA256 || witness.PayloadBytes != receipt.PayloadBytes || witness.FramedBytes != receipt.FramedBytes {
		return fail()
	}
	upload, download := uint64(receipt.PayloadBytes), uint64(receipt.FramedBytes-4)
	exchange, err := p.original("traffic-exchange", map[string]any{"version": 1, "kind": "generated-test-payload-echo-observation", "provenance": "same-original-proved-entry-connection-and-independent-node-witness", "connection_id": request.ConnectionID, "request_kind": request.Kind, "bundle": network.key, "entry": request.Entry, "target_domain": request.TargetDomain, "run_id": binding.run, "connection_proof": binding.proof, "node_witness": receipt.Original, "actual_node": receipt.Node, "payload_sha256": receipt.PayloadSHA256, "echo_sha256": receipt.EchoSHA256, "payload_upload_bytes": upload, "payload_download_bytes": download, "framing_bytes_excluded": 4, "connect_bytes_excluded": true})
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	if request.Kind != "new" {
		p.held[request.ConnectionID] = binding
		keep = true
	}
	refs := []originalRef{binding.proof, receipt.Original, exchange}
	forwarded := true
	provedRun := binding.run
	node := receipt.Node
	return trafficObservation{ConnectionID: request.ConnectionID, Forwarded: observed[bool]{State: "observed", Value: &forwarded, Originals: append([]originalRef(nil), refs...)}, Upload: observed[uint64]{State: "observed", Value: &upload, Originals: append([]originalRef(nil), refs...)}, Download: observed[uint64]{State: "observed", Value: &download, Originals: append([]originalRef(nil), refs...)}, CoreRunID: observed[config.RunID]{State: "observed", Value: &provedRun, Originals: append([]originalRef(nil), refs...)}, ActualNode: observed[string]{State: "observed", Value: &node, Originals: append([]originalRef(nil), refs...)}}, nil
}
func (p *p1ProductTraffic) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	for _, binding := range p.held {
		errs = append(errs, binding.tunnel.Close())
	}
	p.held = map[string]*p1TrafficBinding{}
	errs = append(errs, p.directory.Close())
	return errors.Join(errs...)
}

func p1AssertTrafficUnknown(t *testing.T, got trafficObservation, err error) {
	t.Helper()
	if err == nil || got.Forwarded.Value != nil || got.Upload.Value != nil || got.Download.Value != nil || got.CoreRunID.Value != nil || got.ActualNode.Value != nil || got.Forwarded.State != "unknown" || got.Upload.State != "unknown" || got.Download.State != "unknown" {
		t.Fatal("failed traffic fabricated a successful value or zero")
	}
}

// These metadata-only negative fixtures listen nowhere and are not a simulated
// core/product harness or successful ownership/forwarding acceptance.
func TestP1ProductTrafficInvalidParameters(t *testing.T) {
	store, fixture, _ := p1ProductReadFixture(t)
	network := &p1FixtureNetwork{key: "app-first", target: "alpha.test:23515", entries: map[string]p1NetworkEntry{"entry-a": {address: "127.0.0.1:1"}, "entry-b": {address: "127.0.0.1:2"}}}
	dir := t.TempDir()
	must(t, os.Chmod(dir, 0700))
	traffic, err := newP1ProductTraffic(store, map[string]*p1FixtureNetwork{"app-first": network}, dir)
	must(t, err)
	t.Cleanup(func() { must(t, traffic.Close()) })
	before := p1ProductReadTree(t, fixture)
	base := trafficRequest{Kind: "new", ConnectionID: "invalid-driver", Entry: "entry-a", TargetDomain: "alpha.test", BundleID: "app-first", Bytes: 4096}
	for _, change := range []string{"kind", "id", "entry", "domain", "bundle", "bytes", "missing-held", "nil-handle"} {
		t.Run(change, func(t *testing.T) {
			req := base
			switch change {
			case "kind":
				req.Kind = "unknown"
			case "id":
				req.ConnectionID = "../escape"
			case "entry":
				req.Entry = "unknown"
			case "domain":
				req.TargetDomain = "other.test"
			case "bundle":
				req.BundleID = "unknown"
			case "bytes":
				req.Bytes = 0
			case "missing-held":
				req.Kind = "continue-held"
			}
			got, err := traffic.Exchange(context.Background(), req, "missing-run", nil)
			p1AssertTrafficUnknown(t, got, err)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := traffic.Exchange(ctx, base, "missing-run", nil)
	p1AssertTrafficUnknown(t, got, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("traffic cancellation cause lost")
	}
	if !reflect.DeepEqual(before, p1ProductReadTree(t, fixture)) {
		t.Fatal("invalid traffic changed Store")
	}
	must(t, traffic.Close())
	got, err = traffic.Exchange(context.Background(), base, "missing-run", nil)
	p1AssertTrafficUnknown(t, got, err)
}

// Actual one-core/two-entry scene only; manager faults remain factory work.
func TestP1ProductTrafficCore(t *testing.T) {
	var prior int32
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 37, uintptr(unsafe.Pointer(&prior)), 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	_, _, errno = syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	t.Cleanup(func() {
		_, _, e := syscall.Syscall6(syscall.SYS_PRCTL, 36, uintptr(prior), 0, 0, 0, 0)
		if e != 0 {
			t.Error(e)
		}
	})
	var f *p1TracedCore
	t.Cleanup(func() {
		if f == nil {
			return
		}
		raw, err := os.ReadFile(filepath.Join(f.artifacts, "cleanup.json"))
		if err != nil {
			t.Error(err)
			return
		}
		var receipts []struct {
			Supplemental bool `json:"cleanup_signal_needed"`
			Exited       bool `json:"pidfd_exited"`
			Absent       bool `json:"proc_absent"`
		}
		if json.Unmarshal(raw, &receipts) != nil || len(receipts) != 1 {
			t.Error("missing actual original cleanup receipt")
			return
		}
		if receipts[0].Supplemental || !receipts[0].Exited || !receipts[0].Absent {
			t.Error("traffic scene needed supplemental core cleanup signal")
		}
	})
	f = newP1TracedCore(t, "traffic-core")
	seed, _ := f.apply("app-first", "traffic-seed")
	run, handle := f.pin()
	dir := filepath.Join(f.artifacts, "traffic")
	must(t, os.Mkdir(dir, 0700))
	traffic, err := newP1ProductTraffic(f.store, f.networks, dir)
	must(t, err)
	t.Cleanup(func() { must(t, traffic.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	before := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "before-traffic")
	request := trafficRequest{Kind: "open-held", ConnectionID: "held-core", Entry: "entry-a", TargetDomain: "alpha.test", BundleID: "app-first", Bytes: 4096}
	first, err := traffic.Exchange(ctx, request, run, handle)
	must(t, err)
	p1CheckActualTraffic(t, f, first, request, run, handle)
	held := traffic.held[request.ConnectionID].tunnel.conn
	for _, change := range []string{"duplicate-id", "entry", "bundle", "domain", "run", "missing-id"} {
		changed := request
		changed.Kind = "continue-held"
		requestedRun := run
		switch change {
		case "duplicate-id":
			changed.Kind = "open-held"
		case "entry":
			changed.Entry = "entry-b"
		case "bundle":
			changed.BundleID = "domain-first"
		case "domain":
			changed.TargetDomain = "other.test"
		case "run":
			requestedRun = "not-the-original"
		case "missing-id":
			changed.ConnectionID = "never-opened"
		}
		unknown, e := traffic.Exchange(ctx, changed, requestedRun, handle)
		p1AssertTrafficUnknown(t, unknown, e)
		if traffic.held[request.ConnectionID].tunnel.conn != held {
			t.Fatal("invalid parameters replaced held connection")
		}
	}
	continuation := request
	continuation.Kind = "continue-held"
	continuation.Bytes = 2048
	second, err := traffic.Exchange(ctx, continuation, run, nil)
	must(t, err)
	p1CheckActualTraffic(t, f, second, continuation, run, handle)
	if traffic.held[request.ConnectionID].tunnel.conn != held || first.CoreRunID.Originals[0] != second.CoreRunID.Originals[0] {
		t.Fatal("held continuation redialled/replaced its original proof")
	}
	freshRequest := request
	freshRequest.Kind = "new"
	freshRequest.ConnectionID = "new-core"
	freshRequest.Entry = "entry-b"
	third, err := traffic.Exchange(ctx, freshRequest, run, handle)
	must(t, err)
	p1CheckActualTraffic(t, f, third, freshRequest, run, handle)
	if third.CoreRunID.Originals[0] == first.CoreRunID.Originals[0] {
		t.Fatal("new entry reused another connection proof")
	}
	after := p1ArchiveTree(t, f.cli.Paths(), f.artifacts, "after-traffic")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("traffic adapter wrote product durable state")
	}
	f.networks["app-first"].mu.Lock()
	witnesses := len(f.networks["app-first"].receipts)
	f.networks["app-first"].mu.Unlock()
	if witnesses != 3 {
		t.Fatal("invalid/duplicate requests emitted extra payloads")
	}
	must(t, traffic.Close())
	live, lc := context.WithTimeout(ctx, 30*time.Millisecond)
	liveErr := handle.WaitExited(live)
	lc()
	if !errors.Is(liveErr, context.DeadlineExceeded) {
		t.Fatal("fixture close did not preserve independently observed live original")
	}
	manager := f.service("manager", nil)
	f.call("traffic-stop", "runtime.stop", map[string]any{"expected_revision": f.active().Revision, "accept_disruption": true})
	must(t, handle.WaitExited(ctx))
	f.call("traffic-service-stop", "service.stop", map[string]any{"expected_revision": f.active().Revision})
	must(t, manager.Wait(ctx))
	f.archiveTrace("seed", seed)
	f.archiveTrace("manager", manager)
	_, err = p1WriteOriginal(filepath.Join(f.artifacts, "result.json"), p1JSON(t, map[string]any{"run_id": run, "identity": handle.Identity(), "observations": []trafficObservation{first, second, third}, "node_witness_count": witnesses, "held_same_connection": true, "per_payload_bytes": []uint64{4096, 2048, 4096}, "state_unchanged_by_traffic": true, "core_not_stopped_by_fixture_close": true}))
	must(t, err)
}
func p1CheckActualTraffic(t *testing.T, f *p1TracedCore, got trafficObservation, request trafficRequest, run config.RunID, handle *platform.ProcessHandle) {
	t.Helper()
	if got.ConnectionID != request.ConnectionID || got.Forwarded.Value == nil || !*got.Forwarded.Value || got.Upload.Value == nil || *got.Upload.Value != request.Bytes || got.Download.Value == nil || *got.Download.Value != request.Bytes || got.CoreRunID.Value == nil || *got.CoreRunID.Value != run || got.ActualNode.Value == nil || *got.ActualNode.Value != fixtureExpectedNode(t, request.BundleID, request.Entry) {
		t.Fatal("real traffic/proven run/payload/node mismatch")
	}
	for _, ref := range got.CoreRunID.Originals {
		verifyOriginal(t, ref)
	}
	raw, err := os.ReadFile(got.CoreRunID.Originals[0].Path)
	must(t, err)
	var proof struct {
		Kind       string                   `json:"kind"`
		Provenance string                   `json:"provenance"`
		Run        config.RunID             `json:"run_id"`
		Entry      string                   `json:"entry"`
		Local      string                   `json:"local_address"`
		Remote     string                   `json:"remote_address"`
		Identity   platform.ProcessIdentity `json:"identity"`
	}
	must(t, json.Unmarshal(raw, &proof))
	if proof.Kind != "generated-entry-connection-owner-proof" || proof.Provenance == "" || proof.Run != run || proof.Entry != request.Entry || proof.Identity != handle.Identity() || proof.Local == "" || proof.Remote != f.networks[request.BundleID].entries[request.Entry].address {
		t.Fatal("connection proof does not bind actual entry/run")
	}
}
