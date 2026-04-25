package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPOpener(t *testing.T) {
	modTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Last-Modified", modTime.Format(http.TimeFormat))
			_, _ = w.Write([]byte("pngdata"))
		case "/large":
			_, _ = w.Write([]byte("abcdef"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	op := NewHTTPOpener([]string{"127.0.0.1", "localhost"}, 5*time.Second, 0)

	t.Run("found", func(t *testing.T) {
		rc, meta, err := op.Open(context.Background(), srv.URL+"/ok")
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "pngdata" {
			t.Fatalf("body = %q", string(b))
		}
		if meta.ContentType != "image/png" {
			t.Fatalf("content-type = %q", meta.ContentType)
		}
		if meta.Size != int64(len("pngdata")) {
			t.Fatalf("size = %d", meta.Size)
		}
		if !meta.ModTime.Equal(modTime) {
			t.Fatalf("modtime = %v", meta.ModTime)
		}
		if _, err := rc.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("seek: %v", err)
		}
		b, err = io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "pngdata" {
			t.Fatalf("second read = %q", string(b))
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, _, err := op.Open(context.Background(), srv.URL+"/missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("host denied", func(t *testing.T) {
		denied := NewHTTPOpener([]string{"example.org"}, 5*time.Second, 0)
		_, _, err := denied.Open(context.Background(), srv.URL+"/ok")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("max bytes", func(t *testing.T) {
		limited := NewHTTPOpener([]string{"127.0.0.1", "localhost"}, 5*time.Second, 4)
		_, _, err := limited.Open(context.Background(), srv.URL+"/large")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
