//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"hoopa/internal/config"
	"hoopa/internal/p1testhook"
)

type serviceStopEvidence struct {
	Version         int                  `json:"version"`
	Kind            string               `json:"kind"`
	Step            string               `json:"step"`
	Base            config.ActivePointer `json:"base"`
	Instance        string               `json:"instance"`
	AdmissionClosed bool                 `json:"admission_closed"`
	LeaseReplacedBy string               `json:"lease_replaced_by"`
}

func parseServiceStop(r Request) (config.Revision, error) {
	if r.version != "v1" || r.operation != "service.stop" || !planFields(r.params, "expected_revision") {
		return "", ErrRequest
	}
	var p struct {
		Revision string `json:"expected_revision"`
	}
	if json.Unmarshal(r.params, &p) != nil {
		return "", ErrRequest
	}
	if _, err := config.ParseID(p.Revision); err != nil {
		return "", ErrRequest
	}
	return config.Revision(p.Revision), nil
}

func serviceBaseDigest(base config.ActivePointer) string {
	b, _ := encode(base)
	b, _ = config.CanonicalJSON(b)
	return hash(b)
}

func decodeServiceStop(stored StoredOperation) (serviceStopEvidence, error) {
	var s serviceStopEvidence
	if stored.Record.Compacted || stored.Record.Identity.Operation != "service.stop" || len(stored.Record.Steps) == 0 {
		return s, ErrOperationRecord
	}
	raw := stored.Record.Steps[len(stored.Record.Steps)-1].Evidence
	if json.Unmarshal(raw, &s) != nil {
		return s, ErrOperationRecord
	}
	b, _ := encode(s)
	b, err := config.CanonicalJSON(b)
	i := stored.Record.Identity.Intent
	if err != nil || !bytes.Equal(b, raw) || s.Version != 1 || s.Kind != "service-stop" || !serviceInstance(s.Instance) || s.Base.Version != 1 || s.Base.Revision != i.BaseRevision || i.TargetRevision != nil || i.CandidateDigest != serviceBaseDigest(s.Base) || !reflect.DeepEqual(i.PreviousGeneration, s.Base.Generation) || !reflect.DeepEqual(i.TargetGeneration, s.Base.Generation) {
		return s, ErrOperationRecord
	}
	if s.LeaseReplacedBy != "" && (!serviceInstance(s.LeaseReplacedBy) || s.LeaseReplacedBy == s.Instance || !s.AdmissionClosed) {
		return s, ErrOperationRecord
	}
	r, err := ParseRequest(stored.Record.Request)
	if err != nil {
		return s, err
	}
	revision, err := parseServiceStop(r)
	if err != nil || revision != s.Base.Revision || i.ImpactDigest != hash(r.params) || stored.Record.Phase == Committed && !s.AdmissionClosed || stored.Record.Phase == RolledBack {
		return s, ErrOperationRecord
	}
	return s, nil
}

func serviceStopResult(stored StoredOperation, phase Phase) json.RawMessage {
	outcome := "pending"
	if terminal(phase) || phase == Degraded {
		outcome = string(phase)
	}
	raw, _ := applyResult(stored, applyEvidence{}, phase, outcome)
	if phase != Committed {
		return raw
	}
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	result["warnings"] = []any{map[string]any{
		"code":    "SERVICE_ADMISSION_CLOSED",
		"message": "The originating management service stopped accepting requests; OS exit is not confirmed. Core forwarding was not stopped; collection is unavailable.",
	}}
	raw, _ = encode(result)
	return raw
}

func (c *Coordinator) advanceServiceStop(stored StoredOperation, s serviceStopEvidence, phase Phase, step string) (StoredOperation, error) {
	s.Step = step
	raw, _ := encode(s)
	return c.journal.Advance(stored.Record.Identity.OperationID, stored.SHA256, phase, raw, serviceStopResult(stored, phase))
}

