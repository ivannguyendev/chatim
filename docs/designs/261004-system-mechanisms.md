# chatim — Rà soát cơ chế hệ thống

> Ngày: 2026-10-04 · Trạng thái: **nháp chờ owner review**. M2b tạm hoãn cho tới khi review xong.
> Bổ sung cho [260930-chat-core-gateway-design.md](260930-chat-core-gateway-design.md) (gọi tắt "thiết kế") và [roadmap](../roadmap.md).

## Vì sao có tài liệu này

Lúc lên plan M2b (sửa, xoá, reaction, ghim, read receipt), bản nháp đầu giải từng vấn đề bằng một cơ chế riêng:
- mutation phải đi qua actor;
- reaction có thêm marker `ap` để đếm lại;
- read receipt có bộ gom riêng;
- event thay đổi có đường reconcile riêng.

Owner bác hướng đó (2026-10-04). Mỗi vấn đề phải đưa về **bài toán hệ thống** có cơ chế dùng chung. Tính năng mới chỉ khai báo trên các cơ chế đó, không vá thêm. Tài liệu này gom:
- các nguyên tắc đã chốt;
- bản đồ cơ chế hiện có và còn thiếu;
- các lỗ hổng phải thiết kế trước;
- roadmap đề xuất;
- quy tắc viết plan từ nay.

## 1. Nguyên tắc đã chốt

| # | Nguyên tắc | Nguồn |
|---|---|---|
| P1 | Core nào cũng xử lý đúng mọi room; quyền sở hữu slot chỉ để tối ưu (gom batch, thứ tự, cache) | thiết kế §5.1, D12 |
| P2 | DB là nguồn sự thật. Mỗi thay đổi là một **fact** ghi bằng một lệnh atomic trên một doc; không transaction nhiều doc | thiết kế §4.1, D13 |
| P3 | Mọi thay đổi data của một entity đều phát **một** event lên `CHATIM_EVT`. Fanout/delivery tới user là việc của Notification, không phải core | owner 2026-10-04 |
| P4 | Mọi thứ suy ra từ fact (event, counter, dọn dẹp) là **effect**: hàm thuần của doc, idempotent. Effect chạy ở đường nhanh (best-effort); **reconciler là đường bảo đảm duy nhất** | D47, D51, D52, owner 2026-10-04 |
| P5 | Đếm là bài toán **counter** chung (đếm lại tuyệt đối, chặn theo thời điểm), không dùng `$inc` rời rạc ở từng tính năng | owner 2026-10-04 |
| P6 | Id event là khoá tự nhiên + version của doc; receiver áp "version cao thắng"; không có số liên tục toàn room | D48, D53 |
| P7 | Tin hệ thống (vd "X đã ghim tin") do module SysMsg hứng event mà tạo; core không sinh | owner 2026-10-04 |
| P8 | Client reconnect lấy trạng thái mới nhất, không phát lại event | R10/R15 (decision log M2a.1) |

## 2. Bản đồ cơ chế hệ thống

| # | Cơ chế | Trạng thái | Dùng bởi |
|---|---|---|---|
| C1 | **Ghi fact**: insert theo seq (`_id` làm CAS), CAS theo version, upsert theo khoá unique | Có cho insert (M2a); CAS/upsert chưa có | send; sửa/xoá/ghim (CAS); reaction/ẩn/đọc (upsert) |
| C2 | **Command idempotent**: lệnh tạo dùng cid dedupe; lệnh thay đổi mô tả **trạng thái mong muốn** (đặt text, đặt active, đặt pinned, `$max` read), nên retry là no-op | Có cid dedupe; quy tắc trạng thái mong muốn **chưa thành luật** | mọi RPC ghi |
| C3 | **Commit log → effect**: registry theo collection, một executor dùng chung cho đường nhanh và reconciler; mark chỉ là tối ưu | Một phần: chỉ `msg_created` (M2a.2) | mọi event, counter, dọn dẹp |
| C4 | **Counter**: đếm lại tuyệt đối, chặn theo thời điểm đọc, gom touch, partition khi quá lớn | Chưa có | số reaction, `thread_count`, `member_count`, unread 99+, `mention_unread` |
| C5 | **Thứ tự + version**: seq theo timeline, version theo doc | Có | mọi entity |
| C6 | **Slot + actor**: gom batch đường gửi | Có | chỉ send |
| C7 | **Lifecycle + ngân sách dừng** | Có | mọi component mới có goroutine |
| C8 | **Quyền**: tenant + member + bất biến tác giả/owner | Có tenant + member; bất biến tác giả chưa có | mọi RPC |
| C9 | **View theo người đọc**: ẩn tin `hidden`, `cleared_seq`, placeholder tin đã xoá | Chưa có | GetHistory, ListMyRooms, GetMessages |
| C10 | **Dọn dẹp/retention** như effect: xoá `message_edits` của tin đã xoá; TTL `room_events` (chưa dùng) | Chưa có | xoá tin, retention theo tenant |

