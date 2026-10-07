# M2b.4 — Member (lớp tập) + vị trí đọc — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`.
>
> **Tóm tắt kỹ thuật cho owner:** [2026-10-06-m2b4-members-read-summary.md](2026-10-06-m2b4-members-read-summary.md)

**Goal:** Trước hết đổi tên mọi field của các collection đã xây (trừ `messages`) sang từ tiếng Anh đầy đủ, và đổi clear history sang mốc thời gian `cleared_before_time`. Sau đó làm member theo **lớp tập** (§4): mỗi (room, user) một doc `members` clustered `_id = room│user`, mang `ver` riêng (chỉ tăng, được có lỗ); xoá/rời để tombstone `state = 2`. `AddMembers` mang `request_id` (dedupe như cid). Lệnh chạm tới owner đi qua CAS `rooms.owners_ver` + `pending_owner_change` (thăng người kế nhiệm trước), nên room luôn còn owner; effect `owner_guard` vá khi core chết giữa chừng. `member_count` là **aggregate** đếm lại bằng `counter` tổng quát hoá (CAS `member_count_ver`, hội tụ ~1s, chỉ worker ghi). Vị trí đọc `read_seq/read_ver` là lớp **tần suất cao gộp được**: chỉ toán tử thường, không đụng `ver`, nên không bao giờ vào feed; `read_updated` qua `readcast`. Event member có bản room và bản user (`recipient` → `evt.{t}.user.{u}.{type}`).

**Architecture:**
- Task 1: đổi tên field (codec, index, feed, storetest, itest) + clear history theo thời gian (proto, `view`, `mutate`, `grpcsrv`, corecli). `make infra-reset`.
- `pkg/keys.Member` (room(8) + byte user); `domain`: `MemberState`, field member mới, `Join`, `OwnerChange`, `OwnerState`, `MemberCount`, `ReadPosition`, `ReadUpdate`, lỗi member.
- `store/member.go`: port `MemberWriter`, `MemberReader`, `OwnerChanges`, `MemberCounts`, `ReadPositions`; `ChangeKind` `MemberChanged` (6). Mongo: `members` clustered + 2 index mới; `rooms.member_count_ver/owners_ver/pending_owner_change`.
- Feed: insert/replace của `members` + update có `updatedFields.ver`. `work.Record` đuôi user cho `MemberChanged`, id `g:`.
- Package mới: `ownership` (Apply/Guard), `readcast` (coalescer), `pkg/lru` (chuyển từ `actor`). `counter` tổng quát hoá (`Loop[V]`) + `counter.MemberToucher`. `dedupe.Requests` (namespace `chatim:req:`, RAM LRU + Redis).
- `actor.Router.ForgetMembers` + TTL 10s; `access` 5 action + `DefaultPolicy`; `mutate` 6 lệnh member/đọc; `grpcsrv` 6 RPC; `CreateRoom` phát bản user `member_added`.
- `effects`: `member_counter`, `owner_guard`, `member_event`; metric `owner_repaired_total`, luật `ChatimOwnerRepaired` (16 → 17).
- `/app resync` quét member của từng room theo `updated_at`; route + `corecli` + e2e.

**Tech Stack:** Go 1.26 trong Docker qua `make`; buf; mongo-driver v2 (clustered collection, `BulkWrite` upsert pipeline `$cond`, `FindOneAndUpdate`, aggregate phủ index, causal session, change stream update/replace); go-redis (dedupe Lua có sẵn); nats.go jetstream (RePublish); `testing/synctest`; goleak.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §5.1, §6.3, §6.4, §7, §8, §9, §12, §17.2 (D62, D67, D72, D79–D95); [roadmap](../roadmap.md) dòng M2b.4; thiết kế ràng buộc v2 (owner duyệt 2026-10-07).

Owner chốt 2026-10-06 (giữ nguyên):
- Quyền mặc định qua `access.Policy`: owner làm mọi việc; admin thêm người và xoá người có role `member`; chỉ owner đổi role, xoá admin/owner; ai cũng tự rời được.
- Role owner/admin/member; một group nhiều owner. Owner cuối không tự hạ role. Owner cuối rời → admin vào sớm nhất, không có thì member vào sớm nhất, hoà theo user id.
- Người mới hoặc thêm lại thấy toàn bộ lịch sử, tin cũ coi như đã đọc (`read_seq` = seq cuối lúc vào). Rời/bị xoá mất quyền đọc ngay; thêm lại thì đọc lại được.
- DM cố định 2 người: không thêm, xoá, rời, đổi role. "Đánh dấu chưa đọc" lùi vị trí đọc (kiểu Slack). Core không giới hạn số member.

Owner chốt 2026-10-07:
- Không đánh số dày ở chỗ mới; member là lớp tập, mỗi doc một counter riêng; không có log fact member.
- Tên field là từ tiếng Anh cơ bản, đầy đủ, trừ `messages`. Audit `updated_at/updated_by`; counter chỉ tăng, có lỗ, đuôi `_ver`; không dùng `version`, `change_number`. Ghim giữ pv dày (đổi tên `pin_ver`).
- Đổi tên các collection đã xây là task đầu; sau đó `make infra-reset` (chưa có dữ liệu prod).
- Clear history theo thời gian `cleared_before_time` (giờ server, `$max`); `up_to_seq` reserved.
- `AddMembers` mang `request_id`, dedupe RAM LRU + dedupe Redis, namespace riêng, TTL ~15m.
- Read receipt: DB chỉ nâng; event tới subject user để đồng bộ thiết bị; phát cho room chỉ khi DM hoặc `member_count ≤ READ_RECEIPT_MAX_MEMBERS` (mặc định 20, trần cứng 50); cửa sổ gộp 1–2s.
- `member_count` hội tụ sau ~1s, chỉ qua recount §7. Bỏ `user_rooms`; "phòng của user" dùng index `members {tenant, user_id, state, room_id}`.

Quyết định mới, Task 20 ghi vào Decision Log (§17.2). **D96–D107 của plan cũ (commit a560214, chưa từng thực thi, chưa vào Decision Log) bị huỷ toàn bộ**: fact `member_actions`, mv dày, settle-first, `memberproj`, `user_rooms` (D101 cũ), gập `mc` từ fact (D98 cũ) không còn. Số id được dùng lại từ D96:
- **D96** Tên field đầy đủ cho mọi collection trừ `messages` (bảng đổi tên ở dưới); counter `_ver`; audit `updated_at/updated_by`; fact dùng `created_at/created_by`; `reconciler_state {resume_token, cluster_time}`; bỏ đường đọc vị trí feed cũ `_id: "messages"`. Rolling deploy không áp dụng (dev reset).
- **D97** Clear history theo thời gian: `members.cleared_before_time` = giờ server lúc gọi (`$max`, ms), ẩn tin `created_at ≤` mốc trên mọi timeline; lệch vài ms quanh lúc bấm chấp nhận; chỉ member active. `ClearHistoryRequest.up_to_seq` reserved, response trả `cleared_before_time`. Sửa D85/D72 phần `cleared_before_seq`.
- **D98** Member lớp tập: `members` clustered `_id = keys.Member(room, user)`; `state` 1/2 luôn ghi rõ; `ver` (≤ MaxUint32) tăng mỗi lần đổi membership; `previous_role/previous_state`, `request_id`, `updated_*`; đọc/clear/mute không đụng `ver`. Index `{room_id, state, role, joined_at, user_id}` và `{tenant, user_id, state, room_id}`. Không quét khoảng `_id` (BinData so độ dài trước byte). Thay `user_rooms` của D72 (trả lại dạng projection khi shard).
- **D99** `AddMembers`: `request_id` bắt buộc, dedupe `chatim:req:{room}:{user}:{request_id}` (RAM LRU + Redis, TTL `CID_COMMITTED_TTL`); một `BulkWrite(ordered:false)` upsert pipeline `$cond` (doc active giữ nguyên byte); kết quả suy từ doc sau ghi (`domain.AddedBy`); luôn role `member`; `read_seq = max(read_seq, seq cuối)`; giữ `cleared_before_time`.
- **D100** Xoá/rời/đổi role không chạm owner = một update có điều kiện `ver`; lệnh chạm owner = CAS `rooms {owners_ver: k, pending_owner_change: null}` → thăng kế nhiệm trước → hạ/xoá đích (mỗi bước CAS theo `ver` đã ghi trong pending) → xoá pending khi `owners_ver == k+1`; ≤3 lượt rồi `ErrRetryLater`. Bất biến: group còn member active thì còn owner active. Effect `owner_guard` hoàn tất pending cũ hoặc chọn kế nhiệm.
- **D101** Action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read`; `Request.Target`, `Request.Role`; `DefaultPolicy` theo owner chốt. DM → `FAILED_PRECONDITION`; owner cuối tự hạ → `ErrLastOwner`; `RemoveMember(self)` → `INVALID_ARGUMENT`; người chưa từng là member rời → `PERMISSION_DENIED`; tombstone rời → no-op; xoá/đổi role người không active → `NOT_FOUND`.
- **D102** `member_count` = aggregate (tinh chỉnh D67): chỉ worker `member_counter` ghi, recount có witness `(user, max ver)`, đếm phủ index `{room_id, state}`, CAS `member_count_ver`; event `member_count_changed`; `counter` tổng quát hoá `Loop[V]`.
- **D103** Feed thêm insert/replace của `members` và update có `updatedFields.ver`; kind `MemberChanged` (6) đuôi user, version = `ver`; record id `g:{room}-mb-{user}-v{ver}`; registry `room_activity`, `member_counter`, `owner_guard`, `member_event`; resync quét member của từng room lọc `updated_at`.
- **D104** Event member: bản room `{room}-mb-{user}-v{ver}` + bản user `…-u` (`Event.recipient = 10` → `evt.{t}.user.{u}.{type}`; RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}`); doc tạo cùng room (`ver 1`, `request_id = {room}-created`) không có bản room; payload không có `new_owner` (event `member_role_changed` của người kế nhiệm mang thông tin đó); không ack mark.
- **D105** Vị trí đọc `read_seq/read_ver` chỉ toán tử thường (`MarkRead` `$lt` nâng, `MarkUnread` `$gt` hạ về seq−1, `$inc read_ver`); `read_updated` qua `readcast` (gửi ngay lần đầu, gộp đuôi trong `READ_RECEIPT_WINDOW` 1–2s, mặc định 1s); subject room khi DM hoặc `member_count ≤ READ_RECEIPT_MAX_MEMBERS` (20, trần 50), không thì subject user; id `{room}-rd-{user}-v{read_ver}`; bước dừng 1s (26.2s → 27.2s).
- **D106** Cache member của actor: thế hệ (`Router.ForgetMembers`) + TTL 10s; TTL là giới hạn xuyên core (worker không xoá được cache core khác).
- **D107** `MEMBER_BATCH_MAX` (2..1000, mặc định 500) cho `CreateRoom` và `AddMembers`; bỏ trần 5000 trong `domain.NewRoom`.
- **D108** Guarantee OW1 "group có member active thì có owner active": metric `owner_repaired_total`, luật `ChatimOwnerRepaired` (17 luật).

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API: `make -s go ARGS="doc <pkg> <Symbol>"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới.
- File code dưới 200 dòng; `wc -l` sau mỗi lần sửa file lớn.
- `gosec` G115: không chuyển `int`/`int64` ↔ `uint*` khi chưa chặn biên. Mongo lưu `ver`, `read_seq`, `read_ver`, `member_count_ver`, `owners_ver`, `room_id` dạng int64; đọc khoan dung `AsInt64OK` (int32/int64/double); âm hoặc vượt kiểu Go → `errCorrupt`. `ver` > `MaxUint32` hay `*_ver` > `MaxInt64` bị store từ chối.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5` trên package đó.
- **Plan này chưa từng được biên dịch.** Sửa thuần cơ học để biên dịch (import thiếu, tên biến trùng, kiểu trả về lệch một chữ, idiom lint có hành vi y hệt) được áp và **phải ghi vào báo cáo**. Lệch khác, kết quả khác "Expected", test cũ fail: **dừng và báo cáo**.
- Đổi adapter Mongo/Redis/NATS hoặc wiring `apps/core`: `make itest` một lần cuối task (cần `make infra-up`). `make itest` **không nhận `ARGS`**; chạy riêng một test bằng makefile trong scratchpad (không commit), chạy từ gốc repo:

  ```make
  include Makefile
  itest-one: check-env
  	$(GO_RUN) --network $(NETWORK) -e CHATIM_IT_MONGO_URI -e CHATIM_IT_REDIS_ADDR=chatim-redis:6379 -e CHATIM_IT_REDIS_PASSWORD -e CHATIM_IT_REDIS_DEDUPE_ADDR=chatim-redis-dedupe:6379 -e CHATIM_IT_REDIS_DEDUPE_PASSWORD -e CHATIM_IT_NATS_URL=nats://chatim-nats:4222 $(GO_IMAGE) go test -race -count=1 -run '$(RUN)' $(PKG)
  ```

  `make -f <scratchpad>/itest-one.mk itest-one RUN=TestX PKG=./apps/core/...`.
- **Reset dữ liệu dev:** sau commit Task 1 và sau commit Task 5 chạy `make infra-reset` rồi `make infra-up` trước `make itest` (tên field đổi; `members` chuyển sang clustered nên `Bootstrap` báo `ErrNotClustered` trên collection cũ). Core đang chạy (`core-up`) phải build lại.
- `INDEXES.csv`: field có dấu phẩy (thường `purpose`, `key_symbols`, `decisions`) **phải đặt trong ngoặc kép**. Sau mỗi lần sửa: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → `{7}`.
- Commit theo Conventional Commits, không nhắc AI, không dòng `Co-Authored-By`; `git add` đúng file, `git commit -- <paths>`; cấm `add -A`, `commit -a`, `stash`. Nhánh `feat/m2b`. **Push sau Task 7, Task 15, Task 20** (`git push origin feat/m2b`).
- Review: Task ★ (1, 3, 5, 6, 8, 9, 10, 11, 12, 13, 14, 15, 16) → mỗi task 1 reviewer (spec + chất lượng một lượt, tối đa `-count=3` trên package đụng). Task khác controller kiểm nhanh. Lỗi Minor ghi vào "Kết quả thực thi".

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

| Tính năng | Lớp (§4) | Ghi | Idempotent | Effect + chính sách | Quyền | View | Event (id) | Khuếch đại nhóm 5K | Khuếch đại channel 200K | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|---|
| Thêm member | Tập (room, user) | `BulkWrite` k upsert pipeline `$cond` trên `_id`; doc active giữ nguyên byte | `request_id` (dedupe 15m) + trạng thái mong muốn; retry trễ không thêm lại người đã bị xoá | fast path enqueue 2k event (mỗi người một bản room + một bản user; tạo room thì chỉ bản user); worker `room_activity` (0), `member_counter` (`MEMBER_COUNT_DELAY`), `owner_guard` (`RECONCILE_DELAY`), `member_event` (`RECONCILE_DELAY`, không mark; doc `ver == rec` mới phát) | `add_members` (owner/admin) | `Admit` đọc doc, `state` phải 1 | `member_added` `{room}-mb-{u}-v{ver}` + `…-u` | 2 read admit + 1 Redis + 1 `Last` + 1 bulk k + 1 find k + 2k event; worker: ≤1 write activity/room/lô + 1 recount/room/lô + 2k publish (bỏ trùng) | như 5K mỗi lệnh; recount quét index ~200K khoá (20–60ms) ≤1 lần/s/room nóng | MB1: `effect_dropped_total{effect="member_event"}`, `reconcile_republished_total{effect="member_event"}`, `work_failures_total` |
| Xoá / rời / đổi role (không chạm owner) | Tập | 1 update `{_id, ver: k}` → `$set` + `$inc ver` | trạng thái mong muốn; trượt → đọc lại ≤3 | như trên | `remove_member`, `leave_room`, `change_member_role` | người bị xoá bị từ chối ngay ở core xử lý lệnh, ≤10s ở core khác (TTL actor) | `member_removed` / `member_role_changed` | 2 read + 1 read đích + 1 write + 2 event | như 5K | MB1 |
| Lệnh chạm owner | Tập + CAS đầu room | CAS `rooms.owners_ver` + `pending_owner_change` → thăng kế nhiệm → hạ/xoá đích → xoá pending | pending mang `ver` từng doc; chạy lại an toàn | `owner_guard` (`RECONCILE_DELAY`, 1 lần/room/lô) | như trên | — | như trên (kế nhiệm có `member_role_changed`) | +1 read state, ≤2 read index, 1 CAS, 2–3 write | như 5K (hiếm) | OW1 (mới): `owner_repaired_total`, alert `ChatimOwnerRepaired`; `effect_dropped_total{effect="owner_guard"}` |
| Số member | Aggregate theo room | recount `{room_id, state:1}` phủ index + CAS `member_count_ver` (thiếu = 0) | recount tuyệt đối; bằng → không ghi | chỉ worker `member_counter`; phát lại `member_count_changed` của ver hiện tại | — | `Room.member_count` (trễ ~1s) | `member_count_changed` `{room}-members-v{ver}` | 1 read witness k + 1 count ≤5K khoá + ≤1 CAS + 1 event/room/lô | ≤200K khoá/lần, ≤1 lần/s/room; bucket để milestone Channel | CT1 mở rộng: `effect_dropped_total{effect="member_counter"}`, `work_failures_total` |
| Vị trí đọc / chưa đọc | Tần suất cao gộp được | `updateOne {_id, state:1, read_seq: {$lt\|$gt}}` `$set read_seq` + `$inc read_ver` | chỉ nâng / chỉ hạ; `read_ver` LWW | không effect, không feed | `mark_read` | trả `{read_seq, read_ver}` | `read_updated` `{room}-rd-{u}-v{read_ver}` qua `readcast` | 2 read admit + ≤1 `Last` + 1 write + ≤2 event/W/(room,user) | như 5K; luôn subject user | best-effort; `read_events_unbatched_total` |
| Clear history | Giá trị theo người đọc | `$max cleared_before_time = now` (member active) | `$max` | không | `clear_history` | `HideForViewer` so `CreatedAt` | không | 2 read + 1 write | như 5K | không |
| Phòng của user | (index) | — | — | — | — | M3 `ListMyRooms` | — | — | range index `{tenant, user_id, state, room_id}` | — |

## Bảng đổi tên field (Task 1)

`messages` **giữ nguyên** (`_id`, `t`, `f`, `k`, `x`, `c`, `ts`, `v`, `d`, `ea`, `rx {c: [{e, n}], v}`); Task 20 ghi bảng ánh xạ vào thiết kế §5. Tên Go (struct domain, field Go) **không đổi** trừ `ClearedBeforeSeq` → `ClearedBeforeTime`. Tên `_id` và giá trị `_id` không đổi.

| Collection | Cũ → mới |
|---|---|
| `rooms` | `t` → `tenant`; `ty` → `type`; `n` → `name`; `cb` → `created_by`; `ca` → `created_at`; `mc` → `member_count`; `ls` → `last_seq`; `lm` → `last_message_at`; `lc` → `last_change_at`; `ab` → `activity_bucket`; `pins[]` giữ tên, phần tử `{th, s, by, ts, pv}` → `{thread_root, seq, pinned_by, pinned_at, pin_ver}`; `pv` → `pin_ver` |
| `members` | `r` → `room_id`; `u` → `user_id`; `t` → `tenant`; `ro` → `role`; `ja` → `joined_at`; `cb` (int64 seq) → `cleared_before_time` (date) |
| `message_edits` | `r` → `room_id`; `t` → `tenant`; `k` → `kind`; `by` → `created_by`; `x` → `text`; `p` → `previous_text`; `ts` → `created_at` |
| `hidden` | `u` → `user_id`; `r` → `room_id`; `th` → `thread_root`; `s` → `seq` |
| `reactions` | `k` → `message_key`; `r` → `room_id`; `t` → `tenant`; `u` → `user_id`; `e` → `emoji`; `pe` → `previous_emoji`; `n` → `ver`; `ts` → `updated_at` |
| `pin_actions` | `r` → `room_id`; `t` → `tenant`; `op` → `action`; `th` → `thread_root`; `s` → `seq`; `by` → `created_by`; `ts` → `created_at` |
| `reconciler_state` | `token` → `resume_token`; `at` → `cluster_time` (doc `_id: "changes"`); bỏ đọc/xoá doc cũ `_id: "messages"` (`legacyMessagesFeedID`, `feedStart`) và test của nó |

Index (tên tự sinh theo khoá, `Bootstrap` tạo mới sau reset):

| Collection | Cũ → mới |
|---|---|
| `rooms` | `{ab:1}` → `{activity_bucket:1}`; `{ca:1}` → `{created_at:1}` |
| `members` | `{r:1,u:1}` unique → `{room_id:1,user_id:1}` unique; `{t:1,u:1,r:1}` → `{tenant:1,user_id:1,room_id:1}` (Task 5 thay cả hai, xem Mongo) |
| `message_edits`, `pin_actions` | `{r:1,ts:1}` → `{room_id:1,created_at:1}` (`roomTimeIndexes`) |
| `reactions` | `{k:1,e:1}` → `{message_key:1,emoji:1}`; `{r:1,ts:1}` → `{room_id:1,updated_at:1}` (hàm riêng, không dùng `roomTimeIndexes`) |
| `hidden` | `{u,r,th,s}` unique → `{user_id:1,room_id:1,thread_root:1,seq:1}` unique |

Chỗ dùng tên thô ngoài codec (phải đổi cùng): `rooms.go` (projection `{pins:0, pin_ver:0}`, filter member, `$max cleared_before_time`), `pin_state.go` (projection + CAS `versionIs("pin_ver", …)`), `room_activity.go` (`$max last_change_at/activity_bucket/last_seq/last_message_at`; `ActiveRooms` lọc `activity_bucket`, `created_at`, `tenant`), `edits.go` (`Between` lọc/sắp `room_id, created_at`; `PurgeText` `$unset text, previous_text`), `pins.go` (`Between`), `hidden.go`, `reactions.go` (pipeline `$cond` trên `emoji`, `$ifNull ver`), `reaction_count.go` (witness projection `{user_id, ver}`, aggregate `{$match: {message_key, emoji: {$gt: ""}}}`, `{$group: {_id: "$emoji", count: {$sum: 1}}}`, `Between` theo `updated_at`), `feed.go` (`feedPosition`), `feed_reaction_change.go` (**`updatedFields.n` → `updatedFields.ver`**), `bootstrap.go` (`$ifNull ["$cluster_time", start]`). Test thô: `feed_anchor_integration_test.go`, `feed_change_test.go`, `feed_reaction_change_test.go`, `bootstrap*_integration_test.go`.

Proto (Task 1): `ClearHistoryRequest { string room_id = 1; reserved 2; reserved "up_to_seq"; }`, `ClearHistoryResponse { reserved 1; reserved "cleared_before_seq"; google.protobuf.Timestamp cleared_before_time = 2; }`. **Không id event/record nào đổi** (`-n{n}` của reaction là id, không phải field).

## Hợp đồng chung (mọi task phải khớp đúng chữ ký này)

Struct/interface viết gọn một dòng chỉ là ký hiệu; code thật để `gofmt` dàn dòng, giữ đúng tên, kiểu và thứ tự field. Field mới thêm **cuối** struct trừ khi ghi khác.

### Clear history theo thời gian (Task 1)

```go
type Member struct { Room uint64; Tenant, User string; Role Role; JoinedAt time.Time; ClearedBeforeTime time.Time }

type HistoryClearer interface { ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error) }

type Viewer struct { User string; Room domain.Room; ClearedBeforeTime time.Time; HiddenSeqs map[uint64]bool }

type ClearCmd struct{ Tenant, User string; Room uint64 }
func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (time.Time, error)
```

- `ClearHistory(at)`: `$max cleared_before_time = at`, trả giá trị sau ghi (`FindOneAndUpdate` After); không có doc → `domain.ErrNotMember`. Task 5 thêm điều kiện `state: 1`.
- `mutate.ClearHistory`: `Authorize(ClearHistory)` → `Rooms.ClearHistory(room, user, m.now())` (bỏ `Messages.Last`).
- `view.HideForViewer`: ẩn khi `!v.ClearedBeforeTime.IsZero() && !m.CreatedAt.After(v.ClearedBeforeTime)` hoặc seq trong `HiddenSeqs`. `grpcsrv.viewerOf`: bỏ `HiddenIn` khi `CreatedAt` lớn nhất của trang ≤ mốc.
- `grpcsrv.ClearHistory` trả `ClearedBeforeTime: timestamppb.New(t)`. `corecli clear` bỏ `-seq`; route giữ nguyên chữ ký.

### `pkg/keys` (Task 2)

```go
const MaxMemberUser = 64
func Member(room uint64, user string) []byte
func ParseMember(b []byte) (room uint64, user string, err error)
```

- `Member` = room(8) big-endian nối byte của user. `ParseMember`: `8 < len(b) ≤ 8 + MaxMemberUser`, không thì `ErrLength`.
- **Không quét khoảng `_id` của `members`**: Mongo so BinData theo độ dài trước, rồi subtype, rồi byte, nên khoảng byte không phải khoảng tiền tố room. Mọi truy vấn theo room đi qua index `room_id`; theo người dùng `_id` bằng (`$in`).

### `apps/core/internal/domain` (Task 2; trần Task 13)

```go
const RoleAdmin Role = "admin"
const MaxMemberBatch = 1000

type MemberState int32
const MemberActive, MemberRemoved MemberState = 1, 2

type Member struct { /* 6 field Task 1 */ ; State MemberState; Ver uint32; PreviousRole Role; PreviousState MemberState; RequestID string; UpdatedAt time.Time; UpdatedBy string; ReadSeq, ReadVer uint64 }

type Room struct { /* field cũ */ ; MemberCountVer uint64 }

type Join struct{ Room uint64; Tenant, RequestID, By string; At time.Time; ReadSeq uint64 }

type MemberCount struct{ Count int; Ver uint64 }

type OwnerAction string
const OwnerLeave, OwnerRemove, OwnerChangeRole, OwnerRepair OwnerAction = "leave", "remove", "change_role", "repair"

type OwnerChange struct { Action OwnerAction; User string; UserVer uint32; Role Role; Successor string; SuccessorVer uint32; RequestID, UpdatedBy string; UpdatedAt time.Time }

type OwnerState struct{ Ver uint64; Pending *OwnerChange }
type ReadPosition struct{ Seq, Ver uint64 }

type ReadUpdate struct{ Room uint64; Tenant string; Type RoomType; Members int; User string; Pos ReadPosition; At time.Time }

var (
	ErrDirectRoom     = fmt.Errorf("direct room members are fixed: %w", apperr.ErrFailedPrecondition)
	ErrLastOwner      = fmt.Errorf("last owner cannot step down: %w", apperr.ErrFailedPrecondition)
	ErrMemberNotFound = fmt.Errorf("member %w", apperr.ErrNotFound)
	ErrTooManyMembers = fmt.Errorf("too many members in one request: %w", apperr.ErrInvalidArgument)
)

func ParseRole(s string) (Role, error)
func CreationRequestID(room uint64) string
func (m Member) Active() bool
func (m Member) Next(role Role, state MemberState, requestID, by string, at time.Time) Member
func (j Join) Apply(cur Member, user string) Member
func AddedBy(m Member, requestID, by string) bool
```

- `ParseRole`: `owner|admin|member`, khác → `invalid("role")`. `CreationRequestID(room)` = `strconv.FormatUint(room, 10) + "-created"` (hợp lệ theo `ValidCID`).
- `Next`: bản sao `m` với `Role/State` mới, `PreviousRole = m.Role`, `PreviousState = m.State`, `Ver = m.Ver + 1`, `RequestID`, `UpdatedBy = by`, `UpdatedAt = at`; giữ `JoinedAt`, `ClearedBeforeTime`, `ReadSeq`, `ReadVer`. Ngữ nghĩa chuẩn của `ApplyMember` cho mọi adapter.
- `Join.Apply(cur, user)` (ngữ nghĩa chuẩn của `AddMembers`; `cur` zero = chưa có doc): `cur.Active()` → trả `cur` y nguyên. Không thì `{Room, Tenant, User: user, Role: RoleMember, State: MemberActive, JoinedAt: At, Ver: cur.Ver+1, PreviousRole: cur.Role, PreviousState: cur.State, RequestID, UpdatedBy: By, UpdatedAt: At, ReadSeq: max(cur.ReadSeq, j.ReadSeq), ReadVer: cur.ReadVer+1, ClearedBeforeTime: cur.ClearedBeforeTime}`.
- `AddedBy(m, rid, by)` = `m.Active() && m.RequestID == rid && m.UpdatedBy == by`.
- `NewRoom` (Task 2): mỗi member có `State: MemberActive, Ver: 1, RequestID: CreationRequestID(id), UpdatedAt: now, UpdatedBy: creator` (`ReadSeq/ReadVer` 0). Task 13 bỏ `maxGroupMembers` (DM vẫn đúng 2; group ≥ 1).

### `apps/core/internal/store` (port Task 4; Mongo Task 5; feed Task 6)

```go
const MaxMemberScan = 1000

type MemberWriter interface {
	AddMembers(ctx context.Context, j domain.Join, users []string) ([]domain.Member, error)
	ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error)
}

type MemberReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error)
	Successor(ctx context.Context, room uint64) (domain.Member, bool, error)
	MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error)
}

type OwnerChanges interface {
	OwnerState(ctx context.Context, room uint64) (domain.OwnerState, error)
	BeginOwnerChange(ctx context.Context, room, base uint64, c domain.OwnerChange) (bool, error)
	EndOwnerChange(ctx context.Context, room, ver uint64) (bool, error)
}

type MemberCounts interface {
	CountMembers(ctx context.Context, room uint64, witnesses []Witness) (int, error)
	SetMemberCount(ctx context.Context, room, base uint64, c domain.MemberCount) (bool, error)
}

type ReadPositions interface {
	MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error)
	MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error)
}

const MemberChanged ChangeKind = 6

func ValidateJoin(j domain.Join, users []string) error
func ValidateMemberChange(cur, next domain.Member) error
func ValidateOwnerChange(c domain.OwnerChange) error
```

- File mới `store/member.go`, `store/member_validate.go`. `Change` thêm `Member domain.Member` ngay sau `Pin`. `Witness.N` = `ver` của member (dùng lại `store.Witness`). `ErrStaleRead` dùng lại.
- `Rooms.Member`: doc không có **hoặc** `state ≠ 1` → `domain.ErrNotMember`. `Rooms.Get` đọc thêm `member_count_ver` (thiếu = 0). `Rooms.Create(r, members)` giữ chữ ký: insert room (`member_count = len`, không `member_count_ver`) rồi insert member đã có đủ field từ `NewRoom` (trùng khoá bỏ qua như cũ). `HistoryClearer.ClearHistory`: chỉ member active.
- `ValidateJoin`: room ≠ 0, `ValidTenant`, `ValidCID(RequestID)`, `ValidUser(By)`, `At` khác zero, `ReadSeq ≤ MaxInt64`, users 1..`domain.MaxMemberBatch`, mỗi `ValidUser`, không lặp. `ValidateMemberChange`: cùng room/user, `cur.Ver ≥ 1`, `next.Ver == cur.Ver+1 ≤ MaxUint32`, state 1|2, role hợp lệ, `ValidCID(next.RequestID)`, `next.UpdatedBy` hợp lệ hoặc rỗng. `ValidateOwnerChange`: action hợp lệ; `repair` → `User == ""`, `Successor` hợp lệ; khác → `User` hợp lệ, `UserVer ≥ 1`; `change_role` → `Role` hợp lệ; `Successor != ""` ⇔ `SuccessorVer ≥ 1`; `RequestID` hợp lệ.
- `AddMembers(j, users)`: mỗi user ghi `j.Apply(cur, user)` (no-op khi active, không ghi gì); trả doc **sau ghi** theo thứ tự `users` (đọc lại sau bulk). Không đụng `rooms`.
- `ApplyMember(cur, next)`: `ValidateMemberChange`; ghi khi doc vẫn có `ver == cur.Ver` → `(true, nil)`; trượt hoặc không có doc → `(false, nil)`. Ghi đúng các field `Next` đổi; không đụng `read_*`, `cleared_before_time`, `joined_at`.
- `MembersOf(room, users)`: doc mọi state; user không có doc bị bỏ; ≤ `MaxMemberBatch + 1` user. `Owners(limit)`: owner active, sắp `joined_at` rồi `user_id`, limit 1..`MaxMemberScan`. `Successor`: member active role ≠ owner: admin trước member, rồi `joined_at`, rồi `user_id`; không có → `false`. `MembersBetween`: doc mọi state của room có `updated_at ∈ [from, to]`, sắp `updated_at` rồi `_id`, limit 1..`MaxMemberScan` (`ValidateLimit`).
- `OwnerState`: room không có → `domain.ErrRoomNotFound`. `BeginOwnerChange(room, base, c)`: `ValidateOwnerChange`; `$set owners_ver: base+1, pending_owner_change: c` chỉ khi `owners_ver == base` (base 0 → không tồn tại) **và** `pending_owner_change` null/thiếu; trượt → `(false, nil)`. `EndOwnerChange(room, ver)`: `$unset pending_owner_change` khi `owners_ver == ver`; không khớp → `(false, nil)`.
- `CountMembers(room, ws)`: mọi witness phải có doc `ver ≥ N`, không thì `ErrStaleRead`; trả số doc `state == 1`. `SetMemberCount(room, base, c)`: cần `c.Ver == base+1`; `$set member_count, member_count_ver` khi `member_count_ver == base` (base 0 → không tồn tại); trượt/không có room → `(false, nil)`.
- `MarkRead(room, user, seq)`: member active và `read_seq < seq` → `read_seq = seq`, `read_ver+1` → `(mới, true, nil)`; không đổi → `(hiện tại, false, nil)`; không active → `ErrNotMember`. `MarkUnread(…, to)`: điều kiện `read_seq > to`, `read_seq = to`. `seq`/`to` ≤ `MaxInt64`.
- Write contract: `AddMembers` = `version-bump`; `ApplyMember` = `cas`; `MemberReader.*` = `read`; `OwnerState` = `read`; `BeginOwnerChange`, `EndOwnerChange` = `cas`; `CountMembers` = `read`; `SetMemberCount` = `cas`; `MarkRead`, `MarkUnread` = `version-bump`. Thêm 5 port vào danh sách reflect.
- memstore: Task 4 cho `*Rooms` cài cả 5 port. Task 6 thêm field `member domain.Member` vào `logged` và gọi `s.log.appendFact(logged{kind: store.MemberChanged, member: …})` ở mọi ghi đổi `ver` (insert member trong `Create`, user đổi trong `AddMembers`, `ApplyMember` khớp); cursor trả `Change.Member`. Đọc/clear/`owners_ver`/`member_count` không log.
- storetest: `type MemberRooms interface { store.Rooms; store.HistoryClearer; store.MemberWriter; store.MemberReader; store.OwnerChanges; store.MemberCounts; store.ReadPositions }`; `RunMembers(t, open func(*testing.T) MemberRooms)` (Task 4); `RunMemberFeed(t, open func(*testing.T) (MemberRooms, store.ChangeFeed))` (Task 6). Chạy trên memstore và mongostore (itest).

### Mongo (Task 5; feed Task 6)

- `members` (clustered, `ensureClustered`): `{_id: keys.Member(room, user), room_id: int64, tenant, user_id, role, state: int32, joined_at, ver: int64, previous_role, previous_state: int32, request_id, updated_at, updated_by, cleared_before_time (omitempty), read_seq: int64, read_ver: int64}`. `state`, `ver`, `read_seq`, `read_ver` **luôn ghi** (filter `$lt` không khớp field thiếu). `memberIndexes()` = `{room_id:1, state:1, role:1, joined_at:1, user_id:1}` và `{tenant:1, user_id:1, state:1, room_id:1}`; bỏ hai index cũ. File mới `member_codec.go` (chuyển `memberDoc`, `encodeMember`, `decodeMember` ra khỏi `codec.go`), `members.go`, `member_owner.go`, `member_count.go`, `read_position.go`.
- `rooms` thêm `member_count_ver: int64` (omitempty), `owners_ver: int64`, `pending_owner_change: {action, user_id, user_ver, role, successor_id, successor_ver, request_id, updated_by, updated_at}` (omitempty). `Rooms.Get` giữ projection loại `pins`, `pin_ver`.
- `AddMembers` = một `BulkWrite(ordered:false)` các `UpdateOne({_id}, pipeline, upsert)`; pipeline một `$set`, `active := {$eq: ["$state", 1]}`, mọi field ngoài `room_id/tenant/user_id/state` là `{$cond: [active, "$<field>", <mới>]}`; `previous_role: $ifNull ["$role", ""]`, `previous_state: $ifNull ["$state", 0]`, `ver/read_ver: $ifNull + 1`, `read_seq: {$max: [{$ifNull: ["$read_seq", 0]}, j.ReadSeq]}`; mọi chuỗi người dùng qua `$literal`. Filter chỉ `_id` bằng nên server tự retry upsert trùng khoá. Sau đó `MembersOf` (primary) trả doc sau ghi.
- `ApplyMember` = `UpdateOne({_id, ver: cur.Ver}, {$set: {role, state, previous_role, previous_state, request_id, updated_by, updated_at}, $inc: {ver: 1}})`. `MarkRead` = `FindOneAndUpdate({_id, state: 1, read_seq: {$lt: s}}, {$set: {read_seq: s}, $inc: {read_ver: 1}}, After)`; không khớp → `FindOne({_id})` phân biệt active / không. `ClearHistory` = `FindOneAndUpdate({_id, state: 1}, {$max: {cleared_before_time: at}}, After)`. **Ba lệnh này không bao giờ dùng pipeline, replace hay upsert.**
- `CountMembers`: đọc witness `{_id: {$in}}` projection `{user_id, ver}` majority trong session causal, rồi `CountDocuments({room_id, state: 1})` cùng session (phủ index). `Owners`/`Successor`: `Find({room_id, state: 1, role})` sắp `{joined_at:1, user_id:1}`. `MembersBetween`: `Find({room_id, updated_at: {$gte, $lte}})` sắp `{updated_at:1, _id:1}` (dùng tiền tố `room_id` của index đếm; không index thời gian).
- `Store` cài 5 port (tên method không trùng method cũ của `*Store`; vì `Edits.Between` đã có nên là `MembersBetween`). Index `{tenant, user_id, state, room_id}` chỉ chuẩn bị cho M3 `ListMyRooms`; M2b.4 **không** thêm port "phòng của user" (YAGNI).
- Feed `$match` (Task 6) = `$or` của: insert trên `[messages, rooms, message_edits, reactions, pin_actions, members]`; update/replace trên `reactions`; replace trên `members`; `{operationType: "update", "ns.coll": "members", "updateDescription.updatedFields.ver": {$exists: true}}`. File mới `feed_member_change.go`: insert/replace → giải mã `fullDocument`; update → `keys.ParseMember(documentKey._id)` + `updatedFields.ver` (1..`MaxUint32`, user hợp lệ, không thì `errCorrupt`) → `Change{Kind: MemberChanged, Member: {Room, User, Ver}}` (update chỉ chắc có ba field này).

### `apps/core/internal/work` + `reconcile` (Task 4 case `RecordOf`, Task 6)

- `RecordOf`: `MemberChanged` → `Room = c.Member.Room`, `User = c.Member.User`, `Version = c.Member.Ver` (thread, seq 0).
- Task 6: `KnownKind` nhận `MemberChanged`; `checkKind` bắt buộc đuôi user hợp lệ cho `ReactionChanged` **và** `MemberChanged`, cấm cho kind khác; `ID()` → `"g:" + pbconv.MemberEventID(r.Room, r.User, r.Version)`. Reader forward kind mới (không đổi code reader). `effects_wiring.go` đăng ký tạm `store.MemberChanged: {activity.Effect()}`; Task 16 thay bằng registry cuối.

### `apps/core/internal/counter` (Task 7)

```go
var ErrContended = fmt.Errorf("counter contended: %w", apperr.ErrUnavailable)

type Loop[V any] struct {
	Count  func(ctx context.Context) (V, error)
	Equal  func(a, b V) bool
	Swap   func(ctx context.Context, base uint64, next V) (bool, error)
	Reload func(ctx context.Context) (V, uint64, error)
}

func (l Loop[V]) Run(ctx context.Context, cur V, ver uint64, tries int) (V, uint64, bool, error)

type MemberRooms interface { Get(ctx context.Context, id uint64) (domain.Room, error); store.MemberCounts }

func NewMembers(rooms MemberRooms) (*MemberToucher, error)
func (t *MemberToucher) Touch(ctx context.Context, room uint64, cur domain.MemberCount, witnesses []store.Witness, tries int) (domain.MemberCount, bool, error)
```

- `Run`: `tries < 1` → `ErrInvalidArgument`; mỗi lượt `Count` → `Equal` → trả `(cur, ver, false, nil)` → `Swap(ver, next)` khớp → `(next, ver+1, true, nil)` → trượt → `Reload` lấy `(cur, ver)` mới. Hết lượt → `ErrContended`. Lỗi trả nguyên.
- `Toucher.Touch` (reaction) giữ chữ ký và hành vi, dựng lại trên `Loop[[]domain.ReactionCount]`. `MemberToucher.Touch` = `Loop[int]` với `CountMembers(room, witnesses)`, `SetMemberCount(room, base, {next, base+1})`, `Reload` = `Get` (room không có → `domain.ErrRoomNotFound`).

### `pkg/lru` + `apps/core/internal/dedupe` (Task 8)

```go
func New[K comparable, V any](limit int) *Cache[K, V]
func (c *Cache[K, V]) Get(k K) (V, bool)
func (c *Cache[K, V]) Put(k K, v V)
func (c *Cache[K, V]) Remove(k K)
func (c *Cache[K, V]) Len() int


type Space uint8
const SpaceCID, SpaceRequest Space = 0, 1

type Key struct { Room uint64; User, CID string; Space Space }
type Registry interface { /* Reserve, Commit, Abort như registry cũ, đổi tên exported */ }

type RequestStatus uint8
const RequestNew, RequestDone, RequestBusy RequestStatus = 1, 2, 3

const RequestCacheSize = 4096

func RequestKey(room uint64, user, requestID string) Key
func NewRequests(reg Registry, ttl time.Duration) (*Requests, error)
func (r *Requests) Begin(ctx context.Context, key Key) (RequestStatus, error)
func (r *Requests) Finish(ctx context.Context, key Key, rec Record)
func (r *Requests) Cancel(ctx context.Context, key Key)
```

- `Key.String()`: `SpaceRequest` → `chatim:req:{room}:{user}:{request_id}`, còn lại như cũ (`chatim:cid:`). Giá trị Redis, Lua, TTL dùng chung (`CID_PENDING_TTL` 10s, `CID_COMMITTED_TTL` 15m). Với khoá request, `Record.Seq` = số user của lệnh (≥ 1), `CreatedAt` = giờ lệnh.
- `Begin`: LRU (mutex + `lru.Cache`, `RequestCacheSize`, hết hạn sau `ttl`) có → `RequestDone`. Không thì `Reserve([key])`: `Reserved`/`Absent` → `RequestNew`; `Committed` → nạp LRU, `RequestDone`; `PendingHere`/`PendingElsewhere` → `RequestBusy`; lỗi Redis/`ErrDegraded` → `RequestNew` (chỉ còn LRU, như CD2). Lỗi chỉ khi `ctx` huỷ. `Finish` = `Commit` + nạp LRU; `Cancel` = `Abort`. Cả hai không chặn (batcher xếp hàng).
- `apps/core/internal/actor` chuyển sang `pkg/lru` (xoá `lru_cache.go`, test chuyển theo) trong Task 8.

### `apps/core/internal/actor` (Task 9)

```go
const memberCacheTTL = 10 * time.Second
func (r *Router) ForgetMembers(room uint64)
```

- Actor thêm `memberGen atomic.Uint64`, `seenGen uint64`; `ForgetMembers` giữ `r.mu.RLock`, actor có thì `memberGen.Add(1)`. `member()`: `memberGen.Load() != seenGen` → LRU mới, cập nhật `seenGen`; entry `{m domain.Member, at time.Time}`, quá TTL → đọc lại. Không cache "không phải member". Logic trong file mới `member_cache.go`. Test `synctest`, `-count=5`.

### `apps/core/internal/access` (Task 10)

```go
const AddMembers, RemoveMember, LeaveRoom, ChangeMemberRole, MarkRead Action = "add_members", "remove_member", "leave_room", "change_member_role", "mark_read"

type Request struct { /* field cũ */ ; Target domain.Member; Role domain.Role }
```

- `DefaultPolicy.Check` (luật edit/delete giữ nguyên): `AddMembers` → `Member.Role` owner|admin; `RemoveMember` → owner, hoặc admin khi `Target.Role == member`; `ChangeMemberRole` → chỉ owner; `LeaveRoom`, `MarkRead` → cho phép. Sai → `ErrDenied`. `LockedKinds` không áp. `AllowMembers` không đổi. `Checker` không đổi (`Admit` dựa `Rooms.Member` đã lọc `state`).

### `apps/core/internal/ownership` (Task 11, package mới)

```go
type Store interface { store.MemberReader; store.MemberWriter; store.OwnerChanges }

const MaxTries = 3

type Authorize func(ctx context.Context, caller, target domain.Member) error

type Change struct { Room uint64; Action domain.OwnerAction; Caller, Target string; Role domain.Role; RequestID string; At time.Time; Authorize Authorize }

type Result struct{ Target, Successor domain.Member; Changed bool }

func New(st Store) (*Coordinator, error)
func (c *Coordinator) Apply(ctx context.Context, ch Change) (Result, error)
func (c *Coordinator) Guard(ctx context.Context, room uint64, at time.Time) (bool, error)
```

- `Apply`, tối đa `MaxTries` lượt: `OwnerState` → có pending → `finish` nó rồi lượt sau → đọc caller + đích (`MembersOf`) → caller không active → `ErrNotMember` → `Authorize` (nil thì bỏ) → đích không active hoặc đã đúng trạng thái → `Result{Changed: false}` → `Owners(room, 2)` → luật: hạ owner cuối (`change_role`, role ≠ owner, chỉ còn 1 owner) → `ErrLastOwner`; owner cuối rời → `Successor` (không có → không kế nhiệm, group rỗng) → `BeginOwnerChange(room, k, {…, UserVer, SuccessorVer})` trượt → lượt sau → `finish(k+1)`. Hết lượt → `domain.ErrRetryLater`.
- `finish(ver, p)`: (1) có `Successor`: doc `ver == SuccessorVer` → `ApplyMember(doc, doc.Next(owner, active, p.RequestID, p.UpdatedBy, p.UpdatedAt))`; doc đã là `Next` đó (`ver == SuccessorVer+1`, role owner, cùng `request_id`) → đã áp; khác (đã bị đổi) → `EndOwnerChange`, trả `domain.ErrRetryLater` (không áp đích). (2) đích (khác `repair`): cùng quy tắc theo `UserVer` với `leave/remove` → `state 2` (role giữ), `change_role` → `p.Role`. (3) `EndOwnerChange(room, ver)`. Mỗi bước idempotent; chạy lại sau crash an toàn.
- `Guard(room, at)`: pending có → `finish`, `repaired = true`. Rồi `Owners(room, 1)` rỗng và `Successor` có → `BeginOwnerChange({Action: repair, Successor, SuccessorVer, RequestID: "owners-v{k+1}", UpdatedAt: at})` → `finish` → `true`. `UpdatedBy` của repair rỗng (actor event `""`).

### `apps/core/internal/mutate` (Task 12: member; Task 14: đọc)

```go
type MemberStore interface { store.MemberWriter; MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) }
type OwnerCoordinator interface{ Apply(ctx context.Context, ch ownership.Change) (ownership.Result, error) }
type RequestDedupe interface { Begin(ctx context.Context, key dedupe.Key) (dedupe.RequestStatus, error); Finish(ctx context.Context, key dedupe.Key, rec dedupe.Record); Cancel(ctx context.Context, key dedupe.Key) }
type MemberForgetter interface{ ForgetMembers(room uint64) }
type ReadNotifier interface{ Offer(u domain.ReadUpdate) }

const DefaultMemberBatch, MinMemberBatch, MemberTries = 500, 2, 3

type AddMembersCmd struct{ Tenant, User string; Room uint64; Users []string; RequestID string }
type RemoveMemberCmd struct{ Tenant, User string; Room uint64; Target string }
type LeaveRoomCmd struct{ Tenant, User string; Room uint64 }
type ChangeRoleCmd struct{ Tenant, User string; Room uint64; Target string; Role domain.Role }
type MemberResult struct{ Member domain.Member; Changed bool; Successor string; PreviousRole domain.Role }
type ReadCmd struct{ Tenant, User string; Room, Seq uint64 }

func (m *Mutator) AddMembers(ctx context.Context, c AddMembersCmd) ([]domain.Member, error)
func (m *Mutator) RemoveMember(ctx context.Context, c RemoveMemberCmd) (MemberResult, error)
func (m *Mutator) LeaveRoom(ctx context.Context, c LeaveRoomCmd) (MemberResult, error)
func (m *Mutator) ChangeMemberRole(ctx context.Context, c ChangeRoleCmd) (MemberResult, error)
func (m *Mutator) MemberBatch() int
func (m *Mutator) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPosition, error)
func (m *Mutator) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPosition, error)
```

- `Limits` thêm `MemberBatch int` (`cmp.Or` → 500; `MinMemberBatch..domain.MaxMemberBatch`, sai → `ErrInvalidArgument`). `Deps` thêm `Members MemberStore`, `Owners OwnerCoordinator`, `Requests RequestDedupe`, `Forget MemberForgetter`, `NewRequestID func() string` (nil → 16 byte ngẫu nhiên dạng hex) (Task 12) và `Reads store.ReadPositions`, `ReadCast ReadNotifier` (Task 14); nil → `errMissingDeps` (sửa thông điệp). Task thêm dep sửa `mutate/fixtures_test.go`, rig `grpcsrv`, `apps/core/service_wiring.go` cùng task.
- `AddMembers`: `ValidCID(RequestID)`; users `ValidUser`, khử trùng, rỗng → `invalid("users")`, > `MemberBatch` → `ErrTooManyMembers` → `Admit(AddMembers)` → DM → `ErrDirectRoom` → `Allow` → `Requests.Begin(RequestKey(room, caller, rid))`: `Busy` → `ErrRetryLater`; `Done` → `MembersOf` lọc `AddedBy` → trả; `New` → `Messages.Last(room, 0)` → `Members.AddMembers(Join{…, At: now, ReadSeq: last}, users)` (lỗi → `Cancel`, trả lỗi) → `Forget` → lọc `AddedBy` → enqueue `pbconv.MemberEvents` của từng doc (lỗi bỏ qua) → `Finish(Record{Seq: len(users), CreatedAt: now})` → trả doc đã thêm.
- `RemoveMember` (`Target == User` → `ErrInvalidArgument`), `LeaveRoom`, `ChangeMemberRole` (`Role` hợp lệ): `Admit` (`LeaveRoom`: `ErrNotMember` → `MembersOf` có tombstone → `{Member: doc, Changed: false}`, không có → `ErrNotMember`) → DM → `ErrDirectRoom` → đích: `MembersOf` (không có → `Target` zero) → `Allow(Request{Member: caller, Target, Role})` **trước** mọi kết luận về đích, để member thường không dò được ai từng ở room → remove/role với đích không có hoặc không active → `ErrMemberNotFound` (remove trên tombstone → no-op) → cùng role → no-op → đích là owner hoặc role mới là owner → `Owners.Apply(ownership.Change{…, RequestID: NewRequestID(), Authorize: hàm gọi lại `Allow` với doc mới})`; không thì tối đa `MemberTries` lượt `ApplyMember(cur, cur.Next(…))`, trượt → đọc lại đích và kiểm lại; hết lượt → `ErrRetryLater`. Đổi → `Forget` → enqueue `MemberEvents` của đích (và kế nhiệm) → `MemberResult`.
- `MarkRead`: `Admit(MarkRead)` → `Allow` → `Messages.Last(room, 0)` khi `seq == 0 || seq > room.LastSeq`; kẹp `seq` về last; last 0 → trả vị trí hiện tại của `req.Member`, không ghi → `Reads.MarkRead`. `MarkUnread`: seq 0 → `invalid("seq")`; kẹp như trên; `Reads.MarkUnread(room, user, seq−1)`. Đổi → `ReadCast.Offer(ReadUpdate{Room, Tenant, Type, Members: room.MemberCount, User, Pos, At: now})`.

### `apps/core/internal/readcast` (Task 14, package mới)

```go
type Publisher interface{ Enqueue(room uint64, events []*chatimv1.Event) error }

const DefaultWindow, MinWindow, MaxWindow = time.Second, time.Second, 2 * time.Second
const DefaultMaxMembers, MaxMembersCap, DefaultMaxPending = 20, 50, 65536

type Config struct{ Window time.Duration; MaxMembers, MaxPending int }

func (c Config) Validate() error
func New(pub Publisher, cfg Config) (*Caster, error)
func (c *Caster) Offer(u domain.ReadUpdate)
func (c *Caster) Run(ctx context.Context) error
func (c *Caster) Close(ctx context.Context) error
func (c *Caster) Unbatched() uint64
```

- Zero → mặc định; `Window ∈ [MinWindow, MaxWindow]`, `MaxMembers ∈ [1, MaxMembersCap]`, `MaxPending ≥ 1`.
- `Offer` không chặn: khoá `(room, user)` chưa có hoặc quá W kể từ lần gửi → gửi ngay, ghi `sentAt`; trong W → giữ bản `Pos.Ver` lớn nhất làm pending. Map đầy với khoá mới, hoặc sau `Close` → gửi thẳng, `Unbatched()++`. `Run`: ticker W/2, gửi pending đủ W, bỏ khoá rảnh quá W. `Close`: dừng `Run`, xả pending, idempotent.
- Gửi = `pub.Enqueue(u.Room, []*Event{pbconv.ReadUpdated(u, roomWide)})`, `roomWide = u.Type == RoomDM || u.Members <= MaxMembers`; lỗi enqueue bỏ qua. Test `synctest` + goleak, `-count=5`.

### `pbconv` + proto + `publish` (Task 3)

```go
func MemberEventID(room uint64, user string, ver uint32) string
func MemberUserEventID(room uint64, user string, ver uint32) string
func MemberCountEventID(room, ver uint64) string
func ReadEventID(room uint64, user string, readVer uint64) string
func MemberRole(r domain.Role) chatimv1.MemberRole
func DomainMemberRole(r chatimv1.MemberRole) (domain.Role, error)
func MemberEvents(roomType domain.RoomType, m domain.Member) []*chatimv1.Event
func MemberCountChanged(r domain.Room, at time.Time) *chatimv1.Event
func ReadUpdated(u domain.ReadUpdate, roomWide bool) *chatimv1.Event
```

- Id: `MemberEventID` = `RoomID(room) + "-mb-" + user + "-v" + ver`; `MemberUserEventID` = `MemberEventID(…) + "-u"`; `MemberCountEventID` = `RoomID(room) + "-members-v" + ver`; `ReadEventID` = `RoomID(room) + "-rd-" + user + "-v" + readVer`.
- `MemberEvents(t, m)`: loại theo doc: `State` active và `PreviousState` khác active → `member_added {user, role, joined_at, ver, request_id}`; `State` removed và `PreviousState` active → `member_removed {user, reason: LEFT nếu UpdatedBy == User, không thì REMOVED, previous_role, ver, request_id}`; cả hai active và role khác → `member_role_changed {user, role, previous_role, ver, request_id}`; khác → `nil`. Trả bản room (`Recipient ""`) rồi bản user (`Recipient m.User`); **bỏ bản room** khi `m.Ver == 1 && m.RequestID == domain.CreationRequestID(m.Room)`. Envelope: `Tenant m.Tenant`, `RoomId`, `RoomType`, `Actor m.UpdatedBy`, `Ts m.UpdatedAt`, `Seq 0`. Fast path và worker cùng gọi hàm này (RC2).
- `MemberCountChanged(r, at)`: id `MemberCountEventID(r.ID, r.MemberCountVer)`, `Actor ""`, `Ts at`, payload `{member_count (kẹp int32 như memberCount), member_count_ver}`. `ReadUpdated`: id `ReadEventID(u.Room, u.User, u.Pos.Ver)`, `Actor u.User`, `Ts u.At`, `Recipient` = `""` khi `roomWide`, không thì `u.User`; payload `{user, read_seq, read_ver}`.
- File mới `proto/chatim/v1/members.proto` (không comment):

```proto
enum MemberRole { MEMBER_ROLE_UNSPECIFIED = 0; MEMBER_ROLE_OWNER = 1; MEMBER_ROLE_ADMIN = 2; MEMBER_ROLE_MEMBER = 3; }
enum MemberRemovedReason { MEMBER_REMOVED_REASON_UNSPECIFIED = 0; MEMBER_REMOVED_REASON_REMOVED = 1; MEMBER_REMOVED_REASON_LEFT = 2; }
message AddMembersRequest { string room_id = 1; repeated string users = 2; string request_id = 3; }
message AddedMember { string user = 1; uint32 ver = 2; }
message AddMembersResponse { repeated AddedMember added = 1; }
message RemoveMemberRequest { string room_id = 1; string user = 2; }
message RemoveMemberResponse { bool changed = 1; uint32 ver = 2; }
message LeaveRoomRequest { string room_id = 1; }
message LeaveRoomResponse { bool changed = 1; uint32 ver = 2; string new_owner = 3; }
message ChangeMemberRoleRequest { string room_id = 1; string user = 2; MemberRole role = 3; }
message ChangeMemberRoleResponse { bool changed = 1; uint32 ver = 2; MemberRole previous_role = 3; }
message MarkReadRequest { string room_id = 1; uint64 seq = 2; }
message MarkReadResponse { uint64 read_seq = 1; uint64 read_ver = 2; }
message MarkUnreadRequest { string room_id = 1; uint64 seq = 2; }
message MarkUnreadResponse { uint64 read_seq = 1; uint64 read_ver = 2; }
message MemberAdded { string user = 1; MemberRole role = 2; google.protobuf.Timestamp joined_at = 3; uint32 ver = 4; string request_id = 5; }
message MemberRemoved { string user = 1; MemberRemovedReason reason = 2; MemberRole previous_role = 3; uint32 ver = 4; string request_id = 5; }
message MemberRoleChanged { string user = 1; MemberRole role = 2; MemberRole previous_role = 3; uint32 ver = 4; string request_id = 5; }
message MemberCountChanged { int32 member_count = 1; uint64 member_count_ver = 2; }
message ReadUpdated { string user = 1; uint64 read_seq = 2; uint64 read_ver = 3; }
```

- Field proto **mới** dùng tên DB (`ver`, `read_ver`, `member_count_ver`, `request_id`); field proto cũ (`pin_version`, `change`) giữ nguyên. `core.proto`: import `members.proto`; service thêm `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`. `events.proto`: import `members.proto`; `Event` thêm `string recipient = 10;`; oneof `member_added = 28; member_removed = 29; member_role_changed = 30; member_count_changed = 31; read_updated = 32;`.
- `publish`: hằng `memberAdded`, `memberRemoved`, `memberRoleChanged`, `memberCountChanged`, `readUpdated` (giá trị = tên payload snake_case) + `eventKind`. `Message`: `ev.GetRecipient() != ""` → phải `validToken`, subject `{root}.{t}.user.{recipient}.{kind}`; rỗng → `roomSubject`. RePublish `Source: root + ".*.*.*.*"`, `Destination: live + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}"`. `markKey` không mark loại mới. Itest: stream có luật cũ → `EnsureStream` luật mới không lỗi → event có recipient tới `live.{t}.user.{u}.evt.member_added`.

### `apps/core/internal/grpcsrv` (Task 13, 15)

- Task 13 `create_room.go`: `len(req.GetMembers()) > s.mutator.MemberBatch()` → `domain.ErrTooManyMembers` trước `NewRoom`; sau `Create` enqueue một lô `[RoomCreated(room)]` + `MemberEvents(room.Type, m)` của từng member (chỉ bản user, theo luật bỏ bản room).
- Task 15: file mới `grpcsrv/members.go` (4 RPC) và `grpcsrv/read.go` (2 RPC) qua `callerAndRoom` rồi `s.mutator.*`; role qua `pbconv.DomainMemberRole` (`UNSPECIFIED` → `ErrInvalidArgument`). **`grpcsrv.Deps` không thêm field.**

### `apps/core/internal/effects` (Task 16)

```go
const MemberCounterName, OwnerGuardName, MemberEventName = "member_counter", "owner_guard", "member_event"
const DefaultMemberCountDelay = time.Second

type MemberLookup interface{ MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) }
type MemberToucher interface{ Touch(ctx context.Context, room uint64, cur domain.MemberCount, witnesses []store.Witness, tries int) (domain.MemberCount, bool, error) }
type OwnerRepairer interface{ Guard(ctx context.Context, room uint64, at time.Time) (bool, error) }

type MemberCounterDeps struct{ Counter MemberToucher; Rooms RoomReader; JS publish.JetStream; Now func() time.Time }
type MemberCounterConfig struct{ SubjectRoot string; Delay time.Duration; Tries int }
type OwnerGuardDeps struct{ Owners OwnerRepairer; Rooms RoomReader; Now func() time.Time }
type OwnerGuardConfig struct{ Delay time.Duration; RoomCache int }
type MemberEventDeps struct{ Members MemberLookup; Rooms RoomReader; JS publish.JetStream }

func NewMemberCounter(deps MemberCounterDeps, cfg MemberCounterConfig) (*MemberCounter, error)
func NewOwnerGuard(deps OwnerGuardDeps, cfg OwnerGuardConfig) (*OwnerGuard, error)
func NewMemberEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberEvent, error)
```

- Mỗi kiểu có `Effect()`, `Dropped()`; `MemberCounter`, `MemberEvent` có `Republished()`; `OwnerGuard` có `Repaired()`.
- `member_counter`: gom theo room; witness = `{User, max Version}`; `Rooms.Get` (đọc thật, không cache) → `Touch(room, {MemberCount, MemberCountVer}, ws, cfg.Tries)` (mặc định 3) → `Ver > 0` → publish `MemberCountChanged`. Room không có → dropped; lỗi khác → lỗi cho mọi record của room.
- `owner_guard`: mỗi room một lần mỗi lô; DM → nil; `Guard(room, Now())`; `true` → `Repaired()++`; room không có → dropped; lỗi → lỗi cho record của room.
- `member_event`: mỗi record `MembersOf(room, [user])`; không có → dropped; `doc.Ver > rec.Version` → nil; `<` → `store.ErrStaleRead`; `==` → loại room (cache) → `MemberEvents` → publish **mọi** bản; record nil chỉ khi mọi bản PubAck (`eventPublisher` thêm `eachMany`, effect cũ không đổi hành vi).
- Registry cuối: `store.MemberChanged: {activity, memberCounter, ownerGuard, memberEvent}` (0 → `MEMBER_COUNT_DELAY` → `RECONCILE_DELAY` → `RECONCILE_DELAY`). `counters()` thêm `member_counter`, `member_event` (republished + dropped), `owner_guard` (dropped). Wiring trong file mới `apps/core/member_effects_wiring.go` (`(fx *effectSet) wireMemberEffects(cfg, cl, st) error`, dựng `counter.NewMembers(st)`, `ownership.New(st)`); `wireEffects` không đổi chữ ký.
- Metric `owner_repaired_total` (help: "Groups the owner_guard effect found without an owner or with an unfinished owner change and repaired."); luật `ChatimOwnerRepaired`: `expr: increase(chatim_core_owner_repaired_total[15m]) > 0`, `severity: warning` → **17 luật**.

### `apps/core/internal/config` + lifecycle (Task 15)

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `MEMBER_BATCH_MAX` | `Config.Limits.MemberBatch` | 500 | 2..1000 (`mutate.Limits.Validate`; 1 chặn mọi DM) |
| `MEMBER_COUNT_DELAY` | `Config.MemberCountDelay` | 1s | `> 0` và `≤ RECONCILE_DELAY` |
| `READ_RECEIPT_WINDOW` | `Config.ReadCast.Window` | 1s | 1s..2s (`readcast.Config.Validate`) |
| `READ_RECEIPT_MAX_MEMBERS` | `Config.ReadCast.MaxMembers` | 20 | 1..50 (trần cứng) |

- `Config` thêm `MemberCountDelay`, `ReadCast readcast.Config` (cuối). `componentErrors`: khoá `"REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX"`, thêm `{"READ_RECEIPT_*", c.ReadCast.Validate()}`. `StopPlan` thêm `ReadEvents` (= `CloseTimeout`) trong `total()`: 26.2s → 27.2s < 28s.
- `app` thêm `reads`; `newSupervisor(ctx, 10)`, task `"read events"` khởi động ngay sau publisher; `shutdown`: `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)` ngay sau `grpc`. `stop_order_test.go`: `["read events", "reconciler", "workers", "router", "cid batcher", "flusher", "publisher"]`. Metric `read_events_unbatched_total`. README bảng env.
- `wireService(d serviceDeps)` với `type serviceDeps struct { st *mongostore.Store; router *actor.Router; pub *publish.Publisher; cids *dedupe.Batcher; cfg config.Config; log *slog.Logger }` (Task 12 tạo; Task 14 thêm `reads *readcast.Caster` cuối và dựng `readcast.New(pub, readcast.Config{})`; Task 15 đổi sang `cfg.ReadCast` và gắn vòng đời). `CORE_REQUEST_DEADLINE + 1s < CID_PENDING_TTL` đã được `actor.Config` bảo đảm nên khoá request pending không hết hạn khi lệnh còn chạy.

### `apps/core/internal/resync` (Task 17)

```go
type Members interface{ MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error) }
var ErrMemberPageFull = errors.New("resync: one instant holds more member changes than a member page")
```

- `Deps` thêm `Members Members` (trước `Pub`); `Report` thêm `MemberRecords`; `String()` thêm `member_records=%d` trước `dry_run`. Mỗi room sau pins: `scanByTime` trang `store.MaxMemberScan`; record `{Kind: MemberChanged, Room, User: m.User, Version: m.Ver, CommittedAt: m.UpdatedAt}`. Room được chọn như cũ (activity/`created_at`). `resync_command.go` truyền `Members: st`.

### Route + corecli (Task 18)

- `tools/internal/route/members.go`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`, dạng `func (c *Client) X(ctx, *chatimv1.XRequest) (*chatimv1.XResponse, Stats, error)` qua `inRoom` (retry giữ nguyên request, `request_id` không đổi). `fakeCore` cài sáu method.
- `corecli`: `add-members` (`-room -users a,b [-request-id]`, mặc định ngẫu nhiên), `remove-member` (`-room -target`), `leave` (`-room`), `set-role` (`-room -target -role owner|admin|member`), `read`, `unread` (`-room -seq`); `watch` thêm `-user` (`live.{t}.user.{u}.>`). e2e thêm pha member + đọc (Part C chốt chi tiết).

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc | Phần |
|---|---|---|---|---|
| 1 | ★ Baseline + đổi tên field 7 collection (codec, index, feed `ver`, storetest, itest) + clear history theo thời gian (proto, domain, store, view, mutate, grpcsrv, route test, corecli) + bỏ feed id cũ. Sau commit: `make infra-reset` | **cao** | — | A |
| 2 | `domain` (role admin, `MemberState`, field member, `Join`, `OwnerChange`…, lỗi, `NewRoom` điền field) + `keys.Member` | thấp | 1 | A |
| 3 | ★ Proto (`members.proto`, 6 RPC, `recipient = 10`, oneof 28–32) + `make proto` + pbconv + publish (subject user, RePublish) + itest RePublish | **cao** | 2 | A |
| 4 | Store port (`member.go`, validate) + `MemberChanged`/`Change.Member` + `RecordOf` + memstore 5 port + storetest `RunMembers` + write contract | trung bình | 2, 3 | A |
| 5 | ★ mongostore: `members` clustered + codec + index, `rooms` field mới, 5 port, `Member`/`ClearHistory` lọc `state`, itest `RunMembers`. Sau commit: `make infra-reset` | **cao** | 4 | A |
| 6 | ★ Feed (`$match`, `feed_member_change.go`, memstore log) + `RunMemberFeed` + `KnownKind`/`checkKind`/`ID` + registry tạm + itest feed-skip (MarkRead, MarkUnread, ClearHistory, AddMembers no-op) | **cao** | 4, 5 | A |
| 7 | `counter` tổng quát hoá (`Loop`, `MemberToucher`). **Push** | trung bình | 4 | A |
| 8 | ★ `pkg/lru` (chuyển từ actor) + `dedupe.Space/RequestKey/Requests` + test Redis thật | **cao** | 1 | B |
| 9 | ★ Actor: thế hệ cache member + TTL, `ForgetMembers` | **cao** | 8 | B |
| 10 | ★ `access`: 5 action, `Target`/`Role`, `DefaultPolicy` | **cao** | 2 | B |
| 11 | ★ Package `ownership` (Apply, finish, Guard) trên memstore | **cao** | 4 | B |
| 12 | ★ `mutate`: AddMembers (request dedupe), RemoveMember, LeaveRoom, ChangeMemberRole, `Limits.MemberBatch`, Deps + `serviceDeps` | **cao** | 3, 4, 8–11 | B |
| 13 | ★ `CreateRoom`: trần mỗi lệnh, bản user `member_added`, bỏ trần 5000 | **cao** | 12 | B |
| 14 | ★ Package `readcast` + `mutate.MarkRead/MarkUnread` + Deps + `serviceDeps.reads` | **cao** | 3, 12 | B |
| 15 | ★ Config (4 env) + `grpcsrv/members.go`, `read.go` + vòng đời readcast + StopPlan + metric + README. **Push** | **cao** | 13, 14 | B |
| 16 | ★ Effect `member_counter`, `owner_guard`, `member_event` + registry + counters + metric/luật OW1 + wiring | **cao** | 6, 7, 11, 15 | C |
| 17 | Resync member | trung bình | 5, 6 | C |
| 18 | Route + corecli + e2e member/đọc | thấp | 15 | C |
| 19 | Itest xuyên phần: core chết giữa pending và finish → `owner_guard` vá; hai owner xoá nhau; event trên subject user; người bị xoá gửi/đọc bị từ chối; retry `AddMembers` cùng `request_id` sau khi bị xoá không thêm lại; đọc/clear không vào work stream | trung bình | 15–17 | C |
| 20 | Docs (thiết kế §4, §5, §5.1, §6.3, §6.4, §7, §8, §9.3, §12 OW1/CT1/MB1, D96–D108, sửa D72/D85), roadmap, CLAUDE.md, INDEXES, "Kết quả thực thi" + kiểm chứng cuối (`fmt-check vet lint vuln test itest`, image + `core-up` + e2e, `/metrics`, `alerts-check` 17 luật, resync dry-run, corebench ngắn). **Push** | thấp | tất cả | C |

Ghi chú phụ thuộc:
- Task 4 đổi `memstore.Rooms.Create` sang doc member đủ field; từ đó `MemberCount` vẫn = số member lúc tạo.
- Task 6 đăng ký tạm `MemberChanged → [room_activity]` để record member không trôi; effect thật ở Task 16.
- Task 12 nhận `router` làm `Forget`, `dedupe.NewRequests(cidBatch, cfg.Dedupe.CommittedTTL)`, `ownership.New(st)`.
- Task 8, 10, 11 khác file, review song song được khi làm task kế. Task 9 sau Task 8 (cùng `actor`).

---

## Ghi chú tích hợp (controller, khi ghép 3 phần)

- **Tên phải giống hệt giữa các phần:** `store.MemberChanged` (6), `store.MemberWriter/MemberReader/OwnerChanges/MemberCounts/ReadPositions`, `store.MaxMemberScan`, `storetest.MemberRooms/RunMembers/RunMemberFeed`, `keys.Member/ParseMember`, `domain.Join/OwnerChange/OwnerState/MemberCount/ReadPosition/ReadUpdate/AddedBy/CreationRequestID`, `counter.Loop/NewMembers/MemberToucher`, `dedupe.Requests/RequestKey/SpaceRequest`, `lru.Cache`, `ownership.Coordinator/Change/Result`, `actor.Router.ForgetMembers`, `access.AddMembers…MarkRead`, `mutate.*Cmd/MemberResult/ReadCmd`, `readcast.Caster`, `pbconv.MemberEvents/MemberCountChanged/ReadUpdated/*EventID`, tên effect, hằng config. Part B/C không đổi chữ ký của Part A; cần đổi thì báo controller.
- **Bất biến xuyên phần:**
  1. Group có member active thì có owner active ở mọi thời điểm: chỉ đường owner (CAS `owners_ver`) đổi role owner hay xoá owner, và luôn thăng kế nhiệm trước khi hạ/xoá đích; đường thường chỉ ghi khi đã kiểm đích không phải owner và CAS theo `ver` của đúng doc đã kiểm.
  2. Đọc, đánh dấu chưa đọc, clear history (và mute sau này) chỉ dùng toán tử thường, **không bao giờ** đụng `ver`, không pipeline/replace/upsert → không vào feed, không vào work stream.
  3. Người bị xoá/rời không còn quyền: `Rooms.Member` lọc `state == 1`; lệnh đổi qua `mutate` từ chối ngay; gửi tin qua actor của core khác ≤ TTL 10s.
  4. `ver`, `read_ver`, `member_count_ver`, `owners_ver` chỉ tăng; tombstone không bao giờ bị xoá (giữ `ver`, `read_*`, `cleared_before_time`), nên id event không lặp.
  5. Fast path không báo lỗi vì effect: lỗi enqueue, `Forget` bỏ qua sau khi ghi; worker hội tụ. `member_count` chỉ worker ghi.
  6. Bản user chỉ gửi cho chính user của doc đổi; doc tạo cùng room không có bản room.
- **Id event và record** (`{room}-` rồi token thứ hai phân biệt loại: số, `created`, `p\d`, `mb-`, `members-v`, `rd-`):

  | Loại | Event id | Record id |
  |---|---|---|
  | tin mới | `{room}-{th}-{seq}` | `m:` + event id |
  | sửa/xoá | `{room}-{th}-{seq}-v{ver}` | `e:` + event id |
  | reaction | `{room}-{th}-{seq}-{u}-n{n}` | `x:` + event id |
  | số reaction | `{room}-{th}-{seq}-reactions-v{v}` | — |
  | ghim | `{room}-p{pin_ver}` | `p:` + event id |
  | room | `{room}-created` | `r:{room}` |
  | member, bản room | `{room}-mb-{u}-v{ver}` | `g:` + event id (một record cho cả hai bản) |
  | member, bản user | `{room}-mb-{u}-v{ver}-u` | — |
  | số member | `{room}-members-v{member_count_ver}` | — |
  | đã đọc | `{room}-rd-{u}-v{read_ver}` | — |

  User id chỉ có `[A-Za-z0-9_-]`, nên phần đuôi `-v{số}` (bản room) và `-u` (bản user) quyết định duy nhất; tin luôn có token số sau `{room}-`, loại khác bắt đầu bằng chữ. Tiền tố record `g:` chưa dùng. Task 3 có test bảng (user `mb`, `v1`, `a-v1`, `u`, `members`, `rd`). Id dài nhất ~101 ký tự.
- **Thứ tự effect** cho `MemberChanged`: `room_activity` (0, `Seq = 0` nên chỉ đẩy `last_change_at/activity_bucket`) → `member_counter` (`MEMBER_COUNT_DELAY`) → `owner_guard` (`RECONCILE_DELAY`) → `member_event` (`RECONCILE_DELAY`); delay không giảm. Không ack mark cho event member, `member_count_changed`, `read_updated`; `read_updated` không có đường bù.
- **Ai sửa file chung (tuần tự):**

  | File | Task |
  |---|---|
  | `store/mongostore/*codec*.go`, `bootstrap.go`, `rooms.go`, `room_activity.go`, `pin_state.go`, `pins.go`, `edits.go`, `hidden.go`, `reactions.go`, `reaction_count.go`, `feed*.go` + test thô | 1 → 5 (codec, bootstrap, rooms) → 6 (`feed.go`, `feed_change.go`) |
  | `store/ports.go`, `store/feed.go`, `store/write_contract_test.go` | 1 → 4 |
  | `store/memstore/rooms.go`, `change_log.go`, `feed.go` | 1 → 4 → 6 |
  | `store/storetest/*` | 1 → 4 → 6 |
  | `domain/room.go`, `validate.go` | 1 → 2 → 13 (`validate.go`) |
  | `pkg/keys/*` | 2 |
  | `proto/chatim/v1/*`, `pkg/pb/chatim/v1/*` | 1 (core.proto ClearHistory) → 3 |
  | `pbconv/*`, `publish/message.go`, `publish/stream.go` | 3 |
  | `work/record.go`, `record_codec.go` | 4 → 6 |
  | `counter/*` | 7 |
  | `pkg/lru` (mới), `dedupe/*`, `actor/lru_cache.go` (xoá) | 8 |
  | `actor/*` | 8 → 9 |
  | `access/*` | 10 |
  | `ownership/*` (mới) | 11 |
  | `mutate/mutator.go`, `fixtures_test.go`, `limits.go`, `hide_clear.go` | 1 → 12 → 14 |
  | `view/*` | 1 |
  | `grpcsrv/change_message.go`, `get_history.go` | 1 |
  | `grpcsrv/harness_test.go`, `fake_dependencies_test.go` | 12 → 13 → 14 → 15 |
  | `grpcsrv/create_room.go` | 13 |
  | `readcast/*` (mới) | 14 |
  | `apps/core/service_wiring.go` | 12 → 14 → 15 |
  | `apps/core/wiring.go` | 12 → 14 → 15 → 16 |
  | `apps/core/effects_wiring.go` | 6 → 16 |
  | `apps/core/member_effects_wiring.go` (mới), `deploy/prometheus/alerts.yml` | 16 |
  | `apps/core/metrics_wiring.go` | 15 → 16 |
  | `apps/core/lifecycle.go`, `shutdown.go`, `stop_order_test.go`, `internal/config/*` | 15 |
  | `effects/*` | 16 |
  | `resync/*`, `apps/core/resync_command.go` | 17 |
  | `tools/internal/route/*`, `tools/corecli/*`, `scripts/e2e.sh` | 1 (clear) → 18 |
  | `README.md` | 15 → 20 |
  | `INDEXES.csv` | mọi task theo danh sách dưới |

- **Dòng `INDEXES.csv` mỗi task sửa:** 1: `store/mongostore`, `store`, `store/memstore`, `store/storetest`, `domain`, `view`, `mutate`, `grpcsrv`, `proto/chatim/v1/core.proto`, `tools/corecli`; 2: `domain`, `pkg/keys`; 3: dòng mới `proto/chatim/v1/members.proto`, `core.proto`, `events.proto`, `pkg/pb/chatim/v1`, `pbconv`, `publish`; 4: `store`, `store/memstore`, `store/storetest`, `work`; 5: `store/mongostore`; 6: `store/mongostore`, `store/memstore`, `store/storetest`, `work`, `reconcile`, `apps/core`; 7: `counter`; 8: dòng mới `pkg/lru`, `dedupe`, `actor`; 9: `actor`; 10: `access`; 11: dòng mới `apps/core/internal/ownership`; 12: `mutate`, `apps/core`; 13: `grpcsrv`, `domain`; 14: dòng mới `apps/core/internal/readcast`, `mutate`, `apps/core`; 15: `config`, `grpcsrv`, `apps/core`, `README.md`; 16: `effects`, `apps/core`, `deploy/prometheus/alerts.yml`; 17: `resync`, `apps/core`; 18: `tools/internal/route`, `tools/corecli`, `tools/corecli/internal/e2e`, `scripts/e2e.sh`; 20: thiết kế, roadmap, plan và bản tóm tắt, `CLAUDE.md`. Cột `decisions` thêm `D96`–`D108` đúng chỗ (ngoặc kép khi có dấu phẩy). Hai dòng plan cũ (`2026-10-06-m2b4-members-read.md`, `-summary.md`) giữ đường dẫn, Task 20 sửa purpose.
- **Khoảng trống tạm (chỉ dev):**
  - Task 1 → 5: `members` chưa clustered, chưa có `state`; `Rooms.Member` như cũ.
  - Task 5 → 6: Mongo ghi `ver` nhưng feed chưa xem `members`.
  - Task 6 → 16: worker chỉ chạy `room_activity` cho record member; `member_count` đứng yên ở giá trị lúc tạo; không có `owner_guard`.
  - Task 8 → 12: `dedupe.Requests` chưa ai gọi. Task 9 → 12: `ForgetMembers` chưa ai gọi (chỉ TTL).
  - Task 12 → 13: `CreateRoom` chưa phát bản user `member_added`. Task 12 → 15: lệnh member chưa có RPC.
  - Task 14 → 15: `readcast` dựng trong `wire` nhưng chưa `Run`/`Close`; không chạy core ở khoảng này.
- **Rolling deploy (prod chưa live, ghi vào D96/D103/D104 ở Task 20):** tên field đổi và `members` đổi khoá nên không có đường nâng cấp tại chỗ: dev `make infra-reset`; prod phải go-live từ bản này. Core cũ ghi đè RePublish khi khởi động (`EnsureStream`) và `Nak` kind 6 (D91) → nâng mọi core cùng lúc. NATS 2.15 nhận đổi RePublish trên stream có sẵn (đã thử ở plan cũ); nếu từ chối thì dừng, báo controller.
- **Detector:** sau Task 16 `/metrics` có `effect_dropped_total{effect}` cho `member_counter`, `owner_guard`, `member_event`, `reconcile_republished_total{effect="member_event"|"member_counter"}`, `owner_repaired_total`; sau Task 15 có `read_events_unbatched_total`. `member_counter` **không** đếm vào `counter_repaired_total` (mọi đổi member đều cần recount nên đó là đường chính, không phải sửa lỗi; tránh `ChatimCounterRepairSurge` kêu sai). `make alerts-check` báo 17 luật từ Task 16.
- **File gần 200 dòng (kiểm `wc -l`):** `mongostore/codec.go` (187, Task 5 tách member codec; Task 1 có thể làm file vượt khi đổi tag — tách sớm nếu cần), `mongostore/codec_test.go` (192), `mongostore/bootstrap_integration_test.go` (188), `resync/scan_test.go` (187, Task 17 tách trước), `apps/core/wiring.go` (150), `actor/room_actor.go` (170, Task 9 dùng `member_cache.go`), `mutate/fixtures_test.go` (165), `effects_wiring.go` (~110), `metrics_wiring.go` (123).

### Ghi chú controller khi ghép (2026-10-07)

- Part A đã biên dịch và chạy thật từng task trên bản sao repo (fmt/vet/lint/test xanh; `make itest` xanh sau Task 1, 3, 5, 6; NATS 2.15 nhận đổi RePublish trên stream có sẵn). Part B chạy trên stub của API Part A; Part C viết theo hợp đồng.
- `store.CreationMember(r, m)` (Task 4): `Create` của mọi adapter luôn ghi doc tạo room (active, `ver` 1, `request_id = {room}-created`, `updated_at/updated_by` = `created_at/created_by` của room), kể cả khi caller truyền field khác; fixture cũ (room 4242 của mutate, access, actor…) giữ nguyên.
- `MembersOf` trả theo thứ tự `users`. `MembersBetween` cùng thời điểm sắp như Mongo so BinData (độ dài trước), memstore sắp `(UpdatedAt, len(User), User)`.
- Task 4 → 5: 5 case contract Mongo đỏ tạm thời (đã liệt kê trong Task 4); Task 4 không chạy `make itest`.
- Task 6 sửa thêm `reconcile/forward_test.go`, `reconcile/stats_test.go`, `mongostore/feed_change_test.go` ngoài bảng sở hữu (tạo room giờ sinh change member). Task 1 sửa thêm `apps/core/edit_access_integration_test.go`, `grpcsrv/history_masks_test.go`, `grpcsrv/change_message_test.go`.
- corecli: cờ bỏ ở Task 1 là `-up-to` (không phải `-seq`). README dòng `clear -room ID [-up-to N]` do Task 20 sửa.
- Xoá người đã rời (tombstone) = no-op (theo hợp đồng `mutate`); người chưa từng là member = `NOT_FOUND`; policy được hỏi trước nên member thường không dò được ai từng ở room.
- `MEMBER_COUNT_DELAY`: Task 15 dùng hằng cục bộ 1s trong config; Task 16 có thể chuyển sang `effects.DefaultMemberCountDelay`.
- Owner change chết ngay sau CAS (chưa ghi doc nào) không sinh record feed, nên `owner_guard` không thấy; được làm nốt ở lệnh owner kế tiếp hoặc lần thử lại. Bất biến vẫn giữ.
- **Review sau ghép (2026-10-07):**
  - `changeMember` hỏi `allowMember` với đích giả role `member` khi đích không có / không active (`missing`), **trước** `ErrMemberNotFound`: member thường xoá/đổi role người lạ → `PERMISSION_DENIED`, owner/admin → `NOT_FOUND`. Task 12 thêm ca "member removes zed" và "member sets role of zed" vào `member_rules_test.go`, Task 15 thêm vào bảng `TestMemberErrorsKeepTheirCodes`, cả hai mong `PermissionDenied`.
  - `request_id` chỉ chống gửi lại trong `CID_COMMITTED_TTL` (15 phút) và khi Redis còn khoá; Task 20 ghi giới hạn này vào D99 và dòng README.
  - Hai helper INDEXES khác nhau: Task 8–15 dùng `<scratchpad>/indexes_edit.py` (stdin JSON); Task 16 **luôn** tạo `bin/indexes_edit.py` (CLI `+ = ~ after`), không dùng lại helper của Part B.
  - Task 16, `metrics_wiring.go` có hai `const dropHelp`; chỉ sửa cái trong `workerSources`, bắt đầu bằng `"Work records an effect gave up on (missing room, message, edit fact, reaction or pin fact, or a corrupt document)."`.
  - Task 16 Step 9, chuỗi tạm trong dòng `apps/core` cần thay nguyên văn: `MemberChanged -> room_activity until the member effects of Task 16 (reaction_pin_effects_wiring.go builds the four new effects with their own counter and pin projector)`.
- Ngưỡng "đã xem" so `room.MemberCount` (trễ ~1s); e2e chờ `member_count_changed` trước bước group lớn.

---

### Task 1: ★ Baseline + đổi tên field 7 collection + clear history theo thời gian

Toàn bộ M2b dùng chung nhánh `feat/m2b`. Task này đồng bộ nhánh, chụp baseline xanh, rồi làm hai việc nền cho mọi task sau (D96, D97):

1. **Đổi tên field** của mọi collection đã xây, trừ `messages`, theo "Bảng đổi tên field" ở trên: codec (tag `bson`), mọi tên thô trong filter/update/pipeline/projection, index của `Bootstrap`, `$match`/decoder của feed (`updatedFields.n` → `updatedFields.ver`), test thô và itest. Tên Go không đổi (trừ `ClearedBeforeSeq`). Không id event/record nào đổi. Doc vị trí feed cũ `_id: "messages"` bị bỏ hẳn (`legacyMessagesFeedID`, `feedStart`, test carry-over); `Forget` chỉ xoá doc `changes`.
2. **Clear history theo thời gian:** `members.cleared_before_time` (date, `$max`) = giờ server lúc gọi (`Mutator.now()`, đã cắt ms). `view.HideForViewer` ẩn tin có `CreatedAt ≤` mốc trên mọi timeline. `ClearHistoryRequest.up_to_seq` và `ClearHistoryResponse.cleared_before_seq` thành reserved; response trả `cleared_before_time`. `mutate.ClearHistory` không còn đọc `Messages.Last`. Trường hợp mốc trùng ms với một tin gửi ngay sau đó: tin đó bị ẩn (chấp nhận, D97); test chỉ khẳng định theo mô hình "`CreatedAt ≤` mốc thì ẩn", không đoán thời điểm.

Thêm ngoài hợp đồng (nhỏ, giữ DRY): `func (v view.Viewer) Cleared(createdAt time.Time) bool` — điều kiện ẩn theo mốc, dùng chung cho `HideForViewer` và `grpcsrv.viewerOf` (bỏ `HiddenIn` khi tin mới nhất của trang đã bị clear).

Không có đường nâng cấp tại chỗ: sau commit, dev chạy `make infra-reset` (Step 10). Itest luôn dùng database `chatim_it_*` mới và stream riêng, nên `make itest` xanh cả trước lẫn sau reset; reset là cho database `chatim` của `core-up`/e2e (index cũ `{r:1,u:1}` unique còn đó sẽ làm insert member thứ hai trùng khoá `null`).

**Files:**
- Modify (proto): `proto/chatim/v1/core.proto`; regenerate `pkg/pb/chatim/v1/core.pb.go` (`make proto`)
- Modify (code): `apps/core/internal/domain/room.go`, `apps/core/internal/store/ports.go`, `apps/core/internal/store/memstore/rooms.go`, `apps/core/internal/view/pipeline.go`, `apps/core/internal/view/masks.go`, `apps/core/internal/mutate/mutator.go`, `apps/core/internal/mutate/hide_clear.go`, `apps/core/internal/grpcsrv/get_history.go`, `apps/core/internal/grpcsrv/change_message.go`, `tools/corecli/cmd_change.go`, `tools/corecli/main.go`
- Modify (mongostore): `bootstrap.go`, `codec.go`, `edit_codec.go`, `edits.go`, `feed.go`, `feed_reaction_change.go`, `hidden.go`, `pin_codec.go`, `pin_state.go`, `pins.go`, `reaction_codec.go`, `reaction_count.go`, `reactions.go`, `room_activity.go`, `rooms.go` (đều trong `apps/core/internal/store/mongostore/`)
- Modify (test): `apps/core/internal/domain/edit_test.go`, `apps/core/internal/view/masks_test.go`, `apps/core/internal/view/reaction_masks_test.go`, `apps/core/internal/mutate/hide_clear_test.go`, `apps/core/internal/grpcsrv/change_message_test.go`, `apps/core/internal/grpcsrv/history_masks_test.go`, `apps/core/internal/store/storetest/apply_cases.go`, `apps/core/internal/store/storetest/rooms_cases.go`, `apps/core/internal/store/storetest/viewer_cases.go`, `tools/internal/route/changes_test.go`, `tools/internal/route/fakes_change_test.go`, `apps/core/edit_access_integration_test.go`
- Modify (test mongostore): `codec_test.go`, `edit_codec_test.go`, `pin_codec_test.go`, `reaction_codec_test.go`, `reactions_test.go`, `feed_change_test.go`, `feed_reaction_change_test.go`, `bootstrap_integration_test.go`, `bootstrap_edits_integration_test.go`, `bootstrap_reactions_integration_test.go`, `feed_anchor_integration_test.go`, `feed_skip_integration_test.go`, `reactions_integration_test.go`, `reaction_count_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`, `apps/core/internal/store`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/domain`, `apps/core/internal/view`, `apps/core/internal/mutate`, `apps/core/internal/grpcsrv`, `proto/chatim/v1/core.proto`, `tools/corecli`)

`README.md` dòng 67 còn nhắc `clear -room ID [-up-to N]`: theo bảng file chung README thuộc Task 15 → 20; Task 20 sửa (không sửa ở đây).

**Step 1: Đồng bộ nhánh**

```bash
git switch feat/m2b
git pull --ff-only
git branch --show-current
git log --oneline -1
git status --short
```

Expected: `feat/m2b`; HEAD là commit plan M2b.4 bản v2 do controller commit (con của `a560214`). `git status --short` không có dòng nào thuộc `apps/`, `proto/`, `pkg/`, `tools/`, `deploy/`, `scripts/` hoặc `INDEXES.csv`. Owner có thể đang có thay đổi doc riêng (`docs/...`, `CLAUDE.md`, `README.md`): không đụng, không stage, không stash, không revert.

Nếu `INDEXES.csv` có thay đổi chưa commit (` M INDEXES.csv` hoặc `M  INDEXES.csv`): **dừng và báo controller**. Mọi task dưới đây sửa `INDEXES.csv` và commit theo pathspec, nên sẽ cuốn luôn thay đổi của owner trong file đó.

**Quy tắc commit cho mọi task của M2b.4:**

- File **mới**: `git add <đúng file mới>` (không add thư mục).
- Commit: `git commit -m "<message>" -- <mọi path task sửa, tạo hoặc xoá>`. Có pathspec thì git commit đúng các path đó; file owner đã stage vẫn ở nguyên trong index.
- Cấm `git add -A`, `git add .`, `git commit -a`, `git stash`.
- Sau mỗi commit: `git show --stat HEAD` chỉ được liệt kê file của task.
- Không `git add INDEXES.csv`: pathspec của `git commit` đã gồm file đó. Sau mỗi lần sửa CSV: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected `{7}`. Field có dấu phẩy phải nằm trong ngoặc kép (mọi dòng sửa ở plan này đã có ngoặc kép ở cột `purpose`; giữ nguyên).
- Message Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`.

**Step 2: Baseline**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả xanh (`lint` in `0 issues.`).

Run: `make infra-up && make itest`
Expected: PASS mọi package (`ok  	github.com/ivannguyendev/chatim/apps/core` khoảng 50s, `mongostore` khoảng 45s). Có gì đỏ thì dừng và báo cáo, không sửa gì.

**Step 3: Test**

Mọi sửa dưới đây là thay chuỗi chính xác; áp theo thứ tự trong từng file. Đổi tên field chỉ đổi chuỗi kỳ vọng trong test codec/layout/index; clear history đổi sang mốc thời gian.

`apps/core/edit_access_integration_test.go`, thay:

```go
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 1 {
		t.Fatalf("ClearHistory(1) = %v, %v; want cleared before seq 1", resp, err)
	}
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || !hiddenOnly(got[1]) || got[2].GetHidden() || got[2].GetText() != "visible 2" || !hiddenOnly(got[3]) {
		t.Fatalf("bob's history = %v, want seq 1 cleared, seq 2 visible, seq 3 hidden, every seq kept", got)
	}
	for seq, m := range historyAs(t, client, itUser, roomID) {
```

bằng:

```go
	}
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || got[1].GetHidden() || got[2].GetText() != "visible 2" || !hiddenOnly(got[3]) {
		t.Fatalf("bob's history = %v, want seq 1 and 2 visible, seq 3 hidden, every seq kept", got)
	}
	resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID})
	if err != nil || resp.GetClearedBeforeTime() == nil {
		t.Fatalf("ClearHistory = %v, %v; want the cleared-before time", resp, err)
	}
	mark := resp.GetClearedBeforeTime().AsTime()
	for seq, m := range historyAs(t, client, "bob", roomID) {
		if !hiddenOnly(m) || m.GetCreatedAt().AsTime().After(mark) {
			t.Fatalf("bob sees seq %d as %v after clearing at %v, want a hidden placeholder", seq, m, mark)
		}
	}
	sendAs(t, client, itUser, roomID, "hide-4", "visible 4")
	for seq, m := range historyAs(t, client, itUser, roomID) {
```

thay:

```go
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(latest) = %v, %v; want cleared before seq 3", resp, err)
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(1) after 3 = %v, %v; want it to stay at 3", resp, err)
	}
	for seq, m := range historyAs(t, client, "bob", roomID) {
		if !hiddenOnly(m) {
			t.Fatalf("bob sees seq %d as %v after clearing everything, want a hidden placeholder", seq, m)
		}
	}
```

bằng:

```go
	}
	later := historyAs(t, client, "bob", roomID)[4]
	if hidden := !later.GetCreatedAt().AsTime().After(mark); later.GetHidden() != hidden {
		t.Fatalf("bob sees seq 4 sent at %v as %v, want hidden=%v against the mark %v", later.GetCreatedAt().AsTime(), later, hidden, mark)
	}
	again, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID})
	if err != nil || again.GetClearedBeforeTime().AsTime().Before(mark) {
		t.Fatalf("second ClearHistory = %v, %v; want a mark not before %v", again, err, mark)
	}
```

`apps/core/internal/domain/edit_test.go`, thay:

```go
	}
	if (domain.Member{Room: 1, User: "alice"}).ClearedBeforeSeq != 0 {
		t.Fatal("a new member starts with cleared history")
```

bằng:

```go
	}
	if !(domain.Member{Room: 1, User: "alice"}).ClearedBeforeTime.IsZero() {
		t.Fatal("a new member starts with cleared history")
```

`apps/core/internal/grpcsrv/change_message_test.go`, thay:

```go
	"strconv"
	"testing"
```

bằng:

```go
	"strconv"
	"sync/atomic"
	"testing"
	"time"
```

thay:

```go
func TestHideAndClearHistoryThroughTheService(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
```

bằng:

```go
func TestHideAndClearHistoryThroughTheService(t *testing.T) {
	mark := time.Now().UTC().Truncate(time.Millisecond)
	var clock atomic.Int64
	clock.Store(mark.UnixMilli())
	rg := newRig(t, options{now: func() time.Time { return time.UnixMilli(clock.Load()) }})
	room := rg.createGroup(t, "acme", "alice", "bob")
```

thay:

```go
	cleared, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if err != nil || cleared.GetClearedBeforeSeq() != 2 {
		t.Fatalf("ClearHistory = %v, %v; want cleared before 2", cleared, err)
	}
	again, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 1})
	if err != nil || again.GetClearedBeforeSeq() != 2 {
		t.Fatalf("lower ClearHistory = %v, %v; want the mark kept at 2", again, err)
	}
```

bằng:

```go
	cleared, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if err != nil || !cleared.GetClearedBeforeTime().AsTime().Equal(mark) {
		t.Fatalf("ClearHistory = %v, %v; want cleared before %v", cleared, err, mark)
	}
	clock.Store(mark.Add(-time.Minute).UnixMilli())
	again, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if err != nil || !again.GetClearedBeforeTime().AsTime().Equal(mark) {
		t.Fatalf("ClearHistory with an earlier clock = %v, %v; want the mark kept at %v", again, err, mark)
	}
```

`apps/core/internal/grpcsrv/history_masks_test.go`, thay:

```go
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
```

bằng:

```go
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
```

thay:

```go
func TestHistoryShowsPlaceholdersPerViewer(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		rg.send(t, alice, room, "c-"+strconv.Itoa(i), text)
	}
```

bằng:

```go
func TestHistoryShowsPlaceholdersPerViewer(t *testing.T) {
	sent := time.Now().UTC().Truncate(time.Millisecond)
	rg := newRig(t, options{now: func() time.Time { return sent.Add(30 * time.Second) }})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		m := domain.Message{
			Room: roomNumber(t, room), Seq: uint64(i + 1), Tenant: "acme", From: "alice", Kind: domain.KindText, Text: text,
			CID: "c-" + strconv.Itoa(i), CreatedAt: sent.Add(time.Duration(i) * time.Minute),
		}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert seq %d: %+v", m.Seq, res)
		}
	}
```

thay:

```go
		func() error {
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 1})
			return err
```

bằng:

```go
		func() error {
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
			return err
```

`apps/core/internal/mutate/hide_clear_test.go`, thay:

```go
	"testing"
```

bằng:

```go
	"testing"
	"time"
```

thay:

```go
func TestClearHistoryOnlyRaisesTheMark(t *testing.T) {
	rg := newRig(t, nil)
	for seq := range uint64(3) {
		rg.send(t, seq+1, "alice", "m")
	}
	clearTo := func(user string, upTo uint64) uint64 {
		t.Helper()
		n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: user, Room: room, UpToSeq: upTo})
		if err != nil {
			t.Fatalf("ClearHistory(%s, %d): %v", user, upTo, err)
		}
		return n
	}
	cases := []struct {
		user       string
		upTo, want uint64
	}{
		{"bob", 0, 3},
		{"bob", 1, 3},
		{"alice", 2, 2},
		{"carol", 99, 3},
	}
	for _, c := range cases {
		if got := clearTo(c.user, c.upTo); got != c.want {
			t.Fatalf("ClearHistory(%s, %d) = %d, want %d", c.user, c.upTo, got, c.want)
		}
	}
	m, err := rg.rooms.Member(t.Context(), room, "bob")
	if err != nil || m.ClearedBeforeSeq != 3 {
		t.Fatalf("bob member = %+v, %v; want cleared before 3", m, err)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("clear enqueued %v, want no event", events)
	}
}

func TestClearHistoryOfAnEmptyRoomKeepsZero(t *testing.T) {
	rg := newRig(t, nil)
	n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room})
	if err != nil || n != 0 {
		t.Fatalf("ClearHistory = %d, %v; want 0", n, err)
	}
```

bằng:

```go
func TestClearHistoryMarksTheServerTimeAndOnlyRaisesIt(t *testing.T) {
	rg := newRig(t, nil)
	mark := rg.at()
	clearAt := func(user string, now time.Time) time.Time {
		t.Helper()
		rg.now = now
		got, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: user, Room: room})
		if err != nil {
			t.Fatalf("ClearHistory(%s at %v): %v", user, now, err)
		}
		return got
	}
	cases := []struct {
		user      string
		now, want time.Time
	}{
		{"bob", mark, mark},
		{"bob", mark.Add(-time.Minute), mark},
		{"alice", mark.Add(-time.Minute), mark.Add(-time.Minute)},
		{"bob", mark.Add(time.Second + 1500*time.Microsecond), mark.Add(time.Second + time.Millisecond)},
	}
	for _, c := range cases {
		if got := clearAt(c.user, c.now); !got.Equal(c.want) {
			t.Fatalf("ClearHistory(%s at %v) = %v, want %v", c.user, c.now, got, c.want)
		}
	}
	m, err := rg.rooms.Member(t.Context(), room, "bob")
	if err != nil || !m.ClearedBeforeTime.Equal(mark.Add(time.Second+time.Millisecond)) {
		t.Fatalf("bob member = %+v, %v; want cleared before %v", m, err, mark.Add(time.Second+time.Millisecond))
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("clear enqueued %v, want no event", events)
	}
```

`apps/core/internal/store/mongostore/bootstrap_edits_integration_test.go`, thay:

```go
	assertClusteredLayout(t, db, editsCollection)
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(hiddenCollection)); !hasIndex(got, "u:1,r:1,th:1,s:1", true) {
		t.Fatalf("hidden indexes = %v, want unique u:1,r:1,th:1,s:1", got)
	}
```

bằng:

```go
	assertClusteredLayout(t, db, editsCollection)
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "room_id:1,created_at:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique room_id:1,created_at:1", got)
	}
	if got := indexKeys(t, db.Collection(hiddenCollection)); !hasIndex(got, "user_id:1,room_id:1,thread_root:1,seq:1", true) {
		t.Fatalf("hidden indexes = %v, want unique user_id:1,room_id:1,thread_root:1,seq:1", got)
	}
```

`apps/core/internal/store/mongostore/bootstrap_integration_test.go`, thay:

```go
	got := indexKeys(t, db.Collection(membersCollection))
	want := map[string]bool{"_id:1": false, "r:1,u:1": true, "t:1,u:1,r:1": false}
	for k, unique := range want {
```

bằng:

```go
	got := indexKeys(t, db.Collection(membersCollection))
	want := map[string]bool{"_id:1": false, "room_id:1,user_id:1": true, "tenant:1,user_id:1,room_id:1": false}
	for k, unique := range want {
```

thay:

```go
	got := indexKeys(t, db.Collection(roomsCollection))
	for _, k := range []string{"ab:1", "ca:1"} {
		if unique, ok := got[k]; !ok || unique {
```

bằng:

```go
	got := indexKeys(t, db.Collection(roomsCollection))
	for _, k := range []string{"activity_bucket:1", "created_at:1"} {
		if unique, ok := got[k]; !ok || unique {
```

`apps/core/internal/store/mongostore/bootstrap_reactions_integration_test.go`, thay:

```go
	assertClusteredLayout(t, db, pinActionsCollection)
	if got := indexKeys(t, db.Collection(reactionsCollection)); !hasIndex(got, "k:1,e:1", false) || !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("reactions indexes = %v, want non-unique k:1,e:1 and r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1 after the rename", got)
	}
```

bằng:

```go
	assertClusteredLayout(t, db, pinActionsCollection)
	if got := indexKeys(t, db.Collection(reactionsCollection)); !hasIndex(got, "message_key:1,emoji:1", false) || !hasIndex(got, "room_id:1,updated_at:1", false) {
		t.Fatalf("reactions indexes = %v, want non-unique message_key:1,emoji:1 and room_id:1,updated_at:1", got)
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "room_id:1,created_at:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique room_id:1,created_at:1", got)
	}
```

`apps/core/internal/store/mongostore/codec_test.go`, thay:

```go
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "ty", "n", "cb", "ca", "mc"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

bằng:

```go
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "tenant", "type", "name", "created_by", "created_at", "member_count"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

thay:

```go
	back, raw := roundTrip(t, encodeMember(m, int64(m.Room)))
	if got, want := fieldNames(t, raw), []string{"r", "u", "t", "ro", "ja"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

bằng:

```go
	back, raw := roundTrip(t, encodeMember(m, int64(m.Room)))
	if got, want := fieldNames(t, raw), []string{"room_id", "user_id", "tenant", "role", "joined_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

`apps/core/internal/store/mongostore/edit_codec_test.go`, thay:

```go
	if !bytes.Equal(doc.ID, keys.Edit(e.Room, e.Thread, e.Seq, e.Version)) || doc.Room != 7_340_000_001 {
		t.Fatalf("_id = %x, r = %d; want keys.Edit and the room", doc.ID, doc.Room)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "k", "by", "x", "p", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

bằng:

```go
	if !bytes.Equal(doc.ID, keys.Edit(e.Room, e.Thread, e.Seq, e.Version)) || doc.Room != 7_340_000_001 {
		t.Fatalf("_id = %x, room_id = %d; want keys.Edit and the room", doc.ID, doc.Room)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "text", "previous_text", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

thay:

```go
	_, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "k", "by", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

bằng:

```go
	_, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "kind", "created_by", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

thay:

```go
func TestMemberCodecReadsClearedBefore(t *testing.T) {
	doc := encodeMember(domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}, 7_340_000_001)
	doc.ClearedBefore = 42
	got, err := decodeMember(doc)
	if err != nil || got.ClearedBeforeSeq != 42 {
		t.Fatalf("decodeMember = %+v, %v; want cleared before 42", got, err)
	}
	doc.ClearedBefore = -1
	if _, err := decodeMember(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeMember(negative cb) = %v, want errCorrupt", err)
	}
```

bằng:

```go
func TestMemberCodecReadsClearedBeforeTime(t *testing.T) {
	doc := encodeMember(domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}, 7_340_000_001)
	doc.ClearedBefore = codecTime.Add(time.Hour)
	_, raw := roundTrip(t, doc)
	if got := raw.Lookup("cleared_before_time").Time(); !got.Equal(doc.ClearedBefore) {
		t.Fatalf("stored cleared_before_time = %v, want %v as a date", got, doc.ClearedBefore)
	}
	got, err := decodeMember(doc)
	if err != nil || !got.ClearedBeforeTime.Equal(codecTime.Add(time.Hour)) {
		t.Fatalf("decodeMember = %+v, %v; want cleared before %v", got, err, codecTime.Add(time.Hour))
	}
```

`apps/core/internal/store/mongostore/feed_anchor_integration_test.go`, thay toàn bộ nội dung bằng:

```go
package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestFeedAnchorUsesFullFieldNames(t *testing.T) {
	_, db := itStore(t, itClient(t))
	raw, err := db.Collection(reconcilerStateCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: changesFeedID}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw: %v", err)
	}
	if got := fieldNames(t, raw); !slices.Equal(got, []string{"_id", "cluster_time"}) {
		t.Fatalf("anchor fields = %v, want [_id cluster_time]", got)
	}
}

func TestForgetClearsThePosition(t *testing.T) {
	_, db := itStore(t, itClient(t))
	if err := NewFeed(db).Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	n, err := db.Collection(reconcilerStateCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil || n != 0 {
		t.Fatalf("reconciler_state holds %d documents after Forget (%v), want none", n, err)
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if at := feedState(t, db).At; at.IsZero() {
		t.Fatalf("anchor after Forget = %+v, want a fresh cluster time", at)
	}
}
```

`apps/core/internal/store/mongostore/feed_change_test.go`, thay:

```go
	cases := map[string]changeDoc{
		"members insert":   changeOn(t, membersCollection, bson.D{{Key: "u", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
```

bằng:

```go
	cases := map[string]changeDoc{
		"members insert":   changeOn(t, membersCollection, bson.D{{Key: "user_id", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
```

`apps/core/internal/store/mongostore/feed_reaction_change_test.go`, thay:

```go
	for name, n := range map[string]any{"int32": int32(2), "int64": int64(2), "double": float64(2)} {
		got, err := decodeChange(reactionUpdate(t, id, bson.D{{Key: "pe", Value: "👍"}, {Key: "e", Value: ""}, {Key: "n", Value: n}}))
		if err != nil || got.Kind != store.ReactionChanged || got.Reaction != want || !got.CommittedAt.Equal(codecTime) || got.Msg.Seq != 0 {
```

bằng:

```go
	for name, n := range map[string]any{"int32": int32(2), "int64": int64(2), "double": float64(2)} {
		got, err := decodeChange(reactionUpdate(t, id, bson.D{{Key: "previous_emoji", Value: "👍"}, {Key: "emoji", Value: ""}, {Key: "ver", Value: n}}))
		if err != nil || got.Kind != store.ReactionChanged || got.Reaction != want || !got.CommittedAt.Equal(codecTime) || got.Msg.Seq != 0 {
```

thay:

```go
	cases := map[string]changeDoc{
		"update without n":       reactionUpdate(t, id, bson.D{{Key: "e", Value: "x"}}),
		"update with n 0":        reactionUpdate(t, id, bson.D{{Key: "n", Value: int32(0)}}),
		"update with n too big":  reactionUpdate(t, id, bson.D{{Key: "n", Value: int64(math.MaxUint32) + 1}}),
		"update with string n":   reactionUpdate(t, id, bson.D{{Key: "n", Value: "2"}}),
		"update of a short key":  reactionUpdate(t, keys.Msg(1, 0, 1), bson.D{{Key: "n", Value: int32(2)}}),
		"update of a bad user":   reactionUpdate(t, keys.Reaction(1, 0, 1, "a.b"), bson.D{{Key: "n", Value: int32(2)}}),
		"update of a string key": reactionUpdate(t, "x", bson.D{{Key: "n", Value: int32(2)}}),
		"insert of a bad key":    changeOn(t, reactionsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"insert of a bad pin":    changeOn(t, pinActionsCollection, bson.D{{Key: "_id", Value: []byte{1}}}),
	}
```

bằng:

```go
	cases := map[string]changeDoc{
		"update without ver":      reactionUpdate(t, id, bson.D{{Key: "emoji", Value: "x"}}),
		"update with the old n":   reactionUpdate(t, id, bson.D{{Key: "n", Value: int32(2)}}),
		"update with ver 0":       reactionUpdate(t, id, bson.D{{Key: "ver", Value: int32(0)}}),
		"update with ver too big": reactionUpdate(t, id, bson.D{{Key: "ver", Value: int64(math.MaxUint32) + 1}}),
		"update with string ver":  reactionUpdate(t, id, bson.D{{Key: "ver", Value: "2"}}),
		"update of a short key":   reactionUpdate(t, keys.Msg(1, 0, 1), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a bad user":    reactionUpdate(t, keys.Reaction(1, 0, 1, "a.b"), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a string key":  reactionUpdate(t, "x", bson.D{{Key: "ver", Value: int32(2)}}),
		"insert of a bad key":     changeOn(t, reactionsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"insert of a bad pin":     changeOn(t, pinActionsCollection, bson.D{{Key: "_id", Value: []byte{1}}}),
	}
```

`apps/core/internal/store/mongostore/feed_skip_integration_test.go`, thay:

```go
	}
	if _, err := s.ClearHistory(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("ClearHistory: %v", err)
```

bằng:

```go
	}
	if _, err := s.ClearHistory(ctx, itRoom, "alice", codecTime); err != nil {
		t.Fatalf("ClearHistory: %v", err)
```

`apps/core/internal/store/mongostore/pin_codec_test.go`, thay:

```go
	if !bytes.Equal(doc.ID, keys.Pin(a.Room, a.PV)) || doc.Op != 2 {
		t.Fatalf("_id = %x, op = %d; want keys.Pin and 2", doc.ID, doc.Op)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "op", "th", "s", "by", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

bằng:

```go
	if !bytes.Equal(doc.ID, keys.Pin(a.Room, a.PV)) || doc.Op != 2 {
		t.Fatalf("_id = %x, action = %d; want keys.Pin and 2", doc.ID, doc.Op)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "room_id", "tenant", "action", "thread_root", "seq", "created_by", "created_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
```

thay:

```go
	back, raw := roundTrip(t, pinStateDoc{Pins: pins, PV: pv})
	if got, want := fieldNames(t, raw), []string{"pins", "pv"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
```

bằng:

```go
	back, raw := roundTrip(t, pinStateDoc{Pins: pins, PV: pv})
	if got, want := fieldNames(t, raw), []string{"pins", "pin_ver"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if got, want := fieldNames(t, raw.Lookup("pins", "0").Document()), []string{"thread_root", "seq", "pinned_by", "pinned_at", "pin_ver"}; !slices.Equal(got, want) {
		t.Fatalf("pin fields = %v, want %v", got, want)
	}
```

`apps/core/internal/store/mongostore/reaction_codec_test.go`, thay:

```go
	raw := bson.Raw(data)
	if got, ok := raw.Lookup("$set", "u", "$literal").StringValueOK(); !ok || got != "alice" {
		t.Fatalf("$set.u = %s, want {$literal: alice}", raw.Lookup("$set", "u"))
	}
	if _, k, ok := raw.Lookup("$set", "k").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("$set.k = %s, want the 24-byte message key", raw.Lookup("$set", "k"))
	}
```

bằng:

```go
	raw := bson.Raw(data)
	if got, ok := raw.Lookup("$set", "user_id", "$literal").StringValueOK(); !ok || got != "alice" {
		t.Fatalf("$set.user_id = %s, want {$literal: alice}", raw.Lookup("$set", "user_id"))
	}
	if _, k, ok := raw.Lookup("$set", "message_key").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("$set.message_key = %s, want the 24-byte message key", raw.Lookup("$set", "message_key"))
	}
```

`apps/core/internal/store/mongostore/reaction_count_integration_test.go`, thay:

```go
	})
	if !slices.Contains(indexes, "k_1_e_1") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of k_1_e_1 without FETCH", stages, indexes)
	}
```

bằng:

```go
	})
	if !slices.Contains(indexes, "message_key_1_emoji_1") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of message_key_1_emoji_1 without FETCH", stages, indexes)
	}
```

`apps/core/internal/store/mongostore/reactions_integration_test.go`, thay:

```go
func TestReactionDocumentLayout(t *testing.T) {
```

bằng:

```go
var reactionFields = []string{"_id", "message_key", "room_id", "tenant", "user_id", "previous_emoji", "emoji", "ver", "updated_at"}

func TestReactionDocumentLayout(t *testing.T) {
```

thay:

```go
	}
	if got, want := fieldNames(t, raw), []string{"_id", "k", "r", "t", "u", "pe", "e", "n", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("stored fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("e").StringValue(); e != "$e" {
		t.Fatalf("stored emoji = %q, want $e", e)
	}
	if _, k, ok := raw.Lookup("k").BinaryOK(); !ok || !bytes.Equal(k, keys.Msg(itRoom, 0, 1)) {
		t.Fatalf("stored k = %s, want keys.Msg", raw.Lookup("k"))
	}
```

bằng:

```go
	}
	if got, want := fieldNames(t, raw), reactionFields; !slices.Equal(got, want) {
		t.Fatalf("stored fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("emoji").StringValue(); e != "$e" {
		t.Fatalf("stored emoji = %q, want $e", e)
	}
	if _, k, ok := raw.Lookup("message_key").BinaryOK(); !ok || !bytes.Equal(k, keys.Msg(itRoom, 0, 1)) {
		t.Fatalf("stored message_key = %s, want keys.Msg", raw.Lookup("message_key"))
	}
```

thay:

```go
		}
		if got, want := fieldNames(t, raw), []string{"_id", "k", "r", "t", "u", "pe", "e", "n", "ts"}; !slices.Equal(got, want) {
			t.Fatalf("stored fields after Set(%s) = %v, want %v", e, got, want)
```

bằng:

```go
		}
		if got, want := fieldNames(t, raw), reactionFields; !slices.Equal(got, want) {
			t.Fatalf("stored fields after Set(%s) = %v, want %v", e, got, want)
```

`apps/core/internal/store/mongostore/reactions_test.go`, thay:

```go
	raw := bson.Raw(data)
	for _, field := range []string{"pe", "e", "n", "ts"} {
		cond := raw.Lookup("$set", field)
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$eq", "0").StringValueOK(); !ok || got != "$e" {
			t.Fatalf("$set.%s if = %s, want {$eq: [$e, emoji]}", field, cond)
		}
```

bằng:

```go
	raw := bson.Raw(data)
	for _, field := range []string{"previous_emoji", "emoji", "ver", "updated_at"} {
		cond := raw.Lookup("$set", field)
		if got, ok := raw.Lookup("$set", field, "$cond", "0", "$eq", "0").StringValueOK(); !ok || got != "$emoji" {
			t.Fatalf("$set.%s if = %s, want {$eq: [$emoji, emoji]}", field, cond)
		}
```

thay:

```go
	}
	if got, ok := raw.Lookup("$set", "e", "$cond", "2", "$literal").StringValueOK(); !ok || got != "$e" {
		t.Fatalf("$set.e else = %s, want {$literal: $e}", raw.Lookup("$set", "e"))
	}
```

bằng:

```go
	}
	if got, ok := raw.Lookup("$set", "emoji", "$cond", "2", "$literal").StringValueOK(); !ok || got != "$e" {
		t.Fatalf("$set.emoji else = %s, want {$literal: $e}", raw.Lookup("$set", "emoji"))
	}
```

`apps/core/internal/store/storetest/apply_cases.go`, thay:

```go
	assertErrorIs(t, "Hide", s.hidden.Hide(ctx, "bob", store.KeyOf(m)), context.Canceled)
	_, err = s.rooms.ClearHistory(ctx, roomA, "alice", 1)
	assertErrorIs(t, "ClearHistory", err, context.Canceled)
```

bằng:

```go
	assertErrorIs(t, "Hide", s.hidden.Hide(ctx, "bob", store.KeyOf(m)), context.Canceled)
	_, err = s.rooms.ClearHistory(ctx, roomA, "alice", baseTime)
	assertErrorIs(t, "ClearHistory", err, context.Canceled)
```

`apps/core/internal/store/storetest/rooms_cases.go`, thay:

```go
	}
	gotAt, wantAt := got.JoinedAt, want.JoinedAt
	got.JoinedAt, want.JoinedAt = time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) {
		t.Fatalf("Member(%d, %q) = %+v at %v, want %+v at %v", want.Room, want.User, got, gotAt, want, wantAt)
	}
```

bằng:

```go
	}
	gotAt, wantAt, gotCleared, wantCleared := got.JoinedAt, want.JoinedAt, got.ClearedBeforeTime, want.ClearedBeforeTime
	got.JoinedAt, want.JoinedAt, got.ClearedBeforeTime, want.ClearedBeforeTime = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) || !gotCleared.Equal(wantCleared) {
		t.Fatalf("Member(%d, %q) = %+v at %v cleared %v, want %+v at %v cleared %v", want.Room, want.User, got, gotAt, gotCleared, want, wantAt, wantCleared)
	}
```

`apps/core/internal/store/storetest/viewer_cases.go`, thay:

```go
	"testing"
```

bằng:

```go
	"testing"
	"time"
```

thay:

```go
	mustCreate(t, s.rooms, room, members)
	for _, step := range []struct{ seq, want uint64 }{{5, 5}, {3, 5}, {9, 9}, {0, 9}} {
		got, err := s.rooms.ClearHistory(t.Context(), roomA, "alice", step.seq)
		if err != nil || got != step.want {
			t.Fatalf("ClearHistory(alice, %d) = %d, %v; want %d", step.seq, got, err, step.want)
		}
	}
	alice := members[0]
	alice.ClearedBeforeSeq = 9
	assertMember(t, s.rooms, alice)
```

bằng:

```go
	mustCreate(t, s.rooms, room, members)
	at := func(sec int) time.Time { return baseTime.Add(time.Duration(sec) * time.Second) }
	for _, step := range []struct{ at, want time.Time }{{at(5), at(5)}, {at(3), at(5)}, {at(9), at(9)}, {time.Time{}, at(9)}} {
		got, err := s.rooms.ClearHistory(t.Context(), roomA, "alice", step.at)
		if err != nil || !got.Equal(step.want) {
			t.Fatalf("ClearHistory(alice, %v) = %v, %v; want %v", step.at, got, err, step.want)
		}
	}
	alice := members[0]
	alice.ClearedBeforeTime = at(9)
	assertMember(t, s.rooms, alice)
```

thay:

```go
	mustCreate(t, s.rooms, room, members)
	_, err := s.rooms.ClearHistory(t.Context(), roomA, "carol", 1)
	assertErrorIs(t, "ClearHistory(non member)", err, domain.ErrNotMember)
	_, err = s.rooms.ClearHistory(t.Context(), roomB, "alice", 1)
	assertErrorIs(t, "ClearHistory(missing room)", err, domain.ErrNotMember)
```

bằng:

```go
	mustCreate(t, s.rooms, room, members)
	_, err := s.rooms.ClearHistory(t.Context(), roomA, "carol", baseTime)
	assertErrorIs(t, "ClearHistory(non member)", err, domain.ErrNotMember)
	_, err = s.rooms.ClearHistory(t.Context(), roomB, "alice", baseTime)
	assertErrorIs(t, "ClearHistory(missing room)", err, domain.ErrNotMember)
```

`apps/core/internal/view/masks_test.go`, thay:

```go
func textMsg(seq uint64, text string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: "alice", Text: text, CID: "c-" + text}
}
```

bằng:

```go
func textMsg(seq uint64, text string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: "alice", Text: text, CID: "c-" + text, CreatedAt: sentAt(seq)}
}

func sentAt(seq uint64) time.Time { return editedAt.Add(time.Duration(seq) * time.Minute) }
```

thay:

```go
	before := slices.Clone(page)
	v := view.Viewer{User: "bob", ClearedBeforeSeq: 1, HiddenSeqs: map[uint64]bool{3: true}}
	got := view.HideForViewer(v, page)
```

bằng:

```go
	before := slices.Clone(page)
	v := view.Viewer{User: "bob", ClearedBeforeTime: sentAt(1), HiddenSeqs: map[uint64]bool{3: true}}
	got := view.HideForViewer(v, page)
```

thay:

```go
		t.Fatalf("page = %+v, want seq 3 masked and seq 4 hidden", got)
	}
}
```

bằng:

```go
		t.Fatalf("page = %+v, want seq 3 masked and seq 4 hidden", got)
	}
}

func TestClearedHidesEveryMessageUpToTheMarkTime(t *testing.T) {
	mark := sentAt(2)
	cases := []struct {
		name      string
		mark, at  time.Time
		wantClear bool
	}{
		{"no mark", time.Time{}, sentAt(1), false},
		{"before the mark", mark, sentAt(1), true},
		{"at the mark", mark, mark, true},
		{"one millisecond after", mark, mark.Add(time.Millisecond), false},
	}
	for _, c := range cases {
		if got := (view.Viewer{ClearedBeforeTime: c.mark}).Cleared(c.at); got != c.wantClear {
			t.Errorf("%s: Cleared = %v, want %v", c.name, got, c.wantClear)
		}
	}
	side := textMsg(1, "side")
	side.Thread = 7
	got := view.HideForViewer(view.Viewer{ClearedBeforeTime: mark}, []domain.Message{side, textMsg(3, "later")})
	if !got[0].Hidden || got[1].Hidden {
		t.Fatalf("hidden = %v, %v; want the thread message before the mark hidden and the later one shown", got[0].Hidden, got[1].Hidden)
	}
}
```

`apps/core/internal/view/reaction_masks_test.go`, thay:

```go
	return domain.Message{
		Room: 7, Seq: seq, From: "alice", Text: "t", Deleted: deleted,
		Reactions: domain.ReactionSummary{Counts: slices.Clone(thumbs), Version: 3},
```

bằng:

```go
	return domain.Message{
		Room: 7, Seq: seq, From: "alice", Text: "t", Deleted: deleted, CreatedAt: sentAt(seq),
		Reactions: domain.ReactionSummary{Counts: slices.Clone(thumbs), Version: 3},
```

thay:

```go
	}
	hidden := view.HideForViewer(view.Viewer{ClearedBeforeSeq: 1, HiddenSeqs: map[uint64]bool{3: true}}, page)
	if !uncounted(hidden[0]) || !counted(hidden[1]) || !uncounted(hidden[2]) {
```

bằng:

```go
	}
	hidden := view.HideForViewer(view.Viewer{ClearedBeforeTime: sentAt(1), HiddenSeqs: map[uint64]bool{3: true}}, page)
	if !uncounted(hidden[0]) || !counted(hidden[1]) || !uncounted(hidden[2]) {
```

`tools/internal/route/changes_test.go`, thay:

```go
	"google.golang.org/protobuf/proto"
```

bằng:

```go
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
```

thay:

```go
	"clear": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 9}))
	},
```

bằng:

```go
	"clear": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: room}))
	},
```

thay:

```go
	"hide":   &chatimv1.HideMessageResponse{},
	"clear":  &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: 9},
	"edits":  &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Version: 5}}},
```

bằng:

```go
	"hide":   &chatimv1.HideMessageResponse{},
	"clear":  &chatimv1.ClearHistoryResponse{ClearedBeforeTime: timestamppb.New(clearedAt)},
	"edits":  &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Version: 5}}},
```

`tools/internal/route/fakes_change_test.go`, thay:

```go
	"context"

	"google.golang.org/grpc"
```

bằng:

```go
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
```

thay:

```go
func (f *fakeCore) ClearHistory(ctx context.Context, in *chatimv1.ClearHistoryRequest, _ ...grpc.CallOption) (*chatimv1.ClearHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: in.GetUpToSeq()}, nil
}
```

bằng:

```go
var clearedAt = time.UnixMilli(1_700_000_000_009).UTC()

func (f *fakeCore) ClearHistory(ctx context.Context, _ *chatimv1.ClearHistoryRequest, _ ...grpc.CallOption) (*chatimv1.ClearHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeTime: timestamppb.New(clearedAt)}, nil
}
```

**Step 4: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/... ./apps/core/internal/store/... ./apps/core/internal/view/... ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./tools/internal/route/..."`

Expected: `ok` chỉ ở `apps/core/internal/store`; `FAIL ... [build failed]` ở `domain`, `view`, `store/storetest` (kéo theo `store/memstore`, `store/mongostore`), `mutate`, `grpcsrv`, `tools/internal/route`. Lỗi biên dịch đầu tiên mỗi package:
- `domain`: `apps/core/internal/domain/edit_test.go:21:46: (domain.Member{…}).ClearedBeforeTime undefined (type domain.Member has no field or method ClearedBeforeTime)`
- `view`: `apps/core/internal/view/masks_test.go:40:32: unknown field ClearedBeforeTime in struct literal of type view.Viewer`
- `storetest`: `apps/core/internal/store/storetest/apply_cases.go:120:53: cannot use baseTime (variable of struct type time.Time) as uint64 value in argument to s.rooms.ClearHistory`
- `route`: `tools/internal/route/changes_test.go:79:43: unknown field ClearedBeforeTime in struct literal of type chatimv1.ClearHistoryResponse`
- `mutate`: `apps/core/internal/mutate/hide_clear_test.go:62:10: cannot use got (variable of type uint64) as "time".Time value in return statement`
- `grpcsrv`: `apps/core/internal/grpcsrv/change_message_test.go:119:28: cleared.GetClearedBeforeTime undefined (type *chatimv1.ClearHistoryResponse has no field or method GetClearedBeforeTime)`

Số dòng:cột có thể lệch vài đơn vị nếu file đã được gofmt khác; nội dung lỗi phải giống. Kiểm tên field (codec/layout) chạy được sau Step 6.

**Step 5: Proto**

`proto/chatim/v1/core.proto`, thay:

```proto
message ClearHistoryRequest {
  string room_id = 1;
  uint64 up_to_seq = 2;
}

message ClearHistoryResponse {
  uint64 cleared_before_seq = 1;
}
```

bằng:

```proto
message ClearHistoryRequest {
  reserved 2;
  reserved "up_to_seq";
  string room_id = 1;
}

message ClearHistoryResponse {
  reserved 1;
  reserved "cleared_before_seq";
  google.protobuf.Timestamp cleared_before_time = 2;
}
```

(`google/protobuf/timestamp.proto` đã được import.)

Run: `make proto && make buf-lint && git status --short pkg/pb proto && wc -l proto/chatim/v1/core.proto`
Expected: không lỗi; ` M pkg/pb/chatim/v1/core.pb.go`, ` M proto/chatim/v1/core.proto`; `174 proto/chatim/v1/core.proto`.

**Step 6: Code**

`apps/core/internal/domain/room.go`, thay:

```go
type Member struct {
	Room             uint64
	Tenant           string
	User             string
	Role             Role
	JoinedAt         time.Time
	ClearedBeforeSeq uint64
}
```

bằng:

```go
type Member struct {
	Room              uint64
	Tenant            string
	User              string
	Role              Role
	JoinedAt          time.Time
	ClearedBeforeTime time.Time
}
```

`apps/core/internal/grpcsrv/change_message.go`, thay:

```go
	"context"
```

bằng:

```go
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"
```

thay:

```go
	}
	n, err := s.mutator.ClearHistory(ctx, mutate.ClearCmd{Tenant: who.tenant, User: who.user, Room: room, UpToSeq: req.GetUpToSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: n}, nil
}
```

bằng:

```go
	}
	at, err := s.mutator.ClearHistory(ctx, mutate.ClearCmd{Tenant: who.tenant, User: who.user, Room: room})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeTime: timestamppb.New(at)}, nil
}
```

`apps/core/internal/grpcsrv/get_history.go`, thay:

```go
func (s *Service) viewerOf(ctx context.Context, user string, grant access.Request, q store.PageQuery, page []domain.Message) (view.Viewer, error) {
	v := view.Viewer{User: user, Room: grant.Room, ClearedBeforeSeq: grant.Member.ClearedBeforeSeq}
	if len(page) == 0 {
		return v, nil
	}
	lo, hi := page[0].Seq, page[0].Seq
	for _, m := range page[1:] {
		lo, hi = min(lo, m.Seq), max(hi, m.Seq)
	}
	if hi <= v.ClearedBeforeSeq {
		return v, nil
```

bằng:

```go
func (s *Service) viewerOf(ctx context.Context, user string, grant access.Request, q store.PageQuery, page []domain.Message) (view.Viewer, error) {
	v := view.Viewer{User: user, Room: grant.Room, ClearedBeforeTime: grant.Member.ClearedBeforeTime}
	if len(page) == 0 {
		return v, nil
	}
	lo, hi, newest := page[0].Seq, page[0].Seq, page[0].CreatedAt
	for _, m := range page[1:] {
		lo, hi = min(lo, m.Seq), max(hi, m.Seq)
		if m.CreatedAt.After(newest) {
			newest = m.CreatedAt
		}
	}
	if v.Cleared(newest) {
		return v, nil
```

`apps/core/internal/mutate/hide_clear.go`, thay:

```go
	"context"
```

bằng:

```go
	"context"
	"time"
```

thay:

```go
	Room         uint64
	UpToSeq      uint64
}
```

bằng:

```go
	Room         uint64
}
```

thay:

```go
func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (uint64, error) {
	if _, err := m.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room); err != nil {
		return 0, err
	}
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return 0, err
	}
	seq := c.UpToSeq
	if seq == 0 || seq > last {
		seq = last
	}
	return m.d.Rooms.ClearHistory(ctx, c.Room, c.User, seq)
}
```

bằng:

```go
func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (time.Time, error) {
	if _, err := m.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room); err != nil {
		return time.Time{}, err
	}
	return m.d.Rooms.ClearHistory(ctx, c.Room, c.User, m.now())
}
```

`apps/core/internal/mutate/mutator.go`, thay:

```go
type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}
```

bằng:

```go
type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error)
}
```

`apps/core/internal/store/memstore/rooms.go`, thay:

```go
	"sync"
```

bằng:

```go
	"sync"
	"time"
```

thay:

```go
func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
```

bằng:

```go
func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
```

thay:

```go
	if !ok {
		return 0, domain.ErrNotMember
	}
	m.ClearedBeforeSeq = max(m.ClearedBeforeSeq, seq)
	s.members[k] = m
	return m.ClearedBeforeSeq, nil
}
```

bằng:

```go
	if !ok {
		return time.Time{}, domain.ErrNotMember
	}
	if at.After(m.ClearedBeforeTime) {
		m.ClearedBeforeTime = at
	}
	s.members[k] = m
	return m.ClearedBeforeTime, nil
}
```

`apps/core/internal/store/mongostore/bootstrap.go`, thay:

```go
	"context"
	"errors"
	"fmt"
```

bằng:

```go
	"context"
	"fmt"
```

thay:

```go
	state := db.Collection(reconcilerStateCollection, majority)
	start, err := feedStart(ctx, state)
	if err != nil {
		return err
	}
	keepOrStart := bson.D{{Key: "$ifNull", Value: bson.A{"$at", start}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "at", Value: keepOrStart}}}}}
	filter := bson.D{{Key: "_id", Value: changesFeedID}}
```

bằng:

```go
	state := db.Collection(reconcilerStateCollection, majority)
	keepOrStart := bson.D{{Key: "$ifNull", Value: bson.A{"$cluster_time", "$$CLUSTER_TIME"}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "cluster_time", Value: keepOrStart}}}}}
	filter := bson.D{{Key: "_id", Value: changesFeedID}}
```

thay:

```go
	return nil
}

func feedStart(ctx context.Context, state *mongo.Collection) (any, error) {
	var old feedPosition
	err := state.FindOne(ctx, bson.D{{Key: "_id", Value: legacyMessagesFeedID}}).Decode(&old)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments) || (err == nil && old.At.IsZero()):
		return "$$CLUSTER_TIME", nil
	case err != nil:
		return nil, fmt.Errorf("bootstrap %s: read the messages feed position: %w", reconcilerStateCollection, err)
	default:
		return old.At, nil
	}
}
```

bằng:

```go
	return nil
}
```

thay:

```go
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "r", Value: 1}, {Key: "u", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
	}
}

func roomIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "ab", Value: 1}}}, {Keys: bson.D{{Key: "ca", Value: 1}}}}
}

func roomTimeIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "r", Value: 1}, {Key: "ts", Value: 1}}}}
}

func reactionIndexes() []mongo.IndexModel {
	return append([]mongo.IndexModel{{Keys: bson.D{{Key: "k", Value: 1}, {Key: "e", Value: 1}}}}, roomTimeIndexes()...)
}

func hiddenIndexes() []mongo.IndexModel {
	keys := bson.D{{Key: "u", Value: 1}, {Key: "r", Value: 1}, {Key: "th", Value: 1}, {Key: "s", Value: 1}}
	return []mongo.IndexModel{{Keys: keys, Options: options.Index().SetUnique(true)}}
```

bằng:

```go
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "room_id", Value: 1}}},
	}
}

func roomIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "activity_bucket", Value: 1}}}, {Keys: bson.D{{Key: "created_at", Value: 1}}}}
}

func roomTimeIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "created_at", Value: 1}}}}
}

func reactionIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "message_key", Value: 1}, {Key: "emoji", Value: 1}}},
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "updated_at", Value: 1}}},
	}
}

func hiddenIndexes() []mongo.IndexModel {
	keys := bson.D{{Key: "user_id", Value: 1}, {Key: "room_id", Value: 1}, {Key: "thread_root", Value: 1}, {Key: "seq", Value: 1}}
	return []mongo.IndexModel{{Keys: keys, Options: options.Index().SetUnique(true)}}
```

`apps/core/internal/store/mongostore/codec.go`, thay:

```go
	ID           int64           `bson:"_id"`
	Tenant       string          `bson:"t"`
	Type         domain.RoomType `bson:"ty"`
	Name         string          `bson:"n"`
	CreatedBy    string          `bson:"cb"`
	CreatedAt    time.Time       `bson:"ca"`
	MemberCount  int             `bson:"mc"`
	LastSeq      int64           `bson:"ls,omitempty"`
	LastMsgAt    time.Time       `bson:"lm,omitempty"`
	LastChangeAt time.Time       `bson:"lc,omitempty"`
}

type memberDoc struct {
	Room          int64       `bson:"r"`
	User          string      `bson:"u"`
	Tenant        string      `bson:"t"`
	Role          domain.Role `bson:"ro"`
	JoinedAt      time.Time   `bson:"ja"`
	ClearedBefore int64       `bson:"cb,omitempty"`
}
```

bằng:

```go
	ID           int64           `bson:"_id"`
	Tenant       string          `bson:"tenant"`
	Type         domain.RoomType `bson:"type"`
	Name         string          `bson:"name"`
	CreatedBy    string          `bson:"created_by"`
	CreatedAt    time.Time       `bson:"created_at"`
	MemberCount  int             `bson:"member_count"`
	LastSeq      int64           `bson:"last_seq,omitempty"`
	LastMsgAt    time.Time       `bson:"last_message_at,omitempty"`
	LastChangeAt time.Time       `bson:"last_change_at,omitempty"`
}

type memberDoc struct {
	Room          int64       `bson:"room_id"`
	User          string      `bson:"user_id"`
	Tenant        string      `bson:"tenant"`
	Role          domain.Role `bson:"role"`
	JoinedAt      time.Time   `bson:"joined_at"`
	ClearedBefore time.Time   `bson:"cleared_before_time,omitempty"`
}
```

thay:

```go
	}
	cleared, err := toUint64("member cleared before seq", d.ClearedBefore)
	if err != nil {
		return domain.Member{}, err
	}
	return domain.Member{Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedBeforeSeq: cleared}, nil
}
```

bằng:

```go
	}
	return domain.Member{Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedBeforeTime: d.ClearedBefore}, nil
}
```

`apps/core/internal/store/mongostore/edit_codec.go`, thay:

```go
	ID     []byte          `bson:"_id"`
	Room   int64           `bson:"r"`
	Tenant string          `bson:"t"`
	Kind   domain.EditKind `bson:"k"`
	By     string          `bson:"by"`
	Text   string          `bson:"x,omitempty"`
	Prev   string          `bson:"p,omitempty"`
	At     time.Time       `bson:"ts"`
}
```

bằng:

```go
	ID     []byte          `bson:"_id"`
	Room   int64           `bson:"room_id"`
	Tenant string          `bson:"tenant"`
	Kind   domain.EditKind `bson:"kind"`
	By     string          `bson:"created_by"`
	Text   string          `bson:"text,omitempty"`
	Prev   string          `bson:"previous_text,omitempty"`
	At     time.Time       `bson:"created_at"`
}
```

`apps/core/internal/store/mongostore/edits.go`, thay:

```go
	}
	filter := bson.D{{Key: "r", Value: r}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return s.findEdits(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}
```

bằng:

```go
	}
	filter := bson.D{{Key: "room_id", Value: r}, {Key: "created_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return s.findEdits(ctx, filter, bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}, limit)
}
```

thay:

```go
	}}}
	update := bson.D{{Key: "$unset", Value: bson.D{{Key: "x", Value: ""}, {Key: "p", Value: ""}}}}
	if _, err := s.edits.UpdateMany(ctx, filter, update); err != nil {
```

bằng:

```go
	}}}
	update := bson.D{{Key: "$unset", Value: bson.D{{Key: "text", Value: ""}, {Key: "previous_text", Value: ""}}}}
	if _, err := s.edits.UpdateMany(ctx, filter, update); err != nil {
```

`apps/core/internal/store/mongostore/feed.go`, thay:

```go
	changesFeedID           = "changes"
	legacyMessagesFeedID    = "messages"
	changeStreamHistoryLost = 286
```

bằng:

```go
	changesFeedID           = "changes"
	changeStreamHistoryLost = 286
```

thay:

```go
type feedPosition struct {
	Token bson.Raw       `bson:"token"`
	At    bson.Timestamp `bson:"at"`
}
```

bằng:

```go
type feedPosition struct {
	Token bson.Raw       `bson:"resume_token"`
	At    bson.Timestamp `bson:"cluster_time"`
}
```

thay:

```go
func (f *Feed) Forget(ctx context.Context) error {
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{changesFeedID, legacyMessagesFeedID}}}}}
	if _, err := f.state.DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("forget change feed position: %w", err)
```

bằng:

```go
func (f *Feed) Forget(ctx context.Context) error {
	if _, err := f.state.DeleteOne(ctx, bson.D{{Key: "_id", Value: changesFeedID}}); err != nil {
		return fmt.Errorf("forget change feed position: %w", err)
```

thay:

```go
	}
	filter := bson.D{{Key: "_id", Value: changesFeedID}, {Key: "at", Value: bson.D{{Key: "$lt", Value: p.At}}}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "token", Value: p.Token}, {Key: "at", Value: p.At}}}}
	_, err := c.state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
```

bằng:

```go
	}
	filter := bson.D{{Key: "_id", Value: changesFeedID}, {Key: "cluster_time", Value: bson.D{{Key: "$lt", Value: p.At}}}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "resume_token", Value: p.Token}, {Key: "cluster_time", Value: p.At}}}}
	_, err := c.state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
```

`apps/core/internal/store/mongostore/feed_reaction_change.go`, thay:

```go
	}
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("n").AsInt64OK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a numeric n", errCorrupt)
	}
```

bằng:

```go
	}
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("ver").AsInt64OK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a numeric ver", errCorrupt)
	}
```

`apps/core/internal/store/mongostore/hidden.go`, thay:

```go
type hiddenDoc struct {
	User   string `bson:"u"`
	Room   int64  `bson:"r"`
	Thread int64  `bson:"th"`
	Seq    int64  `bson:"s"`
}
```

bằng:

```go
type hiddenDoc struct {
	User   string `bson:"user_id"`
	Room   int64  `bson:"room_id"`
	Thread int64  `bson:"thread_root"`
	Seq    int64  `bson:"seq"`
}
```

thay:

```go
	filter := bson.D{
		{Key: "u", Value: user}, {Key: "r", Value: r}, {Key: "th", Value: th},
		{Key: "s", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "s", Value: 1}}).SetProjection(bson.D{{Key: "s", Value: 1}, {Key: "_id", Value: 0}})
	cur, err := s.hidden.Find(ctx, filter, opts)
```

bằng:

```go
	filter := bson.D{
		{Key: "user_id", Value: user}, {Key: "room_id", Value: r}, {Key: "thread_root", Value: th},
		{Key: "seq", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetProjection(bson.D{{Key: "seq", Value: 1}, {Key: "_id", Value: 0}})
	cur, err := s.hidden.Find(ctx, filter, opts)
```

`apps/core/internal/store/mongostore/pin_codec.go`, thay:

```go
	ID     []byte    `bson:"_id"`
	Room   int64     `bson:"r"`
	Tenant string    `bson:"t"`
	Op     int32     `bson:"op"`
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
}

type pinDoc struct {
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
	PV     int64     `bson:"pv"`
}

type pinStateDoc struct {
	Pins []pinDoc `bson:"pins"`
	PV   int64    `bson:"pv"`
}
```

bằng:

```go
	ID     []byte    `bson:"_id"`
	Room   int64     `bson:"room_id"`
	Tenant string    `bson:"tenant"`
	Op     int32     `bson:"action"`
	Thread int64     `bson:"thread_root"`
	Seq    int64     `bson:"seq"`
	By     string    `bson:"created_by"`
	At     time.Time `bson:"created_at"`
}

type pinDoc struct {
	Thread int64     `bson:"thread_root"`
	Seq    int64     `bson:"seq"`
	By     string    `bson:"pinned_by"`
	At     time.Time `bson:"pinned_at"`
	PV     int64     `bson:"pin_ver"`
}

type pinStateDoc struct {
	Pins []pinDoc `bson:"pins"`
	PV   int64    `bson:"pin_ver"`
}
```

`apps/core/internal/store/mongostore/pin_state.go`, thay:

```go
	var d pinStateDoc
	onlyPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 1}, {Key: "pv", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, onlyPins); err != nil {
```

bằng:

```go
	var d pinStateDoc
	onlyPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 1}, {Key: "pin_ver", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, onlyPins); err != nil {
```

thay:

```go
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("pv", from)}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "pins", Value: pins}, {Key: "pv", Value: pv}}}}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
```

bằng:

```go
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("pin_ver", from)}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "pins", Value: pins}, {Key: "pin_ver", Value: pv}}}}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
```

`apps/core/internal/store/mongostore/pins.go`, thay:

```go
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return p.find(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}
```

bằng:

```go
	}
	filter := bson.D{{Key: "room_id", Value: rid}, {Key: "created_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return p.find(ctx, filter, bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}, limit)
}
```

`apps/core/internal/store/mongostore/reaction_codec.go`, thay:

```go
	ID     []byte    `bson:"_id"`
	Key    []byte    `bson:"k"`
	Room   int64     `bson:"r"`
	Tenant string    `bson:"t"`
	User   string    `bson:"u"`
	Prev   string    `bson:"pe"`
	Emoji  string    `bson:"e"`
	N      int64     `bson:"n"`
	At     time.Time `bson:"ts"`
}
```

bằng:

```go
	ID     []byte    `bson:"_id"`
	Key    []byte    `bson:"message_key"`
	Room   int64     `bson:"room_id"`
	Tenant string    `bson:"tenant"`
	User   string    `bson:"user_id"`
	Prev   string    `bson:"previous_emoji"`
	Emoji  string    `bson:"emoji"`
	N      int64     `bson:"ver"`
	At     time.Time `bson:"updated_at"`
}
```

`apps/core/internal/store/mongostore/reaction_count.go`, thay:

```go
type witnessDoc struct {
	User string `bson:"u"`
	N    int64  `bson:"n"`
}

type countRow struct {
	Emoji string `bson:"_id"`
	N     int64  `bson:"n"`
}
```

bằng:

```go
type witnessDoc struct {
	User string `bson:"user_id"`
	N    int64  `bson:"ver"`
}

type countRow struct {
	Emoji string `bson:"_id"`
	N     int64  `bson:"count"`
}
```

thay:

```go
	}
	opts := options.Find().SetProjection(bson.D{{Key: "u", Value: 1}, {Key: "n", Value: 1}})
	cur, err := r.coll.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
```

bằng:

```go
	}
	opts := options.Find().SetProjection(bson.D{{Key: "user_id", Value: 1}, {Key: "ver", Value: 1}})
	cur, err := r.coll.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
```

thay:

```go
func countPipeline(key store.MsgKey) mongo.Pipeline {
	match := bson.D{{Key: "k", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, {Key: "e", Value: bson.D{{Key: "$gt", Value: ""}}}}
	group := bson.D{{Key: "_id", Value: "$e"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}
	return mongo.Pipeline{{{Key: "$match", Value: match}}, {{Key: "$group", Value: group}}}
```

bằng:

```go
func countPipeline(key store.MsgKey) mongo.Pipeline {
	match := bson.D{{Key: "message_key", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, {Key: "emoji", Value: bson.D{{Key: "$gt", Value: ""}}}}
	group := bson.D{{Key: "_id", Value: "$emoji"}, {Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}}}
	return mongo.Pipeline{{{Key: "$match", Value: match}}, {{Key: "$group", Value: group}}}
```

thay:

```go
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	opts := options.Find().SetSort(bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := r.coll.Find(ctx, filter, opts)
```

bằng:

```go
	}
	filter := bson.D{{Key: "room_id", Value: rid}, {Key: "updated_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := r.coll.Find(ctx, filter, opts)
```

`apps/core/internal/store/mongostore/reactions.go`, thay:

```go
	}
	filter := bson.D{{Key: "_id", Value: reactionID(key, user)}, {Key: "e", Value: bson.D{{Key: "$ne", Value: ""}}}}
	var d reactionDoc
```

bằng:

```go
	}
	filter := bson.D{{Key: "_id", Value: reactionID(key, user)}, {Key: "emoji", Value: bson.D{{Key: "$ne", Value: ""}}}}
	var d reactionDoc
```

thay:

```go
func setReaction(x domain.Reaction, room int64) mongo.Pipeline {
	same := bson.D{{Key: "$eq", Value: bson.A{"$e", literal(x.Emoji)}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{same, "$" + field, next}}}
	}
	set := bson.D{
		{Key: "k", Value: keys.Msg(x.Room, x.Thread, x.Seq)},
		{Key: "r", Value: room},
		{Key: "t", Value: literal(x.Tenant)},
		{Key: "u", Value: literal(x.User)},
		{Key: "pe", Value: keep("pe", bson.D{{Key: "$ifNull", Value: bson.A{"$e", ""}}})},
		{Key: "e", Value: keep("e", literal(x.Emoji))},
		{Key: "n", Value: keep("n", nextChange())},
		{Key: "ts", Value: keep("ts", x.At)},
	}
```

bằng:

```go
func setReaction(x domain.Reaction, room int64) mongo.Pipeline {
	same := bson.D{{Key: "$eq", Value: bson.A{"$emoji", literal(x.Emoji)}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{same, "$" + field, next}}}
	}
	set := bson.D{
		{Key: "message_key", Value: keys.Msg(x.Room, x.Thread, x.Seq)},
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: literal(x.Tenant)},
		{Key: "user_id", Value: literal(x.User)},
		{Key: "previous_emoji", Value: keep("previous_emoji", bson.D{{Key: "$ifNull", Value: bson.A{"$emoji", ""}}})},
		{Key: "emoji", Value: keep("emoji", literal(x.Emoji))},
		{Key: "ver", Value: keep("ver", nextChange())},
		{Key: "updated_at", Value: keep("updated_at", x.At)},
	}
```

thay:

```go
	set := bson.D{
		{Key: "pe", Value: "$e"},
		{Key: "e", Value: ""},
		{Key: "n", Value: nextChange()},
		{Key: "ts", Value: at},
	}
```

bằng:

```go
	set := bson.D{
		{Key: "previous_emoji", Value: "$emoji"},
		{Key: "emoji", Value: ""},
		{Key: "ver", Value: nextChange()},
		{Key: "updated_at", Value: at},
	}
```

thay:

```go
func nextChange() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$n", 0}}}, 1}}}
}
```

bằng:

```go
func nextChange() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$ver", 0}}}, 1}}}
}
```

`apps/core/internal/store/mongostore/room_activity.go`, thay:

```go
	at := a.At.UTC()
	fields := bson.D{{Key: "lc", Value: at}, {Key: "ab", Value: store.HourBucket(at)}}
	if a.Thread == 0 && a.Seq > 0 {
		fields = append(fields, bson.E{Key: "ls", Value: seq}, bson.E{Key: "lm", Value: at})
	}
```

bằng:

```go
	at := a.At.UTC()
	fields := bson.D{{Key: "last_change_at", Value: at}, {Key: "activity_bucket", Value: store.HourBucket(at)}}
	if a.Thread == 0 && a.Seq > 0 {
		fields = append(fields, bson.E{Key: "last_seq", Value: seq}, bson.E{Key: "last_message_at", Value: at})
	}
```

thay:

```go
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "ab", Value: bson.D{{Key: "$gte", Value: store.HourBucket(q.From)}}}},
			bson.D{{Key: "ca", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}}},
		}},
	}
	if q.Tenant != "" {
		filter = append(filter, bson.E{Key: "t", Value: q.Tenant})
	}
```

bằng:

```go
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "activity_bucket", Value: bson.D{{Key: "$gte", Value: store.HourBucket(q.From)}}}},
			bson.D{{Key: "created_at", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}}},
		}},
	}
	if q.Tenant != "" {
		filter = append(filter, bson.E{Key: "tenant", Value: q.Tenant})
	}
```

`apps/core/internal/store/mongostore/rooms.go`, thay:

```go
	"fmt"
```

bằng:

```go
	"fmt"
	"time"
```

thay:

```go
	var d roomDoc
	withoutPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 0}, {Key: "pv", Value: 0}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, withoutPins); err != nil {
```

bằng:

```go
	var d roomDoc
	withoutPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 0}, {Key: "pin_ver", Value: 0}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, withoutPins); err != nil {
```

thay:

```go
	var d memberDoc
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}
	if err := findOne(ctx, s.members, filter, &d, domain.ErrNotMember); err != nil {
```

bằng:

```go
	var d memberDoc
	filter := bson.D{{Key: "room_id", Value: key}, {Key: "user_id", Value: user}}
	if err := findOne(ctx, s.members, filter, &d, domain.ErrNotMember); err != nil {
```

thay:

```go
func (s *Store) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	}
	upTo, err := toInt64("seq", seq)
	if err != nil {
		return 0, err
	}
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "cb", Value: upTo}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
```

bằng:

```go
func (s *Store) ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return time.Time{}, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	}
	filter := bson.D{{Key: "room_id", Value: key}, {Key: "user_id", Value: user}}
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "cleared_before_time", Value: at}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
```

thay:

```go
	case errors.Is(err, mongo.ErrNoDocuments):
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	case err != nil:
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, err)
	}
	m, err := decodeMember(d)
	if err != nil {
		return 0, err
	}
	return m.ClearedBeforeSeq, nil
}
```

bằng:

```go
	case errors.Is(err, mongo.ErrNoDocuments):
		return time.Time{}, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	case err != nil:
		return time.Time{}, fmt.Errorf("clear history of %q in room %d: %w", user, room, err)
	}
	return d.ClearedBefore, nil
}
```

`apps/core/internal/store/ports.go`, thay:

```go
type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}
```

bằng:

```go
type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error)
}
```

`apps/core/internal/view/masks.go`, thay:

```go
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Seq <= v.ClearedBeforeSeq || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
```

bằng:

```go
	return eachCopy(msgs, func(m *domain.Message) {
		if v.Cleared(m.CreatedAt) || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
```

`apps/core/internal/view/pipeline.go`, thay:

```go
	"slices"
```

bằng:

```go
	"slices"
	"time"
```

thay:

```go
type Viewer struct {
	User             string
	Room             domain.Room
	ClearedBeforeSeq uint64
	HiddenSeqs       map[uint64]bool
}
```

bằng:

```go
type Viewer struct {
	User              string
	Room              domain.Room
	ClearedBeforeTime time.Time
	HiddenSeqs        map[uint64]bool
}

func (v Viewer) Cleared(createdAt time.Time) bool {
	return !v.ClearedBeforeTime.IsZero() && !createdAt.After(v.ClearedBeforeTime)
}
```

`tools/corecli/cmd_change.go`, thay:

```go
	room := fs.String("room", "", "room id")
	upTo := fs.Uint64("up-to", 0, "hide main-timeline messages up to this seq for the caller, 0 for the latest")
	if err := fs.Parse(args); err != nil {
```

bằng:

```go
	room := fs.String("room", "", "room id")
	if err := fs.Parse(args); err != nil {
```

thay:

```go
	}
	req := &chatimv1.ClearHistoryRequest{RoomId: *room, UpToSeq: *upTo}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
```

bằng:

```go
	}
	req := &chatimv1.ClearHistoryRequest{RoomId: *room}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
```

`tools/corecli/main.go`, thay:

```go
  hide               hide a message for the caller only
  clear              hide the history up to a seq for the caller only
  edits              list the edit history of a message
```

bằng:

```go
  hide               hide a message for the caller only
  clear              hide the history up to now for the caller only
  edits              list the edit history of a message
```

Run: `make fmt-check && make vet`
Expected: sạch. `grep -rnwE 'ClearedBeforeSeq|UpToSeq|legacyMessagesFeedID|feedStart' --include='*.go' apps tools | wc -l` → `0`.

Run (kiểm tên thô còn sót, chỉ `messages` được giữ tên ngắn): `grep -nE 'Key: "[a-z]{1,3}"|bson:"[a-z]{1,3}[,"]' apps/core/internal/store/mongostore/*.go | grep -v _test.go`
Expected: đúng 33 dòng, chỉ gồm: field của `messages` (`codec.go` struct `messageDoc` `t/f/k/x/c/ts/v/d/ea/rx`; `apply_edit.go` `v/ea/x/d`; `reaction_codec.go` `reactionsDoc`/`countDoc` `c/v/e/n`; `reaction_summary.go` `rx`), tên đầy đủ ngắn sẵn `seq` (`hidden.go` ×3, `pin_codec.go` ×2, index `hidden` trong `bootstrap.go`) và `ver` (`reaction_codec.go`, `reaction_count.go` ×2, `reactions.go` ×2), `key`/`unique` của clustered index (`bootstrap.go`), `ns` (`feed_change.go`), `ts` của oplog (`oplog_window.go` ×2).

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/... ./apps/core/internal/store/... ./apps/core/internal/view/... ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./tools/..."`
Expected: PASS (mongostore integration skip). Rồi `make lint` → `0 issues.`; `make test` → PASS toàn repo (chữ ký `ClearHistory` đổi xuyên package).

`wc -l apps/core/internal/store/mongostore/codec.go apps/core/internal/store/mongostore/codec_test.go apps/core/internal/store/mongostore/bootstrap_integration_test.go apps/core/internal/store/storetest/rooms_cases.go` → `183`, `192`, `188`, `166`.

**Step 8: Integration**

Run: `make itest`
Expected: PASS mọi package. Riêng mongostore: `TestMongoStoreContract`, `TestMongoEditsContract` (clear theo thời gian), `TestReactionDocumentLayout` (field `_id, message_key, room_id, tenant, user_id, previous_emoji, emoji, ver, updated_at`), `TestReactionCountIsCoveredByTheEmojiIndex` (index `message_key_1_emoji_1`), `TestBootstrap*` (index tên mới), `TestFeedAnchorUsesFullFieldNames`, `TestForgetClearsThePosition`, `TestFeedSkipsSummaryPinActivityEditHideAndClearWrites`; `apps/core`: `TestRealInfraHideAndClearApplyOnlyToTheReader`.

**Step 9: INDEXES + commit**

INDEXES.csv:
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `message_edits (index {r:1, ts:1}), reactions (indexes {k:1, e:1} for a covered count and {r:1, ts:1}) and pin_actions (index {r:1, ts:1}), hidden (unique {u:1, r:1, th:1, s:1}) + rooms/members indexes + reconciler_state and writes the change feed anchor (reconciler_state _id changes) once if missing, carried over from the old _id messages position when present, else cluster time;` bằng `message_edits (index {room_id:1, created_at:1}), reactions (indexes {message_key:1, emoji:1} for a covered count and {room_id:1, updated_at:1}) and pin_actions (index {room_id:1, created_at:1}), hidden (unique {user_id:1, room_id:1, thread_root:1, seq:1}) + rooms/members indexes + reconciler_state and writes the change feed anchor (reconciler_state _id changes, field cluster_time) once if missing, at the cluster time; every collection but messages uses full English field names (D96);`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `Between {r, ts} sorted ts,_id; PurgeText $unset x/p up to a version), hidden insert-or-ignore + covered HiddenIn, members cb via FindOneAndUpdate $max;` bằng `Between {room_id, created_at} sorted created_at,_id; PurgeText $unset text/previous_text up to a version), hidden insert-or-ignore + covered HiddenIn, members cleared_before_time (a date) via FindOneAndUpdate $max;`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `with a pipeline (t/u/e as $literal; $cond on e == emoji keeps pe/e/n/ts, so a same-emoji set leaves the doc byte-identical with no oplog entry; else pe = old e, n + 1)` bằng `with a pipeline (tenant/user_id/emoji as $literal; $cond on emoji == the new emoji keeps previous_emoji/emoji/ver/updated_at, so a same-emoji set leaves the doc byte-identical with no oplog entry; else previous_emoji = old emoji, ver + 1)`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `Remove = FindOneAndUpdate on {_id: k|u, e != ''}` bằng `Remove = FindOneAndUpdate on {_id: k|u, emoji != ''}`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `After clustered _id range, Between {r, ts}); rooms.pins/pv read only by PinState (projection; Rooms.Get excludes them) and written by ApplyPins (CAS on pv);` bằng `After clustered _id range, Between {room_id, created_at}); rooms.pins/pin_ver read only by PinState (projection; Rooms.Get excludes them) and written by ApplyPins (CAS on pin_ver);`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `updatedFields.n (int32/int64/double), a bad key, user or n is a corrupt change;` bằng `updatedFields.ver (int32/int64/double), a bad key, user or ver is a corrupt change;`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `stores the confirmed resume token in reconciler_state _id changes with a conditional majority update that never moves back; Forget clears the changes and the old messages position;` bằng `stores the confirmed resume_token and cluster_time in reconciler_state _id changes with a conditional majority update that never moves back; Forget clears that position;`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `rooms ls/lm (main thread, seq > 0 only)/lc/ab via unordered bulk $max (no upsert) and ActiveRooms query ($or ab >= hour(From) | ca in range, indexes {ab:1} {ca:1} from Bootstrap)` bằng `rooms last_seq/last_message_at (main thread, seq > 0 only)/last_change_at/activity_bucket via unordered bulk $max (no upsert) and ActiveRooms query ($or activity_bucket >= hour(From) | created_at in range, indexes {activity_bucket:1} {created_at:1} from Bootstrap)`.
- dòng `apps/core/internal/store/mongostore`, cột `decisions`: thay `D92;D91` bằng `D92;D91;D96;D97`.
- dòng `apps/core/internal/store`, cột `purpose`: thay `HistoryClearer (ClearHistory: $max cleared-before seq on the member)` bằng `HistoryClearer (ClearHistory: $max cleared-before time on the member, returns the mark)`.
- dòng `apps/core/internal/store`, cột `decisions`: thay `D90;D92` bằng `D90;D92;D97`.
- dòng `apps/core/internal/store/memstore`, cột `purpose`: thay `Rooms.ClearHistory takes the max;` bằng `Rooms.ClearHistory keeps the later time;`.
- dòng `apps/core/internal/store/memstore`, cột `decisions`: thay `D52` bằng `D52;D97`.
- dòng `apps/core/internal/store/storetest`, cột `purpose`: thay `clear history $max and non member` bằng `clear history keeps the later time and non member`.
- dòng `apps/core/internal/store/storetest`, cột `decisions`: thay `D52;D88;D89;D90;D92` bằng `D52;D88;D89;D90;D92;D97`.
- dòng `apps/core/internal/domain`, cột `purpose`: thay `Member.ClearedBeforeSeq;` bằng `Member.ClearedBeforeTime (time-based clear history, D97);`.
- dòng `apps/core/internal/domain`, cột `decisions`: thay `D92;D95` bằng `D92;D95;D97`.
- dòng `apps/core/internal/view`, cột `purpose`: thay `HideForViewer turns seq <= ClearedBeforeSeq or seqs in HiddenSeqs into hidden placeholders` bằng `HideForViewer turns messages created at or before ClearedBeforeTime (Viewer.Cleared; every timeline) or seqs in HiddenSeqs into hidden placeholders`.
- dòng `apps/core/internal/view`, cột `key_symbols`: thay `Viewer;` bằng `Viewer;Viewer.Cleared;`.
- dòng `apps/core/internal/view`, cột `decisions`: thay `D65;D85;D90` bằng `D65;D85;D90;D97`.
- dòng `apps/core/internal/mutate`, cột `purpose`: thay `ClearHistory (UpToSeq 0 or past the last seq clamps to Messages.Last; $max on the member; returns the new mark)` bằng `ClearHistory (marks the server time, $max on the member; returns the mark)`.
- dòng `apps/core/internal/mutate`, cột `decisions`: thay `D92;D95` bằng `D92;D95;D97`.
- dòng `apps/core/internal/grpcsrv`, cột `purpose`: thay `GetHistory builds the view.Viewer from the member's cleared mark and one Hidden.HiddenIn over the page's seq range (skipped for an empty or fully cleared page);` bằng `GetHistory builds the view.Viewer from the member's cleared time and one Hidden.HiddenIn over the page's seq range (skipped for an empty page or one whose newest message is cleared); ClearHistory returns cleared_before_time;`.
- dòng `apps/core/internal/grpcsrv`, cột `decisions`: thay `D94;D95` bằng `D94;D95;D97`.
- dòng `proto/chatim/v1/core.proto`, cột `purpose`: thay `requests carry no tenant/user (metadata); pts reserved (D48)` bằng `requests carry no tenant/user (metadata); pts reserved (D48); ClearHistoryRequest.up_to_seq and ClearHistoryResponse.cleared_before_seq reserved, the response returns cleared_before_time (D97)`.
- dòng `proto/chatim/v1/core.proto`, cột `decisions`: thay `D95` bằng `D95;D97`.
- dòng `tools/corecli`, cột `purpose`: thay `create-room/send/history/edit/delete/hide/clear/edits` bằng `create-room/send/history/edit/delete/hide/clear (up to now, no seq)/edits`.
- dòng `tools/corecli`, cột `decisions`: thay `D95` bằng `D95;D97`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git commit -m "refactor(store): use full field names and clear history by time" -- proto/chatim/v1/core.proto pkg/pb/chatim/v1/core.pb.go apps/core/internal/domain/ apps/core/internal/store/ apps/core/internal/view/ apps/core/internal/mutate/ apps/core/internal/grpcsrv/ apps/core/edit_access_integration_test.go tools/corecli/ tools/internal/route/ INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `git show --stat HEAD` báo `55 files changed` (mọi file ở **Files** + `pkg/pb/chatim/v1/core.pb.go` + `INDEXES.csv`).

**Step 10: Reset dữ liệu dev rồi itest lại**

```bash
make core-down
make infra-reset
make infra-up
make itest
```

Expected: `infra-reset` xoá volume (database `chatim`, stream NATS, Redis); `infra-up` chờ được primary; `make itest` PASS như Step 8. Core image cũ (`chatim/core:dev`) đọc tên field cũ: không `core-up` lại trước khi build image mới (Task 20 làm).

---

### Task 2: `domain` (role admin, `MemberState`, field member, `Join`, `OwnerChange`…, lỗi, `NewRoom` điền field) + `keys.Member`

Đặt kiểu dữ liệu chung cho mọi task sau (D98–D101, D105). `Member` là doc của lớp tập: một doc mỗi (room, user), `State` 1/2 luôn rõ, `Ver` tăng mỗi lần đổi membership (thêm, rời, thêm lại, đổi role), `PreviousRole/PreviousState` cho event biết loại đổi, `RequestID` là lệnh gây đổi cuối. `Member.Next` và `Join.Apply` là hai hàm thuần định nghĩa ngữ nghĩa cho mọi adapter (memstore Task 4, Mongo Task 5 viết lại đúng như vậy bằng update/pipeline), nên fast path và worker cho cùng kết quả:
- `Next(role, state, rid, by, at)`: chỉ đổi membership, `Ver + 1`; giữ `JoinedAt`, `ClearedBeforeTime`, `ReadSeq`, `ReadVer`.
- `Join.Apply(cur, user)`: doc active → trả `cur` y nguyên (no-op, không ghi). Chưa có doc hoặc tombstone → active, role **luôn** `member`, `JoinedAt = At`, `Ver + 1`, `ReadSeq = max(cur, j)` (vào lại không hạ vị trí đọc), `ReadVer + 1` (id `read_updated` không lặp), giữ `ClearedBeforeTime`.

`Room` và `Member` vẫn so sánh được bằng `==` (field mới là số, chuỗi, `time.Time`). `OwnerState.Pending` là con trỏ: so sánh bằng `==` chỉ so địa chỉ, test dùng `reflect.DeepEqual`.

`keys.Member` = room(8) big-endian nối byte của user (giống `keys.Reaction`). **Không** quét khoảng `_id` của `members` (Mongo so BinData theo độ dài trước); mọi truy vấn theo room dùng index `room_id` (Task 5).

`NewRoom` giờ điền `State: MemberActive, Ver: 1, RequestID: CreationRequestID(id), UpdatedAt: now, UpdatedBy: creator`. Adapter chưa lưu các field này tới Task 4 (memstore lưu nguyên struct nên đã có) và Task 5 (Mongo); trần 5000 của `NewRoom` giữ tới Task 13.

**Files:**
- Create: `pkg/keys/member.go`, `pkg/keys/member_test.go`
- Create: `apps/core/internal/domain/member.go`, `apps/core/internal/domain/owner.go`, `apps/core/internal/domain/read.go`, `apps/core/internal/domain/member_test.go`
- Modify: `apps/core/internal/domain/room.go`, `apps/core/internal/domain/errors.go`, `apps/core/internal/domain/validate.go`, `apps/core/internal/domain/room_test.go`, `apps/core/internal/domain/errors_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/domain`, `pkg/keys`)

**Step 1: Test**

`pkg/keys/member_test.go` (file mới):

```go
package keys

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMemberKeyIsTheRoomThenTheUser(t *testing.T) {
	b := Member(9, "alice")
	if len(b) != 8+5 || !bytes.Equal(b[:8], []byte{0, 0, 0, 0, 0, 0, 0, 9}) || string(b[8:]) != "alice" {
		t.Fatalf("Member(9, alice) = %x, want the big-endian room followed by the user bytes", b)
	}
	for _, user := range []string{"a", "alice", strings.Repeat("Z", MaxMemberUser)} {
		room, got, err := ParseMember(Member(0x5f00aa11bb22cc33, user))
		if err != nil || room != 0x5f00aa11bb22cc33 || got != user {
			t.Fatalf("ParseMember(Member(room, %q)) = %d %q %v", user, room, got, err)
		}
	}
}

func TestParseMemberRejectsWrongLengths(t *testing.T) {
	for _, n := range []int{0, 8, 8 + MaxMemberUser + 1} {
		if _, _, err := ParseMember(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParseMember(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
```

`apps/core/internal/domain/member_test.go` (file mới):

```go
package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var joinAt = time.UnixMilli(1_700_000_000_000).UTC()

func TestParseRoleKnowsThreeRoles(t *testing.T) {
	for _, r := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember} {
		if got, err := domain.ParseRole(string(r)); err != nil || got != r {
			t.Fatalf("ParseRole(%q) = %q, %v", r, got, err)
		}
	}
	for _, s := range []string{"", "Owner", "guest"} {
		if _, err := domain.ParseRole(s); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("ParseRole(%q) = %v, want ErrInvalidArgument", s, err)
		}
	}
}

func TestCreationRequestIDIsAValidCID(t *testing.T) {
	id := domain.CreationRequestID(18_446_744_073_709_551_615)
	if id != "18446744073709551615-created" || domain.ValidCID(id) != nil {
		t.Fatalf("CreationRequestID = %q (valid: %v), want {room}-created", id, domain.ValidCID(id))
	}
}

func TestNextMovesMembershipAndKeepsReaderState(t *testing.T) {
	cur := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, State: domain.MemberActive, JoinedAt: joinAt, Ver: 4,
		ClearedBeforeTime: joinAt.Add(time.Hour), ReadSeq: 9, ReadVer: 3, RequestID: "r-1", UpdatedBy: "alice", UpdatedAt: joinAt,
	}
	got := cur.Next(domain.RoleAdmin, domain.MemberRemoved, "r-2", "bob", joinAt.Add(time.Minute))
	want := cur
	want.State, want.PreviousRole, want.PreviousState, want.Ver = domain.MemberRemoved, domain.RoleAdmin, domain.MemberActive, 5
	want.RequestID, want.UpdatedBy, want.UpdatedAt = "r-2", "bob", joinAt.Add(time.Minute)
	if got != want || got.Active() {
		t.Fatalf("Next = %+v, want %+v", got, want)
	}
}

func TestJoinApplyAddsReAddsAndKeepsActiveMembers(t *testing.T) {
	j := domain.Join{Room: 7, Tenant: "acme", RequestID: "r-9", By: "alice", At: joinAt.Add(time.Hour), ReadSeq: 40}
	fresh := j.Apply(domain.Member{}, "bob")
	want := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, State: domain.MemberActive, JoinedAt: j.At, Ver: 1,
		RequestID: "r-9", UpdatedBy: "alice", UpdatedAt: j.At, ReadSeq: 40, ReadVer: 1,
	}
	if fresh != want || !domain.AddedBy(fresh, "r-9", "alice") {
		t.Fatalf("Apply(none) = %+v, want %+v", fresh, want)
	}
	gone := domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, State: domain.MemberRemoved, JoinedAt: joinAt, Ver: 6,
		ClearedBeforeTime: joinAt.Add(time.Minute), ReadSeq: 55, ReadVer: 8, RequestID: "r-3", UpdatedBy: "bob", UpdatedAt: joinAt,
	}
	back := j.Apply(gone, "bob")
	want = domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, State: domain.MemberActive, JoinedAt: j.At, Ver: 7,
		PreviousRole: domain.RoleAdmin, PreviousState: domain.MemberRemoved, RequestID: "r-9", UpdatedBy: "alice", UpdatedAt: j.At,
		ReadSeq: 55, ReadVer: 9, ClearedBeforeTime: joinAt.Add(time.Minute),
	}
	if back != want {
		t.Fatalf("Apply(tombstone) = %+v, want %+v", back, want)
	}
	if again := j.Apply(back, "bob"); again != back {
		t.Fatalf("Apply(active) = %+v, want the doc unchanged", again)
	}
	other := domain.Join{Room: 7, Tenant: "acme", RequestID: "r-10", By: "alice", At: j.At}
	if kept := other.Apply(back, "bob"); domain.AddedBy(kept, "r-10", "alice") || !domain.AddedBy(kept, "r-9", "alice") || domain.AddedBy(kept, "r-9", "carol") {
		t.Fatal("AddedBy must match only the request and the caller that last added the member")
	}
	if domain.AddedBy(gone, "r-3", "bob") {
		t.Fatal("AddedBy must be false for a removed member")
	}
}
```

`apps/core/internal/domain/room_test.go`, thay:

```go
	}
	wantMembers := []domain.Member{
		{Room: 42, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: now},
		{Room: 42, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: now},
		{Room: 42, Tenant: "acme", User: "carol", Role: domain.RoleMember, JoinedAt: now},
	}
	if !slices.Equal(members, wantMembers) {
```

bằng:

```go
	}
	created := func(user string, role domain.Role) domain.Member {
		return domain.Member{
			Room: 42, Tenant: "acme", User: user, Role: role, JoinedAt: now,
			State: domain.MemberActive, Ver: 1, RequestID: "42-created", UpdatedAt: now, UpdatedBy: "alice",
		}
	}
	wantMembers := []domain.Member{created("bob", domain.RoleMember), created("alice", domain.RoleOwner), created("carol", domain.RoleMember)}
	if !slices.Equal(members, wantMembers) {
```

`apps/core/internal/domain/errors_test.go`, thay:

```go
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
	}
```

bằng:

```go
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
		{domain.ErrDirectRoom, apperr.ErrFailedPrecondition},
		{domain.ErrLastOwner, apperr.ErrFailedPrecondition},
		{domain.ErrMemberNotFound, apperr.ErrNotFound},
		{domain.ErrTooManyMembers, apperr.ErrInvalidArgument},
	}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/... ./pkg/keys/..."`
Expected: `FAIL ... [build failed]` ở cả hai package:
- `pkg/keys/member_test.go:11:7: undefined: Member` (rồi `undefined: MaxMemberUser`, `undefined: ParseMember`)
- `apps/core/internal/domain/errors_test.go:26:11: undefined: domain.ErrDirectRoom` (rồi `ErrLastOwner`, `ErrMemberNotFound`, `ErrTooManyMembers`, `domain.RoleAdmin`, `domain.ParseRole`, `domain.CreationRequestID`, `domain.MemberActive`…)

**Step 3: Code**

`pkg/keys/member.go` (file mới):

```go
package keys

const (
	roomLen       = 8
	MaxMemberUser = 64
)

func Member(room uint64, user string) []byte {
	b := make([]byte, roomLen, roomLen+len(user))
	be.PutUint64(b, room)
	return append(b, user...)
}

func ParseMember(b []byte) (room uint64, user string, err error) {
	if len(b) <= roomLen || len(b) > roomLen+MaxMemberUser {
		return 0, "", ErrLength
	}
	return be.Uint64(b[:roomLen]), string(b[roomLen:]), nil
}
```

`apps/core/internal/domain/room.go`, thay:

```go
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

type Room struct {
	ID           uint64
	Tenant       string
	Type         RoomType
	Name         string
	CreatedBy    string
	CreatedAt    time.Time
	MemberCount  int
	LastSeq      uint64
	LastMsgAt    time.Time
	LastChangeAt time.Time
}
```

bằng:

```go
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type Room struct {
	ID             uint64
	Tenant         string
	Type           RoomType
	Name           string
	CreatedBy      string
	CreatedAt      time.Time
	MemberCount    int
	LastSeq        uint64
	LastMsgAt      time.Time
	LastChangeAt   time.Time
	MemberCountVer uint64
}
```

thay:

```go
	ClearedBeforeTime time.Time
}
```

bằng:

```go
	ClearedBeforeTime time.Time
	State             MemberState
	Ver               uint32
	PreviousRole      Role
	PreviousState     MemberState
	RequestID         string
	UpdatedAt         time.Time
	UpdatedBy         string
	ReadSeq, ReadVer  uint64
}

func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleOwner, RoleAdmin, RoleMember:
		return r, nil
	default:
		return "", invalid("role")
	}
}
```

`apps/core/internal/domain/member.go` (file mới):

```go
package domain

import (
	"strconv"
	"time"
)

const MaxMemberBatch = 1000

type MemberState int32

const (
	MemberActive  MemberState = 1
	MemberRemoved MemberState = 2
)

type Join struct {
	Room                  uint64
	Tenant, RequestID, By string
	At                    time.Time
	ReadSeq               uint64
}

type MemberCount struct {
	Count int
	Ver   uint64
}

func CreationRequestID(room uint64) string { return strconv.FormatUint(room, 10) + "-created" }

func (m Member) Active() bool { return m.State == MemberActive }

func (m Member) Next(role Role, state MemberState, requestID, by string, at time.Time) Member {
	next := m
	next.Role, next.State = role, state
	next.PreviousRole, next.PreviousState = m.Role, m.State
	next.Ver = m.Ver + 1
	next.RequestID, next.UpdatedBy, next.UpdatedAt = requestID, by, at
	return next
}

func (j Join) Apply(cur Member, user string) Member {
	if cur.Active() {
		return cur
	}
	return Member{
		Room: j.Room, Tenant: j.Tenant, User: user, Role: RoleMember, State: MemberActive, JoinedAt: j.At,
		Ver: cur.Ver + 1, PreviousRole: cur.Role, PreviousState: cur.State,
		RequestID: j.RequestID, UpdatedBy: j.By, UpdatedAt: j.At,
		ReadSeq: max(cur.ReadSeq, j.ReadSeq), ReadVer: cur.ReadVer + 1, ClearedBeforeTime: cur.ClearedBeforeTime,
	}
}

func AddedBy(m Member, requestID, by string) bool {
	return m.Active() && m.RequestID == requestID && m.UpdatedBy == by
}
```

`apps/core/internal/domain/owner.go` (file mới):

```go
package domain

import "time"

type OwnerAction string

const (
	OwnerLeave      OwnerAction = "leave"
	OwnerRemove     OwnerAction = "remove"
	OwnerChangeRole OwnerAction = "change_role"
	OwnerRepair     OwnerAction = "repair"
)

type OwnerChange struct {
	Action               OwnerAction
	User                 string
	UserVer              uint32
	Role                 Role
	Successor            string
	SuccessorVer         uint32
	RequestID, UpdatedBy string
	UpdatedAt            time.Time
}

type OwnerState struct {
	Ver     uint64
	Pending *OwnerChange
}
```

`apps/core/internal/domain/read.go` (file mới):

```go
package domain

import "time"

type ReadPosition struct{ Seq, Ver uint64 }

type ReadUpdate struct {
	Room    uint64
	Tenant  string
	Type    RoomType
	Members int
	User    string
	Pos     ReadPosition
	At      time.Time
}
```

`apps/core/internal/domain/errors.go`, thay:

```go
	ErrEmojiNotAllowed = fmt.Errorf("emoji not allowed: %w", apperr.ErrInvalidArgument)
)
```

bằng:

```go
	ErrEmojiNotAllowed = fmt.Errorf("emoji not allowed: %w", apperr.ErrInvalidArgument)
	ErrDirectRoom      = fmt.Errorf("direct room members are fixed: %w", apperr.ErrFailedPrecondition)
	ErrLastOwner       = fmt.Errorf("last owner cannot step down: %w", apperr.ErrFailedPrecondition)
	ErrMemberNotFound  = fmt.Errorf("member %w", apperr.ErrNotFound)
	ErrTooManyMembers  = fmt.Errorf("too many members in one request: %w", apperr.ErrInvalidArgument)
)
```

`apps/core/internal/domain/validate.go`, thay:

```go
		}
		out[i] = Member{Room: id, Tenant: tenant, User: u, Role: role, JoinedAt: now}
	}
```

bằng:

```go
		}
		out[i] = Member{
			Room: id, Tenant: tenant, User: u, Role: role, JoinedAt: now,
			State: MemberActive, Ver: 1, RequestID: CreationRequestID(id), UpdatedAt: now, UpdatedBy: creator,
		}
	}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/... ./pkg/keys/..."`
Expected: PASS.

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo (không test nào so `domain.Member` từ `NewRoom` với literal cũ ngoài `room_test.go`). `wc -l apps/core/internal/domain/room.go apps/core/internal/domain/member.go apps/core/internal/domain/validate.go` → `67`, `56`, `108`.

**Step 5: Commit**

INDEXES.csv:
- dòng `apps/core/internal/domain`, cột `purpose`: thay `Room/Member/Message types and validation;` bằng `Room/Member/Message types and validation; roles owner/admin/member (ParseRole); member set doc (D98): State active/removed, Ver per membership change, PreviousRole/PreviousState, RequestID, UpdatedAt/UpdatedBy, ReadSeq/ReadVer; Member.Next (one membership change, keeps join time, clear mark and read position), Join.Apply (add or re-add as member, active doc unchanged, read seq never goes back, clear mark kept), AddedBy, CreationRequestID ({room}-created); NewRoom fills state, ver 1 and the creation request; Room.MemberCountVer; MemberCount; OwnerChange/OwnerState (pending owner change, D100); ReadPosition/ReadUpdate (D105); ErrDirectRoom/ErrLastOwner (failed precondition), ErrMemberNotFound, ErrTooManyMembers (invalid argument); MaxMemberBatch;`.
- dòng `apps/core/internal/domain`, cột `key_symbols`: thay `NewRoom;Room;Member;` bằng `NewRoom;Room;Member;RoleAdmin;ParseRole;MemberState;MemberActive;MemberRemoved;Member.Active;Member.Next;Join;Join.Apply;AddedBy;CreationRequestID;MemberCount;MaxMemberBatch;OwnerAction;OwnerChange;OwnerState;ReadPosition;ReadUpdate;ErrDirectRoom;ErrLastOwner;ErrMemberNotFound;ErrTooManyMembers;`.
- dòng `apps/core/internal/domain`, cột `decisions`: thay `D92;D95;D97` bằng `D92;D95;D97;D98;D99;D100;D101;D105`.
- dòng `pkg/keys`, cột `purpose`: thay `Pin = room|pv (16B);` bằng `Pin = room|pv (16B); Member = room(8) + user bytes (1-64; never range-scanned, BinData compares length first);`.
- dòng `pkg/keys`, cột `key_symbols`: thay `PinLen;MaxReactionUser;` bằng `PinLen;MaxReactionUser;Member;ParseMember;MaxMemberUser;`.
- dòng `pkg/keys`, cột `decisions`: thay `D10;D88;D92` bằng `D10;D88;D92;D98`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add pkg/keys/member.go pkg/keys/member_test.go apps/core/internal/domain/member.go apps/core/internal/domain/owner.go apps/core/internal/domain/read.go apps/core/internal/domain/member_test.go
git commit -m "feat(domain): add the member set doc, owner change and read position types" -- pkg/keys/ apps/core/internal/domain/ INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `12 files changed`.

---

### Task 3: ★ Proto (`members.proto`, 6 RPC, `recipient = 10`, oneof 28–32) + pbconv + publish (subject user, RePublish) + itest RePublish

Proto thêm file `members.proto` (role, lý do rời, request/response của 6 RPC, 5 payload event), 6 RPC vào `CoreService`, field envelope `string recipient = 10` và năm payload `member_added = 28` … `read_updated = 32` (`Event` đang dùng 1–9 với 7 reserved, oneof tới 27). Field proto **mới** dùng tên DB (`ver`, `read_ver`, `member_count_ver`, `request_id`).

`pbconv` (D104, D105):
- Id: bản room `{room}-mb-{user}-v{ver}`, bản user thêm `-u`, số member `{room}-members-v{ver}`, đã đọc `{room}-rd-{user}-v{read_ver}`. Token thứ hai sau `{room}-` phân biệt loại (tin/sửa/reaction/số đếm: số; ghim `p\d`; room `created`; member `mb`; số member `members-v`; đọc `rd`). User chỉ có `[A-Za-z0-9_-]`, nên đuôi `-v{số}` (bản room) và `-v{số}-u` (bản user) quyết định duy nhất; test bảng dùng user `mb`, `v1`, `a-v1`, `u`, `members`, `rd`, `mb-bob-v1`, `v1-u`.
- `MemberEvents(t, m)` đọc **một doc** sau ghi: `member_added` (active, trước đó không active), `member_removed` (removed, trước đó active; `LEFT` khi `UpdatedBy == User`), `member_role_changed` (active cả hai, role đổi); khác → `nil`. Trả bản room rồi bản user (`Recipient = m.User`); doc tạo cùng room (`Ver 1`, `RequestID = {room}-created`) chỉ có bản user. Envelope `Actor = UpdatedBy`, `Ts = UpdatedAt`, `Seq 0`. Fast path (Task 12/13) và worker (Task 16) gọi cùng hàm này.
- `MemberCountChanged(r, at)` (không actor, `member_count` kẹp int32 như `Room`), `ReadUpdated(u, roomWide)` (`Recipient = ""` khi room-wide, không thì người đọc).

Publisher định tuyến event có `recipient` sang `{root}.{t}.user.{u}.{kind}` (recipient phải là một token subject, không thì `errMalformed` như tenant hỏng); luật RePublish thành `{root}.*.*.*.*` → `{live}.{1}.{2}.{3}.evt.{4}`, nên subject room ra **đúng như trước** (`live.{t}.room.{rid}.evt.{kind}`) và subject user ra `live.{t}.user.{u}.evt.{kind}`. `ack_mark_policy.go` không đổi: `markKey` chỉ có case `Event_MessageCreated`, năm payload mới rơi vào `default` (không mark).

Không cần sửa caller: `grpcsrv.Service` nhúng `chatimv1.UnimplementedCoreServiceServer` (6 RPC trả `Unimplemented` tới Task 15), `fakeCore` của `tools/internal/route` nhúng `chatimv1.CoreServiceClient`, `tools/corecli/internal/e2e.EventOf` trả `ok=false` cho payload lạ.

**RePublish trên stream đã có (rủi ro chính của task):** `EnsureStream` gọi `CreateOrUpdateStream`, nên core mới sửa luật của stream cũ. Itest Step 8 đặt lại luật M2b.3 bằng `UpdateStream`, gọi `EnsureStream`, đọc lại cấu hình, rồi publish bản room + bản user của một `member_added`: bản room tới `live.acme.room.101.evt.member_added`, bản user tới `live.acme.user.bob.evt.member_added`. NATS từ chối sửa RePublish (lỗi từ `UpdateStream` hoặc `EnsureStream` nhắc tới republish) → **dừng, báo controller** kèm lỗi nguyên văn; không đổi test cho qua.

**Files:**
- Create: `proto/chatim/v1/members.proto`, `pkg/pb/chatim/v1/members.pb.go` (sinh)
- Modify: `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`; regenerate `pkg/pb/chatim/v1/core.pb.go`, `core_grpc.pb.go`, `events.pb.go`
- Create: `apps/core/internal/pbconv/member.go`, `member_count.go`, `read.go`, `member_event_id_test.go`, `member_test.go`, `member_count_test.go`, `read_test.go`
- Modify: `apps/core/internal/publish/stream.go`, `message.go`, `stream_test.go`
- Create: `apps/core/internal/publish/member_read_event_test.go`, `nats_republish_integration_test.go`
- Modify: `INDEXES.csv` (dòng mới `proto/chatim/v1/members.proto`; dòng `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`, `pkg/pb/chatim/v1`, `apps/core/internal/pbconv`, `apps/core/internal/publish`)

**Step 1: Proto**

`proto/chatim/v1/members.proto` (file mới):

```proto
syntax = "proto3";

package chatim.v1;

import "google/protobuf/timestamp.proto";

enum MemberRole {
  MEMBER_ROLE_UNSPECIFIED = 0;
  MEMBER_ROLE_OWNER = 1;
  MEMBER_ROLE_ADMIN = 2;
  MEMBER_ROLE_MEMBER = 3;
}

enum MemberRemovedReason {
  MEMBER_REMOVED_REASON_UNSPECIFIED = 0;
  MEMBER_REMOVED_REASON_REMOVED = 1;
  MEMBER_REMOVED_REASON_LEFT = 2;
}

message AddMembersRequest {
  string room_id = 1;
  repeated string users = 2;
  string request_id = 3;
}

message AddedMember {
  string user = 1;
  uint32 ver = 2;
}

message AddMembersResponse {
  repeated AddedMember added = 1;
}

message RemoveMemberRequest {
  string room_id = 1;
  string user = 2;
}

message RemoveMemberResponse {
  bool changed = 1;
  uint32 ver = 2;
}

message LeaveRoomRequest {
  string room_id = 1;
}

message LeaveRoomResponse {
  bool changed = 1;
  uint32 ver = 2;
  string new_owner = 3;
}

message ChangeMemberRoleRequest {
  string room_id = 1;
  string user = 2;
  MemberRole role = 3;
}

message ChangeMemberRoleResponse {
  bool changed = 1;
  uint32 ver = 2;
  MemberRole previous_role = 3;
}

message MarkReadRequest {
  string room_id = 1;
  uint64 seq = 2;
}

message MarkReadResponse {
  uint64 read_seq = 1;
  uint64 read_ver = 2;
}

message MarkUnreadRequest {
  string room_id = 1;
  uint64 seq = 2;
}

message MarkUnreadResponse {
  uint64 read_seq = 1;
  uint64 read_ver = 2;
}

message MemberAdded {
  string user = 1;
  MemberRole role = 2;
  google.protobuf.Timestamp joined_at = 3;
  uint32 ver = 4;
  string request_id = 5;
}

message MemberRemoved {
  string user = 1;
  MemberRemovedReason reason = 2;
  MemberRole previous_role = 3;
  uint32 ver = 4;
  string request_id = 5;
}

message MemberRoleChanged {
  string user = 1;
  MemberRole role = 2;
  MemberRole previous_role = 3;
  uint32 ver = 4;
  string request_id = 5;
}

message MemberCountChanged {
  int32 member_count = 1;
  uint64 member_count_ver = 2;
}

message ReadUpdated {
  string user = 1;
  uint64 read_seq = 2;
  uint64 read_ver = 3;
}
```

`proto/chatim/v1/core.proto`, thay:

```proto
import "chatim/v1/reactions_pins.proto";
```

bằng:

```proto
import "chatim/v1/members.proto";
import "chatim/v1/reactions_pins.proto";
```

thay:

```proto
  rpc GetReactionSettings(GetReactionSettingsRequest) returns (GetReactionSettingsResponse);
}
```

bằng:

```proto
  rpc GetReactionSettings(GetReactionSettingsRequest) returns (GetReactionSettingsResponse);
  rpc AddMembers(AddMembersRequest) returns (AddMembersResponse);
  rpc RemoveMember(RemoveMemberRequest) returns (RemoveMemberResponse);
  rpc LeaveRoom(LeaveRoomRequest) returns (LeaveRoomResponse);
  rpc ChangeMemberRole(ChangeMemberRoleRequest) returns (ChangeMemberRoleResponse);
  rpc MarkRead(MarkReadRequest) returns (MarkReadResponse);
  rpc MarkUnread(MarkUnreadRequest) returns (MarkUnreadResponse);
}
```

`proto/chatim/v1/events.proto`, thay:

```proto
import "chatim/v1/core.proto";
import "chatim/v1/reactions_pins.proto";
```

bằng:

```proto
import "chatim/v1/core.proto";
import "chatim/v1/members.proto";
import "chatim/v1/reactions_pins.proto";
```

thay:

```proto
  google.protobuf.Timestamp ts = 9;
  oneof payload {
```

bằng:

```proto
  google.protobuf.Timestamp ts = 9;
  string recipient = 10;
  oneof payload {
```

thay:

```proto
    MessageUnpinned message_unpinned = 27;
  }
```

bằng:

```proto
    MessageUnpinned message_unpinned = 27;
    MemberAdded member_added = 28;
    MemberRemoved member_removed = 29;
    MemberRoleChanged member_role_changed = 30;
    MemberCountChanged member_count_changed = 31;
    ReadUpdated read_updated = 32;
  }
```

Run: `make proto && make buf-lint && git status --short pkg/pb proto && wc -l proto/chatim/v1/core.proto proto/chatim/v1/events.proto proto/chatim/v1/members.proto`
Expected: không lỗi (`buf format -d --exit-code` không in diff); ` M` ở `core.pb.go`, `core_grpc.pb.go`, `events.pb.go`, `core.proto`, `events.proto`, `??` ở `members.pb.go`, `members.proto`; `181`, `77`, `120`.

Run: `make vet`
Expected: sạch (6 RPC mới chưa có handler, `Unimplemented` lo).

**Step 2: Test pbconv + publish**

`roomA` (101), `tenant` ("acme"), `sentAt` có sẵn trong `apps/core/internal/publish/harness_test.go`; `realStream`, `fastSetup` có sẵn trong `nats_integration_test.go`/`harness_test.go`.

`apps/core/internal/pbconv/member_event_id_test.go` (file mới):

```go
package pbconv_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestMemberCountAndReadEventIDs(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"member room copy", pbconv.MemberEventID(42, "bob", 3), "42-mb-bob-v3"},
		{"member user copy", pbconv.MemberUserEventID(42, "bob", 3), "42-mb-bob-v3-u"},
		{"member count", pbconv.MemberCountEventID(42, 7), "42-members-v7"},
		{"read", pbconv.ReadEventID(42, "bob", 9), "42-rd-bob-v9"},
		{
			"widest member user copy",
			pbconv.MemberUserEventID(math.MaxInt64, strings.Repeat("u", 64), math.MaxUint32),
			"9223372036854775807-mb-" + strings.Repeat("u", 64) + "-v4294967295-u",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestMemberCountAndReadEventIDsNeverCollideWithOtherKinds(t *testing.T) {
	users := []string{"mb", "v1", "a-v1", "u", "members", "rd", "bob", "1", "created", "p1", "reactions", "mb-bob-v1", "v1-u"}
	seen := map[string]string{}
	add := func(id, what string) {
		t.Helper()
		if prev, dup := seen[id]; dup {
			t.Fatalf("%s and %s share the id %q", prev, what, id)
		}
		seen[id] = what
	}
	for _, room := range []uint64{1, 42} {
		add(pbconv.RoomCreatedEventID(room), fmt.Sprintf("room %d created", room))
		for _, n := range []uint64{1, 2, 12} {
			at := fmt.Sprintf("%d/%d", room, n)
			add(pbconv.PinEventID(room, n), "pin "+at)
			add(pbconv.MemberCountEventID(room, n), "member count "+at)
			add(pbconv.MessageEventID(room, 0, n), "message "+at)
			add(pbconv.MessageChangeEventID(room, 0, n, 1), "change "+at)
			add(pbconv.ReactionCountsEventID(room, 0, n, 1), "counts "+at)
			for _, u := range users {
				add(pbconv.MemberEventID(room, u, uint32(n)), "member room copy "+at+" "+u)
				add(pbconv.MemberUserEventID(room, u, uint32(n)), "member user copy "+at+" "+u)
				add(pbconv.ReadEventID(room, u, n), "read "+at+" "+u)
				add(pbconv.ReactionEventID(room, 0, n, u, 1), "reaction "+at+" "+u)
			}
		}
	}
}
```

`apps/core/internal/pbconv/member_test.go` (file mới):

```go
package pbconv_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var memberAt = time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)

const memberRoom = "9007199254740993"

func memberDoc(user string, role domain.Role, state domain.MemberState, prevRole domain.Role, prevState domain.MemberState, by string) domain.Member {
	return domain.Member{
		Room: 9_007_199_254_740_993, Tenant: "acme", User: user, Role: role, State: state, JoinedAt: memberAt.Add(-time.Hour),
		Ver: 4, PreviousRole: prevRole, PreviousState: prevState, RequestID: "r-1", UpdatedBy: by, UpdatedAt: memberAt,
	}
}

func memberEnvelope(id, recipient, actor string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: "acme", RoomId: memberRoom, RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Actor: actor, Ts: timestamppb.New(memberAt), Recipient: recipient,
	}
}

func assertEventList(t *testing.T, got, want []*chatimv1.Event) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b *chatimv1.Event) bool { return proto.Equal(a, b) }) {
		t.Fatalf("events = %v,\nwant %v", got, want)
	}
}

func bothCopies(user, actor string, set func(*chatimv1.Event)) []*chatimv1.Event {
	room := memberEnvelope(memberRoom+"-mb-"+user+"-v4", "", actor)
	own := memberEnvelope(memberRoom+"-mb-"+user+"-v4-u", user, actor)
	set(room)
	set(own)
	return []*chatimv1.Event{room, own}
}

func TestMemberAddedHasARoomCopyAndAUserCopy(t *testing.T) {
	added := memberDoc("bob", domain.RoleMember, domain.MemberActive, domain.RoleAdmin, domain.MemberRemoved, "alice")
	want := bothCopies("bob", "alice", func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
			User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, JoinedAt: timestamppb.New(memberAt.Add(-time.Hour)), Ver: 4, RequestId: "r-1",
		}}
	})
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, added), want)
	added.PreviousRole, added.PreviousState = "", 0
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, added), want)
}

func TestMemberRemovedSaysWhetherTheUserLeft(t *testing.T) {
	removed := func(reason chatimv1.MemberRemovedReason, actor string) []*chatimv1.Event {
		return bothCopies("bob", actor, func(ev *chatimv1.Event) {
			ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
				User: "bob", Reason: reason, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, Ver: 4, RequestId: "r-1",
			}}
		})
	}
	byOwner := memberDoc("bob", domain.RoleAdmin, domain.MemberRemoved, domain.RoleAdmin, domain.MemberActive, "alice")
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, byOwner), removed(chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED, "alice"))
	left := memberDoc("bob", domain.RoleAdmin, domain.MemberRemoved, domain.RoleAdmin, domain.MemberActive, "bob")
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, left), removed(chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT, "bob"))
}

func TestMemberRoleChangedCarriesBothRolesAndOtherDocsCarryNothing(t *testing.T) {
	promoted := memberDoc("bob", domain.RoleOwner, domain.MemberActive, domain.RoleAdmin, domain.MemberActive, "")
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, promoted), bothCopies("bob", "", func(ev *chatimv1.Event) {
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
			User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, Ver: 4, RequestId: "r-1",
		}}
	}))
	for name, m := range map[string]domain.Member{
		"same role":          memberDoc("bob", domain.RoleAdmin, domain.MemberActive, domain.RoleAdmin, domain.MemberActive, "alice"),
		"removed twice":      memberDoc("bob", domain.RoleAdmin, domain.MemberRemoved, domain.RoleAdmin, domain.MemberRemoved, "alice"),
		"no state":           {Room: 1, User: "bob", Ver: 1},
		"unknown prev state": memberDoc("bob", domain.RoleAdmin, domain.MemberRemoved, domain.RoleAdmin, 7, "alice"),
	} {
		if got := pbconv.MemberEvents(domain.RoomGroup, m); got != nil {
			t.Errorf("%s: MemberEvents = %v, want nil", name, got)
		}
	}
}

func TestMembersOfANewRoomGetOnlyTheirUserCopy(t *testing.T) {
	m := domain.Member{
		Room: 9_007_199_254_740_993, Tenant: "acme", User: "bob", Role: domain.RoleMember, State: domain.MemberActive, JoinedAt: memberAt,
		Ver: 1, RequestID: domain.CreationRequestID(9_007_199_254_740_993), UpdatedBy: "alice", UpdatedAt: memberAt,
	}
	got := pbconv.MemberEvents(domain.RoomGroup, m)
	if len(got) != 1 || got[0].GetId() != memberRoom+"-mb-bob-v1-u" || got[0].GetRecipient() != "bob" || got[0].GetMemberAdded().GetRequestId() != memberRoom+"-created" {
		t.Fatalf("events of a creation doc = %v, want only the user copy", got)
	}
	m.RequestID = "r-2"
	if got := pbconv.MemberEvents(domain.RoomGroup, m); len(got) != 2 || got[0].GetRecipient() != "" {
		t.Fatalf("events of a first add by AddMembers = %v, want the room copy and the user copy", got)
	}
}

func TestMemberRolesMapBothWays(t *testing.T) {
	for _, r := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember} {
		back, err := pbconv.DomainMemberRole(pbconv.MemberRole(r))
		if err != nil || back != r {
			t.Fatalf("DomainMemberRole(MemberRole(%q)) = %q, %v", r, back, err)
		}
	}
	if got := pbconv.MemberRole("guest"); got != chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED {
		t.Fatalf("MemberRole(guest) = %v, want unspecified", got)
	}
	for _, r := range []chatimv1.MemberRole{chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED, chatimv1.MemberRole(9)} {
		if _, err := pbconv.DomainMemberRole(r); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("DomainMemberRole(%v) = %v, want ErrInvalidArgument", r, err)
		}
	}
}
```

`apps/core/internal/pbconv/read_test.go` (file mới):

```go
package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReadUpdatedGoesToTheRoomOrOnlyToTheReader(t *testing.T) {
	u := domain.ReadUpdate{Room: 42, Tenant: "acme", Type: domain.RoomDM, Members: 2, User: "bob", Pos: domain.ReadPosition{Seq: 7, Ver: 3}, At: memberAt}
	want := &chatimv1.Event{
		Id: "42-rd-bob-v3", Tenant: "acme", RoomId: "42", RoomType: chatimv1.RoomType_ROOM_TYPE_DM, Actor: "bob", Ts: timestamppb.New(memberAt),
		Payload: &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "bob", ReadSeq: 7, ReadVer: 3}},
	}
	if got := pbconv.ReadUpdated(u, true); !proto.Equal(got, want) {
		t.Fatalf("ReadUpdated(room wide) = %v, want %v", got, want)
	}
	want.Recipient = "bob"
	if got := pbconv.ReadUpdated(u, false); !proto.Equal(got, want) {
		t.Fatalf("ReadUpdated(reader only) = %v, want %v", got, want)
	}
}
```

`apps/core/internal/pbconv/member_count_test.go` (file mới):

```go
package pbconv_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberCountChangedCarriesTheCountAndItsVersion(t *testing.T) {
	r := domain.Room{ID: 42, Tenant: "acme", Type: domain.RoomGroup, MemberCount: 5, MemberCountVer: 8}
	want := &chatimv1.Event{
		Id: "42-members-v8", Tenant: "acme", RoomId: "42", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP, Ts: timestamppb.New(memberAt),
		Payload: &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{MemberCount: 5, MemberCountVer: 8}},
	}
	if got := pbconv.MemberCountChanged(r, memberAt); !proto.Equal(got, want) {
		t.Fatalf("MemberCountChanged = %v, want %v", got, want)
	}
	r.MemberCount = math.MaxInt32 + 1
	if got := pbconv.MemberCountChanged(r, memberAt).GetMemberCountChanged().GetMemberCount(); got != math.MaxInt32 {
		t.Fatalf("member_count above int32 = %d, want it clamped to %d", got, math.MaxInt32)
	}
}
```

`apps/core/internal/publish/stream_test.go`, thay:

```go
		t.Fatalf("max age %v duplicates %v", c.MaxAge, c.Duplicates)
	case c.RePublish == nil || c.RePublish.Source != "evt.*.room.*.*" ||
		c.RePublish.Destination != "live.{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}":
		t.Fatalf("republish %+v", c.RePublish)
```

bằng:

```go
		t.Fatalf("max age %v duplicates %v", c.MaxAge, c.Duplicates)
	case c.RePublish == nil || c.RePublish.Source != "evt.*.*.*.*" ||
		c.RePublish.Destination != "live.{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}":
		t.Fatalf("republish %+v", c.RePublish)
```

`apps/core/internal/publish/member_read_event_test.go` (file mới):

```go
package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func memberEvents(role domain.Role, state domain.MemberState, prevRole domain.Role, prevState domain.MemberState) []*chatimv1.Event {
	m := domain.Member{
		Room: roomA, Tenant: tenant, User: "bob", Role: role, State: state, JoinedAt: sentAt, Ver: 2,
		PreviousRole: prevRole, PreviousState: prevState, RequestID: "r-1", UpdatedBy: "alice", UpdatedAt: sentAt,
	}
	return pbconv.MemberEvents(domain.RoomGroup, m)
}

func TestMemberCountAndReadEventsGoToTheRoomOrTheUserSubjectWithoutAMark(t *testing.T) {
	added := memberEvents(domain.RoleMember, domain.MemberActive, "", 0)
	removed := memberEvents(domain.RoleMember, domain.MemberRemoved, domain.RoleMember, domain.MemberActive)
	changed := memberEvents(domain.RoleAdmin, domain.MemberActive, domain.RoleMember, domain.MemberActive)
	read := domain.ReadUpdate{Room: roomA, Tenant: tenant, Type: domain.RoomGroup, Members: 2, User: "bob", Pos: domain.ReadPosition{Seq: 7, Ver: 3}, At: sentAt}
	count := pbconv.MemberCountChanged(domain.Room{ID: roomA, Tenant: tenant, Type: domain.RoomGroup, MemberCount: 2, MemberCountVer: 4}, sentAt)
	cases := map[string]struct {
		ev          *chatimv1.Event
		subject, id string
	}{
		"added, room copy":        {added[0], "evt.acme.room.101.member_added", "101-mb-bob-v2"},
		"added, user copy":        {added[1], "evt.acme.user.bob.member_added", "101-mb-bob-v2-u"},
		"removed, room copy":      {removed[0], "evt.acme.room.101.member_removed", "101-mb-bob-v2"},
		"removed, user copy":      {removed[1], "evt.acme.user.bob.member_removed", "101-mb-bob-v2-u"},
		"role changed, user copy": {changed[1], "evt.acme.user.bob.member_role_changed", "101-mb-bob-v2-u"},
		"member count":            {count, "evt.acme.room.101.member_count_changed", "101-members-v4"},
		"read, room wide":         {pbconv.ReadUpdated(read, true), "evt.acme.room.101.read_updated", "101-rd-bob-v3"},
		"read, reader only":       {pbconv.ReadUpdated(read, false), "evt.acme.user.bob.read_updated", "101-rd-bob-v3"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject || publishtest.MsgID(msg) != c.id {
			t.Fatalf("%s: subject %q msg id %q, want %s and %s", name, msg.Subject, publishtest.MsgID(msg), c.subject, c.id)
		}
		if key, ok := publish.MarkKey(roomA, c.ev); ok {
			t.Fatalf("%s: MarkKey = %v, true; want no mark", name, key)
		}
	}
}

func TestARecipientMustBeOneSubjectToken(t *testing.T) {
	read := domain.ReadUpdate{Room: roomA, Tenant: tenant, Type: domain.RoomGroup, User: "bob", Pos: domain.ReadPosition{Seq: 1, Ver: 1}, At: sentAt}
	for _, to := range []string{"b.b", "b*", "b>", "b b"} {
		ev := pbconv.ReadUpdated(read, false)
		ev.Recipient = to
		if msg, err := publish.Message("evt", roomA, ev); err == nil {
			t.Fatalf("Message(recipient %q) = %q, want an error", to, msg.Subject)
		}
	}
}
```

`apps/core/internal/publish/nats_republish_integration_test.go` (file mới):

```go
package publish_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealJetStreamUpdatesTheRePublishRuleAndRoutesUserCopies(t *testing.T) {
	it := realStream(t, fastSetup)
	s, err := it.js.Stream(t.Context(), it.cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	legacy := info.Config
	legacy.RePublish = &jetstream.RePublish{
		Source:      it.cfg.SubjectRoot + ".*.room.*.*",
		Destination: it.cfg.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
	}
	if _, err := it.js.UpdateStream(t.Context(), legacy); err != nil {
		t.Fatalf("put back the M2b.3 RePublish rule: %v", err)
	}
	if err := publish.EnsureStream(t.Context(), it.js, it.cfg); err != nil {
		t.Fatalf("EnsureStream over the M2b.3 rule: %v", err)
	}
	if info, err = s.Info(t.Context()); err != nil {
		t.Fatalf("stream info after EnsureStream: %v", err)
	}
	wantSource := it.cfg.SubjectRoot + ".*.*.*.*"
	wantDest := it.cfg.LiveRoot + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}"
	if rp := info.Config.RePublish; rp == nil || rp.Source != wantSource || rp.Destination != wantDest {
		t.Fatalf("RePublish after EnsureStream = %+v, want %s -> %s", rp, wantSource, wantDest)
	}
	subscribe := func(subject string) *nats.Subscription {
		sub, err := it.nc.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe %s: %v", subject, err)
		}
		return sub
	}
	room := subscribe(it.cfg.LiveRoot + ".acme.room.101.evt.member_added")
	user := subscribe(it.cfg.LiveRoot + ".acme.user.bob.evt.member_added")
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	for _, ev := range memberEvents(domain.RoleMember, domain.MemberActive, "", 0) {
		msg, err := publish.Message(it.cfg.SubjectRoot, roomA, ev)
		if err != nil {
			t.Fatalf("Message(%s): %v", ev.GetId(), err)
		}
		if _, err := it.js.PublishMsg(t.Context(), msg); err != nil {
			t.Fatalf("publish %s on %s: %v", ev.GetId(), msg.Subject, err)
		}
	}
	for name, c := range map[string]struct {
		sub           *nats.Subscription
		id, recipient string
	}{
		"room copy": {room, "101-mb-bob-v2", ""},
		"user copy": {user, "101-mb-bob-v2-u", "bob"},
	} {
		msg, err := c.sub.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("%s: live message: %v", name, err)
		}
		got := &chatimv1.Event{}
		if err := proto.Unmarshal(msg.Data, got); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if got.GetId() != c.id || got.GetRecipient() != c.recipient || msg.Header.Get(jetstream.MsgIDHeader) != c.id {
			t.Fatalf("%s: live event %s for %q (msg id %q), want %s for %q", name, got.GetId(), got.GetRecipient(), msg.Header.Get(jetstream.MsgIDHeader), c.id, c.recipient)
		}
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: `FAIL ... [build failed]` ở cả hai package:
- `pbconv`: `apps/core/internal/pbconv/member_count_test.go:21:19: undefined: pbconv.MemberCountChanged`, rồi `undefined: pbconv.MemberEventID`, `pbconv.MemberUserEventID`, `pbconv.MemberCountEventID`, `pbconv.ReadEventID`…
- `publish`: `apps/core/internal/publish/member_read_event_test.go:18:16: undefined: pbconv.MemberEvents`, rồi `undefined: pbconv.MemberCountChanged`, `undefined: pbconv.ReadUpdated`.

**Step 4: Code pbconv**

`apps/core/internal/pbconv/member.go` (file mới):

```go
package pbconv

import (
	"fmt"
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnknownRole = fmt.Errorf("%w: role", apperr.ErrInvalidArgument)

func MemberEventID(room uint64, user string, ver uint32) string {
	return RoomID(room) + "-mb-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func MemberUserEventID(room uint64, user string, ver uint32) string {
	return MemberEventID(room, user, ver) + "-u"
}

func MemberRole(r domain.Role) chatimv1.MemberRole {
	switch r {
	case domain.RoleOwner:
		return chatimv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.RoleAdmin:
		return chatimv1.MemberRole_MEMBER_ROLE_ADMIN
	case domain.RoleMember:
		return chatimv1.MemberRole_MEMBER_ROLE_MEMBER
	default:
		return chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func DomainMemberRole(r chatimv1.MemberRole) (domain.Role, error) {
	switch r {
	case chatimv1.MemberRole_MEMBER_ROLE_OWNER:
		return domain.RoleOwner, nil
	case chatimv1.MemberRole_MEMBER_ROLE_ADMIN:
		return domain.RoleAdmin, nil
	case chatimv1.MemberRole_MEMBER_ROLE_MEMBER:
		return domain.RoleMember, nil
	default:
		return "", errUnknownRole
	}
}

func MemberEvents(roomType domain.RoomType, m domain.Member) []*chatimv1.Event {
	set := memberPayload(m)
	if set == nil {
		return nil
	}
	own := memberEnvelope(roomType, m, MemberUserEventID(m.Room, m.User, m.Ver), m.User)
	set(own)
	if m.Ver == 1 && m.RequestID == domain.CreationRequestID(m.Room) {
		return []*chatimv1.Event{own}
	}
	room := memberEnvelope(roomType, m, MemberEventID(m.Room, m.User, m.Ver), "")
	set(room)
	return []*chatimv1.Event{room, own}
}

func memberEnvelope(roomType domain.RoomType, m domain.Member, id, recipient string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: m.Tenant, RoomId: RoomID(m.Room), RoomType: RoomType(roomType),
		Actor: m.UpdatedBy, Ts: timestamppb.New(m.UpdatedAt), Recipient: recipient,
	}
}

func memberPayload(m domain.Member) func(*chatimv1.Event) {
	switch {
	case m.Active() && m.PreviousState != domain.MemberActive:
		return func(ev *chatimv1.Event) {
			ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
				User: m.User, Role: MemberRole(m.Role), JoinedAt: timestamppb.New(m.JoinedAt), Ver: m.Ver, RequestId: m.RequestID,
			}}
		}
	case m.State == domain.MemberRemoved && m.PreviousState == domain.MemberActive:
		return func(ev *chatimv1.Event) {
			ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
				User: m.User, Reason: removedReason(m), PreviousRole: MemberRole(m.PreviousRole), Ver: m.Ver, RequestId: m.RequestID,
			}}
		}
	case m.Active() && m.Role != m.PreviousRole:
		return func(ev *chatimv1.Event) {
			ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
				User: m.User, Role: MemberRole(m.Role), PreviousRole: MemberRole(m.PreviousRole), Ver: m.Ver, RequestId: m.RequestID,
			}}
		}
	default:
		return nil
	}
}

func removedReason(m domain.Member) chatimv1.MemberRemovedReason {
	if m.UpdatedBy == m.User {
		return chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT
	}
	return chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED
}
```

`apps/core/internal/pbconv/member_count.go` (file mới):

```go
package pbconv

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MemberCountEventID(room, ver uint64) string {
	return RoomID(room) + "-members-v" + strconv.FormatUint(ver, 10)
}

func MemberCountChanged(r domain.Room, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id:       MemberCountEventID(r.ID, r.MemberCountVer),
		Tenant:   r.Tenant,
		RoomId:   RoomID(r.ID),
		RoomType: RoomType(r.Type),
		Ts:       timestamppb.New(at),
		Payload: &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{
			MemberCount: memberCount(r.MemberCount), MemberCountVer: r.MemberCountVer,
		}},
	}
}
```

`apps/core/internal/pbconv/read.go` (file mới):

```go
package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func ReadEventID(room uint64, user string, readVer uint64) string {
	return RoomID(room) + "-rd-" + user + "-v" + strconv.FormatUint(readVer, 10)
}

func ReadUpdated(u domain.ReadUpdate, roomWide bool) *chatimv1.Event {
	ev := &chatimv1.Event{
		Id:       ReadEventID(u.Room, u.User, u.Pos.Ver),
		Tenant:   u.Tenant,
		RoomId:   RoomID(u.Room),
		RoomType: RoomType(u.Type),
		Actor:    u.User,
		Ts:       timestamppb.New(u.At),
		Payload:  &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: u.User, ReadSeq: u.Pos.Seq, ReadVer: u.Pos.Ver}},
	}
	if !roomWide {
		ev.Recipient = u.User
	}
	return ev
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: `ok` ở `pbconv`; `publish` FAIL đúng hai test:
- `TestMemberCountAndReadEventsGoToTheRoomOrTheUserSubjectWithoutAMark`: `member_read_event_test.go:43: <case>: Message: event needs an id, a subject-safe tenant and a known payload` (`<case>` là ca đầu tiên theo thứ tự map ngẫu nhiên);
- `TestEnsureStreamConfiguresDedupeAndRepublish`: `stream_test.go:48: republish &{Source:evt.*.room.*.* Destination:live.{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}} HeadersOnly:false}`.

**Step 5: Code publish**

`apps/core/internal/publish/stream.go`, thay:

```go
	msgUnpinned     = "msg_unpinned"
)
```

bằng:

```go
	msgUnpinned     = "msg_unpinned"

	memberAdded        = "member_added"
	memberRemoved      = "member_removed"
	memberRoleChanged  = "member_role_changed"
	memberCountChanged = "member_count_changed"
	readUpdated        = "read_updated"
)
```

thay:

```go
		RePublish: &jetstream.RePublish{
			Source:      c.SubjectRoot + ".*.room.*.*",
			Destination: c.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
		},
```

bằng:

```go
		RePublish: &jetstream.RePublish{
			Source:      c.SubjectRoot + ".*.*.*.*",
			Destination: c.LiveRoot + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}",
		},
```

thay:

```go
	return root + "." + tenant + ".room." + strconv.FormatUint(room, 10) + "." + kind
}
```

bằng:

```go
	return root + "." + tenant + ".room." + strconv.FormatUint(room, 10) + "." + kind
}

func userSubject(root, tenant, user, kind string) string {
	return root + "." + tenant + ".user." + user + "." + kind
}
```

`apps/core/internal/publish/message.go`, thay:

```go
var errMalformed = errors.New("event needs an id, a subject-safe tenant and a known payload")
```

bằng:

```go
var errMalformed = errors.New("event needs an id, a known payload and a subject-safe tenant and recipient")
```

thay:

```go
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: roomSubject(root, ev.GetTenant(), room, kind), Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
```

bằng:

```go
	}
	subject := roomSubject(root, ev.GetTenant(), room, kind)
	if to := ev.GetRecipient(); to != "" {
		if !validToken(to) {
			return nil, errMalformed
		}
		subject = userSubject(root, ev.GetTenant(), to, kind)
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
```

thay:

```go
		return msgUnpinned, true
	default:
```

bằng:

```go
		return msgUnpinned, true
	case *chatimv1.Event_MemberAdded:
		return memberAdded, true
	case *chatimv1.Event_MemberRemoved:
		return memberRemoved, true
	case *chatimv1.Event_MemberRoleChanged:
		return memberRoleChanged, true
	case *chatimv1.Event_MemberCountChanged:
		return memberCountChanged, true
	case *chatimv1.Event_ReadUpdated:
		return readUpdated, true
	default:
```

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: PASS (nats integration skip). `TestMalformedEventsAreDroppedWithOneLogLineAndOthersPublished` vẫn xanh (event không recipient đi đường cũ).

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo. `wc -l apps/core/internal/pbconv/member.go apps/core/internal/pbconv/member_count.go apps/core/internal/pbconv/read.go apps/core/internal/publish/message.go apps/core/internal/publish/stream.go` → `102`, `28`, `30`, `68`, `106`.

**Step 7: Integration (một test)**

Tạo `<scratchpad>/itest-one.mk` như ở "Quy tắc chung" nếu chưa có.

Run: `make infra-up && make -f <scratchpad>/itest-one.mk itest-one RUN='TestRealJetStream' PKG='-v ./apps/core/internal/publish/'`
Expected: `--- PASS` cho `TestRealJetStreamDedupesByMsgIDAndRepublishesLive` (subject room giữ nguyên), `TestRealJetStreamReportsUnstoredPublishesThroughTheErrHandler`, `TestRealJetStreamUpdatesTheRePublishRuleAndRoutesUserCopies` (luật M2b.3 đặt lại được bằng `UpdateStream`, `EnsureStream` đổi sang `.*.*.*.*`, bản room tới `live.…room.101.evt.member_added`, bản user tới `live.…user.bob.evt.member_added` với `recipient` `bob`). NATS từ chối sửa RePublish → **dừng, báo controller**.

**Step 8: Integration toàn repo**

Run: `make itest`
Expected: PASS mọi package (itest `apps/core` dựng stream tên ngẫu nhiên với luật mới; mọi subscription `live.{t}.room.{rid}.>` vẫn nhận event như cũ).

**Step 9: Commit**

INDEXES.csv:
- dòng `proto/chatim/v1/core.proto`, cột `purpose`: thay `UnpinMessage/GetReactionSettings (reaction, pin and reaction settings messages in reactions_pins.proto);` bằng `UnpinMessage/GetReactionSettings/AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole/MarkRead/MarkUnread (reaction, pin and reaction settings messages in reactions_pins.proto; member and read position messages in members.proto);`.
- dòng `proto/chatim/v1/core.proto`, cột `decisions`: thay `D95;D97` bằng `D95;D97;D99;D101;D105`.
- dòng `proto/chatim/v1/events.proto`, cột `purpose`: thay `MessagePinned (26)/MessageUnpinned (27); additive changes only` bằng `MessagePinned (26)/MessageUnpinned (27)/MemberAdded (28)/MemberRemoved (29)/MemberRoleChanged (30)/MemberCountChanged (31)/ReadUpdated (32); recipient (10): empty = the room subject, a user id = that user's subject; additive changes only`.
- dòng `proto/chatim/v1/events.proto`, cột `key_symbols`: thay `Event;MessageCreated;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned` bằng `Event;MessageCreated;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned;MemberAdded;MemberRemoved;MemberRoleChanged;MemberCountChanged;ReadUpdated`.
- dòng `proto/chatim/v1/events.proto`, cột `decisions`: thay `D17;D48;D93` bằng `D17;D48;D93;D102;D104;D105`.
- dòng `pkg/pb/chatim/v1`, cột `key_symbols`: thay `MessagePinned;MessageUnpinned;Message;` bằng `MessagePinned;MessageUnpinned;MemberAdded;MemberRemoved;MemberRoleChanged;MemberCountChanged;ReadUpdated;MemberRole;Message;`.
- dòng `apps/core/internal/pbconv`, cột `purpose`: thay `PinChanged picks by op and drops the text of a deleted target; Pins` bằng `PinChanged picks by op and drops the text of a deleted target; Pins; MemberEventID {room}-mb-{user}-v{ver} (room copy), MemberUserEventID (+ -u, user copy), MemberCountEventID {room}-members-v{ver}, ReadEventID {room}-rd-{user}-v{read_ver} (table test: no two kinds share an id); MemberRole/DomainMemberRole (unspecified -> ErrInvalidArgument); MemberEvents of one member doc: member_added (now active, was not), member_removed (now removed, was active; reason left when updated_by is the user), member_role_changed (active, role moved), else none; room copy then user copy (recipient = the user), no room copy for a doc made with its room (ver 1, request {room}-created); actor/ts = updated_by/updated_at; MemberCountChanged (no actor, count clamped to int32); ReadUpdated (actor = reader; recipient = reader unless room wide)`.
- dòng `apps/core/internal/pbconv`, cột `key_symbols`: thay `PinChanged;Pins;` bằng `PinChanged;Pins;MemberEventID;MemberUserEventID;MemberCountEventID;ReadEventID;MemberRole;DomainMemberRole;MemberEvents;MemberCountChanged;ReadUpdated;`.
- dòng `apps/core/internal/pbconv`, cột `decisions`: thay `D48;D83;D90;D93` bằng `D48;D83;D90;D93;D102;D104;D105`.
- dòng `apps/core/internal/publish`, cột `purpose`: thay `EnsureStream with RePublish to live.* and a 5m duplicate window;` bằng `EnsureStream with RePublish {root}.*.*.*.* -> {live}.{1}.{2}.{3}.evt.{4} (room and user subjects; rewrites the rule of an existing stream) and a 5m duplicate window;`.
- dòng `apps/core/internal/publish`, cột `purpose`: thay `publishes msg_created/room_created/msg_edited/msg_deleted/reaction_changed/counts_changed/msg_pinned/msg_unpinned;` bằng `publishes msg_created/room_created/msg_edited/msg_deleted/reaction_changed/counts_changed/msg_pinned/msg_unpinned/member_added/member_removed/member_role_changed/member_count_changed/read_updated; an event with a recipient goes to {root}.{tenant}.user.{recipient}.{kind} (recipient must be one subject token), else to the room subject;`.
- dòng `apps/core/internal/publish`, cột `tests`: thay `async err handler on real NATS)` bằng `async err handler; RePublish rule rewritten on an existing stream and user copies live on real NATS)`.
- dòng `apps/core/internal/publish`, cột `decisions`: thay `D83;D93` bằng `D83;D93;D102;D104;D105`.
- thêm dòng mới ngay sau dòng `proto/chatim/v1/reactions_pins.proto`:

  ```csv
  proto/chatim/v1/members.proto,proto,"Member and read position messages (same package chatim.v1, imported by core.proto and events.proto): MemberRole (owner/admin/member), MemberRemovedReason (removed/left); AddMembers (users + request_id) / RemoveMember / LeaveRoom / ChangeMemberRole requests and responses (added users with ver, changed, ver, new_owner, previous_role); MarkRead/MarkUnread requests and responses (read_seq, read_ver); event payloads MemberAdded/MemberRemoved/MemberRoleChanged (user, roles, ver, request_id), MemberCountChanged (member_count, member_count_ver), ReadUpdated (user, read_seq, read_ver); new fields use the database names",MemberRole;MemberRemovedReason;AddMembersRequest;AddedMember;AddMembersResponse;RemoveMemberRequest;RemoveMemberResponse;LeaveRoomRequest;LeaveRoomResponse;ChangeMemberRoleRequest;ChangeMemberRoleResponse;MarkReadRequest;MarkReadResponse;MarkUnreadRequest;MarkUnreadResponse;MemberAdded;MemberRemoved;MemberRoleChanged;MemberCountChanged;ReadUpdated,pkg/pb/chatim/v1,buf-lint,D99;D102;D104;D105
  ```

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint && make buf-lint
git add proto/chatim/v1/members.proto pkg/pb/chatim/v1/members.pb.go apps/core/internal/pbconv/member.go apps/core/internal/pbconv/member_count.go apps/core/internal/pbconv/read.go apps/core/internal/pbconv/member_event_id_test.go apps/core/internal/pbconv/member_test.go apps/core/internal/pbconv/member_count_test.go apps/core/internal/pbconv/read_test.go apps/core/internal/publish/member_read_event_test.go apps/core/internal/publish/nats_republish_integration_test.go
git commit -m "feat(proto): add member, member count and read events with user subjects" -- proto/chatim/v1/ pkg/pb/chatim/v1/ apps/core/internal/pbconv/ apps/core/internal/publish/ INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `20 files changed`.

---

### Task 4: Store port + `MemberChanged`/`Change.Member` + `RecordOf` + memstore 5 port + storetest `RunMembers` + write contract

Đặt năm port của lớp tập member (D98–D105) và ngữ nghĩa chuẩn qua storetest `RunMembers`, cài trên memstore; Mongo cài ở Task 5 và chạy cùng bộ case.

Ngữ nghĩa chốt bằng case (cả hai adapter):
- `Rooms.Create` ghi **doc tạo cùng room** qua `store.CreationMember(r, m)`: active, `Ver 1`, `RequestID {room}-created`, `UpdatedAt/UpdatedBy` lấy của member nếu có, không thì `r.CreatedAt/r.CreatedBy`; `ReadSeq/ReadVer/Previous*` = 0. `NewRoom` đã đưa đúng các giá trị này, nên với đường thật đây là phép đồng nhất; fixture cũ (chỉ có `Room/Tenant/User/Role/JoinedAt`) vẫn tạo được member active, không phải sửa ~10 file test của task khác.
- `Rooms.Member`, `ClearHistory`, `MarkRead`, `MarkUnread`: doc không có **hoặc** không active → `domain.ErrNotMember`.
- `AddMembers(j, users)`: mỗi user `j.Apply(cur, user)`; doc active không ghi gì; trả doc sau ghi theo thứ tự `users`. Không đụng `rooms`.
- `ApplyMember(cur, next)`: CAS theo `ver`; ghi đúng `role, state, previous_role, previous_state, ver, request_id, updated_by, updated_at`; trượt hoặc không có doc → `(false, nil)`.
- `MembersOf`: mọi state, theo thứ tự `users` (user lặp → doc lặp, user không có doc bị bỏ), ≤ `MaxMemberBatch + 1` user.
- `Owners(limit)`: owner active sắp `joined_at`, `user_id`. `Successor`: active, admin trước member, rồi `joined_at`, `user_id`.
- `MembersBetween`: mọi state, `updated_at ∈ [from, to]`, sắp `updated_at` rồi `_id`. `_id` là BinData `room(8)+user`, Mongo so BinData **theo độ dài trước**, nên khi trùng thời điểm user ngắn đứng trước (`bob` trước `alice`); memstore sắp `(UpdatedAt, len(User), User)` cho khớp.
- `OwnerState` (room không có → `ErrRoomNotFound`), `BeginOwnerChange` (CAS `owners_ver == base` và không có pending; room không có → false), `EndOwnerChange` (xoá pending khi `owners_ver == ver`; `ver 0` → false).
- `CountMembers`: mọi witness phải có doc `ver ≥ N`, không thì `ErrStaleRead`; đếm doc active. `SetMemberCount`: `c.Ver == base + 1`, `Count ≥ 0`; CAS `member_count_ver` (0 = chưa có).
- `MarkRead` chỉ nâng (`read_seq < seq`), `MarkUnread` chỉ hạ (`read_seq > to`); mỗi lần đổi `read_ver + 1`; không đổi `ver` (case cuối: `ApplyMember` với `ver 1` vẫn khớp sau ba lần đổi vị trí đọc).

Thêm ngoài hợp đồng (Part B/C được dùng): `store.CreationMember`, `store.ValidateMemberCount(base, c)` (`Count ≥ 0`, `Ver == base+1 ≤ MaxInt64`), `store.ValidateReadSeq(seq)` (`≤ MaxInt64`). `MembersOf` validate số user bằng `ValidateLimit(len(users), MaxMemberBatch+1)` (danh sách rỗng → `ErrInvalidArgument`).

`work.RecordOf` có case `MemberChanged` (lint `exhaustive`: switch không có `default`); `KnownKind`, `checkKind`, `ID()` đổi ở Task 6.

**Khoảng trống tạm Task 4 → 5:** storetest `member()` giờ là doc tạo đầy đủ, nên trên Mongo (chưa lưu field mới) 5 case fail: `TestMongoStoreContract/Rooms/{create_then_get_room_and_members, create_of_an_existing_id_fails,_keeps_the_room_and_adds_no_members, membership_is_per_room}`, `TestMongoEditsContract/{cancelled_context_writes_nothing, clear_history_only_moves_forward_and_is_per_member}`. Task này không đổi adapter Mongo nên không chạy `make itest`; Task 5 sửa và chạy itest.

**Files:**
- Create: `apps/core/internal/store/member.go`, `apps/core/internal/store/member_validate.go`, `apps/core/internal/store/member_validate_test.go`
- Modify: `apps/core/internal/store/feed.go`, `apps/core/internal/store/write_contract_test.go`
- Modify: `apps/core/internal/work/record.go`, `apps/core/internal/work/record_test.go`
- Create: `apps/core/internal/store/memstore/members.go`, `owner_state.go`, `member_count.go`, `read_position.go`
- Modify: `apps/core/internal/store/memstore/rooms.go`, `apps/core/internal/store/memstore/memstore_test.go`
- Create: `apps/core/internal/store/storetest/member_cases.go`, `member_change_cases.go`, `member_order_cases.go`, `owner_count_cases.go`, `read_position_cases.go`
- Modify: `apps/core/internal/store/storetest/rooms_cases.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/work`)

**Step 1: Test**

`apps/core/internal/store/member_validate_test.go` (file mới):

```go
package store_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var memberAt = time.UnixMilli(1_700_000_000_000).UTC()

func TestCreationMemberIsActiveAtVersionOne(t *testing.T) {
	r := domain.Room{ID: 42, Tenant: "acme", CreatedBy: "alice", CreatedAt: memberAt}
	bare := domain.Member{Room: 42, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: memberAt, ReadSeq: 9, State: domain.MemberRemoved}
	want := domain.Member{
		Room: 42, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: memberAt,
		State: domain.MemberActive, Ver: 1, RequestID: "42-created", UpdatedAt: memberAt, UpdatedBy: "alice",
	}
	if got := store.CreationMember(r, bare); got != want {
		t.Fatalf("CreationMember = %+v, want %+v", got, want)
	}
	bare.UpdatedBy, bare.UpdatedAt = "carol", memberAt.Add(time.Second)
	want.UpdatedBy, want.UpdatedAt = "carol", memberAt.Add(time.Second)
	if got := store.CreationMember(r, bare); got != want {
		t.Fatalf("CreationMember(with audit) = %+v, want %+v", got, want)
	}
}

func TestValidateMemberCountAndReadSeq(t *testing.T) {
	if err := store.ValidateMemberCount(4, domain.MemberCount{Count: 0, Ver: 5}); err != nil {
		t.Fatalf("ValidateMemberCount(base 4, ver 5) = %v", err)
	}
	for name, c := range map[string]struct {
		base uint64
		c    domain.MemberCount
	}{
		"same ver":       {4, domain.MemberCount{Count: 1, Ver: 4}},
		"negative":       {4, domain.MemberCount{Count: -1, Ver: 5}},
		"above max int":  {math.MaxInt64, domain.MemberCount{Count: 1, Ver: math.MaxInt64 + 1}},
		"base overflows": {math.MaxUint64, domain.MemberCount{Count: 1, Ver: 0}},
	} {
		if err := store.ValidateMemberCount(c.base, c.c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateMemberCount = %v, want ErrInvalidArgument", name, err)
		}
	}
	if store.ValidateReadSeq(math.MaxInt64) != nil || !errors.Is(store.ValidateReadSeq(math.MaxInt64+1), apperr.ErrInvalidArgument) {
		t.Fatal("ValidateReadSeq must accept up to MaxInt64 and reject above")
	}
}

func TestValidateMemberChangeAcceptsOneStep(t *testing.T) {
	cur := domain.Member{Room: 42, User: "bob", Role: domain.RoleMember, State: domain.MemberActive, Ver: math.MaxUint32 - 1}
	if err := store.ValidateMemberChange(cur, cur.Next(domain.RoleOwner, domain.MemberActive, "r-1", "", memberAt)); err != nil {
		t.Fatalf("ValidateMemberChange(to MaxUint32) = %v", err)
	}
	cur.Ver = math.MaxUint32
	next := cur
	next.Ver = 0
	if err := store.ValidateMemberChange(cur, next); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("ValidateMemberChange(past MaxUint32) = %v, want ErrInvalidArgument", err)
	}
	cur.Ver = 0
	if err := store.ValidateMemberChange(cur, cur.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "", memberAt)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("ValidateMemberChange(no doc) = %v, want ErrInvalidArgument", err)
	}
}
```

`apps/core/internal/store/write_contract_test.go`, thay:

```go
	"PinProjector.ApplyPins":         "cas",
}
```

bằng:

```go
	"PinProjector.ApplyPins":         "cas",

	"MemberWriter.AddMembers":       "version-bump",
	"MemberWriter.ApplyMember":      "cas",
	"MemberReader.MembersOf":        "read",
	"MemberReader.Owners":           "read",
	"MemberReader.Successor":        "read",
	"MemberReader.MembersBetween":   "read",
	"OwnerChanges.OwnerState":       "read",
	"OwnerChanges.BeginOwnerChange": "cas",
	"OwnerChanges.EndOwnerChange":   "cas",
	"MemberCounts.CountMembers":     "read",
	"MemberCounts.SetMemberCount":   "cas",
	"ReadPositions.MarkRead":        "version-bump",
	"ReadPositions.MarkUnread":      "version-bump",
}
```

thay:

```go
		reflect.TypeFor[store.PinProjector](),
	}
```

bằng:

```go
		reflect.TypeFor[store.PinProjector](),
		reflect.TypeFor[store.MemberWriter](),
		reflect.TypeFor[store.MemberReader](),
		reflect.TypeFor[store.OwnerChanges](),
		reflect.TypeFor[store.MemberCounts](),
		reflect.TypeFor[store.ReadPositions](),
	}
```

`apps/core/internal/work/record_test.go`, thay:

```go
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
```

bằng:

```go
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
	member := store.Change{Kind: store.MemberChanged, Member: domain.Member{Room: 42, User: "bob", Ver: 3, Role: domain.RoleAdmin}, CommittedAt: committed}
	if got, want := work.RecordOf(member), (work.Record{Kind: store.MemberChanged, Room: 42, Version: 3, User: "bob", CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(member) = %+v, want %+v", got, want)
	}
```

`apps/core/internal/store/storetest/rooms_cases.go`, thay:

```go
func member(room uint64, user string, role domain.Role) domain.Member {
	return domain.Member{Room: room, Tenant: tenant, User: user, Role: role, JoinedAt: baseTime}
}
```

bằng:

```go
func member(room uint64, user string, role domain.Role) domain.Member {
	return domain.Member{
		Room: room, Tenant: tenant, User: user, Role: role, JoinedAt: baseTime,
		State: domain.MemberActive, Ver: 1, RequestID: domain.CreationRequestID(room), UpdatedAt: baseTime, UpdatedBy: "alice",
	}
}
```

thay:

```go
	}
	gotAt, wantAt, gotCleared, wantCleared := got.JoinedAt, want.JoinedAt, got.ClearedBeforeTime, want.ClearedBeforeTime
	got.JoinedAt, want.JoinedAt, got.ClearedBeforeTime, want.ClearedBeforeTime = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) || !gotCleared.Equal(wantCleared) {
		t.Fatalf("Member(%d, %q) = %+v at %v cleared %v, want %+v at %v cleared %v", want.Room, want.User, got, gotAt, gotCleared, want, wantAt, wantCleared)
	}
```

bằng:

```go
	}
	if !sameMember(got, want) {
		t.Fatalf("Member(%d, %q) = %+v, want %+v", want.Room, want.User, got, want)
	}
```

`apps/core/internal/store/storetest/member_cases.go` (file mới):

```go
package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type MemberRooms interface {
	store.Rooms
	store.HistoryClearer
	store.MemberWriter
	store.MemberReader
	store.OwnerChanges
	store.MemberCounts
	store.ReadPositions
}

type memberCase struct {
	name string
	run  func(t *testing.T, s MemberRooms)
}

func RunMembers(t *testing.T, open func(t *testing.T) MemberRooms) {
	t.Helper()
	for _, c := range slices.Concat(memberWriteCases(), memberChangeCases(), memberOrderCases(), ownerCountCases(), readPositionCases()) {
		t.Run(c.name, func(t *testing.T) {
			c.run(t, open(t))
		})
	}
}

func memberWriteCases() []memberCase {
	return []memberCase{
		{"create writes active creation docs", membersCreated},
		{"add members adds, re-adds and leaves active docs alone", membersAdded},
	}
}

func joinOf(room uint64, requestID string, after time.Duration, readSeq uint64) domain.Join {
	return domain.Join{Room: room, Tenant: tenant, RequestID: requestID, By: "alice", At: baseTime.Add(after), ReadSeq: readSeq}
}

func sameMember(a, b domain.Member) bool {
	at := a.JoinedAt.Equal(b.JoinedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.ClearedBeforeTime.Equal(b.ClearedBeforeTime)
	a.JoinedAt, a.UpdatedAt, a.ClearedBeforeTime = time.Time{}, time.Time{}, time.Time{}
	b.JoinedAt, b.UpdatedAt, b.ClearedBeforeTime = time.Time{}, time.Time{}, time.Time{}
	return at && a == b
}

func assertMembers(t *testing.T, op string, got, want []domain.Member) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameMember) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func mustAdd(t *testing.T, s MemberRooms, j domain.Join, users ...string) []domain.Member {
	t.Helper()
	got, err := s.AddMembers(t.Context(), j, users)
	if err != nil {
		t.Fatalf("AddMembers(%v by %s): %v", users, j.RequestID, err)
	}
	return got
}

func mustApplyMember(t *testing.T, s MemberRooms, cur, next domain.Member, want bool) {
	t.Helper()
	if ok, err := s.ApplyMember(t.Context(), cur, next); err != nil || ok != want {
		t.Fatalf("ApplyMember(%s v%d -> v%d) = %v, %v; want %v", cur.User, cur.Ver, next.Ver, ok, err, want)
	}
}

func membersOf(t *testing.T, s MemberRooms, room uint64, users ...string) []domain.Member {
	t.Helper()
	got, err := s.MembersOf(t.Context(), room, users)
	if err != nil {
		t.Fatalf("MembersOf(%d, %v): %v", room, users, err)
	}
	return got
}

func membersCreated(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	assertMembers(t, "MembersOf", membersOf(t, s, roomA, "bob", "zed", "alice"), []domain.Member{members[1], members[0]})
	bare := domain.Member{Room: roomB, Tenant: tenant, User: "carol", Role: domain.RoleOwner, JoinedAt: baseTime}
	mustCreate(t, s, group(roomB, "Other", 1), []domain.Member{bare})
	assertMember(t, s, member(roomB, "carol", domain.RoleOwner))
}

func membersAdded(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	first := joinOf(roomA, "r-1", time.Minute, 7)
	carol := domain.Member{
		Room: roomA, Tenant: tenant, User: "carol", Role: domain.RoleMember, State: domain.MemberActive, JoinedAt: first.At, Ver: 1,
		RequestID: "r-1", UpdatedBy: "alice", UpdatedAt: first.At, ReadSeq: 7, ReadVer: 1,
	}
	assertMembers(t, "AddMembers(carol, alice)", mustAdd(t, s, first, "carol", "alice"), []domain.Member{carol, members[0]})
	assertMembers(t, "AddMembers again", mustAdd(t, s, joinOf(roomA, "r-2", 2*time.Minute, 9), "carol"), []domain.Member{carol})
	if _, err := s.ClearHistory(t.Context(), roomA, "carol", baseTime.Add(90*time.Second)); err != nil {
		t.Fatalf("ClearHistory(carol): %v", err)
	}
	carol.ClearedBeforeTime = baseTime.Add(90 * time.Second)
	gone := carol.Next(domain.RoleMember, domain.MemberRemoved, "r-3", "carol", baseTime.Add(3*time.Minute))
	mustApplyMember(t, s, carol, gone, true)
	back := joinOf(roomA, "r-4", 4*time.Minute, 5)
	want := back.Apply(gone, "carol")
	if want.Ver != 3 || want.ReadSeq != 7 || want.ReadVer != 2 || !want.ClearedBeforeTime.Equal(carol.ClearedBeforeTime) {
		t.Fatalf("Join.Apply(tombstone) = %+v, want ver 3, read seq 7 v2 and the clear mark kept", want)
	}
	assertMembers(t, "AddMembers(re-add)", mustAdd(t, s, back, "carol"), []domain.Member{want})
	assertMember(t, s, want)
	assertMembers(t, "MembersOf after re-add", membersOf(t, s, roomA, "carol"), []domain.Member{want})
}
```

`apps/core/internal/store/storetest/member_change_cases.go` (file mới):

```go
package storetest

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberChangeCases() []memberCase {
	return []memberCase{
		{"apply member is a CAS on ver and keeps the reader state", memberApplyCAS},
		{"a removed member keeps the doc but loses access", memberRemovedLosesAccess},
		{"invalid member writes are rejected", memberWritesInvalid},
	}
}

func memberApplyCAS(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	bob := members[1]
	if _, _, err := s.MarkRead(t.Context(), roomA, "bob", 4); err != nil {
		t.Fatalf("MarkRead(bob): %v", err)
	}
	bob.ReadSeq, bob.ReadVer = 4, 1
	admin := bob.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "alice", baseTime.Add(time.Minute))
	mustApplyMember(t, s, bob, admin, true)
	mustApplyMember(t, s, bob, admin, false)
	stale := members[1].Next(domain.RoleOwner, domain.MemberActive, "r-2", "alice", baseTime.Add(2*time.Minute))
	mustApplyMember(t, s, members[1], stale, false)
	ghost := member(roomA, "zed", domain.RoleMember)
	mustApplyMember(t, s, ghost, ghost.Next(domain.RoleAdmin, domain.MemberActive, "r-3", "alice", baseTime), false)
	assertMember(t, s, admin)
	if admin.PreviousRole != domain.RoleMember || admin.Ver != 2 || admin.ReadSeq != 4 || admin.ReadVer != 1 {
		t.Fatalf("Next = %+v, want previous role member, ver 2 and the read position kept", admin)
	}
	assertMembers(t, "MembersOf(zed)", membersOf(t, s, roomA, "zed"), nil)
}

func memberRemovedLosesAccess(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	gone := members[1].Next(domain.RoleMember, domain.MemberRemoved, "r-1", "bob", baseTime.Add(time.Minute))
	mustApplyMember(t, s, members[1], gone, true)
	assertNotMember(t, s, roomA, "bob")
	_, err := s.ClearHistory(t.Context(), roomA, "bob", baseTime.Add(time.Hour))
	assertErrorIs(t, "ClearHistory(removed)", err, domain.ErrNotMember)
	_, _, err = s.MarkRead(t.Context(), roomA, "bob", 1)
	assertErrorIs(t, "MarkRead(removed)", err, domain.ErrNotMember)
	_, _, err = s.MarkUnread(t.Context(), roomA, "bob", 0)
	assertErrorIs(t, "MarkUnread(removed)", err, domain.ErrNotMember)
	assertMembers(t, "MembersOf(bob)", membersOf(t, s, roomA, "bob"), []domain.Member{gone})
	if n, err := s.CountMembers(t.Context(), roomA, nil); err != nil || n != 1 {
		t.Fatalf("CountMembers = %d, %v; want 1 active member", n, err)
	}
}

func memberWritesInvalid(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	bob := members[1]
	tooMany := make([]string, domain.MaxMemberBatch+1)
	for i := range tooMany {
		tooMany[i] = "u" + strconv.Itoa(i)
	}
	joins := map[string]struct {
		j     domain.Join
		users []string
	}{
		"zero room":        {joinOf(0, "r-1", 0, 0), []string{"carol"}},
		"bad request id":   {joinOf(roomA, "r 1", 0, 0), []string{"carol"}},
		"no time":          {domain.Join{Room: roomA, Tenant: tenant, RequestID: "r-1", By: "alice"}, []string{"carol"}},
		"read seq too big": {joinOf(roomA, "r-1", 0, math.MaxInt64+1), []string{"carol"}},
		"no users":         {joinOf(roomA, "r-1", 0, 0), nil},
		"duplicate users":  {joinOf(roomA, "r-1", 0, 0), []string{"carol", "carol"}},
		"bad user":         {joinOf(roomA, "r-1", 0, 0), []string{"c.d"}},
		"too many users":   {joinOf(roomA, "r-1", 0, 0), tooMany},
	}
	for name, c := range joins {
		_, err := s.AddMembers(t.Context(), c.j, c.users)
		assertErrorIs(t, "AddMembers("+name+")", err, apperr.ErrInvalidArgument)
	}
	jump := bob.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "alice", baseTime)
	jump.Ver = 3
	other := bob.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "alice", baseTime)
	other.User = "carol"
	badState := bob.Next(domain.RoleAdmin, 3, "r-1", "alice", baseTime)
	badRole := bob.Next("guest", domain.MemberActive, "r-1", "alice", baseTime)
	for name, next := range map[string]domain.Member{"ver jump": jump, "other user": other, "bad state": badState, "bad role": badRole} {
		_, err := s.ApplyMember(t.Context(), bob, next)
		assertErrorIs(t, "ApplyMember("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, err := s.MembersOf(t.Context(), roomA, append(tooMany, "x"))
	assertErrorIs(t, "MembersOf(too many)", err, apperr.ErrInvalidArgument)
	assertMember(t, s, bob)
	assertMembers(t, "MembersOf(carol)", membersOf(t, s, roomA, "carol"), nil)
	if _, err := s.ApplyMember(t.Context(), bob, bob.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "", baseTime)); err != nil {
		t.Fatalf("ApplyMember without updated_by = %v, want it accepted", err)
	}
}
```

`apps/core/internal/store/storetest/member_order_cases.go` (file mới):

```go
package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberOrderCases() []memberCase {
	return []memberCase{
		{"owners and successor follow role, join time and user id", memberOwnersAndSuccessor},
		{"members between returns every state by update time", memberBetween},
	}
}

func joined(room uint64, user string, role domain.Role, after time.Duration) domain.Member {
	m := member(room, user, role)
	m.JoinedAt = baseTime.Add(after)
	return m
}

func memberOwnersAndSuccessor(t *testing.T, s MemberRooms) {
	members := []domain.Member{
		joined(roomA, "zoe", domain.RoleOwner, time.Minute),
		joined(roomA, "amy", domain.RoleOwner, time.Minute),
		joined(roomA, "old", domain.RoleOwner, 0),
		joined(roomA, "mia", domain.RoleMember, 0),
		joined(roomA, "ben", domain.RoleAdmin, 2*time.Minute),
		joined(roomA, "ada", domain.RoleAdmin, 2*time.Minute),
		joined(roomA, "gus", domain.RoleAdmin, time.Minute),
	}
	mustCreate(t, s, group(roomA, "Team", len(members)), members)
	gone := members[2].Next(domain.RoleOwner, domain.MemberRemoved, "r-1", "old", baseTime.Add(time.Hour))
	mustApplyMember(t, s, members[2], gone, true)
	owners, err := s.Owners(t.Context(), roomA, 2)
	assertMembersErr(t, "Owners(2)", owners, err, []domain.Member{members[1], members[0]})
	owners, err = s.Owners(t.Context(), roomA, 1)
	assertMembersErr(t, "Owners(1)", owners, err, []domain.Member{members[1]})
	next, ok, err := s.Successor(t.Context(), roomA)
	if err != nil || !ok || !sameMember(next, members[6]) {
		t.Fatalf("Successor = %+v, %v, %v; want the earliest admin gus", next, ok, err)
	}
	gus := members[6].Next(domain.RoleAdmin, domain.MemberRemoved, "r-2", "gus", baseTime.Add(time.Hour))
	mustApplyMember(t, s, members[6], gus, true)
	if next, ok, err = s.Successor(t.Context(), roomA); err != nil || !ok || !sameMember(next, members[5]) {
		t.Fatalf("Successor after gus left = %+v, %v, %v; want ada (tie on join time broken by user id)", next, ok, err)
	}
	mustCreate(t, s, group(roomB, "Solo", 1), []domain.Member{member(roomB, "carol", domain.RoleOwner)})
	if next, ok, err = s.Successor(t.Context(), roomB); err != nil || ok || next.User != "" {
		t.Fatalf("Successor of an owner-only room = %+v, %v, %v; want none", next, ok, err)
	}
	if next, ok, err = s.Successor(t.Context(), roomB+9); err != nil || ok {
		t.Fatalf("Successor of a missing room = %+v, %v, %v; want none", next, ok, err)
	}
	for _, limit := range []int{0, 1001} {
		_, err := s.Owners(t.Context(), roomA, limit)
		assertErrorIs(t, "Owners(bad limit)", err, apperr.ErrInvalidArgument)
	}
}

func assertMembersErr(t *testing.T, op string, got []domain.Member, err error, want []domain.Member) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	assertMembers(t, op, got, want)
}

func memberBetween(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	mustCreate(t, s, group(roomB, "Other", 1), []domain.Member{member(roomB, "carol", domain.RoleOwner)})
	dan := mustAdd(t, s, joinOf(roomA, "r-1", time.Minute, 0), "dan", "eve")
	gone := dan[0].Next(domain.RoleMember, domain.MemberRemoved, "r-2", "dan", baseTime.Add(2*time.Minute))
	mustApplyMember(t, s, dan[0], gone, true)
	cases := []struct {
		name     string
		from, to time.Duration
		limit    int
		want     []domain.Member
	}{
		{"everything, ties by _id with shorter ids first", 0, time.Hour, 10, []domain.Member{members[1], members[0], dan[1], gone}},
		{"limit", 0, time.Hour, 3, []domain.Member{members[1], members[0], dan[1]}},
		{"inclusive bounds", time.Minute, 2 * time.Minute, 10, []domain.Member{dan[1], gone}},
		{"empty range", 3 * time.Minute, time.Hour, 10, nil},
	}
	for _, c := range cases {
		got, err := s.MembersBetween(t.Context(), roomA, baseTime.Add(c.from), baseTime.Add(c.to), c.limit)
		assertMembersErr(t, "MembersBetween("+c.name+")", got, err, c.want)
	}
	for _, limit := range []int{0, 1001} {
		_, err := s.MembersBetween(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, "MembersBetween(bad limit)", err, apperr.ErrInvalidArgument)
	}
}
```

`apps/core/internal/store/storetest/owner_count_cases.go` (file mới):

```go
package storetest

import (
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func ownerCountCases() []memberCase {
	return []memberCase{
		{"an owner change is a CAS on the owners version with one pending change", ownerChangeCAS},
		{"member count needs its witnesses and moves by CAS", memberCountCAS},
	}
}

func pendingLeave() domain.OwnerChange {
	return domain.OwnerChange{
		Action: domain.OwnerLeave, User: "alice", UserVer: 1, Successor: "bob", SuccessorVer: 1,
		RequestID: "r-1", UpdatedBy: "alice", UpdatedAt: baseTime.Add(time.Minute),
	}
}

func assertOwnerState(t *testing.T, s MemberRooms, room uint64, want domain.OwnerState) {
	t.Helper()
	got, err := s.OwnerState(t.Context(), room)
	if err != nil || !sameOwnerState(got, want) {
		t.Fatalf("OwnerState(%d) = %+v (pending %+v), %v; want %+v (pending %+v)", room, got, got.Pending, err, want, want.Pending)
	}
}

func sameOwnerState(a, b domain.OwnerState) bool {
	if a.Ver != b.Ver || (a.Pending == nil) != (b.Pending == nil) {
		return false
	}
	if a.Pending == nil {
		return true
	}
	pa, pb := *a.Pending, *b.Pending
	at := pa.UpdatedAt.Equal(pb.UpdatedAt)
	pa.UpdatedAt, pb.UpdatedAt = time.Time{}, time.Time{}
	return at && pa == pb
}

func ownerChangeCAS(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	assertOwnerState(t, s, roomA, domain.OwnerState{})
	c := pendingLeave()
	begin := func(base uint64, c domain.OwnerChange, want bool) {
		t.Helper()
		if ok, err := s.BeginOwnerChange(t.Context(), roomA, base, c); err != nil || ok != want {
			t.Fatalf("BeginOwnerChange(base %d) = %v, %v; want %v", base, ok, err, want)
		}
	}
	end := func(room, ver uint64, want bool) {
		t.Helper()
		if ok, err := s.EndOwnerChange(t.Context(), room, ver); err != nil || ok != want {
			t.Fatalf("EndOwnerChange(%d, %d) = %v, %v; want %v", room, ver, ok, err, want)
		}
	}
	begin(0, c, true)
	assertOwnerState(t, s, roomA, domain.OwnerState{Ver: 1, Pending: &c})
	begin(0, c, false)
	begin(1, c, false)
	end(roomA, 2, false)
	end(roomA, 1, true)
	assertOwnerState(t, s, roomA, domain.OwnerState{Ver: 1})
	repair := domain.OwnerChange{Action: domain.OwnerRepair, Successor: "bob", SuccessorVer: 1, RequestID: "owners-v2", UpdatedAt: baseTime}
	begin(1, repair, true)
	assertOwnerState(t, s, roomA, domain.OwnerState{Ver: 2, Pending: &repair})
	end(roomA, 2, true)
	end(roomA, 2, true)
	_, err := s.OwnerState(t.Context(), roomB)
	assertErrorIs(t, "OwnerState(missing room)", err, domain.ErrRoomNotFound)
	if ok, err := s.BeginOwnerChange(t.Context(), roomB, 0, c); err != nil || ok {
		t.Fatalf("BeginOwnerChange(missing room) = %v, %v; want false", ok, err)
	}
	end(roomB, 1, false)
	bad := map[string]func(*domain.OwnerChange){
		"unknown action":        func(c *domain.OwnerChange) { c.Action = "steal" },
		"leave without user":    func(c *domain.OwnerChange) { c.User = "" },
		"leave without ver":     func(c *domain.OwnerChange) { c.UserVer = 0 },
		"successor without ver": func(c *domain.OwnerChange) { c.SuccessorVer = 0 },
		"ver without successor": func(c *domain.OwnerChange) { c.Successor = "" },
		"repair with a user":    func(c *domain.OwnerChange) { c.Action = domain.OwnerRepair },
		"role change to guest":  func(c *domain.OwnerChange) { c.Action, c.Role = domain.OwnerChangeRole, "guest" },
		"bad request id":        func(c *domain.OwnerChange) { c.RequestID = "" },
	}
	for name, mutate := range bad {
		c := pendingLeave()
		mutate(&c)
		_, err := s.BeginOwnerChange(t.Context(), roomA, 2, c)
		assertErrorIs(t, "BeginOwnerChange("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, err = s.BeginOwnerChange(t.Context(), roomA, math.MaxInt64, c)
	assertErrorIs(t, "BeginOwnerChange(base max)", err, apperr.ErrInvalidArgument)
	assertOwnerState(t, s, roomA, domain.OwnerState{Ver: 2})
}

func memberCountCAS(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	added := mustAdd(t, s, joinOf(roomA, "r-1", time.Minute, 0), "carol")
	count := func(ws ...store.Witness) (int, error) { return s.CountMembers(t.Context(), roomA, ws) }
	if n, err := count(store.Witness{User: "carol", N: added[0].Ver}, store.Witness{User: "alice", N: 1}); err != nil || n != 3 {
		t.Fatalf("CountMembers = %d, %v; want 3", n, err)
	}
	for name, w := range map[string]store.Witness{"ver ahead": {User: "carol", N: 2}, "no doc": {User: "zed", N: 1}} {
		_, err := count(w)
		assertErrorIs(t, "CountMembers("+name+")", err, store.ErrStaleRead)
	}
	set := func(room, base uint64, c domain.MemberCount, want bool) {
		t.Helper()
		if ok, err := s.SetMemberCount(t.Context(), room, base, c); err != nil || ok != want {
			t.Fatalf("SetMemberCount(%d, base %d, %+v) = %v, %v; want %v", room, base, c, ok, err, want)
		}
	}
	set(roomA, 0, domain.MemberCount{Count: 3, Ver: 1}, true)
	set(roomA, 0, domain.MemberCount{Count: 4, Ver: 1}, false)
	set(roomA, 1, domain.MemberCount{Count: 2, Ver: 2}, true)
	set(roomB, 0, domain.MemberCount{Count: 1, Ver: 1}, false)
	want := room
	want.MemberCount, want.MemberCountVer = 2, 2
	assertRoom(t, s, want)
	for name, c := range map[string]domain.MemberCount{"ver not base + 1": {Count: 1, Ver: 4}, "negative count": {Count: -1, Ver: 3}} {
		_, err := s.SetMemberCount(t.Context(), roomA, 2, c)
		assertErrorIs(t, "SetMemberCount("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, err := s.SetMemberCount(t.Context(), roomA, math.MaxInt64, domain.MemberCount{Count: 1, Ver: math.MaxInt64 + 1})
	assertErrorIs(t, "SetMemberCount(ver above max int64)", err, apperr.ErrInvalidArgument)
	assertRoom(t, s, want)
}
```

`apps/core/internal/store/storetest/read_position_cases.go` (file mới):

```go
package storetest

import (
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func readPositionCases() []memberCase {
	return []memberCase{
		{"mark read only raises and mark unread only lowers the read position", readPositionMoves},
	}
}

func readPositionMoves(t *testing.T, s MemberRooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	steps := []struct {
		name    string
		move    func() (domain.ReadPosition, bool, error)
		want    domain.ReadPosition
		changed bool
	}{
		{"read 5", func() (domain.ReadPosition, bool, error) { return s.MarkRead(t.Context(), roomA, "bob", 5) }, domain.ReadPosition{Seq: 5, Ver: 1}, true},
		{"read 3", func() (domain.ReadPosition, bool, error) { return s.MarkRead(t.Context(), roomA, "bob", 3) }, domain.ReadPosition{Seq: 5, Ver: 1}, false},
		{"read 5 again", func() (domain.ReadPosition, bool, error) { return s.MarkRead(t.Context(), roomA, "bob", 5) }, domain.ReadPosition{Seq: 5, Ver: 1}, false},
		{"unread to 4", func() (domain.ReadPosition, bool, error) { return s.MarkUnread(t.Context(), roomA, "bob", 4) }, domain.ReadPosition{Seq: 4, Ver: 2}, true},
		{"unread to 9", func() (domain.ReadPosition, bool, error) { return s.MarkUnread(t.Context(), roomA, "bob", 9) }, domain.ReadPosition{Seq: 4, Ver: 2}, false},
		{"unread to 0", func() (domain.ReadPosition, bool, error) { return s.MarkUnread(t.Context(), roomA, "bob", 0) }, domain.ReadPosition{Seq: 0, Ver: 3}, true},
	}
	for _, step := range steps {
		got, changed, err := step.move()
		if err != nil || got != step.want || changed != step.changed {
			t.Fatalf("%s = %+v, %v, %v; want %+v, %v", step.name, got, changed, err, step.want, step.changed)
		}
	}
	bob := members[1]
	bob.ReadSeq, bob.ReadVer = 0, 3
	assertMember(t, s, bob)
	assertMember(t, s, members[0])
	_, _, err := s.MarkRead(t.Context(), roomA, "zed", 1)
	assertErrorIs(t, "MarkRead(non member)", err, domain.ErrNotMember)
	_, _, err = s.MarkRead(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkRead(seq above max int64)", err, apperr.ErrInvalidArgument)
	_, _, err = s.MarkUnread(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkUnread(seq above max int64)", err, apperr.ErrInvalidArgument)
	admin := bob.Next(domain.RoleAdmin, domain.MemberActive, "r-1", "alice", baseTime.Add(time.Minute))
	mustApplyMember(t, s, bob, admin, true)
	assertMember(t, s, admin)
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thay:

```go
		return pins, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithPins(pins))
	})
}
```

bằng:

```go
		return pins, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithPins(pins))
	})
}

func TestMembersContract(t *testing.T) {
	storetest.RunMembers(t, func(*testing.T) storetest.MemberRooms { return memstore.NewRooms() })
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/..."`
Expected: `FAIL ... [build failed]` ở `store`, `store/storetest` (kéo theo `memstore`, `mongostore`) và `work`:
- `store`: `apps/core/internal/store/member_validate_test.go:23:18: undefined: store.CreationMember` (rồi `store.ValidateMemberCount`, `store.ValidateReadSeq`, `store.ValidateMemberChange`, `write_contract_test.go:84:25: undefined: store.MemberWriter`…)
- `storetest`: `apps/core/internal/store/storetest/member_cases.go:15:8: undefined: store.MemberWriter` (rồi `store.MemberReader`, `store.OwnerChanges`, `store.MemberCounts`, `store.ReadPositions`, `s.AddMembers undefined`…)
- `work`: `apps/core/internal/work/record_test.go:44:37: undefined: store.MemberChanged`

**Step 3: Code store + work**

`apps/core/internal/store/member.go` (file mới):

```go
package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

const MaxMemberScan = 1000

type MemberWriter interface {
	AddMembers(ctx context.Context, j domain.Join, users []string) ([]domain.Member, error)
	ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error)
}

type MemberReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error)
	Successor(ctx context.Context, room uint64) (domain.Member, bool, error)
	MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error)
}

type OwnerChanges interface {
	OwnerState(ctx context.Context, room uint64) (domain.OwnerState, error)
	BeginOwnerChange(ctx context.Context, room, base uint64, c domain.OwnerChange) (bool, error)
	EndOwnerChange(ctx context.Context, room, ver uint64) (bool, error)
}

type MemberCounts interface {
	CountMembers(ctx context.Context, room uint64, witnesses []Witness) (int, error)
	SetMemberCount(ctx context.Context, room, base uint64, c domain.MemberCount) (bool, error)
}

type ReadPositions interface {
	MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error)
	MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error)
}

func CreationMember(r domain.Room, m domain.Member) domain.Member {
	return domain.Member{
		Room: m.Room, Tenant: m.Tenant, User: m.User, Role: m.Role, JoinedAt: m.JoinedAt,
		State: domain.MemberActive, Ver: 1, RequestID: domain.CreationRequestID(r.ID),
		UpdatedAt: firstTime(m.UpdatedAt, r.CreatedAt), UpdatedBy: firstString(m.UpdatedBy, r.CreatedBy),
	}
}

func firstTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	return a
}

func firstString(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
```

`apps/core/internal/store/member_validate.go` (file mới):

```go
package store

import (
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func ValidateJoin(j domain.Join, users []string) error {
	switch {
	case j.Room == 0:
		return invalid("room")
	case j.At.IsZero():
		return invalid("join time")
	case j.ReadSeq > math.MaxInt64:
		return invalid("read seq")
	case len(users) == 0 || len(users) > domain.MaxMemberBatch:
		return invalid("users")
	}
	if err := domain.ValidTenant(j.Tenant); err != nil {
		return err
	}
	if err := domain.ValidCID(j.RequestID); err != nil {
		return err
	}
	if err := domain.ValidUser(j.By); err != nil {
		return err
	}
	seen := make(map[string]bool, len(users))
	for _, u := range users {
		if err := domain.ValidUser(u); err != nil {
			return err
		}
		if seen[u] {
			return invalid("users")
		}
		seen[u] = true
	}
	return nil
}

func ValidateMemberChange(cur, next domain.Member) error {
	switch {
	case next.Room == 0 || next.Room != cur.Room || next.User != cur.User:
		return invalid("member")
	case cur.Ver < 1 || cur.Ver == math.MaxUint32 || next.Ver != cur.Ver+1:
		return invalid("member ver")
	case next.State != domain.MemberActive && next.State != domain.MemberRemoved:
		return invalid("member state")
	}
	if _, err := domain.ParseRole(string(next.Role)); err != nil {
		return err
	}
	if err := domain.ValidCID(next.RequestID); err != nil {
		return err
	}
	return optionalUser(next.UpdatedBy)
}

func ValidateOwnerChange(c domain.OwnerChange) error {
	switch c.Action {
	case domain.OwnerRepair:
		if c.User != "" || c.Successor == "" {
			return invalid("owner change")
		}
	case domain.OwnerLeave, domain.OwnerRemove, domain.OwnerChangeRole:
		if err := domain.ValidUser(c.User); err != nil {
			return err
		}
		if c.UserVer < 1 {
			return invalid("owner change ver")
		}
	default:
		return invalid("owner action")
	}
	if c.Action == domain.OwnerChangeRole {
		if _, err := domain.ParseRole(string(c.Role)); err != nil {
			return err
		}
	}
	if (c.Successor != "") != (c.SuccessorVer >= 1) {
		return invalid("owner successor")
	}
	if err := optionalUser(c.Successor); err != nil {
		return err
	}
	if err := domain.ValidCID(c.RequestID); err != nil {
		return err
	}
	return optionalUser(c.UpdatedBy)
}

func ValidateMemberCount(base uint64, c domain.MemberCount) error {
	if c.Count < 0 || base == math.MaxUint64 || c.Ver != base+1 || c.Ver > math.MaxInt64 {
		return invalid("member count")
	}
	return nil
}

func ValidateReadSeq(seq uint64) error {
	if seq > math.MaxInt64 {
		return invalid("read seq")
	}
	return nil
}

func optionalUser(u string) error {
	if u == "" {
		return nil
	}
	return domain.ValidUser(u)
}
```

`apps/core/internal/store/feed.go`, thay:

```go
	PinInserted
)
```

bằng:

```go
	PinInserted
	MemberChanged
)
```

thay:

```go
	Pin         domain.PinAction
	CommittedAt time.Time
```

bằng:

```go
	Pin         domain.PinAction
	Member      domain.Member
	CommittedAt time.Time
```

`apps/core/internal/work/record.go`, thay:

```go
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	}
```

bằng:

```go
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	case store.MemberChanged:
		r.Room, r.User, r.Version = c.Member.Room, c.Member.User, c.Member.Ver
	}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/ ./apps/core/internal/work/..."`
Expected: PASS (`TestEveryPortMethodHasAWriteContract` biết 13 method mới). `memstore` chưa build (`*Rooms` thiếu method của `storetest.MemberRooms`).

**Step 4: Code memstore**

`apps/core/internal/store/memstore/rooms.go`, thay:

```go
	pins    map[uint64]domain.PinState
	log     *Messages
}

func NewRooms() *Rooms {
	return &Rooms{rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member), pins: make(map[uint64]domain.PinState)}
}
```

bằng:

```go
	pins    map[uint64]domain.PinState
	owners  map[uint64]domain.OwnerState
	log     *Messages
}

func NewRooms() *Rooms {
	return &Rooms{
		rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member),
		pins: make(map[uint64]domain.PinState), owners: make(map[uint64]domain.OwnerState),
	}
}
```

thay:

```go
		if _, ok := s.members[k]; !ok {
			s.members[k] = m
		}
```

bằng:

```go
		if _, ok := s.members[k]; !ok {
			s.members[k] = store.CreationMember(r, m)
		}
```

thay:

```go
	m, ok := s.members[memberKey{room, user}]
	if !ok {
		return domain.Member{}, domain.ErrNotMember
```

bằng:

```go
	m, ok := s.members[memberKey{room, user}]
	if !ok || !m.Active() {
		return domain.Member{}, domain.ErrNotMember
```

thay:

```go
	m, ok := s.members[k]
	if !ok {
		return time.Time{}, domain.ErrNotMember
```

bằng:

```go
	m, ok := s.members[k]
	if !ok || !m.Active() {
		return time.Time{}, domain.ErrNotMember
```

`apps/core/internal/store/memstore/members.go` (file mới):

```go
package memstore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	_ store.MemberWriter = (*Rooms)(nil)
	_ store.MemberReader = (*Rooms)(nil)
)

func (s *Rooms) AddMembers(ctx context.Context, j domain.Join, users []string) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateJoin(j, users); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Member, len(users))
	for i, u := range users {
		k := memberKey{j.Room, u}
		cur := s.members[k]
		next := j.Apply(cur, u)
		if next != cur {
			s.members[k] = next
		}
		out[i] = next
	}
	return out, nil
}

func (s *Rooms) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateMemberChange(cur, next); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{cur.Room, cur.User}
	doc, ok := s.members[k]
	if !ok || doc.Ver != cur.Ver {
		return false, nil
	}
	doc.Role, doc.State, doc.PreviousRole, doc.PreviousState = next.Role, next.State, next.PreviousRole, next.PreviousState
	doc.Ver, doc.RequestID, doc.UpdatedBy, doc.UpdatedAt = next.Ver, next.RequestID, next.UpdatedBy, next.UpdatedAt
	s.members[k] = doc
	return true, nil
}

func (s *Rooms) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Member, 0, len(users))
	for _, u := range users {
		if m, ok := s.members[memberKey{room, u}]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Rooms) Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	owners, err := s.activeWhere(ctx, room, func(m domain.Member) bool { return m.Role == domain.RoleOwner })
	return owners[:min(limit, len(owners))], err
}

func (s *Rooms) Successor(ctx context.Context, room uint64) (domain.Member, bool, error) {
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		found, err := s.activeWhere(ctx, room, func(m domain.Member) bool { return m.Role == role })
		if err != nil || len(found) > 0 {
			return firstOf(found), len(found) > 0, err
		}
	}
	return domain.Member{}, false, nil
}

func (s *Rooms) MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Member
	for k, m := range s.members {
		if k.room == room && !m.UpdatedAt.Before(from) && !m.UpdatedAt.After(to) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b domain.Member) int {
		return cmp.Or(a.UpdatedAt.Compare(b.UpdatedAt), cmp.Compare(len(a.User), len(b.User)), cmp.Compare(a.User, b.User))
	})
	return out[:min(limit, len(out))], nil
}

func (s *Rooms) activeWhere(ctx context.Context, room uint64, keep func(domain.Member) bool) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.Member
	for k, m := range s.members {
		if k.room == room && m.Active() && keep(m) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b domain.Member) int {
		return cmp.Or(a.JoinedAt.Compare(b.JoinedAt), cmp.Compare(a.User, b.User))
	})
	return out, nil
}

func firstOf(ms []domain.Member) domain.Member {
	if len(ms) == 0 {
		return domain.Member{}
	}
	return ms[0]
}
```

`apps/core/internal/store/memstore/owner_state.go` (file mới):

```go
package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.OwnerChanges = (*Rooms)(nil)

func (s *Rooms) OwnerState(ctx context.Context, room uint64) (domain.OwnerState, error) {
	if err := ctx.Err(); err != nil {
		return domain.OwnerState{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.rooms[room]; !ok {
		return domain.OwnerState{}, domain.ErrRoomNotFound
	}
	return copyOwnerState(s.owners[room]), nil
}

func (s *Rooms) BeginOwnerChange(ctx context.Context, room, base uint64, c domain.OwnerChange) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateOwnerChange(c); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.owners[room]
	if _, ok := s.rooms[room]; !ok || st.Ver != base || st.Pending != nil {
		return false, nil
	}
	s.owners[room] = domain.OwnerState{Ver: base + 1, Pending: &c}
	return true, nil
}

func (s *Rooms) EndOwnerChange(ctx context.Context, room, ver uint64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.owners[room]
	if _, ok := s.rooms[room]; !ok || ver == 0 || st.Ver != ver {
		return false, nil
	}
	s.owners[room] = domain.OwnerState{Ver: ver}
	return true, nil
}

func copyOwnerState(st domain.OwnerState) domain.OwnerState {
	if st.Pending != nil {
		p := *st.Pending
		st.Pending = &p
	}
	return st
}
```

`apps/core/internal/store/memstore/member_count.go` (file mới):

```go
package memstore

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberCounts = (*Rooms)(nil)

func (s *Rooms) CountMembers(ctx context.Context, room uint64, witnesses []store.Witness) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range witnesses {
		if m, ok := s.members[memberKey{room, w.User}]; !ok || m.Ver < w.N {
			return 0, fmt.Errorf("members of room %d: %w", room, store.ErrStaleRead)
		}
	}
	n := 0
	for k, m := range s.members {
		if k.room == room && m.Active() {
			n++
		}
	}
	return n, nil
}

func (s *Rooms) SetMemberCount(ctx context.Context, room, base uint64, c domain.MemberCount) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateMemberCount(base, c); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || r.MemberCountVer != base {
		return false, nil
	}
	r.MemberCount, r.MemberCountVer = c.Count, c.Ver
	s.rooms[room] = r
	return true, nil
}
```

`apps/core/internal/store/memstore/read_position.go` (file mới):

```go
package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReadPositions = (*Rooms)(nil)

func (s *Rooms) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, seq, func(cur uint64) bool { return cur < seq })
}

func (s *Rooms) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, to, func(cur uint64) bool { return cur > to })
}

func (s *Rooms) moveRead(ctx context.Context, room uint64, user string, seq uint64, moves func(cur uint64) bool) (domain.ReadPosition, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReadPosition{}, false, err
	}
	if err := store.ValidateReadSeq(seq); err != nil {
		return domain.ReadPosition{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok || !m.Active() {
		return domain.ReadPosition{}, false, domain.ErrNotMember
	}
	if !moves(m.ReadSeq) {
		return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}, false, nil
	}
	m.ReadSeq, m.ReadVer = seq, m.ReadVer+1
	s.members[k] = m
	return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}, true, nil
}
```

**Step 5: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/..."`
Expected: PASS (mongostore integration skip). `TestMembersContract` chạy 10 case.

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo (fixture tạo room bằng member thiếu field vẫn có member active nhờ `CreationMember`). `wc -l apps/core/internal/store/memstore/members.go apps/core/internal/store/member_validate.go apps/core/internal/store/storetest/rooms_cases.go apps/core/internal/store/storetest/owner_count_cases.go` → `140`, `112`, `167`, `136`.

**Step 6: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store`, cột `purpose`: thay `Storage ports Messages/Rooms;` bằng `Storage ports Messages/Rooms (Rooms.Member: only an active member doc; Create writes creation docs through CreationMember: active, ver 1, request {room}-created); member set ports (member.go, D98-D105): MemberWriter (AddMembers version-bump = Join.Apply per user, active docs untouched, docs after the write in the order of users; ApplyMember CAS on ver, writes only the membership fields), MemberReader (MembersOf any state in the order of users; Owners active owners by joined_at then user id; Successor active admin before member, then joined_at, user id; MembersBetween any state by updated_at then _id up to MaxMemberScan), OwnerChanges (OwnerState: owners_ver + pending change, ErrRoomNotFound; BeginOwnerChange CAS on owners_ver with no pending change; EndOwnerChange clears pending while owners_ver matches), MemberCounts (CountMembers active docs, ErrStaleRead while a Witness doc ver is behind; SetMemberCount CAS on member_count_ver, ver = base + 1), ReadPositions (MarkRead only raises, MarkUnread only lowers read_seq, read_ver + 1 per move, active member only); ValidateJoin, ValidateMemberChange, ValidateOwnerChange, ValidateMemberCount, ValidateReadSeq;`.
- dòng `apps/core/internal/store`, cột `purpose`: thay `ReactionChanged with Reaction or PinInserted with Pin;` bằng `ReactionChanged with Reaction, PinInserted with Pin or MemberChanged (6) with Member;`.
- dòng `apps/core/internal/store`, cột `key_symbols`: thay `Messages;Rooms;` bằng `Messages;Rooms;MemberWriter;MemberReader;OwnerChanges;MemberCounts;ReadPositions;MaxMemberScan;CreationMember;ValidateJoin;ValidateMemberChange;ValidateOwnerChange;ValidateMemberCount;ValidateReadSeq;MemberChanged;`.
- dòng `apps/core/internal/store`, cột `decisions`: thay `D92;D97` bằng `D92;D97;D98;D99;D100;D102;D105`.
- dòng `apps/core/internal/store/memstore`, cột `purpose`: thay `Rooms.ClearHistory keeps the later time;` bằng `Rooms.ClearHistory keeps the later time (active member only); Rooms implements the member set ports (members.go, owner_state.go, member_count.go, read_position.go: Member/ClearHistory/MarkRead only for active docs, AddMembers via Join.Apply, ApplyMember CAS on ver, owners_ver + pending change per room, member count CAS beside the room, MembersBetween ties ordered like Mongo BinData ids: shorter user first);`.
- dòng `apps/core/internal/store/memstore`, cột `decisions`: thay `D52;D97` bằng `D52;D97;D98;D100;D102;D105`.
- dòng `apps/core/internal/store/storetest`, cột `purpose`: thay `room activity case: an activity with seq 0` bằng `members (RunMembers over MemberRooms: creation docs, add/re-add/no-op, ApplyMember CAS, removed member keeps the doc but loses Member/ClearHistory/MarkRead, owners and successor order, MembersBetween by update time, owner change CAS with one pending change, member count witnesses and CAS, read position moves, invalid input); room activity case: an activity with seq 0`.
- dòng `apps/core/internal/store/storetest`, cột `key_symbols`: thay `ReactableMessages;PinnableRooms` bằng `ReactableMessages;PinnableRooms;RunMembers;MemberRooms`.
- dòng `apps/core/internal/store/storetest`, cột `decisions`: thay `D92;D97` bằng `D92;D97;D98;D100;D102;D105`.
- dòng `apps/core/internal/work`, cột `purpose`: thay `PinInserted carries pv as seq;` bằng `PinInserted carries pv as seq; RecordOf maps MemberChanged to room + user + ver (the stream does not carry it yet);`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/member.go apps/core/internal/store/member_validate.go apps/core/internal/store/member_validate_test.go apps/core/internal/store/memstore/members.go apps/core/internal/store/memstore/owner_state.go apps/core/internal/store/memstore/member_count.go apps/core/internal/store/memstore/read_position.go apps/core/internal/store/storetest/member_cases.go apps/core/internal/store/storetest/member_change_cases.go apps/core/internal/store/storetest/member_order_cases.go apps/core/internal/store/storetest/owner_count_cases.go apps/core/internal/store/storetest/read_position_cases.go
git commit -m "feat(store): add the member set ports with the memstore adapter and contract" -- apps/core/internal/store/ apps/core/internal/work/record.go apps/core/internal/work/record_test.go INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `20 files changed`.

---

### Task 5: ★ mongostore — `members` clustered + codec + index, `rooms` field mới, 5 port, `Member`/`ClearHistory` lọc `state`, itest `RunMembers`

`members` thành collection clustered `_id = keys.Member(room, user)` (D98); hai index cũ (`{room_id, user_id}` unique, `{tenant, user_id, room_id}`) thay bằng `{room_id:1, state:1, role:1, joined_at:1, user_id:1}` (đếm active phủ index, owner, kế nhiệm, resync theo room) và `{tenant:1, user_id:1, state:1, room_id:1}` (chỉ chuẩn bị cho M3 `ListMyRooms`, không có port). `Bootstrap` gặp `members` cũ không clustered → `ErrNotClustered` (dev đã reset ở Task 1; Step 7 reset lại).

Mongo cài đúng ngữ nghĩa của storetest Task 4 (bảng ở "Hợp đồng chung → Mongo"):
- Codec `member_codec.go` (chuyển `memberDoc`, `encodeMember`, `decodeMember` khỏi `codec.go`): thứ tự field `_id, room_id, tenant, user_id, role, state, joined_at, ver, previous_role, previous_state, request_id, updated_at, updated_by, cleared_before_time (omitempty), read_seq, read_ver`. `state`, `ver`, `read_seq`, `read_ver` **luôn ghi** (`$lt` không khớp field thiếu). Số ghi int64; đọc qua field int64 của struct (driver nhận int32/int64/double nguyên). `ver > MaxUint32`, số âm, `state ∉ {1,2}`, `previous_state ∉ {0,1,2}` → `errCorrupt`.
- `AddMembers` = **một** `BulkWrite(ordered:false)` các `UpdateOne({_id}, pipeline, upsert)`; pipeline một `$set` (`member_join.go`): `active := {$eq: ["$state", 1]}`; mọi field ngoài `room_id/tenant/user_id/state` là `{$cond: [active, "$<field>", <mới>]}` nên doc active giữ nguyên byte (không oplog, không feed); chuỗi người dùng qua `$literal`; `ver`/`read_ver` = `$ifNull + 1` (int64), `read_seq = $max(read_seq, j.ReadSeq)`; `cleared_before_time` không đụng. Filter chỉ `_id` bằng nên server tự retry upsert trùng khoá. Sau đó `MembersOf` (primary) trả doc sau ghi.
- `ApplyMember` = `UpdateOne({_id, ver}, {$set: role, state, previous_role, previous_state, request_id, updated_by, updated_at; $inc: {ver: 1}})`. `MarkRead/MarkUnread` = `FindOneAndUpdate({_id, state: 1, read_seq: {$lt|$gt: s}}, {$set: {read_seq}, $inc: {read_ver: 1}}, After)`; không khớp → `FindOne({_id, state: 1})` (không có → `ErrNotMember`, có → vị trí hiện tại, `false`). `ClearHistory` = `FindOneAndUpdate({_id, state: 1}, {$max: {cleared_before_time}})`. `Rooms.Member` = `FindOne({_id, state: 1})`. **Ba lệnh đọc/clear không dùng pipeline, replace hay upsert.**
- `rooms` thêm `member_count_ver` (omitempty, `Rooms.Get` đọc), `owners_ver`, `pending_owner_change {action, user_id, user_ver, role, successor_id, successor_ver, request_id, updated_by, updated_at}` (chỉ `OwnerState` đọc qua projection). `Create` không ghi `owners_ver`/`member_count_ver` (CAS base 0 = thiếu, `versionIs`).
- `CountMembers` = đọc witness `{_id: $in}` + `CountDocuments({room_id, state: 1})` trong một session causal majority (`Store.witnessed`, như `Reactions.Count`); itest kiểm explain phủ index `room_id_1_state_1_role_1_joined_at_1_user_id_1`, không `FETCH`.
- Mọi chuyển `uint64 → int64` qua `toInt64` (gosec G115); room id > MaxInt64 trong ghi → `ErrInvalidArgument`.

`Rooms.Create` ghi doc qua `store.CreationMember` (Task 4): member thiếu `State/Ver/RequestID` (fixture cũ của `mutate` room 4242, `access`, `actor`, `pinproj`, `effects`, `reconcile`, `resync`) vẫn thành doc tạo active `ver 1` `{room}-created` trên cả hai adapter; case storetest `create writes active creation docs` chốt điều đó.

**Files:**
- Create: `apps/core/internal/store/mongostore/member_codec.go`, `member_join.go`, `members.go`, `member_owner.go`, `member_count.go`, `read_position.go`
- Modify: `apps/core/internal/store/mongostore/codec.go`, `mongostore.go`, `bootstrap.go`, `rooms.go`
- Create (test): `apps/core/internal/store/mongostore/member_codec_test.go`, `members_integration_test.go`, `bootstrap_members_integration_test.go`
- Modify (test): `apps/core/internal/store/mongostore/codec_test.go`, `edit_codec_test.go`, `bootstrap_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`)

**Step 1: Test**

`itRoom`, `codecTime`, `roundTrip`, `fieldNames`, `walkWinning`, `itStore`, `assertClusteredLayout`, `assertMemberIndexes` có sẵn trong package test.

`apps/core/internal/store/mongostore/codec_test.go`, thay:

```go
func TestMemberCodecRoundTrip(t *testing.T) {
	m := domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}
	back, raw := roundTrip(t, encodeMember(m, int64(m.Room)))
	if got, want := fieldNames(t, raw), []string{"room_id", "user_id", "tenant", "role", "joined_at"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeMember(back)
	if err != nil || !got.JoinedAt.Equal(m.JoinedAt) {
		t.Fatalf("decodeMember = %+v, %v; want %+v", got, err, m)
	}
	got.JoinedAt = m.JoinedAt
	if got != m {
		t.Fatalf("decoded %+v, want %+v", got, m)
	}
}

func TestRoomIDAboveMaxInt64IsRejected(t *testing.T) {
```

bằng:

```go
func TestRoomIDAboveMaxInt64IsRejected(t *testing.T) {
```

thay:

```go
	}
	if _, err := decodeMember(memberDoc{Room: -1}); err == nil {
		t.Fatal("decodeMember accepted a negative room")
	}
```

bằng:

```go
	}
	if _, err := decodeRoom(roomDoc{ID: 1, MemberCountVer: -1}); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative member count ver) = %v, want errCorrupt", err)
	}
```

`apps/core/internal/store/mongostore/edit_codec_test.go`, thay:

```go

func TestMemberCodecReadsClearedBeforeTime(t *testing.T) {
	doc := encodeMember(domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}, 7_340_000_001)
	doc.ClearedBefore = codecTime.Add(time.Hour)
	_, raw := roundTrip(t, doc)
	if got := raw.Lookup("cleared_before_time").Time(); !got.Equal(doc.ClearedBefore) {
		t.Fatalf("stored cleared_before_time = %v, want %v as a date", got, doc.ClearedBefore)
	}
	got, err := decodeMember(doc)
	if err != nil || !got.ClearedBeforeTime.Equal(codecTime.Add(time.Hour)) {
		t.Fatalf("decodeMember = %+v, %v; want cleared before %v", got, err, codecTime.Add(time.Hour))
	}
}

```

bằng:

```go

```

`apps/core/internal/store/mongostore/member_codec_test.go` (file mới):

```go
package mongostore

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var memberFields = []string{
	"_id", "room_id", "tenant", "user_id", "role", "state", "joined_at", "ver", "previous_role", "previous_state",
	"request_id", "updated_at", "updated_by", "cleared_before_time", "read_seq", "read_ver",
}

func sampleMember() domain.Member {
	return domain.Member{
		Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, State: domain.MemberActive, JoinedAt: codecTime,
		Ver: 3, PreviousRole: domain.RoleMember, PreviousState: domain.MemberActive, RequestID: "r-1", UpdatedAt: codecTime.Add(time.Minute),
		UpdatedBy: "alice", ClearedBeforeTime: codecTime.Add(time.Hour), ReadSeq: 9, ReadVer: 2,
	}
}

func TestMemberCodecRoundTrip(t *testing.T) {
	m := sampleMember()
	doc, err := encodeMember(m)
	if err != nil || !bytes.Equal(doc.ID, keys.Member(m.Room, "bob")) {
		t.Fatalf("encodeMember = %x, %v; want keys.Member", doc.ID, err)
	}
	back, raw := roundTrip(t, doc)
	if got := fieldNames(t, raw); !slices.Equal(got, memberFields) {
		t.Fatalf("fields = %v, want %v", got, memberFields)
	}
	got, err := decodeMember(back)
	if err != nil || !got.JoinedAt.Equal(m.JoinedAt) || !got.UpdatedAt.Equal(m.UpdatedAt) || !got.ClearedBeforeTime.Equal(m.ClearedBeforeTime) {
		t.Fatalf("decodeMember = %+v, %v; want %+v", got, err, m)
	}
	got.JoinedAt, got.UpdatedAt, got.ClearedBeforeTime = m.JoinedAt, m.UpdatedAt, m.ClearedBeforeTime
	if got != m {
		t.Fatalf("decoded %+v, want %+v", got, m)
	}
	fresh := m
	fresh.ClearedBeforeTime, fresh.ReadSeq, fresh.ReadVer = time.Time{}, 0, 0
	doc, _ = encodeMember(fresh)
	_, raw = roundTrip(t, doc)
	if got, want := fieldNames(t, raw), slices.DeleteFunc(slices.Clone(memberFields), func(f string) bool { return f == "cleared_before_time" }); !slices.Equal(got, want) {
		t.Fatalf("fields without a clear mark = %v, want %v with read_seq and read_ver always written", got, want)
	}
}

func TestMemberCodecRejectsOutOfRangeValues(t *testing.T) {
	m := sampleMember()
	m.Room = math.MaxInt64 + 1
	if _, err := encodeMember(m); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeMember(room above max int64) = %v, want ErrInvalidArgument", err)
	}
	for name, mutate := range map[string]func(*memberDoc){
		"negative room":       func(d *memberDoc) { d.Room = -1 },
		"negative read seq":   func(d *memberDoc) { d.ReadSeq = -1 },
		"negative read ver":   func(d *memberDoc) { d.ReadVer = -1 },
		"ver above uint32":    func(d *memberDoc) { d.Ver = math.MaxUint32 + 1 },
		"state 0":             func(d *memberDoc) { d.State = 0 },
		"state 3":             func(d *memberDoc) { d.State = 3 },
		"previous state 3":    func(d *memberDoc) { d.PreviousState = 3 },
		"negative prev state": func(d *memberDoc) { d.PreviousState = -1 },
	} {
		d, err := encodeMember(sampleMember())
		if err != nil {
			t.Fatalf("encodeMember: %v", err)
		}
		mutate(&d)
		if _, err := decodeMember(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeMember = %v, want errCorrupt", name, err)
		}
	}
}

func TestJoinPipelineKeepsActiveDocsAndTakesStringsLiterally(t *testing.T) {
	j := domain.Join{Room: 7, Tenant: "acme", RequestID: "$r", By: "$by", At: codecTime, ReadSeq: 9}
	data, err := bson.Marshal(joinMember(j, 7, 9, "$u")[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	raw := bson.Raw(data)
	set, _ := raw.Lookup("$set").Document().Elements()
	names := make([]string, len(set))
	for i, e := range set {
		names[i] = e.Key()
	}
	if want := slices.DeleteFunc(slices.Clone(memberFields), func(f string) bool { return f == "_id" || f == "cleared_before_time" }); !slices.Equal(names, want) {
		t.Fatalf("$set fields = %v, want %v", names, want)
	}
	for field, want := range map[string]string{"user_id": "$u", "request_id": "$r", "updated_by": "$by", "tenant": "acme"} {
		if got := literalOf(raw, field); got != want {
			t.Errorf("$set.%s = %s, want {$literal: %s}", field, raw.Lookup("$set", field), want)
		}
	}
	for _, field := range names {
		if field == "room_id" || field == "tenant" || field == "user_id" || field == "state" {
			continue
		}
		if got, ok := raw.Lookup("$set", field, "$cond", "1").StringValueOK(); !ok || got != "$"+field {
			t.Errorf("$set.%s = %s, want {$cond: [active, $%s, new]}", field, raw.Lookup("$set", field), field)
		}
	}
	if s, ok := raw.Lookup("$set", "state").Int32OK(); !ok || s != 1 {
		t.Fatalf("$set.state = %s, want 1 always", raw.Lookup("$set", "state"))
	}
}

func literalOf(raw bson.Raw, field string) string {
	if v, ok := raw.Lookup("$set", field, "$literal").StringValueOK(); ok {
		return v
	}
	v, _ := raw.Lookup("$set", field, "$cond", "2", "$literal").StringValueOK()
	return v
}
```

`apps/core/internal/store/mongostore/bootstrap_integration_test.go`, thay:

```go
	got := indexKeys(t, db.Collection(membersCollection))
	want := map[string]bool{"_id:1": false, "room_id:1,user_id:1": true, "tenant:1,user_id:1,room_id:1": false}
	for k, unique := range want {
		if u, ok := got[k]; !ok || u != unique {
			t.Fatalf("members indexes = %v, want %s with unique=%v", got, k, unique)
		}
	}
```

bằng:

```go
	got := indexKeys(t, db.Collection(membersCollection))
	for _, k := range []string{"room_id:1,state:1,role:1,joined_at:1,user_id:1", "tenant:1,user_id:1,state:1,room_id:1"} {
		if unique, ok := got[k]; !ok || unique {
			t.Fatalf("members indexes = %v, want non-unique %s", got, k)
		}
	}
	if _, old := got["room_id:1,user_id:1"]; old {
		t.Fatalf("members indexes = %v, want no {room_id, user_id} index on the clustered collection", got)
	}
```

`apps/core/internal/store/mongostore/bootstrap_members_integration_test.go` (file mới):

```go
package mongostore

import (
	"errors"
	"testing"
)

func TestBootstrapClustersMembers(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, membersCollection)
	assertMemberIndexes(t, db)
}

func TestBootstrapRejectsUnclusteredMembers(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), membersCollection); err != nil {
		t.Fatalf("create plain members: %v", err)
	}
	if err := Bootstrap(t.Context(), db); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap error = %v, want ErrNotClustered", err)
	}
}
```

`apps/core/internal/store/mongostore/members_integration_test.go` (file mới):

```go
package mongostore

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoMembersContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMembers(t, func(t *testing.T) storetest.MemberRooms {
		s, _ := itStore(t, client)
		return s
	})
}

func createTeam(t *testing.T, s *Store) {
	t.Helper()
	room := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
	owner := domain.Member{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}
	if err := s.Create(t.Context(), room, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func rawMember(t *testing.T, s *Store, user string) bson.Raw {
	t.Helper()
	raw, err := s.members.FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Member(itRoom, user)}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw %s: %v", user, err)
	}
	return raw
}

func TestMemberDocumentLayoutAndNoOpAdd(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	createTeam(t, s)
	j := domain.Join{Room: itRoom, Tenant: "acme", RequestID: "r-1", By: "alice", At: codecTime.Add(time.Minute), ReadSeq: 4}
	if _, err := s.AddMembers(t.Context(), j, []string{"bob"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	want := slices.DeleteFunc(slices.Clone(memberFields), func(f string) bool { return f == "cleared_before_time" })
	for _, user := range []string{"alice", "bob"} {
		raw := rawMember(t, s, user)
		if got := fieldNames(t, raw); !slices.Equal(got, want) {
			t.Fatalf("%s fields = %v, want %v", user, got, want)
		}
		for _, f := range []string{"ver", "read_seq", "read_ver", "room_id"} {
			if raw.Lookup(f).Type != bson.TypeInt64 {
				t.Fatalf("%s %s is %s, want int64", user, f, raw.Lookup(f).Type)
			}
		}
	}
	if rid := rawMember(t, s, "bob").Lookup("request_id").StringValue(); rid != "r-1" {
		t.Fatalf("request_id = %q, want r-1", rid)
	}
	before := rawMember(t, s, "bob")
	again := j
	again.RequestID, again.At, again.ReadSeq = "r-2", codecTime.Add(time.Hour), 99
	if _, err := s.AddMembers(t.Context(), again, []string{"bob"}); err != nil {
		t.Fatalf("AddMembers again: %v", err)
	}
	if after := rawMember(t, s, "bob"); !bytes.Equal(before, after) {
		t.Fatalf("adding an active member changed the doc: %s -> %s", before, after)
	}
}

func TestMemberCountIsCoveredByTheRoomStateIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	createTeam(t, s)
	j := domain.Join{Room: itRoom, Tenant: "acme", RequestID: "r-1", By: "alice", At: codecTime}
	if _, err := s.AddMembers(t.Context(), j, []string{"bob", "carol"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	count := bson.D{
		{Key: "aggregate", Value: membersCollection},
		{Key: "pipeline", Value: bson.A{
			bson.D{{Key: "$match", Value: activeOf(int64(itRoom))}},
			bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: 1}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}}},
		}},
		{Key: "cursor", Value: bson.D{}},
	}
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: count}, {Key: "verbosity", Value: "queryPlanner"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var stages, indexes []string
	walkWinning(raw, func(d bson.Raw) {
		stages = append(stages, d.Lookup("stage").StringValue())
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			indexes = append(indexes, name)
		}
	})
	if !slices.Contains(indexes, "room_id_1_state_1_role_1_joined_at_1_user_id_1") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of the room/state index", stages, indexes)
	}
	if n, err := s.CountMembers(t.Context(), itRoom, []store.Witness{{User: "carol", N: 1}}); err != nil || n != 3 {
		t.Fatalf("CountMembers = %d, %v; want 3", n, err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/"`
Expected: `FAIL ... [build failed]`; lỗi đầu: `apps/core/internal/store/mongostore/codec_test.go:173:41: unknown field MemberCountVer in struct literal of type roomDoc`, rồi `member_codec_test.go:33:14: assignment mismatch: 2 variables but encodeMember returns 1 value`, `not enough arguments in call to encodeMember`, `d.ReadSeq undefined (type *memberDoc has no field or method ReadSeq)`…

**Step 3: Code**

`apps/core/internal/store/mongostore/codec.go`, thay:

```go
type roomDoc struct {
	ID           int64           `bson:"_id"`
	Tenant       string          `bson:"tenant"`
	Type         domain.RoomType `bson:"type"`
	Name         string          `bson:"name"`
	CreatedBy    string          `bson:"created_by"`
	CreatedAt    time.Time       `bson:"created_at"`
	MemberCount  int             `bson:"member_count"`
	LastSeq      int64           `bson:"last_seq,omitempty"`
	LastMsgAt    time.Time       `bson:"last_message_at,omitempty"`
	LastChangeAt time.Time       `bson:"last_change_at,omitempty"`
}

type memberDoc struct {
	Room          int64       `bson:"room_id"`
	User          string      `bson:"user_id"`
	Tenant        string      `bson:"tenant"`
	Role          domain.Role `bson:"role"`
	JoinedAt      time.Time   `bson:"joined_at"`
	ClearedBefore time.Time   `bson:"cleared_before_time,omitempty"`
}
```

bằng:

```go
type roomDoc struct {
	ID             int64           `bson:"_id"`
	Tenant         string          `bson:"tenant"`
	Type           domain.RoomType `bson:"type"`
	Name           string          `bson:"name"`
	CreatedBy      string          `bson:"created_by"`
	CreatedAt      time.Time       `bson:"created_at"`
	MemberCount    int             `bson:"member_count"`
	LastSeq        int64           `bson:"last_seq,omitempty"`
	LastMsgAt      time.Time       `bson:"last_message_at,omitempty"`
	LastChangeAt   time.Time       `bson:"last_change_at,omitempty"`
	MemberCountVer int64           `bson:"member_count_ver,omitempty"`
}
```

thay:

```go
	}
	return domain.Room{
		ID:           id,
		Tenant:       d.Tenant,
		Type:         d.Type,
		Name:         d.Name,
		CreatedBy:    d.CreatedBy,
		CreatedAt:    d.CreatedAt,
		MemberCount:  d.MemberCount,
		LastSeq:      lastSeq,
		LastMsgAt:    d.LastMsgAt,
		LastChangeAt: d.LastChangeAt,
	}, nil
}

func encodeMember(m domain.Member, room int64) memberDoc {
	return memberDoc{Room: room, User: m.User, Tenant: m.Tenant, Role: m.Role, JoinedAt: m.JoinedAt}
}

func decodeMember(d memberDoc) (domain.Member, error) {
	room, err := toUint64("member room", d.Room)
	if err != nil {
		return domain.Member{}, err
	}
	return domain.Member{Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedBeforeTime: d.ClearedBefore}, nil
}
```

bằng:

```go
	}
	countVer, err := toUint64("room member count ver", d.MemberCountVer)
	if err != nil {
		return domain.Room{}, err
	}
	return domain.Room{
		ID:             id,
		Tenant:         d.Tenant,
		Type:           d.Type,
		Name:           d.Name,
		CreatedBy:      d.CreatedBy,
		CreatedAt:      d.CreatedAt,
		MemberCount:    d.MemberCount,
		LastSeq:        lastSeq,
		LastMsgAt:      d.LastMsgAt,
		LastChangeAt:   d.LastChangeAt,
		MemberCountVer: countVer,
	}, nil
}
```

`apps/core/internal/store/mongostore/member_codec.go` (file mới):

```go
package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type memberDoc struct {
	ID            []byte      `bson:"_id"`
	Room          int64       `bson:"room_id"`
	Tenant        string      `bson:"tenant"`
	User          string      `bson:"user_id"`
	Role          domain.Role `bson:"role"`
	State         int32       `bson:"state"`
	JoinedAt      time.Time   `bson:"joined_at"`
	Ver           int64       `bson:"ver"`
	PreviousRole  domain.Role `bson:"previous_role"`
	PreviousState int32       `bson:"previous_state"`
	RequestID     string      `bson:"request_id"`
	UpdatedAt     time.Time   `bson:"updated_at"`
	UpdatedBy     string      `bson:"updated_by"`
	ClearedBefore time.Time   `bson:"cleared_before_time,omitempty"`
	ReadSeq       int64       `bson:"read_seq"`
	ReadVer       int64       `bson:"read_ver"`
}

func encodeMember(m domain.Member) (memberDoc, error) {
	room, err := toInt64("room id", m.Room)
	if err != nil {
		return memberDoc{}, err
	}
	readSeq, seqErr := toInt64("read seq", m.ReadSeq)
	readVer, verErr := toInt64("read ver", m.ReadVer)
	if err := firstErr(seqErr, verErr); err != nil {
		return memberDoc{}, err
	}
	return memberDoc{
		ID: keys.Member(m.Room, m.User), Room: room, Tenant: m.Tenant, User: m.User, Role: m.Role, State: int32(m.State),
		JoinedAt: m.JoinedAt, Ver: int64(m.Ver), PreviousRole: m.PreviousRole, PreviousState: int32(m.PreviousState),
		RequestID: m.RequestID, UpdatedAt: m.UpdatedAt, UpdatedBy: m.UpdatedBy, ClearedBefore: m.ClearedBeforeTime,
		ReadSeq: readSeq, ReadVer: readVer,
	}, nil
}

func decodeMember(d memberDoc) (domain.Member, error) {
	room, roomErr := toUint64("member room", d.Room)
	readSeq, seqErr := toUint64("member read seq", d.ReadSeq)
	readVer, readVerErr := toUint64("member read ver", d.ReadVer)
	ver, verErr := narrowUint32("member ver", d.Ver)
	if err := firstErr(roomErr, seqErr, readVerErr, verErr); err != nil {
		return domain.Member{}, err
	}
	state, prev := domain.MemberState(d.State), domain.MemberState(d.PreviousState)
	if state != domain.MemberActive && state != domain.MemberRemoved || prev < 0 || prev > domain.MemberRemoved {
		return domain.Member{}, fmt.Errorf("%w: member state %d after %d", errCorrupt, d.State, d.PreviousState)
	}
	return domain.Member{
		Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedBeforeTime: d.ClearedBefore,
		State: state, Ver: ver, PreviousRole: d.PreviousRole, PreviousState: prev, RequestID: d.RequestID,
		UpdatedAt: d.UpdatedAt, UpdatedBy: d.UpdatedBy, ReadSeq: readSeq, ReadVer: readVer,
	}, nil
}

func decodeMembers(docs []memberDoc) ([]domain.Member, error) {
	out := make([]domain.Member, len(docs))
	for i, d := range docs {
		m, err := decodeMember(d)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}
```

`apps/core/internal/store/mongostore/member_join.go` (file mới):

```go
package mongostore

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func joinMember(j domain.Join, room, readSeq int64, user string) mongo.Pipeline {
	active := bson.D{{Key: "$eq", Value: bson.A{"$state", int32(domain.MemberActive)}}}
	keep := func(field string, next any) bson.D {
		return bson.D{{Key: "$cond", Value: bson.A{active, "$" + field, next}}}
	}
	plusOne := func(field string) bson.D {
		return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$" + field, int64(0)}}}, int64(1)}}}
	}
	nextReadSeq := bson.D{{Key: "$max", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$read_seq", int64(0)}}}, readSeq}}}
	set := bson.D{
		{Key: "room_id", Value: room},
		{Key: "tenant", Value: literal(j.Tenant)},
		{Key: "user_id", Value: literal(user)},
		{Key: "role", Value: keep("role", literal(string(domain.RoleMember)))},
		{Key: "state", Value: int32(domain.MemberActive)},
		{Key: "joined_at", Value: keep("joined_at", j.At)},
		{Key: "ver", Value: keep("ver", plusOne("ver"))},
		{Key: "previous_role", Value: keep("previous_role", bson.D{{Key: "$ifNull", Value: bson.A{"$role", ""}}})},
		{Key: "previous_state", Value: keep("previous_state", bson.D{{Key: "$ifNull", Value: bson.A{"$state", int32(0)}}})},
		{Key: "request_id", Value: keep("request_id", literal(j.RequestID))},
		{Key: "updated_at", Value: keep("updated_at", j.At)},
		{Key: "updated_by", Value: keep("updated_by", literal(j.By))},
		{Key: "read_seq", Value: keep("read_seq", nextReadSeq)},
		{Key: "read_ver", Value: keep("read_ver", plusOne("read_ver"))},
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}
```

`apps/core/internal/store/mongostore/mongostore.go`, thay:

```go
	_ store.Pins              = (*Pins)(nil)
)
```

bằng:

```go
	_ store.Pins              = (*Pins)(nil)
	_ store.MemberWriter      = (*Store)(nil)
	_ store.MemberReader      = (*Store)(nil)
	_ store.OwnerChanges      = (*Store)(nil)
	_ store.MemberCounts      = (*Store)(nil)
	_ store.ReadPositions     = (*Store)(nil)
)
```

thay:

```go
type Store struct {
	messages  *mongo.Collection
	committed *mongo.Collection
	rooms     *mongo.Collection
	members   *mongo.Collection
	edits     *mongo.Collection
```

bằng:

```go
type Store struct {
	client    *mongo.Client
	messages  *mongo.Collection
	committed *mongo.Collection
	rooms     *mongo.Collection
	members   *mongo.Collection
	witnessed *mongo.Collection
	edits     *mongo.Collection
```

thay:

```go
	return &Store{
		messages:  db.Collection(messagesCollection, local),
		committed: db.Collection(messagesCollection, majority),
		rooms:     db.Collection(roomsCollection, primary),
		members:   db.Collection(membersCollection, primary),
		edits:     db.Collection(editsCollection, primary),
```

bằng:

```go
	return &Store{
		client:    db.Client(),
		messages:  db.Collection(messagesCollection, local),
		committed: db.Collection(messagesCollection, majority),
		rooms:     db.Collection(roomsCollection, primary),
		members:   db.Collection(membersCollection, primary),
		witnessed: db.Collection(membersCollection, reacted),
		edits:     db.Collection(editsCollection, primary),
```

`apps/core/internal/store/mongostore/bootstrap.go`, thay:

```go
func Bootstrap(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{messagesCollection, editsCollection, reactionsCollection, pinActionsCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	for _, name := range []string{roomsCollection, membersCollection, reconcilerStateCollection, hiddenCollection} {
		if err := createCollection(ctx, db, name); err != nil {
```

bằng:

```go
func Bootstrap(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{messagesCollection, editsCollection, reactionsCollection, pinActionsCollection, membersCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	for _, name := range []string{roomsCollection, reconcilerStateCollection, hiddenCollection} {
		if err := createCollection(ctx, db, name); err != nil {
```

thay:

```go
func memberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "room_id", Value: 1}, {Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "room_id", Value: 1}}},
	}
}
```

bằng:

```go
func memberIndexes() []mongo.IndexModel {
	roomState := bson.D{{Key: "room_id", Value: 1}, {Key: "state", Value: 1}, {Key: "role", Value: 1}, {Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1}}
	userRooms := bson.D{{Key: "tenant", Value: 1}, {Key: "user_id", Value: 1}, {Key: "state", Value: 1}, {Key: "room_id", Value: 1}}
	return []mongo.IndexModel{{Keys: roomState}, {Keys: userRooms}}
}
```

`apps/core/internal/store/mongostore/rooms.go`, thay:

```go
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)
```

bằng:

```go
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)
```

thay:

```go
	for i, m := range members {
		docs[i] = encodeMember(m, room.ID)
	}
```

bằng:

```go
	for i, m := range members {
		if docs[i], err = encodeMember(store.CreationMember(r, m)); err != nil {
			return err
		}
	}
```

thay:

```go
func (s *Store) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.Member{}, fmt.Errorf("member %q of room %d: %w", user, room, domain.ErrNotMember)
	}
	var d memberDoc
	filter := bson.D{{Key: "room_id", Value: key}, {Key: "user_id", Value: user}}
	if err := findOne(ctx, s.members, filter, &d, domain.ErrNotMember); err != nil {
		return domain.Member{}, fmt.Errorf("member %q of room %d: %w", user, room, err)
```

bằng:

```go
func (s *Store) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	var d memberDoc
	if err := findOne(ctx, s.members, activeMember(room, user), &d, domain.ErrNotMember); err != nil {
		return domain.Member{}, fmt.Errorf("member %q of room %d: %w", user, room, err)
```

thay:

```go
func (s *Store) ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return time.Time{}, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	}
	filter := bson.D{{Key: "room_id", Value: key}, {Key: "user_id", Value: user}}
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "cleared_before_time", Value: at}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d memberDoc
	err = s.members.FindOneAndUpdate(ctx, filter, update, opts).Decode(&d)
	switch {
```

bằng:

```go
func (s *Store) ClearHistory(ctx context.Context, room uint64, user string, at time.Time) (time.Time, error) {
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "cleared_before_time", Value: at}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d memberDoc
	err := s.members.FindOneAndUpdate(ctx, activeMember(room, user), update, opts).Decode(&d)
	switch {
```

thay:

```go
	return d.ClearedBefore, nil
}
```

bằng:

```go
	return d.ClearedBefore, nil
}

func activeMember(room uint64, user string) bson.D {
	return bson.D{{Key: "_id", Value: keys.Member(room, user)}, {Key: "state", Value: int32(domain.MemberActive)}}
}
```

`apps/core/internal/store/mongostore/members.go` (file mới):

```go
package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) AddMembers(ctx context.Context, j domain.Join, users []string) ([]domain.Member, error) {
	if err := store.ValidateJoin(j, users); err != nil {
		return nil, err
	}
	room, roomErr := toInt64("room id", j.Room)
	readSeq, seqErr := toInt64("read seq", j.ReadSeq)
	if err := firstErr(roomErr, seqErr); err != nil {
		return nil, err
	}
	models := make([]mongo.WriteModel, len(users))
	for i, u := range users {
		models[i] = mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: keys.Member(j.Room, u)}}).
			SetUpdate(joinMember(j, room, readSeq, u)).
			SetUpsert(true)
	}
	if _, err := s.members.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return nil, fmt.Errorf("add %d members to room %d: %w", len(users), j.Room, err)
	}
	return s.MembersOf(ctx, j.Room, users)
}

func (s *Store) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	if err := store.ValidateMemberChange(cur, next); err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: keys.Member(cur.Room, cur.User)}, {Key: "ver", Value: int64(cur.Ver)}}
	set := bson.D{
		{Key: "role", Value: next.Role},
		{Key: "state", Value: int32(next.State)},
		{Key: "previous_role", Value: next.PreviousRole},
		{Key: "previous_state", Value: int32(next.PreviousState)},
		{Key: "request_id", Value: next.RequestID},
		{Key: "updated_by", Value: next.UpdatedBy},
		{Key: "updated_at", Value: next.UpdatedAt},
	}
	update := bson.D{{Key: "$set", Value: set}, {Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}}}}
	res, err := s.members.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("apply member %q v%d of room %d: %w", cur.User, next.Ver, cur.Room, err)
	}
	return res.MatchedCount == 1, nil
}

func (s *Store) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return nil, err
	}
	ids := make(bson.A, len(users))
	for i, u := range users {
		ids[i] = keys.Member(room, u)
	}
	found, err := s.findMembers(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, nil, 0)
	if err != nil {
		return nil, err
	}
	byUser := make(map[string]domain.Member, len(found))
	for _, m := range found {
		byUser[m.User] = m
	}
	out := make([]domain.Member, 0, len(found))
	for _, u := range users {
		if m, ok := byUser[u]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Store) Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	return s.activeWithRole(ctx, room, domain.RoleOwner, limit)
}

func (s *Store) Successor(ctx context.Context, room uint64) (domain.Member, bool, error) {
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		found, err := s.activeWithRole(ctx, room, role, 1)
		if err != nil || len(found) > 0 {
			return firstMember(found), len(found) > 0, err
		}
	}
	return domain.Member{}, false, nil
}

func (s *Store) MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: rid}, {Key: "updated_at", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return s.findMembers(ctx, filter, bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (s *Store) activeWithRole(ctx context.Context, room uint64, role domain.Role, limit int) ([]domain.Member, error) {
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "room_id", Value: rid}, {Key: "state", Value: int32(domain.MemberActive)}, {Key: "role", Value: role}}
	return s.findMembers(ctx, filter, bson.D{{Key: "joined_at", Value: 1}, {Key: "user_id", Value: 1}}, limit)
}

func (s *Store) findMembers(ctx context.Context, filter, sort bson.D, limit int) ([]domain.Member, error) {
	opts := options.Find()
	if sort != nil {
		opts.SetSort(sort).SetLimit(int64(limit))
	}
	cur, err := s.members.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find members: %w", err)
	}
	var docs []memberDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find members: %w", err)
	}
	return decodeMembers(docs)
}

func firstMember(ms []domain.Member) domain.Member {
	if len(ms) == 0 {
		return domain.Member{}
	}
	return ms[0]
}
```

`apps/core/internal/store/mongostore/member_owner.go` (file mới):

```go
package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ownerChangeDoc struct {
	Action       domain.OwnerAction `bson:"action"`
	User         string             `bson:"user_id"`
	UserVer      int64              `bson:"user_ver"`
	Role         domain.Role        `bson:"role"`
	Successor    string             `bson:"successor_id"`
	SuccessorVer int64              `bson:"successor_ver"`
	RequestID    string             `bson:"request_id"`
	UpdatedBy    string             `bson:"updated_by"`
	UpdatedAt    time.Time          `bson:"updated_at"`
}

type ownerStateDoc struct {
	Ver     int64           `bson:"owners_ver"`
	Pending *ownerChangeDoc `bson:"pending_owner_change"`
}

func (s *Store) OwnerState(ctx context.Context, room uint64) (domain.OwnerState, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.OwnerState{}, fmt.Errorf("owners of room %d: %w", room, domain.ErrRoomNotFound)
	}
	var d ownerStateDoc
	only := options.FindOne().SetProjection(bson.D{{Key: "owners_ver", Value: 1}, {Key: "pending_owner_change", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, only); err != nil {
		return domain.OwnerState{}, fmt.Errorf("owners of room %d: %w", room, err)
	}
	return decodeOwnerState(d)
}

func (s *Store) BeginOwnerChange(ctx context.Context, room, base uint64, c domain.OwnerChange) (bool, error) {
	if err := store.ValidateOwnerChange(c); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, base+1); err != nil {
		return false, err
	}
	key, keyErr := toInt64("room id", room)
	from, baseErr := toInt64("owners base ver", base)
	if err := firstErr(keyErr, baseErr); err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("owners_ver", from), {Key: "pending_owner_change", Value: nil}}
	set := bson.D{{Key: "owners_ver", Value: from + 1}, {Key: "pending_owner_change", Value: encodeOwnerChange(c)}}
	res, err := s.rooms.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return false, fmt.Errorf("begin owner change v%d of room %d: %w", base+1, room, err)
	}
	return res.MatchedCount == 1, nil
}

func (s *Store) EndOwnerChange(ctx context.Context, room, ver uint64) (bool, error) {
	key, keyErr := toInt64("room id", room)
	at, verErr := toInt64("owners ver", ver)
	if keyErr != nil || verErr != nil || ver == 0 {
		return false, ctx.Err()
	}
	filter := bson.D{{Key: "_id", Value: key}, {Key: "owners_ver", Value: at}}
	res, err := s.rooms.UpdateOne(ctx, filter, bson.D{{Key: "$unset", Value: bson.D{{Key: "pending_owner_change", Value: ""}}}})
	if err != nil {
		return false, fmt.Errorf("end owner change v%d of room %d: %w", ver, room, err)
	}
	return res.MatchedCount == 1, nil
}

func encodeOwnerChange(c domain.OwnerChange) ownerChangeDoc {
	return ownerChangeDoc{
		Action: c.Action, User: c.User, UserVer: int64(c.UserVer), Role: c.Role, Successor: c.Successor,
		SuccessorVer: int64(c.SuccessorVer), RequestID: c.RequestID, UpdatedBy: c.UpdatedBy, UpdatedAt: c.UpdatedAt,
	}
}

func decodeOwnerState(d ownerStateDoc) (domain.OwnerState, error) {
	ver, err := toUint64("owners ver", d.Ver)
	if err != nil || d.Pending == nil {
		return domain.OwnerState{Ver: ver}, err
	}
	p := d.Pending
	userVer, userErr := narrowUint32("owner change user ver", p.UserVer)
	successorVer, successorErr := narrowUint32("owner change successor ver", p.SuccessorVer)
	if err := firstErr(userErr, successorErr); err != nil {
		return domain.OwnerState{}, err
	}
	return domain.OwnerState{Ver: ver, Pending: &domain.OwnerChange{
		Action: p.Action, User: p.User, UserVer: userVer, Role: p.Role, Successor: p.Successor, SuccessorVer: successorVer,
		RequestID: p.RequestID, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt,
	}}, nil
}
```

`apps/core/internal/store/mongostore/member_count.go` (file mới):

```go
package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type memberWitnessDoc struct {
	User string `bson:"user_id"`
	Ver  int64  `bson:"ver"`
}

func (s *Store) CountMembers(ctx context.Context, room uint64, witnesses []store.Witness) (int, error) {
	rid, err := toInt64("room id", room)
	if err != nil {
		return 0, err
	}
	sess, err := s.client.StartSession(options.Session().SetCausalConsistency(true))
	if err != nil {
		return 0, fmt.Errorf("count members of room %d: start session: %w", room, err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	sctx := mongo.NewSessionContext(ctx, sess)
	if err := s.memberWitnesses(sctx, room, witnesses); err != nil {
		return 0, err
	}
	n, err := s.witnessed.CountDocuments(sctx, activeOf(rid))
	if err != nil {
		return 0, fmt.Errorf("count members of room %d: %w", room, err)
	}
	return int(n), nil
}

func activeOf(room int64) bson.D {
	return bson.D{{Key: "room_id", Value: room}, {Key: "state", Value: int32(domain.MemberActive)}}
}

func (s *Store) memberWitnesses(ctx context.Context, room uint64, witnesses []store.Witness) error {
	if len(witnesses) == 0 {
		return nil
	}
	need := make(map[string]uint32, len(witnesses))
	ids := make(bson.A, 0, len(witnesses))
	for _, w := range witnesses {
		if _, seen := need[w.User]; !seen {
			ids = append(ids, keys.Member(room, w.User))
		}
		need[w.User] = max(need[w.User], w.N)
	}
	opts := options.Find().SetProjection(bson.D{{Key: "user_id", Value: 1}, {Key: "ver", Value: 1}})
	cur, err := s.witnessed.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
	if err != nil {
		return fmt.Errorf("read member witnesses of room %d: %w", room, err)
	}
	var docs []memberWitnessDoc
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("read member witnesses of room %d: %w", room, err)
	}
	seen := 0
	for _, d := range docs {
		if n, ok := need[d.User]; ok && d.Ver >= int64(n) {
			seen++
		}
	}
	if seen < len(need) {
		return fmt.Errorf("members of room %d: %w", room, store.ErrStaleRead)
	}
	return nil
}

func (s *Store) SetMemberCount(ctx context.Context, room, base uint64, c domain.MemberCount) (bool, error) {
	if err := store.ValidateMemberCount(base, c); err != nil {
		return false, err
	}
	key, keyErr := toInt64("room id", room)
	from, baseErr := toInt64("member count base ver", base)
	ver, verErr := toInt64("member count ver", c.Ver)
	if err := firstErr(keyErr, baseErr, verErr); err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("member_count_ver", from)}
	set := bson.D{{Key: "member_count", Value: c.Count}, {Key: "member_count_ver", Value: ver}}
	res, err := s.rooms.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: set}})
	if err != nil {
		return false, fmt.Errorf("set member count v%d of room %d: %w", c.Ver, room, err)
	}
	return res.MatchedCount == 1, nil
}
```

`apps/core/internal/store/mongostore/read_position.go` (file mới):

```go
package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func (s *Store) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, seq, "$lt")
}

func (s *Store) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPosition, bool, error) {
	return s.moveRead(ctx, room, user, to, "$gt")
}

func (s *Store) moveRead(ctx context.Context, room uint64, user string, seq uint64, op string) (domain.ReadPosition, bool, error) {
	to, err := toInt64("read seq", seq)
	if err != nil {
		return domain.ReadPosition{}, false, err
	}
	filter := append(activeMember(room, user), bson.E{Key: "read_seq", Value: bson.D{{Key: op, Value: to}}})
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "read_seq", Value: to}}},
		{Key: "$inc", Value: bson.D{{Key: "read_ver", Value: int64(1)}}},
	}
	var d memberDoc
	err = s.members.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return s.readPosition(ctx, room, user)
	case err != nil:
		return domain.ReadPosition{}, false, fmt.Errorf("move read position of %q in room %d: %w", user, room, err)
	}
	m, err := decodeMember(d)
	return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}, err == nil, err
}

func (s *Store) readPosition(ctx context.Context, room uint64, user string) (domain.ReadPosition, bool, error) {
	var d memberDoc
	if err := findOne(ctx, s.members, activeMember(room, user), &d, domain.ErrNotMember); err != nil {
		return domain.ReadPosition{}, false, fmt.Errorf("read position of %q in room %d: %w", user, room, err)
	}
	m, err := decodeMember(d)
	return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}, false, err
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: PASS (integration skip).

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo. `wc -l apps/core/internal/store/mongostore/members.go apps/core/internal/store/mongostore/codec.go apps/core/internal/store/mongostore/codec_test.go apps/core/internal/store/mongostore/bootstrap_integration_test.go` → `145`, `168`, `176`, `190`.

**Step 5: Integration**

Run: `make itest`
Expected: PASS mọi package. Riêng mongostore: `TestMongoMembersContract` (10 case), `TestMongoStoreContract` và `TestMongoEditsContract` xanh lại (5 case đỏ từ Task 4), `TestMemberDocumentLayoutAndNoOpAdd`, `TestMemberCountIsCoveredByTheRoomStateIndex`, `TestBootstrapClustersMembers`, `TestBootstrapRejectsUnclusteredMembers`, `TestBootstrapIsIdempotent` (index member mới, không còn `{room_id, user_id}`).

**Step 6: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `pin_actions (index {room_id:1, created_at:1}), hidden` bằng `pin_actions (index {room_id:1, created_at:1}), members (clustered, _id = keys.Member(room, user), indexes {room_id:1, state:1, role:1, joined_at:1, user_id:1} and {tenant:1, user_id:1, state:1, room_id:1}; an old unclustered members collection fails with ErrNotClustered), hidden`.
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `members cleared_before_time (a date) via FindOneAndUpdate $max;` bằng `members cleared_before_time (a date) via FindOneAndUpdate $max on {_id, state: 1}; member set ports (member_codec.go, members.go, member_join.go, member_owner.go, member_count.go, read_position.go): Create inserts creation docs (CreationMember), Member reads {_id, state: 1}; AddMembers = one unordered BulkWrite of _id-only upserts with a $set pipeline ($cond on state == 1 keeps every field so an active doc stays byte-identical; strings as $literal; ver/read_ver $ifNull + 1, read_seq $max) then MembersOf on the primary; ApplyMember = UpdateOne {_id, ver} $set membership fields + $inc ver; MembersOf {_id: $in} in the order of users; Owners/Successor on {room_id, state: 1, role} sorted joined_at, user_id; MembersBetween {room_id, updated_at} sorted updated_at, _id; rooms.owners_ver + pending_owner_change (BeginOwnerChange CAS on owners_ver with pending null, EndOwnerChange $unset while owners_ver matches); CountMembers = witness read + covered count of {room_id, state: 1} in one causal majority session; SetMemberCount CAS on member_count_ver; MarkRead/MarkUnread = FindOneAndUpdate {_id, state: 1, read_seq $lt/$gt} $set read_seq + $inc read_ver (plain operators, never ver); state, ver, read_seq and read_ver are always written;`.
- dòng `apps/core/internal/store/mongostore`, cột `key_symbols`: thay `New;Store;` bằng `New;Store;Store.AddMembers;Store.ApplyMember;Store.MembersOf;Store.Owners;Store.Successor;Store.MembersBetween;Store.OwnerState;Store.BeginOwnerChange;Store.EndOwnerChange;Store.CountMembers;Store.SetMemberCount;Store.MarkRead;Store.MarkUnread;`.
- dòng `apps/core/internal/store/mongostore`, cột `tests`: thay `itest (explain shows bounded clustered scan)` bằng `itest (explain shows bounded clustered scan; member count covered by the room/state index; an active member add leaves the doc byte-identical)`.
- dòng `apps/core/internal/store/mongostore`, cột `decisions`: thay `D96;D97` bằng `D96;D97;D98;D99;D100;D102;D105`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/member_codec.go apps/core/internal/store/mongostore/member_join.go apps/core/internal/store/mongostore/members.go apps/core/internal/store/mongostore/member_owner.go apps/core/internal/store/mongostore/member_count.go apps/core/internal/store/mongostore/read_position.go apps/core/internal/store/mongostore/member_codec_test.go apps/core/internal/store/mongostore/members_integration_test.go apps/core/internal/store/mongostore/bootstrap_members_integration_test.go
git commit -m "feat(mongostore): store members as a clustered set with owner, count and read position writes" -- apps/core/internal/store/mongostore/ INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `17 files changed`.

**Step 7: Reset dữ liệu dev**

```bash
make infra-reset
make infra-up
make itest
```

Expected: PASS như Step 5 (database `chatim` của dev không còn `members` cũ không clustered).

---

### Task 6: ★ Feed (`$match`, `feed_member_change.go`, memstore log) + `RunMemberFeed` + `KnownKind`/`checkKind`/`ID` + registry tạm + itest feed-skip

Mọi đổi membership (đổi `ver`) vào feed và work stream (D103); đọc, đánh dấu chưa đọc, clear, đếm, `owners_ver` **không bao giờ** vào (bất biến 2).

- Mongo `$match` = `$or` của 4 nhánh: insert trên `[messages, rooms, message_edits, reactions, pin_actions, members]`; update/replace trên `reactions`; replace trên `members`; `{operationType: "update", "ns.coll": "members", "updateDescription.updatedFields.ver": {$exists: true}}`. `feed_member_change.go`: insert/replace → giải mã `fullDocument` (doc đầy đủ); update → `keys.ParseMember(documentKey._id)` + `updatedFields.ver` (int32/int64/double, 1..`MaxUint32`, user hợp lệ, không thì `errCorrupt`) → `Change{Kind: MemberChanged, Member: {Room, User, Ver}}` (update chỉ chắc có ba field này).
- memstore: `logged.member`; `Rooms` ghi log ở mọi ghi đổi `ver`: doc tạo trong `Create` (ngay sau room), user đổi trong `AddMembers`, `ApplyMember` khớp. Cursor trả `Change.Member`. Đọc/clear/`owners_ver`/`member_count` không log.
- `work`: `KnownKind` nhận `MemberChanged`; `checkKind` bắt buộc đuôi user hợp lệ cho `ReactionChanged` **và** `MemberChanged` (`hasUser`), cấm cho kind khác; `ID()` → `"g:" + pbconv.MemberEventID(room, user, ver)`. Reader forward kind mới mà không đổi code.
- `apps/core/effects_wiring.go` đăng ký tạm `store.MemberChanged: {activity.Effect()}` (record member chỉ đẩy `last_change_at/activity_bucket` vì `Seq 0`); Task 16 thay bằng registry cuối. Không đăng ký thì worker `Nak` kind 6 mãi.

Test cũ phải đổi vì room giờ kéo theo change member: storetest `feedRooms` (8 change thay vì 4), reconcile `TestForwardsARoomInsertAsARoomRecord` (đổi tên, thêm record `g:777-mb-alice-v1`) và `TestStatsTrackTermsAndForwards` (4 forwarded), mongostore feed-skip (room → member → tin), test pipeline (`TestFeedPipelineLetsOnlyReactionAndMemberChangesThrough`), `TestKnownKindsAreTheFiveChangeKinds` → `…Six…`, ca "members insert" của `TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments` đổi sang `hidden`. Feed-skip itest thêm `MarkRead`, `MarkUnread`, `AddMembers` no-op, `SetMemberCount` (cùng `ClearHistory` có sẵn).

**Files:**
- Modify: `apps/core/internal/store/memstore/change_log.go`, `feed.go`, `rooms.go`, `members.go`, `memstore_test.go`
- Modify: `apps/core/internal/store/mongostore/feed.go`, `feed_change.go`; Create: `feed_member_change.go`, `feed_member_change_test.go`
- Modify (test mongostore): `feed_change_test.go`, `feed_reaction_change_test.go`, `feed_integration_test.go`, `feed_skip_integration_test.go`
- Create: `apps/core/internal/store/storetest/feed_member_cases.go`; Modify: `apps/core/internal/store/storetest/feed_room_cases.go`
- Modify: `apps/core/internal/work/record.go`, `record_codec.go`, `record_test.go`, `record_tail_test.go`
- Modify: `apps/core/internal/reconcile/forward_test.go`, `apps/core/internal/reconcile/stats_test.go`
- Modify: `apps/core/effects_wiring.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/work`, `apps/core/internal/reconcile`, `apps/core`)

**Step 1: Test**

`reactionUpdate`, `changeOn`, `sampleMember` (Task 5), `pendingLeave`, `joinOf`, `mustAdd`, `mustApplyMember`, `assertMemberChanges` (thêm ở dưới) dùng lại.

`apps/core/internal/reconcile/forward_test.go`, thay:

```go
func TestForwardsARoomInsertAsARoomRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
```

bằng:

```go
func TestForwardsARoomInsertAndItsCreationMember(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
```

thay:

```go
		stored := rg.js.Stored()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom)}) {
			t.Fatalf("stored = %v, want only %s", got, roomRecordID(otherRoom))
		}
```

bằng:

```go
		stored := rg.js.Stored()
		owner := work.Record{Kind: store.MemberChanged, Room: otherRoom, User: "alice", Version: 1}.ID()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom), owner}) {
			t.Fatalf("stored = %v, want %s then %s", got, roomRecordID(otherRoom), owner)
		}
		if got, err := work.Decode(stored[1].Data); err != nil || got.Kind != store.MemberChanged || got.User != "alice" || got.Version != 1 {
			t.Fatalf("member record = %+v, %v; want alice v1", got, err)
		}
```

`apps/core/internal/reconcile/stats_test.go`, thay:

```go
		synctest.Wait()
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 3 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 3 forwarded, 0 dropped", s)
		}
```

bằng:

```go
		synctest.Wait()
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 4 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 4 forwarded (2 messages, a room, its member), 0 dropped", s)
		}
```

`apps/core/internal/store/memstore/memstore_test.go`, thay:

```go
	storetest.RunMembers(t, func(*testing.T) storetest.MemberRooms { return memstore.NewRooms() })
}
```

bằng:

```go
	storetest.RunMembers(t, func(*testing.T) storetest.MemberRooms { return memstore.NewRooms() })
}

func TestMemberFeedContract(t *testing.T) {
	storetest.RunMemberFeed(t, func(*testing.T) (storetest.MemberRooms, store.ChangeFeed) {
		rooms := memstore.NewRooms()
		return rooms, memstore.NewFeed(memstore.NewMessages(), rooms, nil)
	})
}
```

`apps/core/internal/store/mongostore/feed_change_test.go`, thay:

```go
	cases := map[string]changeDoc{
		"members insert":   changeOn(t, membersCollection, bson.D{{Key: "user_id", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
```

bằng:

```go
	cases := map[string]changeDoc{
		"hidden insert":    changeOn(t, hiddenCollection, bson.D{{Key: "user_id", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
```

`apps/core/internal/store/mongostore/feed_integration_test.go`, thay:

```go
		return s.Pins(), NewFeed(db)
	})
}
```

bằng:

```go
		return s.Pins(), NewFeed(db)
	})
}

func TestMongoMemberFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMemberFeed(t, func(t *testing.T) (storetest.MemberRooms, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, NewFeed(db)
	})
}
```

`apps/core/internal/store/mongostore/feed_member_change_test.go` (file mới):

```go
package mongostore

import (
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func memberUpdate(t *testing.T, id any, fields bson.D) changeDoc {
	t.Helper()
	ev := reactionUpdate(t, id, fields)
	ev.NS.Coll = membersCollection
	return ev
}

func TestDecodeChangeReadsMemberUpdatesFromTheKeyAndVer(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	want := domain.Member{Room: 7_340_000_001, User: "bob", Ver: 4}
	for name, ver := range map[string]any{"int32": int32(4), "int64": int64(4), "double": float64(4)} {
		got, err := decodeChange(memberUpdate(t, id, bson.D{{Key: "state", Value: int32(2)}, {Key: "ver", Value: ver}}))
		if err != nil || got.Kind != store.MemberChanged || got.Member != want || !got.CommittedAt.Equal(codecTime) || got.Reaction.N != 0 {
			t.Fatalf("%s: update change = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestDecodeChangeReadsMemberInsertsAndReplacesFromTheDocument(t *testing.T) {
	m := sampleMember()
	doc, err := encodeMember(m)
	if err != nil {
		t.Fatalf("encodeMember: %v", err)
	}
	for _, op := range []string{"insert", "replace"} {
		ev := changeOn(t, membersCollection, doc)
		ev.OperationType = op
		got, err := decodeChange(ev)
		if err != nil || got.Kind != store.MemberChanged || got.Member.User != "bob" || got.Member.Ver != 3 || got.Member.ReadSeq != 9 ||
			!got.Member.UpdatedAt.Equal(m.UpdatedAt) || got.Msg.Seq != 0 {
			t.Fatalf("%s change = %+v, %v; want bob v3 with the whole doc", op, got, err)
		}
	}
}

func TestDecodeChangeRejectsBrokenMemberChanges(t *testing.T) {
	id := keys.Member(7_340_000_001, "bob")
	cases := map[string]changeDoc{
		"update without ver":      memberUpdate(t, id, bson.D{{Key: "read_seq", Value: int64(3)}}),
		"update with ver 0":       memberUpdate(t, id, bson.D{{Key: "ver", Value: int32(0)}}),
		"update with ver too big": memberUpdate(t, id, bson.D{{Key: "ver", Value: int64(math.MaxUint32) + 1}}),
		"update of a short key":   memberUpdate(t, keys.Member(1, "")[:8], bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a bad user":    memberUpdate(t, keys.Member(1, "a.b"), bson.D{{Key: "ver", Value: int32(2)}}),
		"update of a string key":  memberUpdate(t, "x", bson.D{{Key: "ver", Value: int32(2)}}),
		"insert of a bad state":   changeOn(t, membersCollection, bson.D{{Key: "room_id", Value: int64(1)}, {Key: "state", Value: int32(7)}}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}
```

`apps/core/internal/store/mongostore/feed_reaction_change_test.go`, thay:

```go
func TestFeedPipelineLetsOnlyReactionUpdatesThrough(t *testing.T) {
	b, err := bson.Marshal(feedPipeline()[0])
```

bằng:

```go
func TestFeedPipelineLetsOnlyReactionAndMemberChangesThrough(t *testing.T) {
	b, err := bson.Marshal(feedPipeline()[0])
```

thay:

```go
	raw := bson.Raw(b)
	inserts, _ := raw.Lookup("$match", "$or", "0", "ns.coll", "$in").Array().Values()
	if op := raw.Lookup("$match", "$or", "0", "operationType").StringValue(); op != "insert" || len(inserts) != 5 {
		t.Fatalf("insert branch = %s, want inserts of the 5 fact collections", raw.Lookup("$match", "$or", "0"))
	}
	if coll := raw.Lookup("$match", "$or", "1", "ns.coll").StringValue(); coll != reactionsCollection {
		t.Fatalf("change branch = %s, want updates and replaces of reactions only", raw.Lookup("$match", "$or", "1"))
	}
```

bằng:

```go
	raw := bson.Raw(b)
	branch := func(i string) bson.Raw { return raw.Lookup("$match", "$or", i).Document() }
	inserts, _ := branch("0").Lookup("ns.coll", "$in").Array().Values()
	if op := branch("0").Lookup("operationType").StringValue(); op != "insert" || len(inserts) != 6 || inserts[5].StringValue() != membersCollection {
		t.Fatalf("insert branch = %s, want inserts of the 6 collections ending with members", branch("0"))
	}
	if coll := branch("1").Lookup("ns.coll").StringValue(); coll != reactionsCollection {
		t.Fatalf("change branch = %s, want updates and replaces of reactions only", branch("1"))
	}
	if op, coll := branch("2").Lookup("operationType").StringValue(), branch("2").Lookup("ns.coll").StringValue(); op != "replace" || coll != membersCollection {
		t.Fatalf("member replace branch = %s, want replaces of members", branch("2"))
	}
	ver, ok := branch("3").Lookup("updateDescription.updatedFields.ver", "$exists").BooleanOK()
	if op := branch("3").Lookup("operationType").StringValue(); op != "update" || !ok || !ver || branch("3").Lookup("ns.coll").StringValue() != membersCollection {
		t.Fatalf("member update branch = %s, want updates of members that set ver", branch("3"))
	}
	if branches, _ := raw.Lookup("$match", "$or").Array().Values(); len(branches) != 4 {
		t.Fatalf("$or = %s, want exactly 4 branches", raw.Lookup("$match", "$or"))
	}
```

`apps/core/internal/store/mongostore/feed_skip_integration_test.go`, thay:

```go
func TestFeedSkipsSummaryPinActivityEditHideAndClearWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
```

bằng:

```go
func TestFeedSkipsSummaryPinActivityEditHideClearReadAndCountWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
```

thay:

```go
	defer cancel()
	for _, want := range []store.ChangeKind{store.RoomInserted, store.MessageInserted} {
		if c, err := cur.Next(wait); err != nil || c.Kind != want {
```

bằng:

```go
	defer cancel()
	for _, want := range []store.ChangeKind{store.RoomInserted, store.MemberChanged, store.MessageInserted} {
		if c, err := cur.Next(wait); err != nil || c.Kind != want {
```

thay:

```go
	}
	if _, _, err := s.Reactions().Set(ctx, domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "👍", At: codecTime}); err != nil {
```

bằng:

```go
	}
	skipReaderWrites(t, s)
	if _, _, err := s.Reactions().Set(ctx, domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "👍", At: codecTime}); err != nil {
```

thay:

```go
	if err != nil || c.Kind != store.ReactionChanged || c.Reaction.User != "alice" || c.Reaction.N != 1 {
		t.Fatalf("next change = %+v, %v; want only the reaction after the summary, pin, activity, edit, hide and clear writes", c, err)
	}
}
```

bằng:

```go
	if err != nil || c.Kind != store.ReactionChanged || c.Reaction.User != "alice" || c.Reaction.N != 1 {
		t.Fatalf("next change = %+v, %v; want only the reaction after the summary, pin, activity, edit, hide, clear, read and count writes", c, err)
	}
}

func skipReaderWrites(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	if _, _, err := s.MarkRead(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if _, _, err := s.MarkUnread(ctx, itRoom, "alice", 0); err != nil {
		t.Fatalf("MarkUnread: %v", err)
	}
	if _, err := s.AddMembers(ctx, domain.Join{Room: itRoom, Tenant: "acme", RequestID: "r-1", By: "alice", At: codecTime}, []string{"alice"}); err != nil {
		t.Fatalf("AddMembers(active): %v", err)
	}
	if ok, err := s.SetMemberCount(ctx, itRoom, 0, domain.MemberCount{Count: 1, Ver: 1}); err != nil || !ok {
		t.Fatalf("SetMemberCount = %v, %v", ok, err)
	}
}
```

`apps/core/internal/store/storetest/feed_member_cases.go` (file mới):

```go
package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunMemberFeed(t *testing.T, open func(t *testing.T) (MemberRooms, store.ChangeFeed)) {
	t.Helper()
	t.Run("every membership change comes out once and reader writes add none", func(t *testing.T) {
		s, feed := open(t)
		cur := openCursor(t, feed)
		room, members := teamOf(roomA)
		mustCreate(t, s, room, members)
		created := nextChanges(t, cur, 3)
		if created[0].Kind != store.RoomInserted {
			t.Fatalf("first change = %+v, want the room", created[0])
		}
		assertMemberChanges(t, created[1:], members)
		added := mustAdd(t, s, joinOf(roomA, "r-1", time.Minute, 0), "carol", "alice")
		gone := added[0].Next(domain.RoleMember, domain.MemberRemoved, "r-2", "carol", baseTime.Add(2*time.Minute))
		mustApplyMember(t, s, added[0], gone, true)
		quiet(t, s)
		back := mustAdd(t, s, joinOf(roomA, "r-3", 3*time.Minute, 0), "carol")
		want := []domain.Member{added[0], gone, back[0]}
		for i, c := range nextChanges(t, cur, len(want)) {
			w := want[i]
			if c.Kind != store.MemberChanged || c.Member.Room != w.Room || c.Member.User != w.User || c.Member.Ver != w.Ver {
				t.Fatalf("change %d = %+v, want %s v%d", i, c, w.User, w.Ver)
			}
		}
	})
}

func quiet(t *testing.T, s MemberRooms) {
	t.Helper()
	ctx := t.Context()
	steps := []struct {
		name string
		run  func() error
	}{
		{"MarkRead", func() error { _, _, err := s.MarkRead(ctx, roomA, "bob", 3); return err }},
		{"MarkUnread", func() error { _, _, err := s.MarkUnread(ctx, roomA, "bob", 1); return err }},
		{"ClearHistory", func() error { _, err := s.ClearHistory(ctx, roomA, "bob", baseTime.Add(time.Hour)); return err }},
		{"AddMembers(active)", func() error {
			_, err := s.AddMembers(ctx, joinOf(roomA, "r-9", time.Hour, 0), []string{"alice"})
			return err
		}},
		{"SetMemberCount", func() error {
			_, err := s.SetMemberCount(ctx, roomA, 0, domain.MemberCount{Count: 2, Ver: 1})
			return err
		}},
		{"BeginOwnerChange", func() error { _, err := s.BeginOwnerChange(ctx, roomA, 0, pendingLeave()); return err }},
		{"EndOwnerChange", func() error { _, err := s.EndOwnerChange(ctx, roomA, 1); return err }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
}
```

`apps/core/internal/store/storetest/feed_room_cases.go`, thay:

```go
import (
	"testing"
```

bằng:

```go
import (
	"slices"
	"testing"
```

thay:

```go
	insertEach(t, msgs, msg(roomB, mainThread, 1))
	got := nextChanges(t, cur, 4)
	kinds := []store.ChangeKind{store.RoomInserted, store.MessageInserted, store.RoomInserted, store.MessageInserted}
	for i, c := range got {
```

bằng:

```go
	insertEach(t, msgs, msg(roomB, mainThread, 1))
	got := nextChanges(t, cur, 8)
	kinds := []store.ChangeKind{
		store.RoomInserted, store.MemberChanged, store.MemberChanged, store.MessageInserted,
		store.RoomInserted, store.MemberChanged, store.MemberChanged, store.MessageInserted,
	}
	for i, c := range got {
```

thay:

```go
	assertChangedRoom(t, got[0], first)
	assertChangedRoom(t, got[2], second)
	msgChanges := []store.Change{got[1], got[3]}
	assertMessages(t, messagesOf(msgChanges), []domain.Message{msg(roomA, mainThread, 1), msg(roomB, mainThread, 1)})
```

bằng:

```go
	assertChangedRoom(t, got[0], first)
	assertChangedRoom(t, got[4], second)
	assertMemberChanges(t, got[1:3], firstMembers)
	assertMemberChanges(t, got[5:7], secondMembers)
	msgChanges := []store.Change{got[3], got[7]}
	assertMessages(t, messagesOf(msgChanges), []domain.Message{msg(roomA, mainThread, 1), msg(roomB, mainThread, 1)})
```

thay:

```go
		t.Fatalf("room change = %+v at %v with message %+v, want %+v at %v and no message", got, gotAt, c.Msg, want, wantAt)
	}
}
```

bằng:

```go
		t.Fatalf("room change = %+v at %v with message %+v, want %+v at %v and no message", got, gotAt, c.Msg, want, wantAt)
	}
}

func assertMemberChanges(t *testing.T, got []store.Change, want []domain.Member) {
	t.Helper()
	for _, w := range want {
		found := slices.ContainsFunc(got, func(c store.Change) bool {
			return c.Member.Room == w.Room && c.Member.User == w.User && c.Member.Ver == w.Ver && c.Msg.Seq == 0 && c.Room.ID == 0
		})
		if !found {
			t.Fatalf("member changes = %+v, want %s v%d of room %d", got, w.User, w.Ver, w.Room)
		}
	}
}
```

`apps/core/internal/work/record_tail_test.go`, thay:

```go
	}
	pin := work.Encode(work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed})
```

bằng:

```go
	}
	member := work.Record{Kind: store.MemberChanged, Room: 42, Version: 3, User: "bob", CommittedAt: committed}
	if got, err := work.Decode(work.Encode(member)); err != nil || got != member || member.ID() != "g:42-mb-bob-v3" {
		t.Fatalf("member record = %+v, %v with id %q; want %+v and g:42-mb-bob-v3", got, err, member.ID(), member)
	}
	pin := work.Encode(work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed})
```

thay:

```go
	room := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	badUser := slices.Clone(reaction)
```

bằng:

```go
	room := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	member := work.Encode(work.Record{Kind: store.MemberChanged, Room: 42, Version: 1, User: "bob", CommittedAt: committed})
	badUser := slices.Clone(reaction)
```

thay:

```go
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
	}
```

bằng:

```go
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
		"member without a user":        slices.Clone(member[:work.RecordSize]),
	}
```

`apps/core/internal/work/record_test.go`, thay:

```go
func TestKnownKindsAreTheFiveChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(8) {
		want := k >= store.MessageInserted && k <= store.PinInserted
		if got := work.KnownKind(k); got != want {
```

bằng:

```go
func TestKnownKindsAreTheSixChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(8) {
		want := k >= store.MessageInserted && k <= store.MemberChanged
		if got := work.KnownKind(k); got != want {
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/... ./apps/core/internal/reconcile/..."`
Expected (biên dịch được, fail lúc chạy):
- `memstore`: `TestMemberFeedContract/every_membership_change_comes_out_once_and_reader_writes_add_none`: `feed_member_cases.go:18: Next after 1 changes: context deadline exceeded` (sau 10s); `TestFeedContract/room_inserts_come_out_in_commit_order_with_their_content`: `feed_room_cases.go:20: Next after 4 changes: context deadline exceeded`.
- `mongostore`: `TestFeedPipelineLetsOnlyReactionAndMemberChangesThrough` (`insert branch = … ["messages","rooms","message_edits","reactions","pin_actions"]…, want inserts of the 6 collections ending with members`), `TestDecodeChangeReadsMemberUpdatesFromTheKeyAndVer` và `TestDecodeChangeReadsMemberInsertsAndReplacesFromTheDocument` (`mongostore: corrupt document: change on collection "members"`).
- `work`: `TestKnownKindsAreTheSixChangeKinds` (`KnownKind(6) = false, want true`), `TestReactionRecordsCarryTheUserTail` (`invalid argument: work record: unknown kind 6`), `TestDecodeRejectsMalformedTails` (`member without a user: Decode = invalid argument: work record: unknown kind 6, want ErrBadRecord and not ErrUnknownKind`).
- `reconcile`: `TestForwardsARoomInsertAndItsCreationMember` (`stored = [r:777], want r:777 then `), `TestStatsTrackTermsAndForwards` (`Forwarded:3`).

**Step 3: Code memstore**

`apps/core/internal/store/memstore/change_log.go`, thay:

```go
	pin      domain.PinAction
	at       time.Time
```

bằng:

```go
	pin      domain.PinAction
	member   domain.Member
	at       time.Time
```

`apps/core/internal/store/memstore/feed.go`, thay:

```go
			return store.Change{
				Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, Reaction: l.reaction, Pin: l.pin,
				CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next)),
```

bằng:

```go
			return store.Change{
				Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, Reaction: l.reaction, Pin: l.pin, Member: l.member,
				CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next)),
```

`apps/core/internal/store/memstore/rooms.go`, thay:

```go
	s.rooms[r.ID] = r
	for _, m := range members {
		k := memberKey{m.Room, m.User}
		if _, ok := s.members[k]; !ok {
			s.members[k] = store.CreationMember(r, m)
		}
	}
	if s.log != nil {
		s.log.appendFact(logged{kind: store.RoomInserted, room: r})
	}
```

bằng:

```go
	s.rooms[r.ID] = r
	s.logRoom(r)
	for _, m := range members {
		k := memberKey{m.Room, m.User}
		if _, ok := s.members[k]; !ok {
			s.members[k] = store.CreationMember(r, m)
			s.logMember(s.members[k])
		}
	}
```

thay:

```go
	return m.ClearedBeforeTime, nil
}
```

bằng:

```go
	return m.ClearedBeforeTime, nil
}

func (s *Rooms) logRoom(r domain.Room) {
	if s.log != nil {
		s.log.appendFact(logged{kind: store.RoomInserted, room: r})
	}
}

func (s *Rooms) logMember(m domain.Member) {
	if s.log != nil {
		s.log.appendFact(logged{kind: store.MemberChanged, member: m})
	}
}
```

`apps/core/internal/store/memstore/members.go`, thay:

```go
			s.members[k] = next
		}
```

bằng:

```go
			s.members[k] = next
			s.logMember(next)
		}
```

thay:

```go
	s.members[k] = doc
	return true, nil
```

bằng:

```go
	s.members[k] = doc
	s.logMember(doc)
	return true, nil
```

**Step 4: Code Mongo**

`apps/core/internal/store/mongostore/feed.go`, thay:

```go
func feedPipeline() mongo.Pipeline {
	facts := bson.A{messagesCollection, roomsCollection, editsCollection, reactionsCollection, pinActionsCollection}
	inserts := bson.D{
```

bằng:

```go
func feedPipeline() mongo.Pipeline {
	facts := bson.A{messagesCollection, roomsCollection, editsCollection, reactionsCollection, pinActionsCollection, membersCollection}
	inserts := bson.D{
```

thay:

```go
	}
	return mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{inserts, reactionChanges}}}}}}
}
```

bằng:

```go
	}
	memberReplaces := bson.D{{Key: "operationType", Value: "replace"}, {Key: "ns.coll", Value: membersCollection}}
	memberChanges := bson.D{
		{Key: "operationType", Value: "update"},
		{Key: "ns.coll", Value: membersCollection},
		{Key: "updateDescription.updatedFields.ver", Value: bson.D{{Key: "$exists", Value: true}}},
	}
	match := bson.D{{Key: "$or", Value: bson.A{inserts, reactionChanges, memberReplaces, memberChanges}}}
	return mongo.Pipeline{{{Key: "$match", Value: match}}}
}
```

`apps/core/internal/store/mongostore/feed_change.go`, thay:

```go
		return decodePinChange(ev)
	default:
```

bằng:

```go
		return decodePinChange(ev)
	case membersCollection:
		return decodeMemberChange(ev)
	default:
```

`apps/core/internal/store/mongostore/feed_member_change.go` (file mới):

```go
package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func decodeMemberChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		m, err := memberFromUpdate(ev)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.MemberChanged, Member: m, CommittedAt: ev.WallTime}, nil
	}
	var d memberDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: member document: %w", errCorrupt, err)
	}
	m, err := decodeMember(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.MemberChanged, Member: m, CommittedAt: ev.WallTime}, nil
}

func memberFromUpdate(ev changeDoc) (domain.Member, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return domain.Member{}, fmt.Errorf("%w: member update without a binary _id", errCorrupt)
	}
	room, user, err := keys.ParseMember(id)
	if err != nil {
		return domain.Member{}, fmt.Errorf("%w: member update _id: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(user); err != nil {
		return domain.Member{}, fmt.Errorf("%w: member update user: %w", errCorrupt, err)
	}
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("ver").AsInt64OK()
	if !ok {
		return domain.Member{}, fmt.Errorf("%w: member update without a numeric ver", errCorrupt)
	}
	ver, err := narrowUint32("member ver", raw)
	if err != nil {
		return domain.Member{}, err
	}
	if ver == 0 {
		return domain.Member{}, fmt.Errorf("%w: member update with ver 0", errCorrupt)
	}
	return domain.Member{Room: room, User: user, Ver: ver}, nil
}
```

**Step 5: Code work + registry tạm**

`apps/core/internal/work/record.go`, thay:

```go
	switch k {
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted:
		return true
```

bằng:

```go
	switch k {
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted, store.MemberChanged:
		return true
```

thay:

```go
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
	default:
```

bằng:

```go
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
	case store.MemberChanged:
		return "g:" + pbconv.MemberEventID(r.Room, r.User, r.Version)
	default:
```

`apps/core/internal/work/record_codec.go`, thay:

```go
		return fmt.Errorf("%w %d", ErrUnknownKind, r.Kind)
	case r.Kind == store.ReactionChanged && domain.ValidUser(r.User) != nil:
		return fmt.Errorf("%w: reaction record without a valid user", ErrBadRecord)
	case r.Kind != store.ReactionChanged && r.User != "":
		return fmt.Errorf("%w: kind %d carries a user", ErrBadRecord, r.Kind)
	default:
		return nil
	}
}
```

bằng:

```go
		return fmt.Errorf("%w %d", ErrUnknownKind, r.Kind)
	case hasUser(r.Kind) && domain.ValidUser(r.User) != nil:
		return fmt.Errorf("%w: kind %d without a valid user", ErrBadRecord, r.Kind)
	case !hasUser(r.Kind) && r.User != "":
		return fmt.Errorf("%w: kind %d carries a user", ErrBadRecord, r.Kind)
	default:
		return nil
	}
}

func hasUser(k store.ChangeKind) bool {
	return k == store.ReactionChanged || k == store.MemberChanged
}
```

`apps/core/effects_wiring.go`, thay:

```go
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
	}
```

bằng:

```go
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
		store.MemberChanged:   {activity.Effect()},
	}
```

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/... ./apps/core/internal/reconcile/... ./apps/core/internal/effects/..."`
Expected: PASS.

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo. `wc -l apps/core/internal/store/mongostore/feed.go apps/core/internal/store/memstore/rooms.go apps/core/internal/work/record_codec.go apps/core/effects_wiring.go` → `141`, `117`, `97`, `94`.

**Step 7: Integration**

Run: `make itest`
Expected: PASS mọi package; mongostore có `TestMongoMemberFeedContract`, `TestMongoFeedContract/room_inserts_…` (room, 2 member, tin), `TestFeedSkipsSummaryPinActivityEditHideClearReadAndCountWrites`; `apps/core` xanh (record member qua `room_activity` rồi ack; không event mới).

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store/mongostore`, cột `purpose`: thay `Feed tails a database-level change stream (inserts into messages, rooms, message_edits, reactions and pin_actions plus updates and replaces of reactions only, never updateLookup; decoded by collection into MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted;` bằng `Feed tails a database-level change stream (inserts into messages, rooms, message_edits, reactions, pin_actions and members, updates and replaces of reactions, replaces of members and updates of members that set ver (read, clear and count writes never do), never updateLookup; decoded by collection into MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted/MemberChanged; a member update is read from documentKey._id (keys.ParseMember) and updatedFields.ver;`.
- dòng `apps/core/internal/store/mongostore`, cột `decisions`: thay `D102;D105` bằng `D102;D103;D105`.
- dòng `apps/core/internal/store/memstore`, cột `purpose`: thay `plus reaction changes and pin facts with the WithReactions/WithPins options (no-op writes are not logged)` bằng `plus member changes of the attached rooms (creation docs, AddMembers changes, matched ApplyMember; reads, clears, counts and owner state are not logged), reaction changes and pin facts with the WithReactions/WithPins options (no-op writes are not logged)`.
- dòng `apps/core/internal/store/memstore`, cột `decisions`: thay `D102;D105` bằng `D102;D103;D105`.
- dòng `apps/core/internal/store/storetest`, cột `purpose`: thay `pin feed (RunPinFeed: facts in commit order with their content, a refused append adds none);` bằng `pin feed (RunPinFeed: facts in commit order with their content, a refused append adds none); member feed (RunMemberFeed: creation docs after their room, add/remove/re-add once each, MarkRead/MarkUnread/ClearHistory/active add/SetMemberCount/owner change add none); room inserts are followed by their creation member changes;`.
- dòng `apps/core/internal/store/storetest`, cột `key_symbols`: thay `RunMembers;MemberRooms` bằng `RunMembers;MemberRooms;RunMemberFeed`.
- dòng `apps/core/internal/store/storetest`, cột `decisions`: thay `D102;D105` bằng `D102;D103;D105`.
- dòng `apps/core/internal/work`, cột `purpose`: thay `only for ReactionChanged (version = change number); PinInserted carries pv as seq; RecordOf maps MemberChanged to room + user + ver (the stream does not carry it yet); KnownKind lists the change kinds the stream carries (message, room, edit, reaction, pin);` bằng `only for ReactionChanged (version = change number) and MemberChanged (version = member ver); PinInserted carries pv as seq; KnownKind lists the change kinds the stream carries (message, room, edit, reaction, pin, member);`.
- dòng `apps/core/internal/work`, cột `purpose`: thay `x:{room}-{thread}-{seq}-{user}-n{change} and p:{room}-p{pv} become Nats-Msg-Id;` bằng `x:{room}-{thread}-{seq}-{user}-n{change}, p:{room}-p{pv} and g:{room}-mb-{user}-v{ver} become Nats-Msg-Id;`.
- dòng `apps/core/internal/work`, cột `decisions`: thay `D84;D91` bằng `D84;D91;D103`.
- dòng `apps/core/internal/reconcile`, cột `purpose`: thay `turns every MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted change` bằng `turns every MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted/MemberChanged change`.
- dòng `apps/core/internal/reconcile`, cột `decisions`: thay `D80;D91` bằng `D80;D91;D103`.
- dòng `apps/core`, cột `purpose`: thay `PinInserted -> room_activity, pin_projection, pin_event (reaction_pin_effects_wiring.go` bằng `PinInserted -> room_activity, pin_projection, pin_event; MemberChanged -> room_activity until the member effects of Task 16 (reaction_pin_effects_wiring.go`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/feed_member_change.go apps/core/internal/store/mongostore/feed_member_change_test.go apps/core/internal/store/storetest/feed_member_cases.go
git commit -m "feat(store): feed membership changes into the work stream" -- apps/core/internal/store/ apps/core/internal/work/ apps/core/internal/reconcile/forward_test.go apps/core/internal/reconcile/stats_test.go apps/core/effects_wiring.go INDEXES.csv
git show --stat HEAD
```

Expected: `{7}`; `23 files changed`.

---

### Task 7: `counter` tổng quát hoá (`Loop`, `MemberToucher`). **Push**

Vòng recount + CAS của reaction (D90) được tách thành `counter.Loop[V]` để `member_count` (D102, aggregate theo room) dùng chung một cơ chế: `Count` → bằng `cur` thì không ghi → `Swap(base, next)` (CAS) → khớp thì `ver + 1` → trượt thì `Reload` lấy `(cur, ver)` mới và đếm lại; hết lượt → `ErrContended` (thông điệp đổi thành `counter contended: …`, vẫn bọc `apperr.ErrUnavailable`); lỗi của bốn hàm trả nguyên; `tries < 1` → `ErrInvalidArgument`.

- `Toucher.Touch` (reaction) giữ chữ ký và hành vi, dựng lại trên `Loop[[]domain.ReactionCount]` (`Equal = slices.Equal`, nên slice rỗng và `nil` bằng nhau như cũ). Lỗi bọc thêm tiền tố `reactions of {room}/{thread}/{seq}:` (`errors.Is` không đổi). `counter_test.go` không sửa và phải xanh nguyên.
- `MemberToucher.Touch(room, cur, witnesses, tries)` = `Loop[int]`: `Count = CountMembers(room, witnesses)`, `Swap = SetMemberCount(room, base, {next, base+1})`, `Reload = Rooms.Get` (room không có → `domain.ErrRoomNotFound`). Chỉ worker `member_counter` (Task 16) gọi.

**Files:**
- Create: `apps/core/internal/counter/loop.go`, `apps/core/internal/counter/member.go`, `apps/core/internal/counter/loop_test.go`, `apps/core/internal/counter/member_test.go`
- Modify: `apps/core/internal/counter/counter.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/counter`)

**Step 1: Test**

`at` (thời điểm chung) có sẵn trong `counter_test.go`.

`apps/core/internal/counter/loop_test.go` (file mới):

```go
package counter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type cell struct {
	value, ver  int
	counts      []int
	swaps       int
	rival       bool
	countErr    error
	swapErr     error
	reloadErr   error
	reloadsMade int
}

func (c *cell) loop() counter.Loop[int] {
	return counter.Loop[int]{
		Count: func(context.Context) (int, error) {
			n := c.counts[min(c.swaps, len(c.counts)-1)]
			return n, c.countErr
		},
		Equal: func(a, b int) bool { return a == b },
		Swap: func(_ context.Context, base uint64, next int) (bool, error) {
			c.swaps++
			if c.swapErr != nil || c.rival || base != uint64(c.ver) {
				return false, c.swapErr
			}
			c.value, c.ver = next, c.ver+1
			return true, nil
		},
		Reload: func(context.Context) (int, uint64, error) {
			c.reloadsMade++
			c.rival = false
			c.ver++
			return c.value, uint64(c.ver), c.reloadErr
		},
	}
}

func TestLoopSwapsAChangedCountAndSkipsAnEqualOne(t *testing.T) {
	c := &cell{value: 2, ver: 4, counts: []int{3}}
	got, ver, moved, err := c.loop().Run(t.Context(), 2, 4, 3)
	if err != nil || !moved || got != 3 || ver != 5 || c.swaps != 1 {
		t.Fatalf("Run = %d v%d moved %v, %v after %d swaps; want 3 v5 after 1 swap", got, ver, moved, err, c.swaps)
	}
	same := &cell{value: 3, ver: 5, counts: []int{3}}
	if got, ver, moved, err := same.loop().Run(t.Context(), 3, 5, 3); err != nil || moved || got != 3 || ver != 5 || same.swaps != 0 {
		t.Fatalf("Run(equal) = %d v%d moved %v, %v after %d swaps; want no swap", got, ver, moved, err, same.swaps)
	}
}

func TestLoopReloadsAfterALostSwapAndGivesUpAfterItsTries(t *testing.T) {
	c := &cell{value: 2, ver: 4, counts: []int{3}, rival: true}
	got, ver, moved, err := c.loop().Run(t.Context(), 2, 4, 3)
	if err != nil || !moved || got != 3 || ver != 6 || c.reloadsMade != 1 {
		t.Fatalf("Run after a rival = %d v%d moved %v, %v after %d reloads; want 3 v6 after 1 reload", got, ver, moved, err, c.reloadsMade)
	}
	stuck := &cell{value: 2, ver: 4, counts: []int{3}}
	l := stuck.loop()
	l.Swap = func(context.Context, uint64, int) (bool, error) { stuck.swaps++; return false, nil }
	if _, _, moved, err := l.Run(t.Context(), 2, 4, 3); !errors.Is(err, counter.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) || moved || stuck.swaps != 3 {
		t.Fatalf("Run(always lost) = moved %v, %v after %d swaps; want ErrContended after 3", moved, err, stuck.swaps)
	}
}

func TestLoopPassesErrorsAndRejectsZeroTries(t *testing.T) {
	boom := errors.New("boom")
	for name, c := range map[string]*cell{
		"count":  {counts: []int{1}, countErr: boom},
		"swap":   {counts: []int{1}, swapErr: boom},
		"reload": {counts: []int{1}, rival: true, reloadErr: boom},
	} {
		if _, _, moved, err := c.loop().Run(t.Context(), 0, 0, 3); !errors.Is(err, boom) || moved {
			t.Errorf("%s error: Run = moved %v, %v; want %v", name, moved, err, boom)
		}
	}
	c := &cell{counts: []int{1}}
	if _, _, _, err := c.loop().Run(t.Context(), 0, 0, 0); !errors.Is(err, apperr.ErrInvalidArgument) || c.swaps != 0 {
		t.Fatalf("Run(0 tries) = %v, want ErrInvalidArgument and no swap", err)
	}
}
```

`apps/core/internal/counter/member_test.go` (file mới):

```go
package counter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberRoom(t *testing.T) (*memstore.Rooms, []domain.Member) {
	t.Helper()
	rooms := memstore.NewRooms()
	r := domain.Room{ID: 42, Tenant: "acme", Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: at, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: 42, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	added, err := rooms.AddMembers(t.Context(), domain.Join{Room: 42, Tenant: "acme", RequestID: "r-1", By: "alice", At: at}, []string{"bob", "carol"})
	if err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	return rooms, added
}

func memberToucher(t *testing.T, rooms counter.MemberRooms) *counter.MemberToucher {
	t.Helper()
	mt, err := counter.NewMembers(rooms)
	if err != nil {
		t.Fatalf("NewMembers: %v", err)
	}
	return mt
}

type countRacer struct {
	*memstore.Rooms
	raced bool
}

func (r *countRacer) SetMemberCount(ctx context.Context, room, base uint64, c domain.MemberCount) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Rooms.SetMemberCount(ctx, room, base, domain.MemberCount{Count: 9, Ver: base + 1}); err != nil {
			return false, err
		}
	}
	return r.Rooms.SetMemberCount(ctx, room, base, c)
}

func TestMemberTouchRecountsWithWitnessesAndBumpsTheVersion(t *testing.T) {
	rooms, added := memberRoom(t)
	mt := memberToucher(t, rooms)
	w := []store.Witness{{User: "carol", N: added[1].Ver}}
	got, moved, err := mt.Touch(t.Context(), 42, domain.MemberCount{Count: 1}, w, 3)
	if err != nil || !moved || got != (domain.MemberCount{Count: 3, Ver: 1}) {
		t.Fatalf("Touch = %+v, %v, %v; want 3 at v1", got, moved, err)
	}
	if again, moved, err := mt.Touch(t.Context(), 42, got, w, 3); err != nil || moved || again != got {
		t.Fatalf("Touch(equal) = %+v, %v, %v; want %+v without a write", again, moved, err, got)
	}
	r, err := rooms.Get(t.Context(), 42)
	if err != nil || r.MemberCount != 3 || r.MemberCountVer != 1 {
		t.Fatalf("room = %+v, %v; want member count 3 at v1", r, err)
	}
	if _, _, err := mt.Touch(t.Context(), 42, got, []store.Witness{{User: "carol", N: 2}}, 3); !errors.Is(err, store.ErrStaleRead) {
		t.Fatalf("Touch(stale witness) = %v, want ErrStaleRead", err)
	}
}

func TestMemberTouchRereadsTheRoomAfterALostCAS(t *testing.T) {
	rooms, _ := memberRoom(t)
	got, moved, err := memberToucher(t, &countRacer{Rooms: rooms}).Touch(t.Context(), 42, domain.MemberCount{Count: 1}, nil, 3)
	if err != nil || !moved || got != (domain.MemberCount{Count: 3, Ver: 2}) {
		t.Fatalf("Touch after a rival write = %+v, %v, %v; want 3 at v2", got, moved, err)
	}
}

func TestMemberTouchReportsAMissingRoomAndBadInput(t *testing.T) {
	rooms, _ := memberRoom(t)
	mt := memberToucher(t, rooms)
	if _, _, err := mt.Touch(t.Context(), 43, domain.MemberCount{Count: 1}, nil, 3); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Touch(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, _, err := mt.Touch(t.Context(), 42, domain.MemberCount{}, nil, 0); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Touch(0 tries) = %v, want ErrInvalidArgument", err)
	}
	if _, err := counter.NewMembers(nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewMembers(nil) = %v, want ErrInvalidArgument", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/counter/..."`
Expected: `FAIL ... [build failed]`: `apps/core/internal/counter/loop_test.go:23:31: undefined: counter.Loop`, `member_test.go:29:48: undefined: counter.MemberRooms`, `undefined: counter.MemberToucher`, `undefined: counter.NewMembers`.

**Step 3: Code**

`apps/core/internal/counter/loop.go` (file mới):

```go
package counter

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrContended = fmt.Errorf("counter contended: %w", apperr.ErrUnavailable)

	errNoTries = fmt.Errorf("%w: touch needs at least one try", apperr.ErrInvalidArgument)
)

type Loop[V any] struct {
	Count  func(ctx context.Context) (V, error)
	Equal  func(a, b V) bool
	Swap   func(ctx context.Context, base uint64, next V) (bool, error)
	Reload func(ctx context.Context) (V, uint64, error)
}

func (l Loop[V]) Run(ctx context.Context, cur V, ver uint64, tries int) (V, uint64, bool, error) {
	if tries < 1 {
		return cur, ver, false, errNoTries
	}
	for range tries {
		next, err := l.Count(ctx)
		if err != nil {
			return cur, ver, false, err
		}
		if l.Equal(next, cur) {
			return cur, ver, false, nil
		}
		ok, err := l.Swap(ctx, ver, next)
		if err != nil {
			return cur, ver, false, err
		}
		if ok {
			return next, ver + 1, true, nil
		}
		if cur, ver, err = l.Reload(ctx); err != nil {
			return cur, ver, false, err
		}
	}
	return cur, ver, false, fmt.Errorf("after %d tries: %w", tries, ErrContended)
}
```

`apps/core/internal/counter/counter.go`, thay toàn bộ nội dung bằng:

```go
package counter

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	store.ReactionSummaries
}

type Reactions interface {
	Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error)
}

var errMissingDeps = fmt.Errorf("%w: counter needs messages and reactions", apperr.ErrInvalidArgument)

type Toucher struct {
	msgs      Messages
	reactions Reactions
}

func New(msgs Messages, reactions Reactions) (*Toucher, error) {
	if msgs == nil || reactions == nil {
		return nil, errMissingDeps
	}
	return &Toucher{msgs: msgs, reactions: reactions}, nil
}

func (t *Toucher) Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	loop := Loop[[]domain.ReactionCount]{
		Count: func(ctx context.Context) ([]domain.ReactionCount, error) {
			return t.reactions.Count(ctx, key, witnesses)
		},
		Equal: slices.Equal[[]domain.ReactionCount],
		Swap: func(ctx context.Context, base uint64, next []domain.ReactionCount) (bool, error) {
			return t.msgs.SetReactions(ctx, key, base, domain.ReactionSummary{Counts: next, Version: base + 1})
		},
		Reload: func(ctx context.Context) ([]domain.ReactionCount, uint64, error) {
			s, err := t.reload(ctx, key)
			return s.Counts, s.Version, err
		},
	}
	counts, ver, moved, err := loop.Run(ctx, cur.Counts, cur.Version, tries)
	if err != nil {
		return cur, false, fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	return domain.ReactionSummary{Counts: counts, Version: ver}, moved, nil
}

func (t *Toucher) reload(ctx context.Context, key store.MsgKey) (domain.ReactionSummary, error) {
	msgs, err := t.msgs.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	if len(msgs) == 0 {
		return domain.ReactionSummary{}, fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, domain.ErrMessageNotFound)
	}
	return msgs[0].Reactions, nil
}
```

`apps/core/internal/counter/member.go` (file mới):

```go
package counter

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type MemberRooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	store.MemberCounts
}

var errMissingRooms = fmt.Errorf("%w: member counter needs rooms", apperr.ErrInvalidArgument)

type MemberToucher struct {
	rooms MemberRooms
}

func NewMembers(rooms MemberRooms) (*MemberToucher, error) {
	if rooms == nil {
		return nil, errMissingRooms
	}
	return &MemberToucher{rooms: rooms}, nil
}

func (t *MemberToucher) Touch(ctx context.Context, room uint64, cur domain.MemberCount, witnesses []store.Witness, tries int) (domain.MemberCount, bool, error) {
	loop := Loop[int]{
		Count: func(ctx context.Context) (int, error) { return t.rooms.CountMembers(ctx, room, witnesses) },
		Equal: func(a, b int) bool { return a == b },
		Swap: func(ctx context.Context, base uint64, next int) (bool, error) {
			return t.rooms.SetMemberCount(ctx, room, base, domain.MemberCount{Count: next, Ver: base + 1})
		},
		Reload: func(ctx context.Context) (int, uint64, error) {
			r, err := t.rooms.Get(ctx, room)
			return r.MemberCount, r.MemberCountVer, err
		},
	}
	n, ver, moved, err := loop.Run(ctx, cur.Count, cur.Ver, tries)
	if err != nil {
		return cur, false, fmt.Errorf("member count of room %d: %w", room, err)
	}
	return domain.MemberCount{Count: n, Ver: ver}, moved, nil
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=3 ./apps/core/internal/counter/... ./apps/core/internal/mutate/... ./apps/core/internal/effects/..."`
Expected: PASS (`counter_test.go` cũ xanh nguyên; `mutate/react_rules_test.go` vẫn thấy `counter.ErrContended`).

Run: `make fmt-check && make vet && make lint && make test`
Expected: sạch, `0 issues.`, PASS toàn repo. `wc -l apps/core/internal/counter/counter.go apps/core/internal/counter/loop.go apps/core/internal/counter/member.go apps/core/internal/counter/counter_test.go` → `66`, `47`, `47`, `189`.

**Step 5: INDEXES + commit + push**

INDEXES.csv:
- dòng `apps/core/internal/counter`, cột `purpose`: thay `Reaction summary touch shared by the mutate fast path and the effect worker (D90):` bằng `Generic recount loop Loop[V] (loop.go: Count, Equal -> no write, Swap CAS with base ver, Reload after a lost CAS; ErrContended once the tries run out; errors pass through; zero tries -> ErrInvalidArgument); reaction summary touch shared by the mutate fast path and the effect worker (D90), built on Loop:`.
- dòng `apps/core/internal/counter`, cột `purpose`: thay `up to the given tries, then ErrContended; store errors pass through` bằng `up to the given tries, then ErrContended; store errors pass through; MemberToucher (member.go, D102): Loop over CountMembers with witnesses (user, max ver) and SetMemberCount CAS on member_count_ver, Reload = Rooms.Get (missing room -> domain.ErrRoomNotFound)`.
- dòng `apps/core/internal/counter`, cột `key_symbols`: thay `Messages;Reactions;Toucher;New;Toucher.Touch;ErrContended` bằng `Messages;Reactions;Toucher;New;Toucher.Touch;ErrContended;Loop;Loop.Run;MemberRooms;MemberToucher;NewMembers;MemberToucher.Touch`.
- dòng `apps/core/internal/counter`, cột `used_by`: thay `apps/core/internal/mutate;apps/core/internal/effects;apps/core` bằng `apps/core/internal/mutate;apps/core/internal/effects;apps/core (member_counter from Task 16)`.
- dòng `apps/core/internal/counter`, cột `decisions`: thay `D67;D90` bằng `D67;D90;D102`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/counter/loop.go apps/core/internal/counter/member.go apps/core/internal/counter/loop_test.go apps/core/internal/counter/member_test.go
git commit -m "refactor(counter): share the recount loop and add the member counter" -- apps/core/internal/counter/ INDEXES.csv
git show --stat HEAD
git push origin feat/m2b
```

Expected: `{7}`; `6 files changed`; push thành công (Task 1–7 lên remote).

---

### Task 8: ★ `pkg/lru` (chuyển từ `actor`) + `dedupe.Space`/`RequestKey`/`Requests` + test Redis thật

`AddMembers` mang `request_id` (D99): một lần gửi lại cùng `request_id` phải trả kết quả cũ và **không bao giờ** áp lại, nên một lần retry trễ không thêm lại người đã bị xoá ở giữa. Task này dựng máy dedupe cho lệnh, dùng lại nguyên máy cid (CD2/CD3):
- **Khoá riêng.** `dedupe.Key` thêm field cuối `Space`. `SpaceRequest` đổi tiền tố thành `chatim:req:{room}:{user}:{request_id}`; `SpaceCID` (zero) giữ `chatim:cid:` nên mọi literal `Key{Room, User, CID}` cũ không đổi nghĩa. Giá trị Redis, Lua, TTL dùng chung (`CID_PENDING_TTL` 10s cho pending, `CID_COMMITTED_TTL` 15m cho committed). Với khoá request, `Record.Seq` = số user của lệnh (≥ 1, vì `parseCommitted` từ chối seq 0) và `CreatedAt` = giờ lệnh.
- **`Registry` exported.** Interface `registry` của batcher đổi tên thành `Registry` (cùng ba method) để `Requests` nhận cả `*Store` lẫn `*Batcher`.
- **`Requests`** (file mới `requests.go`): `Begin` hỏi LRU cục bộ trước (mutex + `lru.Cache[Key, time.Time]`, `RequestCacheSize` 4096, mục còn hiệu lực khi `time.Since(giờ lệnh) < ttl`); không có thì `Reserve([key])`:

| Verdict | `Begin` |
|---|---|
| `Reserved`, `Absent` (giá trị hỏng) | `RequestNew` |
| `Committed` | nạp LRU với `Record.CreatedAt` (giờ lệnh, nên bộ nhớ hết hạn cùng lúc với khoá Redis), `RequestDone` |
| `PendingHere`, `PendingElsewhere` | `RequestBusy` |
| lỗi Redis, `ErrDegraded`, `ErrBatcherClosed`, trả thiếu verdict | `RequestNew` (chỉ còn LRU, như CD2) |

  `Begin` chỉ trả lỗi khi `ctx` đã huỷ (khi đó không biết khoá đã được giữ hay chưa; nếu đã giữ, pending tự hết sau 10s). `Finish` = `Commit` + nạp LRU; `Cancel` = `Abort`. Qua batcher cả hai chỉ xếp hàng, không chặn.
- **`pkg/lru`.** LRU generic của `actor` (`lru_cache.go`) chuyển nguyên sang `pkg/lru` với API exported (`New`, `Get`, `Put`, `Remove`, `Len`), không khoá (người gọi tự giữ khoá). `actor` dùng `lru.Cache` cho cid cache và member cache; test LRU chuyển theo, test cid cache ở lại `actor` (đổi tên file thành `cid_cache_internal_test.go`).

Test chia ba lớp: registry giả theo kịch bản (`requests_test.go`, `synctest` cho TTL), `Store` thật trên miniredis (`requests_redis_test.go`: khoá `chatim:req:` tách khỏi `chatim:cid:`, hai core, core khởi động lại, Redis chết), và Redis thật (`requests_integration_test.go`, bỏ qua khi thiếu `CHATIM_IT_REDIS_DEDUPE_ADDR`/`CHATIM_IT_REDIS_ADDR`). Từ Task 8 tới Task 12 chưa ai gọi `Requests`.

**Files:**
- Create: `pkg/lru/lru.go`, `pkg/lru/lru_test.go`
- Delete: `apps/core/internal/actor/lru_cache.go`
- Rename: `apps/core/internal/actor/lru_cache_internal_test.go` → `cid_cache_internal_test.go` (bỏ test LRU)
- Modify: `apps/core/internal/actor/cid_dedupe.go`, `room_actor.go`, `room_state.go`
- Modify: `apps/core/internal/dedupe/records.go`, `batcher.go`, `batcher_harness_test.go`
- Create: `apps/core/internal/dedupe/requests.go`, `requests_harness_test.go`, `requests_test.go`, `requests_redis_test.go`, `requests_integration_test.go`
- Modify: `INDEXES.csv`

**Step 0: Công cụ cho Part B (một lần, không commit)**

Các Task 8–15 sửa `INDEXES.csv` bằng một script đọc JSON từ stdin: chỉ ghi lại đúng dòng được sửa (dòng khác giữ nguyên byte), tự đặt ngoặc kép cho field có dấu phẩy, in tập số cột (phải là `{7}`), và dừng với thông báo khi không thấy dòng hoặc đoạn cần thay (khi đó dừng task, báo controller: Part A có thể đã đổi dòng đó). `<scratchpad>` là thư mục scratchpad của phiên thực thi. Nếu Part A đã tạo `<scratchpad>/indexes_edit.py` với cùng nội dung thì dùng lại; không thì tạo:

```python
import csv, io, json, sys

COLS = {"kind": 1, "purpose": 2, "key_symbols": 3, "used_by": 4, "tests": 5, "decisions": 6}
ops = json.load(sys.stdin)
lines = open("INDEXES.csv", newline="").read().split("\n")


def find(path):
    hits = [i for i, l in enumerate(lines) if l and next(csv.reader([l]))[0] == path]
    return hits[0] if hits else None


def dump(row):
    buf = io.StringIO()
    csv.writer(buf, lineterminator="").writerow(row)
    return buf.getvalue()


for op in ops:
    i = find(op["path"])
    if "row" in op:
        if i is not None:
            sys.exit("row exists: " + op["path"])
        j = find(op["after"])
        if j is None:
            sys.exit("no row to insert after: " + op["after"])
        lines.insert(j + 1, dump([op["path"]] + op["row"]))
        continue
    if i is None:
        sys.exit("no row: " + op["path"])
    row = next(csv.reader([lines[i]]))
    c = COLS[op["col"]]
    if "replace" in op:
        old, new = op["replace"]
        if old not in row[c]:
            sys.exit("text not found in %s %s: %s" % (op["path"], op["col"], old))
        row[c] = row[c].replace(old, new, 1)
    else:
        row[c] += op["append"]
    lines[i] = dump(row)

open("INDEXES.csv", "w", newline="").write("\n".join(lines))
print({len(r) for r in csv.reader(open("INDEXES.csv", newline="")) if r})
```

Cách dùng: `python3 <scratchpad>/indexes_edit.py <<'EOF'` + mảng JSON + `EOF`, chạy từ gốc repo. Thao tác: `{"path", "col", "append"}` nối vào cuối cột; `{"path", "col", "replace": [cũ, mới]}` thay lần đầu; `{"path", "after", "row": [6 cột còn lại]}` chèn dòng mới.

Makefile chạy riêng một itest (quy tắc chung), tạo nếu chưa có `<scratchpad>/itest-one.mk`:

```make
include Makefile
itest-one: check-env
	$(GO_RUN) --network $(NETWORK) -e CHATIM_IT_MONGO_URI -e CHATIM_IT_REDIS_ADDR=chatim-redis:6379 -e CHATIM_IT_REDIS_PASSWORD -e CHATIM_IT_REDIS_DEDUPE_ADDR=chatim-redis-dedupe:6379 -e CHATIM_IT_REDIS_DEDUPE_PASSWORD -e CHATIM_IT_NATS_URL=nats://chatim-nats:4222 $(GO_IMAGE) go test -race -count=1 -run '$(RUN)' $(PKG)
```

Kiểm nhanh:

```bash
echo '[]' | python3 <scratchpad>/indexes_edit.py && git diff --stat INDEXES.csv
```

Expected: in `{7}`; `git diff --stat` rỗng.

**Step 1: Test**

`pkg/lru/lru_test.go`:

```go
package lru_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/pkg/lru"
)

func TestCacheEvictsTheLeastRecentlyUsed(t *testing.T) {
	c := lru.New[string, int](2)
	c.Put("a", 1)
	c.Put("b", 2)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d, %v", v, ok)
	}
	c.Put("c", 3)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b survived although it was least recently used")
	}
	c.Put("a", 10)
	if v, _ := c.Get("a"); v != 10 || c.Len() != 2 {
		t.Fatalf("after update: a = %d, len = %d", v, c.Len())
	}
}

func TestRemoveForgetsOneKey(t *testing.T) {
	c := lru.New[int, string](4)
	for i := range 3 {
		c.Put(i, "v")
	}
	c.Remove(1)
	c.Remove(9)
	if _, ok := c.Get(1); ok || c.Len() != 2 {
		t.Fatalf("after Remove(1): len %d, 1 still cached %v", c.Len(), ok)
	}
	for _, k := range []int{0, 2} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("Remove(1) dropped key %d", k)
		}
	}
}
```

`apps/core/internal/dedupe/batcher_harness_test.go`: trong chữ ký `func startBatcher(t *testing.T, reg registry, cfg BatchConfig) (*Batcher, *testlog.Sink)` thay `reg registry` bằng `reg Registry`.

`apps/core/internal/dedupe/requests_harness_test.go`:

```go
package dedupe

import (
	"context"
	"sync"
	"testing"
	"time"
)

type scriptedRegistry struct {
	mu       sync.Mutex
	verdict  Verdict
	err      error
	reserves int
	commits  []Entry
	aborts   []Key
}

func (s *scriptedRegistry) answer(v Verdict, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdict, s.err = v, err
}

func (s *scriptedRegistry) Reserve(ctx context.Context, keys []Key) ([]Verdict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserves++
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]Verdict, len(keys))
	for i := range out {
		out[i] = s.verdict
	}
	return out, nil
}

func (s *scriptedRegistry) Commit(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits = append(s.commits, entries...)
	return nil
}

func (s *scriptedRegistry) Abort(_ context.Context, keys []Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborts = append(s.aborts, keys...)
	return nil
}

func (s *scriptedRegistry) reserveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserves
}

func newRequests(t *testing.T, reg Registry, ttl time.Duration) *Requests {
	t.Helper()
	r, err := NewRequests(reg, ttl)
	if err != nil {
		t.Fatalf("NewRequests: %v", err)
	}
	return r
}

func begin(t *testing.T, r *Requests, k Key) RequestStatus {
	t.Helper()
	st, err := r.Begin(t.Context(), k)
	if err != nil {
		t.Fatalf("Begin(%s): %v", k, err)
	}
	return st
}
```

`apps/core/internal/dedupe/requests_test.go`:

```go
package dedupe

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestRequestKeysLiveInTheirOwnNamespace(t *testing.T) {
	req := RequestKey(42, "alice", "r-1")
	cid := Key{Room: 42, User: "alice", CID: "r-1"}
	if got := req.String(); got != "chatim:req:42:alice:r-1" {
		t.Fatalf("request key = %q", got)
	}
	if got := cid.String(); got != "chatim:cid:42:alice:r-1" {
		t.Fatalf("cid key = %q", got)
	}
	if req == cid || req.Space != SpaceRequest || cid.Space != SpaceCID {
		t.Fatalf("request key %+v and cid key %+v must differ by space", req, cid)
	}
}

func TestNewRequestsNeedsARegistryAndAPositiveTTL(t *testing.T) {
	for name, call := range map[string]func() (*Requests, error){
		"no registry": func() (*Requests, error) { return NewRequests(nil, time.Minute) },
		"zero ttl":    func() (*Requests, error) { return NewRequests(&scriptedRegistry{}, 0) },
	} {
		if _, err := call(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewRequests = %v, want ErrInvalidArgument", name, err)
		}
	}
	if RequestCacheSize != 4096 || RequestNew != 1 || RequestDone != 2 || RequestBusy != 3 {
		t.Fatal("request dedupe constants changed")
	}
}

func TestBeginMapsTheRegistryVerdict(t *testing.T) {
	cases := []struct {
		name    string
		verdict Verdict
		err     error
		want    RequestStatus
	}{
		{"reserved", Verdict{Status: Reserved}, nil, RequestNew},
		{"malformed value", Verdict{Status: Absent}, nil, RequestNew},
		{"pending here", Verdict{Status: PendingHere}, nil, RequestBusy},
		{"pending elsewhere", Verdict{Status: PendingElsewhere}, nil, RequestBusy},
		{"committed", Verdict{Status: Committed, Record: Record{Seq: 2, CreatedAt: time.Now()}}, nil, RequestDone},
		{"redis down", Verdict{}, errors.New("connection refused"), RequestNew},
		{"cooling down", Verdict{}, ErrDegraded, RequestNew},
		{"batcher closed", Verdict{}, ErrBatcherClosed, RequestNew},
	}
	for _, c := range cases {
		reg := &scriptedRegistry{}
		reg.answer(c.verdict, c.err)
		if got := begin(t, newRequests(t, reg, time.Minute), RequestKey(42, "alice", "r-1")); got != c.want {
			t.Fatalf("%s: Begin = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFinishAnswersFromMemoryUntilTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &scriptedRegistry{}
		reg.answer(Verdict{Status: Reserved}, nil)
		r := newRequests(t, reg, time.Minute)
		k := RequestKey(42, "alice", "r-1")
		if got := begin(t, r, k); got != RequestNew {
			t.Fatalf("first Begin = %v, want RequestNew", got)
		}
		rec := Record{Seq: 3, CreatedAt: time.Now()}
		r.Finish(t.Context(), k, rec)
		if !slices.Equal(reg.commits, []Entry{{Key: k, Record: rec}}) {
			t.Fatalf("commits = %+v, want the request with its record", reg.commits)
		}
		time.Sleep(time.Minute - time.Millisecond)
		if got := begin(t, r, k); got != RequestDone || reg.reserveCount() != 1 {
			t.Fatalf("Begin inside the ttl = %v after %d reserves, want RequestDone from memory", got, reg.reserveCount())
		}
		time.Sleep(time.Millisecond)
		if got := begin(t, r, k); got != RequestNew || reg.reserveCount() != 2 {
			t.Fatalf("Begin past the ttl = %v after %d reserves, want the registry asked again", got, reg.reserveCount())
		}
		if other := begin(t, r, RequestKey(42, "alice", "r-2")); other != RequestNew {
			t.Fatalf("another request id = %v, want RequestNew", other)
		}
	})
}

func TestACommittedVerdictIsRememberedUntilItsCommandExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg := &scriptedRegistry{}
		reg.answer(Verdict{Status: Committed, Record: Record{Seq: 1, CreatedAt: time.Now().Add(-50 * time.Second)}}, nil)
		r := newRequests(t, reg, time.Minute)
		k := RequestKey(7, "bob", "r-9")
		for range 2 {
			if got := begin(t, r, k); got != RequestDone {
				t.Fatalf("Begin = %v, want RequestDone", got)
			}
		}
		if n := reg.reserveCount(); n != 1 {
			t.Fatalf("reserves = %d, want 1 (the second answer came from memory)", n)
		}
		time.Sleep(10 * time.Second)
		begin(t, r, k)
		if n := reg.reserveCount(); n != 2 {
			t.Fatalf("reserves = %d, want 2 (memory follows the command time, not the read time)", n)
		}
	})
}

func TestCancelAbortsTheReservation(t *testing.T) {
	reg := &scriptedRegistry{}
	reg.answer(Verdict{Status: Reserved}, nil)
	r := newRequests(t, reg, time.Minute)
	k := RequestKey(42, "alice", "r-1")
	begin(t, r, k)
	r.Cancel(t.Context(), k)
	if !slices.Equal(reg.aborts, []Key{k}) || len(reg.commits) != 0 {
		t.Fatalf("aborts %v, commits %v; want only the request aborted", reg.aborts, reg.commits)
	}
	if got := begin(t, r, k); got != RequestNew {
		t.Fatalf("Begin after Cancel = %v, want RequestNew", got)
	}
}

func TestBeginFailsOnlyWhenTheCallerGaveUp(t *testing.T) {
	reg := &scriptedRegistry{}
	reg.answer(Verdict{Status: Reserved}, nil)
	r := newRequests(t, reg, time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if st, err := r.Begin(ctx, RequestKey(42, "alice", "r-1")); !errors.Is(err, context.Canceled) || st != 0 {
		t.Fatalf("Begin with a cancelled context = %v, %v; want context.Canceled", st, err)
	}
}
```

`apps/core/internal/dedupe/requests_redis_test.go`:

```go
package dedupe

import (
	"testing"
	"time"
)

func TestRequestsOnRedisKeepTheirOwnKeys(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newRequests(t, newStore(t, rdb, "core-a", nil), DefaultCommittedTTL)
	b := newRequests(t, newStore(t, rdb, "core-b", nil), DefaultCommittedTTL)
	k := RequestKey(42, "alice", "r-1")
	cid := Key{Room: 42, User: "alice", CID: "r-1"}

	if got := begin(t, a, k); got != RequestNew {
		t.Fatalf("first Begin = %v, want RequestNew", got)
	}
	expectValue(t, mr, k, "p:core-a")
	if mr.Exists(cid.String()) {
		t.Fatalf("a request reserved the cid key %s", cid)
	}
	for name, r := range map[string]*Requests{"same core": a, "other core": b} {
		if got := begin(t, r, k); got != RequestBusy {
			t.Fatalf("%s while pending: Begin = %v, want RequestBusy", name, got)
		}
	}
	rec := Record{Seq: 2, CreatedAt: time.UnixMilli(1_700_000_000_000).UTC()}
	a.Finish(t.Context(), k, rec)
	expectValue(t, mr, k, committedValue(rec))
	if ttl := mr.TTL(k.String()); ttl != DefaultCommittedTTL {
		t.Fatalf("committed request ttl = %v, want %v", ttl, DefaultCommittedTTL)
	}
	restarted := newRequests(t, newStore(t, rdb, "core-a", nil), DefaultCommittedTTL)
	for name, r := range map[string]*Requests{"other core": b, "restarted core": restarted} {
		if got := begin(t, r, k); got != RequestDone {
			t.Fatalf("%s after commit: Begin = %v, want RequestDone", name, got)
		}
	}

	cancelled := RequestKey(42, "alice", "r-2")
	if got := begin(t, a, cancelled); got != RequestNew {
		t.Fatalf("Begin(r-2) = %v, want RequestNew", got)
	}
	a.Cancel(t.Context(), cancelled)
	if mr.Exists(cancelled.String()) {
		t.Fatal("Cancel left the pending request key")
	}
	if got := begin(t, b, cancelled); got != RequestNew {
		t.Fatalf("Begin after Cancel = %v, want RequestNew", got)
	}
}

func TestRequestsFallBackToMemoryWhileRedisIsDown(t *testing.T) {
	mr, rdb := newRedis(t)
	r := newRequests(t, newStore(t, rdb, "core-a", nil), DefaultCommittedTTL)
	k := RequestKey(42, "alice", "r-1")
	mr.Close()
	if got := begin(t, r, k); got != RequestNew {
		t.Fatalf("Begin with redis down = %v, want RequestNew", got)
	}
	r.Finish(t.Context(), k, Record{Seq: 1, CreatedAt: time.Now()})
	if got := begin(t, r, k); got != RequestDone {
		t.Fatalf("Begin after a local finish = %v, want RequestDone from memory", got)
	}
}

var (
	_ Registry = (*Store)(nil)
	_ Registry = (*Batcher)(nil)
)
```

`apps/core/internal/dedupe/requests_integration_test.go`:

```go
package dedupe

import (
	"context"
	"testing"
	"time"
)

func TestRealRedisRequestLifecycle(t *testing.T) {
	rdb := itClient(t)
	ctx := t.Context()
	keys := itKeys(t, rdb, "r-1")
	k := RequestKey(keys[0].Room, keys[0].User, keys[0].CID)
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rdb.Del(cctx, k.String()).Err()
	})
	a := newRequests(t, itStore(t, rdb, "it-core-a", DefaultPendingTTL), DefaultCommittedTTL)
	b := newRequests(t, itStore(t, rdb, "it-core-b", DefaultPendingTTL), DefaultCommittedTTL)

	if got := begin(t, a, k); got != RequestNew {
		t.Fatalf("first Begin = %v, want RequestNew", got)
	}
	if v := rdb.Get(ctx, k.String()).Val(); v != "p:it-core-a" {
		t.Fatalf("pending request value = %q", v)
	}
	if n := rdb.Exists(ctx, keys[0].String()).Val(); n != 0 {
		t.Fatal("a request reserved the cid namespace")
	}
	if got := begin(t, b, k); got != RequestBusy {
		t.Fatalf("other core while pending = %v, want RequestBusy", got)
	}
	a.Finish(ctx, k, Record{Seq: 3, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)})
	if got := begin(t, b, k); got != RequestDone {
		t.Fatalf("other core after commit = %v, want RequestDone", got)
	}
	if ttl := rdb.PTTL(ctx, k.String()).Val(); ttl <= DefaultPendingTTL || ttl > DefaultCommittedTTL+ttlSlack {
		t.Fatalf("committed request ttl = %v, want within (%v, %v]", ttl, DefaultPendingTTL, DefaultCommittedTTL)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/lru/... ./apps/core/internal/dedupe/..."`
Expected: FAIL build:
- `pkg/lru`: `no non-test Go files in /src/pkg/lru`;
- `dedupe` (trình biên dịch dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm `undefined: Registry`, `undefined: Requests`, `undefined: NewRequests`, `undefined: RequestStatus`, `undefined: RequestKey`, `undefined: RequestNew`.

**Step 3: Code `pkg/lru`**

`pkg/lru/lru.go`:

```go
package lru

import "container/list"

type entry[K comparable, V any] struct {
	key K
	val V
}

type Cache[K comparable, V any] struct {
	limit int
	order *list.List
	items map[K]*list.Element
}

func New[K comparable, V any](limit int) *Cache[K, V] {
	return &Cache[K, V]{limit: limit, order: list.New(), items: make(map[K]*list.Element)}
}

func (c *Cache[K, V]) Get(k K) (V, bool) {
	el, ok := c.items[k]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*entry[K, V]).val, true
}

func (c *Cache[K, V]) Put(k K, v V) {
	if el, ok := c.items[k]; ok {
		el.Value.(*entry[K, V]).val = v
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&entry[K, V]{key: k, val: v})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*entry[K, V]).key)
	}
}

func (c *Cache[K, V]) Remove(k K) {
	if el, ok := c.items[k]; ok {
		c.order.Remove(el)
		delete(c.items, k)
	}
}

func (c *Cache[K, V]) Len() int { return c.order.Len() }
```

**Step 4: Code `dedupe`**

`apps/core/internal/dedupe/records.go`: thay khối `const (keyPrefix … committedPrefix)`, `type Key` và `func (k Key) String()` bằng:

```go
const (
	keyPrefix       = "chatim:cid:"
	requestPrefix   = "chatim:req:"
	pendingPrefix   = "p:"
	committedPrefix = "c:"
)

type Space uint8

const (
	SpaceCID     Space = 0
	SpaceRequest Space = 1
)

type Key struct {
	Room      uint64
	User, CID string
	Space     Space
}

func (k Key) String() string {
	prefix := keyPrefix
	if k.Space == SpaceRequest {
		prefix = requestPrefix
	}
	return prefix + strconv.FormatUint(k.Room, 10) + ":" + k.User + ":" + k.CID
}
```

`apps/core/internal/dedupe/batcher.go`: đổi tên interface `registry` thành `Registry` ở ba chỗ (khai báo `type Registry interface {`, field `store    Registry` của `Batcher`, tham số `func NewBatcher(store Registry, cfg BatchConfig, log *slog.Logger)`). Thân interface giữ nguyên.

`apps/core/internal/dedupe/requests.go`:

```go
package dedupe

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

type RequestStatus uint8

const (
	RequestNew  RequestStatus = 1
	RequestDone RequestStatus = 2
	RequestBusy RequestStatus = 3
)

const RequestCacheSize = 4096

type Requests struct {
	reg  Registry
	ttl  time.Duration
	mu   sync.Mutex
	done *lru.Cache[Key, time.Time]
}

func RequestKey(room uint64, user, requestID string) Key {
	return Key{Room: room, User: user, CID: requestID, Space: SpaceRequest}
}

func NewRequests(reg Registry, ttl time.Duration) (*Requests, error) {
	if reg == nil || ttl <= 0 {
		return nil, fmt.Errorf("%w: request dedupe needs a registry and a positive ttl, got ttl %v", apperr.ErrInvalidArgument, ttl)
	}
	return &Requests{reg: reg, ttl: ttl, done: lru.New[Key, time.Time](RequestCacheSize)}, nil
}

func (r *Requests) Begin(ctx context.Context, key Key) (RequestStatus, error) {
	if r.cached(key) {
		return RequestDone, nil
	}
	verdicts, err := r.reg.Reserve(ctx, []Key{key})
	if err != nil || len(verdicts) != 1 {
		if cerr := ctx.Err(); cerr != nil {
			return 0, cerr
		}
		return RequestNew, nil
	}
	switch verdicts[0].Status {
	case Committed:
		r.remember(key, verdicts[0].Record.CreatedAt)
		return RequestDone, nil
	case PendingHere, PendingElsewhere:
		return RequestBusy, nil
	default:
		return RequestNew, nil
	}
}

func (r *Requests) Finish(ctx context.Context, key Key, rec Record) {
	_ = r.reg.Commit(ctx, []Entry{{Key: key, Record: rec}})
	r.remember(key, rec.CreatedAt)
}

func (r *Requests) Cancel(ctx context.Context, key Key) {
	_ = r.reg.Abort(ctx, []Key{key})
}

func (r *Requests) cached(key Key) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.done.Get(key)
	if ok && time.Since(at) < r.ttl {
		return true
	}
	if ok {
		r.done.Remove(key)
	}
	return false
}

func (r *Requests) remember(key Key, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done.Put(key, at)
}
```

**Step 5: `actor` dùng `pkg/lru`**

```bash
git rm apps/core/internal/actor/lru_cache.go
git mv apps/core/internal/actor/lru_cache_internal_test.go apps/core/internal/actor/cid_cache_internal_test.go
```

`apps/core/internal/actor/cid_cache_internal_test.go`: xoá nguyên hàm `TestLRUEvictsLeastRecentlyUsed` (đã chuyển sang `pkg/lru`); trong `TestCIDCacheBoundsCommittedAcksButNeverPendingOnes` thay `d.acks.len()` bằng `d.acks.Len()`. Import giữ `errors`, `testing`, `time`.

`apps/core/internal/actor/cid_dedupe.go`:
- thay `import "time"` bằng khối import `"time"` + dòng trống + `"github.com/ivannguyendev/chatim/pkg/lru"`;
- field `acks    *lru[dedupeKey, cachedAck]` → `acks    *lru.Cache[dedupeKey, cachedAck]`;
- trong `newCIDCache`: `newLRU[dedupeKey, cachedAck](limit)` → `lru.New[dedupeKey, cachedAck](limit)`;
- `d.acks.get(k)` → `d.acks.Get(k)`, `d.acks.remove(k)` → `d.acks.Remove(k)`, `d.acks.put(k, …)` → `d.acks.Put(k, …)`.

`apps/core/internal/actor/room_actor.go`:
- import thêm `"github.com/ivannguyendev/chatim/pkg/lru"` (sau `".../internal/store"`);
- field `members *lru[string, domain.Member]` → `members *lru.Cache[string, domain.Member]`;
- trong `newActor`: `newLRU[string, domain.Member](memberCacheSize)` → `lru.New[string, domain.Member](memberCacheSize)`.

`apps/core/internal/actor/room_state.go`, trong `member`: `a.members.get(user)` → `a.members.Get(user)`, `a.members.put(user, m)` → `a.members.Put(user, m)` (Task 9 chuyển hàm này sang file khác).

Chạy `make -s go ARGS="fmt ./pkg/lru/ ./apps/core/internal/actor/ ./apps/core/internal/dedupe/"`.

**Step 6: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./pkg/lru/... ./apps/core/internal/actor/..."
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/dedupe/..."
make vet
```

Expected: PASS (dedupe 5 lần, goleak sạch; actor không đổi hành vi). `grep -rn "newLRU\|lru\[" apps/core/internal/actor/` rỗng. `wc -l`: `pkg/lru/lru.go` 51, `lru_test.go` 41, `dedupe/requests.go` 89, `requests_test.go` 141, `requests_harness_test.go` 78, `requests_redis_test.go` 70, `records.go` 116, `batcher.go` 191 (không đổi số dòng), `actor/cid_dedupe.go` 68.

**Step 7: Itest Redis thật**

```bash
make infra-up
make -f <scratchpad>/itest-one.mk itest-one RUN='TestRealRedis' PKG=./apps/core/internal/dedupe/
```

Expected: `ok  github.com/ivannguyendev/chatim/apps/core/internal/dedupe` (chạy `TestRealRedisReservationLifecycle` cũ và `TestRealRedisRequestLifecycle` mới trên `chatim-redis-dedupe`).

**Step 8: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "pkg/lru", "after": "pkg/keys", "row": ["package", "Generic LRU cache without locking, moved from apps/core/internal/actor: New(limit) keeps at most limit entries and evicts the least recently used; Get moves the entry to the front; callers hold their own lock", "Cache;New;Cache.Get;Cache.Put;Cache.Remove;Cache.Len", "apps/core/internal/actor;apps/core/internal/dedupe", "unit", "D99;D106"]},
 {"path": "apps/core/internal/dedupe", "col": "purpose", "append": "; request dedupe (D99): Key.Space = SpaceRequest names chatim:req:{room}:{user}:{request_id} with the same values, Lua and TTLs as cid keys (record seq = users in the command, created_at = command time); Requests.Begin answers RequestDone from a local LRU (RequestCacheSize, valid for ttl after the command) or a committed key, RequestBusy for a pending key, RequestNew otherwise and also when Redis fails (memory only, like CD2), and fails only for a cancelled context; Finish commits and remembers, Cancel aborts; the batcher's store interface is exported as Registry"},
 {"path": "apps/core/internal/dedupe", "col": "key_symbols", "append": ";Registry;Space;SpaceCID;SpaceRequest;RequestKey;Requests;NewRequests;Requests.Begin;Requests.Finish;Requests.Cancel;RequestStatus;RequestNew;RequestDone;RequestBusy;RequestCacheSize"},
 {"path": "apps/core/internal/dedupe", "col": "decisions", "append": ";D99"},
 {"path": "apps/core/internal/actor", "col": "purpose", "append": "; its LRU caches come from pkg/lru"}
]
EOF
make -s go ARGS="fmt ./..."
make fmt-check && make vet && make lint
git add pkg/lru/lru.go pkg/lru/lru_test.go apps/core/internal/dedupe/requests.go apps/core/internal/dedupe/requests_harness_test.go \
  apps/core/internal/dedupe/requests_test.go apps/core/internal/dedupe/requests_redis_test.go apps/core/internal/dedupe/requests_integration_test.go
git commit -m "feat(dedupe): request dedupe on its own key space and a shared lru package" -- pkg/lru/ apps/core/internal/dedupe/ apps/core/internal/actor/ INDEXES.csv
```

Expected: script in `{7}`; lint sạch; `git show --stat HEAD` liệt kê 16 mục: 2 trong `pkg/lru`, 8 trong `dedupe` (5 file mới, `records.go`, `batcher.go`, `batcher_harness_test.go`), 5 trong `actor` (xoá `lru_cache.go`, đổi tên test, `cid_dedupe.go`, `room_actor.go`, `room_state.go`), `INDEXES.csv`.

Task rủi ro: một reviewer (khoá `chatim:req:` không đụng `chatim:cid:`; `Begin` không bao giờ trả lỗi Redis; LRU hết hạn theo giờ lệnh; `Registry` chỉ đổi tên; `pkg/lru` giữ nguyên hành vi; tối đa `-count=3` trên `dedupe`, `actor`).

---

### Task 9: ★ Actor: thế hệ cache member + TTL, `Router.ForgetMembers`

Hôm nay `actor.member()` giữ member trong LRU 1024 phần tử, không hết hạn, không ai xoá: một room bận không bao giờ quên người đã bị xoá, nên người đó vẫn gửi tin được qua actor. D106 sửa bằng hai lớp:
- **Thế hệ.** Actor có `memberGen atomic.Uint64` và `seenGen uint64`. `Router.ForgetMembers(room)` giữ `r.mu.RLock`; actor của room có thì `memberGen.Add(1)`, không có thì thôi (actor dựng sau đó có cache rỗng). Đầu `member()`, actor so `memberGen.Load()` với `seenGen`: khác → LRU mới, ghi `seenGen`. Thế hệ được đọc **trước** khi đọc store, nên một lần đọc cũ chen giữa lệnh member và `ForgetMembers` chỉ sống tới lệnh kế tiếp của actor.
- **TTL.** Entry LRU là `cachedMember{m, at}`; quá `memberCacheTTL` (10s, đo bằng `time.Now()` của actor) thì đọc lại store. TTL là giới hạn xuyên core: lệnh member chạy trên core khác (hoặc worker) chỉ gọi `ForgetMembers` của chính nó.
- Kết quả "không phải member" (doc không có hoặc `state ≠ 1`, `Rooms.Member` đã lọc ở Task 4/5) không bao giờ được cache; entry cũ của user đó bị `Remove`, nên thêm lại có hiệu lực ngay.

Logic đặt trong file mới `member_cache.go` (`room_actor.go` đã 174 dòng sau Task 8); `room_state.go` bỏ hàm `member` cũ. Từ Task 9 tới Task 12 chưa ai gọi `ForgetMembers`; chỉ TTL có tác dụng.

Test xoá/thêm bob thẳng trên memstore qua port Part A (`MembersOf` + `ApplyMember(cur, cur.Next(...))`, `AddMembers(Join{...})`), không qua `mutate`.

**Files:**
- Create: `apps/core/internal/actor/member_cache.go`, `member_cache_test.go`
- Modify: `apps/core/internal/actor/room_state.go`, `room_actor.go`, `export_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/actor/export_test.go`: ngay sau dòng `package actor` thêm (một dòng trống trước và sau):

```go
const MemberCacheTTL = memberCacheTTL
```

`apps/core/internal/actor/member_cache_test.go`:

```go
package actor_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func removeMember(t *testing.T, rg *rig, room uint64, user string) {
	t.Helper()
	ctx := context.Background()
	docs, err := rg.rooms.MembersOf(ctx, room, []string{user})
	if err != nil || len(docs) != 1 {
		t.Fatalf("MembersOf(%d, %s) = %+v, %v", room, user, docs, err)
	}
	cur := docs[0]
	next := cur.Next(cur.Role, domain.MemberRemoved, "rm-"+user, "alice", time.Now().UTC())
	if ok, err := rg.rooms.ApplyMember(ctx, cur, next); err != nil || !ok {
		t.Fatalf("remove %s from room %d = %v, %v", user, room, ok, err)
	}
}

func addMember(t *testing.T, rg *rig, room uint64, user string) {
	t.Helper()
	j := domain.Join{Room: room, Tenant: tenant, RequestID: "add-" + user, By: "alice", At: time.Now().UTC()}
	if _, err := rg.rooms.AddMembers(context.Background(), j, []string{user}); err != nil {
		t.Fatalf("add %s to room %d: %v", user, room, err)
	}
}

func TestForgetMembersDropsARemovedMemberAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		mustSend(t, rg.Router, cmd(roomB, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		mustSend(t, rg.Router, cmd(roomA, "bob", "b2"))
		rg.ForgetMembers(roomB)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b3"))
		rg.ForgetMembers(roomA)
		_, err := rg.Send(t.Context(), cmd(roomA, "bob", "b4"))
		expectErr(t, err, domain.ErrNotMember)
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "a1")); ack.Seq != 4 {
			t.Fatalf("alice got seq %d after the forget, want 4", ack.Seq)
		}
		if ack := mustSend(t, rg.Router, cmd(roomB, "bob", "b2")); ack.Seq != 2 {
			t.Fatalf("bob in room %d got seq %d, want 2 (forgetting room %d keeps him there)", roomB, ack.Seq, roomA)
		}
	})
}

func TestCachedMembershipExpiresAfterTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		time.Sleep(actor.MemberCacheTTL - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b2"))
		if n := rg.rooms.memberCalls(); n != 1 {
			t.Fatalf("membership read %d times inside the TTL, want 1", n)
		}
		time.Sleep(time.Millisecond)
		_, err := rg.Send(t.Context(), cmd(roomA, "bob", "b3"))
		expectErr(t, err, domain.ErrNotMember)
		if n := rg.rooms.memberCalls(); n != 2 {
			t.Fatalf("membership read %d times, want 2 (read again once the TTL passed)", n)
		}
	})
}

func TestARemovedMemberIsNeverCachedSoAReAddWorksAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob")
		rg.ForgetMembers(roomA)
		for _, cid := range []string{"b2", "b3"} {
			_, err := rg.Send(t.Context(), cmd(roomA, "bob", cid))
			expectErr(t, err, domain.ErrNotMember)
		}
		addMember(t, rg, roomA, "bob")
		if ack := mustSend(t, rg.Router, cmd(roomA, "bob", "b4")); ack.Seq != 2 {
			t.Fatalf("re-added bob got seq %d, want 2 without any forget", ack.Seq)
		}
	})
}

func TestForgetMembersWithoutAnActorDoesNothing(t *testing.T) {
	rg := newRig(t, baseConfig)
	rg.ForgetMembers(roomA)
	rg.start(t)
	rg.ForgetMembers(roomA)
	mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
	if n := rg.rooms.memberCalls(); n != 1 {
		t.Fatalf("membership read %d times, want 1 (a forget before the actor existed changes nothing)", n)
	}
}

func TestForgetMembersIsSafeAlongsideSends(t *testing.T) {
	rg := started(t, baseConfig)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			rg.ForgetMembers(roomA)
			rg.ForgetMembers(roomB)
		}
	})
	for seq := uint64(1); seq <= 20; seq++ {
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c"+strconv.FormatUint(seq, 10))); ack.Seq != seq {
			t.Fatalf("send %d got seq %d", seq, ack.Seq)
		}
	}
	wg.Wait()
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/actor/..."`
Expected: FAIL build: `apps/core/internal/actor/export_test.go:3:24: undefined: memberCacheTTL` (trình biên dịch dừng ở lỗi đầu của package nội bộ).

**Step 3: Code**

`apps/core/internal/actor/member_cache.go`:

```go
package actor

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/lru"
)

const memberCacheTTL = 10 * time.Second

type cachedMember struct {
	m  domain.Member
	at time.Time
}

func (r *Router) ForgetMembers(room uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a := r.actors[room]; a != nil {
		a.memberGen.Add(1)
	}
}

func (a *actor) member(ctx context.Context, user string) (domain.Member, error) {
	if gen := a.memberGen.Load(); gen != a.seenGen {
		a.members, a.seenGen = lru.New[string, cachedMember](memberCacheSize), gen
	}
	now := time.Now()
	if c, ok := a.members.Get(user); ok && now.Sub(c.at) < memberCacheTTL {
		return c.m, nil
	}
	m, err := a.r.rooms.Member(ctx, a.id, user)
	switch {
	case err == nil:
		a.members.Put(user, cachedMember{m: m, at: now})
		return m, nil
	case errors.Is(err, domain.ErrNotMember):
		a.members.Remove(user)
		return domain.Member{}, domain.ErrNotMember
	default:
		a.r.log.WarnContext(ctx, "membership check failed", "room", a.id, "err", err)
		return domain.Member{}, errUnavailable
	}
}
```

`apps/core/internal/actor/room_state.go`: xoá nguyên hàm `func (a *actor) member(ctx context.Context, user string) (domain.Member, error)` ở cuối file (16 dòng). Import giữ nguyên (`errors`, `fmt`, `access`, `domain`, `store` vẫn được `load`, `refresh`, `admit` dùng).

`apps/core/internal/actor/room_actor.go`:
- import thêm `"sync/atomic"` trong khối stdlib, giữa `"context"` và `"time"`;
- trong `type actor struct`, thay dòng `members *lru.Cache[string, domain.Member]` bằng:

```go
	members   *lru.Cache[string, cachedMember]
	memberGen atomic.Uint64
	seenGen   uint64
```

- trong `newActor`, thay `members: lru.New[string, domain.Member](memberCacheSize),` bằng `members: lru.New[string, cachedMember](memberCacheSize),`.

Chạy `make -s go ARGS="fmt ./apps/core/internal/actor/"` (gofmt căn lại khối field).

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/..."
make vet
```

Expected: PASS 5 lần, goleak sạch. Test cũ `TestSendChecksMembershipAndCachesOnlyMembers` vẫn pass (trong 10s alice vẫn được cache, người lạ không bao giờ được cache). `wc -l`: `member_cache.go` 47, `member_cache_test.go` 121, `room_actor.go` 174, `room_state.go` 82, `export_test.go` 9.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/actor", "col": "purpose", "append": "; caches active members per actor for memberCacheTTL (10s) and drops the whole cache when Router.ForgetMembers bumps the actor's generation, so a removed member stops sending at once on the core that ran the member command and within 10s on any other core; non-members are never cached (D106)"},
 {"path": "apps/core/internal/actor", "col": "key_symbols", "append": ";Router.ForgetMembers"},
 {"path": "apps/core/internal/actor", "col": "decisions", "append": ";D106"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/actor/member_cache.go apps/core/internal/actor/member_cache_test.go
git commit -m "feat(actor): forget cached members by generation and after a ttl" -- apps/core/internal/actor/ INDEXES.csv
```

Expected: `{7}`; lint sạch; `git show --stat HEAD` có 6 file (`member_cache.go`, `member_cache_test.go`, `room_state.go`, `room_actor.go`, `export_test.go`, `INDEXES.csv`).

Task rủi ro: một reviewer (thế hệ đọc trước store, `ForgetMembers` chỉ giữ `RLock` và chỉ chạm atomic, không cache "không phải member", TTL đo bằng giờ actor; tối đa `-count=3` trên `actor`).

---

### Task 10: ★ `access`: 5 action, `Request.Target`/`Role`, `DefaultPolicy`

Quyền member đi qua `access.Policy` như mọi action khác (D101). `mutate` (Task 12) và `ownership` (Task 11, qua callback `Authorize`) hỏi policy với doc caller (`Request.Member`), doc đích (`Request.Target`, zero khi đích chưa từng là member) và role được yêu cầu (`Request.Role`), luôn đọc **mới** ở mỗi lượt thử.

Luật `DefaultPolicy` (owner chốt 2026-10-06):

| Action | Cho phép |
|---|---|
| `add_members` | caller owner hoặc admin |
| `remove_member` | caller owner; hoặc caller admin và `Target.Role == member` |
| `change_member_role` | chỉ caller owner (luật owner cuối nằm ở `ownership`) |
| `leave_room`, `mark_read` | luôn cho phép (`MarkUnread` dùng `mark_read`) |
| `edit_message`, `delete_message` | như cũ: tác giả, loại tin không bị khoá |
| còn lại | cho phép |

`LockedKinds` chỉ áp cho sửa/xoá; role không bao giờ mở khoá một loại tin. `AllowMembers` không đổi (cho phép mọi thứ). `Checker` không đổi: `Admit` dựa vào `Rooms.Member` đã lọc `state == 1` (Task 4/5). Admin xoá một user chưa từng là member (`Target` zero) bị từ chối, đúng chữ "`Target.Role == member`"; `mutate` trả `ErrMemberNotFound` trước khi hỏi policy cho trường hợp này.

**Files:**
- Modify: `apps/core/internal/access/policy.go`
- Create: `apps/core/internal/access/member_policy_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/access/member_policy_test.go`:

```go
package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestMemberActionNames(t *testing.T) {
	for action, name := range map[access.Action]string{
		access.AddMembers: "add_members", access.RemoveMember: "remove_member", access.LeaveRoom: "leave_room",
		access.ChangeMemberRole: "change_member_role", access.MarkRead: "mark_read",
	} {
		if string(action) != name {
			t.Fatalf("action %q, want %q", action, name)
		}
	}
}

func TestDefaultPolicyMemberRules(t *testing.T) {
	const owner, admin, member, none = domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.Role("")
	cases := []struct {
		name           string
		action         access.Action
		caller, target domain.Role
		want           error
	}{
		{"owner adds", access.AddMembers, owner, none, nil},
		{"admin adds", access.AddMembers, admin, none, nil},
		{"member adds", access.AddMembers, member, none, access.ErrDenied},
		{"owner removes an owner", access.RemoveMember, owner, owner, nil},
		{"owner removes an admin", access.RemoveMember, owner, admin, nil},
		{"owner removes a member", access.RemoveMember, owner, member, nil},
		{"admin removes a member", access.RemoveMember, admin, member, nil},
		{"admin removes an admin", access.RemoveMember, admin, admin, access.ErrDenied},
		{"admin removes an owner", access.RemoveMember, admin, owner, access.ErrDenied},
		{"admin removes an unknown user", access.RemoveMember, admin, none, access.ErrDenied},
		{"member removes a member", access.RemoveMember, member, member, access.ErrDenied},
		{"owner promotes a member", access.ChangeMemberRole, owner, member, nil},
		{"owner demotes an owner", access.ChangeMemberRole, owner, owner, nil},
		{"admin changes a role", access.ChangeMemberRole, admin, member, access.ErrDenied},
		{"member changes a role", access.ChangeMemberRole, member, member, access.ErrDenied},
		{"owner leaves", access.LeaveRoom, owner, owner, nil},
		{"admin leaves", access.LeaveRoom, admin, admin, nil},
		{"member leaves", access.LeaveRoom, member, member, nil},
		{"member marks read", access.MarkRead, member, none, nil},
	}
	policies := map[string]access.DefaultPolicy{
		"nothing locked": {},
		"text locked":    {LockedKinds: []domain.Kind{domain.KindText}},
	}
	for _, c := range cases {
		for label, p := range policies {
			req := access.Request{
				Action: c.action, User: "alice", Kind: domain.KindText, Role: domain.RoleAdmin,
				Member: domain.Member{User: "alice", Role: c.caller}, Target: domain.Member{User: "bob", Role: c.target},
			}
			if err := p.Check(t.Context(), req); !errors.Is(err, c.want) {
				t.Fatalf("%s (%s) = %v, want %v", c.name, label, err, c.want)
			}
		}
	}
}

func TestDefaultPolicyKeepsItsMessageRulesNextToMemberRules(t *testing.T) {
	p := access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}}
	owner := domain.Member{User: "alice", Role: domain.RoleOwner}
	for _, action := range []access.Action{access.EditMessage, access.DeleteMessage} {
		req := access.Request{Action: action, User: "alice", Author: "alice", Kind: domain.KindText, Member: owner}
		if err := p.Check(t.Context(), req); !errors.Is(err, access.ErrDenied) {
			t.Fatalf("owner %s of a locked kind = %v, want ErrDenied (roles never unlock a kind)", action, err)
		}
	}
}

func TestAllowMembersAllowsMemberActions(t *testing.T) {
	for _, action := range []access.Action{access.AddMembers, access.RemoveMember, access.ChangeMemberRole} {
		req := access.Request{Action: action, User: "bob", Member: domain.Member{User: "bob", Role: domain.RoleMember}}
		if err := (access.AllowMembers{}).Check(t.Context(), req); err != nil {
			t.Fatalf("AllowMembers %s = %v, want nil", action, err)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: FAIL build (dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm `undefined: access.AddMembers`, `undefined: access.RemoveMember`, `undefined: access.LeaveRoom`, `undefined: access.ChangeMemberRole`, `undefined: access.MarkRead` (lỗi `unknown field Target/Role in struct literal of type access.Request` nằm sau mốc 10 lỗi).

**Step 3: Code**

`apps/core/internal/access/policy.go` (thay cả file):

```go
package access

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	ReadHistory      Action = "read_history"
	SendMessage      Action = "send_message"
	EditMessage      Action = "edit_message"
	DeleteMessage    Action = "delete_message"
	HideMessage      Action = "hide_message"
	ClearHistory     Action = "clear_history"
	ReadEditHistory  Action = "read_edit_history"
	ReactMessage     Action = "react_message"
	PinMessage       Action = "pin_message"
	UnpinMessage     Action = "unpin_message"
	AddMembers       Action = "add_members"
	RemoveMember     Action = "remove_member"
	LeaveRoom        Action = "leave_room"
	ChangeMemberRole Action = "change_member_role"
	MarkRead         Action = "mark_read"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
	Author string
	Kind   domain.Kind
	Room   domain.Room
	Member domain.Member
	Target domain.Member
	Role   domain.Role
}

type Policy interface {
	Check(ctx context.Context, req Request) error
}

type PolicyFunc func(ctx context.Context, req Request) error

func (f PolicyFunc) Check(ctx context.Context, req Request) error { return f(ctx, req) }

type AllowMembers struct{}

func (AllowMembers) Check(context.Context, Request) error { return nil }

type DefaultPolicy struct {
	LockedKinds []domain.Kind
}

func (p DefaultPolicy) Check(_ context.Context, req Request) error {
	caller := req.Member.Role
	switch req.Action {
	case EditMessage, DeleteMessage:
		return allowIf(!slices.Contains(p.LockedKinds, req.Kind) && req.Author == req.User)
	case AddMembers:
		return allowIf(caller == domain.RoleOwner || caller == domain.RoleAdmin)
	case RemoveMember:
		return allowIf(caller == domain.RoleOwner || (caller == domain.RoleAdmin && req.Target.Role == domain.RoleMember))
	case ChangeMemberRole:
		return allowIf(caller == domain.RoleOwner)
	default:
		return nil
	}
}

func allowIf(ok bool) error {
	if ok {
		return nil
	}
	return ErrDenied
}
```

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/... ./apps/core/internal/actor/... ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/..."
make vet
```

Expected: PASS (test cũ của `access`, `actor`, `mutate`, `grpcsrv` không đổi hành vi: sửa/xoá giữ luật cũ, action khác vẫn cho qua). `wc -l`: `policy.go` 82, `member_policy_test.go` 85.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/access", "col": "purpose", "append": "; member actions add_members, remove_member, leave_room, change_member_role and mark_read (also MarkUnread); Request.Target is the target member doc (zero when never a member) and Request.Role the requested role; DefaultPolicy: owners do everything, admins add members and remove plain members, only owners change roles or remove admins and owners, leave_room and mark_read are always allowed, LockedKinds apply only to edit/delete (D101)"},
 {"path": "apps/core/internal/access", "col": "key_symbols", "append": ";AddMembers;RemoveMember;LeaveRoom;ChangeMemberRole;MarkRead;Request.Target;Request.Role"},
 {"path": "apps/core/internal/access", "col": "decisions", "append": ";D101"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/access/member_policy_test.go
git commit -m "feat(access): member and read actions with owner and admin rules" -- apps/core/internal/access/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 3 file.

Task rủi ro: một reviewer (bảng luật đúng chữ owner chốt, `LockedKinds` chỉ cho sửa/xoá, không đổi `Checker`; tối đa `-count=3` trên `access`).

---

### Task 11: ★ Package `ownership` (Apply, finish, Guard) trên memstore

Lệnh chạm tới owner (owner rời, xoá owner, hạ owner, thăng lên owner) đi qua CAS đầu room (D100) để giữ bất biến OW1 "group còn member active thì còn owner active ở mọi thời điểm". `ownership.Coordinator` chỉ dùng port Part A (`MemberReader`, `MemberWriter`, `OwnerChanges`), không biết Mongo.

**`Apply(ch)`**, tối đa `MaxTries` (3) lượt:
1. `OwnerState(room)`. Có `Pending` → `finish(state.Ver, *pending)` (của ai cũng được, idempotent; `ErrRetryLater` của nó bỏ qua) → lượt sau.
2. Đọc caller + đích bằng **một** `MembersOf` (khử trùng khi rời). Caller không active → `domain.ErrNotMember`.
3. `ch.Authorize(caller, target)` nếu có (mutate truyền hàm gọi lại `access.Checker.Allow` với doc mới đọc), lỗi trả nguyên.
4. Đích không active, hoặc `change_role` về đúng role hiện tại → `Result{Target, Changed: false}`.
5. Kế hoạch: `OwnerChange{Action, User, UserVer: target.Ver, Role (chỉ change_role), RequestID, UpdatedBy: caller, UpdatedAt: ch.At}`. Đích là owner và role mới không phải owner → `Owners(room, 2)`: còn ≥ 2 owner → không cần gì thêm; chỉ còn 1 → `change_role` trả `domain.ErrLastOwner`, rời/xoá chọn `Successor(room)` (admin trước member, rồi `joined_at`, rồi user id; không có → group sẽ rỗng) ghi `Successor`/`SuccessorVer`.
6. `BeginOwnerChange(room, state.Ver, change)`; trượt (ai đó vừa đổi owner) → lượt sau.
7. `finish(state.Ver+1, change)`; `ErrRetryLater` (kế nhiệm hoặc đích vừa bị đổi) → lượt sau; còn lại trả kết quả.

Hết lượt → `domain.ErrRetryLater`.

**`finish(ver, p)`** (mọi bước idempotent, chạy lại sau crash an toàn):
1. Có `Successor`: đọc doc; `doc.Ver == SuccessorVer` → `ApplyMember(doc, doc.Next(owner, active, p.RequestID, p.UpdatedBy, p.UpdatedAt))`; trượt thì đọc lại. Doc đã ở `SuccessorVer+1` với `RequestID == p.RequestID` → đã áp. Khác → `EndOwnerChange(room, ver)` rồi `domain.ErrRetryLater` (đích **không** bị chạm, nên owner cũ vẫn còn).
2. Đích (trừ `repair`): cùng quy tắc theo `UserVer`; rời/xoá → `Next(role giữ nguyên, removed, …)`, `change_role` → `Next(p.Role, active, …)`.
3. `EndOwnerChange(room, ver)` (chỉ khớp khi `owners_ver == ver`, nên một lần End muộn không xoá pending của lệnh sau).

Kế nhiệm luôn được thăng **trước** khi hạ/xoá đích, và đường thường (`mutate`) chỉ ghi doc đã kiểm không phải owner bằng CAS `ver` của đúng doc đó, nên ở mọi thời điểm group còn member active thì còn owner active.

**`Guard(room, at)`** (effect `owner_guard`, Task 16), tối đa `MaxTries` lượt: pending → `finish`, `repaired = true`, lượt sau; `Owners(room, 1)` có → trả `repaired`; không có và `Successor` có → `BeginOwnerChange({Action: repair, Successor, SuccessorVer, RequestID: "owners-v{k+1}", UpdatedAt: at})` (`UpdatedBy` rỗng: actor event `""`) → `finish` → `true`; group rỗng → `repaired`. CAS trượt → lượt sau (thường thấy owner do lệnh kia vừa tạo). Room không có → `domain.ErrRoomNotFound` (từ `OwnerState`).

Test chạy trên memstore (Part A Task 4). Một wrapper `hooked` cho phép chèn "đối thủ" ngay trước `BeginOwnerChange` và làm `ApplyMember` lỗi ở lượt thứ n (giả lập core chết giữa CAS và ghi doc). Các tình huống đồng thời được phủ:
- hai owner rời cùng lúc (xen kẽ tất định qua hook, và 24 room × 2 goroutine thật, `-race`);
- hai owner xoá nhau (bên thua thấy mình đã bị xoá → `ErrNotMember`);
- core chết sau CAS trước mọi ghi → `Guard` hoàn tất; chết sau khi thăng kế nhiệm → lệnh owner kế tiếp hoàn tất pending trước rồi mới làm việc của nó;
- kế nhiệm bị đổi giữa kế hoạch và ghi → bỏ change, chọn kế nhiệm khác;
- `Guard` đua `Guard` chỉ sửa một lần.

**Files:**
- Create: `apps/core/internal/ownership/coordinator.go`, `plan.go`, `finish.go`
- Create: `apps/core/internal/ownership/helpers_test.go`, `apply_test.go`, `race_test.go`, `crash_test.go`, `guard_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/ownership/helpers_test.go`:

```go
package ownership_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	tenant        = "acme"
	room   uint64 = 5151
)

var (
	created = time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	later   = created.Add(time.Hour)
	errDown = errors.New("members collection down")
)

type hooked struct {
	*memstore.Rooms
	mu          sync.Mutex
	beforeBegin []func()
	failApply   map[int]error
	applies     int
}

func (h *hooked) BeginOwnerChange(ctx context.Context, id, base uint64, c domain.OwnerChange) (bool, error) {
	h.mu.Lock()
	var hook func()
	if len(h.beforeBegin) > 0 {
		hook, h.beforeBegin = h.beforeBegin[0], h.beforeBegin[1:]
	}
	h.mu.Unlock()
	if hook != nil {
		hook()
	}
	return h.Rooms.BeginOwnerChange(ctx, id, base, c)
}

func (h *hooked) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	h.mu.Lock()
	h.applies++
	err := h.failApply[h.applies]
	h.mu.Unlock()
	if err != nil {
		return false, err
	}
	return h.Rooms.ApplyMember(ctx, cur, next)
}

type world struct {
	rooms *memstore.Rooms
	store *hooked
	c     *ownership.Coordinator
}

func newWorld(t *testing.T, users ...string) *world {
	t.Helper()
	w := &world{rooms: memstore.NewRooms()}
	w.store = &hooked{Rooms: w.rooms, failApply: map[int]error{}}
	c, err := ownership.New(w.store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.c = c
	w.group(t, room, users...)
	return w
}

func (w *world) group(t *testing.T, id uint64, users ...string) {
	t.Helper()
	r, members, err := domain.NewRoom(tenant, users[0], domain.RoomGroup, "crew", users, created, id)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := w.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func (w *world) doc(t *testing.T, id uint64, user string) domain.Member {
	t.Helper()
	docs, err := w.rooms.MembersOf(t.Context(), id, []string{user})
	if err != nil || len(docs) != 1 {
		t.Fatalf("MembersOf(%d, %s) = %+v, %v", id, user, docs, err)
	}
	return docs[0]
}

func (w *world) set(t *testing.T, id uint64, user string, role domain.Role, state domain.MemberState) domain.Member {
	t.Helper()
	cur := w.doc(t, id, user)
	next := cur.Next(role, state, "setup-"+user, "alice", created)
	if ok, err := w.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("set %s to %s/%d = %v, %v", user, role, state, ok, err)
	}
	return next
}

func (w *world) join(t *testing.T, id uint64, user string, at time.Time) {
	t.Helper()
	j := domain.Join{Room: id, Tenant: tenant, RequestID: "join-" + user, By: "alice", At: at}
	if _, err := w.rooms.AddMembers(t.Context(), j, []string{user}); err != nil {
		t.Fatalf("join %s: %v", user, err)
	}
}

func (w *world) expect(t *testing.T, id uint64, user string, role domain.Role, active bool) {
	t.Helper()
	if d := w.doc(t, id, user); d.Role != role || d.Active() != active {
		t.Fatalf("%s = role %s active %v, want role %s active %v", user, d.Role, d.Active(), role, active)
	}
}

func (w *world) settled(t *testing.T, id uint64, wantVer uint64) {
	t.Helper()
	st, err := w.rooms.OwnerState(t.Context(), id)
	if err != nil || st.Pending != nil || st.Ver != wantVer {
		t.Fatalf("owner state = %+v, %v; want version %d and nothing pending", st, err, wantVer)
	}
}

func (w *world) owned(t *testing.T, id uint64, users ...string) {
	t.Helper()
	owners, err := w.rooms.Owners(t.Context(), id, 1)
	if err != nil {
		t.Fatalf("Owners: %v", err)
	}
	docs, err := w.rooms.MembersOf(t.Context(), id, users)
	if err != nil {
		t.Fatalf("MembersOf: %v", err)
	}
	for _, d := range docs {
		if d.Active() && len(owners) == 0 {
			t.Fatalf("room %d has active member %s but no owner", id, d.User)
		}
	}
}

func change(action domain.OwnerAction, caller, target string, role domain.Role, rid string) ownership.Change {
	return ownership.Change{Room: room, Action: action, Caller: caller, Target: target, Role: role, RequestID: rid, At: later}
}

func (w *world) apply(t *testing.T, ch ownership.Change) ownership.Result {
	t.Helper()
	res, err := w.c.Apply(t.Context(), ch)
	if err != nil {
		t.Fatalf("Apply(%s %s by %s): %v", ch.Action, ch.Target, ch.Caller, err)
	}
	return res
}
```

`apps/core/internal/ownership/apply_test.go`:

```go
package ownership_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestTheLastOwnerLeavingPromotesTheEarliestAdminFirst(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.join(t, room, "dave", created.Add(time.Minute))
	w.set(t, room, "dave", domain.RoleAdmin, domain.MemberActive)
	w.set(t, room, "carol", domain.RoleAdmin, domain.MemberActive)
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-1"))
	if !res.Changed || res.Successor.User != "carol" || res.Target.User != "alice" {
		t.Fatalf("Apply = %+v, want alice gone and carol (the earliest admin) promoted", res)
	}
	heir, gone := res.Successor, res.Target
	if heir.Role != domain.RoleOwner || heir.PreviousRole != domain.RoleAdmin || heir.RequestID != "rq-1" || heir.UpdatedBy != "alice" || !heir.UpdatedAt.Equal(later) {
		t.Fatalf("successor = %+v, want an owner from admin with request rq-1 by alice", heir)
	}
	if gone.State != domain.MemberRemoved || gone.Role != domain.RoleOwner || gone.PreviousState != domain.MemberActive || gone.RequestID != "rq-1" {
		t.Fatalf("target = %+v, want a removed owner from request rq-1", gone)
	}
	w.expect(t, room, "carol", domain.RoleOwner, true)
	w.expect(t, room, "dave", domain.RoleAdmin, true)
	w.expect(t, room, "alice", domain.RoleOwner, false)
	w.settled(t, room, 1)
}

func TestWithoutAdminsTheEarliestMemberTakesOverAndTiesGoByUserID(t *testing.T) {
	w := newWorld(t, "alice", "carol", "bob")
	w.join(t, room, "aaron", created.Add(time.Minute))
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-1"))
	if res.Successor.User != "bob" {
		t.Fatalf("successor = %q, want bob (joined first, before carol by id; aaron joined later)", res.Successor.User)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.expect(t, room, "carol", domain.RoleMember, true)
}

func TestAnOwnerLeavingBesideAnotherOwnerNamesNoSuccessor(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.set(t, room, "bob", domain.RoleOwner, domain.MemberActive)
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-1"))
	if !res.Changed || res.Successor.User != "" {
		t.Fatalf("Apply = %+v, want no successor while bob owns the room", res)
	}
	w.expect(t, room, "carol", domain.RoleMember, true)
	w.owned(t, room, "bob", "carol")
}

func TestTheOnlyMemberLeavingEmptiesTheGroup(t *testing.T) {
	w := newWorld(t, "alice")
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-1"))
	if !res.Changed || res.Successor.User != "" || res.Target.Active() {
		t.Fatalf("Apply = %+v, want alice gone without a successor", res)
	}
	w.settled(t, room, 1)
}

func TestTheLastOwnerCannotStepDownButAPromotionGoesThroughTheOwnerPath(t *testing.T) {
	w := newWorld(t, "alice", "bob")
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerChangeRole, "alice", "alice", domain.RoleMember, "rq-1")); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("the last owner steps down = %v, want ErrLastOwner", err)
	}
	w.settled(t, room, 0)
	if res := w.apply(t, change(domain.OwnerChangeRole, "alice", "bob", domain.RoleOwner, "rq-2")); !res.Changed || res.Target.PreviousRole != domain.RoleMember {
		t.Fatalf("promote bob = %+v, want bob made owner from member", res)
	}
	if res := w.apply(t, change(domain.OwnerChangeRole, "alice", "alice", domain.RoleAdmin, "rq-3")); !res.Changed || res.Target.Role != domain.RoleAdmin {
		t.Fatalf("alice steps down beside bob = %+v, want admin", res)
	}
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerChangeRole, "bob", "bob", domain.RoleMember, "rq-4")); !errors.Is(err, domain.ErrLastOwner) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("bob, now the last owner, steps down = %v, want ErrLastOwner", err)
	}
	w.settled(t, room, 2)
}

func TestApplyIsDesiredState(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.set(t, room, "carol", domain.RoleMember, domain.MemberRemoved)
	cases := map[string]ownership.Change{
		"remove a tombstone": change(domain.OwnerRemove, "alice", "carol", "", "rq-1"),
		"remove a stranger":  change(domain.OwnerRemove, "alice", "zed", "", "rq-2"),
		"keep the same role": change(domain.OwnerChangeRole, "alice", "alice", domain.RoleOwner, "rq-3"),
	}
	for name, ch := range cases {
		if res, err := w.c.Apply(t.Context(), ch); err != nil || res.Changed {
			t.Fatalf("%s = %+v, %v; want an unchanged success", name, res, err)
		}
	}
	w.settled(t, room, 0)
}

func TestApplyChecksTheCallerAndAsksAuthorizeWithFreshDocs(t *testing.T) {
	w := newWorld(t, "alice", "bob")
	w.set(t, room, "bob", domain.RoleOwner, domain.MemberActive)
	var seen []domain.Member
	deny := change(domain.OwnerRemove, "alice", "bob", "", "rq-1")
	deny.Authorize = func(_ context.Context, caller, target domain.Member) error {
		seen = append(seen, caller, target)
		return access.ErrDenied
	}
	if _, err := w.c.Apply(t.Context(), deny); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("denied Apply = %v, want ErrDenied", err)
	}
	if len(seen) != 2 || seen[0].User != "alice" || seen[1].User != "bob" || seen[1].Role != domain.RoleOwner || seen[1].Ver != 2 {
		t.Fatalf("Authorize saw %+v, want alice and bob (owner at ver 2)", seen)
	}
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerRemove, "mallory", "bob", "", "rq-2")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("a stranger's Apply = %v, want ErrNotMember", err)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.settled(t, room, 0)
}

func TestNewNeedsAStore(t *testing.T) {
	if _, err := ownership.New(nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil) = %v, want ErrInvalidArgument", err)
	}
	if ownership.MaxTries != 3 {
		t.Fatalf("MaxTries = %d, want 3", ownership.MaxTries)
	}
}
```

`apps/core/internal/ownership/race_test.go`:

```go
package ownership_test

import (
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestTwoOwnersLeavingAtOnceKeepAnOwner(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol", "dave")
	w.set(t, room, "bob", domain.RoleOwner, domain.MemberActive)
	var rival error
	w.store.beforeBegin = []func(){func() {
		_, rival = w.c.Apply(t.Context(), change(domain.OwnerLeave, "bob", "bob", "", "rq-bob"))
	}}
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-alice"))
	if rival != nil {
		t.Fatalf("bob's leave: %v", rival)
	}
	if !res.Changed || res.Successor.User != "carol" {
		t.Fatalf("alice's leave = %+v, want carol promoted once alice saw she was the last owner", res)
	}
	w.expect(t, room, "alice", domain.RoleOwner, false)
	w.expect(t, room, "bob", domain.RoleOwner, false)
	w.expect(t, room, "carol", domain.RoleOwner, true)
	w.expect(t, room, "dave", domain.RoleMember, true)
	w.settled(t, room, 2)
}

func TestOwnersRemovingEachOtherLeaveTheWinnerInCharge(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.set(t, room, "bob", domain.RoleOwner, domain.MemberActive)
	var rival error
	w.store.beforeBegin = []func(){func() {
		_, rival = w.c.Apply(t.Context(), change(domain.OwnerRemove, "bob", "alice", "", "rq-bob"))
	}}
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerRemove, "alice", "bob", "", "rq-alice")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("alice removes bob after losing the race = %v, want ErrNotMember", err)
	}
	if rival != nil {
		t.Fatalf("bob removes alice: %v", rival)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.expect(t, room, "alice", domain.RoleOwner, false)
	w.settled(t, room, 1)
}

func TestConcurrentOwnerLeavesNeverOrphanAGroup(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	const rooms uint64 = 24
	for i := range rooms {
		id := room + 1 + i
		w.group(t, id, "alice", "bob", "carol", "dave")
		w.set(t, id, "bob", domain.RoleOwner, domain.MemberActive)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*rooms)
	for i := range rooms {
		id := room + 1 + i
		for _, u := range []string{"alice", "bob"} {
			wg.Go(func() {
				ch := change(domain.OwnerLeave, u, u, "", "rq-"+u+"-"+strconv.FormatUint(i, 10))
				ch.Room = id
				for range 3 {
					_, err := w.c.Apply(t.Context(), ch)
					if !errors.Is(err, domain.ErrRetryLater) {
						errs <- err
						return
					}
				}
				errs <- domain.ErrRetryLater
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent leave: %v", err)
		}
	}
	for i := range rooms {
		id := room + 1 + i
		w.expect(t, id, "alice", domain.RoleOwner, false)
		w.expect(t, id, "bob", domain.RoleOwner, false)
		w.expect(t, id, "carol", domain.RoleOwner, true)
		w.owned(t, id, "carol", "dave")
	}
}
```

`apps/core/internal/ownership/crash_test.go`:

```go
package ownership_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestACrashBetweenTheCASAndTheWritesIsFinishedByTheGuard(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.store.failApply[1] = errDown
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerLeave, "alice", "alice", "", "rq-1")); !errors.Is(err, errDown) {
		t.Fatalf("Apply with a failing write = %v, want the store error", err)
	}
	st, err := w.rooms.OwnerState(t.Context(), room)
	if err != nil || st.Ver != 1 || st.Pending == nil || st.Pending.Successor != "bob" || st.Pending.User != "alice" {
		t.Fatalf("owner state = %+v, %v; want alice's leave pending with bob as successor", st, err)
	}
	w.expect(t, room, "alice", domain.RoleOwner, true)
	w.owned(t, room, "alice", "bob", "carol")
	repaired, err := w.c.Guard(t.Context(), room, later)
	if err != nil || !repaired {
		t.Fatalf("Guard = %v, %v; want the pending change finished", repaired, err)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.expect(t, room, "alice", domain.RoleOwner, false)
	if bob := w.doc(t, room, "bob"); bob.RequestID != "rq-1" || bob.UpdatedBy != "alice" {
		t.Fatalf("bob = %+v, want the promotion of request rq-1 by alice", bob)
	}
	w.settled(t, room, 1)
	if again, err := w.c.Guard(t.Context(), room, later); err != nil || again {
		t.Fatalf("second Guard = %v, %v; want nothing to repair", again, err)
	}
}

func TestACrashAfterThePromotionIsFinishedByTheNextCommand(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol", "dave")
	w.store.failApply[2] = errDown
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerLeave, "alice", "alice", "", "rq-1")); !errors.Is(err, errDown) {
		t.Fatalf("Apply with a failing second write = %v, want the store error", err)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.expect(t, room, "alice", domain.RoleOwner, true)
	res := w.apply(t, change(domain.OwnerChangeRole, "bob", "carol", domain.RoleOwner, "rq-2"))
	if !res.Changed || res.Target.User != "carol" || res.Target.Role != domain.RoleOwner {
		t.Fatalf("bob promotes carol = %+v, want carol made owner", res)
	}
	w.expect(t, room, "alice", domain.RoleOwner, false)
	if alice := w.doc(t, room, "alice"); alice.RequestID != "rq-1" || alice.Ver != 2 {
		t.Fatalf("alice = %+v, want her leave (rq-1) applied once", alice)
	}
	w.settled(t, room, 2)
}

func TestASuccessorThatChangedMeanwhileIsReplaced(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.store.beforeBegin = []func(){func() { w.set(t, room, "bob", domain.RoleMember, domain.MemberRemoved) }}
	res := w.apply(t, change(domain.OwnerLeave, "alice", "alice", "", "rq-1"))
	if !res.Changed || res.Successor.User != "carol" {
		t.Fatalf("Apply = %+v, want carol promoted after bob left between the plan and the writes", res)
	}
	w.expect(t, room, "bob", domain.RoleMember, false)
	w.expect(t, room, "carol", domain.RoleOwner, true)
	w.expect(t, room, "alice", domain.RoleOwner, false)
	w.settled(t, room, 2)
}

func TestAPendingChangeIsFinishedBeforeAnyNewOne(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.set(t, room, "bob", domain.RoleOwner, domain.MemberActive)
	w.store.failApply[1] = errDown
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerRemove, "alice", "bob", "", "rq-1")); !errors.Is(err, errDown) {
		t.Fatalf("Apply with a failing write = %v, want the store error", err)
	}
	if _, err := w.c.Apply(t.Context(), change(domain.OwnerRemove, "bob", "alice", "", "rq-2")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("bob removes alice while his own removal is pending = %v, want ErrNotMember once it is finished", err)
	}
	w.expect(t, room, "alice", domain.RoleOwner, true)
	w.expect(t, room, "bob", domain.RoleOwner, false)
	w.settled(t, room, 1)
}
```

`apps/core/internal/ownership/guard_test.go`:

```go
package ownership_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestGuardPromotesASuccessorInAGroupWithoutAnOwner(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.join(t, room, "dave", created.Add(time.Minute))
	w.set(t, room, "dave", domain.RoleAdmin, domain.MemberActive)
	w.set(t, room, "alice", domain.RoleOwner, domain.MemberRemoved)
	repaired, err := w.c.Guard(t.Context(), room, later)
	if err != nil || !repaired {
		t.Fatalf("Guard = %v, %v; want a repair", repaired, err)
	}
	dave := w.doc(t, room, "dave")
	if dave.Role != domain.RoleOwner || dave.PreviousRole != domain.RoleAdmin || dave.RequestID != "owners-v1" || dave.UpdatedBy != "" || !dave.UpdatedAt.Equal(later) {
		t.Fatalf("dave = %+v, want owner from admin by repair owners-v1 with no actor", dave)
	}
	w.settled(t, room, 1)
	if again, err := w.c.Guard(t.Context(), room, later); err != nil || again {
		t.Fatalf("second Guard = %v, %v; want nothing to repair", again, err)
	}
}

func TestGuardLeavesEmptyAndOwnedGroupsAlone(t *testing.T) {
	w := newWorld(t, "alice", "bob")
	if repaired, err := w.c.Guard(t.Context(), room, later); err != nil || repaired {
		t.Fatalf("Guard on an owned group = %v, %v; want no repair", repaired, err)
	}
	w.set(t, room, "alice", domain.RoleOwner, domain.MemberRemoved)
	w.set(t, room, "bob", domain.RoleMember, domain.MemberRemoved)
	if repaired, err := w.c.Guard(t.Context(), room, later); err != nil || repaired {
		t.Fatalf("Guard on an empty group = %v, %v; want no repair", repaired, err)
	}
	w.settled(t, room, 0)
	if _, err := w.c.Guard(t.Context(), 1, later); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Guard on a missing room = %v, want ErrRoomNotFound", err)
	}
}

func TestGuardRacingACommandRepairsOnce(t *testing.T) {
	w := newWorld(t, "alice", "bob", "carol")
	w.set(t, room, "alice", domain.RoleOwner, domain.MemberRemoved)
	w.store.beforeBegin = []func(){func() {
		if _, err := w.c.Guard(t.Context(), room, later); err != nil {
			t.Errorf("rival Guard: %v", err)
		}
	}}
	repaired, err := w.c.Guard(t.Context(), room, later)
	if err != nil || repaired {
		t.Fatalf("Guard that lost the CAS = %v, %v; want no second repair", repaired, err)
	}
	w.expect(t, room, "bob", domain.RoleOwner, true)
	w.expect(t, room, "carol", domain.RoleMember, true)
	w.settled(t, room, 1)
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/ownership/..."`
Expected: FAIL build: `no non-test Go files in /src/apps/core/internal/ownership` (package mới chỉ có file test).

**Step 3: Code**

`apps/core/internal/ownership/coordinator.go`:

```go
package ownership

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxTries = 3

var errNoStore = fmt.Errorf("%w: owner coordinator needs a member store", apperr.ErrInvalidArgument)

type Store interface {
	store.MemberReader
	store.MemberWriter
	store.OwnerChanges
}

type Authorize func(ctx context.Context, caller, target domain.Member) error

type Change struct {
	Room           uint64
	Action         domain.OwnerAction
	Caller, Target string
	Role           domain.Role
	RequestID      string
	At             time.Time
	Authorize      Authorize
}

type Result struct {
	Target, Successor domain.Member
	Changed           bool
}

type Coordinator struct {
	st Store
}

func New(st Store) (*Coordinator, error) {
	if st == nil {
		return nil, errNoStore
	}
	return &Coordinator{st: st}, nil
}

func (c *Coordinator) Apply(ctx context.Context, ch Change) (Result, error) {
	for range MaxTries {
		state, err := c.st.OwnerState(ctx, ch.Room)
		if err != nil {
			return Result{}, err
		}
		if state.Pending != nil {
			if _, err := c.finish(ctx, ch.Room, state.Ver, *state.Pending); err != nil && !errors.Is(err, domain.ErrRetryLater) {
				return Result{}, err
			}
			continue
		}
		target, change, err := c.prepare(ctx, ch)
		if err != nil || change == nil {
			return Result{Target: target}, err
		}
		ok, err := c.st.BeginOwnerChange(ctx, ch.Room, state.Ver, *change)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			continue
		}
		res, err := c.finish(ctx, ch.Room, state.Ver+1, *change)
		if !errors.Is(err, domain.ErrRetryLater) {
			return res, err
		}
	}
	return Result{}, domain.ErrRetryLater
}

func (c *Coordinator) Guard(ctx context.Context, room uint64, at time.Time) (bool, error) {
	repaired := false
	for range MaxTries {
		state, err := c.st.OwnerState(ctx, room)
		if err != nil {
			return repaired, err
		}
		if state.Pending != nil {
			if _, err := c.finish(ctx, room, state.Ver, *state.Pending); err != nil && !errors.Is(err, domain.ErrRetryLater) {
				return repaired, err
			}
			repaired = true
			continue
		}
		change, err := c.repair(ctx, room, state.Ver, at)
		if err != nil || change == nil {
			return repaired, err
		}
		ok, err := c.st.BeginOwnerChange(ctx, room, state.Ver, *change)
		if err != nil {
			return repaired, err
		}
		if !ok {
			continue
		}
		_, err = c.finish(ctx, room, state.Ver+1, *change)
		switch {
		case err == nil:
			return true, nil
		case !errors.Is(err, domain.ErrRetryLater):
			return repaired, err
		}
		repaired = true
	}
	return repaired, domain.ErrRetryLater
}

func (c *Coordinator) repair(ctx context.Context, room, ver uint64, at time.Time) (*domain.OwnerChange, error) {
	owners, err := c.st.Owners(ctx, room, 1)
	if err != nil || len(owners) > 0 {
		return nil, err
	}
	heir, ok, err := c.st.Successor(ctx, room)
	if err != nil || !ok {
		return nil, err
	}
	return &domain.OwnerChange{
		Action: domain.OwnerRepair, Successor: heir.User, SuccessorVer: heir.Ver,
		RequestID: "owners-v" + strconv.FormatUint(ver+1, 10), UpdatedAt: at,
	}, nil
}
```

`apps/core/internal/ownership/plan.go`:

```go
package ownership

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func (c *Coordinator) prepare(ctx context.Context, ch Change) (domain.Member, *domain.OwnerChange, error) {
	caller, target, err := c.parties(ctx, ch)
	if err != nil {
		return domain.Member{}, nil, err
	}
	if ch.Authorize != nil {
		if err := ch.Authorize(ctx, caller, target); err != nil {
			return domain.Member{}, nil, err
		}
	}
	if !target.Active() || (ch.Action == domain.OwnerChangeRole && target.Role == ch.Role) {
		return target, nil, nil
	}
	change := &domain.OwnerChange{
		Action: ch.Action, User: target.User, UserVer: target.Ver, RequestID: ch.RequestID, UpdatedBy: ch.Caller, UpdatedAt: ch.At,
	}
	if ch.Action == domain.OwnerChangeRole {
		change.Role = ch.Role
	}
	if target.Role != domain.RoleOwner || change.Role == domain.RoleOwner {
		return target, change, nil
	}
	owners, err := c.st.Owners(ctx, ch.Room, 2)
	if err != nil || len(owners) > 1 {
		return target, change, err
	}
	if ch.Action == domain.OwnerChangeRole {
		return domain.Member{}, nil, domain.ErrLastOwner
	}
	heir, ok, err := c.st.Successor(ctx, ch.Room)
	if err != nil {
		return domain.Member{}, nil, err
	}
	if ok {
		change.Successor, change.SuccessorVer = heir.User, heir.Ver
	}
	return target, change, nil
}

func (c *Coordinator) parties(ctx context.Context, ch Change) (domain.Member, domain.Member, error) {
	users := []string{ch.Caller}
	if ch.Target != ch.Caller {
		users = append(users, ch.Target)
	}
	docs, err := c.st.MembersOf(ctx, ch.Room, users)
	if err != nil {
		return domain.Member{}, domain.Member{}, err
	}
	var caller, target domain.Member
	for _, d := range docs {
		if d.User == ch.Caller {
			caller = d
		}
		if d.User == ch.Target {
			target = d
		}
	}
	if !caller.Active() {
		return domain.Member{}, domain.Member{}, domain.ErrNotMember
	}
	return caller, target, nil
}
```

`apps/core/internal/ownership/finish.go`:

```go
package ownership

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

var errMoved = errors.New("member doc moved past the owner change")

func (c *Coordinator) finish(ctx context.Context, room, ver uint64, p domain.OwnerChange) (Result, error) {
	var res Result
	if p.Successor != "" {
		heir, err := c.step(ctx, room, p.Successor, p.SuccessorVer, p.RequestID, func(m domain.Member) domain.Member {
			return m.Next(domain.RoleOwner, domain.MemberActive, p.RequestID, p.UpdatedBy, p.UpdatedAt)
		})
		if err != nil {
			return Result{}, c.abandon(ctx, room, ver, err)
		}
		res.Successor = heir
	}
	if p.Action != domain.OwnerRepair {
		target, err := c.step(ctx, room, p.User, p.UserVer, p.RequestID, func(m domain.Member) domain.Member {
			if p.Action == domain.OwnerChangeRole {
				return m.Next(p.Role, domain.MemberActive, p.RequestID, p.UpdatedBy, p.UpdatedAt)
			}
			return m.Next(m.Role, domain.MemberRemoved, p.RequestID, p.UpdatedBy, p.UpdatedAt)
		})
		if err != nil {
			return Result{}, c.abandon(ctx, room, ver, err)
		}
		res.Target = target
	}
	if _, err := c.st.EndOwnerChange(ctx, room, ver); err != nil {
		return Result{}, err
	}
	res.Changed = true
	return res, nil
}

func (c *Coordinator) step(ctx context.Context, room uint64, user string, ver uint32, requestID string, next func(domain.Member) domain.Member) (domain.Member, error) {
	doc, err := c.one(ctx, room, user)
	if err != nil {
		return domain.Member{}, err
	}
	if doc.Ver == ver {
		want := next(doc)
		ok, err := c.st.ApplyMember(ctx, doc, want)
		if err != nil {
			return domain.Member{}, err
		}
		if ok {
			return want, nil
		}
		if doc, err = c.one(ctx, room, user); err != nil {
			return domain.Member{}, err
		}
	}
	if doc.Ver == ver+1 && doc.RequestID == requestID {
		return doc, nil
	}
	return domain.Member{}, errMoved
}

func (c *Coordinator) one(ctx context.Context, room uint64, user string) (domain.Member, error) {
	docs, err := c.st.MembersOf(ctx, room, []string{user})
	if err != nil {
		return domain.Member{}, err
	}
	if len(docs) != 1 {
		return domain.Member{}, errMoved
	}
	return docs[0], nil
}

func (c *Coordinator) abandon(ctx context.Context, room, ver uint64, cause error) error {
	if !errors.Is(cause, errMoved) {
		return cause
	}
	if _, err := c.st.EndOwnerChange(ctx, room, ver); err != nil {
		return err
	}
	return domain.ErrRetryLater
}
```

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/ownership/..."
make vet
```

Expected: PASS 5 lần, goleak sạch. `wc -l apps/core/internal/ownership/*.go`: `coordinator.go` 134, `plan.go` 70, `finish.go` 85, `helpers_test.go` 162, `apply_test.go` 131, `race_test.go` 92, `crash_test.go` 82, `guard_test.go` 61.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/ownership", "after": "apps/core/internal/pinproj", "row": ["package", "Owner-affecting member changes under the rooms.owners_ver CAS (D100): Apply re-reads caller and target each try (caller inactive -> ErrNotMember, Authorize callback with fresh docs), treats an inactive target or the same role as a no-op, refuses the last owner stepping down (ErrLastOwner), picks the successor (earliest admin, then earliest member, ties by user id) when the last owner leaves or is removed, CASes pending_owner_change, then finish promotes the successor first, removes or re-roles the target and clears the pending change, every step conditional on the ver recorded in the pending change; a pending change found by any caller is finished first; up to MaxTries (3) then ErrRetryLater. Guard (owner_guard effect) finishes a stale pending change or promotes a successor in a group without an owner (request owners-v{k+1}, no actor) (D108 OW1)", "Store;Coordinator;New;Coordinator.Apply;Coordinator.Guard;Change;Result;Authorize;MaxTries", "apps/core/internal/mutate;apps/core/internal/effects;apps/core", "unit (memstore);goleak;races (two owners leaving or removing each other, 24 rooms x 2 goroutines);crash between the CAS and the writes", "D100;D108"]}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/ownership/
git commit -m "feat(ownership): owner changes under a room cas with successor first" -- apps/core/internal/ownership/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 9 file (8 trong `ownership`, `INDEXES.csv`).

Task rủi ro: một reviewer (thứ tự thăng kế nhiệm → hạ/xoá đích → End; mỗi bước CAS theo `ver` ghi trong pending và nhận ra bước đã áp qua `RequestID`; End theo đúng `owners_ver`; caller/đích đọc lại mỗi lượt; `Guard` không sửa group rỗng; tối đa `-count=3` trên `ownership`).

---

### Task 12: ★ `mutate`: AddMembers (request dedupe), RemoveMember, LeaveRoom, ChangeMemberRole, `Limits.MemberBatch`, Deps + `serviceDeps`

Bốn lệnh member trên lớp tập (D98–D101). Lệnh là **trạng thái mong muốn**: đúng sẵn thì thành công, không ghi, không event.

**`AddMembers(c)`** (`members.go`):
1. `ValidCID(RequestID)` (rỗng hoặc sai → `INVALID_ARGUMENT: request id`); users: `ValidUser` từng người, khử trùng giữ thứ tự, rỗng → `invalid users`, quá `Limits.MemberBatch` người khác nhau → `domain.ErrTooManyMembers`.
2. `Admit(add_members)` → room DM → `domain.ErrDirectRoom` → `Allow` (owner/admin).
3. `Requests.Begin(dedupe.RequestKey(room, caller, request_id))`: `RequestBusy` → `domain.ErrRetryLater`; `RequestDone` → `MembersOf(room, users)` lọc `domain.AddedBy(doc, request_id, caller)` → trả (không ghi, không event; người đã bị xoá sau lần đầu không còn `active` nên không được trả và **không được thêm lại**); `RequestNew` → bước 4.
4. `Messages.Last(room, 0)` **một lần** → `Members.AddMembers(Join{Room, Tenant, RequestID, By: caller, At: now, ReadSeq: last}, users)` (lỗi → `Requests.Cancel`, trả lỗi) → `Forget.ForgetMembers(room)` → lọc `AddedBy` → **một** `Events.Enqueue` gồm `pbconv.MemberEvents(room.Type, doc)` của từng người được thêm (bản room + bản user; lỗi bỏ qua) → `Requests.Finish(key, Record{Seq: số user, CreatedAt: now})` → trả doc đã thêm.

**`RemoveMember`, `LeaveRoom`, `ChangeMemberRole`** (`member_change.go`, `member_paths.go`):
1. Validate: `RemoveMember` đích hợp lệ và khác caller (chính mình → `INVALID_ARGUMENT`, dùng `LeaveRoom`); `ChangeMemberRole` đích hợp lệ, `ParseRole`.
2. `Admit(action)`. `LeaveRoom` gặp `ErrNotMember` (tenant đã đúng vì `Admit` kiểm tenant trước) → `MembersOf` có tombstone → `{Member: doc, Changed: false}`; không có doc → `ErrNotMember`. Room DM → `ErrDirectRoom`.
3. Một `NewRequestID()` cho cả lệnh (nil → 16 byte ngẫu nhiên dạng hex), một `now`.
4. Tối đa `MemberTries` (3) lượt: đọc caller + đích bằng một `MembersOf` → caller không active → `ErrNotMember`; đích chưa từng có doc (`Ver == 0`) → `domain.ErrMemberNotFound`; đổi role của tombstone → `ErrMemberNotFound` (xoá một tombstone đi tiếp: policy được hỏi trước rồi mới là no-op, nên một member thường không dò được ai từng ở trong group) → `Allow(Request{Member: caller, Target: doc, Role})` → đúng sẵn (đích không active cho rời/xoá, cùng role cho đổi role) → `{Member: doc, Changed: false, PreviousRole: doc.Role}` → đích là owner hoặc role mới là owner → **đường owner** → không thì `ApplyMember(doc, doc.Next(role', state', rid, caller, now))`; trượt (doc vừa đổi) → lượt sau đọc lại **cả caller** và đích, hỏi lại policy.
5. Hết lượt → `ErrRetryLater`.

**Đường owner**: `Owners.Apply(ownership.Change{Room, Action (leave|remove|change_role), Caller, Target, Role, RequestID, At, Authorize: hàm gọi lại Allow với doc mới})`; `Changed` → `Forget` → enqueue `MemberEvents` của kế nhiệm (nếu có) rồi của đích → `MemberResult{Member: đích, Changed, Successor, PreviousRole}`.

Đồng thời: mọi ghi là CAS theo `ver` của doc đã đọc, nên (a) một admin bị hạ **trước** lượt đọc bị từ chối, (b) bị hạ **sau** lượt đọc nhưng trước lần ghi thì lần xoá vẫn rơi (khe kiểm-rồi-ghi đã được owner chấp nhận ở thiết kế, không transaction), (c) đích vừa được thăng owner chuyển sang đường owner, (d) hai owner xoá nhau: bên thua thấy mình bị xoá → `ErrNotMember`. Test giả lập bằng wrapper `racingMembers` (chạy "đối thủ" ngay trước `ApplyMember`) và `racingOwners` (ngay trước `BeginOwnerChange`). Dedupe lệnh dùng `dedupe.Requests` thật trên một registry trong bộ nhớ có ngữ nghĩa Redis (`memRegistry`: pending/committed/abort), nên test được cả "core khởi động lại" (Requests mới, cùng registry).

`Limits.MemberBatch` (`MEMBER_BATCH_MAX`, Task 15): `cmp.Or(…, DefaultMemberBatch)` (500), hợp lệ `MinMemberBatch` (2; 1 sẽ chặn mọi DM) .. `domain.MaxMemberBatch` (1000). `Mutator.MemberBatch()` trả giá trị đã điền (Task 13 dùng cho `CreateRoom`).

Test member tạo room riêng `crew` (5151) bằng `domain.NewRoom` để doc member có đủ field (`state`, `ver`, `request_id`); room `room` (4242) của rig cũ không dùng cho member.

`mutate.New` bắt buộc thêm `Members`, `Owners`, `Requests`, `Forget`, nên task này sửa rig `mutate`, `TestNewRequiresEveryDependency`, rig `grpcsrv` (`fake_dependencies_test.go`: router của rig làm `Forget` khi có, không thì `nopForgetter`; `dedupe.NewRequests(acceptAllCIDs{}, time.Minute)`), và `apps/core/service_wiring.go` chuyển sang `wireService(serviceDeps)` (hợp đồng). `grpcsrv/harness_test.go` (185 dòng) không đổi.

**Files:**
- Modify: `apps/core/internal/mutate/limits.go`, `mutator.go`
- Create: `apps/core/internal/mutate/members.go`, `member_change.go`, `member_paths.go`
- Modify: `apps/core/internal/mutate/fixtures_test.go`, `delete_test.go`, `limits_test.go`
- Create: `apps/core/internal/mutate/member_helpers_test.go`, `members_test.go`, `member_change_test.go`, `member_rules_test.go`, `member_race_test.go`
- Modify: `apps/core/internal/grpcsrv/fake_dependencies_test.go`
- Modify: `apps/core/service_wiring.go`, `apps/core/wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Rig và test cũ**

`apps/core/internal/mutate/fixtures_test.go`:
- import thêm `".../internal/dedupe"` (sau `".../internal/counter"`) và `".../internal/ownership"` (sau `".../internal/mutate"`);
- trong `type rig struct`, sau dòng `events    *recordingEvents` thêm:

```go
	forgets   *forgetSpy
	registry  *memRegistry
```

- trong `newRig`, literal `rg := &rig{...}`: ngay sau dòng kết thúc bằng `events: &recordingEvents{},` thêm dòng `forgets: &forgetSpy{}, registry: newMemRegistry(),`;
- trong `deps`, ngay trước `return mutate.Deps{` thêm:

```go
	owners, err := ownership.New(rg.rooms)
	if err != nil {
		t.Fatalf("ownership.New: %v", err)
	}
	requests, err := dedupe.NewRequests(rg.registry, time.Minute)
	if err != nil {
		t.Fatalf("dedupe.NewRequests: %v", err)
	}
```

- và ngay sau dòng `return mutate.Deps{` thêm dòng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: rg.forgets,`.

`apps/core/internal/mutate/delete_test.go`, trong map của `TestNewRequiresEveryDependency`, sau dòng `"no emojis": ...` thêm:

```go
		"no members":    func(d *mutate.Deps) { d.Members = nil },
		"no owners":     func(d *mutate.Deps) { d.Owners = nil },
		"no requests":   func(d *mutate.Deps) { d.Requests = nil },
		"no forgetter":  func(d *mutate.Deps) { d.Forget = nil },
		"member batch":  func(d *mutate.Deps) { d.Limits = mutate.Limits{MemberBatch: domain.MaxMemberBatch + 1} },
```

(file đã import `domain`.)

`apps/core/internal/mutate/limits_test.go`: import thêm `".../internal/domain"`; trong bảng `cases` của `TestLimitsFillDefaultsAndCheckBounds`, sau dòng `{"negative pin limit", ...}` thêm:

```go
		{"member batch at the cap", mutate.Limits{MemberBatch: domain.MaxMemberBatch}, ""},
		{"member batch at the floor", mutate.Limits{MemberBatch: mutate.MinMemberBatch}, ""},
		{"member batch over the cap", mutate.Limits{MemberBatch: domain.MaxMemberBatch + 1}, "member batch 1001"},
		{"member batch below a direct room", mutate.Limits{MemberBatch: 1}, "member batch 1"},
		{"negative member batch", mutate.Limits{MemberBatch: -1}, "member batch -1"},
```

và cuối file thêm:

```go
func TestMemberBatchDefaultsTo500(t *testing.T) {
	rg := newRig(t, nil)
	if got := rg.m.MemberBatch(); got != 500 || mutate.DefaultMemberBatch != 500 || mutate.MinMemberBatch != 2 || mutate.MemberTries != 3 {
		t.Fatalf("MemberBatch() = %d with default %d, floor %d, tries %d; want 500, 500, 2, 3",
			got, mutate.DefaultMemberBatch, mutate.MinMemberBatch, mutate.MemberTries)
	}
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{MemberBatch: 7}
	if got := rg.build(t, d).MemberBatch(); got != 7 {
		t.Fatalf("MemberBatch() = %d, want the configured 7", got)
	}
}
```

**Step 2: Test lệnh member**

`apps/core/internal/mutate/member_helpers_test.go`:

```go
package mutate_test

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const crew uint64 = 5151

type forgetSpy struct {
	mu    sync.Mutex
	rooms []uint64
}

func (f *forgetSpy) ForgetMembers(room uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rooms = append(f.rooms, room)
}

func (f *forgetSpy) calls() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rooms)
}

type memRegistry struct {
	mu   sync.Mutex
	vals map[string]dedupe.Verdict
}

func newMemRegistry() *memRegistry { return &memRegistry{vals: map[string]dedupe.Verdict{}} }

func (r *memRegistry) Reserve(_ context.Context, keys []dedupe.Key) ([]dedupe.Verdict, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]dedupe.Verdict, len(keys))
	for i, k := range keys {
		v, ok := r.vals[k.String()]
		switch {
		case !ok:
			r.vals[k.String()] = dedupe.Verdict{Status: dedupe.PendingHere}
			out[i] = dedupe.Verdict{Status: dedupe.Reserved}
		default:
			out[i] = v
		}
	}
	return out, nil
}

func (r *memRegistry) Commit(_ context.Context, entries []dedupe.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range entries {
		r.vals[e.Key.String()] = dedupe.Verdict{Status: dedupe.Committed, Record: e.Record}
	}
	return nil
}

func (r *memRegistry) Abort(_ context.Context, keys []dedupe.Key) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range keys {
		if r.vals[k.String()].Status == dedupe.PendingHere {
			delete(r.vals, k.String())
		}
	}
	return nil
}

func (r *memRegistry) state(k dedupe.Key) dedupe.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.vals[k.String()]
}

func (rg *rig) crew(t *testing.T, users ...string) {
	t.Helper()
	r, members, err := domain.NewRoom(tenant, users[0], domain.RoomGroup, "crew", users, created, crew)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create crew: %v", err)
	}
}

func (rg *rig) post(t *testing.T, id, seq uint64) {
	t.Helper()
	m := domain.Message{Room: id, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "m", CID: "p-" + strconv.FormatUint(seq, 10), CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
}

func (rg *rig) doc(t *testing.T, user string) (domain.Member, bool) {
	t.Helper()
	got, err := rg.rooms.MembersOf(t.Context(), crew, []string{user})
	if err != nil {
		t.Fatalf("MembersOf(%s): %v", user, err)
	}
	if len(got) == 0 {
		return domain.Member{}, false
	}
	return got[0], true
}

func (rg *rig) expectMember(t *testing.T, user string, role domain.Role, active bool) domain.Member {
	t.Helper()
	d, ok := rg.doc(t, user)
	if !ok || d.Role != role || d.Active() != active {
		t.Fatalf("%s = %+v (found %v), want role %s active %v", user, d, ok, role, active)
	}
	return d
}

func addCmd(by, rid string, users ...string) mutate.AddMembersCmd {
	return mutate.AddMembersCmd{Tenant: tenant, User: by, Room: crew, Users: users, RequestID: rid}
}

func removeCmd(by, target string) mutate.RemoveMemberCmd {
	return mutate.RemoveMemberCmd{Tenant: tenant, User: by, Room: crew, Target: target}
}

func leaveCmd(by string) mutate.LeaveRoomCmd {
	return mutate.LeaveRoomCmd{Tenant: tenant, User: by, Room: crew}
}

func roleCmd(by, target string, role domain.Role) mutate.ChangeRoleCmd {
	return mutate.ChangeRoleCmd{Tenant: tenant, User: by, Room: crew, Target: target, Role: role}
}

func (rg *rig) setRole(t *testing.T, by, target string, role domain.Role) mutate.MemberResult {
	t.Helper()
	res, err := rg.m.ChangeMemberRole(t.Context(), roleCmd(by, target, role))
	if err != nil {
		t.Fatalf("%s sets %s to %s: %v", by, target, role, err)
	}
	return res
}

func (rg *rig) eventsFor(id uint64) []*chatimv1.Event {
	rooms, events := rg.events.list()
	var out []*chatimv1.Event
	for i, ev := range events {
		if rooms[i] == id {
			out = append(out, ev)
		}
	}
	return out
}

func sameEvents(got, want []*chatimv1.Event) bool {
	return slices.EqualFunc(got, want, func(a, b *chatimv1.Event) bool { return proto.Equal(a, b) })
}

func users(docs []domain.Member) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.User
	}
	return out
}
```

`apps/core/internal/mutate/members_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type failingMembers struct {
	mutate.MemberStore
	err error
}

func (f failingMembers) AddMembers(context.Context, domain.Join, []string) ([]domain.Member, error) {
	return nil, f.err
}

func (rg *rig) add(t *testing.T, by, rid string, users ...string) []domain.Member {
	t.Helper()
	got, err := rg.m.AddMembers(t.Context(), addCmd(by, rid, users...))
	if err != nil {
		t.Fatalf("%s adds %v (%s): %v", by, users, rid, err)
	}
	return got
}

func TestAddMembersWritesForgetsAnnouncesAndCommitsTheRequest(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	rg.post(t, crew, 1)
	rg.post(t, crew, 2)
	got := rg.add(t, "alice", "rq-1", "dave", "erin", "dave", "bob")
	if !slices.Equal(users(got), []string{"dave", "erin"}) {
		t.Fatalf("added %v, want dave and erin (bob is already in)", users(got))
	}
	dave := rg.expectMember(t, "dave", domain.RoleMember, true)
	if dave.Ver != 1 || dave.ReadSeq != 2 || dave.ReadVer != 1 || dave.RequestID != "rq-1" || dave.UpdatedBy != "alice" || !dave.JoinedAt.Equal(rg.at()) {
		t.Fatalf("dave = %+v, want ver 1, read up to seq 2, request rq-1 by alice at %v", dave, rg.at())
	}
	if bob := rg.expectMember(t, "bob", domain.RoleMember, true); bob.Ver != 1 {
		t.Fatalf("bob = %+v, want his doc untouched", bob)
	}
	if !slices.Equal(rg.forgets.calls(), []uint64{crew}) {
		t.Fatalf("forgot %v, want [%d]", rg.forgets.calls(), crew)
	}
	want := append(pbconv.MemberEvents(domain.RoomGroup, got[0]), pbconv.MemberEvents(domain.RoomGroup, got[1])...)
	if events := rg.eventsFor(crew); !sameEvents(events, want) || len(events) != 4 {
		t.Fatalf("events = %v, want a room copy and a user copy for dave and erin", events)
	}
	if v := rg.registry.state(dedupe.RequestKey(crew, "alice", "rq-1")); v.Status != dedupe.Committed || v.Record.Seq != 3 || !v.Record.CreatedAt.Equal(rg.at()) {
		t.Fatalf("request record = %+v, want committed with 3 users at %v", v, rg.at())
	}
}

func TestAddingActiveMembersChangesNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	if got := rg.add(t, "alice", "rq-1", "bob", "alice"); len(got) != 0 {
		t.Fatalf("added %v, want nobody", users(got))
	}
	if events := rg.eventsFor(crew); len(events) != 0 {
		t.Fatalf("a no-op add enqueued %v", events)
	}
	rg.expectMember(t, "bob", domain.RoleMember, true)
}

func TestReAddingRestoresAccessAsAMemberAndRaisesTheReadPosition(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "bob")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	for seq := uint64(1); seq <= 3; seq++ {
		rg.post(t, crew, seq)
	}
	rg.now = rg.now.Add(time.Hour)
	got := rg.add(t, "alice", "rq-2", "bob")
	if len(got) != 1 {
		t.Fatalf("added %v, want bob back", users(got))
	}
	bob := got[0]
	if bob.Role != domain.RoleMember || bob.PreviousRole != domain.RoleAdmin || bob.PreviousState != domain.MemberRemoved ||
		bob.Ver != 4 || bob.ReadSeq != 3 || bob.ReadVer != 1 || !bob.JoinedAt.Equal(rg.at()) {
		t.Fatalf("bob = %+v, want a member again at ver 4 who has read seq 3", bob)
	}
	if _, err := rg.rooms.Member(t.Context(), crew, "bob"); err != nil {
		t.Fatalf("Member(bob) after re-add: %v", err)
	}
}

func TestARetryWithTheSameRequestNeverReAddsSomeoneRemovedSince(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	first := rg.add(t, "alice", "rq-1", "dave", "erin")
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "dave")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	before := len(rg.eventsFor(crew))
	restarted := rg.build(t, rg.deps(t, nil))
	for name, m := range map[string]*mutate.Mutator{"same core": rg.m, "restarted core": restarted} {
		got, err := m.AddMembers(t.Context(), addCmd("alice", "rq-1", "dave", "erin"))
		if err != nil || !slices.Equal(users(got), []string{"erin"}) {
			t.Fatalf("%s retry = %v, %v; want only erin (still added by rq-1)", name, users(got), err)
		}
	}
	if dave := rg.expectMember(t, "dave", domain.RoleMember, false); dave.Ver != first[0].Ver+1 {
		t.Fatalf("dave = %+v, want him still removed", dave)
	}
	if after := len(rg.eventsFor(crew)); after != before {
		t.Fatalf("retries enqueued %d events, want none", after-before)
	}
}

func TestARequestStillRunningElsewhereIsRetryLater(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	key := dedupe.RequestKey(crew, "alice", "rq-1")
	if _, err := rg.registry.Reserve(t.Context(), []dedupe.Key{key}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if _, err := rg.m.AddMembers(t.Context(), addCmd("alice", "rq-1", "dave")); !errors.Is(err, domain.ErrRetryLater) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("AddMembers while the request is pending = %v, want ErrRetryLater", err)
	}
	if _, ok := rg.doc(t, "dave"); ok {
		t.Fatal("dave added while the same request was still running")
	}
}

func TestAFailedAddCancelsTheRequestAndEventFailuresAreIgnored(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	d := rg.deps(t, nil)
	d.Members = failingMembers{MemberStore: rg.rooms, err: errBoom}
	if _, err := rg.build(t, d).AddMembers(t.Context(), addCmd("alice", "rq-1", "dave")); !errors.Is(err, errBoom) {
		t.Fatalf("AddMembers with a failing store = %v, want errBoom", err)
	}
	if v := rg.registry.state(dedupe.RequestKey(crew, "alice", "rq-1")); v.Status != dedupe.Absent {
		t.Fatalf("request after a failed add = %+v, want it released", v)
	}
	rg.events.err = errBoom
	if got := rg.add(t, "alice", "rq-1", "dave"); len(got) != 1 {
		t.Fatalf("added %v with refused events, want dave", users(got))
	}
}
```

`apps/core/internal/mutate/member_change_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (rg *rig) fixedRequestIDs(t *testing.T, id string) {
	t.Helper()
	d := rg.deps(t, nil)
	d.NewRequestID = func() string { return id }
	rg.m = rg.build(t, d)
}

func TestRemoveMemberLeavesATombstoneAndRepeatsAsANoOp(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.fixedRequestIDs(t, "rid-1")
	res, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "bob"))
	if err != nil || !res.Changed || res.PreviousRole != domain.RoleMember {
		t.Fatalf("RemoveMember = %+v, %v; want bob removed", res, err)
	}
	bob := rg.expectMember(t, "bob", domain.RoleMember, false)
	if bob.Ver != 2 || bob.PreviousState != domain.MemberActive || bob.RequestID != "rid-1" || bob.UpdatedBy != "alice" || !bob.UpdatedAt.Equal(rg.at()) {
		t.Fatalf("bob = %+v, want ver 2 removed by alice with request rid-1", bob)
	}
	if _, err := rg.rooms.Member(t.Context(), crew, "bob"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("Member(bob) = %v, want ErrNotMember", err)
	}
	events := rg.eventsFor(crew)
	if !sameEvents(events, pbconv.MemberEvents(domain.RoomGroup, bob)) || events[0].GetMemberRemoved().GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED {
		t.Fatalf("events = %v, want bob's member_removed (removed) for the room and for him", events)
	}
	if again, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "bob")); err != nil || again.Changed {
		t.Fatalf("removing bob again = %+v, %v; want an unchanged success", again, err)
	}
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "zed")); !errors.Is(err, domain.ErrMemberNotFound) {
		t.Fatalf("removing a stranger = %v, want ErrMemberNotFound", err)
	}
	if n := len(rg.eventsFor(crew)); n != 2 || !slices.Equal(rg.forgets.calls(), []uint64{crew}) {
		t.Fatalf("%d events and forgets %v, want only the first removal announced", n, rg.forgets.calls())
	}
}

func TestLeaveRoomIsDesiredState(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	res, err := rg.m.LeaveRoom(t.Context(), leaveCmd("carol"))
	if err != nil || !res.Changed || res.Successor != "" || res.Member.UpdatedBy != "carol" {
		t.Fatalf("LeaveRoom = %+v, %v; want carol gone by herself", res, err)
	}
	if ev := rg.eventsFor(crew); len(ev) != 2 || ev[0].GetMemberRemoved().GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT {
		t.Fatalf("events = %v, want carol's member_removed (left)", ev)
	}
	if again, err := rg.m.LeaveRoom(t.Context(), leaveCmd("carol")); err != nil || again.Changed || again.Member.Ver != 2 {
		t.Fatalf("leaving again = %+v, %v; want an unchanged success", again, err)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leaveCmd("mallory")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("a stranger leaves = %v, want ErrNotMember", err)
	}
}

func TestChangeMemberRoleIsDesiredState(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	res := rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	if !res.Changed || res.PreviousRole != domain.RoleMember || res.Member.Role != domain.RoleAdmin || res.Member.Ver != 2 {
		t.Fatalf("ChangeMemberRole = %+v, want bob made admin from member at ver 2", res)
	}
	if again := rg.setRole(t, "alice", "bob", domain.RoleAdmin); again.Changed || again.PreviousRole != domain.RoleAdmin {
		t.Fatalf("same role again = %+v, want unchanged with the current role", again)
	}
	if _, err := rg.m.LeaveRoom(t.Context(), leaveCmd("carol")); err != nil {
		t.Fatalf("LeaveRoom: %v", err)
	}
	for _, target := range []string{"zed", "carol"} {
		if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", target, domain.RoleAdmin)); !errors.Is(err, domain.ErrMemberNotFound) || !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("role of %s = %v, want ErrMemberNotFound", target, err)
		}
	}
}

func TestOwnerChangesGoThroughTheOwnerPath(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.setRole(t, "alice", "carol", domain.RoleAdmin)
	if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", "alice", domain.RoleMember)); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("the last owner steps down = %v, want ErrLastOwner", err)
	}
	before := len(rg.eventsFor(crew))
	res, err := rg.m.LeaveRoom(t.Context(), leaveCmd("alice"))
	if err != nil || !res.Changed || res.Successor != "carol" {
		t.Fatalf("the last owner leaves = %+v, %v; want carol (the only admin) as the new owner", res, err)
	}
	carol := rg.expectMember(t, "carol", domain.RoleOwner, true)
	alice := rg.expectMember(t, "alice", domain.RoleOwner, false)
	want := append(pbconv.MemberEvents(domain.RoomGroup, carol), pbconv.MemberEvents(domain.RoomGroup, alice)...)
	if got := rg.eventsFor(crew)[before:]; !sameEvents(got, want) {
		t.Fatalf("events = %v, want carol's promotion then alice's leave", got)
	}
	promoted := rg.setRole(t, "carol", "bob", domain.RoleOwner)
	if !promoted.Changed || promoted.PreviousRole != domain.RoleMember {
		t.Fatalf("promote bob = %+v, want owner from member", promoted)
	}
	if st, err := rg.rooms.OwnerState(t.Context(), crew); err != nil || st.Ver != 2 || st.Pending != nil {
		t.Fatalf("owner state = %+v, %v; want two finished owner changes", st, err)
	}
	if res, err := rg.m.RemoveMember(t.Context(), removeCmd("bob", "carol")); err != nil || !res.Changed || res.Successor != "" {
		t.Fatalf("owner bob removes owner carol = %+v, %v; want carol gone without a successor", res, err)
	}
	rg.expectMember(t, "bob", domain.RoleOwner, true)
	if res, err := rg.m.LeaveRoom(t.Context(), mutate.LeaveRoomCmd{Tenant: tenant, User: "bob", Room: crew}); err != nil || res.Successor != "" {
		t.Fatalf("the only member leaves = %+v, %v; want an empty group", res, err)
	}
}
```

`apps/core/internal/mutate/member_rules_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMemberCommandsFollowTheDefaultPolicy(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol", "dave", "erin")
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	rg.setRole(t, "alice", "dave", domain.RoleAdmin)
	ctx := t.Context()
	denied := map[string]func() error{
		"member adds": func() error { _, err := rg.m.AddMembers(ctx, addCmd("carol", "rq-1", "frank")); return err },
		"member removes a member": func() error {
			_, err := rg.m.RemoveMember(ctx, removeCmd("carol", "erin"))
			return err
		},
		"admin removes an admin": func() error { _, err := rg.m.RemoveMember(ctx, removeCmd("bob", "dave")); return err },
		"admin removes the owner": func() error {
			_, err := rg.m.RemoveMember(ctx, removeCmd("bob", "alice"))
			return err
		},
		"admin changes a role": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, roleCmd("bob", "carol", domain.RoleAdmin))
			return err
		},
		"admin makes itself owner": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, roleCmd("bob", "bob", domain.RoleOwner))
			return err
		},
	}
	for name, call := range denied {
		if err := call(); !errors.Is(err, access.ErrDenied) || !errors.Is(err, apperr.ErrPermissionDenied) {
			t.Fatalf("%s = %v, want ErrDenied", name, err)
		}
	}
	if got, err := rg.m.AddMembers(ctx, addCmd("bob", "rq-2", "frank")); err != nil || len(got) != 1 {
		t.Fatalf("admin adds = %v, %v; want frank", got, err)
	}
	if res, err := rg.m.RemoveMember(ctx, removeCmd("bob", "erin")); err != nil || !res.Changed {
		t.Fatalf("admin removes a member = %+v, %v", res, err)
	}
	if res, err := rg.m.RemoveMember(ctx, removeCmd("alice", "dave")); err != nil || !res.Changed {
		t.Fatalf("owner removes an admin = %+v, %v", res, err)
	}
	if _, err := rg.m.AddMembers(ctx, addCmd("mallory", "rq-3", "zed")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("a stranger adds = %v, want ErrNotMember", err)
	}
}

func TestMemberCommandsAskThePolicyWithCallerTargetAndRole(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.crew(t, "alice", "bob")
	if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", "bob", domain.RoleAdmin)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ChangeMemberRole = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 {
		t.Fatalf("policy asked %d times, want 1", len(asked))
	}
	r := asked[0]
	if r.Action != access.ChangeMemberRole || r.User != "alice" || r.Member.Role != domain.RoleOwner || r.Target.User != "bob" ||
		r.Target.Role != domain.RoleMember || r.Role != domain.RoleAdmin || r.Room.ID != crew {
		t.Fatalf("policy saw %+v, want alice (owner) asking to make member bob an admin", r)
	}
	if len(rg.eventsFor(crew)) != 0 {
		t.Fatal("a denied command enqueued events")
	}
}

func TestDirectRoomsKeepTheirTwoMembers(t *testing.T) {
	rg := newRig(t, nil)
	const dm uint64 = 77
	r, members, err := domain.NewRoom(tenant, "alice", domain.RoomDM, "", []string{"alice", "bob"}, created, dm)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	ctx := t.Context()
	calls := map[string]func() error{
		"add": func() error {
			_, err := rg.m.AddMembers(ctx, mutate.AddMembersCmd{Tenant: tenant, User: "alice", Room: dm, Users: []string{"carol"}, RequestID: "rq-1"})
			return err
		},
		"remove": func() error {
			_, err := rg.m.RemoveMember(ctx, mutate.RemoveMemberCmd{Tenant: tenant, User: "alice", Room: dm, Target: "bob"})
			return err
		},
		"leave": func() error {
			_, err := rg.m.LeaveRoom(ctx, mutate.LeaveRoomCmd{Tenant: tenant, User: "bob", Room: dm})
			return err
		},
		"role": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, mutate.ChangeRoleCmd{Tenant: tenant, User: "alice", Room: dm, Target: "bob", Role: domain.RoleAdmin})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, domain.ErrDirectRoom) || !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("%s on a dm = %v, want ErrDirectRoom", name, err)
		}
	}
}

func TestMemberCommandsRejectBadInputFirst(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{MemberBatch: 2}
	rg.m = rg.build(t, d)
	ctx := t.Context()
	elsewhere := addCmd("alice", "rq-1", "dave")
	elsewhere.Tenant = "other"
	unknown := addCmd("alice", "rq-1", "dave")
	unknown.Room = 9
	add := func(c mutate.AddMembersCmd) error { _, err := rg.m.AddMembers(ctx, c); return err }
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"no users", add(addCmd("alice", "rq-1")), apperr.ErrInvalidArgument},
		{"invalid user", add(addCmd("alice", "rq-1", "dave", "e e")), apperr.ErrInvalidArgument},
		{"over the batch", add(addCmd("alice", "rq-1", "dave", "erin", "frank")), domain.ErrTooManyMembers},
		{"no request id", add(addCmd("alice", "", "dave")), apperr.ErrInvalidArgument},
		{"bad request id", add(addCmd("alice", "r q", "dave")), apperr.ErrInvalidArgument},
		{"other tenant", add(elsewhere), domain.ErrRoomNotFound},
		{"unknown room", add(unknown), domain.ErrRoomNotFound},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s = %v, want %v", c.name, c.err, c.want)
		}
	}
	others := map[string]error{}
	_, others["remove yourself"] = rg.m.RemoveMember(ctx, removeCmd("alice", "alice"))
	_, others["remove an invalid user"] = rg.m.RemoveMember(ctx, removeCmd("alice", "z z"))
	_, others["unknown role"] = rg.m.ChangeMemberRole(ctx, roleCmd("alice", "bob", "boss"))
	_, others["empty role"] = rg.m.ChangeMemberRole(ctx, roleCmd("alice", "bob", ""))
	for name, err := range others {
		if !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	if got := rg.add(t, "alice", "rq-9", "dave", "erin", "dave"); len(got) != 2 || rg.m.MemberBatch() != 2 {
		t.Fatalf("two distinct users in a batch of 2 = %v, want both added", users(got))
	}
}

func TestABatchOfTheMaximumSizeIsOneWrite(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice")
	many := make([]string, mutate.DefaultMemberBatch)
	for i := range many {
		many[i] = "u" + strconv.Itoa(i)
	}
	if got := rg.add(t, "alice", "rq-1", many...); len(got) != mutate.DefaultMemberBatch {
		t.Fatalf("added %d, want %d", len(got), mutate.DefaultMemberBatch)
	}
	if _, err := rg.m.AddMembers(t.Context(), addCmd("alice", "rq-2", append(many, "one-more")...)); !errors.Is(err, domain.ErrTooManyMembers) {
		t.Fatalf("one user over the batch = %v, want ErrTooManyMembers", err)
	}
}
```

`apps/core/internal/mutate/member_race_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type racingMembers struct {
	mutate.MemberStore
	mu     sync.Mutex
	rivals []func()
	writes int
}

func (r *racingMembers) ApplyMember(ctx context.Context, cur, next domain.Member) (bool, error) {
	r.mu.Lock()
	r.writes++
	var rival func()
	if len(r.rivals) > 0 {
		rival, r.rivals = r.rivals[0], r.rivals[1:]
	}
	r.mu.Unlock()
	if rival != nil {
		rival()
	}
	return r.MemberStore.ApplyMember(ctx, cur, next)
}

type racingOwners struct {
	*memstore.Rooms
	rival func()
}

func (r *racingOwners) BeginOwnerChange(ctx context.Context, id, base uint64, c domain.OwnerChange) (bool, error) {
	if rival := r.rival; rival != nil {
		r.rival = nil
		rival()
	}
	return r.Rooms.BeginOwnerChange(ctx, id, base, c)
}

func (rg *rig) bump(t *testing.T, user string, role domain.Role) {
	t.Helper()
	cur, _ := rg.doc(t, user)
	if ok, err := rg.rooms.ApplyMember(t.Context(), cur, cur.Next(role, domain.MemberActive, "rival-"+user, "alice", created)); err != nil || !ok {
		t.Fatalf("rival change of %s = %v, %v", user, ok, err)
	}
}

func (rg *rig) withMembers(t *testing.T, rivals ...func()) *racingMembers {
	t.Helper()
	d := rg.deps(t, nil)
	race := &racingMembers{MemberStore: rg.rooms, rivals: rivals}
	d.Members = race
	rg.m = rg.build(t, d)
	return race
}

func TestAnAdminDemotedJustBeforeTheWriteStillRemoves(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	rg.withMembers(t, func() { rg.bump(t, "bob", domain.RoleMember) })
	res, err := rg.m.RemoveMember(t.Context(), removeCmd("bob", "carol"))
	if err != nil || !res.Changed {
		t.Fatalf("RemoveMember = %+v, %v; want the removal checked before the demotion to land", res, err)
	}
	rg.expectMember(t, "carol", domain.RoleMember, false)
	rg.expectMember(t, "bob", domain.RoleMember, true)
}

func TestAnAdminDemotedWhileRetryingIsDenied(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	race := rg.withMembers(t, func() {
		rg.bump(t, "bob", domain.RoleMember)
		rg.bump(t, "carol", domain.RoleMember)
	})
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("bob", "carol")); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("RemoveMember after the demotion = %v, want ErrDenied on the retry", err)
	}
	rg.expectMember(t, "carol", domain.RoleMember, true)
	if race.writes != 1 || len(rg.eventsFor(crew)) != 2 {
		t.Fatalf("%d writes and %d events, want one lost write and only the two copies of bob's earlier promotion", race.writes, len(rg.eventsFor(crew)))
	}
}

func TestATargetPromotedToOwnerMeanwhileMovesToTheOwnerPath(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.withMembers(t, func() { rg.bump(t, "carol", domain.RoleOwner) })
	res, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "carol"))
	if err != nil || !res.Changed {
		t.Fatalf("RemoveMember = %+v, %v; want carol removed through the owner path", res, err)
	}
	rg.expectMember(t, "carol", domain.RoleOwner, false)
	if st, err := rg.rooms.OwnerState(t.Context(), crew); err != nil || st.Ver != 1 {
		t.Fatalf("owner state = %+v, %v; want one owner change", st, err)
	}
}

func TestThePlainPathGivesUpAfterThreeLostWrites(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	bump := func() { rg.bump(t, "carol", domain.RoleMember) }
	race := rg.withMembers(t, bump, bump, bump)
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "carol")); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("RemoveMember = %v, want ErrRetryLater", err)
	}
	if race.writes != mutate.MemberTries {
		t.Fatalf("%d writes, want %d", race.writes, mutate.MemberTries)
	}
	rg.expectMember(t, "carol", domain.RoleMember, true)
	if len(rg.eventsFor(crew)) != 0 || len(rg.forgets.calls()) != 0 {
		t.Fatal("a refused command enqueued events or forgot members")
	}
}

func TestOwnersRemovingEachOtherLeaveOneOwner(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob", "carol")
	rg.setRole(t, "alice", "bob", domain.RoleOwner)
	race := &racingOwners{Rooms: rg.rooms}
	owners, err := ownership.New(race)
	if err != nil {
		t.Fatalf("ownership.New: %v", err)
	}
	d := rg.deps(t, nil)
	d.Owners = owners
	rg.m = rg.build(t, d)
	race.rival = func() {
		if _, err := rg.m.RemoveMember(t.Context(), removeCmd("bob", "alice")); err != nil {
			t.Errorf("bob removes alice: %v", err)
		}
	}
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "bob")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("alice removes bob after losing the race = %v, want ErrNotMember", err)
	}
	rg.expectMember(t, "bob", domain.RoleOwner, true)
	rg.expectMember(t, "alice", domain.RoleOwner, false)
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL build (dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm `undefined: mutate.MemberStore`, `undefined: mutate.AddMembersCmd`, `undefined: mutate.RemoveMemberCmd`, `undefined: mutate.LeaveRoomCmd`, `undefined: mutate.ChangeRoleCmd`, `undefined: mutate.MemberResult`, `d.Members undefined (type *mutate.Deps has no field or method Members)`, `d.Owners undefined …`, `d.Requests undefined …`.

**Step 4: Code `mutate`**

`apps/core/internal/mutate/limits.go`:
- khối `const`, sau `FastTouchTries  = 3` thêm (một dòng trống trước):

```go
	DefaultMemberBatch = 500
	MinMemberBatch     = 2
	MemberTries        = 3
```

- `type Limits struct`: sau `PinLimit int` thêm `MemberBatch int` (gofmt căn cột);
- `withDefaults`, trước `return l` thêm `l.MemberBatch = cmp.Or(l.MemberBatch, DefaultMemberBatch)`;
- `validate`, trước `return nil` cuối thêm:

```go
	if l.MemberBatch < MinMemberBatch || l.MemberBatch > domain.MaxMemberBatch {
		return fmt.Errorf("%w: member batch %d must be %d to %d", apperr.ErrInvalidArgument, l.MemberBatch, MinMemberBatch, domain.MaxMemberBatch)
	}
```

`apps/core/internal/mutate/mutator.go`:
- thay dòng `var errMissingDeps = ...` bằng:

```go
var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms, events, reactions, a counter, pins, a pin projector, members, an owner coordinator, request dedupe and a member forgetter", apperr.ErrInvalidArgument)
```

- cuối `type Deps struct` (sau `Now       func() time.Time`) thêm một dòng trống và:

```go
	Members      MemberStore
	Owners       OwnerCoordinator
	Requests     RequestDedupe
	Forget       MemberForgetter
	NewRequestID func() string
```

- trong `New`, điều kiện nil thêm hàng cuối `d.Members == nil || d.Owners == nil || d.Requests == nil || d.Forget == nil` (nối bằng `||` sau `d.Projector == nil`);
- trong `New`, sau khối `if d.Now == nil { d.Now = time.Now }` thêm:

```go
	if d.NewRequestID == nil {
		d.NewRequestID = randomRequestID
	}
```

`apps/core/internal/mutate/members.go`:

```go
package mutate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errNoUsers      = fmt.Errorf("%w: users", apperr.ErrInvalidArgument)
	errBadRequestID = fmt.Errorf("%w: request id", apperr.ErrInvalidArgument)
)

type MemberStore interface {
	store.MemberWriter
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
}

type OwnerCoordinator interface {
	Apply(ctx context.Context, ch ownership.Change) (ownership.Result, error)
}

type RequestDedupe interface {
	Begin(ctx context.Context, key dedupe.Key) (dedupe.RequestStatus, error)
	Finish(ctx context.Context, key dedupe.Key, rec dedupe.Record)
	Cancel(ctx context.Context, key dedupe.Key)
}

type MemberForgetter interface {
	ForgetMembers(room uint64)
}

type AddMembersCmd struct {
	Tenant, User string
	Room         uint64
	Users        []string
	RequestID    string
}

func (m *Mutator) MemberBatch() int { return m.d.Limits.MemberBatch }

func (m *Mutator) AddMembers(ctx context.Context, c AddMembersCmd) ([]domain.Member, error) {
	if domain.ValidCID(c.RequestID) != nil {
		return nil, errBadRequestID
	}
	users, err := m.distinctUsers(c.Users)
	if err != nil {
		return nil, err
	}
	req, err := m.d.Access.Admit(ctx, access.AddMembers, c.Tenant, c.User, c.Room)
	if err != nil {
		return nil, err
	}
	if req.Room.Type == domain.RoomDM {
		return nil, domain.ErrDirectRoom
	}
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return nil, err
	}
	key := dedupe.RequestKey(c.Room, c.User, c.RequestID)
	status, err := m.d.Requests.Begin(ctx, key)
	if err != nil {
		return nil, err
	}
	switch status {
	case dedupe.RequestBusy:
		return nil, domain.ErrRetryLater
	case dedupe.RequestDone:
		docs, err := m.d.Members.MembersOf(ctx, c.Room, users)
		return addedBy(docs, c), err
	default:
	}
	now := m.now()
	added, err := m.addNow(ctx, req.Room, c, users, now)
	if err != nil {
		m.d.Requests.Cancel(ctx, key)
		return nil, err
	}
	m.d.Requests.Finish(ctx, key, dedupe.Record{Seq: uint64(max(len(users), 1)), CreatedAt: now})
	return added, nil
}

func (m *Mutator) addNow(ctx context.Context, r domain.Room, c AddMembersCmd, users []string, now time.Time) ([]domain.Member, error) {
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return nil, err
	}
	j := domain.Join{Room: c.Room, Tenant: r.Tenant, RequestID: c.RequestID, By: c.User, At: now, ReadSeq: last}
	docs, err := m.d.Members.AddMembers(ctx, j, users)
	if err != nil {
		return nil, err
	}
	m.d.Forget.ForgetMembers(c.Room)
	added := addedBy(docs, c)
	m.announce(r.Type, added...)
	return added, nil
}

func (m *Mutator) announce(typ domain.RoomType, docs ...domain.Member) {
	var events []*chatimv1.Event
	for _, d := range docs {
		events = append(events, pbconv.MemberEvents(typ, d)...)
	}
	if len(events) > 0 {
		_ = m.d.Events.Enqueue(docs[0].Room, events)
	}
}

func addedBy(docs []domain.Member, c AddMembersCmd) []domain.Member {
	out := make([]domain.Member, 0, len(docs))
	for _, d := range docs {
		if domain.AddedBy(d, c.RequestID, c.User) {
			out = append(out, d)
		}
	}
	return out
}

func (m *Mutator) distinctUsers(users []string) ([]string, error) {
	seen := make(map[string]struct{}, min(len(users), m.d.Limits.MemberBatch))
	out := make([]string, 0, min(len(users), m.d.Limits.MemberBatch))
	for _, u := range users {
		if err := domain.ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		if len(out) == m.d.Limits.MemberBatch {
			return nil, domain.ErrTooManyMembers
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, errNoUsers
	}
	return out, nil
}

func randomRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

`apps/core/internal/mutate/member_change.go`:

```go
package mutate

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errRemoveSelf = fmt.Errorf("%w: user (leave the room instead of removing yourself)", apperr.ErrInvalidArgument)

type RemoveMemberCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
}

type LeaveRoomCmd struct {
	Tenant, User string
	Room         uint64
}

type ChangeRoleCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
	Role         domain.Role
}

type MemberResult struct {
	Member       domain.Member
	Changed      bool
	Successor    string
	PreviousRole domain.Role
}

type memberCall struct {
	action       access.Action
	owner        domain.OwnerAction
	tenant, user string
	room         uint64
	target       string
	role         domain.Role
}

func (m *Mutator) RemoveMember(ctx context.Context, c RemoveMemberCmd) (MemberResult, error) {
	if err := domain.ValidUser(c.Target); err != nil {
		return MemberResult{}, err
	}
	if c.Target == c.User {
		return MemberResult{}, errRemoveSelf
	}
	return m.changeMember(ctx, memberCall{
		action: access.RemoveMember, owner: domain.OwnerRemove, tenant: c.Tenant, user: c.User, room: c.Room, target: c.Target,
	})
}

func (m *Mutator) LeaveRoom(ctx context.Context, c LeaveRoomCmd) (MemberResult, error) {
	return m.changeMember(ctx, memberCall{
		action: access.LeaveRoom, owner: domain.OwnerLeave, tenant: c.Tenant, user: c.User, room: c.Room, target: c.User,
	})
}

func (m *Mutator) ChangeMemberRole(ctx context.Context, c ChangeRoleCmd) (MemberResult, error) {
	if err := domain.ValidUser(c.Target); err != nil {
		return MemberResult{}, err
	}
	if _, err := domain.ParseRole(string(c.Role)); err != nil {
		return MemberResult{}, err
	}
	return m.changeMember(ctx, memberCall{
		action: access.ChangeMemberRole, owner: domain.OwnerChangeRole, tenant: c.Tenant, user: c.User, room: c.Room,
		target: c.Target, role: c.Role,
	})
}

func (m *Mutator) changeMember(ctx context.Context, c memberCall) (MemberResult, error) {
	req, err := m.d.Access.Admit(ctx, c.action, c.tenant, c.user, c.room)
	if c.action == access.LeaveRoom && errors.Is(err, domain.ErrNotMember) {
		return m.leftBefore(ctx, c)
	}
	if err != nil {
		return MemberResult{}, err
	}
	if req.Room.Type == domain.RoomDM {
		return MemberResult{}, domain.ErrDirectRoom
	}
	rid, now := m.d.NewRequestID(), m.now()
	for range MemberTries {
		caller, target, err := m.parties(ctx, c)
		if err != nil {
			return MemberResult{}, err
		}
		probe := target
		if missing(c, target) {
			probe.Role = domain.RoleMember
		}
		if err := m.allowMember(ctx, req.Room, c, caller, probe); err != nil {
			return MemberResult{}, err
		}
		if missing(c, target) {
			return MemberResult{}, domain.ErrMemberNotFound
		}
		if settled(c, target) {
			return MemberResult{Member: target, PreviousRole: target.Role}, nil
		}
		if target.Role == domain.RoleOwner || c.role == domain.RoleOwner {
			return m.viaOwners(ctx, req.Room, c, rid, now)
		}
		next := target.Next(target.Role, domain.MemberRemoved, rid, c.user, now)
		if c.action == access.ChangeMemberRole {
			next = target.Next(c.role, domain.MemberActive, rid, c.user, now)
		}
		ok, err := m.d.Members.ApplyMember(ctx, target, next)
		if err != nil {
			return MemberResult{}, err
		}
		if ok {
			m.changed(req.Room, next)
			return MemberResult{Member: next, Changed: true, PreviousRole: next.PreviousRole}, nil
		}
	}
	return MemberResult{}, domain.ErrRetryLater
}
```

`apps/core/internal/mutate/member_paths.go`:

```go
package mutate

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
)

func (m *Mutator) viaOwners(ctx context.Context, room domain.Room, c memberCall, rid string, now time.Time) (MemberResult, error) {
	res, err := m.d.Owners.Apply(ctx, ownership.Change{
		Room: c.room, Action: c.owner, Caller: c.user, Target: c.target, Role: c.role, RequestID: rid, At: now,
		Authorize: func(ctx context.Context, caller, target domain.Member) error {
			return m.allowMember(ctx, room, c, caller, target)
		},
	})
	if err != nil {
		return MemberResult{}, err
	}
	if !res.Changed {
		return MemberResult{Member: res.Target, PreviousRole: res.Target.Role}, nil
	}
	if res.Successor.User != "" {
		m.changed(room, res.Successor, res.Target)
	} else {
		m.changed(room, res.Target)
	}
	return MemberResult{Member: res.Target, Changed: true, Successor: res.Successor.User, PreviousRole: res.Target.PreviousRole}, nil
}

func (m *Mutator) parties(ctx context.Context, c memberCall) (domain.Member, domain.Member, error) {
	users := []string{c.user}
	if c.target != c.user {
		users = append(users, c.target)
	}
	docs, err := m.d.Members.MembersOf(ctx, c.room, users)
	if err != nil {
		return domain.Member{}, domain.Member{}, err
	}
	var caller, target domain.Member
	for _, d := range docs {
		if d.User == c.user {
			caller = d
		}
		if d.User == c.target {
			target = d
		}
	}
	if !caller.Active() {
		return domain.Member{}, domain.Member{}, domain.ErrNotMember
	}
	return caller, target, nil
}

func missing(c memberCall, target domain.Member) bool {
	return target.Ver == 0 || (c.action == access.ChangeMemberRole && !target.Active())
}

func (m *Mutator) allowMember(ctx context.Context, room domain.Room, c memberCall, caller, target domain.Member) error {
	return m.d.Access.Allow(ctx, access.Request{Action: c.action, User: c.user, Room: room, Member: caller, Target: target, Role: c.role})
}

func (m *Mutator) leftBefore(ctx context.Context, c memberCall) (MemberResult, error) {
	docs, err := m.d.Members.MembersOf(ctx, c.room, []string{c.user})
	if err != nil {
		return MemberResult{}, err
	}
	if len(docs) == 0 {
		return MemberResult{}, domain.ErrNotMember
	}
	return MemberResult{Member: docs[0], PreviousRole: docs[0].Role}, nil
}

func (m *Mutator) changed(room domain.Room, docs ...domain.Member) {
	m.d.Forget.ForgetMembers(room.ID)
	m.announce(room.Type, docs...)
}

func settled(c memberCall, target domain.Member) bool {
	if c.action == access.ChangeMemberRole {
		return target.Role == c.role
	}
	return !target.Active()
}
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/mutate/"`.

**Step 5: Caller của `mutate.New` ngoài package**

`apps/core/internal/grpcsrv/fake_dependencies_test.go`:
- import thêm `"time"` (stdlib) và `".../internal/ownership"` (sau `".../internal/mutate"`);
- sau `func (nopPublisher) Enqueue(...)` thêm:

```go
type nopForgetter struct{}

func (nopForgetter) ForgetMembers(uint64) {}
```

- trong `newMutator`, ngay sau khối `if o.events != nil { events = o.events }` thêm:

```go
	var forget mutate.MemberForgetter = nopForgetter{}
	if router, ok := o.sender.(*actor.Router); ok {
		forget = router
	}
	owners, err := ownership.New(rg.rooms)
	if err != nil {
		t.Fatalf("ownership.New: %v", err)
	}
	requests, err := dedupe.NewRequests(acceptAllCIDs{}, time.Minute)
	if err != nil {
		t.Fatalf("dedupe.NewRequests: %v", err)
	}
```

- trong literal `mutate.Deps{...}`, sau dòng `Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector, Limits: o.limits,` thêm dòng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: forget,`.

(`newRig` gán `o.sender = startRouter(t, rg)` trước `newMutator`, nên rig thật dùng router làm `Forget`; `acceptAllCIDs` luôn trả `Reserved`, nên lần gửi lại cùng `request_id` được trả lời từ LRU của `Requests`.)

`apps/core/service_wiring.go` (thay cả file; Part A không sửa file này):

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

type serviceDeps struct {
	st     *mongostore.Store
	router *actor.Router
	pub    *publish.Publisher
	cids   *dedupe.Batcher
	cfg    config.Config
	log    *slog.Logger
}

func wireService(d serviceDeps) (*grpcsrv.Service, error) {
	st, cfg := d.st, d.cfg
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: cfg.LockedMessageKinds})
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return nil, fmt.Errorf("wire pin projector: %w", err)
	}
	owners, err := ownership.New(st)
	if err != nil {
		return nil, fmt.Errorf("wire owner coordinator: %w", err)
	}
	requests, err := dedupe.NewRequests(d.cids, cfg.Dedupe.CommittedTTL)
	if err != nil {
		return nil, fmt.Errorf("wire request dedupe: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: d.pub,
		Reactions: reactions, Counter: counts, Pins: pins, Projector: projector, Limits: cfg.Limits,
		Members: st, Owners: owners, Requests: requests, Forget: d.router,
	})
	if err != nil {
		return nil, fmt.Errorf("wire mutator: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: d.router, Rooms: st, Pages: st, Events: d.pub, Mutator: mut, Edits: st, Hidden: st}, d.log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}
```

`apps/core/wiring.go`: thay dòng `svc, err := wireService(st, router, pub, cfg.LockedMessageKinds, cfg.Limits, log)` bằng:

```go
	svc, err := wireService(serviceDeps{st: st, router: router, pub: pub, cids: batch, cfg: cfg, log: log})
```

(`cfg.Dedupe.CommittedTTL` đã có mặc định 15m từ `config.Load`; `CORE_REQUEST_DEADLINE + 1s < CID_PENDING_TTL` do `actor.Config` bảo đảm, nên khoá request pending không hết hạn khi lệnh còn chạy.)

**Step 6: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on -count=3 ./apps/core/internal/mutate/..."
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/"
make vet
```

Expected: PASS (`apps/core` chỉ chạy unit; itest bỏ qua khi thiếu `CHATIM_IT_*`). `wc -l`: `members.go` 156, `member_change.go` 120, `member_paths.go` 85, `member_helpers_test.go` 174, `members_test.go` 153, `member_change_test.go` 122, `member_rules_test.go` 175, `member_race_test.go` 150, `fixtures_test.go` ~179, `mutator.go` ~123, `fake_dependencies_test.go` ~108, `service_wiring.go` 65 (mọi file < 200).

**Step 7: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (wiring mới dựng `ownership` và `dedupe.Requests` trên Mongo/Redis thật; chưa có RPC member, itest cũ không đổi).

**Step 8: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/mutate", "col": "purpose", "append": "; member commands on the set class (D98-D101): AddMembers validates request_id and users (deduped, 1..Limits.MemberBatch else ErrTooManyMembers), Admit + DM refusal + Allow, then request dedupe (Busy -> ErrRetryLater; Done -> the docs this request added that are still active, never re-adding someone removed since; New -> Messages.Last once, one AddMembers write, ForgetMembers, one enqueue of pbconv.MemberEvents, Finish; a failed write Cancels); RemoveMember/LeaveRoom/ChangeMemberRole are desired state: re-read caller and target each try, ErrMemberNotFound for a never-member target, Allow with caller/target/role, a ver-conditional ApplyMember for non-owner targets (up to MemberTries, else ErrRetryLater) and ownership.Apply when the target is or becomes an owner; LeaveRoom of a tombstone is a no-op, of a never-member ErrNotMember; Limits.MemberBatch (MEMBER_BATCH_MAX 2..1000, default 500)"},
 {"path": "apps/core/internal/mutate", "col": "key_symbols", "append": ";MemberStore;OwnerCoordinator;RequestDedupe;MemberForgetter;DefaultMemberBatch;MinMemberBatch;MemberTries;AddMembersCmd;RemoveMemberCmd;LeaveRoomCmd;ChangeRoleCmd;MemberResult;Mutator.AddMembers;Mutator.RemoveMember;Mutator.LeaveRoom;Mutator.ChangeMemberRole;Mutator.MemberBatch"},
 {"path": "apps/core/internal/mutate", "col": "decisions", "append": ";D98;D99;D100;D101;D107"},
 {"path": "apps/core/internal/dedupe", "col": "used_by", "append": ";apps/core/internal/mutate"},
 {"path": "apps/core", "col": "purpose", "append": "; wireService(serviceDeps) builds ownership.New(st) and dedupe.NewRequests(cid batcher, CID_COMMITTED_TTL) for the mutator and passes the router as its member forgetter (D99, D100, D106)"},
 {"path": "apps/core", "col": "key_symbols", "append": ";serviceDeps"},
 {"path": "apps/core", "col": "decisions", "append": ";D99;D100"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/mutate/members.go apps/core/internal/mutate/member_change.go apps/core/internal/mutate/member_paths.go \
  apps/core/internal/mutate/member_helpers_test.go apps/core/internal/mutate/members_test.go apps/core/internal/mutate/member_change_test.go \
  apps/core/internal/mutate/member_rules_test.go apps/core/internal/mutate/member_race_test.go
git commit -m "feat(mutate): add, remove, leave and change role on member docs" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go apps/core/wiring.go INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 17 file: 13 trong `mutate` (`limits.go`, `mutator.go`, `fixtures_test.go`, `delete_test.go`, `limits_test.go`, 3 file code mới, 5 file test mới), `grpcsrv/fake_dependencies_test.go`, `apps/core/service_wiring.go`, `apps/core/wiring.go`, `INDEXES.csv`. Nếu `apps/core/wiring.go` còn dòng nào khác gọi `wireService(` thì dừng, báo controller.

Task rủi ro: một reviewer (thứ tự validate → Admit → DM → Allow → dedupe; `Done` chỉ trả doc còn active với đúng `request_id`/caller; `Cancel` khi ghi lỗi; caller và đích đọc lại mỗi lượt; đích owner hoặc role owner luôn đi `ownership`; `Forget`/enqueue chỉ sau khi ghi và lỗi enqueue bị bỏ qua; tối đa `-count=3` trên `mutate`).

---

### Task 13: ★ `CreateRoom`: trần mỗi lệnh, bản user `member_added`, bỏ trần 5000

Từ Task 2, `domain.NewRoom` trả member đủ field (`State` active, `Ver` 1, `RequestID = CreationRequestID(room)`, `UpdatedBy` = người tạo); `Rooms.Create` ghi đúng các doc đó. Fast path của `CreateRoom` giờ phát cùng lô với `room_created` các event `pbconv.MemberEvents(room.Type, m)` của từng member. Theo luật D104, doc tạo cùng room (`ver 1`, `request_id = {room}-created`) **không có bản room** (tránh một loạt event trên subject room mới mà chưa ai nghe), chỉ có bản user `{room}-mb-{u}-v1-u` (`recipient = u` → `evt.{t}.user.{u}.member_added`): gateway cần bản này để báo cho từng người về room mới. Worker `member_event` (Task 16) dựng lại đúng các event đó từ doc (cùng hàm), stream bỏ trùng theo id. Lỗi enqueue vẫn bị bỏ qua như cũ.

Trần (D107): owner chốt "core không giới hạn số member mỗi group", nên `domain.NewRoom` bỏ `maxGroupMembers = 5000` (DM vẫn đúng 2, group ≥ 1 và có người tạo). Giới hạn chuyển thành **mỗi lệnh**: `CreateRoom` từ chối `len(req.Members) > s.mutator.MemberBatch()` bằng `domain.ErrTooManyMembers` (`INVALID_ARGUMENT`) trước `NewRoom`, đếm thô cả user lặp (chặn bộ nhớ trước khi khử trùng); `AddMembers` đã chặn ở Task 12.

Ba test `grpcsrv` cũ so đúng danh sách event sau `createGroup` (`create_room_event_test.go`, `change_message_test.go`, `react_pin_test.go`) phải thêm các id bản user; helper `createdIDs` nằm trong file mới để `harness_test.go` (185 dòng) không lớn thêm.

**Files:**
- Modify: `apps/core/internal/domain/validate.go`, `room_test.go`
- Modify: `apps/core/internal/grpcsrv/create_room.go`
- Modify: `apps/core/internal/grpcsrv/create_room_event_test.go`, `create_room_test.go`, `change_message_test.go`, `react_pin_test.go`
- Create: `apps/core/internal/grpcsrv/room_created_ids_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test domain**

`apps/core/internal/domain/room_test.go`, trong bảng `TestNewRoomRules`: thay dòng `{"group of 5001", "acme", "alice", domain.RoomGroup, "Big", users(5001), "members"},` bằng:

```go
		{"group of 5001", "acme", "alice", domain.RoomGroup, "Big", users(5001), ""},
		{"group of 20000", "acme", "alice", domain.RoomGroup, "Big", users(20000), ""},
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run TestNewRoomRules ./apps/core/internal/domain/..."`
Expected: FAIL: `TestNewRoomRules/group_of_5001` và `TestNewRoomRules/group_of_20000` báo `NewRoom = invalid argument: members, want nil`.

**Step 3: Code domain**

`apps/core/internal/domain/validate.go`:
- xoá dòng `maxGroupMembers = 5000` khỏi khối `const` (gofmt căn lại);
- thay nguyên `func distinctMembers` bằng:

```go
func distinctMembers(typ RoomType, creator string, members []string) ([]string, error) {
	seen := make(map[string]struct{}, len(members))
	users := make([]string, 0, len(members))
	for _, u := range members {
		if err := ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		users = append(users, u)
	}
	if _, ok := seen[creator]; !ok || (typ == RoomDM && len(users) != 2) {
		return nil, invalid("members")
	}
	return users, nil
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/..."`
Expected: PASS (`dm of one`, `dm of three`, `creator missing`, `no members` vẫn báo `members`).

**Step 4: Test `grpcsrv`**

`apps/core/internal/grpcsrv/room_created_ids_test.go`:

```go
package grpcsrv_test

import "github.com/ivannguyendev/chatim/apps/core/internal/pbconv"

func createdIDs(room uint64, users ...string) []string {
	out := []string{pbconv.RoomCreatedEventID(room)}
	for _, u := range users {
		out = append(out, pbconv.MemberUserEventID(room, u, 1))
	}
	return out
}
```

`apps/core/internal/grpcsrv/create_room_event_test.go`:
- import thêm `".../internal/domain"` (trước `".../internal/pbconv"`);
- thay nguyên `TestCreateRoomEnqueuesRoomCreated` bằng:

```go
func TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers(t *testing.T) {
	clock := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	newID, _ := idSequence(42)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }, events: events})
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice", "bob"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	stored, err := rg.rooms.Get(t.Context(), 42)
	if err != nil {
		t.Fatalf("Get(42): %v", err)
	}
	docs, err := rg.rooms.MembersOf(t.Context(), 42, []string{"alice", "bob"})
	if err != nil || len(docs) != 2 {
		t.Fatalf("MembersOf(42) = %+v, %v", docs, err)
	}
	byUser := map[string]domain.Member{}
	for _, d := range docs {
		byUser[d.User] = d
	}
	want := []*chatimv1.Event{pbconv.RoomCreated(stored)}
	for _, u := range []string{"alice", "bob"} {
		want = append(want, pbconv.MemberEvents(domain.RoomGroup, byUser[u])...)
	}
	rooms, got := events.enqueued()
	if !slices.Equal(rooms, []uint64{42, 42, 42}) || len(got) != len(want) {
		t.Fatalf("enqueued %v %v, want %d events for room 42", rooms, got, len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("event %d = %v, want %v (the events the worker builds from the member docs)", i, got[i], want[i])
		}
	}
	var ids, recipients []string
	for _, ev := range got {
		ids, recipients = append(ids, ev.GetId()), append(recipients, ev.GetRecipient())
	}
	if !slices.Equal(ids, createdIDs(42, "alice", "bob")) || !slices.Equal(recipients, []string{"", "alice", "bob"}) {
		t.Fatalf("ids %v to %q, want room_created then one user copy per member and no room copy", ids, recipients)
	}
}
```

- trong `TestCreateRoomEnqueuesOnlyForTheStoredRoom`, thay `if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3}) {` bằng `if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3, 3, 3}) {` (thông điệp giữ nguyên).

`apps/core/internal/grpcsrv/create_room_test.go`: import thêm `"strconv"` và `".../internal/mutate"`; cuối file thêm:

```go
func TestCreateRoomCapsTheMembersOfOneRequest(t *testing.T) {
	members := func(n int) []string {
		out := []string{"alice"}
		for i := 1; i < n; i++ {
			out = append(out, "u"+strconv.Itoa(i))
		}
		return out
	}
	group := chatimv1.RoomType_ROOM_TYPE_GROUP
	rg := newRig(t, options{sender: &fakeSender{}})
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "Big", Members: members(501)})
	expectCode(t, err, codes.InvalidArgument)
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "Big", Members: members(500)})
	if err != nil || resp.GetRoom().GetMemberCount() != 500 {
		t.Fatalf("CreateRoom with 500 members = %v, %v; want a room of 500", resp, err)
	}
	small := newRig(t, options{sender: &fakeSender{}, limits: mutate.Limits{MemberBatch: 3}})
	for _, req := range [][]string{members(4), {"alice", "bob", "bob", "carol"}} {
		_, err := small.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "T", Members: req})
		expectCode(t, err, codes.InvalidArgument)
	}
	if _, err := small.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "T", Members: members(3)}); err != nil {
		t.Fatalf("CreateRoom at the cap: %v", err)
	}
}
```

`apps/core/internal/grpcsrv/change_message_test.go`, trong `TestEditAndDeleteThroughTheService`, thay dòng
`want := []string{pbconv.RoomCreatedEventID(id), pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)}`
bằng (file đã import `slices`):

```go
	want := slices.Concat(createdIDs(id, "alice", "bob"), []string{pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)})
```

`apps/core/internal/grpcsrv/react_pin_test.go`, trong `TestReactAndPinThroughTheService`, thay khối `wantIDs := []string{ ... }` bằng:

```go
	wantIDs := slices.Concat(createdIDs(id, "alice", "bob"), []string{
		pbconv.ReactionEventID(id, 0, 1, "bob", 1), pbconv.ReactionCountsEventID(id, 0, 1, 1),
		pbconv.ReactionEventID(id, 0, 1, "alice", 1), pbconv.ReactionCountsEventID(id, 0, 1, 2),
		pbconv.PinEventID(id, 1), pbconv.PinEventID(id, 2),
	})
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL:
- `TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers`: `enqueued [42] [...room_created...], want 3 events for room 42`;
- `TestCreateRoomEnqueuesOnlyForTheStoredRoom`: `enqueued for rooms [3], want only 3`;
- `TestEditAndDeleteThroughTheService`, `TestReactAndPinThroughTheService`: `enqueued [...], want [...]` (thiếu `{room}-mb-alice-v1-u`, `{room}-mb-bob-v1-u`);
- `TestCreateRoomCapsTheMembersOfOneRequest`: `status = (OK, ""), want (InvalidArgument, "invalid argument")`.

**Step 6: Code `grpcsrv`**

`apps/core/internal/grpcsrv/create_room.go`, hàm `CreateRoom` (thay cả hàm; phần còn lại của file giữ nguyên):

```go
func (s *Service) CreateRoom(ctx context.Context, req *chatimv1.CreateRoomRequest) (*chatimv1.CreateRoomResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	typ, err := pbconv.DomainRoomType(req.GetType())
	if err != nil {
		return nil, err
	}
	if len(req.GetMembers()) > s.mutator.MemberBatch() {
		return nil, domain.ErrTooManyMembers
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	for range createAttempts {
		room, members, err := domain.NewRoom(who.tenant, who.user, typ, req.GetName(), req.GetMembers(), now, s.newID())
		if err != nil {
			return nil, err
		}
		err = s.rooms.Create(ctx, room, members)
		switch {
		case err == nil:
			events := []*chatimv1.Event{pbconv.RoomCreated(room)}
			for _, m := range members {
				events = append(events, pbconv.MemberEvents(room.Type, m)...)
			}
			_ = s.events.Enqueue(room.ID, events)
			return &chatimv1.CreateRoomResponse{Room: pbconv.Room(room)}, nil
		case errors.Is(err, store.ErrRoomExists):
			s.log.WarnContext(ctx, "room id taken, drawing a new one", "room", room.ID)
		default:
			return nil, err
		}
	}
	return nil, errRoomIDsTaken
}
```

**Step 7: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/internal/domain/..."
make vet
```

Expected: PASS. `wc -l`: `create_room.go` 54, `create_room_test.go` ~143, `create_room_event_test.go` ~117, `room_created_ids_test.go` 11, `harness_test.go` 185 (không đổi), `domain/validate.go` ~97.

**Step 8: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/grpcsrv", "col": "purpose", "append": "; CreateRoom refuses more than Mutator.MemberBatch() members per request (raw count, ErrTooManyMembers, D107) and enqueues room_created plus pbconv.MemberEvents of every new member doc in one batch: only the user copy {room}-mb-{u}-v1-u, since docs created with the room have no room copy (D104)"},
 {"path": "apps/core/internal/grpcsrv", "col": "decisions", "append": ";D104;D107"},
 {"path": "apps/core/internal/domain", "col": "purpose", "append": "; NewRoom no longer caps the size of a group (a DM stays exactly 2); the cap is per request, MEMBER_BATCH_MAX in grpcsrv.CreateRoom and mutate.AddMembers (D107)"},
 {"path": "apps/core/internal/domain", "col": "decisions", "append": ";D107"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/room_created_ids_test.go
git commit -m "feat(grpcsrv): tell each first member about a new room and cap members per request" -- apps/core/internal/grpcsrv/ apps/core/internal/domain/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 9 file (`validate.go`, `room_test.go`, `create_room.go`, 4 test sửa, `room_created_ids_test.go`, `INDEXES.csv`).

Task rủi ro: một reviewer (trần kiểm trước `NewRoom` và đếm thô; event fast path đúng bằng event worker dựng từ doc; không có bản room cho doc tạo cùng room; `NewRoom` giữ luật DM; tối đa `-count=3` trên `grpcsrv`).

---

### Task 14: ★ Package `readcast` + `mutate.MarkRead`/`MarkUnread` + Deps + `serviceDeps.reads`

Vị trí đọc thuộc lớp **tần suất cao gộp được** (D105): `read_seq/read_ver` chỉ đổi bằng toán tử thường của Part A (`MarkRead` `$lt` nâng, `MarkUnread` `$gt` hạ, `$inc read_ver`), **không bao giờ** đụng `ver`, nên không vào change feed, không vào work stream, không có reconciler. Event `read_updated` đi qua coalescer `readcast`.

**`readcast.Caster`** (D105). Giữ một map có trần theo khoá `(room, user)`:
- **Leading edge:** khoá chưa có, hoặc lần gửi trước đã quá `Window` → gửi ngay (trong chính `Offer`, không chặn: `Publisher.Enqueue` không bao giờ chặn), ghi `sentAt` và `read_ver` đã gửi. Có bản pending cũ hơn thì gửi bản `Pos.Ver` lớn nhất.
- **Trong cửa sổ:** giữ bản `Pos.Ver` lớn nhất làm pending. Bản có `Ver` ≤ bản đã gửi là cũ (hai lệnh đua nhau) và bị bỏ.
- **Trailing edge:** `Run` có ticker `Window/2`; khoá có pending đã đủ `Window` kể từ lần gửi → gửi pending; khoá không pending rảnh quá `Window` → xoá khỏi map. Độ trễ phần đuôi tối đa `1.5 × Window` (1,5–3s).
- **Có trần:** map đầy (`MaxPending`, 65536) với khoá mới, hoặc sau `Close` → gửi thẳng, `Unbatched()++` (metric `read_events_unbatched_total`, Task 15).
- **Subject:** `roomWide = Type == dm || Members <= MaxMembers` → `pbconv.ReadUpdated(u, roomWide)`: bản room (DM và group nhỏ) hoặc `recipient = user` (group lớn: chỉ đồng bộ các thiết bị của chính người đọc).
- **Cấu hình:** `Window` mặc định 1s, hợp lệ 1s..2s; `MaxMembers` mặc định 20, **trần cứng 50** (không cấu hình cao hơn được); `MaxPending` ≥ 1. Zero → mặc định.
- **Dừng:** `Close` (idempotent) dừng `Run` (trả `nil`), xả mọi pending vào `Enqueue` một lần; `Run` trả `ctx.Err()` khi context hết. Lỗi `Enqueue` bị bỏ qua. Best-effort.

Một goroutine (`Run`) + mutex; không gửi khi đang giữ mutex. Test `synctest` + goleak, `-count=5`.

**`MarkRead`/`MarkUnread`** (`mutate/read.go`):
1. `MarkUnread` với `seq == 0` → `invalid seq`.
2. `Authorize(mark_read)` (tenant + member active; người đã rời/bị xoá → `ErrNotMember`).
3. Kẹp theo seq cuối **thật**: `0 < seq ≤ room.LastSeq` (đã nạp cùng `Admit`; `last_seq` không bao giờ vượt seq thật) → dùng luôn, không đọc thêm; còn lại → `Messages.Last(room, 0)`, `seq == 0` hoặc `seq > last` → `last`.
4. Kết quả kẹp là 0 (room chưa có tin) → trả vị trí hiện tại của `req.Member`, không ghi.
5. `MarkRead` → `Reads.MarkRead(room, user, seq)`; `MarkUnread` → `Reads.MarkUnread(room, user, seq−1)` (chỉ hạ: "đánh dấu chưa đọc từ tin seq").
6. Đổi → `ReadCast.Offer(ReadUpdate{Room, Tenant, Type, Members: room.MemberCount, User, Pos, At: now})`. Trả `ReadPosition` (đổi hay không).

DM được đánh dấu đọc bình thường (luật DM cố định chỉ áp cho lệnh member). Test kiểm cả bất biến 2: sau đọc/chưa đọc, doc member giữ nguyên mọi field trừ `read_seq/read_ver` (`ver`, role, `request_id`, `updated_*` không đổi) và không có event member hay `ForgetMembers`.

`mutate.New` bắt buộc thêm `Reads`, `ReadCast`: sửa rig `mutate`, `TestNewRequiresEveryDependency`, rig `grpcsrv` (dựng `readcast.New(events, readcast.Config{})` thật, `Close` ở cleanup; chưa `Run`, nên chỉ có leading edge) và `serviceDeps` thêm `reads *readcast.Caster` cuối; `wire` dựng `readcast.New(pub, readcast.Config{})`. Từ task này tới Task 15, `readcast` trong core chưa chạy `Run`/`Close`; không chạy core ở khoảng này.

**Files:**
- Create: `apps/core/internal/readcast/config.go`, `caster.go`
- Create: `apps/core/internal/readcast/helpers_test.go`, `config_test.go`, `caster_test.go`, `close_test.go`
- Create: `apps/core/internal/mutate/read.go`, `read_helpers_test.go`, `read_test.go`
- Modify: `apps/core/internal/mutate/mutator.go`, `fixtures_test.go`, `delete_test.go`
- Modify: `apps/core/internal/grpcsrv/fake_dependencies_test.go`
- Modify: `apps/core/service_wiring.go`, `apps/core/wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Test `readcast`**

`apps/core/internal/readcast/helpers_test.go`:

```go
package readcast_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var stamp = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

type recorder struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
}

func (r *recorder) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return nil
}

func (r *recorder) list() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

func (r *recorder) ids() []string {
	_, events := r.list()
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.GetId())
	}
	return out
}

func read(room uint64, user string, seq, ver uint64) domain.ReadUpdate {
	return domain.ReadUpdate{
		Room: room, Tenant: "acme", Type: domain.RoomGroup, Members: 3, User: user,
		Pos: domain.ReadPosition{Seq: seq, Ver: ver}, At: stamp,
	}
}

func id(room uint64, user string, ver uint64) string {
	return pbconv.ReadEventID(room, user, ver)
}

func newCaster(t *testing.T, pub readcast.Publisher, cfg readcast.Config) *readcast.Caster {
	t.Helper()
	c, err := readcast.New(pub, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func running(t *testing.T, c *readcast.Caster) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return func() error {
		cancel()
		return <-done
	}
}
```

`apps/core/internal/readcast/config_test.go`:

```go
package readcast_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestConfigFillsDefaultsAndChecksBounds(t *testing.T) {
	if readcast.DefaultWindow != time.Second || readcast.MinWindow != time.Second || readcast.MaxWindow != 2*time.Second ||
		readcast.DefaultMaxMembers != 20 || readcast.MaxMembersCap != 50 || readcast.DefaultMaxPending != 65536 {
		t.Fatalf("defaults and bounds changed")
	}
	cases := []struct {
		name string
		cfg  readcast.Config
		ok   bool
	}{
		{"defaults", readcast.Config{}, true},
		{"smallest", readcast.Config{Window: readcast.MinWindow, MaxMembers: 1, MaxPending: 1}, true},
		{"largest", readcast.Config{Window: readcast.MaxWindow, MaxMembers: readcast.MaxMembersCap}, true},
		{"window too short", readcast.Config{Window: readcast.MinWindow - time.Millisecond}, false},
		{"window too long", readcast.Config{Window: readcast.MaxWindow + time.Millisecond}, false},
		{"negative window", readcast.Config{Window: -time.Second}, false},
		{"members over the hard cap", readcast.Config{MaxMembers: readcast.MaxMembersCap + 1}, false},
		{"negative members", readcast.Config{MaxMembers: -1}, false},
		{"negative pending", readcast.Config{MaxPending: -1}, false},
	}
	for _, c := range cases {
		err := c.cfg.Validate()
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Fatalf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
		if _, err := readcast.New(&recorder{}, c.cfg); (err == nil) != c.ok {
			t.Fatalf("%s: New = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	if _, err := readcast.New(nil, readcast.Config{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New without a publisher = %v, want ErrInvalidArgument", err)
	}
}
```

`apps/core/internal/readcast/caster_test.go`:

```go
package readcast_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
)

const window = readcast.DefaultWindow

func TestTheFirstReceiptOfAKeyGoesOutAtOnce(t *testing.T) {
	rec := &recorder{}
	c := newCaster(t, rec, readcast.Config{})
	u := read(1, "bob", 5, 2)
	c.Offer(u)
	rooms, events := rec.list()
	if !slices.Equal(rooms, []uint64{1}) || len(events) != 1 || !proto.Equal(events[0], pbconv.ReadUpdated(u, true)) {
		t.Fatalf("sent %v %v, want one room-wide read_updated for room 1", rooms, events)
	}
	if c.Unbatched() != 0 {
		t.Fatalf("Unbatched() = %d, want 0", c.Unbatched())
	}
}

func TestReceiptsInsideTheWindowCollapseIntoTheNewest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		stop := running(t, c)
		for _, v := range []uint64{1, 3, 4, 2} {
			c.Offer(read(1, "bob", v, v))
		}
		c.Offer(read(1, "carol", 1, 7))
		time.Sleep(window - time.Millisecond)
		synctest.Wait()
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 1), id(1, "carol", 7)}) {
			t.Fatalf("sent %v before the window closed, want only the two leading edges", got)
		}
		time.Sleep(window/2 + time.Millisecond)
		synctest.Wait()
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 1), id(1, "carol", 7), id(1, "bob", 4)}) {
			t.Fatalf("sent %v after the window, want bob's newest (v4) as the trailing edge", got)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	})
}

func TestStaleReceiptsAreDroppedAndAnIdleKeyLeadsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		c.Offer(read(1, "bob", 5, 5))
		c.Offer(read(1, "bob", 3, 3))
		time.Sleep(window)
		c.Offer(read(1, "bob", 4, 4))
		c.Offer(read(1, "bob", 6, 6))
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 5), id(1, "bob", 6)}) {
			t.Fatalf("sent %v, want v5 then v6 at once after the window, stale v3 and v4 dropped", got)
		}
		if err := c.Close(t.Context()); err != nil || len(rec.ids()) != 2 {
			t.Fatalf("Close = %v with %v sent, want nothing pending", err, rec.ids())
		}
	})
}

func TestAFullMapSendsAtOnceAndCountsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{MaxPending: 1})
		stop := running(t, c)
		c.Offer(read(1, "bob", 1, 1))
		c.Offer(read(1, "carol", 1, 1))
		c.Offer(read(1, "carol", 2, 2))
		c.Offer(read(1, "bob", 2, 2))
		want := []string{id(1, "bob", 1), id(1, "carol", 1), id(1, "carol", 2)}
		if got := rec.ids(); !slices.Equal(got, want) || c.Unbatched() != 2 {
			t.Fatalf("sent %v with %d unbatched, want %v with 2", got, c.Unbatched(), want)
		}
		time.Sleep(window + window/2)
		synctest.Wait()
		want = append(want, id(1, "bob", 2))
		if got := rec.ids(); !slices.Equal(got, want) {
			t.Fatalf("sent %v, want bob's pending v2 after the window", got)
		}
		time.Sleep(2 * window)
		synctest.Wait()
		c.Offer(read(1, "carol", 3, 3))
		c.Offer(read(1, "carol", 4, 4))
		want = append(want, id(1, "carol", 3))
		if got := rec.ids(); !slices.Equal(got, want) || c.Unbatched() != 2 {
			t.Fatalf("sent %v with %d unbatched, want carol tracked once bob went idle", got, c.Unbatched())
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := rec.ids(); !slices.Equal(got, append(want, id(1, "carol", 4))) {
			t.Fatalf("sent %v, want carol's pending v4 flushed by Close", got)
		}
	})
}

func TestTheSubjectFollowsTheRoomSize(t *testing.T) {
	rec := &recorder{}
	c := newCaster(t, rec, readcast.Config{})
	small, big, dm := read(1, "bob", 1, 1), read(2, "bob", 1, 1), read(3, "bob", 1, 1)
	small.Members, big.Members = readcast.DefaultMaxMembers, readcast.DefaultMaxMembers+1
	dm.Type, dm.Members = domain.RoomDM, 500
	updates := []domain.ReadUpdate{small, big, dm}
	for _, u := range updates {
		c.Offer(u)
	}
	_, events := rec.list()
	want := []bool{true, false, true}
	if len(events) != len(want) {
		t.Fatalf("sent %d events, want %d", len(events), len(want))
	}
	for i, u := range updates {
		if !proto.Equal(events[i], pbconv.ReadUpdated(u, want[i])) {
			t.Fatalf("event for room %d = %v, want room-wide %v", u.Room, events[i], want[i])
		}
	}
	if events[0].GetRecipient() != "" || events[1].GetRecipient() != "bob" || events[2].GetRecipient() != "" {
		t.Fatalf("recipients %q %q %q, want the room, bob only, the room", events[0].GetRecipient(), events[1].GetRecipient(), events[2].GetRecipient())
	}
}
```

`apps/core/internal/readcast/close_test.go`:

```go
package readcast_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
)

func TestCloseFlushesPendingReceiptsAndStopsRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		done := make(chan error, 1)
		go func() { done <- c.Run(t.Context()) }()
		c.Offer(read(1, "bob", 1, 1))
		c.Offer(read(1, "bob", 2, 2))
		c.Offer(read(2, "carol", 1, 1))
		c.Offer(read(2, "carol", 3, 3))
		if err := c.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
		want := []string{id(1, "bob", 1), id(1, "bob", 2), id(2, "carol", 1), id(2, "carol", 3)}
		got := rec.ids()
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("sent %v, want both leading edges and both pending receipts", got)
		}
		if err := c.Close(context.Background()); err != nil || len(rec.ids()) != 4 {
			t.Fatalf("second Close = %v with %d sent, want a no-op", err, len(rec.ids()))
		}
		c.Offer(read(1, "bob", 3, 3))
		if len(rec.ids()) != 5 || c.Unbatched() != 1 {
			t.Fatalf("an offer after Close sent %d in total with %d unbatched, want 5 and 1", len(rec.ids()), c.Unbatched())
		}
	})
}

func TestOffersFromManyGoroutinesEndOnTheNewestVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		users := []string{"bob", "carol", "dave", "erin"}
		var wg sync.WaitGroup
		for _, u := range users {
			for part := range uint64(4) {
				wg.Go(func() {
					for v := part + 1; v <= 40; v += 4 {
						c.Offer(read(7, u, v, v))
					}
				})
			}
		}
		wg.Wait()
		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		last, sent := map[string]uint64{}, map[string]int{}
		_, events := rec.list()
		for _, ev := range events {
			r := ev.GetReadUpdated()
			if r.GetReadVer() <= last[r.GetUser()] {
				t.Fatalf("%s went back from v%d to v%d", r.GetUser(), last[r.GetUser()], r.GetReadVer())
			}
			last[r.GetUser()], sent[r.GetUser()] = r.GetReadVer(), sent[r.GetUser()]+1
		}
		for _, u := range users {
			if last[u] != 40 || sent[u] > 2 {
				t.Fatalf("%s: last v%d after %d sends, want v40 within 2 sends", u, last[u], sent[u])
			}
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/readcast/..."`
Expected: FAIL build: `no non-test Go files in /src/apps/core/internal/readcast`.

**Step 3: Code `readcast`**

`apps/core/internal/readcast/config.go`:

```go
package readcast

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultWindow = time.Second
	MinWindow     = time.Second
	MaxWindow     = 2 * time.Second
)

const (
	DefaultMaxMembers = 20
	MaxMembersCap     = 50
	DefaultMaxPending = 65536
)

type Config struct {
	Window     time.Duration
	MaxMembers int
	MaxPending int
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.MaxMembers = cmp.Or(c.MaxMembers, DefaultMaxMembers)
	c.MaxPending = cmp.Or(c.MaxPending, DefaultMaxPending)
	return c
}

func (c Config) validate() error {
	if c.Window < MinWindow || c.Window > MaxWindow || c.MaxMembers < 1 || c.MaxMembers > MaxMembersCap || c.MaxPending < 1 {
		return fmt.Errorf("%w: read receipts need a window of %v to %v, 1 to %d room-wide members and a positive pending bound, got %+v",
			apperr.ErrInvalidArgument, MinWindow, MaxWindow, MaxMembersCap, c)
	}
	return nil
}
```

`apps/core/internal/readcast/caster.go`:

```go
package readcast

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errNoPublisher = fmt.Errorf("%w: read receipts need a publisher", apperr.ErrInvalidArgument)

type Publisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type key struct {
	room uint64
	user string
}

type entry struct {
	sentAt  time.Time
	sent    uint64
	pending domain.ReadUpdate
	waiting bool
}

type Caster struct {
	pub       Publisher
	cfg       Config
	mu        sync.Mutex
	entries   map[key]*entry
	closed    bool
	stop      chan struct{}
	unbatched atomic.Uint64
}

func New(pub Publisher, cfg Config) (*Caster, error) {
	if pub == nil {
		return nil, errNoPublisher
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Caster{pub: pub, cfg: cfg, entries: make(map[key]*entry), stop: make(chan struct{})}, nil
}

func (c *Caster) Offer(u domain.ReadUpdate) {
	if out, ok := c.admit(u, time.Now()); ok {
		c.send(out)
	}
}

func (c *Caster) Run(ctx context.Context) error {
	tick := time.NewTicker(c.cfg.Window / 2)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stop:
			return nil
		case <-tick.C:
			for _, u := range c.due(time.Now()) {
				c.send(u)
			}
		}
	}
}

func (c *Caster) Close(context.Context) error {
	c.mu.Lock()
	var out []domain.ReadUpdate
	if !c.closed {
		c.closed = true
		close(c.stop)
		for _, e := range c.entries {
			if e.waiting {
				out = append(out, e.pending)
			}
		}
		c.entries = nil
	}
	c.mu.Unlock()
	for _, u := range out {
		c.send(u)
	}
	return nil
}

func (c *Caster) Unbatched() uint64 { return c.unbatched.Load() }

func (c *Caster) admit(u domain.ReadUpdate, now time.Time) (domain.ReadUpdate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := key{room: u.Room, user: u.User}
	e, ok := c.entries[k]
	switch {
	case c.closed || (!ok && len(c.entries) >= c.cfg.MaxPending):
		c.unbatched.Add(1)
		return u, true
	case !ok:
		c.entries[k] = &entry{sentAt: now, sent: u.Pos.Ver}
		return u, true
	}
	if e.waiting && e.pending.Pos.Ver > u.Pos.Ver {
		u = e.pending
	}
	if u.Pos.Ver <= e.sent {
		return domain.ReadUpdate{}, false
	}
	if now.Sub(e.sentAt) < c.cfg.Window {
		e.pending, e.waiting = u, true
		return domain.ReadUpdate{}, false
	}
	e.sentAt, e.sent, e.waiting = now, u.Pos.Ver, false
	return u, true
}

func (c *Caster) due(now time.Time) []domain.ReadUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.ReadUpdate
	for k, e := range c.entries {
		if now.Sub(e.sentAt) < c.cfg.Window {
			continue
		}
		if !e.waiting {
			delete(c.entries, k)
			continue
		}
		out = append(out, e.pending)
		e.sentAt, e.sent, e.waiting = now, e.pending.Pos.Ver, false
	}
	return out
}

func (c *Caster) send(u domain.ReadUpdate) {
	roomWide := u.Type == domain.RoomDM || u.Members <= c.cfg.MaxMembers
	_ = c.pub.Enqueue(u.Room, []*chatimv1.Event{pbconv.ReadUpdated(u, roomWide)})
}
```

**Step 4: Chạy, thấy pass + commit `readcast`**

```bash
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/readcast/..."
make vet
```

Expected: PASS 5 lần, goleak sạch. `wc -l apps/core/internal/readcast/*.go`: `caster.go` 148, `config.go` 44, `helpers_test.go` 82, `config_test.go` 44, `caster_test.go` 139, `close_test.go` 80.

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/readcast", "after": "apps/core/internal/publish/publishtest", "row": ["package", "Coalescer for read_updated events (D105): Caster.Offer sends the first receipt of a (room, user) at once and keeps only the newest read_ver inside READ_RECEIPT_WINDOW (1-2s, default 1s; stale versions dropped); Run ticks every Window/2 to send the trailing receipt and forget idle keys; the key map is bounded by MaxPending (a full map or a closed caster sends at once and counts Unbatched); DM rooms and groups of at most READ_RECEIPT_MAX_MEMBERS (default 20, hard cap 50) get the room subject, larger groups only the reader's user subject (pbconv.ReadUpdated); Close stops Run and flushes pending receipts into the publisher; best-effort, no reconciler", "Publisher;Config;Config.Validate;DefaultWindow;MinWindow;MaxWindow;DefaultMaxMembers;MaxMembersCap;DefaultMaxPending;Caster;New;Caster.Offer;Caster.Run;Caster.Close;Caster.Unbatched", "", "unit;synctest;goleak", "D104;D105"]}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/readcast/
git commit -m "feat(readcast): coalesce read receipts per room and user" -- apps/core/internal/readcast/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 7 file (`used_by` để trống: bước sau thêm `apps/core`, Task 15 thêm `apps/core/internal/config`).

**Step 5: Test `mutate`**

`apps/core/internal/mutate/fixtures_test.go`:
- trong `type rig struct`, sau `registry  *memRegistry` thêm `reads     *readSpy`;
- trong `newRig`, thay `forgets: &forgetSpy{}, registry: newMemRegistry(),` bằng `forgets: &forgetSpy{}, registry: newMemRegistry(), reads: &readSpy{},`;
- trong `deps`, thay dòng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: rg.forgets,` bằng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: rg.forgets, Reads: rg.rooms, ReadCast: rg.reads,`.

`apps/core/internal/mutate/delete_test.go`, trong map của `TestNewRequiresEveryDependency`, sau `"no forgetter": ...` thêm:

```go
		"no reads":      func(d *mutate.Deps) { d.Reads = nil },
		"no read cast":  func(d *mutate.Deps) { d.ReadCast = nil },
```

`apps/core/internal/mutate/read_helpers_test.go`:

```go
package mutate_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
)

type readSpy struct {
	mu      sync.Mutex
	updates []domain.ReadUpdate
}

func (s *readSpy) Offer(u domain.ReadUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, u)
}

func (s *readSpy) list() []domain.ReadUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.updates)
}

type countingMessages struct {
	mutate.Messages
	mu    sync.Mutex
	lasts int
}

func (c *countingMessages) Last(ctx context.Context, room, thread uint64) (uint64, error) {
	c.mu.Lock()
	c.lasts++
	c.mu.Unlock()
	return c.Messages.Last(ctx, room, thread)
}

func (c *countingMessages) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lasts
}

func readCmd(user string, seq uint64) mutate.ReadCmd {
	return mutate.ReadCmd{Tenant: tenant, User: user, Room: crew, Seq: seq}
}

func (rg *rig) markRead(t *testing.T, user string, seq uint64) domain.ReadPosition {
	t.Helper()
	pos, err := rg.m.MarkRead(t.Context(), readCmd(user, seq))
	if err != nil {
		t.Fatalf("%s marks seq %d read: %v", user, seq, err)
	}
	return pos
}

func (rg *rig) markUnread(t *testing.T, user string, seq uint64) domain.ReadPosition {
	t.Helper()
	pos, err := rg.m.MarkUnread(t.Context(), readCmd(user, seq))
	if err != nil {
		t.Fatalf("%s marks seq %d unread: %v", user, seq, err)
	}
	return pos
}

func (rg *rig) readUpdate(user string, pos domain.ReadPosition, members int) domain.ReadUpdate {
	return domain.ReadUpdate{Room: crew, Tenant: tenant, Type: domain.RoomGroup, Members: members, User: user, Pos: pos, At: rg.at()}
}

func sameUpdate(a, b domain.ReadUpdate) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}

func membershipOf(m domain.Member) domain.Member {
	m.ReadSeq, m.ReadVer = 0, 0
	return m
}
```

`apps/core/internal/mutate/read_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (rg *rig) crewWithThree(t *testing.T) domain.Member {
	t.Helper()
	rg.crew(t, "alice", "bob")
	for seq := uint64(1); seq <= 3; seq++ {
		rg.post(t, crew, seq)
	}
	bob, _ := rg.doc(t, "bob")
	return bob
}

func TestMarkReadOnlyMovesForwardAndNeverTouchesTheMembership(t *testing.T) {
	rg := newRig(t, nil)
	before := rg.crewWithThree(t)
	steps := []struct {
		seq  uint64
		want domain.ReadPosition
	}{
		{2, domain.ReadPosition{Seq: 2, Ver: 1}},
		{1, domain.ReadPosition{Seq: 2, Ver: 1}},
		{0, domain.ReadPosition{Seq: 3, Ver: 2}},
		{9, domain.ReadPosition{Seq: 3, Ver: 2}},
	}
	for _, s := range steps {
		if got := rg.markRead(t, "bob", s.seq); got != s.want {
			t.Fatalf("MarkRead(%d) = %+v, want %+v", s.seq, got, s.want)
		}
	}
	after, _ := rg.doc(t, "bob")
	if after.ReadSeq != 3 || after.ReadVer != 2 {
		t.Fatalf("stored read position = %d v%d, want 3 v2", after.ReadSeq, after.ReadVer)
	}
	if membershipOf(after) != membershipOf(before) {
		t.Fatalf("membership changed by reads: %+v, was %+v (ver, role, request and audit fields must stay)", after, before)
	}
	want := []domain.ReadUpdate{rg.readUpdate("bob", domain.ReadPosition{Seq: 2, Ver: 1}, 2), rg.readUpdate("bob", domain.ReadPosition{Seq: 3, Ver: 2}, 2)}
	if got := rg.reads.list(); !slices.EqualFunc(got, want, sameUpdate) {
		t.Fatalf("offered %+v, want only the two changes %+v", got, want)
	}
	if len(rg.eventsFor(crew)) != 0 || len(rg.forgets.calls()) != 0 {
		t.Fatal("a read enqueued member events or forgot the member cache")
	}
}

func TestMarkUnreadOnlyMovesBack(t *testing.T) {
	rg := newRig(t, nil)
	before := rg.crewWithThree(t)
	rg.markRead(t, "bob", 0)
	steps := []struct {
		seq  uint64
		want domain.ReadPosition
	}{
		{2, domain.ReadPosition{Seq: 1, Ver: 2}},
		{3, domain.ReadPosition{Seq: 1, Ver: 2}},
		{1, domain.ReadPosition{Seq: 0, Ver: 3}},
		{9, domain.ReadPosition{Seq: 0, Ver: 3}},
	}
	for _, s := range steps {
		if got := rg.markUnread(t, "bob", s.seq); got != s.want {
			t.Fatalf("MarkUnread(%d) = %+v, want %+v", s.seq, got, s.want)
		}
	}
	if _, err := rg.m.MarkUnread(t.Context(), readCmd("bob", 0)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("MarkUnread(0) = %v, want ErrInvalidArgument", err)
	}
	if after, _ := rg.doc(t, "bob"); membershipOf(after) != membershipOf(before) {
		t.Fatalf("membership changed by unreads: %+v, was %+v", after, before)
	}
	if n := len(rg.reads.list()); n != 3 {
		t.Fatalf("offered %d updates, want 3 (one read, two unreads)", n)
	}
}

func TestAnEmptyRoomKeepsTheReadPosition(t *testing.T) {
	rg := newRig(t, nil)
	rg.crew(t, "alice", "bob")
	if got := rg.markRead(t, "bob", 0); got != (domain.ReadPosition{}) {
		t.Fatalf("MarkRead in an empty room = %+v, want the zero position", got)
	}
	if got := rg.markUnread(t, "bob", 4); got != (domain.ReadPosition{}) {
		t.Fatalf("MarkUnread in an empty room = %+v, want the zero position", got)
	}
	if n := len(rg.reads.list()); n != 0 {
		t.Fatalf("offered %d updates in an empty room", n)
	}
}

func TestReadNeedsAnActiveMember(t *testing.T) {
	rg := newRig(t, nil)
	rg.crewWithThree(t)
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("alice", "bob")); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	for _, user := range []string{"bob", "mallory"} {
		if _, err := rg.m.MarkRead(t.Context(), readCmd(user, 1)); !errors.Is(err, domain.ErrNotMember) {
			t.Fatalf("%s MarkRead = %v, want ErrNotMember", user, err)
		}
		if _, err := rg.m.MarkUnread(t.Context(), readCmd(user, 1)); !errors.Is(err, domain.ErrNotMember) {
			t.Fatalf("%s MarkUnread = %v, want ErrNotMember", user, err)
		}
	}
	elsewhere := readCmd("alice", 1)
	elsewhere.Tenant = "other"
	if _, err := rg.m.MarkRead(t.Context(), elsewhere); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("MarkRead from another tenant = %v, want ErrRoomNotFound", err)
	}
	if n := len(rg.reads.list()); n != 0 {
		t.Fatalf("refused reads offered %d updates", n)
	}
}

func TestTheClampReadsTheLastSeqOnlyPastTheRoomHead(t *testing.T) {
	rg := newRig(t, nil)
	rg.crewWithThree(t)
	if err := rg.rooms.TouchActivity(t.Context(), []store.Activity{{Room: crew, Seq: 3, At: created}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	d := rg.deps(t, nil)
	counted := &countingMessages{Messages: d.Messages}
	d.Messages = counted
	rg.m = rg.build(t, d)
	rg.markRead(t, "bob", 2)
	if n := counted.count(); n != 0 {
		t.Fatalf("Last read %d times for a seq at or below the room head, want 0", n)
	}
	rg.markRead(t, "bob", 0)
	rg.markUnread(t, "bob", 5)
	if n := counted.count(); n != 2 {
		t.Fatalf("Last read %d times, want 2 (seq 0 and a seq past the head)", n)
	}
}

func TestDirectRoomsTrackReadPositionsToo(t *testing.T) {
	rg := newRig(t, nil)
	r, members, err := domain.NewRoom(tenant, "alice", domain.RoomDM, "", []string{"alice", "bob"}, created, crew)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	rg.post(t, crew, 1)
	pos := rg.markRead(t, "bob", 0)
	offers := rg.reads.list()
	if pos.Seq != 1 || len(offers) != 1 || offers[0].Type != domain.RoomDM || offers[0].Members != 2 || offers[0].Room != crew {
		t.Fatalf("MarkRead in a dm = %+v with offers %+v; want seq 1 offered as a dm of 2", pos, offers)
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL build, các lỗi thuộc nhóm `undefined: mutate.ReadCmd`, `rg.m.MarkRead undefined (type *mutate.Mutator has no field or method MarkRead)`, `rg.m.MarkUnread undefined …`, `d.Reads undefined (type *mutate.Deps has no field or method Reads)`, `d.ReadCast undefined …`, `unknown field Reads in struct literal of type mutate.Deps`, `unknown field ReadCast in struct literal of type mutate.Deps`.

**Step 7: Code `mutate`**

`apps/core/internal/mutate/read.go`:

```go
package mutate

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errNoSeq = fmt.Errorf("%w: seq", apperr.ErrInvalidArgument)

type ReadNotifier interface {
	Offer(u domain.ReadUpdate)
}

type ReadCmd struct {
	Tenant, User string
	Room, Seq    uint64
}

func (m *Mutator) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	grant, seq, err := m.readTarget(ctx, c)
	if err != nil || seq == 0 {
		return position(grant.Member), err
	}
	pos, changed, err := m.d.Reads.MarkRead(ctx, c.Room, c.User, seq)
	return m.announceRead(grant, pos, changed, err)
}

func (m *Mutator) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPosition, error) {
	if c.Seq == 0 {
		return domain.ReadPosition{}, errNoSeq
	}
	grant, seq, err := m.readTarget(ctx, c)
	if err != nil || seq == 0 {
		return position(grant.Member), err
	}
	pos, changed, err := m.d.Reads.MarkUnread(ctx, c.Room, c.User, seq-1)
	return m.announceRead(grant, pos, changed, err)
}

func (m *Mutator) readTarget(ctx context.Context, c ReadCmd) (access.Request, uint64, error) {
	grant, err := m.d.Access.Authorize(ctx, access.MarkRead, c.Tenant, c.User, c.Room)
	if err != nil {
		return access.Request{}, 0, err
	}
	if c.Seq != 0 && c.Seq <= grant.Room.LastSeq {
		return grant, c.Seq, nil
	}
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return access.Request{}, 0, err
	}
	if c.Seq == 0 || c.Seq > last {
		return grant, last, nil
	}
	return grant, c.Seq, nil
}

func (m *Mutator) announceRead(grant access.Request, pos domain.ReadPosition, changed bool, err error) (domain.ReadPosition, error) {
	if err != nil {
		return domain.ReadPosition{}, err
	}
	if changed {
		r := grant.Room
		m.d.ReadCast.Offer(domain.ReadUpdate{Room: r.ID, Tenant: r.Tenant, Type: r.Type, Members: r.MemberCount, User: grant.User, Pos: pos, At: m.now()})
	}
	return pos, nil
}

func position(m domain.Member) domain.ReadPosition {
	return domain.ReadPosition{Seq: m.ReadSeq, Ver: m.ReadVer}
}
```

`apps/core/internal/mutate/mutator.go`:
- trong `errMissingDeps`, thay đuôi `request dedupe and a member forgetter"` bằng `request dedupe, a member forgetter, read positions and a read notifier"`;
- cuối `type Deps struct` (sau `NewRequestID func() string`) thêm một dòng trống và:

```go
	Reads    store.ReadPositions
	ReadCast ReadNotifier
```

- trong `New`, hàng điều kiện `d.Members == nil || d.Owners == nil || d.Requests == nil || d.Forget == nil` thêm `|| d.Reads == nil || d.ReadCast == nil`.

Chạy `make -s go ARGS="fmt ./apps/core/internal/mutate/"`.

**Step 8: Caller ngoài package**

`apps/core/internal/grpcsrv/fake_dependencies_test.go`:
- import thêm `".../internal/readcast"` (sau `".../internal/pinproj"`);
- trong `newMutator`, ngay trước dòng `var forget mutate.MemberForgetter = nopForgetter{}` thêm:

```go
	reads, err := readcast.New(events, readcast.Config{})
	if err != nil {
		t.Fatalf("readcast.New: %v", err)
	}
	t.Cleanup(func() { _ = reads.Close(context.Background()) })
```

- trong literal `mutate.Deps{...}`, thay dòng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: forget,` bằng `Members: rg.rooms, Owners: owners, Requests: requests, Forget: forget, Reads: rg.rooms, ReadCast: reads,`.

`apps/core/service_wiring.go`:
- import thêm `".../internal/readcast"` (sau `".../internal/publish"`);
- `type serviceDeps struct`: sau `log    *slog.Logger` thêm `reads  *readcast.Caster`;
- trong literal `mutate.Deps{...}`, thay `Members: st, Owners: owners, Requests: requests, Forget: d.router,` bằng `Members: st, Owners: owners, Requests: requests, Forget: d.router, Reads: st, ReadCast: d.reads,`.

`apps/core/wiring.go`:
- import thêm `".../internal/readcast"` (sau `".../internal/publish"`);
- thay dòng `svc, err := wireService(serviceDeps{st: st, router: router, pub: pub, cids: batch, cfg: cfg, log: log})` bằng:

```go
	reads, err := readcast.New(pub, readcast.Config{})
	if err != nil {
		return nil, fmt.Errorf("wire read receipts: %w", err)
	}
	svc, err := wireService(serviceDeps{st: st, router: router, pub: pub, cids: batch, cfg: cfg, log: log, reads: reads})
```

**Step 9: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/"
make vet
```

Expected: PASS. `wc -l`: `read.go` 75, `read_test.go` 159, `read_helpers_test.go` 85, `fixtures_test.go` ~180, `mutator.go` ~126, `wiring.go` ~155, `service_wiring.go` 67, `grpcsrv/fake_dependencies_test.go` ~114.

**Step 10: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (`Reads: st` là adapter Mongo của Task 5; chưa có RPC đọc).

**Step 11: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/mutate", "col": "purpose", "append": "; MarkRead raises and MarkUnread lowers the read position (D105): Authorize mark_read (active members only), clamp the seq to the real last seq (rooms.last_seq when 0 < seq <= last_seq, else Messages.Last; seq 0 = latest; MarkUnread(0) is InvalidArgument), MarkUnread(seq) moves to seq-1 and only lowers, an empty room or no change returns the stored position; plain operators only, never ver; a change is offered to the ReadNotifier (readcast)"},
 {"path": "apps/core/internal/mutate", "col": "key_symbols", "append": ";ReadNotifier;ReadCmd;Mutator.MarkRead;Mutator.MarkUnread"},
 {"path": "apps/core/internal/mutate", "col": "decisions", "append": ";D105"},
 {"path": "apps/core", "col": "purpose", "append": "; wire builds readcast.New(pub, ...) and serviceDeps.reads passes it with the store's read positions to the mutator"},
 {"path": "apps/core", "col": "decisions", "append": ";D105"},
 {"path": "apps/core/internal/readcast", "col": "used_by", "append": "apps/core"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/mutate/read.go apps/core/internal/mutate/read_helpers_test.go apps/core/internal/mutate/read_test.go
git commit -m "feat(mutate): mark read and unread without touching the membership" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go apps/core/wiring.go INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 10 file (3 mới, `mutator.go`, `fixtures_test.go`, `delete_test.go`, `fake_dependencies_test.go`, `service_wiring.go`, `wiring.go`, `INDEXES.csv`).

Task rủi ro: một reviewer (leading/trailing edge, bỏ version cũ, map có trần và xoá khoá rảnh, `Close` idempotent và xả đúng một lần, không gửi khi giữ mutex, trần cứng 50; `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ, kẹp theo seq thật, `Offer` chỉ khi đổi, không đụng `ver`; tối đa `-count=3` trên `readcast`, `mutate`).

---

### Task 15: ★ Config (4 env) + `grpcsrv/members.go`, `read.go` + vòng đời `readcast` + `StopPlan` + metric + README. **Push**

Bốn biến môi trường mới (hợp đồng, mục config):

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `MEMBER_BATCH_MAX` | `Config.Limits.MemberBatch` | 500 | `p.count` (> 0) + `mutate.Limits.Validate` (2..1000; 1 chặn mọi DM) |
| `MEMBER_COUNT_DELAY` | `Config.MemberCountDelay` | 1s | `p.span` (> 0) + luật `> 0` và `≤ RECONCILE_DELAY` |
| `READ_RECEIPT_WINDOW` | `Config.ReadCast.Window` | 1s | `p.span` (> 0) + `readcast.Config.Validate` (1s..2s) |
| `READ_RECEIPT_MAX_MEMBERS` | `Config.ReadCast.MaxMembers` | 20 | `p.count` (> 0) + `readcast.Config.Validate` (≤ 50, trần cứng) |

`MemberCountDelay` chỉ được effect `member_counter` (Task 16) dùng; mặc định đặt bằng hằng `defaultMemberCountDelay` trong `config/effects_components.go` vì `effects.DefaultMemberCountDelay` chỉ có từ Task 16 (Task 16 có thể đổi sang hằng đó, giá trị như nhau). `Config` thêm `MemberCountDelay`, `ReadCast readcast.Config` ở cuối.

`StopPlan` thêm `ReadEvents = CloseTimeout` (1s) ngay sau gRPC: kế hoạch dừng mặc định 26.2s → **27.2s** < 28s, không đổi `CORE_SHUTDOWN_BUDGET` hay `stop_grace_period` (33s). Bảng override của `env_test.go` có kế hoạch 19.6s → 20.6s, vượt `CORE_SHUTDOWN_BUDGET=20s` của chính bảng đó, nên task này nâng override lên `22s` (chỉ dữ liệu test).

Sáu RPC mỏng (`grpcsrv.Deps` không thêm field): `callerAndRoom` → `Mutator.*` → response theo `members.proto` của Task 3 (`AddMembersResponse.added[{user, ver}]`, `RemoveMemberResponse{changed, ver}`, `LeaveRoomResponse{changed, ver, new_owner}`, `ChangeMemberRoleResponse{changed, ver, previous_role}`, `MarkRead/MarkUnreadResponse{read_seq, read_ver}`). `ChangeMemberRole` đổi role qua `pbconv.DomainMemberRole` (`UNSPECIFIED` → `INVALID_ARGUMENT`) **sau** khi kiểm caller. Mã lỗi đi qua `pkg/grpcserver` như cũ: `ErrDirectRoom`, `ErrLastOwner` → `FailedPrecondition`; `ErrMemberNotFound`, `ErrRoomNotFound` → `NotFound`; `ErrNotMember`, `access.ErrDenied` → `PermissionDenied`; `ErrTooManyMembers`, request id thiếu, xoá chính mình, `MarkUnread(0)` → `InvalidArgument`; `ErrRetryLater` → `Unavailable`.

Vòng đời: `app.reads` (`drainer`) = `readcast.Caster` dựng từ `cfg.ReadCast`; `serve` dùng `newSupervisor(ctx, 10)` và chạy task `"read events"` ngay sau `"publisher"`; `shutdown` thêm `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)` ngay sau bước `"grpc"` (không còn RPC nào gọi `Offer` sau đó; `Close` xả pending vào publisher đang còn mở). Metric `read_events_unbatched_total` (chẩn đoán, không luật alert).

Ba commit: config; RPC; vòng đời + metric + README. Push cuối task.

**Files:**
- Modify: `apps/core/internal/config/config.go`, `components.go`, `effects_components.go`, `validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`, `parse_test.go`
- Create: `apps/core/internal/grpcsrv/members.go`, `read.go`, `members_test.go`, `read_test.go`
- Modify: `apps/core/internal/grpcsrv/caller_identity_test.go`
- Modify: `apps/core/wiring.go`, `lifecycle.go`, `shutdown.go`, `metrics_wiring.go`
- Modify: `apps/core/stop_order_test.go`, `startup_gate_test.go`, `metrics_wiring_test.go`
- Modify: `README.md` (bảng env)
- Modify: `INDEXES.csv`

**Step 1: Test config**

`apps/core/internal/config/env_test.go`:
- `envKeys`: sau dòng `"REACTION_EMOJIS", "PIN_LIMIT", "REACTION_COUNT_DELAY",` thêm dòng `"MEMBER_BATCH_MAX", "MEMBER_COUNT_DELAY", "READ_RECEIPT_WINDOW", "READ_RECEIPT_MAX_MEMBERS",`.
- `overrides`: thay `"CORE_SHUTDOWN_BUDGET": "20s"` bằng `"CORE_SHUTDOWN_BUDGET": "22s"`; sau dòng `"REACTION_EMOJIS":      "🎉,👍", "PIN_LIMIT": "10", "REACTION_COUNT_DELAY": "2s",` thêm dòng `"MEMBER_BATCH_MAX": "200", "MEMBER_COUNT_DELAY": "3s", "READ_RECEIPT_WINDOW": "2s", "READ_RECEIPT_MAX_MEMBERS": "50",`.

`apps/core/internal/config/load_test.go`: import thêm `".../internal/readcast"` (sau `".../internal/publish"`);
- `TestLoadDefaults`: thay `Limits:             mutate.Limits{Emojis: mutate.DefaultEmojis, PinLimit: 50},` bằng `Limits:             mutate.Limits{Emojis: mutate.DefaultEmojis, PinLimit: 50, MemberBatch: 500},`; sau `ReactionCountDelay: time.Second,` thêm:

```go
		MemberCountDelay:   time.Second,
		ReadCast:           readcast.Config{Window: time.Second, MaxMembers: 20},
```

- `TestLoadOverrides`: thay `ShutdownBudget: 20 * time.Second,` bằng `ShutdownBudget: 22 * time.Second,`; thay `Limits:             mutate.Limits{Emojis: []string{"🎉", "👍"}, PinLimit: 10},` bằng `Limits:             mutate.Limits{Emojis: []string{"🎉", "👍"}, PinLimit: 10, MemberBatch: 200},`; sau `ReactionCountDelay: 2 * time.Second,` thêm:

```go
		MemberCountDelay:   3 * time.Second,
		ReadCast:           readcast.Config{Window: 2 * time.Second, MaxMembers: 50},
```

`apps/core/internal/config/validate_test.go`, trong bảng `TestLoadValidation`:
- thay bốn dòng ngân sách dừng (`"stop phases fill the budget"`, `"stop phases just fit the budget"`, `"cid batch drain follows the redis op timeout"`, `"worker drain counts in the stop plan"`) bằng:

```go
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "27200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "27201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "27600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"worker drain counts in the stop plan", map[string]string{"WORK_DRAIN": "1001ms", "CORE_SHUTDOWN_BUDGET": "27201ms"}, "WORK_DRAIN + 1s"},
		{"read events count in the stop plan", map[string]string{"CORE_SHUTDOWN_BUDGET": "26201ms"}, "CORE_GRPC_SHUTDOWN + 1s (read events) + RECONCILE_DRAIN"},
```

- sau dòng `{"pin limit at the cap", ...}` thêm:

```go
		{"member batch above the cap", map[string]string{"MEMBER_BATCH_MAX": "1001"}, "REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX"},
		{"member batch too small for a dm", map[string]string{"MEMBER_BATCH_MAX": "1"}, "MEMBER_BATCH_MAX"},
		{"member batch at the cap", map[string]string{"MEMBER_BATCH_MAX": "1000"}, ""},
		{"member count delay above the reconcile delay", map[string]string{"MEMBER_COUNT_DELAY": "5001ms"}, "MEMBER_COUNT_DELAY must be positive and at most RECONCILE_DELAY"},
		{"member count delay at the reconcile delay", map[string]string{"MEMBER_COUNT_DELAY": "5s"}, ""},
		{"read receipt window below the minimum", map[string]string{"READ_RECEIPT_WINDOW": "999ms"}, "READ_RECEIPT_*"},
		{"read receipt window at the maximum", map[string]string{"READ_RECEIPT_WINDOW": "2s"}, ""},
		{"read receipt window above the maximum", map[string]string{"READ_RECEIPT_WINDOW": "2001ms"}, "READ_RECEIPT_*"},
		{"read receipt members above the hard cap", map[string]string{"READ_RECEIPT_MAX_MEMBERS": "51"}, "READ_RECEIPT_*"},
		{"read receipt members at the hard cap", map[string]string{"READ_RECEIPT_MAX_MEMBERS": "50"}, ""},
```

`apps/core/internal/config/parse_test.go`, trong bảng `TestLoadRejectsNonPositiveValues`, sau `{"REACTION_COUNT_DELAY", "0s"},` thêm:

```go
		{"MEMBER_BATCH_MAX", "0"},
		{"MEMBER_COUNT_DELAY", "0s"},
		{"READ_RECEIPT_WINDOW", "0s"},
		{"READ_RECEIPT_MAX_MEMBERS", "0"},
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL build: `unknown field MemberCountDelay in struct literal of type config.Config`, `unknown field ReadCast in struct literal of type config.Config` (mỗi lỗi hai lần, ở `TestLoadDefaults` và `TestLoadOverrides`).

**Step 3: Code config**

`apps/core/internal/config/config.go`: import thêm `".../internal/readcast"` (sau `".../internal/publish"`);
- `type Config struct`: sau `ReactionCountDelay  time.Duration` thêm:

```go
	MemberCountDelay    time.Duration
	ReadCast            readcast.Config
```

- `type StopPlan struct`: sau `GRPC       time.Duration` thêm `ReadEvents time.Duration`;
- trong literal `Limits: mutate.Limits{...}` của `Load`, sau dòng `PinLimit: ...` thêm `MemberBatch: p.count("MEMBER_BATCH_MAX", mutate.DefaultMemberBatch),` (gofmt căn cột cả ba dòng);
- trong `StopPlan()`, sau `GRPC:       c.GRPCShutdown,` thêm `ReadEvents: CloseTimeout,`;
- trong `total()`, danh sách thành `[]time.Duration{s.DrainDelay, s.GRPC, s.ReadEvents, s.Reconciler, s.Workers, s.Router, s.CIDBatch, s.Flusher, s.Publisher, s.Slots, s.Close}`.

`apps/core/internal/config/components.go`: import thêm `".../internal/readcast"` (sau `".../internal/publish"`); trong `components`, ngay trước dòng cuối `p.workerConfig(c)` thêm:

```go
	c.ReadCast = readcast.Config{
		Window:     p.span("READ_RECEIPT_WINDOW", readcast.DefaultWindow),
		MaxMembers: p.count("READ_RECEIPT_MAX_MEMBERS", readcast.DefaultMaxMembers),
	}
```

`apps/core/internal/config/effects_components.go` (thay cả file):

```go
package config

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
)

const defaultMemberCountDelay = time.Second

func (p *parser) workerConfig(c *Config) {
	c.Effects = effects.Config{
		Partitions: c.Work.Partitions,
		FetchBatch: p.count("WORK_FETCH_BATCH", effects.DefaultFetchBatch),
		FetchWait:  p.span("WORK_FETCH_WAIT", effects.DefaultFetchWait),
		RetryDelay: p.span("WORK_RETRY_DELAY", effects.DefaultRetryDelay),
		Drain:      p.span("WORK_DRAIN", effects.DefaultDrain),
		Poll:       c.Slot.Tick,
	}
	c.ReactionCountDelay = p.span("REACTION_COUNT_DELAY", effects.DefaultCountDelay)
	c.MemberCountDelay = p.span("MEMBER_COUNT_DELAY", defaultMemberCountDelay)
}
```

`apps/core/internal/config/validate.go`:
- trong hằng `stopPhases`, thay `CORE_GRPC_SHUTDOWN + RECONCILE_DRAIN + 1s` bằng `CORE_GRPC_SHUTDOWN + 1s (read events) + RECONCILE_DRAIN + 1s`;
- trong `rules`, sau dòng luật `REACTION_COUNT_DELAY` thêm:

```go
		{c.MemberCountDelay > 0 && c.MemberCountDelay <= c.EffectDelay, "MEMBER_COUNT_DELAY must be positive and at most RECONCILE_DELAY"},
```

- trong `parts` của `componentErrors`, thay `{"REACTION_EMOJIS, PIN_LIMIT", c.Limits.Validate()},` bằng:

```go
		{"REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX", c.Limits.Validate()},
		{"READ_RECEIPT_*", c.ReadCast.Validate()},
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/config/"`.

**Step 4: Chạy, thấy pass + commit config**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: PASS (`TestOverridesCoverEveryKey` thấy 4 khoá mới ở cả hai bảng). `wc -l apps/core/internal/config/*.go`: mỗi file < 200 (`load_test.go` ~186, `config.go` ~164, `validate_test.go` ~138, `parse_test.go` ~151).

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/config", "col": "purpose", "append": "; MEMBER_BATCH_MAX -> Limits.MemberBatch (default 500, 2..1000 by mutate.Limits.Validate, D107); MEMBER_COUNT_DELAY -> MemberCountDelay (default 1s, positive and at most RECONCILE_DELAY, D102); READ_RECEIPT_WINDOW/READ_RECEIPT_MAX_MEMBERS -> ReadCast (readcast.Config, defaults 1s and 20, checked by readcast.Config.Validate: 1s..2s and 1..50 hard cap, D105); StopPlan.ReadEvents = 1s right after gRPC, default stop plan 27.2s of 28s"},
 {"path": "apps/core/internal/config", "col": "key_symbols", "append": ";Config.MemberCountDelay;Config.ReadCast;StopPlan.ReadEvents"},
 {"path": "apps/core/internal/config", "col": "decisions", "append": ";D102;D105;D107"},
 {"path": "apps/core/internal/readcast", "col": "used_by", "append": ";apps/core/internal/config"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git commit -m "feat(config): member batch cap, member count delay and read receipt limits" -- apps/core/internal/config/ INDEXES.csv
```

Expected: `{7}`; `make vet` sạch (`apps/core` vẫn biên dịch: `StopPlan`/`Config` chỉ thêm field); `git show --stat HEAD` có 9 file.

**Step 5: Test `grpcsrv`**

`apps/core/internal/grpcsrv/caller_identity_test.go`, trong map `rpcs` của `TestEveryRPCChecksCallerIdentityFirst`, sau mục `"UnpinMessage"` thêm:

```go
		"AddMembers": func(ctx context.Context) error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: "42", Users: []string{"bob"}, RequestId: "rq-1"})
			return err
		},
		"RemoveMember": func(ctx context.Context) error {
			_, err := rg.client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: "42", User: "bob"})
			return err
		},
		"LeaveRoom": func(ctx context.Context) error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: "42"})
			return err
		},
		"ChangeMemberRole": func(ctx context.Context) error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: "42", User: "bob"})
			return err
		},
		"MarkRead": func(ctx context.Context) error {
			_, err := rg.client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: "42", Seq: 1})
			return err
		},
		"MarkUnread": func(ctx context.Context) error {
			_, err := rg.client.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: "42", Seq: 1})
			return err
		},
```

(`ChangeMemberRole` gửi role `UNSPECIFIED` có chủ ý: caller phải bị kiểm trước role.)

`apps/core/internal/grpcsrv/members_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func addedUsers(resp *chatimv1.AddMembersResponse) []string {
	var out []string
	for _, a := range resp.GetAdded() {
		out = append(out, a.GetUser())
	}
	return out
}

func TestMemberChangesThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, carol := as(t, "acme", "alice"), as(t, "acme", "carol")
	added, err := rg.client.AddMembers(alice, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"carol", "dave", "bob"}, RequestId: "rq-1"})
	if err != nil || !slices.Equal(addedUsers(added), []string{"carol", "dave"}) || added.GetAdded()[0].GetVer() != 1 {
		t.Fatalf("AddMembers = %v, %v; want carol and dave added at ver 1", added, err)
	}
	promoted, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "carol", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	if err != nil || !promoted.GetChanged() || promoted.GetVer() != 2 || promoted.GetPreviousRole() != chatimv1.MemberRole_MEMBER_ROLE_MEMBER {
		t.Fatalf("ChangeMemberRole = %v, %v; want carol promoted from member at ver 2", promoted, err)
	}
	for i, changed := range []bool{true, false} {
		removed, err := rg.client.RemoveMember(carol, &chatimv1.RemoveMemberRequest{RoomId: room, User: "dave"})
		if err != nil || removed.GetChanged() != changed || removed.GetVer() != 2 {
			t.Fatalf("RemoveMember #%d = %v, %v; want ver 2, changed %v", i+1, removed, err, changed)
		}
	}
	left, err := rg.client.LeaveRoom(alice, &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || !left.GetChanged() || left.GetVer() != 2 || left.GetNewOwner() != "carol" {
		t.Fatalf("LeaveRoom = %v, %v; want carol (the only admin) as the new owner", left, err)
	}
	again, err := rg.client.LeaveRoom(alice, &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || again.GetChanged() || again.GetVer() != 2 {
		t.Fatalf("leaving again = %v, %v; want an unchanged success", again, err)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	ids := make([]string, 0, len(got))
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	want := slices.Concat(createdIDs(id, "alice", "bob"), []string{
		pbconv.MemberEventID(id, "carol", 1), pbconv.MemberUserEventID(id, "carol", 1),
		pbconv.MemberEventID(id, "dave", 1), pbconv.MemberUserEventID(id, "dave", 1),
		pbconv.MemberEventID(id, "carol", 2), pbconv.MemberUserEventID(id, "carol", 2),
		pbconv.MemberEventID(id, "dave", 2), pbconv.MemberUserEventID(id, "dave", 2),
		pbconv.MemberEventID(id, "carol", 3), pbconv.MemberUserEventID(id, "carol", 3),
		pbconv.MemberEventID(id, "alice", 2), pbconv.MemberUserEventID(id, "alice", 2),
	})
	if !slices.Equal(ids, want) {
		t.Fatalf("enqueued %v, want %v", ids, want)
	}
}

func TestARemovedMemberCanNeitherSendNorReadUntilAddedBack(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, bob, room, "b-1", "hi")
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "bob"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	oldest := chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST
	calls := map[string]func() error{
		"send": func() error {
			_, err := rg.client.SendMessage(bob, &chatimv1.SendMessageRequest{RoomId: room, Cid: "b-2", Text: "still here?"})
			return err
		},
		"history": func() error {
			_, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: oldest})
			return err
		},
		"read": func() error {
			_, err := rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) { expectCode(t, call(), codes.PermissionDenied) })
	}
	if _, err := rg.client.AddMembers(alice, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"bob"}, RequestId: "rq-2"}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	page, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: oldest})
	if err != nil || len(page.GetMessages()) != 1 {
		t.Fatalf("history after re-add = %v, %v; want the one earlier message", page, err)
	}
	rg.send(t, bob, room, "b-3", "back")
}

func TestARetriedAddNeverBringsBackSomeoneRemovedSince(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	add := &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"dave"}, RequestId: "rq-1"}
	if _, err := rg.client.AddMembers(alice, add); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "dave"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	retried, err := rg.client.AddMembers(alice, add)
	if err != nil || len(retried.GetAdded()) != 0 {
		t.Fatalf("retried AddMembers = %v, %v; want nobody added", retried, err)
	}
	_, err = rg.client.SendMessage(as(t, "acme", "dave"), &chatimv1.SendMessageRequest{RoomId: room, Cid: "d-1", Text: "hi"})
	expectCode(t, err, codes.PermissionDenied)
}

func TestMemberErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{limits: mutate.Limits{MemberBatch: 2}})
	room := rg.createGroup(t, "acme", "alice", "bob")
	dm, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"}})
	if err != nil {
		t.Fatalf("CreateRoom dm: %v", err)
	}
	alice, bob, mallory := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "mallory")
	add := func(ctx context.Context, roomID, rid string, users ...string) func() error {
		return func() error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: roomID, Users: users, RequestId: rid})
			return err
		}
	}
	role := func(ctx context.Context, user string, r chatimv1.MemberRole) func() error {
		return func() error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: user, Role: r})
			return err
		}
	}
	leave := func(ctx context.Context, roomID string) func() error {
		return func() error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: roomID})
			return err
		}
	}
	remove := func(ctx context.Context, user string) func() error {
		return func() error {
			_, err := rg.client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: room, User: user})
			return err
		}
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"add to a dm", add(alice, dm.GetRoom().GetId(), "rq-1", "carol"), codes.FailedPrecondition},
		{"leave a dm", leave(alice, dm.GetRoom().GetId()), codes.FailedPrecondition},
		{"last owner steps down", role(alice, "alice", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), codes.FailedPrecondition},
		{"remove yourself", remove(alice, "alice"), codes.InvalidArgument},
		{"unspecified role", role(alice, "bob", chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED), codes.InvalidArgument},
		{"too many users", add(alice, room, "rq-2", "c1", "c2", "c3"), codes.InvalidArgument},
		{"no users", add(alice, room, "rq-3"), codes.InvalidArgument},
		{"no request id", add(alice, room, "", "carol"), codes.InvalidArgument},
		{"bad room id", leave(alice, "x"), codes.InvalidArgument},
		{"role of a stranger", role(alice, "zed", chatimv1.MemberRole_MEMBER_ROLE_ADMIN), codes.NotFound},
		{"remove a stranger", remove(alice, "zed"), codes.NotFound},
		{"unknown room", leave(alice, "999"), codes.NotFound},
		{"other tenant", add(as(t, "other", "alice"), room, "rq-4", "carol"), codes.NotFound},
		{"member adds", add(bob, room, "rq-5", "carol"), codes.PermissionDenied},
		{"member changes a role", role(bob, "alice", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), codes.PermissionDenied},
		{"stranger adds", add(mallory, room, "rq-6", "carol"), codes.PermissionDenied},
		{"stranger leaves", leave(mallory, room), codes.PermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
}
```

`apps/core/internal/grpcsrv/read_test.go`:

```go
package grpcsrv_test

import (
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReadPositionThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i := range 3 {
		rg.send(t, alice, room, "c-"+strconv.Itoa(i), "hi")
	}
	steps := []struct {
		name         string
		unread       bool
		seq, wantSeq uint64
		wantVer      uint64
	}{
		{"read seq 2", false, 2, 2, 1},
		{"read the latest", false, 0, 3, 2},
		{"read an older seq", false, 1, 3, 2},
		{"unread from seq 2", true, 2, 1, 3},
	}
	for _, s := range steps {
		var gotSeq, gotVer uint64
		if s.unread {
			resp, err := rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: s.seq})
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			gotSeq, gotVer = resp.GetReadSeq(), resp.GetReadVer()
		} else {
			resp, err := rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room, Seq: s.seq})
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			gotSeq, gotVer = resp.GetReadSeq(), resp.GetReadVer()
		}
		if gotSeq != s.wantSeq || gotVer != s.wantVer {
			t.Fatalf("%s = seq %d ver %d, want seq %d ver %d", s.name, gotSeq, gotVer, s.wantSeq, s.wantVer)
		}
	}
	_, err := rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room})
	expectCode(t, err, codes.InvalidArgument)
	_, err = rg.client.MarkRead(as(t, "acme", "mallory"), &chatimv1.MarkReadRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.MarkRead(as(t, "other", "bob"), &chatimv1.MarkReadRequest{RoomId: room})
	expectCode(t, err, codes.NotFound)
	id := roomNumber(t, room)
	_, got := events.enqueued()
	var first *chatimv1.Event
	for _, ev := range got {
		if ev.GetReadUpdated() != nil {
			first = ev
			break
		}
	}
	if first.GetId() != pbconv.ReadEventID(id, "bob", 1) || first.GetRecipient() != "" || first.GetReadUpdated().GetReadSeq() != 2 || first.GetActor() != "bob" {
		t.Fatalf("first read_updated = %v, want bob's seq 2 at ver 1 for the whole room", first)
	}
	docs, err := rg.rooms.MembersOf(t.Context(), id, []string{"bob"})
	if err != nil || len(docs) != 1 || docs[0].Ver != 1 {
		t.Fatalf("bob after reads = %+v, %v; want his membership still at ver 1", docs, err)
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL (biên dịch được vì Task 3 đã sinh client/server cho 6 RPC và `Service` nhúng `UnimplementedCoreServiceServer`):
- `TestEveryRPCChecksCallerIdentityFirst/AddMembers/no_metadata` (và mọi case của 6 RPC mới): `status = (Unimplemented, "method AddMembers not implemented"), want (Unauthenticated, "unauthenticated")`;
- `TestMemberChangesThroughTheService`: `AddMembers = <nil>, rpc error: code = Unimplemented desc = method AddMembers not implemented; want carol and dave added at ver 1`;
- `TestARemovedMemberCanNeitherSendNorReadUntilAddedBack`: `RemoveMember: rpc error: code = Unimplemented desc = method RemoveMember not implemented`;
- `TestARetriedAddNeverBringsBackSomeoneRemovedSince`: `AddMembers: rpc error: code = Unimplemented desc = method AddMembers not implemented`;
- `TestMemberErrorsKeepTheirCodes/*`: `status = (Unimplemented, "method … not implemented"), want (…)`;
- `TestReadPositionThroughTheService`: `read seq 2: rpc error: code = Unimplemented desc = method MarkRead not implemented`.

**Step 7: Code `grpcsrv`**

`apps/core/internal/grpcsrv/members.go`:

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	added, err := s.mutator.AddMembers(ctx, mutate.AddMembersCmd{
		Tenant: who.tenant, User: who.user, Room: room, Users: req.GetUsers(), RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*chatimv1.AddedMember, len(added))
	for i, m := range added {
		out[i] = &chatimv1.AddedMember{User: m.User, Ver: m.Ver}
	}
	return &chatimv1.AddMembersResponse{Added: out}, nil
}

func (s *Service) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.RemoveMember(ctx, mutate.RemoveMemberCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{Changed: res.Changed, Ver: res.Member.Ver}, nil
}

func (s *Service) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.LeaveRoom(ctx, mutate.LeaveRoomCmd{Tenant: who.tenant, User: who.user, Room: room})
	if err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{Changed: res.Changed, Ver: res.Member.Ver, NewOwner: res.Successor}, nil
}

func (s *Service) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	role, err := pbconv.DomainMemberRole(req.GetRole())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.ChangeMemberRole(ctx, mutate.ChangeRoleCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser(), Role: role})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{Changed: res.Changed, Ver: res.Member.Ver, PreviousRole: pbconv.MemberRole(res.PreviousRole)}, nil
}
```

`apps/core/internal/grpcsrv/read.go`:

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.mutator.MarkRead(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: pos.Seq, ReadVer: pos.Ver}, nil
}

func (s *Service) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.mutator.MarkUnread(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: pos.Seq, ReadVer: pos.Ver}, nil
}

func readCmdOf(ctx context.Context, roomID string, seq uint64) (mutate.ReadCmd, error) {
	who, room, err := callerAndRoom(ctx, roomID)
	if err != nil {
		return mutate.ReadCmd{}, err
	}
	return mutate.ReadCmd{Tenant: who.tenant, User: who.user, Room: room, Seq: seq}, nil
}
```

**Step 8: Chạy, thấy pass + commit RPC**

```bash
make -s go ARGS="test -race -shuffle=on -count=3 ./apps/core/internal/grpcsrv/..."
make vet
```

Expected: PASS. `wc -l apps/core/internal/grpcsrv/*.go`: mỗi file < 200 (`members.go` 67, `read.go` 40, `members_test.go` 182, `read_test.go` 73, `caller_identity_test.go` ~136, `harness_test.go` 185 không đổi).

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/grpcsrv", "col": "purpose", "append": "; AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole (members.go: request_id passed through, role through pbconv.DomainMemberRole after the caller check, responses carry changed and the member ver) and MarkRead/MarkUnread (read.go: read_seq, read_ver) are thin calls into mutate"},
 {"path": "apps/core/internal/grpcsrv", "col": "key_symbols", "append": ";Service.AddMembers;Service.RemoveMember;Service.LeaveRoom;Service.ChangeMemberRole;Service.MarkRead;Service.MarkUnread"},
 {"path": "apps/core/internal/grpcsrv", "col": "decisions", "append": ";D99;D100;D101;D105"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/members.go apps/core/internal/grpcsrv/read.go apps/core/internal/grpcsrv/members_test.go apps/core/internal/grpcsrv/read_test.go
git commit -m "feat(grpcsrv): member and read position rpcs" -- apps/core/internal/grpcsrv/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 6 file.

**Step 9: Test vòng đời `apps/core`**

`apps/core/stop_order_test.go`:
- trong literal `app{...}`, thay dòng `slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"),` bằng `slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"), reads: order.drainer("read events"),`;
- thay `want := []string{"reconciler", "workers", "router", "cid batcher", "flusher", "publisher"}` bằng `want := []string{"read events", "reconciler", "workers", "router", "cid batcher", "flusher", "publisher"}`.

`apps/core/startup_gate_test.go`, trong `startGate`, thay `router: router, slots: slots, workers: idle{},` bằng `router: router, slots: slots, workers: idle{}, reads: idle{},`.

`apps/core/metrics_wiring_test.go`:
- trong `fakeProbes`, sau `oplogWindow:  func() float64 { return 0 },` thêm `readUnbatched: func() uint64 { return 4 },` (gofmt căn cột cả khối);
- trong `everyCore` của `TestCoreMetricSourcesCoverEveryGuarantee`, thay `"counter_repaired_total",` bằng `"counter_repaired_total", "read_events_unbatched_total",`;
- trong `want` của `TestEffectMetricsReadTheirEffectByLabel`, sau `"counter_repaired_total{reactions}":    6,` thêm `"read_events_unbatched_total":          4,`.

**Step 10: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: FAIL build: `unknown field readUnbatched in struct literal of type probes`, `unknown field reads in struct literal of type app` (hai chỗ: `startup_gate_test.go`, `stop_order_test.go`).

**Step 11: Code vòng đời**

`apps/core/wiring.go`:
- trong `type app struct`, sau `publisher  drainer` thêm `reads      drainer`;
- thay `reads, err := readcast.New(pub, readcast.Config{})` (Task 14) bằng `reads, err := readcast.New(pub, cfg.ReadCast)`, và ngay sau khối `if err != nil { ... "wire read receipts" ... }` đó thêm `a.reads = reads`;
- trong literal `p := probes{...}`, sau `oplogWindow:    oplogWindowSeconds(cl.mongo),` thêm `readUnbatched:  reads.Unbatched,`.

`apps/core/metrics_wiring.go`:
- trong `type probes struct`, sau `oplogWindow    func() float64` thêm `readUnbatched  func() uint64`;
- trong `metricSources`, sau dòng `{Name: "mongo_oplog_window_seconds", ...},` thêm:

```go
		{Name: "read_events_unbatched_total", Help: "Read receipts sent without coalescing because the coalescer was full or closed.", Read: func() float64 { return float64(p.readUnbatched()) }},
```

`apps/core/lifecycle.go`:
- `type tasks struct` thành `admin, publisher, reads, flusher, cidBatch, router, slots, workers, reconciler, grpc *task`;
- trong `serve`: `sup := newSupervisor(ctx, 10)`; trong literal `tasks{...}`, sau `publisher: sup.start("publisher", a.publisher.Run),` thêm `reads:     sup.start("read events", a.reads.Run),`.

`apps/core/shutdown.go`: ngay sau `s.step("grpc", 0, nil, t.grpc)` thêm `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)`.

Chạy `make -s go ARGS="fmt ./apps/core/"`.

**Step 12: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/..."
make vet
```

Expected: PASS (`stop_order_test` thấy `read events` đầu danh sách; itest bỏ qua khi thiếu `CHATIM_IT_*`). `wc -l`: `wiring.go` ~158, `lifecycle.go` ~89, `shutdown.go` ~82, `metrics_wiring.go` ~125, `startup_gate_test.go` 174 (không đổi), `stop_order_test.go` ~87, `metrics_wiring_test.go` ~115.

**Step 13: README**

`README.md`, bảng env:
- dòng `CORE_SHUTDOWN_BUDGET`: thay đoạn

```markdown
Mặc định các mốc là 26.2s (gồm `RECONCILE_DRAIN + 1s` của reader,
```

  bằng

```markdown
Mặc định các mốc là 27.2s (gồm 1s để `readcast` xả các `read_updated` đang gộp, `RECONCILE_DRAIN + 1s` của reader,
```

- ngay sau dòng `REACTION_COUNT_DELAY` thêm:

```markdown
| `MEMBER_BATCH_MAX` | `500` | Số user tối đa trong một lệnh `CreateRoom` (đếm thô cả user lặp) hoặc `AddMembers` (sau khi khử trùng), 2–1000 (1 sẽ chặn mọi DM); vượt → `INVALID_ARGUMENT`. Core không giới hạn tổng số member của group (D107) |
| `MEMBER_COUNT_DELAY` | `1s` | Delay của effect `member_counter`: `rooms.member_count` được đếm lại sau `CommittedAt + D` (hội tụ ~1s, chỉ worker ghi); phải dương và không quá `RECONCILE_DELAY` (D102) |
| `READ_RECEIPT_WINDOW` | `1s` | Cửa sổ gộp event `read_updated` mỗi (room, user): lần đầu gửi ngay, các lần sau trong cửa sổ gộp thành bản `read_ver` mới nhất, gửi trễ tối đa 1,5 lần cửa sổ (1s–2s, D105) |
| `READ_RECEIPT_MAX_MEMBERS` | `20` | DM và group có `member_count` không quá giá trị này nhận `read_updated` trên subject room; group lớn hơn chỉ nhận trên subject của chính người đọc (`evt.{t}.user.{u}.read_updated`). 1–50, trần cứng 50 (D105) |
```

**Step 14: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (core thật chạy task `read events`, dừng đúng bước mới; `shutdown_integration_test` vẫn dừng trong giới hạn của nó).

**Step 15: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core", "col": "purpose", "append": "; readcast.Caster (cfg.ReadCast) runs as the 'read events' task started right after the publisher and stops right after gRPC within StopPlan.ReadEvents (1s), flushing pending read receipts into the still open publisher; /metrics read_events_unbatched_total"},
 {"path": "README.md", "col": "purpose", "replace": ["(includes REACTION_EMOJIS, PIN_LIMIT, REACTION_COUNT_DELAY)", "(includes REACTION_EMOJIS, PIN_LIMIT, REACTION_COUNT_DELAY, MEMBER_BATCH_MAX, MEMBER_COUNT_DELAY, READ_RECEIPT_WINDOW, READ_RECEIPT_MAX_MEMBERS; stop plan 27.2s)"]},
 {"path": "README.md", "col": "decisions", "append": ";D102;D105;D107"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git commit -m "feat(core): run the read receipt coalescer and stop it right after grpc" -- apps/core/wiring.go apps/core/lifecycle.go apps/core/shutdown.go apps/core/metrics_wiring.go apps/core/stop_order_test.go apps/core/startup_gate_test.go apps/core/metrics_wiring_test.go README.md INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 9 file; `README.md` chỉ đổi 1 dòng và thêm 4 dòng (nếu `README.md` có thay đổi chưa commit của người khác: dừng, báo controller).

**Step 16: Push**

```bash
git log --oneline origin/feat/m2b..HEAD
git push origin feat/m2b
```

Expected: log liệt kê các commit Task 8–15 (Task 14 hai commit, Task 15 ba commit); push thành công.

Task rủi ro: một reviewer (luật kiểm config và ngân sách dừng 27.2s, thứ tự dừng `grpc → read events → reconciler`, 6 RPC kiểm caller trước mọi thứ, mã lỗi, `read_events_unbatched_total`; tối đa `-count=3` trên `config`, `grpcsrv`, `apps/core`).

### Task 16: ★ Effect `member_counter`, `owner_guard`, `member_event` + registry + metric/luật OW1 + wiring

Lưới an toàn và đường ghi duy nhất của `member_count`, chạy trên record `MemberChanged` (Task 6), theo thứ tự registry với delay không giảm:

```
MemberChanged → room_activity (0) → member_counter (MEMBER_COUNT_DELAY) → owner_guard (RECONCILE_DELAY) → member_event (RECONCILE_DELAY)
```

1. `member_counter` (D102): gom record của lô theo room (`groupRecords`, `recordRoom`); witness = `(user, max Version)` (`witnessesOf`, dùng lại của `reaction_counter`); `Rooms.Get` (đọc thật, không cache) → `MemberToucher.Touch(room, {MemberCount, MemberCountVer}, witnesses, Tries)` → kết quả `Ver > 0` → publish `pbconv.MemberCountChanged(room đã cập nhật, Now)` (phát lại cả khi không đổi; stream bỏ trùng). Chỉ `domain.ErrRoomNotFound` → drop có đếm theo số record; mọi lỗi khác (`ErrStaleRead` khi witness chưa thấy, `ErrContended`, dữ liệu hỏng) → lỗi cho mọi record của room (retry, hiện ở `work_failures_total`). Không đếm vào `counter_repaired_total` (đây là đường ghi chính, không phải sửa lỗi).
2. `owner_guard` (D100, D108): mỗi room một lần mỗi lô; loại room qua cache `roomTypes`; DM → không làm gì; group → `ownership.Coordinator.Guard(room, Now)`; `true` → `Repaired()++`. Chỉ `ErrRoomNotFound` → drop có đếm; lỗi khác → lỗi cho mọi record của room.
3. `member_event` (D104): mỗi record `MembersOf(room, [user])`; không có doc → drop; `doc.Ver > rec.Version` → bỏ (record mới hơn lo); `<` → `store.ErrStaleRead` (Nak); `==` → loại room (cache) → `pbconv.MemberEvents(type, doc)` → publish **mọi** bản; record nil chỉ khi mọi bản có PubAck. `Republished` chỉ đếm PubAck không phải bản trùng. Drop khi room không còn, doc không có, hoặc `ErrInvalidArgument` (doc hỏng), như `reaction_event`.

`eventPublisher` (M2b.3) có `each` một event mỗi record. Task này thêm `eachMany` (builder trả nhiều event; mọi bản của record `i` ghi lỗi vào `errs[i]`, nên một bản lỗi là record lỗi) và viết `each` thành lớp bọc mỏng của `eachMany`: hành vi `reaction_event`, `pin_event` giữ nguyên (test cũ là lưới an toàn).

**Metric + luật (D108):** `owner_repaired_total` (help `Groups the owner_guard effect found without an owner or with an unfinished owner change and repaired.`), luật `ChatimOwnerRepaired` → **17 luật**. `effectSet.counters()` thêm `member_counter` (republished + dropped), `owner_guard` (chỉ dropped), `member_event` (republished + dropped). MB1 dùng luật theo nhãn chung có sẵn.

**Wiring:** file mới `apps/core/member_effects_wiring.go` (`(fx *effectSet) wireMemberEffects(cfg, cl, st) error`) dựng `counter.NewMembers(st)`, `ownership.New(st)` riêng (không giữ trạng thái; bản thứ hai cạnh `service_wiring.go` vô hại) và ba effect. `wireEffects` **không đổi chữ ký**.

Trước khi bắt đầu, kiểm tên của Part A/B (chỉ đọc):

```bash
grep -n "MemberChanged" apps/core/internal/store/feed.go apps/core/effects_wiring.go
grep -n "^func NewMembers\|^func (t \*MemberToucher) Touch\|^type Loop" apps/core/internal/counter/*.go
grep -n "^func New\|) Guard(ctx context.Context, room uint64, at time.Time) (bool, error)" apps/core/internal/ownership/*.go
grep -n "^func MemberEvents\|^func MemberCountChanged\|^func MemberEventID\|^func MemberUserEventID\|^func MemberCountEventID" apps/core/internal/pbconv/*.go
grep -n "MemberCountDelay" apps/core/internal/config/*.go | head -3
grep -n "func ([a-z]* \*Rooms) \(MembersOf\|AddMembers\|ApplyMember\|BeginOwnerChange\|OwnerState\|CountMembers\)" apps/core/internal/store/memstore/*.go
grep -n "readUnbatched\|read_events_unbatched_total" apps/core/metrics_wiring.go apps/core/wiring.go
wc -l apps/core/wiring.go apps/core/metrics_wiring.go apps/core/effects_wiring.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: `MemberChanged ChangeKind = 6` trong `store/feed.go` và đúng một dòng registry tạm `store.MemberChanged: {activity.Effect()},` trong `effects_wiring.go` (Task 6); `counter.NewMembers`, `(*MemberToucher).Touch`, `type Loop`; `ownership.New`, `Guard`; năm hàm pbconv; `Config.MemberCountDelay` (Task 15); sáu method của `*memstore.Rooms`; `read_events_unbatched_total` đã có (Task 15); số dòng ba file wiring (ghi vào báo cáo); `INDEXES dirty=0`. Tên khác hợp đồng → chỉ đổi tên trong snippet dưới, ghi vào báo cáo; thiếu hẳn → dừng và báo cáo. `dirty=1` → dừng, hỏi controller.

**Files:**
- Modify: `apps/core/internal/effects/ports.go` (thêm 3 interface)
- Modify (thay cả file): `apps/core/internal/effects/event_publisher.go`
- Create: `apps/core/internal/effects/member_counter.go`, `owner_guard.go`, `member_event.go`
- Create: `apps/core/internal/effects/member_fixtures_test.go`, `member_counter_test.go`, `owner_guard_test.go`, `member_event_test.go`, `member_constructors_test.go`
- Modify (thay cả file): `apps/core/effects_wiring.go`, `apps/core/effects_wiring_test.go`
- Create: `apps/core/member_effects_wiring.go`
- Modify: `apps/core/metrics_wiring.go`, `apps/core/metrics_wiring_test.go`, `apps/core/wiring.go` (một dòng)
- Modify: `deploy/prometheus/alerts.yml`
- Modify: `INDEXES.csv`

`harness_test.go` (199 dòng), `fixtures_test.go`, `reaction_fixtures_test.go`, `effect_spies_test.go` không đổi. Hằng `tenant`, `room`, `otherRoom`, `delay`, `countDelay`, `countedAt`, `errBoom` và hàm `allNil`, `storedEventIDs`, `brokenStore` đã có trong package test.

**Step 0: Helper sửa `INDEXES.csv` (không commit)**

Task 16–20 sửa những dòng `INDEXES.csv` dài, có dấu phẩy. Dùng helper chỉ viết lại đúng dòng được chọn (dòng khác giữ nguyên từng byte), đặt ở `bin/` (gitignored). Nếu Part A/B đã tạo `bin/indexes_edit.py` với cùng cách dùng thì giữ bản đó.

`bin/indexes_edit.py`:

```python
import csv
import io
import sys

COLUMNS = {"purpose": 2, "key_symbols": 3, "used_by": 4, "tests": 5, "decisions": 6}


def encode(fields):
    out = io.StringIO()
    csv.writer(out, lineterminator="").writerow(fields)
    return out.getvalue()


def main():
    row, col, mode, text = sys.argv[1:5]
    new = sys.argv[5] if len(sys.argv) > 5 else None
    with open("INDEXES.csv", encoding="utf-8") as f:
        lines = f.read().split("\n")
    hits = [i for i, line in enumerate(lines) if line.startswith(row + ",")]
    if len(hits) != 1:
        sys.exit(f"{row}: {len(hits)} rows")
    if mode == "after":
        fields = next(csv.reader([text]))
        if len(fields) != 7:
            sys.exit(f"new row: {len(fields)} fields")
        lines.insert(hits[0] + 1, encode(fields))
    else:
        fields = next(csv.reader([lines[hits[0]]]))
        if len(fields) != 7:
            sys.exit(f"{row}: {len(fields)} fields")
        k = COLUMNS[col]
        if mode == "+":
            fields[k] += text
        elif mode == "=":
            fields[k] = text
        elif mode == "~":
            n = fields[k].count(text)
            if n != 1:
                sys.exit(f"{row} {col}: {n} matches of {text!r}")
            fields[k] = fields[k].replace(text, new)
        else:
            sys.exit(f"unknown mode {mode}")
        lines[hits[0]] = encode(fields)
    with open("INDEXES.csv", "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print(f"{row} {col} {mode} ok")


main()
```

Cách dùng (ở gốc repo): `python3 bin/indexes_edit.py <path> <cột> + "<nối thêm>"`, `… <cột> = "<giá trị mới>"`, `… <cột> ~ "<đoạn cũ>" "<đoạn mới>"` (đoạn cũ phải xuất hiện đúng một lần), `python3 bin/indexes_edit.py <path> - after '<dòng csv mới>'`. `csv.writer` tự đặt ngoặc kép cho field có dấu phẩy. Helper thoát lỗi khi không thấy đúng một dòng hoặc đúng một chỗ khớp: khi đó đọc dòng (`grep -n "^<path>," INDEXES.csv`) rồi sửa lệnh, không sửa tay. Sau mỗi lần dùng: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → `{7}`.

**Step 1: Port + fixture test**

`apps/core/internal/effects/ports.go`, thêm cuối file (import `time` thêm vào khối import):

```go
type MemberLookup interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
}

type MemberToucher interface {
	Touch(ctx context.Context, room uint64, cur domain.MemberCount, witnesses []store.Witness, tries int) (domain.MemberCount, bool, error)
}

type OwnerRepairer interface {
	Guard(ctx context.Context, room uint64, at time.Time) (bool, error)
}
```

`apps/core/internal/effects/member_fixtures_test.go`:

```go
package effects_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const directRoom uint64 = 4646

var memberAt = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

type memberTouchCall struct {
	room      uint64
	witnesses []store.Witness
	tries     int
}

func sameMemberTouch(a, b memberTouchCall) bool {
	return a.room == b.room && a.tries == b.tries && slices.Equal(a.witnesses, b.witnesses)
}

type spyMemberToucher struct {
	inner effects.MemberToucher
	calls []memberTouchCall
}

func (s *spyMemberToucher) Touch(ctx context.Context, r uint64, cur domain.MemberCount, ws []store.Witness, tries int) (domain.MemberCount, bool, error) {
	s.calls = append(s.calls, memberTouchCall{room: r, witnesses: slices.Clone(ws), tries: tries})
	return s.inner.Touch(ctx, r, cur, ws, tries)
}

type spyGuard struct {
	inner effects.OwnerRepairer
	err   error
	rooms []uint64
}

func (s *spyGuard) Guard(ctx context.Context, r uint64, at time.Time) (bool, error) {
	s.rooms = append(s.rooms, r)
	if s.err != nil {
		return false, s.err
	}
	return s.inner.Guard(ctx, r, at)
}

type brokenMembers struct{}

func (brokenMembers) MembersOf(context.Context, uint64, []string) ([]domain.Member, error) {
	return nil, errBoom
}

type memberRig struct {
	rooms   *memstore.Rooms
	js      *publishtest.JetStream
	touches *spyMemberToucher
	guards  *spyGuard
	counter *effects.MemberCounter
	guard   *effects.OwnerGuard
	event   *effects.MemberEvent
}

func newMemberRig(t *testing.T) *memberRig {
	t.Helper()
	rg := &memberRig{rooms: memstore.NewRooms(), js: &publishtest.JetStream{}}
	createRoomOf(t, rg.rooms, room, domain.RoomGroup, "alice", "bob", "carol")
	counts, err := counter.NewMembers(rg.rooms)
	if err != nil {
		t.Fatalf("counter.NewMembers: %v", err)
	}
	owners, err := ownership.New(rg.rooms)
	if err != nil {
		t.Fatalf("ownership.New: %v", err)
	}
	rg.touches, rg.guards = &spyMemberToucher{inner: counts}, &spyGuard{inner: owners}
	if rg.counter, err = effects.NewMemberCounter(
		effects.MemberCounterDeps{Counter: rg.touches, Rooms: rg.rooms, JS: rg.js, Now: func() time.Time { return countedAt }},
		effects.MemberCounterConfig{SubjectRoot: "evt", Delay: countDelay, Tries: 2},
	); err != nil {
		t.Fatalf("NewMemberCounter: %v", err)
	}
	if rg.guard, err = effects.NewOwnerGuard(
		effects.OwnerGuardDeps{Owners: rg.guards, Rooms: rg.rooms, Now: func() time.Time { return memberAt }},
		effects.OwnerGuardConfig{Delay: delay, RoomCache: 16},
	); err != nil {
		t.Fatalf("NewOwnerGuard: %v", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	if rg.event, err = effects.NewMemberEvent(effects.MemberEventDeps{Members: rg.rooms, Rooms: rg.rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewMemberEvent: %v", err)
	}
	return rg
}

func createRoomOf(t *testing.T, rooms *memstore.Rooms, id uint64, typ domain.RoomType, users ...string) {
	t.Helper()
	name := "team"
	if typ == domain.RoomDM {
		name = ""
	}
	r, members, err := domain.NewRoom(tenant, users[0], typ, name, users, memberAt, id)
	if err != nil {
		t.Fatalf("NewRoom(%d): %v", id, err)
	}
	if err := rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
}

func (rg *memberRig) member(t *testing.T, r uint64, user string) domain.Member {
	t.Helper()
	found, err := rg.rooms.MembersOf(t.Context(), r, []string{user})
	if err != nil || len(found) != 1 {
		t.Fatalf("MembersOf(%d, %s) = %+v, %v; want one doc", r, user, found, err)
	}
	return found[0]
}

func (rg *memberRig) add(t *testing.T, users ...string) []domain.Member {
	t.Helper()
	j := domain.Join{Room: room, Tenant: tenant, RequestID: "add-1", By: "alice", At: memberAt}
	added, err := rg.rooms.AddMembers(t.Context(), j, users)
	if err != nil {
		t.Fatalf("AddMembers(%v): %v", users, err)
	}
	return added
}

func (rg *memberRig) apply(t *testing.T, user string, role domain.Role, state domain.MemberState, requestID, by string) domain.Member {
	t.Helper()
	cur := rg.member(t, room, user)
	next := cur.Next(role, state, requestID, by, memberAt)
	if ok, err := rg.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("ApplyMember(%s) = %v, %v; want applied", user, ok, err)
	}
	return next
}

func memberRec(r uint64, user string, ver uint32) work.Record {
	return work.Record{Kind: store.MemberChanged, Room: r, User: user, Version: ver, CommittedAt: time.Now()}
}
```

Room `room` có `alice` (owner), `bob`, `carol` (member), mọi doc `ver 1`, `request_id = {room}-created`, `member_count 3`, `member_count_ver 0`. `rg.add` ghi doc `ver 1` bằng `AddMembers` của memstore (không đi qua `mutate`).

**Step 2: Test effect**

`apps/core/internal/effects/member_counter_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestMemberCounterDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).counter.Effect()
	if e.Name != effects.MemberCounterName || e.Delay != countDelay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MemberCounterName, countDelay)
	}
}

func TestMemberCounterRecountsEachRoomOnceWithTheNewestVersionOfEachUser(t *testing.T) {
	rg := newMemberRig(t)
	createRoomOf(t, rg.rooms, otherRoom, domain.RoomGroup, "alice", "dave")
	rg.add(t, "dave", "erin")
	rg.apply(t, "carol", domain.RoleMember, domain.MemberRemoved, "rm-carol", "alice")
	recs := []work.Record{memberRec(room, "dave", 1), memberRec(otherRoom, "dave", 1), memberRec(room, "carol", 2), memberRec(room, "erin", 1)}
	if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []memberTouchCall{
		{room: room, witnesses: []store.Witness{{User: "dave", N: 1}, {User: "carol", N: 2}, {User: "erin", N: 1}}, tries: 2},
		{room: otherRoom, witnesses: []store.Witness{{User: "dave", N: 1}}, tries: 2},
	}
	if !slices.EqualFunc(rg.touches.calls, want, sameMemberTouch) {
		t.Fatalf("touches = %+v, want one per room with the newest version of each user", rg.touches.calls)
	}
	head, err := rg.rooms.Get(t.Context(), room)
	if err != nil || head.MemberCount != 4 || head.MemberCountVer != 1 {
		t.Fatalf("room = %+v, %v; want 4 members at member count version 1", head, err)
	}
	if other, err := rg.rooms.Get(t.Context(), otherRoom); err != nil || other.MemberCount != 2 || other.MemberCountVer != 0 {
		t.Fatalf("other room = %+v, %v; want its count of 2 untouched", other, err)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberCountEventID(room, 1)}) {
		t.Fatalf("stored = %v, want only the changed room's count", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.member_count_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.MemberCountChanged(head, countedAt); err != nil || len(events) != 1 || !proto.Equal(events[0], want) {
		t.Fatalf("events = %v, %v; want %v", events, err, want)
	}
	if rg.counter.Republished() != 1 || rg.counter.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.counter.Republished(), rg.counter.Dropped())
	}
}

func TestMemberCounterSendsTheCurrentCountAgainWhenNothingChanged(t *testing.T) {
	rg := newMemberRig(t)
	rg.add(t, "dave")
	recs := []work.Record{memberRec(room, "dave", 1)}
	for range 2 {
		if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if head, err := rg.rooms.Get(t.Context(), room); err != nil || head.MemberCount != 4 || head.MemberCountVer != 1 {
		t.Fatalf("room = %+v, %v; want 4 members at version 1 after two runs", head, err)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.counter.Republished() != 1 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 1", len(rg.js.Stored()), len(rg.js.Attempts()), rg.counter.Republished())
	}
}

func TestMemberCounterRetriesARoomBehindItsRecords(t *testing.T) {
	rg := newMemberRig(t)
	errs := rg.counter.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1), memberRec(room, "carol", 3)})
	if len(errs) != 2 || !errors.Is(errs[0], store.ErrStaleRead) || !errors.Is(errs[1], store.ErrStaleRead) {
		t.Fatalf("errs = %v, want a stale read for every record of the room", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.counter.Dropped() != 0 {
		t.Fatalf("attempts %d, dropped %d; want neither for a room still behind", len(rg.js.Attempts()), rg.counter.Dropped())
	}
}

func TestMemberCounterDropsAGoneRoomAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.counter.Effect().Run(t.Context(), []work.Record{memberRec(999, "bob", 1), memberRec(999, "carol", 1)}); !allNil(errs, 2) || rg.counter.Dropped() != 2 {
		t.Fatalf("errs = %v, dropped %d; want both records of the missing room dropped", errs, rg.counter.Dropped())
	}
	eff, err := effects.NewMemberCounter(
		effects.MemberCounterDeps{Counter: rg.touches, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MemberCounterConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMemberCounter: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
```

`apps/core/internal/effects/owner_guard_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestOwnerGuardDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).guard.Effect()
	if e.Name != effects.OwnerGuardName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.OwnerGuardName, delay)
	}
}

func TestOwnerGuardFinishesAnOwnerChangeACoreLeftHalfDone(t *testing.T) {
	rg := newMemberRig(t)
	change := domain.OwnerChange{
		Action: domain.OwnerLeave, User: "alice", UserVer: 1, Successor: "bob", SuccessorVer: 1,
		RequestID: "leave-alice", UpdatedBy: "alice", UpdatedAt: memberAt,
	}
	if ok, err := rg.rooms.BeginOwnerChange(t.Context(), room, 0, change); err != nil || !ok {
		t.Fatalf("BeginOwnerChange = %v, %v", ok, err)
	}
	rg.apply(t, "bob", domain.RoleOwner, domain.MemberActive, "leave-alice", "alice")
	if errs := rg.guard.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	alice, bob := rg.member(t, room, "alice"), rg.member(t, room, "bob")
	if alice.State != domain.MemberRemoved || alice.Ver != 2 || alice.RequestID != "leave-alice" || alice.UpdatedBy != "alice" {
		t.Fatalf("alice = %+v, want left at version 2 by the pending change", alice)
	}
	if bob.Role != domain.RoleOwner || bob.State != domain.MemberActive || bob.Ver != 2 {
		t.Fatalf("bob = %+v, want the active owner at version 2", bob)
	}
	state, err := rg.rooms.OwnerState(t.Context(), room)
	if err != nil || state.Ver != 1 || state.Pending != nil {
		t.Fatalf("owner state = %+v, %v; want version 1 with nothing pending", state, err)
	}
	if rg.guard.Repaired() != 1 || !slices.Equal(rg.guards.rooms, []uint64{room}) {
		t.Fatalf("repaired %d, guarded %v; want one repair of the room", rg.guard.Repaired(), rg.guards.rooms)
	}
}

func TestOwnerGuardPromotesTheSuccessorOfAGroupLeftWithoutOwner(t *testing.T) {
	rg := newMemberRig(t)
	rg.apply(t, "carol", domain.RoleAdmin, domain.MemberActive, "admin-carol", "alice")
	rg.apply(t, "alice", domain.RoleOwner, domain.MemberRemoved, "lost-alice", "alice")
	if errs := rg.guard.Effect().Run(t.Context(), []work.Record{memberRec(room, "alice", 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	carol := rg.member(t, room, "carol")
	if carol.Role != domain.RoleOwner || carol.Ver != 3 || carol.PreviousRole != domain.RoleAdmin || carol.UpdatedBy != "" {
		t.Fatalf("carol = %+v, want the earliest admin promoted to owner at version 3 by the guard", carol)
	}
	if bob := rg.member(t, room, "bob"); bob.Role != domain.RoleMember || bob.Ver != 1 {
		t.Fatalf("bob = %+v, want a plain member untouched", bob)
	}
	state, err := rg.rooms.OwnerState(t.Context(), room)
	if err != nil || state.Ver != 1 || state.Pending != nil {
		t.Fatalf("owner state = %+v, %v; want version 1 with nothing pending", state, err)
	}
	if rg.guard.Repaired() != 1 {
		t.Fatalf("repaired %d, want 1", rg.guard.Repaired())
	}
}

func TestOwnerGuardChecksEachGroupOnceAndSkipsDirectRooms(t *testing.T) {
	rg := newMemberRig(t)
	createRoomOf(t, rg.rooms, otherRoom, domain.RoomGroup, "alice", "dave")
	createRoomOf(t, rg.rooms, directRoom, domain.RoomDM, "alice", "bob")
	recs := []work.Record{memberRec(room, "alice", 1), memberRec(otherRoom, "dave", 1), memberRec(room, "bob", 1), memberRec(directRoom, "bob", 1)}
	if errs := rg.guard.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	if !slices.Equal(rg.guards.rooms, []uint64{room, otherRoom}) || rg.guard.Repaired() != 0 || rg.guard.Dropped() != 0 {
		t.Fatalf("guarded %v, repaired %d, dropped %d; want each group once, no direct room, nothing repaired", rg.guards.rooms, rg.guard.Repaired(), rg.guard.Dropped())
	}
}

func TestOwnerGuardDropsAGoneRoomAndRetriesAFailedRoom(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.guard.Effect().Run(t.Context(), []work.Record{memberRec(999, "bob", 1)}); !allNil(errs, 1) || rg.guard.Dropped() != 1 || len(rg.guards.rooms) != 0 {
		t.Fatalf("errs = %v, dropped %d, guarded %v; want the missing room dropped before any guard", errs, rg.guard.Dropped(), rg.guards.rooms)
	}
	rg.guards.err = errBoom
	errs := rg.guard.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1), memberRec(room, "carol", 1)})
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || rg.guard.Repaired() != 0 {
		t.Fatalf("errs = %v, repaired %d; want both records of the room retried", errs, rg.guard.Repaired())
	}
}
```

Ghi chú: trong test "half done", bước thăng `bob` được làm tay đúng như `finish` sẽ làm (`Next(owner, active, request_id của pending, updated_by, updated_at)`), giả lập core chết sau bước đó; `Guard` nhận ra bước đã áp (`ver == SuccessorVer+1`, role owner, cùng `request_id`), rời `alice` theo `UserVer`, rồi xoá pending. Trong test "no owner", `alice` bị ghi tombstone thẳng qua `ApplyMember` (bỏ qua `ownership`), giả lập dữ liệu hỏng; kế nhiệm là admin `carol`, không phải member `bob`.

`apps/core/internal/effects/member_event_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberEventDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).event.Effect()
	if e.Name != effects.MemberEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MemberEventName, delay)
	}
}

func TestMemberEventPublishesTheRoomCopyAndTheUserCopy(t *testing.T) {
	rg := newMemberRig(t)
	dave := rg.add(t, "dave")[0]
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	ids := []string{pbconv.MemberEventID(room, "dave", 1), pbconv.MemberUserEventID(room, "dave", 1)}
	if got := storedEventIDs(rg.js); !slices.Equal(got, ids) {
		t.Fatalf("stored = %v, want %v", got, ids)
	}
	subjects := []string{"evt.acme.room.4242.member_added", "evt.acme.user.dave.member_added"}
	for i, m := range rg.js.Stored() {
		if m.Subject != subjects[i] {
			t.Fatalf("copy %d went to %q, want %q", i, m.Subject, subjects[i])
		}
	}
	events, err := rg.js.Events()
	fast := pbconv.MemberEvents(domain.RoomGroup, dave)
	if err != nil || len(events) != len(fast) {
		t.Fatalf("events = %v, %v; want %d", events, err, len(fast))
	}
	for i := range fast {
		if !proto.Equal(events[i], fast[i]) {
			t.Fatalf("copy %d = %v, want the fast path copy %v", i, events[i], fast[i])
		}
	}
	if rg.event.Republished() != 2 || rg.event.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.event.Republished(), rg.event.Dropped())
	}
}

func TestMemberEventSendsOnlyTheUserCopyOfADocCreatedWithTheRoom(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberUserEventID(room, "bob", 1)}) {
		t.Fatalf("stored = %v, want only bob's user copy", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.user.bob.member_added" {
		t.Fatalf("subject = %q", subj)
	}
}

func TestMemberEventSkipsANewerDocAndRetriesAnOlderOne(t *testing.T) {
	rg := newMemberRig(t)
	rg.add(t, "dave")
	rg.apply(t, "dave", domain.RoleMember, domain.MemberRemoved, "rm-dave", "alice")
	errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 1), memberRec(room, "dave", 3)})
	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || len(rg.js.Attempts()) != 0 {
		t.Fatalf("errs = %v, attempts %d; want version 1 skipped, version 3 retried, nothing sent", errs, len(rg.js.Attempts()))
	}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %v, %v; want the two copies of the removal", events, err)
	}
	removed := events[0].GetMemberRemoved()
	if removed.GetUser() != "dave" || removed.GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED || events[1].GetRecipient() != "dave" {
		t.Fatalf("events = %v, want dave removed by alice, the second copy addressed to dave", events)
	}
}

func TestMemberEventRetriesARecordUntilEveryCopyIsStored(t *testing.T) {
	rg := newMemberRig(t)
	rg.add(t, "dave")
	userCopy := pbconv.MemberUserEventID(room, "dave", 1)
	rg.js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == userCopy {
			return errBoom
		}
		return nil
	})
	errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 1)})
	if len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the record retried while a copy is missing", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberEventID(room, "dave", 1)}) {
		t.Fatalf("stored = %v", got)
	}
	rg.js.RefuseWhen(nil)
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 2 || rg.event.Republished() != 2 || rg.event.Dropped() != 0 {
		t.Fatalf("stored %d, republished %d, dropped %d; want 2, 2 and 0", len(rg.js.Stored()), rg.event.Republished(), rg.event.Dropped())
	}
}

func TestMemberEventDropsWhatIsGoneAndRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMemberRig(t)
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, "zed", 1), memberRec(999, "dave", 1)}); !allNil(errs, 2) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.event.Dropped() != 2 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 2 (no doc, no room)", len(rg.js.Attempts()), rg.event.Dropped())
	}
	eff, err := effects.NewMemberEvent(
		effects.MemberEventDeps{Members: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMemberEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{memberRec(room, "dave", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
```

`apps/core/internal/effects/member_constructors_test.go`:

```go
package effects_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewMemberEffectsRejectBadInput(t *testing.T) {
	rooms, js := memstore.NewRooms(), &publishtest.JetStream{}
	counts, guards := &spyMemberToucher{}, &spyGuard{}
	count, events := effects.MemberCounterConfig{SubjectRoot: "evt"}, effects.MessageChangedConfig{SubjectRoot: "evt"}
	counterDeps := effects.MemberCounterDeps{Counter: counts, Rooms: rooms, JS: js}
	eventDeps := effects.MemberEventDeps{Members: rooms, Rooms: rooms, JS: js}
	bad := map[string]func() error{
		"member_counter without a counter": func() error {
			_, err := effects.NewMemberCounter(effects.MemberCounterDeps{Rooms: rooms, JS: js}, count)
			return err
		},
		"member_counter without rooms": func() error {
			_, err := effects.NewMemberCounter(effects.MemberCounterDeps{Counter: counts, JS: js}, count)
			return err
		},
		"member_counter without js": func() error {
			_, err := effects.NewMemberCounter(effects.MemberCounterDeps{Counter: counts, Rooms: rooms}, count)
			return err
		},
		"member_counter without a root": func() error {
			_, err := effects.NewMemberCounter(counterDeps, effects.MemberCounterConfig{})
			return err
		},
		"member_counter with a negative delay": func() error {
			_, err := effects.NewMemberCounter(counterDeps, effects.MemberCounterConfig{SubjectRoot: "evt", Delay: -time.Second})
			return err
		},
		"owner_guard without owners": func() error {
			_, err := effects.NewOwnerGuard(effects.OwnerGuardDeps{Rooms: rooms}, effects.OwnerGuardConfig{})
			return err
		},
		"owner_guard without rooms": func() error {
			_, err := effects.NewOwnerGuard(effects.OwnerGuardDeps{Owners: guards}, effects.OwnerGuardConfig{})
			return err
		},
		"member_event without members": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Rooms: rooms, JS: js}, events)
			return err
		},
		"member_event without rooms": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Members: rooms, JS: js}, events)
			return err
		},
		"member_event without js": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Members: rooms, Rooms: rooms}, events)
			return err
		},
		"member_event without a root": func() error {
			_, err := effects.NewMemberEvent(eventDeps, effects.MessageChangedConfig{})
			return err
		},
	}
	for name, build := range bad {
		if err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	c, err := effects.NewMemberCounter(counterDeps, count)
	if err != nil || c.Effect().Delay != effects.DefaultMemberCountDelay {
		t.Fatalf("member_counter with defaults = %v, %v; want delay %v", c, err, effects.DefaultMemberCountDelay)
	}
	g, err := effects.NewOwnerGuard(effects.OwnerGuardDeps{Owners: guards, Rooms: rooms}, effects.OwnerGuardConfig{})
	if err != nil || g.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("owner_guard with defaults = %v, %v; want delay %v", g, err, effects.DefaultDelay)
	}
	e, err := effects.NewMemberEvent(eventDeps, events)
	if err != nil || e.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("member_event with defaults = %v, %v; want delay %v", e, err, effects.DefaultDelay)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: effects.MemberCounter`, `undefined: effects.NewMemberCounter`, `undefined: effects.MemberCounterDeps`, `undefined: effects.OwnerGuard`, `undefined: effects.NewOwnerGuard`, `undefined: effects.MemberEvent`, `undefined: effects.NewMemberEvent`, `undefined: effects.MemberCounterName` (và các tên còn lại của ba file). Port ở Step 1 đã có nên không báo `MemberToucher`, `OwnerRepairer`, `MemberLookup`.

**Step 4: Code effect**

`apps/core/internal/effects/event_publisher.go` (thay cả file; `eventConfig`, `init`, `queue`, hai accessor giữ nguyên):

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type eventPublisher struct {
	js          publish.JetStream
	root        string
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func eventConfig(name string, cfg MessageChangedConfig) (MessageChangedConfig, error) {
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return MessageChangedConfig{}, fmt.Errorf("%w: %s config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, name, cfg)
	}
	return cfg, nil
}

func (p *eventPublisher) init(js publish.JetStream, root string, rooms RoomReader, cache int) {
	p.js, p.root, p.types = js, root, newRoomTypes(rooms, cache)
}

func (p *eventPublisher) Republished() uint64 { return p.republished.Load() }

func (p *eventPublisher) Dropped() uint64 { return p.dropped.Load() }

func (p *eventPublisher) each(ctx context.Context, recs []work.Record, build func(context.Context, work.Record) (*chatimv1.Event, error), drop func(error) bool) []error {
	return p.eachMany(ctx, recs, func(ctx context.Context, r work.Record) ([]*chatimv1.Event, error) {
		ev, err := build(ctx, r)
		if err != nil || ev == nil {
			return nil, err
		}
		return []*chatimv1.Event{ev}, nil
	}, drop)
}

func (p *eventPublisher) eachMany(ctx context.Context, recs []work.Record, build func(context.Context, work.Record) ([]*chatimv1.Event, error), drop func(error) bool) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		evs, err := build(ctx, r)
		switch {
		case drop(err):
			p.dropped.Add(1)
		case err != nil:
			errs[i] = err
		default:
			for _, ev := range evs {
				pending = p.queue(pending, errs, i, r.Room, ev)
			}
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&p.republished))
	return errs
}

func (p *eventPublisher) queue(pending []pendingAck, errs []error, i int, room uint64, ev *chatimv1.Event) []pendingAck {
	msg, err := publish.Message(p.root, room, ev)
	if err != nil {
		p.dropped.Add(1)
		return pending
	}
	return send(p.js, msg, i, errs, pending)
}
```

Nhiều `pendingAck` cùng `index` là hợp lệ: `awaitAcks` chỉ ghi `errs[index]` khi một future lỗi, không xoá lỗi đã có, nên một bản lỗi là cả record lỗi. `drop(nil)` của `reactionGone`, `pinGone`, `memberGone` đều false (`errors.Is(nil, x)` false), nên nhánh `default` chạy đúng như `each` cũ.

`apps/core/internal/effects/member_counter.go`:

```go
package effects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	MemberCounterName       = "member_counter"
	DefaultMemberCountDelay = time.Second
)

type MemberCounterDeps struct {
	Counter MemberToucher
	Rooms   RoomReader
	JS      publish.JetStream
	Now     func() time.Time
}

type MemberCounterConfig struct {
	SubjectRoot string
	Delay       time.Duration
	Tries       int
}

type MemberCounter struct {
	eventPublisher
	deps MemberCounterDeps
	cfg  MemberCounterConfig
}

func NewMemberCounter(deps MemberCounterDeps, cfg MemberCounterConfig) (*MemberCounter, error) {
	if deps.Counter == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs a member counter, rooms and a jetstream client", apperr.ErrInvalidArgument, MemberCounterName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultMemberCountDelay)
	cfg.Tries = cmp.Or(cfg.Tries, DefaultCounterTries)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.Tries < 1 {
		return nil, fmt.Errorf("%w: %s config %+v needs a subject root, a delay and a try", apperr.ErrInvalidArgument, MemberCounterName, cfg)
	}
	c := &MemberCounter{deps: deps, cfg: cfg}
	c.js, c.root = deps.JS, cfg.SubjectRoot
	return c, nil
}

func (c *MemberCounter) Effect() Effect {
	return Effect{Name: MemberCounterName, Delay: c.cfg.Delay, Run: c.run}
}

func (c *MemberCounter) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	groups := groupRecords(recs, recordRoom)
	var pending []pendingAck
	for _, g := range groups {
		ev, err := c.recount(ctx, g.key, witnessesOf(recs, g.indexes))
		switch {
		case errors.Is(err, domain.ErrRoomNotFound):
			g.drop(&c.dropped)
		case err != nil:
			g.fail(errs, err)
		case ev != nil:
			pending = c.queue(pending, errs, g.indexes[0], g.key, ev)
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&c.republished))
	for _, g := range groups {
		g.share(errs)
	}
	return errs
}

func (c *MemberCounter) recount(ctx context.Context, room uint64, witnesses []store.Witness) (*chatimv1.Event, error) {
	r, err := c.deps.Rooms.Get(ctx, room)
	if err != nil {
		return nil, err
	}
	got, _, err := c.deps.Counter.Touch(ctx, room, domain.MemberCount{Count: r.MemberCount, Ver: r.MemberCountVer}, witnesses, c.cfg.Tries)
	if err != nil {
		return nil, err
	}
	if got.Ver == 0 {
		return nil, nil
	}
	r.MemberCount, r.MemberCountVer = got.Count, got.Ver
	return pbconv.MemberCountChanged(r, c.deps.Now().UTC().Truncate(time.Millisecond)), nil
}
```

`types` của `eventPublisher` để nil: `member_counter` đọc loại room từ chính `Rooms.Get`, không dùng cache.

`apps/core/internal/effects/owner_guard.go`:

```go
package effects

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const OwnerGuardName = "owner_guard"

type OwnerGuardDeps struct {
	Owners OwnerRepairer
	Rooms  RoomReader
	Now    func() time.Time
}

type OwnerGuardConfig struct {
	Delay     time.Duration
	RoomCache int
}

type OwnerGuard struct {
	deps     OwnerGuardDeps
	delay    time.Duration
	types    *roomTypes
	repaired atomic.Uint64
	dropped  atomic.Uint64
}

func NewOwnerGuard(deps OwnerGuardDeps, cfg OwnerGuardConfig) (*OwnerGuard, error) {
	if deps.Owners == nil || deps.Rooms == nil {
		return nil, fmt.Errorf("%w: %s needs an owner repairer and rooms", apperr.ErrInvalidArgument, OwnerGuardName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.Delay < 0 || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: %s config %+v needs a delay and a room cache", apperr.ErrInvalidArgument, OwnerGuardName, cfg)
	}
	return &OwnerGuard{deps: deps, delay: cfg.Delay, types: newRoomTypes(deps.Rooms, cfg.RoomCache)}, nil
}

func (e *OwnerGuard) Effect() Effect {
	return Effect{Name: OwnerGuardName, Delay: e.delay, Run: e.run}
}

func (e *OwnerGuard) Repaired() uint64 { return e.repaired.Load() }

func (e *OwnerGuard) Dropped() uint64 { return e.dropped.Load() }

func (e *OwnerGuard) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		err := e.guard(ctx, g.key)
		switch {
		case errors.Is(err, domain.ErrRoomNotFound):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		}
	}
	return errs
}

func (e *OwnerGuard) guard(ctx context.Context, room uint64) error {
	typ, err := e.types.get(ctx, room)
	if err != nil || typ == domain.RoomDM {
		return err
	}
	repaired, err := e.deps.Owners.Guard(ctx, room, e.deps.Now().UTC().Truncate(time.Millisecond))
	if err == nil && repaired {
		e.repaired.Add(1)
	}
	return err
}
```

`apps/core/internal/effects/member_event.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MemberEventName = "member_event"

var errNoMember = fmt.Errorf("member doc %w", apperr.ErrNotFound)

type MemberEventDeps struct {
	Members MemberLookup
	Rooms   RoomReader
	JS      publish.JetStream
}

type MemberEvent struct {
	eventPublisher
	members MemberLookup
	delay   time.Duration
}

func NewMemberEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberEvent, error) {
	if deps.Members == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs members, rooms and a jetstream client", apperr.ErrInvalidArgument, MemberEventName)
	}
	cfg, err := eventConfig(MemberEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &MemberEvent{members: deps.Members, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *MemberEvent) Effect() Effect {
	return Effect{Name: MemberEventName, Delay: e.delay, Run: e.run}
}

func (e *MemberEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.eachMany(ctx, recs, e.events, memberGone)
}

func memberGone(err error) bool { return undeliverable(err) || errors.Is(err, errNoMember) }

func (e *MemberEvent) events(ctx context.Context, r work.Record) ([]*chatimv1.Event, error) {
	found, err := e.members.MembersOf(ctx, r.Room, []string{r.User})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, errNoMember
	}
	doc := found[0]
	switch {
	case doc.Ver > r.Version:
		return nil, nil
	case doc.Ver < r.Version:
		return nil, store.ErrStaleRead
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.MemberEvents(typ, doc), nil
}
```

`undeliverable` (có sẵn) = `ErrRoomNotFound` hoặc `ErrInvalidArgument`, nên room không còn và doc hỏng đều drop có đếm, giống `reaction_event`.

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."
make -s go ARGS="test -race -count=3 ./apps/core/internal/effects/..."
wc -l apps/core/internal/effects/*.go | sort -n | tail -8
```

Expected: PASS cả hai lần (test cũ của `msg_created`, `msg_changed`, `room_created`, `edit_projection`, `reaction_*`, `pin_*`, workers không đổi: `each` giờ đi qua `eachMany`). Mỗi file < 200 (`event_publisher.go` ~77, `member_counter.go` ~100, `owner_guard.go` ~84, `member_event.go` ~76, `member_fixtures_test.go` ~151, `member_counter_test.go` ~106, `owner_guard_test.go` ~95, `member_event_test.go` ~136, `member_constructors_test.go` ~83; `harness_test.go` vẫn 199).

**Step 6: Wiring, metric, luật**

`apps/core/member_effects_wiring.go`:

```go
package main

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireMemberEffects(cfg config.Config, cl *clients, st *mongostore.Store) error {
	counts, err := counter.NewMembers(st)
	if err != nil {
		return fmt.Errorf("wire member counter: %w", err)
	}
	owners, err := ownership.New(st)
	if err != nil {
		return fmt.Errorf("wire owner coordinator: %w", err)
	}
	if fx.memberCounter, err = effects.NewMemberCounter(
		effects.MemberCounterDeps{Counter: counts, Rooms: st, JS: cl.effectsJS},
		effects.MemberCounterConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.MemberCountDelay},
	); err != nil {
		return fmt.Errorf("wire member_counter effect: %w", err)
	}
	if fx.ownerGuard, err = effects.NewOwnerGuard(
		effects.OwnerGuardDeps{Owners: owners, Rooms: st},
		effects.OwnerGuardConfig{Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	); err != nil {
		return fmt.Errorf("wire owner_guard effect: %w", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache}
	if fx.memberEvent, err = effects.NewMemberEvent(effects.MemberEventDeps{Members: st, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire member_event effect: %w", err)
	}
	return nil
}
```

`apps/core/effects_wiring.go` (thay cả file; so với HEAD sau Task 6 chỉ khác: ba field mới, lời gọi `wireMemberEffects`, dòng registry `MemberChanged` cuối thay dòng tạm, ba dòng `counters`; nếu file sau Task 6 còn khác gì ngoài dòng registry tạm thì dừng và báo):

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type effectSet struct {
	workers         *effects.Workers
	msgCreated      *effects.MessageCreated
	roomCreated     *effects.RoomCreated
	editProjection  *effects.EditProjection
	msgChanged      *effects.MessageChanged
	reactionCounter *effects.ReactionCounter
	reactionEvent   *effects.ReactionEvent
	pinProjection   *effects.PinProjection
	pinEvent        *effects.PinEvent
	memberCounter   *effects.MemberCounter
	ownerGuard      *effects.OwnerGuard
	memberEvent     *effects.MemberEvent
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, log *slog.Logger) (effectSet, error) {
	fx := effectSet{}
	var err error
	fx.msgCreated, err = effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: marks, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_created effect: %w", err)
	}
	fx.roomCreated, err = effects.NewRoomCreated(
		effects.RoomCreatedDeps{Rooms: st, JS: cl.effectsJS},
		effects.RoomCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire room_created effect: %w", err)
	}
	fx.editProjection, err = effects.NewEditProjection(effects.EditProjectionDeps{Edits: st, Messages: st, Purger: st})
	if err != nil {
		return effectSet{}, fmt.Errorf("wire edit_projection effect: %w", err)
	}
	fx.msgChanged, err = effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: st, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_changed effect: %w", err)
	}
	if err = fx.wireReactionPinEffects(cfg, cl, st); err != nil {
		return effectSet{}, err
	}
	if err = fx.wireMemberEffects(cfg, cl, st); err != nil {
		return effectSet{}, err
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), fx.msgCreated.Effect()},
		store.RoomInserted:    {fx.roomCreated.Effect()},
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged: {activity.Effect(), fx.reactionCounter.Effect(), fx.reactionEvent.Effect()},
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
		store.MemberChanged:   {activity.Effect(), fx.memberCounter.Effect(), fx.ownerGuard.Effect(), fx.memberEvent.Effect()},
	}
	fx.workers, err = effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p, cfg.Effects.RetryDelay) },
		Owner:    owner,
		Registry: registry,
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return fx, nil
}

func (fx effectSet) counters() map[string]effectCounters {
	return map[string]effectCounters{
		fx.msgCreated.Effect().Name:      {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:     {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:      {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name:  {dropped: fx.editProjection.Dropped},
		fx.reactionCounter.Effect().Name: {republished: fx.reactionCounter.Republished, dropped: fx.reactionCounter.Dropped},
		fx.reactionEvent.Effect().Name:   {republished: fx.reactionEvent.Republished, dropped: fx.reactionEvent.Dropped},
		fx.pinEvent.Effect().Name:        {republished: fx.pinEvent.Republished, dropped: fx.pinEvent.Dropped},
		fx.pinProjection.Effect().Name:   {dropped: fx.pinProjection.Dropped},
		fx.memberCounter.Effect().Name:   {republished: fx.memberCounter.Republished, dropped: fx.memberCounter.Dropped},
		fx.ownerGuard.Effect().Name:      {dropped: fx.ownerGuard.Dropped},
		fx.memberEvent.Effect().Name:     {republished: fx.memberEvent.Republished, dropped: fx.memberEvent.Dropped},
	}
}

func (fx effectSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}
}
```

`apps/core/metrics_wiring.go` (sửa tại chỗ, không thay cả file vì Task 15 đã thêm `read_events_unbatched_total`):
- `type probes struct`: thêm dòng `ownerRepaired  func() uint64` ngay sau dòng `counterRepairs map[string]func() uint64` (gofmt căn cột).
- `metricSources`: ngay sau dòng `out = append(out, counterSources(p.counterRepairs)...)` thêm:

```go
	out = append(out, metrics.Source{
		Name: "owner_repaired_total",
		Help: "Groups the owner_guard effect found without an owner or with an unfinished owner change and repaired.",
		Read: func() float64 { return float64(p.ownerRepaired()) },
	})
```

- `workerSources`: thay hằng `dropHelp` bằng:

```go
	const dropHelp = "Work records an effect gave up on (missing room, message, edit, reaction, pin or member doc, or a corrupt document)."
```

`apps/core/wiring.go`: trong literal `p := probes{…}`, ngay sau dòng `counterRepairs: fx.counterRepairs(),` thêm dòng `ownerRepaired:  fx.ownerGuard.Repaired,` (gofmt căn cột). Không đổi dòng nào khác.

`apps/core/metrics_wiring_test.go` (sửa tại chỗ):
- `fakeProbes`: map `effectCounts` thêm ba mục sau dòng `"pin_projection": …`:

```go
			"member_counter": {republished: func() uint64 { return 9 }, dropped: func() uint64 { return 10 }},
			"owner_guard":    {dropped: func() uint64 { return 11 }},
			"member_event":   {republished: func() uint64 { return 12 }, dropped: func() uint64 { return 13 }},
```

  và sau dòng `counterRepairs: …` thêm `ownerRepaired: func() uint64 { return 14 },`.
- `TestCoreMetricSourcesCoverEveryGuarantee`: slice `everyCore` thêm `"owner_repaired_total"` cuối danh sách.
- `TestEffectMetricsReadTheirEffectByLabel`: map `want` thêm:

```go
		"reconcile_republished_total{member_counter}": 9, "effect_dropped_total{member_counter}": 10,
		"effect_dropped_total{owner_guard}":           11,
		"reconcile_republished_total{member_event}":   12, "effect_dropped_total{member_event}": 13,
		"owner_repaired_total":                        14,
```

  và `for _, silent := range []string{"edit_projection", "pin_projection"}` → `[]string{"edit_projection", "pin_projection", "owner_guard"}`.

(Số 9–14 không trùng giá trị nào của `fakeProbes` ở HEAD; Task 15 có thể đã dùng một số cho `readUnbatched`: nếu trùng thì đổi số của Task này, ghi vào báo cáo. gofmt căn lại cột của các map; chạy `make fmt-check`.)

`apps/core/effects_wiring_test.go` (thay cả file):

```go
package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/ownership"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type noMarks struct{}

func (noMarks) Acked(_ context.Context, keys []store.MsgKey) ([]bool, error) {
	return make([]bool, len(keys)), nil
}

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func TestEffectSetExportsEveryEffect(t *testing.T) {
	msgs, rooms, edits := memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits()
	reactions, pins, js := memstore.NewReactions(), memstore.NewPins(), &publishtest.JetStream{}
	events := effects.MessageChangedConfig{SubjectRoot: "evt"}
	fx := effectSet{
		msgCreated: built(effects.NewMessageCreated(
			effects.MessageCreatedDeps{Marks: noMarks{}, Messages: msgs, Rooms: rooms, JS: js}, effects.MessageCreatedConfig{SubjectRoot: "evt"}))(t),
		roomCreated:    built(effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt"}))(t),
		editProjection: built(effects.NewEditProjection(effects.EditProjectionDeps{Edits: edits, Messages: msgs, Purger: edits}))(t),
		msgChanged:     built(effects.NewMessageChanged(effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		reactionCounter: built(effects.NewReactionCounter(
			effects.ReactionCounterDeps{Messages: msgs, Counter: built(counter.New(msgs, reactions))(t), Rooms: rooms, JS: js},
			effects.ReactionCounterConfig{SubjectRoot: "evt"}))(t),
		reactionEvent: built(effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: rooms, JS: js}, events))(t),
		pinProjection: built(effects.NewPinProjection(built(pinproj.New(pins, rooms))(t)))(t),
		pinEvent:      built(effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		memberCounter: built(effects.NewMemberCounter(
			effects.MemberCounterDeps{Counter: built(counter.NewMembers(rooms))(t), Rooms: rooms, JS: js},
			effects.MemberCounterConfig{SubjectRoot: "evt"}))(t),
		ownerGuard:  built(effects.NewOwnerGuard(effects.OwnerGuardDeps{Owners: built(ownership.New(rooms))(t), Rooms: rooms}, effects.OwnerGuardConfig{}))(t),
		memberEvent: built(effects.NewMemberEvent(effects.MemberEventDeps{Members: rooms, Rooms: rooms, JS: js}, events))(t),
	}
	counters := fx.counters()
	want := []string{
		effects.EditProjectionName, effects.MemberCounterName, effects.MemberEventName, effects.MessageChangedName, effects.MessageCreatedName,
		effects.OwnerGuardName, effects.PinEventName, effects.PinProjectionName, effects.ReactionCounterName, effects.ReactionEventName,
		effects.RoomCreatedName,
	}
	if got := slices.Sorted(maps.Keys(counters)); !slices.Equal(got, want) {
		t.Fatalf("effects with metrics = %v, want %v", got, want)
	}
	for name, c := range counters {
		silent := name == effects.EditProjectionName || name == effects.PinProjectionName || name == effects.OwnerGuardName
		if c.dropped == nil || (c.republished == nil) != silent {
			t.Errorf("%s: dropped set %v, republished set %v; want dropped always and republished only when it publishes", name, c.dropped != nil, c.republished != nil)
		}
	}
	if repairs := fx.counterRepairs(); len(repairs) != 1 || repairs[pbconv.ReactionsCounter] == nil {
		t.Errorf("counter repairs = %v, want only %q (member_counter is the write path, not a repair)", repairs, pbconv.ReactionsCounter)
	}
}
```

(`want` đã sắp theo chữ: `edit_projection` < `member_counter` < `member_event` < `msg_changed` < `msg_created` < `owner_guard` < `pin_event` < `pin_projection` < `reaction_counter` < `reaction_event` < `room_created`.)

`deploy/prometheus/alerts.yml`: thêm cuối file (cùng thụt lề với luật trên):

```yaml
      - alert: ChatimOwnerRepaired
        expr: increase(chatim_core_owner_repaired_total[15m]) > 0
        labels:
          severity: warning
          guarantee: OW1
        annotations:
          summary: The owner_guard effect repaired a group without an owner or with an unfinished owner change; a core likely died during an owner change.
```

**Step 7: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/... ./apps/core/"
make alerts-check
wc -l apps/core/*.go | sort -n | tail -6
```

Expected: PASS (itest skip); `make alerts-check` in `SUCCESS: 17 rules found`. Mỗi file < 200 (`effects_wiring.go` ~103, `member_effects_wiring.go` ~39, `effects_wiring_test.go` ~75, `metrics_wiring.go` ~135, `metrics_wiring_test.go` ~125). `wiring.go` theo Task 15 cộng 1 dòng: ghi số dòng vào báo cáo; **> 200 thì** chuyển khối dựng `p := probes{…}` (cùng `if rec != nil { p.reconcile = rec.Stats }`) sang hàm `func coreProbes(cl *clients, router *actor.Router, cids *dedupe.Registry, …) probes` trong file mới `apps/core/probes_wiring.go` (chỉ di chuyển, giữ nguyên field; tham số đúng các biến khối đó dùng), ghi vào báo cáo.

**Step 8: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok`. Registry cuối dựng ba effect member trên Mongo/NATS thật; record member của mọi room tạo trong itest giờ chạy `member_counter` (recount bằng số lúc tạo → không ghi), `owner_guard` (group có owner → không sửa), `member_event` (bản user của doc tạo cùng room là bản trùng của fast path). Itest riêng cho member ở Task 17 và 19.

**Step 9: INDEXES + commit + review**

```bash
python3 bin/indexes_edit.py apps/core/internal/effects purpose + "; member_counter effect (delay MEMBER_COUNT_DELAY, 1s) for MemberChanged: one recount per room per batch through counter.MemberToucher with witnesses (user, max ver), Rooms.Get then Touch, member_count_ver > 0 republishes member_count_changed {room}-members-v{ver}; the only writer of member_count, not counted as a counter repair; owner_guard effect (delay RECONCILE_DELAY): once per group per batch (DMs skipped) calls ownership.Coordinator.Guard, counts repairs; member_event effect (delay RECONCILE_DELAY, no ack mark): MembersOf(room, [user]), ver equal publishes pbconv.MemberEvents (room copy and user copy), newer skips, older is a stale read; the record is acked only when every copy has a PubAck; eventPublisher.eachMany publishes several events per record and each wraps it; only a missing room is dropped by member_counter and owner_guard"
python3 bin/indexes_edit.py apps/core/internal/effects key_symbols + ";MemberCounter;NewMemberCounter;MemberCounterDeps;MemberCounterConfig;MemberCounterName;DefaultMemberCountDelay;OwnerGuard;NewOwnerGuard;OwnerGuardDeps;OwnerGuardConfig;OwnerGuardName;OwnerGuard.Repaired;MemberEvent;NewMemberEvent;MemberEventDeps;MemberEventName;MemberLookup;MemberToucher;OwnerRepairer"
python3 bin/indexes_edit.py apps/core/internal/effects decisions + ";D100;D102;D103;D104;D108"
python3 bin/indexes_edit.py apps/core purpose + "; member_effects_wiring.go builds member_counter (counter.NewMembers), owner_guard (ownership.New) and member_event; registry MemberChanged -> room_activity, member_counter, owner_guard, member_event; owner_repaired_total metric"
python3 bin/indexes_edit.py apps/core key_symbols + ";effectSet.wireMemberEffects"
python3 bin/indexes_edit.py apps/core decisions + ";D102;D104;D108"
python3 bin/indexes_edit.py deploy/prometheus/alerts.yml purpose "~" "16 alert rules" "17 alert rules"
python3 bin/indexes_edit.py deploy/prometheus/alerts.yml purpose + "; OW1 owner repaired (ChatimOwnerRepaired); MB1 uses the work failing, effect dropping and republish surge rules"
python3 bin/indexes_edit.py deploy/prometheus/alerts.yml decisions + ";D108"
grep -o "MemberChanged[^;\"]*room_activity[^;\"]*" INDEXES.csv
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/effects/member_counter.go apps/core/internal/effects/owner_guard.go apps/core/internal/effects/member_event.go apps/core/internal/effects/member_fixtures_test.go apps/core/internal/effects/member_counter_test.go apps/core/internal/effects/owner_guard_test.go apps/core/internal/effects/member_event_test.go apps/core/internal/effects/member_constructors_test.go apps/core/member_effects_wiring.go
git commit -m "feat(effects): recount members, guard owners and republish member events" -- apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/member_effects_wiring.go apps/core/effects_wiring_test.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/wiring.go deploy/prometheus/alerts.yml INDEXES.csv
git show --stat HEAD
```

Expected: mỗi lệnh helper in `… ok`; lệnh `grep -o` in đoạn registry tạm Task 6 đã viết trong dòng `apps/core` (nếu có, ví dụ `MemberChanged -> room_activity only until the member effects land`): xoá đúng đoạn đó bằng `python3 bin/indexes_edit.py apps/core purpose "~" "<đoạn in ra>" "MemberChanged -> room_activity, member_counter, owner_guard, member_event"` và ghi vào báo cáo; `{7}`; fmt/vet/lint sạch; `git show --stat HEAD` chỉ các file trên (+ `probes_wiring.go` nếu Step 7 phải tách). Không push (push ở Task 20).

Task rủi ro: một reviewer (thứ tự và delay trong registry; `member_counter` là đường ghi duy nhất, chỉ drop `ErrRoomNotFound`, không đếm repair; `owner_guard` bỏ DM, chỉ đếm repair khi `Guard` không lỗi; `member_event` drop/skip/Nak đúng ba nhánh `ver`; `eachMany` không đổi hành vi `each` cũ; metric + 17 luật; tối đa `-count=3` trên `effects`).

---

### Task 17: `/app resync` quét doc `members`

Resync (D81) quét timeline chính, `message_edits`, `reactions`, `pin_actions` (M2b.2–M2b.3). Đổi member không đổi `last_seq`, nên phải quét riêng: sau `pin_actions`, mỗi room gọi `Members.MembersBetween(room, from, to, store.MaxMemberScan)` qua `scanByTime` (phân trang theo `updated_at`, bỏ trùng mép trang theo id record, một thời điểm đầy cả trang → `ErrMemberPageFull`). Record `{Kind: MemberChanged, Room, User, Version: m.Ver, CommittedAt: m.UpdatedAt}`; worker chạy `room_activity`, `member_counter`, `owner_guard`, `member_event` như record từ feed (D103).

Thứ tự trong một room: room record → tin (ngược) → fact sửa → reaction → ghim → member. Worker không dựa vào thứ tự này.

Hệ quả (lớp tập): doc đổi nhiều lần trong khoảng mất chỉ cho record của `ver` cuối, nên event trung gian không được phát lại (chỉ trạng thái cuối, như reaction). Room tạo **trong khoảng** cho thêm record của từng doc member tạo cùng room (`updated_at` = giờ tạo). Giới hạn giống reaction/ghim (D91): resync chọn room theo `activity_bucket`/`created_at`; room chỉ có đổi member trong khoảng mất không được chọn, phải chạy `-room`. Không index thời gian trên `members`: `MembersBetween` dùng tiền tố `room_id` của index đếm rồi lọc `updated_at`, tức đọc mọi doc của room (chấp nhận cho công cụ thủ công; nhóm 5K ≈ 5K khoá).

Trước khi bắt đầu, kiểm tên của Part A (chỉ đọc):

```bash
grep -n "MembersBetween" apps/core/internal/store/member.go apps/core/internal/store/memstore/*.go apps/core/internal/store/mongostore/*.go
grep -n "MaxMemberScan\|MemberChanged" apps/core/internal/store/*.go
grep -n '"g:"' apps/core/internal/work/*.go
grep -n "func (w world) room" -A8 apps/core/internal/resync/scan_test.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: `MembersBetween(ctx, room, from, to, limit) ([]domain.Member, error)` ở port, memstore và mongostore; `store.MaxMemberScan`, `store.MemberChanged`; `work.Record.ID()` có nhánh `"g:"` (Task 6); `world.room` còn dựng member bằng literal không `Ver/UpdatedAt` (nếu Part A đã đổi sang `domain.NewRoom`, giữ bản đó khi chuyển ở Step 1 và bỏ phần đổi `room` ở Step 2); `dirty=0`. Khác thì dừng và báo cáo.

**Files:**
- Create: `apps/core/internal/resync/world_test.go` (tách từ `scan_test.go`, Step 1)
- Modify: `apps/core/internal/resync/scan_test.go`, `edits_test.go`, `reactions_pins_test.go`
- Create: `apps/core/internal/resync/members.go`, `members_test.go`
- Modify: `apps/core/internal/resync/scan.go` (`Deps`, `Report`, `room`)
- Modify: `apps/core/resync_command.go`, `apps/core/resync_integration_test.go`
- Modify: `INDEXES.csv`

**Step 1: Tách `scan_test.go` (chỉ di chuyển, không đổi hành vi)**

`scan_test.go` đang 187 dòng. Chuyển phần dựng thế giới sang file mới; năm test ở lại.

`apps/core/internal/resync/world_test.go`: khối import, khối `const` (`busyRoom`…`foreignRoom`), khối `var` (`lostFrom`…`target`), `publishSpy` + hai method, `world`, `newWorld`, `world.room`, `world.messages`, `world.deps` — chép **nguyên văn** từ `scan_test.go` (dòng 1–118 ở HEAD trừ các import chỉ test dùng). Khối import của `world_test.go`:

```go
import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)
```

`apps/core/internal/resync/scan_test.go`: xoá các khối đã chuyển, giữ nguyên năm test; khối import còn lại:

```go
import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)
```

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."
wc -l apps/core/internal/resync/scan_test.go apps/core/internal/resync/world_test.go
make fmt-check && make vet && make lint
git add apps/core/internal/resync/world_test.go
git commit -m "test(resync): move the scan test world into its own file" -- apps/core/internal/resync/scan_test.go apps/core/internal/resync/world_test.go
```

Expected: PASS y hệt trước; `scan_test.go` ~75 dòng, `world_test.go` ~120; commit chỉ hai file.

**Step 2: Test**

`apps/core/internal/resync/world_test.go`, hai chỗ:

1. `world.room` dựng room và doc owner đủ field bằng `domain.NewRoom` (doc `alice` có `ver 1`, `state 1`, `updated_at = created`), nên room tạo trong khoảng có record member của nó:

```go
func (w world) room(t *testing.T, id uint64, tenant string, created time.Time) {
	t.Helper()
	r, members, err := domain.NewRoom(tenant, "alice", domain.RoomGroup, "Team", []string{"alice"}, created, id)
	if err != nil {
		t.Fatalf("NewRoom(%d): %v", id, err)
	}
	if err := w.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
}
```

2. `world.deps` thêm `Members`, và thêm hàm id của doc tạo cùng room (import thêm `github.com/ivannguyendev/chatim/apps/core/internal/work`):

```go
func (w world) deps(pub resync.Publisher) resync.Deps {
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Reactions: w.reactions, Pins: w.pins, Members: w.rooms, Pub: pub}
}

func creatorRecord(room uint64) string {
	return work.Record{Kind: store.MemberChanged, Room: room, User: "alice", Version: 1}.ID()
}
```

`apps/core/internal/resync/scan_test.go`, hai chỗ:
- `TestResyncPublishesRecordsOfTheLostRangeOnly`: sau dòng `want = append(want, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID())` thêm `want = append(want, creatorRecord(newRoom))`; điều kiện báo cáo thành `rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1})`, thông điệp `"report = %+v, want 2 rooms, 1 room record, 61 message records and the new room's creator"`.
- `TestResyncDryRunCountsWithoutPublishingOrPacing`: `resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, DryRun: true}` → `resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1, DryRun: true}`.
- Ba test còn lại không đổi (`staleRoom` tạo ở `lostFrom − 48h`, doc owner ngoài khoảng; test lỗi publish dừng ở record đầu).

`apps/core/internal/resync/edits_test.go`, `TestResyncPublishesEditsOfTheLostRangeAfterTheTimeline`, thay phần từ `if want := …` tới hết hàm:

```go
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, MemberRecords: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	roomRecord := work.Record{Kind: store.RoomInserted, Room: newRoom}.ID()
	if len(got) != 64 || got[61] != inRange || got[62] != roomRecord || got[63] != creatorRecord(newRoom) {
		t.Fatalf("published %d ids ending %v, want 61 messages, then %s, %s and %s", len(got), got[max(0, len(got)-3):], inRange, roomRecord, creatorRecord(newRoom))
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=0 pin_records=0 member_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}
```

`apps/core/internal/resync/reactions_pins_test.go`, `TestResyncPublishesReactionsAndPinsOfTheLostRangeAfterTheEdits`, thay phần từ `want := resync.Report{…}` tới hết hàm:

```go
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, ReactionRecords: 2, PinRecords: 1, MemberRecords: 1}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	tail := []string{edit, changed, removed, pinned, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), creatorRecord(newRoom)}
	if len(got) != 67 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-6):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=2 pin_records=1 member_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}
```

`apps/core/internal/resync/members_test.go`:

```go
package resync_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (w world) join(t *testing.T, room uint64, user string, at time.Time) domain.Member {
	t.Helper()
	j := domain.Join{Room: room, Tenant: "acme", RequestID: "add-" + user, By: "alice", At: at}
	added, err := w.rooms.AddMembers(t.Context(), j, []string{user})
	if err != nil || len(added) != 1 {
		t.Fatalf("AddMembers(%d, %s) = %+v, %v", room, user, added, err)
	}
	return added[0]
}

func (w world) leave(t *testing.T, cur domain.Member, at time.Time) domain.Member {
	t.Helper()
	next := cur.Next(cur.Role, domain.MemberRemoved, "leave-"+cur.User, cur.User, at)
	if ok, err := w.rooms.ApplyMember(t.Context(), cur, next); err != nil || !ok {
		t.Fatalf("ApplyMember(%s leaves) = %v, %v", cur.User, ok, err)
	}
	return next
}

func memberRecordID(m domain.Member) string {
	return work.Record{Kind: store.MemberChanged, Room: m.Room, User: m.User, Version: m.Ver}.ID()
}

func TestResyncPublishesTheCurrentMemberDocsOfTheLostRangeLast(t *testing.T) {
	w := newWorld(t)
	dave := w.leave(t, w.join(t, busyRoom, "dave", lostFrom.Add(5*time.Minute)), lostFrom.Add(7*time.Minute))
	w.join(t, busyRoom, "erin", lostTo.Add(time.Minute))
	pinned := w.pin(t, busyRoom, 1, 40, lostFrom.Add(6*time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, PinRecords: 1, MemberRecords: 2}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	tail := []string{pinned, memberRecordID(dave), work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), creatorRecord(newRoom)}
	if len(got) != 65 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v (dave once, at his last version)", len(got), got[max(0, len(got)-4):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=0 reaction_records=0 pin_records=1 member_records=2 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesMemberDocsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for i := range 1200 {
		w.join(t, staleRoom, "u"+strconv.Itoa(i+1), lostFrom.Add(time.Duration((i+1)/3)*time.Millisecond))
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberRecords: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 member records, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreMemberChangesThanAPage(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	for i := range 1001 {
		w.join(t, staleRoom, "u"+strconv.Itoa(i+1), at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrMemberPageFull) || rep.MemberRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrMemberPageFull after one full page", rep, err)
	}
}
```

`w.pin` có sẵn ở `reactions_pins_test.go`. `dave` vào ở +5m (ver 1) rồi rời ở +7m (ver 2): doc hiện tại chỉ còn ver 2 nên chỉ một record. `erin` vào sau `lostTo` nên ngoài khoảng.

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: FAIL biên dịch: `unknown field Members in struct literal of type resync.Deps`, `unknown field MemberRecords in struct literal of type resync.Report`, `undefined: resync.ErrMemberPageFull`.

**Step 4: Code**

`apps/core/internal/resync/members.go`:

```go
package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var ErrMemberPageFull = errors.New("resync: one instant holds more member changes than a member page")

type Members interface {
	MembersBetween(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Member, error)
}

func (s *scanner) members(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Member]{
		name:    "member changes",
		limit:   store.MaxMemberScan,
		full:    ErrMemberPageFull,
		between: s.deps.Members.MembersBetween,
		record:  memberRecord,
		counted: &s.rep.MemberRecords,
	})
}

func memberRecord(m domain.Member) work.Record {
	return work.Record{Kind: store.MemberChanged, Room: m.Room, User: m.User, Version: m.Ver, CommittedAt: m.UpdatedAt}
}
```

`apps/core/internal/resync/scan.go`, ba chỗ:

```go
type Deps struct {
	Rooms     Rooms
	Pages     Pages
	Edits     Edits
	Reactions Reactions
	Pins      Pins
	Members   Members
	Pub       Publisher
}
```

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords, ReactionRecords, PinRecords, MemberRecords int
	DryRun                                                                                      bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d reaction_records=%d pin_records=%d member_records=%d dry_run=%t",
		r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.ReactionRecords, r.PinRecords, r.MemberRecords, r.DryRun)
}
```

Trong `room`: `[]func(context.Context, uint64) error{s.timeline, s.edits, s.reactions, s.pins}` → `[]func(context.Context, uint64) error{s.timeline, s.edits, s.reactions, s.pins, s.members}`.

(gofmt căn cột `DryRun`; chạy `make fmt-check`.)

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."
wc -l apps/core/internal/resync/*.go
```

Expected: PASS, gồm ba test mới và các test cũ đã sửa ở Step 2. Mỗi file < 200 (`scan.go` ~166, `members.go` ~32, `members_test.go` ~86, `scan_test.go` ~77, `world_test.go` ~128).

**Step 6: Wiring + diễn tập**

`apps/core/resync_command.go`: dòng `deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Pub: c.js}` → `deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Members: st, Pub: c.js}`.

`apps/core/resync_integration_test.go` (`TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`), bốn chỗ:

1. Ngay sau khối `if err := itPins(st).Append(t.Context(), pin); err != nil { … }`, thêm (ghi thẳng một doc member, giả lập lệnh mà reader bỏ lỡ):

```go
	joined := domain.Join{Room: room, Tenant: itTenant, RequestID: "migrated-1", By: "migrator", At: marked, ReadSeq: 3}
	if _, err := st.AddMembers(t.Context(), joined, []string{"migrated"}); err != nil {
		t.Fatalf("add a member the reader missed: %v", err)
	}
```

2. Dòng `want = append(want, pbconv.ReactionEventID(…), pbconv.ReactionCountsEventID(…), pbconv.PinEventID(room, 1))` thêm cuối danh sách đối số: `pbconv.MemberEventID(room, "migrated", 1), pbconv.MemberCountEventID(room, 1)`.
3. Chuỗi báo cáo mong đợi thành `"resync rooms=1 room_records=1 message_records=3 edit_records=1 reaction_records=1 pin_records=1 member_records=3 dry_run=false"`, thông điệp `"resync output = %q, want one room, its room record, three message, one edit, one reaction, one pin and three member records (alice and bob created with the room, the missed add)"`.
4. Cuối hàm (sau kiểm `pins after resync`), thêm:

```go
	head, err := st.Get(t.Context(), room)
	if err != nil || head.MemberCount != 3 || head.MemberCountVer != 1 {
		t.Fatalf("room after resync = %+v, %v; want the recount to see 3 members at member count version 1", head, err)
	}
	found, err := st.MembersOf(t.Context(), room, []string{"migrated"})
	if err != nil || len(found) != 1 || found[0].State != domain.MemberActive || found[0].ReadSeq != 3 {
		t.Fatalf("migrated after resync = %+v, %v; want active, reading from seq 3", found, err)
	}
```

`createRoom` tạo room `alice` (owner) + `bob` qua gRPC: hai doc `ver 1` có `updated_at` trong khoảng resync, `member_count 2`, `member_count_ver` chưa có. Reader tắt (`RECONCILE_ENABLED=false`) nên không worker nào chạy trước resync; ba record member tới worker sau resync, recount thấy 3 doc active ≠ 2 → CAS ver 1 → `{room}-members-v1`; `member_event` phát bản room `{room}-mb-migrated-v1` (bản user tới subject user, không subscribe ở đây); bản user của `alice`, `bob` đã có từ fast path `CreateRoom` (Task 13), worker gửi lại là bản trùng. `assertNoLiveIDs` trước resync phủ cả hai id mới (không gì phát chúng khi reader tắt).

**Step 7: Chạy**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/resync/..."
make infra-up && make itest
```

Expected: PASS (itest skip ở lệnh đầu); `make itest` mọi package `ok`, gồm `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (log `live events [… <room>-p1 <room>-mb-migrated-v1 <room>-members-v1] arrived …`). `wc -l apps/core/resync_integration_test.go` → ~105.

**Step 8: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py apps/core/internal/resync purpose + "; last per room: members docs with updated_at in range through MembersBetween (room_id index prefix, no time index; record MemberChanged with the doc's current ver, so a doc changed twice in the range yields one record; ErrMemberPageFull)"
python3 bin/indexes_edit.py apps/core/internal/resync key_symbols + ";Members;ErrMemberPageFull;Report.MemberRecords"
python3 bin/indexes_edit.py apps/core/internal/resync decisions + ";D103"
python3 bin/indexes_edit.py apps/core tests + ";itest (resync drill also replays a member added straight to Mongo: member_event republishes it and member_counter recounts the room)"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/resync/members.go apps/core/internal/resync/members_test.go
git commit -m "feat(resync): replay member docs changed in the lost range" -- apps/core/internal/resync/ apps/core/resync_command.go apps/core/resync_integration_test.go INDEXES.csv
git show --stat HEAD
```

Expected: các lệnh helper in `ok`; `{7}`; sạch; commit có 9 file (`members.go`, `members_test.go`, `scan.go`, `scan_test.go`, `world_test.go`, `edits_test.go`, `reactions_pins_test.go`, `resync_command.go`, `resync_integration_test.go`) + `INDEXES.csv`.

Task trung bình: controller kiểm nhanh (thứ tự quét, `scanByTime` dùng chung, test cũ chỉ đổi đúng phần `member_records`), không reviewer.

---

### Task 18: Route + corecli + e2e member và vị trí đọc

Ba phần, hai commit: (1) `tools/internal/route` có sáu lệnh mới; (2) `tools/corecli` có sáu lệnh tay, `watch -user`, và bước e2e `members` (phase 5 của `scripts/e2e.sh`) kiểm cả reply lẫn event trên subject room, subject user và subject của một DM.

**Hợp đồng chỉnh (Part C):** hợp đồng chung ghi `remove-member (-room -target)` và `set-role (-room -target -role …)` vì `-user` đã là cờ người gọi của mọi lệnh corecli (`addOptions`); `watch` không dùng `addOptions` nên dùng `-user` cho subject user.

Lệnh member và đọc đều idempotent theo hợp đồng (`AddMembers` qua `request_id`; xoá/rời/đổi role là trạng thái mong muốn; `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ), nên route gửi qua `inRoom` và retry như các lệnh idempotent khác (`Unavailable`, `ResourceExhausted`, `DeadlineExceeded`, `Aborted`), giữ nguyên request (cùng `request_id`). Retry sau một lần đã thành công nhưng mất reply: `AddMembers` trả đúng kết quả cũ; lệnh khác thấy no-op (`changed` false).

**Kịch bản e2e phase 5 (`corecli e2e members`)**, chạy trên room e2e của phase 1 (chỉ có `e2e-user` là owner, doc `ver 1`, `member_count 1`), sau phase 4. N = seq cuối đã ack (80 với mặc định). Bước tự nối NATS (`-nats`, `-live-root`) và subscribe trước mọi lệnh: subject room `live.e2e.room.{room}.>` và subject user của `e2e-user`, `e2e-bob`, `e2e-carol` (`live.e2e.user.{u}.>`); subject của DM được subscribe ngay sau khi tạo DM. "Chờ" = chờ mọi event mong đợi tới lúc đó (theo id, kind, subject, recipient, payload; bản trùng và id khác bỏ qua) trong `-wait` (45s).

| # | Hành động (người gọi) | Reply mong đợi | Event mong đợi (id → subject) |
|---|---|---|---|
| 1 | `e2e-user` tạo DM với `e2e-bob`; trên DM: thêm `e2e-carol`, xoá `e2e-bob`, rời, đổi role `e2e-bob`; `e2e-user` gửi một tin vào DM; `e2e-bob` `MarkRead(0)` trong DM | bốn lệnh member `FAILED_PRECONDITION`; tin seq 1; `{read_seq 1, read_ver 1}` | `{dm}-mb-e2e-user-v1-u` → user e2e-user, `{dm}-mb-e2e-bob-v1-u` → user e2e-bob (`member_added` của doc tạo cùng room, không bản room); `{dm}-rd-e2e-bob-v1` → **room** DM (`read_updated`, DM luôn phát cho room) |
| 2 | `e2e-user` thêm `e2e-bob`, `e2e-carol` với `request_id` `e2e-add-1`; lặp lại cùng `request_id` | cả hai lần `added [bob:1, carol:1]` (lần hai là kết quả cũ) | `{room}-mb-e2e-bob-v1` → room, `…-u` → user bob; như vậy cho carol (`member_added`, role member); `{room}-members-v1` → room (`member_count_changed` 3). **Chờ** |
| 3 | `e2e-bob` đọc toàn bộ lịch sử (như `e2e check`); `MarkRead(0)` | lịch sử đủ N tin; `{N, 1}` (vào ở tin mới nhất nên không đổi) | — |
| 4 | `e2e-user` đặt `e2e-bob` = admin; lặp lại | `{changed, ver 2, previous member}`; lặp `{ver 2}` | `{room}-mb-e2e-bob-v2` → room, `…-u` → user bob (`member_role_changed` admin ← member) |
| 5 | `e2e-bob` (admin) xoá `e2e-user` (owner); `e2e-bob` xoá `e2e-carol`; lặp lại | `PERMISSION_DENIED`; `{changed, ver 2}`; lặp `{ver 2}` | `{room}-mb-e2e-carol-v2` → room, `…-u` → user carol (`member_removed` removed, previous member); `{room}-members-v2` (2). **Chờ** |
| 6 | `e2e-carol` gửi tin, đọc lịch sử, `MarkRead` | cả ba `PERMISSION_DENIED` | — |
| 7 | `e2e-user` gửi lại `AddMembers(e2e-bob, e2e-carol)` cùng `request_id` `e2e-add-1`; `e2e-carol` đọc lịch sử | `added []` (không thêm lại carol); `PERMISSION_DENIED` | — |
| 8 | `e2e-bob` `MarkUnread(N)`, rồi `MarkRead(0)` | `{N−1, 2}`, rồi `{N, 3}` | `{room}-rd-e2e-bob-v2` (gửi ngay) và `{room}-rd-e2e-bob-v3` (phần đuôi sau `READ_RECEIPT_WINDOW`) → room (2 member ≤ 20) |
| 9 | `e2e-bob` `ClearHistory`; `e2e-user` gửi tin seq N+1; `e2e-bob` đọc trang LATEST 2 tin | có `cleared_before_time`; seq N+1; seq N `hidden`, seq N+1 hiện text | — |
| 10 | `e2e-user` thêm 20 người `e2e-m01..e2e-m20` (`request_id` `e2e-add-2`); **chờ** count; `e2e-bob` `MarkRead(0)` | `added` 20 người ver 1; `{N+1, 4}` | `{room}-members-v3` (22) → room; `{room}-rd-e2e-bob-v4` → **user** bob, `recipient` bob (22 > 20) |
| 11 | `e2e-user` (owner cuối) rời; lặp lại; `e2e-user` đọc lịch sử | `{changed, ver 2, new_owner e2e-bob}`; lặp `{ver 2}`; `PERMISSION_DENIED` | `{room}-mb-e2e-bob-v3` → room + user bob (`member_role_changed` owner ← admin); `{room}-mb-e2e-user-v2` → room + user e2e-user (`member_removed` left, previous owner) |
| 12 | `e2e-bob` (owner mới) thêm lại `e2e-user` (`request_id` `e2e-add-3`), lặp; đặt `e2e-user` = owner, lặp; `e2e-user` `MarkRead(0)` | `added [e2e-user:3]` hai lần; `{changed, ver 4, previous member}`/`{ver 4}`; `{N+1, 1}` | `{room}-mb-e2e-user-v3` (`member_added` member) và `{room}-mb-e2e-user-v4` (`member_role_changed` owner ← member), mỗi cái bản room + bản user |

Cuối bước: chờ đủ **25** event. Rồi phase 5 check (`e2e check` như cũ, người gọi `e2e-user` đã vào lại; state có thêm ack N+1 nên lịch sử là N+1 tin, live có `msg_created` của N+1 từ watcher phase 1). Watcher room vẫn ghi `events.jsonl`; `EventOf` giờ giải mã event member/đọc nhưng `CheckEvents`, `CheckChangeEvents`, `CheckMarkEvents` lọc theo kind nên bỏ qua chúng.

Ghi chú:
- Bước 2, 5, 10 chờ để `member_count_ver` tất định (recount gộp theo lô: nếu bước sau đổi member trước khi recount bước trước chạy thì hai bước chung một ver). Bước 11–12 không kiểm count (rời rồi vào lại trong < 1s có thể gộp thành không đổi).
- Bước 3 chứng minh "người mới thấy toàn bộ lịch sử, tin cũ coi như đã đọc"; bước 7 chứng minh retry trễ cùng `request_id` không thêm lại người đã bị xoá; bước 9 chứng minh clear theo thời gian (tin sau mốc vẫn hiện); bước 10 chứng minh nhóm lớn phát `read_updated` cho subject user; bước 1 chứng minh DM phát cho room; bước 11–12 chứng minh owner cuối rời → admin vào sớm nhất thành owner, người rời mất quyền đọc, thêm lại đọc lại được và vị trí đọc nâng lên tin mới nhất.
- Reply no-op của đổi role và rời: e2e chỉ so `changed` và `ver` (hợp đồng không chốt `previous_role`, `new_owner` của no-op).

Trước khi bắt đầu, kiểm tên proto và tên trùng (chỉ đọc):

```bash
grep -n "func (c \*coreServiceClient) \(AddMembers\|RemoveMember\|LeaveRoom\|ChangeMemberRole\|MarkRead\|MarkUnread\)" pkg/pb/chatim/v1/core_grpc.pb.go
grep -n "MemberRole_MEMBER_ROLE_\(OWNER\|ADMIN\|MEMBER\) \|MemberRemovedReason_MEMBER_REMOVED_REASON_\(LEFT\|REMOVED\) " pkg/pb/chatim/v1/*.pb.go | head
grep -n "func (x \*Event) GetRecipient\|func (x \*Event) GetMemberCountChanged\|func (x \*ReadUpdated) GetReadVer\|func (x \*AddedMember) GetVer\|func (x \*ClearHistoryResponse) GetClearedBeforeTime" pkg/pb/chatim/v1/*.pb.go
grep -n "func wantCode\|func twice\|liveEvents\|func openLive\|memberScenario" tools/corecli/*.go
grep -n "func RoomSubject\|func UserSubject\|type Scope" tools/corecli/internal/e2e/*.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: sáu method client; ba hằng role và hai hằng reason; năm getter; hai grep tên trùng không in gì; `dirty=0`. Khác → dừng và báo cáo.

**Files:**
- Create: `tools/internal/route/members.go`, `tools/internal/route/fakes_members_test.go`
- Modify: `tools/internal/route/changes_test.go`
- Create: `tools/corecli/internal/e2e/members.go`, `members_check.go`, `members_test.go`
- Modify: `tools/corecli/internal/e2e/events.go`
- Create: `tools/corecli/cmd_members.go`
- Modify: `tools/corecli/cmd_watch.go`, `tools/corecli/main.go`, `tools/corecli/cmd_e2e.go`
- Create: `tools/corecli/e2e_members.go`, `e2e_member_join.go`, `e2e_member_read.go`, `e2e_member_owner.go`, `e2e_member_calls.go`, `e2e_member_live.go`
- Modify: `scripts/e2e.sh`
- Modify: `INDEXES.csv`

**Step 1: Test route**

`tools/internal/route/changes_test.go`, map `changeCalls` thêm sáu mục (sau `"unpin"`):

```go
	"add-members": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"bob", "carol"}, RequestId: "add-1"}))
	},
	"remove-member": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: room, User: "carol"}))
	},
	"leave": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: room}))
	},
	"set-role": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}))
	},
	"read": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: room, Seq: 9}))
	},
	"unread": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: 9}))
	},
```

map `changeReplies` thêm:

```go
	"add-members":   &chatimv1.AddMembersResponse{Added: []*chatimv1.AddedMember{{User: "bob", Ver: 1}, {User: "carol", Ver: 1}}},
	"remove-member": &chatimv1.RemoveMemberResponse{Changed: true, Ver: 2},
	"leave":         &chatimv1.LeaveRoomResponse{Changed: true, Ver: 2, NewOwner: "bob"},
	"set-role":      &chatimv1.ChangeMemberRoleResponse{Changed: true, Ver: 2, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER},
	"read":          &chatimv1.MarkReadResponse{ReadSeq: 9, ReadVer: 2},
	"unread":        &chatimv1.MarkUnreadResponse{ReadSeq: 8, ReadVer: 3},
```

(gofmt căn lại cột cả map.) Hai test cũ (`TestChangeCallsRouteByRoomAndRetryAttemptTimeouts`, `TestChangeCallsKeepConflictsAndBadRoomIDsLocal`) chạy mọi mục của map, nên phủ cả sáu lệnh mới: route theo room, retry khi attempt timeout, room id sai bị từ chối tại chỗ.

`tools/internal/route/fakes_members_test.go`:

```go
package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) AddMembers(ctx context.Context, _ *chatimv1.AddMembersRequest, _ ...grpc.CallOption) (*chatimv1.AddMembersResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.AddMembersResponse{Added: []*chatimv1.AddedMember{{User: "bob", Ver: 1}, {User: "carol", Ver: 1}}}, nil
}

func (f *fakeCore) RemoveMember(ctx context.Context, _ *chatimv1.RemoveMemberRequest, _ ...grpc.CallOption) (*chatimv1.RemoveMemberResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{Changed: true, Ver: 2}, nil
}

func (f *fakeCore) LeaveRoom(ctx context.Context, _ *chatimv1.LeaveRoomRequest, _ ...grpc.CallOption) (*chatimv1.LeaveRoomResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{Changed: true, Ver: 2, NewOwner: "bob"}, nil
}

func (f *fakeCore) ChangeMemberRole(ctx context.Context, _ *chatimv1.ChangeMemberRoleRequest, _ ...grpc.CallOption) (*chatimv1.ChangeMemberRoleResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{Changed: true, Ver: 2, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER}, nil
}

func (f *fakeCore) MarkRead(ctx context.Context, in *chatimv1.MarkReadRequest, _ ...grpc.CallOption) (*chatimv1.MarkReadResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: in.GetSeq(), ReadVer: 2}, nil
}

func (f *fakeCore) MarkUnread(ctx context.Context, in *chatimv1.MarkUnreadRequest, _ ...grpc.CallOption) (*chatimv1.MarkUnreadResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: in.GetSeq() - 1, ReadVer: 3}, nil
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: FAIL biên dịch: `c.AddMembers undefined (type *route.Client has no field or method AddMembers)` (và `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`).

**Step 3: Code route**

`tools/internal/route/members.go`:

```go
package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.AddMembersResponse, error) {
		return api.AddMembers(ctx, req)
	})
}

func (c *Client) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.RemoveMemberResponse, error) {
		return api.RemoveMember(ctx, req)
	})
}

func (c *Client) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.LeaveRoomResponse, error) {
		return api.LeaveRoom(ctx, req)
	})
}

func (c *Client) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ChangeMemberRoleResponse, error) {
		return api.ChangeMemberRole(ctx, req)
	})
}

func (c *Client) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkReadResponse, error) {
		return api.MarkRead(ctx, req)
	})
}

func (c *Client) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkUnreadResponse, error) {
		return api.MarkUnread(ctx, req)
	})
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: PASS (`TestChangeCallsRouteByRoomAndRetryAttemptTimeouts` với 14 lệnh, 28 lần hỏi locator). `wc -l tools/internal/route/changes_test.go tools/internal/route/members.go` → ~145 và ~45.

**Step 5: Commit route**

```bash
python3 bin/indexes_edit.py tools/internal/route purpose + "; member and read position calls AddMembers (request_id kept across retries), RemoveMember, LeaveRoom, ChangeMemberRole, MarkRead, MarkUnread routed by room and retried like the other idempotent calls"
python3 bin/indexes_edit.py tools/internal/route key_symbols + ";Client.AddMembers;Client.RemoveMember;Client.LeaveRoom;Client.ChangeMemberRole;Client.MarkRead;Client.MarkUnread"
python3 bin/indexes_edit.py tools/internal/route decisions + ";D99;D105"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add tools/internal/route/members.go tools/internal/route/fakes_members_test.go
git commit -m "feat(route): route member and read position calls by room" -- tools/internal/route/ INDEXES.csv
```

Expected: `ok` từng lệnh helper, `{7}`, sạch; commit 3 file + `INDEXES.csv`.

**Step 6: Test package e2e**

`tools/corecli/internal/e2e/members_test.go`:

```go
package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

var scope = e2e.Scope{Root: "live", Tenant: "e2e", Room: "42"}

func TestMemberIDsAndSubjects(t *testing.T) {
	got := []string{
		e2e.MemberEventID("42", "bob", 3), e2e.MemberUserEventID("42", "bob", 3), e2e.MemberCountEventID("42", 2), e2e.ReadEventID("42", "bob", 5),
		e2e.RoomSubject("live", "e2e", "42"), e2e.UserSubject("live", "e2e", "bob"),
		e2e.RoleName(chatimv1.MemberRole_MEMBER_ROLE_ADMIN), e2e.RoleName(chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED),
		e2e.FormatAdded([]*chatimv1.AddedMember{{User: "carol", Ver: 1}, {User: "bob", Ver: 3}}), e2e.FormatAdded(nil),
	}
	want := []string{"42-mb-bob-v3", "42-mb-bob-v3-u", "42-members-v2", "42-rd-bob-v5", "live.e2e.room.42", "live.e2e.user.bob", "admin", "", "bob:3,carol:1", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEventOfReadsMemberAndReadEvents(t *testing.T) {
	added := &chatimv1.MemberAdded{User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, Ver: 1}
	cases := []struct {
		subject string
		ev      *chatimv1.Event
		want    e2e.Event
	}{
		{
			"live.e2e.room.42.evt.member_added",
			&chatimv1.Event{Id: "42-mb-bob-v1", RoomId: "42", Payload: &chatimv1.Event_MemberAdded{MemberAdded: added}},
			scope.Added("bob", "member", 1)[0],
		},
		{
			"live.e2e.user.bob.evt.member_added",
			&chatimv1.Event{Id: "42-mb-bob-v1-u", RoomId: "42", Recipient: "bob", Payload: &chatimv1.Event_MemberAdded{MemberAdded: added}},
			scope.Added("bob", "member", 1)[1],
		},
		{
			"live.e2e.user.carol.evt.member_removed",
			&chatimv1.Event{Id: "42-mb-carol-v2-u", RoomId: "42", Recipient: "carol", Payload: &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
				User: "carol", Reason: chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, Ver: 2,
			}}},
			scope.Removed("carol", e2e.ReasonRemoved, "member", 2)[1],
		},
		{
			"live.e2e.room.42.evt.member_role_changed",
			&chatimv1.Event{Id: "42-mb-bob-v3", RoomId: "42", Payload: &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
				User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, Ver: 3,
			}}},
			scope.RoleChanged("bob", "owner", "admin", 3)[0],
		},
		{
			"live.e2e.room.42.evt.member_count_changed",
			&chatimv1.Event{Id: "42-members-v2", RoomId: "42", Payload: &chatimv1.Event_MemberCountChanged{MemberCountChanged: &chatimv1.MemberCountChanged{MemberCount: 22, MemberCountVer: 2}}},
			scope.CountChanged(22, 2),
		},
		{
			"live.e2e.user.bob.evt.read_updated",
			&chatimv1.Event{Id: "42-rd-bob-v4", RoomId: "42", Recipient: "bob", Payload: &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "bob", ReadSeq: 81, ReadVer: 4}}},
			scope.Read("bob", 81, 4, false),
		},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf(c.subject, c.ev)
		if !ok || got != c.want || got.IsCreated() || got.IsChange() || got.IsMark() {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v and no message kind", c.ev.GetId(), got, ok, c.want)
		}
	}
}

func TestCheckLiveFindsEachWantedEventByID(t *testing.T) {
	added, removed := scope.Added("bob", "member", 1)[0], scope.Removed("carol", e2e.ReasonRemoved, "member", 2)[0]
	want := []e2e.Event{added, removed}
	other := scope.Read("bob", 7, 9, true)
	missing, err := e2e.CheckLive(want, []e2e.Event{added, other, added})
	if err != nil || len(missing) != 1 || missing[0] != removed.ID {
		t.Fatalf("CheckLive = %v, %v; want only %s missing (duplicates and other ids ignored)", missing, err, removed.ID)
	}
	if missing, err := e2e.CheckLive(want, []e2e.Event{removed, added}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckLive = %v, %v; want nothing missing", missing, err)
	}
	wrong := removed
	wrong.Subject = e2e.UserSubject("live", "e2e", "carol") + ".evt." + e2e.KindMemberRemoved
	if _, err := e2e.CheckLive(want, []e2e.Event{added, wrong}); err == nil {
		t.Fatal("CheckLive accepted a wanted id on the wrong subject")
	}
}

func TestCheckMemberReplyAndCleared(t *testing.T) {
	want := e2e.MemberReply{Changed: true, Ver: 2, NewOwner: "bob"}
	if err := e2e.CheckMemberReply("leave", want, want); err != nil {
		t.Fatalf("equal replies: %v", err)
	}
	if err := e2e.CheckMemberReply("leave", e2e.MemberReply{Ver: 2}, want); err == nil {
		t.Fatal("CheckMemberReply accepted a no-op where a change was wanted")
	}
	page := []*chatimv1.Message{{Seq: 80, Hidden: true}, {Seq: 81, Text: e2e.TextFor("after")}}
	if err := e2e.CheckCleared(page, 80, "after"); err != nil {
		t.Fatalf("CheckCleared: %v", err)
	}
	for _, bad := range [][]*chatimv1.Message{
		{{Seq: 80, Text: "still shown"}, {Seq: 81, Text: e2e.TextFor("after")}},
		{{Seq: 80, Hidden: true}, {Seq: 81, Hidden: true}},
		{{Seq: 80, Hidden: true}},
	} {
		if err := e2e.CheckCleared(bad, 80, "after"); err == nil {
			t.Fatalf("CheckCleared accepted %v", bad)
		}
	}
}
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/internal/e2e/..."`
Expected: FAIL biên dịch: `undefined: e2e.Scope`, `undefined: e2e.MemberEventID`, `undefined: e2e.CheckLive`, `undefined: e2e.MemberReply`, `undefined: e2e.CheckCleared`, `undefined: e2e.FormatAdded` (và các tên khác của file).

**Step 8: Code package e2e**

`tools/corecli/internal/e2e/events.go`, hai chỗ:
- `type Event` (thêm bốn field trước `Subject`; gofmt căn tag):

```go
type Event struct {
	Kind      string `json:"kind,omitempty"`
	Room      string `json:"room"`
	ID        string `json:"id"`
	Seq       uint64 `json:"seq"`
	CID       string `json:"cid"`
	User      string `json:"user,omitempty"`
	Version   uint32 `json:"version,omitempty"`
	Text      string `json:"text,omitempty"`
	Role      string `json:"role,omitempty"`
	Count     int32  `json:"count,omitempty"`
	ReadVer   uint64 `json:"read_ver,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Subject   string `json:"subject,omitempty"`
}
```

- Cuối `EventOf`, dòng `return markOf(subject, ev)` →

```go
	if out, ok := markOf(subject, ev); ok {
		return out, true
	}
	return memberOf(subject, ev)
```

`tools/corecli/internal/e2e/members.go`:

```go
package e2e

import (
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindMemberAdded       = "member_added"
	KindMemberRemoved     = "member_removed"
	KindMemberRoleChanged = "member_role_changed"
	KindMemberCount       = "member_count_changed"
	KindReadUpdated       = "read_updated"

	ReasonRemoved = "removed"
	ReasonLeft    = "left"
)

type Scope struct {
	Root   string
	Tenant string
	Room   string
}

func RoomSubject(root, tenant, room string) string { return root + "." + tenant + ".room." + room }

func UserSubject(root, tenant, user string) string { return root + "." + tenant + ".user." + user }

func MemberEventID(room, user string, ver uint32) string {
	return room + "-mb-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}

func MemberUserEventID(room, user string, ver uint32) string { return MemberEventID(room, user, ver) + "-u" }

func MemberCountEventID(room string, ver uint64) string {
	return room + "-members-v" + strconv.FormatUint(ver, 10)
}

func ReadEventID(room, user string, readVer uint64) string {
	return room + "-rd-" + user + "-v" + strconv.FormatUint(readVer, 10)
}

func RoleName(r chatimv1.MemberRole) string {
	if r == chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(r.String(), "MEMBER_ROLE_"))
}

func reasonName(r chatimv1.MemberRemovedReason) string {
	return strings.ToLower(strings.TrimPrefix(r.String(), "MEMBER_REMOVED_REASON_"))
}

func (s Scope) roomEvent(kind, id string) Event {
	return Event{Kind: kind, Room: s.Room, ID: id, Subject: RoomSubject(s.Root, s.Tenant, s.Room) + ".evt." + kind}
}

func (s Scope) copies(kind, user string, ver uint32, fill func(*Event)) []Event {
	room := s.roomEvent(kind, MemberEventID(s.Room, user, ver))
	room.User, room.Version = user, ver
	fill(&room)
	mine := room
	mine.ID, mine.Recipient = MemberUserEventID(s.Room, user, ver), user
	mine.Subject = UserSubject(s.Root, s.Tenant, user) + ".evt." + kind
	return []Event{room, mine}
}

func (s Scope) Added(user, role string, ver uint32) []Event {
	return s.copies(KindMemberAdded, user, ver, func(e *Event) { e.Role = role })
}

func (s Scope) Created(user, role string) Event { return s.Added(user, role, 1)[1] }

func (s Scope) Removed(user, reason, previous string, ver uint32) []Event {
	return s.copies(KindMemberRemoved, user, ver, func(e *Event) { e.Text, e.Role = reason, previous })
}

func (s Scope) RoleChanged(user, role, previous string, ver uint32) []Event {
	return s.copies(KindMemberRoleChanged, user, ver, func(e *Event) { e.Role, e.Text = role, previous })
}

func (s Scope) CountChanged(count int32, ver uint64) Event {
	ev := s.roomEvent(KindMemberCount, MemberCountEventID(s.Room, ver))
	ev.Count, ev.Seq = count, ver
	return ev
}

func (s Scope) Read(user string, seq, readVer uint64, roomWide bool) Event {
	ev := s.roomEvent(KindReadUpdated, ReadEventID(s.Room, user, readVer))
	ev.User, ev.Seq, ev.ReadVer = user, seq, readVer
	if !roomWide {
		ev.Recipient, ev.Subject = user, UserSubject(s.Root, s.Tenant, user)+".evt."+KindReadUpdated
	}
	return ev
}

func memberOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Recipient: ev.GetRecipient(), Subject: subject}
	switch {
	case ev.GetMemberAdded() != nil:
		a := ev.GetMemberAdded()
		out.Kind, out.User, out.Role, out.Version = KindMemberAdded, a.GetUser(), RoleName(a.GetRole()), a.GetVer()
	case ev.GetMemberRemoved() != nil:
		r := ev.GetMemberRemoved()
		out.Kind, out.User, out.Text, out.Role, out.Version = KindMemberRemoved, r.GetUser(), reasonName(r.GetReason()), RoleName(r.GetPreviousRole()), r.GetVer()
	case ev.GetMemberRoleChanged() != nil:
		c := ev.GetMemberRoleChanged()
		out.Kind, out.User, out.Role, out.Text, out.Version = KindMemberRoleChanged, c.GetUser(), RoleName(c.GetRole()), RoleName(c.GetPreviousRole()), c.GetVer()
	case ev.GetMemberCountChanged() != nil:
		c := ev.GetMemberCountChanged()
		out.Kind, out.Count, out.Seq = KindMemberCount, c.GetMemberCount(), c.GetMemberCountVer()
	case ev.GetReadUpdated() != nil:
		r := ev.GetReadUpdated()
		out.Kind, out.User, out.Seq, out.ReadVer = KindReadUpdated, r.GetUser(), r.GetReadSeq(), r.GetReadVer()
	default:
		return Event{}, false
	}
	return out, true
}
```

Quy ước field của `Event` cho kind mới: `member_added` → `User`, `Role`, `Version` (= `ver`); `member_removed` → `User`, `Text` (reason), `Role` (previous role), `Version`; `member_role_changed` → `User`, `Role` (mới), `Text` (previous role), `Version`; `member_count_changed` → `Count`, `Seq` (= `member_count_ver`); `read_updated` → `User`, `Seq` (= `read_seq`), `ReadVer`. `Recipient` và `Subject` luôn so.

`tools/corecli/internal/e2e/members_check.go`:

```go
package e2e

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type MemberReply struct {
	Changed  bool
	Ver      uint32
	Added    string
	NewOwner string
	Previous string
}

func FormatAdded(added []*chatimv1.AddedMember) string {
	parts := make([]string, len(added))
	for i, a := range added {
		parts[i] = a.GetUser() + ":" + strconv.FormatUint(uint64(a.GetVer()), 10)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func CheckMemberReply(what string, got, want MemberReply) error {
	if got != want {
		return fmt.Errorf("%s returned %+v, want %+v", what, got, want)
	}
	return nil
}

func CheckLive(want, got []Event) ([]string, error) {
	byID := make(map[string]Event, len(want))
	for _, w := range want {
		byID[w.ID] = w
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range got {
		w, ok := byID[ev.ID]
		if !ok {
			continue
		}
		if ev != w {
			return nil, fmt.Errorf("live event %s = %+v, want %+v", ev.ID, ev, w)
		}
		seen[ev.ID] = true
	}
	var missing []string
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func CheckCleared(page []*chatimv1.Message, cleared uint64, cid string) error {
	if len(page) != 2 || page[0].GetSeq() != cleared || page[1].GetSeq() != cleared+1 {
		return fmt.Errorf("latest page after the clear has %d messages, want seq %d and %d", len(page), cleared, cleared+1)
	}
	if before := page[0]; !before.GetHidden() || before.GetText() != "" {
		return fmt.Errorf("seq %d after the clear is hidden %v with text %q, want hidden without text", cleared, before.GetHidden(), before.GetText())
	}
	if after := page[1]; after.GetHidden() || after.GetText() != TextFor(cid) {
		return fmt.Errorf("seq %d sent after the clear is hidden %v with text %q, want shown with %q", cleared+1, after.GetHidden(), after.GetText(), TextFor(cid))
	}
	return nil
}
```

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."`
Expected: PASS cho `tools/corecli/internal/e2e` (gồm test cũ `TestEventOfKeepsMessageEventsOnly`: so sánh struct `Event` vẫn đúng vì field mới bằng 0 ở event cũ); `tools/corecli` (main) vẫn biên dịch. `wc -l tools/corecli/internal/e2e/*.go` → mỗi file < 200 (`members.go` ~121, `members_test.go` ~116, `members_check.go` ~73, `events.go` ~80).

**Step 10: Code corecli**

`tools/corecli/cmd_members.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var (
	memberRoles = map[string]chatimv1.MemberRole{
		"owner":  chatimv1.MemberRole_MEMBER_ROLE_OWNER,
		"admin":  chatimv1.MemberRole_MEMBER_ROLE_ADMIN,
		"member": chatimv1.MemberRole_MEMBER_ROLE_MEMBER,
	}
	errTargetRequired = errors.New("-target is required")
	errUsersRequired  = errors.New("-users is required")
)

type roomCall func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error)

func replyOf[T proto.Message](resp T, st route.Stats, err error) (proto.Message, route.Stats, error) {
	return resp, st, err
}

func roomFlags(name string) (*flag.FlagSet, *options, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return fs, addOptions(fs), fs.String("room", "", "room id")
}

func runRoomCall(ctx context.Context, o *options, name, room string, do roomCall) error {
	if room == "" {
		return errRoomRequired
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := do(ctx, s.client)
		if err != nil {
			return err
		}
		report(name, st)
		return printJSON(resp)
	})
}

func addMembersCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("add-members")
	users := fs.String("users", "", "comma-separated users to add")
	requestID := fs.String("request-id", "", "request id, random when empty; reuse it to retry exactly once")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list := splitList(*users)
	if len(list) == 0 {
		return errUsersRequired
	}
	if *requestID == "" {
		*requestID = randomCID()
	}
	req := &chatimv1.AddMembersRequest{RoomId: *room, Users: list, RequestId: *requestID}
	return runRoomCall(ctx, o, "add-members request "+*requestID, *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.AddMembers(ctx, req))
	})
}

func removeMemberCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("remove-member")
	target := fs.String("target", "", "user to remove; use leave to remove yourself")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return errTargetRequired
	}
	req := &chatimv1.RemoveMemberRequest{RoomId: *room, User: *target}
	return runRoomCall(ctx, o, "remove-member", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.RemoveMember(ctx, req))
	})
}

func leaveCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("leave")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := &chatimv1.LeaveRoomRequest{RoomId: *room}
	return runRoomCall(ctx, o, "leave", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.LeaveRoom(ctx, req))
	})
}

func setRoleCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("set-role")
	target := fs.String("target", "", "member whose role changes")
	role := fs.String("role", "", "owner, admin or member")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, ok := memberRoles[*role]
	switch {
	case *target == "":
		return errTargetRequired
	case !ok:
		return fmt.Errorf("-role %q: want owner, admin or member", *role)
	}
	req := &chatimv1.ChangeMemberRoleRequest{RoomId: *room, User: *target, Role: r}
	return runRoomCall(ctx, o, "set-role", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.ChangeMemberRole(ctx, req))
	})
}

func readCmd(ctx context.Context, args []string) error { return readPositionCmd(ctx, "read", args) }

func unreadCmd(ctx context.Context, args []string) error { return readPositionCmd(ctx, "unread", args) }

func readPositionCmd(ctx context.Context, name string, args []string) error {
	fs, o, room := roomFlags(name)
	seq := fs.Uint64("seq", 0, "read: last read seq, 0 for the latest message; unread: the first seq to show as unread")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "unread" && *seq == 0 {
		return errSeqRequired
	}
	return runRoomCall(ctx, o, name, *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		if name == "read" {
			return replyOf(cl.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: *room, Seq: *seq}))
		}
		return replyOf(cl.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: *room, Seq: *seq}))
	})
}
```

`randomCID()` (`cmd_rpc.go`) trả `cli-<16 hex>`, hợp lệ theo `ValidCID`, dùng làm `request_id` mặc định.

`tools/corecli/cmd_watch.go`, trong `watchCmd`:
- Cờ `tenant := fs.String("tenant", "e2e", "tenant of the room")` → mô tả `"tenant of the room or user"`; sau `room := …` thêm `user := fs.String("user", "", "watch this user's own subject (member and read events) instead of a room")`.
- Thay khối từ `if _, err := ids.ParseRoomID(*room); err != nil {` tới hết khối `if *tenant == "" || strings.ContainsAny(…) { return errors.New(…) }` bằng:

```go
	subject, err := watchSubject(*liveRoot, *tenant, *room, *user)
	if err != nil {
		return err
	}
```

- Xoá dòng `subject := *liveRoot + "." + *tenant + ".room." + *room + ".>"`; dòng `return errors.Join(watch(ctx, *url, subject, w), w.Close())` → `return errors.Join(watch(ctx, *url, subject+".>", w), w.Close())`.
- Thêm cuối file:

```go
func watchSubject(root, tenant, room, user string) (string, error) {
	switch {
	case root == "" || tenant == "":
		return "", errors.New("-tenant and -live-root are required")
	case strings.ContainsAny(root+tenant+user, ".*> "):
		return "", errors.New("-tenant, -user and -live-root must be single subject tokens")
	case (room == "") == (user == ""):
		return "", errors.New("watch exactly one of -room or -user")
	case user != "":
		return e2e.UserSubject(root, tenant, user), nil
	}
	if _, err := ids.ParseRoomID(room); err != nil {
		return "", fmt.Errorf("-room: %w", err)
	}
	return e2e.RoomSubject(root, tenant, room), nil
}
```

(Import của file giữ nguyên: `errors`, `fmt`, `strings`, `ids`, `e2e` vẫn dùng. Biến `err` trong `watchCmd` được khai báo ở đây trước `w, err := openOutput(*out)` nên dòng đó vẫn hợp lệ.)

`tools/corecli/main.go`:
- `usage`: thay dòng `  watch              print live events of a room from NATS` bằng các dòng sau, và dòng `  e2e …` thành `  e2e                end-to-end scenario steps: setup, send, change, react-pin, members, check`:

```
  add-members        add users to a group (-users a,b, -request-id); owners and admins
  remove-member      remove a user from a group (-target); admins remove plain members only
  leave              leave a group; the last owner hands over to the earliest admin, else member
  set-role           set a member's role (-target, -role owner|admin|member); owners only
  read               raise the caller's read position to -seq (0 = the latest message)
  unread             lower the caller's read position to just before -seq
  watch              print live events of a room (-room) or of a user (-user) from NATS
```

- Map `commands` thêm `"add-members": addMembersCmd, "remove-member": removeMemberCmd, "leave": leaveCmd, "set-role": setRoleCmd, "read": readCmd, "unread": unreadCmd,` (gofmt căn cột).

`tools/corecli/cmd_e2e.go`: `e2eUsage` → `"usage: corecli e2e setup|send|change|react-pin|members|check [flags]"`; map `steps` thêm `"members": e2eMembers`.

`tools/corecli/e2e_members.go`:

```go
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	crowdSize         = 20
	memberHistoryPage = int32(50)
)

var errFewAcks = errors.New("the members phase needs at least two acked messages")

type memberScenario struct {
	cl          *route.Client
	base        context.Context
	st          e2e.State
	scope       e2e.Scope
	live        *liveEvents
	peer, other string
	last        uint64
	wait        time.Duration
	want        []e2e.Event
}

func (m *memberScenario) as(user string) context.Context {
	return route.WithCaller(m.base, m.st.Tenant, user)
}

func (m *memberScenario) expect(evs ...e2e.Event) { m.want = append(m.want, evs...) }

func (m *memberScenario) checkpoint(ctx context.Context) error {
	return m.live.await(ctx, m.want, m.wait)
}

func (m *memberScenario) subjects() []string {
	out := []string{e2e.RoomSubject(m.scope.Root, m.st.Tenant, m.st.Room) + ".>"}
	for _, u := range []string{m.st.User, m.peer, m.other} {
		out = append(out, e2e.UserSubject(m.scope.Root, m.st.Tenant, u)+".>")
	}
	return out
}

func e2eMembers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e members", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("nats", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL (env NATS_URL)")
	liveRoot := fs.String("live-root", "live", "live subject root, EVT_LIVE_ROOT of the cores")
	peer := fs.String("peer", "e2e-bob", "user added, made admin, then owner when the owner leaves")
	other := fs.String("other", "e2e-carol", "user added then removed by the admin")
	wait := fs.Duration("wait", 45*time.Second, "wait this long at each checkpoint for the member and read events")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if len(st.Acks) < 2 {
		return errFewAcks
	}
	o.tenant, o.user = st.Tenant, st.User
	m := &memberScenario{
		base: ctx, st: st, scope: e2e.Scope{Root: *liveRoot, Tenant: st.Tenant, Room: st.Room},
		peer: *peer, other: *other, last: st.Acks[len(st.Acks)-1].Seq, wait: *wait,
	}
	if m.live, err = openLive(*url, m.subjects()); err != nil {
		return err
	}
	defer m.live.close()
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		m.cl = s.client
		return m.run(ctx)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "members ok: direct room fixed, %s and %s added, %s admin, %s removed and denied, a retried request id kept %s out, unread then read on the room subject, history cleared by time, %d more members moved read receipts to the user subject, the owner left to %s and came back; %d live events\n",
		m.peer, m.other, m.peer, m.other, m.other, crowdSize, m.peer, len(m.want))
	return e2e.Save(statePath(*dir), m.st)
}

func (m *memberScenario) run(ctx context.Context) error {
	steps := []struct {
		name string
		do   func(context.Context) error
	}{
		{"direct room", m.directRoom},
		{"add", m.addTwo},
		{"new member reads", m.newMemberReads},
		{"promote", m.promotePeer},
		{"admin removes", m.adminRemovesOther},
		{"removed member", m.removedIsDenied},
		{"retried add", m.retriedAddKeepsRemoved},
		{"read position", m.unreadThenRead},
		{"clear history", m.clearByTime},
		{"big group", m.bigGroupReadsOnUserSubject},
		{"last owner leaves", m.ownerLeaves},
		{"owner comes back", m.ownerComesBack},
	}
	for _, s := range steps {
		if err := s.do(ctx); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return m.checkpoint(ctx)
}
```

`m.base` là `ctx` gốc (chưa có metadata người gọi): `withSession` truyền vào closure một context đã gắn `e2e-user`, nên mỗi lệnh tự gắn người gọi bằng `m.as(user)` (`metadata.AppendToOutgoingContext` gắn hai lần sẽ thành hai giá trị header). `ctx` của closure chỉ dùng để chờ event.

`tools/corecli/e2e_member_join.go`:

```go
package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const firstAdd = "e2e-add-1"

func (m *memberScenario) directRoom(context.Context) error {
	owner := m.st.User
	resp, _, err := m.cl.CreateRoom(m.as(owner), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{owner, m.peer}})
	if err != nil {
		return fmt.Errorf("create a direct room: %w", err)
	}
	dm := e2e.Scope{Root: m.scope.Root, Tenant: m.scope.Tenant, Room: resp.GetRoom().GetId()}
	if err := m.live.subscribe(e2e.RoomSubject(dm.Root, dm.Tenant, dm.Room) + ".>"); err != nil {
		return err
	}
	m.expect(dm.Created(owner, "owner"), dm.Created(m.peer, "member"))
	if err := m.directMembersFixed(dm.Room); err != nil {
		return err
	}
	if _, _, err := m.cl.SendMessage(m.as(owner), &chatimv1.SendMessageRequest{RoomId: dm.Room, Cid: "e2e-dm-1", Text: "hello"}); err != nil {
		return fmt.Errorf("send in the direct room: %w", err)
	}
	if err := m.readIn(dm.Room, m.peer, 0, 1, 1); err != nil {
		return err
	}
	m.expect(dm.Read(m.peer, 1, 1, true))
	return nil
}

func (m *memberScenario) directMembersFixed(dm string) error {
	owner := m.as(m.st.User)
	_, _, addErr := m.cl.AddMembers(owner, &chatimv1.AddMembersRequest{RoomId: dm, Users: []string{m.other}, RequestId: "e2e-dm-add"})
	_, _, removeErr := m.cl.RemoveMember(owner, &chatimv1.RemoveMemberRequest{RoomId: dm, User: m.peer})
	_, _, leaveErr := m.cl.LeaveRoom(owner, &chatimv1.LeaveRoomRequest{RoomId: dm})
	_, _, roleErr := m.cl.ChangeMemberRole(owner, &chatimv1.ChangeMemberRoleRequest{RoomId: dm, User: m.peer, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	for what, got := range map[string]error{"add": addErr, "remove": removeErr, "leave": leaveErr, "set role": roleErr} {
		if err := wantCode(what+" on direct room "+dm, got, codes.FailedPrecondition); err != nil {
			return err
		}
	}
	return nil
}

func (m *memberScenario) addTwo(ctx context.Context) error {
	added := e2e.FormatAdded([]*chatimv1.AddedMember{{User: m.peer, Ver: 1}, {User: m.other, Ver: 1}})
	want := e2e.MemberReply{Changed: true, Added: added}
	req := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: []string{m.peer, m.other}, RequestId: firstAdd}
	call := func() (*chatimv1.AddMembersResponse, route.Stats, error) { return m.cl.AddMembers(m.as(m.st.User), req) }
	if err := twice("add-members", call, addReply, want, want); err != nil {
		return err
	}
	m.expect(m.scope.Added(m.peer, "member", 1)...)
	m.expect(m.scope.Added(m.other, "member", 1)...)
	m.expect(m.scope.CountChanged(3, 1))
	return m.checkpoint(ctx)
}

func (m *memberScenario) newMemberReads(context.Context) error {
	if err := checkHistory(m.as(m.peer), m.cl, m.st, memberHistoryPage); err != nil {
		return fmt.Errorf("history as %s: %w", m.peer, err)
	}
	return m.read(m.peer, 0, m.last, 1)
}

func (m *memberScenario) promotePeer(context.Context) error {
	req := &chatimv1.ChangeMemberRoleRequest{RoomId: m.st.Room, User: m.peer, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}
	call := func() (*chatimv1.ChangeMemberRoleResponse, route.Stats, error) { return m.cl.ChangeMemberRole(m.as(m.st.User), req) }
	if err := twice("set-role admin", call, roleReply, e2e.MemberReply{Changed: true, Ver: 2, Previous: "member"}, e2e.MemberReply{Ver: 2}); err != nil {
		return err
	}
	m.expect(m.scope.RoleChanged(m.peer, "admin", "member", 2)...)
	return nil
}

func (m *memberScenario) adminRemovesOther(ctx context.Context) error {
	_, _, denied := m.cl.RemoveMember(m.as(m.peer), &chatimv1.RemoveMemberRequest{RoomId: m.st.Room, User: m.st.User})
	if err := wantCode("admin removes the owner", denied, codes.PermissionDenied); err != nil {
		return err
	}
	req := &chatimv1.RemoveMemberRequest{RoomId: m.st.Room, User: m.other}
	call := func() (*chatimv1.RemoveMemberResponse, route.Stats, error) { return m.cl.RemoveMember(m.as(m.peer), req) }
	if err := twice("remove-member", call, removeReply, e2e.MemberReply{Changed: true, Ver: 2}, e2e.MemberReply{Ver: 2}); err != nil {
		return err
	}
	m.expect(m.scope.Removed(m.other, e2e.ReasonRemoved, "member", 2)...)
	m.expect(m.scope.CountChanged(2, 2))
	return m.checkpoint(ctx)
}
```

`tools/corecli/e2e_member_read.go`:

```go
package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const afterClearCID = "e2e-after-clear"

func (m *memberScenario) removedIsDenied(context.Context) error {
	ctx := m.as(m.other)
	_, _, sendErr := m.cl.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: m.st.Room, Cid: "e2e-removed", Text: "sent after the removal"})
	_, _, historyErr := m.cl.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: m.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	_, _, readErr := m.cl.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: m.st.Room})
	for what, got := range map[string]error{"send": sendErr, "history": historyErr, "read": readErr} {
		if err := wantCode(what+" as removed "+m.other, got, codes.PermissionDenied); err != nil {
			return err
		}
	}
	return nil
}

func (m *memberScenario) retriedAddKeepsRemoved(context.Context) error {
	req := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: []string{m.peer, m.other}, RequestId: firstAdd}
	resp, _, err := m.cl.AddMembers(m.as(m.st.User), req)
	if err == nil {
		err = e2e.CheckMemberReply("add-members retried with "+firstAdd, addReply(resp), e2e.MemberReply{})
	}
	if err != nil {
		return err
	}
	_, _, gone := m.cl.GetHistory(m.as(m.other), &chatimv1.GetHistoryRequest{RoomId: m.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	return wantCode("history of "+m.other+" after the retried add", gone, codes.PermissionDenied)
}

func (m *memberScenario) unreadThenRead(context.Context) error {
	if err := m.unread(m.peer, m.last, m.last-1, 2); err != nil {
		return err
	}
	if err := m.read(m.peer, 0, m.last, 3); err != nil {
		return err
	}
	m.expect(m.scope.Read(m.peer, m.last-1, 2, true), m.scope.Read(m.peer, m.last, 3, true))
	return nil
}

func (m *memberScenario) clearByTime(context.Context) error {
	resp, _, err := m.cl.ClearHistory(m.as(m.peer), &chatimv1.ClearHistoryRequest{RoomId: m.st.Room})
	if err != nil {
		return fmt.Errorf("clear history as %s: %w", m.peer, err)
	}
	if resp.GetClearedBeforeTime() == nil {
		return fmt.Errorf("clear history as %s returned no cleared_before_time", m.peer)
	}
	sent, _, err := m.cl.SendMessage(m.as(m.st.User), &chatimv1.SendMessageRequest{RoomId: m.st.Room, Cid: afterClearCID, Text: e2e.TextFor(afterClearCID)})
	if err != nil {
		return fmt.Errorf("send after the clear: %w", err)
	}
	if sent.GetSeq() != m.last+1 {
		return fmt.Errorf("send after the clear got seq %d, want %d", sent.GetSeq(), m.last+1)
	}
	m.st.Acks = append(m.st.Acks, e2e.Ack{CID: afterClearCID, Seq: sent.GetSeq()})
	msgs, err := page(m.as(m.peer), m.cl, m.st.Room, chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, 0, 2)
	if err != nil {
		return fmt.Errorf("history as %s after the clear: %w", m.peer, err)
	}
	if err := e2e.CheckCleared(msgs, m.last, afterClearCID); err != nil {
		return err
	}
	m.last = sent.GetSeq()
	return nil
}

func (m *memberScenario) bigGroupReadsOnUserSubject(ctx context.Context) error {
	crowd := make([]string, crowdSize)
	added := make([]*chatimv1.AddedMember, crowdSize)
	for i := range crowd {
		crowd[i] = fmt.Sprintf("e2e-m%02d", i+1)
		added[i] = &chatimv1.AddedMember{User: crowd[i], Ver: 1}
	}
	req := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: crowd, RequestId: "e2e-add-2"}
	resp, stats, err := m.cl.AddMembers(m.as(m.st.User), req)
	if err == nil {
		err = e2e.CheckMemberReply("add-members crowd", addReply(resp), e2e.MemberReply{Changed: true, Added: e2e.FormatAdded(added)})
	}
	if err != nil {
		return err
	}
	report("add-members crowd", stats)
	m.expect(m.scope.CountChanged(2+crowdSize, 3))
	if err := m.checkpoint(ctx); err != nil {
		return err
	}
	if err := m.read(m.peer, 0, m.last, 4); err != nil {
		return err
	}
	m.expect(m.scope.Read(m.peer, m.last, 4, false))
	return nil
}
```

`tools/corecli/e2e_member_owner.go`:

```go
package main

import (
	"context"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func (m *memberScenario) ownerLeaves(context.Context) error {
	owner := m.st.User
	req := &chatimv1.LeaveRoomRequest{RoomId: m.st.Room}
	call := func() (*chatimv1.LeaveRoomResponse, route.Stats, error) { return m.cl.LeaveRoom(m.as(owner), req) }
	if err := twice("leave", call, leaveReply, e2e.MemberReply{Changed: true, Ver: 2, NewOwner: m.peer}, e2e.MemberReply{Ver: 2}); err != nil {
		return err
	}
	_, _, gone := m.cl.GetHistory(m.as(owner), &chatimv1.GetHistoryRequest{RoomId: m.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	if err := wantCode("history after leaving", gone, codes.PermissionDenied); err != nil {
		return err
	}
	m.expect(m.scope.RoleChanged(m.peer, "owner", "admin", 3)...)
	m.expect(m.scope.Removed(owner, e2e.ReasonLeft, "owner", 2)...)
	return nil
}

func (m *memberScenario) ownerComesBack(context.Context) error {
	owner := m.st.User
	want := e2e.MemberReply{Changed: true, Added: e2e.FormatAdded([]*chatimv1.AddedMember{{User: owner, Ver: 3}})}
	add := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: []string{owner}, RequestId: "e2e-add-3"}
	addCall := func() (*chatimv1.AddMembersResponse, route.Stats, error) { return m.cl.AddMembers(m.as(m.peer), add) }
	if err := twice("re-add", addCall, addReply, want, want); err != nil {
		return err
	}
	role := &chatimv1.ChangeMemberRoleRequest{RoomId: m.st.Room, User: owner, Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER}
	roleCall := func() (*chatimv1.ChangeMemberRoleResponse, route.Stats, error) { return m.cl.ChangeMemberRole(m.as(m.peer), role) }
	if err := twice("set-role owner", roleCall, roleReply, e2e.MemberReply{Changed: true, Ver: 4, Previous: "member"}, e2e.MemberReply{Ver: 4}); err != nil {
		return err
	}
	if err := m.read(owner, 0, m.last, 1); err != nil {
		return err
	}
	m.expect(m.scope.Added(owner, "member", 3)...)
	m.expect(m.scope.RoleChanged(owner, "owner", "member", 4)...)
	return nil
}
```

`tools/corecli/e2e_member_calls.go`:

```go
package main

import (
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type readPosition interface {
	GetReadSeq() uint64
	GetReadVer() uint64
}

func twice[T any](what string, call func() (T, route.Stats, error), reply func(T) e2e.MemberReply, first, again e2e.MemberReply) error {
	for i, want := range []e2e.MemberReply{first, again} {
		resp, st, err := call()
		if err == nil {
			err = e2e.CheckMemberReply(what, reply(resp), want)
		}
		if err != nil {
			return fmt.Errorf("%s %d: %w", what, i+1, err)
		}
		report(what+" "+strconv.Itoa(i+1), st)
	}
	return nil
}

func wantCode(what string, err error, code codes.Code) error {
	if status.Code(err) != code {
		return fmt.Errorf("%s = %w, want %s", what, err, code)
	}
	return nil
}

func (m *memberScenario) read(user string, seq, wantSeq, wantVer uint64) error {
	return m.readIn(m.st.Room, user, seq, wantSeq, wantVer)
}

func (m *memberScenario) readIn(room, user string, seq, wantSeq, wantVer uint64) error {
	resp, _, err := m.cl.MarkRead(m.as(user), &chatimv1.MarkReadRequest{RoomId: room, Seq: seq})
	return checkPosition("read as "+user+" in "+room, resp, err, wantSeq, wantVer)
}

func (m *memberScenario) unread(user string, seq, wantSeq, wantVer uint64) error {
	resp, _, err := m.cl.MarkUnread(m.as(user), &chatimv1.MarkUnreadRequest{RoomId: m.st.Room, Seq: seq})
	return checkPosition("unread as "+user, resp, err, wantSeq, wantVer)
}

func checkPosition(what string, got readPosition, err error, seq, ver uint64) error {
	switch {
	case err != nil:
		return fmt.Errorf("%s: %w", what, err)
	case got.GetReadSeq() != seq || got.GetReadVer() != ver:
		return fmt.Errorf("%s returned seq %d read_ver %d, want seq %d read_ver %d", what, got.GetReadSeq(), got.GetReadVer(), seq, ver)
	default:
		return nil
	}
}

func addReply(r *chatimv1.AddMembersResponse) e2e.MemberReply {
	return e2e.MemberReply{Changed: len(r.GetAdded()) > 0, Added: e2e.FormatAdded(r.GetAdded())}
}

func removeReply(r *chatimv1.RemoveMemberResponse) e2e.MemberReply {
	return e2e.MemberReply{Changed: r.GetChanged(), Ver: r.GetVer()}
}

func leaveReply(r *chatimv1.LeaveRoomResponse) e2e.MemberReply {
	out := e2e.MemberReply{Changed: r.GetChanged(), Ver: r.GetVer()}
	if out.Changed {
		out.NewOwner = r.GetNewOwner()
	}
	return out
}

func roleReply(r *chatimv1.ChangeMemberRoleResponse) e2e.MemberReply {
	out := e2e.MemberReply{Changed: r.GetChanged(), Ver: r.GetVer()}
	if out.Changed {
		out.Previous = e2e.RoleName(r.GetPreviousRole())
	}
	return out
}
```

`tools/corecli/e2e_member_live.go`:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

type liveEvents struct {
	nc  *nats.Conn
	ch  chan *nats.Msg
	got []e2e.Event
}

func openLive(url string, subjects []string) (*liveEvents, error) {
	nc, err := nats.Connect(url, nats.Name("chatim-corecli-e2e-members"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	l := &liveEvents{nc: nc, ch: make(chan *nats.Msg, 4096)}
	for _, s := range subjects {
		if err := l.subscribe(s); err != nil {
			nc.Close()
			return nil, err
		}
	}
	return l, nil
}

func (l *liveEvents) subscribe(subject string) error {
	if _, err := l.nc.ChanSubscribe(subject, l.ch); err != nil {
		return fmt.Errorf("subscribe %s: %w", subject, err)
	}
	if err := l.nc.FlushTimeout(flushTimeout); err != nil {
		return fmt.Errorf("confirm subscription %s: %w", subject, err)
	}
	return nil
}

func (l *liveEvents) close() { l.nc.Close() }

func (l *liveEvents) await(ctx context.Context, want []e2e.Event, wait time.Duration) error {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		missing, err := e2e.CheckLive(want, l.got)
		switch {
		case err != nil:
			return fmt.Errorf("live events: %w", err)
		case len(missing) == 0:
			return nil
		}
		select {
		case msg := <-l.ch:
			if err := l.add(msg); err != nil {
				return err
			}
		case <-deadline.C:
			return fmt.Errorf("after %v these member and read events never arrived: %v", wait, missing)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *liveEvents) add(msg *nats.Msg) error {
	var ev chatimv1.Event
	if err := proto.Unmarshal(msg.Data, &ev); err != nil {
		return fmt.Errorf("decode event on %s: %w", msg.Subject, err)
	}
	if got, ok := e2e.EventOf(msg.Subject, &ev); ok {
		l.got = append(l.got, got)
	}
	return nil
}
```

`await` kiểm `CheckLive` trên toàn bộ event đã nhận mỗi lần thêm một event (≤ vài trăm event, rẻ). Bộ đệm 4096 đủ cho mọi event của phase 5 giữa hai lần chờ (20 người vào sinh 20 bản room trên subject room; bản user của họ không được subscribe).

`scripts/e2e.sh`, hai chỗ:
- Dòng `echo "e2e PASS: …"` → (một dòng):

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted with the first then the second emoji from GetReactionSettings and an unlisted emoji refused, seq 4 pinned, on replies, history and live; direct room members fixed with read_updated on the room, members added with a request id (retry keeps a removed member out) and the full history read, an admin promoted who removes a member then denied send, history and read, member_count converging, unread then read, history cleared by time, read_updated on the user subject of a big group, the last owner leaving to the admin and re-added, on replies and live room and user subjects"
```

- Sau dòng `cli e2e check -state /state` của phase 4 (ngay trước `result=PASS`), thêm:

```bash

step="phase 5 members and read position"
echo "phase 5: direct room fixed, add e2e-bob and e2e-carol, promote e2e-bob, e2e-bob removes e2e-carol, a retried request id keeps her out, unread then read, clear history, 20 more members move read receipts to the user subject, the owner leaves to e2e-bob and comes back"
cli e2e members -state /state
step="phase 5 check"
cli e2e check -state /state
```

**Step 11: Chạy**

```bash
make -s go ARGS="test -race -shuffle=on ./tools/..."
make -s go ARGS="vet ./tools/..."
wc -l tools/corecli/*.go tools/corecli/internal/e2e/*.go | sort -n | tail -10
```

Expected: PASS; vet sạch; mỗi file < 200 (`cmd_members.go` ~135, `e2e_members.go` ~117, `e2e_member_join.go` ~98, `e2e_member_read.go` ~104, `e2e_member_calls.go` ~88, `e2e_member_live.go` ~81, `e2e_member_owner.go` ~48, `cmd_watch.go` ~115, `main.go` ~88).

Run (cần Task 15 đã có 6 RPC và vòng đời `readcast`, Task 16 có `member_counter`):

```bash
make core-down
make infra-reset
make infra-up && make core-up && make e2e
```

(`make infra-reset` để stream `CHATIM_EVT` có luật RePublish `evt.*.*.*.*` và DB theo tên field mới; `core-up` build lại image core.)

Expected: các dòng `phase 1`…`phase 4` như M2b.3; `phase 5: direct room fixed, …`; các dòng `add-members 1 via …`, `add-members 2 …`, `set-role admin 1/2 …`, `remove-member 1/2 …`, `add-members crowd …`, `leave 1/2 …`, `re-add 1/2 …`, `set-role owner 1/2 …`; `members ok: direct room fixed, e2e-bob and e2e-carol added, …; 25 live events`; ở phase 5 check: `history ok: 81 messages …` và `live ok: an event for each of 81 seq, …`; dòng cuối là PASS mới ở trên. Bước nào khác → dừng, báo (ghi lỗi in ra; ví dụ `after 45s these member and read events never arrived: [<room>-members-v1]` nghĩa là `member_counter` không chạy (registry Task 16) hoặc reader không đẩy record `members` (feed Task 6); `read as e2e-bob … returned seq 80 read_ver 0, want … 1` nghĩa là `AddMembers` không nâng `read_ver` khi vào (Task 2/5); `live event <room>-rd-e2e-bob-v4 = {…subject live.e2e.room…}` nghĩa là `readcast` không đọc `member_count` đã hội tụ (Task 14)).

Thử nhanh lệnh tay (room lấy từ log e2e; `REDIS_PASSWORD` export từ `.env`, không in ra):

```bash
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room <ROOM> -user e2e-user -users e2e-dave -request-id manual-1
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev unread -room <ROOM> -user e2e-dave -seq 81
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev set-role -room <ROOM> -user e2e-dave -target e2e-user -role member
```

Expected: lệnh đầu in protojson `{"added":[{"user":"e2e-dave","ver":1}]}`; lệnh hai `{"readSeq":"80","readVer":"2"}`; lệnh ba (member thường đổi role) → `permission denied`, exit 1. (Số có thể lệch nếu đã chạy lệnh tay khác; ghi số thật vào báo cáo.)

**Step 12: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py tools/corecli purpose + "; member commands add-members (-users, -request-id, random by default), remove-member (-target), leave, set-role (-target, -role), read, unread (-seq); watch -room or -user (live.{t}.user.{u}); e2e members (phase 5) connects NATS itself (-nats, -live-root), subscribes the room, three user subjects and the DM it creates, and checks direct room members fixed with read_updated on the DM room, add with a request id (a same-id retry returns the earlier result and never re-adds a removed member), the new member's full history and read position, admin promotion and removal, the removed user's send/history/read PERMISSION_DENIED, member_count_changed v1..v3, unread then read on the room subject, clear history by time, read_updated on the user subject once the group has 22 members, the last owner leaving to the admin and re-added, and every member_added/member_removed/member_role_changed copy by id, subject, recipient and payload"
python3 bin/indexes_edit.py tools/corecli key_symbols + ";addMembersCmd;removeMemberCmd;leaveCmd;setRoleCmd;readCmd;unreadCmd;watchSubject;e2eMembers;memberScenario;liveEvents"
python3 bin/indexes_edit.py tools/corecli decisions + ";D97;D99;D100;D101;D102;D104;D105"
python3 bin/indexes_edit.py tools/corecli/internal/e2e purpose + "; member and read events decoded by EventOf (member_added user, role, ver; member_removed reason and previous role; member_role_changed role and previous role; member_count_changed count and ver; read_updated read_seq and read_ver; recipient); Scope builds the wanted room and user copies with ids {room}-mb-{user}-v{ver}, …-u, {room}-members-v{ver}, {room}-rd-{user}-v{read_ver} and their live subjects; CheckLive matches wanted events by id (other ids and duplicates ignored, a wrong subject or payload fails); CheckMemberReply, FormatAdded, CheckCleared"
python3 bin/indexes_edit.py tools/corecli/internal/e2e key_symbols + ";Scope;Scope.Added;Scope.Created;Scope.Removed;Scope.RoleChanged;Scope.CountChanged;Scope.Read;RoomSubject;UserSubject;MemberEventID;MemberUserEventID;MemberCountEventID;ReadEventID;RoleName;FormatAdded;CheckLive;MemberReply;CheckMemberReply;CheckCleared;KindMemberAdded;KindMemberRemoved;KindMemberRoleChanged;KindMemberCount;KindReadUpdated;ReasonRemoved;ReasonLeft"
python3 bin/indexes_edit.py tools/corecli/internal/e2e decisions + ";D102;D104;D105"
python3 bin/indexes_edit.py scripts/e2e.sh purpose + "; phase 5 runs e2e members (direct room fixed, members added with a request id, admin promotion and removal, removed user denied, same-id retry keeps her out, member_count converging, unread then read, clear history by time, read receipts on the user subject of a big group, the last owner leaving to the admin and re-added) then e2e check as the re-added owner"
python3 bin/indexes_edit.py scripts/e2e.sh decisions + ";D99;D100;D104;D105"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add tools/corecli/cmd_members.go tools/corecli/e2e_members.go tools/corecli/e2e_member_join.go tools/corecli/e2e_member_read.go tools/corecli/e2e_member_owner.go tools/corecli/e2e_member_calls.go tools/corecli/e2e_member_live.go tools/corecli/internal/e2e/members.go tools/corecli/internal/e2e/members_check.go tools/corecli/internal/e2e/members_test.go
git commit -m "feat(corecli): add member and read position commands, watch a user and an e2e member phase" -- tools/corecli/ scripts/e2e.sh INDEXES.csv
```

Expected: `ok` từng lệnh helper, `{7}`, sạch; commit 15 file + `INDEXES.csv`.

Task thấp: controller kiểm nhanh (kịch bản khớp bảng đầu task; PASS line; `-target` thay `-user` ghi vào báo cáo), không reviewer.

---

### Task 19: Itest xuyên phần member, owner và vị trí đọc

Khẳng định trên hạ tầng thật (skip khi thiếu `CHATIM_IT_*`), dùng helper sẵn có (`realInfra`, `startCore`, `dialCore`, `createRoom` (room `alice` owner + `bob`), `createRoomWith`, `caller`, `callerAs`, `sendAs`, `historyAs`, `parseRoom`, `subscribeLive`, `itStore`, `itFastEffects`, `awaitLiveEvents`, `awaitLiveIDs`, `awaitStored`, `assertNoLiveIDs`, `retryingUnavailable`, `itLiveLimit`, `itTenant` = `acme`, `itUser` = `alice`) và helper mới ở `it_members_test.go`:

- (a) **Core chết giữa pending và finish → `owner_guard` vá**: ghi thẳng vào Mongo `BeginOwnerChange(leave alice → bob)` rồi bước thăng `bob` (đúng như `finish` làm), không có bước rời của `alice` (giả lập core chết); record của `bob` tới worker → `owner_guard` rời `alice` theo pending, xoá pending; `owner_repaired_total` = 1 trên `/metrics`; `member_event` phát `member_removed` (left) của `alice`; `alice` mất quyền đọc.
- (a') **Group mất owner → `owner_guard` chọn kế nhiệm**: tombstone `alice` (owner) ghi thẳng qua `ApplyMember`; `carol` là admin nên thành owner, `member_role_changed` tới subject room.
- (b) **Hai owner rời cùng lúc**: `alice` và `bob` (owner) cùng rời; cả hai thành công; đúng một reply có `new_owner` = `carol` (admin vào sớm nhất); room còn đúng một owner `carol`; `member_count` hội tụ về 2.
- (b') **Hai owner xoá nhau cùng lúc**: đúng một thành công, người kia `PERMISSION_DENIED`; còn đúng một owner. Hai admin xoá nhau → cả hai `PERMISSION_DENIED` (D101).
- (c) **Event trên subject user**: bản user của `member_added`/`member_removed` tới `live.{t}.user.{u}.evt.{kind}` với `recipient = u`; bản room tới subject room; id bản user không xuất hiện trên subject room; luật RePublish của stream là `evt.*.*.*.*`.
- (d) **`member_count` hội tụ**: thêm 3, xoá 1 → `rooms.member_count` = 4 sau ~1s; event `member_count_changed` của ver đã lưu mang 4.
- (e) **Read receipt**: với `READ_RECEIPT_MAX_MEMBERS=2`, group 3 người → `read_updated` chỉ trên subject user (`recipient`); DM → subject room; `MarkUnread` rồi `MarkRead` trong cửa sổ → bản cuối tới.
- (f) **Người bị xoá bị từ chối ngay**: `bob` gửi hai tin (cache actor có `bob`), `alice` xoá `bob` cùng core → lệnh gửi kế tiếp của `bob` `PERMISSION_DENIED` ngay (không đợi TTL 10s), `GetHistory`, `MarkRead` cũng vậy; `alice` vẫn gửi được.
- (g) **Retry cùng `request_id` sau khi bị xoá không thêm lại**: thêm `erin` với `request_id` R, lặp R → cùng kết quả; xoá `erin`; lặp R → `added` rỗng, `erin` vẫn tombstone và bị từ chối đọc; `request_id` mới → `erin` vào lại ở ver 3.
- (h) **Clear history theo thời gian**: `bob` clear → mốc T = `cleared_before_time` đã lưu, `ver` không đổi; tin trước T ẩn, tin sau T hiện; bị xoá rồi thêm lại vẫn giữ T, `read_seq` nâng lên tin mới nhất.
- (i) **Đọc/clear không vào work stream**: subscribe subject work stream; thêm `dave` → record `g:{room}-mb-dave-v1` tới; `MarkRead`, `MarkUnread`, `ClearHistory` của `dave` rồi một tin mốc → record `m:` của tin mốc tới mà không có record `g:` nào khác; doc `dave` giữ `ver 1`, `updated_at` cũ.

Không có bước "thấy fail": các test xác nhận hành vi của Task 2–17. Test fail ở lần chạy đầu là lỗi của task trước: dừng và báo (không sửa test cho qua). Gợi ý: (a) pending còn → `Guard`/`finish` (Task 11) hoặc registry (Task 16); (b) hai owner còn hoặc không còn owner → CAS `owners_ver` (Task 5/11); (c) bản user không tới → `recipient`/RePublish (Task 3); (d) count kẹt → `member_counter` (Task 16) hoặc feed `updatedFields.ver` (Task 6); (e) subject sai → `readcast` (Task 14); (f) gửi lần ba thành công → `ForgetMembers` (Task 9/12); (g) `erin` vào lại → `dedupe.Requests`/`AddedBy` (Task 8/12); (h) mốc khác → `ClearHistory` (Task 1/5); (i) có record `g:` thừa → lệnh đọc đụng `ver` hoặc feed lọc sai (Task 5/6).

Chờ trạng thái lưu (`member_count`, pending, doc) dùng `awaitStored` (poll `itActivityPoll` có hạn `itLiveLimit`) như M2b.3: hội tụ của worker không có tín hiệu nào khác để chờ. Live event chờ theo id.

Trước khi tạo file: `grep -n "func subscribeSubject\|func subscribeUserLive\|func awaitLiveMsg\|func storedMember\|func ownersOf\|func addMembersAs\|func removeMemberAs\|func leaveAs\|func setRoleAs\|func markReadAs\|func markUnreadAs\|func clearHistoryAs\|func ownerRepaired" apps/core/*_test.go` → không in gì (trùng tên với helper Part A/B thì đổi tên cục bộ, ghi vào báo cáo). Kiểm `git diff --quiet -- INDEXES.csv` như Task 16.

**Files:**
- Create: `apps/core/it_members_test.go` (helper)
- Create: `apps/core/member_owner_integration_test.go` (a, a', b, b')
- Create: `apps/core/member_events_integration_test.go` (c, d, e)
- Create: `apps/core/member_access_integration_test.go` (f, g, h)
- Create: `apps/core/member_feed_integration_test.go` (i)
- Modify: `INDEXES.csv`

**Step 1: Helper**

`apps/core/it_members_test.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func subscribeSubject(t *testing.T, it *itInfra, subject string) <-chan *nats.Msg {
	t.Helper()
	live := make(chan *nats.Msg, 512)
	sub, err := it.nc.ChanSubscribe(subject, live)
	if err != nil {
		t.Fatalf("subscribe %s: %v", subject, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush the subscription %s: %v", subject, err)
	}
	return live
}

func subscribeUserLive(t *testing.T, it *itInfra, cfg config.Config, user string) <-chan *nats.Msg {
	t.Helper()
	return subscribeSubject(t, it, cfg.Stream.LiveRoot+"."+itTenant+".user."+user+".>")
}

func awaitLiveMsg(t *testing.T, live <-chan *nats.Msg, id string) (*nats.Msg, *chatimv1.Event) {
	t.Helper()
	deadline := time.After(itLiveLimit)
	for {
		select {
		case m := <-live:
			if m.Header.Get(jetstream.MsgIDHeader) != id {
				continue
			}
			ev := &chatimv1.Event{}
			if err := proto.Unmarshal(m.Data, ev); err != nil {
				t.Fatalf("decode live event %s: %v", id, err)
			}
			return m, ev
		case <-deadline:
			t.Fatalf("live event %s did not arrive within %v", id, itLiveLimit)
		}
	}
}

func storedMember(t *testing.T, st *mongostore.Store, room uint64, user string) domain.Member {
	t.Helper()
	found, err := st.MembersOf(t.Context(), room, []string{user})
	if err != nil || len(found) != 1 {
		t.Fatalf("MembersOf(%d, %s) = %+v, %v; want one doc", room, user, found, err)
	}
	return found[0]
}

func ownersOf(t *testing.T, st *mongostore.Store, room uint64) []string {
	t.Helper()
	owners, err := st.Owners(t.Context(), room, 10)
	if err != nil {
		t.Fatalf("Owners(%d): %v", room, err)
	}
	out := make([]string, len(owners))
	for i, m := range owners {
		out[i] = m.User
	}
	return out
}

func addMembersAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID, requestID string, users ...string) *chatimv1.AddMembersResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.AddMembersResponse, error) {
		return client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: roomID, Users: users, RequestId: requestID})
	})
	if err != nil {
		t.Fatalf("AddMembers(%v, %s) as %s: %v", users, requestID, as, err)
	}
	return resp
}

func removeMemberAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID, user string) (*chatimv1.RemoveMemberResponse, error) {
	return retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.RemoveMemberResponse, error) {
		return client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: roomID, User: user})
	})
}

func leaveAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string) (*chatimv1.LeaveRoomResponse, error) {
	return retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.LeaveRoomResponse, error) {
		return client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: roomID})
	})
}

func setRoleAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID, user string, role chatimv1.MemberRole) {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.ChangeMemberRoleResponse, error) {
		return client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: roomID, User: user, Role: role})
	})
	if err != nil || !resp.GetChanged() {
		t.Fatalf("ChangeMemberRole(%s, %s) as %s = %v, %v; want a change", user, role, as, resp, err)
	}
}

func markReadAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string, seq uint64) *chatimv1.MarkReadResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.MarkReadResponse, error) {
		return client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: roomID, Seq: seq})
	})
	if err != nil {
		t.Fatalf("MarkRead(%d) as %s: %v", seq, as, err)
	}
	return resp
}

func markUnreadAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string, seq uint64) *chatimv1.MarkUnreadResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.MarkUnreadResponse, error) {
		return client.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: roomID, Seq: seq})
	})
	if err != nil {
		t.Fatalf("MarkUnread(%d) as %s: %v", seq, as, err)
	}
	return resp
}

func clearHistoryAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string) time.Time {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.ClearHistoryResponse, error) {
		return client.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: roomID})
	})
	if err != nil || resp.GetClearedBeforeTime() == nil {
		t.Fatalf("ClearHistory as %s = %v, %v; want a cleared before time", as, resp, err)
	}
	return resp.GetClearedBeforeTime().AsTime()
}

func ownerRepaired(ctx context.Context, cfg config.Config) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.AdminAddr+"/metrics", http.NoBody)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, "chatim_core_owner_repaired_total "); ok {
			return strconv.ParseFloat(v, 64)
		}
	}
	return 0, fmt.Errorf("chatim_core_owner_repaired_total not exported at %s", cfg.AdminAddr)
}
```

**Step 2: Test**

`apps/core/member_owner_integration_test.go`:

```go
package main

import (
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraOwnerGuardFinishesAnOwnerChangeACoreLeftHalfDone(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol"})
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	core.awaitTerm(t)
	st := itStore(it, core)
	at := time.Now().UTC().Truncate(time.Millisecond)
	change := domain.OwnerChange{
		Action: domain.OwnerLeave, User: itUser, UserVer: 1, Successor: "bob", SuccessorVer: 1,
		RequestID: "half-done", UpdatedBy: itUser, UpdatedAt: at,
	}
	if ok, err := st.BeginOwnerChange(t.Context(), room, 0, change); err != nil || !ok {
		t.Fatalf("BeginOwnerChange = %v, %v", ok, err)
	}
	bob := storedMember(t, st, room, "bob")
	if ok, err := st.ApplyMember(t.Context(), bob, bob.Next(domain.RoleOwner, domain.MemberActive, "half-done", itUser, at)); err != nil || !ok {
		t.Fatalf("promote bob = %v, %v", ok, err)
	}

	left := pbconv.MemberEventID(room, itUser, 2)
	if ev := awaitLiveEvents(t, live, left)[left]; ev.GetMemberRemoved().GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT {
		t.Fatalf("event %s = %v, want alice left, finished by the guard", left, ev)
	}
	state := awaitStored(t, "owner state", func() (domain.OwnerState, error) { return st.OwnerState(t.Context(), room) },
		func(s domain.OwnerState) bool { return s.Pending == nil })
	if state.Ver != 1 {
		t.Fatalf("owner state = %+v, want version 1 with nothing pending", state)
	}
	if alice := storedMember(t, st, room, itUser); alice.State != domain.MemberRemoved || alice.Ver != 2 || alice.RequestID != "half-done" {
		t.Fatalf("alice = %+v, want left at version 2 by the pending change", alice)
	}
	if got := ownersOf(t, st, room); !slices.Equal(got, []string{"bob"}) {
		t.Fatalf("owners = %v, want only bob", got)
	}
	awaitStored(t, "owner_repaired_total", func() (float64, error) { return ownerRepaired(t.Context(), core.cfg) }, func(v float64) bool { return v == 1 })
	if _, err := client.GetHistory(callerAs(t.Context(), itUser), &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("alice reads after leaving = %v, want PermissionDenied", err)
	}
}

func TestRealInfraOwnerGuardPromotesTheEarliestAdminOfAGroupWithoutOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol"})
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	core.awaitTerm(t)
	setRoleAs(t, client, itUser, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	st := itStore(it, core)
	alice := storedMember(t, st, room, itUser)
	lost := alice.Next(alice.Role, domain.MemberRemoved, "lost-owner", itUser, time.Now().UTC().Truncate(time.Millisecond))
	if ok, err := st.ApplyMember(t.Context(), alice, lost); err != nil || !ok {
		t.Fatalf("drop the only owner = %v, %v", ok, err)
	}
	promoted := pbconv.MemberEventID(room, "carol", 3)
	ev := awaitLiveEvents(t, live, promoted)[promoted]
	if c := ev.GetMemberRoleChanged(); c.GetRole() != chatimv1.MemberRole_MEMBER_ROLE_OWNER || c.GetPreviousRole() != chatimv1.MemberRole_MEMBER_ROLE_ADMIN || ev.GetActor() != "" {
		t.Fatalf("event %s = %v, want carol made owner from admin by the guard", promoted, ev)
	}
	if got := ownersOf(t, st, room); !slices.Equal(got, []string{"carol"}) {
		t.Fatalf("owners = %v, want only carol", got)
	}
}

func TestRealInfraTwoOwnersLeavingAtOnceHandOverToTheEarliestAdmin(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol", "dave"})
	room := parseRoom(t, roomID)
	setRoleAs(t, client, itUser, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_OWNER)
	setRoleAs(t, client, itUser, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	leavers := []string{itUser, "bob"}
	resps := make([]*chatimv1.LeaveRoomResponse, len(leavers))
	errs := make([]error, len(leavers))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, u := range leavers {
		wg.Go(func() {
			<-start
			resps[i], errs[i] = leaveAs(t, client, u, roomID)
		})
	}
	close(start)
	wg.Wait()
	var newOwners []string
	for i, err := range errs {
		if err != nil || !resps[i].GetChanged() {
			t.Fatalf("%s leaves = %v, %v; want a change", leavers[i], resps[i], err)
		}
		if o := resps[i].GetNewOwner(); o != "" {
			newOwners = append(newOwners, o)
		}
	}
	if !slices.Equal(newOwners, []string{"carol"}) {
		t.Fatalf("new owners in the replies = %v, want carol once (only the last owner to leave hands over)", newOwners)
	}
	st := itStore(it, core)
	if got := ownersOf(t, st, room); !slices.Equal(got, []string{"carol"}) {
		t.Fatalf("owners = %v, want only carol", got)
	}
	for _, u := range leavers {
		if m := storedMember(t, st, room, u); m.State != domain.MemberRemoved {
			t.Fatalf("%s = %+v, want left", u, m)
		}
	}
	head := awaitStored(t, "member count", func() (domain.Room, error) { return st.Get(t.Context(), room) },
		func(r domain.Room) bool { return r.MemberCount == 2 })
	if head.MemberCountVer == 0 {
		t.Fatalf("room = %+v, want a recount version", head)
	}
}

func TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol", "dave"})
	room := parseRoom(t, roomID)
	setRoleAs(t, client, itUser, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_OWNER)
	setRoleAs(t, client, itUser, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	setRoleAs(t, client, itUser, roomID, "dave", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	for _, pair := range [][2]string{{"carol", "dave"}, {"dave", "carol"}} {
		if _, err := removeMemberAs(t, client, pair[0], roomID, pair[1]); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("admin %s removes admin %s = %v, want PermissionDenied", pair[0], pair[1], err)
		}
	}
	pairs := [][2]string{{itUser, "bob"}, {"bob", itUser}}
	errs := make([]error, len(pairs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Go(func() {
			<-start
			_, errs[i] = removeMemberAs(t, client, p[0], roomID, p[1])
		})
	}
	close(start)
	wg.Wait()
	won := -1
	for i, err := range errs {
		switch status.Code(err) {
		case codes.OK:
			if won >= 0 {
				t.Fatalf("both owners removed each other: %v", errs)
			}
			won = i
		case codes.PermissionDenied:
		default:
			t.Fatalf("%s removes %s = %v, want success or PermissionDenied", pairs[i][0], pairs[i][1], err)
		}
	}
	if won < 0 {
		t.Fatalf("neither removal succeeded: %v", errs)
	}
	st := itStore(it, core)
	if m := storedMember(t, st, room, pairs[won][1]); m.State != domain.MemberRemoved {
		t.Fatalf("loser %s = %+v, want removed", pairs[won][1], m)
	}
	if got := ownersOf(t, st, room); !slices.Equal(got, []string{pairs[won][0]}) {
		t.Fatalf("owners = %v, want only %s", got, pairs[won][0])
	}
}
```

`apps/core/member_events_integration_test.go`:

```go
package main

import (
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraMemberEventsReachTheRoomAndTheUserSubjects(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	frank := subscribeUserLive(t, it, core.cfg, "frank")
	userSubject := core.cfg.Stream.LiveRoot + "." + itTenant + ".user.frank.evt."
	roomSubject := core.cfg.Stream.LiveRoot + "." + itTenant + ".room." + roomID + ".evt."

	if resp := addMembersAs(t, client, itUser, roomID, "add-frank", "frank"); len(resp.GetAdded()) != 1 || resp.GetAdded()[0].GetVer() != 1 {
		t.Fatalf("AddMembers(frank) = %v, want frank at version 1", resp)
	}
	msg, ev := awaitLiveMsg(t, frank, pbconv.MemberUserEventID(room, "frank", 1))
	if msg.Subject != userSubject+"member_added" || ev.GetRecipient() != "frank" || ev.GetMemberAdded().GetUser() != "frank" || ev.GetMemberAdded().GetRequestId() != "add-frank" {
		t.Fatalf("frank's copy on %q = %v, want member_added for frank on his own subject", msg.Subject, ev)
	}
	if msg, ev := awaitLiveMsg(t, live, pbconv.MemberEventID(room, "frank", 1)); msg.Subject != roomSubject+"member_added" || ev.GetRecipient() != "" {
		t.Fatalf("room copy on %q = %v, want the room subject without a recipient", msg.Subject, ev)
	}
	assertNoLiveIDs(t, live, time.Second, pbconv.MemberUserEventID(room, "frank", 1))

	if resp, err := removeMemberAs(t, client, itUser, roomID, "frank"); err != nil || !resp.GetChanged() || resp.GetVer() != 2 {
		t.Fatalf("RemoveMember(frank) = %v, %v; want a change at version 2", resp, err)
	}
	msg, ev = awaitLiveMsg(t, frank, pbconv.MemberUserEventID(room, "frank", 2))
	if r := ev.GetMemberRemoved(); msg.Subject != userSubject+"member_removed" || r.GetUser() != "frank" || r.GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED {
		t.Fatalf("frank's removal on %q = %v, want member_removed (removed) on his own subject", msg.Subject, ev)
	}
	stream, err := it.js.Stream(t.Context(), core.cfg.Stream.Name)
	if err != nil {
		t.Fatalf("stream %s: %v", core.cfg.Stream.Name, err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if rp := info.Config.RePublish; rp == nil || rp.Source != core.cfg.Stream.SubjectRoot+".*.*.*.*" {
		t.Fatalf("stream RePublish = %+v, want source %s.*.*.*.*", rp, core.cfg.Stream.SubjectRoot)
	}
}

func TestRealInfraMemberCountConvergesAfterAddsAndARemoval(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	addMembersAs(t, client, itUser, roomID, "add-three", "u1", "u2", "u3")
	if _, err := removeMemberAs(t, client, itUser, roomID, "u2"); err != nil {
		t.Fatalf("RemoveMember(u2): %v", err)
	}
	st := itStore(it, core)
	head := awaitStored(t, "member count", func() (domain.Room, error) { return st.Get(t.Context(), room) },
		func(r domain.Room) bool { return r.MemberCount == 4 })
	id := pbconv.MemberCountEventID(room, head.MemberCountVer)
	if c := awaitLiveEvents(t, live, id)[id].GetMemberCountChanged(); c.GetMemberCount() != 4 || c.GetMemberCountVer() != head.MemberCountVer {
		t.Fatalf("member_count_changed = %v, want 4 at version %d", c, head.MemberCountVer)
	}
}

func TestRealInfraReadReceiptsGoToTheUserForBigGroupsAndToTheRoomForDirectRooms(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["READ_RECEIPT_MAX_MEMBERS"] = "2"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	group := createRoomWith(t, client, []string{itUser, "bob", "carol"})
	groupLive, bob := subscribeLive(t, it, core.cfg, group), subscribeUserLive(t, it, core.cfg, "bob")
	sendAs(t, client, itUser, group, "group-1", "first")
	sendAs(t, client, itUser, group, "group-2", "second")
	if r := markReadAs(t, client, "bob", group, 0); r.GetReadSeq() != 2 || r.GetReadVer() != 1 {
		t.Fatalf("bob reads the group = %v, want seq 2 at read_ver 1", r)
	}
	if r := markUnreadAs(t, client, "bob", group, 2); r.GetReadSeq() != 1 || r.GetReadVer() != 2 {
		t.Fatalf("bob marks seq 2 unread = %v, want seq 1 at read_ver 2", r)
	}
	if r := markReadAs(t, client, "bob", group, 0); r.GetReadSeq() != 2 || r.GetReadVer() != 3 {
		t.Fatalf("bob reads the group again = %v, want seq 2 at read_ver 3", r)
	}
	groupRoom := parseRoom(t, group)
	first, last := pbconv.ReadEventID(groupRoom, "bob", 1), pbconv.ReadEventID(groupRoom, "bob", 3)
	msg, ev := awaitLiveMsg(t, bob, last)
	if u := ev.GetReadUpdated(); ev.GetRecipient() != "bob" || u.GetReadSeq() != 2 || u.GetReadVer() != 3 {
		t.Fatalf("read_updated on %q = %v, want bob at seq 2 read_ver 3 on his own subject", msg.Subject, ev)
	}
	assertNoLiveIDs(t, groupLive, time.Second, first, last)

	dm, err := client.CreateRoom(caller(t.Context()), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{itUser, "bob"}})
	if err != nil {
		t.Fatalf("create a direct room: %v", err)
	}
	dmID := dm.GetRoom().GetId()
	dmLive := subscribeLive(t, it, core.cfg, dmID)
	sendAs(t, client, itUser, dmID, "dm-1", "hello")
	if r := markReadAs(t, client, "bob", dmID, 0); r.GetReadSeq() != 1 || r.GetReadVer() != 1 {
		t.Fatalf("bob reads the direct room = %v, want seq 1 at read_ver 1", r)
	}
	dmRead := pbconv.ReadEventID(parseRoom(t, dmID), "bob", 1)
	if _, ev := awaitLiveMsg(t, dmLive, dmRead); ev.GetRecipient() != "" || ev.GetReadUpdated().GetUser() != "bob" {
		t.Fatalf("direct room read_updated = %v, want it on the room subject without a recipient", ev)
	}
}
```

Ghi chú (e): group 3 người > 2 nên mọi `read_updated` của group chỉ đi subject user; `v1` gửi ngay, `v2` và `v3` rơi trong cửa sổ 1s nên chỉ `v3` (lớn nhất) được gửi sau cửa sổ. Test chỉ chờ `v3` trên subject user và kiểm không id nào của group tới subject room (không kiểm `v2` vắng: nếu máy chậm hơn 1s giữa hai lệnh thì `v2` được gửi ngay, vẫn đúng).

`apps/core/member_access_integration_test.go`:

```go
package main

import (
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	sendAs(t, client, "bob", roomID, "bob-1", "warm the member cache")
	sendAs(t, client, "bob", roomID, "bob-2", "still a member")
	if resp, err := removeMemberAs(t, client, itUser, roomID, "bob"); err != nil || !resp.GetChanged() {
		t.Fatalf("RemoveMember(bob) = %v, %v; want a change", resp, err)
	}
	bob := callerAs(t.Context(), "bob")
	_, err := client.SendMessage(bob, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "bob-3", Text: "sent after the removal"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("send after the removal = %v, want PermissionDenied at once, not after the 10s cache TTL", err)
	}
	_, err = client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 10})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("history after the removal = %v, want PermissionDenied", err)
	}
	if _, err := client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: roomID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("read after the removal = %v, want PermissionDenied", err)
	}
	if seq := sendAs(t, client, itUser, roomID, "alice-1", "after the removal"); seq != 3 {
		t.Fatalf("alice's send after the removal got seq %d, want 3", seq)
	}
}

func TestRealInfraAddMembersRetryWithTheSameRequestIDNeverReAddsARemovedUser(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	for i := range 2 {
		resp := addMembersAs(t, client, itUser, roomID, "add-erin", "erin")
		if len(resp.GetAdded()) != 1 || resp.GetAdded()[0].GetUser() != "erin" || resp.GetAdded()[0].GetVer() != 1 {
			t.Fatalf("AddMembers(erin) try %d = %v, want erin at version 1 both times", i+1, resp)
		}
	}
	if resp, err := removeMemberAs(t, client, itUser, roomID, "erin"); err != nil || resp.GetVer() != 2 {
		t.Fatalf("RemoveMember(erin) = %v, %v; want version 2", resp, err)
	}
	if resp := addMembersAs(t, client, itUser, roomID, "add-erin", "erin"); len(resp.GetAdded()) != 0 {
		t.Fatalf("late retry of add-erin = %v, want nothing added", resp)
	}
	st := itStore(it, core)
	if m := storedMember(t, st, room, "erin"); m.State != domain.MemberRemoved || m.Ver != 2 {
		t.Fatalf("erin after the late retry = %+v, want still removed at version 2", m)
	}
	if _, err := client.GetHistory(callerAs(t.Context(), "erin"), &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("erin reads after the late retry = %v, want PermissionDenied", err)
	}
	if resp := addMembersAs(t, client, itUser, roomID, "add-erin-again", "erin"); len(resp.GetAdded()) != 1 || resp.GetAdded()[0].GetVer() != 3 {
		t.Fatalf("a new request id = %v, want erin back at version 3", resp)
	}
}

func TestRealInfraClearHistoryHidesByTimeAndSurvivesARejoin(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	for i := range 3 {
		sendAs(t, client, itUser, roomID, "before-"+strconv.Itoa(i+1), "before the clear")
	}
	cleared := clearHistoryAs(t, client, "bob", roomID)
	st := itStore(it, core)
	before := storedMember(t, st, room, "bob")
	if !before.ClearedBeforeTime.Equal(cleared) || before.Ver != 1 {
		t.Fatalf("bob = %+v, want cleared_before_time %v and version 1 (clear never touches ver)", before, cleared)
	}
	sendAs(t, client, itUser, roomID, "after-1", "after the clear")
	if resp, err := removeMemberAs(t, client, itUser, roomID, "bob"); err != nil || resp.GetVer() != 2 {
		t.Fatalf("RemoveMember(bob) = %v, %v; want version 2", resp, err)
	}
	sendAs(t, client, itUser, roomID, "after-2", "while bob is out")
	if resp := addMembersAs(t, client, itUser, roomID, "rejoin-bob", "bob"); len(resp.GetAdded()) != 1 || resp.GetAdded()[0].GetVer() != 3 {
		t.Fatalf("re-add = %v, want bob at version 3", resp)
	}
	after := storedMember(t, st, room, "bob")
	if !after.ClearedBeforeTime.Equal(cleared) || after.ReadSeq != 5 || after.State != domain.MemberActive {
		t.Fatalf("bob after the re-add = %+v, want the clear mark kept and read_seq raised to 5", after)
	}
	history := historyAs(t, client, "bob", roomID)
	for _, msg := range storedMessages(t, st, room) {
		hidden := !msg.CreatedAt.After(cleared)
		if (msg.Seq <= 3) != hidden && msg.Seq != 4 {
			t.Fatalf("seq %d created at %v against the clear time %v: hidden %v, want only seq 1-3 before it", msg.Seq, msg.CreatedAt, cleared, hidden)
		}
		if m, ok := history[msg.Seq]; !ok || m.GetHidden() != hidden {
			t.Fatalf("seq %d in bob's history = %v (present %v), want hidden %v (created_at at or before the clear time)", msg.Seq, m, ok, hidden)
		}
	}
	if r := markReadAs(t, client, "bob", roomID, 0); r.GetReadSeq() != 5 {
		t.Fatalf("bob reads after the re-add = %v, want seq 5 unchanged", r)
	}
}
```

Ghi chú (h): view phải ẩn đúng tin có `created_at ≤ T`, nên test so `hidden` của từng tin với `created_at` đã lưu (`storedMessages` có sẵn ở `shutdown_integration_test.go`) thay vì đoán theo thứ tự gọi: seq 1–3 chắc chắn trước T, seq 5 (gửi sau khi bị xoá) chắc chắn sau T; seq 4 gửi ngay sau khi `ClearHistory` trả lời nên thường sau T, nhưng nếu rơi cùng millisecond thì bị ẩn — đó là giới hạn "lệch vài ms quanh lúc bấm" đã chấp nhận (D97), test chấp nhận cả hai. `read_seq` của `bob` lúc tạo room là 0; vào lại khi seq cuối là 5 → `max(0, 5) = 5`.

`apps/core/member_feed_integration_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestRealInfraReadPositionAndClearHistoryNeverEnterTheWorkStream(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	core.awaitTerm(t)
	records := subscribeSubject(t, it, core.cfg.Work.SubjectRoot+".>")
	addMembersAs(t, client, itUser, roomID, "add-dave", "dave")
	awaitLiveIDs(t, records, time.Now(), "g:"+pbconv.MemberEventID(room, "dave", 1))
	st := itStore(it, core)
	joined := storedMember(t, st, room, "dave")

	sendAs(t, client, itUser, roomID, "feed-1", "one")
	sendAs(t, client, itUser, roomID, "feed-2", "two")
	markReadAs(t, client, "dave", roomID, 0)
	markUnreadAs(t, client, "dave", roomID, 2)
	clearHistoryAs(t, client, "dave", roomID)
	sentinel := sendAs(t, client, itUser, roomID, "feed-3", "sentinel")
	mark := "m:" + pbconv.MessageEventID(room, 0, sentinel)
	deadline := time.After(itLiveLimit)
	var seen []string
	for done := false; !done; {
		select {
		case m := <-records:
			id := m.Header.Get(jetstream.MsgIDHeader)
			seen = append(seen, id)
			done = id == mark
		case <-deadline:
			t.Fatalf("sentinel record %s did not arrive within %v; saw %v", mark, itLiveLimit, seen)
		}
	}
	for _, id := range seen {
		if strings.HasPrefix(id, "g:") {
			t.Fatalf("work record %s arrived after read, unread and clear; want only message records before %s (saw %v)", id, mark, seen)
		}
	}
	assertNoLiveIDs(t, records, time.Second, "g:"+pbconv.MemberEventID(room, "dave", 2))
	after := storedMember(t, st, room, "dave")
	if after.Ver != joined.Ver || !after.UpdatedAt.Equal(joined.UpdatedAt) || after.ReadVer != joined.ReadVer+2 {
		t.Fatalf("dave = %+v, want version and updated_at untouched by read, unread and clear (read_ver %d → %d)", after, joined.ReadVer, joined.ReadVer+2)
	}
}
```

Ghi chú (i): subscription NATS thường trên subject của work stream thấy mọi record reader publish (JetStream publish là một NATS publish trên đúng subject đó; consumer WorkQueue vẫn nhận bình thường). Reader đọc change stream theo thứ tự commit nên record của tin mốc tới sau mọi thay đổi trước nó; `MarkRead`, `MarkUnread`, `ClearHistory` không đổi `ver` nên không có change nào trong feed. Đợi thêm 1s cho bản thứ hai của doc `dave` (`ver 2`) là để bắt lỗi feed nhận nhầm update không có `ver`. `dave` vào ở `read_seq` 0 của room mới (chưa có tin), `MarkRead(0)` → seq 2 (`read_ver +1`), `MarkUnread(2)` → seq 1 (`read_ver +1`).

**Step 3: Biên dịch**

Run: `make -s go ARGS="vet ./apps/core/"` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: vet sạch; test PASS (itest skip). Lỗi tên (`st.MembersOf`, `st.Owners`, `st.OwnerState`, `st.BeginOwnerChange`, getter proto `GetVer`, `GetReadVer`, `GetRequestId`, `GetClearedBeforeTime`) nghĩa là Part A/B đặt tên khác hợp đồng: chỉ sửa tên trong test, ghi vào báo cáo. `wc -l apps/core/*member*_test.go apps/core/it_members_test.go` → mỗi file < 200 (`it_members_test.go` ~170, `member_owner_integration_test.go` ~183, `member_events_integration_test.go` ~117, `member_access_integration_test.go` ~111, `member_feed_integration_test.go` ~55). `member_owner_integration_test.go` ≥ 195 → chuyển `TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner` sang file mới `member_race_integration_test.go`.

**Step 4: Chạy trên hạ tầng thật**

Chạy riêng các test mới trước bằng makefile scratchpad của "Quy tắc chung" (`make -f <scratchpad>/itest-one.mk itest-one RUN='TestRealInfra(OwnerGuard|TwoOwners|MemberEvents|MemberCount|ReadReceipts|RemovedMember|AddMembersRetry|ClearHistoryHides|ReadPositionAndClear)' PKG=./apps/core/`), rồi cả repo:

```bash
make infra-up && make itest
```

Expected: mọi package `ok`, gồm 11 itest mới (`TestRealInfraOwnerGuardFinishesAnOwnerChangeACoreLeftHalfDone`, `TestRealInfraOwnerGuardPromotesTheEarliestAdminOfAGroupWithoutOwner`, `TestRealInfraTwoOwnersLeavingAtOnceHandOverToTheEarliestAdmin`, `TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner`, `TestRealInfraMemberEventsReachTheRoomAndTheUserSubjects`, `TestRealInfraMemberCountConvergesAfterAddsAndARemoval`, `TestRealInfraReadReceiptsGoToTheUserForBigGroupsAndToTheRoomForDirectRooms`, `TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache`, `TestRealInfraAddMembersRetryWithTheSameRequestIDNeverReAddsARemovedUser`, `TestRealInfraClearHistoryHidesByTimeAndSurvivesARejoin`, `TestRealInfraReadPositionAndClearHistoryNeverEnterTheWorkStream`), drill resync (Task 17), itest RePublish (Task 3), contract `storetest` Mongo (`RunMembers`, `RunMemberFeed`), feed-skip (Task 6), các itest M2b.2/M2b.3. Không skip (kiểm log `--- PASS` cho các tên trên). Chạy `-count=1` mặc định; test đồng thời (b, b') flaky thì chạy riêng test đó 3 lần bằng makefile scratchpad, ghi kết quả, báo controller (không thêm sleep).

**Step 5: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py apps/core tests + ";itest (owner_guard finishes an owner change a core left half done and counts owner_repaired_total; promotes the earliest admin of a group left without owner);itest (two owners leaving at once hand over to the earliest admin once; two owners removing each other leave one owner; admins cannot remove admins);itest (member events reach the room subject and the user subject with recipient through RePublish evt.*.*.*.*; member_count converges; read_updated on the user subject above READ_RECEIPT_MAX_MEMBERS and on the room subject of a DM);itest (a removed member is denied at once through the actor cache; a same request_id retry never re-adds a removed user; clear history hides by time and survives a rejoin);itest (read, unread and clear never enter the work stream)"
python3 bin/indexes_edit.py apps/core decisions + ";D97;D99;D100;D101;D105;D106"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/it_members_test.go apps/core/member_owner_integration_test.go apps/core/member_events_integration_test.go apps/core/member_access_integration_test.go apps/core/member_feed_integration_test.go
git commit -m "test(core): cover owner repair, owner races, member events, read receipts and access on real infra" -- apps/core/it_members_test.go apps/core/member_owner_integration_test.go apps/core/member_events_integration_test.go apps/core/member_access_integration_test.go apps/core/member_feed_integration_test.go INDEXES.csv
```

Expected: `ok`, `{7}`, sạch; commit 5 file (6 nếu tách ở Step 3) + `INDEXES.csv`.

Task trung bình: controller kiểm nhanh (test khớp danh sách (a)–(i), không sửa code sản phẩm), không reviewer.

---

### Task 20: Docs + kiểm chứng cuối milestone, push và chuẩn bị merge `feat/m2b` → `main`

Task cuối, rủi ro thấp (docs) cộng kiểm chứng mốc cuối milestone theo bảng "Verification and review budget": controller kiểm nhanh, không reviewer (trừ khi Step 1 phải tách file code: khi đó commit tách có một reviewer nhanh, chỉ diff đó). M2b.4 không phải milestone perf: không sweep corebench trước/sau, chỉ một lần chạy ngắn để chắc đường gửi (cache member của actor theo thế hệ + TTL) không lỗi. Đây cũng là mốc cuối của cả nhánh `feat/m2b` (M2b.0–M2b.4), nên task kết thúc bằng checklist Definition of Done của `docs/git-workflow.md` và bản nháp PR trong `bin/`. **Không mở PR**: controller hỏi owner trước.

Docs theo luật owner mới (2026-10-07):
- Thiết kế có tên field đầy đủ với **một bảng field cho mỗi collection** (§5), `messages` giữ tên ngắn kèm bảng tên đầy đủ; §4, §5.1, §6.2, §6.4, §7, §8.2, §8.3, §9.2, §9.3, §10–§14, §16 theo cơ chế mới; Decision Log thêm D96–D108, sửa D67, D72, D85.
- Roadmap: luật "mỗi plan có bản tóm tắt **kỹ thuật** (luồng, field, cơ chế, quyết định, chi phí, rủi ro, cách kiểm) owner phản biện trước khi thực thi", luật tên field, dòng M2b.4, khối "Từ M2b.4".
- `CLAUDE.md`: mục Done, khối Field names, Key encoding, Counters, Storage ports, Redis, Effect engine, khối Members and read position, Detectors (17 luật), Lifecycle (27.2s), Shard-readiness, Docs.

Các sửa docs được viết sẵn thành script Python (thay chuỗi chính xác, mỗi chuỗi cũ phải khớp **đúng một lần**, không thì script thoát lỗi và không ghi gì thêm), đã chạy thử trên bản HEAD `a560214` của bốn file. Tasks 1–19 không sửa thiết kế, roadmap, `CLAUDE.md`; chỉ Task 15 sửa bảng env của README (các thao tác README dưới không chạm bảng đó, trừ một thao tác tuỳ chọn cho `CORE_SHUTDOWN_BUDGET`).

**Step 0: Dừng nếu cây làm việc còn thay đổi chưa commit**

```bash
git status --porcelain -- CLAUDE.md README.md INDEXES.csv docs/designs/261005-chatim-architecture.md docs/roadmap.md docs/plans/2026-10-06-m2b4-members-read.md docs/plans/2026-10-06-m2b4-members-read-summary.md
git log --oneline -1
```

Expected: lệnh đầu không in gì (có dòng → **dừng, hỏi controller**; không commit chung thay đổi của người khác, không `stash`/`checkout` chúng); lệnh hai là commit Task 19.

**Step 1: Kích thước file (tách trước khi kiểm)**

```bash
git diff --name-only a560214..HEAD -- '*.go' '*.proto' '*.sh' '*.yml' '*.yaml' | grep -v '^pkg/pb/' | xargs wc -l | sort -n | tail -15
wc -l proto/chatim/v1/*.proto
```

Expected: không file nào > 200 dòng (code, test, proto, script; `pkg/pb` sinh ra được miễn). Có file > 200 → **tách ngay**, mỗi file một commit `refactor(<package>): split <file>` (theo M2b.3, `7d17bb8`):
- Go: chuyển nguyên văn một nhóm hàm/kiểu liền nhau (hoặc một nhóm test và helper riêng của chúng) sang file mới cùng package, tên file mô tả nội dung (vd. `member_owner_cas.go`, `members_paging_test.go`); không đổi tên, chữ ký, hành vi; `make -s go ARGS="test -race -shuffle=on ./<package>/..."` PASS như trước; `make fmt-check && make vet && make lint`.
- Proto: chuyển message sang file mới cùng package (`proto/chatim/v1/<tên>.proto`), file cũ `import` file mới; không đổi tên, số field; `make proto && make buf-lint`; `make -s go ARGS="build ./..."`.
- Sửa dòng `INDEXES.csv` của package đó nếu `key_symbols` nhắc tên file (thường không).
- Có tách code → Step 13 (`make itest`) bắt buộc chạy lại.

File 180–200 dòng ghi vào "Lỗi Minor còn mở" (dòng "File gần 200 dòng").

**Step 2: Kiểm luật alert và metric (không sửa)**

```bash
grep -c "^      - alert:" deploy/prometheus/alerts.yml
make alerts-check
grep -n "owner_repaired_total\|read_events_unbatched_total" apps/core/metrics_wiring.go
grep -n "memberCounter\|ownerGuard\|memberEvent" apps/core/effects_wiring.go | head -3
```

Expected: `17`; `SUCCESS: 17 rules found`; hai metric trong `metrics_wiring.go` (Task 15, 16); ba effect trong `effects_wiring.go` (Task 16). Khác → dừng, báo (task trước chưa xong).

**Step 3: Viết script docs vào `bin/` (gitignored, không commit)**

`bin/m2b4_docs_lib.py`:

```python
import pathlib
import sys

ROOT = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else ".")
DATE = sys.argv[2] if len(sys.argv) > 2 else "YYYY-MM-DD"
DESIGN = "docs/designs/261005-chatim-architecture.md"
ROADMAP = "docs/roadmap.md"
README = "README.md"
CLAUDE = "CLAUDE.md"


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


def write(path, text):
    (ROOT / path).write_text(text, encoding="utf-8")


def sub(path, old, new, optional=False):
    text = read(path)
    n = text.count(old)
    if n == 0 and optional:
        print(f"skip {path}: {old[:60]!r} (already done)")
        return
    if n != 1:
        sys.exit(f"{path}: {n} matches of {old[:80]!r}, want 1")
    write(path, text.replace(old, new))


def line_index(lines, path, prefix):
    hits = [i for i, l in enumerate(lines) if l.startswith(prefix)]
    if len(hits) != 1:
        sys.exit(f"{path}: {len(hits)} lines start with {prefix[:80]!r}, want 1")
    return hits[0]


def line(path, prefix, new):
    lines = read(path).split("\n")
    lines[line_index(lines, path, prefix)] = new
    write(path, "\n".join(lines))


def after(path, prefix, new):
    lines = read(path).split("\n")
    i = line_index(lines, path, prefix)
    lines[i + 1:i + 1] = new.split("\n")
    write(path, "\n".join(lines))


def before(path, prefix, new):
    lines = read(path).split("\n")
    i = line_index(lines, path, prefix)
    lines[i:i] = new.split("\n")
    write(path, "\n".join(lines))


def between(path, start, end, new):
    lines = read(path).split("\n")
    i = line_index(lines, path, start)
    j = line_index(lines, path, end)
    if j <= i:
        sys.exit(f"{path}: {end[:60]!r} is not after {start[:60]!r}")
    lines[i:j] = new.split("\n")
    write(path, "\n".join(lines))
```

`bin/m2b4_docs_design.py` (§2.3, §4, §5, §5.1, §6.2):

```python
from m2b4_docs_lib import DESIGN, after, between, line, sub

line(DESIGN, "| Số member doc |", "| Số member doc | ~500M (10M user × 50 room) | §5 `members` (index `{tenant, user_id, state, room_id}` cho phòng của user, D98) |")

between(DESIGN, "| Lớp | Ví dụ | Ghi | Event | Sửa lỗi |", "Mỗi tính năng còn khai báo **ngân sách khuếch đại**", """| Lớp | Ví dụ | Ghi | Event | Sửa lỗi |
|---|---|---|---|---|
| Fact bất biến | tạo tin, bản sửa/xoá (`message_edits`), lần ghim (`pin_actions`) | Insert theo khoá unique (khoá là CAS). Lệnh tạo dùng cid; lệnh sửa/xoá mang `base_ver`; ghim không mang `base_pv`, server ghi `pin_ver` hiện tại + 1 (D92) | Id = khoá; feed chỉ lấy insert | Reconciler từ feed insert; ack mark theo chính sách |
| State có version (projection) | `messages` hiện tại, `rooms.pins`, room activity | Effect `set … where ver < v` từ fact (ghim: fold fact sau `pin_ver` + CAS `pin_ver == p`, D92); không bao giờ từ chối fact đã commit | Không có event riêng (event thuộc fact) | Reconciler chạy lại projection |
| Tập (target, user) | reaction (một emoji mỗi (user, tin), D88), member (một doc mỗi (room, user), D98), thread_subs, bookmark | Một lệnh atomic trên doc `_id = target│user`, mỗi doc một counter `ver` riêng (chỉ tăng, được có lỗ); cùng giá trị thì doc giữ nguyên, không ghi; gỡ/rời để lại tombstone (D89, D98). Member: `AddMembers` = upsert pipeline `$cond` + `request_id` (D99); xoá/rời/đổi role = update có điều kiện `ver`; lệnh chạm owner qua CAS `rooms.owners_ver` (D100) | Id theo doc + `ver` (reaction `{room}-{th}-{seq}-{user}-n{ver}`, D93; member `{room}-mb-{user}-v{ver}` + bản user `…-u`, D104); feed insert/replace và update có `ver`, doc hiện tại thắng | Worker phát lại doc hiện tại (`reaction_event`, `member_event`); `owner_guard` hoàn tất thay đổi owner dở (D100) |
| Aggregate theo target | số reaction theo emoji, `thread_count`, `member_count` | Recount CAS-ver với witness (§7, D90); `member_count` chỉ worker ghi, hội tụ ~1s (D102) | `counts_changed` `{target}-{counter}-v{ver}` (reaction `{room}-{th}-{seq}-reactions-v{v}`, D93); `member_count_changed` `{room}-members-v{member_count_ver}` (D102) | Touch của worker (`reaction_counter`, `member_counter`) |
| Giá trị theo người đọc | unread, `mention_unread`, view ẩn/đã xoá | Không lưu; tính lúc đọc từ fact thưa (`hidden`) và mốc `members.cleared_before_time` (D97) | Không | Không cần |
| Tần suất cao gộp được | vị trí đọc, đánh dấu chưa đọc | `members.read_seq/read_ver`: `MarkRead` chỉ nâng (`$lt`), `MarkUnread` chỉ hạ (`$gt`), mỗi lần đổi `read_ver + 1`; chỉ toán tử thường, không đụng `ver` nên không vào feed (D105) | `read_updated` `{room}-rd-{user}-v{read_ver}` qua `readcast`: lần đầu gửi ngay, phần đuôi gộp trong `READ_RECEIPT_WINDOW` mỗi (room, user); subject room khi DM hoặc nhóm ≤ `READ_RECEIPT_MAX_MEMBERS`, không thì subject user (D105) | Không ack mark, không đường bù (best-effort) |
| Ephemeral | typing, presence | Không lưu; gateway ↔ NATS core, không qua core | Subject `live.*.eph.*` | Không |
""")

between(DESIGN, "| Collection | `_id` / khoá | Lớp | Trường chính | Index | Trạng thái |", "### 5.1 Sẵn sàng sharding", """| Collection | `_id` / khoá | Lớp | Index | Trạng thái |
|---|---|---|---|---|
| `messages` (clustered) | 24B `room│thread_root│seq` | Fact (tạo) + projection (sửa/xoá) + aggregate (`rx`) | **không có index phụ** | Đã xây (tạo; sửa/xoá M2b.2; `rx` M2b.3) |
| `message_edits` (clustered) | 28B `room│thread_root│seq│ver` | Fact | `{room_id, created_at}` (D70) | Đã xây (M2b.2) |
| `pin_actions` (clustered) | 16B `room│pin_ver` | Fact | `{room_id, created_at}` (D70) | Đã xây (M2b.3) |
| `rooms` | `room_id` (int64) | Metadata + projection + aggregate | `{activity_bucket}` (D69); `{created_at}` (resync) | Đã xây (activity M2b.1; ghim M2b.3; `member_count_ver`, `owners_ver`, `pending_owner_change` M2b.4) |
| `members` (clustered) | `room(8)│user` (`keys.Member`, D98) | Tập + vị trí đọc + mốc clear | `{room_id, state, role, joined_at, user_id}`; `{tenant, user_id, state, room_id}` | Đã xây (M2b.4) |
| `reactions` (clustered) | `message_key│user` (24B khoá tin + byte user) | Tập | `{message_key, emoji}` (đếm phủ index); `{room_id, updated_at}` (resync) (D88) | Đã xây (M2b.3) |
| `hidden` | ObjectId | Fact thưa theo người đọc | `{user_id, room_id, thread_root, seq}` unique | Đã xây (M2b.2) |
| `reconciler_state` | tên feed (`changes`) | Vận hành | — | Đã xây |
| `thread_subs`, `bookmarks` | — | Tập | — | Chưa xây (M2c) |
| `user_rooms` | — | Projection "phòng của user" | — | Bỏ ở M2b.4 (D98); trở lại dạng projection khi shard |
| `room_events` (clustered) | `room│pts` | — | — | Bỏ, không dùng từ M2a.1 |

**Tên field (D96).** Mọi collection trừ `messages` dùng từ tiếng Anh cơ bản, đầy đủ. Counter chỉ tăng, được có lỗ, có đuôi `_ver`; audit là `updated_at/updated_by`; fact dùng `created_at/created_by`. Không dùng `version`, `change_number`. `messages` giữ tên ngắn vì là collection lớn nhất; bảng dưới ghi tên đầy đủ tương ứng.

`messages` (tên ngắn):

| Field | Tên đầy đủ | Kiểu | Ý nghĩa |
|---|---|---|---|
| `_id` | — | BinData 24B | `room│thread_root│seq` |
| `t` | tenant | string | |
| `f` | from | string | người gửi |
| `k` | kind | | loại tin (hiện chỉ `text`) |
| `x` | text | string | nội dung; tin xoá thì rỗng |
| `c` | cid | string | id client, chống trùng (D34) |
| `ts` | created_at | date | giờ server lúc ghi |
| `v` | version | int32 | version sửa/xoá đã áp; thiếu = 0 |
| `d` | deleted | bool | đã xoá cho mọi người |
| `ea` | edited_at | date | lần sửa cuối |
| `rx` | reactions | doc | `{c: [{e: emoji, n: số}], v: version của summary}`, mảng sắp `n` giảm rồi `e` (D90) |

`rooms`:

| Field | Kiểu | Ý nghĩa |
|---|---|---|
| `_id` | int64 | room id (63-bit ngẫu nhiên, khác 0) |
| `tenant`, `type`, `name` | string | `type` là `dm` hoặc `group` |
| `created_by`, `created_at` | string, date | |
| `member_count` | int | số member `state = 1`; lúc tạo = số member, sau đó chỉ worker `member_counter` ghi (D102) |
| `member_count_ver` | int64 | CAS của recount; thiếu = 0 (D102) |
| `owners_ver` | int64 | CAS của lệnh chạm owner; thiếu = 0 (D100) |
| `pending_owner_change` | doc | thay đổi owner đang làm `{action, user_id, user_ver, role, successor_id, successor_ver, request_id, updated_by, updated_at}`; thiếu khi không có (D100) |
| `last_seq`, `last_message_at`, `last_change_at`, `activity_bucket` | int64, date, date, int64 | room activity, chỉ `$max` của worker `room_activity`; `activity_bucket` = giờ của `last_change_at` (D69) |
| `pins` | array | `[{thread_root, seq, pinned_by, pinned_at, pin_ver}]`, mới nhất trước (D92); `Rooms.Get` không đọc |
| `pin_ver` | int64 | version ghim dày (D92) |

`members` (D98):

| Field | Kiểu | Ý nghĩa |
|---|---|---|
| `_id` | BinData | `keys.Member(room, user)` = room 8 byte big-endian + byte user |
| `room_id`, `tenant`, `user_id` | int64, string, string | |
| `role` | string | `owner`, `admin` hoặc `member` |
| `state` | int32 | 1 = active, 2 = đã rời/bị xoá (tombstone, không bao giờ xoá doc); luôn ghi rõ |
| `joined_at` | date | lần vào (lại) cuối |
| `ver` | int64 | số lần membership đổi (vào, rời, vào lại, đổi role); chỉ tăng, ≤ MaxUint32 |
| `previous_role`, `previous_state` | string, int32 | lần đổi cuối đến từ đâu (event phân biệt thêm, xoá, rời, đổi role) |
| `request_id` | string | lệnh làm lần đổi cuối (`AddMembers` của client; tạo room = `{room}-created`; lệnh khác do server sinh) |
| `updated_at`, `updated_by` | date, string | lần đổi membership cuối; `updated_by` rỗng khi `owner_guard` sửa |
| `cleared_before_time` | date | mốc clear history, chỉ `$max` (D97) |
| `read_seq`, `read_ver` | int64 | vị trí đọc và số lần nó đổi (D105) |

`message_edits`: `_id` (28B), `room_id`, `tenant`, `kind` (1 sửa, 2 xoá), `created_by`, `text` (bản mới; xoá dọn text của bản ≤ v−1), `previous_text` (chỉ ở v1: bản gốc), `created_at`.

`pin_actions`: `_id` (16B `room│pin_ver`), `room_id`, `tenant`, `action` (1 ghim, 2 bỏ ghim), `thread_root`, `seq`, `created_by`, `created_at`.

`reactions`: `_id` (`message_key│user`), `message_key` (24B), `room_id`, `tenant`, `user_id`, `emoji` (`""` = đã gỡ), `previous_emoji`, `ver` (số lần đổi; id event giữ chữ `-n{ver}`), `updated_at`.

`hidden`: `user_id`, `room_id`, `thread_root`, `seq`. `reconciler_state` (`_id: "changes"`): `resume_token` (vị trí feed đã xác nhận), `cluster_time` (mốc bootstrap).

`rooms.last_seq/last_message_at/last_change_at/activity_bucket` chỉ đổi qua `$max` của effect `room_activity` (không bao giờ lùi); `activity_bucket = floor(last_change_at/1h)` là giờ hoạt động **cuối**, nên truy vấn resync lấy `activity_bucket ≥ giờ(from)` hoặc `created_at ∈ [from, to]`. **Không quét khoảng `_id` của `members`**: Mongo so BinData theo độ dài trước, rồi subtype, rồi byte, nên khoảng byte không phải khoảng tiền tố room; truy vấn theo room đi index `room_id`, theo người dùng `_id` bằng (`$in`).
""")

line(DESIGN, "| Collection lớn (`messages`", "| Collection lớn (`messages`, `message_edits`, `pin_actions`, `reactions`, `members`) có `_id` prefix room; shard key tương lai `{_id: 1}` | Một room trên một shard; unique toàn cluster |")
line(DESIGN, "| `messages` **không** có index phụ", "| `messages` **không** có index phụ. Collection fact nhỏ (1–3% lưu lượng) được có index `{room_id, created_at}` (D70) | Giữ working set của `messages` nhỏ; fact nhỏ cần quét theo thời gian cho resync |")
sub(DESIGN, "| `reactions` clustered, `_id = k│u`, shard key `{_id: 1}` (D88, thay D68); index `{k, e}` và `{r, ts}` |", "| `reactions` clustered, `_id = message_key│user`, shard key `{_id: 1}` (D88, thay D68); index `{message_key, emoji}` và `{room_id, updated_at}` |")
sub(DESIGN, "nên `Between` của resync sẽ scatter-gather khi đã shard (công cụ thủ công, chấp nhận); tombstone không bao giờ dọn (cố ý: giữ `n`) |", "nên `Between` của resync sẽ scatter-gather khi đã shard (công cụ thủ công, chấp nhận); tombstone không bao giờ dọn (cố ý: giữ `ver`) |")
sub(DESIGN, "`{r, ts}` không có prefix khoá tin", "`{room_id, updated_at}` không có prefix khoá tin")
line(DESIGN, "| `user_rooms` shard theo `u`", "| `members` clustered, `_id = room│user`, shard key `{_id: 1}` (D98); index `{room_id, state, role, joined_at, user_id}` và `{tenant, user_id, state, room_id}`; `user_rooms` (D72) trở lại dạng projection khi shard | Member của một room nằm trên một shard; \"phòng của user\" là range trên index `{tenant, user_id, …}` (scatter-gather khi đã shard, nên khi đó dựng lại `user_rooms` theo user) |")

line(DESIGN, "7. `CreateRoom` ghi room trước, member sau (D35).", "7. `CreateRoom` ghi room trước (`member_count` = số member), rồi doc `members` của từng người (`ver` 1, `state` 1, `request_id = {room}-created`; D35, D98); số member mỗi lệnh ≤ `MEMBER_BATCH_MAX` (D107); fast path phát `room_created` và bản user của `member_added` (không có bản room, D104).")
```

`bin/m2b4_docs_design2.py` (§6.4, §7, §8.2, §8.3):

```python
from m2b4_docs_lib import DESIGN, after, between, line, sub

between(DESIGN, "### 6.4 Tập và vị trí đọc", "## 7. Counter theo target", """### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear M2b.2, reaction M2b.3, member + vị trí đọc M2b.4]

- Reaction [Đã xây, M2b.3, D88, D89, D93, D95]: một emoji cho mỗi (user, tin); emoji khác thay emoji cũ, emoji rỗng là gỡ ("nhiều trên một user" là reply/mention, M2c). Doc `reactions {_id: message_key│user, message_key, room_id, tenant, user_id, emoji, previous_emoji, ver, updated_at}`. Đặt = một upsert `FindOneAndUpdate({_id}, pipeline, upsert, trả doc trước ghi)`; pipeline dùng `$cond`: emoji đang lưu bằng emoji mới thì giữ nguyên `previous_emoji/emoji/ver/updated_at`, nên doc không đổi byte nào (không có entry oplog, không có change trên feed); khác thì `previous_emoji` = emoji cũ, `emoji` = emoji mới, `ver` = `ifNull(ver, 0) + 1`, `updated_at`. Filter chỉ là phép bằng trên `_id` unique nên server tự retry upsert bị trùng khoá: không có vòng thử lại, không đọc lại. Kết quả suy từ doc trước ghi: không có doc → `ver` 1; cùng emoji → no-op; khác → `ver+1`, `previous_emoji` = emoji cũ. Gỡ = `FindOneAndUpdate` filter `{_id, emoji ≠ ""}`, không upsert, để tombstone `emoji: ""` giữ `ver` (không bao giờ chèn tombstone khi chưa có doc). Đúng trạng thái sẵn → không ghi, không event, không touch. Không cid: lệnh là trạng thái mong muốn, retry trễ có thể đưa về trạng thái cũ (chấp nhận cho lớp tập). `ValidateEmoji`: UTF-8 hợp lệ, 1–32 byte, không ký tự điều khiển. Emoji phải nằm trong danh sách cố định `REACTION_EMOJIS` (mặc định `👍,❤️,😂,😮,😢,🙏`, D95); ngoài danh sách → `INVALID_ARGUMENT` (`domain.ErrEmojiNotAllowed`), kiểm trước membership/policy; độ dài danh sách là giới hạn số loại emoji mỗi tin. `GetReactionSettings` trả danh sách theo thứ tự config để frontend chỉ hiện các emoji đó. Tin đã xoá: đặt → `FAILED_PRECONDITION`, gỡ vẫn được. Quyền `react_message` (mặc định mọi member, D94). Sau khi ghi: enqueue `reaction_changed` (id `{room}-{th}-{seq}-{u}-n{ver}`, payload `{user, emoji, previous_emoji, change}`) → touch counter inline (§7) → enqueue `counts_changed` nếu `v` tăng → trả `{change, reactions}`. Worker `reaction_event` (delay `RECONCILE_DELAY`, không ack mark): `Get` doc; `ver == rec.ver` → phát doc hiện tại, `ver > rec.ver` → bỏ (record mới hơn lo), `ver < rec.ver` → `ErrStaleRead`, Nak. Vì vậy event trung gian có thể mất (fast path lỡ mà doc đã đổi tiếp); chỉ trạng thái cuối được bảo đảm (lớp tập, D93). `GetHistory` chỉ trả số đếm (`Message.reactions`); emoji của chính người đọc để M3 (`GetReactions`).
- Member [Đã xây, M2b.4, D98–D101, D104, D106–D108]: lớp tập, một doc `members` mỗi (room, user) (field ở §5), mỗi doc một counter `ver`; không có log fact member, không đánh số dày theo room.
  - Quyền (D101, owner chốt 2026-10-06): action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read`; `access.Request` thêm `Target` (doc đích) và `Role` (role yêu cầu). `DefaultPolicy`: owner mọi việc; admin thêm người và xoá người có role `member`; chỉ owner đổi role, xoá admin/owner; ai cũng tự rời. Một group nhiều owner. DM cố định 2 người: thêm/xoá/rời/đổi role → `FAILED_PRECONDITION` (`ErrDirectRoom`). `RemoveMember(self)` → `INVALID_ARGUMENT`; người chưa từng là member rời → `PERMISSION_DENIED`; rời lần nữa (tombstone) → no-op; xoá/đổi role người không active → `NOT_FOUND`.
  - `AddMembers(users ≤ MEMBER_BATCH_MAX, request_id)` (D99, D107): validate → `Admit` + `Allow` → `Requests.Begin` trên khoá `chatim:req:{room}:{caller}:{request_id}` (RAM LRU rồi Redis dedupe, TTL `CID_COMMITTED_TTL`): đã xong → trả doc vẫn mang đúng `request_id` và người gọi (`domain.AddedBy`), nên retry trễ không thêm lại người đã bị xoá giữa chừng; đang chạy → `UNAVAILABLE` → `Messages.Last` một lần → một `BulkWrite(ordered:false)` upsert pipeline trên `_id`: doc active giữ nguyên byte (không oplog); thiếu hoặc tombstone → `state 1`, role luôn `member`, `joined_at`, `ver+1`, `previous_role/previous_state`, `request_id`, `updated_*`, `read_seq = max(read_seq, seq cuối)`, `read_ver+1`, giữ `cleared_before_time`; chuỗi người dùng qua `$literal`; filter chỉ `_id` nên server tự retry upsert trùng khoá → đọc lại doc sau ghi → `ForgetMembers` → enqueue event → Commit request. Người mới hoặc thêm lại thấy toàn bộ lịch sử, tin cũ coi như đã đọc.
  - Xoá/rời/đổi role không chạm owner: một `updateOne({_id, ver: k})` `$set` + `$inc ver`; trượt → đọc lại đích, kiểm lại, ≤3 lượt rồi `ErrRetryLater`. Rời/bị xoá giữ role trên tombstone; mất quyền đọc ngay (`Rooms.Member` lọc `state = 1`).
  - Lệnh chạm owner (owner rời/bị xoá/bị hạ, thăng owner; D100): đọc `rooms {owners_ver, pending_owner_change}`; có pending → hoàn tất trước; đọc owner active qua index (`Owners(room, 2)`); owner cuối tự hạ → `FAILED_PRECONDITION` (`ErrLastOwner`); owner cuối rời → kế nhiệm = admin vào sớm nhất, không có thì member vào sớm nhất, hoà theo user id (`Successor`); CAS `rooms {owners_ver: k, pending_owner_change: null}` → `{owners_ver: k+1, pending_owner_change: {…, user_ver, successor_ver}}`; thăng kế nhiệm **trước**, rồi hạ/xoá đích, mỗi bước là update có điều kiện `ver` đã ghi trong pending (chạy lại an toàn); xoá pending khi `owners_ver == k+1`; trượt CAS → ≤3 lượt rồi `ErrRetryLater`. Bất biến OW1: group còn member active thì còn owner active ở mọi thời điểm; đường thường không bao giờ ghi lên owner. Worker `owner_guard` hoàn tất pending của core đã chết, hoặc chọn kế nhiệm cho group không còn owner (`request_id = owners-v{k}`, `updated_by` rỗng).
  - `member_count` (D102): aggregate, chỉ worker `member_counter` ghi, hội tụ sau ~`MEMBER_COUNT_DELAY` (1s); lệnh không đụng.
  - Event (D104, không ack mark): `member_added {user, role, joined_at, ver, request_id}`, `member_removed {user, reason removed|left, previous_role, ver, request_id}` (left khi `updated_by == user`), `member_role_changed {user, role, previous_role, ver, request_id}`; loại suy từ `state/previous_state/role/previous_role` của doc. Bản room id `{room}-mb-{user}-v{ver}` trên subject room; bản user id `…-u` với envelope `recipient = user` → subject `evt.{t}.user.{user}.{type}`. Doc tạo cùng room (`ver 1`, `request_id = {room}-created`) chỉ có bản user. Không có `new_owner`: event `member_role_changed` của người kế nhiệm mang thông tin đó. Fast path và worker `member_event` cùng dựng bằng `pbconv.MemberEvents` (RC2).
  - Cache member của actor (D106): thế hệ (`Router.ForgetMembers` sau mỗi lệnh member) + TTL 10s mỗi entry; người bị xoá bị từ chối gửi ngay trên core chạy lệnh, ≤10s ở core khác (worker không xoá được cache core khác).
  - Ngân sách thêm k ở nhóm 5K: 2 read admit + 1 Redis + 1 `Last` + 1 bulk k upsert + 1 read k doc + (1+k) event; worker ≤1 write activity/room/lô + 1 recount/room/lô + 2k publish (bỏ trùng). Xoá/rời/đổi role: 2 read + 1 read đích + 1 write + 2 event. Đường owner: thêm 1 read state, ≤2 read index, 1 CAS, 2–3 write (hiếm). Thêm 500 người sinh 500 bản user.
  - Giới hạn: khoảng hở kiểm–ghi (admin bị hạ ngay sau khi lệnh xoá của họ đã được duyệt thì lệnh vẫn ghi; không transaction nhiều doc); rolling deploy phải nâng mọi core cùng lúc (D96, D103, D104).
- Vị trí đọc [Đã xây, M2b.4, D105]: `members.read_seq/read_ver`. `MarkRead(seq)` = `FindOneAndUpdate({_id, state: 1, read_seq: {$lt: s}}, {$set: {read_seq: s}, $inc: {read_ver: 1}})` (seq 0 hoặc quá seq cuối = tin mới nhất, kẹp theo seq cuối thật); `MarkUnread(seq)` cùng dạng với `$gt` → `read_seq = seq − 1` (chỉ hạ, kiểu Slack); không đổi → trả vị trí hiện tại, không ghi, không event. Chỉ toán tử thường, không bao giờ pipeline/replace/upsert, không đụng `ver` nên không vào feed (vẫn là entry oplog). Quyền `mark_read` (cả `MarkUnread`). Đổi → `readcast.Offer`: lần đầu mỗi (room, user) gửi ngay; trong `READ_RECEIPT_WINDOW` (1s, 1–2s) giữ bản `read_ver` lớn nhất rồi gửi khi hết cửa sổ; map tối đa 65536 khoá, đầy hoặc sau `Close` → gửi thẳng, đếm `read_events_unbatched_total`. Event `read_updated {user, read_seq, read_ver}` id `{room}-rd-{user}-v{read_ver}`: subject room khi DM hoặc `member_count ≤ READ_RECEIPT_MAX_MEMBERS` (20, trần cứng 50), không thì subject user (đồng bộ thiết bị). Best-effort, không đường bù. Đếm unread để M3 (§9.3). SDK nâng `read_seq` khi user gửi tin, để tin của chính mình không tích sau vị trí đọc.
- Ẩn phía tôi [Đã xây, M2b.2]: upsert `hidden {user_id, room_id, thread_root, seq}` (`mutate.Hide`: `Admit` → tin phải tồn tại → policy `HideMessage` với `Author`; mặc định cho phép). Clear history [Đã xây, M2b.2; theo thời gian từ M2b.4, D97]: `FindOneAndUpdate({_id, state: 1}, {$max: {cleared_before_time: giờ server}})` (`mutate.ClearHistory`, quyền `ClearHistory`), trả giá trị sau cập nhật nên không bao giờ lùi; ẩn tin `created_at ≤` mốc trên mọi timeline; lệch vài ms quanh lúc bấm chấp nhận; `ClearHistoryRequest.up_to_seq` reserved. Cả hai không phát event (owner 2026-10-05; đồng bộ đa thiết bị ở M3/M4) và chỉ áp lúc đọc qua `view.HideForViewer` (D85, D97).
""")

line(DESIGN, "## 7. Counter theo target [Đã xây cho reaction, M2b.3]", "## 7. Counter theo target [Đã xây cho reaction M2b.3, `member_count` M2b.4]")
sub(DESIGN, "đọc doc của từng witness (`{user, n}`) bằng majority trong một causal session, doc có `n < N` → `store.ErrStaleRead`; (2) aggregate `{$match: {k, e: {$gt: \"\"}}}, {$group: {_id: \"$e\", n: {$sum: 1}}}` phủ index `{k, e}`", "đọc doc của từng witness (`{user_id, ver}`) bằng majority trong một causal session, doc có `ver < N` → `store.ErrStaleRead`; (2) aggregate `{$match: {message_key, emoji: {$gt: \"\"}}}, {$group: {_id: \"$emoji\", count: {$sum: 1}}}` phủ index `{message_key, emoji}`")
sub(DESIGN, "witness = `max n` của mỗi user, một touch mỗi tin mỗi lô", "witness = `max ver` của mỗi user, một touch mỗi tin mỗi lô")
after(DESIGN, "**Đã xây (M2b.3, D90, tinh chỉnh D67):**", """
**Đã xây (M2b.4, D102):** `member_count` dùng chung vòng `counter.Loop[V]` (Count → bằng thì bỏ → Swap CAS → trượt thì Reload, hết lượt `ErrContended`) với `counter.MemberToucher`: witness = `(user, max ver)` của lô, `CountMembers` đọc witness rồi `CountDocuments({room_id, state: 1})` phủ tiền tố index `{room_id, state, …}` trong cùng causal session, `SetMemberCount` CAS `member_count_ver` (thiếu = 0). Chỉ worker `member_counter` (delay `MEMBER_COUNT_DELAY` 1s, ≤ `RECONCILE_DELAY`) ghi, một recount mỗi room mỗi lô; `member_count_ver > 0` → phát `member_count_changed {member_count, member_count_ver}` id `{room}-members-v{ver}` (phát lại khi không đổi, stream bỏ trùng). Vì là đường ghi duy nhất nên không đếm vào `counter_repaired_total`. Chi phí: nhóm 5K ≈ 1ms, channel 200K ≈ 20–60ms, ≤ 1 lần/s/room nóng; bucket theo `hash(user) % K` chỉ khi metric thời gian recount cho thấy cần, trước đó tăng delay cho room lớn (milestone Channel).""")

sub(DESIGN, "lọc insert của `messages`, `rooms`, `message_edits`, `reactions`, `pin_actions` và update/replace của `reactions` (không `updateLookup`; update giải mã `documentKey._id` + `updatedFields.n`, D91)", "lọc insert của `messages`, `rooms`, `message_edits`, `reactions`, `pin_actions`, `members`, update/replace của `reactions`, replace của `members` và update của `members` có `updatedFields.ver` (không `updateLookup`; update giải mã `documentKey._id` + `updatedFields.ver`, D91, D103)")
sub(DESIGN, "`ReactionChanged` thêm đuôi `len + user`, 38–102 byte; D80, D84, D91)", "`ReactionChanged` và `MemberChanged` thêm đuôi `len + user`, 38–102 byte; D80, D84, D91, D103)")
sub(DESIGN, "`x:{room}-{thread}-{seq}-{user}-n{n}`, `p:{room}-p{pv}`), cửa sổ `RECONCILE_WINDOW`", "`x:{room}-{thread}-{seq}-{user}-n{ver}`, `p:{room}-p{pin_ver}`, `g:{room}-mb-{user}-v{ver}`), cửa sổ `RECONCILE_WINDOW`")
sub(DESIGN, "Reader không chờ delay, không tra mark, không dựng event.", "Reader không chờ delay, không tra mark, không dựng event. Đọc, đánh dấu chưa đọc và clear history chỉ dùng toán tử thường, không đụng `members.ver`, nên không bao giờ vào feed (D105).")

line(DESIGN, "### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2, M2b.3]", "### 8.3 Effect engine [Đã xây phần M2b.1–M2b.4]")
sub(DESIGN, "; projection ghim 0 |", "; projection ghim 0; `member_counter` = `MEMBER_COUNT_DELAY` (1s, ≤ `RECONCILE_DELAY`); `owner_guard`, `member_event` = `RECONCILE_DELAY` |")
sub(DESIGN, "`read_updated` N giây |", "`member_counter`: một recount mỗi room mỗi lô; `read_updated`: lần đầu gửi ngay, phần đuôi gộp trong `READ_RECEIPT_WINDOW` (1s) mỗi (room, user), ngoài work stream (`readcast`, D105) |")
sub(DESIGN, "(`m:`, `r:`, `e:`, `x:`, `p:`; §8.2)", "(`m:`, `r:`, `e:`, `x:`, `p:`, `g:`; §8.2)")
sub(DESIGN, "`Activity{Seq: 0}`: chỉ nâng `lc`/`ab` (`$max`), không đổi `ls`/`lm`", "`Activity{Seq: 0}`: chỉ nâng `last_change_at`/`activity_bucket` (`$max`), không đổi `last_seq`/`last_message_at`")
sub(DESIGN, "`Activity{Seq: 0}` (mọi kind trừ `MessageInserted`, D91): chỉ nâng `lc`/`ab`", "`Activity{Seq: 0}` (mọi kind trừ `MessageInserted`, D91): chỉ nâng `last_change_at`/`activity_bucket`")
sub(DESIGN, "gom theo tin, witness = `max n` mỗi user;", "gom theo tin, witness = `max ver` mỗi user;")
sub(DESIGN, "`Get` doc `(k, u)`: `n == rec.n` → `reaction_changed` của doc hiện tại;", "`Get` doc `(message_key, user)`: `ver == rec.ver` → `reaction_changed` của doc hiện tại;")
after(DESIGN, "   | `PinInserted` | `pin_event` |", """   | `MemberChanged` | `room_activity` | 0 | — | `Activity{Seq: 0}`: chỉ nâng `last_change_at`/`activity_bucket` |
   | `MemberChanged` | `member_counter` | `MEMBER_COUNT_DELAY` | không | gom theo room, witness = `max ver` mỗi user; một recount (§7) mỗi room mỗi lô; CAS `member_count_ver`; `> 0` → `member_count_changed` của ver hiện tại; room không còn → drop có đếm; dữ liệu hỏng, witness cũ, tranh CAS → Nak |
   | `MemberChanged` | `owner_guard` | `RECONCILE_DELAY` | — | mỗi group một lần mỗi lô (DM bỏ qua): hoàn tất `pending_owner_change` còn lại, hoặc group không còn owner mà còn member → chọn kế nhiệm qua cùng CAS; sửa → `owner_repaired_total`; room không còn → drop có đếm |
   | `MemberChanged` | `member_event` | `RECONCILE_DELAY` | không | `MembersOf(room, [user])`: `ver == rec.ver` → bản room + bản user (`pbconv.MemberEvents`), record chỉ ack khi mọi bản có PubAck; `>` bỏ; `<` → Nak; doc/room không còn → drop có đếm |""")
sub(DESIGN, "Effect coalesce ghi `rooms.last_seq`, `last_msg_at`, `last_change_at` (mọi fact), `act_bucket = floor(ts/1h)`", "Effect coalesce ghi `rooms.last_seq`, `last_message_at`, `last_change_at` (mọi thay đổi), `activity_bucket = floor(ts/1h)`")
sub(DESIGN, "quét room `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`", "quét room `activity_bucket ≥ giờ(from)` hoặc `created_at ∈ [from, to]`")
sub(DESIGN, "mỗi room quét `message_edits` theo `{r, ts}` trong", "mỗi room quét `message_edits` theo `{room_id, created_at}` trong")
sub(DESIGN, "record `ReactionChanged` với `n` của doc)", "record `ReactionChanged` với `ver` của doc)")
sub(DESIGN, "cùng cách phân trang (`ErrReactionPageFull`, `ErrPinPageFull`).", "cùng cách phân trang (`ErrReactionPageFull`, `ErrPinPageFull`). M2b.4: cuối cùng quét doc `members` của room có `updated_at ∈ [from, to]` (tiền tố `room_id` của index đếm, không index thời gian; record `MemberChanged` với `ver` hiện tại của doc; `ErrMemberPageFull`); doc đổi nhiều lần trong khoảng chỉ cho record của `ver` cuối (lớp tập: event trung gian có thể mất); room tạo trong khoảng cho thêm record của doc member tạo cùng room.")
sub(DESIGN, "Fact sửa, reaction và ghim cũng chạy `room_activity` (chỉ `lc/ab`)", "Fact sửa, reaction, ghim và đổi member cũng chạy `room_activity` (chỉ `last_change_at/activity_bucket`)")
sub(DESIGN, "Nhưng resync chọn room theo `ab`,", "Nhưng resync chọn room theo `activity_bucket`,")
sub(DESIGN, "room **chỉ** có reaction/ghim trong khoảng đó (không có tin hay fact sửa nào sau đó nâng `ab`)", "room **chỉ** có reaction/ghim/đổi member trong khoảng đó (không có tin hay fact sửa nào sau đó nâng `activity_bucket`)")
sub(DESIGN, "Quét room theo `act_bucket` trong khoảng mất,", "Quét room theo `activity_bucket` trong khoảng mất,")
sub(DESIGN, "quét `message_edits`/`pin_actions` theo `{room, ts}`, chạy lại effect.", "quét `message_edits`/`pin_actions` theo `{room_id, created_at}`, `reactions` theo `{room_id, updated_at}`, `members` theo `updated_at`, chạy lại effect.")
```

`bin/m2b4_docs_design3.py` (§9–§17):

```python
from m2b4_docs_lib import DESIGN, after, line, sub

sub(DESIGN, "2. Ẩn: seq ≤ `cleared_before_seq`; seq trong `hidden`", "2. Ẩn: tin có `created_at ≤ cleared_before_time` của người đọc (D97); seq trong `hidden`")
sub(DESIGN, "bước 2 là `view.HideForViewer` (seq ≤ `Member.ClearedBeforeSeq` hoặc trong", "bước 2 là `view.HideForViewer` (`CreatedAt ≤ Member.ClearedBeforeTime` từ M2b.4, D97, trước đó seq ≤ `ClearedBeforeSeq`; hoặc trong")
line(DESIGN, "- **Phòng của tôi:** đọc `user_rooms` theo prefix `u` (D72).", "- **Phòng của tôi:** range trên index `members {tenant, user_id, state, room_id}` (D98; `user_rooms {u│r}` của D72 bỏ ở M2b.4, trở lại dạng projection khi shard); unread đếm từ `members.read_seq` (D105).")
sub(DESIGN, "client kết nối → lấy room từ `user_rooms` →", "client kết nối → lấy room từ index `members {tenant, user_id, state, room_id}` →")
sub(DESIGN, "`member_removed` → unsubscribe ngay; khoảng rò bằng độ trễ thu hồi.", "`member_removed` → unsubscribe ngay; khoảng rò bằng độ trễ thu hồi. Core phát bản user của event member (`recipient`, D104) và `read_updated` của nhóm lớn (D105) từ M2b.4.")
sub(DESIGN, "chống trùng 5m (phải dài hơn delay lớn nhất của effect).", "chống trùng 5m (phải dài hơn delay lớn nhất của effect); RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}` cho cả subject room và subject user (D104).")
sub(DESIGN, "type, actor, ts, oneof payload}`", "type, actor, ts, recipient, oneof payload}` (`recipient` khác rỗng → subject `evt.{t}.user.{recipient}.{type}`, D104)")
sub(DESIGN, "`deploy/prometheus/alerts.yml` (16 luật)", "`deploy/prometheus/alerts.yml` (17 luật)")
sub(DESIGN, "(reaction: số doc `e ≠ \"\"` theo emoji)", "(reaction: số doc `emoji ≠ \"\"` theo emoji; member: số doc `state = 1`, chỉ worker `member_counter` ghi, D102)")
after(DESIGN, "| CT1 |", """| OW1 | Group còn member active thì còn owner active ở mọi thời điểm (D100) | Chỉ đường owner (CAS `rooms.owners_ver` + `pending_owner_change`) đổi hay xoá owner, luôn thăng kế nhiệm trước khi hạ/xoá đích; worker `owner_guard` hoàn tất thay đổi dở hoặc chọn kế nhiệm; mỗi lần sửa nghĩa là có core chết giữa một thay đổi owner | `owner_repaired_total`, `effect_dropped_total{effect="owner_guard"}`; alert `ChatimOwnerRepaired` (`increase(…[15m]) > 0`) |
| MB1 | Mỗi lần đổi membership (doc `members` đổi `ver`) cuối cùng có bản room và bản user trên stream; người bị xoá mất quyền đọc/gửi ngay ở core chạy lệnh, ≤ 10s ở core khác | Feed lấy insert/replace và update có `ver`; worker `member_event` phát doc hiện tại khi `ver == rec.ver`; `Rooms.Member` lọc `state = 1`; cache actor theo thế hệ + TTL (D106) | `effect_dropped_total{effect="member_event"\\|"member_counter"}`, `reconcile_republished_total{effect="member_event"\\|"member_counter"}`, `work_failures_total`; alert `ChatimEffectDropping`, `ChatimWorkFailing`, `ChatimRepublishSurge` |""")
sub(DESIGN, "dedupe `chatim-redis-dedupe` cho `chatim:cid:*` + `chatim:evtack:*`", "dedupe `chatim-redis-dedupe` cho `chatim:cid:*`, `chatim:req:*` (`request_id` của `AddMembers`, D99) + `chatim:evtack:*`")
sub(DESIGN, "Khởi động: publisher → flusher", "Khởi động: publisher → read events (`readcast`) → flusher")
sub(DESIGN, "Dừng: `/readyz` false → drain → gRPC → reader", "Dừng: `/readyz` false → drain → gRPC → read events (1s, xả `read_updated` đang gộp vào publisher) → reader")
sub(DESIGN, "(26.2s/28s;", "(27.2s/28s;")
sub(DESIGN, "đếm lại sau `REACTION_COUNT_DELAY` |", "đếm lại sau `REACTION_COUNT_DELAY`; member: core chết giữa các bước của thay đổi owner → `owner_guard` hoàn tất (D100), `member_count` do `member_counter` đếm lại |")
after(DESIGN, "| Chi phí recount target nóng |", """| Recount `member_count` channel lớn | Trung bình | ~200K khoá phủ index (20–60ms) ≤ 1 lần/s/room nóng; tăng delay cho room lớn trước, bucket `hash(user) % K` khi metric cho thấy cần (milestone Channel, D102) |
| Read update vẫn vào oplog | Thấp | `MarkRead`/`MarkUnread` không vào feed nhưng vẫn là entry oplog; định cỡ oplog theo số thật (§2.3) |""")
sub(DESIGN, "(tinh chỉnh bởi D90: witness thay `afterClusterTime`)", "(tinh chỉnh bởi D90: witness thay `afterClusterTime`; `member_count` dùng chung `counter.Loop`, chỉ worker ghi, D102)")
sub(DESIGN, "`user_rooms {u│r}`; `hidden` thưa + `cleared_before_seq`, query theo lô", "`user_rooms {u│r}` (bỏ ở M2b.4, D98: index `members {tenant, user_id, state, room_id}`, projection lại khi shard); `hidden` thưa + `cleared_before_seq` (thay bởi `cleared_before_time`, D97), query theo lô")
sub(DESIGN, "| D85 | View: tin có `seq ≤ cleared_before_seq` hoặc", "| D85 | View: tin có `seq ≤ cleared_before_seq` (từ M2b.4: `created_at ≤ cleared_before_time`, D97) hoặc")
after(DESIGN, "| D95 |", """| D96 | Tên field đầy đủ, từ tiếng Anh cơ bản cho mọi collection trừ `messages` (bảng §5): counter chỉ tăng, được có lỗ, đuôi `_ver` (`ver`, `read_ver`, `member_count_ver`, `owners_ver`, `pin_ver`); audit `updated_at/updated_by`; fact `created_at/created_by`; `reconciler_state {resume_token, cluster_time}`; bỏ đường đọc vị trí feed cũ `_id: "messages"`. Không có đường nâng cấp tại chỗ: dev `make infra-reset`, prod go-live từ bản này | Giữ tên ngắn; đổi dần theo collection; `version`, `change_number` | Owner chốt 2026-10-07: tên đọc được khi vận hành và trong plan; `messages` giữ tên ngắn vì là collection lớn nhất; chưa có dữ liệu prod |
| D97 | Clear history theo thời gian: `members.cleared_before_time` = giờ server lúc gọi (`$max`, ms), ẩn tin `created_at ≤` mốc trên mọi timeline; chỉ member active; `ClearHistoryRequest.up_to_seq` reserved, response trả `cleared_before_time`; lệch vài ms quanh lúc bấm chấp nhận (sửa D72, D85 phần `cleared_before_seq`) | `cleared_before_seq` theo seq | Seq chỉ đúng cho một timeline; thread (M2c) cần một mốc chung cho mọi timeline; owner chốt 2026-10-07 |
| D98 | Member là lớp tập: `members` clustered `_id = keys.Member(room, user)`; `state` 1/2 luôn ghi rõ; `ver` (≤ MaxUint32) tăng mỗi lần đổi membership; `previous_role/previous_state`, `request_id`, `updated_*`; đọc/clear/mute không đụng `ver`; index `{room_id, state, role, joined_at, user_id}` (đếm, owner, kế nhiệm) và `{tenant, user_id, state, room_id}` (phòng của user); không quét khoảng `_id` (BinData so độ dài trước); bỏ `user_rooms` của D72 tới khi shard | Fact `member_actions` mv dày + projection settle-first (plan M2b.4 bản đầu, chưa thực thi); `user_rooms` song song | Owner chốt 2026-10-07: không đánh số dày ở chỗ mới (mv dày tuần tự hoá mọi lệnh member của một room, ~100–200 lệnh/s); mỗi doc một counter đủ cho id event và CAS; ít collection hơn |
| D99 | `AddMembers` mang `request_id` bắt buộc, dedupe `chatim:req:{room}:{user}:{request_id}` (RAM LRU + Redis dedupe, TTL `CID_COMMITTED_TTL`, cùng Lua và batcher với cid); một `BulkWrite(ordered:false)` upsert pipeline `$cond` (doc active giữ nguyên byte); kết quả suy từ doc sau ghi (`domain.AddedBy`: active, cùng `request_id` và người gọi); luôn role `member`; `read_seq = max(read_seq, seq cuối)`; giữ `cleared_before_time` | Trạng thái mong muốn không id; `FindOneAndUpdate` từng user | Retry trễ không được thêm lại người đã bị xoá giữa chừng; một bulk cho k user; dùng lại máy chống trùng có sẵn |
| D100 | Xoá/rời/đổi role không chạm owner = một update có điều kiện `ver`; lệnh chạm owner = CAS `rooms {owners_ver: k, pending_owner_change: null}` → thăng kế nhiệm trước → hạ/xoá đích (mỗi bước CAS theo `ver` ghi trong pending) → xoá pending khi `owners_ver == k+1`; ≤3 lượt rồi `ErrRetryLater`; owner cuối không tự hạ (`ErrLastOwner`); owner cuối rời → admin vào sớm nhất, không có thì member vào sớm nhất, hoà theo user id; effect `owner_guard` hoàn tất pending cũ hoặc chọn kế nhiệm | Transaction nhiều doc; cấm owner cuối rời; kiểm owner trong RAM | Bất biến OW1 giữ ở mọi thời điểm không cần transaction; đường thường không bao giờ ghi lên owner; owner chốt 2026-10-06 |
| D101 | Action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read`; `Request.Target`, `Request.Role`; `DefaultPolicy`: owner mọi việc, admin thêm người và xoá member thường, chỉ owner đổi role hay xoá admin/owner, ai cũng tự rời. DM → `FAILED_PRECONDITION`; `RemoveMember(self)` → `INVALID_ARGUMENT`; người chưa từng là member rời → `PERMISSION_DENIED`; tombstone rời → no-op; xoá/đổi role người không active → `NOT_FOUND` | Luật cứng trong `mutate`; một owner duy nhất | Owner chốt 2026-10-06, như D86: luật nằm ở `access.Policy`, module policy chat (Phase 2) thay được |
| D102 | `member_count` là aggregate (tinh chỉnh D67): chỉ worker `member_counter` ghi, recount có witness `(user, max ver)`, đếm phủ index `{room_id, state}`, CAS `member_count_ver`; event `member_count_changed`; `counter` tổng quát hoá thành `Loop[V]`; hội tụ ~1s | Lệnh tự `$inc`; gập số từ fact | Owner chốt 2026-10-07: chấp nhận trễ ~1s; một đường ghi duy nhất, đúng sau mọi đua |
| D103 | Feed thêm insert/replace của `members` và update có `updatedFields.ver`; kind `MemberChanged` (6) đuôi user, version = `ver`; record id `g:{room}-mb-{user}-v{ver}`; registry `room_activity`, `member_counter`, `owner_guard`, `member_event`; resync quét `members` của từng room lọc `updated_at`; core cũ `Nak` kind 6 (D91) nên nâng mọi core cùng lúc | Feed mọi update của `members`; record riêng cho đọc | Đọc/clear không đụng `ver` nên không vào work stream; một record cho mỗi lần đổi membership |
| D104 | Event member: bản room `{room}-mb-{user}-v{ver}` + bản user `…-u` (`Event.recipient = 10` → `evt.{t}.user.{u}.{type}`; RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}`); doc tạo cùng room chỉ có bản user; payload không có `new_owner` (event `member_role_changed` của người kế nhiệm mang thông tin đó); không ack mark; core cũ khởi động lại ghi đè luật RePublish → nâng mọi core cùng lúc | Chỉ bản room; cùng id cho hai bản; stream riêng cho user | Gateway cần bản user để sub/unsub room; JetStream chỉ một luật RePublish mỗi stream; cùng id thì stream bỏ bản sau |
| D105 | Vị trí đọc `read_seq/read_ver` chỉ toán tử thường (`MarkRead` `$lt` nâng, `MarkUnread` `$gt` hạ về seq−1, `$inc read_ver`), không đụng `ver` nên không vào feed; `read_updated` qua `readcast` (gửi ngay lần đầu, gộp đuôi trong `READ_RECEIPT_WINDOW` 1–2s, mặc định 1s); subject room khi DM hoặc `member_count ≤ READ_RECEIPT_MAX_MEMBERS` (20, trần cứng 50), không thì subject user; id `{room}-rd-{user}-v{read_ver}`; bước dừng `read events` 1s (26.2s → 27.2s) | `$max read_seq` + cờ chưa đọc riêng; phát cho room mọi cỡ; reconciler cho read | Owner chốt 2026-10-06/07: "đã xem" chỉ cho DM và nhóm nhỏ, nhóm lớn chỉ đồng bộ thiết bị của chính user; gộp giữ ≤ 2 event mỗi cửa sổ mỗi (room, user) |
| D106 | Cache member của actor: thế hệ (`Router.ForgetMembers`) + TTL 10s; TTL là giới hạn xuyên core (worker không xoá được cache core khác); không cache "không phải member" | Cache không hết hạn; đọc store mỗi lần gửi; pub/sub huỷ cache | Người bị xoá mất quyền gửi ngay trên core chạy lệnh, ≤10s ở nơi khác, không thêm read trên đường gửi |
| D107 | `MEMBER_BATCH_MAX` (2..1000, mặc định 500) cho `CreateRoom` và `AddMembers` (`ErrTooManyMembers`, `INVALID_ARGUMENT`); bỏ trần 5000 member trong `domain.NewRoom` | Trần cứng số member mỗi group | Owner chốt 2026-10-06: core không giới hạn số member; trần mỗi lệnh chặn lệnh và event quá lớn; 1 sẽ chặn mọi DM |
| D108 | Guarantee OW1 "group có member active thì có owner active": metric `owner_repaired_total`, luật `ChatimOwnerRepaired` (17 luật) | Không detector; dựa `effect_dropped_total` | D76: guarantee mới cần detector cùng lúc; mỗi lần sửa là dấu hiệu core chết giữa thay đổi owner |""")
```

`bin/m2b4_docs_roadmap_readme.py`:

```python
from m2b4_docs_lib import DATE, README, ROADMAP, after, before, line, sub

sub(ROADMAP, "> Cập nhật: 2026-10-06 (M2b.3 xong;", f"> Cập nhật: {DATE} (M2b.4 xong, M2b.0–M2b.4 chờ một PR merge `main`;")
line(ROADMAP, "- Mỗi plan có file tóm tắt đi kèm", """- Mỗi plan có file tóm tắt **kỹ thuật** đi kèm `docs/plans/<plan>-summary.md` (owner chốt 2026-10-06, sửa 2026-10-07): tiếng Việt, không có code, viết trước khi thực thi; owner đọc và phản biện, plan chỉ được thực thi khi owner đã duyệt bản tóm tắt. Nội dung bắt buộc: luồng chính (từng bước, ai đọc và ghi gì); field và collection (tên đầy đủ, kiểu, index); cơ chế (CAS, idempotent, effect, id event); quyết định mới (số D, phương án bị loại, lý do); chi phí (read, write, event mỗi lệnh ở nhóm 5K và channel 200K); rủi ro và giới hạn còn lại; cách kiểm (unit, itest, e2e). Plan đầy đủ dành cho AI thực thi.
- Tên field của collection (trừ `messages`) là từ tiếng Anh cơ bản, đầy đủ; counter chỉ tăng, được có lỗ, có đuôi `_ver`; audit là `updated_at/updated_by`; fact dùng `created_at/created_by`; không dùng `version`, `change_number` (D96). Không đánh số dày ở chỗ mới nếu lớp tập đủ (D98).""")
line(ROADMAP, "| 1 | M2b.4 — Member + vị trí đọc |", "| 1 | M2b.4 — Member + vị trí đọc | Đổi tên field mọi collection trừ `messages` (D96); clear history theo thời gian `cleared_before_time` (D97); member lớp tập `members {_id: room│user}` + `ver` mỗi doc (D98); `AddMembers` có `request_id` dedupe (D99); lệnh chạm owner qua CAS `rooms.owners_ver` + `pending_owner_change`, effect `owner_guard` (D100, OW1/D108); quyền owner/admin/member, DM cố định (D101); `member_count` recount chỉ worker, hội tụ ~1s (D102); feed + effect + resync member (D103); event member bản room + bản user qua `recipient` và RePublish `evt.*.*.*.*` (D104); vị trí đọc `read_seq/read_ver` + `read_updated` gộp qua `readcast` (D105); cache member của actor theo thế hệ + TTL (D106); `MEMBER_BATCH_MAX` (D107) | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-06-m2b4-members-read.md), [tóm tắt](plans/2026-10-06-m2b4-members-read-summary.md); 6 RPC, package `ownership`, `readcast`, `pkg/lru`, 3 effect mới, `owner_repaired_total` + `read_events_unbatched_total`, 17 luật alert, resync quét `members`, e2e phase 5 (D96–D108) |")
line(ROADMAP, "| 1 | M2c — Thread & tiện ích |", "| 1 | M2c — Thread & tiện ích | Thread (`thread_count` là counter), mention, reply/forward (cid), bookmark | ⏭ Tiếp theo — cần plan (sau khi merge `feat/m2b` vào `main`) |")
sub(ROADMAP, "`ListMyRooms` qua `user_rooms`;", "`ListMyRooms` qua index `members {tenant, user_id, state, room_id}` (D98; `user_rooms` trở lại dạng projection khi shard); unread tính từ `members.read_seq` (D105);")
sub(ROADMAP, "`member_removed` → unsubscribe;", "`member_removed` → unsubscribe (bản user của event member và `read_updated` có từ M2b.4, D104, D105);")
sub(ROADMAP, "mục mang sang từ M2b.0, M2b.1, M2b.2 và M2b.3 (xem dưới)", "mục mang sang từ M2b.0–M2b.4 (xem dưới)")
sub(ROADMAP, "bắt buộc trước khi channel go-live) |", "bắt buộc trước khi channel go-live); recount `member_count` ~200K khoá: delay dài hơn cho room lớn rồi bucket khi đo thấy cần (D102) |")
after(ROADMAP, "- (owner 2026-10-05) M2b.0 → M2b.4 làm trên một nhánh chung", "- M2b.4 xong (`dev-done`): mở một PR `feat/m2b` → `main` (M2b.0–M2b.4, merge commit, mức `dev-done`) khi owner đồng ý; nhánh của M2c tạo từ `main` sau khi merge.")
before(ROADMAP, "## Mức sẵn sàng", """Từ M2b.4, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-06-m2b4-members-read.md#kết-quả-thực-thi):

- Rolling deploy: tên field đổi và `members` đổi khoá nên không nâng cấp tại chỗ (dev `make infra-reset`, prod go-live từ bản này); core cũ ghi đè luật RePublish khi khởi động và `Nak` record kind 6; nâng mọi core cùng lúc (D96, D103, D104).
- Cache member của actor ở core khác core chạy lệnh chỉ hết theo TTL 10s (D106).
- Khoảng hở kiểm–ghi: lệnh xoá của admin đã được duyệt vẫn ghi dù admin vừa bị hạ (không transaction nhiều doc).
- `member_count` trễ ~1s; recount channel 200K ~20–60ms mỗi lần (D102, milestone Channel).
- `read_updated` best-effort, không đường bù; map `readcast` đầy thì gửi không gộp (`read_events_unbatched_total`); đọc vẫn là entry oplog.
- Resync: `ErrMemberPageFull` khi một thời điểm có ≥ 1000 lần đổi member của một room; doc đổi nhiều lần trong khoảng mất chỉ cho record của `ver` cuối; room chỉ có đổi member trong khoảng mất cần `-room`.
- Thêm 500 người sinh 500 event bản user.
- (các Minor controller ghi ở mục Kết quả thực thi)
""")
sub(ROADMAP, "M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11).", "M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11). M2b.0–M2b.4: `dev-done` trên `feat/m2b`, chờ một PR merge vào `main`.")

sub(README, " và M2b.3 (reaction một emoji mỗi user + số đếm recount, ghim theo fact `pin_actions` + projection) xong trên nhánh `feat/m2b`; tiếp theo là M2b.4 theo [roadmap](docs/roadmap.md)", ", M2b.3 (reaction một emoji mỗi user + số đếm recount, ghim theo fact `pin_actions` + projection) và M2b.4 (tên field đầy đủ; member lớp tập: thêm có `request_id`, xoá/rời/đổi role, nhóm luôn còn owner, `member_count` hội tụ ~1s; vị trí đọc + `read_updated`; clear history theo thời gian) xong trên nhánh `feat/m2b`, chờ một PR merge vào `main`; tiếp theo là M2c theo [roadmap](docs/roadmap.md)")
sub(README, "ghim là fact `pin_actions` + projection `rooms.pins`; reader đọc change stream (`messages`, `rooms`, `message_edits`, `reactions`, `pin_actions`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá/ghim, đếm lại reaction)", "ghim là fact `pin_actions` + projection `rooms.pins`; member là lớp tập `members` (owner/admin/member, nhóm luôn còn owner, event bản room + bản user), vị trí đọc `members.read_seq` + `read_updated` gộp; reader đọc change stream (`messages`, `rooms`, `message_edits`, `reactions`, `pin_actions`, `members`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá/ghim, đếm lại reaction và member, sửa owner)")
sub(README, "internal/{actor,mutate,counter,pinproj,view,access,", "internal/{actor,mutate,counter,pinproj,ownership,readcast,view,access,")
sub(README, "# dùng chung: keys, ids, slotmap,", "# dùng chung: keys, ids, slotmap, lru,")
sub(README, "phase 4 react seq 3 rồi đổi emoji, ghim seq 4)", "phase 4 react seq 3 rồi đổi emoji, ghim seq 4; phase 5 member: DM cố định, thêm/nâng admin/xoá, retry cùng request_id, chưa đọc rồi đã đọc, clear history, nhóm lớn đẩy read receipt sang subject user, owner cuối rời rồi vào lại)")
sub(README, "clear -room ID [-up-to N]", "clear -room ID", optional=True)
after(README, "    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev react ", "    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room ID -users a,b [-request-id R]   # thêm member (owner/admin); remove-member -room ID -target U, leave -room ID, set-role -room ID -target U -role owner|admin|member, read -room ID [-seq N], unread -room ID -seq N; watch -user U xem event riêng của user; -tenant/-user chọn người gọi")
sub(README, "Quét timeline chính rồi `message_edits`, `reactions` (doc hiện tại) và `pin_actions` của từng room (theo `{r, ts}`).", "Quét timeline chính rồi `message_edits` và `pin_actions` (theo `{room_id, created_at}`), `reactions` (doc hiện tại, theo `{room_id, updated_at}`) và doc `members` (theo `updated_at`, record của `ver` hiện tại) của từng room.")
sub(README, "room được chọn theo activity (`ab`) nên room chỉ có reaction/ghim trong khoảng mất phải chạy với `-room` (D91)", "room được chọn theo activity (`activity_bucket`) nên room chỉ có reaction/ghim/đổi member trong khoảng mất phải chạy với `-room` (D91, D103)")
sub(README, "Mặc định các mốc là 26.2s (gồm", "Mặc định các mốc là 27.2s (gồm bước `read events` 1s,", optional=True)
```

`bin/m2b4_docs_claude.py`:

```python
from m2b4_docs_lib import CLAUDE, after, before, line, sub

after(CLAUDE, "- M2b.3 (plan `docs/plans/2026-10-06-m2b3-reactions-pins.md`)", "- M2b.4 (plan `docs/plans/2026-10-06-m2b4-members-read.md`, summary `docs/plans/2026-10-06-m2b4-members-read-summary.md`): full English field names for every collection but `messages` (D96); time-based clear history `members.cleared_before_time` (D97); members as a set class, one clustered `members` doc per (room, user) with its own `ver` and a tombstone `state: 2` (D98); `AddMembers` with a deduplicated `request_id` (D99); owner-affecting commands through a CAS on `rooms.owners_ver` + `pending_owner_change`, successor first, and the `owner_guard` effect (D100, OW1/D108); owner/admin/member policy, DMs fixed (D101); `member_count` recounted only by the `member_counter` worker (D102); feed kind `MemberChanged` and member resync (D103); member events with a room copy and a user copy through the envelope `recipient` and RePublish `evt.*.*.*.*` (D104); read position `read_seq/read_ver` with plain operators and `read_updated` through `readcast` (D105); actor member cache by generation + 10s TTL (D106); `MEMBER_BATCH_MAX` (D107); corecli member commands and e2e phase 5.")
sub(CLAUDE, "room activity `ls/lm/lc/ab` with `$max`", "room activity `last_seq/last_message_at/last_change_at/activity_bucket` (short names until M2b.4) with `$max`")
sub(CLAUDE, "clear history (`members.cb`)", "clear history (`members.cb`; the time mark `members.cleared_before_time` since M2b.4, D97)")
sub(CLAUDE, "with decisions D61–D95", "with decisions D61–D108")
sub(CLAUDE, "M2b.3 (reactions + pins) is done on `feat/m2b`; next is M2b.4 (members + read position); its plan is not written yet.", "M2b.4 (members + read position) is done on `feat/m2b`; next is one PR that merges `feat/m2b` (M2b.0–M2b.4) into `main` once the owner agrees, then M2c (threads and extras), whose plan is not written yet.")
sub(CLAUDE, "then edit seq 1 and delete seq 2, then react to seq 3 and pin seq 4", "then edit seq 1 and delete seq 2, then react to seq 3 and pin seq 4, then the member phase (DM fixed, add/promote/remove, same request_id retry, unread/read, clear history, read receipts on the user subject of a big group, the last owner leaving and coming back)")
before(CLAUDE, "**Key encoding (`pkg/keys`).**", """**Field names (D96).** Every collection but `messages` uses full basic English field names (design §5 has one table per collection). Counters that only grow and may have gaps end in `_ver` (`ver`, `read_ver`, `member_count_ver`, `owners_ver`, `pin_ver`); audit fields are `updated_at/updated_by`; facts use `created_at/created_by`; never `version` or `change_number`. `messages` keeps its short names (`t f k x c ts v d ea rx`). There is no in-place upgrade from the short names: `make infra-reset` on dev.
""")
after(CLAUDE, "- pin_actions = `room│pv` (16B)", "- members = `room│user` (8B room + user bytes, `keys.Member`); never range-scan it by `_id`: Mongo orders BinData by length first, so a byte range is not a room prefix")
line(CLAUDE, "- `pv` is a room's dense pin version;", "- `pin_ver` is a room's dense pin version; `rx.v` versions a message's reaction summary and moves only when a count changes; a reaction's `ver` counts the changes of one (user, message) doc; a member's `ver` counts membership changes of one (room, user) doc, `read_ver` its read position changes; `rooms.member_count_ver` and `rooms.owners_ver` are CAS counters. All of them only grow and may have gaps; there is no dense member numbering.")
sub(CLAUDE, "`store.Pins`, `store.PinProjector` (`PinState`, `ApplyPins`) and `store.ChangeFeed`.", "`store.Pins`, `store.PinProjector` (`PinState`, `ApplyPins`), `store.MemberWriter` (`AddMembers`, `ApplyMember`), `store.MemberReader` (`MembersOf`, `Owners`, `Successor`, `MembersBetween`), `store.OwnerChanges` (`OwnerState`, `BeginOwnerChange`, `EndOwnerChange`), `store.MemberCounts` (`CountMembers`, `SetMemberCount`), `store.ReadPositions` (`MarkRead`, `MarkUnread`) and `store.ChangeFeed`.")
sub(CLAUDE, "- Dedupe, `chatim-redis-dedupe`: `chatim:cid:*` and the acked-event bitmaps", "- Dedupe, `chatim-redis-dedupe`: `chatim:cid:*`, `chatim:req:*` (`AddMembers` request ids, same Lua and TTLs, D99) and the acked-event bitmaps")
sub(CLAUDE, "D83, D84, D91).**", "D83, D84, D91, D103).**")
sub(CLAUDE, "`ReactionChanged`, `PinInserted`); the reader imports no driver", "`ReactionChanged`, `PinInserted`, `MemberChanged`); the reader imports no driver")
sub(CLAUDE, "inserts into `messages`, `rooms`, `message_edits`, `reactions` and `pin_actions`, and updates/replaces of `reactions` (an update decodes `documentKey._id` + `updatedFields.n`; no `updateLookup`)", "inserts into `messages`, `rooms`, `message_edits`, `reactions`, `pin_actions` and `members`, updates/replaces of `reactions`, replaces of `members` and updates of `members` that set `ver` (an update decodes `documentKey._id` + `updatedFields.ver`; no `updateLookup`; reads, unread and clear history never touch `ver`, so they never reach the feed)")
sub(CLAUDE, "plus a `len + user` tail for `ReactionChanged`;", "plus a `len + user` tail for `ReactionChanged` and `MemberChanged`;")
sub(CLAUDE, "`x:{room}-{thread}-{seq}-{user}-n{n}` or `p:{room}-p{pv}`;", "`x:{room}-{thread}-{seq}-{user}-n{ver}`, `p:{room}-p{pin_ver}` or `g:{room}-mb-{user}-v{ver}`;")
sub(CLAUDE, "`room_activity` (`Seq: 0`, only `lc/ab`)", "`room_activity` (`Seq: 0`, only `last_change_at/activity_bucket`)")
sub(CLAUDE, "publishes the current doc when its `n` equals the record's", "publishes the current doc when its `ver` equals the record's")
sub(CLAUDE, "then `pin_event` (delay `RECONCILE_DELAY`) for `PinInserted`.", "then `pin_event` (delay `RECONCILE_DELAY`) for `PinInserted`; `room_activity`, `member_counter` (delay `MEMBER_COUNT_DELAY`, one witness-checked recount per room per batch, the only writer of `member_count`), `owner_guard` (delay `RECONCILE_DELAY`, once per group per batch: finishes a pending owner change or promotes a successor; counts `owner_repaired_total`) then `member_event` (delay `RECONCILE_DELAY`, the room and user copies of the current doc when its `ver` equals the record's) for `MemberChanged`.")
sub(CLAUDE, "- `CreateRoom` publishes `room_created` on the fast path too;", "- `CreateRoom` publishes `room_created` and the user copies of `member_added` on the fast path too;")
sub(CLAUDE, "(scans rooms with `ab >= hour(from)` or `ca` in range, replays the main timeline backwards, then the room's `message_edits`, `reactions` (current docs) and `pin_actions` by `{r, ts}`, into the work stream at `-rate`)", "(scans rooms with `activity_bucket >= hour(from)` or `created_at` in range, replays the main timeline backwards, then the room's `message_edits` and `pin_actions` by `{room_id, created_at}`, `reactions` (current docs) by `{room_id, updated_at}` and `members` (current docs) by `updated_at`, into the work stream at `-rate`)")
sub(CLAUDE, "`{_id: \"changes\", at: $$CLUSTER_TIME}` only when no `at` exists", "`{_id: \"changes\", cluster_time: $$CLUSTER_TIME}` only when no `cluster_time` exists")
line(CLAUDE, "- Room activity (`rooms.ls/lm/lc/ab`, D69)", "- Room activity (`rooms.last_seq/last_message_at/last_change_at/activity_bucket`, D69) is written only by the `room_activity` worker effect; `activity_bucket` is the hour of the last change and is indexed (`{activity_bucket: 1}`, plus `{created_at: 1}` for the resync query).")
sub(CLAUDE, "- Hide upserts `hidden {u, r, th, s}`; clear raises `members.cb` with `$max` (`up_to_seq` 0 or past the end is clamped to the last seq).", "- Hide upserts `hidden {user_id, room_id, thread_root, seq}`; clear raises `members.cleared_before_time` to the server time with `$max` (active members only; `view` hides messages with `created_at` up to the mark on every timeline; `up_to_seq` is reserved; D97).")
sub(CLAUDE, "(`_id` = message key + user; fields `k r t u e pe n ts`; indexes `{k, e}`, `{r, ts}`; D88)", "(`_id` = message key + user; fields `message_key room_id tenant user_id emoji previous_emoji ver updated_at`; indexes `{message_key, emoji}`, `{room_id, updated_at}`; D88, D96)")
sub(CLAUDE, "`$cond` keeps `pe/e/n/ts` when", "`$cond` keeps `previous_emoji/emoji/ver/updated_at` when")
sub(CLAUDE, "otherwise `n+1` and `pe` = the old emoji.", "otherwise `ver+1` and `previous_emoji` = the old emoji.")
sub(CLAUDE, "none = `n` 1, same emoji = no-op, else `n+1` and Prev = the old emoji.", "none = `ver` 1, same emoji = no-op, else `ver+1` and Prev = the old emoji.")
sub(CLAUDE, "leaves the tombstone `e: \"\"`.", "leaves the tombstone `emoji: \"\"`.")
sub(CLAUDE, "`n < N` is `store.ErrStaleRead`), runs the covered aggregate on `{k, e}`", "`ver < N` is `store.ErrStaleRead`), runs the covered aggregate on `{message_key, emoji}`")
sub(CLAUDE, "`pinproj.Projector.Current` folds `rooms.pins/pv`", "`pinproj.Projector.Current` folds `rooms.pins/pin_ver`")
before(CLAUDE, "**Permission hook and reader pipeline (`access`, `view`).**", """**Members and read position (`mutate`, `ownership`, `readcast`, `effects`; D96–D108).**
- `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead` and `MarkUnread` run in `mutate`, called by `grpcsrv` (`grpcsrv/members.go`, `grpcsrv/read.go`), routed by the room's slot. Policy (D101): owners do everything; admins add people and remove plain members; only owners change roles or remove admins and owners; anyone leaves. A DM is `FAILED_PRECONDITION` (`ErrDirectRoom`); `RemoveMember(self)` is `INVALID_ARGUMENT`; the last owner cannot step down (`ErrLastOwner`).
- `members` doc (D98): clustered `_id` = room + user; `role`, `state` (1 active, 2 tombstone), `joined_at`, `ver`, `previous_role/previous_state`, `request_id`, `updated_at/updated_by`, `cleared_before_time`, `read_seq/read_ver`; indexes `{room_id, state, role, joined_at, user_id}` and `{tenant, user_id, state, room_id}`. `Rooms.Member` returns `ErrNotMember` unless `state` is 1, so a removed user loses read and send at once.
- `AddMembers` (D99, D107): ≤ `MEMBER_BATCH_MAX` (500) users and a required `request_id`, deduplicated on `chatim:req:{room}:{caller}:{request_id}` (`dedupe.Requests`: RAM LRU, then the dedupe Redis). Done → returns the docs still carrying that request id (`domain.AddedBy`), so a late retry never re-adds someone removed in between; busy → `UNAVAILABLE`. Writes one `BulkWrite(ordered:false)` of pipeline upserts on `_id` that leave an active doc byte-identical; a (re)join is always role `member`, raises `read_seq` to the last seq and keeps `cleared_before_time`.
- Remove, leave and role changes that do not touch an owner are one `updateOne({_id, ver: k})`; anything that touches the owner role goes through `ownership.Coordinator.Apply` (D100): CAS `rooms {owners_ver: k, pending_owner_change: null}`, promote the successor first (earliest admin, else earliest member, ties by user id), then demote or remove the target, each step conditional on the `ver` recorded in the pending change, then clear it; ≤3 rounds, then `ErrRetryLater`. The `owner_guard` effect finishes a pending change left by a dead core or promotes a successor in a group with no owner (`owner_repaired_total`, alert `ChatimOwnerRepaired`).
- `member_count` (D102) is written only by the `member_counter` effect (`counter.Loop` + `counter.MemberToucher`: witnesses, covered count, CAS `member_count_ver`), converging after ~`MEMBER_COUNT_DELAY` (1s); event `member_count_changed` `{room}-members-v{ver}`.
- Events (D104, no ack marks): `member_added`, `member_removed` (reason removed or left), `member_role_changed`; a room copy `{room}-mb-{user}-v{ver}` and a user copy `…-u` with the envelope `recipient` on `evt.{t}.user.{u}.{type}`; a doc created with the room has only the user copy. The stream RePublishes `evt.*.*.*.*` to `live.{1}.{2}.{3}.evt.{4}`; an old core restarting rewrites the rule, so upgrade every core together.
- Read position (D105): `MarkRead` raises `read_seq` with `$lt`, `MarkUnread` lowers it to `seq − 1` with `$gt`, each change `$inc read_ver`; plain operators only, never `ver`, so reads never reach the feed. `readcast.Caster` sends the first `read_updated` of a (room, user) at once and coalesces the rest per `READ_RECEIPT_WINDOW` (1s, 1..2s); the room subject for DMs and groups with `member_count ≤ READ_RECEIPT_MAX_MEMBERS` (20, hard cap 50), else the user's own subject; `read_events_unbatched_total` counts sends without coalescing. Best-effort.
- Actor member cache (D106): a generation bumped by `Router.ForgetMembers` after every member command plus a 10s TTL per entry.
- Wiring: `apps/core/service_wiring.go` (`wireService(serviceDeps)`) builds `dedupe.Requests`, `ownership.Coordinator` and `readcast.Caster` for `mutate`; `apps/core/member_effects_wiring.go` builds `member_counter`, `owner_guard` and `member_event`.
""")
sub(CLAUDE, "every other action for any member (D86);", "every other message action for any member (D86); member actions follow D101;")
sub(CLAUDE, "edit/delete/hide/clear/react/pin/unpin in `mutate`", "edit/delete/hide/clear/react/pin/unpin, member commands and read positions in `mutate`")
sub(CLAUDE, "(seq ≤ `ClearedBeforeSeq` or hidden by the reader", "(`CreatedAt` ≤ `ClearedBeforeTime` or hidden by the reader")
sub(CLAUDE, "`counter_repaired_total{counter}` counts summaries the workers rewrote.", "`counter_repaired_total{counter}` counts summaries the workers rewrote. Member effects add `member_counter` and `member_event` (both counters) and `owner_guard` (only `effect_dropped_total`); `member_counter` is the only writer of `member_count`, so it never counts into `counter_repaired_total`; `owner_repaired_total` counts owner repairs (OW1); `read_events_unbatched_total` counts read receipts sent without coalescing (diagnostic, no rule).")
sub(CLAUDE, "`deploy/prometheus/alerts.yml` (16 rules)", "`deploy/prometheus/alerts.yml` (17 rules)")
sub(CLAUDE, "Start order: publisher → flusher", "Start order: publisher → read events (`readcast`) → flusher")
sub(CLAUDE, "→ gRPC → reader (`RECONCILE_DRAIN + 1s`)", "→ gRPC → read events (1s, flushes coalesced `read_updated` into the publisher) → reader (`RECONCILE_DRAIN + 1s`)")
sub(CLAUDE, "the default plan is 26.2s of 28s", "the default plan is 27.2s of 28s")
sub(CLAUDE, "Small fact collections (`message_edits`, `pin_actions`) may have `{room, ts}` (D70).", "Small fact collections (`message_edits`, `pin_actions`) may have `{room_id, created_at}` (D70).")
line(CLAUDE, "- `reactions` is clustered with `_id` = message key + user", """- `reactions` is clustered with `_id` = message key + user, so one message's reactions are one range under the future shard key `{_id: 1}`; it has `{message_key, emoji}` and `{room_id, updated_at}` (D88, replaces D68).
- `members` is clustered with `_id` = room + user (D98); queries by room use the `{room_id, state, role, joined_at, user_id}` index, never an `_id` range; `{tenant, user_id, state, room_id}` serves the rooms of a user (scatter-gather once sharded, when `user_rooms` returns as a projection).""")
sub(CLAUDE, "(D1–D60 kept by id, new D61–D95)", "(D1–D60 kept by id, new D61–D108)")
line(CLAUDE, "- Every plan has a companion `docs/plans/<plan>-summary.md`", "- Every plan has a companion technical summary `docs/plans/<plan>-summary.md` (Vietnamese, no code, written before execution): main flows, collections and full field names, mechanisms (CAS, idempotency, effects, event ids), new decisions with rejected options, costs at a 5K group and a 200K channel, risks and limits, and how it is tested. The owner critiques and approves it before any task runs; the full plan is for AI implementers (rule in `docs/roadmap.md`).")
sub(CLAUDE, "M2b.3: `docs/plans/2026-10-06-m2b3-reactions-pins.md` (executed; results and known issues at its end).", "M2b.3: `docs/plans/2026-10-06-m2b3-reactions-pins.md` (executed; results and known issues at its end). M2b.4: `docs/plans/2026-10-06-m2b4-members-read.md` (executed; results and known issues at its end).")
```

**Step 4: Chạy script docs**

```bash
DAY=$(date +%Y-%m-%d)
for f in design design2 design3 roadmap_readme claude; do python3 bin/m2b4_docs_$f.py . "$DAY" || break; done
git diff --stat -- docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md CLAUDE.md
```

Expected: không script nào thoát lỗi (README có thể in `skip README.md: …` cho thao tác tuỳ chọn mà Task 1/15 đã làm); `git diff --stat` có đúng bốn file (thiết kế ~149+/61−, roadmap ~22+/9−, README ~9+/8−, CLAUDE.md ~47+/31−). Một script thoát lỗi `N matches of '…', want 1` nghĩa là file đã bị sửa khác HEAD `a560214` ở chỗ đó: **không sửa tay cho qua**; đọc đoạn đó (`grep -n`), nếu chỉ là một task trước đã viết đúng nội dung thì đánh dấu thao tác đó `optional=True` và chạy lại từ đầu sau `git checkout -- <file>` (chỉ file docs vừa bị script sửa dở, chưa có thay đổi nào khác của bạn), ghi vào báo cáo; nếu khác nghĩa thì dừng, báo controller.

Đọc lại nhanh diff của thiết kế (`git diff docs/designs/261005-chatim-architecture.md | less`): §5 có bảng tổng quan + bảng field cho `messages`, `rooms`, `members` và dòng field cho `message_edits`, `pin_actions`, `reactions`, `hidden`, `reconciler_state`; §6.4 có mục Member với 8 gạch con; §17.2 có D96–D108. Đối chiếu tên field §5 với codec thật (`grep -n 'bson:"' apps/core/internal/store/mongostore/*codec*.go apps/core/internal/store/mongostore/hidden.go apps/core/internal/store/mongostore/feed.go`): tên khác (vd. Part A đặt `pinned_by` khác) → sửa §5 theo code, ghi vào báo cáo.

**Step 5: INDEXES.csv**

Mỗi task trước sửa dòng của mình trong commit của nó; bước này kiểm, vá chỗ thiếu và sửa các dòng docs.

```bash
for p in apps/core apps/core/internal/ownership apps/core/internal/readcast pkg/lru apps/core/internal/domain apps/core/internal/store apps/core/internal/store/memstore apps/core/internal/store/mongostore apps/core/internal/store/storetest apps/core/internal/work apps/core/internal/reconcile apps/core/internal/pbconv apps/core/internal/publish apps/core/internal/actor apps/core/internal/access apps/core/internal/mutate apps/core/internal/grpcsrv apps/core/internal/effects apps/core/internal/resync apps/core/internal/config apps/core/internal/counter apps/core/internal/dedupe pkg/keys proto/chatim/v1/members.proto tools/internal/route tools/corecli tools/corecli/internal/e2e scripts/e2e.sh deploy/prometheus/alerts.yml; do printf '%s ' "$p"; grep -c "^$p," INDEXES.csv; done
grep -n "member_actions\|user_rooms\|memberproj\|MemberInserted\|settle-first" INDEXES.csv | cut -c1-90
grep -c "D108" INDEXES.csv
```

Expected: mỗi path đúng `1`; lệnh hai chỉ in dòng plan và tóm tắt M2b.4 (mô tả plan bản cũ, sửa ngay dưới) — dòng khác còn nhắc → sửa theo hợp đồng chung bằng helper, ghi vào báo cáo task nào đã quên; lệnh ba ≥ 1 (Task 16). Thiếu dòng nào → thêm theo hợp đồng chung bằng helper (`… - after '<dòng csv>'`), ghi vào báo cáo.

```bash
python3 bin/indexes_edit.py docs/designs/261005-chatim-architecture.md purpose = "Single source of truth: requirements (R17 revised) and assumptions needing real numbers; principles P1-P8; data-class framework for every plan; data model with one full-field-name table per collection (messages keeps short names, D96) and shard rules; built write path; fact + projection mutations (edit/delete M2b.2, pins M2b.3); set class for reactions (M2b.3) and members (M2b.4: one members doc per (room, user) with ver, request_id dedupe, owner changes through a CAS on rooms.owners_ver with the owner_guard effect); counter recount CAS-ver with witness (reactions, member_count); read position read_seq/read_ver with readcast; time-based clear history; effect engine (reader -> work stream -> workers); read path, room list, unread, sync token; gateway; guarantees with detectors (OW1, MB1); fixed reaction emoji list (D95); Decision Log D1-D60 kept, D61-D108 new"
python3 bin/indexes_edit.py docs/designs/261005-chatim-architecture.md decisions = "D1-D108"
python3 bin/indexes_edit.py docs/roadmap.md purpose = "Plan-writing rules (each plan has a technical owner summary <plan>-summary.md - flows, fields, mechanisms, decisions, costs, risks, tests - that the owner critiques before execution; full English field names, D96); milestones (M0-M2a.3 merged; M2b.0-M2b.4 dev-done on feat/m2b awaiting one merge to main; next threads and extras (M2c), read path, gateway, hardening); dependencies; readiness; M5 carry-over list"
python3 bin/indexes_edit.py docs/plans/2026-10-06-m2b4-members-read.md purpose = "M2b.4 implementation plan (reworked 2026-10-07): full field names for every collection but messages and time-based clear history (task 1); members as a set class (clustered members doc per (room, user) with ver and tombstone state 2); AddMembers with request_id dedupe (chatim:req); owner-affecting commands through a CAS on rooms.owners_ver + pending_owner_change, successor first, and the owner_guard effect; owner/admin/member policy, DMs fixed; member_count recounted only by the member_counter worker (counter.Loop); feed kind MemberChanged (inserts, replaces, updates that set ver); member events with room and user copies (envelope recipient, RePublish evt.*.*.*.*); read position read_seq/read_ver with plain operators and read_updated through readcast (room subject for DMs and small groups, user subject otherwise); actor member cache by generation + TTL; MEMBER_BATCH_MAX; resync of member docs; corecli + e2e phase 5; execution results and known issues at its end"
python3 bin/indexes_edit.py docs/plans/2026-10-06-m2b4-members-read.md decisions = "D96;D97;D98;D99;D100;D101;D102;D103;D104;D105;D106;D107;D108"
python3 bin/indexes_edit.py docs/plans/2026-10-06-m2b4-members-read-summary.md purpose = "Technical owner summary of M2b.4 (Vietnamese, no code): flows, collections and full field names, mechanisms, decisions D96-D108, costs at a 5K group and a 200K channel, risks and tests; the owner critiqued and approved it before execution"
python3 bin/indexes_edit.py CLAUDE.md purpose = "Guidance for Claude Code: hard rules; verification budget; architecture (field names, key encoding, send path, members and read position, effect engine, detectors, lifecycle, shard rules); docs map"
grep -n "^README.md," INDEXES.csv | cut -c1-200
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
```

Expected: `ok` từng lệnh; dòng `README.md` đã nhắc `MEMBER_BATCH_MAX`, `MEMBER_COUNT_DELAY`, `READ_RECEIPT_WINDOW`, `READ_RECEIPT_MAX_MEMBERS` (Task 15; thiếu → `python3 bin/indexes_edit.py README.md purpose "~" "REACTION_COUNT_DELAY)" "REACTION_COUNT_DELAY, MEMBER_BATCH_MAX, MEMBER_COUNT_DELAY, READ_RECEIPT_WINDOW, READ_RECEIPT_MAX_MEMBERS)"`, ghi vào báo cáo); `{7}`.

**Step 6: Mục "Kết quả thực thi" ở cuối plan**

Viết nội dung vào `bin/m2b4_results.md` (gitignored) theo dàn ý dưới, điền từ nhật ký thực thi của controller (sha, lệch, Minor theo task); phần "Kiểm chứng cuối" để trống, Step 17 điền. Dàn ý cố ý **không** chứa dòng heading, để `grep '^## Kết quả thực thi'` chỉ thấy bản thật (bài học M2b.3: khung mẫu nằm trong code fence, bản thật phải thêm ở cuối file).

```markdown
Commit từng task (nhánh `feat/m2b`):

- Chuẩn bị: `<sha>` plan v2 + tóm tắt kỹ thuật (owner duyệt <ngày>); thay plan `a560214` (chưa thực thi).
- T1 `<sha>` đổi tên field + clear theo thời gian (`make infra-reset` sau commit); T2 `<sha>` domain + keys; T3 `<sha>` proto + pbconv + publish; T4 `<sha>` port store + memstore + `storetest`; T5 `<sha>` mongostore (`make infra-reset`); T6 `<sha>` feed + work; T7 `<sha>` `counter.Loop` (push `<sha>..<sha>`).
- T8 `<sha>` `pkg/lru` + `dedupe.Requests`; T9 `<sha>` actor; T10 `<sha>` access; T11 `<sha>` `ownership`; T12 `<sha>` `mutate` member; T13 `<sha>` `CreateRoom`; T14 `<sha>` `readcast` + đọc; T15 `<sha>` config + grpcsrv + vòng đời (push `<sha>..<sha>`).
- T16 `<sha>` effect; T17 `<sha>` (tách test), `<sha>` resync; T18 `<sha>` route, `<sha>` corecli/e2e; T19 `<sha>` itest; T20 `<sha>` docs (và `<sha>` tách file nếu có).

Quyết định của owner trong lúc làm:

- (theo nhật ký; không có thì "Không có ngoài các quyết định 2026-10-06 và 2026-10-07 ở đầu plan.")

Lệch so với plan:

- T18: `remove-member`/`set-role` dùng `-target` cho user đích (đã ghi trong hợp đồng chung).
- (sửa cơ học để biên dịch; idiom gofmt/vet/lint; tên khác hợp đồng; …)

Lỗi đã biết, đã sửa:

- (theo nhật ký)

Lỗi Minor còn mở:

1. (theo task, do controller/reviewer ghi)
2. File gần 200 dòng (lần sửa sau phải tách): (liệt kê `wc -l` ≥ 180 của các file đã đụng, theo Step 1)

Kiểm chứng trong lúc làm: (itest xanh ở task nào; `make e2e` PASS ở T18; `make alerts-check` 17 luật ở T16)

Kiểm chứng cuối (Task 20, <YYYY-MM-DD>, trên `<sha>`):
```

```bash
python3 - <<'EOF'
p = "docs/plans/2026-10-06-m2b4-members-read.md"
heading = "## " + "Kết quả thực thi"
body = open("bin/m2b4_results.md", encoding="utf-8").read().strip("\n")
text = open(p, encoding="utf-8").read().rstrip("\n")
open(p, "w", encoding="utf-8").write(text + "\n\n---\n\n" + heading + "\n\n" + body + "\n")
EOF
```

Cập nhật luôn khối "Từ M2b.4" của roadmap bằng các Minor đáng mang sang M5, thay dòng `(các Minor controller ghi ở mục Kết quả thực thi)`.

**Step 7: Kiểm**

```bash
grep -c '^## Kết quả thực thi' docs/plans/2026-10-06-m2b4-members-read.md
grep -n '^## Kết quả thực thi\|^### Task 20' docs/plans/2026-10-06-m2b4-members-read.md
grep -c "^| D\(9[6-9]\|10[0-8]\) |" docs/designs/261005-chatim-architecture.md
grep -n "26.2s\|16 rules\|(16 luật)\|D61–D95\|next is M2b.4\|tiếp theo là M2b.4\|ls/lm/lc/ab\|{k, e}\|{r, ts}\|member_actions\|memberproj" CLAUDE.md README.md docs/designs/261005-chatim-architecture.md docs/roadmap.md | cut -c1-90
grep -n "^## 5\.\|^\`members\` (D98)\|^\`rooms\`:\|^\`messages\` (tên ngắn)" docs/designs/261005-chatim-architecture.md
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
```

Expected: `1`; dòng `## Kết quả thực thi` nằm **sau** dòng `### Task 20`; `13`; lệnh bốn chỉ in bốn dòng lịch sử được phép: roadmap khối "Từ M2b.3" (`ErrReactionPageFull` … `{r, ts}`), thiết kế D88 và D90 (tên ngắn của M2b.3, giữ làm lịch sử), D98 (`member_actions` là phương án bị loại), D105 (`26.2s → 27.2s`) — dòng nào khác → sửa (thêm thao tác vào script, chạy lại cho đúng file đó), ghi vào báo cáo; lệnh năm in bốn dòng (heading §5 và ba dòng mở bảng field `messages`, `rooms`, `members`); `{7}`.

**Step 8: Commit docs**

```bash
git commit -m "docs: record the M2b.4 member set class, full field names and decisions D96-D108" -- docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md CLAUDE.md INDEXES.csv docs/plans/2026-10-06-m2b4-members-read.md
git show --stat HEAD
```

Expected: đúng 6 file. Chưa push.

**Step 9:** `make fmt-check && make vet && make lint && make vuln`
Expected: sạch; vuln: `Your code is affected by 0 vulnerabilities` (có thể kèm cảnh báo cấp module GO-2026-5932, có từ trước).

**Step 10:** `make test`
Expected: mọi package `ok`, gồm `apps/core/internal/{ownership,readcast,counter,dedupe,actor,access,mutate,effects,work,resync,pbconv,publish,grpcsrv,config,view,store/...}`, `pkg/keys`, `pkg/lru`, `tools/internal/route`, `tools/corecli/internal/e2e`.

**Step 11: Stack sạch (dữ liệu dev theo tên field mới, luật RePublish mới)**

```bash
make core-down
make infra-reset
make infra-up
```

Expected: `infra-reset` xoá volume (không còn doc tên ngắn, `members` cũ không clustered, stream có luật RePublish cũ); `infra-up` chờ được primary Mongo.

**Step 12: Bootstrap thật**

```bash
make core-up
docker logs chatim-core-1 2>&1 | grep -c '"level":"ERROR"'
```

Expected: `core-up` healthy cả hai core (bootstrap tạo `members` clustered + hai index mới, `rooms` index `activity_bucket`, `created_at`; `EnsureStream` đặt RePublish `evt.*.*.*.*`); `0` dòng ERROR.

**Step 13:** `make itest`
Expected: mọi package `ok`, gồm contract `storetest` Mongo (`RunMembers`, `RunMemberFeed`), `TestBootstrapIsIdempotent`, itest RePublish (Task 3), feed-skip (Task 6), drill resync (Task 17), 11 itest của Task 19, các itest M2b.2/M2b.3. Không cần chạy lại nếu sau lần xanh cuối (Task 19) chỉ đổi docs và Step 1 không tách file code — ghi "xanh ở T19, không chạy lại" vào kết quả.

**Step 14:** `make e2e`
Expected: dòng cuối là PASS của Task 18 (`e2e PASS: 40 messages before and 40 after killing core-1, …, the last owner leaving to the admin and re-added, on replies and live room and user subjects`), có dòng `members ok: … 25 live events`, phase 5 check `history ok: 81 messages …`.

**Step 15: `/metrics` trên cả hai core**

```bash
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_republished_total|effect_dropped_total|owner_repaired_total|read_events_unbatched_total|counter_repaired_total|work_processed_total|work_failures_total)[ {]'; done
```

Expected:
- đúng một core có `chatim_core_reconcile_running 1`;
- cả hai core có `effect_dropped_total{effect=…}` cho `member_counter`, `owner_guard`, `member_event` (cùng các nhãn cũ), tất cả `0`; `reconcile_republished_total` cho `member_counter`, `member_event` (không có nhãn `owner_guard`); `chatim_core_owner_repaired_total 0`; `chatim_core_read_events_unbatched_total 0`; `chatim_core_work_failures_total 0`;
- tổng hai core của `reconcile_republished_total{effect="member_counter"}` ≥ 3 (`member_count_changed` v1–v3 của phase 5 chỉ do worker phát; thêm cho DM/room phụ nếu count đổi) và của `{effect="member_event"}` = 0 (fast path đã phát mọi bản; bản worker là bản trùng). Khác thì ghi lại, không phải lỗi; `counter_repaired_total{counter="reactions"}` như M2b.3.

**Step 16: Luật, resync dry-run, corebench**

```bash
make alerts-check
FROM=$(date -u -v-15M +%Y-%m-%dT%H:%M:%SZ); TO=$(date -u -v+1M +%Y-%m-%dT%H:%M:%SZ)
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -dry-run
pmset -g therm | grep CPU_Speed_Limit
make poc TOOL=corebench ARGS="-rate 1000 -duration 30s -watch 20"
```

Expected: `SUCCESS: 17 rules found`; một dòng `resync rooms=… room_records=R message_records=… edit_records=E reaction_records=X pin_records=P member_records=M dry_run=true` với `E ≥ 2`, `X ≥ 1`, `P ≥ 1`, `R ≥ 2` (room e2e và DM của phase 5, cộng room thừa mà `e2e setup` tạo khi tìm slot), `M ≥ 25` (doc hiện tại của `e2e-user`, `e2e-bob`, `e2e-carol`, 20 người `e2e-m*` và 2 doc của DM, cộng creator của mỗi room thừa và doc của lệnh tay Task 18 nếu có), exit 0 (`date -v` là cú pháp macOS; Linux: `date -u -d '-15 min' +%FT%TZ`); corebench: `sends due=… sent=… acked=… failed=0 …` và `live events on 20 watched rooms: … missing=0 duplicates=0 …` (ghi p99 ack, không so sánh; `CPU_Speed_Limit` < 100 thì ghi kèm).

**Step 17: Kết quả thực thi + push**

Cuối mục "Kết quả thực thi" của plan (Step 6 đã tạo ở cuối file), dưới dòng `Kiểm chứng cuối (Task 20, …)`, điền:

```markdown
- Tách file trước kiểm (Step 1): <không có / sha + file>.
- `make fmt-check`, `make vet`, `make lint` sạch; `make vuln`: <kết quả>.
- `make test`: mọi package `ok` (<n> package).
- `make infra-reset` → `core-up` sạch (bootstrap `members` clustered, RePublish `evt.*.*.*.*`).
- `make itest`: <xanh ở T19, không chạy lại / chạy lại, n package ok>.
- `make e2e`: `e2e PASS: …` (phase 5: <số lần thử mỗi lệnh, core xử lý>; `members ok: … 25 live events`).
- `/metrics` hai core: <reconcile_running; effect_dropped_total và reconcile_republished_total của member_counter, owner_guard, member_event; owner_repaired_total; read_events_unbatched_total; work_failures_total; work_processed_total>.
- `make alerts-check`: `SUCCESS: 17 rules found`.
- resync dry-run: `<dòng in ra>`.
- corebench 1000/s 30s: failed=<n>, missing=<n>, duplicates=<n>, p99 ack <ms>, CPU_Speed_Limit <giá trị>.
- Mức sẵn sàng: `dev-done` trên `feat/m2b`; M2b.0–M2b.4 chờ một PR merge `main` (owner duyệt).
```

Nếu Step 15 thấy `member_event` republished khác 0 hay `owner_repaired_total` khác 0, hoặc Step 9–16 có lệch, thêm vào "Lỗi Minor còn mở" và (nếu đáng mang sang) khối "Từ M2b.4" của roadmap.

```bash
git commit -m "docs: record M2b.4 execution results" -- docs/plans/2026-10-06-m2b4-members-read.md docs/roadmap.md
git push origin feat/m2b
git status --short
git log origin/feat/m2b -1 --oneline
```

Expected: push thành công; `git status` sạch; `git log origin/feat/m2b -1` là commit này.

**Step 18: Checklist merge `feat/m2b` → `main` và nháp PR (không mở PR)**

Theo `docs/git-workflow.md` (Definition of Done) và roadmap ("merge vào `main` một lần khi M2b.4 đạt Definition of Done"). Chỉ đọc và soạn; không `gh pr create`, không merge.

```bash
git fetch origin
git log --oneline origin/feat/m2b..origin/main | wc -l
git merge-tree --write-tree origin/main origin/feat/m2b > /dev/null; echo "merge-tree exit=$?"
git log --oneline origin/main..origin/feat/m2b | wc -l
git diff --stat origin/main...origin/feat/m2b | tail -1
grep -n '^## Kết quả thực thi' docs/plans/2026-10-05-m2b0-mechanism-foundations.md docs/plans/2026-10-05-m2b1-effect-engine.md docs/plans/2026-10-05-m2b2-edit-delete.md docs/plans/2026-10-06-m2b3-reactions-pins.md docs/plans/2026-10-06-m2b4-members-read.md
grep -n "Critical\|Important" docs/plans/2026-10-06-m2b4-members-read.md | tail -5
```

Expected: lệnh hai `0` (khác 0 → ghi số, controller quyết merge `main` vào `feat/m2b` trước hay không); `merge-tree exit=0`; số commit và thống kê diff ghi vào nháp PR; năm plan đều có mục kết quả (M2b.0 dùng heading có ngày, vẫn khớp đầu dòng); không còn finding Critical/Important mở (chỉ trong mục "đã sửa").

Thêm checklist vào cuối mục "Kết quả thực thi" của plan M2b.4 (sau khối kiểm chứng cuối), commit `docs: add the feat/m2b merge checklist` rồi push:

```markdown
Chuẩn bị merge `feat/m2b` → `main` (Definition of Done, `docs/git-workflow.md`):

- [x] 1. Mọi task của plan M2b.0, M2b.1, M2b.2, M2b.3, M2b.4 đã xong; bước lệch "Expected" đã báo và xử lý (mục Kết quả thực thi của từng plan).
- [x] 2. Xanh trên `<sha>`: `make fmt-check`, `vet`, `lint`, `test`, `itest`, `core-up && e2e` (Task 20); CI job `checks` sẽ chạy lại trên PR.
- [x] 3. Không còn finding Critical/Important; Minor ghi trong plan và khối "Từ M2b.x" của roadmap.
- [x] 4. Cùng nhánh đã cập nhật `docs/roadmap.md` (trạng thái, mức sẵn sàng, luật tóm tắt kỹ thuật), Decision Log D61–D108, `INDEXES.csv`, mục Done/Next của `CLAUDE.md`.
- [ ] 5. Mô tả PR ghi mức sẵn sàng và những gì còn thiếu để go-live (nháp ở `bin/m2b-pr.md`, chưa mở PR: chờ owner).
- Nhánh: <n> commit trên `main`, `merge-tree` không xung đột, `main` <không có / có n> commit mới; merge bằng merge commit (`gh pr merge --merge`), không squash, xoá nhánh sau khi merge.
```

```bash
git commit -m "docs: add the feat/m2b merge checklist" -- docs/plans/2026-10-06-m2b4-members-read.md
git push origin feat/m2b
```

Soạn nháp PR ở `bin/m2b-pr.md` (`bin/` gitignored, không commit):

```markdown
feat: M2b mechanisms, effect engine, edits, reactions, pins, members and read position (M2b.0–M2b.4)

## Tóm tắt

- M2b.0: ack mark chỉ cho `msg_created`, actor tự rút khi tranh seq, permission hook + reader pipeline, detector `/metrics` + luật alert (D65, D76–D78).
- M2b.1: reader trên slot 0 → work stream `CHATIM_WORK` → worker mọi core, `room_created`, room activity, `/app resync` (D79–D81).
- M2b.2: sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, `GetEditHistory`, quyền qua `access.Policy` (D82–D87).
- M2b.3: reaction một emoji mỗi user + số đếm recount CAS, danh sách emoji cố định, ghim theo fact `pin_actions` (D88–D95).
- M2b.4: tên field đầy đủ, clear history theo thời gian, member lớp tập (`members` một doc mỗi (room, user), `request_id`, owner luôn còn qua CAS `owners_ver` + `owner_guard`), `member_count` recount chỉ worker, event member bản room + bản user, vị trí đọc + `read_updated` gộp (D96–D108).

Plan và kết quả từng phần: `docs/plans/2026-10-05-m2b0-mechanism-foundations.md`, `…-m2b1-effect-engine.md`, `…-m2b2-edit-delete.md`, `docs/plans/2026-10-06-m2b3-reactions-pins.md`, `…-m2b4-members-read.md` (mục "Kết quả thực thi" ở cuối mỗi file). Thiết kế: `docs/designs/261005-chatim-architecture.md`.

## Mức sẵn sàng

`dev-done`: `make fmt-check vet lint test itest`, `make core-up && make e2e` xanh trên máy dev (<sha>, <ngày>); 17 luật alert; corebench 1000/s không lỗi.

## Còn thiếu để go-live

- PoC prod-like (P1–P10, `docs/poc/README.md`) và số thật thay giả định thiết kế §2.3.
- Oplog `minRetentionHours` ≥ 24h, alert RC5 và lag trên hạ tầng thật, đo bộ nhớ NATS cho map chống trùng.
- M3 (đường đọc), M4 (gateway), M5 (hardening, gồm danh sách "Mục mang sang M5" của roadmap từ M2b.0–M2b.4).
- Không nâng cấp tại chỗ: tên field đổi (D96) và `members` đổi khoá (D98); prod go-live từ bản này; nâng mọi core cùng lúc (D91, D103, D104).

## Kiểm

- [ ] CI `checks` xanh trên PR.
```

Controller dùng nháp này sau khi owner đồng ý: `gh pr create --base main --head feat/m2b --title "<dòng đầu>" --body-file <file body không có dòng tiêu đề>`. Không chạy lệnh này trong task.

Expected: checklist có trong plan và đã push; `bin/m2b-pr.md` tồn tại, không nằm trong `git status`; báo controller: số commit, kết quả `merge-tree`, mục còn mở (chỉ DoD 5: mở PR khi owner duyệt).

Nếu sau đó owner yêu cầu sửa (như M2b.3), thêm mục con `### Sửa theo owner (<ngày>)` ở cuối mục "Kết quả thực thi", ghi từng sửa với sha, lý do và quyết định thiết kế đã đổi; các task ở trên giữ nguyên làm lịch sử.
