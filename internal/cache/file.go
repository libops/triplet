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
	"strings"
	"sync"
	"time"
)

// FileStore stores cache entries as files under Root. Bytes go in <hash>.
// When storeContentType is enabled, metadata goes in <hash>.meta. Keys are
// hashed (SHA-256) so they can contain any characters and still be safe
// filenames.
type FileStore struct {
	Root string

	storeContentType bool

	// MaxBytes optionally bounds total cache size; when exceeded, the
	// oldest payload files are evicted on the next Put based on mtime.
	MaxBytes int64

	// MaxAge optionally bounds how long entries remain usable after Put.
	// Expired entries are removed on Get and opportunistically on Put.
	MaxAge time.Duration

	mu sync.Mutex
}

const tempFilePrefix = ".tmp-"

// NewFileStore constructs a FileStore. Root is created if it does not exist.
func NewFileStore(root string, maxBytes int64) (*FileStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("cache file: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("cache file mkdir: %w", err)
	}
	return &FileStore{Root: abs, storeContentType: true, MaxBytes: maxBytes}, nil
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

// NewPayloadFileStoreWithMaxAge constructs a payload-only FileStore. It stores
// content type out of band in the caller and uses payload file metadata for
// age and size accounting.
func NewPayloadFileStoreWithMaxAge(root string, maxBytes int64, maxAge time.Duration) (*FileStore, error) {
	store, err := NewFileStore(root, maxBytes)
	if err != nil {
		return nil, err
	}
	store.storeContentType = false
	store.MaxAge = maxAge
	return store, nil
}

type fileMeta struct {
	ContentType string `json:"content_type"`
}

// Get implements Store.
func (s *FileStore) Get(_ context.Context, key string) (io.ReadCloser, Entry, error) {
	dataPath, metaPath := s.paths(key)
	contentType := ""
	if s.storeContentType {
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
		contentType = m.ContentType
	}
	f, err := os.Open(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Entry{}, ErrMiss
		}
		return nil, Entry{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, Entry{}, err
	}
	storedAt := info.ModTime()
	if s.expired(storedAt, time.Now()) {
		_ = f.Close()
		_ = os.Remove(dataPath)
		if s.storeContentType {
			_ = os.Remove(metaPath)
		}
		return nil, Entry{}, ErrMiss
	}
	return f, Entry{
		ContentType: contentType,
		Size:        info.Size(),
		StoredAt:    storedAt,
	}, nil
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
	_, err = io.Copy(tmp, value)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if s.storeContentType {
		if err := s.installWithMeta(tmpName, dataPath, metaPath, contentType); err != nil {
			return err
		}
	} else if err := os.Rename(tmpName, dataPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if s.MaxAge > 0 || s.MaxBytes > 0 {
		s.evict()
	}
	return nil
}

func (s *FileStore) installWithMeta(tmpName, dataPath, metaPath, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Rename(tmpName, dataPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	meta := fileMeta{ContentType: contentType}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, mb, 0o640); err != nil {
		_ = os.Remove(dataPath)
		return err
	}
	return nil
}

// Delete implements Store.
func (s *FileStore) Delete(_ context.Context, key string) error {
	dataPath, metaPath := s.paths(key)
	_ = os.Remove(dataPath)
	if s.storeContentType {
		_ = os.Remove(metaPath)
	}
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
		path    string
		size    int64
		modTime time.Time
	}
	var entries []entry
	now := time.Now()
	_ = filepath.WalkDir(s.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".meta" || strings.HasPrefix(d.Name(), tempFilePrefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		metaPath := p + ".meta"
		if s.storeContentType {
			if _, err := os.Stat(metaPath); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					_ = os.Remove(p)
					return nil
				}
				return nil
			}
		}
		if s.expired(info.ModTime(), now) {
			_ = os.Remove(p)
			if s.storeContentType {
				_ = os.Remove(metaPath)
			}
			return nil
		}
		entries = append(entries, entry{path: p, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
		return nil
	})
	if s.MaxBytes <= 0 || total <= s.MaxBytes {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].modTime.Equal(entries[j].modTime) {
			return entries[i].path < entries[j].path
		}
		return entries[i].modTime.Before(entries[j].modTime)
	})
	for _, e := range entries {
		if total <= s.MaxBytes {
			return
		}
		_ = os.Remove(e.path)
		if s.storeContentType {
			_ = os.Remove(e.path + ".meta")
		}
		total -= e.size
	}
}

func (s *FileStore) expired(storedAt, now time.Time) bool {
	return s.MaxAge > 0 && !storedAt.IsZero() && now.Sub(storedAt) > s.MaxAge
}
