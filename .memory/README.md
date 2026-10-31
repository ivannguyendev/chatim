# Memory

Owner-confirmed rules, reasons and decisions for chatim, one fact per file. Read this index before any brainstorm, summary or plan, after the Design checklist and §Workflows of `CLAUDE.md`. Each file is a snapshot: verify a named file, flag or behaviour against the code before relying on it.

## How we work

- [workflow-pipeline](workflow-pipeline.md) — brainstorm → decision log → independent phase-split review → summary and detailed plan per phase (format, ~130-line summary, code-free plan) → plan review → TDD → verify → close the phase with a progress report; fold local plans and decision logs into tracked docs, then delete them
- [rule-retrospective](rule-retrospective.md) — after each phase/milestone and each owner correction: add owner-confirmed rules to CLAUDE.md, reasons here, fix or delete stale ones
- [milestone-keeps-its-own-scope](milestone-keeps-its-own-scope.md) — a milestone finishes its own system; integrating into another module (iam → Mention) waits until it is done, never reopens that module's problems (D234, 2026-10-31)
- [owner-communication](owner-communication.md) — requirements and scope table first, plain Vietnamese with an example, only pivotal questions, rewrite from the final state, real risks only, short summaries, walk-through before asking, failures with numbers, plainest version first

## Design

- [design-principles](design-principles.md) — data classes not patches; state-driven `_state` events (effect engine dropped); survival timer; no internal retries; no limits without a use case; publish every change; mentions per target; shard-ready; full field names; self-expiring state per node
- [no-sharding-in-core](no-sharding-in-core.md) — core never shards; keep the shard-ready rules; deployers pick the shard key (D161)
- [no-shared-tables-across-services](no-shared-tables-across-services.md) — never a separate service reading another service's tables; modular monolith by default
- [index-changes-by-command](index-changes-by-command.md) — index changes on an existing DB only through an explicit command with dry run
- [ops-setup-hard-to-get-wrong](ops-setup-hard-to-get-wrong.md) — cross-app settings and dependent timers are enforced in code (refuse to boot / not ready), never only documented; procedures like key rotation are one command (M6 part 2, 2026-10-31)
- [cache-follows-db](cache-follows-db.md) — server caches change with the database (same-transaction signal, never TTL); design for load, client caching is not ours; boot warm-ups must not storm on K8s restarts (M6 part 3a, 2026-10-31)

## Owner decisions by area

Decisions of a built milestone live in `docs/decision-log.md`, `docs/events.md` and `docs/modules/`; keep a note here only while it is not yet in those docs or when it carries a reason they do not.

- [phase1-phase2-realign](phase1-phase2-realign.md) — Phase 1 exit = prod-like validated; dev bench ≤ 2K/s, dev load 10K; real numbers 5,000 MarkRead/s, 5% edits, 1,000 active rooms (D177–D181)
- [phase2-go-live-scope](phase2-go-live-scope.md) — go-live v1.0.0 for a new product: iam (was identity, D209), media, push, people/group search; order M6 → M9; migrator and webhooks after go-live (D207)
- [mention-app-role](mention-app-role.md) — Mention App in `modules/mention`, embedded in core; owns groups; own filtered consumer; notifications as `WsPublish`; mute is the notification app's
- [server-spec-client-owns-behavior](server-spec-client-owns-behavior.md) — repo is server-only; the server specifies and enforces, client behaviour belongs to the client repo

## Adding a memory

One file per fact, kebab-case name, frontmatter `name`, `description`, `metadata.type` (`user`, `feedback`, `project`, `reference`). For feedback and project notes add **Why:** and **How to apply:**. Link related notes with `[[name]]`. Add one line here. Save only what the owner stated or confirmed; update an existing file instead of adding a duplicate, and delete notes that turn out wrong. The rule text itself belongs in `CLAUDE.md`; this folder keeps the reason, the owner's words and the date.
