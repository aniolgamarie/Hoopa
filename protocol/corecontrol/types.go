// Package corecontrol defines the bounded basic-v1 control wire, not process
// ownership, HTTP authentication, a state machine, or a configuration compiler.
package corecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"unicode/utf8"
)

const SchemaVersion = 1
const MaxJSONBytes = 64 << 10
const MaxPayloadBytes = 4096
const MaxBinaryVersionBytes = 128

// ErrInvalid never incorporates untrusted configuration or request contents.
var ErrInvalid = errors.New("invalid basic core control v1 message")

type Snapshot struct {
	SchemaVersion int     `json:"schema_version"`
	ConfigSHA256  string  `json:"config_sha256"`
	Controller    string  `json:"controller"`
	Entries       []Entry `json:"entries"`
	Proxies       []Proxy `json:"proxies"`
	Rules         []Rule  `json:"rules"`
}
type Entry struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Address    string `json:"address"`
	AuthSHA256 string `json:"auth_sha256"`
}
type Proxy struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Address     string   `json:"address"`
	DialerProxy string   `json:"dialer_proxy"`
	Members     []string `json:"members"`
	Selected    string   `json:"selected"`
}
type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Target  string `json:"target"`
}
type Status struct {
	SchemaVersion    int      `json:"schema_version"`
	RunID            string   `json:"run_id"`
	ConfigGeneration string   `json:"config_generation"`
	ConfigSHA256     string   `json:"config_sha256"`
	BinaryVersion    string   `json:"binary_version"`
	Mode             string   `json:"mode"`
	Capabilities     []string `json:"capabilities"`
	State            string   `json:"state"`
	SnapshotSHA256   string   `json:"snapshot_sha256"`
	Activation       *Result  `json:"activation,omitempty"`
	Drain            *Result  `json:"drain,omitempty"`
}
type Mutation struct {
	SchemaVersion      int    `json:"schema_version"`
	Operation          string `json:"operation"`
	OperationID        string `json:"operation_id"`
	RunID              string `json:"run_id"`
	ConfigGeneration   string `json:"config_generation"`
	ConfigSHA256       string `json:"config_sha256"`
	SnapshotSHA256     string `json:"snapshot_sha256"`
	RegistrationSHA256 string `json:"registration_sha256"`
}
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	OperationID   string `json:"operation_id"`
	RequestSHA256 string `json:"request_sha256"`
	RunID         string `json:"run_id"`
	Result        string `json:"result"`
}

// IDs use [A-Za-z0-9][A-Za-z0-9._:-]{0,127}, measured in ASCII bytes.
func validID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i, c := range []byte(value) {
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '.' && c != '_' && c != ':' && c != '-') {
			return false
		}
	}
	return true
}
func declared(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && validID(value) && validID(strings.TrimPrefix(value, prefix))
}
func digestShape(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}
func endpoint(value string) bool {
	address, err := netip.ParseAddrPort(value)
	return err == nil && address.Port() != 0 && address.Addr() == netip.MustParseAddr("127.0.0.1") && address.String() == value
}

