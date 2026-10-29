//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
)

type p1ArmedFault struct {
	request           faultRequest
	selector          func(p1TraceRecord) (bool, error)
	publishedSelector func(p1TraceRecord, string) (bool, error)
	ioSelector        func(p1TraceRecord, string) (bool, error)
	bind              bool
	killRollback      bool
	sequence          uint64
	peerPID           int
	effect            originalRef
	receipt           originalRef
	phaseOriginal     originalRef
}

type p1FaultScope struct {
	operationID                  config.ID
	requestID, digest, operation string
}

func (h *p1ProductHarness) recordFaultScope(r p1TraceRecord) error {
	if r.Event.Component != "control" || r.Event.Point != "request-begin" && r.Event.Point != "request-end" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Event.Point == "request-end" {
		if h.faultScopes[r.PeerPID].requestID != r.Event.RequestID {
			return errP1Harness
		}
		delete(h.faultScopes, r.PeerPID)
		return nil
	}
	if h.faultScopes[r.PeerPID].requestID != "" {
		return errP1Harness
	}
	w, err := p1TraceUnwrap(r.Event.Data)
	if err != nil {
		return err
	}
	var d struct {
		ID        string `json:"proposed_operation_id"`
		Digest    string `json:"request_digest"`
		Operation string `json:"operation"`
	}
	if json.Unmarshal(w.Detail, &d) != nil || !operationDigest(d.Digest) || r.Event.RequestID == "" || d.Operation == "" {
		return errP1Harness
	}
	id, err := config.ParseID(d.ID)
	if err != nil {
		return err
	}
	if h.faultScopes == nil {
		h.faultScopes = map[int]p1FaultScope{}
	}
	h.faultScopes[r.PeerPID] = p1FaultScope{id, r.Event.RequestID, d.Digest, d.Operation}
	return nil
}

// The caller first waits for the original direct child and collector. A failed
// request alone, including READ_UNAVAILABLE, never establishes a crash cut.
func p1ObservedTraceCrash(p *p1TracedProcess) bool {
	if p == nil || p.TraceError() != nil || p.cmd.ProcessState == nil {
		return false
	}
	status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		return false
	}
	records := p.Records()
	if len(records) == 0 {
		return false
	}
	r := records[len(records)-1]
	return r.Decision == p1TraceCrashManager && r.CrashOriginal != nil && r.PeerPID == p.PID() && p1ValidateTraceRecords(records) == nil
}

func (h *p1ProductHarness) InjectFault(ctx context.Context, r faultRequest) error {
	if ctx == nil {
		return errP1Harness
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.Point == "request-history-capacity-fixture" && r.Failure == "fill-valid-retained-records" && r.Artifact == "" && r.Phase == "" && r.Cut == "" && r.Target == nil && r.Limit == 0 {
		return h.seedCapacity(ctx, r)
	}
	if r.Point == "ownership-fixture" {
		return h.arrangeOwnershipFault(ctx, r)
	}
	f := &p1ArmedFault{request: r}
	if r.Point == "target-entry-bind" && r.Failure == "address-in-use-after-old-stop" && r.Artifact == "" && r.Phase == "" && r.Cut == "" && r.Target == nil && r.Limit == 0 {
		f.bind = true
	} else if r.Point == "old-state-verification" && (r.Failure == "unverifiable-owner" || r.Failure == "io-error-or-unverifiable-owner") && r.Artifact == "" && r.Phase == "" && r.Cut == "" && r.Target == nil && r.Limit == 0 {
		f.killRollback = true
	} else {
		selector, err := p1CrashSelector(r)
		if err != nil {
			f.publishedSelector, err = p1PublishedCrashSelector(r)
			if err != nil {
				f.ioSelector, err = p1ActiveIOSelector(r)
				if err != nil {
					f.ioSelector, err = p1RollbackPublicationIOSelector(r)
					if err != nil {
						return errP1HarnessUnbuilt
					}
				}
			}
		}
		f.selector = selector
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return errP1Harness
	}
	h.faults = append(h.faults, f)
	return nil
}

func (h *p1ProductHarness) decideFault(r p1TraceRecord) p1TraceDecision {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return p1TraceContinue
	}
	faults := append([]*p1ArmedFault(nil), h.faults...)
	h.mu.Unlock()
	for _, f := range faults {
		h.mu.Lock()
		used := f.sequence != 0
		h.mu.Unlock()
		if used {
			continue
		}
		matched := false
		var err error
		if f.bind {
			if r.Event.Component == "runtime" && r.Event.Point == "entry-opening" {
				intent, _, e := h.f.store.ReadRunIntent(config.RunID(r.Event.RunID))
				err = e
				matched = e == nil && intent.Generation == h.f.fixtures[targetBundle].Generation()
			}
		} else if f.killRollback {
			var ref originalRef
			matched, ref, err = h.matchRollbackProofCut(r)
			if matched && err == nil {
				h.mu.Lock()
				f.phaseOriginal = ref
				h.mu.Unlock()
			}
		} else if f.publishedSelector != nil || f.ioSelector != nil {
			var ref originalRef
			if f.ioSelector != nil {
				matched, ref, err = h.matchActiveIOCut(f, r)
			} else {
				matched, ref, err = h.matchPublishedCut(f, r)
			}
			if matched && err == nil {
				h.mu.Lock()
				f.phaseOriginal = ref
				h.mu.Unlock()
			}
		} else {
			matched, err = f.selector(r)
		}
		if err != nil {
			h.mu.Lock()
			h.observerErr = errP1Harness
			h.mu.Unlock()
			return p1TraceCrashManager
		}
		if !matched {
			continue
		}
		h.mu.Lock()
		f.sequence, f.peerPID = r.Sequence, r.PeerPID
		h.mu.Unlock()
		if f.killRollback {
			if err := h.killRollbackForFault(f, r); err != nil {
				h.mu.Lock()
				h.observerErr = err
				h.mu.Unlock()
				return p1TraceCrashManager
			}
			continue
		}
		if !f.bind {
			if f.ioSelector != nil {
				return p1TraceInjectError
			}
			return p1TraceCrashManager
		}
		// A real listener makes the normal Activate fail at the target entry.
		// No returned error or successful rollback is synthesized by the hook.
		address := h.f.networks[targetBundle].entries["entry-a"].address
		listener, e := net.Listen("tcp4", address)
		var ref originalRef
		if e == nil {
			ref, e = p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("bind-fault-%d-%d.json", r.PeerPID, r.Sequence)), p1JSON(h.t, map[string]any{"kind": "actual-test-owned-loopback-listener", "address": listener.Addr().String(), "event_originals": r.Originals, "run_id": r.Event.RunID, "operation_id": r.Event.OperationID}))
		}
		h.mu.Lock()
		if listener != nil {
			h.blockers = append(h.blockers, listener)
		}
		if e != nil {
			h.observerErr = errP1Harness
		} else {
			f.effect = ref
		}
		h.mu.Unlock()
		if e != nil {
			return p1TraceCrashManager
		}
	}
	return p1TraceContinue
}

