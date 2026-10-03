#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/../.env"
for _ in $(seq 1 90); do
  if docker exec chatim-postgres pg_isready -q -h 127.0.0.1 -U "$PG_USER" -d chatim_poc 2>/dev/null; then
    echo "postgres ready"
    exit 0
  fi
  sleep 1
done
echo "postgres not ready after 90s" >&2
exit 1
