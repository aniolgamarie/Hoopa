//go:build linux && amd64

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/subscription"
)

type Options struct {
	LauncherArgs []string
	RestartOnce  bool
	Ready        func(ReadyInfo) error
}
type ReadyInfo struct {
	Instance string          `json:"instance"`
	Revision config.Revision `json:"revision"`
}

// The generic admission entry has no compiler callbacks. The finite source
// applier supplies validated per-request dependencies without changing these.
func unavailableIntent(json.RawMessage, []control.PlanInput) error {
	return control.ErrCapabilityUnsupported
}
func unavailableInputs(context.Context, control.Plan) (control.CurrentPlanInputs, error) {
	return control.CurrentPlanInputs{}, control.ErrCapabilityUnsupported
}
func unavailableMaterial(context.Context, control.Plan) (control.BasicMaterial, error) {
	return control.BasicMaterial{}, control.ErrCapabilityUnsupported
}

type server struct {
	coordinator  *control.Coordinator
	applier      *control.BasicApplier
	sources      *control.SourceUpdater
	endpoint     *Endpoint
	background   context.Context
	cancel       context.CancelFunc
	quiesced     bool // one synchronous dispatcher owns admission
	restartArmed bool
	watch        *control.RestartWatch // set only by dispatcher; watch synchronizes itself
	worker       sync.WaitGroup
	watchMu      sync.Mutex
	restartState string
}

// Run owns the writer until admission has closed and its background worker has
// stopped. Context cancellation and service.stop never signal the core.
func Run(ctx context.Context, paths config.Paths, options Options) (result error) {
	if ctx == nil || len(options.LauncherArgs) == 0 {
		return control.ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := control.OpenCoordinator(paths, unavailableIntent, unavailableInputs)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, c.Close()) }()
	a, err := control.NewSourcesApplier(c, options.LauncherArgs)
	if err != nil {
		return err
	}
	e, err := OpenEndpoint(c)
	if err != nil {
		return err
	}
	background, cancel := context.WithCancel(ctx)
	sources, err := control.NewSourceUpdater(c, background, subscription.FetchOptions{})
	if err != nil {
		cancel()
		return errors.Join(err, e.Close())
	}
	s := &server{coordinator: c, applier: a, sources: sources, endpoint: e, background: background, cancel: cancel, restartArmed: options.RestartOnce, restartState: "disabled"}
	if options.RestartOnce {
		s.restartState = "armed_for_next_start"
	}
	defer func() {
		cancel()
		if s.watch != nil {
			_ = s.watch.Close()
		}
		s.worker.Wait()
		result = errors.Join(result, e.Close())
	}()
	s.worker.Add(1)
	go func() {
		defer s.worker.Done()
		_ = sources.RunSchedule(background)
	}()
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if options.Ready != nil {
		if err := options.Ready(ReadyInfo{e.Instance(), status.Revision}); err != nil {
			return err
		}
	}
	for !s.quiesced {
		conn, err := e.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Handshake rejection is local to that already authenticated peer.
			if errors.Is(err, ErrDiscovery) {
				continue
			}
			return err
		}
		requestContext, cancelRequest := context.WithTimeout(ctx, wireTimeout)
		raw, err := ReadRequest(requestContext, conn)
		var response []byte
		if err != nil {
			response = Failure(raw, control.ErrRequest)
		} else {
			response = s.dispatch(requestContext, raw)
		}
		// A service.stop callback closes listener admission, not this connection
		// or its request context, so the durable receipt can still be delivered.
		_ = WriteResponse(requestContext, conn, response)
		cancelRequest()
		_ = conn.Close()
	}
	return nil
}

func (s *server) status(ctx context.Context) (any, error) {
	v, err := s.coordinator.Status(ctx)
	if err != nil {
		return nil, err
	}
	if s.watch != nil {
		id, exited := s.watch.ExitObserved()
		if exited && s.coordinator.ExitObservationApplies(id) {
			v.Core = control.CoreStatus{State: "verified-stopped", Reason: "observed_original_exit", RunID: &id}
			v.Degraded = true
			v.DegradedReasons = []string{"runtime_exited"}
		}
	}
	s.watchMu.Lock()
	state := s.restartState
	s.watchMu.Unlock()
	v.RestartPolicy = state
	return v, nil
}

