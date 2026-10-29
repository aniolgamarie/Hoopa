//go:build linux

package mihomoevents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

type controlFake struct {
	mu            sync.Mutex
	states        map[string]ControlObservation
	starts, stops int
	failPath      string
	unknown       bool
	beforeStart   func(ControlLaunch)
	beforeStop    func(ControlLaunch)
}

func (f *controlFake) Validate(b ControlBundle) error {
	if b.CorePath == "/invalid" {
		return errors.New("invalid binary")
	}
	return nil
}
func (f *controlFake) Observe(_ context.Context, l ControlLaunch) (ControlObservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unknown {
		return ControlObservation{State: ControlUnknown}, nil
	}
	if x, ok := f.states[l.Nonce]; ok {
		return x, nil
	}
	return ControlObservation{State: ControlUnknown}, nil
}
func (f *controlFake) Start(_ context.Context, l ControlLaunch) (ControlInstance, error) {
	if f.beforeStart != nil {
		f.beforeStart(l)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.states == nil {
		f.states = map[string]ControlObservation{}
	}
	if l.Bundle.CorePath == f.failPath {
		f.states[l.Nonce] = ControlObservation{State: ControlVerifiedStopped}
		return ControlInstance{}, errors.New("known failed start")
	}
	i := ControlInstance{PID: 100 + f.starts, BootID: "12345678-1234-1234-1234-123456789abc", StartTicks: strconv.Itoa(1000 + f.starts), Nonce: l.Nonce, Generation: l.Generation, BundleDigest: l.BundleDigest}
	f.states[l.Nonce] = ControlObservation{State: ControlVerifiedRunning, Instance: &i}
	return i, nil
}
func (f *controlFake) Stop(_ context.Context, l ControlLaunch) error {
	if f.beforeStop != nil {
		f.beforeStop(l)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.states[l.Nonce] = ControlObservation{State: ControlVerifiedStopped}
	return nil
}
func controlBundle(path string) ControlBundle {
	c := []byte("original config\n")
	h := sha256.Sum256(c)
	return ControlBundle{CorePath: path, CoreSHA256: hex.EncodeToString(h[:]), Config: c, ConfigSHA256: hex.EncodeToString(h[:]), RouteMap: json.RawMessage(`{"nodes":[]}`)}
}
func controlOpen(t *testing.T) (*ControlProbe, *controlFake, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "control")
	if err := InitControlProbe(dir); err != nil {
		t.Fatal(err)
	}
	f := &controlFake{}
	p, e := OpenControlProbe(dir, f)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close() })
	return p, f, dir
}
func controlApply(id string, rev uint64, path string) ControlRequest {
	b := controlBundle(path)
	return ControlRequest{Version: 1, RequestID: id, Action: "apply", ExpectedRevision: rev, Bundle: &b, AcceptDisruption: true}
}
func controlTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	e := filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			v, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			m[p] = string(v)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestControlProbeC01AndDuplicate(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	before := controlTree(t, d)
	_, e := p.Execute(ctx, controlApply("stale", 9, "/core"))
	if !errors.Is(e, ErrControlConflict) || !reflect.DeepEqual(before, controlTree(t, d)) || f.starts != 0 {
		t.Fatalf("C01: %v", e)
	}
	r := controlApply("first", 0, "/core")
	op, e := p.Execute(ctx, r)
	if e != nil || op.Phase != "committed" {
		t.Fatalf("%+v %v", op, e)
	}
	before = controlTree(t, d)
	retry, e := p.Execute(ctx, r)
	if e != nil || retry.ID != op.ID || f.starts != 1 || !reflect.DeepEqual(before, controlTree(t, d)) {
		t.Fatalf("retry %v", e)
	}
	r.Bundle = ptrControlBundle(controlBundle("/other"))
	if _, e = p.Execute(ctx, r); !errors.Is(e, ErrControlIDConflict) {
		t.Fatal(e)
	}
}
func ptrControlBundle(b ControlBundle) *ControlBundle { return &b }
func TestControlProbeReadOnlyAndClose(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	if _, e := p.Execute(ctx, controlApply("first", 0, "/core")); e != nil {
		t.Fatal(e)
	}
	before := controlTree(t, d)
	if _, e := p.Status(ctx); e != nil {
		t.Fatal(e)
	}
	if e := p.Close(); e != nil {
		t.Fatal(e)
	}
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if _, e = q.Status(ctx); e != nil {
		t.Fatal(e)
	}
	if f.stops != 0 || !reflect.DeepEqual(before, controlTree(t, d)) {
		t.Fatal("read/open/close mutated state")
	}
}
func TestControlProbePublishRecoveryKeepsInstance(t *testing.T) {
	p, f, d := controlOpen(t)
	p.hook = func(point string) error {
		if point == "after-active" {
			return errors.New("crash")
		}
		return nil
	}
	op, e := p.Execute(context.Background(), controlApply("first", 0, "/core"))
	if e == nil {
		t.Fatal("fault not reached")
	}
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	done, e := q.Recover(context.Background(), op.ID, 1)
	if e != nil || done.Phase != "committed" || f.starts != 1 {
		t.Fatalf("recovery %+v %v starts%d", done, e, f.starts)
	}
}
func TestControlProbeIntentBeforeActionsAndRollback(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	assertIntent := func(l ControlLaunch) {
		raw, e := os.ReadFile(filepath.Join(d, "launches", l.Nonce+".json"))
		if e != nil || len(raw) == 0 {
			t.Errorf("intent missing before action: %v", e)
		}
	}
	f.beforeStart = assertIntent
	f.beforeStop = assertIntent
	first, e := p.Execute(ctx, controlApply("first", 0, "/old"))
	if e != nil {
		t.Fatal(e)
	}
	f.failPath = "/new"
	op, e := p.Execute(ctx, controlApply("replace", 1, "/new"))
	if e != nil || op.Phase != "rolled_back" {
		t.Fatalf("%+v %v", op, e)
	}
	s, e := p.Status(ctx)
	if e != nil || s.Active.Revision != 1 || s.Active.Bundle.CorePath != "/old" || s.Active.Launch.Nonce == first.Target.Nonce || f.starts != 3 {
		t.Fatalf("rollback %+v %v", s, e)
	}
}
func TestControlProbeUnknownNeverKillsOrSpawns(t *testing.T) {
	p, f, _ := controlOpen(t)
	ctx := context.Background()
	if _, e := p.Execute(ctx, controlApply("first", 0, "/old")); e != nil {
		t.Fatal(e)
	}
	f.unknown = true
	op, e := p.Execute(ctx, controlApply("replace", 1, "/new"))
	if e == nil || op.Phase != "degraded" || f.stops != 0 || f.starts != 1 {
		t.Fatalf("%+v %v", op, e)
	}
}
func TestControlProbeStopRecoveryNeverRestarts(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	p.Execute(ctx, controlApply("first", 0, "/old"))
	p.hook = func(point string) error {
		if point == "old-stopped" {
			return errors.New("crash")
		}
		return nil
	}
	op, e := p.Execute(ctx, ControlRequest{Version: 1, RequestID: "stop", Action: "stop", ExpectedRevision: 1, AcceptDisruption: true})
	if e == nil {
		t.Fatal("fault missing")
	}
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	op, e = q.Recover(ctx, op.ID, 1)
	if e != nil || op.Phase != "committed" || f.starts != 1 {
		t.Fatalf("%+v %v", op, e)
	}
}

