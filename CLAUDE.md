# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

chatim is an internal, logically multi-tenant chat platform (CPaaS) in Go. Phase 1 has two apps:
- `core`: rooms, members, messages, seq allocation, MongoDB storage, events to NATS JetStream.
- `gateway`: WebSocket via gws, realtime fanout. Not built yet.

Done:
- M0–M1: foundation and PoC.
- M2a, merged to main (PR #5): core CreateRoom/SendMessage/GetHistory over gRPC, cid dedupe, JetStream publish, crash recovery, and two cores in compose.

Next is M2b: edit/delete/reactions and safe pts allocation. The milestone order is in `docs/roadmap.md`. Project docs are written in Vietnamese.

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

- **Risky tasks get one reviewer.** These are the write path, dedupe, publish, recovery, slots and lifecycle.
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
make e2e                    # tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events
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
- room_events = `room│pts` (16B)

These are `_id`s of MongoDB clustered collections, so any history page, including the oldest, is one range scan. `thread_root = 0` is the main timeline, seq starts at 1, and `MaxUint64` is reserved.

**Counters.**
- `seq` is a position in a timeline.
- `pts` numbers every persisted event in a room; clients use it for gap detection and Sync.
- In M2a pts == seq. Recovery paging and the pubwm pin rely on that, so M2b must switch them to pts.
- Room ids (`pkg/ids`) are random non-zero 63-bit numbers, sent as decimal strings.

**Send path (`apps/core/internal`).**
1. `grpcsrv` reads the tenant and user from the metadata keys `x-chatim-tenant` and `x-chatim-user`; callers are trusted internal apps until mTLS. Handlers return domain or `apperr` errors, and `pkg/grpcserver` maps them to gRPC codes at one boundary, so clients see sentinel text only.
2. `actor.Router` runs one goroutine per room, with a bounded mailbox and idle eviction. An actor is retired when its slot moves, and it keeps one write group in flight.
3. `dedupe` checks the cid against a RAM LRU, then the dedupe Redis `chatim:cid:{room}:{user}:{cid}` (pending `p:{core}` for 10s, committed for 15m). Calls are batched per group. On Redis errors it falls back to the LRU only, using the `redisguard` cooldown.
4. `publish.ActivityMarks` runs one Lua script before the insert: `ZADD chatim:active:{slot}` and `SET NX chatim:pubwm:{room}`, which pins the publish base.
5. `flush.Flusher` shards batch groups into `insertMany(ordered:false, w:majority)` every 2ms or 256 docs. A group that can't finish by its deadline is not sent (`ErrNotSent`).
6. Each insert ends as Inserted, Duplicate, Unknown or Rejected. Duplicate and Unknown are reconciled with `Find` (majority read) by (from, cid). Every retry is bounded by the cid reservation TTL, so a pending key never expires while an attempt is live.
7. After commit: dedupe Commit, then ack, then `publish.Publisher` sends to stream `CHATIM_EVT` on subject `evt.{t}.room.{rid}.msg_created` with `Nats-Msg-Id = {room}-{pts}`, RePublished to `live.*`. `pubwm` advances only over contiguous pts that got a PubAck.
8. `recovery.Sweeper` runs on slot claim and every 30s. Rooms in `active:{slot}` whose DB last pts is above pubwm are republished through their actor. Caught-up rooms are removed by CAS after 15s.

`GetHistory` reads the store directly, not through actors.

**Storage ports.** The ports are `store.Messages` and `store.Rooms`. The adapters are:
- `mongostore`: clustered collections, created by a bootstrap step at startup.
- `memstore`: in-memory, used by unit tests.

Every adapter must pass the `storetest` contract suite, so a PostgreSQL adapter could replace Mongo if the prod-like PoC says so.

**Two Redis instances (D44, D45).**
- State, `chatim-redis`: `chatim:core:*`, `chatim:cores`, `chatim:slot:*`, pub/sub `chatim:slots:changed`, `chatim:pubwm:*`, `chatim:active:*`. AOF everysec, `noeviction`.
- Dedupe, `chatim-redis-dedupe`: only `chatim:cid:*`. No persistence, `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`. If it dies, dedupe falls back to the LRU.
- Both require AUTH. Passwords come from `.env` through compose secrets; cores read `REDIS_PASSWORD_FILE`/`REDIS_DEDUPE_PASSWORD_FILE`, tools read `-redis-password` or `REDIS_PASSWORD`. Passwords never go in argv, `docker inspect`, echoed make lines or logs.
- There is no `REDISCLI_AUTH` env in the containers: use `make redis-cli [INSTANCE=dedupe] ARGS=…`.

**Mongo credentials (D46).** Compose secret `mongo_password` comes from `MONGO_ROOT_PASSWORD`. Cores get a credential-free `MONGO_URI` plus `MONGO_USER` and `MONGO_PASSWORD_FILE` (`MONGO_AUTH_SOURCE` defaults to `admin`). A `MONGO_URI` with credentials still works (itest, non-compose runs), but not together with `MONGO_USER`. mongosh never gets `-p`: scripts authenticate inside `--eval` with `db.getSiblingDB("admin").auth(user, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim())`.

**Slots and soft ownership.** `slot = mix64(room) % 1024` (`pkg/slotmap`, with rendezvous `Score` and `Preferred`). `slot.Manager.Step` runs once per tick:
1. Heartbeat with one Lua script: `SET chatim:core:{id}`, `ZADD chatim:cores` scored by Redis `TIME`, and a prune of expired cores.
2. Renew the leases `chatim:slot:{n}`. This is a multi-key Lua script, so Redis must be a single primary, not Redis Cluster.
3. Claim or release slots toward `ceil(1024/alive)`.
4. Publish on `chatim:slots:changed`.

Hooks:
- The hooks receive batches of slots and share one `HookTimeout` per Step.
- BeforeRelease and AfterLose call `Router.EvictSlots`.
- AfterClaim calls `EvictSlots` and then `Sweeper.Trigger`.

Rules:
- `Owns()` is true only while the lease stamp is newer than `min(LeaseTTL, HeartbeatTTL) − Tick`, measured from the start of the tick (D24).
- The slot manager uses its own Redis client.
- Never use `SCAN` or `KEYS`: cid keys fill the keyspace.
- Clients use `slotmap.Resolver` to map a slot to a core address.

**Invariant: any core must handle any room correctly.** Ownership only buys batching, ordering and cache hits. Correctness comes from the unique `_id` acting as CAS, so a Redis failover or a double owner never loses or duplicates a message.

**Process lifecycle (`apps/core`).**
- Config is validated at boot by `apps/core/internal/config`, which applies cross-field timeout rules; components export `Validate()`.
- Start order: publisher → flusher → router → sweeper → slot manager → gRPC. gRPC is served only after a clean start.
- Shutdown order: `/readyz` false → drain delay → gRPC → sweeper → router → flusher → publisher → slot release → clients. All of it fits within `CORE_SHUTDOWN_BUDGET`.
- `/app probe` is the container healthcheck.
- Every log line goes through a handler that redacts MONGO_URI and NATS_URL credentials, the Mongo password and both Redis passwords.

**Shard-readiness rules (design §4.1).** Mongo runs as a replica set without sharding, but sharding must later need configuration only:
- Large collections use a room-prefixed `_id`; the future shard key is `{_id: 1}`.
- Every query on them carries the room prefix.
- No multi-document transactions, and no `$lookup` into large collections.
- The `reactions` unique index `{k,u,emoji}` starts with `k`.
- The URI, read preference and write concern come from config.
- Collections are created only in the bootstrap step.

**Testing conventions.**
- Packages with goroutines use `testing/synctest` and `goleak`.
- Unit tests use miniredis. Call `testlog.SilenceRedis()` in TestMain when dials fail on purpose.
- No short real-time polls: on the dev VM, CPU clocks skew by up to ~7ms and timers wake up to ~36ms late. Wait on explicit signals instead.

## Docs

- `docs/designs/260930-chat-core-gateway-design.md`: the source of truth for the data model, write and read paths, failure handling and the Decision Log (D1–D46). Add new decisions there.
- `docs/plans/`: per-milestone plans, executed task by task with `subagent-driven-development` or `separate-driven-development`.
- `docs/poc/README.md`: PoC and corebench results (C1). Dev numbers only validate tools; go/no-go needs prod-like runs.
- `docs/roadmap.md`: milestone status and carried-over items.
- `docs/git-workflow.md`: branches, merge Definition of Done, readiness levels, SemVer tags and handling a broken `main`.
- `.claude/plans/m2a-core-send-history_design.md`: local (gitignored) M2a decision log.
