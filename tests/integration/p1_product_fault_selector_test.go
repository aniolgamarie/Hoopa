//go:build linux && amd64 && p1integration

package integration

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/testsupport/p1trace"
)

var errP1CrashSelector = errors.New("P1_CRASH_SELECTOR_INVALID")

// p1CrashSelector selects only finite declared observation points. It neither
// authenticates records nor records hits/signals: the collector/assembly owns
// those facts. The returned predicate has no hit counter or shared state.
func p1CrashSelector(f faultRequest) (func(p1TraceRecord) (bool, error), error) {
	if f.Failure != "crash-manager" || f.Target != nil || f.Limit != 0 {
		return nil, errP1CrashSelector
	}
	type point struct{ component, name string }
	simple := map[string]point{
		"stop-intent-durable":                {"workflow", "stop-intent-durable"},
		"spawn-intent-durable":               {"runtime", "intent-published"},
		"spawn-returned-before-registration": {"workflow", "spawn-returned-before-registration"},
		"old-stop-observed":                  {"workflow", "old-stop-observed"},
		"old-stop-issued":                    {"platform", "signal-sent"},
		"target-verified":                    {"workflow", "target-verified"},
		"entry-opening":                      {"runtime", "entry-opening"},
		"entry-opened":                       {"runtime", "entry-opened"},
	}
	if selected, ok := simple[f.Point]; ok {
		if f.Artifact != "" || f.Phase != "" || f.Cut != "" {
			return nil, errP1CrashSelector
		}

		signalCut := f.Point == "old-stop-issued"
		return func(record p1TraceRecord) (bool, error) {
			if record.Event.Component != selected.component || record.Event.Point != selected.name {
				return false, nil
			}
			if !signalCut {
				return true, nil
			}
			wrapper, err := p1TraceUnwrap(record.Event.Data)
			if err != nil {
				return false, errP1CrashSelector
			}
			var detail struct {
				Signal *int `json:"signal"`
			}
			if json.Unmarshal(wrapper.Detail, &detail) != nil || detail.Signal == nil {
				return false, errP1CrashSelector
			}
			return *detail.Signal == 15, nil
		}, nil
	}
	if f.Point != "durable-boundary" || f.Cut != "after-publication" {
		return nil, errP1CrashSelector
	}
	if f.Artifact == "candidate" && f.Phase == "prepared" {
		return func(record p1TraceRecord) (bool, error) {
			return record.Event.Component == "store" && record.Event.Point == "bundle-published", nil
		}, nil
	}
	if f.Artifact != "operation" {
		return nil, errP1CrashSelector
	}
	switch f.Phase {
	case "applying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
	default:
		return nil, errP1CrashSelector
	}
	phase := f.Phase
	return func(record p1TraceRecord) (bool, error) {
		if record.Event.Component != "journal" || record.Event.Point != "published" {
			return false, nil
		}
		if record.OperationOriginal == nil {
			return false, errP1CrashSelector
		}
		wrapper, err := p1TraceUnwrap(record.Event.Data)
		if err != nil {
			return false, errP1CrashSelector
		}
		var detail struct {
			Phase string `json:"phase"`
		}
		if json.Unmarshal(wrapper.Detail, &detail) != nil {
			return false, errP1CrashSelector
		}
		switch detail.Phase {
		case "prepared", "validated", "applying", "verifying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
		default:
			return false, errP1CrashSelector
		}
		return detail.Phase == phase, nil
	}, nil
}

// All records in this file are synthetic pure-selector fixtures, not product
// captures or proof that a requested fault was hit or any process was killed.
func p1SelectorJournal(t *testing.T, phase any) p1TraceRecord {
	t.Helper()
	return p1TraceRecord{Event: p1SelectorEvent("journal", "published", p1JSON(t, p1TraceWrapper{false, p1JSON(t, map[string]any{"phase": phase})})), OperationOriginal: &originalRef{Path: "synthetic-selector-original-not-read", Bytes: 1, SHA256: strings.Repeat("a", 64)}}
}
func p1SelectorEvent(component, point string, data json.RawMessage) p1trace.Event {
	return p1trace.Event{Component: component, Point: point, Data: data}
}

