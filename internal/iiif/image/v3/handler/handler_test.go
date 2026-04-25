package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/iiif/image/v3/pipeline"
	"github.com/libops/triplet/internal/iiif/image/v3/types"
	"github.com/libops/triplet/internal/storage"
)

func setupTestServer(t *testing.T) (*httptest.Server, string) {
	return setupTestServerWithCache(t, cache.Noop{})
}

func setupTestServerWithCache(t *testing.T, derivCache cache.Store) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.png"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	op, err := storage.NewFileOpener(root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(
		"/iiif/3",
		"http://example.test",
		op,
		pipeline.New(op, pipeline.Limits{MaxOutputPixels: 10_000_000}),
		derivCache,
		types.Limits{MaxArea: 10_000_000, MaxWidth: 4096, MaxHeight: 4096},
		logger,
	)
	mux := http.NewServeMux()
	h.Register(mux)
	return httptest.NewServer(mux), root
}

func TestBaseRedirectsToInfo(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(srv.URL + "/iiif/3/sample.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/iiif/3/sample.png/info.json" {
		t.Fatalf("Location = %q", got)
	}
}

func TestInfoJSON(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/sample.png/info.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var info types.Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Context != types.Context {
		t.Errorf("@context = %q", info.Context)
	}
	if info.Type != types.TypeImageService3 {
		t.Errorf("type = %q", info.Type)
	}
	if info.Width != 200 || info.Height != 100 {
		t.Errorf("dims = %dx%d", info.Width, info.Height)
	}
	if info.Id != "http://example.test/iiif/3/sample.png" {
		t.Errorf("id = %q", info.Id)
	}
	if info.MaxArea == nil || *info.MaxArea != 10_000_000 || info.MaxWidth == nil || *info.MaxWidth != 4096 || info.MaxHeight == nil || *info.MaxHeight != 4096 {
		t.Errorf("limits = %#v", info)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS = %q", got)
	}
	if got := resp.Header.Values("Link"); len(got) != 1 || got[0] != `<http://iiif.io/api/image/3/level2.json>;rel="profile"` {
		t.Errorf("Link = %#v", got)
	}
}

func TestInfoJSONNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/missing/info.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestImageRequestHasCanonicalLink(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	found := false
	for _, v := range resp.Header.Values("Link") {
		if v == `<http://example.test/iiif/3/sample.png/full/max/0/default.png>;rel="canonical"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("Link = %#v", resp.Header.Values("Link"))
	}
	if got := resp.Header.Get("ETag"); got == "" {
		t.Fatal("missing ETag")
	}
	if got := resp.Header.Get("X-Cache"); got != "miss" {
		t.Fatalf("X-Cache = %q", got)
	}
}

func TestBadSyntax(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/bogus.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestImageRequestIfNoneMatch(t *testing.T) {
	store := newMemoryStore()
	srv, _ := setupTestServerWithCache(t, store)
	defer srv.Close()

	first, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag on first response")
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/iiif/3/sample.png/full/max/0/default.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("second status = %d", second.StatusCode)
	}
	b, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Fatalf("expected empty body, got %d bytes", len(b))
	}
	if got := second.Header.Get("ETag"); got != etag {
		t.Fatalf("ETag = %q, want %q", got, etag)
	}
	if got := second.Header.Get("X-Cache"); got != "" {
		t.Fatalf("X-Cache = %q, want empty on 304", got)
	}
}

type memoryStore struct {
	data map[string][]byte
	meta map[string]cache.Entry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		data: map[string][]byte{},
		meta: map[string]cache.Entry{},
	}
}

func (m *memoryStore) Get(_ context.Context, key string) (io.ReadCloser, cache.Entry, error) {
	b, ok := m.data[key]
	if !ok {
		return nil, cache.Entry{}, cache.ErrMiss
	}
	return io.NopCloser(bytes.NewReader(b)), m.meta[key], nil
}

func (m *memoryStore) Put(_ context.Context, key, contentType string, value io.Reader) error {
	b, err := io.ReadAll(value)
	if err != nil {
		return err
	}
	m.data[key] = b
	m.meta[key] = cache.Entry{
		ContentType: contentType,
		Size:        int64(len(b)),
		StoredAt:    time.Now(),
	}
	return nil
}

func (m *memoryStore) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	delete(m.meta, key)
	return nil
}
