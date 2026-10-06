# chatim — Kiến trúc hệ thống

> Ngày: 2026-10-05 · Trạng thái: **nguồn sự thật duy nhất** cho thiết kế; thay [thiết kế Phase 1](../archive/designs/260930-chat-core-gateway-design.md) và [rà soát cơ chế](../archive/designs/261004-system-mechanisms.md) (đã archive).
> Nguồn quyết định: [báo cáo nghiên cứu cơ chế hệ thống](../research/261005-system-mechanisms-synthesis-report.md) · [tài liệu lưu trữ các vòng phản biện](../archive/research/261004-system-mechanisms-synthesis.md) · [nghiên cứu mã nguồn mở](../research/260930-opensource-chat-architecture-research.md).
> Nhãn: **[Đã xây]** là code đang chạy trên `main` hoặc nhánh M2a.2/M2a.3; **[Chưa xây]** là thiết kế đã chốt, chưa code. Milestone ở [roadmap](../roadmap.md).

## 1. Phạm vi

chatim là hạ tầng chat dùng chung (CPaaS nội bộ), multi-tenant về mặt logic: mỗi sản phẩm là một tenant, dữ liệu cô lập theo tenant id, mọi tenant dùng chung một bộ triển khai.

**Phase 1**
- `core`: room, member, message, tương tác, cấp seq, lưu trữ, phát event.
- `gateway`: giữ kết nối WebSocket, đẩy event realtime.
- Room: DM, group ≤5K member, channel ~200K subscriber (chỉ admin post).
- Tin: gửi, sửa (có lịch sử), xoá cho mọi người / phía tôi, trả lời, chuyển tiếp.
- Tương tác: reaction, vị trí đọc + unread, typing, presence, thread, mention, ghim, bookmark, đánh dấu chưa đọc.
- Event stream nội bộ cho các app khác.

**Không làm ở Phase 1:** app auth / api / events / push / migrator, API import trong core, media, search, E2EE, webhook, voice/video, admin UI.

**Quy mô:** ~100K kết nối đồng thời, 5–10K tin/s đỉnh, 5–20 tỷ tin sau 2 năm.

```
 Client SDK ──WebSocket──► gateway ──gRPC (theo slot)──► core ──insertMany w:majority──► MongoDB rs (3)
                              ▲                            │                                  │
                              │                            ├─publish──► JetStream CHATIM_EVT ──RePublish──► live.*
                              └──── subscribe live.* ◄─────┘                                  │
                                                           └◄── reader (slot 0) ◄── change stream
                                                                 └─► work stream (theo slot) ─► worker effect ở mọi core
```

## 2. Yêu cầu

### 2.1 Phi chức năng

| # | Yêu cầu |
|---|---|
| A1 | p99: ack gửi tin ≤30ms; tới người nhận online ≤100ms; trang 50 tin ở bất kỳ vị trí ≤20ms |
| A2 | Đã ack = đã ghi `w:majority`. Thứ tự trong timeline giống nhau cho mọi người. Event best-effort, id tự nhiên để người nhận bỏ trùng; reconcile bù phần mất |
| A3 | 99.95%, không điểm chết đơn lẻ: core ≥2, gateway 2–3, Mongo rs 3 member (chưa shard), NATS 3, Redis Sentinel |
| A4 | Tin và lịch sử sửa giữ mãi; `CHATIM_EVT` giữ 7 ngày |
| A5 | JWT do app auth cấp; mTLS nội bộ; không E2EE |
| A6 | Text ≤16KB, meta ≤8KB, ≤50 mention, ≤10 tham chiếu đính kèm |
| A7 | Member đọc được lịch sử sửa của room; tenant có thể tắt |

### 2.2 Yêu cầu nghiệp vụ đã chốt với owner

| # | Yêu cầu |
|---|---|
| R5 | Thứ tự tin chỉ cần **giống nhau cho mọi người**; không cần nhân quả hay thứ tự commit |
| R6/R7 | Event tạo tin phát trước event thay đổi tin đó (ở đường bình thường); thứ tự tới người nhận không kiểm soát, nên event thay đổi mang snapshot |
| R10/R15 | Client không phát hiện mất event bằng cách đếm; connect/reconnect lấy trạng thái mới nhất; doc tin luôn giữ trạng thái hiện tại |
| R12 | Event best-effort; reconcile bù phần mất |
| R17 | (sửa 2026-10-05, D73) Unread **exact nếu quét xong trong giới hạn S**; không thì trả cận dưới + cờ `approx` rồi đẩy bản đúng sau. Chỉ đếm tin của người khác, chưa xoá, chưa ẩn với tôi, kind có cờ đếm; chặn hiển thị ở 99+. Chỉ áp cho room list; badge push xấp xỉ, do app push giữ |
| R23 | Tin hệ thống (cuộc gọi, vào/ra, ghim) nằm chung với tin thường, có `kind` và cờ "đếm unread" |
| R26 | Cấp seq có kiểm unique (D13) giữ lại vì đang chạy tốt, không phải vì là yêu cầu |
| R-xoá | (D75) Xoá cho mọi người = xoá ở kho chính + huỷ hiển thị. Oplog, stream 7 ngày, backup là log vận hành tự hết hạn. Tenant cần xoá chặt bật chế độ event không mang text (client tự fetch) |

### 2.3 Giả định cần số thật

Chưa có số thật; mọi ước lượng dưới đây dùng các giả định này. **Trước khi viết plan M3, chạy PoC prod-like hoặc định cỡ work stream/oplog: hỏi owner số thật.**

| Giả định | Giá trị | Dùng ở |
|---|---|---|
| Số room mỗi user | 50 (10 room có tin mới lúc reconnect) | §9 tải reconnect storm |
| Tỉ lệ tin bị sửa/xoá | 1–3% | §6 ngân sách lệnh sửa, cỡ `message_edits` |
| Số room active cùng lúc | 3–5K | §8 room activity |
| Số member doc | ~500M (10M user × 50 room) | §9 `user_rooms` |

## 3. Nguyên tắc

| # | Nguyên tắc |
|---|---|
| P1 | Core nào cũng xử lý đúng mọi room. Quyền sở hữu slot chỉ để gom batch, giữ thứ tự, dùng cache. Đúng đắn đến từ khoá unique của DB làm CAS |
| P2 | DB là nguồn sự thật. Mỗi command có **một fact chính** làm điểm tuần tự hoá, ghi bằng một lệnh atomic trên một doc; mọi thứ khác là effect idempotent. Không transaction nhiều doc. **Cấm join không giới hạn**; tra cứu điểm có giới hạn (≤ vài trăm khoá) được phép |
| P3 | Mỗi thay đổi data phát event theo **chính sách của lớp dữ liệu** (§4): fact phát một event; tần suất cao thì coalesce; ephemeral không lưu. Delivery tới user là việc của gateway/push, không phải core |
| P4 | Mọi thứ suy ra từ fact là **effect**. Effect hội tụ (event, projection, counter, dọn dẹp) chạy lại an toàn; effect không hội tụ (push, tin hệ thống, webhook) phải idempotent ở consumer theo event id. Fast path chạy ngay (best-effort); reconciler là đường bảo đảm |
| P5 | Số đếm theo target là counter chung: đếm lại tuyệt đối có CAS theo version, không `$inc` rời (D67). Giá trị theo người đọc (unread) tính lúc đọc, không lưu |
| P6 | Id event là khoá tự nhiên + version; receiver áp "version cao thắng"; không có số liên tục toàn room |
| P7 | Tin hệ thống do module SysMsg sinh từ event, với cid tất định `sys-{event_id}` (`ValidCID` không nhận `:`, D93); core không tự sinh |
| P8 | Reconnect lấy trạng thái mới nhất, không phát lại event |

## 4. Khung lớp dữ liệu (bắt buộc cho mọi plan)

Mỗi tính năng khai báo nó thuộc lớp nào. Lớp quyết định đúng **một** cách ghi, một cách phát event, một cách sửa lỗi. Tính năng không khớp lớp nào thì dừng, thiết kế lớp mới trước (D61).

| Lớp | Ví dụ | Ghi | Event | Sửa lỗi |
|---|---|---|---|---|
| Fact bất biến | tạo tin, bản sửa/xoá (`message_edits`), lần ghim (`pin_actions`), fact member | Insert theo khoá unique (khoá là CAS). Lệnh tạo dùng cid; lệnh sửa/xoá mang `base_ver`; ghim không mang `base_pv`, server ghi pv hiện tại + 1 (D92) | Id = khoá; feed chỉ lấy insert | Reconciler từ feed insert; ack mark theo chính sách |
| State có version (projection) | `messages` hiện tại, `rooms.pins`, `user_rooms`, room activity | Effect `set … where ver < v` từ fact (ghim: fold fact sau `pv` + CAS `pv == p`, D92); không bao giờ từ chối fact đã commit | Không có event riêng (event thuộc fact) | Reconciler chạy lại projection |
| Tập (target, user) | reaction (một emoji mỗi (user, tin), D88), thread_subs, bookmark | Một upsert theo khoá unique (`_id`) + change number `n`; cùng giá trị thì doc giữ nguyên, không ghi; gỡ để lại tombstone (D89) | Id `{target}-{user}-n{n}` (reaction: `{room}-{th}-{seq}-{user}-n{n}`, D93); feed insert/update/replace, doc hiện tại thắng | Worker phát lại doc hiện tại (`reaction_event`) |
| Aggregate theo target | số reaction theo emoji, `thread_count`, `member_count` | Recount CAS-ver với witness (§7, D90) | `counts_changed` id `{target}-{counter}-v{ver}` (reaction: `{room}-{th}-{seq}-reactions-v{v}`, D93) | Touch của worker (`reaction_counter`) |
| Giá trị theo người đọc | unread, `mention_unread`, view ẩn/đã xoá | Không lưu; tính lúc đọc từ fact thưa (`hidden`, `cleared_before_seq`) | Không | Không cần |
| Tần suất cao gộp được | vị trí đọc | Upsert đơn điệu (`$max`); đánh dấu chưa đọc dùng version + last-writer-wins | Coalesce, tối đa 1 lần/N giây/room/user | Không ack mark |
| Ephemeral | typing, presence | Không lưu; gateway ↔ NATS core, không qua core | Subject `live.*.eph.*` | Không |

Mỗi tính năng còn khai báo **ngân sách khuếch đại**: số write, read, lookup, event cho một thao tác ở nhóm 5K và channel 200K. Ngân sách vượt thì phải đổi lớp hoặc thêm chính sách coalesce trước khi viết plan.

## 5. Mô hình dữ liệu

Khoá nhị phân: các số `uint64` ghép big-endian nên thứ tự byte trùng thứ tự số (`pkg/keys`). `room_id` là số 63-bit ngẫu nhiên, khác 0 (`pkg/ids`), trả client dạng chuỗi thập phân. `thread_root = 0` là timeline chính; seq bắt đầu từ 1; `MaxUint64` giữ lại. Mỗi timeline (main, mỗi thread) có dãy seq riêng.

