# Ordered rule calculation v1

Root interface decision for T045/T047, 2026-10-28. This is a pure calculation contract and test-definition input. It is not a new runtime capability, listener allocation mechanism or declaration that US4 has shipped. T044's actual source matrix is complete; named application ingress and its partial-bind recovery remain T046/T048/T051.

## Input and authority

`policy.NewRuleSet(intent []byte, sources map[config.ID]subscription.Snapshot) (RuleSet, error)` strictly parses the complete Intent in Apply mode. It resolves the declared logical groups and validates every referenced source and every rule/default target before returning a usable value. Referenced snapshots must pass their existing pure validation; unreferenced map entries are not dependencies. A missing pin, source or required group fails the complete constructor, including a target on a rule that would later be shadowed. No branch selects an arbitrary first node, guesses a region or substitutes a default after target failure.

The immutable set owns only the parsed rule/target information needed for calculation; caller mutation of the original bytes, snapshots or returned decisions must not change it. It does not retain provider secrets for explanation, open files, select a runtime auto-group member, probe, allocate an endpoint or invoke a controller. Failure is `ErrRules` with constant text `RULES_INVALID`, no partial set/decision or private input text. The zero set and an out-of-range rule index are invalid.

The initial internal Go interfaces are:

```go
type RuleValue struct { Known bool; Value string }
type RuleFacts struct {
    AppID, ProcessInstanceID, ProcessName, ProcessPath RuleValue
    Domain, IP RuleValue
}
type RuleTruth string // "true", "false", "unknown"
const RuleTrue RuleTruth = "true"
const RuleFalse RuleTruth = "false"
const RuleUnknown RuleTruth = "unknown"
type RuleDecision struct {
    Outcome string // "matched", "default", "blocked"
    RuleIndex int // -1 only for default
    RuleID *config.ID // nil only for default
    Target config.Target
    UnknownFields []string // sorted, unique logical field names
}
func (s RuleSet) Match(ruleIndex int, facts RuleFacts) (RuleTruth, error)
func (s RuleSet) Evaluate(facts RuleFacts) (RuleDecision, error)
```

Facts are supplied evidence, not discovered facts. `Known=false` means unavailable; any stale Value must be ignored. Known empty AppID means the entry is positively known to have no app association; known empty Domain means a positively observed IP-only request. Both match their corresponding predicates as false. These states differ from unavailable app/domain evidence. Known nonempty app/instance identifiers use the common ID grammar; known process name/path are nonempty valid text; known IP must parse as an address. Malformed known evidence used by the evaluated predicate returns `ErrRules`, never a permissive false or a match. Unused facts do not become dependencies. Missing process fields remain unknown; a launcher/profile/entry label cannot supply them.

## Order and three-valued predicates

Only the Intent's existing app, process_instance, process_name, process_path, domain exact/suffix, ip_cidr and nonempty all forms are accepted. The parser's maximum nesting depth of 16 and exact union shapes remain authoritative. There is no implicit specificity ordering or app-level target override.

Name/path and app/instance equality are case-sensitive. Domain comparison lowercases ASCII and removes one terminal dot; suffix matches the domain itself or a dot-delimited descendant, never an arbitrary string suffix. IP matching uses parsed addresses and network prefixes. Pure calculation may represent address families that a particular core profile cannot compile; this does not broaden that profile.

For all, any definite false dominates unknown regardless of child order; all true is true; otherwise the result is unknown. Evaluation cannot stop on the first unknown before inspecting later children for a definite false. Unknown fields are deduplicated and lexically sorted using only `app_id`, `process_instance_id`, `process_name`, `process_path`, `domain`, and `ip`.

Malformed known evidence is an input error, not a fourth truth-table operand or an unknown observation. Every child of an evaluated all validates the evidence it uses; a false sibling does not hide that error in either order. Fields not referenced by the evaluated predicate remain unused. Evidence providers must represent unavailable or invalid observations as Known=false instead of asserting that malformed data is known.

Evaluate visits rules in array order. A true rule returns `matched` with its original index/ID and exact target, including explicit reject. A false rule permits the next rule. An unknown rule that has not been suppressed by a definite false inside all returns `blocked`, its rule index/ID, an explicit reject target and the missing logical fields; it does not fall through to a later true rule or the default. If all rules are false, it returns `default`, index -1, nil ID and the exact explicit default target. Returned targets describe intended selection, not an actual established connection or the current member of an auto group.

## Capability boundary and tests

The pure evaluator can calculate process predicates from supplied test facts. Production compilation must still reject each unsupported process field before accepting a configuration, even when a particular fixture fact would make that rule false. The currently approved sources-v2 profile has process lookup disabled and must not gain original-process support from this module or P0 evidence. App context will be derived from a verified named ingress in T048, never treated as a source PID.

T045 defines constructor refusal, immutable results, exact target binding, array order/default, domain/IP overlap, unknown-before-later-true blocking, every all truth-table row in both orders, nesting/shape boundaries, and P1 process-capability rejection. These tests are expected to be red until T047 exists; syntax/test-definition validation is distinct from semantic acceptance. Static shadow explanations and richer impact are T049/T050; actual application/node routes and partial-bind rollback are T046/T051. No test-definition completion proves those runtime requirements.
