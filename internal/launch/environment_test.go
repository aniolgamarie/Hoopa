//go:build linux

package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	profile "hoopa/protocol/sourceprofile"
)

const environmentPrivateUser = "Private.User_52-"
const environmentPrivatePassword = "PrivatePasswordForEnvironment52@/?#%[]+&=;"

func environmentBinding(t *testing.T, id config.ID) config.AppBinding {
	t.Helper()
	binding, err := config.NewAppBinding(id, strings.Repeat("a", 64), profile.Entry{Type: "mixed", Listen: "127.0.0.1", Port: 36401, Users: []profile.User{{Username: environmentPrivateUser, Password: environmentPrivatePassword}}})
	if err != nil {
		t.Fatal("valid immutable binding fixture", err)
	}
	return binding
}
func environmentValues(t *testing.T, value Environment) []string {
	t.Helper()
	got, err := value.SensitiveValues()
	if err != nil {
		t.Fatal("valid environment unavailable", err)
	}
	return got
}
func environmentPrivate(t *testing.T, value any) {
	t.Helper()
	forms := []string{fmt.Sprint(value), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%s", value), fmt.Sprintf("%q", value)}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal("redacted JSON unavailable", err)
	}
	forms = append(forms, string(raw))
	for _, form := range forms {
		for _, private := range []string{environmentPrivateUser, environmentPrivatePassword, "private-parent-value", "http://127.0.0.1:36401", "socks5h://", "old-http-secret"} {
			if strings.Contains(form, private) {
				t.Fatal("ordinary formatting exposed sensitive environment")
			}
		}
	}
}
func environmentFailure(t *testing.T, parent []string, app config.App, binding *config.AppBinding, want error) {
	t.Helper()
	before := append([]string(nil), parent...)
	actualBefore := os.Environ()
	got, err := BuildEnvironment(parent, app, binding)
	if !errors.Is(err, want) || err.Error() != want.Error() {
		t.Fatal("wrong constant error class", err, want)
	}
	if _, consumeErr := got.SensitiveValues(); !errors.Is(consumeErr, ErrEnvironment) {
		t.Fatal("failed build returned consumable environment")
	}
	if len(got.Changes()) != 0 {
		t.Fatal("failed build returned partial changes")
	}
	if !reflect.DeepEqual(before, parent) || !reflect.DeepEqual(actualBefore, os.Environ()) {
		t.Fatal("failure mutated parent")
	}
	environmentPrivate(t, got)
	environmentPrivate(t, &got)
	environmentPrivate(t, err)
}

func TestEnvironmentManagedRemovesEveryCaseDuplicateAndAppendsEightPairs(t *testing.T) {
	t.Setenv("HOOPA_T052_SENTINEL", "private-parent-value")
	actualBefore := os.Environ()
	parent := []string{"KEEP=private-parent-value", "http_proxy=old-http-secret", "HtTp_PrOxY=other", "opaque-no-equals", "HTTPS_PROXY=old-https", "aLl_PrOxY=old-socks", "Ftp_PrOxY=old-ftp", "NO_PROXY=old-bypass", "HTTP_PROXY", "HTTP_PROXY=duplicate", "No_PrOxY", "http_proxy_extra=preserved", "=opaque", "httpſ_proxy=preserved", "BINARY=\xff", " HTTP_PROXY=preserved"}
	before := append([]string(nil), parent...)
	app := config.App{ID: "app-a", Mode: "managed", Bypass: []string{"Example.COM", ".internal.example", "127.0.0.1", "::1", "192.0.2.9/24", "Example.COM"}}
	binding := environmentBinding(t, app.ID)
	entry, err := binding.SensitiveEntry()
	if err != nil || entry.Name != config.AppEntryAlias(app.ID) {
		t.Fatal("fixture alias", err)
	}
	got, err := BuildEnvironment(parent, app, &binding)
	if err != nil {
		t.Fatal(err)
	}
	values := environmentValues(t, got)
	kept := []string{"KEEP=private-parent-value", "opaque-no-equals", "http_proxy_extra=preserved", "=opaque", "httpſ_proxy=preserved", "BINARY=\xff", " HTTP_PROXY=preserved"}
	if len(values) != len(kept)+8 || !reflect.DeepEqual(values[:len(kept)], kept) {
		t.Fatal("unrelated order/content changed or generated count wrong")
	}
	names := []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"}
	generated := map[string]string{}
	for i, name := range names {
		key, value, ok := strings.Cut(values[len(kept)+i], "=")
		if !ok || key != name {
			t.Fatal("generated pair order/name")
		}
		generated[key] = value
	}
	for _, name := range names[:6] {
		parsed, err := url.Parse(generated[name])
		if err != nil {
			t.Fatal("generated proxy URL invalid")
		}
		scheme := "http"
		if strings.EqualFold(name, "all_proxy") {
			scheme = "socks5h"
		}
		if parsed.Scheme != scheme || parsed.Host != "127.0.0.1:36401" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User == nil || parsed.User.Username() != environmentPrivateUser {
			t.Fatal("proxy URL endpoint/scheme/user roundtrip")
		}
		password, present := parsed.User.Password()
		if !present || password != environmentPrivatePassword {
			t.Fatal("reserved userinfo lost or interpreted as URL syntax")
		}
	}
	if generated["http_proxy"] != generated["HTTP_PROXY"] || generated["https_proxy"] != generated["HTTPS_PROXY"] || generated["http_proxy"] != generated["https_proxy"] || generated["all_proxy"] != generated["ALL_PROXY"] {
		t.Fatal("case pair or shared entry differs")
	}
	bypass := "Example.COM,.internal.example,127.0.0.1,::1,192.0.2.9/24,Example.COM"
	if generated["no_proxy"] != bypass || generated["NO_PROXY"] != bypass {
		t.Fatal("explicit bypass order/case/duplicates changed")
	}
	removed := []string{"http_proxy", "HtTp_PrOxY", "HTTPS_PROXY", "aLl_PrOxY", "Ftp_PrOxY", "NO_PROXY", "HTTP_PROXY", "HTTP_PROXY", "No_PrOxY"}
	want := []EnvironmentChange{}
	for _, name := range removed {
		want = append(want, EnvironmentChange{Name: name, Action: "remove"})
	}
	for _, name := range names {
		want = append(want, EnvironmentChange{Name: name, Action: "set"})
	}
	if !reflect.DeepEqual(got.Changes(), want) {
		t.Fatal("safe changes must preserve removed spelling/order/duplicates then set order")
	}
	if !reflect.DeepEqual(parent, before) || !reflect.DeepEqual(actualBefore, os.Environ()) {
		t.Fatal("parent shell environment mutated")
	}
	environmentPrivate(t, got)
	environmentPrivate(t, &got)
	environmentPrivate(t, got.Changes())
}

