package telemetrywire

import (
	"bytes"
	"encoding/json"
	"errors"
	"hoopa/protocol/identityprofile"
	"io"
	"net/netip"
	"strings"
	"unicode/utf8"
)

const SourceInputLimit = 8192

var ErrInvalidSourceEvent = errors.New("invalid experimental source event")

// SourceEvent represents the strict experimental v1/v2/v3 profiles, not proof of identity,
// ready timing or a capability gate. Raw retains the original bytes for replay.
// Null flow/attempt are allowed only on the source-scoped epoch_closed marker.
type SourceEvent struct {
	FieldProfile                           *identityprofile.Profile
	Connector                              *ConnectorEvidence
	Executable                             *ConnectorExecutableEvidence
	SchemaVersion                          uint8
	FlowStartNS                            uint64
	ActualConnectionRef                    string
	ConnectionID                           string
	OrderedChain                           []RouteHop
	Quality                                *SetupQuality
	Segments                               []SegmentCounter
	UnallocatedUpload, UnallocatedDownload uint64
	SourceInstanceID                       string
	SourceEpoch                            string
	EventID                                string
	SourceSeq                              uint64
	FlowID, AttemptID                      string
	ConfigGeneration                       string
	Kind                                   string
	Counter                                *CounterEvent
	Raw                                    json.RawMessage
	Phase                                  string
	GapReason                              string
	GapDimensions                          []string
	UnfinishedFlows                        uint64
	EpochFinality                          string
}

type wireFields map[string]json.RawMessage

// object rejects duplicate decoded names, including escaped aliases. Values
// stay RawMessage: no integer ever passes through float64.
func object(raw []byte) (wireFields, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, false
	}
	f := wireFields{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, false
		}
		k, ok := t.(string)
		if !ok {
			return nil, false
		}
		if _, exists := f[k]; exists {
			return nil, false
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, false
		}
		f[k] = v
	}
	t, err = d.Token()
	if err != nil || t != json.Delim('}') {
		return nil, false
	}
	var tail json.RawMessage
	return f, d.Decode(&tail) == io.EOF
}

func exactFields(f wireFields, names string) bool {
	keys := strings.Fields(names)
	if len(f) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := f[k]; !ok {
			return false
		}
	}
	return true
}

func scalarText(raw []byte) (string, bool) {
	var s string
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil || len(s) > 4096 || strings.ContainsRune(s, 0) {
		return "", false
	}
	return s, true
}

