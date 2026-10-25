---
name: index-changes-by-command
description: "Owner rule - index changes on an existing database run through an explicit one-shot command, never automatically at core startup (bootstrap)"
metadata:
  type: feedback
---

Index changes on an existing database (add a field, replace or drop an index) run through an explicit one-shot command, the same kind as `/app resync` and `/app recount`, with a dry run. They never run automatically in bootstrap at startup. Bootstrap only creates indexes on a fresh database.

**Why:** owner 2026-10-20 rejected the M4 plan step that rebuilt the `members` user index through `bootstrap_replaced_indexes`, with the words "viết cmd để xử index, không chơi tự động bootstrap". An automatic rebuild of a big index at boot is an operation nobody chose to run.

**How to apply:** every plan that changes an index adds or extends the index command: dry run, create the new index next to the old one, drop the old one in a separate step. Bootstrap logs a warning when it finds an outdated index and leaves it alone. Related: [[design-principles]].
