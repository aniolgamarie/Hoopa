package telemetrywire

// This evidence is deliberately absent from Lifecycle and recovery: totals do
// not imply that a recovered flow has route or quality evidence.
type v2Flow struct {
	preRouteAborted                        bool
	start, lastAttemptStart                uint64
	attempts                               map[string]SetupQuality
	connections                            map[string]string
	trackers                               map[string]string
	counters                               map[string]SegmentCounter
	unallocatedUpload, unallocatedDownload uint64
}

func (s *State) nextV2(e SourceEvent, old v2Flow) (v2Flow, bool) {
	f := old
	f.connections = make(map[string]string, len(old.connections))
	for k, v := range old.connections {
		f.connections[k] = v
	}
	f.trackers = make(map[string]string, len(old.trackers))
	for k, v := range old.trackers {
		f.trackers[k] = v
	}
	f.counters = make(map[string]SegmentCounter, len(old.counters))
	for k, v := range old.counters {
		f.counters[k] = v
	}
	f.attempts = make(map[string]SetupQuality, len(old.attempts))
	for k, v := range old.attempts {
		f.attempts[k] = v
	}
	bad := func() (v2Flow, bool) { return v2Flow{}, false }
	if f.preRouteAborted && (e.Kind == "setup_finished" || e.Kind == "route_decided" && e.Phase != "") {
		return bad()
	}
	switch e.Kind {
	case "route_decided":
		switch e.Phase {
		case "actual_connection":
			if len(f.connections) >= 10 || s.finished[e.AttemptID] {
				return bad()
			}
			for _, attempt := range f.connections {
				if attempt == e.AttemptID {
					return bad()
				}
			}
			for _, other := range s.flows {
				if _, exists := other.quality.connections[e.ActualConnectionRef]; exists {
					return bad()
				}
			}
			f.connections[e.ActualConnectionRef] = e.AttemptID
		case "connection_binding":
			if f.connections[e.ActualConnectionRef] != e.AttemptID {
				return bad()
			}
			for _, other := range s.flows {
				for tracker, ref := range other.quality.trackers {
					if tracker == e.ConnectionID || ref == e.ActualConnectionRef {
						return bad()
					}
				}
			}
			f.trackers[e.ConnectionID] = e.ActualConnectionRef
		}
	case "setup_finished":
		q := e.Quality
		if q == nil || q.FlowStartNS != f.start || q.AttemptStartNS < f.lastAttemptStart {
			return bad()
		}
		if q.FailureStage == "pre_route" && (q.ActualConnectionRef != nil || q.AttemptStartNS != f.start || len(f.connections) != 0 || len(f.attempts) != 0) {
			return bad()
		}
		if q.ActualConnectionRef != nil && f.connections[*q.ActualConnectionRef] != e.AttemptID {
			return bad()
		}
		// A registered returned Conn cannot subsequently be reported as absent.
		for ref, attempt := range f.connections {
			if attempt == e.AttemptID && (q.ActualConnectionRef == nil || ref != *q.ActualConnectionRef) {
				return bad()
			}
		}
		f.lastAttemptStart = q.AttemptStartNS
		f.attempts[e.AttemptID] = *q
		f.preRouteAborted = q.FailureStage == "pre_route"
	case "bytes_checkpoint", "closed":
		if len(e.Segments) != len(f.connections) || e.UnallocatedUpload < f.unallocatedUpload || e.UnallocatedDownload < f.unallocatedDownload {
			return bad()
		}
		for _, c := range e.Segments {
			if f.connections[c.ActualConnectionRef] != c.AttemptID {
				return bad()
			}
			prior := f.counters[c.ActualConnectionRef]
			if c.UploadBytes < prior.UploadBytes || c.DownloadBytes < prior.DownloadBytes {
				return bad()
			}
			f.counters[c.ActualConnectionRef] = c
		}
		f.unallocatedUpload, f.unallocatedDownload = e.UnallocatedUpload, e.UnallocatedDownload
	}
	return f, true
}
