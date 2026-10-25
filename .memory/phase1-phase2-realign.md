---
name: phase1-phase2-realign
description: Owner decisions of 2026-10-19 (D177–D181): Phase 1 exit = prod-like validated, chatim owns accounts, migrator runs dual on dev with real data, dev load 10K; real numbers 5,000 MarkRead/s, 5% edits, 1,000 active rooms
metadata:
  type: project
---

The owner approved `docs/plans/2026-10-19-phase1-exit-summary.md` on 2026-10-19. The decisions are D177–D181 in `docs/decision-log.md` §17.5. The RFC and the critique round are archived in `.claude/plans/archive/phase1-phase2-realign/`.

- **Phase 1 exit.** `core` (Mention App embedded) + `websocket` reach `prod-like validated`. Go-live comes after `identity`.
- **Order.** M4 → prod-like PoC → M5. Mention App stays embedded (D177).
- **Accounts.** Chatim owns them (`identity` + `identity-verifier`), and never hands user ownership to another party.
- **Migrator.** It runs dual with real data, on dev too.
- **Load tests.** Dev benches stop at 2K/s (the machine cannot take more) and dev load tests at 10K connections. Prod-like runs 100K connections and the real rates.
- **PoC data.** It comes from the existing bench tools.
- **Real numbers.** ~5,000 `MarkRead`/s, 5% edits, 1,000 active rooms.
- **Still open.** The date the prod-like hardware is ready.

**Why:** these are the owner's own answers. Do not re-ask them.

**How to apply:**
- Never ask for a dev bench above 2K/s or a dev load test above 10K connections.
- Never propose an outside IdP as the owner of users.
- 5,000 `MarkRead`/s is a production rate: run it only on prod-like, together with the send load.

Related: [[mention-app-role]].
