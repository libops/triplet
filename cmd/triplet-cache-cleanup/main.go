// Command triplet-cache-cleanup performs explicit filesystem cache cleanup.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/config"
)

type namedReport struct {
	name      string
	maxConfig string
	report    cache.CleanupReport
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	timeout := flag.Duration("timeout", 0, "optional cleanup timeout")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	reports, err := cleanupCaches(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "cache cleanup: %v\n", err)
		os.Exit(1)
	}
	if len(reports) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no filesystem cache roots configured")
		return
	}

	overMax := false
	for _, r := range reports {
		printReport(os.Stdout, r)
		if r.report.OverMaxBytes {
			overMax = true
			_, _ = fmt.Fprintf(os.Stderr, "%s cache remains over %s: bytes=%d max_bytes=%d\n", r.name, r.maxConfig, r.report.Bytes, r.report.MaxBytes)
		}
	}
	if overMax {
		os.Exit(1)
	}
}

func cleanupCaches(ctx context.Context, cfg *config.Config) ([]namedReport, error) {
	var reports []namedReport
	if cfg.Cache.Root != "" {
		store, err := cache.NewPayloadFileStoreWithMaxAge(cfg.Cache.Root, int64(cfg.Cache.MaxBytes), cfg.Cache.MaxAge)
		if err != nil {
			return nil, fmt.Errorf("derivative cache: %w", err)
		}
		report, err := store.Cleanup(ctx)
		if err != nil {
			return nil, fmt.Errorf("derivative cache: %w", err)
		}
		reports = append(reports, namedReport{
			name:      "derivative",
			maxConfig: "cache.max_bytes",
			report:    report,
		})
	}
	if cfg.Cache.SourceRoot != "" {
		store, err := cache.NewFileStore(cfg.Cache.SourceRoot, int64(cfg.Cache.SourceMaxBytes))
		if err != nil {
			return nil, fmt.Errorf("source cache: %w", err)
		}
		report, err := store.Cleanup(ctx)
		if err != nil {
			return nil, fmt.Errorf("source cache: %w", err)
		}
		reports = append(reports, namedReport{
			name:      "source",
			maxConfig: "cache.source_max_bytes",
			report:    report,
		})
	}
	return reports, nil
}

func printReport(out io.Writer, r namedReport) {
	_, _ = fmt.Fprintf(out,
		"%s cache root=%s scanned=%d removed=%d expired_removed=%d removed_bytes=%d bytes=%d max_bytes=%d over_max=%t\n",
		r.name,
		r.report.Root,
		r.report.Scanned,
		r.report.Removed,
		r.report.ExpiredRemoved,
		r.report.RemovedBytes,
		r.report.Bytes,
		r.report.MaxBytes,
		r.report.OverMaxBytes,
	)
}
