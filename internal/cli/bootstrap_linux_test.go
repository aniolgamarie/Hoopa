//go:build linux && amd64

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCLIReadinessBoundsBindingAndShape(t *testing.T) {
	nonce := strings.Repeat("a", 48)
	valid := readiness{Version: 1, Nonce: nonce, Instance: strings.Repeat("b", 48), Revision: "revision-one"}
	for _, kind := range []string{"valid", "nonce", "instance", "revision", "version", "extra", "duplicate", "trailing", "oversize", "writer-busy", "error"} {
		t.Run(kind, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			value := valid
			switch kind {
			case "nonce":
				value.Nonce = strings.Repeat("c", 48)
			case "instance":
				value.Instance = "INVALID"
			case "revision":
				value.Revision = "../bad"
			case "version":
				value.Version = 2
			case "writer-busy":
				value.Instance = ""
				value.Revision = ""
				value.Error = "writer-busy"
			case "error":
				value.Instance = ""
				value.Revision = ""
				value.Error = "unavailable"
			}
			raw, _ := json.Marshal(value)
			switch kind {
			case "extra":
				raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
			case "duplicate":
				raw = append(raw[:len(raw)-1], []byte(`,"version":1}`)...)
			case "trailing":
				raw = append(raw, []byte(` {}`)...)
			case "oversize":
				raw = []byte(strings.Repeat(" ", readinessLimit+1))
			}
			done := make(chan error, 1)
			go func() { _, err := w.Write(raw); w.Close(); done <- err }()
			ctx, cancel := context.WithCancel(context.Background())
			got, err := readReadiness(ctx, r, nonce)
			cancel()
			if kind == "valid" {
				if err != nil || got != valid {
					t.Fatal(got, err)
				}
				if _, err = r.Stat(); err != nil {
					t.Fatal("late cancellation closed returned pipe", err)
				}
			} else if kind == "writer-busy" {
				if !errors.Is(err, errWriterBusy) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, errBootstrap) {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCLIReadinessCancellationAndDeadlineRemainBounded(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "deadline"
		if cancelled {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			if cancelled {
				cancel()
			}
			start := time.Now()
			_, err = readReadiness(ctx, r, strings.Repeat("a", 48))
			want := context.DeadlineExceeded
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || time.Since(start) > time.Second {
				t.Fatal("pipe deadline/cancellation failed", err, time.Since(start))
			}
		})
	}
}
func TestCLIServiceChildEnvironmentExcludesUnrelatedSecrets(t *testing.T) {
	t.Setenv("HOOPA_RUN_NONCE", "core-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "unrelated-secret")
	t.Setenv("HTTP_PROXY", "proxy-secret")
	t.Setenv("LANG", "")
	allowed := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true, "TMPDIR": true, "LANG": true, "HOOPA_SERVICE_PROTOCOL": true, "HOOPA_SERVICE_NONCE": true}
	env := serviceEnvironment(strings.Repeat("a", 48))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !allowed[key] || strings.Contains(entry, "secret") {
			t.Fatal("child inherited unrelated environment")
		}
	}
}
