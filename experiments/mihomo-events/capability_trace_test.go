//go:build linux && amd64

package mihomoevents

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"hoopa/protocol/telemetrywire"
)

// Positive trace fixture and isolated single-variable rejection cases bind the
// new entry admission to the existing v2 source; identity remains unverified.
func TestCapabilityTraceAdmission(t *testing.T) {
	if os.Getenv("HOOPA_CAPABILITY_NETWORK_TEST") != "1" {
		t.Skip("explicit protected candidate required")
	}
	h := newCapabilityHarness(t)
	f := h.fixture("gen:trace-admission")
	trace := filepath.Join(h.dir, "trace")
	if err := os.Mkdir(trace, 0700); err != nil {
		t.Fatal(err)
	}
	mapping := filepath.Join(h.dir, "route-map.json")
	entries := []qualityMapEntry{}
	for _, n := range f.cfg.Proxies {
		id := n.Name
		entries = append(entries, qualityMapEntry{InternalName: id, AdapterType: "Http", NodeID: &id, DisplayAlias: id})
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "config_generation": f.generation, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	transportPrivateWrite(t, mapping, raw, 0600)
	good := map[string]string{"HOOPA_TRACE_INBOUND": "entry-a", "HOOPA_TRACE_PROFILE": "2", "HOOPA_TRACE_GENERATION": f.generation, "HOOPA_TRACE_ROUTE_MAP": mapping, "HOOPA_TRACE_DIR": trace}
	t.Run("only-generation-mismatch", func(t *testing.T) {
		bad := map[string]string{}
		for k, v := range good {
			bad[k] = v
		}
		bad["HOOPA_TRACE_GENERATION"] = "gen:wrong"
		h.reject(t, f, bad, nil, "")
		assertNoCapabilityEpoch(t, trace)
	})
	t.Run("only-mapping-type-mismatch", func(t *testing.T) {
		badMap := filepath.Join(h.dir, "bad-map.json")
		transportPrivateWrite(t, badMap, bytes.Replace(raw, []byte(`"Http"`), []byte(`"Socks5"`), 1), 0600)
		bad := map[string]string{}
		for k, v := range good {
			bad[k] = v
		}
		bad["HOOPA_TRACE_ROUTE_MAP"] = badMap
		h.reject(t, f, bad, nil, "")
		assertNoCapabilityEpoch(t, trace)
	})
	t.Run("only-source-directory-permission", func(t *testing.T) {
		if err := os.Chmod(trace, 0750); err != nil {
			t.Fatal(err)
		}
		h.reject(t, f, good, nil, "")
		assertNoCapabilityEpoch(t, trace)
		if err := os.Chmod(trace, 0700); err != nil {
			t.Fatal(err)
		}
	})
	p := h.start(t, f, good, nil)
	h.ready(t, p)
	c, br, err := h.connect(f, 0, "alpha.test", h.echoPort, "user-a", capabilityPasswordA)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := strings.Repeat("t", 257)
	capabilityExchange(t, c, br, payload)
	if err := c.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatal("trace flow EOF", err)
	}
	c.Close()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(4 * time.Second):
		t.Fatal("trace core did not stop")
	}
	if p.err != nil {
		t.Fatal("trace core failed on normal shutdown")
	}
	names, err := filepath.Glob(filepath.Join(trace, "epoch-*.source.json"))
	if err != nil || len(names) != 1 {
		t.Fatal("expected one admitted epoch", err, len(names))
	}
	meta, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := telemetrywire.DecodeManifest(meta)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ConfigGeneration != f.generation {
		t.Fatal("manifest generation drift")
	}
	frames, err := os.ReadFile(strings.TrimSuffix(names[0], ".source.json") + ".frames")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSourceRecovery(bytes.NewReader(frames), meta); err != nil {
		t.Fatal("frozen source recovery", err)
	}
	lifecycle, err := ReadSourceLifecycle(bytes.NewReader(frames), manifest.SourceEpoch)
	if err != nil || string(lifecycle.ConfigGeneration) != f.generation || len(lifecycle.Flows) != 1 {
		t.Fatal("trace lifecycle", err)
	}
	final := lifecycle.Flows[0].Final
	if final == nil || uint64(final.UploadBytes) != 257 || uint64(final.DownloadBytes) != 257 {
		t.Fatal("trace final bytes not exact")
	}
	h.noSecrets(t, p.log)
	h.noSecrets(t, p.stdout)
	t.Logf("PASS protected trace generation/map/source admission: 3 isolated rejects, 1 real flow257B each direction; evidence=%s", h.dir)
}
func assertNoCapabilityEpoch(t *testing.T, dir string) {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "epoch-*"))
	if err != nil || len(names) != 0 {
		t.Fatal("rejected trace created epoch", err, names)
	}
}
