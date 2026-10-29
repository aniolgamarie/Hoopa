//go:build linux && amd64 && p1integration

package integration

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
	owned "hoopa/internal/runtime"
)

type p1LaunchProof struct {
	RunID                config.RunID
	LauncherPeerPID      int
	LauncherSequence     uint64
	Intent, Birth, Owner originalRef
}

type p1LaunchOwnerReceipt struct {
	Version         int                         `json:"version"`
	Kind            string                      `json:"kind"`
	CaptureID       string                      `json:"capture_id"`
	ObservedAt      time.Time                   `json:"observed_at"`
	RunID           config.RunID                `json:"run_id"`
	Identity        platform.ProcessIdentity    `json:"identity"`
	ProcessIdentity processIdentity             `json:"process_identity"`
	Observation     owned.ControllerObservation `json:"observation"`
	IntentSHA256    string                      `json:"intent_sha256"`
	BirthSHA256     string                      `json:"birth_sha256"`
}

var errP1LaunchProof = errors.New("P1_LAUNCH_PROOF_INVALID")

// The caller has authenticated records and validated run intents against actual
// bundles. These receipts supplement launch identity only, never registration,
// readiness, or process-control authority. Commit updates only after all pass.
func p1ApplyLaunchProofs(records []p1TraceRecord, runs map[string]p1TraceRun, proofs []p1LaunchProof) error {
	updates := map[string]p1TraceRun{}
	for _, proof := range proofs {
		key := string(proof.RunID)
		run, ok := runs[key]
		if !ok || key == "" || proof.LauncherPeerPID <= 0 || proof.LauncherSequence == 0 {
			return errP1LaunchProof
		}
		if _, duplicate := updates[key]; duplicate {
			return errP1LaunchProof
		}
		var launch *p1TraceRecord
		for i := range records {
			r := &records[i]
			if r.PeerPID == proof.LauncherPeerPID && r.Sequence == proof.LauncherSequence && r.Event.RunID == key && r.Event.Component == "runtime" && r.Event.Point == "launcher-started" {
				if launch != nil {
					return errP1LaunchProof
				}
				launch = r
			}
		}
		if launch == nil || launch.Event.OperationID != string(run.intent.OperationID) {
			return errP1LaunchProof
		}
		wrapper, err := p1TraceUnwrap(launch.Event.Data)
		if err != nil {
			return errP1LaunchProof
		}
		var detail struct {
			PID    int              `json:"pid"`
			Intent config.RunIntent `json:"intent"`
		}
		if !p1HistoryJSON(wrapper.Detail, &detail, "pid", "intent") || !reflect.DeepEqual(detail.Intent, run.intent) {
			return errP1LaunchProof
		}
		intentRaw, err := p1HistoryOriginal(proof.Intent, config.MaxRunRecordBytes)
		if err != nil {
			return errP1LaunchProof
		}
		var intent config.RunIntent
		if !p1HistoryJSON(intentRaw, &intent, "version", "run_id", "operation_id", "generation", "manifest_sha256", "binary", "workdir", "arguments", "controller", "config_sha256", "nonce_sha256") || intent.Validate() != nil || !reflect.DeepEqual(intent, run.intent) {
			return errP1LaunchProof
		}
		birthRaw, err := p1HistoryOriginal(proof.Birth, config.MaxRunRecordBytes)
		if err != nil {
			return errP1LaunchProof
		}
		var birth config.RunBirth
		if !p1HistoryJSON(birthRaw, &birth, "version", "run_id", "intent_sha256", "pid", "uid", "boot_id", "start_ticks") || birth.Match(intent, proof.Intent.SHA256) != nil {
			return errP1LaunchProof
		}
		ownerRaw, err := p1HistoryOriginal(proof.Owner, config.MaxRunRecordBytes)
		if err != nil {
			return errP1LaunchProof
		}
		var owner p1LaunchOwnerReceipt
		if !p1HistoryJSON(ownerRaw, &owner, "version", "kind", "capture_id", "observed_at", "run_id", "identity", "process_identity", "observation", "intent_sha256", "birth_sha256") {
			return errP1LaunchProof
		}
		token, err := hex.DecodeString(owner.CaptureID)
		_, offset := owner.ObservedAt.Zone()
		if err != nil || len(token) != 24 || hex.EncodeToString(token) != owner.CaptureID || owner.ObservedAt.IsZero() || offset != 0 || owner.Version != 1 || owner.Kind != "fresh-connected-owner-observation" || owner.RunID != proof.RunID || owner.IntentSHA256 != proof.Intent.SHA256 || owner.BirthSHA256 != proof.Birth.SHA256 {
			return errP1LaunchProof
		}
		native := owner.Identity
		expected := platform.ProcessIdentity{PID: birth.PID, UID: birth.UID, BootID: birth.BootID, StartTicks: birth.StartTicks, BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256}
		observedRun := p1TraceRun{intent: intent, identity: native}
		observation := owned.ControllerObservation{RunID: intent.RunID, Generation: intent.Generation, ConfigSHA256: intent.ConfigSHA256, BinaryVersion: intent.Binary.Version, Gate: "closed"}
		if native != expected || detail.PID != native.PID || owner.ProcessIdentity != observedRun.process() || owner.Observation != observation || (len(run.proof) > 0 && run.identity != native) {
			return errP1LaunchProof
		}
		refs := []originalRef{proof.Intent, proof.Birth, proof.Owner}
		for _, existing := range run.proof {
			for _, ref := range refs {
				if p1HistorySameRef(existing, ref) {
					return errP1LaunchProof
				}
			}
		}
		run.identity = native
		run.proof = append(append([]originalRef(nil), run.proof...), refs...)
		updates[key] = run
	}
	for key, run := range updates {
		runs[key] = run
	}
	return nil
}

