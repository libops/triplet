#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_DIR="${BENCH_IMAGE_DIR:-$ROOT_DIR/fixtures/benchmark}"
OUT_ROOT="${BENCH_OUT_ROOT:-$ROOT_DIR/results/benchmarks}"
RUN_ID="${BENCH_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
OUT_DIR="$OUT_ROOT/$RUN_ID"

MATRIX="${BENCH_MATRIX:-1}"
MODE="${BENCH_MODE:-uncached}"
MODES="${BENCH_MODES:-uncached cached}"
CONCURRENCY_LIST="${BENCH_CONCURRENCY_LIST:-}"
UNCACHED_CONCURRENCY_LIST="${BENCH_UNCACHED_CONCURRENCY_LIST:-2 4 8}"
CACHED_CONCURRENCY_LIST="${BENCH_CACHED_CONCURRENCY_LIST:-8 32 128}"
TRIPLET_IMAGE="${BENCH_TRIPLET_IMAGE:-triplet-benchmark:dev}"
SKIP_BUILD="${BENCH_SKIP_BUILD:-0}"
TRIPLET_PORT="${BENCH_TRIPLET_PORT:-18080}"
PASSES="${BENCH_PASSES:-5}"
WARMUP_PASSES="${BENCH_WARMUP_PASSES:-1}"
CONCURRENCY="${BENCH_CONCURRENCY:-1}"
STATS_INTERVAL="${BENCH_STATS_INTERVAL:-0.25}"
CURL_TIMEOUT="${BENCH_CURL_TIMEOUT:-120}"
KEEP_CONTAINERS="${BENCH_KEEP_CONTAINERS:-0}"
REQUESTS_FILE="${BENCH_REQUESTS_FILE:-$ROOT_DIR/fixtures/benchmark/requests.tsv}"
PROFILE="${BENCH_PROFILE:-0}"
PROFILE_SECONDS="${BENCH_PROFILE_SECONDS:-30}"
TRIPLET_COLOR_MANAGEMENT="${BENCH_TRIPLET_COLOR_MANAGEMENT:-preserve}"
TRIPLET_LOAD_ACCESS="${BENCH_TRIPLET_LOAD_ACCESS:-auto}"
PRINT_REPORT="${BENCH_PRINT_REPORT:-1}"
APPEND_RUN_REPORTS="${BENCH_APPEND_RUN_REPORTS:-1}"

NETWORK="triplet-bench-$RUN_ID"
TRIPLET_CONTAINER="triplet-bench-triplet-$RUN_ID"

mkdir -p "$OUT_DIR/request-lines" "$OUT_DIR/logs"

cleanup() {
  if [ -n "${PROFILE_PID:-}" ]; then
    wait "$PROFILE_PID" >/dev/null 2>&1 || true
  fi
  if [ -n "${STATS_PID:-}" ]; then
    kill "$STATS_PID" >/dev/null 2>&1 || true
    wait "$STATS_PID" >/dev/null 2>&1 || true
  fi
  docker logs "$TRIPLET_CONTAINER" >"$OUT_DIR/logs/triplet.log" 2>&1 || true
  if [ "$KEEP_CONTAINERS" != "1" ]; then
    docker rm -f "$TRIPLET_CONTAINER" >/dev/null 2>&1 || true
    docker network rm "$NETWORK" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

urlencode() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import quote
print(quote(sys.argv[1], safe=""))
PY
}

wait_http() {
  local name="$1"
  local url="$2"
  local container="${3:-}"
  local deadline=$((SECONDS + 120))
  until curl -fsS -o /dev/null "$url" 2>/dev/null; do
    if [ -n "$container" ] && [ "$(docker inspect "$container" --format '{{.State.Running}}' 2>/dev/null || true)" = "false" ]; then
      echo "$name exited before it became ready: $url" >&2
      docker logs "$container" >&2 || true
      exit 1
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "$name did not become ready: $url" >&2
      if [ -n "$container" ]; then
        docker logs "$container" >&2 || true
      fi
      exit 1
    fi
    sleep 2
  done
}

