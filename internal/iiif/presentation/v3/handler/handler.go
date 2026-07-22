package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/libops/triplet/internal/cors"
	"github.com/libops/triplet/internal/iiif/presentation/v3/store"
	"github.com/libops/triplet/internal/iiif/presentation/v3/validate"
	"github.com/libops/triplet/internal/redact"
)

const (
	maxWriteBodyBytes = 8 << 20
	documentMediaType = `application/ld+json;profile="http://iiif.io/api/presentation/3/context.json"`
)

// Handler serves byte-exact IIIF Presentation API v3 resources beneath a
// configured public prefix.
type Handler struct {
	prefix        string
	publicBaseURL string
	store         store.Store
	cors          cors.Policy
	writeEnabled  bool
	writeToken    string
	logger        *slog.Logger
}

// New constructs a Presentation handler mounted at prefix. publicBaseURL is
// authoritative when matching a resource's JSON-LD id to its request URL.
func New(prefix, publicBaseURL string, st store.Store, corsPolicy cors.Policy, writeEnabled bool, writeToken string, logger *slog.Logger) *Handler {
	return &Handler{
		prefix:        strings.TrimRight(prefix, "/"),
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		store:         st,
		cors:          corsPolicy,
		writeEnabled:  writeEnabled,
		writeToken:    writeToken,
		logger:        logger,
	}
}

