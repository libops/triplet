package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Multiplex routes Open calls to one of several Openers based on identifier
// pattern. Patterns are evaluated in order; the first match wins. If no
// pattern matches, Default is used.
type Multiplex struct {
	Routes  []Route
	Default Opener
}

// Route is a (predicate, opener) pair.
type Route struct {
	// HasPrefix matches identifiers that start with this string. Empty means
	// "match nothing"; combine with HasScheme for URL-based routing.
	HasPrefix string
	// HasScheme matches identifiers that look like a URL with this scheme.
	HasScheme string
	Opener    Opener
}

// Open dispatches identifier to the first matching route.
func (m *Multiplex) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	for _, r := range m.Routes {
		if r.HasPrefix != "" && strings.HasPrefix(identifier, r.HasPrefix) {
			return r.Opener.Open(ctx, identifier)
		}
		if r.HasScheme != "" && strings.HasPrefix(identifier, r.HasScheme+"://") {
			return r.Opener.Open(ctx, identifier)
		}
	}
	if m.Default == nil {
		return nil, Meta{}, fmt.Errorf("%w: no route for identifier", ErrNotFound)
	}
	return m.Default.Open(ctx, identifier)
}
