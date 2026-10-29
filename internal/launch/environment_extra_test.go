//go:build linux

package launch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func TestEnvironmentExplicitEmptyValuesNeverSignalImplicitInheritance(t *testing.T) {
	for _, mode := range []string{"direct", "inherit"} {
		for _, parent := range [][]string{nil, {}} {
			env, err := BuildEnvironment(parent, config.App{ID: "app-a", Mode: mode}, nil)
			if err != nil {
				t.Fatal(err)
			}
			values, err := env.SensitiveValues()
			if err != nil || values == nil || len(values) != 0 {
				t.Fatal("valid empty input must return explicit empty environment, not exec.Cmd.Env inheritance")
			}
			raw, err := json.Marshal(values)
			if err != nil || string(raw) != "[]" {
				t.Fatal("valid empty values are not an array")
			}
			changes := env.Changes()
			raw, err = json.Marshal(changes)
			if err != nil || string(raw) != "[]" {
				t.Fatal("empty safe changes should remain an array")
			}
		}
	}
}
func TestEnvironmentAllFormattingVerbsRemainPrivate(t *testing.T) {
	secret := "private-environment-unexpected-verb"
	env, err := BuildEnvironment([]string{"KEEP=" + secret}, config.App{ID: "app-a", Mode: "direct"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{env, &env, struct{ Child Environment }{env}, struct{ Child *Environment }{&env}, []Environment{env}, map[string]Environment{"child": env}} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), secret) {
			t.Fatal("nested JSON reflected private environment fields")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%+d", "%f", "%e", "%b", "%c", "%t", "%T", "%p", "%w"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, secret) || strings.Contains(text, fmt.Sprintf("%x", []byte(secret))) || strings.Contains(text, fmt.Sprintf("%X", []byte(secret))) {
				t.Fatal("format verb reflected private environment fields", format)
			}
		}
	}
}
