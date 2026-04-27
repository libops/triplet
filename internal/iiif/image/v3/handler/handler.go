// Package handler implements the IIIF Image API 3.0 HTTP surface.
//
// Routes:
//
//   - GET {prefix}/{id}            → 303 redirect to {prefix}/{id}/info.json
//   - GET {prefix}/{id}/info.json  → image information document
//   - GET {prefix}/{id}/{region}/{size}/{rotation}/{quality}.{format}
//     → encoded derivative
package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	gv "github.com/davidbyttow/govips/v2/vips"
	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/cors"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
	"github.com/libops/triplet/internal/iiif/image/v3/pipeline"
	"github.com/libops/triplet/internal/iiif/image/v3/types"
	"github.com/libops/triplet/internal/redact"
	"github.com/libops/triplet/internal/storage"
)

// Handler serves the Image API 3.0 surface mounted at Prefix.
type Handler struct {
	prefix           string
	publicBaseURL    string
	src              storage.Opener
	pipeline         *pipeline.Pipeline
	derivativeCache  cache.Store
	cors             cors.Policy
	infoCacheEnabled bool
	infoCacheMu      sync.RWMutex
	infoCache        map[string]cachedDimensions
	infoLimits       types.Limits
	maxSourcePixels  int64
	maxSourceBytes   int64
	vipsLimiter      chan struct{}
	logger           *slog.Logger
}

type cachedDimensions struct {
	width, height int
	size          int64
	modTime       time.Time
}

const (
	profileLinkHeader   = `<http://iiif.io/api/image/3/level2.json>;rel="profile"`
	exposeHeaders       = "Content-Length, Content-Type, ETag, Link, X-Cache"
	maxInfoCacheEntries = 4096
)

// New constructs an Image API handler.
//
// derivCache may be nil to disable derivative caching.
func New(prefix, publicBaseURL string, src storage.Opener, pipe *pipeline.Pipeline, derivCache cache.Store, allowedOrigins []string, infoLimits types.Limits, infoCacheEnabled bool, maxSourcePixels, maxSourceBytes int64, maxConcurrentTransforms int, logger *slog.Logger) *Handler {
	if derivCache == nil {
		derivCache = cache.Noop{}
	}
	var vipsLimiter chan struct{}
	if maxConcurrentTransforms > 0 {
		vipsLimiter = make(chan struct{}, maxConcurrentTransforms)
	}
	return &Handler{
		prefix:           strings.TrimRight(prefix, "/"),
		publicBaseURL:    strings.TrimRight(publicBaseURL, "/"),
		src:              src,
		pipeline:         pipe,
		derivativeCache:  derivCache,
		cors:             cors.New(allowedOrigins, exposeHeaders),
		infoCacheEnabled: infoCacheEnabled,
		infoCache:        map[string]cachedDimensions{},
		infoLimits:       infoLimits,
		maxSourcePixels:  maxSourcePixels,
		maxSourceBytes:   maxSourceBytes,
		vipsLimiter:      vipsLimiter,
		logger:           logger,
	}
}

