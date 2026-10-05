# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

chatim is an internal, logically multi-tenant chat platform (CPaaS) in Go. Phase 1 has two apps:
- `core`: rooms, members, messages, seq allocation, MongoDB storage, events to NATS JetStream.
- `gateway`: WebSocket via gws, realtime fanout. Not built yet.

Done on `feat/m2b` (shared branch for M2b.0 → M2b.4, one merge to `main` at the end; `dev-done`):
- M2b.0 (plan `docs/plans/2026-10-05-m2b0-mechanism-foundations.md`): ack marks only for `msg_created`, `RECONCILE_DELAY` default 5s with a boot rule (D65); actor backs off and yields the room on seq contention (D77); `access` permission hook and `view` reader pipeline on `GetHistory`; store write-contract test; detectors on `/metrics` and alert rules (D76, D78).
- M2b.1 (plan `docs/plans/2026-10-05-m2b1-effect-engine.md`): change reader on slot 0 → `CHATIM_WORK` work stream (32 partitions by slot) → effect workers on every core (D79–D81); `room_created` (fast path + worker); room activity `ls/lm/lc/ab` with `$max`; `/app resync` with a drill itest; stop budget 28s.

Done and merged to `main` (`dev-done`):
- M0–M1: foundation and PoC on dev.
- M2a (PR #5): core CreateRoom/SendMessage/GetHistory over gRPC, cid dedupe, JetStream publish, two cores in compose.
- M2a.1 (PR #8–#10): no room-wide pts, natural event ids, best-effort events, a queue-only publisher on top of the nats.go async publisher (D47–D51).
- M2a.2 + M2a.3 (PR #11): event reconciliation from the database change feed with acked marks on the dedupe Redis (D52); cross-room cid batching, ack before the cid Commit, a small warm dedupe Redis pool, acked marks in a 10ms window (D58–D60).

The system mechanisms review closed on 2026-10-05 after two rounds with two external reviewers. The result is one design, `docs/designs/261005-chatim-architecture.md`, built on a data-class framework (§4) with decisions D61–D81, and a rewritten `docs/roadmap.md`. Next is M2b.2 (edit + delete); its plan is not written yet. Old design, plans and PoC notes are in `docs/archive/` and are not updated. Project docs are written in Vietnamese.

## Hard rules

- **Never run `go` on the host.** All Go runs in `golang:1.26` through `make`. The host has Go 1.25, and nats.go v1.54 needs 1.26.
- **No comments in code.** This covers Go (including package and doc comments), tests, YAML, shell, the Makefile and `.env.example`. Names carry the meaning. Constraints that need explaining go in the design doc, plan prose or commit bodies. Generated `pkg/pb` is exempt.
- Keep code files under 200 lines, and keep `make fmt-check` clean.
- Use TDD for logic packages: `pkg/*`, `apps/*`, `tools/internal/*` and `tools/poc/internal/*`. PoC binaries in `tools/poc/<tool>` have no unit tests; run them to check them.
- Use Conventional Commits (`feat`, `fix`, `refactor`, `perf`, `test`, `docs`; never `ref`) with no AI mentions.
- Never commit `.env`, `bin/`, `rooms*.txt` or `real-texts*.txt` (the last holds real customer data). Never print values from `.env`.
- When a plan step gives a result different from its "Expected", stop and report. Don't patch it until it passes.

## Verification and review budget

The user finds long review and test loops too slow. Run the cheapest check that proves the change, and escalate only for the area that changed.

| When | Run |
|---|---|
| While coding | `make -s go ARGS="test -race -shuffle=on ./<touched pkgs>/..."` |
| Before each commit | `make fmt-check`, `make vet`, `make lint`, and the tests of touched packages |
| New goroutines, channels or locks | add `-count=5` on that package only |
| `apps/core/internal/slot` or `pkg/slotmap` changed | R5 once at the end of the task: `make go ARGS="test -race -count=20 -timeout 20m ./apps/core/internal/slot/"` (~9 min) |
| Mongo/Redis/NATS adapters or `apps/core` wiring changed | `make itest` once (needs `make infra-up`) |
| Milestone end only | `make test`, `make itest`, `make core-up && make e2e`, corebench |
| Perf milestones only | before/after sweep with `scripts/bench-cell.sh NAME RATE reset [pprof]` at 1K and 5K msg/s (results in `bin/bench/NAME/`). The dev MacBook throttles on heat around 5K and other host load skews latency: record `CPU_Speed_Limit` and corebench pacer lag, and trust only CPU and Redis command counts from a cell where either is off |

- **Risky tasks get one reviewer.** These are the write path, dedupe, publish, slots and lifecycle.
  - The reviewer checks spec and quality in a single pass over the diff.
  - It does not re-run suites the implementer already ran green; at most `-count=3` on touched packages.
- **Findings:**
  - Fix Critical and Important findings in one follow-up commit.
  - Re-review only after a Critical fix, and only that diff.
  - Write Minor findings into the plan or docs instead of starting another loop.
- **Low-risk tasks** (docs, config, compose, CLI, PoC tools) get one quick check by the controller and no reviewer subagent.
- No mutation-testing passes, repeated stress runs or benchmark re-runs unless asked.
- When task N and task N+1 touch different files, review task N while task N+1 is being implemented.
- Report in a few lines. If nothing changed since the last green run, don't re-run the whole repo at the end.

## Git workflow

Full rules in `docs/git-workflow.md`.

- Branches: short-lived `feat/<milestone>-<slug>`, `fix/<slug>`, `docs/<slug>` from `main`. No long-lived `prod`, `develop` or `release` branches.
- `main` holds finished, green work (`dev-done`), not necessarily go-live. Merge by PR with a merge commit. Never commit directly to, force-push or rewrite `main`.
- Merge only when the milestone's Definition of Done holds: plan tasks done; `fmt-check`, `vet`, `lint`, `test`, `itest`, `e2e` green; no open Critical/Important findings; roadmap, Decision Log, `INDEXES.csv` and this file updated in the same PR; the PR states the readiness level.
- CI: `.github/workflows/ci.yml` job `checks` runs `make fmt-check`, `vet`, `lint` and `test` on every PR to `main`; branch protection requires it. `itest` and `e2e` still run on the dev machine.
- Readiness: `dev-done` → `prod-like validated` → `go-live`.
- Releases are SemVer tags on `main`, one per release, not per PR. `v1.0.0` is the first go-live. Prod runs the image built from a tag; the same image goes staging → prod.
- Broken `main`: fix forward with `fix/*`; if that takes more than about a day or blocks others, revert the merge (`git revert -m 1`). A prod bug gets a patch tag; cut `release/vX.Y` from the tag only when `main` holds unready work.

## Commands

Run from the repo root. `make -s …` hides the echoed `docker run` line.

```bash
cp .env.example .env        # MONGO_ROOT_PASSWORD, REDIS_PASSWORD, REDIS_DEDUPE_PASSWORD: letters, digits, - or _ (spliced unescaped into URIs)
make infra-up               # mongo rs0 :27117, redis state :6380, redis dedupe :6381, nats :4223 (monitor :8223); infra-down / infra-reset
make redis-cli ARGS="dbsize"                  # redis-cli inside the container with the secret; INSTANCE=state (default) or dedupe
make test                   # go test -race -shuffle=on ./... (unit only, miniredis; no infra)
make go ARGS="test -race -run TestName ./apps/core/internal/actor/"   # single test
make vet; make fmt-check; make lint; make vuln; make tidy
make proto; make buf-lint   # Buf v2, proto/chatim/v1 → pkg/pb
make itest                  # all tests with CHATIM_IT_MONGO_URI/REDIS_ADDR/NATS_URL set (real infra)
make image TARGET=apps/core # distroless chatim/<name>:dev for any main package
make core-up; make core-down   # core-1 + core-2 (compose profile app), gRPC 127.0.0.1:9001/9002, admin :9090 never published
make alerts-check           # promtool check rules on deploy/prometheus/alerts.yml (in Docker)
make e2e                    # tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events
docker exec chatim-core-1 /app resync -from RFC3339 -to RFC3339 [-tenant T] [-room ID] [-rate 500] [-dry-run]   # replay a lost change feed range into the work stream
make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"   # needs core-up; other tools: mongobench, postgresbench, natsbench, wsbench
```

- `make poc` builds `bin/<tool>` and runs it on the compose network `chatim_default`. `POC_FLAGS` passes extra `docker run` flags. `MONGO_URI`, `PG_URI` and `REDIS_PASSWORD` are built from `.env`, exported by make and passed as bare `-e NAME`, so no password shows up in the echoed command.
- Inside that network, services are reached by name: `chatim-mongodb:27017`, `chatim-redis:6379`, `chatim-redis-dedupe:6379`, `chatim-nats:4222`, `chatim-core-1:9000`.
- Integration tests skip unless the `CHATIM_IT_*` env vars are set. There are no build tags.
- The `apps/core` env vars, their defaults and the ports are listed in README.md.

## Code index

`INDEXES.csv` at the repo root lists every Go package, plus the key docs, scripts and deploy files. Its columns are `path,kind,purpose,key_symbols,used_by,tests,decisions`. Read it before searching the tree.

When you add, remove or rename a package, or change its purpose or key symbols, update its row in the same commit.

## Architecture

There is one module, `github.com/ivannguyendev/chatim`, and one Dockerfile (`deploy/docker/Dockerfile`, `ARG TARGET`).
- Each app lives in `apps/<app>/` with `main.go` and `internal/`.
- `pkg/` holds shared code and no business logic.
- `tools/` holds `corecli`, `corebench` and `tools/internal/route`, a slot-routed gRPC client whose retries keep the same cid.
- Communication is one-way: gateway → core over gRPC unary, core → JetStream, gateway ← NATS.

**Key encoding (`pkg/keys`).** Big-endian uint64 fields are concatenated, so byte order matches numeric order:
- messages `_id` = `room│thread_root│seq` (24B)
- message_edits = the same plus a uint32 version (28B)
- room_events = `room│pts` (16B), unused since M2a.1 and dropped from the design

These are `_id`s of MongoDB clustered collections, so any history page, including the oldest, is one range scan. `thread_root = 0` is the main timeline, seq starts at 1, and `MaxUint64` is reserved.

**Counters.**
- `seq` is a position in a timeline.
- There is no room-wide event counter (proto `pts` fields are reserved). An event id is a natural key: `{room}-{thread}-{seq}` for a new message (`pbconv.MessageEventID`); M2b changes use the target doc's version.
- Room ids (`pkg/ids`) are random non-zero 63-bit numbers, sent as decimal strings.

**Send path (`apps/core/internal`).**
1. `grpcsrv` reads the tenant and user from the metadata keys `x-chatim-tenant` and `x-chatim-user`; callers are trusted internal apps until mTLS. Handlers return domain or `apperr` errors, and `pkg/grpcserver` maps them to gRPC codes at one boundary, so clients see sentinel text only.
2. `actor.Router` runs one goroutine per room, with a bounded mailbox and idle eviction. An actor is retired when its slot moves, and it keeps one write group in flight.
3. `dedupe` checks the cid against a RAM LRU, then the dedupe Redis `chatim:cid:{room}:{user}:{cid}` (pending `p:{core}` for 10s, committed for 15m). Actors call `dedupe.Batcher` (D58), sharded by `slot % CID_BATCH_SHARDS`: a Reserve blocks its caller, goes out at once when no round is in flight and otherwise joins the next round (one `EVALSHA`, at most `CID_BATCH_MAX_KEYS` keys); Commit and Abort only enqueue and a per-shard settle loop pipelines them. On Redis errors it falls back to the LRU only, using the `redisguard` cooldown.
4. `flush.Flusher` shards batch groups into `insertMany(ordered:false, w:majority)` every 2ms or 256 docs. A group that can't finish by its deadline is not sent (`ErrNotSent`).
5. Each insert ends as Inserted, Duplicate, Unknown or Rejected. Duplicate and Unknown are reconciled with `Find` (majority read) by (from, cid). Every retry is bounded by the cid reservation TTL, so a pending key never expires while an attempt is live.
6. After commit: ack (the LRU first), then the event to `publish.Publisher`, then the dedupe Commit (`c:{seq}:{ms}`) queued on the batcher (D59). A core dying between ack and Commit leaves the pending key until `CID_PENDING_TTL`; a fast retry after a failed write can see `PendingHere` and get `ErrRetryLater`. The publisher sends to stream `CHATIM_EVT` on subject `evt.{t}.room.{rid}.msg_created` with `Nats-Msg-Id` = the natural event id, RePublished to `live.*`. The publisher keeps only a queue per shard (sharded by slot), so Enqueue never blocks and a room's events keep arrival order; each shard calls `PublishMsgAsync` without waiting for acks. nats.go owns the in-flight limit (`PUB_MAX_PENDING`), ack timeouts (`PUB_ACK_TIMEOUT`) and no-leader retries; failures go to `WithPublishAsyncErrHandler` and are only logged, at most once per second (D50). After each `PubAck` the publisher marks the message acked on the dedupe Redis, off the ack path in batches of 256 keys or 10ms (`publish.WithAckMarks` → `eventmark`); `eventmark` sends a chunk's `PEXPIRE` at most once per TTL/4 (D60). Best-effort (D47): events lost on nack, ack timeout, too many in flight, queue full or crash are republished by the reconciler (D52).

`GetHistory` reads the store directly, not through actors.

**Storage ports.** The ports are `store.Messages`, `store.Rooms` and `store.ChangeFeed`. The adapters are:
- `mongostore`: clustered collections, created by a bootstrap step at startup.
- `memstore`: in-memory, used by unit tests.

Every adapter must pass the `storetest` contract suite, so a PostgreSQL adapter could replace Mongo if the prod-like PoC says so.

**Two Redis instances (D44, D45).**
- State, `chatim-redis`: used only by the slot manager: `chatim:core:*`, `chatim:cores`, `chatim:slot:*`, pub/sub `chatim:slots:changed`. AOF everysec, `noeviction`.
- Dedupe, `chatim-redis-dedupe`: `chatim:cid:*` and the acked-event bitmaps `chatim:evtack:{room}:{thread}:{seq>>13}` (one bit per message, TTL `EVT_ACK_MARK_TTL` 1h refreshed per chunk; a string key per message would be ~36M keys at 10K msg/s and evict cid keys). No persistence, `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`. If it dies, dedupe falls back to the LRU and the reconciler republishes unmarked messages.
- Both require AUTH. Passwords come from `.env` through compose secrets; cores read `REDIS_PASSWORD_FILE`/`REDIS_DEDUPE_PASSWORD_FILE`, tools read `-redis-password` or `REDIS_PASSWORD`. Passwords never go in argv, `docker inspect`, echoed make lines or logs.
- There is no `REDISCLI_AUTH` env in the containers: use `make redis-cli [INSTANCE=dedupe] ARGS=…`.
- Dedupe client (D60): `PoolSize` = `MaxActiveConns` = `2 × CID_BATCH_SHARDS + 4`, `MinIdleConns` = `2 × CID_BATCH_SHARDS`, `MaxRetries -1` (Reserve is not idempotent), `DialerRetries 1`. Both clients set `DisableIdentity`; the state client keeps its pool of 4 and default retries (changing it touches leases and needs R5). go-redis still drops a connection after a timeout; reusing it could read a late reply.

**Mongo credentials (D46).** Compose secret `mongo_password` comes from `MONGO_ROOT_PASSWORD`. Cores get a credential-free `MONGO_URI` plus `MONGO_USER` and `MONGO_PASSWORD_FILE` (`MONGO_AUTH_SOURCE` defaults to `admin`). A `MONGO_URI` with credentials still works (itest, non-compose runs), but not together with `MONGO_USER`. mongosh never gets `-p`: scripts authenticate inside `--eval` with `db.getSiblingDB("admin").auth(user, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim())`.

**Slots and soft ownership.** `slot = mix64(room) % 1024` (`pkg/slotmap`, with rendezvous `Score` and `Preferred`). `slot.Manager.Step` runs once per tick:
1. Heartbeat with one Lua script: `SET chatim:core:{id}`, `ZADD chatim:cores` scored by Redis `TIME`, and a prune of expired cores.
2. Renew the leases `chatim:slot:{n}`. This is a multi-key Lua script, so Redis must be a single primary, not Redis Cluster.
3. Claim or release slots toward `ceil(1024/alive)`.
4. Publish on `chatim:slots:changed`.

Hooks:
- The hooks receive batches of slots and share one `HookTimeout` per Step.
- BeforeRelease and AfterLose call `Router.EvictSlots`.
- AfterClaim calls `EvictSlots` only.

Rules:
- `Owns()` is true only while the lease stamp is newer than `min(LeaseTTL, HeartbeatTTL) − Tick`, measured from the start of the tick (D24).
- The slot manager uses its own Redis client.
- Never use `SCAN` or `KEYS`: cid keys fill the keyspace.
- Clients use `slotmap.Resolver` to map a slot to a core address.

**Invariant: any core must handle any room correctly.** Ownership only buys batching, ordering and cache hits. Correctness comes from the unique `_id` acting as CAS, so a Redis failover or a double owner never loses or duplicates a message.

**Effect engine (`reconcile` reader, `work`, `effects`; D52, D65, D66, D79–D81).**
- The reader (`apps/core/internal/reconcile`) runs only on the slot 0 owner (`Owns(0)`, no extra lease); each lead is a term that opens the feed and logs `reconcile term started`. `RECONCILE_ENABLED=false` turns only the reader off; workers always run and marks are still written.
- Source is the database commit log through `store.ChangeFeed`/`store.Cursor` (`store.Change.Kind`: `MessageInserted`, `RoomInserted`); the reader imports no driver. `mongostore.Feed` watches the database for inserts into `messages` and `rooms` and stores the position in `reconciler_state` `_id: "changes"` (conditional, `w:majority`, never moves back). Every feed adapter must pass `storetest.RunFeed`.
- The reader turns each change into a `work.Record` (keys + `CommittedAt`, 33 bytes; id `m:{room}-{thread}-{seq}` or `r:{room}`) and publishes it to `CHATIM_WORK` (WorkQueue, subject `work.p{slot % 32}`, `Nats-Msg-Id` = record id) within `RECONCILE_WINDOW`, unbounded retry. It confirms only past the prefix the work stream acked, every `RECONCILE_CONFIRM_EVERY`. It never waits, reads marks or builds events.
- Workers (`effects.Workers`) run on every core: partition n is fetched only while `Owns(n)` (durable consumer `work-p{n}`, batches of `WORK_FETCH_BATCH`). The registry runs, in order, `room_activity` (delay 0, one bulk `$max` per batch on `rooms`) and `msg_created` (delay `RECONCILE_DELAY`, checks ack marks, `Find`s unmarked messages, publishes and waits for the PubAck on the third JetStream client) for `MessageInserted`, and `room_created` (delay `RECONCILE_DELAY`, no mark) for `RoomInserted`. A record is acked only when every effect returned nil; otherwise `Nak(WORK_RETRY_DELAY)`. A missing room or corrupt doc is dropped, counted in `effect_dropped_total`.
- `CreateRoom` publishes `room_created` on the fast path too; the stream drops the worker's copy by id within `EVT_STREAM_DUPLICATES`.
- Boot rules: `RECONCILE_DELAY < EVT_STREAM_DUPLICATES` and `RECONCILE_DELAY > PUB_ACK_TIMEOUT + 10ms + 1s` always apply (D65); `WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`. The publisher marks only `msg_created`. Lost history (`ErrFeedHistoryLost`) logs an error, `Forget`s and restarts from now; recover the gap with `/app resync` (scans rooms with `ab >= hour(from)` or `ca` in range, replays the main timeline backwards into the work stream at `-rate`).
- Bootstrap anchor: `mongostore.Bootstrap` writes `reconciler_state` `{_id: "changes", at: $$CLUSTER_TIME}` only when no `at` exists, so a fresh database also forwards writes made before the first term; re-running it never moves the anchor or a confirmed token.
- Room activity (`rooms.ls/lm/lc/ab`, D69) is written only by the `room_activity` worker effect; `ab` is the hour of the last change and is indexed (`{ab: 1}`, plus `{ca: 1}` for the resync query).

**Permission hook and reader pipeline (`access`, `view`).**
- `access.Policy` is the single permission point; `access.Checker` always enforces tenant and membership first, then asks the policy (default `AllowMembers`). `SendMessage` asks it in the actor (`actor.WithPolicy`), `GetHistory` in `grpcsrv`.
- `view.Pipeline` runs steps over a page read from the store; `view.CollapseRetried` hides CD2/CD3 duplicates by (sender, cid). Hidden and deleted masks are M2b.2.
- On seq contention with another core's doc the actor pauses with `Jitter(5ms → 200ms)` between reassign rounds, then yields the room through `a.retire` (always via `requestRetire`, which holds `r.mu`) so the next command goes to the real owner (D77).

**Detectors (D76, D78).**
- Components keep atomic counters and accessors; `apps/core/internal/metrics` registers them as pull-based `CounterFunc`/`GaugeFunc` on a private Prometheus registry served at `GET /metrics` on the admin port (wiring in `apps/core/metrics_wiring.go`). All names start with `chatim_core_`.
- Alert rules are in `deploy/prometheus/alerts.yml` (15 rules); check with `make alerts-check`. A new guarantee needs a metric and a rule in the same PR.
- `reconcile_lag_seconds`, `reconcile_republished_total{effect}`, `effect_dropped_total{effect}`, `work_processed_total` and `work_failures_total` come from the workers on every core; reader metrics only on cores with `RECONCILE_ENABLED`.
- `reconcile_lag_seconds` is how far the workers run behind each effect's delay, not raw age. `mongo_oplog_window_seconds` is NaN when the oplog cannot be read (needs read access to `local`).

**Process lifecycle (`apps/core`).**
- Config is validated at boot by `apps/core/internal/config`, which applies cross-field timeout rules; components export `Validate()`.
- Start order: publisher → flusher → cid batcher → router → slot manager → workers → reader → gRPC. gRPC is served only after a clean start.
- Shutdown order: `/readyz` false → drain delay → gRPC → reader (`RECONCILE_DRAIN + 1s`) → workers (`WORK_DRAIN + 1s`) → router → cid batcher (flushes queued Commit/Abort within `2 × REDIS_OP_TIMEOUT`) → flusher → publisher (drains its queues, then waits for `PublishAsyncComplete` within `CORE_PUBLISHER_DRAIN`) → slot release → clients. All of it fits within `CORE_SHUTDOWN_BUDGET`: the default plan is 26.2s of 28s, so raising any stop phase needs a higher budget and compose `stop_grace_period` (33s).
- `/app probe` is the container healthcheck.
- `/app resync` is a one-shot subcommand: Mongo + NATS only, same config and redaction as serve.
- Every log line goes through a handler that redacts MONGO_URI and NATS_URL credentials, the Mongo password and both Redis passwords.

**Shard-readiness rules (design §5.1).** Mongo runs as a replica set without sharding, but sharding must later need configuration only:
- Large collections use a room-prefixed `_id`; the future shard key is `{_id: 1}`.
- Every query on `messages` carries the room prefix, and `messages` has no secondary index. Small fact collections (`message_edits`, `pin_actions`) may have `{room, ts}` (D70).
- No multi-document transactions, and no `$lookup` into large collections. Bounded point lookups are fine; unbounded joins are not.
- The `reactions` unique index `{k, emoji, u}` starts with `k` (D68).
- The URI, read preference and write concern come from config.
- Collections are created only in the bootstrap step.

**Testing conventions.**
- Packages with goroutines use `testing/synctest` and `goleak`.
- Unit tests use miniredis. Call `testlog.SilenceRedis()` in TestMain when dials fail on purpose.
- No short real-time polls: on the dev VM, CPU clocks skew by up to ~7ms and timers wake up to ~36ms late. Wait on explicit signals instead.

## Docs

- `docs/designs/261005-chatim-architecture.md`: the single source of truth: requirements (R17 revised), principles P1–P8, the data-class framework every plan must use (§4), data model, write path (built and planned), counter, effect engine, read path, gateway, guarantees with detectors, and the Decision Log (D1–D60 kept by id, new D61–D81). Add new decisions there.
- `docs/roadmap.md`: plan-writing rules, milestones M2b.0 → M5, dependencies, readiness.
- `docs/plans/`: per-milestone plans written with `writing-plans` before coding, executed with `subagent-driven-development` or `separate-driven-development`. M2b.0: `docs/plans/2026-10-05-m2b0-mechanism-foundations.md` (executed; results and known issues at its end). M2b.1: `docs/plans/2026-10-05-m2b1-effect-engine.md` (executed; results and known issues at its end).
- `docs/poc/README.md`: dev results (R1–R5, C1) and the prod-like measurement list P1–P10. Dev numbers only validate tools.
- `docs/research/261005-system-mechanisms-synthesis-report.md`: the comprehensive canonical research report consolidating the entire system mechanisms review.
- `docs/archive/`: the Phase 1 design (full D1–D60 rationale), the draft mechanisms review, the component diagrams, the M0–M2a.3 plans, the 8 archived review documents (`docs/archive/research/261004-*`), and the old PoC notes. Frozen.
- `docs/git-workflow.md`: branches, merge Definition of Done, readiness levels, SemVer tags and handling a broken `main`.
- `.claude/plans/*_design.md`: local (gitignored) decision logs of M2a, M2a.1/M2b draft, M2a.2 and M2a.3.
- Before writing the M3 plan, running the prod-like PoC or sizing the work stream or oplog, ask the owner for real numbers to replace the assumptions in design §2.3.
