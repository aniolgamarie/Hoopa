//go:build linux

package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

// Synthetic record bodies exercise persistence, not business result truth.
func boundsObject(t *testing.T, size int) json.RawMessage {
	t.Helper()
	const shell = `{"text":""}`
	if size < len(shell) {
		t.Fatal("invalid fixture size")
	}
	return json.RawMessage(`{"text":"` + strings.Repeat("x", size-len(shell)) + `"}`)
}
func boundsUnchanged(t *testing.T, j *Journal, p config.Paths, old StoredOperation, before map[string]string) {
	t.Helper()
	got, err := j.Get(old.Record.Identity.OperationID)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatal("rejection changed old record", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, p.State)) {
		t.Fatal("rejected update changed bytes/mode/mtime or created a record")
	}
}
func TestOperationBoundsRequestsAndResults(t *testing.T) {
	if MaxRequestBytes != 128<<10 || MaxResultBytes != 64<<10 || MaxStepBytes != 512<<10 || MaxSteps != 128 {
		t.Fatal("journal contract budget changed")
	}
	t.Run("request-input-and-canonical-expansion", func(t *testing.T) {
		j, _, p := writer(t)
		old := admit(t, j, request(t, "existing", "v1", "config.apply", `{}`))
		before := snapshot(t, p.State)
		prefix := `{"api_version":"v1","request_id":"exact","operation":"config.apply","params":{"text":"`
		suffix := `"}}`
		exact := []byte(prefix + strings.Repeat("x", MaxRequestBytes-len(prefix)-len(suffix)) + suffix)
		over := append(append([]byte(nil), exact...), ' ')
		// CanonicalJSON permits output expansion; the Request boundary independently
		// enforces its final normalized 128 KiB budget.
		numbers := strings.TrimSuffix(strings.Repeat("1e15,", (MaxRequestBytes-200)/5), ",")
		expanded := []byte(`{"api_version":"v1","request_id":"expanded","operation":"config.apply","params":{"numbers":[` + numbers + `]}}`)
		if len(expanded) > MaxRequestBytes {
			t.Fatal("bad fixture")
		}
		for _, raw := range [][]byte{over, expanded} {
			if _, err := ParseRequest(raw); !errors.Is(err, ErrRequest) {
				t.Fatal("oversize request accepted", err)
			}
			boundsUnchanged(t, j, p, old, before)
		}
		r, err := ParseRequest(exact)
		if err != nil {
			t.Fatal("exact request bound rejected", err)
		}
		accepted, created, err := j.Admit(r, intent(), json.RawMessage(`{}`))
		if err != nil || !created || len(accepted.Record.Request) != MaxRequestBytes {
			t.Fatal("exact request not persisted", err)
		}
	})
	t.Run("result-input-and-canonical-expansion", func(t *testing.T) {
		j, _, p := writer(t)
		r := request(t, "result-bound", "v1", "config.apply", `{}`)
		op, created, err := j.Admit(r, intent(), boundsObject(t, MaxResultBytes))
		if err != nil || !created || len(op.Record.Result) != MaxResultBytes {
			t.Fatal("exact result bound rejected", err)
		}
		before := snapshot(t, p.State)
		large := boundsObject(t, MaxResultBytes+1)
		expanded := json.RawMessage(`{"numbers":[` + strings.TrimSuffix(strings.Repeat("1e15,", (MaxResultBytes-100)/5), ",") + `]}`)
		if len(expanded) > MaxResultBytes {
			t.Fatal("bad expansion fixture")
		}
		for _, bad := range []json.RawMessage{large, expanded} {
			if _, err = j.Advance(r.OperationID(), op.SHA256, Validated, json.RawMessage(`{}`), bad); !errors.Is(err, ErrOperationRecord) {
				t.Fatal("oversized update accepted", err)
			}
			if _, made, e := j.Admit(request(t, "new-bad-result", "v1", "config.apply", `{}`), intent(), bad); made || !errors.Is(e, ErrOperationRecord) {
				t.Fatal("oversized initial result admitted", e)
			}
			boundsUnchanged(t, j, p, op, before)
		}
	})
}
func TestOperationBoundsStepBudgets(t *testing.T) {
	t.Run("normalized-cumulative-bytes", func(t *testing.T) {
		j, _, p := writer(t)
		r := request(t, "step-bytes", "v1", "config.apply", `{}`)
		op := admit(t, j, r)
		for i := 0; i < 3; i++ {
			var err error
			op, err = j.Advance(r.OperationID(), op.SHA256, Prepared, boundsObject(t, MaxRequestBytes), json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
		}
		total := 0
		for _, s := range op.Record.Steps {
			total += len(s.Evidence) + len(s.Result)
		}
		remaining := MaxStepBytes - total - len(`{}`)
		if remaining > MaxRequestBytes || remaining < 10 {
			t.Fatal("bad remaining evidence fixture")
		}
		var err error
		op, err = j.Advance(r.OperationID(), op.SHA256, Prepared, boundsObject(t, remaining), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal("exact aggregate budget rejected", err)
		}
		total = 0
		for _, s := range op.Record.Steps {
			total += len(s.Evidence) + len(s.Result)
		}
		if total != MaxStepBytes {
			t.Fatal("not at exact normalized cumulative budget")
		}
		before := snapshot(t, p.State)
		if _, err = j.Advance(r.OperationID(), op.SHA256, Prepared, json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, ErrOperationRecord) {
			t.Fatal("aggregate overflow accepted", err)
		}
		boundsUnchanged(t, j, p, op, before)
	})
	t.Run("single-evidence-limit", func(t *testing.T) {
		j, _, p := writer(t)
		r := request(t, "one-evidence", "v1", "config.apply", `{}`)
		op := admit(t, j, r)
		before := snapshot(t, p.State)
		if _, err := j.Advance(r.OperationID(), op.SHA256, Validated, boundsObject(t, MaxRequestBytes+1), json.RawMessage(`{}`)); !errors.Is(err, ErrOperationRecord) {
			t.Fatal("large evidence accepted", err)
		}
		boundsUnchanged(t, j, p, op, before)
	})
	t.Run("step-count", func(t *testing.T) {
		j, _, p := writer(t)
		r := request(t, "count-bound", "v1", "config.apply", `{}`)
		initial := admit(t, j, r)
		record := initial.Record
		// Seed explicitly synthetic 128-step history through the real validated
		// publication path; this avoids 127 redundant fsync rounds, not validation.
		for n := len(record.Steps); n < MaxSteps; n++ {
			record.Steps = append(record.Steps, Step{n + 1, Prepared, []byte(`{}`), record.Result})
		}
		record.BodySHA256 = bodyHash(record)
		op, err := j.publish(record, initial.SHA256)
		if err != nil || len(op.Record.Steps) != MaxSteps {
			t.Fatal("exact step count rejected", err)
		}
		before := snapshot(t, p.State)
		if _, err = j.Advance(r.OperationID(), op.SHA256, Prepared, json.RawMessage(`{}`), json.RawMessage(record.Result)); !errors.Is(err, ErrOperationRecord) {
			t.Fatal("step overflow accepted", err)
		}
		boundsUnchanged(t, j, p, op, before)
	})
}

func TestOperationBoundsCanonicalBodyAndCompactReplay(t *testing.T) {
	for _, terminalPhase := range []Phase{Committed, RolledBack} {
		t.Run(string(terminalPhase), func(t *testing.T) {
			j, s, p := writer(t)
			r := request(t, "原件\x00/😀-"+string(terminalPhase), "v1", "config.apply", `{ "b": -0.0, "a": [2,1] }`)
			op := admit(t, j, r)
			phases := []Phase{Validated, Applying, Verifying, Publishing, Committed}
			if terminalPhase == RolledBack {
				phases = []Phase{RollingBack, RolledBack}
			}
			resultInput := json.RawMessage("{\"warnings\":[\"<>&\\u2028\\u2029😀\",\"literal \\\\u2028 and \\\\u2029\"],\"outcome\":\"" + string(terminalPhase) + "\",\"error\":null}")
			expected := []byte("{\"error\":null,\"outcome\":\"" + string(terminalPhase) + "\",\"warnings\":[\"<>&\u2028\u2029😀\",\"literal \\\\u2028 and \\\\u2029\"]}")
			if terminalPhase == RolledBack {
				resultInput = json.RawMessage(`{"warnings":["e\u0301"],"outcome":"rolled_back","error":{"message":"line\n<>&😀","code":"FIXTURE_ONLY"}}`)
				expected = []byte("{\"error\":{\"code\":\"FIXTURE_ONLY\",\"message\":\"line\\n<>&😀\"},\"outcome\":\"rolled_back\",\"warnings\":[\"e\u0301\"]}")
			}
			for _, phase := range phases {
				result := json.RawMessage(`{"outcome":"pending","reported_phase":"` + string(phase) + `"}`)
				if phase == terminalPhase {
					result = resultInput
				}
				var err error
				op, err = j.Advance(r.OperationID(), op.SHA256, phase, json.RawMessage(`{ "z": 4.50, "a": 0e1 }`), result)
				if err != nil {
					t.Fatal("terminal publication", phase, err)
				}
			}
			if !bytes.Equal(op.Record.Result, expected) {
				t.Fatalf("persisted canonical result differs: %q", op.Record.Result)
			}
			raw, sha, err := s.ReadOperationFile(r.OperationID())
			if err != nil || sha != op.SHA256 {
				t.Fatal(err)
			}
			decoded, err := decodeOperation(r.OperationID(), raw)
			if err != nil || !reflect.DeepEqual(decoded, op.Record) {
				t.Fatal("returned projection differs from exact published serialization", err)
			}
			expectedRequest := []byte("{\"api_version\":\"v1\",\"operation\":\"config.apply\",\"params\":{\"a\":[2,1],\"b\":0},\"request_id\":\"原件\\u0000/😀-" + string(terminalPhase) + "\"}")
			if !bytes.Equal(decoded.Request, expectedRequest) {
				t.Fatal("complete request body not canonical or opaque ID changed")
			}

			for i, step := range decoded.Steps {
				if i > 0 && string(step.Evidence) != `{"a":0,"z":4.5}` {
					t.Fatal("step body not normalized")
				}
			}
			oldIdentity, oldBodySHA := op.Record.Identity, op.Record.BodySHA256
			compact, err := j.Compact(r.OperationID(), op.SHA256)
			if err != nil || !compact.Record.Compacted || compact.Record.Request != nil || compact.Record.Steps != nil || compact.Record.BodySHA256 != oldBodySHA || !reflect.DeepEqual(compact.Record.Identity, oldIdentity) || !bytes.Equal(compact.Record.Result, expected) || compact.SHA256 == op.SHA256 {
				t.Fatal("compact lost original identity/body digest/result", err)
			}
			s.Close()
			reopened, err := config.OpenStore(p, true)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			j, err = NewJournal(reopened)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, p.State)
			got, made, err := j.Admit(r, Intent{}, json.RawMessage(`null`))
			if err != nil || made || !reflect.DeepEqual(got, compact) {
				t.Fatal("compact replay used new admission fields or changed result", err)
			}
			changed := request(t, r.ID(), "v1", "config.apply", `{"a":[1,2],"b":0}`)
			if _, err = j.Lookup(changed); !errors.Is(err, ErrRequestIDReused) {
				t.Fatal("compact forgot complete old request digest", err)
			}
			if !reflect.DeepEqual(before, snapshot(t, p.State)) {
				t.Fatal("compact replay/conflict wrote state")
			}
		})
	}
}

