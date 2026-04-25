// Package server composes the triplet HTTP surface from configured
// subcomponents.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/config"
	authz "github.com/libops/triplet/internal/iiif/auth/v2/authorizer"
	authhandler "github.com/libops/triplet/internal/iiif/auth/v2/handler"
	imghandler "github.com/libops/triplet/internal/iiif/image/v3/handler"
	"github.com/libops/triplet/internal/iiif/image/v3/pipeline"
	imgtypes "github.com/libops/triplet/internal/iiif/image/v3/types"
	preshandler "github.com/libops/triplet/internal/iiif/presentation/v3/handler"
	presstore "github.com/libops/triplet/internal/iiif/presentation/v3/store"
	searchhandler "github.com/libops/triplet/internal/iiif/search/v2/handler"
	"github.com/libops/triplet/internal/iiif/search/v2/searcher"
	"github.com/libops/triplet/internal/metrics"
	"github.com/libops/triplet/internal/observability"
	"github.com/libops/triplet/internal/storage"
)

// Build constructs an *http.Server from a validated config.
//
// The returned server is not started. Callers own its lifecycle.
func Build(cfg *config.Config, logger *slog.Logger) (*http.Server, error) {
	src, err := buildSource(cfg)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", metrics.Handler())

	if cfg.IIIF.Image.Enabled {
		pipe := pipeline.New(src, pipeline.Limits{
			MaxOutputPixels: cfg.IIIF.Image.MaxOutputPixels,
		})
		derivCache, err := buildDerivativeCache(cfg)
		if err != nil {
			return nil, err
		}
		h := imghandler.New(
			cfg.IIIF.Image.Prefix,
			cfg.Server.PublicBaseURL,
			src,
			pipe,
			derivCache,
			imgtypes.Limits{
				MaxArea:   cfg.IIIF.Image.MaxOutputPixels,
				MaxWidth:  cfg.IIIF.Image.MaxWidth,
				MaxHeight: cfg.IIIF.Image.MaxHeight,
			},
			logger,
		)
		h.Register(mux)
		logger.Info("image api enabled", "prefix", cfg.IIIF.Image.Prefix)
	}
	if cfg.IIIF.Presentation.Enabled {
		st, err := buildPresentationStore(cfg)
		if err != nil {
			return nil, err
		}
		h := preshandler.New(cfg.IIIF.Presentation.Prefix, st, logger)
		h.Register(mux)
		logger.Info("presentation api enabled", "prefix", cfg.IIIF.Presentation.Prefix)
	}
	if cfg.IIIF.Search.Enabled {
		h := searchhandler.New(cfg.IIIF.Search.Prefix, cfg.Server.PublicBaseURL, searcher.Noop{}, logger)
		h.Register(mux)
		logger.Info("search api enabled", "prefix", cfg.IIIF.Search.Prefix)
	}
	if cfg.IIIF.Auth.Enabled {
		h := authhandler.New(cfg.IIIF.Auth.Prefix, cfg.Server.PublicBaseURL, authz.PermitAll{}, logger)
		h.Register(mux)
		logger.Info("auth api enabled", "prefix", cfg.IIIF.Auth.Prefix)
	}

	var handler http.Handler = mux
	handler = metrics.Middleware(handler)
	handler = observability.LoggingMiddleware(logger)(handler)
	handler = observability.RecoverMiddleware(logger)(handler)

	return &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
	}, nil
}

func buildPresentationStore(cfg *config.Config) (presstore.Store, error) {
	if cfg.IIIF.Presentation.DSN != "" {
		return presstore.NewMariaDBStore(context.Background(), cfg.IIIF.Presentation.DSN)
	}
	return presstore.NewFileStore(cfg.IIIF.Presentation.Root)
}

// Run starts s and blocks until ctx is cancelled, then performs a graceful
// shutdown bounded by 30 seconds.
func Run(ctx context.Context, s *http.Server, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", s.Addr)
		err := s.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func buildSource(cfg *config.Config) (storage.Opener, error) {
	ctx := context.Background()

	var fileOp storage.Opener
	if cfg.Sources.File != nil && cfg.Sources.File.Root != "" {
		op, err := storage.NewFileOpener(cfg.Sources.File.Root)
		if err != nil {
			return nil, err
		}
		fileOp = op
	}

	var httpOp storage.Opener
	if cfg.Sources.HTTP != nil {
		httpOp = storage.NewHTTPOpener(
			cfg.Sources.HTTP.AllowedHosts,
			cfg.Sources.HTTP.RequestTimeout,
			cfg.Sources.HTTP.MaxBytes,
		)
		if cfg.Cache.SourceRoot != "" || cfg.Cache.SourceBucketURL != "" {
			sourceCache, err := buildSourceCache(ctx, cfg)
			if err != nil {
				return nil, err
			}
			httpOp = &storage.Caching{Inner: httpOp, Store: sourceCache, StaleAfter: cfg.Cache.SourceStaleAfter}
		}
	}

	var gcsOp storage.Opener
	if cfg.Sources.GCS != nil && cfg.Sources.GCS.BucketURL != "" {
		op, err := storage.NewGCSOpener(ctx, cfg.Sources.GCS.BucketURL, cfg.Sources.GCS.Prefix)
		if err != nil {
			return nil, err
		}
		gcsOp = op
	}

	var defaultOp storage.Opener
	switch cfg.Sources.Default {
	case "file":
		defaultOp = fileOp
	case "http":
		defaultOp = httpOp
	case "gcs":
		defaultOp = gcsOp
	default:
		return nil, fmt.Errorf("source %q not supported", cfg.Sources.Default)
	}
	if defaultOp == nil {
		return nil, fmt.Errorf("source %q not configured", cfg.Sources.Default)
	}

	var routes []storage.Route
	if httpOp != nil {
		routes = append(routes,
			storage.Route{HasScheme: "http", Opener: httpOp},
			storage.Route{HasScheme: "https", Opener: httpOp},
		)
	}
	if gcsOp != nil {
		routes = append(routes, storage.Route{HasScheme: "gs", Opener: gcsOp})
	}
	if len(routes) > 0 && (fileOp != nil || httpOp != nil || gcsOp != nil) {
		return &storage.Multiplex{Routes: routes, Default: defaultOp}, nil
	}
	return defaultOp, nil
}

func buildDerivativeCache(cfg *config.Config) (cache.Store, error) {
	if cfg.Cache.Root == "" {
		if cfg.Cache.BucketURL == "" {
			return cache.Noop{}, nil
		}
		return cache.NewGCSStore(context.Background(), cfg.Cache.BucketURL, cfg.Cache.Prefix)
	}
	return cache.NewFileStore(cfg.Cache.Root, cfg.Cache.MaxBytes)
}

func buildSourceCache(ctx context.Context, cfg *config.Config) (cache.Store, error) {
	if cfg.Cache.SourceRoot != "" {
		return cache.NewFileStore(cfg.Cache.SourceRoot, cfg.Cache.SourceMaxBytes)
	}
	if cfg.Cache.SourceBucketURL != "" {
		return cache.NewGCSStore(ctx, cfg.Cache.SourceBucketURL, cfg.Cache.SourcePrefix)
	}
	return cache.Noop{}, nil
}
