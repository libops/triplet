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
	"time"
)

// ErrNotFound is returned by Opener implementations when no asset exists for
// the given identifier. Handlers translate this to HTTP 404.
var ErrNotFound = errors.New("storage: not found")

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
