//go:build p1integration

package integration

// These are test-only adapter seams, not a product API or a P0 fake backend.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"hoopa/internal/config"
)

type originalRef struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Unobserved false/zero is never a fact. Unknown/unavailable has nil Value and a reason.
type observed[T any] struct {
	State     string
	Value     *T
	Reason    string
	Originals []originalRef
}

type processIdentity struct {
	BootID, OSStartIdentity, BinaryPath, BinarySHA256, LaunchNonceSHA256, PrivateWorkdir string
	PID                                                                                  int
	Generation                                                                           config.Generation
	RunID                                                                                config.RunID
}

type bundleSnapshot struct {
	Generation                   config.Generation
	ManifestSHA256, ConfigSHA256 string
	AssetSHA256                  map[string]string // complete provider/rule/binary/capability inventory
	Selections                   map[string]string
	OrderedRules                 []string
	NodeByEntry                  map[string]string // immutable per-entry routing projection
	EntryAuthSHA256              map[string]string // hashes only; no credential material
	Entries                      []string
}

type fileSnapshot struct {
	Kind       string
	Mode       uint32
	Bytes      int64
	SHA256     string
	ModifiedNS int64
}
type launchInvocation struct {
	BoundaryIndex   uint64
	ID, NonceSHA256 string
	Instance        processIdentity
}
type signalInvocation struct {
	BoundaryIndex uint64
	Signal        string
	Target        processIdentity
}
type operationRecord struct {
	ID, RequestID, RequestDigest, Phase, Desired string
	BaseRevision                                 config.Revision
	TargetRevision                               *config.Revision
	Operation, Subtype                           string
	Original                                     originalRef
	recordSource                                 []byte
	Previous, Target                             bundleSnapshot
}

// String keeps retained request/response bytes out of assertion diagnostics.
func (operationRecord) String() string   { return "operationRecord(redacted)" }
func (operationRecord) GoString() string { return "operationRecord(redacted)" }

type boundaryEvent struct {
	Index                                           uint64
	Name, OperationID, NonceSHA256                  string
	TargetOperationID, OriginOperationID, RequestID string
	RequestDigest                                   string // actual enclosing request-begin digest, not admission
	Instance                                        *processIdentity
	Originals                                       []originalRef // registration records fresh process/owner validation for this operation, never copied registry text
}
type faultHit struct {
	Point, Artifact, Phase, Cut, Failure string
	Original                             originalRef
}
type sessionRecord struct {
	Kind         string
	Began, Ended originalRef
}

// operationVersion is an independently captured historical file, never rebuilt from steps.
type operationVersion struct {
	Original         originalRef
	Raw              []byte
	Previous, Target bundleSnapshot
}

func (operationVersion) String() string   { return "operationVersion(redacted)" }
func (operationVersion) GoString() string { return "operationVersion(redacted)" }

// Fixture seeds retain storage provenance and never authorize runtime effects.
type p1CapacitySeed struct {
	OperationID, RequestID, RequestDigest          string
	RecordOriginal                                 originalRef
	BeginOriginals, PublishOriginals, EndOriginals []originalRef
}

type productEvidence struct {
	FixtureSeeds     map[string]p1CapacitySeed
	OperationHistory []operationVersion
	DurableFiles     map[string]fileSnapshot // complete state/config; excludes test capture dir/runtime socket
	Launches         []launchInvocation      // complete actual product core-launch invocations, not fixture helpers
	Signals          []signalInvocation
	Operations       map[string]operationRecord
	Boundaries       []boundaryEvent
	FaultHits        []faultHit
	Sessions         []sessionRecord
	ForeignProcesses []processIdentity // owned test-fixture processes, never arbitrary host PIDs
}

type productObservation struct {
	Revision              observed[config.Revision]
	Generation            observed[config.Generation] // actual runtime generation when present; never inferred from active pointer
	Desired               observed[string]
	Degraded              observed[bool]   // actual public service status, distinct from Core.State
	Runtime               observed[string] // verified-running / verified-stopped / unknown
	CorePresent           observed[bool]
	Epoch                 observed[config.CounterEpoch]
	Core                  observed[processIdentity]
	ControllerOwnerProof  observed[string] // digest of fresh connected-owner proof
	ManagerAvailable      observed[bool]
	EntryStates           observed[map[string]string]
	ActiveBundle          observed[bundleSnapshot] // committed pointer projection; may differ from a verified publishing target
	PendingOperation      observed[string]
	PendingDesired        observed[string]
	Bytes                 observed[uint64]   // unavailable remains nil, not fabricated zero
	UnavailableComponents observed[[]string] // isolated fixed build/launch fixture evidence, not public or host-wide inventory
}

