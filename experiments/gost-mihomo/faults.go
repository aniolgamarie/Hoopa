package main

import "errors"

// FaultBoundary names the F11 observation points; no hook controls processes.
type FaultBoundary string

const (
	CandidatePersisted   FaultBoundary = "candidate_persisted"
	ApplyingPersisted    FaultBoundary = "applying_persisted"
	OldProcessExited     FaultBoundary = "old_process_exited"
	NewProcessRegistered FaultBoundary = "new_process_registered"
	SelectionsVerified   FaultBoundary = "selections_verified"
	InletsOpened         FaultBoundary = "inlets_opened"
	PublishingPersisted  FaultBoundary = "publishing_persisted"
	ActivePublished      FaultBoundary = "active_published"
	OperationCommitted   FaultBoundary = "operation_committed"
)

var (
	ErrFaultBoundary = errors.New("unknown experiment fault boundary")
	ErrFaultInjected = errors.New("experiment fault injected")
)

type FaultHook struct{ boundaries []FaultBoundary }

func validBoundary(boundary FaultBoundary) bool {
	switch boundary {
	case CandidatePersisted, ApplyingPersisted, OldProcessExited, NewProcessRegistered, SelectionsVerified, InletsOpened, PublishingPersisted, ActivePublished, OperationCommitted:
		return true
	}
	return false
}

func NewFaultHook(boundaries ...FaultBoundary) (*FaultHook, error) {
	for _, boundary := range boundaries {
		if !validBoundary(boundary) {
			return nil, ErrFaultBoundary
		}
	}
	return &FaultHook{boundaries: append([]FaultBoundary{}, boundaries...)}, nil
}

// Check only injects a configured error. A nil error is not an acceptance result;
// callers must actually execute the boundary and its independent assertions.
func (h *FaultHook) Check(boundary FaultBoundary) error {
	if !validBoundary(boundary) {
		return ErrFaultBoundary
	}
	for _, configured := range h.boundaries {
		if configured == boundary {
			return ErrFaultInjected
		}
	}
	return nil
}
