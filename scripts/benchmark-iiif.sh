#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_DIR="${BENCH_IMAGE_DIR:-$ROOT_DIR/fixtures/benchmark}"
OUT_ROOT="${BENCH_OUT_ROOT:-$ROOT_DIR/results/benchmarks}"
RUN_ID="${BENCH_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
OUT_DIR="$OUT_ROOT/$RUN_ID"

TRIPLET_IMAGE="${BENCH_TRIPLET_IMAGE:-triplet-benchmark:dev}"
CANTALOUPE_IMAGE="${BENCH_CANTALOUPE_IMAGE:-islandora/cantaloupe:main}"
TRIPLET_PORT="${BENCH_TRIPLET_PORT:-18080}"
CANTALOUPE_PORT="${BENCH_CANTALOUPE_PORT:-18182}"
PASSES="${BENCH_PASSES:-3}"
CONCURRENCY="${BENCH_CONCURRENCY:-1}"
STATS_INTERVAL="${BENCH_STATS_INTERVAL:-1}"
CURL_TIMEOUT="${BENCH_CURL_TIMEOUT:-120}"
KEEP_CONTAINERS="${BENCH_KEEP_CONTAINERS:-0}"
REQUESTS_FILE="${BENCH_REQUESTS_FILE:-$ROOT_DIR/fixtures/benchmark/requests.tsv}"

NETWORK="triplet-bench-$RUN_ID"
TRIPLET_CONTAINER="triplet-bench-triplet-$RUN_ID"
CANTALOUPE_CONTAINER="triplet-bench-cantaloupe-$RUN_ID"

mkdir -p "$OUT_DIR/request-lines" "$OUT_DIR/logs"

cleanup() {
  if [ -n "${STATS_PID:-}" ]; then
    kill "$STATS_PID" >/dev/null 2>&1 || true
    wait "$STATS_PID" >/dev/null 2>&1 || true
  fi
  docker logs "$TRIPLET_CONTAINER" >"$OUT_DIR/logs/triplet.log" 2>&1 || true
  docker logs "$CANTALOUPE_CONTAINER" >"$OUT_DIR/logs/cantaloupe.log" 2>&1 || true
  if [ "$KEEP_CONTAINERS" != "1" ]; then
    docker rm -f "$TRIPLET_CONTAINER" "$CANTALOUPE_CONTAINER" >/dev/null 2>&1 || true
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
  local deadline=$((SECONDS + 120))
  until curl -fsS -o /dev/null "$url"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "$name did not become ready: $url" >&2
      exit 1
    fi
    sleep 2
  done
}

wait_any_http() {
  local name="$1"
  local url="$2"
  local deadline=$((SECONDS + 120))
  local code
  while true; do
    code="$(curl -sS -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || true)"
    if [ "$code" != "000" ] && [ -n "$code" ]; then
      return
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "$name did not become reachable: $url" >&2
      exit 1
    fi
    sleep 2
  done
}

write_triplet_config() {
  cat >"$OUT_DIR/triplet.yaml" <<EOF
server:
  listen: ":8080"
  public_base_url: "http://127.0.0.1:$TRIPLET_PORT"

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
  presentation:
    enabled: false
  search:
    enabled: false
  auth:
    enabled: false

sources:
  default: file
  file:
    root: /images

cache: {}

extensions:
  transform:
    enabled: false
  uploads:
    enabled: false
EOF
}

write_cantaloupe_config() {
  cat >"$OUT_DIR/cantaloupe.properties" <<'EOF'
endpoint.iiif.1.enabled = false
endpoint.iiif.2.enabled = true
endpoint.iiif.3.enabled = true
endpoint.admin.enabled = false

source.static = FilesystemSource
FilesystemSource.lookup_strategy = BasicLookupStrategy
FilesystemSource.BasicLookupStrategy.path_prefix = /images/
FilesystemSource.BasicLookupStrategy.path_suffix =

cache.server.derivative.enabled = false
cache.server.info.enabled = false

processor.selection_strategy = ManualSelectionStrategy
processor.ManualSelectionStrategy.jpg = Java2dProcessor
processor.ManualSelectionStrategy.jpeg = Java2dProcessor
processor.ManualSelectionStrategy.png = Java2dProcessor
processor.ManualSelectionStrategy.gif = Java2dProcessor
processor.ManualSelectionStrategy.tif = Java2dProcessor
processor.ManualSelectionStrategy.tiff = Java2dProcessor
processor.ManualSelectionStrategy.jp2 = OpenJpegProcessor
processor.ManualSelectionStrategy.j2k = OpenJpegProcessor

log.application.level = warn
EOF
}

