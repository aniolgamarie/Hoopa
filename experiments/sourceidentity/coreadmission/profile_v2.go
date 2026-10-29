package coreadmission

import (
	"errors"
	"hoopa/protocol/identityprofile"
)

// These constants describe the pinned 6.18.54.1 guest, not portable kernel ABI.
const executableLSMFunctionID = 39733
const executableBTFSize = 6018902
const executableBTFSHA256 = "1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9"

var executableMapNames = []string{"capture_config", "connect_records", "capture_faults", "capture_gate", "online_tuples", "online_bindings", "exe_records", "exe_state", "calls"}
var executableProgramNames = []string{"hoopa_connect", "hoopa_lifecycle", "hoopa_stop", "call_enter", "call_exit", "capture_exe"}
var executableLinkNames = []string{"call_exit", "call_enter", "capture_exe", "hoopa_connect", "hoopa_lifecycle"}

func (h handoff) executable() bool { return h.Schema == "hoopa-core-identity-handoff-v2" || h.scoped() }
func (h handoff) mapNames() []string {
	if h.scoped() {
		return scopedMapNames
	}
	if h.executable() {
		return executableMapNames
	}
	return mapNames
}
func (h handoff) programNames() []string {
	if h.executable() {
		return executableProgramNames
	}
	return programNames
}
func (h handoff) linkNames() []string {
	if h.executable() {
		return executableLinkNames
	}
	return linkNames
}
func (h handoff) initialHashes() []string {
	names := []string{"connect_records", "online_tuples", "online_bindings"}
	if h.executable() {
		names = append(names, "exe_records")
	}
	return names
}
func (h handoff) programKind(name string) uint32 {
	switch name {
	case "hoopa_connect":
		return 18
	case "hoopa_lifecycle":
		return 13
	case "hoopa_stop":
		return 3
	case "call_enter", "call_exit":
		return 17
	case "capture_exe":
		return 29
	}
	return 0
}
func (h handoff) programMaps(name string) []string {
	switch name {
	case "hoopa_lifecycle":
		return mapNames
	case "hoopa_stop":
		return []string{"capture_gate"}
	case "call_enter", "call_exit":
		return []string{"capture_config", "exe_state", "calls"}
	case "capture_exe":
		names := []string{"capture_config", "exe_state", "calls", "exe_records"}
		if h.scoped() {
			names = append(names, "identity_scope", "capture_faults")
		}
		return names
	case "hoopa_connect":
		names := []string{"capture_config", "connect_records", "capture_faults", "capture_gate"}
		if h.executable() {
			names = append(names, "exe_state", "calls", "exe_records")
		}
		if h.scoped() {
			names = append(names, "identity_scope")
		}
		return names
	}
	return nil
}

// Extra reads are required only by v2. A v1-only backend cannot adopt v2.
type executableBackend interface {
	linkTarget(int) ([]byte, string, error)
	targetBTF(uint32) error
}

// EventSchemaVersion lets the protected entry bind the adopted resources to
// its writer profile before opening a listener. The handoff is immutable.
func (o *Owner) EventSchemaVersion() int {
	if o.h.Schema == identityprofile.HandoffSchema {
		return 5
	}
	if o.h.executable() {
		return 4
	}
	return 3
}

func (o *Owner) verifyExecutableLink(name string) (uint32, error) {
	b, ok := o.b.(executableBackend)
	if !ok {
		return 0, errors.New("executable backend unavailable")
	}
	raw, target, err := b.linkTarget(o.h.Links[name])
	if err != nil {
		return 0, err
	}
	if len(raw) < 28 {
		return 0, errors.New("short executable link info")
	}
	id := le.Uint32(raw[4:])
	if id == 0 || le.Uint32(raw[8:]) != o.programIDs[name] {
		return 0, errors.New("executable link program mismatch")
	}
	if name == "capture_exe" {
		if le.Uint32(raw) != 2 || le.Uint32(raw[16:]) != 27 || o.btfObjectID == 0 || le.Uint32(raw[20:]) != o.btfObjectID || le.Uint32(raw[24:]) != executableLSMFunctionID || target != "" {
			return 0, errors.New("LSM link target mismatch")
		}
	} else {
		want := "sys_enter"
		if name == "call_exit" {
			want = "sys_exit"
		}
		if le.Uint32(raw) != 1 || target != want || le.Uint32(raw[24:]) != uint32(len(want)+1) {
			return 0, errors.New("raw tracepoint target mismatch")
		}
	}
	if previous, ok := o.linkIDs[name]; ok && previous != id {
		return 0, errors.New("executable link ID changed")
	}
	return id, nil
}
