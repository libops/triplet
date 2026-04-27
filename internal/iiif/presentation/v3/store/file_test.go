package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreGetManifest(t *testing.T) {
	root := t.TempDir()
	itemDir := filepath.Join(root, "item-1")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/presentation/v3/item-1/manifest","type":"Manifest","label":{"en":["Item 1"]},"items":[{"id":"http://example.test/presentation/v3/item-1/canvas/1","type":"Canvas"}]}`)
	if err := os.WriteFile(filepath.Join(itemDir, "manifest.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetManifest(context.Background(), "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %q", string(got))
	}
}

func TestFileStoreGetManifestNotFound(t *testing.T) {
	s, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, itemID := range []string{"missing", "../escape", "", "/abs"} {
		t.Run(itemID, func(t *testing.T) {
			_, err := s.GetManifest(context.Background(), itemID)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestFileStoreAnnotationPageRoundTrip(t *testing.T) {
	root := t.TempDir()
	itemDir := filepath.Join(root, "item-1")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}

	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"http://example.test/presentation/v3/item-1/canvas/canvas-1/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/annotations/1","type":"Annotation","textGranularity":"line","motivation":["supplementing"],"body":{"type":"TextualBody","value":"hello"},"target":{"type":"SpecificResource","source":"http://example.test/presentation/v3/item-1/canvas/canvas-1","selector":{"type":"FragmentSelector","value":"xywh=1,2,3,4"}}}]}`)
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", body, "*"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetAnnotationPage(context.Background(), "item-1", "canvas-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %q", string(got))
	}
}

func TestFileStoreRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "manifest.json"), []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.GetManifest(context.Background(), "link")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	err = s.PutAnnotationPage(context.Background(), "link", "canvas-1", []byte(`{"type":"AnnotationPage","id":"x","items":[]}`), "*")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("put err = %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "canvas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside canvas dir err = %v, want not exist", err)
	}
}

func TestFileStoreAnnotationPagePreconditions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "item-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	first := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/annotations","type":"AnnotationPage","items":[]}`)
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", first, `"missing"`); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("missing exact err = %v, want ErrPreconditionFailed", err)
	}
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", first, "*"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", first, "*"); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("create existing err = %v, want ErrPreconditionFailed", err)
	}
	second := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/a","type":"Annotation"}]}`)
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", second, DocumentETag(first)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnnotationPage(context.Background(), "item-1", "canvas-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(second) {
		t.Fatalf("body = %q", string(got))
	}
}
