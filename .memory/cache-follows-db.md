---
name: cache-follows-db
description: "Owner 2026-10-31 (M6 part 3a) - a server cache changes as soon as the database does (change signal in the same transaction, never TTL); design server-side for load, client caching is not our concern; every in-memory warm-up must survive frequent K8s restarts without a storm"
metadata:
  type: feedback
---

In the M6 part 3a brainstorm I proposed a 1-hour client cache for profiles (`GetUsers`). Once the owner understood that a rename would then show on other devices up to an hour late, they asked "khi thay đổi thì đảm bảo cache cũng đổi tương ứng". I then proposed a websocket push of profile changes; the owner answered "không cần gửi ws, trước mắt chỉ cần đảm bảo db thay đổi thì cache cũng được cập nhật ngay".

**Why:** a cache that lags the database by a timer shows stale data the owner considers wrong, not merely slow.

Then: "ko cần bận tâm client cache hay không cache, chỉ cần đảm bảo khả năng chịu tải và cách tốt nhất cho GetUsers nói riêng và iam nói chung". After comparing direct Postgres reads, LRU, Redis and a full in-memory copy, the owner asked how frequent restarts behave ("IAM khởi động thì thường xuyên thì thế nào?", "phải thêm cơ chế tránh bão khởi động, K8s sẽ restart liên tục") and approved the full in-memory copy per replica, updated by Postgres `LISTEN/NOTIFY` + a 2s recheck, with restart-storm guards (liveness stays OK while loading, no exit on load failure with jittered backoff, at most 4 replicas loading at once, paged load). The owner did not know `NOTIFY` and asked "sao có thêm cơ chế gì nữa?": explain any database feature as a plain story and show it is the same mechanism reused, not a new one.

**How to apply:** design the server for load; client caching belongs to the client. Any server cache is updated by a change signal written in the same transaction as the data (Postgres `NOTIFY`, a `_state` event), never only by expiry. Any cache warmed at boot must keep `/livez` OK while loading, never exit on a load failure, cap concurrent warm-ups and load in pages. Websocket push to clients is a later option, not a default. Related: [[owner-communication]], [[design-principles]].
