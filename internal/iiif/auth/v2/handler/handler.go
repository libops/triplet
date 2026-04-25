package handler

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/libops/triplet/internal/iiif/auth/v2/authorizer"
	"github.com/libops/triplet/internal/iiif/auth/v2/types"
)

type Handler struct {
	prefix        string
	publicBaseURL string
	authz         authorizer.Authorizer
	logger        *slog.Logger
}

func New(prefix, publicBaseURL string, authz authorizer.Authorizer, logger *slog.Logger) *Handler {
	return &Handler{
		prefix:        strings.TrimRight(prefix, "/"),
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		authz:         authz,
		logger:        logger,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(h.prefix+"/", h)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeCORS(w)
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, h.prefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	itemID, err := url.PathUnescape(parts[0])
	if err != nil || itemID == "" {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	switch parts[1] {
	case "probe":
		h.probe(w, r, itemID)
	case "access":
		h.access(w, r, itemID)
	case "token":
		h.token(w, r, itemID)
	case "logout":
		h.logout(w, r, itemID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) probe(w http.ResponseWriter, r *http.Request, itemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := h.authz.Probe(r.Context(), authorizer.Request{ItemID: itemID, Token: bearerToken(r)})
	if err != nil {
		h.logger.Error("auth probe", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "probe failed")
		return
	}
	writeJSONHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(types.ProbeResult{
		Context: types.ContextAuth2,
		Type:    types.TypeProbeResult,
		Status:  status,
	})
}

func (h *Handler) access(w http.ResponseWriter, r *http.Request, itemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeCORS(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Access granted</title><script>window.close()</script><p>Access granted for ` + html.EscapeString(itemID) + `.</p>`))
}

func (h *Handler) token(w http.ResponseWriter, r *http.Request, itemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token, expiresIn, err := h.authz.Token(r.Context(), itemID, r)
	if err != nil {
		h.logger.Error("auth token", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "token failed")
		return
	}
	writeJSONHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(types.TokenResult{
		Context:     types.ContextAuth2,
		AccessToken: token,
		ExpiresIn:   expiresIn,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, itemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.authz.Logout(r.Context(), itemID, r); err != nil {
		h.logger.Error("auth logout", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "logout failed")
		return
	}
	writeCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

func writeJSONHeaders(w http.ResponseWriter) {
	writeCORS(w)
	w.Header().Set("Content-Type", `application/ld+json;profile="http://iiif.io/api/auth/2/context.json"`)
}

func writeCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeCORS(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

type errorResponse struct {
	Error string `json:"error"`
}