// All free text is bounded UTF-8 without NUL. IDs, enum strings, addresses and
// digests have stricter shapes; rule payload is a projection, not reparsed AND.
func text(value string, min, max int) bool {
	return len(value) >= min && len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func encodedSize(value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxJSONBytes {
		return ErrInvalid
	}
	return nil
}
func hash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxJSONBytes {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s Snapshot) Validate() error {
	if s.SchemaVersion != SchemaVersion || !digestShape(s.ConfigSHA256) || !endpoint(s.Controller) || s.Entries == nil || len(s.Entries) < 1 || len(s.Entries) > 4 || s.Proxies == nil || len(s.Proxies) < 6 || len(s.Proxies) > 70 || s.Rules == nil || len(s.Rules) < 1 || len(s.Rules) > 64 {
		return ErrInvalid
	}
	entries := map[string]bool{}
	addresses := map[string]bool{s.Controller: true}
	for i, e := range s.Entries {
		if !validID(e.Name) || i > 0 && s.Entries[i-1].Name >= e.Name || e.Type != "mixed" || !endpoint(e.Address) || addresses[e.Address] || !digestShape(e.AuthSHA256) {
			return ErrInvalid
		}
		entries[e.Name] = true
		addresses[e.Address] = true
	}
	builtins := map[string]string{"DIRECT": "Direct", "REJECT": "Reject", "REJECT-DROP": "RejectDrop", "COMPATIBLE": "Compatible", "PASS": "Pass", "GLOBAL": "Selector"}
	proxies := map[string]Proxy{}
	custom := map[string]bool{}
	for i, p := range s.Proxies {
		if !validID(p.Name) || i > 0 && s.Proxies[i-1].Name >= p.Name || p.Members == nil {
			return ErrInvalid
		}
		if kind, ok := builtins[p.Name]; ok {
			if p.Type != kind {
				return ErrInvalid
			}
		} else {
			if !(declared(p.Name, "node:") && (p.Type == "Direct" || p.Type == "Http" || p.Type == "Socks5") || declared(p.Name, "group:") && p.Type == "Selector") {
				return ErrInvalid
			}
			custom[p.Name] = true
		}
		switch p.Type {
		case "Http", "Socks5":
			if !endpoint(p.Address) || p.DialerProxy != "" && !validID(p.DialerProxy) {
				return ErrInvalid
			}
		default:
			if p.Address != "" || p.DialerProxy != "" {
				return ErrInvalid
			}
		}
		if p.Type != "Selector" && (len(p.Members) != 0 || p.Selected != "") {
			return ErrInvalid
		}
		proxies[p.Name] = p
	}
	for name := range builtins {
		if _, ok := proxies[name]; !ok {
			return ErrInvalid
		}
	}
	if len(custom) > 64 {
		return ErrInvalid
	}
	edges := map[string][]string{}
	for _, p := range s.Proxies {
		if p.DialerProxy != "" {
			if !custom[p.DialerProxy] {
				return ErrInvalid
			}
			edges[p.Name] = append(edges[p.Name], p.DialerProxy)
		}
		if p.Type != "Selector" {
			continue
		}
		limit := 64
		if p.Name == "GLOBAL" {
			limit = 66
		}
		if len(p.Members) < 1 || len(p.Members) > limit {
			return ErrInvalid
		}
		seen := map[string]bool{}
		selected := false
		for _, member := range p.Members {
			allowed := custom[member] || p.Name == "GLOBAL" && (member == "DIRECT" || member == "REJECT")
			if !allowed || seen[member] {
				return ErrInvalid
			}
			seen[member] = true
			selected = selected || member == p.Selected
			edges[p.Name] = append(edges[p.Name], member)
		}
		if !selected {
			return ErrInvalid
		}
	}
	// At most 70 bounded nodes; no artificial depth cap beyond cycle rejection.
	visited := map[string]uint8{}
	var visit func(string) bool
	visit = func(name string) bool {
		if visited[name] == 1 {
			return false
		}
		if visited[name] == 2 {
			return true
		}
		visited[name] = 1
		for _, next := range edges[name] {
			if !visit(next) {
				return false
			}
		}
		visited[name] = 2
		return true
	}
	for name := range proxies {
		if !visit(name) {
			return ErrInvalid
		}
	}
	for i, r := range s.Rules {
		if !(r.Target == "REJECT" || custom[r.Target]) || !text(r.Payload, 0, MaxPayloadBytes) {
			return ErrInvalid
		}
		switch r.Type {
		case "Match":
			if r.Payload != "" || i != len(s.Rules)-1 {
				return ErrInvalid
			}
		case "Domain", "DomainSuffix", "IPCIDR", "AND":
			if r.Payload == "" {
				return ErrInvalid
			}
		case "InName":
			if !entries[r.Payload] {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	if s.Rules[len(s.Rules)-1].Type != "Match" {
		return ErrInvalid
	}
	return encodedSize(s)
}
func (s Snapshot) Digest() (string, error) {
	if s.Validate() != nil {
		return "", ErrInvalid
	}
	return hash(s)
}

func (s Status) Validate() error {
	if s.SchemaVersion != SchemaVersion || !validID(s.RunID) || !validID(s.ConfigGeneration) || !digestShape(s.ConfigSHA256) || !text(s.BinaryVersion, 1, MaxBinaryVersionBytes) || s.Mode != "basic" || !digestShape(s.SnapshotSHA256) {
		return ErrInvalid
	}
	expected := []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
	if len(s.Capabilities) != len(expected) {
		return ErrInvalid
	}
	for i, c := range s.Capabilities {
		if c != expected[i] {
			return ErrInvalid
		}
	}
	if s.State != "closed" && s.State != "open" && s.State != "draining" && s.State != "degraded" {
		return ErrInvalid
	}
	if s.Activation != nil {
		if s.Activation.Validate() != nil || s.Activation.RunID != s.RunID || s.Activation.Result != "opened" && s.Activation.Result != "failed" {
			return ErrInvalid
		}
	}
	if s.Drain != nil {
		if s.Drain.Validate() != nil || s.Drain.RunID != s.RunID || s.Drain.Result != "draining" && s.Drain.Result != "failed" {
			return ErrInvalid
		}
	}
	if s.Activation != nil && s.Drain != nil && s.Activation.OperationID == s.Drain.OperationID {
		return ErrInvalid
	}
	// Historical receipts must still describe a reachable single-RunID history.
	activation, drain := "", ""
	if s.Activation != nil {
		activation = s.Activation.Result
	}
	if s.Drain != nil {
		drain = s.Drain.Result
	}
	valid := false
	switch s.State {
	case "closed":
		valid = activation == "" && drain == ""
	case "open":
		valid = activation == "opened" && drain == ""
	case "draining":
		valid = (activation == "" || activation == "opened") && drain == "draining"
	case "degraded":
		valid = activation == "failed" && drain == "" || activation == "opened" && (drain == "" || drain == "failed") || activation == "" && drain == "failed"
	}
	if !valid {
		return ErrInvalid
	}
	return encodedSize(s)
}
func (m Mutation) Validate() error {
	if m.SchemaVersion != SchemaVersion || m.Operation != "activate" && m.Operation != "drain" || !validID(m.OperationID) || !validID(m.RunID) || !validID(m.ConfigGeneration) || !digestShape(m.ConfigSHA256) || !digestShape(m.SnapshotSHA256) || !digestShape(m.RegistrationSHA256) {
		return ErrInvalid
	}
	return encodedSize(m)
}
func (m Mutation) Digest() (string, error) {
	if m.Validate() != nil {
		return "", ErrInvalid
	}
	return hash(m)
}
func (r Result) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validID(r.OperationID) || !validID(r.RunID) || !digestShape(r.RequestSHA256) || r.Result != "opened" && r.Result != "draining" && r.Result != "failed" {
		return ErrInvalid
	}
	return encodedSize(r)
}
