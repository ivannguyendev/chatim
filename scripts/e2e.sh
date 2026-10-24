#!/usr/bin/env bash
set -euo pipefail

scripts="$(cd "$(dirname "$0")" && pwd)"
network="${NETWORK:-chatim_default}"
image="${CORECLI_IMAGE:-chatim/corecli:dev}"
first="${E2E_FIRST:-40}"
after="${E2E_AFTER:-40}"
victim="${E2E_KILL:-core-1}"
watcher="chatim-e2e-watch-$$"
ws_key="${WS_KEY:-$scripts/../.local/ws-keys/private.jwk}"
ws_url="${WS_URL:-ws://chatim-websocket-1:8080/ws}"
ws_container="${WS_CONTAINER:-chatim-websocket-1}"
ws2_url="${WS2_URL:-ws://chatim-websocket-2:8080/ws}"
ws2_container="${WS2_CONTAINER:-chatim-websocket-2}"
ws2_watcher="chatim-e2e-ws2-watch-$$"
gw_url="${GW_URL:-http://chatim-gateway-1:8080}"
gw_container="${GW_CONTAINER:-chatim-gateway-1}"
state="$(mktemp -d "${TMPDIR:-/tmp}/chatim-e2e.XXXXXX")"
killed=""
ws2_killed=""
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

restore_ws2() {
  if [ -n "$ws2_killed" ]; then
    echo "restore: docker start $ws2_container"
    docker start "$ws2_container" >/dev/null
    "$scripts/wait-core-healthy.sh" "$ws2_container"
    ws2_killed=""
  fi
}

