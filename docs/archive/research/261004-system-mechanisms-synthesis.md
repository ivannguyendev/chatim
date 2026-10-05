# Tổng hợp phản biện vòng 1 — cơ chế hệ thống

> Ngày: 2026-10-04, cập nhật 2026-10-05 · Trạng thái: **đã chốt sau vòng 2**, không cần vòng 3 · Bản lưu cho owner, không gửi reviewer.
> Nguồn: [báo cáo gốc](261004-system-mechanisms-report.md) · [phản biện CL](261004-system-mechanisms-review-cl.md) · [phản biện GE](261004-system-mechanisms-review-ge.md) · Vòng 2: [gửi CL](261004-system-mechanisms-round2-cl.md), [gửi GE](261004-system-mechanisms-round2-ge.md), [CL trả lời](261004-system-mechanisms-round2-review-cl.md), [GE trả lời](261004-system-mechanisms-round2-review-ge.md). Thiết kế đã chốt: [../designs/261005-chatim-architecture.md](../designs/261005-chatim-architecture.md).

## 1. Hai bản đồng ý (chấp nhận, không tranh luận tiếp)

| Chủ đề | CL | GE | Kết luận |
|---|---|---|---|
| Unread | tính lazy lúc đọc, scan `seq > readSeq` có limit | lazy, `limit(101)` | Bỏ unread khỏi C4; unread là giá trị theo người đọc, tính lúc đọc |
| Lịch sử sửa | không phải hàm thuần của doc hiện tại | ghi nội dung v5 vào chỗ v2 | `updateLookup` sai cho lịch sử, phải đổi mô hình (vòng 2, 2.A) |
| Phân loại lệnh | 7 lớp dữ liệu | Type A/B/C | Lấy bảng lớp dữ liệu (CL §5) làm khung chính |
| Chính sách effect | delay, coalesce, ack-mark, ephemeral theo từng effect | ack-mark cho mọi effect | Một registry, chính sách khai báo theo từng effect |
| Event thay đổi tới trước event tạo | receiver upsert theo version | event mang snapshot | Event thay đổi mang đủ trạng thái hiện tại |
| Tin hệ thống không trùng | unique key theo event id | `cid = "sys:" + event_id` | Dùng cid tất định, tái dùng chống trùng cid (B4) |
| Reconnect storm | thundering herd lên đường đọc | sync token | Đưa vào thiết kế M3 |
| CD2/CD3 | chấp nhận, gộp phía đọc theo (sender, cid) | chấp nhận | Giữ; thêm gộp phía đọc và alert chế độ degraded |
| Fencing | chưa cần nếu P1 giữ | epoch theo slot | Chưa cần; actor tự rút khi va doc của core khác, retry có backoff + jitter |
| Thứ tự lỗ hổng | lớp dữ liệu → H3/H4 → inbox → H1 | H3/H4 → H1 → H8 | H3 + H4 trước sửa/xoá |

## 2. GE sai hoặc phóng đại (đã đối chiếu docs)

1. **"Edit vào DB trước khi tin được tạo".** Không thể: edit cần `seq`, client chỉ có `seq` từ ack, ack chỉ trả sau insert `w:majority`. Chỉ thứ tự **event** có thể đảo, báo cáo đã nêu (5.2 mục 6).
2. **"Transaction single-shard vì cùng shard key".** `messages` và `message_edits` là hai collection shard độc lập; chunk của cùng một room có thể nằm ở hai shard, nên đó là transaction phân tán. Lại phạm luật shard-readiness (D13, thiết kế §4.1).
3. **"Bão duplicate xuống consumer".** JetStream bỏ bản trùng theo `Nats-Msg-Id` trong `EVT_STREAM_DUPLICATES` (5m), consumer không thấy. Chi phí thật chỉ là publish + lookup ở reconciler. Đúng một phần với effect lưu lượng cao (vị trí đọc), giải bằng chính sách theo effect.
4. **"Cluster time an toàn tuyệt đối qua failover".** Không có dẫn chứng; CL nghi ngờ. Hết cần kiểm nếu bỏ guard thời gian (vòng 2, 2.B).
5. **"CD3 chỉ vài ms".** Sai: cửa sổ dài tới `CID_PENDING_TTL` (10s) với tin ngoài LRU, và tương quan với tải (CL 2.7).
6. **"Nâng ngưỡng hole lên 12–15s".** CL đúng hơn: bỏ hẳn luật void, vì client không phát hiện mất bằng cách đếm seq (R10).
7. **Query unread `from != me, deleted: false`.** Bỏ qua `hidden` nằm ở collection khác (join, phạm P2) và scan có thể vượt 101 khi nhiều tin của chính mình hoặc đã xoá.
8. **"Hạ D xuống 3–5s".** Hợp lý cho `msg_created` (D chỉ cần lớn hơn `PUB_ACK_TIMEOUT` 2s cộng cửa sổ mark 10ms), nhưng D phải là tham số theo effect, không phải hằng toàn cục.

