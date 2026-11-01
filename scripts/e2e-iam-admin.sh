#!/usr/bin/env bash

iam16_account() {
  local user="$1"
  shift
  docker exec "$iam_container" /app account-create -tenant "$iam16_tenant" -username "$user" "$@" >"$state/iam16-$user.out"
  sed -n 's/^temporary password: //p' "$state/iam16-$user.out" >"$state/iam16-$user.pw"
  if [ ! -s "$state/iam16-$user.pw" ]; then
    echo "account-create printed no temporary password for $user" >&2
    exit 1
  fi
}

iam16_setup() {
  local old_umask
  old_umask="$(umask)"
  umask 077
  sed -n 's/^IAM_NODE_SECRET=//p' "$scripts/../.env" >"$state/iam16-node.secret"
  if [ ! -s "$state/iam16-node.secret" ]; then
    echo "IAM_NODE_SECRET missing from .env" >&2
    exit 1
  fi
  docker exec "$iam_container" /app tenant-create -id "$iam16_tenant" -name e2e >/dev/null
  docker exec "$iam_container" /app tenant-create -id "$iam16_other" -name e2e >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_other" -id kho-b -name "Kho B" >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_tenant" -id cong-ty -name "Công ty" >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_tenant" -id it -name IT -parent cong-ty >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_tenant" -id it-hcm -name "IT HCM" -parent it >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_tenant" -id sale -name Sale -parent cong-ty >/dev/null
  docker exec "$iam_container" /app group-create -tenant "$iam16_tenant" -id admins -name Admins -parent cong-ty >/dev/null
  docker exec "$iam_container" /app group-roles -tenant "$iam16_tenant" -group admins -roles tenant-admin >/dev/null
  iam16_account lan -role tenant-admin
  iam16_account binh
  iam16_account ha
  umask "$old_umask"
  docker exec "$iam_container" /app account-groups -tenant "$iam16_tenant" -username binh -groups it-hcm >/dev/null
  iam16_binh="$(sed -n 's/^account \([^ ]*\) created for .*/\1/p' "$state/iam16-binh.out")"
  iam16_ha="$(sed -n 's/^account \([^ ]*\) created for .*/\1/p' "$state/iam16-ha.out")"
  if ! grep -q '^role: tenant-admin$' "$state/iam16-lan.out"; then
    echo "account-create -role tenant-admin did not report the role for lan" >&2
    exit 1
  fi
}

phase16_iam_admin() {
  step="phase 16 iam admin tenants, groups, roles and accounts"
  iam16_tenant="e2e$(openssl rand -hex 4)"
  iam16_other="e2e$(openssl rand -hex 4)"
  iam16_setup
  step="phase 16 roles and permission checks through AdminService"
  cli e2e iam-admin -tenant "$iam16_tenant" -binh "$iam16_binh" -ha "$iam16_ha" -state /state
  step="phase 16 iam logs hold no secret"
  cat "$state/iam16-lan.pw" "$state/iam16-binh.pw" "$state/iam16-ha.pw" "$state/iam16-node.secret" "$state/iam-secrets" | grep -v '^$' | sort -u >"$state/iam16-all-secrets"
  docker logs "$iam_container" >"$state/iam16.log" 2>&1
  if grep -F -q -f "$state/iam16-all-secrets" "$state/iam16.log"; then
    echo "the logs of $iam_container hold a password, token or secret of phase 16" >&2
    exit 1
  fi
  echo "phase 16: the logs of $iam_container hold none of the $(wc -l <"$state/iam16-all-secrets" | tr -d ' ') secrets used"
}
