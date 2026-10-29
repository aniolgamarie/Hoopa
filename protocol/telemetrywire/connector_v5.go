package telemetrywire

import (
	"encoding/json"
	"hoopa/protocol/identityprofile"
)

func decodeV5(f wireFields, e *SourceEvent) bool {
	if e.Kind != "opened" {
		if !decodeV4(f, e) {
			return false
		}
		if e.Counter != nil {
			e.Counter.SchemaVersion = 5
		}
		return true
	}
	if !exactFields(f, commonFields+" process source_addr entry_addr target_kind target identity_scope flow_start_ns connector") {
		return false
	}
	process, ok := object(f["process"])
	if !ok || !exactFields(process, "status reason") || !literal(process, "status", "unknown") || !literal(process, "reason", "legacy_process_fields_unused") {
		return false
	}
	c, ok := object(f["connector"])
	if !ok || !exactFields(c, "schema boot_id core_generation generation source_cgroup_id root_cgroup_id accepted_cookie client_cookie socket_netns_cookie tgid_initial leader_pid_initial caller_tid_initial leader_start_boottime_ns observed_boot_ns active_pidns_inum leader_comm caller_comm local_endpoint remote_endpoint raw executable field_profile") || !literal(c, "schema", "sourceidentity-core-admission-v3") {
		return false
	}
	generation, ok := scalarText(c["generation"])
	if !ok {
		return false
	}
	profile, err := identityprofile.Decode(c["field_profile"], generation)
	if err != nil {
		return false
	}
	exe, ok := object(c["executable"])
	if !ok || !literal(exe, "schema", "sourceidentity-executable-call-v2") || !literal(exe, "status", "available") {
		return false
	}
	exe["schema"] = json.RawMessage(`"sourceidentity-executable-call-v1"`)
	exe["status"] = json.RawMessage(`"unverified"`)
	b, err := json.Marshal(exe)
	if err != nil {
		return false
	}
	c["executable"] = b
	delete(c, "field_profile")
	c["schema"] = json.RawMessage(`"sourceidentity-core-admission-v2"`)
	c["identity_capability"] = json.RawMessage(`"unverified"`)
	c["process_path"] = json.RawMessage(`"unknown"`)
	b, err = json.Marshal(c)
	if err != nil {
		return false
	}
	normalized := make(wireFields, len(f))
	for k, v := range f {
		normalized[k] = v
	}
	normalized["connector"] = b
	normalized["process"] = json.RawMessage(`{"status":"unknown","reason":"connector_experiment_unverified"}`)
	if !decodeV4(normalized, e) {
		return false
	}
	e.Connector.Schema = "sourceidentity-core-admission-v3"
	e.Connector.IdentityCapability = ""
	e.Connector.ProcessPath = ""
	e.Executable.Schema = "sourceidentity-executable-call-v2"
	e.Executable.Status = "available"
	e.FieldProfile = &profile
	return true
}
