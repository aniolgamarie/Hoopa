package coreadmission

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hoopa/protocol/identityprofile"
	"strconv"
	"unicode/utf8"
)

var le = binary.LittleEndian

type Endpoint struct {
	IP   string `json:"ip"`
	Port uint16 `json:"port"`
}

// TextValid distinguishes invalid UTF-8 from an empty comm without mutable pointers.
type Comm struct {
	RawHex    string `json:"raw_hex"`
	Text      string `json:"text"`
	TextValid bool   `json:"text_valid"`
	Status    string `json:"status"`
}
type RawIdentity struct {
	Binding string `json:"binding"`
	Connect string `json:"connect"`
	Gate    string `json:"gate"`
}

// Identity contains values only: subsequent rejection cannot mutate a previous grant.
type Identity struct {
	FieldProfile    identityprofile.Profile `json:"-"`
	HasFieldProfile bool                    `json:"-"`
	// Experimental v4 payload is emitted explicitly by its writer, never by
	// legacy JSON marshaling. Both fields contain values only.
	Executable            Executable  `json:"-"`
	HasExecutable         bool        `json:"-"`
	Schema                string      `json:"schema"`
	IdentityCapability    string      `json:"identity_capability"`
	ProcessPath           string      `json:"process_path"`
	BootID                string      `json:"boot_id"`
	CoreGeneration        string      `json:"core_generation"`
	Generation            string      `json:"generation"`
	SourceCgroupID        string      `json:"source_cgroup_id"`
	RootCgroupID          string      `json:"root_cgroup_id"`
	AcceptedCookie        string      `json:"accepted_cookie"`
	ClientCookie          string      `json:"client_cookie"`
	SocketNetnsCookie     string      `json:"socket_netns_cookie"`
	TGID                  uint32      `json:"tgid_initial"`
	LeaderPID             uint32      `json:"leader_pid_initial"`
	CallerTID             uint32      `json:"caller_tid_initial"`
	LeaderStartBoottimeNS string      `json:"leader_start_boottime_ns"`
	ObservedBootNS        string      `json:"observed_boot_ns"`
	PIDNSInode            uint32      `json:"active_pidns_inum"`
	LeaderComm            Comm        `json:"leader_comm"`
	CallerComm            Comm        `json:"caller_comm"`
	LocalEndpoint         Endpoint    `json:"local_endpoint"`
	RemoteEndpoint        Endpoint    `json:"remote_endpoint"`
	Raw                   RawIdentity `json:"raw"`
}

type witness struct {
	cookie, netns uint64
	local, remote Endpoint
}
type gate struct {
	closed                      bool
	active, admitted, completed uint64
}

