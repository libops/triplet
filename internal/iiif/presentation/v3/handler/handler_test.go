package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libops/triplet/internal/cors"
	pstore "github.com/libops/triplet/internal/iiif/presentation/v3/store"
)

func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return setupTestServerWithWrites(t, false, "")
}

func setupTestServerWithWrites(t *testing.T, writeEnabled bool, writeToken string) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	itemDir := filepath.Join(root, "item-1")
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/presentation/v3/item-1/manifest","type":"Manifest","label":{"en":["Item 1"]},"items":[{"id":"http://example.test/presentation/v3/item-1/canvas/1","type":"Canvas"}]}`
	if err := os.WriteFile(filepath.Join(itemDir, "manifest.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	annoDir := filepath.Join(itemDir, "canvas", "canvas-1")
	if err := os.MkdirAll(annoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	anno := `{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"http://example.test/presentation/v3/item-1/canvas/canvas-1/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/annotations/1","type":"Annotation","textGranularity":"line","motivation":["supplementing"],"body":{"type":"TextualBody","value":"hello"},"target":{"type":"SpecificResource","source":"http://example.test/presentation/v3/item-1/canvas/canvas-1","selector":{"type":"FragmentSelector","value":"xywh=1,2,3,4"}}}]}`
	if err := os.WriteFile(filepath.Join(annoDir, "annotations.json"), []byte(anno), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := pstore.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New("/presentation/v3", st, cors.New(nil, ""), writeEnabled, writeToken, logger)
	mux := http.NewServeMux()
	h.Register(mux)
	return httptest.NewServer(mux)
}

func TestManifest(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/presentation/v3/item-1/manifest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got == "" {
		t.Fatal("missing content-type")
	}
}

func TestManifestPutMethodNotAllowed(t *testing.T) {
	srv := setupTestServerWithWrites(t, true, "test-token")
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/presentation/v3/item-1/manifest", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAnnotationPageGet(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/presentation/v3/item-1/canvas/canvas-1/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAnnotationPageHead(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodHead, srv.URL+"/presentation/v3/item-1/canvas/canvas-1/annotations", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Fatalf("expected empty body, got %q", string(b))
	}
}

func TestAnnotationPagePut(t *testing.T) {
	srv := setupTestServerWithWrites(t, true, "test-token")
	defer srv.Close()
	body := `{"@context":["http://iiif.io/api/extension/text-granularity/context.json","http://iiif.io/api/presentation/3/context.json"],"id":"http://example.test/presentation/v3/item-1/canvas/canvas-2/annotations","type":"AnnotationPage","items":[{"id":"http://example.test/annotations/2","type":"Annotation","textGranularity":"line","motivation":["supplementing"],"body":{"type":"TextualBody","value":"world"},"target":{"type":"SpecificResource","source":"http://example.test/presentation/v3/item-1/canvas/canvas-2","selector":{"type":"FragmentSelector","value":"xywh=5,6,7,8"}}}]}`
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/presentation/v3/item-1/canvas/canvas-2/annotations", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/ld+json")
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	getResp, err := http.Get(srv.URL + "/presentation/v3/item-1/canvas/canvas-2/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", getResp.StatusCode)
	}
}

func TestAnnotationPagePutDisabled(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/presentation/v3/item-1/canvas/canvas-2/annotations", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAnnotationPagePutRequiresBearerToken(t *testing.T) {
	srv := setupTestServerWithWrites(t, true, "test-token")
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/presentation/v3/item-1/canvas/canvas-2/annotations", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAnnotationPagePutValidationError(t *testing.T) {
	srv := setupTestServerWithWrites(t, true, "test-token")
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/presentation/v3/item-1/canvas/canvas-2/annotations", strings.NewReader(`{"type":"AnnotationPage"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got["error"] == "" {
		t.Fatal("missing error message")
	}
}

func TestManifestNotFound(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/presentation/v3/missing/manifest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAnnotationPageNotFound(t *testing.T) {
	srv := setupTestServer(t)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/presentation/v3/item-1/canvas/missing/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
