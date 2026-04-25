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
	root string
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
	return &FileStore{root: abs}, nil
}

// GetManifest implements Store.
func (s *FileStore) GetManifest(_ context.Context, itemID string) ([]byte, error) {
	path, err := s.resolve(itemID, "manifest.json")
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
	path, err := s.resolve(itemID, "canvas", canvasID, "annotations.json")
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrNotFound
	}
	return path, nil
}