func TestEnvironmentThreeModesAndUnconsumedBypass(t *testing.T) {
	parent := []string{"X=private-parent-value", "http_proxy=x", "HTTPS_PROXY=y", "all_proxy=z", "ftp_proxy=f", "no_proxy=n", "HTTP_PROXY", "opaque", "KEEP=x"}
	actualBefore := os.Environ()
	before := append([]string(nil), parent...)
	zero := config.AppBinding{}
	for _, mode := range []string{"inherit", "direct"} {
		t.Run(mode, func(t *testing.T) {
			// Invalid bypass metadata and a nonmatching/zero binding are deliberately
			// irrelevant in these modes; no hidden managed admission is performed.
			for _, binding := range []*config.AppBinding{nil, &zero} {
				got, err := BuildEnvironment(parent, config.App{ID: "app-a", Mode: mode, Bypass: []string{"not a valid bypass", "http://private.invalid"}}, binding)
				if err != nil {
					t.Fatal(err)
				}
				want := parent
				if mode == "direct" {
					want = []string{"X=private-parent-value", "opaque", "KEEP=x"}
				}
				if !reflect.DeepEqual(environmentValues(t, got), want) {
					t.Fatal("mode did not preserve or clear exact declared set")
				}
				if mode == "inherit" && len(got.Changes()) != 0 {
					t.Fatal("inherit reported changes")
				}
				if mode == "direct" {
					changes := got.Changes()
					if len(changes) != 6 {
						t.Fatal("direct removal count")
					}
					for _, change := range changes {
						if change.Action != "remove" {
							t.Fatal("direct added environment")
						}
					}
				}
			}
		})
	}
	binding := environmentBinding(t, "app-other")
	for _, mode := range []string{"inherit", "direct"} {
		if _, err := BuildEnvironment(parent, config.App{ID: "app-a", Mode: mode}, &binding); err != nil {
			t.Fatal("unused mismatched binding consumed", err)
		}
	}
	if !reflect.DeepEqual(before, parent) || !reflect.DeepEqual(actualBefore, os.Environ()) {
		t.Fatal("parent environment mutated")
	}
}

