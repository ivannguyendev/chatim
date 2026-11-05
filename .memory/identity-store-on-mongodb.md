---
name: identity-store-on-mongodb
description: "Owner 2026-11-02 - identity moved its store from Postgres to MongoDB in phase PM (built 2026-11-04, D245-D259) between PA and PB; reverses D210 because the identity scope keeps only a ~100K-doc mirror and go-live should not run Postgres HA for it"
metadata:
  type: project
---

After the T13 exchange bench of M6 PA, the owner was asked whether production already has Postgres HA (managed or operated). Answer 2026-11-02: "chuyển sang mongodb", and "không" to fixing the Postgres feed bug inside PA.

**Why:** D210 chose Postgres for relational permission data (account ↔ group tree ↔ roles, recursive queries, refresh-token rotation transactions). PA removed roles, sessions and refresh tokens ([[chat-mirrors-users-tenant-owns-identity]]), leaving a user/group mirror, trusted issuers and keys. Postgres HA, backup and alerts were never built, identity needed its own Postgres server (cluster-wide `xmin` stalls the feed), and the bench hit two Postgres-only problems: commits carrying `NOTIFY` serialize on one lock (~560 new users/s per node), and the prepared feed query pins a generic plan that seq-scans `directory_changes`. Chat already runs a MongoDB replica set.

**Owner words during PM (all 2026-11-02 unless dated):**
- "cắt đứt postgres, có thể giữ postgresbench, không giữ service / logic app", then "bỏ container thì bỏ luôn tools/poc/postgresbench" → every Postgres service, make target, env key, app code and pgx left the repo (D253); 2026-11-03 "ok" to delete the dev volume `chatim_postgres_data` (deleted).
- "bỏ Feed HTTP" → the long-poll directory feed (D227) had no consumer and was removed (D257); rebuild it for the first consuming app (Mention 3b, M8 or M9) as plain keep-alive requests ([[http-between-services-plain-keepalive]]).
- "audit_events được ghi cùng với change stream được không" → audit dropped in PM, back in PB derived from the change stream (D256). Transactions only in 5 multi-doc commands (D250).
- "xong PM1 thì tháo luôn được không?" → one phase PM, checked once at the end.
- 2026-11-03: drop the real-286 itest (dev oplog never rolls over); check it on prod-like (roadmap "Mục mang sang M5").
- 2026-11-04 "ok" to batch just-in-time inserts after a cold run reached 849/s (D259).

**Status (2026-11-04):** built, `dev-done` on `feat/m6` (commits `f23a58c` → `77abf88`; summary `docs/plans/2026-11-02-m6-pm-identity-mongodb-summary.md`). Database `chatim_identity`, user `identity` with `readWrite` only, created by compose `identity-mongo-init`; replicas `identity-1` and `identity-2`. Each replica loads the directory in pages under a `slots` lease and follows a change stream (D245, D246, D252); schema checked like chat with `migrate` dry-run/`-apply` (D258). D9 (chat database) stays open until prod-like.

**How to apply:** PB writes directly on `mongostore`; never reintroduce Postgres or a held HTTP request for identity; identity never reads chat collections ([[no-shared-tables-across-services]]). Identity data is mostly re-derivable (JIT recreates users, tenants re-push groups), so store changes need no data migration beyond issuers and keys.