func TestControlProbeCrashWindows(t *testing.T) {
	for _, point := range []string{"recorded", "stop-old", "old-stopped", "new-spawn", "launch-intent", "spawn-returned-before-record", "spawn-returned", "new-verified", "publishing", "before-active", "after-active", "before-committed"} {
		t.Run(point, func(t *testing.T) {
			p, f, d := controlOpen(t)
			p.hook = func(s string) error {
				if s == point {
					return errors.New("injected crash")
				}
				return nil
			}
			op, e := p.Execute(context.Background(), controlApply("first", 0, "/core"))
			if e == nil {
				t.Fatal("fault not reached")
			}
			p.Close()
			q, e := OpenControlProbe(d, f)
			if e != nil {
				t.Fatal(e)
			}
			defer q.Close()
			beforeStarts := f.starts
			rev := q.active.Revision
			op, e = q.Recover(context.Background(), op.ID, rev)
			if point == "launch-intent" {
				if !errors.Is(e, ErrControlUnknown) || op.Phase != "degraded" || f.starts != 0 {
					t.Fatalf("unclaimed spawn %+v %v", op, e)
				}
			} else if e != nil || op.Phase != "committed" || f.starts != 1 {
				t.Fatalf("%+v %v starts%d before%d", op, e, f.starts, beforeStarts)
			}
			if op.RequestDigest != controlDigest(controlApply("first", 0, "/core")) {
				t.Fatal("recover changed original request digest")
			}
		})
	}
}
func TestControlProbeRollbackRecoveryWindows(t *testing.T) {
	for _, point := range []string{"rollback-verified", "rollback-publishing", "after-active"} {
		t.Run(point, func(t *testing.T) {
			p, f, d := controlOpen(t)
			ctx := context.Background()
			if _, e := p.Execute(ctx, controlApply("first", 0, "/old")); e != nil {
				t.Fatal(e)
			}
			f.failPath = "/new"
			p.hook = func(s string) error {
				if s == point {
					return errors.New("rollback crash")
				}
				return nil
			}
			op, e := p.Execute(ctx, controlApply("replace", 1, "/new"))
			if e == nil {
				t.Fatal("fault missing")
			}
			p.Close()
			q, e := OpenControlProbe(d, f)
			if e != nil {
				t.Fatal(e)
			}
			defer q.Close()
			op, e = q.Recover(ctx, op.ID, q.active.Revision)
			if e != nil || op.Phase != "rolled_back" || q.active.Revision != 1 || f.starts != 3 {
				t.Fatalf("%+v %v starts%d", op, e, f.starts)
			}
		})
	}
}
func TestControlProbePendingAndRecoveryRevision(t *testing.T) {
	p, f, d := controlOpen(t)
	p.hook = func(s string) error {
		if s == "recorded" {
			return errors.New("crash")
		}
		return nil
	}
	p.Execute(context.Background(), controlApply("first", 0, "/old"))
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	before := controlTree(t, d)
	if _, e = q.Execute(context.Background(), controlApply("next", 0, "/new")); !errors.Is(e, ErrControlPending) {
		t.Fatal(e)
	}
	if _, e = q.Recover(context.Background(), "first", 1); !errors.Is(e, ErrControlConflict) {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, controlTree(t, d)) {
		t.Fatal("pending/conflict wrote")
	}
}

