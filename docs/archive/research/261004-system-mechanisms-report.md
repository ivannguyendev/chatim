# chatim: System Mechanisms Report for External Review

Date: 2026-10-04. Status: for review. The authors' working documents are in Vietnamese; this report is a self-contained English summary written for reviewers who will not read code or those documents.

## 0. Purpose and how to read this report

**What we ask of you.** Review the design at the level of mechanisms. We would like to know:
- whether the principles are sound;
- whether each built mechanism delivers what it claims;
- whether the proposed mechanisms are the right shape;
- what we have missed.

Section 9 lists specific questions. Code is out of scope.

**Status legend** used throughout:

| Label | Meaning |
|---|---|
| **Built** | Implemented, tested and running on the development environment. Not yet validated on production-like hardware |
| **Partial** | Some of the mechanism exists; the general form does not |
| **Proposed** | Draft design written on 2026-10-04; **not yet approved** by the owner |
| **Missing** | Known need, no design yet |
| **Agreed, not built** | Owner-approved decision with no implementation yet |
| **Proposed to change** | An agreed decision that the 2026-10-04 proposals would replace; not approved |
| **Not started / Later** | Application or feature planned for a later milestone |

Alerts mentioned in this report (lost history, degraded modes, lag) **are not configured yet**; they are planned for the hardening milestone.

**About numbers.** All measurements come from one developer laptop running every service in containers, with a one-node database replica set. They show that the tools and mechanisms work, and how one change compares with another. They are **not** evidence of production performance. Production-like runs are planned but not done.

**Decision ids.** The project keeps a decision log. Ids such as D47 appear in parentheses so you can see which decision a statement comes from. Appendix B lists them.

---

## 1. The system at a glance

chatim is an **internal, logically multi-tenant chat platform**: a CPaaS (Communications Platform as a Service). Several company products use it to embed chat. Each product is a *tenant*: data is isolated by tenant id, but all tenants share one deployment.

**Scope of phase 1:**
- Rooms: direct messages (1:1), groups (up to 5K members) and channels (about 200K subscribers, only admins post).
- Messages: send, edit (with edit history), delete (for everyone or for me), reply, forward.
- Interactions: reactions, read receipts and unread counts, typing, presence, threads, mentions, pins, bookmarks.
- An internal event stream for other company apps.

**Targets:**
- About 100K concurrent connections.
- 5–10K messages/s at peak.
- 5–20 billion stored messages after two years.

**Applications:**

| App | Role | Status |
|---|---|---|
| `core` | Rooms, members, messages; assigns message sequence numbers; stores data; publishes events | Built (send, history); mutations on hold |
| `gateway` | WebSocket connections to client SDKs; pushes events in real time | Not started |
| Later apps (`auth`, `api`, `events`, `push`, `migrator`, notification, system messages) | Tokens, public API, external event stream, push notifications, migration from the legacy store, message delivery logic, system messages | Later |

**Infrastructure:**
- **MongoDB** replica set: 3 members in production, no sharding. The code must stay ready for sharding.
- **Two Redis instances:**
  - "state", for core coordination;
  - "dedupe", for duplicate detection and event bookkeeping.
- **NATS JetStream:** a persistent message log with a built-in duplicate filter, used as the event bus.

**Data flow:**

```
 Client SDK ──WebSocket──► gateway ──gRPC──► core ──insert (majority)──► MongoDB
                              ▲               │
                              │               └──publish──► JetStream stream ──republish──► live subjects
                              └────────────── subscribe live subjects (per room) ◄──────────────┘
```

Communication is one way: gateway → core by request/response, core → event stream, gateway ← event stream. Core never calls the gateway.

**Milestones:**
- **Done on dev:** foundation, the proof-of-concept benchmarks, and core send/history with the reliability work around it (M2a, M2a.1, M2a.2, M2a.3).
- **On hold:** the next milestone (M2b: edit, delete, reactions, pins, read receipts). The owner asked for this system-level review first.

---

## 2. Requirements and assumptions

### 2.1 Non-functional targets

| # | Target |
|---|---|
| A1 | p99 latency: acknowledgement ≤ 30ms; delivery to an online recipient ≤ 100ms; a page of 50 messages at **any** position (including the oldest) ≤ 20ms |
| A2 | "Acknowledged" means durably written with majority write concern. Message order inside a timeline is the same for everyone. Events are best-effort but carry natural ids so receivers can drop duplicates; a reconciliation process makes up for lost events |
| A3 | 99.95% availability, no single point of failure: at least 2 cores, 2–3 gateways, 3-member MongoDB replica set, 3 NATS nodes, Redis with Sentinel |
| A4 | Messages and edit history kept forever; the event stream keeps 7 days |
| A5 | Tokens issued by a separate auth app; mutual TLS inside the platform; no end-to-end encryption |
| A6 | Text ≤ 16KB, metadata ≤ 8KB, ≤ 50 mentions, ≤ 10 attachment references |
| A7 | Edit history readable by members who can read the room; a tenant may disable it |

### 2.2 Requirements agreed with the owner that shaped the design

The R-numbers come from the authors' requirement table; only the ones that shaped the design are listed.

The original design required a gap-free, room-wide event counter. Every attempt to keep that counter correct with two writers failed review, so the owner went back to the requirements (2026-10-03):

| # | Requirement |
|---|---|
| R5 | Message order only needs to be **consistent for everyone**. No causal or commit-order guarantee |
| R6/R7 | The system emits a message's creation event before any event about changes to it. Arrival order at receivers is not controlled |
| R10/R15 | Clients do **not** detect missing events by counting. On connect or reconnect they load the latest state (room list and latest page). Message records always hold their current state |
| R12 | Events are best-effort. A periodic reconciliation module makes up for losses |
| R17 | Unread counts are exact but capped at "99+". They count only messages from others that are not deleted, not hidden, and whose kind is flagged as counting. "Last sequence minus read sequence" is wrong and will not be used |
| R23 | System messages (calls, join/leave, pin notices) live with ordinary messages, with a kind and a per-message "counts as unread" flag |
| R26 | Sequence assignment with a uniqueness check (D13) stays, because it works, not because it is a requirement |

### 2.3 Non-goals for phase 1

Media storage, search, end-to-end encryption, webhooks, voice/video, admin UI, and an import API inside core. Migration from the legacy store will be a separate app.

---

## 3. Principles

All eight principles are **agreed as direction**. The owner set P3, P5 and P7 on 2026-10-04 after the first M2b plan was rejected. A principle can be agreed while the mechanism that implements it is only built in part or only proposed; each principle has a status line saying which.

### P1. Any core handles any room correctly
- **Statement:** Every core instance can serve every room correctly at any time. Room "ownership" exists only to make things faster: batching, ordering, warm caches.
- **Why:** Ownership is coordinated through Redis, which can fail over, lose a second of writes, or briefly let two cores both believe they own a slot. If correctness depended on ownership, any of those events could lose or duplicate messages.
- **Forbids:**
  - skipping a correctness check because "I am the owner";
  - keeping authoritative state only in memory.
