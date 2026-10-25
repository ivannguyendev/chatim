---
name: no-shared-tables-across-services
description: Never propose a separate service that reads another service's tables (a distributed monolith); modular monolith by default, and a split service owns its data and talks only through APIs
metadata:
  type: feedback
---

On 2026-10-19 I proposed a "hybrid" Mention App split. It read core's Mongo tables directly for raw data and called core gRPC for the rule-bearing reads. When the owner asked me to check the enterprise rules (`.claude/skills/go-lang/playbooks/architecture-and-layout.md`), the proposal turned out to be a distributed monolith, and the owner kept the app embedded (D177).

**Why:**
- The rule defaults to a modular monolith while there is one team and one release cadence.
- A split is justified only for its own scaling need, release cadence, SLA, store or team.
- Shared tables still force the two services to deploy together: a field rename in core breaks the other service. The split then adds operations work with no freedom gained.

**How to apply:**
- Before proposing any service split, check the split triggers above against the facts and name the one that holds.
- If none holds, keep the module in-process behind a port.
- A split service owns its data and reaches other services only through versioned APIs, with a breaker and a timeout.
- Check proposals against the go-lang skill's architecture rules before presenting them, not after the owner asks.

Related: [[mention-app-role]], [[design-principles]].
