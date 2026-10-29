package coreadmission

import (
	"bytes"
	"errors"
	"fmt"
	"hoopa/protocol/identityprofile"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

type backend interface {
	environment(handoff) error
	info(int) ([]byte, error)
	programInfo(int) ([]byte, []uint32, error)
	access(int) (int, error)
	cloexec(int) error
	lookup(int, []byte, int, uint64) ([]byte, error)
	nextKey(int, int) error
	update(int, []byte, []byte) error
	testRun(int) error
	close(int) error
	socket(*net.TCPConn) (witness, error)
}

type Status struct {
	Admitted uint64 `json:"admitted"`
	Rejected uint64 `json:"rejected"`
	Revoked  bool   `json:"revoked"`
}
type Owner struct {
	scopeRawHex string
	mu          sync.Mutex
	h           handoff
	b           backend
	fds         []int
	config      []byte
	programIDs  map[string]uint32
	linkIDs     map[string]uint32
	btfObjectID uint32
	status      Status
	closed      bool
	closeErr    error
	now         func() time.Time
	sleep       func(time.Duration)
}

// Adopt transfers the exact versioned FD set only after a completely valid parse.
// Subsequent failure closes each once. It never loads or attaches a BPF program.
func Adopt(raw []byte, expectedCoreGeneration string, expectedPort uint16) (*Owner, error) {
	b, err := newBackend()
	if err != nil {
		return nil, err
	}
	return adopt(raw, expectedCoreGeneration, expectedPort, b)
}
func adopt(raw []byte, expectedCoreGeneration string, expectedPort uint16, b backend) (*Owner, error) {
	h, err := parse(raw)
	if err != nil {
		return nil, err
	}
	o := &Owner{h: h, b: b, fds: h.orderedFDs(), config: config(h), programIDs: map[string]uint32{}, linkIDs: map[string]uint32{}, now: time.Now, sleep: time.Sleep}
	fail := func(err error) (*Owner, error) { return nil, errors.Join(err, o.closeFDs()) }
	if h.CoreGeneration != expectedCoreGeneration || expectedCoreGeneration == "" || h.Port != expectedPort || expectedPort == 0 {
		return fail(errors.New("expected core generation/port mismatch"))
	}
	if err = b.environment(h); err != nil {
		return fail(err)
	}
	if err = o.verifyObjects(); err != nil {
		return fail(err)
	}
	if err = o.initialState(); err != nil {
		return fail(err)
	}
	for _, fd := range o.fds {
		if err = b.cloexec(fd); err != nil {
			return fail(fmt.Errorf("restore CLOEXEC: %w", err))
		}
	}
	return o, nil
}

type mapSpec struct{ kind, key, value, max, flags uint32 }

var mapSpecs = map[string]mapSpec{
	"capture_config": {2, 4, 32, 1, 128}, "connect_records": {1, 8, 112, 1024, 8}, "capture_faults": {2, 4, 8, 1, 8},
	"capture_gate": {2, 4, 40, 1, 8}, "online_tuples": {1, 32, 8, 1024, 8}, "online_bindings": {1, 8, 56, 1024, 8},
	"identity_scope": {2, 4, 24, 1, 128},
	"exe_records":    {1, 8, 296, 1024, 8}, "exe_state": {2, 4, 64, 1, 8}, "calls": {29, 4, 8, 0, 1},
}

func kernelName(name string) string {
	if len(name) > 15 {
		return name[:15]
	}
	return name
}

func infoName(raw []byte) string { return strings.SplitN(string(raw), "\x00", 2)[0] }
func (o *Owner) verifyObjects() error {
	ids := map[string]uint32{}
	seen := map[uint32]bool{}
	for _, name := range o.h.mapNames() {
		raw, err := o.b.info(o.h.Maps[name])
		if err != nil {
			return fmt.Errorf("map info %s: %w", name, err)
		}
		if len(raw) < 40 {
			return errors.New("short map info")
		}
		m := mapSpecs[name]
		id := le.Uint32(raw[4:])
		access, err := o.b.access(o.h.Maps[name])
		if err != nil {
			return err
		}
		wantAccess := 0
		if m.flags&8 == 0 {
			wantAccess = 2
		}
		if le.Uint32(raw) != m.kind || id == 0 || seen[id] || le.Uint32(raw[8:]) != m.key || le.Uint32(raw[12:]) != m.value || le.Uint32(raw[16:]) != m.max || le.Uint32(raw[20:]) != m.flags&^(8|16) || infoName(raw[24:40]) != kernelName(name) || access != wantAccess {
			return fmt.Errorf("map ABI/access mismatch: %s", name)
		}
		seen[id] = true
		ids[name] = id
	}
	seen = map[uint32]bool{}
	for _, name := range o.h.programNames() {
		raw, mapIDs, err := o.b.programInfo(o.h.Programs[name])
		if err != nil {
			return err
		}
		if len(raw) < 80 {
			return errors.New("short program info")
		}
		kind := o.h.programKind(name)
		id := le.Uint32(raw[4:])
		if le.Uint32(raw) != kind || id == 0 || seen[id] || infoName(raw[64:80]) != kernelName(name) {
			return fmt.Errorf("program ABI mismatch: %s", name)
		}
		names := o.h.programMaps(name)
		if len(mapIDs) != len(names) {
			return fmt.Errorf("program map count: %s", name)
		}
		want := map[uint32]bool{}
		for _, n := range names {
			want[ids[n]] = true
		}
		for _, id := range mapIDs {
			if !want[id] {
				return fmt.Errorf("program map references: %s", name)
			}
			delete(want, id)
		}
		if name == "capture_exe" {
			if len(raw) < 228 || le.Uint32(raw[220:]) == 0 || le.Uint32(raw[224:]) != executableLSMFunctionID {
				return errors.New("LSM program target mismatch")
			}
			extended, ok := o.b.(executableBackend)
			if !ok {
				return errors.New("executable backend unavailable")
			}
			o.btfObjectID = le.Uint32(raw[220:])
			if err := extended.targetBTF(o.btfObjectID); err != nil {
				return err
			}
		}
		seen[id] = true
		o.programIDs[name] = id
	}
	for _, name := range o.h.linkNames() {
		id, err := o.verifyLink(name)
		if err != nil {
			return err
		}
		for _, previous := range o.linkIDs {
			if id == previous {
				return errors.New("duplicate link ID")
			}
		}
		o.linkIDs[name] = id
	}
	return nil
}
func (o *Owner) verifyLink(name string) (uint32, error) {
	if name == "call_enter" || name == "call_exit" || name == "capture_exe" {
		return o.verifyExecutableLink(name)
	}
	raw, err := o.b.info(o.h.Links[name])
	if err != nil {
		return 0, err
	}
	if len(raw) < 28 {
		return 0, errors.New("short link info")
	}
	root, _ := u64(o.h.RootCgroupID)
	attach := uint32(10)
	if name == "hoopa_lifecycle" {
		attach = 3
	}
	id := le.Uint32(raw[4:])
	if le.Uint32(raw) != 3 || id == 0 || le.Uint32(raw[8:]) != o.programIDs[name] || le.Uint64(raw[16:]) != root || le.Uint32(raw[24:]) != attach {
		return 0, fmt.Errorf("link ABI mismatch: %s", name)
	}
	if expected, ok := o.linkIDs[name]; ok && id != expected {
		return 0, errors.New("link ID changed")
	}
	return id, nil
}
func (o *Owner) lookup(name string, key []byte, flags uint64) ([]byte, error) {
	raw, err := o.b.lookup(o.h.Maps[name], key, int(mapSpecs[name].value), flags)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", name, err)
	}
	if len(raw) != int(mapSpecs[name].value) {
		return nil, fmt.Errorf("lookup %s: wrong size", name)
	}
	return raw, nil
}
func (o *Owner) checkConfig() error {
	raw, err := o.lookup("capture_config", make([]byte, 4), 0)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, o.config) {
		return errors.New("config readback mismatch")
	}
	return o.checkIdentityScope()
}
func (o *Owner) gate(open bool) ([]byte, gate, error) {
	raw, err := o.lookup("capture_gate", make([]byte, 4), 4)
	if err != nil {
		return nil, gate{}, err
	}
	g, err := readGate(raw)
	if err != nil {
		return nil, g, err
	}
	if open && g.closed {
		return nil, g, errors.New("gate closed")
	}
	return raw, g, nil
}
func (o *Owner) faults() error {
	raw, err := o.lookup("capture_faults", make([]byte, 4), 0)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, make([]byte, 8)) {
		return errors.New("capture faults nonzero")
	}
	if o.h.executable() {
		state, err := o.lookup("exe_state", make([]byte, 4), 0)
		if err != nil {
			return err
		}
		if le.Uint64(state[32:]) != 0 {
			return errors.New("executable capture fault nonzero")
		}
		for _, name := range []string{"call_enter", "call_exit"} {
			misses, err := o.rawRecursionMisses(name)
			if err != nil {
				return err
			}
			if misses != 0 {
				return fmt.Errorf("raw tracepoint recursion misses: %s", name)
			}
		}
	}
	return nil
}
func (o *Owner) initialState() error {
	if err := o.checkConfig(); err != nil {
		return err
	}
	if err := o.b.update(o.h.Maps["capture_config"], make([]byte, 4), o.config); !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("config freeze probe must be EPERM, got %v", err)
	}
	if err := o.checkConfig(); err != nil {
		return err
	}
	if err := o.freezeIdentityScope(); err != nil {
		return err
	}
	for _, name := range o.h.initialHashes() {
		if err := o.b.nextKey(o.h.Maps[name], int(mapSpecs[name].key)); !errors.Is(err, syscall.ENOENT) {
			return fmt.Errorf("hash map not initially empty %s: %v", name, err)
		}
	}
	raw, _, err := o.gate(true)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, make([]byte, 40)) {
		return errors.New("gate not initially zero")
	}
	if o.h.executable() {
		state, err := o.lookup("exe_state", make([]byte, 4), 0)
		if err != nil {
			return err
		}
		if !bytes.Equal(state, make([]byte, 64)) {
			return errors.New("executable state not initially zero")
		}
	}
	return o.faults()
}

