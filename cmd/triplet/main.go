// Command triplet runs the triplet IIIF server.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/libops/triplet/internal/config"
	"github.com/libops/triplet/internal/observability"
	"github.com/libops/triplet/internal/server"
	tvips "github.com/libops/triplet/internal/vips"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	healthcheckURL := flag.String("healthcheck", "", "HTTP healthcheck URL to probe, then exit")
	flag.Parse()

	if *healthcheckURL != "" {
		if err := runHealthcheck(*healthcheckURL); err != nil {
			_, _ = os.Stderr.WriteString("healthcheck: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}

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

func runHealthcheck(url string) error {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &healthcheckError{status: resp.StatusCode}
	}
	return nil
}

type healthcheckError struct {
	status int
}

func (e *healthcheckError) Error() string {
	return http.StatusText(e.status)
}
