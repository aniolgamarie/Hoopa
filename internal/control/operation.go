//go:build linux

// Package control implements persistent request identity. It does not yet
// implement the coordinator or authorize core effects from a stored record.
package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"hoopa/internal/config"
)

const (
	MaxRequestBytes = 128 << 10
	MaxResultBytes  = 64 << 10
	MaxStepBytes    = 512 << 10
	MaxSteps        = 128
)

var (
	ErrRequest             = errors.New("INVALID_OPERATION_REQUEST")
	ErrRequestIDReused     = errors.New("REQUEST_ID_REUSED")
	ErrOperationNotFound   = errors.New("OPERATION_NOT_FOUND")
	ErrOperationRecord     = errors.New("INVALID_OPERATION_RECORD")
	ErrOperationTerminal   = errors.New("OPERATION_ALREADY_TERMINAL")
	ErrOperationIncomplete = errors.New("OPERATION_NOT_COMPACTABLE")
)

type Request struct {
	id, version, operation, digest string
	raw, params                    json.RawMessage
}

func (Request) String() string                { return "control.Request{private material}" }
func (Request) GoString() string              { return "control.Request{private material}" }
func (r Request) ID() string                  { return r.id }
func (r Request) Version() string             { return r.version }
func (r Request) Operation() string           { return r.operation }
func (r Request) Parameters() json.RawMessage { return bytes.Clone(r.params) }
func (r Request) Digest() string              { return r.digest }
func (r Request) OperationID() config.ID      { return config.ID("op-" + hash([]byte(r.id))) }

func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func isHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func validRequestID(s string) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= 1 && n <= 128
}
func boundedString(s string) bool { return s != "" && len(s) <= 128 && utf8.ValidString(s) }

// ParseRequest validates the identity envelope, not an operation's parameter
// schema or authorization. Other version/name values remain distinguishable so
// Lookup can reject altered retries before new-request validation.
func ParseRequest(raw []byte) (Request, error) {
	var r Request
	canonical, err := config.CanonicalJSON(raw)
	if err != nil || len(canonical) > MaxRequestBytes {
		return r, ErrRequest
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(canonical, &object) != nil || len(object) != 4 {
		return r, ErrRequest
	}
	if json.Unmarshal(object["request_id"], &r.id) != nil || !validRequestID(r.id) || json.Unmarshal(object["api_version"], &r.version) != nil || !boundedString(r.version) || json.Unmarshal(object["operation"], &r.operation) != nil || !boundedString(r.operation) {
		return Request{}, ErrRequest
	}
	r.params = object["params"]
	if len(r.params) == 0 || r.params[0] != '{' {
		return Request{}, ErrRequest
	}
	body, _ := encode(struct {
		Version   string          `json:"api_version"`
		Operation string          `json:"operation"`
		Params    json.RawMessage `json:"params"`
	}{r.version, r.operation, r.params})
	body, err = config.CanonicalJSON(body)
	if err != nil {
		return Request{}, ErrRequest
	}
	r.digest, r.raw = hash(body), append(json.RawMessage(nil), canonical...)
	return r, nil
}

func (r Request) persistable() bool {
	if r.version != "v1" || r.raw == nil || !isHash(r.digest) {
		return false
	}
	switch r.operation {
	case "config.apply", "subscription.update", "runtime.stop", "service.stop", "operation.recover":
		return true
	case "runtime.start":
		var params map[string]json.RawMessage
		json.Unmarshal(r.params, &params)
		var controlOnly bool
		return json.Unmarshal(params["control_only"], &controlOnly) == nil && !controlOnly
	}
	return false
}

type Intent struct {
	BaseRevision       config.Revision    `json:"base_revision"`
	TargetRevision     *config.Revision   `json:"target_revision"`
	PreviousGeneration *config.Generation `json:"previous_generation"`
	TargetGeneration   *config.Generation `json:"target_generation"`
	CandidateDigest    string             `json:"candidate_digest"`
	ImpactDigest       string             `json:"impact_digest"`
}

func (i Intent) validate() error {
	if _, err := config.ParseID(string(i.BaseRevision)); err != nil || !isHash(i.CandidateDigest) || !isHash(i.ImpactDigest) {
		return ErrOperationRecord
	}
	if i.TargetRevision != nil {
		if _, err := config.ParseID(string(*i.TargetRevision)); err != nil || *i.TargetRevision == i.BaseRevision {
			return ErrOperationRecord
		}
	}
	for _, g := range []*config.Generation{i.PreviousGeneration, i.TargetGeneration} {
		if g != nil {
			if _, err := config.ParseID(string(*g)); err != nil {
				return ErrOperationRecord
			}
		}
	}
	return nil
}

type Identity struct {
	OperationID   config.ID `json:"operation_id"`
	RequestID     string    `json:"request_id"`
	RequestDigest string    `json:"request_digest"`
	APIVersion    string    `json:"api_version"`
	Operation     string    `json:"operation"`
	Intent        Intent    `json:"intent"`
}

type Phase string

const (
	Prepared    Phase = "prepared"
	Validated   Phase = "validated"
	Applying    Phase = "applying"
	Verifying   Phase = "verifying"
	Publishing  Phase = "publishing"
	Committed   Phase = "committed"
	RollingBack Phase = "rolling_back"
	RolledBack  Phase = "rolled_back"
	Degraded    Phase = "degraded"
)

func terminal(p Phase) bool { return p == Committed || p == RolledBack }
func transition(from, to Phase) bool {
	if terminal(from) {
		return false
	}
	if from == to {
		return validPhase(from)
	}
	if to == RollingBack || to == Degraded {
		return validPhase(from)
	}
	switch from {
	case Prepared:
		return to == Validated
	case Validated:
		return to == Applying
	case Applying:
		return to == Verifying
	case Verifying:
		return to == Publishing
	case Publishing:
		return to == Committed
	case RollingBack:
		return to == RolledBack
	case Degraded:
		return to == Verifying // Explicit recovery must prove it afresh.
	}
	return false
}
func validPhase(p Phase) bool {
	switch p {
	case Prepared, Validated, Applying, Verifying, Publishing, Committed, RollingBack, RolledBack, Degraded:
		return true
	}
	return false
}

// retainedJSON stores canonical JSON as exact bytes. encoding/json's []byte
// representation is base64, avoiding RawMessage's second escaping pass. This
// private file envelope is not the CLI wire response; consumers project Result
// explicitly as json.RawMessage. Compacted bodies encode as null and decode nil.
type retainedJSON []byte

type Step struct {
	Sequence int          `json:"sequence"`
	Phase    Phase        `json:"phase"`
	Evidence retainedJSON `json:"evidence"`
	Result   retainedJSON `json:"result"`
}
type Operation struct {
	Version    int          `json:"version"`
	Identity   Identity     `json:"identity"`
	Request    retainedJSON `json:"request"`
	Steps      []Step       `json:"steps"`
	Phase      Phase        `json:"phase"`
	Result     retainedJSON `json:"result"`
	Compacted  bool         `json:"compacted"`
	BodySHA256 string       `json:"body_sha256"`
}

func (Operation) String() string   { return "control.Operation{private material}" }
func (Operation) GoString() string { return "control.Operation{private material}" }

type StoredOperation struct {
	Record Operation
	SHA256 string
}

func (StoredOperation) String() string   { return "control.StoredOperation{private material}" }
func (StoredOperation) GoString() string { return "control.StoredOperation{private material}" }

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func object(raw []byte, limit int) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > limit {
		return nil, ErrOperationRecord
	}
	canonical, err := config.CanonicalJSON(raw)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' || len(canonical) > limit {
		return nil, ErrOperationRecord
	}
	return canonical, nil
}
func bodyHash(o Operation) string {
	raw, _ := encode(struct {
		Request retainedJSON `json:"request"`
		Steps   []Step       `json:"steps"`
	}{o.Request, o.Steps})
	return hash(raw)
}

