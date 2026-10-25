#!/usr/bin/env bash
set -euo pipefail

cut="${1:?usage: scripts/ws-nats-drill.sh CUT_SECONDS [cut|restart-nats]}"
mode="${2:-cut}"
scripts="$(cd "$(dirname "$0")" && pwd)"
root="$scripts/.."
network="${NETWORK:-chatim_default}"
image="${CORECLI_IMAGE:-chatim/corecli:dev}"
ws_container="${WS_CONTAINER:-chatim-websocket-1}"
ws_key="${WS_KEY:-$root/.local/ws-keys/private.jwk}"
lead="${DRILL_LEAD:-30}"
tail_seconds="${DRILL_TAIL:-120}"
bench_rate="${DRILL_BENCH_RATE:-180}"
drill_rate="${DRILL_RATE:-20}"
out="$root/bin/drill/ws-nats-$mode-${cut}s-$(date +%y%m%d-%H%M%S)"
metrics="chatim-drill-metrics-$$"
net="chatim-drill-net-$$"
total=$((lead + cut + tail_seconds))
remove_after=0
expect_resyncs=0
if [ "$mode" = cut ]; then
  remove_after=$((lead + cut / 2))
  if [ "$cut" -ge 120 ]; then
    expect_resyncs=1
  fi
fi
mkdir -p "$out"

nats_ip="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' chatim-nats)"
rules=(-p tcp -d "$nats_ip" --dport 4222 -j DROP)
back=(-p tcp -s "$nats_ip" --sport 4222 -j DROP)
cut_active=""

cleanup() {
  set +e
  if [ -n "$cut_active" ]; then
    docker exec "$net" iptables -D OUTPUT "${rules[@]}"
    docker exec "$net" iptables -D INPUT "${back[@]}"
  fi
  docker logs "$metrics" >"$out/metrics.txt" 2>&1
  docker rm -f "$metrics" "$net" >/dev/null 2>&1
}
trap cleanup EXIT

docker run -d --name "$metrics" --network "container:$ws_container" curlimages/curl:8.11.1 sh -c \
  'while :; do echo "ts $(date +%s)"; curl -s http://127.0.0.1:9090/metrics | grep -E "^chatim_websocket_(consumer_lag_seconds|consumer_resets_total|consumer_pending|catchup_jumps_total|catchup_jump_suppressed_total|catchup_jump_failures_total|resyncs_total|send_dropped_total|nats_connected|nats_reconnects_total|users_suspended|reloads_total|reload_seconds_(sum|count)|reload_failures_total|room_ids_load_seconds_(sum|count)|room_ids_load_failures_total|consumer_stale_skipped_total|connections)"; sleep 1; done' >/dev/null
if [ "$mode" = cut ]; then
  docker run -d --name "$net" --cap-add NET_ADMIN --network "container:$ws_container" alpine:3.20 sh -c 'apk add -q iptables >/dev/null && touch /ready && sleep infinity' >/dev/null
  until docker exec "$net" test -f /ready 2>/dev/null; do sleep 1; done
fi

echo "drill $mode ${cut}s: lead ${lead}s, tail ${tail_seconds}s, corebench ${bench_rate}/s + websocket ${drill_rate}/s, results in $out"
(cd "$root" && make -s poc TOOL=corebench ARGS="-tenant drill-bench -rooms 200 -rate $bench_rate -warmup 1s -duration $((total + 10))s" >"$out/corebench.txt" 2>&1) &
bench=$!
sleep 15
docker run --rm --network "$network" --user "$(id -u):$(id -g)" -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e ws-drill -duration "${total}s" -rate "$drill_rate" -remove-after "${remove_after}s" -expect-resyncs "$expect_resyncs" >"$out/client.txt" 2>&1 &
client=$!
sleep "$lead"

start="$(date +%s)"
if [ "$mode" = cut ]; then
  docker exec "$net" iptables -I OUTPUT "${rules[@]}"
  docker exec "$net" iptables -I INPUT "${back[@]}"
  cut_active=1
  echo "cut $ws_container from NATS $nats_ip:4222 at $start"
  sleep "$cut"
  docker exec "$net" iptables -D OUTPUT "${rules[@]}"
  docker exec "$net" iptables -D INPUT "${back[@]}"
  cut_active=""
else
  echo "docker restart chatim-nats at $start"
  docker restart chatim-nats >/dev/null
fi
restored="$(date +%s)"
echo "restored at $restored after $((restored - start))s"

client_status=0
wait "$client" || client_status=$?
wait "$bench" || true
docker logs "$metrics" >"$out/metrics.txt" 2>&1
echo "$start $restored" >"$out/window.txt"
"$scripts/ws-drill-summary.sh" "$out/metrics.txt" "$start" "$restored" | tee "$out/summary.txt"
docker logs --since "$start" "$ws_container" 2>&1 | grep -E "feed skipped|reload room ids failed" >"$out/ws-log.txt" || true
cat "$out/ws-log.txt"
tail -n 20 "$out/client.txt"
grep -E "sent|errors|p99|shed" "$out/corebench.txt" | tail -n 5 || true
exit "$client_status"
