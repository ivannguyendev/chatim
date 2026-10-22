#!/usr/bin/env bash
set -euo pipefail

file="${1:?usage: scripts/ws-drill-summary.sh METRICS_FILE START RESTORED}"
start="${2:?start epoch seconds}"
restored="${3:?restored epoch seconds}"

awk -v start="$start" -v restored="$restored" '
$1 == "ts" { ts = $2; samples++; next }
/^chatim_websocket_/ {
  name = $1; value = $2 + 0
  if (!(name in first)) first[name] = value
  last[name] = value
  if (name == "chatim_websocket_users_suspended" && value > maxsusp) maxsusp = value
  if (name ~ /consumer_lag_seconds/ && ts >= start) {
    if (value > maxlag[name]) { maxlag[name] = value; maxat[name] = ts - start }
    if (ts >= restored && value >= 2) behind[name] = ts - restored
  }
  if (name == "chatim_websocket_nats_connected" && ts >= start && value == 0) {
    if (!(name in down)) down[name] = ts - start
    up = ""
  }
  if (name == "chatim_websocket_nats_connected" && ts >= start && value == 1 && (name in down) && up == "") up = ts - start
}
END {
  printf "samples %d\n", samples
  for (n in last) if (n ~ /_total|_sum$|_count$/ && last[n] != first[n]) printf "delta %s %g\n", n, last[n] - first[n]
  rc = last["chatim_websocket_reload_seconds_count"] - first["chatim_websocket_reload_seconds_count"]
  if (rc > 0) printf "reload mean %.3fs over %d users, max suspended %d\n", (last["chatim_websocket_reload_seconds_sum"] - first["chatim_websocket_reload_seconds_sum"]) / rc, rc, maxsusp
  for (n in maxlag) printf "max %s %.1fs at +%ds after the cut\n", n, maxlag[n], maxat[n]
  for (n in maxlag) printf "caught up %s %ds after restore\n", n, (n in behind) ? behind[n] + 1 : 0
  if ("chatim_websocket_nats_connected" in down) printf "nats_connected 0 from +%ds, back to 1 at +%ss\n", down["chatim_websocket_nats_connected"], up
  else print "nats_connected stayed 1"
}' "$file" | sort
