# Tổng hợp phản biện vòng 1 — cơ chế hệ thống

> Ngày: 2026-10-04 · Trạng thái: **chờ phản biện vòng 2** · Bản lưu cho owner, không gửi reviewer.
> Nguồn: [báo cáo gốc](261004-system-mechanisms-report.md) · [phản biện CL](261004-system-mechanisms-review-cl.md) · [phản biện GE](261004-system-mechanisms-review-ge.md) · Vòng 2: [gửi CL](261004-system-mechanisms-round2-cl.md), [gửi GE](261004-system-mechanisms-round2-ge.md).

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

## Câu hỏi còn mở

- Số room trung bình mỗi user (cần cho 2.D). Chưa có số; file vòng 2 để reviewer tự giả định và nêu rõ.
