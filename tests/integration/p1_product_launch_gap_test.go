//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

func p1ReadRunOriginal(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, config.MaxRunRecordBytes+1))
	err = errors.Join(readErr, f.Close())
	if err != nil || len(raw) > config.MaxRunRecordBytes {
		return nil, errP1Harness
	}
	return raw, nil
}

// After the original manager/collector have finished, a launched core can
// complete its independent exec. Read-only observation here has its own bounded
// wait and never extends the producer's three-second synchronous ACK window.
func (h *p1ProductHarness) captureLaunchProofs(ctx context.Context, records []p1TraceRecord) error {
	registered := map[string]bool{}
	for _, r := range records {
		if r.Event.Component == "runtime" && r.Event.Point == "registration-verified" {
			registered[r.Event.RunID] = true
		}
	}
	for _, p := range h.launchProofs {
		registered[string(p.RunID)] = true
	}
	for _, r := range records {
		if r.Event.Component != "runtime" || r.Event.Point != "launcher-started" || registered[r.Event.RunID] {
			continue
		}
		var manager *p1TracedProcess
		for _, p := range h.f.processes {
			if p.PID() == r.PeerPID {
				if manager != nil {
					return errP1Harness
				}
				manager = p
			}
		}
		if manager == nil {
			return errP1Harness
		}
		select {
		case <-manager.traceDone:
		default:
			return errP1Harness
		}
		if !p1ObservedTraceCrash(manager) {
			return errP1Harness
		}
		id := config.RunID(r.Event.RunID)
		dir := h.directory("launch-gap-proof")
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		proof, handle, err := h.observeLaunchGap(bounded, id, dir)
		cancel()
		if err != nil {
			return err
		}
		h.mu.Lock()
		if old := h.f.handles[id]; old != nil {
			if old.Identity() != handle.Identity() {
				h.mu.Unlock()
				handle.Close()
				return errP1Harness
			}
			handle.Close()
		} else {
			h.f.handles[id] = handle
		}
		h.mu.Unlock()
		proof.LauncherPeerPID, proof.LauncherSequence = r.PeerPID, r.Sequence
		h.launchProofs = append(h.launchProofs, proof)
		h.extras = append(h.extras, proof.Intent, proof.Birth, proof.Owner)
		registered[r.Event.RunID] = true
	}
	return nil
}

