//go:build linux && amd64

package mihomo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hoopa/internal/config"
	wire "hoopa/protocol/corecontrol"
)

type inspectorUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type inspectorBlueprint struct {
	record           CapabilityRecord
	intent           config.RunIntent
	snapshot         wire.Snapshot
	raw              []byte
	secret           string
	specs            []struct{ kind, name string }
	capabilityDigest string
	bundleBinaries   []config.BinaryReference
	extra            map[string][]byte
}
type inspectorFixture struct {
	paths      config.Paths
	store      *config.Store
	blueprint  inspectorBlueprint
	controller *Controller
}

func inspectorJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func inspectorContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func inspectorBlueprintFor(t *testing.T, address string, binary config.BinaryReference, entries []string) inspectorBlueprint {
	t.Helper()
	b := inspectorBlueprint{secret: strings.Repeat("private-test-token-", 2), extra: map[string][]byte{}}
	if binary.Path == "" {
		binary = config.BinaryReference{Path: "/synthetic/fixed-core", Version: "1.10.0", SHA256: sha256String([]byte("synthetic fixed binary"))}
	}
	b.record = CapabilityRecord{SchemaVersion: 1, Binary: binary, GOOS: "linux", GOARCH: "amd64", Protocol: 1, Mode: "basic", Capabilities: []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}, EvidenceSHA256: sha256String([]byte("synthetic fixture acceptance; not a production capability grant")), Accepted: true}
	b.capabilityDigest, _ = b.record.Digest()
	b.bundleBinaries = []config.BinaryReference{binary}
	if entries == nil {
		entries = []string{"127.0.0.1:31001", "127.0.0.1:31002"}
	}
	listeners := []map[string]any{}
	projectedEntries := []wire.Entry{}
	for i, endpoint := range entries {
		host, portText, err := net.SplitHostPort(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		var port int
		if _, err := fmt.Sscan(portText, &port); err != nil {
			t.Fatal(err)
		}
		users := []inspectorUser{{strings.Repeat(string(rune('u'+i)), 32), strings.Repeat(string(rune('p'+i)), 32)}}
		name := fmt.Sprintf("entry-%c", 'a'+i)
		listeners = append(listeners, map[string]any{"name": name, "type": "mixed", "listen": host, "port": port, "udp": false, "users": users})
		projectedEntries = append(projectedEntries, wire.Entry{Name: name, Type: "mixed", Address: endpoint, AuthSHA256: sha256String(inspectorJSON(t, users))})
	}
	raw := inspectorJSON(t, map[string]any{"mode": "rule", "log-level": "silent", "ipv6": false, "find-process-mode": "off", "external-controller": address, "secret": b.secret, "listeners": listeners, "hosts": map[string]string{}, "proxies": []map[string]any{{"name": "node:a", "type": "direct"}, {"name": "node:b", "type": "direct"}}, "proxy-groups": []map[string]any{{"name": "group:route", "type": "select", "proxies": []string{"node:a", "node:b"}}}, "rules": []string{"DOMAIN,alpha.test,group:route", "MATCH,node:a"}, "profile": map[string]bool{"store-selected": false, "store-fake-ip": false}})
	b.raw = raw
	b.intent = config.RunIntent{Version: 1, RunID: "run:inspector", OperationID: "operation:inspect", Generation: "generation:inspector", Binary: binary, Workdir: "/synthetic/work", Arguments: []string{binary.Path}, Controller: address, ConfigSHA256: sha256String(raw), NonceSHA256: sha256String([]byte(strings.Repeat("ab", 32)))}
	b.snapshot = wire.Snapshot{SchemaVersion: 1, ConfigSHA256: b.intent.ConfigSHA256, Controller: address, Entries: projectedEntries, Proxies: []wire.Proxy{{Name: "DIRECT", Type: "Direct", Members: []string{}}, {Name: "REJECT", Type: "Reject", Members: []string{}}, {Name: "REJECT-DROP", Type: "RejectDrop", Members: []string{}}, {Name: "COMPATIBLE", Type: "Compatible", Members: []string{}}, {Name: "PASS", Type: "Pass", Members: []string{}}, {Name: "GLOBAL", Type: "Selector", Members: []string{"DIRECT", "REJECT", "node:a", "node:b", "group:route"}, Selected: "DIRECT"}, {Name: "node:a", Type: "Direct", Members: []string{}}, {Name: "node:b", Type: "Direct", Members: []string{}}, {Name: "group:route", Type: "Selector", Members: []string{"node:a", "node:b"}, Selected: "node:a"}}, Rules: []wire.Rule{{Type: "Domain", Payload: "alpha.test", Target: "group:route"}, {Type: "Match", Payload: "", Target: "node:a"}}}
	sort.Slice(b.snapshot.Proxies, func(i, j int) bool { return b.snapshot.Proxies[i].Name < b.snapshot.Proxies[j].Name })
	b.specs = []struct{ kind, name string }{{"generated_config", "core.json"}, {"validation", SnapshotFile}}
	return b
}
func (b *inspectorBlueprint) rebindRaw(raw []byte) {
	b.raw = raw
	b.intent.ConfigSHA256 = sha256String(raw)
	b.snapshot.ConfigSHA256 = b.intent.ConfigSHA256
}
func inspectorPublish(t *testing.T, b inspectorBlueprint) *inspectorFixture {
	t.Helper()
	paths, err := config.ResolvePaths(filepath.Join(t.TempDir(), "home"), nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &inspectorFixture{paths: paths, store: store, blueprint: b}
	t.Cleanup(func() {
		if err := fixture.store.Close(); err != nil {
			t.Error(err)
		}
	})
	allFiles := map[string][]byte{"core.json": b.raw, SnapshotFile: inspectorJSON(t, b.snapshot)}
	for name, data := range b.extra {
		allFiles[name] = data
	}
	files := map[string][]byte{}
	manifest := config.BundleManifest{Version: 1, Generation: b.intent.Generation, CompilerVersion: "inspector-fixture-v1", CapabilitiesDigest: b.capabilityDigest, Binaries: b.bundleBinaries}
	for _, spec := range b.specs {
		raw := allFiles[spec.name]
		files[spec.name] = raw
		manifest.Files = append(manifest.Files, config.BundleFile{Kind: spec.kind, Name: spec.name, Bytes: int64(len(raw)), SHA256: sha256String(raw)})
	}
	sha, err := store.PublishBundle(manifest, files)
	if err != nil {
		t.Fatal("fixture publication:", err)
	}
	fixture.blueprint.intent.ManifestSHA256 = sha
	fixture.controller, err = NewController(store, b.record)
	if err != nil {
		t.Fatal("fixture controller:", err)
	}
	return fixture
}
func inspectorTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + sha256String(raw)
		}
		out[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func inspectorStatus(t *testing.T, b inspectorBlueprint, state string) wire.Status {
	t.Helper()
	digest, err := b.snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s := wire.Status{SchemaVersion: 1, RunID: string(b.intent.RunID), ConfigGeneration: string(b.intent.Generation), ConfigSHA256: b.intent.ConfigSHA256, BinaryVersion: b.intent.Binary.Version, Mode: "basic", Capabilities: append([]string(nil), b.record.Capabilities...), State: state, SnapshotSHA256: digest}
	if state == "open" {
		s.Activation = &wire.Result{SchemaVersion: 1, OperationID: "operation:activate", RequestSHA256: strings.Repeat("d", 64), RunID: s.RunID, Result: "opened"}
	}
	if state == "draining" {
		s.Drain = &wire.Result{SchemaVersion: 1, OperationID: "operation:drain", RequestSHA256: strings.Repeat("e", 64), RunID: s.RunID, Result: "draining"}
	}
	if state == "degraded" {
		s.Activation = &wire.Result{SchemaVersion: 1, OperationID: "operation:activate", RequestSHA256: strings.Repeat("d", 64), RunID: s.RunID, Result: "failed"}
	}
	return s
}
func TestMihomoCapabilityAndMaterialBindings(t *testing.T) {
	baseline := inspectorBlueprintFor(t, "127.0.0.1:31003", config.BinaryReference{}, nil)
	for name, change := range map[string]func(*CapabilityRecord){
		"not-accepted": func(c *CapabilityRecord) { c.Accepted = false }, "mode": func(c *CapabilityRecord) { c.Mode = "identity" }, "platform": func(c *CapabilityRecord) { c.GOOS = "windows" }, "protocol": func(c *CapabilityRecord) { c.Protocol = 2 }, "relative-path": func(c *CapabilityRecord) { c.Binary.Path = "core" }, "unclean-path": func(c *CapabilityRecord) { c.Binary.Path = "/fixed/../core" }, "empty-version": func(c *CapabilityRecord) { c.Binary.Version = "" }, "binary-hash": func(c *CapabilityRecord) { c.Binary.SHA256 = strings.Repeat("A", 64) }, "evidence": func(c *CapabilityRecord) { c.EvidenceSHA256 = "" }, "capability-order": func(c *CapabilityRecord) {
			c.Capabilities = append([]string(nil), c.Capabilities...)
			c.Capabilities[0], c.Capabilities[1] = c.Capabilities[1], c.Capabilities[0]
		},
	} {
		t.Run("capability-"+name, func(t *testing.T) {
			record := baseline.record
			change(&record)
			if err := record.Validate(); !errors.Is(err, ErrCapability) {
				t.Fatal("invalid capability accepted:", err)
			}
			if digest, err := record.Digest(); digest != "" || err == nil {
				t.Fatal("invalid capability minted digest")
			}
		})
	}
	for name, change := range map[string]func(*inspectorBlueprint){
		"manifest-capability": func(b *inspectorBlueprint) { b.capabilityDigest = sha256String([]byte("different capability")) },
		"capability-evidence-binding": func(b *inspectorBlueprint) {
			b.record.EvidenceSHA256 = sha256String([]byte("different accepted evidence"))
		},
		"intent-binary-path": func(b *inspectorBlueprint) {
			b.intent.Binary.Path = "/other/core"
			b.intent.Arguments = []string{b.intent.Binary.Path}
		},
		"intent-binary-version": func(b *inspectorBlueprint) { b.intent.Binary.Version = "other" },
		"intent-binary-hash":    func(b *inspectorBlueprint) { b.intent.Binary.SHA256 = strings.Repeat("c", 64) },
		"bundle-binary": func(b *inspectorBlueprint) {
			b.bundleBinaries = []config.BinaryReference{{Path: "/other/core", Version: "other", SHA256: strings.Repeat("c", 64)}}
		},
		"missing-config": func(b *inspectorBlueprint) { b.specs = b.specs[1:] },
		"duplicate-config": func(b *inspectorBlueprint) {
			b.extra["second.json"] = b.raw
			b.specs = append(b.specs, struct{ kind, name string }{"generated_config", "second.json"})
		},
		"missing-snapshot":    func(b *inspectorBlueprint) { b.specs = b.specs[:1] },
		"wrong-snapshot-kind": func(b *inspectorBlueprint) { b.specs[1].kind = "intent" },
		"snapshot-config":     func(b *inspectorBlueprint) { b.snapshot.ConfigSHA256 = strings.Repeat("b", 64) },
		"snapshot-controller": func(b *inspectorBlueprint) { b.snapshot.Controller = "127.0.0.1:31004" },
		"malformed-snapshot":  func(b *inspectorBlueprint) { b.snapshot.Rules = nil },
		"wrong-controller-in-config": func(b *inspectorBlueprint) {
			b.rebindRaw(bytes.Replace(b.raw, []byte("127.0.0.1:31003"), []byte("127.0.0.1:31004"), 1))
		},
		"short-secret": func(b *inspectorBlueprint) { b.rebindRaw(bytes.Replace(b.raw, []byte(b.secret), []byte("short"), 1)) },
		"colon-secret": func(b *inspectorBlueprint) {
			b.rebindRaw(bytes.Replace(b.raw, []byte(b.secret), []byte(strings.Repeat(":", 32)), 1))
		},
		"duplicate-secret": func(b *inspectorBlueprint) { b.rebindRaw(append([]byte(`{"secret":"`+b.secret+`",`), b.raw[1:]...)) },
	} {
		t.Run("material-"+name, func(t *testing.T) {
			b := inspectorBlueprintFor(t, "127.0.0.1:31003", config.BinaryReference{}, nil)
			change(&b)
			f := inspectorPublish(t, b)
			before := inspectorTree(t, f.paths.State)
			_, err := f.controller.material(f.blueprint.intent)
			if err == nil {
				t.Fatal("unbound material accepted")
			}
			if strings.Contains(err.Error(), f.blueprint.secret) {
				t.Fatal("material error leaked token")
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("material check wrote store")
			}
		})
	}
	t.Run("independent-validation-coexists", func(t *testing.T) {
		b := inspectorBlueprintFor(t, "127.0.0.1:31003", config.BinaryReference{}, nil)
		b.extra["compiler-report.json"] = []byte("{}\n")
		b.specs = append(b.specs, struct{ kind, name string }{"validation", "compiler-report.json"})
		f := inspectorPublish(t, b)
		if _, err := f.controller.material(f.blueprint.intent); err != nil {
			t.Fatal("other named validation unexpectedly forbidden:", err)
		}
	})
	t.Run("duplicate-fixed-name-store-refuses", func(t *testing.T) {
		f := inspectorPublish(t, baseline)
		bundle, err := f.store.ReadBundle(f.blueprint.intent.Generation)
		if err != nil {
			t.Fatal(err)
		}
		m := bundle.Manifest
		m.Generation = "generation:duplicate"
		for _, file := range bundle.Manifest.Files {
			if file.Name == SnapshotFile {
				m.Files = append(m.Files, file)
				break
			}
		}
		before := inspectorTree(t, f.paths.State)
		if _, err := f.store.PublishBundle(m, bundle.Files); !errors.Is(err, config.ErrIntegrity) {
			t.Fatal("duplicate fixed snapshot name accepted")
		}
		if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
			t.Fatal("duplicate snapshot wrote records")
		}
	})
	t.Run("constructor-copy-and-redaction", func(t *testing.T) {
		f := inspectorPublish(t, baseline)
		record := f.blueprint.record
		c, err := NewController(f.store, record)
		if err != nil {
			t.Fatal(err)
		}
		record.Capabilities[0] = "modified"
		if _, err := c.material(f.blueprint.intent); err != nil {
			t.Fatal("capability slice aliased caller")
		}
		if raw := fmt.Sprintf("%#v %+v", c, *c); strings.Contains(raw, f.blueprint.secret) {
			t.Fatal("controller repr leaked secret")
		}
		if c, err := NewController(nil, baseline.record); c != nil || err == nil {
			t.Fatal("nil store accepted")
		}
	})
}

