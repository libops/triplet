#!/usr/bin/env bash

set -euo pipefail

IMAGE_TAG="${TRIPLET_TEST_IMAGE:-triplet-test:dev}"
SKIP_IMAGE_BUILD="${TRIPLET_TEST_SKIP_BUILD:-0}"
REQUIRE_INTEGRATION="${REQUIRE_INTEGRATION:-0}"
COMPOSE_ARGS=()
if [ -n "${COMPOSE_FILE:-}" ]; then
  COMPOSE_ARGS+=(-f "$COMPOSE_FILE")
fi
if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
  COMPOSE_ARGS+=(-p "$COMPOSE_PROJECT_NAME")
fi
if [ -n "${COMPOSE_PROFILES:-}" ]; then
  export COMPOSE_PROFILES
fi

mkdir -p .cache/go-build .cache/go-mod

# If mariadb is running via docker compose, join its network and set TEST_DSN
# so integration tests run alongside unit tests. Otherwise they are skipped.
NETWORK_ARGS=()
ENV_ARGS=()
DB_USER="${MARIADB_USER:-scribe}"
DB_NAME="${MARIADB_DATABASE:-scribe}"

MARIADB_ID=$(docker compose "${COMPOSE_ARGS[@]}" ps -q mariadb 2>/dev/null | head -1 || true)
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

if [ "$REQUIRE_INTEGRATION" = "1" ] && [ "${#NETWORK_ARGS[@]}" -eq 0 ]; then
  echo "REQUIRE_INTEGRATION=1 but no docker compose mariadb service is running" >&2
  echo "Start MariaDB with docker compose, then rerun make test-integration." >&2
  exit 1
fi

if [ "$SKIP_IMAGE_BUILD" = "1" ]; then
  if ! docker image inspect "$IMAGE_TAG" >/dev/null 2>&1; then
    echo "TRIPLET_TEST_SKIP_BUILD=1 but test image is missing: $IMAGE_TAG" >&2
    exit 1
  fi
  echo "Using existing test image: $IMAGE_TAG"
else
  echo "Building test image: $IMAGE_TAG"
  docker build --target test-runner -t "$IMAGE_TAG" .
fi

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
  -e "GO_TEST_FLAGS=${GO_TEST_FLAGS:-}" \
  -v "$PWD:/app" \
  -v "$PWD/.cache/go-build:/tmp/go-build" \
  -v "$PWD/.cache/go-mod:/go/pkg/mod" \
  -w /app \
  "$IMAGE_TAG" \
  /app/scripts/go-test-container.sh "${PKG_ARGS[@]}"