type requestIdentity struct {
	Version, Name, ID string
	BaseRevision      config.Revision
	AcceptDisruption  bool
}
type startRequest struct {
	Identity              requestIdentity
	Fixture               string
	BundleID              string
	AllowRestart          bool
	RestartLimit          int
	UnavailableComponents []string
}

// Independently verified complete basic inputs: T028 can plan/hash/validate
// these without the later US3 compiler. Historical full-identity P0 inputs are
// retained separately; basic P1 cannot grant their source-process capabilities.
type applyRequest struct {
	Identity  requestIdentity
	Fixture   string
	Candidate string // descriptor bundle key; never a request to synthesize rules
}
type recoverRequest struct {
	Identity             requestIdentity
	InterruptedRequestID string
}

// Error means transport/process interruption; completed application rejections use Outcome.
type operationResult struct {
	RequestID string // independently retained invocation ID; never copied from the response
	// Outcome is derived from the retained operation(s), not an invented wire
	// value. A pending/degraded recovery call takes priority over its old target.
	Outcome             string // pending / committed / rolled_back / degraded; empty for unadmitted rejections
	WireOutcome         string // actual response data.outcome, empty when data is null
	ErrorCode           string // actual response error.code, empty on success
	Operation           operationRecord
	RecoveryCall        *operationRecord
	TargetHistory       []operationVersion
	ResponseOperationID string
	Response            []byte
	ResponseOriginal    originalRef
	Originals           []originalRef
}

func (operationResult) String() string   { return "operationResult(redacted)" }
func (operationResult) GoString() string { return "operationResult(redacted)" }

type faultRequest struct {
	Point, Artifact, Phase, Cut, Failure string
	Limit                                int
	Target                               *processIdentity
}
type trafficRequest struct {
	Kind, ConnectionID, Entry, TargetDomain, BundleID string
	Bytes                                             uint64
}
type trafficObservation struct {
	ConnectionID     string
	Forwarded        observed[bool]
	Upload, Download observed[uint64]
	CoreRunID        observed[config.RunID]
	ActualNode       observed[string]
}

type productHarness interface {
	Start(context.Context, startRequest) (operationResult, error)
	Stop(context.Context, requestIdentity) (operationResult, error)
	ServiceStop(context.Context, requestIdentity) (operationResult, error)
	Apply(context.Context, applyRequest) (operationResult, error)
	Recover(context.Context, recoverRequest) (operationResult, error)
	Observe(context.Context) (productObservation, error)
	RestartManager(context.Context, string) error // managed or control-only; no implied Recover
	ExitFrontend(context.Context, string) error   // actual minimal product frontend normal/abnormal exit
	KillManager(context.Context) error
	BeginSession(context.Context, string) error // prepare the actual session before Start launches its core
	SessionEvent(context.Context, string) error // one serial actual event invocation appends exactly one begin/end record; never a signal stand-in
	KillCore(context.Context, processIdentity) error
	InjectFault(context.Context, faultRequest) error // owned fixture/store seams only
	Traffic(context.Context, trafficRequest) (trafficObservation, error)
	Evidence(context.Context) (observed[productEvidence], error)
}

func newProductHarness(t *testing.T) productHarness {
	t.Helper()
	return newP1ProductHarness(t)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func verifyOriginal(t *testing.T, r originalRef) {
	t.Helper()
	path := r.Path
	if !filepath.IsAbs(path) {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("test source location")
		}
		path = filepath.Join(filepath.Dir(file), "../../", path)
	}
	info, err := os.Lstat(path)
	must(t, err)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("original is not regular: %s", r.Path)
	}
	raw, err := os.ReadFile(path)
	must(t, err)
	sum := sha256.Sum256(raw)
	if int64(len(raw)) != r.Bytes || hex.EncodeToString(sum[:]) != r.SHA256 {
		t.Fatalf("original bytes/hash changed: %s", r.Path)
	}
}
func need[T any](t *testing.T, v observed[T], label string) T {
	t.Helper()
	if v.State != "observed" || v.Value == nil || v.Reason != "" || len(v.Originals) == 0 {
		t.Fatalf("%s requires actual observation, got %+v", label, v)
	}
	for _, r := range v.Originals {
		verifyOriginal(t, r)
	}
	return *v.Value
}
func unavailable[T any](t *testing.T, v observed[T]) {
	t.Helper()
	if (v.State != "unknown" && v.State != "unavailable") || v.Value != nil || v.Reason == "" {
		t.Fatalf("unknown/unavailable cannot be zero/false: %+v", v)
	}
}
func observe(t *testing.T, h productHarness, ctx context.Context) productObservation {
	t.Helper()
	o, err := h.Observe(ctx)
	must(t, err)
	return o
}
func evidence(t *testing.T, h productHarness, ctx context.Context) productEvidence {
	t.Helper()
	v, err := h.Evidence(ctx)
	must(t, err)
	return need(t, v, "complete product evidence")
}
func request(name, id string, base config.Revision) requestIdentity {
	return requestIdentity{Version: "1", Name: name, ID: id, BaseRevision: base, AcceptDisruption: true}
}

