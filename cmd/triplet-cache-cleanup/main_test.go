package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libops/triplet/internal/cache"
	"github.com/libops/triplet/internal/config"
)

func TestCleanupCachesRemovesExpiredDerivativeAndReportsOversize(t *testing.T) {
	derivRoot := t.TempDir()
	sourceRoot := t.TempDir()

	derivStore, err := cache.NewPayloadFileStoreWithMaxAge(derivRoot, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := derivStore.Put(context.Background(), "old", "image/jpeg", strings.NewReader("old")); err != nil {
		t.Fatal(err)
	}
	oldFiles := payloadFiles(t, derivRoot)
	if len(oldFiles) != 1 {
		t.Fatalf("payload files after old put = %d, want 1", len(oldFiles))
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFiles[0], oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := derivStore.Put(context.Background(), "new", "image/jpeg", strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}

	sourceStore, err := cache.NewFileStore(sourceRoot, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Put(context.Background(), "source", "image/tiff", strings.NewReader("source")); err != nil {
		t.Fatal(err)
	}

	reports, err := cleanupCaches(context.Background(), &config.Config{
		Cache: config.Cache{
			Root:           derivRoot,
			MaxAge:         time.Hour,
			SourceRoot:     sourceRoot,
			SourceMaxBytes: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	derivReport := reportByName(t, reports, "derivative")
	if derivReport.ExpiredRemoved != 1 {
		t.Fatalf("expired removed = %d, want 1", derivReport.ExpiredRemoved)
	}
	if derivReport.Removed != 1 {
		t.Fatalf("derivative removed = %d, want 1", derivReport.Removed)
	}
	if got := len(payloadFiles(t, derivRoot)); got != 1 {
		t.Fatalf("derivative payload files = %d, want 1", got)
	}

	sourceReport := reportByName(t, reports, "source")
	if !sourceReport.OverMaxBytes {
		t.Fatal("expected source cache to report over max bytes")
	}
	if sourceReport.Bytes != int64(len("source")) {
		t.Fatalf("source bytes = %d, want %d", sourceReport.Bytes, len("source"))
	}
}

func TestCleanupCachesSkipsUnconfiguredRoots(t *testing.T) {
	reports, err := cleanupCaches(context.Background(), &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports = %d, want 0", len(reports))
	}
}

func reportByName(t *testing.T, reports []namedReport, name string) cache.CleanupReport {
	t.Helper()
	for _, report := range reports {
		if report.name == name {
			return report.report
		}
	}
	t.Fatalf("missing %s report", name)
	return cache.CleanupReport{}
}

func payloadFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) == ".meta" || strings.HasPrefix(d.Name(), ".tmp-") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}
