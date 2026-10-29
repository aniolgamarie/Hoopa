package coreadmission

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func example() handoff {
	h := handoff{Schema: "hoopa-core-identity-handoff-v1", CoreGeneration: "core-fixed", Generation: "123", SourceCgroupID: "22", RootCgroupID: "1", NetnsCookie: "3", PIDNSInode: 4026531836, BootID: "01234567-89ab-cdef-0123-456789abcdef", SourcePath: "/sys/fs/cgroup/hoopa-online-fixed", Port: 18993, Maps: map[string]int{}, Programs: map[string]int{}, Links: map[string]int{}}
	fd := 200
	for _, group := range []struct {
		fds   map[string]int
		names []string
	}{{h.Maps, mapNames}, {h.Programs, programNames}, {h.Links, linkNames}} {
		for _, name := range group.names {
			group.fds[name] = fd
			fd++
		}
	}
	return h
}
func encoded(h handoff) []byte { raw, _ := json.Marshal(h); return raw }

type fake struct {
	h             handoff
	infos         map[int][]byte
	refs          map[int][]uint32
	accessModes   map[int]int
	values        map[int][]byte
	w             witness
	log           []string
	closed        []int
	fail          string
	updateErr     error
	nextErr       error
	stopErr       error
	stopBusy      bool
	socketEntered chan struct{}
	socketResume  chan struct{}
	lookupHook    func(int, []byte, uint64)
}

