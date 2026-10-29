//go:build linux && amd64

package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/platform"
)

func endpointFixture(t *testing.T) (*control.Coordinator, config.Paths) {
	t.Helper()
	home, e := os.MkdirTemp("/tmp", "hoopa-endpoint-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	p, e := config.ResolvePaths(home, nil)
	if e != nil {
		t.Fatal(e)
	}
	c, e := control.OpenCoordinator(p, func(json.RawMessage, []control.PlanInput) error { return errors.New("reject fixture") }, func(context.Context, control.Plan) (control.CurrentPlanInputs, error) {
		return control.CurrentPlanInputs{}, errors.New("reject fixture")
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := c.Close(); e != nil {
			t.Error(e)
		}
	})
	return c, p
}
func openTestEndpoint(t *testing.T, c *control.Coordinator) *Endpoint {
	t.Helper()
	e, err := OpenEndpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}
func acceptEndpoint(t *testing.T, e *Endpoint) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c, err := e.Accept(ctx)
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	return done
}
func TestEndpointReadinessAndReadOnly(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	before, err := os.ReadFile(filepath.Join(p.Runtime, discoveryName))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(p.Runtime, discoveryName))
	entries, _ := os.ReadDir(p.Runtime)
	done := acceptEndpoint(t, e)
	conn, err := DialEndpoint(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(p.Runtime, discoveryName))
	now, _ := os.Stat(filepath.Join(p.Runtime, discoveryName))
	end, _ := os.ReadDir(p.Runtime)
	if !bytes.Equal(before, after) || !os.SameFile(info, now) || len(entries) != len(end) {
		t.Fatal("readonly discovery changed tree")
	}
	if !validInstance(e.Instance()) {
		t.Fatal("instance")
	}
	copy := *e
	if err = copy.Close(); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = DialEndpoint(context.Background(), p); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
}
func TestEndpointHandshakeExactAndWireFollows(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	done := make(chan error, 1)
	go func() {
		conn, err := e.Accept(context.Background())
		if err == nil {
			defer conn.Close()
			var b [1]byte
			_, err = io.ReadFull(conn, b[:])
			if b[0] != 42 {
				err = ErrDiscovery
			}
		}
		done <- err
	}()
	root, err := config.OpenRuntimeDirectory(p)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	conn, _, err := platform.DialPrivateUnix(context.Background(), root, e.owned.descriptor.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	greeting := make([]byte, 32)
	copy(greeting, "HPSVC001")
	decoded, _ := hex.DecodeString(e.Instance())
	copy(greeting[8:], decoded)
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = conn.Write(append(greeting, 42)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 32)
	if _, err = io.ReadFull(conn, got); err != nil || !bytes.Equal(got, greeting) {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestEndpointChangedDescriptorPreserved(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	path := filepath.Join(p.Runtime, discoveryName)
	changed := []byte(`{"changed":true}`)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); !errors.Is(err, ErrDiscovery) {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, changed) {
		t.Fatal("changed descriptor removed")
	}
}
func TestEndpointRejectDescriptor(t *testing.T) {
	for _, kind := range []string{"whitespace", "unknown", "pid", "mode", "link", "symlink", "huge", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			c, p := endpointFixture(t)
			e := openTestEndpoint(t, c)
			path := filepath.Join(p.Runtime, discoveryName)
			raw := e.owned.raw
			switch kind {
			case "whitespace":
				raw = append(raw, ' ')
			case "unknown":
				raw = []byte(`{"extra":true}`)
			case "pid":
				d := e.owned.descriptor
				d.PID++
				raw, _ = descriptorBytes(d)
			case "mode":
				os.Chmod(path, 0644)
			case "link":
				if err := os.Link(path, filepath.Join(p.Runtime, "linked")); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				os.Rename(path, path+".saved")
				os.Symlink("service.json.saved", path)
			case "huge":
				raw = []byte(strings.Repeat(" ", 1025))
			case "duplicate":
				raw = []byte(`{"version":1,"version":1}`)
			}
			if kind != "mode" && kind != "link" && kind != "symlink" {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			conn, err := DialEndpoint(context.Background(), p)
			if conn != nil {
				conn.Close()
			}
			if !errors.Is(err, ErrDiscovery) {
				t.Fatal(err)
			}
		})
	}
}
func TestEndpointAcceptCancellationAndClose(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Accept(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	root, err := config.OpenRuntimeDirectory(p)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	raw, _, err := platform.DialPrivateUnix(context.Background(), root, e.owned.descriptor.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	done := make(chan error, 1)
	go func() { _, err := e.Accept(context.Background()); done <- err }()
	time.Sleep(20 * time.Millisecond)
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not interrupt handshake")
	}
}
func TestEndpointWrongHandshakeAndTimeout(t *testing.T) {
	for _, kind := range []string{"wrong", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			c, p := endpointFixture(t)
			e := openTestEndpoint(t, c)
			root, err := config.OpenRuntimeDirectory(p)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			conn, _, err := platform.DialPrivateUnix(context.Background(), root, e.owned.descriptor.Endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if kind == "wrong" {
				conn.Write(make([]byte, 32))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			accepted, err := e.Accept(ctx)
			if accepted != nil {
				accepted.Close()
			}
			if !errors.Is(err, ErrDiscovery) {
				t.Fatal(err)
			}
			if kind == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}
func TestEndpointCapacity(t *testing.T) {
	for _, kind := range []string{"endpoints", "entries"} {
		t.Run(kind, func(t *testing.T) {
			c, p := endpointFixture(t)
			count := 128
			if kind == "entries" {
				count = 4097
			}
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("svc-%04d.sock", i)
				if kind == "entries" {
					name = fmt.Sprintf("opaque-%04d", i)
				}
				if err := os.WriteFile(filepath.Join(p.Runtime, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if e, err := OpenEndpoint(c); !errors.Is(err, ErrDiscovery) {
				if e != nil {
					e.Close()
				}
				t.Fatal(err)
			}
		})
	}
}
func TestEndpointStaleSocketRetained(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	old := e.owned.descriptor.Endpoint
	// A crash leaves the socket and descriptor. Release writer without Endpoint.Close.
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := control.OpenCoordinator(p, func(json.RawMessage, []control.PlanInput) error { return ErrDiscovery }, func(context.Context, control.Plan) (control.CurrentPlanInputs, error) {
		return control.CurrentPlanInputs{}, ErrDiscovery
	})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	newer := openTestEndpoint(t, next)
	if newer.Instance() == e.Instance() {
		t.Fatal("reused incarnation")
	}
	if _, err = os.Lstat(filepath.Join(p.Runtime, old)); err != nil {
		t.Fatal("old socket removed", err)
	}
	if err = e.Close(); !errors.Is(err, ErrDiscovery) {
		t.Fatal("old cleanup must preserve new discovery", err)
	}
	done := acceptEndpoint(t, newer)
	conn, err := DialEndpoint(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndpointWrongIncarnation(t *testing.T) {
	c, p := endpointFixture(t)
	e := openTestEndpoint(t, c)
	d := e.owned.descriptor
	d.Instance = strings.Repeat("f", 48)
	if d.Instance == e.Instance() {
		d.Instance = strings.Repeat("e", 48)
	}
	d.Endpoint = "svc-" + d.Instance + ".sock"
	if err := os.Rename(filepath.Join(p.Runtime, e.owned.descriptor.Endpoint), filepath.Join(p.Runtime, d.Endpoint)); err != nil {
		t.Fatal(err)
	}
	raw, _ := descriptorBytes(d)
	if err := os.WriteFile(filepath.Join(p.Runtime, discoveryName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	done := acceptEndpoint(t, e)
	conn, err := DialEndpoint(context.Background(), p)
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, ErrDiscovery) {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrDiscovery) {
		t.Fatal(err)
	}
}
func TestEndpointMalformedExistingFailsBeforePublish(t *testing.T) {
	c, p := endpointFixture(t)
	path := filepath.Join(p.Runtime, discoveryName)
	raw := []byte(`{"version":1}`)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if endpoint, err := OpenEndpoint(c); !errors.Is(err, ErrDiscovery) {
		if endpoint != nil {
			endpoint.Close()
		}
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	entries, _ := os.ReadDir(p.Runtime)
	if !bytes.Equal(got, raw) || len(entries) != 1 {
		t.Fatal("failed open changed runtime")
	}
}
func TestEndpointDialCancelled(t *testing.T) {
	_, p := endpointFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if conn, err := DialEndpoint(ctx, p); conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
