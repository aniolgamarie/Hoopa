package coreadmission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func exampleV3() handoff {
	h := exampleV2()
	h.Schema = "hoopa-core-identity-handoff-v3"
	h.UserNSInode = 4026531837
	h.MountNSInode = 4026531841
	fd := 200
	for _, group := range []struct {
		fds   map[string]int
		names []string
	}{{h.Maps, h.mapNames()}, {h.Programs, h.programNames()}, {h.Links, h.linkNames()}} {
		for _, name := range group.names {
			group.fds[name] = fd
			fd++
		}
	}
	return h
}

type scopedFake struct {
	*executableFake
	writableScope bool
}

func newScopedFake(h handoff) *scopedFake {
	f := &scopedFake{executableFake: newExecutableFake(h)}
	m := mapSpecs["identity_scope"]
	raw := make([]byte, 256)
	for i, v := range []uint32{m.kind, 10, m.key, m.value, m.max, m.flags} {
		le.PutUint32(raw[i*4:], v)
	}
	copy(raw[24:40], "identity_scope")
	fd := h.Maps["identity_scope"]
	f.infos[fd] = raw
	f.accessModes[fd] = 2
	f.values[fd] = identityScopeConfig(h)
	for _, name := range h.programNames() {
		f.refs[h.Programs[name]] = nil
		for _, m := range h.programMaps(name) {
			f.refs[h.Programs[name]] = append(f.refs[h.Programs[name]], le.Uint32(f.infos[h.Maps[m]][4:]))
		}
	}
	return f
}
func (f *scopedFake) update(fd int, key, value []byte) error {
	if fd != f.h.Maps["identity_scope"] {
		return f.fake.update(fd, key, value)
	}
	if err := f.record("scope-freeze-update"); err != nil {
		return err
	}
	if !bytes.Equal(value, identityScopeConfig(f.h)) || !bytes.Equal(key, make([]byte, 4)) {
		return errors.New("wrong same-value scope probe")
	}
	if f.writableScope {
		return nil
	}
	return syscall.EPERM
}
func TestHandoffV3ExactScopeAndLegacyIsolation(t *testing.T) {
	h := exampleV3()
	p, err := parse(encoded(h))
	if err != nil || len(p.orderedFDs()) != 21 || !p.scoped() {
		t.Fatal(p, err)
	}
	for _, mutate := range []func(*handoff){func(h *handoff) { h.UserNSInode = 0 }, func(h *handoff) { h.MountNSInode = 0 }, func(h *handoff) { delete(h.Maps, "identity_scope") }, func(h *handoff) { h.Schema = "hoopa-core-identity-handoff-v2" }, func(h *handoff) { h.Maps["identity_scope"] = h.Maps["calls"] }, func(h *handoff) { h.Maps["extra"] = 999 }} {
		v := exampleV3()
		mutate(&v)
		if _, err := parse(encoded(v)); err == nil {
			t.Fatal("invalid v3 accepted", v)
		}
	}
	for _, schema := range []string{"hoopa-core-identity-handoff-v1", "hoopa-core-identity-handoff-v2"} {
		v := example()
		if strings.HasSuffix(schema, "v2") {
			v = exampleV2()
		}
		m := map[string]any{}
		_ = json.Unmarshal(encoded(v), &m)
		m["userns_inode"] = 1
		m["mountns_inode"] = 2
		raw, _ := json.Marshal(m)
		if _, err := parse(raw); err == nil {
			t.Fatal("legacy scope accepted")
		}
	}
	for _, raw := range [][]byte{bytes.Replace(encoded(h), []byte(`"userns_inode":4026531837`), []byte(`"userns_inode":4294967296`), 1), bytes.Replace(encoded(h), []byte(`"mountns_inode":4026531841`), []byte(`"mountns_inode":true`), 1), bytes.Replace(encoded(h), []byte(`"userns_inode":`), []byte(`"\u0075serns_inode":1,"userns_inode":`), 1)} {
		if _, err := parse(raw); err == nil {
			t.Fatal("malformed namespace accepted", string(raw))
		}
	}
}
func TestHandoffV3ScopeAdoptionAndAdmitOrder(t *testing.T) {
	h := exampleV3()
	f := newScopedFake(h)
	o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if err != nil {
		t.Fatal(err)
	}
	if o.EventSchemaVersion() != 4 || len(o.fds) != 21 {
		t.Fatal("wrong profile")
	}
	if !strings.Contains(strings.Join(f.log, ","), "scope-freeze-update") {
		t.Fatal("scope freeze not tested")
	}
	for _, entry := range f.log {
		if entry == fmt.Sprintf("next:%d", h.Maps["calls"]) {
			t.Fatal("task storage enumerated")
		}
	}
	f.publishExecutable()
	f.log = nil
	id, err := o.Admit(nil)
	if err != nil || !id.HasExecutable || id.Schema != "sourceidentity-core-admission-v2" || id.IdentityCapability != "unverified" {
		t.Fatal(id, err)
	}
	index := func(s string) int {
		for i, x := range f.log {
			if x == s {
				return i
			}
		}
		return -1
	}
	config := index(fmt.Sprintf("lookup:%d:0", h.Maps["capture_config"]))
	scope := index(fmt.Sprintf("lookup:%d:0", h.Maps["identity_scope"]))
	gate := index(fmt.Sprintf("lookup:%d:4", h.Maps["capture_gate"]))
	fault := index(fmt.Sprintf("lookup:%d:0", h.Maps["capture_faults"]))
	if !(config >= 0 && scope > config && gate > scope && fault > gate) {
		t.Fatal("original config/gate/fault order changed", f.log)
	}
	f.values[h.Maps["identity_scope"]][12] ^= 1
	f.log = nil
	if _, err = o.Admit(nil); err == nil || o.Status() != (Status{Admitted: 1, Rejected: 1, Revoked: true}) {
		t.Fatal("changed scope admitted", err, o.Status())
	}
	if index(fmt.Sprintf("lookup:%d:0", h.Maps["online_bindings"])) >= 0 {
		t.Fatal("binding read after scope refusal")
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	if len(f.closed) != 21 {
		t.Fatal("not all exactly owned FDs closed", f.closed)
	}
}
func TestHandoffV3BadScopeClosesAllOnce(t *testing.T) {
	for i := 0; i < 24; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			h := exampleV3()
			f := newScopedFake(h)
			f.values[h.Maps["identity_scope"]][i] ^= 1
			if o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f); err == nil || o != nil {
				t.Fatal("corrupt config accepted")
			}
			if len(f.closed) != 21 {
				t.Fatal(f.closed)
			}
			seen := map[int]bool{}
			for _, fd := range f.closed {
				if seen[fd] {
					t.Fatal("double close")
				}
				seen[fd] = true
			}
		})
	}
	for _, cause := range []string{"writable", "short", "lookup", "abi", "refs"} {
		t.Run(cause, func(t *testing.T) {
			h := exampleV3()
			f := newScopedFake(h)
			switch cause {
			case "writable":
				f.writableScope = true
			case "short":
				f.values[h.Maps["identity_scope"]] = make([]byte, 23)
			case "lookup":
				f.fail = fmt.Sprintf("lookup:%d:0", h.Maps["identity_scope"])
			case "abi":
				f.infos[h.Maps["identity_scope"]][12] = 23
			case "refs":
				f.refs[h.Programs["capture_exe"]] = f.refs[h.Programs["capture_exe"]][1:]
			}
			if _, err := adopt(encoded(h), h.CoreGeneration, h.Port, f); err == nil {
				t.Fatal("bad scoped resource accepted")
			}
			if len(f.closed) != 21 {
				t.Fatal(f.closed)
			}
		})
	}
	if !reflect.DeepEqual(identityScopeConfig(exampleV3())[16:], []byte{1, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatal("schema/reserved layout")
	}
}

func TestHandoffV3ActualNamespaceCoordinates(t *testing.T) {
	h := exampleV3()
	for _, v := range []struct {
		name  string
		value uint32
	}{{"pid", h.PIDNSInode}, {"user", h.UserNSInode}, {"mnt", h.MountNSInode}} {
		n := uint64(v.value)
		if !h.namespaceMatches(v.name, n, n) || h.namespaceMatches(v.name, n+1, n) || h.namespaceMatches(v.name, n+1, n+1) || h.namespaceMatches(v.name, 0, 0) || h.namespaceMatches(v.name, 1<<32, 1<<32) {
			t.Fatal("invalid actual initial scope", v)
		}
	}
	legacy := exampleV2()
	if !legacy.namespaceMatches("user", 1, 1) || legacy.namespaceMatches("user", 1, 2) {
		t.Fatal("legacy namespace behavior changed")
	}
}
