---
name: identity-store-on-mongodb
description: "Owner 2026-11-02 - identity moves its store from Postgres to MongoDB in its own phase (PM) after PA and before PB; reverses D210 because the new identity scope keeps only a ~100K-row mirror and go-live should not run Postgres HA for it"
metadata:
  type: project
---

After the T13 exchange bench of M6 PA, the owner was asked whether production already has Postgres HA (managed or operated). Answer 2026-11-02: "chuyển sang mongodb", and "không" to fixing the Postgres feed bug inside PA.

**Why:** D210 chose Postgres for relational permission data (account ↔ group tree ↔ roles, recursive queries, refresh-token rotation transactions). PA removed roles, sessions and refresh tokens ([[chat-mirrors-users-tenant-owns-identity]]), leaving a user/group mirror, trusted issuers and keys. Postgres HA, backup and alerts were never built, identity needs its own Postgres server (cluster-wide `xmin` stalls the feed), and the bench hit two Postgres-only problems: commits carrying `NOTIFY` serialize on one lock (~560 new users/s per node), and the prepared feed query pins a generic plan that seq-scans `directory_changes`. Core already runs a MongoDB replica set.

**How to apply:**
- Order is PA (Postgres, as built) → PM (store on MongoDB) → PB (batch server API written directly on MongoDB).
- PM runs the full workflow; the directory feed (D227–D231 relied on `xid`, `pg_snapshot_xmin`, `LISTEN/NOTIFY`, advisory locks, `FOR UPDATE`) needs a new design on MongoDB first.
- Identity gets its own database and user on the replica set and never reads core collections ([[no-shared-tables-across-services]]).
- Identity data is mostly re-derivable (JIT recreates users, tenants re-push groups), so the move needs no data migration beyond issuers and keys; dev Postgres data can be dropped.
- Owner 2026-11-02 after the PM split: "cắt đứt postgres, có thể giữ postgresbench, không giữ service / logic app". PM2 removes every Postgres service, make target, env key and app code; the owner then added "bỏ container thì bỏ luôn tools/poc/postgresbench", so the bench tool was removed the same day and pgx leaves `go.mod` in PM2. D9 (core database) stays open until prod-like; the comparison tool lives only in git history.
- Owner 2026-11-02 in the PM summary review: "bỏ Feed HTTP". The long-poll directory feed for other apps (D227) had no consumer, so PM removes it; rebuild it when the first consuming app (Mention 3b, M8 or M9) needs one, designed for that app. Audit (`audit_events`, actor always `admin-cli`, never read) is also dropped in PM and comes back in PB derived from the change stream. Transactions only where several docs must change together (5 commands).