// Synthetic receipt files test only the pure validator's binding rules. They
// are not ownership observations, actual launcher captures, or product proof.
func p1LaunchProofFixture(t *testing.T) ([]p1TraceRecord, map[string]p1TraceRun, p1LaunchProof, p1LaunchOwnerReceipt) {
	t.Helper()
	intent := config.RunIntent{Version: 1, RunID: "synthetic-run", OperationID: "synthetic-operation", Generation: "synthetic-generation", ManifestSHA256: strings.Repeat("a", 64), Binary: config.BinaryReference{Path: "/synthetic/core", SHA256: strings.Repeat("b", 64), Version: "synthetic-version"}, Workdir: "/synthetic/work", Arguments: []string{"/synthetic/core"}, Controller: "127.0.0.1:12345", ConfigSHA256: strings.Repeat("c", 64), NonceSHA256: strings.Repeat("d", 64)}
	must(t, intent.Validate())
	proof := p1LaunchProof{RunID: intent.RunID, LauncherPeerPID: 123, LauncherSequence: 7, Intent: p1LaunchProofOriginal(t, intent)}
	birth := config.RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: proof.Intent.SHA256, PID: 456, UID: uint32(os.Geteuid()), BootID: "11111111-1111-1111-1111-111111111111", StartTicks: 1234}
	must(t, birth.Match(intent, proof.Intent.SHA256))
	proof.Birth = p1LaunchProofOriginal(t, birth)
	native := platform.ProcessIdentity{PID: birth.PID, UID: birth.UID, BootID: birth.BootID, StartTicks: birth.StartTicks, BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256}
	run := p1TraceRun{intent: intent, identity: native}
	owner := p1LaunchOwnerReceipt{Version: 1, Kind: "fresh-connected-owner-observation", CaptureID: strings.Repeat("ab", 24), ObservedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), RunID: intent.RunID, Identity: native, ProcessIdentity: run.process(), Observation: owned.ControllerObservation{RunID: intent.RunID, Generation: intent.Generation, ConfigSHA256: intent.ConfigSHA256, BinaryVersion: intent.Binary.Version, Gate: "closed"}, IntentSHA256: proof.Intent.SHA256, BirthSHA256: proof.Birth.SHA256}
	proof.Owner = p1LaunchProofOriginal(t, owner)
	record := p1TraceRecord{PeerPID: proof.LauncherPeerPID, Sequence: proof.LauncherSequence, Event: p1SelectorEvent("runtime", "launcher-started", p1JSON(t, p1TraceWrapper{false, p1JSON(t, map[string]any{"pid": birth.PID, "intent": intent})}))}
	record.Event.RunID = string(intent.RunID)
	record.Event.OperationID = string(intent.OperationID)
	return []p1TraceRecord{record}, map[string]p1TraceRun{string(intent.RunID): {intent: intent}}, proof, owner
}
func p1LaunchProofOriginal(t *testing.T, v any) originalRef {
	t.Helper()
	raw := p1JSON(t, v)
	path := filepath.Join(t.TempDir(), "synthetic-proof.json")
	must(t, os.WriteFile(path, raw, 0600))
	return originalRef{Path: path, Bytes: int64(len(raw)), SHA256: p1SHA(raw)}
}

func TestP1LaunchProofValid(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "gap", true: "later-registration"}[existing], func(t *testing.T) {
			records, runs, proof, owner := p1LaunchProofFixture(t)
			if existing {
				run := runs[string(proof.RunID)]
				run.identity = owner.Identity
				run.proof = []originalRef{{Path: "synthetic-registration", SHA256: strings.Repeat("e", 64), Bytes: 1}}
				runs[string(proof.RunID)] = run
			}
			before := copyP1TraceRecord(records[0])
			must(t, p1ApplyLaunchProofs(records, runs, []p1LaunchProof{proof}))
			run := runs[string(proof.RunID)]
			want := 3
			if existing {
				want = 4
			}
			if run.identity != owner.Identity || len(run.proof) != want || !reflect.DeepEqual(before, records[0]) {
				t.Fatal("supplement binding or immutable record violated")
			}
			if p1ApplyLaunchProofs(records, runs, []p1LaunchProof{proof}) == nil {
				t.Fatal("duplicate applied supplement accepted")
			}
		})
	}
}