## 3. CL: điểm mạnh nhất

- **Fact bất biến tách khỏi state projection (2.1).** Giải cùng lúc lịch sử sửa, tin hệ thống "A đã ghim" (P7), feed chỉ cần insert, và bớt phụ thuộc Mongo (`updateLookup`, pre-image).
- **Oplog (2.5).** Retention tính theo byte (đỉnh 10K tin/s ≈ 0,9–1,7TB/ngày); cần quy trình resync có test, tốc độ xả backlog ≥ 3× ingest, alert gắn với từng guarantee ngay khi định nghĩa.
- **Mảng còn thiếu (§4).** Inbox/room list; thu hồi quyền so với live fanout; chế độ import im lặng; contract test bắt buộc bump version; lệnh không đơn điệu (đánh dấu chưa đọc).
- Hai điểm CL tự nhận chưa chắc (cluster time, nút nghẽn reconciler) đều giải bằng thiết kế (2.B) hoặc bằng đo (PoC).

## 4. Đem ra vòng 2

Chỗ hai bản mâu thuẫn hoặc là tổng hợp mới chưa ai tấn công. Chi tiết trong hai file vòng 2.

| # | Đề xuất | Vì sao còn mở |
|---|---|---|
| 2.A | Sửa/xoá/ghim = insert fact bất biến (unique key là CAS version); `messages`, `rooms.pins` là projection | Ý của CL, GE chưa phản bác; GE đề xuất pre/post-image |
| 2.B | Counter theo target: `$inc` + `ver++` ở fast path, recount CAS-ver gom theo target ở reconciler | Ghép ý GE (`$inc`) và CL (CAS-ver); chưa ai kiểm tính hội tụ |
| 2.C | Resync khi mất oplog qua projection "room activity"; topo reconciler | CL: một reader + work stream; GE: N change stream lọc `$mod` |
| 2.D | Room list + unread lúc reconnect storm | Chưa ai tính chi phí lazy scan × số room × 100K user |

## 5. Quyết định đề xuất chốt (không cần vòng 2)

- Khung chính: bảng lớp dữ liệu (CL §5) + reader pipeline (GE Primitive 4) + registry effect có chính sách.
- Mỗi tính năng khai báo thêm: lớp dữ liệu và ngân sách khuếch đại (số write, read, lookup, event mỗi thao tác ở nhóm 5K và channel 200K).
- Mỗi guarantee (RC1–RC5, CD1–CD3) có detector + alert ngay khi định nghĩa, không đợi M5.
- P2 sửa câu: mỗi command có một fact chính làm điểm tuần tự hoá; mọi thứ khác là effect idempotent.
- P3 bỏ "mọi thay đổi đúng một event": event theo chính sách lớp (vị trí đọc coalesce; typing/presence ephemeral qua NATS core).
- P4 chia effect hội tụ (event, counter, dọn dẹp) và không hội tụ (push, tin hệ thống, webhook: idempotent ở consumer theo event id).
- `RECONCILE_DELAY` thành tham số theo effect; `msg_created` hạ về khoảng 5s.
- Bỏ luật "hole cũ hơn 5s là void" (sửa D13).
- Sửa lỗi mark của publisher (`apps/core/internal/publish/publisher.go:130`): mark theo event id và loại event.
- Contract test: mọi write method của adapter là CAS, upsert hoặc bump version.
- Ghi nhận cho sau: thu hồi quyền vs live fanout (M4); chế độ import im lặng (migrator, Phase 2); lệnh không đơn điệu dùng version + last-writer-wins.

## 6. Vòng 2 (2026-10-05)

### Kết luận: không cần vòng 3

Các điểm còn lại thuộc một trong ba loại, không loại nào cần reviewer phản biện thêm:
- mâu thuẫn đã có lời giải: CL đưa phản ví dụ cụ thể, GE chỉ đưa ưu tiên;
- quyết định sản phẩm: owner đã chốt (bên dưới);
- số đo: đưa vào PoC prod-like.

