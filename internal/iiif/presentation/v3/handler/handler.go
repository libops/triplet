package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/libops/triplet/internal/iiif/presentation/v3/store"
	"github.com/libops/triplet/internal/iiif/presentation/v3/validate"
)

// Handler serves a minimal Presentation API v3 surface.
type Handler struct {
	prefix string
	store  store.Store
	logger *slog.Logger
}

// New constructs a presentation handler mounted at prefix.
func New(prefix string, st store.Store, logger *slog.Logger) *Handler {
	return &Handler{
		prefix: strings.TrimRight(prefix, "/"),
		store:  st,
		logger: logger,
	}
}

// Register attaches the handler to mux at the configured prefix.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(h.prefix+"/", h)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodOptions:
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodOptions {
		writeCORS(w)
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, h.prefix)
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 2 && parts[1] == "manifest":
		h.serveManifest(w, r, parts[0])
	case len(parts) == 4 && parts[1] == "canvas" && parts[3] == "annotations":
		h.serveAnnotationPage(w, r, parts[0], parts[2])
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (h *Handler) serveManifest(w http.ResponseWriter, r *http.Request, rawItemID string) {
	itemID, err := url.PathUnescape(rawItemID)
	if err != nil || itemID == "" {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	body, err := h.store.GetManifest(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "manifest not found")
			return
		}
		h.logger.Error("read manifest", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read manifest")
		return
	}
	if err := validate.ValidateManifestBytes(body); err != nil {
		h.logger.Error("validate manifest", "item_id", itemID, "err", err)
		writeError(w, http.StatusInternalServerError, "invalid manifest")
		return
	}
	writeDocumentHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		h.logger.Warn("write manifest", "item_id", itemID, "err", err)
	}
}

func (h *Handler) serveAnnotationPage(w http.ResponseWriter, r *http.Request, rawItemID, rawCanvasID string) {
	itemID, err := url.PathUnescape(rawItemID)
	if err != nil || itemID == "" {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	canvasID, err := url.PathUnescape(rawCanvasID)
	if err != nil || canvasID == "" {
		writeError(w, http.StatusBadRequest, "invalid canvas id")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.getAnnotationPage(w, r, itemID, canvasID)
	case http.MethodPut:
		h.putAnnotationPage(w, r, itemID, canvasID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) getAnnotationPage(w http.ResponseWriter, r *http.Request, itemID, canvasID string) {
	body, err := h.store.GetAnnotationPage(r.Context(), itemID, canvasID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "annotation page not found")
			return
		}
		h.logger.Error("read annotation page", "item_id", itemID, "canvas_id", canvasID, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read annotation page")
		return
	}
	if err := validate.ValidateAnnotationPageBytes(body); err != nil {
		h.logger.Error("validate annotation page", "item_id", itemID, "canvas_id", canvasID, "err", err)
		writeError(w, http.StatusInternalServerError, "invalid annotation page")
		return
	}
	writeDocumentHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		h.logger.Warn("write annotation page", "item_id", itemID, "canvas_id", canvasID, "err", err)
	}
}

func (h *Handler) putAnnotationPage(w http.ResponseWriter, r *http.Request, itemID, canvasID string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.ValidateAnnotationPageBytes(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.PutAnnotationPage(r.Context(), itemID, canvasID, body); err != nil {
		h.logger.Error("write annotation page", "item_id", itemID, "canvas_id", canvasID, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to store annotation page")
		return
	}
	writeCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeDocumentHeaders(w http.ResponseWriter) {
	writeCORS(w)
	w.Header().Set("Content-Type", `application/ld+json;profile="http://iiif.io/api/presentation/3/context.json"`)
}

func writeCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeCORS(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
