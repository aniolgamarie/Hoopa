package coreadmission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func exampleV2() handoff {
	h := example()
	h.Schema = "hoopa-core-identity-handoff-v2"
	h.Maps = map[string]int{}
	h.Programs = map[string]int{}
	h.Links = map[string]int{}
	fd := 200
	for _, group := range []struct {
		fds   map[string]int
		names []string
	}{{h.Maps, executableMapNames}, {h.Programs, executableProgramNames}, {h.Links, executableLinkNames}} {
		for _, name := range group.names {
			group.fds[name] = fd
			fd++
		}
	}
	return h
}

type executableFake struct {
	*fake
	targets map[int]string
	btfErr  error
	btfIDs  []uint32
}

func newExecutableFake(h handoff) *executableFake {
	f := &executableFake{fake: newFake(h), targets: map[int]string{}}
	for i, name := range []string{"exe_records", "exe_state", "calls"} {
		m := mapSpecs[name]
		raw := make([]byte, 256)
		for j, n := range []uint32{m.kind, uint32(7 + i), m.key, m.value, m.max, m.flags &^ (8 | 16)} {
			le.PutUint32(raw[j*4:], n)
		}
		copy(raw[24:40], name)
		fd := h.Maps[name]
		f.infos[fd] = raw
		f.values[fd] = make([]byte, m.value)
		if name == "calls" {
			f.accessModes[fd] = 2
		}
	}
	refs := map[string][]string{
		"hoopa_connect":   {"capture_gate", "capture_config", "capture_faults", "connect_records", "exe_state", "calls", "exe_records"},
		"hoopa_lifecycle": {"capture_config", "connect_records", "capture_faults", "capture_gate", "online_tuples", "online_bindings"},
		"hoopa_stop":      {"capture_gate"}, "call_enter": {"capture_config", "exe_state", "calls"}, "call_exit": {"capture_config", "exe_state", "calls"}, "capture_exe": {"capture_config", "exe_state", "calls", "exe_records"},
	}
	kinds := map[string]uint32{"hoopa_connect": 18, "hoopa_lifecycle": 13, "hoopa_stop": 3, "call_enter": 17, "call_exit": 17, "capture_exe": 29}
	for i, name := range executableProgramNames {
		raw := make([]byte, 256)
		le.PutUint32(raw, kinds[name])
		le.PutUint32(raw[4:], uint32(11+i))
		copy(raw[64:80], name)
		if name == "capture_exe" {
			le.PutUint32(raw[220:], 1)
			le.PutUint32(raw[224:], 39733)
		}
		fd := h.Programs[name]
		f.infos[fd] = raw
		f.refs[fd] = nil
		for _, n := range refs[name] {
			f.refs[fd] = append(f.refs[fd], le.Uint32(f.infos[h.Maps[n]][4:]))
		}
	}
	for i, name := range []string{"call_exit", "call_enter", "capture_exe"} {
		raw := make([]byte, 256)
		le.PutUint32(raw, 1)
		le.PutUint32(raw[4:], uint32(23+i))
		le.PutUint32(raw[8:], le.Uint32(f.infos[h.Programs[name]][4:]))
		fd := h.Links[name]
		if name == "capture_exe" {
			le.PutUint32(raw, 2)
			le.PutUint32(raw[16:], 27)
			le.PutUint32(raw[20:], 1)
			le.PutUint32(raw[24:], 39733)
		} else {
			target := "sys_enter"
			if name == "call_exit" {
				target = "sys_exit"
			}
			f.targets[fd] = target
			le.PutUint32(raw[24:], uint32(len(target)+1))
		}
		f.infos[fd] = raw
	}
	return f
}
func (f *executableFake) linkTarget(fd int) ([]byte, string, error) {
	raw, err := f.info(fd)
	return raw, f.targets[fd], err
}
func (f *executableFake) targetBTF(id uint32) error { f.btfIDs = append(f.btfIDs, id); return f.btfErr }
func (f *executableFake) publishExecutable() {
	raw, _ := executableABI([]byte("/bin/python3"))
	le.PutUint64(raw, 8)
	le.PutUint32(raw[8:], 72)
	le.PutUint32(raw[12:], 73)
	le.PutUint64(raw[24:], 20000000)
	f.values[f.h.Maps["exe_records"]] = raw
}
func TestHandoffV2ExactResourceVersions(t *testing.T) {
	h := exampleV2()
	p, err := parse(encoded(h))
	if err != nil || len(p.orderedFDs()) != 20 {
		t.Fatal(err, p)
	}
	for _, mutate := range []func(*handoff){
		func(h *handoff) { h.Schema = "hoopa-core-identity-handoff-v1" },
		func(h *handoff) { delete(h.Maps, "calls") },
		func(h *handoff) { h.Links["call_exit"] = h.Programs["call_exit"] },
		func(h *handoff) { h.Maps["extra"] = 999 },
	} {
		v := exampleV2()
		mutate(&v)
		if _, err := parse(encoded(v)); err == nil {
			t.Fatal("invalid v2 accepted")
		}
	}
	old := example()
	old.Schema = h.Schema
	if _, err := parse(encoded(old)); err == nil {
		t.Fatal("11 FDs accepted as v2")
	}
}
func TestHandoffV2AdoptionAndImmutableExecutable(t *testing.T) {
	h := exampleV2()
	f := newExecutableFake(h)
	o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.btfIDs, []uint32{1}) {
		t.Fatal("actual target BTF not checked", f.btfIDs)
	}
	for _, s := range f.log {
		if s == fmt.Sprintf("next:%d", h.Maps["calls"]) {
			t.Fatal("task storage enumerated")
		}
	}
	f.publishExecutable()
	id, err := o.Admit(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !id.HasExecutable || id.Schema != "sourceidentity-core-admission-v2" || id.Executable.PathText != "/bin/python3" || id.IdentityCapability != "unverified" || id.ProcessPath != "unknown" {
		t.Fatal(id)
	}
	old := id
	f.values[h.Maps["exe_records"]][40] = 'X'
	if _, err = o.Admit(nil); err == nil || !o.Status().Revoked || o.Status().Admitted != 1 {
		t.Fatal("bad new evidence admitted")
	}
	if old != id {
		t.Fatal("old value mutated")
	}
	legacy, _ := json.Marshal(id)
	if bytes.Contains(legacy, []byte(`"executable"`)) || bytes.Contains(legacy, []byte("HasExecutable")) {
		t.Fatal("implicit legacy JSON extension")
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	got := map[int]int{}
	for _, fd := range f.closed {
		got[fd]++
	}
	if len(got) != 20 {
		t.Fatal(got)
	}
	for _, n := range got {
		if n != 1 {
			t.Fatal("duplicate close")
		}
	}
}
func TestHandoffV2RejectsObjectAndTargetCorruption(t *testing.T) {
	tests := map[string]func(*executableFake){
		"BTF bytes":              func(f *executableFake) { f.btfErr = errors.New("wrong BTF") },
		"BTF function":           func(f *executableFake) { le.PutUint32(f.infos[f.h.Programs["capture_exe"]][224:], 39734) },
		"BTF object link":        func(f *executableFake) { le.PutUint32(f.infos[f.h.Links["capture_exe"]][20:], 2) },
		"raw target same length": func(f *executableFake) { f.targets[f.h.Links["call_enter"]] = "bad_enter" },
		"raw kind":               func(f *executableFake) { le.PutUint32(f.infos[f.h.Links["call_exit"]], 2) },
		"new map access":         func(f *executableFake) { f.accessModes[f.h.Maps["exe_records"]] = 2 },
		"task map flags":         func(f *executableFake) { le.PutUint32(f.infos[f.h.Maps["calls"]][20:], 0) },
		"refs":                   func(f *executableFake) { f.refs[f.h.Programs["hoopa_connect"]][0] = 999 },
		"state not fresh":        func(f *executableFake) { f.values[f.h.Maps["exe_state"]][0] = 1 },
	}
	for label, mutate := range tests {
		t.Run(label, func(t *testing.T) {
			h := exampleV2()
			f := newExecutableFake(h)
			mutate(f)
			if o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f); err == nil || o != nil {
				t.Fatal("bad resources adopted")
			}
			if len(f.closed) != 20 {
				t.Fatal("partial cleanup", f.closed)
			}
		})
	}
}
func TestHandoffV2AdmissionRefusesFaultAndMissingEvidence(t *testing.T) {
	tests := map[string]func(*executableFake){
		"fault":         func(f *executableFake) { f.values[f.h.Maps["exe_state"]][32] = 1 },
		"missing":       func(f *executableFake) { f.values[f.h.Maps["exe_records"]] = nil },
		"unconsumed":    func(f *executableFake) { le.PutUint32(f.values[f.h.Maps["exe_records"]][20:], 0) },
		"wrong cookie":  func(f *executableFake) { le.PutUint64(f.values[f.h.Maps["exe_records"]], 9) },
		"detached link": func(f *executableFake) { f.fail = fmt.Sprintf("info:%d", f.h.Links["call_enter"]) },
		"fault during read": func(f *executableFake) {
			f.lookupHook = func(fd int, _ []byte, _ uint64) {
				if fd == f.h.Maps["exe_records"] {
					f.values[f.h.Maps["exe_state"]][32] = 1
				}
			}
		},
	}
	for label, mutate := range tests {
		t.Run(label, func(t *testing.T) {
			h := exampleV2()
			f := newExecutableFake(h)
			o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			f.publishExecutable()
			mutate(f)
			if _, err = o.Admit(nil); err == nil || !o.Status().Revoked || o.Status().Admitted != 0 {
				t.Fatal("bad evidence admitted")
			}
		})
	}
}