func TestOperationBoundsReadOnlyAndReadFailures(t *testing.T) {
	t.Run("readonly-cannot-admit-or-advance", func(t *testing.T) {
		j, s, p := writer(t)
		r := request(t, "known", "v1", "config.apply", `{}`)
		op := admit(t, j, r)
		s.Close()
		reader, err := config.OpenStore(p, false)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		j, err = NewJournal(reader)
		if err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, p.State)
		if got, err := j.Lookup(r); err != nil || !reflect.DeepEqual(got, op) {
			t.Fatal(err)
		}
		if _, made, err := j.Admit(request(t, "new-readonly", "v1", "config.apply", `{}`), intent(), json.RawMessage(`{}`)); made || !errors.Is(err, config.ErrReadOnly) {
			t.Fatal("readonly created identity", err)
		}
		if _, err = j.Advance(r.OperationID(), op.SHA256, Validated, json.RawMessage(`{}`), json.RawMessage(`{}`)); !errors.Is(err, config.ErrReadOnly) {
			t.Fatal("readonly advanced identity", err)
		}
		if !reflect.DeepEqual(before, snapshot(t, p.State)) {
			t.Fatal("readonly changed storage")
		}
	})
	for _, name := range []string{"missing-directory", "directory-permissions", "file-permissions", "file-symlink", "corrupt-record"} {
		t.Run(name, func(t *testing.T) {
			j, _, p := writer(t)
			r := request(t, "unavailable", "v1", "config.apply", `{}`)
			admit(t, j, r)
			dir := filepath.Join(p.State, "operations")
			file := filepath.Join(dir, string(r.OperationID())+".json")
			switch name {
			case "missing-directory":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
			case "directory-permissions":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "file-permissions":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "file-symlink":
				target := filepath.Join(t.TempDir(), "original")
				raw, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(target, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(target, file); err != nil {
					t.Fatal(err)
				}
			case "corrupt-record":
				if err := os.WriteFile(file, []byte(`{"identity":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, p.State)
			if _, err := j.Lookup(r); err == nil || errors.Is(err, ErrOperationNotFound) {
				t.Fatal("unavailable/corrupt authoritative history became unknown ID", err)
			}
			if _, made, err := j.Admit(r, intent(), json.RawMessage(`{}`)); made || err == nil || errors.Is(err, ErrOperationNotFound) {
				t.Fatal("unavailable record recreated", err)
			}
			if !reflect.DeepEqual(before, snapshot(t, p.State)) {
				t.Fatal("read failure repaired storage")
			}
		})
	}
}