func TestEnvironmentDefaultBypassHasNoInheritedOrImplicitExclusion(t *testing.T) {
	binding := environmentBinding(t, "app-a")
	var first []string
	for _, bypass := range [][]string{nil, {}} {
		got, err := BuildEnvironment([]string{"NO_PROXY=localhost,127.0.0.1", "ftp_proxy=old"}, config.App{ID: "app-a", Mode: "managed", Bypass: bypass}, &binding)
		if err != nil {
			t.Fatal(err)
		}
		values := environmentValues(t, got)
		if len(values) != 8 || values[6] != "no_proxy=" || values[7] != "NO_PROXY=" {
			t.Fatal("implicit/inherited bypass or FTP promise added")
		}
		if first == nil {
			first = values
		} else if !reflect.DeepEqual(first, values) {
			t.Fatal("nil and empty bypass differ")
		}
	}
}

func TestEnvironmentBypassFiniteGrammarAndExactBounds(t *testing.T) {
	binding := environmentBinding(t, "app-a")
	app := config.App{ID: "app-a", Mode: "managed"}
	maxDomain := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	valid := []string{"*", "localhost", "Example.COM", ".Example.COM", "a-b.example", "192.0.2.1", "192.168.001.1", "::1", "2001:db8::1", "192.0.2.9/24", "2001:db8::1/64", "::ffff:192.0.2.1", maxDomain}
	for _, token := range valid {
		app.Bypass = []string{token}
		got, err := BuildEnvironment(nil, app, &binding)
		if err != nil {
			t.Fatal("valid token rejected", token, err)
		}
		if environmentValues(t, got)[6] != "no_proxy="+token {
			t.Fatal("token normalized, masked or reordered")
		}
	}
	invalid := []string{"", "a,b", "a b", " a", "a\t", "a\n", "a\r", "a\x00", "a\x7f", "http://host", "host/path", "host:80", "[::1]", "[::1]:80", "::1%lo", "fe80::1%eth0", "2001:0db8::1", "2001:DB8::1", "192.0.2.1/024", "2001:0db8::/64", "*.example", "a*b", "..example", ".", "host.", "-bad.example", "bad-.example", "a..b", "a_b", "例子.example", strings.Repeat("a", 64), maxDomain + "x"}
	for i, token := range invalid {
		t.Run(fmt.Sprintf("invalid-%02d", i), func(t *testing.T) {
			bad := app
			bad.Bypass = []string{token}
			environmentFailure(t, nil, bad, &binding, ErrEnvironment)
		})
	}
	// 255*63 + 64 characters + 255 separators = exactly16KiB. Domains,
	// ordering and duplicate tokens remain explicit; this is not client matching.
	tokens := make([]string, 256)
	for i := range tokens {
		tokens[i] = strings.Repeat("a", 63)
	}
	tokens[0] = strings.Repeat("b", 62) + ".c"
	app.Bypass = tokens
	got, err := BuildEnvironment(nil, app, &binding)
	if err != nil {
		t.Fatal("exact joined/token limit rejected", err)
	}
	values := environmentValues(t, got)
	if len(strings.TrimPrefix(values[6], "no_proxy=")) != 16<<10 {
		t.Fatal("boundary fixture length")
	}
	tooLong := append([]string(nil), tokens...)
	tooLong[0] = "d" + tooLong[0]
	app.Bypass = tooLong
	environmentFailure(t, nil, app, &binding, ErrEnvironment)
	app.Bypass = make([]string, 257)
	for i := range app.Bypass {
		app.Bypass[i] = "a"
	}
	environmentFailure(t, nil, app, &binding, ErrEnvironment)
}

func TestEnvironmentInvalidShapeBindingAndMeteringReturnZero(t *testing.T) {
	binding := environmentBinding(t, "app-a")
	zero := config.AppBinding{}
	other := environmentBinding(t, "app-b")
	for _, mode := range []string{"managed", "direct", "inherit"} {
		environmentFailure(t, []string{"http_proxy=bad\x00value"}, config.App{ID: "app-a", Mode: mode}, &binding, ErrEnvironment)
		for _, id := range []config.ID{"", "../app", "bad id", config.ID(string([]byte{0xff}))} {
			environmentFailure(t, nil, config.App{ID: id, Mode: mode}, &binding, ErrEnvironment)
		}
	}
	for _, mode := range []string{"", "Managed", "unknown"} {
		environmentFailure(t, nil, config.App{ID: "app-a", Mode: mode}, &binding, ErrEnvironment)
	}
	for _, bad := range []*config.AppBinding{nil, &zero, &other} {
		environmentFailure(t, []string{"X=private-parent-value"}, config.App{ID: "app-a", Mode: "managed"}, bad, ErrEnvironment)
	}
	for _, mode := range []string{"inherit", "direct"} {
		environmentFailure(t, nil, config.App{ID: "app-a", Mode: mode, Metering: true}, nil, ErrMeteringMode)
	}
	environmentFailure(t, nil, config.App{ID: "app-a", Mode: "managed", Metering: true}, &binding, ErrMeteringUnsupported)
	var empty Environment
	if _, err := empty.SensitiveValues(); !errors.Is(err, ErrEnvironment) {
		t.Fatal("zero environment is consumable")
	}
	if len(empty.Changes()) != 0 {
		t.Fatal("zero environment changes")
	}
	environmentPrivate(t, empty)
	environmentPrivate(t, &empty)
}

