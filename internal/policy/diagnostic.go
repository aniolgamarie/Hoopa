package policy

import (
	"errors"
	"strconv"

	"hoopa/internal/config"
)

var ErrCompile = errors.New("SOURCE_COMPILE_INVALID")

// A failure retains only fixed classifications and bounded logical paths. It
// never retains a provider, parsed Intent, probe, endpoint or underlying error.
type policyFailure struct {
	compiler      bool
	reason, field string
}

func (f *policyFailure) base() error {
	if f.compiler {
		return ErrCompile
	}
	return ErrGroups
}
func (f *policyFailure) Error() string    { return f.base().Error() }
func (f *policyFailure) String() string   { return f.Error() }
func (f *policyFailure) GoString() string { return f.Error() }
func (f *policyFailure) Unwrap() error    { return f.base() }
func policyDiagnostic(compiler bool, reason, field string) error {
	if len(reason) == 0 || len(reason) > 64 || len(field) == 0 || len(field) > 512 {
		if compiler {
			return ErrCompile
		}
		return ErrGroups
	}
	return &policyFailure{compiler: compiler, reason: reason, field: field}
}
func groupDiagnostic(reason, field string) error   { return policyDiagnostic(false, reason, field) }
func compileDiagnostic(reason, field string) error { return policyDiagnostic(true, reason, field) }
func compileGroupDiagnostic(err error) error {
	if reason, field, ok := FailureDetails(err); ok {
		return compileDiagnostic(reason, field)
	}
	return ErrCompile
}

// FailureDetails recognizes only this package's private diagnostic type,
// including ordinary wrapping; arbitrary error text is never interpreted.
func FailureDetails(err error) (reason string, field string, ok bool) {
	var failure *policyFailure
	if !errors.As(err, &failure) || failure == nil {
		return "", "", false
	}
	return failure.reason, failure.field, true
}
func groupField(id config.ID) string { return "groups[" + string(id) + "]" }
func ruleField(id config.ID) string  { return "rules[" + string(id) + "]" }

// IntentReferenceFailure explains only a strictly parsed draft's missing local
// references after Apply rejected it. It never replaces the parser's admission
// decision, accepts malformed declarations, resolves nodes or performs IO.
func IntentReferenceFailure(raw []byte) error {
	if _, err := config.ParseIntent(raw, config.IntentApply); err == nil {
		return nil
	}
	intent, err := config.ParseIntent(raw, config.IntentDraft)
	if err != nil {
		return ErrCompile
	}
	sources, groups, apps := map[config.ID]bool{}, map[config.ID]bool{}, map[config.ID]bool{}
	for _, s := range intent.Subscriptions {
		sources[s.ID] = true
	}
	for _, g := range intent.Groups {
		groups[g.ID] = true
	}
	for _, a := range intent.Apps {
		apps[a.ID] = true
	}
	if active := intent.Selection.ActiveSubscription; active != nil && !sources[*active] {
		return compileDiagnostic("source_reference_missing", "selection.active_subscription")
	}
	for _, g := range intent.Groups {
		if g.Source != nil {
			switch g.Source.Kind {
			case "active":
				if intent.Selection.ActiveSubscription == nil {
					return compileDiagnostic("active_source_unset", groupField(g.ID)+".source")
				}
			case "subscription":
				if g.Source.SubscriptionID != nil && !sources[*g.Source.SubscriptionID] {
					return compileDiagnostic("source_reference_missing", groupField(g.ID)+".source.subscription_id")
				}
			}
		}
		if g.Follows != nil && !groups[*g.Follows] {
			return compileDiagnostic("follow_target_missing", groupField(g.ID)+".follows")
		}
	}
	if id, ok := followCycleGroup(intent.Groups); ok {
		return compileDiagnostic("follow_cycle", groupField(id)+".follows")
	}
	target := func(t config.Target, field string) error {
		if t.GroupID != nil && !groups[*t.GroupID] {
			return compileDiagnostic("target_group_missing", field)
		}
		if t.SubscriptionID != nil && !sources[*t.SubscriptionID] {
			return compileDiagnostic("source_reference_missing", field)
		}
		return nil
	}
	var predicate func(config.Predicate, string) error
	predicate = func(p config.Predicate, field string) error {
		if p.AppID != nil && !apps[*p.AppID] {
			return compileDiagnostic("app_reference_missing", field+".app_id")
		}
		for i, child := range p.Conditions {
			if err := predicate(child, field+".conditions["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
		return nil
	}
	for _, rule := range intent.Rules {
		if err := target(rule.Target, ruleField(rule.ID)+".target"); err != nil {
			return err
		}
		if err := predicate(rule.Match, ruleField(rule.ID)+".match"); err != nil {
			return err
		}
	}
	if err := target(intent.DefaultTarget, "default_target"); err != nil {
		return err
	}
	return ErrCompile
}

// Find actual cycle members with iterative walks, then choose the first member
// in declaration order. A dependent chain leading into a cycle is not itself
// reported as a cycle edge. Every vertex is visited at most once.
func followCycleGroup(groups []config.Group) (config.ID, bool) {
	follows := map[config.ID]config.ID{}
	for _, g := range groups {
		if g.Strategy == "follow" && g.Follows != nil {
			follows[g.ID] = *g.Follows
		}
	}
	done, cycles := map[config.ID]bool{}, map[config.ID]bool{}
	for _, g := range groups {
		if done[g.ID] {
			continue
		}
		path := []config.ID{}
		positions := map[config.ID]int{}
		id := g.ID
		for {
			if at, ok := positions[id]; ok {
				for _, member := range path[at:] {
					cycles[member] = true
				}
				break
			}
			if done[id] {
				break
			}
			positions[id] = len(path)
			path = append(path, id)
			next, ok := follows[id]
			if !ok {
				break
			}
			id = next
		}
		for _, id := range path {
			done[id] = true
		}
	}
	for _, g := range groups {
		if cycles[g.ID] {
			return g.ID, true
		}
	}
	return "", false
}
