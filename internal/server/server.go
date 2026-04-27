// Package server composes the triplet HTTP surface from configured
// subcomponents.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/config"
	"github.com/libops/triplet/internal/cors"
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
	var cleanups []func()
	buildSucceeded := false
	defer func() {
		if buildSucceeded {
			return
		}
		for _, cleanup := range cleanups {
			cleanup()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", metrics.Handler())
	if cfg.Debug.PprofEnabled {
		registerPprof(mux, cfg.Debug.PprofPrefix)
		logger.Info("pprof enabled", "prefix", cfg.Debug.PprofPrefix)
	}

	if cfg.IIIF.Image.Enabled {
		src, cleanupSource, err := buildSource(cfg)
		if err != nil {
			return nil, err
		}
		if cleanupSource != nil {
			cleanups = append(cleanups, cleanupSource)
		}
		pipe := pipeline.New(src, pipeline.Limits{
			MaxOutputPixels:    cfg.IIIF.Image.MaxOutputPixels,
			MaxSourcePixels:    cfg.IIIF.Image.MaxSourcePixels,
			MaxSourceBytes:     cfg.IIIF.Image.MaxSourceBytes,
			MaxDerivativeBytes: cfg.IIIF.Image.MaxDerivativeBytes,
		}, pipeline.Options{
			ColorManagement: cfg.IIIF.Image.ColorManagement,
			LoadAccess:      cfg.IIIF.Image.LoadAccess,
		})
		derivCache, err := buildDerivativeCache(cfg)
		if err != nil {
			return nil, err
		}
		if closer, ok := derivCache.(interface{ Close() error }); ok {
			cleanups = append(cleanups, func() { _ = closer.Close() })
		}
		h := imghandler.New(
			cfg.IIIF.Image.Prefix,
			cfg.Server.PublicBaseURL,
			src,
			pipe,
			derivCache,
			imageAllowedOrigins(cfg),
			imgtypes.Limits{
				MaxArea:   cfg.IIIF.Image.MaxOutputPixels,
				MaxWidth:  cfg.IIIF.Image.MaxWidth,
				MaxHeight: cfg.IIIF.Image.MaxHeight,
			},
			cfg.IIIF.Image.InfoDimensionCache == nil || *cfg.IIIF.Image.InfoDimensionCache,
			cfg.IIIF.Image.MaxSourcePixels,
			cfg.IIIF.Image.MaxSourceBytes,
			cfg.IIIF.Image.MaxConcurrentTransforms,
			logger,
		)
		h.Register(mux)
		logger.Info("image api enabled", "prefix", cfg.IIIF.Image.Prefix)
	}
	if cfg.IIIF.Presentation.Enabled {
		st, cleanupStore, err := buildPresentationStore(cfg)
		if err != nil {
			return nil, err
		}
		if cleanupStore != nil {
			cleanups = append(cleanups, cleanupStore)
		}
		h := preshandler.New(
			cfg.IIIF.Presentation.Prefix,
			st,
			cors.New(cfg.IIIF.AllowedOrigins, ""),
			cfg.IIIF.Presentation.WriteEnabled,
			cfg.IIIF.Presentation.WriteToken,
			logger,
		)
		h.Register(mux)
		logger.Info("presentation api enabled", "prefix", cfg.IIIF.Presentation.Prefix)
	}
	if cfg.IIIF.Search.Enabled {
		h := searchhandler.New(cfg.IIIF.Search.Prefix, cfg.Server.PublicBaseURL, searcher.Noop{}, cors.New(cfg.IIIF.AllowedOrigins, ""), logger)
		h.Register(mux)
		logger.Info("search api enabled", "prefix", cfg.IIIF.Search.Prefix)
	}
	if cfg.IIIF.Auth.Enabled {
		if !cfg.IIIF.Auth.DevelopmentPermitAll {
			return nil, errors.New("iiif auth requires an explicit authorizer")
		}
		h := authhandler.New(cfg.IIIF.Auth.Prefix, cfg.Server.PublicBaseURL, authz.PermitAll{}, cors.New(cfg.IIIF.AllowedOrigins, ""), logger)
		h.Register(mux)
		logger.Warn("auth api enabled with development permit-all authorizer", "prefix", cfg.IIIF.Auth.Prefix)
	}

	var handler http.Handler = mux
	handler = metrics.Middleware(handler)
	handler = observability.LoggingMiddleware(logger)(handler)
	handler = observability.RecoverMiddleware(logger)(handler)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
	}
	for _, cleanup := range cleanups {
		cleanup := cleanup
		srv.RegisterOnShutdown(cleanup)
	}
	buildSucceeded = true
	return srv, nil
}

func imageAllowedOrigins(cfg *config.Config) []string {
	if len(cfg.IIIF.Image.AllowedOrigins) > 0 {
		return cfg.IIIF.Image.AllowedOrigins
	}
	return cfg.IIIF.AllowedOrigins
}

func registerPprof(mux *http.ServeMux, prefix string) {
	prefix = strings.TrimRight(prefix, "/")
	mux.HandleFunc("GET "+prefix+"/", pprof.Index)
	mux.HandleFunc("GET "+prefix+"/{name}", pprof.Index)
	mux.HandleFunc("GET "+prefix+"/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET "+prefix+"/profile", pprof.Profile)
	mux.HandleFunc("GET "+prefix+"/symbol", pprof.Symbol)
	mux.HandleFunc("GET "+prefix+"/trace", pprof.Trace)
}

