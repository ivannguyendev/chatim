#!/usr/bin/env bash

id15_now_ms() {
  perl -MTime::HiRes=time -e 'printf "%.3f\n", time * 1000'
}

id15_node_secret() {
  local old_umask
  old_umask="$(umask)"
  umask 077
  sed -n 's/^IDENTITY_NODE_SECRET=//p' "$scripts/../.env" >"$state/identity15-node.secret"
  umask "$old_umask"
  if [ ! -s "$state/identity15-node.secret" ]; then
    echo "IDENTITY_NODE_SECRET missing from .env" >&2
    exit 1
  fi
}

phase15_identity_directory() {
  step="phase 15 directory tenant, groups, issuer and key"
  id15_tenant="e2e$(openssl rand -hex 4)"
  id15_issuer="$id15_tenant-backend"
  identity_trust "$id15_tenant" "$id15_issuer" identity15 k1:EdDSA
  identity_admin group-create -tenant "$id15_tenant" -id kinh-doanh -name "Kinh doanh"
  identity_admin group-create -tenant "$id15_tenant" -id kinh-doanh-hcm -name "Kinh doanh HCM" -parent kinh-doanh
  id15_node_secret
  id15_args=(-tenant "$id15_tenant" -issuer "$id15_issuer" -state /state)
  step="phase 15 exchanges create lan and binh"
  echo "phase 15: tenant $id15_tenant trusts $id15_issuer with k1 (EdDSA)"
  cli e2e identity-directory -stage seed "${id15_args[@]}"
  identity_admin user-groups -issuer "$id15_issuer" -sub binh -groups kinh-doanh-hcm
  step="phase 15 GetUsers polled every 50ms shows the name set by user-set"
  rm -f "$state/identity15-poll-open" "$state/identity15-cli-ms"
  docker run --rm --name "$identity_watch" --network "$network" --user "$(id -u):$(id -g)" -v "$state":/state "$image" \
    e2e identity-directory -stage profile "${id15_args[@]}" &
  id15_watch=$!
  until [ -f "$state/identity15-poll-open" ]; do
    if ! kill -0 "$id15_watch" 2>/dev/null; then
      wait "$id15_watch"
      exit 1
    fi
    sleep 0.05
  done
  id15_start="$(id15_now_ms)"
  identity_admin user-set -issuer "$id15_issuer" -sub binh -name "Bình Trần"
  id15_end="$(id15_now_ms)"
  awk -v a="$id15_start" -v b="$id15_end" 'BEGIN { printf "%.3f\n", b - a }' >"$state/identity15-cli-ms.tmp"
  mv "$state/identity15-cli-ms.tmp" "$state/identity15-cli-ms"
  wait "$id15_watch"
  step="phase 15 user-disable reaches GetUsers, the removed feed is 404, a wrong secret is 401"
  identity_admin user-disable -issuer "$id15_issuer" -sub binh
  cli e2e identity-directory -stage disabled "${id15_args[@]}"
  step="phase 15 restart $identity_container and read the directory again"
  echo "restart: docker restart $identity_container"
  docker restart "$identity_container" >/dev/null
  cli e2e identity-directory -stage restarted "${id15_args[@]}"
  "$scripts/wait-healthy.sh" "$identity_container"
  step="phase 15 identity logs hold no token or secret"
  cat "$state/identity15-tokens" "$state/identity15-node.secret" >"$state/identity15-all-secrets"
  identity_logs_clean 15 "$state/identity15-all-secrets"
}