// Register attaches the handler to mux at the configured prefix.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(h.prefix+"/", h)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		h.writeOptions(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		h.writeMethodNotAllowed(w, r)
		return
	}
	resourceKey, publicID, err := h.requestResource(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.getResource(w, r, resourceKey, publicID)
	case http.MethodPut, http.MethodDelete:
		if !h.writeEnabled {
			h.writeMethodNotAllowed(w, r)
			return
		}
		if !h.authorizedWrite(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="triplet-presentation"`)
			h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method == http.MethodPut {
			h.putResource(w, r, resourceKey, publicID)
			return
		}
		h.deleteResource(w, r, resourceKey)
	}
}

func (h *Handler) getResource(w http.ResponseWriter, r *http.Request, resourceKey, publicID string) {
	document, err := h.store.Get(r.Context(), resourceKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.writeError(w, r, http.StatusNotFound, "presentation resource not found")
			return
		}
		h.logStoreError("read presentation resource", resourceKey, err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to read presentation resource")
		return
	}
	resource, err := validate.ValidateResourceBytes(document.Body)
	if err != nil || resource.ID != publicID {
		validationErr := validationError(err, resource.ID, publicID)
		h.logger.Error("validate stored presentation resource", "resource_key_hash", redact.Hash(resourceKey), "validation_error_hash", redact.Hash(validationErr.Error()))
		h.writeError(w, r, http.StatusInternalServerError, "invalid stored presentation resource")
		return
	}

	etag := store.DocumentETag(document.Body)
	h.writeDocumentHeaders(w, r)
	w.Header().Set("ETag", etag)
	if !document.ModifiedAt.IsZero() {
		w.Header().Set("Last-Modified", document.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	if requestNotModified(r, etag, document.ModifiedAt) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(document.Body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(document.Body); err != nil {
		h.logger.Warn("write presentation resource", "resource_key_hash", redact.Hash(resourceKey), "resource_type", resource.Type, "err", err)
	}
}

func (h *Handler) putResource(w http.ResponseWriter, r *http.Request, resourceKey, publicID string) {
	if !supportedContentType(r.Header.Get("Content-Type")) {
		h.writeError(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json or application/ld+json")
		return
	}
	conditions, status, err := putPreconditions(r)
	if err != nil {
		h.writeError(w, r, status, err.Error())
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriteBodyBytes))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "presentation resource exceeds the 8 MiB limit")
			return
		}
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}
	resource, err := validate.ValidateResourceBytes(body)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid presentation resource: "+err.Error())
		return
	}
	if resource.ID != publicID {
		h.writeError(w, r, http.StatusConflict, "presentation resource id must exactly match its public request URL")
		return
	}
	created, err := h.store.Put(r.Context(), resourceKey, body, conditions)
	if err != nil {
		if errors.Is(err, store.ErrPreconditionFailed) {
			h.writeError(w, r, http.StatusPreconditionFailed, "presentation resource precondition failed")
			return
		}
		h.logStoreError("write presentation resource", resourceKey, err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to store presentation resource")
		return
	}
	h.writeCORS(w, r)
	w.Header().Set("ETag", store.DocumentETag(body))
	if created {
		w.Header().Set("Location", publicID)
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteResource(w http.ResponseWriter, r *http.Request, resourceKey string) {
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" {
		h.writeError(w, r, http.StatusPreconditionRequired, "If-Match is required")
		return
	}
	if !validStrongIfMatch(ifMatch) {
		h.writeError(w, r, http.StatusBadRequest, "If-Match must contain strong entity tags or *")
		return
	}
	if err := h.store.Delete(r.Context(), resourceKey, ifMatch); err != nil {
		if errors.Is(err, store.ErrPreconditionFailed) || errors.Is(err, store.ErrNotFound) {
			h.writeError(w, r, http.StatusPreconditionFailed, "presentation resource precondition failed")
			return
		}
		h.logStoreError("delete presentation resource", resourceKey, err)
		h.writeError(w, r, http.StatusInternalServerError, "failed to delete presentation resource")
		return
	}
	h.writeCORS(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requestResource(r *http.Request) (resourceKey, publicID string, err error) {
	if r.URL.RawQuery != "" {
		return "", "", errors.New("presentation resource URLs must not contain a query")
	}
	escapedPath := r.URL.EscapedPath()
	escapedPrefix := (&url.URL{Path: h.prefix}).EscapedPath()
	if !strings.HasPrefix(escapedPath, escapedPrefix+"/") {
		return "", "", errors.New("invalid presentation resource path")
	}
	escapedRest := strings.TrimPrefix(escapedPath, escapedPrefix+"/")
	decodedRest := strings.TrimPrefix(r.URL.Path, h.prefix+"/")
	if escapedRest == "" || decodedRest == "" || strings.Contains(strings.ToLower(escapedRest), "%2f") || strings.Contains(strings.ToLower(escapedRest), "%5c") {
		return "", "", errors.New("invalid presentation resource path")
	}
	segments := strings.Split(decodedRest, "/")
	canonical := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || !utf8.ValidString(segment) || strings.ContainsAny(segment, "\\\x00\n\r") {
			return "", "", errors.New("invalid presentation resource path")
		}
		canonical = append(canonical, url.PathEscape(segment))
	}
	resourceKey = strings.Join(canonical, "/")
	if resourceKey != escapedRest || len(resourceKey) > store.MaxResourceKeyBytes {
		return "", "", errors.New("presentation resource path is not canonically escaped or is too long")
	}
	return resourceKey, h.publicBaseURL + h.prefix + "/" + resourceKey, nil
}

func putPreconditions(r *http.Request) (store.Preconditions, int, error) {
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match"))
	if ifMatch == "" && ifNoneMatch == "" {
		return store.Preconditions{}, http.StatusPreconditionRequired, errors.New("If-None-Match: * is required to create, or If-Match is required to replace")
	}
	if ifMatch != "" && ifNoneMatch != "" {
		return store.Preconditions{}, http.StatusBadRequest, errors.New("If-Match and If-None-Match cannot be combined")
	}
	if ifNoneMatch != "" && ifNoneMatch != "*" {
		return store.Preconditions{}, http.StatusBadRequest, errors.New("If-None-Match must be * for a conditional create")
	}
	if ifMatch != "" && !validStrongIfMatch(ifMatch) {
		return store.Preconditions{}, http.StatusBadRequest, errors.New("If-Match must contain strong entity tags or *")
	}
	return store.Preconditions{IfMatch: ifMatch, IfNoneMatch: ifNoneMatch}, 0, nil
}

func validStrongIfMatch(value string) bool {
	value = strings.TrimSpace(value)
	if value == "*" {
		return true
	}
	if value == "" {
		return false
	}
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if len(candidate) < 2 || candidate[0] != '"' || candidate[len(candidate)-1] != '"' || strings.HasPrefix(candidate, "W/") || strings.ContainsAny(candidate[1:len(candidate)-1], "\"\r\n") {
			return false
		}
	}
	return true
}

func supportedContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || mediaType == "application/ld+json"
}

func requestNotModified(r *http.Request, etag string, modifiedAt time.Time) bool {
	ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match"))
	if ifNoneMatch != "" {
		for _, candidate := range strings.Split(ifNoneMatch, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
				return true
			}
		}
		return false
	}
	if modifiedAt.IsZero() {
		return false
	}
	ifModifiedSince, err := http.ParseTime(r.Header.Get("If-Modified-Since"))
	if err != nil {
		return false
	}
	return !modifiedAt.UTC().Truncate(time.Second).After(ifModifiedSince.UTC())
}

func validationError(err error, actualID, expectedID string) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("resource id hash %s does not match request id hash %s", redact.Hash(actualID), redact.Hash(expectedID))
}

func (h *Handler) writeOptions(w http.ResponseWriter, r *http.Request) {
	h.writeCORS(w, r)
	methods := "GET, HEAD, OPTIONS"
	headers := "Content-Type, If-None-Match"
	if h.writeEnabled {
		methods = "GET, HEAD, PUT, DELETE, OPTIONS"
		headers = "Authorization, Content-Type, If-Match, If-None-Match"
	}
	w.Header().Set("Access-Control-Allow-Headers", headers)
	w.Header().Set("Access-Control-Allow-Methods", methods)
	w.Header().Set("Allow", methods)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	methods := "GET, HEAD, OPTIONS"
	if h.writeEnabled {
		methods = "GET, HEAD, PUT, DELETE, OPTIONS"
	}
	w.Header().Set("Allow", methods)
	h.writeError(w, r, http.StatusMethodNotAllowed, "method not allowed")
}

func (h *Handler) writeDocumentHeaders(w http.ResponseWriter, r *http.Request) {
	h.writeCORS(w, r)
	w.Header().Set("Content-Type", documentMediaType)
}

func (h *Handler) writeCORS(w http.ResponseWriter, r *http.Request) {
	h.cors.SetHeaders(w, r)
}

func (h *Handler) authorizedWrite(r *http.Request) bool {
	token := bearerToken(r)
	if token == "" || h.writeToken == "" {
		return false
	}
	candidateDigest := sha256.Sum256([]byte(token))
	expectedDigest := sha256.Sum256([]byte(h.writeToken))
	return subtle.ConstantTimeCompare(candidateDigest[:], expectedDigest[:]) == 1
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

func (h *Handler) logStoreError(message, resourceKey string, err error) {
	h.logger.Error(message, "resource_key_hash", redact.Hash(resourceKey), "err", err)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.writeCORS(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

type errorResponse struct {
	Error string `json:"error"`
}