func TestP1LaunchProofRejects(t *testing.T) {
	for _, change := range []string{"unknown-run", "peer", "sequence", "event-run", "event-operation", "pid", "detail-intent", "intent-sha", "intent-unknown", "intent-duplicate", "birth-binding", "owner-unknown", "owner-version", "owner-kind", "capture", "time", "run", "intent-binding", "birth-sha", "native", "process", "generation", "config", "version", "gate", "existing-identity", "duplicate-proof", "atomic-batch"} {
		t.Run(change, func(t *testing.T) {
			records, runs, proof, owner := p1LaunchProofFixture(t)
			key := string(proof.RunID)
			proofs := []p1LaunchProof{proof}
			switch change {
			case "unknown-run":
				delete(runs, key)
			case "peer":
				proof.LauncherPeerPID++
			case "sequence":
				proof.LauncherSequence++
			case "event-run":
				records[0].Event.RunID = "other"
			case "event-operation":
				records[0].Event.OperationID = "other"
			case "pid":
				records[0].Event.Data = p1JSON(t, p1TraceWrapper{false, p1JSON(t, map[string]any{"pid": 789, "intent": runs[key].intent})})
			case "detail-intent":
				i := runs[key].intent
				i.Generation = "other"
				records[0].Event.Data = p1JSON(t, p1TraceWrapper{false, p1JSON(t, map[string]any{"pid": owner.Identity.PID, "intent": i})})
			case "intent-sha":
				proof.Intent.SHA256 = strings.Repeat("f", 64)
			case "intent-unknown", "intent-duplicate":
				raw, _ := os.ReadFile(proof.Intent.Path)
				suffix := `,"unknown":1}`
				if change == "intent-duplicate" {
					suffix = `,"version":1}`
				}
				raw = append(raw[:len(raw)-1], []byte(suffix)...)
				proof.Intent = p1LaunchProofOriginal(t, json.RawMessage(raw))
			case "birth-binding":
				var birth config.RunBirth
				raw, _ := os.ReadFile(proof.Birth.Path)
				must(t, json.Unmarshal(raw, &birth))
				birth.IntentSHA256 = strings.Repeat("f", 64)
				proof.Birth = p1LaunchProofOriginal(t, birth)
			case "owner-unknown":
				raw, _ := os.ReadFile(proof.Owner.Path)
				raw = append(raw[:len(raw)-1], []byte(`,"unknown":1}`)...)
				proof.Owner = p1LaunchProofOriginal(t, json.RawMessage(raw))
			case "owner-version":
				owner.Version = 2
			case "owner-kind":
				owner.Kind = "other"
			case "capture":
				owner.CaptureID = "invalid"
			case "time":
				owner.ObservedAt = time.Time{}
			case "run":
				owner.RunID = "other"
			case "intent-binding":
				owner.IntentSHA256 = strings.Repeat("f", 64)
			case "birth-sha":
				owner.BirthSHA256 = strings.Repeat("f", 64)
			case "native":
				owner.Identity.StartTicks++
			case "process":
				owner.ProcessIdentity.PID++
			case "generation":
				owner.Observation.Generation = "other"
			case "config":
				owner.Observation.ConfigSHA256 = strings.Repeat("f", 64)
			case "version":
				owner.Observation.BinaryVersion = "other"
			case "gate":
				owner.Observation.Gate = "open"
			case "existing-identity":
				run := runs[key]
				run.identity = owner.Identity
				run.identity.PID++
				run.proof = []originalRef{{Path: "synthetic-registration"}}
				runs[key] = run
			case "duplicate-proof":
				proofs = append(proofs, proof)
			case "atomic-batch":
				bad := proof
				bad.RunID = "unknown"
				proofs = append(proofs, bad)
			}
			switch change {
			case "owner-version", "owner-kind", "capture", "time", "run", "intent-binding", "birth-sha", "native", "process", "generation", "config", "version", "gate":
				proof.Owner = p1LaunchProofOriginal(t, owner)
			}
			proofs[0] = proof
			before := map[string]p1TraceRun{}
			for k, v := range runs {
				before[k] = v
			}
			if p1ApplyLaunchProofs(records, runs, proofs) == nil {
				t.Fatal("invalid synthetic supplement accepted")
			}
			if !reflect.DeepEqual(before, runs) {
				t.Fatal("failed validation mutated runs")
			}
		})
	}
}
