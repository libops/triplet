package storage

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/libops/triplet/internal/cache"
)

type countingOpener struct {
	body  []byte
	mu    sync.Mutex
	calls int
}

func (o *countingOpener) Open(_ context.Context, _ string) (io.ReadSeekCloser, Meta, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	return &bytesReadSeekCloser{r: newSeekableBytes(o.body)}, Meta{
		ContentType: "image/png",
		Size:        int64(len(o.body)),
	}, nil
}

func TestCachingOpenerCachesMisses(t *testing.T) {
	store, err := cache.NewFileStore(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}

	inner := &countingOpener{body: []byte("payload")}
	cached := &Caching{Inner: inner, Store: store}

	read := func() string {
		t.Helper()
		rc, meta, err := cached.Open(context.Background(), "id")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if meta.ContentType != "image/png" {
			t.Fatalf("content-type = %q", meta.ContentType)
		}
		return string(b)
	}

	if got := read(); got != "payload" {
		t.Fatalf("first read = %q", got)
	}
	if got := read(); got != "payload" {
		t.Fatalf("second read = %q", got)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
}

func TestCachingOpenerServesStaleAndRefreshes(t *testing.T) {
	store := newTestCacheStore([]byte("old"), cache.Entry{
		ContentType: "image/png",
		Size:        3,
		StoredAt:    time.Now().Add(-2 * time.Hour),
	})
	inner := &countingOpener{body: []byte("new")}
	cached := &Caching{Inner: inner, Store: store, StaleAfter: time.Hour}

	rc, _, err := cached.Open(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "old" {
		t.Fatalf("body = %q", string(b))
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		got := string(store.body)
		store.mu.Unlock()
		if got == "new" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cache was not refreshed")
}

type testCacheStore struct {
	mu    sync.Mutex
	body  []byte
	entry cache.Entry
}

func newTestCacheStore(body []byte, entry cache.Entry) *testCacheStore {
	return &testCacheStore{body: body, entry: entry}
}

func (s *testCacheStore) Get(context.Context, string) (io.ReadCloser, cache.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.body == nil {
		return nil, cache.Entry{}, cache.ErrMiss
	}
	return io.NopCloser(newSeekableBytes(append([]byte(nil), s.body...))), s.entry, nil
}

func (s *testCacheStore) Put(_ context.Context, _ string, contentType string, value io.Reader) error {
	b, err := io.ReadAll(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = b
	s.entry = cache.Entry{ContentType: contentType, Size: int64(len(b)), StoredAt: time.Now()}
	return nil
}

func (s *testCacheStore) Delete(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = nil
	return nil
}