func readGate(raw []byte) (gate, error) {
	var g gate
	if len(raw) != 40 {
		return g, errors.New("gate size")
	}
	closed := le.Uint32(raw[4:])
	g = gate{closed == 1, le.Uint64(raw[8:]), le.Uint64(raw[16:]), le.Uint64(raw[24:])}
	if le.Uint32(raw) != 0 || closed > 1 || le.Uint32(raw[32:]) != 0 || le.Uint32(raw[36:]) != 0 || g.completed > g.admitted || g.active != g.admitted-g.completed {
		return gate{}, errors.New("invalid/poisoned gate")
	}
	return g, nil
}
func comm(raw []byte, status uint32) (Comm, error) {
	if len(raw) != 16 || raw[15] != 0 || status > 1 {
		return Comm{}, errors.New("invalid comm ABI")
	}
	end := 0
	for end < len(raw) && raw[end] != 0 {
		end++
	}
	c := Comm{RawHex: hex.EncodeToString(raw), TextValid: utf8.Valid(raw[:end]), Status: "unavailable"}
	if c.TextValid {
		c.Text = string(raw[:end])
	}
	if status == 1 {
		c.Status = "unverified"
	}
	return c, nil
}
func config(h handoff) []byte {
	out := make([]byte, 32)
	for i, s := range []string{h.Generation, h.SourceCgroupID, h.NetnsCookie} {
		n, _ := u64(s)
		le.PutUint64(out[i*8:], n)
	}
	le.PutUint32(out[24:], uint32(h.Port))
	return out
}
func uintKey(n uint64) []byte { out := make([]byte, 8); le.PutUint64(out, n); return out }
func validateWitness(w witness, h handoff) error {
	netns, _ := u64(h.NetnsCookie)
	if w.cookie == 0 || w.netns != netns || w.local != (Endpoint{"127.0.0.1", h.Port}) || w.remote.IP != "127.0.0.1" || w.remote.Port == 0 {
		return errors.New("original accepted socket namespace/endpoints mismatch")
	}
	return nil
}
func bindingCookie(raw []byte, w witness, h handoff) (uint64, error) {
	if len(raw) != 56 {
		return 0, errors.New("binding size")
	}
	generation, _ := u64(h.Generation)
	netns, _ := u64(h.NetnsCookie)
	client := le.Uint64(raw[24:])
	if le.Uint64(raw) != generation || le.Uint64(raw[8:]) != netns || le.Uint64(raw[16:]) != w.cookie || client == 0 || client == w.cookie ||
		le.Uint32(raw[32:]) != 0x0100007f || le.Uint32(raw[36:]) != 0x0100007f || le.Uint32(raw[40:]) != uint32(w.remote.Port) || le.Uint32(raw[44:]) != uint32(h.Port) || le.Uint32(raw[48:]) != 1 || le.Uint32(raw[52:]) != 0 {
		return 0, errors.New("binding ABI/witness mismatch")
	}
	return client, nil
}
func identity(h handoff, w witness, b, c, g []byte) (Identity, error) {
	var out Identity
	client, err := bindingCookie(b, w, h)
	if err != nil {
		return out, err
	}
	if len(c) != 112 {
		return out, errors.New("connect size")
	}
	generation, _ := u64(h.Generation)
	netns, _ := u64(h.NetnsCookie)
	source, _ := u64(h.SourceCgroupID)
	tgid, tid, pidns, leader := le.Uint32(c[52:]), le.Uint32(c[56:]), le.Uint32(c[60:]), le.Uint32(c[64:])
	if le.Uint64(c) != generation || le.Uint64(c[16:]) != client || le.Uint64(c[24:]) != netns || le.Uint64(c[32:]) != source || le.Uint64(c[40:]) == 0 || le.Uint64(c[8:]) < le.Uint64(c[40:]) || le.Uint32(c[48:]) != 1 || tgid == 0 || tgid > 0x7fffffff || tid == 0 || tid > 0x7fffffff || leader != tgid || pidns != h.PIDNSInode || le.Uint32(c[76:]) != 0 {
		return out, errors.New("connect coordinates/schema mismatch")
	}
	caller, err := comm(c[80:96], le.Uint32(c[68:]))
	if err != nil {
		return out, err
	}
	leaderComm, err := comm(c[96:], le.Uint32(c[72:]))
	if err != nil {
		return out, err
	}
	gate, err := readGate(g)
	if err != nil || gate.closed {
		return out, fmt.Errorf("grant gate: %v closed=%v", err, gate.closed)
	}
	out = Identity{Schema: "sourceidentity-core-admission-v1", IdentityCapability: "unverified", ProcessPath: "unknown", BootID: h.BootID, CoreGeneration: h.CoreGeneration, Generation: h.Generation, SourceCgroupID: h.SourceCgroupID, RootCgroupID: h.RootCgroupID,
		AcceptedCookie: strconv.FormatUint(w.cookie, 10), ClientCookie: strconv.FormatUint(client, 10), SocketNetnsCookie: h.NetnsCookie, TGID: tgid, LeaderPID: leader, CallerTID: tid, LeaderStartBoottimeNS: strconv.FormatUint(le.Uint64(c[40:]), 10), ObservedBootNS: strconv.FormatUint(le.Uint64(c[8:]), 10), PIDNSInode: pidns, LeaderComm: leaderComm, CallerComm: caller, LocalEndpoint: w.local, RemoteEndpoint: w.remote, Raw: RawIdentity{hex.EncodeToString(b), hex.EncodeToString(c), hex.EncodeToString(g)}}
	return out, nil
}
