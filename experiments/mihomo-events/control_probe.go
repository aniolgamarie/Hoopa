//go:build linux

package mihomoevents

// This is an experimental single-writer controller, not a production manager.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrControlConflict   = errors.New("control revision conflict")
	ErrControlIDConflict = errors.New("control request ID conflict")
	ErrControlPending    = errors.New("unfinished control operation")
	ErrControlUnknown    = errors.New("control instance unknown")
	ErrControlPoisoned   = errors.New("control storage uncertain; reopen required")
	ErrControlClosed     = errors.New("control probe closed")
)

const (
	ControlVerifiedRunning = "verified-running"
	ControlVerifiedStopped = "verified-stopped"
	ControlUnknown         = "unknown"
)

type ControlBundle struct {
	CorePath     string `json:"core_path"`
	CoreSHA256   string `json:"core_sha256"`
	Config       []byte `json:"config"`
	ConfigSHA256 string `json:"config_sha256"`
	RouteMap     []byte `json:"route_map"`
}
type ControlInstance struct {
	PID          int    `json:"pid"`
	BootID       string `json:"boot_id"`
	StartTicks   string `json:"start_ticks"`
	Nonce        string `json:"nonce"`
	Generation   string `json:"generation"`
	BundleDigest string `json:"bundle_digest"`
}
type ControlLaunch struct {
	Nonce        string           `json:"nonce"`
	Generation   string           `json:"generation"`
	BundleDigest string           `json:"bundle_digest"`
	Directory    string           `json:"directory"`
	Bundle       ControlBundle    `json:"bundle"`
	Instance     *ControlInstance `json:"instance,omitempty"`
}
type ControlObservation struct {
	State    string           `json:"state"`
	Instance *ControlInstance `json:"instance,omitempty"`
}
type ControlBackend interface {
	Validate(ControlBundle) error
	Observe(context.Context, ControlLaunch) (ControlObservation, error)
	Start(context.Context, ControlLaunch) (ControlInstance, error)
	Stop(context.Context, ControlLaunch) error
}
type ControlRequest struct {
	Version          int            `json:"version"`
	RequestID        string         `json:"request_id"`
	Action           string         `json:"action"`
	ExpectedRevision uint64         `json:"expected_revision"`
	Bundle           *ControlBundle `json:"bundle,omitempty"`
	AcceptDisruption bool           `json:"accept_disruption"`
}
type ControlActive struct {
	Version  int            `json:"version"`
	Revision uint64         `json:"revision"`
	Desired  string         `json:"desired"`
	Bundle   *ControlBundle `json:"bundle,omitempty"`
	Launch   *ControlLaunch `json:"launch,omitempty"`
}
type ControlOperation struct {
	Version        int            `json:"version"`
	ID             string         `json:"operation_id"`
	RequestDigest  string         `json:"request_digest"`
	Request        ControlRequest `json:"request"`
	Base           ControlActive  `json:"base"`
	TargetRevision uint64         `json:"target_revision"`
	Phase          string         `json:"phase"`
	Step           string         `json:"step"`
	Target         *ControlLaunch `json:"target,omitempty"`
	Rollback       *ControlLaunch `json:"rollback,omitempty"`
	Problem        string         `json:"problem,omitempty"`
}
type ControlStatus struct {
	Active      ControlActive      `json:"active"`
	Observation ControlObservation `json:"observation"`
	Pending     []ControlOperation `json:"pending"`
	Poisoned    bool               `json:"poisoned"`
}
type ControlProbe struct {
	mu               sync.Mutex
	store            *controlStore
	backend          ControlBackend
	active           ControlActive
	operations       map[string]ControlOperation
	closed, poisoned bool
	hook             func(string) error // tests explicitly install crash boundaries; nil in normal use
}

var controlToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var controlHex = regexp.MustCompile(`^[0-9a-f]{64}$`)
var controlBootID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var controlTicks = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var controlNonce = regexp.MustCompile(`^[0-9a-f]{32}$`)

