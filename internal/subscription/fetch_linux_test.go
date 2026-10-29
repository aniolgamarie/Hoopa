//go:build linux

package subscription

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hoopa/internal/config"
)

func fetchSecret(t *testing.T, endpoint string) config.SubscriptionSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "url-secret")
	if err := os.WriteFile(path, []byte(endpoint+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return config.SubscriptionSource{Kind: "https", URLSecretRef: path}
}

func fetchRequireError(t *testing.T, ctx context.Context, source config.SubscriptionSource, opts FetchOptions, kind error) {
	t.Helper()
	raw, err := Fetch(ctx, source, opts)
	if raw != nil || !errors.Is(err, ErrFetch) || !errors.Is(err, kind) {
		t.Fatal("fetch returned partial material or wrong error classification")
	}
	for _, secret := range []string{"synthetic-private-query-19", source.URLSecretRef, source.Path} {
		if secret != "" && strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), secret) {
			t.Fatal("source material leaked into error")
		}
	}
}

func TestFetchFileBoundsAndNoPublication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.yaml")
	content := []byte("proxies: []\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	source := config.SubscriptionSource{Kind: "file", Path: path}
	raw, err := Fetch(context.Background(), source, FetchOptions{MaxBytes: len(content)})
	if err != nil || !bytes.Equal(raw, content) {
		t.Fatal("bounded explicit file fetch failed")
	}
	raw[0] = 'X'
	got, _ := os.ReadFile(path)
	entries, _ := os.ReadDir(dir)
	if !bytes.Equal(got, content) || len(entries) != 1 {
		t.Fatal("fetch published or mutated source")
	}
	fetchRequireError(t, context.Background(), source, FetchOptions{MaxBytes: len(content) - 1}, ErrInputLimit)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	fetchRequireError(t, cancelled, source, FetchOptions{}, context.Canceled)
	for _, bad := range []config.SubscriptionSource{
		{Kind: "file", Path: path, URLSecretRef: "extra"}, {Kind: "file", Path: "https://inline.invalid/?synthetic-private-query-19"},
		{Kind: "file"}, {Kind: "other", Path: path}, {Kind: "https", Path: path, URLSecretRef: path},
	} {
		fetchRequireError(t, context.Background(), bad, FetchOptions{}, ErrFetchSource)
	}
	for _, opts := range []FetchOptions{{MaxBytes: -1}, {Timeout: -1}, {MaxBytes: int(^uint(0) >> 1)}} {
		fetchRequireError(t, context.Background(), source, opts, ErrFetchSource)
	}
	fetchRequireError(t, nil, source, FetchOptions{}, ErrFetchSource)
	percentPath := filepath.Join(dir, "literal%file?.yaml")
	if err := os.WriteFile(percentPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := Fetch(context.Background(), config.SubscriptionSource{Kind: "file", Path: percentPath}, FetchOptions{}); err != nil || !bytes.Equal(raw, content) {
		t.Fatal("local path was treated as URL encoding")
	}
}

func TestFetchFileTypeAndPrivateReference(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "secret")
	if err := os.WriteFile(regular, []byte("https://example.invalid/?synthetic-private-query-19"), 0644); err != nil {
		t.Fatal(err)
	}
	fetchRequireError(t, context.Background(), config.SubscriptionSource{Kind: "https", URLSecretRef: regular}, FetchOptions{}, ErrFetchSource)
	if err := os.Chmod(regular, 0600); err != nil {
		t.Fatal(err)
	}
	link, fifo, hard := filepath.Join(dir, "link"), filepath.Join(dir, "fifo"), filepath.Join(dir, "hard")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(regular, hard); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, fifo, dir} {
		for _, kind := range []string{"file", "https"} {
			source := config.SubscriptionSource{Kind: kind}
			if kind == "file" {
				source.Path = path
			} else {
				source.URLSecretRef = path
			}
			fetchRequireError(t, context.Background(), source, FetchOptions{Timeout: time.Second}, ErrFetchSource)
		}
	}
	fetchRequireError(t, context.Background(), config.SubscriptionSource{Kind: "https", URLSecretRef: hard}, FetchOptions{}, ErrFetchSource)
	if err := os.Remove(hard); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regular, bytes.Repeat([]byte("x"), (16<<10)+1), 0600); err != nil {
		t.Fatal(err)
	}
	fetchRequireError(t, context.Background(), config.SubscriptionSource{Kind: "https", URLSecretRef: regular}, FetchOptions{}, ErrInputLimit)
}

