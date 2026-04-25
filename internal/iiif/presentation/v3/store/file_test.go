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
	if err := s.PutAnnotationPage(context.Background(), "item-1", "canvas-1", body); err != nil {
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