func newFake(h handoff) *fake {
	f := &fake{h: h, infos: map[int][]byte{}, refs: map[int][]uint32{}, accessModes: map[int]int{}, values: map[int][]byte{}, w: witness{7, 3, Endpoint{"127.0.0.1", h.Port}, Endpoint{"127.0.0.1", 31001}}, updateErr: syscall.EPERM, nextErr: syscall.ENOENT}
	for i, name := range mapNames {
		m := mapSpecs[name]
		raw := make([]byte, 256)
		for j, n := range []uint32{m.kind, uint32(i + 1), m.key, m.value, m.max, m.flags &^ (8 | 16)} {
			le.PutUint32(raw[j*4:], n)
		}
		copy(raw[24:40], kernelName(name))
		fd := h.Maps[name]
		f.infos[fd] = raw
		f.values[fd] = make([]byte, m.value)
		if m.flags&8 == 0 {
			f.accessModes[fd] = 2
		}
	}
	f.values[h.Maps["capture_config"]] = config(h)
	for i, name := range programNames {
		kind := uint32(18)
		names := mapNames[:4]
		if name == "hoopa_lifecycle" {
			kind = 13
			names = mapNames
		}
		if name == "hoopa_stop" {
			kind = 3
			names = []string{"capture_gate"}
		}
		raw := make([]byte, 256)
		le.PutUint32(raw, kind)
		le.PutUint32(raw[4:], uint32(11+i))
		copy(raw[64:80], name)
		fd := h.Programs[name]
		f.infos[fd] = raw
		for _, n := range names {
			f.refs[fd] = append(f.refs[fd], le.Uint32(f.infos[h.Maps[n]][4:]))
		}
	}
	for i, name := range linkNames {
		raw := make([]byte, 256)
		le.PutUint32(raw, 3)
		le.PutUint32(raw[4:], uint32(21+i))
		le.PutUint32(raw[8:], le.Uint32(f.infos[h.Programs[name]][4:]))
		le.PutUint64(raw[16:], 1)
		attach := uint32(10)
		if name == "hoopa_lifecycle" {
			attach = 3
		}
		le.PutUint32(raw[24:], attach)
		f.infos[h.Links[name]] = raw
	}
	f.publish()
	return f
}
func (f *fake) publish() {
	b := make([]byte, 56)
	for i, n := range []uint64{123, 3, 7, 8} {
		le.PutUint64(b[i*8:], n)
	}
	for i, n := range []uint32{0x0100007f, 0x0100007f, 31001, uint32(f.h.Port), 1, 0} {
		le.PutUint32(b[32+i*4:], n)
	}
	f.values[f.h.Maps["online_bindings"]] = b
	c := make([]byte, 112)
	for i, n := range []uint64{123, 30000000, 8, 3, 22, 10000000} {
		le.PutUint64(c[i*8:], n)
	}
	for i, n := range []uint32{1, 72, 73, f.h.PIDNSInode, 72, 1, 1, 0} {
		le.PutUint32(c[48+i*4:], n)
	}
	copy(c[80:96], "worker")
	copy(c[96:], "leader")
	f.values[f.h.Maps["connect_records"]] = c
}
func (f *fake) record(s string) error {
	f.log = append(f.log, s)
	if s == f.fail {
		return errors.New("injected " + s)
	}
	return nil
}
func (f *fake) environment(h handoff) error { return f.record("environment") }
func (f *fake) info(fd int) ([]byte, error) {
	err := f.record(fmt.Sprintf("info:%d", fd))
	return append([]byte(nil), f.infos[fd]...), err
}
func (f *fake) programInfo(fd int) ([]byte, []uint32, error) {
	err := f.record(fmt.Sprintf("program:%d", fd))
	return append([]byte(nil), f.infos[fd]...), append([]uint32(nil), f.refs[fd]...), err
}
func (f *fake) access(fd int) (int, error) {
	return f.accessModes[fd], f.record(fmt.Sprintf("access:%d", fd))
}
func (f *fake) cloexec(fd int) error { return f.record(fmt.Sprintf("cloexec:%d", fd)) }
func (f *fake) lookup(fd int, key []byte, size int, flags uint64) ([]byte, error) {
	if f.lookupHook != nil {
		f.lookupHook(fd, key, flags)
	}
	err := f.record(fmt.Sprintf("lookup:%d:%d", fd, flags))
	return append([]byte(nil), f.values[fd]...), err
}
func (f *fake) nextKey(fd, size int) error {
	if err := f.record(fmt.Sprintf("next:%d", fd)); err != nil {
		return err
	}
	return f.nextErr
}
func (f *fake) update(fd int, key, value []byte) error {
	if err := f.record("freeze-update"); err != nil {
		return err
	}
	return f.updateErr
}
func (f *fake) testRun(fd int) error {
	if err := f.record("stop"); err != nil {
		return err
	}
	if f.stopErr != nil {
		return f.stopErr
	}
	raw := make([]byte, 40)
	le.PutUint32(raw[4:], 1)
	if f.stopBusy {
		le.PutUint64(raw[8:], 1)
		le.PutUint64(raw[16:], 1)
	}
	f.values[f.h.Maps["capture_gate"]] = raw
	return nil
}
func (f *fake) close(fd int) error {
	f.closed = append(f.closed, fd)
	return f.record(fmt.Sprintf("close:%d", fd))
}
func (f *fake) socket(conn *net.TCPConn) (witness, error) {
	if err := f.record("socket"); err != nil {
		return witness{}, err
	}
	if f.socketEntered != nil {
		close(f.socketEntered)
		<-f.socketResume
	}
	return f.w, nil
}
func adoptFake(t *testing.T) (*Owner, *fake) {
	t.Helper()
	h := example()
	f := newFake(h)
	o, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if err != nil {
		t.Fatal(err)
	}
	f.log = nil
	return o, f
}

