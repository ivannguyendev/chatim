---
name: ops-setup-hard-to-get-wrong
description: Settings that must agree across apps and timers that depend on each other are enforced in code (refuse to boot, stay not ready), never only documented; multi-step procedures become one command
metadata:
  type: feedback
---

The owner treats a setup that is easy to get wrong as an operations risk, not a docs task. In M6 part 2, key rotation depended on three timers in three apps that nothing checked, and e2e kept failing until rotation became one command with the timer relations checked at boot (D224, D225).

**How to apply:** in every design, list the knobs that must agree across apps and the timers that depend on each other; remove each coupling or enforce it in code (refuse to boot, or stay not ready with a clear log, metric and alert). Keep the operator checklist to a few lines with every other value defaulted; make multi-step procedures a single command. The owner declined adding this to the CLAUDE.md checklist, so it stays a judgment kept here. Related: [[owner-communication]].