const verifiedFixture = "testdata/runtime/basic-verified-bundle.json"

const baselineBundle = "app-first"
const targetBundle = "domain-first"

type fixedBundleDescriptor struct {
	ConfigSHA256  string            `json:"decoded_config_sha256"`
	Entries       []string          `json:"application_entries"`
	ExpectedNodes map[string]string `json:"expected_node_by_entry"`
}

func fixedBundle(t *testing.T, bundleID string) fixedBundleDescriptor {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source location")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../", verifiedFixture))
	must(t, err)
	var descriptor struct {
		Bundles map[string]fixedBundleDescriptor `json:"bundles"`
	}
	must(t, json.Unmarshal(raw, &descriptor))
	b, ok := descriptor.Bundles[bundleID]
	if !ok || len(b.ConfigSHA256) != 64 || len(b.Entries) != 2 || len(b.ExpectedNodes) != 2 {
		t.Fatal("missing immutable fixture binding")
	}
	return b
}
func fixtureExpectedNode(t *testing.T, bundleID, entry string) string {
	t.Helper()
	node := fixedBundle(t, bundleID).ExpectedNodes[entry]
	if len(node) != len("node:")+64 {
		t.Fatal("missing exact fixture per-entry node binding")
	}
	return node
}
func assertFixedBundle(t *testing.T, b bundleSnapshot, bundleID string) {
	t.Helper()
	fixed := fixedBundle(t, bundleID)
	if b.ConfigSHA256 != fixed.ConfigSHA256 || !reflect.DeepEqual(b.Entries, fixed.Entries) || !reflect.DeepEqual(b.NodeByEntry, fixed.ExpectedNodes) {
		t.Fatal("operation bundle differs from complete accepted fixture")
	}
}
func assertFixedTarget(t *testing.T, b bundleSnapshot) {
	t.Helper()
	assertFixedBundle(t, b, targetBundle)
}
func assertDurableResult(t *testing.T, e productEvidence, op operationRecord) {
	t.Helper()
	stored, ok := e.Operations[op.ID]
	if !ok || !reflect.DeepEqual(stored, op) {
		t.Fatal("response operation is not the actual durable record")
	}
}

func freshRecoveryIdentity(t *testing.T, h productHarness, ctx context.Context, id string) requestIdentity {
	t.Helper()
	return request("operation.recover", id, need(t, observe(t, h, ctx).Revision, "current revision after manager restart"))
}
func assertCommittedIntentUnchanged(t *testing.T, before, after productObservation) {
	t.Helper()
	if need(t, after.Revision, "current revision") != need(t, before.Revision, "original revision") || need(t, after.Desired, "committed desired") != "running" {
		t.Fatal("automatic restart or exhaustion changed configuration revision/running intent")
	}
	assertBundle(t, need(t, after.ActiveBundle, "committed bundle"), need(t, before.ActiveBundle, "original committed bundle"))
}
func assertStopCommit(t *testing.T, before, after productObservation, op operationRecord) {
	t.Helper()
	assertStopped(t, after)
	if op.Phase != "committed" || op.Desired != "stopped" {
		t.Fatal("explicit stop lacks committed stopped operation")
	}
	if need(t, after.Revision, "stop revision") == need(t, before.Revision, "old revision") || need(t, after.Revision, "stop revision") != requiredTargetRevision(t, op) {
		t.Fatal("explicit stop must commit its new revision")
	}
	assertBundle(t, need(t, after.ActiveBundle, "stopped configured bundle"), need(t, before.ActiveBundle, "original configured bundle"))
	// No core means runtime Generation may be unavailable. The committed bundle keeps the old generation.
	if after.Generation.State == "observed" {
		if need(t, after.Generation, "last verified generation") != need(t, before.Generation, "original runtime generation") {
			t.Fatal("stop replaced the configured generation")
		}
	} else {
		unavailable(t, after.Generation)
	}
}

