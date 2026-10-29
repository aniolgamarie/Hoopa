package telemetrywire

import (
	"bytes"
	"encoding/json"
)

// RouteHop is the identity frozen on an actual connection, in core chain order.
type RouteHop struct {
	InternalName string  `json:"internal_name"`
	AdapterType  string  `json:"adapter_type"`
	ProviderName string  `json:"provider_name"`
	NodeID       *string `json:"node_id"`
	ProviderID   *string `json:"provider_id"`
	GroupID      *string `json:"group_id"`
}

type SegmentCounter struct {
	ActualConnectionRef string `json:"actual_connection_ref"`
	AttemptID           string `json:"attempt_id"`
	UploadBytes         uint64 `json:"upload_bytes,string"`
	DownloadBytes       uint64 `json:"download_bytes,string"`
}

type SetupQuality struct {
	Definition          string  `json:"definition"`
	Protocol            string  `json:"protocol"`
	ReadyKind           string  `json:"ready_kind"`
	Outcome             string  `json:"outcome"`
	Reason              string  `json:"reason"`
	FailureStage        string  `json:"failure_stage"`
	FlowStartNS         uint64  `json:"flow_start_ns,string"`
	AttemptStartNS      uint64  `json:"attempt_start_ns,string"`
	ReadyNS             *uint64 `json:"ready_ns,string"`
	FinishNS            uint64  `json:"finish_ns,string"`
	DurationMS          *uint64 `json:"duration_ms,string"`
	FlowDurationMS      *uint64 `json:"flow_duration_ms,string"`
	ObservedWaitMS      uint64  `json:"observed_wait_ms,string"`
	FlowObservedWaitMS  uint64  `json:"flow_observed_wait_ms,string"`
	ActualConnectionRef *string `json:"actual_connection_ref"`
}

func nullableID(raw []byte) (*string, bool) {
	if null(raw) {
		return nil, true
	}
	v, ok := id(raw)
	return &v, ok
}
func arrayObjects(raw []byte, max int) ([]wireFields, bool) {
	raw = bytes.TrimSpace(raw)
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil || len(items) > max {
		return nil, false
	}
	out := make([]wireFields, 0, len(items))
	for _, r := range items {
		f, ok := object(r)
		if !ok {
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}
func decodeChain(raw []byte) ([]RouteHop, bool) {
	items, ok := arrayObjects(raw, 8)
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]RouteHop, 0, len(items))
	for _, f := range items {
		if !exactFields(f, "internal_name adapter_type provider_name node_id provider_id group_id") {
			return nil, false
		}
		var h RouteHop
		for _, p := range []struct {
			k   string
			dst *string
		}{{"internal_name", &h.InternalName}, {"adapter_type", &h.AdapterType}, {"provider_name", &h.ProviderName}} {
			v, ok := scalarText(f[p.k])
			if !ok || p.k != "provider_name" && v == "" {
				return nil, false
			}
			*p.dst = v
		}
		h.NodeID, ok = nullableID(f["node_id"])
		if !ok {
			return nil, false
		}
		h.ProviderID, ok = nullableID(f["provider_id"])
		if !ok {
			return nil, false
		}
		h.GroupID, ok = nullableID(f["group_id"])
		if !ok {
			return nil, false
		}
		if h.NodeID != nil && (h.GroupID != nil || *h.NodeID != h.InternalName) || h.GroupID != nil && *h.GroupID != h.InternalName || h.ProviderID != nil && (*h.ProviderID != h.ProviderName || h.ProviderName == "") {
			return nil, false
		}
		out = append(out, h)
	}
	return out, true
}

const qualityFields = "definition protocol ready_kind outcome reason failure_stage flow_start_ns attempt_start_ns ready_ns finish_ns duration_ms flow_duration_ms observed_wait_ms flow_observed_wait_ms actual_connection_ref"

