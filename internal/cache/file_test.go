package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, _, errA := store.Get(context.Background(), "a")
		_, _, errB := store.Get(context.Background(), "b")
		if errors.Is(errA, ErrMiss) && errB == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected oldest entry to be evicted")
}

func TestFileStoreMaxAgeExpiresOnGet(t *testing.T) {
	store, err := NewFileStoreWithMaxAge(t.TempDir(), 0, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "old", "text/plain", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)

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
}

func TestFileStoreMaxAgeEvictsOnPut(t *testing.T) {
	store, err := NewFileStoreWithMaxAge(t.TempDir(), 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "old", "text/plain", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	dataPath, metaPath := store.paths("old")
	oldTime := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(dataPath, oldTime, oldTime)
	meta := fileMeta{ContentType: "text/plain", Size: 3, StoredAt: oldTime}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath, mb, 0o640); err != nil {
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
