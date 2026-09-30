#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/../.env"
for _ in $(seq 1 90); do
  if docker exec chatim-mongodb mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" \
    --authenticationDatabase admin --eval 'const h = db.hello(); print(h.setName === "rs0" && h.isWritablePrimary)' 2>/dev/null | grep -q true; then
    echo "mongodb primary ready"
    exit 0
  fi
  sleep 1
done
echo "mongodb primary not ready after 90s" >&2
exit 1
