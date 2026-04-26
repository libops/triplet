package cache

import (
	"bytes"
	"context"
	"errors"
	"io"
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
