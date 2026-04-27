package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/libops/triplet/internal/cors"
	"github.com/libops/triplet/internal/iiif/auth/v2/authorizer"
	"github.com/libops/triplet/internal/iiif/auth/v2/types"
)

func setupAuthServer() *httptest.Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New("/auth/v2", "http://example.test", authorizer.PermitAll{}, cors.New(nil, ""), logger)
	mux := http.NewServeMux()
	h.Register(mux)
	return httptest.NewServer(mux)
}

func TestProbePermitAll(t *testing.T) {
	srv := setupAuthServer()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/auth/v2/item-1/probe")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got types.ProbeResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Context != types.ContextAuth2 || got.Type != types.TypeProbeResult || got.Status != http.StatusOK {
		t.Fatalf("probe = %#v", got)
	}
}

func TestTokenPermitAll(t *testing.T) {
	srv := setupAuthServer()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/auth/v2/item-1/token")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got types.TokenResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Context != types.ContextAuth2 {
		t.Fatalf("token = %#v", got)
	}
}

func TestRejectsOverlongItemID(t *testing.T) {
	srv := setupAuthServer()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/auth/v2/" + strings.Repeat("a", 256) + "/probe")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
