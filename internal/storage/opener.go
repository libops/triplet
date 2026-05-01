// Package storage resolves IIIF identifiers to readable image bytes.
//
// Every Image and Presentation handler routes through an [Opener]. New
// backends (HTTP, GCS, in-memory uploads) implement the same interface so
// the transform pipeline never sees backend-specific code.
package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// ErrNotFound is returned by Opener implementations when no asset exists for
// the given identifier. Handlers translate this to HTTP 404.
var ErrNotFound = errors.New("storage: not found")

// ErrForbidden is returned when a source exists but the caller is not allowed
// to read it. Handlers translate this to HTTP 403.
var ErrForbidden = errors.New("storage: forbidden")

type authHeadersContextKey struct{}

// ContextWithAuthHeaders stores the browser auth headers that storage backends
// may forward to trusted upstream authorization probes.
func ContextWithAuthHeaders(ctx context.Context, headers http.Header) context.Context {
	forwarded := http.Header{}
	for _, name := range []string{"Authorization", "Cookie"} {
		for _, value := range headers.Values(name) {
			forwarded.Add(name, value)
		}
	}
	return context.WithValue(ctx, authHeadersContextKey{}, forwarded)
}

func authHeadersFromContext(ctx context.Context) http.Header {
	headers, _ := ctx.Value(authHeadersContextKey{}).(http.Header)
	if headers == nil {
		return http.Header{}
	}
	return headers
}

// Meta describes a resolved asset. ContentType and ModTime may be zero if the
// backend cannot supply them cheaply.
type Meta struct {
	ContentType string
	Size        int64
	ModTime     time.Time
	// Version identifies the current source bytes for cache validators. It
	// should change whenever the source content changes.
	Version string
}

// Opener resolves an identifier to a seekable byte stream plus metadata.
//
// Implementations must be safe for concurrent use. Callers are responsible for
// closing the returned reader. The supplied context governs cancellation of
// any IO performed by the backend.
type Opener interface {
	Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error)
}

// MetaReader reports source metadata without requiring callers to open and
// spool the whole source body.
type MetaReader interface {
	Meta(ctx context.Context, identifier string) (Meta, error)
}

// AuthInvalidator clears cached authorization decisions for an identifier when
// a backend maintains per-source auth state.
type AuthInvalidator interface {
	InvalidateAuth(ctx context.Context, identifier string) error
}
