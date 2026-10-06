#!/usr/bin/env bash
set -euo pipefail

scripts="$(cd "$(dirname "$0")" && pwd)"
network="${NETWORK:-chatim_default}"
image="${CORECLI_IMAGE:-chatim/corecli:dev}"
first="${E2E_FIRST:-40}"
after="${E2E_AFTER:-40}"
victim="${E2E_KILL:-core-1}"
watcher="chatim-e2e-watch-$$"
state="$(mktemp -d "${TMPDIR:-/tmp}/chatim-e2e.XXXXXX")"
killed=""
result=FAIL
step="start"

cli() {
  docker run --rm --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state "$image" "$@"
}

restore() {
  if [ -n "$killed" ]; then
    echo "restore: docker start chatim-$killed"
    docker start "chatim-$killed" >/dev/null
    "$scripts/wait-core-healthy.sh" "chatim-$killed"
    killed=""
  fi
}

finish() {
  set +e
  docker rm -f "$watcher" >/dev/null 2>&1
  if [ -n "$killed" ]; then
    restore || result=FAIL
  fi
  rm -rf "$state"
  if [ "$result" = PASS ]; then
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live"
    exit 0
  fi
  echo "e2e FAIL during: $step" >&2
  exit 1
}
trap finish EXIT

case "$victim" in
  core-1 | core-2) ;;
  *) echo "E2E_KILL must be core-1 or core-2" >&2; exit 1 ;;
esac

step="both cores share the slots"
cli slots -cores 2 -wait 60s

step="create a room owned by $victim"
setup="$(cli e2e setup -state /state -owner "$victim")"
read -r room owner <<<"$setup"
if [ "$owner" != "$victim" ]; then
  echo "room $room is owned by '$owner', want $victim" >&2
  exit 1
fi
echo "phase 1: room $room, slot owned by $owner"

step="start the live watcher"
docker run -d --name "$watcher" --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state "$image" \
  watch -room "$room" -out /state/events.jsonl >/dev/null
for _ in $(seq 1 30); do
  if docker logs "$watcher" 2>&1 | grep '^watching ' >/dev/null; then
    break
  fi
  sleep 1
done
docker logs "$watcher" 2>&1 | grep '^watching '

step="phase 1 send"
cli e2e send -state /state -count "$first" -prefix a
step="phase 1 check"
cli e2e check -state /state

step="kill $owner"
echo "crash: docker kill chatim-$owner"
docker kill "chatim-$owner" >/dev/null
killed="$owner"

step="phase 2 send while the slot moves"
cli e2e send -state /state -count "$after" -prefix b -resend-last
step="phase 2 check"
cli e2e check -state /state

step="restart $owner"
restore
step="both cores share the slots again"
cli slots -cores 2 -wait 60s

step="phase 3 edit seq 1 and delete seq 2"
echo "phase 3: edit seq 1, delete seq 2 (base version 0)"
cli e2e change -state /state -edit 1 -delete 2
step="phase 3 check"
cli e2e check -state /state
result=PASS
