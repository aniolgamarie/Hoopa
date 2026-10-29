//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

type sourceUpdateRequest struct {
	Revision       config.Revision
	SourceID       config.ID
	Intent         *config.Intent
	RegionRules    []subscription.RegionRule
	HasRegionRules bool
}

func (sourceUpdateRequest) String() string { return "control.sourceUpdateRequest{private input}" }
func (sourceUpdateRequest) GoString() string {
	return "control.sourceUpdateRequest{private input}"
}

// This validates a declaration only. It does not read an Intent file or secret,
// choose a fallback source, publish a draft, or authorize a download.
func parseSourceUpdate(r Request) (sourceUpdateRequest, error) {
	bad := func() (sourceUpdateRequest, error) { return sourceUpdateRequest{}, ErrRequest }
	if r.version != "v1" || r.operation != "subscription.update" || len(r.raw) == 0 {
		return bad()
	}
	p, err := planObject(r.params)
	if err != nil || len(p) < 2 || len(p) > 4 {
		return bad()
	}
	for k := range p {
		if k != "subscription_id" && k != "expected_revision" && k != "intent" && k != "region_rules" {
			return bad()
		}
	}
	var source, revision string
	if json.Unmarshal(p["subscription_id"], &source) != nil || json.Unmarshal(p["expected_revision"], &revision) != nil {
		return bad()
	}
	id, err := config.ParseID(source)
	if err != nil {
		return bad()
	}
	if _, err = config.ParseID(revision); err != nil {
		return bad()
	}
	out := sourceUpdateRequest{Revision: config.Revision(revision), SourceID: id}
	if raw, exists := p["intent"]; exists {
		if len(raw) == 0 || raw[0] != '{' {
			return bad()
		}
		intent, err := config.ParseIntent(raw, config.IntentDraft)
		if err != nil {
			return bad()
		}
		out.Intent = &intent
		if _, err = sourceUpdateDefinition(out, nil); err != nil {
			return bad()
		}
	}
	if raw, exists := p["region_rules"]; exists {
		if json.Unmarshal(raw, &out.RegionRules) != nil || !validSourceRegionRules(out.RegionRules) {
			return bad()
		}
		// The request envelope already uses strict JCS. Compare normalized known
		// fields to reject case-folded/unknown rule fields without rejecting spaces
		// or a caller's legal JSON key order in the original request.
		known, err := json.Marshal(out.RegionRules)
		if err != nil {
			return bad()
		}
		known, err = config.CanonicalJSON(known)
		if err != nil || !bytes.Equal(known, raw) {
			return bad()
		}
		out.HasRegionRules = true
	}
	return out, nil
}

// A supplied declaration is authoritative for this candidate acquisition even
// if it does not yet compile. Otherwise the caller supplies the actual committed
// Intent. Selection.active_subscription is deliberately irrelevant to an exact
// subscription.update ID. No caller-owned schedule pointer escapes.
func sourceUpdateDefinition(r sourceUpdateRequest, committed *config.Intent) (config.Subscription, error) {
	intent := r.Intent
	if intent == nil {
		intent = committed
	}
	if intent == nil {
		return config.Subscription{}, ErrRequest
	}
	var found config.Subscription
	count := 0
	for _, source := range intent.Subscriptions {
		if source.ID == r.SourceID {
			found = source
			count++
		}
	}
	if count != 1 {
		return config.Subscription{}, ErrRequest
	}
	if found.UpdateHours != nil {
		value := *found.UpdateHours
		found.UpdateHours = &value
	}
	return found, nil
}
