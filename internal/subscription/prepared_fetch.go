package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"hoopa/internal/config"
)

// PreparedFetchSource privately freezes one explicit source definition and its
// exact HTTPS secret input. It is an input binding, not provider authority or a
// proof that a later commit rechecked the source/settings identity.
type PreparedFetchSource struct {
	source   config.SubscriptionSource
	secret   string
	endpoint string
	digest   string
}

// PrepareFetchSource performs no network requests or writes. File contents are
// deliberately not read here: they are the candidate acquired by FetchPrepared.
func PrepareFetchSource(ctx context.Context, source config.SubscriptionSource) (PreparedFetchSource, error) {
	if ctx == nil {
		return PreparedFetchSource{}, errors.Join(ErrFetch, ErrFetchSource)
	}
	if ctx.Err() != nil {
		return PreparedFetchSource{}, fetchError(ctx, ctx.Err())
	}
	var secret = []byte{}
	endpoint := ""
	switch source.Kind {
	case "file":
		if !preparedPath(source.Path) || source.URLSecretRef != "" {
			return PreparedFetchSource{}, fetchError(ctx, ErrFetchSource)
		}
	case "https":
		if !preparedPath(source.URLSecretRef) || source.Path != "" {
			return PreparedFetchSource{}, fetchError(ctx, ErrFetchSource)
		}
		var err error
		secret, err = readFetchFile(ctx, source.URLSecretRef, 16<<10, true)
		if err != nil {
			return PreparedFetchSource{}, fetchError(ctx, err)
		}
		target, err := fetchURL(strings.TrimSpace(string(secret)))
		if err != nil {
			return PreparedFetchSource{}, fetchError(ctx, err)
		}
		endpoint = target.String()
	default:
		return PreparedFetchSource{}, fetchError(ctx, ErrFetchSource)
	}
	if ctx.Err() != nil {
		return PreparedFetchSource{}, fetchError(ctx, ctx.Err())
	}
	// The exact Go JSON encoding (including struct field order and base64 bytes)
	// is versioned. Empty file secret is []byte{}, encoded as "", never null.
	raw, err := json.Marshal([]any{"hoopa.fetch-source.v1", source, secret})
	if err != nil {
		return PreparedFetchSource{}, fetchError(ctx, ErrFetchSource)
	}
	sum := sha256.Sum256(raw)
	if ctx.Err() != nil {
		return PreparedFetchSource{}, fetchError(ctx, ctx.Err())
	}
	return PreparedFetchSource{source: source, secret: string(secret), endpoint: endpoint, digest: hex.EncodeToString(sum[:])}, nil
}
func preparedPath(path string) bool {
	return utf8.ValidString(path) && filepath.IsAbs(path) && filepath.Clean(path) == path && fetchLocalPath(path)
}

// FetchPrepared uses only the frozen HTTPS endpoint, even if its secret reference
// changes or disappears. File sources read their current body at execution time.
func FetchPrepared(ctx context.Context, source PreparedFetchSource, options FetchOptions) ([]byte, error) {
	ctx, cancel, options, err := fetchContext(ctx, options)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if ctx.Err() != nil {
		return nil, fetchError(ctx, ctx.Err())
	}
	if source.digest == "" {
		return nil, fetchError(ctx, ErrFetchSource)
	}
	var raw []byte
	switch source.source.Kind {
	case "file":
		raw, err = readFetchFile(ctx, source.source.Path, options.MaxBytes, false)
	case "https":
		endpoint, e := fetchURL(source.endpoint)
		if e != nil {
			return nil, fetchError(ctx, e)
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
func (s PreparedFetchSource) Digest() string { return s.digest }
func (PreparedFetchSource) String() string {
	return "subscription.PreparedFetchSource{private material}"
}
func (PreparedFetchSource) GoString() string {
	return "subscription.PreparedFetchSource{private material}"
}
