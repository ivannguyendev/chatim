#!/usr/bin/env bash
set -u

dir="${1:?usage: bench-sample.sh DIR start|stop}"
action="${2:?usage: bench-sample.sh DIR start|stop}"
vm="chatim-bench-vm"

start() {
  mkdir -p "$dir"
  local pids=()
  docker rm -f "$vm" >/dev/null 2>&1
  docker run -d --rm --name "$vm" alpine sh -c 'while :; do echo "$(date +%s) $(head -1 /proc/stat)"; sleep 1; done' >/dev/null

  (while :; do docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' | grep chatim | sed "s/^/$(date +%s) /"; sleep 1; done) >"$dir/dstats.log" 2>&1 &
  pids+=($!)

  if command -v pmset >/dev/null 2>&1; then
    (while :; do echo "$(date +%s) $(pmset -g therm 2>/dev/null | grep CPU_Speed_Limit | tr -s ' \t' ' ')"; sleep 2; done) >"$dir/therm.log" 2>&1 &
    pids+=($!)
  fi

  docker exec chatim-redis-dedupe sh -c 'read -r REDISCLI_AUTH < "$REDIS_SECRET_FILE"; export REDISCLI_AUTH; while :; do echo "$(date +%s) $(redis-cli info stats | grep -E "^(total_connections_received|total_commands_processed):" | tr -d "\r" | tr "\n" " ")"; sleep 1; done' >"$dir/redis.log" 2>&1 &
  pids+=($!)

  docker exec chatim-mongodb mongosh --quiet --eval '
db.getSiblingDB("admin").auth(process.env.MONGO_INITDB_ROOT_USERNAME, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim());
while (true) {
  const s = db.getSiblingDB("admin").serverStatus();
  const k = s.wiredTiger.checkpoint;
  print(Math.floor(Date.now() / 1000), "ckpt_last_ms=" + k["most recent time (msecs)"], "ckpt_total_ms=" + k["total time (msecs)"], "ckpts=" + k["total succeed number of checkpoints"], "dirty_bytes=" + s.wiredTiger.cache["tracked dirty bytes in the cache"]);
  sleep(1000);
}' >"$dir/mongo.log" 2>&1 &
  pids+=($!)

  echo "${pids[*]}" >"$dir/pids"
}

stop() {
  if [ -f "$dir/pids" ]; then
    for p in $(cat "$dir/pids"); do
      pkill -P "$p" 2>/dev/null
      kill "$p" 2>/dev/null
    done
    rm -f "$dir/pids"
  fi
  docker logs "$vm" >"$dir/vm.log" 2>&1
  docker rm -f "$vm" >/dev/null 2>&1
  docker exec chatim-redis-dedupe sh -c 'pkill -f "[w]hile :"; true' >/dev/null 2>&1
  docker exec chatim-mongodb sh -c 'pkill -f "[s]erverStatus"; true' >/dev/null 2>&1
}

case "$action" in
  start) start ;;
  stop) stop ;;
  *) echo "action must be start or stop" >&2; exit 1 ;;
esac
