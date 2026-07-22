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
BENCHMARK_TMP_DIR=""
BENCHMARK_HELPER=""

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
  if [ -n "$BENCHMARK_TMP_DIR" ] && [ -d "$BENCHMARK_TMP_DIR" ]; then
    rm -f -- "$BENCHMARK_HELPER"
    rmdir -- "$BENCHMARK_TMP_DIR"
  fi
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

prepare_benchmark_helper() {
  BENCHMARK_TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/triplet-benchmark.XXXXXX")"
  BENCHMARK_HELPER="$BENCHMARK_TMP_DIR/triplet-benchmark-helper"
  (
    cd "$ROOT_DIR"
    go build -o "$BENCHMARK_HELPER" ./cmd/triplet-benchmark-helper
  )
  export BENCHMARK_HELPER
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

csv_escape() {
  local v="${1//\"/\"\"}"
  printf '"%s"' "$v"
}

encoded="$("$BENCHMARK_HELPER" urlencode "$image")"
if [ "$request_path" = "info.json" ]; then
  url="${base_url%/}/iiif/3/${encoded}/info.json"
else
  url="${base_url%/}/iiif/3/${encoded}/${request_path}"
fi

hash="$("$BENCHMARK_HELPER" hash "$image" "$request_name" "$pass" "$server")"
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
  "$BENCHMARK_HELPER" matrix-summary "$OUT_ROOT" "$RUN_ID" "$index"
}

append_matrix_reports() {
  local index="$1"
  "$BENCHMARK_HELPER" append-matrix-reports "$OUT_ROOT" "$RUN_ID" "$index"
}

main() {
  require docker
  require curl
  require go
  require mktemp
  require python3
  prepare_benchmark_helper

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

  export OUT_DIR CURL_TIMEOUT BENCHMARK_HELPER
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
  measured_started_epoch="$("$BENCHMARK_HELPER" epoch)"
  xargs -0 -P "$CONCURRENCY" -n 6 /bin/bash "$OUT_DIR/run-one.sh" <"$queue"
  measured_finished_epoch="$("$BENCHMARK_HELPER" epoch)"
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

  "$BENCHMARK_HELPER" update-run \
    "$OUT_DIR/run.json" \
    "$measured_started_at" \
    "$measured_finished_at" \
    "$measured_started_epoch" \
    "$measured_finished_epoch"

  python3 "$ROOT_DIR/scripts/benchmark-summary.py" "$OUT_DIR/requests.csv" "$OUT_DIR/summary.csv"
  python3 "$ROOT_DIR/scripts/benchmark-stats-summary.py" "$OUT_DIR/container-stats.jsonl" "$OUT_DIR/resource-summary.csv"
  python3 "$ROOT_DIR/scripts/benchmark-report.py" "$OUT_DIR/requests.csv" "$OUT_DIR/resource-summary.csv" "$OUT_DIR/run.json" "$OUT_DIR/report.md"
  if [ "$PRINT_REPORT" = "1" ]; then
    python3 "$ROOT_DIR/scripts/extract-benchmark-tldr.py" "$OUT_DIR/report.md"
  fi
}

main "$@"
