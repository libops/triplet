#!/usr/bin/env bash

set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose/docker-compose.yaml}"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-triplet-integration}"
COMPOSE_PROFILES="${COMPOSE_PROFILES:-integration}"
SERVICE="${MARIADB_SERVICE:-mariadb}"
TRIPLET_SERVICE="${TRIPLET_SERVICE:-triplet}"
VALIDATOR_IDENTIFIER="67352ccc-d1b0-11e1-89ae-279075081939.png"
VALIDATOR_IMAGE_URL="${IIIF_VALIDATOR_IMAGE_URL:-https://iiif.io/api/image/validator/$VALIDATOR_IDENTIFIER}"
CONFORMANCE_IDENTIFIER="${TRIPLET_CONFORMANCE_IDENTIFIER:-$VALIDATOR_IDENTIFIER}"
TRIPLET_HEALTH_URL="${TRIPLET_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ "${CI:-}" = "true" ]; then
  REBUILD_TRIPLET_IMAGE="${TEST_INTEGRATION_REBUILD:-0}"
else
  REBUILD_TRIPLET_IMAGE="${TEST_INTEGRATION_REBUILD:-1}"
fi

cd "$PROJECT_DIR"

BRANCH="${GIT_BRANCH:-main}"
TRIPLET_IMAGE="${TRIPLET_IMAGE:-ghcr.io/libops/triplet:${BRANCH}}"
export TRIPLET_IMAGE
export TRIPLET_CONFORMANCE_IDENTIFIER="$CONFORMANCE_IDENTIFIER"
export TRIPLET_PRESENTATION_WRITE_ENABLED="${TRIPLET_PRESENTATION_WRITE_ENABLED:-true}"
export TRIPLET_PRESENTATION_WRITE_TOKEN="${TRIPLET_PRESENTATION_WRITE_TOKEN:-triplet-integration-token}"

sample_png_sha() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

if [ "$CONFORMANCE_IDENTIFIER" = "sample.png" ] && { [ ! -f deploy/compose/images/sample.png ] || [ "$(sample_png_sha deploy/compose/images/sample.png)" = "d3c936ebdd73f46e6422d5946044c99d524554082272019ab738800318b04892" ]; }; then
  mkdir -p deploy/compose/images
  printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC' \
    | base64 -d > deploy/compose/images/sample.png
fi
if [ "$CONFORMANCE_IDENTIFIER" = "$VALIDATOR_IDENTIFIER" ] && [ ! -f "deploy/compose/images/$VALIDATOR_IDENTIFIER" ]; then
  mkdir -p deploy/compose/images
  echo "Downloading IIIF validator fixture: $VALIDATOR_IMAGE_URL"
  curl -fsSL -o "deploy/compose/images/$VALIDATOR_IDENTIFIER" "$VALIDATOR_IMAGE_URL"
fi

if [ "$REBUILD_TRIPLET_IMAGE" = "1" ]; then
  echo "Building Triplet image locally: $TRIPLET_IMAGE"
  docker build --target runtime -t "$TRIPLET_IMAGE" .
elif ! docker image inspect "$TRIPLET_IMAGE" >/dev/null 2>&1; then
  echo "Triplet image not found locally: $TRIPLET_IMAGE"
  if docker pull "$TRIPLET_IMAGE" >/dev/null 2>&1; then
    echo "Pulled Triplet image: $TRIPLET_IMAGE"
  else
    echo "Building Triplet image locally: $TRIPLET_IMAGE"
    docker build --target runtime -t "$TRIPLET_IMAGE" .
  fi
fi

COMPOSE_UP_ARGS=(up -d)
if [ "$REBUILD_TRIPLET_IMAGE" = "1" ]; then
  COMPOSE_UP_ARGS+=(--force-recreate)
fi
COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" "${COMPOSE_UP_ARGS[@]}"

CID="$(COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" ps -q "$SERVICE" | head -1)"
if [ -z "$CID" ]; then
  echo "failed to start $SERVICE from $COMPOSE_FILE" >&2
  exit 1
fi

echo "Waiting for $SERVICE to become healthy"
for _ in $(seq 1 60); do
  STATUS="$(docker inspect "$CID" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}running{{end}}')"
  if [ "$STATUS" = "healthy" ] || [ "$STATUS" = "running" ]; then
    break
  fi
  sleep 2
done

STATUS="$(docker inspect "$CID" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}running{{end}}')"
if [ "$STATUS" != "healthy" ] && [ "$STATUS" != "running" ]; then
  COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" logs --no-color "$SERVICE" >&2
  exit 1
fi

TRIPLET_CID="$(COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" ps -q "$TRIPLET_SERVICE" | head -1)"
if [ -z "$TRIPLET_CID" ]; then
  echo "failed to start $TRIPLET_SERVICE from $COMPOSE_FILE" >&2
  exit 1
fi

echo "Waiting for $TRIPLET_SERVICE to become healthy"
for _ in $(seq 1 60); do
  STATUS="$(docker inspect "$TRIPLET_CID" --format '{{.State.Status}}')"
  if [ "$STATUS" != "running" ]; then
    break
  fi
  if curl -fsS "$TRIPLET_HEALTH_URL" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

STATUS="$(docker inspect "$TRIPLET_CID" --format '{{.State.Status}}')"
if [ "$STATUS" != "running" ] || ! curl -fsS "$TRIPLET_HEALTH_URL" >/dev/null 2>&1; then
  COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" logs --no-color "$TRIPLET_SERVICE" >&2
  exit 1
fi

COMPOSE_FILE="$COMPOSE_FILE" COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT_NAME" COMPOSE_PROFILES="$COMPOSE_PROFILES" REQUIRE_INTEGRATION=1 ./scripts/test.sh "$@"

./scripts/conformance.sh
