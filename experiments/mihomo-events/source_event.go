package mihomoevents

import (
	"encoding/json"
	"hoopa/internal/config"
	"hoopa/protocol/telemetrywire"
)

const sourceInputLimit = telemetrywire.SourceInputLimit

var ErrInvalidSourceEvent = telemetrywire.ErrInvalidSourceEvent

// SourceEvent retains the experiment API; parsing is shared with the source.
type SourceEvent struct {
	SchemaVersion                          uint8
	SourceInstanceID                       config.ID
	SourceEpoch                            config.CounterEpoch
	EventID                                config.ID
	SourceSeq                              uint64
	FlowID, AttemptID                      config.ID
	ConfigGeneration                       config.Generation
	Kind                                   string
	Counter                                *CounterEvent
	Raw                                    json.RawMessage
	FlowStartNS                            uint64
	ActualConnectionRef                    string
	ConnectionID                           string
	OrderedChain                           []telemetrywire.RouteHop
	Quality                                *telemetrywire.SetupQuality
	Segments                               []telemetrywire.SegmentCounter
	UnallocatedUpload, UnallocatedDownload uint64
	wire                                   telemetrywire.SourceEvent
}

func DecodeSourceEvent(raw []byte) (SourceEvent, error) {
	e, err := telemetrywire.DecodeSourceEvent(raw)
	if err != nil {
		return SourceEvent{}, err
	}
	out := SourceEvent{SourceInstanceID: config.ID(e.SourceInstanceID), SourceEpoch: config.CounterEpoch(e.SourceEpoch), EventID: config.ID(e.EventID), SourceSeq: e.SourceSeq, FlowID: config.ID(e.FlowID), AttemptID: config.ID(e.AttemptID), ConfigGeneration: config.Generation(e.ConfigGeneration), Kind: e.Kind, Raw: e.Raw, wire: e}
	out.SchemaVersion, out.FlowStartNS = e.SchemaVersion, e.FlowStartNS
	out.ActualConnectionRef, out.ConnectionID = e.ActualConnectionRef, e.ConnectionID
	out.OrderedChain, out.Quality, out.Segments = e.OrderedChain, e.Quality, e.Segments
	out.UnallocatedUpload, out.UnallocatedDownload = e.UnallocatedUpload, e.UnallocatedDownload
	if e.Counter != nil {
		c := counterFromWire(*e.Counter)
		out.Counter = &c
	}
	return out, nil
}
