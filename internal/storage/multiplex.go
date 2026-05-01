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
	opener, err := m.route(identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	return opener.Open(ctx, identifier)
}

// Meta implements MetaReader when the selected opener supports metadata
// lookups.
func (m *Multiplex) Meta(ctx context.Context, identifier string) (Meta, error) {
	opener, err := m.route(identifier)
	if err != nil {
		return Meta{}, err
	}
	metaReader, ok := opener.(MetaReader)
	if !ok {
		return Meta{}, fmt.Errorf("metadata unavailable for identifier")
	}
	return metaReader.Meta(ctx, identifier)
}

// InvalidateAuth forwards auth-cache invalidation to the selected opener when
// it supports per-identifier auth state.
func (m *Multiplex) InvalidateAuth(ctx context.Context, identifier string) error {
	opener, err := m.route(identifier)
	if err != nil {
		return err
	}
	invalidator, ok := opener.(AuthInvalidator)
	if !ok {
		return nil
	}
	return invalidator.InvalidateAuth(ctx, identifier)
}

func (m *Multiplex) route(identifier string) (Opener, error) {
	for _, r := range m.Routes {
		if r.HasPrefix != "" && strings.HasPrefix(identifier, r.HasPrefix) {
			return r.Opener, nil
		}
		if r.HasScheme != "" && strings.HasPrefix(identifier, r.HasScheme+"://") {
			return r.Opener, nil
		}
	}
	if m.Default == nil {
		return nil, fmt.Errorf("%w: no route for identifier", ErrNotFound)
	}
	return m.Default, nil
}