// This callback runs in the separate supervisor process using its own readonly
// Store: it takes no writer flock and never reenters the child Coordinator.
func (h *p1ProductHarness) matchPublishedCut(f *p1ArmedFault, r p1TraceRecord) (bool, originalRef, error) {
	var empty originalRef
	want := map[string]string{"active": "store/active-published", "launch-intent": "runtime/intent-published", "registration": "runtime/registration-verified"}[f.request.Artifact]
	if r.Event.Component+"/"+r.Event.Point != want {
		return false, empty, nil
	}
	id, err := config.ParseID(r.Event.OperationID)
	if err != nil {
		return false, empty, err
	}
	journal, err := control.NewJournal(h.f.store)
	if err != nil {
		return false, empty, err
	}
	stored, err := journal.Get(id)
	if err != nil {
		return false, empty, err
	}
	matched, err := f.publishedSelector(r, string(stored.Record.Phase))
	if err != nil || !matched {
		return false, empty, err
	}
	raw, sha, err := h.f.store.ReadOperationFile(id)
	if err != nil || sha != stored.SHA256 {
		return false, empty, errP1Harness
	}
	ref, err := p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("fault-phase-%d-%d.json", r.PeerPID, r.Sequence)), raw)
	return err == nil, ref, err
}

func (h *p1ProductHarness) matchActiveIOCut(f *p1ArmedFault, r p1TraceRecord) (bool, originalRef, error) {
	var empty originalRef
	if r.Event.Component != "store" || r.Event.Point != "checkpoint" {
		return false, empty, nil
	}
	w, err := p1TraceUnwrap(r.Event.Data)
	if err != nil {
		return false, empty, err
	}
	var d struct {
		Point string `json:"checkpoint"`
	}
	if json.Unmarshal(w.Detail, &d) != nil {
		return false, empty, errP1Harness
	}
	want := map[string]string{"temporary-write": "active.file.write", "file-sync": "active.file.sync", "rename": "active.rename", "directory-sync": "active.parent-sync"}[f.request.Cut]
	if f.request.Point == "rollback-active-publication" {
		want = "active.file.write"
	}
	if d.Point != want {
		return false, empty, nil
	}
	h.mu.Lock()
	scope := h.faultScopes[r.PeerPID]
	h.mu.Unlock()
	if scope.operation != "config.apply" || scope.operationID == "" {
		return false, empty, errP1Harness
	}
	journal, err := control.NewJournal(h.f.store)
	if err != nil {
		return false, empty, err
	}
	stored, err := journal.Get(scope.operationID)
	if err != nil {
		return false, empty, err
	}
	i := stored.Record.Identity
	if i.RequestID != scope.requestID || i.RequestDigest != scope.digest || i.Operation != scope.operation {
		return false, empty, errP1Harness
	}
	matched, err := f.ioSelector(r, string(stored.Record.Phase))
	if err != nil || !matched {
		return false, empty, err
	}
	raw, sha, err := h.f.store.ReadOperationFile(scope.operationID)
	if err != nil || sha != stored.SHA256 {
		return false, empty, errP1Harness
	}
	ref, err := p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("fault-phase-%d-%d.json", r.PeerPID, r.Sequence)), raw)
	return err == nil, ref, err
}

