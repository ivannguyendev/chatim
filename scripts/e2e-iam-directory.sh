#!/usr/bin/env bash

iam15_now_ms() {
  perl -MTime::HiRes=time -e 'printf "%.3f\n", time * 1000'
}

iam15_accounts() {
  local old_umask
  old_umask="$(umask)"
  umask 077
  sed -n 's/^IAM_NODE_SECRET=//p' "$scripts/../.env" >"$state/iam15-node.secret"
  docker exec "$iam_container" /app account-create -tenant "$iam15_tenant" -username lan -name Lan >"$state/iam15-lan.out"
  docker exec "$iam_container" /app account-create -tenant "$iam15_tenant" -username binh >"$state/iam15-binh.out"
  for user in lan binh; do
    sed -n 's/^temporary password: //p' "$state/iam15-$user.out" >"$state/iam15-$user.pw"
    if [ ! -s "$state/iam15-$user.pw" ]; then
      echo "account-create printed no temporary password for $user" >&2
      exit 1
    fi
  done
  umask "$old_umask"
  if [ ! -s "$state/iam15-node.secret" ]; then
    echo "IAM_NODE_SECRET missing from .env" >&2
    exit 1
  fi
  iam15_lan="$(sed -n 's/^account \([^ ]*\) created for .*/\1/p' "$state/iam15-lan.out")"
  iam15_binh="$(sed -n 's/^account \([^ ]*\) created for .*/\1/p' "$state/iam15-binh.out")"
}

phase15_iam_directory() {
  step="phase 15 iam directory tenant, groups and accounts"
  iam15_tenant="e2e$(openssl rand -hex 4)"
  docker exec "$iam_container" /app tenant-create -id "$iam15_tenant" -name e2e >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam15_tenant" -id kinh-doanh -name "Kinh doanh" >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam15_tenant" -id kinh-doanh-hcm -name "Kinh doanh HCM" -parent kinh-doanh >/dev/null
  iam15_accounts
  docker exec "$iam_container" /app account-groups -tenant "$iam15_tenant" -username binh -groups kinh-doanh-hcm >/dev/null
  iam15_args=(-tenant "$iam15_tenant" -lan "$iam15_lan" -binh "$iam15_binh" -state /state)
  : >"$state/iam15-secrets"
  step="phase 15 GetUsers and the long poll woken by profile-set"
  rm -f "$state/iam15-poll-open" "$state/iam15-cli-ms"
  docker run --rm --name "$iam_watch" --network "$network" --user "$(id -u):$(id -g)" -v "$state":/state "$image" \
    e2e iam-directory -stage profile "${iam15_args[@]}" &
  iam15_watch=$!
  until [ -f "$state/iam15-poll-open" ]; do
    if ! kill -0 "$iam15_watch" 2>/dev/null; then
      wait "$iam15_watch"
      exit 1
    fi
    sleep 0.05
  done
  iam15_start="$(iam15_now_ms)"
  docker exec "$iam_container" /app profile-set -tenant "$iam15_tenant" -username binh -name "Bình Trần" >/dev/null
  iam15_end="$(iam15_now_ms)"
  awk -v a="$iam15_start" -v b="$iam15_end" 'BEGIN { printf "%.3f\n", b - a }' >"$state/iam15-cli-ms.tmp"
  mv "$state/iam15-cli-ms.tmp" "$state/iam15-cli-ms"
  wait "$iam15_watch"
  cat "$state/iam-secrets" >>"$state/iam15-secrets"
  step="phase 15 account-disable reaches the feed and GetUsers"
  docker exec "$iam_container" /app account-disable -tenant "$iam15_tenant" -username binh >/dev/null
  cli e2e iam-directory -stage disabled "${iam15_args[@]}"
  cat "$state/iam-secrets" >>"$state/iam15-secrets"
  step="phase 15 restart $iam_container and read the directory again"
  echo "restart: docker restart $iam_container"
  docker restart "$iam_container" >/dev/null
  cli e2e iam-directory -stage restarted "${iam15_args[@]}"
  cat "$state/iam-secrets" >>"$state/iam15-secrets"
  "$scripts/wait-core-healthy.sh" "$iam_container"
  step="phase 15 iam logs hold no secret"
  cat "$state/iam15-lan.pw" "$state/iam15-binh.pw" "$state/iam15-node.secret" "$state/iam15-secrets" | grep -v '^$' | sort -u >"$state/iam15-all-secrets"
  docker logs "$iam_container" >"$state/iam15.log" 2>&1
  if grep -F -q -f "$state/iam15-all-secrets" "$state/iam15.log"; then
    echo "the logs of $iam_container hold a password, token or secret of phase 15" >&2
    exit 1
  fi
  echo "phase 15: the logs of $iam_container hold none of the $(wc -l <"$state/iam15-all-secrets" | tr -d ' ') secrets used"
}