write_triplet_config() {
  local cache_config
  local debug_config=""
  if [ "$MODE" = "cached" ]; then
    cache_config='cache:
  root: /cache/triplet
  max_bytes: 0'
  else
    cache_config='cache: {}'
  fi
  if [ "$PROFILE" = "1" ]; then
    debug_config='
debug:
  pprof_enabled: true'
  fi
  cat >"$OUT_DIR/triplet.yaml" <<EOF
server:
  listen: ":8080"
  public_base_url: "http://127.0.0.1:$TRIPLET_PORT"
$debug_config

logging:
  level: warn
  format: json

vips:
  block_untrusted: true
  blocked_operations:
    - VipsForeignLoadPdf

iiif:
  image:
    enabled: true
    prefix: /iiif/3
    max_output_pixels: 400000000
    max_source_bytes: 1GiB
    color_management: "$TRIPLET_COLOR_MANAGEMENT"
    load_access: "$TRIPLET_LOAD_ACCESS"
    info_dimension_cache: true
  presentation:
    enabled: false

sources:
  default: file
  file:
    root: /images

$cache_config

extensions:
  transform:
    enabled: false
  uploads:
    enabled: false
EOF
}

collect_stats() {
  while true; do
    stats="$(docker stats --no-stream --format '{{json .}}' "$TRIPLET_CONTAINER" 2>/dev/null || true)"
    if [ -n "$stats" ]; then
      printf '{"ts":"%s","server":"triplet","container":"%s","stats":%s}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$TRIPLET_CONTAINER" "$stats" \
        >>"$OUT_DIR/container-stats.jsonl"
    fi
    sleep "$STATS_INTERVAL"
  done
}

write_queue() {
  local queue="$1"
  local passes="$2"
  : >"$queue"
  for pass in $(seq 1 "$passes"); do
    for image in "${images[@]}"; do
      while IFS=$'\t' read -r request_name request_path; do
        if [ -z "${request_name:-}" ] || [[ "$request_name" == \#* ]]; then
          continue
        fi
        printf '%s\0%s\0%s\0%s\0%s\0%s\0' "triplet" "http://127.0.0.1:$TRIPLET_PORT" "$image" "$pass" "$request_name" "$request_path" >>"$queue"
      done <"$REQUESTS_FILE"
    done
  done
}

write_worker() {
  cat >"$OUT_DIR/run-one.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

server="$1"
base_url="$2"
image="$3"
pass="$4"
request_name="$5"
request_path="$6"

urlencode() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import quote
print(quote(sys.argv[1], safe=""))
PY
}

csv_escape() {
  local v="${1//\"/\"\"}"
  printf '"%s"' "$v"
}

encoded="$(urlencode "$image")"
if [ "$request_path" = "info.json" ]; then
  url="${base_url%/}/iiif/3/${encoded}/info.json"
else
  url="${base_url%/}/iiif/3/${encoded}/${request_path}"
fi

hash="$(python3 - "$image" "$request_name" "$pass" "$server" <<'PY'
import hashlib
import sys
print(hashlib.sha256("\0".join(sys.argv[1:]).encode("utf-8")).hexdigest())
PY
)"
line_file="$OUT_DIR/request-lines/${hash}.csv"

set +e
code="$(curl -sS -o /dev/null \
  --max-time "$CURL_TIMEOUT" \
  -w '%{http_code},%{time_namelookup},%{time_connect},%{time_starttransfer},%{time_total},%{size_download}' \
  "$url")"
exit_code=$?
set -e

printf '%s,%s,%s,%s,%s,%s,%s\n' \
  "$server" "$pass" "$(csv_escape "$image")" "$(csv_escape "$request_name")" "$(csv_escape "$request_path")" "$exit_code" "$code" \
  >"$line_file"
EOF
}

