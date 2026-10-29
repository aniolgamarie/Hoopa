package policy

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

// ErrRules is a constant, input-free failure classification. Detailed formatting
// is deliberately identical to Error; no parser/provider error is retained.
var ErrRules error = ruleFailure{}

type ruleFailure struct{}

func (ruleFailure) Error() string    { return "RULES_INVALID" }
func (ruleFailure) String() string   { return "RULES_INVALID" }
func (ruleFailure) GoString() string { return "RULES_INVALID" }

// RuleValue is supplied evidence. An unavailable value is ignored completely,
// including any stale or malformed text in Value.
type RuleValue struct {
	Known bool
	Value string
}
type RuleFacts struct {
	AppID, ProcessInstanceID, ProcessName, ProcessPath RuleValue
	Domain, IP                                         RuleValue
}
type RuleTruth string

const (
	RuleTrue    RuleTruth = "true"
	RuleFalse   RuleTruth = "false"
	RuleUnknown RuleTruth = "unknown"
)

type RuleDecision struct {
	Outcome       string
	RuleIndex     int
	RuleID        *config.ID
	Target        config.Target
	UnknownFields []string
}

// RuleSet owns parsed rules only. It retains no source snapshots, provider
// material, probes, runtime choices or caller-owned mutable references. Copies
// share immutable private state; every returned decision is detached.
type RuleSet struct {
	valid         bool
	rules         []config.Rule
	defaultTarget config.Target
}

// NewRuleSet performs all local admission before calculation: strict Apply
// parsing, authentic referenced snapshots, complete groups and exact node pins.
// It never opens declared paths, probes nodes or adds a runtime capability.
func NewRuleSet(raw []byte, sources map[config.ID]subscription.Snapshot) (RuleSet, error) {
	intent, err := admitRuleIntent(raw, sources)
	if err != nil {
		return RuleSet{}, ErrRules
	}
	// Admission owns the parsed rule tree and discards all source authority.
	return RuleSet{valid: true, rules: intent.Rules, defaultTarget: intent.DefaultTarget}, nil
}

// Match evaluates only the selected rule's predicate, without using or guessing
// facts for earlier rules. Its constructor has already validated every target.
func (s RuleSet) Match(index int, facts RuleFacts) (RuleTruth, error) {
	if !s.valid || index < 0 || index >= len(s.rules) {
		return "", ErrRules
	}
	truth, _, err := matchRulePredicate(s.rules[index].Match, facts)
	if err != nil {
		return "", ErrRules
	}
	return truth, nil
}

// Evaluate returns the first decisive array entry. Unknown evidence blocks at
// its own rule with an explicit reject instead of falling through or selecting
// a current member of the intended group.
func (s RuleSet) Evaluate(facts RuleFacts) (RuleDecision, error) {
	if !s.valid {
		return RuleDecision{}, ErrRules
	}
	for index, rule := range s.rules {
		truth, missing, err := matchRulePredicate(rule.Match, facts)
		if err != nil {
			return RuleDecision{}, ErrRules
		}
		if truth == RuleFalse {
			continue
		}
		id := rule.ID
		decision := RuleDecision{Outcome: "matched", RuleIndex: index, RuleID: &id, Target: cloneRuleTarget(rule.Target), UnknownFields: []string{}}
		if truth == RuleUnknown {
			decision.Outcome = "blocked"
			decision.Target = config.Target{Kind: "reject"}
			decision.UnknownFields = ruleMissingFields(missing)
		}
		return decision, nil
	}
	return RuleDecision{Outcome: "default", RuleIndex: -1, Target: cloneRuleTarget(s.defaultTarget), UnknownFields: []string{}}, nil
}

func cloneRuleTarget(target config.Target) config.Target {
	if target.GroupID != nil {
		value := *target.GroupID
		target.GroupID = &value
	}
	if target.SubscriptionID != nil {
		value := *target.SubscriptionID
		target.SubscriptionID = &value
	}
	if target.NodeID != nil {
		value := *target.NodeID
		target.NodeID = &value
	}
	return target
}

type ruleMissing uint8

const (
	missingRuleApp ruleMissing = 1 << iota
	missingRuleDomain
	missingRuleIP
	missingRuleInstance
	missingRuleName
	missingRulePath
)

