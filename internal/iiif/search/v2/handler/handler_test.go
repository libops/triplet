package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/libops/triplet/internal/iiif/search/v2/searcher"
	"github.com/libops/triplet/internal/iiif/search/v2/types"
)

func setupTestServer() *httptest.Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New("/search/v2", "http://example.test", searcher.Noop{}, logger)
	mux := http.NewServeMux()
	h.Register(mux)
	return httptest.NewServer(mux)
}

func TestSearchNoop(t *testing.T) {
	srv := setupTestServer()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/search/v2/item-1/search?q=needle")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got == "" {
		t.Fatal("missing content-type")
	}
	var page types.AnnotationPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Context != types.ContextSearch2 {
		t.Fatalf("context = %#v", page.Context)
	}
	if page.Type != types.TypeAnnotationPage {
		t.Fatalf("type = %q", page.Type)
	}
	if page.ID != "http://example.test/search/v2/item-1/search?q=needle" {
		t.Fatalf("id = %q", page.ID)
	}
	if len(page.Items) != 0 {
		t.Fatalf("items = %#v", page.Items)
	}
}

func TestSearchHead(t *testing.T) {
	srv := setupTestServer()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/search/v2/item-1/search?q=needle", nil)
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

func TestSearchRequiresQuery(t *testing.T) {
	srv := setupTestServer()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/search/v2/item-1/search")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