## 3. Lỗ hổng: bài toán chưa có cơ chế

Mỗi lỗ hổng phải thiết kế (brainstorm, chốt quyết định) **trước** khi viết plan cho tính năng dùng nó.

| # | Lỗ hổng | Vì sao phải làm trước | Đề xuất |
|---|---|---|---|
| H1 | **Quản lý member**: thêm/bớt/rời room, đổi role. Roadmap chưa có; core chỉ có CreateRoom | `member_count` (C4), event `member_added`/`member_removed` (gateway cần để sub room), quyền theo role | Đưa vào M2b hoặc M2c, dựng trên C1 + C3 + C4 |
| H2 | **Room type channel** (~200K subscriber, chỉ admin post): có trong phạm vi Phase 1 (thiết kế §1) nhưng `domain` chỉ có dm/group | Quyền post, fanout, counter rất lớn | Chốt milestone cho channel |
| H3 | **View theo người đọc** (C9) chưa có thiết kế chung | Ẩn tin, `cleared_seq`, quyền đọc lịch sử sửa (A7) sẽ bị vá rải rác | Brainstorm C9 trước phần sửa/xoá |
| H4 | **Điểm cắm quyền**: policy để Phase 2 nhưng chưa có chỗ cắm chung | Mỗi RPC tự kiểm thì dễ lệch | Gom kiểm quyền về một interface từ M2b; policy cắm sau |
| H5 | **Reconciler một instance** (core giữ slot 0) sẽ gánh mọi collection | Lag khi tải lớn làm chậm mọi effect | Đo ở M2b.1; thiếu thì chia feed theo partition (cơ chế, không vá) |
| H6 | **API cho caller nội bộ** (SysMsg tạo tin có `kind` + cờ unread) | Unread chính xác (M3) cần cờ; gateway (M4) không được chuyển `kind` từ client | Chốt cùng M3 |
| H7 | **Đồng bộ đa thiết bị**: P3 + subject user có thể thay phần "đồng bộ trạng thái giữa các thiết bị" của M3 | Tránh làm hai cơ chế cho cùng việc | Chốt khi thiết kế lại M3 |
| H8 | **Riêng tư của event user** khi đi qua `live.*` | Gateway phải sub theo user, không để lộ cho cả room | Ghi vào thiết kế M4 |

## 4. Roadmap đề xuất

Chưa áp vào [roadmap](../roadmap.md); chờ owner review.

| Milestone | Nội dung | Cơ chế dùng | Cần thiết kế trước |
|---|---|---|---|
| M2b.1 — Effects + Counter | C3 tổng quát (feed cấp DB, registry, executor); mark ack chỉ cho `msg_created`; C4; `room_created` làm ca chứng minh | C3, C4, C7 | — (nháp ở phụ lục) |
| M2b.2 — Sửa + xoá | sửa (kèm lịch sử), xoá cho mọi người / phía tôi, GetEditHistory, quyền tác giả | C1, C2, C3, C8, C9, C10 | H3, H4 |
| M2b.3 — Reaction + ghim | fact reaction + counter, ghim | C1–C4 | — |
| M2b.4 — Member + đọc | thêm/bớt/rời member, MarkRead | C1–C4 | H1 |
| M2c — Thread & tiện ích | thread (`thread_count` = C4), mention, reply/forward, bookmark, đánh dấu chưa đọc | C1–C5 | seq theo timeline thread trong C6 |
| M3 — Đường đọc | ListMyRooms, unread 99+ (C4 có giới hạn), Sync mới | C4, C9 | H6, H7 |
| M4 — Gateway | WebSocket, fanout theo interest, subject user | — | H8 |
| M5 — Hardening | như roadmap + đo lag reconciler | — | H5 |
| Channel | room type channel | — | H2 |

## 5. Quy tắc viết plan từ nay

1. Mỗi plan milestone mở đầu bằng bảng **tính năng → cơ chế** (§2). Tính năng cần cơ chế chưa có thì dừng, thiết kế cơ chế đó trước, rồi mới viết plan.
2. Mỗi tính năng khai báo đủ:
   - fact: lệnh atomic nào, trên doc nào;
   - command idempotent theo cách nào (C2);
   - effect: event, counter, dọn dẹp;
   - quyền (C8);
   - view theo người đọc (C9);
   - dòng trong danh mục event.
3. Plan chi tiết từng bước TDD như plan M2a.2: file, test, lệnh chạy, "Expected". Người thực thi gặp chỗ plan không nói tới thì **dừng và báo**, không tự vá. Quy tắc này đã có trong CLAUDE.md, giờ áp cả cho thiết kế.
4. Mỗi PR có bộ test chung theo danh mục event:
   - fact sinh đúng effect;
   - no-op không sinh effect;
   - lỗi không sinh effect;
   - đường nhanh và reconciler cho cùng kết quả (parity);
   - chạy effect hai lần cho cùng kết quả.

