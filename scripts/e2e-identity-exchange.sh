#!/usr/bin/env bash

identity_admin() {
  docker exec "$identity_container" /app "$@" >/dev/null
}

identity_trust() {
  identity_admin tenant-create -id "$1" -name e2e
  identity_admin issuer-add -issuer "$2" -tenant "$1"
  identity_issuer_keys "${@:2}"
}

identity_issuer_keys() {
  local issuer="$1" prefix="$2"
  shift 2
  local pair kid alg jwk
  for pair in "$@"; do
    kid="${pair%%:*}"
    alg="${pair##*:}"
    jwk="$(cli dev-issuer-key -alg "$alg" -kid "$kid" -out "/state/$prefix-$kid.jwk")"
    identity_admin issuer-key-add -issuer "$issuer" -kid "$kid" -alg "$alg" -jwk "$jwk"
  done
}

identity_logs_clean() {
  local phase="$1" list="$2"
  if [ ! -s "$list" ]; then
    echo "phase $phase wrote no token list" >&2
    exit 1
  fi
  grep -v '^$' "$list" | sort -u >"$list.sorted"
  docker logs "$identity_container" >"$state/identity$phase.log" 2>&1
  if grep -F -q -f "$list.sorted" "$state/identity$phase.log"; then
    echo "the logs of $identity_container hold a token or secret of phase $phase" >&2
    exit 1
  fi
  echo "phase $phase: the logs of $identity_container hold none of the $(wc -l <"$list.sorted" | tr -d ' ') tokens and secrets used"
}

identity_realm_issuer() {
  local tenant="$1" issuer="$2" realm="$3" prefix="$4" got want
  want="issuer $issuer trusted for tenant $tenant, realm $realm"
  if [ "$realm" = "$issuer" ]; then
    got="$(docker exec "$identity_container" /app issuer-add -issuer "$issuer" -tenant "$tenant")"
  else
    got="$(docker exec "$identity_container" /app issuer-add -issuer "$issuer" -tenant "$tenant" -realm "$realm")"
  fi
  if [ "$got" != "$want" ]; then
    echo "issuer-add printed \"$got\", want \"$want\"" >&2
    exit 1
  fi
  identity_issuer_keys "$issuer" "$prefix" k1:ES256
}

id13_user_find() {
  local got want
  want="$id13_tenant $(cat "$state/identity13-user")"
  got="$(docker exec "$identity_container" /app user-find -issuer "$id13_issuer" -sub ha)"
  if [ "$got" != "$want" ]; then
    echo "user-find printed \"$got\", want \"$want\"" >&2
    exit 1
  fi
  echo "phase 13: user-find -issuer $id13_issuer -sub ha printed the tenant and the chat id of the token: $got"
}

phase13_identity_exchange() {
  step="phase 13 tenant, trusted issuer and keys k1 ES256 and k2 RS256"
  id13_tenant="e2e$(openssl rand -hex 4)"
  id13_issuer="$id13_tenant-backend"
  identity_trust "$id13_tenant" "$id13_issuer" identity k1:ES256 k2:RS256
  id13_args=(-tenant "$id13_tenant" -issuer "$id13_issuer" -state /state -url "$ws_url" -gateway "$gw_url")
  step="phase 13 exchange, websocket, gateway, profile writes and refusals"
  echo "phase 13: tenant $id13_tenant trusts $id13_issuer with k1 (ES256) and k2 (RS256)"
  cli e2e identity-exchange -stage ok "${id13_args[@]}"
  step="phase 13 an issuer with its own realm and one sharing the realm of $id13_issuer"
  identity_realm_issuer "$id13_tenant" "$id13_tenant-other" "$id13_tenant-other" identity-other
  identity_realm_issuer "$id13_tenant" "$id13_tenant-realm" "$id13_issuer" identity-realm
  cli e2e identity-exchange -stage realms -other-issuer "$id13_tenant-other" -realm-issuer "$id13_tenant-realm" "${id13_args[@]}"
  step="phase 13 user-find by issuer and sub"
  id13_user_find
  step="phase 13 user-disable refuses the exchange, user-enable restores it"
  identity_admin user-disable -issuer "$id13_issuer" -sub ha
  cli e2e identity-exchange -stage disabled "${id13_args[@]}"
  identity_admin user-enable -issuer "$id13_issuer" -sub ha
  cli e2e identity-exchange -stage enabled "${id13_args[@]}"
  step="phase 13 issuer-key-remove k1 while k2 keeps working"
  identity_admin issuer-key-remove -issuer "$id13_issuer" -kid k1
  cli e2e identity-exchange -stage key-removed "${id13_args[@]}"
  step="phase 13 tenant-disable refuses the exchange"
  identity_admin tenant-disable -id "$id13_tenant"
  cli e2e identity-exchange -stage tenant-disabled "${id13_args[@]}"
  step="phase 13 identity logs hold no token"
  identity_logs_clean 13 "$state/identity-tokens"
}
