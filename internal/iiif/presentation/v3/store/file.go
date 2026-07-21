package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileStore persists path-keyed Presentation resources beneath a filesystem
// root. Keys are hashed before becoming filenames, avoiding path traversal,
// platform filename limits, and resource/directory name collisions.
type FileStore struct {
	root     string
	realRoot string
	mu       sync.Mutex
}

// NewFileStore constructs a FileStore rooted at root.
func NewFileStore(root string) (*FileStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("presentation file store: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("presentation file store stat: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("presentation file store: root %q is not a directory", abs)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("presentation file store root: %w", err)
	}
	return &FileStore{root: abs, realRoot: realRoot}, nil
}

// Get implements Store.
func (s *FileStore) Get(_ context.Context, resourceKey string) (Document, error) {
	path, err := s.resourcePath(resourceKey)
	if err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err = s.resolveRead(path)
	if err != nil {
		return Document{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Document{}, ErrNotFound
		}
		return Document{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Document{}, ErrNotFound
		}
		return Document{}, err
	}
	return Document{Body: body, ModifiedAt: info.ModTime().UTC()}, nil
}

// Put implements Store.
func (s *FileStore) Put(_ context.Context, resourceKey string, body []byte, conditions Preconditions) (bool, error) {
	path, err := s.resourcePath(resourceKey)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(path)
	if err := s.ensureCreatePathContained(dir); err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, err
	}
	if err := s.ensureContained(dir); err != nil {
		return false, err
	}
	current, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	currentETag := ""
	if exists {
		currentETag = DocumentETag(current)
	}
	if !putPreconditionMatches(exists, currentETag, conditions) {
		return false, ErrPreconditionFailed
	}
	if err := atomicWriteFile(path, body, 0o600); err != nil {
		return false, err
	}
	return !exists, nil
}

// Delete implements Store.
func (s *FileStore) Delete(_ context.Context, resourceKey, ifMatch string) error {
	path, err := s.resourcePath(resourceKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err = s.resolveRead(path)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrPreconditionFailed
		}
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrPreconditionFailed
		}
		return err
	}
	if !IfMatchMatches(ifMatch, DocumentETag(current)) {
		return ErrPreconditionFailed
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func (s *FileStore) resourcePath(resourceKey string) (string, error) {
	if !validResourceKey(resourceKey) {
		return "", ErrNotFound
	}
	sum := sha256.Sum256([]byte(resourceKey))
	digest := hex.EncodeToString(sum[:])
	return filepath.Join(s.root, "resources", digest[:2], digest+".json"), nil
}

func atomicWriteFile(path string, body []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".presentation-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func (s *FileStore) resolveRead(path string) (string, error) {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	if err := s.ensureContained(realPath); err != nil {
		return "", err
	}
	return realPath, nil
}

func (s *FileStore) ensureContained(path string) error {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(s.realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrNotFound
	}
	return nil
}

func (s *FileStore) ensureCreatePathContained(path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return s.ensureContained(path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ErrNotFound
		}
		path = parent
	}
}
