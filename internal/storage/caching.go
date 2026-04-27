package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/libops/triplet/internal/cache"
)

// Caching wraps an Opener with a byte cache keyed by identifier. On hit,
// returns the cached bytes; on miss, fetches from Inner and writes through
// to the cache before returning.
type Caching struct {
	Inner          Opener
	Store          cache.Store
	StaleAfter     time.Duration
	RefreshContext context.Context

	mu         sync.Mutex
	refreshing map[string]struct{}
}

// Close releases resources held by the wrapped cache store when supported.
func (c *Caching) Close() error {
	if closer, ok := c.Store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// Open implements Opener.
func (c *Caching) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	if rc, entry, err := c.Store.Get(ctx, identifier); err == nil {
		if rsc, ok := rc.(io.ReadSeekCloser); ok {
			if c.StaleAfter > 0 && time.Since(entry.StoredAt) > c.StaleAfter {
				c.startRefresh(identifier)
			}
			return rsc, cacheMeta(entry), nil
		}
		rsc, rerr := spoolReadCloser(rc, "triplet-source-cache-*")
		if rerr == nil {
			if c.StaleAfter > 0 && time.Since(entry.StoredAt) > c.StaleAfter {
				c.startRefresh(identifier)
			}
			return rsc, cacheMeta(entry), nil
		}
	} else if !errors.Is(err, cache.ErrMiss) {
		// Cache failures are non-fatal; fall through to upstream.
		_ = err
	}

	rc, meta, err := c.Inner.Open(ctx, identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	if perr := c.Store.Put(ctx, identifier, meta.ContentType, rc); perr != nil {
		// Best effort — log handled at the caller.
		_ = perr
	} else if cached, entry, gerr := c.Store.Get(ctx, identifier); gerr == nil {
		if rsc, ok := cached.(io.ReadSeekCloser); ok {
			_ = rc.Close()
			return rsc, cacheMeta(entry), nil
		}
		if rsc, rerr := spoolReadCloser(cached, "triplet-source-cache-*"); rerr == nil {
			_ = rc.Close()
			return rsc, cacheMeta(entry), nil
		}
	}
	if _, err := rc.Seek(0, io.SeekStart); err != nil {
		_ = rc.Close()
		return nil, Meta{}, err
	}
	return rc, meta, nil
}

// Meta implements MetaReader.
func (c *Caching) Meta(ctx context.Context, identifier string) (Meta, error) {
	if rc, entry, err := c.Store.Get(ctx, identifier); err == nil {
		_ = rc.Close()
		if c.StaleAfter > 0 && time.Since(entry.StoredAt) > c.StaleAfter {
			c.startRefresh(identifier)
		}
		return cacheMeta(entry), nil
	}
	metaReader, ok := c.Inner.(MetaReader)
	if !ok {
		return Meta{}, errors.New("metadata unavailable")
	}
	return metaReader.Meta(ctx, identifier)
}

func cacheMeta(entry cache.Entry) Meta {
	return Meta{
		ContentType: entry.ContentType,
		Size:        entry.Size,
		ModTime:     entry.StoredAt,
		Version:     "cache:" + entry.StoredAt.UTC().Format(time.RFC3339Nano) + ":" + entry.ContentType,
	}
}

func spoolReadCloser(rc io.ReadCloser, pattern string) (io.ReadSeekCloser, error) {
	defer rc.Close()
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, err
	}
	return &tempFileReadSeekCloser{File: tmp, path: tmpName}, nil
}

func (c *Caching) startRefresh(identifier string) {
	c.mu.Lock()
	if c.refreshing == nil {
		c.refreshing = make(map[string]struct{})
	}
	if _, ok := c.refreshing[identifier]; ok {
		c.mu.Unlock()
		return
	}
	c.refreshing[identifier] = struct{}{}
	c.mu.Unlock()

	ctx := c.RefreshContext
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.refreshing, identifier)
			c.mu.Unlock()
		}()
		c.refresh(ctx, identifier)
	}()
}

func (c *Caching) refresh(ctx context.Context, identifier string) {
	rc, meta, err := c.Inner.Open(ctx, identifier)
	if err != nil {
		return
	}
	defer rc.Close()
	_ = c.Store.Put(ctx, identifier, meta.ContentType, rc)
}