func TestEnvironmentOwnsInputsOutputsAndValueCopies(t *testing.T) {
	actualBefore := os.Environ()
	parent := []string{"KEEP=private-parent-value", "HTTP_PROXY=old-http-secret"}
	app := config.App{ID: "app-a", Mode: "managed", Bypass: []string{"Example.COM"}}
	binding := environmentBinding(t, app.ID)
	got, err := BuildEnvironment(parent, app, &binding)
	if err != nil {
		t.Fatal(err)
	}
	original := append([]string(nil), environmentValues(t, got)...)
	changes := got.Changes()
	expectedChanges := append([]EnvironmentChange(nil), changes...)
	parent[0] = "changed"
	app.Bypass[0] = "changed.example"
	binding = config.AppBinding{}
	if !reflect.DeepEqual(environmentValues(t, got), original) {
		t.Fatal("build retained mutable input")
	}
	copyValue := got
	values := environmentValues(t, got)
	values[0] = "caller mutated sensitive output"
	changes[0].Name = "caller changed safe output"
	changes[0].Action = "invalid"
	if !reflect.DeepEqual(environmentValues(t, got), original) || !reflect.DeepEqual(environmentValues(t, copyValue), original) || !reflect.DeepEqual(got.Changes(), expectedChanges) || !reflect.DeepEqual(copyValue.Changes(), expectedChanges) {
		t.Fatal("returned slice/value copy shares mutable state")
	}
	for _, mode := range []string{"inherit", "direct"} {
		input := []string{"KEEP=value"}
		env, err := BuildEnvironment(input, config.App{ID: "app-a", Mode: mode}, nil)
		if err != nil {
			t.Fatal(err)
		}
		input[0] = "changed"
		out := environmentValues(t, env)
		if !reflect.DeepEqual(out, []string{"KEEP=value"}) {
			t.Fatal("unmanaged output aliases input")
		}
		out[0] = "changed again"
		if environmentValues(t, env)[0] != "KEEP=value" {
			t.Fatal("unmanaged getter aliases output")
		}
	}
	if !reflect.DeepEqual(actualBefore, os.Environ()) {
		t.Fatal("real parent process environment changed")
	}
}

func TestEnvironmentEmptyParentStillProducesValidUnmanagedEnvironment(t *testing.T) {
	for _, mode := range []string{"direct", "inherit"} {
		for _, parent := range [][]string{nil, {}} {
			env, err := BuildEnvironment(parent, config.App{ID: "app-a", Mode: mode}, nil)
			if err != nil {
				t.Fatal("empty parent rejected", err)
			}
			if len(environmentValues(t, env)) != 0 || len(env.Changes()) != 0 {
				t.Fatal("empty parent fabricated environment changes")
			}
		}
	}
}

func TestEnvironmentEveryDeclaredNameDuplicatesUseASCIIKeysOnly(t *testing.T) {
	for _, name := range []string{"http_proxy", "https_proxy", "all_proxy", "ftp_proxy", "no_proxy"} {
		lower, upper := strings.ToLower(name), strings.ToUpper(name)
		parent := []string{lower + "=one", upper + "=two", upper, upper + "=three", "UNCHANGED=private-parent-value"}
		got, err := BuildEnvironment(parent, config.App{ID: "app-a", Mode: "direct"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(environmentValues(t, got), []string{"UNCHANGED=private-parent-value"}) {
			t.Fatal("case variant or duplicate survived clearing")
		}
		if !reflect.DeepEqual(got.Changes(), []EnvironmentChange{{Name: lower, Action: "remove"}, {Name: upper, Action: "remove"}, {Name: upper, Action: "remove"}, {Name: upper, Action: "remove"}}) {
			t.Fatal("removed spelling/order/duplicates lost")
		}
	}
	for _, mode := range []string{"direct", "inherit"} {
		oversized := make([]string, 257)
		for i := range oversized {
			oversized[i] = "bad\x00token"
		}
		for _, bypass := range [][]string{oversized, {strings.Repeat("x", (16<<10)+1)}} {
			got, err := BuildEnvironment([]string{"KEEP=value"}, config.App{ID: "app-a", Mode: mode, Bypass: bypass}, nil)
			if err != nil || !reflect.DeepEqual(environmentValues(t, got), []string{"KEEP=value"}) {
				t.Fatal("unused bypass validation changed unmanaged mode", err)
			}
		}
	}
}