| Collection | `_id` / khoá | Lớp | Trường chính | Index | Trạng thái |
|---|---|---|---|---|---|
| `messages` (clustered) | 24B `room│thread│seq` | Fact (tạo) + projection (sửa/xoá) + aggregate (`rx`) | f, kind, text, attachments, mentions, reply_to, forward_from, cid, ts, v, d, ea (lần sửa cuối), meta, `rx {c: [{e, n}], v}` (số reaction theo emoji, mảng sắp theo n giảm rồi e, D90) | **không có index phụ** | Đã xây (tạo; sửa/xoá `v/d/ea`, M2b.2; `rx`, M2b.3) |
| `message_edits` (clustered) | 28B `room│thread│seq│ver` | Fact | `r`, `t`, `k` (`edit`/`delete`), `x` (text), `by`, `ts`; v1 thêm `p` (`prev`, bản gốc); xoá dọn `x`/`p` của bản ≤ v−1 | `{r, ts}` (D70) | Đã xây (M2b.2) |
| `pin_actions` (clustered) | 16B `room│pv` | Fact | `r`, `t`, `op` (1 ghim, 2 bỏ ghim), `th`, `s`, `by`, `ts`; pv dày (D92) | `{r, ts}` (D70) | Đã xây (M2b.3) |
| `rooms` | `room_id` | Metadata + projection | t, type, name, settings, pins `[{th, s, by, ts, pv}]` + `pv` (fold + CAS, D92; `Rooms.Get` không đọc), member_count `{n, ver}`, last_seq, last_msg_at, last_change_at, act_bucket | `{t, dm_key}` unique partial; `{act_bucket}` (D69); `{ca}` (nhánh "tạo trong khoảng" của truy vấn resync, M2b.1) | Đã xây (tạo; activity ls/lm/lc/ab, M2b.1; pins/pv, M2b.3) |
| `members` | ObjectId | Fact member + vị trí đọc | r, u, role, read_seq, cleared_before_seq (`cb`), marked_unread `{v}`, muted_until | `{r, u}` unique | Đã xây (tạo; `cb` M2b.2) |
| `user_rooms` | `u│r` | Projection từ fact member | t, role, joined_at | — (khoá theo user, shard theo user) | Chưa xây |
| `reactions` (clustered) | `k│u` (24B khoá tin + byte user) | Tập | `k`, `r`, `t`, `u`, `e` (`""` = đã gỡ), `pe` (emoji trước), `n`, `ts` | `{k, e}` (đếm phủ index), `{r, ts}` (resync) (D88) | Đã xây (M2b.3) |
| `hidden` | ObjectId | Fact thưa theo người đọc | `u`, `r`, `th`, `s` | `{u, r, th, s}` unique | Đã xây (M2b.2) |
| `thread_subs` | ObjectId | Tập | r, thread_root, u | `{r, thread_root, u}` unique | Chưa xây |
| `bookmarks` | ObjectId | Tập | t, u, r, thread_root, seq, note, ts | `{t, u, ts:-1}` | Chưa xây |
| `reconciler_state` | tên feed (`changes`) | Vận hành | token, at (cluster time) | — | Đã xây |
| `room_events` (clustered) | `room│pts` | — | — | — | Bỏ, không dùng từ M2a.1 |

`rooms.ls/lm/lc/ab` chỉ đổi qua `$max` của effect `room_activity` (không bao giờ lùi); `ab = floor(lc/1h)` là giờ hoạt động **cuối**, nên truy vấn resync lấy `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`.

### 5.1 Sẵn sàng sharding (chỉ đổi cấu hình)

Đã kiểm trên `mongo:8.2.12` (2 shard, 2026-09-30): clustered collection shard được theo `{_id: 1}`, truy vấn khoảng của một room là `SINGLE_SHARD`, `_id` unique toàn cluster.

| Quy tắc | Lý do |
|---|---|
| Collection lớn (`messages`, `message_edits`, `pin_actions`, `reactions`) có `_id` prefix room; shard key tương lai `{_id: 1}` | Một room trên một shard; unique toàn cluster |
| Mọi truy vấn vào `messages` có prefix room | mongos gửi thẳng một shard |
| `messages` **không** có index phụ. Collection fact nhỏ (1–3% lưu lượng) được có index `{room, ts}` (D70) | Giữ working set của `messages` nhỏ; fact nhỏ cần quét theo thời gian cho resync |
| `reactions` clustered, `_id = k│u`, shard key `{_id: 1}` (D88, thay D68); index `{k, e}` và `{r, ts}` | `_id` bắt đầu bằng khoá tin nên reaction của một tin là một range trên một shard; `_id` unique toàn cluster nên không cần unique index phụ có shard key làm prefix. `{r, ts}` không có prefix khoá tin nên `Between` của resync sẽ scatter-gather khi đã shard (công cụ thủ công, chấp nhận); tombstone không bao giờ dọn (cố ý: giữ `n`) |
| `user_rooms` shard theo `u` | "Phòng của tôi" là truy vấn theo user |
| Không transaction nhiều doc (kể cả "cùng room": hai collection shard độc lập), không `$lookup` vào collection lớn | Tránh 2PC xuyên shard |
| URI, read preference, write concern từ cấu hình; tạo collection/index chỉ ở bước bootstrap | Đổi môi trường không đụng code |

## 6. Đường ghi

### 6.1 Slot và quyền sở hữu mềm [Đã xây]

`slot = mix64(room) % 1024` (`pkg/slotmap`, rendezvous `Score`/`Preferred`). `slot.Manager.Step` chạy mỗi tick 1s:
1. Heartbeat bằng một Lua: `SET chatim:core:{id}` (TTL 5s), `ZADD chatim:cores` theo `TIME` của Redis, xoá core hết hạn. Không `SCAN`/`KEYS` (D25).
2. Gia hạn lease `chatim:slot:{n}` (PX 10s) bằng Lua nhiều key, nên Redis state phải là một primary, không Redis Cluster.
3. Nhận/nhả slot về `ceil(1024 / số core sống)`.
4. Publish `chatim:slots:changed`.

`Owns()` chỉ đúng khi lease mới hơn `min(LeaseTTL, HeartbeatTTL) − Tick`, tính từ đầu tick (D24), nên hai core cùng tưởng giữ một slot không quá ~1 tick. Hook nhận cả batch slot, dùng chung `HookTimeout` mỗi `Step` (D26); `BeforeRelease`, `AfterLose`, `AfterClaim` gọi `Router.EvictSlots` (D27, D49). Slot manager dùng Redis client riêng (D28). Client dùng `slotmap.Resolver` để map slot → địa chỉ core; gateway và `tools/internal/route` định tuyến mọi lệnh của một room về core chủ slot.

**Khi va doc của core khác** (D77) [Đã xây]: actor nghỉ có backoff `Jitter(5ms → 200ms)` giữa các vòng gán lại seq; hết lượt gán lại thì actor tự rút qua `a.retire` (cùng cơ chế với slot hook), nên lệnh sau đi về chủ thật và nạp lại từ DB. Double ownership không tự khuếch đại thành bão retry. Không dùng fencing token.

### 6.2 Gửi tin: actor + flusher + chống trùng cid [Đã xây]

1. `grpcsrv` lấy tenant/user từ metadata `x-chatim-tenant`, `x-chatim-user` (caller nội bộ tin cậy tới khi có mTLS); lỗi domain/`apperr` map sang mã gRPC ở `pkg/grpcserver`.
2. `actor.Router`: một goroutine mỗi room, mailbox có giới hạn (đầy → `RESOURCE_EXHAUSTED`), dừng khi rảnh; actor bị retire khi slot đổi chủ; mỗi actor chỉ **một nhóm ghi đang bay** (D29).
3. Chống trùng cid 3 tầng (D34): LRU RAM (nạp từ 100 tin gần nhất khi actor dựng) → Redis dedupe `chatim:cid:{room}:{user}:{cid}` pending `p:{core}` 10s → committed `c:{seq}:{ms}` 15 phút. Actor gọi `dedupe.Batcher` (D58), shard theo `slot % CID_BATCH_SHARDS`: Reserve chặn người gọi, gửi ngay khi rảnh, gom khi đang bay (≤256 key, một `EVALSHA`); Commit/Abort chỉ enqueue, goroutine settle gom pipeline. Redis lỗi → chỉ LRU (`redisguard` cooldown 1s).
4. `flush.Flusher` gom `insertMany(ordered:false, w:majority)` mỗi 2ms hoặc 256 doc; nhóm không kịp deadline thì không gửi (`ErrNotSent`, D33).
5. Kết quả: `Inserted`, `Duplicate`, `Unknown`, `Rejected` (D30). `Duplicate`/`Unknown` đối chiếu bằng `Find` majority theo `(from, cid)`: doc của mình → đã ghi; doc core khác → nạp lại `last_seq`, gán lại (≤3, D31); `Unknown` không thấy → gửi lại đúng seq (≤3). Mỗi entry có ngân sách tuyệt đối = `CID_PENDING_TTL` tính từ Reserve (D32).
6. Xong nhóm (D59): ack (ghi LRU trước) → enqueue event vào publisher → Commit vào batcher → Abort cho entry lỗi. Không bao giờ ack tin chưa ghi.
7. `CreateRoom` ghi room trước, member sau (D35).

Lỗ seq (lỗi ghi dở hiếm) chỉ là số không dùng. Client chèn tin theo seq; không có luật "lỗ cũ hơn 5s là void" (D71).

### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá M2b.2, ghim M2b.3]

Áp cho sửa, xoá cho mọi người, ghim/bỏ ghim (D62–D64). Lệnh đổi không đi qua actor (package `mutate`, D82); vẫn được định tuyến tới core chủ slot.

1. Lệnh sửa/xoá mang `base_ver` là version client đang thấy. Ghim/bỏ ghim **không** mang `base_pv` (D92): server đọc trạng thái ghim hiện tại rồi ghi pv + 1.
2. Đọc **fact cuối** trong range của tin (một reverse scan trên clustered key) để kiểm trạng thái (chưa bị xoá); quyền đã hỏi `access.Policy` trước đó với tác giả và loại của tin (D86: mặc định chỉ tác giả sửa/xoá; D87: loại tin bị khoá theo config; owner/moderator do policy Phase 2). Ràng buộc ghim (`PIN_LIMIT`, mặc định 50) kiểm ở đây, **trước** khi commit fact, bằng fold fact hoặc projection đã tới version hiện tại.
3. Insert fact `message_edits {_id: …│base_ver+1, kind, text, by, ts}`; fact v1 thêm `prev` = text đọc từ `messages` (lúc đó chưa edit nào thắng v1 nên projection còn bản gốc). Sửa và xoá dùng chung không gian version, nên "sửa thua xoá" là kết quả của CAS.
4. Duplicate key → đọc fact ở version đó: cùng tác giả + cùng nội dung → retry, trả thành công (chạy lại projection; event cùng id bị stream bỏ trùng); khác → conflict kèm trạng thái hiện tại.
5. Projection: `updateOne({_id, v: {$lt: ver}}, {$set: {text, v: ver, d}})`. **Ack sau projection** (D64, +1 RT majority; lệnh đổi không thuộc A1). Retry sau crash giữa fact và projection đi qua bước 4 rồi chạy lại projection.
6. Event `msg_edited`/`msg_deleted` id `{room}-{th}-{seq}-v{ver}`, mang snapshot hiện tại. Không ack mark. Worker chạy lại projection (`edit_projection`, delay 0) và event (`msg_changed`, delay `RECONCILE_DELAY`) từ feed insert của `message_edits` (D83).
7. Ghim: fact `pin_actions {room│pv}` với pv = pv hiện tại + 1 (dày) → projection `rooms.pins` + `pv` bằng fold fact sau `pv` + CAS `pv == p` (D92, thay `where pv < v`). "X đã ghim" sinh từ fact (A ghim rồi B bỏ ghim vẫn ra đủ hai thông báo).
8. Xoá cho mọi người cũng dọn nội dung các bản sửa cũ (`$unset text` trên fact edit, giữ khoá; feed chỉ lấy insert nên không sinh effect). Phạm vi xoá theo R-xoá.

