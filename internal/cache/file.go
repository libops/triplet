package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileStore stores cache entries as files under Root. Bytes go in <hash>,
// metadata in <hash>.meta. Keys are hashed (SHA-256) so they can contain any
// characters and still be safe filenames.
type FileStore struct {
	Root string

	// MaxBytes optionally bounds total cache size; when exceeded, the
	// oldest-written entries are evicted on the next Put.
	MaxBytes int64

	// MaxAge optionally bounds how long entries remain usable after Put.
	// Expired entries are removed on Get and opportunistically on Put.
	MaxAge time.Duration

	mu sync.Mutex
}

// NewFileStore constructs a FileStore. Root is created if it does not exist.
func NewFileStore(root string, maxBytes int64) (*FileStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("cache file: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("cache file mkdir: %w", err)
	}
	return &FileStore{Root: abs, MaxBytes: maxBytes}, nil
}

// NewFileStoreWithMaxAge constructs a FileStore with size and age eviction.
func NewFileStoreWithMaxAge(root string, maxBytes int64, maxAge time.Duration) (*FileStore, error) {
	store, err := NewFileStore(root, maxBytes)
	if err != nil {
		return nil, err
	}
	store.MaxAge = maxAge
	return store, nil
}

type fileMeta struct {
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	StoredAt    time.Time `json:"stored_at"`
}

// Get implements Store.
func (s *FileStore) Get(_ context.Context, key string) (io.ReadCloser, Entry, error) {
	dataPath, metaPath := s.paths(key)
	mb, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Entry{}, ErrMiss
		}
		return nil, Entry{}, err
	}
	var m fileMeta
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, Entry{}, fmt.Errorf("cache meta: %w", err)
	}
	if s.expired(m.StoredAt, time.Now()) {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, Entry{}, ErrMiss
	}
	f, err := os.Open(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Entry{}, ErrMiss
		}
		return nil, Entry{}, err
	}
	return f, Entry(m), nil
}

// Put implements Store.
func (s *FileStore) Put(_ context.Context, key, contentType string, value io.Reader) error {
	dataPath, metaPath := s.paths(key)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Root, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	n, err := io.Copy(tmp, value)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dataPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	storedAt := time.Now()
	_ = os.Chtimes(dataPath, storedAt, storedAt)
	meta := fileMeta{ContentType: contentType, Size: n, StoredAt: storedAt}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, mb, 0o640); err != nil {
		return err
	}
	_ = os.Chtimes(metaPath, storedAt, storedAt)
	if s.MaxAge > 0 || s.MaxBytes > 0 {
		s.evict()
	}
	return nil
}

// Delete implements Store.
func (s *FileStore) Delete(_ context.Context, key string) error {
	dataPath, metaPath := s.paths(key)
	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)
	return nil
}

func (s *FileStore) paths(key string) (data, meta string) {
	sum := sha256.Sum256([]byte(key))
	hex := hex.EncodeToString(sum[:])
	// Two-level fan-out so a single directory doesn't grow unboundedly.
	dir := filepath.Join(s.Root, hex[:2], hex[2:4])
	base := filepath.Join(dir, hex)
	return base, base + ".meta"
}

func (s *FileStore) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	type entry struct {
		path     string
		metaPath string
		size     int64
		storedAt time.Time
	}
	var entries []entry
	now := time.Now()
	_ = filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".meta" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		storedAt := info.ModTime()
		metaPath := p + ".meta"
		if mb, err := os.ReadFile(metaPath); err == nil {
			var m fileMeta
			if err := json.Unmarshal(mb, &m); err == nil && !m.StoredAt.IsZero() {
				storedAt = m.StoredAt
			}
		}
		if s.expired(storedAt, now) {
			_ = os.Remove(p)
			_ = os.Remove(metaPath)
			return nil
		}
		entries = append(entries, entry{path: p, metaPath: metaPath, size: info.Size(), storedAt: storedAt})
		total += info.Size()
		return nil
	})
	if s.MaxBytes <= 0 || total <= s.MaxBytes {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].storedAt.Equal(entries[j].storedAt) {
			return entries[i].path < entries[j].path
		}
		return entries[i].storedAt.Before(entries[j].storedAt)
	})
	for _, e := range entries {
		if total <= s.MaxBytes {
			return
		}
		_ = os.Remove(e.path)
		_ = os.Remove(e.metaPath)
		total -= e.size
	}
}

func (s *FileStore) expired(storedAt, now time.Time) bool {
	return s.MaxAge > 0 && !storedAt.IsZero() && now.Sub(storedAt) > s.MaxAge
}
