package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoggingMiddlewareIncludesRemoteClientIPAndUserAgent(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/iiif/3/example/info.json", nil)
	req.RemoteAddr = "198.51.100.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 192.0.2.10")
	req.Header.Set("User-Agent", "triplet-test/1.0")

	rr := httptest.NewRecorder()
	LoggingMiddleware(logger)(next).ServeHTTP(rr, req)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if got := entry["client_ip"]; got != "198.51.100.10" {
		t.Fatalf("client_ip = %#v, want %q", got, "198.51.100.10")
	}
	if got := entry["user_agent"]; got != "triplet-test/1.0" {
		t.Fatalf("user_agent = %#v, want %q", got, "triplet-test/1.0")
	}
}

func TestLoggingMiddlewareCanTrustForwardedClientIP(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/iiif/3/example/info.json", nil)
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 192.0.2.10")
	_, trusted, err := net.ParseCIDR("192.0.2.0/24")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	LoggingMiddleware(logger, LoggingOptions{TrustedProxies: []*net.IPNet{trusted}})(next).ServeHTTP(rr, req)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if got := entry["client_ip"]; got != "203.0.113.9" {
		t.Fatalf("client_ip = %#v, want %q", got, "203.0.113.9")
	}
}

func TestClientIPFallsBackToRemoteAddrHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.10:12345"

	if got := clientIP(req, nil); got != "192.0.2.10" {
		t.Fatalf("clientIP = %q, want %q", got, "192.0.2.10")
	}
}

func TestLoggingMiddlewareSkipsHealthcheck(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	LoggingMiddleware(logger)(next).ServeHTTP(rr, req)

	if logs.Len() != 0 {
		t.Fatalf("healthcheck log = %q, want empty", logs.String())
	}
}
