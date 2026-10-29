//go:build linux

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	"hoopa/internal/config"
	"hoopa/internal/p1testhook"
)

var (
	ErrConflict              = errors.New("CONFLICT")
	ErrRecoveryRequired      = errors.New("RECOVERY_REQUIRED")
	ErrCapabilityUnsupported = errors.New("CAPABILITY_UNSUPPORTED")
	ErrConfirmation          = errors.New("AUTHORIZATION_DENIED")
)

// CurrentPlanInputs is an observation of local, fixed inputs, not the Plan's
// assertions echoed back. Its producer owns complete dependency discovery and
// an independent impact calculation from verified current/candidate material.
type CurrentPlanInputs struct {
	CompilerVersion    string
	CapabilitiesDigest string
	Inputs             []PlanInput
	Impact             PlanImpact
}

// CurrentInputs is trusted read-only code. It must observe all input identities
// in supplied immutable Plan, use no network or writes, and honor cancellation.
// Complete source/compiler semantics are provided by the later compiler layer.
type CurrentInputs func(context.Context, Plan) (CurrentPlanInputs, error)

// Coordinator exclusively owns its writer Store and serializes control changes.
// No writable handle escapes. Future Apply keeps mu across admission and effects;
// Store's own short mutex is never treated as a multi-call transaction.
type Coordinator struct {
	*coordinatorState
}

// Value copies of the public handle share both serialization and lifetime.
// Copying a mutex while retaining the same Store would create two writers.
type coordinatorState struct {
	mu        sync.Mutex
	store     *config.Store
	journal   *Journal
	validator IntentValidator
	current   CurrentInputs
	// Closing a watch never resets this one explicit grant per coordinator.
	// Coordinator/applier value copies share the same lifetime and guard.
	restartGranted  bool
	serviceID       string
	serviceQuiesced bool
	// Source workers are registered once. Cancellation holds mu, joining must
	// not: a cancelled worker may still need mu to persist its bounded receipt.
	sourceStop     func()
	sourceWait     func()
	sourceAlive    func(config.ID) bool
	sourceSchedule func() SourceScheduleStatus
	closeDone      chan struct{}
	closeErr       error
}

// OpenCoordinator is explicit service initialization, separate from request
// admission. Missing dependencies are rejected before any paths are opened.
func OpenCoordinator(paths config.Paths, validator IntentValidator, current CurrentInputs) (*Coordinator, error) {
	if validator == nil || current == nil {
		return nil, ErrRequest
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		return nil, err
	}
	journal, err := NewJournal(store)
	if err != nil {
		store.Close()
		return nil, err
	}
	return &Coordinator{&coordinatorState{store: store, journal: journal, validator: validator, current: current}}, nil
}

