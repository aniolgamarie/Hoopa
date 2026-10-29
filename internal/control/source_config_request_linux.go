//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"

	"hoopa/internal/config"
)

// SourcePlanIntent only decodes the public request. It never reads settings,
// sources or a core, and therefore also serves offline schema validation.
func SourcePlanIntent(request Request) ([]byte, error) {
	if request.version != "v1" || request.operation != "config.plan" || !planFields(request.params, "intent") {
		return nil, ErrRequest
	}
	params, _ := planObject(request.params)
	if _, err := config.ParseIntent(params["intent"], config.IntentApply); err != nil {
		return nil, sourcePlanIntentFailure(params["intent"])
	}
	return config.CanonicalJSON(params["intent"])
}

func sourceApplyPlanShape(request Request) (Plan, error) {
	if request.version != "v1" || request.operation != "config.apply" || !planFields(request.params, "plan", "confirmed_plan_digest", "accept_disruption") {
		return Plan{}, ErrRequest
	}
	params, _ := planObject(request.params)
	var confirmation string
	if json.Unmarshal(params["confirmed_plan_digest"], &confirmation) != nil || !isHash(confirmation) || string(params["accept_disruption"]) != "true" && string(params["accept_disruption"]) != "false" {
		return Plan{}, ErrRequest
	}
	return ParsePlan(params["plan"], func(raw json.RawMessage, _ []PlanInput) error {
		if _, err := config.ParseIntent(raw, config.IntentApply); err != nil {
			return ErrPlan
		}
		return nil
	})
}

// ValidateSourceApplyRequest validates shape only. The source applier must still
// independently restore and compile all inputs before admission or any effect.
func ValidateSourceApplyRequest(request Request) error {
	_, err := sourceApplyPlanShape(request)
	return err
}

// ReadSourcePlan opens only existing read handles. A concurrent writer can make
// the returned captured Plan stale; Apply's revision/input checks remain required.
func ReadSourcePlan(ctx context.Context, paths config.Paths, intent []byte) (plan Plan, err error) {
	c, err := readCoordinator(paths)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return c.PlanSources(ctx, intent)
}