func controlClone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}
func controlDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// DecodeControlRequest rejects duplicate keys, unknown fields and trailing values.
func DecodeControlRequest(raw []byte) (ControlRequest, error) {
	var r ControlRequest
	if len(raw) > 256<<10 {
		return r, errors.New("control request too large")
	}
	if e := controlDecode(raw, &r); e != nil {
		return r, e
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if r.Action == "stop" {
		if _, ok := fields["bundle"]; ok {
			return r, errors.New("stop forbids bundle field")
		}
	}
	return r, controlValidateRequest(r)
}
func controlDecode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func() error
	value = func() error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		switch x := t.(type) {
		case json.Delim:
			switch x {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return errors.New("duplicate JSON field")
					}
					seen[s] = true
					if e = value(); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					return errors.New("invalid object")
				}
			case '[':
				for d.More() {
					if e = value(); e != nil {
						return e
					}
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					return errors.New("invalid array")
				}
			default:
				return errors.New("invalid delimiter")
			}
		}
		return nil
	}
	if e := value(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("trailing JSON")
	}
	if e := controlRequiredFields(raw, reflect.TypeOf(out).Elem()); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

// Persisted records and requests require every non-optional declared field.
func controlRequiredFields(raw []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		// []byte is encoded as base64, not an array of JSON objects.
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := controlRequiredFields(entry, t.Elem()); err != nil {
				return err
			}
		}
		return nil
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("null control object")
	}
	declared := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		declared[name] = true
	}
	for name := range fields {
		// encoding/json also matches struct fields case-insensitively. Reject
		// those aliases before decoding so two different keys cannot overwrite
		// the same typed field or bypass the unknown-key check.
		if !declared[name] {
			return fmt.Errorf("unknown control field %s", name)
		}
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, _, optional := strings.Cut(tag, ",")
		v, present := fields[name]
		if !optional && (!present || bytes.Equal(bytes.TrimSpace(v), []byte("null"))) {
			return fmt.Errorf("missing control field %s", name)
		}
		if present && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			if err := controlRequiredFields(v, f.Type); err != nil {
				return err
			}
		}
	}
	return nil
}