func TestFetchHTTPSBodiesRedirectsAndErrors(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
	defer foreign.Close()
	content := bytes.Repeat([]byte("a"), 128)
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, _ = z.Write(content)
	_ = z.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain":
			_, _ = w.Write(content)
		case "/gzip", "/corrupt", "/truncated":
			w.Header().Set("Content-Encoding", "gzip")
			body := bytes.Clone(compressed.Bytes())
			if r.URL.Path == "/corrupt" {
				body[len(body)-1] ^= 1
			}
			if r.URL.Path == "/truncated" {
				body = body[:len(body)-3]
			}
			_, _ = w.Write(body)
		case "/redirect":
			http.Redirect(w, r, "/plain", http.StatusFound)
		case "/foreign":
			http.Redirect(w, r, foreign.URL+"/?synthetic-private-query-19", http.StatusFound)
		case "/http":
			http.Redirect(w, r, "http://"+r.Host+"/plain", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/encoding":
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write(content)
		case "/chunked-encoded-limit":
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			z := gzip.NewWriter(w)
			var incompressible [256]byte
			for i := range incompressible {
				incompressible[i] = byte(i)
			}
			_, _ = z.Write(incompressible[:])
			_ = z.Close()
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "synthetic-private-query-19")
		}
	}))
	defer server.Close()
	opts := FetchOptions{MaxBytes: 128, Transport: server.Client().Transport}
	for _, path := range []string{"/plain", "/gzip", "/redirect"} {
		t.Run(path, func(t *testing.T) {
			raw, err := Fetch(context.Background(), fetchSecret(t, server.URL+path+"?token=synthetic-private-query-19"), opts)
			if err != nil || !bytes.Equal(raw, content) {
				t.Fatal("actual TLS bounded fetch failed")
			}
		})
	}
	for _, path := range []string{"/corrupt", "/truncated", "/foreign", "/http", "/loop", "/encoding", "/forbidden"} {
		t.Run(path, func(t *testing.T) {
			fetchRequireError(t, context.Background(), fetchSecret(t, server.URL+path), opts, ErrFetch)
		})
	}
	if foreignCalls.Load() != 0 {
		t.Fatal("redirect crossed source origin")
	}
	for _, path := range []string{"/plain", "/gzip"} {
		limited := opts
		limited.MaxBytes = 127
		fetchRequireError(t, context.Background(), fetchSecret(t, server.URL+path), limited, ErrInputLimit)
	}
	fetchRequireError(t, context.Background(), fetchSecret(t, server.URL+"/chunked-encoded-limit"), opts, ErrInputLimit)
}

func TestFetchHTTPSCancellationAndDeadline(t *testing.T) {
	entered, exited := make(chan struct{}, 2), make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
		exited <- struct{}{}
	}))
	defer server.Close()
	source := fetchSecret(t, server.URL+"/?token=synthetic-private-query-19")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		raw, err := Fetch(ctx, source, FetchOptions{Transport: server.Client().Transport})
		if raw != nil {
			err = errors.New("partial bytes")
		}
		done <- err
	}()
	guard, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	select {
	case <-entered:
	case <-guard.Done():
		t.Fatal("request did not enter TLS handler")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrFetch) || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation classification lost")
		}
	case <-guard.Done():
		t.Fatal("fetch ignored cancellation")
	}
	select {
	case <-exited:
	case <-guard.Done():
		t.Fatal("request remained live after cancellation")
	}
	fetchRequireError(t, guard, source, FetchOptions{Transport: server.Client().Transport, Timeout: 100 * time.Millisecond}, context.DeadlineExceeded)
}

func TestFetchURLValidation(t *testing.T) {
	for _, endpoint := range []string{"http://example.test/", "https://user:pass@example.test/", "https://example.test/#", "https://example.test:0/", "https://example.test:65536/", "https://example.test:/", "https://[not-ip]/", "https://[fe80::1%25eth0]/", "https://bad_host/", "https://example.test../", "https://999.999.999.999/", "https://example.test/a\nb", "/relative"} {
		fetchRequireError(t, context.Background(), fetchSecret(t, endpoint), FetchOptions{}, ErrFetchSource)
	}
	for _, endpoint := range []string{"https://example.test/", "HTTPS://EXAMPLE.TEST.:443/path?q=secret", "https://127.0.0.1:1234/", "https://[::1]:65535/"} {
		if _, err := fetchURL(endpoint); err != nil {
			t.Fatal("valid HTTPS authority rejected")
		}
	}
}

func TestFetchDefaultTLSAndTransportIsolation(t *testing.T) {
	var requests, proxyCalls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("ok")) }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); w.WriteHeader(http.StatusBadGateway) }))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	// Deliberately poison the mutable default. Product Fetch must construct its
	// own verified transport, while caller-supplied trusted roots remain usable.
	previous := http.DefaultTransport
	unsafeDefault := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // test-only negative witness
	http.DefaultTransport = unsafeDefault
	defer func() { http.DefaultTransport = previous; unsafeDefault.CloseIdleConnections() }()
	source := fetchSecret(t, server.URL)
	fetchRequireError(t, context.Background(), source, FetchOptions{}, ErrFetch)
	if requests.Load() != 0 || proxyCalls.Load() != 0 {
		t.Fatal("default fetch used ambient TLS trust or proxy")
	}
	raw, err := Fetch(context.Background(), source, FetchOptions{Transport: server.Client().Transport})
	if err != nil || string(raw) != "ok" || requests.Load() != 1 {
		t.Fatal("explicit certificate-trusted transport failed")
	}
}
