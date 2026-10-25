---
name: mention-app-role
description: "Mention App (owner 2026-10-17): records mentions, expands groups, notifies per user; embedded in core for now; own filtered consumer; mute belongs to the notification app"
metadata:
  type: project
---

Owner decisions of 2026-10-17. D162 plus the milestone brainstorm. The full log is `.claude/plans/mention-app_design.md` (MA1–MA16).

**Role.** Mention App is a business module (`modules/mention`, embedded in core). It consumes message events, records user/group/channel mentions, expands groups and publishes notifications as `WsPublish` packets (content once + target users) to `websocket.publish.{tenant}` on `CHATIM_WS` (D192); `notify.{tenant}.{kind}` is reserved for the Phase 2 notification app.

**Run mode.** Mention App runs embedded in core. On 2026-10-19 the owner first chose to split it out, then kept it embedded (D177) after checking the enterprise rule: a modular monolith while there is one team and one release cadence. Split it only for its own team, SLA, release cadence, a measured scaling need, or its own database + CDC. Once split, it owns its data and reaches core only through APIs. See [[no-shared-tables-across-services]].

**Layout (owner 2026-10-20, D182).** Code lives in `modules/mention` (`apps/` = programs with `main.go`, `modules/` = embedded modules). D164 re-checked and kept: the reason for the boundary is one-way dependency + per-user state out of core, not a future split; D162's "same code as a separate server" clause is superseded. Core domains (message/member/interaction/room) are NOT modules: they share `rooms` writes, the D100 transaction, store and sweeper. A part becomes a module only if it owns its collections, depends one way on core, and talks through events + a port.

**Groups.** Mention App owns group membership. The owner chose to set it per user: `SetUserGroups(user, full group list, ver)` into `mention_user_groups`, one doc per user, and the sender must always send every group of the user. A group move is then one write, never two commands. Members change often. Never store a group as one array, and never use per-group add/remove pairs.

**Group picker.** The owner decided this on 2026-10-17. Suggest only groups that have a member in the room, from a catalog of taggable groups (`mention_groups`, `SetGroups`) that maps id to name. The client composes the `@` picker from core `ListMembers` and Mention App `ListRoomGroups` and filters on the device. Groups that are not in the catalog are recorded but neither notified nor listed (D172).

**Scaling.** The owner decided on 2026-10-17 that Mention App is a stateless queue group: every pod (embedded: every core) pulls the `mention` consumer, with no leader and no partitions, so K8s pods share the load. Correctness must not depend on order, so events are self-describing (per-target `at`, `previous_mentions`, conditional writes, tombstones; D173). Never propose a single slot-0 processor for it again.

**Mute.** Muting belongs to the notification app (websocket M4 / push Phase 2). Mention App notifies everyone it targets.

**Consumer.** The app has its own consumer, but it must receive only the data it needs. The owner asked for an event catalog with a flow diagram (`docs/events.md`): what core publishes and who consumes what.

**Notification window.** Notifications are re-sent within 5 minutes of a NATS outage (`MENTION_NOTIFY_MAX_AGE` 5m).

**Order.** Mention App comes before M4.

**Status.** Built and merged to `main` in PR #16 on 2026-10-17 (`dev-done`), after M3 and M3.1 (PR #15). Decisions D162–D176. The owner accepted the bench result: NATS CPU +19–27% and Mongo +17% in exchange for 2–3× lower room-list and projection lag (D176).

**Why:** these are the owner's answers. The owner rejected the per-app mirror and the copy step as premature, and insisted on an efficient consumer.

**How to apply:**
- Keep mention fan-out and push out of core.
- When designing any consumer, list exactly which event kinds and subjects it needs.

Related: [[design-principles]], [[owner-communication]].
