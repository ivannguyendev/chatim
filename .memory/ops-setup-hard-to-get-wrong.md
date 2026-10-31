---
name: ops-setup-hard-to-get-wrong
description: "Settings that must agree across apps or timers that depend on each other are checked in code (refuse to boot / not ready), never only documented; operational procedures such as key rotation are one command"
metadata:
  type: feedback
---

The owner wants setup and operations that are hard to get wrong. After e2e phase 14 of M6 part 2 failed three times (2026-10-30/31), twice because key rotation depended on three timers in three apps (`IAM_KEY_RELOAD`, `*_IAM_KEYS_INTERVAL`, an operator-chosen wait before `key-activate`) that nothing checked, the owner asked "vận hành có dễ lỗi thế này ko? … setup mà phức tạp và dễ sai thế này thì vận hành khó lắm" and approved making rotation a single `key-rotate` with iam activating the key itself, checking the timer relations at boot, and keeping a node not ready when iam refuses it (D224, D225).

**Why:** a coupling that only lives in the docs fails silently in production (a wrong node secret turned revocation off with only a metric to show it); the owner reads such setups as an operations risk, not a docs task.

**How to apply:** in every design, list the knobs that must agree across apps and the timers that depend on each other; for each, remove the coupling, or enforce it in code (refuse to boot, stay not ready with a clear log, metric and alert). Keep the operator checklist to a few lines with every other value defaulted. Run a new e2e phase alone (`E2E_ONLY`) before the full suite. This is my judgment, not a written rule: on 2026-10-31 the owner declined adding it to the CLAUDE.md checklist. Related: [[design-principles]], [[owner-communication]].
