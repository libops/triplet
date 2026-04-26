#!/usr/bin/env bash

set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose/docker-compose.yaml}"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-triplet-integration}"
COMPOSE_PROFILES="${COMPOSE_PROFILES:-integration}"
SERVICE="${MARIADB_SERVICE:-mariadb}"
TRIPLET_SERVICE="${TRIPLET_SERVICE:-triplet}"
CONFORMANCE_IDENTIFIER="${TRIPLET_CONFORMANCE_IDENTIFIER:-sample.ppm}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$PROJECT_DIR"

export TRIPLET_CONFORMANCE_IDENTIFIER="$CONFORMANCE_IDENTIFIER"
if [ "$CONFORMANCE_IDENTIFIER" = "sample.ppm" ] && [ ! -f deploy/compose/images/sample.ppm ]; then
  mkdir -p deploy/compose/images
  printf 'P3\n1 1\n255\n255 255 255\n' > deploy/compose/images/sample.ppm
fi

COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" up -d

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
  STATUS="$(docker inspect "$TRIPLET_CID" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}running{{end}}')"
  if [ "$STATUS" = "healthy" ] || [ "$STATUS" = "running" ]; then
    break
  fi
  sleep 2
done

STATUS="$(docker inspect "$TRIPLET_CID" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}running{{end}}')"
if [ "$STATUS" != "healthy" ] && [ "$STATUS" != "running" ]; then
  COMPOSE_PROFILES="$COMPOSE_PROFILES" docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" logs --no-color "$TRIPLET_SERVICE" >&2
  exit 1
fi

COMPOSE_FILE="$COMPOSE_FILE" COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT_NAME" COMPOSE_PROFILES="$COMPOSE_PROFILES" REQUIRE_INTEGRATION=1 ./scripts/test.sh "$@"

./scripts/conformance.sh
