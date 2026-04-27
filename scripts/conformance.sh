#!/usr/bin/env bash

set -euo pipefail

BASE_URL="${1:-${TRIPLET_BASE_URL:-http://localhost:8080}}"
IDENTIFIER="${2:-${TRIPLET_CONFORMANCE_IDENTIFIER:-67352ccc-d1b0-11e1-89ae-279075081939.png}}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TMPDIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMPDIR"
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
curl -fsS -D "$TMPDIR/base.headers" -o /dev/null "$BASE_IMAGE_URL"
assert_header_contains "$TMPDIR/base.headers" "HTTP/1.1 303"
assert_header_contains "$TMPDIR/base.headers" "Location:"

echo "Fetching $INFO_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$TMPDIR/info.headers" "$INFO_URL" -o "$TMPDIR/info.json"
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

if [ -n "$PRESENTATION_WRITE_TOKEN" ]; then
  WRITE_CANVAS="conformance-write-$$"
  WRITE_URL="${PRESENTATION_BASE_URL}/item-1/canvas/${WRITE_CANVAS}/annotations"
  WRITE_BODY="${TMPDIR}/write-annotations.json"
  UPDATED_BODY="${TMPDIR}/write-annotations-updated.json"
  cat > "$WRITE_BODY" <<JSON
{"@context":"http://iiif.io/api/presentation/3/context.json","id":"${WRITE_URL}","type":"AnnotationPage","items":[]}
JSON
  cat > "$UPDATED_BODY" <<JSON
{"@context":"http://iiif.io/api/presentation/3/context.json","id":"${WRITE_URL}","type":"AnnotationPage","items":[{"id":"${WRITE_URL}/annotation-1","type":"Annotation","motivation":"supplementing","body":{"type":"TextualBody","value":"updated"},"target":{"type":"SpecificResource","source":"${PRESENTATION_BASE_URL}/item-1/canvas/${WRITE_CANVAS}","selector":{"type":"FragmentSelector","value":"xywh=1,2,3,4"}}}]}
JSON

  echo "Checking Presentation write authorization"
  got="$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H "If-Match: *" -H "Content-Type: application/ld+json" --data-binary "@${WRITE_BODY}" "$WRITE_URL")"
  if [ "$got" != "401" ]; then
    echo "unauthorized annotation PUT returned HTTP $got, want 401" >&2
    exit 1
  fi

  echo "Checking Presentation create precondition"
  curl -fsS -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: *" -H "Content-Type: application/ld+json" -D "$TMPDIR/write-create.headers" --data-binary "@${WRITE_BODY}" "$WRITE_URL" -o /dev/null
  WRITE_ETAG="$(awk 'tolower($0) ~ /^etag:/ {gsub(/\r/, "", $0); sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}' "$TMPDIR/write-create.headers")"
  if [ -z "$WRITE_ETAG" ]; then
    echo "annotation PUT create missing ETag" >&2
    exit 1
  fi

  echo "Checking Presentation update precondition"
  curl -fsS -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: ${WRITE_ETAG}" -H "Content-Type: application/ld+json" -D "$TMPDIR/write-update.headers" --data-binary "@${UPDATED_BODY}" "$WRITE_URL" -o /dev/null
  got="$(curl -sS -o /dev/null -w '%{http_code}' -X PUT -H "Authorization: Bearer ${PRESENTATION_WRITE_TOKEN}" -H "If-Match: ${WRITE_ETAG}" -H "Content-Type: application/ld+json" --data-binary "@${UPDATED_BODY}" "$WRITE_URL")"
  if [ "$got" != "412" ]; then
    echo "stale annotation PUT returned HTTP $got, want 412" >&2
    exit 1
  fi
fi

echo "Fetching $IMAGE_URL"
curl -fsS -D "$TMPDIR/default.headers" "$IMAGE_URL" -o "$TMPDIR/default.jpg"
test -s "$TMPDIR/default.jpg"
assert_header_contains "$TMPDIR/default.headers" "Content-Type: image/jpeg"
assert_header_contains "$TMPDIR/default.headers" "ETag:"
assert_header_contains "$TMPDIR/default.headers" "rel=\"canonical\""

ETAG="$(awk 'tolower($0) ~ /^etag:/ {gsub(/\r/, "", $0); sub(/^[^:]+:[[:space:]]*/, "", $0); print; exit}' "$TMPDIR/default.headers")"
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

echo "Running iiif-validate.py"
VALIDATOR="$("$ROOT_DIR/scripts/ensure-iiif-validator.sh")"
SCHEME="${BASE_URL%%://*}"
SERVER="${BASE_URL#*://}"
SERVER="${SERVER%%/*}"
"$VALIDATOR" --scheme "$SCHEME" -s "$SERVER" -p iiif/3 -i "$IDENTIFIER" --version=3.0 --level=2 --quiet

