# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

chatim is an internal, logically multi-tenant chat platform (CPaaS) in Go. Phase 1 has two apps:
- `core`: rooms, members, messages, seq allocation, MongoDB storage, events to NATS JetStream.
- `gateway`: WebSocket via gws, realtime fanout. Not built yet.

Done and merged to `main` in PR #12 on 2026-10-08 (shared branch `feat/m2b`, `dev-done`):
- M2b.0 (summary `docs/plans/2026-10-05-m2b0-mechanism-foundations-summary.md`): ack marks only for `msg_created`, `RECONCILE_DELAY` default 5s with a boot rule (D65); actor backs off and yields the room on seq contention (D77); `access` permission hook and `view` reader pipeline on `GetHistory`; store write-contract test; detectors on `/metrics` and alert rules (D76, D78).
- M2b.1 (summary `docs/plans/2026-10-05-m2b1-effect-engine-summary.md`): change reader on slot 0 → `CHATIM_WORK` work stream (32 partitions by slot) → effect workers on every core (D79–D81); `room_created` (fast path + worker); room activity `ls/lm/lc/ab` with `$max`; `/app resync` with a drill itest; stop budget 28s.
- M2b.2 (summary `docs/plans/2026-10-05-m2b2-edit-delete-summary.md`): edit and delete as immutable facts in `message_edits` plus the `messages` projection (`v/d/ea`), `base_ver` CAS, ack after the projection (D62–D64, D82); hide (`hidden`) and clear history applied per reader in `view` (D85; events and time-based `cleared_at` since M2b.4); `GetEditHistory`; worker effects `edit_projection` and `msg_changed` on `EditInserted` (D83, D84); resync scans `message_edits`; corecli/e2e edit and delete; edit/delete rights only through `access.Policy`, default `access.DefaultPolicy` (author only, D86) with message kinds locked by config `MESSAGE_LOCKED_KINDS` (none by default, D87).
- M2b.3 (summary `docs/plans/2026-10-06-m2b3-reactions-pins-summary.md`): one reaction emoji per (user, message) in clustered `reactions` (`_id` = message key + user, one upsert on `_id` that leaves a same-emoji doc unchanged, tombstone on remove, no cid; D88, D89); a fixed emoji list `REACTION_EMOJIS` served by `GetReactionSettings` (D95); per-emoji counts in `messages.rx` by witness-checked recount + CAS on `rx.v`, touched inline and by the `reaction_counter` worker (D90); pins as dense `pin_actions` facts with a fold + CAS projection on `rooms.pins/pin_ver`, no base version, exact `PIN_LIMIT` (D92); feed update/replace of `reactions`, work record user tail, unknown kinds Nak'd, `room_activity` keeps `Seq` 0 for non-message kinds (D91); events `reaction_changed`, `counts_changed`, `msg_pinned`, `msg_unpinned` (D93); member-level actions (D94); resync scans `reactions` and `pin_actions`; corecli/e2e react and pin.
- M2b.4 (summary `docs/plans/2026-10-06-m2b4-members-read-summary.md`): full English DB field names except `messages`, proto `version` → `ver` (D96); clear history by time `members.cleared_at` and `message_edits` rows per content version with a `ver 0` original (D96, D97); members as a set class in clustered `members` (`_id` = `keys.Member`, own `ver`, tombstone `state = 2`, `priority`; D98); `AddMembers` with `request_id` dedupe (D99); owner changes in one MongoDB transaction bumping `rooms.owners_ver`, no internal retries (D100, D101); `member_count` by `$inc` + survival timer (D102, D111); read position `read_seq/read_ver` (D105); feed kinds 6–9 and timer kind 10 with six new worker effects (D103, D104, D109); subjects by data kind `room`/`member`/`message` (D108); `memberwatch` forgets cached members across cores (D106, D110); `MEMBER_BATCH_MAX` (D107); resync scans `members` and `hidden`; `/app recount`; corecli/e2e member phase 5.

