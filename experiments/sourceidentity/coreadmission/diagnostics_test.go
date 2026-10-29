package coreadmission

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests inject backend evidence; they do not load BPF or reproduce a
// kernel recursion skip. Public behavior, ownership and refusal are the oracle.
func diagnosticsOwner(t *testing.T) (*Owner, *executableFake) {
	t.Helper()
	h := exampleV2()
	f := newExecutableFake(h)
	o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	f.log = nil
	return o, f
}

func diagnosticsCounters(d CaptureDiagnostics) []CounterObservation {
	return []CounterObservation{d.CaptureFaults, d.ExecutableFaultBits, d.RawEnterRecursionMisses, d.RawExitRecursionMisses}
}

func expectDiagnosticCounter(t *testing.T, c CounterObservation, status, value, reason string) {
	t.Helper()
	if c.Status != status || c.Reason != reason {
		t.Fatalf("counter %#v, want %s/%s", c, status, reason)
	}
	if status == "observed" {
		if c.Value == nil || *c.Value != value {
			t.Fatalf("observed value %v, want %q", c.Value, value)
		}
	} else if c.Value != nil {
		t.Fatalf("unknown/inapplicable counter masquerades as value %q", *c.Value)
	}
}

func setDiagnosticCounter(f *executableFake, index int, value uint64) {
	switch index {
	case 0:
		le.PutUint64(f.values[f.h.Maps["capture_faults"]], value)
	case 1:
		le.PutUint64(f.values[f.h.Maps["exe_state"]][32:], value)
	case 2, 3:
		name := []string{"call_enter", "call_exit"}[index-2]
		le.PutUint64(f.infos[f.h.Programs[name]][208:], value)
	default:
		panic("bad counter index")
	}
}

func TestDiagnosticsObservedValuesAndJSON(t *testing.T) {
	for _, index := range []int{-1, 0, 1, 2, 3} {
		for _, value := range []uint64{1, 1<<53 + 1, math.MaxUint64} {
			if index == -1 && value != 1 {
				continue
			}
			t.Run(fmt.Sprintf("counter%d/value%d", index, value), func(t *testing.T) {
				o, f := diagnosticsOwner(t)
				if index >= 0 {
					setDiagnosticCounter(f, index, value)
				}
				d := o.Diagnostics()
				state := "no_fault_observed"
				if index >= 0 {
					state = "fault_observed"
				}
				if d.Schema != "sourceidentity-capture-diagnostics-v1" || d.HandoffSchema != f.h.Schema || d.Generation != f.h.Generation || d.CoreGeneration != f.h.CoreGeneration || d.Observation != "sequential_reads" || d.State != state || d.Incomplete {
					t.Fatalf("wrong diagnostic declaration/state: %#v", d)
				}
				for i, c := range diagnosticsCounters(d) {
					want := "0"
					if i == index {
						want = strconv.FormatUint(value, 10)
					}
					expectDiagnosticCounter(t, c, "observed", want, "")
				}
				raw, err := json.Marshal(d)
				if err != nil {
					t.Fatal(err)
				}
				var wire map[string]json.RawMessage
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatal(err)
				}
				for i, name := range []string{"capture_faults", "executable_fault_bits", "raw_enter_recursion_misses", "raw_exit_recursion_misses"} {
					var counter map[string]any
					if err := json.Unmarshal(wire[name], &counter); err != nil {
						t.Fatal(err)
					}
					if counter["value"] != *diagnosticsCounters(d)[i].Value || counter["status"] != "observed" || len(counter) != 2 {
						t.Fatalf("counter lost decimal-string precision: %s", raw)
					}
				}
				if _, present := wire["capture_exe_recursion_misses"]; present {
					t.Fatal("unsupported LSM recursion guarantee exposed")
				}
			})
		}
	}
}

