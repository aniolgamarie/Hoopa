//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"hoopa/internal/config"
)

// ValidateSourceUpdateFailure bounds the transport's new error.details variant.
// It validates a public shape only; Journal/context checks establish identity.
func ValidateSourceUpdateFailure(raw []byte) bool {
	var failure sourceUpdateFailure
	if len(raw) > MaxResultBytes || json.Unmarshal(raw, &failure) != nil || !validSourceReceipt(failure.Details) || failure.Details.Reason == "" {
		return false
	}
	expected := sourceUpdateFailure{Code: "SUBSCRIPTION_UPDATE_FAILED", Message: "Subscription update did not produce a usable candidate", Details: failure.Details}
	encoded, err := json.Marshal(expected)
	if err != nil {
		return false
	}
	canonical, err := config.CanonicalJSON(encoded)
	actual, actualErr := config.CanonicalJSON(raw)
	return err == nil && actualErr == nil && bytes.Equal(canonical, actual)
}

// SourceUpdateProgress describes one attempt. Validated is true only when that
// attempt committed; an older usable candidate may still exist after a failure.
type SourceUpdateProgress struct {
	SourceID   config.ID `json:"subscription_id"`
	Sequence   uint64    `json:"sequence"`
	Downloaded bool      `json:"downloaded"`
	Validated  bool      `json:"validated"`
	SnapshotID *string   `json:"snapshot_id"`
	StartedAt  string    `json:"started_at"`
	FinishedAt *string   `json:"finished_at"`
	NextDue    *string   `json:"next_due"`
	Reason     string    `json:"reason"`
}

func (c *Coordinator) operationViewLocked(stored StoredOperation) (OperationView, error) {
	view := operationView(stored)
	if stored.Record.Identity.Operation != "subscription.update" {
		return view, nil
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return OperationView{}, err
	}
	entry, err := c.sourceEntryLocked(stored, settings)
	if err != nil {
		return OperationView{}, err
	}
	r := entry.Receipt
	progress := &SourceUpdateProgress{SourceID: r.SourceID, Sequence: r.Sequence, Downloaded: r.Downloaded, Validated: r.Validated, StartedAt: entry.Context.StartedAt, FinishedAt: r.FinishedAt, NextDue: entry.Context.NextDue, Reason: r.Reason}
	if r.Validated {
		id := r.SnapshotID
		progress.SnapshotID = &id
	}
	view.SourceUpdate = progress
	if c.sourceAlive != nil && c.sourceAlive(stored.Record.Identity.OperationID) {
		view.Recovery.Required = false
	}
	if r.Reason != "" {
		code := "SUBSCRIPTION_UPDATE_FAILED"
		view.ErrorCode = &code
	}
	return view, nil
}