// rawInspectorServer is a real loopback transport fixture, not a trusted core
// or owner proof. Its scripts emit raw HTTP and never derive expected material.
type rawInspectorServer struct {
	listener *net.TCPListener
	done     chan struct{}
	stop     chan struct{}
	accepted atomic.Int32
	requests atomic.Int32
	mu       sync.Mutex
	paths    []string
	failure  error
	script   func(int, *http.Request) ([]byte, bool)
}

func newRawInspectorServer(t *testing.T) *rawInspectorServer {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &rawInspectorServer{listener: ln, done: make(chan struct{}), stop: make(chan struct{})}
	t.Cleanup(func() {
		close(s.stop)
		s.listener.Close()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Error("raw server not collected")
		}
		s.mu.Lock()
		err := s.failure
		s.mu.Unlock()
		if err != nil {
			t.Error("raw fixture:", err)
		}
	})
	return s
}
func (s *rawInspectorServer) start(t *testing.T, secret string) {
	t.Helper()
	go func() {
		defer close(s.done)
		for {
			conn, err := s.listener.AcceptTCP()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			reader := bufio.NewReader(conn)
			for {
				request, err := http.ReadRequest(reader)
				if err != nil {
					break
				}
				request.Body.Close()
				s.requests.Add(1)
				s.mu.Lock()
				index := len(s.paths)
				s.paths = append(s.paths, request.URL.Path)
				if request.Method != "GET" || request.Header.Get("Authorization") != "Bearer "+secret || request.Host != s.listener.Addr().String() {
					s.failure = errors.New("unexpected synthetic transport request")
				}
				s.mu.Unlock()
				response, closeAfter := s.script(index, request)
				if response == nil {
					select {
					case <-s.stop:
					case <-time.After(250 * time.Millisecond):
					}
					break
				}
				if _, err := conn.Write(response); err != nil {
					break
				}
				if closeAfter {
					break
				}
			}
			conn.Close()
		}
	}()
}
func (s *rawInspectorServer) dial(t *testing.T) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTCP("tcp4", nil, s.listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}
func responseBytes(body []byte) []byte {
	return append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(body))), body...)
}
func inspectorResponses(t *testing.T, b inspectorBlueprint) [][]byte {
	t.Helper()
	return [][]byte{responseBytes(inspectorJSON(t, inspectorStatus(t, b, "closed"))), responseBytes(inspectorJSON(t, b.snapshot)), responseBytes(inspectorJSON(t, inspectorStatus(t, b, "closed")))}
}
func TestMihomoInspectorSameConnectionAndReadonlyStore(t *testing.T) {
	server := newRawInspectorServer(t)
	f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
	responses := inspectorResponses(t, f.blueprint)
	responses[2] = responseBytes(inspectorJSON(t, inspectorStatus(t, f.blueprint, "open")))
	server.script = func(index int, q *http.Request) ([]byte, bool) {
		if index >= len(responses) {
			return nil, true
		}
		return responses[index], false
	}
	server.start(t, f.blueprint.secret)
	conn := server.dial(t)
	f.store.Close()
	reader, err := config.OpenStore(f.paths, false)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reader
	f.controller, err = NewController(reader, f.blueprint.record)
	if err != nil {
		t.Fatal(err)
	}
	before := inspectorTree(t, f.paths.State)
	observation, err := f.controller.Inspect(inspectorContext(t), conn, f.blueprint.intent)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Gate != "open" || observation.RunID != f.blueprint.intent.RunID || observation.ConfigSHA256 != f.blueprint.intent.ConfigSHA256 {
		t.Fatal("last fresh status not returned")
	}
	server.mu.Lock()
	paths := append([]string(nil), server.paths...)
	server.mu.Unlock()
	if !reflect.DeepEqual(paths, []string{"/hoopa/v1/status", "/hoopa/v1/snapshot", "/hoopa/v1/status"}) || server.accepted.Load() != 1 {
		t.Fatal("connection sequence/retry changed:", paths)
	}
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("Inspector changed readonly tree/mtime")
	}
}

