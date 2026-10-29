//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	profile "hoopa/protocol/sourceprofile"
)

func TestApplicationCaptureDraftModesAndMissingState(t *testing.T) {
	p, e := config.ResolvePaths(t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = CaptureApplication(context.Background(), p, "app-a", "direct", false); !errors.Is(e, ErrProfileUnavailable) {
		t.Fatal(e)
	}
	if _, e = os.Stat(p.State); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("read created state", e)
	}
	c, p := coordinatorFixture(t, nil)
	raw := appPlanIntent(t)
	sha, e := c.store.PublishDraft(raw)
	if e != nil {
		t.Fatal(e)
	}
	before := coordinatorTree(t, p)
	for _, mode := range []string{"direct", "inherit"} {
		capture, e := CaptureApplication(context.Background(), p, "app-a", mode, false)
		if e != nil {
			t.Fatal(e)
		}
		got, e := capture.Profile()
		if e != nil || got.Kind != "draft" || got.Revision != "draft:"+sha || got.App.Mode != "managed" || got.Generation != nil {
			t.Fatal("wrong captured source", e)
		}
		app, _ := capture.Application()
		if app.Mode != mode || app.Bypass == nil {
			t.Fatal("override lost")
		}
		b, _ := capture.SensitiveBinding()
		if b != nil {
			t.Fatal("nonmanaged consumed credentials")
		}
		got.App.Bypass = append(got.App.Bypass, "changed")
		again, _ := capture.Profile()
		if len(again.App.Bypass) != 0 {
			t.Fatal("capture aliased")
		}
	}
	for _, tc := range []struct {
		id    config.ID
		mode  string
		meter bool
		want  error
	}{
		{"app-a", "", false, ErrManagedEntryUnavailable}, {"missing", "direct", false, ErrAppNotFound},
		{"app-a", "direct", true, ErrAppMeteringMode}, {"app-a", "managed", true, ErrAppMeteringUnsupported},
		{"bad id", "direct", false, ErrRequest}, {"app-a", "wrong", false, ErrRequest},
	} {
		if _, e := CaptureApplication(context.Background(), p, tc.id, tc.mode, tc.meter); !errors.Is(e, tc.want) {
			t.Fatalf("%s %s %v", tc.id, tc.mode, e)
		}
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("capture changed state")
	}
}

