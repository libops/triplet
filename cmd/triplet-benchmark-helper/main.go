// Command triplet-benchmark-helper provides small, deterministic helpers for
// the IIIF benchmark shell driver. Keeping these operations in a compiled Go
// command avoids embedding another programming language in shell automation.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const usage = `usage: triplet-benchmark-helper <command> [arguments]

commands:
  urlencode VALUE
  hash VALUE [VALUE...]
  epoch
  update-run RUN_JSON STARTED_AT FINISHED_AT START_EPOCH FINISH_EPOCH
  matrix-summary OUT_ROOT RUN_ID INDEX
  append-matrix-reports OUT_ROOT RUN_ID INDEX`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}

	switch args[0] {
	case "urlencode":
		if len(args) != 2 {
			return errors.New("usage: triplet-benchmark-helper urlencode VALUE")
		}
		_, err := fmt.Fprintln(stdout, percentEncode(args[1]))
		return err
	case "hash":
		if len(args) < 2 {
			return errors.New("usage: triplet-benchmark-helper hash VALUE [VALUE...]")
		}
		_, err := fmt.Fprintln(stdout, hashValues(args[1:]))
		return err
	case "epoch":
		if len(args) != 1 {
			return errors.New("usage: triplet-benchmark-helper epoch")
		}
		_, err := fmt.Fprintf(stdout, "%.6f\n", float64(time.Now().UnixNano())/1e9)
		return err
	case "update-run":
		if len(args) != 6 {
			return errors.New("usage: triplet-benchmark-helper update-run RUN_JSON STARTED_AT FINISHED_AT START_EPOCH FINISH_EPOCH")
		}
		return updateRun(args[1], args[2], args[3], args[4], args[5])
	case "matrix-summary":
		if len(args) != 4 {
			return errors.New("usage: triplet-benchmark-helper matrix-summary OUT_ROOT RUN_ID INDEX")
		}
		return writeMatrixSummary(args[1], args[2], args[3])
	case "append-matrix-reports":
		if len(args) != 4 {
			return errors.New("usage: triplet-benchmark-helper append-matrix-reports OUT_ROOT RUN_ID INDEX")
		}
		return appendMatrixReports(args[1], args[2], args[3])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func percentEncode(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, b := range []byte(value) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b)) {
			_ = encoded.WriteByte(b)
			continue
		}
		_ = encoded.WriteByte('%')
		_ = encoded.WriteByte(hexadecimal[b>>4])
		_ = encoded.WriteByte(hexadecimal[b&0x0f])
	}
	return encoded.String()
}

func hashValues(values []string) string {
	hash := sha256.New()
	for index, value := range values {
		if index > 0 {
			_, _ = hash.Write([]byte{0})
		}
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func updateRun(path, startedAt, finishedAt, startedEpoch, finishedEpoch string) error {
	started, err := strconv.ParseFloat(startedEpoch, 64)
	if err != nil {
		return fmt.Errorf("parse measured start epoch: %w", err)
	}
	finished, err := strconv.ParseFloat(finishedEpoch, 64)
	if err != nil {
		return fmt.Errorf("parse measured finish epoch: %w", err)
	}

	run, err := readRun(path)
	if err != nil {
		return err
	}
	run["measured_started_at"] = startedAt
	run["measured_finished_at"] = finishedAt
	run["measured_duration_seconds"] = math.Round((finished-started)*1e6) / 1e6

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(run); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return atomicWrite(path, output.Bytes())
}

type requestStats struct {
	total int
	ok    int
	times []float64
	sizes []float64
}

type matrixRow struct {
	mode        string
	concurrency string
	server      string
	cells       []string
}

func writeMatrixSummary(outRoot, runID, indexPath string) error {
	runFiles, err := matrixRunFiles(outRoot, runID)
	if err != nil {
		return err
	}

	var summaryRows []matrixRow
	var overallRows []matrixRow
	tripletImages := make(map[string]struct{})
	for _, runPath := range runFiles {
		runDir := filepath.Dir(runPath)
		requestsPath := filepath.Join(runDir, "requests.csv")
		if _, err := os.Stat(requestsPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", requestsPath, err)
		}

		run, err := readRun(runPath)
		if err != nil {
			return err
		}
		tripletImages[fieldString(run, "triplet_image", "-")] = struct{}{}

		byServer, err := readRequestStats(requestsPath)
		if err != nil {
			return err
		}
		resources, err := readResources(filepath.Join(runDir, "resource-summary.csv"))
		if err != nil {
			return err
		}

		mode := fieldString(run, "mode", "-")
		concurrency := fieldString(run, "concurrency", "-")
		duration := fieldFloat(run, "measured_duration_seconds")
		triplet := byServer["triplet"]

		var cpuPerRequest *float64
		if triplet != nil && triplet.ok > 0 {
			if meanCPU, err := strconv.ParseFloat(resources["triplet"]["mean_cpu_percent"], 64); err == nil {
				value := meanCPU / 100 * duration / float64(triplet.ok)
				cpuPerRequest = &value
			}
		}
		var maxMemory *float64
		if value, err := strconv.ParseFloat(resources["triplet"]["max_mem_mib"], 64); err == nil {
			maxMemory = &value
		}

		if triplet == nil {
			triplet = &requestStats{}
		}
		summaryRows = append(summaryRows, matrixRow{
			mode:        mode,
			concurrency: concurrency,
			cells: []string{
				mode,
				concurrency,
				formatRate(triplet.ok, triplet.total),
				formatSeconds(duration),
				formatRatePerSecond(triplet.ok, duration),
				formatMilliseconds(percentile(triplet.times, 0.95), 1),
				formatMilliseconds(percentile(triplet.times, 0.99), 1),
				formatOptionalMilliseconds(cpuPerRequest, 2),
				formatOptionalFloat(maxMemory, 1),
			},
		})

		servers := make([]string, 0, len(byServer))
		for server := range byServer {
			servers = append(servers, server)
		}
		sort.Strings(servers)
		for _, server := range servers {
			stats := byServer[server]
			overallRows = append(overallRows, matrixRow{
				mode:        mode,
				concurrency: concurrency,
				server:      server,
				cells: []string{
					mode,
					concurrency,
					server,
					formatRate(stats.ok, stats.total),
					formatMilliseconds(median(stats.times), 1),
					formatMilliseconds(mean(stats.times), 1),
					formatSize(mean(stats.sizes)),
					fmt.Sprintf("[report](../%s/report.md)", filepath.Base(runDir)),
				},
			})
		}
	}

	if len(summaryRows) == 0 {
		return nil
	}

	sort.Slice(summaryRows, func(i, j int) bool { return matrixRowLess(summaryRows[i], summaryRows[j]) })
	sort.Slice(overallRows, func(i, j int) bool {
		if matrixRowLess(overallRows[i], overallRows[j]) {
			return true
		}
		if matrixRowLess(overallRows[j], overallRows[i]) {
			return false
		}
		return overallRows[i].server < overallRows[j].server
	})

	original, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", indexPath, err)
	}
	title, remainder, found := strings.Cut(string(original), "\n\n")
	if !found {
		remainder = ""
	}

	images := make([]string, 0, len(tripletImages))
	for image := range tripletImages {
		images = append(images, image)
	}
	sort.Strings(images)

	lines := []string{
		title,
		"",
		"## Summary",
		"",
		fmt.Sprintf("Triplet image: `%s`", strings.Join(images, ", ")),
		"",
		"| Mode | Concurrency | Triplet OK | Duration s | Req/s | p95 ms | p99 ms | CPU ms/req | Max MiB |",
		"| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |",
	}
	for _, row := range summaryRows {
		lines = append(lines, "| "+strings.Join(row.cells, " | ")+" |")
	}
	lines = append(lines, "", "Status reflects Triplet request success. Performance metrics are informational.", "")
	if strings.TrimSpace(remainder) != "" {
		lines = append(lines, strings.TrimRightFunc(remainder, unicode.IsSpace), "")
	}
	lines = append(lines,
		"## Overall Summary",
		"",
		"| Mode | Concurrency | Server | Success | Median ms | Mean ms | Mean bytes | Report |",
		"| --- | ---: | --- | --- | ---: | ---: | ---: | --- |",
	)
	for _, row := range overallRows {
		lines = append(lines, "| "+strings.Join(row.cells, " | ")+" |")
	}

	output := strings.TrimRightFunc(strings.Join(lines, "\n"), unicode.IsSpace) + "\n"
	return atomicWrite(indexPath, []byte(output))
}

func appendMatrixReports(outRoot, runID, indexPath string) error {
	runFiles, err := matrixRunFiles(outRoot, runID)
	if err != nil {
		return err
	}

	var appended strings.Builder
	_, _ = appended.WriteString("\n## Run Reports\n\n")
	for _, runPath := range runFiles {
		runDir := filepath.Dir(runPath)
		reportPath := filepath.Join(runDir, "report.md")
		report, err := os.ReadFile(reportPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", reportPath, err)
		}
		run, err := readRun(runPath)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&appended, "### %s\n\n", filepath.Base(runDir))
		_, _ = fmt.Fprintf(&appended, "- Mode: `%s`\n", fieldString(run, "mode", "-"))
		_, _ = fmt.Fprintf(&appended, "- Concurrency: `%s`\n", fieldString(run, "concurrency", "-"))
		_, _ = fmt.Fprintf(&appended, "- Directory: `%s`\n\n", runDir)
		_, _ = appended.WriteString(demoteHeadings(string(report)))
		_ = appended.WriteByte('\n')
	}

	file, err := os.OpenFile(indexPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", indexPath, err)
	}
	if _, err := io.WriteString(file, appended.String()); err != nil {
		_ = file.Close()
		return fmt.Errorf("append %s: %w", indexPath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", indexPath, err)
	}
	return nil
}

func demoteHeadings(markdown string) string {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.TrimRightFunc(markdown, unicode.IsSpace)
	if markdown == "" {
		return "\n"
	}
	lines := strings.Split(markdown, "\n")
	for index, line := range lines {
		if strings.HasPrefix(line, "#") {
			lines[index] = "#" + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func matrixRunFiles(outRoot, runID string) ([]string, error) {
	entries, err := os.ReadDir(outRoot)
	if err != nil {
		return nil, fmt.Errorf("read benchmark output root %s: %w", outRoot, err)
	}
	paths := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), runID+"-") {
			continue
		}
		path := filepath.Join(outRoot, entry.Name(), "run.json")
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func readRun(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.UseNumber()
	var run map[string]any
	if err := decoder.Decode(&run); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return run, nil
}

func readRequestStats(path string) (map[string]*requestStats, error) {
	rows, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	byServer := make(map[string]*requestStats)
	for _, row := range rows {
		server := row["server"]
		stats := byServer[server]
		if stats == nil {
			stats = &requestStats{}
			byServer[server] = stats
		}
		stats.total++
		if row["curl_exit"] != "0" || !strings.HasPrefix(row["http_code"], "2") {
			continue
		}
		total, err := strconv.ParseFloat(row["time_total"], 64)
		if err != nil {
			return nil, fmt.Errorf("parse time_total in %s: %w", path, err)
		}
		size, err := strconv.ParseFloat(row["size_download"], 64)
		if err != nil {
			return nil, fmt.Errorf("parse size_download in %s: %w", path, err)
		}
		stats.ok++
		stats.times = append(stats.times, total)
		stats.sizes = append(stats.sizes, math.Trunc(size))
	}
	return byServer, nil
}

func readResources(path string) (map[string]map[string]string, error) {
	rows, err := readCSV(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]map[string]string), nil
	}
	if err != nil {
		return nil, err
	}
	resources := make(map[string]map[string]string, len(rows))
	for _, row := range rows {
		resources[row["server"]] = row
	}
	return resources, nil
}

func readCSV(path string) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	headers, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read CSV header %s: %w", path, err)
	}
	var rows []map[string]string
	for {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CSV row %s: %w", path, err)
		}
		row := make(map[string]string, len(headers))
		for index, header := range headers {
			row[header] = values[index]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func fieldString(run map[string]any, key, fallback string) string {
	value, ok := run[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func fieldFloat(run map[string]any, key string) float64 {
	value, ok := run[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case float64:
		return typed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func matrixRowLess(left, right matrixRow) bool {
	leftMode := modeOrder(left.mode)
	rightMode := modeOrder(right.mode)
	if leftMode != rightMode {
		return leftMode < rightMode
	}
	leftConcurrency, leftErr := strconv.Atoi(left.concurrency)
	rightConcurrency, rightErr := strconv.Atoi(right.concurrency)
	if leftErr == nil && rightErr == nil && leftConcurrency != rightConcurrency {
		return leftConcurrency < rightConcurrency
	}
	return left.concurrency < right.concurrency
}

func modeOrder(mode string) int {
	switch mode {
	case "uncached":
		return 0
	case "cached":
		return 1
	default:
		return 99
	}
}

func percentile(values []float64, fraction float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	position := float64(len(ordered)-1) * fraction
	lower := int(position)
	upper := min(lower+1, len(ordered)-1)
	value := ordered[lower]
	if lower != upper {
		weight := position - float64(lower)
		value = ordered[lower]*(1-weight) + ordered[upper]*weight
	}
	return &value
}

func median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	value := ordered[middle]
	if len(ordered)%2 == 0 {
		value = (ordered[middle-1] + ordered[middle]) / 2
	}
	return &value
}

func mean(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var total float64
	for _, value := range values {
		total += value
	}
	result := total / float64(len(values))
	return &result
}

func formatRate(ok, total int) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", ok, total, float64(ok)/float64(total)*100)
}

func formatSeconds(value float64) string {
	if value <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f", value)
}

func formatRatePerSecond(count int, duration float64) string {
	if count == 0 || duration == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", float64(count)/duration)
}

func formatMilliseconds(value *float64, precision int) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatFloat(*value*1000, 'f', precision, 64)
}

func formatOptionalMilliseconds(value *float64, precision int) string {
	return formatMilliseconds(value, precision)
}

func formatOptionalFloat(value *float64, precision int) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatFloat(*value, 'f', precision, 64)
}

func formatSize(value *float64) string {
	if value == nil {
		return "-"
	}
	units := []string{"B", "KiB", "MiB", "GiB"}
	size := *value
	unit := units[0]
	for _, candidate := range units {
		unit = candidate
		if math.Abs(size) < 1024 || candidate == units[len(units)-1] {
			break
		}
		size /= 1024
	}
	if unit == "B" {
		return fmt.Sprintf("%.0f %s", size, unit)
	}
	return fmt.Sprintf("%.1f %s", size, unit)
}

func atomicWrite(path string, content []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".triplet-benchmark-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set permissions on temporary file for %s: %w", path, err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