// Register attaches the handler to mux at the configured prefix.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle(h.prefix+"/", h)
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r = r.WithContext(storage.ContextWithAuthHeaders(r.Context(), r.Header))
	rest := strings.TrimPrefix(r.URL.Path, h.prefix)
	req, err := parse.Parse(rest)
	if err != nil {
		h.logger.Debug("parse request", "path", redact.Path(r.URL.Path), "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.cors.SetHeaders(w, r)

	switch req.Kind {
	case parse.KindBase:
		target := h.prefix + "/" + url.PathEscape(req.Identifier) + "/info.json"
		http.Redirect(w, r, target, http.StatusSeeOther)
	case parse.KindInfo:
		h.serveInfo(w, r, req.Identifier)
	case parse.KindImage:
		h.serveImage(w, r, req)
	}
}

func (h *Handler) serveInfo(w http.ResponseWriter, r *http.Request, identifier string) {
	width, height, err := h.imageDimensions(r.Context(), identifier)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "identifier not found")
			return
		}
		if errors.Is(err, storage.ErrForbidden) {
			writeError(w, http.StatusForbidden, "identifier forbidden")
			return
		}
		if errors.Is(err, pipeline.ErrBadRequest) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.Error("read image dimensions", "identifier", redact.Identifier(identifier), "identifier_hash", redact.Hash(identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read image")
		return
	}

	info := types.BuildLevel2Info(
		h.publicBaseURL+h.prefix+"/"+url.PathEscape(identifier),
		width, height,
		h.infoLimits,
	)

	w.Header().Set("Content-Type", `application/ld+json;profile="`+types.Context+`"`)
	w.Header().Add("Link", profileLinkHeader)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		h.logger.Warn("write info.json", "identifier", redact.Identifier(identifier), "identifier_hash", redact.Hash(identifier), "err", err)
	}
}