func TestP1CrashSelectorExactMappings(t *testing.T) {
	cases := []struct {
		name, component, point string
		request                faultRequest
	}{
		{"stop", "workflow", "stop-intent-durable", faultRequest{Point: "stop-intent-durable", Failure: "crash-manager"}},
		{"intent", "runtime", "intent-published", faultRequest{Point: "spawn-intent-durable", Failure: "crash-manager"}},
		{"spawn-return", "workflow", "spawn-returned-before-registration", faultRequest{Point: "spawn-returned-before-registration", Failure: "crash-manager"}},
		{"old-stop", "workflow", "old-stop-observed", faultRequest{Point: "old-stop-observed", Failure: "crash-manager"}},
		{"old-stop-issued", "platform", "signal-sent", faultRequest{Point: "old-stop-issued", Failure: "crash-manager"}},
		{"verified", "workflow", "target-verified", faultRequest{Point: "target-verified", Failure: "crash-manager"}},
		{"opening", "runtime", "entry-opening", faultRequest{Point: "entry-opening", Failure: "crash-manager"}},
		{"opened", "runtime", "entry-opened", faultRequest{Point: "entry-opened", Failure: "crash-manager"}},
		{"candidate", "store", "bundle-published", faultRequest{Point: "durable-boundary", Artifact: "candidate", Phase: "prepared", Cut: "after-publication", Failure: "crash-manager"}},
	}
	for _, phase := range []string{"applying", "publishing", "committed", "rolling_back", "rolled_back", "degraded"} {
		cases = append(cases, struct {
			name, component, point string
			request                faultRequest
		}{phase, "journal", "published", faultRequest{Point: "durable-boundary", Artifact: "operation", Phase: phase, Cut: "after-publication", Failure: "crash-manager"}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			selector, err := p1CrashSelector(test.request)
			must(t, err)
			record := p1TraceRecord{Event: p1SelectorEvent(test.component, test.point, nil)}
			if test.component == "journal" {
				record = p1SelectorJournal(t, test.request.Phase)
			}
			if test.request.Point == "old-stop-issued" {
				record.Event.Data = p1JSON(t, p1TraceWrapper{false, json.RawMessage(`{"signal":15}`)})
			}
			original := copyP1TraceRecord(record)
			for i := 0; i < 2; i++ {
				hit, err := selector(record)
				if err != nil || !hit {
					t.Fatal("exact mapping did not match")
				}
			}
			if !reflect.DeepEqual(record, original) {
				t.Fatal("pure selector mutated record")
			}
			wrong := copyP1TraceRecord(record)
			wrong.Event.Component = "wrong-component"
			hit, err := selector(wrong)
			if err != nil || hit {
				t.Fatal("wrong component matched")
			}
			wrong = copyP1TraceRecord(record)
			wrong.Event.Point = "wrong-point"
			hit, err = selector(wrong)
			if err != nil || hit {
				t.Fatal("wrong point matched")
			}
			if test.component == "journal" {
				hit, err = selector(p1SelectorJournal(t, "prepared"))
				if err != nil || hit {
					t.Fatal("different legal phase matched")
				}
			}
		})
	}
}
func TestP1CrashSelectorRejectsUnsupportedRequests(t *testing.T) {
	base := faultRequest{Point: "target-verified", Failure: "crash-manager"}
	cases := []faultRequest{{Point: "unknown", Failure: "crash-manager"}, {Point: "old-stop-unknown", Failure: "crash-manager"}, {Point: "target-verified", Failure: "io-error"}, {Point: "durable-boundary", Artifact: "active", Phase: "publishing", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "registration", Phase: "verified", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "launch-intent", Phase: "prepared", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "candidate", Phase: "applying", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "operation", Phase: "prepared", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "operation", Phase: "validated", Cut: "after-publication", Failure: "crash-manager"}, {Point: "durable-boundary", Artifact: "operation", Phase: "applying", Cut: "before-publication", Failure: "crash-manager"}}
	for _, field := range []string{"Artifact", "Phase", "Cut", "Target", "Limit"} {
		changed := base
		switch field {
		case "Artifact":
			changed.Artifact = "operation"
		case "Phase":
			changed.Phase = "applying"
		case "Cut":
			changed.Cut = "after-publication"
		case "Target":
			changed.Target = &processIdentity{PID: 1}
		case "Limit":
			changed.Limit = 1
		}
		cases = append(cases, changed)
	}
	for index, request := range cases {
		selector, err := p1CrashSelector(request)
		if !errors.Is(err, errP1CrashSelector) || selector != nil {
			t.Fatal("unsupported/unused request field accepted", index)
		}
	}
}
func TestP1CrashSelectorMalformedOperationDetail(t *testing.T) {
	selector, err := p1CrashSelector(faultRequest{Point: "durable-boundary", Artifact: "operation", Phase: "applying", Cut: "after-publication", Failure: "crash-manager"})
	must(t, err)
	for _, change := range []string{"missing-original", "missing-phase", "number-phase", "unknown-phase", "null-detail", "array-detail", "invalid-json", "duplicate-phase"} {
		t.Run(change, func(t *testing.T) {
			record := p1SelectorJournal(t, "applying")
			switch change {
			case "missing-original":
				record.OperationOriginal = nil
			case "missing-phase":
				record.Event.Data = p1JSON(t, p1TraceWrapper{false, json.RawMessage(`{}`)})
			case "number-phase":
				record = p1SelectorJournal(t, 1)
			case "unknown-phase":
				record = p1SelectorJournal(t, "unknown")
			case "null-detail":
				record.Event.Data = json.RawMessage(`{"injectable":false,"detail":null}`)
			case "array-detail":
				record.Event.Data = json.RawMessage(`{"injectable":false,"detail":[]}`)
			case "invalid-json":
				record.Event.Data = json.RawMessage(`{"injectable":`)
			case "duplicate-phase":
				record.Event.Data = json.RawMessage(`{"injectable":false,"detail":{"phase":"applying","phase":"publishing"}}`)
			}
			hit, err := selector(record)
			if hit || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("malformed matching operation event ignored or selected")
			}
			record.Event.Component = "unrelated"
			hit, err = selector(record)
			if hit || err != nil {
				t.Fatal("unrelated malformed detail was parsed")
			}
		})
	}
}

