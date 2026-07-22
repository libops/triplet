package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFileStoreResourceLifecycle(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "items/item-1/manifest"
	first := []byte(`{"id":"https://example.org/manifest","type":"Manifest"}`)
	created, err := st.Put(ctx, key, first, Preconditions{IfNoneMatch: "*"})
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	document, err := st.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(document.Body) != string(first) {
		t.Fatalf("body = %q", document.Body)
	}
	if document.ModifiedAt.IsZero() {
		t.Fatal("missing modification time")
	}

	if _, err := st.Put(ctx, key, first, Preconditions{IfNoneMatch: "*"}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("duplicate create err = %v", err)
	}
	second := []byte(`{"id":"https://example.org/manifest","type":"Manifest","summary":{"en":["updated"]}}`)
	created, err = st.Put(ctx, key, second, Preconditions{IfMatch: DocumentETag(first)})
	if err != nil || created {
		t.Fatalf("update = %v, %v", created, err)
	}
	if _, err := st.Put(ctx, key, first, Preconditions{IfMatch: DocumentETag(first)}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale update err = %v", err)
	}
	if _, err := st.Put(ctx, key, first, Preconditions{IfMatch: "*"}); err != nil {
		t.Fatalf("wildcard update: %v", err)
	}
	if err := st.Delete(ctx, key, DocumentETag(second)); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale delete err = %v", err)
	}
	if err := st.Delete(ctx, key, "*"); err != nil {
		t.Fatalf("wildcard delete: %v", err)
	}
	if _, err := st.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err = %v", err)
	}
	if err := st.Delete(ctx, key, "*"); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("delete missing err = %v", err)
	}
}

func TestFileStoreMissingAndInvalidKeys(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"", strings.Repeat("a", MaxResourceKeyBytes+1), "bad\nkey"} {
		if _, err := st.Get(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) err = %v", key, err)
		}
	}
	if _, err := st.Put(ctx, "missing", []byte(`{}`), Preconditions{IfMatch: "*"}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("update missing err = %v", err)
	}
	if _, err := st.Put(ctx, "missing", []byte(`{}`), Preconditions{}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("unconditional put err = %v", err)
	}
}

func TestFileStoreRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "resources")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	st, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Put(context.Background(), "items/one", []byte(`{"secret":true}`), Preconditions{IfNoneMatch: "*"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("put err = %v, want ErrNotFound", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside directory was modified: %v", entries)
	}
}

func TestFileStoreConditionalCreateIsAtomic(t *testing.T) {
	st, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 16
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.Put(context.Background(), "one/resource", []byte(`{"id":"one"}`), Preconditions{IfNoneMatch: "*"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	preconditionFailed := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrPreconditionFailed):
			preconditionFailed++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if succeeded != 1 || preconditionFailed != attempts-1 {
		t.Fatalf("successes = %d, precondition failures = %d", succeeded, preconditionFailed)
	}
}

func TestIfMatchMatches(t *testing.T) {
	etag := `"abc"`
	for _, header := range []string{`"abc"`, `"other", "abc"`, "*"} {
		if !IfMatchMatches(header, etag) {
			t.Fatalf("IfMatchMatches(%q) = false", header)
		}
	}
	for _, header := range []string{"", `W/"abc"`, `"other"`} {
		if IfMatchMatches(header, etag) {
			t.Fatalf("IfMatchMatches(%q) = true", header)
		}
	}
}