run_matrix() {
  mkdir -p "$OUT_DIR"
  local index="$OUT_DIR/report.md"
  {
    echo "# Benchmark Matrix: $RUN_ID"
    echo
    echo "## Matrix Runs"
    echo
    echo "| Mode | Concurrency | Report | Status |"
    echo "| --- | --- | --- | --- |"
  } >"$index"

  if [ "$SKIP_BUILD" != "1" ]; then
    echo "Building Triplet benchmark image once for matrix: $TRIPLET_IMAGE"
    docker build --target runtime -t "$TRIPLET_IMAGE" "$ROOT_DIR"
  fi

  local requested_mode mode concurrency child_id child_dir status mode_concurrency_list
  for requested_mode in $MODES; do
    mode="$(normalize_mode "$requested_mode")"
    mode_concurrency_list="$(concurrency_list_for_mode "$mode")"
    for concurrency in $mode_concurrency_list; do
      child_id="$RUN_ID-$mode-c$concurrency"
      child_dir="$OUT_ROOT/$child_id"
      echo "Running benchmark mode=$mode concurrency=$concurrency"
      status="ok"
      if ! BENCH_MATRIX=0 BENCH_SKIP_BUILD=1 BENCH_PRINT_REPORT=0 BENCH_MODE="$mode" BENCH_CONCURRENCY="$concurrency" BENCH_RUN_ID="$child_id" "$0"; then
        status="failed"
      fi
      if [ -f "$child_dir/report.md" ]; then
        printf '| %s | %s | [%s](../%s/report.md) | %s |\n' "$mode" "$concurrency" "$child_id" "$child_id" "$status" >>"$index"
      else
        printf '| %s | %s | - | %s |\n' "$mode" "$concurrency" "$status" >>"$index"
      fi
      if [ "$status" != "ok" ]; then
        return 1
      fi
    done
  done

  write_matrix_summary "$index"
  if [ "$APPEND_RUN_REPORTS" = "1" ]; then
    append_matrix_reports "$index"
  fi
  if [ "$PRINT_REPORT" = "1" ]; then
    python3 "$ROOT_DIR/scripts/extract-benchmark-tldr.py" "$index"
  fi
}

concurrency_list_for_mode() {
  if [ -n "$CONCURRENCY_LIST" ]; then
    echo "$CONCURRENCY_LIST"
    return
  fi
  case "$1" in
    uncached)
      echo "$UNCACHED_CONCURRENCY_LIST"
      ;;
    cached)
      echo "$CACHED_CONCURRENCY_LIST"
      ;;
    *)
      echo "unsupported benchmark mode for concurrency selection: $1" >&2
      return 1
      ;;
  esac
}

normalize_mode() {
  case "$1" in
    uncached|cold)
      echo "uncached"
      ;;
    cached|warm-cache)
      echo "cached"
      ;;
    *)
      echo "BENCH_MODE must be uncached/cached, got: $1" >&2
      return 1
      ;;
  esac
}

