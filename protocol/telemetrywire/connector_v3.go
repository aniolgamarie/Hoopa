package telemetrywire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"regexp"
	"unicode/utf8"
)

type ConnectorEndpoint struct {
	IP   string `json:"ip"`
	Port uint16 `json:"port"`
}

// TextValid distinguishes invalid UTF-8 from an empty comm without mutable pointers.
type ConnectorComm struct {
	RawHex    string `json:"raw_hex"`
	Text      string `json:"text"`
	TextValid bool   `json:"text_valid"`
	Status    string `json:"status"`
}
type ConnectorRawIdentity struct {
	Binding string `json:"binding"`
	Connect string `json:"connect"`
	Gate    string `json:"gate"`
}

// ConnectorEvidence retains experimental connector values; it does not authenticate identity.
type ConnectorEvidence struct {
	Schema                string               `json:"schema"`
	IdentityCapability    string               `json:"identity_capability"`
	ProcessPath           string               `json:"process_path"`
	BootID                string               `json:"boot_id"`
	CoreGeneration        string               `json:"core_generation"`
	Generation            string               `json:"generation"`
	SourceCgroupID        string               `json:"source_cgroup_id"`
	RootCgroupID          string               `json:"root_cgroup_id"`
	AcceptedCookie        string               `json:"accepted_cookie"`
	ClientCookie          string               `json:"client_cookie"`
	SocketNetnsCookie     string               `json:"socket_netns_cookie"`
	TGID                  uint32               `json:"tgid_initial"`
	LeaderPID             uint32               `json:"leader_pid_initial"`
	CallerTID             uint32               `json:"caller_tid_initial"`
	LeaderStartBoottimeNS string               `json:"leader_start_boottime_ns"`
	ObservedBootNS        string               `json:"observed_boot_ns"`
	PIDNSInode            uint32               `json:"active_pidns_inum"`
	LeaderComm            ConnectorComm        `json:"leader_comm"`
	CallerComm            ConnectorComm        `json:"caller_comm"`
	LocalEndpoint         ConnectorEndpoint    `json:"local_endpoint"`
	RemoteEndpoint        ConnectorEndpoint    `json:"remote_endpoint"`
	Raw                   ConnectorRawIdentity `json:"raw"`
}

const connectorScope = "connect_initiator_v1_initial_ns_ipv4_loopback_experimental"

var connectorBootID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func connectorHex(raw []byte, size int) ([]byte, bool) {
	s, ok := scalarText(raw)
	if !ok || len(s) != size*2 {
		return nil, false
	}
	b, err := hex.DecodeString(s)
	return b, err == nil && hex.EncodeToString(b) == s
}

func connectorCommValid(raw []byte, abi []byte, status uint32) bool {
	f, ok := object(raw)
	if !ok || !exactFields(f, "raw_hex text text_valid status") || status > 1 || len(abi) != 16 || abi[15] != 0 {
		return false
	}
	b, ok := connectorHex(f["raw_hex"], 16)
	if !ok || !bytes.Equal(b, abi) {
		return false
	}
	end := bytes.IndexByte(abi, 0)
	valid := utf8.Valid(abi[:end])
	text := ""
	if valid {
		text = string(abi[:end])
	}
	s, ok := scalarText(f["text"])
	wantStatus := "unavailable"
	if status == 1 {
		wantStatus = "unverified"
	}
	return ok && s == text && bytes.Equal(bytes.TrimSpace(f["text_valid"]), []byte(map[bool]string{true: "true", false: "false"}[valid])) && literal(f, "status", wantStatus)
}

func connectorEndpoint(raw []byte) (ConnectorEndpoint, bool) {
	f, ok := object(raw)
	if !ok || !exactFields(f, "ip port") || !literal(f, "ip", "127.0.0.1") {
		return ConnectorEndpoint{}, false
	}
	n, ok := decimal(f["port"], false, true)
	return ConnectorEndpoint{IP: "127.0.0.1", Port: uint16(n)}, ok && n <= 65535
}

