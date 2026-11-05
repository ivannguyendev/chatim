---
name: owner-communication
description: How to brainstorm, ask and write for the chatim owner beyond the CLAUDE.md Design checklist - scope by whose data, mechanisms as stories with real rows, decide details myself, walk-through first, plainest version first
metadata:
  type: feedback
---

1. Classify each scope row by whose data it is, never by who calls it: an API that reads or writes chat collections is chat even if only one app calls it; only the logic that combines it (ordering, unread, sync, cache) belongs to the app.
2. Tell each mechanism as a short story with real numbers and sample rows of the real tables (e.g. ws-2 gets 1002 then 1500, so it asks for 1003–1499 back). No invented terms, no short field names. Explain a database feature as a plain story and say when it reuses an existing mechanism rather than adding a new one.
3. Decide mechanisms and technical details myself and present them for critique; ask only what changes the design. Never ask about client behaviour (reconnect waits, token renew, SDK logic): write it into the client spec, module 09 §11.
4. Start a milestone with a walk-through of what it does as a story before asking anything.
5. Owner summaries stay around 130 lines: what the part does as an example, short tables, decisions/risks/tests in a few lines; config tables, thresholds and edge cases go in the plan.
6. Build the plainest version first; add an optimisation only with a measured number and the owner's go. Flag anything that serialises work. A rejected option gets its concrete failure with numbers.
7. Keep "problems of the current code" visibly separate from "the fix". Never fold a decision that belongs to another app into a design without asking explicitly.

**Why:** repeated owner corrections from M2b to M6: summaries too long and patched, questions not understood, extra mechanisms bringing new bugs, scope split by caller instead of data.
**How to apply:** check every brainstorm, summary and question against this list and the CLAUDE.md Design checklist before sending. Related: [[milestone-keeps-its-own-scope]], [[ops-setup-hard-to-get-wrong]].
