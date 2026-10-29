//go:build p1integration

package p1testhook

import (
	"errors"
	"sync"
)

const Enabled = true

var state struct {
	sync.Mutex
	sink func(Event) bool
}

// Install is available only in a test build, once per process, before starting
// the service. true requests the one explicit injected failure at this event.
// The callback owns handling transport loss by terminating its test process.
func Install(sink func(Event) bool) error {
	state.Lock()
	defer state.Unlock()
	if sink == nil || state.sink != nil {
		return errors.New("P1_TEST_SINK_UNAVAILABLE")
	}
	state.sink = sink
	return nil
}
func emit(event Event) bool {
	state.Lock()
	sink := state.sink
	state.Unlock()
	if sink == nil {
		return false
	}
	event.Data = append([]byte(nil), event.Data...)
	return sink(event)
}
