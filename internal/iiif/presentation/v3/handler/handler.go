package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/libops/triplet/internal/cors"
	"github.com/libops/triplet/internal/iiif/presentation/v3/store"
	"github.com/libops/triplet/internal/iiif/presentation/v3/validate"
	"github.com/libops/triplet/internal/redact"
)

// Handler serves a minimal Presentation API v3 surface.
type Handler struct {
	prefix       string
	store        store.Store
	cors         cors.Policy
	writeEnabled bool
	writeToken   string
	logger       *slog.Logger
}

// New constructs a presentation handler mounted at prefix.
func New(prefix string, st store.Store, corsPolicy cors.Policy, writeEnabled bool, writeToken string, logger *slog.Logger) *Handler {
	return &Handler{
		prefix:       strings.TrimRight(prefix, "/"),
		store:        st,
		cors:         corsPolicy,
		writeEnabled: writeEnabled,
		writeToken:   writeToken,
		logger:       logger,
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
		h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Method == http.MethodOptions {
		h.writeCORS(w, r)
		methods := "GET, HEAD, OPTIONS"
		if h.writeEnabled {
			methods = "GET, HEAD, PUT, OPTIONS"
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		} else {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		w.Header().Set("Access-Control-Allow-Methods", methods)
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
		h.writeError(w, r, http.StatusNotFound, "not found")
	}
}

func (h *Handler) serveManifest(w http.ResponseWriter, r *http.Request, rawItemID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	itemID, err := url.PathUnescape(rawItemID)
	if err != nil || !validRequestID(itemID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid item id")
		return
	}
	body, err := h.store.GetManifest(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.writeError(w, r, http.StatusNotFound, "manifest not found")
			return
		}
		h.logger.Error("read manifest", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "err", err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to read manifest")
		return
	}
	if err := validate.ValidateManifestBytes(body); err != nil {
		h.logger.Error("validate manifest", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "err", err)
		h.writeError(w, r, http.StatusInternalServerError, "invalid manifest")
		return
	}
	h.writeDocumentHeaders(w, r)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		h.logger.Warn("write manifest", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "err", err)
	}
}

func (h *Handler) serveAnnotationPage(w http.ResponseWriter, r *http.Request, rawItemID, rawCanvasID string) {
	itemID, err := url.PathUnescape(rawItemID)
	if err != nil || !validRequestID(itemID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid item id")
		return
	}
	canvasID, err := url.PathUnescape(rawCanvasID)
	if err != nil || !validRequestID(canvasID) {
		h.writeError(w, r, http.StatusBadRequest, "invalid canvas id")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.getAnnotationPage(w, r, itemID, canvasID)
	case http.MethodPut:
		if !h.writeEnabled {
			h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if !h.authorizedWrite(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="triplet-presentation"`)
			h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.putAnnotationPage(w, r, itemID, canvasID)
	default:
		h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) getAnnotationPage(w http.ResponseWriter, r *http.Request, itemID, canvasID string) {
	body, err := h.store.GetAnnotationPage(r.Context(), itemID, canvasID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.writeError(w, r, http.StatusNotFound, "annotation page not found")
			return
		}
		h.logger.Error("read annotation page", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "canvas_id", redact.Identifier(canvasID), "canvas_id_hash", redact.Hash(canvasID), "err", err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to read annotation page")
		return
	}
	if err := validate.ValidateAnnotationPageBytes(body); err != nil {
		h.logger.Error("validate annotation page", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "canvas_id", redact.Identifier(canvasID), "canvas_id_hash", redact.Hash(canvasID), "err", err)
		h.writeError(w, r, http.StatusInternalServerError, "invalid annotation page")
		return
	}
	h.writeDocumentHeaders(w, r)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(body); err != nil {
		h.logger.Warn("write annotation page", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "canvas_id", redact.Identifier(canvasID), "canvas_id_hash", redact.Hash(canvasID), "err", err)
	}
}

func (h *Handler) putAnnotationPage(w http.ResponseWriter, r *http.Request, itemID, canvasID string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.ValidateAnnotationPageBytes(body); err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.PutAnnotationPage(r.Context(), itemID, canvasID, body); err != nil {
		h.logger.Error("write annotation page", "item_id", redact.Identifier(itemID), "item_id_hash", redact.Hash(itemID), "canvas_id", redact.Identifier(canvasID), "canvas_id_hash", redact.Hash(canvasID), "err", err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to store annotation page")
		return
	}
	h.writeCORS(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeDocumentHeaders(w http.ResponseWriter, r *http.Request) {
	h.writeCORS(w, r)
	w.Header().Set("Content-Type", `application/ld+json;profile="http://iiif.io/api/presentation/3/context.json"`)
}

func (h *Handler) writeCORS(w http.ResponseWriter, r *http.Request) {
	h.cors.SetHeaders(w, r)
}

func (h *Handler) authorizedWrite(r *http.Request) bool {
	token := bearerToken(r)
	if token == "" || h.writeToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.writeToken)) == 1
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

func validRequestID(id string) bool {
	return id != "" && len(id) <= 255 && !strings.ContainsAny(id, "\x00\n\r")
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	h.writeCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}

type errorResponse struct {
	Error string `json:"error"`
}