## Câu hỏi cho owner

- Thứ tự M2b.1 → M2b.4 có đúng ưu tiên không? H1 (member) có kéo vào M2b không?
- Channel (H2) đặt ở milestone nào?
- Còn bài toán hệ thống nào thiếu trong §2/§3?

## Phụ lục: nháp cơ chế cho M2b (đề xuất, chưa duyệt)

### A. Commit log → effect (C3)

- Effect = `PublishEvent` | `TouchCounter` | `Cleanup`. Mỗi effect là hàm thuần của post-image doc, idempotent. Chạy lặp hay đảo thứ tự vẫn đúng: event có `Nats-Msg-Id`, counter có chặn thời điểm, dọn dẹp là `deleteMany` theo khoá.
- Một registry theo collection: `Handler(change) []Effect`. Đường nhanh (ngay sau commit) và reconciler (change stream, sau D = 30s) gọi cùng registry.
- Feed: một `db.Watch` lọc `ns.coll` ∈ {messages, message_edits, rooms, members, reactions, hidden}. Update chỉ lấy khi trường version đổi, dùng `fullDocument: updateLookup`. Vẫn một vị trí ở `reconciler_state`; token cũ của stream cấp collection không dùng được thì lùi về `StartAtOperationTime(at)`. Post-image có thể mới hơn thay đổi đang đọc, nên effect dựng theo bản mới; điều này hợp với P6.
- Mark ack là tối ưu khai báo theo loại effect. Hiện chỉ `msg_created` có mark.
  - **Lỗi cần sửa ngay khi mở lại M2b:** publisher đang mark mọi event theo (room, thread, seq) (`apps/core/internal/publish/publisher.go:130`).
  - Một event thay đổi được ack sẽ bật bit của tin, và reconciler sẽ bỏ qua `msg_created` bị mất.
- Mutation không cần đi qua actor. Fact là CAS hoặc upsert trên một doc (P1, P2). Thứ tự giữa các event không cần, vì receiver áp version cao thắng. Hướng này đổi D54 phần "qua actor".

### B. Counter (C4)

- Định nghĩa: `Counter{name, target(doc, field), facts(query có prefix k), cap?}`. Giá trị là đếm lại tuyệt đối từ fact, không dùng `$inc` (đổi D56 phần summary).
- Touch:
  - Đếm bằng read majority trong một session causal, lấy `operationTime` T.
  - Ghi `updateOne({target, "<field>.t": {$lt: T}}, {$set: {"<field>.v": n, "<field>.t": T}})`.
  - Kết quả chỉ tiến, idempotent, và bản đếm cũ hơn không ghi đè được bản mới hơn.
- Gom: nhiều touch cùng counter trong cửa sổ ngắn thành một lần đếm.
- Partition (nút scale, mặc định tắt): đếm theo bucket `hash(u) % B`, tổng = Σ bucket. Chỉ bật khi số đo cần.
- Reconciler touch lại sau D, nên mọi lệch tự hội tụ về đúng.

### C. Danh mục event M2b (nguồn test case)

Subject `evt.{t}.room.{rid}.{type}`; dữ liệu riêng của một user dùng `evt.{t}.user.{uid}.{type}` (thiết kế §7).

| Fact | type | Subject | Id | actor |
|---|---|---|---|---|
| `rooms` insert | `room_created` | room | `{room}-created` | created_by |
| `messages` insert | `msg_created` | room | `{room}-{th}-{seq}` | from |
| `messages` CAS `v` (sửa) | `msg_edited` | room | `{room}-{th}-{seq}-v{ver}` | editor |
| `messages` CAS `v`, `d` (xoá cho mọi người) | `msg_deleted` | room | `{room}-{th}-{seq}-v{ver}` | deleted_by |
| `hidden` insert (xoá phía tôi) | `msg_hidden` | user | `{room}-{th}-{seq}-h-{user}` | user |
| `reactions` upsert (`n++`) | `reaction_changed` | room | `{room}-{th}-{seq}-r{oid}-{n}` | user |
| counter ghi được | `counts_changed` | room | `{target}-{counter}-t{T}` | — |
| `rooms` CAS `pv` (ghim) | `pins_changed` | room | `{room}-pins-v{pv}` | by |
| `members` `$max rs` (đọc) | `read_updated` | room | `{room}-read-{user}-{seq}` | user |

Effect khác ngoài event:
- `msg_deleted` → dọn range `message_edits` của tin.
- `message_edits` insert khi tin đã xoá → dọn bản đó. Đây là trường hợp edit thua delete trong D55.
- `reactions` → touch counter `reactions:{emoji}`.

Quyền (owner chốt 2026-10-04): sửa chỉ người gửi; xoá cho mọi người là người gửi hoặc owner room; react, ghim và đọc chỉ cần là member.
