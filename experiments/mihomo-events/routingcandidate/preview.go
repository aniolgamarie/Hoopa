package routingcandidate

import (
	"errors"
	"net/netip"
	"strings"
	"unicode/utf8"
)

var ErrExplanation = errors.New("invalid routing explanation input")

// ProgramText is a supplied connector text projection, not a live identity.
// Unknown requires empty Value. Known empty text is available but cannot match
// the compiler's nonempty process patterns.
type ProgramText struct {
	Known bool
	Value string
}

// Facts are complete supplied routing projections. RuleHost is the exact core
// RuleHost value: no case conversion or resolution is performed. An empty host
// or zero DestinationIP declares observed absence, not missing knowledge.
type Facts struct {
	Entry                    string
	RuleHost                 string
	DestinationIP            netip.Addr
	ProgramName, ProgramPath ProgramText
}

type RuleState string

const (
	StateTrue         RuleState = "true"
	StateFalse        RuleState = "false"
	StateUnknown      RuleState = "unknown"
	StateNotEvaluated RuleState = "not_evaluated"
)

// RuleExplanation retains resolved compile intent even when a rule is skipped.
// StoppedBy names the earlier stopping rule only for not_evaluated rows.
// UnavailableFields explains an unknown result; a false AND needs no fields.
type RuleExplanation struct {
	RuleID            string
	State             RuleState
	IntendedTarget    string
	StoppedBy         string
	UnavailableFields []string
}

// Explanation describes only supplied facts and compile intent. It is not
// evidence of attribution, forwarding, actual route, or global shadowing.
// Only a matched decision has IntendedTarget. All returned slices are owned.
type Explanation struct {
	Schema            string
	Basis             string
	ConfigSHA256      string
	Rules             []RuleExplanation
	StoppingRuleID    string
	Decision          string
	IntendedTarget    string
	UnavailableFields []string
}

// Explain first validates the complete candidate, including unreachable rules,
// then evaluates supplied facts in literal rule order. It performs no I/O and
// never reads live core, process, DNS, counters, or selector state.
func Explain(s Spec, facts Facts) (Explanation, error) {
	candidate, err := Compile(s)
	if err != nil {
		return Explanation{}, ErrExplanation
	}
	if !validFacts(s, facts) {
		return Explanation{}, ErrExplanation
	}
	result := Explanation{Schema: "hoopa-routing-explanation-v1", Basis: "supplied_facts_v1", ConfigSHA256: candidate.ConfigSHA256, Rules: make([]RuleExplanation, 0, len(candidate.Rules))}
	for _, rule := range candidate.Rules {
		row := RuleExplanation{RuleID: rule.ID, IntendedTarget: rule.InternalTarget}
		if result.StoppingRuleID != "" {
			row.State = StateNotEvaluated
			row.StoppedBy = result.StoppingRuleID
		} else {
			row.State, row.UnavailableFields = explainRule(rule, facts)
			switch row.State {
			case StateTrue:
				result.StoppingRuleID = rule.ID
				result.Decision = "matched"
				result.IntendedTarget = rule.InternalTarget
			case StateUnknown:
				result.StoppingRuleID = rule.ID
				result.Decision = "blocked_unknown"
				result.UnavailableFields = append([]string(nil), row.UnavailableFields...)
			}
		}
		result.Rules = append(result.Rules, row)
	}
	return result, nil
}

func validFacts(s Spec, facts Facts) bool {
	declared := false
	for _, entry := range s.Entries {
		if entry.Name == facts.Entry {
			declared = true
			break
		}
	}
	if !declared || !utf8.ValidString(facts.RuleHost) || len(facts.RuleHost) > 253 || facts.DestinationIP != (netip.Addr{}) && !facts.DestinationIP.Is4() {
		return false
	}
	for _, text := range []ProgramText{facts.ProgramName, facts.ProgramPath} {
		if !text.Known && text.Value != "" || !utf8.ValidString(text.Value) || len(text.Value) > 255 {
			return false
		}
	}
	return true
}

func explainRule(rule RuleMetadata, facts Facts) (RuleState, []string) {
	if rule.Match {
		return StateTrue, nil
	}
	unavailable := []string{}
	for _, condition := range rule.Conditions {
		state, field := explainCondition(condition, facts)
		if state == StateFalse {
			return StateFalse, nil
		}
		if state == StateUnknown {
			seen := false
			for _, previous := range unavailable {
				if previous == field {
					seen = true
					break
				}
			}
			if !seen {
				unavailable = append(unavailable, field)
			}
		}
	}
	if len(unavailable) > 0 {
		return StateUnknown, unavailable
	}
	return StateTrue, nil
}

func explainCondition(condition Condition, facts Facts) (RuleState, string) {
	matched := false
	switch condition.Kind {
	case "app":
		matched = facts.Entry == condition.Value
	case "PROCESS-NAME", "PROCESS-PATH":
		text, field := facts.ProgramName, "program_name"
		if condition.Kind == "PROCESS-PATH" {
			text, field = facts.ProgramPath, "program_path"
		}
		if !text.Known {
			return StateUnknown, field
		}
		matched = text.Value == condition.Value
	case "DOMAIN":
		matched = facts.RuleHost == strings.ToLower(condition.Value)
	case "DOMAIN-SUFFIX":
		suffix := strings.ToLower(condition.Value)
		matched = facts.RuleHost == suffix || strings.HasSuffix(facts.RuleHost, "."+suffix)
	case "IP-CIDR":
		// Compile has already validated this IPv4 prefix and emitted no-resolve.
		prefix, _ := netip.ParsePrefix(condition.Value)
		matched = prefix.Contains(facts.DestinationIP)
	}
	if matched {
		return StateTrue, ""
	}
	return StateFalse, ""
}
