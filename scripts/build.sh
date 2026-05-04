#!/usr/bin/env bash

set -euo pipefail

IMAGE_TAG="${TRIPLET_BUILD_IMAGE:-triplet-build:dev}"
BIN_PATH="${BIN_PATH:-bin/triplet}"
CACHE_CLEANUP_BIN_PATH="${CACHE_CLEANUP_BIN_PATH:-$(dirname "$BIN_PATH")/triplet-cache-cleanup}"

mkdir -p "$(dirname "$BIN_PATH")"
mkdir -p "$(dirname "$CACHE_CLEANUP_BIN_PATH")"

echo "Building builder image: $IMAGE_TAG"
docker build --target build -t "$IMAGE_TAG" .

CID=$(docker create "$IMAGE_TAG")
cleanup() {
  docker rm -f "$CID" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker cp "$CID:/out/triplet" "$BIN_PATH"
docker cp "$CID:/out/triplet-cache-cleanup" "$CACHE_CLEANUP_BIN_PATH"
echo "Wrote $BIN_PATH"
echo "Wrote $CACHE_CLEANUP_BIN_PATH"
