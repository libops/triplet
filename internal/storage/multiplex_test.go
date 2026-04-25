package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type stubOpener struct {
	body string
}

func (s stubOpener) Open(_ context.Context, _ string) (io.ReadSeekCloser, Meta, error) {
	return &bytesReadSeekCloser{r: newSeekableBytes([]byte(s.body))}, Meta{Size: int64(len(s.body))}, nil
}

func TestMultiplexOpen(t *testing.T) {
	m := &Multiplex{
		Routes: []Route{
			{HasScheme: "https", Opener: stubOpener{body: "http"}},
		},
		Default: stubOpener{body: "file"},
	}

	t.Run("scheme route", func(t *testing.T) {
		rc, _, err := m.Open(context.Background(), "https://example.org/image.jp2")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		if string(b) != "http" {
			t.Fatalf("body = %q", string(b))
		}
	})

	t.Run("default route", func(t *testing.T) {
		rc, _, err := m.Open(context.Background(), "local/file.tif")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		if string(b) != "file" {
			t.Fatalf("body = %q", string(b))
		}
	})
}

func TestMultiplexNoRoute(t *testing.T) {
	m := &Multiplex{}
	_, _, err := m.Open(context.Background(), "anything")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "no route") {
		t.Fatalf("err = %v", err)
	}
}