finish() {
  set +e
  docker rm -f "$watcher" "$ws2_watcher" >/dev/null 2>&1
  if [ -n "$killed" ]; then
    restore || result=FAIL
  fi
  if [ -n "$ws2_killed" ]; then
    restore_ws2 || result=FAIL
  fi
  rm -rf "$state"
  if [ "$result" = PASS ]; then
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted with the first then the second emoji from GetReactionSettings and an unlisted emoji refused, seq 4 pinned, on replies, history and live; members added, promoted, removed and refused, read positions, hide and clear, the last owner leaving and coming back, on replies, history and live room, member and message subjects; a direct room opened twice into one room, replies counted and listed, the parent kept while replied, mentions of a user, a group and @all listed, a forward and a bookmark, on replies, lists and live; a system message from a non-member raising unread and one with skip_unread not, the room list ordered and counted after sends, reads, a delete and a hide, pinned first, archived apart, paged by cursor, refreshed by GetRooms and renamed live, history around a seq, messages by seq, threads, reactors, pins newest first, members owner first and a forward keeping skip_unread and meta; Mention App groups set and moved, @group notifying only its current members in the room and never the sender, an inactive group notifying and listing nobody, an edit adding then dropping a user notified then retracted and listed then gone, a delete retracting, @all notifying every other member, room groups with names and an older user group list ignored; through the websocket a dev token connecting, a send, an edit, a reaction and a bookmark, with history and bookmarks read through the gateway matching gRPC, a system message refused, an unknown key and an expired token closed 4001, a silent connection closed 4002 after the auth timeout, two users on the websocket hearing a new group at once, messages without duplicate or gap, reactions only with the room open, nothing of the other user's hides and bookmarks, one notification each for @user and @all, a read position reaching the viewer and the reader's other device but not a member without the room open, member_removed then room_closed and silence after removal, the left group listed with its version by ListMyRoomIDs and still silent after a reconnect, and no token in the websocket logs; through the gateway a group created, a member added and the group renamed with the events reaching a member with the group open on the websocket, ListMyRooms, GetHistory, ListMembers and ListBookmarks equal to gRPC, an expired token refused with 401, SendMessage unimplemented on the gateway and GetHistory unimplemented on the websocket, a CORS preflight answered for a listed origin, and no token in the gateway logs; across two websocket nodes typing reaching a member with the group open on the other node and nobody else, an outsider's typing dropped, a watched user online, offline after closing, still online across a reconnect after 3s, offline after its node was killed, and no token in the logs of $ws2_container"
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

step="phase 4 react to seq 3 and pin seq 4"
echo "phase 4: read the reaction emojis, refuse an unlisted one, react to seq 3, change the emoji, pin seq 4"
cli e2e react-pin -state /state -react 3 -pin 4
step="phase 4 check"
cli e2e check -state /state

step="phase 5 members and read positions"
echo "phase 5: direct room refusals, add e2e-bob and e2e-carol, role and priority, removal, read positions, hide and clear, the owner leaves and comes back"
cli e2e members -state /state
step="phase 5 check"
cli e2e check -state /state

step="phase 6 direct rooms, replies, mentions, forward and bookmarks"
echo "phase 6: open a direct room twice, react, reply twice, refuse deleting the parent, delete a reply, mention a user, a group and @all, forward, bookmark"
cli e2e links -state /state
step="phase 6 check"
cli e2e check -state /state

step="phase 7 room list, room settings and reads"
echo "phase 7: system messages, unread after send/read/delete/hide, pin and archive, page 2, GetRooms, UpdateRoom, AROUND, GetMessages, threads, reactors, pins, members, a forward with skip_unread and meta"
cli e2e rooms -state /state
step="phase 7 check"
cli e2e check -state /state

step="phase 8 Mention App groups, notifications and lists"
echo "phase 8: taggable and inactive groups, user groups set and moved, @group, @inactive group, an edit adding then dropping a user, a delete, @all, room groups, an older user group list"
cli e2e mentions -state /state
step="phase 8 check"
cli e2e check -state /state

step="phase 9 websocket entry point"
echo "phase 9: through $ws_container a dev token, send, edit, react, bookmark, with history, reaction settings and bookmarks read through $gw_container and equal to gRPC, kind system refused, an unknown key and an expired token closed 4001, no auth closed 4002 after the auth timeout"
if [ ! -r "$ws_key" ]; then
  echo "dev signing key missing: run make dev-keys" >&2
  exit 1
fi
docker run --rm --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e ws -state /state -url "$ws_url" -gateway "$gw_url"
step="phase 9 events through the websocket"
echo "phase 9: users A and B on $ws_container, B added to a group and hearing msg_created, reaction_changed only after open_room, none of A's hides and bookmarks, @B and @all once each, member_removed then room_closed and silence, the group in B's left rooms from ListMyRoomIDs, B reconnecting and hearing nothing of it, no duplicate or gap"
docker run --rm --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e ws-events -state /state -url "$ws_url" -gateway "$gw_url"
step="phase 9 websocket logs hold no token"
for list in ws-tokens ws-events-tokens; do
  if [ ! -s "$state/$list" ]; then
    echo "phase 9 wrote no $list" >&2
    exit 1
  fi
done
cat "$state/ws-tokens" "$state/ws-events-tokens" >"$state/ws-all-tokens"
docker logs "$ws_container" >"$state/ws.log" 2>&1
if grep -F -q -f "$state/ws-all-tokens" "$state/ws.log"; then
  echo "the logs of $ws_container hold a token used by phase 9" >&2
  exit 1
fi
echo "phase 9: the logs of $ws_container hold none of the $(wc -l <"$state/ws-all-tokens" | tr -d ' ') tokens used"
step="phase 9 check"
cli e2e check -state /state
step="phase 10 gateway entry point"
echo "phase 10: through $gw_container a group created, a member added and the group renamed with the events reaching a member on $ws_container, ListMyRooms, GetHistory, ListMembers and ListBookmarks equal to gRPC, an expired token 401, SendMessage unimplemented on the gateway, GetHistory unimplemented on the websocket, a CORS preflight"
docker run --rm --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e gateway -state /state -url "$ws_url" -gateway "$gw_url"
step="phase 10 gateway logs hold no token"
if [ ! -s "$state/gw-tokens" ]; then
  echo "phase 10 wrote no gw-tokens" >&2
  exit 1
fi
cat "$state/ws-tokens" "$state/ws-events-tokens" "$state/gw-tokens" >"$state/gw-all-tokens"
docker logs "$gw_container" >"$state/gw.log" 2>&1
if grep -F -q -f "$state/gw-all-tokens" "$state/gw.log"; then
  echo "the logs of $gw_container hold a token used by phases 9 and 10" >&2
  exit 1
fi
echo "phase 10: the logs of $gw_container hold none of the $(wc -l <"$state/gw-all-tokens" | tr -d ' ') tokens used"
step="phase 10 check"
cli e2e check -state /state
step="phase 11 typing and presence across two websocket nodes"
echo "phase 11: A on $ws_container, B, C and A's second device on $ws2_container, typing to B with the group open and not to C or A's device, an outsider's typing dropped, B watching A online and a closed D offline, A reconnecting after 3s without going offline"
docker run --rm --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e ws-ephemeral -state /state -url "$ws_url" -url2 "$ws2_url" -gateway "$gw_url"
step="phase 11 start the watcher of a user on $ws2_container"
docker run --rm --name "$ws2_watcher" --network "$network" --user "$(id -u):$(id -g)" -e REDIS_PASSWORD -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
  e2e ws-ephemeral-kill -state /state -url "$ws_url" -url2 "$ws2_url" -gateway "$gw_url" >"$state/ws2-kill.log" 2>&1 &
ws2_pid=$!
for _ in $(seq 1 60); do
  if grep -w watching "$state/ws2-kill.log" >/dev/null 2>&1 || ! kill -0 "$ws2_pid" 2>/dev/null; then
    break
  fi
  sleep 1
done
if ! grep -w watching "$state/ws2-kill.log"; then
  cat "$state/ws2-kill.log" >&2
  echo "the watcher of $ws2_container never saw its user online" >&2
  exit 1
fi
step="phase 11 kill $ws2_container"
echo "crash: docker kill -s KILL $ws2_container"
docker kill -s KILL "$ws2_container" >/dev/null
ws2_killed=yes
step="phase 11 the user of $ws2_container goes offline"
if ! wait "$ws2_pid"; then
  cat "$state/ws2-kill.log" >&2
  exit 1
fi
cat "$state/ws2-kill.log"
step="phase 11 restart $ws2_container"
restore_ws2
step="phase 11 websocket-2 logs hold no token"
for list in ws-ephemeral-tokens ws-ephemeral-kill-tokens; do
  if [ ! -s "$state/$list" ]; then
    echo "phase 11 wrote no $list" >&2
    exit 1
  fi
done
cat "$state/ws-ephemeral-tokens" "$state/ws-ephemeral-kill-tokens" >"$state/ws2-all-tokens"
docker logs "$ws2_container" >"$state/ws2.log" 2>&1
if grep -F -q -f "$state/ws2-all-tokens" "$state/ws2.log"; then
  echo "the logs of $ws2_container hold a token used by phase 11" >&2
  exit 1
fi
echo "phase 11: the logs of $ws2_container hold none of the $(wc -l <"$state/ws2-all-tokens" | tr -d ' ') tokens used"
step="phase 11 check"
cli e2e check -state /state
result=PASS
