#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${BENCH_FIXTURE_DIR:-$ROOT_DIR/fixtures/benchmark/images}"
INCLUDE_LARGE="${BENCH_FIXTURE_LARGE:-0}"

mkdir -p "$OUT_DIR"

download() {
  local name="$1"
  local url="$2"
  local sha256="${3:-}"
  local min_bytes="${4:-1}"
  local dest="$OUT_DIR/$name"
  local tmp="$dest.tmp"

  if [ -f "$dest" ]; then
    echo "exists: $dest"
  else
    echo "download: $name"
    if ! curl -fL --retry 3 --retry-delay 2 -o "$tmp" "$url"; then
      rm -f "$tmp"
      echo "download failed: $url" >&2
      exit 1
    fi
    local size
    size="$(wc -c <"$tmp" | tr -d ' ')"
    if [ "$size" -lt "$min_bytes" ]; then
      rm -f "$tmp"
      echo "download too small for $name: $size bytes < $min_bytes bytes" >&2
      exit 1
    fi
    mv "$tmp" "$dest"
  fi

  if [ -n "$sha256" ]; then
    local actual
    actual="$(sha256_file "$dest")"
    if [ "$actual" != "$sha256" ]; then
      echo "sha256 mismatch for $dest" >&2
      echo "  got:  $actual" >&2
      echo "  want: $sha256" >&2
      exit 1
    fi
  fi
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

download \
  "iiif-validator-grid.png" \
  "https://iiif.io/api/image/validator/67352ccc-d1b0-11e1-89ae-279075081939.png" \
  "" \
  1000

download \
  "filesamples-640x426.tiff" \
  "https://filesamples.com/samples/image/tiff/sample_640%C3%97426.tiff" \
  "" \
  100000

download \
  "filesamples-1920x1280.tiff" \
  "https://filesamples.com/samples/image/tiff/sample_1920%C3%971280.tiff" \
  "" \
  1000000

download \
  "filesamples-5184x3456.tiff" \
  "https://filesamples.com/samples/image/tiff/sample_5184%C3%973456.tiff" \
  "" \
  10000000

download \
  "jpylyzer-reference.jp2" \
  "https://raw.githubusercontent.com/openpreserve/jpylyzer-test-files/master/files/reference.jp2" \
  "" \
  1000

download \
  "jpylyzer-openjpeg15.jp2" \
  "https://raw.githubusercontent.com/openpreserve/jpylyzer-test-files/master/files/openJPEG15.jp2" \
  "" \
  1000

if [ "$INCLUDE_LARGE" = "1" ]; then
  download \
    "nasa-curiosity-pia23623-m34.tif" \
    "https://mars.nasa.gov/system/downloadable_items/44642_PIA23623_M34.tif" \
    "28001696cc08237aa07a9f8c9c561b221a3ad9ea099b0deb53e4b8e58bb8c903" \
    700000000
else
  cat <<'EOF'

Skipping the 717 MiB NASA Curiosity TIFF. To include it:

  BENCH_FIXTURE_LARGE=1 make benchmark-fixtures
EOF
fi

cat <<EOF

Benchmark fixtures are in:
  $OUT_DIR

Run:
  make benchmark-iiif
EOF