// The phase argument must be read independently from the current actual Store
// operation by the supervisor. It is not the requested/desirable phase and this
// pure selector cannot establish its provenance or authorize a signal.
func p1PublishedCrashSelector(f faultRequest) (func(p1TraceRecord, string) (bool, error), error) {
	if f.Failure != "crash-manager" || f.Point != "durable-boundary" || f.Cut != "after-publication" || f.Target != nil || f.Limit != 0 {
		return nil, errP1CrashSelector
	}
	component, point := "", ""
	switch f.Artifact {
	case "active":
		if f.Phase != "publishing" && f.Phase != "rolling_back" {
			return nil, errP1CrashSelector
		}
		component, point = "store", "active-published"
	case "launch-intent":
		if f.Phase != "applying" {
			return nil, errP1CrashSelector
		}
		component, point = "runtime", "intent-published"
	case "registration":
		if f.Phase != "verifying" {
			return nil, errP1CrashSelector
		}
		component, point = "runtime", "registration-verified"
	default:
		return nil, errP1CrashSelector
	}
	wanted := f.Phase
	return func(record p1TraceRecord, actualPhase string) (bool, error) {
		if record.Event.Component != component || record.Event.Point != point {
			return false, nil
		}
		if record.Event.OperationID == "" {
			return false, errP1CrashSelector
		}
		switch actualPhase {
		case "prepared", "validated", "applying", "verifying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
		default:
			return false, errP1CrashSelector
		}
		return actualPhase == wanted, nil
	}, nil
}
func TestP1PublishedCrashSelectorExactMappings(t *testing.T) {
	for _, test := range []struct{ artifact, phase, component, point string }{{"active", "publishing", "store", "active-published"}, {"active", "rolling_back", "store", "active-published"}, {"launch-intent", "applying", "runtime", "intent-published"}, {"registration", "verifying", "runtime", "registration-verified"}} {
		t.Run(test.artifact+"/"+test.phase, func(t *testing.T) {
			selector, err := p1PublishedCrashSelector(faultRequest{Point: "durable-boundary", Artifact: test.artifact, Phase: test.phase, Cut: "after-publication", Failure: "crash-manager"})
			must(t, err)
			record := p1TraceRecord{Event: p1SelectorEvent(test.component, test.point, nil)}
			record.Event.OperationID = "synthetic-operation"
			for i := 0; i < 2; i++ {
				hit, err := selector(record, test.phase)
				if err != nil || !hit {
					t.Fatal("exact current phase did not select")
				}
			}
			hit, err := selector(record, "prepared")
			if err != nil || hit {
				t.Fatal("another legal current phase selected")
			}
			missing := copyP1TraceRecord(record)
			missing.Event.OperationID = ""
			hit, err = selector(missing, test.phase)
			if hit || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("missing operation ID accepted")
			}
			for _, phase := range []string{"", "unknown", "Publishing"} {
				hit, err = selector(record, phase)
				if hit || !errors.Is(err, errP1CrashSelector) {
					t.Fatal("invalid actual operation phase accepted")
				}
			}
			wrong := copyP1TraceRecord(record)
			wrong.Event.Component = "wrong"
			wrong.Event.OperationID = ""
			hit, err = selector(wrong, "")
			if hit || err != nil {
				t.Fatal("unrelated component inspected phase")
			}
			wrong = copyP1TraceRecord(record)
			wrong.Event.Point = "wrong"
			wrong.Event.OperationID = ""
			hit, err = selector(wrong, "")
			if hit || err != nil {
				t.Fatal("unrelated point inspected phase")
			}
		})
	}
}
func TestP1PublishedCrashSelectorRejectsUnsupported(t *testing.T) {
	base := faultRequest{Point: "durable-boundary", Artifact: "registration", Phase: "verifying", Cut: "after-publication", Failure: "crash-manager"}
	for _, change := range []string{"failure", "point", "cut", "target", "limit", "artifact", "legacy-registration-applying", "active-wrong-phase", "launch-wrong-phase"} {
		f := base
		switch change {
		case "failure":
			f.Failure = "io-error"
		case "point":
			f.Point = "entry-opened"
		case "cut":
			f.Cut = "before-publication"
		case "target":
			f.Target = &processIdentity{}
		case "limit":
			f.Limit = 1
		case "artifact":
			f.Artifact = "unknown"
		case "legacy-registration-applying":
			f.Phase = "applying"
		case "active-wrong-phase":
			f.Artifact = "active"
			f.Phase = "applying"
		case "launch-wrong-phase":
			f.Artifact = "launch-intent"
			f.Phase = "verifying"
		}
		selector, err := p1PublishedCrashSelector(f)
		if selector != nil || !errors.Is(err, errP1CrashSelector) {
			t.Fatal("unsupported publication request accepted", change)
		}
	}
}
func TestP1CrashSelectorOldStopIssuedSignal(t *testing.T) {
	selector, err := p1CrashSelector(faultRequest{Point: "old-stop-issued", Failure: "crash-manager"})
	must(t, err)
	for _, test := range []struct {
		name, detail string
		hit, invalid bool
	}{{"term", `{"signal":15}`, true, false}, {"kill", `{"signal":9}`, false, false}, {"zero", `{"signal":0}`, false, false}, {"missing", `{}`, false, true}, {"null", `{"signal":null}`, false, true}, {"string", `{"signal":"15"}`, false, true}, {"fraction", `{"signal":15.5}`, false, true}, {"duplicate", `{"signal":15,"signal":9}`, false, true}} {
		t.Run(test.name, func(t *testing.T) {
			record := p1TraceRecord{Event: p1SelectorEvent("platform", "signal-sent", p1JSON(t, p1TraceWrapper{false, json.RawMessage(test.detail)}))}
			hit, err := selector(record)
			if hit != test.hit || (err != nil) != test.invalid {
				t.Fatal("actual signal mapping mismatch")
			}
			record.Event.Component = "unrelated"
			hit, err = selector(record)
			if hit || err != nil {
				t.Fatal("unrelated signal detail parsed")
			}
		})
	}
}

