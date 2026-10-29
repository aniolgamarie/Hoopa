package subscription

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"hoopa/internal/config"
)

var ErrFetch = errors.New("SUBSCRIPTION_FETCH_FAILED")
var ErrFetchSource = errors.New("SUBSCRIPTION_SOURCE_INVALID")

// FetchOptions is a trusted caller policy, never a provider-supplied object.
// A custom Transport must honor request cancellation; its lifetime belongs to
// its caller. The default transport does not implicitly use shell proxy vars.
type FetchOptions struct {
	Timeout   time.Duration
	MaxBytes  int
	Transport http.RoundTripper
}

// Fetch reads a single explicitly selected source without publishing anything.
// Both encoded and decoded bodies are bounded. A successful fetch is not a
// validated snapshot, and it never authorizes replacement of a running config.
func Fetch(ctx context.Context, source config.SubscriptionSource, options FetchOptions) ([]byte, error) {
	ctx, cancel, options, err := fetchContext(ctx, options)
	if err != nil {
		return nil, err
	}
	defer cancel()
	var raw []byte
	switch source.Kind {
	case "file":
		if !fetchLocalPath(source.Path) || source.URLSecretRef != "" {
			return nil, errors.Join(ErrFetch, ErrFetchSource)
		}
		raw, err = readFetchFile(ctx, source.Path, options.MaxBytes, false)
	case "https":
		if !fetchLocalPath(source.URLSecretRef) || source.Path != "" {
			return nil, errors.Join(ErrFetch, ErrFetchSource)
		}
		var secret []byte
		secret, err = readFetchFile(ctx, source.URLSecretRef, 16<<10, true)
		if err != nil {
			break
		}
		var endpoint *url.URL
		endpoint, err = fetchURL(strings.TrimSpace(string(secret)))
		if err != nil {
			break
		}
		raw, err = fetchHTTPS(ctx, endpoint, options)
	default:
		err = ErrFetchSource
	}
	if err = fetchError(ctx, err); err != nil {
		return nil, err
	}
	return raw, nil
}

// fetchContext gives both acquisition APIs the same single overall timeout and
// trusted size policy. Standalone Fetch retains its caller-relative path rules.
func fetchContext(ctx context.Context, options FetchOptions) (context.Context, context.CancelFunc, FetchOptions, error) {
	if ctx == nil || options.Timeout < 0 || options.MaxBytes < 0 || options.MaxBytes == int(^uint(0)>>1) {
		return nil, nil, options, errors.Join(ErrFetch, ErrFetchSource)
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	return ctx, cancel, options, nil
}

// Only bounded classifications cross the boundary, never PathError, url.Error,
// response bodies, certificates, URL credentials or arbitrary transport chains.
func fetchError(ctx context.Context, err error) error {
	if err == nil && ctx.Err() == nil {
		return nil
	}
	causes := []error{ErrFetch}
	for _, safe := range []error{ErrInputLimit, ErrFetchSource} {
		if errors.Is(err, safe) {
			causes = append(causes, safe)
		}
	}
	if ctx.Err() != nil {
		causes = append(causes, ctx.Err())
	}
	return errors.Join(causes...)
}

func fetchLocalPath(s string) bool {
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\r\n") {
		return false
	}
	// Paths are not URL-decoded: literal percent and question-mark characters
	// remain local filename bytes. URI scheme prefixes and URI-shaped ://
	// spellings are excluded consistently with the strict Intent boundary.
	if colon := strings.IndexByte(s, ':'); colon >= 0 {
		isScheme := colon > 0
		for i, c := range []byte(s[:colon]) {
			letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
			if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-')) {
				isScheme = false
			}
		}
		if isScheme {
			return false
		}
	}
	return !strings.Contains(s, "://")
}

func fetchURL(s string) (*url.URL, error) {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil, ErrFetchSource
		}
	}
	u, err := url.Parse(s)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" || u.User != nil || strings.Contains(s, "#") || u.Hostname() == "" {
		return nil, ErrFetchSource
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || ip.Is6() != strings.HasPrefix(u.Host, "[") {
			return nil, ErrFetchSource
		}
		host = ip.String()
	} else {
		if strings.HasPrefix(u.Host, "[") || len(host) > 254 {
			return nil, ErrFetchSource
		}
		host = strings.TrimSuffix(strings.ToLower(host), ".")
		if len(host) > 253 {
			return nil, ErrFetchSource
		}
		numericDotted := strings.Contains(host, ".")
		for _, c := range host {
			if c != '.' && (c < '0' || c > '9') {
				numericDotted = false
			}
		}
		if numericDotted {
			return nil, ErrFetchSource
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, ErrFetchSource
			}
			for _, c := range []byte(label) {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, ErrFetchSource
				}
			}
		}
	}
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return nil, ErrFetchSource
	}
	if port == "" {
		port = "443"
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return nil, ErrFetchSource
	}
	u.Scheme = "https"
	u.Host = net.JoinHostPort(host, strconv.FormatUint(n, 10))
	return u, nil
}

func fetchHTTPS(ctx context.Context, endpoint *url.URL, options FetchOptions) ([]byte, error) {
	transport := options.Transport
	if transport == nil {
		// Own this transport rather than trusting mutable DefaultTransport or
		// ambient proxy settings to select a credential-bearing request's path.
		t := &http.Transport{
			DialContext:            (&net.Dialer{Timeout: options.Timeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:      true,
			DisableCompression:     true,
			MaxIdleConns:           2,
			IdleConnTimeout:        30 * time.Second,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  options.Timeout,
			MaxResponseHeaderBytes: 64 << 10,
		}
		defer t.CloseIdleConnections()
		transport = t
	}
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		candidate, err := fetchURL(req.URL.String())
		if err != nil || len(via) > 3 || candidate.Host != endpoint.Host {
			return ErrFetchSource
		}
		req.URL = candidate
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrFetchSource
	}
	req.Header.Set("Accept-Encoding", "gzip")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrFetch
	}
	if response.ContentLength > int64(options.MaxBytes) {
		return nil, ErrInputLimit
	}
	// A limited encoded stream also bounds uncompressible input and trailing
	// gzip members, rather than only limiting the expanded output.
	encoded := &io.LimitedReader{R: response.Body, N: int64(options.MaxBytes) + 1}
	var reader io.Reader = encoded
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		z, err := gzip.NewReader(encoded)
		if err != nil {
			if encoded.N == 0 {
				return nil, ErrInputLimit
			}
			return nil, ErrFetch
		}
		defer z.Close()
		reader = z
	default:
		return nil, ErrFetchSource
	}
	raw, err := fetchRead(ctx, reader, options.MaxBytes)
	if encoded.N == 0 {
		return nil, ErrInputLimit
	}
	return raw, err
}

type fetchContextReader struct {
	ctx context.Context
	io.Reader
}

func (r fetchContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func fetchRead(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(fetchContextReader{ctx, reader}, int64(limit)+1))
	if len(raw) > limit {
		return nil, ErrInputLimit
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return raw, nil
}
