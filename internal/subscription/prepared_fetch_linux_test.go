//go:build linux

package subscription

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

func preparedMake(t *testing.T, source config.SubscriptionSource) PreparedFetchSource {
	t.Helper()
	p, err := PrepareFetchSource(context.Background(), source)
	if err != nil || p.Digest() == "" {
		t.Fatal("prepare failed", err)
	}
	return p
}
func preparedInvalid(t *testing.T, ctx context.Context, source config.SubscriptionSource, want error) {
	t.Helper()
	p, err := PrepareFetchSource(ctx, source)
	if !reflect.DeepEqual(p, PreparedFetchSource{}) || !errors.Is(err, ErrFetch) || !errors.Is(err, want) {
		t.Fatal("prepare returned partial input/wrong error", err)
	}
	for _, private := range []string{source.Path, source.URLSecretRef, "synthetic-private-query-19"} {
		if private != "" && strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), private) {
			t.Fatal("prepare error exposed material")
		}
	}
}
func preparedFetchInvalid(t *testing.T, ctx context.Context, p PreparedFetchSource, options FetchOptions, want error) {
	t.Helper()
	b, err := FetchPrepared(ctx, p, options)
	if b != nil || !errors.Is(err, ErrFetch) || !errors.Is(err, want) {
		t.Fatal("prepared fetch returned partial body/wrong error", err)
	}
	for _, private := range []string{p.source.Path, p.source.URLSecretRef, p.endpoint, p.secret, "synthetic-private-query-19"} {
		if private != "" && strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), private) {
			t.Fatal("fetch error exposed material")
		}
	}
}
func TestPreparedFileGoldenDefinitionAndCurrentBody(t *testing.T) {
	// Independent Python stdlib json compact encoding + hashlib.sha256 vector.
	p := preparedMake(t, config.SubscriptionSource{Kind: "file", Path: "/private/providers/feed.yaml"})
	if p.Digest() != "23ca163dcd79ffe917ecefa319bbebbb73b99bbd938c527084ccda8724238478" {
		t.Fatal("file/empty-byte-secret encoding changed")
	}
	path := filepath.Join(t.TempDir(), "literal%provider?.yaml")
	source := config.SubscriptionSource{Kind: "file", Path: path}
	p = preparedMake(t, source)
	copyValue := p
	// No body must exist to prepare a file definition.
	preparedFetchInvalid(t, context.Background(), p, FetchOptions{}, ErrFetchSource)
	first := []byte("proxies: []\n")
	if err := os.WriteFile(path, first, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := FetchPrepared(context.Background(), p, FetchOptions{})
	if err != nil || !bytes.Equal(got, first) {
		t.Fatal("initial body", err)
	}
	got[0] = '!'
	second := []byte(`{"proxies":[]}`)
	if err := os.WriteFile(path, second, 0644); err != nil {
		t.Fatal(err)
	}
	fresh := preparedMake(t, source)
	if fresh.Digest() != p.Digest() {
		t.Fatal("provider body incorrectly bound as frozen definition")
	}
	source.Path = filepath.Join(t.TempDir(), "other")
	if copyValue.Digest() != p.Digest() {
		t.Fatal("value copy mutated")
	}
	got, err = FetchPrepared(context.Background(), copyValue, FetchOptions{MaxBytes: len(second)})
	if err != nil || !bytes.Equal(got, second) {
		t.Fatal("prepared file did not read current bytes", err)
	}
	preparedFetchInvalid(t, context.Background(), p, FetchOptions{MaxBytes: len(second) - 1}, ErrInputLimit)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("fetch wrote files", err)
	}
}

func TestPreparedBoundaryKeepsStandaloneRelativeFetch(t *testing.T) {
	t.Chdir(t.TempDir())
	content := []byte("proxies: []\n")
	if err := os.WriteFile("provider.yaml", content, 0600); err != nil {
		t.Fatal(err)
	}
	source := config.SubscriptionSource{Kind: "file", Path: "provider.yaml"}
	preparedInvalid(t, context.Background(), source, ErrFetchSource)
	got, err := Fetch(context.Background(), source, FetchOptions{})
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("standalone relative fetch changed", err)
	}
}

