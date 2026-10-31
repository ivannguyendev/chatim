#!/usr/bin/env bash

phase14_iam_revocation() {
  step="phase 14 iam tenant and account"
  iam14_tenant="e2e$(openssl rand -hex 4)"
  docker exec "$iam_container" /app tenant-create -id "$iam14_tenant" -name e2e >/dev/null
  docker exec "$iam_container" /app account-create -tenant "$iam14_tenant" -username minh | sed -n 's/^temporary password: //p' >"$state/iam-c.pw"
  chmod 600 "$state/iam-c.pw"
  cp "$state/iam-c.pw" "$state/iam14-secrets"
  step="phase 14 revoke reaches websocket-1 and the gateway"
  cli e2e iam-revoke -tenant "$iam14_tenant" -state /state
  cat "$state/iam-secrets" >>"$state/iam14-secrets"
  step="phase 14 revoke reaches websocket-2 cut from iam"
  iam_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$iam_container")"
  docker run -d --name "$iam_cut" --cap-add NET_ADMIN --network "container:$ws2_container" alpine:3.20 sh -c 'apk add -q --no-cache iptables >/dev/null && touch /ready && sleep infinity' >/dev/null
  cut_wait=0
  until docker exec "$iam_cut" test -f /ready 2>/dev/null; do
    cut_wait=$((cut_wait + 1))
    if [ "$cut_wait" -gt 60 ] || [ "$(docker inspect -f '{{.State.Running}}' "$iam_cut")" != true ]; then
      docker logs "$iam_cut" 2>&1 | tail -5 >&2
      exit 1
    fi
    sleep 1
  done
  docker exec "$iam_cut" iptables -I OUTPUT -p tcp -d "$iam_ip" --dport 8081 -j REJECT --reject-with tcp-reset
  cli e2e iam-revoke-partitioned -tenant "$iam14_tenant" -state /state
  cat "$state/iam-secrets" >>"$state/iam14-secrets"
  docker rm -f "$iam_cut" >/dev/null
  step="phase 14 key rotation without a restart"
  iam_kid="$(docker exec "$iam_container" /app key-rotate | sed -n 's/^signing key \([^ ]*\) created.*/\1/p')"
  if [ -z "$iam_kid" ]; then
    echo "key-rotate printed no key id" >&2
    exit 1
  fi
  cli e2e iam-key-rotated -tenant "$iam14_tenant" -kid "$iam_kid" -state /state
  cat "$state/iam-secrets" >>"$state/iam14-secrets"
  step="phase 14 tenant-disable closes live connections"
  rm -f "$state/iam-tenant-ready"
  cli e2e iam-tenant-wait -tenant "$iam14_tenant" -state /state &
  tenant_wait=$!
  until [ -f "$state/iam-tenant-ready" ]; do
    if ! kill -0 "$tenant_wait" 2>/dev/null; then
      wait "$tenant_wait"
      exit 1
    fi
    sleep 0.2
  done
  docker exec "$iam_container" /app tenant-disable -id "$iam14_tenant" >/dev/null
  wait "$tenant_wait"
  step="phase 14 logs hold no secret"
  sed -n 's/^IAM_NODE_SECRET=//p' "$scripts/../.env" >>"$state/iam14-secrets"
  grep -v '^$' "$state/iam14-secrets" >"$state/iam14-all-secrets"
  for node in "$iam_container" "$ws_container" "$ws2_container" "$gw_container"; do
    docker logs "$node" >"$state/iam14.log" 2>&1
    if grep -F -q -f "$state/iam14-all-secrets" "$state/iam14.log"; then
      echo "the logs of $node hold a password, token or secret of phase 14" >&2
      exit 1
    fi
  done
  echo "phase 14: the logs of iam, both websocket nodes and the gateway hold none of the $(wc -l <"$state/iam14-all-secrets" | tr -d ' ') secrets used"
}