func TestDiagnosticsUnavailableEvidenceAndPartialFault(t *testing.T) {
	tests := []struct {
		name  string
		index int
		apply func(*executableFake)
	}{
		{"capture-read-error", 0, func(f *executableFake) { f.fail = fmt.Sprintf("lookup:%d:0", f.h.Maps["capture_faults"]) }},
		{"capture-short", 0, func(f *executableFake) { f.values[f.h.Maps["capture_faults"]] = make([]byte, 7) }},
		{"exe-read-error", 1, func(f *executableFake) { f.fail = fmt.Sprintf("lookup:%d:0", f.h.Maps["exe_state"]) }},
		{"exe-short-before-fault-field", 1, func(f *executableFake) { f.values[f.h.Maps["exe_state"]] = make([]byte, 39) }},
	}
	for i, name := range []string{"call_enter", "call_exit"} {
		name, index := name, i+2
		for _, mutation := range []string{"read-error", "short", "wrong-type", "wrong-id", "wrong-name", "zero-owned-id"} {
			mutation := mutation
			tests = append(tests, struct {
				name  string
				index int
				apply func(*executableFake)
			}{name + "/" + mutation, index, func(f *executableFake) {
				fd := f.h.Programs[name]
				switch mutation {
				case "read-error":
					f.fail = fmt.Sprintf("info:%d", fd)
				case "short":
					f.infos[fd] = f.infos[fd][:215]
				case "wrong-type":
					le.PutUint32(f.infos[fd], 29)
				case "wrong-id":
					le.PutUint32(f.infos[fd][4:], 900)
				case "wrong-name":
					copy(f.infos[fd][64:80], "bad_raw_name\x00\x00\x00\x00")
				case "zero-owned-id":
					// A zero ID cannot arise from valid adoption; exercise the
					// public read's fail-closed check using a zero observed ID.
					le.PutUint32(f.infos[fd][4:], 0)
				}
			}})
		}
	}
	for _, tc := range tests {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial-fault-%v", tc.name, partial), func(t *testing.T) {
				o, f := diagnosticsOwner(t)
				faultIndex := (tc.index + 1) % 4
				if partial {
					setDiagnosticCounter(f, faultIndex, math.MaxUint64)
				}
				tc.apply(f)
				d := o.Diagnostics()
				wantState := "unavailable"
				if partial {
					wantState = "fault_observed"
				}
				if d.State != wantState || !d.Incomplete {
					t.Fatalf("unknown was treated as zero or hid a fault: %#v", d)
				}
				for i, c := range diagnosticsCounters(d) {
					if i == tc.index {
						expectDiagnosticCounter(t, c, "unavailable", "", "read_failed")
					} else {
						value := "0"
						if partial && i == faultIndex {
							value = "18446744073709551615"
						}
						expectDiagnosticCounter(t, c, "observed", value, "")
					}
				}
				raw, err := json.Marshal(diagnosticsCounters(d)[tc.index])
				if err != nil || !strings.Contains(string(raw), `"value":null`) {
					t.Fatalf("unknown not encoded as null: %s %v", raw, err)
				}
			})
		}
	}
}

func TestDiagnosticsV1AndClosedNeverReadsReleasedFDs(t *testing.T) {
	for _, executable := range []bool{false, true} {
		t.Run(fmt.Sprint(executable), func(t *testing.T) {
			var o *Owner
			var f *fake
			if executable {
				var ef *executableFake
				o, ef = diagnosticsOwner(t)
				f = ef.fake
			} else {
				o, f = adoptFake(t)
				d := o.Diagnostics()
				if d.State != "no_fault_observed" || d.Incomplete {
					t.Fatal(d)
				}
				expectDiagnosticCounter(t, d.CaptureFaults, "observed", "0", "")
				for _, c := range diagnosticsCounters(d)[1:] {
					expectDiagnosticCounter(t, c, "not_applicable", "", "")
				}
				if !reflect.DeepEqual(f.log, []string{fmt.Sprintf("lookup:%d:0", f.h.Maps["capture_faults"])}) {
					t.Fatalf("v1 touched executable resources: %v", f.log)
				}
			}
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			before := append([]string(nil), f.log...)
			for i := 0; i < 3; i++ {
				d := o.Diagnostics()
				if d.State != "unavailable" || !d.Incomplete || d.HandoffSchema != f.h.Schema {
					t.Fatal(d)
				}
				for _, c := range diagnosticsCounters(d) {
					expectDiagnosticCounter(t, c, "unavailable", "", "owner_closed")
				}
			}
			if !reflect.DeepEqual(f.log, before) {
				t.Fatal("diagnostics touched a closed/reusable FD")
			}
		})
	}
}

