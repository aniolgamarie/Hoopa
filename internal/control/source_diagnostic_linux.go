//go:build linux && amd64

package control

import (
	"errors"

	"hoopa/internal/config"
	"hoopa/internal/policy"
)

// A diagnostic is an explanation of a refusal, never a partially usable Plan.
// Formatting preserves the old fixed class text; only the explicit transport
// accessor exposes bounded reason/field values, never original input/errors.
type sourcePlanFailure struct {
	class  error
	reason string
	field  string
}

func (e *sourcePlanFailure) Error() string    { return e.class.Error() }
func (e *sourcePlanFailure) String() string   { return e.Error() }
func (e *sourcePlanFailure) GoString() string { return e.Error() }
func (e *sourcePlanFailure) Unwrap() error    { return e.class }

func sourcePlanReason(reason string) bool {
	switch reason {
	case "required_group_empty", "required_group_no_region_match", "selected_node_missing", "selected_node_region_mismatch", "source_unavailable", "active_source_unset", "follow_target_missing", "follow_cycle", "follow_region_mismatch", "pinned_node_missing", "target_group_missing", "source_reference_missing", "app_reference_missing", "source_snapshot_missing", "source_definition_changed", "source_candidate_empty", "source_candidate_invalid":
		return true
	}
	return false
}
func sourcePlanDetail(class error, reason, field string) error {
	if !sourcePlanReason(reason) || len(field) == 0 || len(field) > 512 {
		return class
	}
	return &sourcePlanFailure{class: class, reason: reason, field: field}
}
func sourcePlanPolicyFailure(err error) error {
	if reason, field, ok := policy.FailureDetails(err); ok {
		return sourcePlanDetail(ErrPlan, reason, field)
	}
	return ErrPlan
}
func sourcePlanIntentFailure(raw []byte) error {
	return sourcePlanPolicyFailure(policy.IntentReferenceFailure(raw))
}
func sourcePlanSourceFailure(id config.ID, reason string) error {
	return sourcePlanDetail(config.ErrReadUnavailable, reason, "subscriptions["+string(id)+"]")
}

// SourcePlanFailureDetails returns detached, explicitly safe error details. It
// does not infer diagnostics from arbitrary error strings or callback methods.
func SourcePlanFailureDetails(err error) (reason, field string, ok bool) {
	var detail *sourcePlanFailure
	if !errors.As(err, &detail) || detail == nil || !sourcePlanReason(detail.reason) || len(detail.field) == 0 || len(detail.field) > 512 {
		return "", "", false
	}
	return detail.reason, detail.field, true
}
