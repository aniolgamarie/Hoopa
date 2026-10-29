package config

import (
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"hoopa/internal/document"
)

var ErrIntent = errors.New("INTENT_INVALID")

type IntentMode uint8

const (
	IntentDraft IntentMode = iota
	IntentApply
)

type Intent struct {
	SchemaVersion uint64          `json:"schema_version"`
	Core          IntentCore      `json:"core"`
	Subscriptions []Subscription  `json:"subscriptions"`
	Selection     IntentSelection `json:"selection"`
	Groups        []Group         `json:"groups"`
	Apps          []App           `json:"apps"`
	Rules         []Rule          `json:"rules"`
	DefaultTarget Target          `json:"default_target"`
	Retention     Retention       `json:"retention"`
}
type IntentCore struct {
	Mihomo BinaryReference  `json:"mihomo"`
	Gost   *BinaryReference `json:"gost,omitempty"`
}
type IntentSelection struct {
	ActiveSubscription *ID `json:"active_subscription"`
}
type SubscriptionSource struct {
	Kind         string `json:"kind"`
	Path         string `json:"path,omitempty"`
	URLSecretRef string `json:"url_secret_ref,omitempty"`
}
type Subscription struct {
	ID          ID                 `json:"id"`
	Source      SubscriptionSource `json:"source"`
	UpdateHours *uint64            `json:"update_hours"`
}
type GroupSource struct {
	Kind           string `json:"kind"`
	SubscriptionID *ID    `json:"subscription_id,omitempty"`
}
type Probe struct {
	URL             string `json:"url"`
	IntervalSeconds uint64 `json:"interval_seconds"`
	TimeoutMS       uint64 `json:"timeout_ms"`
	ToleranceMS     uint64 `json:"tolerance_ms"`
}
type Group struct {
	ID             ID           `json:"id"`
	Required       bool         `json:"required"`
	Region         *string      `json:"region,omitempty"`
	Strategy       string       `json:"strategy"`
	Source         *GroupSource `json:"source,omitempty"`
	SelectedNodeID *ID          `json:"selected_node_id,omitempty"`
	Probe          *Probe       `json:"probe,omitempty"`
	Follows        *ID          `json:"follows,omitempty"`
}
type App struct {
	ID       ID       `json:"id"`
	Mode     string   `json:"mode"`
	Metering bool     `json:"metering"`
	Bypass   []string `json:"bypass"`
}
type Target struct {
	Kind           string `json:"kind"`
	GroupID        *ID    `json:"group_id,omitempty"`
	SubscriptionID *ID    `json:"subscription_id,omitempty"`
	NodeID         *ID    `json:"node_id,omitempty"`
}
type Predicate struct {
	Kind              string      `json:"kind"`
	AppID             *ID         `json:"app_id,omitempty"`
	ProcessInstanceID *ID         `json:"process_instance_id,omitempty"`
	Value             string      `json:"value,omitempty"`
	Operator          string      `json:"operator,omitempty"`
	Conditions        []Predicate `json:"conditions,omitempty"`
}
type Rule struct {
	ID     ID        `json:"id"`
	Match  Predicate `json:"match"`
	Target Target    `json:"target"`
}
type Retention struct {
	Enabled bool   `json:"enabled"`
	Days    uint64 `json:"days"`
}

