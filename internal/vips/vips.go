// Package vips owns the libvips process lifecycle and exposes a small
// helper surface that the rest of triplet uses.
//
// libvips has process-global initialization (vips_init) and shutdown
// (vips_shutdown), plus thread-affinity requirements for those calls. This
// package isolates that fragility from handlers and the pipeline.
//
// Only Startup, Shutdown, and the panic-safe helpers in this package are
// allowed to interact with govips process-global state directly.
package vips

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	vg "github.com/davidbyttow/govips/v2/vips"
)

// Config controls libvips runtime tuning at startup.
//
// All fields have safe defaults. Concurrency=0 lets libvips pick (typically
// GOMAXPROCS); set explicitly when you want determinism. CacheMaxMem caps
// the in-process operation cache; 0 disables it (recommended for a server
// that has its own derivative cache and does not want libvips holding state
// across requests).
type Config struct {
	Concurrency       int
	CacheMaxMem       int
	CacheMaxFiles     int
	ReportLeaks       bool
	BlockUntrusted    bool
	BlockedOperations []string
}

var blockUntrustedAllowedOperations = []string{
	"VipsForeignLoadJpeg",
	"VipsForeignLoadJpegFile",
	"VipsForeignLoadJpegBuffer",
	"VipsForeignLoadJpegSource",
	"VipsForeignSaveJpeg",
	"VipsForeignSaveJpegFile",
	"VipsForeignSaveJpegBuffer",
	"VipsForeignSaveJpegTarget",
	"VipsForeignLoadPng",
	"VipsForeignLoadPngFile",
	"VipsForeignLoadPngBuffer",
	"VipsForeignLoadPngSource",
	"VipsForeignSavePng",
	"VipsForeignSavePngFile",
	"VipsForeignSavePngBuffer",
	"VipsForeignSavePngTarget",
	"VipsForeignLoadWebp",
	"VipsForeignLoadWebpFile",
	"VipsForeignLoadWebpBuffer",
	"VipsForeignLoadWebpSource",
	"VipsForeignSaveWebp",
	"VipsForeignSaveWebpFile",
	"VipsForeignSaveWebpBuffer",
	"VipsForeignSaveWebpTarget",
	"VipsForeignLoadGif",
	"VipsForeignLoadGifFile",
	"VipsForeignLoadGifBuffer",
	"VipsForeignLoadGifSource",
	"VipsForeignSaveGif",
	"VipsForeignSaveGifFile",
	"VipsForeignSaveGifBuffer",
	"VipsForeignSaveGifTarget",
	"VipsForeignLoadJp2k",
	"VipsForeignLoadJp2kFile",
	"VipsForeignLoadJp2kBuffer",
	"VipsForeignLoadJp2kSource",
	"VipsForeignSaveJp2k",
	"VipsForeignSaveJp2kFile",
	"VipsForeignSaveJp2kBuffer",
	"VipsForeignSaveJp2kTarget",
	"VipsForeignLoadTiff",
	"VipsForeignLoadTiffFile",
	"VipsForeignLoadTiffBuffer",
	"VipsForeignLoadTiffSource",
	"VipsForeignSaveTiff",
	"VipsForeignSaveTiffFile",
	"VipsForeignSaveTiffBuffer",
	"VipsForeignSaveTiffTarget",
}

var (
	startOnce sync.Once
	logger    *slog.Logger
	startErr  error
)

// Startup initializes libvips. Safe to call multiple times; only the first
// call has effect. Must be called before any image processing.
//
// LoggingSettings is a govips-global state change and is intentionally only set
// here. Per the libvips security review, logging configuration must not be
// changed concurrently with image processing.
func Startup(cfg Config, l *slog.Logger) error {
	logger = l
	startOnce.Do(func() {
		if cfg.BlockUntrusted {
			_ = os.Setenv("VIPS_BLOCK_UNTRUSTED", "1")
		}
		if err := vg.Startup(&vg.Config{
			ConcurrencyLevel: cfg.Concurrency,
			MaxCacheMem:      cfg.CacheMaxMem,
			MaxCacheFiles:    cfg.CacheMaxFiles,
			ReportLeaks:      cfg.ReportLeaks,
		}); err != nil {
			l.Error("libvips startup failed", slog.Any("error", err))
			startErr = err
			return
		}
		if cfg.BlockUntrusted {
			for _, op := range blockUntrustedAllowedOperations {
				unblockOperation(op)
				l.Info("libvips operation unblocked", slog.String("operation", op))
			}
		}
		for _, op := range cfg.BlockedOperations {
			blockOperation(op)
			l.Info("libvips operation blocked", slog.String("operation", op))
		}
		vg.LoggingSettings(forwardLog, vg.LogLevelWarning)
		l.Info("libvips started",
			slog.String("version", vg.Version),
			slog.Int("concurrency", cfg.Concurrency),
			slog.Int("cache_max_mem", cfg.CacheMaxMem),
			slog.Bool("block_untrusted", cfg.BlockUntrusted),
			slog.Int("blocked_operations", len(cfg.BlockedOperations)),
		)
	})
	return startErr
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

// Wrap formats a govips error with operation context.
func Wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("vips %s: %w", op, err)
}
