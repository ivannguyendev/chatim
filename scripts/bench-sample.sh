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

  docker exec chatim-mongodb mongosh --quiet --eval '
db.getSiblingDB("admin").auth(process.env.MONGO_INITDB_ROOT_USERNAME, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim());
const roomLagSampler = db.getSiblingDB("chatim");
const msgKey = (room, seq) => { const b = Buffer.alloc(24); b.writeBigUInt64BE(room, 0); b.writeBigUInt64BE(0n, 8); b.writeBigUInt64BE(seq, 16); return BinData(0, b.toString("base64")); };
while (true) {
  const now = Date.now();
  const lags = [];
  for (const r of roomLagSampler.rooms.aggregate([{$match: {last_seq: {$gt: 0}}}, {$sample: {size: 20}}, {$project: {last_seq: 1}}]).toArray()) {
    const room = BigInt(r._id.toString());
    const next = roomLagSampler.messages.find({_id: {$gt: msgKey(room, BigInt(r.last_seq.toString())), $lte: msgKey(room, 18446744073709551614n)}}, {ts: 1}).sort({_id: 1}).limit(1).toArray();
    lags.push(next.length ? (now - next[0].ts.getTime()) / 1000 : 0);
  }
  lags.sort((x, y) => x - y);
  const pick = q => lags.length ? lags[Math.min(lags.length - 1, Math.floor(q * lags.length))].toFixed(3) : "-1";
  print(Math.floor(now / 1000), "room_lag_p50_s=" + pick(0.5), "room_lag_max_s=" + pick(1));
  sleep(1000);
}' >"$dir/room-lag.log" 2>&1 &
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
  docker exec chatim-mongodb sh -c 'pkill -f "[s]erverStatus"; pkill -f "[r]oomLagSampler"; true' >/dev/null 2>&1
}

case "$action" in
  start) start ;;
  stop) stop ;;
  *) echo "action must be start or stop" >&2; exit 1 ;;
esac
