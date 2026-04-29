// Package observability wires structured logging and request middleware.
package observability

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/libops/triplet/internal/redact"
)

type ctxKey int

const requestIDKey ctxKey = iota

// NewLogger builds a slog.Logger from the configured level and format.
//
// format is "json" or "text"; level is "debug" | "info" | "warn" | "error".
// Unknown values fall back to JSON / Info.
func NewLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

// RequestID returns the request ID stored on ctx, or "" if absent.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// LoggingMiddleware logs one structured line per HTTP request, including the
// status code, byte count, latency, and request ID. The request ID comes from
// an inbound X-Request-Id header or is generated.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rid := r.Header.Get("X-Request-Id")
			if rid == "" {
				rid = newRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, rid)
			ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r.WithContext(ctx))
			if r.URL.Path == "/healthz" {
				return
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "http",
				slog.String("request_id", rid),
				slog.String("method", r.Method),
				slog.String("path", redact.Path(r.URL.Path)),
				slog.String("client_ip", clientIP(r)),
				slog.String("user_agent", r.UserAgent()),
				slog.Int("status", ww.status),
				slog.Int64("bytes", ww.bytes),
				slog.Duration("latency", time.Since(start)),
			)
		})
	}
}

// RecoverMiddleware turns a panic in any downstream handler into a logged
// 500 instead of tearing down the server process.
func RecoverMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					logger.Error("panic",
						slog.Any("value", rv),
						slog.String("path", redact.Path(r.URL.Path)),
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		if ip = strings.TrimSpace(ip); ip != "" {
			return ip
		}
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func newRequestID() string {
	// 8 bytes of crypto/rand-style uniqueness without pulling crypto/rand into
	// the hot path: time-based hex is sufficient for log correlation.
	now := time.Now().UnixNano()
	const hex = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		out[15-i] = hex[now&0xf]
		now >>= 4
	}
	return string(out)
}
