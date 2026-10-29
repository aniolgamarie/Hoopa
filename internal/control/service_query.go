//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"hoopa/internal/config"
)

type OperationView struct {
	OperationID    config.ID             `json:"operation_id"`
	Operation      string                `json:"operation"`
	Phase          Phase                 `json:"phase"`
	BaseRevision   config.Revision       `json:"base_revision"`
	TargetRevision *config.Revision      `json:"target_revision"`
	Outcome        string                `json:"outcome"`
	ErrorCode      *string               `json:"error_code"`
	Recovery       RecoveryView          `json:"recovery"`
	SourceUpdate   *SourceUpdateProgress `json:"source_update,omitempty"`
}

// RecoveryView is a bounded historical projection, never private evidence or
// a fresh claim about runtime state. Digests identify the retained source.
type RecoveryView struct {
	Source             string     `json:"source"`
	RecordSHA256       string     `json:"record_sha256"`
	Compacted          bool       `json:"compacted"`
	StepCount          int        `json:"step_count"`
	LastEvidenceSHA256 *string    `json:"last_evidence_sha256"`
	Required           bool       `json:"required"`
	TargetOperationID  *config.ID `json:"target_operation_id"`
}

func operationView(stored StoredOperation) OperationView {
	r := stored.Record
	outcome := "pending"
	if terminal(r.Phase) || r.Phase == Degraded {
		outcome = string(r.Phase)
	}
	recovery := RecoveryView{Source: "durable_operation_journal", RecordSHA256: stored.SHA256, Compacted: r.Compacted, StepCount: len(r.Steps), Required: !terminal(r.Phase)}
	if len(r.Steps) > 0 {
		digest := hash(r.Steps[len(r.Steps)-1].Evidence)
		recovery.LastEvidenceSHA256 = &digest
	}
	if r.Identity.Operation == "operation.recover" && !r.Compacted {
		request, err := ParseRequest(r.Request)
		if err == nil {
			p, err := parseRecoverParameters(request)
			if err == nil {
				recovery.TargetOperationID = &p.OperationID
			}
		}
	}
	var result struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	var code *string
	if json.Unmarshal(r.Result, &result) == nil && result.Error != nil {
		// Only this finite allowlist can be projected from a stored receipt.
		switch result.Error.Code {
		case "RECOVERY_REQUIRED", "APPLY_FAILED":
			code = &result.Error.Code
		}
	}
	return OperationView{OperationID: r.Identity.OperationID, Operation: r.Identity.Operation, Phase: r.Phase, BaseRevision: r.Identity.Intent.BaseRevision, TargetRevision: r.Identity.Intent.TargetRevision, Outcome: outcome, ErrorCode: code, Recovery: recovery}
}

type CoreStatus struct {
	State      string        `json:"state"`
	Reason     string        `json:"reason"`
	RunID      *config.RunID `json:"run_id"`
	PID        *int          `json:"pid"`
	StartTicks *uint64       `json:"start_ticks"`
	BootID     *string       `json:"boot_id"`
}

// ServiceStatus is the currently implemented lifecycle projection. No source,
// telemetry or full diagnostic capability is claimed by these fields.
type ServiceStatus struct {
	ObservedAt       time.Time             `json:"observed_at"`
	Revision         config.Revision       `json:"revision"`
	Generation       *config.Generation    `json:"generation"`
	DesiredRuntime   string                `json:"desired_runtime"`
	CommittedRuntime string                `json:"committed_runtime"`
	Service          string                `json:"service"`
	Core             CoreStatus            `json:"core"`
	Collector        string                `json:"collector"`
	CollectorReason  string                `json:"collector_reason"`
	Pending          []OperationView       `json:"pending"`
	PendingTotal     int                   `json:"pending_total"`
	PendingTruncated bool                  `json:"pending_truncated"`
	Degraded         bool                  `json:"degraded"`
	DegradedReasons  []string              `json:"degraded_reasons"`
	RestartPolicy    string                `json:"restart_policy"`
	Capabilities     []string              `json:"capabilities"`
	SourceSchedule   *SourceScheduleStatus `json:"source_schedule,omitempty"`
}