- **Status:** agreed and built.
- **Enforced by:**
  - Every message write is a conditional insert on a unique key (P2).
  - Tests run two writers against the same room.
  - Duplicate detection never skips its cross-core step, even on the owning core.

### P2. The database is the source of truth; every change is one atomic fact
- **Statement:** Each change is a **fact** written by one atomic operation on one document: an insert, a compare-and-set on a version field, or an upsert on a unique key. No multi-document transactions.
- **Why:**
  - The sharding plan routes each room to one shard. Multi-document transactions across shards would need two-phase commit.
  - A single-document operation either happened or did not. When the outcome is unknown, reading the document back answers the question.
- **Forbids:**
  - transactions;
  - joins into large collections;
  - any state that has to change in two places at once to stay correct.
- **Status:** agreed; built for message inserts. Compare-and-set and upsert facts arrive with the next milestone.
- **Enforced by:**
  - shard-readiness rules (B1);
  - a shared contract test suite that every storage adapter must pass.

### P3. Every data change of an entity emits one domain event; delivery is someone else's job
- **Statement:** When any action changes an entity's data, core publishes one *logical* event describing that change to the event stream. "One" means one event identity: the same event may be delivered more than once, and receivers drop copies by id (P6). Who receives it, and how, belongs to a separate notification/delivery component: per-user fan-out, push, read-receipt visibility rules.
- **Why:** It keeps core small and testable. Each change maps to a known event, and other apps can build features without changing core.
- **Forbids:**
  - core publishing one event per recipient;
  - core deciding delivery policy;
  - a data change with no event.
- **Status:** agreed; built for message creation only. Room creation emits no event yet. The notification/delivery component does not exist yet.
- **Enforced by:**
  - An event catalog (Appendix C) that is also the test plan.
  - Each row has tests for the event emitted, no event on a no-op, and no event on an error.

### P4. Everything derived from a fact is an "effect"; the reconciler is the only guarantee path
- **Statement:** Anything derived from a fact is an **effect**. Examples: publishing its event, updating a count, deleting dependent records.
  - Every effect is a pure function of the stored document, and safe to run any number of times.
  - Effects run twice:
    1. right after the write, on a best-effort fast path;
    2. again from the database's commit log by the **reconciler**, which is the only path that guarantees completeness.
- **Why:** The fast path cannot be made reliable without transactions or blocking the acknowledgement. The commit log sees every write, including writes that do not come through core (e.g. a future migration tool). One general mechanism replaces one fix per feature.
- **Forbids:**
  - per-feature repair jobs;
  - "mark before write" bookkeeping in Redis;
  - effects that cannot be rebuilt from the stored document.
- **Status:** principle agreed.
  - The mechanism is **Built** for message-creation events (B6).
  - The general form is **Proposed** (section 5.2).

### P5. Counting is one shared counter mechanism
- **Statement:** Any derived count uses one counter mechanism, recomputed from the facts. Examples: reactions per emoji, thread replies, member count, capped unread.
- **Why:** Increment-based counters drift whenever the fact write and the increment are separate operations, which P2 forces. Each feature would otherwise invent its own repair.
- **Forbids:** stand-alone increments with ad-hoc fixes.
- **Status:** principle agreed; the counter mechanism is **Proposed** and nothing is built (section 5.3).

### P6. Natural event ids and per-document versions; no global sequence
- **Statement:**
  - An event's id is derived from the data. A new message's event is `{room}-{thread}-{seq}`.
  - A change event uses whatever value grows monotonically for that entity: the document's version (edits, deletes, pins), a change number on the fact (reactions), the read position (read receipts) or the read time (counters, section 5.3).
  - Receivers apply "higher version wins".
  - There is no gap-free room-wide counter.
- **Why:**
  - A room-wide counter needs a coordination round trip on every send, or it fails under two writers (section 2.2).
  - Natural ids repeat exactly when an event is republished, so the stream's duplicate filter and receivers can drop copies.
- **Forbids:**
  - events whose identity depends on when or where they were published;
  - clients detecting loss by counting.
- **Status:** agreed; built for message creation. Version-based change ids (D53) are agreed, not built.

### P7. System messages are produced outside core
- **Statement:** A separate component consumes events and writes system messages such as "X pinned a message". Core does not create them.
- **Consequence:** Every event must carry enough to describe the change: who acted, on what, and the new state.
- **Status:** agreed. The system-message component is not started.

### P8. Reconnect loads the latest state
- **Statement:** Clients never replay a gap of events. On reconnect they fetch current state.
- **Status:** agreed. It will be implemented by client SDKs and the gateway, neither of which exists yet.
- **Consequence:**
  - Live events are an optimisation for online users.
  - For live clients, completeness only matters within one session. Downstream apps still rely on the reconciler (P4).

---

## 4. Built mechanisms

### B1. Storage model and shard readiness — Built

**Problem.** Read any page of any room's history, including the oldest, in one cheap operation, over billions of messages. Do it without secondary indexes on the largest collection, and stay ready to shard without code changes.

**How it works**
1. Messages live in a MongoDB **clustered collection**. The primary key is the physical storage order.
2. The key is 24 bytes: `room id │ thread root │ sequence`, three big-endian 64-bit numbers, so byte order equals numeric order.
   - The main timeline uses thread root 0.
   - Each timeline numbers messages from 1.
3. A history page is one range scan over the key. The oldest page costs the same as the newest.
4. Room ids are random non-zero 63-bit numbers, so ids reveal nothing and spread evenly across future shards.
5. Edit history, which is not built yet, will use the same key plus a 32-bit version.
6. **Shard-readiness rules for all code:**
   - Large collections use room-prefixed keys.
   - Every query carries the room prefix.
   - No multi-document transactions, and no joins into large collections.
   - Unique indexes on future sharded collections start with the shard key.
   - Connection string, read preference and write concern come from configuration.
   - Collections are created only in a bootstrap step.
7. Storage sits behind **ports** (interfaces). The application only sees operations such as insert, last sequence, page, find and change feed. Every adapter must pass a shared contract test suite. Today there is an in-memory adapter and a MongoDB adapter. A PostgreSQL adapter can be added if the production-like proof of concept chooses it.

**Guarantees**
- One page = one range scan.
- Uniqueness of the key is enforced cluster-wide, also after sharding.

**Failure behaviour.**
- A hole in the sequence can appear in a rare failure. Example: a message numbered 10 fails permanently (retry limits exhausted) while message 11 of the same group is stored.
- The original design says clients and APIs treat a hole as void once it is older than 5 seconds.
- **Open point:** a message may be retried for up to about 10 seconds (B3, item 7), so a hole could still be filled after 5 seconds. Section 9 asks about this threshold.

