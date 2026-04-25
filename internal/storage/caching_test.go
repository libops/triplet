package storage

import (
	"context"
	"io"
	"testing"

	"github.com/libops/triplet/internal/cache"
)

type countingOpener struct {
	body  []byte
	calls int
}

func (o *countingOpener) Open(_ context.Context, _ string) (io.ReadSeekCloser, Meta, error) {
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