func ruleMissingFields(missing ruleMissing) []string {
	// Fixed lexical order also deduplicates fields repeated in nested all nodes.
	names := [...]string{"app_id", "domain", "ip", "process_instance_id", "process_name", "process_path"}
	out := []string{}
	for i, name := range names {
		if missing&(1<<i) != 0 {
			out = append(out, name)
		}
	}
	return out
}
func ruleEqual(left, right string) (RuleTruth, ruleMissing, error) {
	if left == right {
		return RuleTrue, 0, nil
	}
	return RuleFalse, 0, nil
}
func matchRulePredicate(p config.Predicate, facts RuleFacts) (RuleTruth, ruleMissing, error) {
	if p.Kind == "all" {
		if len(p.Conditions) == 0 {
			return "", 0, ErrRules
		}
		hasFalse := false
		var missing ruleMissing
		for _, child := range p.Conditions {
			truth, fields, err := matchRulePredicate(child, facts)
			// A false sibling suppresses unknown, never malformed known input. Visit
			// every child unless the result is already an input error.
			if err != nil {
				return "", 0, ErrRules
			}
			if truth == RuleFalse {
				hasFalse = true
			} else if truth == RuleUnknown {
				missing |= fields
			}
		}
		if hasFalse {
			return RuleFalse, 0, nil
		}
		if missing != 0 {
			return RuleUnknown, missing, nil
		}
		return RuleTrue, 0, nil
	}
	var value RuleValue
	var field ruleMissing
	switch p.Kind {
	case "app":
		value, field = facts.AppID, missingRuleApp
	case "process_instance":
		value, field = facts.ProcessInstanceID, missingRuleInstance
	case "process_name":
		value, field = facts.ProcessName, missingRuleName
	case "process_path":
		value, field = facts.ProcessPath, missingRulePath
	case "domain":
		value, field = facts.Domain, missingRuleDomain
	case "ip_cidr":
		value, field = facts.IP, missingRuleIP
	default:
		return "", 0, ErrRules
	}
	if !value.Known {
		return RuleUnknown, field, nil
	}
	switch p.Kind {
	case "app":
		if value.Value == "" {
			return RuleFalse, 0, nil
		}
		if _, err := config.ParseID(value.Value); err != nil || p.AppID == nil {
			return "", 0, ErrRules
		}
		return ruleEqual(value.Value, string(*p.AppID))
	case "process_instance":
		if _, err := config.ParseID(value.Value); err != nil || p.ProcessInstanceID == nil {
			return "", 0, ErrRules
		}
		return ruleEqual(value.Value, string(*p.ProcessInstanceID))
	case "process_name", "process_path":
		// Pure facts intentionally match the parser's text boundary: nonempty valid
		// UTF8, with no additional control-character filter. This validates supplied
		// text only; it does not qualify it as evidence of a real OS process.
		if value.Value == "" || !utf8.ValidString(value.Value) {
			return "", 0, ErrRules
		}
		return ruleEqual(value.Value, p.Value)
	case "domain":
		if value.Value == "" {
			return RuleFalse, 0, nil
		}
		domain, ok := normalizeRuleDomain(value.Value)
		if !ok {
			return "", 0, ErrRules
		}
		expected, _ := normalizeRuleDomain(p.Value) // Apply parsed the predicate already.
		match := domain == expected
		if p.Operator == "suffix" {
			match = match || strings.HasSuffix(domain, "."+expected)
		}
		if match {
			return RuleTrue, 0, nil
		}
		return RuleFalse, 0, nil
	case "ip_cidr":
		address, err := netip.ParseAddr(value.Value)
		if err != nil {
			return "", 0, ErrRules
		}
		prefix, err := netip.ParsePrefix(p.Value)
		if err != nil {
			return "", 0, ErrRules
		}
		if prefix.Masked().Contains(address) {
			return RuleTrue, 0, nil
		}
		return RuleFalse, 0, nil
	}
	return "", 0, ErrRules
}

// Domain evidence uses the same ASCII DNS grammar/length as Apply predicates.
// Strip exactly one terminal dot; an extra dot remains an invalid empty label.
func normalizeRuleDomain(value string) (string, bool) {
	if len(value) > 253 {
		return "", false
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	return strings.ToLower(value), true
}
