package storage

import (
	"context"
	"io"
	"sync"
	"time"
)

// MetaCaching caches successful source metadata lookups in process. It is
// useful for remote sources where metadata checks are required to build stable
// derivative cache keys, but repeated upstream HEAD requests are too expensive.
type MetaCaching struct {
	Inner      Opener
	TTL        time.Duration
	MaxEntries int

	mu    sync.Mutex
	items map[string]metaCacheEntry
}

type metaCacheEntry struct {
	meta     Meta
	storedAt time.Time
}

// Open delegates to Inner and records the returned metadata.
func (c *MetaCaching) Open(ctx context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	rc, meta, err := c.Inner.Open(ctx, identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	c.store(identifier, meta)
	return rc, meta, nil
}

// Meta implements MetaReader with an in-process TTL cache.
func (c *MetaCaching) Meta(ctx context.Context, identifier string) (Meta, error) {
	if meta, ok := c.get(identifier); ok {
		return meta, nil
	}
	metaReader, ok := c.Inner.(MetaReader)
	if !ok {
		rc, meta, err := c.Inner.Open(ctx, identifier)
		if err != nil {
			return Meta{}, err
		}
		_ = rc.Close()
		c.store(identifier, meta)
		return meta, nil
	}
	meta, err := metaReader.Meta(ctx, identifier)
	if err != nil {
		return Meta{}, err
	}
	c.store(identifier, meta)
	return meta, nil
}

func (c *MetaCaching) get(identifier string) (Meta, bool) {
	if c.TTL <= 0 {
		return Meta{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[identifier]
	if !ok {
		return Meta{}, false
	}
	if time.Since(entry.storedAt) > c.TTL {
		delete(c.items, identifier)
		return Meta{}, false
	}
	return entry.meta, true
}

func (c *MetaCaching) store(identifier string, meta Meta) {
	if c.TTL <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = make(map[string]metaCacheEntry)
	}
	if max := c.maxEntries(); len(c.items) >= max {
		var oldestKey string
		var oldest time.Time
		for key, entry := range c.items {
			if oldestKey == "" || entry.storedAt.Before(oldest) {
				oldestKey = key
				oldest = entry.storedAt
			}
		}
		delete(c.items, oldestKey)
	}
	c.items[identifier] = metaCacheEntry{meta: meta, storedAt: time.Now()}
}

func (c *MetaCaching) maxEntries() int {
	if c.MaxEntries > 0 {
		return c.MaxEntries
	}
	return 4096
}
