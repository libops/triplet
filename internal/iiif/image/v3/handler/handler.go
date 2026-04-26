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
	"strconv"
	"strings"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/iiif/image/v3/parse"
	"github.com/libops/triplet/internal/iiif/image/v3/pipeline"
	"github.com/libops/triplet/internal/iiif/image/v3/types"
	"github.com/libops/triplet/internal/storage"

	vg "github.com/cshum/vipsgen/vips"
)

// Handler serves the Image API 3.0 surface mounted at Prefix.
type Handler struct {
	prefix          string
	publicBaseURL   string
	src             storage.Opener
	pipeline        *pipeline.Pipeline
	derivativeCache cache.Store
	infoLimits      types.Limits
	logger          *slog.Logger
}

// New constructs an Image API handler.
//
// derivCache may be nil to disable derivative caching.
func New(prefix, publicBaseURL string, src storage.Opener, pipe *pipeline.Pipeline, derivCache cache.Store, infoLimits types.Limits, logger *slog.Logger) *Handler {
	if derivCache == nil {
		derivCache = cache.Noop{}
	}
	return &Handler{
		prefix:          strings.TrimRight(prefix, "/"),
		publicBaseURL:   strings.TrimRight(publicBaseURL, "/"),
		src:             src,
		pipeline:        pipe,
		derivativeCache: derivCache,
		infoLimits:      infoLimits,
		logger:          logger,
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
	rest := strings.TrimPrefix(r.URL.Path, h.prefix)
	req, err := parse.Parse(rest)
	if err != nil {
		h.logger.Debug("parse request", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")

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
		h.logger.Error("read image dimensions", "identifier", identifier, "err", err)
		writeError(w, http.StatusInternalServerError, "failed to read image")
		return
	}

	info := types.BuildLevel2Info(
		h.publicBaseURL+h.prefix+"/"+url.PathEscape(identifier),
		width, height,
		h.infoLimits,
	)

	w.Header().Set("Content-Type", `application/ld+json;profile="`+types.Context+`"`)
	w.Header().Add("Link", `<http://iiif.io/api/image/3/level2.json>;rel="profile"`)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		h.logger.Warn("write info.json", "identifier", identifier, "err", err)
	}
}

func (h *Handler) serveImage(w http.ResponseWriter, r *http.Request, req parse.Request) {
	key := cache.DerivativeKey(req)
	etag := derivativeETag(key)
	w.Header().Set("ETag", etag)
	w.Header().Add("Link", "<"+h.canonicalImageURL(req)+`>;rel="canonical"`)
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
		h.logger.Warn("derivative cache get", "key", key, "err", err)
	}

	contentType := contentTypeForFormat(req.Format)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Cache", "miss")
	if r.Method == http.MethodHead {
		return
	}

	pr, pw := io.Pipe()
	cacheDone := make(chan error, 1)
	go func() {
		cacheDone <- h.derivativeCache.Put(r.Context(), key, contentType, pr)
	}()

	_, err := h.pipeline.Transform(r.Context(), req, io.MultiWriter(w, pw))
	if err != nil {
		_ = pw.CloseWithError(err)
		<-cacheDone
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "identifier not found")
			return
		}
		h.logger.Error("pipeline transform", "identifier", req.Identifier, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := pw.Close(); err != nil {
		h.logger.Warn("close cache writer", "key", key, "err", err)
	}
	if err := <-cacheDone; err != nil {
		h.logger.Warn("derivative cache put", "key", key, "err", err)
	}
}

func (h *Handler) imageDimensions(ctx context.Context, identifier string) (int, int, error) {
	rc, _, err := h.src.Open(ctx, identifier)
	if err != nil {
		return 0, 0, err
	}
	defer rc.Close()
	source := vg.NewSource(rc)
	defer source.Close()
	img, err := vg.NewImageFromSource(source, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("vips load: %w", err)
	}
	defer img.Close()
	return img.Width(), img.Height(), nil
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
