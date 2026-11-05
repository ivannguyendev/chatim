#!/usr/bin/env bash
set -euo pipefail
limit="${CORE_WAIT_SECONDS:-120}"
deadline=$((SECONDS + limit))
for name in "$@"; do
  until [ "$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$name" 2>/dev/null)" = healthy ]; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "$name not healthy after ${limit}s, inspect it with: docker logs $name" >&2
      exit 1
    fi
    sleep 1
  done
  echo "$name healthy"
done