func (o Operation) validate(id config.ID) error {
	i := o.Identity
	if o.Version != 1 || i.OperationID != id || !validRequestID(i.RequestID) || id != config.ID("op-"+hash([]byte(i.RequestID))) || !isHash(i.RequestDigest) || i.APIVersion != "v1" || !boundedString(i.Operation) || i.Intent.validate() != nil || !validPhase(o.Phase) || !isHash(o.BodySHA256) {
		return ErrOperationRecord
	}
	if _, err := object(o.Result, MaxResultBytes); err != nil {
		return err
	}
	if o.Compacted {
		if !terminal(o.Phase) || o.Request != nil || o.Steps != nil {
			return ErrOperationRecord
		}
		return nil
	}
	r, err := ParseRequest(o.Request)
	if err != nil || !r.persistable() || r.id != i.RequestID || r.digest != i.RequestDigest || r.version != i.APIVersion || r.operation != i.Operation || len(o.Steps) == 0 || len(o.Steps) > MaxSteps || o.BodySHA256 != bodyHash(o) {
		return ErrOperationRecord
	}
	total := 0
	for n, step := range o.Steps {
		if step.Sequence != n+1 || n == 0 && step.Phase != Prepared || n > 0 && !transition(o.Steps[n-1].Phase, step.Phase) {
			return ErrOperationRecord
		}
		if _, err := object(step.Evidence, MaxRequestBytes); err != nil {
			return err
		}
		if _, err := object(step.Result, MaxResultBytes); err != nil {
			return err
		}
		total += len(step.Evidence) + len(step.Result)
	}
	last := o.Steps[len(o.Steps)-1]
	if total > MaxStepBytes || last.Phase != o.Phase || !bytes.Equal(last.Result, o.Result) {
		return ErrOperationRecord
	}
	return nil
}

func decodeOperation(id config.ID, raw []byte) (Operation, error) {
	var o Operation
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&o) != nil {
		return Operation{}, ErrOperationRecord
	}
	encoded, err := encode(o)
	if err != nil || !bytes.Equal(encoded, raw) || o.validate(id) != nil {
		return Operation{}, ErrOperationRecord
	}
	return o, nil
}