func TestControlProbeCapacityRejectsBeforeWrites(t *testing.T) {
	p, f, d := controlOpen(t)
	for _, kind := range []string{"config", "route", "path"} {
		t.Run(kind, func(t *testing.T) {
			r := controlApply("large", 0, "/core")
			switch kind {
			case "config":
				r.Bundle.Config = make([]byte, 8193)
				h := sha256.Sum256(r.Bundle.Config)
				r.Bundle.ConfigSHA256 = hex.EncodeToString(h[:])
			case "route":
				r.Bundle.RouteMap = append([]byte(`{"padding":"`), make([]byte, 8193)...)
			case "path":
				r.Bundle.CorePath = "/" + string(make([]byte, 4096))
			}
			before := controlTree(t, d)
			if _, e := p.Execute(context.Background(), r); e == nil {
				t.Fatal("capacity accepted")
			}
			if f.starts != 0 || f.stops != 0 || !reflect.DeepEqual(before, controlTree(t, d)) {
				t.Fatal("capacity failure had effects")
			}
		})
	}
}
func TestControlProbeMalformedInstanceNeverPublishes(t *testing.T) {
	for _, kind := range []string{"pid", "boot", "ticks", "overflow", "nonce", "generation", "digest"} {
		t.Run(kind, func(t *testing.T) {
			p, f, _ := controlOpen(t)
			p.hook = func(s string) error {
				if s == "spawn-returned" {
					for n, o := range f.states {
						v := *o.Instance
						switch kind {
						case "pid":
							v.PID = 0
						case "boot":
							v.BootID = "boot"
						case "ticks":
							v.StartTicks = "01"
						case "overflow":
							v.StartTicks = "18446744073709551616"
						case "nonce":
							v.Nonce = "wrong"
						case "generation":
							v.Generation = "wrong"
						case "digest":
							v.BundleDigest = "wrong"
						}
						o.Instance = &v
						f.states[n] = o
					}
				}
				return nil
			}
			op, e := p.Execute(context.Background(), controlApply("first", 0, "/core"))
			if e == nil || op.Phase != "degraded" || p.active.Revision != 0 || f.stops != 0 {
				t.Fatalf("%+v %v", op, e)
			}
		})
	}
}

