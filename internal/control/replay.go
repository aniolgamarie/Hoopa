//go:build linux

package control

import (
	"encoding/json"
	"errors"
	"hoopa/internal/p1testhook"
	"os"

	"hoopa/internal/config"
)

type Journal struct{ store *config.Store }

func NewJournal(store *config.Store) (*Journal, error) {
	if store == nil {
		return nil, ErrOperationRecord
	}
	return &Journal{store: store}, nil
}

func (j *Journal) Get(id config.ID) (StoredOperation, error) {
	raw, sha, err := j.store.ReadOperationFile(id)
	readErr := err
	if err != nil && !errors.Is(err, config.ErrUncertain) {
		if errors.Is(err, os.ErrNotExist) && !errors.Is(err, config.ErrReadUnavailable) {
			return StoredOperation{}, ErrOperationNotFound
		}
		return StoredOperation{}, err
	}
	o, err := decodeOperation(id, raw)
	if err != nil {
		return StoredOperation{}, err
	}
	return StoredOperation{o, sha}, readErr
}

// Lookup is always read-only, even on a writer or after an uncertain write.
// It returns the original operation's latest durable result, never executes it.
func (j *Journal) Lookup(r Request) (StoredOperation, error) {
	if r.raw == nil || !validRequestID(r.id) || !isHash(r.digest) {
		return StoredOperation{}, ErrRequest
	}
	stored, err := j.Get(r.OperationID())
	if err != nil {
		return stored, err
	}
	if stored.Record.Identity.RequestID != r.id {
		return StoredOperation{}, ErrOperationRecord
	}
	if stored.Record.Identity.RequestDigest != r.digest {
		return StoredOperation{}, ErrRequestIDReused
	}
	return stored, nil
}

// IDs bounds enumeration without loading every record into memory. Callers
// must Get/validate each record; enumeration alone is not recovery evidence.
func (j *Journal) IDs() ([]config.ID, error) { return j.store.ListOperationIDs() }

// Admit is called only AFTER the coordinator has validated a new mutation and
// serialized revision/inputs/consent. Existing request replay precedes those
// validations in the coordinator. created=false never grants permission to
// repeat effects; interrupted operations require explicit reconciliation.
func (j *Journal) Admit(r Request, intent Intent, result json.RawMessage) (stored StoredOperation, created bool, err error) {
	return j.admitWithEvidence(r, intent, result, json.RawMessage(`{}`))
}

// admitWithEvidence keeps a service incarnation in its first durable admission,
// so a crash before the next phase does not lose the prior writer's identity.
// Evidence remains caller-owned proof; this does not authorize any effect.
func (j *Journal) admitWithEvidence(r Request, intent Intent, result, evidence json.RawMessage) (stored StoredOperation, created bool, err error) {
	if old, err := j.Lookup(r); err == nil {
		return old, false, nil
	} else if !errors.Is(err, ErrOperationNotFound) {
		return StoredOperation{}, false, err
	}
	if !r.persistable() || intent.validate() != nil {
		return StoredOperation{}, false, ErrRequest
	}
	result, err = object(result, MaxResultBytes)
	if err != nil {
		return StoredOperation{}, false, err
	}
	evidence, err = object(evidence, MaxRequestBytes)
	if err != nil {
		return StoredOperation{}, false, err
	}
	o := Operation{
		Version: 1, Identity: Identity{r.OperationID(), r.id, r.digest, r.version, r.operation, intent},
		Request: retainedJSON(r.raw), Steps: []Step{{1, Prepared, retainedJSON(evidence), retainedJSON(result)}}, Phase: Prepared, Result: retainedJSON(result),
	}
	o.BodySHA256 = bodyHash(o)
	stored, err = j.publish(o, "")
	if errors.Is(err, config.ErrOperationConflict) {
		old, readErr := j.Lookup(r)
		return old, false, readErr
	}
	return stored, err == nil, err
}

