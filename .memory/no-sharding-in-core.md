---
name: no-sharding-in-core
description: "Owner decided core does not shard; keep shard-ready rules, deployers choose the shard key for their own workload (D161, 2026-10-17)"
metadata:
  type: project
---

The owner has decided that chatim core does not do sharding. This was confirmed on 2026-10-17 and recorded as D161. Never list sharding as an open question or propose a sharding milestone.

Core still keeps the shard-ready rules:
- room-prefixed clustered `_id`;
- a room prefix on every `messages` query;
- no secondary index on `messages` except `state_pending`;
- bounded `_id` lookups (D139);
- no transactions except D100.

When a deployment needs shards, its own developers choose the shard key and configure it to fit their business. The 2-shard numbers in `docs/plans/2026-10-15-m3-open-topics.md` §2 stay only as reference for them.

**Why:** the shard key depends on each deployment's workload and infrastructure. Core only has to avoid blocking that path.

**How to apply:**
- Do not raise sharding in "what's next" lists or owner questions.
- In designs, check only that the shard-ready rules still hold.

Related: [[design-principles]].
