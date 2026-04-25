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
IMAGE_URL="${BASE_URL%/}/iiif/3/${IDENTIFIER}/full/64,/0/default.jpg"

echo "Fetching $INFO_URL"
curl -fsS "$INFO_URL" -o "$TMPDIR/info.json"

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
curl -fsS "$IMAGE_URL" -o "$TMPDIR/default.jpg"
test -s "$TMPDIR/default.jpg"

if command -v iiif-validate.py >/dev/null 2>&1; then
  echo "Running iiif-validate.py"
  iiif-validate.py "$INFO_URL"
else
  echo "iiif-validate.py not installed; local schema smoke checks passed"
fi