// Advance only records a caller's evidence; it does not prove that evidence.
// The future coordinator owns execution/recovery authorization and must not
// perform further effects after any uncertain persistence error.
func (j *Journal) Advance(id config.ID, expectedSHA string, phase Phase, evidence, result json.RawMessage) (StoredOperation, error) {
	stored, err := j.Get(id)
	if err != nil {
		return StoredOperation{}, err
	}
	if stored.SHA256 != expectedSHA {
		return StoredOperation{}, config.ErrOperationConflict
	}
	o := stored.Record
	if terminal(o.Phase) {
		return StoredOperation{}, ErrOperationTerminal
	}
	if !transition(o.Phase, phase) {
		return StoredOperation{}, ErrOperationRecord
	}
	evidence, err = object(evidence, MaxRequestBytes)
	if err != nil {
		return StoredOperation{}, err
	}
	result, err = object(result, MaxResultBytes)
	if err != nil {
		return StoredOperation{}, err
	}
	o.Steps = append(o.Steps, Step{len(o.Steps) + 1, phase, retainedJSON(evidence), retainedJSON(result)})
	o.Phase, o.Result = phase, retainedJSON(result)
	o.BodySHA256 = bodyHash(o)
	return j.publish(o, expectedSHA)
}

// Compact retains the immutable request identity and exact persisted result.
// It releases bytes, never the dedup slot. Incomplete/degraded entries remain.
// Runtime lifecycle bodies also remain: later start/recovery needs their exit
// proofs. Until reference-aware retention exists, no lifecycle body is removed.
// Recover calls for service/source-only operations retain their target link too:
// empty-runtime Apply must prove that these calls did not change a runtime.
func (j *Journal) Compact(id config.ID, expectedSHA string) (StoredOperation, error) {
	stored, err := j.Get(id)
	if err != nil {
		return StoredOperation{}, err
	}
	if stored.SHA256 != expectedSHA {
		return StoredOperation{}, config.ErrOperationConflict
	}
	if !terminal(stored.Record.Phase) {
		return StoredOperation{}, ErrOperationIncomplete
	}
	if stored.Record.Identity.Operation == "runtime.stop" || stored.Record.Identity.Operation == "runtime.start" || stored.Record.Identity.Operation == "service.stop" {
		return StoredOperation{}, ErrOperationIncomplete
	}
	if stored.Record.Compacted {
		return stored, nil
	}
	if stored.Record.Identity.Operation == "operation.recover" {
		r, err := ParseRequest(stored.Record.Request)
		if err != nil {
			return StoredOperation{}, err
		}
		var p struct {
			OperationID config.ID `json:"operation_id"`
		}
		if !planFields(r.params, "operation_id", "expected_revision") || json.Unmarshal(r.params, &p) != nil {
			return StoredOperation{}, ErrOperationRecord
		}
		target, err := j.Get(p.OperationID)
		if err != nil {
			return StoredOperation{}, err
		}
		if target.Record.Identity.Operation == "service.stop" || target.Record.Identity.Operation == "subscription.update" {
			return StoredOperation{}, ErrOperationIncomplete
		}
	}
	o := stored.Record
	o.Request, o.Steps, o.Compacted = nil, nil, true
	return j.publish(o, expectedSHA)
}

func (j *Journal) publish(o Operation, expectedSHA string) (StoredOperation, error) {
	if err := o.validate(o.Identity.OperationID); err != nil {
		return StoredOperation{}, err
	}
	raw, err := encode(o)
	if err != nil || len(raw) > config.MaxOperationBytes {
		return StoredOperation{}, ErrOperationRecord
	}
	// Validate the exact serialization BEFORE making it authoritative. Return
	// its detached projection so caller mutation cannot alter future replays.
	detached, err := decodeOperation(o.Identity.OperationID, raw)
	if err != nil {
		return StoredOperation{}, err
	}
	sha, err := j.store.CompareAndSwapOperation(o.Identity.OperationID, expectedSHA, raw)
	if err != nil {
		return StoredOperation{}, err
	}
	if p1testhook.Enabled {
		_ = p1testhook.Record("journal", "published", string(o.Identity.OperationID), "", o.Identity.RequestID, false, map[string]any{"record_sha256": sha, "phase": o.Phase, "body_sha256": o.BodySHA256, "operation": o.Identity.Operation})
	}
	return StoredOperation{detached, sha}, nil
}