// Close waits for the whole in-flight control call before releasing the flock.
// It never stops a core or converts a pending operation into a terminal one.
func (c *Coordinator) Close() error {
	if c == nil || c.coordinatorState == nil {
		return nil
	}
	c.mu.Lock()
	if c.closeDone != nil {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closeDone = make(chan struct{})
	c.serviceQuiesced = true
	if c.sourceStop != nil {
		c.sourceStop()
	}
	wait := c.sourceWait
	c.mu.Unlock()
	if wait != nil {
		wait()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store != nil {
		c.closeErr = c.store.Close()
		c.store = nil
	}
	close(c.closeDone)
	return c.closeErr
}

// AdmitApply records only prepared/pending. created is not a token permitting
// callers to run effects after this method releases the lock. The apply layer
// must instead hold mu and use admitApplyLocked within its complete workflow.
func (c *Coordinator) AdmitApply(ctx context.Context, raw []byte) (StoredOperation, bool, error) {
	if c == nil || c.coordinatorState == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	request, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	stored, made, _, err := c.admitApplyLocked(ctx, request)
	return stored, made, err
}

// admitApplyLocked returns a validated Plan only for a newly admitted operation.
// Known identities are replayed before any new-request/environment validation.
func (c *Coordinator) admitApplyLocked(ctx context.Context, request Request) (StoredOperation, bool, Plan, error) {
	return c.admitCheckedApplyLocked(ctx, request, nil)
}

// check runs only after the complete new-request checks, while the same control
// lock remains held. The basic apply path uses it to validate and publish the
// immutable candidate before recording prepared; replay never calls it.
func (c *Coordinator) admitCheckedApplyLocked(ctx context.Context, request Request, check func(Plan, config.ActivePointer) error) (StoredOperation, bool, Plan, error) {
	return c.admitCheckedApplyUsingLocked(ctx, request, c.validator, c.current, check)
}

// Source compilation supplies per-request pure validation and independent
// current-input observation without replacing shared Coordinator callbacks.
// Every other admission/replay/revision/consent/journal gate remains identical.
func (c *Coordinator) admitCheckedApplyUsingLocked(ctx context.Context, request Request, validator IntentValidator, current CurrentInputs, check func(Plan, config.ActivePointer) error) (StoredOperation, bool, Plan, error) {
	fail := func(err error) (StoredOperation, bool, Plan, error) { return StoredOperation{}, false, Plan{}, err }
	if c.store == nil {
		return fail(config.ErrClosed)
	}
	old, err := c.journal.Lookup(request)
	if err == nil {
		return old, false, Plan{}, nil
	}
	if !errors.Is(err, ErrOperationNotFound) {
		return fail(err)
	}
	if c.serviceQuiesced {
		return fail(config.ErrClosed)
	}
	if ctx == nil {
		return fail(ErrRequest)
	}
	if validator == nil || current == nil {
		return fail(ErrRequest)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if request.version != "v1" || request.operation != "config.apply" || !planFields(request.params, "plan", "confirmed_plan_digest", "accept_disruption") {
		return fail(ErrRequest)
	}
	params, _ := planObject(request.params)
	if len(params["plan"]) == 0 || params["plan"][0] != '{' {
		return fail(ErrRequest)
	}
	var confirmation string
	if json.Unmarshal(params["confirmed_plan_digest"], &confirmation) != nil || !isHash(confirmation) {
		return fail(ErrRequest)
	}
	var consent bool
	switch string(params["accept_disruption"]) {
	case "true":
		consent = true
	case "false":
	default:
		return fail(ErrRequest)
	}
	plan, err := ParsePlan(params["plan"], validator)
	if err != nil {
		return fail(err)
	}
	doc := plan.Document()
	if confirmation != plan.Digest() || doc.Impact.RequiresDisruption && !consent {
		return fail(ErrConfirmation)
	}
	if len(doc.Blockers) != 0 {
		return fail(ErrPlan)
	}
	if doc.Impact.RuntimeAction != "start" && doc.Impact.RuntimeAction != "replace" {
		return fail(ErrCapabilityUnsupported)
	}
	// The accepted basic profile has no proven full-replacement connection
	// preservation. A recomputed digest cannot authorize an unsupported claim.
	if doc.Impact.RuntimeAction == "replace" && doc.Impact.ConnectionEffect == "preserve_old" {
		return fail(ErrCapabilityUnsupported)
	}
	active, err := c.store.ReadActive()
	if err != nil {
		return fail(err)
	}
	if string(active.Revision) != doc.BaseRevision || !sameGeneration(active.Generation, doc.Impact.PreviousGeneration) || doc.Impact.PreviousGeneration != nil && *doc.Impact.PreviousGeneration == doc.Impact.TargetGeneration {
		return fail(ErrConflict)
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return fail(err)
	}
	for _, id := range ids {
		stored, err := c.journal.Get(id)
		if err != nil {
			return fail(err)
		}
		if !terminal(stored.Record.Phase) && !c.liveSourceUpdateLocked(stored) {
			return fail(ErrRecoveryRequired)
		}
	}
	observed, err := current(ctx, plan)
	if err != nil {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		return fail(config.ErrReadUnavailable)
	}
	if !sameCurrentInputs(doc, observed) {
		return fail(ErrConflict)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if check != nil {
		if p1testhook.Enabled {
			_ = p1testhook.Record("control", "inputs-revalidated", "", "", request.ID(), false, map[string]any{"plan_digest": plan.Digest()})
		}
		if err := check(plan, active); err != nil {
			return fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// Entropy only names the proposed revision; it does not advance active state.
	// The optional apply check may already have published an immutable candidate,
	// but no operation or runtime effect is authorized until Admit succeeds.
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fail(config.ErrReadUnavailable)
	}
	target := config.Revision("r-" + hex.EncodeToString(nonce[:]))
	generation := config.Generation(doc.Impact.TargetGeneration)
	var previous *config.Generation
	if doc.Impact.PreviousGeneration != nil {
		v := config.Generation(*doc.Impact.PreviousGeneration)
		previous = &v
	}
	parts, _ := planObject(plan.Bytes())
	intent := Intent{BaseRevision: active.Revision, TargetRevision: &target, PreviousGeneration: previous, TargetGeneration: &generation, CandidateDigest: plan.Digest(), ImpactDigest: hash(parts["impact"])}
	result, err := encode(map[string]any{
		"api_version": "v1", "request_id": request.ID(), "ok": true,
		"data": map[string]any{
			"operation_id": request.OperationID(), "phase": Prepared,
			"base_revision": active.Revision, "target_revision": target,
			"outcome": "pending",
		},
		"error": nil, "warnings": []any{},
	})
	if err != nil {
		return fail(ErrRequest)
	}
	stored, made, err := c.journal.Admit(request, intent, result)
	if err != nil || !made {
		return stored, false, Plan{}, err
	}
	return stored, true, plan, nil
}

func sameGeneration(current *config.Generation, planned *string) bool {
	if current == nil || planned == nil {
		return current == nil && planned == nil
	}
	return string(*current) == *planned
}

func sameCurrentInputs(plan PlanDocument, current CurrentPlanInputs) bool {
	if current.CompilerVersion != plan.CompilerVersion || current.CapabilitiesDigest != plan.CapabilitiesDigest || len(current.Inputs) != len(plan.Inputs) || !reflect.DeepEqual(plan.Impact, current.Impact) {
		return false
	}
	want := make(map[string]string, len(plan.Inputs))
	for _, in := range plan.Inputs {
		want[in.Kind+"/"+in.ID] = in.SHA256
	}
	for _, in := range current.Inputs {
		key := in.Kind + "/" + in.ID
		sha, found := want[key]
		if !found || sha != in.SHA256 {
			return false
		}
		delete(want, key) // Reject duplicate observations as well as missing ones.
	}
	return len(want) == 0
}
