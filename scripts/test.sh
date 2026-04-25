#!/usr/bin/env bash

set -euo pipefail
set -x

IMAGE_TAG="${TRIPLET_TEST_IMAGE:-triplet-test:dev}"
GO_TEST_FLAGS="${GO_TEST_FLAGS:--v -race}"

mkdir -p .cache/go-build .cache/go-mod

# If mariadb is running via docker compose, join its network and set TEST_DSN
# so integration tests run alongside unit tests. Otherwise they are skipped.
NETWORK_ARGS=()
ENV_ARGS=()
DB_USER="${MARIADB_USER:-scribe}"
DB_NAME="${MARIADB_DATABASE:-scribe}"

MARIADB_ID=$(docker compose ps -q mariadb 2>/dev/null | head -1 || true)
if [ -n "$MARIADB_ID" ]; then
  NETWORK=$(docker inspect "$MARIADB_ID" \
    --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' \
    | awk '{print $1}' || true)
  if [ -n "$NETWORK" ]; then
    echo "MariaDB detected - running integration tests on network: $NETWORK"
    NETWORK_ARGS+=(--network "$NETWORK")
    if [ -f "./secrets/mariadb_password" ]; then
      DB_PASSWORD=$(tr -d '\n' < ./secrets/mariadb_password)
    else
      DB_PASSWORD="scribe"
    fi
    ENV_ARGS+=(-e "TEST_DSN=${DB_USER}:${DB_PASSWORD}@tcp(mariadb:3306)/${DB_NAME}?parseTime=true")
  fi
fi

echo "Building test image: $IMAGE_TAG"
docker build --target test-runner -t "$IMAGE_TAG" .

PKG_ARGS=("$@")
if [ "${#PKG_ARGS[@]}" -eq 0 ]; then
  PKG_ARGS=(./...)
fi

docker run --rm \
  "${NETWORK_ARGS[@]}" \
  "${ENV_ARGS[@]}" \
  -u "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -e GOCACHE=/tmp/go-build \
  -e GOMODCACHE=/go/pkg/mod \
  -v "$PWD:/app" \
  -v "$PWD/.cache/go-build:/tmp/go-build" \
  -v "$PWD/.cache/go-mod:/go/pkg/mod" \
  -w /app \
  "$IMAGE_TAG" \
  -lc 'export PATH="/usr/local/go/bin:$PATH"; /usr/local/go/bin/go test '"${GO_TEST_FLAGS}"' '"${PKG_ARGS[*]}"
