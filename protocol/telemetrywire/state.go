package telemetrywire

import "errors"

var ErrInvalidLifecycle = errors.New("invalid source epoch lifecycle")

// MaxActiveFlows is the concurrent admission bound of this frozen P0 profile.
// Terminal history remains available independently of the active-flow budget.
const MaxActiveFlows = 32

// Flow projects confirmed v1/v2 cumulative frames, not a network-end observation.
type Flow struct {
	SourceEpoch    string
	FlowID         string
	OpenedEventID  string
	State          string
	Reason         string
	LastCheckpoint *CounterEvent
	Final          *CounterEvent
	TailBytes      *uint64
	GapDimensions  []string
}

type Lifecycle struct {
	SchemaVersion    uint8
	SourceEpoch      string
	SourceInstanceID string
	ConfigGeneration string
	LastSourceSeq    uint64
	EpochFinality    string
	Flows            []Flow
}

type flowState struct {
	Flow
	terminal, shutdown, gapSeen bool
	quality                     v2Flow
}

// State is a single-writer, pure epoch validator. Its zero value is ready for
// the first sequence-1 event. Only pass unmodified DecodeSourceEvent results.
// Validate has no side effects; Commit revalidates and advances after the
// caller's durable write succeeds. No duplicate event is accepted here: receipt
// deduplication belongs to the receiver and precedes validation.
type State struct {
	epoch, instance, generation string
	version                     uint8
	last                        uint64
	finality                    string
	flows                       map[string]flowState
	order                       []string
	events                      map[string]bool
	attempts                    map[string]string
	finished                    map[string]bool
	active                      int
}

func (s *State) next(e SourceEvent) (flowState, error) {
	bad := func() (flowState, error) { return flowState{}, ErrInvalidLifecycle }
	if s.last == ^uint64(0) || e.SourceSeq != s.last+1 || s.finality != "" || s.events[e.EventID] {
		return bad()
	}
	if s.last != 0 && (s.epoch != e.SourceEpoch || s.instance != e.SourceInstanceID || s.generation != e.ConfigGeneration || s.version != e.SchemaVersion) {
		return bad()
	}
	if e.Kind == "epoch_closed" {
		var unfinished uint64
		for _, f := range s.flows {
			if !f.terminal {
				return bad()
			}
			if f.shutdown {
				unfinished++
			}
		}
		if unfinished != e.UnfinishedFlows {
			return bad()
		}
		return flowState{}, nil
	}
	if owner, ok := s.attempts[e.AttemptID]; ok && owner != e.FlowID {
		return bad()
	}
	f, exists := s.flows[e.FlowID]
	if e.Kind == "opened" {
		if exists || s.active >= MaxActiveFlows {
			return bad()
		}
		return flowState{quality: v2Flow{start: e.FlowStartNS}, Flow: Flow{SourceEpoch: e.SourceEpoch, FlowID: e.FlowID, OpenedEventID: e.EventID, State: "pending", Reason: "source_final_missing", GapDimensions: []string{}}}, nil
	}
	if !exists || f.terminal && e.Kind != "gap" {
		return bad()
	}
	if e.SchemaVersion == 2 || e.SchemaVersion == 3 || e.SchemaVersion == 4 || e.SchemaVersion == 5 {
		var ok bool
		f.quality, ok = s.nextV2(e, f.quality)
		if !ok {
			return bad()
		}
	}
	switch e.Kind {
	case "route_decided":
		// v1 has no attempt-start event: decisions, bindings and checkpoints may
		// observe an attempt before setup_finished. Multiple attempts are valid.
	case "setup_finished":
		if s.finished[e.AttemptID] {
			return bad()
		}
	case "bytes_checkpoint", "closed":
		if e.Counter == nil {
			return bad()
		}
		previous := f.LastCheckpoint
		if previous != nil && (e.Counter.UploadBytes < previous.UploadBytes || e.Counter.DownloadBytes < previous.DownloadBytes) {
			return bad()
		}
		counter := *e.Counter
		if e.Kind == "bytes_checkpoint" {
			f.LastCheckpoint = &counter
		} else {
			zero := uint64(0)
			f.Final, f.TailBytes, f.State, f.Reason, f.terminal = &counter, &zero, "closed", "", true
		}
	case "gap":
		if f.gapSeen {
			return bad()
		}
		for _, dim := range e.GapDimensions {
			if dim == "bytes" && f.Final != nil {
				return bad()
			}
		}
		f.GapDimensions = append(append([]string{}, f.GapDimensions...), e.GapDimensions...)
		if f.Final == nil {
			f.State, f.Reason = "gap", e.GapReason
		}
		f.gapSeen, f.terminal = true, true
		f.shutdown = e.GapReason == "source_shutdown_before_final"
	default:
		return bad()
	}
	return f, nil
}

func (s *State) Validate(e SourceEvent) error { _, err := s.next(e); return err }

func (s *State) Commit(e SourceEvent) error {
	f, err := s.next(e)
	if err != nil {
		return err
	}
	if s.events == nil {
		s.events = map[string]bool{}
		s.flows = map[string]flowState{}
		s.attempts = map[string]string{}
		s.finished = map[string]bool{}
	}
	if s.last == 0 {
		s.epoch, s.instance, s.generation = e.SourceEpoch, e.SourceInstanceID, e.ConfigGeneration
		s.version = e.SchemaVersion
	}
	s.last = e.SourceSeq
	s.events[e.EventID] = true
	if e.Kind == "epoch_closed" {
		s.finality = e.EpochFinality
		return nil
	}
	if e.Kind == "opened" {
		s.order = append(s.order, e.FlowID)
		s.active++
	} else if previous := s.flows[e.FlowID]; !previous.terminal && f.terminal {
		s.active--
	}
	s.flows[e.FlowID] = f
	s.attempts[e.AttemptID] = e.FlowID
	if e.Kind == "setup_finished" {
		s.finished[e.AttemptID] = true
	}
	return nil
}

// Snapshot returns a detached, stable projection in original opened order.
func (s *State) Snapshot() Lifecycle {
	finality := s.finality
	if finality == "" {
		finality = "unverified"
	}
	out := Lifecycle{SchemaVersion: s.version, SourceEpoch: s.epoch, SourceInstanceID: s.instance, ConfigGeneration: s.generation, LastSourceSeq: s.last, EpochFinality: finality, Flows: []Flow{}}
	for _, id := range s.order {
		f := s.flows[id].Flow
		f.GapDimensions = append([]string{}, f.GapDimensions...)
		if f.LastCheckpoint != nil {
			v := *f.LastCheckpoint
			f.LastCheckpoint = &v
		}
		if f.Final != nil {
			v := *f.Final
			f.Final = &v
		}
		if f.TailBytes != nil {
			v := *f.TailBytes
			f.TailBytes = &v
		}
		out.Flows = append(out.Flows, f)
	}
	return out
}
