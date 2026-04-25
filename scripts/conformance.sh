#!/usr/bin/env bash

set -euo pipefail

BASE_URL="${1:-${TRIPLET_BASE_URL:-http://127.0.0.1:8080}}"
IDENTIFIER="${2:-${TRIPLET_CONFORMANCE_IDENTIFIER:-sample.png}}"

TMPDIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

INFO_URL="${BASE_URL%/}/iiif/3/${IDENTIFIER}/info.json"
BASE_IMAGE_URL="${BASE_URL%/}/iiif/3/${IDENTIFIER}"
IMAGE_URL="${BASE_IMAGE_URL}/full/64,/0/default.jpg"

assert_status() {
  local want="$1"
  local url="$2"
  local got
  got="$(curl -sS -o /dev/null -w '%{http_code}' "$url")"
  if [ "$got" != "$want" ]; then
    echo "$url returned HTTP $got, want $want" >&2
    exit 1
  fi
}

assert_header_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -Fiq "$needle" "$file"; then
    echo "missing header containing $needle in $file" >&2
    cat "$file" >&2
    exit 1
  fi
}

echo "Checking base redirect"
curl -fsS -D "$TMPDIR/base.headers" -o /dev/null "$BASE_IMAGE_URL"
assert_header_contains "$TMPDIR/base.headers" "HTTP/1.1 303"
assert_header_contains "$TMPDIR/base.headers" "Location:"

echo "Fetching $INFO_URL"
curl -fsS -D "$TMPDIR/info.headers" "$INFO_URL" -o "$TMPDIR/info.json"
assert_header_contains "$TMPDIR/info.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$TMPDIR/info.headers" "rel=\"profile\""

echo "Checking info HEAD"
curl -fsSI "$INFO_URL" -o "$TMPDIR/info-head.headers"
assert_header_contains "$TMPDIR/info-head.headers" "HTTP/1.1 200"

python3 - "$TMPDIR/info.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    doc = json.load(fh)

required = {
    "@context": "http://iiif.io/api/image/3/context.json",
    "type": "ImageService3",
    "protocol": "http://iiif.io/api/image",
    "profile": "level2",
}
for key, want in required.items():
    got = doc.get(key)
    if got != want:
        raise SystemExit(f"{key}: got {got!r}, want {want!r}")

for key in ("id", "width", "height"):
    if key not in doc:
        raise SystemExit(f"missing {key}")
PY

echo "Fetching $IMAGE_URL"
curl -fsS -D "$TMPDIR/default.headers" "$IMAGE_URL" -o "$TMPDIR/default.jpg"
test -s "$TMPDIR/default.jpg"
assert_header_contains "$TMPDIR/default.headers" "Content-Type: image/jpeg"
assert_header_contains "$TMPDIR/default.headers" "ETag:"
assert_header_contains "$TMPDIR/default.headers" "rel=\"canonical\""

ETAG="$(awk 'BEGIN{IGNORECASE=1} /^etag:/ {gsub(/\r/, "", $0); sub(/^etag:[[:space:]]*/, "", $0); print; exit}' "$TMPDIR/default.headers")"
if [ -z "$ETAG" ]; then
  echo "missing ETag value" >&2
  exit 1
fi
echo "Checking conditional GET"
got="$(curl -fsS -H "If-None-Match: $ETAG" -o /dev/null -w '%{http_code}' "$IMAGE_URL")"
if [ "$got" != "304" ]; then
  echo "conditional GET returned HTTP $got, want 304" >&2
  exit 1
fi

echo "Checking PNG derivative"
curl -fsS -D "$TMPDIR/png.headers" "${BASE_IMAGE_URL}/full/32,/0/default.png" -o "$TMPDIR/default.png"
test -s "$TMPDIR/default.png"
assert_header_contains "$TMPDIR/png.headers" "Content-Type: image/png"

echo "Checking PDF derivative"
curl -fsS -D "$TMPDIR/pdf.headers" "${BASE_IMAGE_URL}/full/32,/0/default.pdf" -o "$TMPDIR/default.pdf"
test -s "$TMPDIR/default.pdf"
assert_header_contains "$TMPDIR/pdf.headers" "Content-Type: application/pdf"

echo "Checking syntax rejection"
assert_status 400 "${BASE_IMAGE_URL}/full/max/0/bogus.jpg"

if command -v iiif-validate.py >/dev/null 2>&1; then
  echo "Running iiif-validate.py"
  iiif-validate.py "$INFO_URL"
else
  echo "iiif-validate.py not installed; local schema smoke checks passed"
fi
