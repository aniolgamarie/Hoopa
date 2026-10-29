package telemetrywire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"
)

// ConnectorExecutableEvidence preserves an experimental same-call executable
// snapshot. It confers no authenticated identity or process-rule capability.
type ConnectorExecutableEvidence struct {
	Schema               string `json:"schema"`
	Status               string `json:"status"`
	Source               string `json:"source"`
	NameSource           string `json:"name_source"`
	CallToken            string `json:"call_token"`
	ObservedBootNS       string `json:"observed_boot_ns"`
	PathRawHex           string `json:"path_raw_hex"`
	PathText             string `json:"path_text"`
	PathTextValid        bool   `json:"path_text_valid"`
	ProgramNameRawHex    string `json:"program_name_raw_hex"`
	ProgramName          string `json:"program_name"`
	ProgramNameTextValid bool   `json:"program_name_text_valid"`
	RawRecord            string `json:"raw_record"`
}

func executableText(f wireFields, textKey, validKey string, raw []byte) bool {
	valid := utf8.Valid(raw)
	text := ""
	if valid {
		text = string(raw)
	}
	actual, ok := scalarText(f[textKey])
	want := []byte("false")
	if valid {
		want = []byte("true")
	}
	return ok && actual == text && bytes.Equal(bytes.TrimSpace(f[validKey]), want)
}

func decodeExecutable(raw []byte, c *ConnectorEvidence) (*ConnectorExecutableEvidence, bool) {
	f, ok := object(raw)
	if !ok || !exactFields(f, "schema status source name_source call_token observed_boot_ns path_raw_hex path_text path_text_valid program_name_raw_hex program_name program_name_text_valid raw_record") || !literal(f, "schema", "sourceidentity-executable-call-v1") || !literal(f, "status", "unverified") || !literal(f, "source", "task_exe_file_d_path_v1") || !literal(f, "name_source", "executable_path_basename_v1") {
		return nil, false
	}
	token, ok := decimal(f["call_token"], true, true)
	if !ok {
		return nil, false
	}
	observed, ok := decimal(f["observed_boot_ns"], true, true)
	if !ok {
		return nil, false
	}
	start, _ := decimal([]byte(`"`+c.LeaderStartBoottimeNS+`"`), true, true)
	end, _ := decimal([]byte(`"`+c.ObservedBootNS+`"`), true, true)
	if observed < start || observed > end {
		return nil, false
	}
	b, ok := connectorHex(f["raw_record"], 296)
	if !ok {
		return nil, false
	}
	le := binary.LittleEndian
	cookie, _ := decimal([]byte(`"`+c.ClientCookie+`"`), true, true)
	n := le.Uint32(b[16:])
	if le.Uint64(b) != cookie || le.Uint32(b[8:]) != c.TGID || le.Uint32(b[12:]) != c.CallerTID || n < 2 || n > 256 || le.Uint32(b[20:]) != 1 || le.Uint64(b[24:]) != observed || le.Uint64(b[32:]) != token {
		return nil, false
	}
	path := b[40 : 40+int(n)-1]
	if path[0] != '/' || b[40+int(n)-1] != 0 || bytes.IndexByte(path, 0) >= 0 {
		return nil, false
	}
	pathHex, ok := scalarText(f["path_raw_hex"])
	if !ok || pathHex != hex.EncodeToString(path) {
		return nil, false
	}
	name := path[bytes.LastIndexByte(path, '/')+1:]
	nameHex, ok := scalarText(f["program_name_raw_hex"])
	if !ok || len(name) == 0 || nameHex != hex.EncodeToString(name) || !executableText(f, "path_text", "path_text_valid", path) || !executableText(f, "program_name", "program_name_text_valid", name) {
		return nil, false
	}
	var out ConnectorExecutableEvidence
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return &out, true
}

func decodeV4(f wireFields, e *SourceEvent) bool {
	if e.Kind != "opened" {
		if !decodeV3(f, e) {
			return false
		}
		if e.Counter != nil {
			e.Counter.SchemaVersion = 4
		}
		return true
	}
	if !exactFields(f, commonFields+" process source_addr entry_addr target_kind target identity_scope flow_start_ns connector") {
		return false
	}
	c, ok := object(f["connector"])
	if !ok || !exactFields(c, "schema identity_capability process_path boot_id core_generation generation source_cgroup_id root_cgroup_id accepted_cookie client_cookie socket_netns_cookie tgid_initial leader_pid_initial caller_tid_initial leader_start_boottime_ns observed_boot_ns active_pidns_inum leader_comm caller_comm local_endpoint remote_endpoint raw executable") || !literal(c, "schema", "sourceidentity-core-admission-v2") {
		return false
	}
	// Validate the unchanged connector coordinates with the v3 validator only
	// after checking v4's exact shape. Restore the actual schema in the result.
	executable := c["executable"]
	delete(c, "executable")
	c["schema"] = json.RawMessage(`"sourceidentity-core-admission-v1"`)
	connectorRaw, err := json.Marshal(c)
	if err != nil {
		return false
	}
	normalized := make(wireFields, len(f))
	for k, v := range f {
		normalized[k] = v
	}
	normalized["connector"] = connectorRaw
	if !decodeV3(normalized, e) {
		return false
	}
	e.Connector.Schema = "sourceidentity-core-admission-v2"
	e.Executable, ok = decodeExecutable(executable, e.Connector)
	return ok
}