// Admit does no application I/O and never closes conn, even on refusal.
// Any refusal revokes this owner; callers must close a refused connection.
func (o *Owner) Admit(conn *net.TCPConn) (Identity, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	reject := func(err error) (Identity, error) {
		o.status.Rejected++
		o.status.Revoked = true
		return Identity{}, err
	}
	if o.status.Revoked || o.closed {
		return reject(errors.New("admission revoked"))
	}
	w, err := o.b.socket(conn)
	if err != nil {
		return reject(err)
	}
	if err = validateWitness(w, o.h); err != nil {
		return reject(err)
	}
	for _, name := range o.h.linkNames() {
		if _, err = o.verifyLink(name); err != nil {
			return reject(err)
		}
	}
	if err = o.checkConfig(); err != nil {
		return reject(err)
	}
	if _, _, err = o.gate(true); err != nil {
		return reject(err)
	}
	if err = o.faults(); err != nil {
		return reject(err)
	}
	binding, err := o.lookup("online_bindings", uintKey(w.cookie), 0)
	if err != nil {
		return reject(err)
	}
	client, err := bindingCookie(binding, w, o.h)
	if err != nil {
		return reject(err)
	}
	connect, err := o.lookup("connect_records", uintKey(client), 0)
	if err != nil {
		return reject(err)
	}
	var executableRaw []byte
	if o.h.executable() {
		executableRaw, err = o.lookup("exe_records", uintKey(client), 0)
		if err != nil {
			return reject(err)
		}
	}
	if err = o.faults(); err != nil {
		return reject(err)
	}
	gate, _, err := o.gate(true)
	if err != nil {
		return reject(err)
	}
	id, err := identity(o.h, w, binding, connect, gate)
	if err != nil {
		return reject(err)
	}
	if o.h.executable() {
		id.Executable, err = executableIdentity(executableRaw, id)
		if err != nil {
			return reject(err)
		}
		id.HasExecutable = true
		id.Schema = "sourceidentity-core-admission-v2"
	}
	if o.h.Schema == identityprofile.HandoffSchema {
		id.FieldProfile, err = identityprofile.New(o.scopeRawHex, o.h.Generation)
		if err != nil {
			return reject(err)
		}
		id.HasFieldProfile = true
		id.Schema = "sourceidentity-core-admission-v3"
		id.IdentityCapability = ""
		id.ProcessPath = ""
		id.Executable.Schema = "sourceidentity-executable-call-v2"
		id.Executable.Status = "available"
	}
	o.status.Admitted++
	return id, nil
}
func (o *Owner) Status() Status { o.mu.Lock(); defer o.mu.Unlock(); return o.status }
func (o *Owner) closeFDs() error {
	var errs []error
	// Remove bookkeeping before close: EINTR must not cause unsafe retries.
	for len(o.fds) > 0 {
		last := len(o.fds) - 1
		fd := o.fds[last]
		o.fds = o.fds[:last]
		if err := o.b.close(fd); err != nil {
			errs = append(errs, fmt.Errorf("close fd %d: %w", fd, err))
		}
	}
	return errors.Join(errs...)
}

