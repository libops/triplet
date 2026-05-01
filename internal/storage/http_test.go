package storage

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

	op := NewHTTPOpener([]string{srv.URL}, 5*time.Second, 0)
	op.AllowPrivateHosts = true

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
		if meta.Version == "" {
			t.Fatal("missing version")
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
		denied := NewHTTPOpener([]string{"https://example.org"}, 5*time.Second, 0)
		_, _, err := denied.Open(context.Background(), srv.URL+"/ok")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("origin port must match", func(t *testing.T) {
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		denied := NewHTTPOpener([]string{u.Scheme + "://" + u.Hostname()}, 5*time.Second, 0)
		denied.AllowPrivateHosts = true
		_, _, err = denied.Open(context.Background(), srv.URL+"/ok")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("origin allow-list ignores case", func(t *testing.T) {
		u, err := url.Parse(strings.ToUpper(srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		if !op.originAllowed(u) {
			t.Fatal("expected origin to match allow-list entry")
		}
	})

	t.Run("max bytes", func(t *testing.T) {
		limited := NewHTTPOpener([]string{srv.URL}, 5*time.Second, 4)
		limited.AllowPrivateHosts = true
		_, _, err := limited.Open(context.Background(), srv.URL+"/large")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestHTTPOpenerAppliesDefaultRequestTimeout(t *testing.T) {
	op := NewHTTPOpener([]string{"https://example.org"}, 0, 0)
	if op.Client.Timeout != DefaultRequestTimeout {
		t.Fatalf("Client.Timeout = %s, want %s", op.Client.Timeout, DefaultRequestTimeout)
	}
}

func TestHTTPOpenerRejectsRedirectToDeniedHost(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.org/secret", http.StatusFound)
	}))
	defer redirector.Close()

	op := NewHTTPOpener([]string{redirector.URL}, 5*time.Second, 0)
	op.AllowPrivateHosts = true
	_, _, err := op.Open(context.Background(), redirector.URL+"/redirect")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPOpenerUsesRangeRequests(t *testing.T) {
	body := []byte("0123456789")
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" {
			t.Fatal("expected range request")
		}
		ranges = append(ranges, rng)
		start, end := parseTestRange(t, rng)
		if end >= int64(len(body)) {
			end = int64(len(body)) - 1
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start : end+1])
	}))
	defer srv.Close()

	op := NewHTTPOpener([]string{srv.URL}, 5*time.Second, 0)
	op.AllowPrivateHosts = true
	rc, meta, err := op.Open(context.Background(), srv.URL+"/range")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if meta.Size != int64(len(body)) {
		t.Fatalf("size = %d", meta.Size)
	}
	if meta.Version != "" {
		t.Fatalf("version = %q, want empty without upstream validators", meta.Version)
	}
	buf := make([]byte, 3)
	n, err := rc.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || string(buf) != "012" {
		t.Fatalf("read = %d %q", n, string(buf))
	}
	if _, err := rc.Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, 2)
	n, err = rc.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || string(buf) != "56" {
		t.Fatalf("read after seek = %d %q", n, string(buf))
	}
	if len(ranges) < 3 {
		t.Fatalf("ranges = %#v", ranges)
	}
}

func TestHTTPOpenerMetaUsesHEAD(t *testing.T) {
	body := []byte("0123456789")
	var gotMethods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		if r.Method != http.MethodHead {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	}))
	defer srv.Close()

	op := NewHTTPOpener([]string{srv.URL}, 5*time.Second, 0)
	op.AllowPrivateHosts = true
	meta, err := op.Meta(context.Background(), srv.URL+"/image")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != int64(len(body)) {
		t.Fatalf("size = %d", meta.Size)
	}
	if meta.ContentType != "image/png" {
		t.Fatalf("content-type = %q", meta.ContentType)
	}
	if len(gotMethods) != 1 || gotMethods[0] != http.MethodHead {
		t.Fatalf("methods = %#v", gotMethods)
	}
}

func TestHTTPOpenerBlocksPrivateAddressesByDefault(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1", "fe80::1"} {
		t.Run(raw, func(t *testing.T) {
			if !privateAddressBlocked(net.ParseIP(raw)) {
				t.Fatalf("%s was not blocked", raw)
			}
		})
	}
	if privateAddressBlocked(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address was blocked")
	}
}

func parseTestRange(t *testing.T, v string) (int64, int64) {
	t.Helper()
	v = strings.TrimPrefix(v, "bytes=")
	parts := strings.Split(v, "-")
	if len(parts) != 2 {
		t.Fatalf("bad range %q", v)
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return start, end
}
