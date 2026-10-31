#!/usr/bin/env bash

phase13_iam_login() {
  step="phase 13 iam tenant and accounts"
  iam_tenant="e2e$(openssl rand -hex 4)"
  docker exec "$iam_container" /app tenant-create -id "$iam_tenant" -name e2e >/dev/null
  for pair in lan:iam-a binh:iam-b; do
    docker exec "$iam_container" /app account-create -tenant "$iam_tenant" -username "${pair%%:*}" | sed -n 's/^temporary password: //p' >"$state/${pair##*:}.pw"
    chmod 600 "$state/${pair##*:}.pw"
    if [ ! -s "$state/${pair##*:}.pw" ]; then
      echo "account-create printed no temporary password for ${pair%%:*}" >&2
      exit 1
    fi
  done
  step="phase 13 iam login, refresh and sessions"
  cli e2e iam-login -tenant "$iam_tenant" -state /state
  step="phase 13 iam account-disable"
  docker exec "$iam_container" /app account-disable -tenant "$iam_tenant" -username lan >/dev/null
  cli e2e iam-disabled -tenant "$iam_tenant" -state /state
  step="phase 13 iam logs hold no secret"
  cat "$state/iam-a.pw" "$state/iam-b.pw" "$state/iam-secrets" | grep -v '^$' >"$state/iam-all-secrets"
  docker logs "$iam_container" >"$state/iam.log" 2>&1
  if grep -F -q -f "$state/iam-all-secrets" "$state/iam.log"; then
    echo "the logs of $iam_container hold a password, token or proof of phase 13" >&2
    exit 1
  fi
  echo "phase 13: the logs of $iam_container hold none of the $(wc -l <"$state/iam-all-secrets" | tr -d ' ') secrets used"
}
