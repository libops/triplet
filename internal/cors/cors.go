// Package cors applies a small allowlist-based CORS policy.
package cors

import (
	"net/http"
	"strings"
)

// Policy controls CORS response headers.
type Policy struct {
	AllowedOrigins []string
	ExposeHeaders  string
}

// New constructs a Policy from an allowed origin list.
func New(allowedOrigins []string, exposeHeaders string) Policy {
	return Policy{
		AllowedOrigins: append([]string(nil), allowedOrigins...),
		ExposeHeaders:  exposeHeaders,
	}
}

// SetHeaders applies CORS response headers when the request Origin is allowed.
func (p Policy) SetHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || len(p.AllowedOrigins) == 0 {
		return
	}
	if !p.originAllowed(origin) {
		return
	}
	if p.allowsAnyOrigin() {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	if p.ExposeHeaders != "" {
		w.Header().Set("Access-Control-Expose-Headers", p.ExposeHeaders)
	}
}

func (p Policy) allowsAnyOrigin() bool {
	for _, allowed := range p.AllowedOrigins {
		if strings.TrimSpace(allowed) == "*" {
			return true
		}
	}
	return false
}

func (p Policy) originAllowed(origin string) bool {
	if p.allowsAnyOrigin() {
		return true
	}
	for _, allowed := range p.AllowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == origin {
			return true
		}
	}
	return false
}
