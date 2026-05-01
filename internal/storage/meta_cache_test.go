package storage

import (
	"context"
	"io"
	"testing"
	"time"
)

type countingMetaOpener struct {
	metaCalls int
	openCalls int
	meta      Meta
}

func (o *countingMetaOpener) Open(context.Context, string) (io.ReadSeekCloser, Meta, error) {
	o.openCalls++
	return &bytesReadSeekCloser{r: newSeekableBytes([]byte("source"))}, o.meta, nil
}

func (o *countingMetaOpener) Meta(context.Context, string) (Meta, error) {
	o.metaCalls++
	return o.meta, nil
}

func TestMetaCachingCachesWithinTTL(t *testing.T) {
	inner := &countingMetaOpener{meta: Meta{Size: 10, Version: "v1"}}
	cached := &MetaCaching{Inner: inner, TTL: time.Hour}

	for range 2 {
		meta, err := cached.Meta(context.Background(), "id")
		if err != nil {
			t.Fatal(err)
		}
		if meta.Version != "v1" {
			t.Fatalf("meta version = %q", meta.Version)
		}
	}
	if inner.metaCalls != 1 {
		t.Fatalf("meta calls = %d, want 1", inner.metaCalls)
	}
}

func TestMetaCachingExpires(t *testing.T) {
	inner := &countingMetaOpener{meta: Meta{Size: 10, Version: "v1"}}
	cached := &MetaCaching{Inner: inner, TTL: time.Nanosecond}

	if _, err := cached.Meta(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err := cached.Meta(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
	if inner.metaCalls != 2 {
		t.Fatalf("meta calls = %d, want 2", inner.metaCalls)
	}
}

func TestMetaCachingOpenStoresMeta(t *testing.T) {
	inner := &countingMetaOpener{meta: Meta{Size: 10, Version: "v1"}}
	cached := &MetaCaching{Inner: inner, TTL: time.Hour}

	rc, _, err := cached.Open(context.Background(), "id")
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	if _, err := cached.Meta(context.Background(), "id"); err != nil {
		t.Fatal(err)
	}
	if inner.metaCalls != 0 {
		t.Fatalf("meta calls = %d, want 0", inner.metaCalls)
	}
	if inner.openCalls != 1 {
		t.Fatalf("open calls = %d, want 1", inner.openCalls)
	}
}

func TestMetaCachingEvictsOldest(t *testing.T) {
	inner := &countingMetaOpener{meta: Meta{Size: 10, Version: "v1"}}
	cached := &MetaCaching{Inner: inner, TTL: time.Hour, MaxEntries: 1}

	if _, err := cached.Meta(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Meta(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Meta(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if inner.metaCalls != 3 {
		t.Fatalf("meta calls = %d, want 3", inner.metaCalls)
	}
}