func (s *server) armRestart(ctx context.Context) {
	if !s.restartArmed {
		return
	}
	s.restartArmed = false
	v, err := s.coordinator.Status(ctx)
	var w *control.RestartWatch
	if err == nil {
		w, err = s.applier.AuthorizeRestart(ctx, v.Revision)
	}
	if err != nil {
		s.watchMu.Lock()
		s.restartState = "grant_failed"
		s.watchMu.Unlock()
		return
	}
	s.watch = w
	s.watchMu.Lock()
	s.restartState = "watching_original"
	s.watchMu.Unlock()
	s.worker.Add(1)
	go func() {
		defer s.worker.Done()
		_, made, err := w.Attempt(s.background)
		s.watchMu.Lock()
		if err == nil && made {
			s.restartState = "budget_used_watching_successor"
		} else {
			s.restartState = "inactive"
		}
		s.watchMu.Unlock()
		if err == nil && made {
			_, _, _ = w.Attempt(s.background)
		}
		s.watchMu.Lock()
		s.restartState = "inactive"
		s.watchMu.Unlock()
	}()
}

func (s *server) dispatch(ctx context.Context, raw []byte) []byte {
	r, err := control.ParseRequest(raw)
	if err != nil {
		return Failure(raw, err)
	}
	if !control.IsReadOnlyOperation(r.Operation()) {
		if old, err := s.coordinator.Lookup(raw); err == nil {
			return old.Record.Result
		} else if !errors.Is(err, control.ErrOperationNotFound) {
			return Failure(raw, err)
		}
	}
	p, err := schema(r)
	if err != nil {
		return Failure(raw, err)
	}
	var value any
	var stored control.StoredOperation
	var made bool
	switch r.Operation() {
	case "doctor.get":
		value, err = s.coordinator.Doctor(ctx)
	case "status.get":
		value, err = s.status(ctx)
	case "operation.get":
		value, err = s.coordinator.Operation(config.ID(parameter(p, "operation_id")))
	case "subscription.update":
		stored, _, err = s.sources.Update(ctx, raw)
	case "subscription.list":
		cursor, _ := control.SourceListCursor(r)
		value, err = s.coordinator.Sources(ctx, cursor)
	case "config.plan":
		intent, _ := control.SourcePlanIntent(r)
		var plan control.Plan
		plan, err = s.coordinator.PlanSources(ctx, intent)
		if err == nil {
			value = json.RawMessage(plan.Bytes())
		}
	case "config.apply":
		stored, made, err = s.applier.Apply(ctx, raw)
		if err == nil && made && stored.Record.Phase == control.Committed {
			s.armRestart(ctx)
		}
	case "runtime.start":
		if only, _ := wireBool(p["control_only"]); only {
			var status control.ServiceStatus
			status, err = s.coordinator.Status(ctx)
			if err == nil && string(status.Revision) != parameter(p, "expected_revision") {
				err = control.ErrConflict
			}
			value = map[string]any{"service": "running", "instance": s.endpoint.Instance(), "revision": status.Revision, "control_only": true}
		} else {
			stored, made, err = s.applier.EnsureStarted(ctx, raw)
			if err == nil && made && stored.Record.Phase == control.Committed {
				s.armRestart(ctx)
			}
		}
	case "runtime.stop":
		stored, _, err = s.applier.Stop(ctx, raw)
	case "operation.recover":
		stored, _, err = s.applier.Recover(ctx, raw)
	case "service.stop":
		stored, _, err = s.coordinator.StopService(ctx, raw, func() error { s.quiesced = true; s.cancel(); return s.endpoint.Close() })
	}
	if len(stored.Record.Result) != 0 {
		return stored.Record.Result
	}
	if err != nil {
		return Failure(raw, err)
	}
	return success(r, value)
}
