package main

import "time"

type CheckResult struct {
	CaseID     string         `json:"case_id"`
	Status     EvidenceStatus `json:"status"`
	ReasonCode string         `json:"reason_code"`
}

// These are runtime-registered truths, not inferred from a launcher or proxy.
// Fixture symbols alone cannot set runtimeRegistered=true.
type ExpectedIdentityFlow struct {
	ID                                        string
	PID                                       int
	Name, StartIdentity, BootIdentity, Domain string
	InputBytes, OutputBytes                   uint64
	Shared                                    bool
	UnknownReason                             string
}

type IdentityObservation struct {
	FlowID                                    string
	PID, ProxyPID                             int
	Name, StartIdentity, BootIdentity, Domain string
	SocketEvidenceID, TargetEvidenceID        string
	OriginalIngress, UniqueOwner, Terminal    bool
	SourceObservedAt, ForwardStartedAt        time.Time
	InputBytes, OutputBytes                   uint64
	UnknownReason                             string
}

// CheckIdentityRun is a strict oracle for supplied observations. It collects no
// PID itself. Missing source/terminal evidence stays unverified, never zero/pass.
func CheckIdentityRun(expected []ExpectedIdentityFlow, observed []IdentityObservation, runtimeRegistered bool) CheckResult {
	result := CheckResult{"G01", Unverified, "runtime_registration_missing"}
	if !runtimeRegistered {
		return result
	}
	identified := 0
	expectedIDs := map[string]bool{}
	for _, f := range expected {
		if f.ID == "" || expectedIDs[f.ID] {
			return CheckResult{"G01", Failed, "invalid_oracle"}
		}
		expectedIDs[f.ID] = true
		if !f.Shared {
			if f.PID <= 0 || f.Name == "" || f.StartIdentity == "" || f.BootIdentity == "" || f.Domain == "" {
				return CheckResult{"G01", Failed, "invalid_oracle"}
			}
			identified++
		}
	}
	if identified < 100 {
		return CheckResult{"G01", Unverified, "preregistered_short_flows_missing"}
	}
	byID := map[string]IdentityObservation{}
	for _, o := range observed {
		if !expectedIDs[o.FlowID] {
			return CheckResult{"G01", Failed, "unexpected_flow"}
		}
		if _, ok := byID[o.FlowID]; ok {
			return CheckResult{"G01", Failed, "duplicate_flow"}
		}
		byID[o.FlowID] = o
	}
	missing := ""
	for _, f := range expected {
		o, ok := byID[f.ID]
		if !ok {
			missing = "source_observations_missing"
			continue
		}
		if f.Shared {
			if f.UnknownReason != "shared_socket" || o.PID != 0 || o.UniqueOwner || o.UnknownReason != f.UnknownReason {
				return CheckResult{"G01", Failed, "shared_owner_fabricated"}
			}
			if !o.Terminal {
				missing = "terminal_facts_missing"
				continue
			}
			if o.InputBytes != f.InputBytes || o.OutputBytes != f.OutputBytes {
				return CheckResult{"G01", Failed, "terminal_bytes_mismatch"}
			}
			continue
		}
		if o.PID == 0 || !o.UniqueOwner || !o.OriginalIngress || o.SocketEvidenceID == "" {
			missing = "original_source_evidence_missing"
			continue
		}
		if o.PID == o.ProxyPID || o.PID != f.PID || o.Name != f.Name || o.StartIdentity != f.StartIdentity || o.BootIdentity != f.BootIdentity {
			return CheckResult{"G01", Failed, "source_identity_mismatch"}
		}
		if o.SourceObservedAt.IsZero() || o.ForwardStartedAt.IsZero() || o.SourceObservedAt.After(o.ForwardStartedAt) {
			missing = "synchronous_source_evidence_missing"
			continue
		}
		if o.TargetEvidenceID == "" {
			missing = "target_evidence_missing"
			continue
		}
		if o.Domain != f.Domain {
			return CheckResult{"G01", Failed, "target_mismatch"}
		}
		if !o.Terminal {
			missing = "terminal_facts_missing"
			continue
		}
		if o.InputBytes != f.InputBytes || o.OutputBytes != f.OutputBytes {
			return CheckResult{"G01", Failed, "terminal_bytes_mismatch"}
		}
	}
	if missing != "" {
		return CheckResult{"G01", Unverified, missing}
	}
	return CheckResult{"G01", Passed, "preregistered_facts_verified"}
}