**Trade-offs and limits**
- One replica set must hold everything until sharding is turned on (about 1.5–10TB raw over two years).
- The threshold and the archive policy are open.

**Alternatives rejected**
- Bucket documents (many messages per document): harder in-place edits and page boundaries.
- Plain collection with secondary indexes: extra index I/O and a larger working set.

**Status and evidence**
- Checked once by hand on a two-shard test cluster on 2026-09-30 (functional, not a benchmark). A repeatable two-shard test suite is planned for the read-path milestone:
  - the clustered collection shards on its key;
  - chunk migration works (the cluster's balancer moves ranges of keys between shards);
  - room range queries hit a single shard;
  - key uniqueness holds.
- Dev benchmark with 10M messages and a 1GB cache: p99 page latency of about 8–9ms for oldest, random and latest pages.

### B2. Soft slot ownership — Built

**Problem.** Batching writes and keeping per-room state in memory both need each room to have a preferred core, without a central coordinator and without making correctness depend on it (P1).

**How it works**
1. `slot = hash(room) mod 1024`.
2. Each core runs a loop every 1s with jitter:
   - **Heartbeat:** one atomic Redis script sets the core's key (TTL 5s), records the core in a sorted set scored by its expiry in **Redis server time**, and removes expired cores. Using Redis time means all cores share one clock.
   - **Renew:** extends the leases it holds (one lease key per slot, TTL 10s) if the value is still its own id. Otherwise it records the loss.
   - **Balance:** target = ceil(1024 / live cores).
     - Below target: claim free slots, or slots of cores whose heartbeat has expired. Candidates are taken in **rendezvous-hash** order, so every core agrees on preferences without talking.
     - Above target: release slots.
   - **Notify:** publish "slots changed" so gateways can reload their routing tables. No gateway consumes this yet; internal tools do.
3. **Ownership cutoff.**
   - Each core stamps its own clock at the **start** of the tick, before it calls Redis. It considers itself owner only while that stamp is younger than `min(lease TTL, heartbeat TTL) − tick` = min(10s, 5s) − 1s = 4s.
   - Why this bound: another core may claim the slot as soon as the first core's heartbeat expires in Redis, that is 5s after the heartbeat write at the earliest. The heartbeat write happens after the stamp, so the owner stops believing at least one tick before anyone else may claim.
   - Network delay only makes the stamp older, so it errs on the safe side. What remains is clock drift or a process pause, which can make two cores overlap for about one tick.
4. **Hooks** run on batches of slots (about to release, just claimed, lost). All hooks in one tick share one timeout (half a tick), so a slow hook never stretches a tick. When a room's slot moves, the core **retires** that room's in-memory actor (B3). The next request builds a fresh actor from the database.
5. Redis key scans are banned. The duplicate-detection keys fill the keyspace, and a scan once cost 31% of Redis CPU at idle.

**Guarantees**
- At most about one tick of double ownership under normal clocks.
- Correctness does not depend on ownership at all (P1).

**Failure behaviour**
- State Redis down: cores keep their last view. Leases lapse and ownership turns false, but every core still writes correctly.
- Redis failover loses data: same, plus some re-claiming.

**Trade-offs and limits**
- The renewal script touches several keys, so state Redis must be one primary (no Redis Cluster).
- Double ownership only costs retries and cache misses, never correctness.

**Alternatives rejected**
- A coordinator app plus a NATS key-value store, Kafka consumer groups, etcd: more moving parts. Correctness would still need the same database checks.
- Hard ownership with fencing tokens (a counter that grows with each new owner and is checked by the database on every write, so a stale owner's writes are refused): kept as a fallback if production chaos tests show a problem.

**Status and evidence.** Unit tests with an in-memory Redis, plus a repeated stress run (20 rounds) that simulates Redis losing data. A real chaos test is planned.

### B3. The write path: per-room actor, batching flusher, sequence allocation — Built

**Problem.** Assign each message a position in its timeline, store it durably with majority write concern, and keep up with 5–10K messages/s. A naive design does one database round trip per message, plus counter or duplicate-check round trips: on the order of 20K round trips/s at peak. Batching brings database round trips to about 1.5K/s (design estimate). The design must stay correct with two cores writing the same room.

**How it works**
1. **Router and actors.** Each room with traffic gets one lightweight in-process worker (the *actor*):
   - it has a bounded mailbox of 1024 requests (full → "busy" error);
   - it stops after 5 minutes idle;
   - a core holds at most 100K actors.
   The actor caches the room's last sequence, recent members and recent client ids.
2. **One write group in flight per room.** The actor takes up to 64 queued sends as one group:
   - checks tenant and membership;
   - checks duplicates (B4);
   - numbers them `last + 1 …`;
   - hands the group to the flusher.
   It does not start the next group until it knows the result of the current one.
   - Example: group 1 is numbered 11–12 and group 2 is numbered 13–14 before group 1's result is known. If group 1 then collides with another core's message 11, the room's real last sequence is not 12, and group 2 is built on a wrong base. It ends up with holes or more collisions.
   - Waiting for each result keeps numbering on a known base.
3. **Flusher.** Worker shards (4 in-process workers by default; unrelated to database shards) merge groups from many rooms. One unordered bulk insert with majority write concern goes out every 2ms or at 256 documents. Each shard has one insert in flight. A group carries a deadline; if the insert cannot finish before it, the group is **not sent** and is reported as such.
4. **Uniqueness as compare-and-set.** The unique key makes the insert itself the "compare-and-set". No counter is written on the send path.
5. **Four outcomes per document:**
   - Inserted.
   - Duplicate key: someone else holds this sequence.
   - Unknown: timeout or transport error, so it may or may not have been written.
   - Rejected: any other write error.
6. **Resolving Duplicate and Unknown.** The actor reads those sequence positions back with majority read concern and matches on (sender, client id):
   - Its own document → treat as written.
   - Another core's document → reload the last sequence and renumber, at most 3 times.
   - Unknown and nothing found → resend the same sequence, at most 3 times.
   - Limits exhausted → "retry later", and the actor reloads its state before the next group.
7. **Time budget per message.** Every retry fits inside the duplicate-detection reservation (10s, B4).
   - What this prevents: if a core kept retrying after its pending reservation expired, the client could retry on another core. That core would see no reservation, reserve the cid itself, and write a second copy.
   - Keeping all retries inside the reservation closes that path.
8. **After commit, in this order:**
   1. acknowledge the client (and record the client id in memory);
   2. hand the event to the publisher (B5);
   3. queue the duplicate-detection commit (B4).

**Guarantees**
- An acknowledged message is durably stored.
- No acknowledged message is stored twice by retries within the same client id window (B4).
- Order inside a timeline is the sequence order, the same for everyone (R5).

**Failure behaviour**
- Database primary failover (5–12s): requests wait in the mailbox up to the 3s request deadline, then return "unavailable". The SDK retries with the same client id.
- A core killed mid-write: whatever reached the database counts as done. The next actor reloads from the database.

**Trade-offs and limits**
- A hot room is limited to one group in flight. Batching within the group compensates.
- The rare partial failure can leave a sequence hole (B1).

**Alternatives rejected**
- A counter increment per message: an extra majority round trip on every send.
- A stateless writer: no batching, about 20K round trips/s.
- Hard ownership: needs fencing anyway.
- Log-first (write to the stream, then the database): two sources of truth.

**Status and evidence (dev only)**
- Two cores held 5,000 msg/s for 60s with no shedding and no failures. Acknowledgement p99 was about 150–270ms on clean infrastructure, far from the 30ms target.
- At 10,000 msg/s the dev machine saturates: about 9,450/s achieved, 5.5% shed by the load generator, p99 about 0.8s.
- The dev database is a one-node replica set on a laptop VM. A raw majority bulk insert at 10K docs/s already showed p99 26.8ms per round trip and highly variable tails. Production-like measurement decides go/no-go.

### B4. Client-id duplicate detection — Built

**Problem.** Mobile clients retry. A retry with the same client message id ("cid") must return the original acknowledgement and never store a second copy, even when the retry lands on another core.

**Guarantees, as agreed**

| # | Guarantee |
|---|---|
| CD1 | A retry with the same cid within 15 minutes returns the same acknowledgement and adds no copy, on any core |
| CD2 | If the dedupe Redis is down, detection falls back to each core's memory; cross-core duplicates become possible |
| CD3 | If a core dies between insert and recording the commit, a retry on another core can create a duplicate. This needs two conditions: the retry comes after the 10s pending window, and the message is no longer among the room's latest 100 (the new actor seeds its memory tier from those 100, so it would recognise a recent one). Accepted |

**How it works.** Three tiers:
1. **Memory:** a per-actor LRU (4096 entries, 10 minutes), seeded from the room's latest 100 messages when the actor starts.
2. **Redis "pending":** before the insert, reserve a key per (room, user, cid) with value "pending on core X" and TTL 10s.
   - Pending on another core → "retry later".
   - Pending on this same core with no live write (an abandoned attempt) → also "retry later".
3. **Redis "committed":** after the insert, overwrite the key with "committed: seq, time" and TTL 15 minutes. The database is the source of truth, so this write is unconditional.

**Cross-room batching (D58).** At 5K msg/s over 1,000 rooms most groups hold one message, so each message cost two sequential Redis round trips. A batcher per shard (4 by default) fixes this:
- Reservations go out immediately when no round is in flight. Otherwise they join the next round: at most 256 keys in one script call.
- Commits and aborts are only queued (4,096 per worker shard). A background loop pipelines them.
- When the queue is full the commit is dropped and counted. The key then stays "pending" until its 10s TTL and disappears. Retries on the same core are still caught by its memory tier. On another core, the same CD3 conditions apply.

**Acknowledge before commit (D59).** Removes one Redis round trip from the acknowledgement path. The commit now waits in the batcher queue for a few milliseconds after the acknowledgement. A crash in that time leaves the key pending, so the CD3 window grows by those few milliseconds.

**Degraded mode.** Every Redis call has a 100ms timeout. After a failure the client enters a 1s cooldown with one probe, and logs once on entry and once on exit. Detection then uses memory only (CD2).

**Connection pool (D60).** Small and warm: about 2 × worker shards + 4 connections, no automatic command retries, one dial retry.
- Automatic retries are off because a reservation is not idempotent. Suppose the first attempt reached Redis but its reply was lost. The retry finds "pending on this core" and is answered "retry later" (the abandoned-attempt rule in tier 2): a false conflict.
- Under the default client settings, each burst of timeouts made cores open 100–227 new connections per second. With this setting the worst second had 2.

**Trade-offs.** CD2 and CD3 are accepted holes, documented and limited to failure windows.

**Alternatives rejected**
- 24h retention: about 860M keys at 10K/s.
- Skipping Redis on the owning core: breaks P1 during double ownership.
- A different Redis client with auto-pipelining: rewrites too much.

**Status and evidence (dev).** At 5K msg/s: Redis commands per message 6.35 → 4.74, dedupe Redis CPU 89% → 69%, and the share of core CPU spent on reserve + commit 23.8% → 12.4%.

### B5. Event publishing — Built

**Problem.** Tell gateways and other apps about every committed message quickly, without slowing the acknowledgement and without depending on a gap-free counter.

**How it works**
1. **Event id** = `{room}-{thread}-{seq}`. It is used both as the event's id and as the stream's de-duplication header. JetStream drops any repeat of an id within 5 minutes.
2. **Subject:**
   - Events go to the stream under `evt.{tenant}.room.{room}.{type}`, kept 7 days.
   - The stream republishes each event to a non-persistent `live.…` subject that gateways subscribe to.
3. **Queues.** The publisher keeps one queue per worker shard, chosen by the room's slot (4 worker shards, 1,024 events each). Enqueue never blocks, and a room's events keep arrival order.
4. **Async publish.** Each worker shard publishes asynchronously in arrival order without waiting for stream acknowledgements. The messaging client is configured with:
   - the in-flight limit (256 per shard);
   - acknowledgement timeouts (2s);
   - retries while the stream has no leader.
5. **Failures are only logged**, at most once per second: rejection, timeout, too many in flight, queue full. Such events are dropped; the reconciler makes up for them (B6).
6. **Ack marks.** After each stream acknowledgement, the publisher records an *ack mark* in Redis ("this event reached the stream"). Marks are batched off the client acknowledgement path (B6).
7. **Shutdown:** drain the queues, then wait up to 5s for outstanding acknowledgements.

**Guarantees**
- Per-room order on the normal path.
- An event carries a natural id, so duplicates are harmless.

**Failure behaviour.** An event is lost from the fast path in any of these cases:
- the stream rejects it;
- the acknowledgement times out;
- too many events are in flight;
- the queue is full;
- the core dies between commit and publish.

The reconciler republishes such events after about 30s.

**Trade-offs**
- Library retries, reconnects and slot moves can reorder events. Accepted.
- A short NATS incident drops more events on the fast path than a self-managed retry would.

**Alternatives rejected**
- A per-event watermark with sweepers (the original design): a large amount of self-managed state, 22% worse p99, and still no full ordering guarantee.
- A transactional outbox: adds a write to every send.

**Status and evidence.** Dev: no missing or duplicate live events in the 5K/s runs. The NATS proof of concept with 1M subscriptions at about 5K events/s showed publish-to-subscriber p99 of about 1.2ms.

### B6. Event reconciliation — Built (for message creation)

**Problem.** Make the event stream complete (every committed message eventually has its event) without adding anything to the acknowledgement path.

**Requirements**

| # | Requirement |
|---|---|
| RC1 | Every committed message eventually has at least one event in the stream, within the reconcile delay plus reconciler lag |
| RC2 | A reconciled event is identical to the normal-path event: same id, same content |
| RC3 | A reconciler crash, restart or handover loses nothing |
| RC4 | Duplicates are allowed but rare, and receivers drop them by id |
| RC5 | A gap beyond the database's log retention is lost for good and must raise an alert |

**How it works**
1. **Source of truth = the database's commit log.** On MongoDB this is a *change stream* over message inserts. The reconciler reads it through a database-neutral interface, backed by a shared contract test. A PostgreSQL adapter would use logical replication.
2. **One active instance:** whichever core owns slot 0. No extra lock. Each time a core gains slot 0 it starts a new *term*. Two overlapping reconcilers are still correct, because ids are identical.
3. **Wait the reconcile delay (30s)** past each change's commit time, so the fast path normally gets there first.
4. **Ack marks as an optimisation:**
   - The publisher sets one bit per acknowledged message in a Redis bitmap per room-thread chunk of 8,192 messages.
   - The bitmap has a 1h TTL, refreshed at most every 15 minutes per chunk.
   - Mark writes are batched (256 keys or 10ms).
   - The reconciler checks marks in batches and republishes **only unmarked** messages.
   - A lost or evicted mark only causes an extra publish, never a loss.
   - If the reconciler lags more than 1h, marks have expired and every message is republished. If it lags more than the stream's 5-minute duplicate window, those republished events become real duplicates on the stream. Receivers still drop them by id (RC4).
5. **Rebuild** the event from the stored document with the same id (RC2). The room type is looked up from the rooms collection (cached).
6. **Own publish client:**
   - up to 1,024 events in flight, in commit order;
   - unbounded retry with backoff;
   - a document that cannot become an event (missing room, corrupt record) is dropped and logged.
7. **Confirm the position** about once per second. The *position* is the reconciler's bookmark in the commit log: a resume token plus the cluster time (the database's own logical clock for the replica set).
   - It only moves past changes whose events are all acknowledged or marked.
   - It is stored durably with majority write concern and never moves backward.
8. **Bootstrap anchor.** Database bootstrap records the current cluster time as a starting anchor if none exists. A brand-new database therefore reconciles writes made before the first term.
9. **Lost history.** If the stored position has fallen out of the database log, the reconciler logs an error ("events in the gap are lost for good"), forgets the position and restarts from now.
10. **Duplicate window check.** The stream's duplicate window is 5 minutes. Boot refuses a reconcile delay that is not shorter than that window. Lag beyond the window is logged.

**Guarantees.** RC1–RC4. RC5 needs an alert in production, plus a database log retention of at least 24h.

**Failure behaviour**
- Slot 0 owner dies: the next owner resumes from the last confirmed position. Unconfirmed changes repeat and are dropped as duplicates.
- Dedupe Redis lost: every message is republished. The stream drops repeats inside its window.

**Trade-offs and limits**
- Completeness lag is the reconcile delay (30s) plus reconciler lag.
- A single active instance does all the work (gap H5).
- NATS keeps a duplicate map of about 3M ids at 10K msg/s. Memory to be measured.
- **Today it covers only message creation.** Extending it to every change is proposal 5.2.

**Alternatives rejected**
- **Redis marks as the source of truth:**
  - completeness would depend on a non-persistent, evicting Redis;
  - one more round trip before the insert;
  - needs key scans or an index;
  - every write path must remember to mark;
  - writes made directly to the database are invisible.
- **Republish everything:** doubles publishes, and creates real duplicates when lagging.
- **Change-stream relay as the only publish path:** a single point of failure for live events, and adds change-stream latency to delivery.

**Status and evidence.** Integration test: a message written straight to the database (bypassing core) appears on the stream after about D.

### B7. Two Redis instances — Built

**Problem.** Coordination keys must never be evicted or starved by the high-volume duplicate-detection keys.

**How it works**

| Instance | Holds | Persistence | Eviction |
|---|---|---|---|
| state | core heartbeats, live-core set, slot leases, "slots changed" channel | append-only file, fsync every second | none (writes fail when full) |
| dedupe | cid keys, ack-mark bitmaps | none | evict least recently used across all keys; memory cap (512MB on dev) |

- Sizing for dedupe: about rate × 15 min × 150B × 1.5 ≈ 2GB at 10K msg/s. Here 150B is the approximate memory per key including Redis's own overhead, and × 1.5 is headroom for allocator fragmentation.
- When memory is short, LRU drops the oldest committed cids first, which shortens the CD1 window. Evicting by shortest TTL was rejected because it would drop 10s pending keys first and break cross-core reservations.
- Both instances require a password.
- Production runs each instance under **Redis Sentinel** (Redis's failover supervisor, which promotes a replica when the primary dies).

**Failure behaviour**
- Dedupe down: memory-only duplicate detection (CD2); every message republished by the reconciler.
- State down: B2.

**Limits.** Sentinel failover for both is planned for hardening (M5).

### B8. Process lifecycle and overload control — Built

**How it works**
- **Ordered start:** publisher → flusher → duplicate batcher → router → slot manager → reconciler → request server. The server opens and reports ready only after everything started cleanly.
- **Budgeted shutdown:**
  1. readiness off;
  2. 2s drain delay for load balancers;
  3. graceful stop of the request server;
  4. reconciler (its drain + 1s);
  5. router;
  6. duplicate batcher (flush queued commits);
  7. flusher;
  8. publisher (drain + up to 5s);
  9. release slots;
  10. close clients.

  The plan totals 24.2s of a 25s budget; the container stop grace is 30s. Boot rejects configurations whose phases do not fit.
- **Overload control:**
  - every request has a server-side deadline (3s);
  - a process-wide in-flight limit (2,048) with a short queue wait (25ms) *sheds* excess load early: excess requests are refused at once with "busy" instead of queuing;
  - every internal queue is bounded.
- **Health:** liveness and readiness endpoints on a private admin port.

### B9. Trust, tenancy and secrets — Built

- **Callers are trusted internal apps** until mutual TLS lands (hardening). Tenant and user come from request metadata.
- **Every operation re-checks** that the room belongs to the caller's tenant and that the user is a member.
- **Secrets:**
  - Passwords reach processes only as files (orchestrator secrets), never as command-line arguments, inspectable environment variables or log lines.
  - One logging layer redacts connection strings and passwords from every log line, including background error logs.
- **Message content is never logged.**

### B10. Read path today — Built (minimal)

- History pages are read straight from the store, not through actors.
- Tenant and membership are checked.
- Anchors: latest, oldest, before a sequence, after a sequence. Limit 100.
- Missing: everything that depends on the reader (gap H3 / section 5.5), caches for hot rooms, and the room list with unread counts (later milestone).

### B11. Failure matrix — Built behaviour

| Failure | Behaviour |
|---|---|
| Database primary failover (5–12s) | Writes wait up to the request deadline, then return "unavailable"; SDK retries with the same cid |
| NATS fully down | Database writes continue; fast-path events dropped; reconciler retries until NATS returns; clients reload latest state |
| State Redis down | Last known slot table; ownership lapses; every core still writes correctly |
| Dedupe Redis down | Memory-only duplicate detection (CD2); reconciler republishes everything |
| Core dies | Others claim its slots in about 5s; meanwhile any core serves the rooms; reconciler makes up lost events |
| Core dies between acknowledgement and cid commit | Retry may get "retry later" for up to 10s; after that a duplicate is possible only outside the last 100 messages (CD3) |
| Slow Redis | Each client degrades independently with a 1s cooldown and one probe; no connection storms |
| Reconciler position falls out of the database log | Error log, restart from now; that range of events is lost for good (alert required) |

---

## 5. Partial and missing mechanisms

Everything in this section is **Partial**, **Proposed** (not approved) or **Missing**.

The authors keep a mechanism map with ids C1–C10. This section uses those ids. The mechanisms not listed here are already covered in section 4:
- C1 fact writes: B1, B3.
- C5 ordering and versions: B1, B3, B5.
- C6 slots and actors: B2, B3.
- C7 lifecycle: B8. It came out of planning the next milestone (edit, delete, reactions, pins, read receipts). The first plan solved each problem with its own fix:
- mutations through the actor;
- a repair marker for reaction counts;
- a separate batcher for read receipts;
- a separate path for change events.

The owner rejected it in favour of general mechanisms.

### 5.1 C2: Idempotent commands as desired state — Partial

- **Problem.** Retries are normal. Creation already has cid detection (B4); changes do not.
- **Proposal.** Every change command states the **desired state**, not a delta: set text to X, set reaction to on, set pinned, raise read position to at least N. A retry that finds the state already in place is a no-op: it succeeds and emits nothing. No per-command id registry is needed for changes.
- **Open question.** Is desired-state enough for every future command (e.g. "forward", "mark unread")? Does a command need a cid when it creates something new?

### 5.2 C3: Commit log → effects — Partial (built for message creation only)

**Problem.** Generalise B6 to every derived effect of every fact: events for all changes, counter updates, cleanup.

**Proposal**
1. An **effect** is one of: publish an event, touch a counter, clean up dependent records. Each is a pure function of the stored document and idempotent:
   - events through natural ids;
   - counters through the guard in 5.3;
   - cleanup through deletes by key.
2. One **registry** maps each collection's changes to effects. The fast path (right after a write) and the reconciler (from the commit log, after the reconcile delay) call the same registry, so they cannot diverge. Tests check this "parity" for every event type.
3. **One commit-log feed for the whole database**, filtered to the relevant collections:
   - updates are taken only when a version field changed;
   - each update includes the document as it is *now*, looked up at read time. The alternative would be pre/post images: copies of the document as it was just before and just after that change, which MongoDB can store at extra cost.
   The current document may already be newer than the change being read. The effect then describes the newer state, which fits "higher version wins".
4. **Ack marks stay an optional optimisation per effect type.** Only message creation (the highest volume) has them. Other effects are always re-run by the reconciler; they are idempotent and much rarer.
5. **Bug found during review:** the publisher marks every event by (room, thread, sequence), not by event type. Once change events exist, an acknowledged edit event would set the bit of its message, and the reconciler would then skip a lost creation event. Item 4 above fixes this. It is harmless today because only creation events exist.
6. **Consequence:** changes do not need to go through the per-room actor. Each fact is a single-document compare-and-set or upsert, correct on any core (P1). Ordering between change events is not needed because receivers apply the higher version. This reverses an earlier decision (D54) that routed edits through the actor.
   - **Trade-off against R6/R7:** today creation-before-change holds only on the normal path, and only in the order events are emitted. A client can edit a message only after receiving its acknowledgement, and the creation event is queued right after that acknowledgement, so the order almost always holds. Outside the actor, though, nothing guarantees it.
   - Receivers must therefore accept a change event before the creation event and treat it as an upsert by version. This is a deliberate weakening and is part of question 5.

**Open questions**
- Can one reconciler instance keep up with every collection (H5)?
- Is updating the current document (instead of a pre/post image) acceptable for every event type?

### 5.3 C4: Counter — Proposed (nothing built)

**Problem.** Counts derived from facts live on a different document from the facts:
- reactions per emoji on a message, and who reacted, in a separate collection (a channel post can have 200K reactors, too many for one document);
- thread reply counts;
- room member count;
- exact unread capped at 99+.

Without transactions, "write the fact, then increment the count" drifts. A crash or an unknown outcome between the two steps leaves the count off by one, permanently.

**Proposal**
1. A counter is declared once: name, target document and field, the query that selects its facts (always prefixed by room, for sharding), and an optional cap (for example 100 for "99+").
2. **Touch = recount, not increment:**
   - count the facts with majority read concern inside a causally consistent session (a MongoDB session that guarantees each read sees at least the writes the session has already seen);
   - take the read's cluster time **T**;
   - write the result only if the stored value is older: "set value = n, time = T, where time < T".

   Properties:
   - Touches can repeat any number of times.
   - Two concurrent touches cannot let an older count overwrite a newer one.
   - Any drift heals on the next touch.
3. **Coalescing:** touches of the same counter within a short window (for example 50ms) become one recount.
4. **Partitions (scale knob, off by default):** a very large counter (a viral post) is counted per bucket (`hash(user) mod K`), and the total is the sum. Turned on only when measurements show it is needed.
5. **The counter is an effect (5.2).** The fast path touches it right after the fact. The reconciler touches it again after the reconcile delay, so the stored value always converges to the truth.

**Open questions**
- Recount cost for hot counters. Coalescing and partitions are the levers.
- Is cluster-time ordering of reads a sound guard across failovers?
- Should unread (per user per room) use the same mechanism, or a variant?

### 5.4 C8: Author and owner invariants, and a single permission hook — Partial

- **Built:** tenant and membership checks on every operation.
- **Agreed (owner):**
  - only the sender may edit;
  - delete-for-everyone is allowed for the sender or a room owner;
  - reacting, pinning and reading need membership only;
  - every change records who made it.
- **Missing:** one place where permission checks happen, so a policy engine (planned for later) can plug in without touching each operation (H4).

### 5.5 C9: Reader-specific view — Missing

- **Problem.** What a user sees depends on the user:
  - messages they hid ("delete for me");
  - history they cleared;
  - placeholders for messages deleted for everyone;
  - whether they may read edit history (A7).

  Without a shared mechanism each read API would filter on its own.
- **Draft idea.** A "view" step applied by every read API after the storage read.
  - Hidden messages come back as placeholders without content, so paging by sequence stays exact.
  - No design yet for caching or for the cost of per-reader lookups.

### 5.6 C10: Cleanup and retention as effects — Missing

- **Problem.** Some facts make other records obsolete. Example: deleting a message for everyone must remove its edit history.
- **Draft idea.** Cleanup is an effect (5.2).
  - Example: an edit-history record inserted after its message was deleted (an edit racing a delete) is removed by the same effect, not by special code.
  - Retention policies per tenant would use the same path.

---

## 6. Gaps not yet designed

H3 and H4 have only draft ideas (sections 5.4 and 5.5); none of these gaps has an agreed design.

| # | Gap | Why it must be designed first | Direction |
|---|---|---|---|
| H1 | **Member management**: add, remove, leave, change role. Only room creation exists | Member count (counter), member events (gateways need them to subscribe), role-based permissions | Build on facts + effects + counter; place in the next milestone or the one after |
| H2 | **Channel room type** (about 200K subscribers, admin-only posting) is in scope but not implemented | Posting permission, fan-out, very large counters | Decide which milestone |
| H3 | **Reader-specific view** (5.5) | Otherwise hide/clear/edit-history rules are patched into each read API | Design before edit/delete |
| H4 | **Permission hook** (5.4) | Otherwise each operation checks on its own and drifts | One interface now; policy later |
| H5 | **Reconciler scale**: one active instance will handle every collection | Lag delays every effect, including counter repair | Measure; if short, partition the feed (a mechanism, not a patch) |
| H6 | **Internal caller API** for system messages with a kind and an unread flag | Exact unread needs the flag; the gateway must never pass a kind from clients | Decide with the read-path milestone |
| H7 | **Multi-device sync**: events on a per-user subject (P3) may replace a separate sync mechanism | Avoid building two mechanisms for one job | Decide when the read path is redesigned |
| H8 | **Privacy of per-user events** on live subjects | Private events (e.g. "hidden for me") must not reach other room members | Part of the gateway design |

---

## 7. Proposed roadmap (pending owner review)

| Milestone | Content | Depends on |
|---|---|---|
| M2b.1 Effects + counter | General commit-log feed, effect registry and executor, ack marks for creation only, counter mechanism; "room created" event as the first new effect | 5.2, 5.3 |
| M2b.2 Edit + delete | Edit with history, delete for everyone / for me, edit history read, author rules | 5.1–5.6, H3, H4 |
| M2b.3 Reactions + pins | Reaction facts and counts, pins | 5.1–5.3 |
| M2b.4 Members + read receipts | Member management, read position | H1 |
| M2c Threads and extras | Threads (reply count is a counter), mentions, reply/forward, bookmarks, mark unread | — |
| M3 Read path | Room list with exact unread (capped counter), redesigned sync | H6, H7 |
| M4 Gateway | WebSocket, interest-based fan-out (each gateway subscribes only to rooms its connected users belong to), per-user subjects | H8 |
| M5 Hardening | Load and chaos tests, observability, full CI, Sentinel for both Redis, reconciler lag | H5 |
| Channel | Channel room type | H2 |

From now on, every milestone plan must start with a "feature → mechanism" table. A feature that needs a mechanism not on the map stops planning until that mechanism is designed. Plans are written step by step, with expected results, before coding. An implementer who meets a case the plan does not cover stops and reports.

---

## 8. Accepted trade-offs and open risks

| Risk / trade-off | Severity | Mitigation / status |
|---|---|---|
| Latency target A1 (30ms ack p99) not met on dev | High until measured | Dev is a one-node database on a laptop VM; production-like run decides go/no-go |
| One replica set holds all data until sharding is enabled | Medium | Shard-ready rules (B1); threshold and archive policy open |
| CD3: duplicate possible after a core crash in a narrow window | Low | Documented; window limited to 10s pending TTL and older messages |
| CD2: cross-core duplicates while dedupe Redis is down | Low–medium | Memory-only detection; alert on degraded mode |
| Fast-path events are best-effort; completeness lags by 30s plus reconciler lag | Medium | Reconciler (B6); extended to all effects in 5.2 |
| Reconciler position lost beyond the database log retention | High (permanent loss) | Log retention ≥ 24h; alert on "history lost" and on lag |
| Single reconciler instance | Medium | H5: measure, then partition if needed |
| NATS memory for the 5-minute duplicate map (about 3M ids at 10K/s) | Medium | Measure on production-like setup |
| Event reordering on retries, reconnects or slot moves | Low | Receivers use versions; clients sort by sequence |
| Soft ownership without fencing | Low (by design) | Correctness from unique keys; fencing kept as a fallback if chaos tests fail |
| Pin and its "X pinned" notice are separate writes | Low | Notice is produced from the event by a separate component (P7), so it inherits the reconciler's guarantee |
| Sequence hole treated as void after 5s while a message may still be retried for up to about 10s (B1) | Low | Open: align the threshold with the retry budget |
| Alerts named in this report are not configured | Medium | Planned for hardening (M5) |
| Counter recount cost on very hot targets | Medium | Coalescing and partitions (5.3), turned on by measurement |

---

## 9. Questions for the reviewer

1. **Soft ownership.** Is "soft ownership via Redis leases + unique-key compare-and-set in the database" enough, or should we add fencing tokens before production?
2. **Best-effort events + commit-log reconciliation.** Is this model (P4, B5, B6) acceptable for downstream apps that need every event, given a 30s-plus completeness lag? Are there failure modes we have not listed?
3. **Effects (5.2).** Is it sound to run every derived effect twice (fast path, then reconciler) from one registry, using the *current* document rather than the image at change time?
4. **Counter (5.3).** Is "recount + write guarded by read cluster time" correct under failover and concurrent writers? Is there a cheaper standard pattern with the same guarantees and no transactions?
5. **Mutations outside the actor (5.2 item 6).** Is it safe to let edits, deletes, reactions and pins bypass the per-room actor and rely on single-document compare-and-set?
6. **Single reconciler instance.** At what scale should we expect it to fall behind, and is partitioning the commit-log feed the right next step?
7. **Duplicate detection.** Are CD2 and CD3 acceptable for a chat product, or should one of them be closed?
8. **Unread capped at 99+ (R17).** Is a capped recount per user per room realistic at 100K users, or does it need a different structure?
9. **Missing pieces.** Which gaps in section 6 would you design first? Is anything missing from the mechanism map entirely?
10. **Database choice.** Given B1 and B6 (clustered keys, change streams), is anything locking us into MongoDB more than we think?
11. **Sequence holes (B1).** Is "a hole older than 5 seconds is void" the right client rule, given that a message may be retried for up to about 10 seconds?

---

## Appendix A. Glossary

| Term | Meaning |
|---|---|
| Actor | A lightweight in-memory worker per active room on a core; serialises that room's sends |
| Ack (client acknowledgement) | The reply returned to the client after a durable write |
| Ack mark | A bit in Redis saying a message's creation event reached the stream; lets the reconciler skip it |
| Bootstrap | One-time step at startup that creates collections and indexes, and records the reconciler's starting anchor |
| Change stream / commit log | The database's ordered feed of committed writes (MongoDB change streams; PostgreSQL logical replication) |
| cid | Client-generated id of a message, used to detect retries |
| Cluster time | MongoDB's logical clock for a replica set or cluster; orders writes and reads |
| Clustered collection | A MongoDB collection stored in primary-key order, with no separate primary index |
| Compare-and-set (CAS) | A write that succeeds only if the document still matches an expected condition (e.g. a version) |
| Core | The application that owns writes and events |
| CPaaS | Communications Platform as a Service |
| Fencing token | A number that grows with each new owner and is checked on every write, so writes from a stale owner are refused |
| Reconcile delay | How long the reconciler waits (30s) after a change's commit before handling it. Not to be confused with decision ids D1–D64 |
| Effect | Anything derived from a fact: an event, a counter update, a cleanup |
| Fact | A data change written by one atomic single-document operation |
| Flusher | The component that merges many rooms' writes into one bulk insert |
| JetStream | NATS's persistent stream with a built-in duplicate filter by message id |
| Interest-based fan-out | A gateway subscribes only to rooms that its connected users belong to |
| Lease | A Redis key with a TTL that marks a slot's preferred owner |
| Live subject | A non-persistent NATS subject that the stream republishes each event to; gateways subscribe to it |
| LRU | Least recently used: a cache that evicts the entry unused for longest |
| Majority write/read concern | The write is acknowledged, or the read returns data, only once a majority of replica-set members have it |
| Natural event id | An event id derived from the data (room, thread, sequence, version), so it repeats exactly on republish |
| Position (reconciler) | The reconciler's durable bookmark in the commit log (resume token + cluster time) |
| Redis Sentinel | Redis's failover supervisor; promotes a replica when the primary fails |
| Rendezvous hashing | A way for every node to rank candidate owners for a key identically without coordination |
| Shedding | Refusing excess requests immediately instead of queuing them |
| Stream acknowledgement | JetStream's reply that it stored an event (distinct from the client ack) |
| Subject | A NATS address an event is published to, e.g. `evt.{tenant}.room.{room}.{type}` |
| Sequence (seq) | A message's position in its timeline (main room or one thread), starting at 1 |
| Slot | One of 1,024 buckets that rooms hash into; the unit of ownership |
| Tenant | A product using the platform; the isolation boundary |
| Term | One period during which a given core runs the reconciler (while it owns slot 0) |
| Tick | One round of a core's 1-second slot loop |
| TTL | Time to live: how long a key exists before it expires |
| Version | A per-document counter that grows by one with each edit or delete |
| Worker shard | One of a few in-process worker lanes in a core (flusher, publisher, duplicate batcher); unrelated to database shards |

## Appendix B. Decision index (selected)

| # | Decision | Status |
|---|---|---|
| D9 | MongoDB for all core data (PostgreSQL compared; final choice after production-like runs) | Active |
| D10 | Clustered messages keyed `room│thread│seq` | Active |
| D11 | Slots + per-room actor + batched bulk insert | Active |
| D12 | Soft ownership on Redis; correctness independent of Redis | Active |
| D13 | Sequence in memory, unique key as compare-and-set | Active |
| D19 | Gap-free publishing with watermarks | Replaced by D47 |
| D21 | Replica set now, shard-ready code | Active |
| D24 | Ownership cutoff from the start of the tick | Active |
| D25 | Live cores from a sorted set by Redis time; key scans banned | Active |
| D26–D27 | Batched slot hooks with one timeout; retire actors on slot move | Active |
| D29–D33 | One write group in flight; four insert outcomes; retry limits; per-entry time budget; group deadlines | Active |
| D34 | cid committed retention 15 minutes | Active |
| D36–D38 | Activity marks, watermark, sweeper | Replaced by D49 |
| D44–D46 | Two Redis instances; passwords and secrets as files | Active |
| D47 | Events are best-effort; completeness by reconciliation | Active |
| D48 | No room-wide event counter; natural event ids | Active |
| D49 | Remove watermark, sweeper and recovery walk | Active |
| D50 | Publisher keeps only per-shard queues; async publish | Active |
| D51 | Every event can be rebuilt from the stored document | Active |
| D52 | Reconciler from the commit log, single instance on slot 0, ack marks as optimisation | Active |
| D53 | Change-event ids by target version | Agreed, not built |
| D54 | Edits through the room actor | **Proposed to change** (5.2) |
| D55 | Delete for everyone = tombstone; delete for me = private record, no event | **Proposed to change**: delete for me emits an event (P3) |
| D56 | Reactions: unique fact per (message, user, emoji), summary by increment | **Proposed to change**: summary by counter (5.3) |
| D57 | Pins stored on the room (≤ 50) with a version | Agreed, not built |
| D58–D60 | Cross-room cid batching; acknowledge before commit; small warm Redis pools | Active |
| D61–D64 | Effects registry; counter; ack marks for creation only; database-wide feed | **Proposed** (5.2, 5.3) |

## Appendix C. Draft event catalog for the next milestone (Proposed)

Subjects are `evt.{tenant}.room.{room}.{type}`. Private data of one user uses `evt.{tenant}.user.{user}.{type}`.

| Fact | Event type | Subject | Event id | Actor |
|---|---|---|---|---|
| Room inserted | `room_created` | room | `{room}-created` | creator |
| Message inserted | `msg_created` | room | `{room}-{thread}-{seq}` (built) | sender |
| Message text changed (version N+1) | `msg_edited` | room | `{room}-{thread}-{seq}-v{ver}` | editor |
| Message tombstoned (version N+1) | `msg_deleted` | room | `{room}-{thread}-{seq}-v{ver}` | deleter |
| Hidden-for-me record inserted | `msg_hidden` | user | `{room}-{thread}-{seq}-h-{user}` | user |
| Reaction fact toggled (change number n) | `reaction_changed` | room | `{room}-{thread}-{seq}-r{reaction}-{n}` | user |
| Counter value written (time T) | `counts_changed` | room | `{target}-{counter}-t{T}` | — |
| Pins changed (pins version) | `pins_changed` | room | `{room}-pins-v{ver}` | user who pinned |
| Read position raised | `read_updated` | room | `{room}-read-{user}-{seq}` | user |

Other effects:
- Deleting a message for everyone removes its edit history.
- An edit-history record that lands after its message was deleted is removed.
- A reaction change touches the message's per-emoji counter.

Every row is tested for:
- the right effect for the fact;
- no effect on a no-op retry;
- no effect on an error;
- the same result from the fast path and the reconciler;
- the same result when the effect runs twice.
