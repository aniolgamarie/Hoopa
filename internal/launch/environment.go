//go:build linux

// Package launch prepares child-only access inputs. Environment construction
// neither qualifies an installed entry nor launches/probes any process.
package launch

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"hoopa/internal/config"
)

var (
	ErrEnvironment         = errors.New("ENVIRONMENT_INVALID")
	ErrMeteringMode        = errors.New("METERING_MODE_INVALID")
	ErrMeteringUnsupported = errors.New("METERING_UNSUPPORTED")
)

// EnvironmentChange is the safe preview: it never carries a variable's value.
type EnvironmentChange struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// Environment is immutable to its caller. A valid empty environment is distinct
// from the unusable zero value; sensitive contents require an explicit getter.
type Environment struct{ data *environmentData }

// Keeping sensitive storage behind an immutable pointer is deliberate: fmt
// bypasses Formatter for some invalid verbs (for example %p on a value), then
// reflects the top-level struct. That fallback must see no sensitive slices.
type environmentData struct {
	values  []string
	changes []EnvironmentChange
}

func (Environment) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("launch.Environment{private}"))
}

func (Environment) String() string               { return "launch.Environment{private}" }
func (Environment) GoString() string             { return "launch.Environment{private}" }
func (Environment) MarshalJSON() ([]byte, error) { return []byte(`"launch.Environment{private}"`), nil }
func (e Environment) SensitiveValues() ([]string, error) {
	if e.data == nil {
		return nil, ErrEnvironment
	}
	return append([]string{}, e.data.values...), nil
}
func (e Environment) Changes() []EnvironmentChange {
	if e.data == nil {
		return []EnvironmentChange{}
	}
	return append([]EnvironmentChange{}, e.data.changes...)
}

// BuildEnvironment never touches the actual parent environment. The immutable
// binding's codec establishes shape only; the caller separately qualifies its
// committed identity and current entry availability before a managed launch.
func BuildEnvironment(parent []string, app config.App, binding *config.AppBinding) (Environment, error) {
	if _, err := config.ParseID(string(app.ID)); err != nil {
		return Environment{}, ErrEnvironment
	}
	switch app.Mode {
	case "managed", "direct", "inherit":
	default:
		return Environment{}, ErrEnvironment
	}
	if app.Metering {
		if app.Mode != "managed" {
			return Environment{}, ErrMeteringMode
		}
		return Environment{}, ErrMeteringUnsupported
	}
	for _, value := range parent {
		if strings.ContainsRune(value, 0) {
			return Environment{}, ErrEnvironment
		}
	}
	if app.Mode == "inherit" {
		return Environment{data: &environmentData{values: slices.Clone(parent)}}, nil
	}
	var httpProxy, socksProxy, bypass string
	if app.Mode == "managed" {
		if binding == nil || binding.AppID() != app.ID {
			return Environment{}, ErrEnvironment
		}
		entry, err := binding.SensitiveEntry()
		if err != nil || len(entry.Users) != 1 {
			return Environment{}, ErrEnvironment
		}
		if !environmentBypass(app.Bypass) {
			return Environment{}, ErrEnvironment
		}
		user := entry.Users[0]
		endpoint := net.JoinHostPort(entry.Listen, strconv.Itoa(int(entry.Port)))
		proxy := url.URL{Scheme: "http", Host: endpoint, User: url.UserPassword(user.Username, user.Password)}
		httpProxy = proxy.String()
		proxy.Scheme = "socks5h"
		socksProxy = proxy.String()
		bypass = strings.Join(app.Bypass, ",")
	}
	values := make([]string, 0, len(parent))
	changes := []EnvironmentChange{}
	for _, value := range parent {
		name, _, _ := strings.Cut(value, "=")
		if environmentProxyName(name) {
			changes = append(changes, EnvironmentChange{Name: strings.Clone(name), Action: "remove"})
		} else {
			values = append(values, value)
		}
	}
	if app.Mode == "managed" {
		for _, pair := range []struct{ name, value string }{{"http_proxy", httpProxy}, {"HTTP_PROXY", httpProxy}, {"https_proxy", httpProxy}, {"HTTPS_PROXY", httpProxy}, {"all_proxy", socksProxy}, {"ALL_PROXY", socksProxy}, {"no_proxy", bypass}, {"NO_PROXY", bypass}} {
			values = append(values, pair.name+"="+pair.value)
			changes = append(changes, EnvironmentChange{Name: pair.name, Action: "set"})
		}
	}
	return Environment{data: &environmentData{values: values, changes: changes}}, nil
}

// ASCII-only comparison is intentional: Unicode lookalikes are unrelated
// opaque parent keys and must not be folded into the finite clearing set.
func environmentProxyName(name string) bool {
	for _, candidate := range [...]string{"http_proxy", "https_proxy", "all_proxy", "ftp_proxy", "no_proxy"} {
		if len(name) != len(candidate) {
			continue
		}
		equal := true
		for i := range len(name) {
			c := name[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != candidate[i] {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}
func environmentBypass(tokens []string) bool {
	if len(tokens) > 256 {
		return false
	}
	total := 0
	if len(tokens) > 0 {
		total = len(tokens) - 1
	}
	for _, token := range tokens {
		if len(token) > (16<<10)-total {
			return false
		}
		total += len(token)
		if !environmentBypassToken(token) {
			return false
		}
	}
	return true
}
func environmentBypassToken(token string) bool {
	if token == "*" {
		return true
	}
	if addr, err := netip.ParseAddr(token); err == nil {
		return addr.Zone() == "" && addr.String() == token
	}
	if prefix, err := netip.ParsePrefix(token); err == nil {
		return prefix.Addr().Zone() == "" && prefix.String() == token
	}
	// A numeric dotted name not accepted by netip can still satisfy the DNS
	// grammar. No additional IPv4-lookalike heuristic or DNS lookup is used.
	host := strings.TrimPrefix(token, ".")
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
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
