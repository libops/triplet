#!/usr/bin/env bash

set -euo pipefail

BASE_URL="${1:-${TRIPLET_BASE_URL:-http://localhost:8080}}"
IDENTIFIER="${2:-${TRIPLET_CONFORMANCE_IDENTIFIER:-67352ccc-d1b0-11e1-89ae-279075081939.png}}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CONFORMANCE_TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$CONFORMANCE_TMP_DIR"
}
trap cleanup EXIT

INFO_URL="${BASE_URL%/}/iiif/3/${IDENTIFIER}/info.json"
BASE_IMAGE_URL="${BASE_URL%/}/iiif/3/${IDENTIFIER}"
IMAGE_URL="${BASE_IMAGE_URL}/full/64,/0/default.jpg"
PRESENTATION_BASE_URL="${BASE_URL%/}/presentation/v3"
MANIFEST_URL="${PRESENTATION_BASE_URL}/item-1/manifest"
ANNOTATIONS_URL="${PRESENTATION_BASE_URL}/item-1/canvas/canvas-1/annotations"
PRESENTATION_WRITE_TOKEN="${TRIPLET_PRESENTATION_WRITE_TOKEN:-}"

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
curl -fsS -D "$CONFORMANCE_TMP_DIR/base.headers" -o /dev/null "$BASE_IMAGE_URL"
assert_header_contains "$CONFORMANCE_TMP_DIR/base.headers" "HTTP/1.1 303"
assert_header_contains "$CONFORMANCE_TMP_DIR/base.headers" "Location:"

echo "Fetching $INFO_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$CONFORMANCE_TMP_DIR/info.headers" "$INFO_URL" -o "$CONFORMANCE_TMP_DIR/info.json"
assert_header_contains "$CONFORMANCE_TMP_DIR/info.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$CONFORMANCE_TMP_DIR/info.headers" "rel=\"profile\""

echo "Checking info HEAD"
curl -fsSI "$INFO_URL" -o "$CONFORMANCE_TMP_DIR/info-head.headers"
assert_header_contains "$CONFORMANCE_TMP_DIR/info-head.headers" "HTTP/1.1 200"

if [ -n "$PRESENTATION_WRITE_TOKEN" ]; then
  WRITE_CANVAS="conformance-write-$$"
  WRITE_URL="${PRESENTATION_BASE_URL}/item-1/canvas/${WRITE_CANVAS}/annotations"
  WRITE_BODY="${CONFORMANCE_TMP_DIR}/write-annotations.json"
  UPDATED_BODY="${CONFORMANCE_TMP_DIR}/write-annotations-updated.json"
  cat > "$WRITE_BODY" <<JSON
{"@context":"http://iiif.io/api/presentation/3/context.json","id":"${WRITE_URL}","type":"AnnotationPage","items":[]}
JSON
  cat > "$UPDATED_BODY" <<JSON
{"@context":"http://iiif.io/api/presentation/3/context.json","id":"${WRITE_URL}","type":"AnnotationPage","items":[{"id":"${WRITE_URL}/annotation-1","type":"Annotation","motivation":"supplementing","body":{"type":"TextualBody","value":"updated"},"target":{"type":"SpecificResource","source":"${PRESENTATION_BASE_URL}/item-1/canvas/${WRITE_CANVAS}","selector":{"type":"FragmentSelector","value":"xywh=1,2,3,4"}}}]}
JSON

  echo "Checking Presentation write authorization"
  got="$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H "If-None-Match: *" -H "Content-Type: application/ld+json" --data-binary "@${WRITE_BODY}" "$WRITE_URL")"
  if [ "$got" != "401" ]; then
    echo "unauthorized annotation PUT returned HTTP $got, want 401" >&2
    exit 1
  fi

  echo "Checking Presentation create precondition"
  curl -fsS -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-None-Match: *" -H "Content-Type: application/ld+json" -D "$CONFORMANCE_TMP_DIR/write-create.headers" --data-binary "@${WRITE_BODY}" "$WRITE_URL" -o /dev/null
  assert_header_contains "$CONFORMANCE_TMP_DIR/write-create.headers" "HTTP/1.1 201"
  WRITE_ETAG="$(awk 'tolower($0) ~ /^etag:/ {gsub(/\r/, "", $0); sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}' "$CONFORMANCE_TMP_DIR/write-create.headers")"
  if [ -z "$WRITE_ETAG" ]; then
    echo "annotation PUT create missing ETag" >&2
    exit 1
  fi

  echo "Checking Presentation update precondition"
  curl -fsS -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: ${WRITE_ETAG}" -H "Content-Type: application/ld+json" -D "$CONFORMANCE_TMP_DIR/write-update.headers" --data-binary "@${UPDATED_BODY}" "$WRITE_URL" -o /dev/null
  UPDATED_ETAG="$(awk 'tolower($0) ~ /^etag:/ {gsub(/\r/, "", $0); sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}' "$CONFORMANCE_TMP_DIR/write-update.headers")"
  if [ -z "$UPDATED_ETAG" ]; then
    echo "annotation PUT update missing ETag" >&2
    exit 1
  fi
  got="$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: ${WRITE_ETAG}" -H "Content-Type: application/ld+json" --data-binary "@${UPDATED_BODY}" "$WRITE_URL")"
  if [ "$got" != "412" ]; then
    echo "stale annotation PUT returned HTTP $got, want 412" >&2
    exit 1
  fi

  echo "Checking Presentation conditional delete"
  curl -fsS -X DELETE -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: ${UPDATED_ETAG}" "$WRITE_URL" -o /dev/null
  assert_status 404 "$WRITE_URL"
