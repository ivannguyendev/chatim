#!/usr/bin/env bash

phase16_media() {
  step="phase 16 media buckets"
  "$scripts/media-buckets.sh" >/dev/null
  step="phase 16 media upload, attachments and gallery through the gateway and websocket"
  echo "phase 16: through $gw_container GetClientConfig with media on, a 40 MiB file uploaded in 16, 16 and 8 MiB parts straight to S3 and completed with client ETags, completed again, a stale part URL refused, another user's not_found, the device limit on GetPartURLs, too many parts invalid, no part URL after AbortUpload, then a PNG and an HTML page sent with no text over $ws_container, read by B with both attachments and their mimes in the room list preview, the PNG and the HTML downloaded from plain signed URLs with their exact bytes and hashes, ListUploads newest first and per user, no URL after a hide or a delete, and C downloading through a forward"
  docker run --rm --network "$network" --user "$(id -u):$(id -g)" -v "$state":/state -v "$ws_key":/keys/private.jwk:ro "$image" \
    e2e media -state /state -url "$ws_url" -gateway "$gw_url"
  step="phase 16 logs hold no token and no signed URL"
  if [ ! -s "$state/media-tokens" ]; then
    echo "phase 16 wrote no media-tokens" >&2
    exit 1
  fi
  docker logs "$gw_container" >"$state/gw16.log" 2>&1
  if grep -F -q -f "$state/media-tokens" "$state/gw16.log"; then
    echo "the logs of $gw_container hold a token used by phase 16" >&2
    exit 1
  fi
  for node in chatim-chat-1 chatim-chat-2 "$gw_container"; do
    if docker logs "$node" 2>&1 | grep -q "X-Amz-Signature"; then
      echo "the logs of $node hold a signed S3 URL" >&2
      exit 1
    fi
  done
  echo "phase 16: the logs of $gw_container hold none of the $(wc -l <"$state/media-tokens" | tr -d ' ') tokens used and no node logged a signed URL"
}