func TestStrictHandoff(t *testing.T) {
	valid := encoded(example())
	if _, err := parse(valid); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{"duplicate": []byte(strings.Replace(string(valid), `"schema":`, `"schema":"hoopa-core-identity-handoff-v1","schema":`, 1)), "unknown": []byte(strings.Replace(string(valid), `"schema":`, `"extra":1,"schema":`, 1)), "casealias": []byte(strings.Replace(string(valid), `"schema":`, `"Schema":`, 1)), "trailing": append(append([]byte{}, valid...), []byte(" {}")...), "oversize": make([]byte, 8193)}
	for _, s := range []string{"0", "01", "+1", "-1", "1.0", "18446744073709551616"} {
		h := example()
		h.Generation = s
		tests["generation:"+s] = encoded(h)
	}
	for _, path := range []string{"/sys/fs/cgroup/hoopa-online-x/child", "/sys/fs/cgroup/hoopa-online-x/", "/sys/fs/cgroup/hoopa-online-../x", "relative", "/sys/fs/cgroup/hoopa-online-"} {
		h := example()
		h.SourcePath = path
		tests[path] = encoded(h)
	}
	mutations := map[string]func(*handoff){"fdlow": func(h *handoff) { h.Maps["capture_config"] = 199 }, "fdhigh": func(h *handoff) { h.Maps["capture_config"] = 1024 }, "aliasfd": func(h *handoff) { h.Links["hoopa_connect"] = h.Maps["capture_config"] }, "unknownmap": func(h *handoff) { delete(h.Maps, "capture_config"); h.Maps["other"] = 200 }, "samecgroup": func(h *handoff) { h.SourceCgroupID = h.RootCgroupID }, "portzero": func(h *handoff) { h.Port = 0 }, "pidnszero": func(h *handoff) { h.PIDNSInode = 0 }, "bootuppercase": func(h *handoff) { h.BootID = strings.ToUpper(h.BootID) }}
	for name, mutate := range mutations {
		h := example()
		mutate(&h)
		tests[name] = encoded(h)
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFake(example())
			if _, err := adopt(raw, "core-fixed", 18993, f); err == nil {
				t.Fatal("accepted malformed declaration")
			}
			if len(f.log) != 0 || len(f.closed) != 0 {
				t.Fatal("touched FDs before valid parse")
			}
		})
	}
	for _, s := range []string{"1", "18446744073709551615"} {
		if _, err := u64(s); err != nil {
			t.Fatal(err)
		}
	}
}
func TestAdoptionFailuresCloseAllExactlyOnce(t *testing.T) {
	tests := map[string]func(*fake){
		"environment": func(f *fake) { f.fail = "environment" },
		"maptype":     func(f *fake) { le.PutUint32(f.infos[200], 7) }, "mapflags": func(f *fake) { le.PutUint32(f.infos[201][20:], 8) }, "writable": func(f *fake) { f.accessModes[201] = 2 },
		"mapname": func(f *fake) { f.infos[200][24] = 'x' }, "duplicateMapID": func(f *fake) { le.PutUint32(f.infos[201][4:], 1) }, "shortmap": func(f *fake) { f.infos[200] = make([]byte, 39) },
		"programtype": func(f *fake) { le.PutUint32(f.infos[206], 13) }, "programname": func(f *fake) { f.infos[206][64] = 'x' }, "wrongrefs": func(f *fake) { f.refs[206][0] = 6 }, "duplicaterefs": func(f *fake) { f.refs[206][0] = 2 }, "fewrefs": func(f *fake) { f.refs[207] = f.refs[207][:5] },
		"linkroot": func(f *fake) { le.PutUint64(f.infos[209][16:], 22) }, "linkprogram": func(f *fake) { le.PutUint32(f.infos[210][8:], 11) }, "linkattach": func(f *fake) { le.PutUint32(f.infos[210][24:], 10) },
		"config": func(f *fake) { f.values[200][0]++ }, "unfrozen": func(f *fake) { f.updateErr = nil }, "freezeWrongErrno": func(f *fake) { f.updateErr = syscall.EACCES }, "nonemptyhash": func(f *fake) { f.nextErr = nil }, "closedgate": func(f *fake) { f.values[203][4] = 1 }, "activegate": func(f *fake) { f.values[203][8] = 1; f.values[203][16] = 1 }, "fault": func(f *fake) { f.values[202][0] = 1 }, "cloexecPartial": func(f *fake) { f.fail = "cloexec:205" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := example()
			f := newFake(h)
			mutate(f)
			if _, err := adopt(encoded(h), h.CoreGeneration, h.Port, f); err == nil {
				t.Fatal("accepted mutation")
			}
			want := h.orderedFDs()
			for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
				want[i], want[j] = want[j], want[i]
			}
			if !reflect.DeepEqual(f.closed, want) {
				t.Fatalf("closed %v want %v", f.closed, want)
			}
			for _, s := range f.log {
				if s == "stop" {
					t.Fatal("ran unverified stop")
				}
			}
		})
	}
	for _, mismatch := range []string{"generation", "port"} {
		t.Run(mismatch, func(t *testing.T) {
			h := example()
			f := newFake(h)
			gen, port := h.CoreGeneration, h.Port
			if mismatch == "generation" {
				gen = "other"
			} else {
				port++
			}
			if _, err := adopt(encoded(h), gen, port, f); err == nil || len(f.closed) != 11 {
				t.Fatal("mismatch ownership cleanup")
			}
		})
	}
}
func TestAdmitExactIdentityAndValueLifetime(t *testing.T) {
	o, f := adoptFake(t)
	id, err := o.Admit(nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.ClientCookie != "8" || id.AcceptedCookie != "7" || id.TGID != 72 || id.CallerTID != 73 || id.LeaderStartBoottimeNS != "10000000" || id.ObservedBootNS != "30000000" || id.LeaderComm.Text != "leader" || id.LeaderComm.Status != "unverified" || id.ProcessPath != "unknown" || id.IdentityCapability != "unverified" {
		t.Fatalf("wrong identity %#v", id)
	}
	want := []string{"socket", "info:209", "info:210", "lookup:200:0", "lookup:203:4", "lookup:202:0", "lookup:205:0", "lookup:201:0", "lookup:202:0", "lookup:203:4"}
	if !reflect.DeepEqual(f.log, want) {
		t.Fatalf("admission order %v", f.log)
	}
	f.values[202][0] = 1
	if _, err = o.Admit(nil); err == nil {
		t.Fatal("fault accepted")
	}
	if id.ClientCookie != "8" || id.LeaderComm.Text != "leader" {
		t.Fatal("previous grant mutated")
	}
	if o.Status() != (Status{1, 1, true}) {
		t.Fatal(o.Status())
	}
	before := len(f.log)
	if _, err = o.Admit(nil); err == nil || len(f.log) != before {
		t.Fatal("reads after revoked")
	}
	raw, err := json.Marshal(id)
	if err != nil || !strings.Contains(string(raw), `"active_pidns_inum":4026531836`) {
		t.Fatal(string(raw), err)
	}
	if err = o.Close(); err != nil {
		t.Fatal(err)
	}
	count := len(f.log)
	if err = o.Close(); err != nil || len(f.log) != count {
		t.Fatal("non-idempotent close")
	}
}
func TestAdmitMutationsRevokeAndStopReads(t *testing.T) {
	tests := map[string]func(*fake){
		"socket": func(f *fake) { f.fail = "socket" }, "cookiezero": func(f *fake) { f.w.cookie = 0 }, "netns": func(f *fake) { f.w.netns++ }, "localport": func(f *fake) { f.w.local.Port++ }, "peerip": func(f *fake) { f.w.remote.IP = "127.0.0.2" },
		"lostlink": func(f *fake) { f.fail = "info:209" }, "linkidchanged": func(f *fake) { le.PutUint32(f.infos[209][4:], 99) }, "config": func(f *fake) { f.values[200][0]++ }, "closed": func(f *fake) { f.values[203][4] = 1 }, "poison": func(f *fake) { f.values[203][32] = 1 }, "badgatecounts": func(f *fake) { f.values[203][8] = 1 }, "fault": func(f *fake) { f.values[202][0] = 1 }, "missingbinding": func(f *fake) { f.fail = "lookup:205:0" }, "bindingcookie": func(f *fake) { f.values[205][16]++ }, "bindingschema": func(f *fake) { f.values[205][48]++ }, "bindingport": func(f *fake) { f.values[205][40]++ }, "connectcookie": func(f *fake) { f.values[201][16]++ }, "connectscope": func(f *fake) { f.values[201][32]++ }, "connectns": func(f *fake) { f.values[201][24]++ }, "pidns": func(f *fake) { f.values[201][60]++ }, "leaderpid": func(f *fake) { f.values[201][64]++ }, "startzero": func(f *fake) {
			for i := 40; i < 48; i++ {
				f.values[201][i] = 0
			}
		}, "commstatus": func(f *fake) { f.values[201][68] = 2 }, "commterminator": func(f *fake) { f.values[201][95] = 1 }, "reserved": func(f *fake) { f.values[201][76] = 1 }, "shortconnect": func(f *fake) { f.values[201] = f.values[201][:111] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			o, f := adoptFake(t)
			mutate(f)
			if _, err := o.Admit(nil); err == nil {
				t.Fatal("accepted mutation")
			}
			before := len(f.log)
			if _, err := o.Admit(nil); err == nil || len(f.log) != before {
				t.Fatal("read after refusal")
			}
			if o.Status() != (Status{0, 2, true}) {
				t.Fatal(o.Status())
			}
		})
	}
}
func TestLateFaultAndGateSamples(t *testing.T) {
	for _, target := range []string{"fault", "gate"} {
		t.Run(target, func(t *testing.T) {
			o, f := adoptFake(t)
			f.lookupHook = func(fd int, key []byte, flags uint64) {
				if fd == 201 {
					if target == "fault" {
						f.values[202][0] = 1
					} else {
						f.values[203][4] = 1
					}
				}
			}
			if _, err := o.Admit(nil); err == nil {
				t.Fatal("late change accepted")
			}
		})
	}
}
func TestLookupKeysAndNoEnumeration(t *testing.T) {
	o, f := adoptFake(t)
	f.lookupHook = func(fd int, key []byte, flags uint64) {
		switch fd {
		case 205:
			if len(key) != 8 || le.Uint64(key) != 7 {
				t.Fatal("not original accepted cookie")
			}
		case 201:
			if len(key) != 8 || le.Uint64(key) != 8 {
				t.Fatal("not exact binding client cookie")
			}
		default:
			if len(key) != 4 || le.Uint32(key) != 0 {
				t.Fatal("bad array key")
			}
		}
		if (fd == 203) != (flags == 4) {
			t.Fatal("LOCK flags")
		}
	}
	if _, err := o.Admit(nil); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.log {
		if strings.HasPrefix(call, "next:") {
			t.Fatal("enumerated during grant")
		}
	}
}
func TestCloseErrorsStillReleaseAndBoundedPolling(t *testing.T) {
	for _, mode := range []string{"testerror", "closeerror", "busy-count", "busy-time"} {
		t.Run(mode, func(t *testing.T) {
			o, f := adoptFake(t)
			switch mode {
			case "testerror":
				f.stopErr = syscall.EINVAL
			case "closeerror":
				f.fail = "close:210"
			case "busy-count":
				f.stopBusy = true
				o.now = func() time.Time { return time.Unix(0, 0) }
				o.sleep = func(time.Duration) {}
			case "busy-time":
				f.stopBusy = true
				n := 0
				o.now = func() time.Time { n++; return time.Unix(int64(n), 0) }
				o.sleep = func(time.Duration) {}
			}
			if err := o.Close(); err == nil {
				t.Fatal("missing close failure")
			}
			if len(f.closed) != 11 {
				t.Fatal("leaked FDs")
			}
			calls := 0
			for _, call := range f.log {
				if call == "lookup:203:4" {
					calls++
				}
			}
			if mode == "busy-count" && calls != 1001 {
				t.Fatal(calls)
			}
			if mode == "busy-time" && calls > 3 {
				t.Fatal(calls)
			}
			before := len(f.log)
			if err := o.Close(); err == nil || len(f.log) != before {
				t.Fatal("close retried")
			}
			if _, err := o.Admit(nil); err == nil || len(f.log) != before {
				t.Fatal("read after closed")
			}
		})
	}
}
func TestConcurrentAdmitCloseSerialize(t *testing.T) {
	o, f := adoptFake(t)
	f.socketEntered = make(chan struct{})
	f.socketResume = make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	admitResult := make(chan error, 1)
	closeResult := make(chan error, 1)
	go func() { defer wg.Done(); _, err := o.Admit(nil); admitResult <- err }()
	<-f.socketEntered
	go func() { defer wg.Done(); closeResult <- o.Close() }()
	close(f.socketResume)
	wg.Wait()
	if err := <-admitResult; err != nil {
		t.Fatal(err)
	}
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
	if o.Status() != (Status{1, 0, true}) {
		t.Fatal(o.Status())
	}
	stop := -1
	lastlookup := -1
	for i, s := range f.log {
		if s == "stop" {
			stop = i
		}
		if strings.HasPrefix(s, "lookup:") {
			lastlookup = i
		}
	}
	if stop < 0 || lastlookup <= stop {
		t.Fatal("missing close gate lookup")
	}
	if len(f.closed) != 11 {
		t.Fatal("cleanup")
	}
}
func TestCommUnavailableInvalidUTF8AndGateUnits(t *testing.T) {
	o, f := adoptFake(t)
	f.values[201][68] = 0
	f.values[201][80] = 0xff
	id, err := o.Admit(nil)
	if err != nil {
		t.Fatal(err)
	}
	if id.CallerComm.Status != "unavailable" || id.CallerComm.TextValid || id.CallerComm.Text != "" {
		t.Fatal(id.CallerComm)
	}
	raw := make([]byte, 40)
	le.PutUint64(raw[8:], 2)
	le.PutUint64(raw[16:], 9)
	le.PutUint64(raw[24:], 7)
	g, err := readGate(raw)
	if err != nil || g.active != 2 {
		t.Fatal(g, err)
	}
}

