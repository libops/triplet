package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/libops/triplet/internal/cache"
)

// Caching wraps an Opener with a byte cache keyed by identifier. On hit,
// returns the cached bytes; on miss, fetches from Inner and writes through
// to the cache before returning.
type Caching struct {
	Inner      Opener
	Store      cache.Store
	StaleAfter time.Duration
}

// Open implements Opener.
func (c *Caching) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	if rc, entry, err := c.Store.Get(ctx, identifier); err == nil {
		buf, rerr := io.ReadAll(rc)
		_ = rc.Close()
		if rerr == nil {
			if c.StaleAfter > 0 && time.Since(entry.StoredAt) > c.StaleAfter {
				go c.refresh(context.Background(), identifier)
			}
			return &bytesReadSeekCloser{r: newSeekableBytes(buf)},
				Meta{ContentType: entry.ContentType, Size: entry.Size, ModTime: entry.StoredAt}, nil
		}
	} else if !errors.Is(err, cache.ErrMiss) {
		// Cache failures are non-fatal; fall through to upstream.
		_ = err
	}

	rc, meta, err := c.Inner.Open(ctx, identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, Meta{}, err
	}
	if perr := c.Store.Put(ctx, identifier, meta.ContentType, bytes.NewReader(body)); perr != nil {
		// Best effort — log handled at the caller.
		_ = perr
	}
	return &bytesReadSeekCloser{r: newSeekableBytes(body)}, meta, nil
}

func (c *Caching) refresh(ctx context.Context, identifier string) {
	rc, meta, err := c.Inner.Open(ctx, identifier)
	if err != nil {
		return
	}
	defer rc.Close()
	_ = c.Store.Put(ctx, identifier, meta.ContentType, rc)
}