func applicationCaptureFixture(t *testing.T, mutation string) (*Coordinator, config.Paths, config.AppBinding) {
	t.Helper()
	c, p := coordinatorFixture(t, nil)
	s := initializeSettings(t)
	initializePut(t, c, s)
	b := initializeApp(t, s, "app-a", 27001, "q")
	appInitializePut(t, c, b)
	raw := appPlanIntent(t)
	compiled := appPlanCompilation(t, raw, s, b)
	m, files := compiled.Bundle()
	name, _ := config.AppBindingFilename("app-a")
	for i := 0; i < len(m.Files); i++ {
		f := &m.Files[i]
		if f.Name == name {
			switch mutation {
			case "missing-asset":
				delete(files, name)
				m.Files = append(m.Files[:i], m.Files[i+1:]...)
				i--
				continue
			case "wrong-kind":
				f.Kind = "validation"
			}
		}
		if f.Kind == "generated_config" && (mutation == "listener-auth" || mutation == "controller-secret") {
			var generated profile.Config
			if json.Unmarshal(files[f.Name], &generated) != nil {
				t.Fatal("fixture")
			}
			if mutation == "listener-auth" {
				generated.Listeners[1].Users[0].Password = strings.Repeat("z", 32)
			} else {
				generated.Secret = strings.Repeat("z", 32)
			}
			changed, e := json.Marshal(generated)
			if e != nil {
				t.Fatal(e)
			}
			files[f.Name] = changed
			f.SHA256 = hash(changed)
			f.Bytes = int64(len(changed))
		}
	}
	sha, e := c.store.PublishBundle(m, files)
	if e != nil {
		t.Fatal(e)
	}
	active, e := c.store.ReadActive()
	if e != nil {
		t.Fatal(e)
	}
	g, op := m.Generation, config.ID("app-capture-metadata-fixture")
	active.Generation, active.ManifestSHA256, active.OperationID, active.DesiredRuntime = &g, sha, &op, "running"
	if e = c.store.PublishActive(active); e != nil {
		t.Fatal(e)
	}
	// Draft differs deliberately. A committed selection must never fall back.
	var draft map[string]any
	if json.Unmarshal(raw, &draft) != nil {
		t.Fatal("fixture")
	}
	draft["apps"] = []any{}
	if _, e = c.store.PublishDraft(planRaw(t, draft)); e != nil {
		t.Fatal(e)
	}
	switch mutation {
	case "missing-input":
		if e = os.Remove(filepath.Join(p.State, "credentials", name)); e != nil {
			t.Fatal(e)
		}
	case "changed-input":
		other := initializeApp(t, s, "app-a", 27002, "z")
		raw, _ := other.SensitiveBytes()
		if e = os.WriteFile(filepath.Join(p.State, "credentials", name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	case "missing-settings":
		if e = os.Remove(filepath.Join(p.State, "credentials", "source-settings-v1.json")); e != nil {
			t.Fatal(e)
		}
	}
	return c, p, b
}

func TestApplicationCaptureCommittedIdentityAndNoFallback(t *testing.T) {
	for _, mutation := range []string{"none", "missing-asset", "wrong-kind", "listener-auth", "controller-secret", "missing-input", "changed-input", "missing-settings"} {
		t.Run(mutation, func(t *testing.T) {
			c, p, b := applicationCaptureFixture(t, mutation)
			before := coordinatorTree(t, p)
			capture, e := CaptureApplication(context.Background(), p, "app-a", "", false)
			if mutation != "none" {
				if !errors.Is(e, ErrManagedEntryUnavailable) {
					t.Fatal("managed admission", e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				got, _ := capture.Profile()
				active, _ := c.store.ReadActive()
				if got.Kind != "committed" || got.Generation == nil || *got.Generation != *active.Generation || got.ManifestSHA256 == nil || *got.ManifestSHA256 != active.ManifestSHA256 {
					t.Fatal("wrong profile")
				}
				binding, _ := capture.SensitiveBinding()
				if binding == nil || binding.Digest() != b.Digest() {
					t.Fatal("binding")
				}
				for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%p"} {
					if strings.Contains(fmt.Sprintf(verb, capture), strings.Repeat("q", 32)) {
						t.Fatal("capture leaked")
					}
				}
				*got.Generation = "mutated"
				*got.ManifestSHA256 = "mutated"
				again, _ := capture.Profile()
				if *again.Generation != *active.Generation || *again.ManifestSHA256 != active.ManifestSHA256 {
					t.Fatal("aliased profile")
				}
				if e = QualifyApplicationEntry(context.Background(), p, capture, ServiceStatus{}); !errors.Is(e, ErrManagedEntryUnavailable) {
					t.Fatal("metadata invented liveness", e)
				}
			}
			// Direct needs the committed declaration, not private runtime input.
			direct, e := CaptureApplication(context.Background(), p, "app-a", "direct", false)
			if e != nil {
				t.Fatal(e)
			}
			got, _ := direct.Profile()
			if got.Kind != "committed" {
				t.Fatal("draft fallback")
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("read/qualification rejection changed state")
			}
		})
	}
}

func TestApplicationCaptureCorruptCommittedDoesNotUseDraft(t *testing.T) {
	c, p, _ := applicationCaptureFixture(t, "none")
	if _, e := c.store.PublishDraft(appPlanIntent(t)); e != nil {
		t.Fatal(e)
	}
	active, _ := c.store.ReadActive()
	// Own fixture only: destroy an already selected bundle file.
	path := filepath.Join(p.State, "bundles", hash([]byte(*active.Generation)), "manifest.json")
	if e := os.WriteFile(path, []byte("invalid\n"), 0600); e != nil {
		t.Fatal(e)
	}
	before := coordinatorTree(t, p)
	if _, e := CaptureApplication(context.Background(), p, "app-a", "direct", false); !errors.Is(e, ErrProfileUnavailable) {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
		t.Fatal("repaired corruption")
	}
}

type appAuthFixture struct {
	in    *bytes.Reader
	out   bytes.Buffer
	short bool
}

func (c *appAuthFixture) Read(p []byte) (int, error) { return c.in.Read(p) }
func (c *appAuthFixture) Write(p []byte) (int, error) {
	if c.short {
		return 0, io.ErrShortWrite
	}
	return c.out.Write(p)
}
func TestApplicationEntryAuthenticationIsBoundedAndRequiresPasswordMethod(t *testing.T) {
	user := profile.User{Username: "alice", Password: "secret"}
	for _, tc := range []struct {
		name  string
		input []byte
		ok    bool
		auth  bool
	}{
		{"accepted", []byte{5, 2, 1, 0}, true, true}, {"no-auth", []byte{5, 0}, false, false},
		{"wrong-version", []byte{4, 2}, false, false}, {"truncated", []byte{5}, false, false},
		{"bad-password", []byte{5, 2, 1, 1}, false, true}, {"bad-auth-version", []byte{5, 2, 2, 0}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &appAuthFixture{in: bytes.NewReader(tc.input)}
			e := authenticateApplicationEntry(c, user)
			if (e == nil) != tc.ok {
				t.Fatal(e)
			}
			want := []byte{5, 1, 2}
			if tc.auth {
				want = append(want, 1, 5)
				want = append(want, "alice"...)
				want = append(want, 6)
				want = append(want, "secret"...)
			}
			if !bytes.Equal(c.out.Bytes(), want) {
				t.Fatal("unexpected bytes or destination CONNECT")
			}
		})
	}
	c := &appAuthFixture{in: bytes.NewReader(nil), short: true}
	if !errors.Is(authenticateApplicationEntry(c, user), ErrManagedEntryUnavailable) {
		t.Fatal("short write")
	}
}
