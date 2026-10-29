//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"reflect"

	"hoopa/internal/config"
)

// sourceUpdateReceipt contains only bounded public facts. The referenced private
// context and candidate still need independent Store/RestoreAsset verification.
type sourceUpdateReceipt struct {
	OperationID   config.ID       `json:"operation_id"`
	Phase         Phase           `json:"phase"`
	BaseRevision  config.Revision `json:"base_revision"`
	ContextSHA256 string          `json:"context_sha256"`
	SourceID      config.ID       `json:"subscription_id"`
	Sequence      uint64          `json:"sequence"`
	Downloaded    bool            `json:"downloaded"`
	Validated     bool            `json:"validated"`
	AssetSHA256   string          `json:"asset_sha256"`
	SnapshotID    string          `json:"snapshot_id"`
	FinishedAt    *string         `json:"finished_at"`
	Reason        string          `json:"reason"`
}

type sourceUpdateResponse struct {
	APIVersion string               `json:"api_version"`
	RequestID  string               `json:"request_id"`
	OK         bool                 `json:"ok"`
	Data       *sourceUpdateReceipt `json:"data"`
	Error      *sourceUpdateFailure `json:"error"`
	Warnings   []any                `json:"warnings"`
}
type sourceUpdateFailure struct {
	Code      string              `json:"code"`
	Message   string              `json:"message"`
	Retryable bool                `json:"retryable"`
	Details   sourceUpdateReceipt `json:"details"`
}

func sourceReceipt(ctx sourceUpdateContext, digest string) sourceUpdateReceipt {
	return sourceUpdateReceipt{OperationID: ctx.OperationID, Phase: Prepared, BaseRevision: ctx.Base.Revision, ContextSHA256: digest, SourceID: ctx.Source.ID, Sequence: ctx.Sequence}
}

func validSourceReceipt(r sourceUpdateReceipt) bool {
	if r.OperationID != config.ID("op-"+stringHashSuffix(r.OperationID)) || !isHash(stringHashSuffix(r.OperationID)) || !sourceContextID(string(r.BaseRevision)) || !isHash(r.ContextSHA256) || !sourceContextID(string(r.SourceID)) || r.Sequence == 0 || r.Sequence > (1<<53)-1 {
		return false
	}
	if r.FinishedAt != nil {
		if _, ok := sourceContextTime(*r.FinishedAt); !ok {
			return false
		}
	}
	noAsset := r.AssetSHA256 == "" && r.SnapshotID == ""
	switch r.Phase {
	case Prepared, Validated, Applying:
		return !r.Downloaded && !r.Validated && noAsset && r.FinishedAt == nil && r.Reason == ""
	case Verifying:
		return r.Downloaded && !r.Validated && noAsset && r.FinishedAt == nil && r.Reason == ""
	case Publishing, Committed:
		return r.Downloaded && r.Validated == (r.Phase == Committed) && isHash(r.AssetSHA256) && sourceContextID(string(r.SnapshotID)) && r.FinishedAt != nil && r.Reason == ""
	case RollingBack, RolledBack:
		if r.Validated || !noAsset || r.FinishedAt == nil {
			return false
		}
		switch r.Reason {
		case "download_failed":
			return !r.Downloaded
		case "validation_failed", "storage_failed":
			return r.Downloaded
		case "input_changed", "cancelled", "interrupted":
			return true
		}
	}
	return false
}

func stringHashSuffix(id config.ID) string {
	s := string(id)
	if len(s) != 67 || s[:3] != "op-" {
		return ""
	}
	return s[3:]
}

func encodeSourceUpdateResult(requestID string, r sourceUpdateReceipt) ([]byte, error) {
	if !validRequestID(requestID) || r.OperationID != config.ID("op-"+hash([]byte(requestID))) || !validSourceReceipt(r) {
		return nil, ErrOperationRecord
	}
	response := sourceUpdateResponse{APIVersion: "v1", RequestID: requestID, OK: r.Reason == "", Data: &r, Warnings: []any{}}
	if !response.OK {
		response.Data = nil
		response.Error = &sourceUpdateFailure{Code: "SUBSCRIPTION_UPDATE_FAILED", Message: "Subscription update did not produce a usable candidate", Details: r}
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, ErrOperationRecord
	}
	return object(raw, MaxResultBytes)
}