func TestControlProbeRollbackUnknownPreservesEvidence(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	p.Execute(ctx, controlApply("first", 0, "/old"))
	f.failPath = "/new"
	launches := 0
	p.hook = func(s string) error {
		if s == "launch-intent" {
			launches++
			if launches == 2 {
				return errors.New("rollback spawn intent persisted, no claim")
			}
		}
		return nil
	}
	op, e := p.Execute(ctx, controlApply("replace", 1, "/new"))
	if e == nil || op.Rollback == nil {
		t.Fatal("rollback window missing")
	}
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	op, e = q.Recover(ctx, op.ID, 1)
	if !errors.Is(e, ErrControlUnknown) || op.Phase != "degraded" || f.starts != 2 {
		t.Fatalf("%+v %v starts%d", op, e, f.starts)
	}
	before := controlTree(t, d)
	q.Status(ctx)
	if !reflect.DeepEqual(before, controlTree(t, d)) {
		t.Fatal("degraded status wrote")
	}
}
func TestControlProbeOldStillUnknownAfterTargetVerified(t *testing.T) {
	p, f, d := controlOpen(t)
	ctx := context.Background()
	p.Execute(ctx, controlApply("first", 0, "/old"))
	p.hook = func(s string) error {
		if s == "new-verified" {
			return errors.New("crash")
		}
		return nil
	}
	op, e := p.Execute(ctx, controlApply("replace", 1, "/new"))
	if e == nil {
		t.Fatal("fault missing")
	}
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	old := op.Base.Launch.Nonce
	f.states[old] = ControlObservation{State: ControlUnknown}
	starts, stops := f.starts, f.stops
	op, e = q.Recover(ctx, op.ID, 1)
	if !errors.Is(e, ErrControlUnknown) || op.Phase != "degraded" || f.starts != starts || f.stops != stops {
		t.Fatalf("unproven old end %+v %v", op, e)
	}
}

func TestControlProbeUncertainActivePublishReobservesSameInstance(t *testing.T) {
	p, f, d := controlOpen(t)
	p.hook = func(point string) error {
		if point == "before-active" {
			p.store.fault = func(s string) error {
				if s == "rename" {
					return errors.New("active rename outcome uncertain")
				}
				return nil
			}
		}
		return nil
	}
	op, e := p.Execute(context.Background(), controlApply("first", 0, "/core"))
	if !errors.Is(e, ErrControlPoisoned) {
		t.Fatal(e)
	}
	p.Close()
	q, e := OpenControlProbe(d, f)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if q.active.Revision != 1 {
		t.Fatal("actual renamed record not loaded")
	}
	op, e = q.Recover(context.Background(), op.ID, 1)
	if e != nil || op.Phase != "committed" || f.starts != 1 || f.stops != 0 {
		t.Fatalf("%+v %v", op, e)
	}
}