**Đã xây (M2b.2, D82–D84, D86, D87):** `grpcsrv` gọi `mutate.Mutator`. `Edit`/`Delete`: `access.Checker.Admit` (`EditMessage`/`DeleteMessage`: tenant + membership) → `Find` tin (không có → `NOT_FOUND`) → `Checker.Allow` với `Author` = tác giả tin và `Kind` = loại tin (chỉ policy quyết; mặc định `access.DefaultPolicy` cho sửa/xoá chỉ tác giả, kể cả owner của room cũng bị từ chối, D86, và từ chối mọi người, kể cả tác giả, trên loại tin có trong `MESSAGE_LOCKED_KINDS`, mặc định rỗng, D87; từ chối → `PERMISSION_DENIED`) → `Edits.Latest` (reverse scan một doc); version hiện tại = `max(v của tin, version fact cuối)`; tin đã xoá hoặc `base_version` lệch → `FAILED_PRECONDITION`, trừ khi là retry (fact `base+1` đã có, cùng tác giả/loại/text) → `Edits.Append` fact `base+1` (v1 sửa có `prev`; `base_version ≥ MaxInt32` = conflict) → `Messages.ApplyEdit` (`$set v, ea, x, d` khi `v < ver`) → xoá thì `Edits.PurgeText` các bản ≤ v−1 → `Find` lại → enqueue `msg_edited`/`msg_deleted` (snapshot sau projection; lỗi enqueue bỏ qua, worker bù) → trả snapshot. `GetEditHistory` (`Admit` → `Find` → `Allow` với `ReadEditHistory`, A7) trả bản gốc (v0 từ `prev`) rồi từng fact theo version, tối đa 100 mỗi lần; tin đã xoá trả rỗng, tin không có → `NOT_FOUND`. `prev` chỉ nằm trên fact sửa v1 (không bao giờ trên fact xoá), nên `PurgeText` khi xoá dọn luôn bản gốc (D75). Wiring: `apps/core/service_wiring.go` dựng `access.Checker` (`DefaultPolicy{LockedKinds}` từ `MESSAGE_LOCKED_KINDS`) + `mutate.Mutator` cho `grpcsrv`; checker của `grpcsrv` và actor dùng `DefaultPolicy{}`, vì sửa/xoá chỉ đi qua `mutate`. Core chưa giới hạn thời gian sửa/xoá và không có luật owner/moderator (module policy chat cắm qua `access.Policy` ở Phase 2).

**Đã xây (M2b.3, D92, D94):** `PinMessage`/`UnpinMessage` → `mutate.Pin`/`Unpin`: `Admit` (`pin_message`/`unpin_message`) → `Find` tin → `Allow` (mặc định mọi member, `MESSAGE_LOCKED_KINDS` không áp) → ghim tin đã xoá → `FAILED_PRECONDITION` (bỏ ghim vẫn được; xoá tin không đụng ghim) → tối đa 3 lượt: `pinproj.Projector.Current` (đọc `rooms.pins/pv`, quét `pin_actions` sau `pv` theo trang 1000, `domain.FoldPins`) → đã đúng trạng thái (ghim tin đã ghim, bỏ ghim tin chưa ghim) → trả state, không fact, không event → ghim khi đã có `PIN_LIMIT` ghim → `FAILED_PRECONDITION` (`ErrTooManyPins`; chính xác vì pv dày và kiểm trên state đúng pv−1) → `Pins.Append` fact pv+1 → trùng khoá: fact ở pv cùng op/tin/người thì là kết quả (retry), khác thì lượt sau; hết lượt → `UNAVAILABLE` (`ErrRetryLater`) → `Projector.Project(room, pv)` (fold + CAS `pv == p`, ≤5 lượt; lỗi → trả fold cục bộ, worker `pin_projection` sửa) → enqueue `msg_pinned`/`msg_unpinned` (id `{room}-p{pv}`, snapshot tin, tin xoá không text) → trả `pin_version` + danh sách ghim (mới nhất trước). `Rooms.Get` (chạy ở mọi `access.Admit`) không đọc `pins`; `PinState` đọc riêng bằng projection `{pins, pv}`. Ngân sách: fast path 3 read + 1 read `rooms.pins` + 1 range scan fact sau pv + 1 insert + 1 CAS majority + 1 event; worker 1 read + ≤1 CAS mỗi room mỗi lô.

Ngân sách: 1–3% tin (giả định §2.3) → 100–300 lệnh/s đỉnh, mỗi lệnh 1 reverse scan + 2 write majority không gom ≈ 200–600 RT/s thêm. Nếu đo thấy nặng thì gom qua flusher.

### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear M2b.2, reaction M2b.3; còn lại chưa]

- Reaction [Đã xây, M2b.3, D88, D89, D93, D95]: một emoji cho mỗi (user, tin); emoji khác thay emoji cũ, emoji rỗng là gỡ ("nhiều trên một user" là reply/mention, M2c). Doc `reactions {_id: k│u, k, r, t, u, e, pe, n, ts}`. Đặt = một upsert `FindOneAndUpdate({_id: k│u}, pipeline, upsert, trả doc trước ghi)`; pipeline dùng `$cond`: emoji đang lưu bằng emoji mới thì giữ nguyên `pe/e/n/ts`, nên doc không đổi byte nào (không có entry oplog, không có change trên feed); khác thì `pe` = `e` cũ, `e` = emoji, `n` = `ifNull(n, 0) + 1`, `ts`. Filter chỉ là phép bằng trên `_id` unique nên server tự retry upsert bị trùng khoá: không có vòng thử lại, không đọc lại. Kết quả suy từ doc trước ghi: không có doc → `n` 1; cùng emoji → no-op; khác → `n+1`, `previous_emoji` = emoji cũ. Gỡ = `FindOneAndUpdate` filter `{_id, e ≠ ""}`, không upsert, để tombstone `e: ""` giữ `n` (không bao giờ chèn tombstone khi chưa có doc). Đúng trạng thái sẵn → không ghi, không event, không touch. Không cid: lệnh là trạng thái mong muốn, retry trễ có thể đưa về trạng thái cũ (chấp nhận cho lớp tập). `ValidateEmoji`: UTF-8 hợp lệ, 1–32 byte, không ký tự điều khiển. Emoji phải nằm trong danh sách cố định `REACTION_EMOJIS` (mặc định `👍,❤️,😂,😮,😢,🙏`, D95); ngoài danh sách → `INVALID_ARGUMENT` (`domain.ErrEmojiNotAllowed`), kiểm trước membership/policy; độ dài danh sách là giới hạn số loại emoji mỗi tin. `GetReactionSettings` trả danh sách theo thứ tự config để frontend chỉ hiện các emoji đó. Tin đã xoá: đặt → `FAILED_PRECONDITION`, gỡ vẫn được. Quyền `react_message` (mặc định mọi member, D94). Sau khi ghi: enqueue `reaction_changed` (id `{room}-{th}-{seq}-{u}-n{n}`, payload `{user, emoji, previous_emoji, change}`) → touch counter inline (§7) → enqueue `counts_changed` nếu `v` tăng → trả `{change, reactions}`. Worker `reaction_event` (delay `RECONCILE_DELAY`, không ack mark): `Get` doc; `n == rec.n` → phát doc hiện tại, `n > rec.n` → bỏ (record mới hơn lo), `n < rec.n` → `ErrStaleRead`, Nak. Vì vậy event trung gian có thể mất (fast path lỡ mà doc đã đổi tiếp); chỉ trạng thái cuối được bảo đảm (lớp tập, D93). `GetHistory` chỉ trả số đếm (`Message.reactions`); emoji của chính người đọc để M3 (`GetReactions`).
- Member: fact thêm/bớt/rời/đổi role; effect cập nhật `user_rooms`, touch `member_count`, event `member_added`/`member_removed` trên subject user để gateway sub/unsub.
- Vị trí đọc: `members {$max read_seq}`; event `read_updated` coalesce. Đánh dấu chưa đọc hạ vị trí nên dùng version + last-writer-wins thay `$max`. SDK nâng `read_seq` khi user gửi tin, để tin của chính mình không tích sau `rs`.
- Ẩn phía tôi [Đã xây, M2b.2]: upsert `hidden {u, r, th, s}` (`mutate.Hide`: `Admit` → tin phải tồn tại → policy `HideMessage` với `Author`; mặc định cho phép). Clear history [Đã xây, M2b.2]: `$max members.cb` (`mutate.ClearHistory`, quyền `ClearHistory`; `up_to_seq = 0` hoặc lớn hơn seq cuối → kẹp về `Last(room, 0)`), trả giá trị sau cập nhật nên không bao giờ lùi. Cả hai không phát event (owner 2026-10-05; đồng bộ đa thiết bị ở M3/M4) và chỉ áp lúc đọc qua `view.HideForViewer` (D85).

## 7. Counter theo target [Đã xây cho reaction, M2b.3]

Áp cho số reaction theo emoji, `thread_count`, `member_count` (D67). Không áp cho unread.

1. Summary `{n, ver}` nằm trên doc đích.
2. **Touch** = recount:
   - đọc counter, được `ver = k`;
   - đếm fact bằng majority read với `afterClusterTime` = operationTime của lần ghi fact (fast path) hoặc `clusterTime` của change event (reconciler);
   - `count == n` thì bỏ, không bump `ver`;
   - khác thì `updateOne({_id, ver: k}, {$set: {n: count}, $inc: {ver: 1}})`; CAS fail → đọc lại, đếm lại.
3. Fast path touch ngay sau fact, coalesce theo cửa sổ W (50ms–1s), tối đa R lần thử. Reconciler touch lại sau delay, thử không giới hạn có jitter.
4. **Không `$inc`.** `$inc` đến trễ sau lần recount cuối sẽ làm thừa vĩnh viễn; recount ghi đè và `$inc` không giao hoán.
5. Hội tụ: mỗi fact sinh một touch của reconciler xảy ra sau nó; sau fact cuối chỉ còn hữu hạn touch dở dang, nên touch cuối ghi được. Target nóng chỉ lệch tối đa một chu kỳ recount.
6. Target nóng (post 200K reactor): W lớn hơn; bật bucket `hash(u) % K` (tổng = Σ bucket) khi đo thấy cần. Client cộng lạc quan từ `reaction_changed`.
7. Single-writer theo `hash(target)` chỉ là tối ưu giảm CAS fail, không thay CAS (soft ownership có chồng chéo ~1 tick).

