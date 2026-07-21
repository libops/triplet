package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPercentEncode(t *testing.T) {
	t.Parallel()
	if got, want := percentEncode("a b/c+☃"), "a%20b%2Fc%2B%E2%98%83"; got != want {
		t.Fatalf("percentEncode() = %q, want %q", got, want)
	}
}

func TestHashValuesUsesNULSeparators(t *testing.T) {
	t.Parallel()
	const want = "59b271ae1bbcb1d31d41929817f4b16fb439eb4f31520b5ad1d5ce98920a7138"
	if got := hashValues([]string{"a", "b"}); got != want {
		t.Fatalf("hashValues() = %q, want %q", got, want)
	}
}

func TestUpdateRun(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.json")
	writeTestFile(t, path, `{"run_id":"test","concurrency":2}`+"\n")

	if err := updateRun(path, "start", "finish", "10.100000", "12.3456789"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(content), "\n") {
		t.Fatal("updated run metadata must end with a newline")
	}
	var run map[string]any
	if err := json.Unmarshal(content, &run); err != nil {
		t.Fatal(err)
	}
	if got := run["measured_started_at"]; got != "start" {
		t.Fatalf("measured_started_at = %#v", got)
	}
	if got := run["measured_finished_at"]; got != "finish" {
		t.Fatalf("measured_finished_at = %#v", got)
	}
	if got := run["measured_duration_seconds"]; got != 2.245679 {
		t.Fatalf("measured_duration_seconds = %#v", got)
	}
}

func TestMatrixSummaryAndReportAppend(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	index := filepath.Join(root, "matrix", "report.md")
	if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, index, "# Benchmark Matrix: run\n\n## Matrix Runs\n\n| Mode |\n")

	runDir := filepath.Join(root, "run-uncached-c2")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runDir, "run.json"), `{"mode":"uncached","concurrency":2,"triplet_image":"triplet:test","measured_duration_seconds":2}`+"\n")
	writeTestFile(t, filepath.Join(runDir, "requests.csv"), strings.Join([]string{
		"server,curl_exit,http_code,time_total,size_download",
		"triplet,0,200,0.1,1024",
		"triplet,0,201,0.3,2048",
		"triplet,28,000,1.0,0",
	}, "\n")+"\n")
	writeTestFile(t, filepath.Join(runDir, "resource-summary.csv"), "server,mean_cpu_percent,max_mem_mib\ntriplet,50,12.5\n")
	writeTestFile(t, filepath.Join(runDir, "report.md"), "# Run\n\n## Detail\n")

	if err := writeMatrixSummary(root, "run", index); err != nil {
		t.Fatal(err)
	}
	content := readTestFile(t, index)
	for _, expected := range []string{
		"Triplet image: `triplet:test`",
		"| uncached | 2 | 2/3 (67%) | 2.00 | 1.0 | 290.0 | 298.0 | 500.00 | 12.5 |",
		"## Matrix Runs",
		"| uncached | 2 | triplet | 2/3 (67%) | 200.0 | 200.0 | 1.5 KiB | [report](../run-uncached-c2/report.md) |",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("matrix report does not contain %q:\n%s", expected, content)
		}
	}

	if err := appendMatrixReports(root, "run", index); err != nil {
		t.Fatal(err)
	}
	content = readTestFile(t, index)
	for _, expected := range []string{
		"## Run Reports",
		"### run-uncached-c2",
		"- Mode: `uncached`",
		"## Run\n\n### Detail",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("appended report does not contain %q:\n%s", expected, content)
		}
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