GE không trả lời mục 4.1 (rút lại khẳng định sai). Không đòi nữa: GE đã nhận fact + projection (bỏ pre-image) và topo (a) (bỏ `$mod`), nên các khẳng định đó hết hiệu lực.

### Mâu thuẫn và cách chốt

| Chủ đề | CL | GE | Chốt | Lý do |
|---|---|---|---|---|
| Counter | Bác `$inc` ở fast path: `$inc` trễ quá D tới sau recount → thừa 1 vĩnh viễn. Fast path cũng chỉ là touch (recount, coalesce W) | Giữ `$inc`, gom qua slot router | **CL**: chỉ recount CAS-ver | `$inc` và recount ghi đè không giao hoán. Ý gom của GE vẫn dùng: lệnh của một room đã được định tuyến theo slot về một core, coalescer chạy ở đó |
| Ack lệnh sửa/xoá | Sau projection (+1 RT; sửa không thuộc A1) | Sau fact, projection async | **Sau projection** (owner) | Lỗi retry GE nêu (duplicate key) hết nhờ `base_ver`: retry gặp dup key → đọc fact, cùng tác giả + nội dung → trả thành công |
| Typing/presence | Ephemeral qua NATS core | Đi qua slot router của core | **Ephemeral, không qua core** | Không lưu, không cần thứ tự |
| Checkpoint reader | Vị trí xác nhận = work stream đã ack | `Nats-Msg-Id = "change:" + resume_token` | **Id tự nhiên của fact** | Không phụ thuộc định dạng resume token theo phiên bản server |
| Sync token | `map room → last_seq client có`; token thời gian bỏ sót tin | `{t}` + TTL 7 ngày, quá hạn → full sync | **CL + fallback full sync của GE** | Token thời gian sai khi event best-effort |
| Oplog / resync | Giữ công cụ resync thủ công, có phạm vi, rate limit, diễn tập | Retention 48–72h, bỏ resync | **CL** | Alert chỉ báo đã mất; resync mới là phục hồi |
| `hidden` | `cleared_before_seq` trên member + `hidden` thưa, query theo lô | Embed danh sách vào member doc | **CL** | Danh sách embed không chặn được độ lớn |

### Nhận từ CL

- 3.A: lệnh đổi mang `base_ver`/`base_pv` (chống ABA khi retry); fact v1 mang `prev` (giữ bản gốc); xoá có delay riêng 2–3s; giới hạn 50 pin validate trước khi commit fact, projection không được từ chối fact đã commit.
- 3.B: recount CAS-ver, đọc với `afterClusterTime`; bỏ ghi khi `count == n`; unique index reaction đổi thành `{k, emoji, u}`; id `counts_changed` = `{target}-{counter}-v{ver}`; bucket K bật theo số đo.
- 3.C: topo (a); work stream có retention tự định cỡ; index `act_bucket = floor(ts/1h)` thay `{last_msg_at}`; `rooms.last_change_at` cho mọi fact; collection fact nhỏ được có index `{room, ts}`.
- 3.D: "có tin mới" đọc `last_seq` từ `messages` (lỗ mất tin khi reconnect); room-tail cache theo `(room, last_seq)`; `user_rooms` khoá `{u│r}`; SDK reconnect có jitter; P2 sửa thành "cấm join không giới hạn".

### Owner chốt (2026-10-05)

1. Ack lệnh sửa/xoá sau projection.
2. R17: exact nếu quét xong trong giới hạn S; không thì trả cận dưới + cờ `approx` rồi đẩy bản đúng sau. R17 chỉ áp cho room list; badge push xấp xỉ, do push app giữ.
3. Xoá cho mọi người = xoá ở kho chính + huỷ hiển thị. Oplog, stream 7 ngày, backup là log vận hành tự hết hạn. Tenant cần xoá chặt bật chế độ event không mang text.
4. Số liệu đầu vào (50 room/user, sửa 1–3% tin, 3–5K room active, ~500M member doc) là giả định; hỏi owner số thật khi tới plan M3, PoC prod-like hoặc sizing.

### Số đo đưa vào PoC

Key/s của count trên index; write conflict trên hot doc; JetStream publish ack R3 và độ trễ hop work stream; thông lượng một cursor change stream (xả backlog ≥ 3× ingest); tải đọc lúc reconnect storm (cache lạnh/ấm); dùng `clusterTime` của change event làm `afterClusterTime`; tỉ lệ tin không đếm sau `rs`.