// p1ActiveIOSelector selects an injectable active-publication checkpoint. The
// caller supplies the independently read actual operation phase and owns error
// injection and producer scope; this predicate performs neither operation.
func p1ActiveIOSelector(f faultRequest) (func(p1TraceRecord, string) (bool, error), error) {
	if f.Point != "durable-boundary" || f.Artifact != "active" || f.Phase != "publishing" || f.Failure != "io-error-uncertain" || f.Target != nil || f.Limit != 0 {
		return nil, errP1CrashSelector
	}
	checkpoints := map[string]string{
		"temporary-write": "active.file.write",
		"file-sync":       "active.file.sync",
		"rename":          "active.rename",
		"directory-sync":  "active.parent-sync",
	}
	checkpoint, ok := checkpoints[f.Cut]
	if !ok {
		return nil, errP1CrashSelector
	}
	return func(record p1TraceRecord, actualPhase string) (bool, error) {
		if record.Event.Component != "store" || record.Event.Point != "checkpoint" {
			return false, nil
		}
		wrapper, err := p1TraceUnwrap(record.Event.Data)
		if err != nil || !wrapper.Injectable {
			return false, errP1CrashSelector
		}
		var detail struct {
			Checkpoint string `json:"checkpoint"`
		}
		if json.Unmarshal(wrapper.Detail, &detail) != nil || detail.Checkpoint == "" {
			return false, errP1CrashSelector
		}
		if detail.Checkpoint != checkpoint {
			return false, nil
		}
		switch actualPhase {
		case "prepared", "validated", "applying", "verifying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
		default:
			return false, errP1CrashSelector
		}
		return actualPhase == "publishing", nil
	}, nil
}

