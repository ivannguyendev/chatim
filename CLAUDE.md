# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

chatim is an internal, logically multi-tenant chat platform (CPaaS) in Go. Read `README.md` for the apps and their boundaries and `docs/roadmap.md` for status. Project docs are written in Vietnamese.

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

- Open every milestone plan with a scope and boundary table: what is in scope and which service owns it, what is out and where it belongs. Chat offers data APIs and events; avoid or limit N+1 (prefer loading a screen's data in few calls over one call per item); values per viewer computed at read time (unread, sorted conversation list) are chat since 2026-10-13; stored per-viewer state and sync belong to an app.
- Map each feature to a data class of design §4 (one write, event and repair path per class). Never design a per-feature patch; a feature that fits no class needs a new class first.
- A derived value written inline (counter, projection, summary) is a direct write or `$inc` wrapped in the survival timer (design §7.1, D111); repair = recount + CAS on the version. Never recount on every change.
- No internal retries: a CAS, duplicate-key or transaction conflict returns `ErrRetryLater` (`UNAVAILABLE`) at once; consumers Nak; a failed publish leaves the `_state` flag for the sweeper; transactions run one attempt.
- No data limit without a use case or an existing requirement (A6). A page size is pagination, not a limit.
- Service-to-service HTTP answers at once over one kept-alive connection; never hold a request (no long polling, no SSE) (owner 2026-11-02).
- Every change sets its `_state` bit in the same write, publishes its event inline and clears the bit after the PubAck; the sweeper republishes what is left (a new event needs a bit, a `StateTargetsOf` case and a sweep rebuilder); subjects follow the data kind (`room`/`member`/`message`); chat never picks recipients and never expands mention groups.
- Shard-ready: room-prefixed clustered `_id`; no secondary index on `messages` except the partial `state_pending` index (D148); uniqueness only through `_id` (claim docs such as `room_dms`), never a unique secondary index; no transaction except D100.
- Field names: full basic English words except `messages`; counters end in `_ver`; time fields end in `_at` (owner 2026-11-02, e.g. `expires_at`, never `until`); `updated_at`/`updated_by`; `request_id`.
- Owner summaries: Vietnamese, plain words with examples, written from the final state (rewrite after a review round, never append patches). Risks list only real consequences of this plan, not other systems' operations and not mechanisms already accepted.
- Ask the owner only pivotal questions, in plain words with an example; never re-ask an answered one.
- A decision-log row that starts with `**Thay bởi …**` is a dropped design: never propose it again unless the owner asks.
- Identity: chat owns no identity (no passwords, login, sessions, SSO). It keeps per-tenant trusted issuers, a user mirror, groups and revocation, and exchanges a JWT signed by the tenant's backend or IdP for its own chat token. The tenant always comes from the issuer config, never from a token claim. Never write plugins for an external IAM. Tenant-facing APIs (batch user/group API, revoke-before, external IAM, SCIM) are built when a real tenant integrates, so its needs shape them (D260).

## Workflows

Mandatory for every milestone, phase or other large task; format per `docs/roadmap.md` §"Quy tắc viết plan". This file changes only when a rule changes (no status here).

1. **Brainstorm** (skill `brainstorming`): read `README.md`, this file, the `.memory/` index, `INDEXES.csv`, related docs; requirements table with sources, then the recommendation. Every claim cites a source (design §2, D-id, owner answer with date, `path:line`, command or test output, `docs/poc/results/` or `bin/bench/`); none → write "chưa có nguồn" and ask. With no requirement, decision or summary covering the work, stop and present the purpose and component split first.
2. **Proposed decisions** on the owner's go: `.claude/plans/<feature>_design.md` (local, gitignored, kept until step 9).
3. **Phase split**: an independent reviewer (never saw my reasoning) reads it and proposes phases (goal, deliverable testable alone, dependencies, requirements covered); the owner approves; the list goes into the milestone row of `docs/roadmap.md`. A phase is a part on `feat/<milestone>`.
4. **Summary for one phase**: `docs/plans/<date>-<milestone>-<n>-<slug>-summary.md` with the coverage table (requirement → phase/component → task → test → evidence after execution; an empty cell blocks approval; both reviewers check it); the owner approves.
5. **Detailed plan** for that phase: `.claude/plans/<plan>.md`, coverage table, no code.
6. **Plan review**: an independent reviewer judges enterprise fitness and the coverage table; fold in Critical/Important findings, report the verdict; code only after the owner's go.
7. **Code with TDD**; review per the budget below (overrides the global "review after every implementation").
8. **Verify** per the budget; "done"/"green" only next to command output.
9. **Close-out**: (a) Docs: rewrite the summary as built; fold only accepted decisions meeting the decision-log criteria (§Docs) into the milestone's file in `docs/decisions/` (work-process choices go to the summary); Minor findings → summary or roadmap "Mục mang sang M5"; delete the local files (tracked docs never cite `.claude/plans/`); update `INDEXES.csv`. (b) Rules and memory: note owner corrections in the local decision log when they happen; at close add owner-confirmed rules here, update or delete superseded ones, or say "không có rule mới"; propose memory drafts per Repo Memory. (c) Progress: update the roadmap milestone row; end the report with the phase just finished (evidence), the next phase (goal, first step) and the remaining phases with status.

## Repo Memory

`.memory/` (committed; index `.memory/README.md`) holds the why behind rules and decisions. Read the index before any brainstorm, summary or plan and open only the notes that apply.

- Gate: any create, update or delete in `.memory/` (index included) needs the owner's approval of the exact draft (file name + full content, or the file to delete), given once for all drafts at the phase close. An answer to another question, a general "ok" or silence is not approval.
- When: only at Close-out (Workflows step 9).
- Criteria: write a note only when all hold: the owner confirmed it; it stays true after the task; it is not already in this file, `docs/`, code or git; a future agent would act differently without it. Never store status, proposals, open questions, commit hashes, code or owner quotes.
- Where else: rules → this file; decisions and their reasons → `docs/decisions/`; requirements and specs → `docs/`; phase scope and status → roadmap and summaries; drafts → `.claude/plans/`.
- Format: one fact per note, frontmatter `name`, `description`, `metadata.type`, one line in the index; update a note instead of adding a duplicate. At the close, propose deleting notes a D-id or this file now covers.

## Verification and review budget

The user finds long review and test loops slow: run the cheapest check that proves the change; escalate only for the area that changed.

- While coding: `make -s go ARGS="test -race -shuffle=on ./<touched pkgs>/..."`. Before each commit: `make fmt-check`, `vet`, `lint` and the touched packages' tests.
- New goroutines, channels or locks: `-count=5` on that package only.
- `apps/chat/internal/platform/slot` or `pkg/slotmap` changed: R5 once at task end, `make go ARGS="test -race -count=20 -timeout 20m ./apps/chat/internal/platform/slot/"` (~9 min).
- Mongo/Redis/NATS adapters or `apps/chat` wiring changed: `make itest` once (needs `make infra-up`).
- Milestone end only: `make test`, `make itest`, `make app-up && make e2e`, chatbench.
- Perf milestones only: before/after `scripts/bench-cell.sh NAME RATE reset [pprof]` at 1K and 2K msg/s, never above 2K on dev (owner 2026-10-19; production rates only on prod-like; results in `bin/bench/NAME/`). The dev MacBook throttles above 2K and host load skews latency: record `CPU_Speed_Limit` and chatbench pacer lag, and trust only CPU and Redis command counts from a cell where either is off.
- Risky tasks (write path, dedupe, publish, slots, lifecycle) get one reviewer: spec and quality in one pass over the diff, no re-run of suites already green (at most `-count=3` on touched packages). Low-risk tasks (docs, config, compose, CLI, PoC tools): one quick check by the controller, no reviewer subagent.
- Findings: fix Critical and Important in one follow-up commit; re-review only a Critical fix's diff; Minor findings go into the plan or docs, no new loop.
- No mutation testing, repeated stress runs or benchmark re-runs unless asked. Review task N while task N+1 is implemented when they touch different files.
- Report in a few lines; if nothing changed since the last green run, don't re-run the whole repo.

## Git workflow

Full rules (Definition of Done, readiness, SemVer tags, broken `main`): `docs/git-workflow.md`.

- One branch per milestone, `feat/<milestone>` from `main` (standard, do not ask again), every part pushed there with no PR per part; the PR description is drafted in `bin/<branch>-pr.md` and the owner opens and merges one PR. `fix/<slug>` for stray fixes, `docs/<slug>` for docs outside a milestone; no long-lived `prod`, `develop` or `release` branches.
- `main` holds green `dev-done` work, merged by PR with a merge commit, only when the Definition of Done holds. Never commit directly to, force-push or rewrite `main`.
- CI job `checks` (`.github/workflows/ci.yml`) runs `fmt-check`, `vet`, `lint`, `test` on every PR to `main` (required); `itest` and `e2e` run on the dev machine.

## Commands

Run from the repo root; `make -s …` hides the echoed `docker run` line. Ports and the app list: `README.md`.

```bash
cp .env.example .env        # passwords: letters, digits, - or _ (spliced unescaped into URIs)
make infra-up               # mongo, 3 redis, nats; infra-down / infra-reset
make test                   # unit only, miniredis, no infra
make go ARGS="test -race -run TestName ./apps/chat/internal/message/actor/"
make vet; make fmt-check; make lint; make vuln; make tidy; make proto; make buf-lint
make itest                  # real infra; one area: ITEST_ARGS="-race -count=1 ./apps/identity/..." ($ written $$)
make app-up; make app-down  # all apps, compose profile app
make e2e                    # docs/e2e.md; E2E_ONLY=<phase> runs one identity phase
make -s ws-token ARGS="-tenant T -user U"; make identity-admin ARGS="…"; make redis-cli INSTANCE=dedupe ARGS="dbsize"
make image TARGET=apps/chat   # distroless chatim/<name>:dev; make alerts-check (promtool)
make poc TOOL=chatbench ARGS="-rate 2000 -duration 60s -watch 20"   # needs app-up; also mongobench, natsbench, wsbench, wsload, exchangebench
```

- `make poc` runs `bin/<tool>` on the compose network `chatim_default` (`POC_FLAGS` adds `docker run` flags; `MONGO_URI`, `REDIS_PASSWORD` passed as bare `-e NAME`, so no password is echoed). Services there: `chatim-mongodb:27017`, `chatim-redis:6379`, `chatim-redis-dedupe:6379`, `chatim-nats:4222`, `chatim-chat-1:9000`.
- Integration tests skip unless `CHATIM_IT_*` is set (no build tags). Unit tests sit next to their code; whole-chat integration tests live in `apps/chat/itest`.
- Indexes change only through `/app indexes` (D191). Per-app env vars, ports, admin, metrics and commands (`docker exec chatim-chat-1 /app resync|recount|indexes …`, `chatcli`, dev keys, `wsload`, drills, `identity-admin`): `docs/apps/{chat,websocket,gateway,identity}.md`; when a config var changes, update its row in the same commit.

## Code index

`INDEXES.csv` at the repo root is the entry point for searching the tree; read it first. Columns `path,kind,purpose,key_symbols,used_by,tests`.

- Scope: one row per Go package, plus the official docs, scripts, configs and deploy files useful as retrieval context. Never index plans, drafts, generated code (`pkg/pb` is one row), files always loaded (`CLAUDE.md`) or single summaries (`docs/plans` is one row).
- `purpose`: at most ~300 characters, present tense: what it owns or does and when to open it. No history, status, task or phase ids, env or metric lists, counts that drift, or line numbers.
- `key_symbols`: at most ~15 main symbols; `used_by`: main importers; `tests`: kinds only (`unit`, `itest`, `e2e`, `none`, `synctest`, `goleak`).
- When you add, remove or rename a package or file in scope, or change its purpose or key symbols, update its row in the same commit, written from the current code.

## Architecture

Reference (keys, collections, shard rationale, package tree, lifecycle, metrics): `docs/architecture.md` §5–§8, `docs/modules/`, `docs/events.md`.

- One Go module `github.com/ivannguyendev/chatim`, one Dockerfile `deploy/docker/Dockerfile` (`ARG TARGET`).
- Layout (D182): `apps/<app>/` = program (`main.go` + `internal/`); `modules/<module>/` = embedded business module (one public package + `internal/`), only when it owns collections nobody else writes, depends one way on chat and talks to it through events and a port; chat domains stay in `apps/chat/internal`.
- Imports (depguard, `.golangci.yaml`): websocket and gateway have no database and never import `apps/chat` or `modules/*`; gateway never imports `apps/websocket`; `apps/identity` (own database `chatim_identity`) imports no other app or module and none imports it (`identity-boundaries`); chat imports only `modules/mention/mentionapp`, only from `apps/chat/internal/app` and `config` (`mention-module-entry`); `modules/mention/internal` never imports `apps/chat/internal` and opens only its own collections; `pkg/` has no business logic and never imports `apps/*` or `modules/*` (`pkg-shared-only`); `pkg/wstest` is for tests, e2e and `wsload` only (`wstest-tests-only`), never an SDK (D206).
- `apps/chat`: `apps/chat/main.go` only calls `app.Main`; only `app` reads `config`; `api/grpcsrv` is thin transport (caller, proto ↔ domain, one use case call); use cases sit at each domain root, shared packages hold none. Domains never import `api`, `app`, `config` or event delivery (only `event/timer`); `member`, `message`, `interaction` import no other domain except `interaction` → `message`; `room` imports only `member`; `room/roomlist`, `room/roomcache` import no domain; only the actor imports `message/actor`; event packages import no domain (event tests may build `interaction/pinproj`). Short package names (`actor`, not `sendactor`); a new package goes under its domain, or a shared group if it has no business logic.
- One-way communication: client ↔ websocket (frames, realtime writes, events); client → gateway (HTTPS Connect, reads, room management); websocket → chat gRPC unary by slot (`pkg/chatrpc`); gateway → any live chat node from the Redis node table (`Router.Any`, no slot); chat → JetStream → websocket; websocket ↔ core NATS `eph.{t}.{room}.typing` and `chatim-redis-presence`; websocket/gateway → `identity` HTTPS for the revocation list (internal port, shared via `chatim-redis-presence`) and JWKS (public port).
- **Invariant: any chat node must handle any room correctly.** Ownership only buys batching, ordering and cache hits; correctness comes from the unique `_id` acting as CAS, so a Redis failover or a double owner never loses or duplicates a message.
- Handlers return domain or `apperr` errors, mapped to gRPC codes in `pkg/core/grpcserver`. Every permission goes through `access.Policy` and the `view` reader pipeline. Never `SCAN` or `KEYS` Redis (cid keys fill the keyspace). Writes outside chat (mongosh, migrations) set `_state` + `_state_at` themselves (design §7.3). A new guarantee needs a metric and an alert rule (`deploy/prometheus/alerts.yml`) in the same PR.
- Shard-ready beyond the checklist (design §5.2; chat does not shard, D161, but sharding must need configuration only): every `messages` query carries the room prefix; never send `_id $in` or an `$or` of `_id` equalities to a clustered collection, use `mongobound.IDs`/`StringIDs`, predicates inside each branch, sort in Go (D139); never range-scan `members` by `_id`; no `$lookup` into large collections; URI, read preference and write concern from config; collections created only in bootstrap.
- Tests: goroutine packages use `testing/synctest` and `goleak`; miniredis for unit tests (`testlog.SilenceRedis()` in TestMain when dials fail on purpose); no short real-time polls (dev VM clocks skew ~7ms, timers wake ~36ms late), wait on explicit signals; the actor acks before the cid `Commit` is queued (D59), so a test reading commits or aborts calls `synctest.Wait()` first.

## Docs

- `docs/architecture.md`: the single source of truth: requirements and load numbers (§2), principles, data classes every plan must use (§4), data model, keys and shard rules (§5), layout (§6), lifecycle (§7), guarantees with detectors (§8).
- `docs/decision-log.md` is the index; accepted decisions live in `docs/decisions/`, one file per milestone named `D<first>-<milestone>.md` (3 digits, e.g. `D209-m6-identity.md`); the index lists each file's D-id range, sections and dates, so a phase close adds or updates its index row. D-ids are global; find one through the index or `grep '| D161 |' docs/decisions/`. Rows `| # | Quyết định | Lý do |`, date in the section heading. The index header holds only the update date, never a changelog.
  - A row is a decision that is hard or costly to reverse and shapes the project's direction: data model, contracts, events and APIs, app and module boundaries, correctness mechanisms, operating procedures, product scope. Not a row: how we work (phases, order, branches, PRs), implementation detail (package lists, env, metrics), status.
  - Lý do says why this choice over the alternatives, naming them; there is no rejected-alternatives column. No owner quotes.
  - Proposed decisions live in the local decision log and are discussed (brainstorm, independent reviewer) before the owner accepts them; only then are they folded in. A decision taken while coding goes into the as-built summary and enters the log only when the owner accepts it at the close. Nobody adds a row on their own. A replaced row starts its Quyết định with `**Thay bởi D141 (M3.1).**` or `**Thay một phần bởi D202 (vế ngắn).**`. Add new decisions there.
- `docs/modules/`: feature guides (09 = websocket, client spec in §11; 11 = HTTPS gateway). `docs/apps/*.md`: per-app operations. `docs/e2e.md`: e2e phases.
- `docs/events.md`: the event catalog (streams, subjects, ids, consumers, contract); update it whenever an event, subject or consumer changes.
- `docs/roadmap.md`: milestones, status, plan rules. `docs/git-workflow.md`: branches, DoD, readiness, SemVer.
- `docs/plans/<date>-<milestone>-<n>-<slug>-summary.md`: owner-approved summaries, rewritten as built at phase close; they are the milestone record. `.claude/plans/` (local, gitignored) is folded and deleted at close.
- `docs/poc/README.md`: dev results (they only validate tools) and the prod-like measurement list; small results in `docs/poc/results/`, raw output in `bin/bench/`, `bin/drill/` (gitignored). `docs/research/`: mechanisms synthesis and open-source chat research.