**Đã xây (M2b.3, D90, tinh chỉnh D67):** summary `messages.rx {c: [{e, n}], v}` là **mảng** (emoji như `$x`, `a.b` không được làm tên field), sắp theo `n` giảm rồi `e` (`domain.SortReactionCounts`); `rx` không có = `v` 0. Touch (`counter.Toucher.Touch`, dùng chung cho fast path và worker): (1) **witness** thay `afterClusterTime` — `Reactions.Count(key, witnesses)` đọc doc của từng witness (`{user, n}`) bằng majority trong một causal session, doc có `n < N` → `store.ErrStaleRead`; (2) aggregate `{$match: {k, e: {$gt: ""}}}, {$group: {_id: "$e", n: {$sum: 1}}}` phủ index `{k, e}` trong cùng session (tự mang `afterClusterTime` của lần đọc witness); (3) bằng summary hiện tại → không ghi, không bump `v`; (4) khác → `SetReactions` CAS `rx.v == k` (`$exists: false` khi k = 0), trượt → `Find` lại rồi lượt sau. Fast path touch inline **trước** khi trả lời (unary handler chỉ trả khi return) với `FastTouchTries` 3, lỗi bỏ qua (client nhận summary đang có, worker sửa). Worker `reaction_counter` (delay `REACTION_COUNT_DELAY` 1s, ≤ `RECONCILE_DELAY`): gom record theo tin trong lô fetch, witness = `max n` của mỗi user, một touch mỗi tin mỗi lô (`DefaultCounterTries` 3), bump → `counter_repaired_total{counter="reactions"}`, `v > 0` → phát lại `counts_changed` của `v` hiện tại (stream bỏ trùng với fast path). Witness không đi qua port driver nào nên đúng cả sau failover (thiếu thì Nak, không ghi số sai). Cửa sổ gom W và bucket `hash(u) % K` (mục 6) **để milestone Channel**: target nóng (post nhận R react/s) đếm lại O(số reaction của tin) mỗi touch, bắt buộc trước khi channel go-live.

## 8. Effect: publish, reconcile, work stream

### 8.1 Publish best-effort [Đã xây]

Sau ack, actor enqueue event vào `publish.Publisher` (D50): hàng đợi theo shard (theo slot), `Enqueue` không chặn, mỗi shard gọi `PublishMsgAsync` theo thứ tự nhận, không chờ ack. nats.go lo publish đang bay (`PUB_MAX_PENDING`), timeout (`PUB_ACK_TIMEOUT` 2s), retry khi chưa có leader; lỗi chỉ log ≤1 lần/giây. Stream `CHATIM_EVT`, subject `evt.{t}.room.{rid}.{type}` (dữ liệu riêng: `evt.{t}.user.{uid}.{type}`), `Nats-Msg-Id` = id tự nhiên, cửa sổ chống trùng `EVT_STREAM_DUPLICATES` 5m, RePublish sang `live.*`.

Sau mỗi `PubAck`, publisher ghi ack mark lên Redis dedupe (`publish.WithAckMarks` → `eventmark`): bitmap `chatim:evtack:{room}:{thread}:{seq>>13}`, gom 10ms/256 key, `PEXPIRE` chunk tối đa mỗi TTL/4 (D60). Publisher chỉ mark event có chính sách ack mark (`markKey`, hiện chỉ `msg_created`), nên ack của event thay đổi (`msg_edited`…) không bật bit che mất `msg_created` bị rớt (D65). Boot kiểm `RECONCILE_DELAY > PUB_ACK_TIMEOUT + 10ms + 1s` (`publish.MarkDeadline`); mặc định 5s, nên `PUB_ACK_TIMEOUT` phải dưới khoảng 3,99s. Delay theo từng effect vẫn ở M2b.1.

Event bị bỏ ở fast path: nack, timeout ack, quá nhiều publish đang bay, hàng đợi đầy, core chết giữa commit và publish.

### 8.2 Reader [Đã xây, M2b.1]

Package `reconcile` là reader, chạy trên core giữ slot 0, mỗi lần nhận slot 0 là một term (log `reconcile term started`). Đọc nhật ký commit qua `store.ChangeFeed`/`store.Cursor` (Mongo: change stream cấp database lọc insert của `messages`, `rooms`, `message_edits`, `reactions`, `pin_actions` và update/replace của `reactions` (không `updateLookup`; update giải mã `documentKey._id` + `updatedFields.n`, D91), vị trí ở `reconciler_state._id = "changes"`), dựng **record** (`work.Record`, chỉ khoá + version + `CommittedAt`, 37 byte; `ReactionChanged` thêm đuôi `len + user`, 38–102 byte; D80, D84, D91) và publish vào work stream với `Nats-Msg-Id` = id record (`m:{room}-{thread}-{seq}`, `r:{room}`, `e:{room}-{thread}-{seq}-v{ver}`, `x:{room}-{thread}-{seq}-{user}-n{n}`, `p:{room}-p{pv}`), cửa sổ `RECONCILE_WINDOW`, retry vô hạn. Vị trí xác nhận = prefix record đã được work stream ack, lưu mỗi `RECONCILE_CONFIRM_EVERY`. Reader không chờ delay, không tra mark, không dựng event. Mất lịch sử (`ErrFeedHistoryLost`) → log Error, `Forget`, bắt đầu từ bây giờ, phục hồi bằng `/app resync` (D52, D81). Record đúng dạng nhưng kind lạ (core cũ gặp record của core mới) bị `Nak` (`WORK_RETRY_DELAY`, đếm vào `work_failures_total`) thay vì `Term`; rolling deploy vẫn phải nâng mọi core trước khi có record mới (D91). Nak không giới hạn số lần nên `ChatimWorkFailing` kêu trong lúc còn core cũ; một fetch có thể đếm cùng record kind lạ nhiều lần khi thời gian chờ fetch (`WORK_FETCH_WAIT`) dài hơn `WORK_RETRY_DELAY` (hiếm với mặc định 1s so với 5s).

### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2, M2b.3]

**Registry + chính sách (D65).** Mỗi loại thay đổi map tới danh sách effect; mỗi effect khai báo:

| Chính sách | Ý nghĩa | Ví dụ |
|---|---|---|
| delay | Reconciler chờ bao lâu sau commit | `msg_created`, `msg_changed` = `RECONCILE_DELAY` (~5s, > `PUB_ACK_TIMEOUT` + cửa sổ mark); projection sửa/xoá 0 (D83); `reaction_counter` = `REACTION_COUNT_DELAY` (1s, ≤ `RECONCILE_DELAY`); projection ghim 0 |
| coalesce | Gom theo target trong cửa sổ | counter: một touch mỗi tin mỗi lô fetch (W/bucket để milestone Channel); `read_updated` N giây |
| ack mark | Có ghi mark để reconciler bỏ qua hay không | Chỉ effect lưu lượng cao (`msg_created`); effect hiếm để reconciler chạy lại, JetStream bỏ trùng |
| hội tụ | Chạy lại an toàn hay consumer phải tự idempotent | event, projection, counter: hội tụ; push, SysMsg: không |

Fast path và reconciler gọi cùng registry, nên kết quả không lệch (test parity cho mọi loại event).

**Topo (a) (D66).**
1. **Reader** nhẹ chạy trên core giữ slot 0: một change stream, `$match` đơn giản (`operationType` + `ns.coll`), đọc từ secondary được, không lookup. Đẩy bản ghi thô vào JetStream **work stream** phân vùng theo slot, `Nats-Msg-Id` = id record (`m:`, `r:`, `e:`, `x:`, `p:`; §8.2). Work stream `CHATIM_WORK`: WorkQueue, file, replica `EVT_STREAM_REPLICAS`, `MaxAge` 2h, chống trùng 2m, 32 partition `work.p{n}`, partition = `slot % 32` (D79).
2. **Vị trí xác nhận** = work stream đã ack bản ghi; reader lưu vị trí có điều kiện, không lùi. Reader chết thì core nhận slot 0 đọc lại từ vị trí cuối; phần trùng bị work stream bỏ theo id.
3. **Worker** (`effects.Workers`) ở mọi core: partition n do core giữ slot n tiêu thụ (consumer durable `work-p{n}`, kiểm `Owns(n)` trước mỗi fetch, lô ≤ `WORK_FETCH_BATCH`); chạy effect theo registry, trước mỗi effect chờ `max(CommittedAt) + delay`; record chỉ ack khi mọi effect xong, lỗi → `Nak(WORK_RETRY_DELAY)`. Registry hiện có:

   | Change | Effect (thứ tự) | Delay | Ack mark | Ghi |
   |---|---|---|---|---|
   | `MessageInserted` | `room_activity` | 0 | — | bulk `$max` lên `rooms`, 1 write/room/lô |
   | `MessageInserted` | `msg_created` | `RECONCILE_DELAY` | có | tra mark theo lô; `Find` tin chưa mark; publish + chờ PubAck |
   | `RoomInserted` | `room_created` | `RECONCILE_DELAY` | không | đọc room, publish (stream bỏ trùng với fast path `CreateRoom`) |
   | `EditInserted` | `room_activity` | 0 | — | `Activity{Seq: 0}`: chỉ nâng `lc`/`ab` (`$max`), không đổi `ls`/`lm`; nhờ vậy resync tìm được room chỉ có sửa/xoá |
   | `EditInserted` | `edit_projection` | 0 | — | `At` fact → `ApplyEdit` (`v < ver`); xoá thì `PurgeText` bản ≤ v−1; fact không còn → drop có đếm |
   | `EditInserted` | `msg_changed` | `RECONCILE_DELAY` | không | `At` fact + `Find` tin + loại room → `msg_edited`/`msg_deleted` (snapshot hiện tại, id theo version fact); publish + chờ PubAck; stream bỏ trùng với fast path; chỉ đếm republish khi PubAck không phải bản trùng |
   | `ReactionChanged` | `room_activity` | 0 | — | `Activity{Seq: 0}` (mọi kind trừ `MessageInserted`, D91): chỉ nâng `lc`/`ab` |
   | `ReactionChanged` | `reaction_counter` | `REACTION_COUNT_DELAY` | không | gom theo tin, witness = `max n` mỗi user; một touch (§7) mỗi tin mỗi lô; bump → `counter_repaired_total`; `v > 0` → phát lại `counts_changed` của `v` hiện tại; tin/room không còn → drop có đếm |
   | `ReactionChanged` | `reaction_event` | `RECONCILE_DELAY` | không | `Get` doc `(k, u)`: `n == rec.n` → `reaction_changed` của doc hiện tại; `>` bỏ; `<` → Nak; doc/room không còn → drop có đếm |
   | `PinInserted` | `room_activity` | 0 | — | như trên |
   | `PinInserted` | `pin_projection` | 0 | — | mỗi room một lần mỗi lô: `Project(room, max pv)` (fold + CAS `pv == p`); room không còn → drop có đếm |
   | `PinInserted` | `pin_event` | `RECONCILE_DELAY` | không | `At` fact + `Find` tin + loại room → `msg_pinned`/`msg_unpinned` (snapshot, tin xoá không text); fact/tin/room không còn → drop có đếm |
4. Work stream có retention tự định cỡ (≈300–500B/tin, đỉnh 3–5MB/s, giữ 2h ≈ 22–36GB với R3; tính lại bằng số thật). Rủi ro mất (RC5) chỉ còn khi reader ngừng lâu hơn cửa sổ oplog.
5. Không mở N change stream lọc `$mod`: mỗi cursor vẫn đọc và lọc toàn oplog phía server.

**Room activity (D69).** Effect coalesce ghi `rooms.last_seq`, `last_msg_at`, `last_change_at` (mọi fact), `act_bucket = floor(ts/1h)` (đổi tối đa 1 lần/giờ/room), chỉ worker ghi, gom theo lô fetch (≤ `WORK_FETCH_BATCH` record → ≤ 1 write mỗi room mỗi lô); actor không ghi (owner chốt 2026-10-05). Chỉ dùng để sắp xếp room list và làm chỉ mục resync; **không** dùng để quyết định client đã có đủ tin (§9).

