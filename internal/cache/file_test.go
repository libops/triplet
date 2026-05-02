package cache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestFileStorePutGetDelete(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "abc", "image/png", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}

	rc, entry, err := store.Get(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Fatalf("body = %q", string(b))
	}
	if entry.ContentType != "image/png" {
		t.Fatalf("content-type = %q", entry.ContentType)
	}
	if entry.Size != int64(len("payload")) {
		t.Fatalf("size = %d", entry.Size)
	}
	if entry.StoredAt.IsZero() {
		t.Fatal("stored_at was zero")
	}

	if err := store.Delete(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Get(context.Background(), "abc")
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("err = %v", err)
	}
}

func TestPayloadFileStorePutGetDelete(t *testing.T) {
	store, err := NewPayloadFileStoreWithMaxAge(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "abc", "image/png", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}

	rc, entry, err := store.Get(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Fatalf("body = %q", string(b))
	}
	if entry.ContentType != "" {
		t.Fatalf("content-type = %q, want empty", entry.ContentType)
	}
	if entry.Size != int64(len("payload")) {
		t.Fatalf("size = %d", entry.Size)
	}
	if entry.StoredAt.IsZero() {
		t.Fatal("stored_at was zero")
	}

	_, metaPath := store.paths("abc")
	if _, err := os.Stat(metaPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("meta stat err = %v, want not exist", err)
	}
}

func TestFileStoreMissDoesNotCreateKeyDirectory(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileStore(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	dataPath, _ := store.paths("missing")
	dir := filepath.Dir(dataPath)

	_, _, err = store.Get(context.Background(), "missing")
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("err = %v, want miss", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dir stat err = %v, want not exist", err)
	}

	if err := store.Delete(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dir stat after delete err = %v, want not exist", err)
	}
}

func TestFileStoreEvictsWhenOversize(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "a", "text/plain", bytes.NewReader([]byte("1234"))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.Put(context.Background(), "b", "text/plain", bytes.NewReader([]byte("5678"))); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Get(context.Background(), "a"); !errors.Is(err, ErrMiss) {
		t.Fatalf("a err = %v, want miss", err)
	}
	if rc, _, err := store.Get(context.Background(), "b"); err != nil {
		t.Fatalf("b err = %v", err)
	} else {
		_ = rc.Close()
	}
}

func TestFileStoreGetDoesNotRefreshEvictionOrder(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "a", "text/plain", bytes.NewReader([]byte("1234"))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := store.Put(context.Background(), "b", "text/plain", bytes.NewReader([]byte("5678"))); err != nil {
		t.Fatal(err)
	}

	rc, _, err := store.Get(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()

	time.Sleep(20 * time.Millisecond)
	if err := store.Put(context.Background(), "c", "text/plain", bytes.NewReader([]byte("90"))); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Get(context.Background(), "a"); !errors.Is(err, ErrMiss) {
		t.Fatalf("a err = %v, want miss", err)
	}
	if rc, _, err := store.Get(context.Background(), "b"); err != nil {
		t.Fatalf("b err = %v", err)
	} else {
		_ = rc.Close()
	}
	if rc, _, err := store.Get(context.Background(), "c"); err != nil {
		t.Fatalf("c err = %v", err)
	} else {
		_ = rc.Close()
	}
}

func TestFileStoreEvictsByModTime(t *testing.T) {
	store, err := NewFileStore(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "a", "text/plain", bytes.NewReader([]byte("1234"))); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "b", "text/plain", bytes.NewReader([]byte("5678"))); err != nil {
		t.Fatal(err)
	}

	dataPathA, _ := store.paths("a")
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dataPathA, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	dataPathB, _ := store.paths("b")
	newTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(dataPathB, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "c", "text/plain", bytes.NewReader([]byte("90"))); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.Get(context.Background(), "a"); !errors.Is(err, ErrMiss) {
		t.Fatalf("a err = %v, want miss", err)
	}
	if rc, _, err := store.Get(context.Background(), "b"); err != nil {
		t.Fatalf("b err = %v", err)
	} else {
		_ = rc.Close()
	}
	if rc, _, err := store.Get(context.Background(), "c"); err != nil {
		t.Fatalf("c err = %v", err)
	} else {
		_ = rc.Close()
	}
}

func TestFileStoreMaxAgeExpiresOnGet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, err := NewFileStoreWithMaxAge(t.TempDir(), 0, time.Nanosecond)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Put(context.Background(), "old", "text/plain", strings.NewReader("payload")); err != nil {
			t.Fatal(err)
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()

		_, _, err = store.Get(context.Background(), "old")
		if !errors.Is(err, ErrMiss) {
			t.Fatalf("err = %v, want miss", err)
		}

		dataPath, metaPath := store.paths("old")
		if _, err := os.Stat(dataPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("data stat err = %v, want not exist", err)
		}
		if _, err := os.Stat(metaPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("meta stat err = %v, want not exist", err)
		}
	})
}

func TestFileStoreMaxAgeEvictsOnPut(t *testing.T) {
	store, err := NewFileStoreWithMaxAge(t.TempDir(), 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "old", "text/plain", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	dataPath, _ := store.paths("old")
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dataPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	if err := store.Put(context.Background(), "new", "text/plain", strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(context.Background(), "old"); !errors.Is(err, ErrMiss) {
		t.Fatalf("old err = %v, want miss", err)
	}
	if rc, _, err := store.Get(context.Background(), "new"); err != nil {
		t.Fatalf("new err = %v", err)
	} else {
		_ = rc.Close()
	}
}
