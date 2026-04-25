package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/libops/triplet/internal/iiif/search/v2/searcher"
)

// Handler serves the IIIF Content Search API 2.0 surface.
type Handler struct {
	prefix        string
	publicBaseURL string
	searcher      searcher.Searcher
	logger        *slog.Logger
}

func New(prefix, publicBaseURL string, s searcher.Searcher, logger *slog.Logger) *Handler {
	return &Handler{
		prefix:        strings.TrimRight(prefix, "/"),
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		searcher:      s,
		logger:        logger,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(h.prefix+"/", h)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodOptions {
		writeCORS(w)
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, h.prefix)
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "search" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	itemID, err := url.PathUnescape(parts[0])
	if err != nil || itemID == "" {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "missing q")
		return
	}

	page, err := h.searcher.Search(r.Context(), searcher.Request{
		ItemID: itemID,
		Query:  query,
	})
	if err != nil {
		h.logger.Error("search", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	if page.ID == "" {
		page.ID = h.publicBaseURL + r.URL.RequestURI()
	}

	writeDocumentHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(page); err != nil {
		h.logger.Warn("write search response", "item_id", itemID, "err", err)
	}
}

func writeDocumentHeaders(w http.ResponseWriter) {
	writeCORS(w)
	w.Header().Set("Content-Type", `application/ld+json;profile="http://iiif.io/api/search/2/context.json"`)
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