func buildPresentationStore(cfg *config.Config) (presstore.Store, func(), error) {
	if cfg.IIIF.Presentation.DSN != "" {
		st, err := presstore.NewMariaDBStore(context.Background(), cfg.IIIF.Presentation.DSN)
		if err != nil {
			return nil, nil, err
		}
		return st, func() { _ = st.Close() }, nil
	}
	st, err := presstore.NewFileStore(cfg.IIIF.Presentation.Root)
	return st, nil, err
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

func buildSource(cfg *config.Config) (storage.Opener, func(), error) {
	ctx := context.Background()
	var cleanup func()

	var fileOp storage.Opener
	if cfg.Sources.File != nil && cfg.Sources.File.Root != "" {
		op, err := storage.NewFileOpener(cfg.Sources.File.Root)
		if err != nil {
			return nil, nil, err
		}
		fileOp = op
	}

	var httpOp storage.Opener
	if cfg.Sources.HTTP != nil {
		op := storage.NewHTTPOpener(
			cfg.Sources.HTTP.AllowedHosts,
			cfg.Sources.HTTP.RequestTimeout,
			cfg.Sources.HTTP.MaxBytes,
		)
		op.AllowPrivateHosts = cfg.Sources.HTTP.AllowPrivateHosts
		authOp := storage.NewHTTPOpener(
			cfg.Sources.HTTP.AllowedHosts,
			cfg.Sources.HTTP.RequestTimeout,
			cfg.Sources.HTTP.MaxBytes,
		)
		authOp.AllowPrivateHosts = cfg.Sources.HTTP.AllowPrivateHosts
		authOp.ForwardAuthHeaders = true
		httpOp = op
		if cfg.Cache.SourceRoot != "" || cfg.Cache.SourceBucketURL != "" {
			sourceCache, err := buildSourceCache(ctx, cfg)
			if err != nil {
				return nil, nil, err
			}
			refreshCtx, cancel := context.WithCancel(context.Background())
			cleanup = cancel
			if closer, ok := sourceCache.(interface{ Close() error }); ok {
				previousCleanup := cleanup
				cleanup = func() {
					previousCleanup()
					_ = closer.Close()
				}
			}
			httpOp = &storage.Caching{
				Inner:          httpOp,
				Store:          sourceCache,
				StaleAfter:     cfg.Cache.SourceStaleAfter,
				RefreshContext: refreshCtx,
			}
		}
		localURLMappings, err := buildLocalURLMappings(cfg, fileOp)
		if err != nil {
			return nil, nil, err
		}
		if len(localURLMappings) > 0 {
			httpOp = &storage.LocalURLFallback{
				Mappings:     localURLMappings,
				Fallback:     httpOp,
				AuthFallback: authOp,
			}
		}
	}

	var gcsOp storage.Opener
	if cfg.Sources.GCS != nil && cfg.Sources.GCS.BucketURL != "" {
		op, err := storage.NewGCSOpener(ctx, cfg.Sources.GCS.BucketURL, cfg.Sources.GCS.Prefix, cfg.IIIF.Image.MaxSourceBytes)
		if err != nil {
			return nil, nil, err
		}
		previousCleanup := cleanup
		cleanup = func() {
			if previousCleanup != nil {
				previousCleanup()
			}
			_ = op.Close()
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
		return nil, nil, fmt.Errorf("source %q not supported", cfg.Sources.Default)
	}
	if defaultOp == nil {
		return nil, nil, fmt.Errorf("source %q not configured", cfg.Sources.Default)
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
		return &storage.Multiplex{Routes: routes, Default: defaultOp}, cleanup, nil
	}
	return defaultOp, cleanup, nil
}

func buildLocalURLMappings(cfg *config.Config, fileOp storage.Opener) ([]storage.LocalURLMapping, error) {
	if cfg.Sources.File == nil {
		return nil, nil
	}
	var mappings []storage.LocalURLMapping
	for _, mapping := range cfg.Sources.File.URLMappings {
		op, err := storage.NewFileOpener(mapping.Root)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, storage.LocalURLMapping{
			Prefix:                    mapping.Prefix,
			File:                      op,
			OCFL:                      mapping.OCFL,
			AuthProbe:                 mapping.AuthProbe,
			AuthCacheTTL:              mapping.AuthCacheTTL,
			AuthAnonymousCacheTTL:     mapping.AuthAnonymousCacheTTL,
			AuthAuthenticatedCacheTTL: mapping.AuthAuthenticatedCacheTTL,
			AuthCacheMaxEntries:       mapping.AuthCacheMaxEntries,
		})
	}
	if len(cfg.Sources.File.URLPrefixes) > 0 {
		fileOpener, ok := fileOp.(*storage.FileOpener)
		if !ok {
			return nil, fmt.Errorf("sources.file.root is required when sources.file.url_prefixes is configured")
		}
		for _, prefix := range cfg.Sources.File.URLPrefixes {
			mappings = append(mappings, storage.LocalURLMapping{
				Prefix: prefix,
				File:   fileOpener,
				OCFL:   cfg.Sources.File.URLPrefixesAreOCFL,
			})
		}
	}
	return mappings, nil
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
