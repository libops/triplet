package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	return setupTestServerWithOptions(t, derivCache, nil)
}

func setupTestServerWithAllowedOrigins(t *testing.T, allowedOrigins []string) (*httptest.Server, string) {
	return setupTestServerWithOptions(t, cache.Noop{}, allowedOrigins)
}

func setupTestServerWithOptions(t *testing.T, derivCache cache.Store, allowedOrigins []string) (*httptest.Server, string) {
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
		allowedOrigins,
		types.Limits{MaxArea: 10_000_000, MaxWidth: 4096, MaxHeight: 4096},
		true,
		250_000_000,
		1<<30,
		2,
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

func TestBaseRedirectsAbsoluteURIIdentifierToInfo(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	identifier := "https%3A%2F%2Fislandora-stage.lib.lehigh.edu%2F_flysystem%2Ffedora%2F2024-01%2F305725.tiff"
	resp, err := client.Get(srv.URL + "/iiif/3/" + identifier)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "/iiif/3/"+identifier+"/info.json"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
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
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
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

func TestSlashedIdentifierImageRequestNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/a/b/full/max/0/default.jpg")
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
	foundProfile := false
	for _, v := range resp.Header.Values("Link") {
		if v == `<http://example.test/iiif/3/sample.png/full/max/0/default.png>;rel="canonical"` {
			found = true
		}
		if v == profileLinkHeader {
			foundProfile = true
		}
	}
	if !found {
		t.Fatalf("Link = %#v", resp.Header.Values("Link"))
	}
	if !foundProfile {
		t.Fatalf("Link = %#v", resp.Header.Values("Link"))
	}
	if got := resp.Header.Get("ETag"); got == "" {
		t.Fatal("missing ETag")
	}
	if got := resp.Header.Get("X-Cache"); got != "miss" {
		t.Fatalf("X-Cache = %q", got)
	}
}

func TestPipelineErrorUsesGenericResponse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.png"), []byte("not an image"), 0o600); err != nil {
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
		cache.Noop{},
		nil,
		types.Limits{MaxArea: 10_000_000},
		true,
		250_000_000,
		1<<30,
		2,
		logger,
	)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/iiif/3/bad.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "vips") || !strings.Contains(string(b), "failed to transform image") {
		t.Fatalf("body = %q", string(b))
	}
}

func TestDerivativeCacheFailureWarns(t *testing.T) {
	var logs bytes.Buffer
	root := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
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
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := New(
		"/iiif/3",
		"http://example.test",
		op,
		pipeline.New(op, pipeline.Limits{MaxOutputPixels: 10_000_000}),
		failingCache{},
		nil,
		types.Limits{MaxArea: 10_000_000},
		true,
		250_000_000,
		1<<30,
		2,
		logger,
	)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := logs.String()
	if !strings.Contains(got, "derivative cache get") || !strings.Contains(got, "derivative cache put") {
		t.Fatalf("logs = %q", got)
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	srv, _ := setupTestServerWithAllowedOrigins(t, []string{"https://viewer.example.edu"})
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/iiif/3/sample.png/full/max/0/default.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://viewer.example.edu")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://viewer.example.edu" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != exposeHeaders {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
	if got := resp.Header.Values("Vary"); len(got) != 1 || got[0] != "Origin" {
		t.Fatalf("Vary = %#v", got)
	}
}

func TestCORSWildcardAllowedOrigin(t *testing.T) {
	srv, _ := setupTestServerWithAllowedOrigins(t, []string{"*"})
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/iiif/3/sample.png/info.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://viewer.example.edu")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != exposeHeaders {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
}

func TestCORSDisallowedOrigin(t *testing.T) {
	srv, _ := setupTestServerWithAllowedOrigins(t, []string{"https://viewer.example.edu"})
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/iiif/3/sample.png/info.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://other.example.edu")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != "" {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
}

func TestUpscaleWithoutCaretReturnsBadRequest(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/iiif/3/sample.png/full/201,101/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
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
	foundCanonical := false
	foundProfile := false
	for _, v := range second.Header.Values("Link") {
		if v == `<http://example.test/iiif/3/sample.png/full/max/0/default.png>;rel="canonical"` {
			foundCanonical = true
		}
		if v == profileLinkHeader {
			foundProfile = true
		}
	}
	if !foundCanonical || !foundProfile {
		t.Fatalf("Link = %#v", second.Header.Values("Link"))
	}
	if got := second.Header.Get("X-Cache"); got != "" {
		t.Fatalf("X-Cache = %q, want empty on 304", got)
	}
}

func TestImageRequestETagChangesWhenSourceChanges(t *testing.T) {
	store := newMemoryStore()
	srv, root := setupTestServerWithCache(t, store)
	defer srv.Close()

	first, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d", first.StatusCode)
	}
	firstETag := first.Header.Get("ETag")
	if firstETag == "" {
		t.Fatal("missing first ETag")
	}

	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sample.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := time.Now().Add(time.Second).Truncate(time.Nanosecond)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}

	second, err := http.Get(srv.URL + "/iiif/3/sample.png/full/max/0/default.png")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d", second.StatusCode)
	}
	if got := second.Header.Get("ETag"); got == "" || got == firstETag {
		t.Fatalf("second ETag = %q, first = %q", got, firstETag)
	}
	if got := second.Header.Get("X-Cache"); got != "miss" {
		t.Fatalf("X-Cache = %q", got)
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

type failingCache struct{}

func (failingCache) Get(context.Context, string) (io.ReadCloser, cache.Entry, error) {
	return nil, cache.Entry{}, errCacheFailure
}

func (failingCache) Put(context.Context, string, string, io.Reader) error {
	return errCacheFailure
}

func (failingCache) Delete(context.Context, string) error {
	return nil
}

var errCacheFailure = errors.New("cache failed")
