#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PR_NUMBER="${PR_NUMBER:?PR_NUMBER is required}"
RUN_ID="${BENCH_RUN_ID:-pr-$PR_NUMBER-${GITHUB_RUN_ID:-local}}"
OUT_ROOT="${BENCH_OUT_ROOT:-$ROOT_DIR/results/benchmarks}"
RESULTS_FILE="${BENCH_RESULTS_FILE:-$ROOT_DIR/benchmark_results.md}"
LOG_FILE="${BENCH_LOG_FILE:-$ROOT_DIR/benchmark.log}"
BRANCH="${GITHUB_REF_NAME:-$(git -C "$ROOT_DIR" branch --show-current)}"
IMAGE_TAG="$(printf '%s' "$BRANCH" | sed 's/[^a-zA-Z0-9._-]//g' | awk '{print substr($0, length($0)-120)}')"

"$ROOT_DIR/scripts/setup-benchmark-fixtures.sh"

set +e
BENCH_RUN_ID="$RUN_ID" \
  BENCH_OUT_ROOT="$OUT_ROOT" \
  BENCH_TRIPLET_IMAGE="${BENCH_TRIPLET_IMAGE:-ghcr.io/libops/triplet:$IMAGE_TAG}" \
  BENCH_SKIP_BUILD="${BENCH_SKIP_BUILD:-1}" \
  BENCH_APPEND_RUN_REPORTS="${BENCH_APPEND_RUN_REPORTS:-0}" \
  "$ROOT_DIR/scripts/benchmark-iiif.sh" >"$LOG_FILE" 2>&1
status="$?"
set -e

{
  echo "<!-- triplet-iiif-benchmark-results -->"
  if [ -f "$OUT_ROOT/$RUN_ID/report.md" ]; then
    python3 "$ROOT_DIR/scripts/extract-benchmark-tldr.py" "$OUT_ROOT/$RUN_ID/report.md"
  else
    echo "# Benchmark Matrix: $RUN_ID"
    echo
    echo "Benchmark matrix failed before producing a report."
    echo
    echo '```text'
    tail -n 120 "$LOG_FILE"
    echo '```'
  fi
} >"$RESULTS_FILE"

if [ "$status" -ne 0 ]; then
  echo "benchmark matrix failed; last benchmark log lines:" >&2
  tail -n 120 "$LOG_FILE" >&2
fi

exit "$status"
