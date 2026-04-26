package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// FileOpener resolves identifiers as paths under Root. Identifiers are joined
// to Root and rejected if they would escape it.
type FileOpener struct {
	// Root is the absolute directory identifiers resolve against.
	Root string
}

// NewFileOpener constructs a FileOpener after resolving Root to an absolute
// path. The directory must exist at construction time.
func NewFileOpener(root string) (*FileOpener, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("file source root %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("file source root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("file source root %q: not a directory", abs)
	}
	return &FileOpener{Root: abs}, nil
}

// Open resolves identifier to a file under Root. The identifier is treated
// as a slash-separated relative path; absolute paths and `..` components are
// rejected.
func (f *FileOpener) Open(_ context.Context, identifier string) (io.ReadSeekCloser, Meta, error) {
	clean, err := safeJoin(f.Root, identifier)
	if err != nil {
		return nil, Meta{}, err
	}
	realRoot, err := filepath.EvalSymlinks(f.Root)
	if err != nil {
		return nil, Meta{}, fmt.Errorf("file source root %q: %w", f.Root, err)
	}
	realPath, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Meta{}, fmt.Errorf("%w: %s", ErrNotFound, identifier)
		}
		return nil, Meta{}, fmt.Errorf("resolve %q: %w", identifier, err)
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, Meta{}, fmt.Errorf("%w: identifier escapes source root", ErrNotFound)
	}
	file, err := os.Open(realPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Meta{}, fmt.Errorf("%w: %s", ErrNotFound, identifier)
		}
		return nil, Meta{}, fmt.Errorf("open %q: %w", identifier, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, Meta{}, fmt.Errorf("stat %q: %w", identifier, err)
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(clean)))
	return file, Meta{
		ContentType: ct,
		Size:        info.Size(),
		ModTime:     info.ModTime(),
		Version:     fileVersion(realPath, info),
	}, nil
}

func fileVersion(path string, info fs.FileInfo) string {
	sum := sha256.Sum256([]byte(path))
	return "file:" + hex.EncodeToString(sum[:]) + ":" + fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func safeJoin(root, identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("%w: empty identifier", ErrNotFound)
	}
	if strings.ContainsRune(identifier, 0) {
		return "", fmt.Errorf("%w: identifier contains NUL", ErrNotFound)
	}
	if filepath.IsAbs(identifier) {
		return "", fmt.Errorf("%w: identifier must be relative", ErrNotFound)
	}
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(identifier)))
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: identifier escapes source root", ErrNotFound)
	}
	return clean, nil
}