func literal(f wireFields, key, want string) bool {
	s, ok := scalarText(f[key])
	return ok && s == want
}
func null(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func decimal(raw []byte, quoted bool, nonzero bool) (uint64, bool) {
	s := string(bytes.TrimSpace(raw))
	if quoted {
		var ok bool
		s, ok = scalarText(raw)
		if !ok {
			return 0, false
		}
	}
	v, err := parseBytes(s)
	return uint64(v), err == nil && (!nonzero || v != 0)
}
func id(raw []byte) (string, bool) {
	s, ok := scalarText(raw)
	v, err := parseID(s)
	return v, ok && err == nil
}
func stringsArray(raw []byte, nonempty bool) ([]string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) > 64 || nonempty && len(items) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		s, ok := scalarText(raw)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func processValid(raw []byte) bool {
	p, ok := object(raw)
	if !ok {
		return false
	}
	if literal(p, "status", "unknown") {
		r, ok := id(p["reason"])
		return exactFields(p, "status reason") && ok && r != ""
	}
	if !exactFields(p, "status pid name name_source start_ticks boot_id pid_namespace network_namespace socket_inode path") || !literal(p, "status", "identified") || !literal(p, "name_source", "linux_proc_comm") {
		return false
	}
	pid, ok := decimal(p["pid"], false, true)
	if !ok || pid > 1<<31-1 {
		return false
	}
	if _, ok := decimal(p["start_ticks"], true, false); !ok {
		return false
	}
	if _, ok := decimal(p["socket_inode"], true, true); !ok {
		return false
	}
	for _, k := range []string{"name", "path", "boot_id"} {
		if s, ok := scalarText(p[k]); !ok || s == "" {
			return false
		}
	}
	for _, pair := range []struct{ key, prefix string }{{"pid_namespace", "pid:["}, {"network_namespace", "net:["}} {
		s, ok := scalarText(p[pair.key])
		if !ok || !strings.HasPrefix(s, pair.prefix) || !strings.HasSuffix(s, "]") {
			return false
		}
		n := strings.TrimSuffix(strings.TrimPrefix(s, pair.prefix), "]")
		v, err := parseBytes(n)
		if err != nil || v == 0 {
			return false
		}
	}
	return true
}

const commonFields = "schema_version source_instance_id source_epoch event_id source_seq flow_id attempt_id observed_at config_generation boundary_ref kind"

func kindValid(f wireFields, kind string) bool {
	switch kind {
	case "opened":
		if !exactFields(f, commonFields+" process source_addr entry_addr target_kind target identity_scope") || !processValid(f["process"]) || !literal(f, "identity_scope", "same_uid_netns_ipv4_loopback_held_socket;fd_transfer_unverified") {
			return false
		}
		for _, k := range []string{"source_addr", "entry_addr"} {
			s, ok := scalarText(f[k])
			if !ok {
				return false
			}
			if _, err := netip.ParseAddrPort(s); err != nil {
				return false
			}
		}
		target, ok := scalarText(f["target"])
		if !ok {
			return false
		}
		if literal(f, "target_kind", "unknown") {
			return target == ""
		}
		if literal(f, "target_kind", "ip_only") {
			_, err := netip.ParseAddr(target)
			return err == nil
		}
		return literal(f, "target_kind", "host") && target != ""
	case "route_decided":
		if _, exists := f["phase"]; exists {
			_, ok := id(f["connection_id"])
			return exactFields(f, commonFields+" phase connection_id") && literal(f, "phase", "connection_binding") && ok
		}
		if !exactFields(f, commonFields+" rule rule_payload logical_target process") || !processValid(f["process"]) {
			return false
		}
		for _, k := range []string{"rule", "rule_payload", "logical_target"} {
			if _, ok := scalarText(f[k]); !ok {
				return false
			}
		}
		return true
	case "setup_finished":
		if !exactFields(f, commonFields+" definition duration_ms waited_ms flow_duration_ms flow_waited_ms flow_definition outcome failure_stage ordered_chain provider_chain route_evidence protocol_ready_validation") || !literal(f, "definition", "attempt_dial_handshake_v1_candidate") || !literal(f, "flow_definition", "request_known_to_ready_v1_candidate") || !literal(f, "route_evidence", "returned_conn_only") || !literal(f, "protocol_ready_validation", "unverified") {
			return false
		}
		outcome, ok := scalarText(f["outcome"])
		if !ok || outcome != "success" && outcome != "failure" && outcome != "timeout" && outcome != "unknown" {
			return false
		}
		if _, ok := scalarText(f["failure_stage"]); !ok {
			return false
		}
		for _, k := range []string{"ordered_chain", "provider_chain"} {
			items, ok := stringsArray(f[k], false)
			if !ok {
				return false
			}
			// The fixed core preserves an empty provider name for a direct or
			// otherwise unlabelled chain hop. Keep it verbatim, never infer a
			// provider; actual node names themselves must be nonempty.
			if k == "ordered_chain" {
				for _, item := range items {
					if item == "" {
						return false
					}
				}
			}
		}
		for _, pair := range []struct{ duration, waited string }{{"duration_ms", "waited_ms"}, {"flow_duration_ms", "flow_waited_ms"}} {
			wait, ok := decimal(f[pair.waited], false, false)
			if !ok || wait > 1<<63-1 {
				return false
			}
			if outcome != "success" {
				if !null(f[pair.duration]) {
					return false
				}
				continue
			}
			duration, ok := decimal(f[pair.duration], false, false)
			if !ok || duration > wait {
				return false
			}
		}
		return true
	case "gap":
		if !exactFields(f, commonFields+" reason dimensions upload_bytes download_bytes connection_id") || !null(f["upload_bytes"]) || !null(f["download_bytes"]) {
			return false
		}
		if !literal(f, "reason", "source_event_uncertain") && !literal(f, "reason", "source_shutdown_before_final") {
			return false
		}
		if s, ok := scalarText(f["connection_id"]); !ok {
			return false
		} else if s != "" {
			if _, err := parseID(s); err != nil {
				return false
			}
		}
		dims, ok := stringsArray(f["dimensions"], true)
		if !ok {
			return false
		}
		seen := map[string]bool{}
		for _, dim := range dims {
			if seen[dim] || dim != "bytes" && dim != "route_or_setup" && dim != "window_checkpoints" {
				return false
			}
			seen[dim] = true
		}
		return true
	case "epoch_closed":
		if !exactFields(f, commonFields+" reason unfinished_flows finality") || !null(f["flow_id"]) || !null(f["attempt_id"]) || !literal(f, "reason", "core_shutdown") {
			return false
		}
		n, ok := decimal(f["unfinished_flows"], true, false)
		return ok && (n == 0 && literal(f, "finality", "complete") || n != 0 && literal(f, "finality", "incomplete"))
	}
	return false
}

// DecodeSourceEvent strictly validates v1/v2/v3/v4 candidates, including nested
// ProcessEvidence. It preserves unknown counters as null and candidate timing as
// unverified for v1. It does not infer source continuity from one frame.
func DecodeSourceEvent(raw []byte) (SourceEvent, error) {
	invalid := func() (SourceEvent, error) { return SourceEvent{}, ErrInvalidSourceEvent }
	if len(raw) == 0 || len(raw) > SourceInputLimit || !utf8.Valid(raw) {
		return invalid()
	}
	f, ok := object(raw)
	if !ok || !literal(f, "boundary_ref", "mihomo_proxy_stream_v1") {
		return invalid()
	}
	version := uint8(1)
	if bytes.Equal(bytes.TrimSpace(f["schema_version"]), []byte("2")) {
		version = 2
	} else if bytes.Equal(bytes.TrimSpace(f["schema_version"]), []byte("4")) {
		version = 4
	} else if bytes.Equal(bytes.TrimSpace(f["schema_version"]), []byte("3")) {
		version = 3
	} else if bytes.Equal(bytes.TrimSpace(f["schema_version"]), []byte("5")) {
		version = 5
	} else if !bytes.Equal(bytes.TrimSpace(f["schema_version"]), []byte("1")) {
		return invalid()
	}
	kind, ok := scalarText(f["kind"])
	if !ok {
		return invalid()
	}
	e := SourceEvent{Kind: kind, SchemaVersion: version}
	var epoch, generation string
	for _, item := range []struct {
		key string
		dst *string
	}{{"source_instance_id", &e.SourceInstanceID}, {"source_epoch", &epoch}, {"event_id", &e.EventID}, {"config_generation", &generation}} {
		v, ok := id(f[item.key])
		if !ok {
			return invalid()
		}
		*item.dst = v
	}
	e.SourceEpoch, e.ConfigGeneration = string(epoch), string(generation)
	e.SourceSeq, ok = decimal(f["source_seq"], true, true)
	if !ok {
		return invalid()
	}
	s, ok := scalarText(f["observed_at"])
	if !ok || !strings.HasSuffix(s, "Z") {
		return invalid()
	}
	if _, err := parseTimestamp(s); err != nil {
		return invalid()
	}
	if kind != "epoch_closed" {
		e.FlowID, ok = id(f["flow_id"])
		if !ok {
			return invalid()
		}
		e.AttemptID, ok = id(f["attempt_id"])
		if !ok {
			return invalid()
		}
	}
	if version == 5 {
		if !decodeV5(f, &e) {
			return invalid()
		}
	} else if version == 4 {
		if !decodeV4(f, &e) {
			return invalid()
		}
	} else if version == 3 {
		if !decodeV3(f, &e) {
			return invalid()
		}
	} else if version == 2 {
		if !decodeV2(f, &e) {
			return invalid()
		}
	} else if kind == string(BytesCheckpoint) || kind == string(Closed) {
		counter, err := DecodeCounterEvent(raw)
		if err != nil {
			return invalid()
		}
		e.Counter = &counter
	} else if !kindValid(f, kind) {
		return invalid()
	}
	if kind == "route_decided" && literal(f, "phase", "connection_binding") {
		e.Phase = "connection_binding"
	}
	if kind == "gap" {
		e.GapReason, _ = scalarText(f["reason"])
		e.GapDimensions, _ = stringsArray(f["dimensions"], true)
	}
	if kind == "epoch_closed" {
		e.UnfinishedFlows, _ = decimal(f["unfinished_flows"], true, false)
		e.EpochFinality, _ = scalarText(f["finality"])
	}
	e.Raw = append(json.RawMessage(nil), raw...)
	return e, nil
}