// Close's polling is bounded after syscalls return; it cannot hard-timeout a syscall.
// The writer gate drains connect/sockops only. Executable raw/LSM hooks are
// detached through FD closure; this method does not promise a full snapshot.
func (o *Owner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.closeErr
	}
	o.closed = true
	o.status.Revoked = true
	var errs []error
	if err := o.b.testRun(o.h.Programs["hoopa_stop"]); err != nil {
		errs = append(errs, fmt.Errorf("stop TEST_RUN: %w", err))
	} else {
		deadline := o.now().Add(2 * time.Second)
		drained := false
		for calls := 0; calls < 1001; calls++ {
			_, g, err := o.gate(false)
			if err != nil {
				errs = append(errs, fmt.Errorf("stop gate: %w", err))
				break
			}
			if !g.closed {
				errs = append(errs, errors.New("stop did not close gate"))
				break
			}
			if g.active == 0 {
				drained = true
				break
			}
			if !o.now().Before(deadline) {
				break
			}
			if calls < 1000 {
				o.sleep(2 * time.Millisecond)
			}
		}
		if !drained {
			errs = append(errs, errors.New("stop gate did not drain within bounds"))
		}
	}
	errs = append(errs, o.closeFDs())
	o.closeErr = errors.Join(errs...)
	return o.closeErr
}