func (c *Coordinator) Status(ctx context.Context) (ServiceStatus, error) {
	if c == nil || c.coordinatorState == nil {
		return ServiceStatus{}, config.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked(ctx)
}

func (c *Coordinator) statusLocked(ctx context.Context) (ServiceStatus, error) {
	var out ServiceStatus
	if c.store == nil {
		return out, config.ErrClosed
	}
	if ctx == nil {
		return out, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return out, err
	}
	out = ServiceStatus{Revision: base.Revision, Generation: base.Generation, DesiredRuntime: base.DesiredRuntime, CommittedRuntime: base.DesiredRuntime, Service: "unavailable", Core: CoreStatus{State: "unknown", Reason: "ownership_unverified"}, Collector: "unavailable", CollectorReason: "not_implemented", Pending: []OperationView{}, Capabilities: []string{"status.get", "operation.get", "runtime.start", "runtime.stop", "operation.recover", "service.stop"}}
	out.DegradedReasons = []string{}
	out.RestartPolicy = "unavailable"
	if c.sourceStop != nil {
		out.Capabilities = append(out.Capabilities, "subscription.update")
	}
	out.Capabilities = append(out.Capabilities, "subscription.list")
	out.Capabilities = append(out.Capabilities, "config.plan", "config.apply", "doctor.get")
	if c.sourceSchedule != nil {
		state := c.sourceSchedule()
		out.SourceSchedule = &state
	}
	if c.serviceID != "" {
		out.Service = "running"
	}
	if c.serviceQuiesced {
		out.Service = "quiesced"
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return ServiceStatus{}, err
	}
	corePending := false
	for _, id := range ids {
		stored, err := c.journal.Get(id)
		if err != nil {
			return ServiceStatus{}, err
		}
		if terminal(stored.Record.Phase) {
			continue
		}
		out.PendingTotal++
		if len(out.Pending) < 32 {
			view, err := c.operationViewLocked(stored)
			if err != nil {
				return ServiceStatus{}, err
			}
			out.Pending = append(out.Pending, view)
		} else {
			out.PendingTruncated = true
		}
		out.Degraded = out.Degraded || stored.Record.Phase == Degraded
		switch stored.Record.Identity.Operation {
		case "runtime.stop":
			out.DesiredRuntime = "stopped"
			corePending = true
		case "runtime.start", "config.apply":
			out.DesiredRuntime = "running"
			corePending = true
		case "operation.recover":
			corePending = true
		}
	}
	if base.Generation == nil {
		runs, err := c.store.ListRunIDs()
		if err != nil {
			return ServiceStatus{}, err
		}
		if len(runs) == 0 && !corePending {
			out.Core = CoreStatus{State: "verified-stopped", Reason: "empty_configuration"}
		}
	} else if base.DesiredRuntime == "stopped" {
		if !corePending {
			if _, err := c.verifiedStopped(base); err == nil {
				out.Core = CoreStatus{State: "verified-stopped", Reason: "durable_original_exit"}
			}
		}
	} else if !corePending {
		id, err := c.activeRunForApply(base, "replace", "")
		if errors.Is(err, config.ErrReadOnly) {
			out.Core.Reason = "activation_requires_management"
		}
		if err == nil && id != nil {
			identity, err := c.verifyRecoveredRun(ctx, *id, *base.OperationID)
			if err == nil {
				out.Core = CoreStatus{State: "verified-running", Reason: "fresh_original_proof", RunID: id, PID: &identity.PID, StartTicks: &identity.StartTicks, BootID: &identity.BootID}
			} else if errors.Is(err, config.ErrReadOnly) {
				// Activation verification deliberately requires the live writer
				// capability. Offline readers must not acquire it for a query.
				out.Core.Reason = "activation_requires_management"
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ServiceStatus{}, err
	}
	if out.Degraded {
		out.DegradedReasons = append(out.DegradedReasons, "operation_degraded")
	}
	if out.Core.State == "unknown" {
		out.Degraded = true
		out.DegradedReasons = append(out.DegradedReasons, "core_state_unverified")
	}
	if out.SourceSchedule != nil && out.SourceSchedule.State == "degraded" {
		out.Degraded = true
		out.DegradedReasons = append(out.DegradedReasons, "source_schedule_failed")
	}
	current, err := c.store.ReadActive()
	if err != nil || !reflect.DeepEqual(base, current) {
		return ServiceStatus{}, errors.Join(config.ErrReadUnavailable, err)
	}
	out.ObservedAt = time.Now().UTC()
	return out, nil
}

// Lookup only reads the exact durable request identity; it cannot admit work.
func (c *Coordinator) Lookup(raw []byte) (StoredOperation, error) {
	r, err := ParseRequest(raw)
	if err != nil {
		return StoredOperation{}, err
	}
	if c == nil || c.coordinatorState == nil {
		return StoredOperation{}, config.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		return StoredOperation{}, config.ErrClosed
	}
	return c.journal.Lookup(r)
}

// ExitObservationApplies validates the authority surrounding a retained
// RestartWatch exit fact. It neither observes a new exit nor stores that fact.
func (c *Coordinator) ExitObservationApplies(id config.RunID) bool {
	if c == nil || c.coordinatorState == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.noPendingLifecycle() != nil {
		return false
	}
	base, err := c.store.ReadActive()
	if err != nil || base.DesiredRuntime != "running" || base.OperationID == nil {
		return false
	}
	matched, err := c.activeRunForApply(base, "replace", "")
	if err != nil || matched == nil || *matched != id {
		return false
	}
	_, err = c.stoppedRunOriginals(id, *base.OperationID, "closed")
	return err == nil
}

func readCoordinator(paths config.Paths) (*Coordinator, error) {
	store, err := config.OpenStore(paths, false)
	if err != nil {
		return nil, err
	}
	j, err := NewJournal(store)
	if err != nil {
		store.Close()
		return nil, err
	}
	return &Coordinator{&coordinatorState{store: store, journal: j}}, nil
}

func ReadServiceStatus(ctx context.Context, paths config.Paths) (out ServiceStatus, err error) {
	c, err := readCoordinator(paths)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return c.Status(ctx)
}

func (c *Coordinator) Operation(id config.ID) (OperationView, error) {
	if c == nil || c.coordinatorState == nil {
		return OperationView{}, config.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		return OperationView{}, config.ErrClosed
	}
	s, err := c.journal.Get(id)
	if err != nil {
		return OperationView{}, err
	}
	return c.operationViewLocked(s)
}

func ReadOperation(paths config.Paths, id config.ID) (out OperationView, err error) {
	c, err := readCoordinator(paths)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return c.Operation(id)
}

func ReadReplay(paths config.Paths, raw []byte) (out StoredOperation, err error) {
	r, err := ParseRequest(raw)
	if err != nil {
		return out, err
	}
	c, err := readCoordinator(paths)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return c.journal.Lookup(r)
}