func TestP1ActiveIOSelectorMappings(t *testing.T) {
	for _, test := range []struct{ cut, checkpoint string }{
		{"temporary-write", "active.file.write"}, {"file-sync", "active.file.sync"}, {"rename", "active.rename"}, {"directory-sync", "active.parent-sync"},
	} {
		t.Run(test.cut, func(t *testing.T) {
			selector, err := p1ActiveIOSelector(faultRequest{Point: "durable-boundary", Artifact: "active", Phase: "publishing", Cut: test.cut, Failure: "io-error-uncertain"})
			must(t, err)
			record := p1TraceRecord{Event: p1SelectorEvent("store", "checkpoint", p1JSON(t, p1TraceWrapper{true, p1JSON(t, map[string]string{"checkpoint": test.checkpoint})}))}
			original := copyP1TraceRecord(record)
			for i := 0; i < 2; i++ {
				hit, err := selector(record, "publishing")
				if !hit || err != nil {
					t.Fatal("exact injectable checkpoint not selected")
				}
			}
			if !reflect.DeepEqual(record, original) {
				t.Fatal("selector mutated record")
			}
			for _, phase := range []string{"prepared", "validated", "applying", "verifying", "committed", "rolling_back", "rolled_back", "degraded"} {
				hit, err := selector(record, phase)
				if hit || err != nil {
					t.Fatal("another legal phase selected")
				}
			}
			for _, phase := range []string{"", "unknown", "Publishing"} {
				hit, err := selector(record, phase)
				if hit || !errors.Is(err, errP1CrashSelector) {
					t.Fatal("invalid actual phase accepted")
				}
			}
			unrelated := copyP1TraceRecord(record)
			unrelated.Event.Data = p1JSON(t, p1TraceWrapper{true, p1JSON(t, map[string]string{"checkpoint": "unrelated.checkpoint"})})
			hit, err := selector(unrelated, "")
			if hit || err != nil {
				t.Fatal("unrelated checkpoint inspected phase")
			}
			for _, field := range []string{"component", "point"} {
				unrelated = copyP1TraceRecord(record)
				if field == "component" {
					unrelated.Event.Component = "unrelated"
				} else {
					unrelated.Event.Point = "unrelated"
				}
				unrelated.Event.Data = nil
				hit, err = selector(unrelated, "")
				if hit || err != nil {
					t.Fatal("unrelated event parsed")
				}
			}
		})
	}
}

