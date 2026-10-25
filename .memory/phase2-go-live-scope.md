---
name: phase2-go-live-scope
description: Owner 2026-10-25 (D207) - go-live v1.0.0 is for a new product (no migration) and needs identity, media, push and people/group search; Phase 2 order M6 identity, M7 media, M8 notification, M9 search
metadata:
  type: project
---

On 2026-10-25 the owner answered two questions in the Phase 2 brainstorm:
- The first go-live users are a **new product**, not users moved from an old system. The migrator moves after go-live, and identity does not import old users.
- Go-live `v1.0.0` must have **identity, push when offline, media (images/files) and people/group search**. The API for other systems + webhooks is not required for go-live.

The order and the reasons are in D207 (`docs/decision-log.md` §17.13) and the local log `.claude/plans/phase2-roadmap_design.md`: PR M4 → M6 identity → M7 media → M8 notification + push → M9 search people/groups → go-live.

**Why:** the owner's own priorities. Do not re-ask them.

**How to apply:**
- Never put the migrator or webhooks on the go-live path unless the owner changes this.
- Start each Phase 2 milestone with its own brainstorm.
- At M8, ask the owner who picks push recipients for plain messages (D104). It is still open.

Related: [[phase1-phase2-realign]], [[no-shared-tables-across-services]].