func TestMihomoInspectorRejectsBindingsAndSnapshots(t *testing.T) {
	for _, kind := range []string{"run", "generation", "config", "binary-version", "snapshot-digest", "actual-snapshot", "final-status", "draining", "degraded"} {
		t.Run(kind, func(t *testing.T) {
			server := newRawInspectorServer(t)
			f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
			responses := inspectorResponses(t, f.blueprint)
			first := inspectorStatus(t, f.blueprint, "closed")
			switch kind {
			case "run":
				first.RunID = "run:wrong"
			case "generation":
				first.ConfigGeneration = "generation:wrong"
			case "config":
				first.ConfigSHA256 = strings.Repeat("f", 64)
			case "binary-version":
				first.BinaryVersion = "wrong"
			case "snapshot-digest":
				first.SnapshotSHA256 = strings.Repeat("f", 64)
			case "actual-snapshot":
				actual := f.blueprint.snapshot
				actual.Rules = append([]wire.Rule(nil), actual.Rules...)
				actual.Rules[0].Target = "node:b"
				responses[1] = responseBytes(inspectorJSON(t, actual))
			case "final-status":
				last := first
				last.RunID = "run:wrong"
				responses[2] = responseBytes(inspectorJSON(t, last))
			case "draining", "degraded":
				responses[2] = responseBytes(inspectorJSON(t, inspectorStatus(t, f.blueprint, kind)))
			}
			responses[0] = responseBytes(inspectorJSON(t, first))
			server.script = func(index int, q *http.Request) ([]byte, bool) {
				if index >= 3 {
					return nil, true
				}
				return responses[index], false
			}
			server.start(t, f.blueprint.secret)
			conn := server.dial(t)
			before := inspectorTree(t, f.paths.State)
			_, err := f.controller.Inspect(inspectorContext(t), conn, f.blueprint.intent)
			if !errors.Is(err, ErrController) {
				t.Fatal("bad observation accepted:", err)
			}
			if server.accepted.Load() != 1 {
				t.Fatal("reconnected after mismatch")
			}
			if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
				t.Fatal("rejection wrote store")
			}
		})
	}
}

