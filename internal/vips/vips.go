// Package vips owns the libvips process lifecycle and exposes a small
// helper surface that the rest of triplet uses.
//
// libvips has process-global initialization (vips_init) and shutdown
// (vips_shutdown), plus thread-affinity requirements for those calls. This
// package isolates that fragility from handlers and the pipeline.
//
// Only Startup, Shutdown, and the panic-safe helpers in this package are
// allowed to interact with vipsgen state directly. Other packages import
// vipsgen for image operations but never call Startup/Shutdown.
package vips

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	vg "github.com/cshum/vipsgen/vips"
)

// Config controls libvips runtime tuning at startup.
//
// All fields have safe defaults. Concurrency=0 lets libvips pick (typically
// GOMAXPROCS); set explicitly when you want determinism. CacheMaxMem caps
// the in-process operation cache; 0 disables it (recommended for a server
// that has its own derivative cache and does not want libvips holding state
// across requests).
type Config struct {
	Concurrency    int
	CacheMaxMem    int
	CacheMaxFiles  int
	ReportLeaks    bool
	BlockUntrusted bool
}

var (
	startOnce sync.Once
	logger    *slog.Logger
)

// Startup initializes libvips. Safe to call multiple times; only the first
// call has effect. Must be called before any image processing.
//
// SetLogging is a vipsgen-global state change and is intentionally only set
// here. Per the vipsgen security review, logging configuration must not be
// changed concurrently with image processing.
func Startup(cfg Config, l *slog.Logger) {
	logger = l
	startOnce.Do(func() {
		if cfg.BlockUntrusted {
			_ = os.Setenv("VIPS_BLOCK_UNTRUSTED", "1")
		}
		vg.Startup(&vg.Config{
			ConcurrencyLevel: cfg.Concurrency,
			MaxCacheMem:      cfg.CacheMaxMem,
			MaxCacheFiles:    cfg.CacheMaxFiles,
			ReportLeaks:      cfg.ReportLeaks,
		})
		vg.SetLogging(forwardLog, vg.LogLevelWarning)
		l.Info("libvips started",
			slog.String("version", vg.Version),
			slog.Int("concurrency", cfg.Concurrency),
			slog.Int("cache_max_mem", cfg.CacheMaxMem),
			slog.Bool("block_untrusted", cfg.BlockUntrusted),
		)
	})
}

// Shutdown releases libvips resources. Idempotent.
func Shutdown() {
	vg.Shutdown()
}

// MemStats reports libvips' tracked memory and open files.
type MemStats struct {
	Mem, MemHigh, Files, Allocs int64
}

// ReadMemStats returns a snapshot of libvips memory tracking.
func ReadMemStats() MemStats {
	var s vg.MemoryStats
	vg.ReadVipsMemStats(&s)
	return MemStats{Mem: s.Mem, MemHigh: s.MemHigh, Files: s.Files, Allocs: s.Allocs}
}

func forwardLog(domain string, level vg.LogLevel, msg string) {
	if logger == nil {
		return
	}
	switch {
	case level <= vg.LogLevelCritical:
		logger.Error("vips", slog.String("domain", domain), slog.String("msg", msg))
	case level <= vg.LogLevelWarning:
		logger.Warn("vips", slog.String("domain", domain), slog.String("msg", msg))
	default:
		logger.Debug("vips", slog.String("domain", domain), slog.String("msg", msg))
	}
}

// nopWriteCloser wraps an io.Writer that does not implement io.Closer.
//
// vipsgen targets require io.WriteCloser; HTTP response writers are not
// closers. This adapter is the only place we paper over that gap.
type nopWriteCloser struct {
	w writeOnly
}

type writeOnly interface {
	Write(p []byte) (int, error)
}

// NopWriteCloser returns an io.WriteCloser that ignores Close.
func NopWriteCloser(w writeOnly) interface {
	Write(p []byte) (int, error)
	Close() error
} {
	return &nopWriteCloser{w: w}
}

func (n *nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
func (n *nopWriteCloser) Close() error                { return nil }

// Wrap formats a vipsgen error with operation context.
func Wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("vips %s: %w", op, err)
}
