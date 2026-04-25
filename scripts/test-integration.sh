#!/usr/bin/env bash

set -euo pipefail

COMPOSE_FILE="${COMPOSE_FILE:-deploy/compose/docker-compose.yaml}"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-triplet-integration}"
SERVICE="${MARIADB_SERVICE:-mariadb}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$PROJECT_DIR"

docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" up -d "$SERVICE"

CID="$(docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" ps -q "$SERVICE" | head -1)"
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
  docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT_NAME" logs --no-color "$SERVICE" >&2
  exit 1
fi

COMPOSE_FILE="$COMPOSE_FILE" COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT_NAME" REQUIRE_INTEGRATION=1 ./scripts/test.sh "$@"
