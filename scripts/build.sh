#!/usr/bin/env bash

set -euo pipefail

IMAGE_TAG="${TRIPLET_BUILD_IMAGE:-triplet-build:dev}"
BIN_PATH="${BIN_PATH:-bin/triplet}"

mkdir -p "$(dirname "$BIN_PATH")"

echo "Building builder image: $IMAGE_TAG"
docker build --target build -t "$IMAGE_TAG" .

CID=$(docker create "$IMAGE_TAG")
cleanup() {
  docker rm -f "$CID" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker cp "$CID:/out/triplet" "$BIN_PATH"
echo "Wrote $BIN_PATH"
