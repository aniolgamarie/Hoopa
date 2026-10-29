package policy

import (
	"encoding/json"
	"net/netip"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

// RuleExplanation describes configuration intent and, optionally, a calculation
// from supplied facts. It is never an executable set or observed routing proof.
type RuleExplanation struct {
	Validation       string          `json:"validation"`
	Basis            string          `json:"basis"`
	Evidence         string          `json:"evidence"`
	Rules            []ExplainedRule `json:"rules"`
	DefaultTarget    config.Target   `json:"default_target"`
	Blocker          *RuleBlocker    `json:"blocker,omitempty"`
	Calculation      *RuleDecision   `json:"calculation,omitempty"`
	CalculationBasis string          `json:"calculation_basis,omitempty"`
	ShadowAnalysis   string          `json:"shadow_analysis"`
}

func (RuleExplanation) String() string   { return "policy.RuleExplanation{private material}" }
func (RuleExplanation) GoString() string { return "policy.RuleExplanation{private material}" }

type ExplainedRule struct {
	Index       int           `json:"index"`
	ID          config.ID     `json:"id"`
	Target      config.Target `json:"target"`
	Fields      []string      `json:"fields"`
	ShadowedBy  *int          `json:"shadowed_by,omitempty"`
	ShadowProof string        `json:"shadow_proof"`
}
type RuleBlocker struct {
	Reason string `json:"reason"`
	Field  string `json:"field"`
}

// ExplainRules never discovers facts, authenticates an installed core, or reads
// runtime state. A nil error with Validation=invalid means analysis succeeded,
// not admission. Malformed declarations/facts return zero and ErrRules.
func ExplainRules(raw []byte, sources map[config.ID]subscription.Snapshot, facts *RuleFacts) (RuleExplanation, error) {
	draft, err := config.ParseIntent(raw, config.IntentDraft)
	if err != nil || !validExplainScalars(draft) {
		return RuleExplanation{}, ErrRules
	}
	intent, err := admitRuleIntent(raw, sources)
	var blocker *RuleBlocker
	if err != nil {
		reason, field, ok := FailureDetails(err)
		if !ok {
			return RuleExplanation{}, ErrRules
		}
		blocker = &RuleBlocker{Reason: reason, Field: field}
		intent = draft
	}
	report := RuleExplanation{Validation: "valid", Basis: "configuration_intent", Evidence: "not_observed", Rules: make([]ExplainedRule, 0, len(intent.Rules)), DefaultTarget: cloneRuleTarget(intent.DefaultTarget), Blocker: blocker, ShadowAnalysis: "not_performed"}
	for index, rule := range intent.Rules {
		_, fields, _ := matchRulePredicate(rule.Match, RuleFacts{})
		report.Rules = append(report.Rules, ExplainedRule{Index: index, ID: rule.ID, Target: cloneRuleTarget(rule.Target), Fields: ruleMissingFields(fields), ShadowProof: "not_proven"})
	}
	if blocker != nil {
		report.Validation = "invalid"
		return report, nil
	}
	if facts != nil {
		set := RuleSet{valid: true, rules: intent.Rules, defaultTarget: intent.DefaultTarget}
		decision, err := set.Evaluate(*facts)
		if err != nil {
			return RuleExplanation{}, ErrRules
		}
		report.Calculation = &decision
		report.CalculationBasis = "supplied_facts"
	}
	report.ShadowAnalysis = explainShadows(intent.Rules, report.Rules)
	return report, nil
}

// Draft deliberately defers these three scalar constraints. Check them before
// interpreting reference errors, so a simultaneous missing reference cannot
// turn a malformed declaration into a successful diagnostic report.
func validExplainScalars(intent config.Intent) bool {
	for _, group := range intent.Groups {
		if group.Probe != nil && group.Probe.Validate() != nil {
			return false
		}
	}
	var valid func(config.Predicate) bool
	valid = func(p config.Predicate) bool {
		switch p.Kind {
		case "domain":
			if _, ok := normalizeRuleDomain(p.Value); !ok {
				return false
			}
		case "ip_cidr":
			if _, err := netip.ParsePrefix(p.Value); err != nil {
				return false
			}
		}
		for _, child := range p.Conditions {
			if !valid(child) {
				return false
			}
		}
		return true
	}
	for _, rule := range intent.Rules {
		if !valid(rule.Match) {
			return false
		}
	}
	return true
}

// admitRuleIntent is the single pure admission boundary shared by NewRuleSet
// and explanation. Only fixed safe diagnostics escape. All referenced source
// authority is checked before groups inspect metadata; no partial set escapes.
func admitRuleIntent(raw []byte, sources map[config.ID]subscription.Snapshot) (config.Intent, error) {
	intent, err := config.ParseIntent(raw, config.IntentApply)
	if err != nil {
		return config.Intent{}, IntentReferenceFailure(raw)
	}
	type reference struct {
		id    config.ID
		field string
	}
	refs := []reference{}
	seen := map[config.ID]bool{}
	add := func(id config.ID, field string) {
		if !seen[id] {
			seen[id] = true
			refs = append(refs, reference{id, field})
		}
	}
	if active := intent.Selection.ActiveSubscription; active != nil {
		add(*active, "selection.active_subscription")
	}
	for _, group := range intent.Groups {
		if group.Source == nil {
			continue
		}
		if group.Source.Kind == "active" {
			if intent.Selection.ActiveSubscription == nil {
				return config.Intent{}, compileDiagnostic("active_source_unset", groupField(group.ID)+".source")
			}
			add(*intent.Selection.ActiveSubscription, groupField(group.ID)+".source")
		} else if group.Source.SubscriptionID != nil {
			add(*group.Source.SubscriptionID, groupField(group.ID)+".source")
		}
	}
	addTarget := func(target config.Target, field string) {
		if target.SubscriptionID != nil {
			add(*target.SubscriptionID, field)
		}
	}
	for _, rule := range intent.Rules {
		addTarget(rule.Target, ruleField(rule.ID)+".target")
	}
	addTarget(intent.DefaultTarget, "default_target")
	for _, ref := range refs {
		snapshot, ok := sources[ref.id]
		if !ok || snapshot.SourceID != ref.id || snapshot.Validate() != nil {
			return config.Intent{}, compileDiagnostic("source_unavailable", ref.field)
		}
	}
	groups, err := ResolveGroups(intent.Selection.ActiveSubscription, intent.Groups, sources)
	if err != nil {
		return config.Intent{}, err
	}
	targetValid := func(target config.Target, field string) error {
		switch target.Kind {
		case "direct", "reject":
			return nil
		case "group":
			if target.GroupID != nil {
				if _, ok := groups[*target.GroupID]; ok {
					return nil
				}
			}
			return compileDiagnostic("target_group_missing", field)
		case "node":
			if target.SubscriptionID != nil && target.NodeID != nil {
				for _, node := range sources[*target.SubscriptionID].Nodes {
					if node.ID == *target.NodeID {
						return nil
					}
				}
			}
			return compileDiagnostic("pinned_node_missing", field)
		}
		return ErrRules
	}
	for _, rule := range intent.Rules {
		if err := targetValid(rule.Target, ruleField(rule.ID)+".target"); err != nil {
			return config.Intent{}, err
		}
	}
	if err := targetValid(intent.DefaultTarget, "default_target"); err != nil {
		return config.Intent{}, err
	}
	return intent, nil
}

const explainMaxPairs = 4096
const explainMaxAtoms = 65536

type explainBudget struct{ pairs, atoms int }

func (b *explainBudget) atom() bool {
	if b.atoms >= explainMaxAtoms {
		return false
	}
	b.atoms++
	return true
}
func (b *explainBudget) pair() bool {
	if b.pairs >= explainMaxPairs {
		return false
	}
	b.pairs++
	return true
}

type explainAtoms struct {
	ordered []string
	set     map[string]bool
}

// Normalized exact atoms are transient proof keys, never report material.
// Flattening/deduplication is for proof only; the evaluator retains all children
// and still validates malformed known facts even beside a false sibling.
func normalizedExplainAtoms(p config.Predicate, budget *explainBudget) (explainAtoms, bool) {
	atoms := explainAtoms{set: map[string]bool{}}
	var visit func(config.Predicate) bool
	visit = func(p config.Predicate) bool {
		if !budget.atom() {
			return false
		}
		if p.Kind == "all" {
			for _, child := range p.Conditions {
				if !visit(child) {
					return false
				}
			}
			return true
		}
		parts := []string{p.Kind}
		switch p.Kind {
		case "app":
			parts = append(parts, string(*p.AppID))
		case "process_instance":
			parts = append(parts, string(*p.ProcessInstanceID))
		case "domain":
			domain, _ := normalizeRuleDomain(p.Value)
			parts = append(parts, p.Operator, domain)
		case "ip_cidr":
			prefix, _ := netip.ParsePrefix(p.Value)
			parts = append(parts, prefix.Masked().String())
		default:
			parts = append(parts, p.Value)
		}
		key, _ := json.Marshal(parts)
		text := string(key)
		if !atoms.set[text] {
			atoms.set[text] = true
			atoms.ordered = append(atoms.ordered, text)
		}
		return true
	}
	ok := visit(p)
	return atoms, ok
}
func explainShadows(rules []config.Rule, rows []ExplainedRule) string {
	budget := explainBudget{}
	atoms := make([]explainAtoms, 0, len(rules))
	for i, rule := range rules {
		current, ok := normalizedExplainAtoms(rule.Match, &budget)
		if !ok {
			return "budget_exhausted"
		}
		atoms = append(atoms, current)
		for j := 0; j < i; j++ {
			if !budget.pair() {
				return "budget_exhausted"
			}
			if len(atoms[j].ordered) > len(current.ordered) {
				continue
			}
			subset := true
			for _, atom := range atoms[j].ordered {
				if !budget.atom() {
					return "budget_exhausted"
				}
				if !current.set[atom] {
					subset = false
					break
				}
			}
			if subset {
				previous := j
				rows[i].ShadowedBy = &previous
				rows[i].ShadowProof = "conjunction_subset"
				if len(atoms[j].ordered) == len(current.ordered) {
					rows[i].ShadowProof = "duplicate"
				}
				break
			}
		}
	}
	return "complete"
}
