//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"
	"time"

	"hoopa/internal/config"
)

type DoctorCheck struct {
	ID             string `json:"id"`
	Result         string `json:"result"`
	Evidence       string `json:"evidence"`
	Reason         string `json:"reason"`
	Recommendation string `json:"recommendation"`
}

type DoctorReport struct {
	SchemaVersion int           `json:"schema_version"`
	ObservedAt    time.Time     `json:"observed_at"`
	Checks        []DoctorCheck `json:"checks"`
}

// Doctor uses exactly the existing read-only status observation. Recommendations
// are inert finite labels; neither this query nor its caller executes them.
func (c *Coordinator) Doctor(ctx context.Context) (DoctorReport, error) {
	if ctx == nil {
		return DoctorReport{}, ErrRequest
	}
	status, err := c.Status(ctx)
	if err != nil {
		return DoctorReport{}, err
	}
	return doctorProjection(status), nil
}

func doctorProjection(s ServiceStatus) DoctorReport {
	at := s.ObservedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	checks := []DoctorCheck{
		{"state", "pass", "protected_local_files", "state_readable", "none"},
		{"configuration", "pass", "protected_local_files", "committed_snapshot", "none"},
		{"service", "warning", "not_observed", "service_unavailable", "start_control_service"},
		{"core", "unavailable", "not_observed", "ownership_unverified", "inspect_runtime_status"},
		{"collector", "unavailable", "not_observed", "not_implemented", "not_available_in_current_phase"},
		{"operations", "pass", "durable_operation_journal", "no_pending_operations", "none"},
	}
	if s.Generation == nil {
		checks[1] = DoctorCheck{"configuration", "warning", "protected_local_files", "no_committed_configuration", "config_plan_apply"}
	}
	if s.Service == "running" {
		checks[2] = DoctorCheck{"service", "pass", "current_management_service", "service_running", "none"}
	} else if s.Service == "quiesced" {
		checks[2] = DoctorCheck{"service", "warning", "current_management_service", "service_quiesced", "start_control_service"}
	}
	switch {
	case s.Core.State == "verified-running" && s.Core.Reason == "fresh_original_proof":
		checks[3] = DoctorCheck{"core", "pass", "fresh_core_identity", "fresh_original_proof", "none"}
	case s.Core.State == "verified-stopped" && s.Core.Reason == "empty_configuration":
		checks[3] = DoctorCheck{"core", "pass", "protected_local_files", "empty_configuration", "config_plan_apply"}
	case s.Core.State == "verified-stopped" && s.Core.Reason == "durable_original_exit":
		checks[3] = DoctorCheck{"core", "pass", "durable_operation_journal", "durable_original_exit", "none"}
	case s.Core.Reason == "activation_requires_management":
		checks[3].Reason = "activation_requires_management"
		checks[3].Recommendation = "start_management_for_verification"
	}
	if s.PendingTotal > 0 {
		checks[5] = DoctorCheck{"operations", "warning", "durable_operation_journal", "pending_operations", "inspect_operations"}
	}
	return DoctorReport{SchemaVersion: 1, ObservedAt: at, Checks: checks}
}

// ReadDoctor cannot distinguish a fresh instance from deleted authority. ENOENT
// is a diagnostic fact only; it never authorizes initialization or repair.
func ReadDoctor(ctx context.Context, paths config.Paths) (DoctorReport, error) {
	if ctx == nil {
		return DoctorReport{}, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return DoctorReport{}, err
	}
	status, err := ReadServiceStatus(ctx, paths)
	if err == nil {
		return doctorProjection(status), nil
	}
	if cancel := ctx.Err(); cancel != nil {
		return DoctorReport{}, cancel
	}
	if !errors.Is(err, os.ErrNotExist) || errors.Is(err, config.ErrUnsafePath) || errors.Is(err, config.ErrIntegrity) {
		return DoctorReport{}, err
	}
	report := doctorProjection(ServiceStatus{})
	for i := range report.Checks {
		report.Checks[i].Result = "unavailable"
		report.Checks[i].Evidence = "not_observed"
		report.Checks[i].Reason = "state_unavailable"
		report.Checks[i].Recommendation = "inspect_instance_paths"
	}
	report.Checks[0].Evidence = "protected_local_files"
	report.Checks[0].Reason = "state_missing_or_incomplete"
	// Lack of the collector implementation is known independently of state.
	report.Checks[4] = DoctorCheck{"collector", "unavailable", "not_observed", "not_implemented", "not_available_in_current_phase"}
	return report, nil
}