func TestDiagnosticsReadOnlyAndIndependentValues(t *testing.T) {
	o, f := diagnosticsOwner(t)
	f.publishExecutable()
	if _, err := o.Admit(nil); err != nil {
		t.Fatal(err)
	}
	setDiagnosticCounter(f, 2, 7)
	maps := make(map[int][]byte)
	for fd, raw := range f.values {
		maps[fd] = append([]byte(nil), raw...)
	}
	fds := append([]int(nil), o.fds...)
	status := o.Status()
	f.log = nil
	first, second := o.Diagnostics(), o.Diagnostics()
	if !reflect.DeepEqual(first, second) || first.State != "fault_observed" || o.Status() != status || !reflect.DeepEqual(f.values, maps) || !reflect.DeepEqual(o.fds, fds) || len(f.closed) != 0 {
		t.Fatal("diagnostics changed owner state/resources or lost a repeated observation")
	}
	for _, operation := range f.log {
		if !strings.HasPrefix(operation, "lookup:") && !strings.HasPrefix(operation, "info:") {
			t.Fatalf("non-read diagnostic operation %s", operation)
		}
	}
	*first.RawEnterRecursionMisses.Value = "999"
	setDiagnosticCounter(f, 2, 8)
	third := o.Diagnostics()
	expectDiagnosticCounter(t, second.RawEnterRecursionMisses, "observed", "7", "")
	expectDiagnosticCounter(t, third.RawEnterRecursionMisses, "observed", "8", "")
	if _, err := o.Admit(nil); err == nil || o.Status() != (Status{1, 1, true}) {
		t.Fatal("diagnostic read repaired or concealed the faulty generation")
	}
}

func TestDiagnosticsRawMissesRefuseAdoptionAndAdmission(t *testing.T) {
	for _, name := range []string{"call_enter", "call_exit"} {
		for _, cause := range []string{"nonzero", "unreadable", "short", "type", "id", "name"} {
			for _, phase := range []string{"adoption", "before-evidence", "after-evidence"} {
				t.Run(name+"/"+cause+"/"+phase, func(t *testing.T) {
					h := exampleV2()
					f := newExecutableFake(h)
					mutate := func() {
						fd := h.Programs[name]
						switch cause {
						case "nonzero":
							le.PutUint64(f.infos[fd][208:], math.MaxUint64)
						case "unreadable":
							f.fail = fmt.Sprintf("info:%d", fd)
						case "short":
							f.infos[fd] = f.infos[fd][:215]
						case "type":
							le.PutUint32(f.infos[fd], 29)
						case "id":
							le.PutUint32(f.infos[fd][4:], 999)
						case "name":
							copy(f.infos[fd][64:80], "bad_raw_name\x00\x00\x00\x00")
						}
					}
					if phase == "adoption" {
						mutate()
						o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
						if err == nil || o != nil || len(f.closed) != 20 {
							t.Fatal("bad initial evidence adopted or leaked resources", err, f.closed)
						}
						seen := make(map[int]bool)
						for _, fd := range f.closed {
							if seen[fd] {
								t.Fatal("FD released twice", fd)
							}
							seen[fd] = true
						}
						return
					}
					o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
					if err != nil {
						t.Fatal(err)
					}
					defer o.Close()
					f.publishExecutable()
					if phase == "before-evidence" {
						mutate()
					} else {
						f.lookupHook = func(fd int, _ []byte, _ uint64) {
							if fd == h.Maps["exe_records"] {
								mutate()
							}
						}
					}
					id, err := o.Admit(nil)
					if err == nil || !reflect.DeepEqual(id, Identity{}) || o.Status() != (Status{0, 1, true}) {
						t.Fatal("bad before/after evidence published an identity", id, err, o.Status())
					}
					reads := len(f.log)
					if _, err := o.Admit(nil); err == nil || len(f.log) != reads {
						t.Fatal("revoked admission still reads FD evidence")
					}
				})
			}
		}
	}
}

func TestDiagnosticsConcurrentCloseSerializesDescriptorLifetime(t *testing.T) {
	o, f := diagnosticsOwner(t)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.lookupHook = func(fd int, _ []byte, _ uint64) {
		if fd == f.h.Maps["capture_faults"] {
			once.Do(func() { close(entered); <-resume })
		}
	}
	diagnosticResult := make(chan CaptureDiagnostics, 1)
	closeResult := make(chan error, 1)
	go func() { diagnosticResult <- o.Diagnostics() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic did not enter fake read")
	}
	go func() { closeResult <- o.Close() }()
	close(resume)
	select {
	case d := <-diagnosticResult:
		if d.State != "no_fault_observed" || d.Incomplete {
			t.Fatal("close interfered with in-flight FD reads", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic deadlocked against close")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close blocked after diagnostic completed")
	}
	before := len(f.log)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := o.Diagnostics()
			if d.State != "unavailable" || !d.Incomplete {
				t.Error("post-close diagnostics did not remain unavailable", d)
			}
		}()
	}
	wg.Wait()
	if len(f.log) != before || len(f.closed) != 20 {
		t.Fatal("post-close concurrent diagnostics reused resources")
	}
}
