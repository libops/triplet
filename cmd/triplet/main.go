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
	presstore "github.com/libops/triplet/internal/iiif/presentation/v3/store"
	"github.com/libops/triplet/internal/observability"
	"github.com/libops/triplet/internal/server"
	tvips "github.com/libops/triplet/internal/vips"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	healthcheckURL := flag.String("healthcheck", "", "HTTP healthcheck URL to probe, then exit")
	migratePresentationMariaDB := flag.Bool("migrate-presentation-mariadb", false, "apply the Presentation MariaDB schema, then exit")
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

	if *migratePresentationMariaDB {
		if cfg.IIIF.Presentation.DSN == "" {
			_, _ = os.Stderr.WriteString("migrate presentation mariadb: iiif.presentation.dsn is required\n")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := presstore.ApplyMariaDBSchema(ctx, cfg.IIIF.Presentation.DSN); err != nil {
			_, _ = os.Stderr.WriteString("migrate presentation mariadb: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}

	logger := observability.NewLogger(cfg.Logging.Level, cfg.Logging.Format)

	if err := tvips.Startup(tvips.Config{
		Concurrency:       cfg.Vips.Concurrency,
		CacheMaxMem:       cfg.Vips.CacheMaxMem,
		CacheMaxFiles:     cfg.Vips.CacheMaxFiles,
		ReportLeaks:       cfg.Vips.ReportLeaks,
		BlockUntrusted:    cfg.Vips.BlockUntrusted != nil && *cfg.Vips.BlockUntrusted,
		BlockedOperations: cfg.Vips.BlockedOperations,
	}, logger); err != nil {
		logger.Error("start libvips", slog.Any("err", err))
		os.Exit(1)
	}
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