func fixtureCoreSHA(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source location")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../", verifiedFixture))
	must(t, err)
	var value struct {
		CoreSHA256        string `json:"core_sha256"`
		CredentialsCopied bool   `json:"credentials_copied"`
		BinariesCopied    bool   `json:"binaries_copied"`
	}
	must(t, json.Unmarshal(raw, &value))
	if len(value.CoreSHA256) != 64 || value.CredentialsCopied || value.BinariesCopied {
		t.Fatal("reference-only verified fixture required")
	}
	return value.CoreSHA256
}
func strongIdentity(t *testing.T, p processIdentity) {
	t.Helper()
	if p.PID <= 1 || p.BootID == "" || p.OSStartIdentity == "" || p.BinaryPath == "" || p.BinarySHA256 != fixtureCoreSHA(t) || len(p.LaunchNonceSHA256) != 64 || p.PrivateWorkdir == "" || p.Generation == "" || p.RunID == "" {
		t.Fatalf("incomplete compound process identity: %+v", p)
	}
}
func running(t *testing.T, o productObservation) processIdentity {
	t.Helper()
	if need(t, o.Runtime, "runtime") != "verified-running" {
		t.Fatal("expected verified-running")
	}
	return independentlyOpenCore(t, o)
}

// Fresh process/controller/entry proof is separate from committed management
// status. A pending publication may have a live target while status is unknown.
func independentlyOpenCore(t *testing.T, o productObservation) processIdentity {
	t.Helper()
	if !need(t, o.CorePresent, "independent process presence") {
		t.Fatal("running core absent")
	}
	p := need(t, o.Core, "actual core")
	strongIdentity(t, p)
	if p.Generation != need(t, o.Generation, "generation") || len(need(t, o.ControllerOwnerProof, "fresh connected-controller owner proof")) != 64 {
		t.Fatal("run/generation/controller-owner proof mismatch")
	}
	states := need(t, o.EntryStates, "all entry states")
	if len(states) != 2 {
		t.Fatal("two fixed entries required")
	}
	for _, state := range states {
		if state != "ready" {
			t.Fatal("unverified/open-before-ready entry")
		}
	}
	return p
}
func sameCore(t *testing.T, before processIdentity, after productObservation) {
	t.Helper()
	if got := running(t, after); got != before {
		t.Fatalf("original strong identity changed: before=%+v after=%+v", before, got)
	}
}
func noMutation(t *testing.T, before, after productEvidence) {
	t.Helper()
	if !reflect.DeepEqual(before.FixtureSeeds, after.FixtureSeeds) || !reflect.DeepEqual(before.DurableFiles, after.DurableFiles) || !reflect.DeepEqual(before.Launches, after.Launches) || !reflect.DeepEqual(before.Signals, after.Signals) || !reflect.DeepEqual(before.Operations, after.Operations) {
		t.Fatal("expected zero durable/runtime operation effects")
	}
}
func noDuplicateLaunch(t *testing.T, before, after productEvidence) {
	t.Helper()
	if !reflect.DeepEqual(before.Launches, after.Launches) {
		t.Fatal("unexpected duplicate launcher invocation")
	}
}
func result(t *testing.T, r operationResult, err error, want string) operationRecord {
	t.Helper()
	must(t, err)
	if r.Outcome != want || len(r.Originals) == 0 {
		t.Fatalf("operation outcome: %+v want %s", r, want)
	}
	for _, ref := range r.Originals {
		verifyOriginal(t, ref)
	}
	verifyOriginal(t, r.ResponseOriginal)
	for _, version := range r.TargetHistory {
		verifyOriginal(t, version.Original)
	}
	if err := validateOperationResult(r); err != nil {
		t.Fatal(err)
	}
	return r.Operation
}
func resultError(t *testing.T, r operationResult, err error, code string) {
	t.Helper()
	result(t, r, err, "")
	if r.ErrorCode != code || r.WireOutcome != "" || r.Operation.ID != "" || r.RecoveryCall != nil {
		t.Fatal("rejection must retain the actual error code without invented admission")
	}
}
func start(t *testing.T, h productHarness, ctx context.Context, allowRestart bool, missing ...string) (productObservation, productEvidence) {
	t.Helper()
	before := observe(t, h, ctx)
	r, err := h.Start(ctx, startRequest{Identity: request("runtime.start", "start-first", need(t, before.Revision, "initial revision")), Fixture: verifiedFixture, BundleID: baselineBundle, AllowRestart: allowRestart, RestartLimit: 1, UnavailableComponents: missing})
	op := result(t, r, err, "committed")
	if op.ID == "" || op.Phase != "committed" {
		t.Fatal("start requires original committed operation")
	}
	o := observe(t, h, ctx)
	running(t, o)
	if need(t, o.Desired, "desired") != "running" || need(t, o.Revision, "revision") == need(t, before.Revision, "old revision") {
		t.Fatal("start must commit running with new string revision")
	}
	assertFixedBundle(t, need(t, o.ActiveBundle, "committed baseline bundle"), baselineBundle)
	ev := evidence(t, h, ctx)
	assertDurableResult(t, ev, op)
	return o, ev
}
func traffic(t *testing.T, h productHarness, ctx context.Context, kind, id, entry string, n uint64, run config.RunID) {
	t.Helper()
	trafficForBundle(t, h, ctx, baselineBundle, kind, id, entry, n, run)
}
func trafficForBundle(t *testing.T, h productHarness, ctx context.Context, bundleID, kind, id, entry string, n uint64, run config.RunID) {
	t.Helper()
	v, err := h.Traffic(ctx, trafficRequest{Kind: kind, ConnectionID: id, Entry: entry, Bytes: n, TargetDomain: "alpha.test", BundleID: bundleID})
	must(t, err)
	if v.ConnectionID != id || !need(t, v.Forwarded, "actual forwarding") || need(t, v.Upload, "chunk upload") != n || need(t, v.Download, "chunk download") != n || need(t, v.CoreRunID, "traffic core run") != run || need(t, v.ActualNode, "actual selected node") != fixtureExpectedNode(t, bundleID, entry) {
		t.Fatal("existing/new traffic must match exact accepted per-entry node")
	}
}

