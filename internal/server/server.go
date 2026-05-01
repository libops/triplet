// Package server composes the triplet HTTP surface from configured
// subcomponents.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/config"
	"github.com/libops/triplet/internal/cors"
	imghandler "github.com/libops/triplet/internal/iiif/image/v3/handler"
	"github.com/libops/triplet/internal/iiif/image/v3/pipeline"
	imgtypes "github.com/libops/triplet/internal/iiif/image/v3/types"
	preshandler "github.com/libops/triplet/internal/iiif/presentation/v3/handler"
	presstore "github.com/libops/triplet/internal/iiif/presentation/v3/store"
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
	trustedProxies, err := trustedProxyCIDRs(cfg.Logging.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	imageInvalidationCIDRs, err := parseCIDRs("iiif.image.cache_invalidation_allowed_cidrs", cfg.IIIF.Image.CacheInvalidationAllowedCIDRs)
	if err != nil {
		return nil, err
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.Metrics.Enabled {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	if cfg.Debug.PprofEnabled {
		if cfg.Debug.PprofToken == "" {
			return nil, errors.New("debug.pprof_token is required when debug.pprof_enabled = true")
		}
		registerPprof(mux, cfg.Debug.PprofPrefix, cfg.Debug.PprofToken)
		logger.Info("pprof enabled", "prefix", cfg.Debug.PprofPrefix)
	}

	if cfg.IIIF.Image.Enabled {
		src, cleanupSource, err := buildSource(cfg, logger)
		if err != nil {
			return nil, err
		}
		if cleanupSource != nil {
			cleanups = append(cleanups, cleanupSource)
		}
		pipe := pipeline.New(src, pipeline.Limits{
			MaxOutputPixels:    cfg.IIIF.Image.MaxOutputPixels,
			MaxSourcePixels:    cfg.IIIF.Image.MaxSourcePixels,
			MaxSourceBytes:     int64(cfg.IIIF.Image.MaxSourceBytes),
			MaxDerivativeBytes: int64(cfg.IIIF.Image.MaxDerivativeBytes),
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
			cfg.IIIF.Image.CacheInvalidationToken,
			imageInvalidationCIDRs,
			trustedProxies,
			imgtypes.Limits{
				MaxArea:   cfg.IIIF.Image.MaxOutputPixels,
				MaxWidth:  cfg.IIIF.Image.MaxWidth,
				MaxHeight: cfg.IIIF.Image.MaxHeight,
			},
			cfg.IIIF.Image.InfoDimensionCache == nil || *cfg.IIIF.Image.InfoDimensionCache,
			cfg.IIIF.Image.MaxSourcePixels,
			int64(cfg.IIIF.Image.MaxSourceBytes),
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
			cors.New(cfg.IIIF.AllowedOrigins, "ETag"),
			cfg.IIIF.Presentation.WriteEnabled,
			cfg.IIIF.Presentation.WriteToken,
			logger,
		)
		h.Register(mux)
		logger.Info("presentation api enabled", "prefix", cfg.IIIF.Presentation.Prefix)
	}
	var handler http.Handler = mux
	handler = metrics.Middleware(handler)
	handler = observability.LoggingMiddleware(logger, observability.LoggingOptions{
		TrustedProxies: trustedProxies,
	})(handler)
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

func registerPprof(mux *http.ServeMux, prefix, token string) {
	prefix = strings.TrimRight(prefix, "/")
	mux.HandleFunc("GET "+prefix+"/", pprofHandler(token, pprof.Index))
	mux.HandleFunc("GET "+prefix+"/{name}", pprofHandler(token, pprof.Index))
	mux.HandleFunc("GET "+prefix+"/cmdline", pprofHandler(token, pprof.Cmdline))
	mux.HandleFunc("GET "+prefix+"/profile", pprofHandler(token, pprof.Profile))
	mux.HandleFunc("GET "+prefix+"/symbol", pprofHandler(token, pprof.Symbol))
	mux.HandleFunc("GET "+prefix+"/trace", pprofHandler(token, pprof.Trace))
}

func pprofHandler(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="triplet-pprof"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := strings.TrimSpace(auth[len("Bearer "):])
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="triplet-pprof"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func trustedProxyCIDRs(raw []string) ([]*net.IPNet, error) {
	return parseCIDRs("logging.trusted_proxy_cidrs", raw)
}

func parseCIDRs(name string, raw []string) ([]*net.IPNet, error) {
	cidrs := make([]*net.IPNet, 0, len(raw))
	for _, value := range raw {
		_, cidr, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid CIDR %q: %w", name, value, err)
		}
		cidrs = append(cidrs, cidr)
	}
	return cidrs, nil
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

func buildSource(cfg *config.Config, logger *slog.Logger) (storage.Opener, func(), error) {
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
			cfg.Sources.HTTP.AllowedOrigins,
			cfg.Sources.HTTP.RequestTimeout,
			int64(cfg.Sources.HTTP.MaxBytes),
		)
		op.AllowPrivateHosts = cfg.Sources.HTTP.AllowPrivateHosts
		authOp := storage.NewHTTPOpener(
			cfg.Sources.HTTP.AllowedOrigins,
			cfg.Sources.HTTP.RequestTimeout,
			int64(cfg.Sources.HTTP.MaxBytes),
		)
		authOp.AllowPrivateHosts = cfg.Sources.HTTP.AllowPrivateHosts
		authOp.ForwardAuthHeaders = true
		httpOp = op
		if cfg.Cache.SourceRoot != "" {
			sourceCache, err := buildSourceCache(cfg)
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
		if cfg.Sources.HTTP.MetadataCacheTTL > 0 {
			httpOp = &storage.MetaCaching{
				Inner: httpOp,
				TTL:   cfg.Sources.HTTP.MetadataCacheTTL,
			}
		}
		localURLMappings, err := buildLocalURLMappings(cfg, fileOp)
		if err != nil {
			return nil, nil, err
		}
		if len(localURLMappings) > 0 {
			httpOp = &storage.LocalURLFallback{
				Mappings:       localURLMappings,
				AllowedOrigins: cfg.Sources.HTTP.AllowedOrigins,
				Fallback:       httpOp,
				Logger:         logger,
				AuthFallback:   authOp,
			}
		}
	}

	var defaultOp storage.Opener
	switch cfg.Sources.Default {
	case "file":
		defaultOp = fileOp
	case "http":
		defaultOp = httpOp
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
	if len(routes) > 0 && (fileOp != nil || httpOp != nil) {
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
			Prefix:       mapping.Prefix,
			File:         op,
			OCFL:         mapping.OCFL,
			AuthProbe:    mapping.AuthProbe,
			AuthCacheTTL: cfg.Sources.HTTP.MetadataCacheTTL,
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
		return cache.Noop{}, nil
	}
	return cache.NewFileStoreWithMaxAge(cfg.Cache.Root, int64(cfg.Cache.MaxBytes), cfg.Cache.MaxAge)
}

func buildSourceCache(cfg *config.Config) (cache.Store, error) {
	if cfg.Cache.SourceRoot != "" {
		return cache.NewFileStore(cfg.Cache.SourceRoot, int64(cfg.Cache.SourceMaxBytes))
	}
	return cache.Noop{}, nil
}