func (h *Handler) serveImage(w http.ResponseWriter, r *http.Request, req parse.Request) {
	w.Header().Add("Link", "<"+h.canonicalImageURL(req)+`>;rel="canonical"`)
	w.Header().Add("Link", profileLinkHeader)

	meta, err := h.sourceMeta(r.Context(), req.Identifier)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "identifier not found")
			return
		}
		if errors.Is(err, storage.ErrForbidden) {
			writeError(w, http.StatusForbidden, "identifier forbidden")
			return
		}
		h.logger.Error("read image metadata", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read image")
		return
	}
	key, cacheable := derivativeKey(req, meta)
	etag := ""
	if cacheable {
		etag = derivativeETag(key)
		w.Header().Set("ETag", etag)
		if ifNoneMatchMatches(r.Header.Values("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if rc, entry, err := h.derivativeCache.Get(r.Context(), key); err == nil {
			defer rc.Close()
			if entry.ContentType != "" {
				w.Header().Set("Content-Type", entry.ContentType)
			}
			if entry.Size > 0 {
				w.Header().Set("Content-Length", fmt.Sprintf("%d", entry.Size))
			}
			w.Header().Set("X-Cache", "hit")
			if r.Method == http.MethodHead {
				return
			}
			if _, err := io.Copy(w, rc); err != nil {
				h.logger.Warn("write cached derivative", "err", err)
			}
			return
		} else if !errors.Is(err, cache.ErrMiss) {
			h.logger.Warn("derivative cache get", "cache_key_hash", redact.Hash(key), slog.Any("err", err))
		}
	}

	contentType := contentTypeForFormat(req.Format)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Cache", "miss")
	if r.Method == http.MethodHead {
		return
	}

	release, err := h.acquireVips(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server busy")
		return
	}
	tmp, err := os.CreateTemp("", "triplet-derivative-*")
	if err != nil {
		release()
		h.logger.Error("create derivative temp file", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare response")
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	result, err := func() (pipeline.Result, error) {
		defer release()
		return h.pipeline.Transform(r.Context(), req, tmp)
	}()
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "identifier not found")
			return
		}
		if errors.Is(err, storage.ErrForbidden) {
			writeError(w, http.StatusForbidden, "identifier forbidden")
			return
		}
		if errors.Is(err, pipeline.ErrBadRequest) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.logger.Error("pipeline transform", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to transform image")
		return
	}
	if result.ContentType != "" {
		contentType = result.ContentType
		w.Header().Set("Content-Type", contentType)
	}
	size, err := tmp.Seek(0, io.SeekEnd)
	if err != nil {
		h.logger.Error("stat derivative temp file", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare response")
		return
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		h.logger.Error("rewind derivative temp file", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
		writeError(w, http.StatusInternalServerError, "failed to prepare response")
		return
	}
	if cacheable {
		if err := h.derivativeCache.Put(r.Context(), key, contentType, tmp); err != nil {
			h.logger.Warn("derivative cache put", "cache_key_hash", redact.Hash(key), slog.Any("err", err))
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			h.logger.Error("rewind cached derivative temp file", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
			writeError(w, http.StatusInternalServerError, "failed to prepare response")
			return
		}
	}
	if _, err := io.Copy(w, tmp); err != nil {
		h.logger.Warn("write derivative", "identifier", redact.Identifier(req.Identifier), "identifier_hash", redact.Hash(req.Identifier), "err", err)
	}
}

func (h *Handler) imageDimensions(ctx context.Context, identifier string) (int, int, error) {
	release, err := h.acquireVips(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("acquire vips worker: %w", err)
	}
	defer release()

	rc, meta, err := h.src.Open(ctx, identifier)
	if err != nil {
		return 0, 0, err
	}
	defer rc.Close()
	if h.maxSourceBytes > 0 && meta.Size > h.maxSourceBytes {
		return 0, 0, fmt.Errorf("source exceeds max_source_bytes %d", h.maxSourceBytes)
	}
	cacheableMeta := meta.Size > 0 || !meta.ModTime.IsZero()
	if h.infoCacheEnabled && cacheableMeta {
		h.infoCacheMu.RLock()
		entry, ok := h.infoCache[identifier]
		h.infoCacheMu.RUnlock()
		if ok && entry.size == meta.Size && entry.modTime.Equal(meta.ModTime) {
			return entry.width, entry.height, nil
		}
	}
	path := ""
	if f, ok := rc.(interface{ Name() string }); ok {
		path = f.Name()
	}
	if path == "" {
		tmp, err := os.CreateTemp("", "triplet-info-source-*")
		if err != nil {
			return 0, 0, fmt.Errorf("source temp file: %w", err)
		}
		path = tmp.Name()
		defer os.Remove(path)
		var reader io.Reader = rc
		if h.maxSourceBytes > 0 {
			reader = io.LimitReader(rc, h.maxSourceBytes+1)
		}
		n, err := io.Copy(tmp, reader)
		if err != nil {
			_ = tmp.Close()
			return 0, 0, fmt.Errorf("spool image: %w", err)
		}
		if h.maxSourceBytes > 0 && n > h.maxSourceBytes {
			_ = tmp.Close()
			return 0, 0, fmt.Errorf("source exceeds max_source_bytes %d", h.maxSourceBytes)
		}
		if err := tmp.Close(); err != nil {
			return 0, 0, fmt.Errorf("close source temp file: %w", err)
		}
	}
	params := gv.NewImportParams()
	params.Access.Set(gv.AccessSequential)
	img, err := gv.LoadImageFromFile(path, params)
	if err != nil {
		return 0, 0, fmt.Errorf("vips load: %w", err)
	}
	defer img.Close()
	width, height := img.Width(), img.Height()
	if err := pipeline.CheckSourcePixels(width, height, h.maxSourcePixels); err != nil {
		return 0, 0, err
	}
	if h.infoCacheEnabled && cacheableMeta {
		h.infoCacheMu.Lock()
		if len(h.infoCache) >= maxInfoCacheEntries {
			for key := range h.infoCache {
				delete(h.infoCache, key)
				break
			}
		}
		h.infoCache[identifier] = cachedDimensions{
			width:   width,
			height:  height,
			size:    meta.Size,
			modTime: meta.ModTime,
		}
		h.infoCacheMu.Unlock()
	}
	return width, height, nil
}

func (h *Handler) sourceMeta(ctx context.Context, identifier string) (storage.Meta, error) {
	if metaReader, ok := h.src.(storage.MetaReader); ok {
		meta, err := metaReader.Meta(ctx, identifier)
		if err != nil {
			return storage.Meta{}, err
		}
		if h.maxSourceBytes > 0 && meta.Size > h.maxSourceBytes {
			return storage.Meta{}, fmt.Errorf("source exceeds max_source_bytes %d", h.maxSourceBytes)
		}
		return meta, nil
	}
	rc, meta, err := h.src.Open(ctx, identifier)
	if err != nil {
		return storage.Meta{}, err
	}
	defer rc.Close()
	if h.maxSourceBytes > 0 && meta.Size > h.maxSourceBytes {
		return storage.Meta{}, fmt.Errorf("source exceeds max_source_bytes %d", h.maxSourceBytes)
	}
	return meta, nil
}

func (h *Handler) acquireVips(ctx context.Context) (func(), error) {
	if h.vipsLimiter == nil {
		return func() {}, nil
	}
	select {
	case h.vipsLimiter <- struct{}{}:
		return func() { <-h.vipsLimiter }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) canonicalImageURL(req parse.Request) string {
	return h.publicBaseURL + h.prefix + "/" + url.PathEscape(req.Identifier) + "/" +
		regionString(req.Region) + "/" +
		sizeString(req.Size) + "/" +
		rotationString(req.Rotation) + "/" +
		string(req.Quality) + "." + string(req.Format)
}

func regionString(r parse.Region) string {
	switch r.Kind {
	case parse.RegionFull:
		return "full"
	case parse.RegionSquare:
		return "square"
	case parse.RegionPercent:
		return "pct:" + formatFloat(r.X) + "," + formatFloat(r.Y) + "," + formatFloat(r.W) + "," + formatFloat(r.H)
	default:
		return formatFloat(r.X) + "," + formatFloat(r.Y) + "," + formatFloat(r.W) + "," + formatFloat(r.H)
	}
}

func sizeString(s parse.Size) string {
	prefix := ""
	if s.Upscale {
		prefix = "^"
	}
	switch s.Kind {
	case parse.SizeMax, parse.SizeMaxUp:
		return prefix + "max"
	case parse.SizeWidth:
		return prefix + strconv.Itoa(s.W) + ","
	case parse.SizeHeight:
		return prefix + "," + strconv.Itoa(s.H)
	case parse.SizeWH:
		return prefix + strconv.Itoa(s.W) + "," + strconv.Itoa(s.H)
	case parse.SizeBestFit:
		return prefix + "!" + strconv.Itoa(s.W) + "," + strconv.Itoa(s.H)
	case parse.SizePercent:
		return prefix + "pct:" + formatFloat(s.Percent)
	default:
		return ""
	}
}

func rotationString(r parse.Rotation) string {
	out := ""
	if r.Mirror {
		out = "!"
	}
	return out + formatFloat(r.Degrees)
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func derivativeETag(key string) string {
	sum := sha256.Sum256([]byte(key))
	return `"` + fmt.Sprintf("%x", sum[:]) + `"`
}

func derivativeKey(req parse.Request, meta storage.Meta) (string, bool) {
	key := cache.DerivativeKey(req)
	version := strings.TrimSpace(meta.Version)
	if version == "" && (meta.Size > 0 || !meta.ModTime.IsZero()) {
		version = fmt.Sprintf("meta:%d:%d", meta.Size, meta.ModTime.UnixNano())
	}
	if version == "" {
		return key, false
	}
	return key + "#source=" + version, true
}

func ifNoneMatchMatches(values []string, etag string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			candidate := strings.TrimSpace(part)
			if candidate == "*" || candidate == etag {
				return true
			}
		}
	}
	return false
}

func contentTypeForFormat(format parse.Format) string {
	switch format {
	case parse.FormatJPG:
		return "image/jpeg"
	case parse.FormatPNG:
		return "image/png"
	case parse.FormatGIF:
		return "image/gif"
	case parse.FormatWEBP:
		return "image/webp"
	case parse.FormatTIF:
		return "image/tiff"
	case parse.FormatJP2:
		return "image/jp2"
	case parse.FormatPDF:
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
