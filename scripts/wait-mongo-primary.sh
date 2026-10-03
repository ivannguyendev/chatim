#!/usr/bin/env bash
set -euo pipefail
check='db.getSiblingDB("admin").auth(process.env.MONGO_INITDB_ROOT_USERNAME, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim());
const h = db.hello(); print(h.setName === "rs0" && h.isWritablePrimary)'
for _ in $(seq 1 90); do
  if docker exec chatim-mongodb mongosh --quiet --eval "$check" 2>/dev/null | grep -q true; then
    echo "mongodb primary ready"
    exit 0
  fi
  sleep 1
done
echo "mongodb primary not ready after 90s" >&2
exit 1
