// Command triplet runs the triplet IIIF server.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/libops/triplet/internal/config"
	"github.com/libops/triplet/internal/observability"
	"github.com/libops/triplet/internal/server"
	tvips "github.com/libops/triplet/internal/vips"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		// No logger yet, so a single stderr write is the right move.
		_, _ = os.Stderr.WriteString("config: " + err.Error() + "\n")
		os.Exit(2)
	}

	logger := observability.NewLogger(cfg.Logging.Level, cfg.Logging.Format)

	tvips.Startup(tvips.Config{
		Concurrency:       cfg.Vips.Concurrency,
		CacheMaxMem:       cfg.Vips.CacheMaxMem,
		CacheMaxFiles:     cfg.Vips.CacheMaxFiles,
		ReportLeaks:       cfg.Vips.ReportLeaks,
		BlockUntrusted:    cfg.Vips.BlockUntrusted != nil && *cfg.Vips.BlockUntrusted,
		BlockedOperations: cfg.Vips.BlockedOperations,
	}, logger)
	defer tvips.Shutdown()

	srv, err := server.Build(cfg, logger)
	if err != nil {
		logger.Error("build server", slog.Any("err", err))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, srv, logger); err != nil {
		logger.Error("serve", slog.Any("err", err))
		os.Exit(1)
	}
}