**Resync (D69).** `/app resync -from -to [-tenant] [-room] [-rate] [-dry-run]` (D81, M2b.1): quét room `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`, record `RoomInserted` cho room tạo trong khoảng, scan ngược timeline chính tới khi `ts < from`, đẩy record vào work stream theo `-rate` (mặc định 500/s); diễn tập bằng itest `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`. Chưa có thread nên timeline chính là timeline duy nhất. M2b.2: sau timeline, mỗi room quét `message_edits` theo `{r, ts}` trong `[from, to]` (trang 1000, dời `from` tới `ts` cuối trang, bỏ trùng mép trang theo id record; một thời điểm đầy cả trang → `ErrEditPageFull`) → record `EditInserted`. M2b.3: tiếp đó quét `reactions` (doc hiện tại, kể cả tombstone, record `ReactionChanged` với `n` của doc) rồi `pin_actions` (record `PinInserted`, `Seq = pv`) cùng cách phân trang (`ErrReactionPageFull`, `ErrPinPageFull`). Fact sửa, reaction và ghim cũng chạy `room_activity` (chỉ `lc/ab`) nên room chỉ có sửa/xoá trong khoảng mất vẫn nằm trong chỉ mục activity khi worker đã chạy. Nhưng resync chọn room theo `ab`, mà trong khoảng mất feed không record nào tới worker: room **chỉ** có reaction/ghim trong khoảng đó (không có tin hay fact sửa nào sau đó nâng `ab`) không được chọn, phải chạy với `-room` (D91). Khi mất vị trí ngoài oplog: công cụ thủ công, có phạm vi (tenant/room), rate limit, diễn tập định kỳ. Quét room theo `act_bucket` trong khoảng mất, scan ngược `messages` tới khi `ts` ra khỏi khoảng, quét `message_edits`/`pin_actions` theo `{room, ts}`, chạy lại effect. Ack mark hết TTL 1h nên resync sinh trùng thật ngoài cửa sổ 5m; consumer bỏ trùng theo id. Rủi ro còn lại: room có activity write cũng mất trong khoảng đó và không có tin sau đó.

## 9. Đường đọc

### 9.1 Hiện có [Đã xây]

`GetHistory(room, thread, anchor, dir, limit≤100)` quét khoảng `_id` clustered, đọc thẳng store, không qua actor.

### 9.2 Reader pipeline + permission hook [Đã xây cho `GetHistory`, `GetEditHistory` và lệnh đổi]

Mọi API đọc (`GetHistory`, `GetMessages`, `ListMyRooms`, `GetEditHistory`…) chạy qua một pipeline sau khi đọc store:
1. Permission hook: một interface duy nhất (tenant, member, role, tác giả và loại của tin đích, quyền đọc lịch sử sửa A7). Mặc định `access.DefaultPolicy` (sửa/xoá chỉ tác giả, D86; loại tin khoá theo config, D87); module policy chat (user → role → quyền) cắm sau (Phase 2).
2. Ẩn: seq ≤ `cleared_before_seq`; seq trong `hidden` → placeholder không nội dung (phân trang theo seq vẫn đúng).
3. Mặt nạ xoá: tin `d` trả placeholder `deleted`.
4. Gộp theo `(sender, cid)` để che lớp trùng CD2/CD3.

Lệnh ghi cũng gọi cùng permission hook.

**Đã xây (M2b.0):** bước 1 là `access.Policy` (interface một hàm `Check`) + `access.Checker` (luôn kiểm tenant và membership trước policy; `access.AllowMembers` là policy mặc định tới M2b.2, nay là `access.DefaultPolicy` (D86)); `SendMessage` hỏi qua actor (`actor.WithPolicy`), `GetHistory` hỏi qua `grpcsrv`. Bước 4 là `view.Pipeline` với `view.CollapseRetried` (giữ seq nhỏ nhất của mỗi `(sender, cid)`); `view.Default()` dùng cho `GetHistory`.

**Đã xây (M2b.2, D85):** bước 2 là `view.HideForViewer` (seq ≤ `Member.ClearedBeforeSeq` hoặc trong `Hidden.HiddenIn(user, room, thread, seq đầu trang, seq cuối trang)` → `hidden = true`, không text), bước 3 là `view.MaskDeleted` (`deleted = true`, không text); seq, version, `edited_at` giữ nguyên nên phân trang theo seq vẫn đúng. `view.Default()` = `CollapseRetried → MaskDeleted → HideForViewer`. Lệnh đổi hỏi `access` với action `edit_message`, `delete_message`, `hide_message`, `clear_history`; `GetEditHistory` với `read_edit_history`.

**Đã xây (M2b.2, D86, D87):** `access.Checker` tách `Admit` (room → tenant → membership, không hỏi policy) và `Allow` (hỏi policy); `Authorize` = hai bước. `Request.Author` là tác giả tin đích, `Request.Kind` là loại tin đích (rỗng/0 với action theo room). Action trên một tin (sửa, xoá, ẩn, `GetEditHistory`) chạy `Admit` → `Find` tin → `Allow`; `clear_history`, `read_history`, `send_message` dùng `Authorize`/actor như cũ. `access.DefaultPolicy` là mặc định của `NewChecker` và actor: `edit_message`/`delete_message` trên loại tin nằm trong `LockedKinds` → `PERMISSION_DENIED` cho mọi người, kể cả tác giả (D87); khi `Author ≠ User` → `PERMISSION_DENIED`; mọi action khác cho member. `LockedKinds` đọc từ `MESSAGE_LOCKED_KINDS` (tên loại cách nhau dấu phẩy, phân biệt hoa thường, mặc định rỗng; tên lạ → lỗi boot; hiện chỉ có `text`, loại mới thêm một dòng vào `domain.kindNames`) và chỉ checker của `mutate` nhận. Core không có luật riêng cho tác giả, owner hay loại tin ngoài policy.

### 9.3 Room list, unread, sync [Chưa xây]

- **Phòng của tôi:** đọc `user_rooms` theo prefix `u` (D72).
- **"Có tin mới"** lấy `last_seq` từ `messages` (reverse scan limit 1, nguồn thật), không từ `rooms.last_seq`. Lý do: event của tin m publish trước lúc client subscribe lại, còn projection coalesce chưa có m, nên client sẽ không bao giờ thấy m cho tới tin sau. Secondary lag: đọc primary, hoặc "pass 2" sau ~3s cho room có `last_msg_at` trong 10s gần nhất.
- **Room-tail cache:** cửa sổ ~300 tin `{seq, sender, flags, deleted}` theo `(room, last_seq)`, ~10KB/room. Phần theo người đọc (rs, tin của mình, hidden) lọc trong RAM. Tải DB từ O(user × room) xuống O(room có tin mới).
- **Unread** theo R17: đếm từ cache/scan ngược tới `rs`, dừng khi đủ 100 hoặc chạm S; chạm S thì trả cận dưới + `approx`, hoàn tất bất đồng bộ rồi đẩy cập nhật. `hidden` lấy theo lô `{u, r ∈ R, seq > min rs}`; hidden ≤ rs bỏ qua.
- **Sync token** (D74): map `room → last_seq client thực sự có` (50 room ≈ 1KB), có thể kèm change version. Server so với `last_seq` thật, trả room có thay đổi. Token mất, quá cũ hoặc quá lớn → full sync (`ListMyRooms` phân trang). Không dùng token thời gian: event best-effort làm "đã thấy tới T" sai.
- **Reconnect storm:** 100K user/60s × 50 room ≈ 83K seek + 16,7K tail fill/s (giả định §2.3). SDK reconnect có jitter (trải 60s ra ~5 phút). Đo ở PoC prod-like.

## 10. Gateway realtime [Chưa xây]

- gws, 1 goroutine/connection, tắt nén, frame ≤64KB, ping 25s, 60s không phản hồi thì đóng. JWT trong subprotocol hoặc frame đầu (≤5s), kiểm bằng JWKS có cache.
- Protobuf `ClientFrame{id, oneof send|edit|delete|react|read|typing|history|sync|sub_presence…}`, `ServerFrame{reply|event|heartbeat}`; subprotocol JSON để debug.
- Hàng đợi gửi mỗi connection có giới hạn (256 frame / 1MB): đầy → bỏ typing/presence trước → vẫn đầy thì đóng `4008 slow consumer`.
- Fanout theo interest: `roomIndex` room→{conn}, một sub NATS mỗi room (đếm tham chiếu); client kết nối → lấy room từ `user_rooms` → sub `live.{t}.room.{rid}.>` + `live.{t}.user.{uid}.>`; Broadcaster encode một lần.
- Dữ liệu riêng của user chỉ đi subject user (H8). `member_removed` → unsubscribe ngay; khoảng rò bằng độ trễ thu hồi.
- Typing/presence ephemeral: `live.{t}.room.{rid}.eph.typing`, không qua core, không vào stream; typing 1 lần/3s, không có trong channel; presence đếm connection trên Redis state.
- Gateway không sắp lại thứ tự; client sắp theo seq, bỏ trùng theo event id.
- Rate limit ~10 tin/s/user (burst 20) + quota theo tenant; giới hạn handshake/s; client backoff + jitter.

## 11. Event stream

- `CHATIM_EVT`: subject `evt.>`, R3, lưu file, 7 ngày, chống trùng 5m (phải dài hơn delay lớn nhất của effect).
- `CHATIM_WORK`: work stream nội bộ của effect engine (§8.3), không phải event cho consumer.
- Envelope `chatim.events.v1.Event{id, tenant, room_id, room_type, thread, seq, type, actor, ts, oneof payload}` (`pts` reserved); chỉ thêm field (buf breaking check).
- Consumer nội bộ: durable pull, lọc theo tenant/loại, ack từng event, bỏ trùng theo id. Thứ tự một room chỉ giữ ở đường bình thường.
- Chế độ tenant "xoá chặt": event không mang text, client fetch (R-xoá).
- NATS chỉ nội bộ; app ngoài đi qua app `events` (Phase 2).

## 12. Guarantee và detector

Mỗi guarantee có detector + alert ngay khi định nghĩa (D76), không đợi M5. Detector là bộ đếm trong component, đăng ký vào `GET /metrics` của cổng admin (D78); luật alert ở `deploy/prometheus/alerts.yml` (16 luật), kiểm bằng `make alerts-check`. Mọi metric có tiền tố `chatim_core_`.