func decodeSourceUpdateResult(raw []byte, identity Identity, phase Phase) (sourceUpdateReceipt, error) {
	bad := func() (sourceUpdateReceipt, error) { return sourceUpdateReceipt{}, ErrOperationRecord }
	var response sourceUpdateResponse
	if len(raw) > MaxResultBytes || json.Unmarshal(raw, &response) != nil {
		return bad()
	}
	r := response.Data
	if !response.OK && response.Error != nil {
		r = &response.Error.Details
	}
	if r == nil {
		return bad()
	}
	exact, err := encodeSourceUpdateResult(identity.RequestID, *r)
	if err != nil || !bytes.Equal(exact, raw) || r.OperationID != identity.OperationID || r.Phase != phase || r.BaseRevision != identity.Intent.BaseRevision || r.ContextSHA256 != identity.Intent.CandidateDigest {
		return bad()
	}
	return *r, nil
}

// Pure binding validation works after journal compaction as well. It does not
// read the context asset or infer that provider bytes have been restored.
func decodeSourceUpdateRecord(stored StoredOperation, context sourceUpdateContext) (sourceUpdateReceipt, error) {
	bad := func() (sourceUpdateReceipt, error) { return sourceUpdateReceipt{}, ErrOperationRecord }
	o := stored.Record
	i := o.Identity
	raw, err := encodeSourceUpdateContext(context)
	if err != nil || o.validate(i.OperationID) != nil || i.APIVersion != "v1" || i.Operation != "subscription.update" || i.OperationID != context.OperationID || i.RequestDigest != context.RequestDigest || i.Intent.BaseRevision != context.Base.Revision || i.Intent.TargetRevision != nil || i.Intent.TargetGeneration != nil || !reflect.DeepEqual(i.Intent.PreviousGeneration, context.Base.Generation) || i.Intent.CandidateDigest != hash(raw) {
		return bad()
	}
	r, err := decodeSourceUpdateResult(o.Result, i, o.Phase)
	if err != nil || r.SourceID != context.Source.ID || r.Sequence != context.Sequence {
		return bad()
	}
	if o.Compacted {
		return r, nil
	}
	request, err := ParseRequest(o.Request)
	if err != nil || i.Intent.ImpactDigest != hash(request.params) {
		return bad()
	}
	p, err := parseSourceUpdate(request)
	if err != nil || p.SourceID != context.Source.ID || p.Revision != context.Base.Revision || p.HasRegionRules && !reflect.DeepEqual(p.RegionRules, context.RegionRules) {
		return bad()
	}
	if p.Intent != nil {
		source, err := sourceUpdateDefinition(p, nil)
		if err != nil || !reflect.DeepEqual(source, context.Source) {
			return bad()
		}
	}
	var prior sourceUpdateReceipt
	for n, step := range o.Steps {
		current, err := decodeSourceUpdateResult(step.Result, i, step.Phase)
		if err != nil || current.SourceID != context.Source.ID || current.Sequence != context.Sequence || !bytes.Equal(step.Evidence, []byte(`{}`)) {
			return bad()
		}
		if n > 0 {
			if current.Phase == RollingBack && prior.Phase != RollingBack {
				switch current.Reason {
				case "download_failed":
					if prior.Phase != Applying {
						return bad()
					}
				case "validation_failed", "storage_failed":
					if prior.Phase != Verifying {
						return bad()
					}
				}
			}
			if prior.Downloaded != current.Downloaded && !(prior.Phase == Applying && current.Phase == Verifying && current.Downloaded) {
				return bad()
			}
			if prior.Phase == Publishing && current.Phase == Committed && (prior.AssetSHA256 != current.AssetSHA256 || prior.SnapshotID != current.SnapshotID || !reflect.DeepEqual(prior.FinishedAt, current.FinishedAt)) {
				return bad()
			}
			if prior.Phase == RollingBack && (current.Reason != prior.Reason || !reflect.DeepEqual(prior.FinishedAt, current.FinishedAt)) {
				return bad()
			}
		}
		prior = current
	}
	return r, nil
}