echo "Fetching $MANIFEST_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$TMPDIR/manifest.headers" "$MANIFEST_URL" -o "$TMPDIR/manifest.json"
assert_header_contains "$TMPDIR/manifest.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$TMPDIR/manifest.headers" "Content-Type: application/ld+json"

echo "Checking manifest HEAD"
curl -fsSI "$MANIFEST_URL" -o "$TMPDIR/manifest-head.headers"
assert_header_contains "$TMPDIR/manifest-head.headers" "HTTP/1.1 200"

echo "Fetching $ANNOTATIONS_URL"
curl -fsS -H "Origin: https://viewer.example.edu" -D "$TMPDIR/annotations.headers" "$ANNOTATIONS_URL" -o "$TMPDIR/annotations.json"
assert_header_contains "$TMPDIR/annotations.headers" "Access-Control-Allow-Origin: *"
assert_header_contains "$TMPDIR/annotations.headers" "Content-Type: application/ld+json"

echo "Checking annotation page HEAD"
curl -fsSI "$ANNOTATIONS_URL" -o "$TMPDIR/annotations-head.headers"
assert_header_contains "$TMPDIR/annotations-head.headers" "HTTP/1.1 200"

echo "Validating Presentation API responses"
python3 - "$TMPDIR/manifest.json" "$TMPDIR/annotations.json" <<'PY'
import json
import re
import sys

presentation_context = "http://iiif.io/api/presentation/3/context.json"
text_granularity_context = "http://iiif.io/api/extension/text-granularity/context.json"


def contexts(doc):
    value = doc.get("@context")
    if isinstance(value, str):
        return {value}
    if isinstance(value, list):
        return {v for v in value if isinstance(v, str)}
    return set()


with open(sys.argv[1], "r", encoding="utf-8") as fh:
    manifest = json.load(fh)

if presentation_context not in contexts(manifest):
    raise SystemExit("manifest missing Presentation 3 context")
if manifest.get("type") != "Manifest":
    raise SystemExit(f"manifest type = {manifest.get('type')!r}, want Manifest")
if not manifest.get("id"):
    raise SystemExit("manifest id is required")
items = manifest.get("items")
if not isinstance(items, list) or not items:
    raise SystemExit("manifest items must be a non-empty list")
canvas = items[0]
if canvas.get("type") != "Canvas":
    raise SystemExit("manifest first item must be a Canvas")
if not canvas.get("id") or not isinstance(canvas.get("annotations"), list):
    raise SystemExit("canvas id and annotations are required")

with open(sys.argv[2], "r", encoding="utf-8") as fh:
    page = json.load(fh)

page_contexts = contexts(page)
if presentation_context not in page_contexts:
    raise SystemExit("annotation page missing Presentation 3 context")
if text_granularity_context not in page_contexts:
    raise SystemExit("annotation page missing Text Granularity context")
if page.get("type") != "AnnotationPage":
    raise SystemExit(f"annotation page type = {page.get('type')!r}, want AnnotationPage")
annotations = page.get("items")
if not isinstance(annotations, list) or not annotations:
    raise SystemExit("annotation page items must be a non-empty list")

for idx, annotation in enumerate(annotations):
    if annotation.get("type") != "Annotation":
        raise SystemExit(f"annotation {idx} type must be Annotation")
    if annotation.get("textGranularity") not in {"line", "word", "glyph"}:
        raise SystemExit(f"annotation {idx} has invalid textGranularity")
    motivation = annotation.get("motivation")
    if isinstance(motivation, str):
        motivations = {motivation}
    elif isinstance(motivation, list):
        motivations = {v for v in motivation if isinstance(v, str)}
    else:
        motivations = set()
    if "supplementing" not in motivations:
        raise SystemExit(f"annotation {idx} must include supplementing motivation")
    body = annotation.get("body")
    if not isinstance(body, dict) or body.get("type") != "TextualBody" or not isinstance(body.get("value"), str):
        raise SystemExit(f"annotation {idx} must have a TextualBody value")
    target = annotation.get("target")
    if not isinstance(target, dict) or target.get("type") != "SpecificResource":
        raise SystemExit(f"annotation {idx} target must be a SpecificResource")
    selector = target.get("selector")
    if not isinstance(selector, dict) or selector.get("type") != "FragmentSelector":
        raise SystemExit(f"annotation {idx} selector must be a FragmentSelector")
    if not re.fullmatch(r"xywh=\d+,\d+,\d+,\d+", str(selector.get("value", ""))):
        raise SystemExit(f"annotation {idx} selector must be an xywh fragment")
PY
