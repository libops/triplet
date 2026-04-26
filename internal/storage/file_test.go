package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileOpener(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "nested.png"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	op, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("found", func(t *testing.T) {
		rc, meta, err := op.Open(context.Background(), "hello.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		if string(b) != "hi" {
			t.Errorf("got %q", string(b))
		}
		if meta.Size != 2 {
			t.Errorf("size = %d", meta.Size)
		}
		if meta.Version == "" {
			t.Fatal("missing version")
		}
	})

	t.Run("nested", func(t *testing.T) {
		rc, meta, err := op.Open(context.Background(), "sub/nested.png")
		if err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		if meta.ContentType != "image/png" {
			t.Errorf("content-type = %q", meta.ContentType)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, _, err := op.Open(context.Background(), "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "sub/../../escape", ""} {
		t.Run("reject "+bad, func(t *testing.T) {
			_, _, err := op.Open(context.Background(), bad)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFileOpenerRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	op, err := NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = op.Open(context.Background(), "link/secret.txt")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNewFileOpenerRejectsNonDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileOpener(f); err == nil {
		t.Fatal("expected error for non-directory root")
	}
}