func TestMihomoInspectorHTTPBoundaries(t *testing.T) {
	for _, kind := range []string{"redirect", "unauthorized", "HTTP1.0", "connection-close", "missing-content-length", "zero-content-length", "oversized-body", "understated-length", "truncated-body", "wrong-content-type", "duplicate-content-type", "chunked", "trailer-declaration", "gzip", "duplicate-encoding", "header-overflow", "single-line-overflow", "truncated-header", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			server := newRawInspectorServer(t)
			f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
			body := inspectorJSON(t, inspectorStatus(t, f.blueprint, "closed"))
			base := responseBytes(body)
			response := base
			closeAfter := false
			switch kind {
			case "redirect":
				response = bytes.Replace(base, []byte("200 OK"), []byte("302 Found"), 1)
			case "unauthorized":
				response = bytes.Replace(base, []byte("200 OK"), []byte("401 Unauthorized"), 1)
			case "HTTP1.0":
				response = bytes.Replace(base, []byte("HTTP/1.1"), []byte("HTTP/1.0"), 1)
			case "connection-close":
				response = bytes.Replace(base, []byte("\r\n\r\n"), []byte("\r\nConnection: close\r\n\r\n"), 1)
			case "missing-content-length":
				response = append([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n"), body...)
				closeAfter = true
			case "zero-content-length":
				response = []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 0\r\n\r\n")
			case "oversized-body":
				response = []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 65537\r\n\r\n")
			case "understated-length":
				response = append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n", len(body)-1)), body...)
			case "truncated-body":
				response = base[:len(base)-5]
				closeAfter = true
			case "wrong-content-type":
				response = bytes.Replace(base, []byte("application/json"), []byte("text/plain"), 1)
			case "duplicate-content-type":
				response = bytes.Replace(base, []byte("Content-Type: application/json\r\n"), []byte("Content-Type: application/json\r\nContent-Type: application/json\r\n"), 1)
			case "chunked":
				response = []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", len(body), body))
			case "trailer-declaration":
				response = bytes.Replace(base, []byte("\r\n\r\n"), []byte("\r\nTrailer: X-End\r\n\r\n"), 1)
			case "gzip":
				response = bytes.Replace(base, []byte("\r\n\r\n"), []byte("\r\nContent-Encoding: gzip\r\n\r\n"), 1)
			case "duplicate-encoding":
				response = bytes.Replace(base, []byte("\r\n\r\n"), []byte("\r\nContent-Encoding:\r\nContent-Encoding: gzip\r\n\r\n"), 1)
			case "header-overflow":
				prefix := []byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n", len(body)))
				for i := 0; i < 90; i++ {
					prefix = append(prefix, []byte("X-Pad: "+strings.Repeat("x", 100)+"\r\n")...)
				}
				response = append(append(prefix, []byte("\r\n")...), body...)
			case "single-line-overflow":
				response = append([]byte("HTTP/1.1 200 OK\r\nX-Pad: "+strings.Repeat("x", 8193)+"\r\n\r\n"), body...)
			case "truncated-header":
				response = []byte("HTTP/1.1 200 OK\r\nContent-Len")
				closeAfter = true
			case "timeout":
				response = nil
			}
			server.script = func(index int, q *http.Request) ([]byte, bool) { return response, closeAfter }
			server.start(t, f.blueprint.secret)
			conn := server.dial(t)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := f.controller.Inspect(ctx, conn, f.blueprint.intent)
			if !errors.Is(err, ErrController) {
				t.Fatal("invalid HTTP response accepted:", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("header/body/ctx rejection was unbounded")
			}
			if strings.Contains(err.Error(), f.blueprint.secret) || strings.Contains(err.Error(), string(body)) {
				t.Fatal("transport error leaked token/body")
			}
			if server.accepted.Load() != 1 {
				t.Fatal("transport retried/dialed")
			}
			if server.requests.Load() != 1 {
				t.Fatal("rejected response triggered another GET:", server.requests.Load())
			}
		})
	}
}

func TestMihomoInspectorExactHTTPBounds(t *testing.T) {
	for _, kind := range []string{"header-8192", "body-65536"} {
		t.Run(kind, func(t *testing.T) {
			server := newRawInspectorServer(t)
			f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
			responses := inspectorResponses(t, f.blueprint)
			body := inspectorJSON(t, inspectorStatus(t, f.blueprint, "closed"))
			if kind == "body-65536" {
				body = append(body, bytes.Repeat([]byte(" "), 65536-len(body))...)
				responses[0] = responseBytes(body)
			} else {
				prefix := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nX-Pad: ", len(body))
				header := prefix + strings.Repeat("x", 8192-len(prefix)-4) + "\r\n\r\n"
				if len(header) != 8192 {
					t.Fatal("fixture header size")
				}
				responses[0] = append([]byte(header), body...)
			}
			server.script = func(index int, q *http.Request) ([]byte, bool) {
				if index >= 3 {
					return nil, true
				}
				return responses[index], false
			}
			server.start(t, f.blueprint.secret)
			conn := server.dial(t)
			if observation, err := f.controller.Inspect(inspectorContext(t), conn, f.blueprint.intent); err != nil || observation.Gate != "closed" {
				t.Fatal("exact HTTP bound rejected:", err)
			}
		})
	}
}

func TestMihomoInspectorBufferedExtraResponseRegression(t *testing.T) {
	server := newRawInspectorServer(t)
	f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
	responses := inspectorResponses(t, f.blueprint)
	preload := append(append(append([]byte(nil), responses[0]...), responses[1]...), responses[2]...)
	// Deterministically preload the ORIGINAL shared bufio Reader, then enter the
	// production inspectSession. This regresses the outer Buffered check even if
	// the OS splits a write; no timing or packet-coalescing assumption is needed.
	preloaded := make(chan struct{})
	done := make(chan struct{})
	requests := atomic.Int32{}
	go func() {
		defer close(done)
		conn, err := server.listener.AcceptTCP()
		if err != nil {
			return
		}
		server.accepted.Add(1)
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		conn.Write(preload)
		close(preloaded)
		reader := bufio.NewReader(conn)
		for {
			q, err := http.ReadRequest(reader)
			if err != nil {
				return
			}
			requests.Add(1)
			q.Body.Close()
		}
	}()
	// The generic server's unused done is satisfied by the dedicated peer.
	go func() { <-done; close(server.done) }()
	conn := server.dial(t)
	<-preloaded
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, cleanup, err := beginControllerSession(ctx, conn, f.blueprint.intent.Controller, f.blueprint.secret)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := session.reader.Peek(len(preload)); err != nil {
		t.Fatal("fixture preload:", err)
	}
	material, err := f.controller.material(f.blueprint.intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.controller.inspectSession(session, f.blueprint.intent, material); !errors.Is(err, ErrController) {
		t.Fatal("shared pre-read responses accepted")
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("preload peer not collected")
	}
	if requests.Load() != 1 || server.accepted.Load() != 1 {
		t.Fatal("extra buffered response authorized another GET/reconnect:", requests.Load())
	}
}

func TestMihomoInspectorEarlyCancellationAndMaterialPreflight(t *testing.T) {
	server := newRawInspectorServer(t)
	f := inspectorPublish(t, inspectorBlueprintFor(t, server.listener.Addr().String(), config.BinaryReference{}, nil))
	server.script = func(index int, q *http.Request) ([]byte, bool) { return nil, true }
	server.start(t, f.blueprint.secret)
	conn := server.dial(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := inspectorTree(t, f.paths.State)
	if _, err := f.controller.Inspect(ctx, conn, f.blueprint.intent); !errors.Is(err, ErrController) {
		t.Fatal("cancelled context accepted")
	}
	wrong := f.blueprint.intent
	wrong.ManifestSHA256 = strings.Repeat("f", 64)
	if _, err := f.controller.Inspect(inspectorContext(t), conn, wrong); err == nil {
		t.Fatal("unbound material accepted")
	}
	if _, err := f.controller.Inspect(nil, conn, f.blueprint.intent); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := f.controller.Inspect(context.Background(), nil, f.blueprint.intent); err == nil {
		t.Fatal("nil connection accepted")
	}
	conn.Close()
	if !reflect.DeepEqual(before, inspectorTree(t, f.paths.State)) {
		t.Fatal("preflight mutated store")
	}
	if server.requests.Load() != 0 {
		t.Fatal("cancel/material rejection emitted request")
	}
}

// This complements the shared-reader test: the complete extra tail is pulled
// into the inner HTTP parser buffer, leaving the original reader empty.
func TestMihomoInspectorInnerBufferedTailRegression(t *testing.T) {
	server := newRawInspectorServer(t)
	payload := append(responseBytes([]byte("{}")), []byte("extra")...)
	preloaded := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := server.listener.AcceptTCP()
		if err != nil {
			return
		}
		defer conn.Close()
		server.accepted.Add(1)
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		conn.Write(payload)
		close(preloaded)
		io.Copy(io.Discard, conn)
	}()
	go func() { <-done; close(server.done) }()
	conn := server.dial(t)
	<-preloaded
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, cleanup, err := beginControllerSession(ctx, conn, server.listener.Addr().String(), strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := session.reader.Peek(len(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.response(&http.Request{Method: "GET"}, 200); !errors.Is(err, ErrController) {
		t.Fatal("inner pre-read tail accepted")
	}
	if session.reader.Buffered() != 0 {
		t.Fatal("fixture did not isolate the inner-buffer check")
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inner-tail peer not collected")
	}
}