func TestP1ActiveIOSelectorMalformed(t *testing.T) {
	selector, err := p1ActiveIOSelector(faultRequest{Point: "durable-boundary", Artifact: "active", Phase: "publishing", Cut: "rename", Failure: "io-error-uncertain"})
	must(t, err)
	for name, raw := range map[string]string{
		"empty": "", "invalid": "{", "null": "null", "missing-wrapper": `{}`, "noninjectable": `{"injectable":false,"detail":{"checkpoint":"active.rename"}}`,
		"null-injectable": `{"injectable":null,"detail":{"checkpoint":"active.rename"}}`, "missing-detail": `{"injectable":true}`, "null-detail": `{"injectable":true,"detail":null}`,
		"missing-checkpoint": `{"injectable":true,"detail":{}}`, "null-checkpoint": `{"injectable":true,"detail":{"checkpoint":null}}`, "empty-checkpoint": `{"injectable":true,"detail":{"checkpoint":""}}`,
		"numeric-checkpoint": `{"injectable":true,"detail":{"checkpoint":1}}`, "duplicate-checkpoint": `{"injectable":true,"detail":{"checkpoint":"active.rename","checkpoint":"active.rename"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			hit, err := selector(p1TraceRecord{Event: p1SelectorEvent("store", "checkpoint", json.RawMessage(raw))}, "publishing")
			if hit || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("malformed/noninjectable checkpoint accepted")
			}
		})
	}
}

func TestP1ActiveIOSelectorRejectsUnsupported(t *testing.T) {
	base := faultRequest{Point: "durable-boundary", Artifact: "active", Phase: "publishing", Cut: "rename", Failure: "io-error-uncertain"}
	for _, field := range []string{"point", "artifact", "phase", "failure", "target", "limit", "cut", "empty-cut"} {
		t.Run(field, func(t *testing.T) {
			f := base
			switch field {
			case "point":
				f.Point = "entry-opening"
			case "artifact":
				f.Artifact = "candidate"
			case "phase":
				f.Phase = "rolling_back"
			case "failure":
				f.Failure = "crash-manager"
			case "target":
				f.Target = &processIdentity{}
			case "limit":
				f.Limit = 1
			case "cut":
				f.Cut = "after-publication"
			case "empty-cut":
				f.Cut = ""
			}
			selector, err := p1ActiveIOSelector(f)
			if selector != nil || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("unsupported request accepted")
			}
		})
	}
}

// The supervisor supplies the actual current operation phase. Selection alone
// neither injects an error nor changes the pending RollingBack journal state.
func p1RollbackPublicationIOSelector(f faultRequest) (func(p1TraceRecord, string) (bool, error), error) {
	if f.Point != "rollback-active-publication" || f.Failure != "io-error-or-unverifiable-owner" || f.Artifact != "" || f.Phase != "" || f.Cut != "" || f.Target != nil || f.Limit != 0 {
		return nil, errP1CrashSelector
	}
	return func(record p1TraceRecord, actualPhase string) (bool, error) {
		if record.Event.Component != "store" || record.Event.Point != "checkpoint" {
			return false, nil
		}
		wrapper, err := p1TraceUnwrap(record.Event.Data)
		if err != nil || !wrapper.Injectable {
			return false, errP1CrashSelector
		}
		var detail struct {
			Checkpoint string `json:"checkpoint"`
		}
		if json.Unmarshal(wrapper.Detail, &detail) != nil || detail.Checkpoint == "" {
			return false, errP1CrashSelector
		}
		if detail.Checkpoint != "active.file.write" {
			return false, nil
		}
		switch actualPhase {
		case "prepared", "validated", "applying", "verifying", "publishing", "committed", "rolling_back", "rolled_back", "degraded":
		default:
			return false, errP1CrashSelector
		}
		return actualPhase == "rolling_back", nil
	}, nil
}

func TestP1RollbackPublicationIOSelectorMapping(t *testing.T) {
	selector, err := p1RollbackPublicationIOSelector(faultRequest{Point: "rollback-active-publication", Failure: "io-error-or-unverifiable-owner"})
	must(t, err)
	record := p1TraceRecord{Event: p1SelectorEvent("store", "checkpoint", p1JSON(t, p1TraceWrapper{true, p1JSON(t, map[string]string{"checkpoint": "active.file.write"})}))}
	original := copyP1TraceRecord(record)
	for i := 0; i < 2; i++ {
		hit, err := selector(record, "rolling_back")
		if !hit || err != nil {
			t.Fatal("actual rollback write not selected")
		}
	}
	if !reflect.DeepEqual(original, record) {
		t.Fatal("pure selector mutated record")
	}
	for _, phase := range []string{"prepared", "validated", "applying", "verifying", "publishing", "committed", "rolled_back", "degraded"} {
		hit, err := selector(record, phase)
		if hit || err != nil {
			t.Fatal("other legal actual phase selected")
		}
	}
	for _, phase := range []string{"", "unknown", "RollingBack"} {
		hit, err := selector(record, phase)
		if hit || !errors.Is(err, errP1CrashSelector) {
			t.Fatal("invalid actual phase accepted")
		}
	}
	for _, checkpoint := range []string{"active.file.sync", "active.rename", "active.parent-sync", "unrelated.checkpoint"} {
		other := copyP1TraceRecord(record)
		other.Event.Data = p1JSON(t, p1TraceWrapper{true, p1JSON(t, map[string]string{"checkpoint": checkpoint})})
		hit, err := selector(other, "")
		if hit || err != nil {
			t.Fatal("unrelated checkpoint inspected phase")
		}
	}
	for _, field := range []string{"component", "point"} {
		other := copyP1TraceRecord(record)
		if field == "component" {
			other.Event.Component = "unrelated"
		} else {
			other.Event.Point = "unrelated"
		}
		other.Event.Data = nil
		hit, err := selector(other, "")
		if hit || err != nil {
			t.Fatal("unrelated event parsed")
		}
	}
}

func TestP1RollbackPublicationIOSelectorMalformed(t *testing.T) {
	selector, err := p1RollbackPublicationIOSelector(faultRequest{Point: "rollback-active-publication", Failure: "io-error-or-unverifiable-owner"})
	must(t, err)
	for name, raw := range map[string]string{
		"empty": "", "invalid": "{", "null": "null", "missing-wrapper": `{}`,
		"noninjectable": `{"injectable":false,"detail":{"checkpoint":"active.file.write"}}`, "null-injectable": `{"injectable":null,"detail":{"checkpoint":"active.file.write"}}`,
		"missing-detail": `{"injectable":true}`, "null-detail": `{"injectable":true,"detail":null}`,
		"missing-checkpoint": `{"injectable":true,"detail":{}}`, "null-checkpoint": `{"injectable":true,"detail":{"checkpoint":null}}`, "empty-checkpoint": `{"injectable":true,"detail":{"checkpoint":""}}`,
		"numeric-checkpoint": `{"injectable":true,"detail":{"checkpoint":1}}`, "duplicate-checkpoint": `{"injectable":true,"detail":{"checkpoint":"active.file.write","checkpoint":"active.file.write"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			hit, err := selector(p1TraceRecord{Event: p1SelectorEvent("store", "checkpoint", json.RawMessage(raw))}, "rolling_back")
			if hit || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("malformed or noninjectable checkpoint accepted")
			}
		})
	}
}

func TestP1RollbackPublicationIOSelectorRejectsUnsupported(t *testing.T) {
	base := faultRequest{Point: "rollback-active-publication", Failure: "io-error-or-unverifiable-owner"}
	for _, field := range []string{"point", "failure", "artifact", "phase", "cut", "target", "limit"} {
		t.Run(field, func(t *testing.T) {
			f := base
			switch field {
			case "point":
				f.Point = "durable-boundary"
			case "failure":
				f.Failure = "io-error-uncertain"
			case "artifact":
				f.Artifact = "active"
			case "phase":
				f.Phase = "rolling_back"
			case "cut":
				f.Cut = "temporary-write"
			case "target":
				f.Target = &processIdentity{}
			case "limit":
				f.Limit = 1
			}
			selector, err := p1RollbackPublicationIOSelector(f)
			if selector != nil || !errors.Is(err, errP1CrashSelector) {
				t.Fatal("unsupported request accepted")
			}
		})
	}
}