write_matrix_summary() {
  local index="$1"
  python3 - "$OUT_ROOT" "$RUN_ID" "$index" <<'PY'
import csv
import json
import statistics
import sys
from pathlib import Path

out_root = Path(sys.argv[1])
run_id = sys.argv[2]
index = Path(sys.argv[3])

MODE_ORDER = {"uncached": 0, "cached": 1}

def fmt_ms(value):
    return "-" if value is None else f"{value * 1000:.1f}"

def fmt_cpu_ms(value):
    return "-" if value is None else f"{value * 1000:.2f}"

def fmt_s(value):
    return "-" if value is None else f"{value:.2f}"

def fmt_rate_per_s(count, duration):
    if not count or not duration:
        return "-"
    return f"{count / duration:.1f}"

def fmt_size(value):
    if value is None:
        return "-"
    units = ["B", "KiB", "MiB", "GiB"]
    size = float(value)
    unit = units[0]
    for unit in units:
        if abs(size) < 1024 or unit == units[-1]:
            break
        size /= 1024
    return f"{size:.0f} {unit}" if unit == "B" else f"{size:.1f} {unit}"

def fmt_rate(ok, total):
    if total == 0:
        return "-"
    return f"{ok}/{total} ({ok / total * 100:.0f}%)"

def percentile(values, pct):
    if not values:
        return None
    ordered = sorted(values)
    index = (len(ordered) - 1) * pct
    lower = int(index)
    upper = min(lower + 1, len(ordered) - 1)
    if lower == upper:
        return ordered[lower]
    weight = index - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight

def read_resources(path):
    if not path.exists():
        return {}
    with path.open(newline="", encoding="utf-8") as fh:
        return {row["server"]: row for row in csv.DictReader(fh)}

def sort_key(row):
    return (MODE_ORDER.get(row["mode"], 99), int(row["concurrency"]))

summary_rows = []
overall_rows = []
triplet_images = set()
for run_json in sorted(out_root.glob(f"{run_id}-*/run.json")):
    run_dir = run_json.parent
    requests_csv = run_dir / "requests.csv"
    if not requests_csv.exists():
        continue
    run = json.loads(run_json.read_text(encoding="utf-8"))
    triplet_images.add(run.get("triplet_image", "-"))
    by_server = {}
    with requests_csv.open(newline="", encoding="utf-8") as fh:
        for row in csv.DictReader(fh):
            stats = by_server.setdefault(row["server"], {"total": 0, "ok": 0, "times": [], "sizes": []})
            stats["total"] += 1
            if row["curl_exit"] == "0" and row["http_code"].startswith("2"):
                stats["ok"] += 1
                stats["times"].append(float(row["time_total"]))
                stats["sizes"].append(int(float(row["size_download"])))
    resources = read_resources(run_dir / "resource-summary.csv")
    triplet = by_server.get("triplet", {"total": 0, "ok": 0, "times": [], "sizes": []})
    triplet_times = triplet["times"]
    duration = float(run.get("measured_duration_seconds") or 0)
    cpu_per_request = None
    try:
        if triplet["ok"]:
            mean_cpu = float(resources.get("triplet", {}).get("mean_cpu_percent", ""))
            cpu_per_request = mean_cpu / 100 * duration / triplet["ok"]
    except ValueError:
        pass
    max_mem = None
    try:
        max_mem = float(resources.get("triplet", {}).get("max_mem_mib", ""))
    except ValueError:
        pass
    summary_rows.append({
        "mode": run.get("mode", "-"),
        "concurrency": str(run.get("concurrency", "-")),
        "row": [
            run.get("mode", "-"),
            str(run.get("concurrency", "-")),
            fmt_rate(triplet["ok"], triplet["total"]),
            fmt_s(duration if duration > 0 else None),
            fmt_rate_per_s(triplet["ok"], duration),
            fmt_ms(percentile(triplet_times, 0.95)),
            fmt_ms(percentile(triplet_times, 0.99)),
            fmt_cpu_ms(cpu_per_request),
            f"{max_mem:.1f}" if max_mem is not None else "-",
        ],
    })
    for server, stats in sorted(by_server.items()):
        times = stats["times"]
        sizes = stats["sizes"]
        overall_rows.append({
            "mode": run.get("mode", "-"),
            "concurrency": str(run.get("concurrency", "-")),
            "server": server,
            "row": [
                run.get("mode", "-"),
                str(run.get("concurrency", "-")),
                server,
                fmt_rate(stats["ok"], stats["total"]),
                fmt_ms(statistics.median(times) if times else None),
                fmt_ms(statistics.fmean(times) if times else None),
                fmt_size(statistics.fmean(sizes) if sizes else None),
                f"[report](../{run_dir.name}/report.md)",
            ],
        })

if summary_rows:
    original = index.read_text(encoding="utf-8")
    title, _, remainder = original.partition("\n\n")
    lines = [
        title,
        "",
        "## Summary",
        "",
        f"Triplet image: `{', '.join(sorted(triplet_images))}`",
        "",
        "| Mode | Concurrency | Triplet OK | Duration s | Req/s | p95 ms | p99 ms | CPU ms/req | Max MiB |",
        "| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for item in sorted(summary_rows, key=sort_key):
        lines.append("| " + " | ".join(item["row"]) + " |")
    lines.extend([
        "",
        "Status reflects Triplet request success. Performance metrics are informational.",
        "",
    ])
    if remainder:
        lines.append(remainder.rstrip())
        lines.append("")
    lines.extend([
        "## Overall Summary",
        "",
        "| Mode | Concurrency | Server | Success | Median ms | Mean ms | Mean bytes | Report |",
        "| --- | ---: | --- | --- | ---: | ---: | ---: | --- |",
    ])
    for item in sorted(overall_rows, key=lambda row: (sort_key(row), row["server"])):
        lines.append("| " + " | ".join(item["row"]) + " |")
    index.write_text("\n".join(lines).rstrip() + "\n", encoding="utf-8")
PY
}

append_matrix_reports() {
  local index="$1"
  python3 - "$OUT_ROOT" "$RUN_ID" "$index" <<'PY'
import json
import sys
from pathlib import Path

out_root = Path(sys.argv[1])
run_id = sys.argv[2]
index = Path(sys.argv[3])

def demote_headings(markdown):
    lines = []
    for line in markdown.splitlines():
        if line.startswith("#"):
            lines.append("#" + line)
        else:
            lines.append(line)
    return "\n".join(lines).rstrip() + "\n"

with index.open("a", encoding="utf-8") as out:
    out.write("\n## Run Reports\n\n")
    for run_json in sorted(out_root.glob(f"{run_id}-*/run.json")):
        run_dir = run_json.parent
        report = run_dir / "report.md"
        if not report.exists():
            continue
        run = json.loads(run_json.read_text(encoding="utf-8"))
        out.write(f"### {run_dir.name}\n\n")
        out.write(f"- Mode: `{run.get('mode', '-')}`\n")
        out.write(f"- Concurrency: `{run.get('concurrency', '-')}`\n")
        out.write(f"- Directory: `{run_dir}`\n\n")
        out.write(demote_headings(report.read_text(encoding="utf-8")))
        out.write("\n")
PY
}

main() {
  require docker
  require curl
  require python3

  if [ "$MATRIX" = "1" ]; then
    run_matrix
    return
  fi

  MODE="$(normalize_mode "$MODE")"

  if [ ! -d "$IMAGE_DIR" ]; then
    echo "benchmark image directory does not exist: $IMAGE_DIR" >&2
    exit 1
  fi
  if [ ! -f "$REQUESTS_FILE" ]; then
    echo "request matrix does not exist: $REQUESTS_FILE" >&2
    exit 1
  fi

  images=()
  while IFS= read -r image; do
    images+=("$image")
  done < <(cd "$IMAGE_DIR" && find . -type f \( \
    -iname '*.tif' -o -iname '*.tiff' -o -iname '*.jp2' -o -iname '*.j2k' -o \
    -iname '*.jpg' -o -iname '*.jpeg' -o -iname '*.png' -o -iname '*.webp' -o -iname '*.gif' \
  \) | sed 's#^\./##' | sort)

  if [ "${#images[@]}" -eq 0 ]; then
    echo "no benchmark images found in $IMAGE_DIR" >&2
    echo "add TIFF/JP2/JPEG/PNG fixtures under fixtures/benchmark or set BENCH_IMAGE_DIR" >&2
    exit 1
  fi

  write_triplet_config

  mkdir -p "$OUT_DIR/triplet-cache"
  triplet_cache_desc="derivative=false, source=false, vips_operation=false"
  if [ "$MODE" = "cached" ]; then
    mkdir -p "$OUT_DIR/triplet-cache"
    triplet_cache_desc="derivative=true, root=/cache/triplet, source=false, vips_operation=false"
  fi

  cat >"$OUT_DIR/run.json" <<EOF
{"run_id":"$RUN_ID","mode":"$MODE","image_dir":"$IMAGE_DIR","requests_file":"$REQUESTS_FILE","passes":$PASSES,"warmup_passes":$WARMUP_PASSES,"concurrency":$CONCURRENCY,"stats_interval_seconds":$STATS_INTERVAL,"profile_enabled":$PROFILE,"profile_seconds":$PROFILE_SECONDS,"triplet_image":"$TRIPLET_IMAGE","triplet_cache":"$triplet_cache_desc","triplet_color_management":"$TRIPLET_COLOR_MANAGEMENT","triplet_load_access":"$TRIPLET_LOAD_ACCESS","started_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF

  if [ "$SKIP_BUILD" = "1" ]; then
    echo "Using existing Triplet benchmark image: $TRIPLET_IMAGE"
  else
    echo "Building Triplet benchmark image: $TRIPLET_IMAGE"
    docker build --target runtime -t "$TRIPLET_IMAGE" "$ROOT_DIR"
  fi

  docker network create "$NETWORK" >/dev/null

  docker run -d --name "$TRIPLET_CONTAINER" --network "$NETWORK" \
    -p "127.0.0.1:$TRIPLET_PORT:8080" \
    -v "$IMAGE_DIR:/images:ro" \
    -v "$OUT_DIR/triplet-cache:/cache/triplet" \
    -v "$OUT_DIR/triplet.yaml:/etc/triplet/config.yaml:ro" \
    "$TRIPLET_IMAGE" -config /etc/triplet/config.yaml >/dev/null

  wait_http triplet "http://127.0.0.1:$TRIPLET_PORT/healthz" "$TRIPLET_CONTAINER"

  export OUT_DIR CURL_TIMEOUT
  write_worker

  if [ "$WARMUP_PASSES" -gt 0 ]; then
    echo "Running warmup passes: $WARMUP_PASSES"
    warmup_queue="$OUT_DIR/warmup-queue.args"
    write_queue "$warmup_queue" "$WARMUP_PASSES"
    mkdir -p "$OUT_DIR/warmup/request-lines"
    saved_out_dir="$OUT_DIR"
    OUT_DIR="$OUT_DIR/warmup"
    export OUT_DIR
    xargs -0 -P "$CONCURRENCY" -n 6 /bin/bash "$saved_out_dir/run-one.sh" <"$warmup_queue"
    OUT_DIR="$saved_out_dir"
    export OUT_DIR
  fi

  : >"$OUT_DIR/container-stats.jsonl"
  collect_stats &
  STATS_PID=$!
  if [ "$PROFILE" = "1" ]; then
    curl -fsS "http://127.0.0.1:$TRIPLET_PORT/debug/pprof/profile?seconds=$PROFILE_SECONDS" -o "$OUT_DIR/triplet.cpu.pprof" &
    PROFILE_PID=$!
  fi

  printf 'server,pass,image,request_name,request_path,curl_exit,http_code,time_namelookup,time_connect,time_starttransfer,time_total,size_download\n' >"$OUT_DIR/requests.csv"
  queue="$OUT_DIR/request-queue.args"
  write_queue "$queue" "$PASSES"
  measured_started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  measured_started_epoch="$(python3 - <<'PY'
import time
print(f"{time.time():.6f}")
PY
)"
  xargs -0 -P "$CONCURRENCY" -n 6 /bin/bash "$OUT_DIR/run-one.sh" <"$queue"
  measured_finished_epoch="$(python3 - <<'PY'
import time
print(f"{time.time():.6f}")
PY
)"
  measured_finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [ -n "${STATS_PID:-}" ]; then
    kill "$STATS_PID" >/dev/null 2>&1 || true
    wait "$STATS_PID" >/dev/null 2>&1 || true
    unset STATS_PID
  fi
  if [ -n "${PROFILE_PID:-}" ]; then
    wait "$PROFILE_PID" >/dev/null 2>&1 || true
    unset PROFILE_PID
  fi
  for line in "$OUT_DIR"/request-lines/*.csv; do
    [ -e "$line" ] || continue
    cat "$line" >>"$OUT_DIR/requests.csv"
  done

  python3 - "$OUT_DIR/run.json" "$measured_started_at" "$measured_finished_at" "$measured_started_epoch" "$measured_finished_epoch" <<'PY'
import json
import sys

path, started_at, finished_at, started, finished = sys.argv[1:]
with open(path, encoding="utf-8") as fh:
    run = json.load(fh)
run["measured_started_at"] = started_at
run["measured_finished_at"] = finished_at
run["measured_duration_seconds"] = round(float(finished) - float(started), 6)
with open(path, "w", encoding="utf-8") as fh:
    json.dump(run, fh, separators=(",", ":"))
    fh.write("\n")
PY

  python3 "$ROOT_DIR/scripts/benchmark-summary.py" "$OUT_DIR/requests.csv" "$OUT_DIR/summary.csv"
  python3 "$ROOT_DIR/scripts/benchmark-stats-summary.py" "$OUT_DIR/container-stats.jsonl" "$OUT_DIR/resource-summary.csv"
  python3 "$ROOT_DIR/scripts/benchmark-report.py" "$OUT_DIR/requests.csv" "$OUT_DIR/resource-summary.csv" "$OUT_DIR/run.json" "$OUT_DIR/report.md"
  if [ "$PRINT_REPORT" = "1" ]; then
    python3 "$ROOT_DIR/scripts/extract-benchmark-tldr.py" "$OUT_DIR/report.md"
  fi
}

main "$@"