func TestNoReadsBeyondFirstRejection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fake)
		last   string
	}{
		{"bad-witness", func(f *fake) { f.w.netns = 4 }, "socket"},
		{"missing-link", func(f *fake) { f.fail = "info:209" }, "info:209"},
		{"config", func(f *fake) { f.values[200][0]++ }, "lookup:200:0"},
		{"gate", func(f *fake) { f.values[203][4] = 1 }, "lookup:203:4"},
		{"fault", func(f *fake) { f.values[202][0] = 1 }, "lookup:202:0"},
		{"binding", func(f *fake) { f.values[205][16]++ }, "lookup:205:0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, f := adoptFake(t)
			tt.mutate(f)
			if _, err := o.Admit(nil); err == nil {
				t.Fatal("accepted reject")
			}
			if f.log[len(f.log)-1] != tt.last {
				t.Fatalf("continued reads %v", f.log)
			}
		})
	}
}
func TestStrictNestedFDJSONAndRequiredTypes(t *testing.T) {
	raw := string(encoded(example()))
	for _, bad := range []string{
		strings.Replace(raw, `"capture_config":200`, `"capture_config":200,"capture_config":201`, 1),
		strings.Replace(raw, `"capture_config":200`, `"capture_config":2e2`, 1),
		strings.Replace(raw, `"capture_config":200`, `"capture_config":200.0`, 1),
		strings.Replace(raw, `"capture_config":200`, `"capture_config":true`, 1),
		strings.Replace(raw, `"generation":"123"`, `"generation":123`, 1),
		strings.Replace(raw, `"pidns_inode":4026531836`, `"pidns_inode":4294967296`, 1),
		strings.Replace(raw, `"port":18993`, `"port":65536`, 1),
	} {
		if _, err := parse([]byte(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
func TestAdoptionAndCloseRetainAllCleanupErrors(t *testing.T) {
	h := example()
	f := newFake(h)
	f.values[200][0]++
	f.fail = "close:210"
	_, err := adopt(encoded(h), h.CoreGeneration, h.Port, f)
	if err == nil || !strings.Contains(err.Error(), "config") || !strings.Contains(err.Error(), "close fd 210") || len(f.closed) != 11 {
		t.Fatal(err, f.closed)
	}
	o, f := adoptFake(t)
	f.stopErr = syscall.EINVAL
	f.fail = "close:210"
	err = o.Close()
	if err == nil || !strings.Contains(err.Error(), "stop TEST_RUN") || !strings.Contains(err.Error(), "close fd 210") || len(f.closed) != 11 {
		t.Fatal(err, f.closed)
	}
}
func TestStatusSnapshotAndCloseFirst(t *testing.T) {
	o, f := adoptFake(t)
	snapshot := o.Status()
	snapshot.Revoked = true
	if o.Status().Revoked {
		t.Fatal("mutable status alias")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	n := len(f.log)
	if _, err := o.Admit(nil); err == nil || len(f.log) != n {
		t.Fatal("admit after close read")
	}
	if o.Status() != (Status{0, 1, true}) {
		t.Fatal(o.Status())
	}
}

func TestPIDRangeAndBootClockOrder(t *testing.T) {
	tests := map[string]func([]byte){
		"tgid-high":             func(c []byte) { le.PutUint32(c[52:], 0x80000000); le.PutUint32(c[64:], 0x80000000) },
		"tid-high":              func(c []byte) { le.PutUint32(c[56:], 0x80000000) },
		"observed-zero":         func(c []byte) { le.PutUint64(c[8:], 0) },
		"observed-before-start": func(c []byte) { le.PutUint64(c[8:], 9999999) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			o, f := adoptFake(t)
			mutate(f.values[201])
			if _, err := o.Admit(nil); err == nil {
				t.Fatal("accepted invalid PID/boot-time coordinates")
			}
		})
	}
	o, f := adoptFake(t)
	le.PutUint64(f.values[201][8:], 10000000)
	if _, err := o.Admit(nil); err != nil {
		t.Fatal("equal boot clock timestamps must be allowed", err)
	}
}