// ParseIntent validates a complete v1 shape. Apply additionally resolves references
// available in the document; provider contents and binary capabilities belong to
// subsequent compilation. It never reads paths, probes URLs or starts processes.
func ParseIntent(raw []byte, mode IntentMode) (Intent, error) {
	if mode != IntentDraft && mode != IntentApply {
		return Intent{}, ErrIntent
	}
	value, err := document.Decode(raw, document.Limits{})
	if err != nil {
		return Intent{}, ErrIntent
	}
	p := intentParser{apply: mode == IntentApply}
	m := p.object(value, "schema_version core subscriptions selection groups apps rules default_target", "retention")
	out := Intent{SchemaVersion: p.integer(m["schema_version"], 1, 1), Retention: Retention{Days: 30}}
	core := p.object(m["core"], "mihomo", "gost")
	out.Core.Mihomo = p.binary(core["mihomo"])
	if v, ok := core["gost"]; ok {
		b := p.binary(v)
		out.Core.Gost = &b
	}
	for _, v := range p.array(m["subscriptions"]) {
		s := p.object(v, "id source update_hours", "")
		sub := Subscription{ID: p.id(s["id"]), Source: p.source(s["source"])}
		if s["update_hours"] != nil {
			n := p.integer(s["update_hours"], 1, uint64(math.MaxInt64/int64(time.Hour)))
			sub.UpdateHours = &n
		}
		out.Subscriptions = append(out.Subscriptions, sub)
	}
	sel := p.object(m["selection"], "active_subscription", "")
	if sel["active_subscription"] != nil {
		id := p.id(sel["active_subscription"])
		out.Selection.ActiveSubscription = &id
	}
	for _, v := range p.array(m["groups"]) {
		out.Groups = append(out.Groups, p.group(v))
	}
	for _, v := range p.array(m["apps"]) {
		a := p.object(v, "id mode metering bypass", "")
		app := App{ID: p.id(a["id"]), Mode: p.enum(a["mode"], "managed", "inherit", "direct"), Metering: p.boolean(a["metering"]), Bypass: []string{}}
		for _, v := range p.array(a["bypass"]) {
			app.Bypass = append(app.Bypass, p.text(v, false))
		}
		if app.Metering && app.Mode != "managed" {
			p.bad = true
		}
		out.Apps = append(out.Apps, app)
	}
	for _, v := range p.array(m["rules"]) {
		r := p.object(v, "id match target", "")
		out.Rules = append(out.Rules, Rule{ID: p.id(r["id"]), Match: p.predicate(r["match"], 1), Target: p.target(r["target"])})
	}
	out.DefaultTarget = p.target(m["default_target"])
	if v, ok := m["retention"]; ok {
		r := p.object(v, "enabled days", "")
		out.Retention = Retention{Enabled: p.boolean(r["enabled"]), Days: p.integer(r["days"], 1, maxIntentInteger)}
	}
	p.references(out)
	if p.bad {
		return Intent{}, ErrIntent
	}
	// Preserve arrays as arrays when clients serialize an empty document.
	if out.Subscriptions == nil {
		out.Subscriptions = []Subscription{}
	}
	if out.Groups == nil {
		out.Groups = []Group{}
	}
	if out.Apps == nil {
		out.Apps = []App{}
	}
	if out.Rules == nil {
		out.Rules = []Rule{}
	}
	return out, nil
}

const maxIntentInteger uint64 = 9007199254740991

type intentParser struct{ bad, apply bool }

func (p *intentParser) object(v any, required, optional string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		p.bad = true
		return map[string]any{}
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		allowed[k] = true
		if _, ok := m[k]; !ok {
			p.bad = true
		}
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			p.bad = true
		}
	}
	return m
}
func (p *intentParser) array(v any) []any {
	a, ok := v.([]any)
	if !ok {
		p.bad = true
	}
	return a
}
func (p *intentParser) text(v any, nonempty bool) string {
	s, ok := v.(string)
	if !ok || nonempty && len(s) == 0 {
		p.bad = true
	}
	return s
}
func (p *intentParser) boolean(v any) bool {
	b, ok := v.(bool)
	if !ok {
		p.bad = true
	}
	return b
}
func (p *intentParser) enum(v any, values ...string) string {
	s := p.text(v, true)
	for _, value := range values {
		if s == value {
			return s
		}
	}
	p.bad = true
	return s
}
func (p *intentParser) id(v any) ID {
	s := p.text(v, true)
	id, err := ParseID(s)
	if err != nil {
		p.bad = true
	}
	return id
}
func (p *intentParser) idptr(v any) *ID { id := p.id(v); return &id }

// integer recognizes the mathematical integer value of decimal/exponent forms
// without float64 rounding or an attacker-controlled big integer allocation.
func (p *intentParser) integer(v any, min, max uint64) uint64 {
	n, ok := v.(json.Number)
	if !ok {
		p.bad = true
		return 0
	}
	s := n.String()
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	mantissa, expText, hasExp := strings.Cut(strings.ToLower(s), "e")
	decimal := strings.IndexByte(mantissa, '.')
	fraction := 0
	if decimal >= 0 {
		fraction = len(mantissa) - decimal - 1
		mantissa = mantissa[:decimal] + mantissa[decimal+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		if min > 0 {
			p.bad = true
		}
		return 0
	}
	if negative {
		p.bad = true
		return 0
	}
	exponent := int64(0)
	if hasExp {
		var err error
		exponent, err = strconv.ParseInt(expText, 10, 32)
		if err != nil {
			p.bad = true
			return 0
		}
	}
	scale := exponent - int64(fraction)
	for strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale++
	}
	if scale < 0 || scale > 16 || int64(len(digits))+scale > 16 {
		p.bad = true
		return 0
	}
	digits += strings.Repeat("0", int(scale))
	value, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || value < min || value > max || value > maxIntentInteger {
		p.bad = true
		return 0
	}
	return value
}

var intentHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var intentScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

func localIntentPath(s string) bool {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\r\n") || strings.Contains(s, "://") {
		return false
	}
	return !intentScheme.MatchString(s)
}
func (p *intentParser) binary(v any) BinaryReference {
	m := p.object(v, "path version sha256", "")
	b := BinaryReference{Path: p.text(m["path"], true), Version: p.text(m["version"], true), SHA256: p.text(m["sha256"], true)}
	if !localIntentPath(b.Path) || strings.TrimSpace(b.Version) == "" || strings.EqualFold(strings.TrimSpace(b.Version), "latest") || !intentHash.MatchString(b.SHA256) {
		p.bad = true
	}
	return b
}
func (p *intentParser) source(v any) SubscriptionSource {
	m, _ := v.(map[string]any)
	kind := p.enum(m["kind"], "https", "file")
	out := SubscriptionSource{Kind: kind}
	if kind == "https" {
		m = p.object(v, "kind url_secret_ref", "")
		out.URLSecretRef = p.text(m["url_secret_ref"], true)
		if !localIntentPath(out.URLSecretRef) {
			p.bad = true
		}
	} else {
		m = p.object(v, "kind path", "")
		out.Path = p.text(m["path"], true)
		if !localIntentPath(out.Path) {
			p.bad = true
		}
	}
	return out
}
func (p *intentParser) group(v any) Group {
	m, _ := v.(map[string]any)
	strategy := p.enum(m["strategy"], "manual", "auto", "follow")
	required := "id required strategy "
	switch strategy {
	case "manual":
		required += "source selected_node_id"
	case "auto":
		required += "source probe"
	case "follow":
		required += "follows"
	}
	m = p.object(v, required, "region")
	g := Group{ID: p.id(m["id"]), Required: p.boolean(m["required"]), Strategy: strategy}
	if v, ok := m["region"]; ok && v != nil {
		r := p.text(v, true)
		g.Region = &r
	}
	if strategy == "follow" {
		g.Follows = p.idptr(m["follows"])
		return g
	}
	src, _ := m["source"].(map[string]any)
	kind := p.enum(src["kind"], "active", "subscription")
	g.Source = &GroupSource{Kind: kind}
	if kind == "active" {
		p.object(m["source"], "kind", "")
	} else {
		src = p.object(m["source"], "kind subscription_id", "")
		g.Source.SubscriptionID = p.idptr(src["subscription_id"])
	}
	if strategy == "manual" {
		g.SelectedNodeID = p.idptr(m["selected_node_id"])
	}
	if strategy == "auto" {
		m = p.object(m["probe"], "url interval_seconds timeout_ms tolerance_ms", "")
		g.Probe = &Probe{URL: p.text(m["url"], true), IntervalSeconds: p.integer(m["interval_seconds"], 1, uint64(math.MaxInt64/int64(time.Second))), TimeoutMS: p.integer(m["timeout_ms"], 1, uint64(math.MaxInt64/int64(time.Millisecond))), ToleranceMS: p.integer(m["tolerance_ms"], 0, uint64(math.MaxInt64/int64(time.Millisecond)))}
		if p.apply && !validProbeURL(g.Probe.URL) {
			p.bad = true
		}
	}
	return g
}
func validProbeURL(s string) bool {
	// URL parsing alone accepts bracketed non-IP hosts and out-of-range ports.
	// Validate the authority without DNS lookup or any other network operation.
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	if strings.Contains(s, "#") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Opaque != "" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	addr, ipErr := netip.ParseAddr(host)
	bracketed := strings.HasPrefix(u.Host, "[")
	if bracketed {
		if ipErr != nil || !addr.Is6() || addr.Zone() != "" {
			return false
		}
	} else {
		if strings.Contains(host, ":") {
			return false
		}
		if ipErr != nil {
			dnsHost := strings.TrimSuffix(host, ".")
			if strings.HasSuffix(dnsHost, ".") || !validIntentDomain(dnsHost) {
				return false
			}
			// Numeric dotted hosts must be valid IPv4, rather than an invalid IP
			// interpreted differently by the URL consumer.
			numericDotted := strings.Contains(host, ".")
			for _, c := range host {
				if c != '.' && (c < '0' || c > '9') {
					numericDotted = false
					break
				}
			}
			if numericDotted {
				return false
			}
		}
	}
	// A colon after the host declares a port, including an empty one.
	authority := u.Host
	if bracketed {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return false
		}
		authority = authority[end+1:]
	}
	if strings.Contains(authority, ":") {
		port, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || port == 0 {
			return false
		}
	}
	return true
}
func (p *intentParser) target(v any) Target {
	m, _ := v.(map[string]any)
	kind := p.enum(m["kind"], "group", "node", "direct", "reject")
	required := "kind"
	switch kind {
	case "group":
		required += " group_id"
	case "node":
		required += " subscription_id node_id"
	}
	m = p.object(v, required, "")
	t := Target{Kind: kind}
	if kind == "group" {
		t.GroupID = p.idptr(m["group_id"])
	}
	if kind == "node" {
		t.SubscriptionID = p.idptr(m["subscription_id"])
		t.NodeID = p.idptr(m["node_id"])
	}
	return t
}
func (p *intentParser) predicate(v any, depth int) Predicate {
	if depth > 16 {
		p.bad = true
		return Predicate{}
	}
	m, _ := v.(map[string]any)
	kind := p.enum(m["kind"], "app", "process_instance", "process_name", "process_path", "ip_cidr", "domain", "all")
	required := "kind "
	switch kind {
	case "app":
		required += "app_id"
	case "process_instance":
		required += "process_instance_id"
	case "domain":
		required += "operator value"
	case "all":
		required += "conditions"
	default:
		required += "value"
	}
	m = p.object(v, required, "")
	out := Predicate{Kind: kind}
	switch kind {
	case "app":
		out.AppID = p.idptr(m["app_id"])
	case "process_instance":
		out.ProcessInstanceID = p.idptr(m["process_instance_id"])
	case "all":
		conditions := p.array(m["conditions"])
		if len(conditions) == 0 {
			p.bad = true
		}
		for _, c := range conditions {
			out.Conditions = append(out.Conditions, p.predicate(c, depth+1))
		}
	default:
		out.Value = p.text(m["value"], true)
		if kind == "domain" {
			out.Operator = p.enum(m["operator"], "exact", "suffix")
			if p.apply && !validIntentDomain(out.Value) {
				p.bad = true
			}
		}
		if kind == "ip_cidr" && p.apply {
			if _, err := netip.ParsePrefix(out.Value); err != nil {
				p.bad = true
			}
		}
	}
	return out
}
func validIntentDomain(s string) bool {
	if len(s) > 253 {
		return false
	}
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (p *intentParser) references(in Intent) {
	sources, groups, apps, rules := map[ID]bool{}, map[ID]bool{}, map[ID]bool{}, map[ID]bool{}
	add := func(m map[ID]bool, id ID) {
		if m[id] {
			p.bad = true
		}
		m[id] = true
	}
	for _, s := range in.Subscriptions {
		add(sources, s.ID)
	}
	for _, g := range in.Groups {
		add(groups, g.ID)
	}
	for _, a := range in.Apps {
		add(apps, a.ID)
	}
	for _, r := range in.Rules {
		add(rules, r.ID)
	}
	if !p.apply {
		return
	}
	if in.Selection.ActiveSubscription != nil && !sources[*in.Selection.ActiveSubscription] {
		p.bad = true
	}
	follows := map[ID]ID{}
	for _, g := range in.Groups {
		if g.Source != nil {
			switch g.Source.Kind {
			case "active":
				if in.Selection.ActiveSubscription == nil {
					p.bad = true
				}
			case "subscription":
				if g.Source.SubscriptionID == nil || !sources[*g.Source.SubscriptionID] {
					p.bad = true
				}
			}
		}
		if g.Follows != nil {
			if !groups[*g.Follows] {
				p.bad = true
			}
			follows[g.ID] = *g.Follows
		}
	}
	// Iterative three-colour traversal keeps a long follow chain off the stack.
	done := map[ID]bool{}
	for id := range follows {
		path := map[ID]bool{}
		chain := []ID{}
		for !done[id] {
			if path[id] {
				p.bad = true
				break
			}
			path[id] = true
			chain = append(chain, id)
			next, ok := follows[id]
			if !ok {
				break
			}
			id = next
		}
		for _, v := range chain {
			done[v] = true
		}
	}
	target := func(t Target) {
		if t.GroupID != nil && !groups[*t.GroupID] {
			p.bad = true
		}
		if t.SubscriptionID != nil && !sources[*t.SubscriptionID] {
			p.bad = true
		}
	}
	var predicate func(Predicate)
	predicate = func(v Predicate) {
		if v.AppID != nil && !apps[*v.AppID] {
			p.bad = true
		}
		for _, c := range v.Conditions {
			predicate(c)
		}
	}
	target(in.DefaultTarget)
	for _, r := range in.Rules {
		target(r.Target)
		predicate(r.Match)
	}
}