collect_stats() {
  while true; do
    for server in triplet cantaloupe; do
      local container
      if [ "$server" = "triplet" ]; then
        container="$TRIPLET_CONTAINER"
      else
        container="$CANTALOUPE_CONTAINER"
      fi
      stats="$(docker stats --no-stream --format '{{json .}}' "$container" 2>/dev/null || true)"
      if [ -n "$stats" ]; then
        printf '{"ts":"%s","server":"%s","container":"%s","stats":%s}\n' \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$server" "$container" "$stats" \
          >>"$OUT_DIR/container-stats.jsonl"
      fi
    done
    sleep "$STATS_INTERVAL"
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

main() {
  require docker
  require curl
  require python3

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
  write_cantaloupe_config

  cat >"$OUT_DIR/run.json" <<EOF
{"run_id":"$RUN_ID","image_dir":"$IMAGE_DIR","requests_file":"$REQUESTS_FILE","passes":$PASSES,"concurrency":$CONCURRENCY,"triplet_image":"$TRIPLET_IMAGE","cantaloupe_image":"$CANTALOUPE_IMAGE","started_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF

  echo "Building Triplet benchmark image: $TRIPLET_IMAGE"
  docker build --target runtime -t "$TRIPLET_IMAGE" "$ROOT_DIR"

  docker network create "$NETWORK" >/dev/null

  docker run -d --name "$TRIPLET_CONTAINER" --network "$NETWORK" \
    -p "127.0.0.1:$TRIPLET_PORT:8080" \
    -v "$IMAGE_DIR:/images:ro" \
    -v "$OUT_DIR/triplet.yaml:/etc/triplet/config.yaml:ro" \
    "$TRIPLET_IMAGE" -config /etc/triplet/config.yaml >/dev/null

  docker run -d --name "$CANTALOUPE_CONTAINER" --network "$NETWORK" \
    -p "127.0.0.1:$CANTALOUPE_PORT:8182" \
    -v "$IMAGE_DIR:/images:ro" \
    -v "$OUT_DIR/cantaloupe.properties:/etc/cantaloupe.properties:ro" \
    -v "$OUT_DIR/cantaloupe.properties:/etc/cantaloupe/cantaloupe.properties:ro" \
    -e CANTALOUPE_CONFIG=/etc/cantaloupe.properties \
    "$CANTALOUPE_IMAGE" >/dev/null

  wait_http triplet "http://127.0.0.1:$TRIPLET_PORT/healthz"
  wait_any_http cantaloupe "http://127.0.0.1:$CANTALOUPE_PORT/"

  first_encoded="$(urlencode "${images[0]}")"
  if ! curl -fsS -o /dev/null "http://127.0.0.1:$CANTALOUPE_PORT/iiif/3/$first_encoded/info.json"; then
    echo "warning: Cantaloupe did not return info.json for ${images[0]}; benchmark will record per-request failures" >&2
  fi

  : >"$OUT_DIR/container-stats.jsonl"
  collect_stats &
  STATS_PID=$!

  printf 'server,pass,image,request_name,request_path,curl_exit,http_code,time_namelookup,time_connect,time_starttransfer,time_total,size_download\n' >"$OUT_DIR/requests.csv"
  queue="$OUT_DIR/request-queue.args"
  : >"$queue"
  for pass in $(seq 1 "$PASSES"); do
    for image in "${images[@]}"; do
      while IFS=$'\t' read -r request_name request_path; do
        if [ -z "${request_name:-}" ] || [[ "$request_name" == \#* ]]; then
          continue
        fi
        printf '%s\0%s\0%s\0%s\0%s\0%s\0' "triplet" "http://127.0.0.1:$TRIPLET_PORT" "$image" "$pass" "$request_name" "$request_path" >>"$queue"
        printf '%s\0%s\0%s\0%s\0%s\0%s\0' "cantaloupe" "http://127.0.0.1:$CANTALOUPE_PORT" "$image" "$pass" "$request_name" "$request_path" >>"$queue"
      done <"$REQUESTS_FILE"
    done
  done

  export OUT_DIR CURL_TIMEOUT
  write_worker
  xargs -0 -P "$CONCURRENCY" -n 6 /bin/bash "$OUT_DIR/run-one.sh" <"$queue"
  for line in "$OUT_DIR"/request-lines/*.csv; do
    [ -e "$line" ] || continue
    cat "$line" >>"$OUT_DIR/requests.csv"
  done

  python3 "$ROOT_DIR/scripts/benchmark-summary.py" "$OUT_DIR/requests.csv" "$OUT_DIR/summary.csv"
  python3 "$ROOT_DIR/scripts/benchmark-stats-summary.py" "$OUT_DIR/container-stats.jsonl" "$OUT_DIR/resource-summary.csv"
  echo "Benchmark complete: $OUT_DIR"
  echo "Request timings: $OUT_DIR/requests.csv"
  echo "Summary: $OUT_DIR/summary.csv"
  echo "Resource summary: $OUT_DIR/resource-summary.csv"
  echo "Container stats: $OUT_DIR/container-stats.jsonl"
}

main "$@"
