package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPolicySetHeadersAllowsHost(t *testing.T) {
	p := New([]string{"viewer.example.edu"}, "ETag")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://viewer.example.edu")
	rec := httptest.NewRecorder()

	p.SetHeaders(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://viewer.example.edu" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "ETag" {
		t.Fatalf("Access-Control-Expose-Headers = %q", got)
	}
	if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Origin" {
		t.Fatalf("Vary = %#v", got)
	}
}

func TestPolicySetHeadersAllowsWildcard(t *testing.T) {
	p := New([]string{"*"}, "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://viewer.example.edu")
	rec := httptest.NewRecorder()

	p.SetHeaders(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary = %q", got)
	}
}

func TestPolicySetHeadersRejectsDisallowedOrigin(t *testing.T) {
	p := New([]string{"viewer.example.edu"}, "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://other.example.edu")
	rec := httptest.NewRecorder()

	p.SetHeaders(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}
