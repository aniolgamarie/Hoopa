//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

type resultCodecFixture struct {
	context sourceUpdateContext
	stored  StoredOperation
}

var resultSuccessPhases = []Phase{Prepared, Validated, Applying, Verifying, Publishing, Committed}

func resultFixture(t *testing.T, phases []Phase, reason string, declared, regions bool) resultCodecFixture {
	t.Helper()
	ctx := updateContextFixture()
	params := updateRequestParams()
	if declared {
		intent := updateRequestIntent(t)
		intent["subscriptions"].([]any)[0].(map[string]any)["source"] = map[string]any{"kind": "file", "path": ctx.Source.Source.Path}
		params["intent"] = intent
	}
	if regions {
		params["region_rules"] = ctx.RegionRules
	}
	r, _ := updateRequestParse(t, params)
	ctx.OperationID = r.OperationID()
	ctx.RequestDigest = r.Digest()
	ctxRaw, err := encodeSourceUpdateContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identity := Identity{OperationID: r.OperationID(), RequestID: r.ID(), RequestDigest: r.Digest(), APIVersion: "v1", Operation: "subscription.update", Intent: Intent{BaseRevision: ctx.Base.Revision, PreviousGeneration: ctx.Base.Generation, CandidateDigest: hash(ctxRaw), ImpactDigest: hash(r.params)}}
	o := Operation{Version: 1, Identity: identity, Request: retainedJSON(bytes.Clone(r.raw))}
	previousDownloaded := false
	for n, phase := range phases {
		receipt := sourceReceipt(ctx, hash(ctxRaw))
		receipt.Phase = phase
		finished := "2026-10-28T00:00:02.123456789Z"
		switch phase {
		case Verifying:
			receipt.Downloaded = true
		case Publishing, Committed:
			receipt.Downloaded = true
			receipt.Validated = phase == Committed
			receipt.AssetSHA256 = strings.Repeat("e", 64)
			receipt.SnapshotID = "s-fixture-candidate"
			receipt.FinishedAt = &finished
		case RollingBack, RolledBack:
			receipt.Downloaded = previousDownloaded
			receipt.FinishedAt = &finished
			receipt.Reason = reason
		}
		raw, err := encodeSourceUpdateResult(r.ID(), receipt)
		if err != nil {
			t.Fatalf("encode legal %s receipt: %v", phase, err)
		}
		o.Steps = append(o.Steps, Step{Sequence: n + 1, Phase: phase, Evidence: retainedJSON(`{}`), Result: retainedJSON(raw)})
		o.Phase = phase
		o.Result = retainedJSON(bytes.Clone(raw))
		previousDownloaded = receipt.Downloaded
	}
	o.BodySHA256 = bodyHash(o)
	if err = o.validate(o.Identity.OperationID); err != nil {
		t.Fatal("invalid generic fixture", err)
	}
	f := resultCodecFixture{context: ctx, stored: StoredOperation{Record: o}}
	resultRefresh(t, &f)
	return f
}
func resultRefresh(t *testing.T, f *resultCodecFixture) {
	t.Helper()
	o := &f.stored.Record
	if !o.Compacted {
		o.BodySHA256 = bodyHash(*o)
	}
	raw, err := encode(*o)
	if err != nil {
		t.Fatal(err)
	}
	f.stored.SHA256 = hash(raw)
}
func resultCompact(t *testing.T, f resultCodecFixture) resultCodecFixture {
	t.Helper()
	f.stored.Record.Request = nil
	f.stored.Record.Steps = nil
	f.stored.Record.Compacted = true
	resultRefresh(t, &f)
	return f
}
func resultReceipt(t *testing.T, f resultCodecFixture, n int) sourceUpdateReceipt {
	t.Helper()
	step := f.stored.Record.Steps[n]
	r, err := decodeSourceUpdateResult(step.Result, f.stored.Record.Identity, step.Phase)
	if err != nil {
		t.Fatal("bad fixture receipt", err)
	}
	return r
}
func resultChangeReceipt(t *testing.T, f *resultCodecFixture, n int, change func(*sourceUpdateReceipt)) {
	t.Helper()
	r := resultReceipt(t, *f, n)
	change(&r)
	raw, err := encodeSourceUpdateResult(f.stored.Record.Identity.RequestID, r)
	if err != nil {
		t.Fatal("mutation must remain a valid local receipt", err)
	}
	f.stored.Record.Steps[n].Result = retainedJSON(raw)
	if n == len(f.stored.Record.Steps)-1 {
		f.stored.Record.Result = retainedJSON(bytes.Clone(raw))
	}
	resultRefresh(t, f)
}
func resultRejectRecord(t *testing.T, f resultCodecFixture) {
	t.Helper()
	r, err := decodeSourceUpdateRecord(f.stored, f.context)
	if err != ErrOperationRecord || !reflect.DeepEqual(r, sourceUpdateReceipt{}) {
		t.Fatal("record accepted invalid binding/history or returned partial receipt")
	}
}
func resultRejectRaw(t *testing.T, raw []byte, identity Identity, phase Phase) {
	t.Helper()
	r, err := decodeSourceUpdateResult(raw, identity, phase)
	if err != ErrOperationRecord || !reflect.DeepEqual(r, sourceUpdateReceipt{}) {
		t.Fatal("result accepted invalid schema/binding or returned partial receipt")
	}
}
func resultJSON(t *testing.T, raw []byte, change func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("invalid fixture JSON")
	}
	change(m)
	next, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	next, err = config.CanonicalJSON(next)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func resultRebindContext(t *testing.T, f *resultCodecFixture, change func(*sourceUpdateContext)) {
	t.Helper()
	old := f.stored.Record.Identity
	change(&f.context)
	raw, err := encodeSourceUpdateContext(f.context)
	if err != nil {
		t.Fatal("context mutation must be well formed", err)
	}
	sha := hash(raw)
	for n, step := range f.stored.Record.Steps {
		r, err := decodeSourceUpdateResult(step.Result, old, step.Phase)
		if err != nil {
			t.Fatal(err)
		}
		r.ContextSHA256 = sha
		raw, err := encodeSourceUpdateResult(old.RequestID, r)
		if err != nil {
			t.Fatal(err)
		}
		f.stored.Record.Steps[n].Result = retainedJSON(raw)
		if n == len(f.stored.Record.Steps)-1 {
			f.stored.Record.Result = retainedJSON(bytes.Clone(raw))
		}
	}
	f.stored.Record.Identity.Intent.CandidateDigest = sha
	resultRefresh(t, f)
}
func TestSourceUpdateResultSuccessPhasesAndPublicShape(t *testing.T) {
	for n, phase := range resultSuccessPhases {
		t.Run(string(phase), func(t *testing.T) {
			f := resultFixture(t, resultSuccessPhases[:n+1], "", true, true)
			r, err := decodeSourceUpdateRecord(f.stored, f.context)
			if err != nil || r.Phase != phase || r.SourceID != f.context.Source.ID || r.Sequence != f.context.Sequence {
				t.Fatal("legal success prefix rejected", err)
			}
			if r.Downloaded != (n >= 3) || r.Validated != (phase == Committed) {
				t.Fatal("stage flags disagree with phase")
			}
			if n < 4 && (r.AssetSHA256 != "" || r.SnapshotID != "" || r.FinishedAt != nil) {
				t.Fatal("early phase fabricated candidate")
			}
			var response map[string]any
			if json.Unmarshal(f.stored.Record.Result, &response) != nil {
				t.Fatal("invalid response")
			}
			if len(response) != 6 || response["api_version"] != "v1" || response["request_id"] != f.stored.Record.Identity.RequestID || response["ok"] != true || response["error"] != nil {
				t.Fatal("independent success envelope shape")
			}
			warnings, ok := response["warnings"].([]any)
			if !ok || len(warnings) != 0 {
				t.Fatal("warnings must be explicit empty array")
			}
			data, ok := response["data"].(map[string]any)
			if !ok || len(data) != 12 || data["phase"] != string(phase) || data["validated"] != (phase == Committed) || data["downloaded"] != (n >= 3) || data["reason"] != "" {
				t.Fatal("independent public receipt shape")
			}
		})
	}
	f := resultFixture(t, resultSuccessPhases, "", true, true)
	compact := resultCompact(t, f)
	r, err := decodeSourceUpdateRecord(compact.stored, compact.context)
	if err != nil || r.Phase != Committed || !r.Validated || r.AssetSHA256 != strings.Repeat("e", 64) {
		t.Fatal("compacted successful terminal lost candidate binding", err)
	}
	// Optional declarations/policy are resolved elsewhere when absent; this pure
	// codec validates the frozen context, never reads a committed Intent or asset.
	for _, opts := range [][2]bool{{false, false}, {false, true}, {true, false}} {
		f := resultFixture(t, resultSuccessPhases, "", opts[0], opts[1])
		if _, err := decodeSourceUpdateRecord(f.stored, f.context); err != nil {
			t.Fatal("omitted optional input rejected", err)
		}
	}
}
func TestSourceUpdateResultFailurePhasesAndCompactedAssertions(t *testing.T) {
	for _, reason := range []string{"download_failed", "validation_failed", "storage_failed", "input_changed", "cancelled", "interrupted"} {
		starts := []int{1, 2, 3, 4, 5}
		if reason == "download_failed" {
			starts = []int{3}
		}
		if reason == "validation_failed" || reason == "storage_failed" {
			starts = []int{4}
		}
		for _, count := range starts {
			t.Run(reason+"/"+string(resultSuccessPhases[count-1]), func(t *testing.T) {
				phases := append(append([]Phase{}, resultSuccessPhases[:count]...), RollingBack, RolledBack)
				f := resultFixture(t, phases, reason, true, true)
				for _, end := range []int{len(phases) - 1, len(phases)} {
					prefix := resultFixture(t, phases[:end], reason, true, true)
					r, err := decodeSourceUpdateRecord(prefix.stored, prefix.context)
					if err != nil || r.Reason != reason || r.Validated || r.Downloaded != (count >= 4) || r.AssetSHA256 != "" || r.SnapshotID != "" {
						t.Fatal("legal failure phase rejected", err)
					}
				}
				r, err := decodeSourceUpdateRecord(f.stored, f.context)
				if err != nil {
					t.Fatal(err)
				}
				var response map[string]any
				json.Unmarshal(f.stored.Record.Result, &response)
				failure, ok := response["error"].(map[string]any)
				if !ok || response["ok"] != false || response["data"] != nil || len(failure) != 4 || failure["code"] != "SUBSCRIPTION_UPDATE_FAILED" || failure["message"] != "Subscription update did not produce a usable candidate" || failure["retryable"] != false {
					t.Fatal("independent fixed failure envelope shape")
				}
				details := failure["details"].(map[string]any)
				if details["reason"] != reason || details["validated"] != false || details["phase"] != "rolled_back" {
					t.Fatal("failure details changed")
				}
				compact := resultCompact(t, f)
				got, err := decodeSourceUpdateRecord(compact.stored, compact.context)
				if err != nil || !reflect.DeepEqual(got, r) {
					t.Fatal("compacted failure lost terminal assertion", err)
				}
			})
		}
	}
}
func TestSourceUpdateResultLocalReceiptAndIdentityValidation(t *testing.T) {
	prepared := resultFixture(t, resultSuccessPhases[:1], "", true, true)
	base := resultReceipt(t, prepared, 0)
	for name, change := range map[string]func(*sourceUpdateReceipt){"operation": func(r *sourceUpdateReceipt) { r.OperationID = "op-short" }, "source": func(r *sourceUpdateReceipt) { r.SourceID = "bad source" }, "base": func(r *sourceUpdateReceipt) { r.BaseRevision = "bad revision" }, "context": func(r *sourceUpdateReceipt) { r.ContextSHA256 = "bad" }, "sequence-zero": func(r *sourceUpdateReceipt) { r.Sequence = 0 }, "sequence-over": func(r *sourceUpdateReceipt) { r.Sequence = 1 << 53 }, "phase": func(r *sourceUpdateReceipt) { r.Phase = Degraded }, "early-download": func(r *sourceUpdateReceipt) { r.Downloaded = true }, "early-validation": func(r *sourceUpdateReceipt) { r.Validated = true }, "early-asset": func(r *sourceUpdateReceipt) { r.AssetSHA256 = strings.Repeat("e", 64) }, "early-snapshot": func(r *sourceUpdateReceipt) { r.SnapshotID = "s-forged" }, "early-time": func(r *sourceUpdateReceipt) { s := "2026-10-28T00:00:02Z"; r.FinishedAt = &s }, "early-reason": func(r *sourceUpdateReceipt) { r.Reason = "cancelled" }} {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			raw, err := encodeSourceUpdateResult(prepared.stored.Record.Identity.RequestID, r)
			if raw != nil || err != ErrOperationRecord {
				t.Fatal("invalid receipt encoded")
			}
		})
	}
	for _, requestID := range []string{"", "different-request", strings.Repeat("x", 129)} {
		raw, err := encodeSourceUpdateResult(requestID, base)
		if raw != nil || err != ErrOperationRecord {
			t.Fatal("request identity mismatch encoded")
		}
	}
	for _, phase := range []Phase{Verifying, Publishing, Committed, RollingBack, RolledBack} {
		f := resultFixture(t, resultSuccessPhases, "", true, true)
		r := resultReceipt(t, f, 3)
		r.Phase = phase
		switch phase {
		case Verifying:
			r.Downloaded = false
		case Publishing, Committed:
			r.Downloaded = true
			r.Validated = phase == Committed
			r.AssetSHA256 = strings.Repeat("e", 64)
			r.SnapshotID = "s-valid"
		case RollingBack, RolledBack:
			r.Downloaded = true
			r.Reason = "unbounded-private-error"
			finished := "2026-10-28T00:00:02Z"
			r.FinishedAt = &finished
		}
		if raw, err := encodeSourceUpdateResult(f.stored.Record.Identity.RequestID, r); raw != nil || err != ErrOperationRecord {
			t.Fatal("invalid finite phase accepted")
		}
	}
	f := resultFixture(t, resultSuccessPhases, "", true, true)
	good := resultReceipt(t, f, 5)
	for _, finished := range []string{"0001-01-01T00:00:00Z", "2026-10-28T00:00:02+00:00", "2026-10-28T00:00:02.100Z", "not a time"} {
		r := good
		r.FinishedAt = &finished
		if raw, err := encodeSourceUpdateResult(f.stored.Record.Identity.RequestID, r); raw != nil || err != ErrOperationRecord {
			t.Fatal("invalid time accepted")
		}
	}
	for _, field := range []string{"request-id", "operation-id", "base", "context", "phase"} {
		identity := f.stored.Record.Identity
		phase := Committed
		switch field {
		case "request-id":
			identity.RequestID = "other"
		case "operation-id":
			identity.OperationID = config.ID("op-" + strings.Repeat("f", 64))
		case "base":
			identity.Intent.BaseRevision = "other-revision"
		case "context":
			identity.Intent.CandidateDigest = strings.Repeat("f", 64)
		case "phase":
			phase = Publishing
		}
		resultRejectRaw(t, f.stored.Record.Result, identity, phase)
	}
}
func TestSourceUpdateResultStrictJCSSchemaAndPresence(t *testing.T) {
	for _, failure := range []bool{false, true} {
		phases := resultSuccessPhases
		reason := ""
		if failure {
			phases = append(append([]Phase{}, resultSuccessPhases[:4]...), RollingBack, RolledBack)
			reason = "validation_failed"
		}
		f := resultFixture(t, phases, reason, true, true)
		raw := []byte(f.stored.Record.Result)
		identity := f.stored.Record.Identity
		phase := f.stored.Record.Phase
		cases := [][]byte{nil, []byte(`null`), []byte(`[]`), raw[:len(raw)-1], append([]byte(" "), raw...), append(append([]byte{}, raw...), '\n'), append(append([]byte{}, raw...), []byte(`{}`)...), append([]byte(`{"ok":true,`), raw[1:]...), bytes.Replace(raw, []byte(`"api_version"`), []byte(`"API_VERSION"`), 1), bytes.Replace(raw, []byte(`"sequence":1`), []byte(`"sequence":1.0`), 1), bytes.Replace(raw, []byte(`"source-a"`), []byte(`"source\u002da"`), 1), bytes.Repeat([]byte{' '}, MaxResultBytes+1)}
		for _, change := range []func(map[string]any){func(m map[string]any) { m["foreign"] = "private-callback-error" }, func(m map[string]any) { m["warnings"] = nil }, func(m map[string]any) { m["warnings"] = []any{"private"} }, func(m map[string]any) { m["api_version"] = "v2" }, func(m map[string]any) { m["request_id"] = "other" }, func(m map[string]any) { m["ok"] = !m["ok"].(bool) }} {
			cases = append(cases, resultJSON(t, raw, change))
		}
		for _, key := range []string{"api_version", "request_id", "ok", "data", "error", "warnings"} {
			key := key
			cases = append(cases, resultJSON(t, raw, func(m map[string]any) { delete(m, key) }))
		}
		locate := func(m map[string]any) map[string]any {
			if failure {
				return m["error"].(map[string]any)["details"].(map[string]any)
			}
			return m["data"].(map[string]any)
		}
		for _, key := range []string{"operation_id", "phase", "base_revision", "context_sha256", "subscription_id", "sequence", "downloaded", "validated", "asset_sha256", "snapshot_id", "finished_at", "reason"} {
			key := key
			cases = append(cases, resultJSON(t, raw, func(m map[string]any) { delete(locate(m), key) }), resultJSON(t, raw, func(m map[string]any) { locate(m)[key] = nil }))
		}
		cases = append(cases, resultJSON(t, raw, func(m map[string]any) { locate(m)["foreign"] = false }), resultJSON(t, raw, func(m map[string]any) { d := locate(m); d["Downloaded"] = d["downloaded"]; delete(d, "downloaded") }))
		if failure {
			for _, key := range []string{"code", "message", "retryable", "details"} {
				key := key
				cases = append(cases, resultJSON(t, raw, func(m map[string]any) { delete(m["error"].(map[string]any), key) }))
			}
			cases = append(cases, resultJSON(t, raw, func(m map[string]any) { m["error"].(map[string]any)["message"] = "private-callback-error" }), resultJSON(t, raw, func(m map[string]any) { m["error"].(map[string]any)["retryable"] = true }))
		}
		for _, bad := range cases {
			resultRejectRaw(t, bad, identity, phase)
		}
	}
	// Pending fields that legitimately equal false/empty/null still require their
	// explicit scalar spelling and presence rather than Go decoding defaults.
	f := resultFixture(t, resultSuccessPhases[:1], "", true, true)
	raw := []byte(f.stored.Record.Result)
	for _, key := range []string{"downloaded", "validated", "asset_sha256", "snapshot_id", "reason"} {
		resultRejectRaw(t, resultJSON(t, raw, func(m map[string]any) { m["data"].(map[string]any)[key] = nil }), f.stored.Record.Identity, Prepared)
	}
	resultRejectRaw(t, resultJSON(t, raw, func(m map[string]any) { delete(m["data"].(map[string]any), "finished_at") }), f.stored.Record.Identity, Prepared)
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"subscription_id":"source-a"`), []byte(`"subscription_id":"\ud800"`), 1), bytes.Replace(raw, []byte(`"subscription_id":"source-a"`), []byte{'"', 's', 'u', 'b', 's', 'c', 'r', 'i', 'p', 't', 'i', 'o', 'n', '_', 'i', 'd', '"', ':', '"', 255, '"'}, 1)} {
		resultRejectRaw(t, bad, f.stored.Record.Identity, Prepared)
	}
}
func TestSourceUpdateResultRecordBindingsAndOriginalRequest(t *testing.T) {
	for name, change := range map[string]func(*resultCodecFixture){
		"impact": func(f *resultCodecFixture) { f.stored.Record.Identity.Intent.ImpactDigest = strings.Repeat("f", 64) },
		"target-revision": func(f *resultCodecFixture) {
			r := config.Revision("revision-target")
			f.stored.Record.Identity.Intent.TargetRevision = &r
		},
		"target-generation": func(f *resultCodecFixture) {
			g := config.Generation("generation-target")
			f.stored.Record.Identity.Intent.TargetGeneration = &g
		},
		"previous-generation": func(f *resultCodecFixture) {
			g := config.Generation("generation-other")
			f.stored.Record.Identity.Intent.PreviousGeneration = &g
		},
		"context-digest":    func(f *resultCodecFixture) { f.context.SettingsSHA256 = strings.Repeat("f", 64) },
		"context-operation": func(f *resultCodecFixture) { f.context.OperationID = config.ID("op-" + strings.Repeat("f", 64)) },
		"context-request":   func(f *resultCodecFixture) { f.context.RequestDigest = strings.Repeat("f", 64) },
		"request-body": func(f *resultCodecFixture) {
			f.stored.Record.Request = retainedJSON(`{"request_id":"different","api_version":"v1","operation":"subscription.update","params":{}}`)
		},
		"evidence": func(f *resultCodecFixture) {
			f.stored.Record.Steps[0].Evidence = retainedJSON(`{"private":"not a result proof"}`)
		},
		"step-sequence":       func(f *resultCodecFixture) { f.stored.Record.Steps[1].Sequence = 20 },
		"step-result-binding": func(f *resultCodecFixture) { f.stored.Record.Steps[0].Result = f.stored.Record.Steps[1].Result },
	} {
		t.Run(name, func(t *testing.T) {
			f := resultFixture(t, resultSuccessPhases, "", true, true)
			change(&f)
			resultRefresh(t, &f)
			resultRejectRecord(t, f)
		})
	}
	for name, change := range map[string]func(*sourceUpdateContext){"source": func(c *sourceUpdateContext) { c.Source.ID = "source-other" }, "sequence": func(c *sourceUpdateContext) { c.Sequence++ }, "definition": func(c *sourceUpdateContext) { c.Source.Source.Path = "/private/other-source.yaml" }, "policy-unmatched": func(c *sourceUpdateContext) { c.RegionRules[1].Region = "CA" }} {
		t.Run("rebound-context-"+name, func(t *testing.T) {
			f := resultFixture(t, resultSuccessPhases, "", true, true)
			resultRebindContext(t, &f, change)
			resultRejectRecord(t, f)
		})
	}
	for _, change := range []func(*resultCodecFixture){func(f *resultCodecFixture) { f.stored.Record.Phase = Publishing }, func(f *resultCodecFixture) { f.stored.Record.Request = retainedJSON(`{}`) }, func(f *resultCodecFixture) { f.stored.Record.Steps = []Step{} }, func(f *resultCodecFixture) { f.stored.Record.Identity.Operation = "config.apply" }, func(f *resultCodecFixture) { f.context.Sequence++ }} {
		f := resultCompact(t, resultFixture(t, resultSuccessPhases, "", true, true))
		change(&f)
		resultRefresh(t, &f)
		resultRejectRecord(t, f)
	}
}
func TestSourceUpdateResultStageHistoryIsImmutable(t *testing.T) {
	for _, field := range []string{"asset", "snapshot", "time"} {
		t.Run("publishing-"+field, func(t *testing.T) {
			f := resultFixture(t, resultSuccessPhases, "", true, true)
			resultChangeReceipt(t, &f, 5, func(r *sourceUpdateReceipt) {
				switch field {
				case "asset":
					r.AssetSHA256 = strings.Repeat("f", 64)
				case "snapshot":
					r.SnapshotID = "s-different"
				case "time":
					finished := "2026-10-28T00:00:03Z"
					r.FinishedAt = &finished
				}
			})
			resultRejectRecord(t, f)
		})
	}
	for _, downloaded := range []bool{false, true} {
		for _, field := range []string{"download", "reason", "time"} {
			prefix := 3
			if downloaded {
				prefix = 4
			}
			phases := append(append([]Phase{}, resultSuccessPhases[:prefix]...), RollingBack, RolledBack)
			f := resultFixture(t, phases, "cancelled", true, true)
			last := len(phases) - 1
			resultChangeReceipt(t, &f, last, func(r *sourceUpdateReceipt) {
				switch field {
				case "download":
					r.Downloaded = !r.Downloaded
				case "reason":
					r.Reason = "interrupted"
				case "time":
					finished := "2026-10-28T00:00:03Z"
					r.FinishedAt = &finished
				}
			})
			resultRejectRecord(t, f)
		}
	}
	// Valid local receipts with impossible causal histories: download cannot fail
	// before Applying, and parse/storage failure cannot begin after Publishing.
	for _, prefix := range []int{1, 2} {
		phases := append(append([]Phase{}, resultSuccessPhases[:prefix]...), RollingBack, RolledBack)
		f := resultFixture(t, phases, "download_failed", true, true)
		resultRejectRecord(t, f)
	}
	for _, reason := range []string{"validation_failed", "storage_failed"} {
		phases := append(append([]Phase{}, resultSuccessPhases[:5]...), RollingBack, RolledBack)
		f := resultFixture(t, phases, reason, true, true)
		resultRejectRecord(t, f)
	}
	// Prepared -> rollback false-to-true cannot manufacture a completed download,
	// even for generic cancellation, nor can Verifying -> rollback lose that fact.
	for _, prefix := range []int{1, 4} {
		phases := append(append([]Phase{}, resultSuccessPhases[:prefix]...), RollingBack, RolledBack)
		f := resultFixture(t, phases, "cancelled", true, true)
		for n := len(phases) - 2; n < len(phases); n++ {
			resultChangeReceipt(t, &f, n, func(r *sourceUpdateReceipt) { r.Downloaded = !r.Downloaded })
		}
		resultRejectRecord(t, f)
	}
}

func TestSourceUpdateResultPhaseUnionMasks(t *testing.T) {
	success := resultFixture(t, resultSuccessPhases, "", true, true)
	for _, n := range []int{4, 5} {
		for name, change := range map[string]func(*sourceUpdateReceipt){
			"download":         func(r *sourceUpdateReceipt) { r.Downloaded = false },
			"validated":        func(r *sourceUpdateReceipt) { r.Validated = !r.Validated },
			"asset-empty":      func(r *sourceUpdateReceipt) { r.AssetSHA256 = "" },
			"asset-case":       func(r *sourceUpdateReceipt) { r.AssetSHA256 = strings.Repeat("E", 64) },
			"snapshot-empty":   func(r *sourceUpdateReceipt) { r.SnapshotID = "" },
			"snapshot-invalid": func(r *sourceUpdateReceipt) { r.SnapshotID = "bad snapshot" },
			"finish":           func(r *sourceUpdateReceipt) { r.FinishedAt = nil },
			"reason":           func(r *sourceUpdateReceipt) { r.Reason = "cancelled" },
		} {
			t.Run(string(resultSuccessPhases[n])+"/"+name, func(t *testing.T) {
				r := resultReceipt(t, success, n)
				change(&r)
				if raw, err := encodeSourceUpdateResult(success.stored.Record.Identity.RequestID, r); raw != nil || err != ErrOperationRecord {
					t.Fatal("phase union accepted a single invalid fact")
				}
			})
		}
	}
	for _, reason := range []string{"download_failed", "validation_failed", "storage_failed"} {
		prefix := 3
		if reason != "download_failed" {
			prefix = 4
		}
		phases := append(append([]Phase{}, resultSuccessPhases[:prefix]...), RollingBack, RolledBack)
		f := resultFixture(t, phases, reason, true, true)
		for _, change := range []func(*sourceUpdateReceipt){
			func(r *sourceUpdateReceipt) { r.Downloaded = !r.Downloaded },
			func(r *sourceUpdateReceipt) { r.Validated = true },
			func(r *sourceUpdateReceipt) { r.FinishedAt = nil },
			func(r *sourceUpdateReceipt) { r.AssetSHA256 = strings.Repeat("e", 64) },
			func(r *sourceUpdateReceipt) { r.SnapshotID = "s-forged" },
		} {
			r := resultReceipt(t, f, len(phases)-1)
			change(&r)
			if raw, err := encodeSourceUpdateResult(f.stored.Record.Identity.RequestID, r); raw != nil || err != ErrOperationRecord {
				t.Fatal("failure union accepted a single invalid fact")
			}
		}
	}
}