func controlValidateBundle(b ControlBundle) error {
	if len(b.CorePath) > 4096 || strings.ContainsRune(b.CorePath, 0) || len(b.Config)+len(b.RouteMap) > 8<<10 || !filepath.IsAbs(b.CorePath) || filepath.Clean(b.CorePath) != b.CorePath || !controlHex.MatchString(b.CoreSHA256) || !controlHex.MatchString(b.ConfigSHA256) || len(b.Config) == 0 || len(b.RouteMap) == 0 {
		return errors.New("invalid control bundle")
	}
	h := sha256.Sum256(b.Config)
	if hex.EncodeToString(h[:]) != b.ConfigSHA256 {
		return errors.New("config digest mismatch")
	}
	var route any
	if e := controlDecode(b.RouteMap, &route); e != nil {
		return e
	}
	return nil
}
func controlValidateRequest(r ControlRequest) error {
	if r.Version != 1 || !controlToken.MatchString(r.RequestID) || !r.AcceptDisruption {
		return errors.New("invalid control request or disruption not accepted")
	}
	switch r.Action {
	case "apply":
		if r.Bundle == nil {
			return errors.New("apply requires bundle")
		}
		return controlValidateBundle(*r.Bundle)
	case "stop":
		if r.Bundle != nil {
			return errors.New("stop forbids bundle")
		}
	default:
		return errors.New("unknown control action")
	}
	return nil
}
func controlTerminal(p string) bool { return p == "committed" || p == "rolled_back" }
func (p *ControlProbe) usable() error {
	if p.closed {
		return ErrControlClosed
	}
	if p.poisoned {
		return ErrControlPoisoned
	}
	return nil
}
func (p *ControlProbe) boundary(point string) error {
	if p.hook != nil {
		if e := p.hook(point); e != nil {
			p.poisoned = true
			return e
		}
	}
	return nil
}
func (p *ControlProbe) saveOperation(o *ControlOperation) error {
	if e := p.store.write("operations/"+o.ID+".json", o, 256<<10); e != nil {
		p.poisoned = true
		return e
	}
	p.operations[o.ID] = controlClone(*o)
	return nil
}
func (p *ControlProbe) stage(o *ControlOperation, phase, step string) error {
	o.Phase = phase
	o.Step = step
	if e := p.saveOperation(o); e != nil {
		return e
	}
	return p.boundary(step)
}
func (p *ControlProbe) publish(a ControlActive) error {
	if e := p.boundary("before-active"); e != nil {
		return e
	}
	if e := p.store.write("active.json", a, 64<<10); e != nil {
		p.poisoned = true
		return e
	}
	p.active = controlClone(a)
	return p.boundary("after-active")
}
func (p *ControlProbe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	return p.store.close()
}
func (p *ControlProbe) Execute(ctx context.Context, r ControlRequest) (ControlOperation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.usable(); e != nil {
		return ControlOperation{}, e
	}
	r = controlClone(r)
	digest := controlDigest(r)
	if old, ok := p.operations[r.RequestID]; ok {
		if old.RequestDigest != digest {
			return old, ErrControlIDConflict
		}
		return controlClone(old), nil
	}
	if r.ExpectedRevision != p.active.Revision {
		return ControlOperation{}, ErrControlConflict
	}
	if e := controlValidateRequest(r); e != nil {
		return ControlOperation{}, e
	}
	for _, o := range p.operations {
		if !controlTerminal(o.Phase) {
			return ControlOperation{}, ErrControlPending
		}
	}
	if len(p.operations) >= 32 {
		return ControlOperation{}, errors.New("control operation limit")
	}
	if r.Action == "apply" {
		if e := p.backend.Validate(controlClone(*r.Bundle)); e != nil {
			return ControlOperation{}, e
		}
	}
	if p.active.Revision == ^uint64(0) {
		return ControlOperation{}, errors.New("revision exhausted")
	}
	o := ControlOperation{Version: 1, ID: r.RequestID, RequestDigest: digest, Request: r, Base: controlClone(p.active), TargetRevision: p.active.Revision + 1, Phase: "applying", Step: "recorded"}
	if raw, _ := json.Marshal(o); len(raw) > 256<<10 {
		return o, errors.New("operation too large")
	}
	if e := p.boundary("before-operation"); e != nil {
		return o, e
	}
	if r.Bundle != nil {
		if e := p.store.bundle(*r.Bundle); e != nil {
			p.poisoned = true
			return o, e
		}
	}
	if e := p.saveOperation(&o); e != nil {
		return o, e
	}
	if e := p.boundary("recorded"); e != nil {
		return o, e
	}
	e := p.advance(ctx, &o)
	return controlClone(o), e
}
func (p *ControlProbe) Recover(ctx context.Context, id string, revision uint64) (ControlOperation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.usable(); e != nil {
		return ControlOperation{}, e
	}
	if revision != p.active.Revision {
		return ControlOperation{}, ErrControlConflict
	}
	o, ok := p.operations[id]
	if !ok {
		return o, errors.New("unknown control operation")
	}
	if controlTerminal(o.Phase) {
		return controlClone(o), nil
	}
	e := p.advance(ctx, &o)
	return controlClone(o), e
}
func (p *ControlProbe) Status(ctx context.Context) (ControlStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ControlStatus{}, ErrControlClosed
	}
	s := ControlStatus{Active: controlClone(p.active), Poisoned: p.poisoned, Observation: ControlObservation{State: ControlVerifiedStopped}, Pending: []ControlOperation{}}
	for _, o := range p.operations {
		if !controlTerminal(o.Phase) {
			s.Pending = append(s.Pending, controlClone(o))
		}
	}
	if p.active.Launch != nil {
		var e error
		s.Observation, e = p.observe(ctx, *p.active.Launch)
		if e != nil {
			return s, e
		}
	}
	return s, nil
}
func controlMatches(l ControlLaunch, i *ControlInstance) bool {
	if i == nil || i.PID < 1 || i.PID > 2147483647 || !controlBootID.MatchString(i.BootID) || !controlTicks.MatchString(i.StartTicks) {
		return false
	}
	if _, err := strconv.ParseUint(i.StartTicks, 10, 64); err != nil {
		return false
	}
	return i.Nonce == l.Nonce && i.Generation == l.Generation && i.BundleDigest == l.BundleDigest && (l.Instance == nil || *i == *l.Instance)
}

