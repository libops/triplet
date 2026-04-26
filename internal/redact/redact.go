// Package redact provides small helpers for keeping sensitive request values
// out of logs and error strings.
package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// Identifier returns an identifier safe enough for logs.
func Identifier(identifier string) string {
	u, err := url.Parse(identifier)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return identifier
	}
	if u.User != nil {
		u.User = url.UserPassword("redacted", "redacted")
	}
	if u.RawQuery != "" {
		u.RawQuery = "redacted"
	}
	if u.Fragment != "" {
		u.Fragment = "redacted"
	}
	return u.Redacted()
}

// Hash returns a stable short hash for correlating redacted values.
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// Path returns a best-effort redacted request path for access logs.
func Path(path string) string {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	if !strings.Contains(decoded, "http://") && !strings.Contains(decoded, "https://") {
		return path
	}
	if q := strings.IndexByte(decoded, '?'); q >= 0 {
		return decoded[:q] + "?redacted"
	}
	return decoded
}
