// Package cache stores and retrieves bytes keyed by an opaque string.
//
// Two cache layers exist:
//   - Source cache wraps an [storage.Opener] and memoises remote fetches by
//     identifier.
//   - Derivative cache stores encoded IIIF responses keyed by
//     (identifier, region, size, rotation, quality, format).
//
// Both layers use the same [Store] interface so backends (file, GCS) are
// interchangeable.
package cache

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrMiss is returned by Get when no entry exists for the key.
var ErrMiss = errors.New("cache: miss")

// Entry metadata returned alongside cached bytes.
type Entry struct {
	ContentType string
	Size        int64
	StoredAt    time.Time
}

// Store is the backend interface. Implementations must be safe for
// concurrent use.
type Store interface {
	// Get returns a reader over the cached bytes for key, plus metadata.
	// Returns ErrMiss when the key is not present.
	Get(ctx context.Context, key string) (io.ReadCloser, Entry, error)

	// Put writes value's contents under key with the supplied content type.
	// Implementations stream value through to the backend.
	Put(ctx context.Context, key, contentType string, value io.Reader) error

	// Delete removes key. Missing keys are not an error.
	Delete(ctx context.Context, key string) error
}

// Noop is a Store that always misses and silently drops Put.
type Noop struct{}

// Get always returns ErrMiss.
func (Noop) Get(context.Context, string) (io.ReadCloser, Entry, error) {
	return nil, Entry{}, ErrMiss
}

// Put discards the value.
func (Noop) Put(_ context.Context, _, _ string, value io.Reader) error {
	_, err := io.Copy(io.Discard, value)
	return err
}

// Delete is a no-op.
func (Noop) Delete(context.Context, string) error { return nil }
