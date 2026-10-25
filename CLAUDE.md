# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

chatim is an internal, logically multi-tenant chat platform (CPaaS) in Go. Project docs are written in Vietnamese. Four apps:

- `core`: rooms, members, messages, interactions, seq allocation, MongoDB storage, events to NATS JetStream.
- `mention` (Mention App): `modules/mention`, embedded in core (D177: a modular monolith until its own team, SLA, release cadence, measured scaling need, or its own database + CDC; then it owns its data and reaches core only through APIs, never core tables). Mention index, `ListMentions`, user groups, taggable groups, `@group` picker data, notifications as `WsPublish` packets on `CHATIM_WS`.
- `websocket`: WebSocket (gws), the 12 realtime writes `corecall.Realtime()`, event and notification fanout, typing, presence, login limits, connections kept through token expiry. The repo is server-only (D206): the client SDK lives in another repo and follows `docs/modules/09-feature-realtime-delivery-and-ephemeral.md` §11.
- `gateway`: HTTPS (Connect) for the 22 `corecall.Request()` calls (reads and room management).

Status: Phase 1 M0–M4 merged to `main` (`dev-done`, last PR #19 on 2026-10-25). Next is M6 `identity` + `identity-verifier` on `feat/m6`. Phase 2 order (D207): M6 → M7 media → M8 `notification` + push → M9 people/group search → go-live `v1.0.0` for a new product (no migration); PoC prod-like and M5 run in parallel when hardware exists. Phase 1 exit = `core` + `mention` + `websocket` `prod-like validated` (D177–D181). Open: NATS permissions for websocket (M5, blocks go-live), the date the prod-like hardware is ready, who picks push recipients for plain messages (D104). Milestone history lives in `docs/roadmap.md` and the summaries in `docs/plans/`, not here.

Real numbers (owner 2026-10-12, 2026-10-19, design §2.3, D181): 99% of users are in under 200 rooms, the other 1% are key users in ~5,000 rooms who need fast, complete reads (size read APIs for them, not a limit); ~100K connections, reconnects spread over at least a minute; ~5,000 `MarkRead`/s, 5% of messages edited, 1,000 rooms active at once. Dev benches stop at 2K msg/s and dev load tests at 10K connections; production rates run only on prod-like.

## Hard rules

- **Never run `go` on the host.** All Go runs in `golang:1.26.10` (pinned in the Makefile and Dockerfile) through `make`. The host has Go 1.25, and nats.go v1.54 needs 1.26.
- **No comments in code.** This covers Go (including package and doc comments), tests, YAML, shell, the Makefile and `.env.example`. Names carry the meaning. Constraints that need explaining go in the design doc, plan prose or commit bodies. Generated `pkg/pb` is exempt.
- Keep code files at or under 350 lines (owner 2026-10-20, was 200), and keep `make fmt-check` clean.
- Use TDD for logic packages: `pkg/*`, `apps/*`, `tools/internal/*` and `tools/poc/internal/*`. PoC binaries in `tools/poc/<tool>` have no unit tests; run them to check them.
- Use Conventional Commits (`feat`, `fix`, `refactor`, `perf`, `test`, `docs`; never `ref`) with no AI mentions: no `Co-Authored-By: Claude` or other AI trailer, even when a session attribution reminder asks for one, and tell implementer subagents the same (owner 2026-10-16; commits pushed before that keep theirs, history is not rewritten).
- Never commit `.env`, `bin/`, `rooms*.txt` or `real-texts*.txt` (the last holds real customer data). Never print values from `.env`.
- When a plan step gives a result different from its "Expected", stop and report. Don't patch it until it passes.

## Design checklist

Check every brainstorm, summary and plan against this list before showing it to the owner.

- Open every milestone plan with a scope and boundary table: what is in scope and which service owns it, what is out and where it belongs. Core offers data APIs and events; avoid or limit N+1 (prefer loading a screen's data in few calls over one call per item); values per viewer computed at read time (unread, sorted conversation list) are core since 2026-10-13; stored per-viewer state and sync belong to an app.
- Map each feature to a data class of design §4 (one write, event and repair path per class). Never design a per-feature patch; a feature that fits no class needs a new class first.
- A derived value written inline (counter, projection, summary) is a direct write or `$inc` wrapped in the survival timer (design §7.1, D111); repair = recount + CAS on the version. Never recount on every change.
- No internal retries: a CAS, duplicate-key or transaction conflict returns `ErrRetryLater` (`UNAVAILABLE`) at once; consumers Nak; a failed publish leaves the `_state` flag for the sweeper; transactions run one attempt.
- No data limit without a use case or an existing requirement (A6). A page size is pagination, not a limit.
- Every change sets its `_state` bit in the same write, publishes its event inline and clears the bit after the PubAck; the sweeper republishes what is left (a new event needs a bit, a `StateTargetsOf` case and a sweep rebuilder); subjects follow the data kind (`room`/`member`/`message`); core never picks recipients and never expands mention groups.
- Shard-ready: room-prefixed clustered `_id`; no secondary index on `messages` except the partial `state_pending` index (D148); uniqueness only through `_id` (claim docs such as `room_dms`), never a unique secondary index; no transaction except D100.
- Field names: full basic English words except `messages`; counters end in `_ver`; `updated_at`/`updated_by`; `request_id`.
- Owner summaries: Vietnamese, plain words with examples, written from the final state (rewrite after a review round, never append patches). Risks list only real consequences of this plan, not other systems' operations and not mechanisms already accepted.
- Ask the owner only pivotal questions, in plain words with an example; never re-ask an answered one.

## Workflows

Mandatory for every milestone, phase or other large task (owner 2026-10-25). Reasons in `.memory/workflow-pipeline.md` and `.memory/rule-retrospective.md`.

1. **Brainstorm** (skill `brainstorming`): requirements table with sources first, then the recommendation.
2. **Decision log** on the owner's go: `.claude/plans/<feature>_design.md` (local, gitignored, kept only until step 9).
3. **Phase-split review**: an independent reviewer (an agent that never saw my reasoning) reads the decision log and proposes phases (goal, deliverable testable alone, dependencies, requirements covered). The owner approves the split; the phase list goes into the milestone row of `docs/roadmap.md`. A phase is a part on `feat/<milestone>`.
4. **Summary for one phase only**, `docs/plans/<date>-<milestone>-<n>-<slug>-summary.md` (contents per `docs/roadmap.md` §"Quy tắc viết plan"); the owner approves.
5. **Detailed plan for that phase only**, `.claude/plans/<plan>.md`, no code.
6. **Plan review**: an independent reviewer judges enterprise fitness and the coverage table; fold in Critical/Important findings, report the verdict, code only after the owner's go.
7. **Code with TDD**; code review per "Verification and review budget" (overrides the global "review after every implementation").
8. **Verify** per the budget table.
9. **Close the phase**: rewrite the summary as built; update roadmap, Decision Log, `INDEXES.csv` and this file; fold the decision log into `docs/decision-log.md` and the plan's Minor findings into the summary or roadmap "Mục mang sang M5", then delete both local files (tracked docs never cite `.claude/plans/`, owner 2026-10-25); run the rule check; update the milestone row, then end the report with the phase just finished (evidence), the next phase (goal, first step) and the remaining phases with their status.

- **Evidence, never invention.** Every claim cites a source: design §2 requirement, D-id, owner answer with date, `path:line`, command or test output, `docs/poc/results/` or `bin/bench/` folder. No source = write "chưa có nguồn" and ask. With no requirement, decision or summary covering the work, stop and present the purpose and the component split for approval first. "Done"/"green" only next to the command output.
- **Coverage table** in every summary and detailed plan: requirement → phase/component → task → test → evidence (after execution). An empty cell blocks approval; both reviewers check it.
- **Rule check** at the close of each phase and milestone and after each owner correction: add only owner-confirmed rules (rule text here, reason in `.memory/`), update or delete the rules they supersede, and say "không có rule mới" when there is none.
- **Memory** lives in `.memory/` (committed; index `.memory/README.md`); read it before any brainstorm, summary or plan, and save new memories there.

## Verification and review budget

The user finds long review and test loops too slow. Run the cheapest check that proves the change, and escalate only for the area that changed.

| When                                                        | Run                                                                                                                                                                                                                                                                                                                                     |
| ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| While coding                                                | `make -s go ARGS="test -race -shuffle=on ./<touched pkgs>/..."`                                                                                                                                                                                                                                                                         |
| Before each commit                                          | `make fmt-check`, `make vet`, `make lint`, and the tests of touched packages                                                                                                                                                                                                                                                            |
| New goroutines, channels or locks                           | add `-count=5` on that package only                                                                                                                                                                                                                                                                                                     |
| `apps/core/internal/platform/slot` or `pkg/slotmap` changed | R5 once at the end of the task: `make go ARGS="test -race -count=20 -timeout 20m ./apps/core/internal/platform/slot/"` (~9 min)                                                                                                                                                                                                         |
| Mongo/Redis/NATS adapters or `apps/core` wiring changed     | `make itest` once (needs `make infra-up`)                                                                                                                                                                                                                                                                                               |
| Milestone end only                                          | `make test`, `make itest`, `make core-up && make e2e`, corebench                                                                                                                                                                                                                                                                        |
| Perf milestones only                                        | before/after sweep with `scripts/bench-cell.sh NAME RATE reset [pprof]` at 1K and 2K msg/s, never above 2K on dev (owner 2026-10-19: the machine cannot take more; production rates run only on prod-like) (results in `bin/bench/NAME/`). The dev MacBook throttles on heat above 2K and other host load skews latency: record `CPU_Speed_Limit` and corebench pacer lag, and trust only CPU and Redis command counts from a cell where either is off |

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
cp .env.example .env        # MONGO_ROOT_PASSWORD, REDIS_PASSWORD, REDIS_DEDUPE_PASSWORD, REDIS_PRESENCE_PASSWORD: letters, digits, - or _ (spliced unescaped into URIs)
make infra-up               # mongo rs0 :27117, redis state :6380, redis dedupe :6381, redis presence :6382, nats :4223 (monitor :8223); infra-down / infra-reset
make redis-cli ARGS="dbsize"                  # redis-cli inside the container with the secret; INSTANCE=state (default), dedupe or presence
make test                   # go test -race -shuffle=on ./... (unit only, miniredis; no infra)
make go ARGS="test -race -run TestName ./apps/core/internal/message/actor/"   # single test
make vet; make fmt-check; make lint; make vuln; make tidy
make proto; make buf-lint   # Buf v2, proto/chatim/v1 → pkg/pb
make itest                  # all tests with CHATIM_IT_MONGO_URI/REDIS_ADDR/NATS_URL set (real infra)
make image TARGET=apps/core # distroless chatim/<name>:dev for any main package
make core-up; make core-down   # core-1, core-2, websocket-1, websocket-2, gateway-1 (compose profile app); runs dev-keys first
make -s ws-token ARGS="-tenant T -user U"   # dev token (default -ttl 15m) for both websocket nodes and gateway-1; make ws-logs [NODE=2], make gw-logs
make alerts-check           # promtool check rules on deploy/prometheus/alerts.yml (in Docker)
make e2e                    # tools/corecli + scripts/e2e.sh, 12 phases (docs/e2e.md)
make poc TOOL=corebench ARGS="-rate 2000 -duration 60s -watch 20"   # needs core-up; other tools: mongobench, postgresbench, natsbench, wsbench, wsload
```

- Core one-shot commands (`docker exec chatim-core-1 /app resync|recount|indexes …`) and `corecli` subcommands: `docs/apps/core.md`. Indexes change only through `/app indexes` (D191).
- Dev keys, key rotation, `wsload`, `scripts/wsload-kill.sh` and `scripts/ws-nats-drill.sh`: `docs/apps/websocket.md`. Gateway curl example: `docs/apps/gateway.md`.
- `make poc` builds `bin/<tool>` and runs it on the compose network `chatim_default`. `POC_FLAGS` passes extra `docker run` flags. `MONGO_URI`, `PG_URI` and `REDIS_PASSWORD` are built from `.env`, exported by make and passed as bare `-e NAME`, so no password shows up in the echoed command.
- Inside that network, services are reached by name: `chatim-mongodb:27017`, `chatim-redis:6379`, `chatim-redis-dedupe:6379`, `chatim-nats:4222`, `chatim-core-1:9000`.
- Integration tests skip unless the `CHATIM_IT_*` env vars are set. There are no build tags. Unit tests sit next to their code; only whole-core integration tests live apart, in `apps/core/itest`.
- The `apps/core`, `apps/websocket` and `apps/gateway` env vars, their defaults and the ports are listed in README.md.
- Env vars, defaults, boot rules, ports, admin endpoints and metrics of each app: `docs/apps/core.md`, `docs/apps/websocket.md`, `docs/apps/gateway.md`. When a config var changes, update its row in the same commit.

## Code index

`INDEXES.csv` at the repo root lists every Go package, plus the key docs, scripts and deploy files. Its columns are `path,kind,purpose,key_symbols,used_by,tests` (decisions live in `docs/decision-log.md`, not here). Read it before searching the tree.

When you add, remove or rename a package, or change its purpose or key symbols, update its row in the same commit.

## Architecture

There is one module, `github.com/ivannguyendev/chatim`, and one Dockerfile (`deploy/docker/Dockerfile`, `ARG TARGET`).

- Layout (D182): `apps/<app>/` is a runnable program with `main.go` and `internal/` (`apps/core`, `apps/websocket` with `internal/{app,config,session,command}` and `apps/gateway` with `internal/{app,config,api,limit}`, both with no database, never importing `apps/core` or `modules/*`, and gateway never importing `apps/websocket`); `modules/<module>/` is a business module embedded in a program, with one public package and `internal/`. Mention App lives in `modules/mention`, embedded in core: `modules/mention/mentionapp` is the only thing core imports, and only from `apps/core/internal/app` and `config` (depguard `mention-module-entry`); `modules/mention/internal` cannot import `apps/core/internal` (Go `internal/` rule; shared code lives in `pkg/`) and opens only its four collections (`owned_collections_test.go`). A part becomes a module only when it owns collections nobody else writes, depends one way on core, and talks to core through events and a port; core domains fail all three and stay in `apps/core/internal`.
- `apps/core/main.go` only calls `app.Main`; wiring lives in `apps/core/internal/app`, whole-core integration tests in `apps/core/itest` (they start cores with `app.Run`). Packages are grouped by domain (design §6.2). Each domain keeps its use cases at its root: `message.Editor`/`message.Reader`, `interaction.Service`/`interaction.Reader`, `member.Service`/`member.Reader`, `room.Service`; its own subpackages sit under it: `message/` (actor, flush, view), `interaction/` (pinproj), `member/` (ownership, memberwatch), `room/` (roomlist, roomcache). Shared packages hold no use case: `model/` (domain, access, pbconv), `store/` (ports, mongostore, memstore, storetest), `event/` (publish, stateclear, projection, sweep, timer, eventkit, resync), `dedupe/`, `platform/` (slot, redisguard, testlog). `api/grpcsrv` is thin transport (caller, proto ↔ domain, one use case call); `app` wires and `config` loads. depguard in `.golangci.yaml` enforces the direction: domains never import `api`, `app`, `config` or event delivery (only `event/timer`); `member`, `message` and `interaction` import no other domain, except `interaction` → `message`, and `room` imports only `member`; `room/roomlist` and `room/roomcache` import no domain; only the actor imports `message/actor` among domains; event packages import no domain (event tests may build `interaction/pinproj`); only `app` reads `config`. Package names stay short (`actor`, not `sendactor`); a new package goes under the domain it serves, or into a shared group when it has no business logic.
- `pkg/` holds shared code and no business logic (`jsconsume` pull source and Nak ladder, `mongobound` bounded `_id` lookups, `callerid` caller headers, `ids`, `keys`, …); depguard `pkg-shared-only` keeps it from importing `apps/*` or `modules/*`.
- `tools/` holds `corecli`, `corebench` and `tools/internal/route`, a slot-routed gRPC client whose retries keep the same cid.
- Communication is one-way: client ↔ websocket (WebSocket frames, realtime writes and events), client → gateway (HTTPS Connect, reads and room management), websocket → core over gRPC unary by slot (`pkg/coreroute`), gateway → any core over gRPC unary (round robin), core → JetStream, websocket ← NATS (part 2), websocket ↔ core NATS `eph.{t}.{room}.typing` between nodes and websocket ↔ `chatim-redis-presence` (part 4).

**Key encoding (`pkg/keys`).** Big-endian uint64 fields are concatenated, so byte order matches numeric order:

- messages `_id` = `room│thread_root│seq` (24B)
- message_edits = the same plus a uint32 version (28B)
- message_interactions = the message key, a kind byte (1 reaction, 2 bookmark, 3 reply) and a part: the user bytes (1..64B) or the reply's `thread│seq` (16B). Every reply key of a message is 41B, so `keys.InteractionReplyRange` (both bounds 41B) scans one message's replies in seq order; reaction and bookmark keys vary in length, so count and list them through indexes
- mentions = the message key, a target kind byte (1 user, 2 group, 3 `@all`) and the target id (none for `@all`, 25B)
- pin_actions = `room│pin_ver` (16B)
- reply_threads = the parent message key (24B, M3)
- members = `keys.Member(room, user)`: room 8B plus the user bytes (1..64B). Mongo compares BinData by length first, so never range-scan `members` by `_id`; query by room through the `room_id` index, by user with `_id` equality or `mongobound.IDs` (never `_id $in`, D139)
- room_dms = the string `tenant│smaller user│larger user` (`│` is U+2502, outside the id charset; `domain.DirectKey`), also stored as `rooms.dm_key`

These are `_id`s of MongoDB clustered collections (`members` too), so any history page, including the oldest, is one range scan. `thread_root = 0` is the main timeline, seq starts at 1, and `MaxUint64` is reserved.

**Core mechanics (details in `docs/modules/`, `docs/events.md`, `docs/apps/core.md`).**

- Send path (module 02): `grpcsrv` reads tenant and user from metadata `x-chatim-tenant`/`x-chatim-user` (trusted internal callers until mTLS) → `actor.Router` (one goroutine per room) → `dedupe` cid (RAM LRU + dedupe Redis, batched) → `flush.Flusher` (`insertMany` `w:majority`) → ack → event to `publish.Publisher` → cid Commit. Every entry is written once; an uncertain outcome returns `ErrRetryLater` and is remembered by (user, cid); seq contention yields the room. Handlers return domain or `apperr` errors mapped to gRPC codes in `pkg/grpcserver`.
- Slots (module 02): `slot = mix64(room) % 1024` (`pkg/slotmap`), leases in the state Redis by a multi-key Lua script (single primary, not Redis Cluster), `Owns()` per D24. Never `SCAN` or `KEYS`: cid keys fill the keyspace.
- Events (module 10, `docs/events.md`): `_state` bits + `_state_at` on 7 collections set in the same write; the publisher clears them after the PubAck through `event/stateclear`; the slot 0 sweeper republishes what is left; one `projections` consumer (slot 0) feeds `room_activity` and `reply_index`; survival timers on `CHATIM_TIMER`; `/app resync` re-flags. Consumer contract D156: dedupe by event id and doc version, a seq gap → `GetHistory`, never "drop if seq ≤ last seen".
- Domains: edits and deletes as facts in `message_edits` (module 03); reactions, bookmarks, pins (module 04); replies and mention targets (module 06); rooms, DMs, members, owners, read positions (modules 01, 05, 07); every permission through `access.Policy` and the `view` reader pipeline (modules 03, 08).
- Writes made outside core (mongosh, migrations) must set `_state` + `_state_at` themselves (architecture §7.3).
- Metrics are `chatim_core_*`/`chatim_mention_*`/`chatim_websocket_*`/`chatim_gateway_*` on the admin port; 25 alert rules in `deploy/prometheus/alerts.yml`. A new guarantee needs a metric and a rule in the same PR. Boot and stop order, stop budget: architecture §7.2.

**Invariant: any core must handle any room correctly.** Ownership only buys batching, ordering and cache hits. Correctness comes from the unique `_id` acting as CAS, so a Redis failover or a double owner never loses or duplicates a message.

**Shard-readiness rules (design §5.1).** Mongo runs as a replica set without sharding, but sharding must later need configuration only:

- Large collections use a room-prefixed `_id`; the future shard key is `{_id: 1}`. The 2026-10-14 two-shard try showed this keeps a room on one shard only for fixed-length keys (`messages`, `message_edits`, `pin_actions`, `reply_threads`); `members`, `message_interactions` and `mentions` end in a 1–64-byte id and BinData compares length first, so they scatter. Core does not shard (owner 2026-10-17, D161): it keeps these rules so deployers can choose a shard key for their own workload.
- Every query on `messages` carries the room prefix, and `messages` has no secondary index except the partial `state_pending {_state_at: 1, _id: 1}` (only docs with a pending event, usually a few dozen; D148; once sharded the sweeper asks every shard). Small fact collections (`message_edits`, `pin_actions`) may have `{room_id, created_at}` (D70).
- No multi-document transactions, with one exception (D100): commands that touch an owner run one MongoDB transaction over the room's `members` docs and its `rooms` doc (`$inc owners_ver`), once, a conflict returns `UNAVAILABLE`. No `$lookup` into large collections. Bounded point lookups are fine; unbounded joins are not.
- `message_interactions` and `mentions` are clustered with `_id` prefixed by the message key, so one message's interactions and mentions sit on one shard under the future shard key `{_id: 1}`; their user/target/room/resync indexes (`{tenant, actor_id, …}`, `{tenant, target, …}`, `{room_id, kind, …}`, `{message_key}`, `{room_id, kind, updated_at}`) scatter-gather once sharded (D112, D115, replaces `reactions` of D88). `room_dms` gives one DM per user pair through `_id` alone (D117). `mentions` is written by Mention App; `mention_user_groups`/`mention_groups` use clustered string `_id`s (`tenant│user`, `tenant│group`) read through `mongobound.StringIDs` ranges; `mention_rooms` is a plain collection keyed by room id, where `_id $in` is fine.
- `members` is clustered with `_id` = room + user; "rooms of a user" is its `{tenant, user_id, state, room_id, joined_at, cleared_at, read_seq, pinned_at, archived_at}` index (no `user_rooms`, D98), which scatter-gathers once sharded by room.
- Never send `_id $in` (or an `$or` of `_id` equalities, which the planner folds into `$in`) to a clustered collection: Mongo runs it as one scan from the smallest to the largest key, across rooms and key lengths (20 rooms of the bench: 320,040 docs read for 100). Build the filter with `mongobound.IDs` (`$or` of `{_id: {$gte, $lte}}` per key or per run of consecutive keys, equality for one key), put extra predicates inside each branch and never sort on the server (sort in Go); a sort or a predicate outside the `$or` turns it into a `COLLSCAN` (D139). The explain itest `bounded_lookup_integration_test.go` guards every call site.
- The URI, read preference and write concern come from config.
- Collections are created only in the bootstrap step.

**Testing conventions.**

- Packages with goroutines use `testing/synctest` and `goleak`.
- Unit tests use miniredis. Call `testlog.SilenceRedis()` in TestMain when dials fail on purpose.
- No short real-time polls: on the dev VM, CPU clocks skew by up to ~7ms and timers wake up to ~36ms late. Wait on explicit signals instead.
- The actor acks before the cid `Commit` is queued (D59), so a test that reads commits or aborts calls `synctest.Wait()` first (a flaky test from M2c, fixed in M3).

## Docs

- `docs/architecture.md`: the single source of truth: requirements, principles P1–P8, the data-class framework every plan must use (§4), data model and shard rules, package layout, lifecycle, guarantees with detectors (§8).
- `docs/decision-log.md`: decisions D1–D207 with rejected alternatives. Add new decisions there.
- `docs/modules/README.md`: the 11 feature guides (01–11; 09 is websocket with the client spec in §11, 11 is the HTTPS gateway): flows, diagrams, error codes, mechanisms.
- `docs/apps/{core,websocket,gateway}.md`: per-app env vars, ports, admin, metrics and operating commands. `docs/e2e.md`: the 12 e2e phases.
- `docs/events.md`: the event catalog (streams, subjects, ids, consumers, contract). Update it whenever an event, subject or consumer changes.
- `docs/roadmap.md`: milestones, readiness and plan-writing rules (§"Quy tắc viết plan"). `docs/git-workflow.md`: branches, Definition of Done, SemVer.
- `docs/plans/<date>-<milestone>-<n>-<slug>-summary.md`: owner-approved summaries, rewritten as built when a phase closes; they are the milestone record. Detailed execution plans and decision logs live in `.claude/plans/` (local, gitignored) and are folded and deleted at phase close (Workflows step 9); tracked docs never cite them.
- `docs/poc/README.md`: dev results and the prod-like measurement list P1–P10 (dev numbers only validate tools); small result files in `docs/poc/results/`, raw output in `bin/bench/` and `bin/drill/` (gitignored).
- `docs/research/`: the system mechanisms synthesis report and the open-source chat architecture research.