func decodeConnector(raw []byte, generation string, entry, source []byte) (*ConnectorEvidence, bool) {
	f, ok := object(raw)
	if !ok || !exactFields(f, "schema identity_capability process_path boot_id core_generation generation source_cgroup_id root_cgroup_id accepted_cookie client_cookie socket_netns_cookie tgid_initial leader_pid_initial caller_tid_initial leader_start_boottime_ns observed_boot_ns active_pidns_inum leader_comm caller_comm local_endpoint remote_endpoint raw") || !literal(f, "schema", "sourceidentity-core-admission-v1") || !literal(f, "identity_capability", "unverified") || !literal(f, "process_path", "unknown") || !literal(f, "core_generation", generation) {
		return nil, false
	}
	boot, ok := scalarText(f["boot_id"])
	if !ok || !connectorBootID.MatchString(boot) {
		return nil, false
	}
	nums := map[string]uint64{}
	for _, k := range []string{"generation", "source_cgroup_id", "root_cgroup_id", "accepted_cookie", "client_cookie", "socket_netns_cookie", "leader_start_boottime_ns", "observed_boot_ns"} {
		n, ok := decimal(f[k], true, true)
		if !ok {
			return nil, false
		}
		nums[k] = n
	}
	for _, k := range []string{"tgid_initial", "leader_pid_initial", "caller_tid_initial", "active_pidns_inum"} {
		n, ok := decimal(f[k], false, true)
		if !ok || n > 0xffffffff || k != "active_pidns_inum" && n > 0x7fffffff {
			return nil, false
		}
		nums[k] = n
	}
	if nums["root_cgroup_id"] == nums["source_cgroup_id"] || nums["accepted_cookie"] == nums["client_cookie"] || nums["leader_pid_initial"] != nums["tgid_initial"] || nums["observed_boot_ns"] < nums["leader_start_boottime_ns"] {
		return nil, false
	}
	local, ok := connectorEndpoint(f["local_endpoint"])
	if !ok {
		return nil, false
	}
	remote, ok := connectorEndpoint(f["remote_endpoint"])
	if !ok {
		return nil, false
	}
	for _, p := range []struct {
		raw      []byte
		endpoint ConnectorEndpoint
	}{{entry, local}, {source, remote}} {
		s, ok := scalarText(p.raw)
		if !ok {
			return nil, false
		}
		a, err := netip.ParseAddrPort(s)
		if err != nil || a.Addr().String() != p.endpoint.IP || a.Port() != p.endpoint.Port {
			return nil, false
		}
	}
	r, ok := object(f["raw"])
	if !ok || !exactFields(r, "binding connect gate") {
		return nil, false
	}
	b, ok := connectorHex(r["binding"], 56)
	if !ok {
		return nil, false
	}
	c, ok := connectorHex(r["connect"], 112)
	if !ok {
		return nil, false
	}
	g, ok := connectorHex(r["gate"], 40)
	if !ok {
		return nil, false
	}
	le := binary.LittleEndian
	for _, p := range []struct {
		offset int
		want   uint64
	}{{0, nums["generation"]}, {8, nums["socket_netns_cookie"]}, {16, nums["accepted_cookie"]}, {24, nums["client_cookie"]}} {
		if le.Uint64(b[p.offset:]) != p.want {
			return nil, false
		}
	}
	for _, p := range []struct {
		offset int
		want   uint32
	}{{32, 0x0100007f}, {36, 0x0100007f}, {40, uint32(remote.Port)}, {44, uint32(local.Port)}, {48, 1}, {52, 0}} {
		if le.Uint32(b[p.offset:]) != p.want {
			return nil, false
		}
	}
	for _, p := range []struct {
		offset int
		want   uint64
	}{{0, nums["generation"]}, {8, nums["observed_boot_ns"]}, {16, nums["client_cookie"]}, {24, nums["socket_netns_cookie"]}, {32, nums["source_cgroup_id"]}, {40, nums["leader_start_boottime_ns"]}} {
		if le.Uint64(c[p.offset:]) != p.want {
			return nil, false
		}
	}
	for _, p := range []struct {
		offset int
		want   uint32
	}{{48, 1}, {52, uint32(nums["tgid_initial"])}, {56, uint32(nums["caller_tid_initial"])}, {60, uint32(nums["active_pidns_inum"])}, {64, uint32(nums["leader_pid_initial"])}, {76, 0}} {
		if le.Uint32(c[p.offset:]) != p.want {
			return nil, false
		}
	}
	if !connectorCommValid(f["caller_comm"], c[80:96], le.Uint32(c[68:])) || !connectorCommValid(f["leader_comm"], c[96:], le.Uint32(c[72:])) {
		return nil, false
	}
	admitted, completed := le.Uint64(g[16:]), le.Uint64(g[24:])
	if le.Uint32(g) != 0 || le.Uint32(g[4:]) != 0 || le.Uint32(g[32:]) != 0 || le.Uint32(g[36:]) != 0 || completed > admitted || le.Uint64(g[8:]) != admitted-completed {
		return nil, false
	}
	var out ConnectorEvidence
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return &out, true
}

func decodeV3(f wireFields, e *SourceEvent) bool {
	if e.Kind != "opened" {
		if !decodeV2(f, e) {
			return false
		}
		if e.Counter != nil {
			e.Counter.SchemaVersion = 3
		}
		return true
	}
	if !exactFields(f, commonFields+" process source_addr entry_addr target_kind target identity_scope flow_start_ns connector") || !literal(f, "identity_scope", connectorScope) {
		return false
	}
	p, ok := object(f["process"])
	if !ok || !exactFields(p, "status reason") || !literal(p, "status", "unknown") || !literal(p, "reason", "connector_experiment_unverified") {
		return false
	}
	var valid bool
	e.Connector, valid = decodeConnector(f["connector"], e.ConfigGeneration, f["entry_addr"], f["source_addr"])
	if !valid {
		return false
	}
	copy := make(wireFields, len(f)-1)
	for k, v := range f {
		if k != "connector" {
			copy[k] = v
		}
	}
	copy["identity_scope"] = json.RawMessage(`"same_uid_netns_ipv4_loopback_held_socket;fd_transfer_unverified"`)
	return decodeV2(copy, e)
}
