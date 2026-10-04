#!/usr/bin/env bash
set -u

name="${1:?usage: bench-cell.sh NAME RATE [reset] [pprof]}"
rate="${2:?usage: bench-cell.sh NAME RATE [reset] [pprof]}"
shift 2
reset=""
pprof=""
for opt in "$@"; do
  case "$opt" in
    reset) reset=1 ;;
    pprof) pprof=1 ;;
    *) echo "unknown option $opt" >&2; exit 1 ;;
  esac
done

repo="$(cd "$(dirname "$0")/.." && pwd)"
dir="$repo/bin/bench/$name"
cd "$repo"
mkdir -p "$dir"

mongo_eval() {
  docker exec chatim-mongodb mongosh --quiet --eval "
db.getSiblingDB('admin').auth(process.env.MONGO_INITDB_ROOT_USERNAME, require('fs').readFileSync('/run/secrets/mongo_password', 'utf8').trim());
$1"
}

if [ -n "$reset" ]; then
  echo "[$(date +%H:%M:%S)] $name: rest 300s, then reset"
  sleep 300
  make -s core-down >/dev/null 2>&1
  make -s infra-reset >/dev/null 2>&1
  make -s infra-up >/dev/null
  make -s core-up >/dev/null
fi

for cmd in "config set latency-monitor-threshold 5" "config set slowlog-log-slower-than 5000" "slowlog reset" "latency reset"; do
  make -s redis-cli INSTANCE=dedupe ARGS="$cmd" >/dev/null
done
mongo_eval "const c = db.getSiblingDB('chatim'); c.setProfilingLevel(0); c.system.profile.drop(); c.setProfilingLevel(1, {slowms: 20});" >/dev/null

snapshot() {
  mongo_eval "const a = db.getSiblingDB('admin'); printjson({top: a.runCommand({top: 1}).totals, opcounters: a.serverStatus().opcounters});" >"$dir/mongo-top-$1.json" 2>&1
}

snapshot before
start=$(date +%s)
echo "$start" >"$dir/start"
"$repo/scripts/bench-sample.sh" "$dir" start
if [ -n "$pprof" ]; then
  (sleep 20; docker run --rm --network chatim_default curlimages/curl -s "http://chatim-core-1:9090/debug/pprof/profile?seconds=20" >"$dir/cpu-core1.pb.gz" 2>/dev/null) &
fi
echo "[$(date +%H:%M:%S)] $name: corebench rate=$rate"
make -s poc TOOL=corebench ARGS="-rate $rate -duration 60s -watch 20" >"$dir/corebench.txt" 2>&1
wait
date +%s >"$dir/end"
snapshot after
"$repo/scripts/bench-sample.sh" "$dir" stop

make -s redis-cli INSTANCE=dedupe ARGS="latency history command" >"$dir/redis-latency.txt" 2>&1
make -s redis-cli INSTANCE=dedupe ARGS="slowlog get 256" >"$dir/redis-slowlog.txt" 2>&1
mongo_eval "db.getSiblingDB('chatim').system.profile.find({}, {ts: 1, millis: 1, op: 1, ns: 1}).forEach(d => print(Math.floor(d.ts.getTime() / 1000), d.millis, d.op, d.ns));" >"$dir/mongo-profile.txt" 2>&1
for c in chatim-core-1 chatim-core-2; do
  docker logs --since "$start" "$c" 2>&1 | grep -E 'degraded|recovered|rpc finished|reconcile|dropping' >"$dir/$c.log"
done
grep -E "^sends|^pacer|^ack latency|^live" "$dir/corebench.txt"