| # | Guarantee | Detector / alert | Metric |
|---|---|---|---|
| RC1 | Mọi fact đã commit cuối cùng có event trên stream, trong delay của effect + lag reconciler | Metric tuổi thay đổi cũ nhất chưa xác nhận; alert khi vượt ngưỡng | `reconcile_lag_seconds` (worker chạy trễ so với delay của effect, xuất ở mọi core), `work_failures_total`, `effect_dropped_total{effect}`, `reconcile_running` (reader), `reconcile_dropped_total` (change hỏng reader bỏ); alert `ChatimReconcilerAbsent`, `ChatimReconcilerLagging`, `ChatimReconcilerDropping`, `ChatimWorkFailing`, `ChatimEffectDropping` |
| RC2 | Event bù giống hệt event fast path (id, nội dung) | Test parity trong CI cho mọi loại event | (test parity, không có metric) |
| RC3 | Reconciler/reader crash, restart, đổi chủ không làm mất gì | Itest; log `reconcile term started`; metric số term | `reconcile_terms_total`, `reconcile_forwarded_total` |
| RC4 | Trùng được phép nhưng hiếm; người nhận bỏ theo id | Metric số event publish lại và số bản trùng bị stream từ chối | `reconcile_republished_total{effect}` (worker gửi vì không có mark; `msg_changed` không có mark, chỉ đếm khi PubAck không phải bản trùng, tức fast path đã mất event), `publish_dropped_total{reason}`, `ack_marks_dropped_total{reason}`; alert `ChatimRepublishSurge`, `ChatimEventsDropped` |
| RC5 | Khoảng mất ngoài oplog là mất hẳn | Alert log `change feed history lost`; metric cửa sổ oplog (giờ) và tuổi vị trí reader; alert khi cửa sổ < 2× ngưỡng; phục hồi bằng `/app resync` (D81) | `reconcile_history_lost_total`, `mongo_oplog_window_seconds` (NaN khi không đọc được); alert `ChatimFeedHistoryLost`, `ChatimOplogWindowShort`, `ChatimOplogWindowCritical`, `ChatimOplogWindowUnknown` |
| CD1 | Retry cùng cid trong 15 phút trả đúng ack cũ, không thêm bản, trên mọi core | Số Commit/Abort bị bỏ khỏi hàng đợi settle; Redis vào chế độ suy giảm | `cid_settle_dropped_total`, `redis_degraded{client}`; alert `ChatimCIDSettleDropped` |
| CD2 | Redis dedupe chết → chỉ LRU, trùng giữa core có thể xảy ra | Log/metric vào chế độ suy giảm; alert | `redis_degraded{client}`; alert `ChatimRedisDegraded` |
| CD3 | Core chết giữa insert và Commit, retry sau 10s ở core khác, tin ngoài 100 tin gần nhất → trùng | Metric `ErrRetryLater` do `PendingElsewhere`; phía đọc gộp theo `(sender, cid)` | `cid_pending_elsewhere_total`; alert `ChatimCIDPendingElsewhere` |
| PJ1 | Projection cuối cùng khớp fact cuối | Worker chạy lại projection từ mọi fact: `edit_projection` (CAS `v < ver`, M2b.2), `pin_projection` (fold + CAS `pv == p`, M2b.3); lỗi → retry qua work stream; fact/room không đọc được → drop có đếm | `work_failures_total`, `effect_dropped_total{effect}` với `edit_projection`, `msg_changed`, `pin_projection`, `pin_event`; `reconcile_republished_total{effect="pin_event"}`; alert `ChatimWorkFailing`, `ChatimEffectDropping` |
| CT1 | Summary counter cuối cùng bằng số fact (reaction: số doc `e ≠ ""` theo emoji); `v` chỉ tăng khi số đổi | Worker `reaction_counter` recount mỗi tin mỗi lô sau mỗi fact (witness: lần đếm thấy mọi write của lô); fast path lỡ (CAS hết lượt, witness cũ, core chết giữa fact và touch) thì worker sửa và đếm; tăng vọt nghĩa là touch inline hỏng hệ thống | `counter_repaired_total{counter}`, `effect_dropped_total{effect="reaction_counter"}`, `reconcile_republished_total{effect="reaction_event"\|"reaction_counter"}`, `work_failures_total`; alert `ChatimCounterRepairSurge` (> 1/s trong 10m), `ChatimWorkFailing` |

Vận hành: oplog `minRetentionHours` ≥ 24h; định cỡ oplog theo byte (đỉnh 10K tin/s × 1–2KB ≈ 0,9–1,7TB/ngày); work stream retention theo §8.3; đo bộ nhớ NATS cho map chống trùng 5m (~3M id ở 10K tin/s); tốc độ xả backlog ≥ 3× ingest đỉnh (đo trên dev ở `docs/poc/README.md` (W1), chỉ kiểm công cụ).

## 13. Hạ tầng, bảo mật, vòng đời [Đã xây]

- **Hai Redis** (D44): state `chatim-redis` chỉ cho slot manager (AOF everysec, `noeviction`); dedupe `chatim-redis-dedupe` cho `chatim:cid:*` + `chatim:evtack:*` (không lưu đĩa, `REDIS_DEDUPE_MAXMEMORY`, `allkeys-lru`). Client dedupe: pool `2 × CID_BATCH_SHARDS + 4`, `MinIdleConns` `2 × CID_BATCH_SHARDS`, `MaxRetries -1`, `DialerRetries 1`, `DisableIdentity`; client state giữ pool 4 (D60).
- **Bí mật** (D45, D46): Redis AUTH bắt buộc, mật khẩu Mongo/Redis qua compose secrets và `*_PASSWORD_FILE`; không nằm trong argv, `docker inspect`, dòng `make` in ra hay log. Mọi log qua handler che credential (D41).
- **Bảo mật:** JWT EdDSA/RS256 theo JWKS tenant; mTLS nội bộ; core kiểm room thuộc tenant ở mọi thao tác; kiểm đầu vào theo A6, UTF-8 hợp lệ; không log nội dung tin.
- **Vòng đời core:** config kiểm ở boot (`apps/core/internal/config`, mỗi component có `Validate()`). Khởi động: publisher → flusher → cid batcher → router → slot manager → workers → reader → gRPC (chỉ mở sau khi khởi động sạch, D40). Dừng: `/readyz` false → drain → gRPC → reader (`RECONCILE_DRAIN + 1s`) → workers (`WORK_DRAIN + 1s`) → router → cid batcher → flusher → publisher → nhả slot → đóng client, trong `CORE_SHUTDOWN_BUDGET` (26.2s/28s; tăng mốc nào phải tăng budget và `stop_grace_period` 33s). Mỗi RPC có `CORE_REQUEST_DEADLINE` (D42); client gRPC tắt service config từ DNS (D43). Core có ba JetStream client: publisher fast path, reader, worker (`WORK_PARTITIONS × WORK_FETCH_BATCH` publish đang bay).
- Monorepo một module, một Dockerfile `ARG TARGET`; Go chạy trong `golang:1.26` qua `make` (D20, D22); code không comment (D23).

## 14. Xử lý sự cố

| Sự cố | Phản ứng |
|---|---|
| Mongo đổi primary (5–12s) | Flusher retry có backoff; quá deadline → `UNAVAILABLE`, SDK gửi lại cùng cid |
| NATS chết | DB vẫn ghi; event fast path mất; reader/worker retry tới khi NATS về; client reconnect lấy mới nhất |
| Redis state chết | Lease hết hạn → `Owns()` false, mọi core vẫn ghi đúng nhờ CAS |
| Redis dedupe chết | Chống trùng chỉ còn LRU (CD2); không có mark → reconciler publish lại, stream bỏ trùng trong 5m |
| Core chết | Core khác nhận slot ~5s; event chưa publish được reconciler bù; core nhận slot 0 đọc lại từ vị trí đã xác nhận |
| Core chết giữa ack và Commit cid | Retry có thể nhận `ErrRetryLater` trong 10s; sau đó trùng chỉ khi tin ngoài 100 tin gần nhất (CD3) |
| Core chết giữa fact và projection | Retry lệnh đổi đi qua nhánh duplicate key rồi chạy lại projection; không retry thì worker `edit_projection`/`pin_projection` sửa ngay khi record tới (delay 0); reaction: worker `reaction_counter` đếm lại sau `REACTION_COUNT_DELAY` |
| Reader chậm/ngừng quá cửa sổ oplog | Alert RC5; `/app resync` (§8.3, D81) |
| Gateway chết / reconnect hàng loạt | Client backoff + jitter; sync token; room-tail cache |
| Room nóng | Mailbox có giới hạn, coalesce counter, rate limit post |

## 15. Kiểm thử

- Unit 70% / integration 20% / E2E 10%. Package có goroutine dùng `testing/synctest` + `goleak`; unit dùng miniredis; không poll thời gian thực ngắn.
- Contract suite cho mọi adapter store (`storetest`, `storetest.RunFeed`); thêm contract: mọi write method là insert unique, CAS, upsert hoặc bump version (D65).
- Mỗi PR theo danh mục event: fact sinh đúng effect; no-op không sinh effect; lỗi không sinh effect; parity fast path / reconciler; chạy effect hai lần cho cùng kết quả.
- Sharding: integration trên cluster 2 shard kiểm `explain` của truy vấn collection lớn là `SINGLE_SHARD` (M3).
- Chaos (M5): kill core, `rs.stepDown()`, failover Redis, kill node NATS → mọi cid đã ack có đúng một bản.

## 16. Rủi ro mở

| Rủi ro | Mức | Xử lý |
|---|---|---|
| A1 (ack p99 30ms) chưa đạt trên dev | Cao tới khi đo | PoC prod-like quyết go/no-go |
| Một replica set giữ toàn bộ dữ liệu tới khi shard | Trung bình | Luật §5.1; ngưỡng và archive chốt sau PoC R1 |
| Chi phí recount target nóng | Trung bình | W, bucket K (để milestone Channel, bắt buộc trước khi channel go-live, D90), đo key/s |
| Thông lượng reader + hop work stream | Trung bình | Đo xả backlog ≥ 3× ingest |
| Tải đọc lúc reconnect storm | Trung bình | Room-tail cache, jitter, đo với số thật |
| Soft ownership không fencing | Thấp | CAS; actor tự rút; fencing là phương án dự phòng nếu chaos fail |

## 17. Decision Log