// StopService commits closure of this service's request admission, not a claim
// that its OS process has already exited. quiesce closes admission/listener and
// cancels background work only: it must not close/wait on this Coordinator or
// the originating response connection, nor signal any core.
func (c *Coordinator) StopService(ctx context.Context, raw []byte, quiesce func() error) (StoredOperation, bool, error) {
	if c == nil || c.coordinatorState == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	r, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer p1RequestScope(r)()
	if c.store == nil {
		return StoredOperation{}, false, config.ErrClosed
	}
	if old, err := c.journal.Lookup(r); !errors.Is(err, ErrOperationNotFound) {
		return old, false, err
	}
	if c.serviceQuiesced {
		return StoredOperation{}, false, config.ErrClosed
	}
	if ctx == nil || quiesce == nil || !serviceInstance(c.serviceID) {
		return StoredOperation{}, false, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	revision, err := parseServiceStop(r)
	if err != nil {
		return StoredOperation{}, false, err
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return StoredOperation{}, false, err
	}
	if revision != base.Revision {
		return StoredOperation{}, false, ErrConflict
	}
	// A manager may stop while a core operation needs recovery. It cannot
	// conceal that operation; explicit old-service recovery handles its own
	// record first without advancing unrelated core operations.
	if err := c.store.CheckOperationAdmission(); err != nil {
		return StoredOperation{}, false, err
	}
	s := serviceStopEvidence{Version: 1, Kind: "service-stop", Step: "service-stop-prepared", Base: base, Instance: c.serviceID}
	i := Intent{BaseRevision: base.Revision, PreviousGeneration: base.Generation, TargetGeneration: base.Generation, CandidateDigest: serviceBaseDigest(base), ImpactDigest: hash(r.params)}
	seed := StoredOperation{Record: Operation{Identity: Identity{OperationID: r.OperationID(), RequestID: r.ID(), Intent: i}}}
	if err := ctx.Err(); err != nil {
		return StoredOperation{}, false, err
	}
	evidence, _ := encode(s)
	stored, made, err := c.journal.admitWithEvidence(r, i, serviceStopResult(seed, Prepared), evidence)
	if err != nil || !made {
		return stored, made, err
	}
	stored, err = c.executeServiceStop(ctx, stored, s, quiesce, p1Boundary(r))
	return stored, true, err
}

func (c *Coordinator) executeServiceStop(ctx context.Context, stored StoredOperation, s serviceStopEvidence, quiesce func() error, boundary func(string) error) (StoredOperation, error) {
	advance := func(phase Phase, step string) error {
		next, err := c.advanceServiceStop(stored, s, phase, step)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		stored = next
		return lifecycleCut(boundary, step)
	}
	if err := advance(Validated, "service-stop-intent"); err != nil {
		return stored, err
	}
	if err := ctx.Err(); err != nil {
		return stored, err
	}
	if err := advance(Applying, "service-quiesce"); err != nil {
		return stored, err
	}
	c.serviceQuiesced = true
	if err := quiesce(); err != nil {
		next, writeErr := c.advanceServiceStop(stored, s, Degraded, "service-quiesce-unverified")
		if writeErr == nil {
			stored = next
		}
		return stored, errors.Join(ErrRecoveryRequired, err, writeErr)
	}
	s.AdmissionClosed = true
	if p1testhook.Enabled {
		_ = p1testhook.Record("service", "admission-closed", string(stored.Record.Identity.OperationID), "", stored.Record.Identity.RequestID, false, map[string]any{"service_instance": s.Instance})
	}
	// The effect has happened. Finish recording its fact even if the client
	// disconnected; delivery is separate and an exact retry replays the record.
	for _, phase := range []Phase{Verifying, Publishing, Committed} {
		if err := advance(phase, "service-stop-"+string(phase)); err != nil {
			return stored, err
		}
	}
	return stored, nil
}

func (c *Coordinator) reconcileServiceStop(ctx context.Context, target StoredOperation, boundary func(string) error) (StoredOperation, error) {
	s, err := decodeServiceStop(target)
	if err != nil {
		if terminal(target.Record.Phase) {
			return target, errors.Join(ErrRecoveryRequired, err)
		}
		raw := target.Record.Steps[len(target.Record.Steps)-1].Evidence
		next, writeErr := c.journal.Advance(target.Record.Identity.OperationID, target.SHA256, Degraded, json.RawMessage(raw), serviceStopResult(target, Degraded))
		if writeErr != nil {
			return target, errors.Join(errRecoveryPersistence, writeErr)
		}
		return next, errors.Join(ErrRecoveryRequired, err)
	}
	if target.Record.Phase == Committed {
		return target, nil // Durable historical quiescence, not today's OS state.
	}
	if !serviceInstance(c.serviceID) || c.serviceID == s.Instance || c.serviceQuiesced {
		return target, ErrRecoveryRequired
	}
	// Only OpenServiceRuntime on the actual new exclusive writer can mint this
	// different session. Lease replacement proves the prior service no longer
	// admits authorized writes. It does not prove core or OS-process death.
	base, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(base, s.Base) {
		return target, errors.Join(ErrConflict, err)
	}
	s.AdmissionClosed, s.LeaseReplacedBy = true, c.serviceID
	for target.Record.Phase != Committed {
		if err := ctx.Err(); err != nil {
			return target, err
		}
		next := Verifying
		switch target.Record.Phase {
		case Prepared:
			next = Validated
		case Validated:
			next = Applying
		case Applying, Degraded:
			next = Verifying
		case Verifying:
			next = Publishing
		case Publishing:
			next = Committed
		default:
			return target, ErrOperationRecord
		}
		updated, err := c.advanceServiceStop(target, s, next, "recover-service-"+string(next))
		if err != nil {
			return target, errors.Join(errRecoveryPersistence, err)
		}
		target = updated
		if err := lifecycleCut(boundary, "recover-service-"+string(next)); err != nil {
			return target, err
		}
	}
	return target, nil
}

// First configuration may follow only proved service-only history. Unknown,
// core-related, pending and corrupt records cannot become a pristine first run.
func (c *Coordinator) serviceOnlyHistory(record StoredOperation, base config.ActivePointer) bool {
	if record.Record.Phase != Committed {
		return false
	}
	if record.Record.Identity.Operation == "operation.recover" {
		r, err := ParseRequest(record.Record.Request)
		if err != nil {
			return false
		}
		p, err := parseRecoverParameters(r)
		if err != nil {
			return false
		}
		target, err := c.journal.Get(p.OperationID)
		return err == nil && target.Record.Identity.Operation == "service.stop" && recoveryCallBound(record, target) && c.serviceOnlyHistory(target, base)
	}
	s, err := decodeServiceStop(record)
	return err == nil && s.AdmissionClosed && reflect.DeepEqual(s.Base, base)
}
