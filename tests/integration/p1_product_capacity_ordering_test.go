//go:build linux && amd64 && p1integration

package integration

import (
	"testing"
)

// Synthetic boundary-policy checks over real Journal codec fixtures. These do
// not claim that the events were emitted by a product process.
func TestP1CapacitySeedOrdering(t *testing.T) {
	records, current := p1CapacityProvenanceFixture(t)
	seeds, err := p1ReadCapacitySeeds(records, current)
	must(t, err)
	trace := newPolicyTrace()
	trace.e.FixtureSeeds = seeds
	seedIndex := -1
	for id, op := range current {
		trace.add(op)
		if seed, fixture := seeds[id]; fixture {
			trace.event("fixture-seed-durable", op, nil)
			seedIndex = len(trace.e.Boundaries) - 1
			v := &trace.e.Boundaries[seedIndex]
			v.RequestID = ""
			v.Originals = append(append(append([]originalRef{}, seed.BeginOriginals...), seed.PublishOriginals...), seed.EndOriginals...)
		} else {
			trace.event("operation-durable", op, nil)
		}
	}
	must(t, validateOperationOrdering(trace.e, policyCoreSHA))
	for _, name := range []string{"missing-provenance", "business-admission", "duplicate-seed", "request-scope", "runtime-effect", "missing-window-ref", "changed-record-pin"} {
		t.Run(name, func(t *testing.T) {
			e := clonePolicyEvidence(trace.e)
			e.FixtureSeeds = map[string]p1CapacitySeed{}
			for id, seed := range seeds {
				e.FixtureSeeds[id] = seed
			}
			v := &e.Boundaries[seedIndex]
			switch name {
			case "missing-provenance":
				delete(e.FixtureSeeds, v.OperationID)
				v.Name = "operation-durable"
			case "business-admission":
				v.Name = "operation-durable"
			case "duplicate-seed":
				copy := *v
				copy.Index = uint64(len(e.Boundaries) + 1)
				e.Boundaries = append(e.Boundaries, copy)
			case "request-scope":
				v.RequestID = e.Operations[v.OperationID].RequestID
			case "runtime-effect":
				p := policyIdentity("forged-seed", 100)
				v.Instance = &p
			case "missing-window-ref":
				seed := e.FixtureSeeds[v.OperationID]
				seed.EndOriginals = nil
				e.FixtureSeeds[v.OperationID] = seed
			case "changed-record-pin":
				seed := e.FixtureSeeds[v.OperationID]
				seed.RecordOriginal.SHA256 = policyCoreSHA
				e.FixtureSeeds[v.OperationID] = seed
			}
			if validateOperationOrdering(e, policyCoreSHA) == nil {
				t.Fatal("fixture metadata granted product authority")
			}
		})
	}
}