Quyết định D1–D60 giữ id cũ; chi tiết và phương án bị loại ở [thiết kế Phase 1 (archive)](../archive/designs/260930-chat-core-gateway-design.md#14-decision-log). D53–D57 (nháp M2b) bị bỏ, thay bởi D61–D78.

### 17.1 Còn hiệu lực từ D1–D60

| # | Quyết định |
|---|---|
| D1–D8 | CPaaS nội bộ đa app; Go; ~100K online; DM + group ≤5K + channel lớn (read-fanout); event stream + gRPC stream, không webhook; core và gateway là app riêng; multi-tenant logic; migrate bằng app riêng |
| D9 | MongoDB cho toàn bộ core (chờ PoC prod-like; PostgreSQL thay được qua adapter) |
| D10 | `messages` clustered, `_id` = room│thread│seq |
| D11 | Slot + actor + flush `insertMany` gộp nhiều room |
| D12 | Quyền sở hữu mềm trên Redis, core tự nhận/nhả slot |
| D13 | Seq trong RAM, `_id` unique làm CAS (phần "lỗ → void sau 5s" thay bởi D71) |
| D14 | Lịch sử sửa ở `message_edits` (cách ghi thay bởi D62) |
| D15–D17 | Không API import trong core; NATS chỉ nội bộ; protobuf qua WS + JSON debug |
| D18 | Không cache tin trong Redis (room-tail cache D72 nằm trong RAM core) |
| D20–D23 | Một module, một Dockerfile; replica set sẵn sàng shard; Go qua Docker; không comment trong code |
| D24–D35 | Mốc `Owns`; ZSET `chatim:cores`; hook theo batch; evict actor khi đổi chủ; Redis client riêng cho slot; một nhóm ghi đang bay; 4 trạng thái insert; giới hạn gán lại/gửi lại; ngân sách tuyệt đối mỗi entry; deadline theo nhóm; cid committed 15 phút; room trước member |
| D40–D46 | gRPC mở sau khởi động sạch; che credential trong log; deadline mỗi RPC; tắt service config DNS; hai Redis; Redis AUTH qua secrets; mật khẩu Mongo qua secrets |
| D47, D48 | Event best-effort; id event tự nhiên, bỏ pts |
| D49 | Bỏ watermark, active mark, sweeper |
| D50 | Publisher chỉ giữ hàng đợi theo shard trên cơ chế async của nats.go |
| D51 | Event dựng lại được từ fact/doc với đúng id (áp cho fact theo D62) |
| D52 | Reconcile từ change feed của DB (phần topo thay bởi D66 ở M2b.1) |
| D58–D60 | Gom cid giữa các room; ack trước Commit; pool Redis dedupe nhỏ và ấm, mark gom 10ms |

**Đã thay:** D19 (bởi D47), D36–D38 và phần publisher của D39 (bởi D49).

### 17.2 Mới (2026-10-05)

| # | Quyết định | Phương án bị loại | Lý do |
|---|---|---|---|
| D61 | Khung lớp dữ liệu (§4): mỗi tính năng khai báo lớp + ngân sách khuếch đại; mỗi lớp một cách ghi/event/sửa lỗi | Cơ chế theo kỹ thuật (C1–C10); vá theo tính năng | Tính năng khác nhau theo khối lượng, số người đọc, mức chịu mất; thiếu phân lớp thì mỗi tính năng lại đòi biến thể riêng |
| D62 | Sửa/xoá/ghim = insert fact bất biến (`message_edits` chứa bản mới, v1 có `prev`; `pin_actions`); `messages`, `rooms.pins` là projection | `updateOne` CAS + insert bản cũ (hai write rời); pre/post-image; transaction | Lịch sử đúng; tin hệ thống biết từng lần đổi; feed chỉ cần insert; pre-image bị xoá theo oplog nên không giữ mãi (A4) và khoá vào Mongo; transaction phạm shard-readiness |
| D63 | Lệnh đổi mang `base_ver`/`base_pv`; duplicate key cùng tác giả + nội dung = retry thành công, không event (phần `base_pv` thay bởi D92) | Desired-state thuần | Desired-state thuần bị ABA: retry cũ sau một lần sửa mới cấp version mới và đưa tin về nội dung cũ |
| D64 | Ack lệnh đổi sau projection; ràng buộc (≤50 pin, nay `PIN_LIMIT`) kiểm trước khi commit fact; projection không được từ chối fact đã commit | Ack sau fact, projection async | +1 RT chấp nhận được (lệnh đổi không thuộc A1); ack nghĩa là đọc lịch sử đã thấy bản mới |
| D65 | Registry effect với chính sách theo effect (delay, coalesce, ack mark, hội tụ); mark theo event id + loại; contract test write method | Một delay 30s chung; ack mark cho mọi effect; mark theo seq | Delay chỉ có lý do riêng theo effect; mark theo seq che mất `msg_created` khi có event thay đổi |
| D66 | Topo (a): một reader trên slot 0 → JetStream work stream theo slot, id tự nhiên làm `Nats-Msg-Id`, worker ở mọi core; vị trí xác nhận = work stream đã ack | Một reconciler làm hết; N change stream lọc `$mod`; id theo resume token | Mỗi cursor đọc toàn oplog; work stream đưa retention về thứ tự định cỡ; resume token phụ thuộc phiên bản server |
| D67 | Counter theo target = recount CAS-ver với `afterClusterTime`, fast path cũng chỉ touch; không `$inc` (tinh chỉnh bởi D90: witness thay `afterClusterTime`) | `$inc` + heal định kỳ; guard theo cluster time; `$inc` + recount CAS | `$inc` trễ sau recount cuối thừa vĩnh viễn; guard cluster time chưa kiểm được qua failover |
| D68 | ~~Unique index reaction `{k, emoji, u}`~~ **Thay bởi D88** (M2b.3: một emoji mỗi (user, tin), `_id = k│u`) | `{k, u, emoji}` | Đếm theo emoji thành range count phủ index; vẫn có prefix `k` cho shard |
| D69 | Room activity là projection coalesce (`last_seq`, `last_msg_at`, `last_change_at`, `act_bucket`); index `{act_bucket}`; resync thủ công có phạm vi + rate limit + diễn tập | Index `{last_msg_at}`; resync tự động toàn cục; bỏ resync, chỉ tăng retention | Field đổi liên tục làm index churn; resync toàn cục sinh hàng triệu bản trùng; alert chỉ báo đã mất |
| D70 | Cấm index phụ chỉ áp `messages`; collection fact nhỏ được có `{room, ts}`; P2 thành "cấm join không giới hạn" | Cấm index phụ mọi collection lớn | Sửa tin cũ không đổi `last_seq`, resync cần quét fact theo thời gian |
| D71 | Bỏ luật "lỗ seq cũ hơn 5s là void" | Nâng ngưỡng lên 12–15s | Client không đếm seq (R10); tin chèn theo seq nên tin tới muộn vẫn đúng chỗ |
| D72 | "Có tin mới" đọc từ `messages`; room-tail cache theo `(room, last_seq)`; `user_rooms {u│r}`; `hidden` thưa + `cleared_before_seq`, query theo lô | Dựa `rooms.last_seq` coalesce; unread eager; embed `hidden` vào member | Projection coalesce gây mất tin khi reconnect; eager sinh hàng triệu write/s ở nhóm lớn; danh sách embed không chặn được độ lớn |
| D73 | R17 = exact trong giới hạn S, không thì cận dưới + `approx`; chỉ room list; badge push xấp xỉ | Exact tuyệt đối | Ca xấu (nhiều tin xoá/ẩn sau rs) không thể vừa exact vừa chặn chi phí |
| D74 | Sync token = map `room → last_seq`; full sync khi token mất/quá cũ/quá lớn; SDK reconnect jitter | Token thời gian + TTL 7 ngày; `Sync` theo pts | Event best-effort làm token thời gian bỏ sót tin |
| D75 | Hợp đồng xoá: kho chính + huỷ hiển thị; log vận hành tự hết hạn; tenant xoá chặt dùng event không text | Purge mọi nơi; mã hoá theo tin rồi huỷ khoá | Purge JetStream chưa kiểm chứng; 5–20 tỷ khoá mã hoá không đáng |
| D76 | Mỗi guarantee có detector + alert ngay khi định nghĩa | Để alert tới M5 | Guarantee không có detector thì không biết đã vỡ |
| D77 | Không fencing; actor tự rút khi va doc core khác, retry có backoff + jitter; typing/presence ephemeral không qua core | Fencing epoch theo slot; typing qua slot router | P1 giữ đúng đắn; rủi ro thật là bão retry tự khuếch đại, không phải mất dữ liệu |
| D78 | Detector là bộ đếm atomic + accessor trong component; package `metrics` đăng ký theo kiểu pull vào Prometheus registry riêng, `GET /metrics` trên cổng admin; luật alert trong repo, kiểm bằng `promtool` | Chỉ log; expvar; OTel metrics ngay | Owner chọn 2026-10-05; Prometheus đã nằm trong kế hoạch M5, pull không đổi đường nóng |
| D79 | Work stream `CHATIM_WORK` (WorkQueue, file, `MaxAge` 2h, chống trùng 2m) chia 32 partition `work.p{n}` theo `slot % 32`; consumer durable `work-p{n}` do core giữ slot n tiêu thụ, kiểm `Owns(n)` trước mỗi fetch; ack khi mọi effect của record xong, lỗi → `Nak(WORK_RETRY_DELAY)` | Một consumer theo mỗi slot (1024); một consumer chung, worker tự lọc; partition theo core id; hàng đợi trên Redis | 32 đủ chia tải cho ≤ 32 core với ít consumer; gắn với slot nên worker thường chạy trên chủ room (cache ấm); WorkQueue tự xoá record đã ack nên retention theo backlog chứ không theo lưu lượng; Redis dedupe không bền (D44). Owner chốt 2026-10-05 |
| D80 | Record work stream chỉ mang khoá (kind, room, thread, seq, `CommittedAt`; 33 byte), id `m:{room}-{thread}-{seq}` / `r:{room}`; worker đọc doc khi effect cần (`msg_created` chỉ `Find` tin chưa mark) | Đẩy doc đầy đủ; đẩy event dựng sẵn từ reader | Record nhỏ giữ work stream ≈ 33B/tin thay vì 300–500B (§8.3); phần lớn tin đã có mark nên không cần đọc lại; event chỉ dựng ở effect, dùng chung cho fast path và đường bù (RC2) |
| D81 | Resync là subcommand `/app resync` của binary core: cùng image, secret, config; quét `rooms` theo `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`, scan ngược timeline chính, đẩy record vào work stream có `-rate`; worker chạy effect như bình thường | Binary riêng trong `tools/`; resync tự động khi history lost; tool publish event trực tiếp | Không thêm thứ phải deploy và cấp secret; đi qua work stream nên dùng chung registry + chính sách (delay, mark) và bỏ trùng theo id; tự động dễ sinh hàng triệu bản trùng (D69). Owner chốt 2026-10-05 |
| D82 | Lệnh đổi (sửa, xoá, ẩn, clear) chạy trong package `mutate`, gọi thẳng từ `grpcsrv`, không qua actor; client vẫn định tuyến theo slot của room | Đưa lệnh đổi vào mailbox actor của room; một actor riêng cho lệnh đổi | Lệnh đổi không cấp seq nên không cần thứ tự của actor; CAS trên khoá fact `room│thread│seq│ver` và `v < ver` của projection giữ đúng khi hai core cùng nhận lệnh (P1); không chen vào hàng gửi tin; định tuyến theo slot vẫn giữ cache ấm |
| D83 | Event `msg_edited`/`msg_deleted` mang snapshot hiện tại của tin + `version` của fact, id `{room}-{th}-{seq}-v{ver}`, không ack mark; fast path enqueue sau projection, retry của lệnh đổi enqueue lại đúng id đó; worker chạy `room_activity` (chỉ `lc/ab`), `edit_projection` (delay 0) rồi `msg_changed` (delay `RECONCILE_DELAY`, chỉ đếm republish khi PubAck không phải bản trùng) từ insert của `message_edits` (thay "xoá delay 2–3s") | Event mang diff/text của fact; ack mark cho event đổi; delay xoá riêng 2–3s; projection chỉ ở fast path | Snapshot đúng cả khi event tới trễ hoặc lệch thứ tự (consumer giữ bản version lớn nhất); lệnh đổi hiếm (1–3%) nên publish lại sau delay rẻ và stream bỏ trùng theo id trong 5m, không cần không gian mark riêng (D65); projection delay 0 sửa ngay khi core chết giữa fact và projection; một delay chung đủ vì fast path đã đẩy event ngay |
| D84 | `work.Record` thêm `Version` (`uint32`): 37 byte, id `e:{room}-{th}-{seq}-v{ver}` cho `EditInserted` | Kiểu record riêng cho fact sửa; id theo `_id` nhị phân | Một định dạng cho mọi kind; id đọc được và trùng phần đuôi với id event để truy vết; thêm 4 byte không đáng kể |
| D85 | View: tin có `seq ≤ cleared_before_seq` hoặc trong `hidden` của người đọc → `hidden = true`, không nội dung; tin xoá → `deleted = true`, không nội dung; seq, version giữ nguyên; ẩn/clear không phát event | Bỏ hẳn tin khỏi trang; lọc trong truy vấn store; event `msg_hidden`/`history_cleared` | Phân trang theo seq vẫn đúng và client không tưởng là lỗ seq (R10); `messages` không có index phụ nên lọc ở view rẻ hơn; owner chốt 2026-10-05 không phát event cho ẩn/clear (đồng bộ đa thiết bị ở M3/M4) |
| D86 | Quyền trên một tin (sửa, xoá, ẩn, đọc lịch sử sửa) chỉ do `access.Policy` quyết; core chỉ biết user có quyền hay không, không có luật tác giả/owner. Mặc định `access.DefaultPolicy`: không ai sửa/xoá tin của người khác (chỉ tác giả), action khác cho member. `access.Checker` tách `Admit` (tenant + membership) và `Allow` (policy, `Request.Author` = tác giả tin); action trên một tin chạy `Admit` → `Find` → `Allow`. User → role → quyền thuộc module policy chat (Phase 2) | Luật cứng trong core "sửa: tác giả; xoá: tác giả hoặc owner room" (`domain.ErrNotAuthor`) | Owner chốt 2026-10-05. Mỗi sản phẩm muốn luật khác (owner/moderator xoá, giới hạn thời gian theo tenant); luật cứng trong core không linh hoạt và phải sửa core mỗi lần đổi. Mặc định chặt (chỉ tác giả) an toàn khi chưa có policy; hỏi policy sau `Find` để policy thấy tác giả, trước nhận diện retry nên tác giả gửi lại vẫn được phép |
| D87 | Core không chặn cứng sửa/xoá theo loại tin (vd. tin hệ thống sau này). Config `MESSAGE_LOCKED_KINDS` (tên loại cách nhau dấu phẩy, phân biệt hoa thường; mặc định rỗng = không khoá; tên lạ → lỗi boot) nạp vào `access.DefaultPolicy.LockedKinds`; policy từ chối `edit_message`/`delete_message` trên loại bị khoá, kể cả tác giả. `access.Request.Kind` mang loại của tin đích nên module policy chat (Phase 2) cũng dùng được. Hiện chỉ có `text`; loại mới thêm một dòng vào `domain.kindNames`. Chỉ checker của `mutate` nhận config | Chặn cứng trong core (vd. tin hệ thống không bao giờ sửa/xoá được) | Owner chốt 2026-10-06. Theo D86, core không giữ luật quyền; mỗi sản phẩm tự chọn loại nào khoá. Mặc định không khoá nên hành vi y như D86; sửa/xoá chỉ đi qua `mutate` nên chỉ checker đó cần config |
| D88 | Reaction một emoji cho mỗi (user, tin): collection clustered `reactions`, `_id = k│u` (khoá tin 24B + byte user), trường `k r t u e pe n ts`, shard key tương lai `{_id: 1}`, index `{k, e}` (đếm theo emoji phủ index) và `{r, ts}` (resync); thay D68 | `{k, emoji, u}` unique trên ObjectId (D68, nhiều emoji mỗi user); shard `{k: 1}` | Owner chốt 2026-10-06: một emoji mỗi (user, tin), "nhiều trên một user" là reply/mention (M2c). Khoá `k│u` làm chính `_id` nên là CAS, không cần unique index phụ; `_id` bắt đầu bằng khoá tin nên reaction của một tin là một range trên một shard và unique toàn cluster. `{r, ts}` không có prefix `_id` nên `Between` của resync sẽ scatter-gather khi đã shard (công cụ thủ công, chấp nhận); tombstone không bao giờ dọn (cố ý: giữ `n` để id event không lặp) |
| D89 | Đặt reaction = một upsert `FindOneAndUpdate({_id: k│u}, pipeline, upsert, trả doc trước ghi)`; pipeline `$cond` giữ `pe/e/n/ts` khi emoji đang lưu bằng emoji mới (doc không đổi byte nào: không oplog, không change trên feed), khác thì `n+1`, `pe` = emoji cũ; kết quả suy từ doc trước ghi (không doc → `n` 1; cùng emoji → no-op; khác → `n+1`, previous = emoji cũ); gỡ = update không upsert, filter `e ≠ ""`, để tombstone `e: ""`; đúng trạng thái sẵn → không đổi doc, không event; không cid; `ValidateEmoji` (UTF-8, 1–32 byte, không ký tự điều khiển) + danh sách cố định (D95); tin đã xoá chỉ cho gỡ | Upsert có điều kiện `e ≠ emoji`, trùng khoá → đọc lại majority, thử lại ≤3 rồi `UNAVAILABLE` (bản đầu M2b.3, owner sửa 2026-10-06); toggle theo emoji; cid cho reaction; chèn tombstone khi chưa có doc | Owner sửa 2026-10-06: filter chỉ là phép bằng trên `_id` unique nên server tự retry upsert trùng khoá, bỏ được vòng thử lại, lần đọc lại majority và `UNAVAILABLE` giả; `$cond` thay filter `e ≠ emoji` nên lệnh lặp không ghi gì. Lệnh là trạng thái mong muốn nên retry an toàn; retry trễ có thể đưa về trạng thái cũ, chấp nhận cho lớp tập (người dùng thấy và bấm lại), cid tốn Redis mà không bỏ được ABA; tombstone giữ `n` để id event không lặp |
| D90 | Summary `messages.rx {c: [{e, n}], v}` (mảng, sắp n giảm rồi e); touch = witness (doc của user có `n ≥ N`, majority, causal session) → aggregate phủ `{k, e}` cùng session → bằng thì bỏ (không bump `v`) → khác thì CAS `rx.v == k` (`$exists: false` khi 0); fast path touch inline trước khi trả lời (≤3 CAS, lỗi bỏ qua); worker `reaction_counter` một touch mỗi tin mỗi lô sau `REACTION_COUNT_DELAY` (1s), phát lại `counts_changed` của `v` hiện tại, đếm `counter_repaired_total`; cửa sổ gom W và bucket K để milestone Channel. Tinh chỉnh D67 | `afterClusterTime` truyền qua port (D67 gốc); map `emoji → n`; `$inc`; chỉ touch ở worker | Cluster time không đi qua port mà không lộ kiểu driver, record work chỉ có wall time; witness chứng minh lần đếm đã thấy write, đúng cả sau failover (thiếu thì `ErrStaleRead`, Nak); emoji có thể là `$x`, `a.b` nên không làm tên field; touch inline để người bấm thấy số mới trong reply; worker bảo đảm hội tụ (§7.5) |
| D91 | Feed thêm insert của `reactions`/`pin_actions` và update/replace của `reactions` (update: `documentKey._id` + `updatedFields.n`; thiếu/sai → change hỏng, drop có đếm); kind `ReactionChanged` (4), `PinInserted` (5); `work.Record` thêm đuôi `len(1) + user` chỉ cho `ReactionChanged` (`Version = n`), id `x:`/`p:`; record đúng dạng nhưng kind lạ → Nak có đếm (không Term); `room_activity` để `Seq = 0` cho mọi kind trừ `MessageInserted`; rolling deploy phải nâng mọi core trước khi có record mới (prod chưa live) | `updateLookup`; record riêng cho reaction; đẩy doc đầy đủ vào work stream | `updateLookup` thêm một read mỗi update và trả doc lúc đọc chứ không phải bản của change; worker đọc doc hiện tại nên chỉ cần khoá + `n`; đuôi user giữ record 37 byte cũ đọc được; Nak kind lạ để core cũ không xoá record của core mới; `Seq` của reaction/ghim mà lọt vào `room_activity` sẽ ghi đè `ls/lm`. Đã biết: Nak kind lạ không giới hạn số lần nên `ChatimWorkFailing` kêu trong lúc còn core cũ; một fetch có thể đếm cùng record kind lạ nhiều lần khi `WORK_FETCH_WAIT` dài hơn `WORK_RETRY_DELAY` (hiếm với mặc định 1s so với 5s); resync chọn room theo `ab` nên room chỉ có reaction/ghim trong khoảng mất cần `-room` |
| D92 | Ghim: bỏ `base_pv`; server đọc state (fold `rooms.pins/pv` với fact sau pv) rồi insert fact pv+1 (pv dày, khoá `room│pv` là CAS); đúng trạng thái sẵn = thành công, không fact, không event; `PIN_LIMIT` (50, 1..1000) chính xác; trùng khoá → fact ở pv cùng op/tin/người là kết quả, khác thì thử lại ≤3 rồi `ErrRetryLater`; ghim tin đã xoá → `FAILED_PRECONDITION`, bỏ ghim vẫn được, xoá tin không đụng ghim; projection `rooms.pins` + `pv` = fold + CAS `pv == p` (package `pinproj`, dùng chung fast path và worker `pin_projection`), thay `pv < v`; `Rooms.Get` không đọc pins. Thay phần ghim của D63/D64 | `base_pv` từ client; `$push`/`$pull` + `pv < v`; kiểm giới hạn trên projection | Owner chốt 2026-10-06: client không cần biết pv; fold + CAS đúng mọi thứ tự fact và khi core chết giữa fact và projection; pv dày nên giới hạn chính xác không cần khoá; `Rooms.Get` chạy ở mọi `access.Admit` nên không mang danh sách ghim |
| D93 | Event: `reaction_changed` `{room}-{th}-{seq}-{u}-n{n}` `{user, emoji, previous_emoji, change}`; `counts_changed` `{room}-{th}-{seq}-reactions-v{v}` `{counter: "reactions", reactions}`; `msg_pinned`/`msg_unpinned` `{room}-p{pv}` `{message (tin xoá không text), pin_version}`; không ack mark (worker gửi lại, stream bỏ trùng, chỉ đếm republish khi PubAck không phải bản trùng); `Message.reactions` trong `GetHistory` chỉ là số đếm, `view` che ở tin xoá/ẩn; cid SysMsg `sys-{event_id}` (sửa P7) | `{k}-{u}-{emoji}-n{n}` (§6.4 cũ); ack mark cho event mới; emoji của người đọc trong `GetHistory`; cid `sys:{event_id}` | User `[A-Za-z0-9_-]{1,64}` nên id đọc một cách (ba số từ trái, `n\d+` từ phải) và không trùng `-v{ver}`, `-created`, `-p{pv}`, `-reactions-v{v}`; lưu lượng thấp nên không cần không gian mark; emoji của người đọc cần query theo user (M3 `GetReactions`); `ValidCID` không nhận `:`. Bốn event mới không có ack mark: event reaction trung gian có thể mất (worker chỉ phát doc hiện tại), chỉ trạng thái cuối được bảo đảm (lớp tập) |
| D94 | Action `react_message`, `pin_message`, `unpin_message`; `DefaultPolicy` cho mọi member (không cần là tác giả); `MESSAGE_LOCKED_KINDS` (D87) không áp; thứ tự `Admit` → `Find` → `Allow` (`Author`, `Kind`) | Chỉ owner/tác giả được ghim; khoá loại tin áp cả reaction/ghim | Owner chốt 2026-10-06, như D86: luật owner/moderator thuộc module policy chat (Phase 2) |
| D95 | Danh sách emoji reaction cố định trong config: env `REACTION_EMOJIS` (dấu phẩy, giữ thứ tự; mặc định 6 emoji `👍,❤️,😂,😮,😢,🙏`; để trống = mặc định); boot kiểm không rỗng, emoji hợp lệ (`ValidateEmoji`), không lặp, ≤ 100; emoji ngoài danh sách → `INVALID_ARGUMENT` (`domain.ErrEmojiNotAllowed`), kiểm trước membership/policy, gỡ (emoji rỗng) luôn được; RPC `GetReactionSettings` (không route theo room, cần metadata caller) trả danh sách theo thứ tự config; độ dài danh sách là giới hạn số loại emoji mỗi tin. Bỏ giới hạn mềm theo số loại emoji ở bản đầu của D89 | Mọi emoji hợp lệ, tối đa N loại khác nhau mỗi tin (mặc định 20, mềm, đọc từ `rx`; bản đầu M2b.3) | Owner chốt 2026-10-06: danh sách khai báo trước, frontend chỉ đọc danh sách đó. Giới hạn thành chính xác mà không cần đọc `rx` hay tuần tự hoá trên tin nóng; hết trường hợp user duy nhất của một emoji không đổi được sang emoji mới khi tin đã đủ N loại |