func TestPreparedHTTPSFreezesEndpointAndExactSecret(t *testing.T) {
	var aCalls, bCalls atomic.Int32
	a := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aCalls.Add(1)
		if r.URL.Query().Get("token") != "synthetic-private-query-19" {
			t.Error("frozen query changed")
		}
		io.WriteString(w, "from-A")
	}))
	defer a.Close()
	b := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { bCalls.Add(1); io.WriteString(w, "from-B") }))
	defer b.Close()
	source := fetchSecret(t, a.URL+"/?token=synthetic-private-query-19")
	p := preparedMake(t, source)
	copyValue := p
	if aCalls.Load() != 0 || bCalls.Load() != 0 {
		t.Fatal("prepare performed network")
	}
	// Identical bytes replacing the inode are the same semantic frozen input.
	secret, err := os.ReadFile(source.URLSecretRef)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(source.URLSecretRef); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source.URLSecretRef, secret, 0600); err != nil {
		t.Fatal(err)
	}
	if preparedMake(t, source).Digest() != p.Digest() {
		t.Fatal("inode identity leaked into semantic digest")
	}
	// Whitespace is exact private input, even though the effective URL is unchanged.
	if err = os.WriteFile(source.URLSecretRef, append([]byte(" \t"), secret...), 0600); err != nil {
		t.Fatal(err)
	}
	whitespace := preparedMake(t, source)
	if whitespace.Digest() == p.Digest() || whitespace.endpoint != p.endpoint {
		t.Fatal("exact whitespace or normalized endpoint binding lost")
	}
	if err = os.WriteFile(source.URLSecretRef, []byte(b.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := preparedMake(t, source)
	if changed.Digest() == p.Digest() {
		t.Fatal("changed endpoint not bound")
	}
	for _, state := range []string{"changed", "deleted"} {
		if state == "deleted" {
			if err = os.Remove(source.URLSecretRef); err != nil {
				t.Fatal(err)
			}
		}
		got, err := FetchPrepared(context.Background(), copyValue, FetchOptions{Transport: a.Client().Transport})
		if err != nil || string(got) != "from-A" {
			t.Fatal("prepared source followed changed secret", err)
		}
	}
	preparedInvalid(t, context.Background(), source, ErrFetchSource)
	if aCalls.Load() != 2 || bCalls.Load() != 0 {
		t.Fatal("secret mutation redirected frozen request")
	}
	// A distinct fresh preparation deliberately uses B; it is not commit approval.
	got, err := FetchPrepared(context.Background(), changed, FetchOptions{Transport: b.Client().Transport})
	if err != nil || string(got) != "from-B" || bCalls.Load() != 1 {
		t.Fatal("fresh preparation ignored new endpoint", err)
	}
}
func TestPreparedCanonicalPathsSecretSafetyAndLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("HTTPS://EXAMPLE.INVALID.:00443/a?token=synthetic-private-query-19\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := config.SubscriptionSource{Kind: "https", URLSecretRef: path}
	p := preparedMake(t, source)
	if p.endpoint != "https://example.invalid:443/a?token=synthetic-private-query-19" {
		t.Fatal("URL not normalized")
	}
	for _, source := range []config.SubscriptionSource{{}, {Kind: "other", Path: path}, {Kind: "file"}, {Kind: "https"}, {Kind: "file", Path: path, URLSecretRef: path}, {Kind: "https", Path: path, URLSecretRef: path}} {
		preparedInvalid(t, context.Background(), source, ErrFetchSource)
	}
	for _, path := range []string{"relative/path", "./file", "/tmp/../secret", "/tmp//secret", "/tmp/secret/", "https://inline.invalid", "/tmp/bad\npath", "/tmp/\x00secret", "/tmp/" + string([]byte{255})} {
		for _, kind := range []string{"file", "https"} {
			s := config.SubscriptionSource{Kind: kind}
			if kind == "file" {
				s.Path = path
			} else {
				s.URLSecretRef = path
			}
			preparedInvalid(t, context.Background(), s, ErrFetchSource)
		}
	}
	for _, kind := range []string{"mode", "symlink", "hardlink", "fifo", "directory", "oversize", "invalid-url"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "secret")
			if err := os.WriteFile(path, []byte("https://example.invalid/"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := path + "-target"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+"-link"); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				path = dir
			case "oversize":
				if err := os.Truncate(path, (16<<10)+1); err != nil {
					t.Fatal(err)
				}
			case "invalid-url":
				if err := os.WriteFile(path, []byte("http://example.invalid/?token=synthetic-private-query-19"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want := ErrFetchSource
			if kind == "oversize" {
				want = ErrInputLimit
			}
			preparedInvalid(t, context.Background(), config.SubscriptionSource{Kind: "https", URLSecretRef: path}, want)
		})
	}
	// An exactly 16KiB secret can be valid and its padding remains digest input.
	padded := append([]byte("https://example.invalid/"), bytes.Repeat([]byte{' '}, (16<<10)-len("https://example.invalid/"))...)
	if err := os.WriteFile(source.URLSecretRef, padded, 0600); err != nil {
		t.Fatal(err)
	}
	preparedMake(t, source)
}
func TestPreparedZeroPrivacyCancellationAndOptions(t *testing.T) {
	var zero PreparedFetchSource
	if zero.Digest() != "" {
		t.Fatal("zero digest")
	}
	preparedFetchInvalid(t, context.Background(), zero, FetchOptions{}, ErrFetchSource)
	s := config.SubscriptionSource{Kind: "file", Path: filepath.Join(t.TempDir(), "missing")}
	preparedInvalid(t, nil, s, ErrFetchSource)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	preparedInvalid(t, ctx, s, context.Canceled)
	p := preparedMake(t, s)
	preparedFetchInvalid(t, ctx, p, FetchOptions{}, context.Canceled)
	preparedFetchInvalid(t, nil, p, FetchOptions{}, ErrFetchSource)
	for _, opts := range []FetchOptions{{Timeout: -1}, {MaxBytes: -1}, {MaxBytes: int(^uint(0) >> 1)}} {
		preparedFetchInvalid(t, context.Background(), p, opts, ErrFetchSource)
	}
	https := preparedMake(t, fetchSecret(t, "https://example.invalid/?token=synthetic-private-query-19"))
	for _, p := range []PreparedFetchSource{zero, p, https} {
		if fmt.Sprint(p) != "subscription.PreparedFetchSource{private material}" || fmt.Sprintf("%#v", p) != "subscription.PreparedFetchSource{private material}" {
			t.Fatal("format exposed source")
		}
		b, err := json.Marshal(p)
		if err != nil || string(b) != "{}" {
			t.Fatal("public fields exposed")
		}
	}
}
func TestPreparedHTTPSBoundsRedirectAndTLS(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	content := bytes.Repeat([]byte{'x'}, 128)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			w.Write(content)
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			z.Write(content)
			z.Close()
		case "/same":
			http.Redirect(w, r, "/plain", 302)
		case "/foreign":
			http.Redirect(w, r, foreign.URL+"/?token=synthetic-private-query-19", 302)
		case "/http":
			http.Redirect(w, r, "http://"+r.Host, 302)
		case "/status":
			w.WriteHeader(403)
			io.WriteString(w, "synthetic-private-query-19")
		}
	}))
	defer server.Close()
	for _, path := range []string{"/plain", "/gzip", "/same"} {
		p := preparedMake(t, fetchSecret(t, server.URL+path))
		got, err := FetchPrepared(context.Background(), p, FetchOptions{MaxBytes: 128, Transport: server.Client().Transport})
		if err != nil || !bytes.Equal(got, content) {
			t.Fatal("bounded prepared body failed", err)
		}
		preparedFetchInvalid(t, context.Background(), p, FetchOptions{MaxBytes: 127, Transport: server.Client().Transport}, ErrInputLimit)
	}
	for _, path := range []string{"/foreign", "/http", "/status"} {
		p := preparedMake(t, fetchSecret(t, server.URL+path))
		preparedFetchInvalid(t, context.Background(), p, FetchOptions{Transport: server.Client().Transport}, ErrFetch)
	}
	if foreignCalls.Load() != 0 {
		t.Fatal("cross-origin redirect sent credentials")
	}
	// Default TLS verification cannot be bypassed just by preparing an endpoint.
	p := preparedMake(t, fetchSecret(t, server.URL+"/plain"))
	preparedFetchInvalid(t, context.Background(), p, FetchOptions{}, ErrFetch)
}
func TestPreparedHTTPSCancellationAndTimeout(t *testing.T) {
	entered, exited := make(chan struct{}, 2), make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
		exited <- struct{}{}
	}))
	defer server.Close()
	p := preparedMake(t, fetchSecret(t, server.URL+"/?token=synthetic-private-query-19"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		raw, err := FetchPrepared(ctx, p, FetchOptions{Transport: server.Client().Transport})
		if raw != nil {
			err = errors.New("partial body")
		}
		done <- err
	}()
	guard, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	select {
	case <-entered:
	case <-guard.Done():
		t.Fatal("request never entered")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrFetch) {
			t.Fatal("lost cancellation", err)
		}
	case <-guard.Done():
		t.Fatal("cancel ignored")
	}
	select {
	case <-exited:
	case <-guard.Done():
		t.Fatal("cancel left request live")
	}
	preparedFetchInvalid(t, guard, p, FetchOptions{Transport: server.Client().Transport, Timeout: 100 * time.Millisecond}, context.DeadlineExceeded)
	select {
	case <-exited:
	case <-guard.Done():
		t.Fatal("timeout left request live")
	}
}

type preparedRoundTrip func(*http.Request) (*http.Response, error)

func (f preparedRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPreparedTransportErrorsStayPrivate(t *testing.T) {
	p := preparedMake(t, fetchSecret(t, "https://example.invalid/?token=synthetic-private-query-19"))
	private := errors.New("private-transport-chain")
	transport := preparedRoundTrip(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("private-transport-material: %w", private)
	})
	raw, err := FetchPrepared(context.Background(), p, FetchOptions{Transport: transport})
	if raw != nil || !errors.Is(err, ErrFetch) || errors.Is(err, private) || strings.Contains(fmt.Sprint(err), "private-transport") {
		t.Fatal("untrusted transport chain escaped")
	}
}
