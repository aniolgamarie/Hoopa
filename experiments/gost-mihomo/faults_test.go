package main

import (
	"errors"
	"testing"
)

func TestFaultBoundaryValidationAndSelectiveInjection(t *testing.T) {
	boundaries := []FaultBoundary{CandidatePersisted, ApplyingPersisted, OldProcessExited, NewProcessRegistered, SelectionsVerified, InletsOpened, PublishingPersisted, ActivePublished, OperationCommitted}
	for _, injected := range boundaries {
		hook, err := NewFaultHook(injected)
		if err != nil {
			t.Fatal(err)
		}
		for _, boundary := range boundaries {
			err := hook.Check(boundary)
			if (boundary == injected) != errors.Is(err, ErrFaultInjected) {
				t.Fatalf("configured %s checked %s: %v", injected, boundary, err)
			}
		}
	}
	if _, err := NewFaultHook("kill-by-name"); !errors.Is(err, ErrFaultBoundary) {
		t.Fatalf("unknown configuration: %v", err)
	}
	hook, err := NewFaultHook()
	if err != nil {
		t.Fatal(err)
	}
	if err := hook.Check(CandidatePersisted); err != nil {
		t.Fatal(err)
	}
	if err := hook.Check("unknown"); !errors.Is(err, ErrFaultBoundary) {
		t.Fatal(err)
	}
}