func (p *ControlProbe) observe(ctx context.Context, l ControlLaunch) (ControlObservation, error) {
	v, e := p.backend.Observe(ctx, controlClone(l))
	if e != nil {
		return ControlObservation{State: ControlUnknown}, e
	}
	switch v.State {
	case ControlVerifiedRunning:
		if !controlMatches(l, v.Instance) {
			return ControlObservation{State: ControlUnknown}, ErrControlUnknown
		}
	case ControlVerifiedStopped:
	case ControlUnknown:
	default:
		return ControlObservation{State: ControlUnknown}, ErrControlUnknown
	}
	return controlClone(v), nil
}
func (p *ControlProbe) degrade(o *ControlOperation, e error) error {
	o.Phase = "degraded"
	o.Problem = e.Error()
	if err := p.saveOperation(o); err != nil {
		return err
	}
	return e
}
func (p *ControlProbe) ensureStopped(ctx context.Context, l ControlLaunch) error {
	v, e := p.observe(ctx, l)
	if e != nil {
		return e
	}
	switch v.State {
	case ControlVerifiedStopped:
		return nil
	case ControlVerifiedRunning:
		l.Instance = v.Instance
		if e = p.backend.Stop(ctx, controlClone(l)); e != nil {
			return e
		}
		v, e = p.observe(ctx, l)
		if e == nil && v.State == ControlVerifiedStopped {
			return nil
		}
		if e != nil {
			return e
		}
	}
	return ErrControlUnknown
}
func (p *ControlProbe) newLaunch(o *ControlOperation, b ControlBundle, rollback bool) (*ControlLaunch, error) {
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return nil, e
	}
	n := hex.EncodeToString(nonce[:])
	l := &ControlLaunch{Nonce: n, Generation: "gen:control-" + n, BundleDigest: controlDigest(b), Directory: filepath.Join(p.store.directory, "launches", n), Bundle: controlClone(b)}
	if rollback {
		o.Rollback = l
	} else {
		o.Target = l
	}
	if e := p.store.write("launches/"+n+".json", l, 256<<10); e != nil {
		p.poisoned = true
		return nil, e
	}
	if e := p.saveOperation(o); e != nil {
		return nil, e
	}
	if e := p.boundary("launch-intent"); e != nil {
		return nil, e
	}
	if e := p.store.launchDirectory(n); e != nil {
		p.poisoned = true
		return nil, e
	}
	return l, nil
}
func (p *ControlProbe) launch(ctx context.Context, o *ControlOperation, b ControlBundle, rollback bool) (ControlObservation, error) {
	l := o.Target
	if rollback {
		l = o.Rollback
	}
	fresh := l == nil
	if fresh {
		var e error
		l, e = p.newLaunch(o, b, rollback)
		if e != nil {
			return ControlObservation{}, e
		}
		i, e := p.backend.Start(ctx, controlClone(*l))
		if e == nil {
			if !controlMatches(*l, &i) {
				return ControlObservation{State: ControlUnknown}, ErrControlUnknown
			}
			if fault := p.boundary("spawn-returned-before-record"); fault != nil {
				return ControlObservation{}, fault
			}
			l.Instance = &i
			if e = p.saveOperation(o); e != nil {
				return ControlObservation{}, e
			}
		}
		if e = p.boundary("spawn-returned"); e != nil {
			return ControlObservation{}, e
		}
	}
	return p.observe(ctx, *l)
}
func (p *ControlProbe) advance(ctx context.Context, o *ControlOperation) error {
	// A published active record is authoritative only after re-observing the exact target.
	if o.Phase == "rolling_back" || o.Rollback != nil || o.Step == "rolling-back" || o.Step == "rollback-verified" || o.Step == "rollback-publishing" {
		return p.rollback(ctx, o)
	}
	if o.Request.Action == "stop" {
		if e := p.stage(o, "applying", "stop-old"); e != nil {
			return e
		}
		if o.Base.Launch != nil {
			if e := p.ensureStopped(ctx, *o.Base.Launch); e != nil {
				return p.degrade(o, e)
			}
		}
		if e := p.stage(o, "applying", "old-stopped"); e != nil {
			return e
		}
		if e := p.stage(o, "publishing", "publishing"); e != nil {
			return e
		}
		if e := p.publish(ControlActive{Version: 1, Revision: o.TargetRevision, Desired: "stopped"}); e != nil {
			return e
		}
		return p.stage(o, "committed", "committed")
	}
	if o.Target == nil {
		if e := p.stage(o, "applying", "stop-old"); e != nil {
			return e
		}
		if o.Base.Launch != nil {
			if e := p.ensureStopped(ctx, *o.Base.Launch); e != nil {
				return p.degrade(o, e)
			}
		}
		if e := p.stage(o, "applying", "old-stopped"); e != nil {
			return e
		}
		if e := p.stage(o, "applying", "new-spawn"); e != nil {
			return e
		}
	}
	if o.Target != nil && o.Base.Launch != nil {
		old, err := p.observe(ctx, *o.Base.Launch)
		if err != nil || old.State != ControlVerifiedStopped {
			if err == nil {
				err = ErrControlUnknown
			}
			return p.degrade(o, err)
		}
	}
	v, e := p.launch(ctx, o, *o.Request.Bundle, false)
	if p.poisoned {
		return e
	}
	if e != nil || v.State == ControlUnknown {
		if e == nil {
			e = ErrControlUnknown
		}
		return p.degrade(o, e)
	}
	if v.State == ControlVerifiedStopped {
		if e = p.stage(o, "rolling_back", "rolling-back"); e != nil {
			return e
		}
		return p.rollback(ctx, o)
	}
	o.Target.Instance = v.Instance
	if e = p.stage(o, "verifying", "new-verified"); e != nil {
		return e
	}
	if e = p.stage(o, "publishing", "publishing"); e != nil {
		return e
	}
	b := controlClone(*o.Request.Bundle)
	if e = p.publish(ControlActive{Version: 1, Revision: o.TargetRevision, Desired: "running", Bundle: &b, Launch: controlClone(o.Target)}); e != nil {
		return e
	}
	if e = p.boundary("before-committed"); e != nil {
		return e
	}
	return p.stage(o, "committed", "committed")
}
func (p *ControlProbe) rollback(ctx context.Context, o *ControlOperation) error {
	if o.Target != nil {
		if e := p.ensureStopped(ctx, *o.Target); e != nil {
			return p.degrade(o, e)
		}
	}
	a := controlClone(o.Base)
	if a.Desired == "running" {
		v, e := p.launch(ctx, o, *a.Bundle, true)
		if p.poisoned {
			return e
		}
		if e != nil || v.State != ControlVerifiedRunning {
			if e == nil {
				e = ErrControlUnknown
			}
			return p.degrade(o, e)
		}
		o.Rollback.Instance = v.Instance
		if e = p.stage(o, "rolling_back", "rollback-verified"); e != nil {
			return e
		}
		a.Launch = controlClone(o.Rollback)
	}
	if e := p.stage(o, "rolling_back", "rollback-publishing"); e != nil {
		return e
	}
	if e := p.publish(a); e != nil {
		return e
	}
	return p.stage(o, "rolled_back", "rolled-back")
}

func controlValidateLaunch(l ControlLaunch, directory string) error {
	if len(l.Directory) > 4096 || !controlNonce.MatchString(l.Nonce) || l.Generation != "gen:control-"+l.Nonce || l.BundleDigest != controlDigest(l.Bundle) || l.Directory != filepath.Join(directory, "launches", l.Nonce) {
		return fmt.Errorf("invalid launch identity")
	}
	if e := controlValidateBundle(l.Bundle); e != nil {
		return e
	}
	if l.Instance != nil && !controlMatches(l, l.Instance) {
		return ErrControlUnknown
	}
	return nil
}