func (h *p1ProductHarness) observeLaunchGap(ctx context.Context, id config.RunID, dir string) (p1LaunchProof, *platform.ProcessHandle, error) {
	var fresh p1RunObservation
	for {
		var err error
		fresh, err = p1ObserveRun(ctx, h.f.store, id, dir)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return p1LaunchProof{}, nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if fresh.Observation.Gate != "closed" {
		return p1LaunchProof{}, nil, errP1Harness
	}
	intent, intentSHA, err := h.f.store.ReadRunIntent(id)
	if err != nil || intentSHA != fresh.IntentSHA256 {
		return p1LaunchProof{}, nil, errP1Harness
	}
	birth, birthSHA, err := h.f.store.ReadRunBirth(id)
	if err != nil || birthSHA != fresh.BirthSHA256 {
		return p1LaunchProof{}, nil, errP1Harness
	}
	handle, err := platform.ObserveProcess(ctx, birth.PID, platform.ProcessSpec{BinaryPath: intent.Binary.Path, BinarySHA256: intent.Binary.SHA256, Workdir: intent.Workdir, NonceSHA256: intent.NonceSHA256})
	if err != nil {
		return p1LaunchProof{}, nil, err
	}
	kept := false
	defer func() {
		if !kept {
			handle.Close()
		}
	}()
	native := handle.Identity()
	if native.PID != birth.PID || native.UID != birth.UID || native.BootID != birth.BootID || native.StartTicks != birth.StartTicks || handle.VerifyArguments(ctx, intent.Arguments) != nil || (p1TraceRun{intent: intent, identity: native}).process() != fresh.Identity {
		return p1LaunchProof{}, nil, errP1Harness
	}
	proof := p1LaunchProof{RunID: id, Owner: fresh.Original}
	for name, expected := range map[string]string{"intent.json": intentSHA, "birth.json": birthSHA} {
		raw, err := p1ReadRunOriginal(filepath.Join(h.f.cli.Paths().State, "runs", p1SHA([]byte(id)), name))
		if err != nil || p1SHA(raw) != expected {
			return p1LaunchProof{}, nil, errP1Harness
		}
		ref, err := p1WriteOriginal(filepath.Join(dir, name), raw)
		if err != nil {
			return p1LaunchProof{}, nil, err
		}
		if name == "intent.json" {
			proof.Intent = ref
		} else {
			proof.Birth = ref
		}
	}
	kept = true
	return proof, handle, nil
}

// Only an authenticated, fully finished crash at runtime/intent-published
// proves this trusted control path never called Start. That hook is synchronous
// inside prepare, before it returns and before the one-use launch ticket can be
// consumed. Missing birth alone, or a crash after cmd.Start, is never this proof.
func (h *p1ProductHarness) captureUnlaunchedIntents(records []p1TraceRecord) error {
	if err := p1ValidateTraceRecords(records); err != nil {
		return err
	}
	for _, r := range records {
		if r.Event.Component != "runtime" || r.Event.Point != "intent-published" || r.Decision != p1TraceCrashManager || r.CrashOriginal == nil {
			continue
		}
		id := config.RunID(r.Event.RunID)
		if h.f.notLaunched[id].Path != "" {
			continue
		}
		var process *p1TracedProcess
		for _, p := range h.f.processes {
			if p.PID() == r.PeerPID {
				if process != nil {
					return errP1Harness
				}
				process = p
			}
		}
		if process == nil {
			return errP1Harness
		}
		select {
		case <-process.traceDone:
		default:
			return errP1Harness
		}
		if !p1ObservedTraceCrash(process) {
			return errP1Harness
		}
		stream := process.Records()
		if len(stream) == 0 || stream[len(stream)-1].Sequence != r.Sequence {
			return errP1Harness
		}
		for _, other := range records {
			if other.Event.RunID == string(id) && other.Event.Component == "runtime" && other.Event.Point == "launcher-started" {
				return errP1Harness
			}
		}
		w, err := p1TraceUnwrap(r.Event.Data)
		if err != nil {
			return err
		}
		var detail struct {
			Intent config.RunIntent `json:"intent"`
		}
		if json.Unmarshal(w.Detail, &detail) != nil {
			return errP1Harness
		}
		intent, sha, err := h.f.store.ReadRunIntent(id)
		if err != nil || !reflect.DeepEqual(intent, detail.Intent) || string(intent.OperationID) != r.Event.OperationID {
			return errP1Harness
		}
		if _, _, err = h.f.store.ReadRunBirth(id); !errors.Is(err, os.ErrNotExist) {
			return errP1Harness
		}
		h.mu.Lock()
		handle := h.f.handles[id]
		h.mu.Unlock()
		if handle != nil {
			return errP1Harness
		}
		dir := h.directory("unlaunched-intent")
		raw, err := p1ReadRunOriginal(filepath.Join(h.f.cli.Paths().State, "runs", p1SHA([]byte(id)), "intent.json"))
		if err != nil || p1SHA(raw) != sha {
			return errP1Harness
		}
		intentRef, err := p1WriteOriginal(filepath.Join(dir, "intent.json"), raw)
		if err != nil {
			return err
		}
		streamRef, err := p1WriteOriginal(filepath.Join(dir, "complete-terminated-stream.json"), p1JSON(h.t, stream))
		if err != nil {
			return err
		}
		ref, err := p1WriteOriginal(filepath.Join(dir, "proof.json"), p1JSON(h.t, map[string]any{"kind": "verified-synchronous-cut-before-launch-call", "run_id": id, "intent_original": intentRef, "complete_terminated_stream": streamRef, "event_originals": r.Originals, "crash_original": r.CrashOriginal, "launch_called": false, "scope": "this one-use product launch intent; no host-wide absence or pidfd-exit claim"}))
		if err != nil {
			return err
		}
		if h.f.notLaunched == nil {
			h.f.notLaunched = map[config.RunID]originalRef{}
		}
		h.f.notLaunched[id] = ref
		h.extras = append(h.extras, ref)
	}
	return nil
}
