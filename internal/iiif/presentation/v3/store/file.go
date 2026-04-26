package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileStore reads manifest documents from a filesystem root.
// Manifests are stored at {root}/{itemID}/manifest.json.
type FileStore struct {
	root     string
	realRoot string
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

// GetManifest implements Store.
func (s *FileStore) GetManifest(_ context.Context, itemID string) ([]byte, error) {
	path, err := s.resolveRead(itemID, "manifest.json")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

// GetAnnotationPage implements Store.
func (s *FileStore) GetAnnotationPage(_ context.Context, itemID, canvasID string) ([]byte, error) {
	path, err := s.resolveRead(itemID, "canvas", canvasID, "annotations.json")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

// PutAnnotationPage implements Store.
func (s *FileStore) PutAnnotationPage(_ context.Context, itemID, canvasID string, body []byte) error {
	path, err := s.resolve(itemID, "canvas", canvasID, "annotations.json")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := s.ensureCreatePathContained(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := s.ensureContained(dir); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func (s *FileStore) resolve(itemID string, elems ...string) (string, error) {
	if itemID == "" || strings.ContainsAny(itemID, "\x00\n\r") {
		return "", ErrNotFound
	}
	cleanItem := filepath.Clean(itemID)
	if cleanItem == "." || cleanItem == "/" || cleanItem == ".." || strings.HasPrefix(cleanItem, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanItem) {
		return "", ErrNotFound
	}
	parts := []string{s.root, cleanItem}
	for _, elem := range elems {
		if elem == "" || strings.ContainsAny(elem, "\x00\n\r") {
			return "", ErrNotFound
		}
		clean := filepath.Clean(elem)
		if clean == "." || clean == "/" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
			return "", ErrNotFound
		}
		parts = append(parts, clean)
	}
	path := filepath.Join(parts...)
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrNotFound
	}
	return path, nil
}

func (s *FileStore) resolveRead(itemID string, elems ...string) (string, error) {
	path, err := s.resolve(itemID, elems...)
	if err != nil {
		return "", err
	}
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
