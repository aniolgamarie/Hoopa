package coreadmission

import (
	"errors"
	"strconv"
)

// CounterObservation never represents a failed or inapplicable read as zero.
type CounterObservation struct {
	Status string  `json:"status"`
	Value  *string `json:"value"`
	Reason string  `json:"reason,omitempty"`
}

// CaptureDiagnostics describes sequential reads, not a coherent capture snapshot
// or a proof that all hooks ran. In particular, LSM MAC omissions are not covered
// by the raw tracepoint recursion counters on the pinned kernel.
type CaptureDiagnostics struct {
	Schema                  string             `json:"schema"`
	HandoffSchema           string             `json:"handoff_schema"`
	Generation              string             `json:"generation"`
	CoreGeneration          string             `json:"core_generation"`
	Observation             string             `json:"observation"`
	State                   string             `json:"state"`
	Incomplete              bool               `json:"incomplete"`
	CaptureFaults           CounterObservation `json:"capture_faults"`
	ExecutableFaultBits     CounterObservation `json:"executable_fault_bits"`
	RawEnterRecursionMisses CounterObservation `json:"raw_enter_recursion_misses"`
	RawExitRecursionMisses  CounterObservation `json:"raw_exit_recursion_misses"`
}

func observedCounter(value uint64, err error) CounterObservation {
	if err != nil {
		return CounterObservation{Status: "unavailable", Reason: "read_failed"}
	}
	text := strconv.FormatUint(value, 10)
	return CounterObservation{Status: "observed", Value: &text}
}

func (o *Owner) counter(name string, offset int) (uint64, error) {
	raw, err := o.lookup(name, make([]byte, 4), 0)
	if err != nil {
		return 0, err
	}
	if offset < 0 || len(raw) < offset+8 {
		return 0, errors.New("short counter value")
	}
	return le.Uint64(raw[offset:]), nil
}

func (o *Owner) rawRecursionMisses(name string) (uint64, error) {
	if !o.h.executable() || (name != "call_enter" && name != "call_exit") {
		return 0, errors.New("raw recursion counter not applicable")
	}
	raw, err := o.b.info(o.h.Programs[name])
	if err != nil {
		return 0, err
	}
	// Fixed Linux/amd64 UAPI bpf_prog_info: recursion_misses is u64 at 208.
	// This field does not require enabling the global bpf_stats_enabled switch.
	if len(raw) < 216 || le.Uint32(raw) != 17 || o.programIDs[name] == 0 ||
		le.Uint32(raw[4:]) != o.programIDs[name] || infoName(raw[64:80]) != name {
		return 0, errors.New("raw recursion program info mismatch")
	}
	return le.Uint64(raw[208:]), nil
}

// Diagnostics does not revoke, repair, stop, update maps, or change admission
// counters. The owner lock protects descriptor lifetime against Admit and Close;
// kernel writers may still run between the individual observations.
func (o *Owner) Diagnostics() CaptureDiagnostics {
	o.mu.Lock()
	defer o.mu.Unlock()
	unavailable := CounterObservation{Status: "unavailable", Reason: "owner_closed"}
	result := CaptureDiagnostics{
		Schema: "sourceidentity-capture-diagnostics-v1", HandoffSchema: o.h.Schema,
		Generation: o.h.Generation, CoreGeneration: o.h.CoreGeneration,
		Observation: "sequential_reads", State: "unavailable", Incomplete: true,
		CaptureFaults: unavailable, ExecutableFaultBits: unavailable,
		RawEnterRecursionMisses: unavailable, RawExitRecursionMisses: unavailable,
	}
	if o.closed {
		return result // Never read a released or potentially reused FD.
	}
	result.CaptureFaults = observedCounter(o.counter("capture_faults", 0))
	notApplicable := CounterObservation{Status: "not_applicable"}
	result.ExecutableFaultBits = notApplicable
	result.RawEnterRecursionMisses = notApplicable
	result.RawExitRecursionMisses = notApplicable
	if o.h.executable() {
		result.ExecutableFaultBits = observedCounter(o.counter("exe_state", 32))
		result.RawEnterRecursionMisses = observedCounter(o.rawRecursionMisses("call_enter"))
		result.RawExitRecursionMisses = observedCounter(o.rawRecursionMisses("call_exit"))
	}
	result.State, result.Incomplete = "no_fault_observed", false
	for _, counter := range []CounterObservation{result.CaptureFaults, result.ExecutableFaultBits, result.RawEnterRecursionMisses, result.RawExitRecursionMisses} {
		if counter.Status == "unavailable" {
			result.Incomplete = true
		} else if counter.Status == "observed" && *counter.Value != "0" {
			result.State = "fault_observed"
		}
	}
	if result.Incomplete && result.State != "fault_observed" {
		result.State = "unavailable"
	}
	return result
}