func (h *p1ProductHarness) matchRollbackProofCut(r p1TraceRecord) (bool, originalRef, error) {
	var empty originalRef
	if r.Event.Component != "runtime" || r.Event.Point != "entry-opened" {
		return false, empty, nil
	}
	id, err := config.ParseID(r.Event.OperationID)
	if err != nil {
		return false, empty, err
	}
	journal, err := control.NewJournal(h.f.store)
	if err != nil {
		return false, empty, err
	}
	stored, err := journal.Get(id)
	if err != nil {
		return false, empty, err
	}
	if stored.Record.Phase != control.RollingBack {
		return false, empty, nil
	}
	meaning, err := operationClassify(stored)
	if err != nil || meaning.kind != "apply" || string(meaning.rollbackRunID) != r.Event.RunID {
		return false, empty, errP1Harness
	}
	raw, sha, err := h.f.store.ReadOperationFile(id)
	if err != nil || sha != stored.SHA256 {
		return false, empty, errP1Harness
	}
	ref, err := p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("fault-phase-%d-%d.json", r.PeerPID, r.Sequence)), raw)
	return err == nil, ref, err
}

func (h *p1ProductHarness) killRollbackForFault(f *p1ArmedFault, r p1TraceRecord) error {
	h.mu.Lock()
	handle := h.f.handles[config.RunID(r.Event.RunID)]
	h.mu.Unlock()
	if handle == nil {
		return errP1Harness
	}
	w, err := p1TraceUnwrap(r.Event.Data)
	if err != nil {
		return err
	}
	var detail struct {
		Identity platform.ProcessIdentity `json:"identity"`
	}
	if json.Unmarshal(w.Detail, &detail) != nil || detail.Identity != handle.Identity() {
		return errP1Harness
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := handle.SignalVerified(ctx, syscall.SIGKILL); err != nil {
		return err
	}
	if err := handle.WaitExited(ctx); err != nil {
		return err
	}
	ref, err := p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("rollback-owner-fault-%d-%d.json", r.PeerPID, r.Sequence)), p1JSON(h.t, map[string]any{"kind": "supervisor-original-rollback-pidfd-sigkill-before-open-verification", "run_id": r.Event.RunID, "identity": handle.Identity(), "original_pidfd_exited": true, "event_originals": r.Originals, "phase_original": f.phaseOriginal}))
	if err != nil {
		return err
	}
	h.mu.Lock()
	f.effect = ref
	h.mu.Unlock()
	return nil
}

func (h *p1ProductHarness) faultEvidence(records []p1TraceRecord) ([]faultHit, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var hits []faultHit
	for _, f := range h.faults {
		if f.sequence == 0 {
			continue
		}
		if f.receipt.Path == "" {
			var matched *p1TraceRecord
			for i := range records {
				if records[i].PeerPID == f.peerPID && records[i].Sequence == f.sequence {
					if matched != nil {
						return nil, errP1Harness
					}
					matched = &records[i]
				}
			}
			if matched == nil || (f.bind || f.killRollback) && (f.effect.Path == "" || matched.Decision != p1TraceContinue) || f.ioSelector != nil && (matched.Decision != p1TraceInjectError || f.phaseOriginal.Path == "") || !f.bind && !f.killRollback && f.ioSelector == nil && (matched.Decision != p1TraceCrashManager || matched.CrashOriginal == nil) {
				return nil, errP1Harness
			}
			ref, err := p1WriteOriginal(filepath.Join(h.f.artifacts, fmt.Sprintf("fault-hit-%d-%d.json", f.peerPID, f.sequence)), p1JSON(h.t, map[string]any{"request": f.request, "event_originals": matched.Originals, "effect_original": f.effect, "phase_original": f.phaseOriginal, "crash_original": matched.CrashOriginal, "peer_pid": f.peerPID, "sequence": f.sequence}))
			if err != nil {
				return nil, err
			}
			f.receipt = ref
		}
		r := f.request
		hits = append(hits, faultHit{Point: r.Point, Artifact: r.Artifact, Phase: r.Phase, Cut: r.Cut, Failure: r.Failure, Original: f.receipt})
	}
	return hits, nil
}