func decodeQuality(f wireFields) (*SetupQuality, bool) {
	if !exactFields(f, commonFields+" "+qualityFields) || !literal(f, "definition", "forward_setup_v2") {
		return nil, false
	}
	q := &SetupQuality{Definition: "forward_setup_v2"}
	for _, p := range []struct {
		k   string
		dst *string
	}{{"protocol", &q.Protocol}, {"ready_kind", &q.ReadyKind}, {"outcome", &q.Outcome}, {"reason", &q.Reason}, {"failure_stage", &q.FailureStage}} {
		v, ok := scalarText(f[p.k])
		if !ok {
			return nil, false
		}
		*p.dst = v
	}
	for _, p := range []struct {
		k   string
		dst *uint64
	}{{"flow_start_ns", &q.FlowStartNS}, {"attempt_start_ns", &q.AttemptStartNS}, {"finish_ns", &q.FinishNS}, {"observed_wait_ms", &q.ObservedWaitMS}, {"flow_observed_wait_ms", &q.FlowObservedWaitMS}} {
		v, ok := decimal(f[p.k], true, false)
		if !ok {
			return nil, false
		}
		*p.dst = v
	}
	for _, p := range []struct {
		k   string
		dst **uint64
	}{{"ready_ns", &q.ReadyNS}, {"duration_ms", &q.DurationMS}, {"flow_duration_ms", &q.FlowDurationMS}} {
		if !null(f[p.k]) {
			v, ok := decimal(f[p.k], true, false)
			if !ok {
				return nil, false
			}
			*p.dst = &v
		}
	}
	var ok bool
	q.ActualConnectionRef, ok = nullableID(f["actual_connection_ref"])
	if !ok {
		return nil, false
	}
	ready := map[string]string{"direct_tcp": "tcp_connected", "http_connect": "http_connect_accepted", "socks5": "socks5_connect_accepted", "unknown": "ready_unproven"}
	want, ok := ready[q.Protocol]
	if !ok || q.ReadyKind != "ready_unproven" && q.ReadyKind != want {
		return nil, false
	}
	if q.FlowStartNS > q.AttemptStartNS || q.AttemptStartNS > q.FinishNS || q.ObservedWaitMS != (q.FinishNS-q.AttemptStartNS)/1000000 || q.FlowObservedWaitMS != (q.FinishNS-q.FlowStartNS)/1000000 {
		return nil, false
	}
	if q.ReadyKind == "ready_unproven" {
		if q.ReadyNS != nil || q.Reason != "ready_unknown" {
			return nil, false
		}
	} else {
		if q.ReadyNS == nil || q.ActualConnectionRef == nil || *q.ReadyNS < q.AttemptStartNS || *q.ReadyNS > q.FinishNS || q.Reason != "" {
			return nil, false
		}
	}
	switch q.Outcome {
	case "success":
		if q.ReadyNS == nil || q.DurationMS == nil || q.FlowDurationMS == nil || *q.DurationMS != (*q.ReadyNS-q.AttemptStartNS)/1000000 || *q.FlowDurationMS != (*q.ReadyNS-q.FlowStartNS)/1000000 || q.FailureStage != "" {
			return nil, false
		}
	case "unknown":
		if q.ReadyNS != nil || q.ActualConnectionRef == nil || q.FailureStage != "" || q.DurationMS != nil || q.FlowDurationMS != nil {
			return nil, false
		}
	case "failure", "timeout", "cancelled":
		if q.DurationMS != nil || q.FlowDurationMS != nil || q.FailureStage != "dial_or_handshake" && q.FailureStage != "initial_write" && q.FailureStage != "pre_route" {
			return nil, false
		}
	default:
		return nil, false
	}
	return q, true
}
func decodeV2(f wireFields, e *SourceEvent) bool {
	var ok bool
	switch e.Kind {
	case "opened":
		e.FlowStartNS, ok = decimal(f["flow_start_ns"], true, false)
		if !ok {
			return false
		}
		copy := make(wireFields, len(f))
		for k, v := range f {
			if k != "flow_start_ns" {
				copy[k] = v
			}
		}
		return kindValid(copy, e.Kind)
	case "route_decided":
		if _, exists := f["phase"]; !exists {
			return kindValid(f, e.Kind)
		}
		e.Phase, _ = scalarText(f["phase"])
		e.ActualConnectionRef, ok = id(f["actual_connection_ref"])
		if !ok {
			return false
		}
		switch e.Phase {
		case "actual_connection":
			if !exactFields(f, commonFields+" phase actual_connection_ref ordered_chain") {
				return false
			}
			e.OrderedChain, ok = decodeChain(f["ordered_chain"])
			return ok
		case "connection_binding":
			e.ConnectionID, ok = id(f["connection_id"])
			return ok && exactFields(f, commonFields+" phase connection_id actual_connection_ref")
		}
		return false
	case "setup_finished":
		e.Quality, ok = decodeQuality(f)
		if ok && e.Quality.ActualConnectionRef != nil {
			e.ActualConnectionRef = *e.Quality.ActualConnectionRef
		}
		return ok
	case "bytes_checkpoint", "closed":
		if !exactFields(f, commonFields+" upload_bytes download_bytes segments unallocated_upload_bytes unallocated_download_bytes") {
			return false
		}
		up, ok := decimal(f["upload_bytes"], true, false)
		if !ok {
			return false
		}
		down, ok := decimal(f["download_bytes"], true, false)
		if !ok {
			return false
		}
		e.UnallocatedUpload, ok = decimal(f["unallocated_upload_bytes"], true, false)
		if !ok {
			return false
		}
		e.UnallocatedDownload, ok = decimal(f["unallocated_download_bytes"], true, false)
		if !ok {
			return false
		}
		items, ok := arrayObjects(f["segments"], 10)
		if !ok {
			return false
		}
		e.Segments = make([]SegmentCounter, 0, len(items))
		seen := map[string]bool{}
		u, d := e.UnallocatedUpload, e.UnallocatedDownload
		for _, f := range items {
			if !exactFields(f, "actual_connection_ref attempt_id upload_bytes download_bytes") {
				return false
			}
			var c SegmentCounter
			c.ActualConnectionRef, ok = id(f["actual_connection_ref"])
			if !ok || seen[c.ActualConnectionRef] {
				return false
			}
			seen[c.ActualConnectionRef] = true
			c.AttemptID, ok = id(f["attempt_id"])
			if !ok {
				return false
			}
			c.UploadBytes, ok = decimal(f["upload_bytes"], true, false)
			if !ok {
				return false
			}
			c.DownloadBytes, ok = decimal(f["download_bytes"], true, false)
			if !ok || ^uint64(0)-u < c.UploadBytes || ^uint64(0)-d < c.DownloadBytes {
				return false
			}
			u += c.UploadBytes
			d += c.DownloadBytes
			e.Segments = append(e.Segments, c)
		}
		if u != up || d != down {
			return false
		}
		observed, _ := scalarText(f["observed_at"])
		t, _ := parseTimestamp(observed)
		e.Counter = &CounterEvent{SchemaVersion: 2, SourceInstanceID: e.SourceInstanceID, SourceEpoch: e.SourceEpoch, EventID: e.EventID, SourceSeq: e.SourceSeq, FlowID: e.FlowID, AttemptID: e.AttemptID, ObservedAt: t, ConfigGeneration: e.ConfigGeneration, BoundaryRef: "mihomo_proxy_stream_v1", Kind: CounterKind(e.Kind), UploadBytes: up, DownloadBytes: down}
		return true
	default:
		return kindValid(f, e.Kind)
	}
}