Done and merged to `main` (`dev-done`):
- M0–M1: foundation and PoC on dev.
- M2a (PR #5): core CreateRoom/SendMessage/GetHistory over gRPC, cid dedupe, JetStream publish, two cores in compose.
- M2a.1 (PR #8–#10): no room-wide pts, natural event ids, best-effort events, a queue-only publisher on top of the nats.go async publisher (D47–D51).
- M2a.2 + M2a.3 (PR #11): event reconciliation from the database change feed with acked marks on the dedupe Redis (D52); cross-room cid batching, ack before the cid Commit, a small warm dedupe Redis pool, acked marks in a 10ms window (D58–D60).

The system mechanisms review closed on 2026-10-05 after two rounds with two external reviewers. The result is one design, `docs/designs/261005-chatim-architecture.md`, built on a data-class framework (§4) with decisions D61–D111, and a rewritten `docs/roadmap.md`. M2b.0–M2b.4 are merged to `main` (PR #12); next is M2c (thread, mention, reply/forward, bookmark) on the shared branch `feat/m2c`. Old design, plans and PoC notes are in `docs/archive/` and are not updated. Project docs are written in Vietnamese.

## Hard rules

- **Never run `go` on the host.** All Go runs in `golang:1.26` through `make`. The host has Go 1.25, and nats.go v1.54 needs 1.26.
- **No comments in code.** This covers Go (including package and doc comments), tests, YAML, shell, the Makefile and `.env.example`. Names carry the meaning. Constraints that need explaining go in the design doc, plan prose or commit bodies. Generated `pkg/pb` is exempt.
- Keep code files under 200 lines, and keep `make fmt-check` clean.
- Use TDD for logic packages: `pkg/*`, `apps/*`, `tools/internal/*` and `tools/poc/internal/*`. PoC binaries in `tools/poc/<tool>` have no unit tests; run them to check them.
- Use Conventional Commits (`feat`, `fix`, `refactor`, `perf`, `test`, `docs`; never `ref`) with no AI mentions.
- Never commit `.env`, `bin/`, `rooms*.txt` or `real-texts*.txt` (the last holds real customer data). Never print values from `.env`.
- When a plan step gives a result different from its "Expected", stop and report. Don't patch it until it passes.

## Design checklist

Check every brainstorm, summary and plan against this list before showing it to the owner.
- Map each feature to a data class of design §4 (one write, event and repair path per class). Never design a per-feature patch; a feature that fits no class needs a new class first.
- A derived value written inline (counter, projection, summary) is a direct write or `$inc` wrapped in the survival timer (design §7.1, D111); repair = recount + CAS on the version. Never recount on every change.
- No internal retries: a CAS, duplicate-key or transaction conflict returns `ErrRetryLater` (`UNAVAILABLE`) at once; workers Nak; transactions run one attempt.
- No data limit without a use case or an existing requirement (A6). A page size is pagination, not a limit.
- Every change publishes an event with a worker republish path; subjects follow the data kind (`room`/`member`/`message`); core never picks recipients and never expands mention groups.
- Shard-ready: room-prefixed clustered `_id`; no secondary index on `messages`; uniqueness only through `_id` (claim docs such as `room_dms`), never a unique secondary index; no transaction except D100.
- Field names: full basic English words except `messages`; counters end in `_ver`; `updated_at`/`updated_by`; `request_id`.
- Owner summaries: Vietnamese, plain words with examples, written from the final state (rewrite after a review round, never append patches). Risks list only real consequences of this plan, not other systems' operations and not mechanisms already accepted.
- Ask the owner only pivotal questions, in plain words with an example; never re-ask an answered one.

## Verification and review budget

The user finds long review and test loops too slow. Run the cheapest check that proves the change, and escalate only for the area that changed.

| When | Run |
|---|---|
| While coding | `make -s go ARGS="test -race -shuffle=on ./<touched pkgs>/..."` |
| Before each commit | `make fmt-check`, `make vet`, `make lint`, and the tests of touched packages |
| New goroutines, channels or locks | add `-count=5` on that package only |
| `apps/core/internal/platform/slot` or `pkg/slotmap` changed | R5 once at the end of the task: `make go ARGS="test -race -count=20 -timeout 20m ./apps/core/internal/platform/slot/"` (~9 min) |
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

- Branches (standard, owner 2026-10-08; do not ask again): one shared branch per milestone, `feat/<milestone>` (e.g. `feat/m2c`) from `main`. Every part of the milestone is committed and pushed there, with no PR per part. When the whole milestone meets the Definition of Done, draft the PR description in `bin/<branch>-pr.md` and the owner opens and merges one PR. `fix/<slug>` for stray fixes on `main`, `docs/<slug>` for docs outside a milestone. No long-lived `prod`, `develop` or `release` branches.
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
make go ARGS="test -race -run TestName ./apps/core/internal/send/actor/"   # single test
make vet; make fmt-check; make lint; make vuln; make tidy
make proto; make buf-lint   # Buf v2, proto/chatim/v1 → pkg/pb
make itest                  # all tests with CHATIM_IT_MONGO_URI/REDIS_ADDR/NATS_URL set (real infra)
make image TARGET=apps/core # distroless chatim/<name>:dev for any main package
make core-up; make core-down   # core-1 + core-2 (compose profile app), gRPC 127.0.0.1:9001/9002, admin :9090 never published
make alerts-check           # promtool check rules on deploy/prometheus/alerts.yml (in Docker)
make e2e                    # tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events, then edit seq 1 and delete seq 2, then react to seq 3 and pin seq 4, then the member phase (DM refusals, add, role, priority, removal, read positions, hide and clear, last owner leaves and rejoins)
docker exec chatim-core-1 /app resync -from RFC3339 -to RFC3339 [-tenant T] [-room ID] [-rate 500] [-dry-run]   # replay a lost change feed range into the work stream
docker exec chatim-core-1 /app recount -room ID [-dry-run]   # recount member_count of one room (CAS on member_count_ver, then member_count_changed)
make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"   # needs core-up; other tools: mongobench, postgresbench, natsbench, wsbench
```

- `make poc` builds `bin/<tool>` and runs it on the compose network `chatim_default`. `POC_FLAGS` passes extra `docker run` flags. `MONGO_URI`, `PG_URI` and `REDIS_PASSWORD` are built from `.env`, exported by make and passed as bare `-e NAME`, so no password shows up in the echoed command.
- Inside that network, services are reached by name: `chatim-mongodb:27017`, `chatim-redis:6379`, `chatim-redis-dedupe:6379`, `chatim-nats:4222`, `chatim-core-1:9000`.
- Integration tests skip unless the `CHATIM_IT_*` env vars are set. There are no build tags. Unit tests sit next to their code; only whole-core integration tests live apart, in `apps/core/itest`.
- The `apps/core` env vars, their defaults and the ports are listed in README.md.

## Code index

`INDEXES.csv` at the repo root lists every Go package, plus the key docs, scripts and deploy files. Its columns are `path,kind,purpose,key_symbols,used_by,tests,decisions`. Read it before searching the tree.

When you add, remove or rename a package, or change its purpose or key symbols, update its row in the same commit.

## Architecture

There is one module, `github.com/ivannguyendev/chatim`, and one Dockerfile (`deploy/docker/Dockerfile`, `ARG TARGET`).
- Each app lives in `apps/<app>/` with `main.go` and `internal/`.
- `apps/core/main.go` only calls `app.Main`; wiring lives in `apps/core/internal/app`, whole-core integration tests in `apps/core/itest` (they start cores with `app.Run`). Packages are grouped by flow (design §13, names follow design §6.2/§6.3): `api/` (grpcsrv, view), `model/` (domain, pbconv, access), `send/` (actor, dedupe, flush, memberwatch), `change/` (mutate, ownership, counter, pinproj), `event/` (publish, eventmark, work, effects, reconcile, resync), `store/`, `platform/` (slot, metrics, redisguard, testlog), plus `config/`. Package names stay short (`actor`, not `sendactor`); a new package goes into the group of its flow.
- `pkg/` holds shared code and no business logic.
- `tools/` holds `corecli`, `corebench` and `tools/internal/route`, a slot-routed gRPC client whose retries keep the same cid.
- Communication is one-way: gateway → core over gRPC unary, core → JetStream, gateway ← NATS.

**Key encoding (`pkg/keys`).** Big-endian uint64 fields are concatenated, so byte order matches numeric order:
- messages `_id` = `room│thread_root│seq` (24B)
- message_edits = the same plus a uint32 version (28B)
- reactions = the message key plus the user bytes (24B + 1..64B), so one message's reactions are one range
- pin_actions = `room│pin_ver` (16B)
- members = `keys.Member(room, user)`: room 8B plus the user bytes (1..64B). Mongo compares BinData by length first, so never range-scan `members` by `_id`; query by room through the `room_id` index, by user with `_id` equality or `$in`
- room_events = `room│pts` (16B), unused since M2a.1 and dropped from the design

These are `_id`s of MongoDB clustered collections (`members` too), so any history page, including the oldest, is one range scan. `thread_root = 0` is the main timeline, seq starts at 1, and `MaxUint64` is reserved.

**Counters.**
- `seq` is a position in a timeline.
- There is no room-wide event counter (proto `pts` fields are reserved). An event id is a natural key: `{room}-{thread}-{seq}` for a new message (`pbconv.MessageEventID`); M2b changes use the target doc's version.
- Room ids (`pkg/ids`) are random non-zero 63-bit numbers, sent as decimal strings. `CreateRoom` draws a new id up to 3 times on a taken one (`createAttempts`); the id scheme and this retry are under review for M2c (roadmap R4).
- `pin_ver` is a room's dense pin version; `rx.v` versions a message's reaction summary and moves only when a count changes; a reaction doc's `ver` counts its changes (event id token `-n{ver}`).
- Members: `ver` (≤ MaxUint32) counts membership changes of one (room, user) doc; `read_ver` counts read position changes; `rooms.member_count_ver` counts `member_count` changes (1 at creation); `rooms.owners_ver` is bumped by every owner transaction. All only grow, with gaps; tombstones are never deleted, so event ids never repeat.

**Send path (`apps/core/internal`).**
1. `grpcsrv` reads the tenant and user from the metadata keys `x-chatim-tenant` and `x-chatim-user`; callers are trusted internal apps until mTLS. Handlers return domain or `apperr` errors, and `pkg/grpcserver` maps them to gRPC codes at one boundary, so clients see sentinel text only.
2. `actor.Router` runs one goroutine per room, with a bounded mailbox and idle eviction. An actor is retired when its slot moves, and it keeps one write group in flight.
3. `dedupe` checks the cid against a RAM LRU, then the dedupe Redis `chatim:cid:{room}:{user}:{cid}` (pending `p:{core}` for 10s, committed for 15m). Actors call `dedupe.Batcher` (D58), sharded by `slot % CID_BATCH_SHARDS`: a Reserve blocks its caller, goes out at once when no round is in flight and otherwise joins the next round (one `EVALSHA`, at most `CID_BATCH_MAX_KEYS` keys); Commit and Abort only enqueue and a per-shard settle loop pipelines them. On Redis errors it falls back to the LRU only, using the `redisguard` cooldown.
4. `flush.Flusher` shards batch groups into `insertMany(ordered:false, w:majority)` every 2ms or 256 docs. A group that can't finish by its deadline is not sent (`ErrNotSent`).
5. Each insert ends as Inserted, Duplicate, Unknown or Rejected. Duplicate and Unknown are reconciled with `Find` (majority read) by (from, cid). Every retry is bounded by the cid reservation TTL, so a pending key never expires while an attempt is live.
6. After commit: ack (the LRU first), then the event to `publish.Publisher`, then the dedupe Commit (`c:{seq}:{ms}`) queued on the batcher (D59). A core dying between ack and Commit leaves the pending key until `CID_PENDING_TTL`; a fast retry after a failed write can see `PendingHere` and get `ErrRetryLater`. The publisher sends to stream `CHATIM_EVT` on subject `evt.{t}.message.{rid}.msg_created` with `Nats-Msg-Id` = the natural event id. Subjects go by data kind (D108, `publish.subjectFor`): `evt.{t}.room.{rid}.{kind}` (`room_created`, `msg_pinned`/`msg_unpinned`, `member_count_changed`), `evt.{t}.member.{rid}.{kind}` (`member_*`, `read_updated`, `message_hidden`, `history_cleared`), `evt.{t}.message.{rid}.{kind}` (`msg_*`, `reaction_changed`, `counts_changed`); an unknown kind is not published. One RePublish rule `evt.*.*.*.*` → `live.{t}.{kind of data}.{rid}.evt.{kind}`; everything of a room is `live.{t}.*.{rid}.>`. Core has no user subject and never picks recipients; a later distribution app does. The publisher keeps only a queue per shard (sharded by slot), so Enqueue never blocks and a room's events keep arrival order; each shard calls `PublishMsgAsync` without waiting for acks. nats.go owns the in-flight limit (`PUB_MAX_PENDING`), ack timeouts (`PUB_ACK_TIMEOUT`) and no-leader retries; failures go to `WithPublishAsyncErrHandler` and are only logged, at most once per second (D50). After each `PubAck` the publisher marks the message acked on the dedupe Redis, off the ack path in batches of 256 keys or 10ms (`publish.WithAckMarks` → `eventmark`); `eventmark` sends a chunk's `PEXPIRE` at most once per TTL/4 (D60). Best-effort (D47): events lost on nack, ack timeout, too many in flight, queue full or crash are republished by the reconciler (D52).

`GetHistory` reads the store directly, not through actors.

**Storage ports.** The ports are `store.Messages`, `store.Rooms`, `store.MessageEditor` (`ApplyEdit`), `store.HistoryClearer` (`ClearHistory`), `store.Edits`, `store.Hidden`, `store.Reactions`, `store.ReactionSummaries` (`SetReactions`), `store.Pins`, `store.PinProjector` (`PinState`, `ApplyPins`), the member ports `store.MemberWriter` (`AddMembers`, `ApplyMember`), `store.MemberReader` (`MembersOf`, `MembersBetween`), `store.OwnerChanges` (`ChangeOwners`, the one transaction), `store.MemberCounts` (`AddMemberCount`, `CountMembers`, `SetMemberCount`), `store.ReadPositions` (`MarkRead`, `MarkUnread`), and `store.ChangeFeed`. `store.Hidden` has `Hide`, `HiddenIn`, `Get`, `Between`. The adapters are:
- `mongostore`: clustered collections, created by a bootstrap step at startup.
- `memstore`: in-memory, used by unit tests.

Every adapter must pass the `storetest` contract suite, so a PostgreSQL adapter could replace Mongo if the prod-like PoC says so.

**Two Redis instances (D44, D45).**
- State, `chatim-redis`: used only by the slot manager: `chatim:core:*`, `chatim:cores`, `chatim:slot:*`, pub/sub `chatim:slots:changed`. AOF everysec, `noeviction`.
- Dedupe, `chatim-redis-dedupe`: `chatim:cid:*`, `chatim:req:{room}:{user}:{request_id}` (`AddMembers` retries, same Lua and batcher, D99) and the acked-event bitmaps `chatim:evtack:{room}:{thread}:{seq>>13}` (one bit per message, TTL `EVT_ACK_MARK_TTL` 1h refreshed per chunk; a string key per message would be ~36M keys at 10K msg/s and evict cid keys). No persistence, `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`. If it dies, dedupe falls back to the LRU and the reconciler republishes unmarked messages.
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

**Effect engine (`reconcile` reader, `work`, `effects`; D52, D65, D66, D79–D81, D83, D84, D91, D103, D109, D111).**
- The reader (`apps/core/internal/event/reconcile`) runs only on the slot 0 owner (`Owns(0)`, no extra lease); each lead is a term that opens the feed and logs `reconcile term started`. `RECONCILE_ENABLED=false` turns only the reader off; workers always run and marks are still written.
- Source is the database commit log through `store.ChangeFeed`/`store.Cursor` (`store.Change.Kind` 1–9: `MessageInserted`, `RoomInserted`, `EditInserted`, `ReactionChanged`, `PinInserted`, `MemberChanged`, `ReadChanged`, `MessageHidden`, `HistoryCleared`; kind 10 `MemberCountCheck` comes only from timers); the reader imports no driver. `mongostore.Feed` watches the database for inserts into `messages`, `rooms`, `message_edits`, `reactions`, `pin_actions`, `members` and `hidden`; updates/replaces of `reactions` (an update decodes `documentKey._id` + `updatedFields.ver`; no `updateLookup`); replaces of `members` and updates of `members` with `updatedFields.ver` (`MemberChanged`), `read_ver` without `ver` (`ReadChanged`) or only `cleared_at` (`HistoryCleared`). Updates of `rooms` (`$inc member_count`, `owners_ver`, activity) never enter the feed. The position is in `reconciler_state` `_id: "changes"` `{resume_token, cluster_time}` (conditional, `w:majority`, never moves back). Every feed adapter must pass `storetest.RunFeed`.
- The reader turns each change into a `work.Record` (keys + version + `CommittedAt`, 37 bytes, plus a `len + user` tail for `ReactionChanged`, `MemberChanged`, `ReadChanged`, `MessageHidden`, `HistoryCleared`; id `m:{room}-{thread}-{seq}`, `r:{room}`, `e:{room}-{thread}-{seq}-v{ver}`, `x:{room}-{thread}-{seq}-{user}-n{ver}`, `p:{room}-p{pin_ver}`, `g:{room}-mb-{user}-v{ver}`, `d:{room}-rd-{user}-v{read_ver}`, `h:{room}-hd-{user}-{thread}-{seq}`, `c:{room}-cl-{user}-{CommittedAt ms}` or `k:{room}-{op}`; a well-formed record of an unknown kind is Nak'd, not Term'd) and publishes it to `CHATIM_WORK` (WorkQueue, subject `work.p{slot % 32}`, `Nats-Msg-Id` = record id) within `RECONCILE_WINDOW`, unbounded retry. It confirms only past the prefix the work stream acked, every `RECONCILE_CONFIRM_EVERY`. It never waits, reads marks or builds events.
- Workers (`effects.Workers`) run on every core: partition n is fetched only while `Owns(n)` (durable consumer `work-p{n}`, batches of `WORK_FETCH_BATCH`). The registry runs, in order, `room_activity` (delay 0, one bulk `$max` per batch on `rooms`) and `msg_created` (delay `RECONCILE_DELAY`, checks ack marks, `Find`s unmarked messages, publishes and waits for the PubAck on the third JetStream client) for `MessageInserted`, `room_created` (delay `RECONCILE_DELAY`, no mark) for `RoomInserted`, and `room_activity` (`Seq: 0`, only `last_change_at/activity_bucket`), `edit_projection` (delay 0) then `msg_changed` (delay `RECONCILE_DELAY`, no mark; counts a republish only when the PubAck is not a duplicate) for `EditInserted`; `room_activity`, `reaction_counter` (delay `REACTION_COUNT_DELAY`, one witness-checked touch per message per batch) then `reaction_event` (delay `RECONCILE_DELAY`, publishes the current doc when its `ver` equals the record's) for `ReactionChanged`; `room_activity`, `pin_projection` (delay 0, one fold + CAS per room per batch) then `pin_event` (delay `RECONCILE_DELAY`) for `PinInserted`; `room_activity`, `member_event` (current doc when its `ver` equals the record's) then `member_count_event` (one `Rooms.Get` per room per batch, republishes the current count) for `MemberChanged`; `read_event` for `ReadChanged`; `hidden_event` for `MessageHidden`; `history_cleared_event` (current `cleared_at`) for `HistoryCleared` (all `RECONCILE_DELAY`, no marks); `member_count_repair` (delay 0) for `MemberCountCheck`. `edit_projection` and `msg_changed` return nil at once for the `message_edits` `ver 0` row. `room_activity` keeps `Seq` 0 for every kind but `MessageInserted`. A record is acked only when every effect returned nil; otherwise `Nak(WORK_RETRY_DELAY)`. A missing room or corrupt doc is dropped, counted in `effect_dropped_total`.
- `CreateRoom` publishes `room_created`, one `member_added` per member and `member_count_changed` `{room}-members-v1` on the fast path too; the stream drops the worker's copies by id within `EVT_STREAM_DUPLICATES`.
- Survival timer `work.Timers` (design §7.1, D111): `Arm` publishes a `MemberCountCheck` record to `work.timer.{room}.{op}` on `CHATIM_WORK` (`op` is a fresh random uint32 per `Arm`, kept in `Record.Version`) (`AllowMsgSchedules`) with `WithScheduleAt(now + MEMBER_COUNT_CHECK_DELAY)` and target `work.p{slot % 32}`, waits for the PubAck; `Disarm` is `DeleteMsg` by the PubAck seq (an already-fired timer is `ErrMsgNotFound`, not a warning). Firing rounds up to the second.
- Boot rules: `RECONCILE_DELAY < EVT_STREAM_DUPLICATES` and `RECONCILE_DELAY > PUB_ACK_TIMEOUT + 10ms + 1s` always apply (D65); `WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`. The publisher marks only `msg_created`. Lost history (`ErrFeedHistoryLost`) logs an error, `Forget`s and restarts from now; recover the gap with `/app resync` (scans rooms with `activity_bucket >= hour(from)` or `created_at` in range, replays the main timeline backwards, then the room's `message_edits`, `reactions` (current docs) and `pin_actions` by `{room_id, created_at|updated_at}`, then `members` docs by `last_change_at` (`MemberChanged`, plus `ReadChanged` when `read_ver ≥ 1` and `HistoryCleared` when `cleared_at` is set; `member_records`) and `hidden` by `created_at` (`hidden_records`), into the work stream at `-rate`; a room with only member/read/hide/clear changes in the gap needs `-room`).
- Bootstrap anchor: `mongostore.Bootstrap` writes `reconciler_state` `{_id: "changes", cluster_time: $$CLUSTER_TIME}` only when no `cluster_time` exists, so a fresh database also forwards writes made before the first term; re-running it never moves the anchor or a confirmed token.
- Room activity (`rooms.last_seq/last_message_at/last_change_at/activity_bucket`, D69) is written only by the `room_activity` worker effect; `activity_bucket` is the hour of the last change and is indexed (`{activity_bucket: 1}`, plus `{created_at: 1}` for the resync query).

**Edits (`apps/core/internal/change/mutate`; D62–D64, D82–D87, D96, D97, D109).**
- `EditMessage`, `DeleteMessage`, `HideMessage` and `ClearHistory` run in `mutate`, called by `grpcsrv`, not through actors; tools still route them by the room's slot.
- Edit and delete insert an immutable fact into `message_edits` (`_id` = `room│thread│seq│ver`, 28B, the CAS; `ver = base_ver + 1`; each row is one content version `{room_id, tenant, kind, created_by, text, created_at}`; the first edit also inserts-if-absent the `ver 0` row (`kind original`, the sent text, sender, sent time); a delete of a never-edited message writes no `ver 0` row), project it onto `messages` with `ApplyEdit` (only where `v < ver`), purge older fact texts on delete, then ack. A duplicate fact with the same author, kind and text is a retry and succeeds; a stale base or a deleted message is `FAILED_PRECONDITION`. Rights come only from `access.Policy`, asked with the message author and kind after `Find` and before retry recognition; `mutate` has no author, owner or kind rule (D86, D87).
- The fast path enqueues `msg_edited`/`msg_deleted` (id `{room}-{thread}-{seq}-v{ver}`, current snapshot, no ack mark); a retry re-enqueues the same id. Workers rerun `edit_projection` and `msg_changed` from the `EditInserted` record, which heals a core dying between fact and projection; the stream drops the duplicate event by id.
- Hide upserts `hidden {user_id, room_id, thread_root, seq}` with `$setOnInsert created_at`; the first hide publishes `message_hidden` `{room}-hd-{user}-{thread}-{seq}`, a repeat writes nothing. Clear sets `members.cleared_at = max(old, server time)` (ms, active members only) and returns it; the reader then sees messages with `ts ≤ cleared_at` as hidden on every timeline; a real raise publishes `history_cleared` `{room}-cl-{user}-{cleared_at ms}` (D97, D109; both on the `member` subject). `GetEditHistory` (action `read_edit_history`, `after_ver`) returns the `ver 0` row on the first page then each fact, nothing for a deleted message, `NOT_FOUND` for a missing one.
- Proto renames (D96, field numbers kept): `version` → `ver`, `base_version` → `base_ver`, `after_version` → `after_ver`, `pin_version` → `pin_ver`; `ClearHistoryRequest.up_to_seq` is reserved. Go names (`domain.Message.Version`, `mutate.EditCmd.BaseVersion`) are unchanged.
- Wiring: `apps/core/internal/app/service_wiring.go` (`wireService`) builds the mutate `access.Checker` (`DefaultPolicy{LockedKinds}` from `MESSAGE_LOCKED_KINDS`) and the `mutate.Mutator` for `grpcsrv` (`Deps.Mutator`, `Edits`, `Hidden` are required).

**Reactions and pins (`mutate`, `counter`, `pinproj`; D88–D95).**
- `ReactMessage`, `PinMessage` and `UnpinMessage` run in `mutate` (`Admit` → `Find` → `Allow` with actions `react_message`, `pin_message`, `unpin_message`; the default policy allows any member and `MESSAGE_LOCKED_KINDS` does not apply, D94).
- A reaction is one emoji per (user, message) in clustered `reactions` (`_id` = message key + user; fields `message_key room_id tenant user_id emoji previous_emoji ver updated_at`; indexes `{message_key, emoji}`, `{room_id, updated_at}`; D88; below `e`/`pe`/`n` mean `emoji`/`previous_emoji`/`ver`). Set is one `FindOneAndUpdate({_id}, pipeline, upsert, ReturnDocument Before)`: `$cond` keeps `pe/e/n/ts` when the stored emoji equals the new one, so the doc stays byte-identical (no oplog entry, no feed change); otherwise `n+1` and `pe` = the old emoji. The filter is equality on the unique `_id`, so the server retries a duplicate-key upsert itself. The result comes from the doc before the write: none = `n` 1, same emoji = no-op, else `n+1` and Prev = the old emoji. Remove is an update without upsert that leaves the tombstone `e: ""`. No cid: the command is the desired state (D89). A deleted message only accepts a removal.
- Emojis come from the fixed list `REACTION_EMOJIS` (default `👍,❤️,😂,😮,😢,🙏`, blank = default; boot checks non-empty, valid, no duplicates, at most 100). An emoji outside it is `INVALID_ARGUMENT` (`domain.ErrEmojiNotAllowed`), checked before membership and policy; the list length is the per-message limit. `GetReactionSettings` (not room-routed, needs caller metadata) returns the list in config order so the frontend shows only those (D95).
- Counts (design §7, D90): `messages.rx {c: [{e, n}], v}`, an array sorted by count then emoji. `counter.Toucher.Touch` checks witnesses (`Reactions.Count` reads each witness doc with majority in a causal session; `n < N` is `store.ErrStaleRead`), runs the covered aggregate on `{k, e}` in the same session, skips an equal summary and otherwise CASes `rx.v` (`$exists: false` at 0). The fast path touches inline before replying (`FastTouchTries` 3, errors ignored); the `reaction_counter` worker touches once per message per batch (`DefaultCounterTries` 3) and counts repairs in `counter_repaired_total{counter="reactions"}`. Never `$inc`.
- Pins (D92): no base version. `pinproj.Projector.Current` folds `rooms.pins/pin_ver` with the facts after it; the command appends `pin_actions {_id: room│pin_ver+1}` (dense pv, so `PIN_LIMIT` 50 is exact; already in the wanted state = success with no fact; a duplicate key with the same op, message and user is the result, else retry ≤3, then `ErrRetryLater`), then `Project`s (fold + CAS `pv == p`, up to 5 tries, shared with the `pin_projection` worker). These pin and touch loops break the no-internal-retries rule and drop to one try in M2c (roadmap R1). Pinning a deleted message is `FAILED_PRECONDITION`; unpinning still works. `Rooms.Get` never reads pins.
- Events (D93, no ack marks): `reaction_changed` `{room}-{thread}-{seq}-{user}-n{n}`, `counts_changed` `{room}-{thread}-{seq}-reactions-v{v}`, `msg_pinned`/`msg_unpinned` `{room}-p{pin_ver}` with the message snapshot (no text when deleted). Intermediate reaction events can be lost; only the last state is guaranteed (set class). `GetHistory` returns `Message.reactions` (counts only). SysMsg cids are `sys-{event_id}`.
- Wiring: `apps/core/internal/app/service_wiring.go` builds `counter` and `pinproj` from the Mongo store and passes `mutate.Limits` from config.

**Members, priority and read position (`mutate`, `ownership`, `memberwatch`; D96–D111).**
- `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`, `MarkRead`, `MarkUnread` run in `mutate` (7 RPCs, `grpcsrv/members.go`, `read.go`), routed by the room's slot. No internal retries (rule for every command, owner 2026-10-07; open violations are listed in roadmap R1, R4, R5): a CAS miss, a transaction conflict or a request in flight on another core is `ErrRetryLater` (`UNAVAILABLE`) at once; clients retry (`tools/internal/route` keeps the same `request_id`). A DM refuses member commands (`FAILED_PRECONDITION`).
- `members` doc (D98): `{_id, room_id, tenant, user_id, role, state, priority, joined_at, ver, previous_role, previous_state, previous_priority, request_id, updated_at, updated_by, last_change_at, cleared_at, read_seq, read_ver}`; `state` 1 active / 2 tombstone (never deleted); `state`, `priority`, `ver`, `read_seq`, `read_ver` always written. Indexes `{room_id, state, role, priority: -1, joined_at, user_id}` (count, owners, successors) and `{tenant, user_id, state, room_id}` (rooms of a user). `rooms` adds `member_count_ver` and `owners_ver`.
- Default policy (D101): owner does everything; admin adds people and removes `member`-role people; only the owner changes roles and sets `priority`; anyone leaves. The policy is asked before revealing whether the target exists (a missing or left target is a fake `member`). Codes: bad input or over `MEMBER_BATCH_MAX` → `INVALID_ARGUMENT`; DM, last owner demoting itself → `FAILED_PRECONDITION`; not active, denied, a never-member leaving → `PERMISSION_DENIED`; removing a never-member, changing a non-active target → `NOT_FOUND`; removing a tombstone, leaving twice, setting the current role/priority → `OK` with `changed = false`.
- Add (D99): `request_id` is required and deduped (`dedupe.Requests`, `chatim:req:*`, RAM LRU + Redis, `CID_COMMITTED_TTL`): done → reread and return the people still active and added by this request; busy → `UNAVAILABLE`; new → arm the timer → one `BulkWrite(ordered:false)` of upsert pipelines with `$cond` (an active doc stays byte-identical) → forget the actor cache → `$inc member_count` by `UpsertedCount + ModifiedCount` → disarm → `member_added` per person, then `member_count_changed`. Rejoiners always get role `member`, `priority 0`, `read_seq = max(old, last seq)`, keep `cleared_at`. Dedupe holds only 15 minutes and while Redis keeps the key.
- Plain path (target not an owner, not promoting to owner): one `UpdateOne` CAS on the target's `ver` (`ApplyMember`); remove/leave of an active doc arms the timer first and `$inc`s −1 after.
- Owner path (D100, the only transaction): `ownership.Affects` → `OwnerChanges.ChangeOwners` runs one transaction (`StartTransaction`/`CommitTransaction`, never `WithTransaction`): read `owners_ver`, the docs, up to 2 owners and the top admin/member candidates; the pure `ownership.Plan` writes the successor first (admin before member, then highest `priority`, earliest `joined_at`, lowest user id) then the target, each a CAS on `ver`; `$inc rooms.owners_ver` (the shared conflict point) and the member count in the same doc update; commit. No timer. `LeaveRoom` returns `new_owner`; `member_removed` carries none. Invariant: a group with active members always has an active owner. memstore gives the same semantics.
- Member count (D102, D111): only `Rooms.Create`, the in-command `$inc` (by docs that really changed state), the owner transaction, the `member_count_repair` worker and `/app recount` change it. A failed `$inc` is logged, the timer stays armed, the command still succeeds. Arm failure → `UNAVAILABLE` before any write. `MEMBER_COUNT_CHECK_DELAY` (5s) must exceed `CORE_REQUEST_DEADLINE`.
- Read position (D105): `MarkRead(seq)` clamps 0 or past the end to the last seq and raises when `read_seq < seq`; `MarkUnread(seq)` (0 → `INVALID_ARGUMENT`) lowers to `seq − 1`; both bump `read_ver`, use plain operators and never touch `ver`; a real move publishes one `read_updated` `{room}-rd-{user}-v{read_ver}`.
- Events (D104, subject `member`): `member_added` (with `read_seq`, `read_ver`), `member_removed` (`REMOVED`/`LEFT`), `member_role_changed`, `member_priority_changed`, all `{room}-mb-{user}-v{ver}`, derived from the doc's `previous_*` by `pbconv.MemberEvent`; `member_count_changed` `{room}-members-v{member_count_ver}` on subject `room`. Only the last state of a doc is guaranteed.
- Actor member cache (D106, D110): generation + 10s TTL; no negative caching. `Router.ForgetMembers(room)` runs after each real change on the same core; `memberwatch` on every core subscribes core NATS `{live}.*.member.*.evt.member_removed` and `….member_role_changed`, takes the room id from subject token 4 and forgets. Lost events (core NATS has no replay) fall back to the TTL.
- Config: `MEMBER_BATCH_MAX` (500, 2..1000, counted after dedupe) for `CreateRoom` and `AddMembers`; no total member cap.

**Permission hook and reader pipeline (`access`, `view`).**
- `access.Policy` is the single permission point; `access.Checker` always enforces tenant and membership first, then asks the policy. `Checker.Admit` checks tenant and membership only, `Checker.Allow` asks the policy with `Request.Author` and `Request.Kind` (the target message's author and kind, empty for room actions), `Authorize` does both; actions on one message (edit, delete, hide, edit history) run `Admit` → `Find` → `Allow`. The default policy of `NewChecker` and the actor is `access.DefaultPolicy`: edit/delete only by the author, every other action for any member (D86); the chat policy module (user → role → permission) plugs in at Phase 2.
- Locked kinds (D87): core never hard-blocks edit/delete of a message kind (e.g. future system messages). `MESSAGE_LOCKED_KINDS` is a comma-separated list of kind names, case-sensitive, empty by default (nothing locked); an unknown name is a boot error. `DefaultPolicy.LockedKinds` denies `edit_message`/`delete_message` on a locked kind even to the author. Only the mutate checker gets the config; the `grpcsrv` and actor checkers use `DefaultPolicy{}`. Today only `text` exists; a new kind adds one entry to `domain.kindNames`.
- `SendMessage` asks the checker in the actor (`actor.WithPolicy`), edit/delete/hide/clear/react/pin/unpin, member and read commands in `mutate`, `GetHistory` and `GetEditHistory` in `grpcsrv`. `Rooms.Member` treats a doc with `state ≠ 1` as not a member.
- `view.Pipeline` runs steps over a page read from the store; `view.CollapseRetried` hides CD2/CD3 duplicates by (sender, cid). `view.MaskDeleted` (deleted → no text, no reactions) and `view.HideForViewer` (`CreatedAt ≤ Viewer.ClearedAt` or hidden by the reader → `hidden`, no text, no reactions) follow it in `view.Default()`; seq and version are kept.
- On seq contention with another core's doc the actor pauses with `Jitter(5ms → 200ms)` between reassign rounds, then yields the room through `a.retire` (always via `requestRetire`, which holds `r.mu`) so the next command goes to the real owner (D77). It reassigns at most 3 rounds first; whether to keep that internal retry is open for M2c (roadmap R5). `room_yields_total` counts yields (alert `ChatimSeqContention`).

**Detectors (D76, D78).**
- Components keep atomic counters and accessors; `apps/core/internal/platform/metrics` registers them as pull-based `CounterFunc`/`GaugeFunc` on a private Prometheus registry served at `GET /metrics` on the admin port (wiring in `apps/core/internal/app/metrics_wiring.go`). All names start with `chatim_core_`. Besides the labels below: `grpc_load_shed_total` (calls rejected by the in-flight limiter) and `room_yields_total` (D77). `room_created` still counts every PubAck in `reconcile_republished_total`, duplicates included (fix in M2c, roadmap R3). Edit effects add the labels `edit_projection` (only `effect_dropped_total`; its `effectCounters.republished` is nil) and `msg_changed` (both `effect_dropped_total` and `reconcile_republished_total`, which counts only PubAcks the stream did not flag as duplicates). Reaction and pin effects add `reaction_event`, `reaction_counter` and `pin_event` (both counters) and `pin_projection` (only `effect_dropped_total`); `counter_repaired_total{counter}` counts summaries the workers rewrote. Member effects add `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event` and `member_count_repair` (both counters); repaired member counts go to `counter_repaired_total{counter="members"}` (covered by `ChatimCounterRepairSurge`). `memberwatch` exports `member_cache_forgets_total` and `member_watch_malformed_total` (no alert). No owner metric: the transaction leaves no half state.
- Alert rules are in `deploy/prometheus/alerts.yml` (16 rules); check with `make alerts-check`. A new guarantee needs a metric and a rule in the same PR.
- `reconcile_lag_seconds`, `reconcile_republished_total{effect}`, `effect_dropped_total{effect}`, `work_processed_total` and `work_failures_total` come from the workers on every core; reader metrics only on cores with `RECONCILE_ENABLED`.
- `reconcile_lag_seconds` is how far the workers run behind each effect's delay, not raw age. `mongo_oplog_window_seconds` is NaN when the oplog cannot be read (needs read access to `local`).

**Process lifecycle (`apps/core`).**
- Config is validated at boot by `apps/core/internal/config`, which applies cross-field timeout rules; components export `Validate()`.
- Start order: publisher → flusher → cid batcher → router → slot manager → workers → reader → `memberwatch` (subscribes once the router runs) → gRPC. gRPC is served only after a clean start.
- Shutdown order: `/readyz` false → drain delay → gRPC → reader (`RECONCILE_DRAIN + 1s`) → workers (`WORK_DRAIN + 1s`) → `memberwatch` (Unsubscribe, instant, no stop phase) → router → cid batcher (flushes queued Commit/Abort within `2 × REDIS_OP_TIMEOUT`) → flusher → publisher (drains its queues, then waits for `PublishAsyncComplete` within `CORE_PUBLISHER_DRAIN`) → slot release → clients. All of it fits within `CORE_SHUTDOWN_BUDGET`: the default plan is 26.2s of 28s, so raising any stop phase needs a higher budget and compose `stop_grace_period` (33s).
- `/app probe` is the container healthcheck.
- `/app resync` and `/app recount -room ID [-dry-run]` are one-shot subcommands: Mongo + NATS only, same config and redaction as serve. `recount` prints `room=… stored=… counted=…`; without `-dry-run` it CASes `member_count_ver` (a miss exits with an error, run again) and publishes `member_count_changed`. It writes even when the count is already right.
- Every log line goes through a handler that redacts MONGO_URI and NATS_URL credentials, the Mongo password and both Redis passwords.

**Shard-readiness rules (design §5.1).** Mongo runs as a replica set without sharding, but sharding must later need configuration only:
- Large collections use a room-prefixed `_id`; the future shard key is `{_id: 1}`.
- Every query on `messages` carries the room prefix, and `messages` has no secondary index. Small fact collections (`message_edits`, `pin_actions`) may have `{room_id, created_at}` (D70).
- No multi-document transactions, with one exception (D100): commands that touch an owner run one MongoDB transaction over the room's `members` docs and its `rooms` doc (`$inc owners_ver`), once, a conflict returns `UNAVAILABLE`. No `$lookup` into large collections. Bounded point lookups are fine; unbounded joins are not.
- `reactions` is clustered with `_id` = message key + user, so one message's reactions are one range under the future shard key `{_id: 1}`; it has `{message_key, emoji}` and `{room_id, updated_at}` (D88, replaces D68).
- `members` is clustered with `_id` = room + user; "rooms of a user" is its `{tenant, user_id, state, room_id}` index (no `user_rooms`, D98), which scatter-gathers once sharded by room.
- The URI, read preference and write concern come from config.
- Collections are created only in the bootstrap step.

**Testing conventions.**
- Packages with goroutines use `testing/synctest` and `goleak`.
- Unit tests use miniredis. Call `testlog.SilenceRedis()` in TestMain when dials fail on purpose.
- No short real-time polls: on the dev VM, CPU clocks skew by up to ~7ms and timers wake up to ~36ms late. Wait on explicit signals instead.

## Docs

- `docs/designs/261005-chatim-architecture.md`: the single source of truth: requirements (R17 revised), principles P1–P8, the data-class framework every plan must use (§4), data model, write path (built and planned), counter, effect engine, read path, gateway, guarantees with detectors, and the Decision Log (D1–D60 kept by id, new D61–D111). Add new decisions there.
- `docs/designs/2026-10-04-chat-architecture-components.md`: component view: macro diagram, the `apps/core` package groups with dependency direction, and sequence diagrams of send, change, effect engine and slots. Follows the main design.
- `docs/roadmap.md`: plan-writing rules, milestones M2b.0 → M5, dependencies, readiness.
- Every plan has a companion technical summary `docs/plans/<plan>-summary.md` (Vietnamese, no Go code, written before execution) that the owner critiques and approves; after execution it is updated to the final as-built state and stays as the milestone record: glossary, diagrams, full field table with examples, per-use-case flows, correctness and races, events, libraries, decisions with rejected alternatives, costs, risks, team-chosen points, tests; anything that serialises work must be called out. The full plan is for AI implementers and contains no code: per task it lists goal, files, todos, technique, contracts in prose, tests and what they prove, commands, done criteria and commit; implementers write the code with TDD (rule in `docs/roadmap.md`, overrides the `writing-plans` "complete code" guidance). Commands never retry internally: a CAS or transaction conflict returns `ErrRetryLater` at once, workers Nak. When a derived value (counter, projection) is written inline after the main write without a shared transaction, use the survival timer (design §7.1, D111): arm a NATS scheduled repair before the main write, disarm after the derived write. DB field names are full basic English words except in `messages`; counters end in `_ver`; `updated_at`/`updated_by`; `request_id` for retry dedupe.
- Plan locations (owner 2026-10-09): `docs/plans/` holds only documents for the owner to review and approve (`<plan>-summary.md`). The full execution plan for AI implementers goes to `.claude/plans/<plan>.md` (local, gitignored), written with `writing-plans` (overrides its default `docs/plans/` path) and executed with `subagent-driven-development` or `separate-driven-development`; its "Kết quả thực thi" section stays there and is folded into the summary at the end. Executed: M2b.0–M2b.4 (summaries in `docs/plans/`, execution plans moved to `.claude/plans/` on 2026-10-09).
- `docs/poc/README.md`: dev results (R1–R5, C1) and the prod-like measurement list P1–P10. Dev numbers only validate tools.
- `docs/research/261005-system-mechanisms-synthesis-report.md`: the comprehensive canonical research report consolidating the entire system mechanisms review.
- `docs/archive/`: the Phase 1 design (full D1–D60 rationale), the draft mechanisms review, the component diagrams, the M0–M2a.3 plans, the 8 archived review documents (`docs/archive/research/261004-*`), and the old PoC notes. Frozen.
- `docs/git-workflow.md`: branches, merge Definition of Done, readiness levels, SemVer tags and handling a broken `main`.
- `.claude/plans/*_design.md`: local (gitignored) decision logs of M2a, M2a.1/M2b draft, M2a.2 and M2a.3.
- Before writing the M3 plan, running the prod-like PoC or sizing the work stream or oplog, ask the owner for real numbers to replace the assumptions in design §2.3.