fi

echo "Fetching $IMAGE_URL"
curl -fsS -D "$CONFORMANCE_TMP_DIR/default.headers" "$IMAGE_URL" -o "$CONFORMANCE_TMP_DIR/default.jpg"
test -s "$CONFORMANCE_TMP_DIR/default.jpg"
assert_header_contains "$CONFORMANCE_TMP_DIR/default.headers" "Content-Type: image/jpeg"
assert_header_contains "$CONFORMANCE_TMP_DIR/default.headers" "ETag:"
assert_header_contains "$CONFORMANCE_TMP_DIR/default.headers" "rel=\"canonical\""

ETAG="$(awk 'tolower($0) ~ /^etag:/ {gsub(/\r/, "", $0); sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}' "$CONFORMANCE_TMP_DIR/default.headers")"
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
curl -fsS -D "$CONFORMANCE_TMP_DIR/png.headers" "${BASE_IMAGE_URL}/full/32,/0/default.png" -o "$CONFORMANCE_TMP_DIR/default.png"
test -s "$CONFORMANCE_TMP_DIR/default.png"
assert_header_contains "$CONFORMANCE_TMP_DIR/png.headers" "Content-Type: image/png"

echo "Checking PDF derivative"
curl -fsS -D "$CONFORMANCE_TMP_DIR/pdf.headers" "${BASE_IMAGE_URL}/full/32,/0/default.pdf" -o "$CONFORMANCE_TMP_DIR/default.pdf"
test -s "$CONFORMANCE_TMP_DIR/default.pdf"
assert_header_contains "$CONFORMANCE_TMP_DIR/pdf.headers" "Content-Type: application/pdf"

echo "Checking syntax rejection"
assert_status 400 "${BASE_IMAGE_URL}/full/max/0/bogus.jpg"

echo "Running iiif-validate.py"
VALIDATOR="$("$ROOT_DIR/scripts/ensure-iiif-validator.sh")"
SCHEME="${BASE_URL%%://*}"
SERVER="${BASE_URL#*://}"
SERVER="${SERVER%%/*}"
"$VALIDATOR" --scheme "$SCHEME" -s "$SERVER" -p iiif/3 -i "$IDENTIFIER" --version=3.0 --level=2 --quiet

echo "Fetching $MANIFEST_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$CONFORMANCE_TMP_DIR/manifest.headers" "$MANIFEST_URL" -o "$CONFORMANCE_TMP_DIR/manifest.json"
assert_header_contains "$CONFORMANCE_TMP_DIR/manifest.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$CONFORMANCE_TMP_DIR/manifest.headers" "Content-Type: application/ld+json"

echo "Checking manifest HEAD"
curl -fsSI "$MANIFEST_URL" -o "$CONFORMANCE_TMP_DIR/manifest-head.headers"
assert_header_contains "$CONFORMANCE_TMP_DIR/manifest-head.headers" "HTTP/1.1 200"

echo "Fetching $ANNOTATIONS_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$CONFORMANCE_TMP_DIR/annotations.headers" "$ANNOTATIONS_URL" -o "$CONFORMANCE_TMP_DIR/annotations.json"
assert_header_contains "$CONFORMANCE_TMP_DIR/annotations.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$CONFORMANCE_TMP_DIR/annotations.headers" "Content-Type: application/ld+json"

echo "Checking annotation page HEAD"
curl -fsSI "$ANNOTATIONS_URL" -o "$CONFORMANCE_TMP_DIR/annotations-head.headers"
assert_header_contains "$CONFORMANCE_TMP_DIR/annotations-head.headers" "HTTP/1.1 200"

echo "Validating Presentation API responses"
(
  cd "$ROOT_DIR"
  go run ./cmd/triplet-conformance-check \
    -info "$CONFORMANCE_TMP_DIR/info.json" \
    -manifest "$CONFORMANCE_TMP_DIR/manifest.json" \
    -annotation-page "$CONFORMANCE_TMP_DIR/annotations.json"
)
