#!/usr/bin/env bash
set -euo pipefail

scripts="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$scripts/.." && pwd)"
node="${WSLOAD_KILL_NODE:-chatim-websocket-2}"
rate="${WSLOAD_RATE:-100}"
ready_wait="${WSLOAD_READY_WAIT:-900}"
out="bin/wsload/kill-$(date +%y%m%d-%H%M%S)"
log="$root/$out/run.log"
tool=""
killed=""
mkdir -p "$root/$out"

restore() {
  set +e
  if [ -n "$tool" ] && kill -0 "$tool" 2>/dev/null; then
    kill "$tool"
    wait "$tool"
  fi
  if [ -n "$killed" ]; then
    echo "restarting $node"
    docker start "$node" >/dev/null
    "$scripts/wait-core-healthy.sh" "$node"
  fi
}
trap restore EXIT

echo "wsload kill: $node at kill-ready, rate ${rate}/s, results in $out"
(cd "$root" && make -s poc TOOL=wsload POC_FLAGS="--ulimit nofile=65536:65536" ARGS="-scenario kill -rate $rate -out $out $*") >"$log" 2>&1 &
tool=$!

deadline=$((SECONDS + ready_wait))
until grep -qx kill-ready "$log"; do
  if ! kill -0 "$tool" 2>/dev/null; then
    tool=""
    echo "wsload ended before kill-ready" >&2
    tail -n 20 "$log" >&2
    exit 1
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "no kill-ready after ${ready_wait}s" >&2
    tail -n 20 "$log" >&2
    exit 1
  fi
  sleep 1
done

killed=1
docker kill -s KILL "$node" >/dev/null
echo "killed $node at $(date +%s)"

status=0
wait "$tool" || status=$?
tool=""
docker start "$node" >/dev/null
"$scripts/wait-core-healthy.sh" "$node"
killed=""
cat "$root/$out/summary.txt" 2>/dev/null || tail -n 20 "$log"
exit "$status"
