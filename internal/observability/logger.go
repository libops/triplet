// Package observability wires structured logging and request middleware.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
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

// LoggingOptions controls request log attribution behavior.
type LoggingOptions struct {
	// TrustedProxies allows X-Forwarded-For and X-Real-IP to supply client_ip
	// only when RemoteAddr is inside one of these CIDR ranges.
	TrustedProxies []*net.IPNet
}

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
func LoggingMiddleware(logger *slog.Logger, opts ...LoggingOptions) func(http.Handler) http.Handler {
	cfg := LoggingOptions{}
	if len(opts) > 0 {
		cfg = opts[0]
	}
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
				slog.String("client_ip", clientIP(r, cfg.TrustedProxies)),
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

func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	remote := remoteIP(r.RemoteAddr)
	if trustedProxy(remote, trustedProxies) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip, _, _ := strings.Cut(xff, ",")
			if ip = strings.TrimSpace(ip); ip != "" {
				return ip
			}
		}
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	if remote != nil {
		return remote.String()
	}
	return r.RemoteAddr
}

func remoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func trustedProxy(ip net.IP, cidrs []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	binary.BigEndian.PutUint64(b[8:], uint64(time.Now().UnixNano()))
	return hex.EncodeToString(b[:])
}