// Read-only bounded polling: Observe cannot trigger recovery or mutate product state.
// Unknown/transitional observations are retained by the adapter, never converted to false/zero.
func awaitObservation(t *testing.T, h productHarness, ctx context.Context, label string, ready func(productObservation) bool) productObservation {
	t.Helper()
	// The traced integration driver fsyncs every original event before ACK.
	// This is a bounded correctness wait, not a product latency budget. The
	// first full restart run committed after 4.2s and its read proof exceeded
	// the former 5s window; retain room for that independent proof as well.
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		o, err := h.Observe(bounded)
		if err == nil && ready(o) {
			return o
		}
		if err != nil && bounded.Err() == nil {
			t.Fatal(err)
		}
		select {
		case <-bounded.Done():
			t.Fatalf("bounded read-only observation timed out: %s: %v", label, bounded.Err())
		case <-ticker.C:
		}
	}
}
func observedEquals[T comparable](v observed[T], want T) bool {
	return v.State == "observed" && v.Value != nil && *v.Value == want
}
func awaitStopped(t *testing.T, h productHarness, ctx context.Context) productObservation {
	t.Helper()
	return awaitObservation(t, h, ctx, "owned core stopped", func(o productObservation) bool {
		return observedEquals(o.Runtime, "verified-stopped") && observedEquals(o.CorePresent, false)
	})
}

func validBundle(t *testing.T, b bundleSnapshot) {
	t.Helper()
	if b.Generation == "" || len(b.ManifestSHA256) != 64 || len(b.ConfigSHA256) != 64 || len(b.AssetSHA256) == 0 || len(b.OrderedRules) == 0 || len(b.EntryAuthSHA256) != 2 || len(b.Entries) != 2 || len(b.NodeByEntry) != 2 {
		t.Fatal("incomplete immutable bundle snapshot")
	}
}
func assertBundle(t *testing.T, got, want bundleSnapshot) {
	t.Helper()
	validBundle(t, got)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("complete immutable bundle/selection/rule/auth snapshot mismatch")
	}
}

func assertOrdering(t *testing.T, e productEvidence) {
	t.Helper()
	for _, op := range e.Operations {
		verifyOriginal(t, op.Original)
	}
	for _, version := range e.OperationHistory {
		verifyOriginal(t, version.Original)
	}
	for _, event := range e.Boundaries {
		for _, ref := range event.Originals {
			verifyOriginal(t, ref)
		}
	}
	if err := validateOperationOrdering(e, fixtureCoreSHA(t)); err != nil {
		t.Fatal(err)
	}
}
func requiredTargetRevision(t *testing.T, op operationRecord) config.Revision {
	t.Helper()
	if op.TargetRevision == nil {
		t.Fatal("operation requires an original nonnull target revision")
	}
	return *op.TargetRevision
}
